#!/usr/bin/env python3
"""从 GitHub REST API 取回指定提交对象并在本地**逐字节重建**（git 传输端口不可达时的备用通道）。

背景
----
本机到 `github.com`（git 传输主机）的 443 端口不可达时，`git fetch` 拿不到远端对象；
而 `api.github.com` 正常，GitHub 的 Git Data API 可以把远端提交的**全部元数据**
（tree / parents / author / committer / message / 签名）读回来。

本脚本据此在本地重建**原始提交对象**并写入对象库：

    tree <tree>\nparent <p>\n…\nauthor …\ncommitter …\n[gpgsig …]\n\n<message>\n

两个容易踩的坑（本脚本已处理）：

1. **时间偏移**：REST API 把时间返回成 UTC（`…Z`），但 GitHub 存储的提交对象里用的是
   **账号所在时区**的偏移（实测 `+0800`）。同一个瞬间、不同的偏移写法 → SHA 不同。
   因此脚本会按候选偏移逐个试算，取**能算出与远端相同 SHA** 的那个。
2. **签名头**：GitHub 网页合并会给合并提交签名（web-flow GPG），对象里多一个 `gpgsig` 头；
   缺了它就重建不出同一个 SHA。签名原文在 API 的 `verification.signature` 里。

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


def to_epoch(iso: str) -> int:
    """ISO 8601 → Unix 秒（只取瞬间，与偏移写法无关）。"""
    text = iso.replace("Z", "+00:00")
    return int(datetime.datetime.fromisoformat(text).timestamp())


def offset_label(delta: datetime.timedelta) -> str:
    total = int(delta.total_seconds())
    sign = "+" if total >= 0 else "-"
    total = abs(total)
    return f"{sign}{total // 3600:02d}{(total % 3600) // 60:02d}"


def candidate_offsets() -> list[str]:
    """候选时区偏移：本机当前偏移优先，其次 UTC。"""
    labels: list[str] = []
    local = datetime.datetime.now().astimezone().utcoffset() or datetime.timedelta(0)
    for delta in (local, datetime.timedelta(0)):
        label = offset_label(delta)
        if label not in labels:
            labels.append(label)
    return labels


def gpgsig_header(info: dict) -> str:
    """重建 GitHub 签名提交对象里的 `gpgsig` 头（首行跟在 "gpgsig " 后，其余每行前置一个空格）。"""
    signature = (info.get("verification") or {}).get("signature") or ""
    if not signature.strip():
        return ""
    lines = signature.split("\n")
    # 注意：**不要**去掉结尾的空元素——签名原文以 "\n" 结尾，git 会把它写成一行
    # 只含一个空格的续行（" "），少了这一行就重建不出同一个 SHA。
    out = ["gpgsig " + lines[0]] + [" " + line for line in lines[1:]]
    return "\n".join(out) + "\n"


def build_raw(info: dict, tz_label: str, message: str) -> bytes:
    tree = info.get("tree", {}).get("sha", "")
    parents = [p.get("sha", "") for p in info.get("parents", [])]
    author = info.get("author", {})
    committer = info.get("committer", {})
    return (
        f"tree {tree}\n"
        + "".join(f"parent {p}\n" for p in parents)
        + f"author {author.get('name', '')} <{author.get('email', '')}> "
          f"{to_epoch(author.get('date', '1970-01-01T00:00:00Z'))} {tz_label}\n"
        + f"committer {committer.get('name', '')} <{committer.get('email', '')}> "
          f"{to_epoch(committer.get('date', '1970-01-01T00:00:00Z'))} {tz_label}\n"
        + gpgsig_header(info)
        + "\n"
        + message
    ).encode("utf-8")


def message_variants(message: str) -> list[str]:
    """提交消息的候选形态。

    REST API 的 `message` 字段会**剥掉末尾换行**，但对象里到底带不带尾换行取决于提交是怎么来的：
    - GitHub 网页合并生成的提交：消息**末尾没有换行**（原样即正确）；
    - 通过 Git Data API（`POST /git/commits`）创建的提交：消息**带尾换行**（需补一个 `\\n`）。

    因此两种形态都要试，否则重建必然对不上远端 SHA（实测踩过：本地推上去的
    `dev/05` 提交用 API 取回时算出 `0754e80b6f10`，补尾换行后才是远端的 `34253c696ba6`）。
    """
    variants = [message]
    if not message.endswith("\n"):
        variants.append(message + "\n")
    return variants


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

    remote_sha = info.get("sha", "")
    tree = info.get("tree", {}).get("sha", "")
    parents = [p.get("sha", "") for p in info.get("parents", [])]

    # 前提：树与父提交必须已在本地，否则重建出来的对象缺依赖
    for sha, what in [(tree, "树")] + [(p, "父提交") for p in parents]:
        if not sha:
            continue
        if subprocess.run(["git", "-C", str(repo), "cat-file", "-e", sha],
                           capture_output=True).returncode != 0:
            print(f"本地缺少{what}对象 {sha[:12]}，无法重建（需先取得该对象）")
            return 1

    for tz_label in candidate_offsets():
        for message in message_variants(info.get("message", "")):
            raw = build_raw(info, tz_label, message)
            proc = subprocess.run(["git", "-C", str(repo), "hash-object", "-t", "commit", "--stdin"],
                                  input=raw, capture_output=True)
            if proc.returncode != 0:
                print(f"计算提交对象失败：{proc.stderr.decode('utf-8', 'replace')[:300]}")
                return 1
            local_sha = proc.stdout.decode().strip()
            if local_sha != remote_sha:
                print(f"  偏移 {tz_label} / 消息尾换行 {'有' if message.endswith(chr(10)) else '无'}"
                      f" 算得 {local_sha[:12]}，与远端不符，继续试下一个")
                continue
            # SHA 一致才写入对象库
            proc = subprocess.run(["git", "-C", str(repo), "hash-object", "-t", "commit", "-w", "--stdin"],
                                  input=raw, capture_output=True)
            if proc.returncode != 0:
                print(f"写入提交对象失败：{proc.stderr.decode('utf-8', 'replace')[:300]}")
                return 1
            written = proc.stdout.decode().strip()
            print(f"已在本地重建提交对象 {written[:12]}（与远端逐字节一致，"
                  f"时区偏移 {tz_label}，消息尾换行 {'有' if message.endswith(chr(10)) else '无'}）")
            print(f"  树 {tree[:12]}｜父提交 {'、'.join(p[:12] for p in parents)}")
            return 0

    print(f"重建失败：所有候选时区偏移都算不出与远端一致的 SHA（远端 {remote_sha[:12]}）")
    print("  （不写入不一致的对象，放弃）")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
