package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"modelquota/internal/domain"
	"modelquota/internal/store"
	"modelquota/internal/upstream"
)

// TestNewFallsBackToSystemClock 验证未提供时钟时使用系统时钟。
func TestNewFallsBackToSystemClock(t *testing.T) {
	_, _, db := newTestService(t)
	plain := New(db, nil, &fakeUpstream{})
	if plain.Clock() == nil {
		t.Fatalf("时钟不应为空")
	}
	if plain.CurrentDay() == "" {
		t.Fatalf("当前日期不应为空")
	}
	if plain.MaxCredentialsPerTenant != 2 {
		t.Fatalf("凭证上限 = %d, 期望 2", plain.MaxCredentialsPerTenant)
	}
	if plain.SessionTTL != 12*60*60*1e9 {
		t.Fatalf("会话有效期 = %v, 期望 12h", plain.SessionTTL)
	}
}

// TestOpenTenantUsesDefaultReason 验证未填原因时使用默认留痕说明。
func TestOpenTenantUsesDefaultReason(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()

	tenant, err := svc.OpenTenant(ctx, "ops", TenantInput{Name: "企业丙", DailyQuota: 10})
	if err != nil {
		t.Fatalf("开通租户失败: %v", err)
	}
	changes, err := db.ListQuotaChanges(ctx, tenant.ID, testDay)
	if err != nil {
		t.Fatalf("列出配额变更失败: %v", err)
	}
	if len(changes) != 1 || changes[0].Reason == "" {
		t.Fatalf("应写入默认原因: %+v", changes)
	}

	// 对已有租户重新开通时同样使用默认原因。
	if _, err := svc.OpenTenant(ctx, "ops", TenantInput{TenantID: testTenantA, DailyQuota: 12}); err != nil {
		t.Fatalf("重新开通租户失败: %v", err)
	}
	changes, _ = db.ListQuotaChanges(ctx, testTenantA, testDay)
	if len(changes) != 1 || changes[0].Reason == "" {
		t.Fatalf("应写入默认原因: %+v", changes)
	}
}

// TestInvokeConcurrentSameRequestNo 验证并发提交同一请求编号时按幂等处理。
//
// 只有一个请求会真正落库，其余的应命中幂等而不是抛出唯一约束错误，
// 且占用额度必须被归还，不能泄漏。
func TestInvokeConcurrentSameRequestNo(t *testing.T) {
	svc, up, _ := newTestService(t)
	ctx := context.Background()

	const workers = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	replayed, succeeded, failed := 0, 0, 0

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			res, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1", InvokeInput{
				RequestNo: "req-same", Model: "m", Prompt: "p",
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				failed++
				t.Errorf("并发同编号调用不应失败: %v", err)
			case res.Replayed:
				replayed++
			default:
				succeeded++
			}
		}()
	}
	wg.Wait()

	if succeeded != 1 {
		t.Fatalf("真正受理的请求数 = %d, 期望 1", succeeded)
	}
	if replayed != workers-1 {
		t.Fatalf("幂等命中数 = %d, 期望 %d", replayed, workers-1)
	}
	if failed != 0 {
		t.Fatalf("失败数 = %d, 期望 0", failed)
	}
	if up.callCount() != 1 {
		t.Fatalf("上游调用次数 = %d, 期望 1", up.callCount())
	}

	// 额度不得泄漏：成功量应为 1，处理中应为 0。
	usage, err := svc.TenantUsage(ctx, testTenantA, testDay)
	if err != nil {
		t.Fatalf("查询用量失败: %v", err)
	}
	if usage.Success != 1 || usage.InFlight != 0 {
		t.Fatalf("并发后用量不符: %+v", usage)
	}
}

// TestInvokeConcurrentSameRequestNoDifferentParams 验证并发下参数冲突被正确拒绝。
func TestInvokeConcurrentSameRequestNoDifferentParams(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-x", Model: "m", Prompt: "p1"})
	if err != nil {
		t.Fatalf("首次调用失败: %v", err)
	}
	_, err = svc.Invoke(ctx, "app", testTenantA, "cred-a1",
		InvokeInput{RequestNo: "req-x", Model: "m", Prompt: "p2"})
	if !errors.Is(err, domain.ErrRequestNoConflict) {
		t.Fatalf("期望 ErrRequestNoConflict，实际 %v", err)
	}
}

// ---------- 终态结算（R4） ----------

