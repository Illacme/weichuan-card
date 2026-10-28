# 微传名片 PC 管理后台 · Vue3 前端

Vue3 + Element Plus 的 PC 管理端（超管使用）。对接《三端联调与接口总表》中全部 `admin` 接口：登录 / 开通审核 / 名片修改审核 / 员工管理 / 企业内容 CRUD / 上传 / CDN 刷新 / 超管改密。

> 小程序端（员工侧）在 `../miniprogram`，后端在 `../backend`。

## 技术栈

- Vue 3.5（`<script setup>`）+ TypeScript
- Vite 5（开发代理 `/api` → 后端 8080）
- Element Plus 2.8 + 图标
- Pinia（登录态）+ Vue Router 4（路由守卫）

## 目录结构

```
frontend/
├── index.html
├── package.json
├── vite.config.ts            # /api 代理到后端 8080
├── tsconfig.json / tsconfig.node.json
├── env.d.ts
└── src/
    ├── main.ts               # 入口：挂载 Pinia / Router / Element Plus
    ├── App.vue
    ├── api/
    │   ├── request.ts        # axios 实例 + 统一响应拦截（code=401 跳登录 / 403 提示 / 非0 报错）
    │   ├── auth.ts           # 登录 / 改密
    │   ├── apply.ts          # 开通审核 / 名片修改审核
    │   ├── employees.ts      # 员工管理
    │   ├── content.ts        # 公司信息/简介/分类/项目/服务/首页布局
    │   ├── storage.ts        # CDN 刷新 / 私有桶签名
    │   ├── tenants.ts        # 企业管理 / 企业管理员 / 租户小程序凭据（含 releaseEnabled 代发开关）
    │   ├── mpRelease.ts      # 小程序版本发布：登记 / 提审 / 查审核状态 / 发布 / 撤回
    │   └── assets.ts         # 开源素材库：清单 / 采用 / 预览图 URL
    ├── store/auth.ts         # Pinia 登录态（持久化 localStorage）
    ├── router/index.ts       # 路由表 + 路由守卫
    ├── layout/AdminLayout.vue # 侧边菜单 + 顶栏（改密/退出）
    ├── components/
    │   ├── UploadField.vue   # 通用上传组件（直传预签名 + 回退普通上传 + 素材库入口）
    │   ├── AssetPicker.vue   # 开源素材库选择器（分类 tab + 风格行 + 搜索 + 分页 + 单选/多选采用）
    │   ├── MPReleaseDialog.vue  # 小程序版本发布弹窗（按状态机显隐按钮 + 驳回原因 + 二次确认）
    │   └── ChangePasswordDialog.vue
    └── views/
        ├── Login.vue
        ├── ApplyOpen.vue     # 开通审核（通过弹初始密码 / 驳回）
        ├── ApplyModify.vue   # 名片修改审核（snapshot 旧值→新值对比）
        ├── Employees.vue     # 员工管理（新增/启用停用/重置密码）
        └── content/
            ├── CompanyInfo.vue
            ├── CompanyProfile.vue
            ├── Categories.vue
            ├── Projects.vue   # 富文本+视频号块 / 封面+相册 / 精选+上架
            ├── Services.vue
            ├── HomeLayout.vue
            └── CdnRefresh.vue
```

## 本地运行

```bash
cd frontend
npm install
npm run dev        # 启动开发服务器（默认 http://localhost:5173）
```

- 开发期 `vite.config.ts` 已将 `/api` 与 `/uploads` 代理到 `http://127.0.0.1:8080`；  
  如需指向其他后端地址，用 `VITE_API_BASE=http://x.x.x.x:8080/api npm run dev`  
  （代理目标自动由 `VITE_API_BASE` 去掉末尾 `/api` 推导，无需再设 `VITE_BACKEND_URL`）。
- 生产构建：`npm run build`（产物在 `dist/`，交由 Nginx 托管，API 走反向代理或同源）。

## 登录与鉴权

- 超管账号来自后端 `ADMIN_USER/ADMIN_PASS`（默认 `admin/admin123456`，**上线必改**），首启动由后端 seed。
- **httpOnly Cookie 鉴权**：登录成功后服务端写入 `access_token` / `refresh_token` 两个 `HttpOnly; SameSite=Lax` Cookie，axios 以 `withCredentials` 随请求自动携带，**JS 全程不接触令牌**（防 XSS 窃取）。前端仅在 localStorage 存非敏感的 `admin_user` / `admin_loggedin`，用于路由守卫与界面展示。
- 后端鉴权失败一律返回 HTTP 200 + `{ code: 401 | 403, msg }`（见 `backend/internal/middleware/auth.go`），因此前端以 `res.code` 为准。
- **静默刷新**：遇到 `401` 时，响应拦截先调 `/api/refresh`（凭 refresh Cookie，服务端轮换下发新 Cookie）并自动重试原请求一次（并发 401 用单飞锁合并，`__skipRefresh` 防递归）；刷新失败才清登录态跳登录。403 提示无权限。
- **即时吊销**：改密 / 管理员重置密码 / 禁用会使后端 `token_version` 自增，旧 Cookie 立即失效；超管改密后前端自动登出并回登录页。
- **登出**：`AdminLayout` 调 `POST /api/logout` 清 Cookie 后清本地登录态；路由守卫据 `loggedIn` 标记跳转（令牌在 Cookie 中，JS 以非敏感标记判断）。

