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
