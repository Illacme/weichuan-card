# 多租户自有存储 · P0b 业务 URL 边界清单

> **状态：清单 + 源码级守门已落地；出参侧（9 条公开 GET + signHandler + R11–R15 后台出口）
> 已全部接入；写入侧（W1 上传 / W2 素材 / W3 小程序码 / W5 预签名）已接入带归属前缀的对象键
> （`t/<cid>/...` / `p/...`；本地驱动仍存驱动返回的相对 URL，严格裸键 + nginx R1
> 属云驱动阶段）。**
> 所有行号来自 2026-11-13 对仓库的实读。守门在
> `backend/internal/storage/ref_boundaries_test.go`。
>
> 关联：`docs/tenant-storage-design.md` §3.1 / §4、`docs/commercial-readiness.md` 第五十三／五十七轮。

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

> **接入顺序（已订正，2026-11-13 实装）**：必须先让出参侧兼容三形态，再切写入侧收敛成 `NormalizeRef` 产出的裸键。
> 旧版「先写入侧再出参侧」顺序是**错的**——线上双轨（存量老图走本地盘 `/uploads/`、新上传走对象存储绝对 URL），
> 先写键会有一段窗口期，库里同时存在裸键与旧 URL，而出参侧还没兼容完 → 页面直接裂图。
> 正确顺序的依据：出参侧用 `ResolveRef` 兼容三形态（云端绝对 URL 原样、本地 `/uploads/` 原样、
> 将来裸键/`p/`/`t/` 前缀自动补回 `/uploads/`）对**现行数据零行为变更**，因此先接出参侧是安全的。
>
> **现状（2026-11-13）**：出参侧已在第五十三轮接好（见 §5.2）；写入侧在第五十四轮接入
> 带归属前缀的对象键（W1/W2/W3/W5，见 §5.3）——对象真正落到 `t/<cid>/...` 或 `p/...`
> 前缀下，但本地驱动仍把驱动返回的相对 URL（`/uploads/<key>`）写库，端上契约零改动。
> 严格裸键（只写 `t/<cid>/x`）+ nginx `/uploads/`→桶 反代（R1）属云驱动阶段，尚未做。
> 出参侧口径集中在 `backend/internal/model/refs.go`（`ResolveResourceRef` + 六个模型的 `ResolveRefs()`），
> 经 `storage.ResolveRef(raw, "")` 走通，对现行数据零行为变更。

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
  **存的是 URL 数组字符串**，靠 `handler/content.go:767` 的 `projectToMap`
  与 `handler.go:291-297` 的 `json.Unmarshal` 展开成 `album` 数组出参。
  它不在 json 字段集合里，按「扫 json tag」找边界必然漏掉。
  模型方法解不开 JSON 字符串，所以**只能在两处展开点手工调 `model.ResolveResourceRefs`**
  （`content.go:776`、`handler.go:297`）——守门 `TestAlbumExpansionSitesResolved`
  钉住「展开点恰好 2 处，且这两处必须调 `model.ResolveResourceRefs`」：
  数量变了就报红，逼人重新确认「新增的展开点接了没有」，而不是等线上裂图。
- `MediaCheck.ObjectKey`（`media_check.go:31`，`json:"objectKey"`）是从 URL 反推的裸键；
  P0 之后 `mediachk.ObjectKey`（`mediachk.go:159`）那段反推逻辑应退休。

**同名但不是 URL、不要误接**的 5 个字段（守门白名单，逐条写明理由）：

| json 字段 | 为什么不算 |
| --- | --- |
| `videoFallbackUrl` | `bool` 开关（`models.go:139`），名字带 Url 但不是地址 |
| `bgMask` | `int` 蒙层浓度（`models.go:270`） |
| `bgId` | 内置背景 id（`models.go:446`），值是 `poster-*` / `c-*` 标识 |
| `files` | 存储用量累计文件数（`model/storage_usage.go:26`），`int64` |
| `publicBaseUrl` | **确实是 URL，但角色相反**：租户自有访问域名（`model/tenant_storage.go`，列 `public_base_url`）。它是 `ResolveRef(raw, origin)` 里那个 **origin 的候选值**——解析器的**输入**，不是输出。对它调 `ResolveRef` 只会得到荒谬结果（`https://img.c7.com` 会被当成「存量绝对 URL」原样返回，或更糟：被当相对路径拼上 `/uploads/`）。它的出参处理是**原样回显**给平台超管的配置界面。第一阶段该字段恒为空（客户自有域名未启用），所以连回显都还没有 |

