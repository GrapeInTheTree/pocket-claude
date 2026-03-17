package bot

import (
	"fmt"
	"strings"
	"time"

	"github.com/GrapeInTheTree/pocket-claude/internal/store"
	"github.com/GrapeInTheTree/pocket-claude/internal/worker"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

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
	case "name":
		b.cmdName(msg.CommandArguments())
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
		"/name `<text>` — Rename current session\n" +
		"/resume — Resume a previous session\n" +
		"/btw `<note>` — Add context without processing\n" +
		"/model `<name>` — Switch model (sonnet, opus, haiku)\n" +
		"/cancel — Cancel current processing\n\n" +
		"*Queue:*\n" +
		"/status — Message queue status\n" +
		"/clear — Clean up completed messages\n" +
		"/retry — Force retry error messages\n\n" +
		"*How it works:*\n" +
		"Send any message and Claude will process it.\n" +
		"Conversations persist automatically.\n" +
		"Use /new to start fresh, /resume to go back."
	b.sendMarkdown(text)
}

func (b *Bot) cmdNew() {
	if b.worker != nil {
		b.worker.ResetSession()
	}
	b.sendMessage("🔄 New conversation started.")
}

func (b *Bot) cmdBtw(msg *tgbotapi.Message) {
	text := strings.TrimSpace(msg.CommandArguments())
	if text == "" {
		b.sendMessage("Usage: /btw <context note>\nExample: /btw I'm working on the API project today")
		return
	}

	inboxMsg := store.InboxMessage{
		ID:                fmt.Sprintf("msg_%d", time.Now().UnixMilli()),
		Text:              "[BTW context note, just acknowledge briefly] " + text,
		Status:            store.StatusPending,
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
		b.sendMessage("No previous sessions found. Send some messages first!")
		return
	}

	// Direct selection: /resume 2 or /resume <session_id>
	args = strings.TrimSpace(args)
	if args != "" {
		var idx int
		if _, err := fmt.Sscanf(args, "%d", &idx); err == nil && idx >= 1 && idx <= len(sessions) {
			s := sessions[len(sessions)-idx]
			b.worker.ResumeSession(s.ID)
			b.sendMessage(fmt.Sprintf("🔄 Resumed session #%d: %s", idx, s.FirstMsg))
			return
		}

		for _, s := range sessions {
			if strings.HasPrefix(s.ID, args) {
				b.worker.ResumeSession(s.ID)
				b.sendMessage(fmt.Sprintf("🔄 Resumed session: %s", s.FirstMsg))
				return
			}
		}

		b.sendMessage("Session not found. Use /resume to see the list.")
		return
	}

	// Show inline keyboard with recent sessions (max 5)
	maxShow := 5
	if len(sessions) < maxShow {
		maxShow = len(sessions)
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for i := len(sessions) - 1; i >= len(sessions)-maxShow; i-- {
		num := len(sessions) - i
		s := sessions[i]
		age := time.Since(s.Timestamp)
		var ageStr string
		if age < time.Hour {
			ageStr = fmt.Sprintf("%dm", int(age.Minutes()))
		} else {
			ageStr = fmt.Sprintf("%dh", int(age.Hours()))
		}
		displayName := s.FirstMsg
		if s.Name != "" {
			displayName = s.Name
		}
		label := fmt.Sprintf("#%d %s (%s)", num, worker.Truncate(displayName, 30), ageStr)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, "resume:"+s.ID),
		))
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	msg := tgbotapi.NewMessage(b.cfg.TelegramChatID, "📋 Select a session to resume:")
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

func (b *Bot) cmdName(args string) {
	if b.worker == nil {
		return
	}

	name := strings.TrimSpace(args)
	if name == "" {
		b.sendMessage("Usage: /name <session name>\nExample: /name autoresearch 조사")
		return
	}

	if b.worker.GetCurrentSessionID() == "" {
		b.sendMessage("No active session. Send a message first!")
		return
	}

	b.worker.SetSessionName(name)
	b.sendMessage(fmt.Sprintf("✏️ Session renamed to \"%s\"", name))
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
	sb.WriteString(fmt.Sprintf("  pending    : %d\n", stats[store.StatusPending]))
	sb.WriteString(fmt.Sprintf("  processing : %d\n", stats[store.StatusProcessing]))
	sb.WriteString(fmt.Sprintf("  done       : %d\n", stats[store.StatusDone]))
	sb.WriteString(fmt.Sprintf("  sent       : %d\n", stats[store.StatusSent]))
	sb.WriteString(fmt.Sprintf("  error      : %d\n", stats[store.StatusError]))

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
	if b.worker == nil {
		return
	}

	var retried int
	b.store.UpdateInbox(func(mf *store.InboxFile) bool {
		changed := false
		for i := range mf.Messages {
			if mf.Messages[i].Status == store.StatusError || mf.Messages[i].Status == store.StatusFailed {
				mf.Messages[i].Status = store.StatusPending
				mf.Messages[i].RetryCount = 0
				mf.Messages[i].LastError = ""
				changed = true
				retried++
			}
		}
		return changed
	})

	if retried == 0 {
		b.sendMessage("No error messages to retry.")
	} else {
		b.sendMessage(fmt.Sprintf("Force-retrying %d messages (retry count reset).", retried))
	}
}
