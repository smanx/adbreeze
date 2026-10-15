package api

import (
	"net/http"
	"strings"
	"time"
)

// handleUIDump GET /api/devices/{serial}/ui-dump
// 布局检查数据源：uiautomator dump 到设备临时文件后经 sync 协议拉回 XML。
// 返回 {xml, rotation}。uiautomator dump 耗时 1~3 秒（含等待 idle）。
func (s *Server) handleUIDump(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	c := s.mgr.Client()

	const remote = "/data/local/tmp/adbreeze_uidump.xml"
	out, err := c.ShellTimeout(serial, "uiautomator dump "+remote, 20*time.Second)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "uiautomator dump 失败: "+err.Error())
		return
	}
	if !strings.Contains(out, "dumped to") && !strings.Contains(out, "DUMP_OK") {
		writeErr(w, http.StatusBadGateway, "uiautomator dump 未成功: "+strings.TrimSpace(out))
		return
	}

	var xml strings.Builder
	if err := c.Pull(serial, remote, &xml); err != nil {
		writeErr(w, http.StatusBadGateway, "读取 dump 文件失败: "+err.Error())
		return
	}
	// 拉完即删，避免设备残留
	_, _ = c.ShellTimeout(serial, "rm -f "+remote, 5*time.Second)

	data := xml.String()
	rotation := "0"
	if i := strings.Index(data, `rotation="`); i >= 0 {
		if j := strings.Index(data[i+10:], `"`); j >= 0 {
			rotation = data[i+10 : i+10+j]
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"xml": data, "rotation": rotation})
}