## 上传组件 UploadField

封装 `el-upload`，优先直传预签名（`GET /api/admin/upload/presign` → `PUT putUrl` 到对象存储），不支持时回退 `POST /api/admin/upload` 普通上传（local 驱动）。成功后通过 `v-model` 回传可访问 URL，用于封面 / LOGO / 头像 / 项目相册。

```vue
<UploadField v-model="form.cover" :multiple="false" accept="image/*" />
<UploadField v-model="form.album" :multiple="true" :limit="9" accept="image/*" />
```

`form.cover` 为 `string`，`form.album` 为 `string[]`。

## 开源素材库（减少找图/处理图成本）

所有需要图片的表单（业绩分类图标、业务服务图标、项目封面/相册、公司简介封面、公司信息 LOGO、员工头像、**小程序首页背景**）在 `UploadField` 上方都提供「从素材库选择」入口，点击弹出 `AssetPicker.vue` 选择器：**分类 tab**（全部 + 图标/封面/LOGO/头像/背景）+ **风格行**（简约清晰/温暖自然/轻奢尊贵/柔美清新/光影流动）+ 关键字搜索 + 卡片网格 + **底部分页器**，**单选即采用并回填，多选累计后批量添加**（超出 `limit` 自动截断并提示）。

- **两个筛选维度正交**：分类 tab = "用在哪"，风格行 = "什么气质"。风格行只渲染当前条件下 `count>0` 的档位（服务端回传 `styleCounts`），只有一档可选时整行隐藏——例如图标分类下视觉语言统一，不会摆一排点不出东西的筛选项。已选调性在新条件下无素材时（如切分类、搜索收窄）自动回落「全部」，不会停在空结果页。
- 素材清单走**服务端分页**：`GET /api/admin/assets/library?page=&size=&type=&style=&keyword=`，返回 `{items,total,page,size,pages,styleCounts}`；翻页 / 切分类 / 切风格 / 搜索（300ms 防抖）都会重新请求并重置到第 1 页，已选中的素材跨页保留。
- 当前内置 **915 项**（图标 187 图形 × 4 档配色 = 748 / 封面 52 含建筑主题 / LOGO 40 / 头像 24 / 背景 51 含浅蓝素雅系），预览图走公开只读端点 `GET /api/assets/file/*path`（immutable 长缓存）。
- 采用走 `POST /api/admin/assets/adopt`：后端把内嵌素材复制进当前上传存储，返回与手动上传**完全同构**的 URL，后续展示/CDN 链路零差异。
- 按位置传入 `library-type`（`icon` / `cover` / `logo` / `avatar` / `background`）控制默认定位的分类；素材资产与许可证见后端 `internal/assets/library/`（Lucide 图标 ISC 许可 + 程序化生成图形）。

```vue

<UploadField v-model="form.icon" library-type="icon" :multiple="false" />
<UploadField v-model="form.cover" library-type="cover" :multiple="false" />
<UploadField v-model="layout.bgImage" library-type="background" :multiple="false" />
```

## 企业内容管理说明

| 页面   | 接口                                          | 备注                                             |
| ---- | ------------------------------------------- | ---------------------------------------------- |
| 公司信息 | `GET/PUT /api/admin/company-info`           | id=1 单行，PUT 自动建首行                              |
| 公司简介 | `GET/PUT /api/admin/company-profile`        | 文字 + 视频号 ID + 降级 MP4 + 封面                      |
| 业绩分类 | `GET/POST/PUT/DELETE /api/admin/categories` | 删除强校验：锁定或存在关联项目拦截                              |
| 项目案例 | `GET/POST/PUT/DELETE /api/admin/projects`   | 详情含 `<wx-video channel-id>` 视频号块；相册为 URL 数组    |
| 业务服务 | `GET/POST/PUT/DELETE /api/admin/services`   | content 含视频号块                                  |
| 小程序设置 | `GET/PUT /api/admin/mp-entry`<br>`GET/PUT /api/admin/home-layout` | 一页两页签，回答同一个问题「客户打开小程序看到什么」：<br>· **访客先看到谁**——裸启动落到名片墙还是指定员工的名片<br>· **名片首页样式**——板块顺序 + **首页背景图**（可从素材库「背景」分类直接选用）+ 遮罩浓度与效果预览<br>两者接口独立、各自保存；`el-tab-pane` 带 `lazy`（未激活页签不挂载，否则进页面打双份接口）。旧路径 `/content/home-layout`、`/content/mp-entry` 重定向到对应页签 |

