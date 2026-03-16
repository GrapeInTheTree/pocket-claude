package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	api    *tgbotapi.BotAPI
	cfg    Config
	store  *Store
	worker *Worker
	logger *slog.Logger
	wg     sync.WaitGroup
}

func (b *Bot) SetWorker(w *Worker) {
	b.worker = w
}

func NewBot(cfg Config, store *Store, logger *slog.Logger) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		return nil, fmt.Errorf("create bot API: %w", err)
	}

	logger.Info("Telegram bot authorized", "username", api.Self.UserName)

	return &Bot{
		api:    api,
		cfg:    cfg,
		store:  store,
		logger: logger,
	}, nil
}

// --- Message listener ---

func (b *Bot) Listen(ctx context.Context) {
	b.wg.Add(1)
	defer b.wg.Done()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			b.logger.Info("Stopping message listener")
			return
		case update, ok := <-updates:
			if !ok {
				b.logger.Info("Updates channel closed")
				return
			}
			if update.Message == nil {
				continue
			}
			if update.Message.Chat.ID != b.cfg.TelegramChatID {
				b.logger.Debug("Ignored message from unauthorized chat", "chat_id", update.Message.Chat.ID)
				continue
			}

			if update.Message.IsCommand() {
				go b.handleCommand(update.Message)
			} else {
				go b.handleMessage(update.Message)
			}
		}
	}
}

func (b *Bot) handleMessage(msg *tgbotapi.Message) {
	inboxMsg := InboxMessage{
		ID:                fmt.Sprintf("msg_%d", time.Now().UnixMilli()),
		Text:              msg.Text,
		Status:            StatusPending,
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		RetryCount:        0,
		TelegramMessageID: msg.MessageID,
	}

	if err := b.store.AppendToInbox(inboxMsg); err != nil {
		b.logger.Error("Failed to save message", "id", inboxMsg.ID, "error", err)
		return
	}
	b.logger.Info("Message saved to inbox", "id", inboxMsg.ID, "text", truncate(inboxMsg.Text, 50))

	// Trigger Claude CLI processing immediately
	if b.worker != nil {
		if !b.worker.Enqueue(inboxMsg) {
			b.logger.Error("Failed to enqueue message", "id", inboxMsg.ID)
		}
	}
}

// --- Commands ---

func (b *Bot) handleCommand(msg *tgbotapi.Message) {
	switch msg.Command() {
	case "status":
		b.cmdStatus()
	case "clear":
		b.cmdClear()
	case "retry":
		b.cmdRetry()
	default:
		b.sendMessage(fmt.Sprintf("Unknown command: /%s", msg.Command()))
	}
}

func (b *Bot) cmdStatus() {
	stats, lastMsg, err := b.store.GetInboxStats()
	if err != nil {
		b.sendMessage("Failed to read inbox: " + err.Error())
		return
	}

	var sb strings.Builder
	sb.WriteString("[Bot Status]\n\n")
	sb.WriteString(fmt.Sprintf("  pending    : %d\n", stats[StatusPending]))
	sb.WriteString(fmt.Sprintf("  processing : %d\n", stats[StatusProcessing]))
	sb.WriteString(fmt.Sprintf("  done       : %d\n", stats[StatusDone]))
	sb.WriteString(fmt.Sprintf("  sent       : %d\n", stats[StatusSent]))
	sb.WriteString(fmt.Sprintf("  error      : %d\n", stats[StatusError]))

	total := 0
	for _, v := range stats {
		total += v
	}
	sb.WriteString(fmt.Sprintf("\nTotal: %d", total))

	if !lastMsg.IsZero() {
		sb.WriteString(fmt.Sprintf("\nLast message: %s", lastMsg.Format("2006-01-02 15:04:05 UTC")))
	}

	b.sendMessage(sb.String())
}

func (b *Bot) cmdClear() {
	removed, err := b.store.ClearCompleted()
	if err != nil {
		b.sendMessage("Failed to clear: " + err.Error())
		return
	}
	b.sendMessage(fmt.Sprintf("Cleared %d completed messages.", removed))
}

