#!/usr/bin/env python3
"""生成后台「风格模板」页用的**小程序首页风格示意图**。

**它不是真机截图**：后台是普通网页，跑不了小程序组件树；模板又随小程序版本提审，
没法在浏览器里动态加载 WXSS。第二十七轮起，它按真机截图复刻首页版式：
区块顺序、卡片密度、字号层级、视频控件与底部按钮都与 `pages/index/index.wxml`
及首页组件一致。

**通用平台产品，示意图里不能出现任何真实 / 演示企业客户信息**（第二十八轮）：
企业名、简称、LOGO、人名、电话、座机、邮箱、地址、公司简介一律换成明显的中性
占位；真人头像换成剪影，带企业名的 LOGO 换成「LOGO」占位块。`demoseed.go` 里的
演示身份值会在 --check 时反向核对，出现任意一个就拒绝出图。
保留的是 8 张分类图标 / 4 张服务插画 / 3 张通用行业照片：它们不含企业标识，
租户自己上传的内容本来也不随主题换色。

准的是：18 个 `--wc-*` token 驱动的页面底色 / 主色 / 圆角 / 按钮 / 投影，
以及通用素材带来的版式与密度。不准的是：真机字体渲染、系统状态栏图标、
微信胶囊的真实质感、视频号播放器的真实画面、首页背景图与安全区。
**最终观感必须走 `docs/real-device-acceptance.md` §7 的真机验收**，示意图不能替代。

**P5：按「风格 × 色系」两轴全叉乘出图**。4 个风格 × (默认色 + 8 款品牌色系)
= 36 个组合，文件名是 `<styleId>--<brandColorId 或 default>.png`，两轴都进文件名，
不再压扁成 12 个扁平 templateId。模块节点用 `app.wxss` 的真实钩子类名
（`.emp-card` / `.mp-profile` / `.mp-achievements` / `.mp-services` 及其子元素），
再把 `.wc-style-<id> ...` 规则**原文**（去掉前缀、rpx 换 px）注入 `<style>`：
列数 / 间距 / 圆角 / 封面高度来自端上同一份 WXSS，不存在「另写第二套布局」。
每张图的 18 个 token = 该风格的全套 18 个（含 7 个版式 token）+ 色系的 11 个颜色
覆盖；**色系绝不覆盖版式 token**，否则「紧凑信息 + 绛红」会悄悄长成默认圆角。
画布按所属风格的内容高度自然收口，底栏始终紧贴内容底部。

用法：
    python3 scripts/gen_theme_previews.py            # 重新生成全部预览图
    python3 scripts/gen_theme_previews.py --check    # 只校验产物（CI / 提交前用）
    python3 scripts/gen_theme_previews.py --measure  # 只实测各风格内容高度，不出图

--check 不需要浏览器，查「36 个组合都有图 / PNG 合法 / 按风格尺寸一致 /
token + 布局规则 + 组合映射快照与 app.wxss 同版 / 通用素材齐全 / 区块与按钮
文案同源 / 不含任何真实客户身份」。生成需要本机 Chrome
（可用 THEME_PREVIEW_CHROME 指定路径）。
**改了 app.wxss 的配色 / 圆角 / 布局规则、改了首页区块、换了素材、改了占位文案、
或加了一款模板，都必须重跑本脚本**：否则图片会悄悄停在旧样式。
"""
from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import struct
import subprocess
import sys
import tempfile
from pathlib import Path
from string import Template

ROOT = Path(__file__).resolve().parent.parent
APP_WXSS = ROOT / "miniprogram/app.wxss"
GO_TPL = ROOT / "backend/internal/mptemplate/mptemplate.go"
DEMO_GO = ROOT / "backend/internal/demoseed/demoseed.go"
INDEX_WXML = ROOT / "miniprogram/pages/index/index.wxml"
ACTION_WXML = ROOT / "miniprogram/components/action-bar/index.wxml"
SEED_DIR = ROOT / "backend/internal/demoseed/seedassets"
OUT_DIR = ROOT / "frontend/public/theme-previews"
MANIFEST = OUT_DIR / "manifest.json"

# 画布按 iPhone 逻辑像素 375pt 设计；小程序 750rpx = 屏宽，故 1rpx = 0.5px。
# P4 起高度按**风格**收口：HEIGHT 是四风格中的最大画布（短风格用各自更小的高度，
# 底栏仍紧贴内容底部），CONTENT_BOTTOM 是允许的最大内容底部。两者仍由
# 「内容底部 + 呼吸 + 底栏」算出，绝不手填一个带大尾巴的数。
# 出图前会逐风格实测一次（measure_content_bottom）：改了布局让某风格内容变长时，
# 底栏会悄悄盖住业务服务卡，光靠常量算不出来，必须显式拒绝出图。
WIDTH, HEIGHT = 375, 2580
# CONTENT_BOTTOM 是允许的**上界**（实测最大底部 + 一点余量），不是精确值：
# Chrome 版本 / 字体渲染差 1~2px 不该让出图直接失败。
# P6（2026-11-12）名片区改成版式级差异后，warm 的竖版居中名片把姓名 / LOGO /
# 公司名 / 按钮全部上下叠放，实测内容底部从 2326 抬到 2490，上界同步上调到 2500。
CONTENT_BOTTOM, TAIL_GAP, BAR_HEIGHT = 2500, 20, 60
if HEIGHT != CONTENT_BOTTOM + TAIL_GAP + BAR_HEIGHT:
    raise SystemExit("画布高度必须由「内容底部 + 呼吸 + 底栏」算出，别再手填一个带大尾巴的数")
# 实测时给 .phone 一个足够高的临时画布，让内容自然流完再读 service 的真实 bottom；
# 否则 .phone 高度会反过来限制测量，出现「永远刚好装得下」的假绿。
MEASURE_HEIGHT = 4000
RPX_TO_PX = 0.5
SCALE = 2  # 2x 出图，后台缩小显示仍可辨识，也可直接当销售材料用

# P5：两轴全叉乘。4 个风格 × (默认色 + 8 款色系) = 36 个组合。
# 文件名用 "<style>--<brand 或 default>.png"：两轴都进文件名，谁也不压扁谁。
# 后端 template_id 兼容派生列只有 12 个扁平 id，装不下 36 个组合，不能拿来当图名。
DEFAULT_STYLE = "default"
STYLE_IDS = ("default", "ink", "warm", "teal")
COMBO_SEP = "--"
NO_BRAND = "default"
MODULE_HOOKS = (".emp-card", ".mp-profile", ".mp-achievements", ".mp-services")

