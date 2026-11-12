# 独立验收报告 · v2.4.0 迭代（共同要求第 8 节「GitHub 仓库、开发分支与合并流程」）

> **验收者**：独立验收 Agent（未参与开发）
> **验收对象**：`C:\Users\26765\Documents\trae_projects\skills\2组\素材\d5`，分支 `dev/05`
> **验收时点**：`dev/05` HEAD = `03c2743bbfa654cff9fc375811c6a3994676b38d`
> **判定依据**：题集原文共同要求第 1–184 行（重点第 8 节、第 9 节、第 10 节）与第 5 题（371–415 行）
> **原始题面**：`C:\Users\26765\.minimax\v2\assets\2026\11\11\18-38-19-287-asset_20261111-183819-287_4e49fcd8b2e4_5f8cc178-Go项目业务场景题.md`
> **本报告不采信仓库自述的任何"已完成/已通过"声明**：所有结论均来自本次独立执行的命令与运行时观测。

> **脱敏说明**：文中引用的预置**演示**口令一律以掩码出现（`ops-****`、`admin-a-****`、`admin-b-****`）；
> 完整值只保留在 `cmd/server/main.go` 的 `seedData`，本报告不复制完整值。

> **脱敏补充（2026-11-12 整改轮）**：本报告初稿在「泄密与脱敏」一节引用了
> ① Meegle 设备码授权返回体中的**一次性设备码明文**，
> ② 早期版本测试脚本中的**凭据形态字面量**（合成夹具，非真实凭据），
> ③ 题面预置的**演示账号口令**（`ops-****` 等公开演示种子值）。
> 三者与前两份独立报告的脱敏口径不一致，已按共同要求第 3 节统一改为掩码形式；
> 掩码规则直接复用 `scripts/export_session.py` 的 `redact()`，与仓库导出口径完全一致。
> **判定结论、严重度、证据位置与整改建议均未改动**，仅替换凭据字面量。

> **脱敏补充（2026-11-12 整改轮）**：本报告初稿在「泄密与脱敏」一节引用了
> ① Meegle 设备码授权返回体中的**一次性设备码明文**，
> ② 早期版本测试脚本中的**凭据形态字面量**（合成夹具，非真实凭据）。
> 二者与前两份独立报告的脱敏口径不一致，已按共同要求第 3 节统一改为掩码形式；
> 掩码规则直接复用 `scripts/export_session.py` 的 `redact()`，与仓库导出口径完全一致。
> **判定结论、严重度、证据位置与整改建议均未改动**，仅替换凭据字面量。

> **脱敏补充（2026-11-12 整改轮）**：本报告初稿在「泄密与脱敏」一节引用了 Meegle 设备码授权返回体中的**一次性设备码明文**，与两份前序独立报告的脱敏口径不一致。
> 已按共同要求第 3 节「日志、截图和对话导出均须脱敏」把其中 4 处 UUID 形态的一次性凭据改为掩码形式；
> **判定结论、严重度、证据位置与整改建议均未改动**，仅替换凭据字面量。

---

## 0. 我实际运行过的命令清单

所有输出产物写入临时目录 `%TEMP%\d5-verify\`，仓库源文件未做任何修改；
所有临时 `git worktree` 已在验收结束时移除（`git worktree list` 仅剩主工作区）。

```powershell
# 版本与阶段顺序
git log --oneline --reverse dev/05
git show --stat --name-status <每个 v2.4.0 提交>
git rev-list --parents -n 1 <每个 v2.4.0 提交>        # 拓扑父链
git log --format="%h %ad %s" --date=iso-strict dev/05
git ls-files                                         # 追踪文件清单
git ls-files -- <CODEOWNERS 各路径>                  # 路径存在性核对
git status --short                                  # 确认未改动仓库

# 脚本实跑（输出均重定向到 %TEMP%\d5-verify\）
python scripts/test-delivery-checks.py
python scripts/verify-delivery.py --repo . --out $T/dc-*.md --json-out $T/dc-*.json
python scripts/verify-gate.py --commit HEAD --out $T/gate-*.log
python scripts/coverage-summary.py --profile ... --out ...
python scripts/process-timeline.py --events evidence/会话事件清单.jsonl --repo . --out-md $T/timeline.md --out-json $T/timeline.json
python skills/acceptance-audit/scripts/acceptance_audit.py --repo . --out-dir $T/audit-out --skip-toolchain
python scripts/scenario-verify.py --base-url http://127.0.0.1:8091 --out $T/scenario2.log --json-out $T/scenario2.json

# 可复现性核对（clean worktree，避免工作区干扰）
git worktree add --detach $T\wt-1cc5233 1cc5233   # python scripts/verify-delivery.py --repo $T\wt-1cc5233 ...
git worktree add --detach $T\wt-2411cc8 2411cc8   # 同上
git worktree add --detach $T\wt-7bf0bd3 7bf0bd3   # 同上
git worktree add --detach $T\wt-new    HEAD       # 同上（最终 HEAD）

# 业务实跑（自建干净实例，避开本机 8080 上的既有进程）
go build -o $T/server.exe ./cmd/server ; go build -o $T/mockupstream.exe ./cmd/mockupstream
Start-Process $T\mockupstream.exe -addr 127.0.0.1:9093
Start-Process $T/server.exe -addr 127.0.0.1:8091 -db $T/fresh2/app.db -upstream http://127.0.0.1:9093 -enable-test-hooks
#   → 用 Python 复刻 scripts/smoke-test.sh 的全部 27 条断言，逐条打印 HTTP 码

# 泄密扫描
git grep -n -I -E "ghp_|github_pat_|gho_|ghs_|ghu_|ghr_" -- .
git grep -n -I -i -E "bearer[ ]+[A-Za-z0-9_.-]{16,}" -- .
git grep -n -I -E "ops-****|admin-a-****|admin-b-****" -- .
git grep -n -I -o -E "(^|[^0-9a-fA-F])[0-9a-f]{32}([^0-9a-fA-F]|$)" -- .
git grep -n -I -i -E "device[_-]?code" -- .

# YAML 合法性（python + pyyaml）
python -c "import yaml,sys; yaml.safe_load(open(sys.argv[1],encoding='utf-8'))" .github/workflows/release.yml
python -c "import yaml,sys; yaml.safe_load(open(sys.argv[1],encoding='utf-8'))" .github/workflows/ci.yml