> `publicBaseUrl` 是这五条里唯一「长得像资源字段、语义却是解析器输入」的一条。
> 将来若真有人把它接进 `ResolveRefs`，症状不是裂图，而是**配置页显示的基址被悄悄改形**，
> 而且只在客户启用自有域名后才暴露——所以它进白名单时单独写了理由，不留「同类合并」的余地。

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

> **接入状态（2026-11-13 实装）**：上表 9 条路由**已全部接入** `model/refs.go` 的出参侧解析
> （整模型路由调 `ResolveRefs()`、内联 `gin.H` 路由调 `model.ResolveResourceRef`），
> 对现行生产数据零行为变更。下一阶段写键后，这里无需再动——`ResolveRef` 会自动补前缀。

### 3.2 其它出参出口

| # | 位置 | 标识符 | 接入情况 |
| --- | --- | --- | --- |
| R10 | `handler/wechat.go:238,271` | `gin.H{"url": url}` | 小程序码；私有桶走 `resolveWxacodeURL` 限时签名（第五十三轮已接） |
| R11 | `handler/content.go:72,111,129,230` | `"data": ci` / `"data": cp` | 后台企业信息 / 公司简介 GET + PUT，**第五十七轮已接**（GET 就地解析、PUT 对副本解析） |
| R12 | `handler/content.go:347,376` → `:775-776` | `"data": projectToMap(p)` | 后台案例详情 / 编辑，**第五十七轮已接**：`p.ResolveRefs()` + `album = model.ResolveResourceRefs(album)` |
| R13 | `handler/content.go:239,397`（列表）/ `:255,418`（POST 回显） | `"data": list` / `"data": resp` | 后台分类 / 服务，**第五十七轮已接**（`for i := range list { list[i].ResolveRefs() }`） |
| R14 | `handler/apply.go:109`（`/api/employee/me`）、`:842`（员工导出） | `"data": e` | 员工模型（含 `avatar`），**第五十七轮已接** |
| R15 | `handler/settings.go:73` / `handler/wechat_admin.go:91` | `"data": items` / `rows` | **核查后判定无需接**（见下） |

**R15 为什么不需要接**（这属于判断，不是漏点，所以写清依据）：

- `settings.go:73` 出的是 `setting.All()`，即 `setting/setting.go` 的配置项元数据
  （`miniprogram_base_url` / `upload_base_url` / `upload_video_max_mb` 等）。这些是
  **基址与阈值**，不是某张图的引用；`setting.go` 里也**没有任何** logo/image/avatar 类键
  （已 grep 确认零命中）。将来若要加「品牌 LOGO 走设置项」，那一刻它才变成资源字段，
  必须同步 §2 与 `model/refs.go`。
- `wechat_admin.go:91` 出的是 `buildBindingRows` 产出的 `wechatBindingRow`
  （`Kind`/`ID`/`Name`/`CompanyID`/`CompanyName`/`WechatBound`/`WXOpenidMasked`/`CreatedAt`），
  `wechat_admin.go:191-228` 逐字段看过，**不含任何资源字段**。刻意不塞 `avatar` 也是隐私口径：
  这个接口是「按 openid 反查绑定关系」的运维入口，回传越多越像信息泄露面。

> **原方案已作废**：本清单初版建议「在统一序列化出口（model marshal hook 或 repository 层）
> 一次做掉，不要逐点改 handler」。实装时放弃了这个方案，理由有两条：
> 一是 `gin.H` 内联出参（`/api/company`、`/api/team`、`/api/projects/:id`、`/api/home-layout`、
> `projectToMap`）根本不经过模型序列化，marshal hook 覆盖不到，会留下一半接了一半没接的裂缝；
> 二是 marshal hook 意味着**任何**序列化都自动改写，包括将来可能出现的「原样回吐给写接口」
> 路径——那正是把 `/uploads/...` 写回库的入口。
> 逐点改的代价是「新增接口要记得接」，对策是把清单和守门当作验收关卡（§4），
> 并且用 `TestAlbumExpansionSitesResolved` 钉住「album 展开点必须调解析」，
> 让漏点变成红测试而不是线上裂图。

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
`TestRefWiringGate`（在 `ref_p0a_guard_test.go`，原 `TestP0aRefIsNotWiredIntoProduction`）一样，在真正接线时会变红——
那是要求「按清单逐点接入、逐点把断言改成已接入」的信号，不是让人删断言。
实装轮已把该守门升级为三集合对账（见 §5.2），出参侧接入后由它兜底「写入侧 API 不出 storage 包」。

