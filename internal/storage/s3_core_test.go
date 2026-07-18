package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type fakeS3Object struct {
	data []byte
	etag string
}

type fakeS3Client struct {
	mu                    sync.Mutex
	objects               map[string]fakeS3Object
	version               int
	listPageSize          int
	listCalls             int
	listOverride          func(string, string, int) (s3ListResult, error)
	mutateBeforeDeleteKey string
}

func newFakeS3Client() *fakeS3Client {
	return &fakeS3Client{objects: make(map[string]fakeS3Object)}
}

func (client *fakeS3Client) PutIfAbsent(
	ctx context.Context,
	key string,
	source io.ReadSeeker,
	size int64,
	digest string,
) (bool, error) {
	if err := contextError(ctx); err != nil {
		return false, err
	}
	data, err := io.ReadAll(source)
	if err != nil {
		return false, err
	}
	if int64(len(data)) != size || testSHA256(data) != digest {
		return false, errors.New("fake client received invalid publication metadata")
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if _, exists := client.objects[key]; exists {
		return false, nil
	}
	client.version++
	client.objects[key] = fakeS3Object{data: bytes.Clone(data), etag: fmt.Sprintf("etag-%d", client.version)}
	return true, nil
}

func (client *fakeS3Client) Get(ctx context.Context, key string) (s3GetResult, error) {
	if err := contextError(ctx); err != nil {
		return s3GetResult{}, err
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	object, exists := client.objects[key]
	if !exists {
		return s3GetResult{}, errS3NotFound
	}
	data := bytes.Clone(object.data)
	return s3GetResult{
		Body: io.NopCloser(bytes.NewReader(data)),
		Size: int64(len(data)),
		ETag: object.etag,
	}, nil
}

func (client *fakeS3Client) List(
	ctx context.Context,
	prefix,
	token string,
	limit int,
) (s3ListResult, error) {
	if err := contextError(ctx); err != nil {
		return s3ListResult{}, err
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	client.listCalls++
	if client.listOverride != nil {
		return client.listOverride(prefix, token, limit)
	}
	keys := make([]string, 0)
	for key := range client.objects {
		if strings.HasPrefix(key, prefix) && (token == "" || key > token) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	pageSize := limit
	if client.listPageSize > 0 && client.listPageSize < pageSize {
		pageSize = client.listPageSize
	}
	if len(keys) <= pageSize {
		return s3ListResult{Keys: keys}, nil
	}
	return s3ListResult{Keys: keys[:pageSize], NextToken: keys[pageSize-1]}, nil
}

func (client *fakeS3Client) DeleteIfMatch(ctx context.Context, key, etag string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	object, exists := client.objects[key]
	if !exists {
		return errS3NotFound
	}
	if client.mutateBeforeDeleteKey == key {
		client.mutateBeforeDeleteKey = ""
		client.version++
		object.data = append(bytes.Clone(object.data), '!')
		object.etag = fmt.Sprintf("etag-%d", client.version)
		client.objects[key] = object
	}
	if etag != "*" && etag != object.etag {
		return errS3PreconditionFailed
	}
	delete(client.objects, key)
	return nil
}

func (client *fakeS3Client) set(key string, data []byte) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.version++
	client.objects[key] = fakeS3Object{data: bytes.Clone(data), etag: fmt.Sprintf("etag-%d", client.version)}
}

func (client *fakeS3Client) data(key string) ([]byte, bool) {
	client.mu.Lock()
	defer client.mu.Unlock()
	object, exists := client.objects[key]
	return bytes.Clone(object.data), exists
}

func newFakeS3Store(t *testing.T, client *fakeS3Client) *S3 {
	t.Helper()
	store, err := newS3WithClient(S3Config{
		Region:   "test-1",
		Bucket:   "voice-assets-test",
		Prefix:   "tenant/test",
		TempRoot: t.TempDir(),
	}, client)
	if err != nil {
		t.Fatalf("newS3WithClient() error = %v", err)
	}
	return store
}

func testSHA256(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func testPutPart(t *testing.T, store *S3, uploadID string, number int, content []byte) Part {
	t.Helper()
	part, err := store.PutPart(context.Background(), uploadID, number, bytes.NewReader(content), PutPartOptions{
		ExpectedSize:   int64(len(content)),
		ExpectedSHA256: testSHA256(content),
		MaxBytes:       int64(len(content)),
	})
	if err != nil {
		t.Fatalf("PutPart() error = %v", err)
	}
	return part
}

func assertS3TempRootEmpty(t *testing.T, store *S3) {
	t.Helper()
	entries, err := os.ReadDir(store.tempRoot)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", store.tempRoot, err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary root contains %d entries", len(entries))
	}
}

func TestS3PutPartIsCreateOnlyIdempotentAndBounded(t *testing.T) {
	t.Parallel()

	client := newFakeS3Client()
	store := newFakeS3Store(t, client)
	content := []byte("same remote part")
	options := PutPartOptions{
		ExpectedSize:   int64(len(content)),
		ExpectedSHA256: testSHA256(content),
		MaxBytes:       64,
	}
	first, err := store.PutPart(context.Background(), "upload-1", 4, bytes.NewReader(content), options)
	if err != nil {
		t.Fatalf("first PutPart() error = %v", err)
	}
	if first.Reused {
		t.Fatal("first PutPart() unexpectedly reused an object")
	}
	retry, err := store.PutPart(context.Background(), "upload-1", 4, bytes.NewReader(content), options)
	if err != nil {
		t.Fatalf("retry PutPart() error = %v", err)
	}
	if !retry.Reused || retry.Key != first.Key {
		t.Fatalf("retry PutPart() = %#v, first = %#v", retry, first)
	}
	different := []byte("different bytes")
	_, err = store.PutPart(context.Background(), "upload-1", 4, bytes.NewReader(different), PutPartOptions{
		ExpectedSize:   int64(len(different)),
		ExpectedSHA256: testSHA256(different),
		MaxBytes:       64,
	})
	if !errors.Is(err, ErrPartConflict) {
		t.Fatalf("conflicting PutPart() error = %v, want %v", err, ErrPartConflict)
	}
	stored, exists := client.data(store.fullKey(first.Key))
	if !exists || !bytes.Equal(stored, content) {
		t.Fatalf("published part = %q, exists = %v", stored, exists)
	}
	_, err = store.PutPart(context.Background(), "upload-2", 1, bytes.NewReader(nil), PutPartOptions{
		ExpectedSHA256: testSHA256(nil),
		MaxBytes:       maxS3ObjectBytes + 1,
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized PutPart() error = %v, want %v", err, ErrTooLarge)
	}
	assertS3TempRootEmpty(t, store)
}

func TestS3ConcurrentStoresNeverOverwritePublishedPart(t *testing.T) {
	t.Parallel()

	client := newFakeS3Client()
	stores := []*S3{newFakeS3Store(t, client), newFakeS3Store(t, client)}
	contents := [][]byte{[]byte("first contender"), []byte("second contender")}
	start := make(chan struct{})
	type result struct {
		part    Part
		content []byte
		err     error
	}
	results := make(chan result, len(stores))
	for index, store := range stores {
		index, store := index, store
		go func() {
			<-start
			content := contents[index]
			part, err := store.PutPart(context.Background(), "shared-upload", 1, bytes.NewReader(content), PutPartOptions{
				ExpectedSize:   int64(len(content)),
				ExpectedSHA256: testSHA256(content),
				MaxBytes:       64,
			})
			results <- result{part: part, content: content, err: err}
		}()
	}
	close(start)

	var winner result
	var successes, conflicts int
	for range stores {
		result := <-results
		switch {
		case result.err == nil:
			successes++
			winner = result
		case errors.Is(result.err, ErrPartConflict):
			conflicts++
		default:
			t.Fatalf("concurrent PutPart() error = %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes = %d, conflicts = %d", successes, conflicts)
	}
	stored, exists := client.data(stores[0].fullKey(winner.part.Key))
	if !exists || !bytes.Equal(stored, winner.content) {
		t.Fatalf("published part = %q, exists = %v", stored, exists)
	}
}

func TestS3AssembleVerifiesEveryPartAndReusesExactObject(t *testing.T) {
	t.Parallel()

	client := newFakeS3Client()
	store := newFakeS3Store(t, client)
	firstContent := []byte("first-")
	secondContent := []byte("second")
	first := testPutPart(t, store, "upload-assemble", 1, firstContent)
	second := testPutPart(t, store, "upload-assemble", 2, secondContent)
	content := append(bytes.Clone(firstContent), secondContent...)
	options := AssembleOptions{
		ExpectedSize:   int64(len(content)),
		ExpectedSHA256: testSHA256(content),
		MaxBytes:       64,
	}
	object, err := store.Assemble(context.Background(), "asset-1", "upload-assemble", []PartRef{first.Ref(), second.Ref()}, options)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if object.Backend != BackendS3 || object.Reused || object.Size != int64(len(content)) {
		t.Fatalf("Assemble() = %#v", object)
	}
	stored, exists := client.data(store.fullKey(object.Key))
	if !exists || !bytes.Equal(stored, content) {
		t.Fatalf("assembled object = %q, exists = %v", stored, exists)
	}
	retry, err := store.Assemble(context.Background(), "asset-1", "upload-assemble", []PartRef{first.Ref(), second.Ref()}, options)
	if err != nil {
		t.Fatalf("retry Assemble() error = %v", err)
	}
	if !retry.Reused || retry.Key != object.Key {
		t.Fatalf("retry Assemble() = %#v, first = %#v", retry, object)
	}

	client.set(store.fullKey(second.Key), []byte("tamper"))
	_, err = store.Assemble(context.Background(), "asset-2", "upload-assemble", []PartRef{first.Ref(), second.Ref()}, options)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("tampered Assemble() error = %v, want %v", err, ErrChecksumMismatch)
	}
	assertS3TempRootEmpty(t, store)
}

func TestS3PutImmutableAndOpenUseDisposableSnapshot(t *testing.T) {
	t.Parallel()

	client := newFakeS3Client()
	store := newFakeS3Store(t, client)
	content := []byte("immutable remote artifact")
	object, err := store.PutImmutable(
		context.Background(),
		"asset-open",
		"artifact-open",
		ObjectKindExport,
		bytes.NewReader(content),
		64,
	)
	if err != nil {
		t.Fatalf("PutImmutable() error = %v", err)
	}
	if object.Backend != BackendS3 || object.Reused {
		t.Fatalf("PutImmutable() = %#v", object)
	}
	retry, err := store.PutImmutable(
		context.Background(),
		"asset-open",
		"artifact-open",
		ObjectKindExport,
		bytes.NewReader(content),
		64,
	)
	if err != nil || !retry.Reused {
		t.Fatalf("retry PutImmutable() = %#v, error = %v", retry, err)
	}

	file, err := store.Open(context.Background(), object.Key)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	temporaryName := file.Name()
	if _, err := os.Stat(temporaryName); err != nil {
		t.Fatalf("temporary snapshot Stat() error = %v", err)
	}
	read, err := io.ReadAll(file)
	if err != nil || !bytes.Equal(read, content) {
		t.Fatalf("snapshot read = %q, error = %v", read, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("snapshot Close() error = %v", err)
	}
	if _, err := os.Stat(temporaryName); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary snapshot still exists: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("second snapshot Close() error = %v", err)
	}
	assertS3TempRootEmpty(t, store)
}

func TestS3OpenRejectsInvalidRemoteSizeAndCancellation(t *testing.T) {
	t.Parallel()

	client := newFakeS3Client()
	store := newFakeS3Store(t, client)
	key := "objects/aa/object/original"
	client.set(store.fullKey(key), []byte("content"))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Open(canceled, key); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open(canceled) error = %v, want %v", err, context.Canceled)
	}

	client.mu.Lock()
	object := client.objects[store.fullKey(key)]
	client.mu.Unlock()
	oversized := &fixedGetS3Client{
		fakeS3Client: client,
		result: s3GetResult{
			Body: io.NopCloser(bytes.NewReader(nil)),
			Size: maxS3ObjectBytes + 1,
			ETag: object.etag,
		},
	}
	oversizedStore, err := newS3WithClient(store.config, oversized)
	if err != nil {
		t.Fatalf("newS3WithClient() error = %v", err)
	}
	if _, err := oversizedStore.Open(context.Background(), key); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Open(oversized) error = %v, want %v", err, ErrTooLarge)
	}
}

type fixedGetS3Client struct {
	*fakeS3Client
	result s3GetResult
}

func (client *fixedGetS3Client) Get(context.Context, string) (s3GetResult, error) {
	return client.result, nil
}

func TestS3DeleteObjectRequiresFullIntegrityAndETagMatch(t *testing.T) {
	t.Parallel()

	client := newFakeS3Client()
	store := newFakeS3Store(t, client)
	content := []byte("delete only these exact bytes")
	object, err := store.PutImmutable(
		context.Background(),
		"asset-delete",
		"artifact-delete",
		ObjectKindProviderRawResponse,
		bytes.NewReader(content),
		64,
	)
	if err != nil {
		t.Fatalf("PutImmutable() error = %v", err)
	}
	fullKey := store.fullKey(object.Key)
	if err := store.DeleteObject(context.Background(), object.Key, object.Size, testSHA256([]byte("wrong"))); !errors.Is(err, ErrObjectConflict) {
		t.Fatalf("DeleteObject(wrong hash) error = %v, want %v", err, ErrObjectConflict)
	}
	if _, exists := client.data(fullKey); !exists {
		t.Fatal("DeleteObject(wrong hash) removed object")
	}

	client.mu.Lock()
	client.mutateBeforeDeleteKey = fullKey
	client.mu.Unlock()
	if err := store.DeleteObject(context.Background(), object.Key, object.Size, object.SHA256); !errors.Is(err, ErrObjectConflict) {
		t.Fatalf("DeleteObject(race) error = %v, want %v", err, ErrObjectConflict)
	}
	mutated, exists := client.data(fullKey)
	if !exists || bytes.Equal(mutated, content) {
		t.Fatalf("racing object = %q, exists = %v", mutated, exists)
	}
	if err := store.DeleteObject(context.Background(), object.Key, int64(len(mutated)), testSHA256(mutated)); err != nil {
		t.Fatalf("DeleteObject(mutated object) error = %v", err)
	}
	if _, exists := client.data(fullKey); exists {
		t.Fatal("DeleteObject() left object behind")
	}
	if err := store.DeleteObject(context.Background(), object.Key, int64(len(mutated)), testSHA256(mutated)); err != nil {
		t.Fatalf("DeleteObject(missing object) error = %v", err)
	}
}

func TestS3DeletePartsUsesExactPrefixAndPagination(t *testing.T) {
	t.Parallel()

	client := newFakeS3Client()
	client.listPageSize = 1
	store := newFakeS3Store(t, client)
	for number := 1; number <= 3; number++ {
		testPutPart(t, store, "upload-delete", number, []byte("part-"+strconv.Itoa(number)))
	}
	foreign := testPutPart(t, store, "upload-keep", 1, []byte("keep"))
	object, err := store.PutImmutable(
		context.Background(),
		"asset-keep",
		"artifact-keep",
		ObjectKindWaveform,
		bytes.NewReader([]byte("png")),
		16,
	)
	if err != nil {
		t.Fatalf("PutImmutable() error = %v", err)
	}
	if err := store.DeleteParts(context.Background(), "upload-delete"); err != nil {
		t.Fatalf("DeleteParts() error = %v", err)
	}
	if client.listCalls < 3 {
		t.Fatalf("DeleteParts() list calls = %d, want pagination", client.listCalls)
	}
	for number := 1; number <= 3; number++ {
		key, keyErr := generatedPartKey("upload-delete", number)
		if keyErr != nil {
			t.Fatalf("generatedPartKey() error = %v", keyErr)
		}
		if _, exists := client.data(store.fullKey(key)); exists {
			t.Fatalf("deleted upload part %d still exists", number)
		}
	}
	if _, exists := client.data(store.fullKey(foreign.Key)); !exists {
		t.Fatal("DeleteParts() removed a different upload")
	}
	if _, exists := client.data(store.fullKey(object.Key)); !exists {
		t.Fatal("DeleteParts() removed a non-part object")
	}
}

func TestS3DeletePartsRejectsForeignKeysAndContinuationCycles(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		override func(string, string, int) (s3ListResult, error)
	}{
		{
			name: "foreign key",
			override: func(_ string, _ string, _ int) (s3ListResult, error) {
				return s3ListResult{Keys: []string{"tenant/test/objects/not-a-part"}}, nil
			},
		},
		{
			name: "continuation cycle",
			override: func(prefix, token string, _ int) (s3ListResult, error) {
				next := "token-a"
				if token == "token-a" {
					next = "token-b"
				} else if token == "token-b" {
					next = "token-a"
				}
				return s3ListResult{Keys: []string{prefix + "1.part"}, NextToken: next}, nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := newFakeS3Client()
			client.listOverride = test.override
			store := newFakeS3Store(t, client)
			if err := store.DeleteParts(context.Background(), "upload"); err == nil || !strings.Contains(err.Error(), "invalid S3 part listing") {
				t.Fatalf("DeleteParts() error = %v", err)
			}
		})
	}
}

