package modeprojection

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresRepository records the event and projection in one transaction. A successful
// broker ACK therefore means both the immutable fact and its latest-observed view are durable.
type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) ProjectModeChanged(ctx context.Context, event realtimev1.ModeChangedEvent, payloadHash [sha256.Size]byte) error {
	if r == nil || r.pool == nil {
		return domain.ErrNotImplemented
	}
	if err := event.Validate(); err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin mode projection transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	result, err := tx.Exec(ctx, `
INSERT INTO realtime_mode_events (
    event_id, payload_hash, event_version, trace_id, session_id,
    runtime_instance_id, operation_id, from_mode, to_mode,
    resulting_generation, occurred_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (event_id) DO NOTHING`,
		event.EventID, payloadHash[:], event.EventVersion, event.TraceID, event.SessionID,
		event.RuntimeInstanceID, event.OperationID, event.FromMode, event.ToMode,
		event.ResultingGeneration, event.OccurredAt.UTC(),
	)
	if err != nil {
		return mapError(err)
	}
	if result.RowsAffected() == 0 {
		var storedHash []byte
		if err := tx.QueryRow(ctx, `SELECT payload_hash FROM realtime_mode_events WHERE event_id=$1 FOR UPDATE`, event.EventID).Scan(&storedHash); err != nil {
			return mapError(err)
		}
		if len(storedHash) != len(payloadHash) || !equalHash(storedHash, payloadHash[:]) {
			return domain.ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit mode projection replay: %w", err)
		}
		return nil
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO realtime_mode_projections (
    session_id, runtime_instance_id, active_mode, generation,
    last_event_id, occurred_at
) VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (session_id) DO UPDATE SET
    runtime_instance_id = EXCLUDED.runtime_instance_id,
    active_mode = EXCLUDED.active_mode,
    generation = EXCLUDED.generation,
    last_event_id = EXCLUDED.last_event_id,
    occurred_at = EXCLUDED.occurred_at,
    updated_at = CURRENT_TIMESTAMP
WHERE (
    realtime_mode_projections.runtime_instance_id = EXCLUDED.runtime_instance_id
    AND EXCLUDED.generation > realtime_mode_projections.generation
) OR (
    realtime_mode_projections.runtime_instance_id <> EXCLUDED.runtime_instance_id
    AND EXCLUDED.occurred_at > realtime_mode_projections.occurred_at
)`,
		event.SessionID, event.RuntimeInstanceID, event.ToMode, event.ResultingGeneration,
		event.EventID, event.OccurredAt.UTC(),
	); err != nil {
		return mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit mode projection: %w", err)
	}
	return nil
}

func equalHash(left, right []byte) bool {
	return len(left) == len(right) && string(left) == string(right)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return domain.ErrConflict
		case "23503":
			return domain.ErrNotFound
		case "23514", "22003", "22P02":
			return domain.ErrInvalidArgument
		}
	}
	return fmt.Errorf("postgres mode projection operation: %w", err)
}

var _ Repository = (*PostgresRepository)(nil)
