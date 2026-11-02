#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# 文件资产 + 运行时密钥恢复（配套 scripts/backup_files.sh）。
#
#   scripts/restore_files.sh --all                          # 恢复最新一批（uploads + secrets）
#   scripts/restore_files.sh --uploads backups/uploads-20261102-030000.tar.gz --yes
#   scripts/restore_files.sh --secrets backups/secrets-20261102-030000.tar.gz.enc --yes
#   scripts/restore_files.sh --all --target /tmp/drill      # 只解开到指定目录（演练用，不碰生产）
#
# 选项：
#   --uploads <文件>   恢复上传目录归档（uploads-*.tar.gz）
#   --secrets <文件>   恢复密钥包（secrets-*.tar.gz.enc，需要 BACKUP_ENC_KEY）
#   --all              自动取 BACKUP_DIR 里最新的一批（同一时间戳优先配对）
#   --target <目录>    只解到该目录，不碰容器与生产目录（演练 / 异地检查）
#   --yes              跳过交互确认（脚本化 / 演练；生产恢复请勿省略）
#
# 恢复顺序（灾难恢复，新主机）：
#   1) 先把 stack 起起来，让 compose 建出 uploads_data / data_data 卷；
#   2) scripts/restore.sh <库备份> --yes          恢复数据库
#   3) scripts/restore_files.sh --all --yes       恢复图片与密钥
#   4) 重启 backend，让 runtime.env 生效
# 顺序不能反：先恢复库再恢复密钥，是因为**库里的 PII 密文必须与 runtime.env
# 里的 PII_ENC_KEY 配对**。两者来自不同时期的备份会解不开——本项目没有 re-key。
#
# 覆盖密钥前会自动把当前密钥文件另存到 BACKUP_DIR/pre-restore-secrets-<时间戳>/，
# 恢复错了还有退路（密钥文件很小，留一份几乎不要成本）。
# ─────────────────────────────────────────────────────────────
set -euo pipefail

APP_CONTAINER="${APP_CONTAINER:-weichuan-backend}"
BACKUP_DIR="${BACKUP_DIR:-./backups}"
BACKUP_SOURCE="${BACKUP_SOURCE:-auto}"
UPLOAD_DIR="${UPLOAD_DIR:-./uploads}"
CONFIG_DIR="${CONFIG_DIR:-./data}"
BACKUP_ENC_KEY="${BACKUP_ENC_KEY:-}"
export BACKUP_ENC_KEY

C_UPLOAD_DIR="/app/uploads"
C_DATA_DIR="/app/data"

RESTORE_UPLOADS=""
RESTORE_SECRETS=""
ALL=0
TARGET=""
ASSUME_YES=0

usage() {
  awk 'NR<=2{next} /^# ─/{exit} /^#/{sub(/^# ?/,""); print}' "$0"
}
die()  { echo "[restore-files][FATAL] $*" >&2; exit 1; }
info() { echo "[restore-files] $*"; }
warn() { echo "[restore-files][WARN] $*" >&2; }

while [ $# -gt 0 ]; do
  case "$1" in
    --uploads) RESTORE_UPLOADS="${2:-}"; [ -n "$RESTORE_UPLOADS" ] || die "--uploads 需要一个文件"; shift 2 ;;
    --secrets) RESTORE_SECRETS="${2:-}"; [ -n "$RESTORE_SECRETS" ] || die "--secrets 需要一个文件"; shift 2 ;;
    --all)     ALL=1; shift ;;
    --target)  TARGET="${2:-}"; [ -n "$TARGET" ] || die "--target 需要一个目录"; shift 2 ;;
    -y|--yes)  ASSUME_YES=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数 $1（--help 看用法）" ;;
  esac
done

# ── 形态判定（与 backup_files.sh 同口径：容器在就用容器）──
MODE="host"
case "$BACKUP_SOURCE" in
  container) MODE="container" ;;
  host)      MODE="host" ;;
  auto)
    if command -v docker >/dev/null 2>&1 && docker inspect "$APP_CONTAINER" >/dev/null 2>&1; then
      MODE="container"
    fi
    ;;
  *) die "未知 BACKUP_SOURCE=${BACKUP_SOURCE}" ;;
esac

# ── --all：取最新一批，并尽量让 uploads 与 secrets 来自同一次备份 ──
stamp_of() {
  basename "$1" | sed -nE 's/^(uploads|secrets)-([0-9]{8}-[0-9]{6})\..*/\2/p'
}

