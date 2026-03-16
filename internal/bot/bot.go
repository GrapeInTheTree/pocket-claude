package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/GrapeInTheTree/claude-cowork-telegram/internal/config"
	"github.com/GrapeInTheTree/claude-cowork-telegram/internal/store"
	"github.com/GrapeInTheTree/claude-cowork-telegram/internal/worker"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	api    *tgbotapi.BotAPI
	cfg    config.Config
	store  *store.Store
	worker *worker.Worker
	logger *slog.Logger
	wg     sync.WaitGroup
}

func New(cfg config.Config, st *store.Store, logger *slog.Logger) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		return nil, fmt.Errorf("create bot API: %w", err)
	}

	logger.Info("Telegram bot authorized", "username", api.Self.UserName)

	return &Bot{
		api:    api,
		cfg:    cfg,
		store:  st,
		logger: logger,
	}, nil
}

func (b *Bot) SetWorker(w *worker.Worker) {
	b.worker = w
}

// SendMessage sends a plain text message to the configured chat.
func (b *Bot) SendMessage(text string) error {
	msg := tgbotapi.NewMessage(b.cfg.TelegramChatID, text)
	_, err := b.api.Send(msg)
	return err
}

// SendApprovalRequest sends an inline keyboard for permission approval.
// Falls back to plain text if Markdown parsing fails.
func (b *Bot) SendApprovalRequest(approvalID, text string) error {
	// Sanitize UTF-8 to prevent Telegram API rejection
	text = strings.ToValidUTF8(text, "")

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Allow", "approve:"+approvalID),
			tgbotapi.NewInlineKeyboardButtonData("❌ Deny", "deny:"+approvalID),
		),
	)
	msg := tgbotapi.NewMessage(b.cfg.TelegramChatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	if _, err := b.api.Send(msg); err != nil {
		b.logger.Warn("Markdown send failed, retrying plain text", "error", err)
		msg.ParseMode = ""
		_, err = b.api.Send(msg)
		return err
	}
	return nil
}

func (b *Bot) sendMessage(text string) error {
	return b.SendMessage(strings.ToValidUTF8(text, ""))
}

func (b *Bot) sendMarkdown(text string) error {
	msg := tgbotapi.NewMessage(b.cfg.TelegramChatID, text)
	msg.ParseMode = "Markdown"
	if _, err := b.api.Send(msg); err != nil {
		msg.ParseMode = ""
		_, err = b.api.Send(msg)
		return err
	}
	return nil
}

// Listen starts the Telegram update loop.
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

			if update.CallbackQuery != nil {
				go b.handleCallback(update.CallbackQuery)
				continue
			}

			if update.Message == nil {
				continue
			}
			if update.Message.Chat.ID != b.cfg.TelegramChatID {
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

	// Photos
	if len(msg.Photo) > 0 {
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
		b.logger.Info("Photo received", "path", filePath)
	}

	// Documents
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
		b.logger.Info("Document received", "path", filePath)
	}

	if text == "" {
		return
	}

	inboxMsg := store.InboxMessage{
		ID:                fmt.Sprintf("msg_%d", time.Now().UnixMilli()),
		Text:              text,
		Status:            store.StatusPending,
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		TelegramMessageID: msg.MessageID,
	}

	if err := b.store.AppendToInbox(inboxMsg); err != nil {
		b.logger.Error("Failed to save message", "id", inboxMsg.ID, "error", err)
		return
	}
	b.logger.Info("Message saved to inbox", "id", inboxMsg.ID)

	if b.worker != nil {
		b.worker.Enqueue(inboxMsg)
	}
}

func (b *Bot) handleCallback(cq *tgbotapi.CallbackQuery) {
	data := cq.Data
	parts := strings.SplitN(data, ":", 2)
	if len(parts) != 2 {
		return
	}

	action := parts[0]
	value := parts[1]

	// Handle resume session callback
	if action == "resume" {
		callback := tgbotapi.NewCallback(cq.ID, "")
		b.api.Request(callback)

		if b.worker != nil {
			b.worker.ResumeSession(value)
		}

		// Find session first message for display
		label := value[:12]
		if b.worker != nil {
			for _, s := range b.worker.GetSessions() {
				if s.ID == value {
					label = s.FirstMsg
					break
				}
			}
		}

		edit := tgbotapi.NewEditMessageText(cq.Message.Chat.ID, cq.Message.MessageID,
			fmt.Sprintf("🔄 Resumed: %s", label))
		b.api.Send(edit)
		return
	}

	// Handle permission approval/denial callback
	approvalID := value
	approved := action == "approve"

	b.logger.Info("Permission callback", "approval_id", approvalID, "approved", approved)

	callback := tgbotapi.NewCallback(cq.ID, "")
	b.api.Request(callback)

	var statusText string
	if approved {
		statusText = "✅ Approved — executing with permissions..."
	} else {
		statusText = "❌ Denied — request cancelled."
	}
	edit := tgbotapi.NewEditMessageText(cq.Message.Chat.ID, cq.Message.MessageID, statusText)
	b.api.Send(edit)

	if b.worker != nil {
		b.worker.ResolveApproval(approvalID, approved)
	}
}

// PollOutbox periodically sends done outbox messages to Telegram.
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
			return
		case <-ticker.C:
			b.processOutbox()
		}
	}
}

func (b *Bot) processOutbox() {
	b.store.UpdateOutbox(func(mf *store.OutboxFile) bool {
		changed := false
		for i := range mf.Messages {
			if mf.Messages[i].Status != store.StatusDone {
				continue
			}
			text := mf.Messages[i].Result
			if text == "" {
				continue
			}
			if err := b.sendMessage(text); err != nil {
				continue
			}
			mf.Messages[i].Status = store.StatusSent
			changed = true
		}
		return changed
	})
}

func (b *Bot) Shutdown() {
	b.api.StopReceivingUpdates()
	b.wg.Wait()
}
