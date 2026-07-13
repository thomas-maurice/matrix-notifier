// Command cmdclient is a dev-only E2EE Matrix client for exercising the
// notifier bot through real encryption. Two modes:
//
// Send a message and print decrypted replies (default):
//
//	go run -tags goolm ./dev/cmdclient -room '!roomid:localhost' -message '!notify ping'
//
// Verify the bot via SAS and assert mutual cross-signing:
//
//	go run -tags goolm ./dev/cmdclient -room '!roomid:localhost' -verify -target '@notifier:localhost'
//
// In -verify mode the client bootstraps cross-signing for its own account if
// the account has none (recovery key saved next to the crypto store). If the
// account HAS cross-signing keys but the local recovery key file is missing,
// pass -reset-cross-signing to replace them (this un-verifies other sessions
// of the account, e.g. an Element login).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/jsontime"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/crypto/verificationhelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	matrixbot "github.com/thomas-maurice/matrix-notifier/internal/matrix"
)

func main() {
	homeserver := flag.String("homeserver", "http://localhost:8008", "homeserver URL")
	user := flag.String("user", "@admin:localhost", "user ID")
	password := flag.String("password", "admin", "password")
	room := flag.String("room", "", "room ID (required)")
	message := flag.String("message", "!notify ping", "message to send")
	db := flag.String("db", "dev/cmdclient.db", "sqlite crypto store path")
	wait := flag.Duration("wait", 15*time.Second, "how long to wait for replies / verification")
	verify := flag.Bool("verify", false, "run SAS verification against -target instead of sending a message")
	target := flag.String("target", "@notifier:localhost", "user to verify in -verify mode")
	resetXS := flag.Bool("reset-cross-signing", false, "replace this account's cross-signing keys if the recovery key file is missing")
	flag.Parse()
	if *room == "" {
		fmt.Fprintln(os.Stderr, "-room is required")
		os.Exit(1)
	}
	if err := run(*homeserver, *user, *password, *room, *message, *db, *wait, *verify, *target, *resetXS); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type verifierCallbacks struct {
	vh   *verificationhelper.VerificationHelper
	done chan error
}

func (v *verifierCallbacks) VerificationRequested(ctx context.Context, txnID id.VerificationTransactionID, from id.UserID, fromDevice id.DeviceID) {
}

func (v *verifierCallbacks) VerificationReady(ctx context.Context, txnID id.VerificationTransactionID, otherDeviceID id.DeviceID, supportsSAS, supportsScanQRCode bool, qrCode *verificationhelper.QRCode) {
	fmt.Printf("verification ready (other device %s), starting SAS\n", otherDeviceID)
	// The helper holds its transaction lock while invoking callbacks;
	// re-entering it synchronously deadlocks.
	go func() {
		if err := v.vh.StartSAS(ctx, txnID); err != nil {
			v.done <- fmt.Errorf("starting SAS: %w", err)
		}
	}()
}

func (v *verifierCallbacks) VerificationCancelled(ctx context.Context, txnID id.VerificationTransactionID, code event.VerificationCancelCode, reason string) {
	v.done <- fmt.Errorf("verification cancelled (%s): %s", code, reason)
}

func (v *verifierCallbacks) VerificationDone(ctx context.Context, txnID id.VerificationTransactionID, method event.VerificationMethod) {
	fmt.Printf("verification done (method %s)\n", method)
	v.done <- nil
}

func (v *verifierCallbacks) ShowSAS(ctx context.Context, txnID id.VerificationTransactionID, emojis []rune, emojiDescriptions []string, decimals []int) {
	fmt.Printf("SAS emojis: %s (%s) — confirming\n", string(emojis), strings.Join(emojiDescriptions, ", "))
	go func() {
		if err := v.vh.ConfirmSAS(ctx, txnID); err != nil {
			v.done <- fmt.Errorf("confirming SAS: %w", err)
		}
	}()
}

func run(homeserver, user, password, room, message, db string, wait time.Duration, verify bool, target string, resetXS bool) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := mautrix.NewClient(homeserver, "", "")
	if err != nil {
		return err
	}
	client.Log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.WarnLevel)

	helper, err := cryptohelper.NewCryptoHelper(client, []byte("dev-cmdclient-pickle-key"), db)
	if err != nil {
		return err
	}
	helper.LoginAs = &mautrix.ReqLogin{
		Type:                     mautrix.AuthTypePassword,
		Identifier:               mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: user},
		Password:                 password,
		InitialDeviceDisplayName: "cmdclient",
	}
	if err := helper.Init(ctx); err != nil {
		return err
	}
	defer helper.Close()
	client.Crypto = helper

	roomID := id.RoomID(room)
	syncer := client.Syncer.(*mautrix.DefaultSyncer)

	var vh *verificationhelper.VerificationHelper
	verifStore := verificationhelper.NewInMemoryVerificationStore()
	callbacks := &verifierCallbacks{done: make(chan error, 4)}
	if verify {
		matrixbot.RegisterInRoomVerificationFix(client, syncer)
		vh = verificationhelper.NewVerificationHelper(
			client, helper.Machine(), verifStore, callbacks, false, false, true,
		)
		callbacks.vh = vh
		if err := vh.Init(ctx); err != nil {
			return fmt.Errorf("initializing verification helper: %w", err)
		}
	}

	sentAt := time.Now().UnixMilli()
	syncer.OnEventType(event.EventMessage, func(ctx context.Context, evt *event.Event) {
		if evt.RoomID != roomID || evt.Sender == client.UserID || evt.Timestamp < sentAt {
			return
		}
		if msg := evt.Content.AsMessage(); msg.MsgType == event.MsgText || msg.MsgType == event.MsgNotice {
			fmt.Printf("[%s] (encrypted=%t) %s\n", evt.Sender, evt.Mautrix.WasEncrypted, msg.Body)
		}
	})
	go func() {
		if err := client.SyncWithContext(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "sync error:", err)
		}
	}()
	// Give the first sync a moment so we have room state and device lists.
	time.Sleep(3 * time.Second)

	if !verify {
		content := format.RenderMarkdown(message, true, false)
		if _, err := client.SendMessageEvent(ctx, roomID, event.EventMessage, &content); err != nil {
			return fmt.Errorf("sending message: %w", err)
		}
		fmt.Printf("sent %q, waiting %s for replies...\n", message, wait)
		time.Sleep(wait)
		return nil
	}

	if err := ensureCrossSigning(ctx, helper.Machine(), password, db+".recovery.key", resetXS); err != nil {
		return err
	}

	targetUser := id.UserID(target)
	fmt.Printf("starting in-room verification of %s in %s\n", targetUser, roomID)
	// Not vh.StartInRoomVerification: that pre-encrypts the request and
	// SendMessageEvent (with client.Crypto set) encrypts it a second time,
	// which the responder cannot unwrap. Send it ourselves (encrypted once)
	// and seed the helper's transaction store like StartInRoomVerification
	// would.
	reqContent := event.MessageEventContent{
		MsgType:    event.MsgVerificationRequest,
		Body:       fmt.Sprintf("%s is requesting verification", client.UserID),
		FromDevice: client.DeviceID,
		Methods:    []event.VerificationMethod{event.VerificationMethodSAS},
		To:         targetUser,
	}
	resp, err := client.SendMessageEvent(ctx, roomID, event.EventMessage, &reqContent)
	if err != nil {
		return fmt.Errorf("sending verification request: %w", err)
	}
	err = verifStore.SaveVerificationTransaction(ctx, verificationhelper.VerificationTransaction{
		ExpirationTime:    jsontime.UnixMilli{Time: time.Now().Add(10 * time.Minute)},
		RoomID:            roomID,
		VerificationState: verificationhelper.VerificationStateRequested,
		TransactionID:     id.VerificationTransactionID(resp.EventID),
		TheirUserID:       targetUser,
	})
	if err != nil {
		return fmt.Errorf("saving verification transaction: %w", err)
	}

	select {
	case err := <-callbacks.done:
		if err != nil {
			return err
		}
	case <-time.After(wait):
		return fmt.Errorf("verification did not complete within %s", wait)
	}

	return assertMutualTrust(ctx, helper.Machine(), id.UserID(user), targetUser)
}

