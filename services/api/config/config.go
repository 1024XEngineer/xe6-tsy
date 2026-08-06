// Package config owns API process configuration and startup validation.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

const (
	providerUnconfigured        = "unconfigured"
	providerFakeEmail           = "fake_email"
	providerSMTP                = "smtp"
	defaultRealtimeHTTPTimeout  = 5 * time.Second
	maxRealtimeHTTPTimeout      = 5 * time.Second
	minRealtimeTicketSecretSize = 32
	minRecordsSystemTokenSize   = 32
)

// Config contains only process configuration. Secrets are kept as strings at
// this boundary and are consumed by the runtime constructor; they are never
// included in logs or serialized responses.
type Config struct {
	AppEnv                string
	APIAddr               string
	DatabaseURL           string
	RedisURL              string
	JWTSecret             string
	JWTIssuer             string
	JWTAudience           string
	SessionRuntimeEnabled bool
	RealtimeBaseURL       string
	RealtimeTicketSecret  string
	RealtimeHTTPTimeout   time.Duration
	DestinationKey        string
	DeliveryEnabled       bool
	DeliveryProvider      string
	DeliveryConsumer      string
	DeliveryStream        string
	DeliveryGroup         string
	DeliveryDelayStream   string
	DeliveryDelayKey      string
	UsageStream           string
	UsageGroup            string
	UsageConsumer         string
	SMTPHost              string
	SMTPPort              string
	SMTPUser              string
	SMTPPassword          string
	SMTPFrom              string
	SMTPTLS               bool
	WeComCorpID           string
	WeComCorpSecret       string
	WeComAgentID          string
	RecordsSystemToken    string
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
		AppEnv:                value("APP_ENV", "local"),
		APIAddr:               value("API_ADDR", ":8080"),
		DatabaseURL:           value("DATABASE_URL", ""),
		RedisURL:              value("REDIS_URL", ""),
		JWTSecret:             value("JWT_SECRET", ""),
		JWTIssuer:             value("JWT_ISSUER", "lingow-api"),
		JWTAudience:           value("JWT_AUDIENCE", "lingow-client"),
		SessionRuntimeEnabled: false,
		RealtimeBaseURL:       value("REALTIME_BASE_URL", ""),
		RealtimeTicketSecret:  value("REALTIME_TICKET_SECRET", ""),
		RealtimeHTTPTimeout:   defaultRealtimeHTTPTimeout,
		DestinationKey:        value("LINGOW_DELIVERY_DESTINATION_KEY", ""),
		DeliveryProvider:      value("LINGOW_DELIVERY_PROVIDER", providerUnconfigured),
		DeliveryConsumer:      value("LINGOW_DELIVERY_CONSUMER", ""),
		DeliveryStream:        value("LINGOW_DELIVERY_STREAM", ""),
		DeliveryGroup:         value("LINGOW_DELIVERY_GROUP", ""),
		DeliveryDelayStream:   value("LINGOW_DELIVERY_DELAY_STREAM", ""),
		DeliveryDelayKey:      value("LINGOW_DELIVERY_DELAY_KEY", ""),
		UsageStream:           value("LINGOW_USAGE_STREAM", ""),
		UsageGroup:            value("LINGOW_USAGE_GROUP", ""),
		UsageConsumer:         value("LINGOW_USAGE_CONSUMER", ""),
		SMTPHost:              value("LINGOW_SMTP_HOST", ""),
		SMTPPort:              value("LINGOW_SMTP_PORT", "587"),
		SMTPUser:              value("LINGOW_SMTP_USER", ""),
		SMTPPassword:          value("LINGOW_SMTP_PASSWORD", ""),
		SMTPFrom:              value("LINGOW_SMTP_FROM", ""),
		SMTPTLS:               parseBoolDefault(value("LINGOW_SMTP_TLS", "true"), true),
		WeComCorpID:           value("LINGOW_WECOM_CORP_ID", ""),
		WeComCorpSecret:       value("LINGOW_WECOM_CORP_SECRET", ""),
		WeComAgentID:          value("LINGOW_WECOM_AGENT_ID", ""),
		RecordsSystemToken:    value("LINGOW_RECORDS_SYSTEM_TOKEN", ""),
	}
	sessionRuntimeMode := strings.ToLower(value("LINGOW_SESSION_RUNTIME", "disabled"))
	switch sessionRuntimeMode {
	case "disabled", "false", "0", "":
		config.SessionRuntimeEnabled = false
	case "enabled", "true", "1":
		config.SessionRuntimeEnabled = true
	default:
		return Config{}, fmt.Errorf("%w: LINGOW_SESSION_RUNTIME must be enabled or disabled", domain.ErrInvalidArgument)
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
	if err := validateCore(config); err != nil {
		return Config{}, err
	}
	if config.SessionRuntimeEnabled {
		realtimeHTTPTimeout, err := parseDuration(value("REALTIME_HTTP_TIMEOUT", defaultRealtimeHTTPTimeout.String()))
		if err != nil {
			return Config{}, fmt.Errorf("%w: REALTIME_HTTP_TIMEOUT must be a valid duration", domain.ErrInvalidArgument)
		}
		config.RealtimeHTTPTimeout = realtimeHTTPTimeout
		if err := validateSessionRuntime(config); err != nil {
			return Config{}, err
		}
	}
	if !config.DeliveryEnabled {
		return config, nil
	}
	if err := validateEnabled(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func validateCore(config Config) error {
	for _, required := range []struct {
		key   string
		value string
	}{
		{key: "DATABASE_URL", value: config.DatabaseURL},
		{key: "JWT_SECRET", value: config.JWTSecret},
		{key: "JWT_ISSUER", value: config.JWTIssuer},
		{key: "JWT_AUDIENCE", value: config.JWTAudience},
	} {
		if required.value == "" {
			return fmt.Errorf("%w: %s is required", domain.ErrInvalidArgument, required.key)
		}
	}
	if len([]byte(config.JWTSecret)) < 32 {
		return fmt.Errorf("%w: JWT_SECRET must contain at least 32 bytes", domain.ErrInvalidArgument)
	}
	if config.RecordsSystemToken != "" && len([]byte(config.RecordsSystemToken)) < minRecordsSystemTokenSize {
		return fmt.Errorf("%w: LINGOW_RECORDS_SYSTEM_TOKEN must contain at least %d bytes when configured", domain.ErrInvalidArgument, minRecordsSystemTokenSize)
	}
	return nil
}

func validateSessionRuntime(config Config) error {
	for _, required := range []struct {
		key   string
		value string
	}{
		{key: "REALTIME_BASE_URL", value: config.RealtimeBaseURL},
		{key: "REALTIME_TICKET_SECRET", value: config.RealtimeTicketSecret},
	} {
		if required.value == "" {
			return fmt.Errorf("%w: %s is required when session runtime is enabled", domain.ErrInvalidArgument, required.key)
		}
	}
	if len([]byte(config.RealtimeTicketSecret)) < minRealtimeTicketSecretSize {
		return fmt.Errorf("%w: REALTIME_TICKET_SECRET must contain at least 32 bytes", domain.ErrInvalidArgument)
	}
	if err := validateRealtimeBaseURL(config.RealtimeBaseURL); err != nil {
		return err
	}
	if config.RealtimeHTTPTimeout <= 0 || config.RealtimeHTTPTimeout > maxRealtimeHTTPTimeout {
		return fmt.Errorf("%w: REALTIME_HTTP_TIMEOUT must be between 1ns and %s", domain.ErrInvalidArgument, maxRealtimeHTTPTimeout)
	}
	return nil
}

func validateRealtimeBaseURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: REALTIME_BASE_URL must be a valid HTTP or HTTPS URL", domain.ErrInvalidArgument)
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("%w: REALTIME_BASE_URL must use http or https", domain.ErrInvalidArgument)
	}
	if parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		parsed.Opaque != "" {
		return fmt.Errorf("%w: REALTIME_BASE_URL must include only scheme, host, and optional path", domain.ErrInvalidArgument)
	}
	return nil
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
	case providerSMTP:
		if config.SMTPHost == "" || config.SMTPFrom == "" {
			return fmt.Errorf("%w: LINGOW_SMTP_HOST and LINGOW_SMTP_FROM are required when LINGOW_DELIVERY_PROVIDER=smtp", domain.ErrInvalidArgument)
		}
	default:
		return fmt.Errorf("%w: unsupported delivery provider %q", domain.ErrInvalidArgument, config.DeliveryProvider)
	}
	if err := validateWeComConfig(config); err != nil {
		return err
	}
	if strings.EqualFold(config.AppEnv, "production") && config.DeliveryConsumer == "" {
		return fmt.Errorf("%w: LINGOW_DELIVERY_CONSUMER is required in production", domain.ErrInvalidArgument)
	}
	return nil
}

