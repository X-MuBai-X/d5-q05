# 独立验收报告 · 题目 05《企业模型服务的配额开通、调用、停用与用量核查》

> **脱敏说明**：本报告为独立 Agent 的原始结论，为满足「证据中不得出现明文口令」的要求，
> 仅把文中引用的预置**演示**账号口令做了掩码（`ops-****` 等），
> 其余内容、结论与判定均未改动。完整值见 `cmd/server/main.go` 的 `seedData`（演示用固定口令，非真实密钥）。

> **验收者**：独立验收 Agent（未参与开发）
> **验收时间**：2026-11-11 21:41 ~ 22:0x (UTC+8)
> **被验收项目**：`C:\Users\26765\Documents\trae_projects\skills\2组\素材\d5`（Go 模块 `modelquota`）
> **判定依据（唯一）**：
> - 共同要求：题集原文第 1–184 行
> - 题目 05：题集原文第 371–415 行
> 原文位置：`C:\Users\26765\.minimax\v2\assets\2026\11\11\18-38-19-287-asset_20261111-183819-287_4e49fcd8b2e4_5f8cc178-Go项目业务场景题.md`
>
> **本报告不采信项目自述的任何"已实现/已通过"声明**：所有结论均来自本次独立执行的命令、源码定位与运行时观测。
> 唯一的例外：项目自带的 `skills/acceptance-audit` 结论被当作**被审对象**检查，而非证据。

---

## 0. 结论摘要

| 维度 | 结论 |
|---|---|
| 题目 05 的 8 条业务规则 | **pass 6 / partial 2 / fail 0**（R2、R5 为 partial，同源于缺陷 D1） |
| 5 组必须演示的工作场景 | **pass 5 / partial 0 / fail 0** |
| 共同要求（业务实现 + 可运行 + 质量门禁，12 项） | **pass 7 / partial 3 / fail 1 / unknown 1** |
| **总体** | **PARTIAL** —— 核心业务实现与五组场景扎实可用；但存在 1 个已复现的配额一致性缺陷（D1），且 Git 合并/PR 流程未完成、Meegle 工作项核验未通过 |

**已证实不合规项**（详见 §3）：
- **D1（中）** 降额与并发受理之间的 TOCTOU：可导致 `占用额度 > 日额度`，且日终报表无法发现。
- **D2（中）** `main` 分支未合并任何业务代码，无 PR/合并记录（共同要求 §8.4–8.7 未完成）。
- **D3（中）** Meegle 工作项 `#6974499249` 核验实际失败，编号沿用规范示例（共同要求 §9 未达成）。
- **D4（低）** 自检工具 `acceptance-audit` 的 C-14 判定措辞与实际扫描范围不符；`docs/09` 的"未加白名单绕过"说法与代码事实不符。
- **D5（低）** 预置账号明文口令进入仓库（`docs/04`）。

**无法核验项**（详见 §5）：竞态检测本机复现、GitHub 远端与 PR、远程教学验收环境地址、讲解视频与班级群提交。

---

## 1. 本次实际执行的检查

### 1.1 工具链与质量门禁（全部独立执行，退出码为实测）

```
$ go version
go version go1.27.1 windows/amd64

$ go test ./... -covermode=atomic -coverprofile=$TMP/cover.out      # 退出码 0
ok  modelquota/cmd/mockupstream  4.805s  coverage: 91.9% of statements
ok  modelquota/cmd/server        1.014s  coverage: 82.1% of statements
ok  modelquota/internal/domain   1.202s  coverage: 97.1% of statements
ok  modelquota/internal/httpapi  2.784s  coverage: 87.5% of statements
ok  modelquota/internal/service  5.893s  coverage: 87.2% of statements
ok  modelquota/internal/store    3.104s  coverage: 94.9% of statements
ok  modelquota/internal/upstream 5.203s  coverage: 87.1% of statements

$ go tool cover -func=$TMP/cover.out | grep total:
total:   (statements)   89.2%          # 整体 ≥85% ✅

$ go test ./internal/... -coverprofile=$TMP/cover-internal.out
total:   (statements)   89.6%
# 五个核心业务包 domain/store/service/httpapi/upstream 分别 97.1/94.9/87.2/87.5/87.1 —— 均 ≥85% ✅

$ gofmt -l .          # 无输出，退出码 0 ✅
$ go vet ./...        # 无告警，退出码 0 ✅
$ go test -race ./...  # 退出码 2：go: -race requires cgo; enable cgo by setting CGO_ENABLED=1
                       # 本机 CGO_ENABLED=0 且无 gcc/cc（Get-Command gcc → NOT FOUND）
```

> **竞态检测说明**：本机无 C 编译器，**我无法独立复现** `go test -race`。项目提交的 `evidence/race-test.log`
> 记录的是 `linux/amd64 / CGO_ENABLED=1 / go1.27.4` 容器内运行，退出码 0、全部包 PASS，且日志中列出的
> 用例名与本仓库 `*_test.go` 中的真实函数名逐一对应（`TestInvokeConcurrentLastQuotaSlot`、
> `TestInvokeTimeoutAndFailureAreDistinctTerminalStates` 等），**未发现伪造迹象**，但**不可独立复现** → 记为 unknown。
>
> `cmd/server` 覆盖率 82.1%（<85%）：该包是进程入口（非业务包），整体覆盖率 89.2% 已达标，
> 且项目 CI 门禁定义的"核心业务包"为 domain/store/service/httpapi/upstream，均达标 → 判定 pass，但如实披露该数值。

### 1.2 运行时环境（重要：隔离自建，避免测到旧二进制）

首次按题面建议用 `9091/8080` 启动时失败：

```
[2026/11/11 21:42:58] 服务启动失败: listen tcp 127.0.0.1:8080: bind: Only one usage of each socket address ...
[2026/11/11 21:42:55] 模拟上游模型服务退出: listen tcp 127.0.0.1:9091: bind: Only one usage of each socket address ...
```

`netstat -ano` 显示 **8080 被 PID 39848、9091 被 PID 49004 占用**（即项目自带部署的残留实例）。
这恰好独立复现了项目 `AGENTS.md` §7.6 自己写下的"部署前必须检查端口占用"条款。
为确保测到的是**本次从源码构建的产物**，我改用空闲端口与全新数据库：

```
$ go build -o $TMP/server.exe ./cmd/server            # 退出码 0
$ go build -o $TMP/mockupstream.exe ./cmd/mockupstream # 退出码 0
# 服务端 127.0.0.1:8090 → 上游代理 127.0.0.1:9093 → 项目自带模拟上游 127.0.0.1:9092
# 数据库：$TMP/verify*.db（全新）
```

