// Package admin 提供 DDNAS 的配置 Web 控制台：首次设置、登录、
// 各适配器配置表单（按 ConfigSchema 自动生成）、App 连接信息。
// 配置写入卷持久化文件，保存即触发热重载，无需重启容器。
package admin

import (
	"crypto/rand"
	"encoding/hex"
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
	mux.HandleFunc("GET /admin/connection", a.handleConnection)
	mux.HandleFunc("GET /", a.handleRoot) // 根路径跳转
}

func (a *Admin) handleRoot(w http.ResponseWriter, r *http.Request) {
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
	http.Redirect(w, r, "/admin/", http.StatusFound)
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
	http.Redirect(w, r, "/admin/", http.StatusFound)
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
		type f struct {
			plugin.ConfigField
			Value string
		}
		var fields []f
		for _, fld := range ad.ConfigSchema() {
			val := ""
			if v, ok := cur[fld.Key]; ok {
				switch t := v.(type) {
				case string:
					val = t
				case bool:
					val = ""
				default:
					val = ""
				}
			}
			fields = append(fields, f{ConfigField: fld, Value: val})
		}
		render(w, "adapter", map[string]any{
			"Title":        name,
			"Name":         name,
			"Capabilities": strings.Join(ad.Capabilities(), ", "),
			"Fields":       fields,
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
		})
		return
	}
	if a.reload != nil {
		a.reload()
	}
	// 重新渲染带 Saved 标记：重新读取字段
	type f struct {
		plugin.ConfigField
		Value string
	}
	var fields []f
	for _, fld := range ad.ConfigSchema() {
		fields = append(fields, f{ConfigField: fld})
	}
	render(w, "adapter", map[string]any{
		"Title": name, "Name": name, "Capabilities": strings.Join(ad.Capabilities(), ", "),
		"Saved": true, "Fields": fields,
	})
}

// --- connection ---

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

func randToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
