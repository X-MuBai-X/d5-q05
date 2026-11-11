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
	// RequestNo 是平台侧的请求编号，会透传给上游，
	// 使上游能够按编号识别重复通知（模拟上游的 duplicate 场景）。
	RequestNo string
	// CorrelationID 是平台内部的调用关联标识（model_requests.id）。
	// 它不透传给上游，只在上游客户端观测到迟到结果时原样带回，
	// 让平台能够准确定位对应的请求记录。
	CorrelationID string
	Model         string
	Prompt        string
	Params        map[string]any
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

// LateResultFunc 是迟到结果回调。
//
// 题面 R4：上游在 2 秒硬时限之后才返回的结果属于「迟到结果」，
// 只能作为诊断记录保留，不得改变已经返回的超时结论，也不得追加扣额。
// correlationID 是发起本次调用时传入的 CorrelationID（请求记录 ID）。
type LateResultFunc func(correlationID string, resp *Response, err error)

// LateResultHandlerSetter 由支持迟到结果观测的上游客户端实现。
//
// 该接口是可选的：未实现它的客户端（例如单元测试用的假客户端）
// 只是不提供迟到结果观测能力，不影响其他行为。
type LateResultHandlerSetter interface {
	// SetLateResultHandler 注册迟到结果回调。
	SetLateResultHandler(fn LateResultFunc)
}
