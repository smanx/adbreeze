package api

import (
	"net/http"

	"adbreeze/internal/opencode"
)

// agentSettingsResp 是 GET /api/settings/agent 的响应。
type agentSettingsResp struct {
	Detected   bool   `json:"detected"`
	Path       string `json:"path"`
	Version    string `json:"version"`
	Binary     string `json:"binary"` // 当前保存的自定义路径
	ConfigPath string `json:"configPath"`
	Model      string `json:"model"`
}

func (s *Server) getAgentSettings(w http.ResponseWriter, _ *http.Request) {
	binary := s.settings.Get().Agent.Binary
	d := opencode.Detect(binary)
	writeJSON(w, http.StatusOK, agentSettingsResp{
		Detected: d.Installed, Path: d.Path, Version: d.Version,
		Binary: binary, ConfigPath: d.ConfigPath, Model: d.Model,
	})
}

// getAgentModels GET /api/settings/agent/models —— 读取 opencode 可用模型列表。
func (s *Server) getAgentModels(w http.ResponseWriter, _ *http.Request) {
	models, err := opencode.ListModels(s.settings.Get().Agent.Binary)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// getAgentConfig GET /api/settings/agent/config —— 读取 opencode 配置文件原文（供手动编辑）。
func (s *Server) getAgentConfig(w http.ResponseWriter, _ *http.Request) {
	content, path, err := opencode.ReadConfig()
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": content, "path": path})
}

// putAgentConfig PUT /api/settings/agent/config —— 写回用户手动编辑的配置原文。
func (s *Server) putAgentConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if !readJSONBody(w, r, &req) {
		return
	}
	if err := opencode.WriteConfig(req.Content); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	opencode.InvalidateModels()
	writeJSON(w, http.StatusOK, map[string]string{"message": "配置已保存（opencode 自动热加载）"})
}

// putAgentModel PUT /api/settings/agent/model —— 直接改 opencode 配置文件。
func (s *Server) putAgentModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model string `json:"model"`
	}
	if !readJSONBody(w, r, &req) {
		return
	}
	if err := opencode.WriteModel(req.Model); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "模型已更新（opencode 自动热加载）",
		"model":   req.Model,
	})
}

// startAgentInstall POST /api/settings/agent/install —— 手动触发检测/安装。
func (s *Server) startAgentInstall(w http.ResponseWriter, _ *http.Request) {
	if s.ocInst == nil {
		writeErr(w, http.StatusServiceUnavailable, "Agent 功能未启用")
		return
	}
	binary := s.settings.Get().Agent.Binary
	if err := s.ocInst.Start(binary); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, s.ocInst.Status())
}

func (s *Server) agentInstallStatus(w http.ResponseWriter, _ *http.Request) {
	if s.ocInst == nil {
		writeErr(w, http.StatusServiceUnavailable, "Agent 功能未启用")
		return
	}
	writeJSON(w, http.StatusOK, s.ocInst.Status())
}

// --- MCP（mobile-mcp）---

type mcpSettingsResp struct {
	Installed   bool   `json:"installed"`
	NodeOK      bool   `json:"nodeOk"`
	NodeVersion string `json:"nodeVersion"`
	NpxPath     string `json:"npxPath"`
	ConfigPath  string `json:"configPath"`
	Message     string `json:"message"`
}

func (s *Server) getMCPSettings(w http.ResponseWriter, _ *http.Request) {
	nodeVer, npx := opencode.DetectNode()
	installed := opencode.MCPInstalled()
	msg := ""
	if installed && npx == "" {
		msg = "mobile-mcp 已配置，但未检测到 npx（需 Node.js 20+）"
	} else if installed {
		msg = "mobile-mcp 已注册到 opencode"
	} else {
		msg = "mobile-mcp 尚未注册"
	}
	writeJSON(w, http.StatusOK, mcpSettingsResp{
		Installed: installed, NodeOK: npx != "",
		NodeVersion: nodeVer, NpxPath: npx,
		ConfigPath: opencode.ConfigPath(), Message: msg,
	})
}

// installMCP POST /api/settings/mcp/install —— 手动注册 mobile-mcp 到 opencode 配置。
func (s *Server) installMCP(w http.ResponseWriter, _ *http.Request) {
	if _, npx := opencode.DetectNode(); npx == "" {
		writeErr(w, http.StatusBadRequest, "未检测到 npx，请先安装 Node.js 20+")
		return
	}
	if err := opencode.RegisterMobileMCP(); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "mobile-mcp 已注册到 opencode；下次执行任务时自动可用",
	})
}
