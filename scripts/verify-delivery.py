#!/usr/bin/env python3
"""交付链路自检：逐条核对 `docs/01-需求分析.md` §12 的 D-01~D-09 可验收口径。

共同要求第 8 节第 9 条要求「仓库中保留题号、工作项、分支、提交、PR、验收及部署的索引」，
且「任何密钥、访问令牌及敏感数据不得进入仓库」。本脚本把这些要求变成**可重复执行的自检**：

    python scripts/verify-delivery.py --repo . --out evidence/delivery-check.md

每条检查输出三种状态之一：
  PASS  —— 材料齐备且可核对；
  PENDING—— 该项依赖尚未执行的远端动作（如尚未合并），如实标注为待补证，不臆造通过；
  FAIL  —— 材料存在但内容不满足口径。

退出码：0 = 无 FAIL（允许存在 PENDING）；1 = 存在 FAIL；2 = 执行错误。
"""
from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from datetime import datetime, timezone, timedelta
from pathlib import Path

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

CST = timezone(timedelta(hours=8))

VALID_TYPES = {"feat", "fix", "hotfix", "opt", "docs", "test"}
TYPE_RE = re.compile(r"^\[([a-z]+)\]")
VERSION_RE = re.compile(r"^\[(v\d+\.\d+\.\d+)\]")
NUMBER_RE = re.compile(r"^\[#(\d+)\]")
SECRET_ASSIGN_RE = re.compile(
    r"(?i)(access[_-]?token|private[_-]?token|api[_-]?key|gitee_access_token|authorization)"
    r"\s*[:=]\s*[\"']?[A-Za-z0-9_\-]{16,}")
BEARER_RE = re.compile(r"(?i)bearer\s+[A-Za-z0-9_\-]{16,}")
HEX32_RE = re.compile(r"(?<![0-9a-fA-F])[0-9a-f]{32}(?![0-9a-fA-F])")
# 已知非令牌的 32 位十六进制串来源：证据文件里的 SHA-256 摘要片段等；
# `cli_` 前缀是 Meegle CLI 的**公开 client id**（其 CLI 二进制内即含此值），不是凭据。
SAFE_HEX32_CONTEXT = re.compile(r"(?i)(sha256|digest|checksum|commit|sha|blob|oid|cli_|mvs_)")

SKIP_DIRS = {".git", "deploy", "node_modules", "__pycache__"}
SKIP_SUFFIX = {".exe", ".db", ".png", ".jpg", ".zip", ".pdf"}
# 本脚本自身的输出：上一轮结果可能记录了当时发现的命中，扫描时排除以避免自引用。
SELF_OUTPUTS = {"evidence/delivery-check.json", "evidence/delivery-check.md"}


def parse_message(subject: str) -> tuple[str, str | None, str | None] | None:
    """结构化解析提交消息 `[类型] [版本号] [#编号] 描述`。

    采用**逐段解析**而非单条正则：单正则会在分段畸形时回溯，
    把 `[feat] [v2.4.0][#123] xxx` 误判成「无版本号、无编号」，从而放过不合规消息。
    返回 (类型, 版本号或 None, 编号或 None)；不合规返回 None。
    """
    m = TYPE_RE.match(subject)
    if not m:
        return None
    typ = m.group(1)
    if typ not in VALID_TYPES:
        return None
    rest = subject[m.end():]
    if not rest.startswith(" "):
        return None
    rest = rest[1:]

    version: str | None = None
    number: str | None = None

    if rest.startswith("[v"):
        mv = VERSION_RE.match(rest)
        if not mv:
            return None
        version = mv.group(1)
        rest = rest[mv.end():]
        if not rest.startswith(" "):
            return None
        rest = rest[1:]

    if rest.startswith("[#"):
        mn = NUMBER_RE.match(rest)
        if not mn:
            return None
        number = mn.group(1)
        rest = rest[mn.end():]
        if not rest.startswith(" "):
            return None
        rest = rest[1:]

    if not rest.strip():
        return None
    return typ, version, number


