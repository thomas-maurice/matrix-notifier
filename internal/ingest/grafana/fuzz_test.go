package grafana

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

// Grafana webhook payloads are attacker-influenced input; Parse must return
// a value or an error — never panic.
func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"receiver":"matrix","status":"firing","groupLabels":{"alertname":"SensorOffline","grafana_folder":"IoT"},
		"alerts":[{"status":"firing","labels":{"alertname":"SensorOffline","severity":"warning","instance":"greenhouse"},
		"annotations":{"summary":"no data for 5m"},"generatorURL":"http://g/view","panelURL":"http://g/d/iot?viewPanel=2"}]}`))
	f.Add([]byte(`{"status":"resolved","alerts":[{"status":"resolved"}]}`))
	f.Add([]byte(`{"alerts":[]}`))
	f.Add([]byte(`]`))
	f.Fuzz(func(_ *testing.T, body []byte) {
		_, _ = Parse(httptest.NewRequest("POST", "/grafana", bytes.NewReader(body)))
	})
}
