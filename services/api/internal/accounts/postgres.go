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

const insertRegisteredAccountSQL = `INSERT INTO lingow_accounts (id, kind, phone_hash_v2, created_at) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`
const insertSessionSQL = `INSERT INTO lingow_auth_sessions (id, account_id, refresh_hash, expires_at, created_at) VALUES ($1,$2,$3,$4,$5)`
const revokeActiveSessionSQL = `UPDATE lingow_auth_sessions SET revoked_at=CURRENT_TIMESTAMP WHERE id=$1 AND revoked_at IS NULL`
const rotateActiveSessionSQL = `UPDATE lingow_auth_sessions SET revoked_at=CURRENT_TIMESTAMP WHERE id=$1 AND account_id=$2 AND revoked_at IS NULL AND expires_at > CURRENT_TIMESTAMP`
const challengeAdvisoryLockSQL = `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`
const challengeRateQuerySQL = `
	SELECT MAX(created_at), COUNT(*) FILTER (WHERE created_at > $2)
	FROM lingow_phone_challenges
	WHERE phone_hash = $1
	   OR (digest_version = 1 AND phone_hash = $3)`

const (
	phoneChallengeCooldown                 = time.Minute
	phoneChallengeWindow                   = time.Hour
	phoneChallengeWindowMaxSends           = 5
	defaultPhoneChallengeMaxAttempts int16 = 5
)

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
	if challenge.MaxAttempts == 0 {
		challenge.MaxAttempts = defaultPhoneChallengeMaxAttempts
	}
	if challenge.CreatedAt.IsZero() {
		challenge.CreatedAt = time.Now().UTC()
	}
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		// Advisory locking serializes requests for the same private phone hash
		// across API instances, including when no challenge row exists yet.
		if _, err := tx.Exec(ctx, challengeAdvisoryLockSQL, challenge.PhoneHash); err != nil {
			return err
		}

		var latest *time.Time
		var sends int64
		if err := tx.QueryRow(ctx, challengeRateQuerySQL,
			challenge.PhoneHash, challenge.CreatedAt.Add(-phoneChallengeWindow), challenge.LegacyRateLimitHash,
		).Scan(&latest, &sends); err != nil {
			return err
		}
		if latest != nil && challenge.CreatedAt.Before(latest.Add(phoneChallengeCooldown)) {
			return domain.ErrRateLimited
		}
		if sends >= phoneChallengeWindowMaxSends {
			return domain.ErrRateLimited
		}

		_, err := tx.Exec(ctx, `
			INSERT INTO lingow_phone_challenges
				(id, phone_hash, legacy_phone_hash, code_hash, digest_version, expires_at, created_at, attempts, max_attempts)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			challenge.ID, challenge.PhoneHash, challenge.LegacyPhoneHash, challenge.CodeHash, challenge.DigestVersion,
			challenge.ExpiresAt, challenge.CreatedAt, challenge.Attempts, challenge.MaxAttempts)
		return err
	})
	return mapError(err)
}

func (r *PostgresRepository) ConsumeChallenge(ctx context.Context, id, code string) (PhoneChallenge, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return PhoneChallenge{}, err
	}
	defer tx.Rollback(ctx)

	var challenge PhoneChallenge
	err = tx.QueryRow(ctx, `
		SELECT id, phone_hash, legacy_phone_hash, code_hash, digest_version, expires_at, used_at, created_at,
			attempts, max_attempts, last_attempt_at
		FROM lingow_phone_challenges
		WHERE id = $1
		FOR UPDATE`, id).Scan(
		&challenge.ID, &challenge.PhoneHash, &challenge.LegacyPhoneHash, &challenge.CodeHash, &challenge.DigestVersion, &challenge.ExpiresAt,
		&challenge.UsedAt, &challenge.CreatedAt, &challenge.Attempts,
		&challenge.MaxAttempts, &challenge.LastAttemptAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PhoneChallenge{}, domain.ErrUnauthorized
		}
		return PhoneChallenge{}, mapError(err)
	}

	now := time.Now().UTC()
	if challenge.UsedAt != nil || !now.Before(challenge.ExpiresAt) || challenge.Attempts >= challenge.MaxAttempts {
		return PhoneChallenge{}, domain.ErrUnauthorized
	}
	if challenge.DigestVersion != 2 || subtle.ConstantTimeCompare([]byte(code), []byte(challenge.CodeHash)) != 1 {
		// This branch intentionally commits before returning. A rollback here
		// would make unlimited guesses possible against the same challenge.
		if _, err := tx.Exec(ctx, `
			UPDATE lingow_phone_challenges
			SET attempts = attempts + 1, last_attempt_at = $2
			WHERE id = $1`, id, now); err != nil {
			return PhoneChallenge{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return PhoneChallenge{}, err
		}
		return PhoneChallenge{}, domain.ErrUnauthorized
	}

	if _, err := tx.Exec(ctx, `
		UPDATE lingow_phone_challenges
		SET used_at = $2, last_attempt_at = $2
		WHERE id = $1`, id, now); err != nil {
		return PhoneChallenge{}, err
	}
	challenge.UsedAt = &now
	challenge.LastAttemptAt = &now
	if err := tx.Commit(ctx); err != nil {
		return PhoneChallenge{}, err
	}
	return challenge, nil
}

func (r *PostgresRepository) RestoreChallenge(ctx context.Context, id string) error {
	// A consumed row cannot be claimed by another verifier. Clearing only used
	// rows therefore compensates a later persistence failure without turning an
	// incorrect-code attempt into a reusable challenge.
	_, err := r.pool.Exec(ctx, `UPDATE lingow_phone_challenges SET used_at=NULL WHERE id=$1 AND used_at IS NOT NULL`, id)
	return mapError(err)
}

func (r *PostgresRepository) FindOrCreateByPhoneHashes(ctx context.Context, phoneHash, legacyPhoneHash string) (Account, error) {
	var account Account
	var kind string
	err := r.pool.QueryRow(ctx, `SELECT id, kind, created_at FROM lingow_accounts WHERE phone_hash_v2=$1 AND merged_into IS NULL`, phoneHash).Scan(&account.ID, &kind, &account.CreatedAt)
	if err == nil {
		account.Kind = AccountKind(kind)
		return account, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Account{}, mapError(err)
	}
	// Existing accounts are located through their historical SHA-256 digest and
	// upgraded in place. Once the keyed digest exists, remove the legacy value so
	// the weak deterministic identifier is retained only until that first login.
	err = r.pool.QueryRow(ctx, `SELECT id, kind, created_at FROM lingow_accounts WHERE phone_hash=$1 AND merged_into IS NULL`, legacyPhoneHash).Scan(&account.ID, &kind, &account.CreatedAt)
	if err == nil {
		account.Kind = AccountKind(kind)
		if _, err := r.pool.Exec(ctx, `UPDATE lingow_accounts SET phone_hash_v2=$2, phone_hash=NULL WHERE id=$1 AND phone_hash_v2 IS NULL`, account.ID, phoneHash); err != nil {
			return Account{}, mapError(err)
		}
		return account, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Account{}, mapError(err)
	}
	account = Account{ID: "acct_" + ulid.Make().String(), Kind: AccountKindRegistered, CreatedAt: time.Now().UTC()}
	// New accounts never persist the weak deterministic SHA-256 value. The legacy
	// hash is used only to locate and upgrade pre-v2 accounts above.
	_, err = r.pool.Exec(ctx, insertRegisteredAccountSQL, account.ID, string(account.Kind), phoneHash, account.CreatedAt)
	if err != nil {
		return Account{}, mapError(err)
	}
	return r.GetAccountByPhoneHashesOrID(ctx, phoneHash, legacyPhoneHash, account.ID)
}

func (r *PostgresRepository) GetAccountByPhoneHashesOrID(ctx context.Context, phoneHash, legacyPhoneHash, fallbackID string) (Account, error) {
	var account Account
	var kind string
	err := r.pool.QueryRow(ctx, `SELECT id, kind, created_at FROM lingow_accounts WHERE (phone_hash_v2=$1 OR phone_hash=$2 OR id=$3) AND merged_into IS NULL ORDER BY CASE WHEN phone_hash_v2=$1 THEN 0 WHEN phone_hash=$2 THEN 1 ELSE 2 END LIMIT 1`, phoneHash, legacyPhoneHash, fallbackID).Scan(&account.ID, &kind, &account.CreatedAt)
	if err != nil {
		return Account{}, mapError(err)
	}
	account.Kind = AccountKind(kind)
	return account, nil
}

func (r *PostgresRepository) BindAnonymous(ctx context.Context, anonymousID, registeredID string) (Account, error) {
	if anonymousID == "" || registeredID == "" || anonymousID == registeredID {
		return Account{}, domain.ErrConflict
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback(ctx)
	var anonymousKind string
	var mergedInto *string
	if err := tx.QueryRow(ctx, `SELECT kind, merged_into FROM lingow_accounts WHERE id=$1 FOR UPDATE`, anonymousID).Scan(&anonymousKind, &mergedInto); err != nil {
		return Account{}, mapError(err)
	}
	if mergedInto != nil {
		if *mergedInto == registeredID {
			if err := tx.Commit(ctx); err != nil {
				return Account{}, mapError(err)
			}
			return r.GetAccount(ctx, registeredID)
		}
		return Account{}, domain.ErrConflict
	}
	if anonymousKind != string(AccountKindAnonymous) {
		return Account{}, domain.ErrConflict
	}
	var registeredKind string
	if err := tx.QueryRow(ctx, `SELECT kind FROM lingow_accounts WHERE id=$1 AND merged_into IS NULL FOR UPDATE`, registeredID).Scan(&registeredKind); err != nil {
		return Account{}, mapError(err)
	}
	if registeredKind != string(AccountKindRegistered) {
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

// BindAnonymousAndCreateSession is the login-path variant of BindAnonymous.
// All ownership changes and the first registered session are committed by one
// transaction, so a session insert failure rolls the merge back.
func (r *PostgresRepository) BindAnonymousAndCreateSession(ctx context.Context, anonymousID, registeredID string, session Session) (Account, error) {
	if anonymousID == "" || registeredID == "" || anonymousID == registeredID || session.ID == "" || session.AccountID != registeredID {
		return Account{}, domain.ErrConflict
	}
	var account Account
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var anonymousKind string
		var mergedInto *string
		if err := tx.QueryRow(ctx, `SELECT kind, merged_into FROM lingow_accounts WHERE id=$1 FOR UPDATE`, anonymousID).Scan(&anonymousKind, &mergedInto); err != nil {
			return mapError(err)
		}
		if mergedInto != nil && *mergedInto != registeredID {
			return domain.ErrConflict
		}
		if mergedInto == nil && anonymousKind != string(AccountKindAnonymous) {
			return domain.ErrConflict
		}
		var registeredKind string
		var createdAt time.Time
		if err := tx.QueryRow(ctx, `SELECT kind, created_at FROM lingow_accounts WHERE id=$1 AND merged_into IS NULL FOR UPDATE`, registeredID).Scan(&registeredKind, &createdAt); err != nil {
			return mapError(err)
		}
		if registeredKind != string(AccountKindRegistered) {
			return domain.ErrConflict
		}
		if mergedInto == nil {
			if _, err := tx.Exec(ctx, `UPDATE lingow_auth_sessions SET account_id=$2 WHERE account_id=$1`, anonymousID, registeredID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE lingow_accounts SET merged_into=$2 WHERE id=$1`, anonymousID, registeredID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, insertSessionSQL, session.ID, session.AccountID, session.RefreshHash, session.ExpiresAt, session.CreatedAt); err != nil {
			return err
		}
		account = Account{ID: registeredID, Kind: AccountKindRegistered, CreatedAt: createdAt}
		return nil
	})
	if err != nil {
		return Account{}, mapError(err)
	}
	return account, nil
}

