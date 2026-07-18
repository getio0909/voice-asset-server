package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

const (
	maxS3ObjectBytes = int64(512 * 1024 * 1024)
	maxS3ListKeys    = 10_000
)

var (
	errS3NotFound           = errors.New("S3 object not found")
	errS3PreconditionFailed = errors.New("S3 precondition failed")
)

type s3GetResult struct {
	Body io.ReadCloser
	Size int64
	ETag string
}

type s3ListResult struct {
	Keys      []string
	NextToken string
}

// s3ObjectClient is the small protocol seam implemented by the approved SDK.
// All keys are already prefixed and all error details must be sanitized.
type s3ObjectClient interface {
	PutIfAbsent(context.Context, string, io.ReadSeeker, int64, string) (bool, error)
	Get(context.Context, string) (s3GetResult, error)
	List(context.Context, string, string, int) (s3ListResult, error)
	DeleteIfMatch(context.Context, string, string) error
}

// S3 implements storage semantics independently from any particular SDK.
// Constructing the production protocol client remains in the SDK adapter.
type S3 struct {
	config    S3Config
	client    s3ObjectClient
	tempRoot  string
	lifecycle sync.RWMutex
	locks     [64]sync.Mutex
}

func newS3WithClient(config S3Config, client s3ObjectClient) (*S3, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: S3 client is nil", ErrInvalidArgument)
	}
	if err := (Config{Backend: BackendS3, S3: config}).Validate(); err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(config.TempRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve S3 temporary root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create S3 temporary root: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve S3 temporary root links: %w", err)
	}
	return &S3{config: config, client: client, tempRoot: filepath.Clean(resolvedRoot)}, nil
}

func (*S3) Backend() Backend { return BackendS3 }

func (store *S3) PutPart(
	ctx context.Context,
	uploadID string,
	partNumber int,
	source io.Reader,
	options PutPartOptions,
) (Part, error) {
	if err := contextError(ctx); err != nil {
		return Part{}, err
	}
	if source == nil || partNumber <= 0 || options.ExpectedSize < 0 {
		return Part{}, fmt.Errorf("%w: invalid S3 part input", ErrInvalidArgument)
	}
	expectedHash, err := normalizeSHA256(options.ExpectedSHA256)
	if err != nil {
		return Part{}, err
	}
	if err := validateLimit(options.MaxBytes); err != nil {
		return Part{}, err
	}
	if options.ExpectedSize > options.MaxBytes || options.MaxBytes > maxS3ObjectBytes {
		return Part{}, fmt.Errorf("%w: expected part exceeds maximum", ErrTooLarge)
	}
	key, err := generatedPartKey(uploadID, partNumber)
	if err != nil {
		return Part{}, err
	}
	file, size, actualHash, err := store.stage(ctx, source, options.MaxBytes)
	if err != nil {
		return Part{}, fmt.Errorf("stage S3 part: %w", err)
	}
	defer file.Close()
	if options.ExpectedSize > 0 && size != options.ExpectedSize {
		return Part{}, fmt.Errorf("%w: part expected %d bytes, got %d", ErrSizeMismatch, options.ExpectedSize, size)
	}
	if actualHash != expectedHash {
		return Part{}, fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, expectedHash, actualHash)
	}
	reused, err := store.publish(ctx, key, file, size, actualHash, ErrPartConflict)
	if err != nil {
		return Part{}, err
	}
	return Part{Number: partNumber, Key: key, Size: size, SHA256: actualHash, Reused: reused}, nil
}

