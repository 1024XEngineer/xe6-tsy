//go:build integration

package accounts

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/recordstore"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const accountsTestDatabaseURL = "RECORDSTORE_TEST_DATABASE_URL"

func TestPostgresChallengeRateAndAttemptGuards(t *testing.T) {
	pool := accountsTestDatabase(t)
	repository := NewPostgresRepository(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	digester := integrationCredentialDigester(t)

	legacyPhone := "+8613800000009"
	legacyHash := hashValue(legacyPhone)
	legacyCreatedAt := now.Add(-10 * time.Second)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO lingow_phone_challenges
			(id, phone_hash, legacy_phone_hash, code_hash, digest_version, expires_at, created_at, attempts, max_attempts)
		VALUES ('challenge_legacy_cooldown', $1, $1, $2, 1, $3, $4, 0, 5)`,
		legacyHash, hashValue("123456"), legacyCreatedAt.Add(10*time.Minute), legacyCreatedAt); err != nil {
		t.Fatalf("insert legacy cooldown challenge: %v", err)
	}
	legacyReplacement := PhoneChallenge{
		ID: "challenge_legacy_replacement", PhoneHash: digester.PhoneHash(legacyPhone),
		LegacyPhoneHash: "encrypted-legacy-lookup", LegacyRateLimitHash: legacyHash,
		CodeHash: digester.CodeHash("challenge_legacy_replacement", "123456"), DigestVersion: 2,
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now, MaxAttempts: 5,
	}
	if err := repository.CreateChallenge(t.Context(), legacyReplacement); !errorsIsRateLimited(err) {
		t.Fatalf("legacy cooldown challenge error = %v, want rate limited", err)
	}

	phone := "+8613800000001"
	phoneHash := digester.PhoneHash(phone)
	first := PhoneChallenge{
		ID: "challenge_cooldown_first", PhoneHash: phoneHash, LegacyPhoneHash: hashValue(phone), CodeHash: digester.CodeHash("challenge_cooldown_first", "123456"), DigestVersion: 2,
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now, MaxAttempts: 5,
	}
	if err := repository.CreateChallenge(t.Context(), first); err != nil {
		t.Fatalf("first challenge: %v", err)
	}
	second := first
	second.ID = "challenge_cooldown_second"
	second.CreatedAt = now.Add(2 * time.Second)
	second.ExpiresAt = second.CreatedAt.Add(10 * time.Minute)
	if err := repository.CreateChallenge(t.Context(), second); !errorsIsRateLimited(err) {
		t.Fatalf("cooldown challenge error = %v, want rate limited", err)
	}

	windowPhone := "+8613800000002"
	windowPhoneHash := digester.PhoneHash(windowPhone)
	for index := 0; index < phoneChallengeWindowMaxSends; index++ {
		createdAt := now.Add(-2 * time.Minute).Add(-time.Duration(index) * time.Second)
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO lingow_phone_challenges
				(id, phone_hash, legacy_phone_hash, code_hash, digest_version, expires_at, created_at, attempts, max_attempts)
			VALUES ($1,$2,$3,$4,2,$5,$6,0,5)`,
			fmt.Sprintf("challenge_window_%d", index), windowPhoneHash, hashValue(windowPhone), digester.CodeHash(fmt.Sprintf("challenge_window_%d", index), "123456"),
			createdAt.Add(10*time.Minute), createdAt); err != nil {
			t.Fatalf("insert window challenge %d: %v", index, err)
		}
	}
	windowChallenge := PhoneChallenge{
		ID: "challenge_window_next", PhoneHash: windowPhoneHash, LegacyPhoneHash: hashValue(windowPhone), CodeHash: digester.CodeHash("challenge_window_next", "123456"), DigestVersion: 2,
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now, MaxAttempts: 5,
	}
	if err := repository.CreateChallenge(t.Context(), windowChallenge); !errorsIsRateLimited(err) {
		t.Fatalf("window challenge error = %v, want rate limited", err)
	}

	attemptPhone := "+8613800000003"
	attemptPhoneHash := digester.PhoneHash(attemptPhone)
	attemptChallenge := PhoneChallenge{
		ID: "challenge_attempts", PhoneHash: attemptPhoneHash, LegacyPhoneHash: hashValue(attemptPhone), CodeHash: digester.CodeHash("challenge_attempts", "123456"), DigestVersion: 2,
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now, MaxAttempts: 2,
	}
	if err := repository.CreateChallenge(t.Context(), attemptChallenge); err != nil {
		t.Fatalf("attempt challenge: %v", err)
	}
	if _, err := repository.ConsumeChallenge(t.Context(), attemptChallenge.ID, digester.CodeHash(attemptChallenge.ID, "000000")); !errorsIsUnauthorized(err) {
		t.Fatalf("first invalid code error = %v, want unauthorized", err)
	}
	var attempts int16
	if err := pool.QueryRow(t.Context(), `SELECT attempts FROM lingow_phone_challenges WHERE id = $1`, attemptChallenge.ID).Scan(&attempts); err != nil {
		t.Fatalf("read first attempt count: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts after first invalid code = %d, want 1", attempts)
	}
	if _, err := repository.ConsumeChallenge(t.Context(), attemptChallenge.ID, digester.CodeHash(attemptChallenge.ID, "000001")); !errorsIsUnauthorized(err) {
		t.Fatalf("second invalid code error = %v, want unauthorized", err)
	}
	if _, err := repository.ConsumeChallenge(t.Context(), attemptChallenge.ID, digester.CodeHash(attemptChallenge.ID, "123456")); !errorsIsUnauthorized(err) {
		t.Fatalf("locked correct code error = %v, want unauthorized", err)
	}

	successPhone := "+8613800000004"
	successPhoneHash := digester.PhoneHash(successPhone)
	successChallenge := PhoneChallenge{
		ID: "challenge_success", PhoneHash: successPhoneHash, LegacyPhoneHash: hashValue(successPhone), CodeHash: digester.CodeHash("challenge_success", "123456"), DigestVersion: 2,
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now, MaxAttempts: 3,
	}
	if err := repository.CreateChallenge(t.Context(), successChallenge); err != nil {
		t.Fatalf("success challenge: %v", err)
	}
	if got, err := repository.ConsumeChallenge(t.Context(), successChallenge.ID, digester.CodeHash(successChallenge.ID, "123456")); err != nil {
		t.Fatalf("correct code: %v", err)
	} else if got.PhoneHash != successPhoneHash || got.UsedAt == nil {
		t.Fatalf("consumed challenge = %#v, want phone hash and used timestamp", got)
	}
	if _, err := repository.ConsumeChallenge(t.Context(), successChallenge.ID, digester.CodeHash(successChallenge.ID, "123456")); !errorsIsUnauthorized(err) {
		t.Fatalf("replayed correct code error = %v, want unauthorized", err)
	}
}

