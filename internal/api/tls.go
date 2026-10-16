package api

import (
	"encoding/pem"
	"net/http"

	"adbreeze/internal/tlsutil"
)

// tlsStatus 返回当前 HTTPS 证书状态：是否开启、自签还是自备、CA 指纹、
// 叶证书覆盖的名字与可用访问地址（设置页「HTTPS 证书」块的数据源）。
func (s *Server) tlsStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.tls)
}

// downloadCACert 下发自签 CA 证书（.crt PEM）：
// Windows / macOS / Linux / Android 导入系统信任库用。
func (s *Server) downloadCACert(w http.ResponseWriter, r *http.Request) {
	if len(s.tls.CADER) == 0 {
		writeErr(w, http.StatusBadRequest, "当前没有可下载的 CA 证书（HTTPS 未开启，或正在使用自备证书）")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="adbreeze-ca.crt"`)
	_, _ = w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.tls.CADER}))
}

// downloadMobileConfig 下发 Apple 配置描述文件（.mobileconfig）：
// iOS / iPadOS 安装根证书用（装完还需在「证书信任设置」开完全信任，
// 见 tlsutil.MobileConfig 与界面文案）。
func (s *Server) downloadMobileConfig(w http.ResponseWriter, r *http.Request) {
	if len(s.tls.CADER) == 0 {
		writeErr(w, http.StatusBadRequest, "当前没有可下载的 CA 证书（HTTPS 未开启，或正在使用自备证书）")
		return
	}
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="adbreeze-ca.mobileconfig"`)
	_, _ = w.Write(tlsutil.MobileConfig(s.tls.CADER))
}