// TestSettleSuccessCountsSuccess 验证成功分支的计量口径。
func TestSettleSuccessCountsSuccess(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	req := seedProcessingRequest(t, db, "req-s1")

	if err := svc.settle(ctx, req, &upstream.Response{Content: "输出"}, nil); err != nil {
		t.Fatalf("结算失败: %v", err)
	}
	stored, _ := db.GetRequest(ctx, req.ID)
	if stored.Status != domain.RequestSuccess || stored.UpstreamResult != "输出" {
		t.Fatalf("结算后请求不符: %+v", stored)
	}
	usage, _ := svc.TenantUsage(ctx, testTenantA, testDay)
	if usage.Success != 1 || usage.InFlight != 0 {
		t.Fatalf("结算后用量不符: %+v", usage)
	}
}

// TestSettleTimeoutReleasesOccupancy 验证超时分支释放占用且不计成功量。
func TestSettleTimeoutReleasesOccupancy(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	req := seedProcessingRequest(t, db, "req-s2")

	if err := svc.settle(ctx, req, nil, upstream.ErrTimeout); err != nil {
		t.Fatalf("结算失败: %v", err)
	}
	stored, _ := db.GetRequest(ctx, req.ID)
	if stored.Status != domain.RequestTimeout {
		t.Fatalf("终态 = %s, 期望 timeout", stored.Status)
	}
	usage, _ := svc.TenantUsage(ctx, testTenantA, testDay)
	if usage.Success != 0 || usage.InFlight != 0 {
		t.Fatalf("超时后用量不符: %+v", usage)
	}
}

// TestSettleFailureReleasesOccupancy 验证失败分支释放占用且不计成功量。
func TestSettleFailureReleasesOccupancy(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	req := seedProcessingRequest(t, db, "req-s3")

	if err := svc.settle(ctx, req, nil, upstream.ErrFailed); err != nil {
		t.Fatalf("结算失败: %v", err)
	}
	stored, _ := db.GetRequest(ctx, req.ID)
	if stored.Status != domain.RequestFailed {
		t.Fatalf("终态 = %s, 期望 failed", stored.Status)
	}
	if stored.ErrorReason != "上游明确失败" {
		t.Fatalf("失败原因 = %q", stored.ErrorReason)
	}
	usage, _ := svc.TenantUsage(ctx, testTenantA, testDay)
	if usage.Success != 0 || usage.InFlight != 0 {
		t.Fatalf("失败后用量不符: %+v", usage)
	}
}

// TestSettleOtherErrorKeepsReason 验证非标准错误保留原始原因。
func TestSettleOtherErrorKeepsReason(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	req := seedProcessingRequest(t, db, "req-s4")

	if err := svc.settle(ctx, req, nil, errors.New("网络不可达")); err != nil {
		t.Fatalf("结算失败: %v", err)
	}
	stored, _ := db.GetRequest(ctx, req.ID)
	if stored.Status != domain.RequestFailed {
		t.Fatalf("终态 = %s, 期望 failed", stored.Status)
	}
	if stored.ErrorReason != "网络不可达" {
		t.Fatalf("失败原因 = %q, 期望保留原文", stored.ErrorReason)
	}
}

// TestSettleFailsWhenDatabaseBroken 验证数据库异常时结算返回错误。
func TestSettleFailsWhenDatabaseBroken(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	req := seedProcessingRequest(t, db, "req-s5")

	if err := db.Close(); err != nil {
		t.Fatalf("关闭数据库失败: %v", err)
	}
	if err := svc.settle(ctx, req, &upstream.Response{Content: "x"}, nil); err == nil {
		t.Fatalf("数据库异常时结算应报错")
	}
	if err := svc.settle(ctx, req, nil, upstream.ErrTimeout); err == nil {
		t.Fatalf("数据库异常时结算应报错")
	}
	if err := svc.settle(ctx, req, nil, upstream.ErrFailed); err == nil {
		t.Fatalf("数据库异常时结算应报错")
	}
	if _, err := svc.currentUsage(ctx, testTenantA, testDay); err == nil {
		t.Fatalf("数据库异常时读取用量应报错")
	}
}

// seedProcessingRequest 写入一条处于处理中的请求并占用额度。
func seedProcessingRequest(t *testing.T, db *store.DB, requestNo string) *domain.ModelRequest {
	t.Helper()
	ctx := context.Background()
	req := &domain.ModelRequest{
		ID: "req-" + requestNo, TenantID: testTenantA, CredentialID: "cred-a1",
		RequestNo: requestNo, ParamsHash: "hash-" + requestNo, Day: testDay,
		Status: domain.RequestProcessing, Model: "m", Prompt: "p", ReceivedAt: testNow,
	}
	if err := db.InsertRequest(ctx, req); err != nil {
		t.Fatalf("写入请求失败: %v", err)
	}
	if _, err := db.ReserveInflight(ctx, testTenantA, testDay); err != nil {
		t.Fatalf("占用额度失败: %v", err)
	}
	return req
}
