package waveform

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/asr"
)

func TestFFmpegRendererProducesBoundedPNG(t *testing.T) {
	executable := os.Getenv("TEST_FFMPEG_PATH")
	renderer, err := NewFFmpegRenderer(executable, t.TempDir())
	if err != nil {
		if executable == "" {
			t.Skip("FFmpeg is not available")
		}
		t.Fatalf("NewFFmpegRenderer() error = %v", err)
	}
	wav := sineWaveWAV()
	source := &asr.Audio{
		SizeBytes: int64(len(wav)), Format: "wav", SampleRate: 16000,
		Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(wav)), nil
		},
	}
	rendered, err := renderer.Render(context.Background(), source)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	defer rendered.Content.Close()
	content, err := io.ReadAll(io.LimitReader(rendered.Content, MaxPNGBytes+1))
	if err != nil {
		t.Fatalf("read PNG: %v", err)
	}
	if rendered.Width != Width || rendered.Height != Height || len(content) <= 24 || int64(len(content)) > MaxPNGBytes {
		t.Fatalf("rendered waveform = %dx%d/%d bytes", rendered.Width, rendered.Height, len(content))
	}
	if string(content[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatal("rendered content is not PNG")
	}
	second, err := renderer.Render(context.Background(), source)
	if err != nil {
		t.Fatalf("second Render() error = %v", err)
	}
	secondContent, readErr := io.ReadAll(second.Content)
	closeErr := second.Content.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(content, secondContent) {
		t.Fatalf("repeated render is not byte-identical: read=%v close=%v sizes=%d/%d", readErr, closeErr, len(content), len(secondContent))
	}
}

func TestFFmpegRendererRejectsMissingSource(t *testing.T) {
	renderer := &FFmpegRenderer{executable: "ffmpeg"}
	if _, err := renderer.Render(context.Background(), nil); !errors.Is(err, ErrRender) {
		t.Fatalf("Render() error = %v, want ErrRender", err)
	}
}

func sineWaveWAV() []byte {
	const sampleRate = 16000
	const samples = sampleRate
	dataSize := samples * 2
	buffer := bytes.NewBuffer(make([]byte, 0, 44+dataSize))
	buffer.WriteString("RIFF")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(36+dataSize))
	buffer.WriteString("WAVEfmt ")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(16))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(2))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(16))
	buffer.WriteString("data")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(dataSize))
	for index := 0; index < samples; index++ {
		sample := int16(math.Sin(2*math.Pi*440*float64(index)/sampleRate) * 12000)
		_ = binary.Write(buffer, binary.LittleEndian, sample)
	}
	return buffer.Bytes()
}
