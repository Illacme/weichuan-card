#!/usr/bin/env python3
"""生成 docs/third-party-licenses.md：后端 Go + 前端 npm 的第三方依赖许可清单。

用法（仓库根目录）：

    python3 scripts/gen_third_party_notices.py            # 重新生成并覆写清单
    python3 scripts/gen_third_party_notices.py --check    # 只检查清单是否过期（CI 用）

判定按许可文件**正文**做，而不是 package.json / go.mod 里的 license 字段——
后者可能与实际文件不符（本次生成就遇到两处不一致，以正文为准）。

范围说明：
  - Go 只取 `go list -deps ./...` 命中的模块，即真正链进二进制的那些；
    仅测试期使用的模块不随产物分发，故不入表。
  - npm 从 package-lock.json 取顶层包并排除 devDependencies（构建工具链不入表）。

--check 的必要性：清单是随专有许可一起交付的声明，依赖一变而清单没重生成，
交付出去的就是与实际不符的声明——这种「文档与代码脱节」没人会记得手工核对，
只能交给 CI 挡住。比对时会忽略「生成日期」那一行，否则清单每天都等于过期。
"""

from __future__ import annotations

import difflib
import json
import os
import re
import subprocess
import sys
from datetime import date
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BACKEND = ROOT / "backend"
FRONTEND = ROOT / "frontend"
OUT = ROOT / "docs" / "third-party-licenses.md"

LIC_FILE_RE = re.compile(r"^(LICENSE|LICENCE|COPYING)", re.I)


def read_license_text(d: Path) -> tuple[str, str]:
    """返回 (许可正文, 依据文件名)；找不到返回 ("", "")。"""
    if not d.is_dir():
        return "", ""
    files = sorted(f for f in os.listdir(d) if LIC_FILE_RE.match(f))
    if not files:
        return "", ""
    f = files[0]
    try:
        return (d / f).read_text(encoding="utf-8", errors="replace"), f
    except OSError:
        return "", f


def classify(text: str) -> str | None:
    """按许可正文判定许可类型，无法识别返回 None。"""
    t = text[:6000].lower()
    is_apache = "apache license" in t and "version 2.0" in t
    is_mit = ("mit license" in t
              or "permission is hereby granted, free of charge" in t
              or "licensed under the mit license" in t)
    if is_apache and is_mit:
        return "MIT / Apache-2.0（双许可，取其一即可）"
    if "mozilla public license version 2.0" in t:
        return "MPL-2.0"
    if "isc license" in t:
        return "ISC"
    if is_apache:
        return "Apache-2.0"
    if is_mit:
        return "MIT"
    if "bsd" in t and "3-clause" in t.replace(" ", ""):
        return "BSD-3-Clause"
    if "redistribution and use in source and binary forms" in t:
        # BSD 家族：第三条款是「不得以作者名义背书」，出现即为 3-Clause。
        # 判据必须是 "endorse"——原文写 "endorse or promote"，用 "endorsement" 会漏判。
        return "BSD-3-Clause" if "endorse" in t else "BSD-2-Clause"
    if "gnu general public license" in t:
        return "GPL（需人工复核：与专有分发不兼容）"
    if "gnu lesser" in t or "gnu library" in t:
        return "LGPL（需人工复核）"
    return None


def go_modules() -> list[tuple[str, str, str]]:
    out = subprocess.run(
        ["go", "list", "-deps", "-f", "{{with .Module}}{{.Path}} {{.Dir}}{{end}}", "./..."],
        cwd=BACKEND, capture_output=True, text=True, check=True,
    )
    rows, seen = [], set()
    for line in out.stdout.splitlines():
        line = line.strip()
        if " " not in line:
            continue
        path, d = line.split(" ", 1)
        if not path or path.startswith("weichuan") or path in seen:
            continue
        seen.add(path)
        text, f = read_license_text(Path(d))
        rows.append((path, classify(text) or "未识别（见模块内许可文件）", f or "-"))
    return sorted(rows)


def npm_packages() -> list[tuple[str, str, str]]:
    lock = json.loads((FRONTEND / "package-lock.json").read_text(encoding="utf-8"))
    rows = []
    for key, pkg in (lock.get("packages") or {}).items():
        if not key.startswith("node_modules/") or "/node_modules/" in key:
            continue  # 只取顶层包
        if pkg.get("dev"):
            continue  # 构建工具链不随产物分发
        name = pkg.get("name") or key[len("node_modules/"):]
        text, f = read_license_text(FRONTEND / key)
        lic = classify(text)
        if lic is None:  # 正文读不到（未安装）时回落到 lock 的声明
            lic = pkg.get("license") or "未识别（需人工复核）"
        rows.append((name, pkg.get("version", ""), lic))
    return sorted(rows)


def table(headers: list[str], rows: list[tuple[str, ...]]) -> str:
    widths = [max(len(h), *(len(r[i]) for r in rows)) for i, h in enumerate(headers)]
    line = "| " + " | ".join(h.ljust(widths[i]) for i, h in enumerate(headers)) + " |"
    sep = "|" + "|".join("-" * (w + 2) for w in widths) + "|"
    body = "\n".join(
        "| " + " | ".join(str(c).ljust(widths[i]) for i, c in enumerate(r)) + " |" for r in rows
    )
    return "\n".join([line, sep, body])


DATE_PREFIX = "> 生成日期："


