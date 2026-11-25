#!/usr/bin/env python3
"""把小程序 app.json 的标题同步到运行时常量 CONFIGURED_APP_NAME。

为什么需要它
------------
企业单独发布一套小程序时，标题改在 `app.json → window.navigationBarTitleText`。
但小程序运行时**没有读取 app.json 的 API**（没有 wx.getAppConfig 之类），
`utils/brand.ts` 只能镜像一份常量才能做启动首帧标题。

如果让企业手动维护两处，第一件事就是忘了同步——启动画面顶着平台名，
看起来像没改干净。所以以 app.json 为唯一入口，这个脚本负责自动同步镜像：

    python3 scripts/sync_mp_app_name.py            # 改完 app.json 后跑，写回 brand.ts
    python3 scripts/sync_mp_app_name.py --check    # CI / pre-push 用：只检查不同步则红
    python3 scripts/sync_mp_app_name.py --selftest # 自检：喂坏样本，证明守门会红

为什么要有 --selftest（防守门空转）
----------------------------------
这是典型的「正则抓取型守门」。最坏的坏法不是报错，而是两边锚点同时失配：
正则抓不到任何东西 → 认为 empty==empty → 守门恒绿且无人察觉。
自检在内存里喂「改了 app.json 不同步」「删掉其中一侧锚点」等坏样本，
确认每一种都必须判红；pre-push 与 CI 在真检查前都先跑它。

读不到文件一律 fail closed：本地连真值源都读不到时，守门没有存在意义。
"""
import argparse
import re
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent
APP_JSON = REPO_ROOT / "miniprogram" / "app.json"
BRAND_TS = REPO_ROOT / "miniprogram" / "utils" / "brand.ts"

# 镜像常量的声明锚点。整行替换，不动周围注释。
# 行尾用 [ \t]* 而不是 \s*：\s 会连换行一起吃掉，导致声明后的空行消失。
CONST_RE = re.compile(r"^(export const CONFIGURED_APP_NAME = )(['\"])(.*?)\2[ \t]*$", re.M)
# app.json 里 window.navigationBarTitleText 的值。用 JSON 解析更稳，见 read_app_name。
ANCHOR = "export const CONFIGURED_APP_NAME"


class SyncError(Exception):
    """真值源缺失 / 锚点漂移 / 不同步，均视为失败。"""


def read_app_name(text: str) -> str:
    """从 app.json 文本解析标题；解析失败或空值一律 fail closed。"""
    import json

    try:
        data = json.loads(text)
    except (ValueError, TypeError) as exc:
        raise SyncError(f"app.json 不是合法 JSON：{exc}") from exc
    try:
        raw = data["window"]["navigationBarTitleText"]
    except (KeyError, TypeError) as exc:
        raise SyncError("app.json 缺少 window.navigationBarTitleText，无法确定启动标题") from exc
    name = str(raw).strip()
    if not name:
        raise SyncError("app.json 的 window.navigationBarTitleText 为空，启动标题不能为空")
    return name


def read_const(text: str) -> str:
    """从 brand.ts 文本抓 CONFIGURED_APP_NAME 的字面量；抓不到一律 fail closed。"""
    found = CONST_RE.findall(text)
    if len(found) != 1:
        raise SyncError(
            f"brand.ts 里应恰好有 1 处 {ANCHOR} 声明，实际找到 {len(found)} 处"
            "（锚点漂移时守门会静默失效，必须停下来修）"
        )
    return found[0][2]


def replace_const(text: str, name: str) -> str:
    """整行替换常量声明；用 lambda 避免反斜杠在替换串里被当转义。"""
    # 用单引号，与项目 TS 风格一致；字面量内含单引号时改用双引号。
    quote = "'" if "'" not in name else '"'
    new_line = f"export const CONFIGURED_APP_NAME = {quote}{name}{quote}"
    out, n = CONST_RE.subn(lambda _m: new_line, text)
    if n != 1:
        raise SyncError(f"替换 {ANCHOR} 失败（匹配 {n} 处），未写盘")
    return out


