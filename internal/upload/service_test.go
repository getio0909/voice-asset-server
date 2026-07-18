package upload

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/audio"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

const (
	testAssetID     = "aaaaaaaa-1111-4111-8111-111111111111"
	testWorkspaceID = "bbbbbbbb-2222-4222-8222-222222222222"
	testUserID      = "cccccccc-3333-4333-8333-333333333333"
	testUploadID    = "dddddddd-4444-4444-8444-444444444444"
)

func TestCreateSessionValidatesAndNormalizesDeclaration(t *testing.T) {
	repository := &fakeRepository{created: Session{
		ID: "upload-1", AssetID: testAssetID, WorkspaceID: "workspace-1", State: StateActive,
	}}
	service := NewService(repository)
	service.random = bytes.NewReader(append(
		bytes.Repeat([]byte{0x01}, 16),
		bytes.Repeat([]byte{0x02}, 16)...,
	))
	now := time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	principal := auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite},
	}
	input := CreateInput{
		AssetID: testAssetID, Filename: " recording.wav ", MIMEType: "audio/wav",
		SizeBytes: 1024, SHA256: strings.Repeat("a", 64),
	}

	created, replayed, err := service.Create(context.Background(), principal, input, "upload-key-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if replayed || created.ID != "upload-1" {
		t.Fatalf("Create() = (%+v, %t)", created, replayed)
	}
	params := repository.createParams
	if params.Filename != "recording.wav" || params.MIMEType != "audio/wav" || params.AssetID != testAssetID {
		t.Fatalf("repository declaration = %+v", params)
	}
	if params.WorkspaceID != principal.WorkspaceID || params.CreatedBy != principal.UserID {
		t.Fatalf("repository ownership = %+v", params)
	}
	if params.PartSize != DefaultPartSize || !params.ExpiresAt.Equal(now.Add(DefaultSessionTTL)) {
		t.Fatalf("part/expiry = %d/%s", params.PartSize, params.ExpiresAt)
	}
	if len(params.RequestHash) != 64 || params.IdempotencyKey != "upload-key-1" {
		t.Fatalf("idempotency = %+v", params)
	}
	if params.SessionID == "" || params.AuditID == "" || params.SessionID == params.AuditID {
		t.Fatalf("generated IDs = %q/%q", params.SessionID, params.AuditID)
	}
}

