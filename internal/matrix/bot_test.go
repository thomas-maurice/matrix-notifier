package matrix

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/thomas-maurice/matrix-notifier/internal/notify"
)

func TestBuildMarkdown(t *testing.T) {
	// The title must stand out from the body, and emergency-priority
	// notifications must be visually flagged.
	md := BuildMarkdown(notify.Notification{Title: "Disk full", Body: "on `node1`", Priority: 5})
	assert.Equal(t, "**Disk full**\n\non `node1`", md)

	md = BuildMarkdown(notify.Notification{Title: "Disk full", Body: "x", Priority: 8})
	assert.Equal(t, "‼️ **Disk full**\n\nx", md)

	// Title-less messages (common from scripts) must not produce stray markup.
	md = BuildMarkdown(notify.Notification{Body: "just text"})
	assert.Equal(t, "just text", md)
}

func TestValidateRoomID(t *testing.T) {
	// Raw room IDs bypass server-side alias resolution, so this shape check
	// is the only thing stopping arbitrary garbage from being stored as a
	// channel's routing target.
	valid := []string{
		"!abc:example.org",
		"!abc:localhost",
		"!ZSMDPrDDasLroDNPJh:localhost",
		"!31hneApxJ_1o-63DmFrpeg", // room v12+ (MSC4291): no server part
	}
	for _, room := range valid {
		assert.NoError(t, ValidateRoomID(room), room)
	}

	invalid := []string{
		"",
		"garbage",
		"room:example.org",       // missing sigil
		"!",                      // empty opaque part
		"!:example.org",          // empty opaque part
		"!abc:",                  // empty server part
		"!abc def:example.org",   // whitespace
		"!abc\n:example.org",     // control char
		"#alias:example.org",     // aliases resolve elsewhere, not a raw ID
		"@user:example.org",      // wrong sigil
		"!" + string(rune(0xE9)), // non-ASCII
	}
	for _, room := range invalid {
		assert.Error(t, ValidateRoomID(room), room)
	}
}
