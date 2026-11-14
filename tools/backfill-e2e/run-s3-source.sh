#!/usr/bin/env bash
# 第六十七轮：平台自有 S3 桶当源 → 客户自有桶当目标的真桶灰度。
#
# 与 tools/backfill-e2e/run.sh（第六十六轮，平台本地盘 → 客户桶）并列，两套装置
# 各自独立跑、互不覆盖。本装置只验新增的 --source-store=s3 那一半。
#
# 三条「不能靠单测证明」的证据：
#   1. 源真的是平台 S3 桶，不是本地盘：平台源桶放字节 A，本地盘放不同的字节 B，
#      平台桶再放一份 p/ 前缀的诱饵 C。apply 后客户桶签名 GET 必须 == A、
#      != B、!= C。若有人偷偷回退本地盘或给源键加 p/ 前缀，字节立刻露馅。
#   2. 配置真的走「env + sys_setting 只读覆盖」：env 故意给一份错误的诱饵
#      （driver=local、桶不存在、凭据是假的），sys_setting 给正确值。只看 env
#      的话 driver=local 会被 NewPlatformS3Source 当场拒绝，apply 不可能成功。
#   3. 桶不存在 ≠ 源对象不存在：源端没有 Put 兜底，靠 BucketExists 把两者分开。
#      两个负例必须给出不同的报错，否则运维会去查对象而不是查配置。
#
# 依赖：docker（wc-mysql-ci，mysql:8.0）、go、curl、shasum、19000 上的 versitygw。
# 起服务端的方法见 tools/s3-e2e/README.md。
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
mkdir -p "$HERE/logs"
LOG="$HERE/logs/run-s3-source.log"
: > "$LOG"

S3_ENDPOINT="${BF_E2E_ENDPOINT:-127.0.0.1:19000}"
S3_AK="${BF_E2E_ACCESS_KEY:-testuser}"
S3_SK="${BF_E2E_SECRET_KEY:-testsecret}"
TENANT_BUCKET="${BF_E2E_BUCKET:-wc-t7}"
PLATFORM_BUCKET="${BF_E2E_PLATFORM_BUCKET:-wc-platform}"
ENV_DECOY_BUCKET="wc-env-decoy"
CID="${BF_E2E_COMPANY:-7}"
export PII_ENC_KEY='d2VpY2h1YW4tYmFja2ZpbGwtZTJlLWtleS0wMDAwMDA='
DSN='root:root@tcp(127.0.0.1:3306)/weichuan_backfill_demo?charset=utf8mb4&parseTime=true'
WORK=/tmp/backfill-e2e-s3src
SRC="$WORK/local-decoy"
PLAN="$WORK/plan.json"
BIN="$WORK/storage-backfill"
PREP="$WORK/prep"
SRC_KEY="shared-logo.png"
PREFIX_KEY="p/shared-logo.png"
KEY="t/$CID/shared-logo.png"
TABLES="employees,open_applies,company_infos,company_profiles,achievement_categories,project_cases,biz_services,home_layouts,media_check"
# 必须早于第一次 mysqlq：它的 stderr 要重定向到 $WORK/sql.err，
# 目录不存在会让整条查询直接失败并返回空串（又一处「空串对空串」）。
mkdir -p "$WORK" "$SRC"
rc=0

