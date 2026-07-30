// Package config owns API process configuration and startup validation.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

const (
	providerUnconfigured = "unconfigured"
	providerFakeEmail    = "fake_email"
)

// Config contains only process configuration. Secrets are kept as strings at
// this boundary and are consumed by the runtime constructor; they are never
// included in logs or serialized responses.
type Config struct {
	AppEnv              string
	APIAddr             string
	DatabaseURL         string
	RedisURL            string
	JWTSecret           string
	JWTIssuer           string
	JWTAudience         string
	DestinationKey      string
	DeliveryEnabled     bool
	DeliveryProvider    string
	DeliveryConsumer    string
	DeliveryStream      string
	DeliveryGroup       string
	DeliveryDelayStream string
	DeliveryDelayKey    string
	UsageStream         string
	UsageGroup          string
	UsageConsumer       string
}

// Load reads the process environment and validates only the configuration
// required by the selected runtime mode. The delivery runtime is opt-in so a
// local process without infrastructure remains an explicit not_implemented
// deployment rather than a partially wired one.
func Load() (Config, error) {
	return LoadFrom(os.LookupEnv)
}

// LoadFrom is injectable for deterministic configuration tests.
func LoadFrom(getenv func(string) (string, bool)) (Config, error) {
	if getenv == nil {
		return Config{}, fmt.Errorf("%w: environment reader is required", domain.ErrInvalidArgument)
	}
	value := func(key, fallback string) string {
		if value, ok := getenv(key); ok {
			return strings.TrimSpace(value)
		}
		return fallback
	}
	config := Config{
		AppEnv:              value("APP_ENV", "local"),
		APIAddr:             value("API_ADDR", ":8080"),
		DatabaseURL:         value("DATABASE_URL", ""),
		RedisURL:            value("REDIS_URL", ""),
		JWTSecret:           value("JWT_SECRET", ""),
		JWTIssuer:           value("JWT_ISSUER", "lingow-api"),
		JWTAudience:         value("JWT_AUDIENCE", "lingow-client"),
		DestinationKey:      value("LINGOW_DELIVERY_DESTINATION_KEY", ""),
		DeliveryProvider:    value("LINGOW_DELIVERY_PROVIDER", providerUnconfigured),
		DeliveryConsumer:    value("LINGOW_DELIVERY_CONSUMER", ""),
		DeliveryStream:      value("LINGOW_DELIVERY_STREAM", ""),
		DeliveryGroup:       value("LINGOW_DELIVERY_GROUP", ""),
		DeliveryDelayStream: value("LINGOW_DELIVERY_DELAY_STREAM", ""),
		DeliveryDelayKey:    value("LINGOW_DELIVERY_DELAY_KEY", ""),
		UsageStream:         value("LINGOW_USAGE_STREAM", ""),
		UsageGroup:          value("LINGOW_USAGE_GROUP", ""),
		UsageConsumer:       value("LINGOW_USAGE_CONSUMER", ""),
	}
	runtimeMode := strings.ToLower(value("LINGOW_DELIVERY_RUNTIME", "disabled"))
	switch runtimeMode {
	case "disabled", "false", "0", "":
		config.DeliveryEnabled = false
	case "enabled", "true", "1":
		config.DeliveryEnabled = true
	default:
		return Config{}, fmt.Errorf("%w: LINGOW_DELIVERY_RUNTIME must be enabled or disabled", domain.ErrInvalidArgument)
	}
	if !config.DeliveryEnabled {
		return config, nil
	}
	if err := validateEnabled(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func validateEnabled(config Config) error {
	for key, value := range map[string]string{
		"DATABASE_URL":                    config.DatabaseURL,
		"REDIS_URL":                       config.RedisURL,
		"JWT_SECRET":                      config.JWTSecret,
		"LINGOW_DELIVERY_DESTINATION_KEY": config.DestinationKey,
		"JWT_ISSUER":                      config.JWTIssuer,
		"JWT_AUDIENCE":                    config.JWTAudience,
	} {
		if value == "" {
			return fmt.Errorf("%w: %s is required when delivery runtime is enabled", domain.ErrInvalidArgument, key)
		}
	}
	if len([]byte(config.JWTSecret)) < 32 {
		return fmt.Errorf("%w: JWT_SECRET must contain at least 32 bytes", domain.ErrInvalidArgument)
	}
	switch config.DeliveryProvider {
	case providerUnconfigured:
	case providerFakeEmail:
		if strings.EqualFold(config.AppEnv, "production") {
			return fmt.Errorf("%w: fake email provider is not allowed in production", domain.ErrInvalidArgument)
		}
	default:
		return fmt.Errorf("%w: unsupported delivery provider %q", domain.ErrInvalidArgument, config.DeliveryProvider)
	}
	if strings.EqualFold(config.AppEnv, "production") && config.DeliveryConsumer == "" {
		return fmt.Errorf("%w: LINGOW_DELIVERY_CONSUMER is required in production", domain.ErrInvalidArgument)
	}
	return nil
}
