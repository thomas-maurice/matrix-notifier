package slack

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

// Slack-compat input arrives as JSON or a legacy payload= form, full of
// mrkdwn to convert; any body must return a value or an error — never
// panic.
func FuzzParse(f *testing.F) {
	f.Add("application/json", []byte(`{"text":"pool degraded","username":"TrueNAS"}`))
	f.Add("application/json", []byte(`{"blocks":[{"type":"header","text":{"type":"plain_text","text":"H"}},
		{"type":"section","text":{"type":"mrkdwn","text":"<http://x|link> &amp; more"}}],
		"attachments":[{"color":"danger","title":"T","text":"t"}]}`))
	f.Add("application/x-www-form-urlencoded", []byte(`payload=%7B%22text%22%3A%22hi%22%7D`))
	f.Add("application/json", []byte(`{"text":"<@U123> <!channel> <http://a|b>"}`))
	f.Fuzz(func(_ *testing.T, contentType string, body []byte) {
		req := httptest.NewRequest("POST", "/slack", bytes.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		_, _ = Parse(req)
	})
}
