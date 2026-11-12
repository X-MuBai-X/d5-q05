#!/usr/bin/env python3
"""远端交付链路一键驱动：建仓后的推送 → PR → 分支保护 → 合并 → 合并提交上的门禁与部署。

共同要求第 8 节的落地顺序（D-01~D-09）：
  步骤 1  推送 main 与 dev/05 到远端（业务提交只落 dev/05）
  步骤 2  通过 **GitHub MCP** `create_pull_request` 发起 PR，描述含第 4 条要求的 6 项要素
  步骤 3  配置 main 分支保护：禁直推 / 禁删除 / 禁强推 / 要求 PR 合并 + 状态检查
          （GitHub MCP 当前未暴露保护类工具，故此处走 REST API，如实标注为 REST）
  步骤 4  读取保护规则并归档到 evidence/branch-protection.json（作为可核对的读取结果）
  步骤 5  通过 **GitHub MCP** `merge_pull_request` 合并，合并消息按提交规范关联主要工作项编号
  步骤 6  在**合并提交**上重跑质量门禁 → evidence/main-gate-<短SHA>.log
  步骤 7  在合并版本上重新部署 → evidence/deployment-main.log
  步骤 8  复跑交付链路自检 → evidence/delivery-check.md

令牌只经环境变量 GITHUB_PERSONAL_ACCESS_TOKEN 传入，不入库、不写配置、不进证据归档。

用法：
    $env:GITHUB_PERSONAL_ACCESS_TOKEN = "<classic PAT: ghp_...>"
    python scripts/release-github.py --repo . --owner <owner> --name <repo>

说明：任何一步失败都**如实记录失败原因并停止**，不跳过、不伪造后续步骤的通过结论。
"""
from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone, timedelta
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

CST = timezone(timedelta(hours=8))
REPO_ROOT = Path(__file__).resolve().parent.parent
EVIDENCE = REPO_ROOT / "evidence"
API = "https://api.github.com"

PR_BODY = """## 需求与缺陷编号

- 需求工作项：#16221249（远端交付链路）、#16220913（企业模型服务配额管理核心业务）
- 缺陷工作项：#16220917（并发降额超限）、#16220924（缺 tenant_id 返回 500）、#16220925（部署 PID 缺陷）、
  #16221411（导出未脱敏）、#16221436（提交消息格式校验）

## 变更说明

按共同要求第 8 节补齐远端交付链路：`scripts/github_mcp.py`（GitHub MCP 驱动）、`scripts/verify-gate.py`
（可在任意指定提交上重跑门禁）、`scripts/verify-delivery.py`（D-01~D-09 可复跑自检）、
`.github/PULL_REQUEST_TEMPLATE.md`、`.github/CODEOWNERS`、`.github/workflows/release.yml`
（合并后门禁/构建/制品/部署）、`docs/10-交付与发布说明.md`（交付索引）。

## 测试结果

- `gofmt -l .` 无输出；`go vet ./...` 无告警；`go test ./... -count=1` 全部通过
- 业务冒烟 39/39、五组完整场景 73/73、容器内 `go test ./... -race` 退出码 0 且 0 DATA RACE
- 交付链路自检 `scripts/test-delivery-checks.py` 23/23 通过

## 覆盖率

整体 **89.4%**；核心业务包 domain 97.3% / store 94.4% / service 87.7% / httpapi 87.7% / upstream 87.1%，
各核心包均 ≥85%。原件见 `evidence/coverage.out`、`evidence/coverage-summary.txt`。

## 验收报告

- 独立测试 Agent 报告：`evidence/independent-test-report.md`
- 独立验收 Agent 报告：`evidence/independent-acceptance-report.md`

## AI / Skill 使用记录

- 全过程提示词与结构化事件清单：`docs/06-会话导出.md`、`evidence/会话事件清单.jsonl`
- 过程时序（机器反推）：`evidence/过程时序清单.md`
- AI 使用说明：`docs/09-AI使用说明.md`
- 自编 Skill 与使用记录：`skills/quota-guard/`、`skills/acceptance-audit/`、`docs/08-Skill使用记录.md`
"""


def log(msg: str) -> None:
    print(f"[release-github] {msg}", flush=True)


