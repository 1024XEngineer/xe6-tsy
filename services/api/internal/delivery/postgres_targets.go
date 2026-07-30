package delivery

import (
	"context"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

// TargetRepository exposes account-owned destination bindings without returning ciphertext.
type TargetRepository interface {
	ListMessageTargets(context.Context, string, *Channel) ([]MessageTarget, error)
	BindEmailTarget(context.Context, BindEmailTargetRecord) (MessageTarget, error)
	BindWeChatTarget(context.Context, BindWeChatTargetRecord) (MessageTarget, error)
	RevokeMessageTarget(context.Context, string, Channel, string, time.Time) error
}

func (r *PostgresRepository) ListMessageTargets(ctx context.Context, accountID string, channel *Channel) ([]MessageTarget, error) {
	if accountID == "" {
		return nil, domain.ErrInvalidArgument
	}
	var channelFilter *string
	if channel != nil {
		if !IsSupportedChannel(*channel) {
			return nil, domain.ErrInvalidArgument
		}
		value := string(*channel)
		channelFilter = &value
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT ON (d.channel, d.destination_ref)
			d.destination_ref,
			d.channel,
			(d.verified_at IS NOT NULL AND d.revoked_at IS NULL),
			d.revoked_at,
			d.updated_at
		FROM account_destinations d
		WHERE d.account_id IN (SELECT account_id FROM lingow_account_lineage($1))
		  AND ($2::text IS NULL OR d.channel = $2)
		ORDER BY d.channel, d.destination_ref, (d.account_id = $1) DESC, d.updated_at DESC`,
		accountID, channelFilter,
	)
	if err != nil {
		return nil, mapDeliveryError(err)
	}
	defer rows.Close()

	result := make([]MessageTarget, 0)
	for rows.Next() {
		var target MessageTarget
		if err := rows.Scan(&target.DestinationRef, &target.Channel, &target.Verified, &target.RevokedAt, &target.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, target)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) BindEmailTarget(ctx context.Context, record BindEmailTargetRecord) (MessageTarget, error) {
	if record.AccountID == "" || record.DestinationRef == "" || len(record.Ciphertext) == 0 || record.KeyVersion == "" {
		return MessageTarget{}, domain.ErrInvalidArgument
	}
	var target MessageTarget
	err := r.pool.QueryRow(ctx, `
		INSERT INTO account_destinations (
			id, account_id, channel, destination_ref, provider_target_ciphertext,
			key_version, verified_at, revoked_at, created_at, updated_at
		) VALUES ($1, $2, 'email', $3, $4, $5, $6, NULL, $6, $6)
		ON CONFLICT (account_id, channel, destination_ref) DO UPDATE
		SET provider_target_ciphertext = EXCLUDED.provider_target_ciphertext,
		    key_version = EXCLUDED.key_version,
		    verified_at = EXCLUDED.verified_at,
		    revoked_at = NULL,
		    updated_at = EXCLUDED.updated_at
		RETURNING destination_ref, channel, (verified_at IS NOT NULL AND revoked_at IS NULL), revoked_at, updated_at`,
		record.ID, record.AccountID, record.DestinationRef, record.Ciphertext, record.KeyVersion, record.VerifiedAt,
	).Scan(&target.DestinationRef, &target.Channel, &target.Verified, &target.RevokedAt, &target.UpdatedAt)
	if err != nil {
		return MessageTarget{}, mapDeliveryError(err)
	}
	return target, nil
}

func (r *PostgresRepository) BindWeChatTarget(ctx context.Context, record BindWeChatTargetRecord) (MessageTarget, error) {
	if record.AccountID == "" || record.DestinationRef == "" || len(record.Ciphertext) == 0 || record.KeyVersion == "" {
		return MessageTarget{}, domain.ErrInvalidArgument
	}
	var target MessageTarget
	err := r.pool.QueryRow(ctx, `
		INSERT INTO account_destinations (
			id, account_id, channel, destination_ref, provider_target_ciphertext,
			key_version, verified_at, revoked_at, created_at, updated_at
		) VALUES ($1, $2, 'wechat', $3, $4, $5, $6, NULL, $6, $6)
		ON CONFLICT (account_id, channel, destination_ref) DO UPDATE
		SET provider_target_ciphertext = EXCLUDED.provider_target_ciphertext,
		    key_version = EXCLUDED.key_version,
		    verified_at = EXCLUDED.verified_at,
		    revoked_at = NULL,
		    updated_at = EXCLUDED.updated_at
		RETURNING destination_ref, channel, (verified_at IS NOT NULL AND revoked_at IS NULL), revoked_at, updated_at`,
		record.ID, record.AccountID, record.DestinationRef, record.Ciphertext, record.KeyVersion, record.VerifiedAt,
	).Scan(&target.DestinationRef, &target.Channel, &target.Verified, &target.RevokedAt, &target.UpdatedAt)
	if err != nil {
		return MessageTarget{}, mapDeliveryError(err)
	}
	return target, nil
}

func (r *PostgresRepository) RevokeMessageTarget(ctx context.Context, accountID string, channel Channel, destinationRef string, revokedAt time.Time) error {
	if accountID == "" || destinationRef == "" || !IsSupportedChannel(channel) {
		return domain.ErrInvalidArgument
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE account_destinations
		SET revoked_at = $4, updated_at = $4
		WHERE account_id IN (SELECT account_id FROM lingow_account_lineage($1))
		  AND channel = $2
		  AND destination_ref = $3
		  AND verified_at IS NOT NULL
		  AND revoked_at IS NULL`,
		accountID, channel, destinationRef, revokedAt,
	)
	if err != nil {
		return mapDeliveryError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

var _ TargetRepository = (*PostgresRepository)(nil)

func targetRepository(repository Repository) TargetRepository {
	reader, ok := repository.(TargetRepository)
	if !ok {
		return nil
	}
	return reader
}
