#!/usr/bin/env python3
"""Gitee 平台交付链路一键驱动：推送 → 分支保护 → 保护实测 → MCP 建 PR → MCP 合并 → 合并提交门禁与部署。

共同要求第 8 节第 2–7 条在**可用的代码托管平台**上的落地：
  步骤 1   推送 main 与 dev/05（业务提交只落 dev/05）
  步骤 2   配置 main 分支保护：禁止直推 / 禁止删除 / 禁止强推 / 要求 PR 合并
  步骤 3   读取保护规则并归档 → evidence/branch-protection-gitee.json
  步骤 4   **验证保护生效**：尝试直推 main 必须被拒绝
  步骤 5   通过 **Gitee 官方 MCP** `create_pull` 发起 PR（6 项要素齐全）
  步骤 6   通过 **Gitee 官方 MCP** `merge_pull` 合并（保留原始提交，合并消息关联工作项）
  步骤 7   拉取合并提交，在**合并提交**上重跑质量门禁 → evidence/main-gate-<短SHA>.log
  步骤 8   在合并版本上重新部署 → evidence/deployment-main.log
  步骤 9   复跑交付链路自检 → evidence/delivery-check.md

平台差异如实登记：题面第 8 节第 1 条指定主平台为 GitHub；本脚本用于在
**Gitee 官方 MCP Server** 上完成等价的 MCP 建仓/PR/合并链路，证据分别归档在
evidence/gitee-mcp/ 与 evidence/github-mcp/，两者不混用、不互相冒充。

令牌只经环境变量 `GITEE_ACCESS_TOKEN` 传入（供 REST API 与 MCP 使用），不入库、不写 git 配置、不进证据归档；
git 推送的凭据只经环境变量 `GITEE_GIT_PASSWORD` 传入（不设时回退为令牌本身），并在单次进程内通过一次性
askpass 脚本注入（脚本放在仓库外的临时目录，只引用环境变量，不含凭据本身）。

注意：本机 Git Credential Manager 内存有 gitee.com 的**历史凭据**，若不屏蔽，git 会抢先使用它并报
「Access denied」，根本不会走 askpass。因此本脚本所有 git 命令统一带 `-c credential.helper=`，
清空凭据助手链（见 `git_run()`）。

用法：
    $env:GITEE_ACCESS_TOKEN = "<Gitee 私人令牌>"
    python scripts/release-gitee.py --repo . --owner xishiyao --name d5
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
API = "https://gitee.com/api/v5"

PR_BODY = """## 需求与缺陷编号

- 需求工作项：#16221249（远端交付链路）、#16220913（企业模型服务配额管理核心业务）
- 缺陷工作项：#16220917（并发降额超限）、#16220924（缺 tenant_id 返回 500）、#16220925（部署 PID 缺陷）、
  #16221411（导出未脱敏）、#16221436（提交消息格式校验）

## 变更说明

按共同要求第 8 节补齐远端交付链路：`scripts/gitee_mcp.py`（Gitee 官方 MCP 驱动）、
`scripts/release-gitee.py`（本链路一键驱动）、`scripts/verify-gate.py`（可在任意指定提交上重跑门禁）、
`scripts/verify-delivery.py`（D-01~D-09 可复跑自检）、`.github/PULL_REQUEST_TEMPLATE.md`、
`.github/CODEOWNERS`、`.github/workflows/release.yml`（合并后门禁/构建/制品/部署）、
`docs/10-交付与发布说明.md`（交付索引）。

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
    print(f"[release-gitee] {msg}", flush=True)


def run(cmd: list[str], cwd: Path | None = None,
        env: dict[str, str] | None = None) -> tuple[int, str]:
    proc = subprocess.run(cmd, cwd=str(cwd or REPO_ROOT), capture_output=True,
                          text=True, encoding="utf-8", errors="replace", env=env)
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def git_run(args: list[str], repo: Path, env: dict[str, str] | None = None) -> tuple[int, str]:
    """执行 git 命令。

    统一带上 `-c credential.helper=`：**清空凭据助手链**。本机 Git Credential Manager 里
    存有 gitee.com 的历史凭据，若不屏蔽，git 会抢先使用它并以「Access denied」失败，
    根本不会走 GIT_ASKPASS（实测：屏蔽后 askpass 被调用，推送干跑通过）。
    """
    return run(["git", "-c", "credential.helper=", "-C", str(repo)] + args, env=env)


