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

type ClaudeExecutor struct {
	cliPath      string
	workDir      string
	timeout      time.Duration
	systemPrompt string
	model        string
	logger       *slog.Logger

	mu          sync.Mutex
	hasSession  bool // true after at least one successful execution
}

func NewClaudeExecutor(cfg Config, logger *slog.Logger) *ClaudeExecutor {
	workDir := cfg.CLIWorkDir
	if workDir == "" {
		workDir = "."
	}
	return &ClaudeExecutor{
		cliPath:      cfg.CLIPath,
		workDir:      workDir,
		timeout:      time.Duration(cfg.CLITimeoutSec) * time.Second,
		systemPrompt: cfg.CLISystemPrompt,
		model:        cfg.CLIModel,
		logger:       logger,
	}
}

// Execute runs the Claude CLI. Uses --continue after the first call to maintain session.
func (c *ClaudeExecutor) Execute(ctx context.Context, userMessage string, skipPermissions bool) (*CLIResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	args := []string{"-p", userMessage, "--output-format", "json"}

	// Continue session after first successful execution
	c.mu.Lock()
	shouldContinue := c.hasSession
	c.mu.Unlock()

	if shouldContinue {
		args = append(args, "--continue")
	}

	if skipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}
	if c.systemPrompt != "" {
		args = append(args, "--system-prompt", c.systemPrompt)
	}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}

	cmd := exec.CommandContext(ctx, c.cliPath, args...)
	cmd.Dir = c.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	c.logger.Info("Claude CLI executing",
		"prompt", truncate(userMessage, 80),
		"continue", shouldContinue,
		"skip_permissions", skipPermissions)

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

	// Mark session as active
	c.mu.Lock()
	c.hasSession = true
	c.mu.Unlock()

	return &result, nil
}

// ResetSession clears the session so the next call starts fresh.
func (c *ClaudeExecutor) ResetSession() {
	c.mu.Lock()
	c.hasSession = false
	c.mu.Unlock()
	c.logger.Info("Session reset, next message will start a new conversation")
}
