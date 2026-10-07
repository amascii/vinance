package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEmitsJSON(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, slog.LevelInfo).With("component", "test").Info("hello", "n", 1)

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	assert.Equal(t, "hello", rec["msg"])
	assert.Equal(t, "INFO", rec["level"])
	assert.Equal(t, "test", rec["component"])
}

func TestNewRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, slog.LevelWarn).Info("hidden")
	assert.Empty(t, buf.String())
}
