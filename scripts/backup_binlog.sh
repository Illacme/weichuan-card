#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# MySQL binlog 归档脚本（PITR 的「后半段」）。
#
# 为什么需要它：scripts/backup.sh 做的是**全量逻辑备份**，恢复点 = 备份时刻。
# 凌晨 3 点备份、下午 4 点误删了一张表，全量备份只能把你送回凌晨 3 点——
# 中间 13 小时的数据没了。binlog 记的就是这 13 小时里发生过的每一次写，
# 「全量备份 + binlog 回放」才能把数据恢复到**误操作前一秒**（PITR）。
#
# 本脚本做的事（顺序不能换）：
#   1. FLUSH BINARY LOGS  —— 滚动出一个新的 binlog，当前正在写的那个就此封口，
#      于是「已经写完的文件」可以安全复制（复制活跃中的 binlog 会拿到半截文件）。
#   2. 复制已封口的 binlog 到归档目录并 gzip，逐个 gzip -t 校验
#      —— 没校验就 PURGE 等于「先删源、后知副本坏」，那归档就是自欺欺人。
#   3. PURGE BINARY LOGS TO <最后一个已归档文件>  —— 释放数据库磁盘。
#      只在第 2 步全部成功之后才执行。
#   4. 删除超过 BINLOG_KEEP_DAYS 天的本地归档。
#
# 用法：
#   scripts/backup_binlog.sh                      # 默认容器 weichuan-mysql
#   scripts/backup_binlog.sh --dry-run            # 只打印将做什么，不动文件
#   DB_CONTAINER=my-mysql DB_USER=root DB_PASSWORD=secret scripts/backup_binlog.sh
#   BINLOG_ARCHIVE_DIR=/data/binlog BINLOG_KEEP_DAYS=14 scripts/backup_binlog.sh
#
# 依赖：docker（mysql 客户端在容器内，宿主机不需要装）。
# 前置条件：**数据库必须开启了 binlog**（本项目 compose 的 mysql 服务默认已开，
# 见 docker-compose.yml 里 mysqld 的 --log-bin 参数）。没开的话本脚本会直接报错退出，
# 而不是假装成功——「以为有 PITR 其实没有」比「知道没有」危险得多。
#
# 建议 crontab（每小时一次，binlog 归档要密一些，否则一次要拷很多）：
#   0 * * * * cd /path/to/repo && scripts/backup_binlog.sh >> /var/log/weichuan-binlog.log 2>&1
#
# 恢复怎么做（演练前先通读一遍 docs/deploy-runbook.md §9）：
#   1. 起一个临时 MySQL，用最近一份全量备份恢复（scripts/restore.sh）；
#   2. mysqlbinlog 回放从「备份时刻」到「误操作前一刻」的 binlog；
#   3. 核对数据后再切流。**没有演练过的恢复步骤等于没有**——请每季度用
#      scripts/restore_drill.sh 真跑一遍。
# ─────────────────────────────────────────────────────────────
set -euo pipefail

DB_CONTAINER="${DB_CONTAINER:-weichuan-mysql}"
DB_USER="${DB_USER:-root}"
DB_PASSWORD="${DB_PASSWORD:-root}"
BINLOG_ARCHIVE_DIR="${BINLOG_ARCHIVE_DIR:-./backups/binlog}"
BINLOG_KEEP_DAYS="${BINLOG_KEEP_DAYS:-7}"
DRY_RUN=0

for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    -h|--help) sed -n '2,40p' "$0"; exit 0 ;;
    *) echo "[binlog] 未知参数：${arg}（可选 --dry-run / --help）" >&2; exit 2 ;;
  esac
done

log()  { echo "[binlog] $*"; }
fail() { echo "[binlog] FAILED: $*" >&2; exit 1; }

command -v docker >/dev/null 2>&1 || fail "需要 docker（mysql 客户端在容器内执行）"

# 容器内执行 mysql：走 -N -B 拿纯文本结果，口令用 MYSQL_PWD 传（不出现在 ps / 命令行里）
mysql_q() {
  docker exec -e MYSQL_PWD="$DB_PASSWORD" "$DB_CONTAINER" \
    mysql -u"$DB_USER" -N -B -e "$1"
}

docker inspect "$DB_CONTAINER" >/dev/null 2>&1 || \
  fail "容器 $DB_CONTAINER 不存在（MySQL 形态需 docker compose --profile mysql up）"

