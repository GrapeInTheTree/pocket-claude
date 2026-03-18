package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GrapeInTheTree/pocket-claude/internal/claude"
	"github.com/GrapeInTheTree/pocket-claude/internal/project"
	"github.com/GrapeInTheTree/pocket-claude/internal/store"
)

type Worker struct {
	queue     chan store.InboxMessage
	inFlight  sync.Map
	approvals sync.Map

	projects       *project.Manager
	store          *store.Store
	sendFn         func(string) error
	sendApprovalFn func(approvalID, text string) error
	sendTypingFn   func(ctx context.Context)
	logger         *slog.Logger
	wg             sync.WaitGroup

	approvalTimeout time.Duration
	messageTTL      time.Duration
	maxRetryCount   int

	cancelMu      sync.Mutex
	currentCancel context.CancelFunc
	currentMsgID  string
	cancelled     bool

	bgPool *BackgroundPool
}

func New(
	queueSize int,
	maxRetry int,
	messageTTL time.Duration,
	projects *project.Manager,
	st *store.Store,
	sendFn func(string) error,
	sendApprovalFn func(approvalID, text string) error,
	sendTypingFn func(ctx context.Context),
	logger *slog.Logger,
) *Worker {
	w := &Worker{
		queue:           make(chan store.InboxMessage, queueSize),
		projects:        projects,
		store:           st,
		sendFn:          sendFn,
		sendApprovalFn:  sendApprovalFn,
		sendTypingFn:    sendTypingFn,
		logger:          logger,
		approvalTimeout: 2 * time.Minute,
		messageTTL:      messageTTL,
		maxRetryCount:   maxRetry,
	}
	w.bgPool = NewBackgroundPool(projects, sendFn, sendApprovalFn, sendTypingFn, logger)
	return w
}

func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	defer w.wg.Done()

	w.logger.Info("Worker started", "queue_capacity", cap(w.queue), "ttl", w.messageTTL.String())

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Worker stopping")
			return
		case msg := <-w.queue:
			w.process(ctx, msg)
		}
	}
}

func (w *Worker) Enqueue(msg store.InboxMessage) bool {
	if _, loaded := w.inFlight.LoadOrStore(msg.ID, struct{}{}); loaded {
		return true
	}

	select {
	case w.queue <- msg:
		queueLen := len(w.queue)
		w.logger.Info("Enqueued", "id", msg.ID, "queue_len", queueLen)
		if queueLen > 0 {
			w.sendFn(fmt.Sprintf("📋 Queued (#%d). Processing another request...", queueLen))
		}
		return true
	default:
		w.inFlight.Delete(msg.ID)
		w.logger.Error("Queue full", "id", msg.ID)
		return false
	}
}

func (w *Worker) process(ctx context.Context, msg store.InboxMessage) {
	defer w.inFlight.Delete(msg.ID)

	// TTL check: skip expired messages
	if w.messageTTL > 0 && msg.Age() > w.messageTTL {
		w.logger.Warn("Message expired (TTL)", "id", msg.ID, "age", msg.Age().String())
		w.updateInboxStatus(msg.ID, store.StatusExpired, "", "expired: message too old")
		return
	}

	// Set up cancellable context
	ctx, cancel := context.WithCancel(ctx)
	w.cancelMu.Lock()
	w.currentCancel = cancel
	w.currentMsgID = msg.ID
	w.cancelled = false
	w.cancelMu.Unlock()
	defer func() {
		cancel()
		w.cancelMu.Lock()
		w.currentCancel = nil
		w.currentMsgID = ""
		w.cancelled = false
		w.cancelMu.Unlock()
	}()

	w.logger.Info("Processing started", "id", msg.ID)
	w.updateInboxStatus(msg.ID, store.StatusProcessing, "", "")

	// Start typing indicator
	typingCtx, stopTyping := context.WithCancel(ctx)
	go w.sendTypingFn(typingCtx)
	defer stopTyping()

	// Phase 1: Execute with default permissions
	result, err := w.projects.Execute(ctx, msg.Text, false)
	if err != nil {
		w.handleError(msg.ID, msg.Text, err)
		return
	}

	// Phase 2: Permission approval if needed
	if len(result.PermissionDenials) > 0 {
		w.logger.Info("Permission denials detected", "id", msg.ID, "denials", len(result.PermissionDenials))

		approved, err := w.requestApproval(ctx, msg.ID, result)
		if err != nil {
			w.logger.Error("Approval request failed", "id", msg.ID, "error", err)
			w.sendAndRecord(msg.ID, result.Result)
			return
		}

		if !approved {
			w.logger.Info("User denied permission", "id", msg.ID)
			w.sendAndRecord(msg.ID, result.Result)
			return
		}

		w.logger.Info("User approved, re-executing", "id", msg.ID)
		result, err = w.projects.Execute(ctx, msg.Text, true)
		if err != nil {
			w.handleError(msg.ID, msg.Text, err)
			return
		}
	}

	w.projects.TrackUsage(result)
	response := result.Result
	if summary := buildToolSummary(result); summary != "" {
		response = summary + "\n\n" + response
	}
	w.sendAndRecord(msg.ID, response)
}

