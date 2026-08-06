package sessions

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

const sessionListCursorVersion = 1

type sessionListCursor struct {
	Version   int       `json:"v"`
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func encodeSessionListCursor(createdAt time.Time, id string) (string, error) {
	if createdAt.IsZero() || id == "" {
		return "", ErrInvalidRequest
	}
	payload, err := json.Marshal(sessionListCursor{
		Version:   sessionListCursorVersion,
		CreatedAt: createdAt.UTC(),
		ID:        id,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeSessionListCursor(encoded string) (sessionListCursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return sessionListCursor{}, ErrInvalidRequest
	}
	var cursor sessionListCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return sessionListCursor{}, ErrInvalidRequest
	}
	if cursor.Version != sessionListCursorVersion ||
		cursor.CreatedAt.IsZero() ||
		cursor.ID == "" {
		return sessionListCursor{}, ErrInvalidRequest
	}
	cursor.CreatedAt = cursor.CreatedAt.UTC()
	return cursor, nil
}