# 只引用**通用行业素材**：分类图标、服务插画、公司简介封面与两张案例建筑照片。
# 它们不含企业名 / 人名 / LOGO；照片与插画跟真机一样不随模板换色。
# 刻意不引用真人头像与带企业名的 LOGO（见 check_no_identity）：我们是通用平台产品，
# 示意图里出现具体客户身份，客户会误以为这是别人的真实案例。
SEED_FILES = [
    "cover-company.jpg",
    "cover-daxing.jpg",
    "cover-hospital.jpg",
    *[f"cat-{i}.png" for i in range(1, 9)],
    *[f"svc-{i}.png" for i in range(1, 5)],
]

# 全部是明显的中性占位：一眼能看出是示意图，不会让客户误以为这是某个真实客户。
# 字数 / 行数刻意贴近真实内容的密度（名片两列联系表、公司简介 4 行），
# 这样脱敏不会把上一轮刚复刻好的版式又压扁。
PLACEHOLDER_TEXT = {
    "short_name": "示例企业",
    "full_name": "示例企业全称有限公司",
    "address": "示例地址 · 仅用于版式示意",
    "person": "示例联系人",
    "title": "示例职务",
    "phone": "138-0000-0000",
    "tel": "0000-00000000",
    "email": "name@example.com",
    "intro": "这里是企业简介的示例文字，用于展示本区域的排版密度与行距；正式使用时由企业在后台自行填写，示意图不包含任何真实客户资料。",
}

# 只回查**不含身份信息**的区块标题 / 按钮文案 / 组件注释，防止首页改了而图没跟上。
DEMO_SOURCES = {
    INDEX_WXML: ["经典业绩", "更多案例", "业务服务"],
    ACTION_WXML: ["一键拨号", "存入通讯录"],
    # 首页组件把标题画进图片里，这两处注释必须还在，否则预览可能退回占位块
    ROOT / "miniprogram/components/achievement-grid/index.wxml": ["标题已绘制在图内"],
    ROOT / "miniprogram/components/service-grid/index.wxml": ["标题已绘制在图内"],
}

# 真实 / 演示客户身份的硬黑名单。前半段静态兜底（地址、座机、邮箱后缀、简介片段、
# 身份素材名），后半段由 demoseed.go 的 Demo* 常量动态补齐：以后谁改了演示企业名，
# 不用同步改这里也能拦住。注意别把「示例」两个字加进来。
IDENTITY_LITERALS = [
    "廊坊", "紫金城", "0316-", "@163.com", "成立于 2015", "langhui",
    "avatar-gaolixin.png", "logo.png", "13832603521", "高立新", "朗汇",
]
GO_IDENTITY_RE = re.compile(
    r'Demo(?:CompanyName|ShortName|LoginCode|EmployeePhone|EmployeeName|AdminUser)\s*=\s*"([^"]+)"'
)

TOKEN_RE = re.compile(r"(--wc-[\w-]+)\s*:\s*([^;]+);")
DEFAULT_BLOCK_RE = re.compile(r"page\s*\{([^{}]*--wc-primary[^{}]*)\}")
STYLE_BLOCK_RE = re.compile(r"\.wc-style-([a-z0-9_-]+)\s*\{([^{}]*)\}")
BRAND_BLOCK_RE = re.compile(r"\.wc-brand-([a-z0-9_-]+)\s*\{([^{}]*)\}")
# 布局规则：行首 .wc-style-<id> + 选择器 + { ... }。选择器允许逗号换行（如
# `.mp-profile .cv, .mp-profile .cv-video`），故用 M|S 且只允许非花括号字符。
# 注意它**不会**误吃上面的 token 块：token 块的 id 后面直接是 `{`，
# `([^{}]+?)` 至少要吃掉一个字符才能接 `{`。
STYLE_RULE_RE = re.compile(
    r"^\.wc-style-([a-z0-9_-]+)\s+([^{}]+?)\s*\{([^{}]*)\}", re.M | re.S
)
GO_ID_RE = re.compile(r'ID:\s*(DefaultID|"([a-z0-9_-]+)")')
GO_DEFAULT_RE = re.compile(r'DefaultID\s*=\s*"([a-z0-9_-]+)"')


def is_layout_token(key: str) -> bool:
    """版式 token 只由风格层提供；颜色层一个都不许写（theme.spec.ts 会逐项拦）。"""
    return key.startswith("--wc-r-") or key.startswith("--wc-card-shadow")


def pairs(block: str) -> dict[str, str]:
    # strip 与 theme.spec.ts 的 readThemeVars() 一致，否则快照对账会被尾随空格搞出假红
    return {k: v.strip() for k, v in TOKEN_RE.findall(block)}


def read_tokens() -> dict[str, dict[str, str]]:
    """从 app.wxss 的两层主题块读出 12 组**基础色板**（与 theme.spec.ts 同一口径）。

    P2 起根节点同时挂 .wc-style-*（布局层，给全套 18 个 token）与 .wc-brand-*
    （颜色层，只给 11 个颜色 token）。这里给品牌款补上 default 的 7 个版式 token
    凑成 18 个，供 manifest 快照与守门对账。P5 出图不直接用这组，
    而是由 combo_tokens() 按「风格 18 个 + 色系 11 个颜色」现算 36 组。
    """
    src = APP_WXSS.read_text(encoding="utf-8")
    m = DEFAULT_BLOCK_RE.search(src)
    if not m:
        raise SystemExit("app.wxss 找不到 page 上的默认 token 块")
    default = pairs(m.group(1))
    layout = {k: v for k, v in default.items() if is_layout_token(k)}
    out: dict[str, dict[str, str]] = {"default": default}
    for t in STYLE_BLOCK_RE.finditer(src):
        out[t.group(1)] = pairs(t.group(2))
    for t in BRAND_BLOCK_RE.finditer(src):
        merged = dict(layout)
        merged.update(pairs(t.group(2)))
        out["brand-" + t.group(1)] = merged
    return out


def combo_id(style: str, brand: str | None) -> str:
    """两轴 → 预览图文件名主键。扁平 templateId 表达不了 36 个组合，别再退回它。"""
    return f"{style}{COMBO_SEP}{brand or NO_BRAND}"


def all_combos(brand_ids: list[str]) -> dict[str, tuple[str, str | None]]:
    """4 风格 × (默认色 + 8 款色系) = 36 个组合，顺序与后台两步选择一致。"""
    return {
        combo_id(s, b): (s, b)
        for s in STYLE_IDS
        for b in [None, *brand_ids]
    }


