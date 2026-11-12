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
    # 合成夹具：32 位十六进制串在运行时拼接，仓库内不保留任何真实或形似凭据的字面量
    fake_hex32 = ("a1b2c3d4" * 3) + "abcdef12"
    assert len(fake_hex32) == 32
    with tempfile.TemporaryDirectory() as tmp:
        repo = Path(tmp)
        (repo / "evidence").mkdir()
        (repo / "evidence" / "ok.md").write_text(
            "会话 ID mvs_9c316f3355324f0f81b11d1998e049c3 与 client id cli_6c1cee5056d04561827106d4cb1a2e9e\n",
            encoding="utf-8")
        (repo / "evidence" / "bad.md").write_text(
            "私人令牌是 " + fake_hex32 + "\n", encoding="utf-8")
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
        check("会话 ID / client id 不应判为令牌", hits, ["evidence/bad.md:" + fake_hex32])

    # 测试夹具在运行时拼接，避免仓库内出现任何「凭据形态」的字面量
    fake_value = "x" * 32
    fake_bearer = "y" * 30
    check("令牌赋值模式命中",
          bool(vd.SECRET_ASSIGN_RE.search("access_token = " + fake_value)), True)
    check("Bearer 模式命中",
          bool(vd.BEARER_RE.search("Authorization: Bearer " + fake_bearer)), True)
    check("普通文本不命中 Bearer",
          bool(vd.BEARER_RE.search("bearer of the token idea")), False)


def test_redaction() -> None:
    print("[用例组] 会话导出脱敏")
    ex = importlib.util.spec_from_file_location("export_session", Path(__file__).with_name("export_session.py"))
    es = importlib.util.module_from_spec(ex)
    assert ex.loader is not None
    ex.loader.exec_module(es)

    # 夹具一律运行时拼接：仓库内不保留任何连续的「凭据形态」字面量。
    fake_device_code = "27c9bbff" + "-017c-4a24-88c3-5d34e72b1639"
    masked = es.redact("--device-code-value '" + fake_device_code + "' --host meegle.com")
    check("设备码被掩码", "27c9bbff" not in masked, True)
    check("设备码标记保留", "已掩码" in masked, True)

    fake_hex32 = ("a1b2c3d4" * 3) + "abcdef12"
    token = es.redact("私人令牌是 " + fake_hex32)
    check("独立 32 位十六进制串被掩码", fake_hex32 not in token, True)

    # 公开标识符（会话 ID / OAuth client_id）：前缀 + 32 位十六进制，按前缀判定不被掩码。
    sid_body = "9c316f33" + "55324f0f" + "81b11d19" + "98e049c3"
    cid_body = "6c1cee50" + "56d04561" + "827106d4" + "cb1a2e9e"
    keep = es.redact("会话 mvs_" + sid_body + " 与 cli_" + cid_body)
    check("mvs_ 前缀标识符保留", sid_body in keep, True)
    check("cli_ 前缀标识符保留", cid_body in keep, True)

    # 授权头形态（历史会话中出现过测试夹具串，导出件不得保留任何凭据形态）
    fake_bearer = "y" * 30
    masked_bearer = es.redact("Authorization: Bearer " + fake_bearer)
    check("Bearer 形态凭据被掩码", fake_bearer not in masked_bearer, True)
    check("Bearer 掩码标记保留", "已掩码" in masked_bearer, True)
    check("普通文本不误掩码", es.redact("bearer of the token idea"), "bearer of the token idea")

    # JSON 形态的一次性设备码 / 用户码（Meegle 设备码授权 init 阶段返回体）
    fake_device = "27c9bbff" + "-017c-4a24-88c3-5d34e72b1639"
    json_device = es.redact('{"client_id": "cli_x", "device_code": "' + fake_device + '"}')
    check("JSON 形态设备码被掩码", fake_device not in json_device, True)
    check("JSON 形态设备码标记保留", "已掩码" in json_device, True)
    fake_user = "4GQS7" + "-G5TWK"
    json_user = es.redact('{"device_code": "' + fake_device + '", "user_code": "' + fake_user + '"}')
    check("JSON 形态用户码被掩码", fake_user not in json_user, True)

    # 摘要/提交号占位符不应被 32 位规则误伤（与 verify-delivery.py 同一口径）
    digest = ("a1b2c3d4" * 3) + "abcdef12"
    keep_digest = es.redact("sha256: " + digest)
    check("SHA-256 占位符不被误伤", digest in keep_digest, True)

    # repr 形态（引号被转义）与截断形态的设备码也必须被掩码
    repr_form = es.redact("--device-code-value \\'" + fake_device + "\\' --host meegle.com")
    check("repr 形态设备码被掩码", fake_device not in repr_form, True)
    truncated = es.redact("--device-code-value '" + fake_device + "\\r\\n next")
    check("截断形态设备码被掩码", fake_device not in truncated, True)
    repr_json = es.redact('{\\"device_code\\": \\"' + fake_device + '\\", \\"expires_in\\": 1800}')
    check("repr 形态 JSON 设备码被掩码", fake_device not in repr_json, True)

    # UUID v4 形态的一次性凭据：会话自身标识符（turn_/msg-/call-）必须保留
    uuid = "27c9bbff-017c-4a24-88c3-5d34e72b1639"
    check("裸 UUID 一次性凭据被掩码", uuid not in es.redact("device code = " + uuid), True)
    turn = "1f3f3a8b-45c5-4d63-916d-0e8f9a2cb83c"
    check("turn_ 会话标识符保留", turn in es.redact("turn_" + turn), True)


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
