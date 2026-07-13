package store

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/matrix-notifier/internal/config"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	return st
}

func TestTokenLifecycleAndResolution(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_, err := st.CreateChannel(ctx, "infra", "!infra:example.org", false)
	require.NoError(t, err)

	plaintext, tok, err := st.CreateToken(ctx, "prom", KindAlertmanager, "infra")
	require.NoError(t, err)
	// The plaintext must be usable and must never be stored as-is.
	assert.True(t, strings.HasPrefix(plaintext, "mn_"))
	assert.NotEqual(t, plaintext, tok.TokenHash)

	// Resolution routes to the channel's room, restricted by kind.
	ch, err := st.ResolveToken(ctx, plaintext, KindAlertmanager)
	require.NoError(t, err)
	assert.Equal(t, "!infra:example.org", ch.RoomID)

	// A token restricted to alertmanager must not authenticate gotify.
	_, err = st.ResolveToken(ctx, plaintext, KindGotify)
	assert.ErrorIs(t, err, ErrNotFound)

	// Wrong tokens never resolve.
	_, err = st.ResolveToken(ctx, "mn_forged", KindAlertmanager)
	assert.ErrorIs(t, err, ErrNotFound)

	// Resolution stamps LastUsedAt so stale tokens can be audited.
	toks, err := st.ListTokens(ctx)
	require.NoError(t, err)
	require.Len(t, toks, 1)
	assert.NotNil(t, toks[0].LastUsedAt)

	require.NoError(t, st.DeleteToken(ctx, "prom"))
	_, err = st.ResolveToken(ctx, plaintext, KindAlertmanager)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestChannelDeletionGuard(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_, err := st.CreateChannel(ctx, "infra", "!infra:example.org", false)
	require.NoError(t, err)
	_, _, err = st.CreateToken(ctx, "tok", KindAny, "infra")
	require.NoError(t, err)

	// Deleting a channel with live tokens would silently kill ingestion;
	// it must be refused until the tokens are gone.
	err = st.DeleteChannel(ctx, "infra")
	assert.ErrorIs(t, err, ErrChannelInUse)

	require.NoError(t, st.DeleteToken(ctx, "tok"))
	require.NoError(t, st.DeleteChannel(ctx, "infra"))
}

func TestUniqueNames(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_, err := st.CreateChannel(ctx, "dup", "!a:x", false)
	require.NoError(t, err)
	_, err = st.CreateChannel(ctx, "dup", "!b:x", false)
	assert.ErrorIs(t, err, ErrAlreadyExists)

	_, _, err = st.CreateToken(ctx, "t", KindAny, "dup")
	require.NoError(t, err)
	_, _, err = st.CreateToken(ctx, "t", KindAny, "dup")
	assert.ErrorIs(t, err, ErrAlreadyExists)
}

func TestParseKind(t *testing.T) {
	// Empty defaults to any; garbage is rejected rather than silently
	// becoming an all-access token.
	k, err := ParseKind("")
	require.NoError(t, err)
	assert.Equal(t, KindAny, k)
	_, err = ParseKind("root")
	assert.Error(t, err)
}
