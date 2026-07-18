// Package api implements the Connect RPC admin service: channel and token
// management plus bot status.
package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	notifierv1 "github.com/thomas-maurice/matrix-notifier/gen/notifier/v1"
	"github.com/thomas-maurice/matrix-notifier/internal/logging"
	"github.com/thomas-maurice/matrix-notifier/internal/matrix"
	"github.com/thomas-maurice/matrix-notifier/internal/notify"
	"github.com/thomas-maurice/matrix-notifier/internal/store"
)

// Version is stamped via -ldflags at release time.
var Version = "dev"

// Matrix is the part of the bot the API needs; narrowed for testability.
type Matrix interface {
	notify.Sender
	Status(ctx context.Context) matrix.Status
	RoomStatus(ctx context.Context, roomID string) (joined, encrypted bool)
	RoomAlias(ctx context.Context, roomID string) string
	ResolveRoom(ctx context.Context, room string) (string, error)
	JoinedRooms(ctx context.Context) ([]matrix.RoomInfo, error)
	LeaveRoom(ctx context.Context, roomID string) error
	Profile(ctx context.Context) (matrix.Profile, error)
	SetProfile(ctx context.Context, displayName string, avatar []byte) error
}

type Server struct {
	store  *store.Store
	bot    Matrix
	auth   *AdminAuth
	dbType string
	// kick wakes the outbox dispatcher after RetryDelivery re-queues an
	// entry; nil means the dispatcher's next poll picks it up instead.
	kick func()
}

func NewServer(st *store.Store, bot Matrix, auth *AdminAuth, dbType string, kick func()) *Server {
	return &Server{store: st, bot: bot, auth: auth, dbType: dbType, kick: kick}
}

// Login exchanges the admin password for a session JWT, returned in the body
// (for API clients) and as an httpOnly cookie (for the browser, which never
// sees the token itself).
func (s *Server) Login(_ context.Context, req *connect.Request[notifierv1.LoginRequest]) (*connect.Response[notifierv1.LoginResponse], error) {
	token, expiresAt, err := s.auth.Login(req.Msg.Password)
	if err != nil {
		return nil, err
	}
	resp := connect.NewResponse(&notifierv1.LoginResponse{
		Token:     token,
		ExpiresAt: timestamppb.New(expiresAt),
	})
	resp.Header().Set("Set-Cookie", SessionCookie(token, expiresAt, req.Header()))
	return resp, nil
}

// Logout clears the session cookie. Stateless bearer JWTs remain valid until
// expiry; changing the password revokes them all.
func (s *Server) Logout(_ context.Context, req *connect.Request[notifierv1.LogoutRequest]) (*connect.Response[notifierv1.LogoutResponse], error) {
	resp := connect.NewResponse(&notifierv1.LogoutResponse{})
	resp.Header().Set("Set-Cookie", SessionCookie("", time.Time{}, req.Header()))
	return resp, nil
}

// ChangeAdminPassword rotates the password and the JWT secret (revoking all
// sessions), then hands the caller a fresh token so it stays logged in.
func (s *Server) ChangeAdminPassword(ctx context.Context, req *connect.Request[notifierv1.ChangeAdminPasswordRequest]) (*connect.Response[notifierv1.ChangeAdminPasswordResponse], error) {
	token, expiresAt, err := s.auth.ChangePassword(ctx, req.Msg.CurrentPassword, req.Msg.NewPassword)
	if err != nil {
		return nil, err
	}
	resp := connect.NewResponse(&notifierv1.ChangeAdminPasswordResponse{
		Token:     token,
		ExpiresAt: timestamppb.New(expiresAt),
	})
	resp.Header().Set("Set-Cookie", SessionCookie(token, expiresAt, req.Header()))
	return resp, nil
}