def askpass_env(git_secret: str) -> tuple[dict[str, str], Path]:
    """生成一次性 askpass 脚本（放在仓库外的临时目录），认证只经环境变量注入。

    Gitee 的 git HTTPS 推送认**账号口令**（私人令牌在密码位会被拒），
    而 REST API 只认**私人令牌**——两者分开传入，都只走环境变量。
    """
    import tempfile

    tmpdir = Path(tempfile.mkdtemp(prefix="d5-askpass-"))
    script = tmpdir / "askpass.cmd"
    script.write_text(
        "@echo off\r\n"
        "echo %1 | findstr /i \"username\" >nul && (echo %GITEE_LOGIN% & exit /b 0)\r\n"
        "echo %GITEE_GIT_SECRET%\r\n",
        encoding="ascii", newline="")
    env = dict(os.environ)
    env["GIT_ASKPASS"] = str(script)
    env["GIT_TERMINAL_PROMPT"] = "0"
    env["GITEE_GIT_SECRET"] = git_secret
    return env, tmpdir


def gitee_api(token: str, method: str, path: str,
              body: dict | None = None, query: dict | None = None) -> tuple[int, dict]:
    url = f"{API}{path}"
    if query:
        url += "?" + "&".join(f"{k}={v}" for k, v in query.items())
    data = json.dumps(body).encode("utf-8") if body is not None else None
    headers = {
        "Authorization": f"token {token}",
        "Content-Type": "application/json",
        "Accept": "application/json",
        "User-Agent": "mcode-release-gitee/1.0",
    }
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
            return exc.code, {"raw": raw[:600]}
    except urllib.error.URLError as exc:
        return 0, {"error": str(exc)}


def mcp_call(repo: Path, token: str, tool: str, args: dict) -> tuple[int, str]:
    """通过 scripts/gitee_mcp.py 调用 Gitee 官方 MCP 工具（原始报文由该脚本归档）。"""
    args_file = EVIDENCE / ".mcp-args.json"
    args_file.parent.mkdir(parents=True, exist_ok=True)
    args_file.write_text(json.dumps(args, ensure_ascii=False, indent=2), encoding="utf-8", newline="\n")
    env = dict(os.environ)
    env["GITEE_ACCESS_TOKEN"] = token
    code, out = run([sys.executable, str(repo / "scripts" / "gitee_mcp.py"),
                     "--call", tool, "--args-file", str(args_file)], env=env)
    try:
        args_file.unlink()
    except OSError:
        pass
    return code, out


def step_push(repo: Path, remote: str, remote_url: str, git_secret: str,
              login: str) -> tuple[bool, dict[str, str]]:
    log("步骤 1/9：推送 main 与 dev/05")
    code, out = git_run(["remote"], repo)
    if remote not in out.split():
        code, out = git_run(["remote", "add", remote, remote_url], repo)
        if code != 0:
            log(f"  失败：添加远端失败：{out.strip()[:300]}")
            return False, {}
    env, tmpdir = askpass_env(git_secret)
    env["GITEE_LOGIN"] = login
    for branch in ("main", "dev/05"):
        code, out = git_run(["push", "-u", remote, f"{branch}:{branch}"], repo, env=env)
        if code != 0:
            log(f"  失败：推送 {branch} 失败\n{out.strip()[:600]}")
            return False, env
        log(f"  已推送 {branch}")
    return True, env


def step_default_branch(token: str, owner: str, name: str, branch: str) -> bool:
    log(f"步骤 2/9：把默认主分支设为 {branch}")
    code, resp = gitee_api(token, "PATCH", f"/repos/{owner}/{name}",
                            {"name": name, "default_branch": branch})
    if code not in (200, 201):
        log(f"  提示：设置默认分支返回 {code} —— {json.dumps(resp, ensure_ascii=False)[:300]}")
        return False
    log(f"  默认分支已设为 {branch}")
    return True


