#!/usr/bin/env python3
"""Gitee 官方 MCP Server 客户端：调用 MCP 工具并归档原始请求与响应。

共同要求第 8 节要求「通过 MCP 创建独立仓库，并保留工具名称、请求与结果、时间、仓库 URL」。
本脚本直接以 **MCP 协议（JSON-RPC over Streamable HTTP）** 调用 Gitee 官方远程 MCP Server
（`https://api.gitee.com/mcp`，服务端标识 `gitee-mcp-remote`），而不是直接调 Gitee REST API，
因此调用记录中的工具名、请求体与返回体都是 MCP 层的真实报文。

每一次调用都会在 `evidence/gitee-mcp/` 下归档一份原始报文 JSON，并在
`evidence/gitee-mcp/calls.jsonl` 追加一行调用台账（工具名、时间、请求、结果摘要）。

用法：
    python scripts/gitee_mcp.py --list-tools
    python scripts/gitee_mcp.py --call create_pull --args-file args.json
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone, timedelta
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

CST = timezone(timedelta(hours=8))
DEFAULT_ENDPOINT = "https://api.gitee.com/mcp"
DEFAULT_API_BASE = "https://gitee.com/api/v5"
OUT_DIR = Path("evidence/gitee-mcp")


def post(endpoint: str, token: str, payload: dict, session_id: str | None) -> tuple[dict, dict]:
    """发一次 JSON-RPC 请求，返回 (解析后的 body, 响应头)。"""
    data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    headers = {
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
        "User-Agent": "mcode-gitee-mcp-client/1.0",
    }
    if session_id:
        headers["Mcp-Session-Id"] = session_id
    req = urllib.request.Request(endpoint, data=data, headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=90) as resp:
        raw = resp.read().decode("utf-8", "replace")
        resp_headers = {k: v for k, v in resp.headers.items()}
    body = parse_body(raw)
    return body, resp_headers


def parse_body(raw: str) -> dict:
    """兼容纯 JSON 与 SSE（text/event-stream）两种返回形态。"""
    raw = raw.strip()
    if not raw:
        return {}
    if raw.startswith("{"):
        try:
            return json.loads(raw)
        except json.JSONDecodeError:
            pass
    for line in raw.splitlines():
        if line.startswith("data:"):
            chunk = line[5:].strip()
            if chunk and chunk != "[DONE]":
                try:
                    return json.loads(chunk)
                except json.JSONDecodeError:
                    continue
    return {"_raw": raw}


def handshake(endpoint: str, token: str) -> tuple[str | None, dict]:
    """MCP 初始化握手，返回 (session_id, initialize 响应)。"""
    init = {"jsonrpc": "2.0", "id": 1, "method": "initialize",
            "params": {"protocolVersion": "2024-11-05", "capabilities": {},
                       "clientInfo": {"name": "mcode-gitee-mcp-client", "version": "1.0"}}}
    body, headers = post(endpoint, token, init, None)
    sid = headers.get("Mcp-Session-Id") or headers.get("mcp-session-id")
    # 通知：initialized（服务端对通知一般返回 202 且无正文，这里忽略失败）
    try:
        post(endpoint, token, {"jsonrpc": "2.0", "method": "notifications/initialized"}, sid)
    except (urllib.error.HTTPError, urllib.error.URLError, OSError):
        pass
    return sid, body


def safe_name(tool: str) -> str:
    """把工具名转成安全的文件名（tools/list 之类含斜杠）。"""
    return tool.replace("/", "_").replace("\\", "_").replace(":", "_")


def archive(tool: str, request: dict, response: dict, endpoint: str,
            session_id: str | None, elapsed_ms: int, extra: dict | None = None) -> Path:
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    now = datetime.now(CST)
    payload = {
        "mcp_server": {"endpoint": endpoint, "server": "gitee-mcp-remote",
                       "protocol": "MCP JSON-RPC over Streamable HTTP"},
        "tool": tool,
        "called_at": now.isoformat(),
        "elapsed_ms": elapsed_ms,
        "mcp_session_id": session_id,
        "request": request,
        "response": response,
    }
    if extra:
        payload.update(extra)
    path = OUT_DIR / f"{now.strftime('%Y%m%d-%H%M%S')}-{safe_name(tool)}.json"
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n",
                    encoding="utf-8", newline="\n")
    ledger = OUT_DIR / "calls.jsonl"
    with open(ledger, "a", encoding="utf-8", newline="\n") as fh:
        fh.write(json.dumps({
            "time": now.isoformat(),
            "tool": tool,
            "request": request,
            "result_is_error": bool(isinstance(response.get("result"), dict)
                                    and response["result"].get("isError")),
            "elapsed_ms": elapsed_ms,
            "archive": path.name,
        }, ensure_ascii=False) + "\n")
    return path


def main() -> int:
    ap = argparse.ArgumentParser(description="调用 Gitee 官方 MCP Server 并归档原始报文")
    ap.add_argument("--endpoint", default=os.environ.get("GITEE_MCP_ENDPOINT", DEFAULT_ENDPOINT))
    ap.add_argument("--token", default=os.environ.get("GITEE_ACCESS_TOKEN", ""))
    ap.add_argument("--list-tools", action="store_true", help="列出 MCP 工具清单")
    ap.add_argument("--call", help="调用的 MCP 工具名")
    ap.add_argument("--args-file", help="工具参数 JSON 文件")
    ap.add_argument("--args", help="工具参数 JSON 字符串")
    args = ap.parse_args()

    if not args.token:
        print("缺少令牌：请设置环境变量 GITEE_ACCESS_TOKEN，或用 --token 传入。")
        return 2

    sid, init_body = handshake(args.endpoint, args.token)
    print(f"[mcp] initialized session={sid} server="
          f"{(init_body.get('result') or {}).get('serverInfo', {})}")

    if args.list_tools:
        body, _ = post(args.endpoint, args.token,
                       {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}}, sid)
        tools = (body.get("result") or {}).get("tools", [])
        path = archive("tools/list", {"jsonrpc": "2.0", "id": 2, "method": "tools/list",
                                      "params": {}}, body, args.endpoint, sid, 0,
                       extra={"tool_count": len(tools)})
        for t in tools:
            print(f"  - {t.get('name')}: {(t.get('description') or '')[:90]}")
        print(f"[mcp] {len(tools)} tools -> {path}")
        return 0

    if not args.call:
        print("请指定 --list-tools 或 --call <tool>")
        return 2

    if args.args_file:
        tool_args = json.loads(Path(args.args_file).read_text(encoding="utf-8-sig"))
    elif args.args:
        tool_args = json.loads(args.args)
    else:
        tool_args = {}

    request = {"jsonrpc": "2.0", "id": 3, "method": "tools/call",
               "params": {"name": args.call, "arguments": tool_args}}
    start = time.time()
    body, _ = post(args.endpoint, args.token, request, sid)
    elapsed = int((time.time() - start) * 1000)
    path = archive(args.call, request, body, args.endpoint, sid, elapsed)

    result = body.get("result") or {}
    if result.get("isError"):
        print(f"[mcp] 工具 {args.call} 返回错误：{json.dumps(body, ensure_ascii=False)[:800]}")
        print(f"[mcp] 原始报文 -> {path}")
        return 1
    print(f"[mcp] 工具 {args.call} 调用成功（{elapsed} ms）")
    print(json.dumps(result, ensure_ascii=False)[:2000])
    print(f"[mcp] 原始报文 -> {path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
