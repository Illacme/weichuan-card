#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# 全量备份脚本：数据库 + 上传文件 + 运行时密钥（三件套）。
#
#   DB_DRIVER=mysql （默认）  mysqldump → gzip → 按保留天数轮转
#   DB_DRIVER=sqlite          容器内 `server backup`（VACUUM INTO 一致性快照）→ gzip → 轮转
#   文件与密钥                 转交 scripts/backup_files.sh（uploads-*.tar.gz / secrets-*.tar.gz.enc）
#
# **为什么必须是三件套**：只有数据库备份救不回两类东西——
#   ① uploads/ 里的图片：库恢复了图全 404，名片页满屏裂图；
#   ② /app/data/runtime.env 里的 PII_ENC_KEY：PII 是应用层加密落库的，
#      没有这把钥匙，备份里的姓名/手机就是解不开的密文（无 re-key 机制）。
# 密钥包用 BACKUP_ENC_KEY 加密后落盘，口令由运维另行保管——详见 backup_files.sh 头部。
#
# 用法：
#   BACKUP_ENC_KEY='...' scripts/backup.sh              # 生产标准用法（加密密钥包）
#   scripts/backup.sh                                  # MySQL，默认容器名 weichuan-mysql
#   DB_DRIVER=sqlite scripts/backup.sh                 # SQLite（默认形态），容器名 weichuan-backend
#   DB_CONTAINER=my-mysql DB_USER=root DB_PASSWORD=secret DB_NAME=weichuan scripts/backup.sh
#   BACKUP_DIR=/data/backups KEEP_DAYS=14 scripts/backup.sh
#   BACKUP_SKIP_FILES=1 scripts/backup.sh              # 只备库（不建议：会缺图与密钥）
#
# 一致性：任何一件失败都会删掉本次已产出的全部产物并非零退出。
# 不让「新鲜但缺件」的备份留在目录里——那会让备份新鲜度告警闭嘴。
#
# 为什么 SQLite 不能直接 cp 库文件：库开着 WAL，cp 出来的是「主文件 + 未合并的
# WAL」的撕裂快照，恢复回来是坏库（integrity_check 直接不通过）。`VACUUM INTO`
# 在读事务内完成，产物是干净单文件；它需要 sqlite3 命令行工具，而运行镜像只装了
# tzdata，所以走二进制自带的 `server backup` 子命令——非容器部署也能用。
#
# 依赖：docker（两种形态都用容器内工具，宿主机不需要装 mysqldump / sqlite3）。
# 建议 crontab（每天 3 点）：
#   0 3 * * * cd /path/to/repo && scripts/backup.sh >> /var/log/weichuan-backup.log 2>&1
#
# 注意：逻辑备份不含二进制日志，恢复点 = 备份时刻；需要 PITR（时间点恢复）
# 须另行开启 binlog 并定期备份 binlog 文件。
#
# 恢复：scripts/restore.sh；演练：scripts/restore_drill.sh（建议每季度跑一次）。
# ─────────────────────────────────────────────────────────────
set -euo pipefail

DB_DRIVER="${DB_DRIVER:-mysql}"
DB_CONTAINER="${DB_CONTAINER:-weichuan-mysql}"
APP_CONTAINER="${APP_CONTAINER:-weichuan-backend}"
DB_USER="${DB_USER:-root}"
DB_PASSWORD="${DB_PASSWORD:-root}"
DB_NAME="${DB_NAME:-weichuan}"
BACKUP_DIR="${BACKUP_DIR:-./backups}"
KEEP_DAYS="${KEEP_DAYS:-7}"
BACKUP_SKIP_FILES="${BACKUP_SKIP_FILES:-0}"
BACKUP_ENC_KEY="${BACKUP_ENC_KEY:-}"
export BACKUP_ENC_KEY

# 本次产出的数据库备份路径：文件备份失败时要把这个也删掉（见文末）。
DB_OUT=""

case "$DB_DRIVER" in
  mysql|sqlite) ;;
  *) echo "[backup] 未知 DB_DRIVER=${DB_DRIVER}（可选 mysql / sqlite）" >&2; exit 2 ;;
esac

command -v docker >/dev/null 2>&1 || { echo "[backup] 需要 docker（备份在容器内执行）" >&2; exit 1; }

mkdir -p "$BACKUP_DIR"
STAMP="$(date +%Y%m%d-%H%M%S)"

