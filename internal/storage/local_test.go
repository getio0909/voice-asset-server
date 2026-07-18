package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/storage"
)

func TestLocalPutPartUsesGeneratedKeyAndStreamsContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t, root)
	content := []byte("voice payload")
	uploadID := "../../client-supplied-name.wav"

	part, err := store.PutPart(context.Background(), uploadID, 1, bytes.NewReader(content), storage.PutPartOptions{
		ExpectedSHA256: digest(content),
		MaxBytes:       int64(len(content)),
	})
	if err != nil {
		t.Fatalf("PutPart() error = %v", err)
	}
	if part.Number != 1 || part.Size != int64(len(content)) || part.SHA256 != digest(content) {
		t.Fatalf("PutPart() = %#v", part)
	}
	if part.Reused {
		t.Fatal("first PutPart() unexpectedly reported a reused part")
	}
	if !strings.HasPrefix(part.Key, "parts/") || strings.Contains(part.Key, uploadID) || filepath.IsAbs(part.Key) {
		t.Fatalf("PutPart() returned unsafe or client-derived key %q", part.Key)
	}
	assertOpenContent(t, store, part.Key, content)
	assertNoTemporaryFiles(t, root)
}

func TestLocalPutPartRejectsOversizeAndBadChecksumWithoutPublishing(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		content []byte
		opts    storage.PutPartOptions
		wantErr error
	}{
		{
			name:    "too large",
			content: []byte("12345"),
			opts: storage.PutPartOptions{
				ExpectedSHA256: digest([]byte("12345")),
				MaxBytes:       4,
			},
			wantErr: storage.ErrTooLarge,
		},
		{
			name:    "checksum mismatch",
			content: []byte("payload"),
			opts: storage.PutPartOptions{
				ExpectedSHA256: digest([]byte("different")),
				MaxBytes:       64,
			},
			wantErr: storage.ErrChecksumMismatch,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			store := newStore(t, root)
			_, err := store.PutPart(context.Background(), "upload", 1, bytes.NewReader(test.content), test.opts)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("PutPart() error = %v, want %v", err, test.wantErr)
			}
			assertNoPublishedFiles(t, root)
		})
	}
}

func TestLocalPutPartRejectsDeclaredSizeMismatchWithoutPublishing(t *testing.T) {
	t.Parallel()

	for _, expectedSize := range []int64{3, 10} {
		t.Run(fmt.Sprintf("expected_%d", expectedSize), func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			store := newStore(t, root)
			content := []byte("12345")
			_, err := store.PutPart(context.Background(), "upload-size", 1, bytes.NewReader(content), storage.PutPartOptions{
				ExpectedSize:   expectedSize,
				ExpectedSHA256: digest(content),
				MaxBytes:       64,
			})
			if !errors.Is(err, storage.ErrSizeMismatch) {
				t.Fatalf("PutPart() error = %v, want %v", err, storage.ErrSizeMismatch)
			}
			assertNoPublishedFiles(t, root)
		})
	}
}

func TestLocalPutPartIsIdempotentAndDetectsConflict(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t, root)
	first := []byte("same content")
	firstOpts := storage.PutPartOptions{ExpectedSHA256: digest(first), MaxBytes: 64}

	written, err := store.PutPart(context.Background(), "upload-1", 7, bytes.NewReader(first), firstOpts)
	if err != nil {
		t.Fatalf("first PutPart() error = %v", err)
	}
	retried, err := store.PutPart(context.Background(), "upload-1", 7, bytes.NewReader(first), firstOpts)
	if err != nil {
		t.Fatalf("retry PutPart() error = %v", err)
	}
	if !retried.Reused || retried.Key != written.Key {
		t.Fatalf("retry PutPart() = %#v, first = %#v", retried, written)
	}

	different := []byte("different content")
	_, err = store.PutPart(context.Background(), "upload-1", 7, bytes.NewReader(different), storage.PutPartOptions{
		ExpectedSHA256: digest(different),
		MaxBytes:       64,
	})
	if !errors.Is(err, storage.ErrPartConflict) {
		t.Fatalf("conflicting PutPart() error = %v, want %v", err, storage.ErrPartConflict)
	}
	assertOpenContent(t, store, written.Key, first)
	assertNoTemporaryFiles(t, root)
}

