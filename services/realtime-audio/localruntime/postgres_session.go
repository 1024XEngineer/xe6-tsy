package localruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresSessionReader reads voice_sessions for realtime Start ownership checks.
// It mirrors the API SessionReader SQL without importing services/api (module cycle).
type PostgresSessionReader struct {
	Pool *pgxpool.Pool
}

func (r PostgresSessionReader) GetSession(ctx context.Context, sessionID string) (session.SessionSnapshot, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return session.SessionSnapshot{}, session.ErrSessionIDRequired
	}
	if r.Pool == nil {
		return session.SessionSnapshot{}, fmt.Errorf("postgres session reader pool is required")
	}
	var (
		id, accountID, status string
		startedAt, endedAt    *time.Time
	)
	err := r.Pool.QueryRow(ctx, `
		SELECT id, account_id, status, started_at, ended_at
		FROM voice_sessions
		WHERE id = $1
	`, sessionID).Scan(&id, &accountID, &status, &startedAt, &endedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return session.SessionSnapshot{}, fmt.Errorf("voice session %q not found", sessionID)
		}
		return session.SessionSnapshot{}, fmt.Errorf("read voice_sessions: %w", err)
	}
	return session.SessionSnapshot{
		SessionID: id,
		AccountID: accountID,
		Status:    status,
		StartedAt: startedAt,
		EndedAt:   endedAt,
	}, nil
}
