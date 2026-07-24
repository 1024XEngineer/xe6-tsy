package accounts

import "time"

type AccountKind string

const (
	AccountKindAnonymous  AccountKind = "anonymous"
	AccountKindRegistered AccountKind = "registered"
)

type Account struct {
	ID        string      `json:"id"`
	Kind      AccountKind `json:"kind"`
	CreatedAt time.Time   `json:"created_at"`
}

type Session struct {
	ID          string
	AccountID   string
	RefreshHash string
	ExpiresAt   time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
}

type PhoneChallenge struct {
	ID        string
	PhoneHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type AuthResult struct {
	Account Account `json:"account"`
	Tokens  Tokens  `json:"tokens"`
}