# ── MySQL：mysqldump 逻辑备份 ──
# --single-transaction：InnoDB 一致性快照，不锁表（业务无感）
# --quick：逐行写出，避免大表撑爆内存
# --routines --events：含存储过程 / 事件（本项目未用，留着无害）
# --default-character-set=utf8mb4：与建表字符集一致，避免乱码
backup_mysql() {
  local out="$BACKUP_DIR/${DB_NAME}-${STAMP}.sql.gz"
  echo "[backup] $STAMP dumping ${DB_NAME} from container ${DB_CONTAINER} ..."
  if docker exec -e MYSQL_PWD="$DB_PASSWORD" "$DB_CONTAINER" \
    mysqldump -u"$DB_USER" --single-transaction --quick --routines --events \
    --default-character-set=utf8mb4 "$DB_NAME" | gzip > "$out"; then
    DB_OUT="$out"
    echo "[backup] OK -> $out ($(du -h "$out" | cut -f1))"
  else
    echo "[backup] FAILED (docker exec mysqldump 非零退出)" >&2
    rm -f "$out"
    exit 1
  fi
}

# ── SQLite：容器内 VACUUM INTO 一致性快照 ──
# 三步都不能省：容器内先做快照 → 就地体检（integrity_check）→ 才拷出来。
# 少了体检，一次「快照生成成功但文件是坏的」会安静地躺进备份目录，
# 直到真出事那天才发现——而那时它是唯一的一份。
backup_sqlite() {
  local out="$BACKUP_DIR/${DB_NAME}-${STAMP}.db.gz"
  local inner="/tmp/weichuan-snapshot-$$.db"
  local tmp_local integrity

  echo "[backup] $STAMP snapshotting SQLite db from container ${APP_CONTAINER} ..."
  if ! docker exec "$APP_CONTAINER" /app/server backup --out "$inner"; then
    echo "[backup] FAILED (容器内 server backup 非零退出：看上面 [BACKUP][FATAL] 的原因)" >&2
    exit 1
  fi

  integrity="$(docker exec "$APP_CONTAINER" /app/server dbcheck --file "$inner" --field integrity 2>/dev/null || true)"
  if [ "$integrity" != "ok" ]; then
    docker exec "$APP_CONTAINER" rm -f "$inner" >/dev/null 2>&1 || true
    echo "[backup] FAILED (快照完整性检查未通过：${integrity:-空/取不到})" >&2
    exit 1
  fi

  tmp_local="$(mktemp "${TMPDIR:-/tmp}/weichuan-backup-XXXXXX")"
  # shellcheck disable=SC2064  # 立刻展开 $$：退出时才知道要删哪个临时文件
  trap "rm -f '$tmp_local'" EXIT
  docker cp "$APP_CONTAINER:$inner" "$tmp_local" >/dev/null
  docker exec "$APP_CONTAINER" rm -f "$inner" >/dev/null 2>&1 || true

  gzip -c "$tmp_local" > "$out"
  DB_OUT="$out"
  echo "[backup] OK -> $out ($(du -h "$out" | cut -f1))"
}

if [ "$DB_DRIVER" = "sqlite" ]; then
  backup_sqlite
else
  backup_mysql
fi

# ── 文件资产 + 运行时密钥 ──
# 交给 backup_files.sh（同一套 BACKUP_DIR / KEEP_DAYS / APP_CONTAINER）。
# 它失败 = 这次备份不完整，所以把刚生成的库备份也删掉：
# 留着它会让 alert_check 的「备份新鲜度」看到一份新文件、从而不再告警，
# 而实际上这份备份恢复出来是「缺图 + PII 解不开」。
if [ "$BACKUP_SKIP_FILES" = "1" ]; then
  echo "[backup][WARN] BACKUP_SKIP_FILES=1：本次未备份上传文件与运行时密钥" >&2
  echo "[backup][WARN] 缺这两样，恢复出来会是「图片 404 + PII 解不开」，仅建议在排查时使用" >&2
else
  # 显式传 APP_CONTAINER / BACKUP_SOURCE：它们在本脚本里是普通变量（未 export），
  # 不显式传的话子脚本会用默认容器名——「库从 A 容器来、文件去 B 容器找」，
  # 备份看着成功，恢复时才发现少一半。
  if ! BACKUP_DIR="$BACKUP_DIR" KEEP_DAYS="$KEEP_DAYS" \
       APP_CONTAINER="$APP_CONTAINER" BACKUP_SOURCE="${BACKUP_SOURCE:-auto}" \
       "$(dirname "$0")/backup_files.sh"; then
    echo "[backup][FATAL] 文件/密钥备份失败——删除本次库备份，避免留下「新鲜但缺件」的产物" >&2
    [ -n "$DB_OUT" ] && rm -f "$DB_OUT"
    exit 1
  fi
fi

# 轮转：删除超过保留天数的旧备份（两种形态各自一套通配，互不影响）
if [ -d "$BACKUP_DIR" ]; then
  find "$BACKUP_DIR" -name "${DB_NAME}-*.sql.gz" -type f -mtime +"$KEEP_DAYS" -print -delete
  find "$BACKUP_DIR" -name "${DB_NAME}-*.db.gz" -type f -mtime +"$KEEP_DAYS" -print -delete
fi

echo "[backup] done (driver=${DB_DRIVER}, keep ${KEEP_DAYS} days)"
