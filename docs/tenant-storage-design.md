# 多租户自有存储 · 详细设计（第一阶段）

> **状态：部分实施。** 本文原为设计稿（2026-11-13 首版只做代码事实核查），
> 后续多轮已按 §4 分期推进，当前进度：
>
> | 已落地 | 轮次 / 落点 |
> | --- | --- |
> | 出参侧资源引用解析（9 条公开 GET + signHandler + 后台 R11–R15） | 第五十三、五十七轮 |
> | R1 按租户前缀反代可行性验证（36 项断言） | 第五十五轮，`tools/nginx-check/` |
> | nginx snippet 生成器（`nginxsnippet` + `cmd/nginx-snippets`） | 第五十六轮 |
> | 写入侧落带前缀裸键（W1–W7） | 第五十六轮 |
> | 迁移 0044 建 `tenant_storage_configs` + 模型进 `model.All()` | 第五十八轮 |
> | 生成器接真实配置表（`--dsn`，含对账同一来源） | 第五十八轮 |
>
> | **尚未落地**（按既定顺序，不可颠倒） | 影响 |
> | --- | --- |
> | 配置写接口 + 后台界面（掩码 / 自检 / 审计，仅平台超管） | 目前表能读不能写：生产上只能手工 SQL 插入 |
> | 写入侧 `NormalizeRef` 收敛（③，必须一个 env 能回滚） | 落库形态仍是三种混存 |
> | `UploaderResolver` + 5 处注入（P2） | 上传仍走平台那一套存储，`--dsn` 生成的 snippet 暂无实际流量 |
> | 存量回填 P3 | 老数据仍是绝对 URL / `/uploads/` 形态 |
>
> 文中所有文件路径、标识符与行号都来自对仓库的实读，不引用印象。
>
> 关联决策：`docs/commercial-readiness.md` 第五十二轮。方向由用户明确选定为
> **多租户各自用自己的存储（数据主权 / 产品能力）**，架构分叉由用户明确选定为
> **平台域名统一入口 + 按租户回源，客户自有域名留作未来可选开关**，
> 权限模型由用户明确拍板为 **只有平台超管代填，企业管理员不给这条权限**（见 §1.1）。

---

## 0. 解决什么，不解决什么

**解决**：让每个企业能把图片 / 视频物理存到自己的对象存储账号里，凭据由平台安全保管，
配置错只影响本租户，随时能切回平台默认、存量能搬回来。

**验收判据不看首屏毫秒数**，改看四件事：

| 判据 | 含义 |
| --- | --- |
| 可移植性 | 新上传对象真的落在指定桶；库里不再写死某个域名的完整 URL |
| 租户隔离 | A 租户配置错不影响 B；对象键不互串；平台本地盘不接租户文件 |
| 凭据安全 | 密钥不回显、不进审计、加密落库；密钥策略在写第一条配置前定死 |
| 迁移可逆 | 搬过去能搬回来，且搬迁过程有 dry-run、有记录、有 rollback |

**明确不纳入第一阶段**：

- 性能优化（多域名分流、按文件类型 / 图片子集选平台、同步到多个 CDN）；
- 客户自有域名（端上域名一变，小程序合法域名就要逐个登记）；
- 私有桶限时签名接入统一入口（第一阶段按公开读桶试点，见 R1）；
- 小程序包内 `.js/.wxml/.wxss/.json` 与管理后台 `/assets/`、`/theme-previews/`
  —— 前者必须随包、不允许远程加载，后者是 Vite/nginx 本地产物，都不在
  `storage.Uploader` 抽象范围内。

---

## 1. 已确认的架构决策

```
端上（小程序 / 浏览器）
    │  https://weichuan.ziyuanxingqiu.cn/uploads/t/<cid>/<name>
    ▼
平台统一入口（nginx / CDN，域名不变）
    │  按 t/<cid>/ 前缀回源
    ▼
各租户自己的对象存储桶
```

**为什么选它**：`docs/deploy-runbook.md`、`docs/real-device-acceptance.md`、
`docs/launch-checklist.md` 里微信三类合法域名（request / uploadFile / downloadFile）
当前都只登记 `https://weichuan.ziyuanxingqiu.cn`。端上域名不变 ⇒ 小程序零改动、
零重新登记、零发版。代价是字节链路仍经过平台接入层；这是为「端上不动」付的钱，
不是性能优化。

路径里带 `t/<cid>/` 还有一个顺带好处：缓存键天然按租户隔离，不会串号。

### 1.1 权限模型（用户 2026-11-13 拍板：平台超管代填）

> 用户原话：「平台超管代填，这种权限肯定不能放给企业管理员的」。

**结论**：`tenant_storage_configs` 的**新增 / 修改 / 删除 / 启停 / 连通性自检 / 切回平台默认 /
触发搬迁**，全部只允许平台超级管理员操作；企业管理员连配置读接口都不给，更不给写接口。
理由不只是「权限收敛」，而是这条配置里装的是**客户的云凭据**：企业管理员一旦能改，
就能把自己企业的对象写到任意桶、把别人的桶指到自己账号下，且平台无法从业务侧察觉。
与 R2「静默失败」同源，属于必须在入口就堵死的越权面。

落地口径（全部是仓库里已有的现成机制，不新造轮子）：

| 层 | 做法 | 实读证据 |
| --- | --- | --- |
| 后端中间件 | 新配置路由一律挂 `middleware.PlatformAdminMiddleware(db)`，**不要沿用** `AuthMiddleware(db, "admin")` —— 后者只校验登录 + `role=admin`，企业管理员照样能过 | `backend/internal/middleware/auth.go:81-94`（`companyId != 0` → `code=403 仅平台超级管理员可操作`）、`handler/companies.go:144-149` |
| 后端既有样板 | 租户级微信凭据已是同一口径，照抄即可 | `handler/company_wx.go:31` `plat := middleware.PlatformAdminMiddleware(db)` |
| 前端路由 | 新页面挂 `meta: { platformOnly: true }`，守卫把非超管重定向 | `frontend/src/router/index.ts:121,129,138,146,153,161` 与 `:196` |
| 前端菜单 | 菜单组按 `platformOnly` 过滤，企业管理员看不到入口 | `frontend/src/layout/AdminLayout.vue:376,394`；身份来自 `store/auth.ts:19` 的 `isPlatform` |
| 双保险 | 前端隐藏只是体验，**后端必须独立拦截**；不能因为菜单藏了就省掉中间件 | `router/index.ts:182-183` 注释已写明这个口径 |

三条附加约束：

1. **前端不回显凭据给任何非超管**：企业管理员侧最多显示一句「存储由平台托管」的状态文案，
   不给 endpoint / bucket / 掩码 / 自检按钮。凭据只写不读、提交才覆盖，沿用
   `CompanyWXApp`（`model/models.go:463-499`）口径。
2. **审计要能还原「谁替谁改的」**：动作记录必须带上操作者（平台超管）与目标 `company_id`；
   企业管理员拿着接口地址硬调，应拿到 403 且不产生任何配置变更（见 §5 验收第 8 条）。
3. **运营流程**：客户提供凭据 → 平台超管在「企业管理」选中目标租户填写 → 连通性自检 →
   启用 → 审计留痕 → 企业管理员只看到状态。第一阶段不做「客户自助填凭据」的开关，
   将来若要开放，须单独评审密钥归属与解约清理，不能顺手把 `platformOnly` 摘掉。

> 注意：`RegisterStorage`（`handler/storage.go:20-34`）现有的预签名 / 签名 / CDN 刷新 / 私有文件
> 路由全部是 `AuthMiddleware(db, "admin")`，企业管理员可调用。**本轮不改这些既有路由**；
> 但新增的租户存储配置接口绝不能照抄这个写法，否则权限模型从第一天就是漏的。
> P1 实现时应加一条源码级守门：配置路由的中间件必须是 `PlatformAdminMiddleware`。

---

## 2. 现状盘点（逐条带证据）

