package matrix

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"

	"github.com/thomas-maurice/tocsin/internal/config"
)

// VerifyIdentity proves that the persisted recovery key ALONE can
// re-establish the bot's cryptographic identity — the exact disaster
// recovery the identity model promises. A throwaway device logs in, unlocks
// the server-side SSSS with the on-disk recovery key, decrypts the private
// cross-signing keys, and checks that the derived master public key matches
// the one published on the server. The temporary device is logged out (and
// its local store deleted) on the way out. Read-only with respect to the
// bot's real data_dir and server-side identity.
func VerifyIdentity(ctx context.Context, cfg *config.Config) error {
	keyPath := filepath.Join(cfg.DataDir, "recovery.key")
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("reading recovery key: %w", err)
	}
	recoveryKey := strings.TrimSpace(string(raw))

	client, err := mautrix.NewClient(cfg.Matrix.Homeserver, "", "")
	if err != nil {
		return fmt.Errorf("creating client: %w", err)
	}
	client.Log = zerolog.Nop()

	// Throwaway crypto store: the whole point is proving recovery works
	// WITHOUT the bot's existing store.
	tmpDir, err := os.MkdirTemp("", "tocsin-verify-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	helper, err := cryptohelper.NewCryptoHelper(client, []byte("verify-identity-throwaway"), filepath.Join(tmpDir, "verify.db"))
	if err != nil {
		return fmt.Errorf("creating crypto helper: %w", err)
	}
	helper.LoginAs = &mautrix.ReqLogin{
		Type:                     mautrix.AuthTypePassword,
		Identifier:               mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: cfg.Matrix.UserID},
		Password:                 cfg.Matrix.Password,
		InitialDeviceDisplayName: "verify-identity (temporary)",
	}
	if err := helper.Init(ctx); err != nil {
		return fmt.Errorf("logging in: %w", err)
	}
	defer helper.Close()
	// The temporary device must not linger on the account.
	defer func() { _, _ = client.Logout(context.WithoutCancel(ctx)) }()

	mach := helper.Machine()
	keyID, keyData, err := mach.SSSS.GetDefaultKeyData(ctx)
	if err != nil {
		return fmt.Errorf("the server has no default SSSS key (was the identity ever bootstrapped?): %w", err)
	}
	key, err := keyData.VerifyRecoveryKey(keyID, recoveryKey)
	if err != nil {
		return fmt.Errorf("%s does NOT unlock the server-side SSSS key: %w", keyPath, err)
	}
	if err := mach.FetchCrossSigningKeysFromSSSS(ctx, key); err != nil {
		return fmt.Errorf("decrypting cross-signing keys from SSSS: %w", err)
	}
	derived := mach.CrossSigningKeys.MasterKey.PublicKey()

	resp, err := client.QueryKeys(ctx, &mautrix.ReqQueryKeys{
		DeviceKeys: mautrix.DeviceKeysRequest{client.UserID: mautrix.DeviceIDList{}},
	})
	if err != nil {
		return fmt.Errorf("querying published keys: %w", err)
	}
	published, ok := resp.MasterKeys[client.UserID]
	if !ok {
		return fmt.Errorf("the server publishes no master key for %s", client.UserID)
	}
	if got := published.FirstKey(); got != derived {
		return fmt.Errorf("recovery key decrypts a STALE identity: derived master key %s but the server publishes %s (was the identity reset elsewhere?)", derived, got)
	}
	return nil
}
