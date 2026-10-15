package api

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PerfSample 性能面板一次采样的全部指标（对齐 AYA Performance 面板）。
type PerfSample struct {
	CPUTotal float64     `json:"cpuTotal"` // 0-100
	CPULoads []float64   `json:"cpuLoads"` // 每核 0-100
	CPUFreqs []float64   `json:"cpuFreqs"` // 每核 MHz
	CPUTemp  float64     `json:"cpuTemp"`  // °C（thermalservice CPU* 均值）
	MemUsed  uint64      `json:"memUsed"`  // KB
	MemTotal uint64      `json:"memTotal"` // KB
	Battery  BatteryInfo `json:"battery"`
	FPS      float64     `json:"fps"`
	TopApp   string      `json:"topApp"` // 前台应用包名（空=未知）
	Uptime   float64     `json:"uptime"` // 秒
	Ts       int64       `json:"ts"`     // 采样时刻 ms
}

// fpsState SurfaceFlinger flips 差分法的上次快照（按设备缓存）。
type fpsState struct {
	flaps   uint64
	at      time.Time
	display string // 主 display 行原文（用于匹配同一 layer）
}

var (
	fpsStates  sync.Map // serial → *fpsState
	cpuStatRe  = regexp.MustCompile(`^cpu(\d+)\s+((?:\d+\s*)+)$`)
	thermCPURe = regexp.MustCompile(`Temperature\{mValue=([\d.]+),\s*mType=\d+,\s*mName=(CPU\w*)`)
	flipsRe    = regexp.MustCompile(`size=\[(\d+)\s+(\d+)\].*flips=(\d+)`)
	topAppRe   = regexp.MustCompile(`topActivity=\{([^/}]+)`)
)

// handlePerf GET /api/devices/{serial}/perf
// 一次采样：CPU 双采样（间隔 200ms）+ 内存/电池/温度/FPS/前台应用/uptime 并发。
// 总耗时约 1.2 秒（FPS 的 dumpsys SurfaceFlinger 占大头），前端按 1.5s 轮询。
func (s *Server) handlePerf(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	c := s.mgr.Client()

	// ---- CPU：/proc/stat 双采样 ----
	stat1, err1 := c.ShellTimeout(serial, "cat /proc/stat", 5*time.Second)
	time.Sleep(200 * time.Millisecond)
	stat2, err2 := c.ShellTimeout(serial, "cat /proc/stat", 5*time.Second)
	sample := PerfSample{Ts: time.Now().UnixMilli()}
	if err1 == nil && err2 == nil {
		sample.CPUTotal, sample.CPULoads = calcCPULoad(stat1, stat2)
	}

	// ---- 其余指标并发 ----
	var wg sync.WaitGroup
	var mem, freqOut, therm, batt, sfl, recents, uptimeStr string
	read := func(dst *string, cmd string, timeout time.Duration) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			*dst, _ = c.ShellTimeout(serial, cmd, timeout)
		}()
	}
	read(&mem, "cat /proc/meminfo", 5*time.Second)
	read(&freqOut, freqCatCmd(), 5*time.Second)
	read(&therm, "dumpsys thermalservice", 10*time.Second)
	read(&batt, "dumpsys battery", 10*time.Second)
	read(&sfl, "dumpsys SurfaceFlinger", 15*time.Second)
	read(&recents, "dumpsys activity recents", 10*time.Second)
	read(&uptimeStr, "cat /proc/uptime", 5*time.Second)
	wg.Wait()

	// 内存
	for _, line := range strings.Split(mem, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			sample.MemTotal = atoiU64(f[1])
		case "MemAvailable:":
			sample.MemUsed = subOrZero(sample.MemTotal, atoiU64(f[1]))
		case "MemFree:":
			if sample.MemUsed == 0 {
				sample.MemUsed = subOrZero(sample.MemTotal, atoiU64(f[1]))
			}
		}
	}

	// CPU 频率
	freqs := parseFreqs(freqOut)
	sample.CPUFreqs = freqs

	// CPU 温度：mName=CPU* 均值
	var sum float64
	var n int
	for _, m := range thermCPURe.FindAllStringSubmatch(therm, -1) {
		v, _ := strconv.ParseFloat(m[1], 64)
		sum += v
		n++
	}
	if n > 0 {
		sample.CPUTemp = round1(sum / float64(n))
	}

	// 电池
	sample.Battery = parseBattery(batt)

	// FPS：主 display flips 差分
	sample.FPS = calcFPS(serial, sfl)

	// 前台应用：topActivity={pkg/Activity}（取第一条）
	if m := topAppRe.FindStringSubmatch(recents); m != nil {
		sample.TopApp = m[1]
	}

	// uptime
	if f := strings.Fields(uptimeStr); len(f) > 0 {
		v, _ := strconv.ParseFloat(f[0], 64)
		sample.Uptime = v
	}

	writeJSON(w, http.StatusOK, sample)
}