def git(repo: Path, *args: str) -> tuple[int, str]:
    try:
        proc = subprocess.run(["git", "-C", str(repo), *args], capture_output=True,
                              text=True, encoding="utf-8", errors="replace", timeout=120)
    except (OSError, subprocess.SubprocessError) as exc:
        return 2, str(exc)
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def iter_repo_files(repo: Path):
    for path in repo.rglob("*"):
        if not path.is_file():
            continue
        parts = set(path.relative_to(repo).parts)
        if parts & SKIP_DIRS:
            continue
        if path.suffix.lower() in SKIP_SUFFIX:
            continue
        yield path


INIT_COMMIT_RE = re.compile(r"(?i)^(initial\s*commit|init|初始化.*|新建仓库.*)$")


def check_commit_messages(repo: Path) -> list[dict]:
    """D-03 的一部分：提交消息格式与编号必填规则。

    共同要求第 8 节第 2 条明确「允许初始化空仓库所必需的 README 等初始记录」，
    因此**远端初始化提交**（主题为 Initial commit / init / 初始化…）不适用本格式规则。
    这类提交会被**逐条列出**（不做静默过滤），其余提交一律严格判定。
    """
    code, out = git(repo, "log", "--format=%H%x09%s", "dev/05")
    if code != 0:
        return [{"id": "MSG", "status": "PENDING", "detail": f"无法读取 dev/05 提交：{out.strip()}"}]
    results = []
    bad = []
    exempted = []
    for line in out.splitlines():
        if not line.strip():
            continue
        sha, _, subject = line.partition("\t")
        if INIT_COMMIT_RE.match(subject.strip()):
            exempted.append(f"{sha[:7]} {subject}")
            continue
        parsed = parse_message(subject)
        if parsed is None:
            bad.append(f"{sha[:7]} 格式不合规：{subject}")
            continue
        typ, ver, num = parsed
        if typ in ("feat", "fix"):
            if not ver:
                bad.append(f"{sha[:7]} 缺版本号：{subject}")
            elif not num:
                bad.append(f"{sha[:7]} 缺工作项编号：{subject}")
    detail = ("dev/05 全部提交符合 `[类型] [版本号] [#编号] 描述`，且 feat/fix 均带版本号与编号"
              if not bad else "；".join(bad[:10]))
    if exempted:
        detail += (f"；另有 {len(exempted)} 条远端初始化提交按共同要求第 8 节第 2 条豁免格式规则："
                   + "、".join(exempted))
    results.append({
        "id": "提交消息格式与编号必填",
        "status": "PASS" if not bad else "FAIL",
        "detail": detail,
    })
    return results


