package api

import (
	"net/http"

	"adbreeze/internal/scrcpy"
)

func (s *Server) startMirror(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	info, err := s.mirror.Start(serial, scrcpy.Options{})
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
