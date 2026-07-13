package gotify

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shell scripts commonly use `curl -F`, which sends multipart — it must
// behave identically to JSON and urlencoded bodies.
func TestParseMultipart(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("title", "MP"))
	require.NoError(t, w.WriteField("message", "multipart body"))
	require.NoError(t, w.WriteField("priority", "7"))
	require.NoError(t, w.Close())

	r := httptest.NewRequest("POST", "/message", &buf)
	r.Header.Set("Content-Type", w.FormDataContentType())

	n, err := Parse(r)
	require.NoError(t, err)
	assert.Equal(t, "MP", n.Title)
	assert.Equal(t, "multipart body", n.Body)
	assert.Equal(t, 7, n.Priority)
}

func TestParseJSONWithExtrasAndNoTitle(t *testing.T) {
	// Gotify clients often send extras and omit the title; both must be
	// tolerated.
	r := httptest.NewRequest("POST", "/message", strings.NewReader(
		`{"message":"body only","extras":{"client::display":{"contentType":"text/markdown"}}}`))
	r.Header.Set("Content-Type", "application/json")
	n, err := Parse(r)
	require.NoError(t, err)
	assert.Empty(t, n.Title)
	assert.Equal(t, "body only", n.Body)
}

func TestParseMalformedJSON(t *testing.T) {
	r := httptest.NewRequest("POST", "/message", strings.NewReader(`{"message":`))
	r.Header.Set("Content-Type", "application/json")
	_, err := Parse(r)
	require.Error(t, err)
}

func TestParseContentTypeWithCharset(t *testing.T) {
	// "application/json; charset=utf-8" must be treated as JSON, not form.
	r := httptest.NewRequest("POST", "/message", strings.NewReader(`{"message":"x"}`))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	n, err := Parse(r)
	require.NoError(t, err)
	assert.Equal(t, "x", n.Body)
}
