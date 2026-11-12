package httpapi

import (
	"context"
	"net/http"
	"testing"

	"modelquota/internal/domain"
)

// TestAppCannotReadRequestOfOtherCredentialInSameTenant 验证同租户内应用凭证之间互相隔离（R7）。
//
// 独立测试 Agent 指出该 403 分支没有回归保护（覆盖计数为 0）：
// 原测试夹具只播种了 tenant-a 的一个凭证，无法构造
// 「同租户第二凭证读取第一凭证创建的请求」这一场景。
func TestAppCannotReadRequestOfOtherCredentialInSameTenant(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()
	ctx := context.Background()

	// 为 tenant-a 增加第二个凭证（同一租户、不同凭证）。
	if err := srv.svc.DB().CreateCredential(ctx, &domain.Credential{
		ID: "cred-a2", TenantID: tenantA, Name: "app-a2", KeyHash: domain.HashSecret("sk-a2"),
		Status: domain.CredentialActive, CreatedAt: testNow, CreatedBy: "admin-a",
	}); err != nil {
		t.Fatalf("写入第二个凭证失败: %v", err)
	}

	// 凭证 A 登录并发起一次调用。
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": "sk-a1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("凭证 A 登录状态码 = %d", rec.Code)
	}
	var loginA struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &loginA)

	rec = do(t, h, http.MethodPost, "/api/v1/app/invoke", loginA.Token,
		map[string]any{"request_no": "iso-1", "model": "m", "prompt": "p"})
	if rec.Code != http.StatusOK {
		t.Fatalf("调用状态码 = %d: %s", rec.Code, rec.Body.String())
	}

	// 凭证 B 登录（与 A 同租户）。
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"secret": "sk-a2"})
	if rec.Code != http.StatusOK {
		t.Fatalf("凭证 B 登录状态码 = %d: %s", rec.Code, rec.Body.String())
	}
	var loginB struct {
		Token string `json:"token"`
	}
	decodeJSONBody(t, rec, &loginB)

	// 凭证 B 不得按编号读取凭证 A 创建的请求。
	rec = do(t, h, http.MethodGet, "/api/v1/app/request?request_no=iso-1", loginB.Token, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("同租户其他凭证读取他人请求应返回 403，实际 %d: %s", rec.Code, rec.Body.String())
	}

	// 凭证 B 的请求列表也不得包含凭证 A 的请求。
	rec = do(t, h, http.MethodGet, "/api/v1/app/requests", loginB.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询请求列表状态码 = %d", rec.Code)
	}
	var out struct {
		Requests []domain.ModelRequest `json:"requests"`
	}
	decodeJSONBody(t, rec, &out)
	for _, r := range out.Requests {
		if r.CredentialID == "cred-a1" {
			t.Fatalf("凭证 B 的列表不应包含凭证 A 的请求: %+v", r)
		}
	}

	// 凭证 A 自己仍然可以读取。
	rec = do(t, h, http.MethodGet, "/api/v1/app/request?request_no=iso-1", loginA.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("凭证 A 读取自己的请求应返回 200，实际 %d", rec.Code)
	}
}
