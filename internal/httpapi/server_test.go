package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"modelquota/internal/domain"
	"modelquota/internal/service"
	"modelquota/internal/store"
	"modelquota/internal/upstream"
)

// ---------- 测试替身与夹具 ----------

// fakeUpstream 是可控的上游替身。
type fakeUpstream struct {
	mu      sync.Mutex
	calls   int
	failure error
}

func (f *fakeUpstream) Complete(ctx context.Context, req upstream.Request, timeout time.Duration) (*upstream.Response, error) {
	f.mu.Lock()
	f.calls++
	failure := f.failure
	f.mu.Unlock()
	if failure != nil {
		return nil, failure
	}
	return &upstream.Response{Content: "模拟输出", UpstreamID: "up-1"}, nil
}

func (f *fakeUpstream) Name() string { return "fake-upstream" }

const (
	tenantA = "tenant-a"
	tenantB = "tenant-b"
)

var testNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// newTestServer 构造带预置数据的服务与测试服务器。
func newTestServer(t *testing.T) (*Server, *fakeUpstream) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	for _, id := range []string{tenantA, tenantB} {
		if err := db.CreateTenant(ctx, &domain.Tenant{
			ID: id, Name: "租户 " + id, Status: domain.TenantActive,
			DailyQuota: 10, CreatedAt: testNow,
		}); err != nil {
			t.Fatalf("写入租户失败: %v", err)
		}
	}
	if err := db.CreateCredential(ctx, &domain.Credential{
		ID: "cred-a1", TenantID: tenantA, Name: "app-a1", KeyHash: domain.HashSecret("sk-a1"),
		Status: domain.CredentialActive, CreatedAt: testNow, CreatedBy: "admin-a",
	}); err != nil {
		t.Fatalf("写入凭证失败: %v", err)
	}
	for _, a := range []struct{ id, tenant, user, role string }{
		{"admin-a", tenantA, "admin-a", string(domain.AdminTenant)},
		{"admin-b", tenantB, "admin-b", string(domain.AdminTenant)},
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
	svc := service.New(db, fixedClock{t: testNow}, up)
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("加载模板失败: %v", err)
	}
	return New(svc, tmpl), up
}

// fixedClock 是可控时钟。
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// do 发起一次 JSON 请求并返回响应记录。
func do(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		reader = strings.NewReader(string(buf))
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// doForm 发起一次表单请求（模拟页面表单提交）。
func doForm(t *testing.T, h http.Handler, method, path, token string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// login 以管理员身份登录并返回令牌。
func login(t *testing.T, h http.Handler, username, password string) string {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"username": username, "password": password})
	if rec.Code != http.StatusOK {
		t.Fatalf("登录失败: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析登录响应失败: %v", err)
	}
	return out.Token
}

// decodeJSONBody 解析响应 JSON。
func decodeJSONBody(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
		t.Fatalf("解析响应失败: %v（响应 %s）", err, rec.Body.String())
	}
}

// ---------- 健康检查 ----------

func TestHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := do(t, srv.Handler(), http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("健康检查状态码 = %d", rec.Code)
	}
	var out map[string]any
	decodeJSONBody(t, rec, &out)
	if out["status"] != "ok" {
		t.Fatalf("健康检查响应 = %v", out)
	}
	if rec := do(t, srv.Handler(), http.MethodGet, "/readyz", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("就绪检查状态码 = %d", rec.Code)
	}
}

// ---------- 认证 ----------

func TestLoginAndWhoami(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()

	token := login(t, h, "admin-a", "admin-a-secret")
	rec := do(t, h, http.MethodGet, "/api/v1/auth/whoami", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("whoami 状态码 = %d", rec.Code)
	}
	var out map[string]any
	decodeJSONBody(t, rec, &out)
	if out["tenant_id"] != tenantA {
		t.Fatalf("身份租户 = %v, 期望 %s", out["tenant_id"], tenantA)
	}
	if out["role"] != string(domain.AdminTenant) {
		t.Fatalf("身份角色 = %v", out["role"])
	}
}