func parseDuration(raw string) (time.Duration, error) {
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, domain.ErrInvalidArgument
	}
	return value, nil
}

func parseBoolDefault(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

// SMTPPortInt returns the configured SMTP port or the supplied default.
func (c Config) SMTPPortInt(defaultPort int) int {
	port, err := strconv.Atoi(strings.TrimSpace(c.SMTPPort))
	if err != nil || port <= 0 {
		return defaultPort
	}
	return port
}

func validateWeComConfig(config Config) error {
	values := []string{config.WeComCorpID, config.WeComCorpSecret, config.WeComAgentID}
	set := 0
	for _, value := range values {
		if value != "" {
			set++
		}
	}
	if set == 0 {
		return nil
	}
	if set != len(values) {
		return fmt.Errorf("%w: LINGOW_WECOM_CORP_ID, LINGOW_WECOM_CORP_SECRET, and LINGOW_WECOM_AGENT_ID must be configured together", domain.ErrInvalidArgument)
	}
	if config.WeComAgentIDInt() <= 0 {
		return fmt.Errorf("%w: LINGOW_WECOM_AGENT_ID must be a positive integer", domain.ErrInvalidArgument)
	}
	return nil
}

// WeComAgentIDInt returns the configured WeCom agent id or zero when invalid.
func (c Config) WeComAgentIDInt() int {
	agentID, err := strconv.Atoi(strings.TrimSpace(c.WeComAgentID))
	if err != nil || agentID <= 0 {
		return 0
	}
	return agentID
}

func (c Config) String() string {
	return c.redacted().format()
}

func (c Config) GoString() string {
	return c.redacted().format()
}

func (c Config) LogValue() slog.Value {
	return slog.StringValue(c.String())
}

func (c Config) redacted() Config {
	if c.DatabaseURL != "" {
		c.DatabaseURL = "[redacted]"
	}
	if c.RedisURL != "" {
		c.RedisURL = "[redacted]"
	}
	if c.JWTSecret != "" {
		c.JWTSecret = "[redacted]"
	}
	if c.DatabaseURL != "" {
		c.DatabaseURL = "[redacted]"
	}
	if c.RedisURL != "" {
		c.RedisURL = "[redacted]"
	}
	if c.RealtimeTicketSecret != "" {
		c.RealtimeTicketSecret = "[redacted]"
	}
	if c.DestinationKey != "" {
		c.DestinationKey = "[redacted]"
	}
	if c.SMTPPassword != "" {
		c.SMTPPassword = "[redacted]"
	}
	if c.WeComCorpSecret != "" {
		c.WeComCorpSecret = "[redacted]"
	}
	if c.RecordsSystemToken != "" {
		c.RecordsSystemToken = "[redacted]"
	}
	return c
}

func (c Config) format() string {
	type plain Config
	return fmt.Sprintf("%+v", plain(c))
}
