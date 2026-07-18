// Package waveform creates and serves bounded waveform derivatives.
package waveform

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/asr"
)

const (
	Width        = 1600
	Height       = 256
	MaxPNGBytes  = int64(4 * 1024 * 1024)
	renderFilter = "aformat=channel_layouts=mono,showwavespic=s=1600x256:colors=0x176b62"
)

var ErrRender = errors.New("waveform render failed")

type Rendered struct {
	Content io.ReadCloser
	Width   int
	Height  int
}

type FFmpegRenderer struct {
	executable string
	tempDir    string
}

func NewFFmpegRenderer(executable, tempDir string) (*FFmpegRenderer, error) {
	if strings.TrimSpace(executable) == "" {
		executable = "ffmpeg"
	}
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return nil, fmt.Errorf("locate FFmpeg executable")
	}
	return &FFmpegRenderer{executable: resolved, tempDir: tempDir}, nil
}

func (renderer *FFmpegRenderer) Render(ctx context.Context, source *asr.Audio) (Rendered, error) {
	if ctx == nil || source == nil || source.Open == nil || source.SizeBytes <= 0 {
		return Rendered{}, ErrRender
	}
	input, err := source.Open(ctx)
	if err != nil {
		return Rendered{}, fmt.Errorf("%w: open source", ErrRender)
	}
	defer input.Close()

	temporary, err := os.CreateTemp(renderer.tempDir, "voiceasset-waveform-*.png")
	if err != nil {
		return Rendered{}, fmt.Errorf("%w: create temporary output", ErrRender)
	}
	outputPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(outputPath)
		return Rendered{}, fmt.Errorf("%w: close temporary output", ErrRender)
	}
	if err := os.Remove(outputPath); err != nil {
		return Rendered{}, fmt.Errorf("%w: prepare temporary output", ErrRender)
	}
	removeOutput := true
	defer func() {
		if removeOutput {
			_ = os.Remove(outputPath)
		}
	}()

	command := exec.CommandContext(ctx, renderer.executable,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", "pipe:0", "-filter_complex", renderFilter,
		"-frames:v", "1", "-map_metadata", "-1",
		"-fflags", "+bitexact", "-flags:v", "+bitexact",
		"-f", "image2", "-vcodec", "png", outputPath,
	)
	command.Stdin = input
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return Rendered{}, fmt.Errorf("%w: FFmpeg command", ErrRender)
	}

	file, err := os.Open(filepath.Clean(outputPath))
	if err != nil {
		return Rendered{}, fmt.Errorf("%w: open generated PNG", ErrRender)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 24 || info.Size() > MaxPNGBytes {
		_ = file.Close()
		return Rendered{}, fmt.Errorf("%w: invalid generated PNG size", ErrRender)
	}
	header := make([]byte, 24)
	if _, err := io.ReadFull(file, header); err != nil ||
		string(header[:8]) != "\x89PNG\r\n\x1a\n" || string(header[12:16]) != "IHDR" ||
		binary.BigEndian.Uint32(header[16:20]) != Width ||
		binary.BigEndian.Uint32(header[20:24]) != Height {
		_ = file.Close()
		return Rendered{}, fmt.Errorf("%w: invalid generated PNG header", ErrRender)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return Rendered{}, fmt.Errorf("%w: reset generated PNG", ErrRender)
	}
	removeOutput = false
	return Rendered{Content: &removeOnClose{File: file, path: outputPath}, Width: Width, Height: Height}, nil
}

type removeOnClose struct {
	*os.File
	path string
}

func (file *removeOnClose) Close() error {
	return errors.Join(file.File.Close(), os.Remove(file.path))
}
