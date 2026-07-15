package identity

import "errors"

var (
	ErrInvalidAssertion     = errors.New("invalid identity assertion")
	ErrAuthenticationFailed = errors.New("identity authentication failed")
	ErrProviderUnavailable  = errors.New("identity provider unavailable")
)
