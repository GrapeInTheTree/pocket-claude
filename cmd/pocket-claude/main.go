package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/GrapeInTheTree/pocket-claude/internal/bot"
	"github.com/GrapeInTheTree/pocket-claude/internal/claude"
	"github.com/GrapeInTheTree/pocket-claude/internal/config"
	"github.com/GrapeInTheTree/pocket-claude/internal/store"
	"github.com/GrapeInTheTree/pocket-claude/internal/worker"
)

func main() {
	cfg := config.Load()
	logger, logFile := config.SetupLogger(cfg.LogFile)
	defer logFile.Close()
	slog.SetDefault(logger)

	// Single instance guard
	pidFile := "./bot.pid"
	if err := config.AcquirePIDFile(pidFile); err != nil {
		logger.Error("Another bot instance is running", "error", err)
		os.Exit(1)
	}
	defer os.Remove(pidFile)

	// Store
	lockPath := strings.TrimSuffix(cfg.InboxPath, ".json") + ".lock"
	st := store.New(cfg.InboxPath, cfg.OutboxPath, lockPath,
		time.Duration(cfg.LockTimeoutMinutes)*time.Minute, logger)

	// Bot
	b, err := bot.New(cfg, st, logger)
	if err != nil {
		logger.Error("Failed to create bot", "error", err)
		os.Exit(1)
	}

	// Claude executor
	exec := claude.NewExecutor(cfg, logger)

	// Worker
	messageTTL := time.Duration(cfg.MessageTTLMinutes) * time.Minute
	w := worker.New(cfg.WorkerQueueSize, cfg.MaxRetryCount, messageTTL,
		exec, st, b.SendMessage, b.SendApprovalRequest, logger)
	b.SetWorker(w)

	// Recover stale messages
	if n := w.RecoverStale(); n > 0 {
		logger.Info("Recovered stale messages", "count", n)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start goroutines
	go w.Start(ctx)
	go w.PollPending(ctx, 30*time.Second)
	go w.ProcessRetries(ctx, 30*time.Second)
	go b.PollOutbox(ctx)
	go b.Listen(ctx)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	logger.Info("Received signal, shutting down", "signal", sig)
	cancel()
	b.Shutdown()
	w.Stop()
	logger.Info("Bot stopped gracefully")
}