def combo_tokens(
    base: dict[str, dict[str, str]], style: str, brand: str | None
) -> dict[str, str]:
    """组合的 18 token = 该风格全套 + 色系的 11 个颜色覆盖，版式 token 不被覆盖。

    直接 base[style].update(base[brand]) 会把品牌款里补齐的 **default 版式** 也
    写进来，于是「紧凑信息 + 绛红」的近直角会悄悄变回默认圆角；客户看不到报错，
    只会觉得换了风格没反应。所以这里按 key 逐个过滤。
    """
    merged = dict(base[style])
    if brand:
        for k, v in base[brand].items():
            if not is_layout_token(k):
                merged[k] = v
    return merged


def read_layout_rules() -> dict[str, list[str]]:
    """把 app.wxss 的 `.wc-style-<id> <模块选择器>{...}` 规则按风格分组取出原文。

    预览 HTML 只用这些规则画模块布局，不再自己写一套列宽 / 间距 / 圆角：
    端上改了布局，重新出图就能跟上；没出图则 manifest 的布局快照会漂移，
    --check 直接判红。返回值是「选择器 -> 已经过 rpx→px 的规则文本」列表，
    顺序与 WXSS 一致，方便快照逐字对账。
    """
    src = APP_WXSS.read_text(encoding="utf-8")
    out: dict[str, list[str]] = {sid: [] for sid in STYLE_IDS}
    for m in STYLE_RULE_RE.finditer(src):
        sid = m.group(1)
        if sid not in out:
            raise SystemExit(f"app.wxss 出现未登记的风格布局规则：{sid}")
        selector = re.sub(r"\.wc-style-[a-z0-9_-]+\s+", "", m.group(2)).strip()
        body = to_css_value(m.group(3)).strip()
        out[sid].append(f"{selector} {{ {body} }}")
    for sid, rules in out.items():
        joined = "\n".join(rules)
        missing = [h for h in MODULE_HOOKS if h not in joined]
        if missing:
            raise SystemExit(f"风格 {sid} 缺模块布局规则：{missing}")
    return out


def layout_css(rules: list[str]) -> str:
    return "\n  ".join(rules)


def manifest_payload(
    tokens: dict[str, dict[str, str]],
    layouts: dict[str, str],
    bottoms: dict[str, int],
    heights: dict[str, int],
    combos: dict[str, tuple[str, str | None]],
    combo_tok: dict[str, dict[str, str]],
) -> dict:
    """随图写基础色板 / 36 组合并 token / 组合映射 / 布局 / 画布 / 素材 / 占位快照。

    改了任一样却没重出图，守门能查出图旧了。comboTokens 是 P5 的核心快照：
    它钉住「色系只覆盖颜色、版式仍由风格层给」，否则换风格只换色这种静默失败
    在「36 张图都在 / 尺寸都对」这类检查下会一路绿灯。
    """
    return {
        "source": "miniprogram/app.wxss",
        "canvas": [WIDTH, HEIGHT],
        "scale": SCALE,
        "canvasByStyle": heights,
        "contentBottoms": bottoms,
        "layouts": layouts,
        "combos": {
            cid: {"styleId": s, "brandColorId": b} for cid, (s, b) in combos.items()
        },
        "comboTokens": combo_tok,
        "seedAssets": SEED_FILES,
        "placeholders": PLACEHOLDER_TEXT,
        "tokens": tokens,
    }


def write_manifest(
    tokens: dict[str, dict[str, str]],
    layouts: dict[str, str],
    bottoms: dict[str, int],
    heights: dict[str, int],
    combos: dict[str, tuple[str, str | None]],
    combo_tok: dict[str, dict[str, str]],
) -> None:
    MANIFEST.write_text(
        json.dumps(
            manifest_payload(tokens, layouts, bottoms, heights, combos, combo_tok),
            ensure_ascii=False, indent=2, sort_keys=True,
        ) + "\n",
        encoding="utf-8",
    )


def check_sources() -> list[str]:
    """出图依赖的通用素材必须还在，区块 / 按钮文案仍与小程序源码逐字同源。"""
    problems = []
    for name in SEED_FILES:
        if not (SEED_DIR / name).is_file():
            problems.append(f"缺通用素材：seedassets/{name}")
        elif name.startswith("avatar-") or name.startswith("logo."):
            problems.append(f"通用素材清单里混进了身份素材：{name}")
    for path, keys in DEMO_SOURCES.items():
        if not path.is_file():
            problems.append(f"预览依赖的源码不存在：{path.relative_to(ROOT)}")
            continue
        src = path.read_text(encoding="utf-8")
        for key in keys:
            text = PLACEHOLDER_TEXT.get(key, key)
            if text not in src:
                problems.append(f"{path.name} 里找不到区块 / 按钮文案「{text}」")
    return problems


def identity_tokens() -> list[str]:
    """静态黑名单 + demoseed.go 里所有演示身份常量值，二者取并集。"""
    out = list(IDENTITY_LITERALS)
    out.extend(m.group(1) for m in GO_IDENTITY_RE.finditer(DEMO_GO.read_text(encoding="utf-8")))
    return out


def check_no_identity(html: str) -> list[str]:
    """通用平台产品：示意图里出现任何真实 / 演示客户身份都必须拒绝出图。

    查的是**代入占位文案后的最终 HTML**，不是脚本源码：脚本为了守门会列出黑名单
    字面量，查源码只会得到「永远通过」的假绿（第二十七轮踩过同样的坑）。
    """
    problems = []
    for token in identity_tokens():
        if token and token in html:
            problems.append(f"示意图出现真实客户身份信息「{token}」")
    for key in ("short_name", "full_name", "person", "email"):
        if PLACEHOLDER_TEXT[key] not in html:
            problems.append(f"示意图缺中性占位「{key}={PLACEHOLDER_TEXT[key]}」")
    return problems


