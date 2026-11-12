#!/usr/bin/env python3
"""从会话事件清单推导「过程时序清单」，把开发阶段的真实先后顺序固化为可复核证据。

共同要求第 2 节要求「需求分析 → 原型设计 → 任务规划 → Agent 开发 → Agent 测试 →
Agent 验收 → 自动部署上线」；第 3 节要求项目级 `AGENTS.md` 在业务开发开始前编写并实际使用。
这些都发生在会话里，因此本脚本**从事件清单反推**每个关键产物的最早落盘时刻，
不依赖人工回忆，也不依赖文件最终修改时间。

输出：
  evidence/过程时序清单.md    可读时序表 + 里程碑判定
  evidence/过程时序清单.json  同一内容的结构化版本

用法：
    python scripts/process-timeline.py --events evidence/会话事件清单.jsonl \
        --repo . --out-md evidence/过程时序清单.md --out-json evidence/过程时序清单.json
"""
from __future__ import annotations

import argparse
import json
import subprocess
import sys
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

REPO_MARK = "\\素材\\d5\\"


def normalize(path: str) -> str:
    """把绝对路径还原成仓库内相对路径（Windows 反斜杠）。"""
    p = path.replace("/", "\\")
    if REPO_MARK in p:
        p = p.split(REPO_MARK, 1)[1]
    return p.replace("\\", "/")


def repo_file_events(events: list[dict]) -> list[dict]:
    """取出所有对仓库文件的 write/edit 事件，按时间排序。"""
    out = []
    for ev in events:
        if ev.get("kind") != "tool_call":
            continue
        tool = ev.get("tool", "")
        if tool not in ("write", "edit", "multiedit"):
            continue
        args = ev.get("arguments") or {}
        raw = args.get("path") or args.get("file_path") or ""
        if not isinstance(raw, str) or not raw:
            continue
        rel = normalize(raw)
        if rel.startswith("../") or ":" in rel.split("/")[0]:
            continue  # 仓库外的文件（会话产物、临时目录等）不计入
        out.append({"time": ev.get("time"), "seq": ev.get("seq"), "tool": tool, "path": rel})
    out.sort(key=lambda e: (e["time"] or "", e["seq"]))
    return out


def first_time_for(files: list[dict], predicate) -> tuple[str, str] | None:
    for f in files:
        if predicate(f["path"]):
            return f["time"], f["path"]
    return None


