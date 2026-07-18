package clip

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/audio"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

const (
	testAssetID     = "10000000-0000-4000-8000-000000000001"
	testObjectID    = "20000000-0000-4000-8000-000000000001"
	testWorkspaceID = "30000000-0000-4000-8000-000000000001"
	testUserID      = "40000000-0000-4000-8000-000000000001"
	testAPIKeyID    = "50000000-0000-4000-8000-000000000001"
)

func TestServiceCreateStoresBoundedClipAndAttributesAgent(t *testing.T) {
	sourceFile := testFile(t, []byte("source"), "source.wav")
	clipFile := testFile(t, []byte("generated clip"), "generated.wav")
	repository := &fakeRepository{}
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(
		repository,
		&fakeSource{media: audio.Media{
			ObjectID: testObjectID, AssetID: testAssetID, DurationMS: 10_000, Content: sourceFile,
		}},
		&fakeClipper{result: ClippedAudio{
			Content: clipFile,
			Metadata: audio.Metadata{
				Container: "wav", Codec: "pcm_s16le", SampleRate: 16_000,
				Channels: 1, Bitrate: 256_000, DurationMS: 1_500,
			},
		}},
		store,
	)
	service.random = bytes.NewReader(make([]byte, 32))
	service.now = func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) }
	principal := agentPrincipal(auth.ScopeAudioRead, auth.ScopeMetadataWrite)

	created, err := service.Create(
		context.Background(), principal, testAssetID, CreateInput{StartMS: 500, EndMS: 2_000}, "request-clip-1",
	)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.DownloadURL != "/api/v1/audio-clips/"+created.ID || created.ExpiresAt != service.now().Add(ClipLifetime) {
		t.Fatalf("created clip = %+v", created)
	}
	params := repository.createParams
	if params.ActorType != "agent" || params.ActorID != testUserID || params.CredentialID != testAPIKeyID ||
		params.RequestID != "request-clip-1" || params.ParentObjectID != testObjectID ||
		params.StorageBackend != storage.BackendLocal ||
		params.StartMS != 500 || params.EndMS != 2_000 || params.DurationMS != 1_500 {
		t.Fatalf("repository params = %+v", params)
	}
	file, err := store.Open(context.Background(), params.StorageKey)
	if err != nil {
		t.Fatalf("open stored clip: %v", err)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil || string(content) != "generated clip" {
		t.Fatalf("stored content = %q, error = %v", content, err)
	}
}

func TestServiceCreateRejectsScopesAndInvalidRangesBeforeOpeningAudio(t *testing.T) {
	source := &fakeSource{}
	service := NewService(&fakeRepository{}, source, &fakeClipper{}, nil)
	for _, test := range []struct {
		name      string
		principal auth.Principal
		assetID   string
		input     CreateInput
		requestID string
		want      error
	}{
		{
			name: "missing write scope", principal: agentPrincipal(auth.ScopeAudioRead),
			assetID: testAssetID, input: CreateInput{StartMS: 0, EndMS: 1_000}, requestID: "request", want: ErrForbidden,
		},
		{
			name: "negative start", principal: agentPrincipal(auth.ScopeAudioRead, auth.ScopeMetadataWrite),
			assetID: testAssetID, input: CreateInput{StartMS: -1, EndMS: 1_000}, requestID: "request", want: ErrInvalidInput,
		},
		{
			name: "too long", principal: agentPrincipal(auth.ScopeAudioRead, auth.ScopeMetadataWrite),
			assetID: testAssetID, input: CreateInput{StartMS: 0, EndMS: MaxDurationMS + 1}, requestID: "request", want: ErrInvalidInput,
		},
		{
			name: "invalid request ID", principal: agentPrincipal(auth.ScopeAudioRead, auth.ScopeMetadataWrite),
			assetID: testAssetID, input: CreateInput{StartMS: 0, EndMS: 1_000}, requestID: " bad", want: ErrInvalidInput,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.Create(context.Background(), test.principal, test.assetID, test.input, test.requestID)
			if !errors.Is(err, test.want) {
				t.Fatalf("Create() error = %v, want %v", err, test.want)
			}
		})
	}
	if source.calls != 0 {
		t.Fatalf("source calls = %d, want 0", source.calls)
	}
}

func TestServiceCreateRejectsRangeBeyondKnownSourceDuration(t *testing.T) {
	sourceFile := testFile(t, []byte("source"), "source.wav")
	service := NewService(
		&fakeRepository{},
		&fakeSource{media: audio.Media{ObjectID: testObjectID, DurationMS: 1_000, Content: sourceFile}},
		&fakeClipper{},
		&fakeStore{},
	)
	_, err := service.Create(
		context.Background(), agentPrincipal(auth.ScopeAudioRead, auth.ScopeMetadataWrite),
		testAssetID, CreateInput{StartMS: 500, EndMS: 1_001}, "request",
	)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Create() error = %v, want ErrInvalidInput", err)
	}
}

