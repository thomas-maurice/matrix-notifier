package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/alexedwards/argon2id"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/tocsin/internal/config"
	"github.com/thomas-maurice/tocsin/internal/store"
)

func newTestAuth(t *testing.T, password string) (*AdminAuth, *store.Store) {
	t.Helper()
	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	require.NoError(t, err)
	auth, err := NewAdminAuth(context.Background(), st, hash)
	require.NoError(t, err)
	return auth, st
}

// The password guards token minting; only a correct password may yield a
// session, and the session JWT — not the password — is what authenticates
// subsequent requests.
func TestLoginAndVerify(t *testing.T) {
	auth, _ := newTestAuth(t, "correct-horse")

	_, _, err := auth.Login("wrong")
	require.Error(t, err, "wrong password must never yield a session")
	_, _, err = auth.Login("")
	require.Error(t, err, "empty password must never yield a session")

	token, expiresAt, err := auth.Login("correct-horse")
	require.NoError(t, err)
	assert.True(t, auth.verify(token))
	assert.WithinDuration(t, time.Now().Add(sessionTTL), expiresAt, time.Minute)

	// JWT-only: the raw password is not a bearer credential.
	assert.False(t, auth.verify("correct-horse"))
	assert.False(t, auth.verify(""))
	assert.False(t, auth.verify("garbage.jwt.token"))
}

// An expired session must be rejected even though its signature is valid —
// otherwise the 7-day validity is decorative.
func TestExpiredTokenRejected(t *testing.T) {
	auth, _ := newTestAuth(t, "correct-horse")
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "admin",
		IssuedAt:  jwt.NewNumericDate(time.Now().Add(-8 * 24 * time.Hour)),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(-24 * time.Hour)),
	}).SignedString(auth.credential().JWTSecret)
	require.NoError(t, err)
	assert.False(t, auth.verify(expired))
}

// Changing the password must revoke every outstanding session (the point of
// rotating the signing secret) while handing the caller a fresh one.
func TestChangePasswordRotatesSessions(t *testing.T) {
	auth, _ := newTestAuth(t, "old-password")
	ctx := context.Background()

	oldToken, _, err := auth.Login("old-password")
	require.NoError(t, err)

	_, _, err = auth.ChangePassword(ctx, "wrong", "brand-new-password")
	require.Error(t, err, "changing the password requires the current one")
	_, _, err = auth.ChangePassword(ctx, "old-password", "short")
	require.Error(t, err, "trivially short passwords must be refused")

	newToken, _, err := auth.ChangePassword(ctx, "old-password", "brand-new-password")
	require.NoError(t, err)

	assert.False(t, auth.verify(oldToken), "pre-rotation sessions must be dead")
	assert.True(t, auth.verify(newToken), "the rotating session must survive")

	_, _, err = auth.Login("old-password")
	require.Error(t, err, "the old password must be gone")
	_, _, err = auth.Login("brand-new-password")
	require.NoError(t, err)
}

// The config hash is a SEED: once the credential lives in the DB, a restart
// with a different config hash must not silently reset the password.
func TestConfigHashIsSeedOnly(t *testing.T) {
	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	hash1, err := argon2id.CreateHash("first-password", argon2id.DefaultParams)
	require.NoError(t, err)
	_, err = NewAdminAuth(context.Background(), st, hash1)
	require.NoError(t, err)

	hash2, err := argon2id.CreateHash("second-password", argon2id.DefaultParams)
	require.NoError(t, err)
	auth2, err := NewAdminAuth(context.Background(), st, hash2)
	require.NoError(t, err)

	_, _, err = auth2.Login("first-password")
	assert.NoError(t, err, "DB credential must win over a changed config hash")
	_, _, err = auth2.Login("second-password")
	assert.Error(t, err)
}

// Argon2 verification is expensive by design; unbounded login attempts would
// be a CPU DoS (and a brute-force aid).
func TestLoginRateLimited(t *testing.T) {
	auth, _ := newTestAuth(t, "correct-horse")
	var limited bool
	for range 10 {
		if _, _, err := auth.Login("wrong"); connect.CodeOf(err) == connect.CodeResourceExhausted {
			limited = true
			break
		}
	}
	assert.True(t, limited, "burst of bad logins must hit the rate limit")
}

// Browsers authenticate via the httpOnly cookie, API clients via the
// Authorization header; both must be understood, header first.
func TestSessionTokenExtraction(t *testing.T) {
	h := http.Header{}
	assert.Empty(t, sessionToken(h))

	h.Set("Cookie", "other=1; "+sessionCookie+"=from-cookie; another=2")
	assert.Equal(t, "from-cookie", sessionToken(h))

	h.Set("Authorization", "Bearer from-header")
	assert.Equal(t, "from-header", sessionToken(h))
}

// A corrupted hash in the database must fail closed, not open.
func TestMalformedHashFailsClosed(t *testing.T) {
	auth, _ := newTestAuth(t, "irrelevant")
	auth.cred.PasswordHash = "not-a-phc-string"
	_, _, err := auth.Login("anything")
	require.Error(t, err)
}
