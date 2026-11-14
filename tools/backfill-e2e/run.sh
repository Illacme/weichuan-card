#!/usr/bin/env bash
# 真桶 + 存量对象灰度（第六十六轮）：把「平台本地盘里的存量对象」经
# storage-backfill apply 真的搬进客户自己的 S3 桶，再原样搬回来。
#
# 与 tools/s3-e2e 的分工：那个装置验「新上传的对象能不能进真桶」，
# 本装置补 README 里明写的那条空白——**不覆盖存量对象**。这里的对象
# 是 seed.sql 造出来的历史形态 /uploads/shared-logo.png，不是新上传的。
#
# 证据链（任何一步绿了都不能代表下一步绿）：
#   1. 匿名 GET → 403（端点真的验签，不是桩）；
#   2. 真配置行（凭据 pii.Encrypt）+ 真桶 + 真 MySQL；
#   3. dry-run 零写入（九张业务表 CHECKSUM 不变、桶里没有对象）；
#   4. apply --target-store tenant --yes → 桶里签名 GET 取回的字节与源一致；
#   5. 重跑零改动；status 台账；rollback 后业务表校验和回到基线、对象仍在（R5）；
#   6. 三个负例：缺配置 / 坏密文 / --target-dir 与 tenant 同给，都必须显式失败，
#      且平台本地盘绝不出现 t/<cid>/ 对象（无静默降级）。
#
# 依赖：docker（mysql:8.0，容器名 wc-mysql-ci）、go、curl、一个在 19000 跑的
# versitygw。起服务端的方法见 tools/s3-e2e/README.md。
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
mkdir -p "$HERE/logs"
LOG="$HERE/logs/run.log"
: > "$LOG"

S3_ENDPOINT="${BF_E2E_ENDPOINT:-127.0.0.1:19000}"
S3_AK="${BF_E2E_ACCESS_KEY:-testuser}"
S3_SK="${BF_E2E_SECRET_KEY:-testsecret}"
S3_BUCKET="${BF_E2E_BUCKET:-wc-t7}"
CID="${BF_E2E_COMPANY:-7}"
# 固定密钥：加密配置行与回填命令必须解得出同一把，写死才让取证可复现。
export PII_ENC_KEY='d2VpY2h1YW4tYmFja2ZpbGwtZTJlLWtleS0wMDAwMDA='
DSN='root:root@tcp(127.0.0.1:3306)/weichuan_backfill_demo?charset=utf8mb4&parseTime=true'
WORK=/tmp/backfill-e2e
SRC="$WORK/uploads"
PLAN="$WORK/plan.json"
BIN="$WORK/storage-backfill"
PREP="$WORK/prep"
KEY="t/$CID/shared-logo.png"
TABLES="employees,open_applies,company_infos,company_profiles,achievement_categories,project_cases,biz_services,home_layouts,media_check"
rc=0

