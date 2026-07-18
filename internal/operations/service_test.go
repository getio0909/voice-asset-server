package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

const operationsWorkspaceID = "10000000-0000-4000-8000-000000000071"

func TestListJobsPaginatesAndBindsCursorToFilters(t *testing.T) {
	base := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{jobs: []JobSummary{
		{ID: "30000000-0000-4000-8000-000000000073", State: "failed", Kind: "mock_transcribe", UpdatedAt: base.Add(3 * time.Minute)},
		{ID: "30000000-0000-4000-8000-000000000072", State: "failed", Kind: "mock_transcribe", UpdatedAt: base.Add(2 * time.Minute)},
		{ID: "30000000-0000-4000-8000-000000000071", State: "failed", Kind: "mock_transcribe", UpdatedAt: base.Add(time.Minute)},
	}}
	service := NewService(repository)
	principal := adminPrincipal()

	first, err := service.ListJobs(context.Background(), principal, JobListInput{
		Limit: 2, State: "failed", Kind: "mock_transcribe",
	})
	if err != nil || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("ListJobs(first) = (%+v, %v)", first, err)
	}
	if repository.jobParams.Limit != 3 || repository.jobParams.WorkspaceID != operationsWorkspaceID {
		t.Fatalf("job params = %+v", repository.jobParams)
	}

	repository.jobs = []JobSummary{{
		ID: "30000000-0000-4000-8000-000000000071", UpdatedAt: base.Add(time.Minute),
	}}
	second, err := service.ListJobs(context.Background(), principal, JobListInput{
		Limit: 2, State: "failed", Kind: "mock_transcribe", Cursor: *first.NextCursor,
	})
	if err != nil || len(second.Items) != 1 || second.NextCursor != nil {
		t.Fatalf("ListJobs(second) = (%+v, %v)", second, err)
	}
	if repository.jobParams.BeforeUpdatedAt == nil ||
		!repository.jobParams.BeforeUpdatedAt.Equal(base.Add(2*time.Minute)) ||
		repository.jobParams.BeforeID != "30000000-0000-4000-8000-000000000072" {
		t.Fatalf("second job params = %+v", repository.jobParams)
	}

	if _, err := service.ListJobs(context.Background(), principal, JobListInput{
		Limit: 2, State: "succeeded", Kind: "mock_transcribe", Cursor: *first.NextCursor,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ListJobs(changed filter) error = %v", err)
	}
}

func TestListAuditLogsReturnsBoundedMetadataAndCursor(t *testing.T) {
	base := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{audits: []AuditEntry{
		{ID: "40000000-0000-4000-8000-000000000073", ActorType: "user", Action: "asset.read", TargetType: "asset", Metadata: json.RawMessage(`{"result":"ok"}`), OccurredAt: base.Add(3 * time.Minute)},
		{ID: "40000000-0000-4000-8000-000000000072", ActorType: "user", Action: "asset.read", TargetType: "asset", OccurredAt: base.Add(2 * time.Minute)},
	}}
	service := NewService(repository)

	result, err := service.ListAuditLogs(context.Background(), adminPrincipal(), AuditListInput{
		Limit: 1, ActorType: "user", Action: "asset.read", TargetType: "asset",
	})
	if err != nil || len(result.Items) != 1 || result.NextCursor == nil {
		t.Fatalf("ListAuditLogs() = (%+v, %v)", result, err)
	}
	if string(result.Items[0].Metadata) != `{"result":"ok"}` || repository.auditParams.Limit != 2 {
		t.Fatalf("audit result/params = (%+v, %+v)", result.Items[0], repository.auditParams)
	}
}

func TestOperationsReadsRequireAdminScopeAndValidInput(t *testing.T) {
	service := NewService(&fakeRepository{})
	nonAdmin := adminPrincipal()
	nonAdmin.Scopes = []string{auth.ScopeAssetsRead}
	if _, err := service.ListJobs(context.Background(), nonAdmin, JobListInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListJobs(non-admin) error = %v", err)
	}
	invalidWorkspace := adminPrincipal()
	invalidWorkspace.WorkspaceID = "not-a-workspace"
	if _, err := service.GetSystemStatus(context.Background(), invalidWorkspace); !errors.Is(err, ErrForbidden) {
		t.Fatalf("GetSystemStatus(invalid workspace) error = %v", err)
	}

	for name, input := range map[string]JobListInput{
		"limit": {Limit: maxListLimit + 1},
		"state": {State: "unknown"},
		"kind":  {Kind: "bad kind"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.ListJobs(context.Background(), adminPrincipal(), input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("ListJobs() error = %v", err)
			}
		})
	}
	for name, input := range map[string]AuditListInput{
		"actor":  {ActorType: "robot"},
		"action": {Action: "bad action"},
		"target": {TargetType: "../asset"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.ListAuditLogs(context.Background(), adminPrincipal(), input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("ListAuditLogs() error = %v", err)
			}
		})
	}
}

