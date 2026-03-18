package worker

import (
	"testing"

	"github.com/GrapeInTheTree/pocket-claude/internal/store"
)

func TestIsRalphComplete(t *testing.T) {
	tests := []struct {
		name   string
		result *store.CLIResult
		want   bool
	}{
		{
			name:   "no tools used",
			result: &store.CLIResult{Result: "All done!", ToolSummary: map[string]int{}},
			want:   true,
		},
		{
			name:   "nil tool summary",
			result: &store.CLIResult{Result: "Done"},
			want:   true,
		},
		{
			name:   "RALPH_DONE signal",
			result: &store.CLIResult{Result: "RALPH_DONE\nAll tests passing", ToolSummary: map[string]int{"Bash": 1}},
			want:   true,
		},
		{
			name:   "tools used, not done",
			result: &store.CLIResult{Result: "Working on it...", ToolSummary: map[string]int{"Read": 2, "Edit": 1}},
			want:   false,
		},
		{
			name:   "tools used, no signal",
			result: &store.CLIResult{Result: "Created 3 test files", ToolSummary: map[string]int{"Write": 3}},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRalphComplete(tt.result); got != tt.want {
				t.Errorf("isRalphComplete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseRalphArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		wantMsg string
		wantMax int
	}{
		{"simple message", "write tests", "write tests", 5},
		{"with max flag", "write tests --max 3", "write tests", 3},
		{"max at end", "analyze code --max 10", "analyze code", 10},
		{"max over limit", "task --max 50", "task", 5},
		{"max zero", "task --max 0", "task", 5},
		{"max negative", "task --max -1", "task", 5},
		{"max invalid", "task --max abc", "task", 5},
		{"no max flag", "do everything perfectly", "do everything perfectly", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, maxIter := ParseRalphArgs(tt.args)
			if msg != tt.wantMsg {
				t.Errorf("message = %q, want %q", msg, tt.wantMsg)
			}
			if maxIter != tt.wantMax {
				t.Errorf("maxIter = %d, want %d", maxIter, tt.wantMax)
			}
		})
	}
}

func TestAbs(t *testing.T) {
	if abs(5) != 5 {
		t.Error("abs(5) != 5")
	}
	if abs(-5) != 5 {
		t.Error("abs(-5) != 5")
	}
	if abs(0) != 0 {
		t.Error("abs(0) != 0")
	}
}
