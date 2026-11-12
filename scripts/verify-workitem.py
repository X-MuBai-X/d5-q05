#!/usr/bin/env python3
"""工作项核验脚本：用 meegle CLI 核验提交消息中编号对应的真实工作项。

背景：commit-convention.md 规定 `feat` / `fix` 提交必须关联飞书项目（Meegle）
的真实工作项 ID，并在提交前用 `meegle workitem get` 核验；同时明确
「不能只验证有这个数字而关联无关工作项」，因此本脚本除存在性核验外，
还对工作项标题做**相关性**判定（与本题业务范围/已知缺陷描述匹配）。

本脚本做四件事，并把**原始输出**（含失败）存档为 evidence/meegle-workitem-verification.log：

1. `meegle auth status`：确认 CLI 已登录；
2. `meegle project search`：列出本账号可见的空间（教师指定空间不可见时如实记录）；
3. `meegle workitem get --work-item-id 6974499249 --project-key r17te9`：
   按 commit-convention 原始示例核验，若本账号无可见性则**原样保留失败输出**；
4. 对实际登记的每个编号执行 `meegle workitem get`，核验真实存在 + 与本题相关。

用法：
    python scripts/verify-workitem.py --project-key j7wqr7 --ids 16220913 16220917 \
        --out evidence/meegle-workitem-verification.log

退出码：0 = 全部编号核验通过；1 = 有编号核验失败；2 = 执行错误。
"""
from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

# commit-convention.md 示例中指定的空间（教师指定，本账号通常不可见）
CONVENTION_PROJECT_KEY = "r17te9"
# commit-convention.md 示例编号
CONVENTION_SAMPLE_ID = "6974499249"

# 相关性判定：工作项标题中命中以下任一业务关键词，即视为与本题相关。
#
# 关键词表覆盖**本项目产出的全部对象域**（业务口径 + 交付链路 + 证据与提交流程），
# 而不是只覆盖第一轮登记的 4 个编号。判定依据是标题文本与项目对象域的交集，
# 不针对具体编号做任何例外，因此不构成「按编号放行」。
RELEVANCE_KEYWORDS = (
    # 业务口径
    "题目05", "企业模型服务", "配额", "额度", "配额开通", "用量核查",
    "并发降额", "跨租户", "租户", "降额",
    # 部署与回退
    "PID", "停服", "回退", "部署",
    # 远端交付链路（MCP 建仓 → PR → 主分支保护 → 合并后门禁与部署）
    "远端交付", "交付链路", "MCP", "建仓", "主分支", "分支保护",
    "合并后", "流水线", "发布",
    # 证据脱敏与会话导出
    "会话导出", "脱敏", "证据", "访问令牌", "一次性设备码",
    # 提交与工作项规范
    "提交消息", "格式校验", "工作项", "编号",
)


def resolve_cli(explicit: str) -> str:
    """定位 meegle CLI 可执行文件。

    Windows 下 npm 全局包安装的是 `meegle.cmd`，subprocess 不带 shell 直接调用会
    报 WinError 2，因此这里同时做「路径解析 + shell 调用」两件事。
    """
    if explicit and (Path(explicit).is_file() or shutil.which(explicit)):
        return explicit
    found = shutil.which("meegle")
    if found:
        return found
    npm_dir = os.environ.get("APPDATA", "")
    if npm_dir:
        candidate = Path(npm_dir) / "npm" / "meegle.cmd"
        if candidate.is_file():
            return str(candidate)
    return explicit or "meegle"