func (s *Server) GetStatus(ctx context.Context, _ *connect.Request[notifierv1.GetStatusRequest]) (*connect.Response[notifierv1.GetStatusResponse], error) {
	st := s.bot.Status(ctx)
	resp := &notifierv1.GetStatusResponse{
		UserId:              st.UserID,
		DeviceId:            st.DeviceID,
		Verified:            st.Verified,
		DeliveredSinceStart: st.Delivered,
		UptimeSeconds:       int64(st.Uptime.Seconds()),
		Version:             Version,
		DatabaseType:        s.dbType,
	}
	if !st.LastSync.IsZero() {
		resp.LastSync = timestamppb.New(st.LastSync)
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) ListChannels(ctx context.Context, _ *connect.Request[notifierv1.ListChannelsRequest]) (*connect.Response[notifierv1.ListChannelsResponse], error) {
	channels, err := s.store.ListChannels(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &notifierv1.ListChannelsResponse{}
	for _, ch := range channels {
		resp.Channels = append(resp.Channels, s.protoChannel(ctx, ch))
	}
	return connect.NewResponse(resp), nil
}

// ListRooms exposes every room the bot has joined, annotated with the
// channel bound to it (if any), so the UI can offer unmapped rooms when
// creating channels.
func (s *Server) ListRooms(ctx context.Context, _ *connect.Request[notifierv1.ListRoomsRequest]) (*connect.Response[notifierv1.ListRoomsResponse], error) {
	rooms, err := s.bot.JoinedRooms(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	channels, err := s.store.ListChannels(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	boundTo := make(map[string]string, len(channels))
	for _, ch := range channels {
		boundTo[ch.RoomID] = ch.Name
	}
	resp := &notifierv1.ListRoomsResponse{}
	for _, room := range rooms {
		_, encrypted := s.bot.RoomStatus(ctx, room.ID)
		resp.Rooms = append(resp.Rooms, &notifierv1.Room{
			RoomId:    room.ID,
			Name:      room.Name,
			Channel:   boundTo[room.ID],
			Encrypted: encrypted,
			DmWith:    room.DMWith,
		})
	}
	return connect.NewResponse(resp), nil
}

// LeaveRoom kicks the bot out of a room and cascades: channels bound to the
// room and their tokens are deleted first — after leaving they could never
// deliver anyway.
func (s *Server) LeaveRoom(ctx context.Context, req *connect.Request[notifierv1.LeaveRoomRequest]) (*connect.Response[notifierv1.LeaveRoomResponse], error) {
	if req.Msg.RoomId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("room_id is required"))
	}
	deleted, err := s.store.DeleteChannelsForRoom(ctx, req.Msg.RoomId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if len(deleted) > 0 {
		logging.From(ctx).Info("deleted channels bound to left room", "room_id", req.Msg.RoomId, "channels", deleted)
	}
	if err := s.bot.LeaveRoom(ctx, req.Msg.RoomId); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&notifierv1.LeaveRoomResponse{}), nil
}

func (s *Server) CreateChannel(ctx context.Context, req *connect.Request[notifierv1.CreateChannelRequest]) (*connect.Response[notifierv1.CreateChannelResponse], error) {
	if req.Msg.Name == "" || req.Msg.RoomId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name and room_id are required"))
	}
	// Accept aliases (#foo:server) and store the resolved room ID: every
	// internal lookup (state store, encryption checks) is ID-keyed.
	roomID, err := s.bot.ResolveRoom(ctx, req.Msg.RoomId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	ch, err := s.store.CreateChannel(ctx, req.Msg.Name, roomID, req.Msg.Chart)
	if err != nil {
		return nil, storeError(err)
	}
	return connect.NewResponse(&notifierv1.CreateChannelResponse{Channel: s.protoChannel(ctx, *ch)}), nil
}

func (s *Server) UpdateChannel(ctx context.Context, req *connect.Request[notifierv1.UpdateChannelRequest]) (*connect.Response[notifierv1.UpdateChannelResponse], error) {
	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	ch, err := s.store.UpdateChannelChart(ctx, req.Msg.Name, req.Msg.Chart)
	if err != nil {
		return nil, storeError(err)
	}
	return connect.NewResponse(&notifierv1.UpdateChannelResponse{Channel: s.protoChannel(ctx, *ch)}), nil
}

func (s *Server) DeleteChannel(ctx context.Context, req *connect.Request[notifierv1.DeleteChannelRequest]) (*connect.Response[notifierv1.DeleteChannelResponse], error) {
	if err := s.store.DeleteChannel(ctx, req.Msg.Name); err != nil {
		return nil, storeError(err)
	}
	return connect.NewResponse(&notifierv1.DeleteChannelResponse{}), nil
}

func (s *Server) ListTokens(ctx context.Context, _ *connect.Request[notifierv1.ListTokensRequest]) (*connect.Response[notifierv1.ListTokensResponse], error) {
	tokens, err := s.store.ListTokens(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &notifierv1.ListTokensResponse{}
	for _, tok := range tokens {
		resp.Tokens = append(resp.Tokens, protoToken(tok))
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) CreateToken(ctx context.Context, req *connect.Request[notifierv1.CreateTokenRequest]) (*connect.Response[notifierv1.CreateTokenResponse], error) {
	if req.Msg.Name == "" || req.Msg.Channel == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name and channel are required"))
	}
	kind, err := store.ParseKind(req.Msg.Kind)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var expiresAt *time.Time
	if req.Msg.ExpiresAt != nil {
		t := req.Msg.ExpiresAt.AsTime()
		if t.Before(time.Now()) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("expires_at is in the past"))
		}
		expiresAt = &t
	}
	plaintext, tok, err := s.store.CreateToken(ctx, req.Msg.Name, kind, req.Msg.Channel, req.Msg.Prefix, expiresAt)
	if err != nil {
		return nil, storeError(err)
	}
	return connect.NewResponse(&notifierv1.CreateTokenResponse{
		Token:     protoToken(*tok),
		Plaintext: plaintext,
	}), nil
}

// UpdateToken changes a token's prefix, channel and/or expiry without
// re-minting the credential producers already hold.
func (s *Server) UpdateToken(ctx context.Context, req *connect.Request[notifierv1.UpdateTokenRequest]) (*connect.Response[notifierv1.UpdateTokenResponse], error) {
	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	var expiresAt *time.Time
	if !req.Msg.ClearExpiry && req.Msg.ExpiresAt != nil {
		t := req.Msg.ExpiresAt.AsTime()
		if t.Before(time.Now()) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("expires_at is in the past"))
		}
		expiresAt = &t
	}
	tok, err := s.store.UpdateToken(ctx, req.Msg.Name, req.Msg.Prefix, req.Msg.Channel, expiresAt, req.Msg.ClearExpiry)
	if err != nil {
		return nil, storeError(err)
	}
	return connect.NewResponse(&notifierv1.UpdateTokenResponse{Token: protoToken(*tok)}), nil
}

