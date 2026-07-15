package config

import "os"

type Config struct {
	Address string
	Mode    string
}

func Load() Config {
	address := valueOrDefault("XE6_API_ADDRESS", "127.0.0.1:8080")
	if port := os.Getenv("PORT"); port != "" {
		address = ":" + port
	}
	return Config{
		Address: address,
		Mode:    valueOrDefault("XE6_GIN_MODE", "release"),
	}
}

func valueOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
