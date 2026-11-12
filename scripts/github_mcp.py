#!/usr/bin/env python3
"""GitHub 官方 MCP Server 客户端（stdio 传输）：调用 MCP 工具并归档原始报文。

共同要求第 8 节第 1 条要求「通过 GitHub MCP 创建独立仓库，保留工具名称、请求与结果、
时间、仓库 URL」。本脚本以 **MCP 协议（JSON-RPC over stdio）** 驱动官方
`ghcr.io/github/github-mcp-server` 镜像，归档每一次调用的**原始 JSON-RPC 报文**，
因此「用什么工具、传了什么、返回了什么、什么时候调的」都可逐条复核。

令牌只经环境变量 `GITHUB_PERSONAL_ACCESS_TOKEN` 传入容器，**不写入仓库任何文件**。

用法：
    python scripts/github_mcp.py --list-tools
    python scripts/github_mcp.py --call create_repository --args-file args.json

退出码：0 = 调用成功；1 = 工具返回错误；2 = 执行错误。
"""
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import time
from datetime import datetime, timezone, timedelta
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

CST = timezone(timedelta(hours=8))
IMAGE = "ghcr.io/github/github-mcp-server:latest"
OUT_DIR = Path("evidence/github-mcp")
TOOLSETS = "repos,pull_requests,issues,actions,code_security,secret_protection"


