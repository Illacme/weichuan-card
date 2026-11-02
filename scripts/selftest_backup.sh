#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# 备份链自检（不依赖 docker、不碰任何真实数据）。
#
# 备份脚本是「只有出事那天才会真正执行」的那种代码：平时全绿、坏了自己也不会叫。
# 这个自检把最关键的几条行为钉死，防止后续改动悄悄破坏：
#   1. 备份能产出图片归档 + 加密密钥包；
#   2. 恢复回来逐字节一致（备份 ≠ 能恢复，这两件事要分开验）；
#   3. 密钥包里没有明文（加密真的生效了）；
#   4. 有密钥文件却没给 BACKUP_ENC_KEY → 整次失败且不留半成品；
#   5. 口令错误时恢复硬停，不把垃圾写进 runtime.env。
#
# 用法：scripts/selftest_backup.sh   （CI 里跑这个，退出码非 0 即失败）
# ─────────────────────────────────────────────────────────────
set -uo pipefail

SCRIPTS="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(mktemp -d "${TMPDIR:-/tmp}/weichuan-bk-selftest-XXXXXX")"
cleanup() { rm -rf "$ROOT"; }
trap cleanup EXIT

PASS=0
FAIL=0
ok()  { echo "  [PASS] $*"; PASS=$((PASS + 1)); }
bad() { echo "  [FAIL] $*"; FAIL=$((FAIL + 1)); }
head_() { echo; echo "── $* ──"; }

# ── 夹具：两个图片（含子目录）+ 两个密钥文件 ──
mkdir -p "$ROOT/src/uploads/sub" "$ROOT/src/data" "$ROOT/backups"
printf 'IMG-A' > "$ROOT/src/uploads/avatar.png"
printf 'IMG-B' > "$ROOT/src/uploads/sub/case.jpg"
printf 'PII_ENC_KEY=Zm9v\nJWT_KEY=jwt\nADMIN_PASS=admin\n' > "$ROOT/src/data/runtime.env"
printf 'LICENSE\n' > "$ROOT/src/data/license.key"

BK_ENV=(BACKUP_SOURCE=host
        BACKUP_DIR="$ROOT/backups"
        UPLOAD_DIR="$ROOT/src/uploads"
        CONFIG_FILE="$ROOT/src/data/runtime.env"
        LICENSE_FILE="$ROOT/src/data/license.key")

head_ "1. 备份产出（host 模式）"
if env "${BK_ENV[@]}" BACKUP_ENC_KEY='selftest-pass' "$SCRIPTS/backup_files.sh" >"$ROOT/bk.log" 2>&1; then
  ok "backup_files.sh 退出码 0"
else
  bad "backup_files.sh 失败：$(tail -3 "$ROOT/bk.log" | tr '\n' ' ')"
fi

up_count="$(ls "$ROOT"/backups/uploads-*.tar.gz 2>/dev/null | wc -l | tr -d ' ')"
sec_count="$(ls "$ROOT"/backups/secrets-*.tar.gz.enc 2>/dev/null | wc -l | tr -d ' ')"
[ "$up_count" = "1" ] && ok "产出图片归档 uploads-*.tar.gz" || bad "没有图片归档"
[ "$sec_count" = "1" ] && ok "产出加密密钥包 secrets-*.enc" || bad "没有密钥包"

# 密钥包必须是密文：明文口令/密钥不该出现在文件里
if [ "$sec_count" = "1" ] && ! grep -a -q 'ADMIN_PASS=admin' "$ROOT"/backups/secrets-*.tar.gz.enc; then
  ok "密钥包内无明文（加密生效）"
else
  bad "密钥包疑似明文（加密未生效）"
fi

head_ "2. 恢复并逐字节比对"
mkdir -p "$ROOT/out"
if env "${BK_ENV[@]}" BACKUP_ENC_KEY='selftest-pass' \
   "$SCRIPTS/restore_files.sh" --all --target "$ROOT/out" >"$ROOT/rs.log" 2>&1; then
  ok "restore_files.sh 退出码 0"
else
  bad "restore_files.sh 失败：$(tail -3 "$ROOT/rs.log" | tr '\n' ' ')"
fi

if diff -r "$ROOT/src/uploads" "$ROOT/out/uploads" >/dev/null 2>&1; then
  ok "图片恢复后与源目录一致"
else
  bad "图片恢复后与源不一致"
fi
if cmp -s "$ROOT/src/data/runtime.env" "$ROOT/out/data/runtime.env"; then
  ok "runtime.env 恢复后一致"
else
  bad "runtime.env 恢复后不一致"
fi
if cmp -s "$ROOT/src/data/license.key" "$ROOT/out/data/license.key"; then
  ok "license.key 恢复后一致"
else
  bad "license.key 恢复后不一致"
fi

head_ "3. 有密钥文件但没给 BACKUP_ENC_KEY → 必须失败且不留产物"
rm -rf "$ROOT/backups2"; mkdir -p "$ROOT/backups2"
if env -u BACKUP_ENC_KEY "${BK_ENV[@]}" BACKUP_DIR="$ROOT/backups2" \
   "$SCRIPTS/backup_files.sh" >"$ROOT/nokey.log" 2>&1; then
  bad "缺口令却成功了（应该失败：会产出一份救不回 PII 的备份）"
else
  ok "缺口令时退出码非 0"
fi
left="$(ls -A "$ROOT/backups2" 2>/dev/null | wc -l | tr -d ' ')"
[ "$left" = "0" ] && ok "失败后没有留下半成品" || bad "失败后残留 ${left} 个文件"

head_ "4. 口令错误时恢复硬停"
mkdir -p "$ROOT/out_bad"
if env "${BK_ENV[@]}" BACKUP_ENC_KEY='wrong-pass' \
   "$SCRIPTS/restore_files.sh" --secrets "$ROOT"/backups/secrets-*.tar.gz.enc \
   --target "$ROOT/out_bad" >"$ROOT/badkey.log" 2>&1; then
  bad "错口令竟然恢复成功"
else
  ok "错口令时退出码非 0"
fi
if [ -z "$(find "$ROOT/out_bad" -name 'runtime.env' 2>/dev/null)" ]; then
  ok "错口令时未写出 runtime.env（不会把垃圾写进配置）"
else
  bad "错口令时写出了 runtime.env"
fi

echo
echo "════════ 自检结果：${PASS} 通过 / ${FAIL} 失败 ════════"
[ "$FAIL" -eq 0 ] || exit 1
echo "备份链自检通过。"
