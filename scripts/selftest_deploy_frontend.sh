#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# 发版脚本「非交互口令」自检（不连任何服务器、不需要真口令）。
#
# deploy_frontend.sh 是「只在发版那天跑一次」的脚本，口令注入坏了平时不会叫：
# 最坏的失败形态不是报错，而是**又退回去问密码**，CI/无人值守就永远卡住。
# 这里用假 ssh / 假 sshpass 顶替 PATH，把下面几条钉死：
#   1. --password / --password= / DEPLOY_PASSWORD 三条入口都能走通，CLI 优先于 env；
#   2. 口令不进脚本输出、不进 askpass 文件正文、不进 argv；
#   3. askpass 文件 700、执行时能吐出正确口令、脚本退出后一定被清掉；
#   4. OpenSSH >= 8.4 走 SSH_ASKPASS_REQUIRE=force；老版本自动回退 sshpass；
#      两者都没有时**明确报错**，不静默退化成交互提示；
#   5. 不带口令时完全不碰 askpass（配了免密的用户不能被影响）；
#   6. --password 缺值 / 把下一个选项吃成口令时能立刻拦下。
#
# 用法：bash scripts/selftest_deploy_frontend.sh   （CI 里跑这个，退出码非 0 即失败）
# ─────────────────────────────────────────────────────────────
set -uo pipefail

SCRIPTS="$(cd "$(dirname "$0")" && pwd)"
DEPLOY="$SCRIPTS/deploy_frontend.sh"
ROOT=$(mktemp -d "${TMPDIR:-/tmp}/weichuan-deploy-selftest-XXXXXX")
cleanup() { rm -rf "$ROOT"; }
trap cleanup EXIT

PASS=0
FAIL=0
ok()  { echo "  [PASS] $*"; PASS=$((PASS + 1)); }
bad() { echo "  [FAIL] $*"; FAIL=$((FAIL + 1)); }
head_() { echo; echo "── $* ──"; }
# 断言文件里含/不含某个字面串（-F 固定串，避免口令里的符号被当正则）
has() { grep -qF -- "$2" "$1" 2>/dev/null; }

# 低熵假口令：只为验证数据流，不是任何真实凭据
FAKE_PW='selftest-plain-pw'
FAKE_PW2='another-plain-pw'

mkdir -p "$ROOT/bin" "$ROOT/bin-pass" "$ROOT/tmp" "$ROOT/dist/assets"
printf '<script src="/assets/index-abc.js"></script>\n' > "$ROOT/dist/index.html"
printf 'JS' > "$ROOT/dist/assets/index-abc.js"

FAKE_SSH_LOG="$ROOT/ssh.log"
FAKE_PASS_LOG="$ROOT/sshpass.log"
ASKPASS_COPY="$ROOT/askpass.copy"
ASKPASS_MODE="$ROOT/askpass.mode"
ASKPASS_OUTPUT="$ROOT/askpass.out"
OUT="$ROOT/deploy.out"

# ── 假 ssh：记录现场、模拟远端命令、造出 ControlMaster socket ──
cat > "$ROOT/bin/ssh" <<'FAKE_SSH'
#!/bin/sh
{
    printf 'CALL args=%s\n' "$*"
    printf 'ASKPASS=%s\n' "${SSH_ASKPASS-}"
    printf 'REQUIRE=%s\n' "${SSH_ASKPASS_REQUIRE-}"
    printf 'PWVAR=%s\n' "${WEICHUAN_DEPLOY_PASSWORD-}"
    printf 'SSHPASS=%s\n' "${SSHPASS-}"
} >> "$FAKE_SSH_LOG"

# 版本探测（脚本据此选通道）
case "$*" in
    *-V*) echo "OpenSSH_${FAKE_SSH_VERSION:-10.3p1}, LibreSSL 3.3.6" >&2; exit 0 ;;
esac

# askpass 文件在脚本退出时会被删，只能当场把现场复制出来
if [ -n "${SSH_ASKPASS-}" ] && [ -f "$SSH_ASKPASS" ]; then
    cp "$SSH_ASKPASS" "$ASKPASS_COPY"
    (stat -c '%a' "$SSH_ASKPASS" 2>/dev/null || stat -f '%Lp' "$SSH_ASKPASS") > "$ASKPASS_MODE"
    "$SSH_ASKPASS" > "$ASKPASS_OUTPUT" 2>/dev/null || true
fi

