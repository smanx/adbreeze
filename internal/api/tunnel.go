package api

import (
	"log"
	"net/http"
	"net/url"
	"runtime"
	"strings"

	"adbreeze/internal/config"
	"adbreeze/internal/tunnel"
)

// 外网访问（Cloudflare Tunnel）设置与控制端点。
//
// 配置走 settings 存档（与 adb 路径同一条链路），运行态由 tunnel.Manager
// 持有、绝不落盘。默认密码不拦截开启（与局域网"只警告"策略一致）：
// 公网暴露是用户的显式选择，但会记 WARN 日志并让 UI 展示提醒。

// tunnelSettingsResp 是 GET /api/settings/tunnel 与 PUT 成功后的响应。
type tunnelSettingsResp struct {
	// 存档配置
	Binary      string `json:"binary"`      // cloudflared 路径（空=自动探测）
	Service     string `json:"service"`     // 隧道指向（空=http://127.0.0.1:<HTTP端口>）
	Protocol    string `json:"protocol"`    // quic/http2/空
	TokenSet    bool   `json:"tokenSet"`    // 是否配置了 token（Named 模式）
	TokenMask   string `json:"tokenMask"`   // token 脱敏预览（绝不下发全文）
	AutoRestart bool   `json:"autoRestart"` // 意外退出自动重启
	Resume      bool   `json:"resume"`      // 记住上次状态：下次启动是否自动恢复

	// cloudflared 探测结果
	Resolved  string `json:"resolved"`  // 探测到的路径（空=没找到）
	Available bool   `json:"available"` // 是否有可用二进制
	Version   string `json:"version"`   // cloudflared 版本

	// Platform 为服务端平台（GOOS/GOARCH）——一键安装据此下载对应资产，
	// 界面提示用（浏览器所在平台未必等于服务端平台）
	Platform string `json:"platform"`

	// WeakPassword 是否为出厂默认密码（仅供 UI 展示提醒，不拦截开启）
	WeakPassword bool `json:"weakPassword"`

	// 仅 PUT 响应携带
	Message   string `json:"message,omitempty"`
	Restarted bool   `json:"restarted,omitempty"`
}

// tunnelSettingsReq 是 PUT /api/settings/tunnel 的请求体。
// 全部字段用指针：nil = 未提供（保持原值），非 nil = 替换（空串 = 清除）。
// 合并语义而非全量替换——防止只更新 token 的调用方把 binary 等字段误清空。
// 注意：resume（是否随启动恢复）不在此列——它由开启/关闭动作自动维护，
// 不接受调用方直接设置。
type tunnelSettingsReq struct {
	Binary      *string `json:"binary"`
	Service     *string `json:"service"`
	Protocol    *string `json:"protocol"`
	Token       *string `json:"token"`
	AutoRestart *bool   `json:"autoRestart"`
}

// getTunnelSettings GET /api/settings/tunnel
func (s *Server) getTunnelSettings(w http.ResponseWriter, _ *http.Request) {
	tc := s.settings.Get().Tunnel
	probe := s.tun.Probe()
	writeJSON(w, http.StatusOK, tunnelSettingsResp{
		Binary:       tc.Binary,
		Service:      tc.Service,
		Protocol:     tc.Protocol,
		TokenSet:     tc.Token != "",
		TokenMask:    tunnel.MaskToken(tc.Token),
		AutoRestart:  tc.AutoRestartEnabled(),
		Resume:       tc.ResumeEnabled(),
		Resolved:     probe.Path,
		Platform:     runtime.GOOS + "/" + runtime.GOARCH,
		Available:    probe.Path != "",
		Version:      probe.Version,
		WeakPassword: s.auth.WeakPassword(),
	})
}

// putTunnelSettings PUT /api/settings/tunnel
//
// 校验 → 落盘 → 同步 Manager →（在跑则）热重启。任一步失败都不改变运行时状态。
func (s *Server) putTunnelSettings(w http.ResponseWriter, r *http.Request) {
	var req tunnelSettingsReq
	if !readJSONBody(w, r, &req) {
		return
	}

	// 合并进当前存档（nil = 保持原值）
	cur := s.settings.Get()
	tc := cur.Tunnel
	if req.Binary != nil {
		tc.Binary = strings.TrimSpace(*req.Binary)
	}
	if req.Service != nil {
		tc.Service = strings.TrimSpace(*req.Service)
	}
	if req.Protocol != nil {
		tc.Protocol = strings.TrimSpace(*req.Protocol)
	}
	if req.Token != nil {
		tc.Token = strings.TrimSpace(*req.Token)
	}
	if req.AutoRestart != nil {
		tc.AutoRestart = req.AutoRestart
	}
	if req.AutoRestart != nil {
		tc.AutoRestart = req.AutoRestart
	}

	// 校验（在合并后的最终值上进行）：service 必须是 http(s) URL；protocol 只认 quic/http2/空
	if tc.Service != "" {
		u, err := url.Parse(tc.Service)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			writeErr(w, http.StatusBadRequest, "隧道指向必须是合法的 http:// 或 https:// 地址（如 http://127.0.0.1:9080）")
			return
		}
	}
	switch tc.Protocol {
	case "", "quic", "http2":
	default:
		writeErr(w, http.StatusBadRequest, "传输协议只支持 quic / http2（留空为默认 quic；网络封锁 UDP 时选 http2）")
		return
	}

	// 指定了路径就先探测：把"路径不可用"在保存时挡下，别留到启动才炸
	probe := tunnel.Probe(tunnel.Options{Binary: tc.Binary, DataDir: s.tun.Options().DataDir})
	if tc.Binary != "" && probe.Path == "" {
		writeErr(w, http.StatusBadRequest, "cloudflared 探测失败: "+probe.Error)
		return
	}

	// 落盘（失败则不改变任何状态）
	cur.Tunnel = tc
	if err := s.settings.Set(cur); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 同步 Manager：保留 DataDir / HTTPPort 等非用户字段
	opts := s.tun.Options()
	opts.Binary = tc.Binary
	opts.Service = tc.Service
	opts.Protocol = tc.Protocol
	opts.Token = tc.Token
	opts.AutoRestart = tc.AutoRestartEnabled()
	s.tun.SetOptions(opts)

	// 在跑则热重启，让新配置立即生效（与切换 adb 路径后立即重启 server 一致）
	restarted := false
	if st := s.tun.Status(); st.State == "starting" || st.State == "running" {
		s.tun.Restart()
		restarted = true
	}

	msg := "已保存外网访问设置"
	if restarted {
		msg += "，隧道正在用新配置重启"
	} else {
		msg += "（下次开启隧道时生效）"
	}
	log.Printf("[tunnel] 设置已更新: binary=%q service=%q protocol=%q token=%v resume=%v",
		tc.Binary, tc.Service, tc.Protocol, tc.Token != "", tc.ResumeEnabled())

	writeJSON(w, http.StatusOK, tunnelSettingsResp{
		Binary:       tc.Binary,
		Service:      tc.Service,
		Protocol:     tc.Protocol,
		TokenSet:     tc.Token != "",
		TokenMask:    tunnel.MaskToken(tc.Token),
		AutoRestart:  tc.AutoRestartEnabled(),
		Resume:       tc.ResumeEnabled(),
		Resolved:     probe.Path,
		Platform:     runtime.GOOS + "/" + runtime.GOARCH,
		Available:    probe.Path != "",
		Version:      probe.Version,
		WeakPassword: s.auth.WeakPassword(),
		Message:      msg,
		Restarted:    restarted,
	})
}

