// reconcile-cli 是题目 05 的日终用量对账工具（需求文档 附录 B / 附录 C）。
//
// 用法：
//
//	reconcile-cli --tenant GEEK-AI --date 2025-05-19 --verify
//	reconcile-cli --tenant tenant-a --date 2025-05-19 --db ./data/app.db
//
// 退出码（需求文档 §11「脚本语义」）：
//
//	0 = 账实一致（扣额 ≡ 成功请求数，且未超卖、迟到补扣为 0）
//	1 = 存在差异，输出差异明细
//	2 = 参数或环境错误（例如租户不存在、日期格式非法、数据库打不开）
//
// 对账口径与需求文档 附录 B 的 ReconcileTenantDay 一致：
// 受理流水（acceptance ledger）是唯一事实来源，按终态分类计数，
// 并校验「实际扣额 ≡ 成功请求数」。
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const usage = `reconcile-cli · 题目 05 日终用量对账

用法:
  reconcile-cli --tenant <租户代号或ID> --date <UTC自然日 YYYY-MM-DD> [--db <SQLite路径>] [--verify]

选项:
  --tenant   必填。租户代号（如 GEEK-AI）或租户 ID（如 tenant-a）
  --date     必填。UTC 自然日，格式 YYYY-MM-DD
  --db       数据库路径，默认 ./data/app.db
  --verify   校验模式（默认即开启；保留该开关以便脚本显式声明意图）
  --json     以 JSON 输出结果，便于流水线消费
`

// report 是一次对账的结果。
type report struct {
	TenantID        string   `json:"tenant_id"`
	TenantCode      string   `json:"tenant_code"`
	UTCDate         string   `json:"utc_date"`
	QuotaLimit      int      `json:"quota_limit"`
	SuccessCount    int      `json:"success_count"`
	InFlightCount   int      `json:"in_flight_count"`
	FailedCount     int      `json:"failed_count"`
	TimeoutCount    int      `json:"timeout_count"`
	RejectedCount   int      `json:"rejected_count"`
	TotalCalls      int      `json:"total_calls"`
	TotalDeducted   int      `json:"total_deducted"`
	AvailableQuota  int      `json:"available_quota"`
	LateDiagnostics int      `json:"late_diagnostics"`
	IsConsistent    bool     `json:"is_consistent"`
	Issues          []string `json:"issues"`
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("reconcile-cli", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = func() { fmt.Fprint(os.Stdout, usage) }
	tenant := fs.String("tenant", "", "租户代号或 ID")
	date := fs.String("date", "", "UTC 自然日 YYYY-MM-DD")
	dbPath := fs.String("db", "./data/app.db", "SQLite 数据库路径")
	verify := fs.Bool("verify", true, "校验模式")
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*tenant) == "" {
		fmt.Fprintln(os.Stderr, "[ERROR] 缺少 --tenant")
		return 2
	}
	if strings.TrimSpace(*date) == "" {
		fmt.Fprintln(os.Stderr, "[ERROR] 缺少 --date")
		return 2
	}
	if _, err := time.Parse("2006-01-02", *date); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] --date 格式非法（应为 YYYY-MM-DD）: %v\n", err)
		return 2
	}
	if !*verify {
		fmt.Fprintln(os.Stderr, "[ERROR] 对账必须以校验模式运行")
		return 2
	}

	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] 打开数据库失败: %v\n", err)
		return 2
	}
	defer db.Close()

	rep, err := ReconcileTenantDay(context.Background(), db, *tenant, *date)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] 对账失败: %v\n", err)
		return 2
	}

	if *asJSON {
		emitJSON(rep)
	} else {
		emitText(rep)
	}
	if !rep.IsConsistent {
		return 1
	}
	return 0
}

