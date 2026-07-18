package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/matrix-notifier/internal/config"
	"github.com/thomas-maurice/matrix-notifier/internal/store"
)

func TestVersionEndpointLooksLikeGotify(t *testing.T) {
	_, _, h, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/version", nil))
	// Gotify clients probe /version to detect the server; it must answer
	// without auth and contain a version field.
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"version"`)
}

func TestGotifyBadBodyIs400NotDelivery(t *testing.T) {
	q, _, h, gotifyToken, _ := newTestServer(t)
	req := httptest.NewRequest("POST", "/message", strings.NewReader(`{"title":"no message"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", gotifyToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, q.entries)
}

func TestGotifyFormEncoded(t *testing.T) {
	q, _, h, gotifyToken, _ := newTestServer(t)
	form := url.Values{"title": {"F"}, "message": {"form body"}}
	req := httptest.NewRequest("POST", "/message", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Gotify-Key", gotifyToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, q.entries, 1)
	assert.Equal(t, "form body", q.entries[0].Body)
}

// newChartServer builds a server whose channel has charts enabled.
func newChartServer(t *testing.T) (*recordingQueue, http.Handler, string) {
	t.Helper()
	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	_, err = st.CreateChannel(context.Background(), "charty", "!chart:x", true)
	require.NoError(t, err)
	tok, _, err := st.CreateToken(context.Background(), "t", store.KindAlertmanager, "charty", "", nil)
	require.NoError(t, err)
	q := &recordingQueue{}
	return q, New(slog.New(slog.DiscardHandler), &fakeHealth{healthy: true}, st, q, nil), tok
}

// A chart-flagged channel + chart-annotated alert must queue ONE entry
// carrying the chart target; rendering happens in the dispatcher at send
// time.
func TestAlertmanagerChartTargetQueued(t *testing.T) {
	q, h, tok := newChartServer(t)
	payload := `{"version":"4","status":"firing","groupLabels":{"alertname":"X"},
		"alerts":[{"status":"firing","labels":{"alertname":"X"},
		           "annotations":{"summary":"s","chart":"true"},
		           "generatorURL":"http://p/graph?g0.expr=up"}]}`
	req := httptest.NewRequest("POST", "/alertmanager", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	require.Len(t, q.entries, 1)
	e := q.entries[0]
	assert.Equal(t, "http://p/graph?g0.expr=up", e.ChartGeneratorURL)
	assert.Equal(t, "X", e.ChartAlertName)
	require.NotNil(t, e.ChartStartsAt)
}

// Same setup but the alert did NOT opt in: the entry must carry no chart
// target, so the dispatcher sends plain text.
func TestAlertmanagerChartRequiresAnnotation(t *testing.T) {
	q, h, tok := newChartServer(t)
	payload := `{"version":"4","alerts":[{"status":"firing","labels":{"alertname":"X"},"generatorURL":"http://p/graph?g0.expr=up"}]}`
	req := httptest.NewRequest("POST", "/alertmanager", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, q.entries, 1)
	assert.Empty(t, q.entries[0].ChartGeneratorURL)
}

// The token's prefix must show up on queued notifications — on the title
// when there is one, on the body otherwise.
func TestTokenPrefixApplied(t *testing.T) {
	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	_, err = st.CreateChannel(context.Background(), "c", "!r:x", false)
	require.NoError(t, err)
	tok, _, err := st.CreateToken(context.Background(), "sonarr", store.KindGotify, "c", "📺", nil)
	require.NoError(t, err)
	q := &recordingQueue{}
	h := New(slog.New(slog.DiscardHandler), &fakeHealth{healthy: true}, st, q, nil)

	req := httptest.NewRequest("POST", "/message", strings.NewReader(`{"title":"Episode grabbed","message":"body"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, q.entries, 1)
	assert.Equal(t, "📺 Episode grabbed", q.entries[0].Title)

	// Title-less message: prefix lands on the body instead of vanishing.
	req = httptest.NewRequest("POST", "/message", strings.NewReader(`{"message":"no title"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", tok)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "📺 no title", q.entries[1].Body)
}

// A runaway producer must be throttled, not allowed to flood the room.
func TestRateLimitReturns429(t *testing.T) {
	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	_, err = st.CreateChannel(context.Background(), "c", "!r:x", false)
	require.NoError(t, err)
	tok, _, err := st.CreateToken(context.Background(), "flood", store.KindGotify, "c", "", nil)
	require.NoError(t, err)
	q := &recordingQueue{}
	h := New(slog.New(slog.DiscardHandler), &fakeHealth{healthy: true}, st, q, NewLimiters(0.0001, 1))

	codes := make([]int, 0, 3)
	for range 3 {
		req := httptest.NewRequest("POST", "/message", strings.NewReader(`{"message":"spam"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Gotify-Key", tok)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		codes = append(codes, w.Code)
	}
	assert.Equal(t, http.StatusOK, codes[0])
	assert.Equal(t, http.StatusTooManyRequests, codes[2])
	assert.Len(t, q.entries, 1, "only the first request should have been queued")
}

// When the database refuses the enqueue, the producer must see a 500 — a
// silent 200 would drop the notification with no trace anywhere.
func TestEnqueueFailureIs500(t *testing.T) {
	q, _, h, gotifyToken, _ := newTestServer(t)
	q.err = errors.New("database on fire")
	req := httptest.NewRequest("POST", "/message", strings.NewReader(`{"message":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", gotifyToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// /health must reflect the bot's real state so traefik/docker see a zombie.
func TestHealthReflectsBotState(t *testing.T) {
	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	health := &fakeHealth{healthy: false, reason: "no sync yet"}
	h := New(slog.New(slog.DiscardHandler), health, st, &recordingQueue{}, nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "no sync yet")

	health.healthy = true
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

// /metrics must expose the Prometheus registry unauthenticated.
func TestMetricsEndpoint(t *testing.T) {
	_, _, h, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "matrix_notifier_")
}
