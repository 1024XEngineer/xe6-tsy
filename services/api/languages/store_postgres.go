package languages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
)

// Extra store-level sentinel errors used by CreateActiveConfig and lookups.
var (
	ErrInvalidRequest      = errors.New(CodeInvalidRequest)
	ErrVersionConflict     = errors.New(CodeVersionConflict)
	ErrIdempotencyConflict = errors.New(CodeIdempotencyConflict)
)

// PostgresStore persists language configuration in PostgreSQL.
type PostgresStore struct {
	pool  *pgxpool.Pool
	clock Clock
}

// NewPostgresStore constructs a store. clock may be nil (uses UTC wall clock).
func NewPostgresStore(pool *pgxpool.Pool, clock Clock) *PostgresStore {
	if clock == nil {
		clock = systemClock{}
	}
	return &PostgresStore{pool: pool, clock: clock}
}

func (s *PostgresStore) ListSupportedLanguages(ctx context.Context, activeOnly bool) ([]SupportedLanguage, error) {
	query := `
SELECT language_code, display_name, display_name_en,
       supports_as_source, supports_as_target
FROM supported_languages`
	if activeOnly {
		query += ` WHERE is_active = TRUE`
	}
	query += ` ORDER BY sort_order ASC, language_code ASC`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list supported languages: %w", err)
	}
	defer rows.Close()

	out := make([]SupportedLanguage, 0)
	for rows.Next() {
		var item SupportedLanguage
		if err := rows.Scan(
			&item.LanguageCode,
			&item.DisplayName,
			&item.DisplayNameEN,
			&item.SupportsAsSource,
			&item.SupportsAsTarget,
		); err != nil {
			return nil, fmt.Errorf("scan supported language: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetActiveConfig(ctx context.Context, sessionID string) (LanguageConfig, error) {
	return s.scanConfig(ctx, s.pool,
		`SELECT id, session_id, version, language_pairs, status,
		        effective_from, effective_until, created_by, created_at
		 FROM voice_session_language_configs
		 WHERE session_id = $1 AND status = 'active'`,
		sessionID,
	)
}

func (s *PostgresStore) GetConfigByIdempotencyKey(ctx context.Context, idempotencyKey string) (LanguageConfig, error) {
	if idempotencyKey == "" {
		return LanguageConfig{}, ErrNoActiveConfig
	}
	return s.scanConfig(ctx, s.pool,
		`SELECT id, session_id, version, language_pairs, status,
		        effective_from, effective_until, created_by, created_at
		 FROM voice_session_language_configs
		 WHERE idempotency_key = $1`,
		idempotencyKey,
	)
}

// CreateActiveConfig supersedes the current active row (if any) and inserts version N+1.
//
// Invariants enforced in one transaction:
//   - at most one active config per session (partial unique index)
//   - versions are unique per session
//   - optional expected_version must match the current active version
//   - idempotency_key uniqueness (caller should short-circuit on replay before insert)
func (s *PostgresStore) CreateActiveConfig(ctx context.Context, input CreateConfigInput) (LanguageConfig, error) {
	if input.SessionID == "" {
		return LanguageConfig{}, fmt.Errorf("%w: session_id is required", ErrInvalidRequest)
	}
	if input.CreatedBy == "" {
		return LanguageConfig{}, fmt.Errorf("%w: created_by is required", ErrInvalidRequest)
	}
	if len(input.LanguagePairs) == 0 {
		return LanguageConfig{}, fmt.Errorf("%w: language_pairs is required", ErrInvalidRequest)
	}

	pairsJSON, err := json.Marshal(input.LanguagePairs)
	if err != nil {
		return LanguageConfig{}, fmt.Errorf("marshal language_pairs: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LanguageConfig{}, fmt.Errorf("begin create config: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		currentID      string
		currentVersion int
		hasActive      bool
	)
	err = tx.QueryRow(ctx, `
SELECT id, version
FROM voice_session_language_configs
WHERE session_id = $1 AND status = 'active'
FOR UPDATE`, input.SessionID).Scan(&currentID, &currentVersion)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// first config for this session
	case err != nil:
		return LanguageConfig{}, fmt.Errorf("lock active config: %w", err)
	default:
		hasActive = true
	}

	if input.ExpectedVersion != nil {
		if !hasActive || currentVersion != *input.ExpectedVersion {
			return LanguageConfig{}, ErrVersionConflict
		}
	}

	now := s.clock.Now().UTC()
	nextVersion := 1
	if hasActive {
		nextVersion = currentVersion + 1
		if _, err := tx.Exec(ctx, `
UPDATE voice_session_language_configs
SET status = 'superseded', effective_until = $2, updated_at = $2
WHERE id = $1`, currentID, now); err != nil {
			return LanguageConfig{}, fmt.Errorf("supersede active config: %w", err)
		}
	}

	id := ulid.Make().String()
	var idempotency any
	if input.IdempotencyKey != "" {
		idempotency = input.IdempotencyKey
	}

	_, err = tx.Exec(ctx, `
INSERT INTO voice_session_language_configs (
    id, session_id, version, language_pairs, status,
    effective_from, effective_until, created_by, idempotency_key, created_at, updated_at
) VALUES ($1,$2,$3,$4,'active',$5,NULL,$6,$7,$5,$5)`,
		id, input.SessionID, nextVersion, pairsJSON, now, input.CreatedBy, idempotency,
	)
	if err != nil {
		if mapped := mapInsertUniqueViolation(err); mapped != nil {
			return LanguageConfig{}, mapped
		}
		return LanguageConfig{}, fmt.Errorf("insert active config: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return LanguageConfig{}, fmt.Errorf("commit create config: %w", err)
	}

	return LanguageConfig{
		ID:             id,
		SessionID:      input.SessionID,
		Version:        nextVersion,
		LanguagePairs:  append([]LanguagePair(nil), input.LanguagePairs...),
		Status:         StatusActive,
		EffectiveFrom:  now,
		EffectiveUntil: nil,
		CreatedBy:      input.CreatedBy,
		CreatedAt:      now,
	}, nil
}

func (s *PostgresStore) ListConfigs(ctx context.Context, query ListConfigsQuery) ([]LanguageConfig, string, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	args := []any{query.SessionID}
	sql := `
SELECT id, session_id, version, language_pairs, status,
       effective_from, effective_until, created_by, created_at
FROM voice_session_language_configs
WHERE session_id = $1`
	if query.Cursor != "" {
		version, err := strconv.Atoi(query.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("%w: invalid cursor", ErrInvalidRequest)
		}
		args = append(args, version)
		sql += fmt.Sprintf(` AND version < $%d`, len(args))
	}
	args = append(args, limit+1)
	sql += fmt.Sprintf(` ORDER BY version DESC LIMIT $%d`, len(args))

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list configs: %w", err)
	}
	defer rows.Close()

	items := make([]LanguageConfig, 0, limit)
	for rows.Next() {
		cfg, err := scanConfigRow(rows)
		if err != nil {
			return nil, "", err
		}
		items = append(items, cfg)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(items) > limit {
		items = items[:limit]
		nextCursor = strconv.Itoa(items[len(items)-1].Version)
	}
	return items, nextCursor, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *PostgresStore) scanConfig(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, sql string, args ...any) (LanguageConfig, error) {
	row := q.QueryRow(ctx, sql, args...)
	cfg, err := scanConfigRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return LanguageConfig{}, ErrNoActiveConfig
	}
	return cfg, err
}

func scanConfigRow(row rowScanner) (LanguageConfig, error) {
	var (
		cfg      LanguageConfig
		pairsRaw []byte
	)
	if err := row.Scan(
		&cfg.ID,
		&cfg.SessionID,
		&cfg.Version,
		&pairsRaw,
		&cfg.Status,
		&cfg.EffectiveFrom,
		&cfg.EffectiveUntil,
		&cfg.CreatedBy,
		&cfg.CreatedAt,
	); err != nil {
		return LanguageConfig{}, err
	}
	if err := json.Unmarshal(pairsRaw, &cfg.LanguagePairs); err != nil {
		return LanguageConfig{}, fmt.Errorf("unmarshal language_pairs: %w", err)
	}
	cfg.EffectiveFrom = cfg.EffectiveFrom.UTC()
	cfg.CreatedAt = cfg.CreatedAt.UTC()
	if cfg.EffectiveUntil != nil {
		t := cfg.EffectiveUntil.UTC()
		cfg.EffectiveUntil = &t
	}
	return cfg, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// mapInsertUniqueViolation classifies Postgres unique violations from CreateActiveConfig.
// Only the idempotency index maps to ErrIdempotencyConflict; active/version races map to
// ErrVersionConflict so a lost first-create race is not mislabeled (and retried forever
// against a key that was never stored).
func mapInsertUniqueViolation(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return nil
	}
	switch pgErr.ConstraintName {
	case "idx_lang_config_idempotency":
		return ErrIdempotencyConflict
	default:
		return ErrVersionConflict
	}
}
