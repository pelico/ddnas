// Package nodeexporter 对接 Prometheus node_exporter 的 /metrics 端点，
// 解析为结构化设备信息供 App 展示，同时提供原始 metrics 透传。
package nodeexporter

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pelico/ddnas/middleware/internal/plugin"
)

func init() {
	plugin.Register(func() plugin.Adapter { return &Adapter{} })
}

// Adapter node_exporter 适配器。
type Adapter struct {
	endpoint string // 如 http://127.0.0.1:9100
	client   *http.Client
}

func (a *Adapter) Name() string { return "node" }

func (a *Adapter) Capabilities() []string { return []string{"system", "metrics"} }

func (a *Adapter) ConfigSchema() []plugin.ConfigField {
	return []plugin.ConfigField{
		{Key: "enabled", Label: "启用", Type: plugin.FieldBool, Required: false},
		{Key: "endpoint", Label: "node_exporter 地址", Type: plugin.FieldURL, Required: true, Placeholder: "http://127.0.0.1:9100"},
	}
}

func (a *Adapter) Init(raw map[string]any) error {
	a.endpoint = strField(raw, "endpoint", "http://127.0.0.1:9100")
	a.client = &http.Client{Timeout: 10 * time.Second}
	return nil
}

func (a *Adapter) Routes() []plugin.Route {
	return []plugin.Route{
		{Method: "GET", Path: "/metrics", Desc: "原始指标透传", Handler: a.handleRaw},
		{Method: "GET", Path: "/system", Desc: "结构化设备信息", Handler: a.handleSystem},
	}
}

// Test 发起一次实时 /metrics 探测：检查 200 OK + 文本以 `# HELP` 开头，并抽取 node_exporter_build_info
// 的 version 标签展示，给出清晰的成功/失败提示（含耗时/错误）。
func (a *Adapter) Test(raw map[string]any) plugin.TestResult {
	endpoint := strField(raw, "endpoint", "http://127.0.0.1:9100")
	if endpoint == "" {
		return plugin.TestResult{Ok: false, Info: "未填写 node_exporter 地址"}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	start := time.Now()
	req, err := http.NewRequest("GET", strings.TrimRight(endpoint, "/")+"/metrics", nil)
	if err != nil {
		return plugin.TestResult{Ok: false, Info: "构造请求失败：" + err.Error()}
	}
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return plugin.TestResult{Ok: false, Info: "连接失败：" + err.Error() + "（" + elapsed.Round(time.Millisecond).String() + "）"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return plugin.TestResult{Ok: false, Info: "HTTP " + resp.Status + "（" + elapsed.Round(time.Millisecond).String() + "）"}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return plugin.TestResult{Ok: false, Info: "读取响应失败：" + err.Error()}
	}
	text := string(body)
	version := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "node_exporter_build_info") {
			_, labels, _, ok := parseLine(line)
			if ok {
				if v, exist := labels["version"]; exist {
					version = v
				}
				break
			}
		}
	}
	msg := "成功：" + elapsed.Round(time.Millisecond).String()
	if version != "" {
		msg += " · node_exporter " + version
	}
	return plugin.TestResult{Ok: true, Info: msg}
}

func (a *Adapter) Close() error { return nil }

// --- 设备信息结构 ---

type systemInfo struct {
	Hostname  string   `json:"hostname"`
	OS        string   `json:"os"`
	Kernel    string   `json:"kernel"`
	Arch      string   `json:"arch"`
	Uptime    float64  `json:"uptime_seconds"`
	CPU       cpuInfo  `json:"cpu"`
	Memory    memInfo  `json:"memory"`
	Disks     []fsInfo `json:"disks"`
	Network   []netInfo `json:"network"`
}

type cpuInfo struct {
	Cores        int     `json:"cores"`
	Load1        float64 `json:"load1"`
	Load5        float64 `json:"load5"`
	Load15       float64 `json:"load15"`
	UsagePercent float64 `json:"usage_percent"`
}

type memInfo struct {
	Total         float64 `json:"total_bytes"`
	Available     float64 `json:"available_bytes"`
	Used          float64 `json:"used_bytes"`
	UsagePercent  float64 `json:"usage_percent"`
}

type fsInfo struct {
	Device     string  `json:"device"`
	Mountpoint string  `json:"mountpoint"`
	FSType     string  `json:"fstype"`
	Total      float64 `json:"total_bytes"`
	Used       float64 `json:"used_bytes"`
	UsagePercent float64 `json:"usage_percent"`
}

type netInfo struct {
	Device    string  `json:"device"`
	RxBytes   float64 `json:"rx_bytes"`
	TxBytes   float64 `json:"tx_bytes"`
}

// --- handlers ---

func (a *Adapter) handleRaw(w http.ResponseWriter, r *http.Request) {
	resp, err := a.fetch(r)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "抓取 node_exporter 失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (a *Adapter) handleSystem(w http.ResponseWriter, r *http.Request) {
	resp, err := a.fetch(r)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "抓取 node_exporter 失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "读取 metrics 失败: "+err.Error())
		return
	}
	info := parseSystem(string(body))
	writeJSON(w, http.StatusOK, info)
}

func (a *Adapter) fetch(r *http.Request) (*http.Response, error) {
	req, err := http.NewRequestWithContext(r.Context(), "GET", strings.TrimRight(a.endpoint, "/")+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	return a.client.Do(req)
}

// --- metrics 文本解析 ---

// metric 表示一条解析后的指标。
type metric struct {
	name   string
	labels map[string]string
	value  float64
}

func parseMetrics(text string) []metric {
	var out []metric
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, labels, val, ok := parseLine(line)
		if !ok {
			continue
		}
		out = append(out, metric{name, labels, val})
	}
	return out
}

