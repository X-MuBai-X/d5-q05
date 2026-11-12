# AGENTS.md 读取与使用记录

> 本文件记录项目级 `AGENTS.md` 在本项目开发过程中的**真实读取与使用情况**，
> 用于说明该约定确实被加载并影响了实现，而不是一份写完就放着的文档。
> 时间均按本机时间（UTC+8）记录。

---

## 1. 版本沿革

| 版本 | 归档位置 | 生效阶段 | 说明 |
|---|---|---|---|
| v1.0 | `docs/agents/AGENTS-v1.0.md` | 需求分析确认后 → 业务编码前 | 首版。规则来源是需求分析阶段的 12 项确认结论（Q1–Q12）与假设清单（A1–A13）。**规则集在写第一行业务代码之前已定稿并开始逐条应用**；文件本体 `AGENTS.md`（20:08:35）与归档 `docs/agents/AGENTS-v1.0.md`（20:09:03）落盘，**早于任何业务代码入库**（首个 feat 提交在 20:35 之后），但**晚于首行业务代码草稿**（19:35:37）——该时间差如实记录，见 §2 末尾说明。 |
| v1.1 | 本文件下方「增补说明」+ `AGENTS.md` | 单元测试与代码评审后 | 增补 5 条规则，每条都对应实现过程中**实际暴露的缺陷**（见 §3）。 |
| v1.2 | 仓库根目录 `AGENTS.md` | 部署与验收前 | 增补自编 Skill 调用留痕、并发测试场景清单、证据脱敏三项要求。 |
| v1.3 | `docs/agents/AGENTS-v1.3.md` | 五组完整场景逐条验证与审计整改后 | 增补 6 条规则，对应五组场景验证与审计整改中真实暴露的缺陷（迟到结果不可观测、日报缺零值类别、列表返回 null、参数缺失落 500、端口被占用导致测到旧二进制、验证污染数据库）。 |
| v1.4 | 仓库根目录 `AGENTS.md` | 独立测试/验收 Agent 复核后 | 增补 3 条规则，对应独立验收 Agent 实测复现的缺陷 D1/D4/D5（额度判定与写入跨语句、脱敏扫描白名单与文档表述不符）。 |

---

## 2. 读取记录

| # | 时间 | 读取者 | 读取的文件版本 | 触发场景 | 后续动作 |
|---|---|---|---|---|---|
| 1 | 2026-11-11 19:02 | AI 助手（编码会话） | v1.0 的**规则集内容**（当时尚未落盘为文件，见文末说明） | 需求分析 12 项确认完成，准备开始写 `internal/domain` | 按 §4 计量口径先写纯函数与常量；按 §5 把身份来源固定为服务端会话令牌 |
| 2 | 2026-11-11 19:26 | AI 助手（编码会话） | v1.0 | 写 `internal/store` 的额度占用逻辑 | 按 §4 采用**单条条件 UPDATE** 完成「计数 + 判定」，不写成先查后写 |
| 3 | 2026-11-11 19:48 | AI 助手（编码会话） | v1.0 | 写 `internal/service` 的 `Invoke` 受理顺序 | 按 §4/§5 固定顺序：编号校验 → 幂等 → 状态校验 → 原子占额度 → 调上游 → 计量；租户归属只从身份取 |
| 4 | 2026-11-11 20:01 | AI 助手（编码会话） | v1.0 | 写 `internal/httpapi` 与页面模板 | 按 §5 越权显式拒绝（`tenant_id` 不采信但要报错）；按 §6 页面模板只 `define "content"` |
| 5 | 2026-11-11 20:05 | AI 助手（编码会话） | v1.0 | 单元测试暴露首批缺陷 | 按 §6/§7 补测试并修缺陷，见 §3 的 v1.1 增补清单 |
| 6 | 2026-11-11 20:12 | AI 助手（编码会话） | v1.1 | 准备覆盖率冲刺 | 按 §7 拆出 `Service.settle` 使终态结算可被精确单测，拆分前先确认不改变 §4 的三条计量分支 |
| 7 | 2026-11-11 20:30 | AI 助手（编码会话） | v1.2 | 准备部署与验收 | 按 §8/§9 准备真实部署记录、调用两个自编 Skill 并留存原始输出 |
| 8 | 2026-11-11 21:10 | AI 助手（编码会话） | v1.2 | 五组完整场景逐条验证前 | 按 §7「业务分支验证必须用未污染的数据库」改造部署脚本，新增 `-SkipSmoke` 与端口预检 |
| 9 | 2026-11-11 21:30 | AI 助手（编码会话） | v1.2 | 审计整改阶段 | 按 §4 第 10 条补「五类终态恒列出」，按 §6 第 7 条把列表函数改为返回空数组 |
| 10 | 2026-11-11 21:44 | AI 助手（编码会话） | v1.3 | 独立测试/验收 Agent 报告回收后 | 按 §4 第 11/12 条把额度判定与写入合并进同一条语句；按 §8 第 4 条删除脱敏扫描白名单并对演示口令做掩码 |
| 11 | 2026-11-12 08:45 | AI 助手（编码会话） | v1.4 | D1 修复后的回归与复验 | 按 §7 补 D1/F2/F3/F4/F6 五类回归用例；`skills/quota-guard` 静态不变量由 8 项扩到 10 项 |
| 12 | 2026-11-12 09:50 | AI 助手（编码会话） | v1.4 | 工作项编号核验与提交消息改写 | 按 §8 第 5 条把 `feat`/`fix` 的编号换成**已核验的真实工作项 ID**，并把改写过程与 SHA 变化写入 `docs/07-提交规范说明.md` §4.3 |