// GetUsage returns current usage stats from the active project.
func (w *Worker) GetUsage() *project.ProjectUsage {
	return w.projects.GetUsage()
}

// ResetSessionUsage resets the per-session cost counter for the active project.
func (w *Worker) ResetSessionUsage() {
	w.projects.ResetSessionUsage()
}

func (w *Worker) handleError(msgID, msgText string, err error) {
	errStr := err.Error()
	w.logger.Error("Processing failed", "id", msgID, "error", errStr)

	// Check if this was a user-initiated /cancel
	w.cancelMu.Lock()
	wasCancelled := w.cancelled
	w.cancelMu.Unlock()

	if wasCancelled {
		w.logger.Info("Message cancelled by user", "id", msgID)
		w.updateInboxStatus(msgID, store.StatusFailed, "", "cancelled by user")
		return
	}

	// Classify error: restart kills don't count toward retry limit
	if isRestartError(errStr) {
		w.logger.Info("Restart-related error, will retry once without counting", "id", msgID)
		w.updateInboxStatus(msgID, store.StatusPending, "", errStr)
		return
	}

	w.updateInboxStatus(msgID, store.StatusError, "", errStr)

	w.sendFn(fmt.Sprintf(
		"⚠️ Processing Failed\n\nMessage: %s\nError: %s\n\nWill auto-retry. Or use /retry to force.",
		Truncate(msgText, 80),
		Truncate(errStr, 100)))
}

// isRestartError detects errors caused by bot restart killing the CLI process.
func isRestartError(errStr string) bool {
	return strings.Contains(errStr, "signal: killed") ||
		strings.Contains(errStr, "signal: terminated")
}

func (w *Worker) sendAndRecord(msgID, result string) {
	if result == "" {
		result = "(no response)"
	}

	if err := w.sendFn(result); err != nil {
		w.logger.Error("Telegram send failed, falling back to outbox", "id", msgID, "error", err)
		w.appendOutbox(msgID, result, store.StatusDone)
		w.updateInboxStatus(msgID, store.StatusDone, result, "")
		return
	}

	w.appendOutbox(msgID, result, store.StatusSent)
	w.updateInboxStatus(msgID, store.StatusSent, result, "")
	w.logger.Info("Processing completed", "id", msgID)
}

// CancelCurrent cancels the currently processing message.
func (w *Worker) CancelCurrent() (string, bool) {
	w.cancelMu.Lock()
	defer w.cancelMu.Unlock()
	if w.currentCancel != nil {
		w.cancelled = true
		w.currentCancel()
		return w.currentMsgID, true
	}
	return "", false
}

// PollPending periodically checks inbox for pending messages and enqueues them.
func (w *Worker) PollPending(ctx context.Context, interval time.Duration) {
	w.wg.Add(1)
	defer w.wg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	w.logger.Info("Pending poller started", "interval", interval.String())

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.enqueuePending()
		}
	}
}

func (w *Worker) enqueuePending() {
	inbox, err := w.store.ReadInbox()
	if err != nil {
		return
	}
	for _, msg := range inbox.Messages {
		if msg.Status == store.StatusPending {
			w.Enqueue(msg)
		}
	}
}

