// Package slack parses Slack incoming-webhook payloads so tools that only
// speak "Slack webhook" (TrueNAS alert services, Uptime Kuma, ...) can point
// at this bot. Slack accepts a JSON body or a urlencoded form with the JSON
// in a `payload` field; so do we. Slack mrkdwn links (<url|label>) and the
// HTML entities Slack requires (&amp; &lt; &gt;) are converted to markdown.
package slack

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"github.com/thomas-maurice/matrix-notifier/internal/notify"
)

type textObject struct {
	Text string `json:"text"`
}

type block struct {
	Type string      `json:"type"`
	Text *textObject `json:"text"`
}

type attachment struct {
	Title    string  `json:"title"`
	Text     string  `json:"text"`
	Fallback string  `json:"fallback"`
	Color    string  `json:"color"`
	Blocks   []block `json:"blocks"`
}

type payload struct {
	Text        string       `json:"text"`
	Username    string       `json:"username"`
	Attachments []attachment `json:"attachments"`
	Blocks      []block      `json:"blocks"`
}

// Parse reads a Slack incoming-webhook message from an HTTP request.
func Parse(r *http.Request) (notify.Notification, error) {
	var p payload
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch contentType {
	case "application/json":
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			return notify.Notification{}, fmt.Errorf("invalid JSON body: %w", err)
		}
	default:
		if err := r.ParseMultipartForm(1 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
			return notify.Notification{}, fmt.Errorf("invalid form body: %w", err)
		}
		raw := r.FormValue("payload")
		if raw == "" {
			return notify.Notification{}, fmt.Errorf("missing payload field")
		}
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return notify.Notification{}, fmt.Errorf("invalid payload JSON: %w", err)
		}
	}

	var parts []string
	if t := mrkdwn(p.Text); t != "" {
		parts = append(parts, t)
	}
	parts = append(parts, blockText(p.Blocks)...)
	for _, a := range p.Attachments {
		var sb strings.Builder
		if a.Title != "" {
			fmt.Fprintf(&sb, "**%s**\n", mrkdwn(a.Title))
		}
		if t := mrkdwn(firstNonEmpty(a.Text, a.Fallback)); t != "" {
			sb.WriteString(t)
		}
		for _, bt := range blockText(a.Blocks) {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(bt)
		}
		if s := strings.TrimRight(sb.String(), "\n"); s != "" {
			parts = append(parts, s)
		}
	}
	body := strings.Join(parts, "\n")
	if body == "" {
		return notify.Notification{}, fmt.Errorf("text is required")
	}
	return notify.Notification{
		Title:    p.Username,
		Body:     body,
		Priority: priority(p.Attachments),
	}, nil
}

func blockText(blocks []block) []string {
	var parts []string
	for _, b := range blocks {
		if b.Text == nil || b.Text.Text == "" {
			continue
		}
		switch b.Type {
		case "header":
			parts = append(parts, "**"+mrkdwn(b.Text.Text)+"**")
		case "section":
			parts = append(parts, mrkdwn(b.Text.Text))
		}
	}
	return parts
}

// priority maps Slack attachment colors onto the Gotify scale: senders flag
// severity through color, and a "danger" alert must outrank routine chatter.
func priority(attachments []attachment) int {
	prio := 3
	for _, a := range attachments {
		switch a.Color {
		case "danger":
			return 5
		case "warning":
			prio = 4
		}
	}
	return prio
}

var slackLink = regexp.MustCompile(`<(https?://[^|>]+)\|([^>]+)>`)

var slackEntities = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&")

// mrkdwn converts the Slack-specific parts of mrkdwn to markdown: labeled
// links and the three entities Slack requires senders to escape. Bare <url>
// is left alone (valid markdown autolink).
func mrkdwn(s string) string {
	return slackEntities.Replace(slackLink.ReplaceAllString(s, "[$2]($1)"))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