> 富文本编辑器（项目案例.详情 / 业务服务.内容）已接入 wangEditor v5 可视化编辑，并注册「视频号块」自定义节点；详见下方「富文本编辑器（wangEditor）」。

## 平台管理说明（仅平台超级管理员）

| 页面     | 接口                                    | 备注                                                   |
| ------ | ------------------------------------- | ---------------------------------------------------- |
| 企业管理   | `GET/POST/PUT /api/admin/companies` 等 | 多租户：开通/停用企业、企业管理员管理、企业简称；企业管理员访问会被后端 403 拒绝          |
| 版本发布   | `/api/admin/companies/:id/releases` 等 | 代租户提审 / 发布小程序版本；入口在企业管理行「版本」按钮，详见下节 |
| CDN 刷新 | `POST /api/admin/cdn/refresh`         | 平台级基础设施操作，需启用刷新并选好厂商（阿里云 / 腾讯云 / 七牛云 / Cloudflare / AWS CloudFront）且填全该厂商要求的凭据；覆盖同名对象（素材采用 / 小程序码重生成）已自动刷新，此处为手动兜底 |

## 小程序版本发布（企业管理 →「版本」）

`src/components/MPReleaseDialog.vue` + `src/api/mpRelease.ts`。**上传代码有两条路**：
后台自己传到微信（`source=backend`，不需要任何人装微信开发者工具），或 CI 传完后由后台登记
版本号（`source=ci` / `manual`）。之后都是**提交审核 → 查审核状态 → 发布**，外加一个撤回。

- **「上传代码」与「登记版本」的分工**：登记只是记一笔，版本号写错了要到提审才暴露；
  上传是后台真的把源码传上去，成功后由后端按同一个版本号自动建 draft 记录，
  从根上杜绝「填的号」和「传的号」不是同一个。
- **打开上传弹窗先探活**（`getMPReleaseUploadReadiness`）：缺 node / 没装 miniprogram-ci /
  源码目录不在 / 没配上传密钥，一次列全摆在表单上方，没清完禁用「开始上传」——
  而不是让人干等几分钟再报错。上传是**同步执行、分钟级**，界面上也写明了勿关闭页面。
- **按钮显隐完全跟状态机走**（后端白名单是最终裁判，前端只是少让人点错）：
  `draft` 出「提交审核」；`auditing` 出「查状态 / 撤回」；`audit_passed` 出「查状态 / 发布」；
  `rejected` / `withdrawn` 出「重新提审」；`released` 是终态，行内只有一个不可点的「已上线」。
- **三条不可逆代价都做成了二次确认**，且文案说清后果而不是「确定吗？」：
  重新提社会再扣一次审核额度、撤回不返还已扣额度、发布对全部用户立即生效。
  撤回在后端还要求 `confirm=true` 才执行，双保险。
- **驳回原因单独一列 + 危险色**：被驳回的版本要能一眼看到为什么，否则运营只会反复点
  「重新提审」然后再次被同一个理由驳回。
- **查状态回包区分 `changed`**：微信状态变了才提示「状态已更新」，仍在审核中则提示
  「微信仍在审核中，稍后再查」——不把「没变」渲染成一次成功提示。
- **租户代发开关与上传密钥**都在「凭据」弹窗里（`el-switch`，`releaseEnabled`）：
  关掉后代发动作后端一律 403，列表仍可读（企业要能看见历史）；省略该字段保存时保持原值，
  不会被静默重置。**上传密钥与 AppSecret 是两把不同的钥匙**（前者传代码、后者换 token 调
  提审/发布），同样只写不读——接口只回 `uploadKeyConfigured`，留空保存即清空并改用平台全局那把，
  保存前会弹一次点名「要清掉什么」的确认（两个密钥合并成一次，不连弹两窗）。
- 失败时保留旧数据不清空表格：一次网络抖动不该让人以为版本记录没了。

## 列表分页 / 搜索

六个列表页（开通审核 / 名片修改审核 / 员工管理 / 业绩分类 / 项目案例 / 业务服务）均接入了统一的 `src/composables/useTable.ts` 组合式函数，提供：

- **关键字搜索**：顶部搜索框，按各页核心字段本地筛选（如员工按姓名/手机、项目按名称 + 分类下拉）。
- **分页**：`el-pagination`，默认每页 10 条，可切 10/20/50；切换关键字/分类时自动回到第一页。

