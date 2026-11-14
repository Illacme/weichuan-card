# 多租户自有存储 · R1「按租户前缀反代到对象桶」可行性验证

> **状态：验证已完成，结论「有条件成立」。** 本轮只加验证装置与文档，
> **未改一行生产代码、未动线上 nginx、未写迁移、未改库中数据形态。**
> 全部结论来自 `tools/nginx-r1-check/` 的实测（39 项行为断言 + 2 项失败预期 + 6 组反向验证），
> 不引用印象。日期 2026-11-13。

---

## 0. 这一轮问什么

设计稿 §3.4 与风险 R1 把「nginx 按租户反代」写作**唯一尚未验证的假设**，并给了处置口径：
「先在测试环境 `nginx -t` + 行为验收跑通再铺开；不通则退回客户自有域名路线」。

本轮就是执行这句。要回答的是六个具体问题，不是「反代能不能跑」这种笼统问法：

| # | 问题 | 判据 |
| --- | --- | --- |
| Q1 | `/uploads/t/5/x.jpg` 到桶里必须变成 `t/5/x.jpg`，不能变成 `x.jpg`、也不能带 `/uploads/` | 200 + `X-Amz-Request-Id` 来自对的桶 |
| Q2 | 两家租户不能串桶；`t/50/` 不能被 `t/5/` 前缀吞掉 | 交叉取对象必 404；`t/50/` 必走平台 |
| Q3 | 没有租户配置的前缀必须回落平台盘（**关配置=回默认，不是 404**） | `X-Stub: go-platform` |
| Q4 | 桶自带的 `Cache-Control` 不能和站点 `add_header` 叠加成两条 | 该头出现次数 = 1 |
| Q5 | 状态码分流（404/5xx → `no-store`）、Range/206、HEAD、304 是否仍成立 | 逐条断言 |
| Q6 | 桶域名解析失败时会发生什么（会不会挡住建站） | `nginx -t` 行为 |

---

## 1. 先订正一个事实：`/uploads/` 早就是反代，不是静态托管

设计稿 §3.4 写「`deploy/baota/nginx-site.live.conf` 的 `/uploads/` 现在是本地静态托管」。
**实读该文件 447-459 行，这句是过时的：**

```nginx
location ^~ /uploads/ {
    proxy_pass http://127.0.0.1:8080;
    ...
    add_header Cache-Control $uploads_cache always;
}
```

`/uploads/` 从来都反代到 Go（`main.go` 里 `r.Static("/uploads", cfg.UploadDir)` 出盘）。
nginx 手上没有 `root`，也就没有静态文件路径。

**这条订正把 R1 的性质改了**：不是「把静态托管改成反代」（那是动地基），
而是「在一条已经存在的反代上面，按前缀多分几路」。风险面比设计稿假设的小，
但因此多了四个设计稿没提到的新问题（§4）。

---

## 2. 六问的答案（全部实测）

装置：`tools/nginx-r1-check/`，`bash run.sh` 一键跑，39 项断言全绿。
桩上游用 nginx 自己扮演桶（静态文件 + Range + 桶风格头），并打 `X-Amz-Request-Id`
区分是哪家桶——**「有没有串桶」是靠响应头断言的，不是靠看日志猜**。

| # | 结论 | 关键证据 |
| --- | --- | --- |
| Q1 | ✅ 成立（第六十三轮修正路径风格） | `rewrite ^/uploads/(.*)$ /<bucket>/$1 break;` + `proxy_pass http://桶;`（不带 URI 部分）→ 桶收到 `/<bucket>/t/5/only-a.jpg`。A1/A2/A3。**注**：本节表格里的「桶收到 `/t/5/only-a.jpg`」是第五十五轮桩桶（nginx 静态目录当桶根）下的形态；真桶要的是 `/<bucket>/<key>`，见 §R1-a 的订正 |
| Q2 | ✅ 成立 | 交叉取对象 404（A4/A7）；`t/50/` 走平台（B1）。**依赖 location 尾斜杠**：写成 `location ^~ /uploads/t/5` 就会吞掉 `t/50/`，反向验证 3 已复现 |
| Q3 | ✅ 成立 | 未启用租户 `t/9/`、平台对象 `p/library/`、存量老图 `<rand>.jpg` 全部回落 Go（B2/B4/B6） |
| Q4 | ✅ 成立，但**必须显式藏** | 加 `proxy_hide_header Cache-Control; proxy_hide_header Expires;` 后该头恰好 1 条（C1/C3）。不写这两行 → 出现**两条** `Cache-Control`（E2 实测，取值 `max-age=3600` 与 `immutable` 并存） |
| Q5 | ✅ 成立 | 200/206/304 → `immutable`；404/5xx → `no-store`（C6/C7/D8）；Range 返回 206 + `Content-Range: bytes 0-99/65536`（D1-D3）；HEAD 的 `Content-Length` 与对象等长（D4/D5）；ETag 透传、304 协商成立（D6/D7）；`Accept-Ranges` 透传（C5） |
| Q6 | ⚠️ **会挡住建站** | 桶域名写成不可解析（`.invalid`）时 `nginx -t` 直接 `emerg: host not found in upstream`（G1）。宝塔「test 失败就不 reload」，于是**整份配置的更新全部被卡住**，不只是这一家租户 |

