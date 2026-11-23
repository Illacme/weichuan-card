# R1 上线 runbook · 租户自有桶（平台域名托管）

> 范围：**R1（平台域名托管租户桶）**，不是 R1-b（客户自有域名/CDN）。
> R1 下资产仍走平台统一入口 `https://<平台域名>/uploads/t/<cid>/...`；
> 客户自有域名/CDN 那档（`nginx-site.live.conf` 里加客户 `server` 块+证书、
> R1-b 按租户 CDN 刷新）是**独立一轮**，见文末「不在本范围」。
>
> **第七十五轮起没有「开关」这一步**：`TENANT_STORAGE_ROUTE` 已删除，文件存储
> （`storage_profiles` + `storage_bindings`）是唯一数据源。**给企业绑上一套云文件存储，
> 它的新上传立刻进客户桶**，不需要重启、不需要翻开关。因此本手册从「翻开关清单」
> 变成「绑文件存储 + 让 nginx 认识新前缀」两件事。
>
> 本手册是照做清单：按顺序走，遇到报错看末尾「排错 / FAQ」。

## 前置状态

| 项 | 状态 | 证据 |
| --- | --- | --- |
| ① 缓存键含租户维度 | ✅ 代码已就绪 | 出参 URL 带 `companyId`/`staffId`，origin 不读 `Host`（`backend/internal/handler/publicorigin.go`）；5 条公开 GET 发 `public, s-maxage=60`（`pagination.go:65-68`） |
| ② `/uploads/t/<cid>/` 回源 snippet | ✅ 代码生成 + 验证装置 | `backend/internal/nginxsnippet/snippet.go`、`tools/nginx-r1-check/` |
| ③ CDN Purge（平台口径） | ✅ 已就绪 | 全局 refresher 按精确 URL 清，`backend/cmd/server/main.go` 单实例注入 |
| 全量迁移已同步（含 `0054` 建文件存储表、`0055` 删旧表） | 需人工确认 | 上线前 `./server migrate` 跑齐（详见 `docs/deploy-runbook.md`） |
| `PII_ENC_KEY` 已定 | 需人工确认 | 写任何业务 PII 前必须定下，**无 re-key**（文件存储凭据不加密，见 §0.1） |

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

### 0. 先决条件：至少有一个企业已绑定云文件存储

生成器读 `storage_profiles` + `storage_bindings`（`delivery_profiles` 参与渲染时的域名判定）。
**没有绑定**时生成的 snippet 只会是一堆注释——**R1 此刻没有任何可路由的企业**。

快速确认有没有可接管的企业（在生产库直接查）：

```sql
SELECT b.tenant_id, p.name, p.driver, p.bucket, p.public_read
FROM storage_bindings b
JOIN storage_profiles p ON p.id = b.storage_profile_id
WHERE p.driver <> 'local';
```

- `tenant_id = 0` 是**全局默认绑定**，生成器会把它展开成逐家企业（nginx 没有「全局 location」
  这种形态，必须一家一条 `location`）。
- `tenant_id > 0` 是该企业的覆盖绑定，优先于全局默认。
- `driver = 'local'` 的企业**不会**出现在 snippet 里：它们本来就走平台盘，不需要反代。
- 有结果 → 继续；空 → 先去后台「文件存储」建一套云文件存储并绑定给企业，再回来。

> 绑定**即时生效**（`Resolver.Touch()` 会让下一次解析看到新值，不需要重启后端）。
> 但 **nginx 侧需要重新 `render` + `reload` 才会认识新的 `t/<cid>/` 前缀**——
> 这一步是手工的，绑完文件存储不重新生成 snippet，新上传的图会 404（对象进了客户桶，
> 但平台接入层还不知道该往哪转发）。

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

- **数据源必须显式给一个**：`--dsn`（读库）或 `--targets`（本地 JSON），二者互斥。
  故意不允许「连不上库就退回 JSON」，也不从环境变量自动取——这份输出决定线上
  哪些 `/uploads/t/<cid>/` 会被代理到哪个桶，数据源不确定就等于事后无法复盘。
