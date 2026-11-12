## 需求与缺陷编号

- 需求工作项：#16221249（远端交付链路）、#16220913（企业模型服务配额管理核心业务）
- 缺陷工作项：#16220917（并发降额超限）、#16220924（缺 tenant_id 返回 500）、#16220925（部署 PID 缺陷）、
  #16221411（导出未脱敏）、#16221436（提交消息格式校验）

## 变更说明

本 PR 从 `dev/05` 发起，把开发分支上尚未进入 `main` 的提交合入 `main`：

1. `b43ab07` docs：回填 PR !2 合并提交与合并版本的门禁、部署证据；
2. `dbe5774` fix：修正交付自检对「合并来源」与「建仓成功」的判定口径；
3. `feat` 提交：新增 GitHub 官方 MCP 的设备码授权脚本（`scripts/github_oauth_device.py`），
   修正 MCP 客户端把 JSON-RPC 错误当成功的缺陷，并让远端链路脚本支持「新建仓库首个分支」的推送处理。

题目 05 的业务实现（配额开通、调用、停用与用量核查）随前序 PR !1 / !2 已经进入 `main`，
本次 PR 补齐的是最后一轮修正与 GitHub 侧交付链路。

## 测试结果

- `gofmt -l .` 无输出；`go vet ./...` 无告警；`go test ./... -count=1` 全部通过
- 业务冒烟 39/39、五组完整场景 73/73、容器内 `go test ./... -race` 退出码 0 且 0 DATA RACE
- 交付链路自检 `scripts/test-delivery-checks.py` 全部通过
- 本 PR 的 CI（`.github/workflows/ci.yml`）在本次 PR 上真实运行：质量门禁与业务冒烟两个作业
  的检查结论见本 PR 的 Checks，合并后在**合并提交**上再跑一次本地门禁并重新部署

## 覆盖率

整体 **90.5%**；核心业务包 domain 97.3% / store 94.4% / service 87.7% / httpapi 89.7% / upstream 95.3%，
各核心包均 ≥85%。原件见 `evidence/coverage.out`、`evidence/coverage-summary.txt`。

## 验收报告

- 独立测试 Agent 报告：`evidence/independent-test-report.md`
- 独立验收 Agent 报告：`evidence/independent-acceptance-report.md`

## AI / Skill 使用记录

- 全过程提示词与结构化事件清单：`docs/06-会话导出.md`、`evidence/会话事件清单.jsonl`
- 过程时序（机器反推）：`evidence/过程时序清单.md`
- AI 使用说明：`docs/09-AI使用说明.md`
- 自编 Skill 与使用记录：`skills/quota-guard/`、`skills/acceptance-audit/`、`docs/08-Skill使用记录.md`
