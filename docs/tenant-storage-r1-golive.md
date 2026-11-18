# R1 上线 runbook · 翻 TENANT_STORAGE_ROUTE=on（平台域名托管）

> 范围：**R1（平台域名托管租户桶）**，不是 R1-b（客户自有域名/CDN）。
> R1 下资产仍走平台统一入口 `https://<平台域名>/uploads/t/<cid>/...`，
> `public_base_url` 留空；客户自有域名/CDN 那档（`nginx-site.live.conf` 里加
> 客户 `server` 块+证书、R1-b 按租户 CDN 刷新）是**独立一轮**，见文末「不在本范围」。
>
> 本手册是照做清单：按顺序走，遇到报错看末尾「排错 / FAQ」。

## 前置状态（翻开关前已就绪，无需新代码）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| ① 缓存键含租户维度 | ✅ 代码已就绪 | 出参 URL 带 `companyId`/`staffId`，origin 不读 `Host`（`backend/internal/handler/publicorigin.go:41-63`）；5 条公开 GET 发 `public, s-maxage=60`（`pagination.go:65-68`） |
| ② `/uploads/t/<cid>/` 回源 snippet | ✅ 代码生成 + 验证装置 | `backend/internal/nginxsnippet/snippet.go:176-241`、`tools/nginx-r1-check/` |
| ③ CDN Purge（平台口径） | ✅ 已就绪 | 全局 refresher 按精确 URL 清，`backend/cmd/server/main.go:373-417` 单实例注入 |
| 全量迁移已同步 | 需人工确认 | 上线前 `./server migrate` 跑齐（详见 `docs/deploy-runbook.md`） |
| `PII_ENC_KEY` 已定 | 需人工确认 | 写任何 PII 前必须定下，无 re-key |

> ⚠️ **唯一必须人工验收的代码外前置**：确认 Cloudflare 缓存键**含 query string**
> （`companyId`/`staffId`），不折叠。否则 A 企业客户域名的响应会被发给 B 企业访客。
> 代码侧已保证 URL 带租户维度且不读 `Host`，但折叠 query string 是 CF 配置项，代码无法强制
> （`docs/tenant-storage-design.md` §3.8 / R6、`docs/commercial-readiness.md` R1-b 段）。
> 验收法：对同一张图用不同 `?companyId=` 各打一次，确认 CF 当作不同对象（不同 HIT/MISS）。

---

## 准备：生成器二进制（生产服务器通常**没有 Go 工具链**）

生成器 `backend/cmd/nginx-snippets` 是独立二进制，**不依赖 Go 运行时**，但需要在**有 Go 的机器**
上交叉编译一次，再把二进制拷到生产服务器跑。生产服务器**不需要装 Go**。

```bash
# 在任意有 Go 的机器（开发机 / CI / 本沙箱）上：
cd backend
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o nginx-snippets-linux ./cmd/nginx-snippets
# 产物是静态链接的 ELF，约 16MB，无需任何系统库
file nginx-snippets-linux   # 应显示 ELF 64-bit LSB executable, x86-64, statically linked
```

- **架构对不上会报 `Exec format error`**：如果生产服务器是 ARM64（部分云实例），把
  `GOARCH=amd64` 改成 `GOARCH=arm64` 重新编一份。
- 拷贝到生产服务器：
  ```bash
  scp nginx-snippets-linux user@<生产服务器>:/tmp/
  ```
- 之后所有 `render` / `check` 命令都在**生产服务器**上用这个二进制跑（见下），不再需要 Go。

---

## 步骤

### 0. 先决条件：至少有一个租户已配置自有存储

生成器读 `tenant_storage_configs` 表。如果这张表**没有行**，生成的 snippet 只会是一堆注释——
**R1 此刻没有任何可路由的租户，翻开关也是空操作**。

> 实测踩过的坑：在 production 上跑 `render` 曾报
> `数据源：mysql（tenant_storage_configs 共 0 行）`。这说明**还没有任何租户在后台配过自有存储**。
> 必须先让至少一个租户在管理后台配好：driver / endpoint / bucket / 凭据，且 `enabled=1`。
> 配完之后表里才会出现行，snippet 才会长出 `location` 块。

