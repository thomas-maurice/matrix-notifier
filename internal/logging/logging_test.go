package logging

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLevel(t *testing.T) {
	for input, want := range map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"":      slog.LevelInfo, // unset config means info, not an error
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	} {
		got, err := ParseLevel(input)
		require.NoError(t, err, input)
		assert.Equal(t, want, got, input)
	}
	// A typo in the config must fail startup, not silently log at info.
	_, err := ParseLevel("verbose")
	assert.Error(t, err)
}

func TestContextRoundTrip(t *testing.T) {
	logger := New(slog.LevelInfo)
	ctx := Into(context.Background(), logger)
	assert.Same(t, logger, From(ctx))
	// Absent logger falls back to the default instead of panicking mid-request.
	assert.NotNil(t, From(context.Background()))
}
