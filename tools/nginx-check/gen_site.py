#!/usr/bin/env python3
"""把线上配置改写成可在本地容器里做 nginx -t 与行为验证的桩版本。

只做「路径/依赖替换」，不改任何 location 结构与指令语义——
否则验证通过也说明不了线上那份配置是对的。

用法：
    python3 gen_site.py [线上配置路径]
    不传参数时默认指向仓库 deploy/baota/nginx-site.live.conf。

本脚本与配套 nginx.conf 都用相对路径，clone 到任意位置都能跑；
生成物（conf.d/site.conf、panel-proxy/、cert/、logs/）由 .gitignore 忽略。
"""
import re
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent.parent  # tools/nginx-check -> 仓库根

SRC = Path(sys.argv[1]) if len(sys.argv) > 1 else REPO_ROOT / "deploy/baota/nginx-site.live.conf"
if not SRC.is_file():
    sys.exit(f"❌ 找不到线上配置：{SRC}（可用参数指定：python3 gen_site.py /path/to/nginx-site.live.conf）")

DST = HERE / "conf.d/site.conf"

text = SRC.read_text(encoding="utf-8")

# 1. 面板专属 include 全部指向容器里不存在的路径，桩里注释掉
text = text.replace(
    "    include /www/server/panel/vhost/nginx/well-known/weichuan.ziyuanxingqiu.cn.conf;",
    "    # [stub] well-known include",
)
text = text.replace(
    "    include /www/server/panel/vhost/nginx/extension/weichuan.ziyuanxingqiu.cn/*.conf;",
    "    # [stub] extension include",
)
# 1. 原面板伪静态的 include 已从线上配置移除（SPA 兜底改为自管理，见 live.conf 的
#    `location / { try_files $uri $uri/ @spa; }` + `location @spa`）。所以这里不再注入桩 SPA 兜底，
#    而是**直接复用线上真实的 @spa 逻辑**做验证（改了兜底行为后这是该测的东西）。
#    只需补一个 /_cfcheck 桩（验证 geo 是否把容器网段判成 CF 来源 is_cf=1），线上不存在此路径。
text = text.replace(
    "    location @spa {",
    "    # [stub] 验证 geo 是否把容器网段判成 CF 来源（is_cf=1）；线上不存在此路径\n"
    "    location = /_cfcheck { default_type text/plain; return 200 \"is_cf=$is_cf\"; }\n\n"
    "    location @spa {",
    1,
)

# 1.5 面板「反向代理」的 include：**不能简单删掉**，否则 duplicate location 这类
#     线上真实故障在容器里永远测不出来（目录不存在 → glob 空匹配 → nginx 视为合法）。
#     这里把它指向一个真实存在的桩目录，里面放一份宝塔风格的 proxy 文件。
#     于是「include 忘了注释」会立刻以 duplicate location 暴露出来。
PANEL_DIR = HERE / "panel-proxy"
if not PANEL_DIR.is_dir():
    PANEL_DIR.mkdir()
(PANEL_DIR / "weichuan.ziyuanxingqiu.cn_8080.conf").write_text(
    """# [stub] 模拟宝塔面板「反向代理」生成的文件（原文形态）
location = /healthz {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
}
location /api/ {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    add_header X-Cache $upstream_cache_status;
    add_header Cache-Control no-cache;
}
location ^~ /uploads/
{
    proxy_pass http://127.0.0.1:8080/uploads/;
    proxy_set_header Host $host;
    add_header X-Cache $upstream_cache_status;
}
""",
    encoding="utf-8",
)
_panel_inc = "    include /www/server/panel/vhost/nginx/proxy/weichuan.ziyuanxingqiu.cn/*.conf;"
if _panel_inc in text:
    text = text.replace(_panel_inc, "    include /cfg/panel-proxy/*.conf;")
    print("⚠️  检测到未被注释的面板反代 include —— 已指向桩目录，预期 nginx -t 报 duplicate location")
else:
    print("✓ 面板反代 include 已注释（不会再与手写的 /api/、/uploads/ 冲突）")

# 2. proxy_cache_purge 是第三方模块，官方镜像没有；整段换掉但保留位置说明
text = re.sub(
    r"    location ~ /purge\(/\.\*\) \{.*?\n    \}\n",
    "    # [stub] location ~ /purge 需要 ngx_cache_purge 模块，官方镜像未编译\n",
    text,
    flags=re.S,
)

# 3. 证书（容器里用自签证书，见 mkcerts.sh）
text = text.replace(
    "/www/server/panel/vhost/cert/weichuan.ziyuanxingqiu.cn/fullchain.pem",
    "/cfg/cert/fullchain.pem",
).replace(
    "/www/server/panel/vhost/cert/weichuan.ziyuanxingqiu.cn/privkey.pem",
    "/cfg/cert/privkey.pem",
)

# 4. 站点根目录
text = text.replace(
    "root /www/wwwroot/weichuan.ziyuanxingqiu.cn/frontend;",
    "root /cfg/www/frontend;",
)

# 5. 日志
text = text.replace(
    "access_log  /www/wwwlogs/weichuan.ziyuanxingqiu.cn.log;",
    "access_log  /cfg/logs/access.log;",
).replace(
    "error_log  /www/wwwlogs/weichuan.ziyuanxingqiu.cn.error.log;",
    "error_log  /cfg/logs/error.log;",
)

# 6. 只给「验证 real_ip 是否真的生效」加一条：容器里请求来自 127.0.0.1，
#    不加这一行 CF 网段永远匹配不上，real_ip 段等于没验证到。
#    这一行仅存在于桩文件，不写回线上配置。
text = text.replace(
    "    set_real_ip_from 173.245.48.0/20;",
    "    set_real_ip_from 127.0.0.1;  # [stub] 仅测试用\n"
    "    set_real_ip_from 172.16.0.0/12;  # [stub] Docker 网桥出口，仅测试用\n"
    "    set_real_ip_from 173.245.48.0/20;",
)

# 6.5 geo 块也要把 Docker 网桥当成「可信来源」：geo 用的是 $realip_remote_addr
#     （TCP 对端，不受 real_ip 改写影响），容器请求来自 172.16.0.0/12，
#     不加这一行就会被判成 $is_cf=0 → 全部 403，66 项断言全挂。仅桩文件生效。
text = text.replace(
    "    default          0;",
    "    default          0;\n    172.16.0.0/12   1;  # [stub] Docker 网桥出口，仅测试用",
)

# 7. 80 端口在容器里会和 443 抢（都监听 0.0.0.0），保持原样即可，nginx 允许同 server 双 listen

# 8. 把真实的 public/robots.txt 拷进桩根目录，用来验证它确实被当成静态文件返回
#    （而不是掉进 SPA 兜底变成 index.html）。拷真文件而不是手写一份，
#    这样「robots.txt 内容改错了」也能被发现。
_robots_src = REPO_ROOT / "frontend/public/robots.txt"
_robots_dst = HERE / "www/frontend/robots.txt"
if _robots_src.is_file():
    _robots_dst.write_bytes(_robots_src.read_bytes())
    print(f"✓ robots.txt 已同步到桩根目录（{_robots_src.stat().st_size} 字节）")
else:
    print("⚠️  找不到 frontend/public/robots.txt —— robots.txt 断言会失败")

DST.write_text(text, encoding="utf-8")
print(f"写入 {DST}")

# 输出改动清单，便于核对「只改了路径没改语义」
for i, line in enumerate(text.splitlines(), 1):
    if "[stub]" in line:
        print(f"  {i:4d}| {line.strip()}")
