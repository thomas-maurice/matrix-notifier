package matrix

import (
	"context"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/verificationhelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/thomas-maurice/matrix-notifier/internal/logging"
)

// inRoomVerificationEventTypes are the in-room verification steps that
// reference their transaction via m.relates_to.
var inRoomVerificationEventTypes = []event.Type{
	event.InRoomVerificationReady, event.InRoomVerificationStart,
	event.InRoomVerificationAccept, event.InRoomVerificationKey,
	event.InRoomVerificationMAC, event.InRoomVerificationDone,
	event.InRoomVerificationCancel,
}

// RegisterInRoomVerificationFix works around a mautrix v0.28 defect: the
// verificationhelper resolves the transaction of an in-room verification
// event from the event's own ID, but in-room transactions are keyed by the
// *request* event's ID, carried in m.relates_to. Rewriting evt.ID to the
// relates-to target before the helper's handlers run makes the lookup
// resolve. Echoes of our own events are neutralized instead (blank evt.ID on
// a non-Transactionable content makes the helper drop the event) — the
// helper would otherwise mistake them for peer messages and cancel the
// transaction. Must be registered BEFORE VerificationHelper.Init so it runs
// first; handler order is registration order.
func RegisterInRoomVerificationFix(client *mautrix.Client, syncer mautrix.ExtensibleSyncer) {
	normalize := func(ctx context.Context, evt *event.Event) {
		normalizeInRoomVerificationEvent(evt, client.UserID)
	}
	for _, t := range inRoomVerificationEventTypes {
		syncer.OnEventType(t, normalize)
	}
}

func normalizeInRoomVerificationEvent(evt *event.Event, self id.UserID) {
	if evt.Sender == self {
		evt.ID = ""
		return
	}
	if rel, ok := evt.Content.Parsed.(event.Relatable); ok {
		if rt := rel.OptionalGetRelatesTo(); rt != nil && rt.EventID != "" {
			evt.ID = rt.EventID
		}
	}
}

// verificationCallbacks makes the bot a passive SAS responder: it accepts
// any incoming verification request, waits for the initiator to start SAS,
// confirms immediately (logging the emojis so the human can compare on their
// side), and lets verificationhelper do the cross-signing on completion.
// Requests only arrive from rooms/devices that can already talk to the bot,
// and the human initiator is the one comparing the short auth string.
type verificationCallbacks struct {
	b *Bot
}

func (v *verificationCallbacks) VerificationRequested(ctx context.Context, txnID id.VerificationTransactionID, from id.UserID, fromDevice id.DeviceID) {
	log := logging.From(ctx)
	log.Info("verification requested, auto-accepting", "txn_id", txnID, "from", from, "from_device", fromDevice)
	// Callbacks may be invoked while the helper holds its transaction lock;
	// calling back into the helper synchronously deadlocks.
	go func() {
		if err := v.b.verifHelper.AcceptVerification(ctx, txnID); err != nil {
			log.Error("failed to accept verification", "txn_id", txnID, "error", err)
		}
	}()
}

func (v *verificationCallbacks) VerificationReady(ctx context.Context, txnID id.VerificationTransactionID, otherDeviceID id.DeviceID, supportsSAS, supportsScanQRCode bool, qrCode *verificationhelper.QRCode) {
	// Passive: the initiating client sends m.key.verification.start when the
	// user picks a method. Nothing to do here.
	logging.From(ctx).Info("verification ready, waiting for initiator to start SAS", "txn_id", txnID, "other_device", otherDeviceID)
}

func (v *verificationCallbacks) VerificationCancelled(ctx context.Context, txnID id.VerificationTransactionID, code event.VerificationCancelCode, reason string) {
	logging.From(ctx).Warn("verification cancelled", "txn_id", txnID, "code", code, "reason", reason)
}

func (v *verificationCallbacks) VerificationDone(ctx context.Context, txnID id.VerificationTransactionID, method event.VerificationMethod) {
	logging.From(ctx).Info("verification done", "txn_id", txnID, "method", method)
}

func (v *verificationCallbacks) ShowSAS(ctx context.Context, txnID id.VerificationTransactionID, emojis []rune, emojiDescriptions []string, decimals []int) {
	log := logging.From(ctx)
	log.Info("SAS generated, auto-confirming", "txn_id", txnID, "emojis", string(emojis), "descriptions", emojiDescriptions)
	go func() {
		if err := v.b.verifHelper.ConfirmSAS(ctx, txnID); err != nil {
			log.Error("failed to confirm SAS", "txn_id", txnID, "error", err)
		}
	}()
}
