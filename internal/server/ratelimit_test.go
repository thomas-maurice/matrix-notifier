package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLimiters(t *testing.T) {
	// Disabled (0/s) never throttles — the feature must be opt-out-able.
	assert.True(t, (*limiters)(nil).allow("x"))
	assert.True(t, newLimiters(0, 10) == nil)

	// burst=2 lets two through immediately, then blocks the third.
	l := newLimiters(0.0001, 2)
	assert.True(t, l.allow("tok"))
	assert.True(t, l.allow("tok"))
	assert.False(t, l.allow("tok"), "third request over burst must be denied")

	// A different token has its own bucket.
	assert.True(t, l.allow("other"))
}
