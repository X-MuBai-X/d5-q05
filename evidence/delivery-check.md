# 交付链路自检报告（题目 05）

> 由 `scripts/verify-delivery.py` 自动生成，执行时间 2026-11-15T20:30:17.282681+08:00。
> 口径来源：`docs/01-需求分析.md` §12 的 D-01~D-09（共同要求第 8 节）。
> `PENDING` 表示该项依赖尚未执行的远端动作，如实标注待补证，不臆造通过。

| # | 检查项 | 状态 | 说明 |
|---|---|---|---|
| 1 | D-01 MCP 建仓证据 | PASS | 20261112-160220-create_repository.json：工具=create_repository，时间=2026-11-12T16:02:20.039137+08:00，响应中的仓库 URL=https://github.com/X-MuBai-X/d5-q05；另有 2 次更早的建仓尝试未成功（报文已归档，历史如实保留）：20261112-100341-create_repository.json、20261112-154323-create_repository.json |
| 2 | D-02 远端分支承载 | PENDING | 无法读取任何远端分支：gitee: fatal: unable to access 'https://gitee.com/xishiyao/d5.git/': Failed to connect to gitee.com port 443 via 127.0.0.1 after 2066 ms: Could not connect to server；origin: fatal: unable to access 'https://github.com/X-MuBai-X/d5.git/': Failed to connect to github.com port 443 via 127.0.0.1 after 2052 ms: Could not connect to serve；github-q05: fatal: unable to access 'https://github.com/X-MuBai-X/d5-q05.git/': Failed to connect to github.com port 443 via 127.0.0.1 after 2058 ms: Could not connect to s |
| 3 | 提交消息格式与编号必填 | PASS | dev/05 全部提交符合 `[类型] [版本号] [#编号] 描述`，且 feat/fix 均带版本号与编号；另有 1 条远端初始化提交按共同要求第 8 节第 2 条豁免格式规则：2ecc808 Initial commit |
| 4 | D-04 PR 描述要素 | PASS | PR 描述含全部 6 项要素 |
| 5 | D-05 主分支保护 | PASS | branch-protection.json：保护规则读取结果含禁止直推/删除/强推与 PR 合并要求 |
| 6 | D-06 合并提交保留原始提交 | PASS | main 头部提交 fba84970e720，父提交数=2，消息=[feat] [v2.7.0] [#16221249] 合并题目 05 企业模型服务配额管理与远端交付链路 |
| 7 | D-07 合并提交上的门禁与部署 | PASS | 日志中出现合并 SHA 的文件：['main-gate-fba84970e720.log', 'deployment-main.log'] |
| 8 | D-09 仓库无令牌明文 | PASS | 扫描 276 个文本文件，未命中令牌模式 |

合计：PASS 7 / PENDING 1 / FAIL 0

