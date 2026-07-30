package delivery

import (
	"context"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func (r *PostgresRepository) CreateEmailBindChallenge(ctx context.Context, challenge EmailBindChallenge) error {
	if challenge.ID == "" || challenge.AccountID == "" || challenge.DestinationRef == "" ||
		challenge.Email == "" || challenge.TokenHash == "" || challenge.ExpiresAt.IsZero() {
		return domain.ErrInvalidArgument
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO email_bind_challenges (
			id, account_id, destination_ref, email, token_hash, expires_at, used_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, NULL, $7)`,
		challenge.ID, challenge.AccountID, challenge.DestinationRef, challenge.Email,
		challenge.TokenHash, challenge.ExpiresAt, challenge.CreatedAt,
	)
	return mapDeliveryError(err)
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

var _ EmailBindChallengeRepository = (*PostgresRepository)(nil)
