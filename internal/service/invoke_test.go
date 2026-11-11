package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"modelquota/internal/domain"
	"modelquota/internal/upstream"
)

// ---------- 成功路径（R2/R4） ----------

func TestInvokeSuccessCountsOneQuota(t *testing.T) {
	svc, up, _ := newTestService(t)
	ctx := context.Background()

	res, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1", InvokeInput{
		RequestNo: "req-1", Model: "text-1", Prompt: "你好",
	})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if res.Request.Status != domain.RequestSuccess {
		t.Fatalf("终态 = %s, 期望 success", res.Request.Status)
	}
	if res.Request.UpstreamResult != "模拟输出" {
		t.Fatalf("上游结果 = %s", res.Request.UpstreamResult)
	}
	if res.Usage.Success != 1 || res.Usage.InFlight != 0 {
		t.Fatalf("用量不符: %+v", res.Usage)
	}
	if res.Usage.Remaining() != 9 {
		t.Fatalf("剩余额度 = %d, 期望 9", res.Usage.Remaining())
	}
	if up.callCount() != 1 {
		t.Fatalf("上游调用次数 = %d, 期望 1", up.callCount())
	}
	if res.Replayed {
		t.Fatalf("首次调用不应是幂等命中")
	}
}

func TestInvokePassesParamsToUpstream(t *testing.T) {
	svc, up, _ := newTestService(t)
	_, err := svc.Invoke(context.Background(), "app", testTenantA, "cred-a1", InvokeInput{
		RequestNo: "req-1", Model: "text-1", Prompt: "你好", Params: map[string]any{"temperature": 0.7},
	})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if up.last.Model != "text-1" || up.last.Prompt != "你好" {
		t.Fatalf("上游请求参数不符: %+v", up.last)
	}
	if up.last.Params["temperature"] != 0.7 {
		t.Fatalf("上游请求参数缺少 temperature: %+v", up.last.Params)
	}
}

// ---------- 幂等（R3） ----------

func TestInvokeSameRequestNoSameParamsIsReplay(t *testing.T) {
	svc, up, _ := newTestService(t)
	ctx := context.Background()
	in := InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"}

	first, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1", in)
	if err != nil {
		t.Fatalf("首次调用失败: %v", err)
	}
	second, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1", in)
	if err != nil {
		t.Fatalf("幂等重放失败: %v", err)
	}
	if !second.Replayed {
		t.Fatalf("相同编号相同参数应为幂等命中")
	}
	if second.Request.ID != first.Request.ID {
		t.Fatalf("幂等命中应返回首次的记录")
	}
	if second.Usage.Success != 1 {
		t.Fatalf("幂等命中不应重复计数，成功量 = %d", second.Usage.Success)
	}
	if up.callCount() != 1 {
		t.Fatalf("幂等命中不应再次调用上游，调用次数 = %d", up.callCount())
	}
	if second.Request.ReplayCount != 1 {
		t.Fatalf("幂等命中次数 = %d, 期望 1", second.Request.ReplayCount)
	}
}

func TestInvokeSameRequestNoDifferentParamsIsRejected(t *testing.T) {
	svc, up, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"}); err != nil {
		t.Fatalf("首次调用失败: %v", err)
	}
	_, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "不同的提示词"})
	if !errors.Is(err, domain.ErrRequestNoConflict) {
		t.Fatalf("期望 ErrRequestNoConflict，实际 %v", err)
	}
	if up.callCount() != 1 {
		t.Fatalf("参数冲突不应调用上游，调用次数 = %d", up.callCount())
	}
	// 被拒绝的尝试应单独留痕，且不占用请求编号。
	rejected, err := svc.RejectedAttempts(ctx, testTenantA, testDay)
	if err != nil {
		t.Fatalf("查询被拒绝尝试失败: %v", err)
	}
	if len(rejected) != 1 || rejected[0].Reason == "" {
		t.Fatalf("参数冲突应留痕: %+v", rejected)
	}
}