class MCPClient:
    """极简 MCP stdio 客户端：只实现本脚本需要的 initialize / tools/list / tools/call。"""

    def __init__(self, token: str, toolsets: str = TOOLSETS) -> None:
        self.proc = subprocess.Popen(
            ["docker", "run", "-i", "--rm",
             "-e", "GITHUB_PERSONAL_ACCESS_TOKEN",
             "-e", "GITHUB_TOOLSETS",
             IMAGE, "stdio"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            env={**os.environ, "GITHUB_PERSONAL_ACCESS_TOKEN": token,
                 "GITHUB_TOOLSETS": toolsets},
            text=True, encoding="utf-8", errors="replace", bufsize=1)
        self.next_id = 0
        self.server_info: dict = {}

    def send(self, payload: dict) -> dict:
        assert self.proc.stdin and self.proc.stdout
        self.proc.stdin.write(json.dumps(payload, ensure_ascii=False) + "\n")
        self.proc.stdin.flush()
        while True:
            line = self.proc.stdout.readline()
            if not line:
                err = self.proc.stderr.read() if self.proc.stderr else ""
                raise RuntimeError(f"MCP 服务未返回内容，stderr: {err[:800]}")
            line = line.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except json.JSONDecodeError:
                continue  # 服务端日志行
            if "id" in msg and msg["id"] == payload.get("id"):
                return msg

    def notify(self, method: str, params: dict | None = None) -> None:
        assert self.proc.stdin
        self.proc.stdin.write(json.dumps(
            {"jsonrpc": "2.0", "method": method, "params": params or {}},
            ensure_ascii=False) + "\n")
        self.proc.stdin.flush()

    def request(self, method: str, params: dict | None = None) -> dict:
        self.next_id += 1
        payload = {"jsonrpc": "2.0", "id": self.next_id, "method": method,
                   "params": params or {}}
        return self.send(payload)

    def initialize(self) -> dict:
        resp = self.request("initialize", {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "clientInfo": {"name": "mcode-github-mcp-client", "version": "1.0"},
        })
        self.server_info = (resp.get("result") or {}).get("serverInfo", {})
        self.notify("notifications/initialized")
        return resp

    def list_tools(self) -> list[dict]:
        resp = self.request("tools/list", {})
        return (resp.get("result") or {}).get("tools", [])

    def call(self, name: str, arguments: dict) -> dict:
        return self.request("tools/call", {"name": name, "arguments": arguments})

    def close(self) -> None:
        try:
            if self.proc.stdin:
                self.proc.stdin.close()
            self.proc.wait(timeout=10)
        except Exception:
            self.proc.kill()


def safe_name(tool: str) -> str:
    return tool.replace("/", "_").replace("\\", "_").replace(":", "_")


def archive(tool: str, request: dict, response: dict, server_info: dict,
            elapsed_ms: int, extra: dict | None = None) -> Path:
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    now = datetime.now(CST)
    payload = {
        "mcp_server": {
            "image": IMAGE,
            "transport": "stdio (docker run -i --rm ... stdio)",
            "protocol": "MCP JSON-RPC 2.0",
            "serverInfo": server_info,
        },
        "tool": tool,
        "called_at": now.isoformat(),
        "elapsed_ms": elapsed_ms,
        "request": request,
        "response": response,
    }
    if extra:
        payload.update(extra)
    path = OUT_DIR / f"{now.strftime('%Y%m%d-%H%M%S')}-{safe_name(tool)}.json"
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        fh.write(json.dumps(payload, ensure_ascii=False, indent=2) + "\n")
    with open(OUT_DIR / "calls.jsonl", "a", encoding="utf-8", newline="\n") as fh:
        fh.write(json.dumps({
            "time": now.isoformat(),
            "tool": tool,
            "is_error": bool(isinstance(response.get("result"), dict)
                             and response["result"].get("isError")),
            "elapsed_ms": elapsed_ms,
            "archive": path.name,
            "request": request,
        }, ensure_ascii=False) + "\n")
    return path


def main() -> int:
    ap = argparse.ArgumentParser(description="调用 GitHub 官方 MCP Server 并归档原始报文")
    ap.add_argument("--token", default=os.environ.get("GITHUB_PERSONAL_ACCESS_TOKEN", ""))
    ap.add_argument("--list-tools", action="store_true")
    ap.add_argument("--call", help="要调用的 MCP 工具名")
    ap.add_argument("--args-file", help="工具参数 JSON 文件")
    ap.add_argument("--args", help="工具参数 JSON 字符串")
    ap.add_argument("--toolset", default=TOOLSETS)
    args = ap.parse_args()

    if not args.token:
        print("缺少令牌：请设置环境变量 GITHUB_PERSONAL_ACCESS_TOKEN。")
        return 2

    client = MCPClient(args.token, args.toolset)
    try:
        init = client.initialize()
        print(f"[mcp] initialized server={client.server_info}")

        if args.list_tools:
            tools = client.list_tools()
            request = {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}}
            path = archive("tools/list", request,
                           {"jsonrpc": "2.0", "id": 2,
                            "result": {"tools": [{"name": t.get("name"),
                                                  "description": t.get("description")}
                                                 for t in tools]}},
                           client.server_info, 0, extra={"tool_count": len(tools)})
            for t in tools:
                print(f"  - {t.get('name')}: {(t.get('description') or '')[:88]}")
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

        t0 = time.time()
        response = client.call(args.call, tool_args)
        elapsed = int((time.time() - t0) * 1000)
        request = {"jsonrpc": "2.0", "id": client.next_id, "method": "tools/call",
                   "params": {"name": args.call, "arguments": tool_args}}
        path = archive(args.call, request, response, client.server_info, elapsed)

        result = response.get("result") or {}
        rpc_error = response.get("error")
        if rpc_error:
            # JSON-RPC 层错误（如工具名不在已启用的 toolsets 内）同样是失败，
            # 不能因为没有 result.isError 就当作成功。
            print(f"[mcp] 工具 {args.call} 返回 JSON-RPC 错误："
                  f"{rpc_error.get('code')} {rpc_error.get('message')}")
            print(f"[mcp] 原始报文 -> {path}")
            return 1
        if result.get("isError"):
            print(f"[mcp] 工具 {args.call} 返回错误：")
            print(json.dumps(response, ensure_ascii=False)[:1500])
            print(f"[mcp] 原始报文 -> {path}")
            return 1
        print(f"[mcp] 工具 {args.call} 调用成功（{elapsed} ms）")
        print(json.dumps(result, ensure_ascii=False)[:2500])
        print(f"[mcp] 原始报文 -> {path}")
        return 0
    finally:
        client.close()


if __name__ == "__main__":
    raise SystemExit(main())