# 让脚本的「等 ControlPath socket 就绪」立刻通过
for a in "$@"; do
    case "$a" in
        ControlPath=*)
            python3 -c 'import socket,sys; s=socket.socket(socket.AF_UNIX); s.bind(sys.argv[1]); s.close()' \
                "${a#ControlPath=}" 2>/dev/null || true
            ;;
    esac
done

case "$*" in
    *'command -v rsync'*) exit 0 ;;
    *'if [ -d'*) printf 'DIR_OK\n3\nindex-old.js\n'; exit 0 ;;
esac
cat >/dev/null 2>&1 || true
exit 0
FAKE_SSH

# ── 假 sshpass：只验「wrapper 会调它、口令经 SSHPASS 到达」，再转交假 ssh ──
cat > "$ROOT/bin-pass/sshpass" <<'FAKE_SSHPASS'
#!/bin/sh
printf 'SSHPASS_SEEN=%s args=%s\n' "${SSHPASS-}" "$*" >> "$FAKE_PASS_LOG"
# 真 sshpass 的用法是 `sshpass -e ssh <ssh 参数...>`，这里丢掉前两个再转交假 ssh
shift 2
exec ssh "$@"
FAKE_SSHPASS

chmod 700 "$ROOT/bin/ssh" "$ROOT/bin-pass/sshpass"

# 公共环境：DEPLOY_HOST 非空才走 ssh 通道；tar 模式免掉 rsync 依赖；
# DEPLOY_URL 空 + --no-verify 跳过联网自检；TMPDIR 收进 ROOT 便于统一清理。
COMMON=(env
    "PATH=$ROOT/bin:$PATH"
    DEPLOY_HOST=fake@127.0.0.1
    DEPLOY_TRANSFER=tar
    DEPLOY_URL=
    "DIST_DIR=$ROOT/dist"
    "TMPDIR=$ROOT/tmp"
    "FAKE_SSH_LOG=$FAKE_SSH_LOG"
    "FAKE_PASS_LOG=$FAKE_PASS_LOG"
    "ASKPASS_COPY=$ASKPASS_COPY"
    "ASKPASS_MODE=$ASKPASS_MODE"
    "ASKPASS_OUTPUT=$ASKPASS_OUTPUT"
    "OUT=$OUT")

reset_probe() {
    : > "$FAKE_SSH_LOG"
    : > "$FAKE_PASS_LOG"
    rm -f "$ASKPASS_COPY" "$ASKPASS_MODE" "$ASKPASS_OUTPUT" "$OUT"
    rm -f "$ROOT"/tmp/weichuan-askpass-* "$ROOT"/tmp/weichuan-sshwrap-* "$ROOT"/tmp/weichuan-deploy-*
}

no_stale_tmp() {
    # 脚本退出后不能留下 askpass / ssh wrapper（口令虽不在里面，但_tmp_ 不该堆垃圾）
    local left
    left=$(ls "$ROOT"/tmp/weichuan-askpass-* "$ROOT"/tmp/weichuan-sshwrap-* 2>/dev/null)
    [ -z "$left" ]
}

# ─────────────────────────────────────────────────────────────
head_ "0. 语法与帮助"
if bash -n "$DEPLOY"; then ok "bash -n 通过"; else bad "bash -n 失败"; fi
"$DEPLOY" -h > "$ROOT/help.txt" 2>&1
if has "$ROOT/help.txt" '--password' && has "$ROOT/help.txt" 'DEPLOY_PASSWORD'; then
    ok "-h 里同时有 --password 与 DEPLOY_PASSWORD 说明"
else
    bad "-h 缺少口令相关说明"
fi

# ─────────────────────────────────────────────────────────────
head_ "1. --password 命令行入口（OpenSSH 10.3 → SSH_ASKPASS）"
reset_probe
"${COMMON[@]}" "$DEPLOY" -n --no-verify --password "$FAKE_PW" > "$OUT" 2>&1
RC=$?
if [ "$RC" -eq 0 ]; then ok "退出码 0（全程没卡交互）"; else bad "退出码 ${RC}：$(tail -3 "$OUT" | tr '\n' ' ')"; fi
if has "$OUT" '非交互口令' && has "$OUT" 'SSH_ASKPASS'; then
    ok "输出说明走的是 SSH_ASKPASS 通道"
else
    bad "输出里没有通道说明"
fi
if has "$OUT" "$FAKE_PW"; then bad "口令泄漏到了脚本输出"; else ok "脚本输出不含口令"; fi
if has "$OUT" 'shell history / ps'; then ok "命令行入口给出了 history/ps 安全警告"; else bad "缺少安全警告"; fi

