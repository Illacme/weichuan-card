# 主题系统两轴化设计（风格 × 色系）

> 状态：**P1（数据/契约层）/ P2（CSS 两层 + 前 2 模块布局）/ P3（剩余 2 模块布局）/ P4（后台两步 UI + 预览图按风格重出）/ P5（全叉乘 + 36 组合）均已实现并通过全量门禁**。
> **P5 修订已落地**：用户反馈后把「树形归属」改为「4 风格 × 9 色档全叉乘」，并把风格改按布局特色命名；决策与分期见 §12，§1/§2.3/§10 的树形描述保留作历史事实。
> P4 的实际范围是「固定预览区 + 两步选择 + 12 张图按 4 风格布局重出 + 守门改写」；**刻意未做**白名单物理树化、真机像素级验收，这二项留作后续阶段（见 §7 / §8 / §10），不是漏做。删 `template_id` 兼容列已在 P5 之后经迁移 `0043` 落地（见 §2.1 / §8 / §12.2），不再属于「未做项」。
> **迁移 `0043` 全局口径**：全文档 P1–P5 期间凡描述「`template_id` 保留作兼容派生列 / 旧端靠它兜住 / `splitThemeId()` 拆它」之处，均为彼时事实；该列已于 `0043` 删除，对应写链路（前端 `splitThemeId`、后端 `DeriveTemplateID`/`Split` 对外写）一并移除。仅小程序 `utils/theme.ts` 的 `splitTheme` 保留，用于解析**真正旧版本后端 / 旧缓存**，且不再回写 `template_id`。
> 2026-11-11 用户确认方向：整体风格驱动各模块布局、品牌配色作为叠加在风格之上的色系调整；并确认布局深度为「整模块重排版」。
> 分期记录见 `docs/commercial-readiness.md` 第三十八～四十一轮：P1 完成 `StyleID`+`BrandColorID` 双列、`0042` 迁移与两轴接口；P2/P3 完成 4 风格 × 4 模块布局；P4 完成后台与预览图收口。
>
> **命名澄清（重要）**：设计稿里用的标签名「classic（经典蓝基线）」对应的**真实风格 id 是 `default`**（即 `mptemplate.DefaultID`），不是字面 `classic`。P1 实现中基础风格一律用 `default`；P1–P4 期间 `brand-*` 全部归 `default` 风格之下（**P5 起该归属已废止，见 §12**）。下文凡「classic」均指 `default` 这一风格。

## 0. 目标与不变式

- **风格（Style）** 决定小程序各功能模块的**布局语言**（顶部名片卡区、公司简介、经典业绩、业务服务的结构与排布各有特色）。
- **色系（BrandColor）** 是叠加在所选风格之上的**纯颜色层**，不改布局。
- 不破坏既有不变量：
  - 保存**只提交风格/色系**，不跨页签覆盖（P4 起 `MpTheme.save()` 只发 `{ styleId, brandColorId }`；更早的 P1–P3 期间曾发 `{ templateId }`，当时由后端 `template_id` 兼容派生列兜住，**该列已于迁移 `0043` 删除**，新后端只认两轴）。
  - 后端 PUT 指针语义（`backend/internal/handler/content.go` 的 `StyleID` / `BrandColorID` 指针），未提供的字段保持原值——不存在 `templateId` 字段，不再有单列兼容分支。
  - 保存后访客端即时生效，无需重新上传代码或提审（`MpTheme.vue:4-7` 既有承诺）。

## 1. 概念模型（两轴 + 树形）

```
Style (布局族)               BrandColor (颜色层，叠加在 Style 之上)
├─ classic (经典蓝基线)  ──┬─ default(经典蓝)        ← brandColorId = nil 时用风格默认色
│                         ├─ crimson(绛红)  ← 为该风格调校过
│                         └─ coral(朱橘)    ← 为该风格调校过
├─ ink (沉稳墨)         ──┬─ default(墨蓝)
│                         └─ ...
├─ warm (暖金棕)        ──┬─ default(暖金棕)
│                         └─ ...
└─ teal (松石青)        ──┬─ default(松石青)
                          └─ ...
```

- **组合 = Style × 其 BrandColor**（含「不选色系 = 用该风格默认色」）。
- 端上根节点同时挂两层 class：`class="wc-style-<styleId> wc-brand-<colorId>"`，**布局来自 style 层，颜色来自 brand 层**。
- 选型依据：用户原话「品牌配色是基于不同风格基础之上的色系调整」→ 色系是**每个风格各自调校**的（树形），而非任意风格+任意色的叉乘。叉乘备选见 §10。

## 2. 数据模型

### 2.1 历史形态（已删除）

> 早期（`0041`）`HomeLayout` 只有一个 `template_id` 单列（扁平 12 款内置配色）。`0042` 升级为两轴
> `style_id` + `brand_color_id`，并把 `template_id` 保留作「兼容派生列」。**迁移 `0043` 已删除
> `template_id` 列**，当前 `models.go` 只有 `StyleID` / `BrandColorID` 两轴（见 §2.2），不再存在单列近似。

