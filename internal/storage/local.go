// Package storage provides durable storage primitives for immutable voice assets.
package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	// ErrInvalidArgument reports an invalid identifier, part number, or limit.
	ErrInvalidArgument = errors.New("invalid storage argument")
	// ErrInvalidKey reports a storage key that is non-canonical or escapes the root.
	ErrInvalidKey = errors.New("invalid storage key")
	// ErrTooLarge reports content that exceeds its configured byte limit.
	ErrTooLarge = errors.New("storage content exceeds maximum size")
	// ErrChecksumMismatch reports content that does not match its expected SHA-256.
	ErrChecksumMismatch = errors.New("storage checksum mismatch")
	// ErrSizeMismatch reports content that does not match its expected size.
	ErrSizeMismatch = errors.New("storage size mismatch")
	// ErrPartConflict reports a part number already occupied by different content.
	ErrPartConflict = errors.New("storage part conflict")
	// ErrObjectConflict reports an asset already occupied by different content.
	ErrObjectConflict = errors.New("storage object conflict")
	// ErrPartsOutOfOrder reports part references that are not strictly increasing.
	ErrPartsOutOfOrder = errors.New("storage parts are out of order")
)

const (
	// ObjectKindProviderRawResponse is the immutable, unmodified response
	// returned by an ASR provider. PutImmutable rejects every unlisted kind.
	ObjectKindProviderRawResponse = "provider_raw_response"
	// ObjectKindClip is a bounded, immutable audio excerpt derived from an original.
	ObjectKindClip = "clip"
	// ObjectKindExport is an immutable serialized transcript revision.
	ObjectKindExport = "export"
	// ObjectKindWaveform is a bounded, immutable PNG derived from an original.
	ObjectKindWaveform = "waveform"
)

// PutPartOptions defines integrity and resource limits for one upload part.
type PutPartOptions struct {
	// ExpectedSize is enforced when positive. Zero preserves callers that only
	// have a maximum size and checksum.
	ExpectedSize   int64
	ExpectedSHA256 string
	MaxBytes       int64
}

// AssembleOptions defines integrity and resource limits for a complete object.
type AssembleOptions struct {
	ExpectedSize   int64
	ExpectedSHA256 string
	MaxBytes       int64
}

// Part describes an immutable upload part stored under a server-generated key.
type Part struct {
	Number int
	Key    string
	Size   int64
	SHA256 string
	Reused bool
}

// Ref returns the persistent fields needed to assemble this part later.
func (p Part) Ref() PartRef {
	return PartRef{
		Number: p.Number,
		Key:    p.Key,
		Size:   p.Size,
		SHA256: p.SHA256,
	}
}

// PartRef identifies an immutable part and its expected integrity metadata.
type PartRef struct {
	Number int
	Key    string
	Size   int64
	SHA256 string
}

// Object describes an assembled original stored under an asset-derived key.
type Object struct {
	Backend Backend
	Key     string
	Size    int64
	SHA256  string
	Reused  bool
}

// Local stores upload parts and assembled originals below one filesystem root.
// A Local is safe for concurrent use within a process.
type Local struct {
	root      string
	lifecycle sync.RWMutex
	locks     [64]sync.Mutex
}

// Backend identifies local filesystem objects in persistent metadata.
func (*Local) Backend() Backend {
	return BackendLocal
}

// NewLocal creates a local store rooted at root.
func NewLocal(root string) (*Local, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("%w: storage root is empty", ErrInvalidArgument)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve storage root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve storage root links: %w", err)
	}
	return &Local{root: filepath.Clean(resolvedRoot)}, nil
}

