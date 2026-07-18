package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getio0909/voice-asset-server/internal/account"
	"github.com/getio0909/voice-asset-server/internal/apikey"
	"github.com/getio0909/voice-asset-server/internal/asset"
	"github.com/getio0909/voice-asset-server/internal/audio"
	"github.com/getio0909/voice-asset-server/internal/audit"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/clip"
	"github.com/getio0909/voice-asset-server/internal/glossary"
	"github.com/getio0909/voice-asset-server/internal/hotword"
	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/llmprofile"
	"github.com/getio0909/voice-asset-server/internal/membership"
	"github.com/getio0909/voice-asset-server/internal/notification"
	"github.com/getio0909/voice-asset-server/internal/operations"
	"github.com/getio0909/voice-asset-server/internal/organization"
	"github.com/getio0909/voice-asset-server/internal/platform/config"
	"github.com/getio0909/voice-asset-server/internal/platform/httpapi"
	"github.com/getio0909/voice-asset-server/internal/platform/product"
	"github.com/getio0909/voice-asset-server/internal/platform/telemetry"
	"github.com/getio0909/voice-asset-server/internal/providerprofile"
	"github.com/getio0909/voice-asset-server/internal/realtime"
	"github.com/getio0909/voice-asset-server/internal/review"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/syncchange"
	"github.com/getio0909/voice-asset-server/internal/systemsetting"
	"github.com/getio0909/voice-asset-server/internal/transcript"
	"github.com/getio0909/voice-asset-server/internal/transcriptexport"
	"github.com/getio0909/voice-asset-server/internal/upload"
	"github.com/getio0909/voice-asset-server/internal/waveform"
	"github.com/getio0909/voice-asset-server/internal/webhook"
	"github.com/getio0909/voice-asset-server/internal/workspace"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		slog.Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("voiceasset-api", flag.ContinueOnError)
	flags.SetOutput(output)
	healthcheckURL := flags.String("healthcheck", "", "check a health URL and exit")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		return json.NewEncoder(output).Encode(product.CurrentBuildInfo())
	}
	if *healthcheckURL != "" {
		return checkHealth(*healthcheckURL)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	telemetryShutdown, err := telemetry.Setup(context.Background(), "voiceasset-api", cfg.OTLPEndpoint)
	if err != nil {
		return fmt.Errorf("initialize telemetry: %w", err)
	}
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := telemetryShutdown(shutdownContext); err != nil {
			slog.Error("telemetry shutdown failed", "error", err)
		}
	}()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
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
	authService := auth.NewService(auth.NewPostgresRepository(pool), auth.PasswordHasher{})
	accountService := account.NewService(account.NewPostgresRepository(pool), auth.PasswordHasher{})
	auditService := audit.NewService(audit.NewPostgresRepository(pool))
	apiKeyService := apikey.NewService(apikey.NewPostgresRepository(pool))
	assetService := asset.NewService(asset.NewPostgresRepository(pool))
	syncChangeService := syncchange.NewService(syncchange.NewPostgresRepository(pool))
	notificationService := notification.NewService(notification.NewPostgresRepository(pool))
	organizationService := organization.NewService(organization.NewPostgresRepository(pool))
	operationsService := operations.NewService(operations.NewPostgresRepository(pool))
	membershipService := membership.NewService(membership.NewPostgresRepository(pool))
	workspaceService := workspace.NewService(workspace.NewPostgresRepository(pool))
	objectStorage, err := storage.New(cfg.Storage)
	if err != nil {
		return fmt.Errorf("initialize %s storage: %w", cfg.Storage.Backend, err)
	}
	uploadService := upload.NewService(upload.NewPostgresRepository(pool), objectStorage)
	transcriptRepository := transcript.NewPostgresRepository(pool)
	transcriptService := transcript.NewService(transcriptRepository)
	exportService := transcriptexport.NewService(
		transcriptexport.NewPostgresRepository(pool), transcriptService, objectStorage,
	)
	audioService := audio.NewAccessService(audio.NewPostgresOriginalRepository(pool), objectStorage)
	waveformService := waveform.NewAccessService(waveform.NewPostgresRepository(pool), objectStorage)
	ffmpegClipper, err := clip.NewFFmpegClipper(cfg.FFmpegPath, "")
	if err != nil {
		return fmt.Errorf("initialize audio clipper: %w", err)
	}
	clipService := clip.NewService(clip.NewPostgresRepository(pool), audioService, ffmpegClipper, objectStorage)
	jobService := job.NewService(job.NewPostgresRepository(pool))
	providerProfileService := providerprofile.NewService(providerprofile.NewPostgresRepository(pool), cfg.ProfileCipher)
	hotwordService := hotword.NewService(hotword.NewPostgresRepository(pool))
	llmProfileService := llmprofile.NewService(llmprofile.NewPostgresRepository(pool), cfg.ProfileCipher)
	glossaryService := glossary.NewService(glossary.NewPostgresRepository(pool))
	webhookService := webhook.NewService(webhook.NewPostgresRepository(pool), cfg.ProfileCipher)
	realtimeService := realtime.NewService(realtime.NewPostgresRepository(pool))
	realtimeController := realtime.NewController(realtimeService, realtime.NewStreamHub(realtime.MockStreamFactory{}))
	realtimeEndpoint := httpapi.NewWebSocketRealtimeEndpoint(realtimeController, cfg.PublicOrigin)
	reviewService := review.NewService(review.NewPostgresRepository(pool), transcriptRepository)
	systemSettingService := systemsetting.NewService(systemSettingConfig(cfg))
	applicationHandler := httpapi.NewApplicationHandler(httpapi.Options{
		BrandName: cfg.BrandName, Logger: logger, AuthService: authService,
		AccountService: accountService,
		AuditService:   auditService, APIKeyService: apiKeyService,
		AssetService:         assetService,
		SyncChangeService:    syncChangeService,
		NotificationService:  notificationService,
		OrganizationService:  organizationService,
		OperationsService:    operationsService,
		SystemSettingService: systemSettingService,
		MembershipService:    membershipService,
		WorkspaceService:     workspaceService,
		AudioService:         audioService,
		WaveformService:      waveformService,
		ClipService:          clipService,
		JobService:           jobService,
		CorrectionService:    jobService,
		ReviewService:        reviewService,
		UploadService:        uploadService,
		TranscriptService:    transcriptService,
		ExportService:        exportService,
		ProviderService:      providerProfileService,
		HotwordService:       hotwordService,
		LLMProfileService:    llmProfileService,
		GlossaryService:      glossaryService,
		WebhookService:       webhookService,
		RealtimeEndpoint:     realtimeEndpoint,
		ReadinessCheck:       pool.Ping,
		PublicOrigin:         cfg.PublicOrigin, CookieSecure: cfg.CookieSecure,
	})
	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           telemetry.HTTPMiddleware("voiceasset-api", applicationHandler),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		logger.Info("api listening",
			"address", cfg.HTTPAddress,
			"api_version", product.APIVersion,
			"server_version", product.ServerVersion,
		)
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	logger.Info("api stopped gracefully")
	return nil
}

func systemSettingConfig(runtime config.Config) systemsetting.Config {
	return systemsetting.Config{
		BrandName:                              runtime.BrandName,
		PublicOrigin:                           runtime.PublicOrigin,
		StorageBackend:                         string(runtime.Storage.Backend),
		CookieSecure:                           runtime.CookieSecure,
		ProviderCredentialEncryptionConfigured: runtime.ProfileCipher != nil,
	}
}

func checkHealth(url string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create health request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request health endpoint: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}
