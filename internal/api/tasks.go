package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"adbreeze/internal/scheduler"
	"adbreeze/internal/taskstore"
)

func (s *Server) tasksReady(w http.ResponseWriter) bool {
	if s.tasks == nil || s.sched == nil {
		writeErr(w, http.StatusServiceUnavailable, "定时任务功能未启用")
		return false
	}
	return true
}

func (s *Server) listTasks(w http.ResponseWriter, _ *http.Request) {
	if !s.tasksReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.tasks.ListTasks())
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	if !s.tasksReady(w) {
		return
	}
	var tk taskstore.Task
	if !readJSONBody(w, r, &tk) {
		return
	}
	tk.ID = strings.TrimSpace(tk.ID)
	if tk.ID == "" {
		tk.ID = newTaskID()
	}
	tk.Name = strings.TrimSpace(tk.Name)
	if tk.Name == "" {
		writeErr(w, http.StatusBadRequest, "任务名称不能为空")
		return
	}
	tk.Device = strings.TrimSpace(tk.Device)
	if tk.Device == "" {
		writeErr(w, http.StatusBadRequest, "目标设备不能为空，请从设备列表中选择")
		return
	}
	if _, err := scheduler.ParseCron(tk.Cron); err != nil {
		writeErr(w, http.StatusBadRequest, "cron 表达式非法: "+err.Error())
		return
	}
	if strings.TrimSpace(tk.Prompt) == "" {
		writeErr(w, http.StatusBadRequest, "任务指令不能为空")
		return
	}
	if err := s.tasks.SaveTask(tk); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sched.Reload(tk.ID)
	writeJSON(w, http.StatusOK, tk)
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	if !s.tasksReady(w) {
		return
	}
	id := r.PathValue("id")
	if _, ok := s.tasks.GetTask(id); !ok {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	var tk taskstore.Task
	if !readJSONBody(w, r, &tk) {
		return
	}
	tk.ID = id
	tk.Name = strings.TrimSpace(tk.Name)
	if tk.Name == "" {
		writeErr(w, http.StatusBadRequest, "任务名称不能为空")
		return
	}
	tk.Device = strings.TrimSpace(tk.Device)
	if tk.Device == "" {
		writeErr(w, http.StatusBadRequest, "目标设备不能为空，请从设备列表中选择")
		return
	}
	if _, err := scheduler.ParseCron(tk.Cron); err != nil {
		writeErr(w, http.StatusBadRequest, "cron 表达式非法: "+err.Error())
		return
	}
	if strings.TrimSpace(tk.Prompt) == "" {
		writeErr(w, http.StatusBadRequest, "任务指令不能为空")
		return
	}
	if err := s.tasks.SaveTask(tk); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sched.Reload(tk.ID)
	writeJSON(w, http.StatusOK, tk)
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	if !s.tasksReady(w) {
		return
	}
	id := r.PathValue("id")
	if !s.tasks.DeleteTask(id) {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	s.sched.Reload(id)
	writeJSON(w, http.StatusOK, map[string]string{"message": "已删除"})
}

func (s *Server) runTask(w http.ResponseWriter, r *http.Request) {
	if !s.tasksReady(w) {
		return
	}
	rec, err := s.sched.RunNow(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, rec)
}

func (s *Server) listTaskRuns(w http.ResponseWriter, r *http.Request) {
	if !s.tasksReady(w) {
		return
	}
	limit := 0
	if q := r.URL.Query().Get("limit"); q != "" {
		limit = atoi(q)
	}
	writeJSON(w, http.StatusOK, s.tasks.ListRuns(r.PathValue("id"), limit))
}

func (s *Server) deleteTaskRun(w http.ResponseWriter, r *http.Request) {
	if !s.tasksReady(w) {
		return
	}
	if !s.tasks.DeleteRun(r.PathValue("runId")) {
		writeErr(w, http.StatusNotFound, "记录不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "已删除"})
}

func (s *Server) clearTaskRuns(w http.ResponseWriter, r *http.Request) {
	if !s.tasksReady(w) {
		return
	}
	n := s.tasks.ClearRuns(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"message": "已清空", "count": n})
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func newTaskID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "task-fallback"
	}
	return "task-" + hex.EncodeToString(b)
}
