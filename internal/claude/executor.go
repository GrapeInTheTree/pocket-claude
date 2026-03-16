package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/GrapeInTheTree/claude-cowork-telegram/internal/config"
	"github.com/GrapeInTheTree/claude-cowork-telegram/internal/store"
)

type SessionInfo struct {
	ID        string    `json:"id"`
	FirstMsg  string    `json:"first_msg"`
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

func (e *Executor) Execute(ctx context.Context, userMessage string, skipPermissions bool) (*store.CLIResult, error) {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	args := []string{"-p", userMessage, "--output-format", "json"}

	e.mu.Lock()
	sessionID := e.currentSessionID
	model := e.model
	e.mu.Unlock()

	// Always use --resume with explicit session ID (never --continue)
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
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

	var result store.CLIResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		result = store.CLIResult{
			Result: strings.TrimSpace(stdout.String()),
		}
	}

	e.logger.Info("Claude CLI completed",
		"elapsed", elapsed.String(),
		"result_len", len(result.Result),
		"permission_denials", len(result.PermissionDenials),
		"session_id", result.SessionID)

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

	return &result, nil
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
