package clip

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/storage"

	"github.com/getio0909/voice-asset-server/internal/audio"
)

type FFmpegClipper struct {
	executable string
	tempDir    string
}

func NewFFmpegClipper(executable, tempDir string) (*FFmpegClipper, error) {
	if strings.TrimSpace(executable) == "" {
		executable = "ffmpeg"
	}
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return nil, fmt.Errorf("locate FFmpeg executable")
	}
	return &FFmpegClipper{executable: resolved, tempDir: tempDir}, nil
}

func (clipper *FFmpegClipper) Clip(
	ctx context.Context,
	source storage.File,
	startMS,
	endMS int64,
) (ClippedAudio, error) {
	if source == nil || strings.TrimSpace(source.Name()) == "" || startMS < 0 || endMS <= startMS {
		return ClippedAudio{}, ErrInvalidInput
	}
	temporary, err := os.CreateTemp(clipper.tempDir, "voiceasset-clip-*.wav")
	if err != nil {
		return ClippedAudio{}, fmt.Errorf("create clip temporary path: %w", err)
	}
	outputPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(outputPath)
		return ClippedAudio{}, fmt.Errorf("close clip temporary path: %w", err)
	}
	if err := os.Remove(outputPath); err != nil {
		return ClippedAudio{}, fmt.Errorf("prepare clip temporary path: %w", err)
	}
	removeOutput := true
	defer func() {
		if removeOutput {
			_ = os.Remove(outputPath)
		}
	}()

	command := exec.CommandContext(ctx, clipper.executable,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", source.Name(), "-ss", ffmpegTime(startMS), "-t", ffmpegTime(endMS-startMS),
		"-map_metadata", "-1", "-vn", "-ac", "1", "-ar", "16000",
		"-c:a", "pcm_s16le", "-fflags", "+bitexact", "-flags:a", "+bitexact",
		"-f", "wav", outputPath,
	)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return ClippedAudio{}, fmt.Errorf("FFmpeg clip command failed: %w", err)
	}
	file, err := os.Open(filepath.Clean(outputPath))
	if err != nil {
		return ClippedAudio{}, fmt.Errorf("open generated clip: %w", err)
	}
	metadata, err := audio.ProbeWAVFile(file)
	if err != nil || metadata.DurationMS <= 0 || metadata.DurationMS > MaxDurationMS+1000 {
		_ = file.Close()
		return ClippedAudio{}, fmt.Errorf("validate generated clip")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return ClippedAudio{}, fmt.Errorf("reset generated clip: %w", err)
	}
	removeOutput = false
	return ClippedAudio{Content: &removeOnClose{File: file, path: outputPath}, Metadata: metadata}, nil
}

func ffmpegTime(milliseconds int64) string {
	return strconv.FormatInt(milliseconds/1000, 10) + "." + fmt.Sprintf("%03d", milliseconds%1000)
}

type removeOnClose struct {
	*os.File
	path string
}

func (file *removeOnClose) Close() error {
	closeErr := file.File.Close()
	removeErr := os.Remove(file.path)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}