func TestServiceNormalizesUUIDInputsAtBoundaries(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		repository := &fakeRepository{created: Session{ID: testUploadID}}
		service := NewService(repository)
		service.random = bytes.NewReader(append(
			bytes.Repeat([]byte{0x01}, 16),
			bytes.Repeat([]byte{0x02}, 16)...,
		))
		input := validCreateInput("recording.wav")
		input.AssetID = strings.ToUpper(testAssetID)
		principal := auth.Principal{
			UserID: strings.ToUpper(testUserID), WorkspaceID: strings.ToUpper(testWorkspaceID),
			Scopes: []string{auth.ScopeAssetsWrite},
		}

		if _, _, err := service.Create(context.Background(), principal, input, "key"); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if repository.createParams.AssetID != testAssetID ||
			repository.createParams.WorkspaceID != testWorkspaceID ||
			repository.createParams.CreatedBy != testUserID {
			t.Fatalf("Create() UUID params = %+v", repository.createParams)
		}
	})

	t.Run("put part", func(t *testing.T) {
		repository := &fakeRepository{session: activeTestSession()}
		store := &fakePartStore{part: storage.Part{
			Number: 1, Key: "parts/key/1.part", Size: 1024, SHA256: strings.Repeat("a", 64),
		}}
		service := NewService(repository, store)
		principal := auth.Principal{
			WorkspaceID: strings.ToUpper(testWorkspaceID), Scopes: []string{auth.ScopeAssetsWrite},
		}

		if _, _, err := service.PutPart(
			context.Background(), principal, strings.ToUpper(testUploadID), 1,
			strings.Repeat("a", 64), strings.NewReader("part"),
		); err != nil {
			t.Fatalf("PutPart() error = %v", err)
		}
		if repository.getWorkspaceID != testWorkspaceID || repository.getUploadID != testUploadID ||
			store.uploadID != testUploadID || repository.recordParams.WorkspaceID != testWorkspaceID ||
			repository.recordParams.UploadID != testUploadID {
			t.Fatalf("PutPart() UUIDs = get:%q/%q store:%q record:%+v",
				repository.getWorkspaceID, repository.getUploadID, store.uploadID, repository.recordParams)
		}
	})

	t.Run("get", func(t *testing.T) {
		repository := &fakeRepository{session: activeTestSession()}
		service := NewService(repository)
		principal := auth.Principal{
			WorkspaceID: strings.ToUpper(testWorkspaceID), Scopes: []string{auth.ScopeAssetsWrite},
		}

		if _, err := service.Get(context.Background(), principal, strings.ToUpper(testUploadID)); err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if repository.getWorkspaceID != testWorkspaceID || repository.getUploadID != testUploadID {
			t.Fatalf("Get() UUIDs = %q/%q", repository.getWorkspaceID, repository.getUploadID)
		}
	})

	t.Run("complete", func(t *testing.T) {
		file := temporaryOpenFile(t)
		repository := &fakeRepository{
			session:   activeTestSession(),
			parts:     []Part{{Number: 1, SizeBytes: 1024, SHA256: strings.Repeat("a", 64), StorageKey: "parts/1"}},
			completed: Session{ID: testUploadID, State: StateCompleted},
		}
		store := &fakeCompleteStore{object: storage.Object{
			Backend: storage.BackendLocal,
			Key:     "objects/key/original", Size: 1024, SHA256: strings.Repeat("a", 64),
		}, file: file}
		service := NewService(repository, store)
		service.random = bytes.NewReader(append(
			append(bytes.Repeat([]byte{0x03}, 16), bytes.Repeat([]byte{0x04}, 16)...),
			bytes.Repeat([]byte{0x05}, 16)...,
		))
		service.probeMedia = func(audio.ProbeSource, string) (audio.Metadata, error) {
			return audio.Metadata{Container: "wav", Codec: "pcm_s16le", SampleRate: 16000, Channels: 1}, nil
		}
		principal := auth.Principal{
			UserID: strings.ToUpper(testUserID), WorkspaceID: strings.ToUpper(testWorkspaceID),
			Scopes: []string{auth.ScopeAssetsWrite},
		}

		if _, _, err := service.Complete(context.Background(), principal, strings.ToUpper(testUploadID)); err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
		if repository.getWorkspaceID != testWorkspaceID || repository.getUploadID != testUploadID ||
			repository.markWorkspaceID != testWorkspaceID || repository.markUploadID != testUploadID ||
			repository.finishParams.WorkspaceID != testWorkspaceID ||
			repository.finishParams.UploadID != testUploadID || repository.finishParams.ActorID != testUserID ||
			store.assembleUploadID != testUploadID {
			t.Fatalf("Complete() UUIDs = get:%q/%q mark:%q/%q finish:%+v assemble:%q",
				repository.getWorkspaceID, repository.getUploadID,
				repository.markWorkspaceID, repository.markUploadID,
				repository.finishParams, store.assembleUploadID)
		}
	})
}

func TestCreateSessionRequiresWriteScope(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	principal := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsRead}}
	_, _, err := service.Create(context.Background(), principal, CreateInput{
		AssetID: testAssetID, Filename: "recording.wav", MIMEType: "audio/wav",
		SizeBytes: 1024, SHA256: strings.Repeat("a", 64),
	}, "key")
	if !errors.Is(err, ErrForbidden) || repository.createCalls != 0 {
		t.Fatalf("Create() = (%v), repository calls = %d", err, repository.createCalls)
	}
}