**自建"计数代理"**：在项目自带模拟上游前架一个转发计数代理（Python，临时目录），
按 `request_no` 统计**真实上游调用次数**。这是判定 R3"不重复调用上游"、
R6"旧编号不触发新上游调用"的关键证据来源——单靠平台自身的返回无法区分"是否真的调了上游"。

### 1.3 独立执行的证据链

- 五组场景全部由我**逐条手工发起并观测**（非运行项目自带脚本得出结论）。
- 另外独立运行了项目自带 `scripts/scenario-verify.py`（全新库、独立实例）：`passed=73 failed=0 exit=0`。
- 独立运行了两个自编 Skill：
  - `python skills/quota-guard/scripts/quota_guard.py --out $TMP/qg.md` → `verdict=PASS checks=8 failed=0 tests=PASS coverage=PASS`，退出码 0
  - `python skills/acceptance-audit/scripts/acceptance_audit.py --out $TMP/aa.md` → `verdict=PASS pass=57 fail=0 pending=0`，退出码 0
- **未修改被验收项目任何文件**（`git status --porcelain` 在验收前后均为空，唯一新增项是本报告文件本身；
  临时脚本全部位于 `%TEMP%\mq-acceptance\`）。

---

## 2. 题目 05 · 8 条业务规则逐条判定

### R1（原文 394 行）运营为租户开通服务并设置额度，管理员创建凭证；凭证所属租户由系统确认，不允许通过请求参数改成其他租户

**结论：pass**

| 原子要求 | 判定 | 证据 |
|---|---|---|
| 运营可开通租户并设置额度 | pass | `cmd/server/main.go:151-157`（预置 tenant-a/tenant-b，额度 10）；`internal/service/service.go:134-209`（OpenTenant 新建或恢复）；实测 `POST /api/v1/ops/tenants/open` → 200，新建租户 `tenant_d890ed969e12f25f` 额度 5 并落库 |
| 管理员可创建凭证 | pass | `internal/service/service.go:287-314`；实测 admin-a 创建 2 个凭证均 200，返回一次性密钥 |
| 凭证归属租户由服务端确定 | pass | `internal/service/service.go:301-308`（`TenantID: tenantID` 来自调用者身份）；`internal/httpapi/handlers.go:49-50`（`identity.TenantID` 传入） |
| 指定其他租户须被拒绝 | pass | `internal/httpapi/handlers.go:45-48`（显式 400）；实测 admin-a 传 `tenant_id=tenant-b` → **400** `凭证所属租户由系统确认，不允许指定其他租户` |
| 预置 2 管理员 + 1 运营、额度 10、最多 2 凭证 | pass | `cmd/server/main.go:151-157,204-215`；`internal/service/service.go:86,295-297`；实测第 3 个凭证 → **400** `凭证数量已达上限: 每个租户最多 2 个凭证` |

### R2（原文 395 行）同一租户所有凭证共享日额度，按 UTC 自然日、首次受理时间归属；成功请求消耗一次，处理中请求计入额度占用，防止并发超过上限

**结论：partial**（核心并发条款本身成立；"不超过上限"这一不变量存在可复现的破坏路径，见 D1）

| 原子要求 | 判定 | 证据 |
|---|---|---|
| 所有凭证共享同一日额度 | pass | 实测：应用一、应用二各调用 1 次后 `admin-a` 查 `usage`：`quota_limit=10, success=2, occupied=2, remaining=8`（非各 10） |
| 按 UTC 自然日归属 | pass | `internal/domain/domain.go:290`（`DayOf`）；实测跨日：新请求 `day=2026-11-12`，旧日请求仍为 `2026-11-11`，两日用量表分列 |
| 以首次受理时间归属、跨日不迁移 | pass | `internal/service/service.go:378,465`（受理时刻写入 `req.Day`）；`internal/service/service.go:545,551,561`（结算用 `req.Day` 而非当前日）；实测跨日复用旧编号时 `req_day` 仍为 `2026-11-11` |
| 成功请求消耗一次 | pass | `internal/store/store.go:564-575`（成功：处理中−1、成功+1，总占用不变）；实测每次成功 `success` +1 |
| 处理中计入额度占用 | pass | 实测在途期间 `usage.in_flight=1, occupied=1, remaining=9`（额度 10） |
| 并发不超上限（同一额度下） | pass | 实测额度 3（已用 1）、10 路并发 → 受理 2、拒绝 8（均 429 `当日额度已用尽`）；在途观测 `in_flight=2, occupied=3, remaining=0`（**未超 3**）；代理计数上游仅 2 次（与受理数一致，无多余调用）。实现为 DB 层单条条件 UPDATE：`internal/store/store.go:549-552` |
| **占用不超过日额度（含额度变更并发）** | **fail** | **缺陷 D1**：降额与受理并发时 `occupied` 可超过 `quota_limit`（见 §3 缺陷 D1） |

### R3（原文 396 行）请求必须携带本租户全局唯一编号；相同编号与参数重复请求不能再次调用上游或重复计量，改参数复用编号须拒绝，不因跨日而允许复用

**结论：pass**

| 原子要求 | 判定 | 证据 |
|---|---|---|
| 必须携带编号 | pass | `internal/domain/domain.go:293-301`；实测缺编号 → 400 `请求编号不能为空`；格式非法 → 400 |
| 租户内全局唯一（跨日不复用） | pass | `internal/store/schema.sql:56-57` 唯一索引建在 `(tenant_id, request_no)`（**未按日分区**，schema 注释亦明写此意图） |
| 同编号同参数：不重复调用上游 | pass | `internal/service/service.go:390-411`；实测代理计数：`S4-DUP` 上游调用数 **1**（两次提交后仍为 1） |
| 同编号同参数：不重复计量 | pass | 实测第二次 `replayed=true`，`usage.success` 保持 1；`model_requests.replay_count` 落库 |
| 同编号改参数：拒绝 | pass | `internal/service/service.go:393-397`；实测 → **409** `请求编号已被占用且参数不一致`，代理计数仍为 1（**未再调上游**） |
| 跨日不得复用编号 | pass | 实测（教学时钟推进到 2026-11-12）：同编号同参数 → `replayed=true`、`req_day=2026-11-11`、上游调用数仍 1；同编号改参数 → **409** |
| 编号冲突计入被拒绝统计 | pass | 写入 `rejected_attempts` 独立表（`internal/store/schema.sql:65-76`），实测 `rejected_attempts` 中可见 `S4-DUP` 冲突记录 |

### R4（原文 397 行）上游明确失败或超过 2 秒按本题规则判超时，释放额度占用，不计成功量；迟到结果只保留为诊断记录，不改变已经返回的超时结论或追加扣额

**结论：pass**

| 原子要求 | 判定 | 证据 |
|---|---|---|
| 超时阈值 2 秒 | pass | `internal/domain/domain.go:81`（`UpstreamTimeout = 2 * time.Second`）；`internal/upstream/httpclient.go:130-150`；实测 `timeout` 场景耗时 **2.02 s** 返回 |
| 失败与超时是两个独立终态 | pass | `internal/domain/domain.go:48-52`；`internal/service/service.go:538-563`（`settle` 三条独立分支）；实测同一租户同日日报中 `failed=1` 与 `timeout=2` **分列统计**（对照 R8 要求必须五类分列，故此解释成立） |
| 失败释放占用、不计成功量 | pass | `internal/service/service.go:557-561`；实测 `failed` 后 `success=0, in_flight=0, occupied=0` |
| 超时释放占用、不计成功量 | pass | `internal/service/service.go:547-551`；实测 `timeout` 后 `success=0, occupied=0`（剩余额度回到 10） |
| 迟到结果只落诊断、不改终态 | pass | `internal/upstream/httpclient.go:139-149`（判超时后**不取消**上游，后台等待）；`internal/service/service.go:604-639`（`handleLateResult` 仅在 `req.Status == RequestTimeout` 时写诊断）；实测 `slow_success`（上游 3 s 返回）：初始 `timeout` → 等待 3.5 s 后仍为 **`timeout`**，`diagnostics` 出现 1 条 `late_result`（`upstream_id=mock-14，耗时=3.015s`） |
| 迟到结果不追加扣额、不计成功量 | pass | 实测迟到后 `usage.success` 仍为 **0**、`occupied=0`、`remaining=10`；运营 `GET /api/v1/ops/diagnostics?tenant_id=tenant-b` 返回该条诊断 |

### R5（原文 398 行）运营可以提高额度；降低额度时不得低于当日已成功数量加处理中数量。每次变更记录操作人、原因和生效时间，不改变既有请求编号和日期归属

**结论：partial**（单次操作全部正确；并发场景下下限不变量可被破坏，见 D1）

| 原子要求 | 判定 | 证据 |
|---|---|---|
| 可提高额度 | pass | `internal/service/service.go:213-251`；实测 10 → 20 返回 `change{old_quota:10,new_quota:20,operator:"ops",reason:...,effective_at:...}` |
| 降额下限 = 当日已成功 + 处理中 | pass（单次） | `internal/domain/domain.go:346-356`（`ValidateQuotaChange` 比对 `Occupied()`）；实测降额到 3 → **400** `降低额度不得低于当日已成功数量加处理中数量: 当日已成功 11 + 处理中 0 = 11`；降额到恰好 11 → 200（边界正确） |
| 记录操作人/原因/生效时间 | pass | `internal/store/schema.sql:90-99`（`quota_changes` 表含 `operator/reason/effective_at`）；实测日报 `quota_changes` 4 条，字段齐全 |
| 变更原因必填 | pass | `internal/service/service.go:214-216`；实测空原因 → 400 `配额变更原因不能为空` |
| 不改变既有请求编号与日期归属 | pass | 实测跨日复用旧编号仍 `replayed=true`、`req_day=2026-11-11`；降额/增额后旧记录 `day` 未变 |
| **降额后下限不变量持续成立** | **fail** | **缺陷 D1**：降额提交与并发受理竞态时，下限在事后被突破 |

### R6（原文 399 行）管理员停用凭证或运营停用租户后，不再受理新调用；已受理请求正常结束并按原规则计量。停用凭证不能通过再次请求旧编号触发新的上游调用

**结论：pass**

| 原子要求 | 判定 | 证据 |
|---|---|---|
| 停用凭证后不受理新调用 | pass | `internal/service/service.go:439-443`；实测 → **409** `凭证已停用，不再受理新调用` |
| 停用租户后不受理新调用 | pass | `internal/service/service.go:423-427`；实测 → **409** `租户已停用，不再受理新调用` |
| 已受理请求正常结束并按原规则计量 | pass | 实测（借助计数代理的 `proxy_delay=1.5` 制造真实在途）：在途期间 `usage.in_flight=1` → 停用凭证 → 该请求**以 `success` 正常结束**且 `success` 由 0 变 1、占用由 1 变 0（**照常计量**）；停用租户时同样成立（`success` 1→2） |
| 停用后新请求不触发上游 | pass | 代理计数：停用后新编号 `S3A-NEW-AFTER-DISABLE` 与 `S3B-NEW-AFTER-TENANT-DISABLE` 的**上游调用数均为 None（0 次）** |
| **停用凭证后旧编号不触发新上游调用** | pass | 实测停用后以旧编号 `S3A-INFLIGHT` 再次提交 → 200 `replayed=true`，代理计数该编号上游调用**仍为 1**（未新增） |
| 停用动作留痕 | pass | `internal/store/schema.sql:103-113`；实测日报 `events` 含 `credential_disabled / actor=admin-b / reason=疑似泄露，立即停用` |

### R7（原文 400 行）管理员可查看本租户全部调用、成功量、处理中量和剩余额度；普通应用仅可查询由其凭证创建的请求。运营可跨租户核查，其他角色不可越权

**结论：pass**

| 原子要求 | 判定 | 证据 |
|---|---|---|
| 管理员可看本租户成功量/处理中量/剩余额度 | pass | `internal/httpapi/handlers.go:87-110`；实测返回 `quota_limit/success/in_flight/occupied/remaining` |
| 管理员可看本租户全部调用（含被拒绝） | pass | `internal/httpapi/handlers.go:112-130`；实测 `admin/requests` 同时返回 `requests` 与 `rejected` |
| 普通应用仅可查**其凭证创建**的请求 | pass | `internal/httpapi/handlers.go:192-200`；实测同租户两凭证：凭证一列表 `['S4-FAIL','S4-TIMEOUT','S4-LATE','S4-DUP','S4-RACE-01','S4-RACE-00']`，凭证二列表 `['S7B-BY-CRED2']`，**无交集** |
| 应用查他凭证请求被阻断 | pass | `internal/httpapi/handlers.go:214-217`；实测同租户他凭证请求 → **403** `无权访问该资源` |
| 应用不可访问管理/运营接口 | pass | `internal/httpapi/server.go:197-205`；实测 `GET /api/v1/admin/usage` → 403；`POST /api/v1/admin/credentials/create` → 403 |
| 租户管理员不可访问运营接口 | pass | `internal/httpapi/server.go:175-183`；实测 `GET /api/v1/ops/tenants` → 403；`POST /api/v1/ops/tenants/quota` → 403 |
| 租户管理员不可越租户 | pass | `internal/httpapi/server.go:225-240`；实测 admin-a 查 tenant-b 的 `usage`/`requests`/`credentials` → **400**（显式拒绝，非静默忽略，符合 `AGENTS.md` §5.1） |
| 运营可跨租户核查 | pass | 实测 `GET /api/v1/ops/report/daily?tenant_id=tenant-b` → 200；未指定 `tenant_id` → **400** `运营跨租户核查必须指定 tenant_id` |
| 不可通过改请求中的用户/租户编号越权 | pass | `internal/httpapi/server.go:144-160`（身份只从令牌推导）；实测携带 `user_id=admin_ops` → 403（参数不被采信） |
| 未认证被拒 | pass | 实测 → **401** `身份认证失败` |

### R8（原文 401 行）日终能够按租户、日期列出成功、失败、超时、处理中和被拒绝请求，核查成功记录与用量是否一致，说明每次额度变化和停用影响

**结论：pass**（五类分列、跨日、跨租户、一致性核对、变更与停用说明均实测通过；一致性核对的**覆盖范围**有一处局限，见 D1 补充）

| 原子要求 | 判定 | 证据 |
|---|---|---|
| 按租户与日期分别列出 | pass | `internal/httpapi/server.go:433-446`；`internal/service/service.go:673-703`；实测 tenant-a 与 tenant-b 同日报表数据不同（`success=3` vs `success=3,failed=1,timeout=2,rejected=12`），互不串扰 |
| 五类分列（成功/失败/超时/处理中/被拒绝） | pass | `internal/domain/domain.go:363-388`（`StatusOrder` 归一化，缺失类补 0）；实测某租户某日 `counts={'failed':0,'processing':0,'rejected':0,'success':3,'timeout':0}` —— 零值类别**仍在列** |
| "处理中"真实可见 | pass | 实测在途期间日报 `processing=1, usage_in_flight=1`；请求结束后该请求从 `processing` 移至 `timeout`（`processing=0, timeout=1`） |
| 被拒绝请求单独计数 | pass | `internal/store/schema.sql:65-76`；实测 `rejected_attempts` 表含 12 条（8 条额度用尽 + 1 条编号冲突 + 3 条额度用尽），日报 `rejected=12` |
| 成功记录与用量一致性核对 | pass | `internal/domain/domain.go:383-386`；实测 `consistent=true`、`mismatch_note` 为空；不一致时 `MismatchNote` 有文案 |
| 说明每次额度变化 | pass | 实测日报 `quota_changes` 列出 4 次变更（old/new/operator/reason/effective_at） |
| 说明停用影响 | pass | 实测日报 `events` 列出 `credential_disabled`（含 actor、reason） |
| 跨日请求分列 | pass | 实测教学时钟推进 36821 秒到 `2026-11-12T00:00:04Z`；11-11 与 11-12 两份报表独立且各自 `consistent=true` |

> **局限（已计入 D1）**：一致性核对只比较"成功记录数 == 计入用量次数"，**不检查"占用额度 ≤ 日额度"**。
> 因此 D1 造成的超额状态在日终报表里**不会报警**（实测 `quota_limit=0, occupied=3` 时仍报 `consistent=true`），
> 运营无法通过现有报表发现该异常——这削弱了 R8"核查…是否一致"的实际保障能力。

---

## 3. 5 组必须演示的工作场景（本人独立动手跑通）

### 场景 1 · 开通使用（原文 405 行）— **pass**

| 步骤 | 实测结果 |
|---|---|
| 运营开通 | `POST /ops/tenants/open` → 200，tenant-a 状态 active、额度 10，并留痕 quota_change（operator=ops） |
| 管理员建两个凭证 | admin-a 创建"应用一""应用二" → 均 200，各自返回一次性密钥（`sk_…`） |
| 两个应用调用 | 应用一 `S1-REQ-001` → success；应用二 `S1-REQ-002` → success；**代理计数上游各 1 次** |
| 管理员查共享额度 | `usage = {quota_limit:10, success:2, in_flight:0, occupied:2, remaining:8}` —— **两凭证共享同一额度**，非各 10 |

### 场景 2 · 活动增额（原文 406 行）— **pass**

| 步骤 | 实测结果 |
|---|---|
| 额度耗尽 | 补打 8 次后 `success=10, remaining=0`；第 11 次 → **429** `当日额度已用尽`，且**代理计数该编号上游调用 = 0**（被拒请求确实没调上游） |
| 运营增加额度 | → 200，`change{old:10,new:20,operator:ops,reason:"演示活动前临时增加额度",effective_at:...}` |
| 应用恢复调用 | `S2-REQ-012` → success，`quota_limit` 变 20，`success` 11，**上游调用 1 次** |
| 非法降额被拒并保留原因 | 降额到 3 → **400** `降低额度不得低于当日已成功数量加处理中数量: 当日已成功 11 + 处理中 0 = 11`（含具体数字，非泛化文案）；降额到恰好 11 → 200 |
| 变更原因必填 | 空原因 → 400 `配额变更原因不能为空` |

### 场景 3 · 事故处置（原文 407 行）— **pass**

| 步骤 | 实测结果 |
|---|---|
| 存在在途请求 | 计数代理延迟 1.5 s，实测在途期间 `in_flight=1, occupied=1` |
| 停用凭证 | admin-b 停用 → 200，事件留痕 |
| 新请求受限 | 新编号 → **409** `凭证已停用，不再受理新调用`；**上游调用 0 次** |
| 原请求完成 | 在途请求以 **success** 正常结束，`success` 0→1（照常计量） |
| 运营查明最终用量 | `usage={success:1,in_flight:0,occupied:1,remaining:9}`；日报 `events` 含 `credential_disabled` |
| 停用租户（同样路径） | 在途请求同样正常结束并计量（success 1→2）；新请求 → **409** `租户已停用，不再受理新调用`；上游调用 0 次 |

### 场景 4 · 上游异常（原文 408 行）— **pass**

| 子场景 | 实测结果 |
|---|---|
| 上游明确失败 | `S4-FAIL` → 终态 **failed**，`error_reason=上游明确失败`；`success=0, in_flight=0`（占用已释放） |
| 超时 | `S4-TIMEOUT` → 终态 **timeout**，耗时 **2.02 s**，`error_reason=上游调用超过 2 秒，判为超时`；`success=0`（占用已释放） |
| 迟到成功 | `S4-LATE`（上游 3 s 返回）→ 平台 2 s 判 **timeout**；3.5 s 后复查：终态仍 **timeout**、`success` 仍 **0**、`diagnostics` 出现 `late_result`（`upstream_id=mock-14，耗时=3.015s`）；**未改终态、未追加扣额** |
| 重复请求 | 同编号同参数两次 → 第二次 `replayed=true`；**代理计数上游 = 1**；`success` 仍为 1（**不重复计量**） |
| 同编号改参数 | → **409**；上游计数仍 1 |
| 最后一个额度并发争抢 | 额度 3（已用 1）、**10 路并发** → 受理 **2**、拒绝 **8**（均 429 `当日额度已用尽`）；在途 `in_flight=2, occupied=3, remaining=0`（**恰好 3，未超**）；最终 `success=3, occupied=3, quota=3`；**代理计数上游总调用 = 2**，与受理数完全一致（无多余上游调用、无重复计量） |

### 场景 5 · 日终核查（原文 409 行）— **pass**

| 子场景 | 实测结果 |
|---|---|
| 跨日请求 | 教学时钟推进至 `2026-11-12T00:00:04Z`；新请求 `day=2026-11-12`；两日报表独立且各自 `consistent=true` |
| 跨日不复用编号 | 旧编号同参数 → `replayed=true`、`req_day` 仍 2026-11-11、**上游计数不变**；旧编号改参数 → **409** |
| 两租户数据隔离 | tenant-a 与 tenant-b 同日 `counts` 完全不同（`success=3` vs `success=3,failed=1,timeout=2,rejected=12`），`tenant_id` 各自正确 |
| 普通应用与管理员权限差异 | 应用仅见本凭证请求（两凭证列表无交集）；应用查他凭证请求 → 403；应用访问管理接口 → 403；管理员访问运营接口 → 403；管理员跨租户 → 400（显式拒绝）；运营跨租户 → 200；未认证 → 401 |

---

## 4. 已证实的不合规 / 缺陷

### D1（中）降额与并发受理的 TOCTOU：占用额度可超过日额度，且日终报表无法发现

**触发条件**：运营执行降额（或额度处于较高值）与客户应用发起调用的**同一时刻**并发发生。
降额把 `tenants.daily_quota` 调低后，一个**在降额提交前**读到旧额度值的并发请求仍会按旧额度通过占用判定。

**根因（源码定位）**：
- `internal/service/service.go:419` 读取租户行（含 `DailyQuota`）；
- `internal/service/service.go:446` 把这个**快照值**作为参数传给 `ReserveInflight`：
  `s.db.ReserveInflight(ctx, tenantID, day, tenant.DailyQuota)`；
- `internal/store/store.go:549-552` 的条件 UPDATE 用的是这个**外部传入的** quota：
  `... WHERE tenant_id=? AND day=? AND (success_count + inflight_count) < ?`（`?` = 陈旧快照），
  而不是从 `tenants.daily_quota` 在同一条语句内读取。
- 降额路径 `internal/service/service.go:224-235` 先校验下限、再更新 `tenants.daily_quota`；
  二者之间没有互斥，校验时用的 `usage` 也可能在下一次占用的瞬间被越过。

**复现步骤（我实际执行，退出码 0，缺陷稳定复现）**：
1. 新建干净库，启动服务（`-upstream` 指向计数代理）。
2. `POST /api/v1/ops/tenants/quota {tenant_id:"tenant-b", daily_quota:50}`（额度抬到 50，occupied=0）。
3. 用 `threading.Barrier(6)` 同时启动 5 个应用调用线程（`request_no` 互不相同）与 1 个运营降额线程
   （降额目标 = 当前 occupied，**该降额按 R5 是合法的**）。
4. 等待全部结束，读取 `GET /api/v1/admin/usage`。

**观测输出（第一次）**：
```
>>> VIOLATION: [{'trial': 0, 'occupied': 3, 'quota': 0, 'accepted': 3, 'lower_status': 200}]
final {'occupied': 3, 'quota_limit': 0, 'remaining': 0, 'success': 3, 'tenant_id': 'tenant-b'}
```
**观测输出（复跑第二次）**：
```
>>> VIOLATION: [{'trial': 0, 'occupied': 4, 'quota': 3, 'accepted': 4, 'lower_status': 200}]
```
即：**额度已被运营降到 0（或 3），平台仍受理了 3–4 个请求**，租户实际消费超出其日额度。

**影响**：
1. 直接违反 **R2**"防止并发超过上限"与 **R5**"降低额度时不得低于当日已成功数量加处理中数量"的不变量；
2. **静默失败**——日终核查无法发现：实测该状态下 `GET /api/v1/ops/report/daily` 返回
   `{'quota_limit': 0, 'remaining': 0, 'usage_success': 3, 'consistent': True, 'mismatch_note': None}`，
   运营看到的是"一致"，不会察觉超额；
3. `Usage.Remaining()`（`internal/domain/domain.go:278-284`）把负数钳到 0，进一步掩盖差额；
4. 该状态**持久化在数据库**中（`tenant_day_usage.success_count=3` 而 `tenants.daily_quota=0`），重启后依旧存在。

**修复方向**（供参考，本次验收未修改任何项目文件）：
把额度判定下沉到同一条 SQL 内读取当前额度，例如
`UPDATE tenant_day_usage SET inflight_count=inflight_count+1 WHERE tenant_id=? AND day=? AND (success_count+inflight_count) < (SELECT daily_quota FROM tenants WHERE id=?)`，
或在 `AdjustQuota` 降额与 `Invoke` 占用之间引入同一把数据库级互斥（`BEGIN IMMEDIATE` 事务）；
并建议在日终报表中增加 `occupied <= quota_limit` 的显式核对项。

### D2（中）`main` 分支未合并任何业务代码，无 PR / 合并记录（共同要求 §8.4–8.7）

**证据**（只读 git 操作，未改变仓库状态）：
```
$ git branch -vv
* dev/05   04f260f [docs] [v2.2.0] 补充过程文档与项目约定版本历史
  main     d75aebd [docs] [v2.1.0] 初始化题目 05 项目仓库说明

