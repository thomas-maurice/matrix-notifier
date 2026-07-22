package alertmanager

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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
	// The generator URL becomes a clickable link, anchored to the firing
	// window (see TestGraphURLAnchoredToFiringWindow for the details).
	assert.Contains(t, n.Body, "](http://prometheus/graph?g0.expr=cpu&")
	assert.Contains(t, n.Body, "g0.end_input=")
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

// Prometheus generatorURLs carry no time anchor, so the graph page opens
// "ending now" — useless once the alert is old. The link must be pinned to
// the alert's firing window, on the graph tab, and non-Prometheus generator
// URLs must pass through untouched.
func TestGraphURLAnchoredToFiringWindow(t *testing.T) {
	starts := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	a := Alert{
		StartsAt:     starts,
		GeneratorURL: "http://prom:9090/graph?g0.expr=up%3D%3D0&g0.tab=1",
	}
	u, err := url.Parse(graphURL(a))
	require.NoError(t, err)
	q := u.Query()
	assert.Equal(t, "up==0", q.Get("g0.expr"))
	assert.Equal(t, "0", q.Get("g0.tab"), "must open the graph tab, not the table")
	assert.Equal(t, "1h", q.Get("g0.range_input"))
	assert.Equal(t, "2026-07-18 10:15:00", q.Get("g0.end_input"), "window must end shortly after the onset")
	assert.True(t, strings.HasPrefix(u.RawQuery, "g0.expr="),
		"g0.expr must be the first query param: the Prometheus/Thanos UI drops g0.* params that precede it")

	// A generator URL that is not a Prometheus graph link stays untouched.
	custom := Alert{StartsAt: starts, GeneratorURL: "https://thanos.example/alert/42"}
	assert.Equal(t, "https://thanos.example/alert/42", graphURL(custom))

	// No StartsAt: nothing sensible to anchor to; leave the URL alone.
	noStart := Alert{GeneratorURL: "http://prom:9090/graph?g0.expr=up&g0.tab=1"}
	assert.Equal(t, "http://prom:9090/graph?g0.expr=up&g0.tab=1", graphURL(noStart))
}