> **说明（2026-11-12 订正，如实记录时间差）**：
>
> 上表第 1 条的「读取 v1.0」指的是**按已确认的 Q1–Q12 / A1–A13 规则集开始实现**，
> 当时 `AGENTS.md` **尚未作为文件落盘**；文件本体写于 20:08:35，归档版本写于 20:09:03。
> 由 `evidence/过程时序清单.md`（由 `evidence/会话事件清单.jsonl` 机器推导）可核对：
> 首行业务代码草稿在 **19:35:37** 落盘，需求分析在 **19:30:55** 落盘（**需求分析在编码之前**），
> 而 `AGENTS.md` 文件落盘在 **20:08:35**——**晚于首行代码草稿，但早于任何业务代码入库**
> （首个 `feat` 提交在 20:35 之后），且此后按真实缺陷持续演进 v1.1→v1.4，
> 每条演进都有「触发缺陷 → 规则条款 → 代码落点」的可追溯对照（见 §3、§3b）。
>
> 结论：约束的**内容**在编码前即已确定并被实际使用，约束文件在开发过程中成型并持续演进，
> **不存在完工后补写**；但「文件本体先于首行代码」这一点不成立，故如实标注，不作粉饰。

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

## 3b. v1.4 增补条目与触发缺陷对照

来源：2026-11-11 21:41 起独立运行的两个子会话（独立测试 Agent、独立验收 Agent），
二者均以题面原文与仓库为唯一输入，不接收开发会话的结论。报告原文见
`evidence/independent-test-report.md` 与 `evidence/independent-acceptance-report.md`。

| 增补条款 | 触发的真实缺陷 | 缺陷如何被发现 | 代码落点 |
|---|---|---|---|
| §4 第 11 条：额度上限与降额下限的判定必须与写入在同一条语句内完成 | **D1（中）**：额度上限以参数形式从 `service` 层传入 `ReserveInflight`，并发降额与并发受理交错时实测出现 `occupied=3, quota=0`；降额侧同样是「先读占用、再写额度」两步 | 独立验收 Agent 用 `Barrier` 并发 1 次降额 + 5 次调用，**两次复现**，并直查 SQLite 确认状态持久化 | `internal/store/store.go`（`ReserveInflight` 改为子查询实时读取 `tenants.daily_quota`；新增 `UpdateTenantQuotaWithFloor`）、`internal/service/service.go`（不再传快照） |
| §4 第 12 条：日终报表必须能识别「占用超过额度」 | **D1 加重项**：出现超额后日报仍返回 `consistent=true, mismatch_note=null`，运营看不出超额 | 同上（独立验收 Agent 复现后立即核对日报返回值） | `internal/domain/domain.go`（`BuildDailyReport` 增加 `Occupied() > QuotaLimit` 判定） |
| §8 第 4 条：泄密扫描不得使用白名单 | **D4/D5（低）**：`skills/acceptance-audit` 的 C-14 存在文件级白名单 `allow={docs/01, docs/04, docs/08}`，被抑制的正是 `docs/04` 的 4 行明文口令；而 `docs/09` 却写「不是加白名单绕过」，**文档与代码不符** | 独立验收 Agent 去掉白名单重扫，比对唯一被抑制的行 | `skills/acceptance-audit/scripts/acceptance_audit.py`（删除 `allow`）、`docs/04-部署与使用说明.md`（演示口令改掩码）、`docs/09-AI使用说明.md`（表述改为与代码一致） |