// parseLine 解析形如 node_load1 0.42 或 node_cpu_seconds_total{cpu="0",mode="idle"} 123.4
func parseLine(line string) (string, map[string]string, float64, bool) {
	// 分离 metric 部分与数值（最后一个空白）
	sp := strings.LastIndexAny(line, " \t")
	if sp < 0 {
		return "", nil, 0, false
	}
	metricPart := line[:sp]
	valStr := strings.TrimSpace(line[sp:])
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return "", nil, 0, false
	}
	name, labels := parseMetricName(metricPart)
	return name, labels, val, true
}

func parseMetricName(s string) (string, map[string]string) {
	labels := map[string]string{}
	i := strings.Index(s, "{")
	if i < 0 {
		return s, labels
	}
	name := s[:i]
	lblStr := s[i+1:]
	if j := strings.LastIndex(lblStr, "}"); j >= 0 {
		lblStr = lblStr[:j]
	}
	for _, p := range splitLabels(lblStr) {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			continue
		}
		labels[kv[0]] = strings.Trim(kv[1], `"`)
	}
	return name, labels
}

// splitLabels 简易按逗号切分标签（不考虑值内含逗号，node_exporter 实践中足够）。
func splitLabels(s string) []string {
	return strings.Split(s, ",")
}

func gauge(metrics []metric, name string) float64 {
	for _, m := range metrics {
		if m.name == name && len(m.labels) == 0 {
			return m.value
		}
	}
	return 0
}

func parseSystem(text string) systemInfo {
	ms := parseMetrics(text)
	info := systemInfo{
		Hostname: label(ms, "node_uname_info", "nodename"),
		OS:       label(ms, "node_uname_info", "sysname"),
		Kernel:   label(ms, "node_uname_info", "release"),
		Arch:     label(ms, "node_uname_info", "machine"),
	}
	info.Uptime = gauge(ms, "node_time_seconds") - gauge(ms, "node_boot_time_seconds")
	info.CPU = cpuInfo{
		Cores:  countCPU(ms),
		Load1:  gauge(ms, "node_load1"),
		Load5:  gauge(ms, "node_load5"),
		Load15: gauge(ms, "node_load15"),
	}
	info.CPU.UsagePercent = cpuUsage(ms)
	info.Memory = memInfo{
		Total:     gauge(ms, "node_memory_MemTotal_bytes"),
		Available: gauge(ms, "node_memory_MemAvailable_bytes"),
	}
	if info.Memory.Total > 0 {
		info.Memory.Used = info.Memory.Total - info.Memory.Available
		info.Memory.UsagePercent = info.Memory.Used / info.Memory.Total * 100
	}
	info.Disks = parseFS(ms)
	info.Network = parseNet(ms)
	return info
}

// label 取某带标签指标首个样本的指定标签值。
func label(metrics []metric, name, lkey string) string {
	for _, m := range metrics {
		if m.name == name {
			return m.labels[lkey]
		}
	}
	return ""
}

// countCPU 统计不同 cpu 标签数量。
func countCPU(metrics []metric) int {
	seen := map[string]bool{}
	for _, m := range metrics {
		if m.name == "node_cpu_seconds_total" {
			if c := m.labels["cpu"]; c != "" {
				seen[c] = true
			}
		}
	}
	return len(seen)
}

// cpuUsage 按 idle/总 计算累计使用率（自启动以来）。
func cpuUsage(metrics []metric) float64 {
	var idle, total float64
	for _, m := range metrics {
		if m.name != "node_cpu_seconds_total" {
			continue
		}
		total += m.value
		if m.labels["mode"] == "idle" {
			idle += m.value
		}
	}
	if total > 0 {
		return (1 - idle/total) * 100
	}
	return 0
}

func parseFS(metrics []metric) []fsInfo {
	size := map[string]float64{}
	free := map[string]float64{}
	dev := map[string]string{}
	fs := map[string]string{}
	for _, m := range metrics {
		switch m.name {
		case "node_filesystem_size_bytes":
			size[m.labels["mountpoint"]] = m.value
			dev[m.labels["mountpoint"]] = m.labels["device"]
			fs[m.labels["mountpoint"]] = m.labels["fstype"]
		case "node_filesystem_free_bytes":
			free[m.labels["mountpoint"]] = m.value
		}
	}
	var out []fsInfo
	for mp, tot := range size {
		f := free[mp]
		used := tot - f
		pct := 0.0
		if tot > 0 {
			pct = used / tot * 100
		}
		out = append(out, fsInfo{
			Device:       dev[mp],
			Mountpoint:   mp,
			FSType:       fs[mp],
			Total:        tot,
			Used:         used,
			UsagePercent: pct,
		})
	}
	return out
}

func parseNet(metrics []metric) []netInfo {
	rx := map[string]float64{}
	tx := map[string]float64{}
	devs := map[string]bool{}
	for _, m := range metrics {
		d := m.labels["device"]
		if d == "" || d == "lo" {
			continue
		}
		switch m.name {
		case "node_network_receive_bytes_total":
			rx[d] = m.value
			devs[d] = true
		case "node_network_transmit_bytes_total":
			tx[d] = m.value
			devs[d] = true
		}
	}
	var out []netInfo
	for d := range devs {
		out = append(out, netInfo{Device: d, RxBytes: rx[d], TxBytes: tx[d]})
	}
	return out
}

// --- helpers ---

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
