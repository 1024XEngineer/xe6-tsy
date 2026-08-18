package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	sessionColumns = `id, account_id, status, audio_config, capabilities,
		started_at, ended_at, created_at`
	startOperationColumns = `operation_id, session_id, account_id,
		idempotency_key, request_hash, status, compensation_claim_id,
		created_at, updated_at`
	endIntentColumns = `session_id, account_id, reason, idempotency_key,
		request_hash, trace_id, requested_at, completed_at, retry_count,
		last_error, next_attempt_at, recovery_owner, recovery_lease_expires_at`
)

// PostgresRepository persists the business session and its durable operation
// identities. Every cross-instance lifecycle mutation locks voice_sessions
// before reading an operation or intent row.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

var _ Repository = (*PostgresRepository)(nil)
var _ SessionReader = (*PostgresRepository)(nil)

func (r *PostgresRepository) Create(
	ctx context.Context,
	params CreateParams,
) (VoiceSession, bool, error) {
	if err := r.ready(); err != nil {
		return VoiceSession{}, false, err
	}
	if params.ID == "" || params.AccountID == "" ||
		params.IdempotencyKey == "" || params.RequestHash == "" ||
		params.CreatedAt.IsZero() {
		return VoiceSession{}, false, ErrInvalidRequest
	}
	audioConfig, err := json.Marshal(params.AudioConfig)
	if err != nil {
		return VoiceSession{}, false, fmt.Errorf("sessions postgres marshal audio config: %w", err)
	}
	capabilities, err := json.Marshal(params.Capabilities)
	if err != nil {
		return VoiceSession{}, false, fmt.Errorf("sessions postgres marshal capabilities: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return VoiceSession{}, false, postgresError("begin create", err)
	}
	defer tx.Rollback(ctx)

	stored, found, err := findCreateRequest(ctx, tx, params.AccountID, params.IdempotencyKey)
	if err != nil {
		return VoiceSession{}, false, postgresError("read create request", err)
	}
	if found {
		if stored.requestHash != params.RequestHash {
			return VoiceSession{}, false, ErrIdempotencyKeyConflict
		}
		session, err := getSessionByOwner(
			ctx, tx, params.AccountID, stored.sessionID, false,
		)
		if err == nil && params.DeviceID != "" {
			err = verifyDeviceSession(ctx, tx, params.DeviceID, params.AccountID, stored.sessionID)
		}
		return session, true, postgresError("replay create", err)
	}
	if params.DeviceID != "" {
		if err := verifyActiveDeviceBinding(ctx, tx, params.DeviceID, params.AccountID); err != nil {
			return VoiceSession{}, false, postgresError("verify device binding", err)
		}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO voice_sessions (
			id, account_id, status, audio_config, capabilities, created_at
		) VALUES ($1, $2, $3, $4, $5, $6)`,
		params.ID, params.AccountID, StatusCreated, audioConfig, capabilities,
		params.CreatedAt.UTC())
	if err != nil {
		if constraintName(err) == "voice_sessions_account_id_fkey" {
			return VoiceSession{}, false, ErrVoiceSessionNotFound
		}
		return VoiceSession{}, false, postgresError("insert session", err)
	}
	if params.DeviceID != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO lingow_device_voice_sessions (device_id, session_id, account_id)
			VALUES ($1, $2, $3)`, params.DeviceID, params.ID, params.AccountID); err != nil {
			return VoiceSession{}, false, postgresError("bind device session", err)
		}
	}

	var inserted int
	err = tx.QueryRow(ctx, `
		INSERT INTO voice_session_create_requests (
			account_id, idempotency_key, request_hash, session_id, created_at
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (account_id, idempotency_key) DO NOTHING
		RETURNING 1`,
		params.AccountID, params.IdempotencyKey, params.RequestHash, params.ID,
		params.CreatedAt.UTC()).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		// The losing transaction must discard its session before it can read
		// the winner. This prevents an unbound session from surviving a race.
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			return VoiceSession{}, false, postgresError("rollback create race", rollbackErr)
		}
		return r.replayCreate(ctx, params.AccountID, params.IdempotencyKey, params.RequestHash)
	}
	if err != nil {
		return VoiceSession{}, false, postgresError("insert create request", err)
	}
	session, err := getSessionByOwner(ctx, tx, params.AccountID, params.ID, false)
	if err != nil {
		return VoiceSession{}, false, postgresError("read created session", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return VoiceSession{}, false, postgresError("commit create", err)
	}
	return session, false, nil
}

func verifyActiveDeviceBinding(ctx context.Context, db queryRower, deviceID, accountID string) error {
	var found string
	err := db.QueryRow(ctx, `SELECT device_id FROM lingow_devices WHERE device_id=$1 AND account_id=$2 AND status='active'`, deviceID, accountID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnauthorized
	}
	return err
}

