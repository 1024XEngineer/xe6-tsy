package accounts

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
	"github.com/oklog/ulid/v2"
)

// PostgresRepository owns durable account and authentication session state.
// Secrets are stored only as hashes; access-token verification uses the signed
// token plus the active-session lookup below.
type PostgresRepository struct{ pool *pgxpool.Pool }

const insertRegisteredAccountSQL = `INSERT INTO lingow_accounts (id, kind, phone_hash, created_at) VALUES ($1,$2,$3,$4) ON CONFLICT (phone_hash) WHERE phone_hash IS NOT NULL DO NOTHING`
const revokeActiveSessionSQL = `UPDATE lingow_auth_sessions SET revoked_at=CURRENT_TIMESTAMP WHERE id=$1 AND revoked_at IS NULL`

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) CreateAnonymous(ctx context.Context) (Account, error) {
	now := time.Now().UTC()
	account := Account{ID: "acct_" + ulid.Make().String(), Kind: AccountKindAnonymous, CreatedAt: now}
	_, err := r.pool.Exec(ctx, `INSERT INTO lingow_accounts (id, kind, created_at) VALUES ($1,$2,$3)`, account.ID, string(account.Kind), now)
	return account, mapError(err)
}

func (r *PostgresRepository) GetAccount(ctx context.Context, id string) (Account, error) {
	var account Account
	var kind string
	err := r.pool.QueryRow(ctx, `SELECT id, kind, created_at FROM lingow_accounts WHERE id=$1 AND merged_into IS NULL`, id).Scan(&account.ID, &kind, &account.CreatedAt)
	if err != nil {
		return Account{}, mapError(err)
	}
	account.Kind = AccountKind(kind)
	return account, nil
}

func (r *PostgresRepository) CreateChallenge(ctx context.Context, challenge PhoneChallenge) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO lingow_phone_challenges (id, phone_hash, code_hash, expires_at, created_at) VALUES ($1,$2,$3,$4,$5)`, challenge.ID, challenge.PhoneHash, challenge.CodeHash, challenge.ExpiresAt, challenge.CreatedAt)
	return mapError(err)
}

func (r *PostgresRepository) GetChallenge(ctx context.Context, id string) (PhoneChallenge, error) {
	var challenge PhoneChallenge
	err := r.pool.QueryRow(ctx, `SELECT id, phone_hash, code_hash, expires_at, used_at, created_at FROM lingow_phone_challenges WHERE id=$1`, id).Scan(&challenge.ID, &challenge.PhoneHash, &challenge.CodeHash, &challenge.ExpiresAt, &challenge.UsedAt, &challenge.CreatedAt)
	return challenge, mapError(err)
}

func (r *PostgresRepository) ConsumeChallenge(ctx context.Context, id, code string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var codeHash string
	var expires time.Time
	var usedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT code_hash, expires_at, used_at FROM lingow_phone_challenges WHERE id=$1 FOR UPDATE`, id).Scan(&codeHash, &expires, &usedAt); err != nil {
		return mapError(err)
	}
	if usedAt != nil || !time.Now().UTC().Before(expires) {
		return domain.ErrUnauthorized
	}
	provided := hashValue(code)
	if subtle.ConstantTimeCompare([]byte(provided), []byte(codeHash)) != 1 {
		return domain.ErrUnauthorized
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE lingow_phone_challenges SET used_at=$2 WHERE id=$1`, id, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

func (r *PostgresRepository) FindOrCreateByPhoneHash(ctx context.Context, phoneHash string) (Account, error) {
	var account Account
	var kind string
	err := r.pool.QueryRow(ctx, `SELECT id, kind, created_at FROM lingow_accounts WHERE phone_hash=$1 AND merged_into IS NULL`, phoneHash).Scan(&account.ID, &kind, &account.CreatedAt)
	if err == nil {
		account.Kind = AccountKind(kind)
		return account, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Account{}, mapError(err)
	}
	account = Account{ID: "acct_" + ulid.Make().String(), Kind: AccountKindRegistered, CreatedAt: time.Now().UTC()}
	// phone_hash is protected by a partial unique index (anonymous rows have a
	// NULL phone_hash), so the conflict target must carry the same predicate for
	// PostgreSQL to infer that index.
	_, err = r.pool.Exec(ctx, insertRegisteredAccountSQL, account.ID, string(account.Kind), phoneHash, account.CreatedAt)
	if err != nil {
		return Account{}, mapError(err)
	}
	return r.GetAccountByPhoneOrID(ctx, phoneHash, account.ID)
}

func (r *PostgresRepository) GetAccountByPhoneOrID(ctx context.Context, phoneHash, fallbackID string) (Account, error) {
	var account Account
	var kind string
	err := r.pool.QueryRow(ctx, `SELECT id, kind, created_at FROM lingow_accounts WHERE (phone_hash=$1 OR id=$2) AND merged_into IS NULL ORDER BY CASE WHEN phone_hash=$1 THEN 0 ELSE 1 END LIMIT 1`, phoneHash, fallbackID).Scan(&account.ID, &kind, &account.CreatedAt)
	if err != nil {
		return Account{}, mapError(err)
	}
	account.Kind = AccountKind(kind)
	return account, nil
}

func (r *PostgresRepository) BindAnonymous(ctx context.Context, anonymousID, registeredID string) (Account, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback(ctx)
	var kind string
	if err := tx.QueryRow(ctx, `SELECT kind FROM lingow_accounts WHERE id=$1 AND merged_into IS NULL FOR UPDATE`, anonymousID).Scan(&kind); err != nil {
		return Account{}, mapError(err)
	}
	if kind != string(AccountKindAnonymous) {
		return Account{}, domain.ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE lingow_auth_sessions SET account_id=$2 WHERE account_id=$1`, anonymousID, registeredID); err != nil {
		return Account{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE lingow_accounts SET merged_into=$2 WHERE id=$1`, anonymousID, registeredID); err != nil {
		return Account{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Account{}, err
	}
	return r.GetAccount(ctx, registeredID)
}

func (r *PostgresRepository) CreateSession(ctx context.Context, session Session) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO lingow_auth_sessions (id, account_id, refresh_hash, expires_at, created_at) VALUES ($1,$2,$3,$4,$5)`, session.ID, session.AccountID, session.RefreshHash, session.ExpiresAt, session.CreatedAt)
	return mapError(err)
}