> 采用**前端分页 + 搜索**（对已在内存中的列表做筛选/切片），与《三端联调与接口总表》§2.3「当前版本列表接口不分页（数据量小，运营型后台可接受）」的定位一致，无需改动后端接口契约。若日后数据量显著增长，可平滑改为服务端分页（后端加 `?page&size&keyword` 并返回 `{list,total}`），前端仅替换 `useTable` 的数据源即可。

## 名片统计页（Stats）

`src/views/Stats.vue` + `StatTrend.vue` + `StatDetail.vue`，一层比一层细。

- **信息架构（方案A 同页分区折叠）**：常驻首屏只放工具条 + 转化三卡（访问 / 拨号 / 保存）+ 趋势图，保住「转化 vs 内容浏览」同屏对比与下钻闭环；内容浏览与裂变（三卡 + 两卡 + TOP 双榜）、流量来源与裂变、按员工表各是一个可折叠分区，默认收起、点分区标题展开（移动端即手风琴）。首屏减负但不打断分析链路，也不拆 Tab（拆 Tab 会丢失跨区对比与下钻闭环）。
- **汇总卡**：转化三卡（访问 / 拨号 / 保存）+ **内容浏览三卡**（案例浏览 / 服务浏览 / 视频播放）+ **裂变两卡**（转发 / 海报保存）。两组分开列、**不相加**：内容浏览答「访客在看什么」（带 targetId，进 TOP 榜），裂变答「名片被传出去多少」（不带 targetId，主体可能是员工本人）。
- **趋势图**：一次拿八项指标，**图例可点选**（默认只画转化三线——八条全开会糊成一团），
  勾掉大值系列后纵轴按激活系列重算（否则剩下的线被压成贴底直线）；点某天下钻当天明细。
- **内容浏览 TOP 榜**：案例 / 服务两张榜，按 `target_id` 聚合，点某行下钻该对象的明细
  （预选 `eventType` + `targetId`，抽屉里是可关闭的「对象」筛选标签）。
- **流量来源与裂变（折叠分区，迁移 0022）**：「来源分布」按归一后的 `source` 分组
  （扫码 / 转发卡片 / 搜索 / 海报 / 未知 / 其他），条形宽度 = 占本范围访问数的比例，
  回答「访客从哪来」；「裂变榜」是 `source='share_card'` 的 visit 按员工聚合的排行，
  回答「谁的名片被转发带来的新访客最多」。只统计 `visit` 事件。
  后端未跑迁移 0022 的老副本不回这两项时按空数组兜底（不渲染空分区、也不报 undefined）。
- **员工表**：转化三列 + 「内容浏览」（三类之和）+ 「裂变」（转发+海报之和），各自悬浮看拆分、可排序——
  每组合成一列而不是逐类铺开，避免表格被推到 1200px+ 需要横向滚动；细分走行「明细」下钻。
  两列**不相加**（口径见上）。
- **口径一致性**：明细与汇总同日期范围、同租户（抽屉不自带日期选择器，跟随主页），
  TOP 榜对象删除后显示「已删除」但计数不减，避免「汇总 100、明细 105」这类对账事故。

## 富文本编辑器（wangEditor）

`项目案例.详情` 与 `业务服务.内容` 使用可视化富文本编辑器 `src/components/RichEditor.vue`（基于 wangEditor v5 + `@wangeditor/editor-for-vue`），替换了原先的纯 textarea。

- 工具栏开箱即用（加粗/标题/列表/图片/链接等），并通过 `excludeKeys` 排除了自带视频上传。
- **视频号块节点**：通过 `src/editor/wxVideoModule.ts` 注册了自定义元素 `wxVideo`，编辑区以绿色占位块显示（视频号图标 + 标识），保存时序列化为 `<wx-video feed-id="…" feed-token="…" channel-id="sph_…"></wx-video>`（缺失属性省略），与小程序端 `rich-video` 组件约定一致；回显时 `<wx-video>` 标签会被解析还原为编辑区节点。三个属性的分工与渲染优先级：**`feed-id`**（同主体，视频号助手 → 动态管理复制）> **`feed-token`**（跨主体专用，基础库 2.31.1+，强制不能自动播放）> **`channel-id`**（视频号 ID，**sph 开头**，`gh_` 是公众号 ID，只能展示视频号主页）。存量内容里只有 `channel-id`，故仍读写它以便回退，编辑区会标出「视频号主页」提示它播不了指定视频。
- 编辑器上方有一个「插入视频号块」按钮，点击弹出对话框填 feedId / feed-token（二选一，前者优先）与视频号 ID（选填）后插入（void 原子块，内部不可编辑）。
- **已有视频号块可直接点击重新编辑**：`.wx-video-block` 上挂了点击委托（走 DOM 委托而非 snabbdom 的 `on.click`，后者不在 wangEditor 注册的模块之列），命中后带出现值打开同一个对话框；确认后用 `SlateTransforms.setNodes` **就地更新节点属性**（该 API 由 `@wangeditor/editor` 再导出，不必额外引入 slate），不删除重插，光标与上下文都不受影响。节点 id 缺失时退化为按原值匹配。
- `v-model` 双向绑定 HTML 字符串，保存时直接写入 `detail` / `content` 字段（后端按字符串原样存储）。

