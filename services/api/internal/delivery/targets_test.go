package delivery

import (
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestParseDevEmailBindTokenAcceptsLocalFormats(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		wantRef   string
		wantEmail string
	}{
		{name: "email only", token: "dev:user@example.test", wantRef: "primary-email", wantEmail: "user@example.test"},
		{name: "ref and email", token: "dev:work-email:user@example.test", wantRef: "work-email", wantEmail: "user@example.test"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ref, email, err := parseDevEmailBindToken("local", test.token)
			if err != nil {
				t.Fatalf("parseDevEmailBindToken() error = %v", err)
			}
			if ref != test.wantRef || email != test.wantEmail {
				t.Fatalf("parseDevEmailBindToken() = (%q, %q)", ref, email)
			}
		})
	}
}

func TestParseDevEmailBindTokenFailsClosedOutsideLocal(t *testing.T) {
	_, _, err := parseDevEmailBindToken("production", "dev:user@example.test")
	if !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("parseDevEmailBindToken() error = %v, want not implemented", err)
	}
}

func TestParseDevEmailBindTokenRejectsNonDevTokens(t *testing.T) {
	_, _, err := parseDevEmailBindToken("local", "verify-token")
	if !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("parseDevEmailBindToken() error = %v, want not implemented", err)
	}
}

func TestBindEmailTargetRequiresConfiguredKey(t *testing.T) {
	repository := &PostgresRepository{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	_, err := service.BindEmailTarget(t.Context(), "account-1", "dev:user@example.test")
	if !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("BindEmailTarget() error = %v, want not implemented", err)
	}
}
