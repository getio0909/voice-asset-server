// Package transcriptexport serializes immutable transcript revisions into
// bounded, downloadable artifacts without changing source lineage.
package transcriptexport

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/transcript"
)

const (
	FormatJSON     = "json"
	FormatMarkdown = "markdown"
	FormatSRT      = "srt"
	FormatVTT      = "vtt"
	MaxExportBytes = int64(16 * 1024 * 1024)
	ExportLifetime = time.Hour
)

var (
	ErrForbidden    = errors.New("transcript export access forbidden")
	ErrInvalidInput = errors.New("invalid transcript export input")
	ErrNotFound     = errors.New("transcript export not found")
	ErrUnavailable  = errors.New("transcript export service unavailable")
)

type CreateInput struct {
	Format string `json:"format"`
}

type Export struct {
	ID          string    `json:"id"`
	AssetID     string    `json:"asset_id"`
	RevisionID  string    `json:"revision_id"`
	Format      string    `json:"format"`
	MIMEType    string    `json:"mime_type"`
	FileSize    int64     `json:"file_size"`
	SHA256      string    `json:"sha256"`
	DownloadURL string    `json:"download_url"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type StoredExport struct {
	Export
	StorageBackend storage.Backend
	StorageKey     string
}

type Media struct {
	ExportID  string
	MIMEType  string
	Extension string
	Size      int64
	SHA256    string
	Content   storage.File
}

type CreateParams struct {
	ID, AuditID, WorkspaceID, AssetID, RevisionID string
	ActorID, ActorType, CredentialID, RequestID   string
	StorageBackend                                storage.Backend
	Format, MIMEType, StorageKey, SHA256          string
	FileSize                                      int64
	ExpiresAt                                     time.Time
}

type Repository interface {
	Create(context.Context, CreateParams) (Export, error)
	Get(context.Context, string, string, time.Time) (StoredExport, error)
}

type RevisionSource interface {
	GetRevision(context.Context, auth.Principal, string) (transcript.Revision, error)
}

type Store interface {
	Backend() storage.Backend
	PutImmutable(context.Context, string, string, string, io.Reader, int64) (storage.Object, error)
	DeleteObject(context.Context, string, int64, string) error
	Open(context.Context, string) (storage.File, error)
}

type Service struct {
	repository Repository
	source     RevisionSource
	store      Store
	random     io.Reader
	now        func() time.Time
}

func NewService(repository Repository, source RevisionSource, store Store) *Service {
	return &Service{repository: repository, source: source, store: store, random: rand.Reader, now: time.Now}
}

func (service *Service) Create(
	ctx context.Context,
	principal auth.Principal,
	revisionID string,
	input CreateInput,
	requestID string,
) (Export, error) {
	if !principal.Can(auth.ScopeTranscriptsRead) || !principal.Can(auth.ScopeMetadataWrite) {
		return Export{}, ErrForbidden
	}
	revisionID, validID := identifier.NormalizeUUID(revisionID)
	format := strings.ToLower(strings.TrimSpace(input.Format))
	if !validID || !validFormat(format) || !validRequestID(requestID) {
		return Export{}, ErrInvalidInput
	}
	if service.repository == nil || service.source == nil || service.store == nil {
		return Export{}, ErrUnavailable
	}
	revision, err := service.source.GetRevision(ctx, principal, revisionID)
	if err != nil {
		if errors.Is(err, transcript.ErrForbidden) {
			return Export{}, ErrForbidden
		}
		if errors.Is(err, transcript.ErrNotFound) {
			return Export{}, ErrNotFound
		}
		return Export{}, fmt.Errorf("load transcript export source: %w", err)
	}
	content, mimeType, err := render(revision, format)
	if err != nil {
		return Export{}, err
	}
	if int64(len(content)) > MaxExportBytes {
		return Export{}, ErrInvalidInput
	}
	exportID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Export{}, fmt.Errorf("generate export identifier: %w", err)
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Export{}, fmt.Errorf("generate export audit identifier: %w", err)
	}
	object, err := service.store.PutImmutable(
		ctx, revision.AssetID, exportID, storage.ObjectKindExport, bytes.NewReader(content), MaxExportBytes,
	)
	if err != nil {
		return Export{}, fmt.Errorf("store transcript export: %w", err)
	}
	created, err := service.repository.Create(ctx, CreateParams{
		ID: exportID, AuditID: auditID, WorkspaceID: principal.WorkspaceID,
		AssetID: revision.AssetID, RevisionID: revision.ID,
		ActorID: principal.UserID, ActorType: actorType(principal),
		CredentialID: principal.CredentialID, RequestID: requestID,
		StorageBackend: object.Backend,
		Format:         format, MIMEType: mimeType, StorageKey: object.Key,
		FileSize: object.Size, SHA256: object.SHA256,
		ExpiresAt: service.now().UTC().Add(ExportLifetime),
	})
	if err != nil {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		cleanupErr := service.store.DeleteObject(cleanupContext, object.Key, object.Size, object.SHA256)
		cancel()
		if errors.Is(err, ErrNotFound) {
			return Export{}, errors.Join(ErrNotFound, cleanupErr)
		}
		return Export{}, errors.Join(fmt.Errorf("persist transcript export: %w", err), cleanupErr)
	}
	created.DownloadURL = "/api/v1/transcript-exports/" + created.ID
	return created, nil
}

func (service *Service) Open(
	ctx context.Context,
	principal auth.Principal,
	exportID string,
) (Media, error) {
	if !principal.Can(auth.ScopeTranscriptsRead) {
		return Media{}, ErrForbidden
	}
	exportID, validID := identifier.NormalizeUUID(exportID)
	if !validID {
		return Media{}, ErrNotFound
	}
	if service.repository == nil || service.store == nil {
		return Media{}, ErrUnavailable
	}
	stored, err := service.repository.Get(ctx, principal.WorkspaceID, exportID, service.now().UTC())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Media{}, ErrNotFound
		}
		return Media{}, fmt.Errorf("load transcript export: %w", err)
	}
	if !stored.StorageBackend.Valid() || stored.StorageBackend != service.store.Backend() {
		return Media{}, fmt.Errorf("storage backend %q is unavailable", stored.StorageBackend)
	}
	file, err := service.store.Open(ctx, stored.StorageKey)
	if err != nil {
		return Media{}, fmt.Errorf("open transcript export: %w", err)
	}
	info, err := file.Stat()
	if err != nil || info.IsDir() || info.Size() != stored.FileSize {
		_ = file.Close()
		return Media{}, fmt.Errorf("verify transcript export size")
	}
	hasher := sha256.New()
	if _, err := io.CopyN(hasher, file, stored.FileSize); err != nil || hex.EncodeToString(hasher.Sum(nil)) != stored.SHA256 {
		_ = file.Close()
		return Media{}, fmt.Errorf("verify transcript export checksum")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return Media{}, fmt.Errorf("reset transcript export")
	}
	return Media{
		ExportID: stored.ID, MIMEType: stored.MIMEType, Extension: extension(stored.Format),
		Size: stored.FileSize, SHA256: stored.SHA256, Content: file,
	}, nil
}

func render(revision transcript.Revision, format string) ([]byte, string, error) {
	switch format {
	case FormatJSON:
		content, err := json.MarshalIndent(revision, "", "  ")
		if err != nil {
			return nil, "", fmt.Errorf("render JSON export: %w", err)
		}
		return append(content, '\n'), "application/json", nil
	case FormatMarkdown:
		var output strings.Builder
		fmt.Fprintf(&output, "# Transcript %s\n\n", revision.ID)
		fmt.Fprintf(&output, "- Asset: `%s`\n- Language: `%s`\n- Kind: `%s`\n- Review status: `%s`\n\n", revision.AssetID, revision.Language, revision.Kind, revision.ReviewStatus)
		output.WriteString("## Text\n\n")
		output.WriteString(revision.Text)
		output.WriteString("\n\n## Timeline\n\n")
		for _, segment := range revision.Segments {
			fmt.Fprintf(&output, "- [%d,%d) %s\n", segment.StartMS, segment.EndMS, strings.ReplaceAll(segment.Text, "\n", " "))
		}
		return []byte(output.String()), "text/markdown; charset=utf-8", nil
	case FormatSRT:
		var output strings.Builder
		for index, segment := range revision.Segments {
			fmt.Fprintf(&output, "%d\n%s --> %s\n%s\n\n", index+1,
				subtitleTime(segment.StartMS, ','), subtitleTime(segment.EndMS, ','), normalizeSubtitleText(segment.Text))
		}
		return []byte(output.String()), "application/x-subrip; charset=utf-8", nil
	case FormatVTT:
		var output strings.Builder
		output.WriteString("WEBVTT\n\n")
		for _, segment := range revision.Segments {
			fmt.Fprintf(&output, "%s --> %s\n%s\n\n",
				subtitleTime(segment.StartMS, '.'), subtitleTime(segment.EndMS, '.'), normalizeSubtitleText(segment.Text))
		}
		return []byte(output.String()), "text/vtt; charset=utf-8", nil
	default:
		return nil, "", ErrInvalidInput
	}
}

func subtitleTime(milliseconds int64, separator rune) string {
	hours := milliseconds / 3_600_000
	minutes := (milliseconds / 60_000) % 60
	seconds := (milliseconds / 1000) % 60
	millis := milliseconds % 1000
	return fmt.Sprintf("%02d:%02d:%02d%c%03d", hours, minutes, seconds, separator, millis)
}

func normalizeSubtitleText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

func validFormat(format string) bool {
	return format == FormatJSON || format == FormatMarkdown || format == FormatSRT || format == FormatVTT
}

func extension(format string) string {
	if format == FormatMarkdown {
		return "md"
	}
	return format
}

func validRequestID(value string) bool {
	if value == "" || len(value) > 200 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func actorType(principal auth.Principal) string {
	if principal.Role == "agent" || principal.CredentialType == "api_key" {
		return "agent"
	}
	return "user"
}
