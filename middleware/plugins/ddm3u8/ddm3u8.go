// Package ddm3u8 对接 DDM3U8（Flask + N_m3u8DL-RE 的 M3U8 下载器，独立 Docker 容器）。
//
// 设计为薄反代层：所有业务逻辑（任务调度、下载、合并、状态机）都由 DDM3U8 自行处理，
// 本适配器只做三件事：
//  1. 注入 Basic Auth（对齐 DDM3U8 的 WEB_USER/WEB_PASS），免去前端逐请求带凭证；
//  2. 把 /download/* 子路由透传到 DDM3U8 的 /api/* 与 /down，原样回传响应；
//  3. 声明 ["download"] 能力，由 server.go 的能力路由层挂到通用 /api/download/* 与
//     /portal/api/download/*，前端只依赖能力名，不耦合 adapter 名。
//
// 不在中间件层缓存任务到 SQLite（DDM3U8 自带 tasks_history.json 持久化 + /api/tasks 实时返回），
// 避免双写冗余；store.download_tasks 表预留给后续离线统计需求。
package ddm3u8

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pelico/ddnas/middleware/internal/plugin"
)

func init() {
	plugin.Register(func() plugin.Adapter { return &Adapter{} })
}

// Adapter DDM3U8 适配器。
type Adapter struct {
	endpoint string // DDM3U8 根地址，如 http://127.0.0.1:8080
	user     string // WEB_USER
	pass     string // WEB_PASS
	client   *http.Client
}

func (a *Adapter) Name() string         { return "ddm3u8" }
func (a *Adapter) Capabilities() []string { return []string{"download"} }

func (a *Adapter) ConfigSchema() []plugin.ConfigField {
	return []plugin.ConfigField{
		{Key: "enabled", Label: "启用", Type: plugin.FieldBool, Required: false, Help: "勾选后启用该适配器"},
		{Key: "endpoint", Label: "DDM3U8 地址", Type: plugin.FieldURL, Required: true, Placeholder: "http://127.0.0.1:8080"},
		{Key: "user", Label: "Web 用户名", Type: plugin.FieldText, Required: false, Help: "对应 DDM3U8 的 WEB_USER，留空表示 DDM3U8 未启用鉴权"},
		{Key: "pass", Label: "Web 密码", Type: plugin.FieldPassword, Required: false, Help: "对应 DDM3U8 的 WEB_PASS"},
	}
}

func (a *Adapter) Init(raw map[string]any) error {
	a.endpoint = strings.TrimRight(strField(raw, "endpoint", "http://127.0.0.1:8080"), "/")
	a.user = strField(raw, "user", "")
	a.pass = strField(raw, "pass", "")
	// 下载/合并耗时较长，给足超时；提交任务本身很快但有子目录创建，60s 够用
	a.client = &http.Client{Timeout: 60 * time.Second}
	return nil
}

// Test 探测 DDM3U8 /health：仅判断地址可达 + 鉴权是否匹配，不依赖下载核心就绪。
// /ready 才会校验 ffmpeg/下载核心，那属于"就绪"而非"连通性"，测试连接用 /health 更合适。
func (a *Adapter) Test(raw map[string]any) plugin.TestResult {
	endpoint := strings.TrimRight(strings.TrimSpace(strField(raw, "endpoint", "")), "/")
	if endpoint == "" {
		return plugin.TestResult{Ok: false, Info: "未填写 DDM3U8 地址"}
	}
	user := strField(raw, "user", "")
	pass := strField(raw, "pass", "")
	client := &http.Client{Timeout: 8 * time.Second}
	start := time.Now()
	req, err := http.NewRequest("GET", endpoint+"/health", nil)
	if err != nil {
		return plugin.TestResult{Ok: false, Info: "构造请求失败：" + err.Error()}
	}
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := client.Do(req)
	elapsed := time.Since(start).Round(time.Millisecond)
	if err != nil {
		return plugin.TestResult{Ok: false, Info: "连接失败：" + err.Error() + "（" + elapsed.String() + "）"}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return plugin.TestResult{Ok: false, Info: "鉴权失败：用户名/密码错误（" + elapsed.String() + "）"}
	}
	if resp.StatusCode != http.StatusOK {
		return plugin.TestResult{Ok: false, Info: "HTTP " + resp.Status + "（" + elapsed.String() + "）"}
	}
	return plugin.TestResult{Ok: true, Info: "成功：DDM3U8 可达（" + elapsed.String() + "）"}
}