func TestLoginWithCredentialSecret(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"secret": "sk-a1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("凭证登录状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Identity map[string]any `json:"identity"`
	}
	decodeJSONBody(t, rec, &out)
	if out.Identity["subject_type"] != string(domain.SubjectCredential) {
		t.Fatalf("主体类型 = %v", out.Identity["subject_type"])
	}
}

func TestLoginRejectsBadPassword(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := do(t, srv.Handler(), http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"username": "admin-a", "password": "错误"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误口令状态码 = %d", rec.Code)
	}
}

func TestLoginRejectsBadJSON(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON 状态码 = %d", rec.Code)
	}
}

func TestLoginRejectsUnknownField(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := do(t, srv.Handler(), http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"username": "admin-a", "password": "x", "tenant_id": "tenant-b"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知字段应被拒绝，状态码 = %d", rec.Code)
	}
}

func TestLogout(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")

	rec := do(t, h, http.MethodPost, "/api/v1/auth/logout", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("登出状态码 = %d", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/auth/whoami", token, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("登出后令牌应失效，状态码 = %d", rec.Code)
	}
}

func TestUnauthenticatedRequestRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := do(t, srv.Handler(), http.MethodGet, "/api/v1/ops/tenants", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未认证状态码 = %d", rec.Code)
	}
	rec = do(t, srv.Handler(), http.MethodGet, "/api/v1/ops/tenants", "无效令牌", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无效令牌状态码 = %d", rec.Code)
	}
}

// ---------- 运营接口（R1/R5/R6/R7） ----------

func TestOpsListTenants(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	rec := do(t, h, http.MethodGet, "/api/v1/ops/tenants", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Tenants []domain.Tenant `json:"tenants"`
	}
	decodeJSONBody(t, rec, &out)
	if len(out.Tenants) != 2 {
		t.Fatalf("租户数 = %d, 期望 2", len(out.Tenants))
	}
}

func TestOpsOnlyRejectsTenantAdmin(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")
	rec := do(t, h, http.MethodGet, "/api/v1/ops/tenants", token, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("租户管理员访问运营接口应被拒绝，状态码 = %d", rec.Code)
	}
}

func TestOpsOpenTenant(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	rec := do(t, h, http.MethodPost, "/api/v1/ops/tenants/open", token,
		map[string]any{"name": "企业丙", "daily_quota": 20, "reason": "新签"})
	if rec.Code != http.StatusOK {
		t.Fatalf("开通租户状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Tenant domain.Tenant `json:"tenant"`
	}
	decodeJSONBody(t, rec, &out)
	if out.Tenant.Status != domain.TenantActive || out.Tenant.DailyQuota != 20 {
		t.Fatalf("开通结果不符: %+v", out.Tenant)
	}
}

func TestOpsOpenTenantRejectsInvalidQuota(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	rec := do(t, h, http.MethodPost, "/api/v1/ops/tenants/open", token,
		map[string]any{"name": "企业丙", "daily_quota": 999999})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("超额度应返回 400，状态码 = %d", rec.Code)
	}
}

