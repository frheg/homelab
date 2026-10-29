package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// Acquirer asks the arbiter for the GPU. It blocks until the consumer is ready.
type Acquirer interface {
	Acquire(ctx context.Context, consumer string, ttl time.Duration) error
}

// RefusedError means the arbiter said no: the GPU is reserved or leased to someone else.
type RefusedError struct{ Msg string }

func (e *RefusedError) Error() string { return e.Msg }

// Proxy forwards requests to one GPU consumer and makes sure it holds the GPU first.
type Proxy struct {
	consumer string
	acq      Acquirer
	ttl      time.Duration
	backend  *httputil.ReverseProxy
	now      func() time.Time

	mu         sync.Mutex // one acquire at a time, the others wait for its result
	validUntil time.Time  // our lease is safe to reuse until then (half of ttl)
	inflight   atomic.Int64
}

func New(consumer, backendURL string, acq Acquirer, ttl time.Duration) (*Proxy, error) {
	u, err := url.Parse(backendURL)
	if err != nil {
		return nil, err
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	orig := rp.Director
	rp.Director = func(r *http.Request) { orig(r); r.Host = u.Host }
	rp.FlushInterval = -1 // stream tokens as they come
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("backend error: %v", err)
		http.Error(w, "backend error: "+err.Error(), http.StatusBadGateway)
	}
	return &Proxy{consumer: consumer, acq: acq, ttl: ttl, backend: rp, now: time.Now}, nil
}

// ensure holds the lease. Callers that arrive during an acquire wait for it.
func (p *Proxy) ensure(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.now().Before(p.validUntil) {
		return nil
	}
	if err := p.acq.Acquire(ctx, p.consumer, p.ttl); err != nil {
		return err
	}
	p.validUntil = p.now().Add(p.ttl / 2)
	return nil
}

// Renew keeps the lease alive while requests are in flight, e.g. a long generation.
// With no request in flight it does nothing, and the lease runs out by itself.
func (p *Proxy) Renew(ctx context.Context) {
	t := time.NewTicker(p.ttl / 3)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if p.inflight.Load() > 0 {
				if err := p.ensure(ctx); err != nil {
					log.Printf("renew: %v", err)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/_gpuproxy/healthz" {
		io.WriteString(w, "ok\n")
		return
	}
	p.inflight.Add(1)
	defer p.inflight.Add(-1)

	if err := p.ensure(r.Context()); err != nil {
		var refused *RefusedError
		switch {
		case errors.Is(err, context.Canceled):
			return // the client went away while waiting
		case errors.As(err, &refused):
			w.Header().Set("Retry-After", "30")
			http.Error(w, refused.Msg, http.StatusServiceUnavailable)
		default:
			log.Printf("acquire %s: %v", p.consumer, err)
			http.Error(w, "gpu: "+err.Error(), http.StatusBadGateway)
		}
		return
	}
	p.backend.ServeHTTP(w, r)
}

// httpAcquirer talks to gpud (POST /acquire).
type httpAcquirer struct {
	base   string
	client *http.Client
}

func (h httpAcquirer) Acquire(ctx context.Context, consumer string, ttl time.Duration) error {
	body, _ := json.Marshal(map[string]string{"name": consumer, "ttl": ttl.String()})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.base+"/acquire", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	var e struct{ Error string }
	json.NewDecoder(resp.Body).Decode(&e)
	if e.Error == "" {
		e.Error = resp.Status
	}
	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusLocked {
		return &RefusedError{Msg: e.Error}
	}
	return fmt.Errorf("%s", e.Error)
}
