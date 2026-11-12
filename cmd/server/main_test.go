package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"modelquota/internal/domain"
	"modelquota/internal/store"
)

func TestParseInt(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"0", 0, false},
		{"10", 10, false},
		{" 42 ", 42, false},
		{"-5", -5, false},
		{"", 0, true},
		{"abc", 0, true},
		{"1.5", 0, true},
	}
	for _, c := range cases {
		got, err := parseInt(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("parseInt(%q) 应报错", c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseInt(%q) 报错: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("parseInt(%q) = %d, 期望 %d", c.in, got, c.want)
		}
	}
}

func newTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "seed.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return db
}

func TestSeedDataCreatesTenantsAndAdmins(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := seedData(ctx, db); err != nil {
		t.Fatalf("写入预置数据失败: %v", err)
	}

	for _, id := range []string{"tenant-a", "tenant-b"} {
		tenant, err := db.GetTenant(ctx, id)
		if err != nil {
			t.Fatalf("读取租户 %s 失败: %v", id, err)
		}
		if tenant.Status != domain.TenantActive {
			t.Fatalf("租户 %s 状态 = %s, 期望 active", id, tenant.Status)
		}
		if tenant.DailyQuota != 10 {
			t.Fatalf("租户 %s 额度 = %d, 期望 10（题面预置条件）", id, tenant.DailyQuota)
		}
	}

	// 两个客户管理员分属两个租户（确认结论 Q1）。
	adminA, err := db.GetAdminByUsername(ctx, "admin-a")
	if err != nil {
		t.Fatalf("读取管理员 admin-a 失败: %v", err)
	}
	adminB, err := db.GetAdminByUsername(ctx, "admin-b")
	if err != nil {
		t.Fatalf("读取管理员 admin-b 失败: %v", err)
	}
	if adminA.TenantID != "tenant-a" || adminB.TenantID != "tenant-b" {
		t.Fatalf("管理员租户归属不符: %s / %s", adminA.TenantID, adminB.TenantID)
	}
	if adminA.Role != domain.AdminTenant || adminB.Role != domain.AdminTenant {
		t.Fatalf("管理员角色不符")
	}

	// 运营账号没有租户归属，且口令可校验。
	ops, err := db.GetAdminByUsername(ctx, "ops")
	if err != nil {
		t.Fatalf("读取运营账号失败: %v", err)
	}
	if ops.Role != domain.AdminOps {
		t.Fatalf("运营角色 = %s", ops.Role)
	}
	if ops.TenantID != "" {
		t.Fatalf("运营账号不应绑定租户，实际 %s", ops.TenantID)
	}
	if ops.PasswordHash != domain.HashSecret("ops-secret") {
		t.Fatalf("运营账号口令摘要不符")
	}

	// 每个租户都应有初始配额变更留痕。
	changes, err := db.ListQuotaChanges(ctx, "tenant-a", domain.DayOf(time.Now().UTC()))
	if err != nil {
		t.Fatalf("列出配额变更失败: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("配额变更条数 = %d, 期望 1", len(changes))
	}
}

func TestSeedDataIsIdempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := seedData(ctx, db); err != nil {
		t.Fatalf("首次写入预置数据失败: %v", err)
	}
	if err := seedData(ctx, db); err != nil {
		t.Fatalf("重复写入预置数据应幂等: %v", err)
	}

	tenants, err := db.ListTenants(ctx)
	if err != nil {
		t.Fatalf("列出租户失败: %v", err)
	}
	if len(tenants) != 2 {
		t.Fatalf("租户数 = %d, 期望 2", len(tenants))
	}
}