ck() { # ck 描述 期望 实际
  if [ "$2" = "$3" ]; then printf '  ✓ %s\n' "$1" | tee -a "$LOG"; else
    printf '  ✗ %s\n      期望: %s\n      实际: %s\n' "$1" "$2" "$3" | tee -a "$LOG"; rc=1; fi
}
has() { case "$1" in *"$2"*) return 0;; *) return 1;; esac; }
no() { if [ "$1" = "yes" ]; then return 1; else return 0; fi; }
step() { printf '\n== %s ==\n' "$1" | tee -a "$LOG"; }
# 必须带库名：不带会报 "No database selected"，stderr 被吞掉后查询结果是空串，
# 于是「两边校验和一致」会变成假绿（第六十六轮踩过）。
mysqlq() { docker exec -i wc-mysql-ci mysql -uroot -proot --default-character-set=utf8mb4 -N -e "$1" weichuan_backfill_demo 2>>"$WORK/sql.err"; }
mysqlw() { docker exec -i wc-mysql-ci mysql -uroot -proot --default-character-set=utf8mb4 -e "$1" weichuan_backfill_demo >>"$WORK/sql.out" 2>>"$WORK/sql.err"; }
checksum() { mysqlq "CHECKSUM TABLE $TABLES" | awk 'NF>=2 {print $2}' | tr '\n' ' '; }
settings_sum() { mysqlq "CHECKSUM TABLE sys_setting" | awk 'NF>=2 {print $2}'; }
vals() {
  mysqlq "SELECT avatar FROM employees WHERE staff_id=13 UNION ALL SELECT logo FROM company_infos WHERE id=2 UNION ALL SELECT icon FROM biz_services WHERE id=1" | tr '\n' ' '
}
stat_raw() { # stat_raw 桶 键
  "$PREP" -mode stat -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" -bucket "$1" -company "$CID" -key "$2" 2>/dev/null | tr -d '\r'
}
sha_of() { stat_raw "$1" "$2" | awk -F= '/^SHA256=/{print $2}'; }
# 断言「对象不存在」必须拿到服务端明确的 MISSING=1，不能拿「sha 是空串」：
# 端点不通 / 命令失败时 sha 也是空串，那样会恒绿（第六十六轮踩过）。
missing_of() { # missing_of 桶 键
  if [ "$(stat_raw "$1" "$2")" = "MISSING=1" ]; then echo yes; else echo no; fi
}
bucket_of() { # bucket_of 桶
  "$PREP" -mode bucket -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" -bucket "$1" -company "$CID" 2>/dev/null | tr -d '\r'
}

