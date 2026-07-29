package usage

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

const usageDetailProjection = `event_version,event_id,trace_id,idempotency_key,payload_hash,account_id,session_id,turn_id,service_type,provider,model,input_tokens,output_tokens,audio_duration_ms,COALESCE(cost_amount::text,''),COALESCE(currency,''),occurred_at,recorded_at`

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Record(ctx context.Context, input RecordInput) (Detail, bool, error) {
	hash, err := hashRecordInput(input)
	if err != nil {
		return Detail{}, false, err
	}
	now := time.Now().UTC()
	var storedHash []byte
	// RETURNING makes the first response use the same NUMERIC and TIMESTAMPTZ
	// representation as a later idempotent replay.
	row := r.pool.QueryRow(ctx, `INSERT INTO lingow_usage_records (event_version, event_id, trace_id, idempotency_key, payload_hash, account_id, session_id, turn_id, service_type, provider, model, input_tokens, output_tokens, audio_duration_ms, cost_amount, currency, occurred_at, recorded_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NULLIF($15,'')::numeric,NULLIF($16,''),$17,$18) ON CONFLICT (idempotency_key) DO NOTHING RETURNING `+usageDetailProjection, input.EventVersion, input.ID, input.TraceID, input.IdempotencyKey, hash[:], input.AccountID, input.SessionID, input.TurnID, input.ServiceType, input.Provider, input.Model, input.InputTokens, input.OutputTokens, input.AudioDurationMS, input.CostAmount, input.Currency, input.OccurredAt.UTC(), now)
	detail, err := scanUsageDetail(row, &storedHash)
	if err == nil {
		return detail, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, false, mapUsageError(err)
	}

	detail, err = r.scanDetail(ctx, `SELECT `+usageDetailProjection+` FROM lingow_usage_records WHERE idempotency_key=$1`, &storedHash, input.IdempotencyKey)
	if err != nil {
		return Detail{}, false, err
	}
	if !equalHash(storedHash, hash[:]) {
		return Detail{}, false, domain.ErrConflict
	}
	return detail, false, nil
}

func (r *PostgresRepository) SessionSummary(ctx context.Context, accountID, sessionID string) (Summary, error) {
	return r.summary(ctx, accountID, sessionID, time.Time{}, time.Time{})
}

func (r *PostgresRepository) AccountSummary(ctx context.Context, accountID string, start, end time.Time) (Summary, error) {
	return r.summary(ctx, accountID, "", start, end)
}

func (r *PostgresRepository) summary(ctx context.Context, accountID, sessionID string, start, end time.Time) (Summary, error) {
	args := []any{accountID}
	where := `account_id IN (SELECT account_id FROM lingow_account_lineage($1))`
	if sessionID != "" {
		args = append(args, sessionID)
		where += fmt.Sprintf(" AND session_id=$%d", len(args))
	}
	if !start.IsZero() {
		args = append(args, start, end)
		where += fmt.Sprintf(" AND occurred_at >= $%d AND occurred_at < $%d", len(args)-1, len(args))
	}
	// Counts preserve the distinction between fully unknown pricing and a
	// partially priced group, which must not be reported as a lower total.
	rows, err := r.pool.Query(ctx, `SELECT service_type,COALESCE(currency,''),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(audio_duration_ms),0),COALESCE(SUM(cost_amount),0)::text,COUNT(*),COUNT(cost_amount) FROM lingow_usage_records WHERE `+where+` GROUP BY service_type,currency ORDER BY service_type,currency`, args...)
	if err != nil {
		return Summary{}, mapUsageError(err)
	}
	defer rows.Close()
	result := Summary{AccountID: accountID, SessionID: sessionID, PeriodStart: start, PeriodEnd: end, Totals: make([]StageTotal, 0)}
	seen := make(map[Stage]bool)
	for rows.Next() {
		var total StageTotal
		var rowCount, costCount int64
		if err := rows.Scan(&total.ServiceType, &total.Currency, &total.InputTokens, &total.OutputTokens, &total.AudioDurationMS, &total.CostAmount, &rowCount, &costCount); err != nil {
			return Summary{}, err
		}
		amount, err := aggregateCost(total.CostAmount, total.Currency, rowCount, costCount)
		if err != nil {
			return Summary{}, err
		}
		total.CostAmount = amount
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

func aggregateCost(amount, currency string, rowCount, costCount int64) (string, error) {
	if rowCount <= 0 || costCount < 0 || costCount > rowCount {
		return "", domain.ErrConflict
	}
	if costCount == 0 {
		if currency != "" {
			return "", domain.ErrConflict
		}
		return "", nil
	}
	if costCount != rowCount || currency == "" {
		return "", domain.ErrConflict
	}
	normalized, ok := addMoney("", amount)
	if !ok {
		return "", domain.ErrConflict
	}
	return normalized, nil
}

func (r *PostgresRepository) scanDetail(ctx context.Context, query string, hash *[]byte, args ...any) (Detail, error) {
	detail, err := scanUsageDetail(r.pool.QueryRow(ctx, query, args...), hash)
	return detail, mapUsageError(err)
}

func scanUsageDetail(row pgx.Row, hash *[]byte) (Detail, error) {
	var detail Detail
	var service Stage
	err := row.Scan(&detail.EventVersion, &detail.ID, &detail.TraceID, &detail.IdempotencyKey, hash, &detail.AccountID, &detail.SessionID, &detail.TurnID, &service, &detail.Provider, &detail.Model, &detail.InputTokens, &detail.OutputTokens, &detail.AudioDurationMS, &detail.CostAmount, &detail.Currency, &detail.OccurredAt, &detail.RecordedAt)
	detail.ServiceType = service
	return detail, err
}

func equalHash(left, right []byte) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare(left, right) == 1
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
