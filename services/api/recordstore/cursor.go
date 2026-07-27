package recordstore

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

const cursorVersion = "v1"

// CursorKind fixes the stable ordering represented by an opaque record cursor.
type CursorKind string

const (
	CursorParticipants CursorKind = "participants"
	CursorSessionTurns CursorKind = "session_turns"
	CursorHistory      CursorKind = "history"
)

// Cursor holds one keyset position. Its valid position fields depend on Kind:
// participants use SpeakerCode and ID, session turns use SequenceNo and ID, and history uses
// CreatedAt and ID. Scope binds the cursor to the complete canonical query, including ownership.
type Cursor struct {
	Kind        CursorKind `json:"kind"`
	Scope       string     `json:"scope"`
	SpeakerCode string     `json:"speaker_code,omitzero"`
	SequenceNo  int64      `json:"sequence_no,omitzero"`
	CreatedAt   time.Time  `json:"created_at,omitzero"`
	ID          string     `json:"id"`
}

// CursorCodec signs and verifies versioned opaque keyset cursors.
type CursorCodec struct {
	signingKey []byte
}

// NewCursorCodec constructs a codec with the process-local HMAC key supplied by application wiring.
func NewCursorCodec(signingKey []byte) (*CursorCodec, error) {
	if len(signingKey) == 0 {
		return nil, fmt.Errorf("create cursor codec: signing key is empty")
	}
	return &CursorCodec{signingKey: bytes.Clone(signingKey)}, nil
}

// CursorScope creates the deterministic query binding embedded in a cursor. Callers must include
// every filter and trusted ownership value in values; it normalizes key and value order.
func CursorScope(kind CursorKind, values url.Values) string {
	normalized := make(url.Values, len(values))
	for key, rawValues := range values {
		normalized[key] = slices.Sorted(slices.Values(rawValues))
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(kind))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(normalized.Encode()))
	return base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
}

// Encode signs a valid keyset position as v1.<payload>.<mac>.
func (codec *CursorCodec) Encode(cursor Cursor) (string, error) {
	if err := validateCursor(cursor); err != nil {
		return "", err
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("marshal record cursor: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	return cursorVersion + "." + encodedPayload + "." + codec.signature(encodedPayload), nil
}

// Decode verifies a cursor and rejects cursors issued for another ordering or query scope.
func (codec *CursorCodec) Decode(value string, expectedKind CursorKind, expectedScope string) (Cursor, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != cursorVersion || expectedScope == "" {
		return Cursor{}, ErrInvalidCursor
	}
	if !hmac.Equal([]byte(parts[2]), []byte(codec.signature(parts[1]))) {
		return Cursor{}, ErrInvalidCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var cursor Cursor
	if err := decoder.Decode(&cursor); err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	if decoder.Decode(&struct{}{}) == nil || cursor.Kind != expectedKind || cursor.Scope != expectedScope {
		return Cursor{}, ErrInvalidCursor
	}
	if err := validateCursor(cursor); err != nil {
		return Cursor{}, err
	}
	return cursor, nil
}

func (codec *CursorCodec) signature(payload string) string {
	mac := hmac.New(sha256.New, codec.signingKey)
	_, _ = mac.Write([]byte(cursorVersion))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func validateCursor(cursor Cursor) error {
	if cursor.Scope == "" || cursor.ID == "" {
		return ErrInvalidCursor
	}
	switch cursor.Kind {
	case CursorParticipants:
		if cursor.SpeakerCode == "" || cursor.SequenceNo != 0 || !cursor.CreatedAt.IsZero() {
			return ErrInvalidCursor
		}
	case CursorSessionTurns:
		if cursor.SpeakerCode != "" || cursor.SequenceNo < 1 || !cursor.CreatedAt.IsZero() {
			return ErrInvalidCursor
		}
	case CursorHistory:
		if cursor.SpeakerCode != "" || cursor.SequenceNo != 0 || cursor.CreatedAt.IsZero() {
			return ErrInvalidCursor
		}
	default:
		return ErrInvalidCursor
	}
	return nil
}