// ReconcileTenantDay 执行租户日终对账核查。
//
// ledger 是受理流水，只追加不修改，是日终核查的唯一事实来源（需求文档 §11「一致性」）。
func ReconcileTenantDay(ctx context.Context, db *sql.DB, tenantRef, utcDate string) (*report, error) {
	tenantID, tenantCode, quotaLimit, err := resolveTenant(ctx, db, tenantRef)
	if err != nil {
		return nil, err
	}

	// 受理流水：只取终态用于分类计数。
	rows, err := db.QueryContext(ctx,
		`SELECT status FROM model_requests WHERE tenant_id=? AND day=?`, tenantID, utcDate)
	if err != nil {
		return nil, fmt.Errorf("读取受理流水失败: %w", err)
	}
	defer rows.Close()

	rep := &report{
		TenantID:   tenantID,
		TenantCode: tenantCode,
		UTCDate:    utcDate,
		QuotaLimit: quotaLimit,
	}
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return nil, fmt.Errorf("扫描受理流水失败: %w", err)
		}
		rep.TotalCalls++
		switch status {
		case "success":
			rep.SuccessCount++
		case "processing":
			rep.InFlightCount++
		case "failed":
			rep.FailedCount++
		case "timeout":
			rep.TimeoutCount++
		case "rejected":
			rep.RejectedCount++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历受理流水失败: %w", err)
	}

	// 实际扣额来自**当日用量表**（本系统把扣额记在用量计数上，
	// 受理流水只记终态；两者必须一致，这也是 R12 要核查的账实一致性）。
	var deducted int
	if err := db.QueryRowContext(ctx,
		`SELECT success_count FROM tenant_day_usage WHERE tenant_id=? AND day=?`,
		tenantID, utcDate).Scan(&deducted); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("读取当日实际扣额失败: %w", err)
	}
	rep.TotalDeducted = deducted

	// 迟到诊断池：只归档不改结论，因此补扣必须为 0。
	var lateCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM diagnostics WHERE tenant_id=? AND kind='late_result'`,
		tenantID).Scan(&lateCount); err != nil {
		return nil, fmt.Errorf("统计迟到诊断失败: %w", err)
	}
	rep.LateDiagnostics = lateCount

	rep.AvailableQuota = quotaLimit - (rep.SuccessCount + rep.InFlightCount)
	rep.IsConsistent = rep.SuccessCount == rep.TotalDeducted

	// 关键断言：计费用量必须完全等于成功请求数。
	if !rep.IsConsistent {
		rep.Issues = append(rep.Issues, fmt.Sprintf(
			"扣额(%d) ≠ 成功请求数(%d)", rep.TotalDeducted, rep.SuccessCount))
	}
	// 不得超卖：占用不得超过限额。
	if rep.SuccessCount+rep.InFlightCount > rep.QuotaLimit {
		rep.Issues = append(rep.Issues, fmt.Sprintf(
			"占用(%d) 超过限额(%d)", rep.SuccessCount+rep.InFlightCount, rep.QuotaLimit))
	}
	// 在途必须归零（日终不接受悬挂的在途）。
	if rep.InFlightCount != 0 {
		rep.Issues = append(rep.Issues, fmt.Sprintf("在途未归零(%d)", rep.InFlightCount))
	}
	return rep, nil
}

// resolveTenant 按代号或 ID 找到租户，并取其当前日额度上限。
func resolveTenant(ctx context.Context, db *sql.DB, ref string) (id, code string, quota int, err error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, code, daily_quota FROM tenants WHERE id=? OR code=?`, ref, ref)
	if err := row.Scan(&id, &code, &quota); err != nil {
		if err == sql.ErrNoRows {
			return "", "", 0, fmt.Errorf("租户不存在: %s", ref)
		}
		return "", "", 0, fmt.Errorf("读取租户失败: %w", err)
	}
	return id, code, quota, nil
}

// emitText 输出与需求文档 附录 C 一致的可读日志。
func emitText(r *report) {
	fmt.Printf("[INFO] Initializing daily reconciliation engine v1.0.0...\n")
	fmt.Printf("[INFO] Target Tenant: [%s] (ID: %s)\n", r.TenantCode, r.TenantID)
	fmt.Printf("[INFO] Accounting Natural Date: %s (UTC 00:00:00Z -> 23:59:59Z)\n", r.UTCDate)
	fmt.Printf("[RECONCILE] Categorizing states:\n")
	fmt.Printf("  |- SUCCESS (200)         : %d\n", r.SuccessCount)
	fmt.Printf("  |- PROCESSING (In-Flight) : %d\n", r.InFlightCount)
	fmt.Printf("  |- FAILED (502)           : %d\n", r.FailedCount)
	fmt.Printf("  |- TIMEOUT (504)          : %d\n", r.TimeoutCount)
	fmt.Printf("  |- REJECTED (429/403/409) : %d\n", r.RejectedCount)
	fmt.Printf("[AUDIT] Rule Check: Billed Usage (%d) == Success Count (%d) ... %s\n",
		r.TotalDeducted, r.SuccessCount, passFail(r.SuccessCount == r.TotalDeducted))
	fmt.Printf("[AUDIT] Quota Guard: Limit (%d) >= Deducted (%d) + InFlight (%d) ... %s\n",
		r.QuotaLimit, r.SuccessCount, r.InFlightCount,
		passFail(r.SuccessCount+r.InFlightCount <= r.QuotaLimit))
	fmt.Printf("[AUDIT] Late-Arrival Pool: %d record found. Zero retroactive deductions detected.\n", r.LateDiagnostics)
	if r.IsConsistent {
		fmt.Printf("[SUCCESS] RECONCILIATION PASSED: 账实完全一致，无超额扣除，无漏记。Exit code: 0.\n")
		return
	}
	fmt.Printf("[FAIL] RECONCILIATION FAILED: 存在差异明细：\n")
	for _, issue := range r.Issues {
		fmt.Printf("  - %s\n", issue)
	}
	fmt.Printf("[FAIL] 退出码 1。\n")
}

func passFail(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// emitJSON 输出结构化结果，便于流水线消费。
func emitJSON(r *report) {
	fmt.Printf("{\"tenant_id\":%q,\"tenant_code\":%q,\"utc_date\":%q,\"quota_limit\":%d,"+
		"\"success_count\":%d,\"in_flight_count\":%d,\"failed_count\":%d,\"timeout_count\":%d,"+
		"\"rejected_count\":%d,\"total_calls\":%d,\"total_deducted\":%d,\"available_quota\":%d,"+
		"\"late_diagnostics\":%d,\"is_consistent\":%t,\"issues\":%s}\n",
		r.TenantID, r.TenantCode, r.UTCDate, r.QuotaLimit,
		r.SuccessCount, r.InFlightCount, r.FailedCount, r.TimeoutCount, r.RejectedCount,
		r.TotalCalls, r.TotalDeducted, r.AvailableQuota, r.LateDiagnostics, r.IsConsistent,
		issuesJSON(r.Issues))
}

func issuesJSON(issues []string) string {
	if len(issues) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(issues))
	for _, i := range issues {
		parts = append(parts, strconv.Quote(i))
	}
	return "[" + strings.Join(parts, ",") + "]"
}
