#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# 数据库恢复脚本（配套 scripts/backup.sh）。
#
#   scripts/restore.sh backups/weichuan-20261025-030000.db.gz            # SQLite，恢复生产
#   scripts/restore.sh backups/weichuan-20261025-030000.sql.gz --yes     # MySQL，不交互
#   scripts/restore.sh backups/xxx.db.gz --target /tmp/drill.db          # 只还原到指定文件（演练）
#
# 选项：
#   --yes               跳过交互确认（脚本化 / 演练；生产恢复请勿省略）
#   --target <路径>     恢复到指定文件，不碰生产容器（演练专用）
#   --no-safety         跳过恢复前的自动备份（不推荐：恢复错了就没有退路）
#   --driver mysql|sqlite  强制指定形态（默认按文件后缀判断）
#
# 为什么要「恢复前先自动备份」：恢复是**破坏性**操作，把一份两周前的备份
# 盖上去，等于丢掉这两周的全部数据。安全网不贵（一次 backup.sh），
# 而没有它的代价是不可逆的。
#
# 为什么 SQLite 恢复要删 -wal / -shm：库文件被替换后，旧 WAL 里还是上一个库
# 的页；SQLite 打开时会尝试回放它，结果是把旧数据「恢复」进新库——比不恢复更糟。
# 删掉是最省事也最安全的做法（WAL 本来就是可丢弃的临时结构）。
#
# 恢复完成后一定要看校验结果；校验不过就按最后打印的回滚指引退回安全网那份。
# 演练请用 scripts/restore_drill.sh（自动跑一遍并在临时库上比对，不碰生产）。
# ─────────────────────────────────────────────────────────────
set -euo pipefail

APP_CONTAINER="${APP_CONTAINER:-weichuan-backend}"
DB_CONTAINER="${DB_CONTAINER:-weichuan-mysql}"
DB_USER="${DB_USER:-root}"
DB_PASSWORD="${DB_PASSWORD:-root}"
DB_NAME="${DB_NAME:-weichuan}"
BACKUP_DIR="${BACKUP_DIR:-./backups}"
# SQLite 库在容器内的路径（安装向导写 DB_DSN 时的默认值，见 internal/setup）
SQLITE_INNER_PATH="${SQLITE_INNER_PATH:-/app/data/weichuan.db}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:8080/healthz/ready}"

BACKUP_FILE=""
ASSUME_YES=0
TARGET=""
SAFETY=1
DRIVER=""

usage() {
  # 只打头部注释：跳过 shebang 与开头的分隔线，遇到结尾分隔线就停
  # （早先用固定行号，改动注释后就会把 set -euo 也打出来）
  awk 'NR<=2{next} /^# ─/{exit} /^#/{sub(/^# ?/,""); print}' "$0"
}

die() { echo "[restore][FATAL] $*" >&2; exit 1; }
info() { echo "[restore] $*"; }

while [ $# -gt 0 ]; do
  case "$1" in
    -y|--yes)      ASSUME_YES=1; shift ;;
    --no-safety)   SAFETY=0; shift ;;
    --target)      TARGET="${2:-}"; [ -n "$TARGET" ] || die "--target 需要一个路径"; shift 2 ;;
    --driver)      DRIVER="${2:-}"; shift 2 ;;
    -h|--help)     usage; exit 0 ;;
    -*)            die "未知参数 $1（--help 看用法）" ;;
    *)             BACKUP_FILE="$1"; shift ;;
  esac
done

[ -n "$BACKUP_FILE" ] || { usage; exit 2; }
[ -f "$BACKUP_FILE" ] || die "备份文件不存在：$BACKUP_FILE"

# ── 形态判定 ──
if [ -z "$DRIVER" ]; then
  case "$BACKUP_FILE" in
    *.sql.gz|*.sql) DRIVER="mysql" ;;
    *.db.gz|*.db)   DRIVER="sqlite" ;;
    *) die "无法从文件名判断形态（${BACKUP_FILE}）；请用 --driver mysql|sqlite 指定" ;;
  esac
fi
case "$DRIVER" in mysql|sqlite) ;; *) die "未知 --driver=$DRIVER" ;; esac

# ── 解压到临时文件（先落地再恢复：管道中断会留下半个库）──
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/weichuan-restore-XXXXXX")"
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

if [ "${BACKUP_FILE##*.}" = "gz" ]; then
  command -v gzip >/dev/null 2>&1 || die "需要 gzip 解压"
  gzip -t "$BACKUP_FILE" 2>/dev/null || die "备份文件已损坏（gzip -t 未通过）：$BACKUP_FILE"
  PLAIN="$TMP_DIR/payload"
  gzip -dc "$BACKUP_FILE" > "$PLAIN" || die "解压失败"