if [ "$ALL" = "1" ]; then
  latest_secrets="$(ls -t "$BACKUP_DIR"/secrets-*.tar.gz.enc 2>/dev/null | head -1 || true)"
  latest_uploads="$(ls -t "$BACKUP_DIR"/uploads-*.tar.gz 2>/dev/null | head -1 || true)"

  # 配对：优先与 secrets 同一时间戳的 uploads（同一次运行产出的才保证一致）
  if [ -n "$latest_secrets" ]; then
    stamp="$(stamp_of "$latest_secrets")"
    if [ -n "$stamp" ] && [ -f "$BACKUP_DIR/uploads-${stamp}.tar.gz" ]; then
      latest_uploads="$BACKUP_DIR/uploads-${stamp}.tar.gz"
    fi
  elif [ -n "$latest_uploads" ]; then
    stamp="$(stamp_of "$latest_uploads")"
    if [ -n "$stamp" ] && [ -f "$BACKUP_DIR/secrets-${stamp}.tar.gz.enc" ]; then
      latest_secrets="$BACKUP_DIR/secrets-${stamp}.tar.gz.enc"
    fi
  fi

  [ -n "$latest_uploads" ] && RESTORE_UPLOADS="$latest_uploads"
  [ -n "$latest_secrets" ] && RESTORE_SECRETS="$latest_secrets"

  if [ -z "$RESTORE_UPLOADS" ] && [ -z "$RESTORE_SECRETS" ]; then
    die "$BACKUP_DIR 里没有可恢复的文件备份（先跑 scripts/backup_files.sh）"
  fi
fi

if [ -z "$RESTORE_UPLOADS" ] && [ -z "$RESTORE_SECRETS" ]; then
  usage
  exit 2
fi

# ── 确认 ──
if [ "$ASSUME_YES" != "1" ] && [ -z "$TARGET" ]; then
  echo
  [ -n "$RESTORE_UPLOADS" ] && echo "  上传目录 : $RESTORE_UPLOADS → ${MODE}:${C_UPLOAD_DIR} / host:${UPLOAD_DIR}"
  [ -n "$RESTORE_SECRETS" ] && echo "  运行时密钥: $RESTORE_SECRETS → ${MODE}:${C_DATA_DIR} / host:${CONFIG_DIR}"
  echo "  形态     : $MODE"
  echo
  echo "  注意：恢复密钥会覆盖 runtime.env（含 PII_ENC_KEY / JWT_KEY / ADMIN_PASS）。"
  echo "        PII_ENC_KEY 必须与待恢复的数据库配对，否则库里的姓名/手机解不开。"
  echo
  printf '确认继续？输入 yes：'
  read -r ans
  [ "$ans" = "yes" ] || { info "已取消"; exit 1; }
fi

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/weichuan-restore-files-XXXXXX")"
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

# ═══════════════ 上传目录 ═══════════════
restore_uploads() {
  local file="$1"
  [ -f "$file" ] || die "上传目录归档不存在：$file"
  gzip -t "$file" 2>/dev/null || die "归档已损坏（gzip -t 未通过）：$file"

  local stage="$TMP_DIR/uploads"
  mkdir -p "$stage"
  tar -xzf "$file" -C "$stage" || die "解包失败：$file"
  local n
  n="$(find "$stage" -type f | wc -l | tr -d ' ')"
  info "解包完成：${n} 个文件"

  if [ -n "$TARGET" ]; then
    mkdir -p "$TARGET/uploads"
    cp -R "$stage/." "$TARGET/uploads/"
    info "已解开到 $TARGET/uploads"
    return 0
  fi

  if [ "$MODE" = "container" ]; then
    # docker cp 不会自动创建中间目录：新主机上容器刚建出来时 /app/uploads
    # 可能还不存在（卷由 compose 挂载时才建）。先尽力建一层，
    # 容器已停止时 mkdir 会失败，那就交给下面的 docker cp 报错。
    docker exec "$APP_CONTAINER" mkdir -p "$C_UPLOAD_DIR" >/dev/null 2>&1 || true
    # docker cp 对已停止的容器也有效；目标是挂载卷时会写进卷里
    docker cp "$stage/." "$APP_CONTAINER:$C_UPLOAD_DIR/" >/dev/null \
      || die "写入容器失败（$APP_CONTAINER:$C_UPLOAD_DIR）。常见原因：容器不存在，或卷还没建出来——先 docker compose up -d 起一次让卷就位，再恢复"
    info "已写入 ${APP_CONTAINER}:${C_UPLOAD_DIR}"
  else
    mkdir -p "$UPLOAD_DIR"
    cp -R "$stage/." "$UPLOAD_DIR/"
    info "已写入 $UPLOAD_DIR"
  fi
}

