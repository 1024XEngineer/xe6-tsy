package matter

import (
	"context"
	"time"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/modules/shared"
)

type SessionID = shared.SessionID
type OrganizationID = shared.OrganizationID
type ConfigVersionID = shared.ConfigVersionID
type StaffID = shared.StaffID
type EntityRef = shared.EntityRef
type TranscriptVersionRef = shared.TranscriptVersionRef
type TranscriptVersionSet = shared.TranscriptVersionSet
type TextSpan = shared.TextSpan
type SourceRef = shared.SourceRef
type KnowledgePublicationRef = shared.KnowledgePublicationRef
type ApplicableScope = shared.ApplicableScope
type CommandMeta = shared.CommandMeta
type SuggestionOutcome = shared.SuggestionOutcome
type SuggestionFreshness = shared.SuggestionFreshness
type GenerationInfo = shared.GenerationInfo

const (
	SuggestionGenerated      = shared.SuggestionGenerated
	SuggestionNoCandidate    = shared.SuggestionNoCandidate
	SuggestionProviderFailed = shared.SuggestionProviderFailed
	SuggestionCurrent        = shared.SuggestionCurrent
	SuggestionStale          = shared.SuggestionStale
	SuggestionExpired        = shared.SuggestionExpired
)

// MatterSuggestionGenerator is the application boundary used by handlers and
// workflow orchestration to request a new raw matter suggestion package. The
// implementation owns validation, provider invocation, and operation recovery;
// callers must not interpret provider failures as transport errors.
type MatterSuggestionGenerator interface {
	GenerateMatterSuggestions(ctx context.Context, cmd GenerateMatterSuggestionsCommand) (MatterSuggestionResult, error)
}

// MatterSuggestionReader exposes the authoritative query path for module 3 and
// module 7. Reads return a freshness projection alongside the immutable payload
// so downstream modules can recover after missed events without mutating the
// original suggestion set.
type MatterSuggestionReader interface {
	MatterSuggestionSetByID(ctx context.Context, setID string) (MatterSuggestionSetView, error)
}

type EvidenceKind string

const (
	EvidenceFinal  EvidenceKind = "final"
	EvidenceManual EvidenceKind = "manual"
)

// EvidenceBlock is the transcript evidence accepted by module 5. It is limited
// to final or manual transcript content so partial ASR output cannot trigger
// durable suggestions or later be mistaken for reviewable evidence.
type EvidenceBlock struct {
	BlockID      string       `json:"block_id"`
	SpeakerLabel string       `json:"speaker_label"`
	Kind         EvidenceKind `json:"kind"`
	Text         string       `json:"text"`
	Confidence   *float64     `json:"confidence,omitempty"`
	SourceRefs   []SourceRef  `json:"source_refs"`
}

// AnalysisSupportItem is optional knowledge context supplied by module 6. It
// may improve matter understanding, but it must never become the source of a
// citizen fact because those facts have to trace back to transcript evidence.
type AnalysisSupportItem struct {
	KnowledgeEntryVersionID string          `json:"knowledge_entry_version_id"`
	EntryType               string          `json:"entry_type"`
	Excerpt                 string          `json:"excerpt"`
	SourceIDs               []string        `json:"source_ids"`
	Scope                   ApplicableScope `json:"applicable_scope"`
}

// AnalysisContextInput is the module 6 pre-retrieval snapshot consumed by module
// 5. Services must verify that session, organization, config, scope, transcript
// digest, purpose, and expiry still match before using it; mismatches should
// degrade to transcript-only generation rather than block the user path.
type AnalysisContextInput struct {
	ContextID          string                  `json:"context_id"`
	Purpose            string                  `json:"purpose"`
	SessionID          SessionID               `json:"session_id"`
	OrganizationID     OrganizationID          `json:"organization_id"`
	ConfigVersionID    ConfigVersionID         `json:"config_version_id"`
	Scope              ApplicableScope         `json:"scope"`
	PublicationRef     KnowledgePublicationRef `json:"publication_ref"`
	TranscriptVersions TranscriptVersionSet    `json:"transcript_versions"`
	Items              []AnalysisSupportItem   `json:"items"`
	ExpiresAt          time.Time               `json:"expires_at"`
}

