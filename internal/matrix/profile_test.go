package matrix

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maunium.net/go/mautrix"

	"github.com/thomas-maurice/tocsin/internal/config"
)

// pngHeader makes DetectContentType say image/png.
var pngHeader = []byte("\x89PNG\r\n\x1a\n rest-of-image")

func profileTestBot(t *testing.T, mux *http.ServeMux) *Bot {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client, err := mautrix.NewClient(srv.URL, "@bot:example.org", "token")
	require.NoError(t, err)
	return &Bot{
		cfg:    &config.Config{DataDir: t.TempDir()},
		client: client,
	}
}

// SetProfile uploads the image before pointing the profile at it, and must
// refuse non-image bytes instead of publishing garbage media.
func TestSetProfile(t *testing.T) {
	var uploads, nameSets, avatarSets int
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /_matrix/client/v3/profile/{user}/displayname", func(w http.ResponseWriter, r *http.Request) {
		nameSets++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("POST /_matrix/media/v3/upload", func(w http.ResponseWriter, r *http.Request) {
		uploads++
		assert.Equal(t, "image/png", r.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content_uri":"mxc://example.org/abc"}`))
	})
	mux.HandleFunc("PUT /_matrix/client/v3/profile/{user}/avatar_url", func(w http.ResponseWriter, r *http.Request) {
		avatarSets++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	b := profileTestBot(t, mux)

	require.NoError(t, b.SetProfile(context.Background(), "Notifier", pngHeader))
	assert.Equal(t, 1, nameSets)
	assert.Equal(t, 1, uploads)
	assert.Equal(t, 1, avatarSets)

	// Empty parts are skipped, not sent as empty values.
	require.NoError(t, b.SetProfile(context.Background(), "", nil))
	assert.Equal(t, 1, nameSets)
	assert.Equal(t, 1, uploads)

	// Non-image bytes must be rejected before anything reaches the server.
	err := b.SetProfile(context.Background(), "", []byte("#!/bin/sh evil"))
	require.Error(t, err)
	assert.Equal(t, 1, uploads)
}

// Profile inlines the avatar bytes for the UI; an account that never set an
// avatar (404 from Synapse) must yield the name without an error.
func TestProfile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_matrix/client/v3/profile/{user}/displayname", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"displayname":"Notifier"}`))
	})
	mux.HandleFunc("GET /_matrix/client/v3/profile/{user}/avatar_url", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"avatar_url":"mxc://example.org/abc"}`))
	})
	mux.HandleFunc("GET /_matrix/client/v1/media/download/{server}/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngHeader)
	})
	b := profileTestBot(t, mux)

	p, err := b.Profile(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Notifier", p.DisplayName)
	assert.Equal(t, pngHeader, p.Avatar)
	assert.Equal(t, "image/png", p.AvatarMIME)

	// No avatar ever set: Synapse 404s the avatar_url lookup.
	mux404 := http.NewServeMux()
	mux404.HandleFunc("GET /_matrix/client/v3/profile/{user}/displayname", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"displayname":"Notifier"}`))
	})
	mux404.HandleFunc("GET /_matrix/client/v3/profile/{user}/avatar_url", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errcode":"M_NOT_FOUND","error":"no avatar"}`))
	})
	b = profileTestBot(t, mux404)
	p, err = b.Profile(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Notifier", p.DisplayName)
	assert.Empty(t, p.Avatar)
}