- 输出**只有 `location` 段**，不含 `map`、不含 `server`（`snippet.go:172` 注释）。
- `$uploads_cache` 这个 map 已在 `nginx-site.live.conf:68-79`（http 级）存在，snippet 里直接引用即可。
- **空结果保护**（重要）：若生成结果零 location，但 `--out` 指向的文件已存在且非空，
  生成器**默认拒绝覆盖**（防止「误删绑定」把全部企业撕回平台盘）。
  若确实要清空，加 `--allow-empty`。这通常是「库里 0 条绑定 + 旧 snippet 文件还在」触发的，
  直接 `rm /tmp/tenant-storage.conf` 再跑即可。

### 2. 对账（部署前必跑，防漏配）

```bash
/tmp/nginx-snippets-linux check \
    --dsn '<同上DSN>' \
    --snippet /tmp/tenant-storage.conf
# 退出码 0 = 该有 location 的企业都有、不该有的（local 文件存储）都没；不一致则非零退出。
# check 与 render 必须用同一个数据源（同一个 --dsn / --targets），否则对账的是两份不同的配置。
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

此时**对已存在的对象没有任何行为变化**：还没有 `t/<cid>/` 形态的对象，这段 location
永远不命中，所有 `/uploads/` 仍走回退段（平台盘）。

### 5. 生效：不需要翻开关，绑定已经生效了

第七十五轮删掉了 `TENANT_STORAGE_ROUTE`。写侧与出参 origin **同源**：
`storageprofile.Resolver` 同时回答「这个企业用哪个桶写」与「这个企业的对象对外用什么域名」，
所以不存在「写入切了、出参没切」的中间态，也不需要分步切。

如果企业是**刚绑上**文件存储的，确认后端已经看到（跨进程最多晚 700ms 指纹 TTL，
通过管理接口保存的会立刻 `Touch()` 生效）：

```bash
# 后台「文件存储」页保存一次即可触发；或直接看日志有无配置重载记录
```

> 要**临时**让某家企业回到平台盘：把它的绑定改到系统自带的 local 文件存储（或删掉绑定让它落全局默认）。
> 同样是即时生效，且**已写进客户桶的历史对象不会自动搬回**（那是回填任务，见 §6）。

### 6. 验收

1. 挑一个已绑定云文件存储的企业，在后台上传一张图 →
   对象应落在**该企业桶**的 `t/<cid>/` 前缀下（不是平台盘）。
2. 用平台域名取该图 `GET https://<平台域名>/uploads/t/<cid>/<key>` → 200，
   且由 snippet 的 `proxy_pass` 打到客户桶（`proxy_hide_header Cache-Control` 后
   由 `$uploads_cache` 统一发头，404→no-store）。
3. 改图后触发 CDN 刷新（`POST /api/admin/cdn/refresh` 或代码内自动 refresh）→
   平台 refresher 能命中平台 CDN 边缘（R1 成立）。
4. 把该企业的绑定改回 local 文件存储 → 其**新**上传回平台盘；
   老的 `t/<cid>/` 对象仍按 snippet 从客户桶取（不会 404，也不会自动搬回）。
5. **反向验证**：删掉某家企业的绑定并重新 `render`，确认那家的 `location` 消失、
   `check` 仍然通过——否则「删绑定」在接入层是静默无效的。

### 回滚

- **配置回滚**：把企业绑定改回系统自带的 local 文件存储（或删绑定落全局默认）。即时生效，无需重启。
- **接入层回滚**：重新 `render` + `reload`，或直接把 `--out` 文件清空。
  留着无害（无 `t/<cid>/` 对象时不命中）。
- 注意：已写进客户桶的历史对象不会因为回滚而搬回平台盘。要搬回得走
  `cmd/storage-backfill`，且它的目标端**只认云文件存储**——`driver=local` 会直接报错
  「不回退平台盘」。

---

## 不在本范围（R1-b，独立一轮）

- 客户自有域名 `server` 块 + TLS 证书：仓库无生成代码，需运维在宝塔**逐个域名手加**；
  生成器只产 `location` 段（`snippet.go:172`），不含 server/证书。
- R1-b 按租户自有 CDN 刷新：`delivery_profiles` 目前只有域名与刷新配置，
  `cdnRefreshHandler`/`CdnRefresh.vue` 无租户维度，平台 refresher 够不到客户边缘
  （`docs/tenant-storage-r1-feasibility.md` §4 R1-b 标「未闭环」）。