// PutPart streams a part into a temporary file and atomically publishes it.
// uploadID is hashed before use and no client filename is accepted by this API.
func (s *Local) PutPart(
	ctx context.Context,
	uploadID string,
	partNumber int,
	src io.Reader,
	opts PutPartOptions,
) (Part, error) {
	if err := contextError(ctx); err != nil {
		return Part{}, err
	}
	if src == nil || partNumber <= 0 {
		return Part{}, fmt.Errorf("%w: source is nil or part number is not positive", ErrInvalidArgument)
	}
	expectedHash, err := normalizeSHA256(opts.ExpectedSHA256)
	if err != nil {
		return Part{}, err
	}
	if err := validateLimit(opts.MaxBytes); err != nil {
		return Part{}, err
	}
	if opts.ExpectedSize < 0 {
		return Part{}, fmt.Errorf("%w: expected part size is negative", ErrInvalidArgument)
	}
	if opts.ExpectedSize > opts.MaxBytes {
		return Part{}, fmt.Errorf("%w: expected part size %d exceeds %d", ErrTooLarge, opts.ExpectedSize, opts.MaxBytes)
	}
	key, err := generatedPartKey(uploadID, partNumber)
	if err != nil {
		return Part{}, err
	}
	target, err := s.pathForKey(key)
	if err != nil {
		return Part{}, err
	}

	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()

	root, err := s.openRoot()
	if err != nil {
		return Part{}, err
	}
	defer root.Close()
	if err := secureMkdirAll(root, path.Dir(key)); err != nil {
		return Part{}, fmt.Errorf("create part directory: %w", err)
	}
	temporary, temporaryKey, err := createTemporary(root, path.Dir(key), ".tmp-part-")
	if err != nil {
		return Part{}, fmt.Errorf("create temporary part: %w", err)
	}
	defer root.Remove(temporaryKey)
	defer temporary.Close()

	size, actualHash, err := copyBounded(ctx, temporary, src, opts.MaxBytes)
	if err != nil {
		return Part{}, fmt.Errorf("stream part: %w", err)
	}
	if opts.ExpectedSize > 0 && size != opts.ExpectedSize {
		return Part{}, fmt.Errorf("%w: part expected %d bytes, got %d", ErrSizeMismatch, opts.ExpectedSize, size)
	}
	if actualHash != expectedHash {
		return Part{}, fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, expectedHash, actualHash)
	}
	if err := contextError(ctx); err != nil {
		return Part{}, err
	}
	if err := syncAndClose(temporary); err != nil {
		return Part{}, fmt.Errorf("flush temporary part: %w", err)
	}

	result := Part{Number: partNumber, Key: key, Size: size, SHA256: actualHash}
	lock := s.lockFor(target)
	lock.Lock()
	defer lock.Unlock()

	reused, err := publishNoReplace(ctx, root, temporaryKey, key, size, actualHash, ErrPartConflict)
	if err != nil {
		if errors.Is(err, ErrPartConflict) {
			return Part{}, fmt.Errorf("%w: part %d already contains different bytes", ErrPartConflict, partNumber)
		}
		return Part{}, fmt.Errorf("atomically publish part: %w", err)
	}
	result.Reused = reused
	return result, nil
}

