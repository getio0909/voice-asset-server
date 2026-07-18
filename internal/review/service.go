package review

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/transcript"
)

type RevisionReader interface {
	GetRevision(context.Context, string, string) (transcript.Revision, error)
}

type Repository interface {
	AddDecision(context.Context, DecisionParams) (Record, error)
	Approve(context.Context, ApprovalParams) (ApprovalResult, error)
}

type DecisionParams struct {
	ID, AuditID, WorkspaceID, RevisionID, ReviewerID, Action string
	ChangeIndex                                              *int
}

type ApprovalParams struct {
	ReviewID, AuditID, WorkspaceID, RevisionID, ReviewerID string
	HumanRevisionID, ApprovedRevisionID                    string
	HumanSegmentIDs, ApprovedSegmentIDs                    []string
	AcceptPending                                          bool
}

type Service struct {
	repository Repository
	revisions  RevisionReader
	random     io.Reader
}

func NewService(repository Repository, revisions RevisionReader) *Service {
	return &Service{repository: repository, revisions: revisions, random: rand.Reader}
}

func (service *Service) AddDecision(ctx context.Context, principal auth.Principal, revisionID string, input DecisionInput) (Record, error) {
	if !principal.Can(auth.ScopeCorrectionsWrite) {
		return Record{}, ErrForbidden
	}
	revisionID, ok := identifier.NormalizeUUID(revisionID)
	input.Action = strings.TrimSpace(input.Action)
	if !ok || !validDecision(input.Action, input.ChangeIndex) {
		return Record{}, ErrInvalidInput
	}
	reviewID, err := service.newID()
	if err != nil {
		return Record{}, err
	}
	auditID, err := service.newID()
	if err != nil {
		return Record{}, err
	}
	record, err := service.repository.AddDecision(ctx, DecisionParams{
		ID: reviewID, AuditID: auditID, WorkspaceID: principal.WorkspaceID,
		RevisionID: revisionID, ReviewerID: principal.UserID,
		Action: input.Action, ChangeIndex: cloneInt(input.ChangeIndex),
	})
	return record, publicError("record review decision", err)
}

func (service *Service) Approve(ctx context.Context, principal auth.Principal, revisionID string, input ApprovalInput) (ApprovalResult, error) {
	if !principal.Can(auth.ScopeCorrectionsWrite) {
		return ApprovalResult{}, ErrForbidden
	}
	revisionID, ok := identifier.NormalizeUUID(revisionID)
	if !ok || service.revisions == nil {
		return ApprovalResult{}, ErrInvalidInput
	}
	source, err := service.revisions.GetRevision(ctx, principal.WorkspaceID, revisionID)
	if errors.Is(err, transcript.ErrNotFound) {
		return ApprovalResult{}, ErrNotFound
	}
	if err != nil {
		return ApprovalResult{}, fmt.Errorf("%w: load approval source", ErrRepository)
	}
	if source.Kind != transcript.KindLLMCorrected || source.ParentRevisionID == "" ||
		source.ReviewStatus != "pending" || len(source.Segments) == 0 {
		return ApprovalResult{}, ErrConflict
	}
	ids := make([]string, 0, 4+len(source.Segments)*2)
	for range 4 + len(source.Segments)*2 {
		value, err := service.newID()
		if err != nil {
			return ApprovalResult{}, err
		}
		ids = append(ids, value)
	}
	params := ApprovalParams{
		ReviewID: ids[0], AuditID: ids[1], WorkspaceID: principal.WorkspaceID,
		RevisionID: revisionID, ReviewerID: principal.UserID,
		HumanRevisionID: ids[2], ApprovedRevisionID: ids[3],
		HumanSegmentIDs:    ids[4 : 4+len(source.Segments)],
		ApprovedSegmentIDs: ids[4+len(source.Segments):], AcceptPending: input.AcceptPending,
	}
	result, err := service.repository.Approve(ctx, params)
	return result, publicError("approve correction", err)
}

func validDecision(action string, changeIndex *int) bool {
	switch action {
	case ActionAcceptChange, ActionRejectChange:
		return changeIndex != nil && *changeIndex >= 0
	case ActionAcceptAll, ActionRejectAll:
		return changeIndex == nil
	default:
		return false
	}
}

func publicError(operation string, err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{ErrInvalidInput, ErrNotFound, ErrConflict} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return fmt.Errorf("%w: %s", ErrRepository, operation)
}

func (service *Service) newID() (string, error) {
	value, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return "", fmt.Errorf("generate review identifier: %w", err)
	}
	return value, nil
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
