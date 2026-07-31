// Package localruntime provides minimal adapters for a deployable local
// realtime-audio control-plane process until business SessionReader and
// PipelineManager production ports are wired.
package localruntime

import (
	"context"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

// TrustSessionReader treats any session ID as an API-created business session.
// Local HTTP entrypoints use this until realtime can read session ownership
// from the API database or a shared store.
type TrustSessionReader struct{}

func (TrustSessionReader) GetSession(_ context.Context, sessionID string) (session.SessionSnapshot, error) {
	if sessionID == "" {
		return session.SessionSnapshot{}, session.ErrSessionIDRequired
	}
	return session.SessionSnapshot{
		SessionID: sessionID,
		AccountID: "local-dev",
		Status:    "created",
	}, nil
}