// Assemble streams ordered parts into an original and atomically publishes it.
// assetID determines the stable final key; uploadID is used only for part keys.
func (s *Local) Assemble(
	ctx context.Context,
	assetID string,
	uploadID string,
	parts []PartRef,
	opts AssembleOptions,
) (Object, error) {
	if err := contextError(ctx); err != nil {
		return Object{}, err
	}
	if len(parts) == 0 || opts.ExpectedSize < 0 {
		return Object{}, fmt.Errorf("%w: parts are empty or expected size is negative", ErrInvalidArgument)
	}
	if err := validateLimit(opts.MaxBytes); err != nil {
		return Object{}, err
	}
	if opts.ExpectedSize > opts.MaxBytes {
		return Object{}, fmt.Errorf("%w: expected size %d exceeds %d", ErrTooLarge, opts.ExpectedSize, opts.MaxBytes)
	}
	expectedHash, err := normalizeSHA256(opts.ExpectedSHA256)
	if err != nil {
		return Object{}, err
	}
	if err := validatePartRefs(uploadID, parts, opts); err != nil {
		return Object{}, err
	}
	key, err := generatedObjectKey(assetID)
	if err != nil {
		return Object{}, err
	}
	target, err := s.pathForKey(key)
	if err != nil {
		return Object{}, err
	}
	result := Object{Backend: BackendLocal, Key: key, Size: opts.ExpectedSize, SHA256: expectedHash}

	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()

	root, err := s.openRoot()
	if err != nil {
		return Object{}, err
	}
	defer root.Close()
	if err := secureMkdirAll(root, path.Dir(key)); err != nil {
		return Object{}, fmt.Errorf("create object directory: %w", err)
	}

	lock := s.lockFor(target)
	lock.Lock()
	match, exists, err := fileMatchesRoot(ctx, root, key, opts.ExpectedSize, expectedHash)
	lock.Unlock()
	if err != nil {
		return Object{}, fmt.Errorf("inspect existing object: %w", err)
	}
	if exists {
		if match {
			result.Reused = true
			return result, nil
		}
		return Object{}, fmt.Errorf("%w: asset already contains different bytes", ErrObjectConflict)
	}

	temporary, temporaryKey, err := createTemporary(root, path.Dir(key), ".tmp-object-")
	if err != nil {
		return Object{}, fmt.Errorf("create temporary object: %w", err)
	}
	defer root.Remove(temporaryKey)
	defer temporary.Close()

	wholeHash := sha256.New()
	var total int64
	for _, part := range parts {
		partFile, openErr := s.Open(ctx, part.Key)
		if openErr != nil {
			return Object{}, fmt.Errorf("open part %d: %w", part.Number, openErr)
		}
		remaining := opts.MaxBytes - total
		size, actualPartHash, copyErr := copyBounded(
			ctx,
			io.MultiWriter(temporary, wholeHash),
			partFile,
			remaining,
		)
		closeErr := partFile.Close()
		if copyErr != nil {
			return Object{}, fmt.Errorf("stream part %d: %w", part.Number, copyErr)
		}
		if closeErr != nil {
			return Object{}, fmt.Errorf("close part %d: %w", part.Number, closeErr)
		}
		if size != part.Size {
			return Object{}, fmt.Errorf("%w: part %d expected %d bytes, got %d", ErrSizeMismatch, part.Number, part.Size, size)
		}
		if actualPartHash != part.SHA256 {
			return Object{}, fmt.Errorf("%w: part %d expected %s, got %s", ErrChecksumMismatch, part.Number, part.SHA256, actualPartHash)
		}
		total += size
	}
	if total != opts.ExpectedSize {
		return Object{}, fmt.Errorf("%w: object expected %d bytes, got %d", ErrSizeMismatch, opts.ExpectedSize, total)
	}
	actualHash := hex.EncodeToString(wholeHash.Sum(nil))
	if actualHash != expectedHash {
		return Object{}, fmt.Errorf("%w: object expected %s, got %s", ErrChecksumMismatch, expectedHash, actualHash)
	}
	if err := contextError(ctx); err != nil {
		return Object{}, err
	}
	if err := syncAndClose(temporary); err != nil {
		return Object{}, fmt.Errorf("flush temporary object: %w", err)
	}

	lock.Lock()
	defer lock.Unlock()
	reused, err := publishNoReplace(ctx, root, temporaryKey, key, opts.ExpectedSize, expectedHash, ErrObjectConflict)
	if err != nil {
		if errors.Is(err, ErrObjectConflict) {
			return Object{}, fmt.Errorf("%w: asset already contains different bytes", ErrObjectConflict)
		}
		return Object{}, fmt.Errorf("atomically publish object: %w", err)
	}
	result.Reused = reused
	return result, nil
}

// PutImmutable streams one derived object into a temporary file, verifies its
// resource limit while calculating SHA-256, and atomically publishes it under
// a key derived only from server identifiers and an approved object kind.
// Replaying the same bytes reuses the published object; different bytes at the
// same key are rejected without replacing the original.
func (s *Local) PutImmutable(
	ctx context.Context,
	assetID string,
	objectID string,
	kind string,
	src io.Reader,
	maxBytes int64,
) (Object, error) {
	if err := contextError(ctx); err != nil {
		return Object{}, err
	}
	if src == nil {
		return Object{}, fmt.Errorf("%w: source is nil", ErrInvalidArgument)
	}
	if err := validateLimit(maxBytes); err != nil {
		return Object{}, err
	}
	key, err := generatedImmutableObjectKey(assetID, objectID, kind)
	if err != nil {
		return Object{}, err
	}
	target, err := s.pathForKey(key)
	if err != nil {
		return Object{}, err
	}

	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()

	root, err := s.openRoot()
	if err != nil {
		return Object{}, err
	}
	defer root.Close()
	if err := secureMkdirAll(root, path.Dir(key)); err != nil {
		return Object{}, fmt.Errorf("create immutable object directory: %w", err)
	}
	temporary, temporaryKey, err := createTemporary(root, path.Dir(key), ".tmp-immutable-")
	if err != nil {
		return Object{}, fmt.Errorf("create temporary immutable object: %w", err)
	}
	defer root.Remove(temporaryKey)
	defer temporary.Close()

	size, actualHash, err := copyBounded(ctx, temporary, src, maxBytes)
	if err != nil {
		return Object{}, fmt.Errorf("stream immutable object: %w", err)
	}
	if err := contextError(ctx); err != nil {
		return Object{}, err
	}
	if err := syncAndClose(temporary); err != nil {
		return Object{}, fmt.Errorf("flush temporary immutable object: %w", err)
	}

	result := Object{Backend: BackendLocal, Key: key, Size: size, SHA256: actualHash}
	lock := s.lockFor(target)
	lock.Lock()
	defer lock.Unlock()

	reused, err := publishNoReplace(ctx, root, temporaryKey, key, size, actualHash, ErrObjectConflict)
	if err != nil {
		if errors.Is(err, ErrObjectConflict) {
			return Object{}, fmt.Errorf("%w: immutable object already contains different bytes", ErrObjectConflict)
		}
		return Object{}, fmt.Errorf("atomically publish immutable object: %w", err)
	}
	result.Reused = reused
	return result, nil
}