### 2.2 目标 / 现状（已落地）
```go
StyleID      string   `gorm:"column:style_id;size:32;default:classic" json:"styleId"`
BrandColorID *string  `gorm:"column:brand_color_id;size:32" json:"brandColorId"` // nil = 用风格默认色
```
- 白名单 `mptemplate` 从扁平 12 改为**树**：
  ```go
  type Style struct {
      ID           string
      Name         string
      LayoutTokens map[string]string // 各模块布局规则（见 §4）
      DefaultColor ColorSet          // 该风格默认色系
      BrandColors  []BrandColor      // 该风格下可选的色系
  }
  type BrandColor struct { ID, Name string; ColorTokens ColorSet }
  ```

### 2.3 存量迁移（已确认映射）
- 4 款 `style`（`default`/`ink`/`warm`/`teal`）：`style_id = 同名`、`brand_color_id = nil`。
- 8 款 `brand-*`：**全部归 `default` 风格之下**作色系，即 `(default, brand-*)`.
  —— 用户确认的零突变方案：brand-* 现有形态就是「配色换、版式沿用 default」，归入 default 子树不改动任何存量租户外观。
  （设计稿早期草案曾提「按色温分散到多风格」，已弃用；那样会让存量选了 brand-* 的租户从 default 版式跳到目标风格版式，产生视觉突变。）
- 迁移脚本（`0042`）把旧 `template_id` 拆成 `(style_id, brand_color_id)`；`template_id` 列当时保留作兼容派生列（写入时由两轴反推），便于回滚锚点。**迁移 `0043` 已删除该列**，当前存储只有两轴。

## 3. CSS 架构（两层解耦）

### 3.1 布局层 `.wc-style-<id>`（控制模块结构）
```css
.wc-style-warm .mp-card-area { /* 顶部名片卡：头像左、信息右、按钮胶囊化 */ }
.wc-style-warm .mp-profile  { /* 公司简介：左图右文 / 两段式 */ }
.wc-style-warm .mp-achievements { /* 经典业绩：横向卡片流 */ }
.wc-style-warm .mp-services { /* 业务服务：图标网格 */ }
```
每风格 × 每模块一套规则（见 §4）。

### 3.2 颜色层 `.wc-brand-<id>`（只覆盖颜色 token）
```css
.wc-brand-crimson { --wc-primary:#a01f2e; --wc-primary-deep:#801925; ... /* 仅颜色，无 --wc-r-* / shadow */ }
```
**守门（替换现有 `theme.spec.ts:382`「品牌款版式==default」）**：`.wc-brand-*` 选择器体内**不得出现**任何 `--wc-r-*` / `--wc-card-shadow*` 等布局 token——颜色层只动颜色。

### 3.3 迁移
> ✅ **P2 已落地**（实现细节比上面的方向稿更保守，都是有原因的取舍）：
> - `app.wxss` 里 12 个 `.wc-theme-<id>` 块已全部拆除，改为 `.wc-style-ink/warm/teal`
>   （布局层，各给全套 18 个 token + 模块布局规则）+ `.wc-brand-<8 款>`（颜色层，
>   **只给 11 个颜色 token**）。`default` 不另写 token 块，直接继承 `page` 的 18 个默认值。
> - 颜色层排在布局层之后，同为单类选择器时后定义者覆盖前者，品牌色才会生效。
> - 根节点 `wc-theme {{themeClass}}` 里的 `wc-theme` 基类**保留作语义标记**（已无 token
>   定义），`themeClass` 变量展开成**两层** class：`wc-style-<id>` 恒挂（含 default），
>   有品牌色系时再加 `wc-brand-<id>`。六个访客页的 wxml 因此一行都不用改。
> - `utils/theme.ts` 的 `applyTheme(styleId, brandColorId)` 改两轴；**Storage 仍存扁平
>   `templateId`**（`wc_theme_id`），旧缓存 / 旧后端靠 `splitTheme` 拆、`deriveThemeId` 合，
>   兼容期不做数据结构迁移。`resolveTheme(信封)` 统一两轴优先 / 旧后端拆 `templateId`，
>   首页 `loadAll` 与冷启动 `prefetchTheme` 都走它，不各自猜字段。
> - **12 组组合 token 逐字不变**：颜色层删掉的 7 个版式 token 原本就等于 `page` 默认值，
>   预览脚本 `read_tokens()` 与 `theme.spec.ts` 的 `readThemeVars()` 都按「颜色层 ∪ default
>   版式」合并出 12 组 18 token，与 `manifest.json` 快照逐字相等 → **当轮 12 张 PNG 无需重出、
>   存量配色零突变**。当时「真正按风格重出布局长图」仍是 P4 的事。
> - ✅ **P4 已落地**：布局规则也进预览图（见 §6）。`STYLE_RULE_RE` 从 `app.wxss` 抽取
>   `.wc-style-<id> … { … }` 原文注入预览 HTML，改了列数却没重出图会被 `manifest.layouts`
>   快照与 `--check` 判红。12 组 token 的合并口径本身没变。

