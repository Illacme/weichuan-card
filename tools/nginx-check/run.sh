#!/usr/bin/env bash
# 一键行为验证：生成桩配置 → 起 nginx 容器 → 跑断言 → 关容器。
# 前置：docker、openssl、python3、curl 可用。
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"

echo "== 生成桩配置 =="
python3 gen_site.py || exit 1

echo "== 准备自签证书 =="
bash mkcerts.sh

echo "== 起 nginx 校验容器 =="
docker rm -f nginx-check >/dev/null 2>&1 || true
docker run --rm -d --name nginx-check \
  -v "$HERE:/cfg" \
  -v "$HERE/nginx.conf:/etc/nginx/nginx.conf:ro" \
  -p 8443:443 -p 8081:80 \
  nginx:alpine

# 等 nginx 起来（最多 10s）
for i in $(seq 1 10); do
  if curl -sk -o /dev/null "https://weichuan.ziyuanxingqiu.cn:8443/robots.txt" \
       --resolve weichuan.ziyuanxingqiu.cn:8443:127.0.0.1 --noproxy '*' 2>/dev/null; then
    break
  fi
  sleep 1
done

echo "== 跑行为断言 =="
bash verify.sh
rc=$?

echo "== 关闭容器 =="
docker rm -f nginx-check >/dev/null 2>&1 || true
exit $rc
