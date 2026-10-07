// Package config reads vinance settings from environment variables.
package config

import (
	"log/slog"
	"os"
)

type Config struct {
	DBPath   string
	Addr     string
	LogLevel slog.Level
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	c := Config{
		DBPath:   getenv("VINANCE_DB", "./data/vinance.db"),
		Addr:     getenv("VINANCE_ADDR", "127.0.0.1:8080"),
		LogLevel: slog.LevelInfo,
	}
	if err := c.LogLevel.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		c.LogLevel = slog.LevelInfo
	}
	return c
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
