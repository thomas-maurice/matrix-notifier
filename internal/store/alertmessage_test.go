package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fingerprint→message mapping powers update-in-place: record must
// point at the NEWEST announcement, and lookups must NOT consume —
// Alertmanager re-sends group notifications, and each repeat re-edits the
// same message instead of posting a duplicate.
func TestAlertMessageMapping(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, st.RecordAlertMessages(ctx, "!r:x", "$old", []string{"fp1", "fp2"}))
	// Re-firing replaces the mapping: resolution targets the newest message.
	require.NoError(t, st.RecordAlertMessages(ctx, "!r:x", "$new", []string{"fp1", "fp2"}))

	m, err := st.MapAlertMessages(ctx, "!r:x", []string{"fp1", "fp2"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"fp1": "$new", "fp2": "$new"}, m)

	m, err = st.MapAlertMessages(ctx, "!r:x", []string{"fp1"})
	require.NoError(t, err)
	assert.Equal(t, "$new", m["fp1"], "repeated webhooks must find the mapping again")

	// Mappings are room-scoped: the same fingerprint in another room must
	// not leak edits across rooms.
	require.NoError(t, st.RecordAlertMessages(ctx, "!a:x", "$a", []string{"fp9"}))
	m, err = st.MapAlertMessages(ctx, "!b:x", []string{"fp9"})
	require.NoError(t, err)
	assert.Empty(t, m)
}

// Alerts that never resolve must not accumulate mappings forever.
func TestAlertMessagePrune(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, st.RecordAlertMessages(ctx, "!r:x", "$e", []string{"fp1"}))
	n, err := st.PruneAlertMessages(ctx, time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	m, err := st.MapAlertMessages(ctx, "!r:x", []string{"fp1"})
	require.NoError(t, err)
	assert.Empty(t, m)
}
