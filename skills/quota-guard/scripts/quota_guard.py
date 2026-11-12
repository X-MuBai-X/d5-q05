#!/usr/bin/env python3
"""quota-guard：题目 05 的配额计量口径守卫。

静态不变量检查 + 行为回归 + 核心包覆盖率统计。
退出码：0 = PASS，1 = FAIL，2 = 执行错误。
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
from dataclasses import dataclass, field, asdict
from pathlib import Path

# Windows 控制台默认 GBK，直接打印中文会抛 UnicodeEncodeError。
# 这里把标准输出切到 UTF-8，并对无法编码的字符降级替换。
for _stream in (sys.stdout, sys.stderr):
    if hasattr(_stream, "reconfigure"):
        try:
            _stream.reconfigure(encoding="utf-8", errors="replace")
        except (ValueError, OSError):
            pass

CORE_PACKAGES = ["domain", "store", "service"]
TEST_FILTER = "Quota|Usage|Invoke|Settle|Reserve|Inflight|Idempot|Reject"


@dataclass
class Check:
    no: int
    name: str
    rule: str
    ok: bool
    evidence: str
    detail: str = ""


@dataclass
class Report:
    repo: str
    checks: list[Check] = field(default_factory=list)
    tests: dict = field(default_factory=dict)
    coverage: dict = field(default_factory=dict)
    min_coverage: int = 85

    @property
    def passed(self) -> bool:
        return all(c.ok for c in self.checks) and self.tests.get("ok", False) and self.coverage.get("ok", False)


def find_repo(start: Path) -> Path:
    cur = start.resolve()
    for candidate in [cur, *cur.parents]:
        gomod = candidate / "go.mod"
        if gomod.is_file():
            text = gomod.read_text(encoding="utf-8", errors="replace")
            if "module modelquota" in text:
                return candidate
    raise SystemExit("未找到包含 module modelquota 的 go.mod，请用 --repo 指定仓库根目录")


def read(repo: Path, rel: str) -> str:
    path = repo / rel
    if not path.is_file():
        return ""
    return path.read_text(encoding="utf-8", errors="replace")


def line_of(text: str, pattern: str) -> int:
    """返回第一个匹配 pattern 的行号（1 基），找不到返回 0。"""
    regex = re.compile(pattern)
    for idx, line in enumerate(text.splitlines(), start=1):
        if regex.search(line):
            return idx
    return 0


def run(cmd: list[str], cwd: Path) -> tuple[int, str]:
    proc = subprocess.run(
        cmd, cwd=cwd, capture_output=True, text=True, encoding="utf-8", errors="replace"
    )
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def static_checks(repo: Path) -> list[Check]:
    store = read(repo, "internal/store/store.go")
    domain = read(repo, "internal/domain/domain.go")
    service = read(repo, "internal/service/service.go")
    checks: list[Check] = []

    # 1. 额度占用必须是单条带条件的 UPDATE，且额度上限由本条语句内部实时读取。
    #    2026-11-11 独立验收 Agent 复现的 D1：额度若作为外部参数传入，
    #    并发降额会与判定交错，导致「占用数超过额度」。因此这里额外要求
    #    条件里出现 tenants.daily_quota 的子查询，且函数签名不得再带额度参数。
    cond_line = line_of(store, r"success_count \+ inflight_count\) < COALESCE\(\(SELECT daily_quota FROM tenants WHERE id=\?\)")
    upd_line = line_of(store, r"UPDATE tenant_day_usage SET inflight_count = inflight_count \+ 1")
    sig_line = line_of(store, r"func \(d \*DB\) ReserveInflight\(ctx context\.Context, tenantID, day string\) \(bool, error\)")
    ok = cond_line > 0 and upd_line > 0 and sig_line > 0
    checks.append(Check(
        1, "额度占用为单条条件 UPDATE，且额度上限在本条语句内实时读取", "R2/D1", ok,
        f"internal/store/store.go:{upd_line}" if upd_line else "internal/store/store.go",
        "未找到「条件内子查询读取 tenants.daily_quota」的原子占用语句，或 ReserveInflight 仍接收外部额度参数" if not ok else "",
    ))

    # 2. 占用前确保当日用量行存在。
    ensure = line_of(store, r"func \(d \*DB\) EnsureDayUsage")
    call = line_of(store, r"if err := d\.EnsureDayUsage\(ctx, tenantID, day\); err != nil")
    ok = ensure > 0 and call > 0
    checks.append(Check(
        2, "占用额度前确保当日用量计数行存在", "R2", ok,
        f"internal/store/store.go:{call}" if call else "internal/store/store.go",
        "EnsureDayUsage 未在 ReserveInflight 中被调用" if not ok else "",
    ))

    # 3. 成功转换：处理中 −1、成功 +1，带守卫。
    commit = line_of(store, r"inflight_count = inflight_count - 1, success_count = success_count \+ 1")
    guard = line_of(store, r"AND inflight_count > 0")
    ok = commit > 0 and guard > 0
    checks.append(Check(
        3, "成功转换：处理中 −1、成功 +1，且带 inflight_count > 0 守卫", "R2/R4", ok,
        f"internal/store/store.go:{commit}" if commit else "internal/store/store.go",
        "CommitSuccess 未按预期同时调整两个计数或缺少守卫" if not ok else "",
    ))

    # 4. 失败/超时释放：处理中 −1，带守卫。
    release = line_of(store, r"func \(d \*DB\) ReleaseInflight")
    rel_stmt = line_of(store, r"UPDATE tenant_day_usage SET inflight_count = inflight_count - 1")
    ok = release > 0 and rel_stmt > 0 and guard > 0
    checks.append(Check(
        4, "失败/超时释放占用：处理中 −1，且带 inflight_count > 0 守卫", "R4", ok,
        f"internal/store/store.go:{rel_stmt}" if rel_stmt else "internal/store/store.go",
        "ReleaseInflight 未按预期调整计数或缺少守卫" if not ok else "",
    ))

    # 5. Occupied() = 成功数 + 处理中数。
    occ = line_of(domain, r"func \(u Usage\) Occupied\(\) int \{ return u\.Success \+ u\.InFlight \}")
    ok = occ > 0
    checks.append(Check(
        5, "占用口径 Occupied() = 成功数 + 处理中数", "R2/R5", ok,
        f"internal/domain/domain.go:{occ}" if occ else "internal/domain/domain.go",
        "Occupied() 实现与约定口径不一致" if not ok else "",
    ))

    # 6. 降额校验以 Occupied() 为下限。
    floor = line_of(domain, r"if newQuota < current\.Occupied\(\) \{")
    ok = floor > 0
    checks.append(Check(
        6, "降额下限以当日成功数 + 处理中数为准", "R5", ok,
        f"internal/domain/domain.go:{floor}" if floor else "internal/domain/domain.go",
        "ValidateQuotaChange 未以 Occupied() 作为下限" if not ok else "",
    ))

    # 7. settle 有成功/超时/失败三条独立分支。
    settle = line_of(service, r"func \(s \*Service\) settle\(")
    br_success = line_of(service, r"domain\.RequestSuccess")
    br_timeout = line_of(service, r"domain\.RequestTimeout")
    br_failed = line_of(service, r"domain\.RequestFailed")
    ok = settle > 0 and br_success > 0 and br_timeout > 0 and br_failed > 0
    checks.append(Check(
        7, "终态结算具备成功/超时/失败三条独立分支", "R4/Q1", ok,
        f"internal/service/service.go:{settle}" if settle else "internal/service/service.go",
        "settle 未覆盖三条终态分支" if not ok else "",
    ))

    # 8. 状态校验早于额度占用。
    status_line = line_of(service, r"tenant\.Status == domain\.TenantDisabled")
    reserve_line = line_of(service, r"s\.db\.ReserveInflight\(ctx, tenantID, day\)")
    ok = status_line > 0 and reserve_line > 0 and status_line < reserve_line
    checks.append(Check(
        8, "租户/凭证状态校验早于额度占用（停用后不占额度）", "R6", ok,
        f"internal/service/service.go:{status_line}/{reserve_line}" if status_line and reserve_line else "internal/service/service.go",
        "状态校验与额度占用的先后顺序不符合约定" if not ok else "",
    ))

    # 9. 降额的下限判定与写入必须是同一条语句（D1 的另一半）。
    floor_line = line_of(store, r"func \(d \*DB\) UpdateTenantQuotaWithFloor")
    floor_cond = line_of(store, r"UPDATE tenants SET daily_quota=\?")
    floor_guard = line_of(store, r"COALESCE\(\(SELECT success_count \+ inflight_count FROM tenant_day_usage")
    svc_floor = line_of(service, r"s\.db\.UpdateTenantQuotaWithFloor\(ctx, tenantID, day, newQuota\)")
    ok = floor_line > 0 and floor_cond > 0 and floor_guard > 0 and svc_floor > 0
    checks.append(Check(
        9, "降额下限判定与写入在同一条语句内完成（防 TOCTOU）", "R5/D1", ok,
        f"internal/store/store.go:{floor_line}" if floor_line else "internal/store/store.go",
        "未找到带占用守卫的 UpdateTenantQuotaWithFloor，降额下限可能被并发占用突破" if not ok else "",
    ))

    # 10. 日终报表必须把「占用超过额度」判为不一致（D1 的加重项）。
    over_line = line_of(domain, r"usage\.Occupied\(\) > usage\.QuotaLimit")
    ok = over_line > 0
    checks.append(Check(
        10, "日终报表能识别「占用超过额度」并标记不一致", "R8/D1", ok,
        f"internal/domain/domain.go:{over_line}" if over_line else "internal/domain/domain.go",
        "BuildDailyReport 未检查占用是否超过额度，超额将无法在日报中被发现" if not ok else "",
    ))

    return checks


def behaviour_tests(repo: Path) -> dict:
    cmd = [
        "go", "test",
        "./internal/domain/...", "./internal/store/...", "./internal/service/...",
        "-run", TEST_FILTER, "-count=1",
    ]
    code, out = run(cmd, repo)
    return {"ok": code == 0, "command": " ".join(cmd), "output": out.strip()[-4000:]}


def parse_profile(profile: Path) -> dict[str, dict[str, int]]:
    """解析覆盖率剖面，按包统计语句总数与已覆盖语句数。

    剖面每行格式：<文件>:<起止位置> <语句数> <执行次数>
    执行次数 > 0 表示该块被覆盖。
    """
    stats: dict[str, dict[str, int]] = {}
    for line in profile.read_text(encoding="utf-8", errors="replace").splitlines():
        line = line.strip()
        if not line or line.startswith("mode:"):
            continue
        parts = line.rsplit(" ", 2)
        if len(parts) != 3:
            continue
        location, stmts_raw, count_raw = parts
        try:
            stmts = int(stmts_raw)
            count = int(count_raw)
        except ValueError:
            continue
        pkg = location.split(":")[0].rsplit("/", 1)[0].replace("modelquota/internal/", "")
        entry = stats.setdefault(pkg, {"total": 0, "covered": 0})
        entry["total"] += stmts
        if count > 0:
            entry["covered"] += stmts
    return stats


def coverage(repo: Path, min_cov: int) -> dict:
    with tempfile.TemporaryDirectory() as tmp:
        profile = Path(tmp) / "coverage.out"
        cmd = [
            "go", "test",
            "./internal/domain/...", "./internal/store/...", "./internal/service/...",
            "-coverprofile", str(profile), "-count=1",
        ]
        code, out = run(cmd, repo)
        result = {"ok": False, "packages": {}, "command": " ".join(cmd), "output": out.strip()[-2000:]}
        if code != 0:
            result["error"] = "覆盖率采集失败"
            return result
        if not profile.is_file():
            result["error"] = "未生成覆盖率剖面文件"
            return result
        stats = parse_profile(profile)

    ok = True
    for pkg in CORE_PACKAGES:
        entry = stats.get(pkg)
        if not entry or entry["total"] == 0:
            result["packages"][pkg] = None
            ok = False
            continue
        pct = round(entry["covered"] * 100.0 / entry["total"], 1)
        result["packages"][pkg] = pct
        if pct < min_cov:
            ok = False
    result["ok"] = ok
    return result


def render(report: Report) -> str:
    lines = []
    lines.append("# quota-guard 计量口径守卫报告")
    lines.append("")
    lines.append(f"- 仓库：{report.repo}")
    lines.append(f"- 判定：{'PASS' if report.passed else 'FAIL'}")
    cov = report.coverage.get("packages", {})
    cov_txt = " / ".join(
        f"{pkg} {cov.get(pkg)}%" for pkg in CORE_PACKAGES
    )
    lines.append(f"- 核心包覆盖率（语句口径）：{cov_txt}（下限 {report.min_coverage}%）")
    lines.append("")
    lines.append("## 静态不变量")
    lines.append("")
    lines.append("| # | 检查项 | 规则 | 结论 | 证据 |")
    lines.append("|---|---|---|---|---|")
    for c in report.checks:
        lines.append(f"| {c.no} | {c.name} | {c.rule} | {'PASS' if c.ok else 'FAIL'} | `{c.evidence}` |")
    lines.append("")
    lines.append("## 行为回归")
    lines.append("")
    lines.append(f"- 命令：`{report.tests.get('command', '')}`")
    lines.append(f"- 结果：{'PASS' if report.tests.get('ok') else 'FAIL'}")
    tail = report.tests.get("output", "")
    if tail:
        lines.append("")
        lines.append("```")
        lines.extend(tail.splitlines()[-25:])
        lines.append("```")
    lines.append("")
    lines.append("## 失败项")
    lines.append("")
    failures = [c for c in report.checks if not c.ok]
    if failures:
        for c in failures:
            lines.append(f"- #{c.no} {c.name}（{c.rule}）：{c.detail} —— 证据 `{c.evidence}`")
    else:
        lines.append("无")
    if not report.tests.get("ok", False):
        lines.append("")
        lines.append("行为回归未通过，原始输出见上。")
    if not report.coverage.get("ok", False):
        lines.append("")
        lines.append("覆盖率未达标，见上方核心包覆盖率。")
    lines.append("")
    return "\n".join(lines)


def main() -> int:
    ap = argparse.ArgumentParser(description="题目 05 配额计量口径守卫")
    ap.add_argument("--repo", default=None, help="仓库根目录，默认自动向上查找")
    ap.add_argument("--skip-tests", action="store_true", help="只做静态检查")
    ap.add_argument("--min-coverage", type=int, default=85, help="核心包覆盖率下限")
    ap.add_argument("--out", default=None, help="Markdown 报告输出路径")
    args = ap.parse_args()

    try:
        repo = Path(args.repo).resolve() if args.repo else find_repo(Path.cwd())
    except SystemExit as exc:
        print(f"[quota-guard] {exc}", file=sys.stderr)
        return 2

    report = Report(repo=str(repo), min_coverage=args.min_coverage)
    report.checks = static_checks(repo)

    if args.skip_tests:
        report.tests = {"ok": True, "command": "(skipped by --skip-tests)", "output": ""}
        report.coverage = {"ok": True, "packages": {p: None for p in CORE_PACKAGES}}
    else:
        report.tests = behaviour_tests(repo)
        report.coverage = coverage(repo, args.min_coverage)

    text = render(report)
    if args.out:
        out_path = Path(args.out)
        out_path.parent.mkdir(parents=True, exist_ok=True)
        out_path.write_text(text, encoding="utf-8")
        # 报告已落盘时，控制台只输出 ASCII 摘要，避免控制台编码问题。
        failed = [c.no for c in report.checks if not c.ok]
        print(f"[quota-guard] verdict={'PASS' if report.passed else 'FAIL'} "
              f"checks={len(report.checks)} failed={len(failed)} "
              f"tests={'PASS' if report.tests.get('ok') else 'FAIL'} "
              f"coverage={'PASS' if report.coverage.get('ok') else 'FAIL'} "
              f"report={out_path}")
        if failed:
            print(f"[quota-guard] failed check numbers: {failed}")
    else:
        print(text)
    return 0 if report.passed else 1


if __name__ == "__main__":
    sys.exit(main())
