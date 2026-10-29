#!/usr/bin/env bash
# 生成本地校验用的自签 TLS 证书（仅用于 nginx:alpine 容器内 443 监听，非生产）。
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"
mkdir -p cert
if [ -f cert/fullchain.pem ] && [ -f cert/privkey.pem ]; then
  echo "✓ 证书已存在，跳过（删 cert/ 可重新生成）"
  exit 0
fi
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout cert/privkey.pem -out cert/fullchain.pem \
  -days 365 -subj "/CN=weichuan.ziyuanxingqiu.cn" 2>/dev/null
echo "✓ 自签证书已生成：cert/fullchain.pem / cert/privkey.pem"
