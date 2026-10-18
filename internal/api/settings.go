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

	// 落盘 + 切运行时 + 重启 server（与一键安装成功后的自动切换共用同一条链路）
	writeJSON(w, http.StatusOK, s.applyAdbPathWithRestart(req.Path, versionOut))
}

// adbCandidates GET /api/settings/adb/candidates
//
// 设置页「快捷切换」用：列出所有可用的 adb 候选（当前生效 / 一键安装落点 /
// 常见安装位置扫描），每项带 version 探测结果，点击即切换。
func (s *Server) adbCandidates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, adb.ScanCandidates(s.dataDir, s.mgr.AdbPath()))
}

// startAdbInstall POST /api/settings/adb/install
//
// 一键安装 adb（platform-tools）：从 Google 官方仓库下载当前平台的 zip 到
// 数据目录 platform-tools/（URL 由服务端拼装，见 internal/adb/install.go）。
// 下载在后台进行，立即返回当前状态；进度经 GET 轮询。
// 安装成功后自动切换：设置里存档的路径会更新为新位置（保存时已存的旧路径
// 优先级高于自动探测，装完必须显式接管才能立即生效）。
func (s *Server) startAdbInstall(w http.ResponseWriter, _ *http.Request) {
	if err := s.adbInst.Start(); err != nil {
		// 安装中 / 平台不受支持 / 数据目录未知：都是"现在不能开始"的冲突
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, s.adbInst.Status())
}

// adbInstallStatus GET /api/settings/adb/install
func (s *Server) adbInstallStatus(w http.ResponseWriter, _ *http.Request) {
	st := s.adbInst.Status()
	// 首次看到安装成功（本进程生命周期内）时自动切换到新装的 adb：
	// 写入设置存档 + 切运行时路径 + 重启 server。与用户在设置页手动保存
	// 同一条链路（putAdbSettings），差异仅在路径来源是安装落点。
	if st.State == adb.AdbInstallDone && s.instAppliedPath != st.Path {
		s.instAppliedPath = st.Path
		if err := s.applyAdbPath(st.Path); err != nil {
			log.Printf("[adb] 自动切换到新安装的 adb 失败: %v", err)
		}
	}
	writeJSON(w, http.StatusOK, st)
}

// applyAdbPath 把路径落盘并切运行时路径，然后重启 adb server 生效。
// 一键安装成功后的自动切换用（错误直接返回，由调用方记日志）。
func (s *Server) applyAdbPath(path string) error {
	cur := s.settings.Get()
	cur.AdbPath = path
	if err := s.settings.Set(cur); err != nil {
		return err
	}
	s.mgr.SetAdbPath(path)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.restartAdbServerWith(ctx)
}

// applyAdbPathWithRestart 把路径落盘 + 切运行时路径 + 重启 server，返回设置页
// 所需的 message / restarted / version 字段（设置页手动保存场景用）。
// 落盘失败返回 {"error": ...}（调用方转 500）；重启失败不回滚设置：
// 路径本身已验证可执行，失败多半是环境问题（如 5037 被别的 adb 占用），
// 保留用户选择并在 message 里如实说明。
func (s *Server) applyAdbPathWithRestart(path, versionOut string) map[string]any {
	cur := s.settings.Get()
	cur.AdbPath = path
	if err := s.settings.Set(cur); err != nil {
		return map[string]any{"error": err.Error()}
	}
	s.mgr.SetAdbPath(path)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.restartAdbServerWith(ctx); err != nil {
		log.Printf("[adb] 切换 adb 路径后重启 server 失败: %v", err)
		return map[string]any{
			"message":   "已切换 adb 路径（adb server 重启失败：" + err.Error() + "，将在下次自动恢复时使用新路径）",
			"path":      path,
			"restarted": false,
			"version":   versionOut,
		}
	}
	return map[string]any{
		"message":   "已切换 adb 路径，adb server 已用新路径重启",
		"path":      path,
		"restarted": true,
		"version":   versionOut,
	}
}

// restartAdbServerWith 用当前路径执行 kill-server 再 start-server，
// 让新二进制接管 5037 端口。kill 失败（本来就没在跑）不视为错误。
func (s *Server) restartAdbServerWith(ctx context.Context) error {
	path, err := adb.ResolveAdbPath(s.mgr.AdbPath())
	if err != nil {
		return err
	}
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
