#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# 文件资产 + 运行时密钥备份（配套 scripts/backup.sh）。
#
# 为什么数据库备份之外还要这一份：库救不回两类东西——
#   1. uploads/ 里的图片（头像、案例图、海报素材）。库恢复了，图全 404，
#      名片页满屏裂图，和没恢复差不多。
#   2. /app/data/runtime.env 里的 PII_ENC_KEY。员工姓名 / 手机 / 邮箱是
#      应用层 AES-256-GCM 加密落库的（见 internal/pii），**没有这把钥匙，
#      备份里的 PII 就是一堆解不开的密文**；本项目没有 re-key 机制，
#      换钥即永久失读。密钥丢了 = 数据等于丢了。
#
# 密钥包必须加密后再落盘：如果 runtime.env 明文躺在备份盘上，等于把
# 「库 + 钥匙」放在一起——备份盘泄露就是大规模个人信息泄露，比不备份更糟。
# 所以这里产出 secrets-*.tar.gz.enc（口令来自 BACKUP_ENC_KEY），而口令本身
# 由运维**另行保管**（密码管理器 / 封存），绝不与备份放在同一台机器上。
#
# 用法：
#   scripts/backup_files.sh                          # 自动识别容器 / 宿主
#   BACKUP_ENC_KEY=xxx scripts/backup_files.sh       # 加密密钥包（生产必须）
#   BACKUP_SOURCE=host UPLOAD_DIR=./uploads scripts/backup_files.sh
#   scripts/backup.sh                                # 通常不用单独跑：DB 备份会带上它
#
# 环境变量：
#   BACKUP_DIR       备份目录，默认 ./backups（与 backup.sh 一致）
#   KEEP_DAYS        保留天数，默认 7（与 backup.sh 一致）
#   APP_CONTAINER    应用容器名，默认 weichuan-backend
#   BACKUP_SOURCE    auto（默认，容器在就用容器）/ container / host
#   UPLOAD_DIR       宿主上的上传目录，默认 ./uploads（host 模式）
#   CONFIG_FILE      宿主上的运行时配置，默认 ./data/runtime.env
#   LICENSE_FILE     宿主上的许可证，默认 ./data/license.key
#   BACKUP_ENC_KEY   密钥包口令（openssl rand -base64 24 生成）
#   BACKUP_ALLOW_NO_KEY=1  明确接受「密钥不进备份」的逃生阀（会打 WARN）
#
# 失败即失败：任何一步出错都会删掉本次已产出的半成品并以非零退出。
# 一份「看起来新鲜、其实缺件」的备份比没有备份更危险——告警会因此闭嘴。
# ─────────────────────────────────────────────────────────────
set -euo pipefail

BACKUP_DIR="${BACKUP_DIR:-./backups}"
KEEP_DAYS="${KEEP_DAYS:-7}"
APP_CONTAINER="${APP_CONTAINER:-weichuan-backend}"
BACKUP_SOURCE="${BACKUP_SOURCE:-auto}"
UPLOAD_DIR="${UPLOAD_DIR:-./uploads}"
CONFIG_FILE="${CONFIG_FILE:-./data/runtime.env}"
LICENSE_FILE="${LICENSE_FILE:-./data/license.key}"
BACKUP_ENC_KEY="${BACKUP_ENC_KEY:-}"
BACKUP_ALLOW_NO_KEY="${BACKUP_ALLOW_NO_KEY:-0}"
# 口令要能被 openssl 的 -pass env: 读到，显式导出（bash 会保留原有的 export 属性，
# 但显式写出来才不会被后续改动悄悄弄丢）
export BACKUP_ENC_KEY

# 容器内的固定路径，与 docker-compose.yml 的 UPLOAD_DIR / CONFIG_FILE 一致
C_UPLOAD_DIR="/app/uploads"
C_DATA_DIR="/app/data"

STAMP="$(date +%Y%m%d-%H%M%S)"
CREATED=()

log()  { echo "[backup] $*"; }
warn() { echo "[backup][WARN] $*" >&2; }