// PutSnapshot restores an exact immutable key for the offline backup path.
// The key is never derived from caller-controlled identifiers and existing
// bytes are reused only when their full integrity matches.
func (s *Local) PutSnapshot(
	ctx context.Context,
	key string,
	src io.Reader,
	expectedSize int64,
	expectedSHA256 string,
) (Object, error) {
	if err := contextError(ctx); err != nil {
		return Object{}, err
	}
	if src == nil || expectedSize < 0 {
		return Object{}, fmt.Errorf("%w: invalid snapshot source or size", ErrInvalidArgument)
	}
	expectedHash, err := normalizeSHA256(expectedSHA256)
	if err != nil {
		return Object{}, err
	}
	if expectedSize > maxS3ObjectBytes {
		return Object{}, fmt.Errorf("%w: snapshot exceeds maximum", ErrTooLarge)
	}
	if _, err := s.pathForKey(key); err != nil {
		return Object{}, err
	}

	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	root, err := s.openRoot()
	if err != nil {
		return Object{}, err
	}
	defer root.Close()
	if err := secureMkdirAll(root, path.Dir(key)); err != nil {
		return Object{}, fmt.Errorf("create snapshot directory: %w", err)
	}
	temporary, temporaryKey, err := createTemporary(root, path.Dir(key), ".tmp-snapshot-")
	if err != nil {
		return Object{}, fmt.Errorf("create snapshot temporary: %w", err)
	}
	defer root.Remove(temporaryKey)
	defer temporary.Close()

	size, actualHash, err := copyBounded(ctx, temporary, src, expectedSize)
	if err != nil {
		return Object{}, fmt.Errorf("stream snapshot: %w", err)
	}
	if size != expectedSize || actualHash != expectedHash {
		return Object{}, fmt.Errorf("%w: snapshot integrity does not match manifest", ErrChecksumMismatch)
	}
	if err := syncAndClose(temporary); err != nil {
		return Object{}, fmt.Errorf("flush snapshot: %w", err)
	}
	reused, err := publishNoReplace(ctx, root, temporaryKey, key, size, actualHash, ErrObjectConflict)
	if err != nil {
		return Object{}, fmt.Errorf("publish snapshot: %w", err)
	}
	return Object{Backend: BackendLocal, Key: key, Size: size, SHA256: actualHash, Reused: reused}, nil
}

// Open opens a relative storage key after canonicalization and escape checks.
func (s *Local) Open(ctx context.Context, key string) (File, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if _, err := s.pathForKey(key); err != nil {
		return nil, err
	}
	root, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := ensureNoSymlinks(root, key, true); err != nil {
		return nil, err
	}
	file, err := root.Open(key)
	if err != nil {
		return nil, fmt.Errorf("open storage key: %w", err)
	}
	return file, nil
}