## 4. 各模块 × 各风格的布局语言（设计核心，方向稿非像素定稿）

模块清单（与现有小程序首页一致）：① 顶部名片卡区 ② 公司简介 ③ 经典业绩 ④ 业务服务。

四种风格「性格」与对四模块的差异化处理：

| 风格 | 性格 | 顶部名片卡区 | 公司简介 | 经典业绩 | 业务服务 |
|------|------|------|------|------|------|
| **default**（标准均衡） | 标准、均衡 | 横版左图右文 + 两列联系表 | 上图下文（名 → 线 → 简介竖排） | 4 列分类 / 2 列精选 | 4 列竖版卡 |
| **ink**（紧凑信息） | 硬朗、信息密度高 | 工牌单栏款：方角小头像、联系表**单列四行**、行间细线 | 通栏窄封面 + 左窄栏公司名 / 右通栏简介 | 5 列分类 / 2 列精选 | 4 列收紧 |
| **warm**（宽松亲和） | 亲和、宽松 | 竖版居中款：头像居中、姓名/LOGO/公司名/按钮**上下叠放**、联系表单列居中 | 大圆角封面 + 文字居中、分隔线缩成居中短线 | 2 列分类 / 1 列大图 | 3 列宽松 |
| **teal**（疏排透气） | 清爽、透气 | 侧栏款：卡片左缘**通高竖条**、头像**靠右**、两列表格式带竖分隔线 | **左图右文**（封面 42% 宽） | 3 列分类 / 2 列 | 3 列疏排 |

> 每格是「该风格下该模块长什么样」的**版式描述**；落地由 `app.wxss` 的 `.wc-style-<id> .mp-<module>` 规则实现（4×4=16 套模块布局）。
>
> ✅ **P2 + P3 已落全 4 模块 = 16 套布局**；**P6（2026-11-12）把前两个模块从「只改尺寸」升级为「版式级差异」**。
> 升级动因是用户直接批评：「我看现在整体风格中对于小程序页面顶部的名片信息区和企业介绍区并没有做什么
> 布局调整啊，只是元素高度有点不同而已，比如印刷的名片也有很多种布局样式啊，为什么你就没有更多灵活性的考虑呢」。
> 当时的实现确实如此——四款风格对 `.emp-card` 只改 padding / 头像直径 / 行距，对 `.mp-profile` 只改封面高度与圆角，
> 结构完全一致，客户换风格只会觉得「大了点」。P6 的口径改为：这两个模块允许**纯 CSS 结构重排**
> （改 `flex-direction`、改栏数、改分栏归属与对齐），仍守住下面四条硬约束。
>
> 四条硬约束：
> - `default` 逐字复述既有度量（升级零突变）；
> - **不增删 DOM、不隐藏元素**：「无图款」实现为封面压成窄条，`display:none` 会让客户上传的公司视频
>   悄悄消失；`.ec-contact` 只改栏数、绝不删掉某一列；
> - 规则一律锁在模块钩子内，绝不写全局 `.section` / `.intro` 去波及二级页与员工页；
> - **服务卡标题烧在图片里**（有图时 `service-grid/index.wxml` 只渲染 `.svc-icon`、不渲染 `.svc-title`），
>   差异化只能改列数 / 间距 / 圆角 / 分隔线，绝不把 `.svc-icon` 缩成小图标或改 `aspect-ratio`。
>
> `index.wxml` 的四个钩子：`mp-card-area` / `mp-profile` / `mp-achievements` / `mp-services`。
> 公司简介区**新增一个纯版式分组容器 `.pf-text`**（把「公司名 / 分隔线 / 简介」三个原平级节点包成一栏，
> 顺序与可见性完全不变）：没有它就无法在 `.mp-profile` 上做出「左图右文」与「左窄栏 + 右通栏」两种版式。
> `default` 款不给 `.pf-text` 写任何规则，它是普通块级容器，视觉零变化。
> P3 用**列数**做业绩 / 服务的重排：分类 / 精选 / 服务 = `default` 4/2/4、`ink` 5/2/4、
> `warm` 2/1/2、`teal` 3/2/3。

## 5. 后台 UI（`MpTheme.vue`）

- 分组从「按 `series` 两组」（`MpTheme.vue:147` 的 `groups`）改为**两步选择**：
  1. **选风格**：4 张风格卡，每张展示该风格的「模块布局示意长图」+ 默认色，标注其布局性格。
  2. **选色系**：在所选风格下，渲染该风格的 `BrandColors` 色卡条（含「默认色」），点选即叠加。
- 选中态 = `(styleId, brandColorId)`；`.tab-actions` 的「保存风格」payload 由 `{templateId}` 改为 `{styleId, brandColorId}`（其余两页签不变）。
- 仍嵌在「小程序设置」页签，复用既有 sticky 切换栏与统一保存/重新加载（`frontend/src/views/content/MpSettings.vue` 已就绪）。