func TestPostgresChallengeAttemptsRemainBoundedUnderConcurrency(t *testing.T) {
	pool := accountsTestDatabase(t)
	repository := NewPostgresRepository(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	digester := integrationCredentialDigester(t)
	phone := "+8613800000005"
	challenge := PhoneChallenge{
		ID: "challenge_concurrent_attempts", PhoneHash: digester.PhoneHash(phone), LegacyPhoneHash: hashValue(phone), CodeHash: digester.CodeHash("challenge_concurrent_attempts", "123456"), DigestVersion: 2,
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now, MaxAttempts: 5,
	}
	if err := repository.CreateChallenge(t.Context(), challenge); err != nil {
		t.Fatalf("concurrent challenge: %v", err)
	}

	const callers = 12
	start := make(chan struct{})
	var wait sync.WaitGroup
	errorsByCaller := make(chan error, callers)
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := repository.ConsumeChallenge(context.Background(), challenge.ID, digester.CodeHash(challenge.ID, "000000"))
			errorsByCaller <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByCaller)
	for err := range errorsByCaller {
		if !errorsIsUnauthorized(err) {
			t.Fatalf("concurrent invalid code error = %v, want unauthorized", err)
		}
	}

	var attempts int16
	if err := pool.QueryRow(t.Context(), `SELECT attempts FROM lingow_phone_challenges WHERE id = $1`, challenge.ID).Scan(&attempts); err != nil {
		t.Fatalf("read concurrent attempt count: %v", err)
	}
	if attempts != challenge.MaxAttempts {
		t.Fatalf("concurrent attempts = %d, want max %d", attempts, challenge.MaxAttempts)
	}
}

