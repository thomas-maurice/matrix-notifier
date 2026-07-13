package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/alexedwards/argon2id"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	notifierv1 "github.com/thomas-maurice/matrix-notifier/gen/notifier/v1"
	"github.com/thomas-maurice/matrix-notifier/gen/notifier/v1/notifierv1connect"
	"github.com/thomas-maurice/matrix-notifier/internal/config"
	"github.com/thomas-maurice/matrix-notifier/internal/matrix"
	"github.com/thomas-maurice/matrix-notifier/internal/notify"
	"github.com/thomas-maurice/matrix-notifier/internal/store"
)

type fakeBot struct {
	sent []string
	left []string
}

func (f *fakeBot) Send(_ context.Context, roomID string, _ notify.Notification) error {
	f.sent = append(f.sent, roomID)
	return nil
}

func (f *fakeBot) Status(context.Context) matrix.Status {
	return matrix.Status{UserID: "@bot:x", DeviceID: "DEV", Verified: true, LastSync: time.Now(), Uptime: time.Minute}
}

func (f *fakeBot) RoomStatus(context.Context, string) (bool, bool) { return true, true }

func (f *fakeBot) ResolveRoom(_ context.Context, room string) (string, error) {
	if room == "#alias:x" {
		return "!resolved:x", nil
	}
	return room, nil
}

func (f *fakeBot) JoinedRooms(context.Context) ([]matrix.RoomInfo, error) {
	return []matrix.RoomInfo{{ID: "!r:x", Name: "Mapped"}, {ID: "!free:x", Name: "Unmapped"}}, nil
}

func (f *fakeBot) LeaveRoom(_ context.Context, roomID string) error {
	f.left = append(f.left, roomID)
	return nil
}

func newTestAPI(t *testing.T) (notifierv1connect.AdminServiceClient, *fakeBot) {
	t.Helper()
	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	bot := &fakeBot{}

	hash, err := argon2id.CreateHash("test-admin-token", argon2id.DefaultParams)
	require.NoError(t, err)
	auth := NewAdminAuth(hash)

	path, handler := notifierv1connect.NewAdminServiceHandler(
		NewServer(st, bot, "sqlite"),
		connect.WithInterceptors(auth.Interceptor()),
	)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := notifierv1connect.NewAdminServiceClient(srv.Client(), srv.URL)
	return client, bot
}

func authed(token string) connect.ClientOption {
	return connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Authorization", "Bearer "+token)
			return next(ctx, req)
		}
	}))
}

func newAuthedClient(t *testing.T, token string) (notifierv1connect.AdminServiceClient, *fakeBot) {
	t.Helper()
	st, err := store.Open(config.Database{Type: "sqlite", URI: ":memory:"})
	require.NoError(t, err)
	bot := &fakeBot{}
	hash, err := argon2id.CreateHash("test-admin-token", argon2id.DefaultParams)
	require.NoError(t, err)
	path, handler := notifierv1connect.NewAdminServiceHandler(
		NewServer(st, bot, "sqlite"),
		connect.WithInterceptors(NewAdminAuth(hash).Interceptor()),
	)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return notifierv1connect.NewAdminServiceClient(srv.Client(), srv.URL, authed(token)), bot
}