ck() { # ck 描述 期望 实际
  if [ "$2" = "$3" ]; then printf '  ✓ %s\n' "$1" | tee -a "$LOG"; else
    printf '  ✗ %s\n      期望: %s\n      实际: %s\n' "$1" "$2" "$3" | tee -a "$LOG"; rc=1; fi
}
has() { case "$1" in *"$2"*) return 0;; *) return 1;; esac; }
step() { printf '\n== %s ==\n' "$1" | tee -a "$LOG"; }
# 必须带库名：不带会报 "No database selected"，而 stderr 被吞掉后
# 查询结果是空串——CHECKSUM 两边都空会让「校验和一致」变成假绿。
mysqlq() { docker exec -i wc-mysql-ci mysql -uroot -proot --default-character-set=utf8mb4 -N -e "$1" weichuan_backfill_demo 2>/dev/null; }
checksum() { mysqlq "CHECKSUM TABLE $TABLES" | awk 'NF>=2 {print $2}' | tr '\n' ' '; }
vals() {
  mysqlq "SELECT avatar FROM employees WHERE staff_id=13 UNION ALL SELECT logo FROM company_infos WHERE id=2 UNION ALL SELECT icon FROM biz_services WHERE id=1" | tr '\n' ' '
}
sha_of() { "$PREP" -mode stat -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" -bucket "$S3_BUCKET" -company "$CID" -key "$KEY" 2>/dev/null | awk -F= '/^SHA256=/{print $2}'; }

step "0. 前置：真 S3 端点 / 真 MySQL / 二进制"
code="$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' -m 5 "http://$S3_ENDPOINT/" || true)"
ck "匿名 GET 返回 403（端点会验 SigV4）" "403" "$code"
if [ "$code" != "403" ]; then echo "  先起 versitygw，见 tools/s3-e2e/README.md"; exit 1; fi
ck "MySQL 可达" "1" "$(mysqlq 'SELECT 1')"
mkdir -p "$WORK" "$SRC"
( cd "$REPO_ROOT/backend" && go build -o "$BIN" ./cmd/storage-backfill && go build -o "$PREP" ./cmd/backfill-e2e-prep ) > "$HERE/logs/build.log" 2>&1
ck "两个二进制编译成功" "0" "$?"

step "1. 复位一次性演示库与源对象"
docker exec -i wc-mysql-ci mysql -uroot -proot --default-character-set=utf8mb4 weichuan_backfill_demo \
  < "$REPO_ROOT/tools/backfill-fixture/seed.sql" > /dev/null 2>&1
ck "seed.sql 灌入成功" "0" "$?"
mysqlq "DELETE FROM storage_backfill_refs; DELETE FROM tenant_storage_configs;" > /dev/null
printf '0123456789abcdefghij' > "$SRC/shared-logo.png" # 20 字节，与第六十五轮取证同尺寸
SRCSHA="$(shasum -a 256 "$SRC/shared-logo.png" | awk '{print $1}')"
BASE="$(checksum)"
# 这两条不是形式主义：查询失败时两边都会拿到空串，「一致」反而恒绿。
ck "基线校验和非空（防止空串对空串的假绿）" "yes" "$([ -n "$(printf '%s' "$BASE" | tr -d ' ')" ] && echo yes || echo no)"
ck "库值查询有效（同上）" "yes" "$([ -n "$(vals)" ] && echo yes || echo no)"
ck "基线：三处引用仍是历史 /uploads/ 形态" "/uploads/shared-logo.png /uploads/shared-logo.png /uploads/shared-logo.png " "$(vals)"

OUT="$("$PREP" -mode upsert -dsn "$DSN" -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" -bucket "$S3_BUCKET" -company "$CID" 2>&1)"; URC=$?
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "真桶 + 加密配置行就绪" "0" "$URC"
ck "初始时桶里没有目标对象" "MISSING=1" "$("$PREP" -mode stat -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" -bucket "$S3_BUCKET" -company "$CID" -key "$KEY" 2>/dev/null | tr -d '\r')"

step "2. plan（只读，company $CID 子集）"
OUT="$("$BIN" plan --dsn "$DSN" --base-url 'https://card.example.com/uploads' --company "$CID" --out "$PLAN" 2>&1)"; PRC=$?
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "plan 退出码 0" "0" "$PRC"
ck "计划含 3 条待搬 / 1 个去重对象" "yes" "$(has "$OUT" "待搬迁 3 条（去重后 1 个对象要复制）" && echo yes || echo no)"
ck "plan 后业务表校验和不变" "$BASE" "$(checksum)"

step "3. dry-run：零写入"
OUT="$("$BIN" apply --plan "$PLAN" --dsn "$DSN" --target-store tenant --source-dir "$SRC" --dry-run 2>&1)"; DRC=$?
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "dry-run 退出码 0" "0" "$DRC"
ck "dry-run 明确声明未写入" "yes" "$(has "$OUT" "未写入任何数据" && echo yes || echo no)"
ck "dry-run 后业务表校验和不变" "$BASE" "$(checksum)"
ck "dry-run 后桶里仍无对象" "MISSING=1" "$("$PREP" -mode stat -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" -bucket "$S3_BUCKET" -company "$CID" -key "$KEY" 2>/dev/null | tr -d '\r')"
ck "dry-run 后库值未变" "/uploads/shared-logo.png /uploads/shared-logo.png /uploads/shared-logo.png " "$(vals)"

step "4. apply --yes：对象真的进客户桶"
OUT="$("$BIN" apply --plan "$PLAN" --dsn "$DSN" --target-store tenant --source-dir "$SRC" --yes 2>&1)"; ARC=$?
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "apply 退出码 0" "0" "$ARC"
ck "复制 1 个对象、改写 3 条引用" "yes" "$(has "$OUT" "已复制 1 个对象、改写 3 条引用" && echo yes || echo no)"
ck "签名 GET 取回的字节与源逐字节一致" "$SRCSHA" "$(sha_of)"
ck "三处库值都变成租户键" "t/7/shared-logo.png t/7/shared-logo.png t/7/shared-logo.png " "$(vals)"
ck "平台本地盘没有出现 t/7/ 对象（无静默降级）" "no" "$([ -e "$SRC/t/$CID/shared-logo.png" ] && echo yes || echo no)"

