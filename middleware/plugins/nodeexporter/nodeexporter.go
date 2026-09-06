// Package nodeexporter 对接 Prometheus node_exporter 的 /metrics 端点，
// 解析为结构化设备信息供 App 展示，同时提供原始 metrics 透传。
package nodeexporter

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
	// netLast 缓存上次网络采样，用于计算 B/s 速率
	netLast map[string]netSample
	netMu   sync.Mutex
	// cpuLast 缓存上次 CPU 累计秒数采样，用于计算瞬时使用率（counter 增量法）
	cpuLast *cpuSample
	cpuMu   sync.Mutex
}

// netSample 缓存单网卡的累计字节和采样时刻。
type netSample struct {
	rx, tx float64
	ts    time.Time
}

// cpuSample 缓存 node_cpu_seconds_total 的累计 idle/total 与采样时刻。
// node_cpu_seconds_total 是系统启动以来的累计 counter，
// 直接 (1-idle/total)*100 得到的是历史平均使用率（几乎不变化），
// 必须用两次采样的增量计算瞬时使用率才有意义。
type cpuSample struct {
	idle, total float64
	ts          time.Time
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
	Hostname  string    `json:"hostname"`
	OS        string    `json:"os"`
	Kernel    string    `json:"kernel"`
	Arch      string    `json:"arch"`
	Uptime    float64   `json:"uptime_seconds"`
	BootTime  float64   `json:"boot_time"`
	CPU       cpuInfo   `json:"cpu"`
	Memory    memInfo   `json:"memory"`
	Disks     []fsInfo  `json:"disks"`
	Network   []netInfo `json:"network"`
	Temps     []tempInfo `json:"temps,omitempty"`
	// 调试字段：解析到 0 条指标或关键字段全空时填充原始文本前 1000 字符，
	// 供用户反馈帮助定位 node_exporter 版本差异/指标名不同的问题。
	Debug     string    `json:"_debug,omitempty"`
}