第五十七轮在同文件（`ref_p0a_guard_test.go`）新增 `TestAlbumExpansionSitesResolved`：
盯 `album` 的两处展开点数量与解析调用。它守的是**清单之外的漏点**——
`album` 不在 json 字段集合里，`TestP0bResourceJSONFieldsMatchBoundaryList` 看不见它，
只能靠「展开点必须带解析」这一层拦住。

---

## 5. 本轮边界

本清单横跨两轮，边界口径要分开记：

### 5.1 清单轮（初始，P0a 纯函数 + P0b 边界清单，零行为变更）

- 只新增清单文档 + 守门测试 + P0a 纯函数（`storage/ref.go`）；**生产代码路径零改动**；
- 没有写迁移、没有动 `deploy/baota/nginx-site.live.conf`、没有回填存量数据；
- 没有跑 `./server migrate`、没有碰线上二进制；
- 反向验证：分别注入「新增上传写入点」「新增公开路由」「新增资源 json 字段」，
  三组守门必红；还原后哈希零漂移、全绿（见决策记录补记）。

### 5.2 实装轮（2026-11-13 / 第五十三轮，只接出参侧）

**核心订正**：原清单「先写入侧再出参侧」顺序是错的——线上双轨（存量老图走本地盘 `/uploads/`、
新上传走对象存储绝对 URL），先写键会裂图。本轮先接出参侧、写入侧（W1–W9 / `NormalizeRef`）留到下一阶段。

本轮实际改动的生产代码路径：

- **新增 `backend/internal/model/refs.go`**（出参侧解析口径集中地，`storage` 反向依赖 `model` 合法，
  因 `storage` 不依赖 `model`）：
  - `ResolveResourceRef(raw)`：空值返回空、解析失败返回原值（不静默降级）、经 `storage.ResolveRef(raw, "")` 走通；
  - 六个模型的 `ResolveRefs()`：`Employee.Avatar` / `CompanyInfo.Logo` /
    `CompanyProfile.Cover,VideoURL,VideoBackupURL` / `AchievementCategory.Icon` /
    `ProjectCase.Cover` / `BizService.Icon`；字段集合严格对齐 §2。
- **`handler/handler.go` 接入 9 条公开 GET**：
  - 整模型序列化路由（`/api/card`、`/api/achievements`、`/api/projects`、`/api/services`、
    `/api/services/:id`）在 `cdnCache(c, 60)` **之前**调 `ResolveRefs()`；
  - 内联 `gin.H` 路由（`/api/company`、`/api/team`、`/api/projects/:id`、`/api/home-layout`）
    直接调 `model.ResolveResourceRef(...)`（见 `handler.go:152,198,220,298,304,393`）。
- **`handler/storage.go` 的 `signHandler` 修正**：用 `storage.ObjectKeyFromURL` 复用 `legacyRel`
  （先判 `/uploads/` 再判基址），修复 `UploadBaseURL` 为空时把 `/uploads/x.jpg` 错截成
  `uploads/x.jpg` 的旧 bug（`ref_wiring_test.go` 已钉空 base 场景）。
- **守门升级**（`ref_p0a_guard_test.go` 的 `TestRefWiringGate`，原 `TestP0aRefIsNotWiredIntoProduction`）：
  三集合对账——写入侧 API（`NormalizeRef`/`ObjectKeyFor`/`ParseRef`/`IsNormalizedRef`）一律不得出 `storage` 包；
  `storage.ResolveRef` 只允许在 `model/refs.go`；`model.ResolveResourceRef`/`ResolveRefs()` 只允许在
  sanctioned 出参侧文件（`model/refs.go`、`model/refs_test.go`、`handler/handler.go`、`handler/storage.go`、
  `handler/ref_wiring_test.go` 各自）。新增 `ref_objectkey_test.go`、`ref_wiring_test.go`、`model/refs_test.go`。