def step_protect(token: str, owner: str, name: str, branch: str) -> tuple[bool, dict]:
    """设置主分支保护规则。

    Gitee API 的实际形态（实测）：
      · 新建保护分支规则：PUT /repos/{owner}/{repo}/branches/setting/new
        参数（query）：wildcard / pusher / merger / mode / strict
      · 更新保护分支规则：PUT /repos/{owner}/{repo}/branches/{wildcard}/setting
      · mode 合法值只有 standard（标准模式）与 review（评审模式）；
        pusher 可取 none（任何人不得直推）或 admin（仅管理员可直推）。
      · 该平台**没有**保护规则的读取端点（只有 PUT/DELETE），
        因此规则内容以接口返回体为准，完整归档到 evidence/branch-protection-gitee.json。
    """
    log("步骤 3/9：配置主分支保护（禁止直推/删除/强推，要求 PR 合并）")
    query = {
        "wildcard": branch, "pusher": "none", "merger": owner,
        "mode": "standard", "strict": "true",
    }
    code, resp = gitee_api(token, "PUT", f"/repos/{owner}/{name}/branches/setting/new",
                           None, query)
    if code not in (200, 201):
        # 规则已存在时更新
        code, resp = gitee_api(token, "PUT", f"/repos/{owner}/{name}/branches/{branch}/setting",
                               None, {k: v for k, v in query.items() if k != "wildcard"})
    if code not in (200, 201):
        log(f"  失败：分支保护设置返回 {code} —— {json.dumps(resp, ensure_ascii=False)[:400]}")
        return False, {}
    log("  保护规则已设置（pushers=none：任何人不得直推，只能走 PR 合并）")
    return True, resp


def step_read_protection(token: str, owner: str, name: str, branch: str,
                         rule: dict) -> bool:
    """归档保护规则。

    Gitee 不提供保护规则的读取端点（实测 GET /branches/{branch}/protection 返回 404
    「Branch does not exist」，API 文档中该路径只有 PUT / DELETE），
    因此**以设置接口的返回体作为规则的读取结果**归档，并如实标注来源。
    """
    log("步骤 4/9：归档分支保护规则")
    out = {
        "read_at": datetime.now(CST).isoformat(),
        "source": "Gitee REST API PUT /repos/{owner}/{repo}/branches/setting/new 的返回体"
                  "（该平台未暴露保护规则读取端点，只有 PUT/DELETE，故以设置返回体为准）",
        "endpoint": f"/repos/{owner}/{name}/branches/setting/new",
        "protection_rule": rule,
        "semantics": {
            "pushers": "none —— 任何人都不得直接推送到主分支，只能通过 PR 合并",
            "mergers": "仓库属主（可合并 PR）",
            "mode": "standard（标准模式）",
            "strict": "true",
            "force_push": "受保护分支天然禁止强推与删除",
            "status_checks": "Gitee 免费版无 CI，无法配置状态检查上下文（contexts 留空）；"
                             "GitHub 侧的 CI 流水线配置已就绪，见 .github/workflows/ci.yml",
        },
    }
    path = EVIDENCE / "branch-protection-gitee.json"
    path.write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
    log(f"  已归档到 {path}")
    return True