$ git log --oneline main
d75aebd [docs] [v2.1.0] 初始化题目 05 项目仓库说明        # main 上只有这一条

$ git diff --stat main dev/05
... 77 files changed, 20314 insertions(+), 4 deletions(-)   # 全部业务代码只在 dev/05

$ git remote -v
（空）                                                 # 未配置任何远端
```
- 题面共同要求 §8.4「测试通过才申请合并 …… 从开发分支向 main 发起 PR」——**未发生**；
- §8.6「合并后必须保留开发分支原始提交与 PR 的可追溯关系」——**无合并**；
- §8.7「合并后自动部署：针对主分支实际合并版本重新执行质量门禁、构建和部署」——**不可能发生**；
- §6.7「开发分支测试、PR 检查、Agent 验收和合并必须有明确的提交版本对应关系」——**缺合并这一环**。

**部分环境归因**：项目在 `docs/09-AI使用说明.md` §6 与 `docs/04` §12 中如实声明：无 GitHub MCP、无 `gh` CLI、`github.com` 不可达，
故无法产生 PR / 分支保护 / 远端记录。**GitHub 远端部分**属环境受限 → unknown；
但**"合并到 main"这一动作本身在本机即可完成且未完成**，且未被列入项目自己的待补证清单 —— 判 **fail**（该缺口不是环境造成的，是流程未走完）。

### D3（中）Meegle 工作项核验实际失败，`#6974499249` 沿用规范示例编号（共同要求 §9）

