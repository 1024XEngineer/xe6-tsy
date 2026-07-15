package reception

import "time"

const (
	EventReceptionSessionCreated   = "ReceptionSessionCreated"
	EventReceptionSessionStarted   = "ReceptionSessionStarted"
	EventReceptionSessionEnded     = "ReceptionSessionEnded"
	EventReceptionSessionCancelled = "ReceptionSessionCancelled"
	EventMediaTrackAttached        = "MediaTrackAttached"
	EventMediaTrackDetached        = "MediaTrackDetached"
	EventMediaTrackUnavailable     = "MediaTrackUnavailable"
)

type DomainEvent struct {
	EventID          string         `json:"event_id"`
	EventType        string         `json:"event_type"`
	AggregateID      string         `json:"aggregate_id"`
	AggregateType    string         `json:"aggregate_type"`
	AggregateVersion int64          `json:"aggregate_version"`
	SessionID        string         `json:"session_id"`
	OrganizationID   string         `json:"organization_id"`
	OperatorID       string         `json:"operator_id"`
	TraceID          string         `json:"trace_id"`
	OccurredAt       time.Time      `json:"occurred_at"`
	Payload          map[string]any `json:"payload"`
}

type AuditEntry struct {
	AuditID        string
	Action         string
	AggregateID    string
	SessionID      string
	OperatorID     string
	OrganizationID string
	OccurredAt     time.Time
	Result         string
}
