package config

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
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
	MessageTTLMinutes     int

	// Claude CLI
	CLIPath         string
	CLIWorkDir      string
	CLITimeoutSec   int
	CLISystemPrompt string
	CLIModel        string
	CLIAddDirs      string
	WorkerQueueSize int
	ProjectsFile    string
}

func Load() Config {
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
		MessageTTLMinutes:     envIntOrDefault("MESSAGE_TTL_MINUTES", 10),

		CLIPath:         envOrDefault("CLAUDE_CLI_PATH", "claude"),
		CLIWorkDir:      envOrDefault("CLAUDE_WORK_DIR", "."),
		CLITimeoutSec:   envIntOrDefault("CLAUDE_TIMEOUT_SECONDS", 120),
		CLISystemPrompt: os.Getenv("CLAUDE_SYSTEM_PROMPT"),
		CLIModel:        os.Getenv("CLAUDE_MODEL"),
		CLIAddDirs:      envOrDefault("CLAUDE_ADD_DIRS", "~"),
		WorkerQueueSize: envIntOrDefault("WORKER_QUEUE_SIZE", 100),
		ProjectsFile:    envOrDefault("PROJECTS_FILE", "./projects.json"),
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

func SetupLogger(logFile string) (*slog.Logger, *os.File) {
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		slog.Error("Failed to open log file", "error", err)
		os.Exit(1)
	}

	w := io.MultiWriter(os.Stdout, f)
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(handler), f
}

// AcquirePIDFile ensures only one bot instance runs at a time.
func AcquirePIDFile(path string) error {
	if data, err := os.ReadFile(path); err == nil {
		var pid int
		if _, err := fmt.Sscanf(string(data), "%d", &pid); err == nil {
			if proc, err := os.FindProcess(pid); err == nil {
				if err := proc.Signal(syscall.Signal(0)); err == nil {
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

	return os.WriteFile(path, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
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
