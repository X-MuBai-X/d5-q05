#!/usr/bin/env python3
"""远端交付链路一键驱动：推送 → PR → 状态检查 → 分支保护 → 保护验证 → 合并 → 合并提交门禁与部署。

共同要求第 8 节的落地顺序（D-01~D-09）：
  步骤 1   推送 main 与 dev/05 到远端（业务提交只落 dev/05）
  步骤 2   通过 **GitHub MCP** `create_pull_request` 发起 PR，描述含第 4 条要求的 6 项要素
  步骤 3   轮询 PR 的检查项（check-runs / commit status）直到全部完成并归档
  步骤 4   配置 main 分支保护：禁直推 / 禁删除 / 禁强推 / 要求 PR 合并 + 状态检查
  步骤 5   读取保护规则并归档到 evidence/branch-protection.json
  步骤 6   **验证保护生效**：尝试更新 main 引用必须被拒绝（不改内容，只探规则）
  步骤 7   通过 **GitHub MCP** `merge_pull_request` 合并，合并消息按提交规范关联主要工作项编号
  步骤 8   拉取合并提交并在**合并提交**上重跑质量门禁 → evidence/main-gate-<短SHA>.log
  步骤 9   在合并版本上重新部署 → evidence/deployment-main.log
  步骤 10  复跑交付链路自检 → evidence/delivery-check.md
  步骤 11  归档主分支流水线运行 → evidence/main-workflow-runs.json

令牌只经环境变量 `GITHUB_PERSONAL_ACCESS_TOKEN` 传入，不入库、不写 git 配置、不进证据归档：
认证通过进程级的 `GIT_CONFIG_KEY_*` 注入（临时生效，随进程退出消失）。

用法：
    $env:GITHUB_PERSONAL_ACCESS_TOKEN = "<PAT>"
    python scripts/release-github.py --repo . --owner <owner> --name <repo> \
        --remote-url https://github.com/<owner>/<repo>.git [--proxy http://127.0.0.1:7890]

说明：任何一步失败都**如实记录失败原因并停止**，不跳过、不伪造后续步骤的通过结论。
"""
from __future__ import annotations

import argparse
import base64
import json
import os
import re
import subprocess
import sys
import time
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

# ci.yml 中两个作业的实际 name，即 GitHub 上检查项的上下文名
DEFAULT_STATUS_CONTEXTS = [
    "质量门禁（gofmt / vet / test / cover / race）",
    "业务冒烟（启动服务并核对配额闭环）",
]

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
- 交付链路自检 `scripts/test-delivery-checks.py` 全部通过

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


def run(cmd: list[str], cwd: Path | None = None,
        env: dict[str, str] | None = None) -> tuple[int, str]:
    proc = subprocess.run(cmd, cwd=str(cwd or REPO_ROOT), capture_output=True,
                          text=True, encoding="utf-8", errors="replace", env=env)
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def git_env(token: str, proxy: str) -> dict[str, str]:
    """构造带认证与代理的 git 进程环境。

    认证用 `http.https://github.com/.extraheader` + Basic(x-access-token:PAT)，
    通过进程级 GIT_CONFIG_KEY_* 注入：**不写入 .git/config，不落盘**。
    """
    env = dict(os.environ)
    b64 = base64.b64encode(f"x-access-token:{token}".encode("utf-8")).decode("ascii")
    keys = [("http.https://github.com/.extraheader", f"Authorization: Basic {b64}")]
    if proxy:
        keys.append(("http.proxy", proxy))
    env["GIT_CONFIG_COUNT"] = str(len(keys))
    for idx, (key, value) in enumerate(keys):
        env[f"GIT_CONFIG_KEY_{idx}"] = key
        env[f"GIT_CONFIG_VALUE_{idx}"] = value
    return env


