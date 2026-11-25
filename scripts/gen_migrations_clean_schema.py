#!/usr/bin/env python3
"""从 migrations/history/ 的增量脚本推演「纯净终态 schema」，生成 0001_init.sql。

背景
----
商用把当前版本固化为产品初始版：全新安装走纯净 DDL（只有最终态的
CREATE TABLE，没有历史 ALTER / 数据搬迁 UPDATE / DROP），升级安装继续走
history/ 的增量脚本（按 schema_migrations 记账）。两条路径并存。

为什么生成而不是手写
--------------------
history/ 是 schema 的唯一真值源（62 次演进的逐次记录）。手写终态 DDL
极易漏列、漏索引或把中间态当成终态。本脚本在内存里「重放」所有 DDL：
CREATE TABLE → ADD/MODIFY/DROP COLUMN → ADD INDEX → DROP TABLE，
最后按原始定义顺序输出每表的 CREATE TABLE。

保真边界（严格，不静默降级）：
  - 遇到无法识别的 DDL 语句直接报错退出，不猜、不跳过；
  - INSERT / UPDATE / DELETE 一律忽略——它们只搬运历史数据，全新库无行可改；
  - 数据修正（如 0062 内置存储改名 / 0043 删列）靠 CREATE/ALTER 的 DDL
    结构终态体现，不靠 DML 执行结果；
  - CI 的 backend-mysql job 用真实 MySQL 跑完 history 后 dump schema，
    与本生成物逐表比对（见 baseline_schema_test.go / ci.yml），是最终防线。

用法：
    python3 scripts/gen_migrations_clean_schema.py
    python3 scripts/gen_migrations_clean_schema.py --check   # 只校验不写（CI 用）
"""
import re
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent
HISTORY = REPO_ROOT / "backend/migrations/history"
OUT = REPO_ROOT / "backend/migrations/0001_init.sql"

INSERT_RE = re.compile(r"^\s*(?:--[^\n]*\n)*\s*(INSERT\s+INTO|UPDATE\s|DELETE\sFROM|REPLACE\sINTO)", re.I)


def split_statements(raw: str) -> list[str]:
    """按 ';' 切分，去掉纯注释段；与 apply.go 的 applyFile 同语义。"""
    out = []
    for stmt in raw.split(";"):
        lines = [ln.strip() for ln in stmt.split("\n")]
        if any(ln and not ln.startswith("--") for ln in lines):
            out.append(stmt.strip())
    return out


def strip_comments(s: str) -> str:
    return "\n".join(
        ln for ln in s.split("\n") if not ln.lstrip().startswith("--")
    ).strip()


def split_top_level(s: str, sep: str = ",") -> list[str]:
    """按顶层分隔符切分，忽略括号内逗号（列定义/索引列都有括号）。"""
    parts, buf, depth, quote = [], [], 0, None
    for ch in s:
        if quote:
            buf.append(ch)
            if ch == quote:
                quote = None
            continue
        if ch in "'\"`":
            quote = ch
            buf.append(ch)
        elif ch == "(":
            depth += 1
            buf.append(ch)
        elif ch == ")":
            depth -= 1
            buf.append(ch)
        elif ch == sep and depth == 0:
            parts.append("".join(buf).strip())
            buf = []
        else:
            buf.append(ch)
    if "".join(buf).strip():
        parts.append("".join(buf).strip())
    return parts


def parse_create(stmt: str) -> tuple[str, list[str], str]:
    m = re.match(
        r"CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?`?([a-z_][a-z0-9_]*)`?\s*\((.*)\)\s*(.*)$",
        stmt, re.I | re.S,
    )
    if not m:
        raise ValueError(f"无法识别 CREATE TABLE：{stmt[:120]}")
    table, body, suffix = m.group(1), m.group(2), m.group(3).strip()
    items = split_top_level(body)
    return table, items, suffix


def column_name(item: str) -> str | None:
    if item.upper().startswith(("PRIMARY KEY", "KEY", "UNIQUE KEY", "INDEX", "UNIQUE INDEX", "CONSTRAINT")):
        return None
    m = re.match(r"`?([a-z_][a-z0-9_]*)`?\s+", item)
    return m.group(1) if m else None


def key_name(item: str) -> str | None:
    m = re.match(r"(?:UNIQUE\s+)?(?:KEY|INDEX)\s+`?([a-z_][a-z0-9_]*)`?", item, re.I)
    return m.group(1) if m else None


