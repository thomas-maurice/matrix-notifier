package alertmanager

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

// Webhook payloads are attacker-influenced input; Parse must return a value
// or an error — never panic — including on the URL-mangling graph-anchor
// path and the chart-target scan.
func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"version":"4","status":"firing","groupLabels":{"alertname":"Down"},
		"alerts":[{"status":"firing","labels":{"alertname":"Down","severity":"critical"},
		"annotations":{"summary":"it broke","chart":"true"},
		"generatorURL":"http://p/graph?g0.expr=up","startsAt":"2026-07-18T10:00:00Z"}]}`))
	f.Add([]byte(`{"alerts":[{"status":"resolved"}]}`))
	f.Add([]byte(`{"alerts":[]}`))
	f.Add([]byte(`{`))
	f.Fuzz(func(_ *testing.T, body []byte) {
		p, err := ParsePayload(httptest.NewRequest("POST", "/alertmanager", bytes.NewReader(body)))
		if err != nil {
			return
		}
		_ = Format(p)
		_ = ChartTarget(p)
	})
}