> 富文本中的视频号块约定、小程序端渲染（`<wx-video>` → `<channel-video>`）详见《首页/跳转页组件与接口字段映射》。

## 单元测试（Vitest）

前端配套 Vitest 单测，覆盖关键纯逻辑与契约约定（不依赖真实后端 / 浏览器）。**同时纳管小程序端的纯逻辑测试**（小程序本身无独立测试工具链，`vitest.config.ts` 通过 `server.fs.allow` 放行仓库根外的 spec 路径）。

前端（`src/`）：

- `src/composables/useTable.spec.ts`：分页切片、关键字筛选（大小写不敏感）、关键字变化后回到首页等纯逻辑。
- `src/store/auth.spec.ts`：Pinia 登录态 `setAuth` / `logout` 与 `localStorage` 同步。
- `src/api/request.spec.ts`：统一响应拦截的**鉴权契约**——`code=0` 透传、`code=401` 清 token + 跳登录并重 reject、`code=403` 警告、`code!=0` 业务失败报错；以及请求拦截注入 `Bearer` 头。
- `src/editor/wxVideoModule.spec.ts`：`<wx-video feed-id / channel-id>` 块「仅注册一次」及其序列化/反解析契约（与小程序端 `rich-video` 一致），含**存量只有 channel-id 的内容往返不丢失**。
- `src/views/Login.spec.ts`：登录页视图层用例——表单校验提示、登录成功后写 Pinia + 跳转、`code=401` 时展示错误并不写登录态（jsdom + Element Plus 组件挂载）。
- `src/api/stats.spec.ts`（17 例）：统计接口的解包契约——日期范围参数、分页信封、
  TOP 榜与意向计数透出、**老副本缺项按 0 兜底（不出现 undefined / NaN）**、
  筛选参数字符串化、CSV 文件名解析（RFC 5987 / 回落 / 坏编码）、
  **来源分布与裂变榜缺字段回落空数组**。
- `src/views/Stats.spec.ts`（24 例）：统计页——汇总卡渲染、日期范围与导出、
  **TOP 双榜数据与点行下钻（带上 eventType + targetId + 对象名，且不筛员工）**、
  **员工「明细」不残留上次下钻的预选**、**内容浏览/裂变两组互不串台 + 表格两列各自求和与拆分**、空态文案、
  **折叠分区默认收起、点标题展开（含来源分布条形与裂变榜的占比计算）**。
- `src/components/StatDetail.spec.ts`（14 例）：明细抽屉——打开即查、筛选下沉服务端、
  回包页码被夹后分页栏同步、**TOP 榜下钻的 eventType + targetId 预置与对象标签清除**、
  事件下拉覆盖八类、对象列渲染。
- `src/components/StatTrend.spec.ts`（13 例）：趋势图几何与交互——折线连点、轴刻度、
  **图例点选（默认三线 / 勾选后纵轴重算 / 最后一项点不掉）**、tooltip、空态与 NaN 兜底。
