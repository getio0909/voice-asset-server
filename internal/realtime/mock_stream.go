package realtime

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/getio0909/voice-asset-server/internal/asr"
)

// MockStreamFactory provides a deterministic credential-free stream for local
// development and protocol tests. It reuses the normalized Mock ASR fixture so
// batch and realtime results cannot drift.
type MockStreamFactory struct{}

func (MockStreamFactory) Open(ctx context.Context, session Session) (ProviderStream, error) {
	if session.Encoding != EncodingPCMS16LE || !validSampleRate(session.SampleRateHz) ||
		session.Channels != 1 || session.FrameDurationMS < 20 || session.FrameDurationMS > 100 {
		return nil, ErrInvalidEvent
	}
	result, err := asr.NewMockProvider().Transcribe(ctx, asr.Input{Language: session.Language})
	if err != nil {
		return nil, fmt.Errorf("load realtime Mock ASR fixture: %w", err)
	}
	return &mockProviderStream{
		result: result, frameDurationMS: int64(session.FrameDurationMS),
		maxFrameBytes: session.maximumFrameBytes(),
	}, nil
}

type mockProviderStream struct {
	mu              sync.Mutex
	result          asr.Result
	frameDurationMS int64
	maxFrameBytes   int
	nextSequence    int64
	lastCapturedMS  int64
	emittedSegments int
	revision        int64
	closed          bool
	finished        bool
}

func (stream *mockProviderStream) Push(
	ctx context.Context,
	frame ProviderFrame,
) (*TranscriptUpdate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed || stream.finished {
		return nil, ErrStateConflict
	}
	if frame.Sequence != stream.nextSequence {
		return nil, ErrSequenceGap
	}
	if len(frame.PCM) == 0 || len(frame.PCM) > stream.maxFrameBytes || len(frame.PCM)%2 != 0 ||
		frame.CapturedAtMS < 0 ||
		(frame.Sequence > 0 && frame.CapturedAtMS < stream.lastCapturedMS) {
		return nil, ErrFrameSize
	}
	stream.nextSequence++
	stream.lastCapturedMS = frame.CapturedAtMS
	through := frame.CapturedAtMS + stream.frameDurationMS
	emitThrough := stream.emittedSegments
	for emitThrough < len(stream.result.Segments) &&
		stream.result.Segments[emitThrough].EndMS <= through {
		emitThrough++
	}
	if emitThrough == stream.emittedSegments {
		return nil, nil
	}
	stream.emittedSegments = emitThrough
	stream.revision++
	parts := make([]string, 0, emitThrough)
	for _, segment := range stream.result.Segments[:emitThrough] {
		parts = append(parts, segment.Text)
	}
	separator := " "
	if strings.HasPrefix(strings.ToLower(stream.result.Language), "zh") {
		separator = ""
	}
	return &TranscriptUpdate{
		Revision: stream.revision, Text: strings.Join(parts, separator),
		FinalThroughMS: stream.result.Segments[emitThrough-1].EndMS,
	}, nil
}

func (stream *mockProviderStream) Finish(ctx context.Context) (ProviderResult, error) {
	if err := ctx.Err(); err != nil {
		return ProviderResult{}, err
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed || stream.finished || stream.nextSequence == 0 {
		return ProviderResult{}, ErrStateConflict
	}
	stream.finished = true
	return ProviderResult{
		Text: stream.result.Text, Language: stream.result.Language,
		ProviderID: stream.result.ProviderID,
	}, nil
}

func (stream *mockProviderStream) Close() error {
	stream.mu.Lock()
	stream.closed = true
	stream.mu.Unlock()
	return nil
}
