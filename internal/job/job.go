// Package job owns durable transcription job scheduling and worker leases.
package job

import (
	"encoding/json"
	"errors"
	"time"
)

const (
	KindMockTranscribe   = "mock_transcribe"
	KindLLMCorrect       = "llm_correct"
	KindGenerateWaveform = "generate_waveform"
	KindPurgeAsset       = "purge_asset"

	StateQueued    = "queued"
	StateRunning   = "running"
	StateRetryWait = "retry_wait"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

const DefaultMaxAttempts = 3

const (
	ErrorCodeInternal            = "internal_error"
	ErrorCodeProviderUnavailable = "provider_unavailable"
	ErrorCodeInvalidAudio        = "invalid_audio"
	ErrorCodeProviderRejected    = "provider_rejected"
	ErrorCodeWorkerTimeout       = "worker_timeout"
	ErrorCodeLeaseExpired        = "lease_expired"
)

var (
	ErrForbidden              = errors.New("forbidden")
	ErrInvalidInput           = errors.New("invalid transcription request")
	ErrNotFound               = errors.New("job not found")
	ErrAssetNotReady          = errors.New("asset is not ready for transcription")
	ErrRevisionNotCorrectable = errors.New("transcript revision is not correctable")
	ErrCorrectionActive       = errors.New("a correction job is already active for this asset")
	ErrIdempotencyConflict    = errors.New("idempotency key was used for a different request")
	ErrNoClaimableJob         = errors.New("no claimable job")
	ErrLeaseConflict          = errors.New("job lease is not owned by worker")
	ErrInvalidErrorCode       = errors.New("invalid safe error code")
)

// Job is one durable unit of background work.
type Job struct {
	ID               string          `json:"id"`
	WorkspaceID      string          `json:"workspace_id"`
	AssetID          string          `json:"asset_id"`
	CreatedBy        string          `json:"created_by"`
	Kind             string          `json:"kind"`
	State            string          `json:"state"`
	Payload          json.RawMessage `json:"payload"`
	Attempts         int             `json:"attempts"`
	MaxAttempts      int             `json:"max_attempts"`
	AvailableAt      time.Time       `json:"available_at"`
	LeaseOwner       *string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt   *time.Time      `json:"lease_expires_at,omitempty"`
	LastErrorCode    *string         `json:"last_error_code,omitempty"`
	ResultRevisionID *string         `json:"result_revision_id,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// CreateTranscriptionParams is the validated persistence command produced by
// Service. IDs are generated before entering the repository transaction.
type CreateTranscriptionParams struct {
	JobID          string
	AuditID        string
	WorkspaceID    string
	AssetID        string
	CreatedBy      string
	Kind           string
	Payload        json.RawMessage
	MaxAttempts    int
	IdempotencyKey string
	RequestHash    string
}

type CreateCorrectionParams struct {
	JobID, AuditID, WorkspaceID, SourceRevisionID, CreatedBy string
	Kind, IdempotencyKey, RequestHash                        string
	Payload                                                  json.RawMessage
	MaxAttempts                                              int
}

type ClaimParams struct {
	Kind          string
	WorkerID      string
	Now           time.Time
	LeaseDuration time.Duration
}

type SucceedParams struct {
	JobID            string
	WorkerID         string
	ResultRevisionID *string
	Now              time.Time
}

type FailParams struct {
	JobID     string
	WorkerID  string
	ErrorCode string
	Now       time.Time
	RetryAt   time.Time
}

var safeErrorCodes = map[string]struct{}{
	ErrorCodeInternal:            {},
	ErrorCodeProviderUnavailable: {},
	ErrorCodeInvalidAudio:        {},
	ErrorCodeProviderRejected:    {},
	ErrorCodeWorkerTimeout:       {},
	ErrorCodeLeaseExpired:        {},
}

func IsSafeErrorCode(code string) bool {
	_, ok := safeErrorCodes[code]
	return ok
}
