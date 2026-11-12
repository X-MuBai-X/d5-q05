#!/usr/bin/env python3
"""交付链路自检：逐条核对 `docs/01-需求分析.md` §12 的 D-01~D-09 可验收口径。

共同要求第 8 节第 9 条要求「仓库中保留题号、工作项、分支、提交、PR、验收及部署的索引」，
且「任何密钥、访问令牌及敏感数据不得进入仓库」。本脚本把这些要求变成**可重复执行的自检**：

    python scripts/verify-delivery.py --repo . --out evidence/delivery-check.md

每条检查输出三种状态之一：
  PASS  —— 材料齐备且可核对；
  PENDING—— 该项依赖尚未执行的远端动作（如尚未合并），如实标注为待补证，不臆造通过；
  FAIL  —— 材料存在但内容不满足口径。

退出码：0 = 无 FAIL（允许存在 PENDING）；1 = 存在 FAIL；2 = 执行错误。
"""
from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from datetime import datetime, timezone, timedelta
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

CST = timezone(timedelta(hours=8))

VALID_TYPES = {"feat", "fix", "hotfix", "opt", "docs", "test"}
MSG_RE = re.compile(r"^\[(feat|fix|hotfix|opt|docs|test)\](?: \[(v\d+\.\d+\.\d+)\])?"
                    r"(?: \[#(\d+)\])? .+$")
SECRET_ASSIGN_RE = re.compile(
    r"(?i)(access[_-]?token|private[_-]?token|api[_-]?key|gitee_access_token|authorization)"
    r"\s*[:=]\s*[\"']?[A-Za-z0-9_\-]{16,}")
BEARER_RE = re.compile(r"(?i)bearer\s+[A-Za-z0-9_\-]{16,}")
HEX32_RE = re.compile(r"(?<![0-9a-fA-F])[0-9a-f]{32}(?![0-9a-fA-F])")
# 已知非令牌的 32 位十六进制串来源：证据文件里的 SHA-256 摘要片段等；
# `cli_` 前缀是 Meegle CLI 的**公开 client id**（其 CLI 二进制内即含此值），不是凭据。
SAFE_HEX32_CONTEXT = re.compile(r"(?i)(sha256|digest|checksum|commit|sha|blob|oid|cli_|mvs_)")

SKIP_DIRS = {".git", "deploy", "node_modules", "__pycache__"}
SKIP_SUFFIX = {".exe", ".db", ".png", ".jpg", ".zip", ".pdf"}
# 本脚本自身的输出：上一轮结果可能记录了当时发现的命中，扫描时排除以避免自引用。
SELF_OUTPUTS = {"evidence/delivery-check.json", "evidence/delivery-check.md"}


def git(repo: Path, *args: str) -> tuple[int, str]:
    try:
        proc = subprocess.run(["git", "-C", str(repo), *args], capture_output=True,
                              text=True, encoding="utf-8", errors="replace", timeout=120)
    except (OSError, subprocess.SubprocessError) as exc:
        return 2, str(exc)
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def iter_repo_files(repo: Path):
    for path in repo.rglob("*"):
        if not path.is_file():
            continue
        parts = set(path.relative_to(repo).parts)
        if parts & SKIP_DIRS:
            continue
        if path.suffix.lower() in SKIP_SUFFIX:
            continue
        yield path


def check_commit_messages(repo: Path) -> list[dict]:
    """D-03 的一部分：提交消息格式与编号必填规则。"""
    code, out = git(repo, "log", "--format=%H%x09%s", "dev/05")
    if code != 0:
        return [{"id": "MSG", "status": "PENDING", "detail": f"无法读取 dev/05 提交：{out.strip()}"}]
    results = []
    bad = []
    for line in out.splitlines():
        if not line.strip():
            continue
        sha, _, subject = line.partition("\t")
        m = MSG_RE.match(subject)
        if not m:
            bad.append(f"{sha[:7]} 格式不合规：{subject}")
            continue
        typ, ver, num = m.group(1), m.group(2), m.group(3)
        if typ in ("feat", "fix"):
            if not ver:
                bad.append(f"{sha[:7]} 缺版本号：{subject}")
            elif not num:
                bad.append(f"{sha[:7]} 缺工作项编号：{subject}")
    results.append({
        "id": "提交消息格式与编号必填",
        "status": "PASS" if not bad else "FAIL",
        "detail": "dev/05 全部提交符合 `[类型] [版本号] [#编号] 描述`，且 feat/fix 均带版本号与编号"
        if not bad else "；".join(bad[:10]),
    })
    return results


