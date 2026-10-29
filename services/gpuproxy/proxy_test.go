package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeAcq struct {
	calls atomic.Int64
	delay time.Duration
	err   error
}

func (f *fakeAcq) Acquire(ctx context.Context, c string, ttl time.Duration) error {
	f.calls.Add(1)
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	return f.err
}

func setup(t *testing.T, h http.HandlerFunc, acq Acquirer, ttl time.Duration) (*Proxy, *httptest.Server) {
	t.Helper()
	be := httptest.NewServer(h)
	t.Cleanup(be.Close)
	p, err := New("llm", be.URL, acq, ttl)
	if err != nil {
		t.Fatal(err)
	}
	return p, be
}

func get(p *Proxy, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func TestFirstRequestAcquiresThenForwards(t *testing.T) {
	acq := &fakeAcq{}
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello "+r.URL.Path) }, acq, time.Minute)
	w := get(p, "/v1/models")
	if w.Code != 200 || w.Body.String() != "hello /v1/models" {
		t.Fatalf("code=%d body=%q", w.Code, w.Body.String())
	}
	if acq.calls.Load() != 1 {
		t.Fatalf("acquire calls = %d", acq.calls.Load())
	}
}

func TestLeaseIsReusedUntilHalfTTL(t *testing.T) {
	acq := &fakeAcq{}
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {}, acq, time.Minute)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }
	get(p, "/a")
	now = now.Add(20 * time.Second)
	get(p, "/b")
	if acq.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 inside half the ttl", acq.calls.Load())
	}
	now = now.Add(20 * time.Second) // 40s: past 30s
	get(p, "/c")
	if acq.calls.Load() != 2 {
		t.Fatalf("calls = %d, want a renewal after half the ttl", acq.calls.Load())
	}
}

func TestConcurrentRequestsShareOneAcquire(t *testing.T) {
	acq := &fakeAcq{delay: 150 * time.Millisecond}
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }, acq, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); get(p, "/") }()
	}
	wg.Wait()
	if acq.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 for 20 concurrent requests", acq.calls.Load())
	}
}

func TestRefusedBecomes503WithRetryAfter(t *testing.T) {
	var hit atomic.Bool
	acq := &fakeAcq{err: &RefusedError{Msg: "gpu reserved by v1s until released"}}
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { hit.Store(true) }, acq, time.Minute)
	w := get(p, "/")
	if w.Code != 503 || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), "reserved") {
		t.Fatalf("code=%d retry=%q body=%q", w.Code, w.Header().Get("Retry-After"), w.Body.String())
	}
	if hit.Load() {
		t.Fatal("the backend must not be called when the GPU is refused")
	}
}

func TestOtherAcquireErrorIs502AndIsNotCached(t *testing.T) {
	acq := &fakeAcq{err: errors.New("boom")}
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {}, acq, time.Minute)
	if w := get(p, "/"); w.Code != 502 {
		t.Fatalf("code = %d", w.Code)
	}
	get(p, "/")
	if acq.calls.Load() != 2 {
		t.Fatalf("a failed acquire must be retried, calls = %d", acq.calls.Load())
	}
}

func TestHealthzDoesNotWakeTheGPU(t *testing.T) {
	acq := &fakeAcq{}
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {}, acq, time.Minute)
	if w := get(p, "/_gpuproxy/healthz"); w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	if acq.calls.Load() != 0 {
		t.Fatal("a health probe must not acquire the GPU")
	}
}

func TestResponsesAreStreamed(t *testing.T) {
	release := make(chan struct{})
	acq := &fakeAcq{}
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "second\n")
	}, acq, time.Minute)
	front := httptest.NewServer(p)
	defer front.Close()
	resp, err := http.Get(front.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 6)
	got := make(chan string, 1)
	go func() { io.ReadFull(resp.Body, buf); got <- string(buf) }()
	select {
	case s := <-got:
		if s != "first\n" {
			t.Fatalf("got %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first chunk not delivered while the backend is still writing: response is buffered")
	}
	close(release)
}

func TestLeaseIsRenewedWhileARequestIsInFlight(t *testing.T) {
	acq := &fakeAcq{}
	release := make(chan struct{})
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { <-release }, acq, 90*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Renew(ctx)
	done := make(chan struct{})
	go func() { get(p, "/"); close(done) }()
	time.Sleep(400 * time.Millisecond)
	during := acq.calls.Load()
	close(release)
	<-done
	if during < 3 {
		t.Fatalf("acquire calls while busy = %d, want several renewals", during)
	}
	time.Sleep(300 * time.Millisecond)
	if after := acq.calls.Load(); after != during {
		t.Fatalf("renewals continued with nothing in flight: %d -> %d", during, after)
	}
}