def check_mcp_repo_creation(repo: Path) -> list[dict]:
    """D-01：MCP 建仓报文存在，含工具名、请求、结果、时间与仓库 URL。

    题面第 8 节第 1 条写的主平台是 GitHub，本机可用的等价平台是 Gitee，
    因此两个平台的 MCP 报文目录都接受（工具名分别为 create_repository / create_repo），
    但**只认已落盘的原始报文**，不接受手工说明。
    """
    hits = []
    for platform, tool_names in (("github-mcp", ("create_repository",)),
                                 ("gitee-mcp", ("create_repo",))):
        d = repo / "evidence" / platform
        if not d.is_dir():
            continue
        for f in sorted(d.glob("*.json")):
            if not any(t in f.name for t in tool_names):
                continue
            try:
                payload = json.loads(f.read_text(encoding="utf-8"))
            except (OSError, json.JSONDecodeError):
                continue
            hits.append((f, payload, tool_names))
    if not hits:
        return [{"id": "D-01 MCP 建仓证据", "status": "PENDING",
                 "detail": "evidence/{github,gitee}-mcp/ 下暂无建仓报文（远端建仓动作未执行）"}]

    def evaluate(f: Path, payload: dict, tool_names: tuple[str, ...]) -> tuple[bool, str]:
        need = ["tool", "called_at", "request", "response"]
        missing = [k for k in need if not payload.get(k)]
        resp = payload.get("response") or {}
        resp_blob = json.dumps(resp, ensure_ascii=False)
        is_error = bool(resp.get("isError")) or bool((resp.get("result") or {}).get("isError"))
        # 仓库 URL 必须出现在**响应**里，且是仓库页地址（不是 API 端点）
        repo_url = re.search(r"https://(?:www\.)?(?:github|gitee)\.com/(?!api\.)[\w.\-]+/[\w.\-]+",
                             resp_blob)
        failed = is_error or bool(re.search(r"(?i)(failed to create|403|forbidden|not accessible)",
                                            resp_blob))
        ok = (not missing and not failed and payload.get("tool") in tool_names
              and repo_url is not None)
        detail = (f"{f.name}：工具={payload.get('tool')}，时间={payload.get('called_at')}，"
                  f"响应中的仓库 URL={repo_url.group(0) if repo_url else '未出现'}")
        if failed:
            detail += "；服务端返回失败：" + resp_blob[:200]
        if missing:
            detail += "；报文缺字段：" + "、".join(missing)
        return ok, detail

    # 每个平台的建仓以**最后一次调用**为当前实况；更早的失败尝试逐条列出（保留历史，不掩盖）。
    by_tool: dict[str, list] = {}
    for f, payload, tool_names in hits:
        by_tool.setdefault(payload.get("tool") or f.name, []).append((f, payload, tool_names))
    results = []
    for items in by_tool.values():
        items.sort(key=lambda it: it[1].get("called_at") or "")
        ok, detail = evaluate(*items[-1])
        history = items[:-1]
        if history:
            detail += (f"；另有 {len(history)} 次更早的建仓尝试未成功（报文已归档，历史如实保留）："
                       + "、".join(h.name for h, _, _ in history))
        results.append({
            "id": "D-01 MCP 建仓证据",
            "status": "PASS" if ok else "FAIL",
            "detail": detail})
    return results


def check_pr_description(repo: Path) -> list[dict]:
    """D-04：PR 描述要素齐全。"""
    d = repo / "evidence" / "gitee-mcp"
    files = sorted(d.glob("*create_pull*.json")) if d.is_dir() else []
    if not files:
        return [{"id": "D-04 PR 描述要素", "status": "PENDING",
                 "detail": "暂无 create_pull 报文（PR 未发起）"}]
    payload = json.loads(files[0].read_text(encoding="utf-8"))
    body = json.dumps(payload, ensure_ascii=False)
    required = {
        "工作项编号": r"#\d{6,}",
        "变更说明": r"(变更说明|变更摘要)",
        "测试结果": r"(测试结果|单元测试|冒烟)",
        "覆盖率": r"(覆盖率|cover)",
        "验收报告": r"(验收报告|independent-acceptance)",
        "AI/Skill 记录": r"(AI 使用|Skill 使用|skills/|AI 使用说明)",
    }
    missing = [k for k, pat in required.items() if not re.search(pat, body)]
    return [{"id": "D-04 PR 描述要素",
              "status": "PASS" if not missing else ("FAIL" if files else "PENDING"),
              "detail": "PR 描述含全部 6 项要素" if not missing else "缺少：" + "、".join(missing)}]


