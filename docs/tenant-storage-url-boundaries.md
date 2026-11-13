# 多租户自有存储 · P0b 业务 URL 边界清单

> **状态：清单 + 源码级守门已落地；仍未接入任何 `NormalizeRef` / `ResolveRef`。**
> 本轮没有改任何生产代码路径、没有写迁移、没有动线上 nginx、没有回填存量数据。
> 所有行号来自 2026-11-13 对仓库的实读。守门在
> `backend/internal/storage/ref_boundaries_test.go`。
>
> 关联：`docs/tenant-storage-design.md` §3.1 / §4、`docs/commercial-readiness.md` 第五十二轮补记。

---

## 0. 为什么先要一份清单

P0a 已经把纯函数写好了，但真正接入时最容易出事的**不是函数写错，是漏点**。
漏一个的表现是「同一张图在 A 页面是新的 `t/<cid>/...`、B 页面还是旧绝对 URL」，
或者更糟：某个后台表单把绝对 URL 直接写回库，把去 URL 化的成果冲掉，
而页面全 200、没有任何报错——又是「没人报错时用户无从察觉」。

**本轮最重要的发现**：P1 加的 60 秒边缘缓存只覆盖 **5 条**公开只读接口
（`cdnCache(c, 60)` 在 `handler/handler.go` 出现 5 次），
但 `handler.go` 里下发资源 URL 的公开 GET 有 **9 条**。
若照着「那五条缓存接口」去接 `ResolveRef`，会直接漏掉 4 条：

| 漏掉的接口 | 下发的资源字段 | 证据 |
| --- | --- | --- |
| `/api/company` | `logo` | `handler.go:144-147` |
| `/api/team` | `avatar`（白名单字段手工组装） | `handler.go:192`、`handler.go:206` 注释明确「刻意不设 Cache-Control」 |
| `/api/projects/:id` | `cover`、`album`（JSON 数组） | `handler.go:278-293` |
| `/api/services/:id` | 内嵌整个 `model.BizService`（含 `icon`） | `handler.go:326-329` |

所以设计稿 §3.1「`Resolve` 必须覆盖五条缓存接口」那句**不够**，
正确口径是：**所有出参序列化点**都要过，缓存只是其中一层的时序约束。

---

## 1. 写入侧：URL 从哪里进入系统

后端真正调用 `Uploader` 写对象并拿到 URL 的，只有 4 个文件（守门精确对账文件集合）：

| # | 位置 | 标识符 | 归属（将来 `Scope`） | 为什么是边界 |
| --- | --- | --- | --- | --- |
| W1 | `handler/upload.go:94` | `up.Save(fh)` | `ScopeTenant`（或平台超管 `cid=0` → `ScopePlatform`） | 上传主入口；`upload.go:105` 紧接着 `submitMediaCheck(db, cfg, c, u)` |
| W2 | `handler/assets.go:156` | `up.SaveBytes(rel, data, assets.ContentType(item.File))` | **固定 `ScopePlatform`** | 素材库采用，`rel = library/<id>.<ext>`；不能被 JWT `companyId` 带跑 |
| W3 | `handler/wechat.go:255` | `up.SaveBytes(key, b, "image/jpeg")` | **固定 `ScopePlatform`** | 小程序码，`wxacode/<staffID>.jpg`；`wechat.go:260` 把 `publicURL` 放进内存缓存 |
| W4 | `handler/media_check.go:460` | `up.SaveBytes(row.ObjectKey, data, contentType)` | **按 `row.CompanyID`** | 违规下架覆盖占位图；现在不带租户，多租户下会写错桶 |

另外两类不产生对象、但会把 URL 送进业务字段：

| # | 位置 | 标识符 | 说明 |
| --- | --- | --- | --- |
| W5 | `handler/storage.go:83` | `gin.H{"putUrl": putURL, "url": finalURL, "key": key}` | 直传预签名返回的 `finalURL`，端上拿到后随表单提交入库 |
| W6 | `handler/media_check.go:86` | `func submitMediaCheck(...)` → `:135-136` `URL: d.URL` / `ObjectKey: d.Key` | 送检时把绝对 URL 与反推的对象键各存一份 |

前端把 URL 交回业务表单的 3 个点：