- `src/views/Employees.spec.ts`：员工管理页视图层用例——列表渲染、启用/停用/重置密码的弹窗确认与接口调用、`el-table` 行上下文（provide/inject 复刻列槽 `{ row }`）等。
- `src/views/ApplyOpen.spec.ts`（16 例）：开通审核——三种状态标签与「已处理」行无操作按钮、副号下沉与缺失昵称/openid 的占位、关键字防抖后回第 1 页、手机端卡片列表；**通过后密码弹窗必现并带「仅展示一次」**、**审批后行就地变「已处理」**（不刷新按钮还在，点第二下等于建两个员工账号）、审批失败不弹空密码窗、**驳回原因空/纯空格都点不动**、重开能清掉上次原因、驳回失败不关窗不误报成功。
- `src/views/ApplyModify.spec.ts`（18 例）：名片修改审核——变更摘要「字段→新值」（未知字段用键名兜底）、快照非法 JSON 显示「无」、**旧值按本页 `staffIds` 精确取且去重（不拉全量员工表）**、手机端隐藏员工ID/提交时间列；明细逐字段旧→新（取不到的显示「（空）」）、员工表取不到仍能审批、明细内直接通过；**连点两次「确定驳回」只发一次请求（`rejecting` 防重入）**、审批失败不关窗不报成功。
- `src/components/AssetPicker.spec.ts`：素材选择器用例——清单渲染、`defaultCategory` 定位、单选即采用、多选批量、超限拦截、**翻页带新页码重新请求**、**搜索防抖后走服务端过滤**、**风格行只渲染 count>0 的档位**、**切换风格带 style 请求**、**单一调性分类隐藏风格行**、**已选调性无素材时自动回落全部**（11 例）。
- `src/components/UploadField.spec.ts`：上传组件用例——直传/回退分支、素材库入口与 `library-type` 透传、单选替换、多选追加、超限截断。
- `src/api/mpRelease.spec.ts`（10 例）：版本发布接口契约——路径拼装（租户维度列表 / 登记 vs 记录维度四个动作）、`undo-audit` 必须带 `confirm:true`、审核状态回包 `wxStatus/changed` 解包、`mpReleaseTagType` 六态映射（含未知值回落 info）、**上传与探活两条路径**（`/releases/upload` 带版本号+备注、`/releases/upload-readiness` 把 `data` 摊平且缺字段兜底）。
- `src/components/MPReleaseDialog.spec.ts`（12 例）：版本弹窗——打开即按企业拉列表、**每个状态出哪些按钮**（draft 只有提交审核 / auditing 查状态+撤回 / audit_passed 发布 / released 终态无动作）、驳回原因渲染与「重新提审」、撤回与发布的二次确认（含用户取消时不调接口）、**上传链路**（打开即探活并逐条列出缺失项 / 就绪提示 / **环境不齐时点「开始上传」不发请求** / 就绪后提交并刷新列表）。`el-dialog` 桩认 `modelValue`，否则嵌套的登记弹窗会把「取消 / 登记」混进行内按钮断言；`el-button` 桩要把 `disabled` 声明成 prop，否则它以 attr 落到真 `<button>` 上，VTU 的 `trigger` 会直接跳过点击，那道 guard 就永远测不到。
- `src/views/Companies.spec.ts`（凭据部分）：租户小程序凭据 + **平台代发开关** + **上传密钥**——缺省允许、关掉后随保存提交 `releaseEnabled:false`、回包 `releaseEnabled:false` 时显示关闭态；上传密钥填了随保存提交、**原本已配置却留空时确认文案同时点名 AppSecret 与上传密钥**（一次说清要清掉什么，不连弹两窗）；以及原先的列表标签 / 回填 / Secret 留空二次确认 / AppID 前缀校验。
- `src/router/index.spec.ts`（11 例）：路由守卫门禁——未登录跳登录页、已登录访问登录页回后台、企业管理员访问平台级页面挡回审核页、平台超管放行、未知路径落默认页、登出后门禁立即生效；另守路由表本身（平台管理分组必须都标 `platformOnly`、仅登录页 public、业务页都要有 title）。做法是真实 routes + 桩组件 + 内存 history，挂的是生产同一个 `registerGuards`。
- `src/layout/AdminLayout.spec.ts`（16 例）：后台外壳——**企业管理员看不到「平台管理」组**、平台超管能看到全部入口、品牌名三级回落（企业简称 → 系统设置品牌 → 「· 管理后台」后缀）、顶部标题取路由 `meta.title`、当前菜单跟随路由；刷新后 `profileLoaded=false` 时用 `/api/admin/me` 取回身份（已恢复则不重复请求，失败保留本地身份）；登出（接口失败也要本地登出）、改密弹层、手机端收进头像下拉、路由切换收起窄屏抽屉。
- `src/views/content/` 六个企业内容页全覆盖：
  - `Categories.spec.ts`（10 例）：列表与锁定态、关键字过滤、**首行上移 / 末行下移禁用条件**、下移后按新顺序批量持久化 sort、新增校验不通过不提交、编辑走 update、删除二次确认、列表失败不崩。
  - `CompanyInfo.spec.ts`（7 例）：回填、**保存只提交 5 个可编辑字段（不带 id）**、LOGO 随保存提交、成功提示、失败不误报成功。
  - `CompanyProfile.spec.ts`（16 例）：视频来源五档的动态字段、**存储不支持视频时上传档禁用并给原因**、保存前三道拦截（视频号档缺 feedId/feedToken、直链档缺地址、开了兜底却没填地址）、payload 恰好九字段、能力探测失败降级且**不漏 unhandled rejection**。
  - `HomeLayout.spec.ts`（12 例）：板块顺序与「当前顺序」联动、**保存提交完整顺序数组**、无背景图时滑块禁用且不显示预览、**清除背景要把遮罩一起归零**、预览放大与关闭、拉取失败保留默认布局。
  - `Projects.spec.ts`（12 例）：分类名反查（分类被删显示占位）、精选/上架标签、分类与关键字过滤、**payload 恰好九字段**、**编辑回填相册是拷贝（改表单不污染列表行）**、删除确认、加载失败不崩。
  - `Services.spec.ts`（13 例）：摘要去 HTML 并截断（空内容显示「（空）」）、首末行禁用与批量持久化、新增/编辑/删除、校验不通过不提交、加载失败不崩。

