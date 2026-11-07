# 微传名片 · 微信小程序前端（原生 + TS）骨架

按《首页组件与接口字段映射》《跳转页组件与接口字段映射》两份文档落地的前端代码骨架。后端见 `backend/`。

## 目录
```
miniprogram/
├── app.json / app.ts / app.wxss     # 全局配置 / 入口 / 全局样式
├── project.config.json / tsconfig.json / sitemap.json
├── utils/
│   ├── request.ts      # wx.request 封装（自动注入 empToken、统一 code 处理）
│   ├── adminRequest.ts # 管理员通道：注入 adminToken、401 用 adminRefreshToken 单飞刷新（与员工通道严格分离）
│   ├── adminEntry.ts   # 管理员入口决策：目标页前置判定 + 一次性静默免密（准入标记 + 60s 冷却）
│   ├── tenant.ts       # 多租户：企业独立版锁定（pinnedCompanyId）/ 平台模式扫码分发与粘滞
│   ├── types.ts        # 与后端 JSON 对齐的 TS 类型
│   ├── richtext.ts     # <wx-video> 富文本拆分（视频块三来源 ↔ rich-video：视频号/腾讯视频/MP4）
│   ├── wechat.ts       # 微信身份：wx.login 换 openid（可缓存）/ wxLoginCode（一次性 code，一键登录用）
│   ├── privacy.ts      # 微信隐私授权：查 getPrivacySetting / 需否判定 / 等待与结算 / ensurePrivacy(page)
│   ├── contact.ts      # 保存名片到通讯录（员工名片区 / 底部栏共用）
│   ├── team.ts         # 名片墙数据层：分页参数 / 结果整形 / 跨页去重合并（落地页与全部页共用）
│   ├── thumb.ts        # 图片兜底：thumbnailURL 派生 _thumb；onThumbError 退回原图；onImgFail 记失败 URL 换占位
│   ├── subscribe.ts    # 审核结果订阅消息：预取模板 ID（loadAuditTemplate）+ 同步请求授权（requestAuditSubscribe）
│   ├── postersnap.ts   # 海报页图片地址快照：上次的背景/头像/二维码地址，下次进页面提前起跑（见「首屏耗时」一节）
│   ├── mp4box.ts       # MP4 画幅纯函数：按源分辨率算播放器行内高度（首页 / 详情页共用）
│   └── track.ts        # 埋点上报：8 类事件统一入口（白名单就近丢弃，失败静默）
├── components/         # 8 个可复用组件（均开启 addGlobalClass）
│   ├── employee-card / company-video / achievement-grid
│   ├── featured-projects / service-grid / action-bar
│   ├── rich-video      # 富文本视频块：三来源（视频号 / 腾讯视频 / MP4）；MP4 按分辨率自适应画幅
│   └── privacy-consent # 微信隐私授权弹窗（同意按钮用 open-type="agreePrivacyAuthorization"）
├── pages/
│   ├── team/           # 名片墙（**裸启动落地页**）：公司抬头 + 搜索 + 首屏一页 60 + 「查看全部 N 位同事」；企业把默认入口配成「指定员工」时这里只做一次 reLaunch 改道
│   ├── team/all/       # 查看全部同事：搜索 + 滚动加载（onReachBottom 续页）；样式 @import 落地页的 wxss，不复制一份
│   ├── index/          # 名片首页（自定义顶栏含「我的」入口 + 员工卡 + 公司简介卡片 + 业绩 + 服务；底部 tabBar 已移除）
│   ├── project/list/   # 项目列表（按分类）
│   ├── project/detail/ # 项目详情（富文本+视频号拆分+相册）
│   ├── service/detail/ # 服务详情（富文本+视频号拆分）
│   ├── poster/         # 分享海报（canvas 合成 + 发送给好友 / 保存到相册）
│   ├── privacy/        # 隐私政策页（应用内可查阅，审核硬性要求）
│   ├── admin/          # 企业管理员审核：login（账号密码 + 首登绑微信）/ apply（待审列表 + 通过/驳回）
│   └── employee/       # 员工登录 login + 名片修改 edit（提交审核）
└── tests/              # 纯逻辑单元测试（richtext 拆分 / request 刷新重试 / 隐私授权编排 / 管理员通道与审核页）
```

## 企业管理员审核（小程序端，2026-11-06）

员工在手机上能改名片。但「谁通过了我的申请」过去只能在电脑上做——老板出门在外，审批就得等回办公室。这一版把审核搬到小程序：「我的」→「企业管理员 · 待审申请」。

**身份通路（关键设计）**

- 六个 `/api/admin/apply/*` 端点本来就挂 `middleware.AuthMiddleware(db,"admin")`，而 `ExtractToken` **优先读 `Authorization: Bearer`**、其次才读 Cookie。所以只要拿到 `role=admin` 的 JWT，**审核接口一行都不用改**。
- PC 的 `/api/admin/login` 刻意只下发 httpOnly Cookie（JS 读不到令牌，防 XSS 窃取），小程序没有 Cookie 容器。于是新增三条端点，把令牌放进响应体：
  - `POST /api/admin/login/mobile`：账号密码，校验与 PC 完全一致（bcrypt + 空账号烧等价时间 + 企业停用闸门 + 同一登录限流器）。
  - `POST /api/admin/login/wechat`：`wx.login` 的 code → 服务端 `jscode2session` 换 openid → 找绑定了该 openid 的管理员。
  - `POST /api/admin/wechat/bind`：已登录时绑定 / 换绑微信（覆盖写，同企业内一个 openid 只绑一个管理员）。
- 绑定信息存 `admins.wx_openid`（迁移 0039），与 `Employee.openid` 完全同构：明文列、覆盖写、换微信重新绑定即等同解绑。
- **免密不是无认证**：openid 由服务端用小程序凭据换取，绝不信任入参；企业停用照样拦；平台模式下同一微信可能是多家企业的管理员，先用 `companyId` 收窄，收窄后仍多于一条就返回 `code:3` 让用户走密码登录——绝不随便挑一个签发令牌（登错公司 = 看到别家的待审申请）。

**请求通道**

`utils/adminRequest.ts` 与员工通道 `utils/request.ts` **严格分离**：前者只注入 `adminToken`、401 时只认 `adminRefreshToken`，后者只认 `empToken`。混用会导致「员工刷新把管理员令牌冲掉」。刷新仍走 `/api/refresh` 的 Body 流（该端点按 `claims.Role` 签发，不区分身份）。

**页面行为**

- 「我的」页新增一组「企业管理员 · 待审申请」，**挂在员工登录态判断之外**——老板通常不建员工号，套在 `wx:if="{{me}}"` 里等于把入口藏起来。但 `empToken`（员工身份）**不能**证明 `adminToken`（管理员身份），两套令牌通道严格分离，绝不因为「我在『我的』里微信登录过」就直接放行审核列表。
- **目标页在导航前定好**（`utils/adminEntry.goAdminArea`）：已有 `adminToken` → 直接进审核页；没有 → 进登录页。审核页自己只做同步令牌校验，WXML 在 `ready` 前不渲染任何列表，绝不「先闪一下待处理列表再异步跳登录页」。
- **静默免密只做一次探测、之后有准入门槛**：首次点入口（`adminSilentLoginAt` 为空）给一次「这个微信是不是管理员」的探测——这个标记是本版才引入的，若拿它当硬门槛，老用户升级后第一次点入口明明绑着管理员也会被挡回登录页。探测过之后，只有**确实绑过管理员微信**（`adminWxBound`）且距上次尝试 ≥60s 才继续静默免密，失败一律静默、不弹窗。探测失败的纯访客 / 只登过员工的设备从此不再发登录请求——旧版审核页每次 `onShow` 自动尝试，与账号密码登录共用 `login:<IP>` 限流桶（默认 10 次/分），把用户主动点按钮时的额度挤没，真机上出现「我还没登录却提示登录尝试过于频繁」。
- 用户主动「退出管理员登录」调 `endAdminSession`：清令牌 + 清绑定标记 + **把探测时间写成当前值**（不能清成 0，否则会被当成「从未探测」立刻又免密带回审核页）。列表 401 只清令牌，不动绑定标记（令牌过期不等于微信解绑）。
- 限流失败按信封码分流：`code:429` 标题「请稍后再试」、给「稍等一分钟 / 用账号密码登录」两条出路；`code:3` 才是「免密登录不可用 → 先绑定」。旧版把 429 也套在「免密登录不可用」下，用户一下都没登却看到「登录尝试过于频繁」。
- 登录页两个按钮显式给宽度 / 行高 / `white-space: nowrap`：基础库 `style: v2` 下 `button` 默认按内容收缩并左对齐，长文案「用当前微信免密进入」会折成两行。
- 只做「待处理 / 全部」两档筛选，不做关键字搜索——手机打字慢，审核的动作是按时间清队列。
- 通过开通申请后，后端会回员工编号与初始密码，**必须弹出来给人看清**，否则审核人根本没法把账号交给新员工。
- 驳回理由用**页内 `textarea`**，不用 `wx.showModal` 的 `editable`（后者要基础库 2.17.1，低版本会静默变普通弹窗，理由根本填不进去）。
- 列表失败必须落成**可重试的错误态**，绝不能停在空列表——空列表会被管理员读成「没有待审」，直接把工作漏掉。

**审计**

`admin.mobile_login`（手机端拿到管理员身份）与 `admin.wx_bind`（给某个微信号开管理员入口）都留痕；四个审批接口也补上了 `apply.approve` / `apply.reject`——这两个常量早就有定义，但此前只发订阅消息、没有落审计。

用例见 `tests/admin_request.spec.ts`（7 例）、`tests/admin_entry.spec.ts`（18 例）、`tests/admin_apply.spec.ts`（20 例）、`tests/admin_login.spec.ts`（16 例）、`tests/employee_me.spec.ts`（19 例），后端见 `admin_mobile_test.go`。

> **2026-11-07 真机反馈订正**：上述入口 / 冷却 / 限流文案三条是同一次修复的产物，因果链是「审核页 `onShow` 自动免密 → 页面先渲染再异步 redirect → 闪一下列表 + 打满登录限流桶 → 用户点按钮收到 429 且被套错标题」。修完后真机需复核四件事：点入口不再闪列表、两个按钮居中且单行、绑定过的微信点入口直达审核列表、429 弹窗不再叫「免密登录不可用」。

## 隐私合规（2026-10-26）

微信自 2023-10-17 起强制：调用隐私接口前必须取得用户明示同意，否则真机报
`require privacy authorize`、提审驳回。

- **授权状态是全局一次性的**：同意一次后所有已声明的隐私接口都畅通，不是每次调用都问。
- **调用前统一走 `ensurePrivacy(this)`**（`utils/privacy.ts`）：内部先查
  `wx.getPrivacySetting()`，未同意则弹 `components/privacy-consent` 等用户选择。
- **同意按钮必须用 `open-type="agreePrivacyAuthorization"`**：基础库会校验该按钮确实被点击过，
  用普通按钮 + 自己记状态是无效的。
- **刻意不注册 `wx.onNeedPrivacyAuthorization`**：官方要求注册后必须调 `resolve`，
  否则原接口永久挂起（用户看到"点了没反应"）；不注册时漏网调用由微信官方弹窗兜底。
- **自绘弹窗兜不住时走官方弹窗**：`ensurePrivacy(page)` 找不到 `#privacy-consent` 组件时，
  调 `wx.requirePrivacyAuthorize()` 拉起微信官方弹窗（它不依赖页面里的任何组件），
  **不能** `markAgreed()` 后直接放行——那会把本地同意缓存伪造成 true，后续调用全部跳过查询、
  一路 fail，且再也弹不出授权（2026-10-30 的死循环事故就是这么来的）。
- 涉及隐私接口的位置：`apply/open`（chooseMedia / getUserProfile）、
  `employee/edit`（chooseMedia）、`poster`（chooseMedia / saveImageToPhotosAlbum）、
  `employee-card`（setClipboardData）、`utils/contact.ts`（addPhoneContact）。
- **凡是会调到上面接口的页面，都必须同时做三件事**，缺一件就必然踩隐私失败：
  1. `.json` 的 `usingComponents` 注册 `"privacy-consent": "/components/privacy-consent/index"`；
  2. `.wxml` 最外层（`</block>` 之后）挂 `<privacy-consent id="privacy-consent" visible="{{privacyVisible}}" />`，
     放在最外层是为了任何分支下 `selectComponent` 都找得到它；
  3. `.ts` 的 `data` 里声明 `privacyVisible: false`。
  典型例子：`utils/contact.ts` 的 `addPhoneContact` 由首页 `action-bar` 触发，
  所以 `pages/index` 也必须挂组件，并且 `action-bar` 要把栈顶页面作为 `page` 透传给 `saveCard`。
