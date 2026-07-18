package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxSegments         = 2_000
	maxSegmentRunes     = 10_000
	maxGlossaryRules    = 500
	maxProposalChanges  = 2_000
	maxReasonRunes      = 500
	maxPromptRunes      = 8_000
	maxRawResponseBytes = 8 * 1024 * 1024
	maxReplacementRatio = 0.50
	maxAggregateRatio   = 0.35
)

var (
	numberPattern = regexp.MustCompile(`\d+(?:[.,]\d+)?`)
	unitPattern   = regexp.MustCompile(`(?i)(?:%|毫秒|秒|分钟|小时|千克|公斤|公里|厘米|毫米|米|元|hz|khz|mhz|ghz|kb|mb|gb|tb|ms|kg|km|cm|mm)`)
	negations     = []string{"not", "no", "never", "without", "不", "没", "无", "未", "否"}
)

type ValidationResult struct {
	Valid              bool    `json:"valid"`
	SchemaValid        bool    `json:"schema_valid"`
	OriginalsMatch     bool    `json:"originals_match"`
	GlossarySupported  bool    `json:"glossary_supported"`
	NumbersPreserved   bool    `json:"numbers_preserved"`
	UnitsPreserved     bool    `json:"units_preserved"`
	NegationsPreserved bool    `json:"negations_preserved"`
	TimelinePreserved  bool    `json:"timeline_preserved"`
	ChangeRatio        float64 `json:"change_ratio"`
}

func DefaultMockProfile(id string) Profile {
	return Profile{
		ID: id, ProviderID: MockProviderID, Model: "deterministic_glossary_v1",
		Timeout: 30 * time.Second, Concurrency: 32, Temperature: 0,
		ContextLimit: 64_000, StructuredOutput: true,
		PromptTemplate: PromptVersionV1, AutoApprovalPolicy: AutoApprovalNever,
	}
}

func ValidateProfileDefinition(profile Profile) error {
	if strings.TrimSpace(profile.ID) == "" || strings.TrimSpace(profile.Model) == "" ||
		profile.Timeout < time.Second || profile.Timeout > 5*time.Minute ||
		profile.Concurrency < 1 || profile.Concurrency > 128 ||
		profile.Temperature < 0 || profile.Temperature > 1 ||
		profile.ContextLimit < 1_000 || profile.ContextLimit > 1_000_000 ||
		!profile.StructuredOutput || !validPrompt(profile.PromptTemplate) ||
		(profile.AutoApprovalPolicy != AutoApprovalNever && profile.AutoApprovalPolicy != AutoApprovalGlossaryOnly) {
		return errors.New("invalid LLM profile")
	}
	if profile.DefaultGlossaryID != "" && !validOpaqueIdentifier(profile.DefaultGlossaryID) {
		return errors.New("invalid default glossary identifier")
	}
	switch profile.ProviderID {
	case MockProviderID:
		if !isMockModel(profile.Model) || strings.TrimSpace(profile.BaseURL) != "" || len(profile.CustomHeaders) != 0 {
			return errors.New("mock LLM does not accept an endpoint or headers")
		}
	case OpenAICompatibleProviderID:
		if err := validateCompatibleEndpoint(profile.BaseURL); err != nil {
			return err
		}
		if err := validateCustomHeaders(profile.CustomHeaders); err != nil {
			return err
		}
	default:
		return errors.New("unsupported LLM provider")
	}
	return nil
}

func ValidateRequest(request Request) error {
	if strings.TrimSpace(request.Language) == "" || len(request.Segments) < 1 ||
		len(request.Segments) > maxSegments || len(request.Glossary) > maxGlossaryRules {
		return errors.New("invalid correction request")
	}
	segmentIDs := make(map[string]struct{}, len(request.Segments))
	for _, segment := range request.Segments {
		if strings.TrimSpace(segment.ID) == "" || !validContent(segment.Text, maxSegmentRunes) ||
			segment.StartMS < 0 || segment.EndMS < segment.StartMS {
			return errors.New("invalid correction segment")
		}
		if _, exists := segmentIDs[segment.ID]; exists {
			return errors.New("duplicate correction segment")
		}
		segmentIDs[segment.ID] = struct{}{}
	}
	for _, rule := range request.Glossary {
		if err := validateGlossaryRule(rule); err != nil {
			return err
		}
	}
	return nil
}

