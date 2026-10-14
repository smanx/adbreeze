package api

import (
	"encoding/json"
	"net/http"

	"github.com/pion/webrtc/v4"
)

// createWebRTC 是 WebRTC 方案的信令端点：接收浏览器 offer，
// 完成连接建立后返回 answer（非 trickle，answer 携带完整 ICE candidate）。
func (s *Server) createWebRTC(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	var req struct {
		SDP  string `json:"sdp"`
		Type string `json:"type"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.SDP == "" {
		writeErr(w, http.StatusBadRequest, "无效的信令请求")
		return
	}
	answer, err := s.webrtc.Create(serial, s.mirror, webrtc.SessionDescription{
		Type: webrtc.NewSDPType(req.Type),
		SDP:  req.SDP,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"sdp": answer.SDP, "type": answer.Type.String()})
}
