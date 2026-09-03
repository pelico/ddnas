package admin

import (
	"html/template"
	"io"
)

// 极简内联 CSS，无外部依赖，保持镜像体积小。
const layoutSrc = `{{define "layout"}}<!doctype html>
<html lang="zh-CN"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} - DDNAS</title>
<style>
:root{--bg:#0f1115;--card:#1a1d24;--fg:#e6e6e6;--muted:#8a8f99;--accent:#4f9cff;--ok:#3ecf8e;--warn:#f5a623;--bd:#2a2e37}
*{box-sizing:border-box}
body{margin:0;font-family:system-ui,Segoe UI,Roboto,sans-serif;background:var(--bg);color:var(--fg)}
a{color:var(--accent);text-decoration:none}
.wrap{max-width:760px;margin:0 auto;padding:24px 16px 80px}
header{display:flex;align-items:center;justify-content:space-between;padding:14px 16px;border-bottom:1px solid var(--bd);background:var(--card)}
header .brand{font-weight:700;letter-spacing:.5px}
nav a{margin-left:14px;color:var(--muted);font-size:14px}
nav a.on{color:var(--accent)}
.card{background:var(--card);border:1px solid var(--bd);border-radius:12px;padding:18px;margin-bottom:16px}
.card h2{margin:0 0 4px;font-size:18px}
.card .sub{color:var(--muted);font-size:13px;margin-bottom:14px}
label{display:block;font-size:13px;color:var(--muted);margin:12px 0 4px}
input,select,textarea{width:100%;padding:9px 11px;border-radius:8px;border:1px solid var(--bd);background:#0c0e12;color:var(--fg);font-size:14px}
input[type=checkbox]{width:auto}
.row{display:flex;align-items:center;gap:8px}
button{background:var(--accent);color:#fff;border:0;border-radius:8px;padding:10px 18px;font-size:14px;cursor:pointer}
button.ghost{background:transparent;border:1px solid var(--bd);color:var(--muted)}
.badge{display:inline-block;font-size:12px;padding:2px 8px;border-radius:999px;background:#0c0e12;border:1px solid var(--bd);color:var(--muted)}
.badge.on{color:var(--ok);border-color:#1f3d2e}
.list{display:flex;flex-direction:column;gap:10px}
.item{display:flex;justify-content:space-between;align-items:center;padding:12px 14px;border:1px solid var(--bd);border-radius:10px;background:#0c0e12}
.item .meta{color:var(--muted);font-size:12px}
.kv{display:flex;justify-content:space-between;padding:6px 0;border-bottom:1px dashed var(--bd);font-size:13px}
.kv:last-child{border-bottom:0}
.mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;background:#0c0e12;padding:2px 6px;border-radius:6px}
.alert{padding:10px 12px;border-radius:8px;background:#2a1d12;border:1px solid #4a3520;color:var(--warn);font-size:13px;margin-bottom:12px}
.alert.ok{background:#122418;border-color:#1f3d2e;color:var(--ok)}
.hint{color:var(--muted);font-size:12px;margin-top:6px}
</style>
</head><body>
<header><div class="brand">DDNAS</div><nav>{{if .Nav}}<a href="/admin/" {{if eq .Active "index"}}class="on"{{end}}>总览</a><a href="/admin/connection" {{if eq .Active "conn"}}class="on"{{end}}>App 连接</a>{{end}}</nav></header>
<div class="wrap">{{block "content" .}}{{end}}</div>
</body></html>{{end}}`

const setupSrc = `{{define "content"}}
<div class="card"><h2>首次设置</h2><div class="sub">创建管理员账号与 App 连接令牌，完成后即可配置各适配器。</div>
{{if .Error}}<div class="alert">{{.Error}}</div>{{end}}
<form method="POST" action="/admin/setup">
<label>管理员用户名</label><input name="admin_user" value="{{.AdminUser}}" required>
<label>管理员密码</label><input type="password" name="admin_pass" required>
<label>App 访问令牌（留空自动生成）</label><input name="app_token" placeholder="自动生成">
<div class="hint">App 端用此令牌访问中间件 /api/* 接口，可在「App 连接」页随时查看。</div>
<button type="submit">完成设置</button>
</form></div>{{end}}`

const loginSrc = `{{define "content"}}
<div class="card"><h2>登录</h2><div class="sub">使用管理员账号登录 DDNAS 控制台。</div>
{{if .Error}}<div class="alert">{{.Error}}</div>{{end}}
<form method="POST" action="/admin/login">
<label>用户名</label><input name="admin_user" required>
<label>密码</label><input type="password" name="admin_pass" required>
<button type="submit">登录</button>
</form></div>{{end}}`

