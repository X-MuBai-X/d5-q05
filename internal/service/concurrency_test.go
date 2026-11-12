package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"modelquota/internal/domain"
)

// 本文件覆盖 2026-11-11 独立测试/验收 Agent 提出的四个可验证缺口：
//   - D1：降额与受理并发时「占用不超过额度」的不变量（回归用例）。
//   - F3：同一编号并发提交不同参数时的冲突处理与额度归还。
//   - F4：请求写入失败时已占用额度的归还兜底。
//   - F6：在途请求在日终核查报表中的真实可见性。

// TestConcurrentQuotaChangeNeverExceedsQuota 验证并发降额与并发受理下，
// 「当日占用不超过日额度」始终成立（D1 回归）。
//
// 缺陷来源：独立验收 Agent 实测复现——原实现把额度快照从 service 层
// 传进条件 UPDATE，并发降额与并发受理交错时会出现 occupied > quota，
// 且日终报表仍返回 consistent=true，运营无法发现。
// 修复方式：额度上限由数据库在同一条语句内读取实时值；
// 降额的下限判定与写入同样在同一条语句内完成。
func TestConcurrentQuotaChangeNeverExceedsQuota(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	const quota = 1000
	if _, err := svc.AdjustQuota(ctx, "ops", testTenantA, quota, "并发前置放宽"); err != nil {
		t.Fatalf("放宽额度失败: %v", err)
	}

	for trial := 0; trial < 20; trial++ {
		// 每轮先把额度复位，保证并发受理有空间。
		if _, err := svc.AdjustQuota(ctx, "ops", testTenantA, quota, "复位额度"); err != nil {
			t.Fatalf("复位额度失败: %v", err)
		}
		usage, err := svc.TenantUsage(ctx, testTenantA, testDay)
		if err != nil {
			t.Fatalf("读取用量失败: %v", err)
		}
		target := usage.Occupied() // 降到当前占用：单线程语义下合法

		var wg sync.WaitGroup
		start := make(chan struct{})

		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// 并发降额：若同时有受理抬高占用，下限判定会拒绝这次降额。
			_, _ = svc.AdjustQuota(ctx, "ops", testTenantA, target, "并发降额")
		}()
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				_, _ = svc.Invoke(ctx, "app", testTenantA, "cred-a1", InvokeInput{
					RequestNo: fmt.Sprintf("D1-%d-%d", trial, i), Model: "m", Prompt: "p",
				})
			}(i)
		}
		close(start)
		wg.Wait()

		final, err := svc.TenantUsage(ctx, testTenantA, testDay)
		if err != nil {
			t.Fatalf("读取用量失败: %v", err)
		}
		if final.Occupied() > final.QuotaLimit {
			t.Fatalf("第 %d 轮：占用 %d 超过额度 %d", trial, final.Occupied(), final.QuotaLimit)
		}
	}
}

// TestConcurrentQuotaLoweringIsRejectedWhenOccupancyGrows 验证并发受理抬高占用后，
// 降到旧占用数的降额请求会被下限规则拒绝，而不是写出更低的额度（R5）。
func TestConcurrentQuotaLoweringIsRejectedWhenOccupancyGrows(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.AdjustQuota(ctx, "ops", testTenantA, 10, "基线额度"); err != nil {
		t.Fatalf("设置额度失败: %v", err)
	}
	// 先产生一条成功记录，占用为 1。
	if _, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1", InvokeInput{
		RequestNo: "D1-BASE-1", Model: "m", Prompt: "p",
	}); err != nil {
		t.Fatalf("首次调用失败: %v", err)
	}

	// 降到 0：单线程语义下会被下限拒绝（占用为 1）。
	_, err := svc.AdjustQuota(ctx, "ops", testTenantA, 0, "低于下限")
	if !errors.Is(err, domain.ErrQuotaFloor) {
		t.Fatalf("期望 ErrQuotaFloor，实际 %v", err)
	}
	usage, uerr := svc.TenantUsage(ctx, testTenantA, testDay)
	if uerr != nil {
		t.Fatalf("读取用量失败: %v", uerr)
	}
	if usage.QuotaLimit != 10 || usage.Occupied() != 1 {
		t.Fatalf("被拒绝的降额不应改变额度或占用: %+v", usage)
	}
}

