// Package admin 提供 DDNAS 的配置 Web 控制台：首次设置、登录、
// 各适配器配置表单（按 ConfigSchema 自动生成）、App 连接信息。
// 配置写入卷持久化文件，保存即触发热重载，无需重启容器。
package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/pelico/ddnas/middleware/internal/config"
	"github.com/pelico/ddnas/middleware/internal/plugin"
)

const sessionCookie = "ddnas_admin"
const sessionTTL = 12 * time.Hour

// Admin 配置控制台。
type Admin struct {
	store    *config.Store
	adapters []plugin.Adapter // 仅用于读取 ConfigSchema（静态）
	reload   func()
	sessions sync.Map         // token -> expiry
}

// New 构造 Admin。adapters 为 plugin.Build() 结果；reload 为热重载回调。
func New(store *config.Store, adapters []plugin.Adapter, reload func()) *Admin {
	return &Admin{store: store, adapters: adapters, reload: reload}
}

// Mount 将控制台路由注册到 mux。
func (a *Admin) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/", a.handleIndex)
	mux.HandleFunc("GET /admin/setup", a.handleSetup)
	mux.HandleFunc("POST /admin/setup", a.handleSetup)
	mux.HandleFunc("GET /admin/login", a.handleLogin)
	mux.HandleFunc("POST /admin/login", a.handleLogin)
	mux.HandleFunc("POST /admin/logout", a.handleLogout)
	mux.HandleFunc("GET /admin/adapter/{name}", a.handleAdapter)
	mux.HandleFunc("POST /admin/adapter/{name}", a.handleAdapter)
	// POST /admin/api/test/:name：适配器配置页"测试连接"按钮 AJAX 调用，无需先保存。
	// Body 为当前表单 x-www-form-urlencoded，按 ConfigSchema 组装 raw 后直接调 Adapter.Test 探测。
	mux.HandleFunc("POST /admin/api/test/{name}", a.authedHTML(a.handleAdapterTest))
	mux.HandleFunc("GET /admin/connection", a.handleConnection)
	mux.HandleFunc("GET /", a.handleRoot) // 根路径跳转
}