// ListKeys returns up to limit canonical object keys below prefix. It is used
// only to prove that a clean restore target contains no pre-existing objects.
func (s *Local) ListKeys(ctx context.Context, prefix string, limit int) ([]string, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, fmt.Errorf("%w: list limit must be positive", ErrInvalidArgument)
	}
	if prefix != "" {
		if _, err := s.pathForKey(prefix); err != nil {
			return nil, err
		}
	}
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	base := s.root
	if prefix != "" {
		base = filepath.Join(s.root, filepath.FromSlash(prefix))
	}
	keys := make([]string, 0, limit)
	err := filepath.WalkDir(base, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := contextError(ctx); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: storage key is symbolic link", ErrInvalidKey)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%w: storage key is not regular", ErrInvalidKey)
		}
		relative, err := filepath.Rel(s.root, filename)
		if err != nil {
			return err
		}
		if relative != "." {
			keys = append(keys, filepath.ToSlash(filepath.Clean(relative)))
		}
		if len(keys) >= limit {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return nil, err
	}
	sort.Strings(keys)
	return keys, nil
}

// DeleteParts removes every temporary part for one upload-derived directory.
func (s *Local) DeleteParts(ctx context.Context, uploadID string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	digest, err := identifierDigest(uploadID)
	if err != nil {
		return err
	}
	key := path.Join("parts", digest[:2], digest)
	if _, err := s.pathForKey(key); err != nil {
		return err
	}

	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	root, err := s.openRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := ensureNoSymlinks(root, key, true); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := root.RemoveAll(key); err != nil {
		return fmt.Errorf("delete upload parts: %w", err)
	}
	if err := syncParentDirectory(root, key); err != nil {
		return fmt.Errorf("sync upload parts deletion: %w", err)
	}
	return nil
}

// DeleteObject removes an exact immutable object only when its size and
// checksum still match the caller's expected content. It supports both
// compensation and expiry cleanup and is idempotent when the object is absent.
func (s *Local) DeleteObject(ctx context.Context, key string, expectedSize int64, expectedSHA256 string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if expectedSize < 0 {
		return fmt.Errorf("%w: expected size is negative", ErrInvalidArgument)
	}
	expectedHash, err := normalizeSHA256(expectedSHA256)
	if err != nil {
		return err
	}
	target, err := s.pathForKey(key)
	if err != nil {
		return err
	}
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	lock := s.lockFor(target)
	lock.Lock()
	defer lock.Unlock()
	root, err := s.openRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	match, exists, err := fileMatchesRoot(ctx, root, key, expectedSize, expectedHash)
	if err != nil {
		return fmt.Errorf("inspect object before deletion: %w", err)
	}
	if !exists {
		return nil
	}
	if !match {
		return fmt.Errorf("%w: object integrity changed before deletion", ErrObjectConflict)
	}
	if err := root.Remove(key); err != nil {
		return fmt.Errorf("delete uncommitted object: %w", err)
	}
	if err := syncParentDirectory(root, key); err != nil {
		return fmt.Errorf("sync object deletion: %w", err)
	}
	return nil
}

func validatePartRefs(uploadID string, parts []PartRef, opts AssembleOptions) error {
	var declaredSize int64
	previous := 0
	for index, part := range parts {
		if part.Number <= previous {
			return fmt.Errorf("%w: part %d follows %d at index %d", ErrPartsOutOfOrder, part.Number, previous, index)
		}
		previous = part.Number
		expectedKey, err := generatedPartKey(uploadID, part.Number)
		if err != nil {
			return err
		}
		if part.Key != expectedKey {
			return fmt.Errorf("%w: part %d key does not belong to upload", ErrInvalidKey, part.Number)
		}
		if part.Size < 0 || declaredSize > math.MaxInt64-part.Size {
			return fmt.Errorf("%w: invalid or overflowing part size", ErrInvalidArgument)
		}
		normalized, err := normalizeSHA256(part.SHA256)
		if err != nil {
			return fmt.Errorf("part %d: %w", part.Number, err)
		}
		if normalized != part.SHA256 {
			return fmt.Errorf("%w: part %d checksum is not canonical lowercase hex", ErrInvalidArgument, part.Number)
		}
		declaredSize += part.Size
		if declaredSize > opts.MaxBytes {
			return fmt.Errorf("%w: declared parts exceed %d bytes", ErrTooLarge, opts.MaxBytes)
		}
	}
	if declaredSize != opts.ExpectedSize {
		return fmt.Errorf("%w: parts declare %d bytes, object expects %d", ErrSizeMismatch, declaredSize, opts.ExpectedSize)
	}
	return nil
}