if has "$FAKE_SSH_LOG" 'REQUIRE=force'; then ok "ssh 子进程拿到 SSH_ASKPASS_REQUIRE=force"; else bad "没设置 force"; fi
if has "$FAKE_SSH_LOG" "PWVAR=$FAKE_PW"; then ok "口令经环境变量送达 ssh"; else bad "口令没到 ssh"; fi
if has "$FAKE_SSH_LOG" "ASKPASS=$ROOT/tmp/weichuan-askpass-"; then
    ok "SSH_ASKPASS 指向临时 askpass 文件"
else
    bad "SSH_ASKPASS 未设置或路径不对"
fi

if [ -f "$ASKPASS_COPY" ]; then
    if has "$ASKPASS_COPY" "$FAKE_PW"; then
        bad "askpass 文件正文里写了口令（应只读环境变量）"
    else
        ok "askpass 文件正文不含口令"
    fi
    if has "$ASKPASS_COPY" 'WEICHUAN_DEPLOY_PASSWORD'; then
        ok "askpass 文件只引用环境变量"
    else
        bad "askpass 文件内容不是环境变量打印器"
    fi
else
    bad "没抓到 askpass 文件现场"
fi
if [ -f "$ASKPASS_MODE" ] && [ "$(cat "$ASKPASS_MODE")" = "700" ]; then
    ok "askpass 文件权限 700"
else
    bad "askpass 权限不是 700（实际 $(cat "$ASKPASS_MODE" 2>/dev/null)）"
fi
if [ -f "$ASKPASS_OUTPUT" ] && [ "$(cat "$ASKPASS_OUTPUT")" = "$FAKE_PW" ]; then
    ok "执行 askpass 能吐出正确口令"
else
    bad "askpass 执行结果不对（实际 $(cat "$ASKPASS_OUTPUT" 2>/dev/null)）"
fi
if no_stale_tmp; then ok "脚本退出后临时 askpass 已清理"; else bad "临时 askpass 没清掉"; fi

# ─────────────────────────────────────────────────────────────
head_ "2. --password= 与 DEPLOY_PASSWORD 入口"
reset_probe
"${COMMON[@]}" "$DEPLOY" -n --no-verify "--password=$FAKE_PW" > "$OUT" 2>&1
RC=$?
if [ "$RC" -eq 0 ] && has "$FAKE_SSH_LOG" "PWVAR=$FAKE_PW"; then
    ok "--password= 等号形式生效"
else
    bad "--password= 等号形式没生效（rc=${RC}）"
fi

reset_probe
"${COMMON[@]}" "DEPLOY_PASSWORD=$FAKE_PW" "$DEPLOY" -n --no-verify > "$OUT" 2>&1
RC=$?
if [ "$RC" -eq 0 ] && has "$FAKE_SSH_LOG" "PWVAR=$FAKE_PW"; then
    ok "DEPLOY_PASSWORD 环境变量生效"
else
    bad "DEPLOY_PASSWORD 没生效（rc=${RC}）"
fi
if has "$OUT" 'shell history / ps'; then
    bad "环境变量入口不该报命令行安全警告"
else
    ok "环境变量入口没有多余的命令行警告"
fi
if has "$OUT" "$FAKE_PW"; then bad "环境变量入口把口令打进了输出"; else ok "环境变量入口输出不含口令"; fi

reset_probe
"${COMMON[@]}" "DEPLOY_PASSWORD=$FAKE_PW2" "$DEPLOY" -n --no-verify --password "$FAKE_PW" > "$OUT" 2>&1
if has "$FAKE_SSH_LOG" "PWVAR=$FAKE_PW" && ! has "$FAKE_SSH_LOG" "PWVAR=$FAKE_PW2"; then
    ok "--password 优先级高于 DEPLOY_PASSWORD"
else
    bad "CLI / env 优先级不对"
fi

# ─────────────────────────────────────────────────────────────
head_ "3. 参数防呆"
reset_probe
"${COMMON[@]}" "$DEPLOY" -n --no-verify --password > "$OUT" 2>&1
RC=$?
if [ "$RC" -ne 0 ] && has "$OUT" '后面要跟口令'; then ok "--password 缺值立即报错"; else bad "--password 缺值没拦住（rc=${RC}）"; fi

reset_probe
"${COMMON[@]}" "$DEPLOY" --password --build > "$OUT" 2>&1
RC=$?
if [ "$RC" -ne 0 ] && has "$OUT" '漏了口令'; then ok "--password 误吃下一个选项被拦住"; else bad "误吃选项没拦住（rc=${RC}）"; fi