def check_manifest(
    tokens: dict[str, dict[str, str]],
    layouts: dict[str, str],
    bottoms: dict[str, int],
    heights: dict[str, int],
    combos: dict[str, tuple[str, str | None]],
    combo_tok: dict[str, dict[str, str]],
) -> list[str]:
    if not MANIFEST.is_file():
        return ["缺 manifest.json（无法证明预览图与 app.wxss / 素材同版）"]
    try:
        data = json.loads(MANIFEST.read_text(encoding="utf-8"))
    except (OSError, ValueError) as e:
        return [f"manifest.json 不是合法 JSON：{e}"]
    problems = []
    if data.get("tokens") != tokens:
        problems.append("基础色板快照与 app.wxss 当前值不一致：改了配色 / 圆角却没重跑本脚本")
    if data.get("comboTokens") != combo_tok:
        problems.append("36 组合并 token 快照不一致：风格 / 色系叠加规则变了却没重出图")
    expected_combos = {
        cid: {"styleId": s, "brandColorId": b} for cid, (s, b) in combos.items()
    }
    if data.get("combos") != expected_combos:
        problems.append("组合映射快照不一致：重出图后后台会按新组合寻图，旧图会 404")
    if data.get("layouts") != layouts:
        problems.append("布局规则快照与 app.wxss 当前值不一致：改了模块布局却没重跑本脚本")
    if data.get("canvas") != [WIDTH, HEIGHT] or data.get("scale") != SCALE:
        problems.append("manifest 画布参数与脚本不一致")
    if data.get("canvasByStyle") != heights:
        problems.append("manifest 的各风格画布高度与本次实测不一致")
    if data.get("contentBottoms") != bottoms:
        problems.append("manifest 的各风格内容底部与本次实测不一致")
    bar_top = HEIGHT - BAR_HEIGHT
    for sid, bottom in bottoms.items():
        if bottom + TAIL_GAP > bar_top:
            problems.append(
                f"风格 {sid} 内容底部 {bottom} + 呼吸 {TAIL_GAP} 已超出底栏顶 {bar_top}，"
                f"必须调大 CONTENT_BOTTOM / HEIGHT 后重出图"
            )
    if sorted(bottoms) != sorted(STYLE_IDS) or sorted(heights) != sorted(STYLE_IDS):
        problems.append("manifest 缺某个风格的实测高度（四风格必须逐个实测）")
    if data.get("seedAssets") != SEED_FILES:
        problems.append("manifest 的素材清单与脚本不一致")
    if data.get("placeholders") != PLACEHOLDER_TEXT:
        problems.append("manifest 的占位文案与脚本不一致：换了示意图文案却没重出图")
    if data.get("source") != "miniprogram/app.wxss":
        problems.append("manifest 的 token 来源被改掉了")
    return problems


def read_go_ids() -> list[str]:
    """模板 id 与展示顺序以 Go 白名单为准（DefaultID 那一项写成 ID: DefaultID）。"""
    go = GO_TPL.read_text(encoding="utf-8")
    m = GO_DEFAULT_RE.search(go)
    if not m:
        raise SystemExit("mptemplate.go 找不到 DefaultID")
    default_id = m.group(1)
    return [(x.group(2) or default_id) for x in GO_ID_RE.finditer(go)]


def to_css_value(value: str) -> str:
    """把 token 里的 rpx 换成浏览器认识的 px；色值 / rgb 三元组原样保留。"""
    return re.sub(
        r"(\d+(?:\.\d+)?)rpx",
        lambda m: f"{float(m.group(1)) * RPX_TO_PX:g}px",
        value,
    )


def css_tokens(tokens: dict[str, str]) -> str:
    return "\n".join(f"    {k}: {to_css_value(v)};" for k, v in tokens.items())


