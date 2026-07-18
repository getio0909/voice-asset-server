package performance_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/asset"
	"github.com/getio0909/voice-asset-server/internal/audio"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/transcription"
	"github.com/getio0909/voice-asset-server/internal/upload"
)

const (
	pipelineAssetCount   = 8
	pipelineConcurrency  = 4
	pipelineAudioReads   = 32
	pipelineUploadP95    = 4 * time.Second
	pipelineWorkerP95    = 2 * time.Second
	pipelineAudioP95     = time.Second
	pipelineUploadMinRPS = 1.0
	pipelineWorkerMinRPS = 2.0
	pipelineAudioMinRPS  = 5.0
)

func TestUploadWorkerAudioPerformance(t *testing.T) {
	if !dataPerformanceEnabled() {
		t.Skip("set VOICEASSET_DATA_PERF=1 for the isolated upload/worker/audio performance test")
	}

	pool := migratedPerformancePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	principal, err := auth.NewBootstrapService(
		auth.NewPostgresRepository(pool), auth.PasswordHasher{},
	).CreateOwner(ctx, auth.OwnerInput{
		Email: "pipeline-performance-owner@example.test", Password: "pipeline-performance-password",
		WorkspaceName: "Pipeline Performance Test",
	})
	if err != nil {
		t.Fatalf("bootstrap pipeline performance owner: %v", err)
	}
	localStorage, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("initialize pipeline local storage: %v", err)
	}

	assetRepository := asset.NewPostgresRepository(pool)
	assetService := asset.NewService(assetRepository)
	createdAssets := make([]asset.Asset, pipelineAssetCount)
	for index := range createdAssets {
		created, replayed, createErr := assetService.Create(ctx, principal, asset.CreateInput{
			Title: fmt.Sprintf("Pipeline asset %02d", index), Language: "en-US",
		}, fmt.Sprintf("pipeline-asset-%02d", index))
		if createErr != nil || replayed {
			t.Fatalf("create pipeline asset %d: replayed=%t err=%v", index, replayed, createErr)
		}
		createdAssets[index] = created
	}

	wav := validPerformanceWAV()
	wavDigest := sha256.Sum256(wav)
	parts := [][]byte{wav[:upload.DefaultPartSize], wav[upload.DefaultPartSize:]}
	partDigests := make([]string, len(parts))
	for index, part := range parts {
		digest := sha256.Sum256(part)
		partDigests[index] = hex.EncodeToString(digest[:])
	}
	uploadService := upload.NewService(upload.NewPostgresRepository(pool), localStorage)
	uploadStarted := time.Now()
	uploadSamples := runConcurrentOperations(ctx, pipelineAssetCount, pipelineConcurrency, func(index int) error {
		session, replayed, createErr := uploadService.Create(ctx, principal, upload.CreateInput{
			AssetID: createdAssets[index].ID, Filename: fmt.Sprintf("pipeline-%02d.wav", index),
			MIMEType: "audio/wav", SizeBytes: int64(len(wav)), SHA256: hex.EncodeToString(wavDigest[:]),
		}, fmt.Sprintf("pipeline-upload-%02d", index))
		if createErr != nil {
			return createErr
		}
		if replayed || session.PartSize != upload.DefaultPartSize {
			return fmt.Errorf("unexpected upload create result: replayed=%t part_size=%d", replayed, session.PartSize)
		}
		for partIndex, part := range parts {
			_, partReplayed, partErr := uploadService.PutPart(
				ctx, principal, session.ID, partIndex+1, partDigests[partIndex], bytes.NewReader(part),
			)
			if partErr != nil {
				return partErr
			}
			if partReplayed {
				return fmt.Errorf("upload part %d unexpectedly replayed", partIndex+1)
			}
		}
		completed, reused, completeErr := uploadService.Complete(ctx, principal, session.ID)
		if completeErr != nil {
			return completeErr
		}
		if reused || completed.State != upload.StateCompleted {
			return fmt.Errorf("unexpected upload completion: reused=%t state=%q", reused, completed.State)
		}
		return nil
	})
	uploadMetrics := requireOperationBudget(
		t, "multipart upload/storage/media", uploadSamples, pipelineAssetCount, time.Since(uploadStarted),
		pipelineUploadP95, pipelineUploadMinRPS,
	)

	jobRepository := job.NewPostgresRepository(pool)
	jobService := job.NewService(jobRepository)
	queuedJobs := make([]job.Job, pipelineAssetCount)
	for index, created := range createdAssets {
		queued, replayed, createErr := jobService.CreateTranscription(
			ctx, principal, created.ID, fmt.Sprintf("pipeline-job-%02d", index),
		)
		if createErr != nil || replayed {
			t.Fatalf("create pipeline job %d: replayed=%t err=%v", index, replayed, createErr)
		}
		queuedJobs[index] = queued
	}
	workerStarted := time.Now()
	workerSamples := runConcurrentOperations(ctx, pipelineAssetCount, pipelineConcurrency, func(index int) error {
		processor := transcription.NewProcessor(
			jobRepository, assetRepository, asr.NewMockProvider(), localStorage,
			transcription.NewPostgresCommitter(pool), fmt.Sprintf("performance-worker-%02d", index),
		)
		processed, processErr := processor.RunOnce(ctx)
		if processErr != nil {
			return processErr
		}
		if !processed {
			return fmt.Errorf("worker %d found no claimable job", index)
		}
		return nil
	})
	workerMetrics := requireOperationBudget(
		t, "mock worker/transcript", workerSamples, pipelineAssetCount, time.Since(workerStarted),
		pipelineWorkerP95, pipelineWorkerMinRPS,
	)
	for index, queued := range queuedJobs {
		finished, getErr := jobService.Get(ctx, principal, queued.ID)
		if getErr != nil || finished.State != job.StateSucceeded || finished.ResultRevisionID == nil {
			t.Fatalf("pipeline job %d result: state=%q revision=%v err=%v", index, finished.State, finished.ResultRevisionID, getErr)
		}
	}

	audioService := audio.NewAccessService(audio.NewPostgresOriginalRepository(pool), localStorage)
	audioStarted := time.Now()
	audioSamples := runConcurrentOperations(ctx, pipelineAudioReads, pipelineConcurrency, func(index int) error {
		media, openErr := audioService.Open(ctx, principal, createdAssets[index%len(createdAssets)].ID)
		if openErr != nil {
			return openErr
		}
		if media.Size != int64(len(wav)) || media.SHA256 != hex.EncodeToString(wavDigest[:]) {
			_ = media.Content.Close()
			return fmt.Errorf("unexpected audio metadata: size=%d sha256_match=%t", media.Size, media.SHA256 == hex.EncodeToString(wavDigest[:]))
		}
		return media.Content.Close()
	})
	audioMetrics := requireOperationBudget(
		t, "audio open/full-hash", audioSamples, pipelineAudioReads, time.Since(audioStarted),
		pipelineAudioP95, pipelineAudioMinRPS,
	)

	t.Logf(
		"assets=%d wav_bytes=%d concurrency=%d upload={throughput:%.1f_ops/s p50:%s p95:%s p99:%s max:%s} worker={throughput:%.1f_ops/s p50:%s p95:%s p99:%s max:%s} audio={requests:%d throughput:%.1f_ops/s p50:%s p95:%s p99:%s max:%s}",
		pipelineAssetCount, len(wav), pipelineConcurrency,
		uploadMetrics.throughput, uploadMetrics.p50.Round(time.Microsecond), uploadMetrics.p95.Round(time.Microsecond),
		uploadMetrics.p99.Round(time.Microsecond), uploadMetrics.maximum.Round(time.Microsecond),
		workerMetrics.throughput, workerMetrics.p50.Round(time.Microsecond), workerMetrics.p95.Round(time.Microsecond),
		workerMetrics.p99.Round(time.Microsecond), workerMetrics.maximum.Round(time.Microsecond),
		pipelineAudioReads, audioMetrics.throughput, audioMetrics.p50.Round(time.Microsecond),
		audioMetrics.p95.Round(time.Microsecond), audioMetrics.p99.Round(time.Microsecond),
		audioMetrics.maximum.Round(time.Microsecond),
	)
}

func validPerformanceWAV() []byte {
	totalSize := upload.DefaultPartSize + 1024
	dataSize := totalSize - 44
	result := make([]byte, totalSize)
	copy(result[0:4], "RIFF")
	binary.LittleEndian.PutUint32(result[4:8], uint32(totalSize-8))
	copy(result[8:12], "WAVE")
	copy(result[12:16], "fmt ")
	binary.LittleEndian.PutUint32(result[16:20], 16)
	binary.LittleEndian.PutUint16(result[20:22], 1)
	binary.LittleEndian.PutUint16(result[22:24], 1)
	binary.LittleEndian.PutUint32(result[24:28], 16_000)
	binary.LittleEndian.PutUint32(result[28:32], 32_000)
	binary.LittleEndian.PutUint16(result[32:34], 2)
	binary.LittleEndian.PutUint16(result[34:36], 16)
	copy(result[36:40], "data")
	binary.LittleEndian.PutUint32(result[40:44], uint32(dataSize))
	return result
}