func TestCreateAcceptsAndroidM4ADeclaration(t *testing.T) {
	repository := &fakeRepository{created: Session{ID: testUploadID, State: StateActive}}
	service := NewService(repository)
	service.random = bytes.NewReader(append(bytes.Repeat([]byte{0x01}, 16), bytes.Repeat([]byte{0x02}, 16)...))
	principal := auth.Principal{
		UserID: testUserID, WorkspaceID: testWorkspaceID, Scopes: []string{auth.ScopeAssetsWrite},
	}

	_, _, err := service.Create(context.Background(), principal, CreateInput{
		AssetID: testAssetID, Filename: "recording.m4a", MIMEType: "audio/mp4",
		SizeBytes: 1024, SHA256: strings.Repeat("a", 64),
	}, "android-upload-key")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if repository.createParams.MIMEType != "audio/mp4" || repository.createParams.Filename != "recording.m4a" {
		t.Fatalf("Create() params = %+v", repository.createParams)
	}
}

func TestCreateReplayCleansTerminalParts(t *testing.T) {
	repository := &fakeRepository{
		created:  Session{ID: strings.ToUpper(testUploadID), State: StateFailed},
		replayed: true,
	}
	store := &fakeCompleteStore{}
	service := NewService(repository, store)
	principal := auth.Principal{
		UserID: testUserID, WorkspaceID: testWorkspaceID, Scopes: []string{auth.ScopeAssetsWrite},
	}

	_, replayed, err := service.Create(context.Background(), principal, validCreateInput("recording.wav"), "key")
	if err != nil || !replayed {
		t.Fatalf("Create() = replayed:%t error:%v", replayed, err)
	}
	if !store.deleted || store.deletePartsUploadID != testUploadID {
		t.Fatalf("terminal replay cleanup = %t/%q", store.deleted, store.deletePartsUploadID)
	}
}

func TestCreateSessionRejectsUnsafeDeclaration(t *testing.T) {
	tests := []struct {
		name  string
		input CreateInput
		key   string
	}{
		{name: "path filename", input: validCreateInput("../recording.wav"), key: "key"},
		{name: "unsupported MIME", input: func() CreateInput { v := validCreateInput("recording.wav"); v.MIMEType = "audio/mpeg"; return v }(), key: "key"},
		{name: "oversized", input: func() CreateInput { v := validCreateInput("recording.wav"); v.SizeBytes = MaxUploadBytes + 1; return v }(), key: "key"},
		{name: "invalid hash", input: func() CreateInput { v := validCreateInput("recording.wav"); v.SHA256 = "not-a-hash"; return v }(), key: "key"},
		{name: "invalid asset ID", input: func() CreateInput { v := validCreateInput("recording.wav"); v.AssetID = "asset-1"; return v }(), key: "key"},
		{name: "missing key", input: validCreateInput("recording.wav")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{}
			service := NewService(repository)
			principal := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite}}
			if _, _, err := service.Create(context.Background(), principal, test.input, test.key); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Create() error = %v, want ErrInvalidInput", err)
			}
			if repository.createCalls != 0 {
				t.Fatalf("repository calls = %d, want 0", repository.createCalls)
			}
		})
	}
}

func TestPutPartUsesServerCalculatedExactSize(t *testing.T) {
	repository := &fakeRepository{session: Session{
		ID: "upload-1", WorkspaceID: "workspace-1", AssetID: "asset-1",
		ExpectedSize: int64(DefaultPartSize + 1024), PartSize: DefaultPartSize,
		State: StateActive, ExpiresAt: time.Date(2026, 7, 17, 5, 0, 0, 0, time.UTC),
	}}
	store := &fakePartStore{part: storage.Part{
		Number: 2, Key: "parts/server/key/2.part", Size: 1024, SHA256: strings.Repeat("b", 64),
	}}
	service := NewService(repository, store)
	service.now = func() time.Time { return time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC) }
	principal := auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite},
	}

	part, replayed, err := service.PutPart(
		context.Background(), principal, "upload-1", 2, strings.Repeat("b", 64), strings.NewReader("ignored by fake"),
	)
	if err != nil {
		t.Fatalf("PutPart() error = %v", err)
	}
	if replayed || part.Number != 2 {
		t.Fatalf("PutPart() = (%+v, %t)", part, replayed)
	}
	if store.options.ExpectedSize != 1024 || store.options.MaxBytes != 1024 {
		t.Fatalf("storage options = %+v, want exact final-part size", store.options)
	}
	if repository.recordParams.WorkspaceID != principal.WorkspaceID || repository.recordParams.UploadID != "upload-1" {
		t.Fatalf("record params = %+v", repository.recordParams)
	}
}

