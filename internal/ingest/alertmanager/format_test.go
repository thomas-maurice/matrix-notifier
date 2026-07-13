package alertmanager

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The title's group name is what people scan first; every fallback level
// must produce something meaningful rather than "[FIRING:1] ".
func TestGroupNameFallbacks(t *testing.T) {
	assert.Equal(t, "HighCPU", groupName(&Payload{GroupLabels: map[string]string{"alertname": "HighCPU"}}))
	// No alertname: deterministic sorted key=value pairs.
	assert.Equal(t, "env=prod, team=net", groupName(&Payload{GroupLabels: map[string]string{"team": "net", "env": "prod"}}))
	// No group labels at all: the receiver name.
	assert.Equal(t, "matrix", groupName(&Payload{Receiver: "matrix"}))
}

func TestPriorityLevels(t *testing.T) {
	// warning maps mid-scale, unknown severities stay low — only a firing
	// critical is an emergency.
	warn := &Payload{Alerts: []Alert{{Status: "firing", Labels: map[string]string{"severity": "warning"}}}}
	assert.Equal(t, 5, Format(warn).Priority)
	info := &Payload{Alerts: []Alert{{Status: "firing", Labels: map[string]string{"severity": "info"}}}}
	assert.Equal(t, 3, Format(info).Priority)
}

// Annotation precedence: summary is the curated human text; description and
// message are fallbacks in that order.
func TestAlertTextPrecedence(t *testing.T) {
	a := Alert{Annotations: map[string]string{"summary": "S", "description": "D", "message": "M"}}
	assert.Equal(t, "S", alertText(a))
	a.Annotations = map[string]string{"description": "D", "message": "M"}
	assert.Equal(t, "D", alertText(a))
	a.Annotations = map[string]string{"message": "M"}
	assert.Equal(t, "M", alertText(a))
	a.Annotations = nil
	assert.Equal(t, "", alertText(a))
}

// A minimal alert (no severity, no annotations, no URL, no instance) must
// still render a sensible line.
func TestFormatMinimalAlert(t *testing.T) {
	n := Format(&Payload{Alerts: []Alert{{Status: "firing", Labels: map[string]string{"alertname": "Bare"}}}})
	assert.Equal(t, "[FIRING:1] Bare", n.Title)
	assert.Equal(t, "- 🔥 **Bare**", n.Body)
}

// When only resolved alerts opted into charts, the resolved one is charted
// (better than nothing when the firing alert lacked the annotation).
func TestChartTargetResolvedFallback(t *testing.T) {
	p := &Payload{Alerts: []Alert{
		{Status: "firing", GeneratorURL: "http://p/graph?g0.expr=a"},
		{Status: "resolved", GeneratorURL: "http://p/graph?g0.expr=b", Annotations: map[string]string{"chart": "yes"}},
	}}
	target := ChartTarget(p)
	assert.NotNil(t, target)
	assert.Equal(t, "resolved", target.Status)
}
