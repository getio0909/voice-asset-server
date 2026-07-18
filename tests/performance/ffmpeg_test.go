package performance_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/clip"
	"github.com/getio0909/voice-asset-server/internal/waveform"
)

const (
	ffmpegClipCount      = 12
	ffmpegConcurrency    = 3
	ffmpegClipDurationMS = int64(30_000)
	ffmpegClipP95        = 3 * time.Second
	ffmpegClipMinRPS     = 1.0
	ffmpegWaveformCount  = 12
	ffmpegWaveformP95    = 3 * time.Second
	ffmpegWaveformMinRPS = 1.0
)

func TestFFmpegClipPerformance(t *testing.T) {
	if os.Getenv("VOICEASSET_MEDIA_PERF") != "1" {
		t.Skip("set VOICEASSET_MEDIA_PERF=1 for the real FFmpeg clip performance test")
	}

	ffmpegPath := strings.TrimSpace(os.Getenv("VOICEASSET_FFMPEG_PATH"))
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	outputDir := t.TempDir()
	clipper, err := clip.NewFFmpegClipper(ffmpegPath, outputDir)
	if err != nil {
		t.Fatalf("initialize FFmpeg clipper: %v", err)
	}
	sourcePath := filepath.Join(t.TempDir(), "performance-source.wav")
	sourceWAV := validPerformanceWAV()
	if err := os.WriteFile(sourcePath, sourceWAV, 0o600); err != nil {
		t.Fatalf("write performance source: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	started := time.Now()
	samples := runConcurrentOperations(ctx, ffmpegClipCount, ffmpegConcurrency, func(index int) error {
		source, openErr := os.Open(sourcePath)
		if openErr != nil {
			return fmt.Errorf("open source: %w", openErr)
		}
		defer source.Close()

		startMS := int64(index%8) * 5_000
		clipped, clipErr := clipper.Clip(ctx, source, startMS, startMS+ffmpegClipDurationMS)
		if clipErr != nil {
			return clipErr
		}
		metadata := clipped.Metadata
		written, copyErr := io.Copy(io.Discard, clipped.Content)
		closeErr := clipped.Content.Close()
		if copyErr != nil {
			return fmt.Errorf("read generated clip: %w", copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close generated clip: %w", closeErr)
		}
		if metadata.SampleRate != 16_000 || metadata.Channels != 1 ||
			metadata.DurationMS < ffmpegClipDurationMS-100 || metadata.DurationMS > ffmpegClipDurationMS+100 {
			return fmt.Errorf("unexpected clip metadata: %+v", metadata)
		}
		if written < metadata.DataBytes || written > clip.MaxClipBytes {
			return fmt.Errorf("unexpected generated size: bytes=%d data_bytes=%d", written, metadata.DataBytes)
		}
		return nil
	})
	metrics := requireOperationBudget(
		t, "FFmpeg 30-second clip", samples, ffmpegClipCount, time.Since(started),
		ffmpegClipP95, ffmpegClipMinRPS,
	)

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		t.Fatalf("inspect FFmpeg temporary directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("FFmpeg temporary directory contains %d entry(s) after close", len(entries))
	}
	t.Logf(
		"clips=%d duration_ms=%d source_bytes=%d concurrency=%d throughput=%.1f_ops/s p50=%s p95=%s p99=%s max=%s",
		ffmpegClipCount, ffmpegClipDurationMS, len(sourceWAV), ffmpegConcurrency,
		metrics.throughput, metrics.p50.Round(time.Microsecond), metrics.p95.Round(time.Microsecond),
		metrics.p99.Round(time.Microsecond), metrics.maximum.Round(time.Microsecond),
	)
}

func TestFFmpegWaveformPerformance(t *testing.T) {
	if os.Getenv("VOICEASSET_MEDIA_PERF") != "1" {
		t.Skip("set VOICEASSET_MEDIA_PERF=1 for the real FFmpeg waveform performance test")
	}

	ffmpegPath := strings.TrimSpace(os.Getenv("VOICEASSET_FFMPEG_PATH"))
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	outputDir := t.TempDir()
	renderer, err := waveform.NewFFmpegRenderer(ffmpegPath, outputDir)
	if err != nil {
		t.Fatalf("initialize FFmpeg waveform renderer: %v", err)
	}
	sourcePath := filepath.Join(t.TempDir(), "performance-source.wav")
	sourceWAV := validPerformanceWAV()
	if err := os.WriteFile(sourcePath, sourceWAV, 0o600); err != nil {
		t.Fatalf("write performance source: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	started := time.Now()
	samples := runConcurrentOperations(ctx, ffmpegWaveformCount, ffmpegConcurrency, func(_ int) error {
		source := &asr.Audio{
			SizeBytes:  int64(len(sourceWAV)),
			Format:     "wav",
			SampleRate: 16_000,
			Open: func(context.Context) (io.ReadCloser, error) {
				return os.Open(sourcePath)
			},
		}
		rendered, renderErr := renderer.Render(ctx, source)
		if renderErr != nil {
			return renderErr
		}
		header := make([]byte, 8)
		if _, readErr := io.ReadFull(rendered.Content, header); readErr != nil {
			_ = rendered.Content.Close()
			return fmt.Errorf("read generated waveform header: %w", readErr)
		}
		remaining, readErr := io.Copy(io.Discard, io.LimitReader(rendered.Content, waveform.MaxPNGBytes))
		closeErr := rendered.Content.Close()
		if readErr != nil {
			return fmt.Errorf("read generated waveform: %w", readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close generated waveform: %w", closeErr)
		}
		written := int64(len(header)) + remaining
		if string(header) != "\x89PNG\r\n\x1a\n" || rendered.Width != waveform.Width ||
			rendered.Height != waveform.Height || written <= 24 || written > waveform.MaxPNGBytes {
			return fmt.Errorf("unexpected waveform: dimensions=%dx%d bytes=%d", rendered.Width, rendered.Height, written)
		}
		return nil
	})
	metrics := requireOperationBudget(
		t, "FFmpeg waveform", samples, ffmpegWaveformCount, time.Since(started),
		ffmpegWaveformP95, ffmpegWaveformMinRPS,
	)

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		t.Fatalf("inspect waveform temporary directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("waveform temporary directory contains %d entry(s) after close", len(entries))
	}
	t.Logf(
		"waveforms=%d dimensions=%dx%d source_bytes=%d concurrency=%d throughput=%.1f_ops/s p50=%s p95=%s p99=%s max=%s",
		ffmpegWaveformCount, waveform.Width, waveform.Height, len(sourceWAV), ffmpegConcurrency,
		metrics.throughput, metrics.p50.Round(time.Microsecond), metrics.p95.Round(time.Microsecond),
		metrics.p99.Round(time.Microsecond), metrics.maximum.Round(time.Microsecond),
	)
}