func (s *Server) DeleteToken(ctx context.Context, req *connect.Request[notifierv1.DeleteTokenRequest]) (*connect.Response[notifierv1.DeleteTokenResponse], error) {
	if err := s.store.DeleteToken(ctx, req.Msg.Name); err != nil {
		return nil, storeError(err)
	}
	return connect.NewResponse(&notifierv1.DeleteTokenResponse{}), nil
}

func (s *Server) SendTestNotification(ctx context.Context, req *connect.Request[notifierv1.SendTestNotificationRequest]) (*connect.Response[notifierv1.SendTestNotificationResponse], error) {
	ch, err := s.store.GetChannel(ctx, req.Msg.Channel)
	if err != nil {
		return nil, storeError(err)
	}
	n := notify.Notification{
		Title:    "Test notification",
		Body:     fmt.Sprintf("Sent from the admin API to channel `%s` at %s.", ch.Name, time.Now().Format(time.RFC3339)),
		Priority: 5,
	}
	if err := s.testSend(ctx, ch.Name, ch.RoomID, n); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&notifierv1.SendTestNotificationResponse{}), nil
}

// testSend delivers synchronously (test buttons want immediate feedback,
// not "queued") and records the outcome so the history stays complete.
func (s *Server) testSend(ctx context.Context, channel, roomID string, n notify.Notification) error {
	sendErr := s.bot.Send(ctx, roomID, n)
	e := &store.OutboxEntry{
		Channel:  channel,
		RoomID:   roomID,
		Kind:     "test",
		Title:    n.Title,
		Body:     n.Body,
		Priority: n.Priority,
		Attempts: 1,
		Status:   store.DeliveryDelivered,
	}
	if sendErr != nil {
		e.Status = store.DeliveryFailed
		e.LastError = sendErr.Error()
	}
	if err := s.store.RecordDelivery(ctx, e); err != nil {
		logging.From(ctx).Error("recording test delivery", "error", err)
	}
	return sendErr
}

// TestToken sends a test notification through a token — to its channel's
// room, with its prefix applied — so the operator sees exactly what that
// producer's messages will look like (emoji and all).
func (s *Server) TestToken(ctx context.Context, req *connect.Request[notifierv1.TestTokenRequest]) (*connect.Response[notifierv1.TestTokenResponse], error) {
	tok, err := s.store.GetToken(ctx, req.Msg.Name)
	if err != nil {
		return nil, storeError(err)
	}
	title := "Test notification"
	if tok.Prefix != "" {
		title = tok.Prefix + " " + title
	}
	n := notify.Notification{
		Title:    title,
		Body:     fmt.Sprintf("Sent via token `%s` at %s.", tok.Name, time.Now().Format(time.RFC3339)),
		Priority: 5,
	}
	if err := s.testSend(ctx, tok.Channel.Name, tok.Channel.RoomID, n); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&notifierv1.TestTokenResponse{}), nil
}