func TestLocalConcurrentPutPartNeverOverwritesPublishedContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t, root)
	type result struct {
		content []byte
		part    storage.Part
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, content := range [][]byte{[]byte("first contender"), []byte("second contender")} {
		content := content
		go func() {
			<-start
			part, err := store.PutPart(context.Background(), "contended-upload", 1, bytes.NewReader(content), storage.PutPartOptions{
				ExpectedSHA256: digest(content),
				MaxBytes:       64,
			})
			results <- result{content: content, part: part, err: err}
		}()
	}
	close(start)

	var winner result
	var successes, conflicts int
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			successes++
			winner = result
		case errors.Is(result.err, storage.ErrPartConflict):
			conflicts++
		default:
			t.Fatalf("concurrent PutPart() error = %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results: successes = %d, conflicts = %d", successes, conflicts)
	}
	assertOpenContent(t, store, winner.part.Key, winner.content)
	assertNoTemporaryFiles(t, root)
}

func TestLocalConcurrentStoresNeverOverwritePublishedContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stores := []*storage.Local{newStore(t, root), newStore(t, root)}
	contents := [][]byte{[]byte("first process contender"), []byte("second process contender")}
	ready := make(chan struct{}, len(stores))
	release := make(chan struct{})
	type result struct {
		content []byte
		part    storage.Part
		err     error
	}
	results := make(chan result, len(stores))
	for index, store := range stores {
		index, store := index, store
		go func() {
			content := contents[index]
			part, err := store.PutPart(
				context.Background(),
				"shared-root-upload",
				1,
				&publishBarrierReader{content: content, ready: ready, release: release},
				storage.PutPartOptions{
					ExpectedSize:   int64(len(content)),
					ExpectedSHA256: digest(content),
					MaxBytes:       int64(len(content)),
				},
			)
			results <- result{content: content, part: part, err: err}
		}()
	}
	for range stores {
		<-ready
	}
	close(release)

	var winner result
	var successes, conflicts int
	for range stores {
		result := <-results
		switch {
		case result.err == nil:
			successes++
			winner = result
		case errors.Is(result.err, storage.ErrPartConflict):
			conflicts++
		default:
			t.Fatalf("concurrent PutPart() error = %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent stores: successes = %d, conflicts = %d", successes, conflicts)
	}
	assertOpenContent(t, stores[0], winner.part.Key, winner.content)
	assertNoTemporaryFiles(t, root)
}

func TestLocalAssembleStreamsOrderedPartsAndUsesAssetKey(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t, root)
	first := putPart(t, store, "upload-2", 1, []byte("hello "))
	second := putPart(t, store, "upload-2", 2, []byte("world"))
	whole := []byte("hello world")
	assetID := "../../client-name.mp3"

	object, err := store.Assemble(
		context.Background(),
		assetID,
		"upload-2",
		[]storage.PartRef{first.Ref(), second.Ref()},
		storage.AssembleOptions{
			ExpectedSize:   int64(len(whole)),
			ExpectedSHA256: digest(whole),
			MaxBytes:       int64(len(whole)),
		},
	)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if object.Size != int64(len(whole)) || object.SHA256 != digest(whole) || object.Reused {
		t.Fatalf("Assemble() = %#v", object)
	}
	if !strings.HasPrefix(object.Key, "objects/") || strings.Contains(object.Key, assetID) || filepath.IsAbs(object.Key) {
		t.Fatalf("Assemble() returned unsafe or client-derived key %q", object.Key)
	}
	assertOpenContent(t, store, object.Key, whole)
	assertNoTemporaryFiles(t, root)

	retried, err := store.Assemble(
		context.Background(),
		assetID,
		"upload-2",
		[]storage.PartRef{first.Ref(), second.Ref()},
		storage.AssembleOptions{
			ExpectedSize:   int64(len(whole)),
			ExpectedSHA256: digest(whole),
			MaxBytes:       int64(len(whole)),
		},
	)
	if err != nil {
		t.Fatalf("retry Assemble() error = %v", err)
	}
	if !retried.Reused || retried.Key != object.Key {
		t.Fatalf("retry Assemble() = %#v, first = %#v", retried, object)
	}
}

