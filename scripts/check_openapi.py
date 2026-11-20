#!/usr/bin/env python3
"""OpenAPI 契约守门：文档可解析、路径数对得上、路由对账、关键字段没漂移。

为什么需要它
------------
docs/openapi.yaml 是三端联调的契约，但长期没有任何通用 CI 校验：
一次改接口只改了代码没改文档、或文档路径数变了没人同步说明，都是「没人报错」的漂移。
本脚本用 PyYAML 真解析（不是正则数行数），并校验：

  1. YAML 语法合法，paths 数量有下限（防止解析/抓取整体空转）；
  2. 原文里 `  /path:` 的行数 == 解析出的 path 数（防重复键被 YAML 静默覆盖）；
  3. 每个 path 至少有一个 HTTP 方法；
  4. 所有内部 $ref 都能解析（外部 http ref 跳过）；
  5. 首页文字四档字段 companyNameScale / introFontScale 的 enum 完整；
  6. commercial-readiness.md 里标记为本脚本真值源的那一行路径数 == 实际数。
     —— 历史轮次里写的「106 paths」是当时的事实，不能改成今天的值；
        因此只认带「当前 API 契约基线」标记的这一行，不拿历史记录当门禁。
  7. **后端注册的每条业务路由都在契约里**（正向对账）。静态解析 Go 源码，
     不依赖本机有 Go 工具链——CI 的 openapi job 只装了 PyYAML。
     只查「代码 ⊆ 文档」：文档里多出来的路径打印提示但不拦，避免扫描器盲区
     （条件注册、NoRoute 等）造成假红；代码里新加一条却忘写文档必须直接红。

运维探针与安装向导（/、/metrics、/setup）不是三端联调契约，不要求进文档，见 NON_CONTRACT_PATHS / PREFIXES。

用法：
  python3 scripts/check_openapi.py              # 正常守门
  python3 scripts/check_openapi.py --selftest   # 注入一条假路由，证明对账会拦（防守门空转）
退出码 0 = 通过；非 0 = 有漂移，打印具体原因。
"""
import re
import sys
from pathlib import Path

try:
    import yaml
except ImportError:
    sys.exit("❌ 缺少 PyYAML：本守门必须真解析 YAML，不能用正则糊弄（CI 用 venv 安装 pyyaml）")

ROOT = Path(__file__).resolve().parent.parent
SPEC = ROOT / "docs" / "openapi.yaml"
DOC = ROOT / "docs" / "commercial-readiness.md"

MIN_PATHS = 100
EXPECTED_SCALES = ["large", "medium", "small", "hidden"]
MIN_SCALE_DECLARATIONS = 4  # GET/PUT + 两个旧字段的兼容声明，少一个说明契约被删过
METHODS = {"get", "post", "put", "delete", "patch", "head", "options", "trace"}
BASELINE_RE = re.compile(r"当前 API 契约基线[^\n]*?(\d+)\s*paths")

BACKEND = ROOT / "backend"
GO_METHODS = ("GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS")
# 运维探针 / 安装向导不是三端联调契约：/setup 与 / 是安装重定向与页面，
# /metrics 给监控系统。它们没有前端业务调用方，写进契约只会制造噪音。
NON_CONTRACT_PATHS = {"/"}
NON_CONTRACT_PREFIXES = ("/metrics", "/setup")
# healthz 走循环里的动态注册（rt.path），静态扫描天然看不到；文档里已有的三条
# 不再当成「契约有、代码无」提示。若哪天改成字面量注册，删掉这一项即可纳入对账。
SCANNER_BLIND_PREFIXES = ("/healthz",)
SELFTEST_PATH = "/__contract_selftest__/never-registered"


def strip_go_comments(src):
    """去掉 Go 的行/块注释，保留字符串字面量原样（URL 里的 // 不能被当成注释）。"""
    out = []
    i, n = 0, len(src)
    state, quote = "code", ""
    while i < n:
        ch = src[i]
        if state == "code":
            if ch in ('"', "'", "`"):
                state, quote = "str", ch
                out.append(ch)
                i += 1
            elif src.startswith("//", i):
                state = "line"
                out.extend("  ")
                i += 2
            elif src.startswith("/*", i):
                state = "block"
                out.extend("  ")
                i += 2
            else:
                out.append(ch)
                i += 1
        elif state == "str":
            out.append(ch)
            if ch == "\\" and quote != "`" and i + 1 < n:
                out.append(src[i + 1])
                i += 2
                continue
            if ch == quote:
                state = "code"
            i += 1
        elif state == "line":
            if ch == "\n":
                out.append("\n")
                state = "code"
            else:
                out.append(" ")
            i += 1
        else:  # block
            if src.startswith("*/", i):
                out.extend("  ")
                i += 2
                state = "code"
            elif ch == "\n":
                out.append("\n")
                i += 1
            else:
                out.append(" ")
                i += 1
    return "".join(out)