| 主题 | 事实 | 证据 |
| --- | --- | --- |
| 存储抽象 | `Uploader` 只有两个方法 `Save` / `SaveBytes`；另有可选能力接口 `Presigner`、`Signer`、`CDNRefresher` | `backend/internal/storage/storage.go:16-21`、`capabilities.go` |
| 实现 | `LocalUploader` / `MinioUploader`（`s3` 共用）/ `OssUploader`；CDN 刷新五家（aliyun / tencent / qiniu / cloudflare / cloudfront） | `storage.go:25`、`minio.go:21`、`oss.go:19`、`cdn.go:11-23` |
| **单实例** | 启动时构造**一个** `up`，注入 5 处 `Register*`；`r.Static("/uploads", cfg.UploadDir)` 本地托管 | `backend/cmd/server/main.go:362-394` |
| **静默降级** | `newUploaderWithFallback` 构造失败时降级本地盘，理由是「改错可自愈」 | `main.go:525-541` |
| URL 形态双轨 | local 返 `/uploads/<name>`；云驱动返 `<BaseURL>/<key>` | `storage.go:106`、`minio.go:84`、`oss.go:84` |
| **URL 落库** | `avatar` / `logo` / `cover` / `bgImage` / `videoUrl` / `videoBackupUrl` / `album` 等业务字段直接存字符串；`media_check.url`、`object_key` 另有一份 | `backend/internal/model/models.go:17,76,155,197,267,341`、`media_check.go:27-31` |
| 老坑 | README 明说改 `upload_base_url` **只影响新上传，已入库完整 URL 不自动更新** | `backend/README.md:280` |
| 端上契约 | 小程序 `assetUrl`：绝对地址原样返回，相对路径拼 `baseUrl()`；管理端 `thumbnailURL` 按 URL 末位插 `_thumb` | `miniprogram/utils/request.ts:31-35`、`frontend/src/utils/thumb.spec.ts:6-15` |
| 从 URL 反推 key | `signHandler` 按 `cfg.UploadBaseURL` 前缀 TrimPrefix；`mediachk.ObjectKey` 同构；推不出就拒绝 | `backend/internal/handler/storage.go:135-147`、`internal/mediachk/mediachk.go:159-176` |
| 平台级资源 | 素材采用 `library/<id>.<ext>`、小程序码 `wxacode/<staffID>.jpg`、违规下架覆盖 `row.ObjectKey` —— 都走同一个 `up` | `handler/assets.go:155`、`wechat.go:254-255`、`media_check.go:459-460` |
| 租户计量 | `storage_usage` 已按 `company_id` 累计；`cid<=0` = 平台自身，不计量不拦 | `migrations/0032_storage_usage.sql`、`handler/storage_quota.go:74-78` |
| 匿名上传 | `uploadCompanyID` 取 `PostForm("companyId")`，**不校验企业是否存在 / 是否有效** | `storage_quota.go:163-174` |
| 配额 | `checkStorageQuota` 按 `storage_usage.bytes` 拦新上传 | `storage_quota.go:74-95` |
| 能力探测 | `capabilityHandler` 按**全局** `cfg.StorageDriver` 判 `videoUpload` | `storage.go:93-117` |
| 凭据加密现成件 | `pii` 包有 `Encrypt/Decrypt/IsEncrypted/SetKey/Reload/UsingPlaceholderKey`；`v1:` 前缀、AES-256-GCM、`base64.StdEncoding`、密钥 `PII_ENC_KEY` | `internal/pii/pii.go:32,36,90-109` |
| 租户级配置样板 | `CompanyWXApp` 按 `company_id` 唯一；`AppSecret` / `UploadKey` 是 `json:"-"`，只写不读、接口只回掩码 | `model/models.go:463-499` |
| 迁移基线 | 最新为 `0043_drop_template_id.sql`；迁移只建表、不许 `INSERT INTO`；模型必须进 `model.All()` | `backend/migrations/`、`model/all.go:12-27` |

---

## 3. 核心设计

### 3.1 资源引用去 URL 化（P0 地基）

现状是三种形态混在业务字段里：绝对 URL（云驱动）、`/uploads/...`（local）、以及
`media_check.object_key` 里那份从 URL 反推出的裸键。改 `upload_base_url` 只影响新上传，
正是这个结构造成的。

引入统一引用：

```go
type Scope string

const (
    ScopePlatform Scope = "platform" // 平台自有对象，company_id = 0
    ScopeTenant   Scope = "tenant"   // 归属某个企业
)

type Ref struct {
    ObjectKey string
    Scope     Scope
    CompanyID int64
}
```

统一对象键布局：

- 平台对象：`p/<原有相对路径>`，例如 `p/library/cat-1.png`、`p/wxacode/42.jpg`
- 租户对象：`t/<companyID>/<rand>.<ext>`，缩略图 `t/<companyID>/<rand>_thumb.<ext>`

**不一次性回填全库**。业务字段继续存字符串，但：

1. **写入侧**：上传 / 采用 / 小程序码 / 预签名一律只写 `objectKey`，不再写完整 URL；
2. **读取侧**：所有出参序列化前统一过 `storage.Resolve(ref) (string, error)`，兼容三种历史形态：
   - `http(s)://...`（旧绝对 URL）→ 原样返回，保证存量不裂图；
   - `/uploads/...`（旧相对）→ 按平台统一入口拼；
   - 裸 `objectKey`（新写入）→ 按 `Scope` + `CompanyID` 解析到对应基址。

这样零回填即可升级，P3 搬迁时把前两类批量改写成第三类即可；
`signHandler` 与 `mediachk.ObjectKey` 那段「从 URL 反推 key」的脆弱逻辑随之退休
（改为直接对 `objectKey` 签名 / 覆盖）。

`Resolve` 必须覆盖 **P1 刚加了 60 秒边缘缓存的五条公开只读接口**，否则会出现
「列表页是绝对旧 URL、名片页是新解析 URL」的双轨。

> ✅ **出参侧范围订正（2026-11-13 实装）**：上面「五条缓存接口」是**不够的**。
> 边界清单 §0 实读发现：下发资源 URL 的公开 GET 共有 **9 条**，带 60s 边缘缓存的只有 5 条，
> 漏掉 `/api/company`(logo)、`/api/team`(avatar)、`/api/projects/:id`(cover+album)、
> `/api/services/:id`(内嵌 `BizService`)。照「五条」接会漏一半出参。
> **正确口径：所有出参序列化点都要过，缓存只是其中一层的时序约束。**
> 该出参侧已在第五十三轮实际接好（9 条 GET + `signHandler` 全部过 `model/refs.go`），
> 对现行数据零行为变更。
>
> ✅ **后台出参补齐（2026-11-13 / 第五十七轮）**：上面「所有出参序列化点」在第五十三轮
> 只落实了**公开**那 9 条，后台 R11–R15 五组出口（`content.go` 的 company-info /
> company-profile / categories / services / `projectToMap`、`apply.go` 的 `/api/employee/me`
> 与员工导出）当时未接。本轮补齐——裂图在后台更难被发现（公开页正常时，运营只会怀疑前端）。
> 落地纪律：**只解析「本次请求不会再写库」的值**，PUT/POST 先落库再对副本解析，
> 否则会把 `/uploads/...` 写回库、把裸键形态静默退回历史形态。
> `album` 是 `json:"-"` 的 JSON 数组字符串，只能在两处展开点手工调
> `model.ResolveResourceRefs`，故新增 `TestAlbumExpansionSitesResolved` 盯住展开点数量与解析调用。
>
> ✅ **写入侧实装（2026-11-13 / 第五十四轮，本地驱动安全版）**：W1 上传 / W2 素材 /
> W3 小程序码 / W5 预签名已改为**对象落带归属前缀的键**（`t/<cid>/...` / `p/...`）；
> `Uploader.Save` 接口由 `Save(fh)` 改为 `Save(fh, key)`，键由 `ObjectKeyFor` 生成。
> **但库里仍写驱动返回的 URL**（本地驱动即相对 `/uploads/<key>`），因此端上契约、前端、
> 出参侧零改动（`ResolveRef` 对 `/uploads/` 原样保留）。**严格裸键**（库里只写 `t/<cid>/x`）
> 依赖 nginx `/uploads/`→桶 反代（R1），属云驱动阶段。W4/W6 因 `mediachk` 已把 URL 反推成裸键、
> 且单桶下复用同一键，无需改动。
>
> ✅ **写入侧收敛（2026-11-13 / 第五十九轮）**：上面「库里仍写驱动返回的 URL」这句**本轮已改**。
> 真正把 URL 送进业务字段的不是那 4 个上传点，而是 **12 个业务表单写入点**
> （`content.go` 8 处单值 + 1 处数组、`apply.go` 6 处单值），它们收的都是前端回填的字符串。
> 本轮在这 12 处落库前统一过 `storage.NormalizeOrKeep`，把落库形态从「三种混存」收敛到裸键。
>
> **归一函数为什么叫 `NormalizeOrKeep` 而不是 `NormalizeRef`**：`NormalizeRef` 是**严格**的
> （推不出键就报错），但业务字段里合法存在外链（`video_url` 的 `url` 那一档、租户自建 CDN 的老图）。
> 严格模式会让「填个 B 站链接」直接保存失败。四条分界：
>
> | 输入形态 | 处置 | 标记 |
> | --- | --- | --- |
> | 已带 `p/` / `t/<cid>/` 前缀 | 归一，校验 scope 归属 | `ref-normalized` |
> | `/uploads/...` / 基址开头的 URL / 签名 URL | **必须先判「剥出来是否仍带前缀」**：带前缀（本地驱动返回的 `/uploads/t/5/x.jpg` 剥完就是完整键）→ 归一；不带前缀（第五十四轮前的老对象，键就是 `x.jpg`，对象真的在 `/uploads/x.jpg`）→ **原样保留** | `ref-normalized` / `ref-legacy-path` |
> | 合法 `http(s)://` 外链（host ≠ 平台基址） | 原样保留 | `ref-foreign-url` |
> | 同源却推不出键 / `javascript:` / `data:` / 路径穿越 | **报错**，不放行 | — |
>
> 两个会让功能整体不可用的陷阱，都写成了红测试：
> ① 老对象**绝不能补前缀**（补了指向不存在的键，端上立刻 404，而写库那一刻毫无异常）；
> ② 本地驱动的 `/uploads/t/5/x.jpg` 剥完**本身就是完整键**，再套一次 `ObjectKeyFor` 会撞「双重加前缀」
> 而报错——那会让本地存储下**所有**表单保存全部失败。
>
> **scope 校验有意宽松**：平台对象（`p/...`）出现在租户字段里是**允许**的。真实链路是访客在小程序
> 申请页上传头像时未登录，请求没带 `companyId` → 落在 `p/` 下（`handler.uploadCompanyID`），
> 而申请单的 `company_id` 是某租户。真正要拒的是反过来：A 租户的键（`t/9/`）写进 B 租户字段。
>
> **两处审批侧不是「失败就报错」**：`ResolveResourceRef` 解析失败时**返回原值**（不是空串，
> 与直觉相反），意味着宽松放行脏值会被原样出参，`javascript:` 会进 `<img src>`。
> 故开通审批归一失败 → **置空**（审核是人工动作，不为一张脏头像让整单批不了），
> 名片修改审批失败 → **跳过该字段**。两处都打 WARN，不静默。
>
> **回滚开关**：`STORAGE_REF_STRICT`，默认开；设 `0` / `false` 退回第五十八轮行为（整数原样落库）。
> 选「默认开 + 可关」而非反向——收敛不默认生效等于债没还。回滚是安全降级：
> 出参侧 `ResolveRef` 本来就兼容三种形态。每次调用读 env 而非包级变量（包级会污染测试）。

