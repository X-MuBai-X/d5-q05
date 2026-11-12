package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 本文件补齐 HTTP 上游客户端的**异常分支**覆盖：
// GitHub Actions 的 CI 门禁在 Linux + go1.27.4 下复算覆盖率时，
// upstream 包为 83.8%（差 1 条语句）。补的是真实缺失的分支：
// 迟到等待窗口未配置、上游地址非法、调用方已取消、响应体读取失败、迟到窗口内才拿到结果。

// errBody 是一个读取即失败的响应体，用来触发「读取上游响应失败」分支。
type errBody struct{}

func (errBody) Read([]byte) (int, error) { return 0, errors.New("读取中断") }
func (errBody) Close() error             { return nil }

// roundTripFunc 把函数适配成 http.RoundTripper。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestCompleteUsesDefaultLateWait 未显式配置迟到等待窗口时回落到默认值，
// 不能让后台协程没有上限地等待。
func TestCompleteUsesDefaultLateWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wireResponse{UpstreamID: "up-1", Content: "模型输出"})
	}))
	defer srv.Close()

	c := NewHTTPCallier(srv.URL, srv.Client(), "test")
	c.LateWait = 0 // 触发回落到 DefaultLateWait
	resp, err := c.Complete(context.Background(), Request{Model: "m", Prompt: "p"}, time.Second)
	if err != nil {
		t.Fatalf("调用上游失败: %v", err)
	}
	if resp.UpstreamID != "up-1" {
		t.Fatalf("上游标识 = %s", resp.UpstreamID)
	}
}

// TestCompleteInvalidBaseURL 上游地址非法时必须返回可读的错误，
// 而不是 panic 或空结果。
func TestCompleteInvalidBaseURL(t *testing.T) {
	c := NewHTTPCallier("http://非法 主机地址", &http.Client{}, "test")
	_, err := c.Complete(context.Background(), Request{Model: "m", Prompt: "p"}, time.Second)
	if err == nil {
		t.Fatalf("非法上游地址应返回错误")
	}
	if !strings.Contains(err.Error(), "构造上游请求失败") {
		t.Fatalf("错误信息 = %v，期望包含「构造上游请求失败」", err)
	}
}

// TestCompleteCallerCancelled 调用方在超时前取消时，超时判定分支直接结束，
// 不再等待迟到结果。
func TestCompleteCallerCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wireResponse{UpstreamID: "up-1", Content: "模型输出"})
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := NewHTTPCallier(srv.URL, srv.Client(), "test")
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	_, err := c.Complete(ctx, Request{Model: "m", Prompt: "p"}, 80*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("期望 ErrTimeout，实际 %v", err)
	}
}

// TestCompleteBodyReadFailure 上游响应体读取失败时返回明确错误。
func TestCompleteBodyReadFailure(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       errBody{},
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})}
	c := NewHTTPCallier("http://upstream.invalid", client, "test")
	_, err := c.Complete(context.Background(), Request{Model: "m", Prompt: "p"}, time.Second)
	if err == nil {
		t.Fatalf("读取失败应返回错误")
	}
	if !strings.Contains(err.Error(), "读取上游响应失败") {
		t.Fatalf("错误信息 = %v，期望包含「读取上游响应失败」", err)
	}
}

// TestCompleteLateWindowClosed 结果在迟到等待窗口关闭之后才到达时，
// 按超时处理（R4：迟到结果只留诊断，不改终态）。
func TestCompleteLateWindowClosed(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := &delayedBody{delay: 120 * time.Millisecond, data: `{"upstream_id":"up-1","content":"迟到输出"}`}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       body,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})}
	c := NewHTTPCallier("http://upstream.invalid", client, "test")
	c.LateWait = 30 * time.Millisecond
	_, err := c.Complete(context.Background(), Request{Model: "m", Prompt: "p"}, 500*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("迟到结果应按超时处理，实际 %v", err)
	}
}

// delayedBody 在读取时延迟返回内容。
type delayedBody struct {
	delay time.Duration
	data  string
	off   int
}

func (b *delayedBody) Read(p []byte) (int, error) {
	if b.off == 0 {
		time.Sleep(b.delay)
	}
	if b.off >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.off:])
	b.off += n
	return n, nil
}

func (b *delayedBody) Close() error { return nil }