func TestPutPartRejectsExpiredSessionBeforeStorage(t *testing.T) {
	repository := &fakeRepository{session: Session{
		ID: "upload-1", WorkspaceID: "workspace-1", ExpectedSize: 1024, PartSize: DefaultPartSize,
		State: StateActive, ExpiresAt: time.Date(2026, 7, 16, 4, 0, 0, 0, time.UTC),
	}}
	cleanupErr := errors.New("cleanup failed")
	store := &fakeCompleteStore{deletePartsErr: cleanupErr}
	service := NewService(repository, store)
	service.now = func() time.Time { return time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC) }
	principal := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite}}

	_, _, err := service.PutPart(
		context.Background(), principal, "upload-1", 1, strings.Repeat("a", 64), strings.NewReader("bytes"),
	)
	if !errors.Is(err, ErrExpired) || !errors.Is(err, cleanupErr) {
		t.Fatalf("PutPart() error = %v, want ErrExpired", err)
	}
	if store.calls != 0 {
		t.Fatalf("storage calls = %d, want 0", store.calls)
	}
	if !store.deleted || store.deletePartsUploadID != "upload-1" {
		t.Fatalf("expired parts cleanup = %t/%q", store.deleted, store.deletePartsUploadID)
	}
}

func TestPutPartClassifiesOversizedBody(t *testing.T) {
	repository := &fakeRepository{session: activeTestSession()}
	store := &fakePartStore{err: storage.ErrTooLarge}
	service := NewService(repository, store)
	principal := auth.Principal{WorkspaceID: testWorkspaceID, Scopes: []string{auth.ScopeAssetsWrite}}

	_, _, err := service.PutPart(
		context.Background(), principal, testUploadID, 1,
		strings.Repeat("a", 64), strings.NewReader("oversized"),
	)
	if !errors.Is(err, ErrPartTooLarge) {
		t.Fatalf("PutPart() error = %v, want ErrPartTooLarge", err)
	}
}

func TestPutPartCleansPartsWhenRecordObservesExpiry(t *testing.T) {
	repository := &fakeRepository{session: activeTestSession(), recordErr: ErrExpired}
	store := &fakeCompleteStore{fakePartStore: fakePartStore{part: storage.Part{
		Number: 1, Key: "parts/key/1.part", Size: 1024, SHA256: strings.Repeat("a", 64),
	}}}
	service := NewService(repository, store)
	principal := auth.Principal{WorkspaceID: testWorkspaceID, Scopes: []string{auth.ScopeAssetsWrite}}

	_, _, err := service.PutPart(
		context.Background(), principal, testUploadID, 1,
		strings.Repeat("a", 64), strings.NewReader("part"),
	)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("PutPart() error = %v, want ErrExpired", err)
	}
	if !store.deleted || store.deletePartsUploadID != testUploadID {
		t.Fatalf("record expiry cleanup = %t/%q", store.deleted, store.deletePartsUploadID)
	}
}

func TestGetSessionReturnsWorkspaceScopedParts(t *testing.T) {
	repository := &fakeRepository{session: Session{ID: "upload-1"}, parts: []Part{{Number: 1}}}
	service := NewService(repository)
	principal := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite}}

	session, err := service.Get(context.Background(), principal, "upload-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if session.ID != "upload-1" || len(session.Parts) != 1 {
		t.Fatalf("Get() = %+v", session)
	}
}