> ✅ **P0a 落地订正（2026-11-13）**：上面草稿里写的 `storage.Resolve(ref) (string, error)`
> 只是骨架示意，实际落地的 `backend/internal/storage/ref.go` 是一组**吃库里原始字符串**
> 的纯函数，签名如下（P0a 阶段不接任何 handler，线上行为零变化）：
>
> | API | 职责 |
> | --- | --- |
> | `ObjectKeyFor(scope, companyID, rel)` | 生成 `p/...` / `t/<cid>/...`；拒绝双重前缀与路径穿越 |
> | `ParseRef(raw) (Ref, error)` | 从新键前缀还原 `Scope` / `CompanyID`；历史形态返回 `ErrLegacyRef` |
> | `IsNormalizedRef(raw) bool` | 是否已归一化 |
> | `NormalizeRef(raw, baseURL, scope, companyID) (Ref, error)` | 写入侧：剥 URL / `/uploads/` 前缀并套归属；幂等；跨租户改绑报 `ErrScopeMismatch` |
> | `ResolveRef(raw, origin) (string, error)` | 读取侧：三形态 → 端上 URL；`origin` 必须是**纯 origin**（不含路径） |
>
> 为什么不是「先 Parse 成 Ref 再 Resolve」：出参侧拿到的本来就是三种历史形态混在一起的字符串，
> 多一层对象转换只会给「漏兼容」制造空间。将来接 `UploaderResolver` / 客户自有域名时，
> 在 `ResolveRef` 外包一层「按租户选 origin」即可，端上契约不变。
>
> 三条额外钉子：① 存量绝对 URL **原样返回**（含 `?v=` 缓存戳），改了老数据立刻裂图；
> ② 空值返回空串且无错误（`avatar` / `logo` / `videoUrl` 常为空，出参侧不能整条报错）；
> ③ `data:` / `javascript:` / 路径穿越 / 带 `?` 的业务字段一律显式报错，绝不静默改写。
> `legacyRel` 与 `mediachk.ObjectKey` 同构且**先判 `/uploads/` 相对路径**——反过来会把
> `/uploads/x.jpg` 推成 `uploads/x.jpg`（多一层目录），下架覆盖写错位置。
>
> **守门名对账**：§4 表里草稿写的 `TestResolveKeepsLegacyAbsoluteURL` /
> `TestLegacyUploadsRelativeURL`，实际落地为 `TestResolveRefKeepsLegacyAbsoluteURL` /
> `TestResolveRefLegacyUploadsRelativeURL`（`backend/internal/storage/ref_test.go`）。
> 另有 `TestRefWiringGate`（在 `ref_p0a_guard_test.go`，原 `TestP0aRefIsNotWiredIntoProduction`）
> 钉住「写入侧 API 不许出 `storage` 包、出参侧解析只允许在 sanctioned 文件」——
> **P0b 开始接线时它必然变红，那是有意信号**：先交付边界清单、把它升级成三集合对账守门，
> 不许直接删掉。第五十三轮实装出参侧时已把它升级为 `TestRefWiringGate`（三集合：
> 写入侧 API 越界 / `storage.ResolveRef` 仅限 `model/refs.go` / `model.ResolveResourceRef` 仅限
> sanctioned 出参侧文件）。反向验证已做两组：改坏绝对 URL 分支必红、在 handler 包插一行真实
> `storage.NormalizeRef` 调用必红（守门定位到 `handler/storage.go`）；还原后文件 sha256 零漂移、全包复绿。

### 3.2 `tenant_storage_configs`（迁移 0044）

字段草案（**只建表，不写任何业务数据 / 演示配置**）：

| 列 | 说明 |
| --- | --- |
| `id` / `company_id` | 主键；`company_id` 唯一索引 |
| `driver` | `local` / `minio` / `oss` / `s3`，与现有枚举一致 |
| `endpoint` / `bucket` / `region` / `use_ssl` | 与 `config.OverlayFromSettings` 现有字段一一对应 |
| `access_key_enc` / `secret_key_enc` | 密文列；模型上 `json:"-"`，只写不读 |
| `key_id` | 密钥版本标识，为将来换钥匙留口子 |
| `public_base_url` | 第一阶段**留空**（= 走平台统一入口）；客户自有域名开关启用时才填 |
| `public_read` | 桶是否公开读；第一阶段试点要求为 true |
| `enabled` / `status` / `last_checked_at` / `last_error` | 开关 / 连通性自检结果 / 脱敏错误摘要 |
| `created_at` / `updated_at` | 时间戳 |

四条硬约束：

- 迁移写 **MySQL 8.0 裸 DDL**，不得出现 `DROP/ADD COLUMN IF EXISTS`（0043 线上 1064 事故的钉子，见 `migrate/apply_test.go` 的 `TestMigrationsUseMySQL8DDL`）；
- 新增模型必须进 `model.All()`，否则 devserver 与 `handler_test.go` 运行期 `no such table`；
- 配置由后台界面写入，**不放在 `sys_setting`**——`sys_setting` 是全局单值，没有租户维度；
- **只有平台超管可写**（§1.1）：配置界面挂 `platformOnly`，接口挂 `PlatformAdminMiddleware`；
  企业管理员侧不暴露写接口、菜单隐藏、连读接口都不给。表里的密文列 `json:"-"`，
  模型上永远不参与序列化。

#### ✅ 已落地（2026-11-13 / 第五十八轮）

迁移 `backend/migrations/0044_tenant_storage_configs.sql` 已建表，模型
`backend/internal/model/tenant_storage.go` 已进 `model.All()`。与上面草案的差异只有一处：

**多了 `host_header` 列**（草案里没有）。理由是 R1 真桶实测（第五十五轮）：只给
`proxy_ssl_verify` + CA + `ssl_name` 而漏掉 `proxy_ssl_server_name` + `proxy_ssl_name`，
请求被桶以 **421** 拒绝。而这个 Host 的取值**逐厂商不同**——MinIO 走 path-style、
OSS/S3 走 virtual-hosted-style，桶名拼在 endpoint 前面还是后面不是一条通用规则。
所以给一列让超管照控制台抄，胜过写一段「智能推断」。

实际执行的三条口径：

1. `public_base_url` / `public_read` / `key_id` 三列**建了但当前无人消费**。
   它们是为「客户自有域名」与「密钥轮换」留的位置，写进表比下一轮再动表便宜；
   但也意味着后台若有界面，这三项现在不该出现在表单上（填了不生效比不填更坏）。
2. 凭据列 `access_key_enc` / `secret_key_enc` 走 `pii.Encrypt` 密文（与 0024 的
   `company_wx_app.app_secret` 明文策略**不同**：那次靠「读取只回掩码」就够了，
   本表的凭据要参与真实 S3 签名，漏一次明文就随报错带出去）。
3. **本轮没有写接口**。配置表目前只能靠手工 SQL 写入，接口与后台界面是下一轮的事。
   这点必须写明：否则看到「表存在、生成器能读」会以为功能可用。
   `access_key_enc` 这一列现在**没有任何代码在写**，`pii.Encrypt` 的调用点落地时才补。

