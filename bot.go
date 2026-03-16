package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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

			// Handle callback queries (permission approval buttons)
			if update.CallbackQuery != nil {
				go b.handleCallback(update.CallbackQuery)
				continue
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
	text := msg.Text

	// Handle photos: download and include file path in prompt
	if msg.Photo != nil && len(msg.Photo) > 0 {
		// Get the largest photo
		photo := msg.Photo[len(msg.Photo)-1]
		filePath, err := b.downloadFile(photo.FileID)
		if err != nil {
			b.logger.Error("Failed to download photo", "error", err)
			b.sendMessage("Failed to download photo.")
			return
		}

		caption := msg.Caption
		if caption == "" {
			caption = "Analyze this image"
		}
		text = fmt.Sprintf("%s\n\nImage file: %s", caption, filePath)
		b.logger.Info("Photo received", "path", filePath, "caption", caption)
	}

	// Handle documents (files)
	if msg.Document != nil {
		filePath, err := b.downloadFile(msg.Document.FileID)
		if err != nil {
			b.logger.Error("Failed to download document", "error", err)
			b.sendMessage("Failed to download file.")
			return
		}

		caption := msg.Caption
		if caption == "" {
			caption = "Analyze this file"
		}
		text = fmt.Sprintf("%s\n\nFile: %s", caption, filePath)
		b.logger.Info("Document received", "path", filePath, "name", msg.Document.FileName)
	}

	if text == "" {
		return
	}

	inboxMsg := InboxMessage{
		ID:                fmt.Sprintf("msg_%d", time.Now().UnixMilli()),
		Text:              text,
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

	if b.worker != nil {
		if !b.worker.Enqueue(inboxMsg) {
			b.logger.Error("Failed to enqueue message", "id", inboxMsg.ID)
		}
	}
}

// --- Callback queries (permission approval) ---

func (b *Bot) handleCallback(cq *tgbotapi.CallbackQuery) {
	data := cq.Data // "approve:msg_123" or "deny:msg_123"
	parts := strings.SplitN(data, ":", 2)
	if len(parts) != 2 {
		return
	}

	action := parts[0]
	approvalID := parts[1]
	approved := action == "approve"

	b.logger.Info("Permission callback received",
		"approval_id", approvalID,
		"approved", approved)

	// Answer callback (remove loading indicator)
	callback := tgbotapi.NewCallback(cq.ID, "")
	b.api.Request(callback)

	// Update the message to show result
	var statusText string
	if approved {
		statusText = "✅ *Approved* — executing with permissions..."
	} else {
		statusText = "❌ *Denied* — request cancelled."
	}
	edit := tgbotapi.NewEditMessageText(cq.Message.Chat.ID, cq.Message.MessageID, statusText)
	edit.ParseMode = "Markdown"
	b.api.Send(edit)

	// Resolve approval in worker
	if b.worker != nil {
		b.worker.ResolveApproval(approvalID, approved)
	}
}

// sendApprovalRequest sends an inline keyboard message for permission approval.
func (b *Bot) sendApprovalRequest(approvalID, text string) error {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Allow", "approve:"+approvalID),
			tgbotapi.NewInlineKeyboardButtonData("❌ Deny", "deny:"+approvalID),
		),
	)
	msg := tgbotapi.NewMessage(b.cfg.TelegramChatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	_, err := b.api.Send(msg)
	return err
}

// --- Commands ---

func (b *Bot) handleCommand(msg *tgbotapi.Message) {
	switch msg.Command() {
	case "help":
		b.cmdHelp()
	case "new":
		b.cmdNew()
	case "btw":
		b.cmdBtw(msg)
	case "resume":
		b.cmdResume(msg.CommandArguments())
	case "model":
		b.cmdModel(msg.CommandArguments())
	case "cancel":
		b.cmdCancel()
	case "status":
		b.cmdStatus()
	case "clear":
		b.cmdClear()
	case "retry":
		b.cmdRetry()
	default:
		b.sendMessage(fmt.Sprintf("Unknown command: /%s\nUse /help to see available commands.", msg.Command()))
	}
}

func (b *Bot) cmdHelp() {
	text := "🤖 *Cowork Telegram Bot*\n\n" +
		"*Session:*\n" +
		"/new — Start a new conversation\n" +
		"/resume — Resume a previous session\n" +
		"/btw `<note>` — Add context without processing\n" +
		"/model `<name>` — Switch model (sonnet, opus, haiku)\n" +
		"/cancel — Cancel current processing\n\n" +
		"*Queue:*\n" +
		"/status — Message queue status\n" +
		"/clear — Clean up done/sent messages\n" +
		"/retry — Force retry error messages\n\n" +
		"*How it works:*\n" +
		"Send any message and Claude will process it.\n" +
		"Conversations persist automatically.\n" +
		"Use /new to start fresh, /resume to go back."
	m := tgbotapi.NewMessage(b.cfg.TelegramChatID, text)
	m.ParseMode = "Markdown"
	b.api.Send(m)
}