func ValidateProposal(request Request, proposal Proposal) (ValidationResult, error) {
	result := ValidationResult{
		SchemaValid: true, OriginalsMatch: true, GlossarySupported: true,
		NumbersPreserved: true, UnitsPreserved: true, NegationsPreserved: true,
		TimelinePreserved: true,
	}
	if err := ValidateRequest(request); err != nil || len(proposal.Changes) > maxProposalChanges ||
		!validJSONObject(proposal.RawJSON) || strings.TrimSpace(proposal.ProviderID) == "" ||
		strings.TrimSpace(proposal.Model) == "" || strings.TrimSpace(proposal.PromptVersion) == "" {
		result.SchemaValid = false
		return result, newProviderError(proposal.ProviderID, "validate", ErrorUnsafeProposal, "schema", err)
	}
	segments := make(map[string]Segment, len(request.Segments))
	totalRunes := 0
	for _, segment := range request.Segments {
		segments[segment.ID] = segment
		totalRunes += utf8.RuneCountInString(segment.Text)
	}
	seen := make(map[string]struct{}, len(proposal.Changes))
	changedRunes := 0
	for _, change := range proposal.Changes {
		segment, exists := segments[change.SegmentID]
		if !exists || change.Original != segment.Text {
			result.OriginalsMatch = false
			return result, newProviderError(proposal.ProviderID, "validate", ErrorUnsafeProposal, "original_mismatch", nil)
		}
		if _, duplicate := seen[change.SegmentID]; duplicate {
			result.SchemaValid = false
			return result, newProviderError(proposal.ProviderID, "validate", ErrorUnsafeProposal, "duplicate_segment", nil)
		}
		seen[change.SegmentID] = struct{}{}
		if !validContent(change.Replacement, maxSegmentRunes) ||
			change.Confidence < 0 || change.Confidence > 1 ||
			!validContent(change.Reason, maxReasonRunes) || change.Replacement == change.Original {
			result.SchemaValid = false
			return result, newProviderError(proposal.ProviderID, "validate", ErrorUnsafeProposal, "invalid_change", nil)
		}
		expected := ApplyGlossary(change.Original, request.Language, request.Glossary)
		if expected == change.Original || change.Replacement != expected {
			result.GlossarySupported = false
			return result, newProviderError(proposal.ProviderID, "validate", ErrorUnsafeProposal, "unsupported_change", nil)
		}
		if !equalMatches(numberPattern, change.Original, change.Replacement) {
			result.NumbersPreserved = false
			return result, newProviderError(proposal.ProviderID, "validate", ErrorUnsafeProposal, "numbers_changed", nil)
		}
		if !equalMatches(unitPattern, change.Original, change.Replacement) {
			result.UnitsPreserved = false
			return result, newProviderError(proposal.ProviderID, "validate", ErrorUnsafeProposal, "units_changed", nil)
		}
		if !negationsPreserved(change.Original, change.Replacement) {
			result.NegationsPreserved = false
			return result, newProviderError(proposal.ProviderID, "validate", ErrorUnsafeProposal, "negation_changed", nil)
		}
		changed, ratio := changedSpan(change.Original, change.Replacement)
		if ratio > maxReplacementRatio {
			return result, newProviderError(proposal.ProviderID, "validate", ErrorUnsafeProposal, "change_ratio", nil)
		}
		changedRunes += changed
	}
	if totalRunes > 0 {
		result.ChangeRatio = float64(changedRunes) / float64(totalRunes)
	}
	if result.ChangeRatio > maxAggregateRatio {
		return result, newProviderError(proposal.ProviderID, "validate", ErrorUnsafeProposal, "aggregate_ratio", nil)
	}
	result.Valid = true
	return result, nil
}

