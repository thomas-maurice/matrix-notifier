package api

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