- 这两件是「客户自有域名出图」的前置，与 R1（平台托管）解耦，需单独决策与开发。
- **2026-11-18 复核：仍未实施，且不建议顺手做。** R1-b 缺两样外部输入：客户 CDN 的
  刷新接口/凭据，以及「下架违规图时谁 Purge 客户边缘」的责任归属（否则 `/uploads/`
  的 immutable 缓存会让下架在客户边缘静默失效，见 `docs/tenant-storage-r1-feasibility.md` §4）。
  这些不是代码能单方面补出来的，必须与具体客户一起评审后再排期；本手册的 R1 步骤不依赖它们。

---

## 排错 / FAQ

**Q：`render` 输出「零 location」，snippet 里只有注释**
A：库里没有任何企业绑定云文件存储（或全部绑的是 local）。R1 暂时没东西可路由。
先去管理后台「文件存储」建一套云文件存储并绑定给某企业，再跑 `render` 才会出 `location` 块。
**不要**加 `--allow-empty` 把已有 snippet 清掉——那会让全部企业撕回平台盘。

**Q：`文件存储配置表不存在（迁移 0054 未应用）`**
A：库还没跑 `0054_storage_profiles`。先 `./server migrate`。生成器**故意不降级**：
文件存储表缺失时直接报错，而不是「当作没有绑定」——后者会静默把全站撕回平台盘。

**Q：`企业 N 的文件存储绑定不可用（请先修好再生成 snippet）`**
A：某条 `storage_bindings` 指向的 `storage_profile_id` 不存在。生成器**报错而不是跳过**：
跳过等于让那家企业悄悄回落平台盘，且没有任何人会发现。

**Q：`本次渲染没有任何 location，但 /tmp/tenant-storage.conf 已存在且非空`**
A：安全守卫在生效——本次结果会是空 snippet，但不许覆盖一个已有内容的文件（防误清空）。
原因通常是「库里 0 条绑定 + 旧 snippet 文件还在」。解决：`rm /tmp/tenant-storage.conf` 后重跑；
或若确实要清空所有 location，加 `--allow-empty`。

**Q：`Exec format error` / `cannot execute`**
A：二进制架构与生产服务器不符。本手册默认 `GOARCH=amd64`；ARM64 机器改用 `GOARCH=arm64` 重编。

**Q：`桶域名解析失败，拒绝生成`**
A：`--dns-precheck` 解析 bucket endpoint 不通。先确认 DNS / 网络能解析该域名；
确属无误但暂不想卡这一步，去掉 `--dns-precheck`（风险：万一 endpoint 真错，`nginx -t` 会 emerg）。

**Q：include 后 `nginx -t` 报 `duplicate location` 或 `map` 重复定义**
A：重复 include 了 snippet（显式 include + extension 通配都加了）。二选一去掉一处。
若报 `$uploads_cache` 重复定义，说明 snippet 里误带了 map——正常 snippet 只有 location 段，不含 map/server。

**Q：后台已经绑了云文件存储，上传的图还是 404**
A：两种成因，先分清是哪一种：
1. **对象进了客户桶，但 nginx 不认识前缀** → 忘了重新 `render` + `reload`。查 snippet 里有没有
   该企业的 `location`（`check` 会告诉你）。
2. **对象还在平台盘或取图 403** → 查该企业有没有被更具体的绑定覆盖、文件存储 `driver` 是不是
   还是 local。桶 `public_read=false`（私有桶）本身合法，但必须重新 `render` + `reload`：
   新版 snippet 会把 `/uploads/t/<企业id>/` 指向后端现签并 302，旧版仍是匿名回源私有桶，必 403。

---

## 已就绪的守门

- `tools/nginx-r1-check/run.sh`：本地桩桶验证 `/uploads/t/<cid>/` 分流语义（A–F 组断言）。
- `backend/internal/handler/publicorigin_test.go`：出参 origin 按租户维度、不读 Host。
- `backend/internal/storageprofile/resolver_test.go`：平台实例绝不被文件存储覆盖、
  无绑定回落平台、指纹变化即重载。
- `nginxsnippet` 单测 + `cmd/nginx-snippets check`：文件存储绑定↔snippet 对账，漏配即非零退出。