// listDeliveriesDefault/Max bound the history page: it is a debugging view,
// not an export.
const (
	listDeliveriesDefault = 100
	listDeliveriesMax     = 500
)

func (s *Server) ListDeliveries(ctx context.Context, req *connect.Request[notifierv1.ListDeliveriesRequest]) (*connect.Response[notifierv1.ListDeliveriesResponse], error) {
	limit := int(req.Msg.Limit)
	if limit <= 0 {
		limit = listDeliveriesDefault
	}
	limit = min(limit, listDeliveriesMax)
	entries, err := s.store.ListOutbox(ctx, req.Msg.Channel, limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &notifierv1.ListDeliveriesResponse{}
	for _, e := range entries {
		d := &notifierv1.Delivery{
			Id:        int64(e.ID),
			CreatedAt: timestamppb.New(e.CreatedAt),
			Channel:   e.Channel,
			Kind:      e.Kind,
			Title:     e.Title,
			Body:      e.Body,
			Priority:  int32(e.Priority),
			Status:    string(e.Status),
			Attempts:  int32(e.Attempts),
			LastError: e.LastError,
		}
		if e.DeliveredAt != nil {
			d.DeliveredAt = timestamppb.New(*e.DeliveredAt)
		}
		resp.Deliveries = append(resp.Deliveries, d)
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) RetryDelivery(ctx context.Context, req *connect.Request[notifierv1.RetryDeliveryRequest]) (*connect.Response[notifierv1.RetryDeliveryResponse], error) {
	if req.Msg.Id <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}
	if err := s.store.RequeueOutbox(ctx, uint(req.Msg.Id)); err != nil {
		return nil, storeError(err)
	}
	if s.kick != nil {
		s.kick()
	}
	return connect.NewResponse(&notifierv1.RetryDeliveryResponse{}), nil
}

func (s *Server) GetProfile(ctx context.Context, _ *connect.Request[notifierv1.GetProfileRequest]) (*connect.Response[notifierv1.GetProfileResponse], error) {
	p, err := s.bot.Profile(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&notifierv1.GetProfileResponse{
		DisplayName: p.DisplayName,
		Avatar:      p.Avatar,
		AvatarMime:  p.AvatarMIME,
	}), nil
}

// maxAvatarBytes bounds SetProfile uploads: avatars are rendered at chat
// size, anything past 1 MiB is a mistake and would bloat the media repo.
const maxAvatarBytes = 1 << 20

func (s *Server) SetProfile(ctx context.Context, req *connect.Request[notifierv1.SetProfileRequest]) (*connect.Response[notifierv1.SetProfileResponse], error) {
	if req.Msg.DisplayName == "" && len(req.Msg.Avatar) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("nothing to change: set display_name and/or avatar"))
	}
	if len(req.Msg.Avatar) > maxAvatarBytes {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("avatar exceeds %d bytes", maxAvatarBytes))
	}
	if err := s.bot.SetProfile(ctx, req.Msg.DisplayName, req.Msg.Avatar); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&notifierv1.SetProfileResponse{}), nil
}

func (s *Server) protoChannel(ctx context.Context, ch store.Channel) *notifierv1.Channel {
	joined, encrypted := s.bot.RoomStatus(ctx, ch.RoomID)
	return &notifierv1.Channel{
		Name:      ch.Name,
		RoomId:    ch.RoomID,
		Alias:     s.bot.RoomAlias(ctx, ch.RoomID),
		CreatedAt: timestamppb.New(ch.CreatedAt),
		Joined:    joined,
		Encrypted: encrypted,
		Chart:     ch.Chart,
	}
}

func protoToken(tok store.IngestToken) *notifierv1.Token {
	p := &notifierv1.Token{
		Name:      tok.Name,
		Kind:      string(tok.Kind),
		Channel:   tok.Channel.Name,
		Prefix:    tok.Prefix,
		CreatedAt: timestamppb.New(tok.CreatedAt),
	}
	if tok.LastUsedAt != nil {
		p.LastUsedAt = timestamppb.New(*tok.LastUsedAt)
	}
	if tok.ExpiresAt != nil {
		p.ExpiresAt = timestamppb.New(*tok.ExpiresAt)
	}
	return p
}

func storeError(err error) *connect.Error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrAlreadyExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, store.ErrChannelInUse):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
