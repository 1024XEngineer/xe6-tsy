package languages

import "context"

// SessionOwnerReader is provided by the session-management module.
// Language HTTP handlers use it to enforce session ownership after auth.
type SessionOwnerReader interface {
	GetOwnerAccountID(ctx context.Context, sessionID string) (accountID string, err error)
}

// NotImplementedSessionOwner is an explicit fail-closed adapter used when
// ownership wiring is unavailable. Production composition should prefer
// NewRecordsSessionOwner over this type.
type NotImplementedSessionOwner struct{}

func (NotImplementedSessionOwner) GetOwnerAccountID(context.Context, string) (string, error) {
	return "", ErrNotImplemented
}

// MapSessionOwner returns fixed session→account ownership for tests.
type MapSessionOwner map[string]string

func (m MapSessionOwner) GetOwnerAccountID(_ context.Context, sessionID string) (string, error) {
	accountID, ok := m[sessionID]
	if !ok {
		return "", ErrSessionNotFound
	}
	return accountID, nil
}

// TrustAuthSessionOwner is a development adapter: every session is treated as
// owned by the authenticated caller. Enable only via explicit wiring.
type TrustAuthSessionOwner struct {
	AccountIDFromCtx func(context.Context) (string, bool)
}

func (t TrustAuthSessionOwner) GetOwnerAccountID(ctx context.Context, _ string) (string, error) {
	if t.AccountIDFromCtx == nil {
		return "", ErrNotImplemented
	}
	accountID, ok := t.AccountIDFromCtx(ctx)
	if !ok || accountID == "" {
		return "", ErrUnauthenticated
	}
	return accountID, nil
}