| # | 位置 | 标识符 |
| --- | --- | --- |
| W7 | `frontend/src/api/uploadFile.ts:91` | `return p.data.url`（直传成功后的 finalURL） |
| W8 | `frontend/src/components/UploadField.vue:249` | `(res.data.urls \|\| [])[0]`（multipart 回退） |
| W9 | `frontend/src/components/AssetPicker.vue:239-258` | `emit('picked', resp.data.url)` / `urls.push(resp.data.url)`（素材采用） |

> **接入顺序建议**：先 W1–W9 全部收敛成 `NormalizeRef` 产出的裸键，
> 再动出参侧。反过来做会有一段窗口期，库里同时存在键与旧 URL，
> 而出参侧还没兼容完 → 页面上直接裂图。

---

## 2. 库里哪些字段存的是资源引用

`backend/internal/model/models.go` 与 `model/media_check.go` 的**资源型 json 字段**
（守门精确对账这个集合，新增同类字段必红）：

| json 字段 | 模型 / 证据 | 备注 |
| --- | --- | --- |
| `avatar` | `models.go:17`（Employee）、`:341`（OpenApply） | 员工头像 / 申请表头像 |
| `logo` | `models.go:76` | 企业 LOGO |
| `videoUrl` | `models.go:138` | 主视频；五档来源之一 |
| `videoBackupUrl` | `models.go:144` | 备用 MP4 |
| `cover` | `models.go:155`（CompanyProfile）、`:197`（ProjectCase） | 封面 |
| `icon` | `models.go:183`（Category）、`:216`（BizService） | 图标 |
| `bgImage` | `models.go:267` | 首页背景 |
| `url` | `media_check.go:27` | 送检时的绝对地址 |

**两个隐藏边界**（守门单独钉住，最容易漏）：

- `ProjectCase.Album`（`models.go:198`）是 `json:"-"`、列 `column:album;type:json`，
  **存的是 URL 数组字符串**，靠 `handler/content.go:745-746` 的 `projectToMap`
  与 `handler.go:275-284` 的 `json.Unmarshal` 展开成 `album` 数组出参。
  它不在 json 字段集合里，按「扫 json tag」找边界必然漏掉。
- `MediaCheck.ObjectKey`（`media_check.go:31`，`json:"objectKey"`）是从 URL 反推的裸键；
  P0 之后 `mediachk.ObjectKey`（`mediachk.go:159`）那段反推逻辑应退休。

**同名但不是 URL、不要误接**的 4 个字段（守门白名单，逐条写明理由）：

| json 字段 | 为什么不算 |
| --- | --- |
| `videoFallbackUrl` | `bool` 开关（`models.go:139`），名字带 Url 但不是地址 |
| `bgMask` | `int` 蒙层浓度（`models.go:270`） |
| `bgId` | 内置背景 id（`models.go:446`），值是 `poster-*` / `c-*` 标识 |
| `files` | 存储用量累计文件数（`model/storage_usage.go:26`），`int64` |

---

## 3. 出参侧：URL 从哪里离开系统

### 3.1 公开只读接口（`handler/handler.go`，守门精确对账 9 条路由）

| 路由 | 下发内容 | 60s 边缘缓存 |
| --- | --- | --- |
| `/api/card` | `employee` / `company` / `profile` 三个模型整体 | ✅ `:126` |
| `/api/company` | `"logo": ci.Logo` | ❌ |
| `/api/team` | 白名单 `"avatar": e.Avatar` | ❌ `:206` 刻意不设 |
| `/api/achievements` | `categories` / `featured` 模型数组（含 `icon` / `cover`） | ✅ `:240` |
| `/api/projects` | `data: ps` 模型数组 | ✅ `:258` |
| `/api/projects/:id` | 显式 `gin.H` 的 `cover` + `album` 数组 | ❌ |
| `/api/services` | `data: s` 模型数组 | ✅ `:308` |
| `/api/services/:id` | 内嵌 `model.BizService` + `timeMode` | ❌ |
| `/api/home-layout` | 根级 `bgImage` | ✅ `:361` |

**时序约束**：带缓存的 5 条，`ResolveRef` 必须在 `cdnCache(c, 60)` **之前**完成，
响应里是统一入口 URL；错误分支仍不许带缓存头（`public_list_test.go` 已钉）。
将来开放客户自有域名时 cache key 必须含 `Host`（设计稿 §3.8）。

### 3.2 其它出参出口