// RecoverStale resets interrupted messages on startup with TTL awareness.
func (w *Worker) RecoverStale() int {
	recovered := 0
	w.store.UpdateInbox(func(mf *store.InboxFile) bool {
		changed := false
		for i := range mf.Messages {
			m := &mf.Messages[i]

			switch m.Status {
			case store.StatusProcessing:
				if w.messageTTL > 0 && m.Age() > w.messageTTL {
					m.Status = store.StatusExpired
					w.logger.Warn("Expired stale message", "id", m.ID, "age", m.Age().String())
				} else {
					m.Status = store.StatusPending
					w.logger.Warn("Recovered stale message", "id", m.ID)
					recovered++
				}
				changed = true

			case store.StatusError:
				if m.RetryCount >= w.maxRetryCount {
					m.Status = store.StatusFailed
					w.logger.Info("Marked as permanently failed", "id", m.ID)
					changed = true
				} else if w.messageTTL > 0 && m.Age() > w.messageTTL {
					m.Status = store.StatusExpired
					w.logger.Warn("Expired error message", "id", m.ID)
					changed = true
				}
			}
		}
		return changed
	})
	return recovered
}

// ProcessRetries checks for error messages and retries them.
func (w *Worker) ProcessRetries(ctx context.Context, interval time.Duration) {
	w.wg.Add(1)
	defer w.wg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	w.logger.Info("Retry processor started", "max_retries", w.maxRetryCount)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processRetries()
		}
	}
}

func (w *Worker) processRetries() {
	w.store.UpdateInbox(func(mf *store.InboxFile) bool {
		changed := false
		for i := range mf.Messages {
			m := &mf.Messages[i]
			if m.Status != store.StatusError {
				continue
			}

			// TTL check
			if w.messageTTL > 0 && m.Age() > w.messageTTL {
				m.Status = store.StatusExpired
				w.logger.Warn("Expired during retry check", "id", m.ID)
				changed = true
				continue
			}

			// Max retry check
			if m.RetryCount >= w.maxRetryCount {
				m.Status = store.StatusFailed
				w.sendFn(fmt.Sprintf(
					"❌ Message permanently failed after %d retries\nID: %s\nText: %s",
					m.RetryCount, m.ID, Truncate(m.Text, 100)))
				changed = true
				continue
			}

			m.Status = store.StatusPending
			m.RetryCount++
			w.logger.Info("Retrying message", "id", m.ID, "retry", m.RetryCount)
			changed = true
		}
		return changed
	})
}

func (w *Worker) Stop() {
	w.bgPool.CancelAll()
	w.bgPool.Wait()
	w.wg.Wait()
}

// --- Background task delegation ---

// EnqueueBackground submits a background task for the given project.
func (w *Worker) EnqueueBackground(ctx context.Context, projectName, message string) (string, error) {
	return w.bgPool.Submit(ctx, projectName, message)
}

// BackgroundStatus returns formatted status of background tasks.
func (w *Worker) BackgroundStatus() string {
	return w.bgPool.Status()
}

// BackgroundRunningCount returns the number of active background tasks.
func (w *Worker) BackgroundRunningCount() int {
	return w.bgPool.RunningCount()
}

// CancelBackground cancels a specific background task.
func (w *Worker) CancelBackground(taskID string) error {
	return w.bgPool.Cancel(taskID)
}

// GetBackgroundResult returns the result text and project for a completed bg task.
func (w *Worker) GetBackgroundResult(taskID string) (string, string, error) {
	return w.bgPool.GetResult(taskID)
}

// --- Ralph delegation ---

// EnqueueRalph starts an iterative autonomous loop as a background task.
func (w *Worker) EnqueueRalph(ctx context.Context, projectName, message string, maxIter int) (string, error) {
	return w.bgPool.SubmitRalph(ctx, projectName, message, maxIter)
}

// RalphStatus returns formatted status of running ralph loops.
func (w *Worker) RalphStatus() string {
	return w.bgPool.RalphStatus()
}

// --- Plan delegation ---

// CreatePlan creates a plan using read-only tools.
func (w *Worker) CreatePlan(ctx context.Context, projectName, message string) (string, error) {
	return w.bgPool.SubmitPlan(ctx, projectName, message)
}

// ExecutePlan runs the previously created plan.
func (w *Worker) ExecutePlan(ctx context.Context, projectName string) (string, error) {
	return w.bgPool.ExecutePlan(ctx, projectName)
}

// CancelPlan discards the active plan.
func (w *Worker) CancelPlan(projectName string) bool {
	return w.bgPool.CancelPlan(projectName)
}