def apply_alter(items: list[str], stmt: str) -> None:
    """在内存表结构上应用一条 ALTER（可含多个逗号分隔的动作）。"""
    m = re.match(r"ALTER\s+TABLE\s+`?([a-z_][a-z0-9_]*)`?\s+(.+)$", stmt, re.I | re.S)
    if not m:
        raise ValueError(f"无法识别 ALTER TABLE：{stmt[:120]}")
    table, actions_raw = m.group(1), m.group(2)
    # 只在顶层是 ADD/MODIFY/DROP 时切分；AFTER col 里的逗号不会出现在顶层。
    actions = split_top_level(actions_raw)
    names = {column_name(i) for i in items} | {key_name(i) for i in items}
    for act in actions:
        up = act.upper()
        if up.startswith("ADD UNIQUE INDEX") or up.startswith("ADD UNIQUE KEY") or up.startswith("ADD INDEX") or up.startswith("ADD KEY"):
            key = re.sub(r"^ADD\s+", "", act, flags=re.I)
            kn = key_name(key)
            if kn is None or kn in names:
                raise ValueError(f"ADD INDEX 无法解析或重复：{act[:120]}")
            items.append(key)
            names.add(kn)
        elif up.startswith("ADD COLUMN") or re.match(r"ADD\s+`?[a-z_]", act, re.I):
            col = re.sub(r"^ADD\s+(COLUMN\s+)?", "", act, flags=re.I)
            nm = column_name(col)
            if nm is None or nm in names:
                raise ValueError(f"ADD COLUMN 无法解析或重复：{act[:120]}")
            # 去掉 AFTER <col>：终态 CREATE TABLE 按原始定义顺序输出，不需要位置修饰。
            col = re.sub(r"\s+AFTER\s+`?[a-z_][a-z0-9_]*`?\s*$", "", col, flags=re.I).strip()
            items.append(col)
            names.add(nm)
        elif up.startswith("MODIFY COLUMN") or up.startswith("MODIFY ") or up.startswith("CHANGE COLUMN") or up.startswith("CHANGE "):
            # MODIFY/CHANGE 替换列定义但保持原位置（CHANGE 可能改名，本项目 CHANGE 无实例）。
            col = re.sub(r"^(MODIFY|CHANGE)(\s+COLUMN)?\s+", "", act, flags=re.I)
            nm = column_name(col)
            if nm is None:
                raise ValueError(f"MODIFY COLUMN 无法解析：{act[:120]}")
            for i, item in enumerate(items):
                if column_name(item) == nm:
                    items[i] = col
                    break
            else:
                raise ValueError(f"MODIFY 的列不存在：{nm}")
        elif up.startswith("DROP COLUMN") or up.startswith("DROP "):
            nm = re.sub(r"^DROP\s+(COLUMN\s+)?", "", act, flags=re.I).strip().strip("`")
            idx = next((i for i, item in enumerate(items) if column_name(item) == nm), None)
            if idx is None:
                raise ValueError(f"DROP 的列不存在：{nm}")
            items.pop(idx)
            names.discard(nm)
        elif up.startswith("DROP INDEX") or up.startswith("DROP KEY"):
            nm = re.sub(r"^DROP\s+(INDEX|KEY)\s+", "", act, flags=re.I).strip().strip("`")
            idx = next((i for i, item in enumerate(items) if key_name(item) == nm), None)
            if idx is None:
                raise ValueError(f"DROP 的索引不存在：{nm}")
            items.pop(idx)
            names.discard(nm)
        else:
            raise ValueError(f"无法识别的 ALTER 动作：{act[:120]}")


def main(check: bool) -> None:
    files = sorted(p for p in HISTORY.glob("*.sql") if p.is_file())
    if not files:
        sys.exit(f"❌ {HISTORY} 下没有历史增量")

    tables: dict[str, tuple[list[str], str]] = {}
    order: list[str] = []
    for p in files:
        for stmt in split_statements(p.read_text(encoding="utf-8")):
            code = strip_comments(stmt)
            up = code.upper()
            if up.startswith("CREATE TABLE"):
                name, items, suffix = parse_create(code)
                if name in tables:
                    raise ValueError(f"{p.name} 重复建表：{name}")
                tables[name] = (items, suffix)
                order.append(name)
            elif up.startswith("ALTER TABLE"):
                m = re.match(r"ALTER\s+TABLE\s+`?([a-z_][a-z0-9_]*)`?", code, re.I)
                if not m or m.group(1) not in tables:
                    raise ValueError(f"{p.name} ALTER 的表不存在：{code[:100]}")
                apply_alter(tables[m.group(1)][0], code)
            elif up.startswith("DROP TABLE"):
                m = re.match(r"DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?`?([a-z_][a-z0-9_]*)`?", code, re.I)
                if not m or m.group(1) not in tables:
                    raise ValueError(f"{p.name} DROP 的表不存在：{code[:100]}")
                del tables[m.group(1)]
                order.remove(m.group(1))
            elif INSERT_RE.match(code):
                continue  # 历史数据搬迁，全新库无行可改
            elif up.startswith(("CREATE UNIQUE INDEX", "CREATE INDEX")):
                raise ValueError(f"独立 CREATE INDEX 未实现，需扩展推演器：{code[:100]}")
            else:
                raise ValueError(f"无法识别的语句（{p.name}）：{code[:120]}")

    lines = [
        "-- 纯净终态 schema：由 scripts/gen_migrations_clean_schema.py 从 history/ 推演生成。",
        "-- 全新初始化安装用本文件建表；数据由安装向导与内置档案 ensure 写入。",
        "-- 升级安装走 migrations/history/ 的增量脚本，按 schema_migrations 记账。",
        "-- DO NOT EDIT：改 schema 请改 history/ 后重跑生成脚本。",
        "",
    ]
    for name in order:
        items, suffix = tables[name]
        lines.append(f"CREATE TABLE IF NOT EXISTS {name} (")
        lines.append(",\n".join(f"  {i}" for i in items))
        lines.append(f") {suffix};")
        lines.append("")
    content = "\n".join(lines)

    if check:
        if OUT.read_text(encoding="utf-8") != content:
            sys.exit(f"❌ {OUT.relative_to(REPO_ROOT)} 与 history 推演结果不一致，请重跑生成器")
        print(f"✓ 纯净 schema 校验通过：{len(order)} 张表，{sum(len(t[0]) for t in tables.values())} 个列/索引项")
        return

    OUT.write_text(content, encoding="utf-8")
    print(f"✓ 已生成 {OUT.relative_to(REPO_ROOT)}（{len(order)} 张表，"
          f"{sum(len(t[0]) for t in tables.values())} 个列/索引项）")


if __name__ == "__main__":
    main("--check" in sys.argv)
