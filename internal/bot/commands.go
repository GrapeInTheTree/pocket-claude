package bot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GrapeInTheTree/pocket-claude/internal/project"
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
	case "usage":
		b.cmdUsage()
	case "status":
		b.cmdStatus()
	case "clear":
		b.cmdClear()
	case "retry":
		b.cmdRetry()
	case "project":
		b.cmdProject(msg.CommandArguments())
	case "bg":
		b.cmdBg(msg)
	case "ralph":
		b.cmdRalph(msg)
	case "plan":
		b.cmdPlan(msg)
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
		"*Project:*\n" +
		"/project — Switch project (inline keyboard)\n" +
		"/project info — Current project details\n" +
		"/project add `<name>` `<path>` — Add project\n" +
		"/project search `<keyword>` — Find git repos to add\n" +
		"/project rename `<old>` `<new>` — Rename project\n" +
		"/project remove `<name>` — Remove project\n\n" +
		"*Background:*\n" +
		"/bg `<message>` — Run task in background\n" +
		"/bg inject `<id>` — Inject result into session\n" +
		"/bg status / cancel `<id>`\n\n" +
		"*Ralph (Iterative Loop):*\n" +
		"/ralph `<message>` — Auto-loop until done\n" +
		"/ralph `<msg>` --max `<N>` — Set max iterations\n" +
		"/ralph status / cancel `<id>`\n\n" +
		"*Plan Mode:*\n" +
		"/plan `<message>` — Plan first, execute on approval\n\n" +
		"*Queue:*\n" +
		"/status — Message queue status\n" +
		"/usage — Token cost tracking\n" +
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
		b.worker.ResetSessionUsage()
	}
	b.sendMessage("🔄 New conversation started.")
}

