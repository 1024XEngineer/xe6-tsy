package devices

import "context"

// Repository owns durable device bindings. It must atomically consume pairing
// and challenge records so a captured code or signature cannot be replayed.
type Repository interface {
	Provision(context.Context, Device) (Device, error)
	GetActive(context.Context, string) (Device, error)
	CanCreatePairingCode(context.Context, string) error
	ListBound(context.Context, string) ([]Device, error)
	Revoke(context.Context, string, string) error
	CreatePairingCode(context.Context, PairingCode) error
	BindWithPairingCode(context.Context, string, []byte) (Device, error)
	CreateChallenge(context.Context, Challenge) error
	GetChallenge(context.Context, string, string) (Challenge, error)
	ConsumeChallenge(context.Context, string, string) (Device, error)
	OwnsSession(context.Context, string, string, string) error
}

type TokenIssuer interface {
	Issue(DeviceClaims) (Token, error)
	Verify(context.Context, string) (DeviceClaims, error)
}
