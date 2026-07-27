package accounts

import "context"

// Repository owns durable account, challenge, and login-session state.
type Repository interface {
	// CreateAnonymous creates a new temporary account identity.
	CreateAnonymous(context.Context) (Account, error)
	// GetAccount reads an account by its public identifier.
	GetAccount(context.Context, string) (Account, error)
	// CreateChallenge persists a time-limited phone verification challenge.
	CreateChallenge(context.Context, PhoneChallenge) error
	// ConsumeChallenge atomically validates and marks a challenge as used.
	ConsumeChallenge(context.Context, string, string) error
	// FindOrCreateByPhoneHash resolves the registered account for a normalized phone hash.
	FindOrCreateByPhoneHash(context.Context, string) (Account, error)
	// BindAnonymous transfers an anonymous account into the registered account boundary.
	BindAnonymous(context.Context, string, string) (Account, error)
	// CreateSession persists a refreshable login session.
	CreateSession(context.Context, Session) error
	// GetSessionByRefreshHash resolves an active session without storing plaintext credentials.
	GetSessionByRefreshHash(context.Context, string) (Session, error)
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
