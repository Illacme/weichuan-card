#!/usr/bin/env bash
# R1 一键验证：生成桩对象 + 生成 snippet + 起容器 + 三组检查 + 关容器。
# 前置：docker（nginx:alpine）、openssl、python3、curl。
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"
mkdir -p logs conf.d cert www

rc=0

echo "== 生成桩对象 =="
python3 make_fixtures.py || exit 1

echo "== 生成产品形态 snippet =="
python3 gen_site.py || exit 1

echo "== 准备桩桶证书 =="
bash mkcerts.sh

echo "== 起 nginx 容器 =="
docker rm -f nginx-r1 >/dev/null 2>&1 || true
docker run --rm -d --name nginx-r1 \
  -v "$HERE:/cfg" \
  -v "$HERE/nginx.conf:/etc/nginx/nginx.conf:ro" \
  -p 8090:8090 -p 8091:8091 \
  nginx:alpine >/dev/null || exit 1

for _ in $(seq 1 15); do
  if curl -s --noproxy '*' -o /dev/null "http://127.0.0.1:8090/uploads/legacy32hex.jpg"; then
    break
  fi
  sleep 1
done

echo "== 主配置 nginx -t =="
if docker exec nginx-r1 nginx -t 2>&1 | sed 's/^/  /'; then
  echo "  ✓ nginx -t 通过"; pass_t=1
else
  echo "  ✗ nginx -t 失败"; pass_t=0; rc=1
fi
if [ "$pass_t" = 0 ]; then
  echo "== 容器日志 =="; docker logs nginx-r1 2>&1 | tail -30
fi

echo
echo "== G 组：桶域名解析不出来时 nginx -t 会失败吗（预期**失败**）=="
out="$(docker run --rm -v "$HERE:/cfg:ro" nginx:alpine nginx -t -c /cfg/nginx-dns-probe.conf 2>&1)"
dns_rc=$?
if [ "$dns_rc" -ne 0 ] && printf '%s' "$out" | grep -q "host not found in upstream"; then
  echo "  ✓ G1 解析失败 → nginx -t emerg（退出码 ${dns_rc}），报错：$(printf '%s' "$out" | grep -o 'host not found in upstream[^"]*' | head -1)"
  echo "  → 结论：snippet 生成器必须先做 DNS 预检，不能靠 nginx 去发现（见可行性文档 R1-c）"
else
  echo "  ✗ G1 预期失败但没有失败（退出码 ${dns_rc}）：$out"
  rc=1
fi

echo
echo "== 行为断言 =="
bash verify.sh
[ $? -ne 0 ] && rc=1

echo
echo "== 关闭容器 =="
docker rm -f nginx-r1 >/dev/null 2>&1 || true

if [ "$rc" -eq 0 ]; then echo "✅ R1 验证装置：全绿"; else echo "❌ R1 验证装置：有失败项"; fi
exit "$rc"
