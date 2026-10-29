# nginx-check · 线上 nginx 配置行为验证装置

把生产 nginx 配置（`deploy/baota/nginx-site.live.conf`）改写成可在本地
`nginx:alpine` 容器里跑的桩版本，再用一组 HTTP 断言验证「配置实际会产生什么响应」。
专治「`nginx -t` 通过，但上线后行为不对」——比如安全头漏配、`add_header` 整组继承陷阱、
SPA 兜底误返 200、real_ip 改写、`/uploads/` 缓存策略、TLS 版本等。

## 前置

- `docker`、`openssl`、`python3`、`curl`（macOS / Linux 均可）
- 不需要 Go、不需要真实后端——桩后端是 nginx.conf 里内联的一个回显 server

## 用法

```bash
cd tools/nginx-check
bash run.sh          # 一步到位：生成桩配置 → 起容器 → 跑断言 → 关容器
```

只想重跑某一环也行：

```bash
python3 gen_site.py [可选的线上配置路径]   # 生成 conf.d/site.conf（默认读仓库 deploy/baota/nginx-site.live.conf）
bash mkcerts.sh                           # 生成自签证书到 cert/（已存在则跳过）
docker run --rm -d --name nginx-check \
  -v "$PWD:/cfg" -v "$PWD/nginx.conf:/etc/nginx/nginx.conf:ro" \
  -p 8443:443 -p 8081:80 nginx:alpine     # 手动起容器
bash verify.sh                             # 跑断言（容器需已起在 8443/8081）
docker rm -f nginx-check                   # 收摊
```

## 它到底验什么（75 项断言，节选）

- **80 → 443 强制跳转**（不靠 CF，源站自己也得跳）
- **healthz 三端点打到后端**，且 `/healthz/` 不产生 301 死循环
- **`/api/`、`/uploads/` 带齐安全头**（nosniff / X-Frame / Referrer-Policy）
- **`/uploads/` 缓存按文件名拆分**：`wxacode/` 不缓存（原地覆盖），随机名上传 7 天
- **real_ip 生效且不可伪造**：CF-Connecting-IP 被采信、客户端自带 XFF 被丢弃
- **静态资源头**：`/assets/` immutable、`/index.html` no-cache
- **SPA 兜底修复**：仅浏览器导航（`Accept: text/html`）回落壳，其余（扫描器/fetch/缺失资源）一律 404
- **敏感文件拦截**：`/.env`、`.git/config` 等 404
- **TLS**：禁 TLSv1.1、保留 1.2/1.3、不下发 HSTS（交给 CF）
- **全站不可索引**：`robots.txt` 是真文件、`X-Robots-Tag: noindex` 覆盖每条自定义 location
- **仅允许 Cloudflare 回源**（geo）：桩里把 Docker 网桥当可信来源，验证 `is_cf=1`

## 原理与边界

- `gen_site.py` **只做路径/依赖替换，不改任何 location 语义**——否则验证通过也说明不了线上对。
  它把宝塔专属的 `include` 注释掉、面板反代 `include` 指向桩目录（故意保留以复现
  `duplicate location` 这类线上真实故障）、证书/根目录/日志换成容器内路径、并补 `/_cfcheck` 桩。
- 桩容器把本目录挂到 `/cfg`，`nginx.conf` 内联桩后端（回显收到的头，用于验证 real_ip）。
- 验证的是**行为**（响应头、状态码、回落逻辑），不是性能或压测。
- 真实「仅允许 CF 回源」那一步（非 CF 来源 → 403）在线上用「直连源站 IP」实测，桩里只验 geo 映射逻辑。
