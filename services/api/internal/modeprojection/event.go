package modeprojection

import (
	"crypto/sha256"
	"encoding/json"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

// hashModeChangedEvent uses the same typed JSON representation as the realtime outbox adapter.
// Every contract field participates in replay identity; broker formatting is deliberately excluded.
func hashModeChangedEvent(event realtimev1.ModeChangedEvent) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}
