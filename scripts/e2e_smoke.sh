#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# 端到端冒烟：起一个真实进程，从「安装向导」一路走到「小程序侧读到数据」。
#
#   scripts/e2e_smoke.sh                 # 自动编译后端二进制并跑
#   SERVER_BIN=/path/to/server scripts/e2e_smoke.sh
#   scripts/e2e_smoke.sh --keep          # 保留临时目录（排查用）
#
# 它覆盖的不是单元/接口测试已经覆盖的那部分（业务断言），而是**只有真跑起来
# 才会暴露的那一段**：
#   - 安装向导能不能真的建库建表、写回配置、并让进程自重启后可用；
#   - 登录链路（httpOnly Cookie + 限流器 + 会话）在真实 HTTP 下通不通；
#   - 后台写进去的数据，小程序侧那个**公开接口**能不能读到（两端衔接处最易断）。
# 这三层任何一层断了，单测全绿也照样上不了线。
#
# 全程用临时目录 + 临时端口，不碰任何现有数据；结束即清理。
# 退出码：0 全通过 / 1 有步骤失败 / 2 环境不完整（缺 curl / python3 / go）。
# ─────────────────────────────────────────────────────────────
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
KEEP=0
for arg in "$@"; do
  case "$arg" in
    --keep) KEEP=1 ;;
    -h|--help) awk 'NR<=2{next} /^# ─/{exit} /^#/{sub(/^# ?/,""); print}' "$0"; exit 0 ;;
    *) echo "[smoke] 未知参数：$arg" >&2; exit 2 ;;
  esac
done

command -v curl >/dev/null 2>&1 || { echo "[smoke] 需要 curl" >&2; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "[smoke] 需要 python3（解析 JSON）" >&2; exit 2; }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/weichuan-smoke-XXXXXX")"
SERVER_PID="" # 先占位：cleanup 的 trap 在进程还没起来时也会被调用（set -u 下不能是未绑定变量）
cleanup() {
  [ -n "${SERVER_PID:-}" ] && kill "$SERVER_PID" 2>/dev/null
  wait "$SERVER_PID" 2>/dev/null
  if [ "$KEEP" = "1" ]; then echo; echo "[smoke] 临时目录保留：$WORK"; else rm -rf "$WORK"; fi
}
trap cleanup EXIT

COOKIES="$WORK/cookies.txt"
: > "$COOKIES"
FAILED=0
step() { echo; echo "── $* ──"; }
pass() { echo "  [PASS] $*"; }
fail() { echo "  [FAIL] $*" >&2; FAILED=$((FAILED + 1)); }
info() { echo "  [INFO] $*"; }

# 取一个空闲端口（不写死：写死的端口在 CI 上常被别的 job 占着）
PORT="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')"
BASE="http://127.0.0.1:$PORT"

# 统一走 127.0.0.1，显式关掉代理：CI 上若设了 http_proxy，curl 会把本机请求
# 也发给代理，表现为「服务明明起来了但 curl 连不上」，排查很费时间。
api() {
  local method="$1" path="$2" body="${3:-}"
  local args=(-s -o "$WORK/last.json" -w '%{http_code}' --noproxy '*'
    -b "$COOKIES" -c "$COOKIES" -X "$method" "$BASE$path")
  if [ -n "$body" ]; then
    args+=(-H 'Content-Type: application/json' -d "$body")
  fi
  curl "${args[@]}"
}

# jget <路径>：从上次响应里取值（支持 data.id 这样的点路径）
jget() {
  python3 - "$WORK/last.json" "$1" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    print(""); raise SystemExit(0)
cur = d
for k in sys.argv[2].split('.'):
    if k == "":
        continue
    if isinstance(cur, list):
        try:
            cur = cur[int(k)]
        except Exception:
            print(""); raise SystemExit(0)
    elif isinstance(cur, dict):
        cur = cur.get(k)
    else:
        print(""); raise SystemExit(0)
print("" if cur is None else cur)
PY
}

expect_code() { # expect_code <期望的 code> <步骤说明>
  local want="$1" what="$2" got
  got="$(jget code)"
  if [ "$got" = "$want" ]; then
    pass "${what}（code=${got}）"
  else
    fail "${what}：期望 code=${want}，实际 code=${got:-空} msg=$(jget msg)"
  fi
}

wait_for() { # wait_for <说明> <curl 成功判据命令...>
  local what="$1"; shift
  local i
  for i in $(seq 1 60); do
    if "$@" >/dev/null 2>&1; then
      pass "${what}（${i} 次探活内）"
      return 0
    fi
    sleep 1
  done
  fail "${what}：等待超时"
  return 1
}

# ── 0. 准备二进制 ──
step "0. 准备后端二进制"
if [ -z "${SERVER_BIN:-}" ]; then
  command -v go >/dev/null 2>&1 || { echo "[smoke] 未设 SERVER_BIN 且没有 go，无法编译" >&2; exit 2; }
  (cd "$REPO_ROOT/backend" && go build -o "$WORK/server" ./cmd/server) || { echo "[smoke] 编译失败" >&2; exit 2; }
  SERVER_BIN="$WORK/server"
fi
[ -x "$SERVER_BIN" ] || { echo "[smoke] SERVER_BIN 不可执行：$SERVER_BIN" >&2; exit 2; }
info "使用 $SERVER_BIN"

