package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"modelquota/internal/domain"
)

// ---------- 身份判定 ----------

func TestIdentityPredicates(t *testing.T) {
	ops := Identity{SubjectType: domain.SubjectAdmin, Role: domain.AdminOps}
	admin := Identity{SubjectType: domain.SubjectAdmin, Role: domain.AdminTenant}
	cred := Identity{SubjectType: domain.SubjectCredential}

	if !ops.IsOps() || ops.IsTenantAdmin() || ops.IsCredential() {
		t.Fatalf("运营身份判定错误")
	}
	if admin.IsOps() || !admin.IsTenantAdmin() || admin.IsCredential() {
		t.Fatalf("租户管理员身份判定错误")
	}
	if cred.IsOps() || cred.IsTenantAdmin() || !cred.IsCredential() {
		t.Fatalf("凭证身份判定错误")
	}
}

// ---------- 管理员登录 ----------

func TestLoginAdminSuccess(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	token, identity, err := svc.LoginAdmin(ctx, "admin-a", "admin-a-secret")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if token == "" {
		t.Fatalf("应签发会话令牌")
	}
	if identity.SubjectType != domain.SubjectAdmin || identity.Role != domain.AdminTenant {
		t.Fatalf("身份信息不符: %+v", identity)
	}
	if identity.TenantID != testTenantA {
		t.Fatalf("身份租户 = %s, 期望 %s", identity.TenantID, testTenantA)
	}
	if identity.SubjectID == "" || identity.Username != "admin-a" {
		t.Fatalf("身份信息不完整: %+v", identity)
	}
}

func TestLoginAdminOpsHasNoTenant(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, identity, err := svc.LoginAdmin(context.Background(), "ops", "ops-secret")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if !identity.IsOps() {
		t.Fatalf("应为运营身份: %+v", identity)
	}
	if identity.TenantID != "" {
		t.Fatalf("运营不应绑定租户，实际 %s", identity.TenantID)
	}
}

func TestLoginAdminRejectsBadCredentials(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if _, _, err := svc.LoginAdmin(ctx, "admin-a", "错误口令"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("期望 ErrUnauthorized，实际 %v", err)
	}
	if _, _, err := svc.LoginAdmin(ctx, "nobody", "admin-a-secret"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("期望 ErrUnauthorized，实际 %v", err)
	}
	if _, _, err := svc.LoginAdmin(ctx, "  ", "x"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("期望 ErrUnauthorized，实际 %v", err)
	}
}

// ---------- 凭证登录 ----------

func TestLoginCredentialSuccess(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	token, identity, err := svc.LoginCredential(ctx, "cred-a1")
	if err != nil {
		t.Fatalf("凭证登录失败: %v", err)
	}
	if token == "" {
		t.Fatalf("应签发会话令牌")
	}
	if !identity.IsCredential() {
		t.Fatalf("应为凭证身份: %+v", identity)
	}
	if identity.TenantID != testTenantA || identity.SubjectID != "cred-a1" {
		t.Fatalf("凭证身份信息不符: %+v", identity)
	}
	if identity.DisplayName != "cred-a1" {
		t.Fatalf("展示名不符: %+v", identity)
	}
}

func TestLoginCredentialRejectsUnknownSecret(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, _, err := svc.LoginCredential(context.Background(), "sk_不存在"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("期望 ErrUnauthorized，实际 %v", err)
	}
}

// ---------- 会话解析 ----------

func TestAuthenticateResolvesIdentity(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	token, _, err := svc.LoginAdmin(ctx, "admin-a", "admin-a-secret")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	identity, err := svc.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("解析会话失败: %v", err)
	}
	if identity.Username != "admin-a" || identity.TenantID != testTenantA {
		t.Fatalf("解析出的身份不符: %+v", identity)
	}
}

func TestAuthenticateCredentialIdentity(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	token, _, err := svc.LoginCredential(ctx, "cred-a1")
	if err != nil {
		t.Fatalf("凭证登录失败: %v", err)
	}
	identity, err := svc.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("解析会话失败: %v", err)
	}
	if identity.Username != "cred-a1" || identity.DisplayName != "cred-a1" {
		t.Fatalf("凭证展示名不符: %+v", identity)
	}
	if identity.Role != "" {
		t.Fatalf("凭证不应有管理角色，实际 %s", identity.Role)
	}
}

