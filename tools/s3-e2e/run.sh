#!/usr/bin/env bash
# 真桶端到端装置（第六十三轮）：真 S3 服务端 + 真 Go 写入 + 真 nginx 回源。
#
# 一条链跑完四步，任何一步绿了都不能代表下一步绿：
#   1. cmd/s3-e2e 经 DBLoader → Resolver → MinioUploader 真往桶里 PutObject，
#      并把对象读回来比对字节（Go 侧）；
#   2. cmd/nginx-snippets 用**同一份租户表**生成 snippet（不手写 location）；
#   3. nginx 容器按 snippet 反代到真 S3 服务端；
#   4. 用 curl 走 nginx 取同一个对象，与签名 URL 取回的字节逐字节比对。
#
# 依赖：docker（nginx:alpine）、go、curl；一个能听 19000 端口的 S3 服务端。
# 服务端怎么起见 README.md（本期用 versitygw，POSIX 后端）。
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
mkdir -p logs snippets

S3_ENDPOINT="${S3_E2E_ENDPOINT:-127.0.0.1:19000}"      # Go 侧（宿主）
S3_AK="${S3_E2E_ACCESS_KEY:-testuser}"
S3_SK="${S3_E2E_SECRET_KEY:-testsecret}"
S3_BUCKET="${S3_E2E_BUCKET:-wc-t7}"
CID="${S3_E2E_COMPANY:-7}"
SITE_PORT="${S3_E2E_SITE_PORT:-18080}"
SITE="http://127.0.0.1:${SITE_PORT}"
rc=0

ck() { # ck 描述 期望 实际
  if [ "$2" = "$3" ]; then printf '  ✓ %s\n' "$1"; else
    printf '  ✗ %s\n      期望: %s\n      实际: %s\n' "$1" "$2" "$3"; rc=1; fi
}

echo "== 0. 前置：S3 服务端在不在 =="
# 不带凭据必然是 403（S3 语义）。拿到 200 反而说明这是个不校验签名的假端点。
code="$(curl -s -o /dev/null -w '%{http_code}' -m 5 "http://$S3_ENDPOINT/" || true)"
if [ "$code" = "403" ]; then
  echo "  ✓ $S3_ENDPOINT 是一个会校验签名的 S3 端点（匿名 GET → 403）"
else
  echo "  ✗ $S3_ENDPOINT 不是预期的 S3 端点（匿名 GET → '$code'，期望 403）"
  echo "    先按 README 起服务端：ROOT_ACCESS_KEY=... versitygw --port ${S3_ENDPOINT} posix ..."
  exit 1
fi

echo
echo "== 1. Go 侧：真桶写入 / 读回比对 / 预签名 / 负例 =="
if ! ( cd "$REPO_ROOT/backend" && go run ./cmd/s3-e2e \
        -endpoint "$S3_ENDPOINT" -access-key "$S3_AK" -secret-key "$S3_SK" \
        -bucket "$S3_BUCKET" -company "$CID" \
        -base-url "$SITE/uploads" \
        -platform-dir "$HERE/logs/platform" \
        -emit "$HERE/logs/e2e.env" ); then
  echo "  ✗ Go 侧未通过，后续 nginx 断言不再继续（否则会在错的基线上取证）"
  exit 1
fi
# shellcheck disable=SC1091
. "$HERE/logs/e2e.env"

echo
echo "== 2. 用同一份租户表生成 snippet =="
if ! ( cd "$REPO_ROOT/backend" && go run ./cmd/nginx-snippets render \
        --targets "$HERE/tenants.json" --out "$HERE/snippets/00-tenants.conf" ); then
  echo "  ✗ 生成失败"; exit 1
fi
grep -c 'location ^~ /uploads/t/' "$HERE/snippets/00-tenants.conf" >/dev/null 2>&1 || true
echo "  ✓ 生成物："
sed 's/^/      /' "$HERE/snippets/00-tenants.conf"