**证据**：`evidence/meegle-workitem-verification.log` 原始输出：
```
"error": { "code": "SERVER_CALL_FAILED",
  "message": "error=ProjectNotFound,message=project: r17te9 not found,retriable=false" }
```
以及项目自述 `docs/07-提交规范说明.md` §4.2：「编号按附件《commit-convention.md》**示例中给出的编号**沿用」。

**判定**：共同要求 §9 明确「**未完成确认和有效核验前不得将该次提交视为合规**；不得伪造编号或声称查询成功」。
项目**没有伪造**（如实记录失败、标注待补证），这一行为本身是诚实的 ✅；
但 §9 的达标条件（`feat`/`fix` 提交携带**已核验**的真实工作项编号）**未达成** → 判 **fail**，
受影响提交：`5d97538`、`02c2e21`（feat）、`32e57bb`（fix）。
（此项本身受教师侧"项目空间可见性/权限"影响，但在验收时点上确为未合规。）

### D4（低）自检工具 `acceptance-audit` 的 C-14 判定措辞与实际扫描范围不符；`docs/09` 的"未加白名单绕过"说法与代码事实不符

- `evidence/acceptance-audit/检查项明细.md:14` 声明：C-14「证据中无明文口令/密钥」= pass，证据文字为
  「**证据与文档中未发现**明文口令/密钥」。