# 按真机截图与 pages/index/index.wxml 的区块顺序 1:1 复刻。
# 每个 data-sec 与真实首页一一对应，theme.spec.ts 会逐个对账。
# 18 个 --wc-* token 必须全部以 var() 引用（定义了却没用 = 假同源）；
# 图形全部是 seedassets 里的真实素材，主题色只出现在 var() 里。
#
# P4：模块节点改用 app.wxss 的真实钩子类名，${style_css} 注入该风格的布局规则原文。
# 基础样式只给「结构与颜色」，列数 / 间距 / 封面高度 / 圆角等**布局度量一律由注入规则给**，
# 避免这里维护成与端上分叉的第二套版式。
HTML = Template("""<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<style>
  * { box-sizing: border-box; }
  html, body { margin: 0; padding: 0; width: ${w}px; height: ${h}px; overflow: hidden; }
  body { color: #222; font: 12px/1.5 -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif; }
  /* 真机整页是浅蓝底 + 白卡浮于其上，不是灰白底：这是上一版最刺眼的差异之一 */
  .phone {
    width: ${w}px; height: ${h}px; position: relative; padding-bottom: ${tail_gap}px;
    background: linear-gradient(180deg, var(--wc-primary-soft), var(--wc-primary-tint));
${tokens}
  }
  /* 状态栏与微信胶囊只画占位：真机的系统图标 / 安全区不该被伪造进示意图 */
  .status { height: 26px; display: flex; align-items: center; justify-content: space-between;
            padding: 0 14px; font-size: 11px; font-weight: 600; color: #1a1a1a; }
  .status .sys { font-size: 9px; font-weight: 400; color: #4a5563; letter-spacing: 1px; }
  .nav { height: 42px; display: flex; align-items: center; justify-content: space-between;
         padding: 0 10px; position: relative; }
  .me { display: flex; align-items: center; gap: 4px; height: 26px; padding: 0 8px 0 3px;
        border: 1px solid var(--wc-primary-border); border-radius: var(--wc-r-chip);
        background: #fff; color: var(--wc-primary); font-size: 10px; }
  .me-avatar { width: 20px; height: 20px; border-radius: 50%; flex: none;
               background: var(--wc-primary-chip); border: 1px solid var(--wc-primary-border); }
  .nav-title { position: absolute; left: 0; right: 0; text-align: center;
               font-size: 14px; font-weight: 700; color: #1a1a1a; pointer-events: none; }
  .wx-capsule { width: 76px; height: 28px; border-radius: 15px; background: rgba(255,255,255,.92);
                border: 1px solid #e3e8ef; display: flex; align-items: center;
                justify-content: space-evenly; font-size: 12px; color: #222; }
  .wx-capsule .sep { width: 1px; height: 14px; background: var(--wc-primary-chip); }
  .wx-capsule .dot { width: 12px; height: 12px; border-radius: 50%; border: 2px solid #222;
                     display: flex; align-items: center; justify-content: center; }
  .wx-capsule .dot::after { content: ""; width: 4px; height: 4px; border-radius: 50%; background: #222; }
  .card, .section { background: #fff; margin: 12px 10px 0; padding: 12px;
                     border-radius: var(--wc-r-card); }
  .card { margin-top: 6px; box-shadow: var(--wc-card-shadow); }
  .section { box-shadow: var(--wc-card-shadow-soft); }

  /* ---- 名片卡：头像 / 姓名职位 / 企业 LOGO / 公司全称 / 查看案例 / 地址 / 两列联系表 ----
     类名与 app.wxss 的 .emp-card 规则对齐（.ec-avatar / .ec-rows / .ec-line / .case-btn），
     尺寸由 ${style_css} 注入的布局规则覆盖，这里只给形状与颜色。 */
  .ec-head { display: flex; align-items: flex-start; }
  /* 头像与 LOGO 用中性占位，不引用真人照片 / 带企业名的标识：
     通用平台产品的示意图不能让客户误以为这是某家真实企业的名片。 */
  .avatar-ph, .ec-avatar { width: 56px; height: 56px; border-radius: 50%; flex: none; position: relative;
               overflow: hidden; background: var(--wc-primary-tint); border: 2px solid #fff;
               box-shadow: 0 2px 6px rgba(var(--wc-primary-rgb), .18); }
  .avatar-ph::before, .ec-avatar::before { content: ""; position: absolute; left: 50%; top: 10px;
               transform: translateX(-50%); width: 15px; height: 15px; border-radius: 50%;
               background: var(--wc-primary-border); }
  .avatar-ph::after, .ec-avatar::after { content: ""; position: absolute; left: 50%; bottom: -6px;
               transform: translateX(-50%); width: 30px; height: 22px;
               border-radius: 15px 15px 0 0; background: var(--wc-primary-border); }
  .ec-info { flex: 1; min-width: 0; margin-left: 10px; }
  .ec-line1 { display: flex; align-items: center; justify-content: space-between; }
  .ec-name { font-size: 17px; font-weight: 700; color: #222; }
  .logo-ph { width: 64px; height: 23px; flex: none; display: flex; align-items: center;
             justify-content: center; font-size: 11px; font-weight: 700; letter-spacing: 1px;
             color: var(--wc-primary-deep); background: var(--wc-primary-soft);
             border: 1px dashed var(--wc-primary-border); border-radius: var(--wc-r-chip); }
  .ec-title { color: #6b7a89; font-size: 11px; margin-top: 1px; }
  .ec-line3 { display: flex; align-items: center; justify-content: space-between; margin-top: 8px; gap: 6px; }
  .ec-cname { color: var(--wc-primary-deep); font-size: 12.5px; font-weight: 700;
              white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .case-btn { flex: none; font-size: 9px; color: var(--wc-primary);
              background: var(--wc-primary-soft); border: 1px solid var(--wc-primary-border);
              border-radius: var(--wc-r-chip); padding: 2px 6px; }
  .ec-addr { color: #5f6f80; font-size: 9.5px; margin-top: 4px; }
  /* 联系表：与真机同构——.ec-rows 里先是通栏双色分隔线，再是 .ec-contact 两列
     （左列手机/副号、右列座机/邮箱）。曾经把 .ec-rows-line 放到 .ec-rows 外面、
     且让 .ec-rows 自己 flex 两列，于是 app.wxss 里针对 .ec-contact 的版式规则
     （ink/warm 的「单列四行」）在预览图上完全落不下去，图就成了第二套布局。 */
  .ec-rows { margin-top: 11px; }
  /* 分隔线高度刻意写 2px 而不是严格 2rpx→1px：真机上它是 2rpx，在 3x 屏渲染成 3 个
     物理像素，是一条清晰的细线；预览按 2x 出图、后台还要缩到卡片里显示，1px 会整条消失。
     宽度 / 分段比例 / 外边距仍与 index.wxss 一致（通栏、左 34% 品牌色）。 */
  .ec-rows-line { height: 2px; width: 100%; margin-bottom: 8px;
    background: linear-gradient(90deg, var(--wc-primary-deep) 0, var(--wc-primary-deep) 34%,
                var(--wc-track) 34%, var(--wc-track) 100%); }
  .ec-contact { display: flex; align-items: flex-start; }
  .ec-col { min-width: 0; }
  .ec-contact-short .ec-col { flex: 0 0 50%; }
  .ec-line { display: flex; align-items: baseline; font-size: 10.5px; color: #333; padding: 4px 0; }
  .ec-label { width: 28px; flex: none; color: #98a6b5; font-size: 10px; }
  .ec-val { white-space: nowrap; }

  /* 左蓝右灰分段线：与 pages/index/index.wxss 的 .line-split 同源。
     企业名牌下那条是通栏且紧贴名称（margin 0），板块标题下那条只有短一截（.sec-line）。
     高度同 .ec-rows-line 一样刻意取 2px，理由见上。 */
  .line-split { height: 2px;
    background: linear-gradient(90deg, var(--wc-primary-deep) 0, var(--wc-primary-deep) 34%,
                var(--wc-track) 34%, var(--wc-track) 100%); }
  .company-name-line { margin: 0; }
  /* .pf-text 与真机 index.wxml 同构：把「公司名 / 分隔线 / 简介」三节点包成一栏，
     风格层才做得出来「左图右文」（teal）与「左窄栏公司名 + 右通栏简介」（ink）。
     default 款不给它写规则，视觉零变化。 */
  .pf-text { min-width: 0; }

  /* ---- 公司简介：真实封面图 + 视频控件 + 黑色粗体公司名 + 4 行简介 ----
     .cv / .cv-video / .company-name / .intro 的高度与行距由布局规则注入。 */
  .cv { position: relative; height: 220px; border-radius: var(--wc-r-card-bg);
        overflow: hidden; box-shadow: var(--wc-card-shadow-bg);
        /* 封面宽度不再恒等于卡片宽：teal 的「左图右文」把它压到 42%。
           图上的「走进 XX」大字与假进度条是按通栏宽度画的装饰，窄栏里会叠成一团，
           所以这里建一个内联尺寸容器，窄于 200px 时换一套小号装饰（见文件末尾）。 */
        container-type: inline-size; }
  .cv > img { width: 100%; height: 100%; object-fit: cover; display: block; }
  /* 封面是租户上传的照片/视频帧，真机上**不随模板换色**。这里只加中性暗角
     让白色大字可读，绝不能用主色去染它——否则客户会以为换了风格连视频也变色。 */
  .cover-shade { position: absolute; inset: 0;
    background: linear-gradient(180deg, rgba(0,0,0,.18), rgba(0,0,0,.04) 45%, rgba(0,0,0,.28)); }
  .cover-word { position: absolute; left: 12px; right: 12px; top: 62px; display: flex;
                justify-content: space-between; font-size: 26px; font-weight: 700;
                color: rgba(255,255,255,.75); text-shadow: 0 2px 8px var(--wc-primary-grad); }
  .play { position: absolute; left: 50%; top: 50%; transform: translate(-50%,-50%);
          width: 40px; height: 40px; border-radius: 50%; background: rgba(255,255,255,.92);
          box-shadow: 0 2px 10px rgba(0,0,0,.25); display: flex; align-items: center; justify-content: center; }
  .play::after { content: ""; width: 0; height: 0; margin-left: 3px;
                 border-left: 13px solid var(--wc-primary); border-top: 8px solid transparent;
                 border-bottom: 8px solid transparent; }
  .video-bar { position: absolute; left: 0; right: 0; bottom: 0; height: 24px; padding: 0 8px;
               display: flex; align-items: center; gap: 6px; color: #fff; font-size: 9px;
               background: linear-gradient(0deg, rgba(0,0,0,.45), rgba(0,0,0,0)); }
  .v-track { flex: 1; height: 2px; background: var(--wc-primary-chip); border-radius: 2px; position: relative; }
  .v-track::after { content: ""; position: absolute; left: 0; top: -2px; width: 6px; height: 6px;
                     border-radius: 50%; background: #fff; }
  /* 窄封面（teal 的左图右文把封面压到 42%，约 137px）里，「走进 XX」大字和假进度条
     按通栏尺寸画会叠成一团、时间码还会溢出右缘。装饰整体降一档，字号与播放键同步缩小。
     真机上这块是客户的真实封面 / 视频，宽度不同时由 video 自己的控件适配，
     所以这套缩小只服务示意图，不进 app.wxss。 */
  @container (max-width: 200px) {
    .cover-word { font-size: 12px; top: 8px; left: 8px; right: 8px; }
    .play { width: 26px; height: 26px; }
    .play::after { border-left-width: 9px; border-top-width: 6px; border-bottom-width: 6px; }
    .video-bar { height: 14px; padding: 0 4px; gap: 3px; font-size: 6px; }
    .v-track::after { width: 4px; height: 4px; top: -1px; }
  }
  .company-name { font-size: 14.5px; font-weight: 700; color: #1a1a1a; margin-top: 10px; }
  .intro { font-size: 13px; line-height: 1.75; color: #4b5563; }

  /* ---- 经典业绩 / 业务服务 ----
     容器用 flex-wrap，列宽 25% / 20% / 50% / 33.33% 全部由布局规则给（grid 会忽略
     子节点 width，那样四风格就画不出差异了）。服务卡标题烧在图片里，与真机一样
     有图时只渲染 .svc-icon，不另画文字标题。 */
  .sec-head { display: flex; align-items: center; justify-content: space-between; }
  .sec-title { font-size: 15px; font-weight: 700; color: #1a1a1a; }
  .sec-more { font-size: 10px; color: var(--wc-primary); }
  .sec-line { height: 2px; width: 46px; margin: 6px 0 10px;
    background: linear-gradient(90deg, var(--wc-primary-deep) 0, var(--wc-primary-deep) 28%,
                var(--wc-track) 28%, var(--wc-track) 100%); }
  .cat-grid, .featured, .svc-grid { display: flex; flex-wrap: wrap; align-items: flex-start; }
  .cat-item, .feat-item, .svc-item { box-sizing: border-box; }
  .cat-icon, .feat-cover, .svc-icon { width: 100%; display: block;
    border-radius: var(--wc-r-card-bg); }
  .feat-cover { object-fit: cover; }
  .cat-name { font-size: 10px; color: #6b7a89; text-align: center; margin-top: 3px; }
  .featured { margin-top: 10px; }
  .svc-title { font-size: 11px; color: #333; text-align: center; margin-top: 4px; }
  /* 真机右缘有个悬浮音乐开关。长图只有一帧，放在右缘中段最贴近真机；
     之前用 bottom:8px 锚到 .phone 底部，结果被后画的 .bar 整块盖住——
     元素在 DOM 里、CSS 里也在，客户却永远看不到，属于典型静默失败。 */
  .music { position: absolute; right: 6px; top: 47%; width: 24px; height: 24px;
           border-radius: 50%; background: var(--wc-primary-pcat);
           box-shadow: var(--wc-card-shadow-soft);
           color: var(--wc-primary-alt); font-size: 13px; text-align: center; line-height: 24px; }

  /* ---- 底部固定操作栏：真机是「一键拨号」实心在左、「存入通讯录」描边在右 ----
     高度由 BAR_HEIGHT 注入，和画布收口常量同源；明显的顶边 + 上投影给出「页面到此结束」
     的边界，缩略卡里图内按钮才不会和卡片外的模板名称连成一片。 */
  .bar { position: absolute; left: 0; right: 0; bottom: 0; height: ${bar_h}px;
         background: #fff; border-top: 1px solid #d8dee6;
         box-shadow: 0 -3px 12px rgba(0,0,0,.06); display: flex;
         align-items: center; gap: 10px; padding: 0 10px; }
  .bar button { flex: 1; height: 38px; font-size: 12px; font-weight: 700;
                border-radius: var(--wc-r-control); }
  .bar .call { background: var(--wc-primary); color: #fff; border: 1px solid var(--wc-primary); }
  .bar .save { background: #fff; color: var(--wc-primary); border: 1px solid var(--wc-primary); }

  /* ===== 布局层：由 app.wxss 的 .wc-style-<id> 规则原文注入（已去前缀 / rpx→px） =====
     Default 以外的风格会覆盖上面的尺寸与列宽；这里绝不另写第二套布局。 */
  ${style_css}
</style></head><body>
<div class="phone">
  <div class="status" data-sec="status"><span>09:01</span><span class="sys">▪ ▪ 4G ▮ 21</span></div>
  <div class="nav" data-sec="nav">
    <div class="me"><span class="me-avatar"></span><span>我的</span></div>
    <div class="nav-title">${short_name}</div>
    <div class="wx-capsule"><span>···</span><span class="sep"></span><span class="dot"></span></div>
  </div>

  <div class="card emp-card" data-sec="card">
    <div class="ec-head">
      <div class="avatar-ph ec-avatar" aria-hidden="true"></div>
      <div class="ec-info">
        <div class="ec-line1"><span class="ec-name">${person}</span><span class="logo-ph">LOGO</span></div>
        <div class="ec-title">${title}</div>
        <div class="ec-line3"><span class="ec-cname">${full_name}</span><span class="case-btn">查看案例 ▸</span></div>
        <div class="ec-addr">${address}</div>
      </div>
    </div>
    <div class="ec-rows">
      <div class="ec-rows-line"></div>
      <div class="ec-contact ec-contact-short">
        <div class="ec-col">
          <div class="ec-line"><span class="ec-label">手机</span><span class="ec-val">${phone}</span></div>
          <div class="ec-line"><span class="ec-label"></span><span class="ec-val">138-0000-0001</span></div>
        </div>
        <div class="ec-col ec-col-r">
          <div class="ec-line"><span class="ec-label">座机</span><span class="ec-val">${tel}</span></div>
          <div class="ec-line"><span class="ec-label">邮箱</span><span class="ec-val">${email}</span></div>
        </div>
      </div>
    </div>
  </div>

  <div class="section mp-profile" data-sec="company">
    <div class="company-video">
      <div class="cv cover">
        <img src="assets/cover-company.jpg" alt="">
        <div class="cover-shade"></div>
        <div class="cover-word"><span>走进</span><span>${short_name}</span></div>
        <div class="play"></div>
        <div class="video-bar"><span>▶</span><span>00:00</span><span class="v-track"></span><span>01:15</span><span>⛶</span></div>
      </div>
    </div>
    <div class="pf-text">
      <div class="company-name">${full_name}</div>
      <div class="line-split company-name-line"></div>
      <div class="intro">${intro}</div>
    </div>
  </div>

  <div class="section mp-achievements" data-sec="achievement">
    <div class="sec-head"><span class="sec-title">经典业绩</span><span class="sec-more">更多案例 ▸</span></div>
    <div class="sec-line"></div>
    <div class="cat-grid">${cat_cards}</div>
    <div class="featured">
      <div class="feat-item"><img class="feat-cover" src="assets/cover-daxing.jpg" alt=""></div>
      <div class="feat-item"><img class="feat-cover" src="assets/cover-hospital.jpg" alt=""></div>
    </div>
  </div>

  <div class="section mp-services" data-sec="service">
    <div class="sec-head"><span class="sec-title">业务服务</span></div>
    <div class="sec-line"></div>
    <div class="svc-grid">${svc_cards}</div>
  </div>

  <!-- 悬浮音符是 .phone 的直接子节点：塞进某个 .section 里会跟着卡片裁切/换行 -->
  <span class="music">♪</span>
  <div class="bar" data-sec="bar"><button class="call">一键拨号</button><button class="save">存入通讯录</button></div>
</div></body></html>
""")


