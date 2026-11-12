package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pageGet 访问一个页面并返回响应记录。
func pageGet(t *testing.T, h http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ---------- 登录页 ----------

func TestLoginPage(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := pageGet(t, srv.Handler(), "/login", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("登录页状态码 = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %s", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "登录") {
		t.Fatalf("登录页内容异常")
	}
	if !strings.Contains(body, "预置账号") {
		t.Fatalf("登录页应展示预置账号说明")
	}
	if strings.Contains(body, "admin-a-secret") {
		t.Fatalf("登录页不应回显预置口令")
	}
}

// ---------- 首页跳转 ----------

func TestIndexRedirectsByIdentity(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()

	// 未登录跳转到登录页。
	rec := pageGet(t, h, "/", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("未登录状态码 = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("未登录跳转 = %s, 期望 /login", loc)
	}

	// 运营跳运营台。
	rec = pageGet(t, h, "/", login(t, h, "ops", "ops-secret"))
	if loc := rec.Header().Get("Location"); loc != "/ops" {
		t.Fatalf("运营跳转 = %s, 期望 /ops", loc)
	}

	// 客户管理员跳管理台。
	rec = pageGet(t, h, "/", login(t, h, "admin-a", "admin-a-secret"))
	if loc := rec.Header().Get("Location"); loc != "/admin" {
		t.Fatalf("管理员跳转 = %s, 期望 /admin", loc)
	}

	// 应用凭证跳应用台。
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": "sk-a1"})
	var loginOut struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &loginOut)
	rec = pageGet(t, h, "/", loginOut.Token)
	if loc := rec.Header().Get("Location"); loc != "/app" {
		t.Fatalf("应用跳转 = %s, 期望 /app", loc)
	}
}

func TestUnknownPathReturnsNotFoundPage(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := pageGet(t, srv.Handler(), "/no-such-page", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知路径状态码 = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "页面不存在") {
		t.Fatalf("应返回 404 页面")
	}
}

// ---------- 运营工作台 ----------

func TestOpsPage(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")

	rec := pageGet(t, h, "/ops", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("运营台状态码 = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "租户与配额") {
		t.Fatalf("运营台内容异常")
	}
	if !strings.Contains(body, tenantA) || !strings.Contains(body, tenantB) {
		t.Fatalf("运营台应列出全部租户")
	}
	if !strings.Contains(body, "日终核查") {
		t.Fatalf("运营台应含日终核查区块")
	}
	if !strings.Contains(body, "诊断记录") {
		t.Fatalf("运营台应含诊断记录区块")
	}
}

func TestOpsPageRequiresLogin(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := pageGet(t, srv.Handler(), "/ops", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("未登录状态码 = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("跳转 = %s, 期望 /login", loc)
	}
}

func TestOpsPageRejectsTenantAdmin(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")
	rec := pageGet(t, h, "/ops", token)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("客户管理员访问运营台状态码 = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("跳转 = %s, 期望 /", loc)
	}
}

func TestOpsPageShowsDiagnostics(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	opsToken := login(t, h, "ops", "ops-secret")
	adminToken := login(t, h, "admin-a", "admin-a-secret")

	// 创建一个凭证并用测试钩子制造迟到结果。
	rec := do(t, h, http.MethodPost, "/api/v1/admin/credentials/create", adminToken,
		map[string]any{"name": "app-a2"})
	var created struct {
		Secret string `json:"secret"`
	}
	decodeJSONBody(t, rec, &created)
	if rec.Code != http.StatusOK {
		t.Fatalf("创建凭证状态码 = %d: %s", rec.Code, rec.Body.String())
	}

	rec = pageGet(t, h, "/ops", opsToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("运营台状态码 = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "暂无诊断记录") {
		t.Fatalf("无诊断时应显示空态提示")
	}
}

// ---------- 租户管理台 ----------

func TestAdminPage(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")

	rec := pageGet(t, h, "/admin", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("管理台状态码 = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "租户概况") {
		t.Fatalf("管理台内容异常")
	}
	if !strings.Contains(body, "应用凭证") {
		t.Fatalf("管理台应含凭证区块")
	}
	if !strings.Contains(body, "被拒绝的调用尝试") {
		t.Fatalf("管理台应含被拒绝尝试区块")
	}
	if !strings.Contains(body, "app-a1") {
		t.Fatalf("管理台应列出本租户凭证")
	}
}

func TestAdminPageRejectsOps(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "ops", "ops-secret")
	rec := pageGet(t, h, "/admin", token)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("运营访问管理台状态码 = %d", rec.Code)
	}
}

func TestAdminPageRequiresLogin(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := pageGet(t, srv.Handler(), "/admin", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("未登录状态码 = %d", rec.Code)
	}
}

func TestAdminPageShowsRejectedAttempts(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	adminToken := login(t, h, "admin-a", "admin-a-secret")

	// 用非法请求编号制造一次被拒绝的尝试。
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": "sk-a1"})
	var loginOut struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &loginOut)
	do(t, h, http.MethodPost, "/api/v1/app/invoke", loginOut.Token,
		map[string]any{"request_no": "非法编号"})

	rec = pageGet(t, h, "/admin", adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("管理台状态码 = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "请求编号格式非法") {
		t.Fatalf("管理台应展示被拒绝尝试的原因")
	}
}

// ---------- 应用调用台 ----------

func TestAppPage(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": "sk-a1"})
	var loginOut struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &loginOut)

	rec = pageGet(t, h, "/app", loginOut.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("应用台状态码 = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "发起模型调用") {
		t.Fatalf("应用台内容异常")
	}
	if !strings.Contains(body, "上游场景") {
		t.Fatalf("应用台应含场景选择")
	}
}

func TestAppPageRejectsAdmin(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	token := login(t, h, "admin-a", "admin-a-secret")
	rec := pageGet(t, h, "/app", token)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("管理员访问应用台状态码 = %d", rec.Code)
	}
}

func TestAppPageRequiresLogin(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := pageGet(t, srv.Handler(), "/app", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("未登录状态码 = %d", rec.Code)
	}
}

// ---------- 渲染辅助 ----------

func TestRenderHandlesTemplateError(t *testing.T) {
	srv, _ := newTestServer(t)
	// 传入一个无法渲染的视图，触发渲染错误分支。
	rec := httptest.NewRecorder()
	srv.render(rec, http.StatusOK, srv.templates.admin, pageData{Title: "x", Data: "不是视图"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("渲染失败状态码 = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "页面渲染失败") {
		t.Fatalf("应返回渲染失败提示")
	}
}
