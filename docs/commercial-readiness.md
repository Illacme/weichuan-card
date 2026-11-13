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
| B1 | **授权与强制层 + 租户解析 + 多租户授权已落地（Phase 1+2+3+4）** | 统一 `Entitlement` + `LicenseResolver`（FileResolver 验 Ed25519 签名 / DBResolver 读系统设置）+ 启动校验（私有验签失败/过期 `log.Fatalf` 拒绝启动）；运行时强制经 `handler.EnforceLicense`（`handler.New` 统一注册）：过期只读（`{code:2}`）、席位上限（`{code:403}`）、功能开关 `RequireFeature`（`{code:403}`）；`tenant_license` 表 + `ResolveFor` 按租户解析。**到期提醒（2026-11-02 补齐，见第十三轮）**：`GET /api/admin/license` + 后台顶部横幅 + `[LICENSE][EXPIRING]` 日志 → `alert_check.sh` R5 | 私有化一单一签可绕过（合同约束）；SaaS 售卖仍缺"到期自动停服"与自助续订入口 | **已落地**。待办：见 B1-a（席位上限被绕过）、Phase 5 B2 许可决策（已落地）。**「自动降权但没人知道」这个洞已补**：三层提醒（客户横幅 / 平台汇总 / 运维告警），详见第十三轮 |
| **B1-a** | **席位上限被「申请审批」路径绕过（✅ 已修复）** | **修复前**：`EnforceLicense` 仅拦截 `c.FullPath() == "/api/admin/employees"`；但 `POST /api/admin/apply/open/:id/approve` 经 `db.Create(&emp)` 直接建员工，**不经过席位检查**——两条建号路径只卡了一条。**修复后**：① 席位判定抽成共享函数 `seatExceeded(db, ent, cid)`；② 中间件与审批路径**都调用**它（审批路径按**申请所属租户** `a.CompanyID` 判，不是操作者所在租户）；③ 顺带修掉两处**未判 error 的 `db.Create`/`db.Save`**（此前建号失败仍返回 `code:0` + 一个不存在的 staffId + 永远登不进去的初始密码，且申请单被标成 approved） | 修复前：租户管理员可反复审批开通申请，绕过授权席位上限 → 客户超额不付费 | 已落地。配套测试：席位已满时审批被拒（且**库里不能留下员工记录**）、席位未满时审批正常通过 |
| B2 | **许可已改为商业专有（Phase 5 已落地）** | `LICENSE` 由 MIT 改为**专有许可 + 客户端有限权利**（版权 Illacme）：禁部署/修改/逆向/转售/对外 SaaS/移除声明/**规避内置授权校验**；第九条保留历史 MIT 版本不可撤销权利。第三方组件清单见 `NOTICES.md` + `docs/third-party-licenses.md`（Go 44 / npm 128，无 GPL 类）。许可签发私钥**未进仓库**（已核实） | 原 MIT 风险已消除 | **已落地**，收尾完成 |
| B3 | **按租户存储用量 + 配额（✅ 已落地）** | **修复前**：无 quota 相关代码，上传只受单文件上限 `upload_max_mb`。**修复后**：① 新表 `storage_usage`（`company_id` 主键 + `bytes`/`files`/`quota_mb`，迁移 `0032`），上传时 `clause.OnConflict` 原子累加；② 上传前 `checkStorageQuota` 拦截（三个入口：后台/员工上传、匿名申请头像、对象存储直传 presign）；③ 取值链**租户覆盖 → 全局 `storage_quota_mb` → 0 不限**；④ `GET /api/admin/storage`（本租户用量）+ `PUT /api/admin/storage/quota`（平台超管改覆盖）；⑤ 企业管理列表带 `storageBytes`、新增「授权与配额」弹窗 | 单租户可无限上传；按量计费/成本封顶无从谈起 | **已落地**。口径：**自功能启用起累计**，历史文件不回溯（对象存储无租户元数据、本地 `/uploads` 是扁平目录）；覆盖写不扣减；直传签名成功即记（否则完全统计不到）；查询失败**不拦上传**（宁可少算一次） |
| B4 | **平台模式默认试用授权（✅ 已落地）** | **修复前**：`tenant_license` 表为空时 `ResolveFor` 回落全局设置（不限席位/不过期）→ SaaS 新租户**零约束**。**修复后**：① 设置新增 `license_trial_enabled/days/seats`（缺省 14 天 / 5 席）；② 新增 `license.TrialSpec` + `CreateTrialLicense`/`SetTenantLicense`；③ **开通企业时事务内落一条试用授权行**；④ `GET/PUT /api/admin/companies/:id/license` 供运营转正/延期/改席位；⑤ 企业列表带授权状态，「授权与配额」弹窗四档提示（未配置/已过期/试用中/正式） | "SaaS 收不回钱"在机制层未闭合 | **已落地**。存量企业**刻意不补行**（升级不能让已付费客户突然变只读）；试用天数改动只影响「以后新开的」（不按 `company.created_at` 现算，否则改天数会让所有在用租户的到期日一起漂移）；到期日按 `YYYY-MM-DD → 当天 23:59:59` 落库 |

---

## 三、运维兜底（P2：出事那天才发现）

| # | 缺口 | 现状（证据） | 影响 | 建议动作 |
|---|---|---|---|---|
| C1 | **前端报错已上报（已落地）** | `frontend/src/main.ts` 三处捕获 → 自建 `POST /api/client-error`（无鉴权公开端点，每 IP 限流 60/分 + 64KB + DEV 不落库 + `X-Request-Id` 关联） | 用户白屏/失灵时服务端此前收不到信号 | **已落地**：代码与单测就位 |
| C2 | **备份/恢复/演练脚本已落地 + 演练已实跑留档（✅ 已闭环，2026-11-02 补全为「三件套」）** | `backup.sh`（SQLite `VACUUM INTO` 在线快照 + MySQL `mysqldump --single-transaction` + 轮转 + 体检）**→ 追加调用 `backup_files.sh`**：`uploads-*.tar.gz`（图片）+ `secrets-*.tar.gz.enc`（`runtime.env` 含 `PII_ENC_KEY` + `license.key`，AES-256 + PBKDF2 20 万轮，口令 `BACKUP_ENC_KEY` 单独保管，落盘即自检可解）；`restore.sh`（恢复前自动备份 + WAL 清理 + 回滚指引）+ `restore_files.sh`（图片/密钥恢复，覆盖前另存安全网）；`restore_drill.sh`（不碰生产的演练，未真正校验不得报 PASS，**第 5 项验图片与密钥包**，结尾打印可粘贴留档正文）。**已实跑**：2026-10-27 SQLite/MySQL 各一次；2026-11-02 容器与宿主两种形态各跑通「备份→恢复→逐字节比对」，并验证「缺口令整次失败且不留产物」。记录存档在 `docs/drill-records/`。**同日后台补了「备份与恢复」面板**（`handler/backup.go` + `views/platform/Backup.vue`）：三件套各自最新一份的文件名/大小/新鲜度（缺失也占一行并明说）、SQLite 形态可「立即备份」、库与图片可流式下载；**密钥包不提供下载**（后端直接拒，否则等于把钥匙和数据放回同一故障域）、**恢复不在后台做**（必须走 `restore.sh` 的确认流程）。面板补的是「看不见」这个洞，不替代 crontab——按钮不会自己按，失败也不告警 | 没演练过的备份不算备份；**只备库 = 恢复出来缺图 + PII 解不开** | **已闭环**。剩余是节奏问题：每季度一次（升级迁移 / 改备份脚本 / 换部署形态后补跑），记录继续存 `docs/drill-records/YYYY-MM-DD-<形态>.md`。**`alert_check.sh` R3 已扩展到图片/密钥包新鲜度**（各自独立告警键，避免共用冷却窗口互相吞掉） |
| C3 | **审计日志保留期（✅ 已落地）** | **修复前**：`auditlog.go` 只 `Record`，无归档/清理。**修复后**：① 设置新增 `audit_retention_days`（Group security，**默认 0 = 永不自动清理**）；② 新增 `internal/audit/purge.go`——`PurgeAuditLogs` 分批删除（每批 2000、单 tick 上限 10 万，避免大事务长时间持锁）、`StartAuditLogPurge` 定期跑（间隔 `AUDIT_PURGE_MINUTES`，默认 12h，启动时先清一次）；③ 保留天数**每次重新读设置**（界面改完即时生效），并回落环境变量 `AUDIT_RETENTION_DAYS`；④ 清理动作本身记一条 `audit.purge` 审计（新增 `ActorSystem` 操作者类型，前端显示「系统」），失败也留痕但**不落错误文本**（DB 错误可能带连接串） | 两三年后单表拖慢查询，且长期留存 PII 本身是合规负担 | **已落地**。默认 0 是刻意的：删审计不可逆，升级当天清掉存量＝把本来就有的证据变成再也拿不回来（与 B3/B4 同一条「升级不得突变存量」纪律）。要合规留存策略由运维显式填 180/365 |
| C4 | **不能水平扩展** | 小程序码/access_token 进程内存缓存（`wechat.go:50-53`）；`license.Current()` 亦为进程内存 | 多副本各算各的 token/授权态，限流按实例分桶 | 商用多副本前必须先上 Redis（token + 限流桶 + 授权态 + 缓存） |
| C5 | **数据静态加密（✅ 已落地）** | **修复前**：PII 明文落库。**修复后**：新增 `internal/pii` 包（AES-256-GCM + `v1:` 版本前缀 + 随机 nonce；盲索引 `HMAC-SHA256` 密钥由主密钥域分离派生）。范围**全量**：`employees`（姓名/手机/手机2/邮箱/昵称）、`open_applies`（同五列）、`card_modify_applies.snapshot` 整段。模型三段式（明文 `gorm:"-"` + 密文列沿用原列名 + `*_bidx`），配 `BeforeCreate` 加密 / `AfterFind` 解密钩子；`BackfillPII` 幂等回填（空跳过 / 明文加密 / 密文补索引）；迁移 `0030`/`0031`（含 `snapshot` 改 LONGTEXT——密文是 base64 不是合法 JSON）。登录按 `phone_bidx` 等值；后台搜索对加密列改盲索引等值（`title/landline/openid` 仍 LIKE） | 备份盘/数据库文件泄露 = 大规模个人信息泄露 | **已落地**。生产必须设 `PII_ENC_KEY`（base64 32 字节）；未设时回落内置 dev 密钥并打 `[SECURITY][WARN]`（不拒启，保证 clone 即可跑）。已知取舍：加密字段的后台搜索由模糊收窄为精确。**备份件加密（2026-11-02 部分补上）**：`runtime.env` 等密钥单独打成 `secrets-*.tar.gz.enc`（口令 `BACKUP_ENC_KEY` 与备份分离保管），库备份本身仍是 gzip 明文——但 PII 列在库里就是密文，没有那把钥匙不可读；客户若要求「整包加密」，需再叠一层（备份目录 / 对象存储 SSE） |
| C6 | **CI 密钥扫描（✅ 已落地）** | **修复前**：五 job 无任何密钥扫描。**修复后**：新增 `.gitleaks.toml`（`[extend] useDefault = true` + `[allowlist].regexes` 精确抑制测试假值，**单表不是 `[[allowlist]]`**）+ `ci.yml` 新增 `secrets-scan` job（`fetch-depth: 0` 才能扫历史提交）。本地 8.30.1 实测：无白名单时 6 处命中全为 `generic-api-key` 假值（`middleware-test-key-*`、伪 AppSecret、`from-env-key-*`、文档占位 JWT），加白名单后 **0 leaks** | 密钥一旦进 git 历史极难彻底清除 | **已落地**。仍须遵守「PR 禁止提交真实密钥」；测试内嵌微信上传私钥为假 fixture（`mp_release_upload_test.go:39` `MII-fake-global-key`） |
| C7 | **EXIF/GPS 剥离（✅ 已落地；缩略图仍缺）** | **修复前**：`upload.go` 全文无 `exif`/`Strip`（Grep 零命中），用户上传原样落盘。**修复后**：`internal/storage/image.go` 新增 `ProcessImage(ext, data)`——jpg/jpeg/png **解码再编码**即剥离全部元数据（含 GPS）；gif/webp/mp4 透传；解码失败 fail-open（不因坏图阻断上传）。**缩略图仍未做**（见 E4） | 用户照片带 GPS = PIPL「行踪轨迹/精确定位」敏感个人信息 | **EXIF 部分已落地**；缩略图/压缩见 E4 |
| C8 | **集中日志 + PITR（✅ 已落地）** | **修复前**：日志仅后端本地文件、纯文本；`backup.sh` 不含 binlog（RPO=日备）；`/metrics` 无来源限制。**修复后**：① 新增 `internal/logx`——`LOG_FORMAT=json` 时**包装标准 log 的输出目标**，既有 `log.Printf` 一行不用改就变成 JSONL（级别从既有的 `[WARN]` 等方括号标签推断，认不出按 info、绝不丢行；连续标签取最严重的一个，避免 `[MIGRATE][FATAL]` 被当成 info）；访问日志字段化（`method/path/status/latency_ms/request_id`），文本模式下格式一字不改；② compose 三个服务都配了 `json-file` 日志轮转（10m×7，防止容器日志撑爆磁盘），另附 `deploy/promtail/config.yml`（Loki）与 `deploy/logrotate/weichuan.conf`（非容器部署）；③ **PITR**：compose 的 mysqld 默认带 `--log-bin --server-id=1 --binlog-format=ROW`，新增 `scripts/backup_binlog.sh`（FLUSH 滚动 → 逐个复制并 `gzip -t` 校验 → 校验全过才 `PURGE`，顺序不能换；`log_bin=OFF` 时报错退出而不是空跑成功）；④ `/metrics` 加第二道门 `METRICS_ALLOW_CIDR`（**写错即整体拒绝**，fail-closed） | 多副本/跨机排障受限；误删只能回到日备点 | **已落地**。已知取舍：SQLite 形态无 PITR（没有 binlog 这类增量日志），对数据零丢失有要求的客户应走 MySQL；binlog 回放（PITR 的后半段）尚未做演练——在那之前可承诺的是「恢复到最近一次备份」 |

---

## 四、工程门禁（P3：不是缺陷，是「以后不欠债」的保障）

| # | 缺口 | 现状（证据） | 建议动作 |
|---|---|---|---|
| D1 | **lint 门禁（✅ 已落地）** | 后端新增 `backend/.golangci.yml`（v2 配置，跟着 Go module 走）：`errcheck / govet / ineffassign / staticcheck / unused` 五个——**只留「报出来一定是问题」的静默失败类，不留风格类**；CI 按官方安装脚本取固定版本 v2.15.0（不用 action：action 大版本与 linter 大版本是两套编号）。前端新增 `eslint.config.mjs`（ESLint 9 flat）：未使用变量/导入 + Vue 模板硬错误 + 少数 JS 陷阱；`vue-tsc` 仍是类型门禁，lint 不重复做类型检查。**存量已清零**：29 处后端（20 处 `defer Close()` / 清理路径的显式 `_ =` + 说明、2 处 ShouldBindJSON 按「理由可选」显式丢弃、1 处 ineffassign、1 处未使用的测试 helper）+ 25 处前端（真未使用的导入/变量）全部修掉，现在两边都是 0 issue | 静默失败没人发现（`errcheck` 最典型：丢弃的 error 编译过、vet 也过） | **已落地**。刻意不启用：gofmt（存量文件有 Go 版本导致的注释排版差异，一开就是上百处噪音）、`no-explicit-any`（测试桩里大量 any，逼着加 disable 反而淹没有效信号）、`vue/multi-word-component-names`（组件名与路由一一对应） |
| D2 | **依赖漏洞扫描（✅ 已落地，2026-11-03 起转正式门禁）** | CI 新增 `vuln` job：`govulncheck ./...`（按**实际调用到的函数**判定，比单纯比对版本准）+ `npm audit --omit=dev`（只看运行时依赖，构建链告警量极大且不进产物）。~~job 级 `continue-on-error: true`~~ **已摘除**：2026-11-03 运行时基线清零（follow-redirects GHSA-mrwp-f3q8-3g9p 升 1.16.2，`npm audit --omit=dev` = 0），按本行原定计划转阻塞 | 依赖链上有 CVE 没人知道 | **已落地并转门禁**。口径注意：门禁只看**运行时依赖**；`npm ci` 装包时打印的全量树摘要（含 dev 工具链，如 vitest 的 tinypool critical）只是提示行、不影响退出码——dev 依赖不进产物，修它要升 vitest@5 大版本，噪音不应阻塞合并。之前 CI 上「26 vulnerabilities」红的全貌：那行其实是 `npm ci` 的摘要，真正让 job 失败的只有 follow-redirects 这 1 条 high |
| D3 | **CI 构建镜像（✅ 已落地）** | CI 新增 `images` job：用 buildx 分别 build `backend/` 与 `frontend/` 两个 Dockerfile（`push: false`，带 gha 缓存） | `go build` / `npm run build` 绿只代表代码能编译，Dockerfile 里的 COPY 路径、多阶段产物名、运行镜像缺 tzdata/wget 这类问题只有真 build 才暴露——全是「本地能跑、客户装不上」 | **已落地** |
| D4 | **端到端冒烟（✅ 已落地，2026-11-09 起转正式门禁）** | 新增 `scripts/e2e_smoke.sh`：真起一个进程（临时目录 + 临时端口，不碰现有数据），走完 **安装向导建库 → 登录拿 httpOnly Cookie → 建企业 → 配企业信息 → 建员工 → 小程序侧 `/api/card` 读回刚写的数据**，逐步 PASS/FAIL 并带退出码。CI 新增 `e2e-smoke` job；~~job 级 `continue-on-error: true`~~ **已摘除** | 单测与接口测试**结构上覆盖不到**「真进程 + 自重启 + 两端衔接」这一层 | **已落地并转门禁**。当初按「先非阻塞积累稳定性数据」执行：最近 12 次 CI 该 job 全部 success（含 2 次其它 job 失败的 run），脚本已有动态端口 / `--noproxy '*'` / 探活重试 / 临时目录清理，担心的环境抖动未出现；2026-11-09 本地再跑一次 14 步全 PASS 后摘掉 `continue-on-error` |

---

## 五、体验细节（P4：成本低、感知强）

| # | 缺口 | 现状（证据） | 建议动作 |
|---|---|---|---|
| E1 | **导航栏标题写死客户名（✅ 已落地）** | **修复前**：`app.json` 全局 `navigationBarTitleText` 写死演示租户名；`pages/index/index.wxml` 自绘顶栏、`index.ts` 分享标题的兜底文案也是同一个客户名；`brand_name` 已缓存但无人用于标题。**修复后**：① 全局标题改产品名「微传名片」；② 新增 `utils/brand.ts`——`resolveBrandTitle` 按「当前企业 → `brand_name` → 产品名」解析，`applyBrandTitle` 设导航栏；③ 海报页拿到 `card.company` 后按企业设标题；④ 顺手给 4 个继承全局标题的页面补功能性标题（编辑名片 / 登录 / 案例详情 / 服务详情）；⑤ 顶栏与分享标题兜底改为留白 / 产品名 | **多租户下客户看到别人家公司名**——提审截图里一眼可见的「没改干净」 | **已落地**。守门：`tests/brand.spec.ts` 扫全目录断言不含演示租户名，并比对 `app.json` 全局标题 === `FALLBACK_BRAND` |
| E2 | **源码残留开发者局域网 IP（✅ 已落地）** | **修复前**：`utils/config.ts` `DEFAULT_BASE_URL = 'http://192.168.31.224:8080'`（明文 http + 私有网段），而种子基址是拉取后台配置的**唯一入口**。**修复后**：① 常量**默认为空**，源码不含任何地址；② 新增 `hasBaseUrl()`，`fetchPublicSettings` 与 `app.ts onLaunch` 在基址为空时**不发请求**（否则拼出非法 URL 白等 5s 超时）；③ `fetchPublicSettings` 归一末尾斜杠（避免 `//api/` 被 nginx 前缀匹配漏掉）；④ `preflight.sh` 第 3 节把「空值」也判 FAIL（原先空值只 `skip`，等于没拦），保留 `PREFLIGHT_ALLOW_PRIVATE=1` 放行本机联调 | 别人部署后拿**别人家的内网地址**当首连地址，必然超时且后台配基址也救不回来，只能重新打包提审 | **已落地**。守门：`tests/config.spec.ts`（默认必须空 / 若填只允许 https 或回环 / 空基址不发请求），反向验证时「空基址仍发请求」那条实测耗时 5.0s——正好是白等的那个超时 |
| E3 | **nginx 未开压缩、静态资源无强缓存（✅ 已落地）** | **修复前**：`frontend/nginx.conf` 只有安全头与反代，无 `gzip on` / `expires`。**修复后**：① `gzip on` + `gzip_types` 仅列文本与可压缩字体（ttf/otf），媒体/已压缩二进制（png/jpg/webp/woff2/mp4）不烧 CPU；② `/assets/`（Vite 内容哈希产物）加 `Cache-Control: public, max-age=31536000, immutable`；③ SPA 壳 `index.html` 加 `Cache-Control: no-cache` 确保发版即失效；④ 因 nginx `add_header` 继承陷阱，两个新 location 重新声明了三个安全头，静态资源不丢 nosniff/frame/referrer | 首屏 JS/CSS 传输体积偏大、二次访问命中率低 | **已落地** |
| E4 | 用户上传图片无压缩/无缩略图（✅ 已落地） | 内置素材缩略图离线生成，用户上传没有；EXIF 已由 C7 剥离 | 上传时按长边限制重编码（同时完成压缩与缩略图），列表页改用缩略图 URL | **已落地**。修复后：① `storage.ProcessUpload` 对 jpg/png 按长边 2000 重编码（压缩）+ 额外生成 480 长边缩略图；三后端（Local/MinIO/OSS）的 `Save` 均写入 `<hash>_thumb.<ext>` 兄弟对象（best-effort，失败仅 WARN 不阻断主图）；② 小程序首页三宫格（分类图标/精选案例/服务）、案例列表、名片墙头像，以及后台 `ImageThumb` 统一组件，均改用缩略图 URL；③ 缩略图缺失（存量上传 / 外链 / SaveBytes 生成的二维码素材）由 `binderror`（`@error`）回退原图，零裂图风险；④ 全端不改 `Save` 签名与数据库契约；⑤ **存量图回填工具** `cmd/thumbbackfill`（幂等、原图只读、可反复执行，`-dry-run`/`-force`/`-dir`，见 `docs/deploy-runbook.md` §4.5）——升级后不跑等于「每张图多打一次 404」，比不用缩略图更慢；⑥ 修复缩放插值 `uint8` 回绕缺陷（`float64(v1-v0)` 在 v1<v0 时按 256 回绕成噪声，输出体积膨胀 2.3 倍且肉眼可见噪点），此前**所有**新上传图都受影响。守门：`backend/internal/storage/image_test.go`（`TestProcessUpload*` + `TestResizeContainKeepsGradient` 递增/递减两个方向）、`backfill_test.go`（幂等/dry-run/force/原样存/小写扩展名，8 条）、MinIO/OSS `Save` 断言「两次 PUT 且缩略图更小」+ 小程序 `tests/thumb.spec.ts` + 前端 `src/utils/thumb.spec.ts` |

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
9. ~~B1「到期自动降权但没人知道」；C2「备份只有脚本、看不见」~~ —— **两项已于 2026-11-02 收口**（第十三轮授权到期三层提醒；C2 行补「备份与恢复」面板）。这两项同一类：**机制早已生效，但生效这件事本身没人被通知**。

> 已落地的 A1、B1、B2、C1、C2（代码侧）不在推进顺序内，见文末「已就位」。

---

## 已就位、不需要重复讨论的项

避免后人把已经做过的事再提一遍：

- 部署配置逐项核对 → `docs/launch-checklist.md` + `scripts/preflight.sh`
- 上线后最小告警（healthz / 5xx / 备份新鲜度 / 外部拨测/证书 / 授权到期） → `scripts/alert_check.sh`
- 备份 / 恢复 / 演练（SQLite 与 MySQL 两条路径，含图片与运行时密钥） → `scripts/backup.sh` · `backup_files.sh` · `restore.sh` · `restore_files.sh` · `restore_drill.sh`（`C2` 已闭环）
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

时间约束不变：**必须在首次写入业务数据之前设定**——一旦已用 dev 密钥写入 PII，换钥后旧数据再也解不回来（没有 re-key 机制）。走向导的部署不必手工处理：`scripts/backup.sh` 会把 `runtime.env` **加密**纳入备份（`secrets-*.tar.gz.enc`，口令 `BACKUP_ENC_KEY` 单独保管）。

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

> **✅ 后续订正（2026-11-09 复核：两条都已转正式门禁）**
>
> - D2 于 2026-11-03 清零运行时基线后摘掉 `continue-on-error`（见第十六轮 §2）。
> - D4 于 2026-11-09 摘掉：最近 12 次 CI 该 job 全部 success，脚本的动态端口 /
>   `--noproxy '*'` / 探活重试已消掉起先担心的 runner 抖动；本地复跑亦全绿。
> - 同日还把 `backend-mysql` 从「需配 `TEST_MYSQL_DSN` 才触发」改成 service 容器
>   自供给的必跑门禁。**现在 12 个 job 全部阻塞合并**，本节「先非阻塞」是当时的
>   阶段性结论，保留作追溯，不再是现状描述。

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

## 第十三轮实施记录（授权到期三层提醒，2026-11-02）

### 1. 缺口

授权到期是这套系统里**唯一会静默发生的降级**：`EnforceLicense` 到点自动把写操作拦成
`{code:2}`，私有部署更是 `license.Verify` 失败、**直接拒绝启动**。机制本身早就在跑
（Phase 2 起），但**没有任何东西会主动告诉任何人**——客户是某天上午突然改不了内容
才知道，运营是客户打电话来问才知道。自动降权 + 无人知晓 = 客户以为系统坏了。

一句话：**这不是「缺功能」，是「缺通知」**。而通知要能穿过「没人登录」这道墙。

### 2. 做法：三层，各对一个不同的人

| 层 | 谁看到 | 依赖 |
|---|---|---|
| 客户 | 登录后台即见顶部横幅（`GET /api/admin/license` 的 `self`） | 需要有人登录 |
| 运营 | 同一接口的 `alerts` + `expiring`（平台超管视角，过期优先、最多 10 家） | 需要有人登录 |
| 运维 | 每天扫一次写 `[LICENSE][EXPIRING]` → `alert_check.sh` R5 → webhook | **不需要任何人登录** |

第 ③ 层是重点：它是私有部署（没有平台后台）**唯一**的指望。只做 ①② 的部署，
等于把「发现」寄托在客户先发现。

- **档位 30 / 7 天**：30 天是「联系客户 → 走付款流程 → 钱到账」的现实耗时，再短来不及；
  7 天是「提醒还起作用」的最后窗口（客户还没被停服，还有动力续）。
  档位 `none / ok / warn / urgent / expired`，阈值与判定集中在 `license/expiry.go` 一处。
- **「还剩几天」按日历天算**：到期落库是当天 23:59:59，若用 `Hours()/24`，早上 9 点看
  「明天到期」会算出 0.6 → 显示 1 天，而客户心里的答案是「明天」。两边对不上，
  就会有人来找你对账。`DaysBetween` 先把两边归到本地零点再 `Round`，顺带吸收
  23/25 小时的夏令时天。
- **审计只记跳档，不记天**：`license.expiring` 只在 `warn → urgent → expired` 时写一条。
  同一档位连续 20 天写 20 条，只会把真正的变更淹掉——**审计是证据链，不是闹钟**。
- **续费后要忘记档位**：某项不再属于「临期集合」时删掉内存里的档位记录，否则这个
  客户续过一次之后就**永远**收不到下一次提醒。
- **停用企业不提醒**：不服务流量的企业混进名单，只会让名单越来越长，直到没人看。
- **两种形态文案必须分开**：平台 / SaaS 续费即时生效、无需重启；私有部署必须换
  `data/license.key` **并重启**（到期即拒绝启动）。给私有部署客户看「无需重启」，
  等于让他们一直等到过期才动手——那不是文案瑕疵，是一次可预防的停机。
  顺带修掉一处**既有错误文案**：原只读提示统一写「请续费后重启」，对 SaaS 是错的。

### 3. 验证记录

| 项 | 结果 |
|---|---|
| `go test ./internal/license/` | 全绿（`expiry_test.go` 10 例边界：31 vs 30 天、8 vs 7 天、当天、过期 1 天；`remind_test.go` 5 例：过滤 / 平台全局 / 跳档才记审计 / 续费后忘记 / 坏库不 panic） |
| `go test ./internal/handler/` | 全绿（`license_view_test.go` 3 例：租户只拿自己的 / 平台汇总含包含关系与排序 / 无授权行回落 `none`） |
| `alert_check.sh` R5 | 临时目录实测：首次跑触发 `license_expiring` → 再跑静默（偏移已推进）→ 追加 `level=expired` 行触发**另一个键** `license_expired` |
| 前端 | `licenseBanner.spec.ts` 10 例 + `AdminLayout.spec.ts` 38 例（含 5 例横幅）全绿；`vue-tsc` / `eslint` 干净 |
| 反向验证 | 去掉「续费后忘记」→ 对应用例红；告警键合并成一个 → 过期告警被冷却吞掉（R5 实测复现） |

### 4. 仍然存在的边界

- **私有部署的提醒再早也救不了「没人看 webhook」**：R5 推出去之后要有值班的人。
  真无人值守的私有部署，仍建议客户自己在日历上记到期日——这一点写进了 runbook §8.6。
- **不主动发邮件 / 短信给客户**：现有通知渠道只有后台横幅与 webhook 两条，
  给终端客户发信要接短信/邮件服务商，属新增外部依赖，本轮不做。
- **扫描是进程内的**：多副本部署下每个副本各扫一次（日志会重复，审计有跳档去重兜底）。
  C4 上 Redis 之后一并收敛。

## 第十四轮复检记录（全面复扫，2026-11-03）

### 1. 这次复检的方式变了

前十三轮是「按清单逐项核对文档与代码」，这一轮是**实跑验证**：后端全量测试 +
前端 vitest + 小程序 spec + 对线上站点逐路径探测。目的是让结论**不带历史的惯性**——
上一轮说「已落地」，这一轮要重新跑一遍确认它今天还成立。

| 项 | 结果 |
|---|---|
| 后端 `go test ./...` | 27 个包 —— **修复前 `internal/license` 1 条 FAIL**，其余 26 个 ok |
| 前端 vitest | 76 文件 / **1118 用例全绿** |
| 小程序 spec（32 个） | **571 用例全绿** |
| 线上路由 | `/` `/login` `/companies` `/employees` `/admin` `/settings` 均 200（带 `Accept: text/html`） |
| 线上 `/healthz` | 200（此前那个 301 重定向死循环**已修复**，本轮实测确认） |
| 线上敏感文件 | `/.env` `/auth.json` `/package.json` `/go.mod` 全 404 |
| 线上安全头 | nosniff / X-Frame / Referrer-Policy / X-Robots-Tag 四项齐全 |
| 线上缩略图 | 抽查 avatar / album / cat / logo 的 `_thumb` 均 200 —— **回填已执行完毕** |

### 2. 发现并修复：一条会随日历自己爆炸的用例

**这不是「昨天写坏的测试」，是「每天都会重新爆一次」的测试。**

`license.StatusOf(e, now)` 里，`DaysLeft` 按传入的 `now` 算，`Expired` 却绕开它去调
`e.IsExpired()`（内部 `time.Now()`）——同一个函数里**两套时间基准**。

测试 `TestStatusOfLevels` 把 `now` 固定在 2026-11-02 09:00，于是：

- **2026-11-02 当天**：真实时钟还没越过当天 23:59:59 → level 判 `urgent` → **绿的**
- **2026-11-03 起**：真实时钟越过到期点 → level 判 `expired` → **必红**

已用确定性推演确认时间窗（11-02 20:40 PASS / 11-03 09:52 FAIL），并核对 CI 记录：
最后一次绿是 2026-11-02 12:40 UTC（= 20:40 CST），**确实卡在当天**——下一次 CI 推送必红。

**为什么这比「少一条测试」严重**：一条会随日历走红的用例，会让人以为边界已被钉住，
而实际上它证明的只是「在我写它的那天没问题」。它比没有用例更危险，因为它提供**虚假的安全感**。

**修复**：新增 `Entitlement.IsExpiredAt(now)`，`StatusOf` 改用它，`Expired` 与 `DaysLeft`
共用同一基准。`IsExpired()` 保留（内部即 `IsExpiredAt(time.Now())`），三处
「此刻的真实判断」（强制层降级 / 降级判定 / 许可证文件校验）语义不变。

**回归用例改为日期无关**：`now` 取 2001 年（真实时钟下必然已过期一万年），
于是任何时候跑都成立。已做反向验证：摘掉修复 → 新用例立即 FAIL → 还原 → 全绿。

提交 `eab5d1c`。

> 推广成一条纪律：**固定 `now` 参数的测试，必须确保被测函数完全由 `now` 驱动。**
> 被测函数内部一旦偷用 `time.Now()`，测试就只在「写它那天」成立。

### 3. 验证后排除的三个误报（不写进问题清单）

复检的价值一半在于**不误报**。以下三条都曾像是缺陷，实测后确认不是：

| 曾怀疑 | 实测结论 |
|---|---|
| MySQL DSN 无 `loc=Local`，驱动按 UTC 读写 → 日期差一天 | **不成立**。写读保持同一瞬间，`DaysBetween` 的 8 小时偏移被 `Round` 吸收，`ExpiresAtDate()` 日期不差 |
| 线上 `/login` 返回 404 → SPA 兜底没上线 | **不成立**。是 curl 没带 `Accept: text/html`，命中 `@spa` 的 404 分支；线上兜底用的是「按 Accept 区分导航与扫描器」的更严格写法，行为正确 |
| 小尺寸图也生成 `_thumb` 兄弟对象 → 浪费存储 | **不是缺陷**。种子素材 16 张 PNG 里 11 张 ≤480 边，缩略图边长 480 > 原图 200，本就不该缩；走「原样存」兜底是正确行为 |

### 4. 当时开放的项（本轮新增 1 条；该条已闭环，见下方订正）

**`/uploads/` 的 404 被 Cloudflare 永久缓存**（实测 `cf-cache-status` MISS → HIT）。

`Cache-Control: public, max-age=31536000, immutable` 对 404 同样生效——
`add_header ... always` 无法按状态码区分，于是「文件不存在」这个**应该短命**的响应
被 CF 锁死一年。

- **当前无实际伤害**：线上回填已执行完毕（所有图都有 `_thumb`），不再产生 404；
  新上传的图 `Save` 同时写原图 + `_thumb`，也不会缺。
- **但仍是未引爆的雷**：任何在回填**之前**被访问过的存量图，其 404 会被 CF 锁死，
  之后再回填也取不到——回填工具变得无效，且没有任何报错。
- **修法方向**：`/uploads/` 的 404 需要单独降级缓存。`add_header` 做不到按状态码区分，
  要用 `map $status $cache_control` 或让后端在 404 时返回正确的 `Cache-Control`。
  属于 nginx 侧改动，等确认要动线上配置时一并处理。

> **✅ 后续订正（2026-11-07 复核：此条已闭环，不再是开放项）**
>
> 本条已于 2026-11-03 当日修复并上线，三处证据：
> 1. `deploy/baota/nginx-site.live.conf` 头部新增 `map $status $uploads_cache`——
>    200/206/304 仍是 `immutable` 一年，404/410/5xx 降为 `no-store`，其余 `no-cache`；
>    `/uploads/` 改用 `add_header Cache-Control $uploads_cache always` 按状态码分流。
> 2. 行为守门在 `tools/nginx-check/verify.sh`（两条断言：404 不带 `immutable`、
>    200 仍是 `immutable`）；事故与修法全文见 `docs/deploy-runbook.md` §5.2.5
>    「✅ 已修复：404 也曾被缓存一年」及 `tools/nginx-check` §3.6。
> 3. 线上实测（2026-11-07，只读 HEAD 一个不存在的对象名）：`HTTP/2 404` +
>    `cache-control: no-store` + `cf-cache-status: BYPASS`——404 已不再进 CF 边缘缓存，
>    回填补上文件后客户端可立即取到。
>
> 另核实：`frontend/nginx.conf`（Docker 形态）与 `deploy/baota/nginx-site.conf`
> （粘贴用简版参考模板）的 `/uploads/` **本就没有** `Cache-Control` 头，
> 不存在同类雷，无需同步改动。**本轮真正待办的只有下面「其余开放项」列出的几条。**

其余开放项沿用前几轮结论，本轮未变化：C4 水平扩展（需 Redis）、binlog 回放未演练、
真机验收、备份演练的季度节奏、以及**代码外必须人工完成**的三项
（MP 后台配《用户隐私保护指引》、私有部署换真 `LICENSE_PUB`、手工部署设 `PII_ENC_KEY`）。

> **✅ 后续订正（2026-11-09 复核）**：本段是 2026-11-03 当时的快照，其中两条已闭环——
> ① 《用户隐私保护指引》于 2026-11-07 在 MP 后台审核通过（5 类权限 + 用户标识信息），
> 原串行阻塞解除；② 用户 2026-11-08 确认 **#163 真机验收全部通过**，
> `docs/real-device-acceptance.md` 已留档并把勾选表标为通过；
> ③ 用户 2026-11-10 确认**小程序工信部备案早已通过**（A3 条目与下方 :144 的
> 「需人工确认备案号」是当时口径），`launch-checklist.md` 阻断级与 §四 已改为 ✅，
> 上线窗口执行表也把它从「最长串行前置」里移除。
> **现在仍开放的**只有：C4 水平扩展（Redis，架构演进、暂缓）、binlog 回放演练、
> 备份演练的季度节奏、私有部署换真 `LICENSE_PUB`、手工部署设 `PII_ENC_KEY`、
> 以及 E3 的 CF 面板手工配置——后四类都必须在外部系统由人操作，仓库代码无法代劳。

## 第十五轮实施记录（PII 密钥接入系统设置页，2026-11-03）

### 1. 缺口

`PII_ENC_KEY` 是 C5 全部承诺的支点（员工手机号/姓名在库里只存密文），但它的状态
**只能登服务器看**：设没设、设的是不是自己签发的那把，界面上没有任何呈现。
运维核对一次要 SSH + cat runtime.env；而「忘了设（ALLOW_INSECURE 放行后裸奔）」
这种最危险的静默状态，在产品里完全不可见。

### 2. 做法：归入只读档的第 5 项，而不是可写项

PII 密钥**刻意不做**成可在界面修改的配置，与 db_dsn/jwt_key 同档只读展示。原因比
「改错锁死」更硬：

1. **落库即击穿 C5**。可写项存 sys_setting——密钥若也在库里，库备份就自带解密钥匙，
   「数据库泄露也读不出明文」当场作废；`secrets-*.tar.gz.enc` 与库备份分开保管的整个
   设计就是为了把钥匙挡在库外。
2. **无重加密能力**。换钥不会重加密存量密文，界面上「顺手换个新钥」等于把全部已写入
   的 PII 变成解不开的死数据，且没有任何报错。

落地形态：`setting.registry` 追加 `pii_enc_key`（`Secret + ReadOnly + EnvVar: pii.KeyEnvVar`，
环境变量名引用 pii 包常量防两处漂移）。复用既有机制，零新增接口：
只读项不落库（seedDefaults 跳过 / Set 拒绝并点名两条改法路径）、值走 `preview()`
首尾各 4 位的可辨认预览、`SettingRow` 按 `readOnly` 自动渲染为只读框 +
「已配置 / 未配置·用缺省值」徽标——「未配置」在这里的真实语义是
**「PII 正用源码内置开发密钥加密，形同明文」**，是页面上一眼该看到的红色项。
上线就绪检查同步加入该条（未配置时给可复制的 `PII_ENC_KEY=<openssl rand -base64 32>`
写法 + 形同明文的后果说明）。

### 3. 验证记录

| 项 | 结果 |
|---|---|
| `go test ./internal/handler/` | 全绿：只读五项元数据断言（readOnly/envVar）、PII 预览 = `AAEC****Hh8=`、PUT 拒绝且文案含 `PII_ENC_KEY` 与 `data/runtime.env`、不落库（`sys_setting` 无行）、未设置时 `configured=false` 且无预览值 |
| `go test ./...` | 26 包全绿 |
| 前端 Settings.spec | 56 例全绿：就绪数 4/7、PII 行进「环境变量 / 配置文件」组、未配置态给可复制写法与后果提示 |
| `vue-tsc --noEmit` | 干净 |
| 反向验证 | registry 摘掉 `ReadOnly` → 只读断言与「不落库」断言立即 FAIL，还原后全绿 |

### 4. 仍然存在的边界

- **展示不改变改法**：密钥仍只能经环境变量 / `data/runtime.env` + 重启修改，
  界面只负责「看得见状态」——这是刻意的，见上。
- **换钥风险只靠文案拦**：Desc 写明「已写入的数据换钥后无法解密」，但若运维真换了钥，
  系统不会（也无法）阻止启动——旧密文在读取时报「解密失败」，属显式失败而非静默。
  真正的密钥轮换（re-key）是独立特性，未排期。


## 第十六轮实施记录（E3 gzip 兜底 + D2 基线清零转门禁，2026-11-03）

### 1. E3 收尾：站点级 gzip 兜底（live.conf 补齐与参考版的差距）

**缺口**：E3 主体（frontend/nginx.conf 的 gzip + /assets 强缓存）早已落地，但**线上用的
是宝塔站点配置**（`nginx-site.live.conf`），其中没有 gzip 段——压缩全靠宝塔面板
「性能调整 → gzip」的全局开关。全局依赖意味着：换机器 / 重装面板 / 有人关掉开关，
本站立刻裸奔，且没人会想到「站点配置里本来就没写」。参考版 `nginx-site.conf` 有兜底段，
live 版漏了——两边本该一致。

**做法**：live.conf 补上与参考版相同的 gzip 段（`gzip on` / `gzip_vary` /
`gzip_min_length 1024` / `gzip_proxied any` / `comp_level 6` / `gzip_types` 只列文本与
ttf/otf——png/jpg/webp/mp4/woff2 不进，白烧 CPU）。同名指令「重新定义即覆盖」，
全局开着时行为一致，全局消失时本站不裸奔。文件头改动清单加第 13 条。

**验证**：`tools/nginx-check` 断言 83 → **87 项全绿**，新增 §6.5 四条：
>1024B 文本资产压缩、Vary 头、小响应不压缩（min_length 生效反证）、png 不压缩
（gzip_types 语义）。线上实测压缩本就生效（HTML/JSON/assets 均 `content-encoding: gzip`），
本改动是把「生效」从面板全局收编为站点自持。

**E3 剩余为纯 CF 面板手工项**（runbook §5.2/§5.3 已有完整规则表与验证命令，代码侧无剩余）：
① Browser Cache TTL 改 Respect Existing Headers（否则 /uploads immutable 被覆盖成 4h）；
② HTML 边缘短缓存 60s 规则（TTFB 1.1s → 0.2-0.3s，前置：发版不清空 assets/）。

### 2. D2 收尾：运行时基线清零，vuln job 转正式门禁

**真相反驳了表象**：CI 上一直显示的「26 vulnerabilities (22 moderate, 2 high, 2 critical)」
是 **`npm ci` 装包时顺带打印的全量树审计摘要（含 dev 工具链）**——它不决定退出码。
真正让 job 失败的是 `npm audit --omit=dev` 的 **1 条 high**：
follow-redirects GHSA-mrwp-f3q8-3g9p（跨域重定向丢弃 sensitiveHeaders）。
「26 个」与「1 个」在 CI 日志里上下相邻，极容易误读成同一件事。

**做法**：
1. `npm audit fix`（官方源）→ follow-redirects 1.16.1 → 1.16.2，lockfile 仅 9+/21-，
   package.json 零改动。`npm audit --omit=dev` = **0 vulnerabilities**。
2. 摘掉 vuln job 的 `continue-on-error: true`，job 名去「（非阻塞）」——按本表原定
   「基线清干净再摘」的计划执行。门禁口径写在注释里：只看运行时依赖；
   `npm ci` 的全量摘要（dev 侧 tinypool critical 要升 vitest@5、minimatch/editorconfig
   链等 25 条）是提示行不算红，dev 依赖不进产物，不为它阻塞合并。

**验证**：

| 项 | 结果 |
|---|---|
| `npm audit --omit=dev`（官方源） | 0 vulnerabilities |
| eslint | 0 issue |
| `vue-tsc --noEmit` | 干净 |
| vitest 全量 | 76 文件 / 1119 例全绿 |
| `vite build --outDir dist-check` | 成功 |

### 3. 当时开放的项（本轮划掉后；现状见下方订正）

- C4 水平扩展（需 Redis）、binlog 回放未演练、#163 真机验收（清单已备）、
  #231 向导测试与文档、E3 的两项 CF 面板手工配置。

> **✅ 后续订正（2026-11-09 复核）**：这里列的是 2026-11-03 当时的开放项，
> 其中 `#231 向导测试与文档` 已被**紧随其后的第十七轮**收口（标题就写着「第十七轮
> 实施记录（#231 …收口）」），列在同一份清单里是自相矛盾的历史残留；
> `#163 真机验收` 由用户 2026-11-08 确认全部通过并留档。
> 现存开放项以本报告末尾「第十八轮」的清单为准。

## 第十七轮实施记录（#231 安装向导测试与文档收口，2026-11-03）

### 1. 结论先行

向导的**安全与主流程**测试面本已很厚（同源/反代伪造/信封/双引擎 E2E/零数据/PII 顺序），
本轮补的是**失败路径与半配置语义**——「装到一半坏了，运维面对什么」此前零覆盖，
而文档声称「任何一步失败都不会留下半配置」与「安装中途失败不可重试」恰恰落在这些分支上。

### 2. 新增测试（backend/internal/setup/install_test.go）

| 用例 | 钉住的行为 | 反向验证 |
| --- | --- | --- |
| `TestInstallConfigFileWriteFailureNotRetryable` | 配置目录不可写 → 报错含自救办法（手动创建配置文件）、不留残缺文件、**库已初始化且重试被新鲜库守卫拦**（守卫改「清空重装」= 重放安装页成重置漏洞，此断言第一个红） | 逻辑推演（守卫删则第二条 Install 走到重复 SeedAdmin，报错文案变化必红） |
| `TestInstallDemoDataFailureDegradesToWarning` | 演示数据写失败（UPLOAD_DIR 是文件 → MkdirAll 必败）→ **安装仍成功** + warnings 含「演示数据未完全写入」+ 企业照常写入。这是「不可重试」的直接推论：此刻报错重试只会把运维卡死在半路 | ✅ 临时把降级改为报错 → FAIL；还原 → 绿 |
| `TestInstallKeepsExistingPIIKey` | 环境已提供 PII 密钥时**不写进配置文件**（install.go 刻意只在现场签发时落盘；重装覆盖 = 存量密文变天书） | ✅ 临时改成无条件写入 → FAIL；还原 → 绿 |
| `TestInstallValidationErrors` +3 例 | 用户名含空格 / 超 64 字符、JWT 密钥含换行。**顺带发现**：validateAdmin 先 TrimSpace，尾部换行被静默剥掉（宽容），只有内部换行触发拒绝——用例必须打内部，打尾部会假绿 | — |
| `TestInstallEndpointEnvelope` 增强 | `cookieSecure:true` 必须落盘 `COOKIE_SECURE=true`（HTTPS 部署漏勾的表现是「能登录但换浏览器丢登录态」，极难关联到安装页） | — |

### 3. 文档收口（deploy-runbook 新增 §2.2）

§2.1 只写了 Docker 路径；二进制/宝塔部署的向导行为（实际线上就是这么装的）此前
散落在记忆里而非文档里。新增 §2.2：触发条件（DB_DSN 三处皆空）、四个前置
（CONFIG_FILE 相对 WorkingDirectory、目录可写、UPLOAD_DIR 可写、unit 必设
WorkingDirectory）、HTTPS 站点必勾 Cookie Secure、装完立即备份 runtime.env
（三把密钥，PII 丢 = 敏感数据永远解不开且无报错）、半配置的正确处理与「不可重试」
的原因。§C 第 2 步补「更省事的替代：留空走 §2.2」。

### 4. 验证

`go test ./internal/setup/ -count=1` 全绿（TestInstall* 10 函数 + 子测试）；
`go test ./...` 后端全量全绿。

---

## 第十八轮实施记录（CI 门禁实化 + 文档口径收口，2026-11-09）

### 1. 结论先行

用户 2026-11-08 确认 **#163 真机验收全部通过**，代码完成度与验收都不再是瓶颈。
本轮把「仓库里能自己解决、却因为没人报错而一直挂着」的几件事一次收掉：
**两条失效的 CI 门禁实化**、**文档与源码注释的口径漂移订正**、**验收结论留档**。
外部系统必须人本人操作的事项单列清单，不假装做完。

### 2. 两条 CI 门禁此前其实是「绿着没跑」

| 门禁 | 改造前的事实 | 证据 |
| --- | --- | --- |
| `backend-mysql` | `if: ${{ vars.TEST_MYSQL_DSN != '' }}` + `env: ${{ vars.TEST_MYSQL_DSN }}`，仓库没配这个变量 | 最近 12 次 CI **全部 skipped**，从没真跑过 MySQL parity |
| `e2e-smoke` | job 级 `continue-on-error: true`，挂了也不阻塞 | 最近 12 次 CI **全部 success**（含 2 次其它 job 失败的 run），脚本已有动态端口 / `--noproxy '*'` / 探活重试 / 临时目录清理 |

改造：`backend-mysql` 去掉变量门控，DSN 写死在 job 的 `env`，由 `mysql:8.0` service
容器自供给；`e2e-smoke` 摘掉 `continue-on-error`。现在 **12 个 job 全部阻塞合并**。

### 3. 真跑一次立刻抓出一个从未被发现的 parity 缺陷

本地起与 CI 同构的 `mysql:8.0.46` 容器后，按 CI 的命令逐条跑：

- `TestMySQLMigrations`：PASS；
- `TestAPIFlowMySQL`：**7 个子测试集体 FAIL**，全是 `company id=1 record not found`；
- `TestInstallMySQLEndToEnd`：PASS。

根因：`setupSQLiteEngine` 会建 1 号默认租户，而 `TestAPIFlowMySQL` 只
`TRUNCATE` + 应用迁移，**漏了这一步**；迁移按硬规则只建表结构、不含 `INSERT`。
多租户改造之后这条 parity 就一直是坏的，但因为 CI 从没跑过它，没人知道。

修法：抽出 `seedDefaultCompany(t, db)`，SQLite 与 MySQL 两条链路**共用**同一个函数；
`TestAPIFlowMySQL` 在 `applyMigrations` 之后补齐种子。
反向验证：临时去掉该调用 → `exit=1`、7 个子测试 FAIL；还原 → 全绿且文件字节一致。

> 三条 `grep '^--- PASS: ...'` 断言也随之加进 CI：`go test` 遇到 `t.Skip` 退出码
> 仍是 0，不钉住「用例真的 PASS 了」，下次 DSN 丢失或用例改名又会「绿着跳过」。
> 同 job 还补上 `TestInstallMySQLEndToEnd`（安装向导 MySQL 分支，含 CREATE DATABASE）。

### 4. 文档 / 注释口径订正

历史段落一律用「✅ 后续订正」块保留原结论，不改写当时的判断：

- D4 表格行、§5「先非阻塞」、第十六轮 §3「仍然开放的项」、第十四轮「其余开放项」；
- `docs/branch-protection.md` 还停留在「五个 job，其中四个必跑」，改为 12 个全必跑；
- `backend/README.md` / `docs/deploy-runbook.md` §6 / `docs/launch-checklist.md` /
  `部署与联调.md` 里「配 `TEST_MYSQL_DSN`」「四个必跑 job」「先非阻塞」等旧口径；
- `backend/internal/wechat/sec.go` 包注释仍写图片异步回调「尚未接入」，
  实际 `internal/handler/media_check.go` 的三段式链路早已落地，改为指向实现并说明
  仍需运营在 MP 后台配推送 URL/Token/AESKey；
- `docs/real-device-acceptance.md` 勾选表标为通过并补验收记录（用户口述确认，无逐项截图存档）。

### 5. 验证

| 项 | 结果 |
| --- | --- |
| 本地 MySQL 8.0 容器真跑三条用例 | 全绿（parity 14 个子测试全 PASS） |
| 反向验证 + 自动还原 | 去掉种子 exit=1 / 7 FAIL，还原后 exit=0 |
| `scripts/e2e_smoke.sh` 本地真跑 | 14 步全 PASS，exit=0 |
| CI YAML 解析 | 12 job，无 `continue-on-error`、无 `vars.TEST_MYSQL_DSN` |

### 6. 仍然开放的项（均在外部系统，代码无法代劳）

1. 私有部署换真 `LICENSE_PUB`（换公钥＝旧授权全失效，需与客户约时间）；
2. 手工部署设 `PII_ENC_KEY`（必须在写任何 PII **之前**定下，无 re-key）；
3. E3 的两项 Cloudflare 面板手工配置；
4. binlog 回放演练、恢复演练的季度节奏（运维动作，需留档）；
5. C4 水平扩展（需 Redis）——架构演进，明确暂缓，不是缺陷。

## 第十九轮实施记录（代码级深度审查收口，2026-11-10）

### 1. 背景：上一轮的「已无可写」说早了

用户确认小程序备案早已通过（三份文档的备案事实已订正，见 `a8c2f40`），
随后追问「还能做哪些优化」。此前「仓库侧没有可写代码」只是**发布流程 / CI /
文档 / 开放项层面**的结论，未做代码级审查。本轮派两支只读审查（后端 Go、
前端 Vue3 + 小程序）系统扫描，逐条亲自核实后，把确认属真问题、改动局部、
可反向验证的部分收口。

判据沿用项目自定口径：**没人报错时，用户是否无从察觉？**

### 2. 已修

| 类别 | 问题与证据 | 处理 |
| --- | --- | --- |
| P0 数据正确性 | `content.go` 内容写接口成片丢 GORM `Error`（`company-info` / 分类 / 项目 / 服务 / 首页布局的 Create、Update、Delete），失败仍回 `code:0`；同文件 `company-profile` 早已就同一缺陷修好，属漏网 | 新增 `dbWriteFailed` 统一出口：服务端日志带真实 SQL 错误（最常见是漏同步 `migrations/` 导致的 `Unknown column`），客户端只给固定文案；全部写接口判 `.Error` |
| P0 幂等误判 | 分类 / 服务的 `PUT` 用 `RowsAffected == 0` 判「未找到」，MySQL 原样 UPDATE 会返回 0 → 重复保存被报成「未找到」 | 改为先按租户 `First` 查存在性，再判 `.Error`；`DELETE` 保留 `RowsAffected`（删除不受幂等影响）但补 `.Error` |
| P0 假成功 | `apply.go` 两处改密、`admins.go` 重置平台超管密码丢 `Error`，其中改密在写库**之前**就记了「成功」审计 | 判错并返回；`HashPassword` 也判错；审计移到写库成功之后 |
| P0 错误回显 | 注销 / 审批建号 / 登出等 6 处把 `err.Error()` 拼进客户端 `msg`，会带出表名、列名、约束名 | 固定文案 + `log.Printf` 落服务端；外部厂商 API 的业务错误文案（微信 / 存储 / 发布）**刻意保留回显**，那些是运维排障必需的信息，不属同一类 |
| P0 PII 旁路 | `auth.go` 登录失败审计写 `TargetID: req.Phone` 且 `ActorName: e.Name`：员工表 phone/name 是静态加密列，审计表却只追加明文 | 改为 `TargetID = staffId` 字符串、不写姓名；仍能按「同一编号连续失败」还原爆破序列。管理员侧 `TargetID: req.Username` 是账号名、非 PII，按审计包既有约定保留 |
| P0 进程存活 | 全仓 `recover(` 零命中，`gin.Recovery()` 只管请求协程；10 处 `go fn()`（备份 / 订阅 / CDN 刷新 / 媒体送检 / 5 个 ticker / 安装自重启）任一 panic 直接带崩进程 | 新增 `internal/safego.Run(name, fn)` 统一兜住 + 任务名 + `debug.Stack()`；刻意不自动重试（各任务幂等性不同，统一重试会制造重复副作用）；`backup.go` 用闭包 `defer` 保证 `bkState.finish` 一定执行，否则 `running` 永久卡死、备份按钮再也点不动 |
| P1 弱网体验 | 小程序 `employee/stat` 与 `mydata` 的 `load()` 无 try/catch，`loading:false` 写在 `await` 之后 → 网络失败永久转圈且无任何出口 | try/catch/finally + `error` 态 + 可见重试按钮；复用 `app.wxss` 既有 `.error-box/.retry-btn`，不新增全局类 |
| P1 请求竞态 | `useServerTable.load()` 无序号，旧响应会盖掉新页数据并把 `currentPage` 拨回旧值；`store/tenant.ts` 的全量预取后回来会盖掉关键字搜索结果，用户点中的企业已不在候选里而 `companyId` 已写进请求作用域 | 两者各加请求序号，只认最后一次发出的请求；`finally` 按序号决定要不要清 `loading`；组件卸载 / 登出时主动作废在飞请求 |
| P1 资源泄漏 | `CompanyPicker.vue` / `MpEntry.vue` 的防抖 timer 卸载未清理（同类组件都清了） | 补 `onUnmounted` 清理 |

### 3. 核实后**刻意不改**（别当遗漏再修一遍）

- `/api/home-layout` 未挂 `cdnCache(c, 60)`：与 `/api/company` 同属「管理员改完立刻扫码验证」的路由 / 展示配置，挂 60 秒缓存会制造「改了没生效」的客服问题；其它三个内容接口挂缓存是另一类取舍。
- `content.go` 项目 `PUT` 的二次查库不是多余：GORM 的 map 更新不回写结构体，回包要给出新值与数据库刚刷新的 `updated_at`，必须重读。已补注释与 `.Error` 判断。
- 外部 API 的 `err.Error()` 回显：`mp_release` / `wechat` / `storage` 等返回的是厂商业务错误，管理员需要「为什么发布失败」；一刀切改成固定文案会让排障失去线索。
- `miniprogram/utils/types.ts` 12 个类型零引用：属类型化重构，不是缺陷；当前 `request<T>` 泛型通道空转，另立一轮做。
  > **✅ 后续订正（2026-11-10 第二十轮）**：已重写。复核时发现那 12 个 interface 不只是「暂时没用」，
  > 而是**字段全部过时的错误契约**（`Employee` 缺 `phone2`、`CompanyInfo` 缺 `mpEntryMode`/`mpEntryStaffId`、
  > `HomeLayoutResp` 缺 `bgImage`/`bgMask`），留着会把错误写法当成正确示范。现收敛为首页真正消费的
  > 4 个信封，并被 `pages/index/index.ts` 引用；`request<R>` 泛型改为「整个响应信封」后通道真正生效。
  > 详见下方「第二十轮」§7。

### 4. 已识别但**当时**未改（1 / 2 已在第二十轮闭环，见下方订正）

1. 页面级 `load()` 竞态：`StatDetail.vue` / `MPReleaseDialog.vue` / `Stats.vue`、小程序 `project/list.ts` / `team/all.ts` / `employee/stat.ts`（本轮只修了弱网错误态，未加请求序号）。
2. 请求层统一 `AbortSignal` / 取消能力：这才是竞态的根因层修法；本轮用序号守住了最高频的两个载体，改请求封装会牵动所有调用点，需单独一轮 + 全量回归。
3. `C4` Redis 水平扩展、PII 密钥轮换、binlog 回放演练：沿用既有「明确暂缓」结论。

> **✅ 后续订正（2026-11-10 第二十轮）**：第 **1**、**2** 项均已闭环，
> 不再是开放项。两端请求层新增**可选** `signal`（不传则行为完全不变，
> 现有几十个调用点零改动），并抽出 `latestOnly` / `latestGuard` 守门；
> 上述 6 个页面全部接入，含乱序用例与 8 项反向验证。
> 第 **3** 项仍按既有结论暂缓，属架构演进而非缺陷。

### 5. 验证

| 项 | 结果 |
| --- | --- |
| 后端 `go build` / `go vet` / `go test ./... -count=1` | 28 个包全绿 |
| `golangci-lint v2.15.0 run --timeout=5m` | 0 issues |
| 本地 MySQL 8.0 容器 parity | `TestAPIFlowMySQL` 14 个子测试 + `TestMySQLMigrations` 全 PASS |
| 前端 eslint / `vue-tsc --noEmit` / Vitest 全量 | 0 错误；88 文件 / 1456 例全绿 |
| `vite build`（`--outDir dist-check-*`） | 成功 |
| 小程序类名冲突守门 / 第三方许可清单 / 发版脚本自检 | 无冲突 / 一致 / 50 PASS 0 FAIL |
| `scripts/e2e_smoke.sh` | 全链路 PASS |
| `git diff --check` | OK |
| **反向验证 6 项**（PII 审计、写库失败、panic 屏障、两个序号、弱网错误态） | 逐项改回旧实现 `exit=1`，脚本 `try/finally` 还原后复跑 6 项全 `exit=0`，工作区无残留 |

新增守门：`backend/internal/handler/db_write_test.go`（DROP TABLE 与 GORM Update 回调注入真实 SQL 失败 + 源码级「裸写库」扫描）、
`backend/internal/safego/safego_test.go`（panic 兜住 + 全仓裸 goroutine 扫描）、
前端 `useServerTable.spec.ts` / `tenant.spec.ts` 乱序用例、小程序两页弱网与 WXML 错误态用例。


---

## 第二十轮实施记录（请求竞态的根因层收口，2026-11-10）

### 1. 背景

第十九轮把「页面级 `load()` 竞态」和「请求层统一取消能力」如实留成开放项。
本轮把这两项一起收掉：不再让每个页面各写一套序号，而是让请求层具备**可选**的
取消能力，再抽一个守门工具给页面复用。后端零改动，不影响任何接口契约。

判据仍是项目自定口径：**没人报错时，用户是否无从察觉？** 旧响应盖掉新结果
不会抛错、不会打日志，只会让运营看到「切了筛选数据没变 / 提交成功却像没生效」。

### 2. 已修

| 类别 | 问题与证据 | 处理 |
| --- | --- | --- |
| 根因层（前端） | `frontend/src/api/request.ts` 没有取消路径；`axios` 原生支持 `signal`，但三个会连查的 api 函数没把 config 透出去 | `adminStats` / `adminStatsDetail` / `listMPReleases` 各加**可选** `AxiosRequestConfig`；新增 `api/latest.ts` 的 `latestOnly()`（`next` / `isCurrent` / `cancel`）；error 拦截器对 `isCancel` / `ERR_CANCELED` 静默，否则连点两次筛选会弹一条莫名的「网络异常」 |
| 根因层（小程序） | `miniprogram/utils/request.ts` 的 `wx.request` 返回 `RequestTask` 却丢弃，无法中断在途请求 | 新增 `utils/latest.ts`（`latestGuard` / `SupersededError` / `isSuperseded`）；`request` 加**可选第四参数** `signal`，把 `RequestTask` 挂上去；`success` / `fail` / 401 刷新链各挡一道 `aborted`；取消以 `SupersededError` **reject** 而非让 Promise 悬挂——悬挂会让 `onPullDownRefresh` 的 `finally` 永不执行，下拉指示器卡在屏幕上 |
| 前端 3 页 | `Stats.vue` 连切日期、`StatDetail.vue` 翻页 / 换筛选 / 下钻、`MPReleaseDialog.vue` 打开 / 翻页 / 提交后刷新，旧响应都会盖掉新结果 | 各持一个 `latestOnly()`；`onUnmounted` 取消在飞请求；响应写回与 `finally` 清 loading 都判 `isCurrent`；`MPReleaseDialog` 的 `@closed="reset"` 也作废在飞请求 |
| 小程序 3 页 | `project/list` 连点分类、`team/all` 搜索与加载更多并发、`employee/stat` 连切时间范围 | 各持 `latestGuard()`，`onUnload` 取消；`project/list` 的**列表与分类条各用一个 guard**（下拉刷新并发发两个请求，共用一个会互相取消）；`team/all` 新轮开始时复位 `loadingMore` 锁并加 `mainInFlight`，否则被取消的追加请求会让「加载更多」永久点不动，或追加请求反过来取消主查询 |
| 测试桩耦合（本轮踩到） | 两端共 25 处 `toHaveBeenCalledWith` / `toHaveBeenLastCalledWith` 精确断言，新增可选参数后集体假红；先前两轮 grep 都漏了 `toHaveBeenLastCalledWith` 这个变体 | 全部补上第 N 个参数的 **正向守门**（`expect.objectContaining({ signal: expect.anything() })` / `expect.anything()`），既适配新签名，也钉死「这些页面必须传 signal」 |

**向后兼容**：两端的 `signal` 都是可选参数。全站几十个调用点一个都不用改，
不传时请求行为与改动前逐字一致；`{ ...config, params }` 的展开顺序保证
调用方无法误传 `params` 改掉日期 / 分页口径。

### 3. 刻意不改（别当遗漏再修一遍）

- **不给 `rawRefresh` / 登录端点加 signal**：刷新是全局单飞，一个页面切走就 abort
  掉刷新，会把同时 401 的其它页面一起拖下水。被取消的查询在
  `refreshing.then` 里自行退场即可。
- **不用 `instanceof SupersededError`**：小程序测试会 `resetModules`，跨模块实例
  的 `instanceof` 会失效（`-target es2018` 下 Error 继承链也脆），统一按 `name` 判定。
- **不做全站请求取消**：只有「同一页面会连续重查」的 6 个页面接入。表单提交、
  写操作接取消会造成「用户以为没提交、其实后端已经成功」的更坏歧义。
- **`team/all` 的 `mainInFlight` 不是多余**：它挡的是「主查询在途时触底追加」，
  否则追加请求的 `next()` 会取消主查询，第 1 页永远回不来。

### 4. 测试与反向验证

6 个页面各补 1 个**乱序用例**：让第一次请求挂起，第二次先返回，再放旧响应回来，
断言最终数据 / 页码 / 标题栏 / 错误态都不被旧响应改写。
两端请求层共补 11 个用例（守门工具语义、取消后不交付旧数据、abort 的 fail 不误报
网络错误、`RequestTask.abort()` 真被调用、401 刷新链中不发重试、axios 取消不弹提示）。
另加源码级静态守门：`frontend/src/api/latest.spec.ts` 扫这 6 个页面，
新增同类页面漏接守门即 CI 红。

**反向验证 8 项**：6 个页面的 `isCurrent` 判定、小程序请求层的 3 处 `aborted` 拦截、
前端 axios 取消静默分支，逐项改回旧实现并跑对应 spec 全部 `exit=1`；
`try/finally` 自动还原后复跑 8 个 spec 全 `exit=0`，工作区无临时残留。

### 5. 全量门禁（2026-11-10）

| 项 | 结果 |
| --- | --- |
| 后端 `go build` / `go vet` / `go test ./... -count=1` | 28 个包全绿 |
| `golangci-lint v2.15.0 run --timeout=5m` | 0 issues |
| 本地 MySQL 8.0 parity | `TestAPIFlowMySQL` 14 个子测试 + `TestMySQLMigrations` + `TestInstallMySQLEndToEnd` 全 PASS |
| 前端 eslint / `vue-tsc --noEmit` | 0 错误 |
| Vitest 全量（含小程序 spec） | **90 文件 / 1484 例全绿** |
| `vite build`（`--outDir dist-check-*`） | 成功，临时目录已清理 |
| `scripts/e2e_smoke.sh` | 全链路 PASS |
| `tools/nginx-check/run.sh` | 95 通过 / 0 失败 |
| `scripts/selftest_backup.sh` / `scripts/selftest_deploy_frontend.sh` | 12/0；50 PASS 0 FAIL |
| 小程序类名冲突 / 第三方许可清单 / `git diff --check` | 无冲突 / 一致 / OK |

### 6. 仍未改（如实留档）

只剩第十九轮 §4 第 3 项：`C4` Redis 水平扩展、PII 密钥轮换、binlog 回放演练——
架构演进与演练事项，不是缺陷，沿用既有「明确暂缓」结论。

第十九轮 §3 的 `utils/types.ts` 类型化重构**已在本轮闭环**（见 §7），不再是开放项。

### 7. 追加：小程序类型契约不再腐烂（同日续做）

**为什么做**：前一节收口后，仓库里唯一还剩的「有证据、能改、有收益」的代码项就是
`miniprogram/utils/types.ts`。原判断是「12 个 interface 暂时没人引用」，复核后升级了：
它们与真实回包**已经对不上**——`Employee` 缺第 219 轮刚加的手机 2 字段 `phone2`，
`CompanyInfo` 缺 0034 迁移引入的 `mpEntryMode`/`mpEntryStaffId`，`HomeLayoutResp`
缺 `bgImage`/`bgMask`。这不是「暂未使用」，而是**一份不存在的契约**，谁照着写谁错。

**为什么能腐烂这么久**：`miniprogram/tsconfig.json` 是 `strict: false` + `allowJs: true`
且没有 wx typings，`tsc --noEmit` 本来就有一批基线错误（`Cannot find name 'wx'` 等），
**不是门禁**；零引用的类型文件既没人用、也没人报错。

**处理**：

| 项 | 处理 |
| --- | --- |
| `utils/types.ts` | 12 个零引用 interface → 首页 `loadAll` 真正消费的 4 个信封 + 元素类型；字段全部由脚本从 `backend/internal/model/models.go` 的 json tag 抽出（不凭印象） |
| `request` 泛型 | 从 `T = data 类型` 改为 `R = 整个响应信封`，默认 `ApiResult<any>`——不传泛型时与改动前逐字等价。原语义撑不住「字段在根级」的 `/api/card`、`/api/achievements`、`/api/home-layout`，这正是全站零使用、通道空转的原因 |
| `pages/index/index.ts` | 4 个请求改成 `request<CardResp>` / `<AchievementsResp>` / `<ServicesResp>` / `<HomeLayoutResp>`，顺带删掉 3 处 `as any` |
| 防再次腐烂 | 新增 `miniprogram/tests/types_contract.spec.ts`（9 例）：TS 字段与 Go json tag 逐项对账；`FeaturedProject`/`ServiceBrief` 是裁剪投影、必须分别不含 `detail`/`album`、`content`；`types.ts` 必须被业务文件 import；每个导出类型至少被引用一次 |

**反向验证 3 项**（`try/finally` 自动还原）：把 `phone2` 写成 `phoneTwo`、删掉 `mpEntryMode`、
去掉 `index.ts` 的 import，三次跑 `types_contract.spec.ts` 全部 `exit=1` 且失败用例精确命中
对应断言；还原后 9 例全绿。

**门禁**：`index_home` / `types_contract` / `request` / `latest` 四个 spec 共 73 例全绿；
后端零改动。

## 第二十一轮实施记录（小程序详情页「更新于」可配置，2026-11-10）

### 1. 背景：这不是缺陷，是设计缺口

项目案例与业务服务详情页原先把 `updated_at` 硬编码渲染成「更新于 + 精确到秒」：

| 位置 | 原实现 |
| --- | --- |
| `miniprogram/pages/project/detail/detail.wxml` | `分类名 · 更新于 {{updatedAtText}}` |
| `miniprogram/pages/service/detail/detail.wxml` | `更新于 {{updatedAtText}}` |
| 取数 | `formatDateTime(r.data.updatedAt)`，固定到秒 |
| 后台可调性 | **无**。运营在后台看不到这个字段，但访客能精确到秒地看到它 |

三个问题（按重要性）：

1. **它没有信息量**：`updated_at` 是「运营最后一次保存的时间」，不是「案例交付时间」。访客
   想知道的是「这个项目哪一年做的」，不是「你们昨天 14:34 改了错别字」。
2. **它会失真**：批量整理内容会让全站案例在同一天集体显示「更新于」，看起来像批量灌水；
   反过来长期不动又显得公司停更。
3. **秒级精度是纯噪音**：工业品名片里出现 `14:34:30` 很出戏。

### 2. 方案取舍（三个候选，选了 A）

| 方案 | 做法 | 结论 |
| --- | --- | --- |
| **A. 档位可配** | 三档 `hidden` / `date` / `minute`，案例与服务**分开控制** | **本轮实施**：纯展示、零业务风险、不改访客看到的内容本身 |
| B. 换业务日期 | 加「发布/交付日期」可选字段，运营自己填 | 更彻底，但需要客户逐条录入，留待内容运营有诉求时再做 |
| C. 自动当前年份 | 每年自动刷新 | **明确拒绝**：对访客是误导，与本项目「内容必须真实」的底线冲突 |

默认档位选 `date` 而不是 `hidden`：升级后存量租户的时间显示**不消失**，只是不再暴露秒级，
属于「变干净」而非「东西不见了」；要彻底隐藏可在后台一键关掉。

### 3. 实现

| 层 | 改动 |
| --- | --- |
| 迁移 | `migrations/0040_detail_time_display.sql`：`home_layouts` 加 `project_time_mode` / `service_time_mode`，`DEFAULT 'date'`，只加列不搬迁；SQLite 由 AutoMigrate 按模型 tag 补齐；已重跑 `gen_migrations_manifest.py` |
| 模型 | `model.HomeLayout` 两列 + `TimeMode*` 常量 + `DefaultTimeMode` + `NormalizeTimeMode`（非法值返回空串，由调用方决定报错） |
| 后台接口 | `GET /api/admin/home-layout` 回两个档位（无配置行时回缺省，不回空串）；`PUT` 用 `*string` 指针语义，只改一栏不会刷掉另一栏；枚举外的值 `code=1` 拒绝，不静默兜底 |
| 公开接口 | `/api/projects/:id` 与 `/api/services/:id` 各回一个 `timeMode`（服务用内嵌结构体保留全部原字段，不改成显式 `gin.H` 免得写丢字段）；`detailTimeMode()` 查不到配置行或查询出错都回缺省——纯展示开关不该让详情页整体失败 |
| 后台页面 | `content/HomeLayout.vue` 加「详情页时间显示」分区，案例与服务各一组 radio，下方实时给出「小程序效果：更新于 2026-11-10」；页签名 `名片首页样式` → `名片页面样式` |
| 小程序 | `utils/datetime.ts` 拆出 `formatDate` / `formatMinute`，新增 `detailTimeText(mode, at)`；两个详情页改为按 `timeMode` 渲染，`hidden` 返回空串让 wxml 整行隐藏（不是显示「—」） |

**时区口径刻意保持不变**：格式化仍在端上做（按设备本地时间），后端只下发档位。改成后端
格式化会在「服务器 TZ 未设」时把时间整体偏 8 小时，属于真实回归风险，收益只是省三行代码。

### 4. 测试与反向验证

| 层 | 覆盖 |
| --- | --- |
| 后端 | `runAPIFlow` 新增子测试「详情页时间档位」：缺省值 → 两列分别设为 `hidden` / `minute` → 两个详情接口各按自己的列下发 → 只提交一栏时另一栏保持 → 非法值被拒绝且不落库 |
| 后台 | `HomeLayout.spec.ts` 新增 4 例：两档位分别回填并原样提交、只改案例不影响服务、只改服务不影响案例、档位下方效果文案 |
| 小程序 | 新增 `tests/datetime.spec.ts`（10 例）；`project_detail` / `service_detail` 各补 `date` / `minute` / `hidden` 三档断言 |

**反向验证 4 项**（脚本 `try/finally` 自动还原）：详情接口忽略列值（恒回缺省）、非法档位静默
兜底、端上忽略 `timeMode`、后台保存不提交档位——四项全部 `exit=1` 且失败用例精确命中对应
断言；还原后全绿。

**新踩的坑**：后台 `HomeLayout.spec.ts` 有 2 处 `toHaveBeenCalledWith` 是**全量参数比对**，
保存负载多两个字段后集体假红；这是项目第三次踩同一类坑（前两次在第二十轮）。

### 5. 全量门禁（2026-11-10）

| 项 | 结果 |
| --- | --- |
| 后端 `go build` / `go vet` / `go test ./... -count=1` | 28 个包全绿 |
| `golangci-lint v2.15.0 run --timeout=5m` | 0 issues |
| 本地 MySQL 8.0 parity | `TestAPIFlowMySQL`（含新子测试）+ `TestMySQLMigrations`（实际应用 0040）全 PASS |
| 前端 eslint / `vue-tsc --noEmit` | 0 错误 |
| Vitest 全量（含小程序 spec） | **92 文件 / 1511 例全绿** |
| `vite build`（`--outDir dist-check-*`） | 成功，临时目录已清理 |
| 类名冲突 / 第三方许可清单 / `git diff --check` | 无危险项 / 一致 / OK |
| `docs/openapi.yaml` | YAML 可解析、单 components 块、45 个 `$ref` 全部可解析；后台 home-layout 的读写契约已补两个档位 |

## 第二十一轮补充：后台案例列表补「创建时间」列（2026-11-10 同日）

用户指出后台「项目案例」列表没有时间列（业务服务列表有「创建时间」，案例没有）。
核查：后端 `/api/admin/projects` 的 `projectToMap` **一直都返回 `createdAt`**，
纯粹是 `Projects.vue` 没渲染——前端一行接口改动都不需要。

- `Projects.vue` 在「上架」之后加「创建时间」列（width 180、`v-if="!isPhone"`、
  `formatDateTime`），与 `Services.vue` 完全同口径；空值显示「—」。
- `Projects.spec.ts` 补 1 例：格式化断言 + 缺值占位；桩里的时间**不带时区后缀**，
  本地解析在任何 TZ 的 runner 上断言都成立。
- 反向验证：删掉该列 → 新用例 `exit=1` 精确命中；还原后 13 例全绿。
- 门禁：eslint / `vue-tsc` / Vitest 全量 **92 文件 1512 例** / `vite build` 全绿；
  后端零改动。既有测试输出里的 `[Vue warn]: Component is missing template` 是
  基线噪音（改动前后均 99 处），与本次无关。

## 第二十一轮订正：两个列表页统一显示「更新时间」（2026-11-10 同日）

用户确认：案例与服务列表都应显示**更新时间**，创建时间回答不了运营真正的问题
——「哪些内容旧了该翻新」（一年前建的上周刚改过，内容并不旧）。

- `Projects.vue` / `Services.vue` 两列均改为「更新时间」，绑定 `row.updatedAt`；
  口径不变（width 180 / 手机端隐藏 / `formatDateTime` / 空值「—」）。
- 后端：`projectToMap` 补发 `updatedAt`（此前只回 `createdAt`，列表列会全显「—」）；
  服务列表直接序列化整个模型、天然带 `updatedAt`，零改动。
- **静默失败守门**：后端漏发该字段时列表只是显示「—」，不报错、不打日志——
  `runAPIFlow` 的「详情页时间档位」子测试补两条断言（案例 / 服务列表都必须携带
  非空 `updatedAt`）。
- 测试桩里 `createdAt`（2026-01-01）与 `updatedAt`（2026-10-16）**故意不同**，
  绑错字段断言立即红——这本身就是内建的反向验证。
- 反向验证 2 项：`projectToMap` 删掉 `updatedAt` → API flow 红；`Projects.vue`
  绑回 `createdAt` → 前端 spec 红。还原后全绿。
- 门禁：后端 28 包 / vet / lint 0 / MySQL parity PASS；eslint / vue-tsc 0 错误；
  Vitest 全量 **92 文件 1513 例**；`vite build` 成功。

## 第二十一轮核查：小程序端支持是否到位（2026-11-10 同日）

用户追问「小程序端都一并做好支持了吧」，按证据逐项核查，结论如下：

| 链路 | 状态 | 证据 |
| --- | --- | --- |
| 两个详情页按后台档位渲染 | 已支持 | `project/detail/detail.ts:53`、`service/detail/detail.ts:41` 均走 `detailTimeText(r.data.timeMode, r.data.updatedAt)`；三档各有 spec 断言 |
| 数据源是更新时间 | 从来就是 | 两个详情页读的一直是 `updatedAt`（非 `createdAt`），与后台列表订正后的口径一致 |
| 后端下发档位 | 已支持 | `/api/projects/:id` 与 `/api/services/:id` 各回 `timeMode`，读 `home_layouts` 的对应列 |
| 后台改档位后端上生效 | 实时 | 两个详情接口**都未挂** `cdnCache`（只有 `/api/services` 列表挂了），每次请求现查 |
| 老后端兼容 | 已支持 | 端上收不到 `timeMode`（未知档位）按 `date` 兜底，不让这一行凭空消失 |
| 「不显示」档位不留残影 | **本轮修复** | 见下 |

**发现并修掉的一处遗漏**：业务服务详情页在 `hidden` 档位时，`updatedAtText` 为空串，
但 `<view class="meta">` 仍渲染——`.detail .meta` 带 `padding-bottom: 16rpx`，
标题与正文之间会留下一段莫名其妙的空白。项目详情页在「无分类名 + hidden」时同理。
现给两处 meta 行加条件：服务页 `wx:if="{{updatedAtText}}"`、
案例页 `wx:if="{{project.categoryName || updatedAtText}}"`。

WXML 不进 Vitest 编译范围，因此补了 2 条**源码级守门**（两个 detail spec 各一条），
钉死「meta 行在两种都为空时不得渲染」；反向验证 2 项（去掉 `wx:if`）均 `exit=1`，
还原后全绿。后端零改动。

**核查边界（刻意没动）**：小程序端只有两个详情页展示时间——案例列表、首页精选/服务卡、
名片墙本来就不显示时间，不存在「列表时间要跟着改」的问题。管理员审核页的
`createdAt`（申请提交时间）与「我的资料」的 `createdAt`（注册时间）是各自业务语义，
不该换成更新时间。

门禁：Vitest 全量 **92 文件 1515 例**全绿；eslint / `vue-tsc` 0 错误；
类名冲突无危险项；`git diff --check` OK。

## 第二十一轮补充：小程序页面样式的模块顺序（2026-11-10 同日）

用户指定的配置模块顺序：**首页背景 → 板块顺序 → 详情页时间显示**。
原页面是「板块顺序 → 首页背景 → 详情页时间显示」，且首块没有标题、后两块才有。

- `HomeLayout.vue` 把「首页背景」整块（背景图 / 遮罩浓度 / 效果预览）挪到最前，
  「板块顺序」次之，详情页时间显示保持最后；三块统一带 `el-divider` 标题，
  避免「只挪表单项、标题留在原地」的错位。
- 板块顺序表单项的 label 由「板块顺序」改为「谁在前」，与模块标题不再重复；
  页面顶部说明文案与 `embedded` 注释同步订正为「名片页面样式」。
- **纯展示重排**：脚本逻辑、保存负载、接口契约零改动（16 个原有用例全部照旧通过）。
- 新增 1 条源码级守门：读 `HomeLayout.vue` 源码，断言三个模块标题的
  `indexOf` 严格递增。模板顺序是视觉约定，挂载测试看不出来，只能钉源码。
- 反向验证：把首尾两个模块标题对调 → 守门 `exit=1` 精确命中；还原后全绿。
- 门禁：eslint / `vue-tsc` 0 错误；Vitest 全量 **92 文件 1516 例**；
  `vite build` 成功；`git diff --check` OK。后端零改动。

## 第二十二轮：小程序配色模板（增值能力，2026-11-10 同日）

**需求**：为便于给客户提供更高级的增值服务，管理后台要能提供「小程序模板支持」。
用户确认第一期范围：**只做配色模板，不动版式**。

### 方案：内置白名单模板包 + theme tokens + license feature gate

刻意**不做**「后台上传自由 WXML/CSS」：小程序代码是编译后上传审核的，端上不能
动态加载 WXSS；自由样式会把内部类名变成对客户的长期承诺（以后改版就是破坏定制），
还附带注入与「深底 + 深字」对比度失控的风险。模板必须随小程序版本内置，后台只记
「选了哪一款」。加一款 = 改代码 + 发版，这是刻意保留的成本。

### 复用的三套既有底座（没有另起炉灶）

1. **按租户展示配置链路**：`model.HomeLayout` 注释里本就写明「新增展示类配置可以
   继续放这里」，后台 `GET/PUT /api/admin/home-layout` 与公开 `GET /api/home-layout`
   现成可用 → 只加 `template_id` 一列（`0041`），不新建表 / 新接口路径。
2. **商业授权**：`tenant_license.features` + `license.ResolveFor().HasFeature()`，
   后台 `Companies.vue` 早已能按企业填 `mp_release` 这类开关 → 新增
   `mp_template_advanced`，无需新授权体系。
3. **代码内白名单包**：`internal/cattemplate`（`Default()` 返回副本）与
   `internal/assets/library`（`go:embed`）都是已验证的模式 → 新包
   `internal/mptemplate` 作为 id 唯一真值来源。

### 改动清单

- 后端：`internal/mptemplate`（3 款：`default` 免费 / `ink` / `warm` 高级）、
  `HomeLayout.TemplateID`、迁移 `0041_miniprogram_theme.sql`（缺省 `default`、
  不搬存量）+ manifest 重生成；admin GET 下发 `templateId` / `templates` /
  `templateEntitled`，PUT 校验白名单；公开 GET 在响应**根级**下发 `templateId`。
- 后台：`MpSettings.vue` 新增第三个 lazy 页签「整体风格」（不插进已钉死顺序的
  「名片页面样式」三块），`MpTheme.vue` 用预览色块 + 授权提示；保存**只提交
  `{ templateId }`**。
- 小程序：12 份 WXSS 共 **83 处**主色硬编码抽成 11 个 `--wc-*` 变量；默认 token
  写在 `page` 上（没挂主题 class 的页面取值与上线前逐字一致）；
  `.wc-theme-ink` / `.wc-theme-warm` 覆盖同名变量；6 个访客页的 **25 个顶层状态
  节点**只加 `wc-theme {{themeClass}}` 类、**不改 DOM 结构**；`utils/theme.ts`
  负责 id → class、Storage 缓存与 `getCurrentPages()` 广播。
- 契约：`types.ts` 的 `HomeLayoutResp` 补 `templateId` 并与 Go json tag 对账；
  新增 `tests/theme.spec.ts` 对账 Go 白名单 / CSS 变量齐全 / WXSS 无残留 /
  6 页顶层节点与 `applyTheme` 调用点。

### 关键决策（都不是「顺手实现」）

- **授权 gate 不能挂路由**：`RequireFeature` 挂在整个 PUT 上会连首页背景、板块
  顺序、时间档位一起拦掉——没买高级模板的客户连基础展示设置都存不了。改为在
  PUT 内部只校验「切到高级款」这一步；已选中的高级款带着同一个 id 保存其它设置、
  或切回 `default`，都放行。
- **读路径不抹库**：授权到期时公开接口回落 `default`，但 `template_id` 保留，
  续费后 `ResolveFor` 每次请求重新查库、自动恢复，客户无需重选。与
  `detailTimeMode()` 的「纯展示配置不让页面失败」同一原则。
- **`order` 也改成指针**：三个页签各存各的，模板页只提交 `templateId` 时，
  原实现会无条件把 `order_col` 写成 `"null"`，静默冲掉客户配的板块顺序。
  这是本次顺带修掉的真实数据丢失路径，不是重构洁癖。
- **默认外观逐字不变**：变量默认值就是原硬编码值；一期不动页面底色 / 卡片白 /
  正文灰度，避免深色主色压低文字对比度。

### 一期边界（写清楚，避免被当成 bug）

模板只覆盖访客主链路 6 页（名片首页、名片墙、全员、案例列表、两个详情页）；
员工 / 管理页沿用 `page` 默认 token。一期提交（`afb5705`）只有配色、只有 3 款，
且首次冷启动若没进过名片首页只能用上次缓存——这三条限制已在**第二十三轮**逐条解除
（版式差异、第 4 款、冷启动预热）。「真实预览图」已在**第二十四轮**落地（见文末）；
二期只剩「客户自定义主色」，而白名单模板包是有意的产品边界，理由是端上不能
热加载 WXSS，见下节。

### 门禁与反向验证

- 后端：`go build` / `go vet` / `go test ./...`（28 包）全绿；`golangci-lint v2.15.0`
  **0 issues**；本地 MySQL parity `TestAPIFlowMySQL` 17 个子测试全 PASS，含新增
  「小程序配色模板」——`0041` 迁移在真实 MySQL 上确实跑通。
- 前端：`eslint .` / `vue-tsc --noEmit` 0 错误；全量 Vitest **94 文件 1535 例**全绿；
  `vite build --outDir dist-check-theme` 成功；WXSS 类名冲突自检无危险项；
  `git diff --check` OK。
- **反向验证 6 项**（改回旧实现必红、还原后全绿）：①去掉授权 gate ②恢复 `order`
  无条件写 ③去掉读路径授权回落 ④模板页整表单回传 ⑤首页删掉 `applyTheme`
  ⑥端上类型删掉 `templateId`。六项全部 `exit=1` 精确命中；还原后
  `TestAPIFlow` 与全量 Vitest 均 `exit=0`。

### 上线注意

- **必须同步整个 `migrations/`**（含 `0041`）：只换二进制会在运行期炸
  `Unknown column: template_id`；补齐后 `./server migrate`，不必重启。
- 高级款要按租户在企业授权里填 `mp_template_advanced`（逗号分隔，留空 = 全部开通）。
- 新增模板需同步改 `internal/mptemplate`、`app.wxss` 变量块、`utils/theme.ts` 的 id
  清单，并随小程序**重新提审发版**——端上不能热加载样式。
  （第二十三轮起还要补 `MpTheme.vue` 的 `.layout-<id>` 示意，`theme.spec.ts` 会拦。）

---

## 第二十三轮：整体风格模板从「能选」打磨到「能卖」（2026-11-10 同日）

一期只换了配色：高级款与默认版式一模一样，后台预览卡也只看得出换色，
增值能力讲不清；此外暖金棕有多处对比度不达标、首次冷启动直达二级页会停在默认蓝。
本轮四件事：按实测修可读性、让高级款有真实版式差异、补第 4 款、修冷启动一致性。

### 1. 可读性：先算再调，不凭感觉

用 WCAG 相对亮度公式实测 12 组真实搭配（白字按钮、浅底主色字、深主色字等，
组合来自 `app.wxss` 的真实选择器，不是挑好看的凑数）：

| 主题 | 白字/主色 | 白字/alt | 主色/tint（最低项） | 结论 |
| --- | --- | --- | --- | --- |
| `default` | 3.37 | 4.50 | 2.95 | **上线前既有基线，不能动**（升级零突变） |
| `ink` | 8.90 | 6.97 | 7.56 | 达标 |
| `warm`（旧 `#a5742a`） | 4.09 | 3.27 | 3.58 | **不达标** |
| `warm`（新 `#8f6320`） | 5.28 | 4.73 | 4.62 | 达标 |
| `teal`（新 `#0f766e`） | 5.47 | 5.19 | 4.71 | 达标 |

`warm` 主色按实测调深一档，浅色系保留暖调；`--wc-primary-rgb` 同步改成
`143, 99, 32`，否则 `rgba(var(--wc-primary-rgb), …)` 的阴影会和按钮错色。
后台 `PreviewPrimary` 同步调深，前端 `MpTheme.vue` 预览卡不会还显示旧色。

守门策略刻意区分基线：新增款一律 ≥4.5（正文级 AA），`default` 用 12 个实测基线值
锁「不许变差」——既不放水，也不逼着改存量客户已经看惯的默认外观。
`tests/theme.spec.ts` 里 `srgbLuminance` 遇到非 `#rrggbb` 直接抛错，
防止 `var()` / 渐变让计算返回 `NaN` 而断言假绿。

### 2. 版式 token：真实差异，且不动布局

新增 7 个版式 token：`--wc-r-card` / `--wc-r-card-bg` / `--wc-r-control` /
`--wc-r-chip` + 3 个 `--wc-card-shadow-*`。只改**圆角与阴影**（都不占盒模型尺寸），
`padding` / `width` / `flex` 一律不进 token。默认值逐字等于上线前原值，
`theme.spec.ts` 用 `DEFAULT_LAYOUT_BASELINE` 钉死。

| 款 | 卡片 | 按钮 | 阴影 | 气质 |
| --- | --- | --- | --- | --- |
| 经典蓝 | 20rpx | 40rpx | 蓝灰柔光 | 通用 |
| 沉稳墨 | 6rpx | 8rpx | 细描边代替投影 | 工程 / 制造 / 法律 |
| 暖金棕 | 32rpx | 999rpx 胶囊 | 暖色柔阴影 | 餐饮 / 文旅 / 家居 |
| 松石青 | 12rpx | 20rpx | 极轻青色投影 | 健康 / 教育 / 环保 |

落地 14 处样式引用（`app.wxss` 11 处 + `index.wxss` 3 处，含背景图模式的
`.has-bg .section` 覆盖规则——漏了它，设了首页背景的租户会看不到版式差异）。
后台预览卡按 `layout-<id>` 画对应的圆角 / 描边示意，客户在选之前就能看出差别。

### 3. 第 4 款 `teal`：用跨端守门验收

加款本身只改 5 处（Go 白名单 / `THEME_IDS` / `app.wxss` 变量块 /
`MpTheme.vue` 示意 / 前端 spec mock）。**真正的工作量在验证**：故意制造
「端上漏加 id」「漏写 CSS 块」「后台漏补示意」，`theme.spec.ts` 分别精确变红，
确认 id 对账、变量齐全、对比度、rgb 一致性、预览色、版式差异、示意覆盖
七条守门都自动覆盖新模板，不需要为新款手写一套断言。

### 4. 冷启动主题预热

`app.ts` 的 `onLaunch` 在 `bindCompanyFromScene` 之后调
`utils/themeBootstrap.prefetchTheme(globalData.baseUrl, getCompanyId())`：
拉一次公开 `/api/home-layout`，写 Storage 并广播到页面栈。访客直接从分享 /
小程序码进名片墙或详情页时首页 `loadAll` 根本不会执行，不预热就会停在默认蓝，
直到下次冷启动且打开首页才统一——后台换了风格客户却看不到。

该模块**绝不 import `utils/request.ts`**：后者第 13 行在模块加载期执行 `getApp()`，
而 `app.ts` 静态 import 的模块在 `App({...})` 求值前就被加载，那时 `getApp()`
还是 `undefined`，请求层会永久丢掉后台下发的 `baseUrl` 覆盖（`utils/config.ts`
的引导机制直接失效）。改用裸 `wx.request` + 调用方传入已解析的基址 / 租户，
并加 `hasBaseUrl` 守卫（空基址发请求只会白等超时）。失败一律静默：
纯展示配置不能拖住启动。与首页 `loadAll` 各拉一次很轻的公开 GET，
换来任意入口页一致，是有意的取舍。

### 关键决策

- **`default` 不调到 AA**：它是模板功能上线前就有的配色，改了就是给全部存量客户
  换皮。用「基线锁 + 新增款严卡」代替「一刀切」，是产品决定不是偷懒。
- **版式只进圆角 / 阴影**：任何影响盒模型的 token 都会让 6 个访客页的既有像素
  对齐失效，用户此前多次逐像素纠偏，不能为了「差异明显」冒险。
- **后台预览是示意不是复刻**：真值只有一份（`app.wxss`），前端 `px` 只是缩略图
  尺度；守门保证「加款必有示意」，不假装两端像素级一致。
- **不改 `0041` 迁移**：COMMENT 里的 id 列表是上线当时快照，真实白名单以
  `mptemplate.All()` 为准；加款不需要动数据库，改历史迁移是坏习惯。

### 门禁与反向验证

- 后端：`go build` / `go vet` / `go test ./...`、`golangci-lint`；MySQL parity。
- 前端：`eslint .` / `vue-tsc --noEmit` / 全量 Vitest / `vite build`。
- 契约：`docs/openapi.yaml` 三处 `templateId` enum 补 `teal`，自检单 components 块
  与全部 `$ref` 可解析。
- **反向验证 4 组**：①`warm` 改回旧色 → 对比度守门红；②`--wc-primary-rgb`
  错色 + 后台预览色漏改 → 两条守门各自红；③默认款圆角被改 / 高级款照抄默认 /
  后台漏画示意 → 零突变、差异、示意三条各自红；④端上漏加 `teal` id / 漏写 CSS
  块 / 漏补示意 → 跨端对账红；⑤删掉冷启动预热 / 复用 `request.ts` / 去掉空基址
  守卫 → 预热守门红。全部「改回必红、还原后全绿」。

---

## 第二十四轮：真实预览图（二期第一项，2026-11-11）

> ⚠️ **后续订正（第二十六 / 二十七 / 二十八轮）**：本轮当时的画布是 375×430、省略了业务服务区，
> 并把它称作「真实预览图 / 高保真预览图」。这两个口径都已被后续轮次取代：
> 第二十六轮补成 375×640 的完整首页示意并明确标注非真机截图；第二十七轮再按用户
> 提供的真机截图 1:1 复刻版式，改为 375×1300 长图（2x 750×2600）+ 真实演示素材；
> **第二十八轮又按「通用平台产品」要求脱敏（去掉真实企业客户信息、改用中性占位），
> 并把画布按内容收口为 375×1210（2x 750×2420）以消除底部大空白**。
> 本轮正文保留作历史实施记录，当前口径见文末第二十八轮、`backend/README.md`、
> `miniprogram/README.md` 与 `docs/real-device-acceptance.md` §7。

一期 / 第二十三轮的后台模板卡是**手画 CSS 色块 + 版式示意**：能看出换色和圆角，
但客户选之前看到的并不是小程序的样子。卖增值能力时这是硬伤，而且没人会报错——
端上换了配色 / 圆角，后台那张示意卡悄悄就旧了，属于典型静默失败。

### 1. 预览图是产物，token 是唯一真值

新增 `scripts/gen_theme_previews.py`：从 `miniprogram/app.wxss` 解析 `page` 默认块与
`.wc-theme-*` 块的 18 个 `--wc-*` token，模板 id 与顺序从
`backend/internal/mptemplate/mptemplate.go` 读（不另列一份），把同名 token 注入一份
复刻小程序首页结构的 HTML，再用无头 Chrome 截图到
`frontend/public/theme-previews/<id>.png`。`rpx` 按 0.5 换成浏览器 `px`，
2x 出图 750×860。**图里没有一处主题色是手写的**，全走 `var(--wc-*)`；
改了 `app.wxss` 只要重跑脚本，四张图一起跟上。

画布刻意做成 375×430 而不是整屏 560：后台模板卡宽 230px，整屏缩下去要么看不清、
要么裁掉底部按钮——而底部按钮的圆角正是四款版式差异最大的一处。只复刻
「导航 + 名片卡 + 业绩区 + 底部操作栏」，省略与业绩区重复的业务服务区。

### 2. 后台接入与可见降级

`MpTheme.vue` 每张卡渲染 `<img class="preview-shot">`，路径按 `t.id` 拼
`${import.meta.env.BASE_URL}theme-previews/<id>.png`（带 base 前缀，部署到子路径不会
打错；`encodeURIComponent` 防脏 id 拼出非预期路径）。卡片宽改 230px、容器固定
`aspect-ratio: 375 / 430`，与生成画布一致，图片下载完成前后不跳版、不裁切。

**底层 CSS 示意常驻**：图还在下载、404、或新增模板忘了补图，都不会留一块空白；
`<img>` 的 `@error` 只把**这一张**移走（按 id 记失败，不搞全局开关），露出底下示意，
右下角「示意图」角标让降级是可见的——静默退回旧样式没人能察觉。点「重新加载」
清空失败状态给图片一次重试机会。生产 Nginx 的 SPA 兜底会把缺失的 PNG 回成
`index.html`（HTTP 200 + HTML），浏览器解码失败同样触发 `error`，兜底依然生效。

### 3. 跨端对账（加款漏一步就变红）

`miniprogram/tests/theme.spec.ts` 新增 5 例，把
Go 白名单 ↔ PNG 集合 ↔ 生成画布尺寸 ↔ 后台容器比例 ↔ 接入代码 ↔ token 快照
钉成一条链：每款模板必须有合法 PNG（PNG 头 / 750×860 / 非空白图）且无多余文件；
脚本必须从 `app.wxss` 取 token、HTML 里不得硬编码任一主题主色；`aspect-ratio`
必须等于脚本 `WIDTH/HEIGHT`；后台必须真的用 `previewUrl(t.id)` 且保留 `@error` /
底层示意 / 角标 / 重试清空。脚本自带 `--check` 模式（不需要浏览器）供本地提交前跑；
CI 则由全量 Vitest 覆盖——`frontend/vitest.config.ts` 的 include 含
`../miniprogram/tests/**/*.spec.ts`。

**只查「PNG 都在」有个漏洞**：改了 `app.wxss` 的配色 / 圆角却没重跑脚本时，
文件一个不少、尺寸也对，客户看到的却还是旧样式，而且没人会报错。所以出图时同时
写 `frontend/public/theme-previews/manifest.json`（每款模板的 18 个 token 快照 +
画布参数），`--check` 与 `theme.spec.ts` 都拿它和当前 `app.wxss` 逐项对账。
改了样式不重出图、删快照、手改快照伪造同版，两边都会变红。

加一款模板现在是 **5 处**：Go 白名单 + `app.wxss` 变量块 + `utils/theme.ts` 的
`THEME_IDS` + `MpTheme.vue` 的 `.layout-<id>` 兜底示意 + **重跑生成脚本出 PNG**。
前四处漏了是端上不对，第五处漏了是后台不对，两者都由守门拦住。

### 关键决策

- **不做后台上传自由 WXML/CSS 式模板**：既有边界不变，预览图只是把已内置的白名单
  样式「照」出来，没有引入第二套可漂移的样式源。
- **示意卡不删，降级为兜底**：真实图是静态资源，404 / 缓存 / CDN 都可能出问题；
  留一层可见降级比「要么全有要么白屏」稳。
- **不用 `object-position: top` 裁切**：裁掉底部按钮等于把最值钱的版式差异藏起来。
- **不改 CI 加新 job**：`--check` 只查产物，跨端守门查接入代码，后者已在全量
  Vitest 里，没必要为一个静态资源再拉一条 workflow。

### 门禁与反向验证

- `python3 scripts/gen_theme_previews.py --check`：4 款、4 张 750×860 PNG 一一对应，
  `manifest.json` 的 token 快照与 `app.wxss` 同版。
- `MpTheme.spec.ts` **12 例**（新增 3 例：按 id 拼图 / 单张失败只降级一张 /
  重新加载重试）；`theme.spec.ts` **20 例**（新增 5 例跨端对账，含 token 快照）。
- **反向验证 12 组全部「改回必红、还原后全绿」**：模板页 3 组（吞掉 `error`、
  去掉按 id 拼图、删掉底层示意）+ 跨端 5 组（漏生成 PNG、画布尺寸改了没重出图、
  容器比例漂移、HTML 手写主题主色、后台改回不渲染真实图）+ 快照 4 组
  （改默认主色 / 改 `teal` 圆角但都没重出图、删 manifest、手改快照伪造同版），
  最后四组的 Vitest 守门与 `--check` **同时**变红。
- 全量门禁照旧：后端 `go build` / `go vet` / `go test ./...` / `golangci-lint` /
  MySQL parity；前端 `eslint .` / `vue-tsc --noEmit` / 全量 Vitest /
  `vite build --outDir dist-check-*` / `git diff --check`。
- **真机验收清单已补**：`docs/real-device-acceptance.md` 新增 §7（`T1–T10`），
  `docs/launch-checklist.md` §四新增第 13 项，两者都标为**追加待验项**，
  不计入 `#163` 的 28 项，也不改写其「已通过」结论。四款模板的真机观感、
  分享直达二级页的冷启动一致性、后台真实预览图对照，代码侧无法代劳。

---

## 第二十五轮：品牌配色预设色板（A 方案，二期收口）

### 需求与选型

「客户自定义主色」是给渠道商准备的增值能力。三个方案里选了 **A 预设色板**：

- **A 预设色板（采纳）**：往现有白名单再加 8 款完整模板，id 统一 `brand-` 前缀，
  只换配色、版式沿用 `default`，后台仍只存 `templateId` 一列。
- B 自定义色值：要引入运行时样式注入，会推翻「不下发自由 CSS」这条已确认边界。
  小程序代码随版本审核，端上不能动态加载 WXSS；自由 CSS 还会把内部类名变成对
  客户的长期承诺，并带来注入与「深底 + 深字」对比度失控风险。
- C 客户上传整包模板：同样绕不开审核口径，且把内部 DOM 结构变成契约。

A 的产品表达是「按自有 VI 主色挑一款」，而不是「随便填一个 hex」。它复用了一期
全部底座：授权 gate、冷启动预热、真实预览图、manifest token 快照、跨端守门，
**没有新增任何运行时样式通道，也没有改数据库**。

### 落地

| 层 | 改动 |
|---|---|
| 后端 | `internal/mptemplate` 新增 8 款 `brand-*`（绛红 / 朱橘 / 琥珀 / 森绿 / 海蓝 / 靛蓝 / 堇紫 / 玫紫），全部 `Advanced: true`；新增 `Series` 字段（`style` / `brand`）与 `SeriesOf` / `IsBrand`；`All()` 现返回 12 款 |
| 契约 | `docs/openapi.yaml` 三处 `templateId` enum 扩到 12 个 id；admin GET 的 `templates[]` 补 `series`；后台类型 `ThemeTemplate` 补 `ThemeSeries` |
| 小程序 | `app.wxss` 新增 8 个 `.wc-theme-brand-*` 块，18 个 token 齐全；7 个版式 token **逐字沿用 default**；`THEME_IDS` 扩到 12 款 |
| 后台 | `MpSettings` 页签「整体风格」→「风格模板」；`MpTheme.vue` 按服务端 `series` 分两组渲染，品牌组注明「只换主色，版式沿用经典蓝」；旧后端不下发 `series` 时全部归入整体风格组，卡片不会消失 |
| 预览图 | `gen_theme_previews.py` 重出 12 张 750×860 PNG + `manifest.json` 12 款 token 快照 |

8 款主色按统一算法派生（deep / grad 压暗、alt 提亮、浅色档混白），
**12 组真实搭配全部实测过 WCAG AA**：最低 4.95（琥珀），最高 7.35（堇紫），
均优于既有高级款的 4.59 / 4.71 下限。`--wc-primary-rgb` 与主色同色，
后台预览色与端上 `--wc-primary` 逐款对账。

### 关键决策

- **两个系列的约束正好相反**：整体风格款每款至少 4 项版式 token 不同于 default；
  品牌配色款 7 项版式 token 必须与 default **逐字全等**。`theme.spec.ts` 同时钉住
  「要有差异」与「不许有差异」，任一侧漂移都变红。
- **分组真值在服务端**：`Series` 随清单下发，后台不靠 id 前缀猜；端上守门按
  `brand-` 前缀分组，两份声明由 Go 单测 + `theme.spec.ts` 双向对账。
- **预览图不能只看「文件都在」**：新增一条内容摘要守门，12 张 PNG 必须两两不同。
  生成脚本若把每款都画成 default，此前所有检查（存在 / 尺寸 / 体积 / token 快照）
  都还是绿的，客户却会看到 12 张一样的图——典型的静默失败。
- **真机项仍不代劳**：`docs/real-device-acceptance.md` §7 从 `T1–T10` 扩到
  `T1–T13`，新增品牌配色的「主色变了、版式没变」判定、预览图对照和授权置灰；
  `docs/launch-checklist.md` §四第 13 项同步。仍是**追加待验项**，不计入 #163，
  不改写「真机验收已通过」结论。

### 门禁与反向验证

- 后端 `go test ./...` 全绿；`mptemplate` 包新增系列一致性、8 款数量、
  品牌款必须 `Advanced`、未知 `brand-` id 仍被 `Normalize` 拒绝等断言；
  `handler_test.go` 补「清单下发 series」「品牌款公开接口原样下发」「未授权不能新选品牌款」。
- `theme.spec.ts` **25 例**（新增系列对账 3 例、版式反向约束 1 例、PNG 内容摘要 1 例），
  `MpTheme.spec.ts` **14 例**（新增分组渲染与旧后端兼容 2 例），
  `MpSettings.spec.ts` 11 例；定向合计 50 例全绿。
- **反向验证 4 组，改回必红、还原后全绿**：把一张预览图换成 default 副本（内容摘要守门红）；
  给品牌款偷偷改圆角（2 例红）；把 Go 里品牌款 `Series` 改成 `SeriesStyle`（1 例红）；
  把后台改回不按 `series` 分组（1 例红）。
- `python3 scripts/gen_theme_previews.py --check`：12 款 / 12 张 750×860 PNG
  一一对应，`manifest.json` token 快照与 `app.wxss` 同版。
- 全量门禁照旧：后端 `go build` / `go vet` / `go test ./...` / `golangci-lint` /
  MySQL parity；前端 `eslint .` / `vue-tsc --noEmit` / 全量 Vitest /
  `vite build --outDir dist-check-*` / `git diff --check`。

### 二期边界

「真实预览图」与「客户自定义主色」两件二期功能至此均已落地，代码侧闭环。
剩下的仍是外部人工项：真机视觉验收 `T1–T13`、微信侧发版审核、渠道商授权配置，
以及 `launch-checklist.md` §四里其余需本人执行的事项。

---

## 第二十六轮：从「风格示意图」补成完整首页示意（2026-11-11）

> ⚠️ **后续订正（第二十七 / 二十八轮）**：本轮的 375×640 完整首页示意仍是「按 token
> 拼出来的简化版式」，用户拿真机截图逐项对照后指出「还是差了不少」。
> 第二十七轮按真机 1:3.45 比例改为 375×1300 长图、直接引用 `seedassets`
> 演示素材、补齐 8 分类图标 / 4 张竖版服务卡 / 2 张精选案例 / 底部按钮真实
> 文案与左右顺序，并把「查看大图」改成固定宽度 + 纵向滚动。
> **第二十八轮再按通用平台要求把企业身份全部换成中性占位、画布收口为 375×1210**。
> 本轮正文保留作历史实施记录，当前口径见文末第二十八轮。

### 起因：命名与事实不符，是产品口径问题

用户问「管理后台显示的风格预览图片为什么不是实际的小程序端页面效果呢」。
核查后确认：此前把它叫「真实预览图 / 高保真预览图」是**不准确的**。它确实是
从 `miniprogram/app.wxss` 解析 18 个 token 后注入首页 HTML 生成的，主色、浅色档、
圆角、按钮形态、投影都与端上同源；但它**不是真机截图，也不是小程序组件树的实际渲染**：

- 准的：18 个 `--wc-*` token 驱动的配色 / 圆角 / 按钮 / 投影；
- 不准的：真实 DOM 组件树、真实租户数据、真机字体、状态栏胶囊与安全区、
  首页背景图与遮罩、公司简介封面、业务服务区、真实案例缩略图；
- 早期为了在 375×430 里保住底部按钮的圆角差异，**整个业务服务区被省略**，
  案例区还用「◈」菱形占位——客户其实认不出这是自己的小程序。

三个方向里用户选了「**补成完整首页示意**」：不引入真机截图工具链，保持 token
同源、补齐首页结构，同时把「非真机截图」讲清并支持查看大图；真机验收仍保留。

### 落地

| 层 | 改动 |
|---|---|
| 生成脚本 | `scripts/gen_theme_previews.py` 画布 375×430 → **375×640**（2x 出图 750×1280）；按 `pages/index/index.wxml` 的区块顺序复刻完整首页：状态栏占位 / 自定义顶栏 / 名片卡 / 公司简介封面 / 经典业绩 / 业务服务 / 底部固定操作栏；去掉菱形占位，18 个 token 全部以 `var()` 真实引用 |
| 产物 | 12 张 PNG 全部重出，`manifest.json` 写入 12 款 18 个 token 快照与新画布参数；`--check` 仍只查产物、不需要浏览器 |
| 后台 | `MpTheme.vue` 容器 `aspect-ratio` 改 `375 / 640`；新增**常显**「风格示意 · 非真机截图」角标（不是只在降级时出现）、右上角「查看大图」浮层（750×1280 原图、`contain` 不裁切、点遮罩 / 关闭收起）；说明文案写明准什么、不准什么 |
| 守门 | `theme.spec.ts` 新增「区块顺序与 `index.wxml` 锚点一致」「18 个 token 全被 `var()` 引用」「画布不得压回 430」「后台常显标注 + 查看大图」；`MpTheme.spec.ts` 新增 4 例覆盖常显标注、查看大图不误选中、关闭与失败态、大图正在看的那张降级时自动收起 |
| 文档 | 两份 README、`docs/real-device-acceptance.md` §7（判定口径 + T9/T10/T12 改写 + 新增 `T14`）、`docs/launch-checklist.md` 第 13 项同步；历史轮次正文保留，只在第二十四轮标题下加订正块 |

### 关键决策

- **不引入真机截图**：后台是普通网页、跑不了小程序组件树；模板随小程序版本提审，
  浏览器里也加载不了 WXSS。做成「真机截图」需要额外的真机/模拟器采集与
  逐款存档链路，还会把内部 DOM 变成契约。保持 token 单一真值更安全。
- **「非真机截图」必须常显**：只在图片降级时显示「示意图」角标，等于让客户把
  正常的示意图当成实际页面去验收；这个误解不报错，只能靠标注说清。
  两枚角标语义分开：常显的说「这是示意图」，降级的说「这张图没加载出来」。
- **看大图必须 `@click.stop`**：卡片自身有 `@click="pick(t)"`，不阻止冒泡就会
  「看一眼」变成「选中这款」，是典型的静默误操作；单测专门钉住选中态不变。
- **图降级时不给查看大图入口**：同一个 URL 必然也是坏的，点开只会是一片空白；
  大图正在展示的那张若后续报错，浮层自动收起。
- **示意图不能替代真机验收**：§7 判定口径改为「完整首页示意、非真机截图，
  只比对主色 / 圆角 / 按钮 / 投影四项」，`T1–T13` 扩到 `T1–T14`；仍是追加待验项，
  不计入 #163、不改写「真机验收已通过」结论。

### 门禁与反向验证

- 定向：`theme.spec.ts` **29 例**、`MpTheme.spec.ts` **18 例**，合计 47 例全绿；
  `python3 scripts/gen_theme_previews.py --check` 12 款 / 12 张 750×1280 一一对应，
  `manifest.json` token 快照与 `app.wxss` 同版。
- **反向验证 7 组，全部「改回旧实现必红、还原后全绿」**：删常显标注；
  查看大图漏 `.stop`；拿掉查看大图入口；容器比例压回 375/430；预览 HTML 又裁掉
  业务服务区；画布压回 430（同时验 `--check`）；改了 `app.wxss` 主色却不重出图
  （同时验 `--check`）。用 `try/finally` 临时改文件、自动还原，还原后定向测试与
  `--check` 均回到 0。
- 全量门禁照旧：后端 `go build` / `go vet` / `go test ./...` / `golangci-lint 2.15.0` /
  MySQL parity；前端 `eslint .` / `vue-tsc --noEmit` / 全量 Vitest（**95 文件
  1572 例**）/ `vite build --outDir dist-check-*` / `git diff --check`。
  `vite build` 产物里 12 张 PNG 与 `manifest.json`（画布 `[375, 640]`）齐全。
- **顺带补掉一个门禁缺口**：本地 `vite build --outDir dist-check-<用途>` 会留下
  带后缀的验证构建目录，上一轮给 `.gitignore` 加了通配，但
  `frontend/eslint.config.mjs` 的 `ignores` 仍只有固定名 `dist-check/**`，
  于是对着压缩后的单行 JS 刷出 **1566 条** `no-unused-vars` / `no-empty` 假红
  （`vue-tsc` 不受影响、当次仍为 0）。已补 `dist-check-*/**`：带后缀目录存在时
  `eslint .` 回到 0，`git status` 也不冒出该目录。这是「验证产物污染门禁」的
  第二类，与 `.gitignore` 那条同源，改配置比删目录更根本。

### 边界

本轮只改**后台示意图与它们的守门**，不改模板白名单、不改端上 `app.wxss` token、
不改接口契约、不改数据库。二期两件功能的代码侧仍在闭环状态；剩下的真机视觉
验收 `T1–T14`、微信侧发版审核、渠道商授权配置都是外部人工项，不因示意图更完整
而自动变成已完成。

---

## 第二十七轮：按真机截图 1:1 复刻首页版式（2026-11-11）

### 起因：用户拿真机截图打回「补成完整首页示意」

第二十六轮把预览补成 375×640 的「完整首页示意」后，用户发来**实际小程序页面
截图**（556×1920，≈1:3.45）并明确：「这是实际的小程序页面效果，你的预览图还是
差了不少」。这次不靠猜：把截图裁成上 / 中 / 下三段逐段核对，再读
`pages/index/index.wxml`、首页组件 WXML、`demoseed.go` 与 `seedassets/`。

差距是结构性的，不是配色没调准：

| 项 | 第二十六轮示意图 | 真机 |
| --- | --- | --- |
| 画布 | 375×640（1:1.71） | ≈1:3.45 长图 |
| 页面底色 | 灰白 | 浅蓝渐变 + 白卡 |
| 顶栏 | 无「我的」胶囊 / 微信胶囊 | 两者都有，企业简称居中 |
| 名片卡 | 缺 LOGO / 地址 / 两列联系信息 | 头像 + 姓名职位 + LOGO + 全称 + 地址 + 手机 / 座机 / 邮箱两列 |
| 公司简介 | 简化块 | 真实封面 + 播放控件 + 进度条 + 黑色粗体公司名 + 4 行简介 |
| 经典业绩 | 菱形 / chips 占位 | 8 个分类图标卡（2×4）+ 2 张精选案例大图 |
| 业务服务 | 横条色块 | 4 张竖版服务插画卡 |
| 底部按钮 | 「保存名片 / 拨打电话」，左右写反 | 「一键拨号」实心在左 /「存入通讯录」描边在右 |

关键发现：仓库 `backend/internal/demoseed/seedassets/` 里**已经有真机截图用的那套
演示素材**（`avatar-gaolixin.png` / `logo.png` / `cover-company.jpg` /
`cover-daxing.jpg` / `cover-hospital.jpg` / `cat-1~8.png` / `svc-1~4.png`），
且 `achievement-grid` / `service-grid` 的 WXML 明确写着「标题已绘制在图内，
不重复渲染文字」。所以直接引用真实素材就能 1:1 还原，**不用手画第二套图形**。

### 落地

| 层 | 改动 |
|---|---|
| 生成脚本 | `scripts/gen_theme_previews.py` 改用 `string.Template` 注入 token 与演示文案；画布 375×640 → **375×1300**（2x **750×2600**）；`SEED_FILES` 17 个素材复制到临时目录同树，Chrome `file://` 才能读图；`DEMO_SOURCES` 回查 `demoseed.go` / `index.wxml` / `action-bar/index.wxml` / 两个 grid 组件的注释，演示文案与源码逐字同源 |
| 版式 | 浅蓝渐变底 + 白卡；状态栏占位 /「我的」胶囊 + 微信胶囊 / 名片卡两列联系信息 / 封面与视频控件 / 8 分类图标 + 2 精选案例 / 4 竖版服务卡 / 底部「一键拨号」+「存入通讯录」；18 个 token 仍全部以 `var()` 引用 |
| 素材 | 照片 / 插画 / 图标**不随模板换色**；`.cover-shade` 只叠中性黑渐变，曾误用 `rgba(var(--wc-primary-rgb), …)` 把绛红款封面整片染红，已修并加守门；右缘悬浮音符 ♪ 从被 `.bar` 盖住的 `bottom:8px` 改为 `top:47%` 可见 |
| 产物 | 12 张 750×2600 PNG 全部重出；`manifest.json` 新增 `seedAssets` 素材清单，与 12 款 18 个 token 快照、画布 `[375,1300]`、`scale=2` 一起落盘 |
| 后台 | `MpTheme.vue` 说明文案改为「按真机首页 1:1 复刻的完整首页风格示意」；容器 `aspect-ratio: 375 / 1300`；「查看大图」从 `max-height` + `contain` 改为**固定宽度面板 `min(420px, 92vw)` + `.zoom-scroll` 纵向滚动**（长图不能再被压成一条细线）；补回 `.zoom-head` 等被误删样式 |
| 守门 | `theme.spec.ts` 29 → **32 例**：新增素材同源、封面不被染色、`seedAssets` 快照、悬浮音符可见；原有区块顺序 / 18 token / 画布比例 / 大图滚动断言改为按真机长图口径。`MpTheme.spec.ts` 补 `.zoom-scroll` 与 `img.zoom-shot` 父子关系断言 |
| 文档 | 两份 README、`commercial-readiness.md` 第二十四 / 二十六轮标题下订正块 + 本轮、`real-device-acceptance.md` §7（`T1–T14` → **`T1–T15`**，判定口径 / T9 / T10 / T14 改写、新增 T15 版式对照）、`launch-checklist.md` 第 13 项 |

### 关键决策

- **仍不做真机截图**：后台是普通网页，跑不了小程序组件树；模板随小程序版本提审，
  浏览器加载不了 WXSS。做成真机截图要额外采集 / 逐款存档链路，还会把内部 DOM
  变成对外契约。保持「token 单一真值 + 白名单模板包」边界不变。
- **不手画第二套图形**：标题本就画在演示素材里，再画一遍必然和真机漂移。
  `check_sources()` 让「换了演示素材 / 改了文案却没重出图」变成显式失败，
  而不是让客户看到一张悄悄过时的图。
- **画布比例照真机**：1920 × 375 / 556 ≈ 1295 → 取 1300。之前为了在卡片里
  保住圆角差异把画布压到 430 / 640，代价是整页内容被压扁、区块被简化，
  正是用户指出的「差了不少」。卡片容器比例必须和脚本画布对账。
- **照片不随主题换色**：租户上传的封面 / 视频帧本来就不受 `app.wxss` 影响，
  示意图若把它们染成主色，等于承诺了一个不存在的能力。
- **长图必须能滚**：`max-height` + `object-fit: contain` 对 750×1280 成立，
  对 750×2600 会把整页压成一条细线；固定面板宽度 + 内部纵向滚动才看得清每屏。
- **元素存在 ≠ 用户看得见**：悬浮音符锚到 `.phone` 底部被后画的 `.bar` 整块盖住，
  DOM / CSS 都在却永远不显示，是典型静默失败，专门加一条守门钉死。
- **示意图再像也不是验收结论**：§7 判定口径仍只比对四项 token 观感，新增的
  `T15` 只核对版式与素材是否对得上；`T1–T15` 仍是追加待验项，不计入 #163、
  不改写「真机验收已通过」结论。

### 门禁与反向验证

- 定向：`theme.spec.ts` **32 例** + `MpTheme.spec.ts` **18 例** = **50 例全绿**；
  `python3 scripts/gen_theme_previews.py --check` 12 款 / 12 张 750×2600
  一一对应，token 与 17 个 `seedAssets` 快照同版。
- **反向验证 19 组，全部「改回旧实现必红、还原后全绿」**：画布压回 640 /
  底部留白回 48px / 封面被主题色染 / 底部按钮左右写反 / 分类图标退回菱形占位 /
  音符压回底栏 / 精选案例换错素材 / HTML 模板取不到 / 大图去掉滚动容器 /
  卡片比例回 375/640 / 大图面板宽度回退 / 大图不可滚动 / PNG 内容重复 /
  改 `app.wxss` 不重出图 / 素材常量整体改名 / 删 `check_sources()` 调用 /
  manifest 少记素材（含合法 JSON 变体）/ 真删一张 `seedassets` 让 `--check` 变红。
- 首轮反向验证抓到**两条假绿**：守门只查 `SEED_FILES` / `check_sources()` 的
  字面名字，函数定义和变量引用里也有这些字样，删掉真正的调用照样放行。
  已改成断言 `'"seedAssets": SEED_FILES'` 与 `'problems = check_sources()'`，
  补硬后重验必红。守门本身也要被验证能变红，不然等于没守。
- 全量门禁：后端 `go build` / `go vet` / `go test ./...` /
  `golangci-lint 2.15.0`（**0 issues**）；前端 `eslint .` / `vue-tsc --noEmit` /
  全量 Vitest（**95 文件 1575 例**）/ `vite build --outDir dist-check-replica`
  （产物含 12 张 PNG + `manifest.json` `canvas [375,1300]` / `scale 2` /
  12 款 token / 17 个素材）/ `git diff --check`。

### 边界

本轮只改**后台示意图的生成脚本、产物、页面适配与守门**，不改模板白名单、
不改端上 `app.wxss` token、不改接口契约、不改数据库。剩下 `T1–T15` 真机视觉
验收、微信侧发版审核、渠道商授权配置仍是外部人工项，不因示意图更像真机
而自动变成已完成。

---

## 第二十八轮：通用平台脱敏 + 底部留白收口（2026-11-11）

### 触发：用户拿后台截图提了两点

> 「我们这个产品是作为通用平台产品，示意图中不要体现真实的企业客户信息啊，
> 另外业务服务地步不要留这么高的空白区，和底部的两个按钮有一定割裂感，
> 导致按钮和下面的风格名称及说明混在一起了，请进行优化」

两点都成立，而且**都不会报错**：示意图里出现「廊坊市朗汇机电设备有限公司 /
高立新 / 13832603521 / 朗汇机电 LOGO」只是让客户误以为这是别人的真实案例；
业务服务卡下方那片纯底色空白只是让缩略卡里的按钮看着和模板名称连成一块。
没有崩溃、没有 404、没有失败日志，只能靠用户眼睛发现——典型的静默失败。

### 核查结论

| 项 | 事实 |
|---|---|
| 身份信息来源 | `DEMO_TEXT` 逐字抄自 `backend/internal/demoseed/demoseed.go`，上一轮还专门加了「与源码同源」守门 |
| 带身份的素材 | `logo.png` 上直接画着「朗汇机电」；`avatar-gaolixin.png` 是真人头像；两张 `cover-*.jpg` 是具体工程照片但**无企业文字** |
| 不含身份的素材 | `cat-1~8.png`（居住住宅等通用分类）、`svc-1~4.png`（电梯销售等通用服务）、`cover-company.jpg`（通用电梯井照片） |
| 空白来源 | 画布按真机 556×1920 折算成 375×1300，但复刻的内容只到 y≈1129，底栏是 `absolute bottom:0`，中间空出约 100px 纯底色 |
| 混在一起的来源 | 图内底栏是白底、卡片外 `.theme-meta` 也是白底，中间只有一条极淡的卡片边框，缩略卡里看不出「图到哪里结束」 |

**关键取舍**：不把 15 个通用行业素材全换成色块。用户上一轮刚要求按真机 1:1 复刻
版式密度，脱敏若把 8 分类图标 / 4 服务卡 / 精选案例一起简化，等于用新错误换旧错误。
只换**承载企业身份**的部分（文案、头像、LOGO），保留通用图形与版式密度。

### 改动

| 面 | 内容 |
|---|---|
| 出图脚本 | `DEMO_TEXT` → `PLACEHOLDER_TEXT`（企业名 / 简称 / 地址 / 人名 / 职务 / 手机 / 座机 / 邮箱 / 简介全部中性占位，长度贴近原密度）；`DEMO_SOURCES` 只回查**不含身份**的区块标题与按钮文案；新增 `IDENTITY_LITERALS` + `GO_IDENTITY_RE` + `identity_tokens()` + `check_no_identity(html)`，对**代入占位文案后的最终 HTML** 做黑名单核对 |
| 头像 / LOGO | HTML 不再引用 `assets/avatar-*.png` / `assets/logo.png`，改用 CSS 画的 `.avatar-ph` 人形剪影、`.me-avatar`、`.logo-ph`「LOGO」虚线占位块；仍使用 `--wc-*` token，18 个 token 全部有 `var()` 引用 |
| 素材清单 | `SEED_FILES` 17 → **15**（去掉真人头像与带企业名 LOGO），`check_sources()` 额外拒绝清单里混入 `avatar-` / `logo.` 前缀 |
| 快照 | `manifest.json` 新增 `placeholders` 占位文案快照；`--check` 与 `theme.spec.ts` 双向对账，「改了文案却没重出图」会变红 |
| 画布收口 | 375×1300 → **375×1210**（2x **750×2420**）；新增 `CONTENT_BOTTOM 1130 / TAIL_GAP 20 / BAR_HEIGHT 60` 三个常量与等式校验，CSS 用 `${tail_gap}` / `${bar_h}` 注入同一组值；无头 Chrome 实测内容底部 1129、底栏顶 1150、尾部留白 **21px** |
| 防遮挡 | 新增 `measure_content_bottom()`：出图前用 `--dump-dom` 实测业务服务卡底部，若 `bottom + TAIL_GAP > HEIGHT - BAR_HEIGHT` 直接拒绝出图，避免占位文案变长后 absolute 底栏静默盖住服务卡 |
| 底栏 | `border-top` 加深到 `#d8dee6`、加 `box-shadow: 0 -3px 12px`，给「页面到此结束」的边界 |
| 后台卡片 | `aspect-ratio` 改 `375 / 1210`；`.theme-preview` 加 `border-bottom` 硬边界、`.theme-meta` 改 `#f8fafc` 浅灰底；说明文案写明「本产品是通用平台 / 不含任何真实企业客户资料」；长图尺寸注释同步 750×2420 |
| 守门 | `theme.spec.ts` 把「演示文案与源码同源」整体反转为「不许出现真实身份」：黑名单（静态 + 从 `demoseed.go` 动态抽 `Demo*`）、占位字典本身、HTML 模板、manifest 快照四处都查；新增「画布按内容收口 / 出图前实测」与「预览区与名称区视觉分隔」两组守门；`MpTheme.spec.ts` 新增说明文案渲染断言 |

### 门禁与反向验证

- 12 张 750×2420 PNG 全部重出，`python3 scripts/gen_theme_previews.py --check` 通过；
  12 张内容摘要互不相同（沿用上一轮守门）。
- 定向：`theme.spec.ts` **39 例** + `MpTheme.spec.ts` **19 例** = **58 例全绿**。
- **反向验证 9 组，全部「改回旧实现必红、还原后全绿」**：占位文案改回
  「朗汇机电」 / HTML 重新引用 `logo.png` / 画布退回 1300 / 删掉脱敏校验调用 /
  删掉出图前实测 / 去掉预览区分界线 / 名称区退回纯白 / manifest 占位快照改脏 /
  占位简介变长让底栏盖住服务卡（脚本实测 1197 > 1150 后拒绝出图）。
- **首轮反向验证又抓到一条假绿**：把 `PLACEHOLDER_TEXT` 改回「朗汇机电」时
  `--check` 会红，但 `theme.spec.ts` 只查 HTML 模板（里面是 `${short_name}`）
  和旧 manifest 快照，**端上全绿**。补了「直接读脚本占位字典做黑名单核对」后必红。
  这是继上一轮「守门本身也要验证能变红」之后的第二种变体：
  **守门查的是模板 / 快照，不等于查了真值来源**。
- 另修一条自写的假红：`.bar` 规则里含 `${bar_h}`，正则 `/\.bar\s*\{[^}]*\}/`
  被变量自带的 `}` 截断，拿半条规则去断言 `border-top` 必然失败；改成按相邻的
  `.bar button` 规则切片。

### 边界

本轮只改**后台示意图的生成脚本、产物、页面适配与守门**，不改模板白名单、
不改端上 `app.wxss` token、不改接口契约、不改数据库，也不移植任何真实客户数据。
示意图仍是「按真机首页复刻版式 + 按 token 上色 + 中性占位信息」的**风格示意，
不是真机截图**。`T1–T16` 真机视觉验收、微信侧发版审核、渠道商授权配置
仍是外部人工项，不因示意图脱敏或留白收紧而自动变成已完成。

---

## 第二十九轮：风格示意角标与「查看大图」改为悬停预览图才显示

### 背景

第二十八轮把示意图做了通用平台脱敏 + 底部收口。但 `MpTheme.vue` 上的
「风格示意 · 非真机截图」角标与右上角「查看大图」按钮是**常驻**显示，
用户反馈：这两枚提示不用一直贴在预览图上，指针停在图上时再显示即可——
常驻反而遮挡示意图本身、干扰选色对比。

这是纯 UI 呈现调整，不涉及示意图的生成逻辑、产物、脱敏与底部收口（第二十八轮
已闭环），也不改模板白名单 / 端上 token / 接口契约 / 数据库。

### 改动

| 面 | 内容 |
|---|---|
| 后台卡片 | `.preview-badge` 与 `.preview-zoom-btn` 默认 `opacity: 0` + `pointer-events: none`；新增 `.theme-preview:hover` / `:focus-within` 规则把它们浮现（`opacity: 1` + `pointer-events: auto`）。降级提示 `.fallback-tag` 仍常显，两枚角标语义不同，不混为一谈 |
| 说明文案 | 页面描述段落保留「不是真机截图 / 通用平台不含真实客户资料」文字说明与「点卡片右上角查看大图」指引；只是浮层入口不再常驻 |
| 守门 | `theme.spec.ts` 把原「后台常显标注」断言改为「默认隐藏、悬停才显示」：除校验元素与文案在 DOM 外，新增 `.theme-preview:hover .preview-badge` / `.theme-preview:hover .preview-zoom-btn` / `opacity: 0` 三处源码级对账，改回常驻会变红；`MpTheme.spec.ts` 同步把测试名从「常显」改为「默认隐藏、悬停才显示」，保留元素存在性与降级后仍在 DOM 的断言 |
| 文档 | 两份 README 的「常显」描述改为「悬停预览图时浮现」；`real-device-acceptance.md` §7 的 `T9` / `T10` 同步改口径（仍是真机验收待验项，不计入 #163） |

### 边界

悬停是桌面管理后台的交互（触屏无 hover，但页面文字说明仍把「非真机截图 /
查看大图」讲清，不靠常驻角标）。`T1–T16` 真机视觉验收仍是本人要过的一轮；
本改动只动呈现，不替代真机验收，也不改变「示意图不是真机截图」的口径。

### 门禁

- 定向：`theme.spec.ts` 与 `MpTheme.spec.ts` 改动后全绿（沿用第二十八轮 58 例基线）。
- 全量门禁照旧：前端 `eslint .` / `vue-tsc --noEmit` / 全量 Vitest / `vite build`，
  后端 `go build` / `go vet` / `go test ./...` / `golangci-lint`（本轮未改后端，仅顺带核验）。
- Go 工具链已在第二十八轮补丁升到 1.26.11，CI 依赖漏洞扫描 job 不会因此再红。

---

## 第三十轮：预览卡片改为手机窗口、内部滚动看完整长图

### 背景

第二十九轮把角标与「查看大图」改成悬停才显示。用户进一步反馈：预览卡现在把
整张 1:3.2 的长图压扁在 230px 宽的卡片里（约 745px 高），显得很长；能否默认只
显示**手机屏高度**、管理员滚动查看完整示意图，更贴合手机预览效果。

这是纯呈现调整：不改变示意图的生成逻辑、产物、脱敏与底部收口（第二十八轮已闭环），
也不改模板白名单 / 端上 token / 接口契约 / 数据库。

### 改动

| 面 | 内容 |
|---|---|
| 后台卡片 | `.theme-preview` 从「按长图比例 375/1210 显示整图」改为**手机窗口**：`aspect-ratio: 375 / 750`（约竖屏两屏高），`overflow: hidden`；新增 `.preview-scroll`（`position:absolute; inset:0; overflow-y:auto`）把长图装进去，管理员在卡片内上下滚动看完整 750×2420 长图；`.preview-shot` 由 `object-fit:cover; height:100%` 改为 `width:100%; height:auto`（按原始比例显示，不裁切下半部）；`.preview-badge` / `.preview-zoom-btn` 仍绝对定位于 `.theme-preview` 框，滚动时固定在窗口角、不跟着内容跑 |
| 说明文案 | 卡片描述加「卡片内可上下滚动查看完整首页，悬停卡片后点右上角查看大图可放大查看」 |
| 间距 | 用户反馈卡片左右太紧凑、两组上下太近：`.theme-grid` 的 `gap` 16→28px、`.theme-group + .theme-group` 的 `margin-top` 24→40px（纯视觉呼吸感，不影响功能与守门） |
| 守门 | `theme.spec.ts` 把原「容器比例 == 脚本画布」断言改为「卡片是手机窗口」：脚本画布仍断言 375×1210（产物比例由 `--check` 守门），卡片比例断言落在 1:1.6~2.3 竖屏范围、退回 375/1210 长条会红；并新增 `preview-scroll` / `overflow-y: auto` / `height: auto` 三处对账（用 `object-fit:cover` 会裁下半部，必须拦住） |

### 边界

比例范围 1:1.6~2.3 是「手机竖屏窗口」与「别又变回长条」的折中：过小（接近 1:1）
只能露顶部一屏、滚动太多；过大（>2.3）又回到长条观感。当前 375/750 = 2.0，
230px 宽卡片 ≈ 460px 高，露出约两屏、滚动余量合理。`T1–T16` 真机验收仍是本人
要过的一轮；本改动只动呈现，不替代真机验收，也不改变「示意图不是真机截图」的口径。

### 门禁

- 定向：`theme.spec.ts` / `MpTheme.spec.ts` 改动后全绿（沿用第二十八轮 58 例基线 +
  本轮比例守门更新）。
- 全量门禁照旧：前端 `eslint .` / `vue-tsc --noEmit` / 全量 Vitest / `vite build`，
  后端未改。

## 第三十一轮：小程序首页背景跟随风格模板主题 token

### 背景

第二十九 / 三十轮只动了后台预览卡的呈现（角标悬停、手机窗口滚动、间距）。用户发来
小程序名片页截图，反馈「更换风格模板后首页背景色没看到变化」——后台预览图每款背景不同，
真机却始终是同一片蓝。

根因：小程序首页 `.page` 背景写死蓝渐变 `#d5e7fa...`，没接任何主题 token；后台预览图
`.phone` 用 `var(--wc-primary-soft), var(--wc-primary-tint)`，两端对不上。经
`AskUserQuestion` 确认切模板时首页背景「跟随主题（推荐）」。

### 改动

| 面 | 内容 |
|---|---|
| 小程序首页 | `miniprogram/pages/index/index.wxss` 的 `.page` 背景由写死蓝渐变改为 `linear-gradient(180deg, var(--wc-primary-soft), var(--wc-primary-tint))`，与后台预览图同源；保留「首页背景」图由 `page-bg`（fixed + 负 z-index）整幅覆盖优先——有背景图时主题渐变自然被盖住，两者都是 token，未设图即生效 |
| 守门 | `theme.spec.ts` 新增 1 例「小程序首页背景跟随主题 token，与后台预览一致（改回写死蓝会变红）」：断言 `.page` 规则含 `var(--wc-primary-soft)` / `var(--wc-primary-tint)`、不含 `#d5e7fa`，且生成脚本含 `var(--wc-primary-soft), var(--wc-primary-tint)` |

### 边界

纯小程序端呈现 + 跨端守门，不动模板白名单 / 端上 token / 接口契约 / 数据库 / 生成产物。
守门与「真实预览图」一套逻辑同源：切模板后真机首页背景随主色变化，正是 `T1` 判定的
加固项；真机观感仍由本人在真机过一轮确认。

### 门禁

- 定向：新守门绿；**反向验证**（把 `.page` 背景改回写死蓝）必红后自动还原。
- 全量门禁：前端 `eslint .` / `vue-tsc --noEmit` / 全量 Vitest **1584 例** /
  `vite build` / `git diff --check` 全绿；后端未改、`go build` `go vet` 仍绿。

---

## 第三十二轮：风格模板操作按钮上移 + 切换栏滚动固定顶部

### 背景

第三十一轮修完小程序首页背景跟随主题 token 后，用户附小程序设置页截图，要求：
把「重新加载」按钮上移到「访客先看到谁 / 名片页面样式 / 风格模板」标签页切换栏所在行
的最右侧，「保存风格」移到「重新加载」左侧；并且在页面滚动时，切换栏固定在顶部，
方便管理员翻阅 12 张风格卡时随时点页签或右侧按钮，不必滚回顶部。

原布局里「保存风格」在 `MpTheme` 卡片区底部、「重新加载」在内容区右上角，与页签栏分离，
滚下去就够不着。

### 改动

| 面 | 内容 |
|---|---|
| 父组件 `MpSettings.vue` | 抛弃 Teleport 进 `.el-tabs__header` 方案（VTU 下桩化 `el-tabs` 让 Teleport 目标解析失败），改为自渲染 sticky `.tabs-bar`：内含自绘页签按钮（`.tab` / `.tab.active` 蓝字 + 2px 蓝下划线）与 `.tab-actions`（保存风格在左、`type=primary`；重新加载在右，带 `Refresh` 图标），二者仅在 `tab==='theme'` 时渲染；`el-tabs` 仅作 lazy 挂载用，原生 header 用 `.hidden-header :deep(.el-tabs__header){display:none}` 隐藏，避免两套页签并存 |
| sticky 固定 | `.tabs-bar` 设 `position:sticky;top:0;z-index:10;background:#fff;border-bottom:1px solid #ebeef5`；`flex` 让页签占满左侧、`.tab-actions` 用 `margin-left:auto` 推到最右，滚动时整行固定顶部、内容在下方被遮住而非透出 |
| 子组件 `MpTheme.vue` | 删除自有的顶部 `.page-bar`（h3 标题 + 重新加载）与底部 `.actions`（保存风格 + hint）及对应 CSS；脚本末 `defineExpose({ load, save, loading, saving, templates })` 把操作与状态暴露给父组件 |
| 父调子 | `MpSettings` 用 `themeRef` + 三个 computed（`themeLoading`/`themeSaving`/`themeDisabled`，从 `themeRef.value` 读 loading/saving/templates 驱动按钮 loading/disabled）；`onReloadTheme`/`onSaveTheme` 调 `themeRef.value?.load()/save()`。非 theme 页时 `MpTheme` 未挂载、ref 为 null，按钮因 `v-if` 不渲染，点击不会发生 |
| 守门 | `MpSettings.spec.ts` 新增 4 例：切换栏右侧 theme 页显示「保存风格 / 重新加载」且其它页签不显示；点「重新加载」会再调 `getHomeLayout`；点「保存风格」只提交 `{templateId:'default'}`；源码级守门读 `MpSettings.vue` 断言含 `position: sticky` / `top: 0` / `.tabs-bar` / `class="tab-actions"` / `hidden-header`（改回非 sticky 必红）。`MpTheme.spec.ts` 把按钮交互改为直接调 `(w.vm as any).load()/save()`（VTU 本版 `wrapper.vm` 是属性非函数，`w.vm()` 会报 `w.vm is not a function`） |

### 边界

纯后台管理端 UI 调整：不动模板白名单 / 端上 token / 接口契约 / 数据库 / 生成产物 /
小程序代码。保存语义不变——仍只提交 `templateId`，不冲掉别的页签配置（见主题记忆
「只提交 templateId 不跨页签覆盖」）。按钮只在「风格模板」页有意义，故不出现在不相关的
页签上；`T1–T16` 真机验收仍是本人要过的一轮，本改动只动后台浏览器 UI，不影响真机判定。

### 门禁

- 定向：`MpTheme.spec.ts` 19/19、`MpSettings.spec.ts` 15/15 全绿。
- 全量门禁：前端 `eslint .` / `vue-tsc --noEmit` / 全量 Vitest **1588 例**（95 文件）/
  `vite build` / `git diff --check` 全绿；构建残留 `dist-check-theme2` 已清；后端未改。

---

## 第三十三轮：切换栏吸顶贴顶（抵消 el-main 的 16px padding）

### 背景

第三十二轮把「保存风格 / 重新加载」上移到自有 sticky 切换栏，但用户截图反馈：吸顶后切换栏
上方仍留约 12–16px 空隙，页面滚动时下方风格卡会透出来显示在顶栏与切换栏之间。

### 改动

| 面 | 内容 |
|---|---|
| 根因 | `.admin-main`（`el-main`，`overflow:auto`）带 `padding:16px`；吸顶 `position:sticky; top:0` 的参照是内容区上沿（比顶栏低一截的 16px padding 处），吸顶位置比顶栏底边低 16px，留下空隙、滚动时卡片从空隙透出 |
| 修复 | `.mp-settings` 整体出血 `margin:-16px` 抵消 `.admin-main` 的 padding，内部 `padding:0 16px 16px`（顶部不给内边距，避免把吸顶栏往下挤）；吸顶 `top:0` 仍会比顶栏低 16px（sticky 的 top 参照是滚动容器「内容区上沿」），故吸顶栏改 `top:-16px` 补偿，吸顶时白底直接贴到顶栏底边；静止未滚动时也已因出血而贴顶。仅改 `MpSettings.vue` 样式，不动结构与守门逻辑 |

### 边界

纯后台管理端 UI 微调：不动模板白名单 / 接口 / 数据库 / 小程序；保存语义、按钮位置与 sticky
守门不变。`T1–T16` 真机验收仍是本人要过的一轮，本改动只动后台浏览器 UI。

### 门禁

- 定向：`MpSettings.spec.ts` 在「sticky / top:-16px / tabs-bar / tab-actions / hidden-header」
  源码级守门基础上，新增断言文件含 `margin: -16px`、`padding: 0 16px 16px` 与 `top: -16px`，
  锁死吸顶贴顶不回退。
- 全量门禁：前端 `eslint .` / `vue-tsc --noEmit` / 全量 Vitest **1588 例**（95 文件）/
  `vite build` / `git diff --check` 全绿。

## 第三十四轮：切换栏文字上下留白对称

第三十三轮吸顶贴顶后，用户截图反馈：`.tab` 是 `padding: 0 0 12px`（只有下内边距），
导致文字上沿紧贴栏顶、文字下沿到 2px 蓝下划线之间空出 12px，上下明显不对称、不协调。

| 项 | 说明 |
| --- | --- |
| 根因 | `.tab` 仅 `padding: 0 0 12px`，上边 0、下边 12px；`.tabs-bar` 又 `align-items:center`，文字上沿贴栏顶、下方堆出一段空白才到蓝下划线 |
| 修复 | `.tabs-bar` 改 `align-items: stretch` 让 `.tab` 撑满整条栏高；`.tab` 改 `padding: 12px 0` 上下对称留白，文字在页签里垂直居中；`.tab.active::after` 维持 `bottom:0`（因 stretch 后 `bottom:0` 即贴栏的 `border-bottom` 底边框），文字上沿与栏顶、下沿（含蓝下划线）与栏底等距。仅改 `MpSettings.vue` 样式，不动结构与守门逻辑 |
| 守门 | `.tabs-bar` 加 `padding: 12px 0` 上下对称后，栏高在三种页签下一致（都由 `.tab` 的 `12+文字+12` 撑起），蓝下划线始终贴底 |

- 定向：`MpSettings.spec.ts` 在原有「sticky / top:-16px / tabs-bar / tab-actions / hidden-header /
  margin:-16px / padding:0 16px 16px」守门基础上，新增断言文件含 `align-items: stretch`
  与 `.tab { ... padding: 12px 0`（用正则 `/\.tab\s*\{[^}]*padding:\s*12px 0/`），锁死上下对称留白不回退。
- 全量门禁：前端 `eslint .` / `vue-tsc --noEmit` / 全量 Vitest **1588 例**（95 文件）/
  `vite build` / `git diff --check` 全绿。

> 注：排查过程中一度在 Read/Grep 输出里看到第 27 行 `:disabled="themeDisabled"` 这种形近串，
> 经 `git diff` 与码点核对，磁盘实际始终是 `:disabled`（ASCII），系输出层渲染幻影，无真实缺陷，未改动。

## 第三十五轮：三个页签共用切换栏右侧的「保存 / 重新加载」

用户要求：「访客先看到谁」「名片页面样式」两页的保存 / 重新加载按钮，也像「风格模板」那样
放到切换栏右侧，三页一致。此前只有风格页在切换栏有按钮，另两页的按钮各自留在页面体内
（`MpEntry` / `HomeLayout` 自己的 `.page-bar` 与表单底部「保存」），位置不统一。

| 项 | 说明 |
| --- | --- |
| 改动 | `MpEntry` / `HomeLayout` 内嵌时（`:embedded`）隐藏自带的 `.page-bar`（含「重新加载」）与表单底部「保存」按钮，并 `defineExpose({ load, save, loading, saving, canSave })` / `{ load, save, loading, saving }`；父组件 `MpSettings.vue` 给三个 `el-tab-pane` 各加 `ref`（entryRef / appearanceRef / themeRef），用 `activeRef` 指向当前激活页签的子组件，`onSaveActive` / `onReloadActive` 调其 `load` / `save`；`.tab-actions` 改为始终渲染、`activeLoading/activeSaving/activeDisabled` 与 `saveLabel`（风格页叫「保存风格」、其余「保存」）随当前页切换 |
| 禁用态 | 入口页看 `canSave`（未选员工禁用）、风格页看模板是否拉回、样式页始终可保存；独立访问（非内嵌）仍各自渲染自带按钮，行为不变 |
| 守门 | `MpSettings.spec.ts` 把「按钮只在风格页显示」翻转为「三个页签都显示」，并新增 entry / appearance 的重新加载与保存用例（保存分别提交 `saveMpEntry({mode:'wall',staffId:0})` 与完整 `saveHomeLayout` 负载）；源码级守门新增 `ref="entryRef"` / `ref="appearanceRef"` / `ref="themeRef"` 且 `.tab-actions` 不再含 `v-if="tab === 'theme'"`；`MpEntry.spec` / `HomeLayout.spec` 各加一条「内嵌时自身保存 / 重新加载按钮隐藏」 |

- 全量门禁：前端 `eslint .` / `vue-tsc --noEmit` / 全量 Vitest **1594 例**（95 文件，较上一轮 +6）/
  `vite build` / `git diff --check` 全绿。（构建首次偶发 `CODEBUDDY_BROKER_TIMEOUT` 系环境 broker
  瞬时网络超时，与代码无关，重跑 `REAL_EXIT=0` 通过。）

## 第三十八轮：主题两轴化 P1（数据/契约层，不改视觉）

用户确认方向（第三十七轮）后，选择「现在出完整设计 + 整模块重排版」，并拍板 **P1 的存量映射：
8 款 `brand-*` 全部归 `default` 风格之下**（`(default, brand-*)`，`default/ink/warm/teal` → `(同名, nil)`），
存量租户视觉零突变。基础风格的**真实 id 是 `default`**（即 `mptemplate.DefaultID`），设计稿里的标签名
「classic」即指 `default`，不是字面 `classic`。

P1 只动后端数据/契约层，前端与小程序**完全不动**（`templateId` 兼容派生列续命）。

| 项 | 说明 |
| --- | --- |
| 数据模型 | `model.HomeLayout`：`TemplateID` 单列 → 保留作**兼容派生列**（写入时由两轴反推、便于回滚锚点）+ 新增 `StyleID string`（无 DB 默认，便于 SQLite/存量回退）+ `BrandColorID *string`（nil=风格默认色） |
| 迁移 `0042` | `home_layouts` 加 `style_id`（MySQL `NOT NULL DEFAULT 'default'`）、`brand_color_id`（NULL）两列，按映射回填（brand-* → `style_id='default', brand_color_id=模板id`）；SQLite 走 AutoMigrate 无默认值，靠读路径回退 |
| `mptemplate.go` | 保留 `IsAdvanced(id)`（既有测试依赖）并内部改走两轴；新增 `Split`/`DeriveTemplateID`/`IsAdvancedCombo`/`BelongsToStyle`/`NormalizeStyle`/`IsKnownBrandColor`/`StyleOfBrand`。白名单仍是扁平 12 款（P1 不改 `templates` 清单形状，前端按 `series` 分组逻辑不变） |
| `content.go` GET | 管理/公开都从两轴列算 `styleId`/`brandColorId`（存量未回填行回退 `Split(template_id)`），并**仍下发 `templateId` 派生兼容列** |
| `content.go` PUT | 请求体新增 `styleId`/`brandColorId`（指针语义）；两者都缺时回退拆 `templateId`（兼容旧前端/小程序）；写入同步三列；授权 gate 改为两轴 `IsAdvancedCombo` + 「新选高级款」判定（`changed` 比较两轴值） |
| `handler.go` 公开读 | `publicTemplateID` 重构为 `publicStyleBrand(styleID, brandColorID)`：未开通高级款时整体回落 `(default, nil)`，不抹库 |
| `openapi.yaml` | 公开 GET / 管理 GET / 管理 PUT 三处补充 `styleId`、`brandColorId` 字段（保留 `templateId` 兼容描述） |
| 守门（源码级） | `manifest_gen.go` 追加 `0042`（启动守门比对迁移目录，漏则误报——本仓库 python 生成脚本不在本地，直接补清单项）；`handler_test.go` 新增「风格两轴」子测试（读写两值、跨风格拒绝 code=1、未知风格/色系拒绝、只交 brandColorId 默认落 default、高级品牌色系 gate 403）；`mptemplate_test.go` 新增 `Split/Derive/归属/高级` 单测 |
| 反向验证 | ① 临时 neutralize 跨风格 `BelongsToStyle` 校验 → 「色系不可跨风格」用例必红；② 临时去掉 `themeTouched` 条件 → 「只改板块顺序不应冲掉风格」必红；两处均还原后复绿 |
| 门禁 | **全绿收口**：`golangci-lint v2.15.0`（0 issues）/ `go test ./...` 全包通过（含 `TestManifestMatchesMigrationsDir`、`TestAPIFlow`）/ `gofmt -l` 为空 / `eslint .` / `vue-tsc --noEmit` / 全量 Vitest **1594 例（95 文件）** / `vite build --outDir dist-check-p1` / `git diff --check`，7 项 `SUMMARY ...=0` 全绿（本轮前端零改动，全套复跑确认无回归）。`dist-check-p1` 由 `.gitignore:20 **/dist-check-*/` 正确忽略，`git status` 无污染 |

> 关键坑 1：**基础风格真值是 `default` 不是 `classic`**。初版迁移与测试误写 `classic`，导致
> `NormalizeStyle("classic")==""`（未知风格）使基线 PUT 返回 code=1、`BelongsToStyle("classic",brand)` 永假。
> 全部改为 `default` 后通过。设计稿已加「classic 即 default」命名澄清。

> 关键坑 2（**静默数据丢失，收尾复核时抓到**）：初版把风格三列 `style_id` / `brand_color_id` /
> `template_id` 放在 PUT 的 `upd` 里无条件写入，而原实现只在 `req.TemplateID != nil` 时才写。
> 结果是「名片页面样式」页签保存（payload 只有 `order`/`bgImage`/`bgMask`/两个时间档位）会把客户
> 选好的高级风格**静默冲回 `default`**——没人报错、后台与访客所见悄悄不一致。修法：引入
> `themeTouched := req.StyleID != nil || req.BrandColorID != nil || req.TemplateID != nil`，
> 只有显式带风格字段时才做校验 / 授权 gate / 写三列；新建行仍落合法缺省值。补「只改 order /
> 只改 bgMask 不冲掉风格与品牌色系」断言并做反向验证。这是本项目「静默失败优先判」纪律的典型命中。

> 本轮提交后本地领先远端 **9 个提交**（此前是 8 个提交 + 未提交工作树）；沙箱拦截 `git push`，
> 需本人手动 `git push origin main`。前端/小程序视觉两轴化在 P2–P4。

## 第三十九轮：主题两轴化 P2（CSS 两层 + 前 2 模块布局）

承接第三十八轮 P1（提交 `e743d38`）与用户拍板的「风格驱动各模块布局、品牌配色叠加其上
+ 整模块重排版」。P2 **只动小程序与预览脚本**，Go 后端与 PC 后台零改动——后台仍发
`templateId`，由 P1 的兼容派生列兜住，两端零漂移。

| 项 | 说明 |
| --- | --- |
| `app.wxss` 拆层 | 12 个 `.wc-theme-*` 全部拆除 → `.wc-style-{ink,warm,teal}`（布局层，全套 18 token + 模块规则；`default` 继承 `page` 不另写一层）+ `.wc-brand-*`（8 款颜色层，**只 11 个颜色 token**，一个版式 token 都不许出现）。颜色层排在布局层之后才能覆盖前者 |
| 根节点 | `themeClass` 展开**两层** class：`wc-style-<id>` 恒挂（含 default）+ 可选 `wc-brand-<id>`；`wc-theme` 基类保留作语义标记（已无 token 定义）→ **六个访客页 wxml 一行都不用改** |
| 布局规则 | `4 风格 × 2 模块`（顶部名片卡 `.emp-card` / 公司简介 `.mp-profile`）；`index.wxml` 补 `mp-card-area` / `mp-profile` / `mp-achievements` / `mp-services` 四个钩子（后两个为 P3 铺路） |
| `theme.ts` 两轴 | `applyTheme(styleId, brandColorId)` / `resolveTheme(信封)` / `splitTheme` / `deriveThemeId` / `normalizeStyle` / `normalizeBrand`；**Storage 仍存扁平 `wc_theme_id`**（旧缓存、旧后端零迁移）；品牌色系端上强制归 `default`，与后端 `BelongsToStyle` 一致 |
| 接线 | `index.ts` 与 `themeBootstrap.ts` 都走 `resolveTheme`（两轴优先、旧后端拆 `templateId`），不各自猜字段；`types.ts` 的 `HomeLayoutResp` 补 `styleId` / `brandColorId`，`types_contract.spec.ts` 与 `models.go` json tag 逐项对账 |
| 预览图 | `gen_theme_previews.py` 的 `read_tokens()` 改为 `STYLE_BLOCK_RE` / `BRAND_BLOCK_RE` 两层合并（颜色层 ∪ `default` 的 7 个版式）。**12 组 × 18 token 与 `manifest.json` 逐字相等 → 12 张 PNG 无需重出**，`--check` 保持绿；按风格出 4 张布局长图归 P4 |
| 守门 | `theme.spec.ts` 的 `readThemeVars()` 同步合并；「品牌款版式 == default」改写为「颜色层不含任何布局 token + 12 组合合并后版式逐字 == default」；新增「布局层 18 / 颜色层 11 token 对账」「根节点生成两层 class」「4 风格 × 2 模块选择器 + wxml 钩子」「两层合并 token == manifest」「两轴拆分归一 / 旧缓存」。`themeBootstrap.spec.ts` 的 `resolveTheme` 用真实实现（只 mock 广播出口）；`types_contract.spec.ts` 字段扩到 6 个 |
| 反向验证 | ① 颜色层偷塞 `--wc-r-card` ② `themeClass` 退回单层 `wc-theme-*` ③ 删掉 `warm` 的公司简介规则 ④ 偷改一个色值 —— 4 项**必红**，还原后复绿 |
| 门禁 | **8 项全绿**：`golangci-lint v2.15.0`（0 issues）/ `go test ./...` 全包通过 / `gen_theme_previews.py --check` / `eslint .` / `vue-tsc --noEmit` / 全量 Vitest **1600 例（95 文件，较上轮 +6）** / `vite build --outDir dist-check-p2` / `git diff --check`，`SUMMARY` 八项全部 `=0`。后端与 PC 后台本轮零改动，仍全套复跑确认无回归 |

> 关键坑 1（**拆层最容易炸的连锁**）：`theme.spec.ts` 有 850 行守门、
> `gen_theme_previews.py` 按 `.wc-theme-*` 解析 token、`manifest.json` 记 12 款 token 快照。
> 直接删单层块会让「预览图 / 可读性 / 版式差异」一整串变红，且必须重出 12 张 PNG
> （改色值还会给存量租户引入视觉突变）。解法：颜色层删掉的 7 个版式 token 原本就等于
> `page` 默认值，于是让**生成脚本与测试用同一套「颜色层 ∪ default 版式」合并口径**，
> 12 组 18 token 逐字不变，PNG 与快照全部原样保留。这也是本轮唯一能做到「视觉零突变」的原因。

> 关键坑 2（**兼容期不要顺手迁移数据结构**）：差点把 Storage 改成存 `{styleId, brandColorId}`
> 对象。那样所有老版本缓存读出来都是 undefined，冷启动直达二级页的访客会静默回默认蓝。
> 保持 `wc_theme_id` 存扁平 `templateId`，只在读写两端做 `splitTheme` / `deriveThemeId`，
> 旧缓存与只下发 `templateId` 的旧后端都原样渲染。

> 关键坑 3（**布局规则的静默失败**）：「墨款无图」若照方向稿写成 `display:none` 封面，
> 客户上传的公司简介视频会悄悄消失，后台与访客都不会报错。落地改为「封面压矮 + 去圆角 +
> 文字细描边分隔」；同时所有规则必须以 `.emp-card` / `.mp-profile` 开头，写全局
> `.section` / `.intro` 会波及二级页与员工页。`default` 只逐字复述既有度量，锁住升级零突变。

> **视觉影响告知**：P2 会让**已选 `ink` / `warm` / `teal`** 的存量租户首页顶部名片卡与
> 公司简介出现可见外观变化（这是用户确认的「整模块重排版」预期，不是回归）；
> `default` 与 8 款 `brand-*` 零突变。真机像素级验收仍需人工——本环境无无头浏览器 /
> 真机截图工具链，只能靠 CSS 机制推理 + 全量门禁。

> 验收结论：**8 项全绿**（`SUMMARY LINT=0 GOTEST=0 PREVIEW=0 ESLINT=0 TSC=0 VITEST=0
> BUILD=0 DIFFCHECK=0`），全量 Vitest 95 文件 / 1600 例通过，`dist-check-p2` 由
> `**/dist-check-*/` 忽略、不进提交。反向验证 4 项必红且已还原复绿。
> 本轮提交后本地领先远端 **10 个提交**；沙箱拦截 `git push`，需本人手动
> `git push origin main`。P3（业绩 / 服务两模块布局）与 P4（后台两步 UI、预览图重生成、
> 白名单树化、真机验收）待实施。

## 第四十轮：主题两轴化 P3（经典业绩 / 业务服务布局，4×4 收口）

承接 P2（提交 `65ab681`）。P3 补齐设计稿 §4 里剩下的两个模块，**4 风格 × 4 模块 =
16 套布局至此全部落地**。本轮只改 `app.wxss` 布局规则与 `theme.spec.ts` 守门，
后端 / PC 后台 / 小程序 TS 与 wxml 全部零改动。

| 项 | 说明 |
| --- | --- |
| 真实结构 | 业绩 = `.cat-grid`（4 列分类图标）+ `.featured`（2 列精选照片）；服务 = `.svc-grid`（4 列竖版卡）。钩子 `.mp-achievements` / `.mp-services` 在 P2 已预留在 `index.wxml` |
| 列数重排 | 分类 / 精选 / 服务：`default` 4/2/4（逐字复述基线）、`ink` 5/2/4（紧凑网格、去圆角、细描边）、`warm` 2/1/2（2 列大分类卡 + 精选改**单列横向大卡流**、服务 3 列宽松）、`teal` 3/2/3（疏排、轻卡片微阴影）。这次是真正的 flex 列宽变化，不只是调 rpx |
| 服务卡硬约束 | **标题烧在图片里**：`service-grid/index.wxml` 有图时只渲染 `.svc-icon`、不渲染 `.svc-title`。差异化只能改列数 / 间距 / 圆角 / 分隔线，绝不把竖版卡缩成小图标或改 `aspect-ratio`；占位块 `.svc-icon-ph` 只在无图时出现，规则同时覆盖两者 |
| 不变量 | `default` 零突变；不增删 DOM、不隐藏元素；规则一律锁在模块钩子内（不写全局 `.section` / `.intro` / `.sec-title`）；**未触碰任何颜色 / 版式 token**，12 组 × 18 与 `manifest.json` 仍逐字一致，预览图与 PNG 保持原样 |
| 守门 | `theme.spec.ts` 从「4 风格 × 2 模块」扩到 **4×4**；新增「同模块四风格规则体两两不同」对账（防止复制粘贴同一套度量 = 客户等于没换风格）、「服务卡标题烧在图内 → `.svc-icon` 规则体禁 `width`/`height`/`aspect-ratio`/`max-height`」并钉住组件注释 |
| 反向验证 | ① 删 `warm` 的经典业绩布局 ② 把 `warm` 的服务布局复制成 `default` ③ 把服务卡缩成小图标 —— 3 项**必红**，还原后复绿 |
| 门禁 | **8 项实质全绿**：`golangci-lint v2.15.0`（0 issues）/ `go test ./...` 全包 / `gen_theme_previews.py --check` / `eslint .` / `vue-tsc --noEmit` / 全量 Vitest **1601 例（95 文件，较上轮 +1）** / `vite build --outDir dist-check-p3` / `git diff --check`。构建首次撞 `CODEBUDDY_BROKER_TIMEOUT`（环境 broker 瞬时超时，与代码无关，第三十五轮同款），重跑 `REAL_EXIT=0`、`✓ built in 1m 27s` |

> 关键坑 1（**信息静默丢失**）：设计稿给 ink 的业务服务方向是「紧凑两列、文字优先」。
> 若照字面把 `.svc-item` 改成横排小图标 + 文字，有图时页面上只剩一个被缩小的图，
> **标题本来是画在图内的**，缩小后直接看不清，而后台与访客都不会报错。落地改为保持
> 竖版比例、只改列数 / 间距 / 圆角，并加一条源码级守门把这条约束钉死。

> 关键坑 2（**假绿守门**）：只断言「每个风格每个模块有选择器」会放行「四份规则
> 复制粘贴」——客户切风格等于没换。新增按模块比较四风格**全部规则体**的集合，
> 两两必须不同；反向验证里把 `warm` 服务布局换成 `default` 的值，守门立刻变红。

> **视觉影响告知**：P2 已改变已选 `ink` / `warm` / `teal` 租户的顶部名片卡与公司简介，
> P3 再改变其经典业绩与业务服务区（同样属用户确认的「整模块重排版」预期）。
> `default` 与 8 款 `brand-*` 仍零突变。真机像素级验收需人工，本环境无截图工具链。

> 验收结论：门禁串行输出 `SUMMARY LINT=0 GOTEST=0 PREVIEW=0 ESLINT=0 TSC=0 VITEST=0
> BUILD=1 DIFFCHECK=0`，唯一红项是 `vite build` 的 `CODEBUDDY_BROKER_TIMEOUT`；
> 单独重跑构建得 `REAL_EXIT=0`（1m27s），确认与代码无关，**8 项实质全绿**。
> 全量 Vitest 95 文件 / 1601 例通过，`preview --check` 证明 12 组 token 仍与快照同版，
> `dist-check-p3` 由 `**/dist-check-*/` 忽略、不进提交。反向验证 3 项必红且已还原复绿。
> 本轮提交后本地领先远端 **11 个提交**；沙箱拦截 `git push`，需本人手动
> `git push origin main`。**P1 / P2 / P3 全部收口**，只剩 P4（后台两步 UI、预览图重生成、
> 白名单树化、真机验收）。

## 第四十一轮：主题两轴化 P4（固定预览区 + 两步选择 + 12 张预览图按 4 风格布局重出）

承接第四十轮 P3（提交 `94274e1`）。用户反馈「**管理后台的预览显示区固定在一个位置，
根据所选风格和色系来切换显示是不是更友好合理**」；助手复核后指出旧 12 张 PNG 仍是
P2/P3 之前的统一版式，不体现四风格模块布局，只固定预览区会让后台与真机不一致，
用户选择「**预览图一起重出**」。本轮因此同时改后台 UI、出图脚本、PNG 产物与守门，
**后端 / 迁移 / OpenAPI / 数据库 / 小程序 wxml 全部零改动**。

| 项 | 说明 |
| --- | --- |
| 后台交互 | `MpTheme.vue` 重写：左侧 `<aside class="preview-pane">`（`position: sticky`）**整页只有一个固定预览区**，`:src="previewUrl(currentId)"` 随 `(styleId, brandColorId)` 切图；右侧两步选择（第 1 步 4 张风格卡 / 第 2 步「默认色」+ 8 张色卡）。不再十几张卡片各带预览 |
| 两轴保存 | `save()` 只发 `{ styleId, brandColorId }`；`splitThemeId()` 兼容只下发 `templateId` 的旧后端（按 `brand-` 前缀拆回两轴）；「使用中」只在保存成功后更新；`syncZoom()` 让已打开的大图随组合换图 |
| 树形归属 | 切到非 `default` 风格**自动清空并禁用色系**——否则后端 `BelongsToStyle` 回 `code=1`，客户只看到「保存失败」却不明白原因 |
| 预览图重出 | `gen_theme_previews.py` 用 `STYLE_RULE_RE` 从 `app.wxss` 抽 `.wc-style-<id> … { … }` **规则原文**，去前缀、`rpx→px(×0.5)` 后注入 `${style_css}`；预览 HTML 复用真机模块钩子 `.emp-card` / `.mp-profile` / `.mp-achievements` / `.mp-services`，**不存在第二套布局**。12 张仍是 12 个合法组合：4 风格默认色用各自布局，8 款 `brand-*` 用 `default` 布局 + 自己的色系（`style_of()` 与后端 `Split()` 同口径） |
| 按风格收口画布 | 逐风格 `measure_content_bottom()` 实测：`default/ink/warm/teal` 内容底部 `1217/1112/2326/1794` → 画布 `1297/1192/2406/1874`（+20 呼吸 +60 底栏），上界 `375×2420`；2x PNG 分别 `750×2594 / 2384 / 4812 / 3748`。不再统一 375×1210，否则裁掉暖金棕单列大卡流 |
| manifest 快照 | 新增 `layouts`（4 套规则原文）、`canvasByStyle`、`contentBottoms`；`--check` 与 `theme.spec.ts` 的 `expectedLayoutRules()` 同口径复算，改了 WXSS 布局却没重出图必红 |
| 守门改写 | `MpTheme.spec.ts` 重写 20 例（两步渲染 / 只有一个固定预览区 / 切图 / 归属清空 / 两轴 payload / 旧后端兼容 / 大图同步 / 降级重试 / 脱敏说明）；`MpSettings.spec.ts` 改两轴 payload；`miniprogram/tests/theme.spec.ts` 增布局同源、按风格 PNG 尺寸、固定预览区不回潮等源码级断言 |
| 反向验证 | **8 项必红**（每项字节级还原、还原后复绿）：① 布局注入退回全 `default` ② 布局规则写死不分风格 ③ `save()` 退回 `{ templateId }` ④ 固定预览区 `:src` 写死 ⑤ 切非 `default` 不清色系 ⑥ 退回 `theme-card` 每款一张卡 ⑦ `manifest.layouts` 与 WXSS 漂移（Vitest + `--check` 双红）⑧ `ink.png` 换错尺寸。另做失败原因复核，确认 ①②⑤⑥ 命中的是目标断言行而非编译错误 |
| 门禁 | **8 项实质全绿**：`golangci-lint v2.15.0`（0 issues）/ `go test ./...` 全包 / `gen_theme_previews.py --check` / `eslint .` / `vue-tsc --noEmit` / 全量 Vitest **95 文件 1605 例** / `vite build --outDir dist-check-p4` / `git diff --check`。构建首次撞 `CODEBUDDY_BROKER_TIMEOUT`（环境 broker 瞬时超时，第三十五 / 四十轮同款），单独重跑 `RETRY_BUILD_EXIT=0`、`✓ built in 1m 28s` |

> 关键坑 1（**用户看不见的旧图**）：P2/P3 把 4 风格 × 4 模块布局落进了 `app.wxss`，
> 但 12 张 PNG 仍是旧版式。只固定预览区的话，「12 张图都在 + 尺寸对 + token 快照同版」
> 三项检查全绿，客户却看到与真机不同的排布，**没有任何人报错**。解法是让布局规则原文
> 也进入出图链路并写进 `manifest.layouts`，与 WXSS 逐字对账。
>
> 关键坑 2（**统一画布会裁内容**）：初始 `HEIGHT=2340` 时暖金棕实测内容底部 2326
> + 20 呼吸 > 底栏顶 2280，测量阶段直接拒绝出图。改成 `375×2420` 上界 + **每风格动态收口**，
> 短风格不留大尾巴、长风格不裁内容。随后 `--check` 又因用 `CONTENT_BOTTOM` 而非
> `HEIGHT - BAR_HEIGHT` 判越界假红一次，改回主流程同口径才通过。
>
> 关键坑 3（**静态大图 id 的静默错位**）：打开「查看大图」后切色系，浮层仍显示旧图，
> 客户以为放大看的是刚选中的那一款。新增 `syncZoom()` 在 `pickStyle` / `pickBrand` 同步。
> 另外 P4 移除「每张卡片 + 名称区」后，旧 `.theme-meta` 视觉分隔守门假红，
> 由 `.pane-foot` 承接同一语义（浅灰底 + 边框），不是删掉守门了事。
>
> 关键坑 4（**反向验证要红对地方**）：一次性脚本把 8 处改坏后，另做失败原因复核，
> 逐一确认命中 `theme.spec.ts` 的 847 / 846 / 345 / 867 行断言且无编译错误，
> 再把全部 19 个改动文件做 sha256 字节对账（漂移 0、新增 0），最后统一复绿。
>
> **视觉抽查**：本机无 PIL，改用逐张看图。`default` 为分类 4 列 / 精选 2 列 / 服务 4 列、
> 封面中等高度；`warm` 为分类 2 列 × 多行 / 精选单列大图 / 服务 3 列 × 2 行、封面更高更圆
> —— 证明布局差异确实进了 PNG，不只是 token 变色。

> **本轮刻意未做（留作后续独立阶段）**：白名单 `mptemplate` 物理树化、删 `IsBrand`
> 平面判定、清单改 `styles[]` + `brandColors[]`、OpenAPI 的 `templateId` enum 收敛、
> 删 `template_id` 兼容列。P4 后台仍按服务端 `series` 做两步渲染、**不自己猜 id 前缀**，
> 因此全程不引入新的后端 / 数据库变更风险。真机像素级验收仍需人工（本环境无真机截图
> 工具链），设计稿 §5/§6/§7/§8/§9/§11 已按此口径订正。

> 验收结论：反向验证 8 项必红且已还原复绿（针对性 Vitest 88 例复绿、`--check` 绿），
> 全量 Vitest 95 文件 / **1605 例**通过，`dist-check-p4` 由 `**/dist-check-*/` 忽略、不进提交。
> 本轮提交后本地领先远端 **12 个提交**；沙箱拦截 `git push`，需本人手动
> `git push origin main`。**主题两轴化 P1–P4 全部收口**。

## 第四十二轮：主题两轴化 P5（树形归属 → 4 风格 × 9 色档全叉乘 36 组合 + 风格按布局特色命名）

承接第四十一轮 P4（提交 `569ef09`）。用户质疑「**为什么只有整体风格的经典蓝有品牌配色**」，
明确「整体风格的分类不该按颜色分，而应该按布局特色区分，每种风格都有对应所有品牌配色的设计」。
复核确认 P2 已把 `app.wxss` 拆成 `.wc-style-*`（布局层）与 `.wc-brand-*`（颜色层）两层、根节点
同时挂载、颜色层后定义覆盖风格默认色，因此**任意风格 × 任意色系本来就能渲染**；限制只来自
四个树形判定点（后端 `StyleOfBrand`/`BelongsToStyle`、content.go 跨风格 code=1、端上
`normalizeSelection`、P4 后台禁用与自动清空）。本轮把树形归属拆除，改为全叉乘。

| 项 | 说明 |
| --- | --- |
| 口径修订 | **统一色板 + 风格按布局特色改名**（另两选项：统一色板暂不改名 / 逐风格调校 32 套色被否）。`default/ink/warm/teal` 改名 `标准均衡/紧凑信息/宽松亲和/疏排透气`，内部 id 不变；8 款 brand 说明改为「版式跟随所选整体风格」；风格名/说明禁含颜色词，由 `TestStyleNamesDescribeLayout` 钉住 |
| 真正的硬阻塞 | `template_id` 只有 12 个扁平 id 装不下 36 组合；决策：两轴列是真值，兼容列退化为**旧端近似值**（`Split` 只用于存量未回填行，有损）；端上 `wc_theme_id` 缓存 P5-2 升级为带前缀两轴格式，旧格式零迁移继续解析 |
| 后端 P5-1 | `mptemplate.go` 删 `StyleOfBrand`/`BelongsToStyle` → `IsValidCombo`（只挡未知风格/色系）、`IsAdvancedCombo` 改 OR（修 brand 短路，当前 8 色全高级故行为暂不可观测，属防御性）、兼容列注释写明近似语义；`content.go` 去掉跨风格拒绝；`openapi.yaml` 三处描述 + 36 组合说明；`mptemplate_test.go` 改 `TestCrossProductComboAndAdvanced`（36 组合全遍历）并新增命名守门；`handler_test.go` 改为「ink+brand-ocean / warm+brand-magenta 均放行」并断言公开 GET 下发两轴 |
| 端上 P5-2 | `theme.ts` 的 `normalizeSelection` 放开（不再把带品牌色组合打回 default）；`wc_theme_id` 缓存升级为带前缀两轴格式 `encodeCachedTheme(styleId, brandColorId)`，旧扁平缓存继续走 `splitTheme` 零迁移；`themeBootstrap.ts` / `types.ts` 注释订正 |
| 后台 P5-3 | `MpTheme.vue` 改为固定预览区 + 4×9 组合矩阵，`comboId(styleId, brandColorId)` 生成组合键；去掉「非 default 清空」与「禁用色系」逻辑，按组合寻图；风格名同步布局特色。`MpTheme.spec.ts` 重写为 36 组合矩阵断言 |
| 预览图 P5-4 | `gen_theme_previews.py` 重写为 36 组合出图、文件名 `style--brand` / `style--default`；`check_manifest` 校验 `combos` 与 `comboTokens`；`manifest.json` 新结构（`canvas` / `canvasByStyle` / `combos` 36 映射 / `comboTokens` 36 组 / `layouts` / `tokens` 12 基础色板 / `seedAssets` / `placeholders` / `source`）；旧 12 张删除 + 36 张新图新增，产物约 43 MB，`--check` 通过 |
| 守门改写 | 跨端守门三份（`theme.spec.ts` / `themeBootstrap.spec.ts` / `types_contract.spec.ts`）去掉树形归属断言，改为跨风格组合两轴响应、旧缓存兼容、36 组合跨端守门 |
| 反向验证 | **6 项必红**（每项字节级还原、还原后复绿、5 文件 sha256 零漂移）：A 后端 `IsValidCombo` 退回树形（go）B 生成脚本 `combo_tokens` 让色系覆盖版式（check）C `manifest` 组合映射写错（check+vit）D 后台 `currentId` 退回扁平 id（vit）E 后台恢复「切风格清空色系」（vit）F 端上 `normalizeSelection` 退回树形归属（vit）。Case C 锚点初稿在 `manifest.json` 重复 2 次会误判「配置错误」，改为 `combos` 段独有多行片段修复 |
| 门禁 | **8 项实质全绿**：`golangci-lint v2.15.0`（0 issues）/ `go test ./...` 全包 / `gen_theme_previews.py --check` / `eslint .` / `vue-tsc --noEmit` / 全量 Vitest **95 文件 1612 例** / `vite build --outDir dist-check-p5`（36 图齐全）/ `git diff --check`。`dist-check-p5` 由 `**/dist-check-*/` 忽略 |
| 文档同步 | `backend/README.md`、`miniprogram/README.md`、`docs/theming-two-axis-design.md`、`docs/real-device-acceptance.md`、`docs/launch-checklist.md`、`miniprogram/app.wxss` 旧风格名注释订正；设计稿追加 §12 决策与分期（旧树形描述保留作历史事实，不回改） |

> 关键坑 1（**树形判定点散落四处**）：限制并非来自 CSS（P2 已拆两层），而是来自后端两个函数、
> content.go 一处拒绝、端上归一、后台 UI 三处。单改后端放行会让前端仍把组合打回 default、
> 后台仍禁用色系，**客户看到选项却不让选**。必须同步拆掉全部五处。
>
> 关键坑 2（**兼容列不可逆**）：`template_id` 只有 12 个扁平 id，36 组合塞不进。
> `DeriveTemplateID` 选了色系就丢 style、`Split` 反拆成 (default, 色系)。决策是明确写进契约：
> 两轴列是真值，兼容列只是旧端近似值，读路径优先两轴列、仅未回填存量行回退 `Split`，
> 绝不反向用兼容列覆盖两轴列。
>
> 关键坑 3（**36 张图体积**）：产物约 43 MB，约为 P4 的 3 倍。优化时优先降 `SCALE`
> 而不是退回 4 张图——后者会丢掉「每组合一张真实布局图」的静默失败防护。
>
> 关键坑 4（**端上测试桩假绿**）：P5-2 早期把 `wx.getStorageSync` stub 成字符串而非函数，
> 异常被 `catch` 吞后回落 default，导致针对性用例错误通过；修正为传入函数后复绿（69 例）。

> **本轮刻意未做（留作后续独立阶段）**：OpenAPI 的 `templateId` enum 收敛（当前保留作旧端近似，
> 属刻意）、删 `IsBrand` 平面判定、清单改 `styles[]` + `brandColors[]` 形态。真机像素级验收
> 仍需人工（本环境无真机截图工具链）。**「删 `template_id` 兼容列」已在第四十三轮落地**
> （迁移 `0043` + 模型/接口/前端/OpenAPI 全链路收敛，经用户显式批准动数据库结构）。

> 验收结论：反向验证 6 项必红且已还原复绿（针对性 Vitest + `--check` 复绿），全量 Vitest
> 95 文件 / **1612 例**通过，`dist-check-p5` 由 `**/dist-check-*/` 忽略、不进提交。本轮提交
> **`b42bd6d`**（57 files / +1799 −392，git 识别旧 12 张 PNG rename→`default--*` 等、新 27 张
> `ink-/warm-/teal-*` 为新增）。提交后本地领先远端 **13 个提交**；沙箱拦截 `git push`，
> 需本人手动 `git push origin main`。**主题两轴化 P1–P5 全部收口**。
>
> 第四十二轮文档收口（本轮）：补本记录；并只读核对 `mptemplate` 白名单形态——确认 `IsValidCombo`
> 已取代树形判定、`IsAdvancedCombo` 为 OR、`NormalizeStyle`/`IsKnownBrandColor` 为两轴守门、
> 风格名描述布局特色（`TestStyleNamesDescribeLayout` 钉住），**白名单与全叉乘一致，无需改代码**。
> 本变更仅触及 `docs/commercial-readiness.md` 一个 Markdown 文件，不进任何测试读路径，故只跑
> `git diff --check` 一项门禁即可。

## 第四十三轮：删除 `home_layouts.template_id` 兼容派生列（迁移 0043，全链路收敛）

承接第四十二轮 P5（提交 `b42bd6d`）。P5 收口时把「删 `template_id` 兼容列」列为刻意未做项
（动数据库结构按项目纪律需显式批准）。本轮经用户**显式批准动数据库结构**，把 0041 引入、0042
保留的 `template_id` 兼容派生列彻底删除，只保留两轴 `style_id` / `brand_color_id` 作为存储真值，
结束「两轴 ↔ 单列近似」的折返。

> **动 DB 边界（显式批准）**：本轮回填收敛不涉及任何数据搬移——0042 已完成「两轴列是真值」的回填，
> `template_id` 列自 0042 起只是两轴反推的近似副本。0043 只做 `DROP COLUMN`，升级零突变：
> 旧行无 `template_id` 不影响任何读路径（读路径早已以两轴列为准），SQLite 走 GORM AutoMigrate、
> MySQL 走迁移 0043。

> ⚠️ **订正（第五十一轮，2026-11-13）**：下表这一行的「MySQL 8.0 幂等 DROP、
> 列已不存在静默跳过」是**错误结论**，并已在线上造成一次迁移失败。MySQL 8.0 根本
> 不支持 `DROP COLUMN IF EXISTS`（MariaDB 方言），会在 SQL 解析阶段直接报 1064，
> 语句根本没执行到「判断列在不在」那一步。已改为裸 `DROP COLUMN template_id` +
> `migrate.apply.go` 的 `ignoredErrs` 忽略 1091，并补三层守门与真实 MySQL 8.0 实测。
> 详见本文件第五十一轮与 `docs/deploy-runbook.md` §4「迁移 SQL 本身写错」。
> 下方表格原文保留，作为当时判断的记录，不作为操作依据。

| 项 | 说明 |
| --- | --- |
| 迁移 `0043` | 新建 `backend/migrations/0043_drop_template_id.sql`：`ALTER TABLE home_layouts DROP COLUMN IF EXISTS template_id;`（MySQL 8.0 幂等 DROP，列已不存在静默跳过）；`backend/internal/migrate/manifest_gen.go` 的 `expectedMigrations` 追加 `"0043_drop_template_id.sql"`（`TestManifestMatchesMigrationsDir` 守门） |
| 模型 | `backend/internal/model/models.go` 删除 `HomeLayout.TemplateID` 字段；`StyleID` gorm tag 显式加 `default:default`（与迁移 0042 一致，保证不变量测试有效） |
| `mptemplate.go` | 删除 `Split(id)` / `DeriveTemplateID(styleID, brandColorID)` / `IsAdvanced(id)` 三个对外函数；改用 `IsAdvancedCombo(id, nil)` 取代单参数 `IsAdvanced`；包注释更新为「两轴是唯一存储真值，0043 已删 template_id」 |
| 后端接口 | `content.go`：GET 不再下发 `templateId`（`effectiveStyleBrand` 直接读两轴，不再调 `DeriveTemplateID`）；PUT 请求结构体删除 `TemplateID *string` 字段、`themeTouched` 改为「`StyleID` 或 `BrandColorID` 任一非空」、update map 不再写 `template_id`；`handler.go` 公开 GET 同样只下发两轴 |
| 前端收敛 | `frontend/src/api/content.ts` 删 `templateId` 字段；`MpTheme.vue` 删 `BRAND_PREFIX` / `splitThemeId()`，直接读两轴；`MpTheme.spec.ts` / `MpSettings.spec.ts` 删 `templateId` fixture |
| 小程序保留 | `miniprogram/utils/theme.ts` 的 `splitTheme` + 可选 `templateId?` 字段**保留**（P5-2 旧缓存零迁移承诺，不破坏）；`types_contract.spec.ts` 的 `HomeLayoutResp` 仍声明 `templateId` 为 optional（兼容真正旧版本后端），强约束循环只强约束 `styleId` / `brandColorId`；`theme.spec.ts` 断言改为 `d?.styleId` / `d?.brandColorId` |
| OpenAPI | `docs/openapi.yaml` 删除三处 `templateId` schema（公开 GET 响应 / 管理 GET 响应 / 管理 PUT 请求体），公开 GET 描述与 PUT 描述改为「迁移 0043 已删除 template_id，本接口不再下发也不接受 templateId」；`styleId` 描述去掉「取代 templateId」措辞 |
| 刻意不动 | `backend/internal/wechat/subscribe.go` 的 `TemplateID` 是**微信订阅消息模板 ID**（`TPL-AUDIT`），与主题列无关，严禁触碰；其相关测试 `subscribe_test.go` 一并保留 |
| 后端测试 | `mptemplate_test.go`：`IsAdvanced` 调用改为 `IsAdvancedCombo(..., nil)`；不变量测试 `TestDefaultIDMatchesModelAndMigration` 改校验 `StyleID` 字段 + 读迁移 `0042` 的 `DEFAULT 'default'`（原校验 `TemplateID` + 迁移 `0041`）；删 `TestTwoAxisSplitAndDerive`；`handler_test.go` 子测试 7c/7d 全部 `templateId` 断言改为 `styleId` / `brandColorId` |
| 反向验证 | grep 全仓确认：除 `wechat/subscribe.go` 的 `TemplateID`（微信消息模板，正确保留）外，无 `DeriveTemplateID` / `Split` / `HomeLayout.TemplateID` 残留引用；`frontend/src` 无残留；`miniprogram` 端 `templateId` 为刻意 optional 兼容字段（保留） |

> 关键坑（**静默失败优先判**）：删 `templateId` 后若有调用方仍依赖它，用户不会看到报错——因为读路径早已
> 以两轴列为准，缺 `templateId` 只是字段消失，不触发任何错误。因此反向验证以 **grep 全仓残留**为硬证据，
> 而非靠测试偶然通过；`wechat` 的 `TemplateID` 与小程序 `splitTheme` 是必须区分的「同名不同物」。

> 验收结论：后端 `go build` + `go test ./...` 全绿（handler 约 48.7s、migrate、mptemplate 等）；前端 /
> 小程序 fixture 与 compat 断言同步改写；OpenAPI 三处 `templateId` 已移除；8 项全量门禁（gofmt /
> golangci-lint v2.15.0 / go test ./... / gen_theme_previews.py --check / eslint / vue-tsc /
> 全量 Vitest / vite build dist-check-43 / git diff --check）实跑全绿。本轮提交后由本人手动
> `git push origin main`（沙箱拦截 push）。**主题系统存储彻底收敛为两轴，无单列近似残留**。

## 第四十四轮：名片区 / 企业介绍区的「版式级」差异（P6，2026-11-12）

承接第四十三轮（提交 `b0f9a7f`）。用户在看过 36 张预览图后直接批评：

> 「我看现在整体风格中对于小程序页面顶部的名片信息区和企业介绍区并没有做什么布局调整啊，
> 只是元素高度有点不同而已，比如印刷的名片也有很多种布局样式啊，为什么你就没有更多灵活性的考虑呢」

判断准确。P2/P3 当时给自己定的口径是「只调列宽 / padding / 尺寸 / 行高 / 圆角 / 背景 / 阴影，
绝不改 DOM」，落到 `.emp-card` 与 `.mp-profile` 上就退化成「四个风格共用一套结构，只差尺寸」：
`.wc-style-ink/warm/teal` 对 `.emp-card` 只动 padding、头像直径、行距、圆角，
对 `.mp-profile` 只动封面高度与圆角。客户换风格只会觉得「好像大了点」。

本轮把这两个模块升级为**版式级差异**（允许纯 CSS 结构重排），四个模块里的后两个
（经典业绩 / 业务服务）P3 已经是用列数做的真重排，不动。

| 项 | 说明 |
| --- | --- |
| 名片区 · default | 横版左图右文 + 两列联系表（基线，逐字零突变） |
| 名片区 · ink | **工牌单栏款**：`.ec-head{align-items:flex-start}`、方角小头像 80rpx、整卡 `border-radius:0`；`.ec-contact{flex-direction:column}` 把两列联系表压成**单列四行**，`.ec-line` 加 `border-bottom:1rpx` 行间细线 |
| 名片区 · warm | **竖版居中款**：`.ec-head{flex-direction:column;align-items:center}`，头像 136rpx 顶部居中；`.ec-line1` 与 `.ec-line3` 也改纵向居中，姓名与 LOGO、公司名与「查看案例」全部**上下叠放**；`.ec-info{flex:0 0 auto}` 防列向 `flex:1` 塌高；联系表单列且 `justify-content:center` |
| 名片区 · teal | **左色块侧栏款**：`.emp-card{border-left:12rpx solid var(--wc-primary)}` 左缘通高竖条；`.ec-head{flex-direction:row-reverse}` 头像**移到右侧**；`.ec-col-r{border-left:1rpx}` 给两列联系表加竖分隔线变表格式 |
| 简介区 · default | 上图下文（封面 440rpx、名 → 线 → 简介竖排），零突变 |
| 简介区 · ink | 封面压成 240rpx 通栏窄条去圆角；`.pf-text{display:grid;grid-template-columns:34% 1fr}` 左窄栏放「公司名 + 分隔线」、右通栏放简介（`grid-area` 显式定位，不是两段竖排） |
| 简介区 · warm | 封面 500rpx 大圆角；`.mp-profile{text-align:center}` 文字居中，`.company-name-line` 缩成 260rpx 居中短线 |
| 简介区 · teal | `.mp-profile{display:flex}` + `.company-video{flex:0 0 42%}`，简介区变**左图右文**两栏，封面 320rpx |
| DOM 改动（唯一一处） | `pages/index/index.wxml` 的 `.mp-profile` 内新增**纯版式分组容器 `.pf-text`**，把「公司名 / 分隔线 / 简介」三个原平级节点包成一栏。**顺序与可见性完全不变**；`default` 款不给它写任何规则，它是普通块级容器，视觉零变化。没有它就做不出「左图右文」与「左窄栏 + 右通栏」 |
| 硬约束未松 | 仍然**绝不 `display:none`**（把 `.cv` 藏掉会让客户上传的公司视频悄悄消失）；`.ec-contact` 只改栏数、不删列；规则仍全部锁在 `.emp-card` / `.mp-profile` 钩子内 |
| 守门升级 | `tests/theme.spec.ts` 新增「结构指纹」断言：逐款钉住 `.ec-head` / `.ec-contact` 的 `flex-direction`、卡片左缘色块、`.mp-profile` 与 `.pf-text` 的 `display`、封面分栏宽度、对齐方式，并断言四款指纹**两两不同**；另断言任何布局规则不得出现 `display:none`、`index.wxml` 必须真有 `.pf-text` |
| 后端文案 | `mptemplate.go` 四款 `Description` 改写为版式描述（工牌单栏款 / 竖版居中款 / 侧栏款 …），仍满足 `TestStyleNamesDescribeLayout`（不含颜色词 + 含布局词） |
| 后台降级示意 | `MpTheme.vue` 的 `.layout-ink/warm/teal` 同步改成结构示意（ink 单列拉通 + 行间细线、warm 居中、teal 内容右倾 + 左缘竖条），真图挂掉时的兜底不再是一张说不上区别的灰卡 |
| 预览脚本修正 | `scripts/gen_theme_previews.py` 修了三处「预览 DOM 与真机不同构」：① `.ec-rows-line` 原来在 `.ec-rows` **外面**、`.ec-rows` 自己 flex 两列（真机是 `.ec-rows > .ec-rows-line + .ec-contact > .ec-col ×2`），导致针对 `.ec-contact` 的版式规则在图上根本落不下去；② 缺 `.company-video` 与 `.pf-text` 两个容器；③ `.ec-line` 上下内边距 1px→4px。另加 `@container (max-width: 200px)`：teal 封面只有 42% 宽，图上的「走进 XX」大字与假进度条要换小号装饰，否则叠成一团、时间码溢出右缘 |
| 画布上界 | `CONTENT_BOTTOM 2340→2500`、`HEIGHT 2420→2580`。warm 变竖版居先后内容底部 2326→**2492**，若不抬上界，`position:absolute` 的底栏会**静默盖住业务服务卡**——脚本的逐风格实测会在出图前直接拒绝 |

> 关键坑（**假绿守门**）：原来那条「4 风格 × 4 模块，同模块四风格规则体两两不同」的断言是假绿。
> 把 padding 从 24rpx 改成 30rpx、头像从 112rpx 改成 136rpx，规则体就已经不同了，断言全绿，
> 可客户看到的仍然是同一种版式。本轮把它升级成**结构指纹**：只有 flex 方向、栏数、分栏归属、
> 对齐方式这些字段变了才算换版式，纯尺寸调整不再能让守门变绿。

> 反向验证：① 把 teal 的 `.ec-head{flex-direction:row-reverse}` 改回 `align-items:flex-start`、
> 把 ink 的 `.pf-text{display:grid}` 改回 `display:block` 后，新增用例立即变红并打印
> 「teal 要头像在右：头部 row-reverse: expected 'row' to be 'row-reverse'」，
> 同时布局快照用例（`manifest.layouts` 漂移）一起变红；② 还原后 54 个用例全绿。
> 逐张核对预览图：default 横版两列 / ink 单列四行 + 左窄栏分栏 / warm 竖版居中 /
> teal 左侧竖条 + 头像在右 + 左图右文，四张结构确实不同，无溢出与破版。

> 验收结论：预览重出 36 张，逐风格实测内容底部 `1214/1102/2492/1649` → 画布
> `1294/1182/2572/1729`，2x PNG `750×2588 / 2364 / 5144 / 3458`，
> `gen_theme_previews.py --check` 通过。8 项门禁（gofmt 仅校验改动文件 / golangci-lint v2.15.0
> 0 issues / `go test ./...` 全绿，handler 50.9s / `--check` 通过 / eslint 通过 / vue-tsc 通过 /
> 全量 Vitest **95 文件 1612 例** / `vite build dist-check-44` 1m25s / `git diff --check` 干净）实跑全绿。
> 本轮提交后由本人手动 `git push origin main`（沙箱拦截 push）。

### 第四十四轮 · 附：发版后「切风格预览图不变」的线上排查（同日晚）

**用户反馈原文**：

> 「发布到线上，我切换整体风格和品牌配色，预览图看不到任何变化，是不是发布文件不全导致的，
> 相关文件你都放哪里了」

**第一件事是排除「文件不全」**——这是最容易误判的方向，也是用户的第一直觉。逐项比对：

| 检查 | 线上 | 本地 | 结论 |
| --- | --- | --- | --- |
| `default--default.png` | `200 image/png`、`content-length 1109522`、750×2588 | 1109522 字节 | 字节数完全一致 |
| 36 张图全量 | 逐张比对 | 逐张比对 | 全部一致 |
| 入口 JS | `assets/index-DPoIgPeU.js` | 同 hash | 一致 |
| `MpSettings-*.js` | 22550 字节 | 22550 字节 | 一致 |
| `MpSettings-*.css` | 10228 字节 | 10228 字节 | 一致 |
| 老命名 `default.png` | `404 text/html 146` | — | 反证：36 张新命名图确实已上线 |

**所以部署是完整的。** 真因是**缓存**，而且是一个自己给自己埋的坑：

| 环节 | 事实 |
| --- | --- |
| 文件名 | 36 张图是 `<风格>--<色系>.png` 组合键，**不含内容 hash**（与 `/assets/` 恰好相反） |
| 重出图 | 版式改了、图重出了，**URL 一个字都不变** |
| 源站 | `/theme-previews/` 当时**没有 location 段**，不发 `Cache-Control` |
| CDN | Cloudflare 对静态图片默认补 `max-age=14400`（4 小时） |
| 实测响应头 | `cache-control: max-age=14400`、`cf-cache-status: MISS`、`server: cloudflare` |
| 用户感知 | 切风格 → 看图 → 还是旧的 →「是不是文件没传全」；Network 面板全 200、控制台零红字 |

这是典型的**静默失败**：没有任何一处报错，但用户完全无从察觉真相。

**两处修复 + 一处清理**：

| 层 | 改动 | 文件 |
| --- | --- | --- |
| URL 破缓存 | `previewUrl()` 拼 `?v=${__APP_BUILD__}`，`__APP_BUILD__` 由 vite `define` 注入发版时间戳（`vitest.config.ts` 与 `env.d.ts` 同步补上，否则测试与类型检查会挂） | `MpTheme.vue`、`vite.config.ts`、`vitest.config.ts`、`env.d.ts` |
| 源站不缓存 | `location ^~ /theme-previews/` 发 `Cache-Control: no-cache` + `try_files $uri =404`，并重声明四个安全头（`add_header` 继承陷阱） | `nginx-site.live.conf`、`nginx-site.conf`、`frontend/nginx.conf` |
| 发版自检 | ① 逐张核对 `dist/theme-previews/` 与 `manifest.json` 组合键，缺一张 `die`；② 探测线上 `default--default.png` 必须是 `200 image/png`，拿到 `text/html` 判红 | `scripts/deploy_frontend.sh` |
| CDN 清理 | Cloudflare Purge `/theme-previews/*`（**只能由本人操作**，脚本无权） | 面板 |

**守门**：`tools/nginx-check` 新增 §6.6「主题预览图绝不长缓存」12 条断言（200 / `image/png` /
`no-cache` / 不带 `immutable` / 不带 `max-age` / 四个安全头仍在 / 缺图真 404 / 缺图不是 SPA 壳 /
缺图不进长缓存），总数 95 → **107 项**。Miniprogram 侧 `tests/theme.spec.ts` 另加一条源码级断言，
钉住 `?v=${__APP_BUILD__}` 与两份 nginx 的 `no-cache`、`try_files $uri =404`。

**反向验证（三次，因为前两次都没让断言变红——这本身很有价值）**：

1. 把 `no-cache` 改回 `public, max-age=14400` → 「预览图 no-cache」「预览图不带 max-age」**双红**，符合预期。
2. 删掉 `try_files $uri =404` → 断言**仍全绿**。原因是本 location 里不写 try_files 也是 404
   （nginx 直接去 root 找文件），根本不会落到 `@spa`。**说明我原注释里「缺图会落进 SPA 兜底」
   写错了**，已修正三份配置的措辞。
3. 把 `try_files` 改成 `try_files $uri @spa`（唯一真会 fallback 的写法）+ `@spa` 改为
   `return 200 "SHELL"` → 「缺图是 404」变红（拿到 `200`）、「缺图不能是 SPA 壳」变红（拿到 `SHELL`）。
   这才是这条断言真正的守备对象：**防以后有人顺手把这里改成 `@spa`**。

> 中间还踩了一次自己的坑：先把 `@spa` 里的 `return 404` 改成 `rewrite ^ /index.html last`，
> 断言没红——因为**命名 location 里的 `rewrite ... last` 不生效**，注入根本没构造出坏行为。
> 换成 `return 200 "SHELL"` 才真正复现。

**用户侧待办（本人执行）**：① `git push origin main`；② 按 `nginx-site.live.conf` 更新宝塔站点配置
（保存时自动 `nginx -t` + reload）；③ Cloudflare Purge `/theme-previews/*`；
④ 重跑 `./scripts/deploy_frontend.sh --build` 让新构建戳生效；⑤ 小程序改了 WXSS/wxml，
**必须重新提审发版**，端上不能热加载。

## 第四十五轮：预览图拆成「缩略图 + 原图」两档（P7，2026-11-12）

用户提的：

> 「这些预览图保存一种缩略图和一种大图，默认显示的是缩略小图，点击查看大图的功能本身就是有的，
> 这样是不是性能会更好些」

判断成立，而且收益比"更好一些"更大。先量清楚再动手：

| 事实 | 数值 |
| --- | --- |
| 后台预览窗口宽度 | 230px（`.theme-preview{aspect-ratio:375/750}`） |
| 原图尺寸 | 750 × 2588 / 2364 / 5144 / 3458（2x） |
| 原图体积 | 0.78～1.86 MB，36 张合计 **40.9 MB** |
| 原图解码内存 | `750×5144×4` ≈ **15 MB**（warm 款） |
| 加载时机 | 每次切风格 / 换色都重新拉（`/theme-previews/` 是 `no-cache`） |

也就是说：把一个 15 MB 解码、1.8 MB 传输的位图，缩到 230px 宽的窗口里显示，
而且每换一次配色就重来一遍。这跟"点开大图才需要细节"的场景完全不匹配。

**做法**：每个组合出两档，文件名只差一个 `.thumb` 后缀。

| 档 | 文件 | 规格 | 体积 | 用在哪 |
| --- | --- | --- | --- | --- |
| 缩略图 | `<id>.thumb.png` | 460 宽等比，128 色 PNG8 | 100～212 KB | 列表窗口（460 = 230px × 2x） |
| 原图 | `<id>.png` | 750 宽，原样 | 0.78～1.86 MB | 「查看大图」浮层，点开才请求 |

36 张缩略图合计 **4978 KB**，省 88%；单张最大从 1866 KB 降到 212 KB（省 89%）。
解码内存从 15 MB 降到 `460×3155×4` ≈ 5.5 MB。

**规格是量出来的，不是拍的**：

| 候选 | 单张体积 | 取舍 |
| --- | --- | --- |
| 460 宽真彩 PNG | 548 KB | 太胖，省得不到一半 |
| 460 宽 256 色 | 160 KB | 可用，但没必要多花 26% |
| **460 宽 128 色（选定）** | **127 KB** | 逐张 ViewImage 核对：文字清晰、照片无色带 |
| 460 宽 WebP q82 | 81 KB | 更小，但要额外处理 MIME（`nosniff` 下 MIME 错会被拒渲染），收益不值这个风险 |

460 这个宽度也是算出来的：卡片 230px × 2x = 460，再低在 Retina 上就糊；
而"一眼看出四款版式差异"是缩略图唯一用途，糊了就白做。

**改动清单**：

| 文件 | 改什么 |
| --- | --- |
| `scripts/gen_theme_previews.py` | 新增 `THUMB_WIDTH/THUMB_COLORS/THUMB_SUFFIX` 与 `make_thumb()`（ImageMagick）；`--check` 分 `fulls/thumbs` 两套校验；清理过期图改用 `split_preview_name()`（否则 `x.thumb.png` 的 stem 是 `x.thumb`，会被当过期图连原图一起删）；manifest 增 `thumbWidth/thumbColors` |
| `frontend/src/views/content/MpTheme.vue` | `previewUrl` 拆成 `thumbUrl`/`fullUrl`，列表窗口用前者、浮层用后者 |
| `frontend/src/views/content/MpTheme.spec.ts` | 全部列表图断言指向 `.thumb.png`；大图断言加 `not.toContain('.thumb')` |
| `miniprogram/tests/theme.spec.ts` | 文件集合过滤掉 `.thumb.png`（否则 36 张比成 72 张）；新增用例钉住两档分离 + 逐张读 PNG 头校验尺寸体积 |
| `scripts/deploy_frontend.sh` | 发版对账改成「每个组合两个文件」，线上自检两档都探 |
| `tools/nginx-check/gen_site.py` / `verify.sh` | 桩目录补缩略图；§6.6 加 3 条断言（缩略图也是 `image/png` + `no-cache` + 不带 `max-age`），107 → **110 项** |

**静默失败防线（这轮的核心，不是生图）**：三处退化都不会报错，只会「感觉有点慢」或「图好像没变」：

1. **前端指错档位**——列表图指回原图，页面完全正常，只是每切一次拉 1.8 MB。
   → 源码断言：`class="preview-shot"` 后面必须是 `thumbUrl(`，且**不能**是 `fullUrl(`。
2. **缩略图没生成 / 没传**——`<img>` 404 后走 CSS 兜底示意图，Network 里看得到 404 但没人会盯。
   → `--check` 报「缺缩略图」，`deploy_frontend.sh` 拒绝发版，线上自检两档都探。
3. **缩略图像素没变**——文件名叫 `.thumb.png`，内容还是 750 宽原图（比如 `cp` 错了）。
   → `--check` 断言宽度必须是 460、高度按原图等比 ±1px、体积必须 < 原图 60%。

**反向验证（三次，全部变红后还原）**：

| 注入 | 结果 |
| --- | --- |
| 列表图改回 `fullUrl(currentId)` | `--check` 无关（图都在），但 4 条测试全红：「列表窗口必须用缩略图」「expected ... to contain ':src="thumbUrl(currentId)"'」 |
| 删掉 `default--default.thumb.png` | `--check` →「缺缩略图（后台会退回拉整张原图）：['default--default']」；Vitest →「default--default 缺缩略图：后台会退回拉整张原图」 |
| 用原图覆盖 `ink--default.thumb.png` | `--check` →「缩略图异常：['ink--default((750, 2364), 期望 460x1450)']」；Vitest →「ink--default 缩略图宽度: expected 750 to be 460」 |

**验收**：36 张原图逐字节未变（Chrome 截图是确定性的，git 里只多出 36 个 `.thumb.png` +
`manifest.json` 两行字段），`--check` 通过，nginx-check 110 项全绿。

## 第四十六轮：主题两轴的租户语义固化（初始值 + 互不影响，2026-11-12）

用户确认口径：

> 「每个企业初始都是默认的整体风格为标准均衡，品牌配色为默认色吧，不会互相影响吧」

两句话都对，但「不会互相影响」这种事不能靠读代码口头保证，本轮逐层核实并固化成测试。

**整条链路的证据**：

| 层 | 实现 | 位置 |
| --- | --- | --- |
| DB 默认值 | `style_id VARCHAR(32) NOT NULL DEFAULT 'default'`、`brand_color_id VARCHAR(32) DEFAULT NULL` | 迁移 `0042`、`model.HomeLayout` 的 gorm tag |
| 开通企业 | 事务里只写 `HomeLayout{CompanyID, Order}`，风格两列留给 DB 默认 | `handler/companies.go` |
| 后台读 | 无行时回 `mptemplate.DefaultID`（`"default"`）+ `nil`，不是空串 | `content.go` GET `/api/admin/home-layout` |
| 租户判定 | 企业管理员**强制**取 JWT 里的租户（`?companyId=` 无效）；平台超管才可显式切换 | `tenant.go` `targetCompanyID` |
| 读写条件 | 读 `First(company_id = ?)`；写作用于查出来的那行主键 | `content.go`、`repository.GetHomeLayout` |
| DB 兜底 | `home_layouts` 上 `uk_home_layout_company(company_id)` 唯一索引，一家最多一行 | 迁移 `0011` |
| 访客读 | `publicCompanyID` 取 `?companyId=`（缺省 1 号租户），小程序请求层对 `/api/home-layout` 自动注入 | `handler.go`、`miniprogram/utils/request.ts` |
| 端上归一 | `normalizeStyle(空/未知) → 'default'`；`normalizeBrand(空/未知) → null`（= 该风格默认色） | `miniprogram/utils/theme.ts` |

**实测（临时 Go 测试，跑完即删）**：模拟 `companies.go` 的写入形态（只给 `CompanyID` + `Order`），
落库结果是 `style_id="default"`、`brand_color_id=NULL`；随后把 10 号企业改成 `warm` + `brand-crimson`，
9 号企业仍是 `default`/`NULL`。证明 GORM 的 `default:default` tag 在 `Create` 时确实省略该列、
让 DB 默认生效，而不是把零值 `""` 写进去。

**新增守门**：`TestHomeLayoutTenantIsolation`（SQLite 与 MySQL parity 同一套断言风格）钉住四件事：

1. 新开通企业的 `GET /api/admin/home-layout` 必须是 `styleId="default"` + `brandColorId=null`；
2. 改 1 号租户为 `warm` + `brand-crimson` 后，乙公司读出来仍是 `default`/`null`；
3. 改乙公司为 `ink` 后，1 号租户仍是 `warm` + `brand-crimson`；且只提交 `styleId` 时
   **不能顺带写脏** `brand_color_id`（仍是 `null`）；
4. 访客侧 `/api/home-layout` 与 `?companyId=` 分别取到两个租户各自的值。

**反向验证**：把读接口的 `First(&hl, "company_id = ?", cid)` 改成不带条件（模拟「有人写出串租户
bug」），测试立刻红：`expected: "warm" / actual: "ink"`——正是「乙公司的配置盖到了 1 号租户」的
症状。还原后恢复全绿。

**两个必须知道的边界（不是 bug，是设计）**：

1. `brand_color_id` 存的是 **NULL**，不是字面量 `"default"`。语义上「NULL = 用该风格自带默认色」，
   端上 `normalizeBrand` 同样把 NULL/未知值都归成 `null`。**不要**图省事把它改成 `"default"`：
   白名单里没有 `default` 这一档，`normalizeBrand("default")` 会当未知值丢掉，两套语义就开始打架。
2. 访客侧缺省落 **1 号租户**。所以「企业独立小程序」必须让请求带上 `companyId`（独立版是锁定值，
   平台模式是扫码/分享粘滞值），否则访客会看到 1 号租户的主题。小程序 `request.ts` 已对
   `/api/home-layout` 自动注入，这条链路有 `themeBootstrap.spec.ts` 钉住。
   另外平台超管可以用 `?companyId=N` 改任意企业的主题——这是产品设计（平台运营），不是越权。

## 第四十七轮：预览图缓存策略改口——从 `no-cache` 到「URL 带戳 + immutable 一年」（2026-11-12）

用户提的：

> 「这些图片是会走cloudflare缓存的吧，要总重新从服务器拉取的话消耗带宽太大了」

判断成立。第四十四轮为了修「切风格预览图不变」，给 `/theme-previews/` 发了 `no-cache`
（每次回源校验）。它确实防住了旧图，但**把 CDN 废掉了**：

| 事实 | 数值 |
| --- | --- |
| 36 组原图合计 | **40.9 MB** |
| 36 张缩略图合计 | **4978 KB** |
| 后台切一次风格会取 | 1 张缩略图（列表窗口）+ 可能的 1 张原图（点开大图） |
| `no-cache` 的代价 | 每个用户每次刷新都要回源校验，CF 边缘几乎不命中 → 跨境 RTT + 源站带宽 |

也就是说，第四十四轮的方案把「防旧图」和「省带宽」当成了对立面。但这两件事其实不冲突：
**旧图之所以能被看到，是因为 URL 没变；让 URL 变，就可以随便缓存。**

**现行三层结构**（缺一层都不成立）：

| 层 | 做法 | 位置 |
| --- | --- | --- |
| ① URL 变 | `previewAssetUrl()` 拼 `?v=__APP_BUILD__`（vite `define` 注入发版时间戳） | `frontend/src/views/content/MpTheme.vue`、`vite.config.ts` |
| ② 按状态码分流 | `map $status $preview_cache`：200/206/304 → `public, max-age=31536000, immutable`；404/410/5xx → `no-store`；其余 → `no-cache` | `nginx-site.live.conf`、`nginx-site.conf`、`frontend/nginx.conf`（各带一份 map） |
| ③ 缺图真 404 | `try_files $uri =404`（沿用第四十四轮，防 `@spa` 兜底） | 同上 |

`location ^~ /theme-previews/` 的 `add_header Cache-Control` 由字面量改成 `$preview_cache`。
**为什么不能写死 immutable**：`add_header` 区分不了状态码，写死则 404 也带一年，
CF 会把「这张图不存在」锁死——`/uploads/` 在 2026-11-03 踩过同款坑（见 runbook §5.2.4）。
这不是假想风险：本轮排查时实测线上缺图返回 `404` + `max-age=14400`，**确实会被缓存**。

**为什么长缓存也不会看到旧版式**：图变了必然伴随发版，发版必然换 `__APP_BUILD__`，
于是新版式与旧版式是两个不同的 URL。缓存里留着旧 URL 的旧图完全无害——已经没有页面引用它了。
这与 `/assets/` 那套「文件名带 hash → immutable 一年」是同一条思路，只是 hash 放在查询串上
（文件名要保持稳定，部署脚本与 `manifest.json` 都按组合键对账）。

**守门**：

- `tools/nginx-check` §6.6 改写（仍是 15 条，总数维持 **110 项**）：
  `chk` 200/206 的 `public, max-age=31536000, immutable`、`bad` 退回 `no-cache`、
  缺图 `chk` `no-store` + `bad` 带 `max-age`、缩略图同档；
- `miniprogram/tests/theme.spec.ts` 的源码级断言从「两份 nginx 必须 `no-cache`」改成
  「三份 nginx 必须用 `$preview_cache`、**不得**出现字面量 `no-cache`、map 必须存在且在
  server 块之前（http 级）、200 必须 immutable、404 必须 no-store」；
- `deploy_frontend.sh` 的失败提示改成「先确认 URL 带的是新构建戳，再 Purge」。

**反向验证（两条，都确认会红）**：

1. 把 `$preview_cache` 里的 `404 "no-store"` 改成 `404 "public, max-age=31536000, immutable"`
   → nginx-check 报 **2 项失败**（`缺图 no-store`、`缺图不带 max-age`），其余 108 项仍绿。
   说明 `map` 分流不是摆设，确实是这两条断言在守。
2. 把 `frontend/nginx.conf` 的 `add_header Cache-Control $preview_cache always;` 改回
   `"no-cache"` → `theme.spec.ts` 变红（`Tests 1 failed | 55 passed`），报错信息直接指出
   缺 `$preview_cache`。还原后 56/56 全绿。

**两个必须知道的边界**：

1. **`immutable` 一年要被浏览器真正吃到，CF 侧得配合**。CF 默认的 Browser Cache TTL 会把
   源站的 `Cache-Control` 覆盖成 `max-age=14400`。按 runbook §5.2.5 方案 A
   （Browser Cache TTL → **Respect Existing Headers**）即可，顺带让 `/assets/`、`/uploads/` 一起受益。
2. **换缓存策略必须清一次 CF**。边缘上留着的旧 `max-age=14400` 副本不会自己变短——
   Purge `/theme-previews/*` 或 Purge Everything。别只在浏览器里 Ctrl+F5 验收（那只清本地）。

> 历史记录保留原文：第四十四轮的「源站不缓存」一格、第四十五轮的「`/theme-previews/` 是 `no-cache`」
> 描述的是当时状态，以本轮为准。

**用户侧待办（本人执行）**：① `git push origin main`；② 按 `nginx-site.live.conf` 更新宝塔站点配置
（**注意文件顶部新增的 `map $status $preview_cache` 必须一起粘到 server 块之外**，漏了 nginx 会
报 `unknown "preview_cache" variable` 起不来）；③ Cloudflare Purge `/theme-previews/*`；
④ 重跑 `./scripts/deploy_frontend.sh --build` 让新构建戳进入页面。本轮不涉及小程序端，无需重新提审。

## 第四十八轮：性能优化评估 + P1「公开只读接口 60 秒边缘缓存」（2026-11-12）

起因是「还有哪些能优化的地方」。第一版想当然列了四条，逐条实测后被数据推翻两条，
又发现自己对 element-plus 打包的判断是错的——结论因此重排。

### 一、被实测推翻的判断（先记，免得以后再走一遍）

| 判断 | 实测结果 | 处置 |
| --- | --- | --- |
| `Employees.vue` / `ApplyOpen.vue` 头像直出原图会拖慢后台 | 真实头像 `avatar-gaolixin.png` **仅 3KB**，`_thumb` 也是 3KB | 收益 0，**放弃** |
| `_thumb` 已经在省带宽了 | `cat-1.png` 是 256×256 / 42KB，它的 `_thumb` **也是 42KB**——缩略图不按显示尺寸缩；`cat-*` 8 张共 291KB 完全没优化 | 降级为 P2（需改 `thumbbackfill` 规格） |
| element-plus 是「摇不掉」，可以从导入方式上优化 | **五条路全部 0 收益**，见下 | 负结论落库，不再尝试 |

**EP（element-plus）五路实测**（产物字节数百位不变，连内容 hash 都一样）：

1. 48 个 SFC 把 `ElMessage` / `ElMessageBox` 改成子路径导入
   （`element-plus/es/components/message/index`）；
2. 去掉 `vite.config.ts` 的 `manualChunks` 里对 `element-plus` 的强制分包；
3. `build.treeshake`（放错层级，实际是空转一轮——它该在 `rollupOptions` 下）；
4. `rollupOptions.treeshake.moduleSideEffects` 对 EP 组件返回 `false`（样式目录除外）；
5. 自写 `enforce:'post'` 插件把桶导入拆成 127 个深路径导入。

根因（用 `buildEnd()` + `getModuleIds()` + `getModuleInfo(id).importers` 查出来的）：

```
[DIAG] watermark: importers=["/node_modules/element-plus/es/component.mjs",
                             "/node_modules/element-plus/es/index.mjs"]
[DIAG2] /element-plus/es/index.mjs importers=["/src/App.vue?…", （共 48 个 SFC）]
```

`unplugin-vue-components` 给**每个** SFC 注入的是同一个共享模块 `element-plus/es`，
而 `es/index.mjs` → `es/component.mjs` 把 122 个组件聚合成一个 `makeInstaller([...])`
数组。`App.vue` 引用了它，于是整库 823KB 进首屏。**不是摇不掉，是本来就被引用了。**

顺带记一条踩坑：插件在 `enforce:'post'` 下仍看不到注入的导入
（`id.includes('/src/App.vue') && code.includes('element-plus')` 恒 false），
但 Rollup 的 `importers` 明确指认 App.vue 是 importer——注入发生在 Vue 插件编译之后、
`transform` 钩子看不到的阶段。所以第 5 条路线从原理上就不成立。

**当时认为唯一能真正降首屏的方向是多页拆包**（登录页与后台两个入口，首屏各自只装自己那半），
代价是要动 `vite.config.ts` 的 `rollupOptions.input` 与路由守卫落地页，属于结构变更，
排到 P3。评估全程未提交任何改动（`git checkout -- src` + 还原 `vite.config.ts`）。

> ⚠️ **订正（第五十轮，2026-11-13）**：上面「五条路全 0 收益 ⇒ 只能多页拆包」的结论
> **已被推翻**。当时漏了第二条独立的桶入口来源（54 处命令式 API 的裸包导入），
> 每条路线只掐断一条，另一条仍把整库拉回来，所以字节数才一字不变。两条同时掐断
> 即可全站降 25.7%，无需 MPA。详见本文末「第五十轮」。

### 二、P1 落地：5 条公开只读接口加 60 秒边缘缓存

名片页首屏 4 个接口并发（`card` / `home-layout` / `achievements` / `services`），
每次都要付一次跨境回源（实测一次往返 300～400ms）。这些接口内容**只由 URL 参数决定**，
没有登录态、没有访客相关字段，因此可以交给边缘缓存。

改动只有两处（`handler/pagination.go` + `handler/handler.go`）：

| 改动 | 内容 |
| --- | --- |
| `cdnCache` | 从只发 `max-age` 改成**同时发** `max-age` 与 `s-maxage`（同值） |
| 新增调用 | `/api/card`（`code==0` 支）、`/api/home-layout`（成功支）各加 `cdnCache(c, 60)` |

**为什么两个 TTL 都发**：`s-maxage` 是共享缓存（CDN / 反代）的专用指令，但各家实现
取哪个字段并不统一。只发 `s-maxage`，遇到只读 `max-age` 的边缘节点就等于没配——
表现为「头看着对、`cf-cache-status` 一直 `DYNAMIC`」这种**零报错**的静默失效。
两个都发同值，谁读都对。

**为什么只在成功分支调用**：`code != 0` 的几支（员工不存在 / 名片停用 / 企业停用 /
查询失败）一个头都不能带。错误响应一旦带上 `Cache-Control`，边缘会把故障也缓存
60 秒——「员工不存在」被缓存事小，接口抖动期间把 **500 缓存住**才是灾难。

### 三、守门

| 位置 | 断言 |
| --- | --- |
| `handler/public_list_test.go` `TestPublicListCacheHeader` | 5 条公开接口**同时**带 `public` + `max-age=60` + `s-maxage=60`；错误分支（企业不存在 / 员工不存在 / 名片停用）**一个头都没有**；`/api/team` 成功但**刻意不带头** |
| 同上（新增的前置断言） | 每条都先确认信封 `code` 真是期望值——否则「验的其实是另一条路径」，断言全绿也说明不了问题 |
| `tools/nginx-check` §3.7（+3 条，总数 110 → **113 项**） | 桩后端在 `/api/card` 自己发 `Cache-Control`，断言 nginx **原样透传**（含 `s-maxage`）、**只有一个** `Cache-Control`（没叠加 nginx 自己的）、且没被降级成 `no-cache` |

§3.7 这条是新发现的风险：三份 nginx 配置的 `/api/` 段**都没有** `add_header
Cache-Control`（server 级也没有），所以后端头原样透传是「碰巧对」。一旦有人按
`/assets/` 的写法给 `/api/` 也加一个 `add_header Cache-Control`，就会变成两个值并存，
CDN 取哪个不确定，而**从响应上根本看不出「到底生效没」**。桩后端现在会主动发这个头，
把这条隐式约定变成断言。

反向验证时还挖出**第二个后果**（比双份头更严重）：给 `location /api/` 加上
`add_header Cache-Control` 后，反向验证报 **5 项失败**——除「只有一个 Cache-Control」
之外，`/api/admin/login` 的 **4 条安全头断言全红**。原因是 `add_header` 继承陷阱：
那个 location 里没有重声明安全头，server 级那 5 个头**整组失效**。也就是说，
「顺手补个缓存头」的实际后果是「管理端接口失去 nosniff / X-Frame / Referrer-Policy」。

**反向验证（三条，都确认会红）**：

1. 把 `/api/card` 的 `cdnCache(c, 60)` 提到两个错误分支之前 →
   `TestPublicListCacheHeader` 红：`员工不存在的错误响应不应被缓存` /
   `Should be empty, but was public, max-age=60, s-maxage=60`。
2. 把 `cdnCache` 改回只发 `max-age` → 红：`/api/projects 缺 s-maxage：CDN 拿不到
   共享缓存语义，同样退化成不缓存`。
3. 给 `location /api/` 加 `add_header Cache-Control` → nginx-check **5 项红**
   （双份头 + 4 条安全头失效），还原后 **113/113 全绿**。

### 四、CF 侧三条硬约束（写进 runbook §5.2.7）

1. **绝不写成 `/api/*`**。管理端接口全在 `/api/` 下，带 `Authorization` 但**不带**
   `Cache-Control`。今天的语义是「没头 → 绕过缓存」所以看着安全，但这等于把「管理端
   响应能不能被缓存」交给一条路径通配符——将来任何人给某个管理端 GET 顺手加上
   `cdnCache`，就是**跨租户数据串号**（CF 缓存键不含 Authorization）。
2. **用「等于」不用「通配符」**。`/api/projects*` 会连带覆盖 `/api/projects/:id`
   （详情接口刻意没挂缓存头：后台改完详情要立刻能验）。
3. **Cache Level 必须是 Standard**。否则 `?staffId=1` 与 `?staffId=2` 折叠成同一个
   缓存对象——甲员工的名片会被发给找乙员工的访客，且零报错（同 §5.2.6）。

### 五、代价与边界

- **可见代价**：后台改职务 / 服务后，访客最长 **60 秒**后才看到。管理员改完立刻扫码
  验证最容易误判成「没生效」——等一分钟，或加 `?v=$RANDOM` 强制 MISS。
- **`/api/team` 刻意不缓存**：它是路由决策（裸启动落名片墙还是某员工名片），缓存会
  让管理员以为设置没生效，反复改反复验。
- **端上拿不到收益**：小程序 `wx.request` 不利用 HTTP 缓存，省下的只是 CF 节点那一次
  回源。所以这条优化的收益取决于「同一张名片被重复打开」的比例。
- **还没生效**：**CF 侧那条 Cache Rule 必须由本人加**（runbook §5.2.7 有逐项表格），
  否则后端发了头、CF 依旧 `DYNAMIC`。

### 六、后续排序（评估后确定）

| 优先级 | 事项 | 理由 |
| --- | --- | --- |
| ~~P1~~ | ~~公开只读接口边缘缓存~~ | **本轮已完成** |
| P2 | 缩略图按显示尺寸出图（图标 256→128 + PNG8，改 `thumbbackfill` 规格 + 存量回填） | `cat-*` 8 张 291KB 完全没优化，是唯一有实测收益的前端项 |
| P3 | 后台多页拆包（登录页 / 后台两入口） | 唯一能真正降首屏的方向，但要动构建与路由结构 |
| P4 | 接入路径（国内 CDN / 备案 / 源站访问控制重构） | 收益最大但成本最高，且依赖外部条件 |

## 第四十九轮：P2 缩略图不改尺寸、改编码（PNG 调色板量化，2026-11-13）

上一轮评估里写的 P2 是「缩略图按显示尺寸出图（**图标 256→128** + PNG8，改
`thumbbackfill` 规格 + 存量回填）」。动手前先做了完整评估，发现那个 128 是错的，
用户确认后改成「保持尺寸、只做 PNG8 量化」。

### 一、被推翻的假设：不存在「按显示尺寸下采样」这回事

| 位置 | CSS 显示宽 | 3x 屏物理像素 |
| --- | --- | --- |
| 分类图标 `.cat-icon`（warm 风格 `.cat-item` 50%） | ≈347rpx | **≈545px** |
| 服务图标 `.svc-icon`（25% 列） | ≈170rpx | ≈267px |
| 精选封面 `.feat-cover`（50% 列） | ≈375rpx | ≈589px |
| 项目列表 `.item .cover` | 160rpx | ≈251px |
| 团队头像 `.team-avatar` | 120rpx | ≈188px |

256×256 的原图**本来就小于**这些显示尺寸。缩到 128 只会让图标更糊，而且不会报错——
属于典型的「为了省字节牺牲画质，还以为自己在做适配」。

真正的根因在编码侧：`resizeContain` 在原图 `≤ ThumbMaxEdge(480)` 时**原样返回**，
`encodeImage` 又对 PNG 走无损 `png.Encode`，于是 256×256 的图标被原封不动重存一份，
`_thumb` 与原件体积一字不差。JPEG 分支一直正常（缩 + q80，实测 680×400 的封面
67KB → 32KB，省约 50%），**不用动**。

### 二、三种方案的实测（16 张演示 PNG，合计 951.2 KB）

| 方案 | 结果 | 画质 |
| --- | --- | --- |
| 现状（无损重编码） | 951.2 KB → 约 951 KB | 无损，等于白存一份 |
| 缩到 128px + PNG8 | 53.6 KB（5.6%） | 明显糊 |
| **保持尺寸 + PNG8 256 色量化（采用）** | **199.9 KB（21%）** | 三张代表性图目视无可见差异 |

ImageMagick 原型先验证了量化收益（951→292KB），Go 生产版用中位切分把调色板
选得更准，最终 199.9KB。内置素材库 748 张图标单独跑：**6986 KB → 2793 KB（40%）**，
全量耗时 9.7 秒。

### 三、实现（`backend/internal/storage/png8.go` + `image.go`）

- **中位切分量化**：5bit/通道直方图 → 按「最大通道跨度 × 桶数」反复切盒 →
  盒内加权平均色作调色板 → 逐像素写 `ColorIndex`；输出 `image.Paletted` +
  `png.BestCompression`，保留 alpha（图标普遍带透明背景与抗锯齿半透明边缘，
  只按 RGB 切会让边缘出脏色）。纯标准库，后端二进制不依赖 ImageMagick。
- **两条链路收敛到同一个 `processThumb`**：`ProcessUpload`（新上传）与
  `Thumbnail`（存量回填）此前是两份几乎一样的代码，改规格极易只改一边；
  现在逐字节一致，并有测试守门。
- **上传路径补兜底**：回填路径早有「绝不写出比原图更大的缩略图」，上传路径没有。
  8×8 单色图实测量化后 82 → 87 字节，兜底让它直接复制 `full`（`_thumb` 仍存在，
  端上不会 404 再回退一次）。
- **量化失败回退无损 PNG**（fail-open），不让 `_thumb` 凭空消失。
- `ThumbMaxEdge` **保持 480 不动**，并在 `image.go` 注释里写清「别把它调小去按
  显示尺寸出图」的理由，防止以后有人照着 128 这个旧数字改回来。

### 四、守门与反向验证

`png8_test.go` 新增 7 条：真实图标体积至少减半且**尺寸不变**、输出必须是
`*image.Paletted` 且 ≤256 色、透明背景不丢、平均通道误差 ≤24、上传/回填逐字节一致
（PNG + JPEG 两路）、上传绝不写出更大的 thumb、JPEG 路径不受波及、16 张素材
总收益 ≤40%、连续三次量化确定性；`backfill_test.go` 把「小图一律原样存」的旧用例
改成真正的单色图（继续覆盖兜底），另加一条「照片类小图现在也能变小」。

**反向验证三条，全部确认会红**：

1. 把 `encodeThumb` 的 PNG 分支改回 `encodeImage`（关掉量化）→
   `TestBackfillTinyPhotoNowShrinks` / `TestThumbPNGQuantizesWithoutResizing` /
   `TestThumbPNGSavesAcrossSeedAssets` 三条红。
2. 去掉上传路径的体积兜底 → `TestProcessUploadNeverWritesBiggerThumb` 红
   （`缩略图 87 字节 > 落盘原图 82 字节`）。
3. 去掉直方图按 key 排序（直接用 `range map`）→ `TestQuantizePNGDeterministic`
   与 `TestProcessUploadAndThumbnailAgree` 双双红，且 `-count=2` 稳定复现。

第 3 条是**本轮抓到的真 bug**，不是假想：Go 的 map 迭代顺序随机，桶的初始顺序会
影响中位切分里「相等元素」的相对位置，同一张图两次量化差 31~335 字节。视觉上完全
一样，但 `-force` 回填会反复改写文件、无谓打掉 CDN 缓存，上传与回填也对不上。
「同源一致」这条断言本来是为了防链路漂移，结果把确定性也一起守住了。

### 五、运维动作（runbook §4.5.1）

**必须 `-force` + Purge，缺一件就是静默失败**：

```bash
cd backend
go run ./cmd/thumbbackfill -dry-run -force
go run ./cmd/thumbbackfill -force
# Cloudflare → Caching → Purge：/uploads/*
```

> ⚠️ **订正（2026-11-13，本文多处 `/uploads/*` 均为简写）**：CF 面板「自定义清除」
> 要选 **前缀**，填 `<你的域名>/uploads/`——**不带 `https://`、不带 `*` 通配符**。
> 照抄 `*` 会被当成不存在的路径，UI 提示清除成功、实际一个缓存都没清（返回 200
> 只代表请求被接收）。完整步骤与验收见 runbook §4.5.1；下文本轮/下轮的待办里
> 出现的同一写法也按此理解。

原因：`/uploads/` 的 200 响应是 `public, max-age=31536000, immutable`（§5.2.5），
而 `-force` 是**原地覆盖同名文件**、URL 一个字都不变。不清 CDN，边缘会继续喂一年前的
旧 thumb，页面全 200、控制台零报错，只是流量一点没省——与第四十七轮预览图事故同类。
验证用 PNG 头第 26 字节的 color type（3 = 调色板），只依赖 Python 标准库。

另记一条既有事实：素材采用走 `SaveBytes("library/<id>.<ext>")`，只写原图不生成
`_thumb`，端上派生会先 404 再回退。采用新素材后跑一次不带 `-force` 的回填即可补齐；
回填会扫到 `uploads/library/` 子目录。

**用户侧待办**：① `git push origin main`；② 线上按 §4.5.1 跑 `-force` 回填；
③ **Cloudflare Purge `/uploads/*`**；④ 上一轮 P1 的第 5 条 Cache Rule 若还没加，
一并补上（runbook §5.2.7）。

## 第五十轮：P3 Element Plus 深路径按需引入（首屏 −25.7%，2026-11-13）

### 一、方向变更：不做多页拆包，改做深路径按需导入

第四十八轮把 P3 定成「登录页 / 后台两入口 MPA」。动手前重新评估，否掉：

- MPA **只优化未登录登录页**首屏，已登录后台首屏仍要完整下载同一份资源；
- 登录成功后整页重载，反而把后台资源多拉一遍；
- 登录页本身用 `el-card` / `el-form` / `el-input` / `el-button`，`api/request.ts`
  又静态 import `ElMessage` 与 router，真要甩掉 EP 需重写原生 UI 并解耦请求层；
- 真 MPA 还要改 nginx SPA 兜底与发版脚本，属于结构变更。

用户选择「改做深路径按需导入」：不动路由结构，全站首屏一起降体积。

### 二、订正第四十八轮：「五路全败」的真正根因是两条独立来源

旧结论「别试图靠 tree-shaking 给 element-plus 瘦身」不再成立。整库进首屏有
**两个互不依赖的入口**，指向同一个 `element-plus/es` 桶（→ `es/component.mjs`
聚合 111 个组件）：

| # | 来源 | 注入点 |
| --- | --- | --- |
| 1 | 模板 `<el-*>` | 官方 resolver 给**每个** SFC 注入同一个共享桶入口 `element-plus/es` |
| 2 | 命令式 API | 54 处 `import { ElMessage, ElMessageBox } from 'element-plus'` 直连包根 |

单独修任一条，另一条仍会把整库拉回来，于是此前五条路线（改子路径、拆
`manualChunks`、`treeshake.moduleSideEffects`、自写插件、只改一处来源）每一条都
**精确地 0 收益**。这不是摇树配置问题，两条同时掐断才见效。

### 三、实现（5 个新文件 + 1 处配置改动）

| 文件 | 作用 |
| --- | --- |
| `frontend/vite/ep-resolver.mjs` | 构建期解析器：读 EP 官方 `es/component.mjs` 文本生成「组件名 → 组件目录」表（**不 import 它**，避免把桶入口带进构建图），产出组件 + 指令两个 resolver |
| `frontend/vite/ep-resolver.d.mts` | 构建期模块的类型声明，供守门 spec 静态 import |
| `frontend/vite/element-plus-runtime.mjs` | 裸 `element-plus` 值导入的薄封装，只 re-export `ElMessage` / `ElMessageBox` 且都走深路径 |
| `frontend/src/vite/ep-resolver.spec.ts` | 13 条行为级 / 源码级守门 |
| `frontend/vite.config.ts` | 换成 `createEPResolvers()`、`dts: false`、alias 精确匹配裸包名 |

**四个关键设计**：

1. **映射不手工维护**：45 种在用组件全部能在官方导出表里定位，升级 EP 自动跟随；
   导出表格式若变（比如改成 `export *`），解析器直接抛错，不静默产出空组件。
2. **复合组件「组件目录 + 样式目录」双映射**：14 个组件的导出在父目录，CSS 却只在
   同名子目录的 `style/` 下（子目录没有 `index.mjs`）——`aside/header/main`、
   `descriptions-item`、`dropdown-item/menu`、`form-item`、`menu-item/sub-menu`、
   `option`、`radio-button/group`、`tab-pane`、`table-column`。只引父目录会静默丢
   样式：早期探针实测少 83 条 `.el-form-item` 选择器、CSS 少 12KB，页面零报错。
3. **`v-loading` 是指令不是组件**，24 处模板在用。单独补 `type: 'directive'` 分支，
   解析成 `ElLoadingDirective` + `loading/style/css`；漏了所有 loading 态静默消失。
4. **不改 54 处源码 / 25 个 `vi.mock('element-plus')`**：Vitest 用独立的
   `vitest.config.ts`（alias 只有 `@`，不读 `vite.config.ts`），所以 vite 侧的
   `resolve.alias` 对测试零影响；dev 与 build 统一走薄封装，不存在两套实现。
   alias 用 `/^element-plus$/` 精确匹配，碰不到 `element-plus/es/...` 深路径，
   实测 EP/es 内部没有任何裸自引用，不会形成循环。

**`dts: false` 的取舍**：自定义 resolver 每次构建都会重写 `components.d.ts` 里的类型
导入路径，在受限构建环境写回会超时。声明文件是纯类型产物、与运行时无关，改为
手工维护 + 守门断言「SFC 里实际用到的每个 `el-*` 都在声明里」，新增组件漏声明必红。
另有一条守门钉住「`vitest.config.ts` 不给 element-plus 加 alias」，防止有人顺手同步
别名把 25 个 mock 全打破。

### 四、实测收益（逐字节，基线 `dist-check-p2b` vs `dist-check-p3final`）

| 首屏文件 | 基线 | 本轮 |
| --- | ---: | ---: |
| 入口 JS | 16,537 | 16,535 |
| vendor JS | 298,645 | 275,847 |
| **Element Plus JS** | **823,973** | **494,368** |
| Element Plus CSS | 227,922 | **227,922** |
| 业务 CSS | 2,564 | 2,564 |
| **首屏合计** | **1,369,641** | **1,017,236** |

**省 352,405 字节（−25.7%，1337.5KB → 993.4KB）**。EP CSS 的 sha256 与基线
**逐字相同**（`c24b8ca8c35c99fd…`）、文件名也相同，17 类关键选择器数量全等——
这是「零样式丢失」的最强证据。EP JS 里 `ElAffix` / `ElCalendar` / `ElCascader` /
`ElCarousel` / `ElTimeline` / `ElTransfer` / `ElColorPicker` 等未用组件标识已清空。

比早期探针的 1,000,298 字节多约 17KB，是**刻意换来的**：探针漏了子样式与
`v-loading`，体积更小但 CSS 少 12KB；本轮补齐后样式与旧版完全一致。体积不是
唯一目标，静默丢样式更贵。

### 五、守门与反向验证

13 条守门覆盖：从 SFC 真实扫出 45 个组件（防止空集合假绿）→ 官方导出表定位 →
深路径与 `index.mjs` 存在 → 14 个复合组件必须双样式 → 未知组件必须**抛错**
（返回 null 会让模板渲染成空白块）→ `v-loading` 指令 → `components.d.ts` 声明 →
薄封装导出集合与源码运行时导入符号**逐字对账** → 命令式 API 的 CSS 仍由
`main.ts` 引入 → vite 配置禁止回潮。

**8 项注入全部使守门变红**，还原后文件 sha256 零漂移、守门复绿：

1. 换回官方 resolver；2. 漏复合组件子样式；3. 未知组件返回 null；
4. 漏 `v-loading` 指令；5. 薄封装漏 `ElMessageBox`；6. `components.d.ts` 漏声明；
7. 改回自动 dts；8. alias 不再精确匹配裸包名。

全量门禁串行跑齐：`golangci-lint` 0 issues、`go test ./...` 全绿、
`gen_theme_previews.py --check` 绿、`eslint .` 0、`vue-tsc --noEmit` 0、
全量 Vitest **96 文件 / 1627 例**全绿、`vite build --outDir dist-check-p3final` 成功、
nginx-check **113/113**、`git diff --check` 干净。

### 六、刻意未做 / 边界

- 不动任何 `.vue` 与既有 spec 的 import 语句，不动后端 / 迁移 / OpenAPI / 数据库；
- 原 P3 的 MPA 方案取消；P4「接入路径（国内 CDN / 备案 / 源站访问控制）」仍开放；
- 产物字节阈值**没有**进 CI：CI 跑 Vitest 时没有 `dist/`，写「若产物存在才检查」
  会造成假绿。字节数由本地构建量测并记在本节，守门是源头（源码 / 行为）级；
- 以后新增 `el-*` 组件需手工在 `src/components.d.ts` 补声明，守门会拦；
  新增「从裸 `element-plus` 取值」的符号需同步加进薄封装，守门也会拦；
- EP 升级若改了 `es/component.mjs` 的格式，解析器会显式抛错而不是静默降级。

**用户侧待办**（沿用前两轮，本轮无新增运维动作）：① `git push origin main`；
② 线上按 §4.5.1 跑 `thumbbackfill -force` 回填 + **CF Purge `/uploads/*`**；
③ P1 的第 5 条 CF Cache Rule（runbook §5.2.7）。

## 第五十一轮：线上迁移 0043 报 1064 的修复（MySQL 8.0 方言事故，2026-11-13）

### 一、现象与根因

用户贴出线上主程序启动日志：

```text
Error 1064 (42000): ... near 'IF EXISTS template_id' at line 19
[MIGRATE][FATAL] 应用数据库迁移失败：应用 0043_drop_template_id.sql 失败
[MIGRATE][FATAL] 服务继续启动，但表结构可能与本版本不一致
listen :8080 (storage driver=s3)
```

根因是 `0043` 写了 `ALTER TABLE ... DROP COLUMN IF EXISTS`，注释还宣称「在 MySQL 8.0
幂等」。**这是 MariaDB 方言**：MySQL 8.0 在 SQL 解析阶段就报 1064，语句根本没执行到
「判断列在不在」那一步。用本地 Docker `wc-mysql-ci`（MySQL 8.0.46）实跑复现三条：

| 语句 | 结果 |
| --- | --- |
| `DROP COLUMN IF EXISTS template_id` | `ERROR 1064 ... near 'IF EXISTS template_id'` |
| 裸 `DROP COLUMN template_id` | 成功 |
| 再裸 `DROP COLUMN template_id` | `ERROR 1091: Can't DROP 'template_id'; check that column/key exists` |

`applyFile` 成功后才 `recordVersion`，所以 0043 没进 `schema_migrations`，修好会重放；
它是当时最后一个迁移，没有后续迁移被连带跳过；P5 之后代码已不读写 `template_id`，
多留一列不影响运行 —— 加上启动迁移失败是刻意的 fail-open，于是「页面一切正常、
日志一条 FATAL、表结构落后一版」，典型的静默失败。

**为什么之前全绿**：`migrations/*.sql` 只跑在 MySQL 上，本地默认 SQLite 走
`AutoMigrate`；不设 `TEST_MYSQL_DSN` 时 parity 用例直接 skip。第四十九 / 五十轮的
全量门禁再绿，也碰不到这条 SQL。

### 二、修复（3 处生产改动 + 3 层守门）

| 文件 | 改动 |
| --- | --- |
| `backend/migrations/0043_drop_template_id.sql` | 改为裸 `ALTER TABLE home_layouts DROP COLUMN template_id;`；注释订正方言结论，写明幂等靠「记账 + 忽略 1091」。注意注释里不能出现半角 `;`（`applyFile` 按 `;` 切句） |
| `backend/internal/migrate/apply.go` | 包注释补 MySQL / MariaDB DDL 差异；`ignoredErrs` 增加 `"Can't DROP"`（1091） |
| `backend/internal/migrate/apply_test.go` | 新增 `TestMigrationsUseMySQL8DDL`：扫全目录、剥注释后禁止 4 种 MariaDB 专属写法；另断言 0043 必须真的删列（防止改成 `SELECT 1` 假修复） |
| `backend/internal/migrate/apply_bookkeeping_test.go` | 新增 `TestIsIgnoredToleratesMySQLMissingDropTarget`（1091 必须忽略，1064 / 1054 / 1553 / 1142 不许被吞）+ `TestMySQLApplyFileDropMissingColumnIsIdempotent`（真实 MySQL 连跑两次裸 DROP） |
| `docs/deploy-runbook.md` / `backend/README.md` | 幂等错误清单补 1091；runbook §4 新增现场应急章节（状态判定表、只换 `.sql` 的最小动作、验收命令） |

**刻意没做**：没有改 `manifest_gen.go`（文件名未变，清单不受影响）；没有把 0043
改成 `information_schema` + `PREPARE` 动态 SQL（`PREPARE` 是会话级的，`applyFile`
逐句 `db.Exec` 会跨连接池连接，`EXECUTE` 找不到句柄）；没有动业务代码。

### 三、真实 MySQL 8.0.46 实测

新建空库 `weichuan_mig43`，设 `TEST_MYSQL_DSN` 后：

- 全量 43 个迁移应用成功；`home_layouts.template_id` 列数 = 0；`schema_migrations`
  含 `0043_drop_template_id.sql`，总数 43。
- 精确模拟两种线上应急状态，均通过：
  - **列还在 + 0043 未记账**（用户线上当前态）→ 重放后列数 0、记账 1；
  - **列已删 + 0043 未记账**（运维手工删过列）→ 1091 被忽略、记账补上、总记账仍 43。
- GORM 默认 logger 仍会打印被忽略的 `Error 1091 ... Can't DROP`，**这行不代表失败**；
  判据是 `[MIGRATE]` 有没有 FATAL、版本有没有补记。已写进 runbook。

### 四、反向验证（改回旧实现必红，还原后零漂移）

| 注入的坏改动 | 结果 |
| --- | --- |
| 0043 改回 `DROP COLUMN IF EXISTS` | `TestMigrationsUseMySQL8DDL` 红两条；真实 MySQL parity 精确复现 `1064 near 'IF EXISTS template_id'` |
| `ignoredErrs` 去掉 `"Can't DROP"` | `TestIsIgnoredToleratesMySQLMissingDropTarget` 红；真实 MySQL 幂等用例红；应急场景 A 的 parity 红 |
| 0043 改成 `SELECT 1;`（假修复） | `TestMigrationsUseMySQL8DDL` 红「必须真的 DROP COLUMN」 |

三次还原后复绿，`0043` 与 `apply.go` 的 sha256 与修复态备份逐字一致。

### 五、门禁

串行跑齐：`gofmt`（改动文件 0 处未格式化）、`golangci-lint` v2.15.0 `0 issues`、
`go test ./...`（带 `TEST_MYSQL_DSN`，28 个包全绿，含 `TestAPIFlowMySQL` /
`TestMySQLMigrations` / 新增 MySQL 幂等用例）、`gen_theme_previews.py --check`、
`eslint .`、`vue-tsc --noEmit`（4G 堆）、全量 Vitest **96 文件 / 1627 例**、
`vite build --outDir dist-check-mig43`、nginx-check **113/113**、`git diff --check`。
本轮前端零改动，仍按纪律跑齐。

### 六、最关键的运维结论

迁移 SQL 是**运行期从磁盘 `migrations/` 读的**，二进制里只有文件名清单。因此用户线上
当前态（列还在 + 0043 未记账）**只需替换 `migrations/0043_drop_template_id.sql`
这一个文件 + 跑 `./server-linux-amd64 migrate`**，旧二进制也能成功，不必重新编译、
不必重启服务。只有「列已被手工删但没记账」才需要含 1091 忽略的新二进制（或确认列
已删后手工补一行记账）。完整判定表与命令见 runbook §4。

**用户侧待办**（沿用并新增 ④）：① `git push origin main`；② 线上 `thumbbackfill -force`
回填 + CF Purge 前缀 `weichuan.ziyuanxingqiu.cn/uploads/`，按原始 URL 验 MISS→HIT；
③ P1 第 5 条 CF Cache Rule（runbook §5.2.7）；④ **按 runbook §4 替换线上 0043 `.sql`
并跑 `migrate`，确认启动日志无 `[MIGRATE][FATAL]`**；⑤ 确认 `server` 主程序与
`thumbbackfill` 同 revision 且含 `quantizePNG`；⑥ P4「接入路径」仍开放。

> ✅ **订正（2026-11-13 10:41）**：上面待办 ③「P1 第 5 条 CF Cache Rule」用户
> **已经建好，且线上验收通过**，不需要再建。CF 规则列表 5 条生效，第 5 条
> `公开接口短缓存` 状态「活动」。不靠截图猜 TTL，直接做行为验收：五条接口全部
> `200` + `public, max-age=60, s-maxage=60` + `cf-cache-status: MISS`；同一 URL
> 连打三次 `MISS → HIT(age:1) → HIT(age:3)`；错误分支 `BYPASS` 且无头；
> `/api/admin/login` 为 `DYNAMIC`；`&v=a` / `&v=b` 各自 `MISS`（查询串不折叠）。
> `MISS` 不等于 `DYNAMIC`：前者是有资格没命中，后者是根本没资格。
> 详见 runbook §5.2.7「线上实测结论」。本轮剩余待办只剩 ① / ② / ④ / ⑤ / ⑥。

> ✅ **订正（2026-11-13 10:58）**：待办 ④（0043 线上应急）**也已闭环**。用户替换
> 线上 `migrations/0043_drop_template_id.sql` 后**不带 `migrate` 参数直接重启**，
> 10:40:26 启动日志为 `database driver=mysql` →
> `[MIGRATE] 已应用 1 个迁移：0043_drop_template_id.sql` →
> `listen :8080 (storage driver=s3)`，无 FATAL。不带参数启动时 MySQL 会自动应用
> 待执行迁移（`AUTO_MIGRATE≠0`），与 `server migrate` 同一条代码路径，效果相同；
> 该日志只有 `applyFile` + `recordVersion` 都成功才会打印，等于同时证明 DDL 成功
> 与版本已记账。剩余待办仅 ① push、② CF Purge `/uploads/` 验收、
> ⑤ 确认 `server` 与 `thumbbackfill` 同 revision、⑥ P4 接入路径。

> ✅ **订正（2026-11-13 11:08）**：待办 ②「CF Purge `/uploads/` 验收」**也已闭环**。
> 用户清除后从公网打不带查询串的原始 URL 实测：5 张 `_thumb.png` 首次全部
> `MISS`（旧副本已清）、响应体 `color_type=3 / bit_depth=8`（PNG8）、
> `cache-control: public, max-age=31536000, immutable`；原图仍是 `color_type=6`。
> 三组体积 `cat-1 42,686→12,917`（−69.7%）、`svc-1 140,441→26,923`（−80.8%）、
> `logo 76,966→12,426`（−83.9%）。另订正判据：**「第二次必 HIT」不绝对**——
> CF 任播下 `cf-ray` 在 `PDX`/`HKG`/`SEA` 间跳，换 PoP 会再现一次 MISS，
> 应连打 3~5 次看陆续 HIT 且 `age` 递增。详见 runbook §4.5.1「线上实测结论」。
> 剩余待办仅 ① push、⑤ server revision 确认、⑥ P4。

---

## 第五十二轮：多租户自有存储的方向决策与详细设计稿（2026-11-13）

**这一轮只出设计，没有改任何生产代码 / 迁移 / 线上 nginx。**

用户先提出另一种更灵活的方案：后台支持配置多个文件存储与 CDN 平台，按样式文件 /
脚本文件 / 图片文件甚至图片子集选择平台并同步，以提升小程序与后台性能。评估结论：

- 这个想法把「性能优化」和「多租户数据主权」混在了一起。决定国内访问速度的是
  边缘节点距离，不是域名个数；多域名反而拆散 HTTP/2 复用、增加 DNS/TLS、
  摊薄各平台缓存命中率。
- 小程序 `.js/.wxml/.wxss/.json` 必须随包、不允许远程加载 JS/样式；管理后台
  `/assets/*.js|css` 与 `/theme-previews/` 是 Vite/nginx 本地产物，
  **都不在 `storage.Uploader` 抽象内**，改存储平台对它们无效。
- 现有 `Uploader` 已有 `local/minio/oss/s3` 单实例与五家 CDN refresher，
  缺的不是平台数量，是「按租户选实例」和「库里不再写死完整 URL」。

**用户明确选择方向 = 多租户各自用自己的存储（数据主权 / 产品能力）**，
判据从首屏毫秒数换成可移植性、租户隔离、凭据安全、迁移可逆。

**用户明确同意架构分叉 = 平台域名统一入口 + 按租户回源，客户自有域名留作未来可选开关**。
理由：微信三类合法域名当前都只登记 `https://weichuan.ziyuanxingqiu.cn`，
端上域名不变 ⇒ 小程序零改动；路径带 `t/<cid>/` ⇒ 缓存键天然按租户隔离。

### 交付物

`docs/tenant-storage-design.md`：完整设计稿。含现状盘点（逐条带文件 / 行号证据）、
`Ref`/`Resolve` 去 URL 化、`0044 tenant_storage_configs` 表草案、`UploaderResolver`、
统一入口反代方案、四个归属坑、凭据加密策略、P0–P4 分期与验收清单、R1–R8 风险登记。

### 分期（与第四十八轮「后续排序」里的 P4 接入路径不是同一件事）

| 期 | 内容 |
| --- | --- |
| P0 | 资源引用去 URL 化（顺手解决 `upload_base_url`「只影响新上传」老坑） |
| P1 | `tenant_storage_configs` 表 + 配置界面（凭据加密 / 掩码 / 连通性自检 / 审计） |
| P2 | `UploaderResolver` 按租户选实例 + 对象键加 `t/<cid>/` + 去掉租户级静默降级 + 接入层反代试点 |
| P3 | 存量搬迁命令（dry-run / force / rollback，保留 `old_url`） |
| P4 | 密钥轮换、审计前端标签、配额口径、数据保留 / 解约清理策略 |

### 最需要盯住的四条

1. `newUploaderWithFallback`（`cmd/server/main.go:528`）的静默降级**只能服务平台对象**；
   租户解析失败必须显式报错。否则「客户以为文件在自己桶里，实际在平台本地盘」，
   是没人报错时用户无从察觉的典型静默失败。
2. 平台级资源（素材采用 `library/`、小程序码 `wxacode/`、下架占位图、送检回写）
   固定 `scope=platform`；`takedownMediaCheck` 现在不带租户，多租户下必须按
   `row.CompanyID` 选实例，否则占位图会写进别人的桶。
3. 匿名上传 `uploadCompanyID`（`storage_quota.go:163`）不校验 `companyId` 对应的企业
   是否存在，接 resolver 前必须补，不能让伪造值写进任意租户桶。
4. **R1（唯一未验证假设）**：`deploy/baota/nginx-site.live.conf` 的 `/uploads/`
   现在是本地静态 + `immutable` 一年，改成按租户前缀反代要动线上接入层。
   第一阶段按「单租户 + 公开读桶」试点，先在测试环境 `nginx -t` + 行为验收跑通；
   不通则退回「客户自有域名开关」重新评审，**不硬上**。

> 设计稿末尾留了一个需要用户拍板的决策点（配置由谁填写 / 第一阶段试点口径），
> 确认后才进入 P0。本轮未跑全量门禁（无代码改动）。

### 第五十二轮补记：权限模型拍板 + P0 拆步（2026-11-13 14:04）

> ✅ **订正上面那句**：「配置由谁填写」已由用户明确拍板——
> **平台超管代填，企业管理员不给这条权限**（用户原话：「这种权限肯定不能放给企业管理员的」）。
> 设计稿 §1.1 已把落地口径钉死：后端 `PlatformAdminMiddleware`
> （`backend/internal/middleware/auth.go:83`，`role=admin && company_id=0`，越权 `code=403`）、
> 前端 `meta.platformOnly`（`frontend/src/router/index.ts:196` 守卫 +
> `frontend/src/layout/AdminLayout.vue:394` 菜单过滤）、凭据只写不读沿用
> `CompanyWXApp`（`backend/internal/handler/company_wx.go:31` 是同口径样板）。
> 并新增 R9 登记「照抄 `RegisterStorage` 的 `AuthMiddleware(db, "admin")` 会把企业管理员
> 放进配置接口」这个风险：P1 验收加了企业管理员 403 集成测试与路由中间件源码级守门。
> **前端隐藏不是权限，后端必须独立拦截**，沿用项目既有双保险口径。
>
> 「第一阶段试点口径」按设计稿 §3.4 的默认值（单租户 + 公开读桶）执行，进入 P2 前复核，
> 不阻塞 P0。

**P0 拆两步**（按「零行为变更、可逆」原则）：

| 步 | 交付 | 明确不做的事 |
| --- | --- | --- |
| P0a | `backend/internal/storage/ref.go` + 单测：`Scope` / `Ref`、`ParseRef`、`NormalizeRef`、`ResolveRef`，兼容绝对 URL / `/uploads/` 相对 / 裸 objectKey 三种历史形态 | 不接任何 handler、不改库字段、不改出参、不写迁移、不动 nginx —— 线上行为零变化，随时可整体删除回滚 |
| P0b | 扫出所有把上传 URL 写库的业务边界（写入侧 + 出参侧）清单，并立源码级守门 | 仍不改生产行为；清单用于防止将来接入时漏点，尤其不能漏 P1 刚加了 60 秒边缘缓存的五条公开只读接口 |

本补记与随后 P0a / P0b 的工作仍**只改文档与纯函数 / 守门单测**，
不改任何生产代码路径、不写迁移、不动线上 nginx。

### 第五十二轮补记二：P0a / P0b 落地（2026-11-13）

**交付物**（全部零行为变更、可逆）：

| 文件 | 内容 |
| --- | --- |
| `backend/internal/storage/ref.go` | 纯函数地基：`Scope` / `Ref`、`ObjectKeyFor`、`ParseRef`、`IsNormalizedRef`、`NormalizeRef`、`ResolveRef`。只依赖标准库，键布局 `p/` 与 `t/<cid>/` |
| `backend/internal/storage/ref_test.go` | 16 组行为守门：三形态兼容、幂等、跨租户改绑拒绝、路径穿越、`_thumb` 派生契约、空值无错透传 |
| `backend/internal/storage/ref_p0a_guard_test.go` | 源码级守门：P0a 阶段生产包**不许调用** ref API；`ref.go` 不许有 init / 外部依赖 |
| `backend/internal/storage/ref_boundaries_test.go` | P0b 五组边界守门：写入侧文件集合、公开 GET 路由集合、资源 json 字段集合、小程序 `assetUrl` 消费文件集合、清单锚点存在性 |
| `docs/tenant-storage-url-boundaries.md` | P0b 业务 URL 边界清单（写入侧 W1–W9、库字段、出参侧 R1–R15、端上 C1–C3、守门说明） |

**本轮最重要的发现**：P1 加的 60 秒边缘缓存只覆盖 **5 条**公开只读接口
（`handler/handler.go` 里 `cdnCache(c, 60)` 恰好 5 处），但下发资源 URL 的公开 GET 有
**9 条**。另 4 条 `/api/company`（logo）、`/api/team`（avatar，`:206` 刻意不设缓存）、
`/api/projects/:id`（cover + album 数组）、`/api/services/:id`（内嵌整个 `model.BizService`）
同样要过 `ResolveRef`。设计稿 §3.1 原来只写「必须覆盖五条缓存接口」**不够**，
已补订正：正确口径是所有出参序列化点都要过，缓存只是其中一层的时序约束。

**两个隐藏边界**：`ProjectCase.Album`（`models.go:198`）是 `json:"-"` 的 URL 数组列，
靠 `handler/content.go:745` 的 `projectToMap` 与 `handler.go:275-284` 展开，
按「扫 json tag 找边界」必然漏；`MediaCheck.ObjectKey`（`media_check.go:31`）是
从 URL 反推的裸键，P0 之后 `mediachk.ObjectKey` 那段脆弱逻辑应退休。
另有 4 个名字带 URL 语义但**不是**资源的字段（`videoFallbackUrl` bool / `bgMask` int /
`bgId` 标识 / `files` 计数），守门逐条登记为白名单，避免误接。

**出参侧不该逐点改 handler**：R11–R15 显示后台接口大多是模型直接序列化
（`content.go` 的 `"data": ci` / `cp` / `list` / `projectToMap(p)`），逐个 `gin.H` 补
`ResolveRef` 一定会漏，且每新增一个后台接口就多一处漏点。真正接线时应在统一序列化
出口做一次，再按清单逐点验收。**这个决策留到 P0b 之后的实现轮拍板，本轮只把边界钉死。**

**反向验证（必做项）**：

- P0a 两组：① 改坏 `ResolveRef` 的绝对 URL 原样返回分支 →
  `TestResolveRefKeepsLegacyAbsoluteURL` 必红；② 在 `handler` 包插一行真实
  `storage.ResolveRef("", "")` 调用 → `TestRefWiringGate`（原 `TestP0aRefIsNotWiredIntoProduction`，
  第五十三轮升级为三集合对账守门）必红。
- P0b 三组：同时注入「新增上传写入点」「新增公开 GET 路由」「新增资源 json 字段」→
  `TestP0bUploaderWriteSitesMatchBoundaryList` /
  `TestP0bPublicGetRoutesMatchBoundaryList` /
  `TestP0bResourceJSONFieldsMatchBoundaryList` 三条必红。
- 还原后 8 个文件 `sha256` 零漂移、`go test ./internal/storage -count=1` 全绿、
  临时探针文件全部清理（`git status` 无残留）。

**边界**：只有新增的 4 个 Go 文件 + 3 份文档；`handler.go` 仅在反向验证期间被临时
插过一行并已还原（哈希校验通过）。没有改任何生产代码路径、没有写迁移、
没有动 `deploy/baota/nginx-site.live.conf`、没有回填存量数据、没有跑 `./server migrate`。

---

## 第五十三轮：出参侧实际接入（2026-11-13）

> 承接第五十二轮补记二「边界已钉死、只待实现轮拍板」。用户在确认「出参侧应在统一出口做」
> 之后给 go-ahead「现在就进入真正接线」。**接线前发现一个关键事实推翻了边界清单初版的
> 接入顺序**，必须先订正再动手。

### 推翻旧顺序：必须先出参侧、再写入侧

边界清单 §1 初版写的是「先 W1–W9 全部收敛成 `NormalizeRef` 裸键，再动出参侧」。
实读线上配置后发现这是**错的**：

- 线上是**双轨**：存量老图走本地盘 `/uploads/`（nginx 反代）、新上传走对象存储绝对 URL
  （`MinioUploader.Save` 返回 `<BaseURL>/<key>`）；
- 若先写键，会有一段窗口期——库里同时存在裸键与旧绝对 URL，而出参侧还没兼容完 →
  **页面直接裂图**，且全 200 无报错（又是「没人报错时用户无从察觉」）。

正确顺序：先接出参侧，用 `ResolveRef` 兼容三形态（云绝对 URL 原样 / 本地 `/uploads/` 原样 /
将来裸键自动补 `/uploads/` 前缀），对**现行数据零行为变更**，因此先接出参侧是安全的；
写入侧（W1–W9 / `NormalizeRef`）留到下一阶段。边界清单 §1、§3.1、§5 已同步订正。

### 出参侧实际改动（生产代码路径）

| 文件 | 改动 |
| --- | --- |
| `backend/internal/model/refs.go`（新增） | `ResolveResourceRef(raw)`（空值空串、解析失败返回原值不降级、经 `storage.ResolveRef(raw, "")`）；六个模型 `ResolveRefs()`：`Employee.Avatar` / `CompanyInfo.Logo` / `CompanyProfile.Cover,VideoURL,VideoBackupURL` / `AchievementCategory.Icon` / `ProjectCase.Cover` / `BizService.Icon`。字段集合严格对齐边界清单 §2 |
| `backend/internal/model/refs_test.go`（新增） | 模型解析单测：现行数据零行为变更、未来键补前缀、不安全引用返回原值、六模型 `ResolveRefs` |
| `backend/internal/handler/handler.go` | 9 条公开 GET 接入：整模型路由（`/api/card`、`/api/achievements`、`/api/projects`、`/api/services`、`/api/services/:id`）在 `cdnCache(c, 60)` **之前**调 `ResolveRefs()`；内联 `gin.H` 路由（`/api/company`、`/api/team`、`/api/projects/:id`、`/api/home-layout`）直接调 `model.ResolveResourceRef(...)` |
| `backend/internal/handler/storage.go` | `signHandler` 修正：用 `storage.ObjectKeyFromURL` 复用 `legacyRel`（先判 `/uploads/` 再判基址），修复 `UploadBaseURL` 为空时把 `/uploads/x.jpg` 错截成 `uploads/x.jpg` 的旧 bug |
| `backend/internal/storage/ref.go` | 新增 `ObjectKeyFromURL(raw, baseURL)`（写入侧反推 key 用，与旧 signHandler 语义一致但修空 base bug） |

`model` 反向依赖 `storage` 合法：`storage` 包不 import `model`（已核实）。

### 守门升级（出参侧接线后由它兜底）

`ref_p0a_guard_test.go` 的 `TestRefWiringGate`（原 `TestP0aRefIsNotWiredIntoProduction`）升级为
**三集合对账**：

1. 写入侧 API（`NormalizeRef` / `ObjectKeyFor` / `ParseRef` / `IsNormalizedRef`）一律不得出 `storage` 包；
2. `storage.ResolveRef` 只允许在 `backend/internal/model/refs.go`；
3. `model.ResolveResourceRef` / `ResolveRefs()` 只允许在 sanctioned 出参侧文件
   （`model/refs.go`、`model/refs_test.go`、`handler/handler.go`、`handler/storage.go`、
   `handler/ref_wiring_test.go`）。

新增 `ref_objectkey_test.go`（`TestObjectKeyFromURL` 覆盖空 base 修复）、
`ref_wiring_test.go`（`TestSignHandlerEmptyBaseFix` 用 `stubSigner` 钉空 base 场景）、
`model/refs_test.go`。

### 行为保证与反向验证

- **零行为变更**：对现行生产数据（云绝对 URL 原样、本地 `/uploads/` 原样、空值空串、
  不安全引用返回原值）哈希零漂移；
- 反向验证注入 `storage.NormalizeRef` **真实调用** → 守门正确报红，定位到
  `handler/storage.go`（`../handler/storage.go:122`）；还原后三个文件 `sha256` 零漂移、全包复绿
  （早期用 `_, _ = storage.NormalizeRef("x", "", storage.ScopeTenant, 1)` 值引用未触发，
  改真实调用才命中——守门正则只匹配真实调用）。

### 门禁

串行全绿：`gofmt`（改动文件 0 处）、`golangci-lint v2.15.0` = 0 issues、
`GOTELEMETRY=off go test ./... -count=1` 全包 EXIT=0。本轮前端零改动，未跑
`vue-tsc` / 全量 Vitest / eslint / `vite build`（`bash` 里 `$VAR` 路径含中文用 `${VAR}`；
SQL 运行期读盘、改动 `.sql` 只需换文件 + `./server migrate` 不必换二进制）。

### 边界

- **已做**：出参侧（9 条 GET + signHandler）接入、守门升级、反向验证、文档同步；
- **未做**（下一阶段）：写入侧 W1–W9 / `NormalizeRef` 收敛成裸键、存量回填、迁移 0044、
  动线上 nginx、碰线上二进制。

### 待用户侧动作

`git push origin main`（沙箱拦截 push，本地 commit 已完成）；写入侧接线待拍板后实施。

---

## 第五十四轮：写入侧接线（2026-11-13）

> 承接第五十三轮「出参侧已接、写入侧待拍板」。用户确认生产为 **local 驱动**（`STORAGE_DRIVER`
> 默认值，仓库无覆盖，上一轮实读到线上 nginx 把 `/uploads/` 反代本地盘），据此推进写入侧。

### 关键取舍：scoped 对象键 + 存驱动 URL（本地驱动安全版）

写入侧真正要做的是「让对象落到带归属前缀的键」。本轮只做到这一步，**没**把库里的值改成严格裸键：

- **对象键带前缀**：W1 上传落 `t/<cid>/...`（租户）或 `p/...`（平台超管 cid=0）；
  W2 素材落 `p/library/...`；W3 小程序码落 `p/wxacode/...`；W5 预签名签带前缀键。
- **库里仍写驱动返回的 URL**：本地驱动即相对 `/uploads/<key>`，与第五十三轮前的
  `/uploads/<name>` 同形态 ⇒ 前端、出参侧、端上契约**零改动**，`ResolveRef` 对 `/uploads/` 原样保留。
- **为什么不做严格裸键**：裸键（只写 `t/<cid>/x`）要靠 `ResolveRef` 还原成 `<origin>/uploads/...`，
  云驱动下必须先把 nginx `/uploads/` 反代到对象桶（设计稿 R1，未验证），否则新上传 404。
  本地驱动无此问题，所以严格裸键 + R1 留到云驱动阶段。

### 接口变更（`storage.Uploader`）

`Save(fh *multipart.FileHeader)` → `Save(fh *multipart.FileHeader, key string)`。三实现
（Local/MinIO/OSS）改为按给定 `key` 落盘/落桶，不再自己 `RandName`；`Save` 内新增
`validateObjectKey`（拒绝空值 / `..` 穿越 / 绝对路径 / 反斜杠，挡住 `filepath.Join(Dir,"../x")`）。

### 落点改动

| 落点 | 改动 |
| --- | --- |
| W1 `handler/upload.go` | 循环内 `ObjectKeyFor(scope, cid, RandName(ext))` → `up.Save(fh, objKey)` |
| W2 `handler/assets.go` | rel 经 `ObjectKeyFor(ScopePlatform,0,rel)` 落 `p/library/...` |
| W3 `handler/wechat.go` | key 经 `ObjectKeyFor(ScopePlatform,0,rel)` 落 `p/wxacode/...` |
| W5 `handler/storage.go` | 预签名 key 经 `ObjectKeyFor(scope,cid,name)` |
| W4/W6 `handler/media_check.go` | **无需改**：`mediachk.Plan` 已把 URL 反推成裸键 `d.Key`；单桶下复用 `row.ObjectKey` |
| W7–W9 前端 | **无需改**：后端仍返回驱动 URL（本地 `/uploads/<key>`） |

### 守门 + 反向验证

- `TestRefWiringGate`：sanctioned 新增 `upload.go`/`assets.go`/`wechat.go`；**修正 `writeAPI` 分支
  漏掉的 `&& !sanctioned[p]`**（此前无条件封禁写入侧 API，接线后必须放行 sanctioned 文件）。
- 边界 token：W1/W2 标识符更新 + 新增 4 条 `storage.ObjectKeyFor(...)` 锚点。
- 反向验证：临时在非 sanctioned 文件 `handler/zzz_ref_probe_test.go` 调用 `storage.ObjectKeyFor`
  → `TestRefWiringGate` 报红定位到该行；删除后复绿。
- 新增 `storage/save_key_test.go`：锁「对象落带前缀键下（`t/5/x.jpg` / `p/library/x.jpg` + 同前缀 thumb）」
  与「非法键拒写（`../x`、`/abs`、`t\5\x`、空值）」。

### 门禁

`gofmt` 干净、`go vet ./...` 通过、`go test ./...` 全包绿（handler 58s、storage 9s）。
`golangci-lint` 本沙箱无二进制（上一轮已 lint 绿，本轮只增 Go 源码未重跑）。
前端零改动 → 未跑 vue-tsc / Vitest / eslint / vite build。

### 边界

- **已做**：W1/W2/W3/W5 落带前缀键、`Uploader.Save` 接口变更、守门升级、反向验证、文档同步；
- **未做**（云驱动阶段）：严格裸键（只写 `t/<cid>/x`）、nginx R1（`/uploads/`→桶反代）、
  `NormalizeRef` 收敛、存量回填、迁移 0044、多桶切换。

### 待用户侧动作

`git push origin main`（沙箱拦截 push，本地 commit 待做）。

---

## 第五十五轮：R1 可行性验证（2026-11-13，只加验证装置）

> 承接第五十四轮末尾「严格裸键 + nginx R1 属云驱动阶段」。用户选定的下一步是
> **先做 R1 可行性验证**：只写方案与验证脚本，不改生产 nginx，确认 R1 成立后再动
> `ResolveRef` 与库中数据形态。

### 为什么先验 R1 而不是先切裸键

顺序反过来会有一个**没有任何报错**的失败窗口：库里已经写成 `t/<cid>/x`，而 nginx 还没有
对应的 location → 请求全部落回平台 Go → 文件不在平台盘 → 404，页面全裂。
平台侧日志干净（求到了 Go，Go 只是找不到文件），没有人会想到是接入层没跟上。
所以先证明「接入层这条路通」，再改数据形态。

### 本轮最重要的发现：设计稿那句是过时的

设计稿 §3.4 写「`/uploads/` 现在是本地静态托管」。**实读 `nginx-site.live.conf:447-459`，
它一直是 `proxy_pass http://127.0.0.1:8080`**（Go 侧 `r.Static("/uploads", cfg.UploadDir)` 出盘）。

这条订正改变了 R1 的性质：不是「静态托管改反代」（动地基），而是「在已有反代上按前缀多分几路」。
风险面小于设计稿假设，但因此冒出四个设计稿没写到的新坑（下面四条都是实测的）。

### 装置：`tools/nginx-r1-check/`

`bash run.sh` 一键跑：生成桩对象 → 生成产品形态 snippet → 起 `nginx:alpine` → 断言 → 关容器。

桩上游用 nginx 自己扮演桶（静态文件天然支持 Range、`ETag`、`Accept-Ranges`），
并打 `X-Amz-Request-Id` 区分是哪家桶——**「有没有串桶」靠响应头断言，不靠看日志**。
另有一个 TLS 桩桶（SAN=`bucket-a.test`），在 `$ssl_server_name` 为空时返回 421，
复刻真桶的 Host/SNI 校验。

**实测结果：36 项行为断言全绿 + 1 项失败预期成立。**

| 组 | 验的是什么 | 结论 |
| --- | --- | --- |
| A | 路径映射与租户隔离（`/uploads/t/5/x.jpg` → 桶里 `t/5/x.jpg`） | ✅ 且两桶互不串 |
| B | 前缀边界与回退（`t/50/` 不被 `t/5/` 吞；未配置租户回落平台盘） | ✅「关配置=回默认」成立 |
| C | 缓存头（不与桶的头叠加、404/5xx→`no-store`、安全头继承、`Accept-Ranges`） | ✅ 必须显式 `proxy_hide_header` |
| D | Range/206、HEAD 的 `Content-Length`、ETag、304 | ✅ 视频拖动可用 |
| E | 对照探针（Option A 写法、不 hide、不带 SNI、带 SNI） | ✅ 证明三条指令是「必需」不是「可选优化」 |
| F | 配置表 ↔ snippet 对账（少一段必报红，且点明是哪家租户） | ✅ |
| G | 桶域名解析不出来时 `nginx -t` 的行为 | ⚠️ 直接 emerg，整份配置更新被卡住 |

### 四个新坑（都写进了断言）

1. **location 尾斜杠是隔离边界**：`location ^~ /uploads/t/5/` 去掉尾斜杠就会吞掉 `/uploads/t/50/`，
   打到错误的桶。反向验证 3 复现了后果（B1 报红）。
2. **桶自带 `Cache-Control` 会与 `add_header` 叠加成两条**：实测不写 `proxy_hide_header` 时
   响应里 `max-age=3600` 与 `immutable` 并存。RFC 9111 不规定 CDN 取哪条 ⇒ 同一对象在不同
   边缘可能有两种缓存时长，且没有任何报错。也不能靠「把桶配成不带 Cache-Control」解决——
   那是桶侧行为，换厂商/有人点了控制台就变。
3. **桶域名在 reload 时解析**：解析失败 `nginx -t` emerg → 宝塔不 reload → **整份配置的更新
   全部被卡住**，连带本该生效的其它改动。现象是「保存了但没生效」，没有一条报错指向打错的域名。
   ⇒ snippet 生成器必须先做 DNS 预检，不能把这个发现权交给 nginx。
4. **HTTPS 桶缺 `proxy_ssl_server_name` 会被拒**：`proxy_ssl_verify` + 可信 CA + `proxy_ssl_name`
   全齐也不行（实测 421），必须显式 `proxy_ssl_server_name on`。

### 一条阻断项 R1-b（有条件成立的原因）

`takedownMediaCheck`（`media_check.go:460`）是**覆盖写同一对象键** + `refreshCDN(row.URL)`，
而 `/uploads/` 除 `wxacode/` 外全是 `immutable` 一年 ⇒ **违规下架能否生效完全依赖 Purge**，
且 `refreshCDN` 的设计是「失败只记日志不报错」，这条链上没有任何一处会拦它。

现状本来就如此（2026-10-23 补接线后才真正触发刷新），R1 不改恶化现状，只是把它带进多租户语境：
Purge 用的是**平台**那把 refresher。第一阶段试点（公开读桶 + `public_base_url` 留空，一律走
平台统一入口）下，桶前面的边缘就是平台这把 CF，Purge 按 URL 打得到 → **成立**。
但一旦开「客户自有域名 / 客户自有 CDN」，平台 Purge 打不到客户边缘 ⇒
**违规图会在客户 CDN 上留一年且平台无从察觉**。

> 所以结论写成「**有条件成立**」：第一阶段口径不变则 R1 成立；
> 开客户自有域名之前必须先回答「谁负责 Purge 租户自有边缘」。

### 边界

- **已做**：`tools/nginx-r1-check/`（装置）+ `docs/tenant-storage-r1-feasibility.md`（结论）+
  设计稿 §3.4 / §6-R1 订正；三组反向验证（4 / 5 / 3 项报红，还原后 36 项复绿、`gen_site.py` 字节零漂移）；
- **未做**：生产代码零改动；未改 `deploy/baota/nginx-site.live.conf`、未 reload 线上 nginx、
  未写迁移、未改库、未碰线上二进制；未跑全量门禁（没有 Go/前端代码改动）；
- 装置产物（`conf.d/`、`cert/`、`logs/`、`www/`）全部 gitignore，不进仓库。

### 下一步（顺序不可反）

1. 落 snippet 生成器（含 DNS 预检 + 配置表↔snippet 对账）；
2. 切出参侧：`ResolveRef` 对裸键补 `<origin>/uploads/` 前缀，两种历史形态保持原样；
3. 切写入侧：`NormalizeRef` 收敛成裸键，**开关必须能一个 env 回滚**；
4. 之后才是存量回填（P3）与迁移 0044。

---

## 第五十六轮：snippet 生成器落地（2026-11-13，纯新增，不接线）

> 承接第五十五轮结论「R1 有条件成立」与它给的下一步 ①：落 snippet 生成器
> （DNS 预检 + 配置表↔snippet 对账）。本轮只新增纯函数与 CLI，**不接库、不改路由、
> 不动线上 nginx**。

### 为什么生成器是后端代码，而不是运维脚本

第一版是 `tools/nginx-r1-check/gen_site.py`（第五十五轮的验证装置里）。本轮把它删了，
换成 `backend/internal/nginxsnippet` + `backend/cmd/nginx-snippets`。三个理由：

1. **它最终要读 `tenant_storage_configs`、要被配置接口调用**，属于后端而不是部署目录；
2. snippet 里每一行都对应一个静默失败的坑，必须能用单测钉住——脚本里那些断言只能靠
   跑一遍容器才发现，而容器验证不会在 `go test ./...` 时执行；
3. **两个实现必然漂移**，而漂移的表现正是本轮要防的那类故障：「本地全绿、线上裂图」。
   现在装置直接跑将要上线的那个生成器，断言绿才说明它是对的。

### 三条不变量（写进包注释 + 单测）

| # | 规则 | 改坏的后果 |
| --- | --- | --- |
| 1 | `location ^~ /uploads/t/<cid>/` **必须带尾斜杠** | `/uploads/t/50/...` 被 `t/5` 吃掉，用户的图从别人的桶里取，页面照常 200 |
| 2 | `proxy_pass` **不带 URI 部分**，靠 `rewrite ^/uploads/(.*)$ /$1 break;` 剥前缀 | 键布局在 Go 与 nginx 双写，改一处漏一处 ⇒ 那家租户全站裂图 |
| 3 | `proxy_hide_header Cache-Control/Expires` 必须在 `add_header` **之前** | 桶的头与站点头叠成两条，CDN 取哪条不确定，无报错 |

另有两条同源约束：`NeedsSnippet()` 同时判「启用」与「驱动不是 local」——
只判启用，把驱动改回 local 时会留下一条指向不存在桶的 location，那家租户所有图 404
而后台显示配置正常；重复 `company_id` 直接拒绝，否则 nginx 只会报 `duplicate location` 的行号，
指不到是哪家配了两遍。

### 只产出 `location` 段（不含 map、不含 server）

`map` 是 http 级指令，塞进 `server{}` 里 `nginx -t` 直接报 `directive is not allowed here`。
生产上生成物要被 include 进 `nginx-site.live.conf` 的 `server{}`，所以：

```nginx
# 线上只需加一行（放在 server{} 里）
include /www/server/panel/vhost/nginx/tenant-storage/*.conf;
```

`$uploads_cache` 那个 map 已在 live.conf 的 http 级存在，snippet 里不能再定义一次。
验证装置也跟着改成同样的结构（手写 `site.conf` 壳 + `include snippets/*.conf`），
于是**连 include 机制本身一起验了**。

### DNS 预检：必须在落盘之前

`--dns-precheck` 解析每个桶域名，失败就**拒绝生成、不留半成品文件**，并报出是哪家租户、哪个域名。
不这么做的话：`proxy_pass` 里的主机名在 reload 时解析，解不出来 `nginx -t` emerg，
宝塔 test 失败就不 reload ⇒ **整份配置的更新全被卡住**，现象是「保存了但没生效」。

装置里这两条防线分开验：
- **G1** 让 nginx 自己去解析（`nginx-dns-probe.conf`）→ 确认它确实 emerg，说明前置检查不是多余的；
- **G2** 生成器面对同一个坏域名 → 确认它拒绝落盘且点明 `company_id=99`，产物文件不存在。

### 对账：两个方向都是故障

`Missing`：配了桶却没有 snippet → 请求落回平台盘 → 404、页面裂；
`Extra`：有 snippet 但配置表已停用 → 白开一个口子，把流量送进没人维护的桶。
装置 F 组改成双向反向验证（删一段 / 多一段），都要求报红且点明租户。

### 门禁

- `gofmt -l` 新包干净；`go vet ./...` 通过；**`go test ./...` 30 个包全绿**；
- 新包单测 14 个（渲染不变量、SSL/SNI、local 与停用跳过、空配置、7 类非法输入、
  重复 cid、双向对账、自产自消对账、DNS 四例、hostOnly 七例）；
- 装置：**39 项断言全绿**（原 36 + F5/F6/F7）；
- 反向验证三组（改在 Go 生成器里）：删 `proxy_hide_header` → C1/C2/C6/D8 红；
  rewrite 剥错前缀 → A1/A2/A5/C2/C5 红；location 去尾斜杠 → B1 + F1/F4/F7 红；
  还原后全绿、`snippet.go` 字节零漂移。

**已知既有状态（本轮未动）**：`gofmt -l .` 全树会列出 12 个既有文件，全部是结构体字段
对齐差异（例如 `internal/storage/cdn.go`）。这不是本轮引入的，重排会产生上千行噪声 diff，
故不改——真要统一应单独一轮做「格式化基线」，并同步 `golangci-lint` 口径。

### 边界

- **新增**：`backend/internal/nginxsnippet`（2 源 + 2 测）、`backend/cmd/nginx-snippets`、
  装置里的 `site.conf`（手写壳）；
- **删除**：`tools/nginx-r1-check/gen_site.py`（唯一生成器原则）；
- **未做**：接 `tenant_storage_configs`（表属迁移 0044）、把 DNS 预检结果写进后台
  `status`/`last_error`、reload 编排与审计、出参侧认裸键、写入侧收敛。

### 下一步

按第五十五轮定的顺序，第 2 步是**出参侧认裸键**：`storage.ResolveRef` 对 `t/<cid>/...`
补 `<origin>/uploads/` 前缀，`/uploads/` 与绝对 URL 两种历史形态保持原样。
这一步对现行数据零行为变更，可以先做；但真正写入裸键前，生成器必须先接上真实配置表并跑过一遍。

---

## 第五十七轮：后台出参收口 R11–R15（2026-11-13，对现行数据零行为变更）

> 用户「继续下一步」→ 按第五十六轮结尾给的顺序做第 2 步。落地时发现这一步真正缺的不是
> 「`ResolveRef` 认不认裸键」（它从第五十三轮起就认），而是「**后台那五组出口有没有调它**」。

### 缺口：公开页认了，后台没认

第五十三轮只接了**公开** 9 条 GET + `signHandler`，边界清单 §3.2 的 R11–R15 一直挂「未接」。
这是当时刻意的取舍（先做影响面最大的那条），但它留了一个很难被发现的不对称：

> 写入侧一旦收敛成裸键，公开页正常、**后台编辑页裂图**。
> 公开页坏了立刻有人报；后台是运营一个人看到预览破图，第一反应往往是「前端 bug」，
> 而公开页一切正常，排查方向直接被带偏。

所以这一步的验收标准不是「清单打勾」，而是「库里放裸键时，后台出参也是可访问 URL」。

### 实际改动

| 文件 | 改动 |
| --- | --- |
| `model/refs.go` | 新增 `ResolveResourceRefs([]string) []string`（nil / 空原样返回，避免「没有图集」从 `null` 变成 `[]` 的契约变化）；重写注释，覆盖范围明确为「所有出参」 |
| `handler/content.go` | GET company-info / company-profile 就地 `ResolveRefs()`；PUT 两者 `resp := x; resp.ResolveRefs()`；categories / services 列表 `for i := range list`、POST 回显用副本；`projectToMap` 加 `p.ResolveRefs()` + `album = model.ResolveResourceRefs(album)` |
| `handler/apply.go` | `/api/employee/me` 与员工导出各加 `e.ResolveRefs()` |
| `handler/handler.go` | `album` 的 for 循环改调 `model.ResolveResourceRefs`（与 `content.go` 合成一处口径） |
| `ref_p0a_guard_test.go` | sanctioned 新增 `content.go` / `apply.go`；新增 `TestAlbumExpansionSitesResolved` |
| `handler/refs_exit_test.go`（新增） | 三条接线测试（见下） |

### 落地纪律：只解析「本次请求不会再写库」的值

- GET：查询结果是局部变量，就地解析；
- PUT/POST：**先落库，再对副本解析出参**（`resp := x; resp.ResolveRefs()`）。

反过来（先解析再 Save）会把 `/uploads/t/5/x.jpg` 写回库，形态从裸键退回历史形态，
**而且没有任何报错**——`upload_base_url` 影响存量的老问题就是这么复发的。

### 反向验证（逐点，四处）

| 注掉的解析 | 报红位置 | 实测信息 |
| --- | --- | --- |
| `ci.ResolveRefs()`（后台 company-info） | `refs_exit_test.go:33` | `t/1/logo.png` ≠ `/uploads/t/1/logo.png` |
| `p.ResolveRefs()`（projectToMap cover） | `refs_exit_test.go:64` | `t/1/cover.jpg` ≠ `/uploads/t/1/cover.jpg` |
| 仅 `album = model.ResolveResourceRefs(...)` | `refs_exit_test.go:67` | `t/1/a1.jpg` ≠ `/uploads/t/1/a1.jpg` |
| `e.ResolveRefs()`（employee/me） | `refs_exit_test.go:86` | `t/1/av.jpg` ≠ `/uploads/t/1/av.jpg` |

album 单独注一次是必要的：cover 与 album 在同一个出参里，只注 cover 只能证明 cover 那条通。

### 守门新增：album 展开点

`album` 是 `json:"-"` 的 JSON 数组字符串，模型方法解不开，只能在两处展开点
（`handler.go:297`、`content.go:776`）手工解析；而它又不在「扫 json tag」的字段集合里，
`TestP0bResourceJSONFieldsMatchBoundaryList` **看不见它**。
`TestAlbumExpansionSitesResolved` 因此盯两件事：展开点恰好 2 处；这两处必须调 `model.ResolveResourceRefs`。
数量变了就报红——逼人重新确认「新加的展开点接了没有」。

### 门禁

- `gofmt -l` 本轮改动文件干净（仓内另有 5 个既有文件长期在列，全是结构体字段对齐差异，
  非本轮引入，未动）；`go vet ./...` 通过；`go test ./...` 全包绿（handler 50s、storage 6s）；
- 前端零改动 → 未跑 `vue-tsc` / Vitest / eslint / `vite build`；
- `golangci-lint` 本沙箱无二进制（沿用上一轮结论，本轮只增 Go 源码）。

### 边界与下一步

- **已做**：R11–R15 五组后台出口、`ResolveResourceRefs`、album 展开点守门、三条接线测试、
  四处逐点反向验证；
- **R15 判定无需接**：`settings.go:73` 出的是配置项元数据（基址 / 阈值，`setting.go` 内零 logo 类键），
  `wechat_admin.go:91` 出的 `wechatBindingRow` 逐字段看过不含资源字段——依据写在
  边界清单 §3.2，不是「忘了」；
- **未做**：③ 写入侧 `NormalizeRef` 收敛（必须留一个 env 能整体回滚）→ ④ 存量回填 P3 +
  迁移 0044 + 生成器接真实 `tenant_storage_configs`。
  （**订正**：其中「迁移 0044 + 生成器接真实配置表」已由第五十八轮完成，见下一节；
  ③ 与 ④ 仍未做，顺序不可颠倒。）

---

## 第五十八轮：迁移 0044 建配置表 + 生成器接真实配置表（2026-11-13，对现行数据零行为变更）

### 为什么先做这一步

第五十六轮把 snippet 生成器做出来了，但它的输入是**手写 JSON**（`--targets tenants.json`）。
这意味着「哪些租户存自己桶里」这个事实只存在于运维的某个文件里，不在库里——
后台看不到、审计记不到、换个人接手就断。多租户存储要成立，配置必须落表。

本轮先把「表 + 读」做完，**不做写接口**。理由与③的顺序约束有关：写入侧
`NormalizeRef` 收敛（③）必须做到「一个 env 能整体回滚」，在这之前贸然开一个能改
桶地址的写接口，等于同时动「对象键形态」和「回源目标」两件互相放大的事。
表先建好、生成器先能读真配置，③落地后接写接口就是纯加法。

### 做了什么

| 落点 | 内容 |
| --- | --- |
| `backend/migrations/0044_tenant_storage_configs.sql` | 19 列 + `company_id` 唯一索引，`InnoDB` / `utf8mb4`，只建表不写数据 |
| `backend/internal/model/tenant_storage.go` | `TenantStorageConfig`，显式 `TableName()`，两个凭据列 `json:"-"` |
| `backend/internal/model/all.go` | 进 `model.All()`（否则 devserver / `handler_test` 运行期 `no such table`） |
| `backend/internal/migrate/manifest_gen.go` | 重跑 `scripts/gen_migrations_manifest.py`（44 个文件） |
| `backend/internal/repository/storage_config.go` | `ListStorageTargets` + `StorageTarget` 窄视图 + `NginxTarget()` 映射 |
| `backend/cmd/nginx-snippets/main.go` | 新增 `--dsn`（与 `--targets` 互斥且必须给一个）、原子落盘、零 location 覆盖守卫 |
| `backend/internal/handler/handler_test.go` | `TestMySQLMigrations` 表清单加 `tenant_storage_configs` |

唯一比设计稿多出来的列是 **`host_header`**：R1 真桶实测（第五十五轮）表明，只给
`proxy_ssl_verify` + CA + `ssl_name` 而漏 `proxy_ssl_server_name` 会被桶以 **421** 拒绝；
而这个 Host 的取值逐厂商不同（MinIO path-style、OSS/S3 virtual-hosted-style），
推导等于猜，所以给一列让超管照控制台抄。

### 关键判断：三种结果必须彼此可区分

这一轮真正的风险不在「能不能读库」，而在**读库失败 / 读成空 / 表不存在**三种情况
退化成同一种。三者都会返回 `len==0`，症状是「生成器成功、snippet 为空、`nginx -t` 通过、
平台照常工作」，直到某家租户上传完发现图全裂——而那时距离出错已经很久了。

落地成三条互斥的返回契约，并且各有测试钉住：

| 现场 | 返回值 | 运维看到的文案 |
| --- | --- | --- |
| 真的没人配 | `([], nil)` | `共 0 行` + `⚠️ 没有任何租户接管存储` + 退出码 0 |
| 表不存在 | `(nil, ErrStorageConfigMissing)` | `先跑 ./server migrate 应用迁移 0044（若目录里也没有 0044_...sql，说明发布包不完整）` |
| 连不上 / 库不存在 | `(nil, 包装后的错误)` | `打开数据库失败（driver=mysql）：Error 1049 ...` |

一个实现细节值得记住：gorm 的 `Migrator().HasTable` **只返回 bool、把 error 吞掉了**，
所以不能靠它区分「表不存在」和「连不上」；先打一发 `db.Exec("SELECT 1")` 把后者摘出去。
（真实现场里大多数「连不上」在 `dbopen.Open` 就失败了，`SELECT 1` 兜的是连接随后失效、
以及将来有人加 `DisableAutomaticPing` 的情形——注释里写清了这层分工，没夸大。）

### 另一组判断：不该做的事

- **不过滤** `enabled=false` 与 `driver=local`。判定只允许在 `Target.NeedsSnippet` 一处
  （第五十六轮已写明：只判 `enabled` 会在驱动改成 `local` 时留下一条指向不存在桶的
  `location`，那家租户全站图 404 而后台显示配置是好的）。repository 提前过滤等于把
  这个单点判定复制成两处。
- **凭据列不进 SELECT**。生成器只需要 8 个非密文列；`cmd/nginx-snippets` 跑在能写
  nginx 配置的机器上，把 AccessKey/SecretKey 密文读进内存没有收益、只有风险。
  用 `StorageTarget` 这个窄类型而不是直接返回模型，是让「缺的列」变成编译错误，
  而不是靠注释提醒——返回完整模型时 `PublicRead=false` 到底是「库里就是 false」
  还是「没查这一列」无法分辨，这类误读三个月后一定会发生。
- **`--targets` / `--dsn` 都不许省略，也不许从环境变量自动取**。这份输出决定线上
  哪些 `/uploads/t/<cid>/` 被代理到哪个桶；数据源不确定，等于「这次生成用的是哪份配置」
  无法回答，出事复盘的第一步就断了。两个都给同样拒绝（`--targets` 常是上次演练的残留文件）。

### 落盘安全：两处新增的「拒绝」

1. **原子替换**：写同目录临时文件 → `Sync` → `chmod 644` → `rename`。直接
   `os.WriteFile` 被中断会留下半截配置，最坏形态是某段 `location` 的 `{` 已写出、
   `}` 还没写，`nginx -t` 直接 emerg、下次 reload 卡住，而运维看到的是「文件更新过」。
2. **零 location 不许覆盖非空文件**：配置表被误清、DSN 指到空库、容器挂错卷，
   三种都会走到这里。默认拒绝并提示 `--allow-empty`；首次部署（目标文件不存在）放行。

### 实测记录（本地 `mysql:8.0.46`，docker `wc-mysql-ci`）

| 验证 | 结果 |
| --- | --- |
| 0044 连跑两次（干净库） | 两次 `exit=0`，只有密码 warning；`SHOW CREATE TABLE` 得 19 列 + 唯一索引 + `utf8mb4`，注释全部保留 |
| `TEST_MYSQL_DSN` + `TestMySQLMigrations` | PASS（经真实 `migrate.Apply` 建表） |
| `TEST_MYSQL_DSN` + `TestAPIFlowMySQL` | PASS（完整 parity，13 个业务 flow 全绿） |
| `render --dsn` 对真库（4 行：minio 启用 / oss 启用 / local / s3 停用） | 产出 2 段 `location`，`check --dsn` 对账一致；`status=unknown` 的租户被抖到 stderr |
| 表不存在 / 库不存在 / 端口不通 | 三种文案互不相同，且**全部不落盘**（目标文件未创建） |
| 0 行 + 目标非空 | 拒绝并把字节数说出来；`--allow-empty` 后清空并输出「没有任何租户接管存储」 |

### 反向验证（十处，逐点）

| 注入的坏改动 | 红在哪 | 现象 |
| --- | --- | --- |
| 模型 `TableName()` 改单数 | `migrations_guard_test.go:104/:107` | 双向各报一条（模型缺迁移 / 迁移多表） |
| `storageTargetCols` 偷偷加两个凭据列 | `storage_config_test.go:117` | `should not contain "access_key_enc"` |
| 表缺失分支改成 `return []StorageTarget{}, nil` | `storage_config_test.go:92` | `An error is expected but got nil` |
| 删 `--targets`/`--dsn` 互斥分支 | `main_test.go:36` | 错误变成 `open tenants.json`，证明它走到了读文件 |
| 零 location 守卫置假 | `main_test.go:112` | `An error is expected but got nil` |
| 去掉 `chmod 0644` | `main_test.go`（`TestWriteFileAtomic`） | `expected 0x1a4 / actual 0x180`（0600） |
| 0044 混入 `ADD COLUMN IF NOT EXISTS` | `apply_test.go:201` | 守门报 MariaDB 方言 |
| 0044 混入 `INSERT INTO` | `apply_test.go:153` | 守门报「迁移只放表结构」 |
| `publicBaseUrl` 从白名单移除 | `ref_boundaries_test.go` | 集合多出一项报红 |
| 模型 json tag 改名躲关键字 | 同上 | 仍红——守门是**双向精确对账**，改成什么都瞒不过 |

### 一个被守门抓到的真事

新增 `TenantStorageConfig` 后 `TestP0bResourceJSONFieldsMatchBoundaryList` 立刻红：
新模型的 `publicBaseUrl` 落进了「名字带 url」的扫描集合。没人会主动想到
「配置表里的基址算不算资源引用」，但这条守门逼着回答了一次。

结论写进边界清单 §2：它**确实是 URL，但角色相反**——它是 `ResolveRef(raw, origin)` 里
`origin` 的候选值，是解析器的**输入**不是输出。对它调 `ResolveRef` 不会裂图，
只会把配置页显示的基址悄悄改形，而且要到客户启用自有域名之后才暴露。
这类字段进白名单时单独写了理由，不留「同类合并」的余地。

### 门禁

- `gofmt -l` 本轮改动文件干净；`go vet ./...` exit 0；`go test ./...` 全包绿
  （handler / storage / model / migrate / repository / cmd 全 PASS）；
- 前端与小程序零改动 → 未跑 `vue-tsc` / Vitest / eslint / `vite build`；
- `golangci-lint` 本沙箱无二进制（沿用前几轮结论），用 `go vet` 代偿。

### 边界与下一步

- **已做**：0044 建表、模型进 `model.All()`、repository 窄视图映射、生成器 `--dsn`、
  对账同源、原子落盘、零 location 守卫、十条守门 + 十处反向验证、MySQL 双次实跑与完整 parity；
- **未做（且是有意的）**：写接口与后台配置界面。**表现在能读不能写**，生产上只能手工
  SQL 插入；`access_key_enc` 这一列目前**没有任何代码在写**。看到「表在、生成器能读」
  就以为功能可用，是这一轮最可能的误读，所以设计稿 §3.2 与 §4 都单独标了这一点；
- **下一步顺序（不可颠倒）**：③ 写入侧 `NormalizeRef` 收敛（一个 env 可整体回滚）→
  配置写接口 + 后台界面（`PlatformAdminMiddleware`）→ `UploaderResolver` 5 处注入（P2，
  此时表里的配置才开始真正承载流量）→ 存量回填 P3。

> **订正（第五十九轮已完成 ③）**：上面「下一步」里的第 ③ 步已落地——写入侧不再写驱动返回的
> URL，14 个单值落点 + 1 个数组落点全部在落库前过 `storage.NormalizeOrKeep`，
> 回滚开关是 `STORAGE_REF_STRICT=0`。函数名从计划里的 `NormalizeRef` 改成
> `NormalizeOrKeep`，原因见下一节（严格归一会让「填个外链」保存失败）。
> 剩余顺序不变：配置写接口 + 后台界面 → `UploaderResolver`（P2）→ 存量回填（P3）。

---

## 第五十九轮：写入侧收敛（2026-11-13，落库形态从三种混存收敛到裸键）

### 这一轮的判断错了一次，先说错在哪

开工前的定调是「真正写库的是 **12 个业务表单写入点**（`content.go` 6 处、`apply.go` 6 处），
不是上传接口」。方向对，数字错：实际扫出来是 **14 个单值落点 + 1 个数组落点**——
`content.go` 8 个单值（其中 `company-profile` 那处是 `videoUrl`/`videoBackupUrl`/`cover`
三个字段的**循环**，看上去是一处，实际落三个字段）+ 1 个数组，`apply.go` 6 个。

这个偏差本身不重要，但它说明一件事：**「数一遍写入点」必须由守门来数，不能靠肉眼。**
所以本轮新增的 `TestResourceRefWriteSiteCounts` 不是「顺手加个测试」，它是防止
「以后有人在第三个文件里写业务字段，而所有人都以为已经覆盖完了」的唯一手段。

### 为什么函数不叫 `NormalizeRef`，叫 `NormalizeOrKeep`

计划里写的是「写入侧接 `NormalizeRef`」。真动手时发现 `NormalizeRef` 的契约是
**必须产出带归属前缀的键，推不出来就报错**——而写入侧收到的值里有三类**不该被改**：

| 不该被改的值 | 真实来源 | 硬归一后的症状 |
| --- | --- | --- |
| 外域 URL | `video_source` 的 `url` 档就是任意外域 MP4 直链；员工也会把别处的 CDN 链接贴进头像框 | 一次正常保存变成「保存失败」，用户只会反复重试 |
| 无前缀的老对象（`x.jpg`） | 第五十四轮之前上传的，对象真的就在 `/uploads/x.jpg` | **不报错**，但补成 `t/5/x.jpg` 后出参是 `/uploads/t/5/x.jpg`——一个不存在的对象，端上立刻裂图 |
| 空值 | `avatar` / `cover` / `logo` 大量为空 | 「清空头像」这个操作直接失败 |

所以落成 `NormalizeOrKeep(raw, baseURL, scope, companyID) (string, RefKeepKind, error)`，
返回的第二值是**为什么没归一**（`normalized` / `foreign-url` / `legacy-path`）。
用三值枚举而不是一个 bool，是因为「没归一」的两种原因处置方式完全相反：
外链要留着，历史形态是待搬迁的债。混成一个 bool，日志里只能写「没归一」，
半年后没人分得清是哪种。

### 两个会让功能整体不可用的陷阱

这两个都不是推演出来的，是写测试时被自己的用例打回来的：

1. **本地驱动的返回值和历史对象长得几乎一样，但处置相反。**
   `LocalUploader` 返回 `/uploads/t/5/x.jpg`——剥掉 `/uploads/` 后是 `t/5/x.jpg`，
   **本身就是完整对象键**。如果按老对象处理（原样保留），下次出参就多一层前缀；
   如果按新键处理却再走一次 `ObjectKeyFor`，会撞「禁止双重加前缀」而报错。
   后者更糟：那会让**本地存储下所有表单保存全部失败**——功能整体不可用，而且
   报错信息（「rel 已带归属前缀」）看起来像调用方传错了参数，排查方向直接被带偏。
   正确做法是先 `legacyRel` 剥前缀，**剥完再 `ParseRef` 试一次**，成功就归一。
2. **老对象绝不能补前缀。** 补了不报错，只是指向一个不存在的键。
   这是「写库那一刻没有任何异常、端上才裂图」的典型，最贵的一类 bug。

### 一个反直觉的既有行为，改变了审批侧策略

写注释时我原本写「归一失败保留原值，反正出参 `ResolveRef` 会拦掉」。
读 `model/refs.go:33-42` 才发现 **`ResolveResourceRef` 解析失败时返回的是原值**，
不是空串——也就是说写什么脏值，出参就原样吐什么，`javascript:` 会进 `<img src>`。

于是两处审批侧不能照抄普通表单的「失败就报错」：

- **开通申请审批**（`apply.go:374`）：归一失败 → **置空** + WARN。审核是人工动作，
  不能为一张脏头像让整单批不了；但也绝不能保留原值。
- **名片修改审批**（`apply.go:513`）：归一失败 → **跳过该字段**（不写 `updates["avatar"]`）+ WARN。

普通表单则直接 `code:1`——用户就在页面上，可以改。

### scope 校验为什么故意宽松一档

`checkedScope` 允许**平台对象出现在租户字段里**。这不是漏判，是跟着真实链路走的：
访客在小程序申请页上传头像时还没登录，请求没带 `companyId`，上传就落在 `p/` 下
（见 `handler.uploadCompanyID`），而申请单的 `company_id` 是某家租户。
这是既有数据形态，不是串号。

真正要拒的是**反过来**：A 租户的键（`t/9/...`）写进 B 租户的字段。
那会让 B 的页面显示 A 的文件，两端都看不出异常。
反向验证时把 `checkedScope` 改成无脑放行，`TestResourceRefRejectsForeignTenantKey`
直接把明文报出来：`logo:/uploads/t/9/stolen.png` 已经写进库了。

### 回滚：默认开，可关

`STORAGE_REF_STRICT`，默认开；设 `0` / `false` 退回第五十八轮行为（原样落库）。
选「默认开 + 可关」而不是「默认关 + 可开」——**收敛不默认生效等于债没还**。
关掉是安全降级：出参侧 `ResolveRef` 本来就兼容三种形态，只会让库里退回历史形态，不会裂图。
每次调用读 env 而不是包级变量（包级会污染同包测试）。

### 反向验证六处

| 反向动作 | 谁红了 |
| --- | --- |
| helper 直接返回原值 | `TestResourceRefWriteNormalizesLocalPath`（`库里必须是带归属前缀的裸键`） |
| env 开关写死成回滚 | 同上 |
| `checkedScope` 无脑放行 | `TestNormalizeOrKeepRejectsScopeMismatch` + `TestResourceRefRejectsForeignTenantKey` |
| 给历史对象硬加前缀 | `TestNormalizeOrKeepKeepsLegacyObjects` |
| 删掉 `content.go` 一个调用点 | `TestResourceRefWriteSiteCounts` |
| 从 `sanctioned` 移除 `resourceref.go` | `TestRefWiringGate`（报 `resourceref.go:72` 越界） |

### MySQL parity 证明的是「无回归」，不是「归一生效」

`TestAPIFlowMySQL` 16 个 flow + `TestMySQLMigrations` 全绿。但要说清楚它能证明什么：
流里发的是 `logo: "l.png"`、`bgImage: /uploads/library/...`、`avatar: http://x/a.png`，
按分界分别落进 `legacy-path` 与 `foreign-url`，**被原样保留**。
所以它证明的是「写入侧这 14+1 个点没在 MySQL 下把任何流程改坏」，**不是**「归一生效」。

归一的落库形态由 SQLite 侧的端到端查库用例保证（真的 `db.First` 断言库里的字符串）。
这个分工是有意的：归一发生在任何 SQL 之前，列是 `VARCHAR` / `JSON`，不构成驱动相关差异；
在 MySQL 侧重复同一条断言只是把同一段逻辑再跑一遍，收益接近零，成本是多一套 MySQL 引导。

### 门禁

- `go vet ./...` exit 0；`go test ./... -count=1` 31 个包全绿（handler 约 49s）；
- `TEST_MYSQL_DSN` 实跑 `TestAPIFlowMySQL`（16 flow）+ `TestMySQLMigrations`，19 条 PASS、0 FAIL；
- `gofmt -l` 本轮改动文件干净（`setting/settings_test.go`、`stat.go`、`stat_test.go`、
  `cdn.go`、`storage_test.go` 长期在列，非本轮引入）；
- 前端与小程序零改动。端上契约没变：裸键出参时 `ResolveRef` 补回 `/uploads/`，
  与第五十四轮之后的行为完全一致，所以 `vue-tsc` / Vitest / eslint / `vite build` 未跑；
- `golangci-lint` 本沙箱无二进制（沿用前几轮结论），用 `go vet` 代偿。

### 边界与下一步

- **已做**：`NormalizeOrKeep` 纯函数 + 8 条单测、handler 归一 helper + env 开关、
  14 个单值落点 + 1 个数组落点、端到端 9 条、守门 2 条、反向验证 6 处；
- **未做（有意的）**：云驱动下的验收。裸键要真能出图，依赖 `/uploads/`→桶 反代（R1）
  或 `UploaderResolver` 按租户选桶（P2）。**本地驱动的闭环是真的**（对象在
  `UPLOAD_DIR/t/5/x.jpg`，`/uploads/t/5/x.jpg` 同源可达），但云驱动下这条链还没接；
- **仍未做**：存量回填 P3（老对象搬迁 + 改库），以及 P1 后半段（配置写接口 + 后台界面）；
- **顺序不变**：配置写接口 + 后台界面 → `UploaderResolver` 5 处注入（P2）→ P3 回填。
  这一轮做完后，表里的配置离「真正承载流量」只差 P2，但**仍差 P2**——别看到写入侧收敛了
  就以为多租户存储能用了。

---

## 第六十轮：租户存储配置写接口与后台界面（2026-11-13，P1 收口）

第五十八轮建了 `tenant_storage_configs`，但那张表**能读不能写**——`access_key_enc`
没有任何代码在写，生产上只能手工 SQL。这一轮把它变成可用功能，同时堵住 R9。

### R9 不是「权限收敛」，是「能不能改客户的云凭据」

这条配置里装的是客户的 AccessKey/SecretKey。企业管理员一旦能改，就能把自己企业的对象
写到任意桶、把别人的桶指到自己账号下，而平台从业务侧察觉不到——与 R2「静默失败」同源。

具体漏法极难在 review 里看出来：新接口照抄旁边的 `RegisterStorage`，写
`middleware.AuthMiddleware(db, "admin")`。那行校验的是**登录 + role=admin**，
而企业管理员的 role 就是 admin，所以直接过。两行中间件长得几乎一样。

所以落了两道：

1. **运行时**：`TestTenantStorageTenantAdminForbidden` 造一个真企业管理员（`companyId=1`，
   `role=admin`）去调四个接口，断言全 403 **且配置行零变更**（endpoint/bucket/密文/enabled/status
   逐字段比对）。用员工令牌测不算数——员工在 authenticate 阶段就被挡了，证明不了这条风险。
2. **源码级**：`TestTenantStorageRoutesUsePlatformAdminGuard` 扫出文件里每条 `r.VERB(...)`
   调用（括号配对 + 跳过字符串字面量，因为 check 那条路由跨三行写），要求 `plat` 出现在
   第一个 `func(` 之前。**写在 handler body 里等于没挂**，`contains` 判整个调用会让它蒙混过关。
   再加一条：本文件不许出现 `AuthMiddleware`。

守门第一次跑就抓到了自己：文件头部注释里写着「不要照抄 RegisterStorage 的 AuthMiddleware」，
裸 `contains` 把说明书本身当违规。修法是先 `stripGoComments` 再判——**注释里带 `"//"` 的
URL 也要跳过字符串字面量，否则守门会变成永远通过**。

### 自检：不带凭据去探等于没探

S3 语义下，匿名请求对「不存在的桶」和「存在但无权访问」回同一个 403（防桶名枚举）。
所以「探出 200」只说明域名解析通了，**没法验证客户给的凭据是否有效**。

两条路：做一个只探域名的假自检，或者带凭据真探。选后者，理由是——
**一个「看起来验过了、其实没验凭据」的自检比不做更坏**，它把「没验」伪装成「验过了」，
而这个功能的全部价值就是「填完当场知道对不对」。

实现用 SDK 的 `BucketExists` / `IsBucketExist`（HEAD bucket，最小权限面）。不用列举对象：
那会让「只给了写权限、没给列举权限」的正常桶被误判成不可用。

错误一律不透出 SDK 原文：AccessDenied 响应会回显 AccessKeyId 片段，而 `last_error`
是**常驻后台展示**的字段——一次写入、长期暴露。

同时把边界写进接口返回（`checkedScope`）和界面 alert：自检只验「凭据有效 + 桶可达」，
**不含** nginx 侧 Host/SNI（漏 SNI 会被桶 421，R1 实测过）、CDN 缓存、证书链。
自检通过 ≠ 可以切量。

### 凭据三态：省略 / 空串 / 有值

| 提交 | 含义 |
| --- | --- |
| 不带 `accessKey` 字段 | 保留原值（界面回显掩码时「只看不改点保存」走这条） |
| `"accessKey": ""` | 清空（换账号 / 切回平台账号时用） |
| 有值 | 覆盖，服务端 `pii.Encrypt` 后落库 |

三态不是洁癖：若把「不传」当成「清空」，一次改 bucket 的操作会把客户凭据抹掉，
**而接口照样回 `code:0`**——配置看上去保存成功，等切量那天才全线签名失败。
这与第五十九轮 `*bool` 那条纪律是同一类问题：**「没表态」必须能与「明确不要」分开**。

AccessKey 回掩码（前4****后4）是刻意的：这张表最常见的失败模式是「改了但没生效 / 改错了行」，
只回「已配置」回答不了「现在生效的是不是新那把」。SecretKey 连掩码都不给——它没有核对价值。

### 校验不重写

endpoint 不许带 scheme、bucket 不许含路径与 `..`、hostHeader 不许含引号——
全部复用 `nginxsnippet.Target.Validate` 单点。写入侧复制一份必然与生成侧漂移，
而漂移的表现是「接口说存成功了，生成器却报错」，比直接拒绝难查得多。

只在写入侧加两条**产品边界**（生成器没资格判，因为私有桶的 snippet 也是对的）：
第一阶段只支持公开读桶；启用时必须凭据齐全。

### 反向验证五处

| 反向动作 | 谁红了 |
| --- | --- |
| 摘掉 GET 路由的 `plat` | `TestTenantStorageRoutesUsePlatformAdminGuard`（「路由缺 PlatformAdminMiddleware」） |
| 把 DELETE 换成 `AuthMiddleware(db,"admin")` | 同一条（head 里出现 AuthMiddleware） |
| `accessKeyMasked` 直接回明文 | `TestTenantStorageSuperAdminLifecycle`（「响应体绝不能出现 AccessKey 明文」） |
| 保存后不重置自检状态 | 同上（「改了配置，上一次自检的结论就不再代表当前配置」） |
| 放行私有桶 | `TestTenantStorageValidation` |

每次改回后文件 sha256 零漂移（`faa689bc53ff1179`）、全包复绿。

### 门禁

- `go vet ./...` 0；`go test ./... -count=1` 31 个包全绿（handler 约 55s）；
- `TEST_MYSQL_DSN`：`TestAPIFlowMySQL` 20 条 PASS（新增「租户存储配置」flow）+ `TestMySQLMigrations`；
- 前端：`vue-tsc --noEmit`（4G 堆，单独跑）0、全量 Vitest **97 文件 / 1632 用例**通过、
  `eslint` 0、`vite build --outDir dist-check-storage` 0；
- `gofmt -l` 本轮文件干净。

### 边界与下一步

- **配置仍不承载流量**。上传与出参走平台那一个实例，`trafficEnabled` 恒 false，
  界面顶部有 alert 明说（少这句话，超管填完会以为客户的图已经进自己的桶了）；
- `public_base_url` / `key_id` 仍不进出参也不进表单——**填了不生效比不填更坏**；
- 私有桶、密钥轮换属 P2/P4；
- **下一步只剩 P2**：`UploaderResolver` 5 处注入 + 去掉租户级静默降级。做完那一轮，
  这张表才第一次真正决定文件落在哪个桶。

## 第六十一轮：`UploaderResolver` 接入（2026-11-13，只改形状，开关默认 off）

P2 的前半。目标只有一个：把「哪份配置决定文件落在哪个桶」这条路修通，但**不打开它**。
`TENANT_STORAGE_ROUTE` 默认 off 时注入 `StaticResolver`，进程内只剩一条路径，
与第六十轮逐字节一致——这就是出事时能一秒退回的那条路。

### 开工前先核出两处文档漂移

设计稿 §3.3 有两条要点在开工时已经过期，照抄会做重复功：

| §3.3 原文 | 实际代码 | 处置 |
| --- | --- | --- |
| `presignHandler` 用 `RandName(ext)` 生成裸 key，必须补 `t/<cid>/` | 第五十四轮 W5 已改成 `storage.ObjectKeyFor(scope, cid, name)` | 划掉，标「已落地」 |
| `signHandler` 改为直接按 `objectKey` 签名 | `legacyRel` 的 `/uploads/` 兜底已能从 `<平台入口>/uploads/t/<cid>/x.jpg` 反推 `t/<cid>/x.jpg` | 划掉，本轮只换实例、不动入参契约（前端零改动） |

同时把 Resolver 从四个方法收缩成三个（`For` / `Platform` / `DriverFor`）：
`PresignerFor` / `SignerFor` / `RefresherFor` 都会退化成「先 For 再断言」，
而且静态、租户两套实现要各写一遍。同一个口子写三遍，漏一处的症状是
「A 租户能直传、B 租户不能」，查起来比现在多绕两圈。

### 一条新发现的硬约束：键前缀 ⇔ 实例必须同源

量注入点时才发现，五个 handler 里只有 **3 处写** 需要按租户分流，
另 2 处的对象键前缀就写死了平台：

| 写路径 | 键前缀 | 实例 |
| --- | --- | --- |
| 普通上传 / 直传预签名 / 违规图下架覆盖 | `t/<cid>/` | `For(cid)` |
| 素材采用（`assets.go`） | `p/library/` | `Platform()` |
| 小程序码（`wechat.go`） | `p/wxacode/` | `Platform()` |

`p/` 由平台 nginx 的 `/uploads/` 回退段托管。若把这两处改用租户实例，
结果是「键在平台前缀下、内容在客户桶里」→ 取图 404；而这两处都是
**固定对象名 + 幂等覆盖写**，第一次错不会有人发现（直到某天有人点开那张图）。
所以加了一条源码级守门，逐文件钉死 `For` / `Platform` 的用法。

顺带澄清一个措辞：`newUploaderWithFallback` 是**平台级**的，本轮之前并不存在
「租户级降级」；这一轮的纪律是**新代码不许复用它**，不是删掉它。

### 落地的四条口径

1. **无静默降级**：读配置失败 / 解密失败 / 缺凭据 / 缺基址 → `code:1`，
   错误文案里明写「已按要求不回退平台存储」。客户以为文件在自己桶里、实际躺在
   平台盘上，没有任何人会报错，比上传失败严重得多；
2. **判定复用 `nginxsnippet.Target.NeedsSnippet`**：Go 侧「要不要用租户桶」与 nginx 侧
   「要不要生成回源段」必须是同一个判定。各写一份的结果是「上传写进客户桶、
   nginx 还在从平台盘回源」→ 全站 404 而后台配置显示正常；
3. **缓存按 `updated_at`**：只缓存「构造实例」这一半，配置行每次读一行（唯一索引单行查询）。
   第六十轮的 PUT 走 gorm `Updates`，会自动刷新 `updated_at`，改完立即生效、不必重启；
4. **启动自检**：`on` 时先校验 `tenant_storage_configs` 存在，缺表直接 Fatal 并给出
   「跑 `./server migrate`」。不这么做的话，缺表会让**所有租户**的上传同时不可用
   （不读表就不知道谁配了桶），而第一现场只有一句 MySQL 1146。

### 行为变更只有三处，且都在「判定」上

- `capabilityHandler`：从「按全局 `cfg.StorageDriver` 判」改成「按该租户的实例判」。
  旧实现在「全局 local + A 企业已有自己桶」时会把 A 的「云存储上传」档禁用；
- `signHandler`：从「用平台实例签」改成「用该租户实例签」；
- `trafficEnabled`（配置接口出参）：从硬编码 `false` 改成 `uploadroute.RouteEnabled()`，
  对话框顶部提示随它切换「配置已生效 / 尚未承载流量」。这是本轮自己引入的必须修项：
  开关能打开之后，写死的「尚未承载流量」会在生效后继续骗人，
  而「填了以为没生效」与「以为生效其实没生效」是同一个错误的两个方向。

### 反向验证（改坏一处 → 对应用例必须红）

| 反向动作 | 谁红了 |
| --- | --- |
| `capabilityHandler` 退回按全局驱动判 | `TestCapabilityJudgesTenantInstanceNotGlobalConfig` |
| `For` 读配置失败时静默回退平台实例 | `TestForLoadErrorNeverFallsBack` |
| `DBLoader` 不再判 `NeedsSnippet`（禁用行也当启用） | `TestDBLoaderSkipsRowsThatDontNeedSnippet` |
| 素材采用改用 `res.For(0)`（`p/` 前缀写到客户桶） | `TestUploadWritesRouteByObjectKeyPrefix` |
| `TENANT_STORAGE_ROUTE` 默认改为打开 | `TestRouteEnabledOnlyOnExplicitValues`（8 个「关」子用例全红） |

每次改回后文件零漂移、全包复绿。

### 端到端冒烟（devserver，`TENANT_STORAGE_ROUTE=on`）

对一个匿名上传端点连打三次，只改库里的配置行：

| 状态 | 结果 |
| --- | --- |
| 该企业没有配置行 | `code:0`，URL `/uploads/t/1/<rand>.png`（平台实例） |
| 有行、`enabled=1`、凭据为空 | `code:1`，`企业 1 的自有存储不可用（已按要求不回退平台存储…）：凭据不完整` |
| 删掉配置行 | `code:0`，回到平台实例 |

另外 `off` 态启动日志里**没有任何** `[STORAGE]` 行，`on` 态恰有一行——
证明开关两侧真的是两条不同的装配路径，而不是同一条路加了个空判断。

### 门禁

- `go vet ./...` 0；`go test ./... -count=1` **32 个包**全绿（handler 约 54s）；
- `TEST_MYSQL_DSN`：`TestAPIFlowMySQL` 20 条 PASS + `TestMySQLMigrations`；
- 前端（因 `trafficEnabled` 如实回报而小幅改动）：`vue-tsc --noEmit`（4G 堆，单独跑）0、
  全量 Vitest **97 文件 / 1632 用例**通过、`eslint` 0、`vite build --outDir dist-check-routing` 0
  （该目录被 `.gitignore` 的 `**/dist-check-*/` 覆盖）；
- `gofmt -l` 本轮文件干净（两处 `main.go` 的尾注释对齐因 `up`→`res` 变宽，已重排）。

### 边界与下一步

- **开关仍默认 off**。这一轮交付的是「通路」，不是「已经切了」；
- 开关打开后**只影响新上传**：老对象仍在平台侧，URL 形态不变（都是
  `<平台入口>/uploads/<key>`），所以不会裂图；
- 下一轮（P2 后半）：真桶 + 真 nginx + 真回源的端到端验收，后台把「配置已生效 /
  未生效」显式标出来，再决定是否把默认值翻成 on；
- 之后仍是存量回填（P3）与 `public_base_url` / 密钥轮换（P4）。