cleanup_partial() {
  local f
  for f in "${CREATED[@]:-}"; do
    [ -n "$f" ] && rm -f "$f"
  done
  CREATED=()
}

die() {
  echo "[backup][FATAL] $*" >&2
  cleanup_partial
  exit 1
}

usage() {
  awk 'NR<=2{next} /^# ─/{exit} /^#/{sub(/^# ?/,""); print}' "$0"
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  "") ;;
  *) echo "[backup][FATAL] 未知参数 $1（--help 看用法）" >&2; exit 2 ;;
esac

# ── 容器 / 宿主：auto 时「容器在就用容器」，这样 compose 部署不必额外配路径 ──
MODE="host"
case "$BACKUP_SOURCE" in
  container) MODE="container" ;;
  host)      MODE="host" ;;
  auto)
    if command -v docker >/dev/null 2>&1 && docker inspect "$APP_CONTAINER" >/dev/null 2>&1; then
      MODE="container"
    fi
    ;;
  *) die "未知 BACKUP_SOURCE=${BACKUP_SOURCE}（可选 auto / container / host）" ;;
esac

mkdir -p "$BACKUP_DIR"

# ═══════════════ 1. 上传目录 ═══════════════
backup_uploads() {
  local out="$BACKUP_DIR/uploads-${STAMP}.tar.gz"

  if [ "$MODE" = "container" ]; then
    if ! docker exec "$APP_CONTAINER" test -d "$C_UPLOAD_DIR" 2>/dev/null; then
      log "容器内无 ${C_UPLOAD_DIR}（可能使用对象存储：文件由云厂商负责持久化与备份），跳过"
      return 0
    fi
  else
    if [ ! -d "$UPLOAD_DIR" ]; then
      log "未发现本地上传目录 ${UPLOAD_DIR}（可能使用对象存储），跳过"
      return 0
    fi
  fi

  log "打包上传目录（${MODE}）..."
  # 先登记再动手：中途失败时 cleanup_partial 会删掉半截归档，
  # 不让一个「看起来正常」的坏包留在备份目录里
  CREATED+=("$out")
  if [ "$MODE" = "container" ]; then
    docker exec "$APP_CONTAINER" tar -czf - -C "$C_UPLOAD_DIR" . > "$out" \
      || die "打包上传目录失败（容器内 tar 非零退出）"
  else
    tar -czf "$out" -C "$UPLOAD_DIR" . \
      || die "打包上传目录失败（${UPLOAD_DIR}）"
  fi

  gzip -t "$out" 2>/dev/null || die "上传目录归档校验失败（gzip -t 未通过）：$out"
  local n
  n="$(tar -tzf "$out" | grep -v '/$' | grep -v '^\.$' | wc -l | tr -d ' ')"
  log "OK -> ${out}（$(du -h "$out" | cut -f1)，文件 ${n} 个）"
}

# ═══════════════ 2. 运行时密钥（加密） ═══════════════
# 先探明「有哪些密钥文件」，再做备份——因为「发现了密钥却没给口令」必须
# 在写任何产物**之前**就失败，否则会留下一份「新鲜但救不回 PII」的备份，
# 而备份新鲜度告警恰恰会被它喂饱、不再响。
SECRET_NAMES=()
SECRET_DIRS=()

probe_secrets() {
  if [ "$MODE" = "container" ]; then
    if docker exec "$APP_CONTAINER" test -f "$C_DATA_DIR/runtime.env" 2>/dev/null; then
      SECRET_NAMES+=("runtime.env")
      SECRET_DIRS+=("$C_DATA_DIR")
    fi
    if docker exec "$APP_CONTAINER" test -f "$C_DATA_DIR/license.key" 2>/dev/null; then
      SECRET_NAMES+=("license.key")
      SECRET_DIRS+=("$C_DATA_DIR")
    fi
  else
    if [ -f "$CONFIG_FILE" ]; then
      SECRET_NAMES+=("$(basename "$CONFIG_FILE")")
      SECRET_DIRS+=("$(cd "$(dirname "$CONFIG_FILE")" && pwd)")
    fi
    if [ -f "$LICENSE_FILE" ]; then
      SECRET_NAMES+=("$(basename "$LICENSE_FILE")")
      SECRET_DIRS+=("$(cd "$(dirname "$LICENSE_FILE")" && pwd)")
    fi
  fi
}