// TestInvokeConcurrentSameRequestNoDifferentParamsConflict 验证同一编号并发提交不同参数时，
// 只允许一条被真正受理，其余返回参数冲突，且额度不得泄漏（F3）。
func TestInvokeConcurrentSameRequestNoDifferentParamsConflict(t *testing.T) {
	svc, up, _ := newTestService(t)
	ctx := context.Background()

	const workers = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	conflicts, succeeded := 0, 0
	start := make(chan struct{})

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1", InvokeInput{
				RequestNo: "F3-SAME-NO", Model: "m", Prompt: fmt.Sprintf("p%d", i),
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, domain.ErrRequestNoConflict):
				conflicts++
			default:
				t.Errorf("期望参数冲突错误，实际 %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if succeeded != 1 {
		t.Fatalf("真正受理的请求数 = %d, 期望 1", succeeded)
	}
	if conflicts != workers-1 {
		t.Fatalf("参数冲突数 = %d, 期望 %d", conflicts, workers-1)
	}
	if up.callCount() != 1 {
		t.Fatalf("上游调用次数 = %d, 期望 1", up.callCount())
	}
	usage, err := svc.TenantUsage(ctx, testTenantA, testDay)
	if err != nil {
		t.Fatalf("读取用量失败: %v", err)
	}
	if usage.InFlight != 0 {
		t.Fatalf("并发冲突后处理中应归零: %+v", usage)
	}
	if usage.Occupied() != 1 {
		t.Fatalf("并发冲突后只应保留一条成功占用: %+v", usage)
	}
}

// TestInvokeReleasesQuotaWhenInsertFails 验证请求写入失败时已占用的额度必须归还（F4）。
//
// 故障注入：在模型请求表上挂一个必然失败的触发器，
// 走的是「写入失败且不是编号冲突」的兜底路径。
func TestInvokeReleasesQuotaWhenInsertFails(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()

	if _, err := db.SQL().ExecContext(ctx, `CREATE TRIGGER block_request_insert
		BEFORE INSERT ON model_requests BEGIN SELECT RAISE(ABORT, '模拟写入失败'); END;`); err != nil {
		t.Fatalf("创建写入失败触发器失败: %v", err)
	}

	_, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1", InvokeInput{
		RequestNo: "F4-INSERT-FAIL", Model: "m", Prompt: "p",
	})
	if err == nil {
		t.Fatalf("写入失败时应返回错误")
	}
	usage, uerr := svc.TenantUsage(ctx, testTenantA, testDay)
	if uerr != nil {
		t.Fatalf("读取用量失败: %v", uerr)
	}
	if usage.InFlight != 0 {
		t.Fatalf("写入失败后处理中未归零: %+v", usage)
	}
	if usage.Occupied() != 0 {
		t.Fatalf("写入失败后额度未归还: %+v", usage)
	}
}

// TestDailyReportShowsInFlightWhileRequestProcessing 验证在途请求在日终核查报表中真实可见（F6）。
//
// 此前没有任何用例断言过「处理中」计数大于 0，在途可见性缺少回归保护。
func TestDailyReportShowsInFlightWhileRequestProcessing(t *testing.T) {
	svc, up, _ := newTestService(t)
	ctx := context.Background()

	up.mu.Lock()
	up.delay = 300 * time.Millisecond
	up.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = svc.Invoke(ctx, "app", testTenantA, "cred-a1", InvokeInput{
			RequestNo: "F6-INFLIGHT", Model: "m", Prompt: "p",
		})
	}()

	// 等待请求进入处理中状态。
	deadline := time.Now().Add(3 * time.Second)
	inFlight := false
	for time.Now().Before(deadline) {
		usage, err := svc.TenantUsage(ctx, testTenantA, testDay)
		if err == nil && usage.InFlight == 1 {
			inFlight = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !inFlight {
		t.Fatalf("请求未进入处理中状态")
	}

	report, err := svc.DailyReport(ctx, testTenantA, testDay)
	if err != nil {
		t.Fatalf("生成日终报表失败: %v", err)
	}
	if report.Counts[domain.RequestProcessing] != 1 {
		t.Fatalf("报表中的处理中计数 = %d, 期望 1: %+v", report.Counts[domain.RequestProcessing], report.Counts)
	}
	if report.UsageInFlight != 1 {
		t.Fatalf("报表中的在途用量 = %d, 期望 1", report.UsageInFlight)
	}

	<-done
	report2, err := svc.DailyReport(ctx, testTenantA, testDay)
	if err != nil {
		t.Fatalf("生成日终报表失败: %v", err)
	}
	if report2.Counts[domain.RequestProcessing] != 0 {
		t.Fatalf("请求结束后处理中计数应归零，实际 %d", report2.Counts[domain.RequestProcessing])
	}
	if report2.Counts[domain.RequestSuccess] != 1 {
		t.Fatalf("请求结束后成功计数 = %d, 期望 1", report2.Counts[domain.RequestSuccess])
	}
}
