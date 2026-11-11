package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// ---------- Usage 计量口径（R2/R4/R5） ----------

func TestUsageRemainingAndOccupied(t *testing.T) {
	cases := []struct {
		name      string
		usage     Usage
		remaining int
		occupied  int
	}{
		{"全部剩余", Usage{QuotaLimit: 10}, 10, 0},
		{"有成功", Usage{QuotaLimit: 10, Success: 3}, 7, 3},
		{"有处理中", Usage{QuotaLimit: 10, Success: 3, InFlight: 2}, 5, 5},
		{"额度用尽", Usage{QuotaLimit: 10, Success: 10}, 0, 10},
		{"超额时不出现负数", Usage{QuotaLimit: 10, Success: 12}, 0, 12},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.usage.Remaining(); got != c.remaining {
				t.Fatalf("Remaining() = %d, 期望 %d", got, c.remaining)
			}
			if got := c.usage.Occupied(); got != c.occupied {
				t.Fatalf("Occupied() = %d, 期望 %d", got, c.occupied)
			}
		})
	}
}

// TestOccupiedIncludesInflight 验证处理中计入占用（R2：处理中先占额度）。
func TestOccupiedIncludesInflight(t *testing.T) {
	u := Usage{QuotaLimit: 10, Success: 1, InFlight: 4}
	if u.Occupied() != 5 {
		t.Fatalf("Occupied() = %d, 期望 5", u.Occupied())
	}
	if u.Remaining() != 5 {
		t.Fatalf("Remaining() = %d, 期望 5", u.Remaining())
	}
}

// ---------- DayOf 自然日归属（R2） ----------

func TestDayOfUsesUTCNaturalDay(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	// 本地时间 2026-03-01 00:30（UTC+8）对应 UTC 2026-02-28 16:30。
	local := time.Date(2026, 3, 1, 0, 30, 0, 0, loc)
	if got := DayOf(local); got != "2026-02-28" {
		t.Fatalf("DayOf() = %s, 期望 2026-02-28", got)
	}
	utc := time.Date(2026, 3, 1, 0, 30, 0, 0, time.UTC)
	if got := DayOf(utc); got != "2026-03-01" {
		t.Fatalf("DayOf() = %s, 期望 2026-03-01", got)
	}
}

// ---------- 请求编号校验（R3） ----------

func TestValidateRequestNo(t *testing.T) {
	cases := []struct {
		name string
		no   string
		want error
	}{
		{"正常编号", "req-001", nil},
		{"下划线", "req_001_a", nil},
		{"64 位", strings.Repeat("a", 64), nil},
		{"空串", "", ErrRequestNoRequired},
		{"纯空格", "   ", ErrRequestNoRequired},
		{"含中文", "请求-1", ErrRequestNoFormat},
		{"含斜杠", "a/b", ErrRequestNoFormat},
		{"超长", strings.Repeat("a", 65), ErrRequestNoFormat},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateRequestNo(c.no)
			if c.want == nil {
				if err != nil {
					t.Fatalf("期望通过，实际报错: %v", err)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("期望 %v，实际 %v", c.want, err)
			}
		})
	}
}

// ---------- 参数指纹（R3 幂等判定） ----------

func TestParamsFingerprintStableAcrossKeyOrder(t *testing.T) {
	a, err := ParamsFingerprint("m", "p", map[string]any{"x": 1, "y": "2"})
	if err != nil {
		t.Fatalf("计算指纹失败: %v", err)
	}
	b, err := ParamsFingerprint("m", "p", map[string]any{"y": "2", "x": 1})
	if err != nil {
		t.Fatalf("计算指纹失败: %v", err)
	}
	if a != b {
		t.Fatalf("参数顺序不同应得到同一指纹：%s != %s", a, b)
	}
}

func TestParamsFingerprintIgnoresNilValues(t *testing.T) {
	withNil, err := ParamsFingerprint("m", "p", map[string]any{"x": 1, "y": nil})
	if err != nil {
		t.Fatalf("计算指纹失败: %v", err)
	}
	without, err := ParamsFingerprint("m", "p", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("计算指纹失败: %v", err)
	}
	if withNil != without {
		t.Fatalf("nil 参数应被剔除，指纹应相同")
	}
}

func TestParamsFingerprintDiffersOnDifferentParams(t *testing.T) {
	a, _ := ParamsFingerprint("m", "p", map[string]any{"x": 1})
	b, _ := ParamsFingerprint("m", "p", map[string]any{"x": 2})
	c, _ := ParamsFingerprint("m2", "p", map[string]any{"x": 1})
	d, _ := ParamsFingerprint("m", "p2", map[string]any{"x": 1})
	if a == b || a == c || a == d {
		t.Fatalf("不同参数应得到不同指纹")
	}
}

func TestParamsFingerprintRejectsUnserializableParams(t *testing.T) {
	if _, err := ParamsFingerprint("m", "p", map[string]any{"ch": make(chan int)}); err == nil {
		t.Fatalf("不可序列化的参数应报错")
	}
}

// ---------- 口令摘要 ----------