func TestLocalPutImmutableStreamsProviderRawResponseToGeneratedKey(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t, root)
	content := []byte(`{"request_id":"provider-request","result":"ok"}`)
	assetID := "../../client-asset.wav"
	objectID := "provider/request/../../response.json"

	object, err := store.PutImmutable(
		context.Background(),
		assetID,
		objectID,
		storage.ObjectKindProviderRawResponse,
		bytes.NewReader(content),
		int64(len(content)),
	)
	if err != nil {
		t.Fatalf("PutImmutable() error = %v", err)
	}
	if object.Size != int64(len(content)) || object.SHA256 != digest(content) || object.Reused {
		t.Fatalf("PutImmutable() = %#v", object)
	}
	if !strings.HasPrefix(object.Key, "objects/") ||
		!strings.Contains(object.Key, "/derived/provider_raw_response/") ||
		strings.Contains(object.Key, assetID) || strings.Contains(object.Key, objectID) ||
		filepath.IsAbs(object.Key) {
		t.Fatalf("PutImmutable() returned unsafe or caller-derived key %q", object.Key)
	}
	assertOpenContent(t, store, object.Key, content)
	assertNoTemporaryFiles(t, root)
}

func TestLocalPutImmutableAcceptsBoundedAgentArtifactKinds(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{storage.ObjectKindClip, storage.ObjectKindExport} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			store := newStore(t, t.TempDir())
			content := []byte("bounded agent artifact")
			object, err := store.PutImmutable(
				context.Background(), "asset", "object-"+kind, kind,
				bytes.NewReader(content), int64(len(content)),
			)
			if err != nil {
				t.Fatalf("PutImmutable() error = %v", err)
			}
			if !strings.Contains(object.Key, "/derived/"+kind+"/") {
				t.Fatalf("object key = %q", object.Key)
			}
			assertOpenContent(t, store, object.Key, content)
		})
	}
}

func TestLocalPutImmutableIsIdempotentAndDetectsConflict(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t, root)
	first := []byte(`{"transcript":"first"}`)

	written, err := store.PutImmutable(
		context.Background(), "asset-raw", "provider-job-1", storage.ObjectKindProviderRawResponse,
		bytes.NewReader(first), 128,
	)
	if err != nil {
		t.Fatalf("first PutImmutable() error = %v", err)
	}
	retried, err := store.PutImmutable(
		context.Background(), "asset-raw", "provider-job-1", storage.ObjectKindProviderRawResponse,
		bytes.NewReader(first), 128,
	)
	if err != nil {
		t.Fatalf("retry PutImmutable() error = %v", err)
	}
	if !retried.Reused || retried.Key != written.Key || retried.Size != written.Size || retried.SHA256 != written.SHA256 {
		t.Fatalf("retry PutImmutable() = %#v, first = %#v", retried, written)
	}

	different := []byte(`{"transcript":"different"}`)
	_, err = store.PutImmutable(
		context.Background(), "asset-raw", "provider-job-1", storage.ObjectKindProviderRawResponse,
		bytes.NewReader(different), 128,
	)
	if !errors.Is(err, storage.ErrObjectConflict) {
		t.Fatalf("conflicting PutImmutable() error = %v, want %v", err, storage.ErrObjectConflict)
	}
	assertOpenContent(t, store, written.Key, first)
	assertNoTemporaryFiles(t, root)
}

func TestLocalConcurrentStoresNeverOverwriteImmutableObject(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stores := []*storage.Local{newStore(t, root), newStore(t, root)}
	contents := [][]byte{[]byte(`{"winner":"first"}`), []byte(`{"winner":"second"}`)}
	ready := make(chan struct{}, len(stores))
	release := make(chan struct{})
	type result struct {
		content []byte
		object  storage.Object
		err     error
	}
	results := make(chan result, len(stores))
	for index, store := range stores {
		index, store := index, store
		go func() {
			content := contents[index]
			object, err := store.PutImmutable(
				context.Background(),
				"shared-asset",
				"shared-object",
				storage.ObjectKindProviderRawResponse,
				&publishBarrierReader{content: content, ready: ready, release: release},
				int64(len(content)),
			)
			results <- result{content: content, object: object, err: err}
		}()
	}
	for range stores {
		<-ready
	}
	close(release)

	var winner result
	var successes, conflicts int
	for range stores {
		result := <-results
		switch {
		case result.err == nil:
			successes++
			winner = result
		case errors.Is(result.err, storage.ErrObjectConflict):
			conflicts++
		default:
			t.Fatalf("concurrent PutImmutable() error = %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent immutable results: successes = %d, conflicts = %d", successes, conflicts)
	}
	assertOpenContent(t, stores[0], winner.object.Key, winner.content)
	assertNoTemporaryFiles(t, root)
}

func TestLocalPutImmutableRejectsUnapprovedKindAndOversizeWithoutPublishing(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		assetID  string
		objectID string
		kind     string
		content  []byte
		maxBytes int64
		wantErr  error
	}{
		{
			name: "unapproved kind", assetID: "asset", objectID: "object", kind: "transcript",
			content: []byte("payload"), maxBytes: 64, wantErr: storage.ErrInvalidArgument,
		},
		{
			name: "kind traversal", assetID: "asset", objectID: "object", kind: "../provider_raw_response",
			content: []byte("payload"), maxBytes: 64, wantErr: storage.ErrInvalidArgument,
		},
		{
			name: "empty asset", assetID: " ", objectID: "object", kind: storage.ObjectKindProviderRawResponse,
			content: []byte("payload"), maxBytes: 64, wantErr: storage.ErrInvalidArgument,
		},
		{
			name: "empty object", assetID: "asset", objectID: " ", kind: storage.ObjectKindProviderRawResponse,
			content: []byte("payload"), maxBytes: 64, wantErr: storage.ErrInvalidArgument,
		},
		{
			name: "oversize", assetID: "asset", objectID: "object", kind: storage.ObjectKindProviderRawResponse,
			content: []byte("12345"), maxBytes: 4, wantErr: storage.ErrTooLarge,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			store := newStore(t, root)
			_, err := store.PutImmutable(
				context.Background(), test.assetID, test.objectID, test.kind,
				bytes.NewReader(test.content), test.maxBytes,
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("PutImmutable() error = %v, want %v", err, test.wantErr)
			}
			assertNoPublishedFiles(t, root)
			assertNoTemporaryFiles(t, root)
		})
	}
}

