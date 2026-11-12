#!/usr/bin/env python3
"""`scripts/verify-delivery.py` 的单元测试（自编轻量测试框架，不引入第三方依赖）。

覆盖三条判定逻辑：
  1. 提交消息格式与编号必填规则（`[类型] [版本号] [#编号] 描述`）；
  2. 令牌/凭据扫描的命中与豁免；
  3. 会话导出脱敏规则（一次性设备码、独立 32 位十六进制串）。

用法：
    python scripts/test-delivery-checks.py
退出码：0 = 全部用例通过；1 = 有用例失败。
"""
from __future__ import annotations

import importlib.util
import sys
import tempfile
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

SCRIPT = Path(__file__).with_name("verify-delivery.py")
spec = importlib.util.spec_from_file_location("verify_delivery", SCRIPT)
vd = importlib.util.module_from_spec(spec)
assert spec.loader is not None
spec.loader.exec_module(vd)

FAILED: list[str] = []
PASSED = 0


def check(name: str, actual, expected) -> None:
    global PASSED
    if actual == expected:
        PASSED += 1
        print(f"  PASS  {name}")
    else:
        FAILED.append(name)
        print(f"  FAIL  {name}：期望 {expected!r}，实际 {actual!r}")


def test_message_regex() -> None:
    print("[用例组] 提交消息格式")
    cases = [
        ("[feat] [v2.4.0] [#16221249] 实现远端交付链路", ("feat", "v2.4.0", "16221249")),
        ("[fix] [v2.3.0] [#16221411] 修正会话导出未脱敏的问题", ("fix", "v2.3.0", "16221411")),
        ("[opt] [v2.3.0] 增加自编 Skill、CI 流水线与部署脚本", ("opt", "v2.3.0", None)),
        ("[docs] [v2.4.0] 升级项目约定至 v2.0", ("docs", "v2.4.0", None)),
        ("[feat] [v2.4.0] 缺少编号的 feat", ("feat", "v2.4.0", None)),
        ("[feat] [#16221249] 缺少版本号", ("feat", None, "16221249")),
        ("feat [v2.4.0] [#16221249] 类型未加方括号", None),
        ("[feat] [v2.4.0][#16221249] 缺少分隔空格", None),
        ("[chore] [v2.4.0] 非法类型", None),
    ]
    for subject, expected in cases:
        parsed = vd.parse_message(subject)
        got = None if parsed is None else parsed
        check(f"解析：{subject}", got, expected)


def test_secret_scan() -> None:
    print("[用例组] 令牌扫描")
    with tempfile.TemporaryDirectory() as tmp:
        repo = Path(tmp)
        (repo / "evidence").mkdir()
        (repo / "evidence" / "ok.md").write_text(
            "会话 ID mvs_9c316f3355324f0f81b11d1998e049c3 与 client id cli_6c1cee5056d04561827106d4cb1a2e9e\n",
            encoding="utf-8")
        (repo / "evidence" / "bad.md").write_text(
            "私人令牌是 20ffb6b0ac625c917ecb7ca8b1d37746\n", encoding="utf-8")
        hits = []
        for path in vd.iter_repo_files(repo):
            text = path.read_text(encoding="utf-8", errors="replace")
            rel = path.relative_to(repo).as_posix()
            for line in text.splitlines():
                if line.lstrip().startswith("#"):
                    continue
                for m in vd.HEX32_RE.finditer(line):
                    ctx = line[max(0, m.start() - 40):m.start()]
                    if vd.SAFE_HEX32_CONTEXT.search(ctx):
                        continue
                    hits.append(f"{rel}:{m.group(0)}")
        check("会话 ID / client id 不应判为令牌", hits, ["evidence/bad.md:20ffb6b0ac625c917ecb7ca8b1d37746"])

    check("令牌赋值模式命中",
          bool(vd.SECRET_ASSIGN_RE.search("access_token = abcdefghijklmnopqrstuvwxyz012345")), True)
    check("Bearer 模式命中",
          bool(vd.BEARER_RE.search("Authorization: Bearer abcdefghijklmnopqrstuvwxyz0123")), True)
    check("普通文本不命中 Bearer",
          bool(vd.BEARER_RE.search("bearer of the token idea")), False)


def test_redaction() -> None:
    print("[用例组] 会话导出脱敏")
    ex = importlib.util.spec_from_file_location("export_session", Path(__file__).with_name("export_session.py"))
    es = importlib.util.module_from_spec(ex)
    assert ex.loader is not None
    ex.loader.exec_module(es)

    masked = es.redact("--device-code-value '27c9bbff-017c-4a24-88c3-5d34e72b1639' --host meegle.com")
    check("设备码被掩码", "27c9bbff" not in masked, True)
    check("设备码标记保留", "已掩码" in masked, True)

    token = es.redact("私人令牌是 20ffb6b0ac625c917ecb7ca8b1d37746")
    check("独立 32 位十六进制串被掩码", "20ffb6b0ac625c917ecb7ca8b1d37746" not in token, True)

    keep = es.redact("会话 mvs_9c316f3355324f0f81b11d1998e049c3 与 cli_6c1cee5056d04561827106d4cb1a2e9e")
    check("mvs_ 前缀标识符保留", "9c316f3355324f0f81b11d1998e049c3" in keep, True)
    check("cli_ 前缀标识符保留", "6c1cee5056d04561827106d4cb1a2e9e" in keep, True)


def test_merge_status() -> None:
    print("[用例组] 合并状态判定")
    import json
    import subprocess
    with tempfile.TemporaryDirectory() as tmp:
        repo = Path(tmp)
        subprocess.run(["git", "init", "-q", "-b", "main", str(repo)], check=True)
        subprocess.run(["git", "-C", str(repo), "config", "user.email", "t@example.com"], check=True)
        subprocess.run(["git", "-C", str(repo), "config", "user.name", "t"], check=True)
        (repo / "a.txt").write_text("x", encoding="utf-8")
        subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
        subprocess.run(["git", "-C", str(repo), "commit", "-q", "-m", "[docs] [v2.4.0] 初始化"], check=True)
        subprocess.run(["git", "-C", str(repo), "branch", "dev/05"], check=True)
        res = vd.check_merge_and_gate(repo)
        check("未合并时状态为 PENDING", res[0]["status"], "PENDING")
        check("未合并时 D-07 为 PENDING", res[1]["status"], "PENDING")


def main() -> int:
    print("交付链路自检脚本 · 单元测试")
    test_message_regex()
    test_secret_scan()
    test_redaction()
    test_merge_status()
    print("")
    print(f"合计：PASS {PASSED} / FAIL {len(FAILED)}")
    if FAILED:
        for f in FAILED:
            print(f"  失败用例：{f}")
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
