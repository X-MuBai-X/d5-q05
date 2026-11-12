# quota-guard 计量口径守卫报告

- 仓库：C:\Users\26765\Documents\trae_projects\skills\2组\素材\d5
- 判定：PASS
- 核心包覆盖率（语句口径）：domain 97.3% / store 94.4% / service 87.7%（下限 85%）

## 静态不变量

| # | 检查项 | 规则 | 结论 | 证据 |
|---|---|---|---|---|
| 1 | 额度占用为单条条件 UPDATE，且额度上限在本条语句内实时读取 | R2/D1 | PASS | `internal/store/store.go:556` |
| 2 | 占用额度前确保当日用量计数行存在 | R2 | PASS | `internal/store/store.go:552` |
| 3 | 成功转换：处理中 −1、成功 +1，且带 inflight_count > 0 守卫 | R2/R4 | PASS | `internal/store/store.go:596` |
| 4 | 失败/超时释放占用：处理中 −1，且带 inflight_count > 0 守卫 | R4 | PASS | `internal/store/store.go:596` |
| 5 | 占用口径 Occupied() = 成功数 + 处理中数 | R2/R5 | PASS | `internal/domain/domain.go:287` |
| 6 | 降额下限以当日成功数 + 处理中数为准 | R5 | PASS | `internal/domain/domain.go:351` |
| 7 | 终态结算具备成功/超时/失败三条独立分支 | R4/Q1 | PASS | `internal/service/service.go:559` |
| 8 | 租户/凭证状态校验早于额度占用（停用后不占额度） | R6 | PASS | `internal/service/service.go:443/467` |
| 9 | 降额下限判定与写入在同一条语句内完成（防 TOCTOU） | R5/D1 | PASS | `internal/store/store.go:576` |
| 10 | 日终报表能识别「占用超过额度」并标记不一致 | R8/D1 | PASS | `internal/domain/domain.go:390` |

## 行为回归

- 命令：`go test ./internal/domain/... ./internal/store/... ./internal/service/... -run Quota|Usage|Invoke|Settle|Reserve|Inflight|Idempot|Reject -count=1`
- 结果：PASS

```
ok  	modelquota/internal/domain	2.129s
ok  	modelquota/internal/store	3.972s
ok  	modelquota/internal/service	6.724s
```

## 失败项

无