func TestLocalPutImmutableHonorsCancellationDuringStreaming(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	source := &cancelingReader{content: []byte("provider response"), cancel: cancel}

	_, err := store.PutImmutable(
		ctx, "asset-canceled", "job-canceled", storage.ObjectKindProviderRawResponse,
		source, 64,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PutImmutable() error = %v, want %v", err, context.Canceled)
	}
	assertNoPublishedFiles(t, root)
	assertNoTemporaryFiles(t, root)
}

func TestLocalAssembleRejectsInvalidPartsWithoutFinalObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(root string, parts []storage.PartRef) []storage.PartRef
		opts    func(whole []byte) storage.AssembleOptions
		wantErr error
	}{
		{
			name: "out of order",
			mutate: func(_ string, parts []storage.PartRef) []storage.PartRef {
				return []storage.PartRef{parts[1], parts[0]}
			},
			opts:    validAssembleOptions,
			wantErr: storage.ErrPartsOutOfOrder,
		},
		{
			name: "part key does not belong to upload",
			mutate: func(_ string, parts []storage.PartRef) []storage.PartRef {
				parts[0].Key = parts[1].Key
				return parts
			},
			opts:    validAssembleOptions,
			wantErr: storage.ErrInvalidKey,
		},
		{
			name: "part was modified",
			mutate: func(root string, parts []storage.PartRef) []storage.PartRef {
				if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(parts[0].Key)), []byte("xyz"), 0o600); err != nil {
					panic(err)
				}
				return parts
			},
			opts:    validAssembleOptions,
			wantErr: storage.ErrChecksumMismatch,
		},
		{
			name:   "wrong total size",
			mutate: func(_ string, parts []storage.PartRef) []storage.PartRef { return parts },
			opts: func(whole []byte) storage.AssembleOptions {
				opts := validAssembleOptions(whole)
				opts.ExpectedSize++
				opts.MaxBytes++
				return opts
			},
			wantErr: storage.ErrSizeMismatch,
		},
		{
			name:   "wrong whole checksum",
			mutate: func(_ string, parts []storage.PartRef) []storage.PartRef { return parts },
			opts: func(whole []byte) storage.AssembleOptions {
				opts := validAssembleOptions(whole)
				opts.ExpectedSHA256 = digest([]byte("not the whole"))
				return opts
			},
			wantErr: storage.ErrChecksumMismatch,
		},
		{
			name:   "aggregate too large",
			mutate: func(_ string, parts []storage.PartRef) []storage.PartRef { return parts },
			opts: func(whole []byte) storage.AssembleOptions {
				opts := validAssembleOptions(whole)
				opts.MaxBytes--
				return opts
			},
			wantErr: storage.ErrTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			store := newStore(t, root)
			firstBytes := []byte("abc")
			secondBytes := []byte("def")
			first := putPart(t, store, "upload-invalid", 1, firstBytes)
			second := putPart(t, store, "upload-invalid", 2, secondBytes)
			refs := test.mutate(root, []storage.PartRef{first.Ref(), second.Ref()})
			_, err := store.Assemble(
				context.Background(),
				"asset-"+test.name,
				"upload-invalid",
				refs,
				test.opts(append(firstBytes, secondBytes...)),
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Assemble() error = %v, want %v", err, test.wantErr)
			}
			assertNoObjects(t, root)
			assertNoTemporaryFiles(t, root)
		})
	}
}

