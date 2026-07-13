package gotify

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gotify clients send JSON, urlencoded forms, or multipart forms; all three
// must produce the same notification or existing tooling breaks silently.

func TestParseJSON(t *testing.T) {
	r := httptest.NewRequest("POST", "/message", strings.NewReader(
		`{"title":"Backup done","message":"**42 GB** copied","priority":5}`))
	r.Header.Set("Content-Type", "application/json")

	n, err := Parse(r)
	require.NoError(t, err)
	assert.Equal(t, "Backup done", n.Title)
	assert.Equal(t, "**42 GB** copied", n.Body)
	assert.Equal(t, 5, n.Priority)
}

func TestParseForm(t *testing.T) {
	form := url.Values{"title": {"Hi"}, "message": {"body text"}, "priority": {"8"}}
	r := httptest.NewRequest("POST", "/message", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	n, err := Parse(r)
	require.NoError(t, err)
	assert.Equal(t, "Hi", n.Title)
	assert.Equal(t, "body text", n.Body)
	assert.Equal(t, 8, n.Priority)
}

func TestParseRejectsEmptyMessage(t *testing.T) {
	// Gotify returns 400 for a missing message; we must not deliver empty
	// notifications either.
	r := httptest.NewRequest("POST", "/message", strings.NewReader(`{"title":"no body"}`))
	r.Header.Set("Content-Type", "application/json")

	_, err := Parse(r)
	require.Error(t, err)
}

func TestParseRejectsBadPriority(t *testing.T) {
	form := url.Values{"message": {"x"}, "priority": {"high"}}
	r := httptest.NewRequest("POST", "/message", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	_, err := Parse(r)
	require.Error(t, err)
}