> ✅ **P4 已落地，交互按用户反馈做了一处调整**：用户提出「预览显示区固定在一个位置，
> 根据所选风格和色系切换显示更友好合理」，因此**没有**采用上面方向稿的「4 张风格卡各带一张
> 布局长图 + 色卡条小图」，而是改成：
> - 左侧 `<aside class="preview-pane">` 一个 `position: sticky` 的**固定预览区**，整页只有一个
>   `.theme-preview`，`:src="previewUrl(currentId)"` 随 `(styleId, brandColorId)` 组合切图；
>   右侧两步选择（第 1 步 4 张风格卡 / 第 2 步「默认色」+ 8 张色卡）。
> - 树形归属落地：非 `default` 风格自动清空并禁用色系，否则后端 `BelongsToStyle` 会 `code=1`，
>   客户只看到一句「保存失败」却不知道原因。
> - `save()` 只发 `{ styleId, brandColorId }`；`splitThemeId()` 兼容只下发兼容列 `templateId`
>   的旧后端（按 `brand-` 前缀拆回两轴）。打开大图后切组合时 `syncZoom()` 同步换图；
>   「使用中」只在保存成功后更新，未保存组合不误标。

## 6. 预览图生成（`scripts/gen_theme_previews.py`）

> 当前口径：**12 个合法组合各出一张完整首页长图，布局取自 `app.wxss` 的 `.wc-style-<id>` 规则原文，颜色取自两层合并 token**；`--check` 不跑浏览器即可对账产物。
>
> **早期方向稿（已被 P4 落地口径取代，保留作决策记录）**：曾设想「按风格出 4 张布局长图 +
> 色卡条小图，不再为每组合各出一张整图」；并注明 P2 现状是 12 张按组合出的长图，只把
> `read_tokens()` 改成两层合并读取（`.wc-style-*` 全套 + `.wc-brand-*` 颜色 ∪ default 版式），
> 合并出的 18 token 逐字未变，当轮 `manifest.json` 与 12 张 PNG 全部无需重出、`--check` 保持绿。

> ✅ **P4 实际落地：仍是 12 张合法组合各一张，但每张按所属风格的布局出图**，没有改成
> 「4 张图 + 色卡条」。理由：固定预览区本来就是按组合切一张完整长图，缩成 4 张反而要另做
> 小图合成；保留 12 张还能继续用「PNG 集合与 Go 白名单一一对应 / 12 张内容两两不同」
> 这批已有的守门，不为了改结构而丢掉静默失败防护。
> - `STYLE_RULE_RE` 从 `app.wxss` 直接抽取 `.wc-style-<id> … { … }` **规则原文**，去掉前缀、
>   把 `rpx` 按 0.5 折成浏览器 `px`，注入 `${style_css}`；预览 HTML 复用真机模块钩子
>   `.emp-card` / `.mp-profile` / `.mp-achievements` / `.mp-services`，**不存在第二套布局**。
> - 4 个风格默认色用各自布局，8 款 `brand-*` 用 `default` 布局 + 自己的色系 token
>   （脚本的 `style_of(id)` 与端上 `splitTheme()` 的反推同口径；后端那个把两轴压扁的单列
>   `Split()` 已随迁移 0043 删除，只服务旧缓存兼容的 `splitTheme` 留在端上）。
> - 画布按**每风格实测内容高度**收口（`measure_content_bottom()`）：`default/ink/warm/teal`
>   实测底部 `1214/1102/2492/1649`，画布 `1294/1182/2572/1729`（+20 呼吸 +60 底栏），
>   最大上界 `375×2580`；2x 出的 PNG 分别为 `750×2588 / 2364 / 5144 / 3458`。
>   再让所有图统一 1210 高度会裁掉宽松亲和的单列大卡流。上界 P6 从 2420 抬到 2580，
>   因为 warm 的竖版居中名片把内容底部从 2326 长到 2492（底栏 absolute，上界不够会静默盖卡）。
> - P6 修了三处「预览 DOM 与真机不同构」，否则针对这些节点的版式规则在图上落不下去：
>   `.ec-rows` 里 `.ec-rows-line` 与 `.ec-contact` 的层级、缺 `.company-video` 与
>   `.pf-text` 容器、`.ec-line` 的上下内边距；另用 `@container (max-width: 200px)` 给
>   窄封面（teal 的 42% 左图）换一套小号装饰，避免「走进 XX」大字与假进度条叠成一团。
> - `manifest.json` 新增 `layouts`（4 套规则原文快照）、`canvasByStyle`、`contentBottoms`；
>   `--check` 与 `theme.spec.ts` 的 `expectedLayoutRules()` 同口径复算，改了 WXSS 布局却没
>   重出图必红。

## 7. 接口契约（`docs/openapi.yaml`）

