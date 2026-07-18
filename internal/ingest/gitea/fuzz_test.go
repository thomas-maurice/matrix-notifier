package gitea

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

// Event type (header) and payload are both attacker-influenced; every
// combination must return a value or an error — never panic.
func FuzzParse(f *testing.F) {
	push := []byte(`{"ref":"refs/heads/main","repository":{"full_name":"o/r","html_url":"http://g/o/r"},
		"commits":[{"id":"abcdef1234","message":"fix: x","url":"http://g/c/1","author":{"name":"t"}}],
		"pusher":{"login":"t"}}`)
	f.Add("push", push)
	f.Add("pull_request", []byte(`{"action":"closed","pull_request":{"number":1,"title":"T","merged":true,"html_url":"u"},"repository":{"full_name":"o/r"}}`))
	f.Add("issues", []byte(`{"action":"opened","issue":{"number":2,"title":"I","html_url":"u"},"repository":{"full_name":"o/r"}}`))
	f.Add("release", []byte(`{"action":"published","release":{"tag_name":"v1","html_url":"u"},"repository":{"full_name":"o/r"}}`))
	f.Add("action_run_failure", []byte(`{"run":{"title":"ci","html_url":"u","repository":{"full_name":"o/r"}}}`))
	f.Add("create", []byte(`{"ref":"v1","ref_type":"tag","repository":{"full_name":"o/r"}}`))
	f.Add("unknown_event", []byte(`{}`))
	f.Fuzz(func(_ *testing.T, event string, body []byte) {
		req := httptest.NewRequest("POST", "/gitea", bytes.NewReader(body))
		req.Header.Set("X-Gitea-Event", event)
		_, _ = Parse(req)
	})
}