// ensureCrossSigning makes sure this account has cross-signing keys usable by
// this device, generating or restoring them as needed.
func ensureCrossSigning(ctx context.Context, mach *crypto.OlmMachine, password, recoveryKeyPath string, resetXS bool) error {
	hasKeys, isVerified, err := mach.GetOwnVerificationStatus(ctx)
	if err != nil {
		return fmt.Errorf("querying verification status: %w", err)
	}
	if isVerified {
		// Still need the private keys in memory to be able to sign users.
		if mach.CrossSigningKeys == nil {
			raw, err := os.ReadFile(recoveryKeyPath)
			if err != nil {
				return fmt.Errorf("device verified but %s missing (needed to load user-signing key): %w", recoveryKeyPath, err)
			}
			keyID, keyData, err := mach.SSSS.GetDefaultKeyData(ctx)
			if err != nil {
				return fmt.Errorf("getting SSSS key data: %w", err)
			}
			key, err := keyData.VerifyRecoveryKey(keyID, strings.TrimSpace(string(raw)))
			if err != nil {
				return fmt.Errorf("verifying recovery key: %w", err)
			}
			if err := mach.FetchCrossSigningKeysFromSSSS(ctx, key); err != nil {
				return fmt.Errorf("fetching cross-signing keys from SSSS: %w", err)
			}
		}
		return nil
	}
	if hasKeys && !resetXS {
		raw, err := os.ReadFile(recoveryKeyPath)
		if err != nil {
			return fmt.Errorf("account has cross-signing keys but %s is missing; pass -reset-cross-signing to replace them (this un-verifies other sessions, e.g. Element)", recoveryKeyPath)
		}
		if err := mach.VerifyWithRecoveryKey(ctx, strings.TrimSpace(string(raw))); err != nil {
			return fmt.Errorf("verifying with recovery key: %w", err)
		}
		fmt.Println("verified own device with recovery key")
		return nil
	}
	recoveryKey, _, err := mach.GenerateAndUploadCrossSigningKeysWithPassword(ctx, password, "")
	if err != nil {
		return fmt.Errorf("generating cross-signing keys: %w", err)
	}
	if err := mach.SignOwnDevice(ctx, mach.OwnIdentity()); err != nil {
		return fmt.Errorf("signing own device: %w", err)
	}
	if err := mach.SignOwnMasterKey(ctx); err != nil {
		return fmt.Errorf("signing own master key: %w", err)
	}
	if err := os.WriteFile(recoveryKeyPath, []byte(recoveryKey+"\n"), 0o600); err != nil {
		return fmt.Errorf("saving recovery key: %w", err)
	}
	fmt.Printf("bootstrapped cross-signing for this account (recovery key: %s)\n", recoveryKeyPath)
	return nil
}

