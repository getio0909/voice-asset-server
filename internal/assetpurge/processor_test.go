package assetpurge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

const (
	testJobID       = "10000000-0000-4000-8000-000000000001"
	testWorkspaceID = "20000000-0000-4000-8000-000000000001"
	testAssetID     = "30000000-0000-4000-8000-000000000001"
	testObjectID    = "40000000-0000-4000-8000-000000000001"
	testUploadID    = "50000000-0000-4000-8000-000000000001"
)

func TestProcessorDeletesStorageBeforeFinalizingMetadata(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	claimed := testClaimedJob(now)
	object := Object{
		ID: testObjectID, Backend: storage.BackendLocal, Key: "objects/aa/original",
		Size: 7, SHA256: "239f59ed55e737c77147cf55ad0c1b030b6d7ee748a7426952f9b852d5a935e5",
	}
	inventory := Inventory{
		JobID: testJobID, WorkspaceID: testWorkspaceID, AssetID: testAssetID,
		Objects: []Object{object}, UploadIDs: []string{testUploadID},
	}
	inventory.Fingerprint = inventoryFingerprint(inventory.Objects, inventory.UploadIDs)
	jobs := &fakeJobs{claimed: claimed}
	repository := &fakeRepository{inventory: inventory}
	store := &fakeStore{backend: storage.BackendLocal}
	processor := NewProcessor(jobs, repository, store, "worker-1")
	processor.now = func() time.Time { return now }
	processor.random = &repeatingReader{value: 0x11}

	processed, err := processor.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce() = (%v, %v)", processed, err)
	}
	if len(store.deletedUploads) != 1 || store.deletedUploads[0] != testUploadID ||
		len(store.deletedObjects) != 1 || store.deletedObjects[0].Backend != object.Backend ||
		store.deletedObjects[0].Key != object.Key || store.deletedObjects[0].Size != object.Size ||
		store.deletedObjects[0].SHA256 != object.SHA256 {
		t.Fatalf("deleted uploads/objects = %v/%+v", store.deletedUploads, store.deletedObjects)
	}
	if repository.finalizeCalls != 1 || repository.finalized.Fingerprint != inventory.Fingerprint ||
		repository.workerID != "worker-1" || repository.auditID == "" {
		t.Fatalf("finalization = %+v / worker %q / audit %q", repository.finalized, repository.workerID, repository.auditID)
	}
	if jobs.failCalls != 0 {
		t.Fatalf("failure calls = %d", jobs.failCalls)
	}
}

func TestProcessorRecordsSafeRetryWhenStorageOrInventoryFails(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		inventory Inventory
		storeErr  error
	}{
		{
			name: "backend mismatch",
			inventory: Inventory{
				JobID: testJobID, WorkspaceID: testWorkspaceID, AssetID: testAssetID,
				Objects: []Object{{
					ID: testObjectID, Backend: storage.BackendS3, Key: "key", Size: 0,
					SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				}},
			},
		},
		{
			name: "storage failure",
			inventory: Inventory{
				JobID: testJobID, WorkspaceID: testWorkspaceID, AssetID: testAssetID,
				UploadIDs: []string{testUploadID},
			},
			storeErr: errors.New("private storage detail"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			inventory := test.inventory
			inventory.Fingerprint = inventoryFingerprint(inventory.Objects, inventory.UploadIDs)
			jobs := &fakeJobs{claimed: testClaimedJob(now)}
			repository := &fakeRepository{inventory: inventory}
			store := &fakeStore{backend: storage.BackendLocal, err: test.storeErr}
			processor := NewProcessor(jobs, repository, store, "worker-1")
			processor.now = func() time.Time { return now }
			processed, err := processor.RunOnce(context.Background())
			if !processed || !errors.Is(err, ErrProcessingFailed) {
				t.Fatalf("RunOnce() = (%v, %v)", processed, err)
			}
			if jobs.failCalls != 1 || jobs.failed.ErrorCode != job.ErrorCodeInternal ||
				!jobs.failed.RetryAt.Equal(now.Add(DefaultRetryDelay)) {
				t.Fatalf("failure params = %+v", jobs.failed)
			}
			if repository.finalizeCalls != 0 {
				t.Fatalf("finalize calls = %d", repository.finalizeCalls)
			}
		})
	}
}

func TestProcessorIsIdleWhenNoPurgeIsClaimable(t *testing.T) {
	processor := NewProcessor(
		&fakeJobs{claimErr: job.ErrNoClaimableJob},
		&fakeRepository{}, &fakeStore{backend: storage.BackendLocal}, "worker-1",
	)
	processed, err := processor.RunOnce(context.Background())
	if err != nil || processed {
		t.Fatalf("RunOnce() = (%v, %v)", processed, err)
	}
}

func testClaimedJob(now time.Time) job.Job {
	leaseOwner := "worker-1"
	leaseExpiry := now.Add(DefaultLeaseDuration)
	return job.Job{
		ID: testJobID, WorkspaceID: testWorkspaceID, AssetID: testAssetID,
		CreatedBy: "60000000-0000-4000-8000-000000000001",
		Kind:      job.KindPurgeAsset, State: job.StateRunning,
		LeaseOwner: &leaseOwner, LeaseExpiresAt: &leaseExpiry,
	}
}

type fakeJobs struct {
	claimed    job.Job
	claimErr   error
	failed     job.FailParams
	failCalls  int
	failureErr error
}

func (jobs *fakeJobs) Claim(context.Context, job.ClaimParams) (job.Job, error) {
	return jobs.claimed, jobs.claimErr
}

func (jobs *fakeJobs) Fail(_ context.Context, params job.FailParams) (job.Job, error) {
	jobs.failCalls++
	jobs.failed = params
	return job.Job{}, jobs.failureErr
}

type fakeRepository struct {
	inventory     Inventory
	loadErr       error
	finalizeErr   error
	finalized     Inventory
	workerID      string
	auditID       string
	finalizeCalls int
}

func (repository *fakeRepository) Load(context.Context, string, string, time.Time) (Inventory, error) {
	return repository.inventory, repository.loadErr
}

func (repository *fakeRepository) Finalize(
	_ context.Context,
	inventory Inventory,
	workerID,
	auditID string,
	_ time.Time,
) error {
	repository.finalizeCalls++
	repository.finalized = inventory
	repository.workerID = workerID
	repository.auditID = auditID
	return repository.finalizeErr
}

type fakeStore struct {
	backend        storage.Backend
	err            error
	deletedUploads []string
	deletedObjects []Object
}

func (store *fakeStore) Backend() storage.Backend { return store.backend }

func (store *fakeStore) DeleteParts(_ context.Context, uploadID string) error {
	store.deletedUploads = append(store.deletedUploads, uploadID)
	return store.err
}

func (store *fakeStore) DeleteObject(_ context.Context, key string, size int64, digest string) error {
	store.deletedObjects = append(store.deletedObjects, Object{
		Backend: store.backend, Key: key, Size: size, SHA256: digest,
	})
	return store.err
}

type repeatingReader struct{ value byte }

func (reader *repeatingReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = reader.value
	}
	return len(buffer), nil
}