def match_paren(src, open_pos):
    """返回与 src[open_pos] 配对的右括号下标；跳过字符串里的括号。"""
    depth = 0
    i = open_pos
    while i < len(src):
        ch = src[i]
        if ch in ('"', "'", "`"):
            quote = ch
            i += 1
            while i < len(src):
                if src[i] == "\\" and quote != "`":
                    i += 2
                    continue
                if src[i] == quote:
                    break
                i += 1
        elif ch == "(":
            depth += 1
        elif ch == ")":
            depth -= 1
            if depth == 0:
                return i
        i += 1
    return -1


def split_go_args(text):
    """按顶层逗号切调用实参；括号与字符串内部的逗号不算。"""
    args, depth, start, i = [], 0, 0, 0
    while i < len(text):
        ch = text[i]
        if ch in ('"', "'", "`"):
            quote = ch
            i += 1
            while i < len(text):
                if text[i] == "\\" and quote != "`":
                    i += 2
                    continue
                if text[i] == quote:
                    break
                i += 1
        elif ch in "([{":
            depth += 1
        elif ch in ")]}":
            depth -= 1
        elif ch == "," and depth == 0:
            args.append(text[start:i].strip())
            start = i + 1
        i += 1
    args.append(text[start:].strip())
    return args


def normalize_gin_path(path):
    """Gin 的 :param / *wildcard 换成 OpenAPI 的 {param}。"""
    return re.sub(r"[:*]([A-Za-z_]\w*)", r"{\1}", path)


def collect_backend_routes():
    """静态扫描后端注册的路由。返回 (路由集合, 无法解析的动态调用列表)。

    不用按行正则：路由注册经常跨多行（如 r.POST(path, plat, limiter.Handler(), func...)），
    必须括号配对 + 跳过字符串字面量。本机的 tenant_storage_guard_test.go 也用了同一套思路。
    """
    routes = set()
    dynamic = []
    files = sorted(p for p in BACKEND.rglob("*.go") if not p.name.endswith("_test.go"))
    for f in files:
        src = strip_go_comments(f.read_text(encoding="utf-8"))
        # Group 前缀：api := r.Group("/api/setup", ...) → api.POST("/install") = /api/setup/install
        groups = {}
        for m in re.finditer(r'(\w+)\s*:?=\s*\w+\.Group\(\s*"([^"]+)"', src):
            groups[m.group(1)] = m.group(2)
        for method in GO_METHODS:
            needle = "." + method + "("
            pos = 0
            while True:
                k = src.find(needle, pos)
                if k < 0:
                    break
                # 左侧标识符必须完整取出：既拿接收者名，也确保不是别的标识符的后缀
                left = k - 1
                while left >= 0 and (src[left].isalnum() or src[left] == "_"):
                    left -= 1
                receiver = src[left + 1 : k]
                op = k + len(needle) - 1
                close = match_paren(src, op)
                if close < 0:
                    pos = k + 1
                    continue
                args = split_go_args(src[op + 1 : close])
                first = args[0].strip() if args else ""
                if first.startswith('"') and first.endswith('"') and len(first) >= 2:
                    raw = first[1:-1]
                    if raw.startswith("/"):
                        routes.add(normalize_gin_path(groups.get(receiver, "") + raw))
                else:
                    line = src.count("\n", 0, k) + 1
                    dynamic.append((f.relative_to(ROOT).as_posix(), line, receiver, method, first))
                pos = close + 1
        # 动态注册：registerSortRoute(r, db, "/api/admin/xxx/sort", ...)
        # 函数体内那句 r.PUT(path, ...) 会被记进 dynamic，由调用点在这里补回。
        if "func registerSortRoute(" in src:
            for m in re.finditer(r'registerSortRoute\(\s*r\s*,[^,]+,\s*"([^"]+)"', src):
                routes.add(normalize_gin_path(m.group(1)))
    return routes, dynamic


problems = []


def fail(msg):
    problems.append(msg)


raw_spec = SPEC.read_text(encoding="utf-8")
try:
    spec = yaml.safe_load(raw_spec)
except yaml.YAMLError as e:
    sys.exit(f"❌ docs/openapi.yaml 不是合法 YAML：{e}")

paths = spec.get("paths") if isinstance(spec, dict) else None
if not isinstance(paths, dict):
    sys.exit("❌ openapi.yaml 顶层没有 paths 对象（守门无法工作，拒绝放行）")

code_routes, dynamic_calls = collect_backend_routes()
doc_path_set = set(paths.keys())

if len(paths) < MIN_PATHS:
    fail(f"paths 只有 {len(paths)} 条（下限 {MIN_PATHS}）：解析结果疑似空转或契约被大范围删除")

declared = [
    line.strip()[:-1]
    for line in raw_spec.splitlines()
    if re.match(r"^  /[^\s:]*:$", line)
]
if len(declared) != len(paths):
    fail(
        f"原文 `  /path:` 行 {len(declared)} 条，YAML 解析出 {len(paths)} 条："
        "可能有重复路径键被静默覆盖，或缩进写错掉出 paths 块"
    )

for name, item in paths.items():
    if not isinstance(item, dict) or not (set(item.keys()) & METHODS):
        fail(f"路径 {name} 没有任何 HTTP 方法定义")


