package matter

const (
	// EventMatterSuggestionSetPreparedV1 notifies consumers that module 5 finished a suggestion attempt.
	EventMatterSuggestionSetPreparedV1 = "matter.suggestion_set.prepared.v1"
	// EventMatterSuggestionSetStaleV1 notifies consumers that a prior module 5 suggestion set is no longer current.
	EventMatterSuggestionSetStaleV1 = "matter.suggestion_set.stale.v1"
)

// MatterSuggestionSetPreparedEvent is the minimal display event for a completed module 5 suggestion attempt.
type MatterSuggestionSetPreparedEvent struct {
	SuggestionSetID  string            `json:"suggestion_set_id"`
	OperationID      string            `json:"operation_id"`
	Outcome          SuggestionOutcome `json:"outcome"`
	SessionID        SessionID         `json:"session_id"`
	TranscriptDigest string            `json:"transcript_digest"`
}

// MatterSuggestionSetStaleEvent is emitted when a transcript change invalidates a prior suggestion view.
type MatterSuggestionSetStaleEvent struct {
	SuggestionSetID          string    `json:"suggestion_set_id"`
	SessionID                SessionID `json:"session_id"`
	Reason                   string    `json:"reason"`
	CurrentTranscriptDigest  string    `json:"current_transcript_digest"`
	PreviousTranscriptDigest string    `json:"previous_transcript_digest,omitempty"`
}
