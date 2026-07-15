package reference

import "time"

// SuggestionOutcome is used for generated suggestions and recoverable provider outcomes.
type SuggestionOutcome string

const (
	SuggestionOutcomeGenerated         SuggestionOutcome = "generated"
	SuggestionOutcomeNoMatch           SuggestionOutcome = "no_match"
	SuggestionOutcomeProviderFailed    SuggestionOutcome = "provider_failed"
	SuggestionOutcomeBundleUnavailable SuggestionOutcome = "bundle_unavailable"
	SuggestionOutcomeScopeMismatch     SuggestionOutcome = "scope_mismatch"
)

// AnalysisContextOutcome is the typed result for pre-analysis retrieval.
type AnalysisContextOutcome string

const (
	AnalysisContextOutcomePrepared          AnalysisContextOutcome = "prepared"
	AnalysisContextOutcomeNoMatch           AnalysisContextOutcome = "no_match"
	AnalysisContextOutcomeProviderFailed    AnalysisContextOutcome = "provider_failed"
	AnalysisContextOutcomeBundleUnavailable AnalysisContextOutcome = "bundle_unavailable"
	AnalysisContextOutcomeScopeMismatch     AnalysisContextOutcome = "scope_mismatch"
)

// SuggestionFreshness describes whether an immutable suggestion set still matches its basis.
type SuggestionFreshness string

const (
	SuggestionFreshnessCurrent SuggestionFreshness = "current"
	SuggestionFreshnessStale   SuggestionFreshness = "stale"
	SuggestionFreshnessExpired SuggestionFreshness = "expired"
)

// ReferenceBasisKind identifies the source used to generate reference suggestions.
type ReferenceBasisKind string

const (
	ReferenceBasisKindMatterSuggestionSet ReferenceBasisKind = "matter_suggestion_set"
	ReferenceBasisKindRecordWorkingMatter ReferenceBasisKind = "record_working_matter"
)

// MaterialRequiredHint preserves provider uncertainty without turning it into a final decision.
type MaterialRequiredHint string

const (
	MaterialRequiredHintRequired    MaterialRequiredHint = "required"
	MaterialRequiredHintConditional MaterialRequiredHint = "conditional"
	MaterialRequiredHintOptional    MaterialRequiredHint = "optional"
	MaterialRequiredHintUnknown     MaterialRequiredHint = "unknown"
)

// AnalysisPurpose restricts pre-analysis contexts to module-five assistance.
type AnalysisPurpose string

const (
	AnalysisPurposeMatterAnalysisOnly AnalysisPurpose = "matter_analysis_only"
)

// ApplicableScope binds module-six reads to an organization and optional service scope.
type ApplicableScope struct {
	OrganizationID  string   `json:"organization_id"`
	RegionCodes     []string `json:"region_codes,omitempty"`
	ServicePointIDs []string `json:"service_point_ids,omitempty"`
	WindowIDs       []string `json:"window_ids,omitempty"`
}

// KnowledgePublicationRef identifies the exact published knowledge bundle version to read.
type KnowledgePublicationRef struct {
	PublicationID      string `json:"publication_id"`
	PublicationVersion string `json:"publication_version"`
	ManifestHash       string `json:"manifest_hash"`
}

// TranscriptVersionSet binds generation to stable final/manual transcript versions.
type TranscriptVersionSet struct {
	Watermark string                 `json:"watermark"`
	Digest    string                 `json:"digest"`
	Items     []TranscriptVersionRef `json:"items"`
}

// TranscriptVersionRef identifies one immutable transcript segment version.
type TranscriptVersionRef struct {
	SegmentID string `json:"segment_id"`
	Version   string `json:"version"`
}

// TextSpan points to a half-open range in source text.
type TextSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// SourceRef binds facts or matter projections back to final/manual transcript text.
type SourceRef struct {
	Kind     string   `json:"kind"`
	EntityID string   `json:"entity_id"`
	Version  string   `json:"version"`
	TextSpan TextSpan `json:"text_span"`
}

// RetrieveAnalysisContextCommand requests a short-lived retrieval context for module five.
type RetrieveAnalysisContextCommand struct {
	SessionID          string                  `json:"session_id"`
	OrganizationID     string                  `json:"organization_id"`
	ConfigVersionID    string                  `json:"config_version_id"`
	PublicationRef     KnowledgePublicationRef `json:"publication_ref"`
	Scope              ApplicableScope         `json:"scope"`
	TranscriptVersions TranscriptVersionSet    `json:"transcript_versions"`
	MinimizedQueryText string                  `json:"minimized_query_text"`
}

