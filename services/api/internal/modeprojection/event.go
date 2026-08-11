package modeprojection

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

// ParseModeChangedEvent validates the contract before a broker receipt can be persisted.
// The hash deliberately covers the exact broker payload, including formatting and any
// unknown fields, so a reused event ID can never silently replace an earlier payload.
func ParseModeChangedEvent(payload []byte) (realtimev1.ModeChangedEvent, [sha256.Size]byte, error) {
	var event realtimev1.ModeChangedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return realtimev1.ModeChangedEvent{}, [sha256.Size]byte{}, fmt.Errorf("%w: decode mode changed payload", domain.ErrInvalidArgument)
	}
	if err := event.Validate(); err != nil {
		return realtimev1.ModeChangedEvent{}, [sha256.Size]byte{}, err
	}
	return event, sha256.Sum256(payload), nil
}

// MarshalModeChangedEvent emits the canonical payload used by local tests and publishers.
func MarshalModeChangedEvent(event realtimev1.ModeChangedEvent) ([]byte, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(event)
}