快速确认有没有可接管的租户（在生产库直接查）：
```sql
SELECT company_id, driver, bucket, enabled
FROM tenant_storage_configs
WHERE enabled = 1 AND driver <> 'local';
```
有结果 → 继续；空 → 先去后台给租户配存储，再回来。

### 1. 生成 snippet（在生产服务器上，用二进制）

```bash
ssh user@<生产服务器>
/tmp/nginx-snippets-linux render \
    --dsn '<生产DSN，形如 root:xxx@tcp(127.0.0.1:3306)/weichuan>' \
    --out /tmp/tenant-storage.conf \
    --dns-precheck
# 落盘前会解析每个桶域名；解析失败直接拒绝（否则 nginx -t 会 emerg 卡住整份配置）。
# stderr 摘要：已接管 / 未接管（回落平台盘）的 company_id 列表。
```

- 输出**只有 `location` 段**，不含 `map`、不含 `server`（`snippet.go:172` 注释）。
- `$uploads_cache` 这个 map 已在 `nginx-site.live.conf:68-79`（http 级）存在，snippet 里直接引用即可。
- **空结果保护**（重要）：若生成结果零 location，但 `--out` 指向的文件已存在且非空，
  生成器**默认拒绝覆盖**（防止「误清配置表」把全部租户撕回平台盘）。
  若确实要清空，加 `--allow-empty`。这通常是「库里 0 行 + 旧 snippet 文件还在」触发的，
  直接 `rm /tmp/tenant-storage.conf` 再跑即可。

### 2. 对账（部署前必跑，防漏配）

```bash
/tmp/nginx-snippets-linux check \
    --dsn '<同上DSN>' \
    --snippet /tmp/tenant-storage.conf
# 输出「✓ 配置表与 snippet 一致」= 该有 location 的租户都有、不该有的（已停用）都没。
```

### 3. 落盘 + include 进线上 server{}

把生成的文件放到能被 nginx include 的位置，并在 `deploy/baota/nginx-site.live.conf` 的
`server{}` 内、`location ^~ /uploads/ {`（约 447 行）**之前**确保有一行 include：

```nginx
    # 租户对象桶反代（由 nginx-snippets 生成，勿手改）
    include /www/server/panel/vhost/nginx/extension/weichuan.ziyuanxingqiu.cn/tenant-storage.conf;
```

> 仓库里的 `nginx-site.live.conf` 已预留**注释态**的这行（搜 `R1 上线`），取消注释即可。
> 注意：上面 extension 目录的 `*.conf` 通配（约 219 行）也会自动 include 此文件，
> 显式 include 与靠通配**二选一**，勿两处都加导致重复 include。
> 顺序不影响正确性：`^~ /uploads/t/<cid>/` 比 `^~ /uploads/` 前缀更长，nginx 按**最长前缀**命中，
> tenant 段天然优先。

把 `/tmp/tenant-storage.conf` 传到该路径（如 `scp` 或 `cat` 粘贴），然后：

### 4. nginx -t + reload

```bash
nginx -t            # 宝塔面板「保存」也会自动跑；确认无 emerg
nginx -s reload     # 或面板保存触发
```

此时**没有任何行为变化**：还没有 `t/<cid>/` 形态的对象，这段 location 永远不命中，
所有 `/uploads/` 仍走回退段（平台盘）。

### 5. 翻开关（写入侧 + 出参 origin 一起生效）

服务进程环境变量设 `TENANT_STORAGE_ROUTE=on`，重启服务（`resolver.go` 的
`RouteEnabled()` 读 env，on/1/true/yes 才开；默认 off）。

```bash
# 在部署的环境变量/单元文件里加：
TENANT_STORAGE_ROUTE=on
# 重启后端进程（systemd / docker 重启），使 env 生效
```

> 开关是**单一**变量，同时管「上传路由到租户桶」与「出参 origin 选客户桶」。
> 因为对象键布局也由它决定（`/uploads/t/<cid>/...` vs 平台 `/uploads/...`），
> 读与写在开关下天然同步，不需要分步切。

### 6. 验收

1. 挑一个已配自有桶且 `enabled=1` 的租户，在后台上传一张图 →
   对象应落在**该租户桶**的 `t/<cid>/` 前缀下（不是平台盘）。