const indexSrc = `{{define "content"}}
<div class="card"><h2>适配器总览</h2><div class="sub">点击进入各上游适配器配置。启用并填写参数后保存即热重载，无需重启容器。</div>
<div class="list">{{range .Adapters}}
<div class="item"><div><div>{{.Name}} <span class="badge {{if .Enabled}}on{{end}}">{{if .Enabled}}已启用{{else}}未启用{{end}}</span></div><div class="meta">{{.Capabilities}}</div></div><div><a href="/admin/adapter/{{.Name}}">配置 →</a></div></div>
{{end}}</div></div>
<div class="card"><h2>持久化</h2>
<div class="kv"><span>配置文件</span><span class="mono">{{.ConfigPath}}</span></div>
<div class="kv"><span>建议映射卷</span><span class="mono">-v /your/path:/data</span></div>
<div class="hint">所有配置存于卷内，容器重建不丢失。</div>
</div>{{end}}`

const adapterSrc = `{{define "content"}}
<div class="card"><h2>{{.Name}} 配置</h2><div class="sub">{{.Capabilities}}</div>
{{if .Saved}}<div class="alert ok">已保存并热重载。</div>{{end}}
{{if .Error}}<div class="alert">{{.Error}}</div>{{end}}
<form method="POST" action="/admin/adapter/{{.Name}}">{{range .Fields}}
<label>{{.Label}}{{if .Required}} *{{end}}</label>
{{if eq .Type "bool"}}<div class="row"><input type="checkbox" name="{{.Key}}" {{if .Value}}checked{{end}}></div>
{{else if eq .Type "textarea"}}<textarea name="{{.Key}}" placeholder="{{.Placeholder}}">{{.Value}}</textarea>
{{else}}<input name="{{.Key}}" type="{{.Type}}" placeholder="{{.Placeholder}}" value="{{.Value}}">{{end}}
{{if .Help}}<div class="hint">{{.Help}}</div>{{end}}
{{end}}
<button type="submit">保存并重载</button>
<a href="/admin/"><button type="button" class="ghost">返回</button></a>
</form></div>{{end}}`

const connectionSrc = `{{define "content"}}
<div class="card"><h2>App 连接信息</h2><div class="sub">在 Android App 中填入以下信息连接本中间件。</div>
<div class="kv"><span>服务器地址</span><span class="mono">{{.ServerAddr}}</span></div>
<div class="kv"><span>App 令牌</span><span class="mono">{{.AppToken}}</span></div>
<div class="hint">App 令牌即 Authorization Bearer，用于访问 /api/* 全部接口。</div>
</div>
<div class="card"><h2>可用接口</h2>
<div class="kv"><span>适配器发现</span><span class="mono">GET /api/adapters</span></div>
<div class="kv"><span>设备信息</span><span class="mono">GET /api/node/system</span></div>
<div class="kv"><span>文件列表</span><span class="mono">GET /api/openlist/files/list?path=</span></div>
<div class="kv"><span>流式播放</span><span class="mono">GET /api/openlist/files/stream/{path}</span></div>
<div class="kv"><span>上传</span><span class="mono">POST /api/openlist/files/upload?path=</span></div>
</div>{{end}}`

var pages = map[string]string{
	"setup":      setupSrc,
	"login":      loginSrc,
	"index":      indexSrc,
	"adapter":    adapterSrc,
	"connection": connectionSrc,
}

var parsed = map[string]*template.Template{}

func init() {
	base := template.Must(template.New("base").Parse(layoutSrc))
	for name, src := range pages {
		t, err := base.Clone()
		if err != nil {
			panic(err)
		}
		if _, err := t.Parse(src); err != nil {
			panic(err)
		}
		parsed[name] = t
	}
}

// render 渲染指定页面。data 为模板数据 map，自动补全 Title/Active/Nav。
func render(w io.Writer, page string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	if _, ok := data["Title"]; !ok {
		data["Title"] = "DDNAS"
	}
	if _, ok := data["Active"]; !ok {
		data["Active"] = ""
	}
	if _, ok := data["Nav"]; !ok {
		data["Nav"] = true
	}
	// Execute 执行的是与 template 自身同名("base")的定义块，而该块为空
	// （真正的 HTML 在 {{define "layout"}} 里），导致页面空白。
	// 用 ExecuteTemplate 显式执行 "layout" 块，其内部 {{block "content" .}}
	// 才会用各页面的 content 覆盖块渲染出完整页面。
	return parsed[page].ExecuteTemplate(w, "layout", data)
}