# 写平台自身存储配置。7 个 key 全给正确值；调用方可覆盖其中几个做负例。
# 用 heredoc 而不是一长串 -e：key / group 是 MySQL 保留字，反引号必须转义。
write_settings() { # write_settings bucket driver ak sk
  local b="$1" d="$2" a="$3" s="$4"
  cat > "$WORK/settings.sql" <<SQL
DELETE FROM sys_setting WHERE \`key\` IN (
  'storage_driver','storage_endpoint','storage_bucket','storage_access_key',
  'storage_secret_key','storage_use_ssl','storage_region');
INSERT INTO sys_setting (\`key\`,\`group\`,\`type\`,\`description\`,value) VALUES
  ('storage_driver','storage','string','','$d'),
  ('storage_endpoint','storage','string','','$S3_ENDPOINT'),
  ('storage_bucket','storage','string','','$b'),
  ('storage_access_key','storage','string','','$a'),
  ('storage_secret_key','storage','string','','$s'),
  ('storage_use_ssl','storage','bool','','off'),
  ('storage_region','storage','string','','us-east-1')
  ON DUPLICATE KEY UPDATE value=VALUES(value);
SQL
  docker exec -i wc-mysql-ci mysql -uroot -proot --default-character-set=utf8mb4 weichuan_backfill_demo \
    < "$WORK/settings.sql" >>"$WORK/sql.out" 2>>"$WORK/sql.err"
}
put_source() { # 清空平台源桶、放正确对象 A、再追加 p/ 前缀诱饵 C
  "$PREP" -mode putsrc -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" \
    -bucket "$PLATFORM_BUCKET" -company "$CID" -key "$SRC_KEY" -src-file "$WORK/bytes-A.bin" >/dev/null 2>&1
  "$PREP" -mode addsrc -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" \
    -bucket "$PLATFORM_BUCKET" -company "$CID" -key "$PREFIX_KEY" -src-file "$WORK/bytes-C.bin" >/dev/null 2>&1
}
upsert_tenant() { # upsert_tenant clean(true/false)
  "$PREP" -mode upsert -clean="$1" -dsn "$DSN" -endpoint "$S3_ENDPOINT" \
    -access-key "$S3_AK" -secret-key "$S3_SK" -bucket "$TENANT_BUCKET" -company "$CID" >/dev/null 2>&1
}

# 平台存储配置：env 给一份**错误诱饵**，sys_setting 给正确值。
# 只有 sys_setting 覆盖生效，apply 才可能成功；这是配置同源的真桶证据。
export CONFIG_FILE="$WORK/empty.env"
export STORAGE_DRIVER=local
export MINIO_ENDPOINT="$S3_ENDPOINT"
export MINIO_BUCKET="$ENV_DECOY_BUCKET"
export MINIO_ACCESS_KEY=env-decoy-ak
export MINIO_SECRET_KEY=env-decoy-sk
export MINIO_USE_SSL=false
export STORAGE_REGION=us-east-1
# 跑 apply 时把 env 诱饵固定下来（负例里个别项会用 env -u 临时覆盖）。
APPLY_ENV=(env CONFIG_FILE="$CONFIG_FILE" STORAGE_DRIVER=local MINIO_ENDPOINT="$S3_ENDPOINT" \
  MINIO_BUCKET="$ENV_DECOY_BUCKET" MINIO_ACCESS_KEY=env-decoy-ak MINIO_SECRET_KEY=env-decoy-sk \
  MINIO_USE_SSL=false STORAGE_REGION=us-east-1)
# 负例专用：不带 MINIO_ACCESS_KEY，配合 `env -u` 让「凭据缺一」真的发生。
# 若沿用 APPLY_ENV，`env -u X X=val` 会被后面的赋值重新设回来，负例恒绿。
APPLY_ENV_NO_AK=(CONFIG_FILE="$CONFIG_FILE" STORAGE_DRIVER=local MINIO_ENDPOINT="$S3_ENDPOINT" \
  MINIO_BUCKET="$ENV_DECOY_BUCKET" MINIO_SECRET_KEY=env-decoy-sk \
  MINIO_USE_SSL=false STORAGE_REGION=us-east-1)

step "0. 前置：真 S3 端点 / 真 MySQL / 两个不同的桶 / 二进制"
code="$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' -m 5 "http://$S3_ENDPOINT/" || true)"
ck "匿名 GET 返回 403（端点会验 SigV4）" "403" "$code"
if [ "$code" != "403" ]; then echo "  先起 versitygw，见 tools/s3-e2e/README.md"; exit 1; fi
ck "MySQL 可达" "1" "$(mysqlq 'SELECT 1')"
ck "平台源桶与租户目标桶是两个不同的桶" "yes" "$([ "$PLATFORM_BUCKET" != "$TENANT_BUCKET" ] && echo yes || echo no)"
mkdir -p "$WORK" "$SRC"
printf '# empty\n' > "$CONFIG_FILE"
: > "$WORK/sql.err"; : > "$WORK/sql.out"
( cd "$REPO_ROOT/backend" && go build -o "$BIN" ./cmd/storage-backfill && go build -o "$PREP" ./cmd/backfill-e2e-prep ) > "$HERE/logs/build-s3-source.log" 2>&1
ck "两个二进制编译成功" "0" "$?"

step "1. 复位：真 MySQL + 平台源桶（A / C）+ 本地诱饵 B"
docker exec -i wc-mysql-ci mysql -uroot -proot --default-character-set=utf8mb4 weichuan_backfill_demo \
  < "$REPO_ROOT/tools/backfill-fixture/seed.sql" > /dev/null 2>&1
ck "seed.sql 灌入成功" "0" "$?"
mysqlq "DELETE FROM storage_backfill_refs; DELETE FROM tenant_storage_configs;" > /dev/null
# 三种字节两两不同：先证明量尺自己有区分度，否则「等于 A、不等于 B」可能恒绿。
printf '0123456789abcdefghij' > "$WORK/bytes-A.bin"        # 20B，第六十六轮同款
printf 'LOCAL-DISK-DECOY-B!' > "$WORK/bytes-B.bin"         # 20B
printf 'PREFIXED-DECOY-C!!!' > "$WORK/bytes-C.bin"         # 20B
cp "$WORK/bytes-B.bin" "$SRC/$SRC_KEY"                      # 本地诱饵：s3 模式根本不该读它
ASHA="$(shasum -a 256 "$WORK/bytes-A.bin" | awk '{print $1}')"
BSHA="$(shasum -a 256 "$WORK/bytes-B.bin" | awk '{print $1}')"
CSHA="$(shasum -a 256 "$WORK/bytes-C.bin" | awk '{print $1}')"
ck "三种字节的 sha 两两不同（量尺有区分度）" "yes" \
  "$([ -n "$ASHA" ] && [ "$ASHA" != "$BSHA" ] && [ "$ASHA" != "$CSHA" ] && [ "$BSHA" != "$CSHA" ] && echo yes || echo no)"
write_settings "$PLATFORM_BUCKET" minio "$S3_AK" "$S3_SK"
ck "sys_setting 写入成功" "0" "$?"
ck "sys_setting 恰好 7 项平台存储配置" "7" "$(mysqlq "SELECT COUNT(*) FROM sys_setting WHERE \`key\` IN ('storage_driver','storage_endpoint','storage_bucket','storage_access_key','storage_secret_key','storage_use_ssl','storage_region')")"
ck "env 诱饵确实指向不存在的桶（覆盖生效才有意义）" "$ENV_DECOY_BUCKET" "$MINIO_BUCKET"
ck "env 诱饵的 driver 是 local（只看 env 必被拒绝）" "local" "$STORAGE_DRIVER"
put_source
ck "平台源桶存在" "EXISTS=1" "$(bucket_of "$PLATFORM_BUCKET")"
ck "平台源桶裸键 $SRC_KEY 的字节 == A" "$ASHA" "$(sha_of "$PLATFORM_BUCKET" "$SRC_KEY")"
ck "平台源桶 p/ 诱饵 $PREFIX_KEY 的字节 == C" "$CSHA" "$(sha_of "$PLATFORM_BUCKET" "$PREFIX_KEY")"
ck "本地诱饵文件 == B" "$BSHA" "$(shasum -a 256 "$SRC/$SRC_KEY" | awk '{print $1}')"
upsert_tenant true
ck "租户桶存在" "EXISTS=1" "$(bucket_of "$TENANT_BUCKET")"
ck "初始时租户桶里没有目标对象" "yes" "$(missing_of "$TENANT_BUCKET" "$KEY")"
BASE="$(checksum)"
SSETTINGS="$(settings_sum)"
# 这两条不是形式主义：查询失败时两边都会拿到空串，「一致」反而恒绿。
ck "基线校验和非空（防止空串对空串的假绿）" "yes" "$([ -n "$(printf '%s' "$BASE" | tr -d ' ')" ] && echo yes || echo no)"
ck "库值查询有效（同上）" "yes" "$([ -n "$(vals)" ] && echo yes || echo no)"
ck "sys_setting 校验和非空（同上）" "yes" "$([ -n "$SSETTINGS" ] && echo yes || echo no)"
ck "基线：三处引用仍是历史 /uploads/ 形态" "/uploads/shared-logo.png /uploads/shared-logo.png /uploads/shared-logo.png " "$(vals)"

step "2. plan（只读，company $CID 子集）"
OUT="$("$BIN" plan --dsn "$DSN" --base-url 'https://card.example.com/uploads' --company "$CID" --out "$PLAN" 2>&1)"; PRC=$?
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "plan 退出码 0" "0" "$PRC"
ck "计划含 3 条待搬 / 1 个去重对象" "yes" "$(has "$OUT" "待搬迁 3 条（去重后 1 个对象要复制）" && echo yes || echo no)"
ck "plan 后业务表校验和不变" "$BASE" "$(checksum)"
ck "计划里的 SourceKey 是裸键（不带 p/ 前缀）" "yes" \
  "$(has "$(cat "$PLAN")" "\"SourceKey\": \"$SRC_KEY\"" && ! has "$(cat "$PLAN")" "\"SourceKey\": \"$PREFIX_KEY\"" && echo yes || echo no)"

step "3. dry-run：--source-store=s3，零写入"
OUT="$("${APPLY_ENV[@]}" "$BIN" apply --plan "$PLAN" --dsn "$DSN" --source-store s3 --target-store tenant --dry-run 2>&1)"; DRC=$?
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "dry-run 退出码 0（说明平台桶探测与源对象 HEAD 都通了）" "0" "$DRC"
ck "dry-run 明确声明未写入" "yes" "$(has "$OUT" "未写入任何数据" && echo yes || echo no)"
ck "dry-run 后业务表校验和不变" "$BASE" "$(checksum)"
ck "dry-run 后库值未变" "/uploads/shared-logo.png /uploads/shared-logo.png /uploads/shared-logo.png " "$(vals)"
ck "dry-run 后租户桶仍无对象" "yes" "$(missing_of "$TENANT_BUCKET" "$KEY")"
ck "dry-run 不动平台源对象" "$ASHA" "$(sha_of "$PLATFORM_BUCKET" "$SRC_KEY")"

step "4. apply --yes：S3 源 → 客户桶，字节必须来自 A"
OUT="$("${APPLY_ENV[@]}" "$BIN" apply --plan "$PLAN" --dsn "$DSN" --source-store s3 --target-store tenant --yes 2>&1)"; ARC=$?
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "apply 退出码 0" "0" "$ARC"
ck "复制 1 个对象、改写 3 条引用" "yes" "$(has "$OUT" "已复制 1 个对象、改写 3 条引用" && echo yes || echo no)"
TSHA="$(sha_of "$TENANT_BUCKET" "$KEY")"
ck "目标 sha 非空（防止空串对空串的假绿）" "yes" "$([ -n "$TSHA" ] && echo yes || echo no)"
ck "客户桶签名 GET 的字节 == 平台源桶 A（真从 S3 读）" "$ASHA" "$TSHA"
ck "客户桶字节 != 本地诱饵 B（没读本地盘）" "yes" "$([ "$TSHA" != "$BSHA" ] && echo yes || echo no)"
ck "客户桶字节 != p/ 前缀诱饵 C（源键没加前缀）" "yes" "$([ "$TSHA" != "$CSHA" ] && echo yes || echo no)"
ck "三处库值都变成租户键" "t/7/shared-logo.png t/7/shared-logo.png t/7/shared-logo.png " "$(vals)"
ck "平台源桶对象仍在且字节未变（源只读）" "$ASHA" "$(sha_of "$PLATFORM_BUCKET" "$SRC_KEY")"
ck "平台源桶里没有被写入目标键 t/$CID/（源桶只读）" "yes" "$(missing_of "$PLATFORM_BUCKET" "$KEY")"
ck "平台本地盘没有出现 t/$CID/ 对象（无静默降级）" "no" "$([ -e "$SRC/t/$CID/$SRC_KEY" ] && echo yes || echo no)"
ck "读平台配置没有写 sys_setting（只读）" "$SSETTINGS" "$(settings_sum)"

step "5. 重跑 / status / rollback"
OUT="$("${APPLY_ENV[@]}" "$BIN" apply --plan "$PLAN" --dsn "$DSN" --source-store s3 --target-store tenant --yes 2>&1)"; RRC=$?
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
ck "目标对象仍在客户桶（R5：回滚不删对象）" "$ASHA" "$(sha_of "$TENANT_BUCKET" "$KEY")"
ck "平台源对象仍在（源端只读，回滚也不碰它）" "$ASHA" "$(sha_of "$PLATFORM_BUCKET" "$SRC_KEY")"

step "6. 负例：参数歧义 / 桶不存在 / 对象不存在 / 配置缺项"
OUT="$("${APPLY_ENV[@]}" "$BIN" apply --plan "$PLAN" --dsn "$DSN" --source-store s3 --source-dir "$SRC" --target-store tenant --yes 2>&1)"; XRC=$?
ck "s3 + --source-dir 同给时拒绝" "yes" "$([ "$XRC" -ne 0 ] && has "$OUT" "--source-dir" && echo yes || echo no)"
OUT="$("${APPLY_ENV[@]}" "$BIN" apply --plan "$PLAN" --dsn "$DSN" --source-store ftp --target-store tenant --yes 2>&1)"; XRC=$?
ck "未知 --source-store 时拒绝" "yes" "$([ "$XRC" -ne 0 ] && has "$OUT" "未知 --source-store" && echo yes || echo no)"

# 6a. 桶名错 → 必须说「平台源桶不存在」，不能退化成「源对象不存在」。
write_settings "wc-no-such-bucket-67" minio "$S3_AK" "$S3_SK"
OUT="$("${APPLY_ENV[@]}" "$BIN" apply --plan "$PLAN" --dsn "$DSN" --source-store s3 --target-store tenant --yes 2>&1)"; XRC=$?
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "桶名错时显式失败" "yes" "$([ "$XRC" -ne 0 ] && echo yes || echo no)"
ck "报错点名「平台源桶」而不是只说对象丢了" "yes" "$(has "$OUT" "平台源桶" && has "$OUT" "不存在" && echo yes || echo no)"
ck "报错明确澄清「这不是「源对象不存在」」" "yes" "$(has "$OUT" "这不是「源对象不存在」" && echo yes || echo no)"
ck "桶名错时没有出现「无法复制成」（没走到对象缺失分支）" "yes" "$(! has "$OUT" "无法复制成" && echo yes || echo no)"

# 6b. 桶在、对象不在 → 必须说「源对象不存在」，与上一条形成对照。
write_settings "$PLATFORM_BUCKET" minio "$S3_AK" "$S3_SK"
"$PREP" -mode delsrc -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" \
  -bucket "$PLATFORM_BUCKET" -company "$CID" -key "$SRC_KEY" >/dev/null 2>&1
OUT="$("${APPLY_ENV[@]}" "$BIN" apply --plan "$PLAN" --dsn "$DSN" --source-store s3 --target-store tenant --yes 2>&1)"; XRC=$?
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "对象缺失时退出码 2（数据需人工处理）" "2" "$XRC"
ck "报错是「源对象 ... 不存在，无法复制成」" "yes" "$(has "$OUT" "无法复制成" && echo yes || echo no)"
ck "对象缺失时没有误报「平台源桶不存在」" "yes" "$(! has "$OUT" "平台源桶" && echo yes || echo no)"
put_source

# 6c. driver=oss / 缺凭据 / 删掉界面配置回落 env 诱饵：都必须显式失败且不回退本地盘。
write_settings "$PLATFORM_BUCKET" oss "$S3_AK" "$S3_SK"
OUT="$("${APPLY_ENV[@]}" "$BIN" apply --plan "$PLAN" --dsn "$DSN" --source-store s3 --target-store tenant --yes 2>&1)"; XRC=$?
ck "driver=oss 显式拒绝" "yes" "$([ "$XRC" -ne 0 ] && has "$OUT" "oss" && echo yes || echo no)"
ck "driver=oss 时写明不回退本地盘" "yes" "$(has "$OUT" "不回退本地盘" && echo yes || echo no)"

write_settings "$PLATFORM_BUCKET" minio "$S3_AK" "$S3_SK"
mysqlq "DELETE FROM sys_setting WHERE \`key\`='storage_bucket'" >/dev/null
OUT="$("${APPLY_ENV[@]}" "$BIN" apply --plan "$PLAN" --dsn "$DSN" --source-store s3 --target-store tenant --yes 2>&1)"; XRC=$?
printf '%s\n' "$OUT" | tee -a "$LOG"
ck "界面配置被删后回落到 env 诱饵桶，显式失败" "yes" "$([ "$XRC" -ne 0 ] && has "$OUT" "$ENV_DECOY_BUCKET" && echo yes || echo no)"
ck "回落时仍报「平台源桶」而非静默成功" "yes" "$(has "$OUT" "平台源桶" && echo yes || echo no)"

write_settings "$PLATFORM_BUCKET" minio "$S3_AK" "$S3_SK"
# 两个来源都要清：只清 env 时 sys_setting 里还有正确凭据，负例会恒绿。
mysqlq "DELETE FROM sys_setting WHERE \`key\`='storage_access_key'" >/dev/null
OUT="$(env -u MINIO_ACCESS_KEY "${APPLY_ENV_NO_AK[@]}" "$BIN" apply --plan "$PLAN" --dsn "$DSN" --source-store s3 --target-store tenant --yes 2>&1)"; XRC=$?
ck "平台 S3 凭据缺一时显式失败" "yes" "$([ "$XRC" -ne 0 ] && has "$OUT" "MINIO_ACCESS_KEY" && echo yes || echo no)"
ck "凭据缺失时写明不回退本地盘" "yes" "$(has "$OUT" "不回退本地盘" && echo yes || echo no)"

# 6d. 第六十六轮既有负例在新组合下回归。
# 必须先把 sys_setting 恢复成正确值：6c 删了 storage_access_key，不恢复的话
# 后面每条 apply 都先撞「源端凭据缺失」，根本走不到目标端，负例会假绿。
write_settings "$PLATFORM_BUCKET" minio "$S3_AK" "$S3_SK"
ck "6d 前平台配置已恢复（7 项）" "7" "$(mysqlq "SELECT COUNT(*) FROM sys_setting WHERE \`key\` IN ('storage_driver','storage_endpoint','storage_bucket','storage_access_key','storage_secret_key','storage_use_ssl','storage_region')")"
OUT="$("${APPLY_ENV[@]}" "$BIN" apply --plan "$PLAN" --dsn "$DSN" --source-store s3 --target-store tenant --target-dir "$WORK/should-not-exist" --yes 2>&1)"; XRC=$?
ck "tenant + --target-dir 同给时拒绝" "yes" "$([ "$XRC" -ne 0 ] && has "$OUT" "--target-dir" && echo yes || echo no)"
"$PREP" -mode delete -dsn "$DSN" -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" -bucket "$TENANT_BUCKET" -company "$CID" >/dev/null 2>&1
OUT="$("${APPLY_ENV[@]}" "$BIN" apply --plan "$PLAN" --dsn "$DSN" --source-store s3 --target-store tenant --yes 2>&1)"; XRC=$?
ck "缺租户配置行时显式失败" "yes" "$([ "$XRC" -ne 0 ] && has "$OUT" "没有生效的自有桶配置" && echo yes || echo no)"
upsert_tenant false
OUT="$(env -u PII_ENC_KEY "${APPLY_ENV[@]:1}" "$BIN" apply --plan "$PLAN" --dsn "$DSN" --source-store s3 --target-store tenant --yes 2>&1)"; XRC=$?
ck "内置 dev PII 密钥时拒绝真桶目标" "yes" "$([ "$XRC" -ne 0 ] && has "$OUT" "PII_ENC_KEY" && echo yes || echo no)"

ck "全部负例跑完后业务表校验和仍等于基线" "$BASE" "$(checksum)"
ck "负例跑完后库值仍是历史 /uploads/ 形态" "/uploads/shared-logo.png /uploads/shared-logo.png /uploads/shared-logo.png " "$(vals)"
ck "负例跑完后平台本地盘仍无 t/$CID/ 对象" "no" "$([ -e "$SRC/t/$CID/$SRC_KEY" ] && echo yes || echo no)"
ck "负例跑完后目标对象仍在客户桶（R5）" "$ASHA" "$(sha_of "$TENANT_BUCKET" "$KEY")"
ck "负例跑完后平台源对象仍在" "$ASHA" "$(sha_of "$PLATFORM_BUCKET" "$SRC_KEY")"

printf '\n== 结论：%s ==\n' "$([ "$rc" -eq 0 ] && echo '全部通过' || echo '有断言失败')" | tee -a "$LOG"
printf '取证日志：%s\n' "$LOG"
exit "$rc"
