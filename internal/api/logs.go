package api

import (
	"net/http"
	"strconv"
	"time"

	"adbreeze/internal/logbuf"
)

// 服务日志（设置页「服务日志」）：进程级 log 输出的环形缓冲快照。
//
// 端点：GET /api/logs?since=<unix毫秒>
//   - 无 since：返回缓冲全量（默认 500 行）
//   - 带 since：增量——只返回该时刻之后的行（前端轮询省带宽）
// 响应：{"lines": [{"t": "2006/01/02 15:04:05", "text": "..."}], "now": <unix毫秒>}
// now 供前端作为下一轮 since（服务端时间，避免客户端时钟漂移漏行/重行）。

type logsResp struct {
	Lines []logbuf.Line `json:"lines"`
	Now   int64         `json:"now"` // unix 毫秒，下一轮 since
}

func (s *Server) serviceLogs(w http.ResponseWriter, r *http.Request) {
	var since time.Time
	if raw := r.URL.Query().Get("since"); raw != "" {
		if ms, err := strconv.ParseInt(raw, 10, 64); err == nil && ms > 0 {
			since = time.UnixMilli(ms)
		}
	}
	lines := logbuf.Snapshot(since)
	if lines == nil {
		lines = []logbuf.Line{}
	}
	writeJSON(w, http.StatusOK, logsResp{Lines: lines, Now: time.Now().UnixMilli()})
}