func TestSeedDataRepairsQuota(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := seedData(ctx, db); err != nil {
		t.Fatalf("首次写入预置数据失败: %v", err)
	}
	// 模拟额度被改过，再次 seed 应恢复为题面要求的 10。
	if err := db.UpdateTenantQuota(ctx, "tenant-a", 99); err != nil {
		t.Fatalf("修改额度失败: %v", err)
	}
	if err := seedData(ctx, db); err != nil {
		t.Fatalf("重复写入预置数据失败: %v", err)
	}
	tenant, err := db.GetTenant(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("读取租户失败: %v", err)
	}
	if tenant.DailyQuota != 10 {
		t.Fatalf("额度 = %d, 期望恢复为 10", tenant.DailyQuota)
	}
}

func TestRunMainRejectsUnknownFlag(t *testing.T) {
	if err := runMain([]string{"--不存在的参数"}); err == nil {
		t.Fatalf("未知参数应返回错误")
	}
}

func TestRunMainRejectsInvalidAddress(t *testing.T) {
	err := runMain([]string{
		"-addr", "127.0.0.1:99999",
		"-db", filepath.Join(t.TempDir(), "x.db"),
		"-upstream", "http://127.0.0.1:1",
	})
	if err == nil {
		t.Fatalf("非法监听地址应返回错误")
	}
}

func TestRunRejectsInvalidAddress(t *testing.T) {
	err := run("127.0.0.1:99999", filepath.Join(t.TempDir(), "x.db"), "http://127.0.0.1:1", false, true)
	if err == nil {
		t.Fatalf("非法监听地址应返回错误")
	}
}

func TestRunCreatesDatabaseDirectory(t *testing.T) {
	base := t.TempDir()
	dbPath := filepath.Join(base, "nested", "dir", "app.db")
	err := run("127.0.0.1:99999", dbPath, "http://127.0.0.1:1", false, true)
	if err == nil {
		t.Fatalf("非法监听地址应返回错误")
	}
	if _, statErr := filepath.Glob(filepath.Join(base, "nested", "dir", "app.db")); statErr != nil {
		t.Fatalf("应创建数据库目录: %v", statErr)
	}
}

// TestBuildServerWiresEverything 验证服务装配：建库、预置数据、路由可用。
func TestBuildServerWiresEverything(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.db")
	db, svc, srv, err := buildServer("127.0.0.1:18080", dbPath, "http://127.0.0.1:1", true, true)
	if err != nil {
		t.Fatalf("装配服务失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if svc == nil || srv == nil {
		t.Fatalf("装配结果不应为空")
	}
	if !svc.EnableTestHooks {
		t.Fatalf("应开启测试钩子")
	}
	if srv.Addr != "127.0.0.1:18080" {
		t.Fatalf("监听地址 = %s", srv.Addr)
	}

	// 健康检查可用。
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("健康检查状态码 = %d", rec.Code)
	}

	// 预置数据可用。
	if _, err := db.GetAdminByUsername(context.Background(), "ops"); err != nil {
		t.Fatalf("预置运营账号缺失: %v", err)
	}
}

// TestBuildServerWithoutSeed 验证不写入预置数据时不报错。
func TestBuildServerWithoutSeed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.db")
	db, _, _, err := buildServer("127.0.0.1:18081", dbPath, "http://127.0.0.1:1", false, false)
	if err != nil {
		t.Fatalf("装配服务失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.GetAdminByUsername(context.Background(), "ops"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("未写入预置数据时不应存在运营账号，实际 %v", err)
	}
}

// TestBuildServerRestoresClockOffset 验证重启后恢复教学时钟偏移。
func TestBuildServerRestoresClockOffset(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := db.SetSetting(context.Background(), "clock_offset_seconds", "3600"); err != nil {
		t.Fatalf("写入设置失败: %v", err)
	}
	_ = db.Close()

	db2, svc, _, err := buildServer("127.0.0.1:18082", dbPath, "http://127.0.0.1:1", false, true)
	if err != nil {
		t.Fatalf("装配服务失败: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })

	offset := svc.Clock().Now().Sub(time.Now().UTC())
	if offset < 59*time.Minute || offset > 61*time.Minute {
		t.Fatalf("教学时钟偏移应恢复为 1 小时，实际 %v", offset)
	}
}