func (r *PostgresRepository) CreateSession(ctx context.Context, session Session) error {
	_, err := r.pool.Exec(ctx, insertSessionSQL, session.ID, session.AccountID, session.RefreshHash, session.ExpiresAt, session.CreatedAt)
	return mapError(err)
}

func (r *PostgresRepository) GetSessionByRefreshHash(ctx context.Context, hash string) (Session, error) {
	var session Session
	err := r.pool.QueryRow(ctx, `SELECT id, account_id, refresh_hash, expires_at, revoked_at, created_at FROM lingow_auth_sessions WHERE refresh_hash=$1 AND revoked_at IS NULL AND expires_at > CURRENT_TIMESTAMP`, hash).Scan(&session.ID, &session.AccountID, &session.RefreshHash, &session.ExpiresAt, &session.RevokedAt, &session.CreatedAt)
	return session, mapError(err)
}

func (r *PostgresRepository) RotateSession(ctx context.Context, currentSessionID string, successor Session) error {
	// Revocation and successor insertion share one transaction so an insert
	// failure rolls both changes back and a commit publishes them together.
	// The conditional update also serializes concurrent refresh attempts: only
	// the transaction that changes the active row may persist a successor.
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, rotateActiveSessionSQL, currentSessionID, successor.AccountID)
		if err != nil {
			return err
		}
		if err := revokeSessionResult(result.RowsAffected()); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, insertSessionSQL, successor.ID, successor.AccountID, successor.RefreshHash, successor.ExpiresAt, successor.CreatedAt)
		return err
	})
	return mapError(err)
}

