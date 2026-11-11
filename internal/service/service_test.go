package service

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"modelquota/internal/domain"
	"modelquota/internal/store"
	"modelquota/internal/upstream"
)

// ---------- 测试替身 ----------

// fixedClock 是可控时钟，让计量口径的日期归属可确定。
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// fakeUpstream 是可控的上游替身，用来触发成功、失败与超时三类结果。
type fakeUpstream struct {
	mu      sync.Mutex
	calls   int
	last    upstream.Request
	result  *upstream.Response
	failure error
	delay   time.Duration
}

func (f *fakeUpstream) Complete(ctx context.Context, req upstream.Request, timeout time.Duration) (*upstream.Response, error) {
	f.mu.Lock()
	f.calls++
	f.last = req
	delay := f.delay
	failure := f.failure
	result := f.result
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-ctx.Done():
			return nil, upstream.ErrTimeout
		case <-time.After(delay):
		}
	}
	if failure != nil {
		return nil, failure
	}
	if result == nil {
		result = &upstream.Response{Content: "模拟输出", UpstreamID: "up-1"}
	}
	return result, nil
}

func (f *fakeUpstream) Name() string { return "fake-upstream" }

func (f *fakeUpstream) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// ---------- 测试夹具 ----------

const (
	testTenantA = "tenant-a"
	testTenantB = "tenant-b"
	testDay     = "2026-01-01"
)

var testNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// newTestService 构造带预置租户、凭证与管理员的业务服务。
func newTestService(t *testing.T) (*Service, *fakeUpstream, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "svc.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	for _, id := range []string{testTenantA, testTenantB} {
		if err := db.CreateTenant(ctx, &domain.Tenant{
			ID: id, Name: "租户 " + id, Status: domain.TenantActive,
			DailyQuota: 10, CreatedAt: testNow,
		}); err != nil {
			t.Fatalf("写入租户失败: %v", err)
		}
	}
	for _, c := range []struct{ id, tenant string }{
		{"cred-a1", testTenantA}, {"cred-a2", testTenantA}, {"cred-b1", testTenantB},
	} {
		if err := db.CreateCredential(ctx, &domain.Credential{
			ID: c.id, TenantID: c.tenant, Name: c.id, KeyHash: domain.HashSecret(c.id),
			Status: domain.CredentialActive, CreatedAt: testNow, CreatedBy: "admin",
		}); err != nil {
			t.Fatalf("写入凭证失败: %v", err)
		}
	}
	for _, a := range []struct{ id, tenant, user, role string }{
		{"admin-a", testTenantA, "admin-a", string(domain.AdminTenant)},
		{"admin-b", testTenantB, "admin-b", string(domain.AdminTenant)},
		{"ops", "", "ops", string(domain.AdminOps)},
	} {
		if err := db.CreateAdmin(ctx, &domain.Admin{
			ID: a.id, TenantID: a.tenant, Username: a.user,
			PasswordHash: domain.HashSecret(a.user + "-secret"),
			Role:         domain.AdminRole(a.role), CreatedAt: testNow,
		}); err != nil {
			t.Fatalf("写入管理员失败: %v", err)
		}
	}

	up := &fakeUpstream{}
	svc := New(db, fixedClock{t: testNow}, up)
	return svc, up, db
}

// ---------- 租户开通（R1） ----------

