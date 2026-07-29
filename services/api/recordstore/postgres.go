// Package recordstore owns PostgreSQL-specific storage foundations for voice records.
package recordstore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open creates the PostgreSQL connection pool used by the voice-record storage adapter.
// All connections use UTC so PostgreSQL timestamp values have one representation at this boundary.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse record-store database URL: %w", err)
	}
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	config.ConnConfig.RuntimeParams["TimeZone"] = "UTC"

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open record-store PostgreSQL pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping record-store PostgreSQL pool: %w", err)
	}
	return pool, nil
}