func (b *Bot) cmdNew() {
	if b.worker != nil {
		b.worker.ResetSession()
	}
	m := tgbotapi.NewMessage(b.cfg.TelegramChatID, "🔄 New conversation started.")
	b.api.Send(m)
}

func (b *Bot) cmdModel(args string) {
	if b.worker == nil {
		return
	}

	args = strings.TrimSpace(args)
	if args == "" {
		current := b.worker.GetModel()
		if current == "" {
			current = "(default)"
		}
		b.sendMarkdown(fmt.Sprintf("Current model: `%s`\n\nUsage: /model `<name>`\nExamples: sonnet, opus, haiku", current))
		return
	}

	b.worker.SetModel(args)
	b.sendMarkdown(fmt.Sprintf("✅ Model switched to `%s`", args))
}

func (b *Bot) cmdBtw(msg *tgbotapi.Message) {
	text := strings.TrimSpace(msg.CommandArguments())
	if text == "" {
		b.sendMessage("Usage: /btw <context note>\nExample: /btw I'm working on the API project today")
		return
	}

	// Process as a context note through the normal pipeline
	inboxMsg := InboxMessage{
		ID:                fmt.Sprintf("msg_%d", time.Now().UnixMilli()),
		Text:              "[BTW context note, just acknowledge briefly] " + text,
		Status:            StatusPending,
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		TelegramMessageID: msg.MessageID,
	}

	if err := b.store.AppendToInbox(inboxMsg); err != nil {
		b.logger.Error("Failed to save btw", "error", err)
		return
	}

	if b.worker != nil {
		b.worker.Enqueue(inboxMsg)
	}
}

func (b *Bot) cmdResume(args string) {
	if b.worker == nil {
		return
	}

	sessions := b.worker.GetSessions()

	if len(sessions) == 0 {
		b.sendMessage("No previous sessions found.")
		return
	}

	args = strings.TrimSpace(args)

	// If number provided, resume that session
	if args != "" {
		var idx int
		if _, err := fmt.Sscanf(args, "%d", &idx); err == nil && idx >= 1 && idx <= len(sessions) {
			s := sessions[len(sessions)-idx] // 1 = most recent
			b.worker.ResumeSession(s.ID)
			b.sendMarkdown(fmt.Sprintf("🔄 Resumed session #%d\n`%s`\n_%s_", idx, s.ID[:12], s.FirstMsg))
			return
		}

		// Try as session ID directly
		for _, s := range sessions {
			if strings.HasPrefix(s.ID, args) {
				b.worker.ResumeSession(s.ID)
				b.sendMarkdown(fmt.Sprintf("🔄 Resumed session\n`%s`", s.ID[:12]))
				return
			}
		}

		b.sendMessage("Session not found. Use /resume to see the list.")
		return
	}

	// Show session list
	var sb strings.Builder
	sb.WriteString("📋 *Recent Sessions*\n\n")
	for i := len(sessions) - 1; i >= 0; i-- {
		num := len(sessions) - i
		s := sessions[i]
		age := time.Since(s.Timestamp)
		var ageStr string
		if age < time.Hour {
			ageStr = fmt.Sprintf("%dm ago", int(age.Minutes()))
		} else {
			ageStr = fmt.Sprintf("%dh ago", int(age.Hours()))
		}
		sb.WriteString(fmt.Sprintf("#%d — _%s_ (%s)\n`%s`\n\n", num, s.FirstMsg, ageStr, s.ID[:12]))
	}
	sb.WriteString("Use: /resume `<number>`")
	b.sendMarkdown(sb.String())
}

func (b *Bot) cmdCancel() {
	if b.worker == nil {
		return
	}

	msgID, ok := b.worker.CancelCurrent()
	if ok {
		b.sendMarkdown(fmt.Sprintf("🛑 Cancelled processing: `%s`", msgID))
	} else {
		b.sendMessage("Nothing is being processed right now.")
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

// --- Outbox poller ---

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

// --- Retry processor ---

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

func (b *Bot) downloadFile(fileID string) (string, error) {
	file, err := b.api.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return "", fmt.Errorf("get file info: %w", err)
	}

	// Download via Telegram API
	url := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", b.cfg.TelegramToken, file.FilePath)

	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()

	// Determine extension from original path
	ext := filepath.Ext(file.FilePath)
	if ext == "" {
		ext = ".bin"
	}

	// Save to temp file
	tmpFile, err := os.CreateTemp("", "telegram-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return "", fmt.Errorf("save file: %w", err)
	}

	return tmpFile.Name(), nil
}

func (b *Bot) sendMessage(text string) error {
	msg := tgbotapi.NewMessage(b.cfg.TelegramChatID, text)
	_, err := b.api.Send(msg)
	return err
}

func (b *Bot) sendMarkdown(text string) error {
	msg := tgbotapi.NewMessage(b.cfg.TelegramChatID, text)
	msg.ParseMode = "Markdown"
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
