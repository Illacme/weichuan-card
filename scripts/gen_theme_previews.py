#!/usr/bin/env python3
"""生成后台「整体风格」页用的高保真模板预览图。

为什么不是让设计手画 N 张图：预览一旦成为第二套样式，改了端上配色/圆角却忘了换图，
客户在后台看到的和实际小程序就不一样——而且没人会报错，属于典型的静默失败。
所以这里**从 miniprogram/app.wxss 解析 token**（与端上同名同值），注入一份复刻
小程序首页结构的 HTML，再用无头 Chrome 截图。token 是唯一真值，图是产物。

用法：
    python3 scripts/gen_theme_previews.py            # 重新生成全部预览图
    python3 scripts/gen_theme_previews.py --check    # 只校验产物（CI / 提交前用）

--check 不需要浏览器，只查「每款模板都有图 / PNG 合法 / 尺寸一致 / token 快照与
app.wxss 同版」；生成需要本机 Chrome（可用 THEME_PREVIEW_CHROME 指定路径）。
**改了 app.wxss 的配色或圆角、或加了一款模板，都必须重跑本脚本**：否则图片会
悄悄停留在旧样式，而「文件都在」这种检查永远查不出来。
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
# 高度刻意收到 430：后台模板卡是 230px 宽的小卡，完整展示时的高度 ≈264px。
# 若按整屏 560 画，卡片里要么缩得看不清、要么裁掉底部按钮——而按钮圆角正是
# 各款版式差异最大的一处。这里只复刻首页的「导航 + 名片卡 + 业绩区 + 底部操作栏」，
# 省略与业绩区重复的业务服务区，保证所有 token 差异都在一张图里看得见。
WIDTH, HEIGHT = 375, 430
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


# 只复刻首页里**真正用到 token 的结构**，固定值（药丸按钮 / 图标占位 / 字号）保持原样，
# 不为了让某几款看起来差异更大而造假。文字全是中性占位，不含任何真实姓名或租户信息。
HTML = """<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<style>
  * { box-sizing: border-box; }
  html, body { margin: 0; padding: 0; width: %(w)dpx; height: %(h)dpx; overflow: hidden; }
  body { background: #f5f5f5; color: #333;
         font: 12px/1.5 -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif; }
  .phone {
    width: %(w)dpx; height: %(h)dpx; background: #f5f5f5; position: relative;
%(tokens)s
  }
  .nav { height: 42px; background: rgba(255,255,255,.94); display: flex;
         align-items: center; justify-content: space-between; padding: 0 12px; }
  .nav .me { color: var(--wc-primary); background: var(--wc-primary-soft);
             border: 1px solid var(--wc-primary-border); border-radius: 999px;
             padding: 3px 10px; font-size: 10px; }
  .nav .brand { font-size: 12px; font-weight: 600; color: #222; }
  .nav .caps { width: 34px; }
  .card { background: #fff; margin: 10px 10px 0; padding: 12px;
          border-radius: var(--wc-r-card); box-shadow: var(--wc-card-shadow); }
  .head { display: flex; align-items: center; }
  .avatar { width: 42px; height: 42px; border-radius: 50%%;
            background: var(--wc-primary-tint); border: 2px solid #fff; flex: none; }
  .who { margin-left: 10px; min-width: 0; flex: 1; }
  .name { font-size: 15px; font-weight: 700; color: #222; }
  .title { color: #6b7a89; font-size: 10px; margin-top: 2px; }
  .cname { color: var(--wc-primary-deep); font-size: 11px; font-weight: 600; margin-top: 6px; }
  .cname .pill { float: right; color: var(--wc-primary); background: var(--wc-primary-soft);
                 border: 1px solid var(--wc-primary-border); border-radius: 999px;
                 font-size: 8px; padding: 1px 5px; font-weight: 400; }
  .rows-line { height: 1px; margin: 10px 0 8px;
    background: linear-gradient(90deg, var(--wc-primary-deep) 0, var(--wc-primary-deep) 34%%,
                var(--wc-track) 34%%, var(--wc-track) 100%%); }
  .rows { display: flex; font-size: 9px; color: #333; }
  .rows .col { flex: 1; }
  .rows .lbl { color: #98a6b5; display: inline-block; width: 28px; }
  .section { background: #fff; margin: 9px 10px 0; padding: 11px 12px 12px;
             border-radius: var(--wc-r-card); box-shadow: var(--wc-card-shadow-soft); }
  .sec-head { display: flex; align-items: center; justify-content: space-between; }
  .sec-title { font-size: 13px; font-weight: 600; }
  .sec-more { font-size: 9px; color: var(--wc-primary); }
  .sec-line { height: 2px; width: 34%%; margin: 5px 0 10px;
    background: linear-gradient(90deg, var(--wc-primary-deep) 0, var(--wc-primary-deep) 34%%,
                var(--wc-track) 34%%, var(--wc-track) 100%%); }
  .chips { display: flex; gap: 6px; margin-bottom: 10px; }
  .chip { font-size: 9px; padding: 2px 9px; color: #5f6f80; background: #f2f5f9;
          border-radius: var(--wc-r-chip); }
  .chip.on { color: var(--wc-primary); background: var(--wc-primary-pcat); font-weight: 600; }
  .grid { display: flex; gap: 7px; }
  .grid .cell { flex: 1; height: 52px; border-radius: 14px;
    background: linear-gradient(180deg, var(--wc-primary), var(--wc-primary-grad));
    display: flex; align-items: center; justify-content: center; color: #fff; font-size: 15px; }
  .grid .cell.alt { border-radius: 8px; height: 60px; }
  .bar { position: absolute; left: 0; right: 0; bottom: 0; height: 52px;
         background: #fff; border-top: 1px solid #eee; display: flex;
         align-items: center; gap: 10px; padding: 0 12px; }
  .bar button { flex: 1; height: 30px; font-size: 11px; border-radius: var(--wc-r-control); }
  .bar .solid { background: var(--wc-primary); color: #fff; border: 1px solid var(--wc-primary); }
  .bar .ghost { background: #fff; color: var(--wc-primary); border: 1px solid var(--wc-primary); }
</style></head><body>
<div class="phone">
  <div class="nav"><span class="me">我的</span><span class="brand">微传名片</span><span class="caps"></span></div>
  <div class="card">
    <div class="head">
      <div class="avatar"></div>
      <div class="who"><div class="name">张明</div><div class="title">产品总监</div></div>
    </div>
    <div class="cname">示例科技有限公司<span class="pill">查看案例 ▸</span></div>
    <div class="rows-line"></div>
    <div class="rows">
      <div class="col"><span class="lbl">手机</span>138 **** 0000</div>
      <div class="col"><span class="lbl">邮箱</span>hello@example.com</div>
    </div>
  </div>
  <div class="section">
    <div class="sec-head"><span class="sec-title">经典业绩</span><span class="sec-more">更多案例 ▸</span></div>
    <div class="sec-line"></div>
    <div class="chips"><span class="chip on">全部</span><span class="chip">工程案例</span><span class="chip">服务</span></div>
    <div class="grid"><div class="cell">◈</div><div class="cell">◈</div><div class="cell">◈</div><div class="cell">◈</div></div>
  </div>
  <div class="bar"><button class="ghost">保存名片</button><button class="solid">拨打电话</button></div>
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