func TestCompleteVerifiesPartsAndPersistsM4AMetadata(t *testing.T) {
	file := temporaryOpenFile(t)
	repository := &fakeRepository{session: Session{
		ID: "upload-1", AssetID: "asset-1", WorkspaceID: "workspace-1",
		ExpectedSize: 5, ExpectedSHA256: strings.Repeat("a", 64), PartSize: DefaultPartSize,
		MIMEType: "audio/mp4", State: StateActive,
		ExpiresAt: time.Date(2026, 7, 17, 5, 0, 0, 0, time.UTC),
	}, parts: []Part{{
		Number: 1, SizeBytes: 5, SHA256: strings.Repeat("b", 64), StorageKey: "parts/key/1.part",
	}}, completed: Session{ID: "upload-1", AssetID: "asset-1", State: StateCompleted}}
	store := &fakeCompleteStore{fakePartStore: fakePartStore{}, object: storage.Object{
		Backend: storage.BackendS3,
		Key:     "objects/key/original", Size: 5, SHA256: strings.Repeat("a", 64),
	}, file: file}
	service := NewService(repository, store)
	service.random = bytes.NewReader(append(
		append(bytes.Repeat([]byte{0x03}, 16), bytes.Repeat([]byte{0x04}, 16)...),
		bytes.Repeat([]byte{0x05}, 16)...,
	))
	service.now = func() time.Time { return time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC) }
	service.probeMedia = func(_ audio.ProbeSource, mimeType string) (audio.Metadata, error) {
		if mimeType != "audio/mp4" {
			t.Fatalf("probe MIME type = %q", mimeType)
		}
		return audio.Metadata{
			Container: "m4a", Codec: "aac", SampleRate: 44100, Channels: 1,
			Bitrate: 128000, DurationMS: 1000, DataBytes: 16000,
		}, nil
	}
	principal := auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite},
	}

	result, replayed, err := service.Complete(context.Background(), principal, "upload-1")
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if replayed || result.State != StateCompleted {
		t.Fatalf("Complete() = (%+v, %t)", result, replayed)
	}
	if repository.markAssemblingCalls != 1 || repository.finishParams.Object.StorageKey != store.object.Key ||
		repository.finishParams.Object.StorageBackend != store.object.Backend {
		t.Fatalf("completion persistence = calls:%d params:%+v", repository.markAssemblingCalls, repository.finishParams)
	}
	if repository.finishParams.Object.DurationMS != 1000 || repository.finishParams.Object.SampleRate != 44100 ||
		repository.finishParams.Object.MIMEType != "audio/mp4" || repository.finishParams.Object.Codec != "aac" {
		t.Fatalf("metadata = %+v", repository.finishParams.Object)
	}
	if !store.deleted {
		t.Fatal("successful completion did not clean temporary parts")
	}
}

func TestCompleteRejectsMissingPartBeforeStateTransition(t *testing.T) {
	repository := &fakeRepository{session: Session{
		ID: "upload-1", ExpectedSize: int64(DefaultPartSize + 1), PartSize: DefaultPartSize,
		State: StateActive, ExpiresAt: time.Date(2026, 7, 17, 5, 0, 0, 0, time.UTC),
	}, parts: []Part{{Number: 1, SizeBytes: DefaultPartSize, SHA256: strings.Repeat("a", 64), StorageKey: "parts/1"}}}
	service := NewService(repository, &fakeCompleteStore{})
	service.now = func() time.Time { return time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC) }
	principal := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite}}

	_, _, err := service.Complete(context.Background(), principal, "upload-1")
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Complete() error = %v, want ErrIncomplete", err)
	}
	if repository.markAssemblingCalls != 0 {
		t.Fatalf("MarkAssembling calls = %d, want 0", repository.markAssemblingCalls)
	}
}

