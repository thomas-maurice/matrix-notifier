package api

import (
	"testing"

	"github.com/alexedwards/argon2id"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The admin token is the only thing between the internet and token minting;
// its verification must be exact and resilient to junk input.
func TestAdminAuthCheck(t *testing.T) {
	hash, err := argon2id.CreateHash("correct-horse", argon2id.DefaultParams)
	require.NoError(t, err)
	auth := NewAdminAuth(hash)

	assert.False(t, auth.check(""), "empty token must never pass")
	assert.False(t, auth.check("wrong"), "wrong token must never pass")
	assert.True(t, auth.check("correct-horse"))

	// Second call exercises the constant-time cache path; behaviour must be
	// identical for both accept and reject.
	assert.True(t, auth.check("correct-horse"))
	assert.False(t, auth.check("wrong"))
	assert.False(t, auth.check("correct-horse "), "whitespace variant must not pass")
}

func TestAdminAuthMalformedHash(t *testing.T) {
	// A corrupted hash in the config must fail closed, not open.
	auth := NewAdminAuth("not-a-phc-string")
	assert.False(t, auth.check("anything"))
}