func TestGetSystemStatusReturnsRepositorySnapshot(t *testing.T) {
	expected := SystemStatus{
		GeneratedAt: time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC),
		ActiveUsers: 2,
		Assets:      AssetStatus{Total: 4, Active: 3, Trashed: 1, AudioDurationMS: 42_000},
		Jobs:        JobStatus{Total: 5, Failed: 1, Succeeded: 4},
	}
	repository := &fakeRepository{status: expected}
	result, err := NewService(repository).GetSystemStatus(context.Background(), adminPrincipal())
	if err != nil || result != expected || repository.statusWorkspaceID != operationsWorkspaceID {
		t.Fatalf("GetSystemStatus() = (%+v, %v), workspace = %q", result, err, repository.statusWorkspaceID)
	}
}

func TestRetryJobRequiresAdminWriteAndPassesAnAuditedSingleAttemptCommand(t *testing.T) {
	now := time.Date(2026, 7, 18, 3, 0, 0, 0, time.UTC)
	jobID := "30000000-0000-4000-8000-000000000074"
	repository := &fakeRepository{retryResult: JobSummary{
		ID: jobID, Kind: "mock_transcribe", State: "queued", Attempts: 3, MaxAttempts: 4,
	}}
	service := NewService(repository)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 16))
	service.now = func() time.Time { return now }
	principal := adminPrincipal()
	principal.UserID = "20000000-0000-4000-8000-000000000071"
	principal.Scopes = []string{auth.ScopeAdminWrite}

	retried, err := service.RetryJob(context.Background(), principal, jobID, "retry-request")
	if err != nil || retried.State != "queued" || retried.MaxAttempts != 4 {
		t.Fatalf("RetryJob() = (%+v, %v)", retried, err)
	}
	want := RetryJobParams{
		JobID: jobID, AuditID: "42424242-4242-4242-8242-424242424242",
		WorkspaceID: operationsWorkspaceID, ActorID: principal.UserID,
		RequestID: "retry-request", AvailableAt: now,
	}
	if repository.retryParams != want {
		t.Fatalf("retry params = %+v, want %+v", repository.retryParams, want)
	}

	principal.Scopes = []string{auth.ScopeAdminRead}
	if _, err := service.RetryJob(context.Background(), principal, jobID, "retry-request"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("RetryJob(read-only) error = %v", err)
	}
	principal.Scopes = []string{auth.ScopeAdminWrite}
	if _, err := service.RetryJob(context.Background(), principal, "not-a-job", "retry-request"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RetryJob(invalid ID) error = %v", err)
	}
	if _, err := service.RetryJob(context.Background(), principal, jobID, ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("RetryJob(empty request ID) error = %v", err)
	}
}

func adminPrincipal() auth.Principal {
	return auth.Principal{WorkspaceID: operationsWorkspaceID, Scopes: []string{auth.ScopeAdminRead}}
}

type fakeRepository struct {
	jobs              []JobSummary
	audits            []AuditEntry
	status            SystemStatus
	jobParams         JobListParams
	auditParams       AuditListParams
	statusWorkspaceID string
	retryResult       JobSummary
	retryParams       RetryJobParams
	retryErr          error
}

func (repository *fakeRepository) ListJobs(_ context.Context, params JobListParams) ([]JobSummary, error) {
	repository.jobParams = params
	return append([]JobSummary(nil), repository.jobs...), nil
}

func (repository *fakeRepository) ListAuditLogs(_ context.Context, params AuditListParams) ([]AuditEntry, error) {
	repository.auditParams = params
	return append([]AuditEntry(nil), repository.audits...), nil
}

func (repository *fakeRepository) GetSystemStatus(_ context.Context, workspaceID string) (SystemStatus, error) {
	repository.statusWorkspaceID = workspaceID
	return repository.status, nil
}

func (repository *fakeRepository) RetryJob(_ context.Context, params RetryJobParams) (JobSummary, error) {
	repository.retryParams = params
	return repository.retryResult, repository.retryErr
}
