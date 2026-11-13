#!/usr/bin/env python3
"""从 tenants.json 生成 R1 的**产品形态** nginx snippet（conf.d/site.conf）。

设计要点（三条都是被 probes.conf 的对照探针逼出来的，不是审美选择）：

1. **只剥 `/uploads/` 前缀，不把 `t/<cid>/` 写进 proxy_pass 的 URI 部分。**
   对象键布局的唯一真相在 Go 侧 `storage.ObjectKeyFor`；如果在 nginx 里再写一遍
   `proxy_pass https://bucket/t/5/`，将来键布局一改就要同时改 Go 和每一条 snippet，
   漏改一条 = 那一家租户全站裂图且没有任何报错。所以这里用
   `rewrite ^/uploads/(.*)$ /$1 break;` —— nginx 只知道「剥前缀」，键长什么样它不管。

2. **`proxy_hide_header Cache-Control; proxy_hide_header Expires;` 必须有。**
   桶会把对象元数据里的 Cache-Control 一起回传；不清掉的话，站点 `add_header`
   会**再叠一条**，同一个头两个值。RFC 9111 不规定 CDN 取哪个 → 行为不确定，
   实测见 verify.sh 的 D 组对照（不 hide 时确实出现两条）。

3. **每段 location 都要重声明 server 级的那组安全头。**
   nginx 的继承陷阱：location 里只要出现任何一个 `add_header`，server 级整组失效。
   线上 nginx-site.live.conf:160-173 已写明这条，这里复刻同样的写法。

用法：
    python3 gen_site.py              # 生成 conf.d/site.conf
    python3 gen_site.py --check      # 只对账：配置表 enabled 租户 ↔ 生成的 location
"""
import json
import re
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
TENANTS = HERE / "tenants.json"
# 文件名带 00- 前缀不是洁癖：probes.conf 要用 site.conf 里定义的 $uploads_cache，
# 而 `include conf.d/*.conf` 是按字典序展开的 —— 名字排在后面就等于「变量未定义」，
# nginx -t 直接失败。这条踩过（第一版叫 site.conf，probes.conf 排在它前面）。
DST = HERE / "conf.d/00-site.conf"

# 与线上 deploy/baota/nginx-site.live.conf:68-79 完全同构的 map（逐字对齐，
# 因为「404 被 CF 锁一年」那个坑就是靠它修的，改了要同步线上那份）。
UPLOADS_CACHE_MAP = """\
map $status $uploads_cache {
    200      "public, max-age=31536000, immutable";
    206      "public, max-age=31536000, immutable";
    304      "public, max-age=31536000, immutable";
    404      "no-store";
    410      "no-store";
    500      "no-store";
    502      "no-store";
    503      "no-store";
    504      "no-store";
    default  "no-cache";
}"""

SECURITY_HEADERS = """\
        add_header Cache-Control $uploads_cache always;
        # add_header 继承陷阱：本 location 有 add_header，server 级安全头整组失效，故重声明
        add_header X-Content-Type-Options nosniff always;
        add_header X-Frame-Options SAMEORIGIN always;
        add_header Referrer-Policy strict-origin-when-cross-origin always;
        add_header X-Robots-Tag "noindex, nofollow, noarchive" always;"""


def load():
    # tenants.json 里带 `#` 行注释（刻意为之：那份文件是给人读的运维口径说明，
    # 不是机器配置）。JSON 不支持注释，这里剥掉再解析。
    raw = TENANTS.read_text(encoding="utf-8")
    body = "\n".join(l for l in raw.splitlines() if not l.lstrip().startswith("#"))
    data = json.loads(body)
    return data["tenants"]


def scheme(t):
    return "https" if t.get("useSsl") else "http"


