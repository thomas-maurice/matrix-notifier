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
	ResolveRoom(ctx context.Context, room string) (string, error)
	JoinedRooms(ctx context.Context) ([]matrix.RoomInfo, error)
	LeaveRoom(ctx context.Context, roomID string) error
}

type Server struct {
	store  *store.Store
	bot    Matrix
	dbType string
}

func NewServer(st *store.Store, bot Matrix, dbType string) *Server {
	return &Server{store: st, bot: bot, dbType: dbType}
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
	plaintext, tok, err := s.store.CreateToken(ctx, req.Msg.Name, kind, req.Msg.Channel, req.Msg.Prefix)
	if err != nil {
		return nil, storeError(err)
	}
	return connect.NewResponse(&notifierv1.CreateTokenResponse{
		Token:     protoToken(*tok),
		Plaintext: plaintext,
	}), nil
}

// UpdateToken changes a token's notification prefix without re-minting the
// credential producers already hold.
func (s *Server) UpdateToken(ctx context.Context, req *connect.Request[notifierv1.UpdateTokenRequest]) (*connect.Response[notifierv1.UpdateTokenResponse], error) {
	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	tok, err := s.store.UpdateTokenPrefix(ctx, req.Msg.Name, req.Msg.Prefix)
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
	err = s.bot.Send(ctx, ch.RoomID, notify.Notification{
		Title:    "Test notification",
		Body:     fmt.Sprintf("Sent from the admin API to channel `%s` at %s.", ch.Name, time.Now().Format(time.RFC3339)),
		Priority: 5,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&notifierv1.SendTestNotificationResponse{}), nil
}

func (s *Server) protoChannel(ctx context.Context, ch store.Channel) *notifierv1.Channel {
	joined, encrypted := s.bot.RoomStatus(ctx, ch.RoomID)
	return &notifierv1.Channel{
		Name:      ch.Name,
		RoomId:    ch.RoomID,
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
