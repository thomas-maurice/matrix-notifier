package matrix

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseCommand(t *testing.T) {
	// Only messages explicitly addressed to the bot may trigger a reply —
	// a chatty room must not cause accidental responses.
	for _, body := range []string{"hello", "", "notify ping", "! notify ping", "x !notify ping"} {
		_, _, ok := ParseCommand(body)
		assert.False(t, ok, "%q must not parse as a command", body)
	}

	cmd, args, ok := ParseCommand("!notify ping")
	assert.True(t, ok)
	assert.Equal(t, "ping", cmd)
	assert.Empty(t, args)

	// Bare prefix gets help rather than silence, so the bot is discoverable.
	cmd, _, ok = ParseCommand("!notify")
	assert.True(t, ok)
	assert.Equal(t, "help", cmd)

	// Case-insensitive command, args preserved.
	cmd, args, ok = ParseCommand("!notify STATUS verbose")
	assert.True(t, ok)
	assert.Equal(t, "status", cmd)
	assert.Equal(t, []string{"verbose"}, args)
}
