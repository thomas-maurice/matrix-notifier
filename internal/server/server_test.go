package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/matrix-notifier/internal/config"
	"github.com/thomas-maurice/matrix-notifier/internal/store"
)

// recordingQueue captures what the ingest endpoints enqueue; delivery itself
// is the dispatcher's job (tested in internal/outbox).
type recordingQueue struct {
	entries []*store.OutboxEntry
	err     error
}

func (q *recordingQueue) Enqueue(_ context.Context, e *store.OutboxEntry) error {
	if q.err != nil {
		return q.err
	}
	q.entries = append(q.entries, e)
	return nil
}

type fakeHealth struct {
	healthy bool
	reason  string
}

func (f *fakeHealth) Healthy() (bool, string) { return f.healthy, f.reason }

// newTestServer builds a server backed by a real in-memory store with one
// channel ("alerts" → !room:example.org) and one gotify-kind token plus one
// any-kind token.
func newTestServer(t *testing.T) (*recordingQueue, *store.Store, http.Handler, string, string) {
	t.Helper()
	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	_, err = st.CreateChannel(context.Background(), "alerts", "!room:example.org", false)
	require.NoError(t, err)
	gotifyToken, _, err := st.CreateToken(context.Background(), "gotify-only", store.KindGotify, "alerts", "")
	require.NoError(t, err)
	anyToken, _, err := st.CreateToken(context.Background(), "any-kind", store.KindAny, "alerts", "")
	require.NoError(t, err)
	q := &recordingQueue{}
	return q, st, New(slog.New(slog.DiscardHandler), &fakeHealth{healthy: true}, st, q, nil), gotifyToken, anyToken
}

// Every ingest endpoint is a write path into the user's Matrix rooms; an
// unauthenticated request must never result in a queued message.
func TestRejectsMissingOrWrongToken(t *testing.T) {
	q, _, h, _, _ := newTestServer(t)
	for _, target := range []string{"/message", "/alertmanager", "/message?token=mn_wrong"} {
		req := httptest.NewRequest("POST", target, strings.NewReader(`{"message":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code, target)
	}
	assert.Empty(t, q.entries)
}

// A token restricted to one endpoint kind must not open the other endpoint.
func TestTokenKindIsEnforced(t *testing.T) {
	q, _, h, gotifyToken, _ := newTestServer(t)
	payload := `{"version":"4","alerts":[{"status":"firing","labels":{"alertname":"X"}}]}`
	req := httptest.NewRequest("POST", "/alertmanager", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+gotifyToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Empty(t, q.entries)
}

func TestGotifyEndpointRoutesToChannelRoom(t *testing.T) {
	q, _, h, gotifyToken, _ := newTestServer(t)
	set := []func(r *http.Request){
		func(r *http.Request) { r.Header.Set("X-Gotify-Key", gotifyToken) },
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+gotifyToken) },
		func(r *http.Request) { r.URL.RawQuery = "token=" + gotifyToken },
	}
	for i, apply := range set {
		req := httptest.NewRequest("POST", "/message", strings.NewReader(`{"title":"t","message":"hello"}`))
		req.Header.Set("Content-Type", "application/json")
		apply(req)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "token variant %d", i)
		// Gotify clients check for a message object with an id.
		assert.Contains(t, w.Body.String(), `"id"`)
	}
	require.Len(t, q.entries, 3)
	// The notification must be queued for the room the token's channel maps to.
	assert.Equal(t, "!room:example.org", q.entries[0].RoomID)
	assert.Equal(t, "alerts", q.entries[0].Channel)
	assert.Equal(t, "hello", q.entries[0].Body)
	assert.Equal(t, "gotify", q.entries[0].Kind)
}

func TestAlertmanagerEndpoint(t *testing.T) {
	q, _, h, _, anyToken := newTestServer(t)
	payload := `{"version":"4","status":"firing","groupLabels":{"alertname":"Down"},
		"alerts":[{"status":"firing","labels":{"alertname":"Down","severity":"critical"},"annotations":{"summary":"it broke"}}]}`
	req := httptest.NewRequest("POST", "/alertmanager", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+anyToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, q.entries, 1)
	assert.Equal(t, "!room:example.org", q.entries[0].RoomID)
	assert.Contains(t, q.entries[0].Title, "FIRING:1")
	assert.Contains(t, q.entries[0].Body, "it broke")
}

// Slack-webhook senders can't set headers, so ?token= must carry auth, and
// some check for Slack's literal "ok" body — anything else reads as failure.
func TestSlackEndpoint(t *testing.T) {
	q, _, h, _, anyToken := newTestServer(t)
	req := httptest.NewRequest("POST", "/slack?token="+anyToken,
		strings.NewReader(`{"text":"pool degraded","username":"TrueNAS"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ok", w.Body.String())
	require.Len(t, q.entries, 1)
	assert.Equal(t, "!room:example.org", q.entries[0].RoomID)
	assert.Equal(t, "pool degraded", q.entries[0].Body)
	assert.Equal(t, "TrueNAS", q.entries[0].Title)
}

func TestHealthIsUnauthenticated(t *testing.T) {
	_, _, h, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}