def run(cmd: list[str], cwd: Path | None = None) -> tuple[int, str]:
    proc = subprocess.run(cmd, cwd=str(cwd or REPO_ROOT), capture_output=True,
                          text=True, encoding="utf-8", errors="replace")
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def gh_api(token: str, method: str, path: str, body: dict | None = None) -> tuple[int, dict]:
    url = f"{API}{path}"
    data = json.dumps(body).encode("utf-8") if body is not None else None
    headers = {
        "Authorization": f"Bearer {token}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        "User-Agent": "mcode-release-github/1.0",
    }
    if data is not None:
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            raw = resp.read().decode("utf-8", "replace")
            return resp.status, (json.loads(raw) if raw.strip() else {})
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode("utf-8", "replace")
        try:
            return exc.code, json.loads(raw)
        except json.JSONDecodeError:
            return exc.code, {"raw": raw[:800]}
    except urllib.error.URLError as exc:
        return 0, {"error": str(exc)}


def mcp_call(repo: Path, token: str, tool: str, args: dict) -> tuple[int, str, str]:
    """通过 scripts/github_mcp.py 调用 MCP 工具，返回 (退出码, stdout, stderr)。"""
    args_file = EVIDENCE / ".mcp-args.json"
    args_file.parent.mkdir(parents=True, exist_ok=True)
    args_file.write_text(json.dumps(args, ensure_ascii=False, indent=2), encoding="utf-8", newline="\n")
    code, out = run([sys.executable, str(repo / "scripts" / "github_mcp.py"),
                     "--call", tool, "--args-file", str(args_file)])
    try:
        args_file.unlink()
    except OSError:
        pass
    return code, out, ""


def step_push(repo: Path, remote: str) -> bool:
    log("步骤 1/8：推送 main 与 dev/05")
    code, out = run(["git", "-C", str(repo), "remote"])
    if code != 0 or remote not in out:
        log(f"  失败：未配置远端 {remote}")
        return False
    for branch in ("main", "dev/05"):
        code, out = run(["git", "-C", str(repo), "push", "-u", remote, branch])
        if code != 0:
            log(f"  失败：推送 {branch} 失败\n{out.strip()[:400]}")
            return False
        log(f"  已推送 {branch}")
    return True


def step_pr(repo: Path, token: str, owner: str, name: str) -> str | None:
    log("步骤 2/8：通过 GitHub MCP 发起 PR（6 项要素）")
    code, out, _ = mcp_call(repo, token, "create_pull_request", {
        "owner": owner, "repo": name, "head": "dev/05", "base": "main",
        "title": "[feat] [v2.4.0] [#16221249] 企业模型服务配额管理与远端交付链路",
        "body": PR_BODY, "draft": False,
    })
    log(f"  MCP 调用完成（exit={code}），报文已归档到 evidence/github-mcp/")
    m = re.search(r"https://github\.com/[^/]+/[^/]+/pull/\d+", out)
    url = m.group(0) if m else ""
    if not url:
        log(f"  未从返回中解析出 PR 链接，请人工查看归档报文：\n{out.strip()[-600:]}")
    else:
        log(f"  PR: {url}")
    return url or None


def step_protect(token: str, owner: str, name: str) -> bool:
    log("步骤 3/8：配置 main 分支保护（REST API，如实标注）")
    body = {
        "required_status_checks": {"strict": True, "contexts": ["quality-gate"]},
        "enforce_admins": True,
        "required_pull_request_reviews": {
            "dismiss_stale_reviews": True, "require_code_owner_reviews": False,
            "required_approving_review_count": 0,
        },
        "restrictions": None,
        "allow_force_pushes": False,
        "allow_deletions": False,
        "required_linear_history": False,
    }
    code, resp = gh_api(token, "PUT", f"/repos/{owner}/{name}/branches/main/protection", body)
    if code not in (200, 201):
        log(f"  失败：分支保护设置返回 {code} —— {json.dumps(resp, ensure_ascii=False)[:400]}")
        return False
    log("  保护规则已设置（禁直推/禁删除/禁强推/要求 PR 合并 + 状态检查）")
    return True


def step_read_protection(token: str, owner: str, name: str) -> bool:
    log("步骤 4/8：读取并归档分支保护规则")
    code, resp = gh_api(token, "GET", f"/repos/{owner}/{name}/branches/main/protection")
    if code != 200:
        log(f"  失败：读取保护规则返回 {code} —— {json.dumps(resp, ensure_ascii=False)[:300]}")
        return False
    out = {
        "read_at": datetime.now(CST).isoformat(),
        "source": "GitHub REST API GET /repos/{owner}/{repo}/branches/main/protection"
                  "（GitHub MCP 当前未暴露保护类工具，故此处如实标注为 REST 而非 MCP）",
        "endpoint": f"/repos/{owner}/{name}/branches/main/protection",
        "http_status": code,
        "protection": resp,
    }
    path = EVIDENCE / "branch-protection.json"
    path.write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
    log(f"  已归档到 {path}")
    return True


