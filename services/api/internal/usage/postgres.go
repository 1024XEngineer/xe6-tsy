package usage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Record(ctx context.Context, input RecordInput) (Detail, bool, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return Detail{}, false, err
	}
	hash := sha256.Sum256(payload)
	now := time.Now().UTC()
	result, err := r.pool.Exec(ctx, `INSERT INTO lingow_usage_records (event_version, event_id, trace_id, idempotency_key, payload_hash, account_id, session_id, turn_id, service_type, provider, model, input_tokens, output_tokens, audio_duration_ms, cost_amount, currency, occurred_at, recorded_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NULLIF($15,'')::numeric,NULLIF($16,''),$17,$18) ON CONFLICT (idempotency_key) DO NOTHING`, input.EventVersion, input.ID, input.TraceID, input.IdempotencyKey, hash[:], input.AccountID, input.SessionID, input.TurnID, input.ServiceType, input.Provider, input.Model, input.InputTokens, input.OutputTokens, input.AudioDurationMS, input.CostAmount, input.Currency, input.OccurredAt.UTC(), now)
	if err != nil {
		return Detail{}, false, mapUsageError(err)
	}
	if result.RowsAffected() == 0 {
		var storedHash []byte
		detail, err := r.scanDetail(ctx, `SELECT event_version,event_id,trace_id,idempotency_key,payload_hash,account_id,session_id,turn_id,service_type,provider,model,input_tokens,output_tokens,audio_duration_ms,COALESCE(cost_amount::text,''),COALESCE(currency,''),occurred_at,recorded_at FROM lingow_usage_records WHERE idempotency_key=$1`, input.IdempotencyKey, &storedHash)
		if err != nil {
			return Detail{}, false, err
		}
		if !equalHash(storedHash, hash[:]) {
			return Detail{}, false, domain.ErrConflict
		}
		return detail, false, nil
	}
	detail := Detail{RecordInput: input, RecordedAt: now}
	return detail, true, nil
}

func (r *PostgresRepository) SessionSummary(ctx context.Context, accountID, sessionID string) (Summary, error) {
	return r.summary(ctx, accountID, sessionID, time.Time{}, time.Time{})
}

func (r *PostgresRepository) AccountSummary(ctx context.Context, accountID string, start, end time.Time) (Summary, error) {
	return r.summary(ctx, accountID, "", start, end)
}

func (r *PostgresRepository) summary(ctx context.Context, accountID, sessionID string, start, end time.Time) (Summary, error) {
	args := []any{accountID}
	where := `account_id=$1`
	if sessionID != "" {
		args = append(args, sessionID)
		where += fmt.Sprintf(" AND session_id=$%d", len(args))
	}
	if !start.IsZero() {
		args = append(args, start, end)
		where += fmt.Sprintf(" AND occurred_at >= $%d AND occurred_at < $%d", len(args)-1, len(args))
	}
	rows, err := r.pool.Query(ctx, `SELECT service_type,COALESCE(currency,''),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(audio_duration_ms),0),CASE WHEN COUNT(cost_amount)=0 THEN '' ELSE SUM(cost_amount)::text END FROM lingow_usage_records WHERE `+where+` GROUP BY service_type,currency ORDER BY service_type,currency`, args...)
	if err != nil {
		return Summary{}, mapUsageError(err)
	}
	defer rows.Close()
	result := Summary{AccountID: accountID, SessionID: sessionID, PeriodStart: start, PeriodEnd: end, Totals: make([]StageTotal, 0)}
	seen := make(map[Stage]bool)
	for rows.Next() {
		var total StageTotal
		if err := rows.Scan(&total.ServiceType, &total.Currency, &total.InputTokens, &total.OutputTokens, &total.AudioDurationMS, &total.CostAmount); err != nil {
			return Summary{}, err
		}
		if seen[total.ServiceType] {
			return Summary{}, domain.ErrConflict
		}
		seen[total.ServiceType] = true
		result.Totals = append(result.Totals, total)
	}
	if err := rows.Err(); err != nil {
		return Summary{}, err
	}
	return result, nil
}

func (r *PostgresRepository) scanDetail(ctx context.Context, query string, arg any, hash *[]byte) (Detail, error) {
	var detail Detail
	var service Stage
	err := r.pool.QueryRow(ctx, query, arg).Scan(&detail.EventVersion, &detail.ID, &detail.TraceID, &detail.IdempotencyKey, hash, &detail.AccountID, &detail.SessionID, &detail.TurnID, &service, &detail.Provider, &detail.Model, &detail.InputTokens, &detail.OutputTokens, &detail.AudioDurationMS, &detail.CostAmount, &detail.Currency, &detail.OccurredAt, &detail.RecordedAt)
	detail.ServiceType = service
	return detail, mapUsageError(err)
}

func equalHash(left, right []byte) bool {
	return len(left) == len(right) && string(left) == string(right)
}
func mapUsageError(err error) error {
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
	return fmt.Errorf("postgres usage operation: %w", err)
}