# ── 1. 起服务（未配置模式，走安装向导）──
# CONFIG_FILE 必须指向临时目录：安装向导会把 DB_DSN / JWT_KEY 写进去，
# 写到仓库里的 data/runtime.env 会污染真实配置。
step "1. 启动服务（未配置模式）"
PORT="$PORT" CONFIG_FILE="$WORK/runtime.env" STORAGE_DRIVER=local \
  UPLOAD_DIR="$WORK/uploads" TZ=Asia/Shanghai GIN_MODE=release \
  "$SERVER_BIN" > "$WORK/server.log" 2>&1 &
SERVER_PID=$!
for i in $(seq 1 60); do
  code="$(curl -s --noproxy '*' -o "$WORK/status.json" -w '%{http_code}' "$BASE/api/setup/status" || true)"
  [ "$code" = "200" ] && break
  sleep 1
done
if [ "${code:-}" != "200" ]; then
  echo "  [FAIL] 服务未起来（端口 ${PORT}）：" >&2
  tail -20 "$WORK/server.log" >&2 || true
  exit 1
fi
pass "服务已监听 ${PORT}（未配置模式）"

# ── 2. 安装向导建库建管理员 ──
step "2. 安装向导（SQLite）"
api POST /api/setup/install "{
  \"engine\": \"sqlite\", \"sqlitePath\": \"$WORK/smoke.db\",
  \"adminUser\": \"smokeadmin\", \"adminPass\": \"SmokeTest123\", \"adminPass2\": \"SmokeTest123\",
  \"jwtKey\": \"\"
}" > /dev/null
expect_code 0 "安装完成"
info "安装后服务会自重启，等待 /healthz 就绪"
for i in $(seq 1 60); do
  code="$(curl -s --noproxy '*' -o "$WORK/health.json" -w '%{http_code}' "$BASE/healthz" || true)"
  [ "$code" = "200" ] && break
  sleep 1
done
if [ "${code:-}" != "200" ]; then
  echo "  [FAIL] 安装后 /healthz 未就绪：" >&2
  cat "$WORK/health.json" 2>/dev/null >&2
  tail -20 "$WORK/server.log" >&2 || true
  exit 1
fi
pass "自重启后 /healthz = 200（配置已写回 $WORK/runtime.env）"

# ── 3. 登录 ──
step "3. 后台登录"
api POST /api/admin/login '{"username":"smokeadmin","password":"SmokeTest123"}' > /dev/null
expect_code 0 "登录成功"
grep -q "token" "$COOKIES" && pass "已拿到 httpOnly Cookie（后续请求带上）" || fail "Cookie 未下发"

# ── 4. 建企业 ──
step "4. 建企业"
api POST /api/admin/companies '{"name":"冒烟测试企业","adminUsername":"smokeboss","adminPassword":"SmokeBoss123"}' > /dev/null
expect_code 0 "企业创建成功"
CID="$(jget data.id)"
info "企业 id = ${CID:-未取到}"

# ── 5. 配内容（企业信息）──
step "5. 配内容（企业信息）"
api PUT /api/admin/company-info '{"fullName":"冒烟测试企业","shortName":"冒烟","address":"上海市","tel":"021-00000000","logo":""}' > /dev/null
expect_code 0 "企业信息保存成功"

# ── 6. 建员工 ──
step "6. 建员工"
api POST /api/admin/employees '{"name":"冒烟员工","phone":"13900000001","email":"smoke@example.com","title":"测试工程师"}' > /dev/null
expect_code 0 "员工创建成功"
STAFF_ID="$(jget staffId)"
[ -n "$STAFF_ID" ] || { fail "拿不到 staffId，无法继续验证小程序侧"; }
info "员工 staffId = ${STAFF_ID:-未取到}"

# ── 7. 小程序侧公开接口读到 ──
# 这是整条链最关键的一环：后台写进去的东西，小程序（公开接口、无鉴权）读得到。
step "7. 小程序侧公开接口读回"
if [ -n "$STAFF_ID" ]; then
  api GET "/api/card?staffId=$STAFF_ID" > /dev/null
  expect_code 0 "/api/card 可读"
  NAME="$(jget employee.name)"
  if [ "$NAME" = "冒烟员工" ]; then
    pass "名片接口读到刚创建的员工（${NAME}）"
  else
    fail "/api/card 读到的员工名不对：期望「冒烟员工」，实际「${NAME:-空}」"
  fi
  FULL="$(jget company.fullName)"
  if [ "$FULL" = "冒烟测试企业" ]; then
    pass "名片接口读到刚配的企业信息（${FULL}）"
  else
    fail "/api/card 读到的企业名不对：期望「冒烟测试企业」，实际「${FULL:-空}」"
  fi
fi
api GET "/api/company" > /dev/null
expect_code 0 "/api/company 可读（小程序申请页用）"

# ── 结论 ──
echo
if [ "$FAILED" -gt 0 ]; then
  echo "结论：不通过（${FAILED} 项失败）—— 服务日志尾部：" >&2
  tail -20 "$WORK/server.log" >&2 || true
  exit 1
fi
echo "结论：通过 —— 安装向导 → 登录 → 建企业 → 配内容 → 小程序侧读回，全链路可用"
exit 0
