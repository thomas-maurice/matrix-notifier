package matrix

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// The mautrix verificationhelper resolves in-room transactions from evt.ID,
// but transactions are keyed by the REQUEST event's ID (in m.relates_to).
// The normalizer must rewrite peer events and neutralize our own echoes —
// both behaviours are what makes interactive verification work at all.
func TestNormalizeInRoomVerificationEvent(t *testing.T) {
	self := id.UserID("@bot:x")

	ready := &event.VerificationReadyEventContent{}
	ready.SetRelatesTo(&event.RelatesTo{Type: event.RelReference, EventID: "$request-event"})

	// Peer event: evt.ID must become the relates-to (transaction) event ID.
	peer := &event.Event{
		ID:      "$ready-event",
		Sender:  "@admin:x",
		Content: event.Content{Parsed: ready},
	}
	normalizeInRoomVerificationEvent(peer, self)
	assert.Equal(t, id.EventID("$request-event"), peer.ID)

	// Our own echo: must be neutralized (blank ID → helper drops it),
	// otherwise it resolves to the live transaction and cancels it.
	echo := &event.Event{
		ID:      "$echo-event",
		Sender:  self,
		Content: event.Content{Parsed: ready},
	}
	normalizeInRoomVerificationEvent(echo, self)
	assert.Empty(t, echo.ID)

	// No relates_to (malformed peer event): leave the ID alone.
	bare := &event.Event{
		ID:      "$bare",
		Sender:  "@admin:x",
		Content: event.Content{Parsed: &event.VerificationReadyEventContent{}},
	}
	normalizeInRoomVerificationEvent(bare, self)
	assert.Equal(t, id.EventID("$bare"), bare.ID)
}
