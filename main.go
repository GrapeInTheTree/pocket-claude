package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	TelegramToken         string
	TelegramChatID        int64
	InboxPath             string
	OutboxPath            string
	LockTimeoutMinutes    int
	MaxRetryCount         int
	OutboxPollIntervalSec int
	LogFile               string

	// Claude CLI
	CLIPath         string
	CLIWorkDir      string
	CLITimeoutSec   int
	CLISystemPrompt string
	CLIModel        string
	CLIAddDirs      string
	WorkerQueueSize int
}

func main() {
	cfg := loadConfig()
	logger, logFile := setupLogger(cfg.LogFile)
	defer logFile.Close()
	slog.SetDefault(logger)

	// Ensure single instance via PID file
	pidFile := "./bot.pid"
	if err := acquirePIDFile(pidFile); err != nil {
		logger.Error("Another bot instance is running", "error", err)
		os.Exit(1)
	}
	defer os.Remove(pidFile)

	lockPath := strings.TrimSuffix(cfg.InboxPath, ".json") + ".lock"
	store := NewStore(cfg.InboxPath, cfg.OutboxPath, lockPath,
		time.Duration(cfg.LockTimeoutMinutes)*time.Minute, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bot, err := NewBot(cfg, store, logger)
	if err != nil {
		logger.Error("Failed to create bot", "error", err)
		os.Exit(1)
	}

	// Claude CLI executor + worker
	claude := NewClaudeExecutor(cfg, logger)
	worker := NewWorker(cfg.WorkerQueueSize, claude, store, bot.sendMessage, bot.sendApprovalRequest, logger)
	bot.SetWorker(worker)

	// Recover interrupted messages from previous run
	if n := worker.RecoverStale(); n > 0 {
		logger.Info("Recovered stale messages", "count", n)
	}

	go worker.Start(ctx)
	go worker.PollPending(ctx, 30*time.Second)
	go bot.PollOutbox(ctx)
	go bot.ProcessRetries(ctx)
	go bot.Listen(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	logger.Info("Received signal, shutting down", "signal", sig)
	cancel()
	bot.Shutdown()
	worker.Stop()
	logger.Info("Bot stopped gracefully")
}

func loadConfig() Config {
	if err := godotenv.Load(); err != nil {
		slog.Warn(".env file not found, using environment variables")
	}

	cfg := Config{
		TelegramToken:         mustEnv("TELEGRAM_TOKEN"),
		InboxPath:             envOrDefault("INBOX_PATH", "./inbox.json"),
		OutboxPath:            envOrDefault("OUTBOX_PATH", "./outbox.json"),
		LockTimeoutMinutes:    envIntOrDefault("LOCK_TIMEOUT_MINUTES", 5),
		MaxRetryCount:         envIntOrDefault("MAX_RETRY_COUNT", 3),
		OutboxPollIntervalSec: envIntOrDefault("OUTBOX_POLL_INTERVAL_SECONDS", 10),
		LogFile:               envOrDefault("LOG_FILE", "./bot.log"),

		CLIPath:         envOrDefault("CLAUDE_CLI_PATH", "claude"),
		CLIWorkDir:      envOrDefault("CLAUDE_WORK_DIR", "."),
		CLITimeoutSec:   envIntOrDefault("CLAUDE_TIMEOUT_SECONDS", 120),
		CLISystemPrompt: os.Getenv("CLAUDE_SYSTEM_PROMPT"),
		CLIModel:        os.Getenv("CLAUDE_MODEL"),
		CLIAddDirs:      envOrDefault("CLAUDE_ADD_DIRS", "~"),
		WorkerQueueSize: envIntOrDefault("WORKER_QUEUE_SIZE", 100),
	}

	chatIDStr := mustEnv("TELEGRAM_CHAT_ID")
	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		slog.Error("Invalid TELEGRAM_CHAT_ID", "error", err)
		os.Exit(1)
	}
	cfg.TelegramChatID = chatID

	return cfg
}

func mustEnv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		slog.Error("Required environment variable not set", "key", key)
		os.Exit(1)
	}
	return val
}

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func envIntOrDefault(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

func acquirePIDFile(path string) error {
	// Check if PID file exists and process is still alive
	if data, err := os.ReadFile(path); err == nil {
		var pid int
		if _, err := fmt.Sscanf(string(data), "%d", &pid); err == nil {
			// Check if process is still running
			if proc, err := os.FindProcess(pid); err == nil {
				if err := proc.Signal(syscall.Signal(0)); err == nil {
					// Process is alive — kill it
					slog.Warn("Killing existing bot instance", "pid", pid)
					proc.Signal(syscall.SIGTERM)
					time.Sleep(2 * time.Second)
					proc.Kill()
					time.Sleep(1 * time.Second)
				}
			}
		}
		os.Remove(path)
	}

	// Write our PID
	return os.WriteFile(path, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
}

func setupLogger(logFile string) (*slog.Logger, *os.File) {
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		slog.Error("Failed to open log file", "error", err)
		os.Exit(1)
	}

	w := io.MultiWriter(os.Stdout, f)
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(handler), f
}