另外两条也实测了：

- **TLS 桶不能只填证书校验**：`proxy_ssl_verify on` + 可信 CA + `proxy_ssl_name` 齐了，
  但缺 `proxy_ssl_server_name on` 仍会被拒（E3 = 421）。加上就通（E4 = 200）。
  真桶（OSS/S3/MinIO）对 Host/SNI 不匹配的拒绝方式不同（421 / 403 / 签名错），
  但「必须有 SNI」这条结论一致。
- **`nginx:alpine` 自带 `/etc/ssl/certs/ca-certificates.crt`**（181724 字节，实测）。
  所以 `proxy_ssl_verify on; proxy_ssl_trusted_certificate /etc/ssl/certs/ca-certificates.crt;`
  在官方镜像里可用。**宝塔跑在 CentOS 时该路径是 `/etc/pki/tls/certs/ca-bundle.crt`，
  这条未在目标机验证过，生成 snippet 前要按发行版确认。**

---

## 3. 装置与它的可信边界

```
端上 → nginx(8090 产品段 / 8091 对照探针)
        ├─ /uploads/t/5/  → 桶A(9001)      ← 生成物 snippets/*.conf（Go 生成器产出）
        ├─ /uploads/t/6/  → 桶B(9002)
        ├─ /uploads/t/8/  → 故障桶(9003, 恒 500)
        ├─ /uploads/      → 平台 Go(8080)  ← 手写回退段（= 线上现状，不是生成物）
        └─ /probe/sni|no-sni|no-hide|opt-a → 对照探针（手写，非产品形态）
```

生成物与手写件的边界是刻意切的，而且**生产上走同一条路径**：线上往
`nginx-site.live.conf` 的 `server{}` 里加一行 `include .../tenant-storage/*.conf`，
生成器只往那个目录写 `location` 段。本装置因此连「include 机制本身」一起验了
（`site.conf` 里那行 `include /cfg/snippets/*.conf`）。

**生成器在 `backend/internal/nginxsnippet` + `backend/cmd/nginx-snippets`**，
不是运维脚本：它要读 `tenant_storage_configs`、要被配置接口调用，属于后端代码；
而且 snippet 里每一行都对应一个静默失败的坑，必须能用单测钉住。
三条不变量（尾斜杠、`proxy_pass` 不带 URI、hide 在 add 之前）写在包注释里，
由 `snippet_test.go` 逐条断言。

> 曾有第二份实现（`tools/nginx-r1-check/gen_site.py`），验证轮之后已删除：
> 两个生成器必然漂移，而漂移的表现是「本地全绿、线上裂图」，正是本轮要防的那类故障。
> 现在装置只跑 Go 生成器 —— 断言绿等于**将要上线的那个生成器**是对的。

**可信的部分**：nginx 自己当上游时，Range 分片、`ETag`/`Last-Modified`/`Accept-Ranges`
都是真产生的，所以 Q4、Q5 测的是真行为；容器里桩桶挂 TLS + SAN=`bucket-a.test`，
`proxy_ssl_name` 少写就校验失败，所以 SNI 那条也是真行为。

**证明不了的部分**（不能拿本装置的绿当结论）：

1. 真实 OSS/S3/MinIO 的 Host 风格（path-style vs virtual-hosted）与签名行为；
2. 个别桶对多段 Range 返回 200 而非 206 的差异；
3. 公共 CA 证书链是否可验（本装置用自签 CA 只证明配置写法可通）；
4. **客户自己的 CDN 不会接受平台 refresher 的 Purge** —— 见 §4 R1-b。

---

## 4. 四个新约束 + 一条阻断项

### R1-a 前缀必须带尾斜杠，且键布局只能在 Go 侧定义

`location ^~ /uploads/t/5/` 的尾斜杠是**隔离边界**：去掉它，`/uploads/t/50/...`
会被 `t/5` 前缀吞掉，打到错误的桶。反向验证 3 复现了这个后果（B1 报红）。

**为什么不把 `t/<cid>/` 写进 `proxy_pass`**：`proxy_pass http://桶/t/5/;` 也能通（E1 实测 200），
但那样 nginx 也要知道键布局，将来 `ObjectKeyFor` 一改就要同步改 Go + 每一条 snippet，
漏一条 = 那家租户全站裂图且没有报错。所以产品形态一律用 `rewrite` 剥前缀——**nginx 只知道「剥前缀」，键长什么样它不管**。