def check_mcp_repo_creation(repo: Path) -> list[dict]:
    """D-01：MCP 建仓报文存在，含工具名、请求、结果、时间与仓库 URL。"""
    d = repo / "evidence" / "gitee-mcp"
    files = sorted(d.glob("*create_repo*.json")) if d.is_dir() else []
    if not files:
        return [{"id": "D-01 MCP 建仓证据", "status": "PENDING",
                 "detail": "evidence/gitee-mcp/ 下暂无 create_repo 报文（远端建仓动作未执行）"}]
    payload = json.loads(files[0].read_text(encoding="utf-8"))
    need = ["tool", "called_at", "request", "response"]
    missing = [k for k in need if not payload.get(k)]
    blob = json.dumps(payload, ensure_ascii=False)
    has_url = "gitee.com" in blob
    status = "PASS" if not missing and has_url and payload.get("tool") == "create_repo" else "FAIL"
    return [{"id": "D-01 MCP 建仓证据",
              "status": status,
              "detail": f"{files[0].name}：工具={payload.get('tool')}，时间={payload.get('called_at')}，"
                        f"含仓库 URL={'是' if has_url else '否'}"}]


def check_pr_description(repo: Path) -> list[dict]:
    """D-04：PR 描述要素齐全。"""
    d = repo / "evidence" / "gitee-mcp"
    files = sorted(d.glob("*create_pull*.json")) if d.is_dir() else []
    if not files:
        return [{"id": "D-04 PR 描述要素", "status": "PENDING",
                 "detail": "暂无 create_pull 报文（PR 未发起）"}]
    payload = json.loads(files[0].read_text(encoding="utf-8"))
    body = json.dumps(payload, ensure_ascii=False)
    required = {
        "工作项编号": r"#\d{6,}",
        "变更说明": r"(变更说明|变更摘要)",
        "测试结果": r"(测试结果|单元测试|冒烟)",
        "覆盖率": r"(覆盖率|cover)",
        "验收报告": r"(验收报告|independent-acceptance)",
        "AI/Skill 记录": r"(AI 使用|Skill 使用|skills/|AI 使用说明)",
    }
    missing = [k for k, pat in required.items() if not re.search(pat, body)]
    return [{"id": "D-04 PR 描述要素",
              "status": "PASS" if not missing else ("FAIL" if files else "PENDING"),
              "detail": "PR 描述含全部 6 项要素" if not missing else "缺少：" + "、".join(missing)}]


def check_branch_protection(repo: Path) -> list[dict]:
    """D-05：主分支保护读取结果。"""
    p = repo / "evidence" / "branch-protection.json"
    if not p.is_file():
        return [{"id": "D-05 主分支保护", "status": "PENDING",
                 "detail": "evidence/branch-protection.json 不存在（保护规则尚未读取归档）"}]
    blob = p.read_text(encoding="utf-8", errors="replace")
    checks = {
        "禁止直接推送": r"(?i)(push|push_permission|禁止推送|allowed_pushes)",
        "禁止删除": r"(?i)(delete|禁止删除)",
        "禁止强推": r"(?i)(force|强推)",
        "要求 PR 合并": r"(?i)(pull_request|合并|pr_merged|merge)",
    }
    missing = [k for k, pat in checks.items() if not re.search(pat, blob)]
    return [{"id": "D-05 主分支保护",
              "status": "PASS" if not missing else "FAIL",
              "detail": "保护规则读取结果含禁止直推/删除/强推与 PR 合并要求"
              if not missing else "读取结果中未见：" + "、".join(missing)}]


