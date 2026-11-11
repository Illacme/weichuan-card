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

用法：
    python3 scripts/gen_theme_previews.py            # 重新生成全部预览图
    python3 scripts/gen_theme_previews.py --check    # 只校验产物（CI / 提交前用）

--check 不需要浏览器，查「每款模板都有图 / PNG 合法 / 尺寸一致 / token 快照与
app.wxss 同版 / 通用素材齐全 / 区块与按钮文案同源 / 不含任何真实客户身份」。
生成需要本机 Chrome（可用 THEME_PREVIEW_CHROME 指定路径）。**改了 app.wxss 配色
或圆角、改了首页区块、换了素材、改了占位文案、或加了一款模板，都必须重跑本脚本**：
否则图片会悄悄停在旧样式。
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
# 第二十八轮改为**按内容自然收口**：无头 Chrome 实测业务服务卡底部 y=1129，
# 取 1130 作基线，留 20px 呼吸后直接接 60px 底栏，不再保留真机截图尾部的滚动余量。
# 出图前会再实测一次（measure_content_bottom）：改了占位文案 / 区块高度让内容变长时，
# 底栏会悄悄盖住业务服务卡，光靠常量算不出来，必须显式拒绝出图。
# 早期 375x430 / 375x640 把分类图标网格、视频控件、4 张竖版服务卡全压成色块，
# 客户认不出这是自己的小程序，也不能再退回去。
WIDTH, HEIGHT = 375, 1210
CONTENT_BOTTOM, TAIL_GAP, BAR_HEIGHT = 1130, 20, 60
if HEIGHT != CONTENT_BOTTOM + TAIL_GAP + BAR_HEIGHT:
    raise SystemExit("画布高度必须由「内容底部 + 呼吸 + 底栏」算出，别再手填一个带大尾巴的数")
RPX_TO_PX = 0.5
SCALE = 2  # 2x 出图 750x2420，后台缩小显示仍可辨识，也可直接当销售材料用

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
GO_ID_RE = re.compile(r'ID:\s*(DefaultID|"([a-z0-9_-]+)")')
GO_DEFAULT_RE = re.compile(r'DefaultID\s*=\s*"([a-z0-9_-]+)"')


def pairs(block: str) -> dict[str, str]:
    # strip 与 theme.spec.ts 的 readThemeVars() 一致，否则快照对账会被尾随空格搞出假红
    return {k: v.strip() for k, v in TOKEN_RE.findall(block)}


def read_tokens() -> dict[str, dict[str, str]]:
    """从 app.wxss 的两层主题块合并出 12 组 18 token（与 theme.spec.ts 同一口径）。

    P2 起根节点同时挂 .wc-style-*（布局层，给全套 18 个 token）与 .wc-brand-*
    （颜色层，只给 11 个颜色 token，一个版式 token 都不许有）。后台预览仍按
    12 款组合各出一张图，所以这里把品牌款的颜色层与 default 的 7 个版式 token
    合并成完整一组。合并结果必须与拆层前逐字相同——否则 manifest 快照与 12 张
    PNG 都得重出，还会给存量租户引入视觉突变。
    """
    src = APP_WXSS.read_text(encoding="utf-8")
    m = DEFAULT_BLOCK_RE.search(src)
    if not m:
        raise SystemExit("app.wxss 找不到 page 上的默认 token 块")
    default = pairs(m.group(1))
    layout = {
        k: v for k, v in default.items()
        if k.startswith("--wc-r-") or k.startswith("--wc-card-shadow")
    }
    out: dict[str, dict[str, str]] = {"default": default}
    for t in STYLE_BLOCK_RE.finditer(src):
        out[t.group(1)] = pairs(t.group(2))
    for t in BRAND_BLOCK_RE.finditer(src):
        merged = dict(layout)
        merged.update(pairs(t.group(2)))
        out["brand-" + t.group(1)] = merged
    return out