def without_date(doc: str) -> str:
    """去掉「生成日期」行后再比对。

    清单里带日期是为了交付时能看出新旧，但日期每天都在变；若连它一起比对，
    CI 从第二天起就会无条件失败——那是噪音，不是真的过期。
    """
    return "\n".join(l for l in doc.splitlines() if not l.startswith(DATE_PREFIX))


def check(doc: str) -> int:
    """--check：与已提交的清单比对，不一致则打印差异并返回 1。"""
    if not OUT.exists():
        print(f"[FAIL] 缺少 {OUT}，请先生成", file=sys.stderr)
        return 1
    want = without_date(doc)
    got = without_date(OUT.read_text(encoding="utf-8"))
    if want == got:
        print(f"[OK] {OUT.name} 与当前依赖一致")
        return 0

    diff = list(difflib.unified_diff(
        got.splitlines(), want.splitlines(),
        fromfile=f"{OUT.name}（已提交）", tofile="按当前依赖重新生成", lineterm="",
    ))
    print(f"[FAIL] {OUT.name} 已过期（依赖变了但清单没更新）。", file=sys.stderr)
    print("修复：python3 scripts/gen_third_party_notices.py 然后提交。", file=sys.stderr)
    print("\n".join(diff[:80]), file=sys.stderr)
    if len(diff) > 80:
        print(f"...（差异共 {len(diff)} 行，已截断）", file=sys.stderr)
    return 1


def main() -> int:
    check_only = "--check" in sys.argv[1:]
    if not (FRONTEND / "node_modules").is_dir():
        # 缺 node_modules 时会回落到 package-lock 的 license 声明，而那份声明可能与
        # 实际文件正文不一致——检查出来的「过期」就是假的。宁可明确报错，不要误报。
        msg = f"未找到 {FRONTEND / 'node_modules'}，请先 cd frontend && npm ci"
        if check_only:
            print(f"[FAIL] {msg}", file=sys.stderr)
            return 2
        print(f"[WARN] {msg}；将回落到 package-lock 的 license 声明（可能与正文不符）", file=sys.stderr)
    go_rows = go_modules()
    npm_rows = npm_packages()
    go_count, npm_count = len(go_rows), len(npm_rows)

    doc = f"""# 第三方依赖许可清单（自动生成）

> 生成日期：{date.today().isoformat()}　·　随依赖版本变动会失效，交付前请重新生成一次。
>
> 本软件（微传名片）为专有商业软件，许可见 [../LICENSE](../LICENSE)。
> **本清单中的第三方组件不因该许可而改变**——它们各自仍受其原始许可约束。
> 概览与人工复核要点见 [../NOTICES.md](../NOTICES.md)。

## 生成方式

```bash
python3 scripts/gen_third_party_notices.py    # 仓库根目录执行，覆写本文件
```

判定依据是各依赖目录里的**许可文件正文**（不是 package.json / go.mod 的 license 字段——
后者可能与实际文件不符，本次生成就遇到两处不一致，一律以正文为准）。
BSD 家族按第三条款（不得以作者名义背书）区分 2-Clause / 3-Clause。

## 后端 Go 依赖（{go_count} 个，随服务二进制分发）

{table(["模块", "许可", "依据文件"], go_rows)}

> 只含 `go list -deps ./...` 命中的模块，即真正链进 `cmd/server` / `cmd/devserver` 的那些；
> 仅测试期使用的模块（如 `github.com/stretchr/testify`）不随产物分发，故不在表内。

## 前端 npm 依赖（运行时 {npm_count} 个，随 `npm run build` 产物分发）

{table(["包", "版本", "许可"], npm_rows)}

> 已排除 `devDependencies`（构建工具链不随前端产物分发）。

## 需要留意的许可

| 组件 | 许可 | 为什么留意 |
|---|---|---|
| `github.com/go-sql-driver/mysql` | MPL-2.0 | **文件级 copyleft**：作为未修改的依赖使用不影响专有分发；但若**修改了它的源文件**，该等文件须按 MPL 公开源码（同一文件之外的代码不受传染）。当前未修改。 |
| `github.com/minio/minio-go/v7` 等 minio 系、`github.com/klauspost/compress`、`github.com/go-ini/ini`、`go.yaml.in/yaml/v3` | Apache-2.0 | 要求保留 NOTICE 文件（如有）与变更声明；仅作未修改依赖时义务较轻。 |
| `golang.org/x/*`、`google.golang.org/protobuf` 等 | BSD-3-Clause | 二进制分发须在文档/材料中保留版权声明——本文件即承担该义务。 |
| `github.com/klauspost/compress` | BSD-3-Clause + Apache-2.0 | 一个 LICENSE 里含两份许可：沿用 Go Authors 的部分按 BSD-3，新增部分按 Apache-2.0。 |
| `gopkg.in/yaml.v3`、`go.yaml.in/yaml/v3` | MIT / Apache-2.0 双许可 | 取其一遵守即可。 |
| `typescript`（前端） | Apache-2.0 | 只用于构建期类型检查，不进产物。 |
| Lucide 图标（`backend/internal/assets/library/icons/`） | ISC | 随素材库分发，见 `../NOTICES.md` 与 `backend/internal/assets/library/CREDITS.md`。 |

> 未发现 GPL / LGPL / AGPL 依赖——这类许可与专有分发不兼容，将来若引入须先做评估。
"""
    if check_only:
        return check(doc)
    OUT.write_text(doc, encoding="utf-8")
    print(f"已生成 {OUT}（Go {go_count} 个 + npm {npm_count} 个）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