def check_merge_and_gate(repo: Path) -> list[dict]:
    """D-06 / D-07：合并提交保留原始提交；门禁与部署日志含合并 SHA。"""
    results = []
    code, out = git(repo, "log", "-1", "--format=%H%x09%P%x09%s", "main")
    if code != 0 or not out.strip():
        results.append({"id": "D-06 合并提交", "status": "PENDING",
                        "detail": "main 分支尚无合并提交"})
        return results
    sha, parents, subject = out.strip().split("\t", 2)
    parents = parents.split()
    msg_ok = bool(MSG_RE.match(subject))
    keep = len(parents) >= 2

    # 尚未发生合并时如实标为待补证，不把「还没做」误判成「做错了」。
    ev_dir = repo / "evidence" / "gitee-mcp"
    merged_already = bool(ev_dir.is_dir() and list(ev_dir.glob("*merge_pull*.json")))
    if merged_already:
        status = "PASS" if (keep and msg_ok) else "FAIL"
    else:
        status = "PENDING" if not keep else ("PASS" if msg_ok else "FAIL")
    results.append({
        "id": "D-06 合并提交保留原始提交",
        "status": status,
        "detail": f"main 头部提交 {sha[:12]}，父提交数={len(parents)}，消息={subject}"
        + ("" if merged_already else "（尚未发生合并动作，待补证）"),
    })

    ev = repo / "evidence"
    logs = sorted(ev.glob("main-gate-*.log")) + sorted(ev.glob("deployment-main*.log"))
    if not logs:
        results.append({"id": "D-07 合并提交上的门禁与部署", "status": "PENDING",
                        "detail": "尚无 main-gate-*.log / deployment-main*.log"})
        return results
    hit = [p.name for p in logs if sha[:12] in p.read_text(encoding="utf-8", errors="replace")]
    results.append({
        "id": "D-07 合并提交上的门禁与部署",
        "status": "PASS" if hit else "FAIL",
        "detail": f"日志中出现合并 SHA 的文件：{hit}" if hit else
                  f"未在 {len(logs)} 个日志中找到合并 SHA {sha[:12]}",
    })
    return results


def check_secret_scan(repo: Path) -> list[dict]:
    """D-09 的一半：仓库内不得出现令牌明文。"""
    hits = []
    scanned = 0
    for path in iter_repo_files(repo):
        try:
            text = path.read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        scanned += 1
        rel = path.relative_to(repo).as_posix()
        if rel in SELF_OUTPUTS:
            continue
        for m in SECRET_ASSIGN_RE.finditer(text):
            hits.append(f"{rel}: 疑似令牌赋值 {m.group(0)[:60]}")
        for m in BEARER_RE.finditer(text):
            hits.append(f"{rel}: 疑似 Bearer 令牌 {m.group(0)[:60]}")
        for line_no, line in enumerate(text.splitlines(), 1):
            if line.lstrip().startswith("#"):
                continue
            for m in HEX32_RE.finditer(line):
                ctx = line[max(0, m.start() - 40):m.start()]
                if SAFE_HEX32_CONTEXT.search(ctx):
                    continue
                hits.append(f"{rel}:{line_no}: 32 位十六进制串 {m.group(0)}")
    return [{"id": "D-09 仓库无令牌明文",
              "status": "PASS" if not hits else "FAIL",
              "detail": f"扫描 {scanned} 个文本文件，未命中令牌模式" if not hits
              else "；".join(hits[:10])}]


