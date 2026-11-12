#!/usr/bin/env python3
"""针对**指定提交**执行质量门禁（gofmt / vet / 单元测试 / 覆盖率），并归档完整日志。

共同要求第 8 节第 7 条：「合并后必须针对主分支实际合并版本重新执行质量门禁、构建和部署，
记录部署提交、流水线及制品」。本脚本用于在**合并提交**上复跑门禁：

- 用 `git worktree` 把指定提交检出到独立目录，不影响当前工作区；
- 逐步执行门禁并把每一步的**原始输出**写入日志；
- 日志头部记录被检提交的完整 SHA，因此「门禁跑的是哪个版本」可核查。

用法：
    python scripts/verify-gate.py --commit main --out evidence/main-gate.log
    python scripts/verify-gate.py --commit HEAD --with-race --out evidence/gate-head.log

退出码：0 = 全部门禁通过；1 = 有门禁失败；2 = 执行错误。
"""
from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
import tempfile
import time
from datetime import datetime, timezone, timedelta
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

CST = timezone(timedelta(hours=8))


def run(cmd: list[str], cwd: Path, env: dict[str, str] | None = None,
        timeout: int = 1800) -> tuple[int, str]:
    """执行命令并返回 (退出码, 合并输出)。"""
    try:
        proc = subprocess.run(cmd, cwd=str(cwd), capture_output=True, text=True,
                              encoding="utf-8", errors="replace", timeout=timeout, env=env)
    except (OSError, subprocess.SubprocessError) as exc:
        return 2, f"命令执行失败: {exc}\n"
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def main() -> int:
    ap = argparse.ArgumentParser(description="针对指定提交执行质量门禁")
    ap.add_argument("--commit", default="HEAD", help="被检提交（SHA / 分支名 / HEAD）")
    ap.add_argument("--out", default="evidence/gate.log", help="门禁日志输出路径")
    ap.add_argument("--repo", default=".", help="仓库路径")
    ap.add_argument("--with-race", action="store_true", help="额外执行容器内 go test -race")
    ap.add_argument("--keep-worktree", action="store_true", help="保留临时 worktree 不清理")
    args = ap.parse_args()

    repo = Path(args.repo).resolve()
    started = datetime.now(CST)

    code, sha = run(["git", "rev-parse", args.commit], repo)
    if code != 0:
        print(f"无法解析提交 {args.commit}：\n{sha}", file=sys.stderr)
        return 2
    sha = sha.strip()

    code, subject = run(["git", "log", "-1", "--format=%h %s", sha], repo)
    subject = subject.strip()

    worktree = Path(tempfile.mkdtemp(prefix="d5-gate-"))
    target = worktree / "repo"
    code, out = run(["git", "worktree", "add", "--detach", str(target), sha], repo)
    if code != 0:
        print(f"创建 worktree 失败：\n{out}", file=sys.stderr)
        shutil.rmtree(worktree, ignore_errors=True)
        return 2

    log: list[str] = []
    # 覆盖率剖面路径必须是**绝对路径**：go test 的测试二进制工作目录是**包目录**，
    # 相对路径的 -coverprofile 会写到包目录下（目录不存在时静默不写），
    # 于是统计脚本读到的是上一轮的**陈旧剖面**。本轮实测踩过这个坑。
    cov_profile = target / "evidence" / "coverage.out"
    if cov_profile.exists():
        cov_profile.unlink()
    log.append("=" * 78)
    log.append("题目 05 质量门禁执行日志（针对指定提交）")
    log.append("=" * 78)
    log.append(f"# 执行时间      : {started.isoformat()}")
    log.append(f"# 被检提交      : {sha}")
    log.append(f"# 被检提交标题  : {subject}")
    log.append(f"# 检出方式      : git worktree add --detach（独立目录，不改动主工作区）")
    log.append(f"# 门禁项        : gofmt / go vet / go test（含覆盖率）/ coverage-summary"
               + (" / go test -race（容器）" if args.with_race else ""))
    log.append("")

    steps: list[tuple[str, list[str], int]] = [
        ("gofmt -l .（期望输出为空）", ["gofmt", "-l", "."], 300),
        ("go vet ./...", ["go", "vet", "./..."], 900),
        ("go test ./... -count=1 -coverprofile=<绝对路径> -covermode=atomic",
         ["go", "test", "./...", "-count=1", f"-coverprofile={cov_profile}",
          "-covermode=atomic"], 1800),
    ]

    failed: list[str] = []
    for title, cmd, timeout in steps:
        t0 = time.time()
        code, out = run(cmd, target, timeout=timeout)
        elapsed = time.time() - t0
        log.append("-" * 78)
        log.append(f"$ {' '.join(cmd)}")
        log.append(f"# exit={code}  elapsed={elapsed:.1f}s")
        log.append(out.rstrip() or "(无输出)")
        log.append("")
        if code != 0:
            failed.append(title)

    # 覆盖率剖面必须由本轮真实产出，否则下面的统计就是拿陈旧数据当本轮结论。
    if not cov_profile.is_file():
        log.append("-" * 78)
        log.append("##[gate] 覆盖率剖面未产出：go test -coverprofile 没有写出文件")
        log.append("")
        failed.append("覆盖率剖面产出")
        cov_summary = target / "evidence" / "coverage-summary.txt"
        summary_code, summary_out = run(
            [sys.executable, "scripts/coverage-summary.py",
             "--profile", str(cov_profile),
             "--out", str(cov_summary)], target)
        log.append(f"# coverage-summary exit={summary_out.strip()[:200]}")
        log.append("")
    else:
        # 覆盖率汇总
        t0 = time.time()
        code, out = run([sys.executable, "scripts/coverage-summary.py",
                         "--profile", str(cov_profile),
                         "--out", str(target / "evidence" / "coverage-summary.txt")], target)
        log.append("-" * 78)
        log.append(f"$ python scripts/coverage-summary.py --profile {cov_profile} "
                   f"--out {target / 'evidence' / 'coverage-summary.txt'}")
        log.append(f"# exit={code}  elapsed={time.time() - t0:.1f}s")
        log.append(out.rstrip() or "(无输出)")
        cov_file = target / "evidence" / "coverage-summary.txt"
        if cov_file.is_file():
            log.append("")
            log.append("---- evidence/coverage-summary.txt ----")
            log.append(cov_file.read_text(encoding="utf-8").rstrip())
        log.append("")
        if code != 0:
            failed.append("coverage-summary")

    # 把本轮真实产出的剖面与汇总同步回仓库 evidence/，使归档与被检提交一致
    # （此前仓库里的 coverage.out 可能是更早一轮的陈旧文件）。
    for name in ("coverage.out", "coverage-summary.txt"):
        src = target / "evidence" / name
        if src.is_file():
            dst = repo / "evidence" / name
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(src, dst)
            log.append(f"# 已同步本轮产物到 {dst}")

    # 竞态检测（容器）
    if args.with_race:
        t0 = time.time()
        code, out = run(["sh", "scripts/race-test.sh"], target, timeout=3600)
        log.append("-" * 78)
        log.append("$ sh scripts/race-test.sh（golang:1.27 容器内 go test ./... -race）")
        log.append(f"# exit={code}  elapsed={time.time() - t0:.1f}s")
        log.append(out.rstrip() or "(无输出)")
        log.append("")
        if code != 0:
            failed.append("go test -race")

    finished = datetime.now(CST)
    log.append("=" * 78)
    log.append(f"# 结束时间: {finished.isoformat()}")
    log.append(f"# 被检提交: {sha}")
    if failed:
        log.append(f"# 结论: 失败 —— {'、'.join(failed)}")
    else:
        log.append("# 结论: 全部门禁通过")
    log.append("=" * 78)

    out_path = Path(args.out)
    if not out_path.is_absolute():
        out_path = Path.cwd() / out_path
    out_path.parent.mkdir(parents=True, exist_ok=True)
    with open(out_path, "w", encoding="utf-8", newline="\n") as fh:
        fh.write("\n".join(log) + "\n")

    if not args.keep_worktree:
        run(["git", "worktree", "remove", "--force", str(target)], repo)
        shutil.rmtree(worktree, ignore_errors=True)

    print(f"[verify-gate] commit={sha[:12]} failed={len(failed)} out={out_path}")
    if failed:
        print("[verify-gate] 未通过项：" + "、".join(failed))
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
