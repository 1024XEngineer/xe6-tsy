package delivery

import (
	"context"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

// ListMessages reads recent account-owned messages in stable newest-first order.
func (r *PostgresRepository) ListMessages(ctx context.Context, accountID string, limit int) ([]Message, error) {
	if r == nil || r.pool == nil || accountID == "" || limit <= 0 {
		return nil, domain.ErrInvalidArgument
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id,account_id,channel,destination_ref,snapshot_version,turns,status,
		attempts,last_error_code,created_at,updated_at
		FROM outbound_messages
		WHERE account_id IN (SELECT account_id FROM lingow_account_lineage($1))
		ORDER BY created_at DESC,id DESC
		LIMIT $2`, accountID, limit)
	if err != nil {
		return nil, mapDeliveryError(err)
	}
	defer rows.Close()
	messages := make([]Message, 0, limit)
	for rows.Next() {
		message, err := scanMessageRow(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}
