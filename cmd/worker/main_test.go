package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/platform/product"
)

func TestVersionDoesNotRequireConfiguration(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"--version"}, slog.New(slog.DiscardHandler), &output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var info product.BuildInfo
	if err := json.Unmarshal(output.Bytes(), &info); err != nil {
		t.Fatalf("decode version: %v", err)
	}
	if info != product.CurrentBuildInfo() {
		t.Fatalf("build info = %#v, want %#v", info, product.CurrentBuildInfo())
	}
}

func TestRunCycleProcessesAtMostOneClaim(t *testing.T) {
	processor := &fakeCycleProcessor{processed: true}
	if err := runCycle(context.Background(), slog.New(slog.DiscardHandler), processor); err != nil {
		t.Fatalf("runCycle() error = %v", err)
	}
	if processor.calls != 1 {
		t.Fatalf("RunOnce() calls = %d, want 1", processor.calls)
	}
}

func TestRunCycleReturnsSafeProcessorError(t *testing.T) {
	want := errors.New("safe processing failure")
	processor := &fakeCycleProcessor{err: want}
	if err := runCycle(context.Background(), slog.New(slog.DiscardHandler), processor); !errors.Is(err, want) {
		t.Fatalf("runCycle() error = %v, want %v", err, want)
	}
}

func TestWorkerIdentifierIsValidLeaseOwner(t *testing.T) {
	identifier := workerIdentifier()
	if identifier == "" || len(identifier) > 200 {
		t.Fatalf("workerIdentifier() = %q", identifier)
	}
}

func TestFairSchedulerRotatesAfterProcessedWork(t *testing.T) {
	first := &fakeCycleProcessor{processed: true}
	second := &fakeCycleProcessor{processed: true}
	scheduler := newFairScheduler(first, second)
	if processed, err := scheduler.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("first cycle = %t, %v", processed, err)
	}
	if processed, err := scheduler.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("second cycle = %t, %v", processed, err)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("calls = %d/%d", first.calls, second.calls)
	}
}

type fakeCycleProcessor struct {
	processed bool
	err       error
	calls     int
}

func (p *fakeCycleProcessor) RunOnce(context.Context) (bool, error) {
	p.calls++
	return p.processed, p.err
}
