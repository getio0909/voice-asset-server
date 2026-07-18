// Package assetpurge permanently removes a deliberately trashed asset through
// a retryable storage-first background job while retaining immutable audits.
package assetpurge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

const (
	DefaultLeaseDuration = 10 * time.Minute
	DefaultRetryDelay    = 30 * time.Second
	PurgeTimeout         = 8 * time.Minute
	FailureRecordTimeout = 10 * time.Second
	MaxObjects           = 10_000
	MaxUploads           = 10_000
)

var (
	ErrProcessingFailed = errors.New("asset purge failed")
	ErrInventoryChanged = errors.New("asset purge inventory changed")
)

type Object struct {
	ID      string
	Backend storage.Backend
	Key     string
	Size    int64
	SHA256  string
}

type Inventory struct {
	JobID, WorkspaceID, AssetID string
	Objects                     []Object
	UploadIDs                   []string
	Fingerprint                 string
}

type JobRepository interface {
	Claim(context.Context, job.ClaimParams) (job.Job, error)
	Fail(context.Context, job.FailParams) (job.Job, error)
}

type Repository interface {
	Load(context.Context, string, string, time.Time) (Inventory, error)
	Finalize(context.Context, Inventory, string, string, time.Time) error
}

type Store interface {
	Backend() storage.Backend
	DeleteParts(context.Context, string) error
	DeleteObject(context.Context, string, int64, string) error
}

type Processor struct {
	jobs       JobRepository
	repository Repository
	store      Store
	workerID   string
	random     io.Reader
	now        func() time.Time
}

func NewProcessor(jobs JobRepository, repository Repository, store Store, workerID string) *Processor {
	return &Processor{
		jobs: jobs, repository: repository, store: store, workerID: workerID,
		random: rand.Reader, now: time.Now,
	}
}

func (processor *Processor) RunOnce(ctx context.Context) (bool, error) {
	if processor == nil || processor.jobs == nil || processor.repository == nil ||
		processor.store == nil || strings.TrimSpace(processor.workerID) == "" ||
		processor.random == nil || processor.now == nil {
		return false, errors.New("asset purge processor is not configured")
	}
	now := processor.now().UTC()
	claimed, err := processor.jobs.Claim(ctx, job.ClaimParams{
		Kind: job.KindPurgeAsset, WorkerID: processor.workerID,
		Now: now, LeaseDuration: DefaultLeaseDuration,
	})
	if errors.Is(err, job.ErrNoClaimableJob) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: claim job", ErrProcessingFailed)
	}

	inventory, err := processor.repository.Load(ctx, claimed.ID, processor.workerID, now)
	if err != nil {
		return true, processor.fail(ctx, claimed, "load inventory")
	}
	if err := validateInventory(inventory, claimed, processor.store.Backend()); err != nil {
		return true, processor.fail(ctx, claimed, "validate inventory")
	}
	purgeContext, cancel := context.WithTimeout(ctx, PurgeTimeout)
	defer cancel()
	for _, uploadID := range inventory.UploadIDs {
		if err := processor.store.DeleteParts(purgeContext, uploadID); err != nil {
			return true, processor.fail(ctx, claimed, "delete upload parts")
		}
	}
	for _, object := range inventory.Objects {
		if err := processor.store.DeleteObject(
			purgeContext, object.Key, object.Size, object.SHA256,
		); err != nil {
			return true, processor.fail(ctx, claimed, "delete immutable object")
		}
	}
	auditID, err := identifier.NewUUIDFrom(processor.random)
	if err != nil {
		return true, processor.fail(ctx, claimed, "generate audit identifier")
	}
	if err := processor.repository.Finalize(
		ctx, inventory, processor.workerID, auditID, processor.now().UTC(),
	); err != nil {
		return true, processor.fail(ctx, claimed, "finalize metadata")
	}
	return true, nil
}

func (processor *Processor) fail(ctx context.Context, claimed job.Job, stage string) error {
	now := processor.now().UTC()
	failureContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), FailureRecordTimeout)
	defer cancel()
	_, err := processor.jobs.Fail(failureContext, job.FailParams{
		JobID: claimed.ID, WorkerID: processor.workerID,
		ErrorCode: job.ErrorCodeInternal, Now: now, RetryAt: now.Add(DefaultRetryDelay),
	})
	if err != nil {
		return fmt.Errorf("%w: %s; could not record safe failure", ErrProcessingFailed, stage)
	}
	return fmt.Errorf("%w: %s", ErrProcessingFailed, stage)
}

func validateInventory(inventory Inventory, claimed job.Job, backend storage.Backend) error {
	if !identifier.IsUUID(inventory.JobID) || !identifier.IsUUID(inventory.WorkspaceID) ||
		!identifier.IsUUID(inventory.AssetID) || inventory.JobID != claimed.ID ||
		inventory.WorkspaceID != claimed.WorkspaceID || inventory.AssetID != claimed.AssetID ||
		claimed.Kind != job.KindPurgeAsset || len(inventory.Objects) > MaxObjects ||
		len(inventory.UploadIDs) > MaxUploads || !backend.Valid() {
		return errors.New("asset purge identifiers or limits are invalid")
	}
	for _, object := range inventory.Objects {
		if !identifier.IsUUID(object.ID) || object.Backend != backend ||
			strings.TrimSpace(object.Key) == "" || object.Size < 0 || !validSHA256(object.SHA256) {
			return errors.New("asset purge object metadata is invalid")
		}
	}
	for _, uploadID := range inventory.UploadIDs {
		if !identifier.IsUUID(uploadID) {
			return errors.New("asset purge upload identifier is invalid")
		}
	}
	if inventory.Fingerprint != inventoryFingerprint(inventory.Objects, inventory.UploadIDs) {
		return errors.New("asset purge inventory fingerprint is invalid")
	}
	return nil
}

func inventoryFingerprint(objects []Object, uploadIDs []string) string {
	objects = append([]Object(nil), objects...)
	uploadIDs = append([]string(nil), uploadIDs...)
	sort.Slice(objects, func(left, right int) bool { return objects[left].ID < objects[right].ID })
	sort.Strings(uploadIDs)
	digest := sha256.New()
	for _, object := range objects {
		_, _ = io.WriteString(digest, strings.Join([]string{
			"object", object.ID, string(object.Backend), object.Key,
			strconv.FormatInt(object.Size, 10), object.SHA256,
		}, "\x00")+"\n")
	}
	for _, uploadID := range uploadIDs {
		_, _ = io.WriteString(digest, "upload\x00"+uploadID+"\n")
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(value) == value
}