func (r *PostgresRepository) RevokeSession(ctx context.Context, id string) error {
	// Conditional revocation reports a stale logout or replay instead of
	// silently treating an already-revoked session as active.
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

// PurgeExpiredAuthSessions revokes sessions that have passed their expiry so
// refresh lookup stays bounded and expired credentials cannot linger active.
func (r *PostgresRepository) PurgeExpiredAuthSessions(ctx context.Context) (int64, error) {
	result, err := r.pool.Exec(ctx, `
		UPDATE lingow_auth_sessions
		SET revoked_at = CURRENT_TIMESTAMP
		WHERE revoked_at IS NULL
		  AND expires_at <= CURRENT_TIMESTAMP`)
	if err != nil {
		return 0, mapError(err)
	}
	return result.RowsAffected(), nil
}

// PurgeStalePhoneChallenges removes expired and long-consumed challenges so
// verification state does not grow without bound.
func (r *PostgresRepository) PurgeStalePhoneChallenges(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention)
	result, err := r.pool.Exec(ctx, `
		DELETE FROM lingow_phone_challenges
		WHERE expires_at <= CURRENT_TIMESTAMP
		   OR (used_at IS NOT NULL AND used_at <= $1)`, cutoff)
	if err != nil {
		return 0, mapError(err)
	}
	return result.RowsAffected(), nil
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

// AccountIDForSession returns the immutable owner stored on the voice session.
func (r *PostgresRepository) AccountIDForSession(ctx context.Context, sessionID string) (string, error) {
	var accountID string
	err := r.pool.QueryRow(ctx, `SELECT account_id FROM voice_sessions WHERE id=$1`, sessionID).Scan(&accountID)
	return accountID, mapError(err)
}

// CanonicalAccountID follows an account's merge chain to its active owner.
// The visited set prevents a malformed historical cycle from looping forever.
func (r *PostgresRepository) CanonicalAccountID(ctx context.Context, accountID string) (string, error) {
	var canonicalID string
	err := r.pool.QueryRow(ctx, `
		WITH RECURSIVE ancestors AS (
			SELECT id, merged_into, ARRAY[id] AS visited
			FROM lingow_accounts
			WHERE id = $1
			UNION ALL
			SELECT parent.id, parent.merged_into, child.visited || parent.id
			FROM lingow_accounts AS parent
			JOIN ancestors AS child ON parent.id = child.merged_into
			WHERE NOT parent.id = ANY(child.visited)
		)
		SELECT id FROM ancestors
		WHERE merged_into IS NULL
		LIMIT 1`, accountID).Scan(&canonicalID)
	return canonicalID, mapError(err)
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
