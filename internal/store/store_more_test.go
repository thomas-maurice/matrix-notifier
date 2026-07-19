package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashTokenIsDeterministicAndOneWay(t *testing.T) {
	// Lookups depend on determinism; storage safety depends on the hash not
	// being the plaintext.
	h1 := HashToken("tcsn_abc")
	assert.Equal(t, h1, HashToken("tcsn_abc"))
	assert.NotEqual(t, h1, HashToken("tcsn_abd"))
	assert.NotContains(t, h1, "tcsn_abc")
	assert.Len(t, h1, 64) // sha256 hex
}

func TestAnyKindTokenWorksOnBothEndpoints(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, err := st.CreateChannel(ctx, "c", "!r:x", false)
	require.NoError(t, err)
	tok, _, err := st.CreateToken(ctx, "t", KindAny, "c", "", nil)
	require.NoError(t, err)

	for _, kind := range []TokenKind{KindGotify, KindAlertmanager} {
		tok2, err := st.ResolveToken(ctx, tok, kind)
		require.NoError(t, err, kind)
		assert.Equal(t, "!r:x", tok2.Channel.RoomID)
	}
}

func TestUpdateChannelRoomID(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, err := st.CreateChannel(ctx, "c", "#alias:x", false)
	require.NoError(t, err)

	require.NoError(t, st.UpdateChannelRoomID(ctx, "c", "!resolved:x"))
	ch, err := st.GetChannel(ctx, "c")
	require.NoError(t, err)
	assert.Equal(t, "!resolved:x", ch.RoomID)

	assert.ErrorIs(t, st.UpdateChannelRoomID(ctx, "ghost", "!x:y"), ErrNotFound)
}

// The leave-room cascade must delete exactly the channels of that room —
// with their tokens — and leave other rooms untouched.
func TestDeleteChannelsForRoom(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, err := st.CreateChannel(ctx, "doomed-a", "!dead:x", false)
	require.NoError(t, err)
	_, err = st.CreateChannel(ctx, "doomed-b", "!dead:x", true)
	require.NoError(t, err)
	_, err = st.CreateChannel(ctx, "survivor", "!alive:x", false)
	require.NoError(t, err)
	_, _, err = st.CreateToken(ctx, "doomed-tok", KindAny, "doomed-a", "", nil)
	require.NoError(t, err)
	surviving, _, err := st.CreateToken(ctx, "survivor-tok", KindAny, "survivor", "", nil)
	require.NoError(t, err)

	deleted, err := st.DeleteChannelsForRoom(ctx, "!dead:x")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"doomed-a", "doomed-b"}, deleted)

	channels, err := st.ListChannels(ctx)
	require.NoError(t, err)
	require.Len(t, channels, 1)
	assert.Equal(t, "survivor", channels[0].Name)

	// The doomed token must be gone, the survivor still resolving.
	tokens, err := st.ListTokens(ctx)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	_, err = st.ResolveToken(ctx, surviving, KindGotify)
	assert.NoError(t, err)

	// Idempotent on an unknown room.
	deleted, err = st.DeleteChannelsForRoom(ctx, "!nothing:x")
	require.NoError(t, err)
	assert.Empty(t, deleted)
}

func TestGetChannelNotFound(t *testing.T) {
	st := newTestStore(t)
	_, err := st.GetChannel(context.Background(), "ghost")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestChartFlagPersists(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, err := st.CreateChannel(ctx, "charty", "!r:x", true)
	require.NoError(t, err)
	ch, err := st.GetChannel(ctx, "charty")
	require.NoError(t, err)
	assert.True(t, ch.Chart)
}

func TestUpdateChannelChart(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, err := st.CreateChannel(ctx, "c", "!r:x", false)
	require.NoError(t, err)

	ch, err := st.UpdateChannelChart(ctx, "c", true)
	require.NoError(t, err)
	assert.True(t, ch.Chart)
	ch, err = st.UpdateChannelChart(ctx, "c", false)
	require.NoError(t, err)
	assert.False(t, ch.Chart)

	_, err = st.UpdateChannelChart(ctx, "ghost", true)
	assert.ErrorIs(t, err, ErrNotFound)
}

// A prefix must survive the round trip and be editable in place: producers
// keep their credential while the operator restyles notifications.
func TestTokenPrefix(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, err := st.CreateChannel(ctx, "c", "!r:x", false)
	require.NoError(t, err)
	plaintext, tok, err := st.CreateToken(ctx, "sonarr", KindGotify, "c", "📺", nil)
	require.NoError(t, err)
	assert.Equal(t, "📺", tok.Prefix)

	resolved, err := st.ResolveToken(ctx, plaintext, KindGotify)
	require.NoError(t, err)
	assert.Equal(t, "📺", resolved.Prefix)

	updated, err := st.UpdateToken(ctx, "sonarr", "🎬", "", nil, false)
	require.NoError(t, err)
	assert.Equal(t, "🎬", updated.Prefix)
	assert.Equal(t, "c", updated.Channel.Name) // empty channel leaves it unchanged
	_, err = st.UpdateToken(ctx, "ghost", "x", "", nil, false)
	assert.ErrorIs(t, err, ErrNotFound)
}

// Reassigning a token to another channel changes the room it delivers to,
// without re-minting the credential producers already hold.
func TestUpdateTokenChannel(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, err := st.CreateChannel(ctx, "old", "!old:x", false)
	require.NoError(t, err)
	_, err = st.CreateChannel(ctx, "new", "!new:x", false)
	require.NoError(t, err)
	plaintext, _, err := st.CreateToken(ctx, "prom", KindAlertmanager, "old", "🔥", nil)
	require.NoError(t, err)

	updated, err := st.UpdateToken(ctx, "prom", "🔥", "new", nil, false)
	require.NoError(t, err)
	assert.Equal(t, "new", updated.Channel.Name)
	assert.Equal(t, "🔥", updated.Prefix) // prefix preserved

	// The same credential now resolves to the new room.
	resolved, err := st.ResolveToken(ctx, plaintext, KindAlertmanager)
	require.NoError(t, err)
	assert.Equal(t, "!new:x", resolved.Channel.RoomID)

	// Reassigning to a non-existent channel is a clean not-found, no partial write.
	_, err = st.UpdateToken(ctx, "prom", "🔥", "ghost", nil, false)
	assert.ErrorIs(t, err, ErrNotFound)
	resolved, _ = st.ResolveToken(ctx, plaintext, KindAlertmanager)
	assert.Equal(t, "!new:x", resolved.Channel.RoomID) // unchanged after the failed update
}