// MatterTaxonomyRef pins suggestion generation to the matter taxonomy version
// bound to the session. This prevents retry recovery from mixing suggestions
// generated under different classification rules.
type MatterTaxonomyRef struct {
	TaxonomyID string `json:"taxonomy_id"`
	Version    int64  `json:"version"`
}

// GenerateMatterSuggestionsCommand is the complete input snapshot for a module
// 5 generation attempt. It intentionally carries all upstream versions needed
// for idempotency, stale detection, and audit reconstruction.
type GenerateMatterSuggestionsCommand struct {
	Meta               CommandMeta           `json:"meta"`
	SessionID          SessionID             `json:"session_id"`
	OrganizationID     OrganizationID        `json:"organization_id"`
	ConfigVersionID    ConfigVersionID       `json:"config_version_id"`
	Scope              ApplicableScope       `json:"scope"`
	TaxonomyRef        MatterTaxonomyRef     `json:"taxonomy_ref"`
	TranscriptVersions TranscriptVersionSet  `json:"transcript_versions"`
	EvidenceBlocks     []EvidenceBlock       `json:"evidence_blocks"`
	AnalysisContext    *AnalysisContextInput `json:"analysis_context,omitempty"`
}

// MatterSuggestion is a provider-produced candidate matter, not an
// administrative decision. Module 7 may adopt, edit, or discard it, but those
// review states belong to module 7 and must not be written back here.
type MatterSuggestion struct {
	SuggestionID string      `json:"suggestion_id"`
	MatterCode   string      `json:"matter_code"`
	MatterName   string      `json:"matter_name"`
	Confidence   float64     `json:"confidence"`
	Rationale    string      `json:"rationale"`
	SourceRefs   []SourceRef `json:"source_refs"`
}

// FactValue stores the suggested value in both display and optional normalized
// form. Normalization is advisory at this layer; final record validation remains
// a module 7 responsibility.
type FactValue struct {
	ValueType   string  `json:"value_type"`
	DisplayText string  `json:"display_text"`
	Normalized  *string `json:"normalized,omitempty"`
}

// FactSuggestion proposes a field value that is backed by transcript evidence.
// The required flag is copied from taxonomy/config context so staff can see why
// a missing value matters without module 5 owning the final record state.
type FactSuggestion struct {
	SuggestionID string      `json:"suggestion_id"`
	FieldCode    string      `json:"field_code"`
	FieldName    string      `json:"field_name"`
	Value        FactValue   `json:"value"`
	Confidence   float64     `json:"confidence"`
	Required     bool        `json:"required"`
	SourceRefs   []SourceRef `json:"source_refs"`
}

// FollowUpSuggestion proposes a staff question to fill missing or low-confidence
// facts. It is advisory and has no lifecycle state until module 7 records how
// staff handled it.
type FollowUpSuggestion struct {
	SuggestionID     string   `json:"suggestion_id"`
	Question         string   `json:"question"`
	TargetFieldCodes []string `json:"target_field_codes"`
	Priority         int      `json:"priority"`
}

// MatterSuggestionSet is the immutable raw payload created by module 5. Stores
// may add freshness projections beside it, but they must not update the body
// when transcripts, config, or taxonomy versions change.
type MatterSuggestionSet struct {
	SuggestionSetID    string               `json:"suggestion_set_id"`
	OperationID        string               `json:"operation_id"`
	Version            int64                `json:"version"`
	SessionID          SessionID            `json:"session_id"`
	OrganizationID     OrganizationID       `json:"organization_id"`
	ConfigVersionID    ConfigVersionID      `json:"config_version_id"`
	Scope              ApplicableScope      `json:"scope"`
	TaxonomyRef        MatterTaxonomyRef    `json:"taxonomy_ref"`
	TranscriptVersions TranscriptVersionSet `json:"transcript_versions"`
	AnalysisContextID  *string              `json:"analysis_context_id,omitempty"`
	Summary            string               `json:"summary"`
	Matters            []MatterSuggestion   `json:"matters"`
	Facts              []FactSuggestion     `json:"facts"`
	MissingFieldCodes  []string             `json:"missing_field_codes"`
	FollowUps          []FollowUpSuggestion `json:"follow_ups"`
	Generation         GenerationInfo       `json:"generation"`
	CreatedAt          time.Time            `json:"created_at"`
}