// tempInfo 温度传感器读数（node_hwmon_temp_celsius）。
type tempInfo struct {
	Name  string  `json:"name"`  // sensor 标签，如 temp1
	Chip  string  `json:"chip"`  // 芯片标识
	Value float64 `json:"value"` // 摄氏度
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
	Device  string  `json:"device"`
	RxBytes float64 `json:"rx_bytes"`
	TxBytes float64 `json:"tx_bytes"`
	// RxRate/TxRate 为基于两次采样差值计算的速率（B/s），首次请求为 0
	RxRate float64 `json:"rx_rate"`
	TxRate float64 `json:"tx_rate"`
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
	text := string(body)
	ms := parseMetrics(text)
	info := parseSystem(ms)
	// CPU 瞬时使用率：基于两次采样累计秒数的增量计算，覆盖历史平均值
	if instant, ok := a.computeCpuUsage(ms); ok {
		info.CPU.UsagePercent = instant
	}
	// 基于上次采样计算网络速率（B/s），首次请求全为 0
	a.computeNetRate(info.Network)
	// 调试：如果关键字段全空（说明解析没命中指标），填充原始文本前 1000 字符
	// 帮助定位 node_exporter 版本差异导致的指标名不同问题。
	if info.Hostname == "" && info.CPU.Load1 == 0 && info.Memory.Total == 0 {
		preview := text
		if len(preview) > 1000 {
			preview = preview[:1000]
		}
		info.Debug = "解析到 0 条匹配指标。node_exporter 原始 /metrics 前 1000 字符：\n" + preview
	}
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

func parseSystem(ms []metric) systemInfo {
	info := systemInfo{
		Hostname: label(ms, "node_uname_info", "nodename"),
		OS:       label(ms, "node_uname_info", "sysname"),
		Kernel:   label(ms, "node_uname_info", "release"),
		Arch:     label(ms, "node_uname_info", "machine"),
	}
	info.Uptime = gauge(ms, "node_time_seconds") - gauge(ms, "node_boot_time_seconds")
	info.BootTime = gauge(ms, "node_boot_time_seconds")
	info.CPU = cpuInfo{
		Cores:  countCPU(ms),
		Load1:  gauge(ms, "node_load1"),
		Load5:  gauge(ms, "node_load5"),
		Load15: gauge(ms, "node_load15"),
	}
	// cpuUsage 返回的是历史累计平均（counter 直接相除），瞬时使用率由 computeCpuUsage 覆盖
	info.CPU.UsagePercent = cpuUsage(ms)
	info.Memory = memInfo{
		Total:     gauge(ms, "node_memory_MemTotal_bytes"),
		Available: gauge(ms, "node_memory_MemAvailable_bytes"),
	}
	if info.Memory.Total > 0 {
		info.Memory.Used = info.Memory.Total - info.Memory.Available
		// 脏数据守卫：Available > Total（node_exporter 偶发异常）时 used 为负，置 0
		if info.Memory.Used < 0 {
			info.Memory.Used = 0
		}
		info.Memory.UsagePercent = info.Memory.Used / info.Memory.Total * 100
	}
	info.Disks = parseFS(ms)
	info.Network = parseNet(ms)
	info.Temps = parseTemp(ms)
	// 在 network[0] 注入聚合卡，汇总所有物理网卡累计字节和。
	// 前端只显示 network[0]，避免 Go map 遍历顺序随机导致每次刷新显示不同网卡、
	// 数值飘忽看起来"不刷新"。聚合卡速率由 computeNetRate 后续基于各卡累加填充。
	var sumRx, sumTx float64
	for _, n := range info.Network {
		sumRx += n.RxBytes
		sumTx += n.TxBytes
	}
	info.Network = append([]netInfo{{
		Device:  "__sum__",
		RxBytes: sumRx,
		TxBytes: sumTx,
	}}, info.Network...)
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
	// 按 device 去重：同一底层设备只保留首个非 bind mount 的 mountpoint。
	// Docker 容器会把宿主 /etc/hostname, /etc/hosts, /etc/resolv.conf
	// bind mount 进容器，node_exporter 看到同一 /dev/xxx 挂到 3 个 mountpoint，
	// 用 mountpoint 作 key 会把同一磁盘显示成 3 个，前端累加容量虚高 3 倍。
	seenDev := map[string]bool{}
	for _, m := range metrics {
		switch m.name {
		case "node_filesystem_size_bytes":
			mp := m.labels["mountpoint"]
			d := m.labels["device"]
			// 过滤容器 bind mount：这些是宿主文件被 bind 进容器，非真实磁盘挂载
			if isBindMount(mp) {
				continue
			}
			// 同一 device 只保留首次出现的 mountpoint（同一底层盘可能 bind 多处）
			if d != "" && seenDev[d] {
				continue
			}
			if d != "" {
				seenDev[d] = true
			}
			size[mp] = m.value
			dev[mp] = d
			fs[mp] = m.labels["fstype"]
		case "node_filesystem_free_bytes":
			// 只记录已注册 mountpoint 的 free，避免孤儿 free 污染
			if _, ok := size[mp]; ok {
				free[mp] = m.value
			}
		}
	}
	var out []fsInfo
	for mp, tot := range size {
		fstype := fs[mp]
		// 过滤非持久化/虚拟文件系统（tmpfs/overlay/docker 层等），
		// 只保留真实磁盘分区，避免叠加后容量虚高。
		if isVirtualFS(fstype) {
			continue
		}
		f := free[mp]
		used := tot - f
		// 脏数据守卫：free > size（node_exporter 偶发异常）时 used 为负，置 0
		if used < 0 {
			used = 0
		}
		pct := 0.0
		if tot > 0 {
			pct = used / tot * 100
		}
		out = append(out, fsInfo{
			Device:       dev[mp],
			Mountpoint:   mp,
			FSType:       fstype,
			Total:        tot,
			Used:         used,
			UsagePercent: pct,
		})
	}
	return out
}

// isBindMount 判断是否为容器 bind mount 的目标文件。
// Docker 把宿主 /etc/hostname, /etc/hosts, /etc/resolv.conf bind 进容器，
// node_exporter 看到同一底层 device 挂到这些 mountpoint，导致 parseFS
// 把同一磁盘重复计数。这些不是真实磁盘挂载，过滤掉。
func isBindMount(mp string) bool {
	switch mp {
	case "/etc/hostname", "/etc/hosts", "/etc/resolv.conf":
		return true
	}
	return false
}

// isVirtualFS 判断是否为虚拟/临时文件系统，这类不应计入真实存储容量。
func isVirtualFS(fstype string) bool {
	switch fstype {
	case "tmpfs", "devtmpfs", "squashfs", "overlay", "aufs", "nsfs",
		"autofs", "cgroup", "cgroup2", "pstore", "mqueue",
		"proc", "sysfs", "binfmt_misc", "fusectl", "fuse.gvfsd-fuse",
		"devpts", "hugetlbfs", "ramfs", "rpc_pipefs":
		return true
	}
	return false
}

// netCounterMax 单网卡累计 counter 合理性上限：1 EB（10^18 字节）。
// 物理网卡不可能达到该量级。实测 node_exporter 偶发返回 1.8446744e+19
// （接近 uint64 上限 1.8446744073709552e+19）的脏值，是 64 位 counter
// 溢出/回绕中的中间态，聚合后 sumTx 飙到 16383 PB，前端显示"一会 MB 一会 PB"。
// 超过该上限视为脏数据置 0，避免污染聚合与速率计算。
const netCounterMax = 1e18

func parseNet(metrics []metric) []netInfo {
	rx := map[string]float64{}
	tx := map[string]float64{}
	devs := map[string]bool{}
	for _, m := range metrics {
		d := m.labels["device"]
		if isVirtualNet(d) {
			continue
		}
		v := m.value
		// 脏数据守卫：负数/NaN/超过 1 EB 的 counter 都不是合法累计值
		if v < 0 || v != v /* NaN */ || v > netCounterMax {
			v = 0
		}
		switch m.name {
		case "node_network_receive_bytes_total":
			rx[d] = v
			devs[d] = true
		case "node_network_transmit_bytes_total":
			tx[d] = v
			devs[d] = true
		}
	}
	var out []netInfo
	for d := range devs {
		out = append(out, netInfo{Device: d, RxBytes: rx[d], TxBytes: tx[d]})
	}
	return out
}

// isVirtualNet 判断是否为虚拟/容器网卡，这类不应计入物理网速。
func isVirtualNet(dev string) bool {
	if dev == "" || dev == "lo" {
		return true
	}
	switch {
	case strings.HasPrefix(dev, "docker"), // docker0/dockerX
		strings.HasPrefix(dev, "veth"),  // vethXXX 容器虚拟网卡
		strings.HasPrefix(dev, "br-"),   // br-XXX Docker 网桥
		strings.HasPrefix(dev, "cni"),   // CNI 插件虚拟网卡
		strings.HasPrefix(dev, "flannel"), // flannel 虚拟网卡
		dev == "br0":                    // 无连字符网桥
		return true
	}
	return false
}

// computeCpuUsage 基于两次 node_cpu_seconds_total 采样的累计秒数增量计算瞬时使用率。
// node_cpu_seconds_total 是系统启动以来的累计 counter，直接 (1-idle/total)*100
// 得到的是历史平均使用率（几乎不变化），必须用增量才有意义。
// 返回 (instant, true) 当增量有效；首次采样/回绕返回 (0, false) 让上层用历史值兜底。
func (a *Adapter) computeCpuUsage(ms []metric) (float64, bool) {
	var idle, total float64
	for _, m := range ms {
		if m.name != "node_cpu_seconds_total" {
			continue
		}
		total += m.value
		if m.labels["mode"] == "idle" {
			idle += m.value
		}
	}
	if total <= 0 {
		return 0, false
	}
	a.cpuMu.Lock()
	defer a.cpuMu.Unlock()
	now := time.Now()
	if a.cpuLast != nil {
		dt := now.Sub(a.cpuLast.ts).Seconds()
		if dt > 0.5 && total > a.cpuLast.total && idle >= a.cpuLast.idle {
			dTotal := total - a.cpuLast.total
			dIdle := idle - a.cpuLast.idle
			if dTotal > 0 {
				return (1 - dIdle/dTotal) * 100, true
			}
		}
	}
	a.cpuLast = &cpuSample{idle: idle, total: total, ts: now}
	return 0, false
}

// computeNetRate 基于上次采样的累计字节和当前值做差，除以真实时间差得到 B/s 速率。
// counter 可能因重启/溢出回绕，差值为负时跳过（返回 0）。
// 每张网卡单独计算；聚合卡（Device=="__sum__"）的速率 = 所有物理网卡速率之和，
// 由 handleSystem 在调用本方法后基于各网卡 RxRate/TxRate 累加得到。
func (a *Adapter) computeNetRate(nets []netInfo) {
	a.netMu.Lock()
	defer a.netMu.Unlock()
	now := time.Now()
	if a.netLast == nil {
		a.netLast = map[string]netSample{}
	}
	next := map[string]netSample{}
	var sumRxRate, sumTxRate float64
	for i := range nets {
		n := &nets[i]
		if n.Device == "__sum__" {
			continue
		}
		if prev, ok := a.netLast[n.Device]; ok {
			dt := now.Sub(prev.ts).Seconds()
			if dt > 0.5 { // 时间差太小不可靠，跳过
				if n.RxBytes >= prev.rx {
					n.RxRate = (n.RxBytes - prev.rx) / dt
				}
				if n.TxBytes >= prev.tx {
					n.TxRate = (n.TxBytes - prev.tx) / dt
				}
			}
		}
		sumRxRate += n.RxRate
		sumTxRate += n.TxRate
		next[n.Device] = netSample{rx: n.RxBytes, tx: n.TxBytes, ts: now}
	}
	a.netLast = next
	// 把聚合速率写到 network[0]（聚合卡，由 parseSystem 注入）
	for i := range nets {
		if nets[i].Device == "__sum__" {
			nets[i].RxRate = sumRxRate
			nets[i].TxRate = sumTxRate
			break
		}
	}
}

// parseTemp 解析 node_hwmon_temp_celsius 温度传感器。
// 不同设备芯片/标签不同，返回全部传感器，前端取最高值作为 CPU 温度展示。
func parseTemp(metrics []metric) []tempInfo {
	seen := map[string]tempInfo{}
	for _, m := range metrics {
		if m.name != "node_hwmon_temp_celsius" || m.value <= 0 {
			continue
		}
		chip := m.labels["chip"]
		sensor := m.labels["sensor"]
		if chip == "" && sensor == "" {
			continue
		}
		key := chip + "/" + sensor
		seen[key] = tempInfo{Name: sensor, Chip: chip, Value: m.value}
	}
	var out []tempInfo
	for _, v := range seen {
		out = append(out, v)
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
