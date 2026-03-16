package main

const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusDone       = "done"
	StatusSent       = "sent"
	StatusError      = "error"
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

// Claude CLI JSON output
type CLIResult struct {
	Type              string            `json:"type"`
	Result            string            `json:"result"`
	IsError           bool              `json:"is_error"`
	SessionID         string            `json:"session_id"`
	PermissionDenials []PermissionDenial `json:"permission_denials"`
}

type PermissionDenial struct {
	ToolName  string                 `json:"tool_name"`
	ToolInput map[string]interface{} `json:"tool_input,omitempty"`
}