# ── 0. 确认 binlog 已开启 ──
# 这一步不能省：log_bin=OFF 时下面所有步骤都会「安静地什么也没做」，
# 运维以为自己在做 PITR，直到真出事才发现归档目录一直是空的。
LOG_BIN="$(mysql_q "SHOW VARIABLES LIKE 'log_bin';" | awk '{print $2}' | tail -1 || true)"
if [ "$LOG_BIN" != "ON" ]; then
  fail "数据库未开启 binlog（log_bin=${LOG_BIN:-取不到}）：无法做时间点恢复。
       在 mysqld 启动参数上加 --log-bin=/var/lib/mysql/mysql-bin --server-id=1
       （本项目 compose 已默认带上），重启后再跑本脚本。"
fi

# binlog 文件在服务器上的目录（datadir 下的相对/绝对路径都可能）
BINLOG_DIR="$(mysql_q "SELECT @@log_bin_basename;" 2>/dev/null | tail -1 || true)"
BINLOG_DIR="$(dirname "${BINLOG_DIR:-}")"
[ -n "$BINLOG_DIR" ] && [ "$BINLOG_DIR" != "." ] || \
  fail "取不到 binlog 目录（@@log_bin_basename 为空）：binlog 可能未启用"

# ── 1. 滚动：让当前正在写的 binlog 封口 ──
log "FLUSH BINARY LOGS（滚动出一个新文件，已写完的才能安全复制）"
[ "$DRY_RUN" -eq 1 ] || mysql_q "FLUSH BINARY LOGS;" >/dev/null

# ── 2. 列出待归档文件（排除最后一个 = 正在写的那个）──
# SHOW BINARY LOGS 输出两列：Log_name File_size；最后一行是活跃的。
ALL_LOGS="$(mysql_q "SHOW BINARY LOGS;" | awk '{print $1}' | grep -v '^Log_name$' || true)"
[ -n "$ALL_LOGS" ] || fail "SHOW BINARY LOGS 返回空：binlog 未启用或权限不足"

ACTIVE="$(printf '%s\n' "$ALL_LOGS" | tail -1)"
TO_ARCHIVE="$(printf '%s\n' "$ALL_LOGS" | head -n -1 || true)"
if [ -z "$TO_ARCHIVE" ]; then
  log "没有已封口的 binlog（只有活跃的 ${ACTIVE}），本次无需归档"
  exit 0
fi

STAMP="$(date +%Y%m%d-%H%M%S)"
DEST="$BINLOG_ARCHIVE_DIR/$STAMP"
log "待归档 $(printf '%s\n' "$TO_ARCHIVE" | wc -l | tr -d ' ') 个文件 -> ${DEST}（活跃文件 $ACTIVE 保留在服务器）"
if [ "$DRY_RUN" -eq 1 ]; then
  printf '%s\n' "$TO_ARCHIVE"
  log "--dry-run：到此为止（未复制、未 PURGE）"
  exit 0
fi

mkdir -p "$DEST"
LAST_OK=""
while IFS= read -r f; do
  [ -n "$f" ] || continue
  src="$BINLOG_DIR/$f"
  out="$DEST/$f.gz"
  # 从容器里拷出再压缩：直接 docker exec gzip 会把压缩过程压在数据库容器上。
  tmp="$(mktemp "${TMPDIR:-/tmp}/weichuan-binlog-XXXXXX")"
  if ! docker cp "$DB_CONTAINER:$src" "$tmp" >/dev/null 2>&1; then
    rm -f "$tmp"
    fail "复制 $f 失败（docker cp）：已归档的文件仍保留在 ${DEST}，服务器数据未 PURGE"
  fi
  gzip -c "$tmp" > "$out"
  rm -f "$tmp"
  # 校验：归档坏了自己不知道，等恢复那天才知道，那时源文件早被 PURGE 了
  gzip -t "$out" || fail "归档 $out 校验失败：保留服务器上的 binlog 不 PURGE"
  LAST_OK="$f"
  log "archived $f -> $out ($(du -h "$out" | cut -f1))"
done <<< "$TO_ARCHIVE"

[ -n "$LAST_OK" ] || fail "没有任何文件归档成功"

# ── 3. 释放服务器磁盘：只删到最后一个归档成功的为止 ──
# 用 TO（而不是 BEFORE 某个时间）：TO 的语义是「删到该文件之前」，
# 保留的文件一定包含已归档的全部数据，不会漏。
log "PURGE BINARY LOGS TO '$LAST_OK'（已全部归档并校验）"
mysql_q "PURGE BINARY LOGS TO '$LAST_OK';" >/dev/null

# ── 4. 本地归档轮转 ──
if [ -d "$BINLOG_ARCHIVE_DIR" ]; then
  find "$BINLOG_ARCHIVE_DIR" -mindepth 1 -maxdepth 1 -type d -mtime +"$BINLOG_KEEP_DAYS" -print -exec rm -rf {} +
fi

log "done (archive=$DEST, keep ${BINLOG_KEEP_DAYS} days)"
