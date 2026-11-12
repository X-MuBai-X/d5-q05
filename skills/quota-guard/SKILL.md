---
name: quota-guard
description: 企业模型服务（题目 05）的配额计量口径守卫。用于在改动 internal/domain、internal/store、internal/service 中与配额、额度占用、成功量、处理中数、幂等有关的逻辑之后，快速核验 R2/R4/R5 的计量不变量没有被破坏，并输出可归档的逐项结论。当用户提到「配额口径」「额度占用」「计量」「幂等是否还正确」「改完配额逻辑要验证一下」时使用本 Skill。
---

# quota-guard — 配额计量口径守卫

## 1. 用途

题目 05 的核心风险集中在**计量口径**上：额度占用、成功量、处理中数、失败与超时的释放、
幂等不重复计量、降额下限。这类错误往往不会让程序崩溃，只会让日终核查的数字对不上，
因此需要一个**可重复执行、结论明确、可归档**的守卫。

本 Skill 回答一个问题：**这次改动之后，R2 / R4 / R5 的计量口径还是不是原来那套？**

守卫由两部分组成：

1. **静态不变量检查**：用源码模式匹配确认关键原子操作没有被改写（例如额度占用必须是
   「带条件的单条 UPDATE」，而不是「先查后写」）。
2. **行为回归**：跑掉与计量直接相关的单元测试，并统计核心包覆盖率。

## 2. 触发场景

| 触发场景 | 说明 |
|---|---|
| 修改了额度占用/释放逻辑 | `internal/store/store.go` 的 `ReserveInflight` / `CommitSuccess` / `ReleaseInflight` |
| 修改了调用受理流程 | `internal/service/service.go` 的 `Invoke` / `settle` |
| 修改了降额校验 | `internal/domain/domain.go` 的 `ValidateQuotaChange` / `Usage.Occupied` |
| 修改了用量表结构 | `internal/store/schema.sql` 中的 `tenant_day_usage` |
| 提交前自检 | 任何涉及计量的提交之前 |

**不适用**：只改页面样式、只改文档、只改 CI 配置时无需调用本 Skill。

## 3. 输入

| 参数 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `--repo` | 否 | 当前目录（向上查找含 `go.mod` 的目录） | 仓库根路径 |
| `--skip-tests` | 否 | 否 | 只做静态检查，不执行 `go test`（用于快速迭代） |
| `--min-coverage` | 否 | `85` | 核心包覆盖率下限（百分比） |
| `--out` | 否 | 标准输出 | 结论文件输出路径（Markdown） |

无参数时直接在仓库根目录执行即可。

## 4. 执行步骤

1. **定位仓库**：从当前目录向上查找包含 `go.mod` 且模块名为 `modelquota` 的目录；找不到则失败退出。
2. **静态不变量检查**（共 10 项，见 §6）：
   - `ReserveInflight` 必须使用**单条带条件 UPDATE**，条件含 `success_count + inflight_count < COALESCE((SELECT daily_quota FROM tenants WHERE id=?), 0)`；
   - `ReserveInflight` **不得**再接收外部额度参数（额度必须由本条语句实时读取，否则并发降额会突破上限，见 D1）；
   - `ReserveInflight` 必须先确保用量行存在（`EnsureDayUsage`）；
   - `CommitSuccess` 必须同时 `-1 inflight_count` 与 `+1 success_count`，且带 `inflight_count > 0` 守卫；
   - `ReleaseInflight` 必须 `-1 inflight_count` 且带 `inflight_count > 0` 守卫；
   - `Usage.Occupied()` 必须等于 `Success + InFlight`（降额下限口径）；
   - `ValidateQuotaChange` 必须以 `Occupied()` 为下限；
   - `settle` 必须同时存在「成功」「超时」「失败」三条分支，且失败/超时走释放、成功走实扣；
   - `Invoke` 必须在占用额度**之前**完成租户/凭证状态校验（停用后不得占用额度）；
   - `UpdateTenantQuotaWithFloor` 必须把降额下限判定与写入放在同一条语句里；
   - `BuildDailyReport` 必须能识别「占用超过额度」并标记为不一致。