// authedHTML 仅用于控制台内 AJAX/表单：要求 admin 会话 cookie，否则 401 JSON。
func (a *Admin) authedHTML(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.store.Configured() || !a.loggedIn(r) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"ok":false,"info":"未登录，请先登录控制台"}`))
			return
		}
		next(w, r)
	}
}

func (a *Admin) handleRoot(w http.ResponseWriter, r *http.Request) {
	// 已登录直达 /portal（App 套壳首页）；否则走配置控制台流程（setup/login）。
	if a.store.Configured() && a.loggedIn(r) {
		http.Redirect(w, r, "/portal", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusFound)
}

// --- index ---

func (a *Admin) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !a.store.Configured() {
		http.Redirect(w, r, "/admin/setup", http.StatusFound)
		return
	}
	if !a.loggedIn(r) {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	type v struct {
		Name         string
		Enabled      bool
		Capabilities string
	}
	var items []v
	for _, ad := range a.adapters {
		ac := a.store.AdapterConfig(ad.Name())
		en, _ := ac["enabled"].(bool)
		items = append(items, v{Name: ad.Name(), Enabled: en, Capabilities: strings.Join(ad.Capabilities(), ", ")})
	}
	render(w, "index", map[string]any{
		"Title":      "总览",
		"Active":     "index",
		"Adapters":   items,
		"ConfigPath": a.store.Path(),
	})
}

// --- setup ---

func (a *Admin) handleSetup(w http.ResponseWriter, r *http.Request) {
	if a.store.Configured() {
		http.Redirect(w, r, "/admin/", http.StatusFound)
		return
	}
	if r.Method == "GET" {
		render(w, "setup", map[string]any{"Title": "首次设置", "Nav": false})
		return
	}
	user := strings.TrimSpace(r.FormValue("admin_user"))
	pass := r.FormValue("admin_pass")
	if user == "" || pass == "" {
		render(w, "setup", map[string]any{"Title": "首次设置", "Nav": false, "Error": "用户名和密码不能为空"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		render(w, "setup", map[string]any{"Title": "首次设置", "Nav": false, "Error": err.Error()})
		return
	}
	token := strings.TrimSpace(r.FormValue("app_token"))
	if token == "" {
		token = randToken(16)
	}
	cfg := a.store.Get()
	cfg.Auth = config.AuthCfg{AdminUser: user, AdminPass: string(hash), AppToken: token}
	if err := a.store.Save(cfg); err != nil {
		render(w, "setup", map[string]any{"Title": "首次设置", "Nav": false, "Error": err.Error()})
		return
	}
	if a.reload != nil {
		a.reload()
	}
	a.setSession(w)
	http.Redirect(w, r, redirectTarget(r, "/portal"), http.StatusFound)
}

// --- login ---

func (a *Admin) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.store.Configured() {
		http.Redirect(w, r, "/admin/setup", http.StatusFound)
		return
	}
	if r.Method == "GET" {
		render(w, "login", map[string]any{"Title": "登录", "Nav": false})
		return
	}
	user := strings.TrimSpace(r.FormValue("admin_user"))
	pass := r.FormValue("admin_pass")
	cfg := a.store.Get()
	if user != cfg.Auth.AdminUser || bcrypt.CompareHashAndPassword([]byte(cfg.Auth.AdminPass), []byte(pass)) != nil {
		render(w, "login", map[string]any{"Title": "登录", "Nav": false, "Error": "用户名或密码错误"})
		return
	}
	a.setSession(w)
	// 登录成功默认进功能页 /portal（App 套壳首页）；
	// 若带 ?redirect= 且为本站内合法相对路径，则回跳原请求页。
	http.Redirect(w, r, redirectTarget(r, "/portal"), http.StatusFound)
}

func (a *Admin) handleLogout(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(sessionCookie)
	if err == nil {
		a.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

// --- adapter config ---

// handleAdapterTest 解析 adapter 表单（与 handleAdapter POST 相同的解析逻辑），
// 调用 ad.Test(raw) 返回 plugin.TestResult JSON，供配置页即时显示绿灯/红灯与详情。
func (a *Admin) handleAdapterTest(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	name := r.PathValue("name")
	var ad plugin.Adapter
	for _, x := range a.adapters {
		if x.Name() == name {
			ad = x
			break
		}
	}
	if ad == nil {
		writeJSON(w, http.StatusNotFound, plugin.TestResult{Ok: false, Info: "未知适配器：" + name})
		return
	}
	raw := map[string]any{}
	for _, fld := range ad.ConfigSchema() {
		if fld.Type == plugin.FieldBool {
			raw[fld.Key] = r.FormValue(fld.Key) == "on"
		} else {
			raw[fld.Key] = r.FormValue(fld.Key)
		}
	}
	res := ad.Test(raw)
	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *Admin) handleAdapter(w http.ResponseWriter, r *http.Request) {
	if !a.store.Configured() || !a.loggedIn(r) {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	name := r.PathValue("name")
	var ad plugin.Adapter
	for _, x := range a.adapters {
		if x.Name() == name {
			ad = x
			break
		}
	}
	if ad == nil {
		http.NotFound(w, r)
		return
	}
	cur := a.store.AdapterConfig(name)
	if r.Method == "GET" {
		render(w, "adapter", map[string]any{
			"Title":        name,
			"Name":         name,
			"Capabilities": strings.Join(ad.Capabilities(), ", "),
			"Fields":       buildFields(ad, cur),
		})
		return
	}
	// POST: 收集表单
	saved := map[string]any{}
	for _, fld := range ad.ConfigSchema() {
		if fld.Type == plugin.FieldBool {
			saved[fld.Key] = r.FormValue(fld.Key) == "on"
		} else {
			saved[fld.Key] = r.FormValue(fld.Key)
		}
	}
	cfg := a.store.Get()
	if cfg.Adapters == nil {
		cfg.Adapters = map[string]map[string]any{}
	}
	cfg.Adapters[name] = saved
	if err := a.store.Save(cfg); err != nil {
		render(w, "adapter", map[string]any{
			"Title": name, "Name": name, "Capabilities": strings.Join(ad.Capabilities(), ", "),
			"Error": err.Error(),
			"Fields": buildFields(ad, saved),
		})
		return
	}
	if a.reload != nil {
		a.reload()
	}
	// 保存成功：从 store 重读当前配置回填表单，避免显示空值（曾导致"保存后变默认"假象）
	render(w, "adapter", map[string]any{
		"Title": name, "Name": name, "Capabilities": strings.Join(ad.Capabilities(), ", "),
		"Saved": true, "Fields": buildFields(ad, a.store.AdapterConfig(name)),
	})
}

// adapterField 是适配器配置页表单字段的视图模型：ConfigField + 当前值字符串。
// 模板用 {{if .Value}}checked{{end}} 决定 checkbox 是否勾选，因此 bool 字段
// 必须返回 "true"/"false" 而非空串，否则 enabled 永远显示为未勾选。
type adapterField struct {
	plugin.ConfigField
	Value string
}

// buildFields 按 ConfigSchema 从 raw 构造表单字段视图，统一 GET/POST 渲染逻辑。
// - string 字段直接回填原值
// - bool 字段返回 "true"/"false"，模板据此正确勾选 checkbox
func buildFields(ad plugin.Adapter, raw map[string]any) []adapterField {
	var fields []adapterField
	for _, fld := range ad.ConfigSchema() {
		val := ""
		if v, ok := raw[fld.Key]; ok {
			switch t := v.(type) {
			case string:
				val = t
			case bool:
				// 必须用 "true"/"false"，让模板 {{if .Value}}checked{{end}} 生效
				if t {
					val = "true"
				} else {
					val = ""
				}
			default:
				val = ""
			}
		}
		fields = append(fields, adapterField{ConfigField: fld, Value: val})
	}
	return fields
}

// --- connection ---

// redirectTarget 从 query 取 ?redirect= 作为登录/设置后的回跳地址。
// 仅接受本站内相对路径（以单个 "/" 开头，不含协议/主机/反斜杠，防开放重定向）。
// 非法或缺省时返回 def。
func redirectTarget(r *http.Request, def string) string {
	rt := strings.TrimSpace(r.URL.Query().Get("redirect"))
	if rt == "" || !strings.HasPrefix(rt, "/") || strings.HasPrefix(rt, "//") {
		return def
	}
	// 拒绝包含协议、反斜杠、控制字符等危险字符
	if strings.ContainsAny(rt, "\\\r\n\t") || strings.Contains(rt, "http:") || strings.Contains(rt, "https:") {
		return def
	}
	return rt
}

func (a *Admin) handleConnection(w http.ResponseWriter, r *http.Request) {
	if !a.store.Configured() || !a.loggedIn(r) {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	cfg := a.store.Get()
	// 推断外部访问地址
	host := r.Host
	if host == "" {
		host = "localhost:8080"
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	render(w, "connection", map[string]any{
		"Title":      "App 连接",
		"Active":     "conn",
		"ServerAddr": scheme + "://" + host,
		"AppToken":   cfg.Auth.AppToken,
	})
}

// --- session helpers ---

func (a *Admin) setSession(w http.ResponseWriter) {
	tok := randToken(24)
	a.sessions.Store(tok, time.Now().Add(sessionTTL))
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds())})
}

func (a *Admin) loggedIn(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	v, ok := a.sessions.Load(c.Value)
	if !ok {
		return false
	}
	exp, _ := v.(time.Time)
	if time.Now().After(exp) {
		a.sessions.Delete(c.Value)
		return false
	}
	return true
}

// LoggedIn 报告当前请求是否持有有效 admin 会话 cookie。
// 供 server 包为 /portal 页面与 /portal/api/* 接口做 cookie 会话鉴权。
func (a *Admin) LoggedIn(r *http.Request) bool { return a.loggedIn(r) }

// AuthedAPI 要求 admin 会话；失败返回 401 JSON。
// 供 /portal/api/* 等 JSON 接口（WebView 同源 fetch 与原生 ExoPlayer 注入 cookie 访问）。
func (a *Admin) AuthedAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.loggedIn(r) {
			next(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}
}

func randToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