> ✅ **P1 已落地**：公开/管理 GET 根级下发 `styleId` + `brandColorId`；PUT 接受 `{ styleId?, brandColorId? }`
> （两者都缺时不接受 `templateId`，已是 P1–P5 的口径；`brandColorId` 任意风格可叠加，未知 id `code=1`；
> 高级风格/高级色系「新选」按 `mp_template_advanced` 回 `code=403`）。`template_id` 兼容派生列已于
> 迁移 `0043` 删除，当前接口只认两轴（`openapi.yaml` 三处已移除 `templateId` 字段）。

- **P2 已落地**：小程序 `utils/types.ts` 的 `HomeLayoutResp` 补 `styleId` / `brandColorId`
  （`types_contract.spec.ts` 与 `models.go` json tag 对账）；首页 `loadAll` 与冷启动
  `prefetchTheme` 都经 `resolveTheme` 消费两值并兼容旧后端。**P2/P3 期间**后台 `MpTheme.vue`
  仍发 `templateId`，由 P1 的兼容派生列兜住，两边零漂移。
- **P4 已落地**：后台 `frontend/src/api/content.ts` 的 `HomeLayoutData` 补两轴字段与兼容列，
  `MpTheme.save()` 改发 `{ styleId, brandColorId }`；`splitThemeId()` 兼容旧后端只下发
  `templateId`。**接口契约本身未改**（P1 的 GET/PUT 已能直接消费两轴），因此 `openapi.yaml`
  本轮零改动。
- **后续阶段（P4 刻意未做）**：`templateId` enum 收敛、清单从扁平 `templates[]` 改为
  `styles[]` + 每风格 `brandColors[]`。P4 后台仍按服务端 `series` 做两步渲染，
  **不自己猜 id 前缀**；清单树化要连同后端、OpenAPI、前端一起改，避免单侧漂移。

## 8. 后端 handler / model / 迁移

> ✅ **P1 已落地**（与下面「最终形态」的差异是刻意的兼容取舍，不是漏做）：
> - `models.go`：新增 `StyleID` + `BrandColorID`（当前已是唯一存储形态；早期 `TemplateID` 兼容派生列已于迁移 `0043` 删除）。
> - `mptemplate.go`：白名单**仍是扁平 12 款**（`IsBrand`/`SeriesOf` 保留），另加一组两轴纯函数
>   `Split`/`DeriveTemplateID`/`IsAdvancedCombo`/`BelongsToStyle`/`NormalizeStyle`/`IsKnownBrandColor`/`StyleOfBrand`；
>   归属判定由 `StyleOfBrand()` 提供（P1 全部返回 `default`），**未做物理树化**。
> - `content.go`：GET 下发两值 + 兼容列；PUT 两轴解析 + 归属校验；授权 gate 改为两轴 `IsAdvancedCombo` + `changed` 比较。
> - `handler.go`：`publicTemplateID` → `publicStyleBrand(styleID, brandColorID)`，未开通时回落 `(default, nil)` 且不抹库。
> - 迁移 `0042_mp_theme_two_axis.sql`：加 `style_id`（DEFAULT `'default'`）/ `brand_color_id`（NULL）两列 + 按 §2.3 回填；
>   随后迁移 `0043_drop_template_id.sql` 删除 `template_id` 列，`manifest_gen.go` 追加清单项。
> - `handler_test.go` 新增「风格两轴」子测试；`mptemplate_test.go` 新增两轴函数单测；`gofmt` / `go test ./...` 全绿。

- **后续阶段（P4 未纳入，不是漏做）**：白名单物理树化并删除 `IsBrand` 平面判定、列表接口改返回树、
  前端两步 UI 改为消费树。删 `template_id` 列与兼容字段已在迁移 `0043` 落地（小程序端 `splitTheme`
  仍保留以兼容真正旧版本后端 / 旧缓存，不回写 `template_id`）。
  P4 只做后台 UX 与预览图收口，**后端 `mptemplate` / 迁移 / OpenAPI 零改动**，
  这样两轴化全程不引入新的数据库或接口变更风险。

## 9. 守门 / 测试改动

- **P1 已加**：后端 `handler_test.go` 两轴子测试（跨风格拒绝、未知 id 拒绝、只交 brandColorId 默认落 `default`、
  高级品牌色系 gate 403）、`mptemplate_test.go` 两轴函数单测、`migrate` 清单比对；反向验证（neutralize 归属校验必红）已做。
