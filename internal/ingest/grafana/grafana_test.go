package grafana

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parse(t *testing.T, body string) (title, md string, prio int) {
	t.Helper()
	n, err := Parse(httptest.NewRequest("POST", "/grafana", strings.NewReader(body)))
	require.NoError(t, err)
	return n.Title, n.Body, n.Priority
}

// A real (abridged) Grafana unified-alerting webhook payload must produce a
// counted title, a linked alert line, and the panel link operators click to
// act on the alert.
func TestParseFiringWithPanelLink(t *testing.T) {
	title, md, prio := parse(t, `{
		"receiver": "matrix",
		"status": "firing",
		"groupLabels": {"alertname": "SensorOffline", "grafana_folder": "IoT"},
		"alerts": [{
			"status": "firing",
			"labels": {"alertname": "SensorOffline", "severity": "warning", "instance": "greenhouse"},
			"annotations": {"summary": "no data for 5m"},
			"generatorURL": "http://grafana:3000/alerting/grafana/abc/view",
			"dashboardURL": "http://grafana:3000/d/iot",
			"panelURL": "http://grafana:3000/d/iot?viewPanel=2"
		}]
	}`)
	assert.Equal(t, "[FIRING:1] SensorOffline", title)
	assert.Contains(t, md, "🔥 **[SensorOffline](http://grafana:3000/alerting/grafana/abc/view)**")
	assert.Contains(t, md, "`warning`")
	assert.Contains(t, md, "no data for 5m")
	assert.Contains(t, md, "(`greenhouse`)")
	// Panel beats dashboard: it is the most specific place to act.
	assert.Contains(t, md, "[📈](http://grafana:3000/d/iot?viewPanel=2)")
	assert.Equal(t, 5, prio)
}

// Severity must drive priority the same way the alertmanager receiver does,
// so a critical IoT alert pings like an emergency.
func TestPriorityMapping(t *testing.T) {
	_, _, prio := parse(t, `{"alerts":[{"status":"firing","labels":{"severity":"critical"}}]}`)
	assert.Equal(t, 8, prio)

	_, _, prio = parse(t, `{"alerts":[{"status":"resolved","labels":{"severity":"critical"}}]}`)
	assert.Equal(t, 3, prio, "resolved alerts must not escalate priority")
}

func TestResolvedRendering(t *testing.T) {
	title, md, _ := parse(t, `{
		"status": "resolved",
		"groupLabels": {"alertname": "SensorOffline"},
		"alerts": [{"status": "resolved", "labels": {"alertname": "SensorOffline"}}]
	}`)
	assert.Equal(t, "[RESOLVED:1] SensorOffline", title)
	assert.Contains(t, md, "✅")
}

// grafana_folder is grouping metadata Grafana always injects, and the
// receiver is just the contact-point name — neither says WHAT is alerting.
// With no usable group labels the title must fall back to the alert's name.
func TestFolderLabelNotUsedAsTitle(t *testing.T) {
	title, _, _ := parse(t, `{
		"receiver": "matrix",
		"groupLabels": {"grafana_folder": "IoT"},
		"alerts": [{"status": "firing", "labels": {"alertname": "X"}}]
	}`)
	assert.Equal(t, "[FIRING:1] X", title)
}

// Garbage and empty payloads must be rejected so the endpoint 400s instead
// of queueing an empty notification.
func TestRejectsBadPayloads(t *testing.T) {
	_, err := Parse(httptest.NewRequest("POST", "/grafana", strings.NewReader("not json")))
	require.Error(t, err)
	_, err = Parse(httptest.NewRequest("POST", "/grafana", strings.NewReader(`{"alerts":[]}`)))
	require.Error(t, err)
}
