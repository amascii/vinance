package config

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
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