func (store *S3) Assemble(
	ctx context.Context,
	assetID,
	uploadID string,
	parts []PartRef,
	options AssembleOptions,
) (Object, error) {
	if err := contextError(ctx); err != nil {
		return Object{}, err
	}
	if len(parts) == 0 || options.ExpectedSize < 0 {
		return Object{}, fmt.Errorf("%w: parts are empty or expected size is negative", ErrInvalidArgument)
	}
	if err := validateLimit(options.MaxBytes); err != nil {
		return Object{}, err
	}
	if options.ExpectedSize > options.MaxBytes || options.MaxBytes > maxS3ObjectBytes {
		return Object{}, fmt.Errorf("%w: S3 object exceeds maximum", ErrTooLarge)
	}
	expectedHash, err := normalizeSHA256(options.ExpectedSHA256)
	if err != nil {
		return Object{}, err
	}
	if err := validatePartRefs(uploadID, parts, options); err != nil {
		return Object{}, err
	}
	key, err := generatedObjectKey(assetID)
	if err != nil {
		return Object{}, err
	}
	match, exists, err := store.objectMatches(ctx, key, options.ExpectedSize, expectedHash)
	if err != nil {
		return Object{}, fmt.Errorf("inspect S3 object: %w", err)
	}
	if exists {
		if match {
			return Object{Backend: BackendS3, Key: key, Size: options.ExpectedSize, SHA256: expectedHash, Reused: true}, nil
		}
		return Object{}, fmt.Errorf("%w: asset already contains different bytes", ErrObjectConflict)
	}

	file, err := store.temporary("voiceasset-s3-assemble-*")
	if err != nil {
		return Object{}, err
	}
	defer file.Close()
	digest := sha256.New()
	var total int64
	for _, part := range parts {
		result, getErr := store.client.Get(ctx, store.fullKey(part.Key))
		if getErr != nil {
			return Object{}, fmt.Errorf("open S3 part %d: %w", part.Number, getErr)
		}
		if result.Body == nil || result.Size != part.Size {
			if result.Body != nil {
				_ = result.Body.Close()
			}
			return Object{}, fmt.Errorf("%w: part %d size changed", ErrSizeMismatch, part.Number)
		}
		remaining := options.MaxBytes - total
		size, partHash, copyErr := copyBounded(ctx, io.MultiWriter(file, digest), result.Body, remaining)
		closeErr := result.Body.Close()
		if copyErr != nil {
			return Object{}, fmt.Errorf("stream S3 part %d: %w", part.Number, copyErr)
		}
		if closeErr != nil {
			return Object{}, fmt.Errorf("close S3 part %d: %w", part.Number, closeErr)
		}
		if size != part.Size {
			return Object{}, fmt.Errorf("%w: part %d expected %d bytes, got %d", ErrSizeMismatch, part.Number, part.Size, size)
		}
		if partHash != part.SHA256 {
			return Object{}, fmt.Errorf("%w: part %d checksum changed", ErrChecksumMismatch, part.Number)
		}
		total += size
	}
	if total != options.ExpectedSize {
		return Object{}, fmt.Errorf("%w: object expected %d bytes, got %d", ErrSizeMismatch, options.ExpectedSize, total)
	}
	actualHash := hex.EncodeToString(digest.Sum(nil))
	if actualHash != expectedHash {
		return Object{}, fmt.Errorf("%w: object expected %s, got %s", ErrChecksumMismatch, expectedHash, actualHash)
	}
	if err := syncAndRewind(file.File); err != nil {
		return Object{}, fmt.Errorf("flush assembled S3 object: %w", err)
	}
	reused, err := store.publish(ctx, key, file, total, actualHash, ErrObjectConflict)
	if err != nil {
		return Object{}, err
	}
	return Object{Backend: BackendS3, Key: key, Size: total, SHA256: actualHash, Reused: reused}, nil
}

func (store *S3) PutImmutable(
	ctx context.Context,
	assetID,
	objectID,
	kind string,
	source io.Reader,
	maxBytes int64,
) (Object, error) {
	if err := contextError(ctx); err != nil {
		return Object{}, err
	}
	if source == nil {
		return Object{}, fmt.Errorf("%w: source is nil", ErrInvalidArgument)
	}
	if err := validateLimit(maxBytes); err != nil {
		return Object{}, err
	}
	if maxBytes > maxS3ObjectBytes {
		return Object{}, fmt.Errorf("%w: S3 object exceeds maximum", ErrTooLarge)
	}
	key, err := generatedImmutableObjectKey(assetID, objectID, kind)
	if err != nil {
		return Object{}, err
	}
	file, size, actualHash, err := store.stage(ctx, source, maxBytes)
	if err != nil {
		return Object{}, fmt.Errorf("stage immutable S3 object: %w", err)
	}
	defer file.Close()
	reused, err := store.publish(ctx, key, file, size, actualHash, ErrObjectConflict)
	if err != nil {
		return Object{}, err
	}
	return Object{Backend: BackendS3, Key: key, Size: size, SHA256: actualHash, Reused: reused}, nil
}

// PutSnapshot restores an exact immutable key for the offline backup path.
// Existing bytes are reused only when their full size and SHA-256 match.
func (store *S3) PutSnapshot(
	ctx context.Context,
	key string,
	source io.Reader,
	expectedSize int64,
	expectedSHA256 string,
) (Object, error) {
	if err := contextError(ctx); err != nil {
		return Object{}, err
	}
	if source == nil || expectedSize < 0 {
		return Object{}, fmt.Errorf("%w: invalid snapshot source or size", ErrInvalidArgument)
	}
	if err := validateKey(key); err != nil {
		return Object{}, err
	}
	if expectedSize > maxS3ObjectBytes {
		return Object{}, fmt.Errorf("%w: snapshot exceeds maximum", ErrTooLarge)
	}
	expectedHash, err := normalizeSHA256(expectedSHA256)
	if err != nil {
		return Object{}, err
	}
	file, size, actualHash, err := store.stage(ctx, source, expectedSize)
	if err != nil {
		return Object{}, fmt.Errorf("stage snapshot: %w", err)
	}
	defer file.Close()
	if size != expectedSize || actualHash != expectedHash {
		return Object{}, fmt.Errorf("%w: snapshot integrity does not match manifest", ErrChecksumMismatch)
	}
	reused, err := store.publish(ctx, key, file, size, actualHash, ErrObjectConflict)
	if err != nil {
		return Object{}, fmt.Errorf("publish snapshot: %w", err)
	}
	return Object{Backend: BackendS3, Key: key, Size: size, SHA256: actualHash, Reused: reused}, nil
}

