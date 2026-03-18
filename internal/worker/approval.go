package worker

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/GrapeInTheTree/pocket-claude/internal/store"
)

// sanitizeUTF8 removes invalid UTF-8 bytes from a string.
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "")
}

func (w *Worker) requestApproval(ctx context.Context, msgID string, result *store.CLIResult) (bool, error) {
	ch := make(chan bool, 1)
	w.approvals.Store(msgID, ch)
	defer w.approvals.Delete(msgID)

	text := buildPermissionMessage(result)

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

// ResolveApproval resolves a pending approval request from a Telegram callback.
func (w *Worker) ResolveApproval(id string, approved bool) {
	if val, ok := w.approvals.Load(id); ok {
		if ch, ok := val.(chan bool); ok {
			select {
			case ch <- approved:
			default:
			}
		}
	}
}

func buildPermissionMessage(result *store.CLIResult) string {
	var sb strings.Builder
	sb.WriteString("🔐 *Permission Required*\n\n")

	type toolDetail struct {
		details []string
	}
	tools := make(map[string]*toolDetail)
	order := []string{}

	for _, d := range result.PermissionDenials {
		name := FormatToolName(d.ToolName)
		if _, exists := tools[name]; !exists {
			tools[name] = &toolDetail{}
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

	if result.Result != "" {
		escaped := sanitizeUTF8(EscapeMD(Truncate(result.Result, 150)))
		sb.WriteString(fmt.Sprintf("\n💬 Claude: %s\n", escaped))
	}

	sb.WriteString("\n_Expires in 2 min_")
	return sb.String()
}

func extractToolDetail(d store.PermissionDenial) string {
	if d.ToolInput == nil {
		return ""
	}

	switch d.ToolName {
	case "Bash":
		if cmd, ok := d.ToolInput["command"].(string); ok {
			return Truncate(cmd, 60)
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
		var parts []string
		for k, v := range d.ToolInput {
			if s, ok := v.(string); ok && s != "" {
				parts = append(parts, fmt.Sprintf("%s=%s", k, Truncate(s, 30)))
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

// FormatToolName converts internal tool names to readable labels.
func FormatToolName(raw string) string {
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
				"Slack": "💬", "Notion": "📝", "Gmail": "📧", "pencil": "🎨",
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

// EscapeMD escapes Telegram Markdown special characters.
func EscapeMD(s string) string {
	replacer := strings.NewReplacer("_", "\\_", "*", "\\*", "`", "\\`", "[", "\\[")
	return replacer.Replace(s)
}

// Truncate shortens a string to maxLen runes, appending "..." if truncated.
// Safe for multi-byte UTF-8 (Korean, emoji, CJK).
func Truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
