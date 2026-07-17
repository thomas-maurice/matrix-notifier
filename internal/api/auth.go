package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/alexedwards/argon2id"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/time/rate"

	"github.com/thomas-maurice/matrix-notifier/internal/store"
)

var (
	errUnauthenticated = errors.New("invalid or missing session token")
	errBadPassword     = errors.New("invalid password")
)

const (
	// sessionTTL is how long a login lasts before the password is asked again.
	sessionTTL = 7 * 24 * time.Hour
	// sessionCookie carries the JWT for browser sessions; httpOnly so the UI
	// never touches (or stores) the token itself.
	sessionCookie = "mn_admin_session"
	// loginProcedure is the only RPC reachable without a session.
	loginProcedure = "/notifier.v1.AdminService/Login"
)

// AdminAuth manages the admin session lifecycle: password verification
// against the argon2id hash stored in the database, JWT minting and
// validation. The credential row is cached; password changes go through
// ChangePassword which refreshes the cache and rotates the signing secret.
type AdminAuth struct {
	store *store.Store

	// Argon2 is deliberately expensive; cap login attempts so the endpoint
	// cannot be used to burn CPU (or brute-force the password quickly).
	loginLimiter *rate.Limiter

	mu   sync.RWMutex
	cred *store.AdminCredential
}

// NewAdminAuth loads the admin credential, seeding it from the configured
// argon2id hash when the database has none yet (first boot / migration).
func NewAdminAuth(ctx context.Context, st *store.Store, seedHash string) (*AdminAuth, error) {
	cred, err := st.SeedAdminCredential(ctx, seedHash)
	if err != nil {
		return nil, fmt.Errorf("loading admin credential: %w", err)
	}
	return &AdminAuth{
		store:        st,
		loginLimiter: rate.NewLimiter(rate.Every(2*time.Second), 5),
		cred:         cred,
	}, nil
}

func (a *AdminAuth) credential() *store.AdminCredential {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cred
}

// Login verifies the password and mints a session JWT.
func (a *AdminAuth) Login(password string) (token string, expiresAt time.Time, err error) {
	if !a.loginLimiter.Allow() {
		return "", time.Time{}, connect.NewError(connect.CodeResourceExhausted, errors.New("too many login attempts, slow down"))
	}
	ok, err := argon2id.ComparePasswordAndHash(password, a.credential().PasswordHash)
	if err != nil || !ok {
		return "", time.Time{}, connect.NewError(connect.CodeUnauthenticated, errBadPassword)
	}
	return a.mint()
}

// ChangePassword verifies the current password, stores the new hash and
// rotates the JWT secret — killing every outstanding session — then mints a
// fresh token so the calling session survives its own rotation.
func (a *AdminAuth) ChangePassword(ctx context.Context, current, next string) (token string, expiresAt time.Time, err error) {
	ok, err := argon2id.ComparePasswordAndHash(current, a.credential().PasswordHash)
	if err != nil || !ok {
		return "", time.Time{}, connect.NewError(connect.CodeUnauthenticated, errBadPassword)
	}
	if len(next) < 8 {
		return "", time.Time{}, connect.NewError(connect.CodeInvalidArgument, errors.New("new password must be at least 8 characters"))
	}
	hash, err := argon2id.CreateHash(next, argon2id.DefaultParams)
	if err != nil {
		return "", time.Time{}, connect.NewError(connect.CodeInternal, err)
	}
	cred, err := a.store.UpdateAdminPassword(ctx, hash)
	if err != nil {
		return "", time.Time{}, connect.NewError(connect.CodeInternal, err)
	}
	a.mu.Lock()
	a.cred = cred
	a.mu.Unlock()
	return a.mint()
}

func (a *AdminAuth) mint() (string, time.Time, error) {
	expiresAt := time.Now().Add(sessionTTL)
	claims := jwt.RegisteredClaims{
		Subject:   "admin",
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.credential().JWTSecret)
	if err != nil {
		return "", time.Time{}, connect.NewError(connect.CodeInternal, err)
	}
	return token, expiresAt, nil
}

func (a *AdminAuth) verify(token string) bool {
	if token == "" {
		return false
	}
	_, err := jwt.ParseWithClaims(token, &jwt.RegisteredClaims{},
		func(*jwt.Token) (any, error) { return a.credential().JWTSecret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	return err == nil
}

// sessionToken extracts the JWT from the Authorization header (API clients)
// or the session cookie (browsers).
func sessionToken(header http.Header) string {
	if h := header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	// http.Request parses cookies for us, but Connect only hands us headers.
	for _, c := range strings.Split(header.Get("Cookie"), ";") {
		if name, value, found := strings.Cut(strings.TrimSpace(c), "="); found && name == sessionCookie {
			return value
		}
	}
	return ""
}

// SessionCookie renders the Set-Cookie value that stores (or, with an empty
// token, clears) the session JWT. httpOnly + SameSite=Strict: the token is
// invisible to scripts and never leaves the origin. Secure is set when the
// request came over TLS (directly or via a reverse proxy).
func SessionCookie(token string, expiresAt time.Time, requestHeader http.Header) string {
	c := &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   requestHeader.Get("X-Forwarded-Proto") == "https",
	}
	if token == "" {
		c.MaxAge = -1
	} else {
		c.Expires = expiresAt
	}
	return c.String()
}

// Interceptor authenticates every RPC except Login via the session JWT,
// taken from the Authorization header or the session cookie.
func (a *AdminAuth) Interceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if req.Spec().Procedure == loginProcedure {
				return next(ctx, req)
			}
			if !a.verify(sessionToken(req.Header())) {
				return nil, connect.NewError(connect.CodeUnauthenticated, errUnauthenticated)
			}
			return next(ctx, req)
		}
	}
}