#### ✅ 第六十三轮订正：`host_header` 不只是 SNI，它还决定路径风格

真桶实测（`tools/s3-e2e/`，versitygw 1.11 + POSIX 后端）推翻了第五十五轮桩装置留下的一个假设：
桩用「nginx 静态目录当桶」时，桶根就是目录根，于是「只剥 `/uploads/`」看起来是通的；
换成真 S3 服务端后，`GET /t/7/x.png` 回来的是一句

```
400 InvalidBucketName   <BucketName>t</BucketName>
```

S3 把路径第一段当成了桶名。现在的判定（`nginxsnippet.Target.PathStyle`）：

| `host_header` | 形态 | 生成的 rewrite |
| --- | --- | --- |
| 留空 / 等于 endpoint | 路径风格（自建 MinIO 单 IP 最常见） | `/uploads/(.*)$ → /<bucket>/$1` |
| 以 `<bucket>.` 开头 | 虚拟主机风格（AWS S3 / COS 控制台给的域名） | `/uploads/(.*)$ → /$1` |
| 其它取值 | **生成失败**（判断不出风格；猜错=该企业全站裂图且无任何报错） | — |

桶名进 path 不违反「键布局只在 Go 侧」：桶名是这条配置自己的属性（同段 `proxy_pass`
就指向它），`t/<cid>/` 仍然只由 `ObjectKeyFor` 定义。守门：
`TestRenderBucketAppearsExactlyOnce`（两种形态）+ `TestRewriteKeepsObjectKeyIntact`（跨包契约）
+ `tools/s3-e2e`（真桶取字节）。

守门清单（全部已做反向验证：改坏必红、还原后 sha256 零漂移）：

| 守门 | 位置 | 盯什么 |
| --- | --- | --- |
| `TestMigrationsUseMySQL8DDL` / `TestMigrationsCarryNoBusinessData` / `TestManifestMatchesMigrationsDir` | `internal/migrate/` | 0044 不许混入 MariaDB 方言与业务数据；清单登记 |
| `TestMigrationsTableNamesMatchModels` | `internal/model/` | 模型表名与迁移表名双向一致 |
| `TestListStorageTargetsMapsAllRows` | `internal/repository/storage_config_test.go` | 逐字段映射；**不过滤** `enabled=false` / `driver=local`，判定只归 `NeedsSnippet` |
| `TestListStorageTargetsMissingTableIsRecognizable` | 同上 | 表缺失是 `errors.Is(ErrStorageConfigMissing)`，不是一句混在一起的 `Error 1146` |
| `TestListStorageTargetsEmptyIsSuccess` | 同上 | 空表 = 正常成功。与上一条互为反面，两者都返回 `len==0`，唯一区别是 err |
| `TestListStorageTargetsNeverSelectsCredentials` | 同上 | `storageTargetCols` 静态断言 + gorm `DryRun` 双保险：凭据列绝不进 SELECT |
| `TestResolveTargetsRequiresExplicitSource` / `...RejectsBothSources` | `cmd/nginx-snippets/main_test.go` | `--dsn` / `--targets` 必须且只能给一个 |
| `TestRenderRefusesToWipeExistingSnippet` | 同上 | 零 location 覆盖非空目标文件默认拒绝，要清空必须 `--allow-empty` |
| `TestWriteFileAtomic` | 同上 | 权限 0644（`CreateTemp` 是 0600）+ 临时文件不残留 |
| `TestMySQLMigrations`（表清单新增 `tenant_storage_configs`） | `internal/handler/handler_test.go` | 0044 在真实 MySQL 8.0 上必须建出这张表 |

MySQL 实跑记录（2026-11-13，本地 `mysql:8.0.46` 容器）：
- 0044 连跑两次退出码均为 0、只有密码 warning，`SHOW CREATE TABLE` 得到 19 列 + 唯一索引 +
  `utf8mb4`，注释全部保留；
- `TEST_MYSQL_DSN` 跑 `TestMySQLMigrations` 与 `TestAPIFlowMySQL`（完整 parity，含全部业务
  flow）全绿；
- 生成器对真库（4 行：minio 启用 / oss 启用 / local / s3 停用）实跑，产出 2 段 location，
  `check --dsn` 对账一致；三种失败现场（表不存在 / 库不存在 / 端口不通）的错误文案互不相同，
  且**全部不落盘**。

一个不值得复制的坑：0044 的注释里原本有一句「（`CREATE TABLE IF NOT EXISTS` 在 MySQL 8.0
合法）」，被 `TestMigrationsTableNamesMatchModels` 扫成一张**叫 `IF` 的幽灵表**并报红——
那个正则不剥注释。注释改写成「建表语句自带 `IF NOT EXISTS`」才过。

#### ✅ 第六十四轮：P3 拆成两半，先交「只读的计划层」

搬迁由「复制对象 + 改库 + 台账」三步组成，对象存储与 MySQL **不在同一个事务里**，失败必然要能续跑和回滚。在不知道分布的情况下开跑，等于把不可回滚的操作压在一次运行上。所以 P3 拆两半，本轮只交前半：

| 层 | 交付 | 能不能改数据 |
| --- | --- | --- |
| 计划（本轮） | `storage.PlanNormalize` + `internal/refmigrate` + `cmd/storage-backfill plan` | **不能**：不 UPDATE、不 INSERT、不访问对象存储 |
| 执行（下一轮） | 按计划复制对象 → 改库 → 台账 → `--force` / `--rollback` | 能，且必须先有幂等与回滚测试 |

三条不能省的约束：

1. **执行器消费计划 JSON，不许自己再扫库**（`--out` 产出的那份）。否则「计划看到的」与「执行改的」会分叉，而这种分叉在两边各自的单测里都看不出来。
2. **`PlanNormalize` 与写入侧 `NormalizeOrKeep` 同源**：写入侧的「能不能原样留」与搬迁计划的「搬完该是什么」共用同一套判定（`ParseRef → legacyRel → ObjectKeyFor`），不各写一份。分叉的症状是「迁移脚本搬过去的键，写入侧又不认」。用**差分守门**钉住（同一批 13 个输入喂两个函数逐条比对语义），而不是各写一份期望值。
3. **对象按「源键 → 目标键」去重**：同一张图可能被多个字段引用（实测 `shared-logo.png` 被 3 个字段引用、`media_check` 的 `url` 与 `object_key` 指向同一对象），复制只做一次，改库仍逐条。

7 种归类，每一种处置都不同（压成一个 bool 就丢信息）：

| 归类 | 含义 | 执行器该做什么 |
| --- | --- | --- |
| `empty` | 空值 | 不进计划（单值列 SQL 层排除、数组空槽跳过） |
| `tenant-ready` | 已是 `t/<cid>/` | 库不用改；对象在不在桶里另查（要联网） |
| `platform` | `p/` 平台对象（访客未登录上传头像的真实形态） | **永不搬** |
| `migrate` | 历史形态（`/uploads/x.jpg`、绝对 URL、裸键） | 复制对象 + 改键 |
| `foreign-url` | 外域直链（`video_source=url` 那一档） | 原样保留 |
| `foreign-tenant` | 别人的租户键（`t/9/` 出现在 `t/5` 的行） | **人工确认**，不许跳过 |
| `invalid` | 伪协议 / 路径穿越 / 坏图集 / `company_id=0` | **人工确认**，不许猜 |

真实 MySQL 取证（`mysql:8.0.46`，44 个迁移全应用 + `tools/backfill-fixture/seed.sql` 灌 13 张表共 31 条引用）：

```
合计：31 条引用
  待搬迁 15 条（去重后 12 个对象要复制）
  已就绪 3 条 · 平台对象 4 条（永不搬）· 外域直链 3 条（原样保留）
⚠ 需要人工处理 6 条（脚本不猜）
```

三处交叉验证：

- 两次运行 `--out` 的 JSON **逐字节相同**（Items 随列清单 + 主键有序）；
- 九张表 `CHECKSUM TABLE` 在盘点前后**逐字节不变**；
- 反向验证：往扫描里注入一次 `UPDATE <表> SET <列>='t/0/x'` 后，九张表校验和**全部变化**、`TestScanIsReadOnlyAndIdempotent` 变红——说明上面那条「不变」是真检测，不是巧合。

本轮自己踩到并修掉的四件事：

1. **`text/tabwriter` 排不好中文表**：它按 rune 个数补空格，而中文是双宽字符，「员工头像」（8 列宽）与「case1-a.jpg」被算成同样宽，整表错位。换成按显示宽度对齐的 `renderTable`（东亚宽字符算 2 列）。这不是美观问题：列错位后运维会**读错列的数字**。
2. **对齐用例不能自己量自己**：第一版只断言「每行宽度相同」，而它用的尺子就是被测的 `dispWidth`——尺子整体算错时仍然通过。补 `TestRenderTableGolden` 写死整段期望输出。
3. **退出码契约写错**：`usage` 承诺「有异常数据 → 2、命令出错 → 1」，第一版实现把两者都走成 1。CI 里这两种红灯的处置完全相反（重跑命令 vs 去改数据）。抽出 `exitFor` 并单测。
4. **`album` 是 MySQL 的 `json` 列**：`not-json` 这类语法坏值在 INSERT 阶段就被拒（`ERROR 3140`），生产库里**不可达**；真实能出现的坏形态只有「JSON 合法、解不成 `[]string`」（如 `["/uploads/ok.jpg", 123]`）。单测用 SQLite 造坏 JSON 是个更宽的口子，fixture 必须用真实可达的那种。

