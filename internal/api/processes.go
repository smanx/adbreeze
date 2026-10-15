package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ProcEntry 进程列表项（top -b -n 1 解析）。
type ProcEntry struct {
	PID  string  `json:"pid"`
	User string  `json:"user"`
	CPU  float64 `json:"cpu"` // %
	Mem  string  `json:"mem"` // RES 列原文（如 30M / 504K）
	Name string  `json:"name"`
	Args string  `json:"args,omitempty"`
}

// handleProcList GET /api/devices/{serial}/processes
// top -b -n 1 采样。旧版 top（无 -o 参数支持）回退解析默认表头。
func (s *Server) handleProcList(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	out, err := s.mgr.Client().ShellTimeout(serial, "top -b -n 1 -o pid -o %cpu -o time+ -o res -o user -o name -o args 2>/dev/null || top -b -n 1", 15*time.Second)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	procs := parseTop(out)
	// top 自身与 sh -c 包装进程无意义，剔除
	filtered := procs[:0]
	for _, p := range procs {
		if p.Name == "top" || p.Name == "sh" && strings.Contains(p.Args, "top") {
			continue
		}
		filtered = append(filtered, p)
	}
	// CPU 降序
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].CPU > filtered[j].CPU })
	writeJSON(w, http.StatusOK, filtered)
}

// handleProcKill POST /api/devices/{serial}/processes/kill {pid, package}
// 应用进程走 am force-stop（干净杀），其余 kill -9。
func (s *Server) handleProcKill(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PID     string `json:"pid"`
		Package string `json:"package"`
	}
	if !readJSONBody(w, r, &req) {
		return
	}
	pid := strings.TrimSpace(req.PID)
	if n, err := strconv.Atoi(pid); err != nil || n <= 0 || req.Package == "" && n < 100 {
		writeErr(w, http.StatusBadRequest, "需要合法的 pid")
		return
	}
	if req.Package != "" {
		pkg := sanitizePackageName(req.Package)
		if pkg == "" {
			writeErr(w, http.StatusBadRequest, "需要合法的 package 参数")
			return
		}
		if _, err := s.mgr.Client().ShellTimeout(r.PathValue("serial"), "am force-stop "+pkg, 10*time.Second); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "已停止 " + pkg})
		return
	}
	out, err := s.mgr.Client().ShellTimeout(r.PathValue("serial"), "kill -9 "+pid, 10*time.Second)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	msg := strings.TrimSpace(out)
	if msg == "" {
		msg = "已发送 kill -9"
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

// parseTop 解析 top 输出，兼容两种表头：
//
//	新: PID %CPU TIME+ RES USER NAME [ARGS]
//	旧: PID USER PR NI VIRT RES SHR S[%CPU] %MEM TIME+ ARGS
func parseTop(out string) []ProcEntry {
	var procs []ProcEntry
	lines := strings.Split(out, "\n")
	headerIdx := -1
	newFmt := false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "PID ") || strings.HasPrefix(t, "PID%") {
			headerIdx = i
			newFmt = strings.Contains(t, "NAME")
			break
		}
	}
	if headerIdx < 0 {
		return procs
	}
	for _, line := range lines[headerIdx+1:] {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) < 6 || !isDigits(f[0]) {
			continue
		}
		if newFmt {
			// PID %CPU TIME+ RES USER NAME [ARGS...]
			procs = append(procs, ProcEntry{
				PID: f[0], CPU: atof(f[1]), Mem: f[3], User: f[4],
				Name: f[5], Args: strings.Join(f[6:], " "),
			})
		} else {
			// PID USER PR NI VIRT RES SHR S[%CPU] %MEM TIME+ ARGS...
			// 旧格式：%CPU 可能带 S 前缀（如 "S 23.3" 两列或 "S23.3" 一列）
			if len(f) < 11 {
				continue
			}
			cpuStr := f[7]
			cpu := atof(strings.TrimLeft(cpuStr, "S"))
			if cpu == 0 && !strings.Contains(cpuStr, ".") {
				cpu = atof(strings.TrimPrefix(f[7], "S"))
			}
			procs = append(procs, ProcEntry{
				PID: f[0], User: f[1], CPU: cpu, Mem: f[5],
				Name: lastArg(f), Args: strings.Join(f[11:], " "),
			})
		}
	}
	return procs
}

// lastArg 旧格式最后一列是 ARGS（进程名常与首个 token 相同，无参时即进程名）。
func lastArg(f []string) string {
	if len(f) >= 12 {
		return f[11]
	}
	return f[len(f)-1]
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func atof(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}
