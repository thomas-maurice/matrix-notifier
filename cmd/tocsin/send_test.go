package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSend(t *testing.T) {
	var gotToken, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.URL.Query().Get("token")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := send(srv.URL, "tcsn_tok", "Title", "hello **world**", 7); err != nil {
		t.Fatal(err)
	}
	if gotToken != "tcsn_tok" {
		t.Fatalf("token not forwarded: %q", gotToken)
	}
	// Gotify-shaped JSON body with our fields.
	for _, want := range []string{`"title":"Title"`, `"message":"hello **world**"`, `"priority":7`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body missing %s: %s", want, gotBody)
		}
	}
}

func TestSendErrors(t *testing.T) {
	if err := send("", "t", "", "m", 5); err == nil {
		t.Error("expected error for missing URL")
	}
	if err := send("http://x", "", "", "m", 5); err == nil {
		t.Error("expected error for missing token")
	}
	// Non-200 from the server must surface as an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	if err := send(srv.URL, "bad", "", "m", 5); err == nil {
		t.Error("expected error for 401")
	}
}
