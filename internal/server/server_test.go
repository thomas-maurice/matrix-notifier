package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/matrix-notifier/internal/config"
	"github.com/thomas-maurice/matrix-notifier/internal/notify"
	"github.com/thomas-maurice/matrix-notifier/internal/store"
)

type sent struct {
	room string
	n    notify.Notification
}

// recordingSender is written to by the async chart goroutine and read by
// tests: it must be locked or the race detector rightly complains.
type recordingSender struct {
	mu     sync.Mutex
	sent   []sent
	images []string
}

func (r *recordingSender) Send(_ context.Context, roomID string, n notify.Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, sent{room: roomID, n: n})
	return nil
}

func (r *recordingSender) SendWithImage(_ context.Context, roomID string, _ notify.Notification, _ string, _ []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.images = append(r.images, roomID)
	return nil
}

func (r *recordingSender) Sent() []sent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sent(nil), r.sent...)
}

func (r *recordingSender) Images() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.images...)
}

func (r *recordingSender) Healthy() (bool, string) { return true, "ok" }

// newTestServer builds a server backed by a real in-memory store with one
// channel ("alerts" → !room:x) and one gotify-kind token plus one any-kind
// token.
func newTestServer(t *testing.T) (*recordingSender, *store.Store, http.Handler, string, string) {
	t.Helper()
	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	_, err = st.CreateChannel(context.Background(), "alerts", "!room:example.org", false)
	require.NoError(t, err)
	gotifyToken, _, err := st.CreateToken(context.Background(), "gotify-only", store.KindGotify, "alerts", "")
	require.NoError(t, err)
	anyToken, _, err := st.CreateToken(context.Background(), "any-kind", store.KindAny, "alerts", "")
	require.NoError(t, err)
	sender := &recordingSender{}
	return sender, st, New(slog.New(slog.DiscardHandler), sender, st, nil, nil), gotifyToken, anyToken
}

// Every ingest endpoint is a write path into the user's Matrix rooms; an
// unauthenticated request must never result in a delivered message.
func TestRejectsMissingOrWrongToken(t *testing.T) {
	sender, _, h, _, _ := newTestServer(t)
	for _, target := range []string{"/message", "/alertmanager", "/message?token=mn_wrong"} {
		req := httptest.NewRequest("POST", target, strings.NewReader(`{"message":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code, target)
	}
	assert.Empty(t, sender.Sent())
}

// A token restricted to one endpoint kind must not open the other endpoint.
func TestTokenKindIsEnforced(t *testing.T) {
	sender, _, h, gotifyToken, _ := newTestServer(t)
	payload := `{"version":"4","alerts":[{"status":"firing","labels":{"alertname":"X"}}]}`
	req := httptest.NewRequest("POST", "/alertmanager", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+gotifyToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Empty(t, sender.Sent())
}

func TestGotifyEndpointRoutesToChannelRoom(t *testing.T) {
	sender, _, h, gotifyToken, _ := newTestServer(t)
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
	require.Len(t, sender.Sent(), 3)
	// The notification must land in the room the token's channel maps to.
	assert.Equal(t, "!room:example.org", sender.Sent()[0].room)
	assert.Equal(t, "hello", sender.Sent()[0].n.Body)
}

func TestAlertmanagerEndpoint(t *testing.T) {
	sender, _, h, _, anyToken := newTestServer(t)
	payload := `{"version":"4","status":"firing","groupLabels":{"alertname":"Down"},
		"alerts":[{"status":"firing","labels":{"alertname":"Down","severity":"critical"},"annotations":{"summary":"it broke"}}]}`
	req := httptest.NewRequest("POST", "/alertmanager", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+anyToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, sender.Sent(), 1)
	assert.Equal(t, "!room:example.org", sender.Sent()[0].room)
	assert.Contains(t, sender.Sent()[0].n.Title, "FIRING:1")
	assert.Contains(t, sender.Sent()[0].n.Body, "it broke")
}

func TestHealthIsUnauthenticated(t *testing.T) {
	_, _, h, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}