// AnalysisContextResult returns a typed retrieval outcome without inventing empty success.
type AnalysisContextResult struct {
	OperationID string                    `json:"operation_id"`
	Outcome     AnalysisContextOutcome    `json:"outcome"`
	Context     *AnalysisRetrievalContext `json:"context,omitempty"`
	ErrorCode   string                    `json:"error_code,omitempty"`
	Retryable   bool                      `json:"retryable"`
}

// AnalysisRetrievalContext is an immutable, short-lived context for matter analysis only.
type AnalysisRetrievalContext struct {
	ContextID          string                  `json:"context_id"`
	OperationID        string                  `json:"operation_id"`
	Purpose            AnalysisPurpose         `json:"purpose"`
	SessionID          string                  `json:"session_id"`
	OrganizationID     string                  `json:"organization_id"`
	ConfigVersionID    string                  `json:"config_version_id"`
	Scope              ApplicableScope         `json:"scope"`
	PublicationRef     KnowledgePublicationRef `json:"publication_ref"`
	TranscriptVersions TranscriptVersionSet    `json:"transcript_versions"`
	Items              []AnalysisContextItem   `json:"items"`
	CreatedAt          time.Time               `json:"created_at"`
	ExpiresAt          time.Time               `json:"expires_at"`
}

// AnalysisContextItem is a verified retrieval item from a published knowledge bundle.
type AnalysisContextItem struct {
	KnowledgeEntryVersionID string          `json:"knowledge_entry_version_id"`
	Title                   string          `json:"title"`
	Summary                 string          `json:"summary"`
	Scope                   ApplicableScope `json:"scope"`
	ContentHash             string          `json:"content_hash"`
}

// GenerateReferenceSuggestionsCommand requests policy and material suggestions from a basis.
type GenerateReferenceSuggestionsCommand struct {
	SessionID          string                  `json:"session_id"`
	OrganizationID     string                  `json:"organization_id"`
	ConfigVersionID    string                  `json:"config_version_id"`
	PublicationRef     KnowledgePublicationRef `json:"publication_ref"`
	Scope              ApplicableScope         `json:"scope"`
	TranscriptVersions TranscriptVersionSet    `json:"transcript_versions"`
	Basis              ReferenceBasis          `json:"basis"`
}

// ReferenceBasis binds reference generation to either a module-five set or a working record projection.
type ReferenceBasis struct {
	Kind                   ReferenceBasisKind `json:"kind"`
	MatterSuggestionSetRef *EntityRef         `json:"matter_suggestion_set_ref,omitempty"`
	RecordDraftRef         *EntityRef         `json:"record_draft_ref,omitempty"`
	MatterCode             string             `json:"matter_code"`
	MatterName             string             `json:"matter_name"`
	Facts                  []BasisFact        `json:"facts"`
	SourceRefs             []SourceRef        `json:"source_refs"`
	BasisDigest            string             `json:"basis_digest"`
}

// EntityRef references an immutable upstream entity version.
type EntityRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// BasisFact is a compact matter fact projection used as reference-generation input.
type BasisFact struct {
	FieldCode  string      `json:"field_code"`
	FieldName  string      `json:"field_name"`
	Value      FactValue   `json:"value"`
	Required   bool        `json:"required"`
	SourceRefs []SourceRef `json:"source_refs"`
}

// FactValue carries both display text and an optional normalized representation.
type FactValue struct {
	ValueType   string `json:"value_type"`
	DisplayText string `json:"display_text"`
	Normalized  string `json:"normalized,omitempty"`
}

// ReferenceSuggestionResult returns the typed outcome of reference generation.
type ReferenceSuggestionResult struct {
	OperationID   string                  `json:"operation_id"`
	AttemptID     string                  `json:"attempt_id,omitempty"`
	Outcome       SuggestionOutcome       `json:"outcome"`
	SuggestionSet *ReferenceSuggestionSet `json:"suggestion_set,omitempty"`
	ErrorCode     string                  `json:"error_code,omitempty"`
	Retryable     bool                    `json:"retryable"`
}

