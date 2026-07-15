package config

import "os"

type Config struct {
	Address string
	Mode    string
}

func Load() Config {
	return Config{
		Address: valueOrDefault("XE6_API_ADDRESS", "127.0.0.1:8080"),
		Mode:    valueOrDefault("XE6_GIN_MODE", "release"),
	}
}

func valueOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
