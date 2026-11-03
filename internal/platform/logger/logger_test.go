package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogger_AddsRequestIDFromContext(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo).With(slog.String("component", "test"))

	log.InfoContext(WithRequestID(context.Background(), "abc"), "hello")

	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	assert.Equal(t, "hello", line["msg"])
	assert.Equal(t, "abc", line["request_id"])
	assert.Equal(t, "test", line["component"])
}

func TestLogger_NoRequestIDWithoutContextValue(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, slog.LevelInfo).InfoContext(context.Background(), "hello")

	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	assert.NotContains(t, line, "request_id")
}

func TestLogger_RespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelWarn)

	log.Info("dropped")
	assert.Empty(t, buf.String())

	log.Warn("kept")
	assert.Contains(t, buf.String(), "kept")
}

func TestRequestID_EmptyWhenMissing(t *testing.T) {
	assert.Empty(t, RequestID(context.Background()))
}