func TestPostgresPhoneDigestUpgradeRemovesLegacyCompatibilityHash(t *testing.T) {
	pool := accountsTestDatabase(t)
	repository := NewPostgresRepository(pool)
	legacyHash := "legacy-sha256-phone-digest"
	newHash := "peppered-phone-digest"
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO lingow_accounts (id, kind, phone_hash, created_at)
		VALUES ('acct_legacy_phone', 'registered', $1, $2)`, legacyHash, createdAt); err != nil {
		t.Fatalf("insert legacy account: %v", err)
	}

	account, err := repository.FindOrCreateByPhoneHashes(t.Context(), newHash, legacyHash)
	if err != nil {
		t.Fatalf("FindOrCreateByPhoneHashes() error = %v", err)
	}
	if account.ID != "acct_legacy_phone" {
		t.Fatalf("account ID = %q, want legacy account", account.ID)
	}

	var storedLegacyHash *string
	var storedNewHash string
	if err := pool.QueryRow(t.Context(), `
		SELECT phone_hash, phone_hash_v2 FROM lingow_accounts WHERE id = $1`, account.ID,
	).Scan(&storedLegacyHash, &storedNewHash); err != nil {
		t.Fatalf("read upgraded account: %v", err)
	}
	if storedLegacyHash != nil || storedNewHash != newHash {
		t.Fatalf("upgraded digests = (%v, %q), want (nil, %q)", storedLegacyHash, storedNewHash, newHash)
	}
}

func errorsIsUnauthorized(err error) bool { return errors.Is(err, domain.ErrUnauthorized) }

func errorsIsRateLimited(err error) bool { return errors.Is(err, domain.ErrRateLimited) }

func integrationCredentialDigester(t *testing.T) *CredentialDigester {
	t.Helper()
	digester, err := NewCredentialDigester("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCredentialDigester() error = %v", err)
	}
	return digester
}

func accountsTestDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv(accountsTestDatabaseURL)
	if databaseURL == "" {
		t.Skipf("set %s to run PostgreSQL integration tests", accountsTestDatabaseURL)
	}

	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse %s: %v", accountsTestDatabaseURL, err)
	}
	if !strings.HasSuffix(strings.ToLower(adminConfig.ConnConfig.Database), "_test") {
		t.Fatalf("%s must target a dedicated database ending in _test, got %q", accountsTestDatabaseURL, adminConfig.ConnConfig.Database)
	}
	adminConfig.ConnConfig.RuntimeParams["TimeZone"] = "UTC"
	admin, err := pgxpool.NewWithConfig(t.Context(), adminConfig)
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	t.Cleanup(admin.Close)

	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("create integration schema name: %v", err)
	}
	schema := fmt.Sprintf("accounts_%x", randomBytes)
	quote := `"` + strings.ReplaceAll(schema, `"`, `""`) + `"`
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+quote); err != nil {
		t.Fatalf("create integration schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+quote+" CASCADE"); err != nil {
			t.Errorf("drop integration schema: %v", err)
		}
	})

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse isolated database URL: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["TimeZone"] = "UTC"
	poolConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, "SET search_path TO "+quote)
		return err
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), poolConfig)
	if err != nil {
		t.Fatalf("open isolated PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := recordstore.Migrate(t.Context(), pool); err != nil {
		t.Fatalf("apply record-store migrations: %v", err)
	}
	return pool
}

func TestPostgresRefreshRotationIsSingleUse(t *testing.T) {
	pool := accountsTestDatabase(t)
	repository := NewPostgresRepository(pool)
	digester := integrationCredentialDigester(t)
	issuer, err := NewHMACIssuerWithAccount(
		strings.Repeat("j", 36),
		"lingow-api",
		"lingow-client",
		repository.SessionActiveForAccount,
	)
	if err != nil {
		t.Fatalf("NewHMACIssuerWithAccount() error = %v", err)
	}
	service := NewPersistentUseCases(repository, issuer, issuer, verificationSenderStub{}, digester)

	anonymous, err := service.CreateAnonymous(t.Context())
	if err != nil {
		t.Fatalf("CreateAnonymous() error = %v", err)
	}
	rotated, err := service.Refresh(t.Context(), anonymous.Tokens.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if _, err := service.Refresh(t.Context(), anonymous.Tokens.RefreshToken); !errorsIsUnauthorized(err) {
		t.Fatalf("replay Refresh() error = %v, want unauthorized", err)
	}
	if _, err := issuer.VerifyAccessToken(t.Context(), rotated.AccessToken); err != nil {
		t.Fatalf("VerifyAccessToken() after refresh error = %v", err)
	}
}

func TestPostgresAuthMaintenancePurgesExpiredState(t *testing.T) {
	pool := accountsTestDatabase(t)
	repository := NewPostgresRepository(pool)
	now := time.Now().UTC()

	if _, err := pool.Exec(t.Context(), `
		INSERT INTO lingow_accounts (id, kind, created_at) VALUES ('acct_expired', 'anonymous', $1)`,
		now); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO lingow_auth_sessions (id, account_id, refresh_hash, expires_at, created_at)
		VALUES ('auths_expired', 'acct_expired', 'hash-expired', $1, $2)`,
		now.Add(-time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("insert expired session: %v", err)
	}
	digester := integrationCredentialDigester(t)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO lingow_phone_challenges
			(id, phone_hash, legacy_phone_hash, code_hash, digest_version, expires_at, created_at, used_at, attempts, max_attempts)
		VALUES ('challenge_expired', $1, $2, $3, 2, $4, $5, $6, 1, 5)`,
		digester.PhoneHash("+8613800000099"),
		"legacy",
		digester.CodeHash("challenge_expired", "123456"),
		now.Add(-time.Hour),
		now.Add(-2*time.Hour),
		now.Add(-90*24*time.Hour),
	); err != nil {
		t.Fatalf("insert stale challenge: %v", err)
	}

	sessions, err := repository.PurgeExpiredAuthSessions(t.Context())
	if err != nil {
		t.Fatalf("PurgeExpiredAuthSessions() error = %v", err)
	}
	if sessions != 1 {
		t.Fatalf("purged sessions = %d, want 1", sessions)
	}

	challenges, err := repository.PurgeStalePhoneChallenges(t.Context(), 30*24*time.Hour)
	if err != nil {
		t.Fatalf("PurgeStalePhoneChallenges() error = %v", err)
	}
	if challenges != 1 {
		t.Fatalf("purged challenges = %d, want 1", challenges)
	}
}
