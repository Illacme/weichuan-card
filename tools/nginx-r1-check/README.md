# R1 验证装置 · `/uploads/` 按租户前缀反代到对象桶

**这不是线上配置，是验证装置。** 目的只有一个：回答设计稿 §3.4 / 风险 R1 那句
「nginx 按租户反代尚未验证」——把它从假设变成有实测依据的结论。

线上配置的回归验证在 `tools/nginx-check/`（那套跑整站断言）。本目录只测
**R1 新增的那部分语义**，两者互不替代。

---

## 为什么需要单独一套

设计稿把 R1 写成「`/uploads/` 现在是本地静态托管，改成反代要同步改这里」。
**实读线上配置后这句是过时的**：`deploy/baota/nginx-site.live.conf:447-459` 里
`location ^~ /uploads/` 已经是 `proxy_pass http://127.0.0.1:8080`（Go 侧
`r.Static("/uploads", cfg.UploadDir)` 出盘）。也就是说 R1 不是「静态改反代」，
而是**在一条已经存在的反代上面，再加一层按租户前缀分流**——风险面比设计稿假设的小，
但多了四个新问题（见 `docs/tenant-storage-r1-feasibility.md` §4）：

1. `proxy_pass` 的 URI 部分怎么拼，才不会把对象键拼错（`/uploads/t/5/x.jpg` → 桶里 `t/5/x.jpg`）；
2. 桶自带的 `Cache-Control` 与 `add_header` 叠加成**两条**头，CDN 取哪个不确定；
3. 桶域名在 **reload 时**解析，解不出来 `nginx -t` 直接失败 → 一次填错域名会挡住建站；
4. 真桶校验 Host/SNI，不带 SNI 会被拒（421 / `Misdirected Request`）。

## 目录

| 文件 | 作用 |
| --- | --- |
| `nginx.conf` | 容器主配置：4 个桩上游（平台 Go + 两个桶 + 一个恒 500 桶）+ TLS 桩桶，最后 include `site.conf` 与 `probes.conf` |
| `site.conf` | **手写入库的站点壳**：`$uploads_cache` map + `server{}` + `/uploads/` 回退段 + `include snippets/*.conf` |
| `snippets/` | **生成物（gitignored）**：由 `backend/cmd/nginx-snippets` 产出的租户 location 段 |
| `tenants.json` | 模拟 `tenant_storage_configs` 的 enabled 行（含一条停用，用于验回退） |
| `probes.conf` | **手写的对照探针，非产品形态**：只用来证明某条指令是「必需」而不是「可选优化」 |
| `nginx-dns-probe.conf` | 桶域名不可解析时的 `nginx -t` 行为（G1） |
| `make_fixtures.py` | 生成桩对象（gitignored，避免往仓库塞二进制） |
| `mkcerts.sh` | 生成桩桶 TLS 证书（自签 CA，SAN=`bucket-a.test`） |
| `verify.sh` | 断言本体（A–F 组） |
| `run.sh` | 一键：生成 snippet → 起容器 → G 组 → 断言 → 关容器 |

**生成器不在这个目录里**，在 `backend/internal/nginxsnippet`（纯函数 + 单测）
与 `backend/cmd/nginx-snippets`（CLI）。这么放的三个理由：

1. 它最终要读 `tenant_storage_configs`、要被配置接口调用，属于后端代码而不是运维脚本；
2. snippet 里每一行都对应一个静默失败的坑，必须能用单测钉住（三条不变量见包注释）；
3. **只有一份生成器**。以前这里有个 `gen_site.py` 做同样的事，两个实现必然漂移，
   而漂移的表现是「本地全绿、线上裂图」。

## 跑法

```bash
bash run.sh          # tools/nginx-r1-check 下没有可执行位，用 bash
```

前置：`docker`（`nginx:alpine`）、`go`、`openssl`、`python3`、`curl`。

单独用生成器：

```bash
cd backend
go run ./cmd/nginx-snippets render --targets ../tools/nginx-r1-check/tenants.json \
    --out /tmp/tenants.conf --dns-precheck
go run ./cmd/nginx-snippets check  --targets ../tools/nginx-r1-check/tenants.json \
    --snippet /tmp/tenants.conf
```

## 这套装置**证明不了**什么（可信边界）

本地桩桶不是真桶，以下四条只能到真桶上验，不能拿本装置的绿当结论：

- 真实 OSS/S3/MinIO 的 Host 风格（path-style vs virtual-hosted-style）与签名行为；
- 真桶的 `Range` 语义差异（个别桶对多段 Range 返回 200 而非 206）；
- TLS 证书链是否为公共 CA 可验（本装置用自签 CA 只证明「配置写法可通」）；
- 桶侧 CDN（客户自己的 CDN）不会接受平台 refresher 的 Purge —— 见可行性文档 §4 R1-b。

## 与线上配置的关系

生成器只产出 `location` 段，**不含 map、不含 server**：生产上它要被 include 进
`nginx-site.live.conf` 的 `server{}` 里（`$uploads_cache` 那个 map 已在 http 级存在，
snippet 里再定义一次就是重复定义，`nginx -t` 直接报错）。

`site.conf` 是手写壳，只复刻 `/uploads/` 相关的 location 与缓存头 map，
其余整站配置（安全头、SPA 兜底、real_ip、敏感文件正则）不复制。因此
「本装置全绿」只说明 R1 这部分语义成立，不代表可以把整份配置直接替换线上。
