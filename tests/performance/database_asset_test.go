package performance_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asset"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	databaseSeedAssetCount = 5_000
	databaseCreateCount    = 100
	databaseReadCount      = 400
	databaseConcurrency    = 8
	databaseCreateP95      = 1500 * time.Millisecond
	databaseReadP95        = 750 * time.Millisecond
	databaseCreateMinRPS   = 5.0
	databaseReadMinRPS     = 20.0
)

type operationMetrics struct {
	throughput float64
	p50        time.Duration
	p95        time.Duration
	p99        time.Duration
	maximum    time.Duration
}

func TestRunConcurrentOperationsCompletesEveryJob(t *testing.T) {
	var calls atomic.Int64
	samples := runConcurrentOperations(context.Background(), 32, 4, func(_ int) error {
		calls.Add(1)
		return nil
	})
	if len(samples) != 32 || calls.Load() != 32 {
		t.Fatalf("runConcurrentOperations() = %d samples/%d calls, want 32/32", len(samples), calls.Load())
	}
	for index, sample := range samples {
		if sample.err != nil {
			t.Fatalf("sample %d error: %v", index, sample.err)
		}
	}
}

func TestDatabaseAssetPerformance(t *testing.T) {
	if !dataPerformanceEnabled() {
		t.Skip("set VOICEASSET_DATA_PERF=1 for the isolated database/asset performance test")
	}

	pool := migratedPerformancePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	authRepository := auth.NewPostgresRepository(pool)
	principal, err := auth.NewBootstrapService(authRepository, auth.PasswordHasher{}).CreateOwner(ctx, auth.OwnerInput{
		Email: "performance-owner@example.test", Password: "performance-test-password",
		WorkspaceName: "Performance Test",
	})
	if err != nil {
		t.Fatalf("bootstrap performance owner: %v", err)
	}
	seedPerformanceAssets(t, ctx, pool, principal, databaseSeedAssetCount)

	service := asset.NewService(asset.NewPostgresRepository(pool))
	createStarted := time.Now()
	createSamples := runConcurrentOperations(ctx, databaseCreateCount, databaseConcurrency, func(index int) error {
		created, replayed, createErr := service.Create(ctx, principal, asset.CreateInput{
			Title: fmt.Sprintf("Measured asset %04d", index), Language: "en-US",
		}, fmt.Sprintf("performance-create-%04d", index))
		if createErr != nil {
			return createErr
		}
		if replayed || created.Title == "" {
			return fmt.Errorf("unexpected asset create result: replayed=%t title=%q", replayed, created.Title)
		}
		return nil
	})
	createMetrics := requireOperationBudget(
		t, "asset create/audit", createSamples, databaseCreateCount, time.Since(createStarted),
		databaseCreateP95, databaseCreateMinRPS,
	)

	readStarted := time.Now()
	readSamples := runConcurrentOperations(ctx, databaseReadCount, databaseConcurrency, func(index int) error {
		input := asset.ListInput{Limit: 25}
		if index%2 == 1 {
			input.Query = "performance needle"
		}
		result, listErr := service.List(ctx, principal, input)
		if listErr != nil {
			return listErr
		}
		if len(result.Items) != 25 {
			return fmt.Errorf("asset list returned %d items, want 25", len(result.Items))
		}
		return nil
	})
	readMetrics := requireOperationBudget(
		t, "asset list/search", readSamples, databaseReadCount, time.Since(readStarted),
		databaseReadP95, databaseReadMinRPS,
	)

	t.Logf(
		"seed_assets=%d concurrency=%d create={requests:%d throughput:%.1f_req/s p50:%s p95:%s p99:%s max:%s} read={requests:%d throughput:%.1f_req/s p50:%s p95:%s p99:%s max:%s}",
		databaseSeedAssetCount, databaseConcurrency,
		databaseCreateCount, createMetrics.throughput,
		createMetrics.p50.Round(time.Microsecond), createMetrics.p95.Round(time.Microsecond),
		createMetrics.p99.Round(time.Microsecond), createMetrics.maximum.Round(time.Microsecond),
		databaseReadCount, readMetrics.throughput,
		readMetrics.p50.Round(time.Microsecond), readMetrics.p95.Round(time.Microsecond),
		readMetrics.p99.Round(time.Microsecond), readMetrics.maximum.Round(time.Microsecond),
	)
}

