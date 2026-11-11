# AGENTS.md 读取与使用记录

> 本文件记录项目级 `AGENTS.md` 在本项目开发过程中的**真实读取与使用情况**，
> 用于说明该约定确实被加载并影响了实现，而不是一份写完就放着的文档。
> 时间均按本机时间（UTC+8）记录。

---

## 1. 版本沿革

| 版本 | 归档位置 | 生效阶段 | 说明 |
|---|---|---|---|
| v1.0 | `docs/agents/AGENTS-v1.0.md` | 需求分析确认后 → 业务编码前 | 首版。规则来源是需求分析阶段的 12 项确认结论（Q1–Q12）与假设清单（A1–A13），在写第一行业务代码之前定稿。 |
| v1.1 | 本文件下方「增补说明」+ `AGENTS.md` | 单元测试与代码评审后 | 增补 5 条规则，每条都对应实现过程中**实际暴露的缺陷**（见 §3）。 |
| v1.2 | 仓库根目录 `AGENTS.md` | 部署与验收前 | 增补自编 Skill 调用留痕、并发测试场景清单、证据脱敏三项要求。 |

---

## 2. 读取记录

| # | 时间 | 读取者 | 读取的文件版本 | 触发场景 | 后续动作 |
|---|---|---|---|---|---|
| 1 | 2026-11-11 19:02 | AI 助手（编码会话） | v1.0（当时的定稿规则集） | 需求分析 12 项确认完成，准备开始写 `internal/domain` | 按 §4 计量口径先写纯函数与常量；按 §5 把身份来源固定为服务端会话令牌 |
| 2 | 2026-11-11 19:26 | AI 助手（编码会话） | v1.0 | 写 `internal/store` 的额度占用逻辑 | 按 §4 采用**单条条件 UPDATE** 完成「计数 + 判定」，不写成先查后写 |
| 3 | 2026-11-11 19:48 | AI 助手（编码会话） | v1.0 | 写 `internal/service` 的 `Invoke` 受理顺序 | 按 §4/§5 固定顺序：编号校验 → 幂等 → 状态校验 → 原子占额度 → 调上游 → 计量；租户归属只从身份取 |
| 4 | 2026-11-11 20:01 | AI 助手（编码会话） | v1.0 | 写 `internal/httpapi` 与页面模板 | 按 §5 越权显式拒绝（`tenant_id` 不采信但要报错）；按 §6 页面模板只 `define "content"` |
| 5 | 2026-11-11 20:05 | AI 助手（编码会话） | v1.0 | 单元测试暴露首批缺陷 | 按 §6/§7 补测试并修缺陷，见 §3 的 v1.1 增补清单 |
| 6 | 2026-11-11 20:12 | AI 助手（编码会话） | v1.1 | 准备覆盖率冲刺 | 按 §7 拆出 `Service.settle` 使终态结算可被精确单测，拆分前先确认不改变 §4 的三条计量分支 |
| 7 | 2026-11-11 20:30 | AI 助手（编码会话） | v1.2 | 准备部署与验收 | 按 §8/§9 准备真实部署记录、调用两个自编 Skill 并留存原始输出 |

> 说明：v1.0 的定稿时点早于第一行业务代码；v1.1 / v1.2 的增补条目均在下表逐条给出
> 「触发缺陷 → 规则条款 → 代码落点」的可追溯关系。

---

## 3. v1.1 增补条目与触发缺陷对照

| 增补条款 | 触发的真实缺陷 | 缺陷如何被发现 | 代码落点 |
|---|---|---|---|
| 页面模板只 `define "content"` | 5 个页面模板都写成 `{{define "xxx.html"}}{{define "content"}}`，Go 模板解析直接报 `unexpected <define> in command`，`LoadTemplates()` 必然失败，**服务无法启动** | `internal/httpapi` 单元测试在构造测试服务器时失败 | `internal/httpapi/web/templates/*.html` |
| 非 string 键映射不得用字符串下标 | `DailyReport.Counts` 键类型是 `domain.RequestStatus`，模板里 `{{index .Report.Counts "success"}}` 键类型不匹配，运营台渲染 500 | 页面渲染测试 | `domain.DailyReport.CountOf` + `web/templates/ops.html` |
| HTTP 500 不透出内部细节 | 运营账号调用「创建凭证」时因无租户归属触发外键失败，原始数据库错误 `constraint failed: FOREIGN KEY constraint failed (787)` 直接回给客户端 | HTTP 接口测试 | `internal/httpapi/server.go` 的 `writeError`、`internal/httpapi/decode.go` 的 `respond` |
| 表单与 JSON 双通道 | 页面表单提交的是 `application/x-www-form-urlencoded`，而处理器只解 JSON，**页面上所有表单都不可用** | 手工走查页面 + HTTP 接口测试 | `internal/httpapi/decode.go`（新增）+ `server.go` / `handlers.go` 改用 `decodeInput`/`respond` |
| 运营不得代建凭证 | 运营账号没有租户归属，凭证所属租户无法从身份推导，却仍能进「创建凭证」接口 | HTTP 接口测试 | `internal/httpapi/server.go` 新增 `tenantAdminOnly` |

另有两条在 v1.1 固化但不新增条款的修复，同样由 `AGENTS.md` 规则驱动：

- `internal/store/schema.sql` 中 `admins.tenant_id` 原本是 `NOT NULL REFERENCES tenants(id)`，
  运营账号没有租户 → 预置数据写入必然违反外键。依据 §5「运营没有租户归属」改为可空外键。
- 登录页原本在**未认证页面**明文展示预置口令，依据 §8「截图、日志、导出文件必须脱敏」的同一原则，
  改为脱敏展示并指向部署说明文档。

---

## 4. 规则到实现的映射索引

| AGENTS.md 条款 | 实现落点 | 验证方式 |
|---|---|---|
| §4 计量口径（8 条） | `internal/domain/domain.go`（`Usage.Remaining/Occupied`、`ValidateQuotaChange`、`BuildDailyReport`）、`internal/store/store.go`（`ReserveInflight`/`CommitSuccess`/`ReleaseInflight`）、`internal/service/service.go`（`Invoke`/`settle`） | `internal/domain/domain_test.go`、`internal/store/store_test.go`、`internal/service/invoke_test.go`；`skills/quota-guard` |
| §5 身份与权限（6 条） | `internal/service/auth.go`（`Identity`/`issueSession`/`AuthorizeTenantAccess`）、`internal/httpapi/server.go`（`identityFrom`/`tenantScope`/`tenantAdminOnly`） | `internal/service/auth_test.go`、`internal/httpapi/server_test.go` |
| §6 代码规范（7 条） | 全仓库 | `gofmt -l .` 空、`go vet ./...` 无告警、`skills/acceptance-audit` |
| §7 测试要求（5 条） | `*_test.go` 全仓库 | `go test ./... -coverprofile`、并发用例、`go test -race` |
| §8 证据与提交 | `evidence/`、`docs/`、git 提交消息 | `docs/07-提交规范说明.md`、git log |
| §9 Skill 调用约定 | `skills/quota-guard`、`skills/acceptance-audit` | `docs/08-Skill使用记录.md` |
