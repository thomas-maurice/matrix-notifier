package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"github.com/alexedwards/argon2id"
)

var errUnauthenticated = errors.New("invalid or missing admin token")

// AdminAuth verifies bearer tokens against the configured argon2id hash.
// Argon2 verification is deliberately slow, so the first successful token is
// cached and subsequent requests use a constant-time comparison.
type AdminAuth struct {
	hash string

	mu       sync.RWMutex
	verified string
}

func NewAdminAuth(argon2Hash string) *AdminAuth {
	return &AdminAuth{hash: argon2Hash}
}

func (a *AdminAuth) check(token string) bool {
	if token == "" {
		return false
	}
	a.mu.RLock()
	verified := a.verified
	a.mu.RUnlock()
	if verified != "" {
		return subtle.ConstantTimeCompare([]byte(token), []byte(verified)) == 1
	}
	ok, err := argon2id.ComparePasswordAndHash(token, a.hash)
	if err != nil || !ok {
		return false
	}
	a.mu.Lock()
	a.verified = token
	a.mu.Unlock()
	return true
}

// Interceptor authenticates every RPC via the Authorization: Bearer header.
func (a *AdminAuth) Interceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			token := strings.TrimPrefix(req.Header().Get("Authorization"), "Bearer ")
			if !a.check(token) {
				return nil, connect.NewError(connect.CodeUnauthenticated, errUnauthenticated)
			}
			return next(ctx, req)
		}
	}
}
