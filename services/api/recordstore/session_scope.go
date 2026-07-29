package recordstore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresSessionScopeReader provides the complete account session scope used by
// record queries without depending on the unfinished sessions Repository.
type PostgresSessionScopeReader struct {
	pool *pgxpool.Pool
}

func NewPostgresSessionScopeReader(pool *pgxpool.Pool) (*PostgresSessionScopeReader, error) {
	if pool == nil {
		return nil, fmt.Errorf("create PostgreSQL session scope reader: pool is required")
	}
	return &PostgresSessionScopeReader{pool: pool}, nil
}

func (r *PostgresSessionScopeReader) SessionIDsForAccount(ctx context.Context, accountID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id
		FROM voice_sessions
		WHERE account_id = $1
		ORDER BY id ASC`, accountID)
	if err != nil {
		return nil, fmt.Errorf("query account session scope: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan account session scope: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read account session scope: %w", err)
	}
	return ids, nil
}

var _ AccountSessionScopeReader = (*PostgresSessionScopeReader)(nil)
