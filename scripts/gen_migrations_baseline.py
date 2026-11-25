#!/usr/bin/env python3
"""从 migrations/history/ 的历史增量脚本生成单一基线 0001_init.sql。

背景
----
migrations 顶层曾有 62 个增量文件（0001..0062），是 62 次 schema 演进的逐次记录。
线上无存量、商用按全新部署交付时，这些历史不需要参与部署包：顶层只保留一个
基线文件即可建出最终 schema，历史移入 history/ 归档，仍可追溯。

为什么是「按序拼接」而不是「推导最终表结构」
----------------------------------------
拼接保留每一条原始 DDL/DML 的顺序与文本，终态与逐个执行历史文件逐字一致；
手工推导 CREATE TABLE 最终形态极易漏列/漏索引，且无法覆盖数据修正类迁移
（0062 内置存储改名）。apply.go 的 applyFile 按 ";" 切分并忽略重复错误，
因此拼接后的幂等性与原来完全相同。

用法
----
    # 首次：把历史增量归档（脚本只从 history 读，不读顶层旧文件）
    mkdir -p backend/migrations/history
    git mv backend/migrations/0*.sql backend/migrations/history/

    # 之后随时可重建基线（幂等）
    python3 scripts/gen_migrations_baseline.py

生成的基线由 backend/internal/migrate/baseline_fidelity_test.go 守门：
基线语句序列必须与 history 里全部历史文件按序拼接的语句序列逐条一致。
未来新增迁移用顶层 0063_xxx.sql 增量文件，不要改基线。
"""
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent
MIGRATIONS_DIR = REPO_ROOT / "backend/migrations"
HISTORY_DIR = MIGRATIONS_DIR / "history"
BASELINE = MIGRATIONS_DIR / "0001_init.sql"


def is_comment_only(stmt: str) -> bool:
    """与 apply.go 的 isCommentOnly 同语义：每段只由 -- 注释行/空行组成。"""
    for line in stmt.split("\n"):
        line = line.strip()
        if line and not line.startswith("--"):
            return False
    return True


def statements(raw: str) -> list[str]:
    """与 apply.go 的 applyFile 同语义：按 ';' 切分，丢空段与纯注释段。"""
    out = []
    for stmt in raw.split(";"):
        s = stmt.strip()
        if s and not is_comment_only(s):
            out.append(s)
    return out


def main() -> None:
    if not HISTORY_DIR.is_dir():
        sys.exit(f"❌ 找不到 {HISTORY_DIR}；先把历史增量 git mv 进 history/ 再运行")

    files = sorted(p for p in HISTORY_DIR.glob("*.sql") if p.is_file())
    if not files:
        sys.exit(f"❌ {HISTORY_DIR} 下没有 .sql 文件")

    # 保真验证：历史按序拼接的语句序列 == 基线文件的语句序列。
    expected: list[str] = []
    parts: list[str] = []
    for p in files:
        raw = p.read_text(encoding="utf-8")
        expected.extend(statements(raw))
        # 分隔用的孤立 ";" 不可省：apply.go 按 ";" 切分，注释头若不独立成段，
        # 会和下一条语句并成一段，语句序列就不再与历史逐条一致（生成器自身会拦）。
        parts.append(f"-- ===== 基线合并：来源 {p.name} =====\n;\n")
        parts.append(raw.rstrip("\n") + "\n\n")

    merged = "".join(parts)
    actual = statements(merged)
    if actual != expected:
        for i, (a, b) in enumerate(zip(actual, expected)):
            if a != b:
                sys.exit(f"❌ 拼接改变了语句序列：第 {i + 1} 条不一致\n基线: {a[:120]}\n历史: {b[:120]}")
        sys.exit(f"❌ 拼接改变了语句数量：基线 {len(actual)} 条，历史 {len(expected)} 条")

    BASELINE.write_text(merged, encoding="utf-8")
    print(f"✓ 已生成 {BASELINE.relative_to(REPO_ROOT)}"
          f"（{len(files)} 个历史文件，{len(actual)} 条语句，语句序列逐条保真）")


if __name__ == "__main__":
    main()
