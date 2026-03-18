package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GrapeInTheTree/pocket-claude/internal/project"
	"github.com/GrapeInTheTree/pocket-claude/internal/store"
)

const maxBackgroundSlots = 3

// taskCounter provides unique IDs without millisecond collisions.
var taskCounter atomic.Int64

func init() {
	// Seed with current time so IDs are roughly chronological across restarts.
	taskCounter.Store(time.Now().UnixMilli())
}

// BackgroundTask represents a single background task.
type BackgroundTask struct {
	ID        string
	Project   string
	Message   string
	State     string // "running", "approval", "done", "failed", "cancelled"
	StartedAt time.Time
	Cancel    context.CancelFunc
	Error     string
}

// BackgroundPool manages concurrent background tasks with independent executors.
type BackgroundPool struct {
	mu     sync.Mutex
	tasks  map[string]*BackgroundTask
	wg     sync.WaitGroup
	closed bool // set by CancelAll to reject new submissions

	approvals sync.Map // taskID -> chan bool

	sem            chan struct{}
	projects       *project.Manager
	sendFn         func(string) error
	sendApprovalFn func(approvalID, text string) error
	sendTypingFn   func(ctx context.Context)
	logger         *slog.Logger
}

// NewBackgroundPool creates a new pool with bounded concurrency.
func NewBackgroundPool(
	projects *project.Manager,
	sendFn func(string) error,
	sendApprovalFn func(approvalID, text string) error,
	sendTypingFn func(ctx context.Context),
	logger *slog.Logger,
) *BackgroundPool {
	return &BackgroundPool{
		tasks:          make(map[string]*BackgroundTask),
		sem:            make(chan struct{}, maxBackgroundSlots),
		projects:       projects,
		sendFn:         sendFn,
		sendApprovalFn: sendApprovalFn,
		sendTypingFn:   sendTypingFn,
		logger:         logger,
	}
}

// Submit starts a background task. Returns the task ID or error if slots are full or pool is closed.
func (bp *BackgroundPool) Submit(ctx context.Context, projectName, message string) (string, error) {
	bp.mu.Lock()
	if bp.closed {
		bp.mu.Unlock()
		return "", fmt.Errorf("background pool is shutting down")
	}
	bp.mu.Unlock()

	// Try to acquire semaphore without blocking
	select {
	case bp.sem <- struct{}{}:
	default:
		return "", fmt.Errorf("all %d background slots are busy", maxBackgroundSlots)
	}

	taskID := fmt.Sprintf("bg_%d", taskCounter.Add(1))

	taskCtx, cancel := context.WithCancel(ctx)
	task := &BackgroundTask{
		ID:        taskID,
		Project:   projectName,
		Message:   message,
		State:     "running",
		StartedAt: time.Now(),
		Cancel:    cancel,
	}

	bp.mu.Lock()
	if bp.closed {
		bp.mu.Unlock()
		cancel()
		<-bp.sem
		return "", fmt.Errorf("background pool is shutting down")
	}
	bp.tasks[taskID] = task
	bp.mu.Unlock()

	bp.wg.Add(1)
	go func() {
		defer func() {
			<-bp.sem
			bp.wg.Done()
		}()
		bp.run(taskCtx, task)
	}()

	return taskID, nil
}