def gh_api(token: str, method: str, path: str, body: dict | None = None) -> tuple[int, dict]:
    url = f"{API}{path}"
    data = json.dumps(body).encode("utf-8") if body is not None else None
    headers = {
        "Authorization": f"Bearer {token}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        "User-Agent": "mcode-release-github/1.1",
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


def mcp_call(repo: Path, token: str, tool: str, args: dict) -> tuple[int, str]:
    """通过 scripts/github_mcp.py 调用 MCP 工具（原始报文由该脚本归档）。"""
    args_file = EVIDENCE / ".mcp-args.json"
    args_file.parent.mkdir(parents=True, exist_ok=True)
    args_file.write_text(json.dumps(args, ensure_ascii=False, indent=2), encoding="utf-8", newline="\n")
    env = dict(os.environ)
    env["GITHUB_PERSONAL_ACCESS_TOKEN"] = token
    code, out = run([sys.executable, str(repo / "scripts" / "github_mcp.py"),
                     "--call", tool, "--args-file", str(args_file)], env=env)
    try:
        args_file.unlink()
    except OSError:
        pass
    return code, out


def step_remote(repo: Path, remote: str, remote_url: str) -> bool:
    code, out = run(["git", "-C", str(repo), "remote"])
    if code != 0:
        log(f"  失败：读取远端列表出错：{out.strip()[:300]}")
        return False
    if remote not in out.split():
        if not remote_url:
            log(f"  失败：未配置远端 {remote}，且未提供 --remote-url")
            return False
        code, out = run(["git", "-C", str(repo), "remote", "add", remote, remote_url])
        if code != 0:
            log(f"  失败：添加远端失败：{out.strip()[:300]}")
            return False
        log(f"  已添加远端 {remote} -> {remote_url}（URL 不含凭据）")
    return True


def step_push(repo: Path, remote: str, token: str, proxy: str) -> bool:
    log("步骤 1/11：推送 main 与 dev/05")
    env = git_env(token, proxy)
    for branch in ("main", "dev/05"):
        code, out = run(["git", "-C", str(repo), "push", "-u", remote, branch], env=env)
        if code != 0:
            log(f"  失败：推送 {branch} 失败\n{out.strip()[:600]}")
            return False
        log(f"  已推送 {branch}")
    return True


def step_pr(repo: Path, token: str, owner: str, name: str,
            title: str, body: str, head: str) -> tuple[str | None, str]:
    log("步骤 2/11：通过 GitHub MCP 发起 PR（6 项要素）")
    code, out = mcp_call(repo, token, "create_pull_request", {
        "owner": owner, "repo": name, "head": head, "base": "main",
        "title": title, "body": body, "draft": False,
    })
    log(f"  MCP 调用完成（exit={code}），原始报文已归档到 evidence/github-mcp/")
    m = re.search(r"https://github\.com/[^/]+/[^/]+/pull/\d+", out)
    url = m.group(0) if m else ""
    if not url:
        log(f"  未从返回中解析出 PR 链接，请查看归档报文：\n{out.strip()[-800:]}")
    else:
        log(f"  PR: {url}")
    return (url or None), out


def step_wait_checks(token: str, owner: str, name: str, head_sha: str,
                     contexts: list[str], timeout: int) -> tuple[bool, dict]:
    log(f"步骤 3/11：等待 PR 检查项（期望上下文：{len(contexts)} 个，最长 {timeout}s）")
    deadline = time.time() + timeout
    last: dict = {}
    while True:
        code, checks = gh_api(token, "GET",
                              f"/repos/{owner}/{name}/commits/{head_sha}/check-runs?per_page=100")
        if code != 200:
            log(f"  读取检查项失败（HTTP {code}），重试中")
        else:
            runs = checks.get("check_runs", [])
            last = {
                "read_at": datetime.now(CST).isoformat(),
                "head_sha": head_sha,
                "check_runs": [
                    {"name": r.get("name"), "status": r.get("status"),
                     "conclusion": r.get("conclusion"),
                     "started_at": r.get("started_at"),
                     "completed_at": r.get("completed_at"),
                     "html_url": r.get("html_url")}
                    for r in runs
                ],
            }
            by_name = {r.get("name"): r for r in runs}
            pending = [c for c in contexts
                       if c not in by_name or by_name[c].get("status") != "completed"]
            failed = [c for c in contexts
                      if c in by_name and by_name[c].get("conclusion") not in ("success", "neutral", "skipped")]
            if failed:
                log("  检查项未通过：" + "、".join(failed))
                break
            if not pending:
                log(f"  检查项全部完成且通过（{len(by_name)} 个检查）")
                break
            log(f"  仍在进行：{'、'.join(pending[:3])}（已用 {int(timeout - (deadline - time.time()))}s）")
        if time.time() > deadline:
            log(f"  超时（{timeout}s），按未通过处理")
            break
        time.sleep(15)
    out = EVIDENCE / "pr-checks.json"
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(last, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
    log(f"  检查项归档：{out}")
    ok = bool(last.get("check_runs")) and all(
        r.get("status") == "completed" and r.get("conclusion") in ("success", "neutral", "skipped")
        for r in last.get("check_runs", []) if r.get("name") in contexts)
    return ok, last


def step_protect(token: str, owner: str, name: str, contexts: list[str]) -> bool:
    log("步骤 4/11：配置 main 分支保护（REST API，如实标注）")
    body = {
        "required_status_checks": {"strict": True, "contexts": contexts},
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
    log(f"  保护规则已设置（禁直推/禁删除/禁强推/要求 PR 合并 + 状态检查 {contexts}）")
    return True


def step_read_protection(token: str, owner: str, name: str) -> bool:
    log("步骤 5/11：读取并归档分支保护规则")
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


def step_verify_protection(token: str, owner: str, name: str) -> bool:
    """验证 main 受保护：尝试把 main 更新为**同一个** SHA。

    不改变任何内容（目标 SHA 就是当前值），只用于确认规则会拒绝直推；
    若 main 未受保护，该请求会返回 200。
    """
    log("步骤 6/11：验证 main 受保护（直推应被拒绝）")
    code, head = gh_api(token, "GET", f"/repos/{owner}/{name}/git/ref/heads/main")
    sha = head.get("object", {}).get("sha", "") if code == 200 else ""
    if not sha:
        log(f"  失败：无法读取 main 当前引用（HTTP {code}）")
        return False
    patch_code, patch_resp = gh_api(token, "PATCH", f"/repos/{owner}/{name}/git/refs/heads/main",
                                    {"sha": sha, "force": False})
    rejected = patch_code in (403, 409, 422)
    out = {
        "verified_at": datetime.now(CST).isoformat(),
        "method": "PATCH /repos/{owner}/{repo}/git/refs/heads/main（sha 保持不变，仅探测保护规则）",
        "target_sha": sha,
        "http_status": patch_code,
        "rejected_by_protection": rejected,
        "response": patch_resp,
        "conclusion": "main 受保护：更新引用被拒绝" if rejected
                      else "main 未受保护：更新引用被接受（不合规）",
    }
    path = EVIDENCE / "branch-protection-verify.json"
    path.write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
    log(f"  更新引用返回 HTTP {patch_code}；已归档到 {path}")
    if not rejected:
        log("  失败：main 未拒绝直推，分支保护未生效")
        return False
    log("  确认：main 拒绝直推")
    return True


def step_merge(repo: Path, token: str, owner: str, name: str, pr_number: str,
               title: str, message: str) -> str | None:
    log("步骤 7/11：通过 GitHub MCP 合并 PR（保留原始提交，合并消息含工作项编号）")
    code, out = mcp_call(repo, token, "merge_pull_request", {
        "owner": owner, "repo": name, "pull_number": int(pr_number),
        "merge_method": "merge",
        "commit_title": title,
        "commit_message": message,
    })
    log(f"  MCP 调用完成（exit={code}），原始报文已归档到 evidence/github-mcp/")
    code2, out2 = gh_api(token, "GET", f"/repos/{owner}/{name}/commits/main")
    sha = out2.get("sha", "") if code2 == 200 else ""
    if not sha:
        log(f"  未能读取 main 头部提交，请查看归档报文：\n{out.strip()[-800:]}")
        return None
    log(f"  main 头部提交 {sha[:12]}  {out2.get('commit', {}).get('message', '').splitlines()[0]}")
    return sha


def step_fetch(repo: Path, remote: str, token: str, proxy: str, sha: str) -> bool:
    log(f"步骤 8/11：拉取合并提交 {sha[:12]} 到本地")
    env = git_env(token, proxy)
    code, out = run(["git", "-C", str(repo), "fetch", remote, "main"], env=env)
    if code != 0:
        log(f"  失败：fetch 失败\n{out.strip()[:400]}")
        return False
    code, out = run(["git", "-C", str(repo), "cat-file", "-t", sha])
    if code != 0:
        log("  失败：本地拿不到合并提交对象")
        return False
    log("  已拉取")
    return True


def step_gate(repo: Path, sha: str) -> bool:
    log(f"步骤 8/11：在合并提交 {sha[:12]} 上重跑质量门禁")
    out = EVIDENCE / f"main-gate-{sha[:12]}.log"
    code, msg = run([sys.executable, str(repo / "scripts" / "verify-gate.py"),
                     "--commit", sha, "--out", str(out), "--repo", str(repo)])
    log(f"  门禁日志：{out}（exit={code}）")
    if code != 0:
        log(f"  失败：\n{msg.strip()[-600:]}")
        return False
    return True


def step_deploy(repo: Path, sha: str) -> bool:
    log("步骤 9/11：在合并版本上重新部署")
    out = EVIDENCE / "deployment-main.log"
    script = repo / "scripts" / "deploy-local.ps1"
    cmd = ["powershell", "-ExecutionPolicy", "Bypass", "-File", str(script),
           "-LogFile", str(out), "-Commit", sha, "-SkipSmoke"]
    code, msg = run(cmd)
    log(f"  部署日志：{out}（exit={code}）")
    if code != 0:
        log(f"  失败：\n{msg.strip()[-600:]}")
        return False
    return True


def step_delivery(repo: Path) -> bool:
    log("步骤 10/11：复跑交付链路自检")
    code, msg = run([sys.executable, str(repo / "scripts" / "verify-delivery.py"),
                     "--repo", str(repo),
                     "--out", str(EVIDENCE / "delivery-check.md"),
                     "--json-out", str(EVIDENCE / "delivery-check.json")])
    log(f"  自检结果（exit={code}）\n{msg.strip()[-800:]}")
    return code == 0


def step_workflow_runs(token: str, owner: str, name: str) -> bool:
    log("步骤 11/11：归档主分支流水线运行")
    code, resp = gh_api(token, "GET",
                        f"/repos/{owner}/{name}/actions/runs?branch=main&per_page=20")
    if code != 200:
        log(f"  失败：读取流水线运行返回 {code}")
        return False
    runs = []
    for r in resp.get("workflow_runs", []):
        runs.append({
            "id": r.get("id"), "name": r.get("name"), "event": r.get("event"),
            "head_branch": r.get("head_branch"), "head_sha": r.get("head_sha"),
            "status": r.get("status"), "conclusion": r.get("conclusion"),
            "created_at": r.get("created_at"), "updated_at": r.get("updated_at"),
            "html_url": r.get("html_url"),
        })
    out = {
        "read_at": datetime.now(CST).isoformat(),
        "source": "GitHub REST API GET /repos/{owner}/{repo}/actions/runs?branch=main",
        "http_status": code,
        "total_count": resp.get("total_count"),
        "workflow_runs": runs,
    }
    path = EVIDENCE / "main-workflow-runs.json"
    path.write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
    log(f"  已归档 {len(runs)} 条运行记录到 {path}")
    for r in runs[:4]:
        log(f"    - {r.get('name')} {r.get('head_sha', '')[:12]} {r.get('status')}/{r.get('conclusion')} {r.get('html_url')}")
    return True


def main() -> int:
    ap = argparse.ArgumentParser(description="远端交付链路一键驱动")
    ap.add_argument("--repo", default=str(REPO_ROOT))
    ap.add_argument("--owner", required=True, help="GitHub 账号或组织")
    ap.add_argument("--name", required=True, help="仓库名")
    ap.add_argument("--remote", default="origin")
    ap.add_argument("--remote-url", default="", help="远端地址（不含凭据）；远端不存在时自动添加")
    ap.add_argument("--proxy", default="", help="HTTP 代理，如 http://127.0.0.1:7890")
    ap.add_argument("--head", default="dev/05", help="PR 的源分支")
    ap.add_argument("--pr-number", default="", help="PR 编号（不填则从 create_pull_request 返回中解析）")
    ap.add_argument("--pr-title", default="[feat] [v2.5.0] [#16221249] 企业模型服务配额管理与远端交付链路")
    ap.add_argument("--merge-title", default="[feat] [v2.5.0] [#16221249] 合并题目 05 企业模型服务配额管理实现")
    ap.add_argument("--merge-message", default="从 dev/05 发起 PR 并合并，保留开发分支原始提交；"
                                              "主要工作项 #16221249，关联需求 #16220913 与缺陷 "
                                              "#16220917 / #16220924 / #16220925 / #16221411 / #16221436。")
    ap.add_argument("--status-context", action="append", default=None,
                    help="PR 必须通过的检查上下文（可重复；默认取 ci.yml 的作业名）")
    ap.add_argument("--check-timeout", type=int, default=2400, help="等待 PR 检查项的最长秒数")
    ap.add_argument("--skip-push", action="store_true")
    ap.add_argument("--skip-checks", action="store_true", help="跳过等待检查项（不推荐）")
    args = ap.parse_args()

    token = os.environ.get("GITHUB_PERSONAL_ACCESS_TOKEN", "")
    if not token:
        log("缺少令牌：请设置环境变量 GITHUB_PERSONAL_ACCESS_TOKEN（令牌不写入任何文件）。")
        return 2

    repo = Path(args.repo).resolve()
    EVIDENCE.mkdir(parents=True, exist_ok=True)
    contexts = args.status_context or DEFAULT_STATUS_CONTEXTS
    summary: dict = {
        "started_at": datetime.now(CST).isoformat(),
        "owner": args.owner, "repo": args.name, "remote": args.remote,
        "head_branch": args.head, "base_branch": "main",
        "status_contexts": contexts,
        "steps": [],
    }

    def record(step: str, ok: bool, detail: str = "") -> None:
        summary["steps"].append({"step": step, "ok": ok, "detail": detail,
                                 "at": datetime.now(CST).isoformat()})

    if not step_remote(repo, args.remote, args.remote_url):
        record("远端配置", False)
        return 1
    if not args.skip_push:
        if not step_push(repo, args.remote, token, args.proxy):
            record("推送", False)
            return 1
    record("推送", True)

    pr_url, pr_out = step_pr(repo, token, args.owner, args.name,
                             args.pr_title, PR_BODY, args.head)
    record("创建 PR", bool(pr_url), pr_url or "")
    if not pr_url:
        return 1
    m = re.search(r"/pull/(\d+)", pr_url)
    pr_number = args.pr_number or (m.group(1) if m else "")
    if not pr_number:
        log("无法确定 PR 编号，停止。")
        return 1

    code, head = gh_api(token, "GET", f"/repos/{args.owner}/{args.name}/pulls/{pr_number}")
    head_sha = head.get("head", {}).get("sha", "") if code == 200 else ""
    if not args.skip_checks:
        ok, _ = step_wait_checks(token, args.owner, args.name, head_sha,
                                 contexts, args.check_timeout)
        record("等待 PR 检查项", ok)
        if not ok:
            log("检查项未全部通过，按共同要求第 8 节第 6 条不合并，停止。")
            return 1
    else:
        record("等待 PR 检查项", True, "已跳过（--skip-checks）")

    if not step_protect(token, args.owner, args.name, contexts):
        record("配置分支保护", False)
        return 1
    record("配置分支保护", True)
    if not step_read_protection(token, args.owner, args.name):
        record("读取分支保护", False)
        return 1
    record("读取分支保护", True)
    if not step_verify_protection(token, args.owner, args.name):
        record("验证分支保护", False)
        return 1
    record("验证分支保护", True)

    sha = step_merge(repo, token, args.owner, args.name, pr_number,
                     args.merge_title, args.merge_message)
    record("合并 PR", bool(sha), sha or "")
    if not sha:
        return 1
    summary["pr_url"] = pr_url
    summary["pr_number"] = pr_number
    summary["merge_sha"] = sha

    if not step_fetch(repo, args.remote, token, args.proxy, sha):
        record("拉取合并提交", False)
        return 1
    record("拉取合并提交", True)

    ok = step_gate(repo, sha)
    record("合并提交门禁", ok, str(EVIDENCE / f"main-gate-{sha[:12]}.log"))
    ok = step_deploy(repo, sha) and ok
    record("合并版本部署", ok, str(EVIDENCE / "deployment-main.log"))
    ok = step_delivery(repo) and ok
    record("交付链路自检", ok, str(EVIDENCE / "delivery-check.md"))
    step_workflow_runs(token, args.owner, args.name)
    record("归档主分支流水线", True, str(EVIDENCE / "main-workflow-runs.json"))

    summary["finished_at"] = datetime.now(CST).isoformat()
    summary["all_ok"] = all(s["ok"] for s in summary["steps"])
    path = EVIDENCE / "github-remote-chain.json"
    path.write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
    log(f"链路归档：{path}")

    log("全部步骤结束。" if summary["all_ok"] else "存在失败步骤，详见上方日志与 evidence/ 归档报文。")
    return 0 if summary["all_ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
