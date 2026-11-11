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

// HTTPCallier 是通过 HTTP 调用上游模型的客户端。
type HTTPCallier struct {
	// BaseURL 是上游服务地址，例如 http://127.0.0.1:9091。
	BaseURL string
	// Client 为底层 HTTP 客户端。
	Client *http.Client
	// ClientName 为客户端标识。
	ClientName string
}

// NewHTTPCallier 构造 HTTP 上游客户端。
func NewHTTPCallier(baseURL string, client *http.Client, name string) *HTTPCallier {
	if client == nil {
		client = &http.Client{}
	}
	return &HTTPCallier{BaseURL: baseURL, Client: client, ClientName: name}
}

// Name 返回客户端标识。
func (c *HTTPCallier) Name() string {
	if c.ClientName == "" {
		return "http-upstream"
	}
	return c.ClientName
}

type wireRequest struct {
	Model  string         `json:"model"`
	Prompt string         `json:"prompt"`
	Params map[string]any `json:"params,omitempty"`
}

type wireResponse struct {
	UpstreamID string `json:"upstream_id"`
	Content    string `json:"content"`
}

// Complete 调用上游 /v1/complete。
//
// 超时判定：题面 R4 规定超过 2 秒按超时处理，因此这里用 timeout 作为
// 上游调用的硬时限，返回 ErrTimeout。
func (c *HTTPCallier) Complete(ctx context.Context, req Request, timeout time.Duration) (*Response, error) {
	body, err := json.Marshal(wireRequest{Model: req.Model, Prompt: req.Prompt, Params: req.Params})
	if err != nil {
		return nil, fmt.Errorf("序列化上游请求失败: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost,
		c.BaseURL+"/v1/complete", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构造上游请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	started := time.Now()
	resp, err := c.Client.Do(httpReq)
	if err != nil {
		if callCtx.Err() != nil {
			return nil, ErrTimeout
		}
		return nil, fmt.Errorf("调用上游失败: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		if callCtx.Err() != nil {
			return nil, ErrTimeout
		}
		return nil, fmt.Errorf("读取上游响应失败: %w", err)
	}

	if callCtx.Err() != nil {
		return nil, ErrTimeout
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: 上游返回状态码 %d: %s", ErrFailed, resp.StatusCode, truncate(string(payload), 200))
	}

	var out wireResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("解析上游响应失败: %w", err)
	}
	return &Response{
		Content:    out.Content,
		Latency:    time.Since(started),
		UpstreamID: out.UpstreamID,
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
