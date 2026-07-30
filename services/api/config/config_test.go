package config

import (
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestLoadDefaultsToDisabledFailClosedMode(t *testing.T) {
	config, err := LoadFrom(mapEnv(map[string]string{}))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if config.DeliveryEnabled {
		t.Fatal("DeliveryEnabled = true, want false by default")
	}
	if config.DeliveryProvider != providerUnconfigured {
		t.Fatalf("DeliveryProvider = %q, want %q", config.DeliveryProvider, providerUnconfigured)
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

func TestLoadEnabledAcceptsLocalFakeProvider(t *testing.T) {
	config, err := LoadFrom(mapEnv(map[string]string{
		"APP_ENV":                         "local",
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"DATABASE_URL":                    "postgres://localhost/lingow",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"JWT_SECRET":                      "01234567890123456789012345678901",
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
	_, err := LoadFrom(mapEnv(map[string]string{
		"APP_ENV":                         "production",
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"DATABASE_URL":                    "postgres://localhost/lingow",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"JWT_SECRET":                      "01234567890123456789012345678901",
		"LINGOW_DELIVERY_DESTINATION_KEY": "base64-key",
		"LINGOW_DELIVERY_PROVIDER":        "fake_email",
	}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func TestLoadRequiresUniqueConsumerInProduction(t *testing.T) {
	_, err := LoadFrom(mapEnv(map[string]string{
		"APP_ENV":                         "production",
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"DATABASE_URL":                    "postgres://localhost/lingow",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"JWT_SECRET":                      "01234567890123456789012345678901",
		"LINGOW_DELIVERY_DESTINATION_KEY": "base64-key",
	}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func TestLoadUsesProcessEnvironment(t *testing.T) {
	t.Setenv("LINGOW_DELIVERY_RUNTIME", "disabled")
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
	_, err := LoadFrom(mapEnv(map[string]string{
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"DATABASE_URL":                    "postgres://localhost/lingow",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"JWT_SECRET":                      "short",
		"LINGOW_DELIVERY_DESTINATION_KEY": "base64-key",
	}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func TestLoadEnabledRejectsUnsupportedProvider(t *testing.T) {
	_, err := LoadFrom(mapEnv(map[string]string{
		"APP_ENV":                         "local",
		"LINGOW_DELIVERY_RUNTIME":         "enabled",
		"DATABASE_URL":                    "postgres://localhost/lingow",
		"REDIS_URL":                       "redis://localhost:6379/0",
		"JWT_SECRET":                      "01234567890123456789012345678901",
		"LINGOW_DELIVERY_DESTINATION_KEY": "base64-key",
		"LINGOW_DELIVERY_PROVIDER":        "smtp",
	}))
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("LoadFrom() error = %v, want invalid argument", err)
	}
}

func mapEnv(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