func TestLocalOpenRejectsTraversalAndSymlinkEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t, root)
	for _, key := range []string{
		"",
		"../outside",
		"parts/../../outside",
		"/absolute/path",
		`parts\..\outside`,
		"C:/outside",
		"parts//file",
	} {
		if _, err := store.Open(context.Background(), key); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("Open(%q) error = %v, want %v", key, err, storage.ErrInvalidKey)
		}
	}

	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Logf("symlink unavailable; escape test skipped: %v", err)
		return
	}
	if _, err := store.Open(context.Background(), "escape/secret"); !errors.Is(err, storage.ErrInvalidKey) {
		t.Fatalf("Open() symlink escape error = %v, want %v", err, storage.ErrInvalidKey)
	}
}

func TestLocalRejectsIntermediateSymlinkForWriteAndDelete(t *testing.T) {
	t.Parallel()

	t.Run("write", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		store := newStore(t, root)
		uploadID := "symlink-write-upload"
		uploadDigest := digest([]byte(uploadID))
		if err := os.MkdirAll(filepath.Join(root, "parts"), 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "parts", uploadDigest[:2])
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("directory symlink unavailable: %v", err)
		}
		content := []byte("must stay inside the storage root")
		_, err := store.PutPart(context.Background(), uploadID, 1, bytes.NewReader(content), storage.PutPartOptions{
			ExpectedSize: int64(len(content)), ExpectedSHA256: digest(content), MaxBytes: int64(len(content)),
		})
		if !errors.Is(err, storage.ErrInvalidKey) {
			t.Fatalf("PutPart() error = %v, want %v", err, storage.ErrInvalidKey)
		}
		assertNoPublishedFiles(t, outside)
	})

	t.Run("delete", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		store := newStore(t, root)
		uploadID := "symlink-delete-upload"
		uploadDigest := digest([]byte(uploadID))
		outsideUpload := filepath.Join(outside, uploadDigest)
		if err := os.MkdirAll(outsideUpload, 0o700); err != nil {
			t.Fatal(err)
		}
		victim := filepath.Join(outsideUpload, "victim.part")
		if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "parts"), 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "parts", uploadDigest[:2])
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("directory symlink unavailable: %v", err)
		}
		if err := store.DeleteParts(context.Background(), uploadID); !errors.Is(err, storage.ErrInvalidKey) {
			t.Fatalf("DeleteParts() error = %v, want %v", err, storage.ErrInvalidKey)
		}
		if content, err := os.ReadFile(victim); err != nil || string(content) != "keep" {
			t.Fatalf("outside victim = %q, %v", content, err)
		}
	})
}

func TestLocalPublishedObjectSurvivesStoreReopen(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	content := []byte(`{"provider":"durable"}`)
	first := newStore(t, root)
	object, err := first.PutImmutable(
		context.Background(), "asset-durable", "job-durable", storage.ObjectKindProviderRawResponse,
		bytes.NewReader(content), int64(len(content)),
	)
	if err != nil {
		t.Fatalf("PutImmutable() error = %v", err)
	}

	reopened := newStore(t, root)
	assertOpenContent(t, reopened, object.Key, content)
	assertNoTemporaryFiles(t, root)
}

func TestLocalDeletePartsRemovesOnlyDerivedUploadDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t, root)
	first := putPart(t, store, "upload-delete", 1, []byte("first"))
	other := putPart(t, store, "upload-keep", 1, []byte("other"))

	if err := store.DeleteParts(context.Background(), "upload-delete"); err != nil {
		t.Fatalf("DeleteParts() error = %v", err)
	}
	if _, err := store.Open(context.Background(), first.Key); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open(deleted part) error = %v, want %v", err, os.ErrNotExist)
	}
	assertOpenContent(t, store, other.Key, []byte("other"))
}

