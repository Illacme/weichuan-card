#!/usr/bin/env python3
"""OpenAPI 契约守门：文档可解析、路径数对得上、关键字段没漂移。

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

用法：python3 scripts/check_openapi.py
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

if problems:
    for p in problems:
        print(f"❌ {p}", file=sys.stderr)
    sys.exit(1)

print(f"✓ OpenAPI 守门通过：{len(paths)} paths，${raw_spec.count('$ref:')} 个 $ref 均可解析，四档字段一致。")