func TestAuthenticateRejectsMissingOrUnknownToken(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.Authenticate(ctx, ""); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("期望 ErrUnauthorized，实际 %v", err)
	}
	if _, err := svc.Authenticate(ctx, "   "); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("期望 ErrUnauthorized，实际 %v", err)
	}
	if _, err := svc.Authenticate(ctx, "不存在的令牌"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("期望 ErrUnauthorized，实际 %v", err)
	}
}

func TestAuthenticateRejectsExpiredSession(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	token, _, err := svc.LoginAdmin(ctx, "admin-a", "admin-a-secret")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	// 会话有效期 12 小时，把时钟往后推 13 小时。
	svc.SetClockOffset(13 * time.Hour)
	if _, err := svc.Authenticate(ctx, token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("过期会话应被拒绝，实际 %v", err)
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	token, _, err := svc.LoginAdmin(ctx, "admin-a", "admin-a-secret")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if err := svc.Logout(ctx, token); err != nil {
		t.Fatalf("登出失败: %v", err)
	}
	if _, err := svc.Authenticate(ctx, token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("登出后令牌应失效，实际 %v", err)
	}
}

// ---------- 授权（R7） ----------

func TestAuthorizeTenantAccess(t *testing.T) {
	ops := Identity{SubjectType: domain.SubjectAdmin, Role: domain.AdminOps}
	admin := Identity{SubjectType: domain.SubjectAdmin, Role: domain.AdminTenant, TenantID: testTenantA}
	cred := Identity{SubjectType: domain.SubjectCredential, TenantID: testTenantA}

	if err := AuthorizeTenantAccess(ops, testTenantB); err != nil {
		t.Fatalf("运营应可跨租户核查: %v", err)
	}
	if err := AuthorizeTenantAccess(admin, testTenantA); err != nil {
		t.Fatalf("租户管理员应可访问本租户: %v", err)
	}
	if err := AuthorizeTenantAccess(admin, testTenantB); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("期望 ErrForbidden，实际 %v", err)
	}
	if err := AuthorizeTenantAccess(cred, testTenantA); err != nil {
		t.Fatalf("应用凭证应可访问本租户: %v", err)
	}
	if err := AuthorizeTenantAccess(cred, testTenantB); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("期望 ErrForbidden，实际 %v", err)
	}
	// 没有租户归属的身份一律拒绝。
	if err := AuthorizeTenantAccess(admin, ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("期望 ErrForbidden，实际 %v", err)
	}
}

func TestAuthorizeOps(t *testing.T) {
	ops := Identity{SubjectType: domain.SubjectAdmin, Role: domain.AdminOps}
	admin := Identity{SubjectType: domain.SubjectAdmin, Role: domain.AdminTenant, TenantID: testTenantA}
	cred := Identity{SubjectType: domain.SubjectCredential, TenantID: testTenantA}

	if err := AuthorizeOps(ops); err != nil {
		t.Fatalf("运营应通过校验: %v", err)
	}
	if err := AuthorizeOps(admin); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("期望 ErrForbidden，实际 %v", err)
	}
	if err := AuthorizeOps(cred); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("期望 ErrForbidden，实际 %v", err)
	}
}

// ---------- 身份不可由请求参数伪造 ----------

// TestIdentityComesFromServerSideSession 验证租户归属只来自服务端签发的会话。
func TestIdentityComesFromServerSideSession(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	// 客户管理员登录后，其身份里的租户只能是自己的租户。
	_, identity, err := svc.LoginAdmin(ctx, "admin-a", "admin-a-secret")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if identity.TenantID != testTenantA {
		t.Fatalf("身份租户 = %s, 期望 %s", identity.TenantID, testTenantA)
	}
	// 运营没有租户归属，因此对任意租户的核查都必须显式声明租户。
	_, opsIdentity, err := svc.LoginAdmin(ctx, "ops", "ops-secret")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if opsIdentity.TenantID != "" {
		t.Fatalf("运营不应有租户归属")
	}
}