### R1-a 订正（第六十三轮，真桶实测）：桶名必须出现在 path 或 Host 里，且只出现一次

上面那段在桩桶下完全成立，但桩桶有一个隐藏假设：**桶根就是目录根**（虚拟主机风格）。
第五十五轮的桩用 nginx 静态目录扮演桶，`root /cfg/www/bucket-a` + 路径 `/t/5/x.jpg` 正好读得到
`bucket-a/t/5/x.jpg`——于是「只剥 `/uploads/`」看起来是对的。

换成真 S3 服务端（versitygw，`tools/s3-e2e/`）之后，同一个请求得到的是：

```
GET /t/7/e2e-xxx.png
400 InvalidBucketName  <BucketName>t</BucketName>
```

S3 把路径第一段当成了桶名。所以生成器现在按 `host_header` 判定风格：

| host_header | 风格 | rewrite |
| --- | --- | --- |
| 留空 / 等于 endpoint | 路径风格（自建 MinIO、单 IP 最常见） | `/uploads/(.*)$ → /<bucket>/$1` |
| 以 `<bucket>.` 开头 | 虚拟主机风格（AWS S3 / COS 控制台给的域名） | `/uploads/(.*)$ → /$1` |
| 其他 | **拒绝**（判断不出风格，猜错 = 全站裂图且无报错） | — |

桶名进 path **不违反**「键布局只在 Go 侧」：桶名是这行配置自己的属性（同一条 location 的
`proxy_pass` 就指向它），而 `t/<cid>/` 仍然只由 `ObjectKeyFor` 定义。桩装置相应改成
在桶根下放一个以桶名命名的自指目录，把「桶根 + 对象键」这件事如实表达出来。

### R1-b（阻断项，条件成立）下架覆盖 × immutable × 租户自有 CDN

`takedownMediaCheck`（`handler/media_check.go:460`）是**覆盖写同一个对象键**
（`up.SaveBytes(row.ObjectKey, ...)`），覆盖完靠 `refreshCDN(db, refresher, row.URL)` 让边缘失效。
而 `/uploads/` 下除 `wxacode/` 之外全部是 `immutable` 一年。

这意味着：**Purge 是下架能不能生效的唯一依赖**，它失败就等于「后台显示已下架、
用户还能看一年违规图」。`refreshCDN` 的设计是「失败只记日志不报错」（`upload.go:145`），
所以这条链上没有任何一处会拦住它。

现状本来就如此（2026-10-23 补接线后才真正触发刷新），**R1 不改恶化现状，只是把它带进多租户语境**：
现在 Purge 用的是**平台**那一把 refresher。第一阶段试点是「公开读桶 + `public_base_url` 留空
（一律走平台统一入口）」，此时桶前面的边缘就是平台这把 CF，Purge 按 URL 打得到，**成立**。

一旦开放「客户自有域名 / 客户自己的 CDN」，平台 Purge 打不到客户的边缘 →
**违规图会在客户 CDN 上留一年且平台无从察觉**。所以：

> **R1-b 的结论是「有条件成立」：第一阶段口径（§3.4 单租户、公开读桶、`public_base_url` 留空）
> 下 R1 成立；开客户自有域名之前，必须先回答「谁负责 Purge 租户自有边缘」，
> 否则内容安全链在最贵的那一步静默断掉。**

> **第六十九轮补记（2026-11-14）**：`public_base_url` 的配置面（第六十八轮）与
> 出参侧按租户选 origin（第六十九轮）**已在代码侧闭环**，别再把上文「留空」读成
> 「这列现在不能用」。但 `TENANT_STORAGE_ROUTE` 默认仍 off，且
> `handler.publicOriginState` 要求对象真的写进客户桶后域名才生效，
> 所以第一阶段试点的**实际效果**与留空一致。
> **R1-b 本身没有闭环**：一旦翻开关、客户自有 CDN 接在前面，「谁 Purge 租户自有边缘」
> 仍必须先回答，下架覆盖写违规图在客户 CDN 留一年的风险不变。
> 另新增一条翻开关前检查项：按租户选 origin 后接入层 cache key 必须含
> `companyId` / `staffId` 且不折叠 query string（设计稿 §3.8）。

可选的处置（未实施，留给后续评审）：下架改为「写固定占位 key + 改库引用」而不是覆盖原地；
或给「可被覆盖的对象」划一个 `no-cache` 子空间。

### R1-c 桶域名在 reload 时解析 → 生成器必须先做 DNS 预检

G1 实测：域名解析不出来 → `nginx -t` emerg → 宝塔不 reload → **整份配置更新被卡住**，
连带本该生效的其它改动一起挡掉。这种失败模式很坏：现象是「保存了但没生效」，
没有任何一条报错指向「某家租户的桶域名打错了」。