func generatedPartKey(uploadID string, partNumber int) (string, error) {
	if partNumber <= 0 {
		return "", fmt.Errorf("%w: part number must be positive", ErrInvalidArgument)
	}
	digest, err := identifierDigest(uploadID)
	if err != nil {
		return "", err
	}
	return path.Join("parts", digest[:2], digest, strconv.Itoa(partNumber)+".part"), nil
}

func generatedObjectKey(assetID string) (string, error) {
	digest, err := identifierDigest(assetID)
	if err != nil {
		return "", err
	}
	return path.Join("objects", digest[:2], digest, "original"), nil
}

func generatedImmutableObjectKey(assetID, objectID, kind string) (string, error) {
	if err := validateImmutableObjectKind(kind); err != nil {
		return "", err
	}
	assetDigest, err := identifierDigest(assetID)
	if err != nil {
		return "", fmt.Errorf("asset identifier: %w", err)
	}
	objectDigest, err := identifierDigest(objectID)
	if err != nil {
		return "", fmt.Errorf("object identifier: %w", err)
	}
	return path.Join(
		"objects",
		assetDigest[:2],
		assetDigest,
		"derived",
		kind,
		objectDigest,
	), nil
}

func validateImmutableObjectKind(kind string) error {
	switch kind {
	case ObjectKindProviderRawResponse, ObjectKindClip, ObjectKindExport, ObjectKindWaveform:
		return nil
	default:
		return fmt.Errorf("%w: immutable object kind %q is not approved", ErrInvalidArgument, kind)
	}
}

func identifierDigest(identifier string) (string, error) {
	if strings.TrimSpace(identifier) == "" {
		return "", fmt.Errorf("%w: identifier is empty", ErrInvalidArgument)
	}
	digest := sha256.Sum256([]byte(identifier))
	return hex.EncodeToString(digest[:]), nil
}

func normalizeSHA256(value string) (string, error) {
	if len(value) != sha256.Size*2 {
		return "", fmt.Errorf("%w: SHA-256 must contain 64 hexadecimal characters", ErrInvalidArgument)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("%w: malformed SHA-256", ErrInvalidArgument)
	}
	return strings.ToLower(value), nil
}

func validateLimit(limit int64) error {
	if limit <= 0 || limit == math.MaxInt64 {
		return fmt.Errorf("%w: maximum byte limit must be between 1 and %d", ErrInvalidArgument, int64(math.MaxInt64-1))
	}
	return nil
}

func (s *Local) openRoot() (*os.Root, error) {
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, fmt.Errorf("open storage root: %w", err)
	}
	return root, nil
}

func secureMkdirAll(root *os.Root, directory string) error {
	if directory == "." {
		return nil
	}
	if err := validateKey(directory); err != nil {
		return err
	}
	current := ""
	for _, component := range strings.Split(directory, "/") {
		current = path.Join(current, component)
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := root.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = root.Lstat(current)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%w: storage directory %q is not a real directory", ErrInvalidKey, current)
		}
	}
	return nil
}

func ensureNoSymlinks(root *os.Root, key string, includeFinal bool) error {
	if key == "." {
		return nil
	}
	if err := validateKey(key); err != nil {
		return err
	}
	components := strings.Split(key, "/")
	if !includeFinal {
		components = components[:len(components)-1]
	}
	current := ""
	for index, component := range components {
		current = path.Join(current, component)
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: storage path %q contains a symbolic link", ErrInvalidKey, current)
		}
		if index < len(components)-1 && !info.IsDir() {
			return fmt.Errorf("%w: storage path %q has a non-directory parent", ErrInvalidKey, current)
		}
	}
	return nil
}

