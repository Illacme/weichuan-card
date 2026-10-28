# 微传名片 · 微信小程序前端（原生 + TS）骨架

按《首页组件与接口字段映射》《跳转页组件与接口字段映射》两份文档落地的前端代码骨架。后端见 `backend/`。

## 目录
```
miniprogram/
├── app.json / app.ts / app.wxss     # 全局配置 / 入口 / 全局样式
├── project.config.json / tsconfig.json / sitemap.json
├── utils/
│   ├── request.ts      # wx.request 封装（自动注入 empToken、统一 code 处理）
│   ├── tenant.ts       # 多租户：企业独立版锁定（pinnedCompanyId）/ 平台模式扫码分发与粘滞
│   ├── types.ts        # 与后端 JSON 对齐的 TS 类型
│   ├── richtext.ts     # <wx-video> 富文本拆分（视频号块 ↔ channel-video）
│   ├── wechat.ts       # 微信身份：wx.login 换 openid / getUserProfile 授权昵称
│   ├── privacy.ts      # 微信隐私授权：查 getPrivacySetting / 需否判定 / 等待与结算 / ensurePrivacy(page)
│   ├── contact.ts      # 保存名片到通讯录（员工名片区 / 底部栏共用）
│   └── track.ts        # 埋点上报：8 类事件统一入口（白名单就近丢弃，失败静默）
├── components/         # 8 个可复用组件（均开启 addGlobalClass）
│   ├── employee-card / company-video / achievement-grid
│   ├── featured-projects / service-grid / action-bar
│   ├── rich-video      # 视频号渲染控件：有 feedId 用 <channel-video feed-id>，否则回退 finder-user-name
│   └── privacy-consent # 微信隐私授权弹窗（同意按钮用 open-type="agreePrivacyAuthorization"）
├── pages/
│   ├── team/           # 名片墙（**裸启动落地页**：公司抬头 + 全员宫格，点谁看谁；企业把默认入口配成「指定员工」时这里只做一次 reLaunch 改道）
│   ├── index/          # 名片首页（自定义顶栏含「我的」入口 + 员工卡 + 公司简介卡片 + 业绩 + 服务；底部 tabBar 已移除）
│   ├── project/list/   # 项目列表（按分类）
│   ├── project/detail/ # 项目详情（富文本+视频号拆分+相册）
│   ├── service/detail/ # 服务详情（富文本+视频号拆分）
│   ├── poster/         # 分享海报（canvas 合成 + 发送给好友 / 保存到相册）
│   ├── privacy/        # 隐私政策页（应用内可查阅，审核硬性要求）
│   └── employee/       # 员工登录 login + 名片修改 edit（提交审核）
└── tests/              # 纯逻辑单元测试（richtext 拆分 / request 刷新重试 / 隐私授权编排）
```

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
- 涉及隐私接口的位置：`apply/open`（chooseMedia / getUserProfile）、
  `employee/edit`（chooseMedia）、`poster`（chooseMedia / saveImageToPhotosAlbum）、
  `employee-card`（setClipboardData）、`utils/contact.ts`（addPhoneContact）。
- 平台侧未声明对应隐私类型时**接口会被直接禁用**（`errno: 112`）——
  声明清单见 `docs/deploy-runbook.md` §8。

## 与后端接口对应
| 页面 | 接口 |
|---|---|
| index | GET /api/card, /api/achievements, /api/services, /api/home-layout, POST /api/stat/track(visit / share) |
| project/list | GET /api/projects?categoryId= |
| project/detail | GET /api/projects/:id + POST /api/stat/track(project_view / share，targetId=案例ID) |
| service/detail | GET /api/services/:id + POST /api/stat/track(service_view / share，targetId=服务ID) |
| employee/login → edit | POST /api/employee/login, GET /api/employee/me, PUT /api/employee/me, POST /api/employee/upload(头像) |
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

## 员工头像上传
- 员工修改页 `pages/employee/edit` 的头像由「URL 文本输入」改为「选图上传」：用 `wx.chooseMedia` 选图，经 `wx.uploadFile` 调后端 `POST /api/employee/upload`（员工鉴权，复用同一 storage.Uploader），返回 URL 写回 `form.avatar`，再随 `PUT /api/employee/me` 提交审核。与 PC 端 `UploadField` 调用 `/api/admin/upload` 同一思路、同一存储层，仅鉴权角色不同。

