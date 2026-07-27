package accounts

import "time"

// AccountKind distinguishes temporary anonymous ownership from a registered identity.
type AccountKind string

const (
	// AccountKindAnonymous identifies an account that has not completed registration.
	AccountKindAnonymous AccountKind = "anonymous"
	// AccountKindRegistered identifies an account backed by a verified login identity.
	AccountKindRegistered AccountKind = "registered"
)

// Account is the stable ownership identity shared by sessions, usage, and delivery.
type Account struct {
	ID        string      `json:"id"`
	Kind      AccountKind `json:"kind"`
	CreatedAt time.Time   `json:"created_at"`
}

// Session represents a revocable login session backed by a hashed refresh token.
type Session struct {
	ID          string
	AccountID   string
	RefreshHash string
	ExpiresAt   time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
}

// PhoneChallenge stores non-plaintext verification state for a single login attempt.
type PhoneChallenge struct {
	ID        string
	PhoneHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

// Tokens is the credential pair returned after authentication or refresh.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// AuthResult returns the authenticated account together with its newly issued credentials.
type AuthResult struct {
	Account Account `json:"account"`
	Tokens  Tokens  `json:"tokens"`
}

// AccessTokenClaims contains only identity established by access-token validation.
// HTTP adapters must not populate these claims from client account fields.
type AccessTokenClaims struct {
	AccountID string
	SessionID string
}
