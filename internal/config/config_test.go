package config

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("VINANCE_DB", "")
	t.Setenv("VINANCE_ADDR", "")
	t.Setenv("LOG_LEVEL", "")
	c := Load()
	assert.Equal(t, "./data/vinance.db", c.DBPath)
	assert.Equal(t, "127.0.0.1:8080", c.Addr)
	assert.Equal(t, slog.LevelInfo, c.LogLevel)
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("VINANCE_DB", "/tmp/x.db")
	t.Setenv("VINANCE_ADDR", ":9000")
	t.Setenv("LOG_LEVEL", "debug")
	c := Load()
	assert.Equal(t, "/tmp/x.db", c.DBPath)
	assert.Equal(t, ":9000", c.Addr)
	assert.Equal(t, slog.LevelDebug, c.LogLevel)
}

func TestLoadBadLogLevelFallsBack(t *testing.T) {
	t.Setenv("LOG_LEVEL", "nonsense")
	assert.Equal(t, slog.LevelInfo, Load().LogLevel)
}

func TestValidate(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	require.NoError(t, err)

	for addr, ok := range map[string]bool{
		"127.0.0.1:8080": true, "localhost:8080": true, "[::1]:8080": true,
		"0.0.0.0:8080": false, ":8080": false, "192.168.1.5:8080": false, "nonsense": false,
	} {
		err := Config{Addr: addr}.Validate()
		if ok {
			assert.NoError(t, err, addr)
		} else {
			assert.ErrorContains(t, err, "VINANCE_PASSWORD_HASH is required", addr)
		}
	}

	assert.NoError(t, Config{Addr: "0.0.0.0:8080", PasswordHash: string(hash)}.Validate(), "a password makes any address fine")
	assert.ErrorContains(t, Config{Addr: "127.0.0.1:8080", PasswordHash: "hunter2"}.Validate(), "not a bcrypt hash")
}

func TestLoadAuthSettings(t *testing.T) {
	t.Setenv("VINANCE_PASSWORD_HASH", "h")
	t.Setenv("VINANCE_SECURE_COOKIES", "true")
	c := Load()
	assert.Equal(t, "h", c.PasswordHash)
	assert.True(t, c.SecureCookies)

	t.Setenv("VINANCE_SECURE_COOKIES", "")
	assert.False(t, Load().SecureCookies)
}
