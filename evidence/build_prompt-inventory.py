# -*- coding: utf-8 -*-
"""生成《全部提示词整理》文档。

输入：新导出的完整会话事件清单（含本轮）。
输出：docs/18-全部提示词整理.md —— 按来源分组、逐条列出提示词全文与用途归类。

原则：
- 提示词全文**逐字保留**，不截断、不改写；
- 归类只做「用途标签」，不改原文；
- 平台系统提醒与上下文注入不属提示词，单独说明；
- 敏感信息按仓库既有脱敏口径处理（口令/令牌掩码）。
"""
from __future__ import annotations

import collections
import io
import json
import os
import re
from pathlib import Path

SRC = os.path.join(os.environ['TEMP'], 'events-full.jsonl')
OUT = Path(r'C:\Users\26765\Documents\trae_projects\skills\2组\素材\d5\docs\18-全部提示词整理.md')

# ---------- 脱敏（沿用仓库既有口径：口令/令牌/一次性码不入档） ----------
REDACT = [
    (re.compile(r"(--device-code-value\s+)(?:\\?['\"])?[0-9a-fA-F\-]{16,}"), r"\1'（已用完的一次性设备码，已掩码）'"),
    (re.compile(r"gh[pousr]_[A-Za-z0-9]{16,}"), "ghp_****（令牌已掩码）"),
    (re.compile(r"(?i)(password|passwd|口令)(\s*[:=]\s*)(\S{6,})"), r"\1\2****（已掩码）"),
    # Meegle CLI 的 client_id：属一次性授权材料，按从严口径一并掩码
    (re.compile(r"cli_[0-9a-f]{32}"), "cli_****（CLI client_id 已掩码）"),
]


def mask(text: str) -> str:
    for pat, rep in REDACT:
        text = pat.sub(rep, text)
    return text


# ---------- 用途归类（只打标签，不改原文） ----------
def classify(origin: str, text: str) -> tuple[str, str]:
    t = text
    if origin == 'user':
        if t.startswith('ask_') or 'questionnaire' in t:
            return '问卷答复', '用户对需求分析 12 项开放问题（Q1–Q12）的逐项确认'
        if 'Local background tasks have finished' in t:
            return '系统回执', '后台任务完成通知（平台自动注入，非人工编写）'
        if 'student-project-audit' in t:
            return '复评指令', '指定用新版 v3.0 审计规则重新测试'
        if 'github' in t.lower() and 'questionnaire' in t:
            return '平台选择', '用户选择提交平台与凭据方式'
        if '通行密钥' in t:
            return '凭据补充', '用户口头提供 Gitee 账号口令（已按脱敏口径处理）'
        if '视频' in t or '视频分' in t:
            return '得分查询', '用户询问/调整带视频分的测算结果'
        if '原型' in t or 'SVG' in t or '思维导图' in t:
            return '产出补充', '用户追加可视化产出要求'
        if 'Agent' in t or 'skills' in t.lower() or 'Skill' in t:
            return '自检要求', '用户要求自检 Agent 与 Skill 的使用情况'
        if '测试账号' in t or '全部测试' in t:
            return '信息查询', '用户查询测试账号等信息'
        return '项目指令', '用户对交付范围、平台或流程的直接指令'
    if origin == 'subagent_task':
        if '独立' in t and ('测试' in t or '验收' in t):
            return '独立复核', '下发给全新上下文子 Agent 的独立测试/验收任务提示词'
        if '技术文档工程师' in t:
            return '文档撰写', '下发给 worker 子 Agent 的过程文档撰写任务提示词'
        if '只读调研' in t:
            return '跨项目调研', '下发给 explore 子 Agent 的只读调研任务提示词（属其它项目，非本题）'
        return '子 Agent 任务', '下发给子 Agent 的任务提示词'
    if origin == 'background_task':
        return '后台指令', '通过 run_in_background 启动的后台任务所执行的指令原文'
    return '其他', ''


