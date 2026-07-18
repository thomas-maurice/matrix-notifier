package slack

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/matrix-notifier/internal/notify"
)

func parseJSON(t *testing.T, body string) notify.Notification {
	t.Helper()
	r := httptest.NewRequest("POST", "/slack", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	n, err := Parse(r)
	require.NoError(t, err)
	return n
}

func TestSimpleText(t *testing.T) {
	n := parseJSON(t, `{"text":"disk is full"}`)
	assert.Equal(t, "disk is full", n.Body)
	assert.Empty(t, n.Title)
	assert.Equal(t, 3, n.Priority)
}

// Slack's legacy form encoding wraps the JSON in a `payload` field — senders
// using it must work, and a form without one must be a clear error.
func TestFormPayload(t *testing.T) {
	form := url.Values{"payload": {`{"text":"hello","username":"TrueNAS"}`}}
	r := httptest.NewRequest("POST", "/slack", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	n, err := Parse(r)
	require.NoError(t, err)
	assert.Equal(t, "hello", n.Body)
	assert.Equal(t, "TrueNAS", n.Title)

	r = httptest.NewRequest("POST", "/slack", strings.NewReader("other=x"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_, err = Parse(r)
	require.Error(t, err)
}

// mrkdwn labeled links render as garbage in markdown unless converted, and
// Slack-escaped entities must come back out as their real characters.
func TestMrkdwnConversion(t *testing.T) {
	n := parseJSON(t, `{"text":"see <https://grafana.example/d/abc|the dashboard> &amp; act &lt;now&gt;"}`)
	assert.Equal(t, "see [the dashboard](https://grafana.example/d/abc) & act <now>", n.Body)
}

// Attachment color is how Slack senders flag severity: danger must outrank
// the default so real alerts stand out, resolved/plain ones must not.
func TestAttachmentsAndPriority(t *testing.T) {
	n := parseJSON(t, `{"text":"intro","attachments":[
		{"title":"Pool degraded","text":"tank: one disk offline","color":"danger"}]}`)
	assert.Contains(t, n.Body, "**Pool degraded**")
	assert.Contains(t, n.Body, "tank: one disk offline")
	assert.Contains(t, n.Body, "intro")
	assert.Equal(t, 5, n.Priority)

	n = parseJSON(t, `{"attachments":[{"fallback":"only a fallback","color":"warning"}]}`)
	assert.Equal(t, "only a fallback", n.Body)
	assert.Equal(t, 4, n.Priority)
}

func TestBlocks(t *testing.T) {
	n := parseJSON(t, `{"blocks":[
		{"type":"header","text":{"type":"plain_text","text":"Backup failed"}},
		{"type":"section","text":{"type":"mrkdwn","text":"cloud sync task x errored"}},
		{"type":"divider"}]}`)
	assert.Equal(t, "**Backup failed**\ncloud sync task x errored", n.Body)
}

// A payload with no usable text anywhere must 400, not deliver an empty
// message to the room.
func TestEmptyPayloadRejected(t *testing.T) {
	r := httptest.NewRequest("POST", "/slack", strings.NewReader(`{"username":"ghost"}`))
	r.Header.Set("Content-Type", "application/json")
	_, err := Parse(r)
	require.Error(t, err)
}
