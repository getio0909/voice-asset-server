package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/getio0909/voice-asset-server/internal/artifactreaper"
	"github.com/getio0909/voice-asset-server/internal/asset"
	"github.com/getio0909/voice-asset-server/internal/assetpurge"
	"github.com/getio0909/voice-asset-server/internal/audio"
	"github.com/getio0909/voice-asset-server/internal/correction"
	"github.com/getio0909/voice-asset-server/internal/glossary"
	"github.com/getio0909/voice-asset-server/internal/hotword"
	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/llmprofile"
	"github.com/getio0909/voice-asset-server/internal/platform/config"
	"github.com/getio0909/voice-asset-server/internal/platform/product"
	"github.com/getio0909/voice-asset-server/internal/platform/telemetry"
	"github.com/getio0909/voice-asset-server/internal/providerprofile"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/transcript"
	"github.com/getio0909/voice-asset-server/internal/transcription"
	"github.com/getio0909/voice-asset-server/internal/waveform"
	"github.com/getio0909/voice-asset-server/internal/webhook"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
)

type cycleProcessor interface {
	RunOnce(ctx context.Context) (bool, error)
}

type fairScheduler struct {
	processors []cycleProcessor
	next       int
}

func newFairScheduler(processors ...cycleProcessor) *fairScheduler {
	return &fairScheduler{processors: processors}
}

func (scheduler *fairScheduler) RunOnce(ctx context.Context) (bool, error) {
	if len(scheduler.processors) == 0 {
		return false, nil
	}
	for offset := 0; offset < len(scheduler.processors); offset++ {
		index := (scheduler.next + offset) % len(scheduler.processors)
		processorContext, span := otel.Tracer("voiceasset-worker").Start(ctx, "voiceasset.worker.cycle")
		processed, err := scheduler.processors[index].RunOnce(processorContext)
		span.End()
		if processed || err != nil {
			scheduler.next = (index + 1) % len(scheduler.processors)
			return processed, err
		}
	}
	return false, nil
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(os.Args[1:], logger, os.Stdout); err != nil {
		logger.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

func run(args []string, logger *slog.Logger, output io.Writer) error {
	flags := flag.NewFlagSet("voiceasset-worker", flag.ContinueOnError)
	flags.SetOutput(output)
	once := flags.Bool("once", false, "perform one scheduler cycle and exit")
	heartbeat := flags.Duration("heartbeat", 30*time.Second, "idle scheduler heartbeat")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		return json.NewEncoder(output).Encode(product.CurrentBuildInfo())
	}
	if *heartbeat <= 0 {
		return errors.New("heartbeat must be positive")
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	telemetryShutdown, err := telemetry.Setup(context.Background(), "voiceasset-worker", cfg.OTLPEndpoint)
	if err != nil {
		return fmt.Errorf("initialize telemetry: %w", err)
	}
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := telemetryShutdown(shutdownContext); err != nil {
			logger.Error("telemetry shutdown failed", "error", err)
		}
	}()
	databaseContext, cancelDatabase := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelDatabase()
	pool, err := pgxpool.New(databaseContext, cfg.DatabaseURL)
	if err != nil {
		return errors.New("create database pool")
	}
	defer pool.Close()
	if err := pool.Ping(databaseContext); err != nil {
		return errors.New("connect to database")
	}
	objectStorage, err := storage.New(cfg.Storage)
	if err != nil {
		return fmt.Errorf("initialize %s storage: %w", cfg.Storage.Backend, err)
	}
	providerRepository := providerprofile.NewPostgresRepository(pool)
	jobRepository := job.NewPostgresRepository(pool)
	transcriptionProcessor := transcription.NewProcessorWithResolvers(
		jobRepository,
		asset.NewPostgresRepository(pool),
		transcription.NewOriginalAudioSource(audio.NewPostgresOriginalRepository(pool), objectStorage),
		providerprofile.NewResolver(providerRepository, cfg.ProfileCipher, nil),
		hotword.NewService(hotword.NewPostgresRepository(pool)),
		objectStorage,
		transcription.NewPostgresCommitter(pool),
		workerIdentifier(),
	)
	correctionProcessor := correction.NewProcessor(
		jobRepository,
		transcript.NewPostgresRepository(pool),
		llmprofile.NewResolver(llmprofile.NewPostgresRepository(pool), cfg.ProfileCipher, nil),
		glossary.NewService(glossary.NewPostgresRepository(pool)),
		objectStorage,
		correction.NewPostgresCommitter(pool),
		workerIdentifier(),
	)
	waveformRenderer, err := waveform.NewFFmpegRenderer(cfg.FFmpegPath, "")
	if err != nil {
		return fmt.Errorf("initialize waveform renderer: %w", err)
	}
	waveformProcessor := waveform.NewProcessor(
		jobRepository,
		transcription.NewOriginalAudioSource(waveform.NewPostgresOriginalRepository(pool), objectStorage),
		waveformRenderer,
		objectStorage,
		waveform.NewPostgresCommitter(pool),
		workerIdentifier(),
	)
	expiredArtifactReaper := artifactreaper.New(
		artifactreaper.NewPostgresRepository(pool),
		objectStorage,
	)
	assetPurgeProcessor := assetpurge.NewProcessor(
		jobRepository,
		assetpurge.NewPostgresRepository(pool),
		objectStorage,
		workerIdentifier(),
	)
	processors := []cycleProcessor{
		transcriptionProcessor, correctionProcessor, waveformProcessor,
		expiredArtifactReaper, assetPurgeProcessor,
	}
	if cfg.ProfileCipher != nil {
		processors = append(processors, webhook.NewDeliveryWorker(
			webhook.NewPostgresRepository(pool), cfg.ProfileCipher, nil, workerIdentifier(),
		))
	}
	processor := newFairScheduler(processors...)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runCycle(ctx, logger, processor); err != nil && *once {
		return err
	}
	if *once {
		return nil
	}

	ticker := time.NewTicker(*heartbeat)
	defer ticker.Stop()
	logger.Info("worker scheduler started", "server_version", product.ServerVersion)
	for {
		select {
		case <-ctx.Done():
			logger.Info("worker scheduler stopped gracefully")
			return nil
		case <-ticker.C:
			_ = runCycle(ctx, logger, processor)
		}
	}
}

func runCycle(ctx context.Context, logger *slog.Logger, processor cycleProcessor) error {
	processed, err := processor.RunOnce(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "worker scheduler cycle failed", "error", err)
		return err
	}
	if processed {
		logger.InfoContext(ctx, "worker scheduler cycle completed", "processed_batches", 1)
	} else {
		logger.DebugContext(ctx, "worker scheduler idle", "processed_batches", 0)
	}
	return nil
}

func workerIdentifier() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "worker"
	}
	identifier := fmt.Sprintf("%s-%d", hostname, os.Getpid())
	identifier = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return '-'
		}
		return character
	}, identifier)
	if len(identifier) > 200 {
		identifier = identifier[:200]
	}
	return identifier
}
