package api

import (
	"encoding/json"
	"net/http"

	"adbreeze/internal/scrcpy"
)

func (s *Server) startMirror(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	// 请求体可选：{"deviceAudio":true} 切换为回放捕获（设备扬声器正常出声）
	var req struct {
		DeviceAudio bool `json:"deviceAudio"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	opts := scrcpy.Options{}
	if req.DeviceAudio {
		opts.AudioSource = "playback"
	}
	info, err := s.mirror.Start(serial, opts)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) stopMirror(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if err := s.mirror.Stop(serial); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "投屏已停止"})
}

func (s *Server) listMirrors(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.mirror.Active())
}
