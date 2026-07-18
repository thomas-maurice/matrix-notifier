package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/matrix-notifier/internal/chart"
	"github.com/thomas-maurice/matrix-notifier/internal/config"
	"github.com/thomas-maurice/matrix-notifier/internal/notify"
	"github.com/thomas-maurice/matrix-notifier/internal/store"
)

type fakeSender struct {
	err    error
	sent   []notify.Notification
	images []string
}

func (f *fakeSender) Send(_ context.Context, _ string, n notify.Notification) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, n)
	return nil
}

func (f *fakeSender) SendWithImage(_ context.Context, _ string, _ notify.Notification, filename string, _ []byte) error {
	if f.err != nil {
		return f.err
	}
	f.images = append(f.images, filename)
	return nil
}

func newTestDispatcher(t *testing.T) (*Dispatcher, *fakeSender, *store.Store) {
	t.Helper()
	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	sender := &fakeSender{}
	return New(slog.Default(), st, sender, nil, time.Hour), sender, st
}

// The point of the outbox: a notification that fails to send is NOT lost —
// it stays queued with backoff and goes out once Matrix recovers.
func TestDispatcherRetriesUntilDelivered(t *testing.T) {
	d, sender, st := newTestDispatcher(t)
	ctx := context.Background()
	sender.err = errors.New("matrix down")

	require.NoError(t, d.Enqueue(ctx, &store.OutboxEntry{
		Channel: "infra", RoomID: "!r:x", Kind: "gotify", Title: "t", Body: "b", Priority: 5,
	}))
	d.drain(ctx)

	list, err := st.ListOutbox(ctx, "", 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	e := list[0]
	assert.Equal(t, store.DeliveryPending, e.Status, "failed send must stay queued")
	assert.Equal(t, 1, e.Attempts)
	assert.Contains(t, e.LastError, "matrix down")
	assert.True(t, e.NextAttemptAt.After(time.Now()), "retry must be scheduled in the future")

	// Matrix recovers; force the entry due instead of waiting out the backoff.
	sender.err = nil
	require.NoError(t, st.RescheduleOutbox(ctx, e.ID, e.Attempts, time.Now().Add(-time.Second), e.LastError))
	d.drain(ctx)

	require.Len(t, sender.sent, 1)
	assert.Equal(t, "t", sender.sent[0].Title)
	list, err = st.ListOutbox(ctx, "", 10)
	require.NoError(t, err)
	assert.Equal(t, store.DeliveryDelivered, list[0].Status)
	assert.Equal(t, 2, list[0].Attempts)
}

// Past maxPendingAge the dispatcher stops retrying: the entry becomes a
// visible failure in the history instead of retrying forever.
func TestDispatcherGivesUpAtMaxAge(t *testing.T) {
	old := maxPendingAge
	maxPendingAge = 0
	t.Cleanup(func() { maxPendingAge = old })

	d, sender, st := newTestDispatcher(t)
	ctx := context.Background()
	sender.err = errors.New("permanently broken")

	require.NoError(t, d.Enqueue(ctx, &store.OutboxEntry{
		Channel: "infra", RoomID: "!r:x", Kind: "gotify", Body: "b",
	}))
	d.drain(ctx)

	list, err := st.ListOutbox(ctx, "", 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, store.DeliveryFailed, list[0].Status)
	assert.Contains(t, list[0].LastError, "permanently broken")

	// A failed entry must not be retried by the loop anymore.
	d.drain(ctx)
	list, _ = st.ListOutbox(ctx, "", 10)
	assert.Equal(t, 1, list[0].Attempts)
}

func chartEntry() *store.OutboxEntry {
	startsAt := time.Now().Add(-10 * time.Minute)
	return &store.OutboxEntry{
		Channel: "charty", RoomID: "!chart:x", Kind: "alertmanager",
		Title: "FIRING:1 X", Body: "s",
		ChartGeneratorURL: "http://p/graph?g0.expr=up",
		ChartStartsAt:     &startsAt,
		ChartAlertName:    "X",
	}
}

// An entry carrying a chart target must go out as ONE image-with-caption
// message, not a separate text message.
func TestDispatcherRendersChart(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[{"metric":{"__name__":"up"},"values":[[1720000000,"1"]]}]}}`)
	}))
	defer prom.Close()

	d, sender, st := newTestDispatcher(t)
	d.charts = chart.New(prom.URL)
	ctx := context.Background()

	require.NoError(t, d.Enqueue(ctx, chartEntry()))
	d.drain(ctx)

	require.Len(t, sender.images, 1)
	assert.Equal(t, "X.png", sender.images[0])
	assert.Empty(t, sender.sent, "the combined message replaces the plain text one")
	list, _ := st.ListOutbox(ctx, "", 10)
	assert.Equal(t, store.DeliveryDelivered, list[0].Status)
}

// Chart rendering is best-effort: when Prometheus is down the notification
// itself must still be delivered as text — never lost to a chart failure.
func TestDispatcherChartFailureDegradesToText(t *testing.T) {
	d, sender, st := newTestDispatcher(t)
	d.charts = chart.New("http://127.0.0.1:1")
	ctx := context.Background()

	require.NoError(t, d.Enqueue(ctx, chartEntry()))
	d.drain(ctx)

	assert.Empty(t, sender.images)
	require.Len(t, sender.sent, 1)
	assert.Contains(t, sender.sent[0].Title, "FIRING:1")
	list, _ := st.ListOutbox(ctx, "", 10)
	assert.Equal(t, store.DeliveryDelivered, list[0].Status)
}

// Backoff must grow (no hot-loop hammering a down homeserver) but stay
// capped so recovery is picked up within minutes, not hours.
func TestBackoffDoublesAndCaps(t *testing.T) {
	assert.Equal(t, 10*time.Second, backoff(1))
	assert.Equal(t, 20*time.Second, backoff(2))
	assert.Equal(t, 40*time.Second, backoff(3))
	assert.Equal(t, backoffCap, backoff(50))
}
