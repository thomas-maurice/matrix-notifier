package matrix

import (
	"context"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/thomas-maurice/tocsin/internal/logging"
	"github.com/thomas-maurice/tocsin/internal/notify"
)

const commandPrefix = "!notify"

const helpText = "**tocsin commands**\n\n" +
	"- `!notify ping` — liveness check\n" +
	"- `!notify status` — device, encryption, key backup, and delivery stats\n" +
	"- `!notify test` — send a test notification through the delivery path\n" +
	"- `!notify help` — this text"

// ParseCommand extracts a bot command from a message body. ok is false for
// anything that is not addressed to the bot.
func ParseCommand(body string) (cmd string, args []string, ok bool) {
	fields := strings.Fields(body)
	if len(fields) == 0 || fields[0] != commandPrefix {
		return "", nil, false
	}
	if len(fields) == 1 {
		return "help", nil, true
	}
	return strings.ToLower(fields[1]), fields[2:], true
}

// handleMessage reacts to commands sent in any room the bot has joined.
// Only messages that arrived encrypted after the bot started are considered.
func (b *Bot) handleMessage(ctx context.Context, evt *event.Event) {
	if evt.Sender == b.client.UserID {
		return
	}
	if evt.Timestamp < b.startTime.UnixMilli() {
		return
	}
	// Rooms are E2EE; a plaintext message bypassed encryption and does not
	// get to drive the bot.
	if !evt.Mautrix.WasEncrypted {
		return
	}
	msg := evt.Content.AsMessage()
	if msg.MsgType != event.MsgText {
		return
	}
	cmd, _, ok := ParseCommand(msg.Body)
	if !ok {
		return
	}
	log := logging.From(ctx)
	log.Info("handling command", "command", cmd, "sender", evt.Sender, "room_id", evt.RoomID)
	if _, err := b.sendMarkdown(ctx, evt.RoomID, b.runCommand(ctx, cmd, evt.RoomID)); err != nil {
		log.Error("failed to reply to command", "command", cmd, "error", err)
	}
}

func (b *Bot) runCommand(ctx context.Context, cmd string, roomID id.RoomID) string {
	switch cmd {
	case "ping":
		return fmt.Sprintf("pong 🏓 — up %s", time.Since(b.startTime).Round(time.Second))
	case "status":
		return b.statusText(ctx, roomID)
	case "test":
		_, err := b.Send(ctx, roomID.String(), notify.Notification{
			Title:    "Test notification",
			Body:     "Requested via `!notify test` — the full delivery path works.",
			Priority: 5,
		})
		if err != nil {
			return fmt.Sprintf("test delivery **failed**: %v", err)
		}
		return ""
	case "help":
		return helpText
	default:
		return fmt.Sprintf("unknown command `%s` — try `!notify help`", cmd)
	}
}

func (b *Bot) statusText(ctx context.Context, roomID id.RoomID) string {
	mach := b.helper.Machine()
	verified := "unknown"
	if _, isVerified, err := mach.GetOwnVerificationStatus(ctx); err == nil {
		verified = fmt.Sprintf("%t", isVerified)
	}
	encrypted, _ := b.client.StateStore.IsEncrypted(ctx, roomID)
	return fmt.Sprintf(
		"**tocsin status**\n\n"+
			"- device: `%s` (verified: %s)\n"+
			"- this room encrypted: %t\n"+
			"- notifications delivered since start: %d\n"+
			"- uptime: %s",
		b.client.DeviceID, verified, encrypted,
		b.delivered.Load(), time.Since(b.startTime).Round(time.Second),
	)
}
