// Package openlist 对接 OpenList（AList 内核）文件服务 API。
// 提供：目录列表、文件详情、流式播放代理（含 Range）、上传。
// 作为 plugin.Adapter 实现，路由挂载到 /api/openlist 下。
package openlist

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	endpoint     string
	token        string // OpenList/AList 的访问令牌（JWT）
	root         string // 挂载根路径前缀，默认 "/"
	client       *http.Client // 普通 API 调用（list/get/mkdir），带整体超时
	streamClient *http.Client // 流代理专用：不设整体超时，仅限响应头到达时间，避免大文件中途断流
	uploadClient *http.Client // 上传专用：无整体超时，大文件（GB 级）传完才收响应头
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
	// stream client：整体无超时，仅给响应头 30s 超时，body 可慢速读取（云盘 / 大文件）
	a.streamClient = &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
	}
	// upload client：大文件上传可能耗时很长（GB 级 + 慢链路），
	// 无整体超时、无 ResponseHeaderTimeout（响应头在 body 传完后才返回）
	a.uploadClient = &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			ResponseHeaderTimeout: 0,
			IdleConnTimeout:       90 * time.Second,
		},
	}
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

// joinPath 拼接 root 与相对路径。拒绝含 .. 的路径以防穿越。
func (a *Adapter) joinPath(p string) string {
	if p == "" {
		return a.root
	}
	// 防路径穿越：拒绝含 .. 的路径
	if strings.Contains(p, "..") {
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

// getRawURL 调用 /api/fs/get 获取文件直链（raw_url，带 sign）。
// 复用与 list 接口相同的 JWT 鉴权，绕过 /d/ 接口 401 问题。
// raw_url 可能是 AList 自身 /d/?sign=xxx，也可能是云盘原始下载地址。
func (a *Adapter) getRawURL(full string) (string, error) {
	resp, err := a.doJSON("POST", "/api/fs/get", map[string]any{"path": full})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			RawURL    string `json:"raw_url"`
			Name       string `json:"name"`
			IsDir      bool   `json:"is_dir"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Code != 200 {
		return "", fmt.Errorf("OpenList 错误: %s", out.Msg)
	}
	if out.Data.IsDir {
		return "", fmt.Errorf("目标是目录，非文件")
	}
	if out.Data.RawURL == "" {
		return "", fmt.Errorf("raw_url 为空")
	}
	return out.Data.RawURL, nil
}

func (a *Adapter) handleStream(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("path")
	full := a.joinPath(p)

	// 1) 先调 /api/fs/get 获取带 sign 的直链 raw_url
	rawURL, err := a.getRawURL(full)
	if err != nil {
		log.Printf("[openlist] stream 获取 raw_url 失败 path=%s err=%v", full, err)
		writeErr(w, http.StatusBadGateway, "获取文件直链失败: "+err.Error())
		return
	}

	// 2) 用 streamClient 代理 raw_url（云盘直链可能慢，用无整体超时的 client）
	req, err := http.NewRequestWithContext(r.Context(), r.Method, rawURL, nil)
	if err != nil {
		log.Printf("[openlist] stream 构造请求失败 raw_url=%s err=%v", rawURL, err)
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// raw_url 已带 sign，不再需要 Authorization 头（云盘直链不认 JWT）
	if cr := r.Header.Get("Range"); cr != "" {
		req.Header.Set("Range", cr)
	}
	// 标识 UA，部分云盘（如阿里云盘）需要
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10) AppleWebKit/537.36 DDNAS/1.1")

	resp, err := a.streamClient.Do(req)
	if err != nil {
		log.Printf("[openlist] stream 回源失败 raw_url=%s err=%v", rawURL, err)
		writeErr(w, http.StatusBadGateway, "回源直链失败: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// 4xx/5xx：读取响应体帮助定位
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		log.Printf("[openlist] stream 上游错误 status=%d raw_url=%s range=%s ctype=%s body=%s",
			resp.StatusCode, rawURL, r.Header.Get("Range"), resp.Header.Get("Content-Type"), strings.TrimSpace(string(body)))
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("直链返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
		return
	}

	log.Printf("[openlist] stream ok status=%d ctype=%s len=%s raw_url=%s",
		resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Length"), rawURL)

	// 透传响应头与状态码
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Content-Disposition", "ETag", "Last-Modified"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
	resp, err := a.uploadClient.Do(req)
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
	upBody, _ := io.ReadAll(resp.Body)
	// 上游 HTTP 非 2xx 直接判失败（与 uploadFile 一致）
	if resp.StatusCode >= 300 {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("OpenList mkdir 失败(%d): %s", resp.StatusCode, string(upBody)))
		return
	}
	// OpenList/AList 用 {"code":200,...} 表示业务结果，HTTP 恒 200；
	// code 非 0 且非 200 视为业务失败（如无权限/路径非法），避免假阳性 ok:true
	var aj struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(upBody, &aj) == nil && aj.Code != 0 && aj.Code != 200 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": aj.Message, "code": aj.Code, "path": full})
		return
	}
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
