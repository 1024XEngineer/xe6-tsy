package delivery

import (
	"context"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
)

const emailBindAdvisoryLockSQL = `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`

const emailBindAccountRateQuerySQL = `
	SELECT MAX(created_at), COUNT(*) FILTER (WHERE created_at > $2)
	FROM email_bind_challenges
	WHERE account_id = $1`

const emailBindAddressRateQuerySQL = `
	SELECT MAX(created_at), COUNT(*) FILTER (WHERE created_at > $2)
	FROM email_bind_challenges
	WHERE email = $1`

func (r *PostgresRepository) CreateEmailBindChallenge(ctx context.Context, challenge EmailBindChallenge) error {
	if challenge.ID == "" || challenge.AccountID == "" || challenge.DestinationRef == "" ||
		challenge.Email == "" || challenge.TokenHash == "" || challenge.ExpiresAt.IsZero() {
		return domain.ErrInvalidArgument
	}
	if challenge.CreatedAt.IsZero() {
		challenge.CreatedAt = time.Now().UTC()
	}
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, emailBindAdvisoryLockSQL, challenge.AccountID); err != nil {
			return err
		}
		windowStart := challenge.CreatedAt.Add(-emailBindChallengeWindow)
		if err := checkEmailBindRateLimit(ctx, tx, emailBindAccountRateQuerySQL, challenge.AccountID, challenge.CreatedAt, windowStart); err != nil {
			return err
		}
		if err := checkEmailBindRateLimit(ctx, tx, emailBindAddressRateQuerySQL, challenge.Email, challenge.CreatedAt, windowStart); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO email_bind_challenges (
				id, account_id, destination_ref, email, token_hash, expires_at, used_at, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, NULL, $7)`,
			challenge.ID, challenge.AccountID, challenge.DestinationRef, challenge.Email,
			challenge.TokenHash, challenge.ExpiresAt, challenge.CreatedAt,
		)
		return err
	})
	return mapDeliveryError(err)
}

func checkEmailBindRateLimit(ctx context.Context, tx pgx.Tx, querySQL, key string, now, windowStart time.Time) error {
	var latest *time.Time
	var sends int64
	if err := tx.QueryRow(ctx, querySQL, key, windowStart).Scan(&latest, &sends); err != nil {
		return err
	}
	return enforceEmailBindRateLimit(latest, sends, now)
}

func (r *PostgresRepository) ConsumeEmailBindChallenge(ctx context.Context, accountID, tokenHash string) (EmailBindChallenge, error) {
	if accountID == "" || tokenHash == "" {
		return EmailBindChallenge{}, domain.ErrInvalidArgument
	}
	var challenge EmailBindChallenge
	err := r.pool.QueryRow(ctx, `
		UPDATE email_bind_challenges
		SET used_at = NOW()
		WHERE token_hash = $1
		  AND account_id = $2
		  AND used_at IS NULL
		  AND expires_at > NOW()
		RETURNING id, account_id, destination_ref, email, token_hash, expires_at, used_at, created_at`,
		tokenHash, accountID,
	).Scan(
		&challenge.ID, &challenge.AccountID, &challenge.DestinationRef, &challenge.Email,
		&challenge.TokenHash, &challenge.ExpiresAt, &challenge.UsedAt, &challenge.CreatedAt,
	)
	if err != nil {
		return EmailBindChallenge{}, mapDeliveryError(err)
	}
	return challenge, nil
}

func (r *PostgresRepository) RestoreEmailBindChallenge(ctx context.Context, id string) error {
	if id == "" {
		return domain.ErrInvalidArgument
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE email_bind_challenges
		SET used_at = NULL
		WHERE id = $1
		  AND used_at IS NOT NULL
		  AND expires_at > NOW()`, id)
	return mapDeliveryError(err)
}

var _ EmailBindChallengeRepository = (*PostgresRepository)(nil)