def find_chrome() -> str | None:
    override = os.environ.get("THEME_PREVIEW_CHROME")
    if override and Path(override).exists():
        return override
    candidates = [
        "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
        shutil.which("google-chrome"),
        shutil.which("chromium"),
        shutil.which("chromium-browser"),
    ]
    return next((c for c in candidates if c and Path(c).exists()), None)


def measure_content_bottom(chrome: str, html: str) -> int:
    """出图前实测业务服务卡底部，防止固定底栏悄悄盖住内容。

    改了布局（列数 / padding / 封面高度）内容会变长，而画布高度是常量：底栏是
    absolute 定位，内容被盖住不会报任何错，客户只会在缩略卡里看到半张服务卡。
    让浏览器报一次真实布局，比在注释里写「注意别改布局」可靠。P4 起四个风格
    逐个实测，每个风格用自己的画布高度收口。
    """
    probe = (
        '<script>window.addEventListener("load",function(){'
        'var s=document.querySelector(\'[data-sec="service"]\');'
        'document.body.setAttribute("data-wc-bottom",'
        'String(Math.round(s.getBoundingClientRect().bottom)));});</script>'
    )
    with tempfile.TemporaryDirectory(prefix="wc-theme-measure-") as td:
        page = Path(td) / "measure.html"
        assets = Path(td) / "assets"
        assets.mkdir()
        for name in SEED_FILES:
            shutil.copy2(SEED_DIR / name, assets / name)
        page.write_text(html.replace("</body>", probe + "</body>"), encoding="utf-8")
        cmd = [
            chrome, "--headless=new", "--disable-gpu", "--no-sandbox",
            "--hide-scrollbars", "--allow-file-access-from-files",
            "--virtual-time-budget=2500", f"--window-size={WIDTH},{MEASURE_HEIGHT}",
            "--dump-dom", page.as_uri(),
        ]
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=90)
        m = re.search(r'data-wc-bottom="(\d+)"', r.stdout)
        if r.returncode != 0 or not m:
            raise SystemExit(f"实测首页内容高度失败：{r.stderr[-400:]}")
        return int(m.group(1))