**行为保证**：对现行生产数据**零行为变更**（云绝对 URL 原样、本地 `/uploads/` 原样、空值空串、
不安全引用返回原值）；哈希零漂移、全绿；反向验证注入 `storage.NormalizeRef` 真实调用 → 守门正确报红、
定位到 `handler/storage.go`，还原后复绿。

**仍未做**：写入侧（W1–W9 / `NormalizeRef` 收敛成裸键）、存量回填、迁移、碰线上二进制——下一阶段。

### 5.3 写入侧实装（2026-11-13 / 第五十四轮，本地驱动安全版）

**核心取舍**：本轮只让**对象真正落到带归属前缀的键**（`t/<cid>/...` / `p/...`），
而**库里仍写驱动返回的 URL**。本地驱动下该 URL 本就是相对路径 `/uploads/<key>`
（与第五十三轮前的 `/uploads/<name>` 同形态），所以端上契约、前端、出参侧**零改动**，
`ResolveRef` 对 `/uploads/...` 原样保留（no-op）。**严格裸键**（库里只写 `t/<cid>/x`）
+ nginx `/uploads/`→桶 反代（R1）留到云驱动阶段——那时才需要把 `<origin>/uploads/<key>`
反代到对象桶，否则会 404。

**实际改动（生产代码路径）**：

| 落点 | 改动 |
| --- | --- |
| **W1** `handler/upload.go` | `Uploader.Save` 接口由 `Save(fh)` 改为 `Save(fh, key)`（Local/MinIO/OSS 三实现同步）；上传循环先 `ObjectKeyFor(scope, cid, RandName(ext))` 生成带前缀键再 `up.Save(fh, objKey)`。平台超管（cid=0）落 `p/`，员工/租户落 `t/<cid>/` |
| **W2** `handler/assets.go` | 素材采用 rel 经 `ObjectKeyFor(ScopePlatform, 0, rel)` 落 `p/library/...` |
| **W3** `handler/wechat.go` | 小程序码 key 经 `ObjectKeyFor(ScopePlatform, 0, rel)` 落 `p/wxacode/...` |
| **W5** `handler/storage.go` | 预签名签的是带前缀键（`ObjectKeyFor(scope, cid, name)`），客户端直传落桶即落该前缀；返回的 `url`（finalURL）形态不变 |
| **W4/W6** `handler/media_check.go` | **无需改**：`submitMediaCheck` 经 `mediachk.Plan` 已把 URL 反推成裸键（`d.Key`），下架占位图直接复用 `row.ObjectKey`（已带前缀）。单桶阶段写同一键即可，P2 多桶时才需按 `row.CompanyID` 选实例 |
| **W7–W9** 前端 | **无需改**：后端返回的仍是驱动 URL（本地 `/uploads/<key>`），前端存/预览逻辑不变 |

**接口变更（`storage.Uploader`）**：`Save(fh *multipart.FileHeader, key string) (string, error)`。
`key` 由调用方经 `storage.ObjectKeyFor` 生成；`Save` 内部新增 `validateObjectKey` 防御
（拒绝空值 / `..` 路径穿越 / 绝对路径 / 反斜杠，防止 `filepath.Join(Dir, "../x")` 写出 Dir 之外）。

**守门升级**：`TestRefWiringGate` 的 sanctioned 集合新增 `upload.go` / `assets.go` / `wechat.go`，
并修正 `writeAPI` 分支漏了的 `&& !sanctioned[p]`（此前它无条件封禁写入侧 API，接线后必须放行
sanctioned 文件）。边界 token 更新 W1/W2 的标识符并新增 4 条 `storage.ObjectKeyFor(...)` 锚点。

**反向验证**：临时在非 sanctioned 文件（`handler/zzz_ref_probe_test.go`）调用 `storage.ObjectKeyFor`，
`TestRefWiringGate` 正确报红并定位到该行；删除探针后复绿。新增 `storage/save_key_test.go`
锁住「对象落在带前缀键下 + 非法键拒写」。

**门禁**：`gofmt` 干净、`go vet ./...` 通过、`go test ./...` 全包绿（含 handler 58s、storage 9s）。
`golangci-lint` 本沙箱无二进制（上一轮已 lint 绿，本轮只增 Go 源码，未重跑）。
前端零改动 → 未跑 vue-tsc / Vitest / eslint / vite build。

**仍未做**：严格裸键（库里只写 `t/<cid>/x`）、nginx R1（`/uploads/`→桶 反代）、
`NormalizeRef` 收敛、存量回填、迁移 0044、云驱动多桶切换——下一阶段。

