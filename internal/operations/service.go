// Package operations exposes bounded, workspace-scoped administration read models.
package operations

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const (
	defaultListLimit = 50
	maxListLimit     = 100
	maxCursorLength  = 1024
)

var (
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidInput = errors.New("invalid operations input")
	ErrNotFound     = errors.New("operations resource not found")
	ErrNotRetryable = errors.New("job is not retryable")
	ErrRetryLimit   = errors.New("job retry limit reached")
	filterPattern   = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,99}$`)
)

var jobStates = map[string]struct{}{
	"queued": {}, "running": {}, "retry_wait": {}, "succeeded": {},
	"failed": {}, "cancelled": {},
}

var actorTypes = map[string]struct{}{"user": {}, "agent": {}, "system": {}}

type JobSummary struct {
	ID               string     `json:"id"`
	AssetID          string     `json:"asset_id,omitempty"`
	CreatedBy        string     `json:"created_by"`
	Kind             string     `json:"kind"`
	State            string     `json:"state"`
	Attempts         int        `json:"attempts"`
	MaxAttempts      int        `json:"max_attempts"`
	AvailableAt      time.Time  `json:"available_at"`
	LeaseExpiresAt   *time.Time `json:"lease_expires_at,omitempty"`
	LastErrorCode    *string    `json:"last_error_code,omitempty"`
	ResultRevisionID *string    `json:"result_revision_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Retryable        bool       `json:"retryable"`
}

type AuditEntry struct {
	ID         string          `json:"id"`
	ActorID    string          `json:"actor_id,omitempty"`
	ActorEmail string          `json:"actor_email,omitempty"`
	ActorType  string          `json:"actor_type"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id,omitempty"`
	RequestID  string          `json:"request_id,omitempty"`
	Metadata   json.RawMessage `json:"metadata"`
	OccurredAt time.Time       `json:"occurred_at"`
}

type JobListInput struct {
	Limit  int
	Cursor string
	State  string
	Kind   string
}

type AuditListInput struct {
	Limit      int
	Cursor     string
	ActorType  string
	Action     string
	TargetType string
}

type JobList struct {
	Items      []JobSummary `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

type AuditList struct {
	Items      []AuditEntry `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

type AssetStatus struct {
	Total           int64 `json:"total"`
	Active          int64 `json:"active"`
	Trashed         int64 `json:"trashed"`
	Purging         int64 `json:"purging"`
	Failed          int64 `json:"failed"`
	AudioDurationMS int64 `json:"audio_duration_ms"`
}

type StorageStatus struct {
	ObjectCount int64 `json:"object_count"`
	Bytes       int64 `json:"bytes"`
}

type TranscriptStatus struct {
	TranscriptCount int64 `json:"transcript_count"`
	RevisionCount   int64 `json:"revision_count"`
}

type JobStatus struct {
	Total     int64 `json:"total"`
	Queued    int64 `json:"queued"`
	Running   int64 `json:"running"`
	RetryWait int64 `json:"retry_wait"`
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
	Cancelled int64 `json:"cancelled"`
}

type ProviderStatus struct {
	EnabledASR int64 `json:"enabled_asr"`
	EnabledLLM int64 `json:"enabled_llm"`
}

type SystemStatus struct {
	GeneratedAt time.Time        `json:"generated_at"`
	ActiveUsers int64            `json:"active_users"`
	Assets      AssetStatus      `json:"assets"`
	Storage     StorageStatus    `json:"storage"`
	Transcripts TranscriptStatus `json:"transcripts"`
	Jobs        JobStatus        `json:"jobs"`
	Providers   ProviderStatus   `json:"providers"`
}

type JobListParams struct {
	WorkspaceID     string
	Limit           int
	State           string
	Kind            string
	BeforeUpdatedAt *time.Time
	BeforeID        string
}

type AuditListParams struct {
	WorkspaceID      string
	Limit            int
	ActorType        string
	Action           string
	TargetType       string
	BeforeOccurredAt *time.Time
	BeforeID         string
}

type RetryJobParams struct {
	JobID, AuditID, WorkspaceID, ActorID, RequestID string
	AvailableAt                                     time.Time
}

type Repository interface {
	ListJobs(context.Context, JobListParams) ([]JobSummary, error)
	RetryJob(context.Context, RetryJobParams) (JobSummary, error)
	ListAuditLogs(context.Context, AuditListParams) ([]AuditEntry, error)
	GetSystemStatus(context.Context, string) (SystemStatus, error)
}

type Service struct {
	repository Repository
	random     io.Reader
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, random: rand.Reader, now: time.Now}
}

