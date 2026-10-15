package api

import (
	"context"
	"log"
	"net/http"
	"time"

	"adbreeze/internal/adb"
)

// ADB 可执行文件路径设置。
//
// 后端与 adb server 的通信走 TCP（127.0.0.1:5037），adb 可执行文件只在
// 「拉起 / 重启 server」时用到，因此切换路径无需重建整个应用，只要在保存后
// 用新路径重启一次 server 即可生效。

// adbSettingsResp 是 GET /api/settings/adb 的响应。
type adbSettingsResp struct {
	// Path 当前生效的路径（空字符串表示「默认（从 PATH 查找）」）
	Path string `json:"path"`
	// Default 为当前解析出的默认 adb 路径（PATH 中找到的那个），供界面提示
	Default string `json:"default"`
	// Available 默认路径是否可用（PATH 里有没有 adb）
	Available bool `json:"available"`
	// ConfigFile 存档位置，便于用户排查
	ConfigFile string `json:"configFile"`
}

func (s *Server) getAdbSettings(w http.ResponseWriter, _ *http.Request) {
	// PATH 默认路径解析失败不视为错误：用户可能就是用自定义路径的
	def, err := adb.ResolveAdbPath("")
	resp := adbSettingsResp{
		Path:       s.mgr.AdbPath(),
		Default:    def,
		Available:  err == nil,
		ConfigFile: s.settings.FilePath(),
	}
	if err != nil {
		resp.Default = ""
	}
	writeJSON(w, http.StatusOK, resp)
}

type adbSettingsReq struct {
	Path string `json:"path"`
}

// putAdbSettings POST /api/settings/adb
//
// 保存并立即生效：先用新路径执行 `adb version` 验证可用性（避免存下死路径），
// 通过后写入存档、切换内存中的路径，并用它重启 adb server（新二进制接管 5037），
// 最后等设备列表刷新。
func (s *Server) putAdbSettings(w http.ResponseWriter, r *http.Request) {
	var req adbSettingsReq
	if !readJSONBody(w, r, &req) {
		return
	}

	// 先校验：路径不存在 / 不是可执行的 adb 都会在这里被拦住
	versionOut, err := adb.ProbeAdb(req.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// 落盘（失败则不改变运行时状态）
	cur := s.settings.Get()
	cur.AdbPath = req.Path
	if err := s.settings.Set(cur); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.mgr.SetAdbPath(req.Path)

	// 用新路径重启 server，让切换立即生效而非等到下次崩溃自愈。
	// restart 失败不回滚设置：路径本身已验证可执行，失败多半是环境问题
	// （如 5037 被别的 adb 占用），保留用户选择并在 message 里如实说明。
	restarted := true
	var restartMsg string
	if err := s.restartAdbServer(); err != nil {
		restarted = false
		restartMsg = err.Error()
		log.Printf("[adb] 切换 adb 路径后重启 server 失败: %v", err)
	}

	msg := "已切换 adb 路径"
	if restarted {
		msg += "，adb server 已用新路径重启"
	} else {
		msg += "（adb server 重启失败：" + restartMsg + "，将在下次自动恢复时使用新路径）"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":   msg,
		"path":      req.Path,
		"restarted": restarted,
		"version":   versionOut,
	})
}

// restartAdbServer 用当前路径执行 kill-server 再 start-server，
// 让新二进制接管 5037 端口。kill 失败（本来就没在跑）不视为错误。
func (s *Server) restartAdbServer() error {
	path, err := adb.ResolveAdbPath(s.mgr.AdbPath())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = adb.RunAdb(ctx, path, "kill-server")
	if err := adb.RunAdb(ctx, path, "start-server"); err != nil {
		return err
	}
	// 等 server 真的可达再谈重连：EnsureServerNow 内部会轮询 Ping 直到就绪/超时
	if err := s.mgr.EnsureServerNow(ctx); err != nil {
		return err
	}
	// 重启 server 会断开所有 TCP 设备，把它们连回来，别让用户看到一片 offline
	reconnected := s.mgr.ReconnectWireless()
	if len(reconnected) > 0 {
		log.Printf("[adb] 切换路径后已重连无线设备: %v", reconnected)
	}
	return nil
}
