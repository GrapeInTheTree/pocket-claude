package bot

import (
	"testing"
)

func TestSafeTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"truncated", "hello world", 5, "hello"},
		{"empty", "", 5, ""},
		{"zero max", "hello", 0, ""},
		{"Korean under limit", "안녕하세요", 10, "안녕하세요"},
		{"Korean at limit", "안녕하세요", 5, "안녕하세요"},
		{"Korean over limit", "안녕하세요 세계", 5, "안녕하세요"},
		{"Korean truncated", "가나다라마바사", 3, "가나다"},
		{"emoji", "👋🌍🚀✨", 2, "👋🌍"},
		{"mixed", "hello 세계!", 7, "hello 세"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := safeTruncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("safeTruncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestSafeTruncateNoEllipsis(t *testing.T) {
	// safeTruncate should NOT add "..." (unlike worker.Truncate)
	got := safeTruncate("hello world", 5)
	if got != "hello" {
		t.Errorf("Expected no ellipsis, got %q", got)
	}
}