func (bp *BackgroundPool) run(ctx context.Context, task *BackgroundTask) {
	bp.logger.Info("Background task started", "id", task.ID, "project", task.Project, "message", Truncate(task.Message, 80))

	// Create ephemeral executor for this task
	exec, err := bp.projects.NewBackgroundExecutor(task.Project)
	if err != nil {
		bp.setTaskState(task.ID, "failed", err.Error())
		bp.sendFn(fmt.Sprintf("❌ Background Task Failed\n🆔 %s\nError: %s", task.ID, err.Error()))
		return
	}

	// Start typing indicator
	typingCtx, stopTyping := context.WithCancel(ctx)
	go bp.sendTypingFn(typingCtx)
	defer stopTyping()

	// Phase 1: Execute with default permissions
	result, err := exec.Execute(ctx, task.Message, false)
	if err != nil {
		if ctx.Err() != nil {
			bp.setTaskState(task.ID, "cancelled", "")
			bp.logger.Info("Background task cancelled", "id", task.ID)
			return
		}
		bp.setTaskState(task.ID, "failed", err.Error())
		bp.sendFn(fmt.Sprintf("❌ Background Task Failed\n📂 Project: %s\n🆔 %s\n\n%s", task.Project, task.ID, Truncate(err.Error(), 200)))
		return
	}

	// Phase 2: Permission approval if needed
	if len(result.PermissionDenials) > 0 {
		bp.setTaskState(task.ID, "approval", "")
		stopTyping() // stop typing while waiting for user
		bp.logger.Info("Background task needs approval", "id", task.ID, "denials", len(result.PermissionDenials))

		approved, err := bp.requestApproval(ctx, task.ID, result)
		if err != nil {
			bp.logger.Error("Background approval failed", "id", task.ID, "error", err)
			bp.setTaskState(task.ID, "done", "")
			bp.sendResult(task, result)
			return
		}

		if !approved {
			bp.logger.Info("Background task denied", "id", task.ID)
			bp.setTaskState(task.ID, "done", "")
			bp.sendResult(task, result)
			return
		}

		bp.setTaskState(task.ID, "running", "")
		// Restart typing for phase 2
		typingCtx2, stopTyping2 := context.WithCancel(ctx)
		go bp.sendTypingFn(typingCtx2)
		defer stopTyping2()

		bp.logger.Info("Background task approved, re-executing", "id", task.ID)
		result, err = exec.Execute(ctx, task.Message, true)
		if err != nil {
			if ctx.Err() != nil {
				bp.setTaskState(task.ID, "cancelled", "")
				return
			}
			bp.setTaskState(task.ID, "failed", err.Error())
			bp.sendFn(fmt.Sprintf("❌ Background Task Failed\n📂 Project: %s\n🆔 %s\n\n%s", task.Project, task.ID, Truncate(err.Error(), 200)))
			return
		}
	}

	bp.projects.TrackUsageForProject(task.Project, result)
	bp.setTaskState(task.ID, "done", "")
	bp.sendResult(task, result)
}

func (bp *BackgroundPool) requestApproval(ctx context.Context, taskID string, result *store.CLIResult) (bool, error) {
	ch := make(chan bool, 1)
	bp.approvals.Store(taskID, ch)
	defer bp.approvals.Delete(taskID)

	text := fmt.Sprintf("🔄 Background Task %s\n\n", taskID) + buildPermissionMessage(result)

	if err := bp.sendApprovalFn(taskID, text); err != nil {
		return false, fmt.Errorf("send approval request: %w", err)
	}

	bp.logger.Info("Background task waiting for approval", "id", taskID)

	select {
	case approved := <-ch:
		return approved, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(2 * time.Minute):
		bp.sendFn(fmt.Sprintf("⏰ Background task %s: permission timed out (2 min).", taskID))
		return false, fmt.Errorf("approval timeout")
	}
}

// ResolveApproval resolves a pending background task approval.
func (bp *BackgroundPool) ResolveApproval(id string, approved bool) {
	if val, ok := bp.approvals.Load(id); ok {
		if ch, ok := val.(chan bool); ok {
			select {
			case ch <- approved:
			default:
			}
		}
	}
}

func (bp *BackgroundPool) sendResult(task *BackgroundTask, result *store.CLIResult) {
	elapsed := time.Since(task.StartedAt)
	var elapsedStr string
	if elapsed < time.Minute {
		elapsedStr = fmt.Sprintf("%.0fs", elapsed.Seconds())
	} else {
		elapsedStr = fmt.Sprintf("%dm%ds", int(elapsed.Minutes()), int(elapsed.Seconds())%60)
	}

	response := result.Result
	if response == "" {
		response = "(no response)"
	}

	summary := buildToolSummary(result)

	var sb strings.Builder
	sb.WriteString("✅ Background Task Done\n")
	sb.WriteString(fmt.Sprintf("📂 Project: %s\n", task.Project))
	sb.WriteString(fmt.Sprintf("🆔 %s\n", task.ID))
	if summary != "" {
		sb.WriteString(summary + "\n")
	}
	sb.WriteString(fmt.Sprintf("⏱ %s\n\n", elapsedStr))
	sb.WriteString(response)

	if err := bp.sendFn(sb.String()); err != nil {
		bp.logger.Error("Failed to send background result", "id", task.ID, "error", err)
	}
	bp.logger.Info("Background task completed", "id", task.ID, "elapsed", elapsedStr)
}