// The admin API can mint ingest tokens; without authentication it must be a
// brick wall.
func TestRejectsBadAdminToken(t *testing.T) {
	client, _ := newTestAPI(t)
	_, err := client.GetStatus(context.Background(), connect.NewRequest(&notifierv1.GetStatusRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	badClient, _ := newAuthedClient(t, "wrong-token")
	_, err = badClient.GetStatus(context.Background(), connect.NewRequest(&notifierv1.GetStatusRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestChannelAndTokenLifecycle(t *testing.T) {
	client, bot := newAuthedClient(t, "test-admin-token")
	ctx := context.Background()

	_, err := client.CreateChannel(ctx, connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "infra", RoomId: "!r:x"}))
	require.NoError(t, err)

	tok, err := client.CreateToken(ctx, connect.NewRequest(&notifierv1.CreateTokenRequest{Name: "prom", Kind: "alertmanager", Channel: "infra"}))
	require.NoError(t, err)
	// The plaintext is the only chance the operator gets to copy the token.
	assert.NotEmpty(t, tok.Msg.Plaintext)

	// Deleting a channel that still routes tokens must be refused.
	_, err = client.DeleteChannel(ctx, connect.NewRequest(&notifierv1.DeleteChannelRequest{Name: "infra"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

	// Test notifications go to the channel's room.
	_, err = client.SendTestNotification(ctx, connect.NewRequest(&notifierv1.SendTestNotificationRequest{Channel: "infra"}))
	require.NoError(t, err)
	require.Len(t, bot.sent, 1)
	assert.Equal(t, "!r:x", bot.sent[0])

	_, err = client.DeleteToken(ctx, connect.NewRequest(&notifierv1.DeleteTokenRequest{Name: "prom"}))
	require.NoError(t, err)
	_, err = client.DeleteChannel(ctx, connect.NewRequest(&notifierv1.DeleteChannelRequest{Name: "infra"}))
	require.NoError(t, err)
}

// Joined rooms are exposed with their channel binding so the UI can suggest
// unmapped rooms — a room the bot joined must show up even before any
// channel points at it.
func TestListRoomsMarksBindings(t *testing.T) {
	client, _ := newAuthedClient(t, "test-admin-token")
	ctx := context.Background()
	_, err := client.CreateChannel(ctx, connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "mapped", RoomId: "!r:x"}))
	require.NoError(t, err)

	resp, err := client.ListRooms(ctx, connect.NewRequest(&notifierv1.ListRoomsRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Rooms, 2)
	byID := map[string]string{}
	for _, r := range resp.Msg.Rooms {
		byID[r.RoomId] = r.Channel
	}
	assert.Equal(t, "mapped", byID["!r:x"])
	assert.Empty(t, byID["!free:x"])
}

// Leaving a room must take its channels and tokens with it: after the bot
// is gone they could never deliver, and stale tokens would keep
// authenticating producers into a black hole.
func TestLeaveRoomCascades(t *testing.T) {
	client, bot := newAuthedClient(t, "test-admin-token")
	ctx := context.Background()
	_, err := client.CreateChannel(ctx, connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "doomed", RoomId: "!r:x"}))
	require.NoError(t, err)
	_, err = client.CreateToken(ctx, connect.NewRequest(&notifierv1.CreateTokenRequest{Name: "doomed-tok", Channel: "doomed"}))
	require.NoError(t, err)

	_, err = client.LeaveRoom(ctx, connect.NewRequest(&notifierv1.LeaveRoomRequest{RoomId: "!r:x"}))
	require.NoError(t, err)
	require.Equal(t, []string{"!r:x"}, bot.left)

	chs, err := client.ListChannels(ctx, connect.NewRequest(&notifierv1.ListChannelsRequest{}))
	require.NoError(t, err)
	assert.Empty(t, chs.Msg.Channels)
	toks, err := client.ListTokens(ctx, connect.NewRequest(&notifierv1.ListTokensRequest{}))
	require.NoError(t, err)
	assert.Empty(t, toks.Msg.Tokens)
}

// Users paste aliases; every internal lookup is room-ID-keyed, so creation
// must resolve them or sends fail later with a confusing error.
func TestCreateChannelResolvesAlias(t *testing.T) {
	client, _ := newAuthedClient(t, "test-admin-token")
	resp, err := client.CreateChannel(context.Background(), connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "aliased", RoomId: "#alias:x"}))
	require.NoError(t, err)
	assert.Equal(t, "!resolved:x", resp.Msg.Channel.RoomId)
}

func TestStatus(t *testing.T) {
	client, _ := newAuthedClient(t, "test-admin-token")
	resp, err := client.GetStatus(context.Background(), connect.NewRequest(&notifierv1.GetStatusRequest{}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Verified)
	assert.Equal(t, "@bot:x", resp.Msg.UserId)
	assert.Equal(t, "sqlite", resp.Msg.DatabaseType)
}