func TestHashSecretIsDeterministicAndSalted(t *testing.T) {
	a := HashSecret("ops-secret")
	if a != HashSecret("ops-secret") {
		t.Fatalf("相同口令应得到相同摘要")
	}
	if a == HashSecret("other-secret") {
		t.Fatalf("不同口令不应得到相同摘要")
	}
	if a == "ops-secret" {
		t.Fatalf("摘要不应等于原文")
	}
	if len(a) != 64 {
		t.Fatalf("摘要长度 = %d, 期望 64", len(a))
	}
}

// ---------- 额度校验（R5） ----------

func TestValidateQuota(t *testing.T) {
	if err := ValidateQuota(0); err != nil {
		t.Fatalf("额度 0 应合法: %v", err)
	}
	if err := ValidateQuota(MaxQuota); err != nil {
		t.Fatalf("额度 %d 应合法: %v", MaxQuota, err)
	}
	if err := ValidateQuota(-1); !errors.Is(err, ErrQuotaRange) {
		t.Fatalf("负额度应报 ErrQuotaRange，实际 %v", err)
	}
	if err := ValidateQuota(MaxQuota + 1); !errors.Is(err, ErrQuotaRange) {
		t.Fatalf("超额应报 ErrQuotaRange，实际 %v", err)
	}
}

func TestValidateQuotaChangeFloorUsesSuccessPlusInflight(t *testing.T) {
	current := Usage{QuotaLimit: 10, Success: 6, InFlight: 2}
	// 下限 = 成功 6 + 处理中 2 = 8
	if err := ValidateQuotaChange(8, current); err != nil {
		t.Fatalf("额度等于下限应允许: %v", err)
	}
	err := ValidateQuotaChange(7, current)
	if !errors.Is(err, ErrQuotaFloor) {
		t.Fatalf("低于下限应报 ErrQuotaFloor，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "6") || !strings.Contains(err.Error(), "2") {
		t.Fatalf("错误信息应说明成功数与处理中数，实际 %q", err.Error())
	}
}

func TestValidateQuotaChangeRejectsOutOfRange(t *testing.T) {
	if err := ValidateQuotaChange(MaxQuota+1, Usage{}); !errors.Is(err, ErrQuotaRange) {
		t.Fatalf("应先校验取值范围，实际 %v", err)
	}
}

// ---------- 日终报表（R8） ----------

func TestBuildDailyReportConsistent(t *testing.T) {
	counts := map[RequestStatus]int{RequestSuccess: 3, RequestFailed: 1, RequestTimeout: 1}
	usage := Usage{TenantID: "t", Day: "2026-01-01", QuotaLimit: 10, Success: 3}
	report := BuildDailyReport("t", "2026-01-01", counts, usage, nil, nil)
	if !report.Consistent {
		t.Fatalf("成功记录数与用量次数一致时 Consistent 应为 true")
	}
	if report.MismatchNote != "" {
		t.Fatalf("一致时不应有差异说明，实际 %q", report.MismatchNote)
	}
	if report.Remaining != 7 {
		t.Fatalf("Remaining = %d, 期望 7", report.Remaining)
	}
	if report.Counts[RequestSuccess] != 3 {
		t.Fatalf("成功计数 = %d, 期望 3", report.Counts[RequestSuccess])
	}
}

func TestBuildDailyReportDetectsMismatch(t *testing.T) {
	counts := map[RequestStatus]int{RequestSuccess: 2}
	usage := Usage{TenantID: "t", Day: "2026-01-01", QuotaLimit: 10, Success: 5}
	report := BuildDailyReport("t", "2026-01-01", counts, usage, nil, nil)
	if report.Consistent {
		t.Fatalf("成功记录数 2 与用量次数 5 不一致，Consistent 应为 false")
	}
	if report.MismatchNote == "" {
		t.Fatalf("不一致时应给出差异说明")
	}
}

// ---------- 状态排序 ----------

func TestSortStatusesKeepsStableOrder(t *testing.T) {
	got := SortStatuses([]RequestStatus{RequestRejected, RequestSuccess, RequestTimeout, RequestFailed, RequestProcessing})
	want := []RequestStatus{RequestSuccess, RequestFailed, RequestTimeout, RequestProcessing, RequestRejected}
	if len(got) != len(want) {
		t.Fatalf("长度 = %d, 期望 %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 项 = %s, 期望 %s", i, got[i], want[i])
		}
	}
}

func TestSortStatusesDeduplicates(t *testing.T) {
	got := SortStatuses([]RequestStatus{RequestSuccess, RequestSuccess, RequestFailed})
	if len(got) != 2 {
		t.Fatalf("重复状态应去重，长度 = %d, 期望 2", len(got))
	}
}

func TestSortStatusesKeepsUnknownStatus(t *testing.T) {
	unknown := RequestStatus("unknown")
	got := SortStatuses([]RequestStatus{unknown, RequestSuccess})
	if len(got) != 2 {
		t.Fatalf("未知状态不应被丢弃，长度 = %d", len(got))
	}
	if got[1] != unknown {
		t.Fatalf("未知状态应排在已知状态之后，实际 %v", got)
	}
}

// ---------- 常量约束 ----------

func TestMaxQuotaAndTimeout(t *testing.T) {
	if MaxQuota != 100000 {
		t.Fatalf("MaxQuota = %d, 期望 100000", MaxQuota)
	}
	if UpstreamTimeout != 2*time.Second {
		t.Fatalf("UpstreamTimeout = %v, 期望 2s", UpstreamTimeout)
	}
}
