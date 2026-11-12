#!/usr/bin/env python3
"""运行业务冒烟并归档原始输出到 evidence/smoke-test.log。

为什么需要这个包装器：
  · `scripts/smoke-test.sh` 只把断言打到 stdout，证据归档需要**带头部**的完整日志
    （执行时间、被测地址、退出码、结果统计），否则无法判断归档的是哪一次运行；
  · Windows 上 `bash` 默认指向 WSL（可能不可用），需要自动挑选 Git Bash；
  · 归档必须是 UTF-8 无 BOM + LF（仓库行尾约定），不能依赖 PowerShell 的 `Out-File`。

用法：
    python scripts/run-smoke.py [--base-url http://127.0.0.1:8080] [--out evidence/smoke-test.log]
退出码：0 = 冒烟全部通过；1 = 有失败项；2 = 执行错误（找不到 bash / 脚本缺失）。
"""
from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
from datetime import datetime, timezone, timedelta
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

CST = timezone(timedelta(hours=8))
REPO = Path(__file__).resolve().parent.parent


def find_bash() -> str | None:
    """优先用 PATH 里的 bash；Windows 上再退到 Git Bash。"""
    found = shutil.which("bash")
    if found:
        # WSL 的 bash 可能不可用（网络与挂载异常），先探测一次能否执行。
        # 探测用**字节模式**：WSL 启动时可能打印 UTF-16 编码的提示横幅，
        # 文本模式会按本机 GBK 解码并抛 UnicodeDecodeError。
        try:
            probe = subprocess.run([found, "-c", "echo ok"], capture_output=True, timeout=20)
            out = (probe.stdout or b"").decode("utf-8", errors="replace")
            if probe.returncode == 0 and "ok" in out:
                return found
        except (OSError, subprocess.SubprocessError):
            pass
    if os.name == "nt":
        for cand in (r"C:\Program Files\Git\bin\bash.exe",
                     r"C:\Program Files\Git\usr\bin\bash.exe"):
            if Path(cand).is_file():
                return cand
    return None


def main() -> int:
    ap = argparse.ArgumentParser(description="运行业务冒烟并归档原始输出")
    ap.add_argument("--base-url", default=os.environ.get("BASE_URL", "http://127.0.0.1:8080"))
    ap.add_argument("--out", default="evidence/smoke-test.log")
    ap.add_argument("--script", default="scripts/smoke-test.sh")
    args = ap.parse_args()

    script = REPO / args.script
    if not script.is_file():
        print(f"找不到冒烟脚本：{script}", file=sys.stderr)
        return 2
    bash = find_bash()
    if not bash:
        print("找不到可用的 bash（PATH 与 Git Bash 均未命中）。", file=sys.stderr)
        return 2

    env = dict(os.environ)
    env["BASE_URL"] = args.base_url
    env["PYTHONIOENCODING"] = "utf-8"
    started = datetime.now(CST)
    # 用字节模式捕获后自行解码：Windows 上子进程的默认编码可能是 GBK，
    # 直接交给 text=True 会在管道解码线程里抛 UnicodeDecodeError。
    proc = subprocess.run([bash, str(script)], cwd=str(REPO), env=env,
                          capture_output=True, timeout=1800)
    ended = datetime.now(CST)

    def decode(raw: bytes) -> str:
        for enc in ("utf-8", "gbk"):
            try:
                return raw.decode(enc)
            except UnicodeDecodeError:
                continue
        return raw.decode("utf-8", errors="replace")

    body = decode(proc.stdout or b"") + decode(proc.stderr or b"")
    passed = body.count("[PASS]")
    failed = body.count("[FAIL]")

    out_path = Path(args.out)
    if not out_path.is_absolute():
        out_path = REPO / out_path
    out_path.parent.mkdir(parents=True, exist_ok=True)

    header = [
        "题目 05 业务冒烟归档",
        f"执行时间：{started.isoformat()} ~ {ended.isoformat()}",
        f"被测地址：{args.base_url}",
        f"执行命令：{bash} {args.script}",
        f"退出码：{proc.returncode}",
        f"断言结果：通过 {passed} 项，失败 {failed} 项",
        "-" * 78,
        "",
    ]
    out_path.write_text("\n".join(header) + body.rstrip() + "\n", encoding="utf-8", newline="\n")
    print(f"[run-smoke] exit={proc.returncode} pass={passed} fail={failed} out={out_path}")
    if proc.returncode != 0:
        sys.stdout.write(body[-2000:])
    return proc.returncode


if __name__ == "__main__":
    raise SystemExit(main())
