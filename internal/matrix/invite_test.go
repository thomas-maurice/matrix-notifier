package matrix

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"maunium.net/go/mautrix/id"
)

// Auto-join is the bot's only entry into rooms; a federated stranger must
// never be able to pull it in.
func TestInviterAllowed(t *testing.T) {
	self := id.UserID("@notifier:example.org")

	// Default (no allowlist): own homeserver only.
	assert.True(t, InviterAllowed("@thomas:example.org", self, nil))
	assert.False(t, InviterAllowed("@attacker:evil.net", self, nil))
	// Same localpart on another server must not slip through.
	assert.False(t, InviterAllowed("@thomas:evil.net", self, nil))

	// Explicit allowlist replaces the default entirely.
	allowed := []string{"friends.org"}
	assert.True(t, InviterAllowed("@x:friends.org", self, allowed))
	assert.False(t, InviterAllowed("@thomas:example.org", self, allowed))
}
