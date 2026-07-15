package reference

import (
	"context"
	"time"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/modules/matter"
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

// AnalysisContextRetriever is the application boundary for pre-retrieval used
// before matter generation. A failed or empty result is a typed business outcome
// so workflow code can continue with transcript-only matter analysis.
type AnalysisContextRetriever interface {
	RetrieveAnalysisContext(ctx context.Context, cmd RetrieveAnalysisContextCommand) (AnalysisContextResult, error)
}

// ReferenceSuggestionGenerator is the application boundary for producing raw
// policy, material, path, and FAQ suggestions. Adoption and citation snapshots
// remain module 7 responsibilities and must not be implemented here.
type ReferenceSuggestionGenerator interface {
	GenerateReferenceSuggestions(ctx context.Context, cmd GenerateReferenceSuggestionsCommand) (ReferenceSuggestionResult, error)
}

// ReferenceSuggestionReader exposes the recovery query for module 3 and module
// 7. The returned view includes freshness so clients can recover after missed
// events without trusting stale reference content as current.
type ReferenceSuggestionReader interface {
	ReferenceSuggestionSetByID(ctx context.Context, setID string) (ReferenceSuggestionSetView, error)
}

// RetrieveAnalysisContextCommand binds pre-retrieval to one session, scope,
// publication, and transcript snapshot. Services should reject or type-result
// mismatches before calling retrievers so providers cannot broaden the search
// beyond authorized context.
type RetrieveAnalysisContextCommand struct {
	Meta               CommandMeta             `json:"meta"`
	SessionID          SessionID               `json:"session_id"`
	OrganizationID     OrganizationID          `json:"organization_id"`
	ConfigVersionID    ConfigVersionID         `json:"config_version_id"`
	PublicationRef     KnowledgePublicationRef `json:"publication_ref"`
	Scope              ApplicableScope         `json:"scope"`
	TranscriptVersions TranscriptVersionSet    `json:"transcript_versions"`
	MinimizedQueryText string                  `json:"minimized_query_text"`
}

type AnalysisContextOutcome string

const (
	ContextPrepared          AnalysisContextOutcome = "prepared"
	ContextNoMatch           AnalysisContextOutcome = "no_match"
	ContextProviderFailed    AnalysisContextOutcome = "provider_failed"
	ContextBundleUnavailable AnalysisContextOutcome = "bundle_unavailable"
	ContextScopeMismatch     AnalysisContextOutcome = "scope_mismatch"
)

// RetrievedAnalysisItem is a compact knowledge excerpt returned for matter
// analysis support. It is not a formal citation and should not be copied into a
// finalized record without module 7 citation snapshot handling.
type RetrievedAnalysisItem struct {
	KnowledgeEntryVersionID string          `json:"knowledge_entry_version_id"`
	EntryType               string          `json:"entry_type"`
	Excerpt                 string          `json:"excerpt"`
	SourceIDs               []string        `json:"source_ids"`
	Scope                   ApplicableScope `json:"applicable_scope"`
}

// AnalysisRetrievalContext is the stored pre-retrieval snapshot. It can expire
// independently from the transcript, and stale/expired state must be projected
// outside the immutable context body.
type AnalysisRetrievalContext struct {
	ContextID          string                  `json:"context_id"`
	OperationID        string                  `json:"operation_id"`
	Purpose            string                  `json:"purpose"`
	SessionID          SessionID               `json:"session_id"`
	OrganizationID     OrganizationID          `json:"organization_id"`
	ConfigVersionID    ConfigVersionID         `json:"config_version_id"`
	Scope              ApplicableScope         `json:"scope"`
	PublicationRef     KnowledgePublicationRef `json:"publication_ref"`
	TranscriptVersions TranscriptVersionSet    `json:"transcript_versions"`
	Items              []RetrievedAnalysisItem `json:"items"`
	CreatedAt          time.Time               `json:"created_at"`
	ExpiresAt          time.Time               `json:"expires_at"`
}

// AnalysisContextResult represents the typed result of pre-retrieval. Only the
// prepared outcome should carry a context; no-match, scope mismatch, bundle
// unavailable, and provider failure remain recoverable outcomes for workflow.
type AnalysisContextResult struct {
	Outcome   AnalysisContextOutcome    `json:"outcome"`
	Context   *AnalysisRetrievalContext `json:"context,omitempty"`
	ErrorCode *string                   `json:"error_code,omitempty"`
	Retryable bool                      `json:"retryable"`
}

type ReferenceBasisKind string

const (
	BasisMatterSuggestionSet ReferenceBasisKind = "matter_suggestion_set"
	BasisRecordWorkingMatter ReferenceBasisKind = "record_working_matter"
)

// FactForReference is the matter/fact projection used to build a reference
// query. It may originate from module 5 suggestions or module 7 working drafts,
// but module 6 treats it only as an input snapshot.
type FactForReference struct {
	FieldCode   string  `json:"field_code"`
	FieldName   string  `json:"field_name"`
	DisplayText string  `json:"display_text"`
	Normalized  *string `json:"normalized,omitempty"`
}

// ReferenceBasis records why a reference suggestion was generated. The digest
// is the idempotency and stale-detection key for retries and later draft edits.
type ReferenceBasis struct {
	Kind                   ReferenceBasisKind `json:"kind"`
	MatterSuggestionSetRef *EntityRef         `json:"matter_suggestion_set_ref,omitempty"`
	RecordDraftRef         *EntityRef         `json:"record_draft_ref,omitempty"`
	MatterCode             string             `json:"matter_code"`
	MatterName             string             `json:"matter_name"`
	Facts                  []FactForReference `json:"facts"`
	SourceRefs             []SourceRef        `json:"source_refs"`
	BasisDigest            string             `json:"basis_digest"`
}

// GenerateReferenceSuggestionsCommand is the complete snapshot required to
// generate module 6 suggestions. It pins the knowledge publication and basis so
// the service never reads "latest" knowledge during retry or offline recovery.
type GenerateReferenceSuggestionsCommand struct {
	Meta               CommandMeta             `json:"meta"`
	SessionID          SessionID               `json:"session_id"`
	OrganizationID     OrganizationID          `json:"organization_id"`
	ConfigVersionID    ConfigVersionID         `json:"config_version_id"`
	PublicationRef     KnowledgePublicationRef `json:"publication_ref"`
	Scope              ApplicableScope         `json:"scope"`
	TranscriptVersions TranscriptVersionSet    `json:"transcript_versions"`
	Basis              ReferenceBasis          `json:"basis"`
}

// CitationSource captures source metadata that can later be copied into a
// module 7 record-level snapshot. It is read-only evidence here, not proof that
// staff adopted the citation.
type CitationSource struct {
	SourceID         string     `json:"source_id"`
	Title            string     `json:"title"`
	Issuer           string     `json:"issuer"`
	SourceURL        *string    `json:"source_url,omitempty"`
	SourceDocumentID *string    `json:"source_document_id,omitempty"`
	SourceLocator    string     `json:"source_locator"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	EffectiveFrom    *time.Time `json:"effective_from,omitempty"`
	EffectiveUntil   *time.Time `json:"effective_until,omitempty"`
	ContentHash      string     `json:"content_hash"`
	ReviewStatus     string     `json:"review_status"`
}

// ReferenceEvidence is the normalized evidence block shared by each reference
// suggestion type. It ties provider hits back to a published knowledge entry so
// generated suggestions can be audited without retaining provider-specific
// search responses.
type ReferenceEvidence struct {
	ReferenceID             string           `json:"reference_id"`
	KnowledgeEntryVersionID string           `json:"knowledge_entry_version_id"`
	Title                   string           `json:"title"`
	Summary                 string           `json:"summary"`
	QuotedText              *string          `json:"quoted_text,omitempty"`
	Sources                 []CitationSource `json:"sources"`
	Scope                   ApplicableScope  `json:"applicable_scope"`
}

// PolicySuggestion recommends a policy reference for the current basis. It does
// not grant authority or finalize a policy citation; staff review happens in
// module 7.
type PolicySuggestion struct {
	Evidence   ReferenceEvidence `json:"evidence"`
	PolicyCode *string           `json:"policy_code,omitempty"`
}

// MaterialSuggestion recommends a required, conditional, optional, or unknown
// material hint. The hint remains advisory until module 7 records staff action.
type MaterialSuggestion struct {
	Evidence     ReferenceEvidence `json:"evidence"`
	MaterialCode *string           `json:"material_code,omitempty"`
	RequiredHint string            `json:"required_hint"`
}

// HandlingPathSuggestion recommends procedural steps from published knowledge.
// It should be copied into records only through module 7 adoption and snapshot
// logic.
type HandlingPathSuggestion struct {
	Evidence ReferenceEvidence `json:"evidence"`
	Steps    []string          `json:"steps"`
}

// FAQSuggestion recommends a reusable question-and-answer reference. It remains
// separate from the staff conversation transcript and final consultation record.
type FAQSuggestion struct {
	Evidence ReferenceEvidence `json:"evidence"`
	Question string            `json:"question"`
	Answer   string            `json:"answer"`
}

// ReferenceSuggestionSet is the immutable raw module 6 suggestion package.
// Stores may mark it stale when transcript, basis, or publication context
// changes, but they must not rewrite the body after creation.
type ReferenceSuggestionSet struct {
	SuggestionSetID    string                   `json:"suggestion_set_id"`
	OperationID        string                   `json:"operation_id"`
	Version            int64                    `json:"version"`
	SessionID          SessionID                `json:"session_id"`
	OrganizationID     OrganizationID           `json:"organization_id"`
	ConfigVersionID    ConfigVersionID          `json:"config_version_id"`
	Scope              ApplicableScope          `json:"scope"`
	PublicationRef     KnowledgePublicationRef  `json:"publication_ref"`
	TranscriptVersions TranscriptVersionSet     `json:"transcript_versions"`
	Basis              ReferenceBasis           `json:"basis"`
	QueryDigest        string                   `json:"query_digest"`
	Policies           []PolicySuggestion       `json:"policies"`
	Materials          []MaterialSuggestion     `json:"materials"`
	HandlingPaths      []HandlingPathSuggestion `json:"handling_paths"`
	FAQs               []FAQSuggestion          `json:"faqs"`
	Generation         GenerationInfo           `json:"generation"`
	CreatedAt          time.Time                `json:"created_at"`
}

// ReferenceSuggestionResult is the typed outcome of a reference generation
// attempt. No candidate, provider failure, bundle unavailable, and scope
// mismatch are business outcomes that can be stored and replayed during recovery.
type ReferenceSuggestionResult struct {
	AttemptID string                  `json:"attempt_id"`
	Outcome   SuggestionOutcome       `json:"outcome"`
	Set       *ReferenceSuggestionSet `json:"suggestion_set,omitempty"`
	ErrorCode *string                 `json:"error_code,omitempty"`
	Retryable bool                    `json:"retryable"`
}

// ReferenceSuggestionSetView combines the immutable reference payload with its
// query-time freshness projection. This lets module 3 and module 7 treat GET as
// the authority after WSS/SSE events are missed.
type ReferenceSuggestionSetView struct {
	Set          ReferenceSuggestionSet `json:"suggestion_set"`
	Freshness    SuggestionFreshness    `json:"freshness"`
	StateVersion int64                  `json:"state_version"`
	Reason       *string                `json:"reason,omitempty"`
}

// PublishedKnowledgeReader is the read-only module 2 boundary consumed by module
// 6. It must return the exact publication ref requested and must not create,
// update, review, publish, or retire knowledge.
type PublishedKnowledgeReader interface {
	PublishedBundle(ctx context.Context, ref KnowledgePublicationRef) (PublishedKnowledgeBundle, error)
}

// PublishedKnowledgeEntry is the verified knowledge item module 6 may cite. It
// is copied from the pinned publication bundle to prevent retriever hits from
// becoming formal references without validation.
type PublishedKnowledgeEntry struct {
	KnowledgeEntryID        string           `json:"knowledge_entry_id"`
	KnowledgeEntryVersionID string           `json:"knowledge_entry_version_id"`
	EntryType               string           `json:"entry_type"`
	MatterCodes             []string         `json:"matter_codes"`
	Title                   string           `json:"title"`
	Content                 string           `json:"content"`
	ContentHash             string           `json:"content_hash"`
	Scope                   ApplicableScope  `json:"applicable_scope"`
	Sources                 []CitationSource `json:"sources"`
}

// PublishedKnowledgeBundle is the pinned knowledge release visible to one
// organization and scope. The service should treat status or scope mismatches as
// typed reference outcomes before provider search is attempted.
type PublishedKnowledgeBundle struct {
	OrganizationID OrganizationID            `json:"organization_id"`
	PublicationRef KnowledgePublicationRef   `json:"publication_ref"`
	Status         PublicationStatus         `json:"status"`
	Scope          ApplicableScope           `json:"applicable_scope"`
	Entries        []PublishedKnowledgeEntry `json:"entries"`
}

type PublicationStatus string

const (
	PublicationPublished PublicationStatus = "published"
)

// MatterSuggestionSetReader is the read-only module 5 dependency used when a
// reference basis points at a matter suggestion set. Module 6 must verify the
// source set is current before generating references from it.
type MatterSuggestionSetReader interface {
	MatterSuggestionSetByID(ctx context.Context, setID string) (matter.MatterSuggestionSetView, error)
}

// CurrentTranscriptVersionReader is the read-only module 4 dependency for
// freshness checks. It prevents old reference sets from remaining current after
// transcript correction.
type CurrentTranscriptVersionReader interface {
	CurrentTranscriptVersions(ctx context.Context, sessionID SessionID) (TranscriptVersionSet, error)
}

type RetrievalPurpose string

const (
	RetrievalForAnalysis   RetrievalPurpose = "matter_analysis"
	RetrievalForSuggestion RetrievalPurpose = "reference_suggestion"
)

// Retriever is the RAG/search provider port consumed by module 6. It receives
// minimized query text and pinned publication context, and it must not persist
// formal knowledge or record-level citations.
type Retriever interface {
	Search(ctx context.Context, query RetrievalQuery) ([]RetrievalHit, error)
}

// RetrievalQuery is the provider-facing search request. It excludes staff audit
// metadata and storage IDs so provider adapters stay replaceable and cannot own
// authorization or persistence decisions.
type RetrievalQuery struct {
	Purpose        RetrievalPurpose        `json:"purpose"`
	PublicationRef KnowledgePublicationRef `json:"publication_ref"`
	Scope          ApplicableScope         `json:"scope"`
	QueryText      string                  `json:"query_text"`
	Basis          *ReferenceBasis         `json:"basis,omitempty"`
	Limit          int                     `json:"limit"`
}

// RetrievalHit is an untrusted provider hit that must be reconciled against the
// pinned PublishedKnowledgeBundle before becoming ReferenceEvidence.
type RetrievalHit struct {
	KnowledgeEntryVersionID string  `json:"knowledge_entry_version_id"`
	ChunkRef                string  `json:"chunk_ref"`
	QuotedText              string  `json:"quoted_text"`
	Score                   float64 `json:"score"`
}

// AnalysisContextStore persists pre-retrieval outcomes and their freshness
// projection. SaveForOperation should be idempotent by operation ID so worker
// retries recover the original context instead of duplicating it.
type AnalysisContextStore interface {
	SaveForOperation(ctx context.Context, operationID string, result AnalysisContextResult) error
	ViewByID(ctx context.Context, contextID string) (AnalysisContextResult, error)
	MarkStaleByTranscript(ctx context.Context, sessionID SessionID, previousDigest, currentDigest string) error
}

// ReferenceSuggestionStore persists reference generation outcomes and separate
// stale projections. It must support invalidation by transcript and by basis
// digest without mutating the immutable suggestion set body.
type ReferenceSuggestionStore interface {
	SaveResultForOperation(ctx context.Context, operationID string, result ReferenceSuggestionResult) error
	ViewByID(ctx context.Context, suggestionSetID string) (ReferenceSuggestionSetView, error)
	MarkStaleByTranscript(ctx context.Context, sessionID SessionID, previousDigest, currentDigest string) error
	MarkStaleByBasis(ctx context.Context, basisDigest string, reason string) error
}
