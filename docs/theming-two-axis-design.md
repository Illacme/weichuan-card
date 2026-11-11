# 主题系统两轴化设计（风格 × 色系）

> 状态：**P1（数据/契约层）**、**P2（CSS 两层 + 前 2 模块布局）**、**P3（剩余 2 模块布局）均已实现并测试通过**；P4（后台两步 UI / 预览图重生成 / 白名单树化 / 全量真机验收）待实施。
> 2026-11-11 用户确认方向：整体风格驱动各模块布局、品牌配色作为叠加在风格之上的色系调整；并确认布局深度为「整模块重排版」。
> 当前实现见 `docs/commercial-readiness.md` 第三十八轮：后端已完成 `StyleID`+`BrandColorID` 双列、`0042` 迁移、PUT/GET 两轴接口与跨风格归属校验，前端/小程序暂不动（`templateId` 兼容列续命）。
>
> **命名澄清（重要）**：设计稿里用的标签名「classic（经典蓝基线）」对应的**真实风格 id 是 `default`**（即 `mptemplate.DefaultID`），不是字面 `classic`。P1 实现中基础风格一律用 `default`；`brand-*` 全部归 `default` 风格之下。下文凡「classic」均指 `default` 这一风格。

## 0. 目标与不变式

- **风格（Style）** 决定小程序各功能模块的**布局语言**（顶部名片卡区、公司简介、经典业绩、业务服务的结构与排布各有特色）。
- **色系（BrandColor）** 是叠加在所选风格之上的**纯颜色层**，不改布局。
- 不破坏既有不变量：
  - 保存**只提交风格/色系**，不跨页签覆盖（`MpTheme.save` 只发 `{styleId, brandColorId}`，见 `frontend/src/views/content/MpTheme.vue:225` 现有 `{ templateId }`）。
  - 后端 PUT 指针语义（`backend/internal/handler/content.go:491` `TemplateID *string`），未提供字段保持原值。
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

### 2.1 现状（`backend/internal/model/models.go:280`）
```go
TemplateID string `gorm:"column:template_id;size:32;default:default" json:"templateId"`
```
单一字符串列 + 后端白名单 `backend/internal/mptemplate/mptemplate.go`（扁平 12 款，靠 `series` 分组）。

### 2.2 目标
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
- 迁移脚本（`0042`）把旧 `template_id` 拆成 `(style_id, brand_color_id)`；`template_id` 列保留作兼容派生列（写入时由两轴反推），便于回滚锚点。

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
>   版式」合并出 12 组 18 token，与 `manifest.json` 快照逐字相等 → **12 张 PNG 无需重出、
>   存量配色零突变**。真正按风格重出 4 张布局长图是 P4 的事。

## 4. 各模块 × 各风格的布局语言（设计核心，方向稿非像素定稿）

模块清单（与现有小程序首页一致）：① 顶部名片卡区 ② 公司简介 ③ 经典业绩 ④ 业务服务。

四种风格「性格」与对四模块的差异化处理：

| 风格 | 性格 | 顶部名片卡区 | 公司简介 | 经典业绩 | 业务服务 |
|------|------|------|------|------|------|
| **classic**（经典蓝基线） | 标准、均衡 | 居中头像+姓名职务+底部拨号/存通讯录 | 左 logo 右简介两段 | 竖向时间线/网格卡片 | 图标+文字列表 |
| **ink**（沉稳墨） | 硬朗、信息密度高 | 近直角卡、左对齐、头像小、信息紧凑一行 | 无图、纯文字分段、细描边分隔 | 紧凑网格、去卡片化、描边分隔 | 紧凑两列、文字优先 |
| **warm**（暖金棕） | 亲和、宽松 | 大圆角卡、头像大、胶囊按钮、暖投影 | 图文左右、圆角图、暖色留白 | 横向大卡流、圆角图片 | 圆角图标网格、宽松间距 |
| **teal**（松石青） | 清爽、透气 | 中圆角、半透磨砂卡、极轻投影 | 图文上下、留白大、细线分隔 | 轻卡片、微阴影、疏排 | 线性图标、疏排网格 |

> 每格是「该风格下该模块长什么样」的方向描述；落地时由 `app.wxss` 的 `.wc-style-<id> .mp-<module>` 规则 + 前端结构微调实现。这是工作量主体（4×4=16 套模块布局）。
>
> ✅ **P2 + P3 已落全 4 模块 = 16 套布局**：P2 落顶部名片卡 `.emp-card` / 公司简介
> `.mp-profile`，P3 落经典业绩 `.mp-achievements` / 业务服务 `.mp-services`。
> 落地口径比方向稿克制，四条硬约束：`default` 逐字复述既有度量（升级零突变）；
> **不增删 DOM、不隐藏元素**（「墨款无图」实现为封面压矮去圆角，`display:none` 会让客户
> 上传的公司视频悄悄消失）；规则一律锁在模块钩子内，绝不写全局 `.section` / `.intro`
> 去波及二级页与员工页；**服务卡标题烧在图片里**（有图时 `service-grid/index.wxml`
> 只渲染 `.svc-icon`、不渲染 `.svc-title`），差异化只能改列数 / 间距 / 圆角 / 分隔线，
> 绝不把 `.svc-icon` 缩成小图标或改 `aspect-ratio`，否则标题静默看不清。
> `index.wxml` 的四个钩子：`mp-card-area` / `mp-profile` / `mp-achievements` / `mp-services`。
> P3 用**列数**做真正可见的重排（分类 / 精选 / 服务）：`default` 4/2/4、`ink` 5/2/4、
> `warm` 2/1/2（精选改单列横向大卡流）、`teal` 3/2/3。

