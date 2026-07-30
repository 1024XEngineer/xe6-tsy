package config

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestLoadDefaultsToDisabledFailClosedMode(t *testing.T) {
	config, err := LoadFrom(mapCoreEnv(map[string]string{}))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if config.DeliveryEnabled {
		t.Fatal("DeliveryEnabled = true, want false by default")
	}
	if config.SessionRuntimeEnabled {
		t.Fatal("SessionRuntimeEnabled = true, want false by default")
	}
	if config.DeliveryProvider != providerUnconfigured {
		t.Fatalf("DeliveryProvider = %q, want %q", config.DeliveryProvider, providerUnconfigured)
	}
}

func TestLoadDisabledSessionRuntimeDoesNotRequireRealtimeConfig(t *testing.T) {
	config, err := LoadFrom(mapCoreEnv(map[string]string{
		"LINGOW_SESSION_RUNTIME": "disabled",
		"REALTIME_HTTP_TIMEOUT":  "not-a-duration",
	}))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if config.SessionRuntimeEnabled {
		t.Fatal("SessionRuntimeEnabled = true, want false")
	}
}

func TestLoadRequiresRealtimeSessionConfigWhenEnabled(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{name: "base URL", env: map[string]string{"REALTIME_BASE_URL": ""}},
		{name: "ticket secret", env: map[string]string{"REALTIME_TICKET_SECRET": ""}},
		{name: "short ticket secret", env: map[string]string{"REALTIME_TICKET_SECRET": strings.Repeat("s", 31)}},
		{name: "invalid URL", env: map[string]string{"REALTIME_BASE_URL": "127.0.0.1:8090"}},
		{name: "invalid timeout", env: map[string]string{"REALTIME_HTTP_TIMEOUT": "soon"}},
		{name: "zero timeout", env: map[string]string{"REALTIME_HTTP_TIMEOUT": "0s"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadFrom(mapSessionRuntimeEnv(test.env))
			if !errors.Is(err, domain.ErrInvalidArgument) {
				t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
			}
		})
	}
}

func TestLoadSetsRealtimeHTTPTimeoutDefault(t *testing.T) {
	config, err := LoadFrom(mapSessionRuntimeEnv(map[string]string{}))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if config.RealtimeHTTPTimeout != 5*time.Second {
		t.Fatalf("RealtimeHTTPTimeout = %s, want 5s", config.RealtimeHTTPTimeout)
	}
}

func TestLoadValidatesRealtimeBaseURL(t *testing.T) {
	tests := []struct {
		rawURL string
		wantOK bool
	}{
		{rawURL: "http://127.0.0.1:8090", wantOK: true},
		{rawURL: "https://realtime.example.com", wantOK: true},
		{rawURL: "https://example.com/internal", wantOK: true},
		{rawURL: "ftp://example.com", wantOK: false},
		{rawURL: "file:///tmp/realtime", wantOK: false},
		{rawURL: "http://user:pass@example.com", wantOK: false},
		{rawURL: "http://example.com?token=value", wantOK: false},
		{rawURL: "http://example.com#fragment", wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.rawURL, func(t *testing.T) {
			_, err := LoadFrom(mapSessionRuntimeEnv(map[string]string{"REALTIME_BASE_URL": test.rawURL}))
			if test.wantOK && err != nil {
				t.Fatalf("LoadFrom() error = %v, want nil", err)
			}
			if !test.wantOK && !errors.Is(err, domain.ErrInvalidArgument) {
				t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
			}
		})
	}
}

func TestLoadValidatesRealtimeHTTPTimeout(t *testing.T) {
	tests := []struct {
		value  string
		wantOK bool
	}{
		{value: "5s", wantOK: true},
		{value: "1s", wantOK: true},
		{value: "0s", wantOK: false},
		{value: "-1s", wantOK: false},
		{value: "6s", wantOK: false},
		{value: "30s", wantOK: false},
		{value: "abc", wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			config, err := LoadFrom(mapSessionRuntimeEnv(map[string]string{"REALTIME_HTTP_TIMEOUT": test.value}))
			if test.wantOK {
				if err != nil {
					t.Fatalf("LoadFrom() error = %v, want nil", err)
				}
				want, _ := time.ParseDuration(test.value)
				if config.RealtimeHTTPTimeout != want {
					t.Fatalf("RealtimeHTTPTimeout = %s, want %s", config.RealtimeHTTPTimeout, want)
				}
				return
			}
			if !errors.Is(err, domain.ErrInvalidArgument) {
				t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
			}
		})
	}
}

func TestConfigFormattingRedactsSecrets(t *testing.T) {
	config := Config{
		DatabaseURL:          "postgres://user:password@localhost/lingow",
		RedisURL:             "redis://:redis-password@localhost:6379/0",
		JWTSecret:            "01234567890123456789012345678901",
		RealtimeTicketSecret: "realtime-ticket-secret-123456789012",
		DestinationKey:       "delivery-destination-key",
		SMTPPassword:         "smtp-secret",
		WeComCorpSecret:      "wecom-secret",
	}
	formatted := fmt.Sprintf("%#v %v", config, config)
	for _, secret := range []string{
		"postgres://user:password@localhost/lingow",
		"redis://:redis-password@localhost:6379/0",
		"01234567890123456789012345678901",
		"realtime-ticket-secret-123456789012",
		"delivery-destination-key",
		"smtp-secret",
		"wecom-secret",
	} {
		if strings.Contains(formatted, secret) {
			t.Fatalf("formatted config leaked secret %q: %s", secret, formatted)
		}
	}
}

