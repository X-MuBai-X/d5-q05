package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"modelquota/internal/domain"
	"modelquota/internal/service"
)

// 本文件补充「错误路径与边界路径」的单元测试。
//
// 目的：让 HTTP 层的错误映射、参数边界、页面错误渲染等分支都有真实断言，
// 避免覆盖率只反映成功路径（R7 越权、R8 核查分支的兜底路径）。

// TestStatusForMapsDomainErrors 验证领域错误到 HTTP 状态码的映射。
func TestStatusForMapsDomainErrors(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{domain.ErrUnauthorized, http.StatusUnauthorized},
		{domain.ErrForbidden, http.StatusForbidden},
		{domain.ErrNotFound, http.StatusNotFound},
		{domain.ErrQuotaExceeded, http.StatusTooManyRequests},
		{domain.ErrRequestNoConflict, http.StatusConflict},
		{domain.ErrRequestNoRequired, http.StatusBadRequest},
		{domain.ErrRequestNoFormat, http.StatusBadRequest},
		{domain.ErrQuotaRange, http.StatusBadRequest},
		{domain.ErrQuotaFloor, http.StatusBadRequest},
		{domain.ErrReasonRequired, http.StatusBadRequest},
		{domain.ErrCredentialLimit, http.StatusBadRequest},
		{domain.ErrTenantMismatch, http.StatusBadRequest},
		{domain.ErrTenantScopeRequired, http.StatusBadRequest},
		{domain.ErrTenantDisabled, http.StatusConflict},
		{domain.ErrCredentialDisabled, http.StatusConflict},
		{errors.New("未分类的内部错误"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		if got := statusFor(c.err); got != c.want {
			t.Fatalf("statusFor(%v) = %d, 期望 %d", c.err, got, c.want)
		}
	}
}

// TestOpsOpenTenantCreatesNewTenant 验证运营可以开通一个全新租户。
func TestOpsOpenTenantCreatesNewTenant(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	// 不指定 tenant_id 表示新建租户（由系统生成租户编号）。
	rec := do(t, h, http.MethodPost, "/api/v1/ops/tenants/open", token, map[string]any{
		"name": "企业丙", "daily_quota": 5, "reason": "新客户开通",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("开通新租户失败: %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, h, http.MethodGet, "/api/v1/ops/tenants", token, nil)
	var out struct {
		Tenants []domain.Tenant `json:"tenants"`
	}
	decodeJSONBody(t, rec, &out)
	found := false
	for _, tenant := range out.Tenants {
		if tenant.Name == "企业丙" {
			found = true
			if tenant.DailyQuota != 5 {
				t.Fatalf("新租户额度 = %d, 期望 5", tenant.DailyQuota)
			}
			if tenant.Status != domain.TenantActive {
				t.Fatalf("新租户状态 = %s, 期望 active", tenant.Status)
			}
			if tenant.ID == "" {
				t.Fatalf("新租户应有系统生成的编号")
			}
		}
	}
	if !found {
		t.Fatalf("租户列表中应包含新建的「企业丙」")
	}
}

// TestOpsOpenTenantRequiresNameForNewTenant 验证新建租户必须给出名称。
func TestOpsOpenTenantRequiresNameForNewTenant(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	rec := do(t, h, http.MethodPost, "/api/v1/ops/tenants/open", token, map[string]any{
		"daily_quota": 5, "reason": "缺名称",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺少名称应返回 400，实际 %d %s", rec.Code, rec.Body.String())
	}
}

// TestOpsDiagnosticsRequiresTenantScope 验证运营跨租户诊断查询必须显式指定租户。
func TestOpsDiagnosticsRequiresTenantScope(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	rec := do(t, h, http.MethodGet, "/api/v1/ops/diagnostics", token, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未指定租户应返回 400，实际 %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, h, http.MethodGet, "/api/v1/ops/diagnostics?tenant_id=tenant-a", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("指定租户应返回 200，实际 %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Diagnostics []domain.Diagnostic `json:"diagnostics"`
	}
	decodeJSONBody(t, rec, &out)
	if out.Diagnostics == nil {
		t.Fatalf("diagnostics 应为数组（无记录时是空数组而不是 null）")
	}
}

// TestOpsDailyReportAcceptsExplicitDay 验证可以按指定日期核查。
func TestOpsDailyReportAcceptsExplicitDay(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	rec := do(t, h, http.MethodGet, "/api/v1/ops/report/daily?tenant_id=tenant-a&day=2026-01-01", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("按日期核查失败: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Report domain.DailyReport `json:"report"`
	}
	decodeJSONBody(t, rec, &out)
	if out.Report.Day != "2026-01-01" {
		t.Fatalf("报表日期 = %s, 期望 2026-01-01", out.Report.Day)
	}
	// R8：五类终态必须全部列出，即使计数为 0。
	for _, status := range domain.StatusOrder {
		if _, ok := out.Report.Counts[status]; !ok {
			t.Fatalf("报表缺少终态 %s（R8 要求列出五类）", status)
		}
	}
}

// TestAdminUsageAcceptsExplicitDay 验证管理员可按指定日期查看用量。
func TestAdminUsageAcceptsExplicitDay(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")

	rec := do(t, h, http.MethodGet, "/api/v1/admin/usage?day=2026-01-01", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("按日期查用量失败: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Usage domain.Usage `json:"usage"`
	}
	decodeJSONBody(t, rec, &out)
	if out.Usage.Day != "2026-01-01" {
		t.Fatalf("用量日期 = %s, 期望 2026-01-01", out.Usage.Day)
	}
}

// TestAppListOwnRequestsAPI 验证应用凭证可以通过接口列出本凭证的请求。
func TestAppListOwnRequestsAPI(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")

	// 先用管理员创建凭证，再以凭证身份调用。
	rec := do(t, h, http.MethodPost, "/api/v1/admin/credentials/create", token,
		map[string]any{"name": "app-list"})
	if rec.Code != http.StatusOK {
		t.Fatalf("创建凭证失败: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Secret string `json:"secret"`
	}
	decodeJSONBody(t, rec, &created)

	rec = do(t, h, http.MethodPost, "/api/v1/auth/login", "",
		map[string]any{"secret": created.Secret})
	if rec.Code != http.StatusOK {
		t.Fatalf("凭证登录失败: %d %s", rec.Code, rec.Body.String())
	}
	var session struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &session)

	rec = do(t, h, http.MethodPost, "/api/v1/app/invoke", session.Token,
		map[string]any{"request_no": "list-req-1", "model": "m", "prompt": "p"})
	if rec.Code != http.StatusOK {
		t.Fatalf("调用失败: %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, h, http.MethodGet, "/api/v1/app/requests", session.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("列出本凭证请求失败: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Requests []domain.ModelRequest `json:"requests"`
	}
	decodeJSONBody(t, rec, &out)
	if len(out.Requests) != 1 || out.Requests[0].RequestNo != "list-req-1" {
		t.Fatalf("本凭证请求列表不符: %+v", out.Requests)
	}
}

// TestInvokeParamsScenarioTakesPrecedence 验证 params.scenario 优先于顶层 scenario。
func TestInvokeParamsScenarioTakesPrecedence(t *testing.T) {
	srv, up := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")

	rec := do(t, h, http.MethodPost, "/api/v1/admin/credentials/create", token,
		map[string]any{"name": "app-params"})
	if rec.Code != http.StatusOK {
		t.Fatalf("创建凭证失败: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Secret string `json:"secret"`
	}
	decodeJSONBody(t, rec, &created)
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login", "",
		map[string]any{"secret": created.Secret})
	var session struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &session)

	rec = do(t, h, http.MethodPost, "/api/v1/app/invoke", session.Token, map[string]any{
		"request_no": "params-req-1", "model": "m", "prompt": "p",
		"scenario": "top-level",
		"params":   map[string]any{"scenario": "in-params"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("调用失败: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Request domain.ModelRequest `json:"request"`
	}
	decodeJSONBody(t, rec, &out)
	// 幂等指纹必须由最终生效的参数算出，重复提交同一请求体应命中幂等。
	rec = do(t, h, http.MethodPost, "/api/v1/app/invoke", session.Token, map[string]any{
		"request_no": "params-req-1", "model": "m", "prompt": "p",
		"scenario": "top-level",
		"params":   map[string]any{"scenario": "in-params"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("重复调用失败: %d %s", rec.Code, rec.Body.String())
	}
	var replay struct {
		Replayed bool `json:"replayed"`
	}
	decodeJSONBody(t, rec, &replay)
	if !replay.Replayed {
		t.Fatalf("相同参数应命中幂等")
	}
	if up.calls != 1 {
		t.Fatalf("上游调用次数 = %d, 期望 1（幂等不应再次调用上游）", up.calls)
	}
}

// TestIdentityFromCookie 验证会话也可以通过 Cookie 携带。
func TestIdentityFromCookie(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/whoami", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Cookie 鉴权失败: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Role string `json:"role"`
	}
	decodeJSONBody(t, rec, &out)
	if out.Role != string(domain.AdminOps) {
		t.Fatalf("角色 = %s, 期望 ops", out.Role)
	}
}

// TestLogoutWithoutTokenStillSucceeds 验证无令牌登出也返回成功（幂等）。
func TestLogoutWithoutTokenStillSucceeds(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := do(t, srv.Handler(), http.MethodPost, "/api/v1/auth/logout", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("无令牌登出应返回 200，实际 %d %s", rec.Code, rec.Body.String())
	}
}

// TestPagesRenderErrorWhenDatabaseUnavailable 验证页面在数据装配失败时渲染错误页。
//
// 走真实的渲染函数而不是整条 HTTP 链路：会话解析本身也要读数据库，
// 数据库整体不可用时请求会在鉴权阶段就结束，无法抵达页面渲染分支。
func TestPagesRenderErrorWhenDatabaseUnavailable(t *testing.T) {
	srv, _ := newTestServer(t)
	identity := service.Identity{
		SubjectType: domain.SubjectAdmin,
		Role:        domain.AdminOps,
		Username:    "ops",
	}

	req := httptest.NewRequest(http.MethodGet, "/ops", nil)
	rec := httptest.NewRecorder()
	srv.renderOpsError(rec, req, identity, errors.New("列出租户失败"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("运营台错误页应返回 500，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "列出租户失败") {
		t.Fatalf("错误页应展示失败原因，实际 %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec = httptest.NewRecorder()
	srv.renderAdminError(rec, req, identity, errors.New("读取租户失败"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("管理台错误页应返回 500，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "读取租户失败") {
		t.Fatalf("错误页应展示失败原因，实际 %s", rec.Body.String())
	}
}

// TestWriteErrorMasksInternalDetails 验证 500 响应统一掩码，不透出内部细节。
func TestWriteErrorMasksInternalDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusInternalServerError, errors.New("sqlite: database is closed"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d, 期望 500", rec.Code)
	}
	body := rec.Body.String()
	for _, leak := range []string{"sqlite", "closed", "database"} {
		if containsFold(body, leak) {
			t.Fatalf("500 响应透出了内部细节 %q：%s", leak, body)
		}
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析错误响应失败: %v", err)
	}
	if out["error"] != "服务内部错误，请稍后重试" {
		t.Fatalf("500 响应体 = %v, 期望统一掩码文案", out["error"])
	}
}

// containsFold 判断字符串是否包含指定子串（忽略大小写）。
func containsFold(s, sub string) bool {
	return len(sub) == 0 || indexFold(s, sub) >= 0
}

// indexFold 返回子串在字符串中的位置（忽略大小写），找不到返回 -1。
func indexFold(s, sub string) int {
	lower := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b + 32
		}
		return b
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if lower(s[i+j]) != lower(sub[j]) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// TestServiceAuthorizeTenantAccessRejectsOtherTenant 验证租户越权判定。
func TestServiceAuthorizeTenantAccessRejectsOtherTenant(t *testing.T) {
	if err := service.AuthorizeTenantAccess(service.Identity{
		SubjectType: domain.SubjectAdmin,
		TenantID:    tenantA,
	}, tenantB); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("跨租户访问应被拒绝，实际 %v", err)
	}
	if err := service.AuthorizeTenantAccess(service.Identity{
		SubjectType: domain.SubjectAdmin,
		TenantID:    tenantA,
	}, tenantA); err != nil {
		t.Fatalf("同租户访问应被允许，实际 %v", err)
	}
}
