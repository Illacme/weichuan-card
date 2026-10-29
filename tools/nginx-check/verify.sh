#!/bin/bash
# 对桩版 nginx 做行为验证：只看线上配置实际会产生什么响应
# 前置：先起容器（见 run.sh，它会把本目录挂到容器 /cfg，并发布 8443/8081）
set -o pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
R="--resolve weichuan.ziyuanxingqiu.cn:8443:127.0.0.1"
B="https://weichuan.ziyuanxingqiu.cn:8443"
NOPROXY=(--noproxy "*")
ACCESS_LOG="$HERE/logs/access.log"
pass=0; fail=0
ok(){ printf '  \033[32m✓\033[0m %s\n' "$1"; pass=$((pass+1)); }
no(){ printf '  \033[31m✗\033[0m %s\n     → %s\n' "$1" "$2"; fail=$((fail+1)); }
# HTTP/2 的响应头一律小写，断言必须大小写不敏感
low(){ printf '%s' "$1" | tr 'A-Z' 'a-z'; }
chk(){ if [[ "$(low "$2")" == *"$(low "$3")"* ]]; then ok "$1"; else no "$1" "$2"; fi; }
bad(){ if [[ "$(low "$2")" != *"$(low "$3")"* ]]; then ok "$1"; else no "$1" "$2"; fi; }