reset_probe
"${COMMON[@]}" "$DEPLOY" -n --no-verify --password= > "$OUT" 2>&1
RC=$?
if [ "$RC" -ne 0 ] && has "$OUT" '不能为空'; then ok "--password= 空值被拦住"; else bad "空值没拦住（rc=${RC}）"; fi

# ─────────────────────────────────────────────────────────────
head_ "4. 不带口令时不干扰免密用户（回归）"
reset_probe
"${COMMON[@]}" "$DEPLOY" -n --no-verify > "$OUT" 2>&1
RC=$?
if [ "$RC" -eq 0 ]; then ok "免密路径仍退出 0"; else bad "免密路径挂了（rc=${RC}）"; fi
if has "$FAKE_SSH_LOG" 'REQUIRE=force' || has "$FAKE_SSH_LOG" "ASKPASS=$ROOT/tmp/"; then
    bad "没给口令却设置了 askpass"
else
    ok "没给口令时完全不碰 askpass"
fi
if has "$OUT" '非交互口令'; then bad "没给口令却打印了口令段"; else ok "没给口令时无多余输出"; fi

# 本地模式（DEPLOY_HOST= 显式置空）压根不连 ssh，带口令应明确忽略而不是白建 askpass
reset_probe
"${COMMON[@]}" "DEPLOY_HOST=" "DEPLOY_PATH=$ROOT/target" \
    "$DEPLOY" -n --no-verify --password "$FAKE_PW" > "$OUT" 2>&1
RC=$?
if [ "${RC}" -eq 0 ] && has "$OUT" '用不到 ssh 口令'; then
    ok "本地模式明确忽略口令"
else
    bad "本地模式没正确忽略口令（rc=${RC}）"
fi
if has "$FAKE_SSH_LOG" 'REQUIRE=force'; then bad "本地模式不该设置 askpass"; else ok "本地模式不碰 askpass"; fi
if no_stale_tmp; then ok "本地模式不残留临时文件"; else bad "本地模式残留临时文件"; fi

# ─────────────────────────────────────────────────────────────
head_ "5. 老 OpenSSH 回退 sshpass"
reset_probe
"${COMMON[@]}" "PATH=$ROOT/bin-pass:$ROOT/bin:$PATH" "FAKE_SSH_VERSION=7.9p1" \
    "$DEPLOY" -n --no-verify --password "$FAKE_PW" > "$OUT" 2>&1
RC=$?
if [ "$RC" -eq 0 ] && has "$OUT" '方式：sshpass'; then
    ok "OpenSSH 7.9 自动回退 sshpass"
else
    bad "sshpass 回退没生效（rc=${RC}）：$(tail -3 "$OUT" | tr '\n' ' ')"
fi
if has "$FAKE_PASS_LOG" "SSHPASS_SEEN=$FAKE_PW"; then
    ok "口令经 SSHPASS 到达 sshpass wrapper"
else
    bad "sshpass wrapper 没拿到口令"
fi
if has "$FAKE_SSH_LOG" 'REQUIRE=force'; then bad "sshpass 通道不该再设 force"; else ok "sshpass 通道不设 force"; fi
if no_stale_tmp; then ok "sshpass wrapper 退出后已清理"; else bad "sshpass wrapper 没清掉"; fi

# ─────────────────────────────────────────────────────────────
head_ "6. 两条通道都不可用：明确报错，不静默卡交互"
reset_probe
# 用窄 PATH 把系统里可能装过的 sshpass 也排除掉，确保真的「两条通道都没有」
"${COMMON[@]}" "PATH=$ROOT/bin:/usr/bin:/bin" "FAKE_SSH_VERSION=7.9p1" \
    "$DEPLOY" -n --no-verify --password "$FAKE_PW" > "$OUT" 2>&1
RC=$?
if env PATH="$ROOT/bin:/usr/bin:/bin" command -v sshpass >/dev/null 2>&1; then
    bad "测试夹具没能把系统 sshpass 排除掉，本用例无效"
elif [ "$RC" -ne 0 ] && has "$OUT" 'sshpass' && has "$OUT" 'ssh-copy-id'; then
    ok "缺能力时直接报错并给出三条出路"
else
    bad "缺能力时没有明确报错（rc=${RC}）"
fi
if has "$OUT" "$FAKE_PW"; then bad "报错信息里泄漏了口令"; else ok "报错信息不含口令"; fi

# ─────────────────────────────────────────────────────────────
echo
echo "结果：PASS=$PASS FAIL=$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