def check_branch_protection(repo: Path) -> list[dict]:
    """D-05：主分支保护读取结果。

    保护规则的归档文件名随平台不同（GitHub 侧 branch-protection.json，
    Gitee 侧 branch-protection-gitee.json），两个都接受，但**只认已落盘的归档**。
    """
    candidates = [repo / "evidence" / name for name in
                  ("branch-protection.json", "branch-protection-gitee.json",
                   "branch-protection-github.json")]
    p = next((c for c in candidates if c.is_file()), None)
    if p is None:
        return [{"id": "D-05 主分支保护", "status": "PENDING",
                 "detail": "evidence/branch-protection*.json 不存在（保护规则尚未读取归档）"}]
    blob = p.read_text(encoding="utf-8", errors="replace")
    checks = {
        "禁止直接推送": r"(?i)(push|push_permission|禁止推送|allowed_pushes)",
        "禁止删除": r"(?i)(delete|禁止删除)",
        "禁止强推": r"(?i)(force|强推)",
        "要求 PR 合并": r"(?i)(pull_request|合并|pr_merged|merge)",
    }
    missing = [k for k, pat in checks.items() if not re.search(pat, blob)]
    return [{"id": "D-05 主分支保护",
              "status": "PASS" if not missing else "FAIL",
              "detail": f"{p.name}：保护规则读取结果含禁止直推/删除/强推与 PR 合并要求"
              if not missing else f"{p.name}：读取结果中未见：" + "、".join(missing)}]


def check_merge_and_gate(repo: Path) -> list[dict]:
    """D-06 / D-07：合并提交保留原始提交；门禁与部署日志含合并 SHA。"""
    results = []
    code, out = git(repo, "log", "-1", "--format=%H%x09%P%x09%s", "main")
    if code != 0 or not out.strip():
        results.append({"id": "D-06 合并提交", "status": "PENDING",
                        "detail": "main 分支尚无合并提交"})
        return results
    sha, parents, subject = out.strip().split("\t", 2)
    parents = parents.split()
    msg_ok = parse_message(subject) is not None
    keep = len(parents) >= 2

    # 尚未发生合并时如实标为待补证，不把「还没做」误判成「做错了」。
    ev_dir = repo / "evidence" / "gitee-mcp"
    merged_already = bool(ev_dir.is_dir() and list(ev_dir.glob("*merge_pull*.json")))
    if merged_already:
        status = "PASS" if (keep and msg_ok) else "FAIL"
    else:
        status = "PENDING" if not keep else ("PASS" if msg_ok else "FAIL")
    results.append({
        "id": "D-06 合并提交保留原始提交",
        "status": status,
        "detail": f"main 头部提交 {sha[:12]}，父提交数={len(parents)}，消息={subject}"
        + ("" if merged_already else "（尚未发生合并动作，待补证）"),
    })

    ev = repo / "evidence"
    logs = sorted(ev.glob("main-gate-*.log")) + sorted(ev.glob("deployment-main*.log"))
    if not logs:
        results.append({"id": "D-07 合并提交上的门禁与部署", "status": "PENDING",
                        "detail": "尚无 main-gate-*.log / deployment-main*.log"})
        return results
    hit = [p.name for p in logs if sha[:12] in p.read_text(encoding="utf-8", errors="replace")]
    results.append({
        "id": "D-07 合并提交上的门禁与部署",
        "status": "PASS" if hit else "FAIL",
        "detail": f"日志中出现合并 SHA 的文件：{hit}" if hit else
                  f"未在 {len(logs)} 个日志中找到合并 SHA {sha[:12]}",
    })
    return results


def check_secret_scan(repo: Path) -> list[dict]:
    """D-09 的一半：仓库内不得出现令牌明文。"""
    hits = []
    scanned = 0
    for path in iter_repo_files(repo):
        try:
            text = path.read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        scanned += 1
        rel = path.relative_to(repo).as_posix()
        if rel in SELF_OUTPUTS:
            continue
        for m in SECRET_ASSIGN_RE.finditer(text):
            hits.append(f"{rel}: 疑似令牌赋值 {m.group(0)[:60]}")
        for m in BEARER_RE.finditer(text):
            hits.append(f"{rel}: 疑似 Bearer 令牌 {m.group(0)[:60]}")
        for line_no, line in enumerate(text.splitlines(), 1):
            if line.lstrip().startswith("#"):
                continue
            for m in HEX32_RE.finditer(line):
                ctx = line[max(0, m.start() - 40):m.start()]
                if SAFE_HEX32_CONTEXT.search(ctx):
                    continue
                hits.append(f"{rel}:{line_no}: 32 位十六进制串 {m.group(0)}")
    return [{"id": "D-09 仓库无令牌明文",
              "status": "PASS" if not hits else "FAIL",
              "detail": f"扫描 {scanned} 个文本文件，未命中令牌模式" if not hits
              else "；".join(hits[:10])}]