- 平台侧未声明对应隐私类型时**接口会被直接禁用**（`errno: 112`）——
  声明清单见 `docs/deploy-runbook.md` §8。这种失败**加多少次授权都没用**，
  所以 `utils/privacy.ts` 的 `privacyFailKind()` 把 fail 分成三类，调用方必须分开处理：

  | 归类 | 判据 | 该怎么办 |
  |---|---|---|
  | `undeclared` | `errno === 112` 或 errMsg 含 `not declared` | 去公众平台补隐私声明，**别提示用户点「同意」** |
  | `unauthorized` | errMsg 含 `privacy` | 调 `requireAuthorize()` 补授权，成功则重试一次 |
  | `none` | 其余（含 `auth deny`） | 按取消处理；`deny` 才引导去微信设置 |

## 与后端接口对应
| 页面 | 接口 |
|---|---|
| team | GET /api/team?page=&size=60&keyword=（首屏一页；`hasMore` 时显示「查看全部」） |
| team/all | GET /api/team?page=N&size=60&keyword=（滚动续页，同一接口） |
| index | GET /api/card, /api/achievements, /api/services, /api/home-layout, POST /api/stat/track(visit / share) |
| project/list | GET /api/projects?categoryId= |
| project/detail | GET /api/projects/:id + POST /api/stat/track(project_view / share，targetId=案例ID) |
| service/detail | GET /api/services/:id + POST /api/stat/track(service_view / share，targetId=服务ID) |
| employee/login → edit | POST /api/employee/login（手机号密码）、**POST /api/employee/login/wechat（微信一键登录）**、GET /api/company（登录页品牌：企业简称 / LOGO）、GET /api/employee/me、PUT /api/employee/me、POST /api/employee/upload(头像)、GET /api/subscribe/config（订阅模板 ID） |
| employee/me | GET /api/employee/me（含根级 `wechatBound`）、**POST /api/employee/wechat/bind（绑定微信）**、PUT 密码、POST /api/employee/me/cancel |
| apply/open | POST /api/apply/open、POST /api/apply/open/avatar、GET /api/company、GET /api/subscribe/config（订阅模板 ID） |
| action-bar | wx.makePhoneCall + POST /api/stat/track(call/save) |
| company-video | POST /api/stat/track(video_play，组件级去重)（mp4 与 txv 两档；视频号档官方无播放事件，记不了） |
| poster | GET /api/card, GET /api/wechat/wxacode?staffId=&from=poster + POST /api/stat/track(share：发送给好友 / 右上角转发；poster_save：保存成功后) |
| employee/stat | GET /api/employee/me/stat（八类计数 + 按天趋势 + 案例/服务 TOP3 + 来源分布） |

## 埋点（utils/track.ts）
- 统一入口 `track(event, staffId, targetId = 0, source = '')`：staffId ≤ 0 或事件不在白名单里**就地丢弃**（不发请求），
  请求失败 `.catch(() => {})` 静默——埋点失败绝不能影响用户操作。
  第 4 个参数 `source` 是流量来源，空串时**不传该字段**，保持老载荷形状。
- 事件表（权威定义在 `backend/internal/handler/stat.go` 文件头）：

  | 事件 | 触发点 | targetId |
  |---|---|---|
  | visit | 首页 onShow | 0 |
  | call / save | 底部操作栏点击 | 0 |
  | project_view / service_view | 案例 / 服务详情页加载成功 | 对象 ID（后端强制校验存在 + 同租户） |
  | video_play | company-video 的 `<video>` / `<txv-video>` 的 bindplay | 0 |
  | share | 各页 onShareAppMessage；海报页「发送给好友」 | 0（名片）或对象 ID |
  | poster_save | 海报保存成功回调 | 0 |

- targetId 为 0 时**不传该字段**，保持 visit/call/save 的老载荷形状。
- **source（流量来源，迁移 0022）**：由进入场景 `scene` 推导，首页 `onLoad` 取
  `wx.getEnterOptionsSync()`（回落 `getLaunchOptionsSync()`）的 scene，经
  `deriveSource(scene, query)` 映射后随 `visit` 上报：

  | source | 场景 | 说明 |
  |---|---|---|
  | scan | 扫码 / 小程序码区间（1011、1013、1047、1048 等） | 含海报上的码 |
  | share_card | 转发卡片区间（1007、1008、1036 等） | 聊天 / 群聊 / App 互跳，即「裂变」来源 |
  | search | 搜索 / 发现入口（1001、1020、1023 等） | 微信搜索、发现页、我的小程序 |
  | poster | 扫**海报**上的码 | 普通码与海报码的 scene 同在扫码区间，靠 `query.scene` 里的 `from=poster` 标记区分（海报页请求 `/api/wechat/wxacode?staffId=&from=poster`，后端把标记埋进 scene） |
  | unknown | 未识别 / 老客户端 | 端上不传该字段，后端对空串归一 |

  只认明确区间——**识别不了就返回空串、不传该字段**（保持老载荷形状），
  由后端归一为 `unknown`；两个端上口径完全一致，不会丢统计。
  服务端还会再做一次白名单归一（脏值→other）：**source 只作统计维度**，
  绝不参与权限 / 租户 / 计费判断。
- 已知边界：`<channel-video>`（视频号）官方只开放 `binderror`，无播放事件——视频号档
  位的 video_play 缺失是文档级限制，不是实现缺陷；`<video>` 档位正常上报。
- 埋点在 PC 后台「名片统计」消费：转化三指标 + 内容浏览三指标 + 裂变两指标 +
  TOP 案例/服务榜 + 明细下钻 + **流量来源分布 + 裂变榜**。
  三组**互不相加**：内容浏览只有带 targetId 的案例/服务/视频三类（进 TOP 榜），
  `share` / `poster_save` 属裂变动作——它们不带 targetId，且海报页唯一入口
  「我的 → 生成海报」只有员工本人进得去，主体可能是本人（见 `backend/README.md`）。
- 员工端「我的 → 数据统计」自己消费同一份埋点：八项计数 + 按天趋势 + 案例/服务 TOP3 +
  **访客从哪来**（只统计 `visit`，各行之和恒等于访问数，可与转化卡对账）。
  其中「海报」一行是员工唯一能证明海报真的带来访问的凭据——裂变里的
  「海报保存」主体可能是本人，证明不了效果。

## 微信一键登录（2026-10-31）

员工忘记密码是 B 端最高频的工单，而每次都要管理员去后台重置。一键登录把它整个省掉：
`wx.login` 静默拿一次性 code → 后端换 openid → 找到已绑定该 openid 的员工 → 发令牌。
全程没有授权弹窗，用户什么都不用输。

**前提是「员工账号已绑定 openid」**，两条来源：

| 来源 | 是否开箱可用 | 说明 |
|---|---|---|
| 小程序内自助申请开通 | ✅ | `POST /api/apply/open` 提交的 openid 在审批通过时写进员工记录（`apply.go` 审批分支） |
| 管理员后台建号 | ❌ 需绑一次 | 后台建号没有 openid；员工用手机号密码登录后，在「我的 → 微信一键登录」点一下绑定 |

**刻意不做「首次用微信登录自动建号」**：员工账号是审核出来的（谁是哪家公司的人必须有人确认），
自动建号等于任何人点一下就变成某企业的员工，名片墙会立刻被陌生人占满。

页面与状态：

- `pages/employee/login`：默认登录是「手机号 + 密码」（蓝色主按钮）；「微信一键登录」放在其**下方**，
  白底绿描边作为次入口（保留微信绿与微信 logo，仍一眼可辨）。顶部品牌区优先显示**企业简称**
  （有企业 LOGO 就显示 LOGO），拿不到企业名时显示中性的「员工登录」——**不回落到产品名**，
  避免登录页顶着平台品牌让客户觉得「没改干净」。
  手机号密码仍是一键登录失败时的唯一出路，也是绑定微信的前提。
- `pages/employee/me`：「账号」分组里的「微信一键登录」单元格显示 `已绑定 / 未绑定`，
  未绑定时点一下即绑（状态来自 `/api/employee/me` 根级 `wechatBound`，不由前端猜 `data.openid` 是否为空）。

失败分支必须分开处理，混成一句「登录失败」等于功能不可用：

| 后端 code | 含义 | 端上行为 |
|---|---|---|
| 0 | 成功 | 存 token + refreshToken，返回「我的」页 |
| 1 | 参数 / 微信侧错误（code 过期、未配凭据） | toast 后端文案 |
| 2 | 账号或企业已停用 | toast「账号已停用 / 所属企业已停用」 |
| **3** | 该微信未绑定，或绑定了多个账号 | **modal 说清「先密码登录 → 我的页绑定」**，不跳转、不存令牌 |

几个容易踩的点：

- **必须用 `wxLoginCode()` 拿新 code，不能复用 `getOpenid()`**：后者把 openid 缓存进 storage
  （openid 不变，缓存是为了少打一次接口），而一键登录必须每次拿新 code——code 一次性、5 分钟过期。
- **请求要带 `companyId`**：企业独立版各配自己的 appid，凭据按租户选；不带的话后端按平台全局凭据换，
  换出来的是另一个小程序的用户，永远匹配不上。
- **绑定请求不带 `companyId`**：绑定是已登录场景，后端一律用员工自己的企业取凭据，不信客户端传值。
- **换微信 = 重新绑定（覆盖写）**，不需要「解绑」按钮：旧 openid 被覆盖后匹配不到任何员工，
  效果等同解绑，还不会顺手清掉后台用于核实身份的历史记录。
- **同一企业内一个微信只能绑一个账号**：否则两人共用一个微信号时，谁点一键登录都进到先绑的那个人。

用例见 `tests/login_wechat.spec.ts`（20 例），后端见 `login_wechat_test.go`（4 组）。

## 订阅消息（审核结果通知，2026-10-31）

审核在 PC 后台做，而员工/申请人提交完就关掉小程序了——没有这条通知，他永远不知道
自己通过没通过，管理员还得另外用微信或电话告诉他初始密码。

**只做「审核结果」，不做「名片被访问」**：微信一次性订阅是「用户同意一次只能发一条」，
长期订阅又只对政务/医疗等类目开放。名片访问是高频事件，用户同意一次只收到第一条、
之后再也不响，比没有更糟。所以刻意不做。

接线分两步，顺序不能颠倒：

| 时机 | 动作 |
|---|---|
| 页面 `onLoad` | `loadAuditTemplate()` 预取模板 ID（GET `/api/subscribe/config`，模块级缓存） |
| 点击提交 | `requestAuditSubscribe()` **同步**调用（在第一个 `await` 之前） |

三个必须记住的点：

- **`wx.requestSubscribeMessage` 必须在用户手势的同步栈里调**。`await` 之后再调会被微信
  拒绝，现象是「点了提交但从没弹过授权窗」，代码看起来完全正常。
  这就是为什么要把「预取模板 ID」和「请求授权」拆成两步。
- **用户拒绝订阅不影响提交**。通知是可选增强，不能拿它要挟主流程；`accept` / `reject` /
  `ban` 三种回调都不做任何处理。
- **模板 ID 必须来自后端，不能写死在端上**。企业独立版各家小程序各有各的模板；
  写死会让「平台能发、独立版发不出去」只在某个客户的真机上暴露。
  未配置时后端回空串，端上据此跳过授权、不弹空窗（空 `tmplIds` 微信直接 fail，
  用户看到的是「点了没反应」）。

接入位置：`pages/apply/open`（开通申请）、`pages/employee/edit`（名片修改）。

用例见 `tests/subscribe.spec.ts`（9 例，含「先订阅后提交」的顺序断言），后端见
`subscribe_test.go`（11 例）与 `internal/wechat/subscribe_test.go`（8 例）。

## 员工头像上传
- 员工修改页 `pages/employee/edit` 的头像由「URL 文本输入」改为「选图上传」：用 `wx.chooseMedia` 选图，经 `wx.uploadFile` 调后端 `POST /api/employee/upload`（员工鉴权，复用同一 storage.Uploader），返回 URL 写回 `form.avatar`，再随 `PUT /api/employee/me` 提交审核。与 PC 端 `UploadField` 调用 `/api/admin/upload` 同一思路、同一存储层，仅鉴权角色不同。

