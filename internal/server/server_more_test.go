package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/matrix-notifier/internal/chart"
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
	sender, _, h, gotifyToken, _ := newTestServer(t)
	req := httptest.NewRequest("POST", "/message", strings.NewReader(`{"title":"no message"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", gotifyToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, sender.Sent())
}

func TestGotifyFormEncoded(t *testing.T) {
	sender, _, h, gotifyToken, _ := newTestServer(t)
	form := url.Values{"title": {"F"}, "message": {"form body"}}
	req := httptest.NewRequest("POST", "/message", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Gotify-Key", gotifyToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, sender.Sent(), 1)
	assert.Equal(t, "form body", sender.Sent()[0].n.Body)
}

// Full path integration: chart-flagged channel + chart-annotated alert +
// reachable "Prometheus" must produce ONE image-with-caption message and no
// separate text message.
func TestAlertmanagerChartPath(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[{"metric":{"__name__":"up"},"values":[[1720000000,"1"]]}]}}`)
	}))
	defer prom.Close()

	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	_, err = st.CreateChannel(context.Background(), "charty", "!chart:x", true)
	require.NoError(t, err)
	tok, _, err := st.CreateToken(context.Background(), "t", store.KindAlertmanager, "charty", "")
	require.NoError(t, err)
	sender := &recordingSender{}
	h := New(slog.New(slog.DiscardHandler), sender, st, chart.New(prom.URL))

	payload := `{"version":"4","status":"firing","groupLabels":{"alertname":"X"},
		"alerts":[{"status":"firing","labels":{"alertname":"X"},
		           "annotations":{"summary":"s","chart":"true"},
		           "generatorURL":"http://p/graph?g0.expr=up"}]}`
	req := httptest.NewRequest("POST", "/alertmanager", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// Chart delivery is async; wait for it.
	require.Eventually(t, func() bool { return len(sender.Images()) == 1 }, 5*time.Second, 20*time.Millisecond)
	assert.Equal(t, "!chart:x", sender.Images()[0])
	// The combined message replaces the plain text one.
	assert.Empty(t, sender.Sent())
}

// Same setup but the alert did NOT opt in: plain text, no chart.
func TestAlertmanagerChartRequiresAnnotation(t *testing.T) {
	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	_, err = st.CreateChannel(context.Background(), "charty", "!chart:x", true)
	require.NoError(t, err)
	tok, _, err := st.CreateToken(context.Background(), "t", store.KindAlertmanager, "charty", "")
	require.NoError(t, err)
	sender := &recordingSender{}
	h := New(slog.New(slog.DiscardHandler), sender, st, chart.New("http://unreachable.invalid"))

	payload := `{"version":"4","alerts":[{"status":"firing","labels":{"alertname":"X"},"generatorURL":"http://p/graph?g0.expr=up"}]}`
	req := httptest.NewRequest("POST", "/alertmanager", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, sender.Sent(), 1)
	assert.Empty(t, sender.Images())
}

// Chart channels degrade to text when Prometheus is down — the notification
// itself must never be lost to a chart failure.
func TestAlertmanagerChartFailureDegradesToText(t *testing.T) {
	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	_, err = st.CreateChannel(context.Background(), "charty", "!chart:x", true)
	require.NoError(t, err)
	tok, _, err := st.CreateToken(context.Background(), "t", store.KindAlertmanager, "charty", "")
	require.NoError(t, err)
	sender := &recordingSender{}
	h := New(slog.New(slog.DiscardHandler), sender, st, chart.New("http://127.0.0.1:1"))

	payload := `{"version":"4","alerts":[{"status":"firing","labels":{"alertname":"X"},
		"annotations":{"chart":"true"},"generatorURL":"http://p/graph?g0.expr=up"}]}`
	req := httptest.NewRequest("POST", "/alertmanager", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	require.Eventually(t, func() bool { return len(sender.Sent()) == 1 }, 5*time.Second, 20*time.Millisecond)
	assert.Empty(t, sender.Images())
	assert.Contains(t, sender.Sent()[0].n.Title, "FIRING:1")
}

// The token's prefix must show up on delivered notifications — on the title
// when there is one, on the body otherwise.
func TestTokenPrefixApplied(t *testing.T) {
	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	_, err = st.CreateChannel(context.Background(), "c", "!r:x", false)
	require.NoError(t, err)
	tok, _, err := st.CreateToken(context.Background(), "sonarr", store.KindGotify, "c", "📺")
	require.NoError(t, err)
	sender := &recordingSender{}
	h := New(slog.New(slog.DiscardHandler), sender, st, nil)

	req := httptest.NewRequest("POST", "/message", strings.NewReader(`{"title":"Episode grabbed","message":"body"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, sender.Sent(), 1)
	assert.Equal(t, "📺 Episode grabbed", sender.Sent()[0].n.Title)

	// Title-less message: prefix lands on the body instead of vanishing.
	req = httptest.NewRequest("POST", "/message", strings.NewReader(`{"message":"no title"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", tok)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "📺 no title", sender.Sent()[1].n.Body)
}