# 工作项编号独立核验（meegle CLI，账号已认证）
& "$env:APPDATA\npm\meegle.cmd" workitem get --work-item-id 16221249 --project-key j7wqr7
& "$env:APPDATA\npm\meegle.cmd" workitem get --work-item-id 16221411 --project-key j7wqr7
& "$env:APPDATA\npm\meegle.cmd" workitem get --work-item-id 16221436 --project-key j7wqr7
```

---
## 1. 结论

**VERDICT: PARTIAL**

阶段顺序、脚本真实性、YAML 合法性、脱敏有效性、PR 模板与 CODEOWNERS 这几项**均已独立验证通过**；
但共同要求第 8 节的**远端交付闭环（建仓 → 推送 → PR → 分支保护 → 合并 → 合并后门禁与部署）在本仓库中并未真正跑通**，
且交付自检脚本存在**一处会掩盖自身失败的判定缺陷**，发布流水线的产物命名与其自检口径**对不上**。

共同要求第 10 节第 1 条写明：「完成指业务场景、质量门禁、Git 合并、自动部署及讲解视频全部达到要求」。
本迭代交付的是**机制与工具**，不是**远端闭环的达成**；仓库自身对此是诚实登记的（多处标「待补证」），
因此不判 FAIL——但按题面口径，第 8 节的远端动作**尚不可核验**，属未完成项。

| 验收项 | 结论 |
|---|---|
| 阶段顺序（需求→原型→计划→约束→开发→测试） | **通过**（严格有序，无倒序） |
| 需求-原型-计划-实现一致性（D-01~D-09 落点） | **部分通过**（3 条完全落点：D-03/D-08/D-09；3 条部分：D-01/D-02/D-04；3 条未落点：D-05/D-06/D-07） |
| 脚本真实性与可复现性 | **部分通过**（脚本真实、无假实现；但自检存在自指误报缺陷，已在本次验收过程中被开发者修复并复验通过） |
| 发布流水线可执行性 | **部分通过**（YAML 合法、步骤真实；但产物命名与 D-07 校验口径不匹配） |
| 泄密与脱敏 | **通过**（无明文令牌；设备码掩码规则有形式覆盖缺口，见发现 6） |
| PR 模板与 CODEOWNERS | **通过** |
| 诚实性核查 | **部分通过**（多处如实登记待补证；但有 10 行陈述与实际状态不符，涉及 6 份文档，见 §8 逐条对照表） |

### 发现项按严重度汇总

| 严重度 | 数量 | 编号 |
|---|---|---|
| 严重 | 2 | F-1、F-2 |
| 中等 | 4 | F-3、F-4、F-5、F-6 |
| 轻微 | 4 | F-7、F-8、F-9、F-10 |

---
## 2. 检查 1：阶段顺序

**结论：通过。** v2.4.0 迭代在 `dev/05` 上严格按「需求 → 原型 → 计划 → 约束 → 开发 → 测试」推进，无倒序。

实测提交序列（`git log --oneline --reverse 94aff98..HEAD`）：

| 序 | 提交 | 阶段 | 实际改动文件 | 提交时间 |
|---|---|---|---|---|
| 1 | `e58f1ce` | 需求 | `docs/01-需求分析.md`（新增 §12） | 09:46:58 |
| 2 | `5a9c5e8` | 原型 | `docs/02-原型设计.md`（新增 §12） | 09:47:25 |
| 3 | `1d10341` | 计划 | `docs/03-任务规划.md`（新增 T-79~T-88、§8） | 09:47:52 |
| 4 | `dab71f3` | 约束 | `AGENTS.md`、`docs/agents/AGENTS-v2.0.md`、`docs/agents/AGENTS-读取记录.md` | 09:48:46 |
| 5 | `2411cc8` | 开发 | `.github/CODEOWNERS`、`.github/PULL_REQUEST_TEMPLATE.md`、`.github/workflows/release.yml`、`docs/10-交付与发布说明.md`、`scripts/gitee_mcp.py`、`scripts/verify-delivery.py`、`scripts/verify-gate.py` | 09:53:55 |
| 6 | `7bf0bd3` | 缺陷修复 | `scripts/export_session.py`、会话导出件、`evidence/gitee-mcp/*` | 09:53:55 |
| 7 | `a9a19aa` | 缺陷修复 | `scripts/verify-delivery.py` | 09:55:58 |
| 8 | `1cc5233` | 测试 | `scripts/test-delivery-checks.py`、`evidence/delivery-check.*`、`evidence/gate-dev05.log` | 09:55:58 |
| 9 | `5646419` | 缺陷修复 | `scripts/export_session.py`、测试夹具 | — |
| 10 | `5a01569` | 测试 | 重导会话清单与时序清单、刷新交付自检 | — |
| 11 | `d4138bb` | 开发 | `scripts/github_mcp.py`、`evidence/github-mcp/*` | — |
| 12 | `17fd717` | 文档 | `docs/11-班级群提交消息.md`、`docs/12-讲解视频带时间点目录.md` | — |
| 13 | `c7ebd52` | 测试 | `evidence/skill-quota-guard.md` | — |
| 14 | `03c2743` | 测试 | `evidence/gate-dev05.log` | — |

**关键判定：约束（`dab71f3`，09:48:46）严格早于本迭代任何业务代码（`2411cc8`，09:53:55）**，
满足共同要求第 3 节「开发前分析业务及验收条件」与本迭代自设的迭代纪律第 4 项「约束先于代码」。

拓扑父链亦连续无分叉（`git rev-list --parents -n 1`）：
`e58f1ce → 5a9c5e8 → 1d10341 → dab71f3 → 2411cc8 → 7bf0bd3 → a9a19aa → 1cc5233`，
每个提交的父提交均为序列中的前一个。

**一处需要澄清但不构成倒序的现象**：`evidence/过程时序清单.md` 的「关键产物首次落盘时刻」表显示
「首个业务代码文件 19:35:37」早于「原型设计 20:15:59」与「任务规划 20:18:09」。
这是**首次迭代（v1.0~v2.3.0）的历史事实**，不是 v2.4.0 的倒序；
仓库已在 `docs/03-任务规划.md` §8 第 318–319 行以 R-13 如实登记该偏差并声明「不回填、不倒签」。
本次验收确认该登记与 `evidence/过程时序清单.md` 的机器推导结果一致，**不额外计为缺陷**。

---
## 3. 检查 2：需求-原型-计划-实现一致性（D-01~D-09 落点）

`docs/01-需求分析.md` §12.2 的 D-01~D-09 逐条落点核对：

| 编号 | 声明的落点 | 实际是否存在 | 判定 |
|---|---|---|---|
| D-01 | `scripts/gitee_mcp.py`、`evidence/gitee-mcp/` | 脚本存在；目录存在但**只有 `tools_list.json`**，无 `create_repo` 报文 | 部分（见 F-1） |
| D-02 | `git ls-remote` 读取结果、提交历史 | 提交历史有；**`git remote -v` 为空**，无远端可读 | 部分（见 F-1） |
| D-03 | `evidence/过程时序清单.md`、提交历史 | 均存在 | 通过 |
| D-04 | PR 描述正文（归档） | 模板 `.github/PULL_REQUEST_TEMPLATE.md` 存在且要素齐全；**无归档 PR 正文** | 部分（见 F-2） |
| D-05 | `evidence/gitee-mcp/`、`evidence/branch-protection.json` | **两个文件均不存在** | 未落点（见 F-2） |
| D-06 | 合并提交对象、`git log --graph` | `main` 仍只有初始化提交 `d75aebd`，无合并提交 | 未落点（见 F-2） |
| D-07 | `evidence/main-gate-*.log`、`evidence/deployment-main.log` | **均不存在**，且流水线也不会生成该命名（见 F-3） | 未落点（见 F-3） |
| D-08 | `scripts/stop-local.ps1`、`evidence/deployment*.log` | 脚本与日志均存在 | 通过 |
| D-09 | `docs/10-交付与发布说明.md`、`skills/acceptance-audit` C-14 | 两者均存在，C-14 实跑 `PASS_WITH_PENDING pass=53 fail=0` | 通过 |

**原型侧**：`docs/02-原型设计.md` §12.4 提供 D-01~D-09 的逐条对应表（D-01→12.1 步骤①/12.2 第①行 … D-09→12.2 第⑧行），
§12.2 的 8 行确实逐行对应 9 条口径，**映射关系自洽**。

**计划侧**：`docs/03-任务规划.md` T-79~T-88 覆盖 D-01~D-09，T-87（远端链路实跑）、T-88（合并提交上的门禁与部署复跑）
明确标注「待补证」。T-84 声明的落点是 `scripts/verify-gate.ps1` 与 `scripts/deploy-local.ps1`（`-Commit` 参数），
**前者不存在、后者无 `-Commit` 参数**（见 F-8）。

**约束侧**：`AGENTS.md` §10（v2.0）6 条与 D-01~D-09 一一对应，来源标注完整。

### 没有落点的条目

- **D-05（主分支保护读取结果）**：`evidence/branch-protection.json` 不存在，`evidence/gitee-mcp/` 下无保护类报文。
- **D-06（合并提交保留原始提交）**：`main` 无合并提交。
- **D-07（合并提交上的门禁与部署）**：`evidence/main-gate-*.log` 与 `evidence/deployment-main.log` 均不存在。

这三条与 D-01/D-02/D-04 同源——**远端动作未执行**，仓库以「待补证」如实登记（`docs/10-交付与发布说明.md` §5、§8）。
因此不按「虚报」计为缺陷，而是按共同要求第 8 节的**未完成项**计入 F-1/F-2。

---
## 4. 检查 3：脚本真实性与可复现性

### 4.1 `scripts/verify-delivery.py` — 真实实现，无假实现

逐函数核对：全部 8 个检查函数均调用真实工具或真实文件系统，
无任何硬编码返回值、无「直接 return PASS」的分支：
`check_commit_messages` 调 `git log`；`check_mcp_repo_creation` / `check_pr_description` /
`check_branch_protection` 解析真实 JSON；`check_merge_and_gate` 调 `git log -1 --format=%H%x09%P%x09%s main`；
`check_secret_scan` 逐文件正则扫描；`check_remote_branches` 调 `git ls-remote`。

`parse_message` 是**逐段解析**（先类型、再版本号、再编号，每步都校验分隔空格），
非单条正则，**畸形消息 `[feat] [v2.4.0][#16221249] 缺少分隔空格` 会被正确判为不合规**。
这正是提交 `a9a19aa` 所声称修复的问题，实测确认修复有效。

### 4.2 `scripts/test-delivery-checks.py` — 用例真在断言，无恒真写法

`check()` 实现为 `if actual == expected: PASSED += 1 else: FAILED.append(name)`，**失败会进 FAILED 列表并使退出码为 1**，
不存在恒真断言。用例覆盖 4 组：提交消息格式（9 例）、令牌扫描（4 例）、会话导出脱敏（8 例）、合并状态判定（2 例）。

实跑（最终 HEAD）：`合计：PASS 23 / FAIL 0`，`EXIT=0`。

### 4.3 可复现性缺陷（本报告的核心发现 F-4）

**在验收开始时（HEAD = `1cc5233`），提交的 `evidence/delivery-check.md` 无法复现。**

提交的证据声明：

```
| 8 | D-09 仓库无令牌明文 | PASS | 扫描 101 个文本文件，未命中令牌模式 |
合计：PASS 2 / PENDING 6 / FAIL 0
```

但在同一提交上实跑 `scripts/verify-delivery.py`：

```
[verify-delivery] PASS=1 PENDING=6 FAIL=1
  [FAIL   ] D-09 仓库无令牌明文：scripts/test-delivery-checks.py: 疑似令牌赋值 access_token = （凭据，已掩码）；…
EXIT=1
```

用 clean worktree 排除工作区干扰后复现一致：

| 被检树 | 扫描文件数 | D-09 结果 |
|---|---|---|
| `2411cc8`（测试文件尚未加入） | 96 | FAIL（命中 `docs/06-会话导出.md` 中一个真实 32 位串） |
| `7bf0bd3` | 98 | PASS ← **提交的证据就是在这个状态下生成的** |
| `1cc5233`（测试文件已加入） | 102 | **FAIL** |

**根因**：`7bf0bd3` 修复了导出脱敏，`1cc5233` 又新增了 `scripts/test-delivery-checks.py`，
该测试文件**在源码里硬编码了 `access_token = （凭据，已掩码）`、
`Bearer （凭据，已掩码）`、32 位十六进制串 `20ffb6b0…` 三类「凭据形态」字面量**，
而 `verify-delivery.py` 的 `check_secret_scan` 扫描**全仓库**（含 `scripts/`），于是自己命中自己。
证据是在 `1cc5233` 之前生成的（时间戳 `09:53:31`），提交时未重跑，因此**归档证据与仓库状态不一致**。

**影响**：共同要求第 6 节第 8 条「AI 自述『通过』不作为通过依据」要求归档证据必须可复跑；
若教师按提交证据判定，会得到与实际运行相反的结论（证据说 PASS，实际退出码 1）。

**当前状态**：验收过程中开发者已提交 `5646419`，把测试夹具改为**运行时拼接**
（`fake_hex32 = ("a1b2c3d4" * 3) + "abcdef12"`、`fake_value = "x" * 32`）并补充了 Bearer 掩码用例，
`5a01569` 随后刷新了 `evidence/delivery-check.*`。
**最终 HEAD（`03c2743`）上已复验通过**：

```
[verify-delivery] PASS=2 PENDING=6 FAIL=0
  [PASS   ] D-09 仓库无令牌明文：扫描 108 个文本文件，未命中令牌模式
EXIT=0
```
（clean worktree 实测 108 个文件，提交证据写 109 —— 差 1 个是 `evidence/delivery-check.md` 自身被计入扫描，
属自指计数口径差异，不影响判定。）

**本报告按最终 HEAD 状态判定，但保留该发现**，因为它证明归档证据与仓库状态曾不一致。

### 4.4 `scripts/verify-gate.py` — 真实执行，可复现

实跑 `python scripts/verify-gate.py --commit HEAD --out $T/gate-final.log`：

```
[verify-gate] commit=03c2743bbfa6 failed=0 out=...\gate-final.log
EXIT=0
```

日志内容与仓库当前代码一致：gofmt 无输出、`go vet` 退出码 0、
各包覆盖率 `domain 97.3% / httpapi 87.7% / service 87.7% / store 94.4% / upstream 87.1%`、
整体 `89.4%`，与 `evidence/coverage-summary.txt` 相同。

**一处证据滞后（非缺陷，仅记录）**：`evidence/gate-dev05.log` 归档的被检提交是 `c7ebd52`（其父提交），
而非最终 HEAD `03c2743`——这是必然的（该文件由 `03c2743` 本身提交，日志不可能包含自身）。
实测两个提交的业务代码完全一致（`03c2743` 只改了 `.log`），覆盖率数值可比对。

### 4.5 `scripts/gitee_mcp.py` / `scripts/github_mcp.py` — 真实 MCP 客户端

`gitee_mcp.py` 用 `urllib` 直接发 JSON-RPC 到 `https://api.gitee.com/mcp`（Streamable HTTP），
兼容 JSON 与 SSE 两种返回，归档原始请求/响应。**不是假实现**。

已归档的 `evidence/gitee-mcp/20261112-094457-tools_list.json` 经我解析确认为**真实报文**：
`tool_count=25`，工具列表实际含 25 项，`create_repo` / `create_pull` / `merge_pull` / `manage_pull_review` 均在列。

`github_mcp.py` 通过 Docker 跑官方 `ghcr.io/github/github-mcp-server`，
`evidence/github-mcp/20261112-100341-create_repository.json` 记录的**是失败调用**：
`"isError": true`，文本为 `failed to create repository: POST https://api.github.com/user/repos: 403 Resource not accessible by personal access token`。
**这是诚实归档失败，不是伪造成功**。

### 4.6 业务实跑（独立验证核心业务确实可跑通）

本机 8080 端口已被既有进程占用（`Get-NetTCPConnection` 显示 OwningProcess 23504），
故我在 8091 端口另起**干净数据库**实例并用 Python 复刻 `scripts/smoke-test.sh` 的全部断言：

```
[PASS] healthz (200) / readyz (200) / ops login / admin login / 未认证 401 / 管理员越权 403
[PASS] 列出租户(tenants=2) / 调整额度 / 缺原因 400 / 越界 400
[PASS] 创建凭证(secret 非空) / 跨租户建凭证 400 / 运营代建 403
[PASS] 应用登录 / 调用 200(status=success) / 幂等重放(replayed=True) / 改参数 409
[PASS] 缺编号 400 / 编号非法 400 / 应用查询本凭证请求 200 / 应用访问管理员接口 403
[PASS] 查询用量(success=1) / 日终报表(consistent=True) / 未指定租户 400
[PASS] 停用租户 / 停用后调用 409 / 停用缺原因 400
TOTAL PASS=27 FAIL=0
```

`scripts/scenario-verify.py` 在干净实例上实跑：`passed=73 failed=0 exit=0`。
**业务闭环、幂等、越权、并发额度上限、日终一致性均真实成立**，非静态页面或写死成功。

---
## 5. 检查 4：发布流水线

### 5.1 YAML 合法性 — 通过

`yaml.safe_load` 解析两个工作流均成功：

```
== .github/workflows/release.yml : YAML PARSE OK
   top keys: ['name', 'True', 'permissions', 'jobs']
   trigger: {'push': {'branches': ['main']}, 'workflow_dispatch': None}
   job gate   | needs= None            | steps= 9
   job smoke  | needs= ['gate']        | steps= 5
   job deploy | needs= ['gate','smoke']| steps= 4
== .github/workflows/ci.yml : YAML PARSE OK
```

（`'True'` 是 YAML 1.1 把裸 `on` 解析成布尔键的正常表现，GitHub Actions 侧按 `on` 处理，**非缺陷**。）

### 5.2 触发与门禁链路 — 通过

`release.yml` 在 `push` 到 `main` 时触发，`workflow_dispatch` 可手动触发；
`gate → smoke → deploy` 三级依赖正确（`needs` 实测为 `None` / `['gate']` / `['gate','smoke']`）。
`ci.yml` 额外覆盖 `pull_request → main`，与共同要求第 8 节第 5 条「要求通过 PR 合并并通过规定的状态检查」相容。

### 5.3 引用的脚本与文件是否真实存在 — 通过

| 引用 | 存在 |
|---|---|
| `scripts/coverage-summary.py`（release.yml:52、ci.yml 内联同逻辑） | 是 |
| `scripts/smoke-test.sh`（release.yml:88、ci.yml:170） | 是 |
| `./cmd/mockupstream`、`./cmd/server`（build 目标） | 是 |
| `evidence/smoke-test.log`（release.yml:93 上传路径） | 是（但见 F-5） |

`ci.yml` 的覆盖率门禁我用 `yaml.safe_load` 抽出实际 heredoc 文本逐行核对：
逻辑为「整体 < 85% 或任一核心包 < 85% 即 `sys.exit(1)`」，**与题面口径一致**。

### 5.4 发现 F-3：流水线产物命名与 D-07 校验口径对不上（中等）

`verify-delivery.py` 的 D-07 校验这样找证据（第 237 行）：

```python
logs = sorted(ev.glob("main-gate-*.log")) + sorted(ev.glob("deployment-main*.log"))
```

但 `release.yml` **从不生成这两个文件名**：
它的产物是 GitHub **artifact 名**（`main-gate-coverage-${{ github.sha }}`、`main-gate-smoke-…`、`main-deploy-…`），
上传路径是 `coverage.out` / `coverage-summary.txt` / `evidence/smoke-test.log` / `deploy/bin/**` / `deploy/data/**`。
`GATE_COMMIT` 环境变量在第 30 行写入 `$GITHUB_ENV` 后**再无任何步骤引用它**。

**影响**：即使流水线在 main 上跑成功、门禁全绿，
`evidence/main-gate-<sha>.log` 与 `evidence/deployment-main.log` 仍不会出现，
D-07 会一直停在 PENDING——**「流水线可执行」与「流水线能产出验收脚本认定的证据」是两件事，当前只有前者**。
共同要求第 8 节第 7 条要求「记录部署提交、流水线及制品」，D-07 口径要求「日志中出现该 SHA」，当前无载体。

**最小整改**：在 `release.yml` 的 `gate` 与 `deploy` 步骤各加一行，把 `git rev-parse HEAD` 写入
`evidence/main-gate-<短SHA>.log` 与 `evidence/deployment-main.log`（可直接调用已存在的 `scripts/verify-gate.py`，
其日志头部已包含完整 SHA 与提交标题）。

### 5.5 发现 F-5：`evidence/smoke-test.log` 是过期且失败的归档（中等）

`release.yml` 第 93 行上传 `evidence/smoke-test.log`，但该文件在仓库里是 **2026-11-11 20:19:36 的旧文件**，
其内容自报 `退出码：1`、`通过 38 项，失败 1 项`，失败项为「应用查询本凭证请求」。

对照 `evidence/deployment.log`：同一晚 20:24:36 起已有多次 `通过 39 项，失败 0 项`（最近一次 2026-11-12 09:04:05），
即**归档的 smoke 记录是一次失败运行，而仓库内更新的成功记录没有回填到这个文件**。
`git log -- evidence/smoke-test.log` 显示它自 `fbc4203`（v2.2.0）起再未更新。

**影响**：
(a) `docs/10-交付与发布说明.md` §7 与 `docs/04-部署与使用说明.md` §104 都把该文件列为「业务冒烟」证据，
教师按此文件读到的是一次失败运行；
(b) 流水线在 CI 上传该文件时，会把这份**旧的失败记录**当作本次运行的冒烟证据上传
（`scripts/smoke-test.sh` 只输出到 stdout，不写该文件，见我对脚本的通读）。

**最小整改**：用干净数据库重跑 `scripts/smoke-test.sh` 并回填 `evidence/smoke-test.log`（我已实测干净环境下 27/27 断言通过）；
并在流水线中改为 `bash scripts/smoke-test.sh 2>&1 | tee evidence/smoke-test.log`。

---
## 6. 检查 5：泄密与脱敏

### 6.1 明文令牌扫描 — 通过

对**全部追踪文件**（`git grep`）实测：

| 模式 | 命中 |
|---|---|
| `ghp_` / `github_pat_` / `gho_` / `ghs_` / `ghu_` / `ghr_` | 仅命中 `scripts/export_session.py` 的**正则模式字面量**与 `evidence/会话事件清单.jsonl:2975` 中**记录该正则源码的会话片段**；**无真实令牌** |
| `Bearer <16+ 字符>` | **0 命中** |
| `Gitee 32 位十六进制串`（独立出现） | **0 命中**（导出件中已全部替换为「（32 位十六进制串，已掩码）」） |
| 演示口令 `ops-****` / `admin-a-****` / `admin-b-****` | 仅出现在 `cmd/server/main.go`、`cmd/server/main_test.go`、`internal/**/*_test.go`、`scripts/smoke-test.sh`、`scripts/scenario-verify.py`、`skills/acceptance-audit/scripts/acceptance_audit.py`、以及会话导出件中记录这些文件源码的片段 |

**文档侧**：全仓库 `docs/` 下无明文演示口令；`docs/04-部署与使用说明.md:119-121` 以
`ops-****` / `admin-a-****` / `admin-b-****` 掩码给出，符合「完整值只保留在 `cmd/server/main.go`」的约定。

### 6.2 设备码脱敏的形式覆盖缺口（发现 F-6，中等）

`scripts/export_session.py` 的设备码规则只覆盖 **CLI 参数形态**：

```python
(re.compile(r"--device-code-value\s+'[0-9a-fA-F\-]{16,}'"), …)
(re.compile(r"(--device-code-value\s+)[0-9a-fA-F\-]{16,}"), …)
```

但会话工具输出里设备码还以 **JSON 形态** 出现（`"device_code": "<uuid>"`）。实测最终 HEAD：

```
=== HEAD committed 会话事件清单.jsonl ===
   unmasked device_code count: 4
       （UUID 一次性凭据，已掩码）
       （UUID 一次性凭据，已掩码）
       （UUID 一次性凭据，已掩码）
       （UUID 一次性凭据，已掩码）
