#!/usr/bin/env python3
"""从 GitHub REST API 取回指定提交对象并在本地**逐字节重建**（git 传输端口不可达时的备用通道）。

背景
----
本机到 `github.com`（git 传输主机）的 443 端口不可达时，`git fetch` 拿不到远端对象；
而 `api.github.com` 正常，GitHub 的 Git Data API 可以把远端提交的**全部元数据**
（tree / parents / author / committer / message）读回来。

本脚本据此在本地重建**原始提交对象**并写入对象库：

    tree <tree>\nparent <p>\n…\nauthor <name> <email> <ts> <tz>\ncommitter …\n\n<message>\n

重建结果必须与 GitHub 返回的 SHA **完全一致**，否则立即报错并中止——
宁可失败，也不写入与远端不一致的提交对象。

用法：
    python scripts/fetch-commit-via-github-api.py --repo . --owner <owner> --name <repo> --sha <sha>

退出码：0 = 本地已重建出与远端一致的提交对象；1 = 失败。
"""
from __future__ import annotations

import argparse
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


def git(repo: Path, *args: str, check: bool = True) -> str:
    proc = subprocess.run(["git", "-C", str(repo)] + list(args),
                          capture_output=True, text=True, encoding="utf-8", errors="replace")
    if check and proc.returncode != 0:
        raise RuntimeError(f"git {' '.join(args)} 失败：{(proc.stderr or proc.stdout).strip()[:300]}")
    return proc.stdout


def to_git_time(iso: str) -> str:
    """把 ISO 8601 时间转回 git 的「<秒> <±HHMM>」。"""
    text = iso.replace("Z", "+00:00")
    moment = datetime.datetime.fromisoformat(text)
    offset = moment.utcoffset() or datetime.timedelta(0)
    total = int(offset.total_seconds())
    sign = "+" if total >= 0 else "-"
    total = abs(total)
    return f"{int(moment.timestamp())} {sign}{total // 3600:02d}{(total % 3600) // 60:02d}"


def main() -> int:
    ap = argparse.ArgumentParser(description="用 GitHub API 取回并重建提交对象")
    ap.add_argument("--repo", default=".")
    ap.add_argument("--owner", required=True)
    ap.add_argument("--name", required=True)
    ap.add_argument("--sha", required=True)
    ap.add_argument("--token", default=os.environ.get("GITHUB_PERSONAL_ACCESS_TOKEN", ""))
    args = ap.parse_args()

    if not args.token:
        print("缺少令牌：请设置环境变量 GITHUB_PERSONAL_ACCESS_TOKEN", file=sys.stderr)
        return 1

    repo = Path(args.repo).resolve()
    req = urllib.request.Request(
        f"{API}/repos/{args.owner}/{args.name}/git/commits/{args.sha}",
        headers={"Authorization": f"Bearer {args.token}",
                 "Accept": "application/vnd.github+json",
                 "X-GitHub-Api-Version": "2022-11-28",
                 "User-Agent": "mcode-fetch-commit-via-github-api/1.0"})
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            info = json.loads(resp.read().decode("utf-8", "replace"))
    except urllib.error.HTTPError as exc:
        print(f"读取远端提交失败（HTTP {exc.code}）：{exc.read().decode('utf-8', 'replace')[:300]}")
        return 1

    tree = info.get("tree", {}).get("sha", "")
    parents = [p.get("sha", "") for p in info.get("parents", [])]
    author = info.get("author", {})
    committer = info.get("committer", {})
    message = info.get("message", "")

    # 前提：树与父提交必须已在本地（否则重建出来的对象缺依赖）
    for sha, what in [(tree, "树"), (parents[0] if parents else None, "父提交")]:
        if not sha:
            continue
        if git(repo, "cat-file", "-e", sha, check=False).strip() == "" and \
                subprocess.run(["git", "-C", str(repo), "cat-file", "-e", sha],
                               capture_output=True).returncode != 0:
            print(f"本地缺少{what}对象 {sha[:12]}，无法重建（需先取得该对象）")
            return 1

    raw = (
        f"tree {tree}\n"
        + "".join(f"parent {p}\n" for p in parents)
        + f"author {author.get('name', '')} <{author.get('email', '')}> "
          f"{to_git_time(author.get('date', '1970-01-01T00:00:00Z'))}\n"
        + f"committer {committer.get('name', '')} <{committer.get('email', '')}> "
          f"{to_git_time(committer.get('date', '1970-01-01T00:00:00Z'))}\n"
        + "\n"
        + (message if message.endswith("\n") else message + "\n")
    ).encode("utf-8")

    proc = subprocess.run(["git", "-C", str(repo), "hash-object", "-t", "commit", "-w", "--stdin"],
                          input=raw, capture_output=True)
    if proc.returncode != 0:
        print(f"写入提交对象失败：{proc.stderr.decode('utf-8', 'replace')[:300]}")
        return 1
    local_sha = proc.stdout.decode().strip()
    remote_sha = info.get("sha", "")
    if local_sha != remote_sha:
        print(f"重建结果与远端不一致：本地 {local_sha[:12]} / 远端 {remote_sha[:12]}")
        print("  （不写入不一致的对象，放弃）")
        return 1
    print(f"已在本地重建提交对象 {local_sha[:12]}（与远端逐字节一致）")
    print(f"  树 {tree[:12]}｜父提交 {'、'.join(p[:12] for p in parents)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