def main() -> int:
    rows = []
    with io.open(SRC, encoding='utf-8') as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            d = json.loads(line)
            if d.get('kind') == 'prompt':
                rows.append(d)

    origins = collections.Counter(r.get('origin') for r in rows)
    kinds = collections.Counter((r.get('origin'), classify(r.get('origin', ''), r.get('summary', ''))[0])
                                for r in rows)

    L = []
    L.append('# 18 · 全部提示词整理（题目 05）')
    L.append('')
    L.append('> 本文汇总**本次会话树内所有向 Agent 发出的指令**（提示词），逐条给出原文与用途归类。')
    L.append('> 归类只打标签、**不改原文**；平台系统提醒与上下文注入不属于提示词，见 §4。')
    L.append('>')
    L.append('> 数据源：本机 MiniMax Code 会话存储（根会话 + 派生会话 + 压缩快照），')
    L.append('> 经 `scripts/export_session.py` 统一导出为结构化事件后按 `kind=prompt` 提取。')
    L.append('> 原始清单：`evidence/会话事件清单.jsonl`；本文生成脚本：')
    L.append('> `evidence/build_prompt-inventory.py`。')
    L.append('')
    L.append('---')
    L.append('')
    L.append('## 1. 总览')
    L.append('')
    L.append('| 项 | 条数 |')
    L.append('|---|---|')
    L.append('| **提示词合计** | **%d** |' % len(rows))
    L.append('| 用户在根会话输入（origin=user） | %d |' % origins.get('user', 0))
    L.append('| 下发给子 Agent 的任务提示词（origin=subagent_task） | %d |' % origins.get('subagent_task', 0))
    L.append('| 后台任务指令（origin=background_task） | %d |' % origins.get('background_task', 0))
    L.append('')
    L.append('### 1.1 按用途分布')
    L.append('')
    L.append('| 来源 | 用途 | 条数 |')
    L.append('|---|---|---|')
    for (o, u), n in sorted(kinds.items(), key=lambda kv: (kv[0][0], -kv[1])):
        L.append('| %s | %s | %d |' % (o, u, n))
    L.append('')

    # ---------- 分组正文 ----------
    groups = [
        ('user', '2. 用户在根会话输入的提示词'),
        ('subagent_task', '3. 下发给子 Agent 的任务提示词'),
        ('background_task', '4. 后台任务指令'),
    ]
    idx = 0
    for origin, title in groups:
        subset = [r for r in rows if r.get('origin') == origin]
        L.append('## %s（%d 条）' % (title, len(subset)))
        L.append('')
        for r in subset:
            idx += 1
            text = mask(r.get('summary', ''))
            cat, note = classify(origin, r.get('summary', ''))
            t = str(r.get('time') or '')[:19]
            extra = ''
            if origin == 'subagent_task':
                extra = '，子 Agent `%s`（%s）' % (r.get('agent', ''), r.get('description', ''))
            L.append('### 提示词 #%d · %s' % (idx, cat))
            L.append('')
            L.append('- 来源：`%s`｜时间：%s｜长度：%d 字%s' % (origin, t or '不可获取', len(text), extra))
            L.append('- 用途：%s' % (note or '—'))
            L.append('')
            if not text.strip():
                L.append('> （空内容：该轮次无实际文本）')
                L.append('')
                continue
            L.append('```text')
            L.append(text)
            L.append('```')
            L.append('')

    L.append('## 5. 不计入提示词的部分（如实说明）')
    L.append('')
    L.append('以下内容**不是**用户或父会话发出的指令，因此不计入上表：')
    L.append('')
    L.append('| 内容 | 说明 |')
    L.append('|---|---|')
    L.append('| 平台系统提醒（`kind=system_reminder`） | 运行环境注入的状态提示与任务完成通知，由平台生成 |')
    L.append('| 工具调用与返回（`kind=tool_call` / `tool_result`） | 工具调用记录，不是提示词 |')
    L.append('| 助手消息（`kind=assistant_text`） | 模型输出，不是提示词 |')
    L.append('| 上下文压缩事件（`kind=compaction`） | 会话摘要压缩，非指令 |')
    L.append('')
    L.append('> 用户界面上的「Local background tasks have finished…」属平台自动回执，')
    L.append('> 已保留在 `user` 分组中并标注为「系统回执」，**不计入人工编写的指令**。')
    L.append('')
    L.append('---')
    L.append('')
    L.append('## 6. 脱敏口径')
    L.append('')
    L.append('按 `AGENTS.md` §8 第 3–4 条，本文中的口令、令牌、一次性设备码**一律掩码**，')
    L.append('完整值不入档、不入库；仓库扫描 `scripts/verify-delivery.py` 的 D-09 项持续复核。')
    L.append('')
    L.append('| 类别 | 处理 |')
    L.append('|---|---|')
    L.append('| GitHub 令牌（`ghp_…` 等） | 只保留前缀 + `****` |')
    L.append('| OAuth 一次性设备码 | 整串替换为「已用完的一次性设备码，已掩码」 |')
    L.append('| Meegle CLI client_id | 整串替换为「CLI client_id 已掩码」 |')
    L.append('| 明文口令 | 替换为 `****` |')
    L.append('| 预置演示口令 | 仅以掩码出现（完整值只在源码 `cmd/server/main.go`） |')
    L.append('')

    OUT.write_text('\n'.join(L) + '\n', encoding='utf-8', newline='\n')
    print('written:', OUT)
    print('提示词合计: %d（user %d / subagent_task %d / background_task %d）' % (
        len(rows), origins.get('user', 0), origins.get('subagent_task', 0), origins.get('background_task', 0)))
    print('文档行数:', len(L))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
