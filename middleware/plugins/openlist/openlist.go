// Package openlist 对接 OpenList（AList 内核）文件服务 API。
// 提供：目录列表、文件详情、流式播放代理（含 Range）、上传。
// 作为 plugin.Adapter 实现，路由挂载到 /api/openlist 下。
package openlist

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/pelico/ddnas/middleware/internal/plugin"
)

func init() {
	plugin.Register(func() plugin.Adapter { return &Adapter{} })
}

// Adapter OpenList 适配器。
type Adapter struct {
	endpoint string
	token    string // OpenList/AList 的访问令牌
	root     string // 挂载根路径前缀，默认 "/"
	client   *http.Client
}

func (a *Adapter) Name() string { return "openlist" }

func (a *Adapter) Capabilities() []string {
	return []string{"files", "stream", "upload", "backup"}
}

func (a *Adapter) ConfigSchema() []plugin.ConfigField {
	return []plugin.ConfigField{
		{Key: "enabled", Label: "启用", Type: plugin.FieldBool, Required: false, Help: "勾选后启用该适配器"},
		{Key: "endpoint", Label: "OpenList 地址", Type: plugin.FieldURL, Required: true, Placeholder: "http://127.0.0.1:5244"},
		{Key: "token", Label: "OpenList 令牌", Type: plugin.FieldPassword, Required: true, Help: "AList 管理后台或用户的访问令牌"},
		{Key: "root", Label: "挂载根路径", Type: plugin.FieldText, Required: false, Placeholder: "/", Help: "文件浏览的根目录前缀"},
	}
}

func (a *Adapter) Init(raw map[string]any) error {
	a.endpoint = strField(raw, "endpoint", "http://127.0.0.1:5244")
	a.token = strField(raw, "token", "")
	a.root = strField(raw, "root", "/")
	if a.root == "" {
		a.root = "/"
	}
	if !strings.HasPrefix(a.root, "/") {
		a.root = "/" + a.root
	}
	a.client = &http.Client{Timeout: 60 * time.Second}
	return nil
}

func (a *Adapter) Routes() []plugin.Route {
	return []plugin.Route{
		{Method: "GET", Path: "/files/list", Desc: "列出目录", Handler: a.handleList},
		{Method: "GET", Path: "/files/get", Desc: "文件详情", Handler: a.handleGet},
		{Method: "GET", Path: "/files/stream/{path...}", Desc: "流式播放代理", Handler: a.handleStream},
		{Method: "POST", Path: "/files/upload", Desc: "上传文件", Handler: a.handleUpload},
		{Method: "POST", Path: "/files/mkdir", Desc: "新建目录", Handler: a.handleMkdir},
	}
}

// Test 以临时 client 探测 OpenList/AList：调用 /api/fs/list(path=root)，
// 解析 {code,message}，code==200 视为成功，否则展示错误提示；含耗时。
func (a *Adapter) Test(raw map[string]any) plugin.TestResult {
	endpoint := strings.TrimSpace(strField(raw, "endpoint", ""))
	token := strField(raw, "token", "")
	root := strField(raw, "root", "/")
	if root == "" {
		root = "/"
	}
	if endpoint == "" {
		return plugin.TestResult{Ok: false, Info: "未填写 OpenList/AList 地址"}
	}
	client := &http.Client{Timeout: 8 * time.Second}
	start := time.Now()
	body := map[string]any{"path": root, "page": 1, "per_page": 1, "refresh": false}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", strings.TrimRight(endpoint, "/")+"/api/fs/list", strings.NewReader(string(buf)))
	if err != nil {
		return plugin.TestResult{Ok: false, Info: "构造请求失败：" + err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return plugin.TestResult{Ok: false, Info: "连接失败：" + err.Error() + "（" + elapsed.Round(time.Millisecond).String() + "）"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return plugin.TestResult{Ok: false, Info: "HTTP " + resp.Status + "（" + elapsed.Round(time.Millisecond).String() + "），请检查地址或令牌权限"}
	}
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&out)
	if out.Code != 200 {
		msg := out.Msg
		if msg == "" {
			msg = "返回码 " + fmt.Sprint(out.Code)
		}
		return plugin.TestResult{Ok: false, Info: "OpenList/AList 错误：" + msg + "（" + elapsed.Round(time.Millisecond).String() + "）"}
	}
	return plugin.TestResult{Ok: true, Info: "成功：" + elapsed.Round(time.Millisecond).String() + " · 目录 " + root + " 可达"}
}

func (a *Adapter) Close() error { return nil }

// joinPath 拼接 root 与相对路径。
func (a *Adapter) joinPath(p string) string {
	if p == "" {
		return a.root
	}
	if strings.HasPrefix(p, "/") {
		p = strings.TrimPrefix(p, "/")
	}
	return path.Join(a.root, p)
}

// --- handlers ---

func (a *Adapter) handleList(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	body := map[string]any{
		"path":     a.joinPath(p),
		"page":     1,
		"per_page": 0,
		"refresh":  false,
	}
	resp, err := a.doJSON("POST", "/api/fs/list", body)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "调用 OpenList list 失败: "+err.Error())
		return
	}
	defer resp.Body.Close()

	var out struct {
		Code int `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			Content []map[string]any `json:"content"`
			Total    int             `json:"total"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		writeErr(w, http.StatusBadGateway, "解析 OpenList 响应失败: "+err.Error())
		return
	}
	if out.Code != 200 {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("OpenList 错误: %s", out.Msg))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out.Data.Content, "total": out.Data.Total})
}