def sync(check_only: bool) -> int:
    try:
        app_text = APP_JSON.read_text(encoding="utf-8")
        brand_text = BRAND_TS.read_text(encoding="utf-8")
        name = read_app_name(app_text)
        current = read_const(brand_text)
    except OSError as exc:
        raise SyncError(f"读文件失败：{exc}") from exc

    if current == name:
        # 值一致但格式不标准（如之前手改留下的双引号）也要纠正，让镜像保持规范。
        canonical = replace_const(brand_text, name)
        if canonical != brand_text:
            if check_only:
                print(
                    f"❌ app.json 与 brand.ts 标题值一致（{name}），但镜像常量格式不规范；"
                    "请运行 python3 scripts/sync_mp_app_name.py 规范化",
                    file=sys.stderr,
                )
                return 1
            BRAND_TS.write_text(canonical, encoding="utf-8")
            print(f"✓ 标题值一致（{name}），已把镜像常量规范为单引号")
        else:
            print(f"✓ app.json 标题与 brand.ts 镜像一致：{name}")
        return 0

    if check_only:
        print(
            f"❌ 启动标题不同步：app.json = {name!r}，brand.ts 的 CONFIGURED_APP_NAME = {current!r}\n"
            "   企业只改 app.json 即可；镜像常量由脚本生成，不要手改 brand.ts。\n"
            f"   修复：python3 scripts/sync_mp_app_name.py\n"
            f"（从仓库根目录运行；脚本位于 {HERE.relative_to(REPO_ROOT)}）",
            file=sys.stderr,
        )
        return 1

    new_text = replace_const(brand_text, name)
    BRAND_TS.write_text(new_text, encoding="utf-8")
    # 回读确认：写盘后必须自洽，否则等于没做
    if read_const(BRAND_TS.read_text(encoding="utf-8")) != name:
        raise SyncError("写盘后回读不一致，同步失败")
    print(f"✓ 已把 app.json 的标题 {name!r} 同步到 {BRAND_TS.relative_to(REPO_ROOT)}")
    return 0


def selftest() -> int:
    """喂坏样本，证明 --check 不是恒绿；不碰磁盘上的真文件。"""
    cases = [
        # (说明, app.json 文本, brand.ts 文本, 期望是否判红)
        (
            "改了 app.json 不同步镜像",
            '{"window":{"navigationBarTitleText":"新公司名"}}',
            'export const CONFIGURED_APP_NAME = "微传名片"\n',
            True,
        ),
        (
            "镜像常量锚点被删（正则抓不到，必须 fail closed）",
            '{"window":{"navigationBarTitleText":"微传名片"}}',
            'export const SOMETHING_ELSE = "微传名片"\n',
            True,
        ),
        (
            "app.json 标题写空（fail closed）",
            '{"window":{"navigationBarTitleText":"   "}}',
            'export const CONFIGURED_APP_NAME = "微传名片"\n',
            True,
        ),
        (
            "两侧一致（必须判绿，否则守门 unusable）",
            '{"window":{"navigationBarTitleText":"微传名片"}}',
            'export const CONFIGURED_APP_NAME = "微传名片"\n',
            False,
        ),
    ]
    failed = 0
    for label, app_text, brand_text, expect_fail in cases:
        try:
            name = read_app_name(app_text)
            current = read_const(brand_text)
            mismatched = current != name
        except SyncError:
            mismatched = True
        got_fail = mismatched
        ok = got_fail == expect_fail
        print(f"  {'✓' if ok else '✗'} {label}：期望{'红' if expect_fail else '绿'}，实际{'红' if got_fail else '绿'}")
        if not ok:
            failed += 1
    if failed:
        print(f"❌ 自检失败 {failed} 项：守门已空转，禁止继续依赖它", file=sys.stderr)
        return 1
    print("✓ 自检通过：坏样本全部判红，正常样本判绿")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="同步 app.json 启动标题到小程序运行时常量")
    parser.add_argument("--check", action="store_true", help="只检查不同步则退出 1（CI / pre-push 用）")
    parser.add_argument("--selftest", action="store_true", help="自检：喂坏样本证明守门会红")
    args = parser.parse_args()
    try:
        if args.selftest:
            return selftest()
        return sync(check_only=args.check)
    except SyncError as exc:
        print(f"❌ {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