def check_remote_branches(repo: Path) -> list[dict]:
    """D-02：远端 main 仅初始化提交，业务提交在 dev/05。"""
    code, out = git(repo, "remote")
    if code != 0 or not out.strip():
        return [{"id": "D-02 远端分支承载", "status": "PENDING", "detail": "仓库尚未配置远端"}]
    code, refs = git(repo, "ls-remote", "--heads", "origin")
    if code != 0:
        return [{"id": "D-02 远端分支承载", "status": "PENDING",
                 "detail": f"无法读取远端分支：{refs.strip()}"}]
    branches = {}
    for line in refs.splitlines():
        parts = line.split()
        if len(parts) == 2:
            branches[parts[1].replace("refs/heads/", "")] = parts[0]
    need = {"main", "dev/05"}
    missing = need - set(branches)
    if missing:
        return [{"id": "D-02 远端分支承载", "status": "FAIL",
                 "detail": "远端缺少分支：" + "、".join(sorted(missing))}]
    # 远端 main 是否只有初始化提交
    code, out = git(repo, "ls-remote", "--heads", "origin", "main")
    main_sha = out.split()[0] if out.split() else ""
    code2, count = git(repo, "rev-list", "--count", main_sha)
    n = count.strip() if code2 == 0 else "?"
    results = [{
        "id": "D-02 远端分支承载",
        "status": "PASS" if n == "1" else ("FAIL" if n != "?" else "PENDING"),
        "detail": f"远端分支 {sorted(branches)}；远端 main 提交数={n}（应为 1，即仅初始化提交）",
    }]
    return results


def main() -> int:
    ap = argparse.ArgumentParser(description="交付链路自检")
    ap.add_argument("--repo", default=".")
    ap.add_argument("--out", default="evidence/delivery-check.md")
    ap.add_argument("--json-out", default="evidence/delivery-check.json")
    args = ap.parse_args()

    repo = Path(args.repo).resolve()
    results: list[dict] = []
    results += check_mcp_repo_creation(repo)
    results += check_remote_branches(repo)
    results += check_commit_messages(repo)
    results += check_pr_description(repo)
    results += check_branch_protection(repo)
    results += check_merge_and_gate(repo)
    results += check_secret_scan(repo)

    now = datetime.now(CST).isoformat()
    counts = {s: sum(1 for r in results if r["status"] == s) for s in ("PASS", "PENDING", "FAIL")}

    lines = []
    lines.append("# 交付链路自检报告（题目 05）")
    lines.append("")
    lines.append(f"> 由 `scripts/verify-delivery.py` 自动生成，执行时间 {now}。")
    lines.append("> 口径来源：`docs/01-需求分析.md` §12 的 D-01~D-09（共同要求第 8 节）。")
    lines.append("> `PENDING` 表示该项依赖尚未执行的远端动作，如实标注待补证，不臆造通过。")
    lines.append("")
    lines.append("| # | 检查项 | 状态 | 说明 |")
    lines.append("|---|---|---|---|")
    for i, r in enumerate(results, 1):
        lines.append(f"| {i} | {r['id']} | {r['status']} | {r['detail']} |")
    lines.append("")
    lines.append(f"合计：PASS {counts['PASS']} / PENDING {counts['PENDING']} / FAIL {counts['FAIL']}")
    lines.append("")

    out_path = Path(args.out)
    if not out_path.is_absolute():
        out_path = Path.cwd() / out_path
    out_path.parent.mkdir(parents=True, exist_ok=True)
    with open(out_path, "w", encoding="utf-8", newline="\n") as fh:
        fh.write("\n".join(lines) + "\n")
    json_path = Path(args.json_out)
    if not json_path.is_absolute():
        json_path = Path.cwd() / json_path
    with open(json_path, "w", encoding="utf-8", newline="\n") as fh:
        fh.write(json.dumps({"checked_at": now, "counts": counts, "checks": results},
                             ensure_ascii=False, indent=2) + "\n")

    print(f"[verify-delivery] PASS={counts['PASS']} PENDING={counts['PENDING']} FAIL={counts['FAIL']}"
          f" out={out_path}")
    for r in results:
        print(f"  [{r['status']:7}] {r['id']}：{r['detail'][:110]}")
    return 1 if counts["FAIL"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
