// Package glossary manages versioned post-ASR LLM correction dictionaries.
// It is deliberately independent from the ASR hotword subsystem.
package glossary

import (
	"errors"
	"time"

	"github.com/getio0909/voice-asset-server/internal/llm"
)

const (
	ScopeWorkspace  = "workspace"
	ScopeCollection = "collection"
	ScopeAsset      = "asset"

	StateEnabled  = "enabled"
	StateDisabled = "disabled"
)

var (
	ErrForbidden    = errors.New("glossary access forbidden")
	ErrInvalidInput = errors.New("invalid glossary input")
	ErrConflict     = errors.New("glossary conflict")
	ErrNotFound     = errors.New("glossary set not found")
	ErrRepository   = errors.New("glossary repository failure")
)

type Entry = llm.GlossaryRule

type Set struct {
	ID              string    `json:"id"`
	WorkspaceID     string    `json:"workspace_id"`
	DisplayName     string    `json:"display_name"`
	ScopeType       string    `json:"scope_type"`
	ScopeID         *string   `json:"scope_id"`
	State           string    `json:"state"`
	CurrentVersion  int       `json:"current_version"`
	ResourceVersion int64     `json:"resource_version"`
	Entries         []Entry   `json:"entries"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CreateInput struct {
	DisplayName string  `json:"display_name"`
	ScopeType   string  `json:"scope_type"`
	ScopeID     *string `json:"scope_id,omitempty"`
	State       string  `json:"state,omitempty"`
	Entries     []Entry `json:"entries"`
}

type AddVersionInput struct {
	Entries []Entry `json:"entries"`
}

type UpdateInput struct {
	State *string `json:"state,omitempty"`
}

type Resolution struct {
	Rules    []llm.GlossaryRule `json:"rules"`
	Snapshot []byte             `json:"snapshot"`
}

type resolvedSet struct {
	ID             string
	ScopeType      string
	ScopeID        *string
	CurrentVersion int
	Entries        []Entry
}
