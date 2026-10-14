package api

import (
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"

	"adbreeze/internal/adb"
)

// sanitizeDevicePath 校验并规整设备路径（以 / 开头，禁止 .. 逃逸到顶层之外）。
func sanitizeDevicePath(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" || !strings.HasPrefix(p, "/") {
		return "", false
	}
	clean := path.Clean(p)
	if strings.Contains(clean, "..") {
		return "", false
	}
	return clean, true
}

// listFiles GET /api/devices/{serial}/files?path=/sdcard
func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	p, ok := sanitizeDevicePath(r.URL.Query().Get("path"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "需要合法的 path 参数（以 / 开头）")
		return
	}
	entries, err := s.mgr.Client().List(r.PathValue("serial"), p)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if entries == nil {
		entries = []adb.FileEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// uploadFile PUT /api/devices/{serial}/files?path=/sdcard/xxx（body = 文件内容）
func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	p, ok := sanitizeDevicePath(r.URL.Query().Get("path"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "需要合法的 path 参数")
		return
	}
	if strings.HasSuffix(p, "/") {
		writeErr(w, http.StatusBadRequest, "path 必须是文件而非目录")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 2<<30)) // 上限 2GB
	if err != nil {
		writeErr(w, http.StatusBadRequest, "读取上传内容失败: "+err.Error())
		return
	}
	if err := s.mgr.Client().Push(r.PathValue("serial"), p, data, "0644"); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "已上传"})
}

// downloadFile GET /api/devices/{serial}/files/content?path=（流式回传文件内容）
func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	p, ok := sanitizeDevicePath(r.URL.Query().Get("path"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "需要合法的 path 参数")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(path.Base(p)))
	w.Header().Set("Content-Type", "application/octet-stream")
	if err := s.mgr.Client().Pull(r.PathValue("serial"), p, w); err != nil {
		log.Printf("[files:%s] 下载 %s 失败: %v", r.PathValue("serial"), p, err)
	}
}

// deleteFile DELETE /api/devices/{serial}/files?path=
func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	p, ok := sanitizeDevicePath(r.URL.Query().Get("path"))
	if !ok || p == "/" {
		writeErr(w, http.StatusBadRequest, "需要合法的 path 参数")
		return
	}
	safe := strings.ReplaceAll(p, "'", "")
	if _, err := s.mgr.Shell(r.PathValue("serial"), "rm -rf '"+safe+"'"); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "已删除"})
}

// mkdir POST /api/devices/{serial}/files/mkdir?path=
func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) {
	p, ok := sanitizeDevicePath(r.URL.Query().Get("path"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "需要合法的 path 参数")
		return
	}
	safe := strings.ReplaceAll(p, "'", "")
	if _, err := s.mgr.Shell(r.PathValue("serial"), "mkdir -p '"+safe+"'"); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "已创建"})
}
