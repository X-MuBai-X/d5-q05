// Command mockupstream 是题目 05 的模拟上游模型服务。
//
// 题面共同要求第 1 节第 5 条：第三方服务可采用可控的 HTTP 模拟服务，
// 必须能触发题面要求的成功、失败、超时及重复通知场景。
//
// 场景由请求中的 scenario 字段或 X-Mock-Scenario 头指定：
//
//	success        正常返回（默认）
//	fail           返回 500，模拟上游明确失败（题面 R4）
//	timeout        挂起不响应，触发平台侧 2 秒超时判定（题面 R4）
//	duplicate      同一请求编号重复到达，返回与首次一致的内容（幂等对照）
//	slow_success   延迟 3 秒后返回成功，用于观察超时后仍可能完成的在途请求
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type wireRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	Params  map[string]any `json:"params,omitempty"`
	Request string         `json:"request_no,omitempty"`
}

type wireResponse struct {
	UpstreamID string `json:"upstream_id"`
	Content    string `json:"content"`
	Scenario   string `json:"scenario"`
}

type server struct {
	mu   sync.Mutex
	seen map[string]string // request_no -> content，用于重复通知场景
	seq  int
}

// main 是模拟上游服务的进程入口。
//
// 参数解析与启动逻辑放在 runMain 里，便于单元测试直接验证。
func main() {
	if err := runMain(os.Args[1:]); err != nil {
		log.Printf("模拟上游服务退出: %v", err)
		os.Exit(1)
	}
}

// runMain 解析命令行参数并启动模拟上游服务。
func runMain(args []string) error {
	fs := flag.NewFlagSet("mockupstream", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:9091", "监听地址")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return run(*addr)
}

// run 启动模拟上游服务，监听失败时返回错误。
func run(addr string) error {
	srv := &server{seen: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/complete", srv.handleComplete)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("模拟上游模型服务已启动: http://%s", addr)
	if err := httpServer.ListenAndServe(); err != nil {
		return err
	}
	return nil
}

// handleComplete 处理上游补全请求，按 params.scenario 返回不同的模拟结果。
//
// 支撑题面要求的四类场景（题面 R4 与共同要求「必须能触发成功、失败、超时及重复通知场景」）：
//   - success          正常返回内容
//   - fail             返回上游明确失败
//   - timeout          挂起不返回，用于触发「判定超时但上游稍后才有结果」
//   - duplicate        同一请求编号重复通知，返回与首次相同的内容
func (s *server) handleComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "仅支持 POST", http.StatusMethodNotAllowed)
		return
	}
	var req wireRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体解析失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	scenario := r.Header.Get("X-Mock-Scenario")
	if scenario == "" {
		scenario = stringParam(req.Params, "scenario")
	}
	if scenario == "" {
		scenario = "success"
	}

	switch scenario {
	case "fail":
		// 上游明确失败：平台应释放额度占用且不计成功量（题面 R4）。
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":  "upstream_internal_error",
			"detail": "模拟上游明确失败",
		})
		return
	case "timeout":
		// 挂起不响应，由平台侧 2 秒超时判定（题面 R4）。
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
		return
	case "slow_success":
		// 延迟返回成功：平台 2 秒后已判超时并归还额度，
		// 该结果属于「迟到结果」，只作诊断，不改变已返回的超时结论。
		select {
		case <-r.Context().Done():
			return
		case <-time.After(3 * time.Second):
		}
	case "duplicate":
		// 重复通知场景：同一 request_no 始终返回与首次一致的内容。
	default:
	}

	content := s.contentFor(req, scenario)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(wireResponse{
		UpstreamID: s.newID(),
		Content:    content,
		Scenario:   scenario,
	})
}

// contentFor 生成响应内容。duplicate 场景下同一 request_no 返回相同内容。
func (s *server) contentFor(req wireRequest, scenario string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := req.Request
	if key == "" {
		key = req.Prompt
	}
	if scenario == "duplicate" {
		if existing, ok := s.seen[key]; ok {
			return existing
		}
	}
	s.seen[key] = fmt.Sprintf("模拟模型输出：%s", req.Prompt)
	return s.seen[key]
}

// newID 生成模拟上游的任务编号（形如 mock-1）。
//
// 编号在进程内自增，配合 seen 缓存即可实现「重复通知不重复生成内容」。
func (s *server) newID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return fmt.Sprintf("mock-%d", s.seq)
}

// stringParam 从参数表中取字符串值，取不到或类型不符时返回空串。
//
// 场景名等可选参数都通过它读取，避免调用方到处做类型断言。
func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}
