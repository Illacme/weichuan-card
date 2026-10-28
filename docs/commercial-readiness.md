# 商用发布就绪度评估

> 本文回答的是「**能不能作为商品交付、能不能合规上架、出事能不能兜住**」，
> 与 `docs/launch-checklist.md`（部署配置逐项核对）不是同一张表：
> 那份管「装上去跑不跑得起来」，这份管「跑起来之后能不能卖、会不会被下架、赔不赔得起」。
>
> **评估基准**：2026-10-26 第二次复扫 + **同日修复实施**（29 个迁移 / 后端 22 个包 / 小程序 19 个 spec）。
> 本轮在首版（`A1` 内容安全、`B1` 授权、`B2` 许可、`C2` 备份恢复已落地）基础上，
> 由三个只读扫描 agent + 主代理逐文件核实，**修正了一处误判（默认凭据已被启动硬门槛拦截），并新增两处 P0、两处 P1**。
> 首版说"缺什么"的，本轮逐条回到代码确认"到底落地没有"。
>
> **本轮末尾已按本报告实施修复**：A4 / A5 / A6 / A7 / A1-b / B1-a 六项全部落地并有测试。
> 实施过程中**回到微信官方文档核实了本报告初稿的三处事实错误**，见下方「实施记录与事实订正」。

## 结论摘要

**代码完成度不构成瓶颈。** 鉴权、多租户、审计、上传魔数嗅探、限流跨副本、迁移幂等、
备份/恢复/演练、最小告警、前端报错上报、内容安全（文本+图片）、授权地基+强制层、
商业专有许可——都已落地，且大部分有对应测试。**这些不再讨论**（见文末「已就位」）。

真正的缺口集中在三层，且**都不在「功能有没有」这一层**：

| 层 | 性质 | 不解决会怎样 |
|---|---|---|
| 合规 | 微信隐私授权 + PIPL 权利入口 + 小程序隐私政策页 | **提审被驳回**，或上架后被举报封能力 |
| 授权 | 占位公钥未换 / 席位上限被绕过 | 私有部署无法激活，或客户超额不付费 |
| 运维 | 可观测 / 可恢复 / 密钥治理 | 出事时「不知道出事了」或「知道也救不回来」 |

**本轮新增的发布阻断级（P0）两条，均已修复**：
1. **微信隐私授权完全未实现**（代码级）—— 已落地：新增 `miniprogram/utils/privacy.ts`（授权状态查询与编排）+ `components/privacy-consent`（自绘授权弹窗，同意按钮用官方 `open-type="agreePrivacyAuthorization"`），三个调隐私接口的页面接入，`app.json` 补 `__usePrivacyCheck__: true`，另加隐私政策页与 apply 表单明示同意勾选。
2. **授权验签公钥仍是占位演示密钥**（`backend/internal/license/embed.go:16`）—— 已落地：公钥改为**环境变量 `LICENSE_PUB` 可覆盖**（不必改代码重编译），并新增 `IsPlaceholderPublicKey()` 自检——私有部署仍用占位密钥时**拒绝启动**并给出换钥指引（集中/平台模式不受影响，保证开源仓库「clone 就能跑」）。

> 实施时发现本报告初稿把接口名与隐私接口范围写错了，详见文末「实施记录与事实订正」。

---

## 一、合规阻断（P0：不做就上不了架 / 激活不了）

| # | 缺口 | 现状（证据） | 为什么卡 | 建议动作 |
|---|---|---|---|---|
| A1 | **UGC 内容安全检测（已落地）** | 文本：`/wxa/msg_sec_check` v2 接在 `POST /api/apply/open` 与 `PUT /api/employee/me`；图片：`/wxa/media_check_async` 异步送检 → 消息推送回调落库 `media_check`（迁移 `0028`）→ 后台「内容安全」页一键下架。`internal/handler/upload.go` 仍保留扩展名白名单 + 魔数嗅探（防木马，已不再是唯一防线） | 员工头像、名片修改（文字+图片）都是 UGC；微信强制要求内容安全能力 | **已落地**。图片检测需运营在微信公众平台配置「消息推送」URL+Token+AESKey（系统设置→微信已加这两项并展示推送地址）；未配置则图片自动跳过检测（fail-open，不阻断上传） |
| A1-b | **匿名上传端点绕过图片内容安全（✅ 已修复）** | **修复前**：`upload.go:30` 注册 `POST /api/apply/open/avatar`（访客申请头像，**无鉴权、无限流**）；`media_check.go` 对匿名端点 `openid` 为空，`mediachk.Plan` 以 `no_openid` 跳过送检。**修复后**：① 该端点叠按 IP 固定窗口限流（`uploadLimiter`，默认 20/分，桶键 `upload:` 前缀避免与登录/刷新/埋点挤进同一行计数，跨副本走 `rate_limit_hits` 共享计数）；② 客户端上传时随请求带上 `openid`（`wx.login` 静默换取，无需授权），服务端 `formValue(c,"openid")` 取到后**照常送检**（`source=apply`），端到端测试验证「真的发起送检并落 pending 记录」 | 修复前：攻击者可在公开端点无限传图，且完全不进内容安全筛查——更关键的是**这张图上传成功即返回永久 URL（立刻公开可访问）**，而它最终会写进员工头像，等于「违规图在人工审核之前就已挂在网上」 | 限流阈值可在后台「系统设置 → 登录与令牌 → 匿名上传频率限制」调整；**仍需 MP 后台配好《小程序用户隐私保护指引》并声明相册/照片类型**，否则平台侧仍会拦 |
| A2 | **个人信息权利无自助入口（✅ 已修复）** | **修复前**：小程序只有「退出登录」（`pages/employee/me/me.wxml` 无「我的数据/注销」），无对应页面；后台只有统计埋点 CSV 导出，无按员工导出个人信息。**修复后**：① 小程序新增 `pages/mydata/mydata`——调 `GET /api/employee/me` 把系统持有的本人 PII 带标签列出（空值过滤、status 转中文、头像拼绝对）+「复制全部」走 `ensurePrivacy` 授权后写剪贴板（PIPL 查阅复制权）；② 「我的」页加「我的数据」入口（`goMydata`）+「注销账号」入口（`cancelAccount`，两次强确认红字按钮，不可逆）；③ 后端新增 `POST /api/employee/me/cancel`（员工本人注销，事务删除 `Employee`+`CardModifyApply`+`StatEvent` 三表，仅删本人；删除后 `TokenVersion` 查不到员工 → 会话立即被中间件锁死）；④ 后端新增 `GET /api/admin/employees/:id/export`（按员工导出 PII，受 `scopeCond` 租户隔离，审计留痕 `employee.export`）；⑤ 后台 `Employees.vue` 加「导出」按钮（`responseType:blob` 下载 `employee-<id>.json`，不含 `json:"-"` 的密码哈希等） | PIPL 查阅复制权、删除权、注销权此前靠人工找 DBA 执行 | 已落地。配套测试：后端 `pipl_rights_test.go`（注销删本人且不动别人/连带清统计/审计留痕/未登录被拒/后台导出含 PII 且跨租户被拒）+ 小程序 `mydata.spec.ts`（渲染过滤/授权复制/被拒不复制）+ `employee_me.spec.ts` 注销分支（两次确认才发请求/首次取消不发/失败不清本地态） |
| A3 | **小程序备案（已进清单）** | `docs/deploy-runbook.md` §0 前置条件已加「小程序备案」行；`docs/launch-checklist.md` 阻断级已列入 | 2024 年起小程序本身需完成备案（与域名 ICP 备案相互独立），未备案不能发布 | **已落地**：runbook §0 + launch-checklist 阻断级（preflight 不覆盖，需人工确认备案号） |
| **A4** | **微信隐私授权接口完全未实现（✅ 已修复）** | **修复前**：`app.json` 无 `__usePrivacyCheck__`、全仓零命中隐私授权接口。**修复后**：① `app.json` 补 `"__usePrivacyCheck__": true`；② 新增 `miniprogram/utils/privacy.ts`——`queryPrivacy()` 查 `wx.getPrivacySetting`、`needConsent()` 判定是否需授权、`requestConsent()/settle()` 编排等待、`ensurePrivacy(page)` 供页面统一调用；③ 新增 `components/privacy-consent` 自绘授权弹窗，**同意按钮用官方 `open-type="agreePrivacyAuthorization"`**（基础库会校验按钮确被点击过，普通按钮无效）；④ 三个调隐私接口的页面接入：`apply/open`（`chooseMedia`/`getUserProfile`）、`employee/edit`（`chooseMedia`）、`poster`（`chooseMedia` + `saveImageToPhotosAlbum`） | 未实现时：真机隐私接口直接 `fail: require privacy authorize`（昵称采集与头像上传不可用）、提审驳回 | 官方要求「注册监听后**必须**调 resolve，否则原接口永久挂起」——本实现走 `getPrivacySetting` **主动查询**路线且**不注册**监听，漏网调用由微信官方弹窗兜底，不会出现「点了没反应」；`openid` 虽不在隐私接口表内，但属需在 MP 后台声明的「用户标识信息」 |
| **A5** | **小程序内无隐私政策页 / 协议入口（✅ 已修复）** | **修复前**：全小程序无任何隐私政策页面。**修复后**：新增 `pages/privacy/privacy`（六节完整正文：收集哪些信息 / 如何使用 / 如何保护 / 你的权利 / 联系我们 / 微信平台隐私指引），`app.json` 注册路由；「我的」页**未登录与已登录两态都放了入口**（访客也要能查阅）；页内可一键打开微信侧配置的《小程序用户隐私保护指引》原文 | 微信审核要求用户可在应用内查阅隐私保护指引，缺此即不通过 | 已落地。政策正文口径与产品真实行为一致（收集 openid / 填写的联系方式 / 上传图片 / 访问埋点；存储在部署方自有服务器；权利含查阅复制更正删除注销撤回同意） |
| **A6** | **apply/open 采集个人信息无告知同意（✅ 已修复）** | **修复前**：`open.ts` 收集 `name/phone/phone2/title/landline/email/avatar/nickname` + 静默 `openid`，`onSubmit` 仅校验姓名手机即提交，无任何告知与同意。**修复后**：表单末尾加**自定义勾选框 + 告知文案**（「我已阅读并同意《隐私政策》，同意本小程序收集上述信息用于生成与展示我的电子名片」，其中《隐私政策》可点跳隐私政策页）；`onSubmit` 前校验 `agreed`，**未勾选直接拒绝提交并提示**（该校验必须在端上做——后端拿不到「用户是否真的看过并勾了」这个事实） | PIPL「告知—同意」原则的直接违反点，也是微信审核高频驳回项（与 A4 弹窗互补：弹窗是平台级前置，此处是表单级明示同意） | 已落地。勾选状态独立于 `form`（不进提交载荷），配 5 条守护用例（未勾选拒提交 / 勾选可提交 / 可来回切换 / 跳隐私政策页） |
| **A7** | **授权验签公钥仍为占位演示密钥（✅ 已修复）** | **修复前**：`embed.go:16` 是编译期常量，无任何覆盖途径。**修复后**：① 公钥支持**环境变量 `LICENSE_PUB` 覆盖**（base64，优先于内嵌值）——发布时注入真公钥**不必改代码重新编译**；② 新增 `license.IsPlaceholderPublicKey()` 自检；③ `cmd/server/main.go` 启动时**私有部署仍用占位密钥即拒绝启动**并打印换钥指引（含 `license keygen` 用法与「私钥绝不入仓」提醒）；④ 环境变量写错（非法 base64 / 长度不对）时**不静默退回内嵌公钥**，而是让自检发现（否则「以为用的是环境变量那把、实际是内嵌那把」是最难查的一类错） | 占位密钥的配对私钥写在签发工具注释里 → 任何拿到二进制的人都能伪造「永久 / 无限席位」的许可证 | 已落地。**只在私有部署模式拦**：集中/平台模式不验本地许可证文件，而开源仓库需要「clone 就能跑」，一刀切拦会让本地开发无从下手。自检排在 `license.Verify` **之前**——否则用户只会看到「授权校验失败」而不知道真正原因是「公钥没换」 |

