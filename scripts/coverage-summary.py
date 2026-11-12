#!/usr/bin/env python3
"""按覆盖率剖面生成可读的覆盖率汇总（题目 05）。

统计口径与 `go tool cover -func` 一致：按语句数统计 covered/total，
而不是取各函数覆盖率的最小值（后者会把整体统计拉低，且不常用）。

用法：
    go test ./... -count=1 -coverprofile=evidence/coverage.out -covermode=atomic
    python scripts/coverage-summary.py --profile evidence/coverage.out \
        --out evidence/coverage-summary.txt

退出码：0 = 生成成功且所有核心业务包 ≥ 阈值；1 = 有核心包未达标或生成失败。
"""
from __future__ import annotations

import argparse
import subprocess
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

# 核心业务包：题面要求「各核心业务包分别 ≥85%」。
CORE_PACKAGES = ["domain", "httpapi", "service", "store", "upstream"]
# 其他自编包：列出但不参与门槛判定（共同要求只规定核心包）。
OTHER_PACKAGES = ["cmd/mockupstream", "cmd/server"]
THRESHOLD = 85.0


def parse_profile(path: Path) -> dict[str, dict[str, int]]:
    """解析覆盖率剖面，返回 包路径 -> {total, covered}。"""
    stats: dict[str, dict[str, int]] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.strip() or line.startswith("mode:"):
            continue
        parts = line.rsplit(" ", 2)
        if len(parts) != 3:
            continue
        location, statements, count = parts
        pkg = location.split(":")[0]
        entry = stats.setdefault(pkg, {"total": 0, "covered": 0})
        total = int(statements)
        covered = total if int(count) > 0 else 0
        entry["total"] += total
        entry["covered"] += covered
    return stats


def pkg_key(location: str) -> str:
    """把剖面里的文件路径归并到包名。"""
    normalized = location.replace("\\", "/")
    if "/internal/" in normalized:
        return normalized.split("/internal/", 1)[1].split("/", 1)[0]
    if "/cmd/" in normalized:
        parts = normalized.split("/cmd/", 1)[1].split("/")
        return "/".join(["cmd", parts[0]])
    return normalized


def main() -> int:
    ap = argparse.ArgumentParser(description="生成覆盖率汇总")
    ap.add_argument("--profile", default="evidence/coverage.out")
    ap.add_argument("--out", default="evidence/coverage-summary.txt")
    ap.add_argument("--threshold", type=float, default=THRESHOLD)
    args = ap.parse_args()

    profile = Path(args.profile)
    if not profile.is_file():
        print(f"覆盖率剖面不存在：{profile}", file=sys.stderr)
        return 1

    raw = parse_profile(profile)
    stats: dict[str, dict[str, int]] = {}
    for location, entry in raw.items():
        key = pkg_key(location)
        merged = stats.setdefault(key, {"total": 0, "covered": 0})
        merged["total"] += entry["total"]
        merged["covered"] += entry["covered"]

    total_all = {"total": 0, "covered": 0}
    for entry in stats.values():
        total_all["total"] += entry["total"]
        total_all["covered"] += entry["covered"]

    def pct(entry: dict[str, int]) -> float:
        if not entry["total"]:
            return 0.0
        return round(entry["covered"] * 100.0 / entry["total"], 1)

    try:
        go_version = subprocess.run(
            ["go", "version"], capture_output=True, text=True, check=False).stdout.strip()
    except OSError:
        go_version = "go version 不可获取"

    cst = timezone(timedelta(hours=8))
    now = datetime.now(cst).strftime("%Y-%m-%d %H:%M:%S %z")

    lines: list[str] = []
    lines.append("# 覆盖率汇总（题目 05）")
    lines.append(f"# 执行时间：{now}（本机时间，UTC+8）")
    lines.append(f"# Go 版本：{go_version}")
    lines.append("# 命令：go test ./... -count=1 -coverprofile=evidence/coverage.out -covermode=atomic")
    lines.append(f"# 覆盖率原件：{args.profile}（由上述命令直接产出，未做人工修改）")
    lines.append("# 统计口径：按覆盖率剖面逐块统计语句数（covered/total），非函数最小值口径")
    lines.append("")
    lines.append(f"### 一、核心业务包（阈值 {args.threshold:.0f}%，任一不达标即不合规）")
    ok = True
    for name in CORE_PACKAGES:
        entry = stats.get(name)
        if not entry or not entry["total"]:
            lines.append(f"- {name}: 无数据 ⚠")
            ok = False
            continue
        value = pct(entry)
        flag = "✅" if value >= args.threshold else "❌"
        if value < args.threshold:
            ok = False
        lines.append(f"- {name}: {value}%  ({entry['covered']}/{entry['total']} 语句)  {flag}")
    lines.append("")
    lines.append(f"### 二、其他自编包（列出但不参与门槛判定；共同要求只规定核心包）")
    for name in OTHER_PACKAGES:
        entry = stats.get(name)
        if not entry or not entry["total"]:
            lines.append(f"- {name}: 无数据 ⚠")
            continue
        lines.append(f"- {name}: {pct(entry)}%  ({entry['covered']}/{entry['total']} 语句)")
    lines.append("")
    lines.append(f"### 三、整体：{pct(total_all)}%  ({total_all['covered']}/{total_all['total']} 语句)，"
                 f"阈值 {args.threshold:.0f}%")
    lines.append("")
    if ok:
        lines.append("结论：整体与全部核心业务包均达到阈值。")
    else:
        lines.append("结论：存在未达标项，需补测试。")

    out_path = Path(args.out)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text("\n".join(lines) + "\n", encoding="utf-8")

    print(f"coverage-summary: overall={pct(total_all)}% core_ok={ok} out={out_path}")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
