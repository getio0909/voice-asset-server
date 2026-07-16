package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/product"
)

func main() {
	flags := flag.NewFlagSet("voiceasset-worker", flag.ExitOnError)
	once := flags.Bool("once", false, "perform one scheduler cycle and exit")
	heartbeat := flags.Duration("heartbeat", 30*time.Second, "idle scheduler heartbeat")
	flags.Parse(os.Args[1:])

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	runCycle(logger)
	if *once {
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(*heartbeat)
	defer ticker.Stop()
	logger.Info("worker scheduler started", "server_version", product.ServerVersion)

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker scheduler stopped gracefully")
			return
		case <-ticker.C:
			runCycle(logger)
		}
	}
}

func runCycle(logger *slog.Logger) {
	// Phase 0 has no registered job handlers; reporting the scheduler state is
	// intentionally observable and keeps the process lifecycle production-safe.
	logger.Info("worker scheduler cycle", "registered_handlers", 0)
}
