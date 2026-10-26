#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# 恢复演练（不碰生产数据）。
#
#   scripts/restore_drill.sh                     # 演练最新一份备份
#   scripts/restore_drill.sh backups/xxx.db.gz   # 演练指定备份
#   scripts/restore_drill.sh --keep              # 保留临时产物便于排查
#
# 它回答的问题只有一个：**「现在真出事，这份备份能不能救回来」**。
# 所以它做的不是「文件存在吗」，而是：
#   1. 备份包能不能解开（gzip -t）
#   2. 恢复出来的库能不能打开（integrity_check）
#   3. 表齐不齐（与生产当前库对比，少表 = FAIL）
#   4. 数据新旧（行数差异只报告、不判失败——日备本来就会落后）
#
# SQLite 全程在临时目录里做，不启停容器；MySQL 恢复到临时库
# `weichuan_drill_<时间戳>` 并在结束时删掉。
#
# 退出码：0 全部通过 / 1 有 FAIL（可直接接 CI 或 crontab 月度巡检）/
#         2 环境不完整（没有可用的 dbcheck 或数据库容器，没真正校验过，不算通过）。
# 建议每季度至少手动跑一次，并把输出贴进运维记录——没演练过的备份不算备份。
# ─────────────────────────────────────────────────────────────
set -uo pipefail

APP_CONTAINER="${APP_CONTAINER:-weichuan-backend}"
DB_CONTAINER="${DB_CONTAINER:-weichuan-mysql}"
DB_USER="${DB_USER:-root}"
DB_PASSWORD="${DB_PASSWORD:-root}"
DB_NAME="${DB_NAME:-weichuan}"
BACKUP_DIR="${BACKUP_DIR:-./backups}"

usage() {
  # 只打头部注释：跳过 shebang 与开头的分隔线，遇到结尾分隔线就停
  awk 'NR<=2{next} /^# ─/{exit} /^#/{sub(/^# ?/,""); print}' "$0"
}

KEEP=0
BACKUP_FILE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --keep) KEEP=1; shift ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "[drill][FATAL] 未知参数 $1" >&2; exit 2 ;;
    *) BACKUP_FILE="$1"; shift ;;
  esac
done

# 演练输出请留档（docs/drill-records/），所以脚本在结尾直接打印一份可粘贴的
# Markdown 正文——靠人去「回头整理」的记录，最后往往就没有记录。
DIFF_LINES=""
INTEGRITY=""
TABLES_BACKUP=""
TABLES_PROD=""
MISSING_TABLES=0

FAILED=0
# DEEP=1 表示真的打开了恢复出来的库做过校验。没做过就不能报「通过」——
# 一份「什么都没检查却显示 PASS」的演练报告比没有报告更危险。
DEEP=0
pass() { echo "  [PASS] $*"; }
fail() { echo "  [FAIL] $*"; FAILED=$((FAILED + 1)); }
warn() { echo "  [WARN] $*"; }
info() { echo "  [INFO] $*"; }
head_() { echo; echo "── $* ──"; }

# 取最新备份（两种产物都认）
if [ -z "$BACKUP_FILE" ]; then
  BACKUP_FILE="$(ls -t "$BACKUP_DIR"/weichuan-*.sql.gz "$BACKUP_DIR"/weichuan-*.db.gz 2>/dev/null | head -1 || true)"
fi
[ -n "$BACKUP_FILE" ] || { echo "[drill][FATAL] $BACKUP_DIR 里没有备份文件（先跑 scripts/backup.sh）" >&2; exit 1; }
[ -f "$BACKUP_FILE" ] || { echo "[drill][FATAL] 备份文件不存在：$BACKUP_FILE" >&2; exit 1; }

case "$BACKUP_FILE" in
  *.sql.gz|*.sql) DRIVER="mysql" ;;
  *.db.gz|*.db)   DRIVER="sqlite" ;;
  *) echo "[drill][FATAL] 无法判断形态：$BACKUP_FILE" >&2; exit 2 ;;