func TestOpsAdjustQuota(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	rec := do(t, h, http.MethodPost, "/api/v1/ops/tenants/quota", token,
		map[string]any{"tenant_id": tenantA, "daily_quota": 30, "reason": "扩容"})
	if rec.Code != http.StatusOK {
		t.Fatalf("调整额度状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	// 原因必填。
	rec = do(t, h, http.MethodPost, "/api/v1/ops/tenants/quota", token,
		map[string]any{"tenant_id": tenantA, "daily_quota": 30})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺原因应返回 400，状态码 = %d", rec.Code)
	}
}

func TestOpsDisableTenant(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	rec := do(t, h, http.MethodPost, "/api/v1/ops/tenants/disable", token,
		map[string]any{"tenant_id": tenantA, "reason": "欠费"})
	if rec.Code != http.StatusOK {
		t.Fatalf("停用租户状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	// 原因必填。
	rec = do(t, h, http.MethodPost, "/api/v1/ops/tenants/disable", token,
		map[string]any{"tenant_id": tenantA})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺原因应返回 400，状态码 = %d", rec.Code)
	}
}

func TestOpsDailyReportRequiresTenantScope(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	// 运营跨租户核查必须显式指定租户。
	rec := do(t, h, http.MethodGet, "/api/v1/ops/report/daily", token, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("未指定租户应报错，状态码 = %d", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/ops/report/daily?tenant_id="+tenantA, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("指定租户后状态码 = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOpsClock(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	rec := do(t, h, http.MethodPost, "/api/v1/ops/clock", token, map[string]any{"offset_seconds": 3600})
	if rec.Code != http.StatusOK {
		t.Fatalf("设置时钟状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	decodeJSONBody(t, rec, &out)
	if out["offset_seconds"].(float64) != 3600 {
		t.Fatalf("时钟偏移 = %v", out["offset_seconds"])
	}
}

func TestOpsDiagnostics(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	rec := do(t, h, http.MethodGet, "/api/v1/ops/diagnostics?tenant_id="+tenantA, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询诊断状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Diagnostics []domain.Diagnostic `json:"diagnostics"`
	}
	decodeJSONBody(t, rec, &out)
	if len(out.Diagnostics) != 0 {
		t.Fatalf("无诊断记录时长度 = %d", len(out.Diagnostics))
	}
}

func TestOpsMethodsMustBePost(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")
	for _, path := range []string{
		"/api/v1/ops/tenants/open", "/api/v1/ops/tenants/quota",
		"/api/v1/ops/tenants/disable", "/api/v1/ops/clock",
	} {
		rec := do(t, h, http.MethodGet, path, token, nil)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s 的 GET 应返回 405，实际 %d", path, rec.Code)
		}
	}
}

// ---------- 管理员接口（R1/R6/R7） ----------

func TestAdminListCredentials(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")

	rec := do(t, h, http.MethodGet, "/api/v1/admin/credentials", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Credentials []domain.Credential `json:"credentials"`
	}
	decodeJSONBody(t, rec, &out)
	if len(out.Credentials) != 1 {
		t.Fatalf("凭证数 = %d, 期望 1", len(out.Credentials))
	}
}

func TestAdminCreateCredential(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")

	rec := do(t, h, http.MethodPost, "/api/v1/admin/credentials/create", token,
		map[string]any{"name": "app-a2"})
	if rec.Code != http.StatusOK {
		t.Fatalf("创建凭证状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Credential domain.Credential `json:"credential"`
		Secret     string            `json:"secret"`
	}
	decodeJSONBody(t, rec, &out)
	if out.Secret == "" {
		t.Fatalf("应返回密钥")
	}
	if out.Credential.TenantID != tenantA {
		t.Fatalf("凭证租户 = %s", out.Credential.TenantID)
	}
}

// TestAdminCreateCredentialRejectsCrossTenant 验证不能把凭证创建到其他租户（R1）。
func TestAdminCreateCredentialRejectsCrossTenant(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")

	rec := do(t, h, http.MethodPost, "/api/v1/admin/credentials/create", token,
		map[string]any{"name": "恶意凭证", "tenant_id": tenantB})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("越权创建应返回 400，状态码 = %d", rec.Code)
	}
}

func TestAdminDisableCredential(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")

	rec := do(t, h, http.MethodPost, "/api/v1/admin/credentials/disable", token,
		map[string]any{"credential_id": "cred-a1", "reason": "密钥泄露"})
	if rec.Code != http.StatusOK {
		t.Fatalf("停用凭证状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	// 原因必填。
	rec = do(t, h, http.MethodPost, "/api/v1/admin/credentials/disable", token,
		map[string]any{"credential_id": "cred-a1"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺原因应返回 400，状态码 = %d", rec.Code)
	}
}

// TestAdminCannotDisableOtherTenantCredential 验证管理员不能停用其他租户的凭证（R7）。
func TestAdminCannotDisableOtherTenantCredential(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")

	rec := do(t, h, http.MethodPost, "/api/v1/admin/credentials/disable", token,
		map[string]any{"credential_id": "cred-a1", "reason": "x", "tenant_id": tenantB})
	if rec.Code == http.StatusOK {
		t.Fatalf("越权停用不应成功")
	}
}

func TestAdminUsageAndRequests(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")

	rec := do(t, h, http.MethodGet, "/api/v1/admin/usage", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询用量状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Usage map[string]any `json:"usage"`
	}
	decodeJSONBody(t, rec, &out)
	if out.Usage["quota_limit"].(float64) != 10 {
		t.Fatalf("额度 = %v", out.Usage["quota_limit"])
	}
	if out.Usage["occupied"].(float64) != 0 {
		t.Fatalf("占用 = %v", out.Usage["occupied"])
	}

	rec = do(t, h, http.MethodGet, "/api/v1/admin/requests", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询请求状态码 = %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAdminCrossTenantQueryRejected 验证管理员不能通过参数查看其他租户数据（R7）。
func TestAdminCrossTenantQueryRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")

	rec := do(t, h, http.MethodGet, "/api/v1/admin/usage?tenant_id="+tenantB, token, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("跨租户查询应返回 400，状态码 = %d", rec.Code)
	}
}

func TestAppCannotAccessAdminAPI(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")
	rec := do(t, h, http.MethodGet, "/api/v1/app/requests", token, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("管理员访问应用接口应返回 403，状态码 = %d", rec.Code)
	}
}

// ---------- 应用接口（R3/R4/R7） ----------

func TestAppInvoke(t *testing.T) {
	srv, up := newTestServer(t)
	h := srv.Handler()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": "sk-a1"})
	var loginOut struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &loginOut)

	rec = do(t, h, http.MethodPost, "/api/v1/app/invoke", loginOut.Token, map[string]any{
		"request_no": "req-1", "model": "text-1", "prompt": "你好",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("调用状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Request  domain.ModelRequest `json:"request"`
		Replayed bool                `json:"replayed"`
	}
	decodeJSONBody(t, rec, &out)
	if out.Request.Status != domain.RequestSuccess {
		t.Fatalf("终态 = %s", out.Request.Status)
	}
	if out.Replayed {
		t.Fatalf("首次调用不应是幂等命中")
	}
	if up.calls != 1 {
		t.Fatalf("上游调用次数 = %d", up.calls)
	}
}

func TestAppInvokeIdempotent(t *testing.T) {
	srv, up := newTestServer(t)
	h := srv.Handler()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": "sk-a1"})
	var loginOut struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &loginOut)

	body := map[string]any{"request_no": "req-1", "model": "text-1", "prompt": "你好"}
	rec = do(t, h, http.MethodPost, "/api/v1/app/invoke", loginOut.Token, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("首次调用状态码 = %d", rec.Code)
	}
	rec = do(t, h, http.MethodPost, "/api/v1/app/invoke", loginOut.Token, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("幂等重放状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Replayed bool `json:"replayed"`
	}
	decodeJSONBody(t, rec, &out)
	if !out.Replayed {
		t.Fatalf("相同编号相同参数应命中幂等")
	}
	if up.calls != 1 {
		t.Fatalf("幂等命中不应重复调用上游，调用次数 = %d", up.calls)
	}
}

func TestAppInvokeConflict(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": "sk-a1"})
	var loginOut struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &loginOut)

	do(t, h, http.MethodPost, "/api/v1/app/invoke", loginOut.Token,
		map[string]any{"request_no": "req-1", "model": "m", "prompt": "p1"})
	rec = do(t, h, http.MethodPost, "/api/v1/app/invoke", loginOut.Token,
		map[string]any{"request_no": "req-1", "model": "m", "prompt": "p2"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("参数冲突应返回 409，状态码 = %d", rec.Code)
	}
}

func TestAppInvokeQuotaExceeded(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	opsToken := login(t, h, "ops", "ops-secret")
	do(t, h, http.MethodPost, "/api/v1/ops/tenants/quota", opsToken,
		map[string]any{"tenant_id": tenantA, "daily_quota": 1, "reason": "限额"})

	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": "sk-a1"})
	var loginOut struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &loginOut)

	body := map[string]any{"request_no": "req-1", "model": "m", "prompt": "p"}
	if rec := do(t, h, http.MethodPost, "/api/v1/app/invoke", loginOut.Token, body); rec.Code != http.StatusOK {
		t.Fatalf("首次调用状态码 = %d", rec.Code)
	}
	rec = do(t, h, http.MethodPost, "/api/v1/app/invoke", loginOut.Token,
		map[string]any{"request_no": "req-2", "model": "m", "prompt": "p"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("额度用尽应返回 429，状态码 = %d", rec.Code)
	}
}

func TestAppInvokeMissingRequestNo(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": "sk-a1"})
	var loginOut struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &loginOut)

	rec = do(t, h, http.MethodPost, "/api/v1/app/invoke", loginOut.Token,
		map[string]any{"model": "m", "prompt": "p"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺请求编号应返回 400，状态码 = %d", rec.Code)
	}
}

func TestAppInvokeUpstreamFailure(t *testing.T) {
	srv, up := newTestServer(t)
	h := srv.Handler()
	up.failure = upstream.ErrFailed

	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": "sk-a1"})
	var loginOut struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &loginOut)

	rec = do(t, h, http.MethodPost, "/api/v1/app/invoke", loginOut.Token,
		map[string]any{"request_no": "req-1", "model": "m", "prompt": "p"})
	if rec.Code != http.StatusOK {
		t.Fatalf("失败应作为终态返回，状态码 = %d", rec.Code)
	}
	var out struct {
		Request domain.ModelRequest `json:"request"`
	}
	decodeJSONBody(t, rec, &out)
	if out.Request.Status != domain.RequestFailed {
		t.Fatalf("终态 = %s, 期望 failed", out.Request.Status)
	}
}

func TestAppListOwnRequests(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": "sk-a1"})
	var loginOut struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &loginOut)

	do(t, h, http.MethodPost, "/api/v1/app/invoke", loginOut.Token,
		map[string]any{"request_no": "req-1", "model": "m", "prompt": "p"})

	rec = do(t, h, http.MethodGet, "/api/v1/app/requests", loginOut.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询请求状态码 = %d", rec.Code)
	}
	var out struct {
		Requests []domain.ModelRequest `json:"requests"`
	}
	decodeJSONBody(t, rec, &out)
	if len(out.Requests) != 1 {
		t.Fatalf("请求数 = %d, 期望 1", len(out.Requests))
	}
}

func TestAppGetRequest(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": "sk-a1"})
	var loginOut struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &loginOut)

	do(t, h, http.MethodPost, "/api/v1/app/invoke", loginOut.Token,
		map[string]any{"request_no": "req-1", "model": "m", "prompt": "p"})

	rec = do(t, h, http.MethodGet, "/api/v1/app/request?request_no=req-1", loginOut.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询请求状态码 = %d: %s", rec.Code, rec.Body.String())
	}

	// 缺编号。
	rec = do(t, h, http.MethodGet, "/api/v1/app/request", loginOut.Token, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺编号应返回 400，状态码 = %d", rec.Code)
	}
	// 不存在的编号。
	rec = do(t, h, http.MethodGet, "/api/v1/app/request?request_no=none", loginOut.Token, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在应返回 404，状态码 = %d", rec.Code)
	}
}

// TestOpsCannotCreateCredential 验证运营账号不能代客户创建凭证（R1）。
//
// 凭证所属租户由服务端从调用者身份推导，运营账号没有租户归属，
// 因此凭证的创建与停用只允许客户管理员操作。
func TestOpsCannotCreateCredential(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	opsToken := login(t, h, "ops", "ops-secret")

	rec := do(t, h, http.MethodPost, "/api/v1/admin/credentials/create", opsToken,
		map[string]any{"name": "app-x"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("运营创建凭证应返回 403，状态码 = %d", rec.Code)
	}
	rec = do(t, h, http.MethodPost, "/api/v1/admin/credentials/disable", opsToken,
		map[string]any{"credential_id": "cred-a1", "reason": "x"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("运营停用凭证应返回 403，状态码 = %d", rec.Code)
	}
	// 运营仍可跨租户只读核查。
	rec = do(t, h, http.MethodGet, "/api/v1/admin/credentials?tenant_id="+tenantA, opsToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("运营查看凭证列表状态码 = %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------- 表单提交（页面可操作） ----------

func TestFormLoginRedirects(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	rec := doForm(t, h, http.MethodPost, "/api/v1/auth/login", "",
		url.Values{"username": {"admin-a"}, "password": {"admin-a-secret"}, "kind": {"admin"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("表单登录应重定向，状态码 = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/") {
		t.Fatalf("重定向地址 = %s", loc)
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Fatalf("表单登录也应下发会话 Cookie")
	}
}

func TestFormAdjustQuotaRedirects(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	rec := doForm(t, h, http.MethodPost, "/api/v1/ops/tenants/quota", token,
		url.Values{"tenant_id": {tenantA}, "daily_quota": {"40"}, "reason": {"表单扩容"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("表单提交应重定向，状态码 = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/ops") {
		t.Fatalf("重定向地址 = %s, 期望 /ops", loc)
	}
}

func TestFormAdjustQuotaRejectsNonNumeric(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	rec := doForm(t, h, http.MethodPost, "/api/v1/ops/tenants/quota", token,
		url.Values{"tenant_id": {tenantA}, "daily_quota": {"不是数字"}, "reason": {"x"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法数字应返回 400，状态码 = %d", rec.Code)
	}
}

func TestFormCreateCredentialRendersSecret(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")

	rec := doForm(t, h, http.MethodPost, "/api/v1/admin/credentials/create", token,
		url.Values{"name": {"app-a2"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("表单创建凭证状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "app-a2") {
		t.Fatalf("响应应包含凭证名称")
	}
	if !strings.Contains(body, "sk_") {
		t.Fatalf("响应应包含一次性密钥")
	}
}

func TestFormInvokeRedirects(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": "sk-a1"})
	var loginOut struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &loginOut)

	rec = doForm(t, h, http.MethodPost, "/api/v1/app/invoke", loginOut.Token,
		url.Values{"request_no": {"req-1"}, "model": {"m"}, "prompt": {"你好"}, "scenario": {"success"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("表单调用应重定向，状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/app") {
		t.Fatalf("重定向地址 = %s, 期望 /app", loc)
	}
}

// TestEffectiveParams 验证场景参数合并规则。
func TestEffectiveParams(t *testing.T) {
	in := invokeRequest{Scenario: "fail"}
	if got := in.effectiveParams(); got["scenario"] != "fail" {
		t.Fatalf("场景参数未合并: %+v", got)
	}

	in = invokeRequest{Params: map[string]any{"scenario": "timeout"}}
	if got := in.effectiveParams(); got["scenario"] != "timeout" {
		t.Fatalf("params 中的场景应优先: %+v", got)
	}

	in = invokeRequest{Params: map[string]any{"a": 1}, Scenario: "fail"}
	got := in.effectiveParams()
	if got["scenario"] != "fail" || got["a"] != 1 {
		t.Fatalf("合并结果不符: %+v", got)
	}

	in = invokeRequest{Params: map[string]any{"a": 1}}
	if out := in.effectiveParams(); len(out) != 1 {
		t.Fatalf("无场景时不应新增字段: %+v", out)
	}
}
