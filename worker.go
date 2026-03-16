package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type Worker struct {
	queue     chan InboxMessage
	inFlight  sync.Map
	approvals sync.Map // map[string]chan bool

	claude         *ClaudeExecutor
	store          *Store
	sendFn         func(string) error
	sendApprovalFn func(approvalID, text string) error
	logger         *slog.Logger
	wg             sync.WaitGroup

	approvalTimeout time.Duration

	cancelMu      sync.Mutex
	currentCancel context.CancelFunc
	currentMsgID  string
}

func NewWorker(
	queueSize int,
	claude *ClaudeExecutor,
	store *Store,
	sendFn func(string) error,
	sendApprovalFn func(approvalID, text string) error,
	logger *slog.Logger,
) *Worker {
	return &Worker{
		queue:           make(chan InboxMessage, queueSize),
		claude:          claude,
		store:           store,
		sendFn:          sendFn,
		sendApprovalFn:  sendApprovalFn,
		logger:          logger,
		approvalTimeout: 2 * time.Minute,
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	defer w.wg.Done()

	w.logger.Info("Worker started", "queue_capacity", cap(w.queue))

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

func (w *Worker) Enqueue(msg InboxMessage) bool {
	if _, loaded := w.inFlight.LoadOrStore(msg.ID, struct{}{}); loaded {
		w.logger.Debug("Already in-flight, skipping", "id", msg.ID)
		return true
	}

	select {
	case w.queue <- msg:
		w.logger.Info("Enqueued", "id", msg.ID, "queue_len", len(w.queue))
		return true
	default:
		w.inFlight.Delete(msg.ID)
		w.logger.Error("Queue full", "id", msg.ID)
		return false
	}
}

func (w *Worker) process(ctx context.Context, msg InboxMessage) {
	defer w.inFlight.Delete(msg.ID)

	// Set up cancellable context
	ctx, cancel := context.WithCancel(ctx)
	w.cancelMu.Lock()
	w.currentCancel = cancel
	w.currentMsgID = msg.ID
	w.cancelMu.Unlock()
	defer func() {
		cancel()
		w.cancelMu.Lock()
		w.currentCancel = nil
		w.currentMsgID = ""
		w.cancelMu.Unlock()
	}()

	w.logger.Info("Processing started", "id", msg.ID)
	w.updateInboxStatus(msg.ID, StatusProcessing, "", "")

	// Phase 1: Execute with default permissions
	result, err := w.claude.Execute(ctx, msg.Text, false)
	if err != nil {
		w.handleError(msg.ID, msg.Text, err)
		return
	}

	// Check for permission denials
	if len(result.PermissionDenials) > 0 {
		w.logger.Info("Permission denials detected",
			"id", msg.ID,
			"denials", len(result.PermissionDenials))

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

		// Phase 2: Re-execute with permissions skipped
		w.logger.Info("User approved, re-executing", "id", msg.ID)
		result, err = w.claude.Execute(ctx, msg.Text, true)
		if err != nil {
			w.handleError(msg.ID, msg.Text, err)
			return
		}
	}

	w.sendAndRecord(msg.ID, result.Result)
}

func (w *Worker) handleError(msgID, msgText string, err error) {
	w.logger.Error("Processing failed", "id", msgID, "error", err)
	w.updateInboxStatus(msgID, StatusError, "", err.Error())

	// Notify user on Telegram
	errMsg := fmt.Sprintf(
		"⚠️ *Processing Failed*\n\n"+
			"Message: _%s_\n"+
			"Error: `%s`\n\n"+
			"Will auto-retry. Or use /retry to force.",
		truncate(msgText, 80),
		truncate(err.Error(), 100))
	w.sendFn(errMsg)
}

func (w *Worker) sendAndRecord(msgID, result string) {
	if result == "" {
		result = "(no response)"
	}

	if err := w.sendFn(result); err != nil {
		w.logger.Error("Telegram send failed, falling back to outbox",
			"id", msgID, "error", err)
		w.appendOutbox(msgID, result, StatusDone)
		w.updateInboxStatus(msgID, StatusDone, result, "")
		return
	}

	w.appendOutbox(msgID, result, StatusSent)
	w.updateInboxStatus(msgID, StatusSent, result, "")
	w.logger.Info("Processing completed", "id", msgID)
}

// requestApproval sends a detailed approval request to Telegram.
func (w *Worker) requestApproval(ctx context.Context, msgID string, result *CLIResult) (bool, error) {
	ch := make(chan bool, 1)
	w.approvals.Store(msgID, ch)
	defer w.approvals.Delete(msgID)

	// Build detailed permission message
	text := w.buildPermissionMessage(result)

	if err := w.sendApprovalFn(msgID, text); err != nil {
		return false, fmt.Errorf("send approval request: %w", err)
	}

	w.logger.Info("Waiting for approval", "id", msgID)

	select {
	case approved := <-ch:
		return approved, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(w.approvalTimeout):
		w.sendFn("⏰ Permission request timed out (2 min).")
		return false, fmt.Errorf("approval timeout")
	}
}

func (w *Worker) buildPermissionMessage(result *CLIResult) string {
	var sb strings.Builder
	sb.WriteString("🔐 *Permission Required*\n\n")

	// Group denials by tool, show details
	type toolDetail struct {
		icon    string
		details []string
	}
	tools := make(map[string]*toolDetail)
	order := []string{}

	for _, d := range result.PermissionDenials {
		name := formatToolName(d.ToolName)
		if _, exists := tools[name]; !exists {
			tools[name] = &toolDetail{icon: name}
			order = append(order, name)
		}
		detail := extractToolDetail(d)
		if detail != "" {
			tools[name].details = append(tools[name].details, detail)
		}
	}

	for _, name := range order {
		td := tools[name]
		sb.WriteString(fmt.Sprintf("• %s\n", name))
		// Show unique details (max 3)
		seen := make(map[string]bool)
		count := 0
		for _, d := range td.details {
			if !seen[d] && count < 3 {
				sb.WriteString(fmt.Sprintf("    `%s`\n", d))
				seen[d] = true
				count++
			}
		}
	}

	// Show Claude's explanation (truncated)
	if result.Result != "" {
		sb.WriteString(fmt.Sprintf("\n💬 _Claude says:_ %s\n", truncate(result.Result, 150)))
	}

	sb.WriteString("\n_Expires in 2 min_")
	return sb.String()
}

func extractToolDetail(d PermissionDenial) string {
	if d.ToolInput == nil {
		return ""
	}

	switch d.ToolName {
	case "Bash":
		if cmd, ok := d.ToolInput["command"].(string); ok {
			return truncate(cmd, 60)
		}
	case "Write":
		if fp, ok := d.ToolInput["file_path"].(string); ok {
			return "write → " + fp
		}
	case "Edit":
		if fp, ok := d.ToolInput["file_path"].(string); ok {
			return "edit → " + fp
		}
	default:
		// MCP tools: try to show key params
		var parts []string
		for k, v := range d.ToolInput {
			if s, ok := v.(string); ok && s != "" {
				parts = append(parts, fmt.Sprintf("%s=%s", k, truncate(s, 30)))
				if len(parts) >= 2 {
					break
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, ", ")
		}
	}
	return ""
}

func (w *Worker) ResolveApproval(id string, approved bool) {
	if val, ok := w.approvals.Load(id); ok {
		ch := val.(chan bool)
		select {
		case ch <- approved:
		default:
		}
	}
}

// CancelCurrent cancels the currently processing message.
func (w *Worker) CancelCurrent() (string, bool) {
	w.cancelMu.Lock()
	defer w.cancelMu.Unlock()
	if w.currentCancel != nil {
		w.currentCancel()
		msgID := w.currentMsgID
		return msgID, true
	}
	return "", false
}

func (w *Worker) ResetSession() {
	w.claude.ResetSession()
}

func (w *Worker) SetModel(model string) {
	w.claude.SetModel(model)
}

func (w *Worker) GetModel() string {
	return w.claude.GetModel()
}

func (w *Worker) GetSessions() []SessionInfo {
	return w.claude.GetSessions()
}

func (w *Worker) ResumeSession(id string) {
	w.claude.SetResumeID(id)
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
			w.logger.Info("Pending poller stopping")
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
		if msg.Status == StatusPending {
			w.Enqueue(msg)
		}
	}
}

func (w *Worker) RecoverStale() int {
	recovered := 0
	w.store.UpdateInbox(func(mf *InboxFile) bool {
		changed := false
		for i := range mf.Messages {
			if mf.Messages[i].Status == StatusProcessing {
				mf.Messages[i].Status = StatusPending
				w.logger.Warn("Recovered stale message", "id", mf.Messages[i].ID)
				changed = true
				recovered++
			}
		}
		return changed
	})
	return recovered
}

func (w *Worker) Stop() {
	w.wg.Wait()
}

// --- helpers ---

func formatToolName(raw string) string {
	if strings.HasPrefix(raw, "mcp__") {
		parts := strings.Split(raw, "__")
		if len(parts) >= 3 {
			service := parts[2]
			action := parts[len(parts)-1]
			action = strings.ReplaceAll(action, "_", " ")
			words := strings.Fields(action)
			for i, w := range words {
				if len(w) > 0 {
					words[i] = strings.ToUpper(w[:1]) + w[1:]
				}
			}
			action = strings.Join(words, " ")

			icons := map[string]string{
				"Slack": "💬", "Notion": "📝", "Gmail": "📧",
				"pencil": "🎨",
			}
			icon := "🔌"
			if i, ok := icons[service]; ok {
				icon = i
			}
			return fmt.Sprintf("%s %s → %s", icon, service, action)
		}
	}

	icons := map[string]string{
		"Bash": "⚡ Terminal Command", "Write": "📄 File Write",
		"Edit": "✏️ File Edit", "Read": "📖 File Read",
		"Glob": "🔍 File Search", "Grep": "🔎 Content Search",
		"WebFetch": "🌐 Web Fetch", "WebSearch": "🔍 Web Search",
	}
	if name, ok := icons[raw]; ok {
		return name
	}
	return "🔧 " + raw
}

func (w *Worker) updateInboxStatus(id, status, result, lastError string) {
	w.store.UpdateInbox(func(mf *InboxFile) bool {
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
	w.store.UpdateOutbox(func(mf *OutboxFile) bool {
		mf.Messages = append(mf.Messages, OutboxMessage{
			ID:        id,
			Status:    status,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Result:    result,
		})
		return true
	})
}