落地要求：snippet 生成器在写文件前先 `net.LookupHost(桶域名)`，解析失败就拒绝生成并
在后台标红该租户的 `status` / `last_error`，**不要把这个发现权交给 nginx**。

另一面：静态 `proxy_pass` 的 IP 在 reload 时固定。桶域名指向变更后，不 reload 就一直打旧 IP。
若要运行期动态解析，得用 `resolver` + 变量形式的 `proxy_pass`——但变量形式下 nginx 不再
帮你处理 URI，必须自己拼完整路径，且 `proxy_ssl_server_name` 变成必需。
**这条没在本轮验证**，第一阶段按「换域名就 reload」处理即可。

### R1-d CA 路径按发行版确认

见 §2 末尾。`nginx:alpine` 已实测有 `/etc/ssl/certs/ca-certificates.crt`；
宝塔在 CentOS 上是 `/etc/pki/tls/certs/ca-bundle.crt`。生成 snippet 时按发行版探测，
写死一个路径会在换机器时以 `502` 的形式爆出来（`proxy_ssl_verify` 失败），
且报错只说「SSL_do_handshake() failed」，不说是证书路径错了。

### R1-e 缓存头必须显式藏，不能靠「桶应该不带」

E2 实测：不写 `proxy_hide_header Cache-Control` 时，响应里两条 `Cache-Control` 并存
（桶的 `max-age=3600` + 站点的 `immutable`）。RFC 9111 不规定 CDN 取哪条，
**同一个对象在不同边缘节点可能出现两种缓存时长**，且没有任何报错。

这也是为什么「把桶配置成不带 Cache-Control」不是解法：桶的默认行为随厂商/控制台设置变，
换一家桶或有人在控制台点了一下，行为就变了。**必须在 nginx 侧显式藏掉再自己发。**

---

## 5. 结论

**R1 成立，有条件。**

- nginx 层语义全部通过（39 项断言 + 2 项失败预期），四个设计稿未预见的坑
  （尾斜杠隔离、头叠加、DNS 与 reload、SNI）都有对应断言与反向验证兜住；
- 成立的前提是第一阶段口径不被顺手放宽：**单租户试点 + 公开读桶 + `public_base_url` 留空**；
- 一旦要开客户自有域名 / 自有 CDN，**R1-b 必须先解决**，否则内容安全链在最贵的一步静默断掉。

因此可以进入「严格裸键」阶段（库里只写 `t/<cid>/x`），但顺序不能反：

1. **先落 snippet 生成器**（含 DNS 预检、配置表↔snippet 对账）；
2. **再切出参侧**：`ResolveRef` 对裸键补 `<origin>/uploads/` 前缀（`/uploads/` 与绝对 URL 两种历史形态保持原样）；
3. **最后切写入侧**：`NormalizeRef` 收敛成裸键。**切换开关必须可回滚**（一个 env 就能退回「存驱动 URL」），
   因为这一步一旦写入，库中数据形态就变了；
4. 存量回填（P3）与迁移 0044 仍在其后，且回填只改能确定归属的行，不确定的留着走三形态兼容。

第 1 步不做就切裸键的后果：库里出现 `t/<cid>/x` 而 nginx 还没有对应 location →
全部落回平台 Go → 文件不在平台盘 → **404，页面全裂，且平台侧没有任何报错**。

> ✅ **第 1 步已落地（同一日，紧接本轮）**：`backend/internal/nginxsnippet`（渲染 + DNS 预检 +
> 对账，纯函数，14 个单测）+ `backend/cmd/nginx-snippets`（`render` / `check` 两个子命令）。
> 装置改为直接跑这个生成器：断言从 36 项扩到 **39 项**（F 组改成双向对账 + G 组加了
> 「生成器自己挡住不可解析域名」那条），全绿。**仍未接库**——输入是 JSON 文件，
> 等迁移 0044 建表后再换成表读取，渲染/预检/对账三层不用动。

---

## 6. 本轮边界

- **新增** `tools/nginx-r1-check/`（验证装置）与本文件；生产代码零改动；
- 未改 `deploy/baota/nginx-site.live.conf`、未 reload 线上 nginx、未 `nginx -t` 线上配置；
- 未写迁移、未改库、未碰线上二进制；
- 装置的产物（`snippets/`、`cert/`、`logs/`、`www/`）全部 gitignore，不进仓库；
- 反向验证三组：删 `proxy_hide_header`（C1/C2/C6/D8 报红 4 项）、
  rewrite 剥错前缀（A1/A2/A5/C2/C5 报红）、location 去掉尾斜杠（B1 + 对账 F1/F4/F7 报红）；
  还原后全绿、生成器源码字节零漂移。
