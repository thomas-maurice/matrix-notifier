package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/thomas-maurice/matrix-notifier/internal/config"
	"github.com/thomas-maurice/matrix-notifier/internal/matrix"
)

// newVerifyIdentityCmd builds the cron-able identity check: it proves the
// on-disk recovery key can re-establish the bot's identity from nothing.
// Exit 0 with one line on success, non-zero with the reason otherwise.
func newVerifyIdentityCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "verify-identity",
		Short: "Prove the persisted recovery key still unlocks the account's cross-signing identity",
		Long: `Logs in as a TEMPORARY device (removed afterwards), unlocks the server-side
SSSS with data_dir/recovery.key, decrypts the private cross-signing keys and
checks the derived master key against the one published on the server.

recovery.key is the only file worth backing up: with it, a fresh bot
rebuilds its crypto store and re-verifies automatically. Run this from cron
so a silently mismatched key (e.g. after an identity reset elsewhere) is
discovered before a disaster, not during one.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()
			if err := matrix.VerifyIdentity(ctx, cfg); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "OK: recovery key unlocks SSSS and matches the published master key")
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "path to config file (default: ./config.yaml)")
	return cmd
}