## 首页背景图（商业化美化包装，可选）
- PC 后台「小程序设置 → 名片首页样式」可设置首页背景图（支持从内置素材库「背景」分类直接选用 750×1334 竖幅素材），并可调节遮罩浓度 0~100。后端经 `GET /api/home-layout` 在**响应根级**下发 `bgImage` / `bgMask`（与 `order` 同级）。
- 首页 `pages/index` 收到非空 `bgImage` 时：铺一层全屏背景（`mode="aspectFill"`），其上叠 `bgMask/100` 透明度的黑色蒙层，白色内容卡片浮于其上形成层次；`bgImage` 为空即保持默认灰底布局，不渲染背景层。
- 背景层与蒙层用 `position: fixed` + 负 `z-index` 铺在最底层，内容按常规流绘制天然在其上方，不会互相遮挡；有背景时顶栏改为半透明白、板块卡片加圆角阴影（`.has-bg`）。

## 腾讯视频档（`company-video` 的 txv 分支）

`company-video` 按后端 `videoSource` 选档：`channels` → `<channel-video>`（feedId / feed-token）、
`upload` / `url` → `<video>`、`tencent` → 腾讯视频插件的 `<txv-video vid>`、`none` → 封面占位。
主源 `binderror` 且后台开了 `videoFallbackUrl` 并填了 MP4 时，回落 `<video>`；没开兜底则回落封面占位。

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

**内容来源硬约束**：插件**只播放您自己腾讯视频创作号上传的视频**，不支持他人视频或任意 URL。vid 必须来自运营方自己的创作号；创作号上传时分类别选「文化 / 历史」可能导致无法播放全片，宣传视频建议选「企业 / 商业」类。

已知形态（插件自带，界面改不了）：免费版片头有广告；`txv-video` 的 `z-index` 为 11，
自定义浮层要压在它上面时记得抬高。

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
- **视频号渲染控件 `rich-video`**：抽出的可复用组件，内部按优先级三分支渲染 `<channel-video>`——`feed-id`（同主体，可自动播放）→ `feed-token`（跨主体，强制不能自动播放）→ `finder-user-name`（兼容存量富文本，只展示视频号主页；历史块里只存了视频号 ID，直接丢弃会让内容整块消失）。详情页（`pages/project/detail`、`pages/service/detail`）在富文本分段后，遇到视频号段改用 `<rich-video feedId="{{item.feedId}}" channelId="{{item.channelId}}" />` 渲染，不再内联 `<channel-video>`，避免页面各自重复条件判断。
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

## 单元测试（经前端 Vitest 运行）

`tests/` 收录小程序**纯逻辑**单测（不依赖微信开发者工具 / 真机）。小程序侧没有独立测试工具链，统一由前端工程的 Vitest 纳管（`frontend/vitest.config.ts` 通过 `server.fs.allow` 放行仓库根外路径 + `vi.resetModules()` + 动态 import），并对 `getApp` / `wx` 全局做桩：

