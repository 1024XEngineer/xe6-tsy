package matter

import "time"

// SessionID identifies the consultation session bound to a suggestion attempt.
type SessionID string

// OrganizationID identifies the organization that owns the session and suggestion scope.
type OrganizationID string

// ConfigVersionID identifies the immutable configuration version used for generation.
type ConfigVersionID string

// SuggestionOutcome distinguishes generated suggestions from typed business non-success results.
type SuggestionOutcome string

const (
	// SuggestionOutcomeGenerated means module 5 produced a suggestion set.
	SuggestionOutcomeGenerated SuggestionOutcome = "generated"
	// SuggestionOutcomeNoCandidate means provider execution completed without usable matter candidates.
	SuggestionOutcomeNoCandidate SuggestionOutcome = "no_candidate"
	// SuggestionOutcomeProviderFailed means provider execution failed but the failure is a typed result.
	SuggestionOutcomeProviderFailed SuggestionOutcome = "provider_failed"
)

// SuggestionFreshness is the read-time projection for immutable suggestion set bodies.
type SuggestionFreshness string

const (
	// SuggestionFreshnessCurrent means the suggestion set still matches the current transcript basis.
	SuggestionFreshnessCurrent SuggestionFreshness = "current"
	// SuggestionFreshnessStale means upstream evidence changed after the suggestion set was created.
	SuggestionFreshnessStale SuggestionFreshness = "stale"
	// SuggestionFreshnessExpired means the suggestion set is no longer valid for time-bound use.
	SuggestionFreshnessExpired SuggestionFreshness = "expired"
)

// EvidenceKind restricts module 5 evidence to stable transcript inputs.
type EvidenceKind string

const (
	// EvidenceKindFinal marks final transcript evidence.
	EvidenceKindFinal EvidenceKind = "final"
	// EvidenceKindManual marks manually corrected transcript evidence.
	EvidenceKindManual EvidenceKind = "manual"
)

// SourceKind names the immutable transcript source backing a suggestion.
type SourceKind string

const (
	// SourceKindTranscriptFinal points to final transcript text.
	SourceKindTranscriptFinal SourceKind = "transcript_final"
	// SourceKindTranscriptManual points to manually corrected transcript text.
	SourceKindTranscriptManual SourceKind = "transcript_manual"
)

// EntityRef references an immutable upstream entity version.
type EntityRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// ApplicableScope captures the organization and optional service boundaries for a suggestion attempt.
type ApplicableScope struct {
	OrganizationID  OrganizationID `json:"organization_id"`
	RegionCodes     []string       `json:"region_codes,omitempty"`
	ServicePointIDs []string       `json:"service_point_ids,omitempty"`
	WindowIDs       []string       `json:"window_ids,omitempty"`
}

// TranscriptVersionSet binds suggestion generation to audited final or manual transcript versions.
type TranscriptVersionSet struct {
	Watermark string                 `json:"watermark"`
	Digest    string                 `json:"digest"`
	Items     []TranscriptVersionRef `json:"items"`
}

// TranscriptVersionRef identifies one transcript segment version included in a generation basis.
type TranscriptVersionRef struct {
	SegmentID string `json:"segment_id"`
	Version   string `json:"version"`
}

// SourceRef points from a suggestion back to a transcript source span.
type SourceRef struct {
	Kind     SourceKind `json:"kind"`
	EntityID string     `json:"entity_id"`
	Version  string     `json:"version"`
	TextSpan TextSpan   `json:"text_span"`
}

// TextSpan identifies a half-open character range in source text.
type TextSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// MatterTaxonomyRef binds generation to a specific matter taxonomy version.
type MatterTaxonomyRef struct {
	TaxonomyID string `json:"taxonomy_id"`
	Version    string `json:"version"`
}

// AnalysisContextInput is the optional read-only module 6 pre-retrieval projection consumed by module 5.
type AnalysisContextInput struct {
	ContextID        string    `json:"context_id"`
	OperationID      string    `json:"operation_id"`
	TranscriptDigest string    `json:"transcript_digest"`
	ExpiresAt        time.Time `json:"expires_at"`
}

// EvidenceBlock is a stable final or manual transcript block supplied to matter generation.
type EvidenceBlock struct {
	BlockID      string       `json:"block_id"`
	SpeakerLabel string       `json:"speaker_label"`
	Kind         EvidenceKind `json:"kind"`
	Text         string       `json:"text"`
	Confidence   *float64     `json:"confidence,omitempty"`
	SourceRefs   []SourceRef  `json:"source_refs"`
}