3. **行为回归**：执行
   `go test ./internal/domain/... ./internal/store/... ./internal/service/... -run 'Quota|Usage|Invoke|Settle|Reserve|Inflight|Idempot|Reject' -count=1`
   全部用例必须通过。
4. **覆盖率统计**：执行 `go test ./internal/domain/... ./internal/store/... ./internal/service/... -coverprofile`，
   用 `go tool cover -func` 读取三个核心包的覆盖率，任一低于阈值即判定不通过。
5. **产出结论**：按 §5 的格式输出逐项结论与总判定，并写入 `--out` 指定文件（若提供）。

## 5. 输出格式

```markdown
# quota-guard 计量口径守卫报告

- 仓库：<绝对路径>
- 判定：PASS | FAIL
- 核心包覆盖率：domain 96.8% / store 94.9% / service 86.8%（下限 85%）

## 静态不变量

| # | 检查项 | 规则 | 结论 | 证据 |
|---|---|---|---|---|
| 1 | 额度占用为单条条件 UPDATE | R2 | PASS | internal/store/store.go:545 |
| ... | | | | |

## 行为回归

- 命令：go test ... -run 'Quota|Usage|...'
- 结果：PASS（38 个用例全部通过，0 失败）

## 失败项

（如无则写「无」；有则必须写明文件:行号、违反的规则编号与修复方向）
```

## 6. 检查项清单

| # | 检查项 | 规则编号 | 判定方式 |
|---|---|---|---|
| 1 | 额度占用是单条条件 UPDATE，且额度上限在本条语句内实时读取（不得接收外部额度参数） | R2/D1 | 源码模式匹配 |
| 2 | 占用前确保当日用量行存在 | R2 | 源码模式匹配 |
| 3 | 成功转换：处理中 −1、成功 +1，带 `inflight_count > 0` 守卫 | R2/R4 | 源码模式匹配 |
| 4 | 失败/超时释放：处理中 −1，带 `inflight_count > 0` 守卫 | R4 | 源码模式匹配 |
| 5 | `Occupied()` = 成功数 + 处理中数 | R5 | 源码模式匹配 + 单元测试 |
| 6 | 降额校验以 `Occupied()` 为下限 | R5 | 源码模式匹配 + 单元测试 |
| 7 | `settle` 具备成功/超时/失败三条独立分支 | R4/Q1 | 源码模式匹配 + 单元测试 |
| 8 | 状态校验早于额度占用 | R6 | 源码顺序匹配 |
| 9 | 降额下限判定与写入在同一条语句内完成（防 TOCTOU） | R5/D1 | 源码模式匹配 |
| 10 | 日终报表能识别「占用超过额度」并标记不一致 | R8/D1 | 源码模式匹配 + 单元测试 |

## 7. 失败处理

- **任一静态检查项不通过** → 总判定 `FAIL`，在「失败项」中写明：文件:行号、违反的规则编号、修复方向。
- **行为回归有用例失败** → 总判定 `FAIL`，附 `go test` 的原始失败输出（截断到 40 行）。
- **覆盖率低于阈值** → 总判定 `FAIL`，写明实际值与阈值。
- **Go 工具链不可用 / 环境缺失** → 总判定 `FAIL`（或 `SKIP`，若显式传了 `--skip-tests`），
  **不得**因为跑不起来就输出 PASS。
- **禁止**为了让报告好看而放宽判定标准或删除检查项。

## 8. 完成标准

同时满足才算完成：

1. 10 项静态检查全部有明确结论（PASS / FAIL），无「跳过」；
2. 行为回归全部用例通过；
3. 三个核心包覆盖率均 ≥ 阈值；
4. 总判定为 `PASS`，且报告已写入 `--out`（若提供）或标准输出；
5. 报告已被**真实调用记录**留存（见 `docs/08-Skill使用记录.md`）。

## 9. 快速使用

```powershell
# 在仓库根目录执行
python skills/quota-guard/scripts/quota_guard.py --out evidence/skill-quota-guard.md

# 只跑静态检查（快速迭代）
python skills/quota-guard/scripts/quota_guard.py --skip-tests
```

退出码：`0` = PASS，`1` = FAIL，`2` = 执行错误（环境问题）。
