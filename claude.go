package main

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
)

type SessionInfo struct {
	ID        string    `json:"id"`
	FirstMsg  string    `json:"first_msg"`
	Timestamp time.Time `json:"timestamp"`
}

type ClaudeExecutor struct {
	cliPath      string
	workDir      string
	timeout      time.Duration
	systemPrompt string
	addDirs      []string
	logger       *slog.Logger

	mu         sync.Mutex
	hasSession bool
	model      string
	resumeID   string           // if set, use --resume instead of --continue
	sessions   []SessionInfo    // recent sessions (max 10)
}

const maxSessions = 10

func NewClaudeExecutor(cfg Config, logger *slog.Logger) *ClaudeExecutor {
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

	return &ClaudeExecutor{
		cliPath:      cfg.CLIPath,
		workDir:      workDir,
		timeout:      time.Duration(cfg.CLITimeoutSec) * time.Second,
		systemPrompt: cfg.CLISystemPrompt,
		model:        cfg.CLIModel,
		addDirs:      addDirs,
		logger:       logger,
	}
}

func (c *ClaudeExecutor) Execute(ctx context.Context, userMessage string, skipPermissions bool) (*CLIResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	args := []string{"-p", userMessage, "--output-format", "json"}

	c.mu.Lock()
	shouldContinue := c.hasSession
	model := c.model
	resumeID := c.resumeID
	c.resumeID = "" // consume resume ID
	c.mu.Unlock()

	if resumeID != "" {
		args = append(args, "--resume", resumeID)
	} else if shouldContinue {
		args = append(args, "--continue")
	}

	if skipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}
	if c.systemPrompt != "" {
		args = append(args, "--system-prompt", c.systemPrompt)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	for _, dir := range c.addDirs {
		args = append(args, "--add-dir", dir)
	}

	cmd := exec.CommandContext(ctx, c.cliPath, args...)
	cmd.Dir = c.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	c.logger.Info("Claude CLI executing",
		"prompt", truncate(userMessage, 80),
		"continue", shouldContinue,
		"resume", resumeID,
		"skip_permissions", skipPermissions,
		"model", model)

	start := time.Now()

	if err := cmd.Run(); err != nil {
		elapsed := time.Since(start)
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("timeout after %s", c.timeout)
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		c.logger.Error("Claude CLI failed", "elapsed", elapsed.String(), "error", errMsg)
		return nil, fmt.Errorf("CLI error: %s", errMsg)
	}

	elapsed := time.Since(start)

	var result CLIResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		result = CLIResult{
			Result: strings.TrimSpace(stdout.String()),
		}
	}

	c.logger.Info("Claude CLI completed",
		"elapsed", elapsed.String(),
		"result_len", len(result.Result),
		"permission_denials", len(result.PermissionDenials),
		"session_id", result.SessionID)

	if result.Result == "" && !result.IsError {
		return nil, fmt.Errorf("empty response from Claude CLI")
	}

	// Track session
	c.mu.Lock()
	c.hasSession = true
	if result.SessionID != "" {
		c.trackSession(result.SessionID, userMessage)
	}
	c.mu.Unlock()

	return &result, nil
}

func (c *ClaudeExecutor) trackSession(id, firstMsg string) {
	// Update existing or add new
	for i := range c.sessions {
		if c.sessions[i].ID == id {
			return // already tracked
		}
	}

	c.sessions = append(c.sessions, SessionInfo{
		ID:        id,
		FirstMsg:  truncate(firstMsg, 50),
		Timestamp: time.Now(),
	})

	// Keep only last N
	if len(c.sessions) > maxSessions {
		c.sessions = c.sessions[len(c.sessions)-maxSessions:]
	}
}

func (c *ClaudeExecutor) ResetSession() {
	c.mu.Lock()
	c.hasSession = false
	c.mu.Unlock()
	c.logger.Info("Session reset")
}

func (c *ClaudeExecutor) SetResumeID(id string) {
	c.mu.Lock()
	c.resumeID = id
	c.hasSession = true // so we don't start fresh
	c.mu.Unlock()
	c.logger.Info("Resume session set", "session_id", id)
}

func (c *ClaudeExecutor) GetSessions() []SessionInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]SessionInfo, len(c.sessions))
	copy(result, c.sessions)
	return result
}

func (c *ClaudeExecutor) SetModel(model string) {
	c.mu.Lock()
	c.model = model
	c.mu.Unlock()
	c.logger.Info("Model changed", "model", model)
}

func (c *ClaudeExecutor) GetModel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.model
}
