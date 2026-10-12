// Package config reads vinance settings from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"

	"golang.org/x/crypto/bcrypt"
)

type Config struct {
	DBPath   string
	Addr     string
	LogLevel slog.Level

	// PasswordHash is the bcrypt hash of the login password (`vinance hash-password`). Empty means no login.
	PasswordHash string
	// SecureCookies marks the session cookie Secure; set it when the app is reached over HTTPS (Tailscale serve, a tunnel).
	SecureCookies bool
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	c := Config{
		DBPath:   getenv("VINANCE_DB", "./data/vinance.db"),
		Addr:     getenv("VINANCE_ADDR", "127.0.0.1:8080"),
		LogLevel: slog.LevelInfo,

		PasswordHash:  os.Getenv("VINANCE_PASSWORD_HASH"),
		SecureCookies: isTrue(os.Getenv("VINANCE_SECURE_COOKIES")),
	}
	if err := c.LogLevel.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		c.LogLevel = slog.LevelInfo
	}
	return c
}

// Validate refuses configurations that would put an unprotected app on the network.
func (c Config) Validate() error {
	if c.PasswordHash != "" {
		if _, err := bcrypt.Cost([]byte(c.PasswordHash)); err != nil {
			return fmt.Errorf("VINANCE_PASSWORD_HASH is not a bcrypt hash (make one with `vinance hash-password`; single-quote it in a shell, it contains $): %w", err)
		}
		return nil
	}
	if !loopback(c.Addr) {
		return fmt.Errorf("VINANCE_PASSWORD_HASH is required when listening on %s: only a loopback address may run without a login", c.Addr)
	}
	return nil
}

// loopback reports whether addr (host:port) binds only to this machine.
func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isTrue(s string) bool { b, _ := strconv.ParseBool(s); return b }

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