else
  PLAIN="$BACKUP_FILE"
fi

# ── 安全网：先把「当前」的库备份一份 ──
SAFETY_DIR=""
if [ "$SAFETY" = "1" ] && [ -z "$TARGET" ]; then
  SAFETY_DIR="$BACKUP_DIR/pre-restore-$(date +%Y%m%d-%H%M%S)"
  mkdir -p "$SAFETY_DIR"
  info "恢复前先备份当前库 → $SAFETY_DIR"
  if ! BACKUP_DIR="$SAFETY_DIR" DB_DRIVER="$DRIVER" "$(dirname "$0")/backup.sh"; then
    die "恢复前备份失败，已中止（不带着「没有退路」的风险做破坏性操作）。确需继续请加 --no-safety"
  fi
fi

# ── 确认 ──
target_desc="$TARGET"
if [ -z "$target_desc" ]; then
  if [ "$DRIVER" = "sqlite" ]; then
    target_desc="容器 $APP_CONTAINER 的 ${SQLITE_INNER_PATH}（生产库）"
  else
    target_desc="容器 $DB_CONTAINER 的数据库 ${DB_NAME}（生产库）"
  fi
fi
if [ "$ASSUME_YES" != "1" ]; then
  echo
  echo "  备份文件 : $BACKUP_FILE"
  echo "  形态     : $DRIVER"
  echo "  恢复到   : $target_desc"
  [ -n "$SAFETY_DIR" ] && echo "  安全网   : $SAFETY_DIR"
  echo
  printf '确认覆盖？这一步不可撤销，输入 yes 继续：'
  read -r ans
  [ "$ans" = "yes" ] || { info "已取消"; exit 1; }
fi

# ── 校验工具：优先用 SERVER_BIN（原生部署 / 本机演练），否则进容器执行 ──
# 两条路走同一份二进制、同一套校验，不会出现「容器里过了、本机不过」的分歧。
run_server() {
  if [ -n "${SERVER_BIN:-}" ]; then
    "$SERVER_BIN" "$@"
  else
    docker exec "$APP_CONTAINER" /app/server "$@"
  fi
}
dbcheck() {
  local file="$1" field="$2"
  run_server dbcheck --file "$file" --field "$field" 2>/dev/null
}

verify_sqlite_file() {
  local file="$1"
  [ -s "$file" ] || die "恢复产物是空文件：$file"
  # 魔数：SQLite 文件头固定 16 字节，前 15 字节是 "SQLite format 3" + \0
  local magic
  magic="$(head -c 15 "$file" | tr -d '\0')"
  [ "$magic" = "SQLite format 3" ] || die "恢复产物不是 SQLite 库（文件头不符）"
  info "文件头 OK（SQLite format 3）"
}

