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

	claude        *ClaudeExecutor
	store         *Store
	sendFn        func(string) error
	sendApprovalFn func(approvalID, text string) error
	logger        *slog.Logger
	wg            sync.WaitGroup

	approvalTimeout time.Duration
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

// Start runs the main processing loop.
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

// Enqueue adds a message to the processing queue.
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

	w.logger.Info("Processing started", "id", msg.ID)
	w.updateInboxStatus(msg.ID, StatusProcessing, "", "")

	// Phase 1: Execute with default permissions
	result, err := w.claude.Execute(ctx, msg.Text, false)
	if err != nil {
		w.logger.Error("Claude failed", "id", msg.ID, "error", err)
		w.updateInboxStatus(msg.ID, StatusError, "", err.Error())
		return
	}

	// Check for permission denials
	if len(result.PermissionDenials) > 0 {
		w.logger.Info("Permission denials detected",
			"id", msg.ID,
			"denials", len(result.PermissionDenials))

		approved, err := w.requestApproval(ctx, msg.ID, result.PermissionDenials)
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

		// Phase 2: Re-execute with permissions skipped (user approved)
		w.logger.Info("User approved, re-executing with permissions", "id", msg.ID)
		result, err = w.claude.Execute(ctx, msg.Text, true)
		if err != nil {
			w.logger.Error("Claude re-execution failed", "id", msg.ID, "error", err)
			w.updateInboxStatus(msg.ID, StatusError, "", err.Error())
			return
		}
	}

	w.sendAndRecord(msg.ID, result.Result)
}

func (w *Worker) sendAndRecord(msgID, result string) {
	if result == "" {
		result = "(no response)"
	}

	// Send directly to telegram
	if err := w.sendFn(result); err != nil {
		w.logger.Error("Telegram send failed, falling back to outbox",
			"id", msgID, "error", err)
		w.appendOutbox(msgID, result, StatusDone)
		w.updateInboxStatus(msgID, StatusDone, result, "")
		return
	}

	// Success
	w.appendOutbox(msgID, result, StatusSent)
	w.updateInboxStatus(msgID, StatusSent, result, "")
	w.logger.Info("Processing completed", "id", msgID)
}

// requestApproval sends an approval request to Telegram and waits for response.
func (w *Worker) requestApproval(ctx context.Context, msgID string, denials []PermissionDenial) (bool, error) {
	ch := make(chan bool, 1)
	w.approvals.Store(msgID, ch)
	defer w.approvals.Delete(msgID)

	// Build tool list
	var tools []string
	seen := make(map[string]bool)
	for _, d := range denials {
		if !seen[d.ToolName] {
			tools = append(tools, d.ToolName)
			seen[d.ToolName] = true
		}
	}

	var toolLines []string
	for _, t := range tools {
		toolLines = append(toolLines, "  "+formatToolName(t))
	}

	text := fmt.Sprintf(
		"🔐 *Permission Required*\n\n"+
			"Claude needs the following tools:\n\n%s\n\n"+
			"Allow execution?  _(expires in 2 min)_",
		strings.Join(toolLines, "\n"))

	if err := w.sendApprovalFn(msgID, text); err != nil {
		return false, fmt.Errorf("send approval request: %w", err)
	}

	w.logger.Info("Waiting for approval", "id", msgID, "tools", tools)

	select {
	case approved := <-ch:
		return approved, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(w.approvalTimeout):
		w.sendFn("[Timeout] Permission request expired.")
		return false, fmt.Errorf("approval timeout after %s", w.approvalTimeout)
	}
}

// ResolveApproval resolves a pending approval request.
func (w *Worker) ResolveApproval(id string, approved bool) {
	if val, ok := w.approvals.Load(id); ok {
		ch := val.(chan bool)
		select {
		case ch <- approved:
		default:
		}
	}
}

// ResetSession resets the Claude CLI session for a fresh conversation.
func (w *Worker) ResetSession() {
	w.claude.ResetSession()
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

// RecoverStale resets "processing" messages to "pending" on startup.
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
	// MCP tools: mcp__claude_ai_Slack__slack_send_message → 💬 Slack → Send Message
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

	// Built-in tools
	icons := map[string]string{
		"Bash": "⚡ Terminal Command", "Write": "📄 File Write",
		"Edit": "✏️ File Edit", "Read": "📖 File Read",
		"WebFetch": "🌐 Web Fetch", "WebSearch": "🔍 Web Search",
		"NotebookEdit": "📓 Notebook Edit",
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
