package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 本文件补齐**异常入参分支**的用例：
// GitHub Actions 的 CI 门禁在 Linux + go1.27.4 下按同一套覆盖率口径复算，
// httpapi 包曾因此低于 85%（84.2%，差 5 条语句）。这里补的是真实缺失的分支覆盖：
// 方法不符、请求体不是合法 JSON、运营账号缺少租户范围——都是题面 R1/R7 明确要求的行为，
// 之前只测了成功路径。

// doRaw 发起一次原始请求体请求（用于构造非法 JSON 等异常入参）。
func doRaw(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// appToken 用凭证密钥登录，返回客户应用令牌。
func appToken(t *testing.T, h http.Handler, secret string) string {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": secret})
	if rec.Code != http.StatusOK {
		t.Fatalf("凭证登录状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &out)
	return out.Token
}

// TestWriteEndpointsRejectWrongMethod 只允许 POST 的接口在方法不符时必须返回 405，
// 而不是落到业务逻辑里（R1/R6 的写操作入口约束）。
func TestWriteEndpointsRejectWrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	adminToken := login(t, h, "admin-a", "admin-a-secret")
	clientToken := appToken(t, h, "sk-a1")

	cases := []struct {
		name  string
		path  string
		token string
	}{
		{"创建凭证", "/api/v1/admin/credentials/create", adminToken},
		{"停用凭证", "/api/v1/admin/credentials/disable", adminToken},
		{"发起调用", "/api/v1/app/invoke", clientToken},
	}
	for _, c := range cases {
		rec := do(t, h, http.MethodGet, c.path, c.token, nil)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s：方法不符应返回 405，实际 %d: %s", c.name, rec.Code, rec.Body.String())
		}
	}
}

// TestWriteEndpointsRejectInvalidJSON 请求体不是合法 JSON 时必须返回 400，
// 不能把解析失败当成业务入参继续执行。
func TestWriteEndpointsRejectInvalidJSON(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	adminToken := login(t, h, "admin-a", "admin-a-secret")
	clientToken := appToken(t, h, "sk-a1")

	rec := doRaw(t, h, http.MethodPost, "/api/v1/admin/credentials/create", adminToken, "{不是 JSON")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("创建凭证：非法 JSON 应返回 400，实际 %d: %s", rec.Code, rec.Body.String())
	}
	rec = doRaw(t, h, http.MethodPost, "/api/v1/app/invoke", clientToken, "{不是 JSON")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("发起调用：非法 JSON 应返回 400，实际 %d: %s", rec.Code, rec.Body.String())
	}
}

// TestOpsScopeRequiresTenantID 运营账号跨租户操作必须显式给出 tenant_id（R1/R7），
// 缺少租户范围时返回 400。
func TestOpsScopeRequiresTenantID(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	opsToken := login(t, h, "ops", "ops-secret")

	rec := do(t, h, http.MethodGet, "/api/v1/admin/credentials", opsToken, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("运营缺少 tenant_id 应返回 400，实际 %d: %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodGet, "/api/v1/admin/requests", opsToken, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("运营缺少 tenant_id 应返回 400，实际 %d: %s", rec.Code, rec.Body.String())
	}
}