echo
echo "== 3. 起 nginx 容器（只带生成物）=="
docker rm -f s3-e2e-nginx >/dev/null 2>&1 || true
if ! docker run --rm -d --name s3-e2e-nginx \
      -v "$HERE:/cfg" \
      -v "$HERE/nginx.conf:/etc/nginx/nginx.conf:ro" \
      -p "${SITE_PORT}:${SITE_PORT}" \
      nginx:alpine >/dev/null; then
  echo "  ✗ 容器起不来"; exit 1
fi
for _ in $(seq 1 20); do
  curl -s --noproxy '*' -o /dev/null -m 1 "$SITE/" && break
  sleep 0.5
done
if docker exec s3-e2e-nginx nginx -t 2>&1 | sed 's/^/      /'; then
  echo "  ✓ nginx -t 通过"
else
  echo "  ✗ nginx -t 失败"; docker logs s3-e2e-nginx 2>&1 | tail -20; docker rm -f s3-e2e-nginx >/dev/null 2>&1; exit 1
fi

echo
echo "== 4. 真回源断言 =="
HDR="$HERE/logs/resp.hdr"
BODY="$HERE/logs/resp.bin"
FBHDR="$HERE/logs/fallback.hdr"
# 取某个头的取值 / 出现次数。**不用 IGNORECASE**：macOS 自带的 awk（BWK）不认它，
# 而它不会报错，只会让断言永远拿到空串——那种「静默失效的断言」比没有断言更坏。
hval() { awk -v k="$1" 'index(tolower($0), tolower(k)":")==1 {sub(/^[^:]*:[ ]*/,""); gsub(/\r$/,""); printf "%s", $0}' "$2"; }
hcount() { awk -v k="$1" 'index(tolower($0), tolower(k)":")==1 {n++} END{print n+0}' "$2"; }

code="$(curl -sS --noproxy '*' -D "$HDR" -o "$BODY" -w '%{http_code}' "$SITE/uploads/$KEY" || true)"
ck "H1 GET /uploads/$KEY → 200" "200" "$code"
if [ "$code" = "200" ]; then
  # 基准是「从桶里按签名取回的字节」，不是本地文件——本地文件在这条链里没有输出过。
  curl -sS --noproxy '*' -o "$HERE/logs/bucket.bin" "$GETURL" || true
  ck "H2 走 nginx 取回的字节与桶内对象一致" "same" \
     "$(cmp -s "$BODY" "$HERE/logs/bucket.bin" && echo same || echo differ)"
  ck "H3 字节数" "$(wc -c < "$HERE/logs/bucket.bin" | tr -d ' ')" "$(wc -c < "$BODY" | tr -d ' ')"
  ck "H4 Content-Type 由桶决定（image/png）" "image/png" "$(hval Content-Type "$HDR")"
  ck "H5 安全头仍在（snippet 的 add_header 生效、未被桶覆盖）" "1" "$(hcount X-Content-Type-Options "$HDR")"
  ck "H6 Cache-Control 只有一条（桶的不与站点叠加）" "1" "$(hcount Cache-Control "$HDR")"
fi

# 预签名直传写进去的那个对象，走同一条 nginx 路径也应该取得到：
# 它证明「直传落桶」与「回源取图」用的是同一套键前缀，不是各拼各的。
code2="$(curl -sS --noproxy '*' -o /dev/null -w '%{http_code}' "$SITE/uploads/$PUTKEY" || true)"
ck "H7 预签名直传的对象也能经 nginx 取到" "200" "$code2"

# 未配置的租户前缀必须落回平台段，而不是被 t/7/ 吞掉或打到桶里。
code3="$(curl -sS --noproxy '*' -D "$FBHDR" -o /dev/null -w '%{http_code}' "$SITE/uploads/t/99/probe.png" || true)"
ck "H8 未配置的 t/99/ → 平台回退段（404 + 标记）" "404" "$code3"
ck "H9 确实没进桶" "platform-fallback" "$(hval X-Stub "$FBHDR")"

echo
echo "== 5. 关闭容器 =="
docker rm -f s3-e2e-nginx >/dev/null 2>&1 || true

if [ "$rc" -eq 0 ]; then echo "✅ 真桶端到端：全绿"; else echo "❌ 真桶端到端：有失败项"; fi
exit "$rc"
