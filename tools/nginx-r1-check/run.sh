#!/usr/bin/env bash
# R1 一键验证：生成 snippet（Go 生成器）+ 生成桩对象 → 起容器 → 三组检查 → 关容器。
# 前置：docker（nginx:alpine）、go、openssl、python3、curl。
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
mkdir -p logs snippets cert www

rc=0

echo "== 生成桩对象 =="
python3 make_fixtures.py || exit 1

echo "== 生成租户 snippet（backend/cmd/nginx-snippets）=="
if ! ( cd "$REPO_ROOT/backend" && go run ./cmd/nginx-snippets render \
        --targets "$HERE/tenants.json" \
        --out "$HERE/snippets/00-tenants.conf" \
        --dns-precheck ); then
  echo "  ✗ 生成失败"; exit 1
fi

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
echo "== G 组：桶域名解析失败的两道防线 =="
# G1：如果让 nginx 去发现，会发生什么（预期**失败** —— 这正是要避免的场景）
out="$(docker run --rm -v "$HERE:/cfg:ro" nginx:alpine nginx -t -c /cfg/nginx-dns-probe.conf 2>&1)"
dns_rc=$?
if [ "$dns_rc" -ne 0 ] && printf '%s' "$out" | grep -q "host not found in upstream"; then
  echo "  ✓ G1 nginx 自己去解析 → -t emerg（退出码 ${dns_rc}）⇒ 必须前置检查，不能让它去发现"
else
  echo "  ✗ G1 预期失败但没有失败（退出码 ${dns_rc}）：$out"
  rc=1
fi

# G2：生成器自己挡住（预期**拒绝落盘**并点明是哪家租户，不留半成品文件）
bad_targets=/tmp/r1-bad-tenants.json
bad_out=/tmp/r1-bad-snippet.conf
cat > "$bad_targets" <<'JSON'
{"tenants":[
  {"companyId":5,"driver":"minio","endpoint":"127.0.0.1:9001","bucket":"wc-t5","enabled":true},
  {"companyId":99,"driver":"oss","endpoint":"bucket-does-not-exist.invalid","bucket":"wc-t99","useSsl":true,"enabled":true}
]}
JSON
rm -f "$bad_out"
gout="$( cd "$REPO_ROOT/backend" && go run ./cmd/nginx-snippets render \
          --targets "$bad_targets" --out "$bad_out" --dns-precheck 2>&1 )"
g2_rc=$?
if [ "$g2_rc" -ne 0 ] && printf '%s' "$gout" | grep -q "company_id=99" && [ ! -f "$bad_out" ]; then
  echo "  ✓ G2 生成器拒绝落盘并点明 company_id=99（没有产出半成品文件）"
else
  echo "  ✗ G2 生成器没有挡住（退出码 ${g2_rc}，产物存在=$([ -f "$bad_out" ] && echo yes || echo no)）：$gout"
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
