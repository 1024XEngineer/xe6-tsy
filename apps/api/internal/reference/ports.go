package reference

// PublishedKnowledgeBundle is a minimal read model for an exact module-two publication.
type PublishedKnowledgeBundle struct {
	PublicationRef KnowledgePublicationRef `json:"publication_ref"`
	Scope          ApplicableScope         `json:"scope"`
	Entries        []KnowledgeEntry        `json:"entries"`
}

// KnowledgeEntry represents a published knowledge entry version available to module six.
type KnowledgeEntry struct {
	EntryVersionID string           `json:"entry_version_id"`
	Title          string           `json:"title"`
	Summary        string           `json:"summary"`
	Scope          ApplicableScope  `json:"scope"`
	ContentHash    string           `json:"content_hash"`
	Sources        []CitationSource `json:"sources"`
}

// AnalysisRetrievalQuery is the provider input for pre-analysis retrieval.
type AnalysisRetrievalQuery struct {
	PublicationRef     KnowledgePublicationRef `json:"publication_ref"`
	Scope              ApplicableScope         `json:"scope"`
	TranscriptVersions TranscriptVersionSet    `json:"transcript_versions"`
	MinimizedQueryText string                  `json:"minimized_query_text"`
}

// ReferenceGenerationQuery is the provider input for reference suggestion generation.
type ReferenceGenerationQuery struct {
	PublicationRef     KnowledgePublicationRef `json:"publication_ref"`
	Scope              ApplicableScope         `json:"scope"`
	TranscriptVersions TranscriptVersionSet    `json:"transcript_versions"`
	Basis              ReferenceBasis          `json:"basis"`
}

// ReferenceGeneration contains provider output after it has been mapped back to published knowledge.
type ReferenceGeneration struct {
	Policies      []PolicySuggestion       `json:"policies"`
	Materials     []MaterialSuggestion     `json:"materials"`
	HandlingPaths []HandlingPathSuggestion `json:"handling_paths"`
	FAQs          []FAQSuggestion          `json:"faqs"`
	Generation    ReferenceGenerationInfo  `json:"generation"`
}

// ReferenceEvent is the notification payload used by WSS/SSE or future outbox adapters.
type ReferenceEvent struct {
	Name             string                   `json:"name"`
	SessionID        string                   `json:"session_id"`
	ContextID        string                   `json:"context_id,omitempty"`
	SuggestionSetID  string                   `json:"suggestion_set_id,omitempty"`
	OperationID      string                   `json:"operation_id,omitempty"`
	Outcome          string                   `json:"outcome,omitempty"`
	BasisDigest      string                   `json:"basis_digest,omitempty"`
	TranscriptDigest string                   `json:"transcript_digest,omitempty"`
	PublicationRef   *KnowledgePublicationRef `json:"publication_ref,omitempty"`
	Reason           string                   `json:"reason,omitempty"`
}
