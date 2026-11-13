#!/usr/bin/env bash
# R1 行为断言。由 run.sh 调用（容器已起、配置已生成）。
#
# 断言分组即可行性问题分组：
#   A 路径映射与租户隔离 / B 前缀边界与回退 / C 缓存头 / D Range·HEAD·304
#   E 对照探针（证明某条指令必需）/ F 生成器对账（含反向验证）
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"

SITE=http://127.0.0.1:8090
PROBE=http://127.0.0.1:8091
HDR=/tmp/r1.hdr
BODY=/tmp/r1.body
pass=0
fail=0

ck() { # ck 描述 期望 实际
  if [ "$2" = "$3" ]; then
    printf '  ✓ %s\n' "$1"; pass=$((pass + 1))
  else
    printf '  ✗ %s\n      期望: %s\n      实际: %s\n' "$1" "$2" "$3"; fail=$((fail + 1))
  fi
}
ckc() { # ckc 描述 期望子串 实际
  case "$3" in
    *"$2"*) printf '  ✓ %s\n' "$1"; pass=$((pass + 1)) ;;
    *) printf '  ✗ %s\n      期望包含: %s\n      实际: %s\n' "$1" "$2" "$3"; fail=$((fail + 1)) ;;
  esac
}
req() { # req URL [curl 附加参数...] → 回显状态码；头/体落 $HDR/$BODY
  local url="$1"; shift
  curl -sS --noproxy '*' -D "$HDR" -o "$BODY" -w '%{http_code}' "$@" "$url"
}
hval() { # 该头的全部取值，多个用 " | " 连接
  awk -v k="$1" 'BEGIN{IGNORECASE=1} index(tolower($0), tolower(k)":")==1 {sub(/^[^:]*:[ ]*/,""); gsub(/\r$/,""); printf "%s | ", $0}' "$HDR" | sed 's/ | $//'
}
hcount() { # 该头出现几次
  awk -v k="$1" 'BEGIN{IGNORECASE=1} index(tolower($0), tolower(k)":")==1 {n++} END{print n+0}' "$HDR"
}

echo "== A 组：路径映射与租户隔离 =="
ck "A1 /uploads/t/5/only-a.jpg → 200" 200 "$(req "$SITE/uploads/t/5/only-a.jpg")"
ck "A2 Content-Type 由桶决定（image/jpeg）" "image/jpeg" "$(hval Content-Type)"
ck "A3 真的到了租户 5 的桶（X-Amz-Request-Id 透传）" "stub-bucket-a" "$(hval X-Amz-Request-Id)"
ck "A4 桶 A 里没有的对象 → 404（不回落、不兜底成 HTML）" 404 "$(req "$SITE/uploads/t/5/only-b.jpg")"
ck "A5 /uploads/t/6/only-b.jpg → 200" 200 "$(req "$SITE/uploads/t/6/only-b.jpg")"
ck "A6 租户 6 的请求真的到了 B 桶" "stub-bucket-b" "$(hval X-Amz-Request-Id)"
ck "A7 桶 B 里没有的对象 → 404（两桶互不串）" 404 "$(req "$SITE/uploads/t/6/only-a.jpg")"

echo "== B 组：前缀边界与回退 =="
req "$SITE/uploads/t/50/only-a.jpg" >/dev/null
ck "B1 /uploads/t/50/... 未被 t/5/ 前缀吞掉（→ 平台 Go）" "go-platform" "$(hval X-Stub)"
req "$SITE/uploads/t/9/only-a.jpg" >/dev/null
ck "B2 未启用租户 t/9/ 回落平台 Go（关配置=回默认，不是 404）" "go-platform" "$(hval X-Stub)"
ck "B3 平台对象 p/library/... → 200" 200 "$(req "$SITE/uploads/p/library/icon-house.png")"
ck "B4 平台对象仍走 Go（没被前缀路由误接管）" "go-platform" "$(hval X-Stub)"
ck "B5 存量老图 /uploads/<rand>.jpg → 200" 200 "$(req "$SITE/uploads/legacy32hex.jpg")"
ck "B6 存量老图仍走 Go" "go-platform" "$(hval X-Stub)"

