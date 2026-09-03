// Package downloader 下载器适配器占位，演示未来接入 aria2/qBittorrent/transmission 等。
// 实现了 plugin.Adapter 接口与配置 schema，启用后即可在 Admin UI 与 /api/adapters 出现。
// 后续补全 Init/Routes 即可对接真实下载器，无需改动核心。
package downloader

import (
	"net/http"
	"strings"
	"time"

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

// Test 下载器适配器：占位实现，仅尝试 HEAD endpoint 判断 HTTP 是否可达。
// 真实类型（aria2/qbittorrent/...）上线后替换为对应 RPC 探测。
func (a *Adapter) Test(raw map[string]any) plugin.TestResult {
	typ := strings.TrimSpace(strField(raw, "type", ""))
	endpoint := strings.TrimSpace(strField(raw, "endpoint", ""))
	if endpoint == "" {
		return plugin.TestResult{Ok: false, Info: "未填写 RPC 地址"}
	}
	if typ == "" {
		return plugin.TestResult{Ok: false, Info: "请先选择下载器类型（aria2 / qbittorrent / transmission）"}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	start := time.Now()
	req, _ := http.NewRequest("GET", endpoint, nil)
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return plugin.TestResult{Ok: false, Info: "连接失败：" + err.Error() + "（" + elapsed.Round(time.Millisecond).String() + "）"}
	}
	defer resp.Body.Close()
	return plugin.TestResult{Ok: true, Info: "可达（占位探测）：HTTP " + resp.Status + " · 类型 " + typ + " · " + elapsed.Round(time.Millisecond).String() + " · 真实 RPC 校验待后续接入"}
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