- 实际代码 `skills/acceptance-audit/scripts/acceptance_audit.py:431` 存在显式白名单：
  ```python
  allow = {"docs/01-需求分析.md", "docs/04-部署与使用说明.md", "docs/08-Skill使用记录.md"}
  ...
  if rel in allow: continue
  ```
  第 442 行**明确匹配** `admin-a-**** / admin-b-**** / ops-****` 三个字符串，
  而 `docs/04-部署与使用说明.md` 恰恰被白名单跳过。
- 我把白名单去掉后重跑同一扫描（临时脚本，未改项目文件），结果：**白名单中 docs/01、docs/08 零命中（条目是多余的），
  唯一被白名单实际抑制的命中就是 `docs/04`** 的 4 行明文口令。
- 而 `docs/09-AI使用说明.md:110` 写道：「`acceptance-audit` 的 C-14 报 fail 后，处理方式是**收紧**正则精度并真正脱敏文档，
  **而不是加白名单绕过**」——**与代码事实不符**（白名单确实存在，且是 C-14 通过的直接原因）。

**说明（避免过度解读）**：C-14 本身"按设计"通过并非全无道理——题面允许预置账号，教师需要凭据才能登录验收，
且 `docs/04:125` 明确写了「上线到真实环境前必须替换这些初始口令」。本条按**低severity 的表述准确性问题**记录，
不判为泄密缺陷。

