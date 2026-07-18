package transcription

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/audio"
)

var ErrSourceAudio = errors.New("transcription source audio is unavailable")

type AudioSource interface {
	Resolve(ctx context.Context, workspaceID, assetID string) (*asr.Audio, error)
}

// OriginalAudioSource resolves workspace-scoped immutable object metadata and
// verifies size and SHA-256 each time a provider opens the stream.
type OriginalAudioSource struct {
	repository audio.OriginalRepository
	store      audio.OriginalStore
}

func NewOriginalAudioSource(
	repository audio.OriginalRepository,
	store audio.OriginalStore,
) *OriginalAudioSource {
	return &OriginalAudioSource{repository: repository, store: store}
}

func (source *OriginalAudioSource) Resolve(
	ctx context.Context,
	workspaceID,
	assetID string,
) (*asr.Audio, error) {
	if source == nil || source.repository == nil || source.store == nil || ctx == nil {
		return nil, ErrSourceAudio
	}
	original, err := source.repository.GetOriginal(ctx, workspaceID, assetID)
	if err != nil {
		return nil, fmt.Errorf("%w: load metadata", ErrSourceAudio)
	}
	format, ok := providerAudioFormat(original.MIMEType, original.Container)
	expectedHash, hashErr := hex.DecodeString(original.SHA256)
	if !ok || original.Size < 1 || hashErr != nil || len(expectedHash) != sha256.Size ||
		!original.StorageBackend.Valid() || original.StorageBackend != source.store.Backend() {
		return nil, fmt.Errorf("%w: invalid metadata", ErrSourceAudio)
	}
	return &asr.Audio{
		SizeBytes: original.Size, Format: format, SampleRate: original.SampleRate,
		Open: func(openContext context.Context) (io.ReadCloser, error) {
			if openContext == nil || openContext.Err() != nil {
				return nil, fmt.Errorf("%w: context", ErrSourceAudio)
			}
			file, err := source.store.Open(openContext, original.StorageKey)
			if err != nil {
				return nil, fmt.Errorf("%w: open object", ErrSourceAudio)
			}
			info, err := file.Stat()
			if err != nil || !info.Mode().IsRegular() || info.Size() != original.Size {
				_ = file.Close()
				return nil, fmt.Errorf("%w: size verification", ErrSourceAudio)
			}
			digest := sha256.New()
			if _, err := io.CopyN(digest, sourceContextReader{ctx: openContext, reader: file}, original.Size); err != nil ||
				subtle.ConstantTimeCompare(digest.Sum(nil), expectedHash) != 1 {
				_ = file.Close()
				return nil, fmt.Errorf("%w: checksum verification", ErrSourceAudio)
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				_ = file.Close()
				return nil, fmt.Errorf("%w: reset object", ErrSourceAudio)
			}
			return file, nil
		},
	}, nil
}

func providerAudioFormat(mimeType, container string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "audio/wav", "audio/x-wav":
		return "wav", container == "" || strings.EqualFold(container, "wav")
	case "audio/mp4":
		return "m4a", container == "" || strings.EqualFold(container, "m4a")
	default:
		return "", false
	}
}

type sourceContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader sourceContextReader) Read(destination []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(destination)
}