// Routes 对外暴露 /download/* 子路由，能力路由层会挂到通用 /api/download/* 与 /portal/api/download/*。
// 路径设计对齐前端语义（tasks/submit/task/clear/files），上游路径在 handler 内映射到 DDM3U8 原生路径。
func (a *Adapter) Routes() []plugin.Route {
	return []plugin.Route{
		{Method: "GET", Path: "/download/tasks", Desc: "任务列表（含活跃数/并发上限）", Handler: a.proxy("/api/tasks")},
		{Method: "POST", Path: "/download/submit", Desc: "提交下载（form: url/name/referer/sub_path）", Handler: a.proxy("/down")},
		{Method: "POST", Path: "/download/task/{id}", Desc: "任务操作（JSON: action=pause|resume|cancel|merge）", Handler: a.handleTaskAction},
		{Method: "POST", Path: "/download/clear", Desc: "清除所有已结束任务", Handler: a.proxy("/api/clear")},
		{Method: "POST", Path: "/download/clear-selected", Desc: "清除指定任务（JSON: ids[]）", Handler: a.proxy("/api/clear-selected")},
		{Method: "GET", Path: "/download/files", Desc: "已下载视频文件列表", Handler: a.proxy("/api/video_files")},
		{Method: "GET", Path: "/download/folders", Desc: "下载目录列表（一层子目录）", Handler: a.proxy("/api/folders")},
	}
}

func (a *Adapter) Close() error { return nil }

// proxy 返回一个把请求透传到 DDM3U8 upstreamPath 的 handler。
// 透传 method/body/Content-Type，注入 Basic Auth，原样回传状态码与响应体。
// form-data 与 JSON body 都能流式转发（r.Body 不缓存到内存）。
func (a *Adapter) proxy(upstreamPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.proxyTo(upstreamPath, w, r)
	}
}

// handleTaskAction 取路径参数 {id} 拼到上游 /api/task/<id>，其余透传。
// DDM3U8 的 action（pause/resume/cancel/merge）在 JSON body 里，原样转发即可。
func (a *Adapter) handleTaskAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "缺少任务 ID")
		return
	}
	a.proxyTo("/api/task/"+id, w, r)
}

// proxyTo 把当前请求转发到 DDM3U8 的 upstreamPath。
// 鉴权由本层注入，前端/外部调用方无需带 Basic Auth。
func (a *Adapter) proxyTo(upstreamPath string, w http.ResponseWriter, r *http.Request) {
	if a.endpoint == "" {
		writeErr(w, http.StatusServiceUnavailable, "DDM3U8 适配器未初始化")
		return
	}
	url := a.endpoint + upstreamPath
	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, r.Body)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "构造上游请求失败: "+err.Error())
		return
	}
	// 透传 Content-Length：form-data 必须带正确长度，否则 http client 用 chunked
	// 传输，Flask request.form 解析失败，DDM3U8 收不到 url/sub_path 等字段，
	// 报"URL不能为空"或无法创建临时目录
	req.ContentLength = r.ContentLength
	if a.user != "" || a.pass != "" {
		req.SetBasicAuth(a.user, a.pass)
	}
	// 透传 Content-Type：form-data 的 boundary、JSON 的 application/json 都必须保留
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "调用 DDM3U8 失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	// 原样回传：状态码 + Content-Type + 响应体
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// --- helpers ---

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, `{"error":`+jsonStr(msg)+`}`)
}

// jsonStr 极简 JSON 字符串转义，避免引 import encoding/json 开销。
func jsonStr(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range s {
		switch c {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			b.WriteRune(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func strField(raw map[string]any, key, def string) string {
	if v, ok := raw[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return def
}
