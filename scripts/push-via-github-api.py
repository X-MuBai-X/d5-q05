#!/usr/bin/env python3
"""通过 GitHub REST **Git Data API** 推送本地分支（git 传输端口不可达时的备用通道）。

背景
----
题面第 8 节第 1 条要求「通过 GitHub MCP 创建独立仓库」，随后按 PR / 分支保护 / 合并推进。
正常做法是 `git push`，但本次实跑时本机到 `github.com`（git 传输主机）的 443 端口不可达
（`api.github.com` 正常），而 GitHub 的 Git Data API 走的是 `api.github.com`，
因此改用它完成推送。

与 `git push` 的等价性
---------------------
本脚本**不新建提交**，而是把本地已有的提交原样搬到远端：

1. 逐个上传新增/修改的 blob（`POST /git/blobs`），返回的 SHA 必须与本地一致；
2. 以父提交树为基底创建新树（`POST /git/trees`），返回的 SHA 必须与本地提交树一致；
3. 用**本地提交的全部元数据**（message / author / committer / parents）创建提交
   （`POST /git/commits`），返回的 SHA 必须与本地提交 SHA 一致；
4. 最后快进更新分支引用（`PATCH /git/refs/heads/{branch}`）。

任何一步的 SHA 对不上就**立即中止**并报错，绝不留下半推状态。

用法：
    $env:GITHUB_PERSONAL_ACCESS_TOKEN = "<classic PAT: ghp_…，需 repo scope>"
    python scripts/push-via-github-api.py --repo . --owner <owner> --name <repo> --branch dev/05

退出码：0 = 推送成功且远端与本地 SHA 一致；1 = 失败。
"""
from __future__ import annotations

import argparse
import base64
import datetime
import json
import os
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

API = "https://api.github.com"


def git(repo: Path, *args: str) -> str:
    proc = subprocess.run(["git", "-C", str(repo)] + list(args),
                          capture_output=True, text=True, encoding="utf-8", errors="replace")
    if proc.returncode != 0:
        raise RuntimeError(f"git {' '.join(args)} 失败：{(proc.stderr or proc.stdout).strip()[:300]}")
    return proc.stdout


def git_bin(repo: Path, sha: str) -> bytes:
    proc = subprocess.run(["git", "-C", str(repo), "cat-file", "blob", sha],
                          capture_output=True)
    if proc.returncode != 0:
        raise RuntimeError(f"读取 blob {sha} 失败")
    return proc.stdout


