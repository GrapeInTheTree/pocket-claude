package worker

import (
	"strings"
	"testing"

	"github.com/GrapeInTheTree/pocket-claude/internal/store"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short ASCII", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"truncated ASCII", "hello world", 5, "hello..."},
		{"empty", "", 5, ""},
		{"Korean text under limit", "안녕하세요", 10, "안녕하세요"},
		{"Korean text at limit", "안녕하세요", 5, "안녕하세요"},
		{"Korean text over limit", "안녕하세요 세계", 5, "안녕하세요..."},
		{"Korean truncated mid-word", "가나다라마바사", 3, "가나다..."},
		{"emoji", "👋🌍🚀✨", 2, "👋🌍..."},
		{"mixed Korean+ASCII", "hello 세계", 7, "hello 세..."},
		{"zero max", "hello", 0, "..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestTruncateUTF8Safety(t *testing.T) {
	// Ensure no byte-level splitting of multi-byte characters
	korean := "한국어테스트문자열입니다"
	result := Truncate(korean, 5)
	// Result should be valid UTF-8 and exactly 5 runes + "..."
	runes := []rune(result)
	// 5 Korean chars + 3 dots
	if len(runes) != 8 {
		t.Errorf("Expected 8 runes (5+...), got %d: %q", len(runes), result)
	}
}

func TestEscapeMD(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no special chars", "hello world", "hello world"},
		{"underscore", "my_var", "my\\_var"},
		{"asterisk", "*bold*", "\\*bold\\*"},
		{"backtick", "`code`", "\\`code\\`"},
		{"bracket", "[link]", "\\[link]"},
		{"mixed", "*_`[all", "\\*\\_\\`\\[all"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EscapeMD(tt.input)
			if got != tt.want {
				t.Errorf("EscapeMD(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatToolName(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"Bash", "Bash", "⚡ Terminal Command"},
		{"Write", "Write", "📄 File Write"},
		{"Edit", "Edit", "✏️ File Edit"},
		{"Read", "Read", "📖 File Read"},
		{"unknown tool", "CustomTool", "🔧 CustomTool"},
		// mcp__claude_ai_Slack__slack_send_message → service="Slack", action="Send Message"
		{"MCP Slack", "mcp__claude_ai_Slack__slack_send_message", "💬 Slack → Send Message"},
		{"MCP Notion", "mcp__claude_ai_Notion__notion_search", "📝 Notion → Search"},
		// mcp__foo__bar__do_thing → namespace="foo", service="foo", action="do_thing"
		{"MCP unknown service", "mcp__foo__bar__do_thing", "🔌 foo → Do Thing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatToolName(tt.raw)
			if got != tt.want {
				t.Errorf("FormatToolName(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestBuildPermissionMessage(t *testing.T) {
	result := &store.CLIResult{
		PermissionDenials: []store.PermissionDenial{
			{
				ToolName:  "Bash",
				ToolInput: map[string]any{"command": "rm -rf /tmp/test"},
			},
			{
				ToolName:  "Write",
				ToolInput: map[string]any{"file_path": "/home/user/file.txt"},
			},
		},
		Result: "I need to run a command",
	}

	msg := buildPermissionMessage(result)

	// Should contain tool names
	if !contains(msg, "Terminal Command") {
		t.Error("Expected 'Terminal Command' in message")
	}
	if !contains(msg, "File Write") {
		t.Error("Expected 'File Write' in message")
	}
	// Should contain command detail
	if !contains(msg, "rm -rf /tmp/test") {
		t.Error("Expected command in message")
	}
	// Should contain expiry notice
	if !contains(msg, "2 min") {
		t.Error("Expected expiry notice in message")
	}
}

func TestBuildPermissionMessageDedup(t *testing.T) {
	// Multiple denials for the same tool should be grouped
	result := &store.CLIResult{
		PermissionDenials: []store.PermissionDenial{
			{ToolName: "Bash", ToolInput: map[string]any{"command": "ls"}},
			{ToolName: "Bash", ToolInput: map[string]any{"command": "pwd"}},
			{ToolName: "Bash", ToolInput: map[string]any{"command": "cat file"}},
			{ToolName: "Bash", ToolInput: map[string]any{"command": "extra"}}, // 4th, should be capped at 3
		},
	}

	msg := buildPermissionMessage(result)

	// Should show max 3 details per tool
	if contains(msg, "extra") {
		t.Error("Expected 4th detail to be omitted (max 3)")
	}
}

func TestSanitizeUTF8(t *testing.T) {
	valid := "hello 세계"
	if got := sanitizeUTF8(valid); got != valid {
		t.Errorf("Valid UTF-8 should be unchanged, got %q", got)
	}

	// Invalid UTF-8: 0xff is not valid
	invalid := "hello\xff world"
	got := sanitizeUTF8(invalid)
	if got != "hello world" {
		t.Errorf("Expected invalid bytes removed, got %q", got)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
