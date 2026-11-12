package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultLateWait 是超时后继续等待上游结果的最长时间。
//
// 题面 R4 要求「迟到结果只保留为诊断记录」，因此判定超时之后
// 不能立刻断开上游连接，否则平台永远观测不到迟到结果。
// 该值与模拟上游最长挂起时间（30 秒）一致。
const DefaultLateWait = 30 * time.Second

// HTTPCallier 是通过 HTTP 调用上游模型的客户端。
type HTTPCallier struct {
	// BaseURL 是上游服务地址，例如 http://127.0.0.1:9091。
	BaseURL string
	// Client 为底层 HTTP 客户端。
	Client *http.Client
	// ClientName 是客户端标识。
	ClientName string
	// LateWait 是判定超时后继续等待上游结果的最长时间。
	LateWait time.Duration
	// onLateResult 是迟到结果回调。
	onLateResult LateResultFunc
}

// NewHTTPCallier 构造 HTTP 上游客户端。
func NewHTTPCallier(baseURL string, client *http.Client, name string) *HTTPCallier {
	if client == nil {
		client = &http.Client{}
	}
	return &HTTPCallier{BaseURL: baseURL, Client: client, ClientName: name, LateWait: DefaultLateWait}
}

// Name 返回客户端标识。
func (c *HTTPCallier) Name() string {
	if c.ClientName == "" {
		return "http-upstream"
	}
	return c.ClientName
}

// SetLateResultHandler 注册迟到结果回调。
func (c *HTTPCallier) SetLateResultHandler(fn LateResultFunc) {
	c.onLateResult = fn
}

type wireRequest struct {
	// RequestNo 透传给上游，便于上游按编号识别重复通知。
	RequestNo string         `json:"request_no,omitempty"`
	Model     string         `json:"model"`
	Prompt    string         `json:"prompt"`
	Params    map[string]any `json:"params,omitempty"`
}

type wireResponse struct {
	UpstreamID string `json:"upstream_id"`
	Content    string `json:"content"`
}

// callResult 是一次上游调用的原始结果。
type callResult struct {
	resp *Response
	err  error
}

// Complete 调用上游 /v1/complete。
//
// 超时判定：题面 R4 规定超过 2 秒按超时处理，因此这里用 timeout 作为
// 判定时限，返回 ErrTimeout。
//
// 迟到结果：判定超时的同时**不中断**上游调用（用 WithoutCancel 派生独立上下文），
// 而是在后台继续等待上游结果。若上游随后返回，则通过 OnLateResult 回调上报，
// 由上层写成诊断记录（R4：只留诊断，不改终态、不追加扣额）。
func (c *HTTPCallier) Complete(ctx context.Context, req Request, timeout time.Duration) (*Response, error) {
	body, err := json.Marshal(wireRequest{
		RequestNo: req.RequestNo,
		Model:     req.Model,
		Prompt:    req.Prompt,
		Params:    req.Params,
	})
	if err != nil {
		return nil, fmt.Errorf("序列化上游请求失败: %w", err)
	}

	// 上游调用上下文与调用方的取消信号解绑，但保留取值：
	//  1. 调用方（HTTP 请求）结束后不会误伤仍在等待的上游调用；
	//  2. 判定超时后不取消它，从而能观测到迟到结果（R4）；
	//  3. 仍保留 LateWait 上限，避免后台协程无限期挂起。
	lateWait := c.LateWait
	if lateWait <= 0 {
		lateWait = DefaultLateWait
	}
	upstreamCtx, cancelUpstream := context.WithTimeout(context.WithoutCancel(ctx), lateWait)

	// 调用方主动取消（如 HTTP 客户端断开）仍应中断上游调用；
	// 但一旦判定超时，就停止观察调用方取消信号，保留迟到结果的观测窗口。
	stopWatch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			cancelUpstream()
		case <-stopWatch:
		}
	}()

	httpReq, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost,
		c.BaseURL+"/v1/complete", bytes.NewReader(body))
	if err != nil {
		close(stopWatch)
		cancelUpstream()
		return nil, fmt.Errorf("构造上游请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	started := time.Now()
	// 缓冲通道：即使调用方已经返回，后台协程也不会阻塞。
	ch := make(chan callResult, 1)
	go func() {
		ch <- c.do(upstreamCtx, httpReq, started)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case r := <-ch:
		close(stopWatch)
		cancelUpstream()
		return r.resp, r.err
	case <-timer.C:
		// 判定超时（R4）：返回 ErrTimeout，上层释放额度占用、不计成功量。
		// 后台继续等待上游，若随后返回则上报迟到结果。
		if ctx.Err() != nil {
			// 调用方已取消：没有迟到结果可言，直接结束。
			close(stopWatch)
			cancelUpstream()
			return nil, ErrTimeout
		}
		close(stopWatch)
		go c.awaitLateResult(req.CorrelationID, ch, cancelUpstream)
		return nil, ErrTimeout
	}
}

// do 执行一次真实的上游 HTTP 调用并归一化结果。
func (c *HTTPCallier) do(ctx context.Context, httpReq *http.Request, started time.Time) callResult {
	resp, err := c.Client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			// 上下文到期：既可能是硬时限，也可能是迟到等待窗口关闭。
			return callResult{err: ErrTimeout}
		}
		return callResult{err: fmt.Errorf("调用上游失败: %w", err)}
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return callResult{err: ErrTimeout}
		}
		return callResult{err: fmt.Errorf("读取上游响应失败: %w", err)}
	}

	if ctx.Err() != nil {
		return callResult{err: ErrTimeout}
	}
	if resp.StatusCode != http.StatusOK {
		return callResult{err: fmt.Errorf("%w: 上游返回状态码 %d: %s", ErrFailed, resp.StatusCode, truncate(string(payload), 200))}
	}

	var out wireResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		return callResult{err: fmt.Errorf("解析上游响应失败: %w", err)}
	}
	return callResult{resp: &Response{
		Content:    out.Content,
		Latency:    time.Since(started),
		UpstreamID: out.UpstreamID,
	}}
}

// awaitLateResult 在判定超时之后等待上游的最终结果，并上报迟到结果。
func (c *HTTPCallier) awaitLateResult(correlationID string, ch <-chan callResult, cancel context.CancelFunc) {
	defer cancel()
	r := <-ch
	if c.onLateResult == nil {
		return
	}
	c.onLateResult(correlationID, r.resp, r.err)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