# ═══════════════ 运行时密钥 ═══════════════
restore_secrets() {
  local file="$1"
  [ -f "$file" ] || die "密钥包不存在：$file"
  [ -n "$BACKUP_ENC_KEY" ] || die "需要 BACKUP_ENC_KEY 才能解密密钥包（口令由运维单独保管）"
  command -v openssl >/dev/null 2>&1 || die "需要 openssl"

  local stage="$TMP_DIR/secrets"
  mkdir -p "$stage"
  # 解密失败必须硬停：错口令时 openssl 可能吐出一段垃圾，tar 报错，
  # 若不检查就会把垃圾写进 runtime.env —— 服务下次启动直接读不到配置。
  if ! openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 -pass env:BACKUP_ENC_KEY -in "$file" \
       | tar -xzf - -C "$stage"; then
    die "解密失败（BACKUP_ENC_KEY 不对，或文件损坏）：$file"
  fi
  [ -f "$stage/runtime.env" ] || die "密钥包里没有 runtime.env —— 这不像一份本项目的密钥备份"

  # 至少确认关键项在（不打印值）
  if ! grep -q '^PII_ENC_KEY=' "$stage/runtime.env"; then
    warn "恢复的 runtime.env 里没有 PII_ENC_KEY：若库中的 PII 是加密的，将无法解密"
  fi
  info "解密完成：$(cd "$stage" && ls -1 | tr '\n' ' ')"

  if [ -n "$TARGET" ]; then
    mkdir -p "$TARGET/data"
    cp -R "$stage/." "$TARGET/data/"
    info "已解开到 $TARGET/data"
    return 0
  fi

  # 安全网：当前密钥先另存（很小，留一份几乎不要成本）
  local safety="$BACKUP_DIR/pre-restore-secrets-$(date +%Y%m%d-%H%M%S)"
  mkdir -p "$safety"
  if [ "$MODE" = "container" ]; then
    docker cp "$APP_CONTAINER:$C_DATA_DIR/runtime.env" "$safety/runtime.env" >/dev/null 2>&1 \
      && info "当前 runtime.env 已另存：$safety/runtime.env"
    docker cp "$APP_CONTAINER:$C_DATA_DIR/license.key" "$safety/license.key" >/dev/null 2>&1 || true
  else
    [ -f "$CONFIG_DIR/runtime.env" ] && cp -p "$CONFIG_DIR/runtime.env" "$safety/runtime.env" \
      && info "当前 runtime.env 已另存：$safety/runtime.env"
    [ -f "$CONFIG_DIR/license.key" ] && cp -p "$CONFIG_DIR/license.key" "$safety/" || true
  fi
  # 目标原本就没有密钥文件时，上面只会建出一个空目录——删掉，别在备份目录里留垃圾
  if [ -d "$safety" ] && [ -z "$(ls -A "$safety" 2>/dev/null)" ]; then
    rmdir "$safety" 2>/dev/null || true
  fi

  local f
  for f in "$stage"/*; do
    [ -f "$f" ] || continue
    if [ "$MODE" = "container" ]; then
      docker exec "$APP_CONTAINER" mkdir -p "$C_DATA_DIR" >/dev/null 2>&1 || true
      docker cp "$f" "$APP_CONTAINER:$C_DATA_DIR/$(basename "$f")" >/dev/null \
        || die "写入容器失败：$(basename "$f")（容器不存在或卷未就位？先 docker compose up -d）"
    else
      mkdir -p "$CONFIG_DIR"
      cp "$f" "$CONFIG_DIR/"
    fi
  done
  info "已写入 ${MODE}:${C_DATA_DIR}（host: ${CONFIG_DIR}）"
  warn "runtime.env 是**启动时**读取的：必须重启 backend 才生效（docker compose restart backend）"
}

if [ -n "$RESTORE_UPLOADS" ]; then
  restore_uploads "$RESTORE_UPLOADS"
fi
if [ -n "$RESTORE_SECRETS" ]; then
  restore_secrets "$RESTORE_SECRETS"
fi

echo
info "完成。"
[ -n "$TARGET" ] && info "（--target 模式：只解开文件，未触碰任何服务）"
exit 0
