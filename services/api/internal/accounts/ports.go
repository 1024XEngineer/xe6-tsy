package accounts

import "context"

// Repository owns durable account, challenge, and login-session state.
type Repository interface {
	// CreateAnonymous creates a new temporary account identity.
	CreateAnonymous(context.Context) (Account, error)
	// GetAccount reads an account by its public identifier.
	GetAccount(context.Context, string) (Account, error)
	// CreateChallenge persists a time-limited challenge after atomically enforcing
	// the per-phone cooldown and rolling send limit.
	CreateChallenge(context.Context, PhoneChallenge) error
	// ConsumeChallenge atomically validates a pre-derived challenge-code digest and returns its private
	// phone binding only after a successful one-time use. Failed code attempts
	// must be durably counted before returning unauthorized.
	ConsumeChallenge(context.Context, string, string) (PhoneChallenge, error)
	// RestoreChallenge makes a consumed challenge retryable after a downstream
	// account or session persistence failure.
	RestoreChallenge(context.Context, string) error
	// FindOrCreateByPhoneHashes resolves the registered account with its current
	// HMAC digest and a legacy SHA-256 lookup digest for lazy migration.
	FindOrCreateByPhoneHashes(context.Context, string, string) (Account, error)
	// BindAnonymous transfers an anonymous account into the registered account boundary.
	BindAnonymous(context.Context, string, string) (Account, error)
	// CreateSession persists a refreshable login session.
	CreateSession(context.Context, Session) error
	// GetSessionByRefreshHash resolves an active session without storing plaintext credentials.
	GetSessionByRefreshHash(context.Context, string) (Session, error)
	// RotateSession atomically revokes the current session and persists its successor.
	// Implementations must leave the current session active if the successor cannot be stored.
	RotateSession(context.Context, string, Session) error
	// RevokeSession invalidates a login session and its refresh-token chain.
	RevokeSession(context.Context, string) error
}

// VerificationSender isolates delivery of phone verification codes from account policy.
type VerificationSender interface {
	// SendCode sends a one-time code to the provider target without exposing it in API output.
	SendCode(context.Context, string, string) error
}

// TokenIssuer owns access-token creation and refresh-token hashing policy.
type TokenIssuer interface {
	// Issue creates a credential pair for an authenticated account session.
	Issue(context.Context, Account, Session) (Tokens, error)
	// HashRefreshToken derives the value safe for repository lookup and storage.
	HashRefreshToken(string) string
}

// AccessTokenVerifier validates an access token before its account identity is trusted.
// Implementations must reject invalid, expired, or otherwise unacceptable tokens.
type AccessTokenVerifier interface {
	VerifyAccessToken(context.Context, string) (AccessTokenClaims, error)
}

// CanonicalAccountResolver resolves an account to the active account at the
// top of its merge chain. Read-side modules use it before comparing ownership
// so a registered account can access its anonymous-period history.
type CanonicalAccountResolver interface {
	CanonicalAccountID(context.Context, string) (string, error)
}

// Service defines the account use cases consumed by the HTTP adapter.
type Service interface {
	// CreateAnonymous establishes temporary ownership and returns initial credentials.
	CreateAnonymous(context.Context) (AuthResult, error)
	// CreatePhoneChallenge starts phone verification and returns its opaque challenge ID.
	CreatePhoneChallenge(context.Context, string) (string, error)
	// VerifyPhone consumes a challenge and optionally merges an anonymous account.
	VerifyPhone(context.Context, string, string, string) (AuthResult, error)
	// Refresh rotates credentials for an active login session.
	Refresh(context.Context, string) (Tokens, error)
	// Logout revokes the session identified by a refresh token.
	Logout(context.Context, string) error
	// Me returns the account selected by trusted authentication context.
	Me(context.Context, string) (Account, error)
}
