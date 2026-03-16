package main

import (
	"context"
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
}

func main() {
	cfg := loadConfig()
	logger, logFile := setupLogger(cfg.LogFile)
	defer logFile.Close()
	slog.SetDefault(logger)

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

	go bot.PollOutbox(ctx)
	go bot.PollInboxDone(ctx)
	go bot.ProcessRetries(ctx)
	go bot.Listen(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	logger.Info("Received signal, shutting down", "signal", sig)
	cancel()
	bot.Shutdown()
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