backup_secrets() {
  local n="${#SECRET_NAMES[@]}"

  if [ "$n" -eq 0 ]; then
    log "未发现运行时密钥文件（密钥可能由环境变量注入，由部署侧另行保管），跳过"
    return 0
  fi

  if [ -z "$BACKUP_ENC_KEY" ]; then
    if [ "$BACKUP_ALLOW_NO_KEY" = "1" ]; then
      warn "BACKUP_ALLOW_NO_KEY=1：跳过密钥备份。PII_ENC_KEY 不在备份内——"
      warn "主机损毁后库里的姓名/手机/邮箱将无法解密（没有 re-key 机制）。"
      return 0
    fi
    die "发现密钥文件（${SECRET_NAMES[*]}）但没有 BACKUP_ENC_KEY，拒绝产出一份「救不回 PII」的备份。
  1) 生成口令：openssl rand -base64 24
  2) export BACKUP_ENC_KEY='<上一步的口令>'（写进 crontab 的环境或 root 的受保护文件）
  3) 口令本身请单独保管：密码管理器 / 纸质封存，**不要**和备份放同一台机器
  确需跳过密钥备份（明确接受 PII 永久失读的风险）：BACKUP_ALLOW_NO_KEY=1"
  fi

  command -v openssl >/dev/null 2>&1 || die "需要 openssl（加密密钥包；运行镜像里没有，加密在宿主机做）"

  local out="$BACKUP_DIR/secrets-${STAMP}.tar.gz.enc"
  local i
  local tar_args=(-czf -)
  for i in "${!SECRET_NAMES[@]}"; do
    tar_args+=(-C "${SECRET_DIRS[$i]}" "${SECRET_NAMES[$i]}")
  done

  log "加密打包运行时密钥（${SECRET_NAMES[*]}）..."
  CREATED+=("$out")
  # 容器内只有 busybox（无 openssl），所以「容器里打包 → 宿主机加密」：
  # 明文密钥只走管道，不落宿主机磁盘。
  if [ "$MODE" = "container" ]; then
    docker exec "$APP_CONTAINER" tar "${tar_args[@]}" \
      | openssl enc -aes-256-cbc -pbkdf2 -iter 200000 -salt -pass env:BACKUP_ENC_KEY -out "$out" \
      || die "密钥包加密失败（容器内 tar 或 openssl 非零退出）"
  else
    tar "${tar_args[@]}" \
      | openssl enc -aes-256-cbc -pbkdf2 -iter 200000 -salt -pass env:BACKUP_ENC_KEY -out "$out" \
      || die "密钥包加密失败"
  fi

  # 自检：用同一口令解开并列出成员。口令打错、openssl 参数不兼容（比如换了
  # 一个不认 -pbkdf2 的实现）这类问题，如果不在备份时暴露，就只会在恢复那天
  # 暴露——而那时它已经是你唯一的一份了。
  if ! openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 -pass env:BACKUP_ENC_KEY -in "$out" \
       | tar -tzf - >/dev/null 2>&1; then
    die "密钥包自检失败（用当前 BACKUP_ENC_KEY 解不开）——口令或 openssl 参数有问题，这份包不可用"
  fi

  log "OK -> ${out}（$(du -h "$out" | cut -f1)，含 ${SECRET_NAMES[*]}）"
}

# ═══════════════ 3. 轮转 ═══════════════
rotate() {
  find "$BACKUP_DIR" -name "uploads-*.tar.gz"     -type f -mtime +"$KEEP_DAYS" -print -delete 2>/dev/null || true
  find "$BACKUP_DIR" -name "secrets-*.tar.gz.enc" -type f -mtime +"$KEEP_DAYS" -print -delete 2>/dev/null || true
}

probe_secrets
backup_uploads
backup_secrets
rotate

log "done（文件与密钥，mode=${MODE}，keep ${KEEP_DAYS} 天）"