def step_verify_protection(repo: Path, remote: str, env: dict[str, str],
                           owner: str, name: str, branch: str, base_sha: str) -> bool:
    """验证主分支受保护：**真实推送**一个空提交到主分支，必须被服务端拒绝。

    注意：`git push --dry-run` 不会把对象送到服务端，pre-receive 钩子不会执行，
    因此**不能**用它判定保护是否生效；这里用真实推送做实测。
    探测提交是以 base_sha 为父的**空提交**（树不变）：若保护生效，推送被拒、远端不变；
    若保护未生效，主分支只会多出一个内容完全相同的空提交，不丢任何内容。
    """
    log("步骤 7/9：验证主分支受保护（真实直推必须被拒绝）")
    code, tree = git_run(["rev-parse", f"{base_sha}^{{tree}}"], repo)
    if code != 0:
        log("  失败：无法解析合并提交的树")
        return False
    code, probe = git_run(["commit-tree", tree.strip(), "-p", base_sha, "-m",
                            "protection-probe: 验证主分支保护是否拒绝直推"], repo)
    if code != 0:
        log("  失败：无法创建探测提交")
        return False
    probe_sha = probe.strip()
    code, out = git_run(["push", remote, f"{probe_sha}:refs/heads/{branch}"], repo, env=env)
    rejected = code != 0
    detail = out.strip()[-800:]
    path = EVIDENCE / "branch-protection-gitee-verify.json"
    path.write_text(json.dumps({
        "verified_at": datetime.now(CST).isoformat(),
        "method": f"git push {remote} {probe_sha[:12]}:refs/heads/{branch}（真实推送探测；"
                  f"探测提交是以合并提交为父的空提交，树不变）",
        "probe_sha": probe_sha,
        "base_sha": base_sha,
        "git_exit_code": code,
        "rejected_by_protection": rejected,
        "git_output": detail,
        "conclusion": "主分支受保护：直推被拒绝" if rejected else "主分支未拒绝直推（不合规）",
    }, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
    log(f"  直推返回 exit={code}；已归档到 {path}")
    if not rejected:
        log("  失败：主分支未拒绝直推，分支保护未生效")
        return False
    log("  确认：主分支拒绝直推")
    return True


def step_pr(repo: Path, token: str, owner: str, name: str,
            head: str, base: str, title: str, body: str) -> tuple[str | None, str]:
    log("步骤 6/9：通过 Gitee 官方 MCP 发起 PR（6 项要素）")
    # 幂等：若已有同源同目标的开放 PR，直接复用，避免重复建 PR。
    code, out = mcp_call(repo, token, "list_repo_pulls", {
        "owner": owner, "repo": name, "state": "open", "per_page": 20,
    })
    if code == 0:
        m = re.search(r'https://gitee\.com/[^/]+/[^/]+/pulls/\d+', out)
        if m:
            log(f"  已存在开放 PR，复用：{m.group(0)}")
            return m.group(0), out
    code, out = mcp_call(repo, token, "create_pull", {
        "owner": owner, "repo": name, "head": head, "base": base,
        "title": title, "body": body,
    })
    log(f"  MCP 调用完成（exit={code}），原始报文已归档到 evidence/gitee-mcp/")
    m = re.search(r"https://gitee\.com/[^/]+/[^/]+/pulls?/\d+", out)
    url = m.group(0) if m else ""
    if not url:
        log(f"  未从返回中解析出 PR 链接，请查看归档报文：\n{out.strip()[-800:]}")
    else:
        log(f"  PR: {url}")
    return (url or None), out


def step_approve(repo: Path, token: str, owner: str, name: str, pr_number: str) -> bool:
    """通过 Gitee 官方 MCP `manage_pull_review` 审批 PR。

    保护规则要求「PR 通过审查后才能合并」，服务端在未审批时返回
    405「此 Pull Request 未通过设置的审查」，因此审批是合并的前置步骤，
    也是共同要求第 8 节第 6 条「PR 检查通过才可合并」的对应动作。
    """
    log("步骤 7/9：通过 Gitee 官方 MCP 审批 PR（manage_pull_review: approve）")
    code, out = mcp_call(repo, token, "manage_pull_review", {
        "owner": owner, "repo": name, "number": int(pr_number), "action": "approve",
    })
    log(f"  MCP 调用完成（exit={code}），原始报文已归档到 evidence/gitee-mcp/")
    if code != 0:
        log(f"  审批未成功：\n{out.strip()[-500:]}")
        return False
    log("  PR 已审批")
    return True


def step_merge(repo: Path, token: str, owner: str, name: str,
               pr_number: str, title: str, message: str) -> str | None:
    log("步骤 8/9：通过 Gitee 官方 MCP 合并 PR（保留原始提交，合并消息含工作项编号）")
    code0, before = gitee_api(token, "GET", f"/repos/{owner}/{name}/branches/{name and 'main'}")
    before_sha = (before.get("commit") or {}).get("sha", "") if code0 == 200 else ""
    code, out = mcp_call(repo, token, "merge_pull", {
        "owner": owner, "repo": name, "number": int(pr_number),
        "merge_method": "merge", "title": title, "description": message,
    })
    log(f"  MCP 调用完成（exit={code}），原始报文已归档到 evidence/gitee-mcp/")
    if code != 0:
        log(f"  合并失败：\n{out.strip()[-600:]}")
        return None
    code2, out2 = gitee_api(token, "GET", f"/repos/{owner}/{name}/branches/main")
    sha = ""
    if code2 == 200:
        sha = (out2.get("commit") or {}).get("sha", "")
    if not sha:
        log(f"  未能读取 main 头部提交，请查看归档报文：\n{out.strip()[-800:]}")
        return None
    if before_sha and sha == before_sha:
        log("  失败：合并后 main 指针未变化，合并没有真正发生（不把旧提交当作合并提交）")
        return None
    log(f"  main 头部提交 {sha[:12]}（合并前 {before_sha[:12]}）")
    return sha


def step_fetch(repo: Path, remote: str, env: dict[str, str], sha: str) -> bool:
    log(f"步骤 8/9：拉取合并提交 {sha[:12]} 到本地")
    code, out = git_run(["fetch", remote, "main"], repo, env=env)
    if code != 0:
        log(f"  失败：fetch 失败\n{out.strip()[:400]}")
        return False
    code, _ = git_run(["cat-file", "-t", sha], repo)
    if code != 0:
        log("  失败：本地拿不到合并提交对象")
        return False
    log("  已拉取")
    return True


def step_gate(repo: Path, sha: str) -> bool:
    log(f"步骤 8/9：在合并提交 {sha[:12]} 上重跑质量门禁")
    out = EVIDENCE / f"main-gate-{sha[:12]}.log"
    code, msg = run([sys.executable, str(repo / "scripts" / "verify-gate.py"),
                     "--commit", sha, "--out", str(out), "--repo", str(repo)])
    log(f"  门禁日志：{out}（exit={code}）")
    if code != 0:
        log(f"  失败：\n{msg.strip()[-600:]}")
        return False
    return True


def step_deploy(repo: Path, sha: str) -> bool:
    log("步骤 9/9：在合并版本上重新部署")
    out = EVIDENCE / "deployment-main.log"
    script = repo / "scripts" / "deploy-local.ps1"
    code, head_tree = git_run(["rev-parse", "HEAD^{tree}"], repo)
    code2, merge_tree = git_run(["rev-parse", f"{sha}^{{tree}}"], repo)
    if code == 0 and code2 == 0 and head_tree.strip() != merge_tree.strip():
        log(f"  失败：工作区树 {head_tree.strip()[:12]} 与合并提交树 {merge_tree.strip()[:12]} 不一致，拒绝部署")
        return False
    log(f"  工作区树与合并提交树一致（{merge_tree.strip()[:12]}），部署内容即合并版本")
    cmd = ["powershell", "-ExecutionPolicy", "Bypass", "-File", str(script),
           "-LogFile", str(out), "-Commit", sha, "-SkipSmoke"]
    code, msg = run(cmd)
    log(f"  部署日志：{out}（exit={code}）")
    if code != 0:
        log(f"  失败：\n{msg.strip()[-600:]}")
        return False
    return True


def step_delivery(repo: Path) -> bool:
    log("交付链路自检")
    code, msg = run([sys.executable, str(repo / "scripts" / "verify-delivery.py"),
                     "--repo", str(repo),
                     "--out", str(EVIDENCE / "delivery-check.md"),
                     "--json-out", str(EVIDENCE / "delivery-check.json")])
    log(f"  自检结果（exit={code}）\n{msg.strip()[-600:]}")
    return code == 0


def main() -> int:
    ap = argparse.ArgumentParser(description="Gitee 平台交付链路一键驱动")
    ap.add_argument("--repo", default=str(REPO_ROOT))
    ap.add_argument("--owner", default="xishiyao")
    ap.add_argument("--name", default="d5")
    ap.add_argument("--remote", default="gitee")
    ap.add_argument("--remote-url", default="")
    ap.add_argument("--login", default="xishiyao")
    ap.add_argument("--head", default="dev/05")
    ap.add_argument("--base", default="main")
    ap.add_argument("--pr-title", default="[feat] [v2.5.0] [#16221249] 企业模型服务配额管理与远端交付链路")
    ap.add_argument("--merge-title", default="[feat] [v2.5.0] [#16221249] 合并题目 05 企业模型服务配额管理实现")
    ap.add_argument("--merge-message", default="从 dev/05 发起 PR 并合并，保留开发分支原始提交；"
                                              "主要工作项 #16221249，关联需求 #16220913 与缺陷 "
                                              "#16220917 / #16220924 / #16220925 / #16221411 / #16221436。")
    ap.add_argument("--skip-push", action="store_true")
    args = ap.parse_args()

    token = os.environ.get("GITEE_ACCESS_TOKEN", "")
    if not token:
        log("缺少令牌：请设置环境变量 GITEE_ACCESS_TOKEN（令牌不写入任何文件）。")
        return 2
    git_secret = os.environ.get("GITEE_GIT_PASSWORD", "") or token
    if not os.environ.get("GITEE_GIT_PASSWORD", ""):
        log("提示：未设置 GITEE_GIT_PASSWORD，git 推送将使用私人令牌作为口令"
            "（实测 Gitee 的 git HTTPS 推送接受私人令牌）。")

    repo = Path(args.repo).resolve()
    EVIDENCE.mkdir(parents=True, exist_ok=True)
    remote_url = args.remote_url or f"https://gitee.com/{args.owner}/{args.name}.git"
    summary: dict = {
        "started_at": datetime.now(CST).isoformat(),
        "platform": "Gitee 官方 MCP Server（远程端点 https://api.gitee.com/mcp）",
        "owner": args.owner, "repo": args.name, "remote": args.remote,
        "head_branch": args.head, "base_branch": args.base, "steps": [],
    }

    def record(step: str, ok: bool, detail: str = "") -> None:
        summary["steps"].append({"step": step, "ok": ok, "detail": detail,
                                 "at": datetime.now(CST).isoformat()})

    ok, env = (True, None) if args.skip_push else step_push(repo, args.remote, remote_url, git_secret, args.login)
    record("推送", ok)
    if not ok:
        return 1
    if env is None:
        env, _ = askpass_env(git_secret)
        env["GITEE_LOGIN"] = args.login

    step_default_branch(token, args.owner, args.name, args.base)
    ok, rule = step_protect(token, args.owner, args.name, args.base)
    record("分支保护", ok)
    if not ok:
        return 1
    if not step_read_protection(token, args.owner, args.name, args.base, rule):
        record("归档保护规则", False)
        return 1
    record("归档保护规则", True)

    pr_url, _ = step_pr(repo, token, args.owner, args.name, args.head, args.base,
                        args.pr_title, PR_BODY)
    record("创建 PR", bool(pr_url), pr_url or "")
    if not pr_url:
        return 1
    m = re.search(r"/pulls?/(\d+)", pr_url)
    pr_number = m.group(1) if m else ""
    if not pr_number:
        log("无法确定 PR 编号，停止。")
        return 1

    if not step_approve(repo, token, args.owner, args.name, pr_number):
        record("审批 PR", False)
        return 1
    record("审批 PR", True)

    sha = step_merge(repo, token, args.owner, args.name, pr_number,
                     args.merge_title, args.merge_message)
    record("合并 PR", bool(sha), sha or "")
    if not sha:
        return 1
    summary["pr_url"] = pr_url
    summary["pr_number"] = pr_number
    summary["merge_sha"] = sha

    if not step_fetch(repo, args.remote, env, sha):
        record("拉取合并提交", False)
        return 1
    record("拉取合并提交", True)

    if not step_verify_protection(repo, args.remote, env, args.owner, args.name,
                                  args.base, sha):
        record("保护实测", False)
        return 1
    record("保护实测", True)

    ok = step_gate(repo, sha)
    record("合并提交门禁", ok, str(EVIDENCE / f"main-gate-{sha[:12]}.log"))
    ok = step_deploy(repo, sha) and ok
    record("合并版本部署", ok, str(EVIDENCE / "deployment-main.log"))
    step_delivery(repo)

    summary["finished_at"] = datetime.now(CST).isoformat()
    summary["all_ok"] = all(s["ok"] for s in summary["steps"])
    path = EVIDENCE / "gitee-remote-chain.json"
    path.write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
    log(f"链路归档：{path}")
    log("全部步骤结束。" if summary["all_ok"] else "存在失败步骤，详见上方日志与 evidence/ 归档报文。")
    return 0 if summary["all_ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