func TestServiceOpenVerifiesStoredContent(t *testing.T) {
	content := []byte("verified clip")
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	object, err := store.PutImmutable(
		context.Background(), testAssetID, testObjectID, storage.ObjectKindClip,
		bytes.NewReader(content), int64(len(content)),
	)
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeRepository{stored: StoredClip{
		Clip:           Clip{ID: testObjectID, MIMEType: "audio/wav", FileSize: object.Size, SHA256: object.SHA256},
		StorageBackend: object.Backend, StorageKey: object.Key,
	}}
	service := NewService(repository, nil, nil, store)

	media, err := service.Open(context.Background(), agentPrincipal(auth.ScopeAudioRead), testObjectID)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer media.Content.Close()
	got, err := io.ReadAll(media.Content)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("Open() content = %q, error = %v", got, err)
	}
	if repository.getWorkspaceID != testWorkspaceID {
		t.Fatalf("workspace = %q", repository.getWorkspaceID)
	}
}

func TestFFmpegClipperCreatesMono16KClipAndRemovesTemporaryOutput(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not available")
	}
	source := testFile(t, pcmWAV(2*time.Second), "source.wav")
	clipper, err := NewFFmpegClipper("ffmpeg", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	clipped, err := clipper.Clip(context.Background(), source, 250, 1_250)
	if err != nil {
		t.Fatalf("Clip() error = %v", err)
	}
	path := clipped.Content.(*removeOnClose).Name()
	if clipped.Metadata.SampleRate != 16_000 || clipped.Metadata.Channels != 1 ||
		clipped.Metadata.DurationMS < 990 || clipped.Metadata.DurationMS > 1_010 {
		t.Fatalf("clip metadata = %+v", clipped.Metadata)
	}
	if err := clipped.Content.Close(); err != nil {
		t.Fatalf("close generated clip: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary output still exists: %v", err)
	}
}

type fakeRepository struct {
	createParams   CreateParams
	createErr      error
	stored         StoredClip
	getErr         error
	getWorkspaceID string
}

func (repository *fakeRepository) Create(_ context.Context, params CreateParams) (Clip, error) {
	repository.createParams = params
	return Clip{
		ID: params.ID, AssetID: params.AssetID, StartMS: params.StartMS, EndMS: params.EndMS,
		DurationMS: params.DurationMS, MIMEType: "audio/wav", FileSize: params.FileSize,
		SHA256: params.SHA256, CreatedAt: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		ExpiresAt: params.ExpiresAt,
	}, repository.createErr
}

func (repository *fakeRepository) Get(_ context.Context, workspaceID, _ string, _ time.Time) (StoredClip, error) {
	repository.getWorkspaceID = workspaceID
	return repository.stored, repository.getErr
}

type fakeSource struct {
	media audio.Media
	err   error
	calls int
}

func (source *fakeSource) Open(_ context.Context, _ auth.Principal, _ string) (audio.Media, error) {
	source.calls++
	return source.media, source.err
}

type fakeClipper struct {
	result ClippedAudio
	err    error
}

func (clipper *fakeClipper) Clip(context.Context, storage.File, int64, int64) (ClippedAudio, error) {
	return clipper.result, clipper.err
}

type fakeStore struct{}

func (*fakeStore) PutImmutable(context.Context, string, string, string, io.Reader, int64) (storage.Object, error) {
	return storage.Object{}, errors.New("unexpected PutImmutable call")
}
func (*fakeStore) Backend() storage.Backend                                  { return storage.BackendLocal }
func (*fakeStore) DeleteObject(context.Context, string, int64, string) error { return nil }
func (*fakeStore) Open(context.Context, string) (storage.File, error)        { return nil, os.ErrNotExist }

func agentPrincipal(scopes ...string) auth.Principal {
	return auth.Principal{
		UserID: testUserID, WorkspaceID: testWorkspaceID, Role: "agent", Scopes: scopes,
		CredentialType: "api_key", CredentialID: testAPIKeyID,
	}
}

func testFile(t *testing.T, content []byte, name string) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func pcmWAV(duration time.Duration) []byte {
	const sampleRate = 16_000
	dataSize := int(duration.Seconds() * sampleRate * 2)
	result := make([]byte, 44+dataSize)
	copy(result[0:4], "RIFF")
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(result)-8))
	copy(result[8:12], "WAVE")
	copy(result[12:16], "fmt ")
	binary.LittleEndian.PutUint32(result[16:20], 16)
	binary.LittleEndian.PutUint16(result[20:22], 1)
	binary.LittleEndian.PutUint16(result[22:24], 1)
	binary.LittleEndian.PutUint32(result[24:28], sampleRate)
	binary.LittleEndian.PutUint32(result[28:32], sampleRate*2)
	binary.LittleEndian.PutUint16(result[32:34], 2)
	binary.LittleEndian.PutUint16(result[34:36], 16)
	copy(result[36:40], "data")
	binary.LittleEndian.PutUint32(result[40:44], uint32(dataSize))
	return result
}

func digest(content []byte) string {
	value := sha256.Sum256(content)
	return string(value[:])
}

var (
	_ Repository = (*fakeRepository)(nil)
	_ Source     = (*fakeSource)(nil)
	_ Clipper    = (*fakeClipper)(nil)
	_ Store      = (*fakeStore)(nil)
)