func (r *PostgresRepository) GetSessionByRefreshHash(ctx context.Context, hash string) (Session, error) {
	var session Session
	err := r.pool.QueryRow(ctx, `SELECT id, account_id, refresh_hash, expires_at, revoked_at, created_at FROM lingow_auth_sessions WHERE refresh_hash=$1 AND revoked_at IS NULL AND expires_at > CURRENT_TIMESTAMP`, hash).Scan(&session.ID, &session.AccountID, &session.RefreshHash, &session.ExpiresAt, &session.RevokedAt, &session.CreatedAt)
	return session, mapError(err)
}

func (r *PostgresRepository) RevokeSession(ctx context.Context, id string) error {
	// Conditional revocation makes refresh-token rotation single-winner under
	// concurrent requests: once one caller revokes the session, a replay cannot
	// mint a second successor token.
	result, err := r.pool.Exec(ctx, revokeActiveSessionSQL, id)
	if err != nil {
		return mapError(err)
	}
	return revokeSessionResult(result.RowsAffected())
}

func revokeSessionResult(rowsAffected int64) error {
	if rowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) SessionActive(ctx context.Context, id string) (bool, error) {
	var active bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lingow_auth_sessions WHERE id=$1 AND revoked_at IS NULL AND expires_at > CURRENT_TIMESTAMP)`, id).Scan(&active)
	return active, mapError(err)
}

// SessionActiveForAccount validates the session's lifecycle and its current
// ownership together. The account predicate is required because anonymous
// account binding can move existing sessions to a registered account; a token
// issued before that move must no longer authorize as the old subject.
func (r *PostgresRepository) SessionActiveForAccount(ctx context.Context, sessionID, accountID string) (bool, error) {
	var active bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lingow_auth_sessions WHERE id=$1 AND account_id=$2 AND revoked_at IS NULL AND expires_at > CURRENT_TIMESTAMP)`, sessionID, accountID).Scan(&active)
	return active, mapError(err)
}

// AccountIDForSession is shared by usage, language, turns, and delivery ownership adapters.
// It returns the canonical account after an anonymous account has been merged.
func (r *PostgresRepository) AccountIDForSession(ctx context.Context, sessionID string) (string, error) {
	var accountID string
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(owner.merged_into, owner.id)
		FROM voice_sessions AS sessions
		JOIN lingow_accounts AS owner ON owner.id = sessions.account_id
		WHERE sessions.id = $1`, sessionID).Scan(&accountID)
	return accountID, mapError(err)
}

func mapError(err error) error {
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
		}
	}
	return fmt.Errorf("postgres account operation: %w", err)
}
