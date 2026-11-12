# 交付链路自检报告（题目 05）

> 由 `scripts/verify-delivery.py` 自动生成，执行时间 2026-11-12T15:04:06.368430+08:00。
> 口径来源：`docs/01-需求分析.md` §12 的 D-01~D-09（共同要求第 8 节）。
> `PENDING` 表示该项依赖尚未执行的远端动作，如实标注待补证，不臆造通过。

| # | 检查项 | 状态 | 说明 |
|---|---|---|---|
| 1 | D-01 MCP 建仓证据 | FAIL | 20261112-100341-create_repository.json：工具=create_repository，时间=2026-11-12T10:03:41.117933+08:00，响应中的仓库 URL=未出现；服务端返回失败：{"jsonrpc": "2.0", "id": 2, "result": {"content": [{"type": "text", "text": "failed to create repository: POST https://api.github.com/user/repos: 403 Resource not accessible by personal access token [ |
| 2 | D-02 远端分支承载 | PASS | 远端 gitee 分支 ['dev/05', 'main', 'master']；远端 main 头部 0c9cca3 是**合并提交**（父提交数=2，第二父提交 = 远端 dev/05 头部 1d4d547），业务内容经 PR 合入 main 而非直推；main 提交数=74；真实推送实测直推被服务端拒绝 |
| 3 | 提交消息格式与编号必填 | PASS | dev/05 全部提交符合 `[类型] [版本号] [#编号] 描述`，且 feat/fix 均带版本号与编号；另有 1 条远端初始化提交按共同要求第 8 节第 2 条豁免格式规则：2ecc808 Initial commit |
| 4 | D-04 PR 描述要素 | PASS | PR 描述含全部 6 项要素 |
| 5 | D-05 主分支保护 | PASS | branch-protection-gitee.json：保护规则读取结果含禁止直推/删除/强推与 PR 合并要求 |
| 6 | D-06 合并提交保留原始提交 | PASS | main 头部提交 0c9cca3051bb，父提交数=2，消息=[feat] [v2.6.0] [#16221249] 合并题目 05 企业模型服务配额管理与远端交付链路 从 dev/05 发起 PR 并合并，保留开发分支原始提交；主要工作项 #16221249，关联需求 #16220913 与缺陷 #16220917 / #16220924 / #16220925 / #16221411 / #16221436。 |
| 7 | D-07 合并提交上的门禁与部署 | PASS | 日志中出现合并 SHA 的文件：['main-gate-0c9cca3051bb.log', 'deployment-main.log'] |
| 8 | D-09 仓库无令牌明文 | PASS | 扫描 155 个文本文件，未命中令牌模式 |

合计：PASS 7 / PENDING 0 / FAIL 1