新增守门（都做过反向验证）：

| 守门 | 盯什么 |
| --- | --- |
| `TestInventoryCoversEveryResolveRefsField` | 源码扫描 `model/refs.go` 的 `ResolveRefs`，每个被解析的字段在 `Inventory` 里必须有对应列。漏掉的后果是**那批对象永远留在平台盘上，且没有任何报错** |
| `TestInventoryHasNoDuplicateColumn` | 同一「表 + 列」不许盘点两次（否则合计翻倍、执行器重复复制同一个对象） |
| `TestPlanNormalizeMatchesNormalizeOrKeep` | 差分守门（13 子例，覆盖空值/新形态/相对路径/绝对 URL/缓存戳/裸键/外链/伪协议/穿越/串号） |
| `TestObjectsDedupesBySource` | 去重（3 条引用 → 1 个对象、`Refs==3`） |
| `TestAlbumBrokenJSONIsReported` | 坏图集报 `invalid` 而不是当空消失 |
| `TestScanIsReadOnlyAndIdempotent` | 两次扫描完全一致 + 库里值一字节不变 |
| `TestRenderTableGolden` / `TestExitForDistinguishesProblemsFromFailures` | 中文表格对齐与退出码契约的绝对预期 |

边界：**本轮（第六十四轮）没有 `apply` / `rollback` 子命令**，也不打算在缺幂等与
回滚测试之前给出。`--dsn` 刻意没有默认值（避免误连生产）。
第六十五轮在补齐幂等 / 回滚测试之后交付了执行器，见 §4 的 P3 进度。

#### ✅ P1 后半段已落地（2026-11-13 / 第六十轮，表终于能写了）

第五十八轮之后这张表的状态是「能读不能写」——`access_key_enc` 没有任何代码在写，
生产上只能手工 SQL 写入。本轮补齐写接口与后台界面：

| 接口 | 作用 |
| --- | --- |
| `GET /api/admin/companies/:id/storage` | 读（凭据只回掩码；没配过回 `configured=false` 的完整视图，不是错误） |
| `PUT /api/admin/companies/:id/storage` | 新建 / 覆盖（凭据 `pii.Encrypt` 落密文） |
| `DELETE /api/admin/companies/:id/storage` | 切回平台存储（删行，幂等） |
| `POST /api/admin/companies/:id/storage/check` | 连通性自检（回写 `status` / `last_checked_at` / `last_error`） |

四条落地口径：

1. **权限**：四个接口全部挂 `PlatformAdminMiddleware`，不是 `AuthMiddleware`。
   源码级守门 `TestTenantStorageRoutesUsePlatformAdminGuard` 用括号配对扫出每条
   `r.VERB(...)` 调用，要求 `plat` 出现在第一个 `func(` 之前——写在 handler body 里
   等于没挂。另加一条：本文件不许出现 `AuthMiddleware`（判之前先剥注释，
   否则文档注释里「不要用 AuthMiddleware」这句会被当成违规，守门误伤自己）。
2. **凭据三态**：省略字段 = 保留原值、空串 = 清空、有值 = 覆盖。三态不是洁癖——
   界面回显的是掩码，「只看不改点保存」提交的是不带 `accessKey` 的请求；
   若把「不传」当成「清空」，一次改 bucket 的操作会把客户凭据抹掉，而接口照样回 `code:0`。
3. **校验不重写**：endpoint 不许带 scheme、bucket 不许含路径与 `..`、hostHeader 不许含引号，
   全部复用 `nginxsnippet.Target.Validate` 单点（写入侧复制一份必然与生成侧漂移，
   漂移的表现是「接口说存成功了，生成器却报错」）。只在写入侧加两条**产品边界**：
   第一阶段只支持公开读桶（私有桶要预签名链路）、启用时必须凭据齐全。
4. **改了配置就作废上一次自检结论**（`status` 回 `unknown`）。否则「改完地址但没重新自检」
   的后台会一直显示上一次的绿点，读的人会以为新配置已经验过了。

**自检为什么必须带凭据**：S3 语义下匿名请求对「不存在的桶」和「存在但无权访问」回同一个
403（防桶名枚举），**不带签名的探测对 S3 没有判别力**——探出 200 只说明域名解析通了。
做一个「看起来验过了、其实没验凭据」的自检比不做更坏。实现用 SDK 的
`BucketExists` / `IsBucketExist`（HEAD bucket，最小权限面；换成列举对象会让
「只给写权限、没给列举权限」的正常桶被误判）。错误只回我们自己的文案，**不透出**
SDK 原文——AccessDenied 响应会回显 AccessKeyId 片段，而 `last_error` 是常驻后台展示的字段。

**自检的覆盖边界必须对运营明说**（接口回 `checkedScope`）：只验「凭据有效 + 桶可达」，
**不含** nginx 侧 Host/SNI（漏 SNI 会被桶 421，见 R1）、CDN 缓存、证书链。
这一点和「自检通过 ≠ 可以切量」是同一件事，写在界面 alert 里比写在文档里有用。

**仍不做**：配置不承载流量（上传与出参仍走平台那一个实例，要等 P2 的
`UploaderResolver`）；`public_base_url` / `key_id` 仍不进出参也不进表单（填了不生效比不填更坏）；
私有桶；密钥轮换。**别看到「表能写、能自检」就以为多租户存储能用了。**

守门清单（本轮新增，全部做过反向验证）：

| 守门 | 位置 | 盯什么 |
| --- | --- | --- |
| `TestTenantStorageRoutesUsePlatformAdminGuard` | `handler/tenant_storage_guard_test.go` | 每条路由都挂 `plat` 且在第一个 `func(` 之前；文件里无 `AuthMiddleware`；路由数量变化必须回来确认 |
| `TestTenantStorageViewNeverExposesCredentials` | 同上 | SecretKey 无掩码出参；AccessKey 掩码必须经 `maskCredential` |
| `TestTenantStorageTenantAdminForbidden` | `handler/tenant_storage_test.go` | 企业管理员（role=admin）四个接口全 403，且配置行零变更 |
| `TestTenantStorageCredentialTriState` | 同上 | 不传=保留 / 空串=清空 / 有值=覆盖（密文重新生成） |
| `TestTenantStorageValidation` | 同上 | scheme / 驱动枚举 / 私有桶 / bucket 路径 / 企业不存在 / cid=0 六种拒绝 |
| `TestTenantStorageCheckFailure` | 同上 | 失败原因写进 `last_error`，且不含凭据 |
| `runAPIFlow` 的「租户存储配置」 | `handler/handler_test.go` | 四个接口在 SQLite 与 MySQL 下行为一致（parity 覆盖，非本轮的 SQL 分支） |

### 3.3 `UploaderResolver`（P2）

> **落地时接口收敛了（第六十一轮）。** 原草案的四个方法收缩为三个，理由记在下面；
> 同时订正两处**已经过期**的要点（presign 前缀与 signHandler 入参）。
> 改之前请以本节为准，`§3.3` 的旧代码块只作历史留档。

```go
// 原草案
type Resolver interface {
    For(cid int64) (Uploader, error)              // cid <= 0 → 平台默认实例
    PresignerFor(cid int64) (Presigner, bool)
    SignerFor(cid int64) (Signer, bool)
    RefresherFor(cid int64) CDNRefresher
}

// 实际落地（internal/storage/resolver.go）
type Resolver interface {
    For(cid int64) (Uploader, error)
    Platform() Uploader        // 平台自身那一份，不受租户路由影响
    DriverFor(cid int64) string
}
```

要点：

- **实例缓存**：按 `company_id + updated_at` 版本号失效，别每次上传都重建 S3 客户端。
  落地时保留了「每次读一行库」：单行唯一索引查询，比缓存 stale 的代价小得多；
  只把**构造实例**这一半缓存起来（缓存命中的指针相等有单测钉着）；
- **去掉租户级静默降级**：租户配置解析失败必须显式 `code=1`，**绝不回退平台实例**。
  现在 `newUploaderWithFallback`（`main.go:528`）的降级是为了「改错还能进后台改回来」，
  在平台级保留合理；但**绝不能当租户的兜底**——客户以为文件在自己桶里、实际躺在
  平台本地盘，是「没人报错时用户无从察觉」的典型静默失败，比直接报错严重得多；
