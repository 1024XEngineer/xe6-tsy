package accounts

import (
	"context"
	"log/slog"
	"time"
)

const (
	defaultMaintenanceInterval      = 15 * time.Minute
	defaultChallengeRetentionPeriod = 7 * 24 * time.Hour
)

// AuthMaintainer periodically purges expired login sessions and stale phone
// challenges so PostgreSQL remains the sole durable auth store.
type AuthMaintainer struct {
	repository         *PostgresRepository
	interval           time.Duration
	challengeRetention time.Duration
}

func NewAuthMaintainer(repository *PostgresRepository, interval, challengeRetention time.Duration) *AuthMaintainer {
	if interval <= 0 {
		interval = defaultMaintenanceInterval
	}
	if challengeRetention <= 0 {
		challengeRetention = defaultChallengeRetentionPeriod
	}
	return &AuthMaintainer{
		repository:         repository,
		interval:           interval,
		challengeRetention: challengeRetention,
	}
}

// Run executes maintenance until the context is cancelled.
func (m *AuthMaintainer) Run(ctx context.Context) error {
	m.purge(ctx)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.purge(ctx)
		}
	}
}

func (m *AuthMaintainer) purge(ctx context.Context) {
	sessions, err := m.repository.PurgeExpiredAuthSessions(ctx)
	if err != nil {
		slog.Error("auth maintenance: purge expired sessions", "error", err)
	} else if sessions > 0 {
		slog.Info("auth maintenance: purged expired sessions", "count", sessions)
	}

	challenges, err := m.repository.PurgeStalePhoneChallenges(ctx, m.challengeRetention)
	if err != nil {
		slog.Error("auth maintenance: purge stale phone challenges", "error", err)
	} else if challenges > 0 {
		slog.Info("auth maintenance: purged stale phone challenges", "count", challenges)
	}
}