echo "== 1. 80 → 443 强制跳转 =="
r=$(curl -s -o /dev/null -w '%{http_code} %{redirect_url}' "${NOPROXY[@]}" -H 'Host: weichuan.ziyuanxingqiu.cn' http://127.0.0.1:8081/api/foo)
chk "http 请求被 301 到 https" "$r" "301 https://weichuan.ziyuanxingqiu.cn/api/foo"

echo "== 2. healthz 三端点必须打到后端（P0-1）=="
for p in /healthz /healthz/live /healthz/ready /healthz/; do
  body=$(curl -sk "${NOPROXY[@]}" $R "$B$p")
  chk "$p → 后端 JSON" "$body" '"stub":"backend"'
done
chk "/healthz/live 的 uri 未被改写" "$(curl -sk "${NOPROXY[@]}" $R "$B/healthz/live")" '"uri":"/healthz/live"'
bad "/healthz 不返回 SPA 壳" "$(curl -sk "${NOPROXY[@]}" $R "$B/healthz")" 'SHELL'
# 关键：^~ /healthz 不能触发 nginx 内建 301（会与 Gin 互踢成死循环）
code=$(curl -sk -o /dev/null -w '%{http_code}' "${NOPROXY[@]}" $R "$B/healthz/")
chk "/healthz/ 不产生 301 死循环" "$code" "200"

echo "== 3. /api/ 与 /uploads/ 必须带上三个安全头（P1-5）=="
for p in /api/admin/login /uploads/a.png; do
  h=$(curl -sk -D- -o /dev/null "${NOPROXY[@]}" $R "$B$p")
  chk "$p 有 X-Content-Type-Options" "$h" "X-Content-Type-Options: nosniff"
  chk "$p 有 X-Frame-Options"        "$h" "X-Frame-Options: SAMEORIGIN"
  chk "$p 有 Referrer-Policy"        "$h" "Referrer-Policy: strict-origin-when-cross-origin"
  chk "$p 打到后端" "$(curl -sk "${NOPROXY[@]}" $R "$B$p")" '"stub":"backend"'
done

echo "== 3.5 /uploads/ 缓存策略必须按「文件名会不会变」拆开 =="
h=$(curl -sk -D- -o /dev/null "${NOPROXY[@]}" $R "$B/uploads/wxacode/12.jpg")
chk "小程序码 no-cache（会原地覆盖）" "$h" "cache-control: no-cache"
chk "小程序码 有 nosniff" "$h" "x-content-type-options: nosniff"
chk "小程序码 打到后端" "$(curl -sk "${NOPROXY[@]}" $R "$B/uploads/wxacode/12.jpg")" '"stub":"backend"'
h=$(curl -sk -D- -o /dev/null "${NOPROXY[@]}" $R "$B/uploads/deadbeef.png")
chk "随机名上传可长缓存" "$h" "cache-control: public, max-age=604800"
chk "随机名上传 有 nosniff" "$h" "x-content-type-options: nosniff"
chk "随机名上传 有 X-Frame-Options" "$h" "x-frame-options: SAMEORIGIN"
# 普通前缀（非 ^~）才能让敏感文件正则先生效
for p in /uploads/.env /uploads/dump.sql; do
  code=$(curl -sk -o /dev/null -w '%{http_code}' "${NOPROXY[@]}" $R "$B$p")
  chk "$p 仍被敏感文件规则拦掉" "$code" "404"
done

echo "== 4. /api/ 反代头是否正确 =="
j=$(curl -sk "${NOPROXY[@]}" $R "$B/api/x")
chk "Host 保持原始域名" "$j" '"host":"weichuan.ziyuanxingqiu.cn"'
chk "X-Forwarded-Host 已设置" "$j" '"xfh":"weichuan.ziyuanxingqiu.cn"'
chk "X-Forwarded-Proto=https" "$j" '"proto":"https"'

echo "== 5. real_ip 生效 + 不可伪造 =="
# 5a 正常 CF 请求：CF-Connecting-IP 被采信，$remote_addr 变成访客 IP
u="/api/probe-realip-$$"
j=$(curl -sk "${NOPROXY[@]}" $R -H 'CF-Connecting-IP: 203.0.113.9' "$B$u")
# $remote_addr 只能从 access_log 第一列看到 —— 桩后端回显的是它自己的 $remote_addr
lip=$(grep -a "GET $u " "$ACCESS_LOG" | awk '{print $1}' | tail -1)
chk "access_log 里 remote_addr = 访客真实 IP" "$lip" "203.0.113.9"
chk "X-Real-IP = 访客真实 IP"   "$j" '"xri":"203.0.113.9"'
chk "XFF 被覆盖为访客 IP"       "$j" '"xff":"203.0.113.9"'
# 5b 伪造：客户端自带 XFF 必须被丢弃（否则限流可绕过）
j=$(curl -sk "${NOPROXY[@]}" $R -H 'CF-Connecting-IP: 203.0.113.9' -H 'X-Forwarded-For: 9.9.9.9, 8.8.8.8' "$B/api/x")
bad "伪造的 XFF 未透传到后端" "$j" '9.9.9.9'
bad "伪造的 XFF 未透传到后端" "$j" '8.8.8.8'
# 5c 无 CF 头（直连源站）时不能凭 XFF 冒充
j=$(curl -sk "${NOPROXY[@]}" $R -H 'X-Forwarded-For: 6.6.6.6' "$B/api/x")
bad "无 CF 头时 XFF 不被采信" "$j" '6.6.6.6'

echo "== 6. 静态资源缓存头 =="
h=$(curl -sk -D- -o /dev/null "${NOPROXY[@]}" $R "$B/assets/app-abc123.js")
chk "/assets/ 有 immutable" "$h" "Cache-Control: public, max-age=31536000, immutable"
chk "/assets/ 有 nosniff"   "$h" "X-Content-Type-Options: nosniff"
h=$(curl -sk -D- -o /dev/null "${NOPROXY[@]}" $R "$B/index.html")
chk "/index.html no-cache"  "$h" "Cache-Control: no-cache"
# 浏览器导航（Accept: text/html）才回落 SPA 壳；裸 curl 默认 Accept:*/* 现在会 404（预期）
h=$(curl -sk -D- -o /dev/null -H 'Accept: text/html' "${NOPROXY[@]}" $R "$B/")
chk "/ 落到 index.html 且 no-cache" "$h" "Cache-Control: no-cache"
chk "/ 返回 SPA 壳" "$(curl -sk -H 'Accept: text/html' "${NOPROXY[@]}" $R "$B/")" "SHELL"

echo "== 7. 404 与 SPA 兜底不互相污染 =="
code=$(curl -sk -o /dev/null -w '%{http_code}' "${NOPROXY[@]}" $R "$B/assets/nope.js")
chk "缺失的 assets 返回 404（非 HTML）" "$code" "404"
code=$(curl -sk -o /dev/null -w '%{http_code}' -H 'Accept: text/html' "${NOPROXY[@]}" $R "$B/console/companies")
chk "SPA 深链(浏览器导航)返回 200" "$code" "200"
chk "SPA 深链返回壳" "$(curl -sk -H 'Accept: text/html' "${NOPROXY[@]}" $R "$B/console/companies")" "SHELL"

echo "== 7.5 SPA 兜底修复：仅 HTML 导航回落壳，其余 404（修老问题）=="
# 修复前：/auth.json、/.env、缺失 js 全返回 index.html + 200 → 扫描器误判找到文件、缺失资源报 MIME 错。
# 修复后：只有 Accept 含 text/html 的浏览器导航才回落壳；API/fetch/扫描器/缺失资源一律 404。
# 7.5a 非 HTML 请求（扫描器 / fetch / 缺失文件）→ 404，不再兜成 index.html
for p in /auth.json /firebase-config.json /google-services.json /config/secrets.yml /sdk.js; do
  code=$(curl -sk -o /dev/null -w '%{http_code}' "${NOPROXY[@]}" $R "$B$p")
  chk "$p（非 HTML 请求） → 404" "$code" "404"
done
# 7.5b 裸 curl（默认 Accept:*/*）深链也 404（健康检查请打 /healthz）
code=$(curl -sk -o /dev/null -w '%{http_code}' "${NOPROXY[@]}" $R "$B/some/deep/route")
chk "裸 curl 深链 → 404" "$code" "404"
# 7.5c 浏览器导航（Accept: text/html）仍正常回落壳，保证前端路由深链可用
code=$(curl -sk -o /dev/null -w '%{http_code}' -H 'Accept: text/html' "${NOPROXY[@]}" $R "$B/login")
chk "/login(浏览器导航) → 200" "$code" "200"
chk "/login 返回 SPA 壳" "$(curl -sk -H 'Accept: text/html' "${NOPROXY[@]}" $R "$B/login")" "SHELL"

echo "== 8. 敏感文件拦截 =="
for p in /.env /go.mod /.git/config /backup.sql; do
  code=$(curl -sk -o /dev/null -w '%{http_code}' "${NOPROXY[@]}" $R "$B$p")
  chk "$p → 404" "$code" "404"
done

echo "== 9. TLS 与头 =="
h=$(curl -sk -D- -o /dev/null "${NOPROXY[@]}" $R "$B/")
bad "nginx 不下发 HSTS（交给 CF）" "$h" "Strict-Transport-Security"
chk "Server 头隐藏版本号" "$h" "Server: nginx"
bad "Server 头无版本号" "$h" "Server: nginx/"
if curl -sk --tlsv1.1 --tls-max 1.1 "${NOPROXY[@]}" $R "$B/" >/dev/null 2>&1; then
  no "TLSv1.1 应被拒绝" "仍然握手成功"
else
  ok "TLSv1.1 已被拒绝"
fi
if curl -sk --tlsv1.2 "${NOPROXY[@]}" $R "$B/" >/dev/null 2>&1; then ok "TLSv1.2 可用"; else no "TLSv1.2 应可用" "握手失败"; fi
if echo | openssl s_client -connect 127.0.0.1:8443 -servername weichuan.ziyuanxingqiu.cn -tls1_3 2>/dev/null | grep -q 'Protocol *: *TLSv1.3'; then ok "TLSv1.3 可用"; else no "TLSv1.3 应可用" "握手失败"; fi

echo "== 10. 全站不可索引（robots.txt + X-Robots-Tag）=="
# 10a robots.txt 必须是真文件，不能掉进 SPA 兜底变成 index.html
h=$(curl -sk -D- "${NOPROXY[@]}" $R "$B/robots.txt")
chk "/robots.txt 是 text/plain" "$h" "content-type: text/plain"
bad "/robots.txt 不是 SPA 壳" "$h" "SHELL"
body=$(curl -sk "${NOPROXY[@]}" $R "$B/robots.txt")
chk "/robots.txt 全站禁抓" "$body" "Disallow: /"
chk "/robots.txt 有 User-agent" "$body" "User-agent: *"
# 10b X-Robots-Tag 必须覆盖每一条「自己有 add_header」的路径
#     （add_header 整组继承陷阱：漏一处就等于那条路径可被索引）
for p in / /index.html /console/companies /assets/app-abc123.js \
         /uploads/deadbeef.png /uploads/wxacode/12.jpg /api/admin/login; do
  h=$(curl -sk -D- -o /dev/null -H 'Accept: text/html' "${NOPROXY[@]}" $R "$B$p")
  chk "$p 带 X-Robots-Tag noindex" "$h" "X-Robots-Tag: noindex, nofollow, noarchive"
done
# 10c 反过来确认没有把「安全头」漏在某个 location 里（同一个继承陷阱的另一面）
for p in /assets/app-abc123.js /uploads/deadbeef.png /uploads/wxacode/12.jpg /index.html; do
  h=$(curl -sk -D- -o /dev/null "${NOPROXY[@]}" $R "$B$p")
  chk "$p 仍带 nosniff" "$h" "X-Content-Type-Options: nosniff"
done

echo "== 11. 仅允许 Cloudflare 回源（geo + \$realip_remote_addr）=="
# 桩里把 Docker 网桥 172.16.0.0/12 当成了「可信 CF 来源」，所以容器内请求应判 is_cf=1。
# 线上没有这条桩规则，真实非 CF 流量会判 is_cf=0 → 403（这一步在线上用直连源站实测）。
c=$(curl -sk "${NOPROXY[@]}" $R "$B/_cfcheck")
chk "geo 把容器网段判为 CF 来源(is_cf=1)" "$c" "is_cf=1"

echo
printf '通过 %d 项，失败 %d 项\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
