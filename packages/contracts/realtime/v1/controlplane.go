package realtimev1

import "time"

// StartRequest binds a durable control-plane operation to one media runtime.
// OperationID is generated and persisted by the Session service before this
// request crosses the realtime boundary.
type StartRequest struct {
	OperationID string `json:"operation_id"`
	TraceID     string `json:"trace_id"`
	StartedBy   string `json:"started_by"`
}

// StopRequest carries the Session service's durable end intent metadata to the
// media plane. Realtime cleanup does not mutate business session state.
type StopRequest struct {
	TraceID string    `json:"trace_id"`
	Reason  string    `json:"reason"`
	EndedAt time.Time `json:"ended_at"`
}
