package shared

import (
	"encoding/json"
	"time"
)

type SessionID string
type OrganizationID string
type ConfigVersionID string
type StaffID string

// EntityRef identifies an immutable or versioned domain object across module
// boundaries. Callers must carry the version they observed so later adoption or
// recovery code can detect stale upstream data instead of silently reading the
// latest object.
type EntityRef struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

// TranscriptVersionRef points at one finalized or manually corrected transcript
// segment. Module 5 and module 6 use this instead of partial transcript IDs so
// generated suggestions can be invalidated deterministically when module 4
// publishes a newer version.
type TranscriptVersionRef struct {
	SegmentID string `json:"segment_id"`
	Version   int64  `json:"version"`
}

// TranscriptVersionSet is the upstream transcript snapshot bound to a command
// or suggestion set. The digest is the comparison key for retry recovery and
// stale projection; implementations should not rewrite generated payloads when
// this set changes.
type TranscriptVersionSet struct {
	Watermark string                 `json:"watermark"`
	Digest    string                 `json:"digest"`
	Items     []TranscriptVersionRef `json:"items"`
}

// TextSpan records the source text interval used as evidence. It is carried
// through suggestion packages so module 7 can audit where a recommendation came
// from without asking model or RAG providers to reconstruct their reasoning.
type TextSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// SourceRef binds a suggestion fragment back to a final or manual transcript
// version. It is intentionally provider-neutral; adapters may retain richer
// traces internally, but transport contracts must not expose SDK-specific
// evidence objects.
type SourceRef struct {
	Kind     string   `json:"kind"`
	EntityID string   `json:"entity_id"`
	Version  int64    `json:"version"`
	TextSpan TextSpan `json:"text_span"`
}

// KnowledgePublicationRef pins reads to one published knowledge bundle. All
// three fields must be validated together so retries and offline recovery do not
// accidentally mix an older command with a newer publication under the same ID.
type KnowledgePublicationRef struct {
	PublicationID      string `json:"publication_id"`
	PublicationVersion int64  `json:"publication_version"`
	ManifestHash       string `json:"manifest_hash"`
}

// ApplicableScope limits configuration, transcript analysis, and knowledge
// retrieval to the organization context that authorized the session. Stores and
// provider ports should treat mismatches as typed business failures rather than
// broadening the search silently.
type ApplicableScope struct {
	OrganizationID  OrganizationID `json:"organization_id"`
	RegionCodes     []string       `json:"region_codes,omitempty"`
	ServicePointIDs []string       `json:"service_point_ids,omitempty"`
	WindowIDs       []string       `json:"window_ids,omitempty"`
}

// CommandMeta carries the audit and idempotency keys supplied by the service
// boundary. Worker retries must reuse the same idempotency key so stores can
// recover the existing operation result instead of creating duplicate suggestion
// sets.
type CommandMeta struct {
	CommandID      string    `json:"command_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	ActorID        StaffID   `json:"actor_id"`
	RequestedAt    time.Time `json:"requested_at"`
}

type SuggestionOutcome string

const (
	SuggestionGenerated      SuggestionOutcome = "generated"
	SuggestionNoCandidate    SuggestionOutcome = "no_candidate"
	SuggestionProviderFailed SuggestionOutcome = "provider_failed"
)

type SuggestionFreshness string

const (
	SuggestionCurrent SuggestionFreshness = "current"
	SuggestionStale   SuggestionFreshness = "stale"
	SuggestionExpired SuggestionFreshness = "expired"
)

// GenerationInfo captures provider-level provenance without making model or RAG
// SDKs part of the module contract. Degraded results remain valid typed results;
// module 7 decides whether staff must review or retry before finalizing a record.
type GenerationInfo struct {
	ProviderKind        string  `json:"provider_kind"`
	ModelVersion        *string `json:"model_version,omitempty"`
	ProviderTraceRef    *string `json:"provider_trace_ref,omitempty"`
	UsedAnalysisContext bool    `json:"used_analysis_context"`
	Degraded            bool    `json:"degraded"`
}

type OperationStatus string
type OperationKind string
type OperationResultType string

const (
	OperationAccepted  OperationStatus = "accepted"
	OperationRunning   OperationStatus = "running"
	OperationSucceeded OperationStatus = "succeeded"
	OperationFailed    OperationStatus = "failed"

	OperationMatterSuggestion    OperationKind = "matter_suggestion"
	OperationAnalysisContext     OperationKind = "analysis_context"
	OperationReferenceSuggestion OperationKind = "reference_suggestion"

	ResultMatterSuggestion    OperationResultType = "matter_suggestion_result.v1"
	ResultAnalysisContext     OperationResultType = "analysis_context_result.v1"
	ResultReferenceSuggestion OperationResultType = "reference_suggestion_result.v1"
)

// OperationView is the durable envelope used to resume async commands after
// network loss or worker retry. The raw result fields are intentionally opaque
// here because each operation kind owns its typed payload and error semantics.
type OperationView struct {
	OperationID    string               `json:"operation_id"`
	Kind           OperationKind        `json:"kind"`
	SessionID      SessionID            `json:"session_id"`
	OrganizationID OrganizationID       `json:"organization_id"`
	Status         OperationStatus      `json:"status"`
	ResultType     *OperationResultType `json:"result_type,omitempty"`
	Result         json.RawMessage      `json:"result,omitempty"`
	Error          json.RawMessage      `json:"error,omitempty"`
	UpdatedAt      time.Time            `json:"updated_at"`
}

// ErrorResponse is the transport-safe error shape returned by handlers. It must
// avoid stack traces, SQL, provider responses, and sensitive text while still
// carrying stable codes that frontends and audit logs can correlate.
type ErrorResponse struct {
	Code          string            `json:"code"`
	Message       string            `json:"message"`
	Retryable     bool              `json:"retryable"`
	CorrelationID string            `json:"correlation_id"`
	Details       map[string]string `json:"details,omitempty"`
}
