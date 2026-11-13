# R1 验证装置 · `/uploads/` 按租户前缀反代到对象桶

**这不是线上配置，是验证装置。** 目的只有一个：回答设计稿 §3.4 / 风险 R1 那句
「nginx 按租户反代尚未验证」——把它从假设变成有实测依据的结论。

线上配置的回归验证在 `tools/nginx-check/`（那套跑 75 项整站断言）。本目录只测
**R1 新增的那部分语义**，两者互不替代。

---

## 为什么需要单独一套

设计稿把 R1 写成「`/uploads/` 现在是本地静态托管，改成反代要同步改这里」。
**实读线上配置后这句是过时的**：`deploy/baota/nginx-site.live.conf:447-459` 里
`location ^~ /uploads/` 已经是 `proxy_pass http://127.0.0.1:8080`（Go 侧
`r.Static("/uploads", cfg.UploadDir)` 出盘）。也就是说 R1 不是「静态改反代」，
而是**在一条已经存在的反代上面，再加一层按租户前缀分流**——风险面比设计稿假设的小，
但多了四个新问题（见 `docs/tenant-storage-r1-feasibility.md` §2）：

1. `proxy_pass` 的 URI 部分怎么拼，才不会把对象键拼错（`/uploads/t/5/x.jpg` → 桶里 `t/5/x.jpg`）；
2. 桶自带的 `Cache-Control` 与 `add_header` 叠加成**两条**头，CDN 取哪个不确定；
3. 桶域名在 **reload 时**解析，解不出来 `nginx -t` 直接失败 → 一次填错域名会挡住建站；
4. 真桶校验 Host/SNI，不带 SNI 会被拒（421 / `Misdirected Request`）。

## 目录

| 文件 | 作用 |
| --- | --- |
| `nginx.conf` | 容器主配置：4 个桩上游（平台 Go + 两个桶 + 一个 500 桶）+ TLS 桩桶，include `conf.d/*.conf` |
| `gen_site.py` | 从 `tenants.json` 生成**产品形态**的 `conf.d/site.conf`（每租户一段 `location ^~ /uploads/t/<cid>/` + 回退段）；`--check` 做配置表↔snippet 对账 |
| `tenants.json` | 模拟 `tenant_storage_configs` 的 enabled 行（含一条 disabled 用于验回退） |
| `probes.conf` | **手写的对照探针，非产品形态**：只用来证明某条指令是「必需」而不是「可选优化」 |
| `nginx-dns-probe.conf` | 桶域名不可解析时的 `nginx -t` 行为（验证第 3 条风险） |
| `make_fixtures.py` | 生成桩对象（gitignored，避免往仓库塞二进制） |
| `mkcerts.sh` | 生成桩桶 TLS 证书（自签 CA，SAN=`bucket-a.test`） |
| `verify.sh` | 断言本体 |
| `run.sh` | 一键：生成 → 起容器 → 断言 → 关容器 |

## 跑法

```bash
bash run.sh          # tools/nginx-r1-check 下没有可执行位，用 bash
```

前置：`docker`（`nginx:alpine` 已本地缓存）、`openssl`、`python3`、`curl`。

## 这套装置**证明不了**什么（可信边界）

本地桩桶不是真桶，以下四条只能到真桶上验，不能拿本装置的绿当结论：

- 真实 OSS/S3/MinIO 的 Host 风格（path-style vs virtual-hosted-style）与签名行为；
- 真桶的 `Range` 语义差异（个别桶对多段 Range 返回 200 而非 206）；
- TLS 证书链是否为公共 CA 可验（本装置用自签 CA 只证明「配置写法可通」）；
- 桶侧 CDN（客户自己的 CDN）不会接受平台 refesher 的 Purge —— 见可行性文档 §4 R1-b。

## 与线上配置的关系

`gen_site.py` 输出的 `server{}` **只复刻** `/uploads/` 相关的 location 与缓存头 map，
其余整站配置（安全头、SPA 兜底、real_ip、敏感文件正则）不复制。因此
「本装置全绿」只说明 R1 这部分语义成立，不代表可以把整份配置直接替换线上。
