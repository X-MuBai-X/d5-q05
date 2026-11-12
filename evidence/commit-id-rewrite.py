#!/usr/bin/env python3
"""按「原提交 → 真实工作项」映射重建提交链（字节精确，不改动未命中的提交）。

为什么不用 git filter-branch：Windows 下 filter-branch 的 msg-filter 经由 shell 管道，
Python 文本模式会把提交消息尾部 LF 转成 CRLF，导致**未命中**的提交 SHA 也被改写，
进而使 main 与 dev/05 失去共同祖先。本脚本直接用 git commit-tree 以原始字节重建，
未命中映射的提交保持原 SHA 不变（tree/parent/message/author/committer 全部沿用）。

用法：
    python rebuild_commits.py --ref refs/heads/dev/05 --backup-ref refs/heads/backup/pre-id-rewrite
"""
from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path

# 原始 commit SHA -> 真实工作项 ID（commit-convention.md 要求 feat/fix 关联真实编号）
MAPPING = {
    "5d97538": "16220913",  # feat 实现企业模型服务配额管理核心业务 -> 需求《题目05 ...》
    "02c2e21": "16220913",  # feat 补齐迟到结果观测链路与日终报表终态归一 -> 同一需求
    "32e57bb": "16220924",  # fix 运营核查未指定租户返回内部错误 -> 缺陷《...返回 500 而非 400》
    "4c66ac7": "16220917",  # fix 并发降额突破额度上限 -> 缺陷《并发降额与受理交错...》
    "507927f": "16220925",  # fix 部署脚本未记录进程号 -> 缺陷《部署脚本未写入 PID 文件...》
}
OLD = b"#6974499249"


def git(*args: str, env: dict[str, str] | None = None) -> bytes:
    proc = subprocess.run(["git", *args], capture_output=True, env=env)
    if proc.returncode != 0:
        raise RuntimeError(f"git {' '.join(args)} failed: {proc.stderr.decode('utf-8', 'replace')}")
    return proc.stdout


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--ref", default="refs/heads/dev/05")
    ap.add_argument("--backup-ref", default="refs/heads/backup/pre-id-rewrite")
    args = ap.parse_args()

    commits = git("rev-list", "--reverse", args.backup_ref).decode().split()
    print(f"待重建提交数：{len(commits)}")

    new_parent: str | None = None
    changed: list[tuple[str, str, str]] = []
    for sha in commits:
        raw = git("cat-file", "commit", sha)
        head, _, message = raw.partition(b"\n\n")
        headers: dict[str, str] = {}
        for line in head.split(b"\n"):
            key, _, value = line.partition(b" ")
            headers[key.decode()] = value.decode()

        tree = headers["tree"]
        new_message = message
        target = MAPPING.get(sha[:7])
        if target and OLD in new_message:
            new_message = new_message.replace(OLD, ("#" + target).encode())

        author = headers["author"]
        committer = headers["committer"]
        a_name, a_email, a_date = _split_ident(author)
        c_name, c_email, c_date = _split_ident(committer)

        env = {
            "GIT_AUTHOR_NAME": a_name,
            "GIT_AUTHOR_EMAIL": a_email,
            "GIT_AUTHOR_DATE": a_date,
            "GIT_COMMITTER_NAME": c_name,
            "GIT_COMMITTER_EMAIL": c_email,
            "GIT_COMMITTER_DATE": c_date,
            "PATH": env_path(),
        }
        msg_file = Path(__file__).with_name("_msg.tmp")
        msg_file.write_bytes(new_message)

        cmd = ["commit-tree", tree]
        if new_parent:
            cmd += ["-p", new_parent]
        cmd += ["-F", str(msg_file)]
        new_sha = git(*cmd, env=env).decode().strip()
        msg_file.unlink(missing_ok=True)

        if new_sha != sha:
            changed.append((sha, new_sha, new_message.split(b"\n")[0].decode()))
        new_parent = new_sha

    git("update-ref", args.ref, new_parent or "")
    print(f"重建完成：{args.ref} -> {new_parent}")
    print(f"实际改写 {len(changed)} 条：")
    for old, new, subject in changed:
        print(f"  {old[:7]} -> {new[:7]}  {subject}")
    print(f"未改写 {len(commits) - len(changed)} 条（SHA 保持不变）")
    return 0


def env_path() -> str:
    import os
    return os.environ.get("PATH", "")


def _split_ident(value: str) -> tuple[str, str, str]:
    """拆分 'Name <email> 1700000000 +0800' 为 (name, email, date)。"""
    name_part, _, rest = value.partition(" <")
    email, _, date = rest.partition("> ")
    return name_part, email, date.strip()


if __name__ == "__main__":
    raise SystemExit(main())