def git_commits(repo: Path, ref: str) -> list[dict]:
    out = subprocess.run(
        ["git", "-C", str(repo), "log", "--reverse", "--format=%h\t%cI\t%s", ref],
        capture_output=True, text=True, encoding="utf-8", errors="replace")
    if out.returncode != 0:
        return []
    rows = []
    for line in out.stdout.splitlines():
        parts = line.split("\t")
        if len(parts) == 3:
            rows.append({"sha": parts[0], "time": parts[1], "subject": parts[2]})
    return rows


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--events", default="evidence/会话事件清单.jsonl")
    ap.add_argument("--repo", default=".")
    ap.add_argument("--out-md", default="evidence/过程时序清单.md")
    ap.add_argument("--out-json", default="evidence/过程时序清单.json")
    ap.add_argument("--ref", default="dev/05")
    args = ap.parse_args()

    events = [json.loads(l) for l in open(args.events, encoding="utf-8") if l.strip()]
    files = repo_file_events(events)
    repo = Path(args.repo).resolve()

    # 关键产物分类
    def is_req(p: str) -> bool:
        return p.startswith("docs/01-") or p.endswith("docs/01-需求分析.md")

    def is_proto(p: str) -> bool:
        return p.startswith("docs/02-")

    def is_plan(p: str) -> bool:
        return p.startswith("docs/03-")

    def is_agents(p: str) -> bool:
        return p == "AGENTS.md" or p.startswith("docs/agents/AGENTS-v")

    def is_code(p: str) -> bool:
        return (p.startswith("internal/") or p.startswith("cmd/")) and p.endswith(".go")

    def is_test(p: str) -> bool:
        return p.endswith("_test.go")

    def is_skill(p: str) -> bool:
        return p.startswith("skills/")

    def is_ci(p: str) -> bool:
        return p.startswith(".github/")

    milestones = []
    for name, pred in (("需求分析（docs/01）", is_req), ("原型设计（docs/02）", is_proto),
                       ("任务规划（docs/03）", is_plan), ("项目约定（AGENTS.md）", is_agents),
                       ("首个业务代码文件（*.go）", is_code), ("首个测试文件（*_test.go）", is_test),
                       ("自编 Skill（skills/）", is_skill), ("CI 流水线（.github/）", is_ci)):
        hit = first_time_for(files, pred)
        if hit:
            milestones.append({"milestone": name, "first_write": hit[0], "path": hit[1]})

    task_prompts = [ev for ev in events if ev.get("kind") == "prompt"
                    and ev.get("origin") == "subagent_task"]
    bg_prompts = [ev for ev in events if ev.get("kind") == "prompt"
                  and ev.get("origin") == "background_task"]
    commits = git_commits(repo, args.ref)

    payload = {
        "source_events": args.events,
        "repo_file_write_events": len(files),
        "milestones": milestones,
        "subagent_task_prompts": [
            {"seq": e["seq"], "time": e.get("time"), "agent": e.get("agent"),
             "description": e.get("description"), "via": e.get("via")} for e in task_prompts],
        "background_task_prompts": [
            {"seq": e["seq"], "time": e.get("time"), "tool": e.get("tool")} for e in bg_prompts],
        "commits": commits,
    }

    md = []
    md.append("# 过程时序清单（由会话事件清单机器推导）")
    md.append("")
    md.append(f"> 由 `scripts/process-timeline.py` 从 `{args.events}` 反推，")
    md.append("> 不依赖人工回忆，也不依赖文件最终修改时间；结论与 `evidence/会话事件清单.jsonl` 一一可查。")
    md.append("> 事件条数：文件写入/编辑事件 "
              f"{len(files)} 条；子 Agent 任务提示词 {len(task_prompts)} 条；"
              f"后台任务指令 {len(bg_prompts)} 条。")
    md.append("")
    md.append("## 1. 关键产物首次落盘时刻")
    md.append("")
    md.append("| 里程碑 | 首次落盘时间 | 对应路径 |")
    md.append("|---|---|---|")
    for m in milestones:
        md.append(f"| {m['milestone']} | {m['first_write']} | `{m['path']}` |")
    md.append("")
    md.append("## 2. 子 Agent 任务提示词（`kind=prompt`，origin=subagent_task）")
    md.append("")
    md.append("| 时间 | 子 Agent | 任务 | 载体 |")
    md.append("|---|---|---|---|")
    for e in payload["subagent_task_prompts"]:
        md.append(f"| {e['time']} | `{e.get('agent','')}` | {e.get('description','')} | {e.get('via','')} |")
    md.append("")
    md.append("## 3. 后台任务指令（`kind=prompt`，origin=background_task）")
    md.append("")
    md.append("| 时间 | 工具 |")
    md.append("|---|---|")
    for e in payload["background_task_prompts"]:
        md.append(f"| {e['time']} | `{e.get('tool','')}` |")
    md.append("")
    md.append(f"## 4. 提交历史（{args.ref}，按提交时间升序）")
    md.append("")
    md.append("| 提交 | 时间 | 标题 |")
    md.append("|---|---|---|")
    for c in commits:
        md.append(f"| `{c['sha']}` | {c['time']} | {c['subject']} |")
    md.append("")

    Path(args.out_md).parent.mkdir(parents=True, exist_ok=True)
    Path(args.out_md).write_text("\n".join(md) + "\n", encoding="utf-8")
    Path(args.out_json).write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n",
                                   encoding="utf-8")
    print(f"[process-timeline] milestones={len(milestones)} writes={len(files)} -> {args.out_md}")
    for m in milestones:
        print(f"  {m['first_write']}  {m['milestone']}  ({m['path']})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
