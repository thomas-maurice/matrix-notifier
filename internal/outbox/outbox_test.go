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

type edit struct {
	target string
	n      notify.Notification
}

type fakeSender struct {
	err     error
	editErr error
	sent    []notify.Notification
	images  []string
	edits   []edit
}

func (f *fakeSender) Send(_ context.Context, _ string, n notify.Notification) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.sent = append(f.sent, n)
	return fmt.Sprintf("$e%d", len(f.sent)), nil
}

func (f *fakeSender) SendWithImage(_ context.Context, _ string, _ notify.Notification, filename string, _ []byte) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.images = append(f.images, filename)
	return fmt.Sprintf("$img%d", len(f.images)), nil
}

func (f *fakeSender) SendEdit(_ context.Context, _ string, target string, n notify.Notification) error {
	if f.editErr != nil {
		return f.editErr
	}
	f.edits = append(f.edits, edit{target: target, n: n})
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

// The point of correlation: a resolved notification edits the firing
// message in place instead of posting an unrelated second message.
func TestDispatcherResolvesByEdit(t *testing.T) {
	d, sender, st := newTestDispatcher(t)
	ctx := context.Background()

	require.NoError(t, d.Enqueue(ctx, &store.OutboxEntry{
		Channel: "infra", RoomID: "!r:x", Kind: "alertmanager",
		Title: "[FIRING:1] Down", Body: "🔥", Fingerprints: "fp1,fp2",
	}))
	d.drain(ctx)
	require.Len(t, sender.sent, 1)

	require.NoError(t, d.Enqueue(ctx, &store.OutboxEntry{
		Channel: "infra", RoomID: "!r:x", Kind: "alertmanager",
		Title: "[RESOLVED:1] Down", Body: "✅", ResolveFingerprints: "fp1,fp2",
	}))
	d.drain(ctx)

	require.Len(t, sender.edits, 1, "resolution must edit, not re-post")
	assert.Equal(t, "$e1", sender.edits[0].target, "the FIRING message is the edit target")
	assert.Equal(t, "[RESOLVED:1] Down", sender.edits[0].n.Title)
	assert.Len(t, sender.sent, 1, "no standalone resolved message")

	list, err := st.ListOutbox(ctx, "", 10)
	require.NoError(t, err)
	assert.Equal(t, store.DeliveryDelivered, list[0].Status, "an edit counts as delivered")

	// Alertmanager re-sends resolved group notifications (group_interval,
	// flapping): a repeat must re-edit the same message idempotently, never
	// post a duplicate standalone message.
	require.NoError(t, d.Enqueue(ctx, &store.OutboxEntry{
		Channel: "infra", RoomID: "!r:x", Kind: "alertmanager",
		Title: "[RESOLVED:1] Down", Body: "✅", ResolveFingerprints: "fp1",
	}))
	d.drain(ctx)
	require.Len(t, sender.edits, 2)
	assert.Equal(t, "$e1", sender.edits[1].target, "the repeat edits the same firing message")
	assert.Len(t, sender.sent, 1, "no duplicate standalone message on repeated resolution")
}

// Grouped alerts keep ONE live message: a partial resolution (A resolved,
// B still firing, no NEW alert) edits the group message in place; a payload
// introducing an unannounced firing alert must post fresh — an edit pings
// nobody.
func TestDispatcherPartialResolutionEditsInPlace(t *testing.T) {
	d, sender, _ := newTestDispatcher(t)
	ctx := context.Background()

	// Group fires with two alerts → one message ($e1).
	require.NoError(t, d.Enqueue(ctx, &store.OutboxEntry{
		Channel: "infra", RoomID: "!r:x", Kind: "alertmanager",
		Title: "[FIRING:2] Down", Body: "🔥🔥", Fingerprints: "fpA,fpB",
	}))
	d.drain(ctx)
	require.Len(t, sender.sent, 1)

	// A resolves, B keeps firing: the group message is updated in place.
	require.NoError(t, d.Enqueue(ctx, &store.OutboxEntry{
		Channel: "infra", RoomID: "!r:x", Kind: "alertmanager",
		Title: "[FIRING:1, RESOLVED:1] Down", Body: "🔥✅",
		Fingerprints: "fpB", ResolveFingerprints: "fpA",
	}))
	d.drain(ctx)
	require.Len(t, sender.edits, 1, "partial resolution must edit the group message")
	assert.Equal(t, "$e1", sender.edits[0].target)
	assert.Contains(t, sender.edits[0].n.Title, "RESOLVED:1")
	assert.Len(t, sender.sent, 1, "no new message for a pure status change")

	// A new alert C joins while A stays resolved: must POST (people need
	// the ping), and the firing alerts re-point to the new message.
	require.NoError(t, d.Enqueue(ctx, &store.OutboxEntry{
		Channel: "infra", RoomID: "!r:x", Kind: "alertmanager",
		Title: "[FIRING:2, RESOLVED:1] Down", Body: "🔥🔥✅",
		Fingerprints: "fpB,fpC", ResolveFingerprints: "fpA",
	}))
	d.drain(ctx)
	assert.Len(t, sender.edits, 1)
	require.Len(t, sender.sent, 2, "an unannounced firing alert must post a new message")

	// Everything resolves: the NEWEST group message ($e2) gets the final
	// flip.
	require.NoError(t, d.Enqueue(ctx, &store.OutboxEntry{
		Channel: "infra", RoomID: "!r:x", Kind: "alertmanager",
		Title: "[RESOLVED:3] Down", Body: "✅✅✅", ResolveFingerprints: "fpA,fpB,fpC",
	}))
	d.drain(ctx)
	// fpA still maps to $e1 while fpB/fpC map to $e2 → split targets →
	// posts normally rather than editing two messages with one rendering.
	require.Len(t, sender.sent, 3)

	// But a final resolution whose fingerprints all live on one message
	// edits it: resolve only fpB+fpC.
	require.NoError(t, d.Enqueue(ctx, &store.OutboxEntry{
		Channel: "infra", RoomID: "!r:x", Kind: "alertmanager",
		Title: "[RESOLVED:2] Down", Body: "✅✅", ResolveFingerprints: "fpB,fpC",
	}))
	d.drain(ctx)
	require.Len(t, sender.edits, 2)
	assert.Equal(t, "$e2", sender.edits[1].target)
}

// Unknown fingerprints (never announced, pruned, restart before the
// feature) must not lose the resolved notification — it posts normally.
func TestDispatcherResolveFallsBackWithoutMapping(t *testing.T) {
	d, sender, _ := newTestDispatcher(t)
	ctx := context.Background()

	require.NoError(t, d.Enqueue(ctx, &store.OutboxEntry{
		Channel: "infra", RoomID: "!r:x", Kind: "grafana",
		Title: "[RESOLVED:1] X", Body: "✅", ResolveFingerprints: "ghost",
	}))
	d.drain(ctx)
	assert.Empty(t, sender.edits)
	require.Len(t, sender.sent, 1)
}

// A failed edit (redacted original, purged history) must degrade to a
// normal message, never a dropped notification.
func TestDispatcherEditFailureFallsBack(t *testing.T) {
	d, sender, _ := newTestDispatcher(t)
	ctx := context.Background()

	require.NoError(t, d.Enqueue(ctx, &store.OutboxEntry{
		Channel: "infra", RoomID: "!r:x", Kind: "alertmanager", Title: "f", Body: "b", Fingerprints: "fp1",
	}))
	d.drain(ctx)

	sender.editErr = errors.New("event redacted")
	require.NoError(t, d.Enqueue(ctx, &store.OutboxEntry{
		Channel: "infra", RoomID: "!r:x", Kind: "alertmanager", Title: "r", Body: "✅", ResolveFingerprints: "fp1",
	}))
	d.drain(ctx)
	assert.Empty(t, sender.edits)
	require.Len(t, sender.sent, 2, "edit failure must fall back to a normal send")
}

// Backoff must grow (no hot-loop hammering a down homeserver) but stay
// capped so recovery is picked up within minutes, not hours.
func TestBackoffDoublesAndCaps(t *testing.T) {
	assert.Equal(t, 10*time.Second, backoff(1))
	assert.Equal(t, 20*time.Second, backoff(2))
	assert.Equal(t, 40*time.Second, backoff(3))
	assert.Equal(t, backoffCap, backoff(50))
}
