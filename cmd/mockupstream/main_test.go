package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// contextWithTimeout 构造带超时的上下文。
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func newTestServer() *server { return &server{seen: map[string]string{}} }

func TestHandleCompleteSuccess(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/complete",
		strings.NewReader(`{"model":"m","prompt":"你好","params":{"a":1}}`))
	rec := httptest.NewRecorder()
	s.handleComplete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	var out wireResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if !strings.Contains(out.Content, "你好") {
		t.Fatalf("响应内容 = %s", out.Content)
	}
	if out.UpstreamID == "" {
		t.Fatalf("应返回上游调用标识")
	}
	if out.Scenario != "success" {
		t.Fatalf("场景 = %s, 期望 success", out.Scenario)
	}
}

func TestHandleCompleteRejectsNonPost(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/v1/complete", nil)
	rec := httptest.NewRecorder()
	s.handleComplete(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 = %d, 期望 405", rec.Code)
	}
}

func TestHandleCompleteRejectsBrokenBody(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/complete", strings.NewReader("{"))
	rec := httptest.NewRecorder()
	s.handleComplete(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, 期望 400", rec.Code)
	}
}

func TestHandleCompleteFailScenario(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/complete",
		strings.NewReader(`{"prompt":"p","params":{"scenario":"fail"}}`))
	rec := httptest.NewRecorder()
	s.handleComplete(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d, 期望 500", rec.Code)
	}
}

func TestHandleCompleteScenarioFromHeader(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/complete", strings.NewReader(`{"prompt":"p"}`))
	req.Header.Set("X-Mock-Scenario", "fail")
	rec := httptest.NewRecorder()
	s.handleComplete(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d, 期望 500", rec.Code)
	}
}

func TestHandleCompleteTimeoutScenarioReleases(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/complete",
		strings.NewReader(`{"prompt":"p","params":{"scenario":"timeout"}}`))
	ctx, cancel := contextWithTimeout(100 * time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	done := make(chan struct{})
	go func() {
		rec := httptest.NewRecorder()
		s.handleComplete(rec, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("超时场景应在上游请求取消后返回")
	}
}

func TestHandleCompleteDuplicateScenarioReturnsSameContent(t *testing.T) {
	s := newTestServer()
	body := `{"prompt":"同样的问题","request_no":"req-1","params":{"scenario":"duplicate"}}`

	first := httptest.NewRecorder()
	s.handleComplete(first, httptest.NewRequest(http.MethodPost, "/v1/complete", strings.NewReader(body)))
	second := httptest.NewRecorder()
	s.handleComplete(second, httptest.NewRequest(http.MethodPost, "/v1/complete", strings.NewReader(body)))

	var a, b wireResponse
	if err := json.Unmarshal(first.Body.Bytes(), &a); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &b); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if a.Content != b.Content {
		t.Fatalf("重复通知场景应返回一致内容：%s != %s", a.Content, b.Content)
	}
}

func TestContentForKeysByPrompt(t *testing.T) {
	s := newTestServer()
	first := s.contentFor(wireRequest{Prompt: "问题一"}, "success")
	again := s.contentFor(wireRequest{Prompt: "问题一"}, "success")
	if first != again {
		t.Fatalf("相同提示词应返回相同内容")
	}
	other := s.contentFor(wireRequest{Prompt: "问题二"}, "success")
	if other == first {
		t.Fatalf("不同提示词应返回不同内容")
	}
}

func TestNewIDIsUnique(t *testing.T) {
	s := newTestServer()
	a := s.newID()
	b := s.newID()
	if a == b {
		t.Fatalf("上游标识应唯一")
	}
	if !strings.HasPrefix(a, "mock-") {
		t.Fatalf("上游标识 = %s", a)
	}
}

func TestStringParam(t *testing.T) {
	if got := stringParam(nil, "scenario"); got != "" {
		t.Fatalf("空参数表应返回空串，实际 %q", got)
	}
	if got := stringParam(map[string]any{"scenario": "fail"}, "scenario"); got != "fail" {
		t.Fatalf("字符串参数 = %q", got)
	}
	if got := stringParam(map[string]any{"scenario": 1}, "scenario"); got != "" {
		t.Fatalf("非字符串参数应返回空串，实际 %q", got)
	}
	if got := stringParam(map[string]any{"other": "x"}, "scenario"); got != "" {
		t.Fatalf("缺失参数应返回空串，实际 %q", got)
	}
}

func TestRunRejectsInvalidAddress(t *testing.T) {
	if err := run("127.0.0.1:99999"); err == nil {
		t.Fatalf("非法监听地址应返回错误")
	}
}

func TestRunMainRejectsUnknownFlag(t *testing.T) {
	if err := runMain([]string{"--不存在的参数"}); err == nil {
		t.Fatalf("未知参数应返回错误")
	}
}

func TestRunMainRejectsInvalidAddress(t *testing.T) {
	if err := runMain([]string{"-addr", "127.0.0.1:99999"}); err == nil {
		t.Fatalf("非法监听地址应返回错误")
	}
}

// TestRunServesHealthz 验证模拟上游可启动并提供健康检查。
func TestRunServesHealthz(t *testing.T) {
	// 先找一个空闲端口，避免端口冲突。
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("探测空闲端口失败: %v", err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	go func() { _ = run(addr) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("健康检查状态码 = %d", resp.StatusCode)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("模拟上游未在超时前就绪")
}