## 首页背景图（商业化美化包装，可选）
- PC 后台「小程序设置 → 名片首页样式」可设置首页背景图（支持从内置素材库「背景」分类直接选用 750×1334 竖幅素材），并可调节遮罩浓度 0~100。后端经 `GET /api/home-layout` 在**响应根级**下发 `bgImage` / `bgMask`（与 `order` 同级）。
- 首页 `pages/index` 收到非空 `bgImage` 时：铺一层全屏背景（`mode="aspectFill"`），其上叠 `bgMask/100` 透明度的黑色蒙层，白色内容卡片浮于其上形成层次；`bgImage` 为空即保持默认灰底布局，不渲染背景层。
- 背景层与蒙层用 `position: fixed` + 负 `z-index` 铺在最底层，内容按常规流绘制天然在其上方，不会互相遮挡；有背景时顶栏改为半透明白、板块卡片加圆角阴影（`.has-bg`）。

## 腾讯视频档（`company-video` 的 txv 分支）

`company-video` 按后端 `videoSource` 选档：`channels` → `<channel-video>`（feedId / feed-token）、
`upload` / `url` → `<video>`、`tencent` → 腾讯视频插件的 `<txv-video vid>`、`none` → 封面占位。

> `upload` 与 `url` 在这里是**同一个分支**，渲染结果完全一样（都是原生 `<video src>`）。
> 2026-11-04 起后台界面已把两者合并成「自托管 MP4（上传或直链）」一档，
> 但**接口取值不变**，所以这个分支不动——把两者分开处理反而是错的。
主源 `binderror` 且后台开了 `videoFallbackUrl` 时按档位回落：视频号 / 腾讯视频档改用 `videoUrl` 里的 MP4；自托管 MP4 档改用独立的 `videoBackupUrl`（见下）。没开兜底则回落封面占位，或直接显示加载失败态。

### MP4 档的地址与失败态（2026-11-05）

- **地址必须绝对化**：`pages/index/index.ts` 在 `loadAll` 里对 `profile.videoUrl` 过一次 `assetUrl`，和头像/LOGO/封面同批。直传失败回退 multipart 时后端可能返回 `/uploads/xxx.mp4` 相对路径，原生 `<video src>` 不认——表现是一块黑屏 + `00:00`，既不弹错也不报错。主源与「兜底 MP4」共用这一列，处理一处即可；已是 `http(s)://` 的外链原样保留。
- **失败不再沉默**：`upload` / `url` 以及「兜底切过来的 MP4」触发 `binderror` 时，`mp4Error` 置位，播放器上盖一层「视频加载失败 + 重新加载」。控制台同时打出 `src`——这类失败九成是地址本身在浏览器里就打不开（CDN 未启用 HTTPS / 证书主机名不匹配 / 对象不存在 / 私有桶没签名），光看 `errMsg` 定位不了。
- **重试要真的重建播放器**：`src` 绑的是 `mp4Src` 而不是 `videoUrl`，`onRetryMp4` 先 `setData({mp4Src:''})` 再在回调里设回地址；对同一个 src 直接 `setData` 不会重新发请求。
- 后台「公司简介」页会显示**最终播放地址**并允许点开验证；这里打不开，小程序里一定播不了。
- **画幅：MP4 按源文件分辨率调整窗口，完整呈现**（2026-11-05 二次修正）：容器原本固定 `width:100%` + `height:440rpx`（约 1.52:1），上传的 MP4 多为 16:9。最早用 `object-fit="contain"` → 上下各约 39px 黑边；改成 `cover` 去凑「和视频号一样满屏」→ 画面按高度铺满、**左右各裁掉约 7%**，两侧字幕 / LOGO 直接消失，用户明确否掉了这个改法。实测 `854×480`（HEVC / 30fps）抽帧证实源片无黑条，服务端 `ProcessUpload` 对 mp4 原样透传，**黑边与转码、分辨率变化无关，纯粹是容器比例不匹配**。正解是 `bindloadedmetadata`（基础库 2.7.0+，`detail={width,height,duration}`）拿源文件宽高，`wx.createSelectorQuery().in(this).select('.cv-mp4')` 现场量容器宽度（首页卡片有左右内边距，不能用 750rpx 硬算），由 `mp4BoxStyleFor()` 算出高度写进 `<video style="{{mp4BoxStyle}}">`，行内样式覆盖 WXSS 的 440rpx，配 `object-fit="contain"`——容器比例 == 视频比例，既不裁切也不留黑边。metadata 未到 / 基础库低于 2.7.0 / 量不到宽度时退回 440rpx + contain：**最多留黑边，绝不裁内容**；极端竖屏（9:16）用 `MAX_MP4_RATIO=4/3` 钳高，避免公司简介区把整页顶下去。换档 / 换地址 / 切备用 / 重试时都会复位 `mp4BoxStyle`，等新 metadata 重算。视频号是 `channel-video` 插件、腾讯视频是 `txv-video` 插件，**都没有 loadedmetadata**，各自保持 `cover` / `contain`；不要为了「三档一致」牺牲画面内容。`<video>` 的 `contain` + `bindloadedmetadata` + `style="{{mp4BoxStyle}}"` 由 `tests/company_video.spec.ts` 静态守门钉住，误改回 `cover` 会直接红。
- **详情页富文本 MP4 块同步（2026-11-06）**：`components/rich-video` 的 mp4 档原先固定 `360rpx` 高（内容区约 702rpx 宽，≈1.95:1），16:9 横屏 `contain` 会左右各留约 31rpx 黑边——方向恰好与首页早期的上下黑边相反，**留黑边而非裁内容**。现在 `bindloadedmetadata` 拿源宽高、`createSelectorQuery().in(this).select('.rich-video')` 现场量宽度，共用 `utils/mp4box.ts` 的 `mp4BoxStyleFor()` 写进行内高度；换块时清掉上一段视频算出的高度，量不到 / 低版本退回 360rpx + contain（最多留黑边）。`txv-video` / `channel-video` 是插件组件，**没有 loadedmetadata**，各自保持 `contain` / 不写 object-fit，不为「三档一致」改变插件行为。`tests/rich_video.spec.ts` 静态守门钉住这些边界。

### 自托管 MP4 的兜底备用地址（2026-11-05）

视频号 / 腾讯视频档早就能配「主源失败时改用 MP4」，自托管 MP4 档此前没有第二个地址。现在四档共用 `videoFallbackUrl` 开关，但**地址分两条槽**：

- `channels` / `tencent`：兜底地址存 `videoUrl`（这两档下它没有别的用途，存量行为不变）。
- `upload` / `url`：主源是 `videoUrl`，备用地址存独立的 `videoBackupUrl`（后端迁移 0038 新列）。两列不能合并——保存主源时会把共用槽覆盖掉，结果是「界面上填了、库里却没有」。

小程序端的切换规则（`company-video`）：

- 主 MP4 `binderror` 且开了兜底、备用地址非空且与主源不同 → `mp4FallbackUsed` 置位，先清空 `mp4Src` 销毁 `<video>`，再在 `setData` 回调里设回备用地址。**只切一次**；备用也失败才显示失败态，提示会点明「主视频与备用地址都无法加载」，避免管理员只盯着主源查。
- 换档 / 换地址时 observer 复位 `mp4FallbackUsed`，新配置能重新兜一次。
- 点「重新加载」重试的是**当前**地址（已切到备用就重试备用），不会跳回失败过的主源；报错日志打的也是当前 `src`。
- `videoBackupUrl` 与主源一样过 `assetUrl` 绝对化；后端没下发该列（升级前部署）时归一成空串。
- 备用地址**不能与主源相同**：小程序端切换条件是 `backup !== currentSrc`，填成同一个地址时主源一失败就直接显示失败态，兜底根本不触发；后台保存时会拦下这种配置。建议用**与主源不同的域名 / CDN**，同一域名挂了两边都挂，等于没兜底。填完请在浏览器里打开确认能播放（后台不再把备用地址重复回显成一行链接，输入框里就是最终写库的值）。

`tencent` 档依赖腾讯视频小程序插件（provider `wxa75efa648b60994b`），而插件必须**静态声明**：

> 声明了却没在 MP 后台授权的插件会让**整个小程序编译失败、连预览都传不上去**。
> 所以默认不带插件，要用的部署方显式打开；不用的部署方升级后行为不变。

```bash
node miniprogram/scripts/enable-tencent-video.mjs on    # 写 app.json + company-video/index.json
node miniprogram/scripts/enable-tencent-video.mjs off   # 还原
node miniprogram/scripts/enable-tencent-video.mjs       # 只看状态
```

脚本只改这两处静态声明，另外两件事：① MP 后台「设置 → 第三方服务 → 插件管理」
添加腾讯视频插件并等授权通过（只能人工做）；② 后台「系统设置 → 功能开关」打开
「腾讯视频档」（即时生效，不用重启；也可在部署环境设 `MP_TENCENT_VIDEO=true` 作为
界面未设置时的回落值）。插件版本号默认 2.6.2（1.X 已停止维护，官方要求升级到 2.1.X 后续版本），需覆盖时用
`PLUGIN_VERSION=2.6.3 ... on`（必须 2.X）。

**内容来源**：`<txv-video>` 取一个 `vid`（v.qq.com 播放页 `page/` 与 `.html` 之间的那段）。**只要是腾讯视频公开可播的 vid 都能放**——您自己创作号的、别人公开的非会员视频都行（服务市场文案「仅可播放您上传的视频」是营销口径，微信开放社区开发者实测他人 vid 也能播）。插件**不支持任意 URL**：传的是腾讯视频 `vid` 而非裸 mp4/http 链接。创作号分类别选「文化 / 历史」可能导致无法播放全片（有用户反馈，未逐一核验），宣传视频建议选「企业 / 商业」类。

已知形态（插件自带，界面改不了）：免费版片头有广告；`txv-video` 的 `z-index` 为 11，
自定义浮层要压在它上面时记得抬高。

### `autoplay` 必须显式写（2026-11-04）

腾讯视频插件的 `autoplay` **默认是 `true`**（官方参数表），而微信原生 `<video>` 默认
`false`。我们原来没写这个属性 = 继承插件默认 = 一进名片页就带声音自己播。

现在两处渲染点都显式声明，且各自由来不同：

| 位置 | 取值 | 为什么 |
|---|---|---|
| `components/company-video`（公司简介） | `{{autoplay}}`，来自 `profile.videoAutoplay` | 后台有开关（默认关），客户要自动播放可以自己打开 |
| `components/rich-video`（详情页富文本块） | 固定 `false` | 正文中间突然出声比首页更糟；富文本块没有逐块播放设置 |

`tests/company_video.spec.ts` 有一条静态守门扫这两个 wxml：**任何 `<txv-video>` 缺
`autoplay=` 直接红**。删掉这个属性不报错、不变丑，只会悄悄自动播放，靠人眼 review 拦不住。

### 点整块播放、暂停后一点就继续（2026-11-04）

这条是两轮反馈叠出来的：

1. 插件播放键默认在**左下角**（`playBtnPosition` 默认就是 `bottom`），又小又偏，
   访客点画面中央毫无反应 → 反馈「视频坏了」。
2. 第一版只修了「首次启动」——遮罩绑在「没播过」上，播起来就永久撤掉，
   于是**暂停之后再点又没反应**，还得回去角落翻那个小按钮。

**先用插件自己的两个属性**（比自己盖遮罩靠谱，不挡任何原生控件）：

| 属性 | 值 | 作用 |
|---|---|---|
| `play-btn-position` | `center` | 播放键从左下角挪到中央 |
| `enable-play-gesture` | `true` | 双击播放/暂停（默认 `false`，必须显式开） |

**遮罩只补「点任意位置」这一件事**，按状态分两张：

| 状态 | 遮罩 | 出现条件 |
|---|---|---|
| 没播过 | 全屏，点任意处即播 | `!txvStarted && !autoplay && !txvMaskOff` |
| 暂停 / 播完 | 中央 240rpx **透明**热区（不画按钮） | `txvStarted && !txvPlaying && !autoplay && !txvMaskOff` |
| 正在播 | 无 | — |

**为什么暂停态只盖中央、播放中完全不盖**：暂停时用户最常见的动作是拖进度条。
铺满就把底部进度条和全屏键挡死了；播放中若还盖着，连免费版片头的
「跳过广告」按钮都点不到。所以播放中必须把界面整个让出来。

四个状态字段，**别再退回「一个 started 布尔走天下」**：

| 字段 | 含义 | 谁维护 |
|---|---|---|
| `txvStarted` | 曾经播过 → 决定用全屏遮罩还是中央小遮罩 | `bindplay` |
| `txvPlaying` | 当前是否在播 | `bindplay` / **`bindpause`** / **`bindended`** |
| `txvMaskOff` | 遮罩永久关闭（退化路径专用） | 取不到上下文 / `play()` 抛错 / 起播超时 |
| `txvEnded` | 已播完 → 下次点击先 `seek(0)` | `bindended` / `bindplay` |