// setResume 维护"记住上次状态"标志：开启动作置 true、手动关闭置 false。
// 存档失败不影响本次操作本身（隧道已启/已停），只记日志——
// 最坏后果是下次启动不自动恢复，用户手动再开一次即可。
func (s *Server) setResume(v bool) {
	cur := s.settings.Get()
	if cur.Tunnel.ResumeEnabled() == v && cur.Tunnel.Resume != nil {
		return // 无变化，跳过写盘
	}
	cur.Tunnel.Resume = &v
	cur.Tunnel.LegacyAutoStart = nil
	if err := s.settings.Set(cur); err != nil {
		log.Printf("[tunnel] 保存 resume=%v 失败（不影响本次操作）: %v", v, err)
		return
	}
	log.Printf("[tunnel] resume=%v（%s）", v, map[bool]string{true: "下次启动将自动恢复隧道", false: "下次启动不再自动恢复"}[v])
}

// tunnelStatus GET /api/tunnel
func (s *Server) tunnelStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.tun.Status())
}

// tunnelCandidates GET /api/settings/tunnel/candidates
//
// 设置页「快捷切换」用：列出所有可用的 cloudflared 候选（当前指定 /
// 一键安装落点 / PATH / 常见安装位置），每项带 version 探测结果。
func (s *Server) tunnelCandidates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, tunnel.ScanCandidates(s.dataDir, s.tun.Options().Binary))
}

// startTunnelInstall POST /api/settings/tunnel/install
//
// 一键安装 cloudflared：从官方 GitHub releases 下载当前平台二进制到数据目录
// bin/（URL 由服务端按 GOOS/GOARCH 拼装，见 internal/tunnel/install.go）。
// 下载在后台进行，立即返回当前状态；进度经 GET 轮询。
func (s *Server) startTunnelInstall(w http.ResponseWriter, _ *http.Request) {
	if err := s.tun.StartInstall(); err != nil {
		// 安装中 / 平台不受支持 / 数据目录未知：都是"现在不能开始"的冲突
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, s.tun.InstallStatus())
}

// tunnelInstallStatus GET /api/settings/tunnel/install
func (s *Server) tunnelInstallStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.tun.InstallStatus())
}

// startTunnel POST /api/tunnel/start
func (s *Server) startTunnel(w http.ResponseWriter, _ *http.Request) {
	// 默认密码不拦截（与局域网"默认密码 + 对外监听只警告"一致）：是否暴露
	// 公网是用户的显式选择。但可猜密码 + 公网地址风险实在大，记 WARN 日志，
	// UI 侧也会展示提醒（见 TunnelSettings）。
	if s.auth.WeakPassword() {
		log.Printf("[tunnel][安全警告] 正在使用默认密码 %s 且开启了外网访问，公网任何人都可尝试登录；请用 -password 或 ADBREEZE_PASSWORD 设置强密码", config.DefaultPassword)
	}
	// 本机得有 cloudflared，否则启动即失败
	probe := s.tun.Probe()
	if probe.Path == "" {
		writeErr(w, http.StatusConflict, "未找到 cloudflared，无法开启外网访问；"+probe.Error)
		return
	}
	log.Printf("[tunnel] 手动开启外网访问（%s 模式）", s.tun.Options().Mode())
	_ = s.tun.Start()
	s.setResume(true) // 开启过且未手动关闭 → 下次启动自动恢复
	writeJSON(w, http.StatusOK, s.tun.Status())
}

// stopTunnel POST /api/tunnel/stop
func (s *Server) stopTunnel(w http.ResponseWriter, _ *http.Request) {
	log.Printf("[tunnel] 手动关闭外网访问")
	s.tun.Stop()
	s.setResume(false) // 手动关闭 → 下次启动不再自动恢复
	writeJSON(w, http.StatusOK, s.tun.Status())
}
