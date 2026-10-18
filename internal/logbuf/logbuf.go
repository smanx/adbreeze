// Package logbuf 提供进程级日志环形缓冲：把标准库 log 的输出同时写入
// stdout 与内存环形缓冲，供网页设置页展示（GET /api/logs）。
//
// 设计要点：
//   - 挂在 log 标准库的输出端（log.SetOutput(io.MultiWriter)），业务代码
//     零改动——所有 log.Printf 自动入缓冲；
//   - 容量按「行数 × 单行上限」双限制：行数防列表无限增长，单行截断防
//     个别超长行（如大响应体误打印）撑爆内存；
//   - 写入端（任意业务 goroutine）与读取端（API 快照）经同一把锁串行化，
//     拷贝出快照再返回，避免读迭代与并发写冲突（CME 教训同源）。
package logbuf

import (
	"io"
	"strings"
	"sync"
	"time"
)

const (
	maxLines  = 500                 // 环形缓冲保留的行数
	maxLineRp = 4 * 1024            // 单行截断上限（字节）
	timeLayout = "2006/01/02 15:04:05"
)

// Line 是缓冲中的一条日志（API 响应字段）。
type Line struct {
	T   time.Time `json:"t"`
	Text string   `json:"text"`
}

type buffer struct {
	mu    sync.Mutex
	lines []Line
}

var buf = &buffer{lines: make([]Line, 0, 128)}

// Writer 返回可交给 log.SetOutput 的 io.Writer（与 os.Stdout 组成 MultiWriter 用）。
// Write 实现按行拆分：标准库 log 每次输出天然带换行结尾，一行一次 Write，
// 但稳妥起见仍按 \n 切分逐条入缓冲。
func Writer() io.Writer { return writer{} }

type writer struct{}

func (writer) Write(p []byte) (int, error) {
	s := string(p)
	for _, part := range strings.Split(s, "\n") {
		if part == "" {
			continue
		}
		if len(part) > maxLineRp {
			part = part[:maxLineRp] + "…(截断)"
		}
		buf.mu.Lock()
		buf.lines = append(buf.lines, Line{T: time.Now(), Text: part})
		if len(buf.lines) > maxLines {
			buf.lines = buf.lines[len(buf.lines)-maxLines:]
		}
		buf.mu.Unlock()
	}
	return len(p), nil
}

// Snapshot 返回缓冲快照（按时间正序）。since 非零时只返回该时刻之后的行
// （增量拉取用；零值 = 全量）。
func Snapshot(since time.Time) []Line {
	buf.mu.Lock()
	defer buf.mu.Unlock()
	if since.IsZero() {
		out := make([]Line, len(buf.lines))
		copy(out, buf.lines)
		return out
	}
	// 环形缓冲整体有序，找到第一条晚于 since 的行即可
	lo := 0
	for lo < len(buf.lines) && !buf.lines[lo].T.After(since) {
		lo++
	}
	out := make([]Line, len(buf.lines)-lo)
	copy(out, buf.lines[lo:])
	return out
}

// Format 时间戳格式化（API 无需原始字段时用；当前前端直接用 t 字段序列化）。
func (l Line) Format() string { return l.T.Format(timeLayout) }