**`bindpause` 和 `bindended` 少接一个，遮罩就退回一次性**——这是第二轮反馈的根因。
这两个回调没有视觉症状，测试里专门有一条守门。

其余四条静默失效约束（漏了都不报错、不变丑，只是点了没反应）：

| 约束 | 测试怎么钉 |
|---|---|
| `playerid` 与 `TXV_PLAYER_ID` 一字不差 | 扫 wxml ↔ ts 常量 |
| 插件名是 `tencentvideo`（全小写，不是官方示例的 `tencentVideo`） | 比对 `app.json` 的 plugins key |
| 遮罩 `z-index` > **11**（插件内部层级） | 解析 `app.wxss` |
| 两个遮罩的 `wx:if` 条件不能写反 | 扫 wxml 条件片段 |

两条**故意**的退化（都是宁可少一层、也不能留一块点不动的挡板）：

- **拿不到 `TxvContext`**（插件未声明 / 组件未渲染完 / `play()` 抛错）→ `txvMaskOff` 永久关掉遮罩，
  把界面交回插件。
- **起播 10 秒没等来 `bindplay`** → 同样关掉。`vid` 未过审、断网时插件可能既不播也不报
  `error`，没这个兜底就是永久转圈，看着像小程序卡死。

遮罩是普通 `view`，靠 `<video>` 同层渲染才压得住原生组件。真机上万一压不住，
它不显示也不接事件，界面自动回到插件自己的按钮——**是渐进增强，不是必需依赖**，
所以代码里没有任何「先探测再决定」的分支。

> `rich-video`（详情页富文本块）的 **MP4 档**从 2026-11-06 起共用这套画幅算法
> （见「MP4 档的地址与失败态」），插件两档仍按原样，不做「三档一致」。
> 另外它有个独立的坑已一并修掉：腾讯视频插件的 `playerid` 要求同页全局唯一，
> 原先写死 `richVideo`，富文本一页插多个腾讯视频块会互相串。现在 `attached`
> 时用模块级自增序列生成实例唯一值；首次渲染仍给非空保底值，且 `autoplay=false`，
> 属性变化发生在用户点播放之前，不会打断观看。`tests/rich_video.spec.ts` 有静态守门。

## 小程序码 + 海报生成
- **海报页对访客是「只读预览」**（2026-10-28 起）：`canEdit` = 登录 && 页面 `staffId` 就是我自己
  （两个来源：员工接口回的 `staffId`，或登录时写进 `globalData.myStaffId` 的那份；
  两个都没有才降级只读）。只读态隐藏拖动层 / 工具条 / 选项面板 / 上传入口，
  只留「保存到相册」「发送给好友」——海报是发给外人看的营销物料，
  被收件人改写再转发出去，品牌就不可控。只读态顶部有一行说明这是谁的名片。
- **访客看到的必须是员工排的那张**：转发链接只带 `staffId`，而版式原本只存在员工本机
  （`posterLayout:<staffId>`）与他的账号里（`/api/employee/poster-prefs` 要鉴权）——
  访客两者都拿不到，会落回 `DEFAULT_LAYOUT`，员工调的排版 / 挑的背景 / 写的 slogan
  全部传不出去。故新增公开快照 `GET /api/poster-prefs?staffId=`（无鉴权、只回渲染
  这一张所需的最小集），访客用它渲染；本机缓存**只对本人**生效。
- 后端 `GET /api/wechat/wxacode?staffId=` 调用微信 `getwxacodeunlimit` 生成小程序码（scene=`staffId=12&from=poster`），按员工磁盘缓存到 `uploads/wxacode/<id>.jpg` 并返回可访问 URL（复用 `/uploads` 静态托管），避免反复消耗微信配额；access_token 在服务端内存缓存并在失效时自动刷新重试。
- 小程序端 `pages/poster/poster` 用 canvas 2d 把「头像 / 姓名 / 职位 / 公司 / 地址 / 电话 / 小程序码」合成为海报图，`wx.canvasToTempFilePath` 导出后 `wx.saveImageToPhotosAlbum` 保存到相册。
- 首页「生成分享海报」与「我的」页「生成海报」均跳转到 poster 页（携带 staffId）。

### 海报背景（二十四节气 / 商务场景 / 自定义上传）
海报页下方是**工具条 + 单行选项面板**：工具条固定六个入口（背景 / 版式 / 元素 / 文字 /
大小 / 位置），点哪个只在面板里渲染那一个工具的选项。背景工具是一条横滑的
**背景选择条**：素材取自后端内置素材库的 `poster` 分类（`GET /api/assets/posters`，公开端点），
24 张二十四节气 + 35 张节日 / 商务场景（除夕/腊八/小年/龙抬头/七夕/重阳/情人节/
女神节/劳动节/母亲节/儿童节/父亲节/教师节/感恩节/平安夜 + 商务合作/开业大吉/新品上市/
客户答谢/开工大吉…），点一下即整张换背景重画；首项「原样」= 白底卡片，
第二项「上传」= 用自己的照片当背景。

> **工具按「调哪个维度」分组，不是按「调哪个对象」**：头像大小和码大小同在「大小」，
> 内容区位置和码位置同在「位置」。按对象分（头像 / 位置）会逼用户先想「码算哪个对象的」——
> 它两个都沾；想「把码放大点」的人也不会先去点「头像」里找。
> 「位置」里两行各带自己的复位（`resetContent` / `resetQr`）：拖歪的从来只是刚弄乱的那一块，
> 一起归零会顺手挪回另一块，用户会以为自己的调整没保存上。

> 为什么不是「背景 / 版式」两个 tab + 五行选项：海报画布本身占 920rpx，五行选项
> （模板 / 元素 / 配色 / 头像 / 位置）全铺开必然把「位置」和「保存 / 分享」顶到屏幕外，
> 用户得先滚动才够得着——而「位置」恰恰最需要边看海报边调。改成工具条后面板高度
> 从 5 行降到 1～2 行，全部选项一屏可见，「位置」一点就到。

要点：

- **构图固定**：素材上部 0~150（画布坐标）是烧进图里的标题（立春 / 合作共赢…），
  下部已整体压深；小程序把员工信息一律画在 150 以下并用浅色字——这样一张模板
  能给所有员工复用，不必为每人出一张图，也保证任何海报上白字都读得清。
  自定义背景没有这段留白，内容起点改为 24，并额外叠一层「透明 → 黑」渐变蒙层
  （用户随手拍的图底部可能是白墙，白字压上去会糊）。
- **上传自定义背景**：`wx.chooseMedia` 选图 → `wx.uploadFile` 到
  `POST /api/employee/upload`（员工鉴权，与员工改头像同一条链路、同一存储层）→
  返回 URL 加进「我的背景」并立即选中。id 由客户端生成（`c-` + 时间戳36进制 + 4位随机），
  后端只校验格式。上限 9 张，长按删除（内置素材不可删——它们编译在二进制里）。
  未登录（访客扫码进来）不显示上传入口，点了会提示先登录。
- **版式可自定义**：8 套模板（经典 / 居中 / 极简 / 卡片 / 分栏 / 大字 / 清单 / 画报，
  两排选满）× 9 个元素显隐（头像 / 姓名 / 职位 / 公司 / 地址 / 手机 / 座机 / 邮箱 /
  二维码）× 文字配色（自动 / 浅色 / 深色）×
  大小（头像三档 56 / 72 / 88 + 码四档 自动 / 76 / 104 / 140）×
  一句话自定义文案 × 信息区与二维码各自的位移。改任何一项都整张重画。
- **四套新模板各自的定位**（不是换汤不换药，每套解决的问题不同）：
  - `split` 分栏：头像在左、文字全部**右对齐**，镜像的经典款——同一份内容换个方向，
    观感就从「名片」变成「版式设计」。
  - `left` 大字：姓名 27px 独占一行，下面压一条主色短线，像一张「个人抬头」。
  - `stack` 清单：每行「标签 + 值」两列对齐。**九项全开时只有它还能看清**
    哪个是公司、哪个是座机——信息密度最高的一套。
  - `poster` 画报：内容下沉到画布中下部，上方整块留给背景图主体——风景 / 人物类
    背景的上半部分往往最好看，压满文字就白瞎了。
  分隔线（`Geo.rule`）用 `fillRect` 画 2px 实心块而不是 `stroke`：任何 dpr 下都不糊。
- **元素开关里座机 / 邮箱默认关、二维码默认开**，两个方向的默认值刻意相反：
  前两者是「新出现的信息」，默认塞给用户会改掉他已经调好的版式；后者是海报唯一的
  落地入口，默认藏起来等于让用户发出去一张没人扫得到的图。
- **关掉二维码 = 纯形象海报**：`qr.size` 归零，不加载、不占位、不画「长按识别名片」，
  码的位移区间一并归零（否则「位置」里还能拖一个看不见的东西），
  连「码没生成」的黄条也不弹——那是用户明确关的，不是故障。
- **姓名可以关掉**（活动海报 / 只做品牌曝光时不想露个人名字）：关掉后**职位自动升为
  标题行**（13px 灰字 → 17px 主字，经典/居中/卡片三套模板一致；极简模板升到 18px）。
  不做这一步的话，头像旁边会只剩一条孤零零的小灰字，看着像「名字没加载出来」。
- **一句话自定义文案**（`customText`，上限 30 字）：宣传语 / 服务承诺 / 活动信息，
  画在名片信息下方，用主色强调（浅底 `#1a8cff`、深底 `#9ad0ff`）与名片信息区分开。
  存在版式里而不是每次现填——它更像「我的签名」，调好一次以后每张海报都带着。
  两个实现要点：
  - **输入与提交分开**：`bindinput` 每敲一个字只 `render()`（画布立刻跟着变，**不导出**），
    `bindblur` 才 `applyLayout()` 归一 + 落存储 + 同步后端 + 重新导出。导出要编码整张 PNG，
    一个字导一次会明显卡顿；而打字时用户根本不看图，只想看文字对不对。
  - **必须压平换行**：`fillText` 不认 `\n`（会画出缺字或方块），而粘贴进来的文案很容易
    带换行。`normalizeCustomText` / `normalizePosterText`（前后端各一份，规则一致）
    把控制字符折成空格、连续空白折成一个、按**码点**截断（不会把 emoji 劈成半个）。
- **位置能拖到画布内任意位置**：按住海报拖「信息区」，按住二维码单独拖「码」——
  两者各有各的偏移，互不牵连（以前码钉死在底部居中，想挪它只能整张海报一起动，
  结果文字跟着歪）。「位置」工具里二维码会显形为虚线框 + 角标，否则没人会想到它能拖。
  按钮保留做精调（每次 10px，内容与码各一组），各带自己的「复位」。
- **可拖范围按内容包围盒算，不是拍脑袋的固定值**（`measureBox` + `shiftRange`）：
  先量出这段文字实际多宽、多高，再反推「左/右/上/下各还能挪多少，且不出画布」。
  两字姓名能横跨大半个画布，二十字公司全称就只给一点点余量——固定区间做不到这种
  不对称：给宽了长文本会被裁掉尾字，给窄了两字姓名拖两下就顶死（这正是之前横向只给
  ±16 时被抱怨「拖不到任意位置」的原因）。内容比画布还宽时区间退化为 `[0, 0]`
  （宁可钉住不动，也不推到更糟的位置）。
- **夹紧后的位移会回写**（`reconcileOffsets`）：连点「下移」到边界后数字不再增长，
  往回拖时也不会出现「前半个行程没反应」的死区。拖动中用「增量 + 每帧夹紧」而不是
  「起点 + 总位移」，同一个原因。
- **拖动实现要点**：
  - **拖动中不导出、不 setData**。`canvasToTempFilePath` 要编码整张 PNG（几十毫秒），
    每帧调必掉帧；`setData` 走视图层通信，同样拖不动。所以拖动中直接改 `this.data`
    （不通知视图层）并只 `render()`，松手才 `setData` + 导出一次。为此把原来的
    `draw()` 拆成 `render()`（只画）+ `exportImage()`（只导），`draw()` = 两者串联。
  - **图片走 `loadCached`**：背景 / 头像 / 二维码按 src 缓存，同一张只加载一次。
    不缓存的话每帧都在等网络和解码，画面一顿一顿的。失败也缓存，但只锁 3 秒——
    不缓存会在断网时每帧发一次请求，永久缓存又会让一次抖动把这张图在本页判死。
  - **两层热区**：`canvas` 本身收不到 `bindtouch`，所以盖一块透明的 `.poster-drag`
    接住「拖信息区」；再叠一块位置由几何算出的 `.qr-hit`（`syncQrRect`）接住「拖码」。
    用热区而不是「把触摸点换算成画布坐标」：换算要拿 `boundingClientRect`，而拖动是
    同步的、拿不到异步查询结果，第一下必然错位。热区由几何直接给出，天然准；且手指
    按下时就锁定在码上，拖的过程中热区不必跟着动（`syncQrRect` 在拖动中故意跳过）。
  - 两层都用 `catchtouchmove` 阻止页面跟着滚；加载 / 失败时撤掉，让遮罩上的重试按钮可点。
  - 2px 死区：手指按下几乎必然带 1px 抖动，不设死区会让「点一下海报」也触发重画。