## 5. 后台 UI（`MpTheme.vue`）

- 分组从「按 `series` 两组」（`MpTheme.vue:147` 的 `groups`）改为**两步选择**：
  1. **选风格**：4 张风格卡，每张展示该风格的「模块布局示意长图」+ 默认色，标注其布局性格。
  2. **选色系**：在所选风格下，渲染该风格的 `BrandColors` 色卡条（含「默认色」），点选即叠加。
- 选中态 = `(styleId, brandColorId)`；`.tab-actions` 的「保存风格」payload 由 `{templateId}` 改为 `{styleId, brandColorId}`（其余两页签不变）。
- 仍嵌在「小程序设置」页签，复用既有 sticky 切换栏与统一保存/重新加载（`frontend/src/views/content/MpSettings.vue` 已就绪）。

## 6. 预览图生成（`scripts/gen_theme_previews.py`）

- 现状：每 `templateId` 一张长图，从 `app.wxss` token 生成（`MpTheme.vue:47` 注释）。
- 目标：
  - 按 **风格**生成 4 张「布局示意长图」（体现 §4 的模块排布差异）。
  - 色系以**色卡条**形式叠加渲染（同一布局 + 不同主色），不再为每 (风格×色系) 各出一张整图。
- 后台卡片：风格卡显示布局长图；色系卡显示「该布局 + 此色」的局部应用小图。

> **P2 现状**：仍是 12 张按组合出的长图，只把 `read_tokens()` 改成两层合并读取
> （`.wc-style-*` 全套 + `.wc-brand-*` 颜色 ∪ default 版式）。合并出的 18 token 逐字未变，
> `manifest.json` 与 12 张 PNG **全部无需重出**，`--check` 保持绿。上面「4 张布局长图 +
> 色卡条」是 **P4 目标**，随后台两步 UI 一起做。

## 7. 接口契约（`docs/openapi.yaml`）

> ✅ **P1 已落地**：公开/管理 GET 根级下发 `styleId` + `brandColorId`，并保留 `templateId` 派生兼容列；
> PUT 接受 `{ styleId?, brandColorId? }`（两者都缺时兼容拆 `templateId`）；`brandColorId` 必须属于该
> `styleId`，否则 `code=1`；高级风格/高级色系「新选」按 `mp_template_advanced` 回 `code=403`。
> `openapi.yaml` 三处（公开 GET / 管理 GET / 管理 PUT）已补两轴字段。

- **P2 已落地**：小程序 `utils/types.ts` 的 `HomeLayoutResp` 补 `styleId` / `brandColorId`
  （`types_contract.spec.ts` 与 `models.go` json tag 对账）；首页 `loadAll` 与冷启动
  `prefetchTheme` 都经 `resolveTheme` 消费两值并兼容旧后端。后台 `MpTheme.vue` 仍发
  `templateId`，由 P1 的兼容派生列兜住，两边零漂移。
- **P4 待做**：`templateId` enum 收敛、清单从扁平 `templates[]` 改为 `styles[]` + 每风格 `brandColors[]`
  （P1 刻意**保留扁平 12 款清单与 `series` 分组**，后台 UI 零改动；树化随两步 UI 一起做，避免单侧漂移）。

## 8. 后端 handler / model / 迁移

> ✅ **P1 已落地**（与下面「最终形态」的差异是刻意的兼容取舍，不是漏做）：
> - `models.go`：新增 `StyleID` + `BrandColorID`，**保留 `TemplateID` 作兼容派生列**（回滚锚点 + 旧端续命）。
> - `mptemplate.go`：白名单**仍是扁平 12 款**（`IsBrand`/`SeriesOf` 保留），另加一组两轴纯函数
>   `Split`/`DeriveTemplateID`/`IsAdvancedCombo`/`BelongsToStyle`/`NormalizeStyle`/`IsKnownBrandColor`/`StyleOfBrand`；
>   归属判定由 `StyleOfBrand()` 提供（P1 全部返回 `default`），**未做物理树化**。
> - `content.go`：GET 下发两值 + 兼容列；PUT 两轴解析 + 归属校验；授权 gate 改为两轴 `IsAdvancedCombo` + `changed` 比较。
> - `handler.go`：`publicTemplateID` → `publicStyleBrand(styleID, brandColorID)`，未开通时回落 `(default, nil)` 且不抹库。
> - 迁移 `0042_mp_theme_two_axis.sql`：加 `style_id`（DEFAULT `'default'`）/ `brand_color_id`（NULL）两列 + 按 §2.3 回填；
>   `template_id` **保留不删**（P1 写入时同步反推），`manifest_gen.go` 追加清单项。
> - `handler_test.go` 新增「风格两轴」子测试；`mptemplate_test.go` 新增两轴函数单测；`gofmt` / `go test ./...` 全绿。

