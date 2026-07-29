package realtimev1

// ControlPlaneErrorCode is a stable cross-service error identifier returned
// by realtime control-plane lifecycle operations.
type ControlPlaneErrorCode string

const (
	ErrorRuntimeOperationConflict ControlPlaneErrorCode = "runtime_operation_conflict"
)

// StartRequest binds a durable control-plane operation to one media runtime.
// OperationID is generated and persisted by the Session service before this
// request crosses the realtime boundary.
type StartRequest struct {
	OperationID string `json:"operation_id"`
	TraceID     string `json:"trace_id"`
	StartedBy   string `json:"started_by"`
}