// GetPlan returns the active plan for a project.
func (w *Worker) GetPlan(projectName string) *PlanState {
	return w.bgPool.GetPlan(projectName)
}

// ResolveBackgroundApproval resolves a pending background task approval.
func (w *Worker) ResolveBackgroundApproval(id string, approved bool) {
	w.bgPool.ResolveApproval(id, approved)
}

// CleanupBackground periodically removes old completed background tasks.
func (w *Worker) CleanupBackground(ctx context.Context, interval time.Duration) {
	w.wg.Add(1)
	defer w.wg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.bgPool.Cleanup(30 * time.Minute)
		}
	}
}

// HasProject checks if a project exists (for /bg argument parsing).
func (w *Worker) HasProject(name string) bool {
	return w.projects.HasProject(name)
}

// --- Delegated methods ---

func (w *Worker) ResetSession()                     { w.projects.ResetSession() }
func (w *Worker) SetModel(model string)             { w.projects.SetModel(model) }
func (w *Worker) GetModel() string                  { return w.projects.GetModel() }
func (w *Worker) GetSessions() []claude.SessionInfo { return w.projects.GetSessions() }
func (w *Worker) ResumeSession(id string)           { w.projects.SetResumeID(id) }
func (w *Worker) SetSessionName(name string)        { w.projects.SetSessionName(name) }
func (w *Worker) GetCurrentSessionID() string       { return w.projects.GetCurrentSessionID() }

// --- Project delegation ---

func (w *Worker) ActiveProject() string                 { return w.projects.ActiveProject() }
func (w *Worker) SwitchProject(name string) error       { return w.projects.SwitchProject(name) }
func (w *Worker) AddProject(name, workDir string) error { return w.projects.AddProject(name, workDir) }
func (w *Worker) RemoveProject(name string) error       { return w.projects.RemoveProject(name) }
func (w *Worker) RenameProject(oldName, newName string) error {
	return w.projects.RenameProject(oldName, newName)
}
func (w *Worker) ListProjects() (string, map[string]project.ProjectConfig) {
	return w.projects.ListProjects()
}
func (w *Worker) GetProjectInfo() (project.ProjectConfig, *project.ProjectUsage, int) {
	return w.projects.GetProjectInfo()
}

// --- helpers ---

var toolIcons = map[string]string{
	"Bash": "⚡", "Read": "📖", "Edit": "✏️", "Write": "📄",
	"Glob": "🔍", "Grep": "🔎", "WebFetch": "🌐", "WebSearch": "🔍",
	"Agent": "🤖", "Skill": "🎯",
}

func buildToolSummary(result *store.CLIResult) string {
	if len(result.ToolSummary) == 0 {
		return ""
	}

	// Sort tool names for stable output
	names := make([]string, 0, len(result.ToolSummary))
	for name := range result.ToolSummary {
		names = append(names, name)
	}
	sort.Strings(names)

	var parts []string
	for _, name := range names {
		count := result.ToolSummary[name]
		icon := toolIcons[name]
		if icon == "" {
			if strings.HasPrefix(name, "mcp__") {
				icon = "🔌"
			} else {
				icon = "🔧"
			}
		}
		if count > 1 {
			parts = append(parts, fmt.Sprintf("%s%s ×%d", icon, name, count))
		} else {
			parts = append(parts, fmt.Sprintf("%s%s", icon, name))
		}
	}

	dur := fmt.Sprintf("%.0fs", float64(result.DurationMs)/1000)
	cost := fmt.Sprintf("$%.4f", result.TotalCostUSD)

	return fmt.Sprintf("📋 %s | %s | %s", strings.Join(parts, "  "), dur, cost)
}

func (w *Worker) updateInboxStatus(id, status, result, lastError string) {
	w.store.UpdateInbox(func(mf *store.InboxFile) bool {
		for i := range mf.Messages {
			if mf.Messages[i].ID == id {
				mf.Messages[i].Status = status
				if result != "" {
					mf.Messages[i].Result = result
				}
				if lastError != "" {
					mf.Messages[i].LastError = lastError
				}
				return true
			}
		}
		return false
	})
}

func (w *Worker) appendOutbox(id, result, status string) {
	w.store.UpdateOutbox(func(mf *store.OutboxFile) bool {
		mf.Messages = append(mf.Messages, store.OutboxMessage{
			ID:        id,
			Status:    status,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Result:    result,
		})
		return true
	})
}
