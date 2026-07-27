package languages

import "context"

// Stub is an empty implementation of the language-configuration boundary ports.
// Every method returns ErrNotImplemented until the internal service is built.
type Stub struct{}

// NewStub returns a ready-to-wire empty language module.
func NewStub() *Stub {
	return &Stub{}
}

var (
	_ LanguageConfigReader   = (*Stub)(nil)
	_ LanguageTargetResolver = (*Stub)(nil)
)

// GetCurrentConfig implements LanguageConfigReader as a no-op stub.
func (s *Stub) GetCurrentConfig(ctx context.Context, sessionID string) (LanguageConfigSnapshot, error) {
	_ = ctx
	_ = sessionID
	return LanguageConfigSnapshot{}, ErrNotImplemented
}

// ResolveTarget implements LanguageTargetResolver as a no-op stub.
func (s *Stub) ResolveTarget(
	ctx context.Context,
	sessionID string,
	sourceLanguage string,
) (string, int, error) {
	_ = ctx
	_ = sessionID
	_ = sourceLanguage
	return "", 0, ErrNotImplemented
}