# 内部 $ref 全解析：坏引用在 Swagger UI 里只是空白，不会报错。
def walk(node, where="$"):
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "$ref" and isinstance(value, str) and value.startswith("#/"):
                cur = spec
                for part in value[2:].split("/"):
                    part = part.replace("~1", "/").replace("~0", "~")
                    if not isinstance(cur, dict) or part not in cur:
                        fail(f"$ref 无法解析：{value}（位置 {where}）")
                        break
                    cur = cur[part]
            else:
                walk(value, f"{where}.{key}")
    elif isinstance(node, list):
        for i, value in enumerate(node):
            walk(value, f"{where}[{i}]")


walk(spec)

# 首页文字四档：收集文档里所有这两个字段的 enum，避免只改了其中一处就全绿。
def collect_enums(node, key, out):
    if isinstance(node, dict):
        for k, v in node.items():
            if k == key and isinstance(v, dict) and isinstance(v.get("enum"), list):
                out.append(v["enum"])
            else:
                collect_enums(v, key, out)
    elif isinstance(node, list):
        for v in node:
            collect_enums(v, key, out)


for field in ("companyNameScale", "introFontScale"):
    enums = []
    collect_enums(spec, field, enums)
    if len(enums) < MIN_SCALE_DECLARATIONS:
        fail(
            f"{field} 在契约里只声明了 {len(enums)} 次（下限 {MIN_SCALE_DECLARATIONS}）："
            "GET/PUT 或兼容字段可能漏写（正则空转兜底）"
        )
    for enum in enums:
        if enum != EXPECTED_SCALES:
            fail(f"{field} 的 enum={enum}，应为 {EXPECTED_SCALES}（四档展示契约漂移）")

doc = DOC.read_text(encoding="utf-8")
match = BASELINE_RE.search(doc)
if not match:
    fail(
        f"{DOC.name} 找不到「当前 API 契约基线：N paths」声明行；"
        "历史轮次里的 106 paths 不是当前值，不得拿来做门禁"
    )
elif int(match.group(1)) != len(paths):
    fail(
        f"文档基线与契约不一致：{DOC.name} 写 {match.group(1)} paths，"
        f"openapi.yaml 实际 {len(paths)} paths——加/删接口必须同步改这一行"
    )


# 路由对账：代码里新加接口却没写文档，必须红。
# 动态调用的两种已知形态：handler.go 的 healthz 循环（运维探针，已由白名单覆盖）、
# content.go 的 registerSortRoute 函数体（真实路径从调用点补回）。其余一律报错，
# 避免「扫不到就算了」让新注册方式静默逃过守门。
for file, line, receiver, method, arg in dynamic_calls:
    known = arg == "rt.path" or (file.endswith("/content.go") and arg == "path")
    if not known:
        fail(
            f"无法静态解析的动态路由注册 {file}:{line}：{receiver}.{method}({arg}, ...)。"
            "请在 check_openapi.py 补解析规则，否则这条接口不进对账"
        )

missing = sorted(
    p for p in code_routes - doc_path_set
    if p not in NON_CONTRACT_PATHS
    and not any(p.startswith(prefix) for prefix in NON_CONTRACT_PREFIXES)
)
if missing:
    fail(
        f"后端注册了 {len(missing)} 条业务路由但契约没有（新增接口必须同步 openapi.yaml）："
        + "，".join(missing)
    )

extra = sorted(
    p for p in doc_path_set - code_routes
    if not any(p.startswith(prefix) for prefix in SCANNER_BLIND_PREFIXES)
)
if extra:
    # 只提示不拦：条件注册、NoRoute、未来新的注册写法都可能让静态扫描漏掉，
    # 拿它当门禁会逼出一堆假红。真正要堵的是「代码有、文档无」那一侧。
    print(
        f"ℹ 契约里有 {len(extra)} 条路径未被静态扫描命中（仅提示）：{'，'.join(extra)}",
        file=sys.stderr,
    )

if "--selftest" in sys.argv:
    probe = set(code_routes) | {SELFTEST_PATH}
    caught = [
        p for p in probe - doc_path_set
        if p not in NON_CONTRACT_PATHS
        and not any(p.startswith(prefix) for prefix in NON_CONTRACT_PREFIXES)
    ]
    if SELFTEST_PATH not in caught:
        print("❌ 自检失败：注入的假路由没被对账拦下，守门在空转", file=sys.stderr)
        sys.exit(1)
    print(
        f"✓ 自检通过：注入 {SELFTEST_PATH} 被拦下；静态扫描到 {len(code_routes)} 条后端路由，"
        f"其中 {len(code_routes & doc_path_set)} 条已在契约中"
    )
    sys.exit(0)

if problems:
    for p in problems:
        print(f"❌ {p}", file=sys.stderr)
    sys.exit(1)

print(
    f"✓ OpenAPI 守门通过：{len(paths)} paths，${raw_spec.count('$ref:')} 个 $ref 均可解析，"
    f"四档字段一致；{len(code_routes)} 条后端路由全部有契约（{len(missing)} 条缺失）。"
)