func (service *Service) ListJobs(
	ctx context.Context,
	principal auth.Principal,
	input JobListInput,
) (JobList, error) {
	workspaceID, err := authorize(principal)
	if err != nil {
		return JobList{}, err
	}
	state, ok := normalizeOptionalFilter(input.State, jobStates)
	if !ok {
		return JobList{}, ErrInvalidInput
	}
	kind, ok := normalizePatternFilter(input.Kind)
	if !ok {
		return JobList{}, ErrInvalidInput
	}
	binding := strings.Join([]string{workspaceID, state, kind}, "\x00")
	limit, beforeAt, beforeID, err := normalizeListInput(input.Limit, input.Cursor, "jobs", binding)
	if err != nil {
		return JobList{}, err
	}
	items, err := service.repository.ListJobs(ctx, JobListParams{
		WorkspaceID: workspaceID, Limit: limit + 1, State: state, Kind: kind,
		BeforeUpdatedAt: beforeAt, BeforeID: beforeID,
	})
	if err != nil {
		return JobList{}, fmt.Errorf("list operations jobs: %w", err)
	}
	result := JobList{Items: items}
	if len(result.Items) > limit {
		result.Items = append([]JobSummary(nil), result.Items[:limit]...)
		last := result.Items[len(result.Items)-1]
		cursor, encodeErr := encodeCursor("jobs", binding, last.UpdatedAt, last.ID)
		if encodeErr != nil {
			return JobList{}, fmt.Errorf("encode operations job cursor: %w", encodeErr)
		}
		result.NextCursor = &cursor
	}
	if result.Items == nil {
		result.Items = make([]JobSummary, 0)
	}
	return result, nil
}

func (service *Service) ListAuditLogs(
	ctx context.Context,
	principal auth.Principal,
	input AuditListInput,
) (AuditList, error) {
	workspaceID, err := authorize(principal)
	if err != nil {
		return AuditList{}, err
	}
	actorType, ok := normalizeOptionalFilter(input.ActorType, actorTypes)
	if !ok {
		return AuditList{}, ErrInvalidInput
	}
	action, ok := normalizePatternFilter(input.Action)
	if !ok {
		return AuditList{}, ErrInvalidInput
	}
	targetType, ok := normalizePatternFilter(input.TargetType)
	if !ok {
		return AuditList{}, ErrInvalidInput
	}
	binding := strings.Join([]string{workspaceID, actorType, action, targetType}, "\x00")
	limit, beforeAt, beforeID, err := normalizeListInput(input.Limit, input.Cursor, "audit_logs", binding)
	if err != nil {
		return AuditList{}, err
	}
	items, err := service.repository.ListAuditLogs(ctx, AuditListParams{
		WorkspaceID: workspaceID, Limit: limit + 1, ActorType: actorType,
		Action: action, TargetType: targetType, BeforeOccurredAt: beforeAt, BeforeID: beforeID,
	})
	if err != nil {
		return AuditList{}, fmt.Errorf("list operations audit logs: %w", err)
	}
	for index := range items {
		if len(items[index].Metadata) == 0 {
			items[index].Metadata = json.RawMessage(`{}`)
		}
	}
	result := AuditList{Items: items}
	if len(result.Items) > limit {
		result.Items = append([]AuditEntry(nil), result.Items[:limit]...)
		last := result.Items[len(result.Items)-1]
		cursor, encodeErr := encodeCursor("audit_logs", binding, last.OccurredAt, last.ID)
		if encodeErr != nil {
			return AuditList{}, fmt.Errorf("encode operations audit cursor: %w", encodeErr)
		}
		result.NextCursor = &cursor
	}
	if result.Items == nil {
		result.Items = make([]AuditEntry, 0)
	}
	return result, nil
}