- **二维码大小可选档位**（`qrSize`：0 自动 / 76 小 / 104 中 / 140 大）：
  自动 = 按内容高度算（内容越多码越小），中档 104 正是改造前自动算出的上限——
  没动过设置的人，选「自动」和选「中」看到的是同一张图，升级不会惊吓。
  大档给「扫得到最重要」的场景（贴群里、打印），320 宽画布上占 44%。
  两个实现要点：
  - **指定档位严格生效**：`clamp(qrSize, QR_MIN, 140)`，不再按 `fitSize` 二次夹。
    内容多时码会盖住最后一两行，由白底衬保证可扫——用户点「大」就该变大，
    「点了没反应」比「盖住一行地址」糟得多。详见下面那条踩坑记录。
  - **0 是合法值，不能当「没选」**：`pickQrSize` 不能写成 `if (!size) return`
    （头像档位可以那么写，因为头像没有「自动」），否则点「自动」毫无反应。
  - 档位文案与尺寸绑在 `QR_OPTIONS` 里（`{size,label}`），wxml 直接渲染 `item.label`，
    不在模板里套三元——加档位只改一处。
- **选「自动」时二维码尺寸随内容高度自适应**（内容越多码越小），默认位置下方留 32px 给「长按识别名片」；
  提示文字**跟着码走**，码被拖到画布上方时提示不会孤零零留在底部。
- **码的上下可拖区间是对称的**（都只留 `EDGE_MARGIN`），提示文字另算：
  这里踩过一个很隐蔽的坑——下界曾经扣掉「提示占用的 32px」，而默认位置
  （码底 = `QR_BOTTOM`）正好用满这 32px，于是 `qrYMax` **恒为 0**：
  「下移」永远按不动、码永远贴不到底边，而上界只受 `EDGE_MARGIN` 约束能一路顶到画布顶。
  用户看到的就是「往上能溢出、往下卡死」。现在允许贴到底边，
  提示放不下时**干脆不画**（`hint.show=false`）——夹到 `H-6` 会压在码上，
  半个「长按识别名片」比没有更糟。
- **二维码底下无条件垫一块圆角白底**（四边各内缩 5px）：深色素材上不垫会糊成一片；
  码放大后还可能压住最后一行文字，不垫就变成「字从码缝里透出来」，扫码成功率直接掉。
  浅底上白衬融进白底、肉眼看不出来，所以不必再按 `geo.light` 分支。
- **档位严格生效，不被内容高度二次夹紧**：这里连踩过两次坑，值得记下来。
  第一版把「指定尺寸」也按 `min(140, fitSize)` 夹，内容一多 `fitSize` 掉到 100，
  选「中」和选「大」都是 100，看起来像没反应；第二版加「内容超高就整体上移给码腾地方」，
  上移把 `baseBottom` 压到 `QR_BOTTOM - QR_MIN - QR_GAP`（358），`fitSize` 恒等于 56，
  于是 `clamp(qrSize, 56, min(140, 56))` 对**小 / 中 / 大一律返回 56**，彻底失效。
  现在的规则很简单：**用户选什么就是什么**，内容放不下时码会盖住最后一两行，
  由白底衬保证码本身清晰可扫；想两全的人会自己关掉几个元素或把码选小一档。
  「自动」仍然按内容高度算（内容越多码越小），这条不变。
- **选择记忆两层**：本机 `posterBg:<staffId>` / `posterLayout:<staffId>` 保证秒开且离线可用；
  登录状态下再同步到后端（`GET/PUT /api/employee/poster-prefs`）跟随账号跨设备。
  后端返回 `exists=false` 表示「没存过」，此时以本机缓存为准——否则换新账号第一次进页面，
  手挑的背景和调好的版式会被服务端默认值冲掉。
- **「原样」的本地存储写哨兵 `none` 而不是空串**：`wx.getStorageSync` 对「键不存在」和
  「存的是空串」都返回 `''`，不区分开就分不清「用户明确选了白底」和「这人从没选过」。
- **兜底优先于新功能**：清单拉不到（老后端 / 断流）、背景图加载失败（域名未配
  `downloadFile` 白名单是小程序最常见的一坑）、上次选的素材已下架或被删，几种情况
  都退化成白底海报继续出图，绝不卡在「生成中」。
- **小程序码取不到必须显式暴露，不能静默留白**：码是整张海报唯一的入口，
  悄悄画一张没有码的图发出去等于发一张废纸。最常见原因是后端没配
  `wx_appid` / `wx_appsecret`（`GET /api/wechat/wxacode` 直接回 `code:1`）。
  处理：海报上画虚线占位框写「小程序码 / 未生成」、**不再**印「长按识别名片」
  （码都没有还提示长按识别是骗人），页面顶部给一条黄条 + 「重试」按钮
  （`retryQr` 只重取码，不必把背景和版式一起重新加载；成功后会清掉图片缓存，
  因为同一 staffId 的码对象名固定，URL 可能不变而内容已刷新）。
- 换背景 / 改版式都会整张重画，故必须先清掉 `tempFilePath` 再重走导出——否则
  「保存/分享」拿到的还是上一版的图，且不报错。
- 版式计算（`computeGeometry`）是纯函数：先算出每个元素的位置、二维码能有多大，
  再交给 `drawContent` 画。拆开是因为二维码尺寸要随内容高度自适应（内容越多码越小）、
  还要被用户选的档位覆盖并二次夹紧，合在一起算就只能靠肉眼调参；拆开后单测能直接断言坐标。


### 海报的分享闭环（裂变主路径）
海报是「生成 → 分享 → 好友看海报 → 扫海报码进来记为 `poster` 来源」这一串的载体，
**只做「保存到相册」等于把闭环断在最关键的一步**（发出去全靠用户自己想起来去相册里找），
故海报页有两个分享出口：

- **「发送给好友」按钮**：`wx.showShareImageMenu({ path })` 直接把画好的图发给好友 / 群
  （基础库 **2.14.3** 起支持，官方文档）。先用 `wx.canIUse('showShareImageMenu')` 探测，
  低版本**降级提示「先保存到相册再从相册发」**——不探测直接调用会抛 unknown api，
  表现为点了按钮毫无反应（用户只会当成 bug）。
  分享的图片消息默认带小程序入口（`needShowEntrance`，3.8.2+ 默认 true），
  好友点图片即可进海报页，闭环下一段自然续上。
- **右上角「转发」**（`onShareAppMessage`）：转发的是**海报页本身**（`?staffId=`），
  好友打开即看到同一张海报，可继续保存或转发。

两条出口都在发起时计一次 `share`（targetId=0，名片级，与首页转发同一把尺子）。
口径说明：微信对 `showShareImageMenu` 只回「接口调用成功」（弹窗打开即 success），
**不告知用户是否真的发出去**，所以按「发起分享」计数，与 `onShareAppMessage` 一致，
不追求确认送达；用户取消（fail）不计。

### 加载 / 失败 / 重试
海报是「取数据 → 画 canvas → 导出图片」三步串联，任一步挂了都必须落到失败态并给
「重新生成」入口——否则用户只看到一块白板，既不知道在加载也不知道失败了，只能退出重进。
要点：

- `Promise.all` **必须 catch**（此前没有，断网时 reject 冒泡出 async 函数，页面永远空白）；
- `draw()` 取不到 canvas 节点、以及 `canvasToTempFilePath` 导出失败，都同样进失败态，
  不能静默 `return` / 空 `fail` 回调；
- 覆盖层（`.poster-mask`）叠在 canvas 上而**不用 `wx:if` 销毁 canvas**：节点销毁重建后
  `createSelectorQuery` 可能取不到刚渲染的节点，重试反而更画不出来；
- 重试时先清空 `tempFilePath`，否则导出失败后按钮仍拿着上一版海报，用户拿到旧图且不报错。
- 通过小程序码扫码进入时，`index.onLoad` 解析 `query.scene`（形如 `staffId=12`）定位到对应员工名片。
- 后端需配置 `WX_APPID` / `WX_APPSECRET`（环境变量）；未配置时接口返回明确错误。
  企业独立版按租户取凭据，未配该企业那份时静默回落到平台全局那一份（见后端 README「小程序码」的三级回落链）。

### 首屏为什么快（三条性能约束，2026-11-04）

用户反馈「打开生成海报页很慢」，实测下来是三件事叠在一起，改完都加了回归用例：

1. **海报码曾经每请求都重新生成**（后端 `/api/wechat/wxacode?from=poster` 不进缓存）：
   一次 miss = 调微信 + 写对象存储 + 刷 CDN 三个同步外网往返，全部压在 `prepare()` 的
   `Promise.all` 上——海报**一张都还没开始画**就先等它。改用「staffId + 对象名后缀」分键后
   海报码也能进缓存，TTL 内是内存命中。端上不需要改，但**别为了「拿最新的码」在端上
   加随机参数**：那等于把这条缓存彻底废掉，一次不落全走微信。
2. **三张图并行预载**（`preload`）：原来是 onload 套 onload（背景 → 头像 → 码），
   真机上就是三个连续 RTT。现在并行发起、全部落定后一次性绘制，总等待≈最慢那张。
   拖动时三张都在 `_imgs`，`loadCached` 同步回缓存，每帧时序与改造前一致。
   回归用例 `三张图并行预载` 把「图片何时回来」交给用例控制——串行实现下只会发起 1 个请求。
3. **背景条必须挂 `lazy-load`**：这一条横滑里有 59 张内置素材 + 最多 9 张自己的图，
   不懒加载就是进页面瞬间并发几十个图片请求，把画布真正需要的背景/头像/码挤到队尾。
   `poster.spec.ts` 有一条静态守门，删掉属性会红（它不报错、不变丑，只悄悄变慢）。

仍然存在的一段等待（不可优化，只能解释）：**每个员工每 30 天内第一次**进海报页时，
微信那边要真生成一次码，那一次必然慢一点；之后都走缓存。

### 进小程序要等多久（裸启动的那次多余往返，2026-11-04）

反馈是「进入小程序时总是卡在这个界面很久」，截图里那个界面是名片墙
（`pages/team/team`，小程序的 `entryPagePath`）。关键在于：**它不是终点，是中转站**。

企业配了「默认进某位员工的名片」时，裸启动（扫体验版二维码 / 直接打开）走的是：

```
冷启动 → 名片墙 onLoad → 等 /api/team（才知道要不要改道）→ reLaunch → 名片页再等 /api/card
                          └─ 弱网 1~3 秒，访客就盯着这一页 ─┘
```

两段串行往返。第二段没法省（要看名片就得拿名片数据），**第一段完全可以省**——
那个「要不要改道」的答案几乎不变。

修法是 `utils/entry.ts`：把上次的结论记在本机，下次裸启动直接进名片页。

| 边界 | 为什么 |
|---|---|
| 缓存键带 `companyId` | 平台模式下本机可能粘滞着不同企业（扫 A 家码再扫 B 家码）。不分键 = 把 A 的默认员工套到 B 头上，点进去是别人家公司 |
| 24 小时过期 | 管理员换人 / 员工停用后，缓存要能自己退场 |
| **每次凭缓存跳都要回后台核对** | 缓存只负责「省掉那一次往返」，**谁的名片该被打开始终由后台说了算**。见下条 |
| **失效自愈**（`fromEntryCache=1` → `skipEntryCache=1`） | 过期是慢兜底（最迟一天），自愈是快兜底（当场）。名片页发现那位员工不存在时清缓存并退回名片墙；名片墙收到 `skipEntryCache=1` 就**不再改道**，直接渲染列表——不再改道这一条是防死循环的关键，否则后端仍说「该跳」时两个页面会来回弹 |

**「以后台为准」是怎么做到的**：`/api/card` 的 `company` 里本来就有
`mpEntryMode` / `mpEntryStaffId`（后台「企业管理」配的那两个字段），
所以名片页拿到名片数据时**顺手就能核对，不需要额外请求，也不用改后端**。
凭缓存跳进来（带 `fromEntryCache=1`）时比对当前 `staffId` 与后台配置：

| 后台怎么说 | 怎么做 |
|---|---|
| `mode=staff` 且就是当前这位 | 什么都不做，正常渲染 |
| `mode=staff` 但换成了别人 | 立刻改道到新的那位（并清缓存） |
| 取消了默认入口 / 那位员工已不可用 | 回名片墙按最新配置重新决定（并清缓存） |