func (store *S3) Open(ctx context.Context, key string) (File, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if err := validateKey(key); err != nil {
		return nil, err
	}
	result, err := store.client.Get(ctx, store.fullKey(key))
	if err != nil {
		return nil, fmt.Errorf("get S3 object: %w", err)
	}
	if result.Body == nil || result.Size < 0 || result.Size > maxS3ObjectBytes {
		if result.Body != nil {
			_ = result.Body.Close()
		}
		return nil, fmt.Errorf("%w: invalid S3 object size", ErrTooLarge)
	}
	file, err := store.temporary("voiceasset-s3-read-*")
	if err != nil {
		_ = result.Body.Close()
		return nil, err
	}
	size, _, copyErr := copyBounded(ctx, file, result.Body, maxS3ObjectBytes)
	closeErr := result.Body.Close()
	if copyErr != nil || closeErr != nil || size != result.Size {
		_ = file.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("download S3 object: %w", copyErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close S3 response: %w", closeErr)
		}
		return nil, fmt.Errorf("%w: S3 response size changed", ErrSizeMismatch)
	}
	if err := syncAndRewind(file.File); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("flush S3 object snapshot: %w", err)
	}
	return file, nil
}

// ListKeys returns up to limit keys below prefix. It is used only to prove
// that a clean restore target contains no pre-existing objects.
func (store *S3) ListKeys(ctx context.Context, prefix string, limit int) ([]string, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxS3ListKeys {
		return nil, fmt.Errorf("%w: invalid S3 list limit", ErrInvalidArgument)
	}
	if prefix != "" {
		if err := validateKey(prefix); err != nil {
			return nil, err
		}
	}
	result, err := store.client.List(ctx, store.fullKey(prefix), "", limit)
	if err != nil {
		return nil, fmt.Errorf("list S3 snapshot keys: %w", err)
	}
	keys := make([]string, 0, len(result.Keys))
	base := strings.TrimSuffix(store.config.Prefix, "/")
	for _, key := range result.Keys {
		key = strings.TrimPrefix(key, base)
		key = strings.TrimPrefix(key, "/")
		if err := validateKey(key); err != nil {
			return nil, fmt.Errorf("invalid S3 object listing: %w", err)
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func (store *S3) DeleteParts(ctx context.Context, uploadID string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	digest, err := identifierDigest(uploadID)
	if err != nil {
		return err
	}
	prefix := store.fullKey(path.Join("parts", digest[:2], digest)) + "/"
	token := ""
	tokens := map[string]struct{}{"": {}}
	seen := 0
	for {
		result, err := store.client.List(ctx, prefix, token, 1000)
		if err != nil {
			return fmt.Errorf("list S3 upload parts: %w", err)
		}
		if len(result.Keys) == 0 && result.NextToken != "" {
			return fmt.Errorf("invalid S3 part listing continuation")
		}
		for _, key := range result.Keys {
			seen++
			if seen > maxS3ListKeys || !strings.HasPrefix(key, prefix) {
				return fmt.Errorf("invalid S3 part listing")
			}
			if err := store.client.DeleteIfMatch(ctx, key, "*"); err != nil && !errors.Is(err, errS3NotFound) {
				return fmt.Errorf("delete S3 upload part: %w", err)
			}
		}
		if result.NextToken == "" {
			return nil
		}
		if _, duplicate := tokens[result.NextToken]; duplicate {
			return fmt.Errorf("invalid S3 part listing continuation")
		}
		token = result.NextToken
		tokens[token] = struct{}{}
	}
}

func (store *S3) DeleteObject(
	ctx context.Context,
	key string,
	expectedSize int64,
	expectedSHA256 string,
) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if expectedSize < 0 || expectedSize > maxS3ObjectBytes {
		return fmt.Errorf("%w: invalid expected S3 object size", ErrInvalidArgument)
	}
	expectedHash, err := normalizeSHA256(expectedSHA256)
	if err != nil {
		return err
	}
	if err := validateKey(key); err != nil {
		return err
	}
	lock := store.lockFor(store.fullKey(key))
	lock.Lock()
	defer lock.Unlock()
	match, exists, etag, err := store.objectMatchWithETag(ctx, key, expectedSize, expectedHash)
	if err != nil {
		return fmt.Errorf("inspect S3 object before deletion: %w", err)
	}
	if !exists {
		return nil
	}
	if !match || strings.TrimSpace(etag) == "" {
		return fmt.Errorf("%w: object integrity changed before deletion", ErrObjectConflict)
	}
	if err := store.client.DeleteIfMatch(ctx, store.fullKey(key), etag); err != nil {
		if errors.Is(err, errS3PreconditionFailed) {
			return fmt.Errorf("%w: object changed during deletion", ErrObjectConflict)
		}
		if errors.Is(err, errS3NotFound) {
			return nil
		}
		return fmt.Errorf("delete S3 object: %w", err)
	}
	return nil
}

func (store *S3) stage(ctx context.Context, source io.Reader, maxBytes int64) (*temporaryFile, int64, string, error) {
	file, err := store.temporary("voiceasset-s3-stage-*")
	if err != nil {
		return nil, 0, "", err
	}
	size, digest, err := copyBounded(ctx, file, source, maxBytes)
	if err != nil {
		_ = file.Close()
		return nil, size, digest, err
	}
	if err := syncAndRewind(file.File); err != nil {
		_ = file.Close()
		return nil, size, digest, err
	}
	return file, size, digest, nil
}

func (store *S3) publish(
	ctx context.Context,
	key string,
	file *temporaryFile,
	size int64,
	digest string,
	conflict error,
) (bool, error) {
	lock := store.lockFor(store.fullKey(key))
	lock.Lock()
	defer lock.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return false, fmt.Errorf("rewind staged S3 object: %w", err)
		}
		created, err := store.client.PutIfAbsent(ctx, store.fullKey(key), file, size, digest)
		if err != nil {
			return false, fmt.Errorf("publish S3 object: %w", err)
		}
		if created {
			return false, nil
		}
		match, exists, err := store.objectMatches(ctx, key, size, digest)
		if err != nil {
			return false, err
		}
		if exists {
			if match {
				return true, nil
			}
			return false, conflict
		}
	}
	return false, conflict
}

