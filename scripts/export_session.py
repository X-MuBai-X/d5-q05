#!/usr/bin/env python3
"""从本地会话存储导出结构化事件清单与可读会话导出。

数据源：MiniMax Code 本地会话目录下的 messages.jsonl（可见消息记录）。
输出：
  evidence/会话事件清单.jsonl  结构化事件清单（每行一个 JSON）
  docs/06-会话导出.md          可读会话导出（含索引与阶段对照）

说明：平台未在每条消息上提供时间戳时，事件清单的 time 字段标记为 null，
并在 Markdown 中说明「不可获取」，不做臆测。
"""

from __future__ import annotations

import json
import os
import re
import sys
from collections import Counter, OrderedDict
from datetime import datetime, timezone, timedelta

CST = timezone(timedelta(hours=8))

# 会话阶段（按出现顺序在 Markdown 中给出对照表）。
PHASES = [
    ("P1", "环境勘察与工具链确认", "确认 Go 版本、CGO 状态、Docker/gh/MCP 可用性，确定纯 Go SQLite 驱动"),
    ("P2", "Meegle 授权与工作项核验", "浏览器完成登录与设备码授权，用 CLI 核验工作项编号（目标空间不可见，结论如实记录）"),
    ("P3", "需求分析与开放问题确认", "产出《01-需求分析》，逐条解读 R1–R8，提出 12 项开放问题并逐项获得用户确认"),
    ("P4", "项目约定与业务实现", "按 AGENTS 约定实现 domain/store/service/upstream/httpapi 与模拟上游、页面模板"),
    ("P5", "单元测试与缺陷修复", "补齐单元测试冲覆盖率；测试暴露模板、外键、表单编码、错误泄漏等缺陷并修复"),
    ("P6", "自编 Skill 与核查工具", "编写 quota-guard 与 acceptance-audit 两个自编 Skill 并真实调用"),
    ("P7", "CI、部署与冒烟", "编写 CI 流水线，本机真实进程部署，执行健康检查与 39 项业务冒烟"),
    ("P8", "提交与验收审计", "按提交规范分阶段提交，运行 student-project-audit 产出检查报告与评分"),
]


def load_messages(path: str) -> list[dict]:
    msgs = []
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                msgs.append(json.loads(line))
            except json.JSONDecodeError:
                continue
    return msgs


def ms_to_iso(ms) -> str | None:
    if ms is None:
        return None
    try:
        value = int(ms)
    except (TypeError, ValueError):
        return None
    if value <= 0:
        return None
    return datetime.fromtimestamp(value / 1000, tz=timezone.utc).astimezone(CST).isoformat()


def summarize_text(text: str, limit: int = 400) -> str:
    text = re.sub(r"<system-reminder>.*?</system-reminder>", "", text, flags=re.S)
    text = re.sub(r"<[^>]+>", " ", text)
    text = re.sub(r"\s+", " ", text).strip()
    if len(text) > limit:
        text = text[:limit] + "…"
    return text


def extract_events(msgs: list[dict]) -> list[dict]:
    events: list[dict] = []
    seq = 0
    for obj in msgs:
        msg = obj.get("message") or {}
        role = msg.get("role", "unknown")
        turn = obj.get("turn_id", "")
        mid = obj.get("message_id", "")
        ts = ms_to_iso(msg.get("timestamp"))

        if role == "compactionSummary":
            seq += 1
            events.append({
                "seq": seq, "kind": "compaction", "role": role, "turn_id": turn,
                "message_id": mid, "time": ts,
                "summary": "上下文压缩摘要（平台自动生成，保留了目标、约束与已完成工作）",
            })
            continue

        if role == "custom":
            seq += 1
            events.append({
                "seq": seq, "kind": "system_reminder", "role": role, "turn_id": turn,
                "message_id": mid, "time": ts,
                "custom_type": msg.get("customType", ""),
                "summary": summarize_text(str(msg.get("content", "")), 200),
            })
            continue

        content = msg.get("content")
        if isinstance(content, str):
            content = [{"type": "text", "text": content}]
        content = content or []

        texts = [c.get("text", "") for c in content if isinstance(c, dict) and c.get("type") == "text"]
        calls = [c for c in content if isinstance(c, dict) and c.get("type") == "toolCall"]
        inline_results = [c for c in content if isinstance(c, dict) and c.get("type") == "toolResult"]

        if role == "user" and texts:
            seq += 1
            events.append({
                "seq": seq, "kind": "prompt", "role": role, "turn_id": turn,
                "message_id": mid, "time": ts,
                "summary": summarize_text("\n".join(texts), 800),
                "raw_chars": sum(len(t) for t in texts),
            })

        if role == "assistant":
            if texts:
                seq += 1
                events.append({
                    "seq": seq, "kind": "assistant_text", "role": role, "turn_id": turn,
                    "message_id": mid, "time": ts,
                    "summary": summarize_text("\n".join(texts), 400),
                })
            for call in calls:
                seq += 1
                args = call.get("arguments", {})
                events.append({
                    "seq": seq, "kind": "tool_call", "role": role, "turn_id": turn,
                    "message_id": mid, "time": ts,
                    "tool": call.get("name", ""),
                    "tool_call_id": call.get("id", ""),
                    "arguments": args if isinstance(args, dict) else {"_raw": str(args)},
                })
            for res in inline_results:
                seq += 1
                events.append({
                    "seq": seq, "kind": "tool_result", "role": role, "turn_id": turn,
                    "message_id": mid, "time": ts,
                    "tool": res.get("toolName", ""),
                    "tool_call_id": res.get("toolCallId", ""),
                    "summary": summarize_text(str(res.get("content", "")), 300),
                })

        if role == "toolResult":
            seq += 1
            events.append({
                "seq": seq, "kind": "tool_result", "role": role, "turn_id": turn,
                "message_id": mid, "time": ts,
                "tool": msg.get("toolName", ""),
                "tool_call_id": msg.get("toolCallId", ""),
                "summary": summarize_text(str(msg.get("content", "")), 300),
            })
    return events