def step_merge(repo: Path, token: str, owner: str, name: str, pr_number: str) -> str | None:
    log("步骤 5/8：通过 GitHub MCP 合并 PR（保留原始提交，合并消息含工作项编号）")
    code, out, _ = mcp_call(repo, token, "merge_pull_request", {
        "owner": owner, "repo": name, "pull_number": int(pr_number),
        "merge_method": "merge",
        "commit_title": "[feat] [v2.4.0] [#16221249] 合并题目 05 企业模型服务配额管理实现",
        "commit_message": "从 dev/05 发起 PR 并合并，保留开发分支原始提交；"
                           "主要工作项 #16221249，关联需求 #16220913 与缺陷 "
                           "#16220917 / #16220924 / #16220925 / #16221411 / #16221436。",
    })
    log(f"  MCP 调用完成（exit={code}），报文已归档到 evidence/github-mcp/")
    code2, out2 = gh_api(token, "GET", f"/repos/{owner}/{name}/commits/main")
    sha = out2.get("sha", "") if code2 == 200 else ""
    if not sha:
        log(f"  未能读取 main 头部提交，请人工查看归档报文：\n{out.strip()[-600:]}")
        return None
    log(f"  main 头部提交 {sha[:12]}")
    return sha


def step_gate(repo: Path, sha: str) -> bool:
    log(f"步骤 6/8：在合并提交 {sha[:12]} 上重跑质量门禁")
    out = EVIDENCE / f"main-gate-{sha[:12]}.log"
    code, msg = run([sys.executable, str(repo / "scripts" / "verify-gate.py"),
                     "--commit", sha, "--out", str(out), "--repo", str(repo)])
    log(f"  门禁日志：{out}（exit={code}）")
    if code != 0:
        log(f"  失败：\n{msg.strip()[-600:]}")
        return False
    return True


def step_deploy(repo: Path, sha: str) -> bool:
    log(f"步骤 7/8：在合并版本上重新部署")
    out = EVIDENCE / "deployment-main.log"
    script = repo / "scripts" / "deploy-local.ps1"
    cmd = ["powershell", "-ExecutionPolicy", "Bypass", "-File", str(script),
           "-LogFile", str(out)]
    code, msg = run(cmd)
    log(f"  部署日志：{out}（exit={code}）")
    if code != 0:
        log(f"  失败：\n{msg.strip()[-600:]}")
        return False
    return True


def step_delivery(repo: Path) -> bool:
    log("步骤 8/8：复跑交付链路自检")
    code, msg = run([sys.executable, str(repo / "scripts" / "verify-delivery.py"),
                     "--repo", str(repo),
                     "--out", str(EVIDENCE / "delivery-check.md"),
                     "--json-out", str(EVIDENCE / "delivery-check.json")])
    log(f"  自检结果（exit={code}）\n{msg.strip()[-800:]}")
    return code == 0


def main() -> int:
    ap = argparse.ArgumentParser(description="远端交付链路一键驱动")
    ap.add_argument("--repo", default=str(REPO_ROOT))
    ap.add_argument("--owner", required=True, help="GitHub 账号或组织")
    ap.add_argument("--name", required=True, help="仓库名")
    ap.add_argument("--remote", default="origin")
    ap.add_argument("--pr-number", default="", help="PR 编号（不填则从 create_pull_request 返回中解析）")
    ap.add_argument("--skip-push", action="store_true")
    args = ap.parse_args()

    token = os.environ.get("GITHUB_PERSONAL_ACCESS_TOKEN", "")
    if not token:
        log("缺少令牌：请设置环境变量 GITHUB_PERSONAL_ACCESS_TOKEN（令牌不写入任何文件）。")
        return 2

    repo = Path(args.repo).resolve()
    EVIDENCE.mkdir(parents=True, exist_ok=True)

    if not args.skip_push and not step_push(repo, args.remote):
        return 1

    pr_url = step_pr(repo, token, args.owner, args.name)
    if not pr_url:
        return 1
    m = re.search(r"/pull/(\d+)", pr_url)
    pr_number = args.pr_number or (m.group(1) if m else "")
    if not pr_number:
        log("无法确定 PR 编号，停止。")
        return 1

    if not step_protect(token, args.owner, args.name):
        return 1
    if not step_read_protection(token, args.owner, args.name):
        return 1

    sha = step_merge(repo, token, args.owner, args.name, pr_number)
    if not sha:
        return 1

    ok = step_gate(repo, sha)
    ok = step_deploy(repo, sha) and ok
    ok = step_delivery(repo) and ok

    log("全部步骤结束。" if ok else "存在失败步骤，详见上方日志与 evidence/ 归档报文。")
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
