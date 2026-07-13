//go:build integration

// Package integration spins up a real Synapse in a container and exercises
// the E2EE-sensitive surface that no unit test can cover: login,
// cross-signing bootstrap, encrypted send, and decryption by a second
// client. It is the regression guard for mautrix-go version bumps.
//
// Run with: go test -tags "goolm integration" ./test/integration/
package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/thomas-maurice/matrix-notifier/internal/config"
	"github.com/thomas-maurice/matrix-notifier/internal/matrix"
	"github.com/thomas-maurice/matrix-notifier/internal/notify"
)

// entrypoint generates a Synapse config on first boot, binds it to all
// interfaces (so the mapped port is reachable), and enables open
// registration so the test can create users. The generated config is edited
// with Python because it is YAML the shell must not mangle.
const entrypoint = `#!/bin/sh
set -e
CFG=/data/homeserver.yaml
if [ ! -f "$CFG" ]; then
  python -m synapse.app.homeserver --server-name localhost --config-path "$CFG" --generate-config --report-stats=no
  python - "$CFG" <<'PY'
import sys, yaml
p = sys.argv[1]
c = yaml.safe_load(open(p))
for l in c.get('listeners', []):
    l['bind_addresses'] = ['0.0.0.0']
c['registration_shared_secret'] = 'integration-secret'
c['enable_registration'] = True
c['enable_registration_without_verification'] = True
c['suppress_key_server_warning'] = True
yaml.safe_dump(c, open(p, 'w'))
PY
fi
exec python -m synapse.app.homeserver --config-path "$CFG"
`

func startSynapse(ctx context.Context, t *testing.T) string {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "matrixdotorg/synapse:latest",
		ExposedPorts: []string{"8008/tcp"},
		Env: map[string]string{
			"SYNAPSE_SERVER_NAME":  "localhost",
			"SYNAPSE_REPORT_STATS": "no",
		},
		Files: []testcontainers.ContainerFile{{
			Reader:            strings.NewReader(entrypoint),
			ContainerFilePath: "/entrypoint.sh",
			FileMode:          0o755,
		}},
		Entrypoint: []string{"/bin/sh", "/entrypoint.sh"},
		WaitingFor: wait.ForHTTP("/health").WithPort("8008/tcp").WithStartupTimeout(120 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "8008/tcp")
	require.NoError(t, err)

	// Register the bot and a verifier account.
	for _, u := range []string{"notifier", "admin"} {
		_, _, err := c.Exec(ctx, []string{
			"register_new_matrix_user", "-u", u, "-p", "password",
			"--no-admin", "-c", "/data/homeserver.yaml", "http://localhost:8008",
		})
		require.NoError(t, err, "registering %s", u)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port())
}

func TestEndToEndEncryptedDelivery(t *testing.T) {
	ctx := context.Background()
	homeserver := startSynapse(ctx, t)

	// The verifier client (as "admin") creates an encrypted room, invites the
	// bot, and will decrypt what the bot sends.
	verifier := newClient(t, ctx, homeserver, "admin")
	roomID := createEncryptedRoom(t, ctx, verifier, "@notifier:localhost")

	// Start the bot: login + cross-signing bootstrap + encrypted-room refusal
	// logic all run here against a real homeserver.
	cfg := &config.Config{
		DataDir:  t.TempDir(),
		LogLevel: "warn",
		Matrix: config.Matrix{
			Homeserver: homeserver,
			UserID:     "@notifier:localhost",
			Password:   "password",
		},
		Database: config.Database{Type: "sqlite", URI: filepath.Join(t.TempDir(), "bot.db")},
	}
	bot, err := matrix.New(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = bot.Close() })
	require.NoError(t, bot.Start(ctx))

	// The bot must report itself verified (cross-signing bootstrapped).
	require.Eventually(t, func() bool { return bot.Status(ctx).Verified }, 20*time.Second, time.Second,
		"bot device should be cross-signed and verified")

	// Wait for the bot to auto-join the invite, then send.
	require.Eventually(t, func() bool {
		joined, encrypted := bot.RoomStatus(ctx, roomID)
		return joined && encrypted
	}, 30*time.Second, time.Second, "bot should join the encrypted room")

	const marker = "integration-test-secret-payload"
	require.NoError(t, bot.Send(ctx, roomID, notify.Notification{Title: "E2E", Body: marker}))

	// The verifier must be able to decrypt the megolm message the bot sent.
	got := waitForDecryptedMessage(t, verifier, id.RoomID(roomID), 30*time.Second)
	require.Contains(t, got, marker, "verifier should decrypt the bot's encrypted message")
}

type client struct {
	mx     *mautrix.Client
	helper *cryptohelper.CryptoHelper
	msgs   chan string
}

func newClient(t *testing.T, ctx context.Context, homeserver, user string) *client {
	t.Helper()
	mx, err := mautrix.NewClient(homeserver, "", "")
	require.NoError(t, err)
	mx.Log = zerolog.Nop()

	helper, err := cryptohelper.NewCryptoHelper(mx, []byte("integration-pickle-key-"+user), filepath.Join(t.TempDir(), user+".db"))
	require.NoError(t, err)
	helper.LoginAs = &mautrix.ReqLogin{
		Type:       mautrix.AuthTypePassword,
		Identifier: mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: user},
		Password:   "password",
	}
	require.NoError(t, helper.Init(ctx))
	mx.Crypto = helper
	t.Cleanup(func() { _ = helper.Close() })

	c := &client{mx: mx, helper: helper, msgs: make(chan string, 16)}
	syncer := mx.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnEventType(event.EventMessage, func(_ context.Context, evt *event.Event) {
		if evt.Sender != mx.UserID {
			c.msgs <- evt.Content.AsMessage().Body
		}
	})
	go func() { _ = mx.SyncWithContext(ctx) }()
	time.Sleep(2 * time.Second) // let the first sync establish device lists
	return c
}

func createEncryptedRoom(t *testing.T, ctx context.Context, c *client, invite string) string {
	t.Helper()
	resp, err := c.mx.CreateRoom(ctx, &mautrix.ReqCreateRoom{
		Preset: "private_chat",
		Invite: []id.UserID{id.UserID(invite)},
		InitialState: []*event.Event{{
			Type: event.StateEncryption,
			Content: event.Content{Parsed: &event.EncryptionEventContent{
				Algorithm: id.AlgorithmMegolmV1,
			}},
		}},
	})
	require.NoError(t, err)
	return resp.RoomID.String()
}

func waitForDecryptedMessage(t *testing.T, c *client, room id.RoomID, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case body := <-c.msgs:
			if body != "" {
				return body
			}
		case <-deadline:
			t.Fatal("timed out waiting for a decrypted message")
			return ""
		}
	}
}