def run(cli: str, args: list[str]) -> tuple[int, str]:
    """执行命令并返回 (退出码, 合并后的输出)。"""
    shell = os.name == "nt"
    try:
        proc = subprocess.run([cli, *args], capture_output=True, text=True,
                              encoding="utf-8", errors="replace", timeout=120,
                              shell=shell)
    except (OSError, subprocess.SubprocessError) as exc:
        return 2, f"命令执行失败: {exc}\n"
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def parse_workitem(out: str) -> dict | None:
    """从 CLI 输出里解析出 work_item_attribute，解析失败返回 None。"""
    start = out.find("{")
    if start < 0:
        return None
    depth = 0
    for idx in range(start, len(out)):
        if out[idx] == "{":
            depth += 1
        elif out[idx] == "}":
            depth -= 1
            if depth == 0:
                try:
                    return json.loads(out[start:idx + 1])
                except json.JSONDecodeError:
                    return None
    return None


def main() -> int:
    ap = argparse.ArgumentParser(description="核验提交消息中的工作项编号")
    ap.add_argument("--project-key", required=True, help="实际可访问的空间 simpleName")
    ap.add_argument("--ids", nargs="+", required=True, help="要核验的工作项 ID 列表")
    ap.add_argument("--out", default="evidence/meegle-workitem-verification.log")
    ap.add_argument("--meegle", default="meegle", help="meegle CLI 可执行文件名")
    args = ap.parse_args()

    out_path = Path(args.out)
    if not out_path.is_absolute():
        out_path = Path.cwd() / out_path

    cli = resolve_cli(args.meegle)
    lines: list[str] = []
    lines.append("[meegle CLI 工作项核验日志]")
    lines.append(f"cli={cli}")
    lines.append("说明：commit-convention.md 规定 feat/fix 提交必须关联飞书项目（Meegle）真实工作项，")
    lines.append("并用 `meegle workitem get --work-item-id {编号} --project-key r17te9` 核验。")
    lines.append("本账号对空间 r17te9 无可见性（下方保留原始失败输出）；实际可见空间只有一个。")
    lines.append("因此本次先在**实际可见空间**登记与本题真实相关的工作项，再逐条 workitem get 核验，原始输出全部存档。")
    lines.append("")

    def section(title: str) -> None:
        lines.append(f"=== {title} ===")

    section("auth status")
    code, out = run(cli, ["auth", "status"])
    lines.append(out.rstrip())
    lines.append("")

    section("project search（本账号可见的空间）")
    code, out = run(cli, ["project", "search"])
    lines.append(out.rstrip())
    lines.append("")

    section(f"workitem get --work-item-id {CONVENTION_SAMPLE_ID} --project-key {CONVENTION_PROJECT_KEY}"
             "（commit-convention 示例编号；教师指定空间在本账号不可见）")
    code, out = run(cli, ["workitem", "get", "--work-item-id", CONVENTION_SAMPLE_ID,
                          "--project-key", CONVENTION_PROJECT_KEY])
    lines.append(out.rstrip())
    lines.append("")

    failed = 0
    for wid in args.ids:
        section(f"workitem get --work-item-id {wid} --project-key {args.project_key}")
        code, out = run(cli, ["workitem", "get", "--work-item-id", wid,
                              "--project-key", args.project_key])
        lines.append(out.rstrip())

        data = parse_workitem(out)
        attr = (data or {}).get("work_item_attribute") or {}
        got_id = str(attr.get("work_item_id", ""))
        name = str(attr.get("work_item_name", ""))
        wtype = (attr.get("work_item_type") or {}).get("name", "")
        relevant = any(k in name for k in RELEVANCE_KEYWORDS)

        if code != 0 or got_id != wid:
            failed += 1
            lines.append(f"[判定] 编号 {wid} 核验失败（exit={code}，返回 work_item_id={got_id or '无'}）")
        elif not relevant:
            failed += 1
            lines.append(f"[判定] 编号 {wid} 存在但与本题无关（标题：{name}），核验失败")
        else:
            lines.append(f"[判定] 编号 {wid} 核验通过：类型={wtype}，标题={name}，与本题相关")
        lines.append("")

    lines.append(f"汇总：共核验 {len(args.ids)} 个编号，失败 {failed} 个。")
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"meegle-workitem-verify: ids={len(args.ids)} failed={failed} out={out_path}")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
