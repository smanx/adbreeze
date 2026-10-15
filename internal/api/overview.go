package api

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// OverviewInfo 是概览面板的结构化数据（对齐 AYA Overview 面板字段）。
type OverviewInfo struct {
	MarketName    string `json:"marketName"`
	Brand         string `json:"brand"`
	Model         string `json:"model"`
	SerialNo      string `json:"serialno"`
	Android       string `json:"android"`
	SDK           string `json:"sdk"`
	Board         string `json:"board"`
	ABI           string `json:"abi"`
	Incremental   string `json:"incremental"`
	Kernel        string `json:"kernel"`
	CPUCores      int    `json:"cpuCores"`
	MemTotalKB    uint64 `json:"memTotalKB"`
	MemAvailKB    uint64 `json:"memAvailKB"`
	StorageUsedKB uint64 `json:"storageUsedKB"`
	StorageTotKB  uint64 `json:"storageTotalKB"`
	PhysSize      string `json:"physSize"`
	PhysDensity   string `json:"physDensity"`
	OverSize      string `json:"overSize"`
	OverDensity   string `json:"overDensity"`
	FontScale     string `json:"fontScale"`
	Wifi          string `json:"wifi"`
	IP            string `json:"ip"`
	MAC           string `json:"mac"`
	CPUTemp       float64 `json:"cpuTemp"`
	Battery       BatteryInfo `json:"battery"`
}

// BatteryInfo 电池状态（dumpsys battery）。
type BatteryInfo struct {
	Level           int     `json:"level"`
	Status          int     `json:"status"` // 2=充电中 3=放电中 5=已充满
	Temperature     float64 `json:"temperature"` // 摄氏度
	Voltage         int     `json:"voltage"`     // mV
	Technology      string  `json:"technology"`
	ACPowered       bool    `json:"acPowered"`
	USBPowered      bool    `json:"usbPowered"`
	WirelessPowered bool    `json:"wirelessPowered"`
	ChargeCounter   int     `json:"chargeCounter"` // µAh
}

// handleOverview GET /api/devices/{serial}/overview
// 并发执行 3 组 shell（getprop 全量 / 基础系统信息 / dumpsys 慢命令），解析为结构化数据。
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	c := s.mgr.Client()

	propsCh := make(chan map[string]string, 1)
	baseCh := make(chan string, 1)
	slowCh := make(chan string, 1)

	go func() {
		out, _ := c.ShellTimeout(serial, "getprop", 15*time.Second)
		propsCh <- parseProps(out)
	}()
	go func() {
		script := strings.Join([]string{
			"echo ===KERNEL===", "uname -r",
			"echo ===CPUCORES===", "cat /proc/stat | grep -cE '^cpu[0-9]+'",
			"echo ===MEM===; head -5 /proc/meminfo",
			"echo ===WLAN===", "ip addr show wlan0",
			"echo ===WM===; wm size; wm density",
			"echo ===FONT===", "settings get system font_scale",
		}, "; ")
		out, _ := c.ShellTimeout(serial, script, 15*time.Second)
		baseCh <- out
	}()
	go func() {
		script := strings.Join([]string{
			"echo ===DISK===", "dumpsys diskstats | head -8",
			"echo ===WIFI===; dumpsys wifi | grep -m1 mWifiInfo",
			"echo ===THERM===; cat /sys/class/thermal/thermal_zone0/temp 2>/dev/null",
			"echo ===BATTERY===", "dumpsys battery | head -40",
		}, "; ")
		out, _ := c.ShellTimeout(serial, script, 20*time.Second)
		slowCh <- out
	}()

	props, base, slow := <-propsCh, <-baseCh, <-slowCh
	info := buildInfo(props, sectionMap(base), sectionMap(slow))

	writeJSON(w, http.StatusOK, map[string]any{"serial": serial, "props": props, "info": info})
}

