#!/usr/bin/env python3
"""GitHub 官方 MCP Server 所用 OAuth 应用的「设备码」授权流程。

背景
----
共同要求第 8 节第 1 条要求「通过 GitHub MCP 创建独立仓库」。官方镜像
`ghcr.io/github/github-mcp-server` 的 stdio 模式在没有 `GITHUB_PERSONAL_ACCESS_TOKEN`
时，支持用 GitHub OAuth App 做交互式登录（`--oauth-client-id`）；其交互动作就是
GitHub 的**设备码流程**（device flow）。本脚本实现同一套 OAuth 交换，把拿到的访问
令牌交给 MCP Server 使用，使「建仓动作本身」仍然由 GitHub 官方 MCP Server 的
`create_repository` 工具完成。

安全约定
--------
* 令牌**只写本机文件**（默认 `C:\\Users\\26765\\.minimax\\github_oauth_token.txt`），
  不进仓库、不进证据归档、不回显到终端、不进命令行参数；
* 仓库内只保留本脚本与「本次授权的元数据」（client_id、scope、授权时间、令牌指纹前 8 位）；
* 令牌用完可自行删除该文件。

用法
----
    python scripts/github_oauth_device.py                 # 申请设备码并等待授权
    python scripts/github_oauth_device.py --scope repo read:user

授权完成后，令牌从环境变量注入后续链路：

    $env:GITHUB_PERSONAL_ACCESS_TOKEN = (Get-Content <令牌文件> -Raw).Trim()
    python <audit>\\evidence\\github_chain.py --repo . --owner <owner> --name <repo>

退出码：0 = 授权成功并已落盘；1 = 授权被拒绝/超时；2 = 执行错误。
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone, timedelta
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

CST = timezone(timedelta(hours=8))

# GitHub 官方 MCP Server 使用的公共 OAuth 客户端（非机密凭据，可公开分发）。
DEFAULT_CLIENT_ID = "Iv1.b507a08c87ecfe98"
DEFAULT_TOKEN_FILE = Path(r"C:\Users\26765\.minimax\github_oauth_token.txt")
DEVICE_CODE_URL = "https://github.com/login/device/code"
ACCESS_TOKEN_URL = "https://github.com/login/oauth/access_token"
GRANT_TYPE = "urn:ietf:params:oauth:grant-type:device_code"


def _opener() -> urllib.request.OpenerDirector:
    """沿用环境变量里的代理设置（GitHub 访问需要本机代理时由 HTTPS_PROXY 指定）。"""
    return urllib.request.build_opener()


def _post(url: str, data: dict[str, str]) -> tuple[int, dict]:
    body = urllib.parse.urlencode(data).encode("utf-8")
    req = urllib.request.Request(
        url, data=body, method="POST",
        headers={"Accept": "application/json",
                 "Content-Type": "application/x-www-form-urlencoded",
                 "User-Agent": "mcode-github-oauth-device/1.0"})
    try:
        with _opener().open(req, timeout=60) as resp:
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


def request_device_code(client_id: str, scope: str) -> dict:
    code, data = _post(DEVICE_CODE_URL, {"client_id": client_id, "scope": scope})
    if code != 200 or "device_code" not in data:
        print(f"[oauth] 申请设备码失败（HTTP {code}）：{json.dumps(data, ensure_ascii=False)[:400]}",
              file=sys.stderr)
        return {}
    return data


def poll_for_token(client_id: str, device_code: str, interval: int,
                   timeout: int) -> tuple[str, dict]:
    deadline = time.time() + timeout
    last: dict = {}
    while time.time() < deadline:
        code, data = _post(ACCESS_TOKEN_URL, {
            "client_id": client_id,
            "device_code": device_code,
            "grant_type": GRANT_TYPE,
        })
        last = {"http_status": code, **data}
        if code == 200 and data.get("access_token"):
            return data["access_token"], last
        err = data.get("error", "")
        if err in ("authorization_pending", "slow_down"):
            if err == "slow_down":
                interval += 5
            time.sleep(interval)
            continue
        if err == "expired_token":
            print("[oauth] 设备码已过期，请重新运行脚本。", file=sys.stderr)
            return "", last
        if err == "access_denied":
            print("[oauth] 用户拒绝了授权。", file=sys.stderr)
            return "", last
        print(f"[oauth] 授权失败（HTTP {code}）：{json.dumps(data, ensure_ascii=False)[:400]}",
              file=sys.stderr)
        return "", last
    print("[oauth] 等待授权超时。", file=sys.stderr)
    return "", last


def main() -> int:
    ap = argparse.ArgumentParser(description="GitHub MCP 设备码授权")
    ap.add_argument("--client-id", default=DEFAULT_CLIENT_ID,
                    help="GitHub 官方 MCP Server 的 OAuth 客户端 ID")
    ap.add_argument("--scope", default="repo read:user",
                    help="申请的权限范围（repo 覆盖建仓与分支保护）")
    ap.add_argument("--out", default=str(DEFAULT_TOKEN_FILE), help="令牌落盘位置")
    ap.add_argument("--meta-out", default="", help="授权元数据归档位置（不含令牌）")
    ap.add_argument("--timeout", type=int, default=900, help="等待授权的最长秒数")
    ap.add_argument("--print-code-only", action="store_true",
                    help="只申请设备码并打印，不等待（便于人工分步操作）")
    args = ap.parse_args()

    device = request_device_code(args.client_id, args.scope)
    if not device:
        return 2
    user_code = device.get("user_code", "")
    uri = device.get("verification_uri", "https://github.com/login/device")
    interval = int(device.get("interval", 5))
    expires_in = int(device.get("expires_in", 900))

    print("=" * 72)
    print("[oauth] 已申请 GitHub 设备码（GitHub 官方 MCP Server 公共客户端）")
    print(f"[oauth] 请在浏览器打开：{uri}")
    print(f"[oauth] 输入验证码：{user_code}")
    print(f"[oauth] 有效期 {expires_in}s；请在 GitHub 上确认授权「GitHub MCP Server」。")
    print("=" * 72, flush=True)

    if args.print_code_only:
        print(json.dumps({"client_id": args.client_id, "scope": args.scope,
                          "user_code": user_code, "verification_uri": uri,
                          "device_code": device.get("device_code", "")},
                         ensure_ascii=False, indent=2))
        return 0

    token, last = poll_for_token(args.client_id, device.get("device_code", ""),
                                 interval, args.timeout)
    if not token:
        return 1

    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(token, encoding="utf-8", newline="")
    try:
        os.chmod(out, 0o600)
    except OSError:
        pass

    fingerprint = hashlib.sha256(token.encode("utf-8")).hexdigest()[:8]
    meta = {
        "authorized_at": datetime.now(CST).isoformat(),
        "method": "GitHub OAuth 设备码流程（GitHub 官方 MCP Server 公共客户端）",
        "client_id": args.client_id,
        "scope": args.scope,
        "verification_uri": uri,
        "token_file": str(out),
        "token_length": len(token),
        "token_prefix": token.split("_")[0] + "_",
        "token_sha256_prefix": fingerprint,
        "token_scope_granted": last.get("scope", ""),
        "note": "令牌本身不写入本文件，仅记录指纹前 8 位与授权范围",
    }
    if args.meta_out:
        meta_path = Path(args.meta_out)
        meta_path.parent.mkdir(parents=True, exist_ok=True)
        meta_path.write_text(json.dumps(meta, ensure_ascii=False, indent=2) + "\n",
                             encoding="utf-8", newline="\n")
    print(f"[oauth] 授权成功：令牌已写入 {out}")
    print(f"[oauth] 令牌长度 {len(token)}，前缀 {meta['token_prefix']}，"
          f"指纹 {fingerprint}，授权范围 {meta['token_scope_granted'] or args.scope}")
    if args.meta_out:
        print(f"[oauth] 授权元数据归档：{args.meta_out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
