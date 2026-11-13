package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestName(t *testing.T) {
	if got := NewHTTPCallier("http://x", nil, "").Name(); got != "http-upstream" {
		t.Fatalf("Name() = %s, 期望 http-upstream", got)
	}
	if got := NewHTTPCallier("http://x", nil, "mock").Name(); got != "mock" {
		t.Fatalf("Name() = %s, 期望 mock", got)
	}
}

func TestCompleteSuccess(t *testing.T) {
	var gotReq wireRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/complete" {
			t.Errorf("请求路径 = %s, 期望 /v1/complete", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("请求方法 = %s, 期望 POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %s", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("解析上游请求失败: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wireResponse{UpstreamID: "up-1", Content: "模型输出"})
	}))
	defer srv.Close()

	c := NewHTTPCallier(srv.URL, srv.Client(), "test")
	resp, err := c.Complete(context.Background(), Request{Model: "m", Prompt: "p", Params: map[string]any{"a": 1}}, time.Second)
	if err != nil {
		t.Fatalf("调用上游失败: %v", err)
	}
	if resp.Content != "模型输出" || resp.UpstreamID != "up-1" {
		t.Fatalf("上游响应不符: %+v", resp)
	}
	// 耗时的正确断言是「已测量且未超过超时上限」。
	// 这里**不能**断言 Latency > 0：Windows 上 time.Now() 的时钟分辨率约 0.5ms，
	// 本地 httptest 往返极快时两次读数可能落在同一 tick 内，time.Since 返回 0，
	// 导致该断言在 CI/本机偶发失败（实测在 acceptance-audit 的全量并发跑中复现）。
	// 业务真正要保证的是「耗时已被测量」与「没有突破 2 秒硬时限」，故按此断言。
	if resp.Latency < 0 {
		t.Fatalf("耗时应为非负: %v", resp.Latency)
	}
	if resp.Latency > time.Second {
		t.Fatalf("耗时应不超过调用超时上限: %v > %v", resp.Latency, time.Second)
	}
	if gotReq.Model != "m" || gotReq.Prompt != "p" {
		t.Fatalf("上游收到的请求不符: %+v", gotReq)
	}
	if gotReq.Params["a"] != float64(1) {
		t.Fatalf("上游收到的参数不符: %+v", gotReq.Params)
	}
}

func TestCompleteUpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	c := NewHTTPCallier(srv.URL, srv.Client(), "test")
	_, err := c.Complete(context.Background(), Request{Model: "m", Prompt: "p"}, time.Second)
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("期望 ErrFailed，实际 %v", err)
	}
}

func TestCompleteTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	c := NewHTTPCallier(srv.URL, srv.Client(), "test")
	start := time.Now()
	_, err := c.Complete(context.Background(), Request{Model: "m", Prompt: "p"}, 200*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("期望 ErrTimeout，实际 %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("超时判定耗时过长: %v", elapsed)
	}
}

func TestCompleteMalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("这不是 JSON"))
	}))
	defer srv.Close()

	c := NewHTTPCallier(srv.URL, srv.Client(), "test")
	_, err := c.Complete(context.Background(), Request{Model: "m", Prompt: "p"}, time.Second)
	if err == nil {
		t.Fatalf("非法响应应报错")
	}
	if errors.Is(err, ErrFailed) || errors.Is(err, ErrTimeout) {
		t.Fatalf("非法响应不属于失败/超时终态: %v", err)
	}
}

func TestCompleteUnreachableUpstream(t *testing.T) {
	c := NewHTTPCallier("http://127.0.0.1:1", &http.Client{Timeout: time.Second}, "test")
	_, err := c.Complete(context.Background(), Request{Model: "m", Prompt: "p"}, 500*time.Millisecond)
	if err == nil {
		t.Fatalf("上游不可达应报错")
	}
	if errors.Is(err, ErrTimeout) {
		t.Fatalf("连接失败不应判为超时: %v", err)
	}
}

func TestCompleteContextCanceled(t *testing.T) {
	// 处理器必须能被主动唤醒：服务端在客户端断开后未必立刻感知，
	// 若一直阻塞在 r.Context() 上，httptest 的 Close 会挂起。
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	c := NewHTTPCallier(srv.URL, srv.Client(), "test")
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := c.Complete(ctx, Request{Model: "m", Prompt: "p"}, 5*time.Second)
	if err == nil {
		t.Fatalf("上下文取消应报错")
	}
}

func TestCompleteWithUnserializableParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"content":"x"}`))
	}))
	defer srv.Close()

	c := NewHTTPCallier(srv.URL, srv.Client(), "test")
	_, err := c.Complete(context.Background(),
		Request{Model: "m", Prompt: "p", Params: map[string]any{"ch": make(chan int)}}, time.Second)
	if err == nil {
		t.Fatalf("不可序列化的参数应报错")
	}
}

func TestCompleteLateResultObservedAfterTimeout(t *testing.T) {
	// 上游在硬时限之后才返回成功：平台应先判超时，再上报迟到结果（R4）。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wireResponse{UpstreamID: "up-late", Content: "迟到的输出"})
	}))
	defer srv.Close()

	type late struct {
		correlationID string
		resp          *Response
		err           error
	}
	lateCh := make(chan late, 1)

	c := NewHTTPCallier(srv.URL, srv.Client(), "test")
	c.SetLateResultHandler(func(correlationID string, resp *Response, err error) {
		lateCh <- late{correlationID: correlationID, resp: resp, err: err}
	})

	_, err := c.Complete(context.Background(),
		Request{RequestNo: "req-1", CorrelationID: "corr-1", Model: "m", Prompt: "p"}, 100*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("期望 ErrTimeout，实际 %v", err)
	}

	select {
	case got := <-lateCh:
		if got.err != nil {
			t.Fatalf("迟到结果不应是错误: %v", got.err)
		}
		if got.correlationID != "corr-1" {
			t.Fatalf("迟到结果关联 ID = %s，期望 corr-1", got.correlationID)
		}
		if got.resp == nil || got.resp.UpstreamID != "up-late" {
			t.Fatalf("迟到结果内容不符: %+v", got.resp)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("未在预期时间内收到迟到结果")
	}
}

func TestCompleteNoLateResultWhenCallerCanceled(t *testing.T) {
	// 调用方取消后不应再上报迟到结果。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wireResponse{UpstreamID: "up-late", Content: "迟到的输出"})
	}))
	defer srv.Close()

	lateCh := make(chan string, 1)
	c := NewHTTPCallier(srv.URL, srv.Client(), "test")
	c.SetLateResultHandler(func(correlationID string, resp *Response, err error) {
		lateCh <- correlationID
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	_, err := c.Complete(ctx, Request{CorrelationID: "corr-x", Model: "m", Prompt: "p"}, 5*time.Second)
	if err == nil {
		t.Fatalf("上下文取消应报错")
	}
	select {
	case id := <-lateCh:
		t.Fatalf("调用方取消后不应上报迟到结果，实际收到 %s", id)
	case <-time.After(400 * time.Millisecond):
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abc", 5); got != "abc" {
		t.Fatalf("truncate = %s, 期望 abc", got)
	}
	if got := truncate("abcdef", 3); got != "abc..." {
		t.Fatalf("truncate = %s, 期望 abc...", got)
	}
}