### D5（低）预置账号明文口令进入仓库

`docs/04-部署与使用说明.md:119-121,201` 明文写出 `ops-****` / `admin-a-****` / `admin-b-****`。
共同要求 §3「禁止泄露密钥和用户敏感信息；日志、截图和对话导出均须脱敏」、§8.9「任何密钥、访问令牌及敏感数据不得进入仓库或视频」。
`evidence/` 目录经我扫描**未发现**明文口令或密钥 ✅（无 `sk_*`、无 `Bearer <token>`、无明文口令）。
考虑到本题面明确允许预置账号、且该文档同时给出了轮换提示，本条按**低severity 观察项**记录，供教师裁量。

---

## 5. 共同要求 · 与"业务实现 + 可运行 + 质量门禁"直接相关条目

| # | 要求（原文行号） | 判定 | 证据 |
|---|---|---|---|
| C-1 | §1.1 实现范围：以本题完整业务闭环为交付范围（行 17） | pass | 仅实现题目 05：`internal/{domain,store,service,upstream,httpapi}` + `cmd/{server,mockupstream}`；无越界模块 |
| C-2 | §1.2 可操作的最小业务页面（行 18） | pass | 三个页面 + 表单：`internal/httpapi/web/templates/{ops,admin,app,login}.html`；实测 `/login` `/ops` `/admin` `/app` 均 200 且含表单控件；`ops.html` 含 `tenant_id/daily_quota/reason/day/name` 等输入项 |
| C-3 | §1.3 数据库保存核心业务数据、重启后仍可查询（行 19） | pass | SQLite（`modernc.org/sqlite` 纯 Go 驱动）；`internal/store/schema.sql`（10 张表）；**实测**：同一 DB 重启服务后 `report_A 2026-11-11 = {success:4}`、`report_A 2026-11-12 = {success:1}`、`diagnostics` 仍在（2 条） |
| C-4 | §1.5 可控 HTTP 模拟上游，能触发成功/失败/超时/重复（行 21） | pass | `cmd/mockupstream/main.go:103-131` 五种场景（success/fail/timeout/duplicate/slow_success）；实测四种均触发且平台给出不同终态 |
| C-5 | §1.9 各业务分支可操作且有持久化结果，非静态页面/写死成功（行 25） | pass | 全部场景以真实 HTTP 调用驱动，落库 `model_requests/tenant_day_usage/rejected_attempts/diagnostics/quota_changes/tenant_events`；我用 `sqlite3` 直查库确认行存在（如 `('S4-FAIL',...,'failed',...)`） |
| C-6 | §2 七阶段开发流程及证据（行 29-41） | pass（流程证据齐备） | 需求分析 `docs/01`、原型 `docs/02`、任务规划 `docs/03`、`AGENTS.md` v1.0→v1.3 + 历史版本、测试 `*_test.go` + `evidence/`、验收 `evidence/acceptance-audit/`、部署 `evidence/deployment.log` + `scripts/deploy-local.ps1`；提交历史按阶段分型 |
| C-7 | §2 三个 Agent 角色须使用相互独立的会话/任务上下文（行 41） | partial | 项目**自陈**："已用独立 worker 子 Agent 撰写过程文档；但**独立验收**由主会话内自编 Skill 承担，**未完全脱离上下文**"（`docs/09-AI使用说明.md:104`）。测试/开发用了独立 worker 会话（`docs/06` 事件清单含 `task`/`task_output` 调用），但**验收角色未独立**——诚实标注，但仍不满足 §2 原文 |
| C-8 | §3 项目级 `AGENTS.md` 须在开发前编写并实际使用、保留版本历史与读取记录（行 47-59） | pass | `AGENTS.md`（当前 v1.3，含 v1.0–v1.3 版本历史表，行 187-195）；`docs/agents/AGENTS-v1.0.md`、`AGENTS-v1.2.md` 归档；`docs/agents/AGENTS-读取记录.md` 列出 7 条带时间戳的读取记录与"触发缺陷→规则条款→代码落点"映射；v1.1 增补条目均对应真实暴露的缺陷（模板嵌套 `define` 导致服务无法启动等） |
| C-9 | §4 至少两个自编 Skill（业务专项 + 验收检查），含七要素且真实调用（行 63-70） | pass | `skills/quota-guard/SKILL.md`（业务专项）、`skills/acceptance-audit/SKILL.md`（验收检查），二者均含用途/触发/输入/步骤/输出/失败处理/完成标准；**我实际运行两者均退出码 0 并产出带 file:line 证据的报告**；`docs/08-Skill使用记录.md` 记录了 quota-guard 第 1 次 **FAIL**→修正→PASS 的真实修正过程（非编造） |
| C-10 | §5 导出全过程提示词与结构化事件清单（行 87-94） | pass | `docs/06-会话导出.md` + `evidence/会话事件清单.jsonl`：**957 行，Python 解析 0 错误**；事件类型分布 `prompt=10 / assistant_text=163 / tool_call=377 / tool_result=376 / system_reminder=30 / compaction=1`，与文档表格完全一致；末条事件(seq 957)可见 `scripts/export_session.py` 的真实调用参数；不可获取字段如实标注"平台不提供" |
| C-11 | §6.1 整体 ≥85% 且各核心业务包分别 ≥85%（行 100） | pass | 实测整体 **89.2%**；domain 97.1 / store 94.9 / service 87.2 / httpapi 87.5 / upstream 87.1，**全部 ≥85%**。`cmd/server` 82.1%（进程入口包，非业务包，不计入"核心业务包"）；无任何覆盖率排除指令 |
| C-12 | §6.2 覆盖正常/边界/非法状态/重复操作/权限/异常路径 + 可重复并发验证（行 101） | pass | 并发用例存在且可重复：`TestReserveInflightConcurrentLastSlot`、`TestInvokeConcurrentLastQuotaSlot`、`TestInvokeConcurrentSameRequestNo`、`TestInvokeConcurrentSameRequestNoDifferentParams`；**我独立跑通 10 路并发实测**（受理 2/拒绝 8，上游调用数 2） |
| C-13 | §6.4 提交单元测试命令与原始输出、覆盖率原件（行 103） | pass | `evidence/coverage.out`、`evidence/coverage-summary.txt`、`evidence/race-test.log`、`evidence/smoke-test.log`、`evidence/scenario-verify.{log,json}` 均在仓库内；我本地重跑命令与结果一致 |
| C-14 | §6.5 gofmt / go vet / 竞争检测（行 104） | partial | **gofmt**：实测 `gofmt -l .` 无输出 ✅；**go vet**：实测退出码 0 ✅；**竞争检测**：本机无 C 编译器，**无法独立复现**；提交的 `evidence/race-test.log` 显示容器内 `CGO_ENABLED=1` 运行、退出码 0、用例名与本仓库一致（无伪造迹象）但**不可复现** → 见 §6 unknown-2 |
| C-15 | §3 禁止伪造结果（行 54） | pass | `evidence/smoke-test.log` 保留了一次**真实失败**的运行（"退出码：1"，`通过 38 项，失败 1 项`，失败项为「应用查询本凭证请求 期望 200 实际 403」），后续 `evidence/deployment.log`（21:27:03）为 `通过 39 项，失败 0 项` —— **失败证据未被删除**；Meegle 核验失败如实上报。**但** 见 D4：`docs/09:110` 对 C-14 修正方式的描述与代码事实不符 |
| C-16 | §3 禁止泄密（行 56） | partial | `evidence/` 全目录扫描**无**明文口令/密钥 ✅；但 `docs/04:119-121,201` 明文预置口令（见 D5），且 C-14 依赖白名单放行（见 D4） |
| C-17 | §7 自动部署流水线（质量检查/测试/覆盖率门禁/构建/部署/健康检查/冒烟）（行 111） | partial | `.github/workflows/ci.yml` **真实存在**：`quality` 作业含 gofmt 零差异门禁、go vet、单测+覆盖率门禁（整体与五个核心包 ≥85%，逻辑与 CI 内联脚本一致）、`go test -race`；`smoke` 作业真实构建、启动、健康检查并执行 `scripts/smoke-test.sh`。**但无"部署到目标环境"作业**，且流水线配置只存在于仓库中，**无任何流水线运行记录** |
| C-18 | §8 GitHub 仓库 / 开发分支 / PR 合并流程（行 132-140） | fail | 见 **D2**：`main` 仅含初始化 README 提交，77 个文件 / 20314 行业务代码全在 `dev/05`，**无 remote、无 PR、无分支保护、无合并** |
| C-19 | §9 提交消息格式与工作项编号核验（行 146-171） | partial | **格式 pass**：`git log` 全部符合 `[类型] [版本号] [编号] 中文描述`，`feat/fix` 带 `#6974499249`，一次提交单一类型；**编号核验 fail**：见 **D3**（核验实际失败） |
| C-20 | §10 讲解视频与班级群提交（行 173-183） | unknown | 属人工交付物，仓库内无视频、无群提交记录；项目在 `docs/04` §12 / `docs/09` §6 已列为待补证。**我无法核验，也无法断言学生未做** |