// ReferenceSuggestionSet is the immutable policy/material/path/FAQ suggestion package.
type ReferenceSuggestionSet struct {
	SuggestionSetID    string                   `json:"suggestion_set_id"`
	OperationID        string                   `json:"operation_id"`
	Version            string                   `json:"version"`
	SessionID          string                   `json:"session_id"`
	OrganizationID     string                   `json:"organization_id"`
	ConfigVersionID    string                   `json:"config_version_id"`
	Scope              ApplicableScope          `json:"scope"`
	PublicationRef     KnowledgePublicationRef  `json:"publication_ref"`
	TranscriptVersions TranscriptVersionSet     `json:"transcript_versions"`
	Basis              ReferenceBasis           `json:"basis"`
	QueryDigest        string                   `json:"query_digest"`
	Policies           []PolicySuggestion       `json:"policies"`
	Materials          []MaterialSuggestion     `json:"materials"`
	HandlingPaths      []HandlingPathSuggestion `json:"handling_paths"`
	FAQs               []FAQSuggestion          `json:"faqs"`
	Generation         ReferenceGenerationInfo  `json:"generation"`
	CreatedAt          time.Time                `json:"created_at"`
}

// ReferenceSuggestionSetView adds freshness projection without mutating the immutable set.
type ReferenceSuggestionSetView struct {
	SuggestionSet ReferenceSuggestionSet `json:"suggestion_set"`
	Freshness     SuggestionFreshness    `json:"freshness"`
	StateVersion  string                 `json:"state_version"`
	Reason        string                 `json:"reason,omitempty"`
}

// PolicySuggestion points to a policy evidence item and optional local policy code.
type PolicySuggestion struct {
	SuggestionID string            `json:"suggestion_id"`
	Evidence     ReferenceEvidence `json:"evidence"`
	PolicyCode   string            `json:"policy_code,omitempty"`
}

// MaterialSuggestion points to a material evidence item and preserves requiredness as a hint.
type MaterialSuggestion struct {
	SuggestionID string               `json:"suggestion_id"`
	Evidence     ReferenceEvidence    `json:"evidence"`
	MaterialCode string               `json:"material_code,omitempty"`
	RequiredHint MaterialRequiredHint `json:"required_hint"`
}

// HandlingPathSuggestion describes a candidate handling path backed by published knowledge.
type HandlingPathSuggestion struct {
	SuggestionID string            `json:"suggestion_id"`
	Evidence     ReferenceEvidence `json:"evidence"`
	Steps        []string          `json:"steps"`
}

// FAQSuggestion describes a candidate FAQ answer backed by published knowledge.
type FAQSuggestion struct {
	SuggestionID string            `json:"suggestion_id"`
	Evidence     ReferenceEvidence `json:"evidence"`
	Question     string            `json:"question"`
	Answer       string            `json:"answer"`
}

// ReferenceEvidence binds a suggestion back to a published knowledge entry version.
type ReferenceEvidence struct {
	ReferenceID             string           `json:"reference_id"`
	KnowledgeEntryVersionID string           `json:"knowledge_entry_version_id"`
	Title                   string           `json:"title"`
	Summary                 string           `json:"summary"`
	QuotedText              string           `json:"quoted_text,omitempty"`
	Sources                 []CitationSource `json:"sources"`
	Scope                   ApplicableScope  `json:"scope"`
}

// CitationSource preserves published source metadata for later record-level citation snapshots.
type CitationSource struct {
	SourceID         string     `json:"source_id"`
	Title            string     `json:"title"`
	Issuer           string     `json:"issuer"`
	SourceURL        string     `json:"source_url,omitempty"`
	SourceDocumentID string     `json:"source_document_id,omitempty"`
	SourceLocator    string     `json:"source_locator"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	EffectiveFrom    *time.Time `json:"effective_from,omitempty"`
	EffectiveUntil   *time.Time `json:"effective_until,omitempty"`
	ContentHash      string     `json:"content_hash"`
	ReviewStatus     string     `json:"review_status"`
}

// ReferenceGenerationInfo records provider metadata without storing full prompts or raw responses.
type ReferenceGenerationInfo struct {
	ProviderKind     string `json:"provider_kind"`
	ModelVersion     string `json:"model_version,omitempty"`
	ProviderTraceRef string `json:"provider_trace_ref,omitempty"`
	Degraded         bool   `json:"degraded"`
}