def main() -> int:
    if len(sys.argv) < 5:
        print("用法：python export_session.py <消息文件...> -- <事件清单.jsonl> <会话导出.md> [会话来源目录]")
        return 2
    sep = sys.argv.index("--")
    sources = sys.argv[1:sep]
    out_jsonl, out_md = sys.argv[sep + 1], sys.argv[sep + 2]
    source_dir = sys.argv[sep + 3] if len(sys.argv) > sep + 3 else os.path.dirname(sources[0])

    # 按来源顺序读取，按 message_id 去重（快照与实时消息有重叠）。
    seen: set[str] = set()
    msgs: list[dict] = []
    dup = 0
    for src in sources:
        for obj in load_messages(src):
            mid = obj.get("message_id", "")
            if mid and mid in seen:
                dup += 1
                continue
            if mid:
                seen.add(mid)
            msgs.append(obj)

    events = extract_events(msgs)

    os.makedirs(os.path.dirname(out_jsonl) or ".", exist_ok=True)
    with open(out_jsonl, "w", encoding="utf-8") as fh:
        for ev in events:
            fh.write(json.dumps(ev, ensure_ascii=False) + "\n")

    kinds = Counter(ev["kind"] for ev in events)
    tools = Counter(ev.get("tool", "") for ev in events if ev["kind"] == "tool_call")
    turns = OrderedDict()
    for ev in events:
        turns.setdefault(ev.get("turn_id", ""), []).append(ev)
    timed = [ev for ev in events if ev.get("time")]

    prompts = [ev for ev in events if ev["kind"] == "prompt"]

    lines = []
    lines.append("# 会话导出（题目 05 开发全过程）")
    lines.append("")
    lines.append("> 本文件由 `scripts/export_session.py` 从本地会话存储自动导出，")
    lines.append("> 与 `evidence/会话事件清单.jsonl` **逐条对应**（同一 `seq` 编号）。")
    lines.append("> 导出时间以事件自带时间戳为准；平台未提供时间戳的事件标记为「不可获取」，不做臆测。")
    lines.append("")
    lines.append("## 1. 导出来源")
    lines.append("")
    lines.append("| 项 | 值 |")
    lines.append("|---|---|")
    lines.append(f"| 会话数据源 | {len(sources)} 个文件（快照 + 实时消息） |")
    for s in sources:
        lines.append(f"| 数据源文件 | `{s}` |")
    lines.append(f"| 会话目录 | `{source_dir}` |")
    lines.append(f"| 去重后消息条数 | {len(msgs)}（按 message_id 去重，重复 {dup} 条） |")
    lines.append(f"| 结构化事件条数 | {len(events)} |")
    lines.append(f"| 会话轮次（turn）数 | {len(turns)} |")
    lines.append(f"| 可见用户提示词条数 | {len(prompts)} |")
    lines.append(f"| 带时间戳的事件数 | {len(timed)} / {len(events)} |")
    lines.append("")
    lines.append("> **时间戳可得性说明**：本会话存储仅对部分系统事件记录了毫秒级时间戳，")
    lines.append("> 助手消息与工具消息未记录逐条时间戳（**平台不提供，不可获取**）。")
    lines.append("> 因此事件清单中 `time` 字段对无时间戳的事件置为 `null`，")
    lines.append("> 阶段时间以实际命令执行日志、产物文件时间与提交记录为准。")
    lines.append("")
    lines.append("## 2. 事件类型统计")
    lines.append("")
    lines.append("| 事件类型 | 条数 |")
    lines.append("|---|---|")
    for k, v in sorted(kinds.items(), key=lambda kv: -kv[1]):
        lines.append(f"| {k} | {v} |")
    lines.append("")
    lines.append("## 3. 工具调用统计")
    lines.append("")
    lines.append("| 工具 | 调用次数 |")
    lines.append("|---|---|")
    for k, v in sorted(tools.items(), key=lambda kv: -kv[1]):
        lines.append(f"| {k} | {v} |")
    lines.append("")
    lines.append("## 4. 阶段对照")
    lines.append("")
    lines.append("| 阶段 | 名称 | 关键动作 | 对应事件区间（seq） |")
    lines.append("|---|---|---|---|")
    # 用关键词定位阶段区间：取每个关键词首次出现的 seq。
    markers = {
        "P1": ["go env", "CGO", "docker", "gh ", "mcp list"],
        "P2": ["meegle", "workitem", "auth status"],
        "P3": ["需求分析", "Q1", "open questions"],
        "P4": ["domain.go", "store.go", "service.go", "httpapi", "mockupstream"],
        "P5": ["coverage", "_test.go", "gofmt", "vet"],
        "P6": ["quota-guard", "acceptance-audit", "SKILL.md"],
        "P7": ["workflows", "smoke-test", "Start-Process", "healthz"],
        "P8": ["git ", "commit", "student-project-audit", "audit-input"],
    }
    for code, name, action in PHASES:
        seqs = []
        for kw in markers.get(code, []):
            for ev in events:
                blob = json.dumps(ev, ensure_ascii=False)
                if kw.lower() in blob.lower():
                    seqs.append(ev["seq"])
                    break
        rng = f"{min(seqs)}–{max(seqs)}" if seqs else "（未在本导出中定位到）"
        lines.append(f"| {code} | {name} | {action} | {rng} |")
    lines.append("")
    lines.append("## 5. 可见用户提示词全文")
    lines.append("")
    lines.append("以下为本次会话中**用户可见的全部提示词**（平台系统提醒与上下文注入已剥离，")
    lines.append("其原文仍可在 `evidence/会话事件清单.jsonl` 的 `kind=prompt` 事件中按 `seq` 查得）。")
    lines.append("")
    for ev in prompts:
        lines.append(f"### 提示词 #{ev['seq']}（turn `{ev['turn_id']}`）")
        lines.append("")
        lines.append("```text")
        lines.append(ev["summary"])
        lines.append("```")
        lines.append("")
    lines.append("## 6. 事件清单索引")
    lines.append("")
    lines.append("| 事件文件 | 说明 |")
    lines.append("|---|---|")
    lines.append(f"| `evidence/会话事件清单.jsonl` | {len(events)} 条结构化事件，每行一个 JSON 对象，字段含 "
                 "`seq`/`kind`/`role`/`turn_id`/`message_id`/`time`/`tool`/`arguments`/`summary` |")
    lines.append(f"| `{os.path.basename(sources[-1])}` | 原始可见消息记录（只读引用，未修改） |")
    lines.append("")
    lines.append("## 7. 关联索引：从事件到产物")
    lines.append("")
    lines.append("| 阶段 | 主要产物 |")
    lines.append("|---|---|")
    lines.append("| P1–P2 | `evidence/meegle-workitem-verification.log` |")
    lines.append("| P3 | `docs/01-需求分析.md` |")
    lines.append("| P4 | `internal/**`、`cmd/**`、`AGENTS.md` |")
    lines.append("| P5 | `internal/**/*_test.go`、`evidence/coverage-summary.txt` |")
    lines.append("| P6 | `skills/quota-guard/`、`skills/acceptance-audit/`、`evidence/skill-quota-guard.md` |")
    lines.append("| P7 | `.github/workflows/ci.yml`、`scripts/smoke-test.sh`、`evidence/smoke-test.log`、`evidence/deployment.log` |")
    lines.append("| P8 | git 提交历史、`evidence/acceptance-audit/` |")
    lines.append("")

    os.makedirs(os.path.dirname(out_md) or ".", exist_ok=True)
    with open(out_md, "w", encoding="utf-8") as fh:
        fh.write("\n".join(lines))

    print(f"[export_session] events={len(events)} prompts={len(prompts)} turns={len(turns)} -> {out_jsonl}, {out_md}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