> 写表格类视图用例时注意：`el-table` 的行上下文 stub 若用 `provide(行对象)`，setup 只执行一次，
> 行数变少时实例复用会拿到**上一行的数据**（表现为筛选后仍显示旧行）。应 provide 一个
> `computed`，由列在渲染时取值。
>
> 另两个高频坑：`el-form` 的 stub 必须真的渲染默认插槽（写成 `h('slot')` 会得到一个无效元素，
> 表单里的输入框全都不进 DOM，断言就变成「对着空气验证」）；`el-button` 的 stub 不要再
> `$emit('click')`，stub 根元素若是真 `<button>`，一次点击会同时触发原生冒泡与自定义事件，
> 结果是接口被调两遍。

小程序纯逻辑（`miniprogram/tests/`，经本目录 Vitest 运行，桩化 `getApp`/`wx` 全局）：

- `miniprogram/tests/richtext.spec.ts`：`<wx-video>` 富文本拆分（自闭合 / 成对 / 属性顺序无关）、无视频号时整体走 `rich-text` 等。
- `miniprogram/tests/request.spec.ts`：`wx.request` 封装的 token 注入、`code=401` 静默 refresh 重试一次、失败清 token 等。
- `miniprogram/tests/poster.spec.ts`（19 例）：分享海报页。桩掉全局 `Page` 捕获页面选项，再手工驱动 `onLoad`，
  覆盖「带 staffId 进入」「**扫码 scene 解析**（含混参 / 无参归 0）」「头像与小程序码转绝对 URL」
  「小程序码接口无 data 时不崩」「**保存成功才计 poster_save（失败 / 未生成不计）**」
  「**分享图片计 share + 低版本降级 + 用户取消不计**」
  「**取数失败 / 取不到 canvas 节点 / 导出失败三态与重试（重试先清旧图）**」。
  选它是因为 scene 解析只在真机扫码才走得到，本地开发永远复现不了。

其余 11 个 spec（共 151 例）用同一套手法，公共桩在 `miniprogram/tests/helpers/harness.ts`
（`installPageCapture` / `makeInstance` / `installWx` / `installApp` / `flush`）：

- `index_home.spec.ts`（33 例）：scene 与 `globalData.myStaffId` 解析、自定义顶栏避让、四次并行请求的**根级**字段拼装、停用/不存在/异常三态、**访问埋点去重**、**分享埋点（多次分享各计一次）**、查看案例「滚动 or 跳转」、分享标题与路径、顶部遮罩渐变与 0.01 死区。
- `apply_open.spec.ts`（17 例）：企业名简称回落、头像上传三种响应（成功 / 业务错误 / 非 JSON）、昵称授权、提交校验与 `saving` 复位、返回兜底。
- `employee_me.spec.ts`（14 例）：未登录与失效回退、头像绝对化 + `globalData` 回写、改密三条校验、**改密成功后延时静默登出**、退出清两个令牌。
- `employee_edit.spec.ts`（13 例）：回填与头像绝对化、备用手机号**收起即清空**、上传三条支线、提交待审。
- `project_list.spec.ts`（15 例）：分类根级、`toView` 滚动定位、标题回显与三种回落、切换分类去重。
- `project_detail.spec.ts`（16 例）/ `service_detail.spec.ts`（11 例）：绝对 URL、富文本片段、时间格式化、空态文案、分享回带 `companyId`、相册预览、**加载成功上报 view 埋点（失败/不存在不上报）**、**分享上报 share 且带 targetId**。
- `employee_login.spec.ts`（9 例）/ `employee_stat.spec.ts`（6 例）：空值校验、成功/失败/401 分流、`submitting` 复位。
- `track.spec.ts`（5 例）：埋点统一入口——带 targetId、**targetId 为 0 时省略字段（保持老载荷形状）**、staffId=0 丢弃、白名单外丢弃、请求失败静默。
- `company_video.spec.ts`（12 例）：视频档位选择六例 + **video_play 去重上报** + staffId 缺失时丢弃。

**视图层 spec 的 Element Plus 桩统一在 `src/test/elStubs.ts`**（`mountOpts` / `flush` / `paged` / `rowOf` / `btnIn` / `dlgOf`），新增列表页 spec 直接复用，别再各写一套。文件头写了三条必须遵守的约定，改动前先读：

