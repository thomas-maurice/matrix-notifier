package gotify

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

// The parser chews fully attacker-influenced input across three content
// types (JSON, urlencoded, multipart); whatever arrives, it must return a
// value or an error — never panic.
func FuzzParse(f *testing.F) {
	f.Add("application/json", []byte(`{"title":"t","message":"hello","priority":5}`))
	f.Add("application/json", []byte(`{"title":"no message"}`))
	f.Add("application/x-www-form-urlencoded", []byte("title=F&message=form+body"))
	f.Add("multipart/form-data; boundary=b", []byte("--b\r\nContent-Disposition: form-data; name=\"message\"\r\n\r\nhi\r\n--b--\r\n"))
	f.Add("", []byte("not json at all"))
	f.Fuzz(func(_ *testing.T, contentType string, body []byte) {
		req := httptest.NewRequest("POST", "/message", bytes.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		_, _ = Parse(req)
	})
}
