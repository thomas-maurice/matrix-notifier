package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	notifierv1 "github.com/thomas-maurice/matrix-notifier/gen/notifier/v1"
)

// Test sends stay synchronous for immediate operator feedback, but must
// still land in the delivery history — both outcomes.
func TestDeliveryHistoryRecordsTestSends(t *testing.T) {
	client, bot := newAuthedClient(t, "test-admin-token")
	ctx := context.Background()

	_, err := client.CreateChannel(ctx, connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "infra", RoomId: "!r:x"}))
	require.NoError(t, err)

	_, err = client.SendTestNotification(ctx, connect.NewRequest(&notifierv1.SendTestNotificationRequest{Channel: "infra"}))
	require.NoError(t, err)

	bot.sendErr = errors.New("room key withheld")
	_, err = client.SendTestNotification(ctx, connect.NewRequest(&notifierv1.SendTestNotificationRequest{Channel: "infra"}))
	require.Error(t, err)

	resp, err := client.ListDeliveries(ctx, connect.NewRequest(&notifierv1.ListDeliveriesRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Deliveries, 2)
	// Newest first: the failure is on top.
	assert.Equal(t, "failed", resp.Msg.Deliveries[0].Status)
	assert.Contains(t, resp.Msg.Deliveries[0].LastError, "room key withheld")
	assert.Equal(t, "delivered", resp.Msg.Deliveries[1].Status)
	assert.Equal(t, "test", resp.Msg.Deliveries[1].Kind)
	assert.NotNil(t, resp.Msg.Deliveries[1].DeliveredAt)

	// Channel filter must not leak other channels' history.
	resp, err = client.ListDeliveries(ctx, connect.NewRequest(&notifierv1.ListDeliveriesRequest{Channel: "nope"}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Deliveries)
}

// Expiry set at creation must round-trip to the listing (so the UI can show
// it) and a past expiry must be refused outright.
func TestCreateTokenWithExpiry(t *testing.T) {
	client, _ := newAuthedClient(t, "test-admin-token")
	ctx := context.Background()

	_, err := client.CreateChannel(ctx, connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "infra", RoomId: "!r:x"}))
	require.NoError(t, err)

	expiry := time.Now().Add(24 * time.Hour)
	_, err = client.CreateToken(ctx, connect.NewRequest(&notifierv1.CreateTokenRequest{
		Name: "rotating", Channel: "infra", ExpiresAt: timestamppb.New(expiry),
	}))
	require.NoError(t, err)
	list, err := client.ListTokens(ctx, connect.NewRequest(&notifierv1.ListTokensRequest{}))
	require.NoError(t, err)
	require.Len(t, list.Msg.Tokens, 1)
	require.NotNil(t, list.Msg.Tokens[0].ExpiresAt)
	assert.WithinDuration(t, expiry, list.Msg.Tokens[0].ExpiresAt.AsTime(), time.Second)

	_, err = client.CreateToken(ctx, connect.NewRequest(&notifierv1.CreateTokenRequest{
		Name: "stillborn", Channel: "infra", ExpiresAt: timestamppb.New(time.Now().Add(-time.Hour)),
	}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a token born expired is a mistake, refuse it")

	// Expiry is editable after the fact: clearing it makes the token
	// permanent again, a past replacement is refused like at creation.
	upd, err := client.UpdateToken(ctx, connect.NewRequest(&notifierv1.UpdateTokenRequest{
		Name: "rotating", ClearExpiry: true,
	}))
	require.NoError(t, err)
	assert.Nil(t, upd.Msg.Token.ExpiresAt)
	_, err = client.UpdateToken(ctx, connect.NewRequest(&notifierv1.UpdateTokenRequest{
		Name: "rotating", ExpiresAt: timestamppb.New(time.Now().Add(-time.Hour)),
	}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// Retry revives only failed deliveries; anything else is NotFound so the UI
// cannot accidentally duplicate an already-delivered notification.
func TestRetryDelivery(t *testing.T) {
	client, bot := newAuthedClient(t, "test-admin-token")
	ctx := context.Background()

	_, err := client.CreateChannel(ctx, connect.NewRequest(&notifierv1.CreateChannelRequest{Name: "infra", RoomId: "!r:x"}))
	require.NoError(t, err)
	bot.sendErr = errors.New("boom")
	_, err = client.SendTestNotification(ctx, connect.NewRequest(&notifierv1.SendTestNotificationRequest{Channel: "infra"}))
	require.Error(t, err)

	resp, err := client.ListDeliveries(ctx, connect.NewRequest(&notifierv1.ListDeliveriesRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Deliveries, 1)
	id := resp.Msg.Deliveries[0].Id

	_, err = client.RetryDelivery(ctx, connect.NewRequest(&notifierv1.RetryDeliveryRequest{Id: id}))
	require.NoError(t, err)
	resp, err = client.ListDeliveries(ctx, connect.NewRequest(&notifierv1.ListDeliveriesRequest{}))
	require.NoError(t, err)
	assert.Equal(t, "pending", resp.Msg.Deliveries[0].Status, "retried delivery must be queued again")

	// Pending (not failed) now: a second retry must refuse.
	_, err = client.RetryDelivery(ctx, connect.NewRequest(&notifierv1.RetryDeliveryRequest{Id: id}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	_, err = client.RetryDelivery(ctx, connect.NewRequest(&notifierv1.RetryDeliveryRequest{}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