func TestCompleteCleansPartsWhenSessionExpiresDuringTransition(t *testing.T) {
	repository := &fakeRepository{
		session:           activeTestSession(),
		parts:             []Part{{Number: 1, SizeBytes: 1024, SHA256: strings.Repeat("a", 64), StorageKey: "parts/1"}},
		markAssemblingErr: ErrExpired,
	}
	store := &fakeCompleteStore{}
	service := NewService(repository, store)
	principal := auth.Principal{WorkspaceID: testWorkspaceID, Scopes: []string{auth.ScopeAssetsWrite}}

	_, _, err := service.Complete(context.Background(), principal, testUploadID)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("Complete() error = %v, want ErrExpired", err)
	}
	if !store.deleted || store.deletePartsUploadID != testUploadID {
		t.Fatalf("transition expiry cleanup = %t/%q", store.deleted, store.deletePartsUploadID)
	}
}

func TestCompleteMarksInvalidAudioFailed(t *testing.T) {
	file := temporaryOpenFile(t)
	repository := &fakeRepository{session: Session{
		ID: "upload-1", AssetID: "asset-1", ExpectedSize: 5, ExpectedSHA256: strings.Repeat("a", 64),
		PartSize: DefaultPartSize, State: StateActive,
		ExpiresAt: time.Date(2026, 7, 17, 5, 0, 0, 0, time.UTC),
	}, parts: []Part{{Number: 1, SizeBytes: 5, SHA256: strings.Repeat("b", 64), StorageKey: "parts/1"}}}
	store := &fakeCompleteStore{object: storage.Object{
		Backend: storage.BackendLocal,
		Key:     "objects/key/original", Size: 5, SHA256: strings.Repeat("a", 64),
	}, file: file}
	service := NewService(repository, store)
	service.now = func() time.Time { return time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC) }
	ctx, cancel := context.WithCancel(context.Background())
	service.probeMedia = func(audio.ProbeSource, string) (audio.Metadata, error) {
		cancel()
		return audio.Metadata{}, audio.ErrInvalidWAV
	}
	principal := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite}}

	_, _, err := service.Complete(ctx, principal, "upload-1")
	if !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("Complete() error = %v, want ErrUnsupportedMedia", err)
	}
	if repository.failedCode != "invalid_audio" {
		t.Fatalf("failed code = %q", repository.failedCode)
	}
	if !store.deletedObject {
		t.Fatal("invalid assembled object was not removed")
	}
	if !store.deleted {
		t.Fatal("invalid upload parts were not removed")
	}
	if repository.failedContextErr != nil || !repository.failedContextHasDeadline {
		t.Fatalf("MarkFailed context = err:%v deadline:%t", repository.failedContextErr, repository.failedContextHasDeadline)
	}
}

func TestCompleteResetUsesIndependentBoundedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repository := &fakeRepository{
		session: activeTestSession(),
		parts:   []Part{{Number: 1, SizeBytes: 1024, SHA256: strings.Repeat("a", 64), StorageKey: "parts/1"}},
	}
	store := &fakeCompleteStore{
		err:        errors.New("assembly failed"),
		onAssemble: cancel,
	}
	service := NewService(repository, store)
	principal := auth.Principal{WorkspaceID: testWorkspaceID, Scopes: []string{auth.ScopeAssetsWrite}}

	if _, _, err := service.Complete(ctx, principal, testUploadID); err == nil {
		t.Fatal("Complete() error = nil")
	}
	if repository.resetCalls != 1 || repository.resetContextErr != nil || !repository.resetContextHasDeadline {
		t.Fatalf("ResetToActive = calls:%d err:%v deadline:%t",
			repository.resetCalls, repository.resetContextErr, repository.resetContextHasDeadline)
	}
}

