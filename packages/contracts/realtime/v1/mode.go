package realtimev1

import "time"

const (
	// ModeChangedTopic carries durable realtime mode changes to control-plane consumers.
	ModeChangedTopic = "realtime.mode.changed"
	// AssistantReplyTopic carries finalized assistant replies without overloading FinalTurn.
	AssistantReplyTopic = "assistant.reply"

	ModeChangedEventVersion    = 1
	AssistantReplyEventVersion = 1
)

// Mode identifies the business handler used after the shared ASR final result.
type Mode string

const (
	ModeAssistant      Mode = "assistant"
	ModeInterpretation Mode = "interpretation"
)

// Valid reports whether the mode is currently executable by the public contract.
func (m Mode) Valid() bool {
	switch m {
	case ModeAssistant, ModeInterpretation:
		return true
	default:
		return false
	}
}

// OrLegacyDefault preserves the existing interpretation behavior when an older caller omits mode.
func (m Mode) OrLegacyDefault() Mode {
	if m == "" {
		return ModeInterpretation
	}
	return m
}

// ModePhase describes whether a runtime can open normal business turns.
type ModePhase string

const (
	ModePhaseActive    ModePhase = "active"
	ModePhaseSwitching ModePhase = "switching"
)

// Valid reports whether the phase belongs to the public mode-state contract.
func (p ModePhase) Valid() bool {
	switch p {
	case ModePhaseActive, ModePhaseSwitching:
		return true
	default:
		return false
	}
}

// ModeSwitchStatus distinguishes a state change from an idempotent no-op.
type ModeSwitchStatus string

const (
	ModeSwitchApplied   ModeSwitchStatus = "applied"
	ModeSwitchUnchanged ModeSwitchStatus = "unchanged"
)

// Valid reports whether the status belongs to the public switch-result contract.
func (s ModeSwitchStatus) Valid() bool {
	switch s {
	case ModeSwitchApplied, ModeSwitchUnchanged:
		return true
	default:
		return false
	}
}

// ModeStateSnapshot is the authoritative business-mode state owned by one realtime runtime.
// Generation is monotonic only within RuntimeInstanceID.
type ModeStateSnapshot struct {
	SessionID         string    `json:"session_id"`
	RuntimeInstanceID string    `json:"runtime_instance_id"`
	ActiveMode        Mode      `json:"active_mode"`
	Generation        int64     `json:"generation"`
	Phase             ModePhase `json:"phase"`
	LastOperationID   *string   `json:"last_operation_id"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// SwitchModeCommand requests an idempotent compare-and-switch against one runtime generation.
type SwitchModeCommand struct {
	SessionID          string `json:"session_id"`
	RuntimeInstanceID  string `json:"runtime_instance_id"`
	OperationID        string `json:"operation_id"`
	TraceID            string `json:"trace_id"`
	ExpectedGeneration int64  `json:"expected_generation"`
	TargetMode         Mode   `json:"target_mode"`
}

// SwitchModeResult returns the state produced by the first execution of an operation.
type SwitchModeResult struct {
	OperationID string            `json:"operation_id"`
	Status      ModeSwitchStatus  `json:"status"`
	State       ModeStateSnapshot `json:"state"`
}

// ModeChangedEvent is the durable fact projected from realtime to the API service.
type ModeChangedEvent struct {
	EventVersion        int       `json:"event_version"`
	EventID             string    `json:"event_id"`
	TraceID             string    `json:"trace_id"`
	SessionID           string    `json:"session_id"`
	RuntimeInstanceID   string    `json:"runtime_instance_id"`
	OperationID         string    `json:"operation_id"`
	FromMode            Mode      `json:"from_mode"`
	ToMode              Mode      `json:"to_mode"`
	ResultingGeneration int64     `json:"resulting_generation"`
	OccurredAt          time.Time `json:"occurred_at"`
}

// AssistantReplyEvent is a finalized realtime assistant reply and is never a translation FinalTurn.
type AssistantReplyEvent struct {
	EventVersion      int       `json:"event_version"`
	EventID           string    `json:"event_id"`
	TraceID           string    `json:"trace_id"`
	SessionID         string    `json:"session_id"`
	TurnID            string    `json:"turn_id"`
	RuntimeInstanceID string    `json:"runtime_instance_id"`
	Generation        int64     `json:"generation"`
	Text              string    `json:"text"`
	Language          string    `json:"language"`
	OccurredAt        time.Time `json:"occurred_at"`
}