- **注入点**：`RegisterUpload` / `RegisterStorage` / `RegisterMediaCheck` /
  `RegisterAssets` / `RegisterWechat` 的参数由 `storage.Uploader` 换成 `storage.Resolver`，
  以及 `cmd/server/main.go`、`cmd/devserver/main.go` 与 `handler/handler_test.go` 的 `buildEngine`
  三处装配。按项目纪律：新增 `handler.RegisterX` 必须同时改这三处，漏 devserver = 静默 404；
- `capabilityHandler` 改为按当前租户实例判 `videoUpload`，不再看全局
  `cfg.StorageDriver`——否则 A 配了 S3、B 仍是 local，后台会给 B 也显示「可直传」；
- ~~`presignHandler` 的 `storage.RandName(ext)` 生成裸 key，必须带上 `t/<cid>/` 前缀~~
  ——**已在第五十四轮 W5 落地**（走 `storage.ObjectKeyFor(scope, cid, name)`），本轮核实为过期；
- ~~`signHandler` 改为直接按 `objectKey` 签名~~ ——**不需要改入参契约**：
  `storage.legacyRel` 在基址对不上时会退到「只认 `/uploads/` 段」的兜底，而租户对象存库形态是
  `<平台入口>/uploads/t/<cid>/x.jpg`，反推 `t/<cid>/x.jpg` 是确定性的。本轮只换成
  「用该租户的 Signer」，前端不必改。

#### ✅ 第六十一轮已落地：形状接上，开关默认 off

| 项 | 落地内容 |
| --- | --- |
| `storage.Resolver` | 上述三方法接口 + `StaticResolver`（`TENANT_STORAGE_ROUTE=off` 时的**正式实现**，不是临时脚手架） |
| `internal/uploadroute` | `Resolver`（按 cid 取实例、按 `updated_at` 缓存）、`DBLoader`（单行查询 + 解密）、`Verify`（启动自检表在不在）、`RouteEnabled` |
| 装配 | `main.go` / `cmd/devserver/main.go` / `buildEngine` 三处；`TENANT_STORAGE_ROUTE` 默认 off，只有 `on/1/true/yes` 才开 |
| 行为变更 | 只有两处**判定**：`capabilityHandler` 按租户实例判、`signHandler` 按租户实例签。off 下两者等价于旧实现 |
| 生效状态回报 | 出参 `trafficEnabled` 改为 `uploadroute.RouteEnabled()`，对话框顶部提示随之切换；写死 `false` 会在开关打开后继续骗人。第六十二轮补 `activeForTenant` / `inactiveReason`（按企业，五态） |

**键前缀 ⇔ 实例必须同源**（本轮新增的一条硬约束）：

| 写路径 | 键前缀 | 实例 | 为什么不能换 |
| --- | --- | --- | --- |
| 普通上传 / 直传预签名 / 违规图下架覆盖 | `t/<cid>/` | `For(cid)` | 文件属于该租户，nginx 按租户回源 |
| 素材采用（`assets.go`） | `p/library/` | `Platform()` | `p/` 由平台 `/uploads/` 回退段托管；写进客户桶 = 键在平台前缀下、内容在客户桶里 → 404，且是幂等覆盖写，第一次错没人会发现 |
| 小程序码（`wechat.go`） | `p/wxacode/` | `Platform()` | 同上。注意这与「按 staffId 取**凭据**」不冲突：用的是客户 AppID，产出的图仍是平台资源 |

覆盖边界（本轮**不**做，留给下一轮）：

- 翻开关的端到端验收（真桶、真 nginx、真回源）；
- 存量对象回填（P3）：开关打开后**只影响新上传**，老对象仍在平台侧，URL 形态不变所以不会裂图；
- `public_base_url` 的消费方（第一阶段恒空 = 走平台统一入口，由 §3.4 的回源段承接）；
- CDN 刷新按租户分桶（`refreshCDN` 目前按精确 URL 清，统一入口下 URL 已带 `t/<cid>/`，够用）。

> ✅ 第六十二轮已按上面的区分补了 `activeForTenant` + `inactiveReason` 两个字段：
> `trafficEnabled` 只答「配置保存后能不能生效」（进程级），`activeForTenant` 答
> 「这家此刻是不是真的在用自有桶」，未生效时 `inactiveReason` 说明是五种成因里的哪一种
> （未配置 / 未启用 / 驱动是 local / 进程未开路由 / 凭据不完整）——五种成因的处置动作
> 完全不同，只回一个 bool 会让超管拿着 false 去猜。界面顶部提示据此分三态。
>
> 另外补了一条跨包契约守门 `TestLocationPrefixMatchesGoObjectKey`：
> `storage.ObjectKeyFor` 拼出的键前缀必须**等于** `nginxsnippet` 生成的
> `location ^~ /uploads/t/<cid>/` 前缀。两边各自独立拼字符串，任何一边被改
> （`t/` → `tenants/`、location 漏了 cid、rewrite 多剥一层）都是「写进去了、URL 也有、
> 取图 404」，且后台显示正常。这条是 R1 里真正缺的那一环：不是 nginx 语义对不对，
> 而是 Go 侧写的键与 nginx 侧认的路径是不是同一个东西。

守门与验证（第六十一轮实跑）：

- 源码级：`TestUploadWritesRouteByObjectKeyPrefix`（逐文件钉死 `For` / `Platform` 的用法）、
  `TestRegisterFuncsTakeResolver`（五个 Register 签名不得退回 `storage.Uploader`）、
  `TestAssemblyPassesResolverNotUploader`（三处装配都得有两条分支）；
- 单测：`storage/resolver_test.go` 3 条、`uploadroute/resolver_test.go` 9 条（含「读配置失败不回退」
  「禁用行当平台」「按 updated_at 失效」「开关默认侧别」「DBLoader 三态」「Verify 缺表」）；
- 接线：`TestPresignUsesTenantInstanceAndTenantKey`（cid 透传 + `t/7/` 前缀）、
  `TestCapabilityJudgesTenantInstanceNotGlobalConfig`（全局 local + 租户 minio → 可直传）；
- 端到端冒烟（devserver，`TENANT_STORAGE_ROUTE=on`）：无配置行 → `code:0` 落 `t/1/…`；
  有行但凭据空 → `code:1` 且回「已按要求不回退平台存储」；删掉行 → 回到 `code:0`。

### 3.4 平台域名统一入口 + 按租户回源（P2 的接入层）

**这是本设计唯一尚未验证的假设，也是第一阶段最大风险（R1）。**

链路：`/uploads/t/<cid>/<name>` → 平台接入层按前缀反代到该租户桶的公网读域名 → 回源。
字节不经过 Go 服务（`capabilityHandler` 已经写过：大视频经应用服务器中转会拖垮服务）。

必须同步处理的既有事实：

- `deploy/baota/nginx-site.live.conf` 的 `/uploads/` 现在是本地静态托管，且 P1 给它配了
  `public, max-age=31536000, immutable`；改成反代要同步改这里，并**先验证**反代与缓存头不打架；
- 新增 / 修改租户要生成 nginx snippet → `nginx -t` → reload；要有守门脚本对账
  「配置表里 enabled 的租户」与「生成的 snippet」，漏一条就是 404；
- `refreshCDN`（`upload.go:126`）现在只接 URL 列表。统一入口下 URL 带 `t/<cid>/`，
  要么按租户分别 Purge，要么接受清整个 `/uploads/` 会误伤所有租户缓存；第一阶段
  建议 `refreshCDN` 增加 `companyID` 参数，先用平台 refresher 按精确 URL 清。

**第一阶段试点口径**：租户桶公开读 + 单租户试点。私有桶签名、跨地域 CDN、
客户自有域名都留到后续阶段再评估。

> ✅ **R1 已实测（2026-11-13 / 第五十五轮）**：结论「**有条件成立**」，
> 装置与证据在 `tools/nginx-r1-check/`，完整记录见 `docs/tenant-storage-r1-feasibility.md`。
> 要点：
>
> 1. **本节开头那句「`/uploads/` 现在是本地静态托管」是过时的** ——
>    `nginx-site.live.conf:447-459` 实读为 `proxy_pass http://127.0.0.1:8080`（Go 侧 `r.Static` 出盘）。
>    所以 R1 不是「静态改反代」，而是「在已有反代上按前缀多分几路」，风险面小于本节假设。
> 2. 语义全部通过（36 项断言）：路径映射、租户隔离、`t/50/` 不被 `t/5/` 吞、未配置前缀回落平台盘、
>    状态码分流（404/5xx→`no-store`）、Range/206/HEAD/304、ETag 透传。
> 3. **四个本节没预见的坑**：① location 尾斜杠是隔离边界，去掉就串桶；
>    ② 桶自带 `Cache-Control` 必须 `proxy_hide_header` 藏掉，否则两条并存、CDN 取哪条不确定（已实测复现）；
>    ③ 桶域名在 **reload 时**解析，解析失败 `nginx -t` emerg → 整份配置更新被卡住 ⇒ 生成器必须先做 DNS 预检；
>    ④ HTTPS 桶缺 `proxy_ssl_server_name` 会被拒（实测 421）。
> 4. **阻断项 R1-b**：`takedownMediaCheck` 覆盖写 + `/uploads/` immutable 一年 ⇒ 下架能否生效**完全依赖 Purge**。
>    平台 refresher 打不到客户自有 CDN，所以「开客户自有域名」之前必须先回答「谁负责 Purge 租户自有边缘」。
>    第一阶段口径（公开读桶 + `public_base_url` 留空）下这条不触发，可继续推进。
>
> ✅ **snippet 生成器已落地（同日）**：`backend/internal/nginxsnippet`（渲染 + DNS 预检 + 对账，
> 三条不变量写进包注释与单测）+ `backend/cmd/nginx-snippets`（`render` / `check`）。
> 它只产出 **`location` 段**（不含 map / server），生产上被 include 进 `site.live.conf` 的 `server{}`；
> 三条硬规则：`location ^~ /uploads/t/<cid>/` **带尾斜杠**、`proxy_pass` **不带 URI 部分**
> （键布局只在 `storage.ObjectKeyFor` 定义）、`proxy_hide_header Cache-Control/Expires` 必须在
> `add_header` 之前。**尚未接 `tenant_storage_configs`**（表还没建，属 0044）；
> 渲染/预检/对账三层与数据源无关，建表后只换读取层。