核对**只在 `fromEntryCache` 为真时生效**：扫码进来看某个人时，`staffId` 本来就不是
默认入口，这时去「纠正」会把访客从他要看的人那儿弹走。这条有反向用例钉着。

顺带修掉同一现场的两个观感问题：

- **页面抬头初始值不再用产品名**。原来 `title` 初值是 `FALLBACK_BRAND`（「微传名片」），
  于是弱网下访客看到「微传名片 + 加载中」——自家小程序顶着平台名，像没配置好。
  现在初始为空，抬头整块不渲染；只有**加载完仍拿不到企业名**时才回落到品牌名。
- **加载中不给搜索框**。一个还搜不了的输入框帮不上忙，只会诱使访客去点。

> 别把 `/api/team` 的缓存做成「整页数据缓存」来提速：那会把员工名单（含姓名/头像）
> 落到访客本机，且名单变化后要处理陈旧数据。这里只缓存「一个整数 + 时间戳」。

### 节日 / 节气日自动选背景（节日优先）
进入海报页时**先判节日、再判节气**：当天命中哪个，就自动选中哪张内置背景图，并把它
排到背景条第一位；缩略图底部有一条名字角标说明原因（节日朱红、节气墨黑，不读字也分得清）。
两者都没命中时与改造前完全一致（默认商务款 `poster-brand`），不会出现「十月还在发立春图」。

- 实现在 `utils/festival.ts` + `utils/solarTerm.ts`，两张压缩表都不引入农历库。
- **节日优先于节气**：同一天既是节日又是节气时用节日那张。2020–2040 有 14 处撞期
  （如 2026-03-20 龙抬头撞春分、2038-02-04 春节撞立春），正月初四用户想看到的是
  「春节」而不是「立春」。
- **农历节日优先于公历节日**：也有 8 处撞期（2033-02-14 元宵撞情人节、
  2037-02-14 除夕撞情人节）。传统节日文化指向更明确，除夕那天显然不该出情人节海报。
- 节日覆盖 19 个：农历 10 个（除夕/腊八/小年/春节/元宵/龙抬头/端午/七夕/中秋/重阳）
  + 公历 9 个固定日（元旦/情人节/女神节/劳动节/儿童节/教师节/国庆/平安夜/圣诞）。
  清明与冬至**故意不登记**——它们既是节气也是节日，但 `poster-qingming` /
  `poster-dongzhi` 已由节气链路自动选中，重复登记会有两个真相源。
  生日海报也不自动选：后端没有生日字段，猜不出来。
- **逐级回落**：节日图不在清单里（老后端没有新节日图 / 运营下架）时退到节气图，
  再不行才落商务默认款。不能因为缺一张节日图就连节气图都不给了。
- 以下规则与节气版完全一致：用户自己的选择优先；「存的正好是默认商务款」算没选过；
  自动选中的那张**不落库**（`savePrefsNow` 在 `bgAuto` 时写默认款）；
  用户一点背景 `bgAuto` 即清零。
- 表覆盖 2020–2040；表外年份的农历节日不猜不插值，公历节日不依赖表、照样生效。
- 日期可信度：农历表由 solarlunar 的 `lunar2solar` 离线生成，210 个日期逐一
  `solar2lunar` 反算，农历月日全部对得上；2026 年春节 2/17、小年 2/10（北方，腊月廿三）
  与公开日历一致。**闰月里的同名日不是节日**（2023-03-23 是闰二月初二，不是龙抬头），
  生成时 `isLeapMonth` 固定为 false 即不会取到闰月。

### 节气日自动选背景（节日之外的兜底）
当天正好是节气日、且不是节日日时，走节气那条链路，规则同上。

- 实现在 `utils/solarTerm.ts`：一张「每年 24 个几号」的压缩表（21 年约 1.2KB），
  按公历年月日判等，不引入农历库。
- 24 个节气都有对应素材（`poster-xiaohan` … `poster-dongzhi`，见素材 manifest）；
  节气图不在清单里（素材调整 / 老后端）时原样返回，不插空位。
- **用户自己的选择优先**：存过别的背景、或明确选过「原样」，都不自动选；
  但排序仍然生效——当天是节气，那张就在最前。
  「存的正好是默认商务款」算没选过（默认款是兜底值而不是审美表态），
  否则第一次在节气日调下版式就会把偏好固化成默认款，之后每个节气日都不再自动换了。
- **自动选中的那张不落库**：`savePrefsNow` 在 `bgAuto` 时写默认款而不是节气 id。
  否则用户在节气日只调了下版式，PUT 就把 bgId 一起写走，一个月后进页面还在发上个月的图。
  用户一点背景（`pickBg` / 上传 / 删除）`bgAuto` 即清零。
- 表覆盖 2020–2040；表外年份返回 null（不猜不插值），表现退化为「不自动选」，
  用户仍可手动挑。到期后按 `solarTerm.ts` 头注释里的两条来源重新生表。
- 日期可信度：表由 chinese-days（寿星公式 + 例外校正）导出，并用 Meeus
  《Astronomical Algorithms》的太阳视黄经公式独立复算，2020–2040 共 504 个节气日
  两者完全一致。残差风险只有「交割时刻落在北京时间午夜前后一两小时内」这一种
  （约每年两三个），该区间内没有临界个例；2045 小暑（23:57）即为一例，已通过把表截到 2040 避开。

## 海报页首屏耗时：把「两波串行」压成一波（2026-11-04）

进海报页的等待是**两波串行**，这才是「还是慢」的真正来源：

```
Wave1  4 个接口并行（/api/card、小程序码、背景清单、偏好设置）    ≈ 0.5s
Wave2  背景 + 头像 + 二维码三张图并行（preload 三张齐了才开画）   ≈ 1.0s
```

Wave2 必须等 Wave1 回来才知道图片地址，于是用户的实际等待 = 两波相加。

线上实测（同一连接复用，已排除 TCP/TLS 握手）：

| 请求 | 体积 | 耗时 |
|---|---|---|
| `/api/card` | 1.1 KB | 0.46 s |
| `/api/assets/posters` | 10.1 KB | 0.46 s |
| `/api/wechat/wxacode`（命中进程内缓存） | 57 B | 0.47 s |
| `/api/wechat/wxacode`（未命中：调微信+写对象存储+刷 CDN） | 57 B | 1.6 s |
| 头像 PNG | 3.4 KB | 0.78 s |
| 背景原图 | 29.6 KB | 1.10 s |

**3.4 KB 与 29.6 KB 相差 8 倍体积、耗时只差 0.3 秒——耗时由「一次网络往返」
决定，与体积几乎无关。** 这条结论直接否掉了三种想当然的优化：

- **把海报打进小程序包**：59 张原图合计 **2136 KB**，主包上限 2 MB，塞不下
  （分包单个也是 2 MB，且分包首次进入要下更多字节，反而更慢）；
- **打进缩略图**：缩略图只有 **320×460**，而画布逻辑尺寸 320×460，按 `dpr`
  放大到 **640×920** 才清晰——用缩略图必然糊，海报是要存相册发群里的；
- **只打包默认那张**：`preload` 要三张**全到齐**才 `cb`，背景变成本地文件后
  画布仍在等头像和二维码那两次往返，关键路径一秒都省不下来。

唯一有效的办法是**让 Wave2 不必等 Wave1**：`utils/postersnap.ts` 把上次出图
用到的三个地址（背景 / 头像 / 二维码）存进本地 storage，下次进页面在 `onLoad`
就拿着它们起跑（预热进 `loadCached` 的内存缓存），与 Wave1 并行；接口回来后
`draw()` 直接从缓存取图，Wave2 被完全吸收。

四条边界，缺一条都会出事：

1. **只记地址、不记内容**。真正画什么永远以接口返回为准，快照只决定「提前下
   哪三张」。所以快照全错的最坏结果是白跑一次下载，绝不会画出错的海报。
2. **按员工分键**。同一台手机可能看不同同事的名片，串了就是预热别人的头像。
3. **只认 http(s) 绝对地址**。相对路径喂给 `canvas.createImage` **不报错、不
   回调**，却会把 3 秒失败锁写进去，让 `draw()` 半秒后跳过同一地址——比不预热更糟。
4. **预热失败必须清掉失败锁**。同样的道理：`loadCached` 的 3 秒失败锁本来是给
   「拖动时每帧重试不打网络」兜底的，预热这一次不该占坑，否则一次网络抖动就会
   让整张海报的背景变成白块。

配套约束：

- `warmFromSnap()` 必须排在 `this.prepare()` **之前**——顺序反了不报错，但图片
  请求会排在四个接口后面出门，等于白做（有静态守门用例钉住）。
- 离屏 canvas 只是拿一个能 `createImage` 的壳（`wx.createOffscreenCanvas`，
  基础库 2.16.1+），不画任何东西；取不到就静默跳过，那些环境只是没变快。
- 换背景 / 上传自定义背景 / 重取小程序码都要**同步更新快照**，否则下次预热的
  还是旧地址，白跑一次下载。
- 首次进入（无快照）行为与改造前完全一致，不退化。

## 多租户与企业独立版（第二期）

小程序按企业分发，`utils/tenant.ts` 是唯一决策点，两种部署形态：

1. **企业独立小程序版本**（`tenantConfig.pinnedCompanyId` 设为企业 id，在 PC 后台「企业管理」查看编号）：
   整个小程序锁定为该企业的独立版本——内容 / 申请 / 分享恒带该 companyId，
   且**不读任何外部来源**（扫码 scene、分享参数、Storage 残留都不能改写），
   不存在切换多租户的行为，呈现为「该企业专属小程序」。
   > 锁定值可由后台下发，无需改代码重新提审：在 PC 后台「系统设置 → 小程序 →
   > `pinned_company_id`」填企业 id，小程序冷启动拉 `/api/public/settings` 覆盖
   > （见 `utils/config.ts`）。缺省 0。
2. **平台模式**（`pinned_company_id = 0`，默认）：
   访客扫企业专属小程序码（scene 携带 `companyId=N`，或分享链接带 `companyId`）
   首次进入时绑定该企业并写入 Storage 粘滞，之后本机所有入口（直接打开 / 分享）均落该企业；
   无任何来源时不传 companyId，由后端缺省落 1 号租户（演示租户）。

机制说明：
- `request.ts` 对内容类 GET（`/api/achievements|services|projects|home-layout|company`）与申请 POST（`/api/apply/open`）自动注入 companyId；`/api/card` 按员工归属企业取，`/api/employee/*`、登录刷新等与租户无关，一律不注入。
- `app.ts` `onLaunch` 与 `index.onLoad` 都会调 `bindCompanyFromScene`（幂等，覆盖冷启动与热启动两条路径）。
- 申请页（`pages/apply/open`）顶部展示「申请加入：XX公司」（`GET /api/company`），访客可确认申请目标；申请由该企业的管理员在 PC 后台审核。
- 员工登录态与名片内容始终按员工归属企业走（token / staffId 维度），不受本模块影响。
- 后端公开接口的 `companyId` 参数、企业停用拦截（`code=1`）与两级后台见 `backend/README.md`。

## 运行
1. 微信开发者工具 → 导入项目 → 选择 `miniprogram/` 目录（appid 可用测试号）。
2. **基址按运行环境自动切换**（见 `utils/config.ts` 的 `resolveBaseUrl()`）：微信开发者工具（`envVersion='develop'`）自动用 `DEV_BASE_URL`（默认 `http://127.0.0.1:8080`，需在开发者工具勾选「不校验合法域名」，`project.config.json` 已设 `urlCheck:false`）；体验版 / 正式版（`trial`/`release`）用 `DEFAULT_BASE_URL`（生产域名 `https://weichuan.ziyuanxingqiu.cn`）。**无需再手动改代码**；临时想让 develop 连别的地址，可在开发者工具 Console 执行 `wx.setStorageSync('mpBaseUrlOverride', 'https://xxx')`（删除该键或写空串即恢复自动）。生产基址也可由 PC 后台「系统设置 → 小程序 → `miniprogram_base_url`」下发覆盖（目标域名仍需是已配置的 request 合法域名）。
3. 生产需在小程序后台配置 request 合法域名（https）。