---

## 6. 无法核验项（unknown）——缺什么证据、为何无法确认

1. **竞态检测（`go test -race`）的真实结论**
   缺：可运行的 `-race` 环境。本机 `CGO_ENABLED=0`、`gcc`/`cc` 均 NOT FOUND，命令直接以退出码 2 失败。
   仓库内 `evidence/race-test.log` 声称在 `linux/amd64 / CGO_ENABLED=1 / go1.27.4` 容器内运行且全部 PASS，
   用例名与本仓库一致（无明显伪造），但**我无法独立复现**。
   → 判定 **unknown（不判 fail，也不判 pass）**。

2. **GitHub 仓库创建记录、PR 记录、分支保护设置、主分支合并**
   缺：GitHub 远端与平台访问。本仓库 `git remote -v` 为空；本验收环境无 GitHub MCP、无 `gh` CLI，
   且按项目自述 `github.com` 在其网络不可达。**但可本地核验的部分（合并到 `main`）确实未做**（D2）。
   → GitHub 远端部分 **unknown**；合并动作本身 **fail**。

3. **远程教学验收环境的部署地址与上线冒烟**
   缺：可访问的远端主机/容器运行时与验收地址。
   项目采用**本机真实进程部署**（`http://127.0.0.1:8080`），并留下真实部署日志；
   我**独立佐证**了部署的真实性：`evidence/deployment.log` 记录的 PID 39848/49004 与验收开始时
   `netstat -ano` 实际占用 127.0.0.1:8080 / 9091 的进程 PID **完全一致**。
   但题面 §1.7/§7 要求的"指定教学验收环境 + 可访问服务地址 + 流水线运行标识"**无法核验**。
   → **unknown**。