func TestInvokeRequestNoMustBePresentAndValid(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: ""}); !errors.Is(err, domain.ErrRequestNoRequired) {
		t.Fatalf("期望 ErrRequestNoRequired，实际 %v", err)
	}
	if _, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "非法编号"}); !errors.Is(err, domain.ErrRequestNoFormat) {
		t.Fatalf("期望 ErrRequestNoFormat，实际 %v", err)
	}

	rejected, err := svc.RejectedAttempts(ctx, testTenantA, testDay)
	if err != nil {
		t.Fatalf("查询被拒绝尝试失败: %v", err)
	}
	if len(rejected) != 2 {
		t.Fatalf("被拒绝尝试数 = %d, 期望 2", len(rejected))
	}
}

// TestInvokeRequestNoNotReusableAcrossDays 验证请求编号跨日不复用。
//
// 题面要求编号「全局唯一」：跨天后同一编号改用不同参数必须拒绝，
// 参数相同则仍视为同一次调用的重放。
func TestInvokeRequestNoNotReusableAcrossDays(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"}); err != nil {
		t.Fatalf("首次调用失败: %v", err)
	}

	// 把时钟推到第二天。
	svc.SetClockOffset(24 * time.Hour)
	if svc.CurrentDay() == testDay {
		t.Fatalf("教学时钟推进后当前日期应变化")
	}

	_, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "新的提示词"})
	if !errors.Is(err, domain.ErrRequestNoConflict) {
		t.Fatalf("跨日复用编号应被拒绝，实际 %v", err)
	}
}

// ---------- 额度（R2） ----------

func TestInvokeRejectsWhenQuotaExhausted(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	if err := db.UpdateTenantQuota(ctx, testTenantA, 1); err != nil {
		t.Fatalf("调整额度失败: %v", err)
	}

	if _, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"}); err != nil {
		t.Fatalf("首次调用应成功: %v", err)
	}
	_, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-2", Model: "m", Prompt: "p"})
	if !errors.Is(err, domain.ErrQuotaExceeded) {
		t.Fatalf("期望 ErrQuotaExceeded，实际 %v", err)
	}

	usage, _ := svc.TenantUsage(ctx, testTenantA, testDay)
	if usage.Success != 1 || usage.InFlight != 0 {
		t.Fatalf("被拒绝的请求不应影响用量: %+v", usage)
	}
}

// TestInvokeRetrySameRequestNoAfterQuotaIncrease 验证增额后可用同一编号重试（确认结论 Q7）。
func TestInvokeRetrySameRequestNoAfterQuotaIncrease(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	if err := db.UpdateTenantQuota(ctx, testTenantA, 0); err != nil {
		t.Fatalf("调整额度失败: %v", err)
	}

	_, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"})
	if !errors.Is(err, domain.ErrQuotaExceeded) {
		t.Fatalf("期望 ErrQuotaExceeded，实际 %v", err)
	}

	if _, err := svc.AdjustQuota(ctx, "ops", testTenantA, 5, "客户扩容"); err != nil {
		t.Fatalf("调整额度失败: %v", err)
	}
	res, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"})
	if err != nil {
		t.Fatalf("增额后用同一编号重试应成功: %v", err)
	}
	if res.Request.Status != domain.RequestSuccess {
		t.Fatalf("重试终态 = %s, 期望 success", res.Request.Status)
	}
	if res.Usage.Success != 1 {
		t.Fatalf("重试后成功量 = %d, 期望 1", res.Usage.Success)
	}
}

// TestInvokeQuotaSharedByAllCredentials 验证租户下所有凭证共享同一份额度（R2）。
func TestInvokeQuotaSharedByAllCredentials(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	if err := db.UpdateTenantQuota(ctx, testTenantA, 1); err != nil {
		t.Fatalf("调整额度失败: %v", err)
	}

	if _, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"}); err != nil {
		t.Fatalf("首次调用应成功: %v", err)
	}
	_, err := svc.Invoke(ctx, "app", testTenantA, "cred-a2",
		InvokeInput{RequestNo: "req-2", Model: "m", Prompt: "p"})
	if !errors.Is(err, domain.ErrQuotaExceeded) {
		t.Fatalf("第二个凭证应受同一份额度限制，实际 %v", err)
	}
}