func verifyDeviceSession(ctx context.Context, db queryRower, deviceID, accountID, sessionID string) error {
	var found bool
	err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM lingow_device_voice_sessions WHERE device_id=$1 AND account_id=$2 AND session_id=$3)`, deviceID, accountID, sessionID).Scan(&found)
	if err != nil {
		return err
	}
	if !found {
		return ErrUnauthorized
	}
	return nil
}

func (r *PostgresRepository) replayCreate(
	ctx context.Context,
	accountID string,
	idempotencyKey string,
	requestHash string,
) (VoiceSession, bool, error) {
	stored, found, err := findCreateRequest(ctx, r.pool, accountID, idempotencyKey)
	if err != nil {
		return VoiceSession{}, false, postgresError("read winning create request", err)
	}
	if !found {
		return VoiceSession{}, false, fmt.Errorf(
			"sessions postgres resolve create race: %w", ErrConcurrentTransition,
		)
	}
	if stored.requestHash != requestHash {
		return VoiceSession{}, false, ErrIdempotencyKeyConflict
	}
	session, err := getSessionByOwner(
		ctx, r.pool, accountID, stored.sessionID, false,
	)
	return session, true, postgresError("read winning session", err)
}

func (r *PostgresRepository) GetOwned(
	ctx context.Context,
	accountID string,
	sessionID string,
) (VoiceSession, error) {
	if err := r.ready(); err != nil {
		return VoiceSession{}, err
	}
	if accountID == "" || sessionID == "" {
		return VoiceSession{}, ErrInvalidRequest
	}
	session, err := getSessionForActor(
		ctx, r.pool, accountID, sessionID, false,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return VoiceSession{}, ErrVoiceSessionNotFound
	}
	return session, postgresError("get owned session", err)
}

func (r *PostgresRepository) GetSession(
	ctx context.Context,
	sessionID string,
) (SessionSnapshot, error) {
	if err := r.ready(); err != nil {
		return SessionSnapshot{}, err
	}
	if sessionID == "" {
		return SessionSnapshot{}, ErrInvalidRequest
	}
	session, err := getSessionTrusted(ctx, r.pool, sessionID, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionSnapshot{}, ErrVoiceSessionNotFound
	}
	if err != nil {
		return SessionSnapshot{}, postgresError("get trusted session", err)
	}
	return SessionSnapshot{
		SessionID: session.ID, AccountID: session.AccountID, Status: session.Status,
		AudioConfig: session.AudioConfig, Capabilities: session.Capabilities,
		StartedAt: session.StartedAt, EndedAt: session.EndedAt,
	}, nil
}

func (r *PostgresRepository) List(
	ctx context.Context,
	filter ListFilter,
) (ListPage, error) {
	if err := r.ready(); err != nil {
		return ListPage{}, err
	}
	if filter.AccountID == "" || filter.Limit < 1 || filter.Limit > maxListLimit {
		return ListPage{}, ErrInvalidRequest
	}
	if filter.Status != nil && !filter.Status.Valid() {
		return ListPage{}, ErrInvalidRequest
	}

	args := []any{filter.AccountID}
	where := []string{
		"account_id IN (SELECT account_id FROM lingow_account_lineage($1))",
	}
	if filter.Status != nil {
		args = append(args, *filter.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if filter.Cursor != "" {
		cursor, err := decodeSessionListCursor(filter.Cursor)
		if err != nil {
			return ListPage{}, err
		}
		args = append(args, cursor.CreatedAt, cursor.ID)
		where = append(where, fmt.Sprintf(
			"(created_at, id) < ($%d, $%d)", len(args)-1, len(args),
		))
	}
	args = append(args, filter.Limit+1)
	query := `
		SELECT id, account_id, status, started_at, ended_at, created_at
		FROM voice_sessions
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY created_at DESC, id DESC
		LIMIT $` + fmt.Sprint(len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return ListPage{}, postgresError("list sessions", err)
	}
	defer rows.Close()

	items := make([]VoiceSessionListItem, 0, filter.Limit)
	for rows.Next() {
		var item VoiceSessionListItem
		var status string
		if err := rows.Scan(
			&item.ID, &item.AccountID, &status, &item.StartedAt,
			&item.EndedAt, &item.CreatedAt,
		); err != nil {
			return ListPage{}, postgresError("scan listed session", err)
		}
		item.Status = Status(status)
		if !item.Status.Valid() {
			return ListPage{}, fmt.Errorf(
				"sessions postgres scan listed session: invalid status %q", status,
			)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ListPage{}, postgresError("iterate sessions", err)
	}

	page := ListPage{Sessions: items}
	if len(items) > filter.Limit {
		items = items[:filter.Limit]
		page.Sessions = items
		last := items[len(items)-1]
		cursor, err := encodeSessionListCursor(last.CreatedAt, last.ID)
		if err != nil {
			return ListPage{}, fmt.Errorf("sessions postgres encode list cursor: %w", err)
		}
		page.NextCursor = &cursor
	}
	return page, nil
}

type createRequestRecord struct {
	requestHash string
	sessionID   string
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func findCreateRequest(
	ctx context.Context,
	db queryRower,
	accountID string,
	idempotencyKey string,
) (createRequestRecord, bool, error) {
	var stored createRequestRecord
	err := db.QueryRow(ctx, `
		SELECT request_hash, session_id
		FROM voice_session_create_requests
		WHERE account_id = $1 AND idempotency_key = $2`,
		accountID, idempotencyKey).Scan(&stored.requestHash, &stored.sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return createRequestRecord{}, false, nil
	}
	return stored, err == nil, err
}

// getSessionForActor authorizes the current account through the lineage
// function while preserving the immutable owner returned from voice_sessions.
func getSessionForActor(
	ctx context.Context,
	db queryRower,
	actorAccountID string,
	sessionID string,
	forUpdate bool,
) (VoiceSession, error) {
	query := `SELECT ` + sessionColumns + `
		FROM voice_sessions
		WHERE id = $1
		  AND account_id IN (
			SELECT account_id FROM lingow_account_lineage($2)
		  )`
	return querySession(ctx, db, query, []any{sessionID, actorAccountID}, forUpdate)
}

// getSessionByOwner performs an exact owner read after authorization has
// already resolved the immutable account used by operation and intent rows.
func getSessionByOwner(
	ctx context.Context,
	db queryRower,
	ownerAccountID string,
	sessionID string,
	forUpdate bool,
) (VoiceSession, error) {
	query := `SELECT ` + sessionColumns + `
		FROM voice_sessions
		WHERE id = $1 AND account_id = $2`
	return querySession(ctx, db, query, []any{sessionID, ownerAccountID}, forUpdate)
}

// getSessionTrusted is reserved for the internal SessionReader boundary, which
// is explicitly not an account authorization path.
func getSessionTrusted(
	ctx context.Context,
	db queryRower,
	sessionID string,
	forUpdate bool,
) (VoiceSession, error) {
	query := `SELECT ` + sessionColumns + ` FROM voice_sessions WHERE id = $1`
	return querySession(ctx, db, query, []any{sessionID}, forUpdate)
}

func querySession(
	ctx context.Context,
	db queryRower,
	query string,
	args []any,
	forUpdate bool,
) (VoiceSession, error) {
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var session VoiceSession
	var status string
	err := db.QueryRow(ctx, query, args...).Scan(
		&session.ID, &session.AccountID, &status, &session.AudioConfig,
		&session.Capabilities, &session.StartedAt, &session.EndedAt,
		&session.CreatedAt,
	)
	if err != nil {
		return VoiceSession{}, err
	}
	session.Status = Status(status)
	if !session.Status.Valid() {
		return VoiceSession{}, fmt.Errorf("invalid persisted session status %q", status)
	}
	return session, nil
}

func scanStartOperation(row pgx.Row) (StartOperation, error) {
	var operation StartOperation
	var status string
	err := row.Scan(
		&operation.ID, &operation.SessionID, &operation.AccountID,
		&operation.IdempotencyKey, &operation.RequestHash, &status,
		&operation.CompensationClaimID, &operation.CreatedAt, &operation.UpdatedAt,
	)
	if err != nil {
		return StartOperation{}, err
	}
	operation.Status = StartOperationStatus(status)
	if !operation.Status.Valid() {
		return StartOperation{}, fmt.Errorf(
			"invalid persisted start operation status %q", status,
		)
	}
	return operation, nil
}

func scanEndIntent(row pgx.Row) (EndIntent, error) {
	var intent EndIntent
	var reason string
	err := row.Scan(
		&intent.SessionID, &intent.AccountID, &reason, &intent.IdempotencyKey,
		&intent.RequestHash, &intent.TraceID, &intent.RequestedAt,
		&intent.CompletedAt, &intent.RetryCount, &intent.LastError,
		&intent.NextAttemptAt, &intent.RecoveryOwner, &intent.LeaseExpiresAt,
	)
	if err != nil {
		return EndIntent{}, err
	}
	intent.Reason = EndReason(reason)
	if !intent.Reason.Valid() {
		return EndIntent{}, fmt.Errorf("invalid persisted end reason %q", reason)
	}
	return intent, nil
}

func (r *PostgresRepository) ready() error {
	if r == nil || r.pool == nil {
		return ErrInvalidDependency
	}
	return nil
}

func constraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

func postgresError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("sessions postgres %s: %w", operation, err)
}

func validTimestamp(at time.Time) bool {
	return !at.IsZero() && at.Location() != nil
}