| # | 位置 | 标识符 | 说明 |
| --- | --- | --- | --- |
| R10 | `handler/wechat.go:238,271` | `gin.H{"url": url}` | 小程序码；私有桶走 `resolveWxacodeURL` 限时签名 |
| R11 | `handler/content.go:70,105,114,121,220` | `"data": ci` / `"data": cp` | 后台企业信息 / 公司简介，模型直接序列化 |
| R12 | `handler/content.go:331,360` | `"data": projectToMap(p)` | 后台案例详情 / 编辑，`album` 在这里展开 |
| R13 | `handler/content.go:227,380` | `"data": list` | 后台列表，模型数组直接序列化 |
| R14 | `handler/apply.go:837` | `"data": e` | 申请表 / 员工模型（含 `avatar`） |
| R15 | `handler/settings.go:73` / `handler/wechat_admin.go:91` | `"data": items` / `rows` | 后台列表类，形态各异 |

> R11–R15 说明一件事：**出参侧不该逐点改 handler**。模型直接序列化是主流，
> 逐个 `gin.H` 补 `ResolveRef` 一定会漏，而且每新增一个后台接口就多一处漏点。
> 真正接线时应在**统一序列化出口**（model 的 marshal hook 或在 repository 层
> 统一投影）做一次，再用本清单逐点验收。这个决策留到 P0b 之后的实现轮拍板，
> 本轮只把边界钉死。

### 3.3 端上消费契约（改 URL 形态必须两端对账）

| # | 位置 | 契约 |
| --- | --- | --- |
| C1 | `miniprogram/utils/request.ts:31-35` | `assetUrl`：绝对地址原样返回，相对路径拼 `baseUrl()` |
| C2 | `frontend/src/utils/thumb.ts` | `thumbnailURL`：URL 末位扩展名前插 `_thumb` |
| C3 | `frontend/src/components/ImageThumb.vue` | 管理端缩略图组件 |

小程序里调用 `assetUrl(` 的文件共 **13 个**（含定义处与一个测试），
守门精确对账这个集合：新增页面用到资源 URL 就要重新确认契约。
`ref_test.go` 的 `TestResolveRefPreservesThumbnailContract` 已验证
`ResolveRef` 输出仍是普通 URL 形态、`_thumb` 派生后仍落在同一租户前缀下。

---

## 4. 源码级守门守的是什么

`backend/internal/storage/ref_boundaries_test.go` 五组断言：

| 断言 | 防的是什么 |
| --- | --- |
| `TestP0bBoundaryTokensStillExist` | 清单里每个文件 + 标识符必须真实存在；代码挪走 / 改名后清单不许腐 |
| `TestP0bUploaderWriteSitesMatchBoundaryList` | `handler/*.go` 中含 `up.Save(` / `up.SaveBytes(` 的**文件集合**必须等于 W1–W4；新增写入点必红 |
| `TestP0bPublicGetRoutesMatchBoundaryList` | `handler.go` 的公开 GET 路由集合必须等于 §3.1 的 9 条；`cdnCache(c, 60)` 必须恰好 5 处；新增公开接口必红 |
| `TestP0bResourceJSONFieldsMatchBoundaryList` | model 包里所有「名字含 url/image/logo/cover/avatar/icon/bg/file/album」的 json 字段集合必须等于 §2 的 8 个 + 4 个白名单；新增资源字段必红。另单独钉住 `album` / `object_key` 两个隐藏边界 |
| `TestP0bMiniProgramAssetUrlConsumersMatchBoundaryList` | 小程序里含 `assetUrl(` 的 `.ts` 文件集合必须等于 13 个；端上新增消费点必红 |

这些断言**不依赖运行时行为**，纯源码对账，因此和 P0a 的
`TestP0aRefIsNotWiredIntoProduction` 一样，在真正接线时会变红——
那是要求「按清单逐点接入、逐点把断言改成已接入」的信号，不是让人删断言。

---

## 5. 本轮边界

- 只新增清单文档 + 守门测试 + P0a 纯函数；**生产代码路径零改动**；
- 没有写迁移、没有动 `deploy/baota/nginx-site.live.conf`、没有回填存量数据；
- 没有跑 `./server migrate`、没有碰线上二进制；
- 反向验证：分别注入「新增上传写入点」「新增公开路由」「新增资源 json 字段」，
  三组守门必红；还原后哈希零漂移、全绿（见决策记录补记）。