def check_remote_branches(repo: Path) -> list[dict]:
    """D-02：远端分支承载与主分支内容的来源。

    共同要求第 8 节第 2/3 条：默认主分支 main；**允许**初始化空仓库所必需的
    README 等初始记录留在 main；业务内容先在 dev/<题号> 分支提交，再经 PR 合入 main。
    因此远端 main 的合法状态有两种：
      ① 仅含初始化提交（尚未合并 PR 的早期状态）；
      ② 由 PR 合并而来的**合并提交**（父提交数为 2，且第二父提交 == 远端 dev/05 头部）。
    两种状态都判定为合规；「直接推送业务内容到 main」由分支保护的**真实推送实测**
    证据判定（evidence/branch-protection*-verify.json 的 rejected_by_protection）。

    远端平台可能是 GitHub 或 Gitee（题面主平台与本机可用平台差异已在 docs/10 登记），
    因此逐个尝试已配置的远端，取**第一个能连通**的（优先 gitee，其次 origin）。
    """
    code, out = git(repo, "remote")
    if code != 0 or not out.strip():
        return [{"id": "D-02 远端分支承载", "status": "PENDING", "detail": "仓库尚未配置远端"}]
    remotes = out.split()
    order = ([r for r in ("gitee", "origin") if r in remotes]
             + [r for r in remotes if r not in ("gitee", "origin")])
    branches: dict[str, str] = {}
    used_remote = ""
    errors: list[str] = []
    for remote in order:
        code, refs = git(repo, "ls-remote", "--heads", remote)
        if code != 0:
            errors.append(f"{remote}: {refs.strip()[:160]}")
            continue
        branches = {}
        for line in refs.splitlines():
            parts = line.split()
            if len(parts) == 2:
                branches[parts[1].replace("refs/heads/", "")] = parts[0]
        used_remote = remote
        break
    if not used_remote:
        return [{"id": "D-02 远端分支承载", "status": "PENDING",
                 "detail": "无法读取任何远端分支：" + "；".join(errors)}]
    need = {"main", "dev/05"}
    missing = need - set(branches)
    if missing:
        return [{"id": "D-02 远端分支承载", "status": "FAIL",
                 "detail": f"远端 {used_remote} 缺少分支：" + "、".join(sorted(missing))}]

    main_sha = branches["main"]
    dev_sha = branches["dev/05"]
    code, cat = git(repo, "cat-file", "-p", main_sha)
    parents = [ln.split()[1] for ln in cat.splitlines() if ln.startswith("parent ")] if code == 0 else []
    code2, count = git(repo, "rev-list", "--count", main_sha)
    n = count.strip() if code2 == 0 else "?"

    # 分支保护实测证据（直推是否被服务端拒绝）
    protect = ""
    for name in ("branch-protection-gitee-verify.json", "branch-protection-verify.json",
                 "branch-protection-github-verify.json"):
        p = repo / "evidence" / name
        if p.is_file():
            blob = p.read_text(encoding="utf-8", errors="replace")
            protect = ("真实推送实测直推被服务端拒绝"
                       if '"rejected_by_protection": true' in blob.replace("True", "true")
                       else "保护实测证据存在但未标记为已拒绝直推")
            break

    if len(parents) >= 2:
        # 第二父提交必须是远端 dev/05 头部的**祖先**（而不必相等）：
        # 合并之后 dev/05 上可能又产生新提交（例如发布后的文档回填），
        # 那时 main 的合并点仍是 dev/05 的某个祖先，属正常状态。
        code3, anc = git(repo, "merge-base", "--is-ancestor", parents[1], dev_sha)
        merged_from_dev = code3 == 0
        if merged_from_dev:
            status = "PASS"
            detail = (f"远端 {used_remote} 分支 {sorted(branches)}；远端 main 头部 {main_sha[:7]} 是"
                      f"**合并提交**（父提交数={len(parents)}，第二父提交 {parents[1][:7]} 是远端 dev/05 头部"
                      f" {dev_sha[:7]} 的祖先），业务内容经 PR 合入 main 而非直推；main 提交数={n}"
                      + (f"；{protect}" if protect else ""))
        else:
            status = "FAIL"
            detail = (f"远端 {used_remote} 的 main 头部 {main_sha[:7]} 虽是合并提交，"
                      f"但第二父提交 {parents[1][:7]} 不在远端 dev/05 的历史中")
    elif len(parents) <= 1 and n == "1":
        status = "PASS"
        detail = (f"远端 {used_remote} 分支 {sorted(branches)}；远端 main 仅含初始化提交"
                  f"（提交数=1，{main_sha[:7]}），业务提交在 dev/05"
                  + (f"；{protect}" if protect else ""))
    else:
        status = "FAIL"
        detail = (f"远端 {used_remote} 的 main 既不是初始化单提交，也不是来自 dev/05 的合并提交"
                  f"（父提交数={len(parents)}，提交数={n}）")
    return [{"id": "D-02 远端分支承载", "status": status, "detail": detail}]


