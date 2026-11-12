# 独立质量验证报告（Independent Verification Report）

> **脱敏说明**：本报告为独立 Agent 的原始结论，为满足「证据中不得出现明文口令」的要求，
> 仅把文中引用的预置**演示**账号口令做了掩码（`ops-****` 等），
> 其余内容、结论与判定均未改动。完整值见 `cmd/server/main.go` 的 `seedData`（演示用固定口令，非真实密钥）。

- **被验证项目**：企业模型服务配额系统（Go 模块 `modelquota`）
- **项目路径**：`C:\Users\26765\Documents\trae_projects\skills\2组\素材\d5`
- **验证者角色**：独立测试 Agent（未参与开发，未采信任何自述结论，全部结论以本次实际执行结果为准）
- **验证时间**：2026-11-11 21:40 – 21:56 (GMT+8)
- **Go 工具链**：`go1.27.1 windows/amd64`；**Docker**：`29.9.3`（`golang:1.27` 镜像）
- **本次验证对项目的改动**：**无**。所有产物写入 `%TEMP%\quota_audit`；仅按要求写入本报告一个文件。验证结束后 `git status --porcelain` 为空（工作树干净）。

---

## 0. 结论速览

| 门禁项 | 结果 | 说明 |
|---|---|---|
| `gofmt -l .` | **PASS** | 无输出，退出码 0 |
| `go vet ./...` | **PASS** | 无告警，退出码 0 |
| `go test ./... -count=1` | **PASS** | 7/7 包 ok，退出码 0 |
| 覆盖率（整体 ≥85%） | **PASS** | **整体 89.2%**（1609/1804） |
| 覆盖率（5 个核心包各 ≥85%） | **PASS** | domain 97.1 / store 94.9 / service 87.2 / httpapi 87.5 / upstream 87.1 |
| 竞态检测 `-race` | **PASS** | Docker `golang:1.27` 容器内执行，退出码 0，0 处 DATA RACE |
| 生产代码被排除在覆盖率之外 | **未发现** | 12 个生产文件全部在册；唯一缺席的 `upstream.go` 为 0 语句纯声明文件 |
| 单元测试断言质量 | **良好** | 793 条断言中仅 97 条（12%）是状态码断言；关键业务规则分支基本全覆盖 |
| 接口层行为抽查 | **PASS** | 全部 40+ 项抽查符合预期，见 §4 |

**总判定：PASS**（无阻断级缺陷；存在 4 项低severity测试覆盖改进点，见 §6）

---

## 1. 执行的命令与退出码

所有 Go 命令均以 `go -C "<项目绝对路径>"` 执行（路径含中文「2组」）。

| # | 命令 | 退出码 | 关键输出 |
|---|---|---|---|
| 1 | `gofmt -l .` | **0** | 无输出（符合要求） |
| 2 | `go vet ./...` | **0** | 无告警 |
| 3 | `go test ./... -count=1` | **0** | `ok` ×7：`cmd/mockupstream 5.083s`、`cmd/server 0.879s`、`internal/domain 1.255s`、`internal/httpapi 2.912s`、`internal/service 6.043s`、`internal/store 3.470s`、`internal/upstream 5.578s` |
| 4 | `go test ./... -count=1 -coverprofile=%TEMP%\quota_audit\cov.out -covermode=atomic` | **0** | 见 §2 |
| 5 | `go tool cover -func=%TEMP%\quota_audit\cov.out` | **0** | `total: (statements) 89.2%`（180 个函数条目） |
| 6 | `python %TEMP%\quota_audit\cov_report.py <cov.out>`（自写解析脚本） | **0** | 见 §2 表格 |
| 7 | `docker run --rm -v "<项目绝对路径>:/work" -v quota-gomodcache:/go/pkg/mod -w /work -e GOPROXY=https://goproxy.cn,direct -e CGO_ENABLED=1 golang:1.27 go test ./... -count=1 -race` | **0** | 见 §3 |
| 8 | `git status --porcelain=v1`（验证前后各一次） | **0** | 两次均为空输出 → 项目未被本次验证改动 |

> 注：仓库当前工作树实际为**干净**状态（与任务简报中「有未提交内容」的说法不符）。已按要求**未执行**任何 git 提交/reset/checkout。

### 覆盖率解析脚本（自写，非复用 CI 脚本）

写入 `%TEMP%\quota_audit\cov_report.py`（临时目录，未进入项目目录）。该脚本**独立解析** `cov.out` 的 `file:start,end numstmt count` 记录，按**文件所在目录**（即完整包路径）聚合 covered/total，不依赖 CI 中 `replace("modelquota/internal/", "")` 的字符串替换逻辑，并对每个核心包与整体独立判定 ≥85% 阈值。解析结果与 `go tool cover -func` 的 `total: 89.2%` **完全一致**，互为交叉验证。

---

## 2. 覆盖率结果

### 2.1 整体与各包（语句覆盖率）