// buildInfo 把三路原始输出拼装为结构化信息，缺字段一律留零值（前端显示"—"）。
func buildInfo(props map[string]string, base, slow map[string]string) OverviewInfo {
	info := OverviewInfo{
		Brand:       props["ro.product.brand"],
		Model:       props["ro.product.model"],
		SerialNo:    props["ro.serialno"],
		Android:     props["ro.build.version.release"],
		SDK:         props["ro.build.version.sdk"],
		Board:       props["ro.product.board"],
		ABI:         props["ro.product.cpu.abi"],
		Incremental: props["ro.build.version.incremental"],
		Kernel:      strings.TrimSpace(base["KERNEL"]),
		FontScale:   strings.TrimSpace(base["FONT"]),
		PhysSize:    afterColon(base["WM"], "Physical size"),
		PhysDensity: afterColon(base["WM"], "Physical density"),
		OverSize:    afterColon(base["WM"], "Override size"),
		OverDensity: afterColon(base["WM"], "Override density"),
	}
	// 市场名兜底链：小米系 marketname → ColorOS oplus → 通用 vendor → model
	for _, k := range []string{"ro.product.marketname", "ro.vendor.oplus.market.name", "ro.vendor.product.marketname"} {
		if v := props[k]; v != "" {
			info.MarketName = v
			break
		}
	}
	if info.MarketName == "" {
		info.MarketName = info.Model
	}
	info.CPUCores = atoiOr(base["CPUCORES"], 0)

	// 内存：MemTotal / MemAvailable（旧设备兜底 MemFree）
	for _, line := range strings.Split(base["MEM"], "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			info.MemTotalKB = atoiU64(f[1])
		case "MemAvailable:":
			info.MemAvailKB = atoiU64(f[1])
		case "MemFree:":
			if info.MemAvailKB == 0 {
				info.MemAvailKB = atoiU64(f[1])
			}
		}
	}

	// 存储容量
	for _, line := range strings.Split(slow["DISK"], "\n") {
		if m := diskRe.FindStringSubmatch(line); m != nil {
			free, _ := strconv.ParseUint(m[1], 10, 64)
			total, _ := strconv.ParseUint(m[2], 10, 64)
			info.StorageTotKB = total
			if total > free {
				info.StorageUsedKB = total - free
			}
			break
		}
	}

	// wlan0 IP / MAC
	for _, line := range strings.Split(base["WLAN"], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inet ") && info.IP == "" {
			f := strings.Fields(line)
			if len(f) >= 2 {
				info.IP = strings.Split(f[1], "/")[0]
			}
		}
		if strings.HasPrefix(line, "link/ether") && info.MAC == "" {
			f := strings.Fields(line)
			if len(f) >= 2 {
				info.MAC = f[1]
			}
		}
	}

	// Wi-Fi SSID（mWifiInfo SSID: "xxx"）
	if m := wifiSSIDRe.FindStringSubmatch(slow["WIFI"]); m != nil {
		info.Wifi = m[1]
	}

	// CPU 温度（thermal_zone0，单位毫摄氏度）
	if v := atoiOr(strings.TrimSpace(slow["THERM"]), 0); v > 0 {
		info.CPUTemp = float64(v) / 1000
	}

	// 电池
	b := &info.Battery
	for _, line := range strings.Split(slow["BATTERY"], "\n") {
		kv := strings.SplitN(line, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(kv[0]))
		val := strings.TrimSpace(kv[1])
		switch key {
		case "level":
			b.Level = atoiOr(val, 0)
		case "status":
			b.Status = atoiOr(val, 0)
		case "temperature":
			if t := atoiOr(val, 0); t > 0 {
				b.Temperature = float64(t) / 10
			}
		case "voltage":
			b.Voltage = atoiOr(val, 0)
		case "technology":
			b.Technology = val
		case "ac powered":
			b.ACPowered = val == "true"
		case "usb powered":
			b.USBPowered = val == "true"
		case "wireless powered":
			b.WirelessPowered = val == "true"
		case "charge counter":
			b.ChargeCounter = atoiOr(val, 0)
		}
	}
	return info
}

var (
	diskRe     = regexp.MustCompile(`Data-Free:\s*(\d+)K\s*/\s*(\d+)K`)
	wifiSSIDRe = regexp.MustCompile(`mWifiInfo\s+SSID:\s*"?([^",]+)"?,`)
)

// sectionMap 解析 "echo ===KEY===" 分段脚本输出。
func sectionMap(out string) map[string]string {
	sections := map[string]string{}
	cur := ""
	var sb strings.Builder
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "===") && strings.HasSuffix(line, "===") && len(line) > 6 {
			if cur != "" {
				sections[cur] = sb.String()
			}
			cur = strings.Trim(line, "=")
			sb.Reset()
			continue
		}
		if cur != "" {
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
	}
	if cur != "" {
		sections[cur] = sb.String()
	}
	return sections
}

// parseProps 解析 getprop 输出：[key]: [value]。
func parseProps(out string) map[string]string {
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		i := strings.Index(line, "]: [")
		if i < 0 || !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
			continue
		}
		props[line[1:i]] = line[i+4 : len(line)-1]
	}
	return props
}

func afterColon(sectionText, prefix string) string {
	for _, line := range strings.Split(sectionText, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, prefix), ":"))
		}
	}
	return ""
}

func atoiOr(s string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return v
	}
	return def
}

func atoiU64(s string) uint64 {
	v, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return v
}
