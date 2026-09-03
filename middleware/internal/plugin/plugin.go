// Package plugin 定义适配器（Adapter）插件接口与注册表。
// 每个 adapter 声明自身能力、配置表单 schema 与路由，
// 中间件据此自动初始化、挂载路由、并在 Admin UI 渲染配置表单。
// 新增上游 API 只需实现 Adapter 接口并在 init() 中 Register，
// 路由与配置 UI 自动接入，核心无需改动。
package plugin

import "net/http"

// FieldType 表单字段类型，供 Admin UI 渲染。
const (
	FieldText     = "text"
	FieldPassword = "password"
	FieldURL      = "url"
	FieldNumber   = "number"
	FieldBool     = "bool"
	FieldTextarea = "textarea"
)

// ConfigField 描述 adapter 的一项配置，Admin UI 据此自动生成表单。
type ConfigField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
}

// Route 由 adapter 声明的、挂载到 /api/<adapter-name> 下的子路由。
// Path 为相对路径，如 "/files/list"；最终路径为 /api/<name><Path>。
type Route struct {
	Method  string
	Path    string
	Desc    string
	Handler http.HandlerFunc
}

// TestResult 是适配器"测试连接"的返回结构。
// UI 会根据 Ok 显示绿灯/红灯，并展示 Info 作为诊断信息（如版本号/返回的 HTTP 状态/耗时）。
type TestResult struct {
	Ok   bool   `json:"ok"`
	Info string `json:"info"` // 人类可读提示，如 "成功：node_exporter build 0.27.1 · 28ms" 或 "失败：拨号超时 192.168.1.10:9100"
}

// Adapter 适配器插件接口。
type Adapter interface {
	// Name 唯一标识，用作路由前缀与配置段键名（如 "openlist"）。
	Name() string
	// Capabilities 人类可读的能力标签，如 ["files","stream","upload"]，供 App 发现可用功能。
	Capabilities() []string
	// ConfigSchema 配置表单 schema，UI 自动渲染。
	ConfigSchema() []ConfigField
	// Init 用配置段初始化。raw 为该 adapter 的配置 map；enabled 由调用方在调用前判断。
	Init(raw map[string]any) error
	// Test 用配置段发起一次真实的探测（无需影响现有 Init 状态），返回连接是否可用与详细提示。
	// Admin 控制台的"测试连接"按钮直接调用此方法，用于"配完立刻知道通不通"的即时反馈。
	Test(raw map[string]any) TestResult
	// Routes 返回对外暴露的子路由；未启用时返回 nil。
	Routes() []Route
	// Close 释放资源。
	Close() error
}

// factory 适配器构造函数。
type factory func() Adapter

var (
	registry []factory
)

// Register 注册一个 adapter 构造函数，通常在各 adapter 包的 init() 中调用。
func Register(f factory) {
	registry = append(registry, f)
}

// Build 实例化所有已注册 adapter，返回实例切片。
// 启用与否由中间件根据配置在调用 Init 时决定。
func Build() []Adapter {
	out := make([]Adapter, 0, len(registry))
	for _, f := range registry {
		out = append(out, f())
	}
	return out
}
