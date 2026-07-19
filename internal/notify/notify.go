package notify

import "context"

// Notification is the normalized form every ingest format is converted to.
type Notification struct {
	Title string
	// Body is markdown; the sender renders it to Matrix HTML.
	Body string
	// Priority follows the Gotify scale (0-10, >=8 is emergency).
	Priority int
}

// Sender delivers a notification to a destination room, returning the
// Matrix event ID of the sent message (empty when nothing was sent).
type Sender interface {
	Send(ctx context.Context, roomID string, n Notification) (eventID string, err error)
}
