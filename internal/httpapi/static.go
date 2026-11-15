// 本文件是 httpapi 包中负责「静态资源托管」的部分：把前端构建产物交给同一个进程伺服。
//
// 为什么要它（对应需求文档 §11「演示环境」与共同要求「可访问的服务地址」）：
//   - 前端是 React 单页应用，构建后是一堆静态文件（HTML/JS/CSS）；
//   - 如果让前端另起一个服务，就会出现两个进程、两个地址、跨域与端口不一致；
//   - 由 Go 直接托管后，**一个进程、一个端口**（默认 http://127.0.0.1:8080）
//     就能同时提供 API 与界面，部署与回退都只涉及一个制品。
//
// 实现要点：
//   - net/http 的 ServeMux **不允许对同一路径重复注册**（会 panic），
//     所以这里不往 mux 上再注册 "/"，而是**在 mux 外面包一层**：
//     静态文件优先，命中不了再交给 mux（API 与后端自己的页面）。
//   - 前端目录不存在时不启用静态托管，后端可独立启动与验收。

package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// StaticDir 是前端构建产物的默认目录名（相对工作目录）。
const StaticDir = "frontend/dist"

// staticHandler 在 mux 外面包一层静态资源处理。
//
// 命中顺序：
//  1. /api/、/healthz、/readyz 交给 mux（后端自己的路径，绝不被静态文件吞掉）；
//  2. 目录下存在的文件直接返回；
//  3. 其余路径回退到 index.html，交给前端单页路由。
type staticHandler struct {
	mux   *http.ServeMux
	dir   string
	index string
	files http.Handler
}

// MountStatic 在前端构建目录存在时启用静态托管。
//
// 返回值是**新的根处理器**：调用方必须用它替换原来的 mux 处理器。
// 若目录不存在或没有 index.html，则原样返回原处理器（后端独立可用）。
func (s *Server) MountStatic(dir string) http.Handler {
	if dir == "" {
		dir = StaticDir
	}
	index := filepath.Join(dir, "index.html")
	if _, err := os.Stat(index); err != nil {
		// 前端未构建：保留服务端渲染页面，后端仍可独立运行。
		return s.mux
	}
	return &staticHandler{
		mux:   s.mux,
		dir:   dir,
		index: index,
		files: http.FileServer(http.Dir(dir)),
	}
}

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 后端自己的路径不被静态资源接管。
	if strings.HasPrefix(r.URL.Path, "/api/") ||
		r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
		h.mux.ServeHTTP(w, r)
		return
	}
	clean := filepath.Clean(r.URL.Path)
	if info, err := os.Stat(filepath.Join(h.dir, clean)); err == nil && !info.IsDir() {
		h.files.ServeHTTP(w, r)
		return
	}
	// 单页应用路由：未知路径交回前端。
	http.ServeFile(w, r, h.index)
}