func TestOpenTenantCreatesTenantAndQuotaChange(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()

	tenant, err := svc.OpenTenant(ctx, "ops", TenantInput{Name: "企业丙", DailyQuota: 25, Reason: "新签"})
	if err != nil {
		t.Fatalf("开通租户失败: %v", err)
	}
	if tenant.Status != domain.TenantActive || tenant.DailyQuota != 25 {
		t.Fatalf("租户信息不符: %+v", tenant)
	}
	changes, err := db.ListQuotaChanges(ctx, tenant.ID, testDay)
	if err != nil {
		t.Fatalf("列出配额变更失败: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("配额变更条数 = %d, 期望 1", len(changes))
	}
	if changes[0].Operator != "ops" || changes[0].Reason != "新签" || changes[0].NewQuota != 25 {
		t.Fatalf("配额变更留痕不符: %+v", changes[0])
	}
}

func TestOpenTenantRequiresNameForNewTenant(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.OpenTenant(context.Background(), "ops", TenantInput{DailyQuota: 10}); err == nil {
		t.Fatalf("新建租户时名称为空应报错")
	}
}

func TestOpenTenantRejectsQuotaOutOfRange(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.OpenTenant(context.Background(), "ops",
		TenantInput{Name: "企业丙", DailyQuota: domain.MaxQuota + 1}); !errors.Is(err, domain.ErrQuotaRange) {
		t.Fatalf("期望 ErrQuotaRange，实际 %v", err)
	}
}

func TestOpenTenantReactivatesExistingTenant(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()
	if err := db.UpdateTenantStatus(ctx, testTenantA, domain.TenantDisabled); err != nil {
		t.Fatalf("停用租户失败: %v", err)
	}

	tenant, err := svc.OpenTenant(ctx, "ops", TenantInput{TenantID: testTenantA, Name: "", DailyQuota: 30, Reason: "恢复服务"})
	if err != nil {
		t.Fatalf("重新开通租户失败: %v", err)
	}
	if tenant.Status != domain.TenantActive || tenant.DailyQuota != 30 {
		t.Fatalf("重新开通后的租户不符: %+v", tenant)
	}

	events, err := db.ListEvents(ctx, testTenantA, testDay)
	if err != nil {
		t.Fatalf("列出事件失败: %v", err)
	}
	if len(events) != 1 || events[0].Kind != "tenant_enabled" {
		t.Fatalf("应记录租户恢复事件: %+v", events)
	}
}

func TestOpenTenantUnknownTenant(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.OpenTenant(context.Background(), "ops",
		TenantInput{TenantID: "nope", DailyQuota: 10}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

// ---------- 配额调整（R5） ----------

func TestAdjustQuotaRecordsChange(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()

	change, err := svc.AdjustQuota(ctx, "ops", testTenantA, 50, "客户扩容")
	if err != nil {
		t.Fatalf("调整额度失败: %v", err)
	}
	if change.OldQuota != 10 || change.NewQuota != 50 {
		t.Fatalf("配额变更不符: %+v", change)
	}
	if change.Operator != "ops" || change.Reason != "客户扩容" || change.EffectiveAt.IsZero() {
		t.Fatalf("留痕信息不完整: %+v", change)
	}
	tenant, _ := db.GetTenant(ctx, testTenantA)
	if tenant.DailyQuota != 50 {
		t.Fatalf("租户额度 = %d, 期望 50", tenant.DailyQuota)
	}
}

func TestAdjustQuotaRequiresReason(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.AdjustQuota(context.Background(), "ops", testTenantA, 20, "  "); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("期望 ErrReasonRequired，实际 %v", err)
	}
}

func TestAdjustQuotaLowerBoundUsesSuccessPlusInflight(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	// 先消耗 3 次成功量。
	for i := 0; i < 3; i++ {
		_, err := svc.Invoke(ctx, "app", testTenantA, "cred-a1", InvokeInput{
			RequestNo: "req-" + string(rune('a'+i)), Model: "m", Prompt: "p",
		})
		if err != nil {
			t.Fatalf("调用失败: %v", err)
		}
	}

	// 下限 = 成功 3 + 处理中 0 = 3，降到 2 应被拒绝。
	if _, err := svc.AdjustQuota(ctx, "ops", testTenantA, 2, "降额"); !errors.Is(err, domain.ErrQuotaFloor) {
		t.Fatalf("期望 ErrQuotaFloor，实际 %v", err)
	}
	if _, err := svc.AdjustQuota(ctx, "ops", testTenantA, 3, "降额到下限"); err != nil {
		t.Fatalf("降到下限应允许: %v", err)
	}
}

func TestAdjustQuotaRejectsOutOfRangeAndUnknownTenant(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	if _, err := svc.AdjustQuota(ctx, "ops", testTenantA, -1, "x"); !errors.Is(err, domain.ErrQuotaRange) {
		t.Fatalf("期望 ErrQuotaRange，实际 %v", err)
	}
	if _, err := svc.AdjustQuota(ctx, "ops", "nope", 5, "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

// ---------- 停用（R6） ----------

func TestDisableTenant(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()

	if err := svc.DisableTenant(ctx, "ops", testTenantA, "欠费停机"); err != nil {
		t.Fatalf("停用租户失败: %v", err)
	}
	tenant, _ := db.GetTenant(ctx, testTenantA)
	if tenant.Status != domain.TenantDisabled {
		t.Fatalf("租户状态 = %s, 期望 disabled", tenant.Status)
	}
	events, _ := db.ListEvents(ctx, testTenantA, testDay)
	if len(events) != 1 || events[0].Kind != "tenant_disabled" || events[0].Reason != "欠费停机" {
		t.Fatalf("应记录停用事件: %+v", events)
	}
}

func TestDisableTenantRequiresReason(t *testing.T) {
	svc, _, _ := newTestService(t)
	if err := svc.DisableTenant(context.Background(), "ops", testTenantA, ""); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("期望 ErrReasonRequired，实际 %v", err)
	}
}

func TestDisableTenantUnknown(t *testing.T) {
	svc, _, _ := newTestService(t)
	if err := svc.DisableTenant(context.Background(), "ops", "nope", "r"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

// ---------- 凭证（R1/R6） ----------

func TestCreateCredential(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()

	// tenant-b 基础数据只有 1 个凭证，仍在上限 2 之内。
	cred, secret, err := svc.CreateCredential(ctx, "admin-b", testTenantB, CredentialInput{Name: "app-b2"})
	if err != nil {
		t.Fatalf("创建凭证失败: %v", err)
	}
	if cred.TenantID != testTenantB {
		t.Fatalf("凭证租户 = %s, 期望 %s", cred.TenantID, testTenantB)
	}
	if secret == "" {
		t.Fatalf("应返回明文密钥")
	}
	stored, err := db.GetCredentialByKeyHash(ctx, domain.HashSecret(secret))
	if err != nil {
		t.Fatalf("按摘要读取凭证失败: %v", err)
	}
	if stored.ID != cred.ID {
		t.Fatalf("摘要对应的凭证不符")
	}
	if cred.KeyHash == secret {
		t.Fatalf("不应存储明文密钥")
	}
}

func TestCreateCredentialRequiresName(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, _, err := svc.CreateCredential(context.Background(), "admin-a", testTenantA, CredentialInput{Name: " "}); err == nil {
		t.Fatalf("凭证名称为空应报错")
	}
}

func TestCreateCredentialRespectsTenantLimit(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	// 基础数据里 tenant-a 已有 cred-a1、cred-a2，正好等于上限 2。
	if _, _, err := svc.CreateCredential(ctx, "admin-a", testTenantA, CredentialInput{Name: "app-3"}); !errors.Is(err, domain.ErrCredentialLimit) {
		t.Fatalf("期望 ErrCredentialLimit，实际 %v", err)
	}
	// 另一个租户不受影响。
	if _, _, err := svc.CreateCredential(ctx, "admin-b", testTenantB, CredentialInput{Name: "app-b2"}); err != nil {
		t.Fatalf("其他租户创建凭证不应受限: %v", err)
	}
}

func TestDisableCredential(t *testing.T) {
	svc, _, db := newTestService(t)
	ctx := context.Background()

	if err := svc.DisableCredential(ctx, "admin-a", testTenantA, "cred-a1", "密钥泄露"); err != nil {
		t.Fatalf("停用凭证失败: %v", err)
	}
	cred, _ := db.GetCredential(ctx, "cred-a1")
	if cred.Status != domain.CredentialDisabled {
		t.Fatalf("凭证状态 = %s, 期望 disabled", cred.Status)
	}
	events, _ := db.ListEvents(ctx, testTenantA, testDay)
	if len(events) != 1 || events[0].Kind != "credential_disabled" {
		t.Fatalf("应记录凭证停用事件: %+v", events)
	}
}

func TestDisableCredentialRejectsOtherTenant(t *testing.T) {
	svc, _, _ := newTestService(t)
	if err := svc.DisableCredential(context.Background(), "admin-a", testTenantA, "cred-b1", "越权"); !errors.Is(err, domain.ErrTenantMismatch) {
		t.Fatalf("期望 ErrTenantMismatch，实际 %v", err)
	}
}

func TestDisableCredentialRequiresReason(t *testing.T) {
	svc, _, _ := newTestService(t)
	if err := svc.DisableCredential(context.Background(), "admin-a", testTenantA, "cred-a1", ""); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("期望 ErrReasonRequired，实际 %v", err)
	}
}

func TestDisableCredentialUnknown(t *testing.T) {
	svc, _, _ := newTestService(t)
	if err := svc.DisableCredential(context.Background(), "admin-a", testTenantA, "nope", "r"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}