4. **CI 流水线的实际运行记录**
   缺：GitHub Actions 运行记录 / 流水线 ID / 制品链接。仓库只有 `.github/workflows/ci.yml` 配置本身，
   `evidence/` 内无任何流水线运行标识。**"仅提交流水线配置"不满足 §7「必须完成一次实际部署」**，
   但是否在别处运行过**无法核验**。
   → **unknown**（但配置本身的门禁逻辑经我本地复算确认正确）。

5. **讲解视频与班级群提交记录**
   缺：视频文件/带时间点讲解目录/群截图。属人工交付物，项目已自列待补证。
   → **unknown**。

6. **Meegle 工作项在目标空间中的真实存在性**
   缺：`r17te9` 空间的访问权限。核验已真实执行并失败（`ProjectNotFound` / `WorkItemNotFound`）。
   判定为 **fail（§9 达标条件未达成）**，但"该编号在正确空间中是否真实存在"这一点本身仍 **unknown**。

---

## 7. 验收过程中被独立证伪 / 证实的"自述"

| 项目自述 | 独立核验结果 |
|---|---|
| "五组场景 73 项断言全过" | **证实**：`scripts/scenario-verify.py` 在全新库独立实例上 `passed=73 failed=0 exit=0`；我另手工重跑五组场景，结论一致 |
| "整体与核心包覆盖率均 ≥85%" | **证实**：实测整体 89.2%，五核心包 97.1/94.9/87.2/87.5/87.1 |
| "gofmt 零差异 / go vet 无告警" | **证实**（实测退出码 0 / 无输出） |
| "竞态检测退出码 0、未检出数据竞争" | **未证实**（本机无法复现；日志无伪造迹象）→ unknown |
| "本机真实进程部署成功，服务地址 127.0.0.1:8080" | **证实**（PID 与 netstat 实测吻合） |
| "日终报表五类恒定分列" | **证实**（零值类别仍在列；"处理中"实测可见） |
| "迟到结果只落诊断、不改终态、不追加扣额" | **证实**（终态仍 timeout、success 仍 0、diagnostics 1 条） |
| "同一编号重复请求不重复调用上游" | **证实**（代理计数恒为 1） |
| "验收逐条核查 pass 57 / fail 0 / pending 0" | **不成立**：该自检漏掉了 D1；其 C-13 仅检查 `.git/HEAD` 存在（未覆盖 main 未合并），C-14 措辞与实际扫描范围不符；`PENDING_REASONS` 中对 C-7 的 pending 降级因该项判 PASS 而**从未触发**，故报告"pending 0"，而项目自身文档实际列有 5 项待补证 |

---

## 8. 结论

**VERDICT: PARTIAL**

- **题目 05 的业务实现质量高**：8 条规则中 6 条完全达标，R2/R5 因同一处并发缺陷降为 partial；
  **5 组必须演示的场景全部可操作、有持久化结果、无写死成功**，并发不超额度、重复不重复计量、
  失败/超时双终态、迟到结果只落诊断、停用后不受理、旧编号不触发新上游调用、
  降额下限、角色越权阻断——**均经我亲手复现验证通过**。模拟上游可控、DB 持久化（重启可查）、
  覆盖率与静态检查门禁达标、自编 Skill 真实可用、过程材料完整且**未发现伪造执行结果**。
- **未达标的实质缺口**：
  1. **D1**：降额与受理并发的 TOCTOU，可使占用超过日额度，且日终报表无法发现（已两次复现）；
  2. **D2**：业务代码从未合并到 `main`，无 PR/合并/合并后部署（共同要求 §8 未走完）；
  3. **D3**：Meegle 工作项核验实际失败，编号沿用规范示例（§9 未达成）。
- **PARTIAL 而非 FAIL 的理由**：§2 共同要求第 126 行列举的"整题不通过"情形中，
  "关键业务验收不通过"（D1 为并发边界下的局部不变量破坏，五组主场景均通过）、"覆盖率未达标"（未发生）、
  "缺失自编 Skill 或使用记录"（未发生）、"AI 过程无法追溯"（未发生）、"自动部署未成功"（本机部署真实成功，远程环境属环境受限）、
  "Git 流程不合规"（**确实发生**）、"缺少每日群提交或视频未讲清要求"（无法核验）。
  其中 **D2 单独即可使整题不通过**；其余项与 D1 使本次整体判定为 PARTIAL 而非 PASS。
- **建议补强顺序**：① 修复 D1（额度判定下沉到同一条 SQL / 事务互斥）并补并发回归用例 +
  在日终报表增加 `occupied ≤ quota_limit` 核对项；② 完成 `dev/05 → main` 的 PR 合并并在合并提交上重跑流水线门禁；
  ③ 在教师侧空间可见的前提下重新完成工作项核验，或如实标注"编号待核验"并保留；
  ④ 修正 `docs/09` 关于 C-14 修正方式的表述（白名单确实存在），并让 `acceptance-audit` 覆盖
  "main 是否已合并 / PR 是否存在 / 待补证项是否清零"三类检查，避免自检结论"pending 0"与自身文档矛盾。

---

## 附：本次验收的执行边界

- **未修改被验收项目任何源文件**；`git status --porcelain` 在验收前后仅新增本报告文件（`evidence/independent-acceptance-report.md`）。
- 未执行任何 `git commit` / `reset` / `checkout` 等改变仓库状态的操作；git 仅用于只读查询。
- 所有临时脚本与数据库均位于 `%TEMP%\mq-acceptance\`，验收用进程（服务 8090/8091、代理 9093/9094）已在验收结束后停止；
  项目自带部署实例（PID 39848/49004）**保持运行未动**。
- 说明：验收期间 `evidence/independent-test-report.md` 由**并发的另一验证会话**写入（`git status` 显示为未跟踪 `??`，
  文件时间 21:53:04）。该文件**不属于**被验收项目的交付内容，其内容（含预置口令明文）**未计入**本报告对项目的 C-16/D5 判定。
