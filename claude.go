package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

type ClaudeExecutor struct {
	cliPath      string
	workDir      string
	timeout      time.Duration
	systemPrompt string
	model        string
	logger       *slog.Logger
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

func (c *ClaudeExecutor) Execute(ctx context.Context, userMessage string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	args := []string{"-p", userMessage}

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
		"timeout", c.timeout.String())

	start := time.Now()

	if err := cmd.Run(); err != nil {
		elapsed := time.Since(start)
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("timeout after %s", c.timeout)
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		c.logger.Error("Claude CLI failed",
			"elapsed", elapsed.String(),
			"error", errMsg)
		return "", fmt.Errorf("CLI error: %s", errMsg)
	}

	result := strings.TrimSpace(stdout.String())
	elapsed := time.Since(start)

	c.logger.Info("Claude CLI completed",
		"elapsed", elapsed.String(),
		"result_len", len(result))

	if result == "" {
		return "", fmt.Errorf("empty response from Claude CLI")
	}

	return result, nil
}
