// Package grafana parses Grafana unified-alerting webhook payloads and
// formats them as markdown notifications. The payload is a superset of the
// Alertmanager receiver format: same alert grouping, plus per-alert
// dashboard/panel links which are rendered when the rule is bound to a
// dashboard.
package grafana

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/thomas-maurice/tocsin/internal/notify"
)

type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	GeneratorURL string            `json:"generatorURL"`
	DashboardURL string            `json:"dashboardURL"`
	PanelURL     string            `json:"panelURL"`
	Fingerprint  string            `json:"fingerprint"`
}

type Payload struct {
	Status      string            `json:"status"`
	Receiver    string            `json:"receiver"`
	GroupLabels map[string]string `json:"groupLabels"`
	Alerts      []Alert           `json:"alerts"`
}

func Parse(r *http.Request) (notify.Notification, error) {
	p, err := ParsePayload(r)
	if err != nil {
		return notify.Notification{}, err
	}
	return Format(p), nil
}

// ParsePayload decodes the raw webhook payload, for callers that also need
// the alert fingerprints for resolve-by-edit correlation.
func ParsePayload(r *http.Request) (*Payload, error) {
	var p Payload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		return nil, fmt.Errorf("invalid grafana payload: %w", err)
	}
	if len(p.Alerts) == 0 {
		return nil, fmt.Errorf("payload contains no alerts")
	}
	return &p, nil
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

// Format renders one line per alert, in the same spirit as the alertmanager
// receiver so mixed rooms read uniformly.
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
		name = fmt.Sprintf("[%s](%s)", name, a.GeneratorURL)
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
	// Grafana sets these only when the rule is linked to a dashboard panel;
	// that link is where an operator acts on the alert, so surface it.
	if link := firstOf(a.PanelURL, a.DashboardURL); link != "" {
		fmt.Fprintf(&sb, " [📈](%s)", link)
	}
	return sb.String()
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
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
	// grafana_folder is grouping metadata, not a name — never title on it.
	if name := p.GroupLabels["alertname"]; name != "" {
		return name
	}
	pairs := make([]string, 0, len(p.GroupLabels))
	for k, v := range p.GroupLabels {
		if k == "grafana_folder" {
			continue
		}
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	if len(pairs) > 0 {
		sort.Strings(pairs)
		return strings.Join(pairs, ", ")
	}
	// Unlike Alertmanager, Grafana policies often don't group by alertname
	// (groupLabels arrives empty) and the receiver is just the contact-point
	// name — so prefer the first alert's own name.
	for _, a := range p.Alerts {
		if name := a.Labels["alertname"]; name != "" {
			return name
		}
	}
	if p.Receiver != "" {
		return p.Receiver
	}
	return "alerts"
}

// priority maps severity onto the Gotify scale exactly like the
// alertmanager receiver: firing critical → emergency, warning → mid-scale,
// everything else (including resolved-only payloads) → low.
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