class GitHubAPI:
    def __init__(self, token: str) -> None:
        self.token = token
        self.opener = urllib.request.build_opener()

    def call(self, method: str, path: str, body: dict | None = None) -> tuple[int, dict]:
        data = json.dumps(body).encode("utf-8") if body is not None else None
        headers = {
            "Authorization": f"Bearer {self.token}",
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
            "User-Agent": "mcode-push-via-github-api/1.0",
        }
        if data is not None:
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(API + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(req, timeout=120) as resp:
                raw = resp.read().decode("utf-8", "replace")
                return resp.status, (json.loads(raw) if raw.strip() else {})
        except urllib.error.HTTPError as exc:
            raw = exc.read().decode("utf-8", "replace")
            try:
                return exc.code, json.loads(raw)
            except json.JSONDecodeError:
                return exc.code, {"raw": raw[:500]}
        except urllib.error.URLError as exc:
            return 0, {"error": str(exc)}


def split_commit(raw: str) -> tuple[dict, str]:
    """拆开 commit 对象：头部字段与消息正文。"""
    head, _, body = raw.partition("\n\n")
    fields: dict = {}
    for line in head.splitlines():
        key, _, value = line.partition(" ")
        fields.setdefault(key, value)
    return fields, body


def to_iso8601(when: str) -> str:
    """把 git 的原始时间戳「<秒> <±HHMM>」转成 GitHub API 接受的 ISO 8601。"""
    ts_text, _, tz = when.partition(" ")
    ts = int(ts_text)
    sign = -1 if tz.startswith("-") else 1
    offset = datetime.timedelta(hours=int(tz[1:3]), minutes=int(tz[3:5])) * sign
    moment = datetime.datetime.fromtimestamp(ts, datetime.timezone(offset))
    return moment.isoformat()


def main() -> int:
    ap = argparse.ArgumentParser(description="用 GitHub Git Data API 推送本地分支")
    ap.add_argument("--repo", default=".")
    ap.add_argument("--owner", required=True)
    ap.add_argument("--name", required=True)
    ap.add_argument("--branch", required=True)
    ap.add_argument("--token", default=os.environ.get("GITHUB_PERSONAL_ACCESS_TOKEN", ""))
    args = ap.parse_args()

    if not args.token:
        print("缺少令牌：请设置环境变量 GITHUB_PERSONAL_ACCESS_TOKEN", file=sys.stderr)
        return 1

    repo = Path(args.repo).resolve()
    api = GitHubAPI(args.token)

    head_sha = git(repo, "rev-parse", args.branch).strip()
    parent_sha = git(repo, "rev-parse", f"{head_sha}^").strip()
    tree_sha = git(repo, "rev-parse", f"{head_sha}^{{tree}}").strip()
    parent_tree = git(repo, "rev-parse", f"{parent_sha}^{{tree}}").strip()
    print(f"本地 {args.branch} = {head_sha[:12]}（父提交 {parent_sha[:12]}，树 {tree_sha[:12]}）")

    # 远端当前分支位置（决定快进还是多提交）
    code, ref = api.call("GET", f"/repos/{args.owner}/{args.name}/git/ref/heads/{args.branch}")
    if code == 200:
        remote_sha = (ref.get("object") or {}).get("sha", "")
        print(f"远端 {args.branch} = {remote_sha[:12] or '(空)'}")
    elif code == 404:
        remote_sha = ""
        print(f"远端尚无 {args.branch} 分支")
    else:
        print(f"读取远端引用失败（HTTP {code}）：{json.dumps(ref, ensure_ascii=False)[:300]}")
        return 1

    # 需要搬运的提交：从远端头部之后到本地头部
    if remote_sha:
        missing = git(repo, "rev-list", "--reverse", f"{remote_sha}..{head_sha}").split()
        if not missing:
            print("远端已包含本地提交，无需推送")
            return 0
    else:
        missing = git(repo, "rev-list", "--reverse", head_sha).split()
    print(f"待推送提交 {len(missing)} 个：{'、'.join(s[:12] for s in missing)}")

    for commit_sha in missing:
        parents = git(repo, "rev-list", "--parents", "-n", "1", commit_sha).split()[1:]
        raw = git(repo, "cat-file", "commit", commit_sha)
        fields, message = split_commit(raw)

        # 1) 上传本提交相对其第一个父提交变化的文件
        diff_args = ["diff-tree", "-r", "--no-commit-id", "--raw", "-z"]
        if parents:
            diff_args.append(parents[0])
        diff_args.append(commit_sha)
        raw_diff = git(repo, *diff_args)
        entries: list[dict] = []
        # `git diff-tree --raw -z` 的格式是
        #   :<旧模式> <新模式> <旧SHA> <新SHA> <状态>\0<路径>\0
        # （-z 用 NUL 分隔，元数据与路径**不是**用 TAB 分隔；重命名时多一个旧路径字段）
        chunks = raw_diff.split("\0")
        idx = 0
        while idx < len(chunks):
            meta = chunks[idx]
            if not meta.startswith(":"):
                idx += 1
                continue
            parts = meta[1:].split()
            if len(parts) < 5:
                idx += 1
                continue
            old_mode, new_mode, old_sha, new_sha, status = parts[0], parts[1], parts[2], parts[3], parts[4]
            if status.startswith(("R", "C")):
                path = chunks[idx + 2] if idx + 2 < len(chunks) else ""
                idx += 3
            else:
                path = chunks[idx + 1] if idx + 1 < len(chunks) else ""
                idx += 2
            if not path:
                raise RuntimeError(f"无法解析 diff 条目的路径：{meta!r}")
            if new_sha == "0":  # 删除
                entries.append({"path": path, "mode": old_mode, "type": "blob", "sha": None})
                continue
            blob = git_bin(repo, new_sha)
            b64 = base64.b64encode(blob).decode("ascii")
            code, resp = api.call("POST", f"/repos/{args.owner}/{args.name}/git/blobs",
                                  {"content": b64, "encoding": "base64"})
            if code not in (200, 201) or resp.get("sha") != new_sha:
                print(f"上传 blob {new_sha[:12]} 失败（HTTP {code}）：{json.dumps(resp, ensure_ascii=False)[:300]}")
                return 1
            entries.append({"path": path, "mode": new_mode, "type": "blob", "sha": new_sha})

        # 2) 创建树
        base_tree = git(repo, "rev-parse", f"{parents[0]}^{{tree}}").strip() if parents else None
        body = {"tree": entries}
        if base_tree:
            body["base_tree"] = base_tree
        code, resp = api.call("POST", f"/repos/{args.owner}/{args.name}/git/trees", body)
        local_tree = git(repo, "rev-parse", f"{commit_sha}^{{tree}}").strip()
        if code not in (200, 201) or resp.get("sha") != local_tree:
            print(f"创建树失败或与本地不一致（HTTP {code}）：远端 {resp.get('sha')} / 本地 {local_tree}")
            print(json.dumps(resp, ensure_ascii=False)[:400])
            return 1

        # 3) 创建提交（严格复用本地元数据，保证 SHA 一致）
        def who(prefix: str) -> dict:
            raw_who = fields.get(prefix, "")
            name, _, rest = raw_who.partition(" <")
            email, _, when = rest.partition(">")
            return {"name": name, "email": email, "date": to_iso8601(when.strip())}

        body = {
            # 消息原样送出（**保留结尾换行**）：git 提交对象里的消息以 "\n" 结尾，
            # 去掉它会让重算出的提交 SHA 与本地不一致。
            "message": message,
            "tree": local_tree,
            "parents": parents,
            "author": who("author"),
            "committer": who("committer"),
        }
        code, resp = api.call("POST", f"/repos/{args.owner}/{args.name}/git/commits", body)
        if code not in (200, 201) or resp.get("sha") != commit_sha:
            print(f"创建提交失败或与本地不一致（HTTP {code}）：远端 {resp.get('sha')} / 本地 {commit_sha}")
            print(json.dumps(resp, ensure_ascii=False)[:400])
            return 1
        print(f"  已搬运提交 {commit_sha[:12]}（SHA 与本地一致）")

    # 4) 快进更新分支引用
    code, resp = api.call("PATCH", f"/repos/{args.owner}/{args.name}/git/refs/heads/{args.branch}",
                          {"sha": head_sha, "force": False})
    if code not in (200, 201):
        print(f"更新分支引用失败（HTTP {code}）：{json.dumps(resp, ensure_ascii=False)[:300]}")
        return 1
    print(f"远端 {args.branch} 已更新到 {head_sha[:12]}")

    # 5) 复核
    code, ref = api.call("GET", f"/repos/{args.owner}/{args.name}/git/ref/heads/{args.branch}")
    now = (ref.get("object") or {}).get("sha", "")
    if now != head_sha:
        print(f"复核不一致：远端 {now[:12]} / 本地 {head_sha[:12]}")
        return 1
    print("复核通过：远端与本地提交 SHA 完全一致")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
