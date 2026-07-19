package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newSendCmd builds the `send` subcommand: a small client that posts a
// notification to a tocsin's Gotify endpoint. Handy for scripts and
// cron jobs.
func newSendCmd() *cobra.Command {
	var (
		server   string
		token    string
		title    string
		message  string
		priority int
	)
	cmd := &cobra.Command{
		Use:   "send [message]",
		Short: "Send a notification to a tocsin instance",
		Long: "Post a notification to a tocsin (Gotify endpoint). The message is\n" +
			"taken from the argument, --message, or stdin (in that order).\n\n" +
			"Server and token default to the TOCSIN_URL and\n" +
			"TOCSIN_TOKEN environment variables.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if server == "" {
				server = os.Getenv("TOCSIN_URL")
			}
			if token == "" {
				token = os.Getenv("TOCSIN_TOKEN")
			}
			if len(args) == 1 {
				message = args[0]
			}
			if message == "" {
				raw, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("reading message from stdin: %w", err)
				}
				message = strings.TrimRight(string(raw), "\n")
			}
			return send(server, token, title, message, priority)
		},
	}
	cmd.Flags().StringVar(&server, "url", "", "notifier base URL (env TOCSIN_URL)")
	cmd.Flags().StringVar(&token, "token", "", "ingest token (env TOCSIN_TOKEN)")
	cmd.Flags().StringVarP(&title, "title", "t", "", "notification title")
	cmd.Flags().StringVarP(&message, "message", "m", "", "notification message (markdown; else read from stdin)")
	cmd.Flags().IntVarP(&priority, "priority", "p", 5, "Gotify priority (>=8 is emergency)")
	return cmd
}

func send(server, token, title, message string, priority int) error {
	if server == "" {
		return fmt.Errorf("no server URL (--url or TOCSIN_URL)")
	}
	if token == "" {
		return fmt.Errorf("no token (--token or TOCSIN_TOKEN)")
	}
	if message == "" {
		return fmt.Errorf("empty message")
	}
	body, err := json.Marshal(map[string]any{
		"title":    title,
		"message":  message,
		"priority": priority,
	})
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(server, "/") + "/message?token=" + url.QueryEscape(token)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sending notification: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("notifier returned %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}
