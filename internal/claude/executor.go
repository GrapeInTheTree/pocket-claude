package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/GrapeInTheTree/pocket-claude/internal/config"
	"github.com/GrapeInTheTree/pocket-claude/internal/store"
)

type SessionInfo struct {
	ID        string    `json:"id"`
	FirstMsg  string    `json:"first_msg"`
	Name      string    `json:"name,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

const maxSessions = 10

type Executor struct {
	cliPath      string
	workDir      string
	timeout      time.Duration
	systemPrompt string
	addDirs      []string
	logger       *slog.Logger

	mu               sync.Mutex
	currentSessionID string // explicit session ID — never uses --continue
	sessionName      string // display name for current session, passed via --name
	model            string
	sessions         []SessionInfo
}

func NewExecutor(cfg config.Config, logger *slog.Logger) *Executor {
	workDir := cfg.CLIWorkDir
	if workDir == "" {
		workDir = "."
	}

	var addDirs []string
	if cfg.CLIAddDirs != "" {
		for _, d := range strings.Split(cfg.CLIAddDirs, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				addDirs = append(addDirs, d)
			}
		}
	}

	return &Executor{
		cliPath:      cfg.CLIPath,
		workDir:      workDir,
		timeout:      time.Duration(cfg.CLITimeoutSec) * time.Second,
		systemPrompt: cfg.CLISystemPrompt,
		model:        cfg.CLIModel,
		addDirs:      addDirs,
		logger:       logger,
	}
}

// NewProjectExecutor creates an Executor for a specific project directory.
func NewProjectExecutor(cliPath, workDir string, addDirs []string, timeout time.Duration, systemPrompt, model string, logger *slog.Logger) *Executor {
	if workDir == "" {
		workDir = "."
	}
	return &Executor{
		cliPath:      cliPath,
		workDir:      workDir,
		timeout:      timeout,
		systemPrompt: systemPrompt,
		model:        model,
		addDirs:      addDirs,
		logger:       logger,
	}
}

func (e *Executor) Execute(ctx context.Context, userMessage string, skipPermissions bool) (*store.CLIResult, error) {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	args := []string{"-p", userMessage, "--output-format", "stream-json", "--verbose"}

	e.mu.Lock()
	sessionID := e.currentSessionID
	model := e.model
	name := e.sessionName
	e.mu.Unlock()

	// Always use --resume with explicit session ID (never --continue)
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	if name != "" {
		args = append(args, "--name", name)
	}

	if skipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}
	if e.systemPrompt != "" {
		args = append(args, "--system-prompt", e.systemPrompt)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	for _, dir := range e.addDirs {
		args = append(args, "--add-dir", dir)
	}

	cmd := exec.CommandContext(ctx, e.cliPath, args...)
	cmd.Dir = e.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	e.logger.Info("Claude CLI executing",
		"prompt", truncate(userMessage, 80),
		"session", sessionID,
		"skip_permissions", skipPermissions,
		"model", model)

	start := time.Now()

	if err := cmd.Run(); err != nil {
		elapsed := time.Since(start)
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("timeout after %s", e.timeout)
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		e.logger.Error("Claude CLI failed", "elapsed", elapsed.String(), "error", errMsg)
		return nil, fmt.Errorf("CLI error: %s", errMsg)
	}

	elapsed := time.Since(start)

	result := parseStreamJSON(stdout.Bytes())

	e.logger.Info("Claude CLI completed",
		"elapsed", elapsed.String(),
		"result_len", len(result.Result),
		"permission_denials", len(result.PermissionDenials),
		"session_id", result.SessionID,
		"tools_used", len(result.ToolSummary))

	if result.Result == "" && !result.IsError {
		return nil, fmt.Errorf("empty response from Claude CLI")
	}

	// Track session: always use the returned session ID for next call
	if result.SessionID != "" {
		e.mu.Lock()
		e.currentSessionID = result.SessionID
		e.trackSession(result.SessionID, userMessage)
		e.mu.Unlock()
	}

	return result, nil
}

func (e *Executor) trackSession(id, firstMsg string) {
	for i := range e.sessions {
		if e.sessions[i].ID == id {
			return
		}
	}
	e.sessions = append(e.sessions, SessionInfo{
		ID:        id,
		FirstMsg:  truncate(firstMsg, 50),
		Timestamp: time.Now(),
	})
	if len(e.sessions) > maxSessions {
		e.sessions = e.sessions[len(e.sessions)-maxSessions:]
	}
}

// ResetSession clears the current session so next message starts fresh.
func (e *Executor) ResetSession() {
	e.mu.Lock()
	e.currentSessionID = ""
	e.mu.Unlock()
	e.logger.Info("Session reset")
}

// SetResumeID switches to a specific session.
func (e *Executor) SetResumeID(id string) {
	e.mu.Lock()
	e.currentSessionID = id
	e.mu.Unlock()
	e.logger.Info("Switched to session", "session_id", id)
}

func (e *Executor) GetSessions() []SessionInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]SessionInfo, len(e.sessions))
	copy(result, e.sessions)
	return result
}

func (e *Executor) SetSessionName(name string) {
	e.mu.Lock()
	e.sessionName = name
	// Update the session list too
	for i := range e.sessions {
		if e.sessions[i].ID == e.currentSessionID {
			e.sessions[i].Name = name
			break
		}
	}
	e.mu.Unlock()
	e.logger.Info("Session renamed", "name", name)
}

func (e *Executor) GetCurrentSessionID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.currentSessionID
}

func (e *Executor) SetModel(model string) {
	e.mu.Lock()
	e.model = model
	e.mu.Unlock()
}

func (e *Executor) GetModel() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.model
}

// parseStreamJSON parses stream-json output from Claude CLI.
// Extracts the final result and counts tool usage from assistant message events.
func parseStreamJSON(data []byte) *store.CLIResult {
	var result store.CLIResult
	toolCounts := make(map[string]int)

	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Increase buffer for large responses
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "assistant":
			// Extract tool_use from message.content
			msg, _ := event["message"].(map[string]any)
			if msg == nil {
				continue
			}
			content, _ := msg["content"].([]any)
			for _, c := range content {
				block, _ := c.(map[string]any)
				if block == nil {
					continue
				}
				if block["type"] == "tool_use" {
					if name, ok := block["name"].(string); ok {
						toolCounts[name]++
					}
				}
			}

		case "result":
			// Final result event — parse into CLIResult
			json.Unmarshal(line, &result)
		}
	}

	// Fallback: if no result event found, try parsing entire output as single JSON
	if result.Result == "" && result.SessionID == "" {
		json.Unmarshal(data, &result)
	}

	result.ToolSummary = toolCounts
	return &result
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