func TestHTTPAcquirerMapsStatuses(t *testing.T) {
	cases := []struct {
		status  int
		body    string
		refused bool
		ok      bool
	}{
		{200, `{}`, false, true},
		{409, `{"error":"gpu busy"}`, true, false},
		{423, `{"error":"gpu reserved"}`, true, false},
		{500, `{"error":"boom"}`, false, false},
	}
	for _, c := range cases {
		var gotBody map[string]string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&gotBody)
			w.WriteHeader(c.status)
			io.WriteString(w, c.body)
		}))
		err := httpAcquirer{base: srv.URL, client: srv.Client()}.Acquire(context.Background(), "llm", 2*time.Minute)
		srv.Close()
		var re *RefusedError
		switch {
		case c.ok && err != nil, !c.ok && err == nil:
			t.Errorf("status %d: err = %v", c.status, err)
		case errors.As(err, &re) != c.refused:
			t.Errorf("status %d: refused = %v, want %v", c.status, errors.As(err, &re), c.refused)
		}
		if gotBody["name"] != "llm" || gotBody["ttl"] != "2m0s" {
			t.Errorf("status %d: request body = %v", c.status, gotBody)
		}
	}
}

// hookAcq lets a test change the world when the arbiter is asked.
type hookAcq struct {
	calls atomic.Int64
	hook  func(n int64)
}

func (h *hookAcq) Acquire(ctx context.Context, c string, ttl time.Duration) error {
	n := h.calls.Add(1)
	if h.hook != nil {
		h.hook(n)
	}
	return nil
}

func TestStaleLeaseIsRecoveredWhenTheBackendIsGone(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	serve := func(l net.Listener) *http.Server {
		s := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "woke") })}
		go s.Serve(l)
		return s
	}
	srv := serve(ln)
	var current atomic.Pointer[http.Server]
	current.Store(srv)

	acq := &hookAcq{}
	acq.hook = func(n int64) {
		if n == 2 { // the arbiter brings the consumer back: the backend listens again
			l, err := net.Listen("tcp", addr)
			if err != nil {
				t.Errorf("relisten: %v", err)
				return
			}
			current.Store(serve(l))
		}
	}
	p, err := New("llm", "http://"+addr, acq, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if w := get(p, "/"); w.Code != 200 || acq.calls.Load() != 1 {
		t.Fatalf("setup: code=%d acquires=%d", w.Code, acq.calls.Load())
	}
	// someone else takes the GPU: our backend disappears while the lease still looks valid
	srv.Close()
	w := get(p, "/")
	if w.Code != 200 || w.Body.String() != "woke" {
		t.Fatalf("stale lease not recovered: code=%d body=%q", w.Code, w.Body.String())
	}
	if acq.calls.Load() != 2 {
		t.Fatalf("acquires = %d, want 2 (the retry asked again)", acq.calls.Load())
	}
	current.Load().Close()
}

func TestRefusedDuringRetryIs503(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close() // nothing listens: every dial fails
	acq := &fakeAcq{err: &RefusedError{Msg: "gpu reserved by v1s until released"}}
	p, _ := New("llm", "http://"+addr, acq, time.Minute)
	p.validUntil = time.Now().Add(time.Hour) // pretend we hold a lease
	w := get(p, "/")
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("code=%d retry=%q body=%q", w.Code, w.Header().Get("Retry-After"), w.Body.String())
	}
}

func TestBackendThatIsLateToListenGetsItsPostBody(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	var srv *http.Server
	acq := &hookAcq{}
	acq.hook = func(n int64) {
		if n == 1 { // the endpoint shows up shortly after the arbiter says ready
			time.AfterFunc(150*time.Millisecond, func() {
				l, err := net.Listen("tcp", addr)
				if err != nil {
					t.Errorf("listen: %v", err)
					return
				}
				srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					io.WriteString(w, "got "+string(b))
				})}
				go srv.Serve(l)
			})
		}
	}
	p, _ := New("llm", "http://"+addr, acq, time.Minute)
	p.dialPause = 20 * time.Millisecond
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"x":1}`)))
	if w.Code != 200 || w.Body.String() != `got {"x":1}` {
		t.Fatalf("code=%d body=%q", w.Code, w.Body.String())
	}
	srv.Close()
}

func TestBackendThatNeverListensIs502AfterTheGrace(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	p, _ := New("llm", "http://"+addr, &fakeAcq{}, time.Minute)
	p.dialGrace, p.dialPause = 100*time.Millisecond, 20*time.Millisecond
	if w := get(p, "/"); w.Code != 502 {
		t.Fatalf("code=%d", w.Code)
	}
}
