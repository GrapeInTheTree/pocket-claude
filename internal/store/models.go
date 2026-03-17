package store

import "time"

const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusDone       = "done"
	StatusSent       = "sent"
	StatusError      = "error"
	StatusFailed     = "failed"  // max retries exceeded, permanent
	StatusExpired    = "expired" // TTL exceeded

	DefaultMessageTTL = 10 * time.Minute
)

type InboxMessage struct {
	ID                string `json:"id"`
	Text              string `json:"text"`
	Status            string `json:"status"`
	Timestamp         string `json:"timestamp"`
	RetryCount        int    `json:"retry_count"`
	LastError         string `json:"last_error,omitempty"`
	TelegramMessageID int    `json:"telegram_message_id,omitempty"`
	Result            string `json:"result,omitempty"`
	Project           string `json:"project,omitempty"`
}

// Age returns the duration since the message was created.
func (m *InboxMessage) Age() time.Duration {
	t, err := time.Parse(time.RFC3339, m.Timestamp)
	if err != nil {
		return 0
	}
	return time.Since(t)
}

type InboxFile struct {
	Messages []InboxMessage `json:"messages"`
}

type OutboxMessage struct {
	ID        string `json:"id"`
	Text      string `json:"text,omitempty"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
	Result    string `json:"result,omitempty"`
}

type OutboxFile struct {
	Messages []OutboxMessage `json:"messages"`
}

type LockInfo struct {
	PID       int    `json:"pid"`
	Timestamp string `json:"timestamp"`
}

// CLIResult represents the JSON output from Claude CLI.
type CLIResult struct {
	Type              string             `json:"type"`
	Result            string             `json:"result"`
	IsError           bool               `json:"is_error"`
	SessionID         string             `json:"session_id"`
	PermissionDenials []PermissionDenial `json:"permission_denials"`
	TotalCostUSD      float64            `json:"total_cost_usd"`
	DurationMs        int                `json:"duration_ms"`
	NumTurns          int                `json:"num_turns"`
}

type PermissionDenial struct {
	ToolName  string                 `json:"tool_name"`
	ToolInput map[string]interface{} `json:"tool_input,omitempty"`
}