func TestLoadEnabledRequiresInfrastructureAndSecrets(t *testing.T) {
	_, err := LoadFrom(mapEnv(map[string]string{"LINGOW_DELIVERY_RUNTIME": "enabled"}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func TestLoadRejectsUnknownRuntimeMode(t *testing.T) {
	_, err := LoadFrom(mapEnv(map[string]string{"LINGOW_DELIVERY_RUNTIME": "enabeld"}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func TestLoadRejectsUnknownSessionRuntimeMode(t *testing.T) {
	_, err := LoadFrom(mapCoreEnv(map[string]string{"LINGOW_SESSION_RUNTIME": "enabeld"}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func TestLoadEnabledAcceptsLocalFakeProvider(t *testing.T) {
	config, err := LoadFrom(mapCoreEnv(map[string]string{
		"APP_ENV":                         "local",
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"LINGOW_DELIVERY_DESTINATION_KEY": "base64-key",
		"LINGOW_DELIVERY_PROVIDER":        "fake_email",
	}))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if !config.DeliveryEnabled || config.DeliveryProvider != providerFakeEmail {
		t.Fatalf("config = %#v, want enabled fake provider", config)
	}
}

func TestLoadRejectsFakeProviderInProduction(t *testing.T) {
	_, err := LoadFrom(mapCoreEnv(map[string]string{
		"APP_ENV":                         "production",
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"LINGOW_DELIVERY_DESTINATION_KEY": "base64-key",
		"LINGOW_DELIVERY_PROVIDER":        "fake_email",
	}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func TestLoadRequiresUniqueConsumerInProduction(t *testing.T) {
	_, err := LoadFrom(mapCoreEnv(map[string]string{
		"APP_ENV":                         "production",
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"LINGOW_DELIVERY_DESTINATION_KEY": "base64-key",
	}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func TestLoadUsesProcessEnvironment(t *testing.T) {
	t.Setenv("LINGOW_DELIVERY_RUNTIME", "disabled")
	t.Setenv("DATABASE_URL", "postgres://localhost/lingow")
	t.Setenv("JWT_SECRET", "01234567890123456789012345678901")
	t.Setenv("LINGOW_SESSION_RUNTIME", "disabled")
	config, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.DeliveryEnabled {
		t.Fatal("DeliveryEnabled = true, want false")
	}
}

func TestLoadFromRejectsNilEnvironmentReader(t *testing.T) {
	_, err := LoadFrom(nil)
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom(nil) error = %v, want invalid argument", err)
	}
}

func TestLoadEnabledRejectsShortJWTSecret(t *testing.T) {
	_, err := LoadFrom(mapCoreEnv(map[string]string{
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"JWT_SECRET":                      "short",
		"LINGOW_DELIVERY_DESTINATION_KEY": "base64-key",
	}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func TestLoadEnabledRejectsSMTPWithoutMailConfig(t *testing.T) {
	_, err := LoadFrom(mapCoreEnv(map[string]string{
		"APP_ENV":                         "local",
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"LINGOW_DELIVERY_DESTINATION_KEY": "base64-key",
		"LINGOW_DELIVERY_PROVIDER":        "smtp",
	}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func TestLoadEnabledAcceptsSMTPProvider(t *testing.T) {
	config, err := LoadFrom(mapCoreEnv(map[string]string{
		"APP_ENV":                         "production",
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"LINGOW_DELIVERY_DESTINATION_KEY": "base64-key",
		"LINGOW_DELIVERY_PROVIDER":        "smtp",
		"LINGOW_DELIVERY_CONSUMER":        "api-prod-1",
		"LINGOW_SMTP_HOST":                "smtp.example.test",
		"LINGOW_SMTP_FROM":                "noreply@example.test",
	}))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if config.DeliveryProvider != providerSMTP {
		t.Fatalf("DeliveryProvider = %q, want %q", config.DeliveryProvider, providerSMTP)
	}
}

func TestLoadEnabledRejectsPartialWeComConfig(t *testing.T) {
	_, err := LoadFrom(mapCoreEnv(map[string]string{
		"APP_ENV":                         "production",
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"LINGOW_DELIVERY_DESTINATION_KEY": "base64-key",
		"LINGOW_DELIVERY_PROVIDER":        "fake_email",
		"LINGOW_WECOM_CORP_ID":            "corp-id",
	}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func mapCoreEnv(values map[string]string) func(string) (string, bool) {
	env := map[string]string{
		"DATABASE_URL": "postgres://localhost/lingow",
		"JWT_SECRET":   "01234567890123456789012345678901",
	}
	for key, value := range values {
		env[key] = value
	}
	return mapEnv(env)
}

func mapSessionRuntimeEnv(values map[string]string) func(string) (string, bool) {
	env := map[string]string{
		"LINGOW_SESSION_RUNTIME": "enabled",
		"REALTIME_BASE_URL":      "http://127.0.0.1:8090",
		"REALTIME_TICKET_SECRET": "realtime-ticket-secret-123456789012",
	}
	for key, value := range values {
		env[key] = value
	}
	return mapCoreEnv(env)
}

func mapEnv(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
