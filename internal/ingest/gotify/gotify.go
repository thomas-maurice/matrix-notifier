// Package gotify parses notifications in the format of the Gotify server's
// POST /message endpoint, so tools that speak Gotify can point at this bot
// unmodified.
package gotify

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"

	"github.com/thomas-maurice/matrix-notifier/internal/notify"
)

// Message mirrors the request body of Gotify's POST /message.
type Message struct {
	Title    string         `json:"title"`
	Message  string         `json:"message"`
	Priority int            `json:"priority"`
	Extras   map[string]any `json:"extras"`
}

// Parse reads a Gotify-format message from an HTTP request. Gotify accepts
// JSON, urlencoded-form, and multipart-form bodies; so do we.
func Parse(r *http.Request) (notify.Notification, error) {
	var msg Message
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch contentType {
	case "application/json":
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			return notify.Notification{}, fmt.Errorf("invalid JSON body: %w", err)
		}
	default:
		// ParseMultipartForm falls back to ParseForm for urlencoded bodies.
		if err := r.ParseMultipartForm(1 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
			return notify.Notification{}, fmt.Errorf("invalid form body: %w", err)
		}
		msg.Title = r.FormValue("title")
		msg.Message = r.FormValue("message")
		if p := r.FormValue("priority"); p != "" {
			prio, err := strconv.Atoi(p)
			if err != nil {
				return notify.Notification{}, fmt.Errorf("invalid priority %q: %w", p, err)
			}
			msg.Priority = prio
		}
	}
	if msg.Message == "" {
		return notify.Notification{}, fmt.Errorf("message is required")
	}
	return notify.Notification{
		Title:    msg.Title,
		Body:     msg.Message,
		Priority: msg.Priority,
	}, nil
}
