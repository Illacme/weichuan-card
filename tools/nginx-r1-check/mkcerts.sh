#!/usr/bin/env bash
# 生成桩桶的 TLS 证书（仅用于本地容器，非生产）。
#
# 两件事必须同时成立，否则 P4 断言没有意义：
#   1. SAN 是 DNS:bucket-a.test —— 故意**不**带 IP，这样 proxy_ssl_name 少写就会校验失败；
#   2. 自签证书自己就是 CA（basicConstraints=critical,CA:TRUE），
#      才能被 proxy_ssl_trusted_certificate 当成可信根，把「校验链」这条也测到。
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"
mkdir -p cert
if [ -f cert/bucket-test.pem ] && [ -f cert/bucket-test.key ]; then
  echo "✓ 桩桶证书已存在，跳过（删 cert/ 可重新生成）"
  exit 0
fi
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout cert/bucket-test.key -out cert/bucket-test.pem \
  -days 365 -subj "/CN=bucket-a.test" \
  -addext "subjectAltName=DNS:bucket-a.test" \
  -addext "basicConstraints=critical,CA:TRUE" 2>/dev/null
echo "✓ 桩桶证书已生成：cert/bucket-test.pem / cert/bucket-test.key（SAN=bucket-a.test）"