## 本地联调（推荐：免 MySQL 的 devserver）
后端在 `backend/cmd/devserver` 提供了一个纯 SQLite（免 cgo）的开发态服务，复用与生产**完全一致**的路由装配，启动即自动 seed 演示数据，无需 MySQL / Docker 即可一键联调：
```bash
cd backend
go run ./cmd/devserver          # 监听 :8080（可用 PORT 环境变量覆盖）
```
内置演示数据：平台超管 `admin / admin123456`；演示员工 手机 `13832603521` / 密码 `weichuan123`（高立新，业务经理，staffId=1）；公司信息、7 个业绩分类、多个项目案例、4 个业务服务、首页布局。
小程序侧联调步骤：
1. 微信开发者工具导入 `miniprogram/`（测试号 appid，`urlCheck:false` 已开）。
2. 无需改代码：微信开发者工具模拟器（`envVersion='develop'`）会自动走 `DEV_BASE_URL=http://127.0.0.1:8080` 连本机 devserver（`project.config.json` 已设 `urlCheck:false`）；真机调试若需连本机，把 `DEV_BASE_URL` 改成电脑局域网 IP（同网段、关防火墙）。体验版 / 正式版恒走生产 `DEFAULT_BASE_URL`，互不影响。
3. 首页直接打开会弹「输入员工编号」，输入 `1` 进入演示员工名片；或经 PC 后台生成的分享链接 / 小程序码进入（scene=`staffId=1`）。
4. 员工登录页用 `13832603521 / weichuan123` 登录，可改头像（上传走 `/api/employee/upload`）、提交名片修改（待平台/企业管理员审核）。
5. 多租户联调：平台超管在 PC 后台「企业管理」开通企业并建企业管理员 → 企业管理员登录管理自己企业的内容 → 小程序申请页填 `companyId` 或用 `?companyId=` 访问公开接口验证隔离（见「多租户与企业独立版」）。

> 接口约定：登录返回 `token` 在**根级**（`{code:0, token}`），`login.ts` 按 `r.token` 读取（与 PC 后台 `adminLogin` 一致）；其余资源在 `data` 下。视频号 `<channel-video>` 需真机 + 已绑定视频号的小程序 AppID 才能渲染，测试号下该块留空、`company-video` 自动回退封面/MP4；小程序码接口需配置 `WX_APPID/WX_APPSECRET` 才返回真实码，未配置时返回 `code:1`（联调预期内）。

## 技术要点
- **富文本视频块组件 `rich-video`**：抽出的可复用组件，按 `type` 三来源渲染（视频号 `<channel-video>` / 腾讯视频 `<txv-video>` / MP4 直链 `<video>`，画幅与 playerid 见上）。视频号档内部按优先级三分支——`feed-id`（同主体，可自动播放）→ `feed-token`（跨主体，强制不能自动播放）→ `finder-user-name`（兼容存量富文本，只展示视频号主页；历史块里只存了视频号 ID，直接丢弃会让内容整块消失）。详情页（`pages/project/detail`、`pages/service/detail`）在富文本分段后，遇到视频号段改用 `<rich-video feedId="{{item.feedId}}" channelId="{{item.channelId}}" />` 渲染，不再内联 `<channel-video>`，避免页面各自重复条件判断。
- **富文本 + 视频号混排**：`utils/richtext.ts` 用正则 `/<wx-video\s+([^>]*?)\/?>(\s*<\/wx-video>)?/g` 整段捕获属性后再按名提取 `feed-id` / `feed-token` / `channel-id`（属性不再写死进正则，顺序无关，**成对写法时闭合标签一并消费**，不会漏进后续 html 片段），拆分 `<wx-video feed-id="…" feed-token="…" channel-id="sph_…">`；三个属性都为空的标签不拆分，留给 `rich-text` 原样渲染。视频号段用 `rich-video` 组件、其余用 `rich-text`，保证图文/视频号位置正确（对应需求难点）。PC 富文本编辑器插入视频号时调用 `buildWxVideo(feedId, channelId, feedToken)` 统一输出 `<wx-video feed-id="${feedId}" feed-token="${feedToken}" channel-id="${channelId}"></wx-video>`（缺失属性省略），保证前后端片段规则一致。
- **视频号 ID 语义**：视频号 ID 以 **`sph` 开头**（`gh_` 是公众号 ID，早期注释写错了，勿沿用）。`<channel-video>` 的 `finder-user-name` 只用于展示视频号主页，**播放指定视频必须用 `feed-id`**：公司简介档位 `channels` 的 `feedId` 来自视频号助手 → 动态管理 → 复制 feedId，且要求视频号与小程序**同主体**（跨主体需改用 `feed-token`，且强制不能自动播放）。
- **公司简介视频多来源**：`components/company-video` 按后端 `profile.videoSource` 选档——`channels` 渲染 `<channel-video>`（**同主体用 `feed-id`、跨主体用 `feed-token`**，`feed-id` 优先）；`upload` / `url` 渲染 `<video src=videoUrl>`；其余回封面占位。选档是**显式的**（不做隐式自动降级链），只有视频号主源 `bind:error` 且后台开了 `videoFallbackUrl` 时才回落到 `videoUrl`。跨主体必须走 `feed-token`：官方明确 `feed-id`「仅同主体时生效」，平台模式下客户视频号与我们小程序必然不同主体，代价是强制 `autoplay=false`。`tencent`（腾讯视频插件）是预留档：插件必须在 `app.json` 静态声明，统一声明会让未通过插件授权的客户小程序直接报「插件未授权」，故默认既不声明也不渲染。
- **员工 token**：登录后存 `wx.storage('empToken')`（access，15 分钟）+ `empRefreshToken`（refresh，30 天），`request.ts` 自动注入；`code=401` 先用 refresh 令牌静默换发并重试一次，失败再清 token 引导重新登录。改密 / 被禁用会令旧令牌立即失效。（小程序走 Bearer 头鉴权；PC 管理后台则改用 httpOnly Cookie，见前端 README。）
- **状态语义**：员工 `status=disabled` 时首页整页提示「已停用」；名片修改走「提交申请 → 超管审核通过后公开生效」。

## 组件开发规范与样式自查清单

所有组件均开启 `addGlobalClass: true`，组件目录内**没有独立 wxss**，样式统一写在 `app.wxss`。由此带来一条必须遵守的约束（踩过真实线上样式 bug）：

1. **组件内禁用裸通用类名**：`cell` / `row` / `label` / `val` / `name` / `title` / `info` / `head` / `avatar` / `logo` 等，一律加组件前缀（如 employee-card 用 `ec-`）。
   - 原因：全局 `app.wxss` 里任何同名独立类（例如给「我的」页写的 `.cell { border-bottom: 1rpx solid #f0f0f0 }`）都会命中组件内部元素，泄漏出凭空多出的边框 / 边距，且症状与业务代码无关、极难排查。
   - 组件自身根类名（`.emp-card`、`.cat-grid` 等）属于项目约定，写在 `app.wxss` 中不受此限。
2. **分隔线 / 装饰线用真实元素**（`<view class="xx-line" />`）而非 `::before` 伪元素，结构在 WXML 可见，便于比对与排查。
3. **新增组件或全局类后跑一次审计**（秒级，无需开发者工具）：

```bash
python3 miniprogram/tests/audit_class_collision.py
```

脚本会做两件事：① 找出「被组件与其他页面/组件同时使用」的全局独立类（真正的跨模块污染，应为 0）；② 列出组件内残留的通用字类名（未来风险提示）。

### 排查「预览里凭空多出的线 / 边距」的方法
截图肉眼判断不可靠，直接用像素扫描定位：取截图逐行找**连续横向线段**，输出其起止 x、宽度占比与主色——据此即可反查是哪个元素、什么颜色、多宽（例：曾据此定位到 `#f0f0f0`、宽 50% 的线来自全局 `.cell` 的 `border-bottom`，而非组件代码）。

## 名片墙分页与搜索（2026-10-30）

改之前：`GET /api/team` 一次拉全员，后端上限 300。企业超过 300 人时访客只能看到前 300 位，
而且**没有任何入口看到剩下的人**——页面底部只有一句「仅显示前 300 位」。
500 人以上的企业等于不能用，这是产品上的硬伤，不是性能优化。

现在拆成两层：

| 页面 | 职责 |
|---|---|
| `pages/team/team` | 公司抬头 + 搜索框 + **首屏一页 60** + 「查看全部 N 位同事」（`hasMore` 时出现） |
| `pages/team/all` | 搜索框（吸顶）+ 宫格 + `onReachBottom` 续页 + 底部「加载中 / 重试 / 已经到底了」 |

四条约定：

- **`hasMore` 以后端为准**，不要用 `total - list.length` 推：搜索时 `total` 是匹配总数、`data` 是当前页，
  两者相减得到的是「还没加载的匹配数」，语义不同。后端回包里有 `hasMore` 就直接用。
- **首屏只拉 60**：大企业不必等 300 条 JSON 解析完才出画面。要看全部就进 `pages/team/all`。
- **输入不发请求**：`keyword`（输入框的值）与 `activeKeyword`（已提交给接口的值）分开存，
  只有按键盘「搜索」键 / 清空时才 `setData({ activeKeyword })` 并重查。否则每敲一个字打一次接口，
  既费流量又会让宫格不停闪烁。
- **续页三件套**：防重入（`loadingMore` 锁，惯性滚动会连触发 `onReachBottom`）、
  按 `staffId` 去重（翻页期间管理员增删员工会让同一人出现在相邻两页）、
  失败不清空（续页失败只标 `moreError` 在底部给重试，已经看到的人不能消失）。

搜索走的是**后端内存过滤**，不是 SQL：姓名是 PII 密文列（`model.Employee.NameEnc`），
SQL 层只有盲索引能做等值匹配、做不了 `LIKE '张%'`，而名片墙的搜索必须支持「张」这种前缀。
所以带关键字时后端把该企业全员解密后过滤再分页（上限 5000 人，见
`repository.MaxTeamSearchScan`）。不要"优化"回 SQL LIKE——那样搜不到任何前缀。

样式：`pages/team/all.wxss` 用 `@import "./team.wxss"` 复用落地页的宫格样式，
不复制一份（复制出来的两份迟早会漂移）。

## 图片失败兜底

图挂了不会报错，只会安静地留一块白或裂图——而挂掉的往往是第一眼位置（名片头像、企业
LOGO、首页背景、案例封面）。所以**所有 `<image>` 都必须挂 `binderror`**，由
`tests/img_fallback.spec.ts` 扫全部 `.wxml` 守着，新增页面漏写会直接红。

两种处理器，按「这张图有没有原图可退」二选一：

| 场景 | 处理器 | data 绑定 | 失败后 |
|---|---|---|---|
| 列表 / 宫格（有 `_thumb` 兄弟对象） | `onThumbError` | `data-arr` + `data-idx` + `data-key` + `data-full` | 改回原图 |
| 单图 / 图集（没有可退的原图） | `onImgFail` | `data-fail` + `data-src`（图集再加 `data-idx`） | 换成同尺寸占位 |

`onImgFail` 记的是**失败的 URL**，WXML 用 `src && xxxFail !== src` 判断还要不要画：

```wxml
<image src="{{me.avatar}}" mode="aspectFill" wx:if="{{me.avatar && avatarFail !== me.avatar}}"
  binderror="onImgFail" data-fail="avatarFail" data-src="{{me.avatar}}" />
<view class="avatar" wx:else />
```

为什么不记布尔标记：布尔标记必须在每次换图时手动清掉（换头像、下拉刷新换了地址、图集
重排……），漏一处就是「换了新头像却永远不显示」这种极难查的 bug。记 URL 天然自愈——地址
一变就重新尝试，同一张坏图也不会无限重试；图集按下标记时若列表增删导致下标错位，比较的是
URL，错位只会让标记失效（多画一次图），不会把别的图误藏起来。

占位形态按容器自己的底色来，不额外加样式：`.ec-avatar` / `.album-img` / `.bg-thumb` /
`.avatar-ph` 都自带背景，失败时露出的就是同尺寸的灰/浅蓝块，布局不塌、也不跳。

表单页（`apply/open`、`employee/edit`）的占位文案区分两种状态——`{{form.avatar ? '加载失败'
: '未设置'}}`。都写「未设置」会让用户以为刚才选图白选了。

## 下拉刷新

9 个内容页支持下拉重拉（页面 JSON 开 `enablePullDownRefresh` + TS 实现 `onPullDownRefresh`）：名片墙、查看全部同事、名片首页、项目列表、项目详情、服务详情、我的、数据统计、个人信息。

三条约定：

- **收尾必须走 `finally`**：`onPullDownRefresh` 里写成 `try { await load({ silent: true }) } catch {} finally { wx.stopPullDownRefresh() }`。少写 `finally`，加载失败时顶部指示器会一直转，用户以为小程序卡死；少写 `catch`，`stat` / `mydata` 这类没有内部 try/catch 的加载函数会把网络异常抛成未捕获的 Promise rejection。
- **`silent` 不闪整页 loading**：各页 `load(opts?: { silent?: boolean })` 在 silent 时不置 `loading: true`，只清 `error`。否则下拉瞬间内容被「加载中…」替换，比不刷新还难受。首次进入与点「重新加载」仍走非 silent。
- **编辑态页面不加**：`apply/open`（申请表单）、`employee/edit`（资料编辑）、`employee/login`、`poster`（海报 canvas）、`privacy`（静态政策）。下拉会打断输入或打乱 canvas。