```

`docs/06-会话导出.md` 中该形态为 0 命中（Markdown 导出做了摘要截断）。

**影响判断**：这 4 个是 Meegle CLI 的**一次性设备码**，且已过期、无法复用，
不构成实际凭据泄露；共同要求第 3 节「禁止泄露密钥和用户敏感信息；日志、截图和对话导出均须脱敏」
要求的是「导出须脱敏」。仓库自身在 `docs/06-会话导出.md` 中对**同一批**设备码的 CLI 形态已做掩码，
说明其脱敏意图覆盖设备码，JSON 形态属**规则覆盖不全**。

**最小整改**：在 `REDACT_RULES` 中补一条
`(re.compile(r"(\"device_code\"\s*:\s*\")[0-9a-fA-F\-]{16,}")` → `\1（已用完的一次性设备码，已掩码）"`，
然后重导 `evidence/会话事件清单.jsonl` 与 `docs/06-会话导出.md`。

### 6.3 脱敏规则是否误伤必要证据 — 通过（关键复核项）

这是本轮的重点复核项，逐条实测：

| 证据类型 | 是否被误伤 | 实测依据 |
|---|---|---|
| **Git 提交 SHA（40 位十六进制）** | **否，完整保留** | 导出件中检出 6 个唯一 40 位十六进制串（如 `089c66a1e6f3c736d93c1eb450ec2f5f1a87cbc4`），可正常用于复核 |
| **会话 ID（`mvs_` 前缀）** | **否，保留** | 正则含负向断言 `(?<!mvs_)`；实测 `mvs_9c316f33…` 在导出件中可见 |
| **Meegle CLI client id（`cli_` 前缀）** | **否，保留** | 同上 `(?<!cli_)`；`cli_6c1cee5056d04561827106d4cb1a2e9e` 在 `docs/06-会话导出.md:384` 可见 |
| **UUID 型 turn_id / message_id** | **否** | 32 位规则要求「连续 32 位十六进制」，UUID 含连字符不匹配；导出件中 UUID 完整保留 |
| **SHA-256 摘要** | 会被当作 32 位串掩码（见下） | — |

**需要注意的一点（轻微，F-10）**：导出件中出现了
`expect = "（32 位十六进制串，已掩码）"  # placeholder` —— 这是一个 **SHA-256 校验和占位符**，
被 32 位规则误伤。它不影响可复核性（只是占位符，不是证据），但说明规则对「非凭据的 32 位十六进制」
缺乏区分。`verify-delivery.py` 的 `SAFE_HEX32_CONTEXT` 已针对这一点做了上下文豁免
（`sha256|digest|checksum|commit|sha|blob|oid|cli_|mvs_`），**导出脚本没有对应豁免**，
两者口径不一致。

### 6.4 掩码后的证据是否仍可复核 — 通过

会话导出件仍完整保留：时间戳、`seq` 序号、`tool` 名、工具参数结构、退出码摘要、
以及**全部 40 位提交 SHA**。因此「哪一步在什么时间做了什么、产出了什么」的对应关系未被破坏，
符合 `export_session.py` 注释所述的设计意图（「只针对一次性凭据做掩码，其余内容逐字保留」）。

---
## 7. 检查 6：PR 模板与 CODEOWNERS

### 7.1 `.github/PULL_REQUEST_TEMPLATE.md` — 通过

共同要求第 8 节第 4 条要求 PR 附：**需求与缺陷编号、变更说明、实际测试结果、覆盖率、验收报告及 AI/Skill 记录位置**。
模板逐项覆盖：

| 题面要求 | 模板位置 | 内容 |
|---|---|---|
| 需求与缺陷编号 | §1 关联工作项编号（必填） | `#16220913` 格式示例 + Meegle 核验说明 |
| 变更说明 | §2 变更说明（必填） | 改了什么/为什么/对业务口径有无影响 |
| 实际测试结果 | §3 实际测试结果（必填，粘贴真实输出摘要，不要写「已通过」） | 6 行门禁表格：gofmt / vet / 单测 / 覆盖率 / 竞态 / 冒烟 / 场景验证 |
| 覆盖率 | §4 覆盖率（必填） | 整体 % + 核心包 % + 原件位置 |
| 验收报告 | §5 独立验收报告（必填，填写路径） | `evidence/independent-test-report.md`、`evidence/independent-acceptance-report.md` |
| AI/Skill 记录位置 | §6 AI 使用与 Skill 记录位置（必填） | `docs/09-AI使用说明.md`、`docs/08-Skill使用记录.md`、`docs/06-会话导出.md` |

另有 §7 自检清单 4 项（含「main 未被直接推送」「仓库内不含任何密钥明文」）。
**6 项要素全覆盖，无遗漏**。

### 7.2 `.github/CODEOWNERS` — 通过

用 `git ls-files` 核对各路径在仓库中真实存在且非空：

| CODEOWNERS 路径 | 追踪文件数 | 存在 |
|---|---|---|
| `/internal/domain/` | 2 | 是 |
| `/internal/store/` | 3 | 是 |
| `/internal/service/` | 7 | 是 |
| `/scripts/` | 13 | 是 |
| `/.github/` | 4 | 是 |
| `/evidence/` | 24 | 是 |
| `/docs/` | 17 | 是 |

无引用不存在路径的情况。

**一点如实说明（非缺陷）**：CODEOWNERS 中的评审人一律写 `@xishiyao`，
文件头部注释与 `docs/10-交付与发布说明.md` §8 均声明「替换前该项属于待补证」，且 `evidence/branch-protection.json`
尚不存在。**登记与实际状态一致**，不构成虚报。

---
## 8. 检查 7：诚实性核查（逐份文档）

以下逐条列出「文档陈述 → 实际状态 → 判定」。凡与实际不符的均计入发现项。

### 8.1 `docs/01-需求分析.md`

| # | 陈述 | 实际 | 判定 |
|---|---|---|---|
| 1 | §12.1「远端平台 **Gitee**（`https://gitee.com/xishiyao/d5.git`），使用 Gitee 官方 MCP Server」 | `evidence/gitee-mcp/20261112-094457-tools_list.json` 确为 `gitee-mcp-remote` 端点、25 工具含 `create_repo` —— **MCP 服务与工具清单属实** | 一致 |
| 2 | §12.1「本账号无 GitHub 凭据」 | **与最终 HEAD 矛盾**：`d4138bb` 提交的 `evidence/github-mcp/20261112-100341-create_repository.json` 证明账号**有** GitHub PAT 并成功连上官方 MCP（`serverInfo: github-mcp-server v2.1.0`，`tools/list` 正常返回），只是 PAT **缺建仓权限**（403 Resource not accessible by personal access token）。另 `scripts/github_mcp.py` 已入库 | **不一致 → F-9** |
| 3 | §12.2 D-01「必须存在一条 `tools/call` 报文，工具名为 `create_repo`…一并归档」，落点 `evidence/gitee-mcp/` | `evidence/gitee-mcp/` 下**只有 `tools_list.json`**，无 `create_repo` 报文 | 口径与「未完成」并存，**未虚报**（脚本判 PENDING 一致） |
| 4 | §12.3 第 2 条「`main` 仅含初始化提交，业务提交全在 `dev/05`」 | `git log --oneline main` = `d75aebd [docs] [v2.1.0] 初始化题目 05 项目仓库说明`（仅 1 条）；`dev/05` 含全部业务提交 | **一致**（本地可核验部分） |

### 8.2 `docs/02-原型设计.md`

| # | 陈述 | 实际 | 判定 |
|---|---|---|---|
| 1 | §12.2 第 ⑦ 行「`scripts/verify-gate.ps1` / `scripts/deploy-local.ps1`，输入=合并提交 SHA」 | **`scripts/verify-gate.ps1` 不存在**（实际是 `scripts/verify-gate.py`）；`scripts/deploy-local.ps1` **无 `-Commit` 参数**（实测参数仅 `Port/UpstreamPort/DbPath/LogFile/SkipSmoke/KeepRunning`），且脚本内**无任何 `git rev-parse`/SHA 记录** | **不一致 → F-8** |
| 2 | §12.4 D-01~D-09 对应表 | 9 条口径逐条有原型行对应，表结构自洽 | 一致 |

### 8.3 `docs/03-任务规划.md`

| # | 陈述 | 实际 | 判定 |
|---|---|---|---|
| 1 | T-84 落点 `scripts/verify-gate.ps1`、`scripts/deploy-local.ps1`（`-Commit` 参数），状态「已落盘」 | 同 8.2 第 1 条：**两处路径/参数均不属实** | **不一致 → F-8** |
| 2 | §8 迭代纪律「在每次迭代交付时用 `scripts/process-timeline.py` 复核」 | 该脚本只输出**全仓库首次落盘**的 8 个里程碑（`scripts/process-timeline.py:119-126` 的 `first_time_for`），**无法区分迭代**；实跑输出仍只有 v1.0 时段的 8 行，无 v2.4.0 专属复核段 | **部分不一致 → F-7（轻微）** |
| 3 | T-87 / T-88 状态「待补证」 | 远端产物确实未生成 | 一致（诚实登记） |

### 8.4 `docs/05-假设清单.md`

| # | 陈述 | 实际 | 判定 |
|---|---|---|---|
| 1 | N-04「登录页不展示明文口令」 | 实测 `internal/httpapi/web/templates/login.html` **不含** `ops-****` / `admin-a-****` | 一致 |
| 2 | N-05「以本机真实进程部署作为教学验收环境」 | `evidence/deployment.log` 记录了真实的双进程启动、健康检查与冒烟 | 一致 |
| 3 | N-06「工作项编号：由沿用示例编号纠正为真实且已核验的编号」 | 见 8.7 | 一致 |

### 8.5 `docs/07-提交规范说明.md`

| # | 陈述 | 实际 | 判定 |
|---|---|---|---|
| 1 | §4 开篇「本项目 `feat` / `fix` 提交使用的编号…**逐条用 `meegle workitem get` 核验通过**，原始输出见 `evidence/meegle-workitem-verification.log`」 | 该日志只核验了 **4 个**编号（16220913 / 16220917 / 16220924 / 16220925）。而 v2.4.0 的 `feat`/`fix` 提交用的是 **16221249 / 16221411 / 16221436**，**均不在该日志中** | **不一致 → F-2** |
| 2 | §4.1「当前 4 个编号**全部通过**」 | 4 个编号的核验判定行确实存在 | 一致 |

### 8.6 `docs/09-AI使用说明.md`

| # | 陈述 | 实际 | 判定 |
|---|---|---|---|
| 1 | §6 第 1 行「无任何 GitHub 凭据（`GITHUB_TOKEN` / `GH_TOKEN` 均未设置）」 | 同 8.1 第 2 条：**有** GitHub PAT 并已实际调用官方 MCP（403 是权限不足，不是无凭据） | **不一致 → F-9** |
| 2 | §5「所有『通过』结论都来自真实命令输出；未能执行的项…一律记『待补证』」 | 我实跑 `scripts/test-delivery-checks.py`（23/23）、`scripts/verify-gate.py`（failed=0）、`scenario-verify.py`（73/0）均可复现 | 一致 |
| 3 | §7「事件清单与会话导出中的口令、密钥、令牌**未做删改**」 | **与事实相反**：`scripts/export_session.py` 明确做了掩码，且导出件中大量 `（已掩码）` 标记。此处「未做删改」应指「未删改步骤结构」，但字面表述与代码不符 | **不一致 → F-10（轻微）** |

### 8.7 `docs/10-交付与发布说明.md`

| # | 陈述 | 实际 | 判定 |
|---|---|---|---|
| 1 | §3 分支表「`main` … **受保护**（禁止直推 / 删除 / 强推，必须走 PR）」 | `evidence/branch-protection.json` 不存在；`evidence/gitee-mcp/` 无保护类报文；§5 同一文档又把该行列为「待补证」 | **文档内部自相矛盾 → F-2** |
| 2 | §2 工作项表列 5 个编号，含 `#16221249`，关联「v2.4.0 迭代的 `feat` 提交」 | 该编号**真实存在且相关**（我用 meegle CLI 独立核验：`题目05 远端交付链路：MCP 建仓、开发分支 PR、主分支保护、合并后门禁与部署`，空间 `j7wqr7` 可见）。但表中**漏列** `16221411`、`16221436` 两个 v2.4.0 `fix` 提交所用编号 | **索引不完整 → F-2** |
| 3 | §4「仓库地址 `https://gitee.com/xishiyao/d5.git`」 | `git remote -v` **为空**，本地无任何远端配置；该地址无法在仓库内自证 | 登记合理（远端信息本就只能在平台侧核），**不判缺陷** |
| 4 | §5 证据索引各行状态 | 「待补证」的 6 项确实未生成；「已落盘」的 3 项确实存在 | 一致 |

### 8.8 `evidence/过程时序清单.md`

| # | 陈述 | 实际 | 判定 |
|---|---|---|---|
| 1 | 「由 `scripts/process-timeline.py` 从会话事件清单反推」 | 我实跑该脚本，输出与文档一致（8 个里程碑 + 提交历史） | 一致 |
| 2 | 里程碑表显示「原型设计 20:15:59」「任务规划 20:18:09」晚于「首个业务代码 19:35:37」 | 这是 v1.0 历史事实；`docs/03` §8 以 R-13 如实登记该偏差 | 一致（诚实登记） |

### 8.9 `docs/11-班级群提交消息.md` / `docs/12-讲解视频带时间点目录.md`

`17fd717` 新增。属人工交付物模板与目录，我核对了其指向的文件路径存在性；
其中涉及远端地址/视频链接的部分标注为「待本人执行/待录制」，**未发现把未完成项写成已完成**。

### 8.10 独立验收报告 `evidence/independent-acceptance-report.md`

| # | 陈述 | 实际 | 判定 |
|---|---|---|---|
| 1 | C-13「`evidence/smoke-test.log` … 均在仓库内；我本地重跑命令与结果一致」 | 该文件记录的是**退出码 1、38 通过 1 失败**的旧运行；仓库内更新的成功运行只在 `evidence/deployment.log` 里 | **不一致 → F-5** |
| 2 | C-14「证据中无明文口令/密钥」 | 我实跑 `acceptance_audit.py` → `PASS_WITH_PENDING pass=53 fail=0`；且我的独立扫描未发现明文令牌 | 一致 |

---
## 9. 发现项清单（按严重度）

### 严重

#### F-1：共同要求第 8 节的远端交付闭环在本仓库完全未跑通，且无可核验证据

- **触发条件**：教师按共同要求第 8 节第 1~7 条逐条核验本仓库。
- **实际**：仓库无任何远端配置（`git remote -v` 为空），MCP 建仓、推送、PR、分支保护读取、合并、
  合并后门禁与部署**全部未执行**。唯一的 MCP 建仓尝试是 GitHub 官方 MCP 的 `create_repository`，
  且**失败**（`isError: true`，403 Resource not accessible by personal access token）。
  `evidence/gitee-mcp/` 仅有 `tools_list.json`；`evidence/branch-protection.json` 不存在；
  `main` 仍只有初始化提交 `d75aebd`。
- **影响**：违反共同要求第 8 节第 1 条（不得用手工创建/CLI 记录替代 MCP 创建证据）、
  第 4 条（PR 要素）、第 5 条（分支保护读取结果）、第 6 条（合并提交保留原始提交）、
  第 7 条（合并后重新门禁与部署）；直接触及第 7 节末句「Git 流程不合规…均不得判定整题通过」。
  注意：**这不是伪造**——仓库以「待补证」逐项如实登记（`docs/10` §5/§8、`docs/03` T-87/T-88、`docs/09` §6），
  但**按题面口径第 8 节仍未达成**。
- **证据**：`git remote -v` → 空；`git log --oneline main` → 仅 `d75aebd`；
  `evidence/gitee-mcp/calls.jsonl` 仅 1 行 `tools/list`；
  `evidence/github-mcp/20261112-100341-create_repository.json:48,51` → `isError: true` + 403；
  `python scripts/verify-delivery.py` → D-01/D-02/D-04/D-05/D-06/D-07 共 **6 项 PENDING**。
- **最小整改**：用具备建仓权限的 GitHub PAT（或等效平台）实际跑通建仓→推送→PR→保护→合并→合并后门禁与部署，
  并把六类报文（`create_repository` / `create_pull` / `manage_pull_review` / `merge_pull` /
  分支保护读取结果 / 合并提交上的门禁与部署日志）落盘到 `evidence/`。
  若平台确实不可得，须按题面第 1 节第 6 条由教师确认并出具书面说明。

#### F-2：工作项核验证据与提交实际使用的编号不匹配；交付索引不完整且自相矛盾

- **触发条件**：核对 `docs/07` §4 的「逐条核验通过」声明与 `docs/10` §2/§3 的索引。
- **实际**：
  (a) `evidence/meegle-workitem-verification.log` 只含 4 个编号的核验记录
      （16220913 / 16220917 / 16220924 / 16220925），
      **不含** v2.4.0 三个 `feat`/`fix` 提交实际使用的 **16221249 / 16221411 / 16221436**；
  (b) `docs/07` §4 开篇却声明「本项目 `feat` / `fix` 提交使用的编号…逐条用 `meegle workitem get` 核验通过」；
  (c) `docs/10` §2 索引**漏列** 16221411、16221436；
  (d) `docs/10` §3 断言 `main`「受保护」，而 §5 同文档把该列为「待补证」，**自相矛盾**。
- **补充（对仓库有利）**：我用 meegle CLI 独立核验，三个编号**都真实存在且与本题相关**：
  `16221249` =「题目05 远端交付链路：MCP 建仓、开发分支 PR、主分支保护、合并后门禁与部署」、
  `16221411` =「会话导出包含一次性设备码与访问令牌明文，违反证据脱敏要求」、
  `16221436` =「提交消息格式校验用单条正则，分段畸形时回溯放过不合规消息」，
  空间均为 `软件开发`（`project_key 6af44f0114103c3750cd053c` / `j7wqr7`）。
  **因此这是「核验记录未归档」与「索引不全」，不是「编造编号」**。
- **影响**：违反共同要求第 9 节「在 `feat`、`fix` 提交前，须由 AI 调用 meegle CLI 核验对应编号，并保留实际输出」；
  同时违反第 8 节第 9 条的「可审计交付」索引要求。
- **证据**：`evidence/meegle-workitem-verification.log` 中 `[判定]` 行仅 4 条；
  `git grep -n 'work-item-id' -- evidence/会话事件清单.jsonl` → 仅 `16220913`/`16220917`/`69744992`；
  `docs/07-提交规范说明.md:41`；`docs/10-交付与发布说明.md:30`（缺 2 条）与 `:43` vs `:68`（矛盾）。
- **最小整改**：跑 `python scripts/verify-workitem.py --project-key j7wqr7 --ids 16220913 16220917 16220924 16220925 16221249 16221411 16221436 --out evidence/meegle-workitem-verification.log`，
  并把 3 个新编号补进 `docs/07` §4.1 与 `docs/10` §2；把 `docs/10` §3 的「受保护」改为「待补证（见 §5）」。

### 中等

#### F-3：发布流水线的产物命名与 D-07 校验口径不匹配（见 §5.4）

- **触发条件**：合并到 `main` 后流水线跑完，再跑 `scripts/verify-delivery.py`。
- **实际**：`verify-delivery.py:237` 找 `evidence/main-gate-*.log` 与 `evidence/deployment-main*.log`；
  `release.yml` 只上传 `coverage.out` / `coverage-summary.txt` / `evidence/smoke-test.log` / `deploy/bin/**` / `deploy/data/**`，
  artifact 名为 `main-gate-coverage-<sha>` 等；`GATE_COMMIT` 写入后**再无引用**。
- **影响**：共同要求第 8 节第 7 条「记录部署提交、流水线及制品」在流水线侧无落点；
  D-07 永远 PENDING，合并后的门禁/部署无法被仓库自身脚本认可。
- **最小整改**：在 `release.yml` 的 `gate`/`deploy` 步骤调用已存在的 `scripts/verify-gate.py`，
  输出到 `evidence/main-gate-<短SHA>.log`；deploy 步骤把 `git rev-parse HEAD` 写入 `evidence/deployment-main.log`。

#### F-4：提交的交付自检证据曾与仓库状态不一致（自检脚本自指误报）

- **触发条件**：在提交 `1cc5233` 上重跑 `python scripts/verify-delivery.py`。
- **实际**：归档证据写 `D-09 PASS / 扫描 101 个文本文件`；实跑为 `D-09 FAIL`、退出码 1。
  根因是 `scripts/test-delivery-checks.py` 硬编码了「凭据形态」测试夹具字面量，
  被 `check_secret_scan` 的全仓库扫描命中自己。
  clean worktree 逐提交复现：2411cc8→96 文件 FAIL、7bf0bd3→98 文件 PASS（证据即在此态生成）、
  1cc5233→102 文件 FAIL。
- **影响**：违反共同要求第 6 节第 8 条「AI 自述『通过』不作为通过依据」与第 7 节「教师将独立运行验收用例」——
  归档证据此时不能作为通过依据。
- **当前状态**：**已修复**。`5646419` 把夹具改为运行时拼接，`5a01569` 刷新证据；
  最终 HEAD 实测 `PASS=2 PENDING=6 FAIL=0`、`EXIT=0`，`test-delivery-checks.py` 23/23 通过。
- **最小整改**：无需再整改；但建议在 `verify-delivery.py` 的 `SELF_OUTPUTS` 之外，
  把 `scripts/test-*.py` 纳入「测试夹具白名单」或统一采用运行时拼接，避免同类回归。

#### F-5：`evidence/smoke-test.log` 是过期且失败的归档（见 §5.5）

- **触发条件**：按 `docs/10` §7 与 `docs/04` §104 查「业务冒烟」证据；或流水线在 CI 上传该文件。
- **实际**：文件头 `执行时间 2026-11-11 20:19:36`、`退出码：1`、`通过 38 项，失败 1 项`；
  `git log -- evidence/smoke-test.log` 显示自 `fbc4203` 起未更新；
  而 `evidence/deployment.log` 中 20:24:36 起有多次 `通过 39 项，失败 0 项`。
- **影响**：共同要求第 7 节要求「提交…实际上线冒烟结果」；此处归档的是失败运行，
  且 `evidence/independent-acceptance-report.md` C-13 声称「我本地重跑命令与结果一致」与此不符。
  我在干净环境实测 27/27 断言通过、场景验证 73/73 通过，说明**业务本身没问题，是归档滞后**。
- **最小整改**：干净库重跑 `scripts/smoke-test.sh` 并回填 `evidence/smoke-test.log`；
  流水线中改为 `bash scripts/smoke-test.sh 2>&1 | tee evidence/smoke-test.log`。

#### F-6：设备码脱敏规则未覆盖 JSON 形态（见 §6.2）

- **触发条件**：`evidence/会话事件清单.jsonl` 中出现 `"device_code": "<uuid>"` 形态。
- **实际**：最终 HEAD 仍含 4 个未掩码设备码（`27c9bbff…`、`8bcc7bc2…`、`a0e7800f…`、`ac772439…`）。
- **影响**：均为已过期的一次性设备码，**不构成实际凭据泄露**；
  但违反共同要求第 3 节「日志、截图和对话导出均须脱敏」的完整覆盖要求，且与仓库自身对 CLI 形态的掩码口径不一致。
- **最小整改**：补一条 JSON 形态规则并重导导出件（见 §6.2 给出的正则）。

### 轻微

#### F-7：`process-timeline.py` 无法支撑「每次迭代复核」的声明（见 8.3 第 2 条）

- **实际**：脚本只输出全仓库首次落盘的 8 个里程碑（`scripts/process-timeline.py:119-126`），无迭代维度。
- **影响**：`docs/03` §8「并在每次迭代交付时用 `scripts/process-timeline.py` 复核」的**机制描述与实际能力不符**；
  v2.4.0 的顺序纪律目前靠提交拓扑链佐证（本次已独立验证通过），而非该脚本。
- **最小整改**：给脚本加 `--since <日期或提交>` 参数，按迭代窗口过滤后再输出里程碑。

#### F-8：T-84 / 原型 §12.2 引用的脚本路径与参数不存在（见 8.2、8.3）

- **实际**：`scripts/verify-gate.ps1` 不存在（实际为 `scripts/verify-gate.py`）；
  `scripts/deploy-local.ps1` 无 `-Commit` 参数，且脚本内不记录任何提交 SHA。
- **影响**：计划文档与原型文档指向了不存在的入口，教师按图索骥会落空；
  且 T-84 承诺的「部署日志含同一 SHA」在当前部署脚本中无实现。
- **最小整改**：把 T-84 与原型 §12.2 第 ⑦ 行的路径改为 `scripts/verify-gate.py`，
  并给 `deploy-local.ps1` 增加 `-Commit` 参数（写入部署日志头部）。

#### F-9：`docs/01` §12.1 与 `docs/09` §6 关于「无 GitHub 凭据」的陈述已与仓库状态矛盾（见 8.1、8.6）

- **实际**：仓库已含 `scripts/github_mcp.py` 与 `evidence/github-mcp/`（含一次真实的 `create_repository` 调用），
  证明账号**有** GitHub PAT 且已连上官方 MCP Server；失败原因是 PAT 缺建仓权限（403），不是无凭据。
- **影响**：文档低估了已具备的能力，同时把失败原因归错，会误导教师判断补齐路径
  （真正要做的是**换有 `repo` 权限的凭据**，而不是「申请 GitHub MCP 接入」）。
- **最小整改**：把两处改为「已具备 GitHub MCP 接入与 PAT，但该 PAT 缺建仓权限（403 Resource not accessible），
  需换用含 `repo` 权限的凭据或由教师开通」。

#### F-10：`docs/09` §7「口令、密钥、令牌未做删改」字面与代码不符；导出脚本缺少非凭据 32 位串豁免（见 8.6、6.3）

- **实际**：导出脚本确实做了掩码（大量「已掩码」标记）；另 SHA-256 占位符被 32 位规则误伤，
  而 `verify-delivery.py` 有 `SAFE_HEX32_CONTEXT` 豁免、导出脚本没有，两者口径不一致。
- **影响**：表述不精确可能让读者误以为导出件未脱敏；误伤仅限占位符，不影响复核性。
- **最小整改**：把 `docs/09` §7 改为「结构与时间戳未做删改，仅对一次性凭据做掩码」；
  并把 `SAFE_HEX32_CONTEXT` 的同类豁免同步到 `export_session.py`。

---
## 10. 未验证项（受环境/访问边界限制）

以下项目本次**未能独立核验**，如实列出，不作通过或失败判定：

| 未验证项 | 原因 |
|---|---|
| GitHub/Gitee 远端仓库的真实存在性、分支保护规则、PR 与合并记录 | 本仓库无远端配置；本环境无 GitHub/Gitee 有效凭据（403）。已在 F-1 记为未达成 |
| `release.yml` / `ci.yml` 在真实 GitHub Actions runner 上的端到端执行结果 | 本环境无 GitHub Actions runner。YAML 合法性、步骤引用存在性、门禁逻辑（覆盖率阈值 `sys.exit(1)`）已逐项静态核对并单独执行验证 |
| `scripts/race-test.sh` 的容器内竞态检测 | 本机无 C 编译器；`evidence/race-test.log` 为既有归档，本次未复现（不影响本次迭代范围，但影响整体门禁的可复现性） |
| 讲解视频与班级群提交记录 | 属人工交付物，本次无法验证 |
| `docs/12-讲解视频带时间点目录.md` 与题面第 10 节第 8 条的逐条对应完整性 | 需视频本体才能核对时间点 |

## 10.1 追加：验收期间的并发提交与最终状态复核

验收过程中发现生产侧仍在提交（本次验收不涉及修改任何仓库文件）。
为避免以陈旧快照归因，本节记录最终复核结果。

验收开始时 HEAD = `1cc5233`，结束时 HEAD = `8474e4c`（期间新增 6 个提交）。
最终状态复核（clean worktree，`--repo $T\wt-last`）：

```
[verify-delivery] PASS=2 PENDING=6 FAIL=0
  [PENDING] D-01 MCP 建仓证据：evidence/gitee-mcp/ 下暂无 create_repo 报文（远端建仓动作未执行）
  [PENDING] D-02 远端分支承载：仓库尚未配置远端
  [PASS   ] 提交消息格式与编号必填
  [PENDING] D-04 PR 描述要素  [PENDING] D-05 主分支保护
  [PENDING] D-06 合并提交     [PENDING] D-07 合并提交上的门禁与部署
  [PASS   ] D-09 仓库无令牌明文
python scripts/test-delivery-checks.py → 合计：PASS 23 / FAIL 0
```

各发现项在最终 HEAD 上的状态：

| 发现 | 最终状态 | 说明 |
|---|---|---|
| F-1 远端闭环 | **仍未完成** | `git remote -v` 仍为空；`main` 仍仅 `d75aebd`；仓库新增 `scripts/release-github.py`（一键驱动脚本）但**实跑未执行**，未产出任何新报文 |
| F-2 核验证据/索引 | **部分修复** | `docs/10` §5 已改为 GitHub MCP 且如实标注 403；但 §2 仍**漏列** `#16221411`/`#16221436`，§3 仍写「受保护」与 §5 矛盾 |
| F-3 流水线产物命名 | **仍未修复** | `release.yml` 仍不生成 `evidence/main-gate-*.log` / `evidence/deployment-main.log` |
| F-4 自检自指误报 | **已修复并复验通过** | 最终 HEAD：`FAIL=0`、`EXIT=0`、测试 23/23 |
| F-5 `evidence/smoke-test.log` 过期 | **仍未修复** | 文件仍为 2026-11-11 20:19:36 的失败运行 |
| F-6 设备码 JSON 形态脱敏 | **仍未修复** | 最终 HEAD 仍有 4 个未掩码设备码 |
| F-7 `process-timeline.py` 无迭代维度 | **仍未修复** | 实跑输出仍为 8 个全局里程碑 |
| F-8 T-84 路径/参数不存在 | **仍未修复** | `scripts/verify-gate.ps1` 不存在；`deploy-local.ps1` 无 `-Commit` |
| F-9 「无 GitHub 凭据」表述矛盾 | **仍未修复** | `docs/01` §12.1 与 `docs/09` §6 仍写「本账号无 GitHub 凭据」 |
| F-10 `docs/09` §7 表述不精确 | **仍未修复** | 字面仍与代码不符 |

**说明**：上表以最终 HEAD `8474e4c` 为准。表中保留的事实与证据均是本次验收独立取证得到的，
不依赖生产者的任何声明。本节不重新评估 F-4 的修复质量（已复验通过）。

---
## 11. 结论复述

- **VERDICT: PARTIAL**
- 阶段顺序（第 1 项检查）**严格正确**，无倒序，约束先于业务代码。
- 需求→原型→计划→约束→实现的**文档链路自洽**，D-01~D-09 均有对应落点声明；
  脚本**真实可跑、无假实现、无恒真断言**，业务闭环经独立实例实跑验证通过（冒烟 27/27、场景 73/73）。
- **主要缺口在远端闭环**：共同要求第 8 节的建仓/PR/保护/合并/合并后部署在本仓库**尚未发生**，
  且交付索引与工作项核验证据不完整（F-1、F-2）。仓库对此如实登记「待补证」，故不判 FAIL。
- 次要缺口为流水线产物命名与 D-07 口径不匹配（F-3）、归档 smoke 记录过期且失败（F-5）、
  设备码 JSON 形态未脱敏（F-6）及若干文档陈述与实际不符（F-7~F-10）。
- 按共同要求第 10 节第 1 条与第 7 节末句的口径，**本迭代尚不足以判定整题通过**；
  补齐 F-1、F-2 后可复评。

---
*本报告由独立验收 Agent 独立取证生成；未修改仓库内任何源码、文档或测试文件，仅新增本报告一份。*
