// Package downloader 下载器适配器占位，演示未来接入 aria2/qBittorrent/transmission 等。
// 实现了 plugin.Adapter 接口与配置 schema，启用后即可在 Admin UI 与 /api/adapters 出现。
// 后续补全 Init/Routes 即可对接真实下载器，无需改动核心。
package downloader

import (
	"net/http"

	"github.com/pelico/ddnas/middleware/internal/plugin"
)

func init() {
	plugin.Register(func() plugin.Adapter { return &Adapter{} })
}

// Adapter 下载器适配器（占位）。
type Adapter struct {
	endpoint string
	rpcToken string
}

func (a *Adapter) Name() string         { return "downloader" }
func (a *Adapter) Capabilities() []string { return []string{"download"} }

func (a *Adapter) ConfigSchema() []plugin.ConfigField {
	return []plugin.ConfigField{
		{Key: "enabled", Label: "启用", Type: plugin.FieldBool, Required: false},
		{Key: "type", Label: "下载器类型", Type: plugin.FieldText, Required: true, Placeholder: "aria2", Help: "aria2 / qbittorrent / transmission"},
		{Key: "endpoint", Label: "RPC 地址", Type: plugin.FieldURL, Required: true, Placeholder: "http://127.0.0.1:6800/jsonrpc"},
		{Key: "token", Label: "RPC 密钥/密码", Type: plugin.FieldPassword, Required: false},
	}
}

func (a *Adapter) Init(raw map[string]any) error {
	// TODO: 按 type 对接不同下载器 RPC
	a.endpoint = strField(raw, "endpoint", "")
	a.rpcToken = strField(raw, "token", "")
	return nil
}

func (a *Adapter) Routes() []plugin.Route {
	return []plugin.Route{
		{Method: "GET", Path: "/status", Desc: "下载器状态（占位）", Handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"not_implemented","hint":"downloader adapter is a placeholder"}`))
		}},
	}
}

func (a *Adapter) Close() error { return nil }

func strField(raw map[string]any, key, def string) string {
	if v, ok := raw[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return def
}
