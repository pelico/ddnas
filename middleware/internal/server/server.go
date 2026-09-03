// Package server 组装中间件 HTTP 服务：挂载 Admin 控制台、对外 API、
// 各适配器路由；支持配置变更后热重载（重建 mux，不重启进程）。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/pelico/ddnas/middleware/internal/admin"
	"github.com/pelico/ddnas/middleware/internal/config"
	"github.com/pelico/ddnas/middleware/internal/plugin"
)

// Server DDNAS 中间件服务。
type Server struct {
	store    *config.Store
	adapters []plugin.Adapter // schema 来源（plugin.Build() 一次）
	admin    *admin.Admin

	mu   sync.RWMutex
	mux  *http.ServeMux
	srv  *http.Server
	active []plugin.Adapter // 当前已初始化的实例，供关闭
}

// New 构造服务。reload 回调由调用方设为 s.Reload。
func New(store *config.Store, adapters []plugin.Adapter) *Server {
	s := &Server{store: store, adapters: adapters}
	s.admin = admin.New(store, adapters, s.Reload)
	return s
}

// Reload 根据当前配置重建活跃适配器与路由。
func (s *Server) Reload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 关闭旧实例
	for _, a := range s.active {
		_ = a.Close()
	}
	s.active = nil

	cfg := s.store.Get()
	mux := http.NewServeMux()
	s.admin.Mount(mux)

	// /portal 页面：App 套壳 WebView 加载，cookie 会话鉴权。
	mux.HandleFunc("GET /portal", s.servePortal)

	// /api/adapters 发现接口（Bearer）
	mux.HandleFunc("GET /api/adapters", s.authed(s.handleAdapters, cfg.Auth.AppToken))
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	// /portal/api/health 供 App 套壳探测：cookie 会话鉴权。
	mux.HandleFunc("GET /portal/api/health", s.admin.AuthedAPI(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))

	// 挂载各启用适配器：同时以 Bearer（/api/*，供外部程序化访问）与
	// cookie 会话（/portal/api/*，供 WebView 同源 fetch 与原生 ExoPlayer 注入 cookie）两种鉴权镜像挂载。
	for _, a := range s.adapters {
		raw := s.store.AdapterConfig(a.Name())
		if en, _ := raw["enabled"].(bool); !en {
			continue
		}
		if err := a.Init(raw); err != nil {
			continue
		}
		s.active = append(s.active, a)
		for _, rt := range a.Routes() {
			full := "/api/" + a.Name() + rt.Path
			mux.HandleFunc(rt.Method+" "+full, s.authed(rt.Handler, cfg.Auth.AppToken))
			portal := "/portal/api/" + a.Name() + rt.Path
			mux.HandleFunc(rt.Method+" "+portal, s.admin.AuthedAPI(rt.Handler))
		}
	}
	s.mux = mux
}

// Run 启动 HTTP 服务。
func (s *Server) Run(addr string) error {
	s.Reload()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		m := s.mux
		s.mu.RUnlock()
		if m == nil {
			http.Error(w, "service not ready", http.StatusServiceUnavailable)
			return
		}
		m.ServeHTTP(w, r)
	})
	s.srv = &http.Server{Addr: addr, Handler: handler}
	return s.srv.ListenAndServe()
}

// Shutdown 优雅关闭。
func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv != nil {
		return s.srv.Shutdown(ctx)
	}
	return nil
}

// --- discovery ---

func (s *Server) handleAdapters(w http.ResponseWriter, r *http.Request) {
	type routeInfo struct {
		Method string `json:"method"`
		Path    string `json:"path"`
		Desc    string `json:"desc"`
	}
	type adapterInfo struct {
		Name         string       `json:"name"`
		Enabled      bool         `json:"enabled"`
		Capabilities []string     `json:"capabilities"`
		Routes       []routeInfo  `json:"routes"`
	}
	var out []adapterInfo
	for _, a := range s.adapters {
		raw := s.store.AdapterConfig(a.Name())
		en, _ := raw["enabled"].(bool)
		var rs []routeInfo
		for _, rt := range a.Routes() {
			rs = append(rs, routeInfo{Method: rt.Method, Path: "/api/" + a.Name() + rt.Path, Desc: rt.Desc})
		}
		out = append(out, adapterInfo{
			Name: a.Name(), Enabled: en, Capabilities: a.Capabilities(), Routes: rs,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"adapters": out})
}

// --- auth ---

func (s *Server) authed(h http.HandlerFunc, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token == "" || checkToken(r, token) {
			h(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
	}
}

func checkToken(r *http.Request, token string) bool {
	// Authorization: Bearer <token>
	auth := r.Header.Get("Authorization")
	if auth != "" {
		if strings.HasPrefix(auth, "Bearer ") && strings.TrimSpace(auth[7:]) == token {
			return true
		}
	}
	// 兜底 query
	if r.URL.Query().Get("token") == token {
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
