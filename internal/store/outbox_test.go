package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func enqueueTestEntry(t *testing.T, st *Store, channel string) *OutboxEntry {
	t.Helper()
	e := &OutboxEntry{Channel: channel, RoomID: "!r:x", Kind: "gotify", Title: "t", Body: "b", Priority: 5}
	require.NoError(t, st.EnqueueOutbox(context.Background(), e))
	return e
}

// The outbox is the durability guarantee: an enqueued notification must be
// due immediately, and rescheduling must keep it out of the due set until
// its next attempt.
func TestOutboxQueueLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	e := enqueueTestEntry(t, st, "infra")
	assert.Equal(t, DeliveryPending, e.Status)

	due, err := st.DueOutbox(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.Len(t, due, 1)

	// A rescheduled entry must not be retried before its backoff elapses.
	require.NoError(t, st.RescheduleOutbox(ctx, e.ID, 1, time.Now().Add(time.Minute), "boom"))
	due, err = st.DueOutbox(ctx, time.Now(), 10)
	require.NoError(t, err)
	assert.Empty(t, due)
	// ...but becomes due once the clock passes it.
	due, err = st.DueOutbox(ctx, time.Now().Add(2*time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, 1, due[0].Attempts)
	assert.Equal(t, "boom", due[0].LastError)

	require.NoError(t, st.MarkOutboxDelivered(ctx, e.ID, 2))
	due, err = st.DueOutbox(ctx, time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	assert.Empty(t, due, "delivered entries must leave the queue")

	list, err := st.ListOutbox(ctx, "", 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, DeliveryDelivered, list[0].Status)
	assert.Empty(t, list[0].LastError, "delivery clears the transient error")
	assert.NotNil(t, list[0].DeliveredAt)
}

// Requeue is the UI "retry" button: it must only revive failed entries —
// re-sending a delivered notification would duplicate it.
func TestOutboxRequeueOnlyFailed(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	e := enqueueTestEntry(t, st, "infra")
	require.NoError(t, st.MarkOutboxFailed(ctx, e.ID, 3, "gave up"))

	require.NoError(t, st.RequeueOutbox(ctx, e.ID))
	due, err := st.DueOutbox(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, 0, due[0].Attempts, "requeue restarts the backoff schedule")

	require.NoError(t, st.MarkOutboxDelivered(ctx, e.ID, 1))
	err = st.RequeueOutbox(ctx, e.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestOutboxListFiltersAndOrders(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	enqueueTestEntry(t, st, "infra")
	second := enqueueTestEntry(t, st, "other")

	list, err := st.ListOutbox(ctx, "", 10)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, second.ID, list[0].ID, "history is newest first")

	list, err = st.ListOutbox(ctx, "other", 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "other", list[0].Channel)
}

// Retention must only ever remove terminal entries: pruning a pending row
// would silently drop a queued notification.
func TestOutboxPruneKeepsPending(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	pending := enqueueTestEntry(t, st, "infra")
	done := enqueueTestEntry(t, st, "infra")
	require.NoError(t, st.MarkOutboxDelivered(ctx, done.ID, 1))

	// Cutoff in the future: everything qualifies by age.
	n, err := st.PruneOutbox(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	list, err := st.ListOutbox(ctx, "", 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, pending.ID, list[0].ID)

	count, err := st.CountPendingOutbox(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// RecordDelivery backfills history for synchronous sends (test
// notifications); a delivered record must carry its timestamp.
func TestOutboxRecordDelivery(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	e := &OutboxEntry{Channel: "infra", RoomID: "!r:x", Kind: "test", Body: "b", Status: DeliveryDelivered, Attempts: 1}
	require.NoError(t, st.RecordDelivery(ctx, e))
	list, err := st.ListOutbox(ctx, "", 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.NotNil(t, list[0].DeliveredAt)

	due, err := st.DueOutbox(ctx, time.Now(), 10)
	require.NoError(t, err)
	assert.Empty(t, due, "recorded terminal entries must not enter the queue")
}