func (b *Bot) cmdRetry() {
	var retried int
	err := b.store.UpdateInbox(func(mf *InboxFile) bool {
		changed := false
		for i := range mf.Messages {
			if mf.Messages[i].Status == StatusError {
				mf.Messages[i].Status = StatusPending
				mf.Messages[i].RetryCount = 0
				mf.Messages[i].LastError = ""
				changed = true
				retried++
			}
		}
		return changed
	})
	if err != nil {
		b.sendMessage("Failed to retry: " + err.Error())
		return
	}
	if retried == 0 {
		b.sendMessage("No error messages to retry.")
	} else {
		b.sendMessage(fmt.Sprintf("Force-retrying %d messages (retry count reset).", retried))
	}
}

// --- Workers ---

func (b *Bot) PollOutbox(ctx context.Context) {
	b.wg.Add(1)
	defer b.wg.Done()

	interval := time.Duration(b.cfg.OutboxPollIntervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	b.logger.Info("Outbox poller started", "interval", interval.String())

	for {
		select {
		case <-ctx.Done():
			b.logger.Info("Stopping outbox poller")
			return
		case <-ticker.C:
			if err := b.processOutbox(); err != nil {
				b.logger.Error("Outbox processing error", "error", err)
			}
		}
	}
}

func (b *Bot) processOutbox() error {
	return b.store.UpdateOutbox(func(mf *OutboxFile) bool {
		changed := false
		for i := range mf.Messages {
			if mf.Messages[i].Status != StatusDone {
				continue
			}

			text := mf.Messages[i].Result
			if text == "" {
				b.logger.Debug("Skipping outbox message with empty result", "id", mf.Messages[i].ID)
				continue
			}

			if err := b.sendMessage(text); err != nil {
				b.logger.Error("Failed to send outbox message", "id", mf.Messages[i].ID, "error", err)
				continue
			}

			mf.Messages[i].Status = StatusSent
			changed = true
			b.logger.Info("Outbox message sent", "id", mf.Messages[i].ID)
		}
		return changed
	})
}

func (b *Bot) ProcessRetries(ctx context.Context) {
	b.wg.Add(1)
	defer b.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	b.logger.Info("Retry processor started", "max_retries", b.cfg.MaxRetryCount)

	for {
		select {
		case <-ctx.Done():
			b.logger.Info("Stopping retry processor")
			return
		case <-ticker.C:
			b.processRetries()
		}
	}
}

func (b *Bot) processRetries() {
	err := b.store.UpdateInbox(func(mf *InboxFile) bool {
		changed := false
		for i := range mf.Messages {
			if mf.Messages[i].Status != StatusError {
				continue
			}

			if mf.Messages[i].RetryCount >= b.cfg.MaxRetryCount {
				if !strings.HasPrefix(mf.Messages[i].LastError, "[MAX_RETRY]") {
					b.sendMessage(fmt.Sprintf(
						"[Error] Message failed after %d retries\nID: %s\nText: %s\nError: %s",
						mf.Messages[i].RetryCount,
						mf.Messages[i].ID,
						truncate(mf.Messages[i].Text, 100),
						mf.Messages[i].LastError,
					))
					mf.Messages[i].LastError = "[MAX_RETRY] " + mf.Messages[i].LastError
					changed = true
				}
				continue
			}

			mf.Messages[i].Status = StatusPending
			mf.Messages[i].RetryCount++
			b.logger.Info("Retrying message", "id", mf.Messages[i].ID, "retry", mf.Messages[i].RetryCount)
			changed = true
		}
		return changed
	})
	if err != nil {
		b.logger.Error("Retry processing error", "error", err)
	}
}

// --- Helpers ---

func (b *Bot) sendMessage(text string) error {
	msg := tgbotapi.NewMessage(b.cfg.TelegramChatID, text)
	_, err := b.api.Send(msg)
	return err
}

func (b *Bot) Shutdown() {
	b.api.StopReceivingUpdates()
	b.wg.Wait()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