def render(chrome: str, html: str, out: Path, height: int) -> None:
    with tempfile.TemporaryDirectory(prefix="wc-theme-preview-") as td:
        page = Path(td) / "preview.html"
        assets = Path(td) / "assets"
        assets.mkdir()
        # 素材必须放到 HTML 同目录树下：Chrome 对 file:// 跨目录读图有安全限制，
        # 用相对路径最稳；也不用把演示素材再拷一份进 frontend/public。
        for name in SEED_FILES:
            src = SEED_DIR / name
            if not src.is_file():
                raise SystemExit(f"缺演示素材：{src.relative_to(ROOT)}")
            shutil.copy2(src, assets / name)
        page.write_text(html, encoding="utf-8")
        cmd = [
            chrome, "--headless=new", "--disable-gpu", "--no-sandbox",
            "--hide-scrollbars", "--allow-file-access-from-files",
            f"--force-device-scale-factor={SCALE}",
            f"--window-size={WIDTH},{height}", f"--screenshot={out}",
            page.as_uri(),
        ]
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=90)
        if r.returncode != 0 or not out.exists():
            raise SystemExit(f"Chrome 截图失败：{r.stderr[-600:]}")


def png_size(path: Path) -> tuple[int, int] | None:
    data = path.read_bytes()
    if not data.startswith(b"\x89PNG\r\n\x1a\n") or len(data) < 24:
        return None
    return struct.unpack(">II", data[16:24])