- `tests/richtext.spec.ts`：富文本与视频号拆分——自闭合 `<wx-video ... />`、成对 `<wx-video ...></wx-video>`（闭合标签必须一并消费，不能漏进后续 html 片段）、属性顺序无关、无视频号时整体作为一段 html、含 `channel-id` 缺失时不误判等。
- `tests/request.spec.ts`：`wx.request` 封装——自动注入 `Authorization: Bearer`、`code=0` 直接 resolve、`code=401` 先用 refresh 令牌静默换发并重试一次（成功则放行）、刷新失败清 token 并 reject、`code!=0` 业务错误直接 reject 等。采用「同步挂起 + `respond(i, v)` 显式应答」模式驱动 Promise 链，不依赖真实计时器。
- `tests/tenant.spec.ts`：多租户解析——平台模式扫码 scene / 分享 query 绑定并粘滞、无来源缺省 0、非法值拦截、`resetCompanyBinding` 清绑定；企业独立版恒返回锁定值且拒绝一切外部绑定（scene / query / Storage 残留）。
- `tests/poster.spec.ts`（82 例）：海报页——`staffId` / 扫码 **scene** 解析（含混参与无参归 0）、头像与小程序码转绝对 URL、**保存成功才计 `poster_save`**（失败不计）、**分享图片计 `share` + 低版本降级 + 取消不计**、**取数 / 取不到 canvas / 导出失败三态与重试（重试先清旧图）**；**海报背景**：默认款选择、清单接口失败退化白底、切换背景记住选择并重画、切回「原样」（存哨兵 `none`）、上次选择沿用与已下架回落、背景图加载失败兜底；**自定义背景**：上传成功加入列表并选中、上传返回非 JSON 提示失败、未登录拦在相册前、删除背景（含删掉当前选中项要回落默认款）、自定义背景叠渐变蒙层而内置素材不叠；**小程序码缺失**：`qrError` 置位并透出后端原因、无码时画占位框且不印「长按识别名片」、`retryQr` 成功后可恢复；**工具条**：默认落在背景、白名单外的 key 被忽略；**版式**：模板切换重画与白名单、元素显隐反转、**姓名关掉后职位升为标题行**、位移到边界即停（不再有骗人的数字）、头像三档、**二维码四档大小（0 是自动且不能被当成「没选」、**内容多时档位照样生效不被压回小码**、内容少时「大」就是 140 且底边钉住往上长）**、PUT 同步（含原始 file 路径不被重复拼 baseUrl）、未登录只存本地；**自定义文案**：输入时只重画不导出、失焦才落库并导出、画在电话下方且带 accent、清空后这一行消失；**拖动**：位移跟随手指、拖动中不导出松手才导出、**拖到画布边缘字也不被裁掉**、**可拖范围远大于原来的 ±16（按包围盒算）**、**反向拖动无死区**、**拖二维码只挪码不动信息区**、码拖不出画布、2px 死区、**内容与码各自复位（复位一个不会顺手挪另一个）**、**码的按钮微调不动内容区**、加载中不响应；**码的上下界对称**（能贴到底边、贴底时提示不画、向上仍能顶到上沿）、**元素开关**（座机邮箱默认关、打开才出现、关码后 `size=0` 且位移区间归零）、**八套模板都能排出画布内且码不小于扫码下限**、**大字/画报带主色分隔线**、**九项全开时内容仍在画布内、自动档缩到最小可用尺寸**；**纯函数**：`normalizeLayout` / `normalizeCustomText`（压平空白 / 剔控制字符 / 按码点截断）/ `resolveBgId` 三态 / `nearestAvatarSize` / `nearestQrSize`（等距取较小档）/ `shiftRange`，以及 `computeGeometry` 的坐标断言（内置素材从 150 起、自定义从 24 起、内容越多二维码越小且不重叠、配色 auto 跟随背景、内容与码各自独立位移、提示文字跟着码走）。选它是因为 scene 解析与分享 API 差异只在真机上暴露，本地开发永远复现不了。
- `tests/track.spec.ts`：埋点入口的 targetId / staffId / 白名单 / 失败静默，以及 `source` 场景推导（含 `from=poster` 的 URL 解码）。
- `tests/team.spec.ts`：**名片墙**（裸启动落地页）——四种状态分开（有内容 / 公司没人 / 企业不可用 / 网络失败且能重试）、无头像用姓名首字占位、人数超上限时 `truncated` 置位、点某人跳名片页**必须带 `companyId`**（不传会落到平台缺省的 1 号企业，点进去是别家公司的人）、接口回传的 `companyId` 优先于 query、企业信息为空时标题回落品牌名、转发落点回到名片墙、企业默认入口为 `staff` 时**改道该员工名片且不渲染宫格**。
- `tests/index_home.spec.ts`：名片首页四请求拼装、`/api/card` 根级字段（误读会恒走 notFound）、失败不停在「加载中」、访问埋点去重与**时序兜底**（onShow 早于 staffId 就绪时由 loadAll 补报）、以及**裸启动不再弹「输入员工编号」而是 reLaunch 到名片墙**。

```bash
cd frontend && npm test        # 前端 + 小程序 spec 一并运行
```

> 视图层 / 页面渲染不在覆盖范围（需微信 CLI 或真机自动化），由联调步骤保证。

## 待补（相对完整产品）
- 真实视频号 feed-id 映射、图片懒加载/失败占位细化、下拉刷新等体验项。
