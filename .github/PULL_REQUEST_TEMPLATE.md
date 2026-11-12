<!--
本模板用于 `dev/05` → `main` 的合并请求。
共同要求第 8 节第 4 条要求 PR 必须附：需求与缺陷编号、变更说明、实际测试结果、
覆盖率、验收报告及 AI/Skill 记录位置。缺任一项即视为未满足要求（AGENTS.md §10 第 4 条）。
未填写的必填项请写「无（说明原因）」，不要留空。
-->

## 1. 关联工作项编号（必填）

<!-- 例：#16220913；多个用顿号分隔。编号须为飞书项目（Meegle）中真实存在的工作项，已用 meegle CLI 核验 -->

-

## 2. 变更说明（必填）

<!-- 本 PR 改了什么、为什么改、对业务口径有无影响 -->

-

## 3. 实际测试结果（必填，粘贴真实输出摘要，不要写「已通过」）

| 门禁 | 命令 | 结果 |
|---|---|---|
| 格式 | `gofmt -l .` | |
| 静态检查 | `go vet ./...` | |
| 单元测试 | `go test ./... -count=1` | |
| 覆盖率 | `go test ./... -coverprofile=...` | 整体 __% ；核心包 __% |
| 竞态 | `go test ./... -race` | |
| 业务冒烟 | `scripts/smoke-test.sh` | |
| 场景验证 | `scripts/scenario-verify.py` | |

## 4. 覆盖率（必填）

- 整体覆盖率：__%
- 核心业务包（domain / store / service / httpapi / upstream）：__%
- 覆盖率原件位置：`evidence/coverage.out`、`evidence/coverage-summary.txt`

## 5. 独立验收报告（必填，填写路径）

- 独立测试报告：`evidence/independent-test-report.md`
- 独立验收报告：`evidence/independent-acceptance-report.md`

## 6. AI 使用与 Skill 记录位置（必填）

- AI 使用说明：`docs/09-AI使用说明.md`
- Skill 使用记录：`docs/08-Skill使用记录.md`
- 会话导出：`docs/06-会话导出.md`、`evidence/会话事件清单.jsonl`

## 7. 自检清单

- [ ] 提交消息符合 `[类型] [版本号] [#编号] 中文描述`，`feat`/`fix` 均带真实工作项编号
- [ ] `main` 未被直接推送，全部业务提交在 `dev/05`
- [ ] 仓库内不含任何密钥、访问令牌明文
- [ ] 本次合并后将在**合并提交**上重新执行门禁与部署