// MatterSuggestionResult is the typed outcome of a generation attempt. No
// candidate and provider failure are successful business outcomes for recovery
// purposes; transport errors are reserved for invalid input, authorization, or
// infrastructure failures outside the provider result.
type MatterSuggestionResult struct {
	AttemptID string               `json:"attempt_id"`
	Outcome   SuggestionOutcome    `json:"outcome"`
	Set       *MatterSuggestionSet `json:"suggestion_set,omitempty"`
	ErrorCode *string              `json:"error_code,omitempty"`
	Retryable bool                 `json:"retryable"`
}

// MatterSuggestionSetView combines the immutable suggestion body with the latest
// query-time state projection. This shape lets GET recovery and missed WSS/SSE
// events converge on the same authority without rewriting suggestion content.
type MatterSuggestionSetView struct {
	Set          MatterSuggestionSet `json:"suggestion_set"`
	Freshness    SuggestionFreshness `json:"freshness"`
	StateVersion int64               `json:"state_version"`
	Reason       *string             `json:"reason,omitempty"`
}

// CandidateGenerator is the provider port consumed by module 5. Implementations
// may call a fake, model, or replay source, but they must return typed outcomes
// and must not perform module 7 review or persistence work.
type CandidateGenerator interface {
	Generate(ctx context.Context, input CandidateGenerationInput) (CandidateGenerationOutput, error)
}

// CandidateGenerationInput is the provider-facing subset of the command. It
// deliberately omits staff identity and storage metadata so provider adapters
// cannot make authorization or persistence decisions.
type CandidateGenerationInput struct {
	TaxonomyRef     MatterTaxonomyRef     `json:"taxonomy_ref"`
	EvidenceBlocks  []EvidenceBlock       `json:"evidence_blocks"`
	AnalysisSupport []AnalysisSupportItem `json:"analysis_support,omitempty"`
}

// CandidateGenerationOutput is the provider-neutral response that the service
// maps into a durable suggestion result. Provider trace data remains optional so
// fake and offline tests can satisfy the same contract.
type CandidateGenerationOutput struct {
	Outcome           SuggestionOutcome    `json:"outcome"`
	Summary           string               `json:"summary"`
	Matters           []MatterSuggestion   `json:"matters"`
	Facts             []FactSuggestion     `json:"facts"`
	MissingFieldCodes []string             `json:"missing_field_codes"`
	FollowUps         []FollowUpSuggestion `json:"follow_ups"`
	ModelVersion      *string              `json:"model_version,omitempty"`
	ProviderTraceRef  *string              `json:"provider_trace_ref,omitempty"`
}

// CurrentTranscriptReader is the read-only module 4 port used for freshness
// checks. Query paths should compare current digest values with stored snapshots
// before declaring a suggestion set usable.
type CurrentTranscriptReader interface {
	CurrentTranscriptVersions(ctx context.Context, sessionID SessionID) (TranscriptVersionSet, error)
}

// MatterSuggestionStore is the persistence boundary for module 5 results and
// freshness projections. Implementations must make SaveResultForOperation
// idempotent by operation ID and mark stale state separately from the immutable
// suggestion body.
type MatterSuggestionStore interface {
	SaveResultForOperation(ctx context.Context, operationID string, result MatterSuggestionResult) error
	ViewByID(ctx context.Context, suggestionSetID string) (MatterSuggestionSetView, error)
	MarkStaleByTranscript(ctx context.Context, sessionID SessionID, previousDigest, currentDigest string) error
}