- **P2 已做**（原计划放在 P4 的端上守门，因拆层发生在 P2 必须同步，否则全量测试立刻红）：
  - `theme.spec.ts` 的 `readThemeVars()` 改两层合并；「品牌款 7 个版式 token == default」改写为
    **「`.wc-brand-*` 块内不含任何布局 token」+「12 组合合并后版式逐字 == default」**；
    新增「布局层 18 / 颜色层 11 token 对账」「根节点生成两层 class」「4 风格 × 模块
    选择器 + wxml 模块钩子（P3 扩到 4×4）」「两层合并 token == manifest 快照」以及两轴拆分 / 归一 / 旧缓存用例。
  - `themeBootstrap.spec.ts` 改两轴断言（`resolveTheme` 用真实实现，只 mock 广播出口）；
    `types_contract.spec.ts` 的 `HomeLayoutResp` 字段扩到 6 个并逐个对账 Go json tag。
  - 生成脚本新增 `STYLE_BLOCK_RE` / `BRAND_BLOCK_RE` 源码级断言，禁止退回单层解析。
  - **反向验证 4 项必红**：颜色层偷塞版式 token / 退回单层 class / 删某风格模块规则 /
    偷改一个色值；还原后复绿。
- **P3 已做**：
  - 守门从 4×2 扩到 **4 风格 × 4 模块**（`.emp-card` / `.mp-profile` /
    `.mp-achievements` / `.mp-services`）；新增「同模块四风格的规则体两两不同」对账，
    防止四个风格复制粘贴同一套度量——只查「选择器存在」会假绿，客户等于没换风格。
  - 新增「服务卡标题烧在图内」守门：`.svc-icon` / `.svc-icon-ph` 的规则体不得出现
    `width` / `height` / `aspect-ratio` / `max-height`，并钉住 `service-grid/index.wxml`
    的注释，避免以后有人「顺手优化」把竖版卡缩成小图标。
  - **反向验证 3 项必红**：删 warm 业绩布局 / warm 服务布局复制 default /
    把服务卡缩成小图标；还原后复绿。P3 不触碰任何 token，12 组 × 18 与 manifest 仍逐字一致。
- **P4 已做**：
  - `MpTheme.spec.ts` 重写（20 例）：两步渲染、整页只有一个固定预览区、风格/色系切图、
    非 `default` 自动清空色系且点色卡无效、保存只提交两轴、旧后端 `templateId` 拆分、
    大图随组合切换、按张降级与重新加载重试、授权到期锁高级款、页面脱敏说明。
  - `MpSettings.spec.ts` 改断言父组件切换栏「保存风格」只提交两轴值。
  - `miniprogram/tests/theme.spec.ts` 改源码级守门：后台固定预览区 / 两轴 payload /
    `theme-card` 不得回潮、脚本按 `STYLE_RULE_RE` 注入布局、`manifest.layouts` 与
    `app.wxss` 原文逐字对账、12 张 PNG **按所属风格画布**逐张校验尺寸、四风格高度互不相同。
    「12 张 PNG 与 Go 白名单一一对应 / 12 张内容两两不同」这批守门保留，没有为改结构而丢掉。
  - **反向验证 8 项必红**（每项字节级还原、还原后复绿）：① 布局注入退回全 `default`
    ② 布局规则写死不分风格 ③ `save()` 退回 `{ templateId }` ④ 固定预览区 `:src` 写死
    ⑤ 切非 `default` 不清色系 ⑥ 退回每款一张 `theme-card` ⑦ `manifest.layouts` 与
    WXSS 漂移（Vitest 与 `--check` 双红）⑧ `ink.png` 换错尺寸。复核确认 ①②⑤⑥
    红的是对应断言行而非编译错误。
  - 全量门禁 8 项实质全绿：`golangci-lint` 0 issues / `go test ./...` / `--check` /
    `eslint .` / `vue-tsc --noEmit` / 全量 Vitest **95 文件 1605 例** / `vite build`
    （首次撞环境 `CODEBUDDY_BROKER_TIMEOUT`，单独重跑绿）/ `git diff --check`。

## 10. 风险与待确认项

1. ~~**品牌色归属哪个 Style**~~ ✅ **已拍板**：8 款 `brand-*` 全部归 `default` 风格之下（零突变，见 §2.3）。
2. ~~**树 vs 叉乘**~~ ✅ **已定树形**：色系为每个风格单独调校，保证不撞风格；P1 以 `StyleOfBrand()` 表达归属，P4 在后台 UI 上落实「非 `default` 无可用色系」。**白名单物理树化仍属后续阶段**。
3. ~~**工作量爆炸**~~ ✅ **16 套模块布局已在 P2+P3 全部落地**，靠「4 风格 × 4 模块各有专属规则 +
   同模块布局互不雷同」守门替代人工逐套核对；**真机验收组合仍开放**（建议只覆盖「每风格默认色」
   +「每风格抽 1 个品牌色」，其余靠单元测试守门）。
4. **（历史）兼容期双写**：P1–P5 期间 `template_id` 与两轴列同步写，读路径以两轴为准、未回填行回退 `Split(template_id)`；
   旧端仍读 `templateId`。**迁移 `0043` 已删除 `template_id` 列，后端不再写也不读它**；小程序端 `splitTheme` 仅用于兼容真正旧版本后端 / 旧缓存，不再回写 `template_id`。改动这块必带反向验证。