| 包 | covered | total | 覆盖率 | 阈值 85% | 判定 |
|---|---:|---:|---:|---|---|
| `modelquota/internal/domain` | 66 | 68 | **97.1%** | ≥85% | **PASS** |
| `modelquota/internal/store` | 369 | 389 | **94.9%** | ≥85% | **PASS** |
| `modelquota/internal/httpapi` | 621 | 710 | **87.5%** | ≥85% | **PASS** |
| `modelquota/internal/service` | 319 | 366 | **87.2%** | ≥85% | **PASS** |
| `modelquota/internal/upstream` | 74 | 85 | **87.1%** | ≥85% | **PASS** |
| **整体（含 cmd/*）** | **1609** | **1804** | **89.2%** | ≥85% | **PASS** |
| `modelquota/cmd/mockupstream`（非核心包） | 68 | 74 | 91.9% | — | — |
| `modelquota/cmd/server`（非核心包） | 92 | 112 | **82.1%** | — | 不适用（非核心包） |

**结论**：整体与 5 个核心业务包**全部满足 ≥85% 阈值**，覆盖率门禁 PASS。
`cmd/server` 为 82.1%，低于 85%，但其不属于题目定义的「核心业务包」集合，且拉低了整体值仍使整体保持 89.2%，因此**不构成门禁失败**。建议后续单独提升（见 §6-F5）。

### 2.2 存在未覆盖语句的文件（供改进参考）

| 文件 | covered/total | 未覆盖语句数 |
|---|---|---:|
| `internal/httpapi/pages.go` | 155/192 | 37 |
| `internal/service/service.go` | 256/297 | 41 |
| `internal/httpapi/handlers.go` | 77/107 | 30 |
| `internal/httpapi/server.go` | 309/330 | 21 |
| `cmd/server/main.go` | 92/112 | 20 |
| `internal/store/store.go` | 369/389 | 20 |
| `internal/upstream/httpclient.go` | 74/85 | 11 |
| `cmd/mockupstream/main.go` | 68/74 | 6 |
| `internal/service/auth.go` | 63/69 | 6 |
| `internal/domain/domain.go` | 66/68 | 2 |
| `internal/httpapi/decode.go` | 80/81 | 1 |

### 2.3 覆盖率口径澄清（重要，避免误读）

`go tool cover -func` 显示 `internal/domain/domain.go:394: CountOf 0.0%`。**这不是「该函数没被测过」**，而是 Go 覆盖率工具的**跨包不计量**特性：

- 不带 `-coverpkg` 时，`go test ./... -coverprofile` 中每个包的测试二进制**只插桩被测包自身**的语句；其他包的测试驱动本包代码时，这些语句**不计入**该包统计。
- 实测验证：`go test ./internal/httpapi/ -run TestOpsPage -coverpkg=./...` → `modelquota/internal/domain/domain.go:395.2,396.1 1 20`，即 `CountOf` 在 httpapi 测试中**被真实执行了 20 次**。
- 而 `go test ./internal/httpapi/ -coverprofile=...`（不带 `-coverpkg`）产出的 profile 中**只有** `decode.go/handlers.go/pages.go/server.go`，**完全没有 domain.go** —— 证明跨包语句在默认口径下不被统计。

结论：项目 CI 采用的**逐包覆盖率**是 Go 的标准口径，阈值判定有效；本报告 §2.1 的数字是**真实覆盖率的下界**。见 §6-F1。

---

## 3. 竞态检测（`-race`）

本机 Windows 无 C 编译器，无法直接执行 `-race`（`CGO_ENABLED=0` 默认关闭）。按要求改用官方容器执行：

```
docker run --rm \
  -v "C:\Users\26765\Documents\trae_projects\skills\2组\素材\d5:/work" \
  -v quota-gomodcache:/go/pkg/mod -w /work \
  -e GOPROXY=https://goproxy.cn,direct -e CGO_ENABLED=1 \
  golang:1.27 go test ./... -count=1 -race
```

- **退出码：0**
- 完整输出（全部 7 个包 ok）：

```
ok  	modelquota/cmd/mockupstream	1.135s
ok  	modelquota/cmd/server	2.175s
ok  	modelquota/internal/domain	1.021s
ok  	modelquota/internal/httpapi	10.691s
ok  	modelquota/internal/service	11.480s
ok  	modelquota/internal/store	4.594s
ok  	modelquota/internal/upstream	2.016s
```

- 日志中 `DATA RACE` 出现次数：**0**
- 日志中 `WARNING` 出现次数：**0**

**结论：竞态检测 PASS。** 容器内为 Linux + CGO 启用，与 CI（ubuntu-latest）环境一致，因此可代表 CI 的竞态结论。

> 补充说明：测试套件本身已包含针对并发竞态的**行为断言**（非仅靠 `-race` 兜底），见 §5 的 `TestInvokeConcurrentLastQuotaSlot`、`TestReserveInflightConcurrentLastSlot`、`TestInvokeConcurrentSameRequestNo`。

---

## 4. 接口层行为抽查（实测）

### 4.1 验证环境

- 8080/9091 上**已有**一套来自 `deploy/bin/` 的旧部署在运行（状态未知、不可控），故**未**复用它。
- 改为自行构建二进制到临时目录并在独立端口启动**全新数据库**的实例，确保状态确定、可复现：
  - 模拟上游 `127.0.0.1:19091`（`cmd/mockupstream`）
  - 服务实例 A `127.0.0.1:18080`（主流程）
  - 服务实例 B `127.0.0.1:18081`（迟到结果 / 跨凭证 / 跨日）
  - 服务实例 C `127.0.0.1:18082`（项目自带冒烟脚本）
- 预置账号**读自源码** `cmd/server/main.go` 的 `seedData`（未猜测）：
  - 运营：`ops` / `ops-****`（`Role: domain.AdminOps`，`TenantID: ""`）
  - 租户管理员甲：`admin-a` / `admin-a-****`（tenant-a）
  - 租户管理员乙：`admin-b` / `admin-b-****`（tenant-b）
  - 两个租户初始日额度均为 10。
- 客户端为 `curl 8.13.0`；所有请求体经文件传入（`--data-binary @file`）以规避 PowerShell 5.1 原生命令引号吞噬问题。
- 验证结束后已停止本人启动的全部临时实例；**原有 8080/9091 部署未被触碰**（复测仍返回 `{"status":"ok"}`）。

### 4.2 抽查结果

#### （1）认证与越权

| 抽查项 | 期望 | 实测状态码 | 响应要点 |
|---|---|---|---|
| 未认证 `GET /api/v1/ops/tenants` | 401 | **401** | `{"error":"身份认证失败"}` |
| 未认证 `GET /api/v1/admin/credentials` | 401 | **401** | 同上 |
| 未认证 `POST /api/v1/app/invoke` | 401 | **401** | 同上 |
| 未认证 `GET /api/v1/ops/report/daily` | 401 | **401** | 同上 |
| 租户管理员 → `GET /api/v1/ops/tenants` | 403 | **403** | `{"error":"无权访问该资源"}` |
| 租户管理员 → `POST /api/v1/ops/tenants/quota` | 403 | **403** | 同上 |
| 租户管理员 → `GET /api/v1/ops/report/daily` | 403 | **403** | 同上 |
| 应用凭证 → `GET /api/v1/admin/credentials` | 403 | **403** | 同上 |
| 应用凭证 → `GET /api/v1/ops/tenants` | 403 | **403** | 同上 |
| 应用凭证 → `GET /api/v1/admin/usage` | 403 | **403** | 同上 |
| 应用凭证 → `GET /api/v1/app/requests` | 200 | **200** | `{"requests":[]}`（仅自身范围） |
| 运营 → `POST /api/v1/admin/credentials/create` | 403 | **403** | 运营无租户归属，不可代建凭证 |
| 租户管理员甲 → `GET /api/v1/admin/usage?tenant_id=tenant-b` | 拒绝 | **400** | `{"error":"凭证所属租户由系统确认，不允许指定其他租户"}` |

#### （2）R1 凭证所属租户由系统确认

| 抽查项 | 期望 | 实测 |
|---|---|---|
| admin-a 创建凭证携带 `tenant_id=tenant-b` | 显式拒绝 | **400** `{"error":"凭证所属租户由系统确认，不允许指定其他租户"}` ✅ **被显式拒绝（非静默忽略）** |
| admin-a 创建凭证携带 `tenant_id=tenant-a`（自身） | 允许 | **200**，`tenant_id` 仍为 `tenant-a` |
| admin-a 创建凭证不携带 `tenant_id` | 允许 | **200**，`tenant_id` 由身份推导为 `tenant-a` |

#### （3）R2/R3 幂等、超时双终态

| 抽查项 | 实测 |
|---|---|
| `R-TO`（scenario=timeout）首次调用 | **200**，耗时 **2069ms**，`status="timeout"`，`error_reason="上游调用超过 2 秒，判为超时"`，`usage.success=0, in_flight=0` ✅ |
| `R-TO` 同编号**同参数**再次调用 | **200**，`replayed=true`，`request.id` 与首次**相同**，`replay_count=1`，耗时仅 **30ms**（远小于 2000ms 阈值 → 证明**未再次调用上游**），用量未增加 ✅ |
| `R-TO` 同编号**改参数** | **409** `{"error":"请求编号已被占用且参数不一致"}` ✅ |
| `R-FAIL`（scenario=fail） | **200**，`status="failed"`，`error_reason="上游明确失败"`，`success=0` ✅ |
| `R-OK`（默认 success） | **200**，`status="success"`，`upstream_result="模拟模型输出：ok-case"`，`success=1` ✅ |

#### （4）R2 额度耗尽 429 与增额后同编号重试

| 步骤 | 实测 |
|---|---|
| 运营将 tenant-a 额度 10→1 | **200**，`change.old_quota=10, new_quota=1, operator=ops, reason="shrink for test", effective_at=...` ✅ 变更留痕含操作人/原因/生效时间 |
| 调用前用量 | `{"quota_limit":1,"success":1,"in_flight":0,"occupied":1,"remaining":0}` |
| `R-429` 新调用 | **429** `{"error":"当日额度已用尽"}` ✅ |
| 429 之后用量 | `success=1` **未增加** ✅（被拒请求不占额度） |
| 运营增额 1→5 | **200**，留痕 `old_quota=1, new_quota=5, operator=ops, reason="raise for retry"` ✅ |
| `R-429` **同一编号**再次调用 | **200**，`status="success"`，`replayed=false`（新记录 `req_adf532958c440ef4`）✅ **增额后同一编号可再次调用成功** |

#### （5）R5 降额下限与变更原因

| 抽查项 | 实测 |
|---|---|
| 当日已成功 2 + 处理中 0 时降额到 0 | **400** `{"error":"降低额度不得低于当日已成功数量加处理中数量: 当日已成功 2 + 处理中 0 = 2"}` ✅ 报错信息给出具体口径 |
| 降额/调额未填原因 | **400** `{"error":"配额变更原因不能为空"}` ✅ |
| 增额到 99（带原因） | **200**，留痕 `old_quota=5, new_quota=99` ✅ |

#### （6）R6 停用后不再受理新调用

| 抽查项 | 实测 |
|---|---|
| 停用凭证 | **200** `{"status":"disabled"}` |
| 停用凭证 + **新编号**调用 | **409** `{"error":"凭证已停用，不再受理新调用"}` ✅ |
| 停用凭证 + **旧编号同参数**调用 | **200**，`replayed=true`，返回原记录，`replay_count=1` ✅ **未触发新的上游调用**（规则 6「停用凭证不能通过再次请求旧编号触发新的上游调用」成立） |
| 运营停用 tenant-b | **200** `{"status":"disabled","tenant_id":"tenant-b"}` |
| 停用租户后新调用 | **409** `{"error":"租户已停用，不再受理新调用"}` ✅ |
| 凭证数量上限 | 第 3 个凭证 **400** `{"error":"凭证数量已达上限: 每个租户最多 2 个凭证"}` ✅ |

#### （7）R7 普通应用仅可查询本凭证创建的请求

| 抽查项 | 实测 |
|---|---|
| 同租户**另一个**凭证读取他凭证创建的请求 | **403** `{"error":"无权访问该资源"}` ✅ |
| 跨租户凭证读取他租户请求 | **404** `{"error":"对象不存在"}` —— 因查询按租户范围限定，租户外不可见（不泄露存在性），符合安全预期 |
| 应用凭证 `GET /api/v1/app/requests` | **200**，只返回本凭证创建的请求（另一凭证返回 `{"requests":[]}`）✅ |
| 运营跨租户日报 `?tenant_id=tenant-b` | **200** ✅ |
| 运营跨租户日报**未指定** `tenant_id` | **400** `{"error":"运营跨租户核查必须指定 tenant_id"}` ✅ |

#### （8）R8 日终报表五类计数

`GET /api/v1/ops/report/daily?tenant_id=tenant-a` 实测返回：

```json
{"report":{"tenant_id":"tenant-a","day":"2026-11-11",
  "counts":{"failed":1,"processing":0,"rejected":3,"success":2,"timeout":1},
  "usage_success":2,"usage_in_flight":0,"quota_limit":99,"remaining":97,
  "quota_changes":[...4 条，含 operator/reason/effective_at...],
  "events":[{"kind":"credential_disabled","actor":"admin-a","reason":"leak","detail":"停用凭证 app-1",...}],
  "consistent":true}}
```

- ✅ **成功/失败/超时/处理中/被拒绝五类计数全部出现**，即使 `processing=0` 也存在（符合「即使为 0 也要出现」）。
- ✅ `consistent=true`，成功记录数(2) == 计入用量次数(2)。
- ✅ 每次额度变更记录操作人/原因/生效时间；停用事件记录 actor/reason/detail/时间。
- ✅ tenant-b 报表同样包含五类计数（`failed:0, processing:0, rejected:1, success:0, timeout:0`）与 `tenant_disabled` 事件。

#### （9）R4 迟到结果（真实上游链路，非测试钩子）

使用模拟上游 `slow_success` 场景（上游在 ~3s 后返回成功），平台 2s 判定超时：

| 时刻 | 实测 |
|---|---|
| 调用返回 | **200**，耗时 **2083ms**，`status="timeout"`，`diagnostics=[]`，`usage.success=0` |
| 立即查询诊断 | `{"diagnostics":[]}`（迟到结果尚未到达）✅ |
| 等待 3s 后查询诊断 | **200**，`{"diagnostics":[{"kind":"late_result","detail":"迟到结果（上游在超时后返回成功）：upstream_id=mock-3，耗时=3.003s",...}]}` ✅ |
| 迟到结果到达后再次查用量 | `success=0, in_flight=0` ✅ **未追加扣额** |
| 迟到结果到达后再查该请求 | `status` 仍为 **`timeout`** ✅ **未改变已返回的超时结论** |

#### （10）跨日编号复用（教学时钟 +48h）

| 抽查项 | 实测 |
|---|---|
| 时钟推进 +48h | `{"now":"2026-11-13T13:46:24Z","offset_seconds":172800}` |
| 新一天用**同编号同参数**调用 | **200**，`replayed=true`，返回**原记录**（`day` 仍为 `2026-11-11`）→ 不产生新调用、不重复计量 ✅ |
| 新一天用**同编号改参数**调用 | **409** `{"error":"请求编号已被占用且参数不一致"}` ✅ **跨日不允许改参数复用编号** |
| 新一天报表 | `{"day":"2026-11-13","counts":{"failed":0,"processing":0,"rejected":1,"success":0,"timeout":0},...}` |
| 原日报表 | `{"day":"2026-11-11","counts":{"failed":0,"processing":0,"rejected":0,"success":0,"timeout":1},...}` ✅ 按 UTC 自然日正确分日归属 |

#### （11）项目自带业务冒烟脚本（额外验证）

`scripts/smoke-test.sh` 在容器内对全新实例执行（`BASE_URL=http://host.docker.internal:18082`）：

```
=== 业务冒烟：http://host.docker.internal:18082 ===
[1] 健康检查 ... [2] 登录与会话 ... [3] 运营：租户与配额 ... [4] 客户管理员：凭证 ...
[5] 应用凭证：调用与计量 ... [6] 用量与日终核查 ... [7] 并发争抢最后一个额度名额 ...
[8] 停用后不再受理新调用
=== 冒烟结果：通过 39 项，失败 0 项 ===
```

**退出码 0，39/39 通过。**

---

## 5. 单元测试断言质量评估

### 5.1 量化指标

| 指标 | 数值 |
|---|---:|
| 顶层 `Test*` 函数 | **238** |
| 子测试（`t.Run`） | 2 |
| 断言调用（`t.Fatalf`/`t.Errorf`） | **793** |
| 其中仅检查 HTTP 状态码的断言 | **97（12.2%）** |
| 单文件断言密度最高 | `internal/store/store_test.go`：26 个测试 / 173 条断言 |

**约 88% 的断言检查的是业务语义（终态、计数、上游调用次数、错误类型、留痕字段、并发结果），而非仅看状态码或 nil。** 未发现「只检查 err == nil 就通过」的空壳断言。

### 5.2 逐项抽读结论

| 文件 | 评估 |
|---|---|
| `internal/domain/domain_test.go` | **优秀**。表驱动 + 精确数值断言。例：`TestValidateQuotaChangeFloorUsesSuccessPlusInflight` 不仅断言 `ErrQuotaFloor`，还断言错误信息中含成功数「6」与处理中数「2」；`TestBuildDailyReportListsAllStatuses` 断言五类终态键**必须存在**且计数为 0，并断言 `len(Counts) == len(StatusOrder)`；`TestBuildDailyReportDoesNotMutateInput` 额外验证不污染入参 map。 |
| `internal/service/invoke_test.go` | **优秀**。断言业务行为而非返回值非空。例：`TestInvokeSameRequestNoSameParamsIsReplay` 断言 `up.callCount() != 1`（**上游确未被再次调用**）、`Usage.Success` 未变、`ReplayCount == 1`；`TestInvokeTimeoutAndFailureAreDistinctTerminalStates` 断言失败与超时**是两个不同终态且计量口径一致**；`TestInvokeUpstreamLateResultOnlyRecordedAsDiagnostic` 断言迟到结果后终态仍为 timeout 且 `usage.Success == 0`。 |
| `internal/service/edge_test.go` | **优秀**。`TestInvokeConcurrentSameRequestNo`：6 goroutine 同编号并发，断言 `succeeded==1`、`replayed==5`、`failed==0`、`up.callCount()==1`、且**额度不泄漏**（`Success==1, InFlight==0`）。`TestSettleFailsWhenDatabaseBroken` 覆盖 DB 异常路径。 |
| `internal/store/store_test.go` | **优秀**。`TestReserveInflightConcurrentLastSlot`：16 goroutine 争抢 1 个名额，断言 `granted==1` 且 `usage.InFlight==1`。`TestCommitSuccessWithoutInflightFails` 断言计数异常时**报错而非静默通过**。断言大量使用精确值（`changes[0].NewQuota != 20`、`events[0].CredentialID != "c1"`）。 |
| `internal/httpapi/server_test.go` | **良好**。多数用例在断言状态码后继续断言响应体业务字段（如终态 `status`）。`TestAdminCreateCredentialRejectsCrossTenant`、`TestOpsCannotCreateCredential`、`TestAppCannotAccessAdminAPI` 等越权用例均独立断言 403。 |
| `internal/httpapi/paths_test.go` | **良好**。`TestStatusForMapsDomainErrors` 表驱动覆盖全部错误→状态码映射；`TestServiceAuthorizeTenantAccessRejectsOtherTenant` 直接断言 `ErrForbidden`。 |
| `internal/upstream/httpclient_test.go` | **优秀**。用真实 `httptest` 服务器而非 mock。`TestCompleteSuccess` 断言实际发往上游的路径/方法/Content-Type/请求体/参数类型；`TestCompleteTimeout` 断言**耗时**（`elapsed <= 2s`）；`TestCompleteMalformedResponse` 断言非法响应**不属于**失败/超时终态；`TestCompleteLateResultObservedAfterTimeout` 断言迟到结果的关联 ID 与内容，且 `TestCompleteNoLateResultWhenCallerCanceled` 断言调用方取消后**不应**上报迟到结果。 |

### 5.3 八条业务规则的关键分支覆盖矩阵

| 规则 | 关键分支 | 对应测试 | 覆盖 |
|---|---|---|---|
| R1 | 凭证租户由系统确认 / 不可跨租户 | `TestAdminCreateCredentialRejectsCrossTenant`、`TestOpsCannotCreateCredential`、`TestInvokeRejectsCredentialOfOtherTenant`、`TestAdminCannotDisableOtherTenantCredential` | ✅ |
| R2 | 同租户共享额度、UTC 自然日、处理中占位、并发不超发 | `TestInvokeQuotaSharedByAllCredentials`、`TestInvokeConcurrentLastQuotaSlot`、`TestReserveInflightConcurrentLastSlot`、`TestDayOfUsesUTCNaturalDay`、`TestOccupiedIncludesInflight` | ✅ |
| R3 | 幂等命中不重复调用/计量、改参数拒绝、跨日不复用 | `TestInvokeSameRequestNoSameParamsIsReplay`、`TestInvokeSameRequestNoDifferentParamsIsRejected`、`TestInvokeRequestNoNotReusableAcrossDays`、`TestInvokeConcurrentSameRequestNo` | ✅ |
| R4 | 失败/超时**双终态**、释放占用不计成功、迟到结果仅诊断 | `TestInvokeTimeoutAndFailureAreDistinctTerminalStates`、`TestInvokeUpstreamFailureReleasesQuota`、`TestInvokeUpstreamTimeoutReleasesQuota`、`TestInvokeUpstreamLateResultOnlyRecordedAsDiagnostic`、`TestCompleteLateResultObservedAfterTimeout` | ✅ |
| R5 | 降额下限 = 成功+处理中、变更留痕 | `TestValidateQuotaChangeFloorUsesSuccessPlusInflight`、`TestDailyReportIncludesQuotaChangesAndEvents`、`TestOpsAdjustQuota` | ✅ |
| R6 | 停用租户/凭证后不受理新调用、旧编号不触发新上游调用 | `TestInvokeRejectedAfterTenantDisabled`、`TestInvokeRejectedAfterCredentialDisabled`、`TestOpsDisableTenant`、`TestAdminDisableCredential` | ✅ |
| R7 | 运营/管理员/应用三级越权隔离 | `TestOpsOnlyRejectsTenantAdmin`、`TestAppCannotAccessAdminAPI`、`TestAdminCrossTenantQueryRejected`、`TestServiceAuthorizeTenantAccessRejectsOtherTenant` | ✅（**缺 1 个子分支，见 F2**） |
| R8 | 五类分列 + 一致性核对 + 变更/停用影响 | `TestDailyReportSplitsTerminalStates`、`TestBuildDailyReportListsAllStatuses`、`TestBuildDailyReportDetectsMismatch`、`TestDailyReportIncludesQuotaChangesAndEvents` | ✅（**处理中计数只被断言为 0，见 F6**） |

### 5.4 覆盖率统计是否排除了自研生产代码（专项核查）

| 核查项 | 方法 | 结果 |
|---|---|---|
| build tag 排除 | 全仓库 `grep "//go:build\|// +build\|coverpkg\|go:build ignore"` | **无匹配** ✅ |
| `-coverpkg` 排除 | 核查 CI 脚本与本次执行的全部 go 命令 | 均**未使用** `-coverpkg` 排除任何包 ✅ |
| `//go:build ignore` | 同上 grep | 无 ✅ |
| 生产文件是否全部在册 | 递归枚举 12 个非 `_test.go` 的 `.go` 文件，与 `cov.out` 中的文件集合逐一比对 | 12 个中 11 个在册；唯一缺席者 `internal/upstream/upstream.go` 经核查**只含接口、结构体与 2 个 `errors.New` 错误变量，0 个可插桩语句**（实测即使加 `-coverpkg=./...` 仍不出现在 profile 中）→ **无任何可执行逻辑被排除在统计外** ✅ |

**结论：未发现自研生产代码被排除在覆盖率统计之外。**

---

## 6. 发现的问题（按严重度排序）

> 以下均为**低severity**的测试覆盖/指标表达改进项，**不构成功能缺陷**，均已实测确认产品行为正确。

### F1 [低·指标表达] 逐包覆盖率口径不跨包，`CountOf` 在覆盖率报告中显示 0.0% 易被误读
- **触发条件**：阅读 `go tool cover -func` 或 CI 覆盖率报告时，看到 `internal/domain/domain.go:394: CountOf 0.0%`。
- **实测影响**：误判「R8 日终核查表的取值方法无测试覆盖」。**实际该函数被 httpapi 测试执行了 20 次**（`go test ./internal/httpapi/ -coverpkg=./...` → `domain.go:395.2,396.1 1 20`）。
- **根因**：Go 默认逐包插桩，跨包驱动不计���。项目 CI 的阈值判定本身**有效**（整体与核心包均达标），但报告数字是真实覆盖率的下界。
- **建议**：在覆盖率报告中标注口径说明，或在 CI 中额外输出一份 `-coverpkg=./...` 的参考数据（注意其分母会被多份测试二进制重复计入，不可直接当阈值）。
- **证据**：`go tool cover -func` 输出 `domain.go:394: CountOf 0.0%`；`go test ./internal/httpapi/ -coverprofile=...` 产出的 profile 仅含 `decode.go/handlers.go/pages.go/server.go`（无 domain.go）；加 `-coverpkg=./...` 后 `domain.go:395.2,396.1 1 20`。

### F2 [低·测试缺口] 应用凭证「跨凭证（同租户）越权读取请求」的 403 分支无测试覆盖
- **触发条件**：同租户凭证 B 查询由凭证 A 创建的请求。
- **实测影响**：**产品行为正确**（实测 **403** `{"error":"无权访问该资源"}`），但该分支无回归保护 —— 若将来有人误删 `internal/httpapi/handlers.go:214` 的凭证归属判断，测试**不会失败**。
- **根因**：测试夹具 `internal/httpapi/server_test.go` 的 `newTestServer` 只为 tenant-a 播种 **1 个**凭证（`cred-a1`），无法构造「同租户第二凭证读取第一凭证请求」。
- **证据**：覆盖率块 `modelquota/internal/httpapi/handlers.go:215.3,217.1 2 0`（计数 0）；`handlers.go:214.2,214.44 1 1`（条件被求值但分支体从未进入）。
- **建议**：在 `newTestServer` 中增加 tenant-a 的第二个凭证 `cred-a2`/`sk-a2`，补一条断言 403 的用例。

### F3 [低·测试缺口] `Invoke` 中「并发插入冲突且参数不一致」的子分支未覆盖
- **触发条件**：同一编号并发提交**不同参数**，且竞争到同一行插入。
- **实测影响**：`internal/service/service.go:473-476` 计数 0。该分支负责「并发抢占写入时归还额度 + 记录参数冲突拒绝」，当前无测试守护。
- **根因**：`internal/service/edge_test.go:118` 的 `TestInvokeConcurrentSameRequestNoDifferentParams` 是**串行**的，走的是更早的幂等检查（`service.go:393-397`），而非插入冲突恢复路径。
- **证据**：覆盖率零计数块 `service.go:473.5,476.1 3 0`。
- **建议**：改为并发提交不同参数，断言返回 `ErrRequestNoConflict` 且 `usage.InFlight` 归零（额度不泄漏）。

### F4 [低·测试缺口] `Invoke` 中「写入失败必须归还额度」的兜底路径未覆盖
- **触发条件**：`InsertRequest` 失败且**非**唯一约束冲突（例如 DB 不可用）。
- **实测影响**：`internal/service/service.go:494-497` 计数 0。额度归还失败会返回包装错误 —— 属正确设计，但无测试验证「额度确实被归还、不泄漏」。
- **证据**：覆盖率零计数块 `service.go:494.3,494.72 1 0`、`service.go:495.4,496.1 1 0`、`service.go:497.3,497.18 1 0`。
- **建议**：在插入前关闭 DB 或注入写失败，断言 `InFlight` 归零。

### F5 [低·覆盖率] `cmd/server` 语句覆盖率 82.1%，低于 85%
- **实测影响**：不违反门禁（`cmd/server` 不在「核心业务包」集合内，且整体仍为 89.2%）。但作为对外交付的启动入口，覆盖率低于其余各包。
- **证据**：`go tool cover -func`：`cmd/server/main.go:28 main 0.0%`、`main.go:50 run 78.3%`、`main.go:83 buildServer 75.8%`。
- **说明**：`main()` 0% 属正常（`os.Args` 入口），`run`/`buildServer` 的未覆盖部分多为信号处理与错误分支。

### F6 [信息·可观测性] 日终报表的「处理中」计数在测试中只被断言为 0
- **实测影响**：R8 要求列出处理中请求；当前**没有任何测试断言过 `counts[processing] > 0`**（在途请求可见性无回归保护）。实测接口确实返回该键（值为 0）。
- **证据**：`grep` 全量测试中 `RequestProcessing` 仅用于播种记录与断言 `== 0`；`internal/service/invoke_test.go:635` 断言 `report.Counts[domain.RequestProcessing] != 0` → `t.Fatalf`。
- **建议**：补一条「上游阻塞时并发查询报表，断言 processing ≥ 1」的用例。

### F7 [信息·行为特性，非缺陷] 教学时钟前跳会使既有会话立即失效
- **触发条件**：调用 `POST /api/v1/ops/clock` 把偏移设为 +48h 后，继续使用**跳转前**签发的令牌。
- **实测影响**：所有旧令牌立即返回 **401** `{"error":"身份认证失败"}`，需重新登录。原因是会话有效期按**偏移后的服务时钟**计算（`cmd/server` 恢复 `clock_offset_seconds` 偏移），+48h 跳变超过 12h 的 `SessionTTL`。
- **判定**：符合「会话 12 小时有效」的安全语义，**不是缺陷**；仅提示「跨日演示需重新登录」。
- **证据**：实测 `api_crossday.log` 第 64-71 行（401），重新登录后同一场景通过。

### F8 [信息] 仓库工作树实际为干净状态
- 任务简报称「git 仓库有未提交内容」，实测 `git status --porcelain=v1` 输出为空（退出码 0），当前分支 `dev/05`。
- 未执行任何 git 变更类操作。

---

## 7. 无法验证的项目（明确列出，不作猜测）

| # | 未验证项 | 原因 |
|---|---|---|
| 1 | **Windows 原生 `go test -race`** | 本机无 C 编译器，`CGO_ENABLED=0` 无法启用竞态检测。已用官方 `golang:1.27` 容器（Linux + CGO_ENABLED=1）完成等价验证；**Windows 平台特有的竞态行为未验证**（该项目 CI 仅在 ubuntu-latest 运行，故不影响 CI 结论） |
| 2 | **GitHub Actions 工作流的实际运行** | 无 CI 触发权限与凭据。已**本地逐条复现**了 CI 的全部步骤（gofmt/vet/test/coverage/race/smoke），但 CI runner 的真实执行未观测 |
| 3 | **`scripts/deploy-local.ps1` / `scripts/stop-local.ps1` / `scripts/scenario-verify.py` / `scripts/export_session.py`** | 这些脚本会改变 8080/9091 上**既有部署**的状态，属于超出「不修改环境」的边界，**未执行**。仅执行了只读的 `scripts/smoke-test.sh`（容器内、只读挂载） |
| 4 | **部署产物的运行形态**（`deploy/bin/` 下预编译 `server.exe`/`mockupstream.exe`） | 未验证其是否由当前源码构建、是否与源码一致。观测到的既有实例仅确认 `/healthz` 返回 `{"status":"ok"}`，其数据状态不可控，故全部接口抽查改用自建实例 |
| 5 | **数据库 schema 升级/迁移路径** | `migrations/` 目录**为空**（无任何文件），schema 由 `internal/store` 的 `db.Migrate()` 在启动时创建。**不存在**可供测试的「旧版本 schema 升级」路径，因此该场景无法验证 |
| 6 | **浏览器端 UI 交互与可访问性** | 仅验证了服务端返回的 HTML 内容与状态码（运营台日终核查表实测渲染出 tenant-a 行：成功 2/失败 1/超时 1/处理中 0/被拒绝 3/额度 99/剩余 97/一致性「一致」），**未**做浏览器端点击、表单、响应式与可访问性测试 |
| 7 | **真实（非模拟）上游服务集成** | 环境内仅有 `cmd/mockupstream` 模拟上游；未对接任何真实模型服务，故真实上游的限流、连接池、重试等行为未验证 |
| 8 | **性能、负载与长稳** | 未做压测或长时运行；`go test` 中的并发用例规模为 6~16 goroutine，不足以代表生产并发量 |
| 9 | **SQLite 并发写与 WAL 长期稳定性** | 仅通过 16 goroutine 争抢 1 个名额的用例间接验证原子性；未做长时间/高强度的并发写压测 |
| 10 | **`internal/httpapi/pages.go` 中未覆盖的 37 条语句** | 属页面渲染的异常/边界分支；未逐条构造场景验证其行为，仅记录为覆盖率缺口（§2.2） |

---

## 8. 附录：关键证据文件位置

| 内容 | 路径 |
|---|---|
| 本报告 | `evidence/independent-test-report.md` |
| 覆盖率 profile（本次生成） | `%TEMP%\quota_audit\cov.out` |
| 自写覆盖率解析脚本 | `%TEMP%\quota_audit\cov_report.py` |
| 竞态检测完整日志 | `%TEMP%\quota_audit\race.log` |
| 接口抽查主流程日志 | `%TEMP%\quota_audit\api_full.log` |
| 迟到结果 / 跨凭证 / 凭证上限日志 | `%TEMP%\quota_audit\api_late.log` |
| 跨日编号复用日志 | `%TEMP%\quota_audit\api_crossday.log` |
| 运营台渲染 HTML | `%TEMP%\quota_audit\ops_page.html` |

> 上述临时产物均在系统临时目录，未写入项目目录；本次验证对项目文件的唯一改动是本报告文件。

---

**验证结论：PASS** — 四项质量门禁（gofmt / vet / test / coverage）全绿，竞态检测在官方容器中全绿且无 DATA RACE，接口层 40+ 项抽查全部符合八条业务规则，未发现自研生产代码被排除在覆盖率统计之外。共记录 4 项低severity测试覆盖改进项（F1~F4）、1 项覆盖率改进项（F5）与 3 项信息性说明（F6~F8），均为可改进项而非功能缺陷。