func dataPerformanceEnabled() bool {
	return os.Getenv("VOICEASSET_DATA_PERF") == "1"
}

func runConcurrentOperations(
	ctx context.Context,
	operationCount,
	concurrency int,
	operation func(int) error,
) []requestSample {
	jobs := make(chan int)
	results := make(chan requestSample, operationCount)
	done := make(chan struct{})
	for range concurrency {
		go func() {
			defer func() { done <- struct{}{} }()
			for index := range jobs {
				started := time.Now()
				err := operation(index)
				results <- requestSample{duration: time.Since(started), err: err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := 0; index < operationCount; index++ {
			select {
			case jobs <- index:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		for range concurrency {
			<-done
		}
		close(results)
	}()

	samples := make([]requestSample, 0, operationCount)
	for sample := range results {
		samples = append(samples, sample)
	}
	return samples
}

func requireOperationBudget(
	t *testing.T,
	name string,
	samples []requestSample,
	expectedCount int,
	elapsed time.Duration,
	p95Budget time.Duration,
	minimumRPS float64,
) operationMetrics {
	t.Helper()
	if len(samples) != expectedCount {
		t.Fatalf("%s produced %d samples, want %d", name, len(samples), expectedCount)
	}
	durations := make([]time.Duration, 0, len(samples))
	var firstFailure error
	for _, sample := range samples {
		if sample.err != nil {
			if firstFailure == nil {
				firstFailure = sample.err
			}
			continue
		}
		durations = append(durations, sample.duration)
	}
	if len(durations) != len(samples) {
		t.Fatalf("%s failed: count=%d first=%v", name, len(samples)-len(durations), firstFailure)
	}
	sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
	metrics := operationMetrics{
		throughput: float64(len(durations)) / elapsed.Seconds(),
		p50:        durationPercentile(durations, 0.50),
		p95:        durationPercentile(durations, 0.95),
		p99:        durationPercentile(durations, 0.99),
		maximum:    durations[len(durations)-1],
	}
	if metrics.p95 > p95Budget {
		t.Fatalf("%s p95 latency = %s, budget = %s", name, metrics.p95, p95Budget)
	}
	if metrics.throughput < minimumRPS {
		t.Fatalf("%s throughput = %.1f req/s, minimum = %.1f req/s", name, metrics.throughput, minimumRPS)
	}
	return metrics
}

func seedPerformanceAssets(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	principal auth.Principal,
	count int,
) {
	t.Helper()
	rows := make([][]any, 0, count)
	for index := 0; index < count; index++ {
		assetID, err := identifier.NewUUID()
		if err != nil {
			t.Fatalf("generate seed asset identifier: %v", err)
		}
		title := fmt.Sprintf("Performance seed %05d", index)
		if index%20 == 0 {
			title = fmt.Sprintf("Performance needle %05d", index)
		}
		rows = append(rows, []any{
			assetID, principal.WorkspaceID, title, "en-US", "draft", principal.UserID,
		})
	}
	inserted, err := pool.CopyFrom(
		ctx,
		pgx.Identifier{"assets"},
		[]string{"id", "workspace_id", "title", "language", "status", "created_by"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		t.Fatalf("seed performance assets: %v", err)
	}
	if inserted != int64(count) {
		t.Fatalf("seeded assets = %d, want %d", inserted, count)
	}
}

func migratedPerformancePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required when VOICEASSET_DATA_PERF=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect to performance PostgreSQL")
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	schema := fmt.Sprintf("asset_perf_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal("create isolated performance schema")
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop isolated performance schema")
		}
	})

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse performance database configuration")
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = databaseConcurrency
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("create performance database pool")
	}
	t.Cleanup(pool.Close)
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("acquire performance migration connection")
	}
	files, err := migration.Load("../../migrations")
	if err != nil {
		connection.Release()
		t.Fatalf("load performance migrations: %v", err)
	}
	if _, err := migration.Apply(ctx, connection.Conn(), files); err != nil {
		connection.Release()
		t.Fatalf("apply performance migrations: %v", err)
	}
	connection.Release()
	return pool
}
