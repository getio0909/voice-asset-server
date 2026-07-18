// Package hotword manages provider-neutral ASR hotwords independently from
// post-transcription correction dictionaries.
package hotword

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asr"
)

const (
	ScopeWorkspace  = "workspace"
	ScopeCollection = "collection"
	ScopeAsset      = "asset"

	StateEnabled  = "enabled"
	StateDisabled = "disabled"
)

var (
	ErrForbidden    = errors.New("hotword access forbidden")
	ErrInvalidInput = errors.New("invalid hotword input")
	ErrConflict     = errors.New("hotword conflict")
	ErrNotFound     = errors.New("hotword set not found")
	ErrRepository   = errors.New("hotword repository failure")
)

// Entry is the provider-neutral representation persisted in an immutable set
// version. ProviderMapping is validated but never interpreted by the worker;
// provider adapters own vendor-specific compilation.
type Entry struct {
	Term            string                     `json:"term"`
	Aliases         []string                   `json:"aliases"`
	Language        string                     `json:"language"`
	Weight          int                        `json:"weight"`
	ProviderMapping map[string]json.RawMessage `json:"provider_mapping"`
	Enabled         bool                       `json:"enabled"`
	Description     string                     `json:"description,omitempty"`
}

// EntryInput uses a pointer so omitted enabled values can safely default to
// true without making it impossible to submit an explicit false value.
type EntryInput struct {
	Term            string                     `json:"term"`
	Aliases         []string                   `json:"aliases,omitempty"`
	Language        string                     `json:"language"`
	Weight          int                        `json:"weight"`
	ProviderMapping map[string]json.RawMessage `json:"provider_mapping,omitempty"`
	Enabled         *bool                      `json:"enabled,omitempty"`
	Description     string                     `json:"description,omitempty"`
}

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
	DisplayName string       `json:"display_name"`
	ScopeType   string       `json:"scope_type"`
	ScopeID     *string      `json:"scope_id,omitempty"`
	State       string       `json:"state,omitempty"`
	Entries     []EntryInput `json:"entries"`
}

type AddVersionInput struct {
	Entries []EntryInput `json:"entries"`
}

type UpdateInput struct {
	State *string `json:"state,omitempty"`
}

type Resolution struct {
	Hotwords []asr.Hotword   `json:"hotwords"`
	Snapshot json.RawMessage `json:"snapshot"`
}

type resolvedSet struct {
	ID             string
	DisplayName    string
	ScopeType      string
	ScopeID        *string
	CurrentVersion int
	Entries        []Entry
}
