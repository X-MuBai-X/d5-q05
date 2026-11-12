## 需求与缺陷编号

- 需求工作项：#16221249（远端交付链路）、#16220913（企业模型服务配额管理核心业务）
- 缺陷工作项：#16220917（并发降额超限）、#16220924（缺 tenant_id 返回 500）、#16220925（部署 PID 缺陷）、
  #16221411（导出未脱敏）、#16221436（提交消息格式校验）

## 变更说明

本 PR 从 `dev/05` 发起，把 PR !1 之后开发分支上新增的提交合入 `main`（题目 05 的业务实现
已随 PR !1 进入 `main`）：

1. `435222a` test：补齐 `internal/httpapi` 与 `internal/upstream` 的异常分支用例
   （方法不符 405、非法 JSON 400、运营缺少租户范围 400；迟到等待窗口未配置、上游地址非法、
   调用方已取消、响应体读取失败、迟到窗口内才拿到结果），把核心包覆盖率抬到阈值以上；
2. `9b8f352` fix：修正 `go test -coverprofile` 的**产出路径**（相对路径会写到包目录、目录不存在时
   静默不写，导致统计脚本读到**陈旧剖面**），并让 **CI 与本机门禁共用同一个** `scripts/coverage-summary.py`，
   消除两处统计逻辑的口径漂移；
3. `8a79a58` fix：修正 GitHub MCP 合并工具的参数名（`pullNumber`）与主分支保护「禁止删除」的判定口径，
   并让链路在合并未真正发生时中止（不再把旧 main 当作合并提交去跑门禁和部署）；
4. `a81d472` feat：新增 `scripts/push-via-github-api.py` 与 `scripts/fetch-commit-via-github-api.py`
   —— 本机到 `github.com` 的 git 传输端口不可达时，用 GitHub REST Git Data API 推送，
   并按远端元数据**逐字节重建**提交对象（每一步 SHA 复核，不一致即中止）；
5. `131647d` / `dacc11a`：归档 GitHub 侧建仓 → PR → CI → 分支保护 → 合并 → 合并提交门禁 → 合并版本部署
   的完整证据（含合并提交 `3b06d206055a` 的门禁日志与部署流水），并回填 `docs/10`、`docs/11`
   的发布版本、合并 SHA 与班级群消息；
6. `8862ab6` fix：修正「按 API 元数据重建提交对象」的三个真实坑——GitHub 网页合并会加 **GPG 签名头**
   （`gpgsig`）、REST API 的时间是 UTC 而对象用的是**账号时区偏移**、合并提交的消息**末尾没有换行**；
   修正后重建出的提交对象与远端**逐字节一致**（已在两个合并提交上验证）；
   同时归档 PR !2 合并提交 `4e15b2c3b344` 的门禁与部署流水，并回填发布版本与班级群消息；
7. `8775ac0` fix：修正部署步骤里的**常驻进程管道卡死**（部署脚本拉起的常驻进程继承 stdout 管道，
   使 `capture_output` 永远等不到 EOF，实测卡死），改为把输出重定向到文件；
   并归档 PR !3 合并提交 `1454f78a6d53` 的门禁与合并版本部署流水，回填最终发布版本与班级群消息。

> 本 PR 为**收尾 PR**：只含交付脚本修正与证据归档，**无业务代码变更**；
> 题目 05 的业务实现与门禁口径均已在 PR !1 ~ PR !3 合入 main。

## 测试结果

- `gofmt -l .` 无输出；`go vet ./...` 无告警；`go test ./... -count=1` 全部通过
- 全量接口能力测试 113/113 通过（`scripts/api-full-test.py`，归档 `evidence/api-full-test.log`）
- 业务冒烟 39/39、五组完整场景 73/73、容器内 `go test ./... -race` 退出码 0 且 0 DATA RACE
- 交付链路自检 `scripts/verify-delivery.py`：**PASS=8 / PENDING=0 / FAIL=0**
  （`evidence/delivery-check.md`）

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
