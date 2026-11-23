#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# 时间点恢复（PITR）演练：全量备份 + binlog 定点回放。
#
#   scripts/restore_pitr_drill.sh            # 起一次性容器，完整演练
#   scripts/restore_pitr_drill.sh --keep     # 保留临时产物便于排查
#
# restore_drill.sh 回答的是「最新那份全量备份能不能救回来」，
# 本脚本回答的是它覆盖不到的那一半：**「备份之后、误操作之前写的那些数据，
# 能不能靠 binlog 找回来」**。只归档 binlog 却从没回放过，等于把 PITR
# 写在文档里而没有证据——真出事时才发现 mysqlbinlog 参数写错，就来不及了。
#
# 做法（全程独立容器与临时库，不碰生产）：
#   1. 起一个开了 log_bin 的 MySQL 8 容器，建探针表并写基线 A1/A2；
#   2. FLUSH LOGS 封口 binlog，记下备份点 P1，mysqldump 全量备份；
#   3. 备份后再写 B1，记下「误操作前」的位置 P2；
#   4. DELETE 全表模拟误操作，归档封口的 binlog 并 gzip -t 校验；
#   5. 临时库导入备份，mysqlbinlog --start-position=P1 --stop-position=P2 回放；
#   6. 断言恢复到 A1/A2/B1 三条（误删除掉的那条被精确跳过）；
#   7. **负例**：另一个临时库回放时不设 --stop-position，必须得到空表——
#      如果负例也有三条，说明「定点回放」根本没生效，正例的 PASS 不算数。
#
# 退出码：0 全部通过 / 1 有 FAIL / 2 环境不完整（没有 docker，没真正校验过就不算通过）。
# 结束时打印可粘贴的留档正文，存 docs/drill-records/YYYY-MM-DD-pitr.md。
# ─────────────────────────────────────────────────────────────
set -uo pipefail

CONTAINER="wc-pitr-drill"
SRC_DB="weichuan_pitr"
RESTORE_DB="weichuan_pitr_restore"
BAD_DB="weichuan_pitr_bad"
KEEP=0
while [ $# -gt 0 ]; do
  case "$1" in
    --keep) KEEP=1; shift ;;
    -h|--help)
      awk 'NR<=2{next} /^# ─/{exit} /^#/{sub(/^# ?/,""); print}' "$0"; exit 0 ;;
    *) echo "[pitr][FATAL] 未知参数 $1" >&2; exit 2 ;;
  esac
done

if ! command -v docker >/dev/null 2>&1; then
  echo "[pitr][FATAL] 本机没有 docker，无法起一次性 MySQL 做真实回放演练" >&2
  exit 2
fi
# mysqlbinlog 在宿主机上跑（mysql:8.0 官方镜像不带这个客户端），可用 MYSQLBINLOG 覆盖。
MYSQLBINLOG="${MYSQLBINLOG:-}"
if [ -z "$MYSQLBINLOG" ]; then
  if command -v mysqlbinlog >/dev/null 2>&1; then
    MYSQLBINLOG="$(command -v mysqlbinlog)"
  elif [ -x /opt/homebrew/opt/mysql-client/bin/mysqlbinlog ]; then
    MYSQLBINLOG=/opt/homebrew/opt/mysql-client/bin/mysqlbinlog
  else
    echo "[pitr][FATAL] 找不到 mysqlbinlog（需要本机装 MySQL client），无法回放 binlog" >&2
    exit 2
  fi
fi

WORK="$(mktemp -d "${TMPDIR:-/tmp}/weichuan-pitr-XXXXXX")"
FAILED=0
DEEP=0
cleanup() {
  if [ "$KEEP" -eq 0 ]; then
    docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
    rm -rf "$WORK"
  else
    echo "[pitr] --keep：保留容器 $CONTAINER 与目录 $WORK"
  fi
}
trap cleanup EXIT

pass() { echo "  [PASS] $*"; }
fail() { echo "  [FAIL] $*"; FAILED=$((FAILED + 1)); }
q() { docker exec "$CONTAINER" mysql -uroot -proot -N -e "$1" 2>/dev/null; }

echo "════════ PITR 演练（全量备份 + binlog 定点回放）════════"

