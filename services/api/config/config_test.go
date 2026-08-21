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

func TestLoadRuntimeModesAcceptDocumentedBooleanAliases(t *testing.T) {
	tests := []struct {
		name         string
		sessionMode  string
		deliveryMode string
		wantSession  bool
		wantDelivery bool
	}{
		{name: "session true alias", sessionMode: "TRUE", wantSession: true},
		{name: "session one alias", sessionMode: "1", wantSession: true},
		{name: "delivery false alias", deliveryMode: "FALSE", wantDelivery: false},
		{name: "delivery zero alias", deliveryMode: "0", wantDelivery: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{}
			if tt.sessionMode != "" {
				env["LINGOW_SESSION_RUNTIME"] = tt.sessionMode
			}
			if tt.deliveryMode != "" {
				env["LINGOW_DELIVERY_RUNTIME"] = tt.deliveryMode
			}
			getenv := mapCoreEnv(env)
			if tt.wantSession {
				getenv = mapSessionRuntimeEnv(env)
			}
			config, err := LoadFrom(getenv)
			if err != nil {
				t.Fatalf("LoadFrom() error = %v", err)
			}
			if config.SessionRuntimeEnabled != tt.wantSession || config.DeliveryEnabled != tt.wantDelivery {
				t.Fatalf("runtime flags = (%t, %t), want (%t, %t)", config.SessionRuntimeEnabled, config.DeliveryEnabled, tt.wantSession, tt.wantDelivery)
			}
		})
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

func TestLoadSecretMinimumBoundaries(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "jwt secret 31 bytes", env: map[string]string{"JWT_SECRET": strings.Repeat("j", 31)}, want: "invalid"},
		{name: "jwt secret 32 bytes", env: map[string]string{"JWT_SECRET": strings.Repeat("j", 32)}},
		{name: "realtime ticket secret 31 bytes", env: map[string]string{"REALTIME_TICKET_SECRET": strings.Repeat("r", 31)}, want: "invalid"},
		{name: "realtime ticket secret 32 bytes", env: map[string]string{"REALTIME_TICKET_SECRET": strings.Repeat("r", 32)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFrom(mapSessionRuntimeEnv(tt.env))
			if tt.want == "invalid" {
				if !errors.Is(err, domain.ErrInvalidArgument) {
					t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadFrom() error = %v, want nil", err)
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

func TestLoadSessionRuntimeReadsModeProjectionStreamConfig(t *testing.T) {
	config, err := LoadFrom(mapSessionRuntimeEnv(map[string]string{
		"LINGOW_MODE_CHANGED_STREAM":   "lingow:realtime:mode:changed:test",
		"LINGOW_MODE_CHANGED_GROUP":    "mode-projection-test",
		"LINGOW_MODE_CHANGED_CONSUMER": "api-test-1",
	}))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if config.ModeChangedStream != "lingow:realtime:mode:changed:test" ||
		config.ModeChangedGroup != "mode-projection-test" ||
		config.ModeChangedConsumer != "api-test-1" {
		t.Fatalf("mode projection stream config = (%q, %q, %q)", config.ModeChangedStream, config.ModeChangedGroup, config.ModeChangedConsumer)
	}
}

func TestLoadSessionRuntimeRequiresRedis(t *testing.T) {
	_, err := LoadFrom(mapSessionRuntimeEnv(map[string]string{"REDIS_URL": ""}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func TestLoadSessionRuntimeRequiresUniqueModeConsumerInProduction(t *testing.T) {
	_, err := LoadFrom(mapSessionRuntimeEnv(map[string]string{
		"APP_ENV": "production",
	}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}

	_, err = LoadFrom(mapSessionRuntimeEnv(map[string]string{
		"APP_ENV":                      "production",
		"LINGOW_MODE_CHANGED_CONSUMER": "api-prod-mode-1",
	}))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v, want nil", err)
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

func TestLoadDeliveryRuntimeValidatesRealtimeHTTPTimeout(t *testing.T) {
	tests := []struct {
		value  string
		want   time.Duration
		wantOK bool
	}{
		{value: "3s", want: 3 * time.Second, wantOK: true},
		{value: "soon"},
		{value: "0s"},
		{value: "6s"},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			config, err := LoadFrom(mapCoreEnv(map[string]string{
				"LINGOW_DELIVERY_RUNTIME":         "enabled",
				"REDIS_URL":                       "redis://localhost:6379/0",
				"LINGOW_DELIVERY_DESTINATION_KEY": "base64-key",
				"REALTIME_HTTP_TIMEOUT":           test.value,
			}))
			if !test.wantOK {
				if !errors.Is(err, domain.ErrInvalidArgument) {
					t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadFrom() error = %v", err)
			}
			if config.RealtimeHTTPTimeout != test.want {
				t.Fatalf("RealtimeHTTPTimeout = %s, want %s", config.RealtimeHTTPTimeout, test.want)
			}
		})
	}
}

func TestConfigFormattingRedactsSecrets(t *testing.T) {
	config := Config{
		DatabaseURL:          "postgres://user:pass@localhost/db",
		RedisURL:             "redis://user:pass@localhost:6379/0",
		JWTSecret:            "01234567890123456789012345678901",
		RealtimeTicketSecret: "realtime-ticket-secret-123456789012",
		DestinationKey:       "destination-key-secret",
		SMTPPassword:         "smtp-secret",
		WeComCorpSecret:      "wecom-secret",
		RecordsSystemToken:   "records-system-token-secret-123456",
	}
	formatted := fmt.Sprintf("%#v %v", config, config)
	for _, secret := range []string{
		"postgres://user:pass@localhost/db",
		"redis://user:pass@localhost:6379/0",
		"01234567890123456789012345678901",
		"realtime-ticket-secret-123456789012",
		"destination-key-secret",
		"smtp-secret",
		"wecom-secret",
		"records-system-token-secret-123456",
	} {
		if strings.Contains(formatted, secret) {
			t.Fatalf("formatted config leaked secret %q: %s", secret, formatted)
		}
	}
}

func TestLoadAcceptsMissingRecordsSystemToken(t *testing.T) {
	config, err := LoadFrom(mapCoreEnv(map[string]string{}))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if config.RecordsSystemToken != "" {
		t.Fatalf("RecordsSystemToken = %q, want empty", config.RecordsSystemToken)
	}
}

func TestLoadAcceptsConfiguredRecordsSystemToken(t *testing.T) {
	config, err := LoadFrom(mapCoreEnv(map[string]string{
		"LINGOW_RECORDS_SYSTEM_TOKEN": "records-system-token-secret-123456",
	}))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if config.RecordsSystemToken != "records-system-token-secret-123456" {
		t.Fatalf("RecordsSystemToken = %q, want configured value", config.RecordsSystemToken)
	}
}

func TestLoadRejectsShortRecordsSystemToken(t *testing.T) {
	_, err := LoadFrom(mapCoreEnv(map[string]string{
		"LINGOW_RECORDS_SYSTEM_TOKEN": "short",
	}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func TestLoadReadsAndRedactsCommandSystemToken(t *testing.T) {
	token := "command-system-token-secret-123456"
	config, err := LoadFrom(mapCoreEnv(map[string]string{"LINGOW_COMMAND_SYSTEM_TOKEN": token}))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if config.CommandSystemToken != token || strings.Contains(config.String(), token) {
		t.Fatalf("command token was not loaded and redacted: %s", config.String())
	}
	if _, err := LoadFrom(mapCoreEnv(map[string]string{"LINGOW_COMMAND_SYSTEM_TOKEN": "short"})); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("short command token error = %v", err)
	}
}

func TestLoadEnabledRequiresInfrastructureAndSecrets(t *testing.T) {
	_, err := LoadFrom(mapEnv(map[string]string{"LINGOW_DELIVERY_RUNTIME": "enabled"}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func TestLoadEnabledRequiresRealtimeFallbackConfiguration(t *testing.T) {
	base := map[string]string{
		"APP_ENV":                         "local",
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"LINGOW_DELIVERY_DESTINATION_KEY": "base64-key",
	}
	for _, key := range []string{"REALTIME_BASE_URL", "REALTIME_TICKET_SECRET"} {
		t.Run(key, func(t *testing.T) {
			values := map[string]string{}
			for name, value := range base {
				values[name] = value
			}
			values["REALTIME_BASE_URL"] = "http://127.0.0.1:8090"
			values["REALTIME_TICKET_SECRET"] = "realtime-ticket-secret-123456789012"
			values[key] = ""
			if _, err := LoadFrom(mapCoreEnv(values)); !errors.Is(err, domain.ErrInvalidArgument) {
				t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
			}
		})
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
	if config.SMTPPortInt(25) != 587 {
		t.Fatalf("SMTPPortInt() = %d, want configured default 587", config.SMTPPortInt(25))
	}
}

func TestConfigNumericFieldsUseExplicitPositiveSemantics(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
		want  int
	}{
		{name: "smtp port", value: "2525", want: 2525},
		{name: "invalid smtp port falls back", value: "not-a-port", want: 25},
		{name: "zero smtp port falls back", value: "0", want: 25},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Config{SMTPPort: tt.value}).SMTPPortInt(25); got != tt.want {
				t.Fatalf("SMTPPortInt() = %d, want %d", got, tt.want)
			}
		})
	}
	for _, tt := range []struct {
		name  string
		value string
		want  int
	}{
		{name: "positive agent", value: "42", want: 42},
		{name: "invalid agent", value: "abc", want: 0},
		{name: "zero agent", value: "0", want: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Config{WeComAgentID: tt.value}).WeComAgentIDInt(); got != tt.want {
				t.Fatalf("WeComAgentIDInt() = %d, want %d", got, tt.want)
			}
		})
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

func TestLoadEnabledAcceptsCompleteWeComConfigAndParsesAgentID(t *testing.T) {
	config, err := LoadFrom(mapCoreEnv(map[string]string{
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"LINGOW_DELIVERY_DESTINATION_KEY": "base64-key",
		"LINGOW_DELIVERY_PROVIDER":        "fake_email",
		"LINGOW_WECOM_CORP_ID":            "corp-id",
		"LINGOW_WECOM_CORP_SECRET":        "corp-secret",
		"LINGOW_WECOM_AGENT_ID":           "42",
	}))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if config.WeComAgentIDInt() != 42 {
		t.Fatalf("WeComAgentIDInt() = %d, want 42", config.WeComAgentIDInt())
	}
}

func TestLoadReportsTheInvalidRuntimeSetting(t *testing.T) {
	for _, tt := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "session runtime", env: map[string]string{"LINGOW_SESSION_RUNTIME": "invalid"}, want: "LINGOW_SESSION_RUNTIME must be enabled or disabled"},
		{name: "delivery runtime", env: map[string]string{"LINGOW_DELIVERY_RUNTIME": "invalid"}, want: "LINGOW_DELIVERY_RUNTIME must be enabled or disabled"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFrom(mapCoreEnv(tt.env))
			if !errors.Is(err, domain.ErrInvalidArgument) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadFrom() error = %v, want invalid argument containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadReportsMissingCoreAndDeliveryDependencies(t *testing.T) {
	for _, tt := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "database", env: map[string]string{"DATABASE_URL": ""}, want: "DATABASE_URL is required"},
		{name: "issuer", env: map[string]string{"JWT_ISSUER": ""}, want: "JWT_ISSUER is required"},
		{name: "audience", env: map[string]string{"JWT_AUDIENCE": ""}, want: "JWT_AUDIENCE is required"},
	} {
		t.Run("core "+tt.name, func(t *testing.T) {
			_, err := LoadFrom(mapCoreEnv(tt.env))
			if !errors.Is(err, domain.ErrInvalidArgument) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadFrom() error = %v, want invalid argument containing %q", err, tt.want)
			}
		})
	}

	base := map[string]string{
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"LINGOW_DELIVERY_DESTINATION_KEY": "destination-key",
	}
	for _, tt := range []struct {
		name string
		key  string
	}{
		{name: "redis", key: "REDIS_URL"},
		{name: "destination key", key: "LINGOW_DELIVERY_DESTINATION_KEY"},
		{name: "realtime base URL", key: "REALTIME_BASE_URL"},
		{name: "realtime ticket secret", key: "REALTIME_TICKET_SECRET"},
	} {
		t.Run("delivery "+tt.name, func(t *testing.T) {
			env := make(map[string]string, len(base)+1)
			for key, value := range base {
				env[key] = value
			}
			env[tt.key] = ""
			_, err := LoadFrom(mapCoreEnv(env))
			if !errors.Is(err, domain.ErrInvalidArgument) || !strings.Contains(err.Error(), tt.key+" is required when delivery runtime is enabled") {
				t.Fatalf("LoadFrom() error = %v, want invalid argument naming %s", err, tt.key)
			}
		})
	}
}

func TestLoadDeliveryProviderAndWeComValidationAreIndependent(t *testing.T) {
	base := map[string]string{
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"LINGOW_DELIVERY_DESTINATION_KEY": "destination-key",
		"LINGOW_DELIVERY_PROVIDER":        "fake_email",
	}
	for _, tt := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "fake provider in production", env: map[string]string{"APP_ENV": "production", "LINGOW_DELIVERY_CONSUMER": "api-1"}, want: "fake email provider is not allowed in production"},
		{name: "unsupported provider", env: map[string]string{"LINGOW_DELIVERY_PROVIDER": "unknown"}, want: "unsupported delivery provider"},
		{name: "smtp host", env: map[string]string{"LINGOW_DELIVERY_PROVIDER": "smtp", "LINGOW_SMTP_FROM": "noreply@example.test"}, want: "LINGOW_SMTP_HOST and LINGOW_SMTP_FROM are required"},
		{name: "smtp from", env: map[string]string{"LINGOW_DELIVERY_PROVIDER": "smtp", "LINGOW_SMTP_HOST": "smtp.example.test"}, want: "LINGOW_SMTP_HOST and LINGOW_SMTP_FROM are required"},
		{name: "wecom only corp ID", env: map[string]string{"LINGOW_WECOM_CORP_ID": "corp"}, want: "must be configured together"},
		{name: "wecom only secret", env: map[string]string{"LINGOW_WECOM_CORP_SECRET": "secret"}, want: "must be configured together"},
		{name: "wecom only agent", env: map[string]string{"LINGOW_WECOM_AGENT_ID": "42"}, want: "must be configured together"},
		{name: "wecom zero agent", env: map[string]string{"LINGOW_WECOM_CORP_ID": "corp", "LINGOW_WECOM_CORP_SECRET": "secret", "LINGOW_WECOM_AGENT_ID": "0"}, want: "must be a positive integer"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := make(map[string]string, len(base)+len(tt.env))
			for key, value := range base {
				env[key] = value
			}
			for key, value := range tt.env {
				env[key] = value
			}
			_, err := LoadFrom(mapCoreEnv(env))
			if !errors.Is(err, domain.ErrInvalidArgument) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadFrom() error = %v, want invalid argument containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadSystemTokenAndPositiveValueBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "records token 31 bytes", env: map[string]string{"LINGOW_RECORDS_SYSTEM_TOKEN": strings.Repeat("r", 31)}},
		{name: "records token 32 bytes", env: map[string]string{"LINGOW_RECORDS_SYSTEM_TOKEN": strings.Repeat("r", 32)}, want: true},
		{name: "command token 31 bytes", env: map[string]string{"LINGOW_COMMAND_SYSTEM_TOKEN": strings.Repeat("c", 31)}},
		{name: "command token 32 bytes", env: map[string]string{"LINGOW_COMMAND_SYSTEM_TOKEN": strings.Repeat("c", 32)}, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFrom(mapCoreEnv(tt.env))
			if tt.want && err != nil {
				t.Fatalf("LoadFrom() error = %v, want nil", err)
			}
			if !tt.want && !errors.Is(err, domain.ErrInvalidArgument) {
				t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
			}
		})
	}

	if got := (Config{SMTPPort: "1"}).SMTPPortInt(25); got != 1 {
		t.Fatalf("SMTPPortInt() = %d, want 1", got)
	}
	if got := (Config{WeComAgentID: "1"}).WeComAgentIDInt(); got != 1 {
		t.Fatalf("WeComAgentIDInt() = %d, want 1", got)
	}
	config, err := LoadFrom(mapSessionRuntimeEnv(map[string]string{"REALTIME_HTTP_TIMEOUT": "1ns"}))
	if err != nil || config.RealtimeHTTPTimeout != time.Nanosecond {
		t.Fatalf("LoadFrom() = (%#v, %v), want a 1ns timeout", config, err)
	}
}

func mapCoreEnv(values map[string]string) func(string) (string, bool) {
	env := map[string]string{
		"DATABASE_URL":           "postgres://localhost/lingow",
		"JWT_SECRET":             "01234567890123456789012345678901",
		"REALTIME_BASE_URL":      "http://127.0.0.1:8090",
		"REALTIME_TICKET_SECRET": "realtime-ticket-secret-123456789012",
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
		"REDIS_URL":              "redis://localhost:6379/0",
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