func (store *S3) objectMatches(ctx context.Context, key string, expectedSize int64, expectedHash string) (bool, bool, error) {
	match, exists, _, err := store.objectMatchWithETag(ctx, key, expectedSize, expectedHash)
	return match, exists, err
}

func (store *S3) objectMatchWithETag(
	ctx context.Context,
	key string,
	expectedSize int64,
	expectedHash string,
) (bool, bool, string, error) {
	result, err := store.client.Get(ctx, store.fullKey(key))
	if errors.Is(err, errS3NotFound) {
		return false, false, "", nil
	}
	if err != nil {
		return false, false, "", err
	}
	if result.Body == nil {
		return false, true, result.ETag, errors.New("S3 response body is nil")
	}
	if result.Size != expectedSize {
		_ = result.Body.Close()
		return false, true, result.ETag, nil
	}
	digest := sha256.New()
	written, copyErr := io.CopyBuffer(
		&contextWriter{ctx: ctx, writer: digest},
		&contextReader{ctx: ctx, reader: io.LimitReader(result.Body, expectedSize+1)},
		make([]byte, 32*1024),
	)
	if copyErr != nil {
		_ = result.Body.Close()
		return false, true, result.ETag, copyErr
	}
	if closeErr := result.Body.Close(); closeErr != nil {
		return false, true, result.ETag, closeErr
	}
	if written != expectedSize {
		return false, true, result.ETag, nil
	}
	return hex.EncodeToString(digest.Sum(nil)) == expectedHash, true, result.ETag, nil
}

func (store *S3) fullKey(key string) string {
	if store.config.Prefix == "" {
		return key
	}
	return path.Join(store.config.Prefix, key)
}

func (store *S3) temporary(pattern string) (*temporaryFile, error) {
	store.lifecycle.RLock()
	defer store.lifecycle.RUnlock()
	file, err := os.CreateTemp(store.tempRoot, pattern)
	if err != nil {
		return nil, fmt.Errorf("create S3 temporary file: %w", err)
	}
	return &temporaryFile{File: file, path: file.Name()}, nil
}

func (store *S3) lockFor(key string) *sync.Mutex {
	digest := sha256.Sum256([]byte(key))
	return &store.locks[int(digest[0])%len(store.locks)]
}

type temporaryFile struct {
	*os.File
	path string
	once sync.Once
	err  error
}

func (file *temporaryFile) Close() error {
	file.once.Do(func() {
		file.err = errors.Join(file.File.Close(), removeTemporary(file.path))
	})
	return file.err
}

func removeTemporary(filename string) error {
	err := os.Remove(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func syncAndRewind(file *os.File) error {
	if err := file.Sync(); err != nil {
		return err
	}
	_, err := file.Seek(0, io.SeekStart)
	return err
}

var _ Driver = (*S3)(nil)