step "5. 重跑 / status / rollback"
OUT="$("$BIN" apply --plan "$PLAN" --dsn "$DSN" --target-store tenant --source-dir "$SRC" --yes 2>&1)"; RRC=$?
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "重跑退出码 0" "0" "$RRC"
ck "重跑复制 0 / 改写 0" "yes" "$(has "$OUT" "已复制 0 个对象、改写 0 条引用" && echo yes || echo no)"
OUT="$("$BIN" status --plan "$PLAN" --dsn "$DSN" 2>&1)"
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "台账 3 行 applied" "yes" "$(has "$OUT" "applied" && has "$OUT" "合计 3 行" && echo yes || echo no)"
OUT="$("$BIN" rollback --plan "$PLAN" --dsn "$DSN" --dry-run 2>&1)"
ck "rollback 不加 --yes 也是零写入" "yes" "$(has "$OUT" "未写入任何数据" && echo yes || echo no)"
OUT="$("$BIN" rollback --plan "$PLAN" --dsn "$DSN" --yes 2>&1)"; BRC=$?
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "rollback 退出码 0" "0" "$BRC"
ck "三处库值回到历史 /uploads/ 形态" "/uploads/shared-logo.png /uploads/shared-logo.png /uploads/shared-logo.png " "$(vals)"
ck "九张业务表校验和与搬迁前逐字节一致" "$BASE" "$(checksum)"
ck "对象仍在桶里（R5：回滚不删对象）" "$SRCSHA" "$(sha_of)"

step "6. 负例：绝不静默回退 / 绝不吞解密失败"
OUT="$("$BIN" apply --plan "$PLAN" --dsn "$DSN" --target-store tenant --source-dir "$SRC" --target-dir "$WORK/should-not-exist" --yes 2>&1)"; XRC=$?
ck "tenant + --target-dir 同给时拒绝" "yes" "$([ "$XRC" -ne 0 ] && has "$OUT" "--target-dir" && echo yes || echo no)"
"$PREP" -mode delete -dsn "$DSN" -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" -bucket "$S3_BUCKET" -company "$CID" > /dev/null 2>&1
OUT="$("$BIN" apply --plan "$PLAN" --dsn "$DSN" --target-store tenant --source-dir "$SRC" --yes 2>&1)"; XRC=$?
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "缺配置行时显式失败" "yes" "$([ "$XRC" -ne 0 ] && has "$OUT" "没有生效的自有桶配置" && echo yes || echo no)"
ck "错误信息写明不回退平台盘" "yes" "$(has "$OUT" "不回退平台盘" && echo yes || echo no)"
ck "负例后业务表校验和仍等于基线" "$BASE" "$(checksum)"
ck "负例后平台本地盘仍无 t/7/ 对象" "no" "$([ -e "$SRC/t/$CID/shared-logo.png" ] && echo yes || echo no)"

# -clean=false：负例阶段要保住第 5 步留下的对象，顺带再证一次「回滚/拒绝都不删对象」。
"$PREP" -mode upsert -clean=false -dsn "$DSN" -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" -bucket "$S3_BUCKET" -company "$CID" > /dev/null 2>&1
mysqlq "UPDATE tenant_storage_configs SET access_key_enc='v1:broken-cipher' WHERE company_id=$CID" > /dev/null 2>&1
OUT="$("$BIN" apply --plan "$PLAN" --dsn "$DSN" --target-store tenant --source-dir "$SRC" --yes 2>&1)"; XRC=$?
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "凭据解密失败时显式失败（不回退）" "yes" "$([ "$XRC" -ne 0 ] && has "$OUT" "解密失败" && echo yes || echo no)"
ck "全部负例跑完后对象仍在桶里（R5）" "$SRCSHA" "$(sha_of)"

printf '\n== 结论：%s ==\n' "$([ "$rc" -eq 0 ] && echo '全部通过' || echo '有断言失败')" | tee -a "$LOG"
printf '取证日志：%s\n' "$LOG"
exit "$rc"