### 5.4 后台出参收口（2026-11-13 / 第五十七轮，对现行数据零行为变更）

第五十三轮只接了**公开**那 9 条 + `signHandler`，R11–R15 这五组后台出口一直挂着「未接」。
本轮补齐，理由不是「清单要求」，而是一个具体的失效场景：

> **后台裂图比公开页裂图难被发现。** 公开页坏了立刻有人报；后台是运营一个人看到编辑页
> 预览破图，第一反应通常是「前端 bug」，而此时公开页正常，排查方向直接被带偏。

写入侧还没收敛成裸键，所以这一步对现行数据是 no-op（库里是 `/uploads/...` 或云绝对 URL，
`ResolveRef` 原样返回）。它接的是**将来**：写入侧一旦收敛成 `t/<cid>/x`，
后台与公开页同时认，不会出现「一半好一半坏」。

**实际改动**：

| 落点 | 改动 |
| --- | --- |
| `model/refs.go` | 新增 `ResolveResourceRefs([]string) []string`（nil/空原样返回，避免 `null`→`[]` 的契约变化）；重写注释，把覆盖范围明确为「所有出参」并写入落地纪律 |
| `handler/content.go` | GET company-info / company-profile 就地 `ResolveRefs()`；PUT 两者用 `resp := x; resp.ResolveRefs()`；categories/services 的 GET 走 `for i := range list`、POST 回显用副本；`projectToMap` 加 `p.ResolveRefs()` + `album = model.ResolveResourceRefs(album)` |
| `handler/apply.go` | `/api/employee/me`（`:109`）与员工导出（`:842`）各加 `e.ResolveRefs()` |
| `handler/handler.go` | `album` 的 for 循环改为 `model.ResolveResourceRefs(album)`（与 `content.go` 同一实现，两份口径合成一处） |
| `ref_p0a_guard_test.go` | sanctioned 新增 `content.go` / `apply.go`；新增 `TestAlbumExpansionSitesResolved` |

**落地纪律（本轮最重要的结论，已写进 `model/refs.go` 注释）**：
**只解析「本次请求不会再写库」的值。**

- GET：查询结果是局部变量，就地解析；
- PUT/POST：先落库，再对**副本**解析后出参（`resp := x; resp.ResolveRefs()`）。

反过来（先解析再 Save）会把 `/uploads/t/5/x.jpg` 写回库，形态从裸键退回历史形态，
**而且没有任何报错**——`upload_base_url` 改动影响存量的老问题就是这么复发的。
这条靠测试钉住：`TestAdminExitsResolveBareKeys` / `TestEmployeeMeResolvesAvatar`
在断言出参的同时，直接回查 DB 断言仍是裸键。

**新增测试**（`handler/refs_exit_test.go`，三条）：

| 测试 | 钉住什么 |
| --- | --- |
| `TestAdminExitsResolveBareKeys` | 后台 company-info 出参把 `t/1/logo.png` 补成 `/uploads/t/1/logo.png`；且 DB 未被污染 |
| `TestAdminProjectExitResolvesCoverAndAlbum` | cover 走模型方法、album 走展开点手工解析；三元素分别验证「裸键补前缀 / 存量绝对 URL 原样 / 空保持空」 |
| `TestEmployeeMeResolvesAvatar` | `/api/employee/me` 的 avatar；DB 未被污染 |

**反向验证（逐点，必须做）**：三处解析分别注掉，三条测试各自报红且报在**自己那一行**——
`refs_exit_test.go:33`（logo）、`:64`（cover）、`:86`（avatar）；
再单独只注掉 album 那一行，报在 `:67`（`t/1/a1.jpg` vs `/uploads/t/1/a1.jpg`）。
四处还原后哈希零漂移、三测复绿。

**门禁**：`gofmt -l` 本轮改动文件干净（仓内另有 5 个既有文件因结构体字段对齐而长期在列，
非本轮引入，要统一应单独一轮做格式化基线）、`go vet ./...` 通过、
`go test ./...` 全包绿（handler 50s、storage 6s）。前端零改动 → 未跑 vue-tsc / Vitest / eslint / build。

**仍未做**：严格裸键、`NormalizeRef` 收敛（写入侧）、存量回填、迁移 0044、
生成器接真实配置表（`tenant_storage_configs` → snippet）。
