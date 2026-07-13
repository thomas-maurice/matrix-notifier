package api

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	notifierv1 "github.com/thomas-maurice/matrix-notifier/gen/notifier/v1"
)

func TestCreateChannelValidation(t *testing.T) {
	client, _ := newAuthedClient(t, "test-admin-token")
	ctx := context.Background()

	_, err := client.CreateChannel(ctx, connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "", RoomId: "!r:x"}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = client.CreateChannel(ctx, connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "x", RoomId: ""}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// Duplicates must be a clean conflict, not a 500.
	_, err = client.CreateChannel(ctx, connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "dup", RoomId: "!r:x"}))
	require.NoError(t, err)
	_, err = client.CreateChannel(ctx, connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "dup", RoomId: "!r:x"}))
	assert.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))
}

func TestCreateTokenValidation(t *testing.T) {
	client, _ := newAuthedClient(t, "test-admin-token")
	ctx := context.Background()

	// Garbage kind must be rejected, not silently become an all-access token.
	_, err := client.CreateChannel(ctx, connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "c", RoomId: "!r:x"}))
	require.NoError(t, err)
	_, err = client.CreateToken(ctx, connect.NewRequest(&notifierv1.CreateTokenRequest{Name: "t", Kind: "root", Channel: "c"}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// Unknown channel is NotFound.
	_, err = client.CreateToken(ctx, connect.NewRequest(&notifierv1.CreateTokenRequest{Name: "t", Channel: "ghost"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestDeleteMissingResources(t *testing.T) {
	client, _ := newAuthedClient(t, "test-admin-token")
	ctx := context.Background()
	_, err := client.DeleteChannel(ctx, connect.NewRequest(&notifierv1.DeleteChannelRequest{Name: "ghost"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = client.DeleteToken(ctx, connect.NewRequest(&notifierv1.DeleteTokenRequest{Name: "ghost"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestSendTestNotificationUnknownChannel(t *testing.T) {
	client, bot := newAuthedClient(t, "test-admin-token")
	_, err := client.SendTestNotification(context.Background(), connect.NewRequest(&notifierv1.SendTestNotificationRequest{Channel: "ghost"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	assert.Empty(t, bot.sent)
}

func TestLeaveRoomValidation(t *testing.T) {
	client, bot := newAuthedClient(t, "test-admin-token")
	_, err := client.LeaveRoom(context.Background(), connect.NewRequest(&notifierv1.LeaveRoomRequest{RoomId: ""}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	assert.Empty(t, bot.left)
}

func TestChartFlagRoundTrip(t *testing.T) {
	client, _ := newAuthedClient(t, "test-admin-token")
	ctx := context.Background()
	created, err := client.CreateChannel(ctx, connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "c", RoomId: "!r:x", Chart: true}))
	require.NoError(t, err)
	assert.True(t, created.Msg.Channel.Chart)

	listed, err := client.ListChannels(ctx, connect.NewRequest(&notifierv1.ListChannelsRequest{}))
	require.NoError(t, err)
	require.Len(t, listed.Msg.Channels, 1)
	assert.True(t, listed.Msg.Channels[0].Chart)
}

// Toggling charts on a live channel must not require destroying it (and its
// tokens) — that's the whole point of UpdateChannel.
func TestUpdateChannelTogglesChart(t *testing.T) {
	client, _ := newAuthedClient(t, "test-admin-token")
	ctx := context.Background()
	_, err := client.CreateChannel(ctx, connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "c", RoomId: "!r:x"}))
	require.NoError(t, err)

	resp, err := client.UpdateChannel(ctx, connect.NewRequest(&notifierv1.UpdateChannelRequest{Name: "c", Chart: true}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Channel.Chart)

	resp, err = client.UpdateChannel(ctx, connect.NewRequest(&notifierv1.UpdateChannelRequest{Name: "c", Chart: false}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Channel.Chart)

	_, err = client.UpdateChannel(ctx, connect.NewRequest(&notifierv1.UpdateChannelRequest{Name: "ghost", Chart: true}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestTokenPrefixLifecycle(t *testing.T) {
	client, _ := newAuthedClient(t, "test-admin-token")
	ctx := context.Background()
	_, err := client.CreateChannel(ctx, connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "c", RoomId: "!r:x"}))
	require.NoError(t, err)
	created, err := client.CreateToken(ctx, connect.NewRequest(&notifierv1.CreateTokenRequest{Name: "sonarr", Channel: "c", Prefix: "📺"}))
	require.NoError(t, err)
	assert.Equal(t, "📺", created.Msg.Token.Prefix)

	updated, err := client.UpdateToken(ctx, connect.NewRequest(&notifierv1.UpdateTokenRequest{Name: "sonarr", Prefix: "🎬"}))
	require.NoError(t, err)
	assert.Equal(t, "🎬", updated.Msg.Token.Prefix)

	_, err = client.UpdateToken(ctx, connect.NewRequest(&notifierv1.UpdateTokenRequest{Name: "ghost", Prefix: "x"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}
