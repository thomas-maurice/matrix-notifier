// Package alertmanager parses the Prometheus Alertmanager webhook receiver
// payload (version 4) and formats it as a markdown notification.
package alertmanager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/thomas-maurice/tocsin/internal/notify"
)

// graphURL anchors a Prometheus generatorURL to the alert's firing window.
// The URL Prometheus emits has no time parameters, so its graph page opens
// "ending now" — clicked an hour after the alert fired, the interesting
// window has scrolled out of view (and g0.tab=1 opens the table, not the
// graph). Pin the end a bit past the onset and force the graph tab; both
// the classic and the React UI honor range_input/end_input (UTC).
func graphURL(a Alert) string {
	if a.StartsAt.IsZero() {
		return a.GeneratorURL
	}
	u, err := url.Parse(a.GeneratorURL)
	if err != nil {
		return a.GeneratorURL
	}
	q := u.Query()
	if q.Get("g0.expr") == "" {
		// Not a Prometheus graph URL (custom generator); leave it alone.
		return a.GeneratorURL
	}
	q.Set("g0.tab", "0")
	q.Set("g0.range_input", "1h")
	q.Set("g0.end_input", a.StartsAt.UTC().Add(15*time.Minute).Format("2006-01-02 15:04:05"))
	// The UI drops any g0.* param that precedes g0.expr in the query string,
	// and Encode() sorts keys (end_input < expr), so expr must be emitted
	// first by hand.
	expr := q.Get("g0.expr")
	q.Del("g0.expr")
	u.RawQuery = "g0.expr=" + url.QueryEscape(expr) + "&" + q.Encode()
	return u.String()
}

type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

type Payload struct {
	Version           string            `json:"version"`
	Status            string            `json:"status"`
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []Alert           `json:"alerts"`
}

func Parse(r *http.Request) (notify.Notification, error) {
	p, err := ParsePayload(r)
	if err != nil {
		return notify.Notification{}, err
	}
	return Format(p), nil
}

// ParsePayload decodes the raw webhook payload, for callers that need more
// than the formatted notification (e.g. chart rendering).
func ParsePayload(r *http.Request) (*Payload, error) {
	var p Payload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		return nil, fmt.Errorf("invalid alertmanager payload: %w", err)
	}
	if len(p.Alerts) == 0 {
		return nil, fmt.Errorf("payload contains no alerts")
	}
	return &p, nil
}

// Format renders the webhook payload as a markdown notification, one line
// per alert, in the spirit of Alertmanager's own notification templates.
func Format(p *Payload) notify.Notification {
	var firing, resolved int
	for _, a := range p.Alerts {
		if a.Status == "resolved" {
			resolved++
		} else {
			firing++
		}
	}

	var counts []string
	if firing > 0 {
		counts = append(counts, fmt.Sprintf("FIRING:%d", firing))
	}
	if resolved > 0 {
		counts = append(counts, fmt.Sprintf("RESOLVED:%d", resolved))
	}
	title := fmt.Sprintf("[%s] %s", strings.Join(counts, ", "), groupName(p))

	var sb strings.Builder
	for _, a := range p.Alerts {
		sb.WriteString(formatAlert(a))
		sb.WriteString("\n")
	}

	return notify.Notification{
		Title:    title,
		Body:     strings.TrimRight(sb.String(), "\n"),
		Priority: priority(p),
	}
}

func formatAlert(a Alert) string {
	emoji := "🔥"
	if a.Status == "resolved" {
		emoji = "✅"
	}
	name := a.Labels["alertname"]
	if name == "" {
		name = "alert"
	}
	if a.GeneratorURL != "" {
		name = fmt.Sprintf("[%s](%s)", name, graphURL(a))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "- %s **%s**", emoji, name)
	if sev := a.Labels["severity"]; sev != "" {
		fmt.Fprintf(&sb, " `%s`", sev)
	}
	if text := alertText(a); text != "" {
		sb.WriteString(": ")
		sb.WriteString(text)
	}
	if inst := a.Labels["instance"]; inst != "" {
		fmt.Fprintf(&sb, " (`%s`)", inst)
	}
	return sb.String()
}

func alertText(a Alert) string {
	for _, key := range []string{"summary", "description", "message"} {
		if v := a.Annotations[key]; v != "" {
			return v
		}
	}
	return ""
}

func groupName(p *Payload) string {
	if name := p.GroupLabels["alertname"]; name != "" {
		return name
	}
	if len(p.GroupLabels) > 0 {
		pairs := make([]string, 0, len(p.GroupLabels))
		for k, v := range p.GroupLabels {
			pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
		}
		sort.Strings(pairs)
		return strings.Join(pairs, ", ")
	}
	if p.Receiver != "" {
		return p.Receiver
	}
	// Degenerate payloads (no grouping, no receiver): fall back to the
	// first alert's name so the title never ends in a dangling space.
	for _, a := range p.Alerts {
		if name := a.Labels["alertname"]; name != "" {
			return name
		}
	}
	return "alerts"
}

// Fingerprints splits the payload's alert fingerprints by status, for
// resolve-by-edit correlation. Alerts without a fingerprint are skipped.
func Fingerprints(p *Payload) (firing, resolved []string) {
	for _, a := range p.Alerts {
		if a.Fingerprint == "" {
			continue
		}
		if a.Status == "resolved" {
			resolved = append(resolved, a.Fingerprint)
		} else {
			firing = append(firing, a.Fingerprint)
		}
	}
	return firing, resolved
}

// ChartTarget picks the alert to chart: charts are opt-in per alert via the
// `chart` annotation (rule authors set it), preferring firing alerts. Returns
// nil when no alert opted in or none has a generator URL.
func ChartTarget(p *Payload) *Alert {
	var target *Alert
	for i := range p.Alerts {
		a := &p.Alerts[i]
		if a.GeneratorURL == "" || !chartRequested(a) {
			continue
		}
		if a.Status != "resolved" {
			return a
		}
		if target == nil {
			target = a
		}
	}
	return target
}

func chartRequested(a *Alert) bool {
	switch strings.ToLower(a.Annotations["chart"]) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

// priority maps severity onto the Gotify priority scale: any firing critical
// alert is an emergency, warnings are mid-scale, everything else is low.
func priority(p *Payload) int {
	prio := 3
	for _, a := range p.Alerts {
		if a.Status == "resolved" {
			continue
		}
		switch a.Labels["severity"] {
		case "critical":
			return 8
		case "warning":
			prio = 5
		}
	}
	return prio
}
