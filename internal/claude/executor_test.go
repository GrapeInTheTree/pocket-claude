package claude

import (
	"testing"
)

func TestParseStreamJSON(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantResult string
		wantSID    string
		wantTools  map[string]int
		wantCost   float64
	}{
		{
			name: "single result event",
			input: `{"type":"result","result":"Hello world","session_id":"sess_123","total_cost_usd":0.01,"duration_ms":500}`,
			wantResult: "Hello world",
			wantSID:    "sess_123",
			wantCost:   0.01,
		},
		{
			name: "stream with assistant tool_use and result",
			input: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"},{"type":"tool_use","name":"Read"},{"type":"tool_use","name":"Bash"}]}}
{"type":"result","result":"Done reading files","session_id":"sess_456","total_cost_usd":0.05}`,
			wantResult: "Done reading files",
			wantSID:    "sess_456",
			wantTools:  map[string]int{"Read": 2, "Bash": 1},
			wantCost:   0.05,
		},
		{
			name: "multiple assistant events accumulate tools",
			input: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Grep"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Grep"},{"type":"tool_use","name":"Edit"}]}}
{"type":"result","result":"Code updated","session_id":"sess_789"}`,
			wantResult: "Code updated",
			wantSID:    "sess_789",
			wantTools:  map[string]int{"Grep": 2, "Edit": 1},
		},
		{
			name:  "empty input",
			input: "",
			wantResult: "",
			wantSID:    "",
		},
		{
			name:  "malformed JSON lines",
			input: "not json\n{bad\n",
			wantResult: "",
			wantSID:    "",
		},
		{
			name: "result with permission denials",
			input: `{"type":"result","result":"I need permission","session_id":"sess_perm","permission_denials":[{"tool_name":"Bash","tool_input":{"command":"ls"}}]}`,
			wantResult: "I need permission",
			wantSID:    "sess_perm",
		},
		{
			name: "fallback single JSON (no type field)",
			input: `{"result":"fallback response","session_id":"sess_fb","total_cost_usd":0.001}`,
			wantResult: "fallback response",
			wantSID:    "sess_fb",
			wantCost:   0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseStreamJSON([]byte(tt.input))

			if result.Result != tt.wantResult {
				t.Errorf("Result = %q, want %q", result.Result, tt.wantResult)
			}
			if result.SessionID != tt.wantSID {
				t.Errorf("SessionID = %q, want %q", result.SessionID, tt.wantSID)
			}
			if tt.wantCost > 0 && result.TotalCostUSD != tt.wantCost {
				t.Errorf("TotalCostUSD = %f, want %f", result.TotalCostUSD, tt.wantCost)
			}
			if tt.wantTools != nil {
				for tool, count := range tt.wantTools {
					if result.ToolSummary[tool] != count {
						t.Errorf("ToolSummary[%q] = %d, want %d", tool, result.ToolSummary[tool], count)
					}
				}
			}
		})
	}
}

func TestParseStreamJSONPermissionDenials(t *testing.T) {
	input := `{"type":"result","result":"Need permission","session_id":"s1","permission_denials":[{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}},{"tool_name":"Write","tool_input":{"file_path":"/etc/passwd"}}]}`

	result := parseStreamJSON([]byte(input))

	if len(result.PermissionDenials) != 2 {
		t.Fatalf("Expected 2 permission denials, got %d", len(result.PermissionDenials))
	}
	if result.PermissionDenials[0].ToolName != "Bash" {
		t.Errorf("First denial tool = %q, want 'Bash'", result.PermissionDenials[0].ToolName)
	}
	if result.PermissionDenials[1].ToolName != "Write" {
		t.Errorf("Second denial tool = %q, want 'Write'", result.PermissionDenials[1].ToolName)
	}
}

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"ASCII short", "hello", 10, "hello"},
		{"ASCII exact", "hello", 5, "hello"},
		{"ASCII over", "hello world", 5, "hello..."},
		{"Korean", "안녕하세요", 3, "안녕하..."},
		{"emoji", "🚀🌍👋", 2, "🚀🌍..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