// calcCPULoad 两次 /proc/stat 快照差分。返回总量与逐核负载（0-100）。
func calcCPULoad(s1, s2 string) (float64, []float64) {
	t1 := parseCPULine(s1) // [ [user,nice,sys,idle,...], ... ] 每核
	t2 := parseCPULine(s2)
	n := len(t1)
	if n == 0 || len(t2) != n {
		return 0, nil
	}
	loads := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		l1 := coreLoad(t1[i]) // user+nice+sys+irq+iowait+softirq
		l2 := coreLoad(t2[i])
		total1 := float64(l1 + t1[i][3]) // + idle
		total2 := float64(l2 + t2[i][3])
		dt := total2 - total1
		if dt <= 0 {
			loads = append(loads, 0)
			continue
		}
		v := (float64(l2-l1) / dt) * 100
		if v < 0 {
			v = 0
		}
		if v > 100 {
			v = 100
		}
		loads = append(loads, round1(v))
	}
	// 总 CPU：逐核负载均值近似
	var sum float64
	for _, v := range loads {
		sum += v
	}
	var totalSum float64
	if n > 0 {
		totalSum = round1(sum / float64(n))
	}
	return totalSum, loads
}

// parseCPULine 解析 /proc/stat 的 cpuN 行（跳过聚合行 cpu），按核号排序。
func parseCPULine(out string) [][]uint64 {
	type core struct {
		idx  int
		vals []uint64
	}
	var cores []core
	for _, line := range strings.Split(out, "\n") {
		m := cpuStatRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		idx, _ := strconv.Atoi(m[1])
		var vals []uint64
		for _, fs := range strings.Fields(m[2]) {
			v, _ := strconv.ParseUint(fs, 10, 64)
			vals = append(vals, v)
		}
		if len(vals) >= 4 {
			cores = append(cores, core{idx, vals})
		}
	}
	sort.Slice(cores, func(i, j int) bool { return cores[i].idx < cores[j].idx })
	out2 := make([][]uint64, len(cores))
	for i, c := range cores {
		out2[i] = c.vals
	}
	return out2
}

// coreLoad = user+nice+sys+irq+iowait+softirq（/proc/stat cpuN 行字段序：
// user nice sys idle iowait irq softirq steal...）。行不足 7 列时对缺失字段按 0 处理。
func coreLoad(v []uint64) uint64 {
	get := func(i int) uint64 {
		if i < len(v) {
			return v[i]
		}
		return 0
	}
	return get(0) + get(1) + get(2) + get(4) + get(5) + get(6)
}

func freqCatCmd() string {
	// 一次 shell 拿全部核心频率（cpu0..cpu7），缺失核心输出空行
	return "for i in 0 1 2 3 4 5 6 7; do cat /sys/devices/system/cpu/cpu$i/cpufreq/scaling_cur_freq 2>/dev/null; echo; done"
}

func parseFreqs(out string) []float64 {
	var freqs []float64
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if kHz, err := strconv.ParseFloat(line, 64); err == nil {
			freqs = append(freqs, float64(int64(kHz/1000))) // floor MHz
		}
	}
	return freqs
}

// calcFPS 主 display flips 差分（flaps 是笔误保留字段名）。首测拿不到差分时返回 0。
func calcFPS(serial, sfl string) float64 {
	// 取第一个 flips 行（主 display）；每行是不同 layer 的累计值
	var flips uint64
	var display string
	for _, line := range strings.Split(sfl, "\n") {
		if m := flipsRe.FindStringSubmatch(line); m != nil {
			display = m[1] + "x" + m[2]
			flips, _ = strconv.ParseUint(m[3], 10, 64)
			break
		}
	}
	if display == "" {
		return 0
	}
	now := time.Now()
	if prev, ok := fpsStates.Load(serial); ok {
		st := prev.(*fpsState)
		if st.display == display && flips >= st.flaps {
			dt := now.Sub(st.at).Seconds()
			if dt > 0.2 {
				fps := float64(flips-st.flaps) / dt
				if fps > 144 {
					fps = 144 // 合理上限
				}
				fpsStates.Store(serial, &fpsState{flaps: flips, at: now, display: display})
				return round1(fps)
			}
		}
	}
	fpsStates.Store(serial, &fpsState{flaps: flips, at: now, display: display})
	return 0 // 首次采样无差分
}

func parseBattery(out string) BatteryInfo {
	var b BatteryInfo
	for _, line := range strings.Split(out, "\n") {
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
		case "ac powered":
			b.ACPowered = val == "true"
		case "usb powered":
			b.USBPowered = val == "true"
		}
	}
	return b
}

func subOrZero(total, avail uint64) uint64 {
	if total > avail {
		return total - avail
	}
	return 0
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
