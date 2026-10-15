package api

import (
	"archive/zip"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

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

// uploadFile PUT /api/devices/{serial}/files?path=/sdcard/xxx（body = 文件内容，流式直写 sync，不驻留内存）
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
	if err := s.mgr.Client().PushStream(r.PathValue("serial"), p, r.Body, "0644"); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "已上传"})
}

// downloadFile GET /api/devices/{serial}/files/content?path=
// 文件：流式回传；目录：服务端递归打包 zip 流式回传（边拉边压，不落盘）。
func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	p, ok := sanitizeDevicePath(r.URL.Query().Get("path"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "需要合法的 path 参数")
		return
	}
	name := path.Base(p)

	// 目录：zip 打包下载
	if r.URL.Query().Get("zip") == "1" {
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name+".zip"))
		w.Header().Set("Content-Type", "application/zip")
		zw := zip.NewWriter(w)
		defer zw.Close()
		if err := s.zipDir(serial, p, "", zw); err != nil {
			// 头已发出无法改状态码，记录日志即可
			log.Printf("[files:%s] 打包 %s 失败: %v", serial, p, err)
		}
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Header().Set("Content-Type", "application/octet-stream")
	if err := s.mgr.Client().Pull(serial, p, w); err != nil {
		log.Printf("[files:%s] 下载 %s 失败: %v", serial, p, err)
	}
}

// zipDir 递归把设备目录打包进 zip（rel 为 zip 内相对路径前缀，根为空）。
func (s *Server) zipDir(serial, deviceDir, rel string, zw *zip.Writer) error {
	c := s.mgr.Client()
	entries, err := c.List(serial, deviceDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		devicePath := deviceDir + "/" + e.Name
		zipPath := rel + e.Name
		if e.Dir {
			if err := s.zipDir(serial, devicePath, zipPath+"/", zw); err != nil {
				return err
			}
			continue
		}
		hdr := &zip.FileHeader{Name: zipPath, Method: zip.Deflate, Modified: mtimeOf(e)}
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if err := c.Pull(serial, devicePath, fw); err != nil {
			return err
		}
	}
	return nil
}

func mtimeOf(e adb.FileEntry) (t time.Time) {
	return time.Unix(int64(e.Time), 0)
}

// renameFile POST /api/devices/{serial}/files/rename {from, to}（文件/目录通用）
func (s *Server) renameFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !readJSONBody(w, r, &req) {
		return
	}
	from, ok1 := sanitizeDevicePath(req.From)
	to, ok2 := sanitizeDevicePath(req.To)
	if !ok1 || !ok2 {
		writeErr(w, http.StatusBadRequest, "from/to 需为合法设备路径")
		return
	}
	serial := r.PathValue("serial")
	c := s.mgr.Client()
	// 目标已存在时拒绝覆盖，避免误操作
	if entries, err := c.List(serial, path.Dir(to)); err == nil {
		for _, e := range entries {
			if e.Name == path.Base(to) {
				writeErr(w, http.StatusConflict, "目标名称已存在: "+path.Base(to))
				return
			}
		}
	}
	if _, err := c.ShellTimeout(serial, "mv '"+shQuote(from)+"' '"+shQuote(to)+"'", 15*time.Second); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "已重命名"})
}

// shQuote 单引号转义（' → '\''），防 shell 注入。
func shQuote(p string) string {
	return strings.ReplaceAll(p, "'", `'\''`)
}

// deleteFile DELETE /api/devices/{serial}/files?path=
func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	p, ok := sanitizeDevicePath(r.URL.Query().Get("path"))
	if !ok || p == "/" {
		writeErr(w, http.StatusBadRequest, "需要合法的 path 参数")
		return
	}
	if _, err := s.mgr.Shell(r.PathValue("serial"), "rm -rf '"+shQuote(p)+"'"); err != nil {
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
	if _, err := s.mgr.Shell(r.PathValue("serial"), "mkdir -p '"+shQuote(p)+"'"); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "已创建"})
}