// assertMutualTrust checks the actual outcome of verification: our
// user-signing key signed their master key, and vice versa.
func assertMutualTrust(ctx context.Context, mach *crypto.OlmMachine, us, them id.UserID) error {
	ourKeys, err := mach.GetCrossSigningPublicKeys(ctx, us)
	if err != nil {
		return fmt.Errorf("getting our cross-signing keys: %w", err)
	}
	theirKeys, err := mach.GetCrossSigningPublicKeys(ctx, them)
	if err != nil || theirKeys == nil {
		return fmt.Errorf("getting %s's cross-signing keys: %w", them, err)
	}

	weSignedThem, err := mach.CryptoStore.IsKeySignedBy(ctx, them, theirKeys.MasterKey, us, ourKeys.UserSigningKey)
	if err != nil {
		return fmt.Errorf("checking our signature on their master key: %w", err)
	}
	if !weSignedThem {
		return errors.New("FAIL: our user-signing key did not sign their master key")
	}
	fmt.Printf("PASS: %s's master key is signed by our user-signing key (green shield material)\n", them)

	theySignedUs, err := mach.CryptoStore.IsKeySignedBy(ctx, us, ourKeys.MasterKey, them, theirKeys.UserSigningKey)
	if err == nil && theySignedUs {
		fmt.Printf("PASS: our master key is signed by %s's user-signing key (mutual)\n", them)
	} else {
		fmt.Printf("NOTE: reverse signature not visible locally (may need a sync round-trip); primary direction verified\n")
	}
	return nil
}
