// Package scheduler 驱动定时任务：按 cron 触发，经 opencode Runner 执行，
// 并把每次执行写入 taskstore。同一任务不重叠执行，进程退出时优雅停止。
package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"adbreeze/internal/opencode"
	"adbreeze/internal/taskstore"
)

// Manager 管理所有定时任务的调度。
type Manager struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	store   *taskstore.Store
	runner  *opencode.Runner
	timers  map[string]*time.Timer
	running map[string]bool // taskID -> 是否正在执行
	wg      sync.WaitGroup
	stopped bool
}

func New(ctx context.Context, store *taskstore.Store, runner *opencode.Runner) *Manager {
	c, cancel := context.WithCancel(ctx)
	return &Manager{
		ctx: c, cancel: cancel, store: store, runner: runner,
		timers: map[string]*time.Timer{}, running: map[string]bool{},
	}
}

// Start 加载所有启用的任务并建立定时器。
func (m *Manager) Start() error {
	tasks := m.store.ListTasks()
	for _, tk := range tasks {
		if tk.Enabled {
			if err := m.schedule(tk); err != nil {
				log.Printf("[scheduler] 任务 %s(%s) 调度失败: %v", tk.Name, tk.ID, err)
			}
		}
	}
	log.Printf("[scheduler] 已启动，启用任务数: %d", len(m.timers))
	return nil
}

// Stop 停止所有定时器并等待执行中的任务结束（受各自超时约束）。
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	for _, tm := range m.timers {
		tm.Stop()
	}
	m.timers = map[string]*time.Timer{}
	m.cancel()
	m.mu.Unlock()
	m.wg.Wait()
	log.Printf("[scheduler] 已停止")
}

// Reload 在任务新增/更新/删除后重排该任务的定时器。
func (m *Manager) Reload(taskID string) {
	m.mu.Lock()
	if tm := m.timers[taskID]; tm != nil {
		tm.Stop()
		delete(m.timers, taskID)
	}
	stopped := m.stopped
	m.mu.Unlock()
	if stopped {
		return
	}
	tk, ok := m.store.GetTask(taskID)
	if !ok || !tk.Enabled {
		return
	}
	if err := m.schedule(tk); err != nil {
		log.Printf("[scheduler] 重排任务 %s 失败: %v", taskID, err)
	}
}

// schedule 解析 cron 并建立触发定时器。
func (m *Manager) schedule(tk taskstore.Task) error {
	cron, err := ParseCron(tk.Cron)
	if err != nil {
		return err
	}
	next := cron.Next(time.Now())
	if next.IsZero() {
		return fmt.Errorf("无法计算下次触发时间")
	}
	delay := time.Until(next)
	m.mu.Lock()
	defer m.mu.Unlock()
	tm := time.AfterFunc(delay, func() {
		m.fire(tk.ID, "cron")
		m.Reload(tk.ID) // 计算下一次触发
	})
	m.timers[tk.ID] = tm
	log.Printf("[scheduler] 任务 %s(%s) 下次触发: %s", tk.Name, tk.ID, next.Format("2006-01-02 15:04:05"))
	return nil
}

// RunNow 手动触发一次执行，返回新建的记录（状态 running）。
func (m *Manager) RunNow(taskID string) (taskstore.RunRecord, error) {
	if _, ok := m.store.GetTask(taskID); !ok {
		return taskstore.RunRecord{}, fmt.Errorf("任务不存在")
	}
	return m.fire(taskID, "manual")
}

// fire 执行一次任务：同步写 running 记录并立即返回，实际执行在后台 goroutine 进行，
// 完成后更新记录。HTTP「立即执行」接口因此不会被长任务阻塞。
func (m *Manager) fire(taskID, trigger string) (taskstore.RunRecord, error) {
	m.mu.Lock()
	if m.running[taskID] {
		m.mu.Unlock()
		return taskstore.RunRecord{}, fmt.Errorf("任务 %s 正在执行中，跳过本次触发", taskID)
	}
	m.running[taskID] = true
	m.mu.Unlock()

	tk, ok := m.store.GetTask(taskID)
	if !ok {
		m.mu.Lock()
		delete(m.running, taskID)
		m.mu.Unlock()
		return taskstore.RunRecord{}, fmt.Errorf("任务不存在")
	}

	rec := taskstore.RunRecord{
		ID:        newID(),
		TaskID:    tk.ID,
		TaskName:  tk.Name,
		Trigger:   trigger,
		StartedAt: time.Now().Format(time.RFC3339),
		Status:    "running",
		Model:     opencode.ReadModel(),
		Device:    tk.Device,
		Prompt:    tk.Prompt,
	}
	if err := m.store.AddRun(rec); err != nil {
		log.Printf("[scheduler] 写执行记录失败: %v", err)
	}

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			m.mu.Lock()
			delete(m.running, taskID)
			m.mu.Unlock()
		}()

		start := time.Now()
		res := m.runner.Run(m.ctx, tk.ID, promptWithDevice(tk.Prompt, tk.Device), tk.Model)
		rec.FinishedAt = time.Now().Format(time.RFC3339)
		rec.DurationMs = time.Since(start).Milliseconds()
		rec.Output = res.Output
		rec.ExitCode = res.ExitCode
		switch {
		case res.TimedOut:
			rec.Status = "timeout"
			rec.Error = errString(res.Err)
		case res.Err != nil:
			rec.Status = "failed"
			rec.Error = errString(res.Err)
		default:
			rec.Status = "success"
		}
		if err := m.store.UpdateRun(rec); err != nil {
			log.Printf("[scheduler] 更新执行记录失败: %v", err)
		}
		log.Printf("[scheduler] 任务 %s(%s) 执行完成: %s", tk.Name, tk.ID, rec.Status)
	}()
	return rec, nil
}

// promptWithDevice 把设备约束写进实际交给 Agent 的指令。
// 真正的任务放在最前面并加边界：此前把设备约束写在开头，模型会把它当成
// 主任务，只回一句“请问要做什么”，反而丢掉了用户真正要执行的指令。
func promptWithDevice(prompt, device string) string {
	device = strings.TrimSpace(device)
	if device == "" {
		return prompt
	}
	return fmt.Sprintf("【任务】\n%s\n\n【执行要求】\n目标设备（ADB serial）：%s。\n只使用 mobile-mcp 操作这台设备（必要时先用 mobile-mcp 的 list_devices / select_device 选中它），不要操作其他设备。\n直接完成上面的任务并给出结果，不要反问、不要等待用户确认。", prompt, device)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