func createTemporary(root *os.Root, directory, prefix string) (*os.File, string, error) {
	for range 100 {
		var randomValue [16]byte
		if _, err := rand.Read(randomValue[:]); err != nil {
			return nil, "", err
		}
		name := prefix + hex.EncodeToString(randomValue[:])
		key := path.Join(directory, name)
		file, err := root.OpenFile(key, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return file, key, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("create collision-free temporary file: %w", os.ErrExist)
}

func publishNoReplace(
	ctx context.Context,
	root *os.Root,
	temporaryKey,
	targetKey string,
	expectedSize int64,
	expectedHash string,
	conflict error,
) (bool, error) {
	if err := contextError(ctx); err != nil {
		return false, err
	}
	if err := root.Link(temporaryKey, targetKey); err == nil {
		if err := root.Remove(temporaryKey); err != nil {
			return false, fmt.Errorf("remove published temporary link: %w", err)
		}
		if err := syncParentDirectory(root, targetKey); err != nil {
			return false, err
		}
		return false, nil
	} else if !errors.Is(err, os.ErrExist) {
		return false, err
	}

	match, exists, err := fileMatchesRoot(ctx, root, targetKey, expectedSize, expectedHash)
	if err != nil {
		return false, err
	}
	if !exists || !match {
		return false, conflict
	}
	if err := syncParentDirectory(root, targetKey); err != nil {
		return false, err
	}
	return true, nil
}

func syncParentDirectory(root *os.Root, key string) error {
	directory := path.Dir(key)
	for {
		if err := ensureNoSymlinks(root, directory, true); err == nil {
			file, err := root.Open(directory)
			if err != nil {
				return err
			}
			syncErr := syncDirectoryFile(file)
			closeErr := file.Close()
			if syncErr != nil {
				return syncErr
			}
			return closeErr
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if directory == "." {
			return nil
		}
		directory = path.Dir(directory)
	}
}

func (s *Local) pathForKey(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	candidate := filepath.Join(s.root, filepath.FromSlash(key))
	if !pathWithinRoot(s.root, candidate) {
		return "", fmt.Errorf("%w: key escapes storage root", ErrInvalidKey)
	}
	return candidate, nil
}

func validateKey(key string) error {
	if key == "" || strings.ContainsRune(key, '\x00') || strings.Contains(key, `\`) || strings.Contains(key, ":") {
		return fmt.Errorf("%w: key is empty or contains a platform-specific separator", ErrInvalidKey)
	}
	if path.IsAbs(key) || path.Clean(key) != key || key == "." || key == ".." || strings.HasPrefix(key, "../") {
		return fmt.Errorf("%w: key is not a canonical relative path", ErrInvalidKey)
	}
	return nil
}

func pathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func (s *Local) lockFor(path string) *sync.Mutex {
	digest := sha256.Sum256([]byte(path))
	return &s.locks[int(digest[0])%len(s.locks)]
}

func copyBounded(ctx context.Context, destination io.Writer, source io.Reader, maxBytes int64) (int64, string, error) {
	digest := sha256.New()
	limited := &io.LimitedReader{R: &contextReader{ctx: ctx, reader: source}, N: maxBytes + 1}
	written, err := io.CopyBuffer(
		&contextWriter{ctx: ctx, writer: io.MultiWriter(destination, digest)},
		limited,
		make([]byte, 32*1024),
	)
	if err != nil {
		return written, hashString(digest), err
	}
	if written > maxBytes {
		return written, hashString(digest), ErrTooLarge
	}
	return written, hashString(digest), nil
}

func fileMatchesRoot(
	ctx context.Context,
	root *os.Root,
	key string,
	expectedSize int64,
	expectedHash string,
) (bool, bool, error) {
	if err := ensureNoSymlinks(root, key, true); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, false, nil
		}
		return false, false, err
	}
	info, err := root.Lstat(key)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if !info.Mode().IsRegular() || info.Size() != expectedSize {
		return false, true, nil
	}
	file, err := root.Open(key)
	if err != nil {
		return false, true, err
	}
	digest := sha256.New()
	_, copyErr := io.CopyBuffer(&contextWriter{ctx: ctx, writer: digest}, &contextReader{ctx: ctx, reader: file}, make([]byte, 32*1024))
	closeErr := file.Close()
	if copyErr != nil {
		return false, true, copyErr
	}
	if closeErr != nil {
		return false, true, closeErr
	}
	return hashString(digest) == expectedHash, true, nil
}

func syncAndClose(file *os.File) error {
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func hashString(digest hash.Hash) string {
	return hex.EncodeToString(digest.Sum(nil))
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrInvalidArgument)
	}
	return ctx.Err()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := contextError(r.ctx); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w *contextWriter) Write(buffer []byte) (int, error) {
	if err := contextError(w.ctx); err != nil {
		return 0, err
	}
	return w.writer.Write(buffer)
}
