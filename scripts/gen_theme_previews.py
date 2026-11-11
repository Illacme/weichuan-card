#!/usr/bin/env python3
"""生成后台「风格模板」页用的**首页风格示意图**。

**它不是真机截图**：后台是普通网页，跑不了小程序组件树；模板又随小程序版本提审，
没法在浏览器里动态加载 WXSS。这里按 `pages/index/index.wxml` 的真实区块顺序复刻
一份首页结构（顶栏 / 名片卡 / 公司简介 / 经典业绩 / 业务服务 / 底部操作栏），
把 **miniprogram/app.wxss 的 18 个 token** 逐字注入同名的 CSS 变量，再用无头 Chrome
截图。所以准的是「配色 / 圆角 / 按钮形态 / 投影」，不准的是真实租户内容、真机字体、
状态栏胶囊、安全区与首页背景图——这些必须由真机验收 T1–T13 覆盖。
后台卡片常显「风格示意 · 非真机截图」角标，并可点开看完整大图。

为什么不让设计手画 N 张图：预览一旦成为第二套样式，改了端上配色/圆角却忘了换图，
客户在后台看到的和实际小程序就不一样——而且没人会报错，属于典型的静默失败。
token 是唯一真值，图是产物；`manifest.json` 记下每款 token 快照防漂移。

用法：
    python3 scripts/gen_theme_previews.py            # 重新生成全部预览图
    python3 scripts/gen_theme_previews.py --check    # 只校验产物（CI / 提交前用）

--check 不需要浏览器，只查「每款模板都有图 / PNG 合法 / 尺寸一致 / token 快照与
app.wxss 同版」；生成需要本机 Chrome（可用 THEME_PREVIEW_CHROME 指定路径）。
**改了 app.wxss 的配色或圆角、改了首页区块、或加了一款模板，都必须重跑本脚本**：
否则图片会悄悄停留在旧样式，而「文件都在」这种检查永远查不出来。
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

ROOT = Path(__file__).resolve().parent.parent
APP_WXSS = ROOT / "miniprogram/app.wxss"
GO_TPL = ROOT / "backend/internal/mptemplate/mptemplate.go"
OUT_DIR = ROOT / "frontend/public/theme-previews"
MANIFEST = OUT_DIR / "manifest.json"

# 画布按 iPhone 逻辑像素 375pt 设计；小程序 750rpx = 屏宽，故 1rpx = 0.5px。
# 高度 640：要装下真实首页的**全部区块**（顶栏 / 名片卡 / 公司简介封面 / 经典业绩 /
# 业务服务 / 底部操作栏），不能再像早期版本那样为了小卡片裁掉业务服务区——
# 客户会认不出这是自己的小程序。各区块相对顺序与真实 index.wxml 一致，
# 但尺寸做了适度压缩（封面 440rpx 收成 100px、案例卡收成缩略图），
# 且全部使用中性占位内容，静态资源里绝不出现真实租户数据。
# 底部操作栏是绝对定位：真机上它也是固定底栏，按钮圆角是各款版式差异最大处。
# 后台卡片按 375/640 等比显示整张示意图，另提供「查看大图」看原始 2x 尺寸。
WIDTH, HEIGHT = 375, 640
RPX_TO_PX = 0.5
SCALE = 2  # 2x 出图，后台缩小显示仍然锐利，也可直接当销售材料用

TOKEN_RE = re.compile(r"(--wc-[\w-]+)\s*:\s*([^;]+);")
DEFAULT_BLOCK_RE = re.compile(r"page\s*\{([^{}]*--wc-primary[^{}]*)\}")
THEME_BLOCK_RE = re.compile(r"\.wc-theme-([a-z0-9_-]+)\s*\{([^{}]*)\}")
GO_ID_RE = re.compile(r'ID:\s*(DefaultID|"([a-z0-9_-]+)")')
GO_DEFAULT_RE = re.compile(r'DefaultID\s*=\s*"([a-z0-9_-]+)"')


def pairs(block: str) -> dict[str, str]:
    # strip 与 theme.spec.ts 的 readThemeVars() 一致，否则快照对账会被尾随空格搞出假红
    return {k: v.strip() for k, v in TOKEN_RE.findall(block)}


def read_tokens() -> dict[str, dict[str, str]]:
    """从 app.wxss 读默认块与每套主题块的全部 token（与 theme.spec.ts 同一套解析口径）。"""
    src = APP_WXSS.read_text(encoding="utf-8")
    m = DEFAULT_BLOCK_RE.search(src)
    if not m:
        raise SystemExit("app.wxss 找不到 page 上的默认 token 块")
    out: dict[str, dict[str, str]] = {"default": pairs(m.group(1))}
    for t in THEME_BLOCK_RE.finditer(src):
        out[t.group(1)] = pairs(t.group(2))
    return out


def manifest_payload(tokens: dict[str, dict[str, str]]) -> dict:
    """随图写 token 快照：改了 app.wxss 却没重出图时，守门能查出图已经旧了。"""
    return {
        "source": "miniprogram/app.wxss",
        "canvas": [WIDTH, HEIGHT],
        "scale": SCALE,
        "tokens": tokens,
    }


def write_manifest(tokens: dict[str, dict[str, str]]) -> None:
    MANIFEST.write_text(
        json.dumps(manifest_payload(tokens), ensure_ascii=False, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )


def check_manifest(tokens: dict[str, dict[str, str]]) -> list[str]:
    if not MANIFEST.is_file():
        return ["缺 manifest.json（无法证明预览图与 app.wxss 同版）"]
    try:
        data = json.loads(MANIFEST.read_text(encoding="utf-8"))
    except (OSError, ValueError) as e:
        return [f"manifest.json 不是合法 JSON：{e}"]
    problems = []
    if data.get("tokens") != tokens:
        problems.append("token 快照与 app.wxss 当前值不一致：改了配色 / 圆角却没重跑本脚本")
    if data.get("canvas") != [WIDTH, HEIGHT] or data.get("scale") != SCALE:
        problems.append("manifest 画布参数与脚本不一致")
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


# 按 pages/index/index.wxml 的区块顺序复刻首页，尺寸适度压缩以装进 375×640：
# 每个 data-sec 与真实首页的区块一一对应，theme.spec.ts 会逐个对账，
# 防止有人为了省事又退回「只画名片区 + 一排菱形」的简化版。
# 18 个 --wc-* token 必须全部以 var() 引用（定义了却没用 = 假同源）；
# 文字与图片全是中性占位，静态资源里不出现任何真实租户数据。
HTML = """<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<style>
  * { box-sizing: border-box; }
  html, body { margin: 0; padding: 0; width: %(w)dpx; height: %(h)dpx; overflow: hidden; }
  body { background: #f5f5f5; color: #333;
         font: 11px/1.45 -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif; }
  .phone {
    width: %(w)dpx; height: %(h)dpx; background: #f5f5f5; position: relative;
    padding-bottom: 48px;
%(tokens)s
  }
  /* 状态栏与胶囊只画占位：真机的系统图标 / 安全区不该被伪造进示意图 */
  .status { height: 20px; display: flex; align-items: center; justify-content: space-between;
            padding: 0 12px; font-size: 9px; color: #555; }
  .capsule { width: 52px; height: 16px; border-radius: 9px; border: 1px solid #d8dde3;
             background: rgba(255,255,255,.7); }
  /* 自定义顶栏：融入页面底色（不是白底），企业简称居中，「我的」是主色药丸 */
  .nav { height: 34px; display: flex; align-items: center; justify-content: space-between;
         padding: 0 12px; position: relative; }
  .nav .me { color: var(--wc-primary); background: var(--wc-primary-soft);
             border: 1px solid var(--wc-primary-border); border-radius: 999px;
             padding: 3px 9px; font-size: 9px; }
  .nav .brand { position: absolute; left: 0; right: 0; text-align: center;
                font-size: 11px; font-weight: 600; color: #222; }
  .nav .nav-caps { width: 60px; }
  .card { background: #fff; margin: 4px 10px 0; padding: 10px;
          border-radius: var(--wc-r-card); box-shadow: var(--wc-card-shadow); }
  .head { display: flex; align-items: center; }
  .avatar { width: 34px; height: 34px; border-radius: 50%%;
            background: var(--wc-primary-tint); border: 2px solid #fff; flex: none;
            box-shadow: 0 2px 5px rgba(0,0,0,.08); }
  .who { margin-left: 9px; min-width: 0; flex: 1; }
  .name { font-size: 13px; font-weight: 700; color: #222; }
  .title { color: #6b7a89; font-size: 9px; margin-top: 1px; }
  .logo { width: 44px; height: 14px; border-radius: 3px; flex: none;
          background: var(--wc-primary-chip); }
  .cname { color: var(--wc-primary-deep); font-size: 10px; font-weight: 600; margin-top: 7px; }
  .cname .pill { float: right; color: var(--wc-primary); background: var(--wc-primary-soft);
                 border: 1px solid var(--wc-primary-border); border-radius: 999px;
                 font-size: 8px; padding: 1px 5px; font-weight: 400; }
  .rows-line { height: 1px; margin: 8px 0 6px;
    background: linear-gradient(90deg, var(--wc-primary-deep) 0, var(--wc-primary-deep) 34%%,
                var(--wc-track) 34%%, var(--wc-track) 100%%); }
  .rows { display: flex; font-size: 8px; color: #333; }
  .rows .col { flex: 1; min-width: 0; line-height: 1.6; }
  .rows .lbl { color: #98a6b5; display: inline-block; width: 25px; }
  .section { background: #fff; margin: 8px 10px 0; padding: 9px 10px 10px;
             border-radius: var(--wc-r-card); box-shadow: var(--wc-card-shadow-soft); }
  /* 公司简介封面：真实页面是租户上传的图/视频，这里只能用主色渐变 + 中性几何占位 */
  .cover { position: relative; height: 100px; border-radius: var(--wc-r-card-bg);
           background: linear-gradient(135deg, var(--wc-primary-deep), var(--wc-primary-grad));
           box-shadow: var(--wc-card-shadow-bg); overflow: hidden; }
  .cover::before { content: ""; position: absolute; width: 100px; height: 100px; right: -18px;
                   top: -32px; border-radius: 50%%; background: rgba(255,255,255,.10); }
  .cover::after { content: ""; position: absolute; width: 68px; height: 68px; right: 26px;
                  bottom: -28px; border-radius: 50%%; background: rgba(255,255,255,.08); }
  .cover-title { position: absolute; left: 10px; top: 9px; color: #fff; font-size: 12px;
                 font-weight: 600; text-shadow: 0 1px 3px rgba(0,0,0,.28); }
  .cover-play { position: absolute; left: 50%%; top: 50%%; transform: translate(-50%%,-50%%);
                width: 24px; height: 24px; border-radius: 50%%; background: rgba(255,255,255,.88); }
  .company-name { color: var(--wc-primary-deep); font-size: 10px; font-weight: 600; margin-top: 7px; }
  .line-split { height: 1px; margin: 5px 0 6px;
    background: linear-gradient(90deg, var(--wc-primary-deep) 0, var(--wc-primary-deep) 34%%,
                var(--wc-track) 34%%, var(--wc-track) 100%%); }
  .intro { font-size: 8px; color: #6b7a89; line-height: 1.55; }
  .sec-head { display: flex; align-items: center; justify-content: space-between; }
  .sec-title { font-size: 11px; font-weight: 600; color: #222; }
  .sec-more { font-size: 8px; color: var(--wc-primary); }
  .sec-line { height: 2px; width: 30%%; margin: 4px 0 8px;
    background: linear-gradient(90deg, var(--wc-primary-deep) 0, var(--wc-primary-deep) 34%%,
                var(--wc-track) 34%%, var(--wc-track) 100%%); }
  .chips { display: flex; gap: 5px; margin-bottom: 7px; }
  .chip { font-size: 8px; padding: 2px 8px; color: #5f6f80; background: #f2f5f9;
          border-radius: var(--wc-r-chip); }
  .chip.on { color: var(--wc-primary); background: var(--wc-primary-pcat); font-weight: 600; }
  .cases { display: flex; gap: 5px; }
  .case { flex: 1; min-width: 0; }
  .case .thumb { height: 42px; border-radius: var(--wc-r-card-bg);
                 background: linear-gradient(160deg, var(--wc-primary), var(--wc-primary-grad));
                 box-shadow: 0 1px 4px rgba(var(--wc-primary-rgb), .18); }
  .case .cap { margin-top: 3px; height: 4px; width: 80%%; border-radius: 2px; background: #dfe4ea; }
  .case .cap.short { margin-top: 2px; height: 3px; width: 55%%; background: #e9edf2; }
  .svc { display: flex; gap: 5px; }
  .svc-item { flex: 1; display: flex; align-items: center; gap: 4px; padding: 5px;
              border: 1px solid var(--wc-primary-border); border-radius: var(--wc-r-card-bg);
              background: var(--wc-primary-soft); }
  .svc-item .ico { width: 14px; height: 14px; border-radius: 4px; flex: none;
                   background: var(--wc-primary-alt); }
  .svc-item .txt { font-size: 8px; color: #5f6f80; overflow: hidden; white-space: nowrap; }
  /* 底部操作栏：真机上也是固定底栏，按钮圆角是各款版式差异最大的一处 */
  .bar { position: absolute; left: 0; right: 0; bottom: 0; height: 48px;
         background: #fff; border-top: 1px solid #eee; display: flex;
         align-items: center; gap: 9px; padding: 0 10px; }
  .bar button { flex: 1; height: 28px; font-size: 10px; border-radius: var(--wc-r-control); }
  .bar .solid { background: var(--wc-primary); color: #fff; border: 1px solid var(--wc-primary); }
  .bar .ghost { background: #fff; color: var(--wc-primary); border: 1px solid var(--wc-primary); }
</style></head><body>
<div class="phone">
  <div class="status" data-sec="status"><span>9:41</span><span class="capsule"></span></div>
  <div class="nav" data-sec="nav">
    <span class="me">我的</span><span class="brand">微传名片</span><span class="nav-caps"></span>
  </div>
  <div class="card" data-sec="card">
    <div class="head">
      <div class="avatar"></div>
      <div class="who"><div class="name">张明</div><div class="title">产品总监</div></div>
      <div class="logo"></div>
    </div>
    <div class="cname">示例科技有限公司<span class="pill">查看案例 ▸</span></div>
    <div class="rows-line"></div>
    <div class="rows">
      <div class="col"><span class="lbl">手机</span>138 **** 0000<br><span class="lbl">备用</span>139 **** 0000</div>
      <div class="col"><span class="lbl">座机</span>0755-****<br><span class="lbl">邮箱</span>hello@example.com</div>
    </div>
  </div>
  <div class="section" data-sec="company">
    <div class="cover"><span class="cover-title">公司简介</span><span class="cover-play"></span></div>
    <div class="company-name">示例科技有限公司</div>
    <div class="line-split"></div>
    <div class="intro">专注企业数字化服务，提供从咨询、设计到落地的一体化解决方案。</div>
  </div>
  <div class="section" data-sec="achievement">
    <div class="sec-head"><span class="sec-title">经典业绩</span><span class="sec-more">更多案例 ▸</span></div>
    <div class="sec-line"></div>
    <div class="chips"><span class="chip on">全部</span><span class="chip">工程案例</span><span class="chip">服务</span></div>
    <div class="cases">
      <div class="case"><div class="thumb"></div><div class="cap"></div><div class="cap short"></div></div>
      <div class="case"><div class="thumb"></div><div class="cap"></div><div class="cap short"></div></div>
      <div class="case"><div class="thumb"></div><div class="cap"></div><div class="cap short"></div></div>
      <div class="case"><div class="thumb"></div><div class="cap"></div><div class="cap short"></div></div>
    </div>
  </div>
  <div class="section" data-sec="service">
    <div class="sec-head"><span class="sec-title">业务服务</span><span class="sec-more">全部 ▸</span></div>
    <div class="sec-line"></div>
    <div class="svc">
      <div class="svc-item"><span class="ico"></span><span class="txt">方案咨询</span></div>
      <div class="svc-item"><span class="ico"></span><span class="txt">项目实施</span></div>
      <div class="svc-item"><span class="ico"></span><span class="txt">运维支持</span></div>
    </div>
  </div>
  <div class="bar" data-sec="bar"><button class="ghost">保存名片</button><button class="solid">拨打电话</button></div>
</div></body></html>
"""


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


def render(chrome: str, html: str, out: Path) -> None:
    with tempfile.TemporaryDirectory(prefix="wc-theme-preview-") as td:
        page = Path(td) / "preview.html"
        page.write_text(html, encoding="utf-8")
        cmd = [
            chrome, "--headless=new", "--disable-gpu", "--no-sandbox",
            "--hide-scrollbars", f"--force-device-scale-factor={SCALE}",
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
    # manifest / 出图只记白名单里的款；app.wxss 若有多余的 .wc-theme-* 块不进产物
    active = {i: tokens[i] for i in ids}

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
        print("✓ 预览图与模板白名单一一对应，PNG 合法且尺寸一致")
        return 0

    chrome = find_chrome()
    if not chrome:
        print("✗ 找不到 Chrome；设 THEME_PREVIEW_CHROME=/path/to/chrome 后重试", file=sys.stderr)
        return 1
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    for i in ids:
        html = HTML % {"w": WIDTH, "h": HEIGHT, "tokens": css_tokens(active[i])}
        out = OUT_DIR / f"{i}.png"
        render(chrome, html, out)
        print(f"✓ {out.relative_to(ROOT)}  {png_size(out)}  {out.stat().st_size}B")
    write_manifest(active)
    print(f"完成：{len(ids)} 张预览图 + token 快照，token 全部来自 {APP_WXSS.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
