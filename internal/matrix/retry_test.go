package matrix

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetrySend(t *testing.T) {
	b := &Bot{retryBase: time.Millisecond}
	ctx := context.Background()

	// First-try success calls fn exactly once.
	calls := 0
	require.NoError(t, b.retrySend(ctx, func() error { calls++; return nil }))
	assert.Equal(t, 1, calls)

	// A transient failure is retried and then succeeds — the notification
	// survives a brief homeserver blip.
	calls = 0
	err := b.retrySend(ctx, func() error {
		calls++
		if calls < 2 {
			return errors.New("connection refused")
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, calls)

	// Persistent failure gives up after sendMaxAttempts and surfaces the error.
	calls = 0
	err = b.retrySend(ctx, func() error { calls++; return errors.New("down") })
	require.Error(t, err)
	assert.Equal(t, sendMaxAttempts, calls)
	assert.Contains(t, err.Error(), "down")
}

func TestRetrySendHonoursContext(t *testing.T) {
	b := &Bot{retryBase: time.Hour} // long backoff so cancellation is what ends it
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := b.retrySend(ctx, func() error { calls++; return errors.New("x") })
	require.Error(t, err)
	// One attempt, then the cancelled context aborts before any backoff wait.
	assert.Equal(t, 1, calls)
}