func (b *Bot) cmdBtw(msg *tgbotapi.Message) {
	text := strings.TrimSpace(msg.CommandArguments())
	if text == "" {
		b.sendMessage("Usage: /btw <context note>\nExample: /btw I'm working on the API project today")
		return
	}

	var projectName string
	if b.worker != nil {
		projectName = b.worker.ActiveProject()
	}

	inboxMsg := store.InboxMessage{
		ID:                fmt.Sprintf("msg_%d", time.Now().UnixMilli()),
		Text:              "[BTW context note, just acknowledge briefly] " + text,
		Status:            store.StatusPending,
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		TelegramMessageID: msg.MessageID,
		Project:           projectName,
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

func (b *Bot) cmdUsage() {
	if b.worker == nil {
		return
	}

	u := b.worker.GetUsage()
	projectName := b.worker.ActiveProject()
	text := fmt.Sprintf(
		"📊 *Usage* [%s]\n\n"+
			"*Session*\n"+
			"  Cost : $%.4f\n\n"+
			"*Total (since restart)*\n"+
			"  Messages : %d\n"+
			"  Cost     : $%.4f\n\n"+
			"_Estimated API-equivalent cost._",
		projectName,
		u.SessionCostUSD,
		u.TotalMessages, u.TotalCostUSD)
	b.sendMarkdown(text)
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

	if b.worker != nil {
		bgCount := b.worker.BackgroundRunningCount()
		if bgCount > 0 {
			sb.WriteString(fmt.Sprintf("\n\n🔄 Background tasks: %d/%d", bgCount, 3))
		}
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

func (b *Bot) cmdProject(args string) {
	if b.worker == nil {
		return
	}

	args = strings.TrimSpace(args)

	// /project add <name> <path>
	if strings.HasPrefix(args, "add ") {
		parts := strings.Fields(args)
		if len(parts) < 3 {
			b.sendMessage("Usage: /project add <name> <path>\nExample: /project add my-app /Users/me/my-app")
			return
		}
		name := parts[1]
		path := parts[2]
		if err := b.worker.AddProject(name, path); err != nil {
			b.sendMessage("Failed: " + err.Error())
			return
		}
		b.sendMessage(fmt.Sprintf("✅ Project \"%s\" added (%s)", name, path))
		return
	}

	// /project remove <name>
	if strings.HasPrefix(args, "remove ") {
		parts := strings.Fields(args)
		if len(parts) < 2 {
			b.sendMessage("Usage: /project remove <name>")
			return
		}
		name := parts[1]
		if err := b.worker.RemoveProject(name); err != nil {
			b.sendMessage("Failed: " + err.Error())
			return
		}
		b.sendMessage(fmt.Sprintf("🗑 Project \"%s\" removed.", name))
		return
	}

	// /project rename <old> <new>
	if strings.HasPrefix(args, "rename ") {
		parts := strings.Fields(args)
		if len(parts) < 3 {
			b.sendMessage("Usage: /project rename <old-name> <new-name>")
			return
		}
		oldName, newName := parts[1], parts[2]
		if err := b.worker.RenameProject(oldName, newName); err != nil {
			b.sendMessage("Failed: " + err.Error())
			return
		}
		b.sendMessage(fmt.Sprintf("✏️ Project \"%s\" renamed to \"%s\"", oldName, newName))
		return
	}

	// /project info
	if args == "info" {
		pc, u, sessionCount := b.worker.GetProjectInfo()
		home, _ := os.UserHomeDir()
		displayPath := pc.WorkDir
		if home != "" && strings.HasPrefix(displayPath, home) {
			displayPath = "~" + displayPath[len(home):]
		}
		text := fmt.Sprintf(
			"📂 *Project: %s*\n\n"+
				"📁 Path: `%s`\n"+
				"🗂 Sessions: %d\n\n"+
				"*Session cost*: $%.4f\n\n"+
				"*Total usage*\n"+
				"  Messages : %d\n"+
				"  Cost     : $%.4f\n\n"+
				"_Estimated API-equivalent cost. Since bot started._",
			pc.Name, displayPath, sessionCount,
			u.SessionCostUSD,
			u.TotalMessages, u.TotalCostUSD)
		b.sendMarkdown(text)
		return
	}

	// /project search <keyword>
	if strings.HasPrefix(args, "search ") {
		keyword := strings.TrimSpace(strings.TrimPrefix(args, "search"))
		if keyword == "" {
			b.sendMessage("Usage: /project search <keyword>\nExample: /project search my-app")
			return
		}
		b.cmdProjectSearch(keyword)
		return
	}

	// /project list — explicit list
	if args == "list" {
		args = ""
		// fall through to show keyboard
	}

	// /project <name> — direct switch
	if args != "" {
		if err := b.worker.SwitchProject(args); err != nil {
			b.sendMessage("Failed: " + err.Error())
			return
		}
		b.sendMessage(fmt.Sprintf("📂 Switched to project \"%s\"", args))
		return
	}

	// /project — show inline keyboard
	active, projects := b.worker.ListProjects()
	home, _ := os.UserHomeDir()

	// Sort project names for stable button order, active first
	names := make([]string, 0, len(projects))
	for name := range projects {
		if name != active {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	names = append([]string{active}, names...)

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, name := range names {
		pc := projects[name]
		displayPath := pc.WorkDir
		if home != "" && strings.HasPrefix(displayPath, home) {
			displayPath = "~" + displayPath[len(home):]
		}
		var label string
		if name == active {
			label = fmt.Sprintf("▶ %s  (%s)", name, worker.Truncate(displayPath, 30))
		} else {
			label = fmt.Sprintf("   %s  (%s)", name, worker.Truncate(displayPath, 30))
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, "project:"+name),
		))
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	text := fmt.Sprintf("📂 *Projects* (%d) — Active: *%s*\n\n"+
		"Tap to switch, or use:\n"+
		"`/project info` — details & usage\n"+
		"`/project add <name> <path>`\n"+
		"`/project search <keyword>`\n"+
		"`/project rename <old> <new>`\n"+
		"`/project remove <name>`",
		len(projects), active)
	msg := tgbotapi.NewMessage(b.cfg.TelegramChatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	if _, err := b.api.Send(msg); err != nil {
		msg.ParseMode = ""
		b.api.Send(msg)
	}
}

func (b *Bot) cmdProjectSearch(keyword string) {
	results := project.SearchRepos(keyword, 8)
	if len(results) == 0 {
		b.sendMessage(fmt.Sprintf("No git repos found matching \"%s\".\nTry a different keyword, or add manually:\n/project add <name> <path>", keyword))
		return
	}

	home, _ := os.UserHomeDir()

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, path := range results {
		name := filepath.Base(path)
		// Show shortened path: ~/sub/repo instead of /Users/.../sub/repo
		displayPath := path
		if home != "" && strings.HasPrefix(path, home) {
			displayPath = "~" + path[len(home):]
		}
		label := fmt.Sprintf("+ %s  (%s)", name, worker.Truncate(displayPath, 35))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, "project_add:"+name+"|"+path),
		))
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	text := fmt.Sprintf("🔍 *Found %d repo(s)* matching \"%s\"\n\nTap to add as project:", len(results), keyword)
	msg := tgbotapi.NewMessage(b.cfg.TelegramChatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	if _, err := b.api.Send(msg); err != nil {
		// Markdown fallback
		msg.ParseMode = ""
		b.api.Send(msg)
	}
}

func (b *Bot) cmdBg(msg *tgbotapi.Message) {
	if b.worker == nil {
		return
	}

	args := strings.TrimSpace(msg.CommandArguments())

	// /bg → help
	if args == "" {
		b.sendMarkdown(
			"🔄 *Background Tasks*\n\n" +
				"`/bg <message>` — Run in current project\n" +
				"`/bg <project> <message>` — Run in specific project\n" +
				"`/bg status` — Show running tasks\n" +
				"`/bg inject <id>` — Inject result into session\n" +
				"`/bg cancel <id>` — Cancel a task\n\n" +
				"Background tasks run independently.\n" +
				"Use inject to bring results into your conversation.")
		return
	}

	// /bg status
	if args == "status" {
		b.sendMessage(b.worker.BackgroundStatus())
		return
	}

	// /bg inject <id>
	if strings.HasPrefix(args, "inject ") {
		taskID := strings.TrimSpace(strings.TrimPrefix(args, "inject"))
		if taskID == "" {
			b.sendMessage("Usage: /bg inject <task_id>")
			return
		}
		resultText, projectName, err := b.worker.GetBackgroundResult(taskID)
		if err != nil {
			b.sendMessage("Failed: " + err.Error())
			return
		}

		// Truncate to avoid excessive context injection
		injected := worker.Truncate(resultText, 4000)
		btwText := fmt.Sprintf("[Background task %s result from project %q] %s", taskID, projectName, injected)

		inboxMsg := store.InboxMessage{
			ID:        fmt.Sprintf("msg_%d", time.Now().UnixMilli()),
			Text:      "[Injected context — do NOT respond to this, just silently remember it for future reference] " + btwText,
			Status:    store.StatusPending,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Project:   b.worker.ActiveProject(),
		}
		if err := b.store.AppendToInbox(inboxMsg); err != nil {
			b.sendMessage("Failed to inject: " + err.Error())
			return
		}
		b.worker.Enqueue(inboxMsg)
		b.sendMessage(fmt.Sprintf("💉 Injected %s into current session.\nClaude will now have context from that background task.", taskID))
		return
	}

	// /bg cancel <id>
	if strings.HasPrefix(args, "cancel ") {
		taskID := strings.TrimSpace(strings.TrimPrefix(args, "cancel"))
		if taskID == "" {
			b.sendMessage("Usage: /bg cancel <task_id>")
			return
		}
		if err := b.worker.CancelBackground(taskID); err != nil {
			b.sendMessage("Failed: " + err.Error())
		} else {
			b.sendMessage(fmt.Sprintf("🛑 Cancelled background task: %s", taskID))
		}
		return
	}

	// /bg <project> <message> or /bg <message>
	projectName, message := b.parseBgArgs(args)

	taskID, err := b.worker.EnqueueBackground(context.Background(), projectName, message)
	if err != nil {
		b.sendMessage("❌ " + err.Error())
		return
	}

	b.sendMessage(fmt.Sprintf(
		"🔄 Background task started\n🆔 %s\n📂 Project: %s\n💬 %s\n\nUse /bg status to check progress.",
		taskID, projectName, worker.Truncate(message, 60)))
}

// parseBgArgs splits args into project name and message.
// If the first word matches a registered project (and it's different from
// the active project), it's used as the target project. Otherwise the
// entire string is the message for the active project.
// This avoids misrouting when the active project name happens to be the first word.
func (b *Bot) parseBgArgs(args string) (projectName, message string) {
	active := b.worker.ActiveProject()
	parts := strings.SplitN(args, " ", 2)
	if len(parts) == 2 {
		candidate := parts[0]
		// Only treat as project routing if:
		// 1. It's a known project name, AND
		// 2. It's different from the active project (otherwise "test do X" would
		//    be misrouted if active project is called "test")
		if candidate != active && b.worker.HasProject(candidate) {
			return candidate, parts[1]
		}
	}
	return active, args
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

// --- /ralph command ---

func (b *Bot) cmdRalph(msg *tgbotapi.Message) {
	if b.worker == nil {
		return
	}

	args := strings.TrimSpace(msg.CommandArguments())

	if args == "" {
		b.sendMarkdown(
			"🔁 *Ralph — Iterative Loop*\n\n" +
				"`/ralph <message>` — Auto-loop until done\n" +
				"`/ralph <message> --max <N>` — Set max iterations (default 5)\n" +
				"`/ralph status` — Show running loops\n" +
				"`/ralph cancel <id>` — Cancel a loop\n\n" +
				"Claude repeats the task across iterations,\n" +
				"seeing its previous work each time.")
		return
	}

	if args == "status" {
		b.sendMessage(b.worker.RalphStatus())
		return
	}

	if strings.HasPrefix(args, "cancel ") {
		taskID := strings.TrimSpace(strings.TrimPrefix(args, "cancel"))
		if taskID == "" {
			b.sendMessage("Usage: /ralph cancel <task_id>")
			return
		}
		if err := b.worker.CancelBackground(taskID); err != nil {
			b.sendMessage("Failed: " + err.Error())
		} else {
			b.sendMessage(fmt.Sprintf("🛑 Cancelled ralph loop: %s", taskID))
		}
		return
	}

	message, maxIter := worker.ParseRalphArgs(args)
	if message == "" {
		b.sendMessage("Usage: /ralph <message> [--max <N>]")
		return
	}
	projectName := b.worker.ActiveProject()

	taskID, err := b.worker.EnqueueRalph(context.Background(), projectName, message, maxIter)
	if err != nil {
		b.sendMessage("❌ " + err.Error())
		return
	}

	b.sendMessage(fmt.Sprintf(
		"🔁 Ralph loop started\n🆔 %s\n📂 Project: %s\n🔄 Max iterations: %d\n💬 %s",
		taskID, projectName, maxIter, worker.Truncate(message, 60)))
}

// --- /plan command ---

func (b *Bot) cmdPlan(msg *tgbotapi.Message) {
	if b.worker == nil {
		return
	}

	text := strings.TrimSpace(msg.CommandArguments())
	if text == "" {
		b.sendMarkdown(
			"📋 *Plan Mode*\n\n" +
				"`/plan <message>` — Ask Claude to plan first\n\n" +
				"Claude will analyze and create a plan without executing.\n" +
				"Then you can review, modify, and say \"execute\" naturally.")
		return
	}

	var projectName string
	if b.worker != nil {
		projectName = b.worker.ActiveProject()
	}

	planPrompt := "[Plan mode: Create a detailed implementation plan for the following task. " +
		"Analyze the codebase and outline specific steps with file paths. " +
		"Do NOT execute anything yet — only plan. " +
		"Wait for my approval before making any changes.] " + text

	inboxMsg := store.InboxMessage{
		ID:                fmt.Sprintf("msg_%d", time.Now().UnixMilli()),
		Text:              planPrompt,
		Status:            store.StatusPending,
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		TelegramMessageID: msg.MessageID,
		Project:           projectName,
	}

	if err := b.store.AppendToInbox(inboxMsg); err != nil {
		b.logger.Error("Failed to save plan", "error", err)
		return
	}

	if b.worker != nil {
		b.worker.Enqueue(inboxMsg)
	}
}