func TestLocalDeleteObjectRequiresMatchingIntegrity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store := newStore(t, root)
	content := []byte("complete object")
	part := putPart(t, store, "upload-delete-object", 1, content)
	object, err := store.Assemble(
		context.Background(), "asset-delete-object", "upload-delete-object",
		[]storage.PartRef{part.Ref()}, storage.AssembleOptions{
			ExpectedSize: int64(len(content)), ExpectedSHA256: digest(content), MaxBytes: int64(len(content)),
		},
	)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if err := store.DeleteObject(context.Background(), object.Key, object.Size, digest([]byte("different"))); !errors.Is(err, storage.ErrObjectConflict) {
		t.Fatalf("mismatched DeleteObject() error = %v, want ErrObjectConflict", err)
	}
	assertOpenContent(t, store, object.Key, content)
	if err := store.DeleteObject(context.Background(), object.Key, object.Size, object.SHA256); err != nil {
		t.Fatalf("DeleteObject() error = %v", err)
	}
	if _, err := store.Open(context.Background(), object.Key); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open(deleted object) error = %v, want not exist", err)
	}
}

func TestLocalPutPartHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.PutPart(ctx, "upload", 1, bytes.NewReader([]byte("payload")), storage.PutPartOptions{
		ExpectedSHA256: digest([]byte("payload")),
		MaxBytes:       64,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PutPart() error = %v, want %v", err, context.Canceled)
	}
	assertNoPublishedFiles(t, root)
}

func newStore(t *testing.T, root string) *storage.Local {
	t.Helper()

	store, err := storage.NewLocal(root)
	if err != nil {
		t.Fatalf("NewLocal() error = %v", err)
	}
	return store
}

func putPart(t *testing.T, store *storage.Local, uploadID string, number int, content []byte) storage.Part {
	t.Helper()

	part, err := store.PutPart(context.Background(), uploadID, number, bytes.NewReader(content), storage.PutPartOptions{
		ExpectedSHA256: digest(content),
		MaxBytes:       int64(len(content)),
	})
	if err != nil {
		t.Fatalf("PutPart() error = %v", err)
	}
	return part
}

func validAssembleOptions(whole []byte) storage.AssembleOptions {
	return storage.AssembleOptions{
		ExpectedSize:   int64(len(whole)),
		ExpectedSHA256: digest(whole),
		MaxBytes:       int64(len(whole)),
	}
}

func assertOpenContent(t *testing.T, store *storage.Local, key string, want []byte) {
	t.Helper()

	file, err := store.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", key, err)
	}
	defer file.Close()
	got, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll(%q) error = %v", key, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Open(%q) content = %q, want %q", key, got, want)
	}
}

func assertNoPublishedFiles(t *testing.T, root string) {
	t.Helper()

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return fmt.Errorf("unexpected file %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertNoObjects(t *testing.T, root string) {
	t.Helper()

	objects := filepath.Join(root, "objects")
	err := filepath.WalkDir(objects, func(filename string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return fmt.Errorf("failed assemble left object file %s", filename)
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func assertNoTemporaryFiles(t *testing.T, root string) {
	t.Helper()

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), ".tmp-") {
			return fmt.Errorf("temporary file was not cleaned up: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func digest(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

type cancelingReader struct {
	content []byte
	cancel  context.CancelFunc
	read    bool
}

type publishBarrierReader struct {
	content []byte
	offset  int
	ready   chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (r *publishBarrierReader) Read(buffer []byte) (int, error) {
	if r.offset < len(r.content) {
		written := copy(buffer, r.content[r.offset:])
		r.offset += written
		return written, nil
	}
	r.once.Do(func() {
		r.ready <- struct{}{}
		<-r.release
	})
	return 0, io.EOF
}

func (r *cancelingReader) Read(buffer []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	written := copy(buffer, r.content)
	r.cancel()
	return written, nil
}

func TestLocalPutSnapshotRestoresExactKeyAndRejectsTampering(t *testing.T) {
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("snapshot bytes")
	key := "objects/snapshot/object"
	object, err := store.PutSnapshot(context.Background(), key, bytes.NewReader(content), int64(len(content)), digest(content))
	if err != nil {
		t.Fatalf("PutSnapshot() error = %v", err)
	}
	if object.Key != key || object.Backend != storage.BackendLocal || object.Reused {
		t.Fatalf("object = %+v", object)
	}
	reused, err := store.PutSnapshot(context.Background(), key, bytes.NewReader(content), int64(len(content)), digest(content))
	if err != nil || !reused.Reused {
		t.Fatalf("replay = %+v/%v, want reuse", reused, err)
	}
	_, err = store.PutSnapshot(context.Background(), key, bytes.NewReader([]byte("tampered")), int64(len("tampered")), digest([]byte("tampered")))
	if !errors.Is(err, storage.ErrObjectConflict) {
		t.Fatalf("tampered restore error = %v, want object conflict", err)
	}
}
