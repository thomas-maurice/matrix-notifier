package alertmanager

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const samplePayload = `{
  "version": "4",
  "status": "firing",
  "receiver": "matrix",
  "groupLabels": {"alertname": "HighCPU"},
  "commonLabels": {"alertname": "HighCPU", "severity": "critical"},
  "externalURL": "http://alertmanager:9093",
  "alerts": [
    {
      "status": "firing",
      "labels": {"alertname": "HighCPU", "severity": "critical", "instance": "node1:9100"},
      "annotations": {"summary": "CPU above 90% for 10m"},
      "startsAt": "2026-07-11T10:00:00Z",
      "generatorURL": "http://prometheus/graph?g0.expr=cpu"
    },
    {
      "status": "resolved",
      "labels": {"alertname": "HighCPU", "severity": "warning", "instance": "node2:9100"},
      "annotations": {"summary": "CPU above 80% for 10m"},
      "startsAt": "2026-07-11T09:00:00Z",
      "endsAt": "2026-07-11T09:30:00Z"
    }
  ]
}`

func TestParseAndFormat(t *testing.T) {
	r := httptest.NewRequest("POST", "/alertmanager", strings.NewReader(samplePayload))
	n, err := Parse(r)
	require.NoError(t, err)

	// The title must show firing/resolved counts and the group name so the
	// room is readable at a glance without opening details.
	assert.Equal(t, "[FIRING:1, RESOLVED:1] HighCPU", n.Title)

	// Firing and resolved alerts must be visually distinct.
	assert.Contains(t, n.Body, "🔥")
	assert.Contains(t, n.Body, "✅")
	// The human-facing annotation must survive formatting.
	assert.Contains(t, n.Body, "CPU above 90% for 10m")
	assert.Contains(t, n.Body, "node1:9100")
	// The generator URL becomes a clickable link.
	assert.Contains(t, n.Body, "](http://prometheus/graph?g0.expr=cpu)")
}

func TestPriorityMapping(t *testing.T) {
	// A firing critical alert must be an emergency (>=8 on the Gotify
	// scale); once resolved it must not be.
	p := &Payload{Alerts: []Alert{{Status: "firing", Labels: map[string]string{"severity": "critical"}}}}
	assert.GreaterOrEqual(t, Format(p).Priority, 8)

	p = &Payload{Alerts: []Alert{{Status: "resolved", Labels: map[string]string{"severity": "critical"}}}}
	assert.Less(t, Format(p).Priority, 8)
}

// Charts are per-alert opt-in via the `chart` annotation: a chart-enabled
// channel must not graph every alert, only the ones the rule author marked.
func TestChartTarget(t *testing.T) {
	p := &Payload{Alerts: []Alert{
		{Status: "firing", GeneratorURL: "http://p/graph?g0.expr=a", Annotations: map[string]string{}},
		{Status: "resolved", GeneratorURL: "http://p/graph?g0.expr=b", Annotations: map[string]string{"chart": "true"}},
		{Status: "firing", GeneratorURL: "http://p/graph?g0.expr=c", Annotations: map[string]string{"chart": "TRUE"}},
	}}
	target := ChartTarget(p)
	require.NotNil(t, target)
	// Firing beats resolved; the un-annotated alert is never picked.
	assert.Contains(t, target.GeneratorURL, "g0.expr=c")

	// Nobody opted in → no chart.
	none := &Payload{Alerts: []Alert{{Status: "firing", GeneratorURL: "http://p/graph?g0.expr=a"}}}
	assert.Nil(t, ChartTarget(none))

	// Opt-in without a generatorURL cannot be charted.
	noURL := &Payload{Alerts: []Alert{{Status: "firing", Annotations: map[string]string{"chart": "true"}}}}
	assert.Nil(t, ChartTarget(noURL))
}

func TestParseRejectsEmptyAlerts(t *testing.T) {
	r := httptest.NewRequest("POST", "/alertmanager", strings.NewReader(`{"version":"4","alerts":[]}`))
	_, err := Parse(r)
	require.Error(t, err)
}