func (bp *BackgroundPool) setTaskState(id, state, errMsg string) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if t, ok := bp.tasks[id]; ok {
		t.State = state
		t.Error = errMsg
	}
}

// Status returns a formatted status string of all background tasks.
func (bp *BackgroundPool) Status() string {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	if len(bp.tasks) == 0 {
		return "No background tasks."
	}

	// Collect active tasks (running or approval)
	type taskInfo struct {
		id      string
		state   string
		project string
		message string
		elapsed time.Duration
	}
	var active []taskInfo
	for id, t := range bp.tasks {
		if t.State == "running" || t.State == "approval" {
			active = append(active, taskInfo{
				id: id, state: t.State, project: t.Project,
				message: t.Message, elapsed: time.Since(t.StartedAt),
			})
		}
	}

	if len(active) == 0 {
		return "No active background tasks."
	}

	sort.Slice(active, func(i, j int) bool {
		return active[i].elapsed > active[j].elapsed // oldest first
	})

	var sb strings.Builder
	sb.WriteString("🔄 Background Tasks\n\n")

	stateIcon := map[string]string{"running": "🔄", "approval": "🔐"}

	for _, a := range active {
		icon := stateIcon[a.state]
		var elapsedStr string
		if a.elapsed < time.Minute {
			elapsedStr = fmt.Sprintf("%.0fs", a.elapsed.Seconds())
		} else {
			elapsedStr = fmt.Sprintf("%dm%ds", int(a.elapsed.Minutes()), int(a.elapsed.Seconds())%60)
		}
		sb.WriteString(fmt.Sprintf("%s %s [%s] %s\n", icon, a.id, a.state, Truncate(a.message, 30)))
		sb.WriteString(fmt.Sprintf("   📂 %s | ⏱ %s\n\n", a.project, elapsedStr))
	}

	sb.WriteString(fmt.Sprintf("Slots: %d/%d", len(active), maxBackgroundSlots))

	return sb.String()
}

// RunningCount returns the number of active (running or approval) background tasks.
func (bp *BackgroundPool) RunningCount() int {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	count := 0
	for _, t := range bp.tasks {
		if t.State == "running" || t.State == "approval" {
			count++
		}
	}
	return count
}

// Cancel cancels a specific background task by ID.
func (bp *BackgroundPool) Cancel(taskID string) error {
	bp.mu.Lock()
	t, ok := bp.tasks[taskID]
	if !ok {
		bp.mu.Unlock()
		return fmt.Errorf("task %q not found", taskID)
	}
	state := t.State
	if state != "running" && state != "approval" {
		bp.mu.Unlock()
		return fmt.Errorf("task %q is already %s", taskID, state)
	}
	cancelFn := t.Cancel
	t.State = "cancelled"
	t.Error = "cancelled by user"
	bp.mu.Unlock()

	cancelFn()
	bp.logger.Info("Background task cancelled", "id", taskID)
	return nil
}

// CancelAll cancels all running background tasks and rejects new submissions (for shutdown).
func (bp *BackgroundPool) CancelAll() {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.closed = true
	for _, t := range bp.tasks {
		if t.State == "running" || t.State == "approval" {
			t.Cancel()
			t.State = "cancelled"
		}
	}
}

// Wait waits for all background goroutines to finish.
func (bp *BackgroundPool) Wait() {
	bp.wg.Wait()
}

// Cleanup removes completed tasks older than maxAge.
func (bp *BackgroundPool) Cleanup(maxAge time.Duration) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	now := time.Now()
	for id, t := range bp.tasks {
		if t.State == "done" || t.State == "failed" || t.State == "cancelled" {
			if now.Sub(t.StartedAt) > maxAge {
				delete(bp.tasks, id)
			}
		}
	}
}
