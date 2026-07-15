package fake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/modules/identity"
)

func TestIdentityService_StartAccessSession(t *testing.T) {
	fixedNow := time.Date(2026, time.July, 15, 8, 0, 0, 0, time.UTC)
	service := NewIdentityService(func() time.Time { return fixedNow })

	tests := []struct {
		name        string
		provider    string
		fixture     string
		wantRole    identity.RoleCode
		wantSession string
		wantErr     error
	}{
		{
			name:        "active staff",
			provider:    "demo",
			fixture:     "staff-active",
			wantRole:    identity.RoleStaff,
			wantSession: "session-demo-staff-001",
		},
		{
			name:        "configuration maintainer",
			provider:    "demo",
			fixture:     "config-maintainer",
			wantRole:    identity.RoleConfigMaintainer,
			wantSession: "session-demo-maintainer-001",
		},
		{
			name:     "disabled staff",
			provider: "demo",
			fixture:  "staff-disabled",
			wantErr:  identity.ErrAuthenticationFailed,
		},
		{
			name:     "provider unavailable",
			provider: "demo",
			fixture:  "provider-error",
			wantErr:  identity.ErrProviderUnavailable,
		},
		{
			name:     "unknown fixture",
			provider: "demo",
			fixture:  "unknown",
			wantErr:  identity.ErrInvalidAssertion,
		},
		{
			name:     "unsupported provider",
			provider: "real",
			fixture:  "staff-active",
			wantErr:  identity.ErrInvalidAssertion,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := service.StartAccessSession(context.Background(), identity.IdentityAssertion{
				ProviderCode:    test.provider,
				ExternalSubject: test.fixture,
			})
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("StartAccessSession() error = %v", err)
			}
			if got.AccessSessionID != test.wantSession {
				t.Fatalf("AccessSessionID = %q, want %q", got.AccessSessionID, test.wantSession)
			}
			if len(got.RoleCodes) != 1 || got.RoleCodes[0] != test.wantRole {
				t.Fatalf("RoleCodes = %#v, want [%q]", got.RoleCodes, test.wantRole)
			}
			if !got.IssuedAt.Equal(fixedNow) || !got.ExpiresAt.Equal(fixedNow.Add(accessSessionLifetime)) {
				t.Fatalf("session times = %v to %v", got.IssuedAt, got.ExpiresAt)
			}
		})
	}
}

func TestIdentityService_StartAccessSessionHonorsCancellation(t *testing.T) {
	service := NewIdentityService(time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := service.StartAccessSession(ctx, identity.IdentityAssertion{
		ProviderCode:    "demo",
		ExternalSubject: "staff-active",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want %v", err, context.Canceled)
	}
}
