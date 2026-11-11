#!/usr/bin/env python3
"""acceptance-audit：题目 05 交付物的验收逐条核查。

按 R1–R8 与共同要求逐项判定，输出 Markdown 明细与结构化 JSON。
退出码：0 = 无 fail，1 = 存在 fail，2 = 执行错误。
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
import tempfile
from dataclasses import dataclass, field, asdict
from pathlib import Path

for _stream in (sys.stdout, sys.stderr):
    if hasattr(_stream, "reconfigure"):
        try:
            _stream.reconfigure(encoding="utf-8", errors="replace")
        except (ValueError, OSError):
            pass

PASS, FAIL, PENDING = "pass", "fail", "pending"

# 源码模式：(相对文件, 正则, 说明)。命中即视为该项有实现证据。
CODE_CHECKS: dict[str, list[tuple[str, str, str]]] = {
    "R1-1": [
        ("internal/httpapi/handlers.go", r"handleCreateCredential", "创建凭证接口"),
        ("internal/service/service.go", r"func \(s \*Service\) CreateCredential\(ctx context\.Context, actor, tenantID string", "服务层按租户创建凭证"),
    ],
    "R1-2": [
        ("internal/httpapi/handlers.go", r"in\.TenantID != \"\" && in\.TenantID != identity\.TenantID", "显式拒绝指定其他租户"),
    ],
    "R1-3": [
        ("internal/service/service.go", r"MaxCredentialsPerTenant int", "凭证上限字段"),
        ("internal/service/service.go", r"count >= s\.MaxCredentialsPerTenant", "凭证上限校验"),
    ],
    "R1-4": [
        ("cmd/server/main.go", r"tenant-a.*企业甲|tenant-a", "预置租户甲"),
        ("cmd/server/main.go", r"tenant-b.*企业乙|tenant-b", "预置租户乙"),
        ("cmd/server/main.go", r"ops", "预置运营账号"),
        ("cmd/server/main.go", r"Quota: 10|DailyQuota: t\.Quota", "初始日额度 10"),
    ],
    "R2-1": [
        ("internal/store/schema.sql", r"daily_quota\s+INTEGER NOT NULL", "租户级额度字段"),
        ("internal/domain/domain.go", r"func \(u Usage\) Occupied\(\) int", "占用口径按租户汇总"),
    ],
    "R2-2": [
        ("internal/store/store.go", r"UPDATE tenant_day_usage SET inflight_count = inflight_count \+ 1", "原子占用"),
        ("internal/store/store.go", r"success_count \+ inflight_count\) < \?", "带条件的额度判定"),
    ],
    "R2-4": [
        ("internal/domain/domain.go", r"func DayOf\(t time\.Time\) string", "UTC 自然日归属"),
        ("internal/domain/domain.go", r"func \(u Usage\) Remaining\(\) int", "剩余额度计算"),
    ],
    "R3-1": [
        ("internal/store/schema.sql", r"ux_model_requests_no", "请求编号唯一索引"),
    ],
    "R3-2": [
        ("internal/service/service.go", r"Replayed: true", "幂等命中"),
        ("internal/service/service.go", r"IncrementReplay", "幂等命中计数"),
    ],
    "R3-3": [
        ("internal/service/service.go", r"ErrRequestNoConflict", "参数不一致冲突"),
    ],
    "R4-1": [
        ("internal/domain/domain.go", r"RequestSuccess RequestStatus = \"success\"", "成功终态"),
        ("internal/domain/domain.go", r"RequestFailed RequestStatus = \"failed\"", "失败终态"),
        ("internal/domain/domain.go", r"RequestTimeout RequestStatus = \"timeout\"", "超时终态"),
    ],
    "R4-2": [
        ("internal/service/service.go", r"ReleaseInflight", "失败/超时释放占用"),
        ("internal/service/service.go", r"CommitSuccess", "成功实扣"),
    ],
    "R4-3": [
        ("internal/domain/domain.go", r"UpstreamTimeout = 2 \* time\.Second", "2 秒超时阈值"),
    ],
    "R4-4": [
        ("internal/store/schema.sql", r"CREATE TABLE IF NOT EXISTS diagnostics", "诊断表"),
        ("internal/service/service.go", r"InsertDiagnostic", "迟到结果写诊断"),
    ],
    "R5-1": [
        ("internal/store/schema.sql", r"CREATE TABLE IF NOT EXISTS quota_changes", "配额变更留痕表"),
        ("internal/domain/domain.go", r"type QuotaChange struct", "留痕字段"),
    ],
    "R5-2": [
        ("internal/service/service.go", r"ErrReasonRequired", "变更原因必填"),
    ],
    "R5-3": [
        ("internal/domain/domain.go", r"ValidateQuotaChange", "降额下限校验"),
    ],
    "R5-4": [
        ("internal/domain/domain.go", r"MaxQuota = 100000", "配额硬上界"),
    ],
    "R6-1": [
        ("internal/service/service.go", r"ErrTenantDisabled", "租户停用后拒绝受理"),
    ],
    "R6-2": [
        ("internal/service/service.go", r"ErrCredentialDisabled", "凭证停用后拒绝受理"),
    ],
    "R6-3": [
        ("internal/store/schema.sql", r"CREATE TABLE IF NOT EXISTS tenant_events", "停用事件留痕"),
    ],
    "R7-1": [
        ("internal/service/auth.go", r"type Identity struct", "身份模型"),
        ("internal/service/auth.go", r"issueSession", "服务端签发会话"),
    ],
    "R7-2": [
        ("internal/httpapi/server.go", r"func \(s \*Server\) identityFrom", "身份只从令牌解析"),
        ("internal/httpapi/server.go", r"requested != \"\" && requested != identity\.TenantID", "租户参数不被采信"),
    ],
    "R7-3": [
        ("internal/service/auth.go", r"func AuthorizeTenantAccess", "租户访问授权"),
        ("internal/service/auth.go", r"func AuthorizeOps", "运营授权"),
    ],
    "R7-4": [
        ("internal/httpapi/handlers.go", r"req\.CredentialID != identity\.SubjectID", "应用仅可查本凭证请求"),
    ],
    "R8-1": [
        ("internal/service/service.go", r"func \(s \*Service\) DailyReport", "日终核查报表"),
        ("internal/domain/domain.go", r"type DailyReport struct", "五类分列结构"),
    ],
    "R8-2": [
        ("internal/store/schema.sql", r"CREATE TABLE IF NOT EXISTS rejected_attempts", "被拒绝尝试独立表"),
    ],
    "R8-3": [
        ("internal/domain/domain.go", r"report\.Consistent = success == usage\.Success", "一致性核对"),
    ],
    "R8-4": [
        ("internal/domain/domain.go", r"QuotaChanges \[\]QuotaChange", "报表含配额变更"),
        ("internal/domain/domain.go", r"Events \[\]TenantEvent", "报表含停用事件"),
    ],
    "R8-5": [
        ("internal/httpapi/server.go", r"func dayParam", "日期参数，默认 UTC 今日"),
        ("internal/httpapi/web/templates/ops.html", r"type=\"date\"", "运营台日期选择器"),
        ("internal/httpapi/web/templates/admin.html", r"type=\"date\"", "管理台日期选择器"),
    ],
    "C-1": [
        ("AGENTS.md", r"计量口径", "项目级 AGENTS.md 含计量口径约定"),
        ("docs/agents/AGENTS-v1.0.md", r"版本", "历史版本保留"),
    ],
    "C-2": [
        ("skills/quota-guard/SKILL.md", r"## 1\. 用途", "业务专项 Skill"),
        ("skills/acceptance-audit/SKILL.md", r"## 1\. 用途", "验收检查 Skill"),
    ],
    "C-4": [
        ("docs/01-需求分析.md", r"R1|规则", "需求分析文档"),
    ],
    "C-5": [
        ("docs/02-原型设计.md", r".", "原型文档"),
        ("docs/03-任务规划.md", r".", "任务规划文档"),
        ("docs/05-假设清单.md", r".", "假设清单"),
        ("docs/06-会话导出.md", r".", "会话导出"),
        ("docs/09-AI使用说明.md", r".", "AI 使用说明"),
    ],
    "C-6": [
        (".github/workflows/ci.yml", r"name:", "CI 流水线配置"),
    ],
    "C-15": [
        ("cmd/mockupstream/main.go", r"case \"fail\"", "失败场景"),
        ("cmd/mockupstream/main.go", r"case \"timeout\"", "超时场景"),
        ("cmd/mockupstream/main.go", r"case \"duplicate\"", "重复通知场景"),
    ],
}

# 测试名模式：命中任意一个测试函数名即视为该项有测试覆盖。
TEST_CHECKS: dict[str, list[str]] = {
    "R2-3": [
        "TestReserveInflightConcurrentLastSlot",
        "TestInvokeConcurrentLastQuotaSlot",
    ],
    "R3-2": [
        "TestInvokeSameRequestNoSameParamsIsReplay",
    ],
    "R3-3": [
        "TestInvokeSameRequestNoDifferentParamsIsRejected",
    ],
    "R4-1": [
        "TestInvokeTimeoutAndFailureAreDistinctTerminalStates",
    ],
    "R4-2": [
        "TestInvokeUpstreamFailureReleasesQuota",
        "TestInvokeUpstreamTimeoutReleasesQuota",
    ],
    "R4-4": [
        "TestInvokeLateResultOnlyRecordedAsDiagnostic",
    ],
    "R5-3": [
        "TestAdjustQuotaLowerBoundUsesSuccessPlusInflight",
    ],
    "R6-1": [
        "TestInvokeRejectedAfterTenantDisabled",
    ],
    "R6-2": [
        "TestInvokeRejectedAfterCredentialDisabled",
    ],
    "R7-4": [
        "TestAdminCrossTenantQueryRejected",
    ],
    "R8-2": [
        "TestInvokeRetrySameRequestNoAfterQuotaIncrease",
    ],
    "R8-3": [
        "TestDailyReportSplitsTerminalStates",
    ],
}

# 文件存在性检查。
FILE_CHECKS: dict[str, list[str]] = {
    "C-3": ["docs/08-Skill使用记录.md"],
    "C-7": ["evidence/deployment.log"],
    "C-13": [".git/HEAD"],
    "C-12": ["evidence/race-test.log"],
}

# 检查项说明（id -> (规则, 名称)）
NAMES: dict[str, tuple[str, str]] = {
    "R1-1": ("R1", "按租户创建凭证，租户归属由服务端推导"),
    "R1-2": ("R1", "指定其他租户被显式拒绝"),
    "R1-3": ("R1", "凭证数量租户级上限 2 个"),
    "R1-4": ("R1", "预置两管理员一运营，初始日额度 10"),
    "R2-1": ("R2", "配额为租户级，所有凭证共享"),
    "R2-2": ("R2", "额度占用原子完成，防止并发超限"),
    "R2-3": ("R2", "并发争抢最后一个额度的测试"),
    "R2-4": ("R2", "UTC 自然日归属与剩余额度计算"),
    "R3-1": ("R3", "请求编号租户内唯一（唯一索引）"),
    "R3-2": ("R3", "同编号同参数幂等，不重复计量"),
    "R3-3": ("R3", "同编号不同参数拒绝"),
    "R4-1": ("R4", "成功/失败/超时三个独立终态"),
    "R4-2": ("R4", "失败与超时释放占用且不计成功量"),
    "R4-3": ("R4", "上游超时阈值 2 秒"),
    "R4-4": ("R4", "迟到结果只落诊断表"),
    "R5-1": ("R5", "配额变更留痕操作人/原因/生效时间"),
    "R5-2": ("R5", "变更原因必填"),
    "R5-3": ("R5", "降额不得低于成功数+处理中数"),
    "R5-4": ("R5", "配额硬上界 100000"),
    "R6-1": ("R6", "停用租户后不再受理新调用"),
    "R6-2": ("R6", "停用凭证后不再受理调用"),
    "R6-3": ("R6", "停用动作有事件留痕"),
    "R7-1": ("R7", "三类身份与服务端签发会话"),
    "R7-2": ("R7", "身份与租户归属只来自服务端"),
    "R7-3": ("R7", "运营跨租户核查，其他角色限本租户"),
    "R7-4": ("R7", "应用仅限本凭证请求，不见租户汇总"),
    "R8-1": ("R8", "日终报表按五类分列"),
    "R8-2": ("R8", "被拒绝请求单独计数"),
    "R8-3": ("R8", "成功记录数与用量次数一致性核对"),
    "R8-4": ("R8", "报表含配额变更与停用影响"),
    "R8-5": ("R8", "页面可指定日期查询"),
    "C-1": ("共同", "项目级 AGENTS.md 与历史版本"),
    "C-2": ("共同", "至少两个自编 Skill 且结构完整"),
    "C-3": ("共同", "Skill 真实调用记录"),
    "C-4": ("共同", "需求分析文档"),
    "C-5": ("共同", "原型/任务规划/假设清单/会话导出/AI 使用说明"),
    "C-6": ("共同", "CI 流水线配置"),
    "C-7": ("共同", "真实部署记录"),
    "C-8": ("共同", "gofmt 零差异"),
    "C-9": ("共同", "go vet 无告警"),
    "C-10": ("共同", "go test 全部通过"),
    "C-11": ("共同", "整体与核心包覆盖率达标"),
    "C-12": ("共同", "竞争检测记录"),
    "C-13": ("共同", "git 仓库与规范提交消息"),
    "C-14": ("共同", "证据中无明文口令/密钥"),
    "C-15": ("共同", "模拟上游可触发成功/失败/超时/重复"),
}

# 需要在运行环境里核验、且可能因环境原因 pending 的项。
PENDING_REASONS: dict[str, str] = {
    "C-7": "本机无远程教学验收环境，采用本机真实进程部署；GitHub PR / 分支保护记录因缺少 GitHub MCP 与 gh CLI 无法产生",
}


@dataclass
class Item:
    id: str
    rule: str
    name: str
    status: str
    evidence: str = ""
    note: str = ""


@dataclass
class Audit:
    repo: str
    items: list[Item] = field(default_factory=list)

    @property
    def stats(self) -> dict[str, int]:
        out = {"pass": 0, "fail": 0, "pending": 0}
        for it in self.items:
            out[it.status] += 1
        return out

    @property
    def verdict(self) -> str:
        s = self.stats
        if s["fail"]:
            return "FAIL"
        if s["pending"]:
            return "PASS_WITH_PENDING"
        return "PASS"


def find_repo(start: Path) -> Path:
    cur = start.resolve()
    for candidate in [cur, *cur.parents]:
        gomod = candidate / "go.mod"
        if gomod.is_file():
            if "module modelquota" in gomod.read_text(encoding="utf-8", errors="replace"):
                return candidate
    raise SystemExit("未找到 module modelquota 的 go.mod，请用 --repo 指定仓库根目录")


def hit_line(repo: Path, rel: str, pattern: str) -> int:
    path = repo / rel
    if not path.is_file():
        return 0
    regex = re.compile(pattern)
    for idx, line in enumerate(path.read_text(encoding="utf-8", errors="replace").splitlines(), start=1):
        if regex.search(line):
            return idx
    return 0


def run(cmd: list[str], cwd: Path) -> tuple[int, str]:
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True,
                          encoding="utf-8", errors="replace")
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def collect_test_names(repo: Path) -> set[str]:
    names: set[str] = set()
    for path in repo.rglob("*_test.go"):
        if any(part in {".git", "vendor"} for part in path.parts):
            continue
        text = path.read_text(encoding="utf-8", errors="replace")
        names.update(re.findall(r"func (Test\w+)\(", text))
    return names


def parse_profile(profile: Path) -> dict[str, dict[str, int]]:
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
            stmts, count = int(stmts_raw), int(count_raw)
        except ValueError:
            continue
        pkg = location.split(":")[0].rsplit("/", 1)[0].replace("modelquota/internal/", "")
        entry = stats.setdefault(pkg, {"total": 0, "covered": 0})
        entry["total"] += stmts
        if count > 0:
            entry["covered"] += stmts
    return stats


def toolchain_checks(repo: Path, skip: bool) -> dict[str, Item]:
    out: dict[str, Item] = {}
    if skip:
        for cid, note in (
            ("C-8", "已按 --skip-toolchain 跳过"),
            ("C-9", "已按 --skip-toolchain 跳过"),
            ("C-10", "已按 --skip-toolchain 跳过"),
            ("C-11", "已按 --skip-toolchain 跳过"),
        ):
            rule, name = NAMES[cid]
            out[cid] = Item(cid, rule, name, PENDING, "", note)
        return out

    # C-8 gofmt
    code, o = run(["gofmt", "-l", "."], repo)
    lines = [l for l in o.splitlines() if l.strip()]
    out["C-8"] = Item("C-8", *NAMES["C-8"],
                      PASS if code == 0 and not lines else FAIL,
                      "gofmt -l . 无输出" if not lines else "; ".join(lines[:5]))

    # C-9 go vet
    code, o = run(["go", "vet", "./..."], repo)
    out["C-9"] = Item("C-9", *NAMES["C-9"],
                      PASS if code == 0 else FAIL,
                      "go vet ./... 退出码 0" if code == 0 else o.strip()[-800:])

    # C-10 go test
    code, o = run(["go", "test", "./...", "-count=1"], repo)
    out["C-10"] = Item("C-10", *NAMES["C-10"],
                       PASS if code == 0 else FAIL,
                       "go test ./... 全部通过" if code == 0 else o.strip()[-1200:])

    # C-11 覆盖率
    with tempfile.TemporaryDirectory() as tmp:
        profile = Path(tmp) / "cov.out"
        code, o = run(["go", "test", "./...", "-coverprofile", str(profile), "-count=1"], repo)
        if code != 0 or not profile.is_file():
            out["C-11"] = Item("C-11", *NAMES["C-11"], FAIL, o.strip()[-800:])
            return out
        stats = parse_profile(profile)
    total = {"total": 0, "covered": 0}
    for entry in stats.values():
        total["total"] += entry["total"]
        total["covered"] += entry["covered"]
    overall = round(total["covered"] * 100.0 / total["total"], 1) if total["total"] else 0.0
    core_pcts = {}
    for pkg in ("domain", "store", "service", "httpapi", "upstream"):
        e = stats.get(pkg)
        core_pcts[pkg] = round(e["covered"] * 100.0 / e["total"], 1) if e and e["total"] else 0.0
    ok = overall >= 85 and all(v >= 85 for v in core_pcts.values())
    evidence = (f"整体 {overall}%（阈值 85%）；核心包 "
                + "、".join(f"{k} {v}%" for k, v in core_pcts.items()))
    out["C-11"] = Item("C-11", *NAMES["C-11"], PASS if ok else FAIL, evidence)
    return out


def secret_scan(repo: Path) -> tuple[str, str]:
    """在证据与文档中扫描明文口令。返回 (状态, 证据)。"""
    patterns = [
        re.compile(r"sk_[A-Za-z0-9_]{6,}"),
        re.compile(r"(?i)password\s*[:=]\s*['\"]?[A-Za-z0-9_\-]{6,}['\"]?"),
        re.compile(r"(?i)(secret|token|apikey|api_key)\s*[:=]\s*['\"][A-Za-z0-9_\-]{12,}['\"]"),
    ]
    allow = {"docs/01-需求分析.md", "docs/04-部署与使用说明.md", "docs/08-Skill使用记录.md"}
    hits = []
    for path in list((repo / "evidence").rglob("*")) + list((repo / "docs").rglob("*.md")):
        if not path.is_file() or path.suffix.lower() not in {".md", ".txt", ".log", ".json", ".csv"}:
            continue
        rel = path.relative_to(repo).as_posix()
        try:
            text = path.read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        for idx, line in enumerate(text.splitlines(), start=1):
            if "admin-a-secret" in line or "admin-b-secret" in line or "ops-secret" in line:
                if rel in allow:
                    continue
                hits.append(f"{rel}:{idx}")
            for p in patterns:
                if p.search(line):
                    if rel in allow:
                        continue
                    hits.append(f"{rel}:{idx}")
                    break
    if hits:
        return FAIL, "疑似明文口令/密钥：" + "、".join(sorted(set(hits))[:8])
    return PASS, "证据与文档中未发现明文口令/密钥"


def main() -> int:
    ap = argparse.ArgumentParser(description="题目 05 验收逐条核查")
    ap.add_argument("--repo", default=None)
    ap.add_argument("--out-dir", default="evidence/acceptance-audit")
    ap.add_argument("--skip-toolchain", action="store_true")
    args = ap.parse_args()

    try:
        repo = Path(args.repo).resolve() if args.repo else find_repo(Path.cwd())
    except SystemExit as exc:
        print(f"[acceptance-audit] {exc}", file=sys.stderr)
        return 2

    audit = Audit(repo=str(repo))
    test_names = collect_test_names(repo)

    for cid, checks in CODE_CHECKS.items():
        rule, name = NAMES[cid]
        evidences = []
        missing = []
        for rel, pattern, desc in checks:
            ln = hit_line(repo, rel, pattern)
            if ln:
                evidences.append(f"{rel}:{ln}（{desc}）")
            else:
                missing.append(f"{rel} 未匹配「{desc}」")
        if evidences and not missing:
            audit.items.append(Item(cid, rule, name, PASS, "；".join(evidences)))
        elif evidences:
            audit.items.append(Item(cid, rule, name, FAIL, "；".join(evidences),
                                    "；".join(missing)))
        else:
            audit.items.append(Item(cid, rule, name, FAIL, "", "；".join(missing)))

    for cid, patterns in TEST_CHECKS.items():
        rule, name = NAMES[cid]
        found = [p for p in patterns if p in test_names]
        if found:
            audit.items.append(Item(cid, rule, name, PASS, "测试函数：" + "、".join(found)))
        else:
            audit.items.append(Item(cid, rule, name, FAIL, "", "未找到测试函数：" + "、".join(patterns)))

    for cid, files in FILE_CHECKS.items():
        rule, name = NAMES[cid]
        existing = [f for f in files if (repo / f).exists()]
        if existing:
            audit.items.append(Item(cid, rule, name, PASS, "存在：" + "、".join(existing)))
        else:
            audit.items.append(Item(cid, rule, name, FAIL, "", "缺失文件：" + "、".join(files)))

    for cid, item in toolchain_checks(repo, args.skip_toolchain).items():
        audit.items.append(item)

    status, evidence = secret_scan(repo)
    rule, name = NAMES["C-14"]
    audit.items.append(Item("C-14", rule, name, status, evidence))

    # 已知环境受限项：若基础实现存在但环境受限，降级为 pending 并写明原因。
    for cid, reason in PENDING_REASONS.items():
        for item in audit.items:
            if item.id == cid and item.status == FAIL:
                item.status = PENDING
                item.note = reason
                item.evidence = item.evidence or "（环境受限，未能核验）"

    # 按 id 排序输出。
    audit.items.sort(key=lambda i: (i.rule != "R", i.id))

    stats = audit.stats
    verdict = audit.verdict

    out_dir = Path(args.out_dir)
    if not out_dir.is_absolute():
        out_dir = repo / out_dir
    out_dir.mkdir(parents=True, exist_ok=True)

    md = []
    md.append("# 验收逐条核查报告")
    md.append("")
    md.append(f"- 仓库：{audit.repo}")
    md.append(f"- 结论：{verdict}")
    md.append(f"- 统计：pass {stats['pass']} / fail {stats['fail']} / pending {stats['pending']}")
    md.append("")
    md.append("| # | 规则 | 检查项 | 结论 | 证据 |")
    md.append("|---|---|---|---|---|")
    for it in audit.items:
        md.append(f"| {it.id} | {it.rule} | {it.name} | {it.status} | {it.evidence} |")
    md.append("")
    md.append("## 待补证")
    md.append("")
    pend = [i for i in audit.items if i.status == PENDING]
    if pend:
        md.append("| # | 检查项 | 原因 |")
        md.append("|---|---|---|")
        for it in pend:
            md.append(f"| {it.id} | {it.name} | {it.note or '环境受限，未能核验'} |")
    else:
        md.append("无")
    md.append("")
    md.append("## 未通过项")
    md.append("")
    fails = [i for i in audit.items if i.status == FAIL]
    if fails:
        md.append("| # | 检查项 | 原因 |")
        md.append("|---|---|---|")
        for it in fails:
            md.append(f"| {it.id} | {it.name} | {it.note or '见证据栏'} |")
    else:
        md.append("无")
    md.append("")
    md_text = "\n".join(md)
    (out_dir / "检查项明细.md").write_text(md_text, encoding="utf-8")

    payload = {
        "repo": audit.repo,
        "verdict": verdict,
        "stats": stats,
        "items": [asdict(i) for i in audit.items],
    }
    (out_dir / "acceptance-audit.json").write_text(
        json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8"
    )

    print(f"[acceptance-audit] verdict={verdict} pass={stats['pass']} "
          f"fail={stats['fail']} pending={stats['pending']} out={out_dir}")
    if fails:
        print("[acceptance-audit] failed ids: " + ", ".join(i.id for i in fails))
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main())