此外，独立测试 Agent 指出 4 处**测试覆盖缺口**（F2/F3/F4/F6，均为低 severity、非功能缺陷），
按 §7「关键业务任务必须有实现及验证链」补齐了回归用例：

| 覆盖缺口 | 补齐用例 | 落点 |
|---|---|---|
| F2：同租户跨凭证越权读取的 403 分支无回归保护 | `TestAppCannotReadRequestOfOtherCredentialInSameTenant` | `internal/httpapi/isolation_test.go` |
| F3：并发提交同编号不同参数的冲突分支未覆盖 | `TestInvokeConcurrentSameRequestNoDifferentParamsConflict` | `internal/service/concurrency_test.go` |
| F4：写入失败必须归还额度的兜底路径未覆盖 | `TestInvokeReleasesQuotaWhenInsertFails`（用 SQLite 触发器注入故障） | `internal/service/concurrency_test.go` |
| F6：报表「处理中」计数只被断言为 0 | `TestDailyReportShowsInFlightWhileRequestProcessing` | `internal/service/concurrency_test.go` |

D1 的回归用例：`TestConcurrentQuotaChangeNeverExceedsQuota`（service 层）、
`TestReserveInflightNeverExceedsLiveQuota`（store 层）、
`TestUpdateTenantQuotaWithFloorRejectsBelowOccupied`（store 层）、
`TestBuildDailyReportFlagsOccupancyAboveQuota`（domain 层）。

---

## 4. 规则到实现的映射索引

| AGENTS.md 条款 | 实现落点 | 验证方式 |
|---|---|---|
| §4 计量口径（12 条） | `internal/domain/domain.go`（`Usage.Remaining/Occupied`、`ValidateQuotaChange`、`BuildDailyReport`）、`internal/store/store.go`（`ReserveInflight`/`CommitSuccess`/`ReleaseInflight`/`UpdateTenantQuotaWithFloor`）、`internal/service/service.go`（`Invoke`/`settle`/`AdjustQuota`） | `internal/domain/domain_test.go`、`internal/store/store_test.go`、`internal/service/invoke_test.go`、`internal/service/concurrency_test.go`；`skills/quota-guard` |
| §5 身份与权限（6 条） | `internal/service/auth.go`（`Identity`/`issueSession`/`AuthorizeTenantAccess`）、`internal/httpapi/server.go`（`identityFrom`/`tenantScope`/`tenantAdminOnly`） | `internal/service/auth_test.go`、`internal/httpapi/server_test.go`、`internal/httpapi/isolation_test.go` |
| §6 代码规范（9 条） | 全仓库 | `gofmt -l .` 空、`go vet ./...` 无告警、`skills/acceptance-audit` |
| §7 测试要求（7 条） | `*_test.go` 全仓库 | `go test ./... -coverprofile`、并发用例、`go test -race` |
| §8 证据与提交（5 条） | `evidence/`、`docs/`、git 提交消息 | `docs/07-提交规范说明.md`、`skills/acceptance-audit` 的 C-14（无白名单）、git log |
| §9 Skill 调用约定 | `skills/quota-guard`、`skills/acceptance-audit` | `docs/08-Skill使用记录.md` |