### 3.5 归属与 scope 判定（四个必须钉住的坑）

1. **平台级资源不归租户**：素材采用（`assets.go:155`）、小程序码（`wechat.go:254`）、
   违规下架占位图（`media_check.go:459`）、内容安全送检回写，全部 `ScopePlatform` /
   `company_id=0`，不受 JWT 里的 `companyId` 带跑；`storage_quota.go:76` 的
   「平台自身对象不计量不限配额」继续成立。
2. **下架要带租户**：`takedownMediaCheck` 现在用 `row.ObjectKey` 覆盖却不带租户信息。
   多租户下必须用 `row.CompanyID` 选实例，否则占位图写进平台桶、违规图还在客户桶里在线。
3. **匿名上传必须校验归属**：`uploadCompanyID`（`storage_quota.go:163`）从表单取
   `companyId` 但没校验企业存在 / 状态有效。接 resolver 后必须补校验，不能让伪造的
   `companyId` 把文件写进任意租户桶；`cid=0` 也不许悄悄落到平台桶，要显式拒绝。
4. **配额语义要显式改**：`checkStorageQuota`（`storage_quota.go:74`）按累计字节拦上传。
   租户用自己的桶后成本不在平台，配额应从「限制」改成「计量 / 展示」（或按套餐开关），
   不能继续拦客户自己的空间。这一条不写进设计就会悄悄留下来，变成上线后的怪事。

### 3.6 凭据加密与密钥策略

现成机制在 `internal/pii/pii.go`：`Encrypt` / `Decrypt` / `IsEncrypted` / `SetKey` /
`Reload`，`v1:` 前缀、AES-256-GCM、`base64.StdEncoding`，密钥来自 `PII_ENC_KEY`，
且有启动自检 `UsingPlaceholderKey()`。项目基线是 **写任何 PII 之前必须把密钥定死，
没有 re-key**；租户凭据同理，第一条配置写进去之前就要定死。

**推荐**：抽 `internal/secrets`，用独立的 `STORAGE_ENC_KEY`，不与 PII 共用一把钥匙。
理由：两类数据的保留期、轮换需求、泄露后果都不同；`key_id` 列预留在表里，将来换钥匙
只需按版本解密重加密，不必动 PII。代价是要复制一套启动自检与守门。

**降级路径**：直接复用 `pii.Encrypt/Decrypt` + `PII_ENC_KEY`（省事、已有测试覆盖），
但必须在评审时写进文档：钥匙与 PII 绑定、将来分离要引入 `v2:` 前缀 + 全量重加密。
不能等写进去了才发现。

界面沿用 `CompanyWXApp`（`models.go:467`）口径：凭据只写不读、回掩码、提交才覆盖。
连通性自检（HEAD / PUT 一个探针对象）结果落 `status` / `last_error`；
**错误原文不进审计**——与 `refreshCDN`（`upload.go:121`）同口径，刷新 / 连接错误常带
连接串或签名 token，审计表泄密比不记更糟。

### 3.7 审计与前端对账

- 新增动作常量（`internal/audit/audit.go`）：`company.storage_config.update`、
  `company.storage_config.test`、`tenant_storage.resolve_failed`；
- 同步 `frontend/src/api/audit.ts` 的 `ACTION_LABELS`。`frontend/src/api/audit.spec.ts:41-46`
  会把后端常量与前端标签逐字对账，漏一个必红；
- 配置类动作要能回答「哪个平台超管、替哪家企业、改了什么」：记录操作者与目标
  `company_id`，但**不记凭据、endpoint、连接串、签名 token 或自检错误原文**（§3.6 口径）。
  企业管理员硬调被 403 拦截的请求不产生配置变更，也不写成「操作成功」。

### 3.8 与 P1 边缘缓存的关系

- 五条公开只读接口的 60 秒缓存（`cdnCache(c, 60)`）应在 `Resolve` **之后**发出，
  响应里是平台统一入口 URL；
- `staffId → company_id` 是确定映射，且 `staffId` 进 cache key ⇒ 不会跨租户串号；
- **但将来开放客户自有域名时，cache key 必须含 `Host`**，否则同一 `staffId` 在 A / B
  两个域名下会复用同一份响应 → 跨租户串图。这条写进 P4 守门；
- 改动后必须重跑 CF 行为验收：五条路径 + 错误分支 `BYPASS` + 查询串不折叠。

---

## 4. 分期与验收

| 期 | 交付 | 验收判据 | 守门 |
| --- | --- | --- | --- |
| **P0** | `Ref` / `Resolve` / 统一对象键；写入侧只写 objectKey；读取侧兼容三种历史形态 | 存量数据零裂图；新上传库里是 objectKey；`upload_base_url` 改动不再影响存量 | `TestResolveKeepsLegacyAbsoluteURL`、`TestLegacyUploadsRelativeURL`、`TestNoFullURLWrittenToBusinessColumns` |
| **P1** | `0044` 建表 + 模型进 `model.All()` + 配置界面（掩码 / 自检 / 审计，仅平台超管可见可写） | 装完零业务数据；配置写库为密文；改错能改回来；**企业管理员读写一律 403 且不产生变更** | 迁移只建表守门；`audit.spec.ts` 常量对账；配置路由必须挂 `PlatformAdminMiddleware` 的源码级守门；企业管理员越权集成测试 |
| **P2** | `UploaderResolver` + 5 处注入 + 三处组装同步 + 接入层反代试点 | 单租户新对象落在自己桶；其它租户不受影响；配置错显式失败且不落平台盘 | `TestResolverFailureDoesNotAffectOtherTenants`、`TestTenantUploadNeverFallsBackToPlatformLocal`、`TestAnonymousUploadRejectsForeignCompanyID`、nginx snippet 对账 |
| **P3** | 存量搬迁命令（dry-run / force / rollback，记录 `old_url → object_key → config`） | 可搬过去、可搬回来；对象不删；失败可重跑 | 搬迁幂等 + 反向验证 |
| **P4** | 密钥轮换、审计前端标签、配额口径、数据保留 / 解约后清理策略 | 换钥匙后老配置仍可解；解约租户数据按策略清理 | `key_id` 分支覆盖 |

> **P1 进度（2026-11-13 第五十八轮）**：前两项已落地（迁移 0044 + 模型进 `model.All()`，
> 生成器可读），**配置界面与写接口未做**。已落地的守门见 §3.2「守门清单」；
> 后半段（掩码回显 / 自检按钮 / `PlatformAdminMiddleware` / 越权集成测试）仍是待办。
>
> **P1 完成（2026-11-13 第六十轮）**：P1 五项交付**全部落地**——迁移 0044、模型、
> 配置写接口（四个）、后台配置界面（`TenantStorageDialog.vue`，入口在「企业管理 → 存储」）、
> 掩码回显 + 自检 + `PlatformAdminMiddleware` + 越权集成测试。验收判据逐条对齐：
> 装完零业务数据 ✅（迁移只建表）；配置写库为密文 ✅（`pii.Encrypt`，测试断言 `v1:` 前缀）；
> 改错能改回来 ✅（PUT 覆盖 / DELETE 切回默认）；企业管理员 403 且零变更 ✅
> （`TestTenantStorageTenantAdminForbidden`）。
> 仍未做的是 P1 里的**非交付项**：`public_base_url` 与 `key_id` 的消费方（自有域名 / 密钥轮换），
> 以及私有桶。这三项都不是「漏了」，是明确排到 P2/P4。
>
> **P0 进度（2026-11-13 第五十九轮）**：P0 的验收判据「新上传库里是 objectKey」**已达成**。
> 出参侧 9 条公开 GET + 后台五组出口（第五十三 / 五十七轮）、写入侧 12 个业务表单写入点
> （第五十九轮）全部接好，`upload_base_url` 改动对存量不再有影响。剩余未做的是 P3 存量搬迁。
> 守门名对账：§4 表里写的 `TestResolveKeepsLegacyAbsoluteURL` / `TestLegacyUploadsRelativeURL` /
> `TestNoFullURLWrittenToBusinessColumns` 实际落地为
> `TestResolveRefKeepsLegacyAbsoluteURL` / `TestResolveRefLegacyUploadsRelativeURL`
> （`storage/ref_test.go`）+ `TestResourceRefWriteSiteCounts` / `TestResourceRefWriteNormalizesLocalPath`
> （`handler/resourceref_test.go`）。第三个名字没有直接对应物：它原本设想的「源码级扫描业务列赋值」
> 被拆成两件更可靠的事——写入点数对账（数量变了必红）与端到端落库断言（真的查库）。