func ApplyGlossary(text, language string, rules []GlossaryRule) string {
	ordered := append([]GlossaryRule(nil), rules...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return ordered[left].Priority > ordered[right].Priority
	})
	result := text
	for _, rule := range ordered {
		if rule.Language != "*" && !strings.EqualFold(rule.Language, language) {
			continue
		}
		if containsAny(result, rule.ForbiddenContexts, rule.CaseSensitive) ||
			(len(rule.ContextTerms) > 0 && !containsAny(result, rule.ContextTerms, rule.CaseSensitive)) {
			continue
		}
		for _, alias := range rule.Aliases {
			pattern := regexp.QuoteMeta(alias)
			if rule.Regex {
				pattern = alias
			}
			if !rule.CaseSensitive {
				pattern = "(?i:" + pattern + ")"
			}
			compiled, err := regexp.Compile(pattern)
			if err != nil {
				continue
			}
			// Canonical text is data, not a regexp replacement template. A '$'
			// in a product name must therefore remain literal.
			result = compiled.ReplaceAllStringFunc(result, func(string) string {
				return rule.CanonicalForm
			})
		}
	}
	return result
}

func DecodeStructuredProposal(value string) (Proposal, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	var proposal Proposal
	if err := decoder.Decode(&proposal); err != nil {
		return Proposal{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Proposal{}, errors.New("proposal contains trailing JSON")
	}
	return proposal, nil
}

func validateGlossaryRule(rule GlossaryRule) error {
	if !validContent(rule.CanonicalForm, 200) || len(rule.Aliases) < 1 || len(rule.Aliases) > 50 ||
		(rule.Language != "*" && strings.TrimSpace(rule.Language) == "") ||
		len(rule.ContextTerms) > 50 || len(rule.ForbiddenContexts) > 50 ||
		rule.Priority < 1 || rule.Priority > 1_000 || !validOptionalContent(rule.Description, 500) {
		return errors.New("invalid glossary rule")
	}
	for _, value := range append(append(append([]string{}, rule.Aliases...), rule.ContextTerms...), rule.ForbiddenContexts...) {
		if !validContent(value, 200) {
			return errors.New("invalid glossary term")
		}
		if rule.Regex {
			if len(value) > 500 {
				return errors.New("glossary regex is too large")
			}
			if _, err := regexp.Compile(value); err != nil {
				return errors.New("invalid glossary regex")
			}
		}
	}
	return nil
}

func containsAny(text string, values []string, caseSensitive bool) bool {
	if !caseSensitive {
		text = strings.ToLower(text)
	}
	for _, value := range values {
		if !caseSensitive {
			value = strings.ToLower(value)
		}
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func equalMatches(pattern *regexp.Regexp, left, right string) bool {
	leftMatches := pattern.FindAllString(strings.ToLower(left), -1)
	rightMatches := pattern.FindAllString(strings.ToLower(right), -1)
	sort.Strings(leftMatches)
	sort.Strings(rightMatches)
	return slices.Equal(leftMatches, rightMatches)
}

func negationsPreserved(left, right string) bool {
	left = strings.ToLower(left)
	right = strings.ToLower(right)
	for _, negation := range negations {
		if strings.Count(left, negation) != strings.Count(right, negation) {
			return false
		}
	}
	return true
}

func changedSpan(left, right string) (int, float64) {
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	prefix := 0
	for prefix < len(leftRunes) && prefix < len(rightRunes) && leftRunes[prefix] == rightRunes[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(leftRunes)-prefix && suffix < len(rightRunes)-prefix &&
		leftRunes[len(leftRunes)-1-suffix] == rightRunes[len(rightRunes)-1-suffix] {
		suffix++
	}
	changed := max(len(leftRunes)-prefix-suffix, len(rightRunes)-prefix-suffix)
	denominator := max(len(leftRunes), len(rightRunes))
	if denominator == 0 {
		return changed, 0
	}
	return changed, float64(changed) / float64(denominator)
}

func validContent(value string, maxRunes int) bool {
	return strings.TrimSpace(value) != "" && validOptionalContent(value, maxRunes)
}

func validOptionalContent(value string, maxRunes int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return false
	}
	for _, character := range value {
		if character == 0 || (unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t') {
			return false
		}
	}
	return true
}

func validPrompt(value string) bool {
	return validContent(value, maxPromptRunes)
}

func validOpaqueIdentifier(value string) bool {
	return utf8.ValidString(value) && len(value) <= 256 && strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func validJSONObject(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) > 1 && len(trimmed) <= maxRawResponseBytes && trimmed[0] == '{' && json.Valid(trimmed)
}
