package api

import (
	"encoding/json"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.version})
}

func (s *Server) listDevices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.mgr.Devices())
}

type connectReq struct {
	Address string `json:"address"`
}

func (s *Server) connectDevice(w http.ResponseWriter, r *http.Request) {
	var req connectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Address == "" {
		writeErr(w, http.StatusBadRequest, "需要 address 字段，如 192.168.1.20:5555")
		return
	}
	msg, err := s.mgr.Connect(req.Address)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

func (s *Server) disconnectDevice(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if err := s.mgr.Disconnect(serial); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "已断开"})
}

type shellReq struct {
	Command string `json:"command"`
}

func (s *Server) shell(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	var req shellReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Command == "" {
		writeErr(w, http.StatusBadRequest, "需要 command 字段")
		return
	}
	out, err := s.mgr.Shell(serial, req.Command)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
}
