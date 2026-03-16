package main

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type Worker struct {
	queue    chan InboxMessage
	inFlight sync.Map
	claude   *ClaudeExecutor
	store    *Store
	sendFn   func(string) error
	logger   *slog.Logger
	wg       sync.WaitGroup
}

func NewWorker(queueSize int, claude *ClaudeExecutor, store *Store, sendFn func(string) error, logger *slog.Logger) *Worker {
	return &Worker{
		queue:  make(chan InboxMessage, queueSize),
		claude: claude,
		store:  store,
		sendFn: sendFn,
		logger: logger,
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

// Enqueue adds a message to the processing queue. Returns false if queue is full.
func (w *Worker) Enqueue(msg InboxMessage) bool {
	// Dedup: skip if already queued or processing
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

	// inbox → processing
	w.updateInboxStatus(msg.ID, StatusProcessing, "", "")

	// Execute Claude CLI
	result, err := w.claude.Execute(ctx, msg.Text)
	if err != nil {
		w.logger.Error("Claude failed", "id", msg.ID, "error", err)
		w.updateInboxStatus(msg.ID, StatusError, "", err.Error())
		return
	}

	// Send directly to telegram
	if err := w.sendFn(result); err != nil {
		w.logger.Error("Telegram send failed, falling back to outbox",
			"id", msg.ID, "error", err)
		// Fallback: outbox poller will retry
		w.appendOutbox(msg.ID, result, StatusDone)
		w.updateInboxStatus(msg.ID, StatusDone, result, "")
		return
	}

	// Success: record everywhere
	w.appendOutbox(msg.ID, result, StatusSent)
	w.updateInboxStatus(msg.ID, StatusSent, result, "")

	w.logger.Info("Processing completed", "id", msg.ID)
}

// PollPending periodically checks inbox for pending messages and enqueues them.
// This catches retried/recovered messages that aren't from new telegram input.
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
