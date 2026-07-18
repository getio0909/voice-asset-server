// Package review records append-only correction decisions and creates
// immutable human-edited and approved transcript revisions.
package review

import (
	"errors"
	"time"

	"github.com/getio0909/voice-asset-server/internal/transcript"
)

const (
	ActionAcceptChange = "accept_change"
	ActionRejectChange = "reject_change"
	ActionAcceptAll    = "accept_all"
	ActionRejectAll    = "reject_all"
	ActionApprove      = "approve"
)

var (
	ErrForbidden    = errors.New("review access forbidden")
	ErrInvalidInput = errors.New("invalid review input")
	ErrNotFound     = errors.New("review target not found")
	ErrConflict     = errors.New("review conflict")
	ErrRepository   = errors.New("review repository failure")
)

type DecisionInput struct {
	Action      string `json:"action"`
	ChangeIndex *int   `json:"change_index,omitempty"`
}

type ApprovalInput struct {
	// AcceptPending controls undecided changes. False is the conservative
	// default: only explicitly accepted changes enter the approved revision.
	AcceptPending bool `json:"accept_pending"`
}

type Record struct {
	ID                  string    `json:"id"`
	RevisionID          string    `json:"revision_id"`
	ReviewerID          string    `json:"reviewer_id"`
	Action              string    `json:"action"`
	ChangeIndex         *int      `json:"change_index,omitempty"`
	ResultingRevisionID string    `json:"resulting_revision_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

type ApprovalResult struct {
	ReviewRecord     Record              `json:"review"`
	HumanRevision    transcript.Revision `json:"human_revision"`
	ApprovedRevision transcript.Revision `json:"approved_revision"`
}
