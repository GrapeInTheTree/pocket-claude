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

func TestSplitMessage(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		maxLen    int
		wantCount int
	}{
		{"short message", "hello", 4096, 1},
		{"empty", "", 4096, 1},
		{"exact limit", string(make([]rune, 4096)), 4096, 1},
		{"just over limit", string(make([]rune, 4097)), 4096, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := splitMessage(tt.input, tt.maxLen)
			if len(chunks) != tt.wantCount {
				t.Errorf("splitMessage() returned %d chunks, want %d", len(chunks), tt.wantCount)
			}
		})
	}
}

func TestSplitMessagePreservesContent(t *testing.T) {
	// Build a long message with newlines
	var input string
	for i := 0; i < 200; i++ {
		input += "This is line number that is fairly long.\n"
	}

	chunks := splitMessage(input, 100)

	// Reassemble and verify no content is lost
	var reassembled string
	for _, c := range chunks {
		reassembled += c
	}
	if reassembled != input {
		t.Errorf("Content lost during split: input len=%d, reassembled len=%d", len(input), len(reassembled))
	}
}

func TestSplitMessagePrefersNewline(t *testing.T) {
	// 80 chars + newline + 80 chars, with maxLen=100
	line1 := string(make([]rune, 80))
	line2 := string(make([]rune, 80))
	input := line1 + "\n" + line2

	chunks := splitMessage(input, 100)

	if len(chunks) != 2 {
		t.Fatalf("Expected 2 chunks, got %d", len(chunks))
	}
	// First chunk should end at newline (81 runes: 80 + '\n')
	if []rune(chunks[0])[len([]rune(chunks[0]))-1] != '\n' {
		t.Error("Expected first chunk to end with newline")
	}
}

func TestSplitMessageKorean(t *testing.T) {
	// Korean text should split on rune boundaries, not byte boundaries
	var input string
	for i := 0; i < 2000; i++ {
		input += "안녕"
	}
	// 4000 Korean runes

	chunks := splitMessage(input, 3000)

	// Verify each chunk is valid and within limit
	for i, c := range chunks {
		runes := []rune(c)
		if len(runes) > 3000 {
			t.Errorf("Chunk %d has %d runes, exceeds limit 3000", i, len(runes))
		}
	}

	// Verify no content lost
	var reassembled string
	for _, c := range chunks {
		reassembled += c
	}
	if reassembled != input {
		t.Error("Korean content lost during split")
	}
}