func (a *Adapter) handleGet(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	body := map[string]any{"path": a.joinPath(p)}
	resp, err := a.doJSON("POST", "/api/fs/get", body)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "调用 OpenList get 失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	var out struct {
		Code int            `json:"code"`
		Msg  string         `json:"message"`
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		writeErr(w, http.StatusBadGateway, "解析 OpenList 响应失败: "+err.Error())
		return
	}
	if out.Code != 200 {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("OpenList 错误: %s", out.Msg))
		return
	}
	writeJSON(w, http.StatusOK, out.Data)
}

func (a *Adapter) handleStream(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("path")
	full := a.joinPath(p)
	// OpenList 直接下载地址：/d/<path>
	u := strings.TrimRight(a.endpoint, "/") + "/d" + full
	req, err := http.NewRequestWithContext(r.Context(), r.Method, u, nil)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if a.token != "" {
		req.Header.Set("Authorization", a.token)
	}
	if cr := r.Header.Get("Range"); cr != "" {
		req.Header.Set("Range", cr)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "回源 OpenList 失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	// 透传响应头与状态码
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (a *Adapter) handleUpload(w http.ResponseWriter, r *http.Request) {
	dest := r.URL.Query().Get("path") // 目标完整路径（含文件名），如 /movies/a.mp4
	if dest == "" {
		writeErr(w, http.StatusBadRequest, "缺少 path 参数")
		return
	}
	full := a.joinPath(dest)
	u := strings.TrimRight(a.endpoint, "/") + "/api/fs/put"
	req, err := http.NewRequestWithContext(r.Context(), "PUT", u, r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if a.token != "" {
		req.Header.Set("Authorization", a.token)
	}
	req.Header.Set("File-Path", url.PathEscape(full))
	req.Header.Set("As-Task", "false")
	req.Header.Set("Content-Type", "application/octet-stream")
	if ct := r.Header.Get("Content-Length"); ct != "" {
		req.Header.Set("Content-Length", ct)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "上传到 OpenList 失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("OpenList 上传失败(%d): %s", resp.StatusCode, string(body)))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": full})
}

func (a *Adapter) handleMkdir(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		writeErr(w, http.StatusBadRequest, "缺少 path 参数")
		return
	}
	full := a.joinPath(p)
	body := map[string]any{"path": full}
	resp, err := a.doJSON("POST", "/api/fs/mkdir", body)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "调用 OpenList mkdir 失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": full})
}

// --- helpers ---

func (a *Adapter) doJSON(method, urlPath string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = strings.NewReader(string(buf))
	}
	req, err := http.NewRequest(method, strings.TrimRight(a.endpoint, "/")+urlPath, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.token != "" {
		req.Header.Set("Authorization", a.token)
	}
	return a.client.Do(req)
}

func strField(raw map[string]any, key, def string) string {
	if v, ok := raw[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return def
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}