esac

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/weichuan-drill-XXXXXX")"
cleanup() {
  if [ "$KEEP" = "1" ]; then
    echo; echo "[drill] 临时产物保留在：$TMP_DIR"
  else
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT

echo "════════ 恢复演练 ════════"
echo "  备份文件 : $BACKUP_FILE"
echo "  形态     : $DRIVER"
echo "  时间     : $(date '+%F %T')"

# ── 1. 包完整性 ──
head_ "1. 备份包完整性"
if [ "${BACKUP_FILE##*.}" = "gz" ]; then
  if gzip -t "$BACKUP_FILE" 2>/dev/null; then
    pass "gzip -t 通过（归档未损坏）"
  else
    fail "gzip -t 未通过——归档已损坏，这份备份不可用"
    echo; echo "结论：不可用（${FAILED} 项失败）"; exit 1
  fi
  PLAIN="$TMP_DIR/payload"
  gzip -dc "$BACKUP_FILE" > "$PLAIN" || { fail "解压失败"; exit 1; }
else
  PLAIN="$BACKUP_FILE"
  warn "未经 gzip 压缩，跳过归档完整性检查"
fi
info "解压后大小：$(du -h "$PLAIN" | cut -f1)"

# 校验工具：优先用 SERVER_BIN（原生部署 / 本机演练），否则进容器执行。
# 两条路走同一份二进制、同一套校验，不会出现「容器里过了、本机不过」的分歧。
run_server() {
  if [ -n "${SERVER_BIN:-}" ]; then
    "$SERVER_BIN" "$@"
  else
    docker exec "$APP_CONTAINER" /app/server "$@"
  fi
}
dbcheck() { run_server dbcheck --file "$1" --field "$2" 2>/dev/null; }

# ═══════════ SQLite 演练 ═══════════
drill_sqlite() {
  local restored="$TMP_DIR/restored.db"
  cp "$PLAIN" "$restored"

  head_ "2. 恢复到临时目录"
  local magic
  magic="$(head -c 15 "$restored" | tr -d '\0')"
  if [ "$magic" = "SQLite format 3" ]; then
    pass "文件头正确（SQLite format 3）"
  else
    fail "文件头不是 SQLite 库——备份内容与预期不符"
    return
  fi

  head_ "3. 库完整性"
  local integrity tables
  if integrity="$(dbcheck "$restored" integrity)" && [ -n "$integrity" ]; then
    DEEP=1
    INTEGRITY="$integrity"
    if [ "$integrity" = "ok" ]; then pass "integrity_check = ok"; else fail "integrity_check = $integrity"; fi
    tables="$(dbcheck "$restored" tables || echo 0)"
    TABLES_BACKUP="$tables"
    if [ "${tables:-0}" -gt 0 ]; then pass "表数量 = $tables"; else fail "一张表都没有"; fi
  else
    warn "容器内 dbcheck 不可用，跳过深度校验（只验了文件头）"
    warn "要做完整演练请在有 $APP_CONTAINER 容器的机器上跑，或设 APP_CONTAINER 指向它"
    return
  fi

  head_ "4. 与生产当前库对比"
  local snap="/tmp/weichuan-drill-prod-$$.db"
  if ! run_server backup --out "$snap" >/dev/null 2>&1; then
    warn "取不到生产库快照（容器未运行 / DB 未配置），跳过表清单比对"
    return
  fi

  local prod_tables
  prod_tables="$(dbcheck "$snap" tables || echo 0)"
  TABLES_PROD="$prod_tables"
  info "生产当前库表数量 = ${prod_tables:-?}（备份 = ${tables}）"

  # 表清单比对：遍历**生产库**的表，逐个确认**备份里也有**。
  # 少表 = 硬故障（备份不完整，恢复回去就是缺功能）。
  # 注意方向别写反：要查的是 restored（备份），不是 snap（生产）——
  # 查 snap 等于「生产有没有这张表」，恒为真，这个断言就永远绿。
  local missing=0 t
  while IFS='=' read -r t _n; do
    [ -n "$t" ] || continue
    if ! dbcheck "$restored" "rows:$t" >/dev/null 2>&1; then
      fail "备份缺少生产库里的表：$t"
      missing=$((missing + 1))
    fi
  done < <(dbcheck "$snap" rows)
  [ "$missing" -eq 0 ] && pass "备份的表清单覆盖生产库（未缺表）"

  # 行数差异只报告：日备落后是正常现象，判失败会让演练永远红着
  local rows_backup rows_prod
  rows_backup="$(dbcheck "$restored" rows)"
  rows_prod="$(dbcheck "$snap" rows)"
  echo "  [INFO] 行数对比（备份 vs 生产，差异为正说明生产更新）："
  while IFS='=' read -r t n; do
    [ -n "$t" ] || continue
    local pn
    pn="$(printf '%s\n' "$rows_prod" | awk -F= -v k="$t" '$1==k{print $2}')"
    [ -n "$pn" ] || continue
    if [ "$pn" != "$n" ]; then
      printf '         %-24s %6s vs %-6s (%+d)\n' "$t" "$n" "$pn" "$((pn - n))"
      DIFF_LINES="${DIFF_LINES}  - ${t}: 备份 ${n} / 生产 ${pn}（${pn} 比备份多 $((pn - n)) 行）\n"
    fi
  done <<< "$rows_backup"

  docker exec "$APP_CONTAINER" rm -f "$snap" >/dev/null 2>&1 || true
  [ -n "${SERVER_BIN:-}" ] && rm -f "$snap"
}

# ═══════════ MySQL 演练 ═══════════
# mydrill <库名> <mysql 参数...>：在容器里跑 mysql 客户端。
# 临时库的创建与删除都在 drill_mysql 内成对完成（不用 RETURN trap——
# bash 的 RETURN trap 在非 -T 模式下不可靠，漏删会在库里留下垃圾库）。
mydrill() {
  local db="$1"; shift
  if [ -n "$db" ]; then
    docker exec -e MYSQL_PWD="$DB_PASSWORD" "$DB_CONTAINER" mysql -u"$DB_USER" "$db" "$@"
  else
    docker exec -e MYSQL_PWD="$DB_PASSWORD" "$DB_CONTAINER" mysql -u"$DB_USER" "$@"
  fi
}

drill_mysql() {
  head_ "2. 恢复到临时库"
  local drill_db="weichuan_drill_$(date +%Y%m%d%H%M%S)"
  local t a b

  if ! docker inspect "$DB_CONTAINER" >/dev/null 2>&1; then
    fail "找不到容器 ${DB_CONTAINER}，无法演练 MySQL 备份"
    return
  fi
  if ! mydrill "" -e "CREATE DATABASE \`$drill_db\` DEFAULT CHARACTER SET utf8mb4"; then
    fail "创建临时库失败（权限不足？）"
    return
  fi

  if docker exec -i -e MYSQL_PWD="$DB_PASSWORD" "$DB_CONTAINER" \
      mysql -u"$DB_USER" --default-character-set=utf8mb4 "$drill_db" < "$PLAIN"; then
    pass "导入成功"
    DEEP=1
  else
    fail "导入失败——这份备份不能用来恢复"
    mydrill "" -e "DROP DATABASE IF EXISTS \`$drill_db\`" >/dev/null 2>&1 || true
    return
  fi

  local t_drill t_prod
  t_drill="$(mydrill "" -N -B -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='$drill_db'")"
  t_prod="$(mydrill "" -N -B -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='$DB_NAME'")"
  if [ "${t_drill:-0}" -gt 0 ]; then pass "临时库表数量 = ${t_drill}（生产 = ${t_prod}）"; else fail "临时库没有表"; fi
  TABLES_BACKUP="$t_drill"
  TABLES_PROD="$t_prod"

  head_ "3. 与生产库对比表清单"
  local missing
  missing="$(mydrill "" -N -B -e \
    "SELECT table_name FROM information_schema.tables WHERE table_schema='$DB_NAME'
     AND table_name NOT IN (SELECT table_name FROM information_schema.tables WHERE table_schema='$drill_db')")"
  if [ -z "$missing" ]; then
    pass "备份的表清单覆盖生产库（未缺表）"
  else
    while read -r t; do [ -n "$t" ] && fail "备份缺少生产库里的表：$t"; done <<< "$missing"
  fi
  MISSING_TABLES="$(printf '%s\n' "$missing" | grep -c . || true)"

  head_ "4. 核心表行数（报告）"
  for t in company employees open_apply card_modify_apply sys_setting audit_log; do
    a="$(mydrill "$drill_db" -N -B -e "SELECT COUNT(*) FROM \`$t\`" 2>/dev/null || echo '-')"
    b="$(mydrill "$DB_NAME" -N -B -e "SELECT COUNT(*) FROM \`$t\`" 2>/dev/null || echo '-')"
    [ "$a" = "-" ] && [ "$b" = "-" ] && continue
    printf '  [INFO] %-20s 备份 %-8s 生产 %-8s\n' "$t" "$a" "$b"
    [ "$a" != "$b" ] && DIFF_LINES="${DIFF_LINES}  - ${t}: 备份 ${a} / 生产 ${b}\n"
  done

  # 清理临时库：演练不该在生产库实例上留垃圾
  if mydrill "" -e "DROP DATABASE IF EXISTS \`$drill_db\`"; then
    info "已删除临时库 $drill_db"
  else
    warn "临时库 $drill_db 删除失败，请手动清理"
  fi
}

if [ "$DRIVER" = "sqlite" ]; then
  drill_sqlite
else
  drill_mysql
fi

# ── 结论 ──
echo
CONCLUSION=""
if [ "$FAILED" -gt 0 ]; then
  CONCLUSION="不通过（${FAILED} 项失败）"
elif [ "$DEEP" -eq 0 ]; then
  CONCLUSION="未完成（环境不完整）"
else
  CONCLUSION="通过"
fi

# 可粘贴的留档正文：演练的价值一半在「跑」，一半在「留痕」。
# 客户或审计问「凭什么说备份能用」，拿得出手的只有这份记录。
echo "── 留档正文（贴进 docs/drill-records/$(date +%F)-${DRIVER}.md）──"
echo "- 日期：$(date +%F)"
echo "- 备份文件：$(basename "$BACKUP_FILE")"
echo "- 形态：${DRIVER}｜结论：**${CONCLUSION}**"
echo "- 校验：gzip -t $([ "$FAILED" -gt 0 ] && echo 见上 || echo PASS)｜integrity_check=${INTEGRITY:-不适用（MySQL 以「导入成功 + 表清单覆盖」为判据）}｜表数 备份 ${TABLES_BACKUP:-?} / 生产 ${TABLES_PROD:-?}｜缺表 ${MISSING_TABLES}"
echo "- 行数差异（备份落后于生产，属正常）："
if [ -n "$DIFF_LINES" ]; then printf "$DIFF_LINES"; else echo "  - 无"; fi
echo

if [ "$FAILED" -gt 0 ]; then
  echo "结论：不通过（${FAILED} 项失败）—— 先修备份链路，再谈「有备份」"
  exit 1
fi
if [ "$DEEP" -eq 0 ]; then
  echo "结论：未能完成演练（环境不完整：没有可用的 dbcheck / 数据库容器）"
  echo "      只验证了「归档没坏 + 文件头像 SQLite」，这不等于「备份可用」。"
  echo "      请在部署机上重跑（或设 APP_CONTAINER / DB_CONTAINER 指向运行中的容器）。"
  exit 2
fi
echo "结论：通过 —— 这份备份可以用于恢复（${BACKUP_FILE}）"
exit 0