1. `el-button` 桩**不声明** `emits:['click']`（否则 `@click` 只认 `$emit`，`trigger('click')` 打在空气上），但**必须把 `disabled` 声明成 prop**（否则渲染期告警，且「禁用后点不动」这类 guard 断言不可靠）。
2. `el-dialog` 桩带 `data-title`：一页往往不止一个弹窗，`find('.el-dialog')` 永远只拿第一个，断言会静默落到另一个窗上。
3. `el-table` 桩按真实模型写——**列只是配置**（`el-table-column` 不渲染任何东西），行与单元格由 `el-table` 按 `data` 统一渲染。若按「每列各自渲染自己的单元格」写桩，**纯 prop 列（无插槽）会被 Vue 静态提升**，父组件重渲染时整块 vnode 被跳过：数据变了、带插槽的列更新了，prop 列却停在首次渲染的旧值（实测表现就是「驳回原因列刷新后一直是空的」）。配套要求 `paged()` 返回**新数组**，`:data` 引用不变则行不会重算。

配置见 `vitest.config.ts`（jsdom 环境 + `@` 别名 + vue 插件 + 小程序 spec 纳管）。运行：

```bash
cd frontend
npm test            # 等价于 vitest run，跑前端 + 小程序全部 spec
npm run test:watch  # 监听模式
```

> 说明：CI 中 `frontend` job 执行 `npm ci` + `npm run build` + `npm test`（前端与小程序单测一并覆盖）。  
> 严格类型检查（`npm run type-check` / `vue-tsc --noEmit`）历史上有 4 个 `@wangeditor/*` 相关错误（editor-for-vue 的 `exports` 未暴露 types、自定义节点 `registerNodeType` 的类型签名），已通过 tsconfig `paths` 指向其真实 d.ts + 精准断言收敛修复，**当前 0 错误**；CI 的 `frontend-typecheck` job 已从「预警（continue-on-error）」升级为**阻塞门禁**，后续不得引入新的类型错误。

## 反向代理配置（`nginx.conf`）的三个要点

线上由 `nginx.conf` 托管静态产物并反代后端。改这个文件时有两处**不是风格问题**：

1. **`location = /healthz` 必须单列，不能落进 `location /` 的 SPA 兜底。**
   `try_files $uri $uri/ /index.html` 会把 `/healthz` 回退成 `index.html` 并返回 200，
   而那是 nginx 自己的静态文件——后端进程已死、数据库连不上、upstream 502，这个地址
   照样 200。于是 `scripts/alert_check.sh` 的 R1（本地探活）/ R4（外部拨测）、
   `preflight.sh` 第 10 节、以及外部 LB/k8s 的 HTTP 探针**全部永远绿**，
   最该报警的故障恰恰不报警。实测：停掉 backend 后旧配置仍返回 `200 text/html`，
   新配置返回 `504`。

2. **`X-Forwarded-For` 要覆盖（`$remote_addr`），不能用 `$proxy_add_x_forwarded_for` 追加。**
   追加会把客户端自带的伪造值留在链左侧；后端的可信代理网段是私有网段，而反代自己
   正是其中一个地址，于是「跳过可信代理、取第一个不可信地址」取到的是那个伪造值。
   登录 / 刷新 / 小程序码三处限流都按这个 IP 分桶，换一个伪造头就换一个桶——
   实测 14/14 全部放行。改成覆盖后同一手法第 11 次即返回 `code:429`。

3. **`proxy_read_timeout` 要大于后台上传小程序代码的耗时（`MP_UPLOAD_TIMEOUT_SEC`，默认 300s）。**
   `POST /api/admin/companies/:id/releases/upload` 是**同步执行**的——编译 + 传代码都在
   这一次请求里做完，分钟级。nginx 默认的 60s 会在上传途中先返回 504，
   而那时代码可能已经传到微信了：前端看到失败、后台没有 draft 记录，
   运营再点一次就撞上「版本号已占用」。改的是反代，不是前端能兜住的。

## 已知边界（与后端一致）

- 分类 / 业绩项目 / 业务服务列表未分页（数量有界且需整表拖拽排序）；员工 / 企业 / 申请 / 账号 / 审计日志已服务端分页（`useServerTable` + 后端 `?page=&size=`）。
- 私有桶场景下，前端无需改造：上传/访问经签名通道，URL 由后端返回限时签名地址（见《三端联调与接口总表》§4.4/§4.5）。
- 初始密码仅返回一次（开通/新增/重置），前端弹窗展示，需超管线下通知员工。
- **小程序版本发布是 MVP**：上传代码不在后台也不在后端——由 CI 用「代码上传密钥」经
  `miniprogram-ci` 完成。后台登记版本号时**无法校验该版本是否真的上传过**，写错只在提审时
  由微信暴露；`item_list`（审核类目）也未开放，取后台已配类目。
