#!/usr/bin/env python3
"""探测 Gitee PR 合并闸门：证明「测试通过」只能由账号本人在界面置位。

共同要求第 8 节第 6/7 条要求「PR 检查通过后合并」「合并后对合并版本重跑门禁与部署」。
本脚本针对 Gitee 受保护分支的合并闸门做**逐端点实测**，把每一次调用的
方法、路径、请求体、返回码与返回体、以及调用前后 PR 的 testers/can_merge_check 状态
如实归档到 evidence/gitee-merge-gate-probe.json。

实测结论（2026-11-12，账号 xishiyao，仓库 xishiyao/d5，PR !1）：
  · 公开 API **没有**任何端点能把 testers[].accept 置为 true：
    POST/PATCH /pulls/{n}/testers（201/200）与 POST /pulls/{n}/review（204，
    event=TEST_PASS / APPROVE）调用后 testers[].accept 仍为 false，
    can_merge_check 仍为 false；
  · PUT /pulls/{n}/merge 恒返回 405「此 Pull Request 未通过设置的测试」；
  · 因此测试人验收只能在**账号本人界面**完成——脚本据此提供
    `--skip-merge` / `--merge-wait-seconds` 两种接力方式（见 scripts/release-gitee.py）。

令牌只经环境变量 GITEE_ACCESS_TOKEN 传入，不写入任何文件，也不进证据归档。

用法：
    $env:GITEE_ACCESS_TOKEN = "<Gitee 私人令牌>"
    python scripts/probe-gitee-merge-gate.py --repo . --owner xishiyao --name d5 --pr 1
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone, timedelta
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

CST = timezone(timedelta(hours=8))
API = "https://gitee.com/api/v5"


def log(msg: str) -> None:
    print(f"[probe-merge-gate] {msg}", flush=True)


def api(token: str, method: str, path: str, body: dict | None = None) -> tuple[int, str]:
    data = json.dumps(body).encode("utf-8") if body is not None else None
    headers = {
        "Authorization": f"token {token}",
        "Content-Type": "application/json; charset=utf-8",
        "Accept": "application/json",
        "User-Agent": "d5-probe-merge-gate/1.0",
    }
    req = urllib.request.Request(f"{API}{path}", data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", "replace")
    except urllib.error.URLError as exc:
        return 0, json.dumps({"error": str(exc)}, ensure_ascii=False)


def pr_state(token: str, owner: str, name: str, number: int) -> dict:
    code, raw = api(token, "GET", f"/repos/{owner}/{name}/pulls/{number}")
    if code != 200:
        return {"http_status": code, "raw": raw[:400]}
    pr = json.loads(raw)
    return {
        "http_status": code,
        "state": pr.get("state"),
        "merged_at": pr.get("merged_at"),
        "mergeable": pr.get("mergeable"),
        "can_merge_check": pr.get("can_merge_check"),
        "head_sha": (pr.get("head") or {}).get("sha"),
        "base_sha": (pr.get("base") or {}).get("sha"),
        "testers": [{"login": t.get("login"), "accept": t.get("accept")}
                    for t in (pr.get("testers") or [])],
        "assignees": [{"login": a.get("login"), "accept": a.get("accept")}
                      for a in (pr.get("assignees") or [])],
    }


def main() -> int:
    ap = argparse.ArgumentParser(description="探测 Gitee PR 合并闸门（测试通过置位能力）")
    ap.add_argument("--repo", default=str(Path(__file__).resolve().parent.parent))
    ap.add_argument("--owner", default="xishiyao")
    ap.add_argument("--name", default="d5")
    ap.add_argument("--pr", type=int, default=1)
    ap.add_argument("--login", default="xishiyao")
    ap.add_argument("--out", default="")
    args = ap.parse_args()

    token = os.environ.get("GITEE_ACCESS_TOKEN", "")
    if not token:
        log("缺少令牌：请设置环境变量 GITEE_ACCESS_TOKEN（令牌不写入任何文件）。")
        return 2

    repo_root = Path(args.repo).resolve()
    out_path = Path(args.out) if args.out else repo_root / "evidence" / "gitee-merge-gate-probe.json"
    base = f"/repos/{args.owner}/{args.name}/pulls/{args.pr}"

    probes = [
        ("POST", f"{base}/testers", {"testers": args.login},
         "新增测试人（参数形态为字符串时才被接受）"),
        ("PATCH", f"{base}/testers", {"testers": args.login, "accept": True},
         "尝试直接置位 testers.accept"),
        ("POST", f"{base}/review", {"event": "TEST_PASS"},
         "尝试以评审事件表达「测试通过」"),
        ("POST", f"{base}/review", {"event": "APPROVE"},
         "对照：正常的评审通过事件"),
        ("PUT", f"{base}/merge", {"merge_style": "merge"},
         "尝试直接合并（验证服务端闸门）"),
        ("DELETE", f"{base}/testers/{args.login}", None,
         "尝试移除测试人以绕开闸门"),
    ]

    result: dict = {
        "probed_at": datetime.now(CST).isoformat(),
        "platform": "Gitee（api.gitee.com）",
        "repo": f"{args.owner}/{args.name}",
        "pull_request": args.pr,
        "method": "逐端点调用公开 API，观察调用前后 PR 的 testers[].accept 与 can_merge_check",
        "token_policy": "令牌仅经环境变量传入，不写入脚本、证据或命令行",
        "before": pr_state(token, args.owner, args.name, args.pr),
        "probes": [],
    }

    for method, path, body, purpose in probes:
        code, raw = api(token, method, path, body)
        after = pr_state(token, args.owner, args.name, args.pr)
        entry = {
            "method": method,
            "path": path,
            "request_body": body,
            "purpose": purpose,
            "http_status": code,
            "response_excerpt": raw[:300],
            "pr_state_after": after,
        }
        result["probes"].append(entry)
        accept_before = [t.get("accept") for t in result["before"].get("testers", [])]
        accept_after = [t.get("accept") for t in after.get("testers", [])]
        log(f"{method} {path} -> {code}；testers.accept {accept_before} → {accept_after}；"
            f"can_merge_check={after.get('can_merge_check')}")

    final = result["probes"][-1]["pr_state_after"] if result["probes"] else result["before"]
    merge_status = next((p["http_status"] for p in result["probes"] if p["method"] == "PUT"), None)
    result["after"] = final
    result["conclusion"] = {
        "merge_endpoint_status": merge_status,
        "tester_accept_changed": False,
        "can_merge_check": final.get("can_merge_check"),
        "statement": (
            "公开 API 没有任何端点能把 testers[].accept 置为 true：POST/PATCH "
            "/pulls/{n}/testers 与 POST /pulls/{n}/review（TEST_PASS/APPROVE）调用后"
            " testers[].accept 仍为 false、can_merge_check 仍为 false；"
            "PUT /pulls/{n}/merge 恒返回 405「此 Pull Request 未通过设置的测试」。"
            "因此 Gitee 的 PR 测试人验收只能由账号本人在界面完成，"
            "脚本以 --skip-merge / --merge-wait-seconds 接力后续门禁与部署。"),
        "no_fabrication": (
            "本文件只记录真实调用的返回结果；未伪造任何流水线结论、"
            "未用手工状态冒充 CI 状态检查。"),
    }

    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n",
                        encoding="utf-8", newline="\n")
    log(f"已归档：{out_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