2. 用平台域名取该图 `GET https://<平台域名>/uploads/t/<cid>/<key>` → 200，
   且由 snippet 的 `proxy_pass` 打到租户桶（`proxy_hide_header Cache-Control` 后
   由 `$uploads_cache` 统一发头，404→no-store）。
3. 改图后触发 CDN 刷新（`POST /api/admin/cdn/refresh` 或代码内自动 refresh）→
   平台 refresher 能命中平台 CDN 边缘（R1 成立）。
4. 停用一个租户配置（`enabled=0`）→ 其 `/uploads/t/<cid>/` 回落平台盘（不 404）。

### 回滚

- 关开关：`TENANT_STORAGE_ROUTE=off` + 重启 → 新上传回平台盘。
- snippet 可留可删：留着无害（无 `t/<cid>/` 对象时不命中）；要彻底清就删 `--out` 文件并对账。
- 注意：已写进租户桶的历史对象不会因为关开关而搬回平台盘（那是回填任务，非本范围）。

---

## 不在本范围（R1-b，独立一轮）

- 客户自有域名 `server` 块 + TLS 证书：仓库无生成代码，需运维在宝塔**逐个域名手加**；
  生成器只产 `location` 段（`snippet.go:172`），不含 server/证书。
- R1-b 按租户自有 CDN 刷新：`TenantStorageConfig` 无 per-tenant CDN 字段、
  `cdnRefreshHandler`/`CdnRefresh.vue` 无租户维度，平台 refresher 够不到客户边缘
  （`docs/tenant-storage-r1-feasibility.md` §4 R1-b 标「未闭环」）。
- 这两件是「客户自有域名出图」的前置，与 R1（平台托管）解耦，需单独决策与开发。
- **2026-11-18 复核：仍未实施，且不建议顺手做。** R1-b 缺两样外部输入：客户 CDN 的
  刷新接口/凭据，以及「下架违规图时谁 Purge 客户边缘」的责任归属（否则 `/uploads/`
  的 immutable 缓存会让下架在客户边缘静默失效，见 `docs/tenant-storage-r1-feasibility.md` §4）。
  这些不是代码能单方面补出来的，必须与具体客户一起评审后再排期；本手册的 R1 步骤不依赖它们。

---

## 排错 / FAQ

**Q：`数据源：mysql（tenant_storage_configs 共 0 行）`**
A：库里没有任何租户配置自有存储。R1 暂时没东西可路由。先去管理后台给某租户配好存储
（driver / endpoint / bucket / 凭据，`enabled=1`），表里出现行后再跑 `render` 才会出 `location` 块。

**Q：`本次渲染没有任何 location，但 /tmp/tenant-storage.conf 已存在且非空`**
A：安全守卫在生效——本次结果会是空 snippet，但不许覆盖一个已有内容的文件（防误清空）。
原因通常是「库里 0 行 + 旧 snippet 文件还在」。解决：`rm /tmp/tenant-storage.conf` 后重跑；
或若确实要清空所有 location，加 `--allow-empty`。

**Q：`Exec format error` / `cannot execute`**
A：二进制架构与生产服务器不符。本手册默认 `GOARCH=amd64`；ARM64 机器改用 `GOARCH=arm64` 重编。

**Q：`桶域名解析失败，拒绝生成`**
A：`--dns-precheck` 解析 bucket endpoint 不通。先确认 DNS / 网络能解析该域名；
确属无误但暂不想卡这一步，去掉 `--dns-precheck`（风险：万一 endpoint 真错，`nginx -t` 会 emerg）。

**Q：include 后 `nginx -t` 报 `duplicate location` 或 `map` 重复定义**
A：重复 include 了 snippet（显式 include + extension 通配都加了）。二选一去掉一处。
若报 `$uploads_cache` 重复定义，说明 snippet 里误带了 map——正常 snippet 只有 location 段，不含 map/server。

---

## 已就绪的守门（翻前放心）

- `tools/nginx-r1-check/run.sh`：本地桩桶验证 `/uploads/t/<cid>/` 分流语义（A–F 组断言）。
- `backend/internal/handler/publicorigin_test.go`：出参 origin 按租户维度、不读 Host。
- `nginxsnippet` 单测 + `cmd/nginx-snippets check`：配置表↔snippet 对账，漏配即非零退出。