> **P3 进度（2026-11-13 第六十四轮）**：P3 拆成「计划 / 执行」两层，**计划层已交付**——
> `storage.PlanNormalize` + `internal/refmigrate` + `cmd/storage-backfill plan`，
> 并在真实 MySQL（44 个迁移 + 13 张表 31 条引用）上取证：15 条待搬、去重 12 个对象、
> 6 条需人工。命令只读（九张表校验和盘点前后逐字节不变）。
> **执行器仍未全部完成**，本节验收判据「可搬过去、可搬回来；对象不删；失败可重跑」原样保留，等执行器落地时逐条校验。
> 详见 §3「✅ 第六十四轮」。
>
> **P3 执行器（2026-11-13 第六十五轮）**：`apply` / `rollback` / `status` 已交付，
> 台账落迁移 `0045_storage_backfill_refs`。**消费同一份计划 JSON，不重扫库**，
> 并逐条做过期检测（库值不是计划的 Raw 就整批停下）。分成两段：
> ① 复制对象（只增不删、临时文件 + rename 原子落盘）；
> ② 改库 + 记台账（**同一个 MySQL 事务**，用 gorm callback 注入记账失败做过原子性反向验证）。
> `writeRef` 是比较并交换，预检到提交之间有人改库也不会被覆盖。
> 真实 MySQL 8.0 取证：dry-run 零写入 → apply 3 条引用 / 1 个对象 → 重跑零改动 →
> rollback 后九张业务表 `CHECKSUM` 与搬迁前逐字节一致、对象仍在；
> 四种拒绝场景（含异常条目的全量计划 / 过期计划 / 篡改目标键 / 目标半截对象）均退出码 2。
> **仍未做**：按 `company_id` 回源真桶的 S3 适配器与「真桶 + 存量对象」灰度，
> 这也是 `TENANT_STORAGE_ROUTE` 翻默认值剩下的前置条件。

**门禁纪律照旧**：`gofmt` → `golangci-lint` → `go test ./...` → `vue-tsc --noEmit`
（需 `NODE_OPTIONS=--max-old-space-size=4096`）→ 全量 Vitest（`--pool=threads`）→
`eslint .` → `vite build --outDir dist-check-*` → nginx-check → `git diff --check`，
串行不并行（并行会 137）。新增接口必进 `runAPIFlow`；每个新守门都要做反向验证
（注入坏改动必红、还原后零漂移）。上线必同步整个 `migrations/`。

---

## 5. 验收清单（不以毫秒衡量）

1. 切到租户自有桶后，新上传对象能在该租户桶里 HEAD 到 200，库里是 `t/<cid>/...`；
2. 把该租户配置改错 → 上传显式 `code=1`，其它租户上传照常，平台本地盘不出现该租户文件；
3. 关掉配置切回平台默认 → 老对象仍可访问（搬迁可逆）；
4. 匿名上传带合法 `companyId` 落到正确租户桶；伪造 / 不存在的 `companyId` 被拒；
5. 素材采用、小程序码、违规下架占位图仍落在平台桶，不进任何租户桶；
6. 小程序 / 管理端不新增合法域名，现有页面图片、视频、`_thumb` 全绿；
7. 配置界面不回显密钥，审计里没有凭据 / 错误原文；
8. 以企业管理员身份访问配置页被路由守卫挡下、菜单里没有入口；直接拿接口地址带该企业管理员
   令牌硬调 CRUD / 自检 / 启停 / 搬迁，全部 `code=403`，配置表零变更；
9. 以平台超管身份替某企业改配置，审计里能查到操作者与目标 `company_id`，且不带密钥原文。

---

## 6. 风险登记

| # | 风险 | 处置 |
| --- | --- | --- |
| **R1** | **nginx 按租户反代尚未验证**：现有 `/uploads/` 是本地静态 + immutable 一年，改反代要动线上接入层，且私有桶要签名 | 第一阶段单租户 + 公开读桶试点，先在测试环境 `nginx -t` + 行为验收跑通再铺开；不通则退回「客户自有域名开关」路线重新评审，**不硬上**。**2026-11-13 已实测：有条件成立**（`tools/nginx-r1-check/` 36 项断言全绿；断言里同时钉住尾斜杠隔离、头叠加、DNS 与 reload、SNI 四条），遗留阻断项 R1-b（下架覆盖 × 租户自有 CDN 的 Purge 归属）见 §3.4 与 `docs/tenant-storage-r1-feasibility.md` |
| R2 | 静默降级被沿用为租户兜底 → 数据主权叙事失效且无人察觉 | 租户解析失败必须显式失败 + 审计；平台级降级仅服务 `company_id=0` |
| R3 | URL 契约双轨：小程序 `assetUrl` 绝对原样返回、管理端 `thumbnailURL` 按 URL 末位插 `_thumb` | `Resolve` 统一出参；`_thumb` 派生规则前后端对账守门 |
| R4 | 平台级资源被 JWT `companyId` 带跑 | scope 显式枚举，四类平台资源单测逐个钉住 |
| R5 | 解约 / 换桶后对象残留在平台桶，或搬迁把旧 URL 洗没了 | P3 保留 `old_url` 记录 + rollback；对象只复制不删除 |
| R6 | CF Purge 按租户误伤 / 漏清；P1 的 60 秒边缘缓存跨租户串号 | 刷新带 `companyID`；开放自有域名前把 `Host` 纳入 cache key 写进守门 |
| R7 | 密钥与 PII 绑定、将来无法独立轮换 | 推荐独立 `STORAGE_ENC_KEY` + `key_id`；若复用必须写进评审结论 |
| R8 | 新 `RegisterX` 漏改 devserver / `handler_test.go` → 静默 404 | 三处同步清单 + 源码级守门 |
| **R9** | **权限误放开**：新配置接口照抄 `RegisterStorage` 的 `AuthMiddleware(db,"admin")`，企业管理员就能改客户云凭据；或前端只藏菜单、后端没拦 | 用户已拍板「只有平台超管代填」（§1.1）；P1 加源码级守门 + 企业管理员 403 集成测试，前端 `platformOnly` 只当体验层 |

---

## 7. 本轮边界

- 只核准了代码事实、写了本设计稿；**未改生产代码、未写迁移、未动线上 nginx**；
- 未跑全量门禁（没有代码改动）；
- 存量业务数据零改动；
- 下一步需要用户确认一个决策后才能进入 P0，见对话。

> ✅ **订正（2026-11-13 14:04）**：该决策点**已闭环**。用户明确：「平台超管代填，这种权限
> 肯定不能放给企业管理员的」。已写进 §1.1、§3.2、§3.7、§4、§5、R9，并同步
> `docs/commercial-readiness.md` 第五十二轮。
>
> 因此 P0 可以开工，且按「零行为变更、可逆」拆两步：
> **P0a** 只落 `backend/internal/storage/ref.go` 的纯函数与守门单测（不接任何 handler，
> 线上行为零变化）；**P0b** 先扫出所有把上传 URL 写库的业务边界并立源码级守门，
> 再谈接入 `NormalizeRef` / `ResolveRef`。
>
> 第一阶段试点口径（单租户 + 公开读桶，§3.4）是设计默认值，进入 P2 前复核，不阻塞 P0。
>
> ✅ **补充（2026-11-13 / 第五十八轮）**：本轮只做「表 + 读」——迁移 0044、模型、
> repository 映射、生成器 `--dsn`、以及全部守门。**没有**写接口、没有后台界面、
> 没有接线到上传链路，因此对现行数据与线上行为零变更（本表无人写入 = 零行 =
> 生成器输出与手写 JSON 时一致）。
>
> 明确的下一步顺序（不可颠倒）：
> 1. ③ 写入侧 `NormalizeRef` 收敛——必须先做到「一个 env 能回滚」；
> 2. 配置写接口 + 后台界面（仅平台超管，挂 `PlatformAdminMiddleware`）；
> 3. `UploaderResolver` + 5 处注入（P2），此时表里的配置才开始真正承载流量；
> 4. 存量回填 P3。
