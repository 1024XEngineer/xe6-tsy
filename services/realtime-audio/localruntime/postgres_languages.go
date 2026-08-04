package localruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresLanguageConfigReader loads the active bilingual config for a session.
type PostgresLanguageConfigReader struct {
	Pool *pgxpool.Pool
	Now  func() time.Time
}

type languagePairJSON struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

func (r PostgresLanguageConfigReader) GetCurrentConfig(
	ctx context.Context,
	sessionID string,
) (session.LanguageConfigSnapshot, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return session.LanguageConfigSnapshot{}, session.ErrSessionIDRequired
	}
	if r.Pool == nil {
		return session.LanguageConfigSnapshot{}, fmt.Errorf("postgres language reader pool is required")
	}
	var (
		version int64
		status  string
		raw     []byte
	)
	err := r.Pool.QueryRow(ctx, `
		SELECT version, status, language_pairs
		FROM voice_session_language_configs
		WHERE session_id = $1 AND status = 'active'
	`, sessionID).Scan(&version, &status, &raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return session.LanguageConfigSnapshot{}, fmt.Errorf("no active language config for session %q", sessionID)
		}
		return session.LanguageConfigSnapshot{}, fmt.Errorf("read language config: %w", err)
	}
	var pairs []languagePairJSON
	if err := json.Unmarshal(raw, &pairs); err != nil {
		return session.LanguageConfigSnapshot{}, fmt.Errorf("decode language_pairs: %w", err)
	}
	out := make([]session.LanguagePair, 0, len(pairs))
	for _, pair := range pairs {
		source := strings.TrimSpace(pair.Source)
		target := strings.TrimSpace(pair.Target)
		if source == "" || target == "" || source == target {
			continue
		}
		out = append(out, session.LanguagePair{Source: source, Target: target})
	}
	if len(out) == 0 {
		return session.LanguageConfigSnapshot{}, fmt.Errorf("active language config has no usable pairs")
	}
	now := r.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return session.LanguageConfigSnapshot{
		SessionID:     sessionID,
		Version:       version,
		Status:        status,
		LanguagePairs: out,
		UpdatedAt:     now(),
	}, nil
}

// FallbackLanguageConfigReader tries Primary, then Fallback (env static pair).
type FallbackLanguageConfigReader struct {
	Primary  session.LanguageConfigReader
	Fallback session.LanguageConfigReader
}

func (r FallbackLanguageConfigReader) GetCurrentConfig(
	ctx context.Context,
	sessionID string,
) (session.LanguageConfigSnapshot, error) {
	if r.Primary != nil {
		snapshot, err := r.Primary.GetCurrentConfig(ctx, sessionID)
		if err == nil {
			return snapshot, nil
		}
	}
	if r.Fallback == nil {
		return session.LanguageConfigSnapshot{}, fmt.Errorf("language config unavailable for %q", sessionID)
	}
	return r.Fallback.GetCurrentConfig(ctx, sessionID)
}
