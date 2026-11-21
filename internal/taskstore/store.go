// Package taskstore 持久化定时任务定义与执行记录。
//
// 任务定义（tasks.json）整体读写；执行记录（task-runs.jsonl）append-only 追加，
// 启动时读入内存并按上限裁剪，避免单个 JSON 文件随执行次数无限膨胀。
// 两层数据都带互斥锁：HTTP 请求与调度器并发访问。
package taskstore

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Task 是一条定时任务定义。
type Task struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Cron    string `json:"cron"`   // 标准 5 段：分 时 日 月 周
	Prompt  string `json:"prompt"` // 交给 Agent 的指令
	Device  string `json:"device"` // 目标设备 serial（仅作 prompt 约束提示）
	Model   string `json:"model"`  // 仅记录用；模型实际从 opencode 配置读取
	Enabled bool   `json:"enabled"`
}

// RunRecord 是一次执行的记录。
type RunRecord struct {
	ID         string `json:"id"`
	TaskID     string `json:"taskId"`
	TaskName   string `json:"taskName"`
	Trigger    string `json:"trigger"` // cron / manual
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	Status     string `json:"status"` // running / success / failed / timeout
	Model      string `json:"model"`
	Device     string `json:"device"` // 目标设备 serial
	Prompt     string `json:"prompt"`
	Output     string `json:"output"`
	Error      string `json:"error"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
}

const defaultMaxRuns = 1000

// Store 是任务与执行记录的存储。
type Store struct {
	mu        sync.RWMutex
	tasks     map[string]Task
	taskIDs   []string // 保持创建顺序，供稳定列表
	runs      []RunRecord
	tasksPath string
	runsPath  string
	maxRuns   int
}

// New 打开存储。路径为空时退化为内存态（不落盘）。
func New(tasksPath, runsPath string) *Store {
	s := &Store{
		tasks:     map[string]Task{},
		tasksPath: tasksPath,
		runsPath:  runsPath,
		maxRuns:   defaultMaxRuns,
	}
	s.load()
	return s
}

func (s *Store) load() {
	if s.tasksPath != "" {
		data, err := os.ReadFile(s.tasksPath)
		if err == nil {
			var tasks []Task
			if json.Unmarshal(data, &tasks) == nil {
				for _, tk := range tasks {
					s.tasks[tk.ID] = tk
					s.taskIDs = append(s.taskIDs, tk.ID)
				}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "[taskstore] 读取任务失败: %v\n", err)
		}
	}
	if s.runsPath != "" {
		f, err := os.Open(s.runsPath)
		if err == nil {
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 64*1024), 2*1024*1024)
			for sc.Scan() {
				var r RunRecord
				if json.Unmarshal(sc.Bytes(), &r) == nil {
					s.runs = append(s.runs, r)
				}
			}
			f.Close()
		}
	}
	s.trimLocked()
}

// trimLocked 保留最近 maxRuns 条（调用方持锁）。
func (s *Store) trimLocked() {
	if len(s.runs) > s.maxRuns {
		s.runs = append([]RunRecord(nil), s.runs[len(s.runs)-s.maxRuns:]...)
	}
}

// --- 任务定义 ---

func (s *Store) ListTasks() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, 0, len(s.taskIDs))
	for _, id := range s.taskIDs {
		if tk, ok := s.tasks[id]; ok {
			out = append(out, tk)
		}
	}
	return out
}

func (s *Store) GetTask(id string) (Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tk, ok := s.tasks[id]
	return tk, ok
}

func (s *Store) SaveTask(tk Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tasks[tk.ID]; !exists {
		s.taskIDs = append(s.taskIDs, tk.ID)
	}
	s.tasks[tk.ID] = tk
	return s.persistTasksLocked()
}

func (s *Store) DeleteTask(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return false
	}
	delete(s.tasks, id)
	for i, x := range s.taskIDs {
		if x == id {
			s.taskIDs = append(s.taskIDs[:i], s.taskIDs[i+1:]...)
			break
		}
	}
	_ = s.persistTasksLocked()
	return true
}

func (s *Store) persistTasksLocked() error {
	if s.tasksPath == "" {
		return nil
	}
	list := s.ListTasksUnsafe()
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.tasksPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.tasksPath, data, 0o600)
}

// ListTasksUnsafe 调用方须持锁。
func (s *Store) ListTasksUnsafe() []Task {
	out := make([]Task, 0, len(s.taskIDs))
	for _, id := range s.taskIDs {
		if tk, ok := s.tasks[id]; ok {
			out = append(out, tk)
		}
	}
	return out
}

// --- 执行记录 ---

func (s *Store) AddRun(r RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append(s.runs, r)
	s.trimLocked()
	return s.persistRunsLocked()
}

func (s *Store) UpdateRun(r RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.runs {
		if s.runs[i].ID == r.ID {
			s.runs[i] = r
			break
		}
	}
	return s.persistRunsLocked()
}

// ListRuns 返回执行记录，可按 taskID 过滤；结果按开始时间倒序（最新在前）。
func (s *Store) ListRuns(taskID string, limit int) []RunRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RunRecord, 0, len(s.runs))
	for _, r := range s.runs {
		if taskID == "" || r.TaskID == taskID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Store) GetRun(id string) (RunRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.runs {
		if r.ID == id {
			return r, true
		}
	}
	return RunRecord{}, false
}

func (s *Store) DeleteRun(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.runs {
		if r.ID == id {
			s.runs = append(s.runs[:i], s.runs[i+1:]...)
			_ = s.persistRunsLocked()
			return true
		}
	}
	return false
}

func (s *Store) ClearRuns(taskID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.runs[:0]
	removed := 0
	for _, r := range s.runs {
		if taskID == "" || r.TaskID == taskID {
			removed++
			continue
		}
		kept = append(kept, r)
	}
	s.runs = kept
	_ = s.persistRunsLocked()
	return removed
}

func (s *Store) persistRunsLocked() error {
	if s.runsPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.runsPath), 0o700); err != nil {
		return err
	}
	tmp := s.runsPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, r := range s.runs {
		line, err := json.Marshal(r)
		if err != nil {
			f.Close()
			return err
		}
		if _, err := w.Write(line); err != nil {
			f.Close()
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.runsPath)
}