5. **分期**：见 §11，先数据层后视觉层、先 2 模块后 4 模块 —— P1/P2/P3/P4 已按此走完；
   剩下的不是分期内的实施项，而是**独立的后续工作**（白名单树化 / 删兼容列 / 真机像素级验收）。

## 11. 实施分期（建议）

- **P1 数据/契约层（不改视觉）** ✅ **已完成并测试通过**：`models.go` 双列（`StyleID`+`BrandColorID`） + `0042` 迁移（加两列 + 按映射回填，当时保留 `template_id` 兼容派生列）+ `mptemplate` 两轴函数（`Split`/`DeriveTemplateID`/`IsAdvancedCombo`/`BelongsToStyle`/`NormalizeStyle`/`IsKnownBrandColor`）+ 接口 GET 下发两值 / PUT 两值与跨风格归属校验 + `openapi` 三处补全 + 后端测试（含跨风险拒绝与高级色系 gate）。前端/小程序暂用 `templateId` 兼容字段，未改动。后续迁移 `0043` 已删除 `template_id` 列、移除了 `DeriveTemplateID`/`Split` 对外的写链路。
- **P2 CSS 两层 + 顶部名片卡 / 公司简介布局**（2 模块）✅ **已实现并测试通过**：`app.wxss` 拆 `.wc-style-*`（4 风格，18 token + 2 模块布局规则）与 `.wc-brand-*`（8 色系，仅 11 颜色 token）；`theme.ts` 两轴 + 扁平缓存兼容；`index.ts` / `themeBootstrap.ts` / `types.ts` 接线；预览脚本两层合并读取；端上守门改写 + 4 项反向验证。后台与小程序 wxml 的顶层节点无需改动（`wc-theme` 基类保留）。**12 组组合 token 逐字未变，PNG 无需重出。** P2 才会给已选 `ink/warm/teal` 的租户带来首页这两个模块的可见外观变化；`default` 与 8 款 `brand-*` 零突变。
- **P3 经典业绩 / 业务服务布局**（剩余 2 模块）✅ **已实现并测试通过**：`app.wxss` 补齐
  `.mp-achievements` / `.mp-services` 的 4 风格规则，用**列数**做真正可见的重排
  （分类 / 精选 / 服务：default 4/2/4、ink 5/2/4、warm 2/1/2、teal 3/2/3），
  辅以圆角 / 间距 / 描边 / 微阴影；`default` 逐字复述基线零突变。
  守门扩到 4×4 + 同模块布局互不雷同 + 服务卡不得缩放；3 项反向验证必红。
  **未触碰任何颜色 / 版式 token**，预览图与 manifest 保持原样。至此 4 风格 × 4 模块 = 16 套布局全部落地。
- **P4 后台两步 UI + 预览图重生成 + 守门改写** ✅ **已实现并测试通过**：`MpTheme.vue` 重写为
  左侧 sticky 固定预览区 + 右侧两步选择（用户反馈后定稿），保存只发两轴、兼容旧后端、
  树形归属自动清空色系；`gen_theme_previews.py` 从 `app.wxss` 抽布局规则原文按风格注入，
  12 张 PNG 全部重出并按每风格实测高度收口，`manifest.json` 增布局/画布快照；
  `MpTheme.spec.ts` / `MpSettings.spec.ts` / `theme.spec.ts` 改写，8 项反向验证必红、
  8 项全量门禁全绿。**本轮不动后端 / 迁移 / OpenAPI / 数据库。**
  **未纳入本期的后续工作**：白名单物理树化、`openapi` enum 收敛、真机像素级验收（本环境无真机截图工具链，预览图只是复刻示意、不能替代）。删 `template_id` 兼容列已在迁移 `0043` 落地，不再列于此。

> 每一期独立可交付、可回滚；P1 不触视觉，风险最低，建议先行。

## 12. P5 修订：从树形归属改为全叉乘（2026-11-12 用户反馈）

> **用户原话**：「为什么只有整体风格的经典蓝有品牌配色呢，我觉得整体风格的分类不应该按颜色分，
> 而应该按布局特色区分，每种风格都有对应所有品牌配色的设计。」
> 口径经一次确认定为「**统一色板 + 风格按布局特色改名**」。本节**推翻** §1 / §2.3 / §10 里
> 「树形、色系隶属于某个风格」的早期决策；上文保留作历史事实，不回改。

### 12.1 为什么之前是树形，为什么现在改

- P1 拍板「8 款 `brand-*` 全归 `default`」的理由是**存量零突变**：这些款原本就是「换色、
  版式沿用 default」，按色温分散到其它风格会让存量租户跳版式。随后 §10 又写了「色系为每个
  风格单独调校」作为树形理由——这等于把风格与颜色重新绑在一起，正是用户不认可的地方。
- 实际代码里，P2 已把 `app.wxss` 拆成 `.wc-style-*`（布局）与 `.wc-brand-*`（颜色）两层，
  根节点同时挂两个 class、颜色层后定义因而覆盖风格自带的默认色：**任意风格 + 任意色系本来
  就能正确渲染**。限制只来自几处清单：`mptemplate.StyleOfBrand` / `BelongsToStyle`、
  `content.go` 的跨风格 `code=1`、`theme.ts` 的 `normalizeSelection`，以及 P4 后台 UI 的
  禁用与自动清空。**不是技术做不到。**