`project/list` 下拉时分类与列表一起重拉（分类可能被后台增删，只刷列表会留下失效的切换条）。名片首页的访问埋点不必额外处理——`trackVisit` 按 staffId 去重，同一张名片只记一次。

用例见 `tests/pulldown.spec.ts`（26 例）：逐页钉「注册了处理函数 / 触发正确的加载函数与入参 / 加载抛错也收指示器」，外加 team 上的 silent 精确断言。

## 单元测试（经前端 Vitest 运行）

`tests/` 收录小程序**纯逻辑**单测（不依赖微信开发者工具 / 真机）。小程序侧没有独立测试工具链，统一由前端工程的 Vitest 纳管（`frontend/vitest.config.ts` 通过 `server.fs.allow` 放行仓库根外路径 + `vi.resetModules()` + 动态 import），并对 `getApp` / `wx` 全局做桩：

- `tests/richtext.spec.ts`：富文本与视频号拆分——自闭合 `<wx-video ... />`、成对 `<wx-video ...></wx-video>`（闭合标签必须一并消费，不能漏进后续 html 片段）、属性顺序无关、无视频号时整体作为一段 html、含 `channel-id` 缺失时不误判等。
- `tests/request.spec.ts`：`wx.request` 封装——自动注入 `Authorization: Bearer`、`code=0` 直接 resolve、`code=401` 先用 refresh 令牌静默换发并重试一次（成功则放行）、刷新失败清 token 并 reject、`code!=0` 业务错误直接 reject 等。采用「同步挂起 + `respond(i, v)` 显式应答」模式驱动 Promise 链，不依赖真实计时器。
- `tests/tenant.spec.ts`：多租户解析——平台模式扫码 scene / 分享 query 绑定并粘滞、无来源缺省 0、非法值拦截、`resetCompanyBinding` 清绑定；企业独立版恒返回锁定值且拒绝一切外部绑定（scene / query / Storage 残留）。
- `tests/poster.spec.ts`（114 例）：海报页——`staffId` / 扫码 **scene** 解析（含混参与无参归 0）、头像与小程序码转绝对 URL、**保存成功才计 `poster_save`**（失败不计）、**分享图片计 `share` + 低版本降级 + 取消不计**、**取数 / 取不到 canvas / 导出失败三态与重试（重试先清旧图）**；**海报背景**：默认款选择、清单接口失败退化白底、切换背景记住选择并重画、切回「原样」（存哨兵 `none`）、上次选择沿用与已下架回落、背景图加载失败兜底；**自定义背景**：上传成功加入列表并选中、上传返回非 JSON 提示失败、未登录拦在相册前、删除背景（含删掉当前选中项要回落默认款）、自定义背景叠渐变蒙层而内置素材不叠；**小程序码缺失**：`qrError` 置位并透出后端原因、无码时画占位框且不印「长按识别名片」、`retryQr` 成功后可恢复；**工具条**：默认落在背景、白名单外的 key 被忽略；**版式**：模板切换重画与白名单、元素显隐反转、**姓名关掉后职位升为标题行**、位移到边界即停（不再有骗人的数字）、头像三档、**二维码四档大小（0 是自动且不能被当成「没选」、**内容多时档位照样生效不被压回小码**、内容少时「大」就是 140 且底边钉住往上长）**、PUT 同步（含原始 file 路径不被重复拼 baseUrl）、未登录只存本地；**自定义文案**：输入时只重画不导出、失焦才落库并导出、画在电话下方且带 accent、清空后这一行消失；**拖动**：位移跟随手指、拖动中不导出松手才导出、**拖到画布边缘字也不被裁掉**、**可拖范围远大于原来的 ±16（按包围盒算）**、**反向拖动无死区**、**拖二维码只挪码不动信息区**、码拖不出画布、2px 死区、**内容与码各自复位（复位一个不会顺手挪另一个）**、**码的按钮微调不动内容区**、加载中不响应；**码的上下界对称**（能贴到底边、贴底时提示不画、向上仍能顶到上沿）、**元素开关**（座机邮箱默认关、打开才出现、关码后 `size=0` 且位移区间归零）、**八套模板都能排出画布内且码不小于扫码下限**、**大字/画报带主色分隔线**、**九项全开时内容仍在画布内、自动档缩到最小可用尺寸**；**纯函数**：`normalizeLayout` / `normalizeCustomText`（压平空白 / 剔控制字符 / 按码点截断）/ `resolveBgId` 三态 / `nearestAvatarSize` / `nearestQrSize`（等距取较小档）/ `shiftRange`，以及 `computeGeometry` 的坐标断言（内置素材从 150 起、自定义从 24 起、内容越多二维码越小且不重叠、配色 auto 跟随背景、内容与码各自独立位移、提示文字跟着码走）。**性能守门**：三张图并行预载（串行实现下只会发起 1 个请求，用例直接红）、背景条每张图都带 `lazy-load`（静态扫 wxml）。选它是因为 scene 解析与分享 API 差异只在真机上暴露，本地开发永远复现不了。
- `tests/track.spec.ts`：埋点入口的 targetId / staffId / 白名单 / 失败静默，以及 `source` 场景推导（含 `from=poster` 的 URL 解码）。
- `tests/team.spec.ts`：**名片墙**（裸启动落地页）——四种状态分开（有内容 / 公司没人 / 企业不可用 / 网络失败且能重试）、无头像用姓名首字占位、点某人跳名片页**必须带 `companyId`**（不传会落到平台缺省的 1 号企业，点进去是别家公司的人）、接口回传的 `companyId` 优先于 query、企业信息为空时标题回落品牌名、转发落点回到名片墙、企业默认入口为 `staff` 时**改道该员工名片且不渲染宫格**；**分页与搜索**（2026-10-30）：首屏只拉一页 60、`hasMore` 以后端为准、输入时不发请求、确认搜索才带 `keyword` 从第一页重查、关键字没变不重复请求、清空恢复全员、搜索态下不改道、进「查看全部」带 `companyId` 与 encode 过的关键字。
- `tests/team_all.spec.ts`：**查看全部同事**（名片墙第二层）——滚动加载第 2 页并追加、**防重入**（请求在途时连滚只发一次）、后端 `hasMore=false` 时不再请求、**按 staffId 去重**（翻页期间增删员工会让同一人出现在相邻两页）、越界页码用后端回传值、**续页失败保留已加载列表**（只在底部标可重试，不能整页清空）、点重试能续上、带关键字进页面直接按关键字查、换关键字清空旧列表、下拉刷新成败都收指示器。
- `tests/login_wechat.spec.ts`（15 例）：**微信一键登录**——成功时带 `code` + `companyId` 换令牌并存 `refreshToken`、未绑定（code=3）弹 modal 说清「先密码登录 → 我的页绑定」且不跳转不存令牌、`wx.login` 拿不到 code 时不发请求、其它业务失败用后端文案、网络异常不卡 loading、连点只发一次；**手机号密码**断网时 `submitting` 也要落下来（原来没 catch，按钮会永远转圈）；**绑定微信**——`wechatBound` 落到 data、未登录归位 false、未绑定能绑、已绑定不发请求只说明怎么换、绑定失败用 modal 说全原因、在途连点只发一次。
- `tests/img_fallback.spec.ts`：**图片失败兜底**——扫全部 `.wxml` 断言每个 `<image>` 都挂了 `binderror`、且带 `data-fail` 或 `data-arr`（漏写会安静地留一块白）；`onImgFail` 的单图 / 图集 / `idx=0` / 缺参数 / 字段还不是数组五种形态；`onThumbError` 的「缩略图挂了改回原图」与「已在原图上不再 setData」（后者防的是 binderror 无限循环）。
- `tests/subscribe.spec.ts`（9 例）：**审核结果订阅消息**——模板 ID 预取后缓存不重复请求、未配置时不弹窗、`tmplIds` 传数组、预取失败**不缓存空串**（缓存了会让整个会话都收不到通知且用户无从察觉）、用户拒绝不抛错不重试；**页面接线**：申请页「先请求订阅、再发提交请求」的顺序断言（顺序反了微信直接拒授权）、拒绝订阅提交照样发出、后端没配模板时既不弹窗也不拦提交、编辑页同样在提交前订阅。
- `tests/index_home.spec.ts`：名片首页四请求拼装、`/api/card` 根级字段（误读会恒走 notFound）、失败不停在「加载中」、访问埋点去重与**时序兜底**（onShow 早于 staffId 就绪时由 loadAll 补报）、以及**裸启动不再弹「输入员工编号」而是 reLaunch 到名片墙**。
- `tests/admin_request.spec.ts`（7 例）：**管理员请求通道**——带的是 `adminToken` 而不是 `empToken`、GET 参数拼 query、**不自动注入 companyId**（租户隔离靠 JWT）、`401` 时用 `adminRefreshToken` 单飞刷新并重试一次（并发只刷一次）、刷新失败彻底清令牌并把 401 交回页面、网关兜底页（HTML）按网络错误 reject、`saveAdminAuth` 拒绝空令牌、**微信绑定标记独立于令牌**（401 只清令牌、主动退出才清绑定）。
- `tests/admin_entry.spec.ts`（18 例）：**管理员入口决策层**——升级后首次允许一次探测、探测失败的设备此后不再打登录接口（反向钉住限流误伤）、已有令牌直达审核页、60s 冷却且失败也记账、空 code 不发请求、业务失败 / 网络异常 / 存令牌失败都静默返回 false、静默成功直达审核页且 loading 必收起、静默失败安静落登录页、主动退出把探测时间写成当前值而非清 0（否则退出按钮失效）；静态守门钉住「审核页绝不出现 `/api/admin/login/wechat`」「WXML 在 `ready` 前不渲染列表」「『我的』页不再无条件 navigateTo 审核页」。
- `tests/admin_apply.spec.ts`（20 例）：**企业审核页**——有令牌置 `ready` 后拉列表、无令牌当帧同步跳登录页且零请求（不再先闪列表）、审核页不消费任何微信登录响应、退出走 `endAdminSession`、列表 401 清令牌跳登录、开通申请卡片时间格式化与联系方式行、修改申请 snapshot 解析（头像显示「已更换」、清空显示「（清空）」）、切页签/切筛选重置到第一页并带对参数、`hasMore` 按 total 判定、接口失败落到可重试错误态而非空列表、通过开通申请必须把**员工编号与初始密码**弹出来、取消确认不发请求、驳回理由原样进请求体、驳回失败保留弹窗与理由；静态守门另钉住审核页只用 `adminRequest` 且不含 `wxLoginCode`。
- `tests/admin_login.spec.ts`（16 例）：**管理员登录页**——空账号密码只提示不发请求、密码错误原样透出后端文案、登录成功存令牌→绑微信→**标记可静默免密**→进审核页、绑定失败不阻断登录且绝不标成已绑定、密码登录撞 429 说「请稍后再试」、免密成功标记已绑定、`code:3` 说清先账号密码登录、**`code:429` 不再套「免密登录不可用」**、其他失败回归「登录失败」、`onLoad` 一次性静默（成功直达 / 失败静默 / 未过门槛零请求 / checking 期间两按钮都不响应）、已有令牌直接跳审核页；静态守门钉住按钮宽度 / 居中 / `nowrap` 与双按钮在 checking 期间一起禁用。
- `tests/postersnap.spec.ts`（12 例）：**海报图片地址快照**——按员工分键、TTL 边界（等于 TTL 仍有效）、时钟回拨按过期、非绝对地址一律丢弃（相对路径会让 `createImage` 静默失败并占住失败锁）、三项全空时不落库、`staffId<=0` 不读不写、脏数据当没有、`at` 缺失当没有、`clear` 自愈、写存储抛异常不影响调用方；海报页侧的接线另有 7 例（`onLoad` 当场起跑且早于 `prepare()`、无快照不建离屏 canvas、基础库不支持不抛、**预热失败不留 3 秒失败锁**、出图后写快照、换背景后快照跟着换、静态守门钉住调用顺序）。

```bash
cd frontend && npm test        # 前端 + 小程序 spec 一并运行
```

> 视图层 / 页面渲染不在覆盖范围（需微信 CLI 或真机自动化），由联调步骤保证。

## 待补（相对完整产品）
- 真实视频号 feed-id 映射（需正式 AppID + 已绑定视频号，代码链路已就绪）。
