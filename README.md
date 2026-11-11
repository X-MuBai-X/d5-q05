# 企业模型服务配额管理（题目 05）

> Go 实现的「企业模型服务的配额开通、调用、停用与用量核查」。
> 本仓库只实现题面第 5 题。

## 能力概览

| 能力 | 说明 |
|---|---|
| 配额开通与调整 | 运营为租户开通服务、调整日额度（变更留痕操作人/原因/生效时间） |
| 凭证管理 | 客户管理员在本租户创建、停用应用凭证（租户上限 2 个，密钥只显示一次） |
| 调用受理与计量 | 请求编号全局唯一、相同参数幂等、2 秒超时、成功/失败/超时三终态分列计量 |
| 停用 | 租户停用与凭证停用后不再受理新调用，已受理请求正常结束 |
| 用量核查 | 按 UTC 自然日与租户分列成功/失败/超时/处理中/被拒绝，并核对一致性 |

## 技术栈

Go 1.27 · SQLite（`modernc.org/sqlite` 纯 Go 驱动，无需 CGO）· 标准库 `net/http` · 零额外框架。

## 快速开始

```bash
# 终端 1：模拟上游模型服务
go run ./cmd/mockupstream -addr 127.0.0.1:9091

# 终端 2：业务服务
go run ./cmd/server -addr 127.0.0.1:8080 -db ./deploy/data/app.db -upstream http://127.0.0.1:9091
```

打开 <http://127.0.0.1:8080/login>，用预置账号登录（账号与初始口令见
`docs/04-部署与使用说明.md` §5）。

一键部署与冒烟（Windows）：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/deploy-local.ps1
```

## 目录

| 路径 | 说明 |
|---|---|
| `cmd/server` | 服务入口 |
| `cmd/mockupstream` | 模拟上游模型服务（成功/失败/超时/重复通知） |
| `internal/domain` | 领域模型与计量口径纯函数 |
| `internal/store` | SQLite 持久化（`schema.sql` 为结构唯一事实来源） |
| `internal/service` | 业务编排与计量 |
| `internal/upstream` | 上游调用客户端 |
| `internal/httpapi` | HTTP 接口、RBAC、页面模板 |
| `docs/` | 需求分析、原型、任务规划、部署说明、Skill 使用记录、AI 使用说明 |
| `skills/` | 自编 Skill：配额口径守卫、验收逐条核查 |
| `evidence/` | 真实执行证据（测试、覆盖率、部署、冒烟） |
| `AGENTS.md` | 项目级工作约定 |

## 质量门禁

```bash
gofmt -l .                                   # 必须无输出
go vet ./...                                 # 必须无告警
go test ./... -count=1 -coverprofile=coverage.out
go test ./... -race                          # 需要 CGO 与 C 编译器
```

整体覆盖率与各核心业务包覆盖率均 ≥ 85%（实测见 `evidence/coverage-summary.txt`）。

## 文档索引

| 文档 | 内容 |
|---|---|
| `docs/01-需求分析.md` | R1–R8 逐条解读、假设清单、12 项确认结论 |
| `docs/02-原型设计.md` | 页面原型、流程图、交互与接口约定 |
| `docs/03-任务规划.md` | 阶段任务分解、关键路径、风险与应对 |
| `docs/04-部署与使用说明.md` | 部署、账号、页面用法、接口示例、恢复与回退 |
| `docs/05-假设清单.md` | A1–A13 假设与交付后新增澄清 |
| `docs/06-会话导出.md` | 全过程可见会话导出与索引 |
| `docs/07-提交规范说明.md` | 提交格式、编号核验状态、提交分组 |
| `docs/08-Skill使用记录.md` | 两个自编 Skill 的定义与真实调用记录 |
| `docs/09-AI使用说明.md` | AI 使用方式、合理性自查、环境限制与待补证 |
