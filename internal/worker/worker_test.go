package worker

import (
	"testing"

	"github.com/GrapeInTheTree/pocket-claude/internal/store"
)

func TestBuildToolSummary(t *testing.T) {
	tests := []struct {
		name    string
		result  *store.CLIResult
		want    string
		wantNot string
	}{
		{
			name:   "empty tools",
			result: &store.CLIResult{ToolSummary: map[string]int{}},
			want:   "",
		},
		{
			name: "single tool once",
			result: &store.CLIResult{
				ToolSummary: map[string]int{"Read": 1},
				DurationMs:  5000,
				TotalCostUSD: 0.01,
			},
			want: "📖Read",
		},
		{
			name: "single tool multiple times",
			result: &store.CLIResult{
				ToolSummary: map[string]int{"Bash": 3},
				DurationMs:  10000,
				TotalCostUSD: 0.05,
			},
			want: "⚡Bash ×3",
		},
		{
			name: "MCP tool",
			result: &store.CLIResult{
				ToolSummary: map[string]int{"mcp__slack__send": 1},
				DurationMs:  1000,
			},
			want: "🔌mcp__slack__send",
		},
		{
			name: "unknown tool",
			result: &store.CLIResult{
				ToolSummary: map[string]int{"CustomTool": 2},
				DurationMs:  2000,
			},
			want: "🔧CustomTool ×2",
		},
		{
			name: "multiple tools sorted",
			result: &store.CLIResult{
				ToolSummary: map[string]int{"Read": 3, "Bash": 2, "Edit": 1},
				DurationMs:  30000,
				TotalCostUSD: 0.1,
			},
			want: "⚡Bash ×2  ✏️Edit  📖Read ×3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildToolSummary(tt.result)
			if tt.want == "" {
				if got != "" {
					t.Errorf("Expected empty, got %q", got)
				}
				return
			}
			if !contains(got, tt.want) {
				t.Errorf("buildToolSummary() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestBuildToolSummaryFormat(t *testing.T) {
	result := &store.CLIResult{
		ToolSummary:  map[string]int{"Read": 1},
		DurationMs:   5000,
		TotalCostUSD: 0.0234,
	}
	got := buildToolSummary(result)

	// Should contain duration
	if !contains(got, "5s") {
		t.Errorf("Expected '5s' in %q", got)
	}
	// Should contain cost
	if !contains(got, "$0.0234") {
		t.Errorf("Expected '$0.0234' in %q", got)
	}
	// Should start with 📋
	runes := []rune(got)
	if len(runes) == 0 || string(runes[0]) != "📋" {
		t.Errorf("Expected to start with 📋, got %q", got)
	}
}

func TestIsRestartError(t *testing.T) {
	tests := []struct {
		err  string
		want bool
	}{
		{"signal: killed", true},
		{"signal: terminated", true},
		{"CLI error: signal: killed", true},
		{"timeout after 10m0s", false},
		{"empty response from Claude CLI", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.err, func(t *testing.T) {
			if got := isRestartError(tt.err); got != tt.want {
				t.Errorf("isRestartError(%q) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