# ═══════════════ SQLite ═══════════════
restore_sqlite() {
  verify_sqlite_file "$PLAIN"

  # 演练 / 指定目标：只写文件，不碰容器
  if [ -n "$TARGET" ]; then
    mkdir -p "$(dirname "$TARGET")"
    cp "$PLAIN" "$TARGET"
    info "已写入 $TARGET"
    local integrity tables
    if integrity="$(dbcheck "$TARGET" integrity)" && [ -n "$integrity" ]; then
      [ "$integrity" = "ok" ] || die "完整性检查未通过：$integrity"
      tables="$(dbcheck "$TARGET" tables || echo '?')"
      info "校验通过：integrity=ok, tables=$tables"
    else
      info "跳过深度校验（容器内 dbcheck 不可用；已验文件头）"
    fi
    return 0
  fi

  command -v docker >/dev/null 2>&1 || die "需要 docker（恢复生产库要操作容器）"
  docker inspect "$APP_CONTAINER" >/dev/null 2>&1 || die "找不到容器 $APP_CONTAINER"

  # 定位 /app/data 的挂载：容器停止后无法 exec，只能借一个一次性容器挂同一个卷/目录
  local mount type name source image
  mount="$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/app/data"}}{{.Type}}|{{.Name}}|{{.Source}}{{end}}{{end}}' "$APP_CONTAINER")"
  [ -n "$mount" ] || die "容器 $APP_CONTAINER 没有把 /app/data 挂出来——库文件不持久，先修部署再谈恢复"
  type="${mount%%|*}"; name="$(printf '%s' "$mount" | cut -d'|' -f2)"; source="$(printf '%s' "$mount" | cut -d'|' -f3)"
  image="$(docker inspect -f '{{.Config.Image}}' "$APP_CONTAINER")"

  info "停止 $APP_CONTAINER ..."
  docker stop "$APP_CONTAINER" >/dev/null

  local inner="${SQLITE_INNER_PATH#/app/data/}"
  local mnt_flag
  if [ "$type" = "volume" ] && [ -n "$name" ]; then
    mnt_flag="-v ${name}:/data"
  else
    mnt_flag="-v ${source}:/data"
  fi

  # 一次性容器完成三件事：删旧 WAL（必须）→ 覆盖库文件 → 校验
  # 用后端自己的镜像，不额外拉 alpine，也不依赖宿主机权限。
  info "写入并校验（借一次性容器挂载 /app/data）..."
  if ! docker run --rm $mnt_flag -v "$PLAIN:/restore-payload:ro" --entrypoint sh "$image" -c \
      "set -e; rm -f '/data/$inner-wal' '/data/$inner-shm'; cp /restore-payload '/data/$inner'; ls -l '/data/$inner'"; then
    docker start "$APP_CONTAINER" >/dev/null 2>&1 || true
    die "写入失败；容器已尝试重新启动，库文件可能仍是原样。请检查上面的错误"
  fi

  info "启动 $APP_CONTAINER ..."
  docker start "$APP_CONTAINER" >/dev/null

  # 等服务起来再体检：库是坏的，进程会连不上库、/healthz 返回 503
  local i=0
  while [ "$i" -lt 30 ]; do
    if curl -fsS -m 3 -o /dev/null "$HEALTH_URL" 2>/dev/null; then break; fi
    sleep 1; i=$((i + 1))
  done
  if [ "$i" -ge 30 ]; then
    echo "[restore][WARN] 30 秒内 /healthz 未恢复 2xx：库可能不可用，或服务还在迁移/初始化" >&2
    echo "[restore][WARN] 排查：docker logs --tail 50 $APP_CONTAINER" >&2
  else
    info "服务已恢复（$HEALTH_URL 返回 2xx）"
  fi

  local integrity tables
  if integrity="$(dbcheck "$SQLITE_INNER_PATH" integrity)" && [ -n "$integrity" ]; then
    [ "$integrity" = "ok" ] || die "恢复后完整性检查未通过：$integrity"
    tables="$(dbcheck "$SQLITE_INNER_PATH" tables || echo '?')"
    info "校验通过：integrity=ok, tables=$tables"
  else
    info "跳过深度校验（dbcheck 不可用）；请手动确认后台能登录、列表有数据"
  fi
}

# ═══════════════ MySQL ═══════════════
restore_mysql() {
  command -v docker >/dev/null 2>&1 || die "需要 docker（恢复通过容器内 mysql 客户端执行）"
  docker inspect "$DB_CONTAINER" >/dev/null 2>&1 || die "找不到容器 $DB_CONTAINER"

  info "导入到 $DB_CONTAINER/$DB_NAME ..."
  # mysqldump 默认带 DROP TABLE IF EXISTS，导入即替换表结构，无需先清库
  if ! docker exec -i -e MYSQL_PWD="$DB_PASSWORD" "$DB_CONTAINER" \
      mysql -u"$DB_USER" --default-character-set=utf8mb4 "$DB_NAME" < "$PLAIN"; then
    die "导入失败（上面的 mysql 报错为准）；库可能处于半导入状态，请用安全网那份重来"
  fi

  local tables
  tables="$(docker exec -e MYSQL_PWD="$DB_PASSWORD" "$DB_CONTAINER" \
    mysql -N -B -u"$DB_USER" -e \
    "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='$DB_NAME'")"
  [ "${tables:-0}" -gt 0 ] || die "导入后一张表都没有，恢复失败"
  info "校验通过：tables=$tables"

  local ver
  ver="$(docker exec -e MYSQL_PWD="$DB_PASSWORD" "$DB_CONTAINER" \
    mysql -N -B -u"$DB_USER" "$DB_NAME" -e \
    "SELECT MAX(version) FROM schema_migrations" 2>/dev/null || true)"
  [ -n "$ver" ] && info "schema_migrations 最新版本：$ver"
}

if [ "$DRIVER" = "sqlite" ]; then
  restore_sqlite
else
  restore_mysql
fi

# ── 回滚指引 ──
echo
info "完成。"
if [ -n "$SAFETY_DIR" ]; then
  info "如需回滚：scripts/restore.sh $SAFETY_DIR/*.gz --yes"
fi
info "建议接着跑一次 scripts/restore_drill.sh 确认备份链路整体可用。"