> A1 是三者中唯一"写代码能解决"的，已做完。A2/A4/A5/A6 一半流程一半产品功能；A7 是构建期一道换钥工序。

---

## 二、经营闭环（P1：不影响上架，影响能不能收钱）

| # | 缺口 | 现状（证据） | 影响 | 建议动作 |
|---|---|---|---|---|
| B1 | **授权与强制层 + 租户解析 + 多租户授权已落地（Phase 1+2+3+4）** | 统一 `Entitlement` + `LicenseResolver`（FileResolver 验 Ed25519 签名 / DBResolver 读系统设置）+ 启动校验（私有验签失败/过期 `log.Fatalf` 拒绝启动）；运行时强制经 `handler.EnforceLicense`（`handler.New` 统一注册）：过期只读（`{code:2}`）、席位上限（`{code:403}`）、功能开关 `RequireFeature`（`{code:403}`）；`tenant_license` 表 + `ResolveFor` 按租户解析 | 私有化一单一签可绕过（合同约束）；SaaS 售卖仍缺"到期自动停服"与自助续订入口 | **已落地**。待办：见 B1-a（席位上限被绕过）、Phase 5 B2 许可决策（已落地） |
| **B1-a** | **席位上限被「申请审批」路径绕过（✅ 已修复）** | **修复前**：`EnforceLicense` 仅拦截 `c.FullPath() == "/api/admin/employees"`；但 `POST /api/admin/apply/open/:id/approve` 经 `db.Create(&emp)` 直接建员工，**不经过席位检查**——两条建号路径只卡了一条。**修复后**：① 席位判定抽成共享函数 `seatExceeded(db, ent, cid)`；② 中间件与审批路径**都调用**它（审批路径按**申请所属租户** `a.CompanyID` 判，不是操作者所在租户）；③ 顺带修掉两处**未判 error 的 `db.Create`/`db.Save`**（此前建号失败仍返回 `code:0` + 一个不存在的 staffId + 永远登不进去的初始密码，且申请单被标成 approved） | 修复前：租户管理员可反复审批开通申请，绕过授权席位上限 → 客户超额不付费 | 已落地。配套测试：席位已满时审批被拒（且**库里不能留下员工记录**）、席位未满时审批正常通过 |
| B2 | **许可已改为商业专有（Phase 5 已落地）** | `LICENSE` 由 MIT 改为**专有许可 + 客户端有限权利**（版权 Illacme）：禁部署/修改/逆向/转售/对外 SaaS/移除声明/**规避内置授权校验**；第九条保留历史 MIT 版本不可撤销权利。第三方组件清单见 `NOTICES.md` + `docs/third-party-licenses.md`（Go 44 / npm 128，无 GPL 类）。许可签发私钥**未进仓库**（已核实） | 原 MIT 风险已消除 | **已落地**，收尾完成 |
| B3 | **按租户存储用量 + 配额（✅ 已落地）** | **修复前**：无 quota 相关代码，上传只受单文件上限 `upload_max_mb`。**修复后**：① 新表 `storage_usage`（`company_id` 主键 + `bytes`/`files`/`quota_mb`，迁移 `0032`），上传时 `clause.OnConflict` 原子累加；② 上传前 `checkStorageQuota` 拦截（三个入口：后台/员工上传、匿名申请头像、对象存储直传 presign）；③ 取值链**租户覆盖 → 全局 `storage_quota_mb` → 0 不限**；④ `GET /api/admin/storage`（本租户用量）+ `PUT /api/admin/storage/quota`（平台超管改覆盖）；⑤ 企业管理列表带 `storageBytes`、新增「授权与配额」弹窗 | 单租户可无限上传；按量计费/成本封顶无从谈起 | **已落地**。口径：**自功能启用起累计**，历史文件不回溯（对象存储无租户元数据、本地 `/uploads` 是扁平目录）；覆盖写不扣减；直传签名成功即记（否则完全统计不到）；查询失败**不拦上传**（宁可少算一次） |
| B4 | **平台模式默认试用授权（✅ 已落地）** | **修复前**：`tenant_license` 表为空时 `ResolveFor` 回落全局设置（不限席位/不过期）→ SaaS 新租户**零约束**。**修复后**：① 设置新增 `license_trial_enabled/days/seats`（缺省 14 天 / 5 席）；② 新增 `license.TrialSpec` + `CreateTrialLicense`/`SetTenantLicense`；③ **开通企业时事务内落一条试用授权行**；④ `GET/PUT /api/admin/companies/:id/license` 供运营转正/延期/改席位；⑤ 企业列表带授权状态，「授权与配额」弹窗四档提示（未配置/已过期/试用中/正式） | "SaaS 收不回钱"在机制层未闭合 | **已落地**。存量企业**刻意不补行**（升级不能让已付费客户突然变只读）；试用天数改动只影响「以后新开的」（不按 `company.created_at` 现算，否则改天数会让所有在用租户的到期日一起漂移）；到期日按 `YYYY-MM-DD → 当天 23:59:59` 落库 |

---

## 三、运维兜底（P2：出事那天才发现）

| # | 缺口 | 现状（证据） | 影响 | 建议动作 |
|---|---|---|---|---|
| C1 | **前端报错已上报（已落地）** | `frontend/src/main.ts` 三处捕获 → 自建 `POST /api/client-error`（无鉴权公开端点，每 IP 限流 60/分 + 64KB + DEV 不落库 + `X-Request-Id` 关联） | 用户白屏/失灵时服务端此前收不到信号 | **已落地**：代码与单测就位 |
| C2 | **备份/恢复/演练脚本已落地 + 演练已实跑留档（✅ 已闭环）** | `backup.sh`（SQLite `VACUUM INTO` 在线快照 + MySQL `mysqldump --single-transaction` + 轮转 + 体检）；`restore.sh`（恢复前自动备份 + WAL 清理 + 回滚指引）；`restore_drill.sh`（不碰生产的演练，未真正校验不得报 PASS，结尾直接打印可粘贴的留档正文）。**已实跑**：2026-10-27 在 SQLite 与 MySQL 两种形态各跑一次，均通过，记录存档在 `docs/drill-records/` | 没演练过的备份不算备份 | **已闭环**。剩余是节奏问题：每季度一次（升级迁移 / 改备份脚本 / 换部署形态后补跑），记录继续存 `docs/drill-records/YYYY-MM-DD-<形态>.md` |
| C3 | **审计日志保留期（✅ 已落地）** | **修复前**：`auditlog.go` 只 `Record`，无归档/清理。**修复后**：① 设置新增 `audit_retention_days`（Group security，**默认 0 = 永不自动清理**）；② 新增 `internal/audit/purge.go`——`PurgeAuditLogs` 分批删除（每批 2000、单 tick 上限 10 万，避免大事务长时间持锁）、`StartAuditLogPurge` 定期跑（间隔 `AUDIT_PURGE_MINUTES`，默认 12h，启动时先清一次）；③ 保留天数**每次重新读设置**（界面改完即时生效），并回落环境变量 `AUDIT_RETENTION_DAYS`；④ 清理动作本身记一条 `audit.purge` 审计（新增 `ActorSystem` 操作者类型，前端显示「系统」），失败也留痕但**不落错误文本**（DB 错误可能带连接串） | 两三年后单表拖慢查询，且长期留存 PII 本身是合规负担 | **已落地**。默认 0 是刻意的：删审计不可逆，升级当天清掉存量＝把本来就有的证据变成再也拿不回来（与 B3/B4 同一条「升级不得突变存量」纪律）。要合规留存策略由运维显式填 180/365 |
| C4 | **不能水平扩展** | 小程序码/access_token 进程内存缓存（`wechat.go:50-53`）；`license.Current()` 亦为进程内存 | 多副本各算各的 token/授权态，限流按实例分桶 | 商用多副本前必须先上 Redis（token + 限流桶 + 授权态 + 缓存） |
| C5 | **数据静态加密（✅ 已落地）** | **修复前**：PII 明文落库。**修复后**：新增 `internal/pii` 包（AES-256-GCM + `v1:` 版本前缀 + 随机 nonce；盲索引 `HMAC-SHA256` 密钥由主密钥域分离派生）。范围**全量**：`employees`（姓名/手机/手机2/邮箱/昵称）、`open_applies`（同五列）、`card_modify_applies.snapshot` 整段。模型三段式（明文 `gorm:"-"` + 密文列沿用原列名 + `*_bidx`），配 `BeforeCreate` 加密 / `AfterFind` 解密钩子；`BackfillPII` 幂等回填（空跳过 / 明文加密 / 密文补索引）；迁移 `0030`/`0031`（含 `snapshot` 改 LONGTEXT——密文是 base64 不是合法 JSON）。登录按 `phone_bidx` 等值；后台搜索对加密列改盲索引等值（`title/landline/openid` 仍 LIKE） | 备份盘/数据库文件泄露 = 大规模个人信息泄露 | **已落地**。生产必须设 `PII_ENC_KEY`（base64 32 字节）；未设时回落内置 dev 密钥并打 `[SECURITY][WARN]`（不拒启，保证 clone 即可跑）。已知取舍：加密字段的后台搜索由模糊收窄为精确。备份文件本身加密仍未做 |
| C6 | **CI 密钥扫描（✅ 已落地）** | **修复前**：五 job 无任何密钥扫描。**修复后**：新增 `.gitleaks.toml`（`[extend] useDefault = true` + `[allowlist].regexes` 精确抑制测试假值，**单表不是 `[[allowlist]]`**）+ `ci.yml` 新增 `secrets-scan` job（`fetch-depth: 0` 才能扫历史提交）。本地 8.30.1 实测：无白名单时 6 处命中全为 `generic-api-key` 假值（`middleware-test-key-*`、伪 AppSecret、`from-env-key-*`、文档占位 JWT），加白名单后 **0 leaks** | 密钥一旦进 git 历史极难彻底清除 | **已落地**。仍须遵守「PR 禁止提交真实密钥」；测试内嵌微信上传私钥为假 fixture（`mp_release_upload_test.go:39` `MII-fake-global-key`） |
| C7 | **EXIF/GPS 剥离（✅ 已落地；缩略图仍缺）** | **修复前**：`upload.go` 全文无 `exif`/`Strip`（Grep 零命中），用户上传原样落盘。**修复后**：`internal/storage/image.go` 新增 `ProcessImage(ext, data)`——jpg/jpeg/png **解码再编码**即剥离全部元数据（含 GPS）；gif/webp/mp4 透传；解码失败 fail-open（不因坏图阻断上传）。**缩略图仍未做**（见 E4） | 用户照片带 GPS = PIPL「行踪轨迹/精确定位」敏感个人信息 | **EXIF 部分已落地**；缩略图/压缩见 E4 |
| C8 | **集中日志 + PITR（✅ 已落地）** | **修复前**：日志仅后端本地文件、纯文本；`backup.sh` 不含 binlog（RPO=日备）；`/metrics` 无来源限制。**修复后**：① 新增 `internal/logx`——`LOG_FORMAT=json` 时**包装标准 log 的输出目标**，既有 `log.Printf` 一行不用改就变成 JSONL（级别从既有的 `[WARN]` 等方括号标签推断，认不出按 info、绝不丢行；连续标签取最严重的一个，避免 `[MIGRATE][FATAL]` 被当成 info）；访问日志字段化（`method/path/status/latency_ms/request_id`），文本模式下格式一字不改；② compose 三个服务都配了 `json-file` 日志轮转（10m×7，防止容器日志撑爆磁盘），另附 `deploy/promtail/config.yml`（Loki）与 `deploy/logrotate/weichuan.conf`（非容器部署）；③ **PITR**：compose 的 mysqld 默认带 `--log-bin --server-id=1 --binlog-format=ROW`，新增 `scripts/backup_binlog.sh`（FLUSH 滚动 → 逐个复制并 `gzip -t` 校验 → 校验全过才 `PURGE`，顺序不能换；`log_bin=OFF` 时报错退出而不是空跑成功）；④ `/metrics` 加第二道门 `METRICS_ALLOW_CIDR`（**写错即整体拒绝**，fail-closed） | 多副本/跨机排障受限；误删只能回到日备点 | **已落地**。已知取舍：SQLite 形态无 PITR（没有 binlog 这类增量日志），对数据零丢失有要求的客户应走 MySQL；binlog 回放（PITR 的后半段）尚未做演练——在那之前可承诺的是「恢复到最近一次备份」 |

---

## 四、工程门禁（P3：不是缺陷，是「以后不欠债」的保障）

| # | 缺口 | 现状（证据） | 建议动作 |
|---|---|---|---|
| D1 | **lint 门禁（✅ 已落地）** | 后端新增 `backend/.golangci.yml`（v2 配置，跟着 Go module 走）：`errcheck / govet / ineffassign / staticcheck / unused` 五个——**只留「报出来一定是问题」的静默失败类，不留风格类**；CI 按官方安装脚本取固定版本 v2.15.0（不用 action：action 大版本与 linter 大版本是两套编号）。前端新增 `eslint.config.mjs`（ESLint 9 flat）：未使用变量/导入 + Vue 模板硬错误 + 少数 JS 陷阱；`vue-tsc` 仍是类型门禁，lint 不重复做类型检查。**存量已清零**：29 处后端（20 处 `defer Close()` / 清理路径的显式 `_ =` + 说明、2 处 ShouldBindJSON 按「理由可选」显式丢弃、1 处 ineffassign、1 处未使用的测试 helper）+ 25 处前端（真未使用的导入/变量）全部修掉，现在两边都是 0 issue | 静默失败没人发现（`errcheck` 最典型：丢弃的 error 编译过、vet 也过） | **已落地**。刻意不启用：gofmt（存量文件有 Go 版本导致的注释排版差异，一开就是上百处噪音）、`no-explicit-any`（测试桩里大量 any，逼着加 disable 反而淹没有效信号）、`vue/multi-word-component-names`（组件名与路由一一对应） |
| D2 | **依赖漏洞扫描（✅ 已落地，先非阻塞）** | CI 新增 `vuln` job：`govulncheck ./...`（按**实际调用到的函数**判定，比单纯比对版本准）+ `npm audit --omit=dev`（只看运行时依赖，构建链告警量极大且不进产物）。job 级 `continue-on-error: true` | 依赖链上有 CVE 没人知道 | **已落地**。为什么先不当门禁：相当一部分 CVE 在本项目里根本调不到那条路径，修它要升大版本；一上来就红灯＝红灯常态化＝没人看。现在先让风险**看得见**，基线清干净再摘掉 `continue-on-error` | 
| D3 | **CI 构建镜像（✅ 已落地）** | CI 新增 `images` job：用 buildx 分别 build `backend/` 与 `frontend/` 两个 Dockerfile（`push: false`，带 gha 缓存） | `go build` / `npm run build` 绿只代表代码能编译，Dockerfile 里的 COPY 路径、多阶段产物名、运行镜像缺 tzdata/wget 这类问题只有真 build 才暴露——全是「本地能跑、客户装不上」 | **已落地** | 
| D4 | **端到端冒烟（✅ 已落地，非阻塞）** | 新增 `scripts/e2e_smoke.sh`：真起一个进程（临时目录 + 临时端口，不碰现有数据），走完 **安装向导建库 → 登录拿 httpOnly Cookie → 建企业 → 配企业信息 → 建员工 → 小程序侧 `/api/card` 读回刚写的数据**，七步逐步 PASS/FAIL 并带退出码。CI 新增 `e2e-smoke` job（非阻塞） | 单测与接口测试**结构上覆盖不到**「真进程 + 自重启 + 两端衔接」这一层 | **已落地**。本机实测全绿（七步全 PASS）。为什么非阻塞：它依赖起真实进程与端口可用，runner 上偶发环境性抖动，先积累稳定性数据再转门禁 | 

---

## 五、体验细节（P4：成本低、感知强）

| # | 缺口 | 现状（证据） | 建议动作 |
|---|---|---|---|
| E1 | **导航栏标题写死客户名（✅ 已落地）** | **修复前**：`app.json` 全局 `navigationBarTitleText` 写死演示租户名；`pages/index/index.wxml` 自绘顶栏、`index.ts` 分享标题的兜底文案也是同一个客户名；`brand_name` 已缓存但无人用于标题。**修复后**：① 全局标题改产品名「微传名片」；② 新增 `utils/brand.ts`——`resolveBrandTitle` 按「当前企业 → `brand_name` → 产品名」解析，`applyBrandTitle` 设导航栏；③ 海报页拿到 `card.company` 后按企业设标题；④ 顺手给 4 个继承全局标题的页面补功能性标题（编辑名片 / 登录 / 案例详情 / 服务详情）；⑤ 顶栏与分享标题兜底改为留白 / 产品名 | **多租户下客户看到别人家公司名**——提审截图里一眼可见的「没改干净」 | **已落地**。守门：`tests/brand.spec.ts` 扫全目录断言不含演示租户名，并比对 `app.json` 全局标题 === `FALLBACK_BRAND` |
| E2 | **源码残留开发者局域网 IP（✅ 已落地）** | **修复前**：`utils/config.ts` `DEFAULT_BASE_URL = 'http://192.168.31.224:8080'`（明文 http + 私有网段），而种子基址是拉取后台配置的**唯一入口**。**修复后**：① 常量**默认为空**，源码不含任何地址；② 新增 `hasBaseUrl()`，`fetchPublicSettings` 与 `app.ts onLaunch` 在基址为空时**不发请求**（否则拼出非法 URL 白等 5s 超时）；③ `fetchPublicSettings` 归一末尾斜杠（避免 `//api/` 被 nginx 前缀匹配漏掉）；④ `preflight.sh` 第 3 节把「空值」也判 FAIL（原先空值只 `skip`，等于没拦），保留 `PREFLIGHT_ALLOW_PRIVATE=1` 放行本机联调 | 别人部署后拿**别人家的内网地址**当首连地址，必然超时且后台配基址也救不回来，只能重新打包提审 | **已落地**。守门：`tests/config.spec.ts`（默认必须空 / 若填只允许 https 或回环 / 空基址不发请求），反向验证时「空基址仍发请求」那条实测耗时 5.0s——正好是白等的那个超时 |
| E3 | nginx 未开压缩、静态资源无强缓存 | `frontend/nginx.conf` 只有安全头与反代，无 `gzip on` / `expires` | 开 gzip + 给 `assets/` 加 `Cache-Control: immutable`（文件名带 hash） |
| E4 | 用户上传图片无压缩/无缩略图 | 内置素材缩略图离线生成，用户上传没有；EXIF 已由 C7 剥离 | 上传时按长边限制重编码（同时完成压缩与缩略图），列表页改用缩略图 URL |

---

## 修正说明：默认凭据风险（首版误判纠正）

首版把"硬编码默认口令/默认 JWT 密钥"列为 P0 悬念。本轮由主代理逐文件核实，**该风险已被启动硬门槛拦截，不再是开放 P0**：

- `backend/internal/config/config.go:13-18`：`DefaultJWTKey="change-me"`、`DefaultAdminPass="admin123456"`，但二者都原样写在公开文档里（已知凭据）。
- `config.go:200-211` `InsecureGates()`：当 `JWT_KEY==默认` 或 `AdminPass==默认`（或长度不足）时返回硬门槛项。
- `cmd/server/main.go:82-93`：**启动时若 `InsecureGates` 非空且 `ALLOW_INSECURE!=1`，逐条打印 `[SECURITY][FATAL]` 并 `log.Fatal` 拒绝启动**（自检方式：改一个环境变量重启，不会弄丢"改回来的入口"）。
- `cmd/server/main.go:172-174`：`license.Verify` 失败同样 `log.Fatalf` 拒绝启动。

**剩余风险（请知悉，非 P0）**：
1. `ALLOW_INSECURE=1` 是逃生门（注释"仅本地联调用"），若生产误设即绕过全部门槛——发布 runbook 应明确禁止生产设此值。
2. `InsecureWarnings()`（如口令长度<8）只提示不拦，依赖运维判断。
3. `setup.Unconfigured()` 分支（`main.go:69-76`）在未配 `DB_DSN` 时只起只读引导页，不加载业务——这是预期行为，不是漏洞。

---

## 建议的推进顺序（本轮修订）

1. **A4 微信隐私授权（P0，代码级，必须最先做）** —— `app.json` 加 `__usePrivacyCheck__:true` + `app.ts` 注册 `onNeedPrivacyAuthorize` + 调用前 `requirePrivacyAuthorize` + 隐私政策页 + apply 表单同意勾选 + MP 后台补全《隐私保护指引》。
2. **A7 替换占位授权公钥（P0）** —— 发布构建前把 `embed.go:16` 换成自有公钥 + runbook 加比对断言。
3. **A5/A6 隐私政策页 + apply 明示同意（P0/P1）**。
4. **A1-b / B1-a（P1）** —— 匿名上传加限流+送检；席位上限抽到共享函数覆盖审批路径。
5. **A2 PIPL 自助权利入口（P1）** —— 小程序「我的数据」+「注销」+ 后台按员工导出。
6. ~~C7 上传剥 EXIF、C6 CI 加 gitleaks、C5 静态加密~~ —— **三项已于 2026-10-26 第三轮全部落地**（C5 分两阶段：Employee → 申请单/快照）。
7. ~~B3 存储配额、B4 平台模式默认试用授权~~ —— **两项已于 2026-10-26 第五轮全部落地**（含后台「授权与配额」弹窗）。经营闭环至此无开放项。
8. ~~C2 跑一次恢复演练并留档；D1–D4 工程门禁~~ —— **已于 2026-10-27 第六轮全部落地**（C2 两种形态实跑留档、C3 审计保留期、C8 集中日志 + PITR、D1 lint、D2 漏洞扫描、D3 镜像构建、D4 端到端冒烟）。运维兜底与工程门禁至此无开放项（C4 水平扩展仍开放，属架构演进而非缺陷）。

> 已落地的 A1、B1、B2、C1、C2（代码侧）不在推进顺序内，见文末「已就位」。

---

## 已就位、不需要重复讨论的项

避免后人把已经做过的事再提一遍：

- 部署配置逐项核对 → `docs/launch-checklist.md` + `scripts/preflight.sh`
- 上线后最小告警（healthz / 5xx / 备份新鲜度 / 外部拨测/证书） → `scripts/alert_check.sh`
- 备份 / 恢复 / 演练（SQLite 与 MySQL 两条路径） → `scripts/backup.sh` · `restore.sh` · `restore_drill.sh`（`C2` 代码侧完成，流程待闭环）
- 操作审计、双令牌吊销、上传魔数嗅探、登录限流、可信反代 → 见 `SECURITY.md` 与 `README.md`「安全与已知限制」
- 数据库迁移自动补齐与回滚 → `deploy-runbook.md` §4 / §9
- 上线合规前置（域名 ICP 备案 + 小程序备案）：`docs/deploy-runbook.md` §0 + `docs/launch-checklist.md` 阻断级（小程序备案 preflight 不覆盖，需人工确认备案号）
- 前端报错上报（无鉴权 `POST /api/client-error`，每 IP 限流 + 64KB + 日志 `[CLIENT-ERROR]` 关联 `X-Request-Id`）→ `frontend/src/main.ts` · `frontend/src/api/clientError.ts` · `backend/internal/handler/clienterror.go`
- 内容安全（A1）：文本 `MsgSecCheck` v2 + 图片 `media_check_async` + 消息推送回调落库 + 后台一键下架
- 授权地基 + 强制层 + 多租户授权：`internal/license`（`Entitlement`/`LicenseResolver`/Ed25519 验签/启动校验/运行时 `EnforceLicense`/`RequireFeature`/`tenant_license` 表/`cmd/license` 签发工具）；**启动硬门槛** `InsecureGates` + `license.Verify` 已接入 `cmd/server` boot 序列（`main.go:82` / `:172`）
- 许可策略（B2）：`LICENSE` 商业专有许可 + `NOTICES.md` + `docs/third-party-licenses.md`；签发私钥文件加载、未进仓库
- **默认凭据已非 P0**：`InsecureGates` 拒绝以默认 `JWT_KEY`/`ADMIN_PASS` 启动（见「修正说明」）
- **测试内嵌微信上传私钥为假 fixture**：`mp_release_upload_test.go:39` 含 `MII-fake-global-key`，非真实泄露（见 C6）

---

## 实施记录与事实订正（2026-10-26 修复实施）

本轮末尾按本报告实施了 A4 / A5 / A6 / A7 / A1-b / B1-a 六项修复。
实施前**回到微信官方文档逐条核实**接口契约——这一步纠正了本报告初稿的三处事实错误，
也直接决定了实现路线。此处如实记录，避免后人按错误的前提重复判断。

### 订正一：接口名是 `wx.onNeedPrivacyAuthorization`（本报告初稿写错）

初稿写的是 `wx.onNeedPrivacyAuthorize`（少一个 `ion`）。官方文档实际为
`wx.onNeedPrivacyAuthorization`，基础库 **2.32.3** 起支持。

### 订正二：注册监听后**必须**调用 `resolve`，否则原接口永久挂起

官方原文：「如果注册了 `wx.onNeedPrivacyAuthorization` 监听，则一定要调用 resolve 接口」，
且该监听是**覆盖式注册**（重复注册只有最后一次生效）。
漏调 resolve 的后果是触发事件的隐私接口**永远停在 pending**——用户看到的是「点了没反应」。

**因此本实现刻意不注册监听**，改走官方 demo1 的「主动查询」路线：
`wx.getPrivacySetting()` 查 `needAuthorization` → 未同意则弹自绘窗 →
用户点「同意」（`open-type="agreePrivacyAuthorization"`，基础库会校验该按钮确被点击过）→
微信侧同步同意状态 → 后续隐私接口直接可调。
**漏网之处由微信官方弹窗兜底**（官方文档：开发者未响应时微信会主动弹出官方弹窗），
不会出现「点了没反应」这种最难排查的故障。

### 订正三：隐私接口范围比初稿列的更广，且 `wx.login` 不在其中

官方《小程序用户隐私保护指引内容介绍》的接口对照表确认：

| 隐私类型 | 接口 / 组件 |
|---|---|
| 收集你的昵称、头像 | `<button open-type="chooseAvatar">`、`<input type="nickname">`、`wx.getUserProfile`（已回收） |
| 收集你选中的照片或视频信息 | `wx.chooseImage`、`wx.chooseMedia`、`wx.chooseVideo` |
| 使用你的相册（仅写入）权限 | `wx.saveImageToPhotosAlbum`、`wx.saveVideoToPhotosAlbum` |
| 读取你的剪切板 | `wx.setClipboardData`、`wx.getClipboardData` |
| 使用你的通讯录（仅写入）权限 | `wx.addPhoneContact` |

- **初稿漏了** `saveImageToPhotosAlbum`（海报存相册）与 `setClipboardData`（复制手机号）——
  本轮已一并接入授权检查（前者；后者发生在访客复制名片公开信息时，走官方兜底并补了 fail 分支）。
- **初稿错把 `wx.login` 列为隐私接口**——它不在表内（openid 属需在 MP 后台声明的「用户标识信息」，
  但 `wx.login` 本身不被隐私接口拦截）。

### 顺带发现并修复的数据质量 bug

`wx.getUserProfile` **自 2022-10-25 起被平台收回**：新发布的小程序版本调用它统一返回
「默认灰色头像 + 昵称『微信用户』」，拿不到真实微信昵称。
原先 `utils/wechat.ts` 的 `getWechatNickname()` 会把「微信用户」当成真昵称写进表单并提交入库
（假数据）。现已识别该占位值并视为「没拿到」。

> 想要**真实**昵称需改用官方「头像昵称填写能力」（`<input type="nickname">`）——
> 那会改动 apply 表单布局，属产品决策，未在本轮实施。

### 本轮改动清单

| 层 | 文件 | 改动 |
|---|---|---|
| 小程序 | `utils/privacy.ts`（新） | 授权状态查询 / 需否判定 / 等待与结算 / `ensurePrivacy(page)` 编排 |
| 小程序 | `components/privacy-consent/`（新） | 自绘授权弹窗（同意按钮用官方 open-type） |
| 小程序 | `pages/privacy/privacy/`（新） | 隐私政策页（六节正文） |
| 小程序 | `app.json` | `__usePrivacyCheck__` + 注册 privacy 路由 |
| 小程序 | `pages/apply/open/` | 表单明示同意勾选 + 授权检查 + 上传带 openid |
| 小程序 | `pages/employee/edit/` · `pages/poster/` | 隐私接口调用前授权检查 |
| 小程序 | `pages/employee/me/` | 隐私政策入口（登录前后两态） |
| 小程序 | `utils/wechat.ts` | 删除失效的 `getWechatNickname`（getUserProfile 已收回，见第七轮改用原生填写能力） |
| 小程序 | `utils/contact.ts` · `components/employee-card/` | 隐私被拒的失败分支（原来会静默失效） |
| 后端 | `internal/license/embed.go` | 公钥支持 `LICENSE_PUB` 覆盖 + 占位密钥自检 |
| 后端 | `cmd/server/main.go` | 私有部署用占位公钥即拒绝启动（排在验签之前） |
| 后端 | `internal/handler/upload.go` | 匿名上传端点限流（`upload:` 桶键） |
| 后端 | `internal/handler/media_check.go` | 匿名上传带 openid 时照常送检 |
| 后端 | `internal/handler/license_enforce.go` | 席位判定抽成共享函数 `seatExceeded` |
| 后端 | `internal/handler/apply.go` | 审批路径补席位检查 + 补 `db.Create`/`db.Save` 错误处理 |
| 后端 | `internal/setting/setting.go` | 新增「匿名上传频率限制」设置项 |

### 验证

- **小程序全量：19 个 spec / 356 例全绿**（含新增 `tests/privacy.spec.ts` 15 例；
  `apply_open` 22 / `employee_edit` 13 / `poster` 105 / `employee_me` 14 / `index_home` 33 无回归）。
- **后端全量：`go test ./...` 22 个包全绿**（含新增 `internal/handler/commercial_readiness_test.go` 6 例
  + `internal/license/embed_test.go` 5 例）。
- 前端 `vue-tsc --noEmit` 与 `vite build` 均通过（本轮未改前端源码；首次构建失败是沙箱文件写入代理
  的 `EPIPE` 故障，沙箱外重跑正常）。
- **反向验证**（确认测试在守护真实行为、不是假通过）：
  - 小程序侧破坏 `settle` 与 `ensurePrivacy` 两处实现 → **6 例 FAIL**；
  - 后端侧破坏席位检查与 openid 送检 → **2 例 FAIL**；
  - 后端侧破坏公钥覆盖与占位密钥识别 → **4 例 FAIL**。
  三次破坏均命中**恰好对应**的用例，还原后全绿。
- `python3 miniprogram/tests/audit_class_collision.py` 无冲突（新增组件类名统一 `pc-` 前缀）。

### 仍需人工完成（代码侧无法代劳）

1. **MP 后台配置《小程序用户隐私保护指引》**并声明用到的隐私类型
   （昵称/头像、选中的照片或视频、相册写入、通讯录写入、剪切板；以及 openid 所属的「用户标识信息」）。
   **未声明对应类型时平台会直接禁用该接口**（`errno: 112`），代码再对也调不通。
2. **发行方生成自有密钥对**（仅私有 / 独立部署需要；集中 / 平台模式不验本地许可证）：在**签发环境**跑 `scripts/license_setup.sh` 一键完成（或 `license keygen` 后自行填 `LICENSE_PUB`），**私钥只留在签发环境**。
3. 真实微信昵称：**已在第七轮落地**——申请页改用 `<input type="nickname">`（原 `wx.getUserProfile` 已被平台收回，永远返回占位值「微信用户」）。

---

## 第三轮实施记录（C5 / C6 / C7 收口，2026-10-26）

本轮把「数据泄露后赔不赔得起」相关三项补齐，P2 段只剩 **C2 流程闭环 / C3 保留期 / C4 Redis / C8 日志与 PITR**。

| 项 | 落地内容 | 验证 |
|---|---|---|
| **C7** EXIF/GPS 剥离 | `internal/storage/image.go` `ProcessImage`：jpg/png 解码→重编码剥离全部元数据；gif/webp/mp4 透传；坏图 fail-open | `go test ./internal/storage/...` ok |
| **C6** CI 密钥扫描 | `.gitleaks.toml` + `ci.yml` `secrets-scan` job | 本地 gitleaks 8.30.1 扫 199 commits：无白名单 6 处假值 → 加白名单 0 leaks |
| **C5** PII 静态加密 | 见上方 C5 行；新增 `internal/pii`、`model/employee_pii.go`、`model/apply_pii.go`、`handler/pii_fields.go`、迁移 `0030`/`0031` | `go build ./...` / `go vet ./...` 干净；`go test ./...` **23 包全绿** |

**实施中踩到并已钉住的坑**（后人不要重犯）：

1. **GORM map 更新不触发模型钩子** —— `db.Model(...).Updates(map)` 不走 `BeforeCreate`，必须显式 `pii.Encrypt` + `pii.BlindIndex` 同时写密文与索引，否则写入的是明文、且搜索永远命中不了。
2. **加密后 `LIKE` 必然失效** —— 只在「名片修改申请」里改了子查询、漏改员工列表的 `keywordCond`，导致 `TestAPIFlow` 与 `TestEmployeeListKeywordAndStatus` 双双 FAIL（「按手机搜不到该员工」）。**新增加密列时必须把所有搜索入口一起过一遍**。
3. **MySQL JSON 列装不下密文** —— `card_modify_applies.snapshot` 原为 JSON 列，密文是 base64 文本，必须先 `MODIFY COLUMN ... LONGTEXT`（迁移 `0031`），否则插入直接失败。
4. **裸 SQL / `Scan` 不过钩子** —— 统计模块的 `Group`/`Scan` 拿到的 `employees.name` 是密文，需显式 `decPII`；此类点位只能靠人工审计（`AfterFind` 不覆盖 `Scan`）。

**反向验证**：摘掉 `Employee.BeforeCreate` → `TestEmployeePIIEncryptedAtRest` 报「姓名应以密文落库」；摘掉 `OpenApply.BeforeCreate` → 报「申请人姓名应以密文落库」；均还原后全绿。

**PII 加密密钥（2026-10-27 由「只告警」收紧为「启动硬门槛」，见第七轮记录）**：此前未设置 `PII_ENC_KEY` 时进程仍带内置 dev 密钥启动，生产漏设无人察觉。现在：

- **手工部署**未设置 → `cmd/server` 拒绝启动并打印生成命令；逃生门 `ALLOW_INSECURE=1`（仅本地联调，放行时打 `[SECURITY][ALLOW_INSECURE]` 留痕）。
- **安装向导**自动签发一把随机密钥写进 `data/runtime.env`，走向导的新装不受影响。
- **配置文件形态已打通**：`PII_ENC_KEY` 现在既能用环境变量、也能写在配置文件里（此前 `pii` 包在 `init` 里只读环境变量，而 `init` 早于配置加载，写进文件的密钥根本读不到）。

时间约束不变：**必须在首次写入业务数据之前设定**——一旦已用 dev 密钥写入 PII，换钥后旧数据再也解不回来（没有 re-key 机制）。走向导的部署请**务必把 `data/runtime.env` 纳入备份**。

---

## 第四轮实施记录（E1 / E2 收口，2026-10-26）

两项「低成本高感知」的体验缺陷。共同点是**不会报错、只会默默错**，因此都配了静态守门用例。

| 项 | 改动 | 守门 |
|---|---|---|
| **E1** 导航栏标题写死客户名 | `app.json` 全局标题 → 产品名；新增 `utils/brand.ts`（企业 → `brand_name` → 产品名的解析顺序）；海报页按 `card.company` 设标题；补 4 个页面功能性标题（编辑名片 / 登录 / 案例详情 / 服务详情）；首页自绘顶栏与分享标题兜底改为留白 / 产品名 | `tests/brand.spec.ts`：扫 `miniprogram/`（除 tests）断言不含演示租户名 + `app.json` 全局标题 === `FALLBACK_BRAND` |
| **E2** 源码残留局域网 IP | `DEFAULT_BASE_URL` 默认空；`hasBaseUrl()` 守卫；基址为空时 `fetchPublicSettings` 与 `app.ts onLaunch` 不发请求；末尾斜杠归一；`preflight.sh` 空值由 `skip` 改为 `FAIL` | `tests/config.spec.ts`：默认必须空 / 若填只允许 https 或回环 / 空基址不发请求（反向验证时该用例实测 5.0s，正是被白等的那个超时） |

**为什么 E2 不是「随便填个占位域名」**：种子基址是拉取后台配置的**唯一入口**。
空着 / 填错 → 首连失败 → 永远拿不到后台下发的 `miniprogram_base_url` → 只能重新打包提审。
所以默认值的正确形态是**空**（源码零地址），由部署方填，由 preflight 在发布前拦。

**E1 的顺序不能反**：`brand_name` 是**部署方**品牌（平台模式下 = 平台名），
而名片/海报是员工替**自己企业**做的物料——企业名必须优先，否则客户的海报上顶着平台名。

---

## 第五轮实施记录（B3 存储配额 / B4 试用授权，2026-10-26）

上一轮结束时，经营闭环只剩这两项：**B3 完全没做，B4 是「机制在那儿但默认可绕过」**。
两者的共同原则是**升级不能让存量部署突变**——这也是本轮的每一个取舍背后的判据。

| 项 | 后端 | 前端 | 守门 |
|---|---|---|---|
| **B4** 试用授权 | `setting` 注册 `license_trial_enabled/days/seats`；`license/trial.go`（`TrialSpec` / `CreateTrialLicense` / `SetTenantLicense` / `TenantLicenseOf`）；开通企业改事务并落试用行；`handler/license_admin.go` 的 GET/PUT；`audit.ActionCompanyLicense` | 列表「授权」列（四态标签 + tooltip）；「授权与配额」弹窗（版本标识/席位/到期日/功能开关/存储上限） | `trial_test.go` 7 例；`commercial_license_storage_test.go` 的 `TestCompanyCreateGrantsTrialLicense` / `TestCompanyLicensePut`；`Companies.spec.ts` 的四态标签、回填、保存、过去日期二次确认 |
| **B3** 存储配额 | `model/storage_usage.go` + 迁移 `0032`；`handler/storage_quota.go`（`checkStorageQuota` / `addStorageUsage` / `storageQuotaBytesFor` / `uploadCompanyID`）；`upload.go`、`storage.go`（presign）接入；`audit.ActionCompanyStorageQuota` | 列表「存储」列（`fmtBytes`）；弹窗内配额覆盖 + 已用量与全局兜底说明 | `TestUploadStorageQuota`（1MB 配额：600KB 放行 → 再 600KB 拒 → 被拒**不计**用量 → percent 58）；`TestStorageQuotaOverride`（租户覆盖压过全局、改配额不清用量、非法配额拒绝）；前端存储列与配额回填用例 |

**四条不可反的取舍（都写进了代码注释）**：

1. **存量企业不补授权行**。升级即给所有老客户套上「14 天 / 5 席」= 升级当天把人停服。
   所以只对「新开通的企业」落行；存量维持「无行 = 不限制」，要约束就由运营显式填写。
2. **试用天数不按 `company.created_at` 现算**。现算的结果是：运营把试用期从 14 天改成 30 天，
   **所有在用租户的到期日一起往后漂**。落行是快照，改配置只影响以后新开的。
3. **配额默认 0 = 不限**。与上一条同一原则；默认给个 500MB 会让升级后的第一次上传就报
   「存储空间已用尽」，而那时用户完全不知道有这回事。
4. **配额/用量查库失败不拦上传**。宁可少算一次，也不要因为一次数据库抖动让整个企业传不了东西——
   那种情况下用户看到的是「空间已用尽」，而实际根本没满，支持成本极高。代价是日志会 WARN，需监控。

**为什么到期日要按当天 23:59:59 落库**：客户理解的「到 12-31 到期」是这一天整天才算完；
存成当天 `00:00` 会让客户在最后一天上午就被停服——这类「说不清是谁的错」的投诉最伤口碑。

**为什么 `SetTenantLicense` 用 map 写库**：`expires_at` 要能写成 `NULL`。结构体零值会被 GORM
序列化成 `'0000-00-00 00:00:00'`，MySQL 严格模式下直接报错，「清空到期 = 不过期」这条路就走不通。

**反向验证**：摘掉 `license.CreateTrialLicense` 调用 → `TestCompanyCreateGrantsTrialLicense` 报
`Received unexpected error`；摘掉 `checkStorageQuota` → `TestUploadStorageQuota` 报
`expected int(1) actual float64(0)`；前端把「过去日期确认」短路成 `false` + 把过期标签改成 info →
对应的两条 `Companies.spec.ts` 用例变红。全部还原后：后端 `go test ./...` **23 包全绿**，
前端 `Companies.spec.ts` **29 例全绿**。

**仍留的口子（已在代码注释与界面文案里写明）**：用量统计自功能启用起累计、不含历史文件；
覆盖写（`SaveBytes` 的固定名对象）不扣减；直传按客户端上报的 `size` 记（宁可多记不可不记）；
**用量接口拉取失败时不提交配额**（否则会把客户已设的上限悄悄改回「跟随全局」），并明确提示只保存了授权。

**顺带修掉的两个既有缺口**（跑全量测试时暴露，都不是本轮引入的）：

1. **审计中文名表缺 4 个动作**（`employee.cancel` / `employee.export` / `company.license` /
   `company.storage_quota`）。`audit.spec.ts` 的跨端守门（读 `audit.go` 比对）一直红着——
   界面上会把 `employee.cancel` 这样的原始键直接摊给运营看。前两个是 A2 轮次遗漏的，
   后两个是本轮新增。**这就是守门用例的价值：漏了就红，不靠人记。**
2. **`request.ts` 取 `resp.headers['x-request-id']` 没用可选链**：测试替身/自定义 adapter
   返回的响应没有 `headers` 时，整个响应拦截器直接抛 TypeError，所有业务错误都变成
   `Cannot read properties of undefined (reading 'x-request-id')`，真实原因全部丢失
   （`request.spec.ts` 6 条全红）。改为可选链 + 补一条「没有 headers 也不崩」的用例。


## 第六轮实施记录（C2 / C3 / C8 / D1–D4，2026-10-27）

本轮把**运维兜底**与**工程门禁**两层收口。七项全部落地，其中四条是不可反的取舍，
先写在这里，免得以后有人「顺手优化」掉：

### 1. C3 审计保留期：**默认 0 = 永不自动清理**

删除审计日志是**不可逆**的，而审计的价值恰恰在「出事之后能翻到很久以前」。
若缺省就清理，升级当天会把客户的存量证据清空——这与 B3（配额默认不限）、
B4（存量不补授权）是同一条纪律：**升级不得突变存量**。所以必须由运维显式填天数才生效。

配套的三个细节都不是装饰：
- **分批删除**（每批 2000、单 tick 上限 10 万）。一次 DELETE 清掉几十万行会在 MySQL 上
  长时间持锁，而这张表还在被业务持续写入；分批把单次持锁压到毫秒级。
  分批写法是「先按主键查 id、再按 id 删」——MySQL 不允许 `IN` 子查询里写 `LIMIT`，
  SQLite 不支持 `DELETE ... LIMIT`，这是两种库都能跑的唯一写法。
- **清理动作本身记一条 `audit.purge`**（新增 `ActorSystem` 操作者类型）。能删日志的人
  同时也能抹掉自己的痕迹，所以「什么时候、按多少天、清了多少条」必须留痕。
  错误文本**不进 detail**——数据库错误可能携带连接串（含密码），审计表不能变成新的泄密面。
- **没删到东西时不留痕**。否则每个 tick 都往审计表里塞一条空跑记录，很快淹没真正有用的行。

反向验证：把 `keepDays <= 0` 的短路摘掉 → `TestPurgeAuditLogsZeroRetentionDeletesNothing` 立即变红。

### 2. C8 结构化日志：**包装标准 log 的输出目标，而不是换日志库**

全站已有上百处 `log.Printf`，换日志库要改所有调用点，还会把 `[WARN]` 这类已被运维
写进告警规则的前缀一起改掉。这里改成包装 `log.SetOutput`——JSON 模式下把已打出的每一行
包成 JSONL，既有代码一行都不用改，级别从既有的方括号标签推断：
- 认不出的标签按 `info`，**绝不丢行**（排障时最需要的往往正是那一行）；
- 连续标签取**最严重**的一个——`[MIGRATE][FATAL]` 若只认第一个标签，数据库迁移失败
  这种致命错误会被记成普通 info，告警规则直接漏掉；
- gin 自己的两个 writer 也接进来（`DefaultWriter` / `DefaultErrorWriter`），
  否则 gin 的启动日志与 panic 栈会以纯文本混进 JSONL，让采集器那几行解析失败。

访问日志是真的字段化（`method/path/status/latency_ms/request_id`）——「按路径聚合 5xx」
这条最常用，自由文本只能靠正则捞。**文本模式下格式一字不改**，运维既有的 grep 规则不因升级失效。

### 3. C8 `/metrics` 白名单：**配错即整体拒绝（fail-closed）**

`METRICS_ALLOW_CIDR` 是第二道门（第一道是 `metrics_enabled` 默认关）。任一条 CIDR 解析
失败时**全部拒绝**而不是放行：配了白名单却没生效，是最难发现的一类暴露——
「以为限制了」比「知道没限制」危险得多。启动日志会点名哪一条解析失败。
另一侧保持宽松：列表为空时不额外限制（没配第二道门不该顺手把第一道门也堵死，
否则配了 `METRICS_ENABLED=1` 的人会得到一个永远 404 的监控端点且不知道为什么）。

反向验证：把 fail-closed 改成放行 → `TestNewIPAllowlistRules` 变红。

### 4. D1 lint：**只留「报出来一定是问题」的规则**

后端 `errcheck / govet / ineffassign / staticcheck / unused`，前端「未使用变量/导入 + Vue 模板硬错误 + 少数 JS 陷阱」。
**刻意不启用**：gofmt（存量文件有 Go 版本导致的注释排版差异，一开就是上百处噪音）、
`no-explicit-any`、命名/复杂度/排版类规则。理由是：风格类规则一开就是几十上百条，
结局只有两种——花几天改代码，或者加一堆 `nolint` / `eslint-disable` 把它变成摆设，两头都不划算。

存量已清零（后端 29 → 0，前端 25 → 0）。修的时候有一条纪律：**每个 `_ =` 都必须带一句
「为什么不判这个错」**，只在真正无后果的清理路径上用；写路径的 Close 一律照常判错
（`config/file.go` 写配置、`mpupload.go` 写私钥——这两个的 Close 失败意味着数据没落盘，
后面报错会更晚、更难定位）。

### 5. D2 / D4：**先非阻塞**

依赖漏洞扫描与端到端冒烟都设了 `continue-on-error`。理由一致：
- 漏洞扫描报的相当一部分 CVE 在本项目里根本调不到那条路径，修它往往要升大版本，
  一上来就当门禁＝红灯常态化＝没人看；
- 端到端冒烟依赖「起真实进程 + 自重启 + 端口可用」，runner 上偶发环境性抖动。

先让它们**跑起来、看得见**，稳定后再摘掉 `continue-on-error` 转正式门禁。

### 6. 本轮踩到并记下的两个坑

1. **bash 全角标点吞变量名**：`$PORT（未配置）` 里的全角括号被 bash 当成变量名的一部分
   （`PORT（` 未绑定 → `unbound variable`）。本项目脚本里凡是「变量名紧跟中文标点」的地方
   一律写成 `${VAR}`，已用脚本扫一遍修掉（`e2e_smoke.sh` 10 处、`backup_binlog.sh` 4 处）。
2. **改 `defer os.Remove(tmpName)` 成同步调用**：为了让 errcheck 闭嘴，我第一次改成了
   `_ = os.Remove(tmpName)`，结果临时文件在 rename 之前就被删掉——6 个测试立刻变红。
   正确写法是 `defer func() { _ = os.Remove(tmpName) }()`。**lint 驱动的修改同样会引入行为变化，
   改完必须跑测试。**

### 7. 验证记录

| 项 | 结果 |
|---|---|
| 后端 `go build` / `go vet` / `golangci-lint run`（v2.15.0） | 干净 / 干净 / **0 issue** |
| 后端 `go test ./...` | 全绿（新增 `internal/audit` 12 例、`internal/logx` 10 例、中间件 3 例） |
| 前端 `vue-tsc --noEmit` / `eslint .` | 干净 / **0 error 0 warning** |
| 前端受影响的 11 个 spec 逐个复跑 | 全绿 |
| 恢复演练（SQLite / MySQL 各一次） | 均通过，记录存 `docs/drill-records/2026-10-27-{sqlite,mysql}.md` |
| 端到端冒烟 `scripts/e2e_smoke.sh` | 七步全 PASS |
| 第三方许可清单 `--check`（新增 116 个 dev 依赖后） | 仍一致（清单只统计运行时依赖） |

---

## 第七轮实施记录（两项「人工事项」收口，2026-10-27）

第六轮结束时还剩两条被标为「人工」的项：`PII_ENC_KEY` 生产必设、真实昵称要改
`<input type="nickname">`。复盘后两者的「不可代码化」程度并不一样，本轮分别处理。

### 1. PII_ENC_KEY：从「只告警」收紧为「启动硬门槛」，并让安装向导自动签发

**原来为什么不敢拦**：怕拒启把「clone 即跑 / 首次安装 / 测试」全搞挂。
**解法是先把「首次安装」这条堵上**——让安装向导自己签发密钥，于是被拦的就只剩
「手工部署漏设」这一真正需要有人喊一声的场景。

- `setup.Install`：安装时若 `PII_ENC_KEY` 尚未配置，随机生成 32 字节并写入 `data/runtime.env`。
  - **必须用 `base64.StdEncoding`，不能复用同文件的 `secret()`（RawURLEncoding）**：
    `pii` 包按 StdEncoding 解码，`-` / `_` 会被判为非法字符；拿 `secret(32)` 当密钥会
    「密钥格式错误」，且只在随机值里恰好出现这两个字符时才暴露——本地跑十次都正常、
    上线某次就起不来。`TestGeneratedPIIKeyAcceptedByPIIPackage` 钉死这条。
- `cmd/server`：新增 `piiKeyGate()`——未设置 → 拒绝启动；格式错误 → 拒绝启动且
  **不受 `ALLOW_INSECURE` 影响**（放行会让此后每次加解密都失败）；`ALLOW_INSECURE=1` → 放行并留痕。
- **打通配置文件形态**：`pii` 包在包级 `init` 里读环境变量，而 `init` 早于 `config.Load()`——
  写进配置文件的密钥此前**根本读不到**。新增 `pii.Reload()`，由 `syncPIIKeyFromConfig()`
  把配置值补进环境变量后重新装载，两种部署形态才一致。

真跑验证（编译出 `/tmp/wc-server`）：① 配置文件无密钥 → 打印 FATAL 并退出；
② 追加 `openssl rand -base64 32` → 正常起（`database driver=sqlite` … `listen`）；
③ `ALLOW_INSECURE=1` → 起并打 `[SECURITY][ALLOW_INSECURE]` 留痕。

### 2. 昵称：改用官方「头像昵称填写能力」

`wx.getUserProfile` 自 2022-10-25 被平台收回，此后永远返回占位值「微信用户」，
原来的「授权获取」按钮点了等于白点。头像此前已是 `open-type="chooseAvatar"`，
本轮把昵称补齐为同一套能力：

- `pages/apply/open/open.wxml`：昵称行由「文本 + 授权获取按钮」改为
  `<input type="nickname">`，值经既有 `bindinput` 写入 `form.nickname`。
- `pages/apply/open/open.ts`：删除 `authorizeNickname` 与 `getWechatNickname` 导入。
- `utils/wechat.ts`：**删除** `getWechatNickname`——它是「调它就能拿到昵称」的错觉来源，
  留着只会让人再踩一次；删除处留注释说明改用原生填写能力。
- 授权弹窗交给微信自兜（与既有 `chooseAvatar` 处理一致），代码不额外拦；
  前提是 MP 后台已声明「收集你的昵称、头像」（未声明平台直接禁用，`errno 112`）。

**踩到的坑**：源码守门用例最初写 `expect(wxml).toMatch(/type="nickname"/)`，
被**自己写的注释**命中——注释里也提到了 `type="nickname"` 这个写法，
于是「把控件改回普通 input」这种真回退也不会变红。修法是匹配前先剥掉 `<!-- -->`。
反向验证：改成 `type="text"` → 红；还原 → 绿。

### 3. 验证记录

| 项 | 结果 |
|---|---|
| 后端 `go build` / `go vet` / `golangci-lint run` | 干净 / 干净 / **0 issue** |
| `go test`（pii / setup / cmd/server） | 全绿（新增 `TestReloadPicksUpNewKey`、`TestGeneratedPIIKeyAcceptedByPIIPackage`、`TestPIIKeyGate` 四个子例） |
| 真二进制三种形态（漏设 / 已设 / ALLOW_INSECURE） | 拒绝启动 / 正常起 / 放行并留痕 |
| `scripts/e2e_smoke.sh` | 七步全 PASS（走向导的新装不被新门槛拦住） |
| 小程序 `apply_open.spec.ts` / `employee_login.spec.ts` | 23 + 9 全绿 |
| 反向验证 ×2 | 门槛置空 → `TestPIIKeyGate` 红；`type="nickname"` 改 `text` → 守门红；均还原后转绿 |

---

## 第八轮实施记录（商用零数据基线，2026-10-28）

### 1. 问题：装完就带着「别人的东西」

用户装完全新版后台第一眼看到的是：一家叫「廊坊市朗汇机电设备有限公司」的企业，
外加 7 条业绩分类。根因不在安装向导，而在**迁移脚本里混进了业务种子数据**：

| 来源 | 内容 | 影响面 |
|---|---|---|
| `migrations/0001_init.sql` | 7 条 `achievement_categories`（居住住宅/政务办公/…） | 只在 MySQL 新建库出现（SQLite 走 AutoMigrate，本来就没有） |
| `migrations/0011_company_tenant.sql` | `INSERT company (1, '廊坊市朗汇…')` | 两种引擎都会出现 |

演示数据的真实来源是 `cmd/devserver` 的 `seedDemoData()`（企业信息/8 分类/10 案例/4 服务/
1 员工/1 管理员 + 27 张占位图），那套东西是给本地联调用的，不该出现在客户库里。

### 2. 做法：零数据为默认 + 演示包改为「安装时可选」

1. **迁移只放表结构**。摘掉上面两处 INSERT。已上线库不受影响——迁移按文件名记账
   （`schema_migrations`），不会重放；新装库从此是干净的空库。
2. **演示数据下沉到 `internal/demoseed`**。原来在 `cmd/devserver`（package main），
   安装向导在生产二进制里、且 `main` 之间不能互相 import，必须下沉。
   `demoseed.Load(db, Options{})` 一套代码被两个使用方复用：
   - `cmd/devserver`：每次启动都写，口令固定（联调要反复登录）；
   - `internal/setup`：只在勾选时写一次，**口令随机生成**并在安装结果里回显一次。
   商用库里绝不能出现「文档里写明的已知口令」——那是后门，不是演示。
3. **安装向导加第四个表单块「初始数据」**（默认不勾）。勾选时写入整套样板，
   安装完成页列出两个演示账号与随机口令（库里只有哈希，离页即失）。
4. **`InstallResult` 增加 `demo` / `warnings`**。演示数据写失败只降级为 warning，
   不让整个安装报错：此时库与超管已就绪，报错重试会被「新鲜库守卫」挡住，
   运维会被卡死在半路（一个已存在的设计陷阱，不再给它加新的触发点）。

### 3. 顺带修掉的一个顺序陷阱（最贵的一条）

演示员工的手机号**盲索引**是用「写入那一刻的 PII 密钥」算出来的。原安装流程里
`PII_ENC_KEY` 是**最后一步写配置文件时**才签发的，若照原顺序插入演示数据，
员工行会用内置 dev 密钥算盲索引；重启换成真密钥后，这个员工永远查不到
（小程序登录报「账号不存在」），而报错信息与密钥毫无关联，且没有 re-key 机制。

修法：把密钥签发提到「写任何 PII 之前」，并新增 `pii.SetKey()` **在内存中**装钥
（不改环境变量——env 有「已设置」语义，安装中途失败重试时会让人误判密钥已就绪、
漏写进配置文件）。配置文件仍在最后一步原子写。

### 4. 验证记录

| 项 | 结果 |
|---|---|
| 后端 `go build ./...` / `go vet` / `golangci-lint run ./...` | 干净 / 干净 / **0 issue** |
| `go test ./...`（26 个包） | 全绿 |
| 新增 `internal/demoseed` 6 例 | 整套落齐 / 账号可登录 / 指定口令 / 幂等 / 盲索引跟随当前密钥 / 空目录不报错 |
| 新增 `setup` 2 例 | `TestInstallLeavesNoBusinessData`（零数据 + 平台超管 company_id=0 + 素材零落盘）、`TestInstallWithDemoData`（9 张表行数 + 素材落盘 + 按配置文件密钥能查到员工 + 口令与哈希配对） |
| 新增 `migrate` 1 例 | `TestMigrationsCarryNoBusinessData`（剥注释后禁止任何 `INSERT INTO`） |
| 反向验证 ×3 | 0011 塞回 INSERT → 迁移守卫红；去掉提前的 `pii.SetKey` → 「查不到演示员工」红；强制 `LoadDemoData` → 零数据用例红。均还原后转绿 |

> 迁移守卫的**注释剥离**是必须的：这两个文件现在正写着「原先是 INSERT …」的说明，
> 不剥注释会被自己的注释命中，真回退反而不红（第七轮同款坑，已内建）。

### 5. 仍未落地（下一批）

- **平台超管「切换当前企业」**：后端 `?companyId=` 三处（企业内容 / 审核 / 统计）都已支持，
  是纯前端活——顶栏加切换器并把参数透传下去，未选时给空态。
- **新建企业自动复制分类模板**：零数据后新企业一个业绩分类都没有。分类属于客户业务内容，
  不该在迁移里预置；正确做法是「开通企业时复制一份模板」（当前开通企业只种子了
  公司信息/简介/首页布局三件套）。

---

## 第九轮实施记录（平台超管「切换当前企业」，2026-10-28）

### 1. 缺口：平台超管的「全部企业」视角没法收窄

零数据基线之后，平台超管要做的第一件事是「新建企业 → 给这家企业录内容」。
但在此之前，后台**没有任何地方能指定「我现在管的是哪家」**：

| 后端行为 | 后果 |
|---|---|
| 企业内容接口 `targetCompanyID`：平台超管不传 `?companyId=` → **回落 1 号企业** | 改着 B 家的公司信息，实际写进 1 号企业 |
| 统计 / 审核 / 员工接口 `scopeCond`：平台超管不传 → **不按企业过滤** | 几家企业的数据混在一张表里，看着像「汇总」，其实是串数据 |

两种都不会报错，也都不弹提示——属于最难排查的一类。

### 2. 做法：一个注入点 + 一处硬拦截

- **请求作用域收在一处**（`store/companyScope.ts` + `api/request.ts` 的请求拦截器）：
  平台超管选了企业后，所有 `/admin/**` 请求自动带 `?companyId=`，调用方已显式传的不覆盖。
  为什么不做「每个 api 函数加个参数」：企业内容 6 页、审核 2 页、统计 3 个端点、员工 5 个操作，
  逐个传必漏，漏了不报错、只静默串数据——把遗漏面从「N 个调用点」收敛成「1 个拦截器」。
  用叶子模块而不是让 request 直接读 Pinia store，是为了避开 store → api → request → store 的循环依赖。
- **未选企业 = 硬拦截**（`AdminLayout` 的 `needPickCompany`）：企业内容 / 统计 / 审核 / 员工页
  渲染「请先选择要管理的企业」空态，**不发请求**；平台级页面（企业管理 / 系统设置 / 账号 /
  操作日志 / CDN / 内容安全）不受影响。宁可挡住，也不要让后端那两种缺省行为在界面上出现。
- **切换企业 = 重建页面**：`<component :key="route.path + '#' + tenant.currentId">`。
  不重建的话页面还是上一家的数据，而「保存」按钮是活的——那是最坏的一种错。
- 顶栏选择器（桌面内联 / 手机端收进头像下拉 → 弹窗）共用 `components/CompanyPicker.vue`；
  企业停用仍可选中查看历史内容，但列表里标「已停用」。
- 顺带补齐 `GET /api/admin/media-checks` 的 `?companyId=`（原先是手写 `CurrentCompanyID != 0`
  判断，平台超管无法收窄）。不收窄的话顶栏选了 A 家、内容安全页却列着所有家。

### 3. 验证记录

| 项 | 结果 |
|---|---|
| 前端全量 `vitest`（单 fork） | **60 文件 / 878 例全绿**（本批 +26） |
| `vue-tsc --noEmit` / `eslint` | 0 错 / 0 报错 |
| 后端 `go test ./...` | 26 包全绿；`golangci-lint`（连同上一批）0 issue |
| 新增 `store/tenant.spec.ts`（10 例） | 企业管理员固定本企业且不注入 / needPick / 持久化 / 取消 / 刷新恢复 / 只拉一次 / 失败不清空 / 存下的企业已不存在则自动清 / 搜索不清选择 / clear |
| 新增 `request.spec.ts`（+5 例） | 自动带 companyId / 补 params / 未激活不加 / 非 `/admin` 不加 / 显式传的不覆盖 |
| 扩展 `AdminLayout.spec.ts`（+11 例） | 身份同步 / 空态拦截（内容·审核·统计·员工）/ 平台级页面不拦 / 已选正常 / 企业管理员不进空态 / 选择器只在平台超管出现 / 空态按钮开弹窗 / 手机端下拉开弹窗 / 登出清视角 |
| 后端 `media_check_test.go` +1 断言 | 平台超管带 `companyId=2` 只看该企业 |
| 反向验证 ×3 | 关掉请求注入 → 2 例红；把 `needPickCompany` 置 false → 3 例红；还原后全绿 |

> 踩到的坑（已记入测试注释）：`AdminLayout.spec.ts` 的第二个 `describe` 忘了写 `beforeEach`，
> 模块级的 `mocks.isPhone` / `tenant.*` 从上一条用例漏到下一条，导致「登出」用例
> 继承了手机端布局、找不到顶栏按钮。逐例重置是这类模块级单例的必需品。

### 4. 仍未落地

- ~~**新建企业自动复制业绩分类模板**~~ → **第十轮已落地**。
- **平台超管的「全部企业」汇总视图**：本轮刻意不做（写页面必须选定一家）。
  若要「跨企业对比」，应另开一个只读的汇总页，而不是让内容页在「全部」态下可写。

## 第十轮实施记录（开通企业自带业绩分类模板，2026-10-28）

第八轮把业务种子从迁移里摘干净之后，留下一个空洞：**新开通的企业一个分类都没有**。
企业管理员登录后「经典业绩」页是空的、小程序业绩 Tab 连一组都渲染不出来，必须先自己
建分类才录得了案例。分类属于客户业务内容，不该回迁移里预置；正确落点是「开通企业时
复制一份模板」。

### 1. 为什么单独有个包

`internal/cattemplate` 只放一份「名称 + 排序」的八分类名单。演示数据包
（`internal/demoseed`）用的正是同一批分类名——两处各写一份迟早漂移，某天演示里叫
「医院、学校」、新企业模板叫「医疗」，而客户在这两处看到的本该是同一套东西。
模板包是叶子（零 import），handler 与 demoseed 都引它，`Default()` 返回副本，
避免调用方就地改排序污染下一次开通。

### 2. 三个刻意的选择

- **icon 留空**：演示分类的图标是演示企业上传目录里的文件，干净库（未勾选演示数据）里
  根本不存在，照抄 URL 只会得到一排裂图。图标由企业管理员在「业绩分类」页从素材库挑。
- **is_locked=false**：锁定是给演示租户保设计稿用的（分类不可删）。客户要按自己行业
  改名或删掉用不上的分类，锁死等于逼他们另建一家企业。模板的意义是「有骨架可以改」。
- **失败不中断事务**：分类是骨架数据，缺了大不了业绩页空着；而为它回滚会把企业行一起
  撤掉，运营重试还得重新起名起号。错误单独接出 `catErr`，**提交之后**才打 WARN——
  在事务里打日志的话，事务一回滚那条 WARN 就是假警报（分类其实没留下，企业也没留下）。

### 3. 可取消，且默认要

`POST /api/admin/companies` 新增 `seedCategories`，**用指针**（`*bool`）而不是 `bool`：
`nil` = 没表态 = 按 true 处理，`false` = 明确不要。用裸 bool 的话老客户端不带该字段会
被当成「明确不要」，静默少掉分类。前端「开通企业」弹窗对应一个默认勾选的复选框，
取消勾选时**显式提交 `false`**（不是 `undefined`——后者等于没表态，用户取消了个寂寞）。

### 4. 验证记录

| 项 | 结果 |
|---|---|
| 后端 `go test ./...` | 27 包全绿（新增 `internal/cattemplate`）；`golangci-lint` 0 issue |
| 新增 `cattemplate_test.go`（2 例） | `Default()` 返回副本（改过的不外泄）/ 排序 1..N 且无重复 |
| 新增 `TestCreateCompanySeedsCategoryTemplate` | 默认 8 类且落在新企业名下 / 排序 1..8 / icon 为空 / isLocked=false / 名单与设计稿一致 / 显式 false 建 0 条 / 1 号企业不受影响 |
| 前端全量 `vitest`（单 fork） | **60 文件 / 881 例全绿**（本批 +3） |
| `vue-tsc --noEmit` / `eslint` | 0 错 / 0 报错 |
| 新增 `Companies.spec.ts` 3 例 | 默认勾选并提交 true / 取消后提交 false / 关面板再开回到默认 |
| `openapi.yaml` | 补 `seedCategories`（顺带补上一直漏写的 `loginCode`）；`yaml.safe_load` + 悬空 `$ref` 校验通过 |
| 反向验证 ×6 | ①模板循环置空 → 后端红；②忽略显式 false → 后端红；③模板写进 1 号企业 → 后端红；④`Default()` 直接返回共享切片 → 模板包 2 例红；⑤去掉 payload 字段 → 前端 2 例红；⑥去掉 `resetCreate` 里的复位 → 前端 1 例红。均还原后全绿 |

> 顺带修的既有测试：`TestMultiTenantIsolation` 原先拿「乙公司分类数为 0」当隔离证据。
> 模板落地后这句话不再成立（乙公司合法拥有 8 条自己的分类），改为在建企业时显式
> `seedCategories: false`——那条用例验的是租户隔离，让模板进来会把所有计数改成「8 + n」，
> 隔离意图被淹掉；模板本身另有专项用例（含「必须落在新企业名下」）。
>
> 测试桩也补了一处：`Companies.spec.ts` 的 `ElDialog` 桩原先声明了 `emits: ['closed']`
> 却从不触发，于是「关掉弹窗再打开，勾选态是否复位」这类用例在桩上永远测不到。
> 加上 `watch(modelValue)` 后桩才和真组件行为一致。

## 第十一轮实施记录（裸启动落地页改为名片墙，2026-10-28）

### 1. 现场问题

「发布体验版后，扫体验版二维码进小程序，依然让我先输入员工编号」。

要分成两层看，混在一起会得出错误结论：

- **微信机制（不是缺陷）**：体验版二维码是**裸启动**，微信不会带任何参数——既没有
  `staffId` 也没有 `scene`。能带参数的只有员工小程序码（`scene=staffId=`）与分享卡片。
  所以「扫体验版码看到某个员工的名片」这件事在微信层面就做不到。
- **产品缺陷（是）**：拿不到 `staffId` 时首页走了 `promptStaffId()`——一个
  `wx.showModal({ editable: true, title: '输入员工编号' })`。代码注释自己写着
  「联调取数 / 演示用」「缺省兜底」，但它实际成了**裸启动时唯一会出现的界面**：
  客户第一次打开自家小程序，看到的就是一个要求输入数字的弹窗。

### 2. 做法

- **新增名片墙** `pages/team/team`：公司抬头（LOGO + 企业名）+ 全员宫格（头像 / 姓名 /
  职位），点谁看谁。作为 `app.json` 的 `entryPagePath`——**不**动 `pages` 数组第一项：
  分享链接写死了 `pages/index/index?staffId=`，把首页换成名片墙会让所有历史分享失效。
- **新增公开接口** `GET /api/team`：只列 `status=enabled` 员工，只回
  `staffId/name/title/avatar` 四个字段。`model.Employee` 上挂着 Phone / Phone2 / Email /
  Landline / OpenID，直接序列化等于把全员手机号交给任何扫过码的人——白名单是硬要求。
  同时回 `total`，人数超上限（`MaxTeamList=300`）时端上要能说清「只显示了前 N 位」。
- **删掉 `promptStaffId`**：无 staffId 且无登录态时 `wx.reLaunch` 到名片墙。
  保留 `app.globalData.myStaffId` 兜底（员工从「我的」返回自己名片时靠它，那条路径无参数）。
- **列裁剪踩到的坑**：员工姓名是 PII 密文列，`AfterFind` 钩子靠 `name` 列解密。
  `Select` 里漏掉 `name` 不会报错，只会让宫格上每个人都没有名字——`teamBriefCols`
  的注释里写明了这一点。
- **`/api/team` 进 `tenantAwarePath` 白名单**：漏了它，平台模式下访客会看到「平台缺省
  企业（1 号）」的全员名单，一头雾水且看不出错在哪。
- 名片墙四种状态分开画：加载失败（可重试）/ 企业不可用（不存在或停用）/ 公司还没人
  （不是坏了，是还没人开通）/ 正常列表。混在一起访客只会以为系统坏了。

### 3. 验证记录

| 项 | 结果 |
|---|---|
| 后端 `go test ./...` | 27 包全绿；`golangci-lint` 0 issue |
| 新增 `TestPublicTeamWall` | 停用员工不上墙 / `total` 只数启用 / 字段白名单（phone·email·landline·openid 一律不出现）/ 姓名解出明文 / 按 staff_id 升序 / 租户隔离 / 企业不存在 code=1 |
| 前端全量 `vitest` | **61 文件 / 889 例全绿**（新增 `team.spec.ts` 10 例；`index_home.spec.ts` 3 例改 1 例） |
| `vue-tsc --noEmit` / `eslint` | 0 错 / 0 报错 |
| `audit_class_collision.py` | 无跨模块全局类冲突（新类一律 `team-` 前缀） |
| `openapi.yaml` | 新增 `/api/team`（含 `CompanyScope` 参数）；`yaml.safe_load` + 悬空 `$ref` 校验通过 |
| 真机数据核对（devserver 实测） | `code=0`、`company.id=1`、`total=1`、字段恰为 `avatar/name/staffId/title`；`companyId=999` → `code=1`；`Cache-Control: public, max-age=60`；响应体里**搜不到**演示员工手机号 |
| 反向验证 ×4 | ①多输出 `phone` → 白名单用例红；②去掉 `status=enabled` → 「停用员工不该上墙」红；③跳名片页去掉 `companyId` → 2 例红；④去掉 `reLaunch` → 「跳名片墙」红。均还原后全绿 |

> 测试里踩到的坑（已记入注释）：`team.spec.ts` 的 `vi.mock` 路径写成 `'../../utils/request'`，
> 从 `tests/` 出发那指向仓库根而不是 `miniprogram/`，mock 静默不生效、真实 request 被调用，
> 表现是「spy 零次调用 + 页面走 error 态」——与「代码有 bug」长得一模一样。
> 同级目录的 spec 一律用 `'../utils/…'`。

### 4. 仍然存在的边界

- **平台模式 + 本机未粘滞企业**：裸启动不知道是哪家企业，会落到后端缺省（1 号企业）。
  零数据新装时 1 号企业不存在 → 名片墙显示「该企业暂不可用」。独立版（`pinned_company_id`
  非 0）没有这个问题。彻底解决需要「平台首页/企业选择」，属于平台模式的产品形态问题，
  不在本轮范围。
- 名片墙不做分页（宫格 + 上限 300），企业人数上千时需要虚拟列表或分页。

## 第十二轮实施记录（小程序默认入口可由企业管理员配置，2026-10-28）

### 1. 需求

上一轮把裸启动的落点定成了名片墙，但这个落点是**写死在客户端**的：所有企业一样。
而「一张名片打天下」的企业（老板本人 / 只有一位销售就是公司门面）希望扫码直接进
那位员工的名片，省掉一层宫格。要求**企业管理员自己就能改**，不必找平台。

### 2. 做法

- **迁移 0034** 给 `company_infos` 加 `mp_entry_mode`（缺省 `wall`）与 `mp_entry_staff_id`。
  缺省取 `wall` 是为了**存量企业升级后行为不变**——升级不得突变存量是硬约束。
- **「配置原值」与「生效值」都回传**：`mode` 是管理员设的，`effectiveMode` 是访客实际
  看到的。指定员工被停用/删除时服务端回落 `wall`，两者不一致本身就是要提示管理员的信息；
  只回生效值等于把管理员的设置悄悄改掉，只回原值则等于撒谎。
- **回落不改配置原值**：员工重新启用后设置自动恢复生效，不需要管理员再点一次保存。
- **写库用 `map`**：`staff_id` 归零是常态（切回名片墙）。GORM 结构体 `Updates` 会
  **跳过零值**，旧 `staff_id` 永远清不掉，表现为「切回名片墙了，但访客还进那个人」。
- **PUT 严格校验，不静默兜底**：非法 `mode` 直接拒绝（不是 `NormalizeMpEntryMode` 兜成
  `wall`——那会让前端写错值时表现为「保存成功但没变」）；`staffId` 必须是**本企业**员工
  （否则能把别家销售挂到自己小程序首页）且 `status=enabled`（入口指向停用员工，
  访客点进去只看到「该员工名片已停用」，首页直接报废）。
- **`/api/team` 随列表下发 `entry` 并去掉 `cdnCache`**：入口设置改完，管理员会立刻扫码
  验证，缓存 60 秒会被当成「没保存成功」。
- **小程序端**：名片墙页拿到 `entry.mode==='staff'` 时 `wx.reLaunch` 到该员工名片再
  `return`，不渲染宫格。
- **管理端页面**放在「企业内容 → 小程序入口」（不是 `platformOnly` 的系统设置）：
  这是企业自己的内容决策，平台超管可 `?companyId=` 代改。员工选择用远程搜索 +
  250ms 防抖 + 只取 20 条（员工可能上千人，本地过滤既慢又漏）。

### 3. 验证记录

| 项 | 结果 |
|---|---|
| 后端 `go test ./...` | 28 包全绿（含 `cmd/devserver`）；`golangci-lint` 0 issue |
| 新增 `TestMPEntry` | 缺省 wall / 设 staff 后生效 / 员工停用后**公开接口回落但管理端仍显示原值** / 跨租户拒绝 / 员工不存在拒绝 / staffId=0 拒绝 / 非法 mode 拒绝 / 切回 wall 时 staff_id 归零 |
| 新增 `TestMPEntryTenantAdmin` | 企业管理员能改本企业 / 改别家不生效 / 乙公司管理员不能指向甲公司员工 |
| 前端全量 `vitest` | **62 文件 / 902 例全绿**（新增 `MpEntry.spec.ts` 10 例） |
| `vue-tsc --noEmit` / `eslint` | 0 错 / 0 报错 |
| `openapi.yaml` | 新增 `/api/admin/mp-entry`（GET/PUT）与 `MpEntry` schema；`/api/team` 补 `entry`。`yaml.safe_load` + 悬空 `$ref` 校验通过（41 个 $ref 全部可解析） |
| 守门（本轮补） | `AdminLayout.spec.ts` 新增两条：①**每个带 `meta.group` 的路由都必须在侧边栏有菜单项**；②**每个菜单项都必须指向真实路由**。前者正是本轮漏掉的：路由和页面都加了，菜单数组是手写的，忘了加 → 功能做完用户永远找不到入口 |
| 反向验证 ×7 | 前端 3 条：①名片墙分支不传 `staffId` → 「显式提交 0」红；②去掉 mismatch 判断 → 停用/删除两例红；③去掉 250ms 防抖 → 防抖用例红。后端 4 条：去掉回落 / 裸结构体写库 / 去掉租户校验 → 各自对应用例红。均还原后全绿 |

> 守门测试抓到一个真问题：本轮加完 `handler.RegisterMPEntry` 后忘了注册到 `cmd/devserver`，
> `TestDevRoutesMatchProduction` 直接报「devserver 缺少路由注册——联调点开该功能会静默 404」。
> 生产 `cmd/server` 有、devserver 没有的偏差，靠人眼是看不出来的。

### 4. 仍然存在的边界

- 名片墙页先挂载、拿到 `entry` 后才 `reLaunch`，配置成「指定员工」时访客会看到宫格
  **闪一下**。改动面最小的做法（`entry` 与列表同包下发，不额外加一次请求），接受。
- 入口设置只影响裸启动；员工小程序码、转发卡片、海报码一律不受影响（这是刻意的，
  否则员工发出去的名片会集体指向别人）。
- 平台模式下裸启动仍落到后端缺省企业（未粘滞时=1 号企业），与本设置无关，
  属于上一轮记录的同一类边界。
- 入口设置不校验席位/授权到期：指向的员工超出席位时，名片页自己会按授权状态拦。

### 5. 菜单调整：两项合并为「小程序设置」（同日）

反馈：**「小程序入口」与「首页布局」都是小程序端的界面设置项，合并更易理解**。

合并的依据不是「听起来像一类」，而是它们回答同一个问题——**客户打开小程序看到什么**：
入口决定访客先落在哪一页，首页样式决定那一页长什么样。其余「企业内容」菜单
（分类 / 案例 / 服务）配的是「往小程序里填什么」，性质不同。

做法与取舍：

- **一个菜单项 + 两个页签**（`content/mp-settings`：「访客先看到谁」/「名片首页样式」），
  而不是同屏上下两块。两页是**两个接口、两次保存**，同屏会有「改完上一段点了下一段的
  保存」；页签把两次保存分到两屏。`el-tab-pane` 用 `lazy`——不写这个属性，进页面就会
  同时打 `/admin/mp-entry` 与 `/admin/home-layout` 两个请求（页面照样能用，只是白打
  一次、两个 loading，光看界面看不出来）。
- **组件不动结构，只加一层壳**：`MpEntry.vue` 与 `HomeLayout.vue` 各自保留（含已有测试），
  新增 `MpSettings.vue` 用页签内嵌，两个组件加 `embedded` prop 隐藏自己的页面标题
  （否则页签名与卡片标题重复）。
- **旧路径保留重定向**：`/content/home-layout` → `?tab=appearance`，
  `/content/mp-entry` → `?tab=entry`。文档、收藏、群聊天记录里的链接不能变成空白页。
  页签状态写进 URL，照着文档发链接给同事，打开就是那一页。

验证：`MpSettings.spec.ts` 8 例（默认只拉入口配置 / 切页签才挂载首页样式 / 切走再切回
不重复请求 / `?tab=` 定位与脏参数回落 / 页签写回 URL / 旧路由重定向 / `embedded` 透传）；
反向验证 ×3：去掉 `lazy` → 3 例红；断开 `@tab-change` → URL 用例红；`fromQuery` 忽略
参数 → `?tab=appearance` 用例红。
