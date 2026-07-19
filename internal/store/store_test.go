package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/tocsin/internal/config"
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

	plaintext, tok, err := st.CreateToken(ctx, "prom", KindAlertmanager, "infra", "", nil)
	require.NoError(t, err)
	// The plaintext must be usable and must never be stored as-is.
	assert.True(t, strings.HasPrefix(plaintext, "tcsn_"))
	assert.NotEqual(t, plaintext, tok.TokenHash)

	// Resolution routes to the channel's room, restricted by kind.
	tok2, err := st.ResolveToken(ctx, plaintext, KindAlertmanager)
	require.NoError(t, err)
	assert.Equal(t, "!infra:example.org", tok2.Channel.RoomID)

	// A token restricted to alertmanager must not authenticate gotify.
	_, err = st.ResolveToken(ctx, plaintext, KindGotify)
	assert.ErrorIs(t, err, ErrNotFound)

	// Wrong tokens never resolve.
	_, err = st.ResolveToken(ctx, "tcsn_forged", KindAlertmanager)
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

// Expiry is forced rotation: past its instant a token must be exactly as
// dead as a forged one, while an unexpired one keeps working.
func TestTokenExpiry(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	_, err := st.CreateChannel(ctx, "infra", "!r:x", false)
	require.NoError(t, err)

	past := time.Now().Add(-time.Minute)
	expired, _, err := st.CreateToken(ctx, "expired", KindAny, "infra", "", &past)
	require.NoError(t, err)
	_, err = st.ResolveToken(ctx, expired, KindGotify)
	assert.ErrorIs(t, err, ErrNotFound)

	future := time.Now().Add(time.Hour)
	valid, tok, err := st.CreateToken(ctx, "valid", KindAny, "infra", "", &future)
	require.NoError(t, err)
	require.NotNil(t, tok.ExpiresAt)
	_, err = st.ResolveToken(ctx, valid, KindGotify)
	assert.NoError(t, err)

	// Expiry is editable in place: replacing it moves the deadline...
	extended := time.Now().Add(48 * time.Hour)
	updated, err := st.UpdateToken(ctx, "valid", "", "", &extended, false)
	require.NoError(t, err)
	require.NotNil(t, updated.ExpiresAt)
	assert.WithinDuration(t, extended, *updated.ExpiresAt, time.Second)

	// ...and clearing it revives even an already-expired token.
	updated, err = st.UpdateToken(ctx, "expired", "", "", nil, true)
	require.NoError(t, err)
	assert.Nil(t, updated.ExpiresAt)
	_, err = st.ResolveToken(ctx, expired, KindGotify)
	assert.NoError(t, err, "clearing the expiry must make the token authenticate again")

	// nil + no clear leaves the expiry untouched.
	updated, err = st.UpdateToken(ctx, "valid", "", "", nil, false)
	require.NoError(t, err)
	require.NotNil(t, updated.ExpiresAt)
	assert.WithinDuration(t, extended, *updated.ExpiresAt, time.Second)
}

func TestChannelDeletionGuard(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_, err := st.CreateChannel(ctx, "infra", "!infra:example.org", false)
	require.NoError(t, err)
	_, _, err = st.CreateToken(ctx, "tok", KindAny, "infra", "", nil)
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

	_, _, err = st.CreateToken(ctx, "t", KindAny, "dup", "", nil)
	require.NoError(t, err)
	_, _, err = st.CreateToken(ctx, "t", KindAny, "dup", "", nil)
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