// TestInvokeConcurrentLastQuotaSlot 验证并发调用不会突破日额度上限（R2）。
//
// 该用例配合 -race 运行：额度为 1 时，8 个并发调用只能有 1 个成功。
func TestInvokeConcurrentLastQuotaSlot(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	if err := db.UpdateTenantQuota(ctx, testTenantA, 1); err != nil {
		t.Fatalf("调整额度失败: %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded, rejected := 0, 0

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1", InvokeInput{
				RequestNo: "req-" + string(rune('a'+i)), Model: "m", Prompt: "p",
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, domain.ErrQuotaExceeded):
				rejected++
			default:
				t.Errorf("并发调用出现意外错误: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if succeeded != 1 {
		t.Fatalf("额度为 1 时成功数 = %d, 期望 1", succeeded)
	}
	if rejected != workers-1 {
		t.Fatalf("被拒数 = %d, 期望 %d", rejected, workers-1)
	}
	usage, _ := svc.TenantUsage(ctx, testTenantA, testDay)
	if usage.Success != 1 || usage.Occupied() != 1 {
		t.Fatalf("并发后用量不符: %+v", usage)
	}
}

// ---------- 失败与超时（R4） ----------

func TestInvokeUpstreamFailureReleasesQuota(t *testing.T) {
	svc, up, _ := newTestService(t)
	ctx := context.Background()
	up.failure = upstream.ErrFailed

	res, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"})
	if err != nil {
		t.Fatalf("失败应作为终态返回而不是错误: %v", err)
	}
	if res.Request.Status != domain.RequestFailed {
		t.Fatalf("终态 = %s, 期望 failed", res.Request.Status)
	}
	if res.Usage.Success != 0 || res.Usage.InFlight != 0 {
		t.Fatalf("失败应释放占用且不计成功量: %+v", res.Usage)
	}
	if res.Usage.Remaining() != 10 {
		t.Fatalf("失败后剩余额度 = %d, 期望 10", res.Usage.Remaining())
	}
	if res.Request.ErrorReason == "" {
		t.Fatalf("失败应记录原因")
	}
}

func TestInvokeUpstreamTimeoutReleasesQuota(t *testing.T) {
	svc, up, _ := newTestService(t)
	ctx := context.Background()
	up.failure = upstream.ErrTimeout

	res, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"})
	if err != nil {
		t.Fatalf("超时应作为终态返回而不是错误: %v", err)
	}
	if res.Request.Status != domain.RequestTimeout {
		t.Fatalf("终态 = %s, 期望 timeout", res.Request.Status)
	}
	if res.Usage.Success != 0 || res.Usage.InFlight != 0 {
		t.Fatalf("超时应释放占用且不计成功量: %+v", res.Usage)
	}
}

// TestInvokeTimeoutAndFailureAreDistinctTerminalStates 验证失败与超时是两个独立终态（确认结论 Q1）。
func TestInvokeTimeoutAndFailureAreDistinctTerminalStates(t *testing.T) {
	svc, up, _ := newTestService(t)
	ctx := context.Background()

	up.failure = upstream.ErrFailed
	failed, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-f", Model: "m", Prompt: "p"})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}

	up.failure = upstream.ErrTimeout
	timedOut, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-t", Model: "m", Prompt: "p"})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}

	if failed.Request.Status == timedOut.Request.Status {
		t.Fatalf("失败与超时必须是不同终态")
	}
	if failed.Usage.Occupied() != timedOut.Usage.Occupied() {
		t.Fatalf("失败与超时的计量口径应一致")
	}
}

// TestInvokeLateResultOnlyRecordedAsDiagnostic 验证迟到结果只作诊断，不改变计量（R4/确认结论 Q11）。
func TestInvokeLateResultOnlyRecordedAsDiagnostic(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	svc.EnableTestHooks = true
	svc.AddTestHook(TestHook{Kind: "late_result", RequestNo: "req-1"})

	res, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("诊断记录数 = %d, 期望 1", len(res.Diagnostics))
	}
	if res.Diagnostics[0].Kind != "late_result" {
		t.Fatalf("诊断类型 = %s, 期望 late_result", res.Diagnostics[0].Kind)
	}
	if res.Request.Status != domain.RequestSuccess {
		t.Fatalf("迟到结果不应改变终态，终态 = %s", res.Request.Status)
	}
	if res.Usage.Success != 1 {
		t.Fatalf("迟到结果不应追加扣额，成功量 = %d", res.Usage.Success)
	}

	diags, err := svc.Diagnostics(ctx, testTenantA)
	if err != nil {
		t.Fatalf("查询诊断失败: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("租户诊断记录数 = %d, 期望 1", len(diags))
	}
}

// TestInvokeUpstreamLateResultOnlyRecordedAsDiagnostic 验证真实上游链路下的迟到结果：
// 上游在 2 秒硬时限之后才返回成功，平台只写诊断记录，
// 不改变已经返回的超时终态，也不追加扣额（R4）。
func TestInvokeUpstreamLateResultOnlyRecordedAsDiagnostic(t *testing.T) {
	svc, fake, db := newTestService(t)
	ctx := context.Background()
	fake.failure = upstream.ErrTimeout

	res, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-late", Model: "m", Prompt: "p"})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if res.Request.Status != domain.RequestTimeout {
		t.Fatalf("终态 = %s，期望 timeout", res.Request.Status)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("尚未收到迟到结果时不应有诊断记录，实际 %d 条", len(res.Diagnostics))
	}

	// 上游在超时之后返回成功。
	fake.fireLateResult(res.Request.ID, &upstream.Response{Content: "迟到输出", UpstreamID: "up-late"}, nil)

	diags, err := db.ListDiagnosticsByRequest(ctx, res.Request.ID)
	if err != nil {
		t.Fatalf("查询诊断失败: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("诊断记录数 = %d，期望 1", len(diags))
	}
	if diags[0].Kind != "late_result" {
		t.Fatalf("诊断类型 = %s，期望 late_result", diags[0].Kind)
	}

	// 终态不变，仍是超时。
	req, err := db.GetRequest(ctx, res.Request.ID)
	if err != nil {
		t.Fatalf("读取请求失败: %v", err)
	}
	if req.Status != domain.RequestTimeout {
		t.Fatalf("迟到结果不应改变终态，终态 = %s", req.Status)
	}
	// 不追加扣额：超时已释放占用，迟到结果不计入成功量。
	usage, err := db.GetDayUsage(ctx, testTenantA, testDay, 10)
	if err != nil {
		t.Fatalf("读取用量失败: %v", err)
	}
	if usage.Success != 0 || usage.InFlight != 0 {
		t.Fatalf("迟到结果不应追加扣额，成功量 = %d，处理中 = %d", usage.Success, usage.InFlight)
	}
}

// TestInvokeLateResultIgnoredForNonTimeoutRequest 验证非超时终态不会记录迟到结果。
func TestInvokeLateResultIgnoredForNonTimeoutRequest(t *testing.T) {
	svc, fake, db := newTestService(t)
	ctx := context.Background()

	res, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-ok", Model: "m", Prompt: "p"})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	fake.fireLateResult(res.Request.ID, &upstream.Response{Content: "不应记录", UpstreamID: "up-x"}, nil)

	diags, err := db.ListDiagnosticsByRequest(ctx, res.Request.ID)
	if err != nil {
		t.Fatalf("查询诊断失败: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("成功终态不应记录迟到结果，实际 %d 条", len(diags))
	}
}

// TestInvokeLateResultIgnoredForUnknownRequest 验证关联 ID 不存在时不写诊断。
func TestInvokeLateResultIgnoredForUnknownRequest(t *testing.T) {
	svc, fake, _ := newTestService(t)
	fake.fireLateResult("req-not-exist", &upstream.Response{Content: "x", UpstreamID: "up-x"}, nil)
	fake.fireLateResult("", &upstream.Response{Content: "x", UpstreamID: "up-x"}, nil)
	diags, err := svc.Diagnostics(context.Background(), testTenantA)
	if err != nil {
		t.Fatalf("查询诊断失败: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("未知请求不应产生诊断记录，实际 %d 条", len(diags))
	}
}

func TestInvokeTestHooksDisabledByDefault(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.AddTestHook(TestHook{Kind: "late_result", RequestNo: "req-1"})

	res, err := svc.Invoke(context.Background(), "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("未开启测试钩子时不应产生诊断记录")
	}
}

func TestInvokeTestHookIgnoresOtherRequestNo(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.EnableTestHooks = true
	svc.AddTestHook(TestHook{Kind: "late_result", RequestNo: "other"})
	svc.AddTestHook(TestHook{Kind: "other_kind", RequestNo: "req-1"})

	res, err := svc.Invoke(context.Background(), "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("只应匹配 late_result 且编号相符的钩子")
	}
}

func TestInvokeTestHookCustomDetail(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.EnableTestHooks = true
	svc.AddTestHook(TestHook{Kind: "late_result", RequestNo: "req-1", Detail: "上游在超时后 0.5 秒返回"})

	res, err := svc.Invoke(context.Background(), "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Detail != "上游在超时后 0.5 秒返回" {
		t.Fatalf("诊断详情不符: %+v", res.Diagnostics)
	}
}

// ---------- 停用后的受理（R6） ----------

func TestInvokeRejectedAfterTenantDisabled(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	if err := svc.DisableTenant(ctx, "ops", testTenantA, "停机"); err != nil {
		t.Fatalf("停用租户失败: %v", err)
	}

	_, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"})
	if !errors.Is(err, domain.ErrTenantDisabled) {
		t.Fatalf("期望 ErrTenantDisabled，实际 %v", err)
	}
	rejected, _ := svc.RejectedAttempts(ctx, testTenantA, testDay)
	if len(rejected) != 1 {
		t.Fatalf("被拒绝尝试数 = %d, 期望 1", len(rejected))
	}
}

func TestInvokeRejectedAfterCredentialDisabled(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	if err := svc.DisableCredential(ctx, "admin-a", testTenantA, "cred-a1", "泄露"); err != nil {
		t.Fatalf("停用凭证失败: %v", err)
	}

	_, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"})
	if !errors.Is(err, domain.ErrCredentialDisabled) {
		t.Fatalf("期望 ErrCredentialDisabled，实际 %v", err)
	}
}

func TestInvokeRejectsCredentialOfOtherTenant(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, err := svc.Invoke(context.Background(), "app", testTenantA, "cred-b1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"})
	if !errors.Is(err, domain.ErrTenantMismatch) {
		t.Fatalf("期望 ErrTenantMismatch，实际 %v", err)
	}
}

func TestInvokeUnknownCredential(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, err := svc.Invoke(context.Background(), "app", testTenantA, "nope",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

func TestInvokeUnknownTenant(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, err := svc.Invoke(context.Background(), "app", "nope", "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

// ---------- 日终核查（R8） ----------

func TestDailyReportSplitsTerminalStates(t *testing.T) {
	svc, up, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"}); err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	up.failure = upstream.ErrFailed
	if _, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-2", Model: "m", Prompt: "p"}); err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	up.failure = upstream.ErrTimeout
	if _, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-3", Model: "m", Prompt: "p"}); err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	// 额度超限产生一条被拒绝记录。
	if _, err := svc.AdjustQuota(ctx, "ops", testTenantA, 1, "降额"); err != nil {
		t.Fatalf("调整额度失败: %v", err)
	}
	if _, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-4", Model: "m", Prompt: "p"}); !errors.Is(err, domain.ErrQuotaExceeded) {
		t.Fatalf("期望 ErrQuotaExceeded，实际 %v", err)
	}

	report, err := svc.DailyReport(ctx, testTenantA, testDay)
	if err != nil {
		t.Fatalf("生成日终报表失败: %v", err)
	}
	if report.Counts[domain.RequestSuccess] != 1 {
		t.Fatalf("成功数 = %d, 期望 1", report.Counts[domain.RequestSuccess])
	}
	if report.Counts[domain.RequestFailed] != 1 {
		t.Fatalf("失败数 = %d, 期望 1", report.Counts[domain.RequestFailed])
	}
	if report.Counts[domain.RequestTimeout] != 1 {
		t.Fatalf("超时数 = %d, 期望 1", report.Counts[domain.RequestTimeout])
	}
	if report.Counts[domain.RequestRejected] != 1 {
		t.Fatalf("被拒数 = %d, 期望 1", report.Counts[domain.RequestRejected])
	}
	if report.Counts[domain.RequestProcessing] != 0 {
		t.Fatalf("处理中数 = %d, 期望 0", report.Counts[domain.RequestProcessing])
	}
	if !report.Consistent {
		t.Fatalf("成功记录数与用量次数应一致: %s", report.MismatchNote)
	}
	if report.UsageSuccess != 1 || report.UsageInFlight != 0 {
		t.Fatalf("用量口径不符: %+v", report)
	}
	if report.QuotaLimit != 1 || report.Remaining != 0 {
		t.Fatalf("额度与剩余不符: %+v", report)
	}
}

func TestDailyReportIncludesQuotaChangesAndEvents(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.AdjustQuota(ctx, "ops", testTenantA, 20, "扩容"); err != nil {
		t.Fatalf("调整额度失败: %v", err)
	}
	if err := svc.DisableCredential(ctx, "admin-a", testTenantA, "cred-a1", "泄露"); err != nil {
		t.Fatalf("停用凭证失败: %v", err)
	}

	report, err := svc.DailyReport(ctx, testTenantA, testDay)
	if err != nil {
		t.Fatalf("生成日终报表失败: %v", err)
	}
	if len(report.QuotaChanges) != 1 {
		t.Fatalf("配额变更条数 = %d, 期望 1", len(report.QuotaChanges))
	}
	if len(report.Events) != 1 || report.Events[0].Kind != "credential_disabled" {
		t.Fatalf("停用事件缺失: %+v", report.Events)
	}
}

func TestDailyReportUnknownTenant(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.DailyReport(context.Background(), "nope", testDay); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

// ---------- 明细查询 ----------

func TestRequestsByTenantAndCredential(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-1", Model: "m", Prompt: "p"}); err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if _, err := svc.Invoke(ctx, "app", testTenantB, "cred-b1",
		InvokeInput{RequestNo: "req-2", Model: "m", Prompt: "p"}); err != nil {
		t.Fatalf("调用失败: %v", err)
	}

	byTenant, err := svc.RequestsByTenant(ctx, testTenantA, testDay)
	if err != nil {
		t.Fatalf("查询租户请求失败: %v", err)
	}
	if len(byTenant) != 1 || byTenant[0].RequestNo != "req-1" {
		t.Fatalf("租户请求列表不符: %+v", byTenant)
	}

	byCred, err := svc.RequestsByCredential(ctx, "cred-b1", testDay)
	if err != nil {
		t.Fatalf("查询凭证请求失败: %v", err)
	}
	if len(byCred) != 1 || byCred[0].RequestNo != "req-2" {
		t.Fatalf("凭证请求列表不符: %+v", byCred)
	}
}

func TestTenantUsageReflectsQuota(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	usage, err := svc.TenantUsage(ctx, testTenantA, testDay)
	if err != nil {
		t.Fatalf("查询用量失败: %v", err)
	}
	if usage.QuotaLimit != 10 || usage.Success != 0 {
		t.Fatalf("用量不符: %+v", usage)
	}
}

// ---------- 时钟 ----------

func TestOffsetClock(t *testing.T) {
	base := fixedClock{t: testNow}
	c := OffsetClock{Base: base, Offset: 3 * time.Hour}
	if got := c.Now(); !got.Equal(testNow.Add(3 * time.Hour)) {
		t.Fatalf("偏移时间 = %v", got)
	}
	// 基时钟为空时退回系统时钟。
	c2 := OffsetClock{Offset: time.Minute}
	if c2.Now().IsZero() {
		t.Fatalf("空基时钟应退回系统时钟")
	}
}

func TestServiceClockAccessors(t *testing.T) {
	svc, _, db := newTestService(t)
	if svc.DB() != db {
		t.Fatalf("DB() 应返回同一个句柄")
	}
	if _, ok := svc.Clock().(fixedClock); !ok {
		t.Fatalf("Clock() 应返回构造时的时钟")
	}
	if svc.CurrentDay() != testDay {
		t.Fatalf("当前日期 = %s, 期望 %s", svc.CurrentDay(), testDay)
	}
	svc.SetClockOffset(48 * time.Hour)
	if svc.CurrentDay() == testDay {
		t.Fatalf("设置偏移后当前日期应变化")
	}
}