func (service *Service) RetryJob(
	ctx context.Context,
	principal auth.Principal,
	jobID,
	requestID string,
) (JobSummary, error) {
	workspaceID, actorID, err := authorizeWrite(principal)
	if err != nil {
		return JobSummary{}, err
	}
	jobID, validID := identifier.NormalizeUUID(jobID)
	if !validID {
		return JobSummary{}, ErrNotFound
	}
	if requestID == "" || len(requestID) > 200 || service.random == nil || service.now == nil {
		return JobSummary{}, ErrInvalidInput
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return JobSummary{}, fmt.Errorf("generate job retry audit identifier: %w", err)
	}
	retried, err := service.repository.RetryJob(ctx, RetryJobParams{
		JobID: jobID, AuditID: auditID, WorkspaceID: workspaceID,
		ActorID: actorID, RequestID: requestID, AvailableAt: service.now().UTC(),
	})
	if err != nil {
		for _, sentinel := range []error{ErrNotFound, ErrNotRetryable, ErrRetryLimit} {
			if errors.Is(err, sentinel) {
				return JobSummary{}, sentinel
			}
		}
		return JobSummary{}, fmt.Errorf("retry operations job: %w", err)
	}
	return retried, nil
}

func (service *Service) GetSystemStatus(
	ctx context.Context,
	principal auth.Principal,
) (SystemStatus, error) {
	workspaceID, err := authorize(principal)
	if err != nil {
		return SystemStatus{}, err
	}
	result, err := service.repository.GetSystemStatus(ctx, workspaceID)
	if err != nil {
		return SystemStatus{}, fmt.Errorf("get operations system status: %w", err)
	}
	return result, nil
}

func authorize(principal auth.Principal) (string, error) {
	if !principal.Can(auth.ScopeAdminRead) {
		return "", ErrForbidden
	}
	workspaceID, ok := identifier.NormalizeUUID(principal.WorkspaceID)
	if !ok {
		return "", ErrForbidden
	}
	return workspaceID, nil
}

func authorizeWrite(principal auth.Principal) (string, string, error) {
	if !principal.Can(auth.ScopeAdminWrite) {
		return "", "", ErrForbidden
	}
	workspaceID, validWorkspace := identifier.NormalizeUUID(principal.WorkspaceID)
	actorID, validActor := identifier.NormalizeUUID(principal.UserID)
	if !validWorkspace || !validActor {
		return "", "", ErrForbidden
	}
	return workspaceID, actorID, nil
}

func normalizeOptionalFilter(value string, allowed map[string]struct{}) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", true
	}
	_, ok := allowed[value]
	return value, ok
}

func normalizePatternFilter(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, value == "" || filterPattern.MatchString(value)
}

type listCursor struct {
	Kind    string `json:"kind"`
	Binding string `json:"binding"`
	At      string `json:"at"`
	ID      string `json:"id"`
}

func normalizeListInput(
	limit int,
	cursor,
	kind,
	binding string,
) (int, *time.Time, string, error) {
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit < 1 || limit > maxListLimit {
		return 0, nil, "", ErrInvalidInput
	}
	if cursor == "" {
		return limit, nil, "", nil
	}
	at, id, err := decodeCursor(cursor, kind, binding)
	if err != nil {
		return 0, nil, "", ErrInvalidInput
	}
	return limit, at, id, nil
}

func encodeCursor(kind, binding string, at time.Time, id string) (string, error) {
	id, ok := identifier.NormalizeUUID(id)
	if !ok || at.IsZero() {
		return "", ErrInvalidInput
	}
	encoded, err := json.Marshal(listCursor{
		Kind: kind, Binding: binding, At: at.UTC().Format(time.RFC3339Nano), ID: id,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeCursor(value, kind, binding string) (*time.Time, string, error) {
	if len(value) > maxCursorLength {
		return nil, "", ErrInvalidInput
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, "", ErrInvalidInput
	}
	var cursor listCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.Kind != kind || cursor.Binding != binding {
		return nil, "", ErrInvalidInput
	}
	at, err := time.Parse(time.RFC3339Nano, cursor.At)
	if err != nil {
		return nil, "", ErrInvalidInput
	}
	id, ok := identifier.NormalizeUUID(cursor.ID)
	if !ok {
		return nil, "", ErrInvalidInput
	}
	at = at.UTC()
	return &at, id, nil
}