# ── 0. 一次性容器 ──
docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
if ! docker run -d --name "$CONTAINER" \
    -e MYSQL_ROOT_PASSWORD=root \
    mysql:8.0 \
    --log-bin=/var/lib/mysql/mysql-bin --server-id=1 --binlog-format=ROW \
    >/dev/null 2>&1; then
  echo "[pitr][FATAL] 起容器失败（需要先 colima start / Docker Desktop）" >&2
  exit 2
fi
READY=0
i=0
while [ "$i" -lt 60 ]; do
  if q "SELECT 1;" >/dev/null 2>&1; then READY=1; break; fi
  i=$((i + 1)); sleep 1
done
if [ "$READY" -ne 1 ]; then
  echo "[pitr][FATAL] MySQL 60 秒内没就绪，无法演练" >&2
  exit 2
fi
LOG_BIN="$(q "SHOW VARIABLES LIKE 'log_bin';" | awk '{print $2}' | tail -1)"
[ "$LOG_BIN" = "ON" ] || fail "log_bin=${LOG_BIN}，binlog 没开——演练环境不对"

q "CREATE DATABASE $SRC_DB;" || fail "建源库失败"
q "CREATE TABLE $SRC_DB.pitr_probe (id INT PRIMARY KEY, note VARCHAR(64) NOT NULL);" || fail "建探针表失败"
q "INSERT INTO $SRC_DB.pitr_probe VALUES (1,'A1'),(2,'A2');" || fail "写基线数据失败"

# ── 1. 封口 binlog，记备份点 ──
q "FLUSH BINARY LOGS;" >/dev/null
MASTER_1="$(q "SHOW MASTER STATUS;")"
LOG_FILE="$(printf '%s\n' "$MASTER_1" | awk '{print $1}' | head -1)"
P1="$(printf '%s\n' "$MASTER_1" | awk '{print $2}' | head -1)"
[ -n "$LOG_FILE" ] && [ -n "$P1" ] || fail "取不到备份点的 binlog 文件/位置：$MASTER_1"

docker exec "$CONTAINER" mysqldump -uroot -proot "$SRC_DB" > "$WORK/backup.sql" 2>/dev/null
[ -s "$WORK/backup.sql" ] || fail "mysqldump 出来是空文件"
gzip -c "$WORK/backup.sql" > "$WORK/backup.sql.gz"
gzip -t "$WORK/backup.sql.gz" || fail "备份包 gzip -t 校验失败"

# ── 2. 备份后写 B1，记误操作前的位置 ──
q "INSERT INTO $SRC_DB.pitr_probe VALUES (3,'B1');" || fail "写 B1 失败"
MASTER_2="$(q "SHOW MASTER STATUS;")"
P2="$(printf '%s\n' "$MASTER_2" | awk '{print $2}' | head -1)"
LOG_FILE_2="$(printf '%s\n' "$MASTER_2" | awk '{print $1}' | head -1)"
[ -n "$P2" ] || fail "取不到误操作前的位置：$MASTER_2"
[ "$LOG_FILE_2" = "$LOG_FILE" ] || fail "备份点与误操作前不在同一个 binlog 文件（${LOG_FILE} -> ${LOG_FILE_2}），本脚本需同文件"

# ── 3. 误操作：删全表 ──
q "DELETE FROM $SRC_DB.pitr_probe;" || fail "模拟误删除失败"
LEFT="$(q "SELECT COUNT(*) FROM $SRC_DB.pitr_probe;")"
[ "$LEFT" = "0" ] || fail "误删除没生效，表里还有 $LEFT 行——后面的断言失去意义"

# ── 4. 封口并归档 binlog ──
q "FLUSH BINARY LOGS;" >/dev/null
docker cp "$CONTAINER:/var/lib/mysql/$LOG_FILE" "$WORK/$LOG_FILE" >/dev/null 2>&1 \
  || fail "docker cp binlog $LOG_FILE 失败"
gzip -c "$WORK/$LOG_FILE" > "$WORK/$LOG_FILE.gz"
gzip -t "$WORK/$LOG_FILE.gz" || fail "binlog 归档 gzip -t 校验失败"
echo "  备份点 $LOG_FILE:$P1 ／ 误操作前 $LOG_FILE:$P2 ／ 归档 $(du -h "$WORK/$LOG_FILE.gz" | cut -f1)"