- **P4 最终形态（待做）**：白名单物理树化并删除 `IsBrand` 平面判定、列表接口改返回树、前端两步 UI 消费树、
  确认无旧端后再删 `template_id` 列与兼容字段。

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
- **P4 待做**：
  - `frontend/src/views/content/MpTheme.spec.ts` / `MpSettings.spec.ts`：payload 改两值、UI 两步选择、源码级守门改断言两轴与两步结构。
  - 预览图改为 4 张布局长图 + 色卡条后，同步改写「12 张 PNG 与 Go 白名单一一对应」一类的守门。
  - 全量门禁（eslint / vue-tsc / Vitest / vite build / git diff --check）与**反向验证必做**（改回旧单层模型必红）。

## 10. 风险与待确认项

1. ~~**品牌色归属哪个 Style**~~ ✅ **已拍板**：8 款 `brand-*` 全部归 `default` 风格之下（零突变，见 §2.3）。
2. ~~**树 vs 叉乘**~~ ✅ **已定树形**：色系为每个风格单独调校，保证不撞风格；P1 以 `StyleOfBrand()` 表达归属，物理树化放 P4。
3. ~~**工作量爆炸**~~ ✅ **16 套模块布局已在 P2+P3 全部落地**，靠「4 风格 × 4 模块各有专属规则 +
   同模块布局互不雷同」守门替代人工逐套核对；**真机验收组合仍开放**（建议只覆盖「每风格默认色」
   +「每风格抽 1 个品牌色」，其余靠单元测试守门）。
4. **兼容期双写**：P1 起 `template_id` 与两轴列同步写，读路径以两轴为准、未回填行回退 `Split(template_id)`；
   旧端仍读 `templateId`。任何一处漏同步都会造成「后台显示与访客所见不一致且无人报错」，改这块必带反向验证。
5. **分期**：见 §11，先数据层后视觉层、先 2 模块后 4 模块 —— P1/P2/P3 已按此走完，只剩 P4。

## 11. 实施分期（建议）

- **P1 数据/契约层（不改视觉）** ✅ **已完成并测试通过**：`models.go` 双列（`StyleID`+`BrandColorID`） + `0042` 迁移（加两列 + 按映射回填，保留 `template_id` 兼容派生列）+ `mptemplate` 两轴函数（`Split`/`DeriveTemplateID`/`IsAdvancedCombo`/`BelongsToStyle`/`NormalizeStyle`/`IsKnownBrandColor`）+ 接口 GET 下发两值 / PUT 两值与跨风格归属校验 + `openapi` 三处补全 + 后端测试（含跨风格拒绝与高级色系 gate）。前端/小程序暂用 `templateId` 兼容字段，未改动。
- **P2 CSS 两层 + 顶部名片卡 / 公司简介布局**（2 模块）✅ **已实现并测试通过**：`app.wxss` 拆 `.wc-style-*`（4 风格，18 token + 2 模块布局规则）与 `.wc-brand-*`（8 色系，仅 11 颜色 token）；`theme.ts` 两轴 + 扁平缓存兼容；`index.ts` / `themeBootstrap.ts` / `types.ts` 接线；预览脚本两层合并读取；端上守门改写 + 4 项反向验证。后台与小程序 wxml 的顶层节点无需改动（`wc-theme` 基类保留）。**12 组组合 token 逐字未变，PNG 无需重出。** P2 才会给已选 `ink/warm/teal` 的租户带来首页这两个模块的可见外观变化；`default` 与 8 款 `brand-*` 零突变。
- **P3 经典业绩 / 业务服务布局**（剩余 2 模块）✅ **已实现并测试通过**：`app.wxss` 补齐
  `.mp-achievements` / `.mp-services` 的 4 风格规则，用**列数**做真正可见的重排
  （分类 / 精选 / 服务：default 4/2/4、ink 5/2/4、warm 2/1/2、teal 3/2/3），
  辅以圆角 / 间距 / 描边 / 微阴影；`default` 逐字复述基线零突变。
  守门扩到 4×4 + 同模块布局互不雷同 + 服务卡不得缩放；3 项反向验证必红。
  **未触碰任何颜色 / 版式 token**，预览图与 manifest 保持原样。至此 4 风格 × 4 模块 = 16 套布局全部落地。
- **P4 后台两步 UI + 预览图重生成 + 守门改写 + 全量真机验收**。

> 每一期独立可交付、可回滚；P1 不触视觉，风险最低，建议先行。
