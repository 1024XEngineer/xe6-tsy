// Package devices owns concrete hardware identity, pairing, and constrained
// device credentials. It does not replace account authentication: a device is
// useful only after a user account has explicitly bound it.
package devices

import "time"

type Status string

const (
	StatusActive  Status = "active"
	StatusRevoked Status = "revoked"
)

type Device struct {
	DeviceID  string     `json:"device_id"`
	ProductID string     `json:"product_id"`
	PublicKey []byte     `json:"-"`
	AccountID *string    `json:"-"`
	Status    Status     `json:"status"`
	BoundAt   *time.Time `json:"bound_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type PairingCode struct {
	ID        string
	AccountID string
	Hash      []byte
	ExpiresAt time.Time
	CreatedAt time.Time
}

type Challenge struct {
	ID        string
	DeviceID  string
	Nonce     string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type DeviceClaims struct {
	AccountID string
	DeviceID  string
	ExpiresAt time.Time
}

type Token struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	DeviceID    string    `json:"device_id"`
}

func (s Status) valid() bool { return s == StatusActive || s == StatusRevoked }