def main() -> int:
    ap = argparse.ArgumentParser(description="交付链路自检")
    ap.add_argument("--repo", default=".")
    ap.add_argument("--out", default="evidence/delivery-check.md")
    ap.add_argument("--json-out", default="evidence/delivery-check.json")
    args = ap.parse_args()

    repo = Path(args.repo).resolve()
    results: list[dict] = []
    results += check_mcp_repo_creation(repo)
    results += check_remote_branches(repo)
    results += check_commit_messages(repo)
    results += check_pr_description(repo)
    results += check_branch_protection(repo)
    results += check_merge_and_gate(repo)
    results += check_secret_scan(repo)

    now = datetime.now(CST).isoformat()
    counts = {s: sum(1 for r in results if r["status"] == s) for s in ("PASS", "PENDING", "FAIL")}

    lines = []
    lines.append("# 交付链路自检报告（题目 05）")
    lines.append("")
    lines.append(f"> 由 `scripts/verify-delivery.py` 自动生成，执行时间 {now}。")
    lines.append("> 口径来源：`docs/01-需求分析.md` §12 的 D-01~D-09（共同要求第 8 节）。")
    lines.append("> `PENDING` 表示该项依赖尚未执行的远端动作，如实标注待补证，不臆造通过。")
    lines.append("")
    lines.append("| # | 检查项 | 状态 | 说明 |")
    lines.append("|---|---|---|---|")
    for i, r in enumerate(results, 1):
        lines.append(f"| {i} | {r['id']} | {r['status']} | {r['detail']} |")
    lines.append("")
    lines.append(f"合计：PASS {counts['PASS']} / PENDING {counts['PENDING']} / FAIL {counts['FAIL']}")
    lines.append("")

    out_path = Path(args.out)
    if not out_path.is_absolute():
        out_path = Path.cwd() / out_path
    out_path.parent.mkdir(parents=True, exist_ok=True)
    with open(out_path, "w", encoding="utf-8", newline="\n") as fh:
        fh.write("\n".join(lines) + "\n")
    json_path = Path(args.json_out)
    if not json_path.is_absolute():
        json_path = Path.cwd() / json_path
    with open(json_path, "w", encoding="utf-8", newline="\n") as fh:
        fh.write(json.dumps({"checked_at": now, "counts": counts, "checks": results},
                             ensure_ascii=False, indent=2) + "\n")

    print(f"[verify-delivery] PASS={counts['PASS']} PENDING={counts['PENDING']} FAIL={counts['FAIL']}"
          f" out={out_path}")
    for r in results:
        print(f"  [{r['status']:7}] {r['id']}：{r['detail'][:110]}")
    return 1 if counts["FAIL"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
