package usage

import (
	"crypto/sha256"
	"encoding/json"
)

type recordPayloadHash [sha256.Size]byte

// hashRecordInput defines the payload identity shared by in-memory and
// PostgreSQL repositories when enforcing idempotency-key conflicts.
func hashRecordInput(input RecordInput) (recordPayloadHash, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return recordPayloadHash{}, err
	}
	return recordPayloadHash(sha256.Sum256(payload)), nil
}