def build_html(
    tokens: dict[str, str],
    style_id: str = DEFAULT_STYLE,
    height: int = HEIGHT,
    style_css: str = "",
) -> str:
    cat_cards = "\n".join(
        f'<div class="cat-item"><img class="cat-icon" src="assets/cat-{i}.png" alt="">'
        f'<div class="cat-name">示例分类</div></div>'
        for i in range(1, 9)
    )
    # 服务卡标题已烧在图内（与 service-grid/index.wxml 一致），不重复渲染文字标题
    svc_cards = "\n".join(
        f'<div class="svc-item"><img class="svc-icon" src="assets/svc-{i}.png" alt=""></div>'
        for i in range(1, 5)
    )
    return HTML.substitute(
        w=WIDTH, h=height, bar_h=BAR_HEIGHT, tail_gap=TAIL_GAP,
        tokens=css_tokens(tokens), style_css=style_css,
        cat_cards=cat_cards, svc_cards=svc_cards,
        **PLACEHOLDER_TEXT,
    )


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--check", action="store_true", help="只校验产物，不需要浏览器")
    ap.add_argument("--measure", action="store_true", help="只实测四风格内容高度，不出图")
    args = ap.parse_args()

    tokens = read_tokens()
    layout_rules = read_layout_rules()
    layouts = {sid: "\n  ".join(rules) for sid, rules in layout_rules.items()}
    ids = read_go_ids()
    missing_tokens = [i for i in ids if i not in tokens]
    if missing_tokens:
        print(f"✗ app.wxss 缺主题变量块：{missing_tokens}", file=sys.stderr)
        return 1
    if len(set(ids)) != len(ids):
        print("✗ Go 白名单出现重复 id", file=sys.stderr)
        return 1
    go_styles = [i for i in ids if not i.startswith("brand-")]
    go_brands = [i for i in ids if i.startswith("brand-")]
    if go_styles != list(STYLE_IDS):
        print(f"✗ Go 白名单的风格款与布局规则对不上：{go_styles} != {list(STYLE_IDS)}", file=sys.stderr)
        return 1
    if ids[0] != DEFAULT_STYLE:
        print("✗ 默认款必须是 default 风格", file=sys.stderr)
        return 1
    if not go_brands:
        print("✗ Go 白名单没有品牌配色款，叉乘不出全组合", file=sys.stderr)
        return 1
    combos = all_combos(go_brands)
    if len(combos) != len(STYLE_IDS) * (1 + len(go_brands)):
        print("✗ 组合键发生冲突（两轴拼不出全叉乘）", file=sys.stderr)
        return 1
    combo_tok = {cid: combo_tokens(tokens, s, b) for cid, (s, b) in combos.items()}
    first = combo_tok[combo_id(DEFAULT_STYLE, None)]

    # 查的是代入占位文案后的最终 HTML，不能只查模板源码，否则是假绿。
    # P5 起四个风格各查一遍：布局不同，占位文案 / 身份素材必须同样干净。
    problems = check_sources() + check_no_identity(build_html(first))
    for sid in STYLE_IDS:
        problems += check_no_identity(build_html(first, sid))
    if problems:
        for p in problems:
            print(f"✗ {p}", file=sys.stderr)
        return 1

    if args.check:
        files = {p.stem: p for p in OUT_DIR.glob("*.png")} if OUT_DIR.is_dir() else {}
        extra = sorted(set(files) - set(combos))
        absent = [cid for cid in combos if cid not in files]
        data = json.loads(MANIFEST.read_text(encoding="utf-8")) if MANIFEST.is_file() else {}
        heights = data.get("canvasByStyle", {})
        bad = []
        for cid, (sid, _brand) in combos.items():
            p = files.get(cid)
            if not p:
                continue
            h = heights.get(sid)
            size = png_size(p)
            expected = (WIDTH * SCALE, h * SCALE) if isinstance(h, int) else None
            if p.stat().st_size < 5000 or size != expected:
                bad.append(f"{cid}({size}, {p.stat().st_size}B, 期望 {expected})")
        print(f"组合 {len(combos)} 个：{len(STYLE_IDS)} 风格 × {1 + len(go_brands)} 色档")
        print(f"预览图 {len(files)} 张，按风格画布 {heights}")
        # --check 不跑浏览器：用上一次出图时记下的实测值做快照对账。
        # 布局规则 / 基础色板 / 36 组合并 token 一旦变了，下面的比对就会判红，
        # 要求重出图（重出图时会重新实测并重写这几个快照）。
        problems = check_manifest(
            tokens, layouts, data.get("contentBottoms", {}), heights, combos, combo_tok
        )
        if absent:
            problems.append(f"缺图：{absent}")
        if extra:
            problems.append(f"多余图片（旧扁平 id 残留？）：{extra}")
        if bad:
            problems.append(f"尺寸/体积异常：{bad}")
        if problems:
            for p in problems:
                print(f"✗ {p}", file=sys.stderr)
            return 1
        print("✓ 36 组合预览图、四风格布局、组合 token 与模板白名单同版，PNG 合法且按风格尺寸一致")
        return 0

    chrome = find_chrome()
    if not chrome:
        print("✗ 找不到 Chrome；设 THEME_PREVIEW_CHROME=/path/to/chrome 后重试", file=sys.stderr)
        return 1

    # 逐风格实测：测量时用 MEASURE_HEIGHT 让内容自然流完，再按实测底部给该风格收口。
    # 36 张图按所属风格共用该风格的画布高度，底栏始终紧贴内容，短风格不留大尾巴。
    bottoms: dict[str, int] = {}
    heights: dict[str, int] = {}
    bar_top = HEIGHT - BAR_HEIGHT
    for sid in STYLE_IDS:
        probe_html = build_html(
            first, sid, MEASURE_HEIGHT, layout_css(layout_rules[sid])
        )
        bottom = measure_content_bottom(chrome, probe_html)
        bottoms[sid] = bottom
        heights[sid] = bottom + TAIL_GAP + BAR_HEIGHT
        if bottom + TAIL_GAP > bar_top:
            print(f"✗ 风格 {sid} 底栏会盖住业务服务卡：内容底部 {bottom} + 呼吸 {TAIL_GAP} "
                  f"> 底栏顶 {bar_top}；请同步调大 CONTENT_BOTTOM / HEIGHT 后重出图", file=sys.stderr)
            return 1
        print(f"✓ {sid}: 内容底部实测 {bottom}，画布 {heights[sid]}，尾部留白 {TAIL_GAP}px")
    if max(bottoms.values()) > CONTENT_BOTTOM:
        print(f"✗ 实测最大内容底部 {max(bottoms.values())} 超出 CONTENT_BOTTOM {CONTENT_BOTTOM}；"
              f"请同步调大 CONTENT_BOTTOM / HEIGHT 后重出图", file=sys.stderr)
        return 1
    if args.measure:
        print("（--measure：只实测，未出图）")
        return 0

    OUT_DIR.mkdir(parents=True, exist_ok=True)
    for cid, (sid, _brand) in combos.items():
        out = OUT_DIR / f"{cid}.png"
        html = build_html(combo_tok[cid], sid, heights[sid], layout_css(layout_rules[sid]))
        render(chrome, html, out, heights[sid])
        print(f"✓ {out.relative_to(ROOT)}  [{sid}]  {png_size(out)}  {out.stat().st_size}B")
    # P5 改了命名：旧扁平 id 的 12 张图已成死资源，留着会让 --check 判「多余图片」，
    # 仓库还白背十几 MB。只清本脚本自己产出的 *.png，不碰目录里其它文件。
    for p in OUT_DIR.glob("*.png"):
        if p.stem not in combos:
            p.unlink()
    write_manifest(tokens, layouts, bottoms, heights, combos, combo_tok)
    print(f"完成：{len(combos)} 张预览图（{len(STYLE_IDS)} 风格 × {1 + len(go_brands)} 色档）"
          f"+ 基础色板 / 组合 token / 布局快照，"
          f"token 与布局均来自 {APP_WXSS.relative_to(ROOT)}，图形来自 {SEED_DIR.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