def tenant_block(t):
    cid = t["companyId"]
    up = f"{scheme(t)}://{t['endpoint']}"
    lines = [
        f"    # 租户 {cid} · driver={t['driver']} · bucket={t['bucket']} · key 前缀 t/{cid}/",
        f"    location ^~ /uploads/t/{cid}/ {{",
        f"        proxy_pass {up};",
        "        # 只剥 /uploads/：剩下的就是桶里的对象键，键布局只在 Go 侧定义",
        "        rewrite ^/uploads/(.*)$ /$1 break;",
        f'        proxy_set_header Host "{t["hostTemplate"]}";',
        "        proxy_http_version 1.1;",
        "        proxy_read_timeout 60s;",
        "        # 桶自带的 Cache-Control/Expires 必须先藏掉，否则 add_header 会叠成两条",
        "        proxy_hide_header Cache-Control;",
        "        proxy_hide_header Expires;",
    ]
    if t.get("useSsl"):
        lines += [
            "        # 真桶校验 SNI/证书，这三行缺一个就是 421 或 502",
            "        proxy_ssl_server_name on;",
            f'        proxy_ssl_name "{t["hostTemplate"]}";',
        ]
    lines.append(SECURITY_HEADERS)
    lines.append("    }")
    return "\n".join(lines)


def fallback_block():
    # 逐字复刻线上 nginx-site.live.conf:447-459 的 /uploads/ 段（只把 upstream 指向桩 Go）
    return "\n".join([
        "    # 回退：没有租户配置的 /uploads/... 一律走平台 Go（存量老图、素材库、小程序码、",
        "    # 以及未接入租户的新上传）。这一段就是线上现状，必须保持原样——它保证",
        "    # 「关掉某个租户的存储配置」等于「立刻回到平台默认」，而不是 404。",
        "    location ^~ /uploads/ {",
        "        proxy_pass http://127.0.0.1:8080;",
        "        proxy_set_header Host $host;",
        "        proxy_set_header X-Real-IP $remote_addr;",
        "        proxy_set_header X-Forwarded-For $remote_addr;",
        "        proxy_set_header X-Forwarded-Proto $scheme;",
        "        proxy_read_timeout 60s;",
        SECURITY_HEADERS,
        "    }",
    ])


def render(tenants):
    enabled = [t for t in tenants if t.get("enabled")]
    parts = [
        "# 由 gen_site.py 从 tenants.json 生成，勿手改。",
        "# 产品形态：每租户一段 ^~ /uploads/t/<cid>/ + 一段回退 ^~ /uploads/。",
        "",
        UPLOADS_CACHE_MAP,
        "",
        "server {",
        "    listen 8090;",
        "    server_name r1.test;",
        "",
        "    # 安全响应头（server 级）：覆盖回退段等没有自己 add_header 的路径",
        "    add_header X-Content-Type-Options nosniff always;",
        "    add_header X-Frame-Options SAMEORIGIN always;",
        "    add_header Referrer-Policy strict-origin-when-cross-origin always;",
        '    add_header X-Robots-Tag "noindex, nofollow, noarchive" always;',
        "",
    ]
    for t in sorted(enabled, key=lambda x: x["companyId"]):
        parts.append(tenant_block(t))
        parts.append("")
    parts.append(fallback_block())
    parts.append("}")
    return "\n".join(parts) + "\n"


def emitted_cids(text):
    return {int(m) for m in re.findall(r"location \^~ /uploads/t/(\d+)/", text)}


def main():
    tenants = load()
    text = render(tenants)
    DST.parent.mkdir(parents=True, exist_ok=True)

    if "--check" in sys.argv:
        # 对账：配置表 enabled 集合 ↔ 生成物里的 location 集合
        if not DST.is_file():
            sys.exit(f"❌ {DST} 不存在，先跑 python3 gen_site.py")
        want = {t["companyId"] for t in tenants if t.get("enabled")}
        got = emitted_cids(DST.read_text(encoding="utf-8"))
        missing, extra = want - got, got - want
        if missing or extra:
            print("❌ 配置表与 snippet 不一致：")
            if missing:
                print(f"   配了存储但没生成 snippet（这些租户的图会 404）：{sorted(missing)}")
            if extra:
                print(f"   生成了 snippet 但配置表没启用（白开了个口子）：{sorted(extra)}")
            return 1
        print(f"✓ 配置表↔snippet 一致（enabled 租户：{sorted(got)}）")
        return 0

    DST.write_text(text, encoding="utf-8")
    enabled = sorted(t["companyId"] for t in tenants if t.get("enabled"))
    disabled = sorted(t["companyId"] for t in tenants if not t.get("enabled"))
    print(f"写入 {DST}")
    print(f"  已接管租户（生成 location）：{enabled}")
    print(f"  未接管租户（回落平台 Go）：{disabled}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