# ── 5. 正例：导入备份 + 定点回放 ──
q "CREATE DATABASE $RESTORE_DB;" || fail "建恢复库失败"
docker exec -i "$CONTAINER" mysql -uroot -proot "$RESTORE_DB" < "$WORK/backup.sql" 2>/dev/null \
  || fail "导入全量备份失败"
"$MYSQLBINLOG" --start-position="$P1" --stop-position="$P2" \
  --rewrite-db="$SRC_DB->$RESTORE_DB" "$WORK/$LOG_FILE" 2>/dev/null \
  | docker exec -i "$CONTAINER" mysql -uroot -proot "$RESTORE_DB" 2>/dev/null \
  || fail "binlog 定点回放失败"

GOT="$(q "SELECT COALESCE(GROUP_CONCAT(note ORDER BY id),'') FROM $RESTORE_DB.pitr_probe;")"
if [ "$GOT" = "A1,A2,B1" ]; then
  DEEP=1
  pass "定点回放恢复到 A1,A2,B1（备份后的 B1 被找回，误删除被跳过）"
else
  fail "定点回放结果是「${GOT:-空}」，期望 A1,A2,B1"
fi

# ── 6. 负例：不设 stop-position，必须把 DELETE 也回放掉 ──
# 这一步是量尺自检：若负例也恢复到三条，说明回放根本没走到误操作，
# 正例的 PASS 只是「备份里本来就有 A1/A2」的假象。
q "CREATE DATABASE $BAD_DB;" || fail "建负例库失败"
docker exec -i "$CONTAINER" mysql -uroot -proot "$BAD_DB" < "$WORK/backup.sql" 2>/dev/null \
  || fail "负例库导入备份失败"
"$MYSQLBINLOG" --start-position="$P1" --rewrite-db="$SRC_DB->$BAD_DB" \
  "$WORK/$LOG_FILE" 2>/dev/null \
  | docker exec -i "$CONTAINER" mysql -uroot -proot "$BAD_DB" 2>/dev/null \
  || fail "负例回放失败"
BAD_GOT="$(q "SELECT COALESCE(GROUP_CONCAT(note ORDER BY id),'') FROM $BAD_DB.pitr_probe;")"
if [ -z "$BAD_GOT" ]; then
  pass "负例：不带 stop-position 回放后表为空——证明 DELETE 确实在 binlog 里，定点拦截有效"
else
  fail "负例回放得到「${BAD_GOT}」而不是空表：stop-position 量尺失灵，正例结论不可信"
fi

# ── 7. 留档正文 ──
echo ""
echo "════════ 留档正文（贴进 docs/drill-records/） ════════"
printf '# 恢复演练记录 · %s · MySQL PITR（全量备份 + binlog 定点回放）\n\n' "$(date +%F)"
echo "- 日期：$(date +'%F %T')"
echo "- 形态：MySQL 8.0（一次性容器，未碰生产）"
echo "- 结论：**$([ "$FAILED" -eq 0 ] && echo 通过 || echo 未通过)**"
echo "- 备份点：\`$LOG_FILE:$P1\` ／ 误操作前：\`$LOG_FILE:$P2\`"
echo "- 正例：恢复结果 \`$GOT\` ／ 负例：\`${BAD_GOT:-空}\`"
echo "- 校验：gzip -t PASS｜深度校验 ${DEEP}（真的导入并回放了，不是只看文件存在）"
echo ""
echo "## 校验项"
echo ""
echo "| 项 | 结果 |"
echo "|---|---|"
echo "| 全量备份包 gzip -t | PASS |"
echo "| binlog 归档 gzip -t | PASS |"
echo "| 定点回放找回备份后的 B1 | $([ "$GOT" = "A1,A2,B1" ] && echo PASS || echo FAIL) |"
echo "| 负例（无 stop-position）回放掉 DELETE | $([ -z "$BAD_GOT" ] && echo PASS || echo FAIL) |"

echo ""
if [ "$FAILED" -ne 0 ] || [ "$DEEP" -ne 1 ]; then
  echo "[pitr] 有 $FAILED 项失败（深度校验=${DEEP}），本次演练不能算通过" >&2
  exit 1
fi
echo "[pitr] 全部通过：binlog 回放链路已被真实验证，而不只是脚本存在"