// GenerateMatterSuggestionsCommand is the module 5 input contract for creating a matter suggestion set.
type GenerateMatterSuggestionsCommand struct {
	SessionID          SessionID             `json:"session_id"`
	OrganizationID     OrganizationID        `json:"organization_id"`
	ConfigVersionID    ConfigVersionID       `json:"config_version_id"`
	Scope              ApplicableScope       `json:"scope"`
	TaxonomyRef        MatterTaxonomyRef     `json:"taxonomy_ref"`
	TranscriptVersions TranscriptVersionSet  `json:"transcript_versions"`
	EvidenceBlocks     []EvidenceBlock       `json:"evidence_blocks"`
	AnalysisContext    *AnalysisContextInput `json:"analysis_context,omitempty"`
}

// MatterSuggestionResult is the typed result returned by a module 5 generation attempt.
type MatterSuggestionResult struct {
	AttemptID     string               `json:"attempt_id"`
	Outcome       SuggestionOutcome    `json:"outcome"`
	SuggestionSet *MatterSuggestionSet `json:"suggestion_set,omitempty"`
	ErrorCode     string               `json:"error_code,omitempty"`
	Retryable     bool                 `json:"retryable"`
}

// MatterSuggestionSetView adds read-time freshness to the immutable suggestion set body.
type MatterSuggestionSetView struct {
	SuggestionSet MatterSuggestionSet `json:"suggestion_set"`
	Freshness     SuggestionFreshness `json:"freshness"`
	StateVersion  string              `json:"state_version"`
	Reason        string              `json:"reason,omitempty"`
}

// MatterSuggestionSet is the immutable module 5 package of matter, fact, and follow-up suggestions.
type MatterSuggestionSet struct {
	SuggestionSetID    string               `json:"suggestion_set_id"`
	OperationID        string               `json:"operation_id"`
	Version            string               `json:"version"`
	SessionID          SessionID            `json:"session_id"`
	OrganizationID     OrganizationID       `json:"organization_id"`
	ConfigVersionID    ConfigVersionID      `json:"config_version_id"`
	Scope              ApplicableScope      `json:"scope"`
	TaxonomyRef        MatterTaxonomyRef    `json:"taxonomy_ref"`
	TranscriptVersions TranscriptVersionSet `json:"transcript_versions"`
	AnalysisContextID  string               `json:"analysis_context_id,omitempty"`
	Summary            string               `json:"summary"`
	Matters            []MatterSuggestion   `json:"matters"`
	Facts              []FactSuggestion     `json:"facts"`
	MissingFieldCodes  []string             `json:"missing_field_codes"`
	FollowUps          []FollowUpSuggestion `json:"follow_ups"`
	Generation         GenerationInfo       `json:"generation"`
	CreatedAt          time.Time            `json:"created_at"`
}

// MatterSuggestion represents one candidate matter classification with transcript source references.
type MatterSuggestion struct {
	SuggestionID string      `json:"suggestion_id"`
	MatterCode   string      `json:"matter_code"`
	MatterName   string      `json:"matter_name"`
	Confidence   float64     `json:"confidence"`
	Rationale    string      `json:"rationale"`
	SourceRefs   []SourceRef `json:"source_refs"`
}

// FactSuggestion represents one candidate field value grounded in final or manual transcript sources.
type FactSuggestion struct {
	SuggestionID string      `json:"suggestion_id"`
	FieldCode    string      `json:"field_code"`
	FieldName    string      `json:"field_name"`
	Value        FactValue   `json:"value"`
	Confidence   float64     `json:"confidence"`
	Required     bool        `json:"required"`
	SourceRefs   []SourceRef `json:"source_refs"`
}

// FactValue carries both display text and an optional normalized value for a suggested fact.
type FactValue struct {
	ValueType   string `json:"value_type"`
	DisplayText string `json:"display_text"`
	Normalized  string `json:"normalized,omitempty"`
}

// FollowUpSuggestion is a question module 7 may use when required facts remain missing.
type FollowUpSuggestion struct {
	SuggestionID     string   `json:"suggestion_id"`
	Question         string   `json:"question"`
	TargetFieldCodes []string `json:"target_field_codes"`
	Priority         int      `json:"priority"`
}

// GenerationInfo records non-sensitive provider metadata for a suggestion set.
type GenerationInfo struct {
	ProviderKind        string `json:"provider_kind"`
	ModelVersion        string `json:"model_version,omitempty"`
	ProviderTraceRef    string `json:"provider_trace_ref,omitempty"`
	UsedAnalysisContext bool   `json:"used_analysis_context"`
	Degraded            bool   `json:"degraded"`
}