func activeTestSession() Session {
	return Session{
		ID: testUploadID, AssetID: testAssetID, WorkspaceID: testWorkspaceID,
		ExpectedSize: 1024, ExpectedSHA256: strings.Repeat("a", 64), PartSize: DefaultPartSize,
		MIMEType: "audio/wav", State: StateActive, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
}

func validCreateInput(filename string) CreateInput {
	return CreateInput{
		AssetID: testAssetID, Filename: filename, MIMEType: "audio/wav",
		SizeBytes: 1024, SHA256: strings.Repeat("a", 64),
	}
}

type fakeRepository struct {
	createParams             CreateParams
	created                  Session
	replayed                 bool
	createErr                error
	createCalls              int
	session                  Session
	parts                    []Part
	getErr                   error
	getWorkspaceID           string
	getUploadID              string
	recordParams             RecordPartParams
	recorded                 Part
	replayedPart             bool
	recordErr                error
	markAssemblingCalls      int
	markAssemblingErr        error
	markWorkspaceID          string
	markUploadID             string
	finishParams             FinishParams
	completed                Session
	finishErr                error
	resetCalls               int
	resetContextErr          error
	resetContextHasDeadline  bool
	failedCode               string
	failedContextErr         error
	failedContextHasDeadline bool
}

func (f *fakeRepository) Get(_ context.Context, workspaceID, uploadID string) (Session, []Part, error) {
	f.getWorkspaceID = workspaceID
	f.getUploadID = uploadID
	return f.session, f.parts, f.getErr
}

func (f *fakeRepository) RecordPart(_ context.Context, params RecordPartParams) (Part, bool, error) {
	f.recordParams = params
	if f.recorded.Number == 0 {
		f.recorded = params.Part
	}
	return f.recorded, f.replayedPart, f.recordErr
}

func (f *fakeRepository) MarkAssembling(_ context.Context, workspaceID, uploadID string) error {
	f.markAssemblingCalls++
	f.markWorkspaceID = workspaceID
	f.markUploadID = uploadID
	return f.markAssemblingErr
}

func (f *fakeRepository) Finish(_ context.Context, params FinishParams) (Session, error) {
	f.finishParams = params
	return f.completed, f.finishErr
}

func (f *fakeRepository) ResetToActive(ctx context.Context, _, _ string) error {
	f.resetCalls++
	f.resetContextErr = ctx.Err()
	_, f.resetContextHasDeadline = ctx.Deadline()
	return nil
}

func (f *fakeRepository) MarkFailed(ctx context.Context, params FailureParams) error {
	f.failedCode = params.ErrorCode
	f.failedContextErr = ctx.Err()
	_, f.failedContextHasDeadline = ctx.Deadline()
	return nil
}

type fakePartStore struct {
	part     storage.Part
	err      error
	calls    int
	uploadID string
	number   int
	options  storage.PutPartOptions
}

func (f *fakePartStore) PutPart(
	_ context.Context,
	uploadID string,
	number int,
	_ io.Reader,
	options storage.PutPartOptions,
) (storage.Part, error) {
	f.calls++
	f.uploadID = uploadID
	f.number = number
	f.options = options
	return f.part, f.err
}

type fakeCompleteStore struct {
	fakePartStore
	object              storage.Object
	err                 error
	file                *os.File
	deleted             bool
	deletedObject       bool
	deletePartsErr      error
	deletePartsUploadID string
	assembleUploadID    string
	onAssemble          func()
}

func (f *fakeCompleteStore) Assemble(
	_ context.Context,
	_ string,
	uploadID string,
	_ []storage.PartRef,
	_ storage.AssembleOptions,
) (storage.Object, error) {
	f.assembleUploadID = uploadID
	if f.onAssemble != nil {
		f.onAssemble()
	}
	return f.object, f.err
}

func (f *fakeCompleteStore) Open(context.Context, string) (storage.File, error) {
	return f.file, nil
}

func (f *fakeCompleteStore) DeleteParts(_ context.Context, uploadID string) error {
	f.deleted = true
	f.deletePartsUploadID = uploadID
	return f.deletePartsErr
}

func (f *fakeCompleteStore) DeleteObject(context.Context, string, int64, string) error {
	f.deletedObject = true
	return nil
}

func temporaryOpenFile(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "audio-*.wav")
	if err != nil {
		t.Fatalf("create temporary audio: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func (f *fakeRepository) Create(_ context.Context, params CreateParams) (Session, bool, error) {
	f.createCalls++
	f.createParams = params
	return f.created, f.replayed, f.createErr
}