def manifest_payload(tokens: dict[str, dict[str, str]]) -> dict:
    """随图写 token / 素材 / 占位文案快照：改了任一样却没重出图，守门能查出图旧了。"""
    return {
        "source": "miniprogram/app.wxss",
        "canvas": [WIDTH, HEIGHT],
        "scale": SCALE,
        "seedAssets": SEED_FILES,
        "placeholders": PLACEHOLDER_TEXT,
        "tokens": tokens,
    }


def write_manifest(tokens: dict[str, dict[str, str]]) -> None:
    MANIFEST.write_text(
        json.dumps(manifest_payload(tokens), ensure_ascii=False, indent=2, sort_keys=True) + "\n",
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


def check_manifest(tokens: dict[str, dict[str, str]]) -> list[str]:
    if not MANIFEST.is_file():
        return ["缺 manifest.json（无法证明预览图与 app.wxss / 素材同版）"]
    try:
        data = json.loads(MANIFEST.read_text(encoding="utf-8"))
    except (OSError, ValueError) as e:
        return [f"manifest.json 不是合法 JSON：{e}"]
    problems = []
    if data.get("tokens") != tokens:
        problems.append("token 快照与 app.wxss 当前值不一致：改了配色 / 圆角却没重跑本脚本")
    if data.get("canvas") != [WIDTH, HEIGHT] or data.get("scale") != SCALE:
        problems.append("manifest 画布参数与脚本不一致")
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

  /* ---- 名片卡：头像 / 姓名职位 / 企业 LOGO / 公司全称 / 查看案例 / 地址 / 两列联系表 ---- */
  .ec-head { display: flex; align-items: flex-start; }
  /* 头像与 LOGO 用中性占位，不引用真人照片 / 带企业名的标识：
     通用平台产品的示意图不能让客户误以为这是某家真实企业的名片。 */
  .avatar-ph { width: 50px; height: 50px; border-radius: 50%; flex: none; position: relative;
               overflow: hidden; background: var(--wc-primary-tint); border: 2px solid #fff;
               box-shadow: 0 2px 6px rgba(var(--wc-primary-rgb), .18); }
  .avatar-ph::before { content: ""; position: absolute; left: 50%; top: 10px;
               transform: translateX(-50%); width: 15px; height: 15px; border-radius: 50%;
               background: var(--wc-primary-border); }
  .avatar-ph::after { content: ""; position: absolute; left: 50%; bottom: -6px;
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
  .ec-rows-line, .line-split { height: 2px; margin: 10px 0 8px;
    background: linear-gradient(90deg, var(--wc-primary-deep) 0, var(--wc-primary-deep) 28%,
                var(--wc-track) 28%, var(--wc-track) 100%); }
  .line-split { width: 46px; margin: 7px 0 9px; }
  .ec-contact { display: flex; justify-content: space-between; gap: 8px; }
  .ec-col { flex: 1; min-width: 0; }
  .ec-line { display: flex; align-items: baseline; font-size: 10.5px; color: #333; padding: 1px 0; }
  .ec-label { width: 28px; flex: none; color: #98a6b5; font-size: 10px; }
  .ec-val { white-space: nowrap; }

  /* ---- 公司简介：真实封面图 + 视频控件 + 黑色粗体公司名 + 4 行简介 ---- */
  .cover { position: relative; height: 175px; border-radius: var(--wc-r-card-bg);
           overflow: hidden; box-shadow: var(--wc-card-shadow-bg); }
  .cover > img { width: 100%; height: 100%; object-fit: cover; display: block; }
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
  .company-name { font-size: 14.5px; font-weight: 700; color: #1a1a1a; margin-top: 10px; }
  .intro { font-size: 13px; line-height: 1.75; color: #4b5563; }

  /* ---- 经典业绩：8 个分类图标卡（2x4）+ 2 张精选案例大图 ---- */
  .sec-head { display: flex; align-items: center; justify-content: space-between; }
  .sec-title { font-size: 15px; font-weight: 700; color: #1a1a1a; }
  .sec-more { font-size: 10px; color: var(--wc-primary); }
  .sec-line { height: 2px; width: 46px; margin: 6px 0 10px;
    background: linear-gradient(90deg, var(--wc-primary-deep) 0, var(--wc-primary-deep) 28%,
                var(--wc-track) 28%, var(--wc-track) 100%); }
  .cat-grid, .svc-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 5px; }
  .cat-grid img, .svc-grid img { width: 100%; display: block; border-radius: var(--wc-r-card-bg); }
  .featured { display: flex; gap: 6px; margin-top: 10px; }
  .featured img { width: 50%; height: 88px; object-fit: cover; display: block;
                  border-radius: var(--wc-r-card-bg); }
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
</style></head><body>
<div class="phone">
  <div class="status" data-sec="status"><span>09:01</span><span class="sys">▪ ▪ 4G ▮ 21</span></div>
  <div class="nav" data-sec="nav">
    <div class="me"><span class="me-avatar"></span><span>我的</span></div>
    <div class="nav-title">${short_name}</div>
    <div class="wx-capsule"><span>···</span><span class="sep"></span><span class="dot"></span></div>
  </div>

  <div class="card" data-sec="card">
    <div class="ec-head">
      <div class="avatar-ph" aria-hidden="true"></div>
      <div class="ec-info">
        <div class="ec-line1"><span class="ec-name">${person}</span><span class="logo-ph">LOGO</span></div>
        <div class="ec-title">${title}</div>
        <div class="ec-line3"><span class="ec-cname">${full_name}</span><span class="case-btn">查看案例 ▸</span></div>
        <div class="ec-addr">${address}</div>
      </div>
    </div>
    <div class="ec-rows-line"></div>
    <div class="ec-contact">
      <div class="ec-col"><div class="ec-line"><span class="ec-label">手机</span><span class="ec-val">${phone}</span></div></div>
      <div class="ec-col">
        <div class="ec-line"><span class="ec-label">座机</span><span class="ec-val">${tel}</span></div>
        <div class="ec-line"><span class="ec-label">邮箱</span><span class="ec-val">${email}</span></div>
      </div>
    </div>
  </div>

  <div class="section" data-sec="company">
    <div class="cover">
      <img src="assets/cover-company.jpg" alt="">
      <div class="cover-shade"></div>
      <div class="cover-word"><span>走进</span><span>${short_name}</span></div>
      <div class="play"></div>
      <div class="video-bar"><span>▶</span><span>00:00</span><span class="v-track"></span><span>01:15</span><span>⛶</span></div>
    </div>
    <div class="company-name">${full_name}</div>
    <div class="line-split"></div>
    <div class="intro">${intro}</div>
  </div>

  <div class="section" data-sec="achievement">
    <div class="sec-head"><span class="sec-title">经典业绩</span><span class="sec-more">更多案例 ▸</span></div>
    <div class="sec-line"></div>
    <div class="cat-grid">${cat_cards}</div>
    <div class="featured">
      <img src="assets/cover-daxing.jpg" alt=""><img src="assets/cover-hospital.jpg" alt="">
    </div>
  </div>

  <div class="section" data-sec="service">
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

    改了占位文案长度 / 区块密度，内容会变长，而 HEIGHT 是常量：底栏是 absolute
    定位，内容被盖住不会报任何错，客户只会在缩略卡里看到半张服务卡。让浏览器
    报一次真实布局，比在注释里写「注意别改文案」可靠。
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
            "--virtual-time-budget=2500", f"--window-size={WIDTH},{HEIGHT}",
            "--dump-dom", page.as_uri(),
        ]
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=90)
        m = re.search(r'data-wc-bottom="(\d+)"', r.stdout)
        if r.returncode != 0 or not m:
            raise SystemExit(f"实测首页内容高度失败：{r.stderr[-400:]}")
        return int(m.group(1))


def render(chrome: str, html: str, out: Path) -> None:
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
            f"--window-size={WIDTH},{HEIGHT}", f"--screenshot={out}",
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


def build_html(tokens: dict[str, str]) -> str:
    cat_cards = "\n".join(
        f'<img src="assets/cat-{i}.png" alt="">' for i in range(1, 9)
    )
    svc_cards = "\n".join(
        f'<img src="assets/svc-{i}.png" alt="">' for i in range(1, 5)
    )
    return HTML.substitute(
        w=WIDTH, h=HEIGHT, bar_h=BAR_HEIGHT, tail_gap=TAIL_GAP,
        tokens=css_tokens(tokens), cat_cards=cat_cards, svc_cards=svc_cards,
        **PLACEHOLDER_TEXT,
    )


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--check", action="store_true", help="只校验产物，不需要浏览器")
    args = ap.parse_args()

    tokens = read_tokens()
    ids = read_go_ids()
    missing_tokens = [i for i in ids if i not in tokens]
    if missing_tokens:
        print(f"✗ app.wxss 缺主题变量块：{missing_tokens}", file=sys.stderr)
        return 1
    if len(set(ids)) != len(ids):
        print("✗ Go 白名单出现重复 id", file=sys.stderr)
        return 1
    active = {i: tokens[i] for i in ids}

    # 12 张图共用同一套 HTML（只有 token 不同），抽第一张验证脱敏即可；
    # 查的是代入占位文案后的最终 HTML，不能只查模板源码，否则是假绿。
    problems = check_sources() + check_no_identity(build_html(active[ids[0]]))
    if problems:
        for p in problems:
            print(f"✗ {p}", file=sys.stderr)
        return 1

    if args.check:
        files = {p.stem: p for p in OUT_DIR.glob("*.png")} if OUT_DIR.is_dir() else {}
        extra = sorted(set(files) - set(ids))
        absent = [i for i in ids if i not in files]
        expected = (WIDTH * SCALE, HEIGHT * SCALE)
        bad = []
        for i in ids:
            p = files.get(i)
            if not p:
                continue
            size = png_size(p)
            if p.stat().st_size < 5000 or size != expected:
                bad.append(f"{i}({size}, {p.stat().st_size}B)")
        print(f"模板 {len(ids)} 款：{', '.join(ids)}")
        print(f"预览图 {len(files)} 张，期望尺寸 {expected[0]}x{expected[1]}")
        problems = check_manifest(active)
        if absent:
            problems.append(f"缺图：{absent}")
        if extra:
            problems.append(f"多余图片：{extra}")
        if bad:
            problems.append(f"尺寸/体积异常：{bad}")
        if problems:
            for p in problems:
                print(f"✗ {p}", file=sys.stderr)
            return 1
        print("✓ 预览图、演示素材与模板白名单同版，PNG 合法且尺寸一致")
        return 0

    chrome = find_chrome()
    if not chrome:
        print("✗ 找不到 Chrome；设 THEME_PREVIEW_CHROME=/path/to/chrome 后重试", file=sys.stderr)
        return 1
    bottom = measure_content_bottom(chrome, build_html(active[ids[0]]))
    bar_top = HEIGHT - BAR_HEIGHT
    if bottom + TAIL_GAP > bar_top:
        print(f"✗ 底栏会盖住业务服务卡：内容底部 {bottom} + 呼吸 {TAIL_GAP} "
              f"> 底栏顶 {bar_top}；请同步调大 CONTENT_BOTTOM / HEIGHT 后重出图", file=sys.stderr)
        return 1
    print(f"✓ 内容底部实测 {bottom}，底栏顶 {bar_top}，尾部留白 {bar_top - bottom}px")
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    for i in ids:
        out = OUT_DIR / f"{i}.png"
        render(chrome, build_html(active[i]), out)
        print(f"✓ {out.relative_to(ROOT)}  {png_size(out)}  {out.stat().st_size}B")
    write_manifest(active)
    print(f"完成：{len(ids)} 张预览图 + token / 素材快照，token 来自 {APP_WXSS.relative_to(ROOT)}，"
          f"图形来自 {SEED_DIR.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
