// Package llm defines the structured, provider-neutral post-ASR correction
// boundary. It is intentionally separate from ASR hotword compilation.
package llm

import (
	"context"
	"encoding/json"
	"time"
)

const (
	MockProviderID             = "mock_llm"
	OpenAICompatibleProviderID = "openai_compatible_llm"
	PromptVersionV1            = "correction.v1"
	AutoApprovalNever          = "never"
	AutoApprovalGlossaryOnly   = "validated_glossary_only"
)

type Provider interface {
	ID() string
	Capabilities() Capabilities
	ValidateProfile(profile Profile) error
	Health(ctx context.Context) error
	Correct(ctx context.Context, request Request) (Proposal, error)
}

type Capabilities struct {
	ProviderID       string `json:"provider_id"`
	StructuredPatch  bool   `json:"structured_patch"`
	CustomHeaders    bool   `json:"custom_headers"`
	MaxContextTokens int    `json:"max_context_tokens"`
	MaxConcurrency   int    `json:"max_concurrency"`
}

type Profile struct {
	ID                 string
	ProviderID         string
	BaseURL            string
	Model              string
	CustomHeaders      map[string]string
	Timeout            time.Duration
	Concurrency        int
	Temperature        float64
	ContextLimit       int
	StructuredOutput   bool
	PromptTemplate     string
	DefaultGlossaryID  string
	AutoApprovalPolicy string
}

type Credentials struct {
	APIKey string
}

func (Credentials) String() string { return "Credentials{REDACTED}" }

type Request struct {
	Language string         `json:"language"`
	Segments []Segment      `json:"segments"`
	Glossary []GlossaryRule `json:"glossary"`
}

type Segment struct {
	ID      string `json:"segment_id"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Text    string `json:"text"`
}

type GlossaryRule struct {
	CanonicalForm     string   `json:"canonical_form"`
	Aliases           []string `json:"aliases"`
	Language          string   `json:"language"`
	ContextTerms      []string `json:"context_terms"`
	ForbiddenContexts []string `json:"forbidden_contexts"`
	Regex             bool     `json:"regex"`
	CaseSensitive     bool     `json:"case_sensitive"`
	Priority          int      `json:"priority"`
	Description       string   `json:"description,omitempty"`
}

type Change struct {
	SegmentID   string  `json:"segment_id"`
	Original    string  `json:"original"`
	Replacement string  `json:"replacement"`
	Confidence  float64 `json:"confidence"`
	Reason      string  `json:"reason"`
}

type Proposal struct {
	Changes       []Change        `json:"changes"`
	RawJSON       json.RawMessage `json:"-"`
	ProviderID    string          `json:"-"`
	ProfileID     string          `json:"-"`
	Model         string          `json:"-"`
	PromptVersion string          `json:"-"`
}
