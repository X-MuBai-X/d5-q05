// Package upstream 定义平台调用上游模型的客户端接口与结果类型。
//
// 题面共同要求第 1 节第 5 条允许第三方服务使用可控的 HTTP 模拟服务，
// 但要求能触发成功、失败、超时与迟到等场景。本包只描述契约，
// 具体实现见 internal/upstream/httpclient.go 与 cmd/mockupstream。
package upstream

import (
	"context"
	"errors"
	"time"
)

// ErrTimeout 表示调用上游超过题面 R4 规定的时限。
var ErrTimeout = errors.New("上游调用超时")

// ErrFailed 表示上游明确返回失败。
var ErrFailed = errors.New("上游明确失败")

// Request 是一次上游调用请求。
type Request struct {
	Model  string
	Prompt string
	Params map[string]any
}

// Response 是上游成功返回的结果。
type Response struct {
	// Content 是模型返回的文本内容。
	Content string
	// Latency 是实际耗时。
	Latency time.Duration
	// UpstreamID 是上游返回的调用标识，用于诊断。
	UpstreamID string
}

// Client 是上游模型调用客户端。
type Client interface {
	// Complete 执行一次非流式文本调用。
	// 超过 timeout 或上游返回失败时分别返回 ErrTimeout / ErrFailed。
	Complete(ctx context.Context, req Request, timeout time.Duration) (*Response, error)
	// Name 返回客户端标识，用于部署信息展示。
	Name() string
}