echo "== C 组：缓存头 =="
req "$SITE/uploads/t/5/only-a.jpg" >/dev/null
ck "C1 200 的 Cache-Control 恰好一条（不与桶的头叠加）" 1 "$(hcount Cache-Control)"
ck "C2 200 的 Cache-Control 值" "public, max-age=31536000, immutable" "$(hval Cache-Control)"
ck "C3 桶自带的 Expires 被藏掉" 0 "$(hcount Expires)"
ck "C4 安全头 4 件套在本段仍齐（add_header 继承陷阱）" 4 \
  "$(( $(hcount X-Content-Type-Options) + $(hcount X-Frame-Options) + $(hcount Referrer-Policy) + $(hcount X-Robots-Tag) ))"
ck "C5 Accept-Ranges 透传（视频拖动依赖它）" "bytes" "$(hval Accept-Ranges)"
req "$SITE/uploads/t/5/only-b.jpg" >/dev/null
ck "C6 桶 404 → Cache-Control: no-store（不进 CF 长缓存）" "no-store" "$(hval Cache-Control)"
req "$SITE/uploads/t/8/x.jpg" >/dev/null
ck "C7 桶 5xx → Cache-Control: no-store" "no-store" "$(hval Cache-Control)"

echo "== D 组：Range / HEAD / 304 =="
ck "D1 Range 请求 → 206" 206 "$(req "$SITE/uploads/t/5/video.mp4" -r 0-99)"
ck "D2 Content-Range 正确（键没被拼错，取到的是同一个对象）" "bytes 0-99/65536" "$(hval Content-Range)"
ck "D3 分片长度正确" 100 "$(hval Content-Length)"
ck "D4 HEAD → 200" 200 "$(req "$SITE/uploads/t/5/video.mp4" -I)"
ck "D5 HEAD 的 Content-Length = 对象大小" 65536 "$(hval Content-Length)"
etag="$(req "$SITE/uploads/t/5/only-a.jpg" >/dev/null; hval ETag)"
ckc "D6 桶的 ETag 被透传" '"' "$etag"
ck "D7 If-None-Match → 304" 304 "$(req "$SITE/uploads/t/5/only-a.jpg" -H "If-None-Match: $etag")"
ck "D8 304 仍带 immutable（map 的 304 分支）" \
  "public, max-age=31536000, immutable" "$(hval Cache-Control)"

echo "== E 组：对照探针（证明指令是必需项，不是可选优化）=="
ck "E1 Option A（前缀写进 proxy_pass）也能通" 200 "$(req "$PROBE/probe/opt-a/only-a.jpg")"
req "$PROBE/probe/no-hide/only-a.jpg" >/dev/null
ck "E2 不 hide 桶的 Cache-Control → 出现两条（故 proxy_hide_header 必需）" 2 "$(hcount Cache-Control)"
ck "E3 HTTPS 桶不带 SNI → 421（故 proxy_ssl_server_name 必需）" 421 "$(req "$PROBE/probe/no-sni/only-a.jpg")"
ck "E4 带 SNI + 证书校验 → 200" 200 "$(req "$PROBE/probe/sni/only-a.jpg")"

echo "== F 组：配置表 ↔ snippet 对账 =="
python3 gen_site.py --check >/tmp/r1.check 2>&1
ck "F1 一致时 --check 通过" 0 "$?"
cp conf.d/00-site.conf /tmp/r1.site.orig
python3 - <<'PY'
import pathlib, re
p = pathlib.Path("conf.d/00-site.conf")
t = p.read_text(encoding="utf-8")
t2 = re.sub(r"    # 租户 6 ·.*?\n    \}\n", "", t, flags=re.S)
assert t2 != t, "反向验证无效：没有删掉任何 location 段"
p.write_text(t2, encoding="utf-8")
PY
python3 gen_site.py --check >/tmp/r1.check2 2>&1
ck "F2 少生成一段（模拟加租户忘记 reload）→ --check 报红" 1 "$?"
ckc "F3 报红能点明是哪家租户" "6" "$(cat /tmp/r1.check2)"
cp /tmp/r1.site.orig conf.d/00-site.conf
python3 gen_site.py --check >/dev/null 2>&1
ck "F4 复原后重新一致" 0 "$?"

echo
if [ "$fail" -eq 0 ]; then
  echo "全部通过：$pass 项"
  exit 0
fi
echo "失败 $fail 项 / 通过 $pass 项"
exit 1
