package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func TestHTTPCodes(t *testing.T) {
	a, _, _, ck := setup("thesis", "homelab")
	ctx := context.Background()
	a.Tick(ctx)
	ck.add(5 * time.Minute)
	h := newMux(a)

	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"status", "GET", "/status", "", 200},
		{"acquire ok", "POST", "/acquire", `{"name":"homelab","ttl":"5m"}`, 200},
		{"unknown", "POST", "/acquire", `{"name":"nope"}`, 404},
		{"bad json", "POST", "/acquire", `{`, 400},
		{"bad ttl", "POST", "/acquire", `{"name":"homelab","ttl":"99h"}`, 400},
		{"reserve needs by", "POST", "/reserve", `{}`, 400},
		{"reserve", "POST", "/reserve", `{"by":"v1s","for":"1h"}`, 200},
		{"acquire reserved", "POST", "/acquire", `{"name":"homelab"}`, 423},
		{"unreserve", "POST", "/unreserve", "", 200},
	}
	for _, c := range cases {
		if got := do(t, h, c.method, c.path, c.body).Code; got != c.want {
			t.Errorf("%s: code = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestHTTPBusyIs409(t *testing.T) {
	a, _, _, ck := setup("thesis", "homelab", "bench")
	ctx := context.Background()
	a.Tick(ctx)
	ck.add(5 * time.Minute)
	h := newMux(a)
	do(t, h, "POST", "/acquire", `{"name":"homelab","ttl":"30m"}`)
	if got := do(t, h, "POST", "/acquire", `{"name":"bench"}`).Code; got != 409 {
		t.Fatalf("code = %d, want 409", got)
	}
}
