package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"modelquota/internal/domain"
)

// newTestDB 创建一个独立的临时数据库并完成建表，同时写入测试所需的基础数据。
//
// 表结构启用了外键约束，因此请求、用量计数等记录都必须先有对应的租户与凭证。
func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	seedBase(t, db)
	return db
}

// seedBase 写入两个租户与三个凭证。
func seedBase(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	seedTenant(t, db, "t1", 10)
	seedTenant(t, db, "t2", 10)
	for _, c := range []struct{ id, tenant string }{
		{"c1", "t1"}, {"c2", "t1"}, {"c3", "t2"},
	} {
		err := db.CreateCredential(ctx, &domain.Credential{
			ID: c.id, TenantID: c.tenant, Name: c.id, KeyHash: domain.HashSecret(c.id),
			Status: domain.CredentialActive, CreatedAt: time.Now().UTC(), CreatedBy: "admin-a",
		})
		if err != nil {
			t.Fatalf("写入凭证 %s 失败: %v", c.id, err)
		}
	}
}

// seedTenant 写入一个租户。
func seedTenant(t *testing.T, db *DB, id string, quota int) {
	t.Helper()
	if err := db.CreateTenant(context.Background(), &domain.Tenant{
		ID: id, Name: "租户 " + id, Status: domain.TenantActive,
		DailyQuota: quota, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("写入租户失败: %v", err)
	}
}

// ---------- 租户 ----------

func TestTenantCRUD(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	got, err := db.GetTenant(ctx, "t1")
	if err != nil {
		t.Fatalf("读取租户失败: %v", err)
	}
	if got.Name != "租户 t1" || got.DailyQuota != 10 || got.Status != domain.TenantActive {
		t.Fatalf("读取到的租户不符: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Fatalf("创建时间不应为零值")
	}

	tenants, err := db.ListTenants(ctx)
	if err != nil {
		t.Fatalf("列出租户失败: %v", err)
	}
	// 基础数据里有 t1、t2 两个租户。
	if len(tenants) != 2 {
		t.Fatalf("租户数 = %d, 期望 2", len(tenants))
	}

	if err := db.UpdateTenantStatus(ctx, "t1", domain.TenantDisabled); err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}
	if err := db.UpdateTenantQuota(ctx, "t1", 20); err != nil {
		t.Fatalf("更新额度失败: %v", err)
	}
	got, _ = db.GetTenant(ctx, "t1")
	if got.Status != domain.TenantDisabled || got.DailyQuota != 20 {
		t.Fatalf("更新后租户不符: %+v", got)
	}
}

func TestTenantNotFound(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.GetTenant(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
	if err := db.UpdateTenantStatus(ctx, "missing", domain.TenantActive); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
	if err := db.UpdateTenantQuota(ctx, "missing", 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

func TestCreateTenantDuplicateFails(t *testing.T) {
	db := newTestDB(t)
	// t1 已由基础数据写入，重复写入应失败。
	if err := db.CreateTenant(context.Background(), &domain.Tenant{ID: "t1", Name: "重复", Status: domain.TenantActive}); err == nil {
		t.Fatalf("重复租户编号应写入失败")
	}
}

// ---------- 管理员 ----------

func TestAdminQueries(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	admin := &domain.Admin{
		ID: "a1", TenantID: "t1", Username: "admin-a",
		PasswordHash: domain.HashSecret("pw"), Role: domain.AdminTenant, CreatedAt: time.Now().UTC(),
	}
	if err := db.CreateAdmin(ctx, admin); err != nil {
		t.Fatalf("写入管理员失败: %v", err)
	}

	byName, err := db.GetAdminByUsername(ctx, "admin-a")
	if err != nil {
		t.Fatalf("按用户名读取失败: %v", err)
	}
	if byName.ID != "a1" || byName.Role != domain.AdminTenant {
		t.Fatalf("管理员信息不符: %+v", byName)
	}

	byID, err := db.GetAdminByID(ctx, "a1")
	if err != nil {
		t.Fatalf("按 ID 读取失败: %v", err)
	}
	if byID.Username != "admin-a" {
		t.Fatalf("按 ID 读取的管理员不符: %+v", byID)
	}

	list, err := db.ListAdminsByTenant(ctx, "t1")
	if err != nil {
		t.Fatalf("列出管理员失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("管理员数 = %d, 期望 1", len(list))
	}

	if _, err := db.GetAdminByUsername(ctx, "nobody"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
	if _, err := db.GetAdminByID(ctx, "nobody"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

// ---------- 凭证 ----------

func TestCredentialLifecycle(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	cred := &domain.Credential{
		ID: "c9", TenantID: "t1", Name: "app-1", KeyHash: domain.HashSecret("sk_9"),
		Status: domain.CredentialActive, CreatedAt: now, CreatedBy: "admin-a",
	}
	if err := db.CreateCredential(ctx, cred); err != nil {
		t.Fatalf("写入凭证失败: %v", err)
	}

	got, err := db.GetCredential(ctx, "c9")
	if err != nil {
		t.Fatalf("读取凭证失败: %v", err)
	}
	if got.Name != "app-1" || got.Status != domain.CredentialActive {
		t.Fatalf("凭证信息不符: %+v", got)
	}
	if got.DisabledAt != nil {
		t.Fatalf("未停用凭证不应有停用时间")
	}

	byKey, err := db.GetCredentialByKeyHash(ctx, domain.HashSecret("sk_9"))
	if err != nil {
		t.Fatalf("按摘要读取凭证失败: %v", err)
	}
	if byKey.ID != "c9" {
		t.Fatalf("按摘要读到的凭证不符: %+v", byKey)
	}

	if err := db.DisableCredential(ctx, "c9", "admin-a", "密钥泄露", now); err != nil {
		t.Fatalf("停用凭证失败: %v", err)
	}
	got, _ = db.GetCredential(ctx, "c9")
	if got.Status != domain.CredentialDisabled {
		t.Fatalf("停用后状态 = %s, 期望 disabled", got.Status)
	}
	if got.DisableWhy != "密钥泄露" || got.DisabledBy != "admin-a" {
		t.Fatalf("停用信息不符: %+v", got)
	}
	if got.DisabledAt == nil {
		t.Fatalf("停用时间不应为空")
	}

	if err := db.DisableCredential(ctx, "missing", "x", "y", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

func TestCountAndListCredentials(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	for i, id := range []string{"cx1", "cx2"} {
		if err := db.CreateCredential(ctx, &domain.Credential{
			ID: id, TenantID: "t1", Name: id, KeyHash: domain.HashSecret(id),
			Status: domain.CredentialActive, CreatedAt: time.Now().UTC().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("写入凭证失败: %v", err)
		}
	}
	if err := db.CreateCredential(ctx, &domain.Credential{
		ID: "cx3", TenantID: "t2", Name: "cx3", KeyHash: domain.HashSecret("cx3"),
		Status: domain.CredentialActive, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("写入凭证失败: %v", err)
	}

	n, err := db.CountCredentials(ctx, "t1")
	if err != nil {
		t.Fatalf("统计凭证失败: %v", err)
	}
	// 基础数据里 t1 已有 c1、c2，再加上 cx1、cx2 共 4 个。
	if n != 4 {
		t.Fatalf("t1 的凭证数 = %d, 期望 4", n)
	}

	list, err := db.ListCredentialsByTenant(ctx, "t2")
	if err != nil {
		t.Fatalf("列出凭证失败: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("t2 的凭证数 = %d, 期望 2", len(list))
	}
}

// ---------- 调用请求 ----------

func newRequest(t *testing.T, db *DB, id, tenantID, credID, requestNo, day string) {
	t.Helper()
	err := db.InsertRequest(context.Background(), &domain.ModelRequest{
		ID: id, TenantID: tenantID, CredentialID: credID, RequestNo: requestNo,
		ParamsHash: "hash-" + requestNo, Day: day, Status: domain.RequestProcessing,
		Model: "m", Prompt: "p", ReceivedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("写入请求失败: %v", err)
	}
}

func TestRequestInsertAndQueries(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	newRequest(t, db, "r1", "t1", "c1", "req-1", "2026-01-01")

	got, err := db.GetRequestByNo(ctx, "t1", "req-1")
	if err != nil {
		t.Fatalf("按编号读取失败: %v", err)
	}
	if got.ID != "r1" || got.Status != domain.RequestProcessing {
		t.Fatalf("请求信息不符: %+v", got)
	}

	byID, err := db.GetRequest(ctx, "r1")
	if err != nil {
		t.Fatalf("按 ID 读取失败: %v", err)
	}
	if byID.RequestNo != "req-1" {
		t.Fatalf("按 ID 读取的请求不符: %+v", byID)
	}

	if _, err := db.GetRequestByNo(ctx, "t1", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
	if _, err := db.GetRequest(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

// TestRequestNoUniquePerTenant 验证同一租户内请求编号唯一（R3）。
func TestRequestNoUniquePerTenant(t *testing.T) {
	db := newTestDB(t)
	newRequest(t, db, "r1", "t1", "c1", "req-1", "2026-01-01")
	err := db.InsertRequest(context.Background(), &domain.ModelRequest{
		ID: "r2", TenantID: "t1", CredentialID: "c1", RequestNo: "req-1",
		ParamsHash: "h", Day: "2026-01-02", Status: domain.RequestProcessing,
	})
	if err == nil {
		t.Fatalf("同一租户内重复请求编号应写入失败")
	}
}

func TestFinishRequestOnlyFromProcessing(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	newRequest(t, db, "r1", "t1", "c1", "req-1", "2026-01-01")

	if err := db.FinishRequest(ctx, "r1", domain.RequestSuccess, "输出", "", time.Now().UTC()); err != nil {
		t.Fatalf("结案失败: %v", err)
	}
	got, _ := db.GetRequest(ctx, "r1")
	if got.Status != domain.RequestSuccess || got.UpstreamResult != "输出" {
		t.Fatalf("结案后信息不符: %+v", got)
	}
	if got.CompletedAt == nil {
		t.Fatalf("完成时间不应为空")
	}

	// 终态不可再次迁移。
	if err := db.FinishRequest(ctx, "r1", domain.RequestFailed, "", "x", time.Now().UTC()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("已结案的请求不应再次迁移，实际 %v", err)
	}
	if err := db.FinishRequest(ctx, "missing", domain.RequestSuccess, "", "", time.Now().UTC()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

func TestIncrementReplay(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	newRequest(t, db, "r1", "t1", "c1", "req-1", "2026-01-01")
	for i := 0; i < 3; i++ {
		if err := db.IncrementReplay(ctx, "r1"); err != nil {
			t.Fatalf("记录幂等命中失败: %v", err)
		}
	}
	got, _ := db.GetRequest(ctx, "r1")
	if got.ReplayCount != 3 {
		t.Fatalf("幂等命中次数 = %d, 期望 3", got.ReplayCount)
	}
}

func TestCountAndListRequests(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	newRequest(t, db, "r1", "t1", "c1", "req-1", "2026-01-01")
	newRequest(t, db, "r2", "t1", "c2", "req-2", "2026-01-01")
	newRequest(t, db, "r3", "t2", "c3", "req-3", "2026-01-01")
	newRequest(t, db, "r4", "t1", "c1", "req-4", "2026-01-02")

	if err := db.FinishRequest(ctx, "r1", domain.RequestSuccess, "o", "", time.Now().UTC()); err != nil {
		t.Fatalf("结案失败: %v", err)
	}
	if err := db.FinishRequest(ctx, "r2", domain.RequestFailed, "", "上游失败", time.Now().UTC()); err != nil {
		t.Fatalf("结案失败: %v", err)
	}

	counts, err := db.CountRequestsByStatus(ctx, "t1", "2026-01-01")
	if err != nil {
		t.Fatalf("统计终态失败: %v", err)
	}
	if counts[domain.RequestSuccess] != 1 || counts[domain.RequestFailed] != 1 || counts[domain.RequestProcessing] != 0 {
		t.Fatalf("终态统计不符: %+v", counts)
	}

	list, err := db.ListRequestsByTenant(ctx, "t1", "2026-01-01")
	if err != nil {
		t.Fatalf("列出租户请求失败: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("t1 当日请求数 = %d, 期望 2", len(list))
	}

	credList, err := db.ListRequestsByCredential(ctx, "c1", "2026-01-01")
	if err != nil {
		t.Fatalf("列出凭证请求失败: %v", err)
	}
	if len(credList) != 1 || credList[0].ID != "r1" {
		t.Fatalf("凭证请求列表不符: %+v", credList)
	}
}

// ---------- 被拒绝的尝试 ----------

func TestRejectedAttempts(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		err := db.InsertRejectedAttempt(ctx, &domain.RejectedAttempt{
			ID: "rej" + string(rune('a'+i)), TenantID: "t1", CredentialID: "c1",
			RequestNo: "req-x", Day: "2026-01-01", Status: domain.RequestRejected,
			Reason: "当日额度已用尽", Actor: "credential:c1", OccurredAt: now,
		})
		if err != nil {
			t.Fatalf("写入被拒绝尝试失败: %v", err)
		}
	}

	n, err := db.CountRejectedAttempts(ctx, "t1", "2026-01-01")
	if err != nil {
		t.Fatalf("统计被拒绝尝试失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("被拒绝次数 = %d, 期望 2", n)
	}

	list, err := db.ListRejectedAttempts(ctx, "t1", "2026-01-01")
	if err != nil {
		t.Fatalf("列出被拒绝尝试失败: %v", err)
	}
	if len(list) != 2 || list[0].Reason != "当日额度已用尽" {
		t.Fatalf("被拒绝尝试列表不符: %+v", list)
	}

	// 空字段应写成 NULL，读回为空串。
	err = db.InsertRejectedAttempt(ctx, &domain.RejectedAttempt{
		ID: "rej-c", TenantID: "t1", Day: "2026-01-02", Status: domain.RequestRejected,
		Reason: "编号非法", Actor: "x", OccurredAt: now,
	})
	if err != nil {
		t.Fatalf("写入空字段记录失败: %v", err)
	}
	list, err = db.ListRejectedAttempts(ctx, "t1", "2026-01-02")
	if err != nil {
		t.Fatalf("列出被拒绝尝试失败: %v", err)
	}
	if list[0].CredentialID != "" || list[0].RequestNo != "" {
		t.Fatalf("空字段应读回空串: %+v", list[0])
	}
}

// ---------- 用量计数 ----------

func TestReserveInflightRespectsQuota(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	ok, err := db.ReserveInflight(ctx, "t1", "2026-01-01", 2)
	if err != nil || !ok {
		t.Fatalf("第一次占用应成功: ok=%v err=%v", ok, err)
	}
	ok, err = db.ReserveInflight(ctx, "t1", "2026-01-01", 2)
	if err != nil || !ok {
		t.Fatalf("第二次占用应成功: ok=%v err=%v", ok, err)
	}
	ok, err = db.ReserveInflight(ctx, "t1", "2026-01-01", 2)
	if err != nil {
		t.Fatalf("第三次占用不应报错: %v", err)
	}
	if ok {
		t.Fatalf("额度已满时第三次占用应失败")
	}

	usage, err := db.GetDayUsage(ctx, "t1", "2026-01-01", 2)
	if err != nil {
		t.Fatalf("读取用量失败: %v", err)
	}
	if usage.InFlight != 2 || usage.Success != 0 {
		t.Fatalf("用量不符: %+v", usage)
	}
	if usage.Remaining() != 0 {
		t.Fatalf("剩余额度 = %d, 期望 0", usage.Remaining())
	}
}

func TestCommitSuccessAndRelease(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.ReserveInflight(ctx, "t1", "2026-01-01", 5); err != nil {
		t.Fatalf("占用失败: %v", err)
	}
	if err := db.CommitSuccess(ctx, "t1", "2026-01-01"); err != nil {
		t.Fatalf("计入成功量失败: %v", err)
	}
	usage, _ := db.GetDayUsage(ctx, "t1", "2026-01-01", 5)
	if usage.Success != 1 || usage.InFlight != 0 {
		t.Fatalf("成功转换后用量不符: %+v", usage)
	}
	if usage.Occupied() != 1 {
		t.Fatalf("占用数 = %d, 期望 1", usage.Occupied())
	}

	if _, err := db.ReserveInflight(ctx, "t1", "2026-01-01", 5); err != nil {
		t.Fatalf("占用失败: %v", err)
	}
	if err := db.ReleaseInflight(ctx, "t1", "2026-01-01"); err != nil {
		t.Fatalf("释放额度失败: %v", err)
	}
	usage, _ = db.GetDayUsage(ctx, "t1", "2026-01-01", 5)
	if usage.Success != 1 || usage.InFlight != 0 {
		t.Fatalf("释放后用量不符: %+v", usage)
	}
}

func TestCommitSuccessWithoutInflightFails(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := db.CommitSuccess(ctx, "t1", "2026-01-01"); err == nil {
		t.Fatalf("没有处理中计数时计入成功量应失败")
	}
	if err := db.ReleaseInflight(ctx, "t1", "2026-01-01"); err == nil {
		t.Fatalf("没有处理中计数时释放额度应失败")
	}
}

func TestGetDayUsageWithoutRow(t *testing.T) {
	db := newTestDB(t)
	usage, err := db.GetDayUsage(context.Background(), "t1", "2026-01-01", 10)
	if err != nil {
		t.Fatalf("读取用量失败: %v", err)
	}
	if usage.Success != 0 || usage.InFlight != 0 || usage.QuotaLimit != 10 {
		t.Fatalf("无记录时的用量不符: %+v", usage)
	}
}

// TestReserveInflightConcurrentLastSlot 验证并发争抢最后一个额度名额时只有一次成功（R2）。
//
// 该用例配合 -race 运行，验证原子占用在并发下不超发。
func TestReserveInflightConcurrentLastSlot(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	const workers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			ok, err := db.ReserveInflight(ctx, "t1", "2026-01-01", 1)
			if err != nil {
				t.Errorf("并发占用出错: %v", err)
				return
			}
			if ok {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if granted != 1 {
		t.Fatalf("额度为 1 时并发占用成功数 = %d, 期望 1", granted)
	}
	usage, _ := db.GetDayUsage(ctx, "t1", "2026-01-01", 1)
	if usage.InFlight != 1 {
		t.Fatalf("处理中计数 = %d, 期望 1", usage.InFlight)
	}
}

// ---------- 配额变更与事件 ----------

func TestQuotaChangesAndEvents(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	err := db.InsertQuotaChange(ctx, &domain.QuotaChange{
		ID: "qc1", TenantID: "t1", OldQuota: 10, NewQuota: 20,
		Operator: "ops", Reason: "客户扩容", EffectiveAt: now, Day: "2026-01-01",
	})
	if err != nil {
		t.Fatalf("写入配额变更失败: %v", err)
	}
	changes, err := db.ListQuotaChanges(ctx, "t1", "2026-01-01")
	if err != nil {
		t.Fatalf("列出配额变更失败: %v", err)
	}
	if len(changes) != 1 || changes[0].NewQuota != 20 || changes[0].Reason != "客户扩容" {
		t.Fatalf("配额变更列表不符: %+v", changes)
	}

	err = db.InsertEvent(ctx, &domain.TenantEvent{
		ID: "ev1", TenantID: "t1", CredentialID: "c1", Kind: "credential_disabled",
		Actor: "admin-a", Reason: "密钥泄露", Detail: "停用凭证 app-1",
		Day: "2026-01-01", OccurredAt: now,
	})
	if err != nil {
		t.Fatalf("写入事件失败: %v", err)
	}
	events, err := db.ListEvents(ctx, "t1", "2026-01-01")
	if err != nil {
		t.Fatalf("列出事件失败: %v", err)
	}
	if len(events) != 1 || events[0].Kind != "credential_disabled" || events[0].CredentialID != "c1" {
		t.Fatalf("事件列表不符: %+v", events)
	}
}

func TestEventWithEmptyCredentialAndDetail(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	err := db.InsertEvent(ctx, &domain.TenantEvent{
		ID: "ev2", TenantID: "t1", Kind: "tenant_disabled",
		Actor: "ops", Reason: "欠费", Day: "2026-01-01", OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("写入事件失败: %v", err)
	}
	events, err := db.ListEvents(ctx, "t1", "2026-01-01")
	if err != nil {
		t.Fatalf("列出事件失败: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("事件数 = %d, 期望 1", len(events))
	}
	if events[0].CredentialID != "" || events[0].Detail != "" {
		t.Fatalf("空字段应读回空串: %+v", events[0])
	}
}

// ---------- 诊断 ----------

func TestDiagnostics(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	newRequest(t, db, "r1", "t1", "c1", "req-1", "2026-01-01")
	err := db.InsertDiagnostic(ctx, &domain.Diagnostic{
		ID: "d1", RequestID: "r1", TenantID: "t1", Kind: "late_result",
		Detail: "迟到结果", RecordedAt: now,
	})
	if err != nil {
		t.Fatalf("写入诊断失败: %v", err)
	}

	byTenant, err := db.ListDiagnosticsByTenant(ctx, "t1")
	if err != nil {
		t.Fatalf("列出租户诊断失败: %v", err)
	}
	if len(byTenant) != 1 || byTenant[0].Kind != "late_result" {
		t.Fatalf("租户诊断列表不符: %+v", byTenant)
	}

	byReq, err := db.ListDiagnosticsByRequest(ctx, "r1")
	if err != nil {
		t.Fatalf("列出请求诊断失败: %v", err)
	}
	if len(byReq) != 1 || byReq[0].Detail != "迟到结果" {
		t.Fatalf("请求诊断列表不符: %+v", byReq)
	}

	empty, err := db.ListDiagnosticsByRequest(ctx, "none")
	if err != nil {
		t.Fatalf("列出请求诊断失败: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("无诊断时长度 = %d, 期望 0", len(empty))
	}
}

// ---------- 会话 ----------

func TestSessionLifecycle(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	err := db.CreateSession(ctx, "tok1", string(domain.SubjectAdmin), "a1", "t1",
		string(domain.AdminTenant), now, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("写入会话失败: %v", err)
	}

	s, err := db.GetSession(ctx, "tok1")
	if err != nil {
		t.Fatalf("读取会话失败: %v", err)
	}
	if s.SubjectID != "a1" || s.TenantID != "t1" || s.Role != domain.AdminTenant {
		t.Fatalf("会话信息不符: %+v", s)
	}

	if err := db.DeleteSession(ctx, "tok1"); err != nil {
		t.Fatalf("删除会话失败: %v", err)
	}
	if _, err := db.GetSession(ctx, "tok1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

func TestSessionWithEmptyTenantAndRole(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	err := db.CreateSession(ctx, "tok2", string(domain.SubjectCredential), "c1", "", "",
		now, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("写入会话失败: %v", err)
	}
	s, err := db.GetSession(ctx, "tok2")
	if err != nil {
		t.Fatalf("读取会话失败: %v", err)
	}
	if s.TenantID != "" || s.Role != "" {
		t.Fatalf("空租户与空角色应读回空串: %+v", s)
	}
}

// ---------- 设置 ----------

func TestSettingsUpsert(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := db.SetSetting(ctx, "clock_offset_seconds", "0"); err != nil {
		t.Fatalf("写入设置失败: %v", err)
	}
	if err := db.SetSetting(ctx, "clock_offset_seconds", "3600"); err != nil {
		t.Fatalf("覆盖设置失败: %v", err)
	}
	v, err := db.GetSetting(ctx, "clock_offset_seconds")
	if err != nil {
		t.Fatalf("读取设置失败: %v", err)
	}
	if v != "3600" {
		t.Fatalf("设置值 = %s, 期望 3600", v)
	}
	if _, err := db.GetSetting(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，实际 %v", err)
	}
}

// ---------- 底层句柄 ----------

func TestSQLAndClose(t *testing.T) {
	db := newTestDB(t)
	if db.SQL() == nil {
		t.Fatalf("SQL() 不应为空")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("关闭数据库失败: %v", err)
	}
}

// TestNewWithRawSQLDB 验证可以用外部 *sql.DB 构造 DB（测试注入路径）。
func TestNewWithRawSQLDB(t *testing.T) {
	sdb, err := sql.Open("sqlite", "file:rawdb?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })

	db := New(sdb)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	seedTenant(t, db, "t9", 3)
	got, err := db.GetTenant(ctx, "t9")
	if err != nil {
		t.Fatalf("读取租户失败: %v", err)
	}
	if got.DailyQuota != 3 {
		t.Fatalf("额度 = %d, 期望 3", got.DailyQuota)
	}
}

// TestOperationsOnClosedDB 验证数据库关闭后各操作返回错误而不是 panic。
func TestOperationsOnClosedDB(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := db.Close(); err != nil {
		t.Fatalf("关闭数据库失败: %v", err)
	}

	if err := db.Migrate(ctx); err == nil {
		t.Fatalf("关闭后建表应报错")
	}
	if _, err := db.GetCredential(ctx, "c1"); err == nil {
		t.Fatalf("关闭后读取凭证应报错")
	}
	if _, err := db.GetCredentialByKeyHash(ctx, domain.HashSecret("c1")); err == nil {
		t.Fatalf("关闭后按摘要读取凭证应报错")
	}
	if err := db.IncrementReplay(ctx, "r1"); err == nil {
		t.Fatalf("关闭后记录幂等命中应报错")
	}
	if _, err := db.ReserveInflight(ctx, "t1", "2026-01-01", 1); err == nil {
		t.Fatalf("关闭后占用额度应报错")
	}
	if err := db.DeleteSession(ctx, "tok"); err == nil {
		t.Fatalf("关闭后删除会话应报错")
	}
	if _, err := db.GetTenant(ctx, "t1"); err == nil {
		t.Fatalf("关闭后读取租户应报错")
	}
	if _, err := db.ListTenants(ctx); err == nil {
		t.Fatalf("关闭后列出租户应报错")
	}
	if _, err := db.ListCredentialsByTenant(ctx, "t1"); err == nil {
		t.Fatalf("关闭后列出凭证应报错")
	}
	if _, err := db.CountCredentials(ctx, "t1"); err == nil {
		t.Fatalf("关闭后统计凭证应报错")
	}
	if _, err := db.GetDayUsage(ctx, "t1", "2026-01-01", 1); err == nil {
		t.Fatalf("关闭后读取用量应报错")
	}
	if _, err := db.CountRequestsByStatus(ctx, "t1", "2026-01-01"); err == nil {
		t.Fatalf("关闭后统计终态应报错")
	}
	if _, err := db.ListRequestsByTenant(ctx, "t1", "2026-01-01"); err == nil {
		t.Fatalf("关闭后列出请求应报错")
	}
	if _, err := db.ListRequestsByCredential(ctx, "c1", "2026-01-01"); err == nil {
		t.Fatalf("关闭后列出凭证请求应报错")
	}
	if _, err := db.CountRejectedAttempts(ctx, "t1", "2026-01-01"); err == nil {
		t.Fatalf("关闭后统计被拒绝尝试应报错")
	}
	if _, err := db.ListRejectedAttempts(ctx, "t1", "2026-01-01"); err == nil {
		t.Fatalf("关闭后列出被拒绝尝试应报错")
	}
	if _, err := db.ListQuotaChanges(ctx, "t1", "2026-01-01"); err == nil {
		t.Fatalf("关闭后列出配额变更应报错")
	}
	if _, err := db.ListEvents(ctx, "t1", "2026-01-01"); err == nil {
		t.Fatalf("关闭后列出事件应报错")
	}
	if _, err := db.ListDiagnosticsByTenant(ctx, "t1"); err == nil {
		t.Fatalf("关闭后列出诊断应报错")
	}
	if _, err := db.ListDiagnosticsByRequest(ctx, "r1"); err == nil {
		t.Fatalf("关闭后列出请求诊断应报错")
	}
	if _, err := db.GetAdminByUsername(ctx, "admin-a"); err == nil {
		t.Fatalf("关闭后按用户名读取管理员应报错")
	}
	if _, err := db.GetAdminByID(ctx, "a1"); err == nil {
		t.Fatalf("关闭后按 ID 读取管理员应报错")
	}
	if _, err := db.ListAdminsByTenant(ctx, "t1"); err == nil {
		t.Fatalf("关闭后列出管理员应报错")
	}
	if _, err := db.GetSession(ctx, "tok"); err == nil {
		t.Fatalf("关闭后读取会话应报错")
	}
	if _, err := db.GetSetting(ctx, "k"); err == nil {
		t.Fatalf("关闭后读取设置应报错")
	}
	if err := db.SetSetting(ctx, "k", "v"); err == nil {
		t.Fatalf("关闭后写入设置应报错")
	}
	if err := db.CreateTenant(ctx, &domain.Tenant{ID: "x", Name: "x"}); err == nil {
		t.Fatalf("关闭后写入租户应报错")
	}
	if err := db.CreateAdmin(ctx, &domain.Admin{ID: "x", Username: "x"}); err == nil {
		t.Fatalf("关闭后写入管理员应报错")
	}
	if err := db.CreateCredential(ctx, &domain.Credential{ID: "x", TenantID: "t1", Name: "x", KeyHash: "x", CreatedBy: "x"}); err == nil {
		t.Fatalf("关闭后写入凭证应报错")
	}
	if err := db.InsertRequest(ctx, &domain.ModelRequest{ID: "x", TenantID: "t1", CredentialID: "c1", RequestNo: "x", ParamsHash: "x", Day: "d", Status: domain.RequestProcessing, Model: "m", Prompt: "p"}); err == nil {
		t.Fatalf("关闭后写入请求应报错")
	}
	if err := db.FinishRequest(ctx, "x", domain.RequestSuccess, "", "", time.Now().UTC()); err == nil {
		t.Fatalf("关闭后结案应报错")
	}
	if err := db.DisableCredential(ctx, "c1", "a", "r", time.Now().UTC()); err == nil {
		t.Fatalf("关闭后停用凭证应报错")
	}
	if err := db.UpdateTenantStatus(ctx, "t1", domain.TenantActive); err == nil {
		t.Fatalf("关闭后更新租户状态应报错")
	}
	if err := db.UpdateTenantQuota(ctx, "t1", 1); err == nil {
		t.Fatalf("关闭后更新租户额度应报错")
	}
	if err := db.InsertQuotaChange(ctx, &domain.QuotaChange{ID: "x", TenantID: "t1", Day: "d"}); err == nil {
		t.Fatalf("关闭后写入配额变更应报错")
	}
	if err := db.InsertEvent(ctx, &domain.TenantEvent{ID: "x", TenantID: "t1", Kind: "k", Actor: "a", Reason: "r", Day: "d"}); err == nil {
		t.Fatalf("关闭后写入事件应报错")
	}
	if err := db.InsertDiagnostic(ctx, &domain.Diagnostic{ID: "x", RequestID: "r1", TenantID: "t1", Kind: "k", Detail: "d"}); err == nil {
		t.Fatalf("关闭后写入诊断应报错")
	}
	if err := db.InsertRejectedAttempt(ctx, &domain.RejectedAttempt{ID: "x", TenantID: "t1", Day: "d", Reason: "r", Actor: "a"}); err == nil {
		t.Fatalf("关闭后写入被拒绝尝试应报错")
	}
	if err := db.CreateSession(ctx, "tok", "admin", "a1", "t1", "ops", time.Now().UTC(), time.Now().UTC()); err == nil {
		t.Fatalf("关闭后写入会话应报错")
	}
	if err := db.EnsureDayUsage(ctx, "t1", "d"); err == nil {
		t.Fatalf("关闭后初始化用量计数应报错")
	}
	if err := db.CommitSuccess(ctx, "t1", "d"); err == nil {
		t.Fatalf("关闭后计入成功量应报错")
	}
	if err := db.ReleaseInflight(ctx, "t1", "d"); err == nil {
		t.Fatalf("关闭后释放额度应报错")
	}
}