- 4 款风格还全部按颜色命名（经典蓝 / 沉稳墨 / 暖金棕 / 松石青），与「风格 = 布局族」冲突；
  叠加品牌色后「沉稳墨 + 绛红」更显得名不副实。

### 12.2 拍板口径

| 项 | 决策 |
| --- | --- |
| 组合 | 4 风格 × (nil + 8 色系) = **36** 个合法组合，任意搭配 |
| 颜色层 | 8 款色系在 4 个风格上取值**逐字一致**（统一色板），不做逐风格调校；风格只管布局 |
| 默认色 | `brandColorId = null` 仍用该风格自带的默认色，存量租户零突变；它不是统一色板的一员 |
| 风格命名 | 显示名改为布局特色（标准均衡 / 紧凑信息 / 宽松亲和 / 疏排透气），**内部 id 不变**；说明文字不得含颜色词，由 Go 单测钉住 |
| 兼容列（已删） | `template_id` 早期只有 12 个扁平 id，装不下 36 组合 → 两轴列是真值。该兼容列已于迁移 `0043` 删除；`splitTheme` 仅保留在端上用于解析真正旧版本后端 / 旧缓存，不再有后端写链路。 |
| 端上缓存 | `wc_theme_id` 升级为带前缀的两轴格式，旧格式仍按老规则解析，旧缓存零迁移 |
| 预览图 | 按组合出 36 张（4 风格 × 9 色档），文件名用组合键；布局按 style、颜色按组合；manifest 改为 12 套色板 + combos 映射 |
| 授权 | 风格或色系**任一高级**即受 `mp_template_advanced` 约束（修掉 brand 短路） |

### 12.3 不变的东西

不改动两轴列本身（两轴列已在 `0042` 引入、`0043` 删除兼容列）、不动 4×4=16 套布局规则、不动 `app.wxss` 的两层机制、
不改 12 个 id 集合、不做运行时自由色值注入。存量 `(default, brand-*)`、`(ink, nil)` 等
组合逐字不变。

### 12.4 分期

- **P5-1 后端契约层** ✅：`mptemplate.go` 删 `StyleOfBrand` / `BelongsToStyle` 改
  `IsValidCombo`、4 款风格改名、`IsAdvancedCombo` 改 OR、兼容列注释写明近似语义；
  `content.go` 去掉跨风格拒绝；`openapi.yaml` 三处描述与 36 组合说明；Go 单测新增
  「36 组合全合法」「风格名不含颜色词且说明描述布局」；`handler_test.go` 改为
  「ink + brand-ocean / warm + brand-magenta 均放行」。
- **P5-2 端上** ✅：`normalizeSelection` 放开（风格与色系各自归一、不再打回 default）；
  `wc_theme_id` 升级为 `wc2:<styleId>|<brandColorId 或 ->`，删除 `deriveThemeId`
  这条压扁入口，旧扁平缓存仍走 `splitTheme` 零迁移；`themeBootstrap` 补跨风格响应断言，
  三份端上守门改写并做 4 项反向验证。
- **P5-3 后台** ✅：两步选择放开到 4 风格 × 9 色档，去掉禁用提示 / 自动清空 / 色卡置灰，
  切风格保留已选色系；`comboId()` 按组合键寻图，「默认色」色卡跟随当前风格默认主色；
  `MpTheme.spec.ts` 重写为 36 组合矩阵 + 跨风格保存断言。
- **P5-4 预览图** ✅：`gen_theme_previews.py` 改全叉乘出图，文件名
  `<styleId>--<brandColorId 或 default>.png`，`combo_tokens()` 保证「风格 18 token +
  色系 11 颜色、版式不被覆盖」；`manifest.json` 新增 `combos`（36 组合映射）与
  `comboTokens`（36 组合并 token 快照），保留 `tokens` 12 组基础色板；12 张旧的扁平
  id 图由脚本自行清理。跨端守门改为「36 张两两不同 + 同风格 9 档 / 同色档 4 风格各不同」。
- **P5-5** ✅：文档同步、8 项全量门禁、整期反向验证、提交。

> 体积实况与取舍：36 张 2x PNG 合计约 **43 MB**（原 12 张约 13.3 MB）。保留 `SCALE=2`
> 是因为后台预览窗口 280px、大图面板 420px 都要看清长图里的文字与列数，且页面**按需只
> 加载当前组合那一张**（约 0.85–1.85 MB），首屏不加载其余 35 张。若仓库体积成为问题，
> 第一步仍是降 `SCALE` 到 1（所有尺寸守门从脚本读 `scale`，自动适配），**绝不退回**
> 「4 张布局图 + 色卡条」——固定预览区需要按组合可切的一张完整长图。