func TestS3RejectsCanceledMutationWithoutPublishing(t *testing.T) {
	t.Parallel()

	client := newFakeS3Client()
	store := newFakeS3Store(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	content := []byte("never publish")
	_, err := store.PutPart(ctx, "upload-canceled", 1, bytes.NewReader(content), PutPartOptions{
		ExpectedSize:   int64(len(content)),
		ExpectedSHA256: testSHA256(content),
		MaxBytes:       64,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PutPart(canceled) error = %v, want %v", err, context.Canceled)
	}
	if len(client.objects) != 0 {
		t.Fatalf("canceled PutPart() published %d objects", len(client.objects))
	}
	assertS3TempRootEmpty(t, store)
}

func TestS3PutSnapshotRestoresExactKeyAndRejectsTampering(t *testing.T) {
	t.Parallel()

	client := newFakeS3Client()
	store := newFakeS3Store(t, client)
	content := []byte("snapshot bytes")
	key := "objects/snapshot/object"
	object, err := store.PutSnapshot(context.Background(), key, bytes.NewReader(content), int64(len(content)), testSHA256(content))
	if err != nil {
		t.Fatalf("PutSnapshot() error = %v", err)
	}
	if object.Key != key || object.Backend != BackendS3 || object.Reused {
		t.Fatalf("object = %+v", object)
	}
	reused, err := store.PutSnapshot(context.Background(), key, bytes.NewReader(content), int64(len(content)), testSHA256(content))
	if err != nil || !reused.Reused {
		t.Fatalf("replay = %+v/%v, want reuse", reused, err)
	}
	_, err = store.PutSnapshot(context.Background(), key, bytes.NewReader([]byte("tampered")), int64(len("tampered")), testSHA256([]byte("tampered")))
	if !errors.Is(err, errS3PreconditionFailed) && !errors.Is(err, ErrObjectConflict) {
		t.Fatalf("tampered restore error = %v, want conflict", err)
	}
	assertS3TempRootEmpty(t, store)
}
