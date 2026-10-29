package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/frheg/homelab/services/profctl/profile"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

//go:embed index.html
var page []byte

const (
	consumerLabel = "gpu.kratos/consumer"
	acquireTTL    = "30m"
	acquireWait   = 30 * time.Minute // a consumer like strata can take long to start
	actionWait    = 2 * time.Minute
)

type Panel struct {
	cs  kubernetes.Interface
	gpu gpuClient

	mu   sync.Mutex
	last string // result of the latest action, lost on restart
}

func New(cs kubernetes.Interface, gpuctl string) *Panel {
	return &Panel{cs: cs, gpu: gpuClient{base: gpuctl, hc: &http.Client{}}}
}

func (p *Panel) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page)
	})
	mux.HandleFunc("GET /api/state", p.state)
	mux.HandleFunc("POST /api/gpu/reserve", guard(p.reserve))
	mux.HandleFunc("POST /api/gpu/unreserve", guard(p.unreserve))
	mux.HandleFunc("POST /api/gpu/acquire", guard(p.acquire))
	mux.HandleFunc("POST /api/gpu/release", guard(p.release))
	mux.HandleFunc("POST /api/profile/up", guard(p.profileUp))
	mux.HandleFunc("POST /api/profile/down", guard(p.profileDown))
	return mux
}

// guard lets only the page itself change things. The browser sends Basic credentials
// on cross-site posts too, so ask for a header (forces a CORS preflight) and a same-host Origin.
func guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Panel") != "1" {
			fail(w, http.StatusForbidden, "missing X-Panel header")
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			if u, err := url.Parse(o); err != nil || u.Host != r.Host {
				fail(w, http.StatusForbidden, "cross-origin request")
				return
			}
		}
		h(w, r)
	}
}

type profileView struct {
	Name       string         `json:"name"`
	Namespaces []string       `json:"namespaces"`
	Running    int            `json:"running"` // managed workloads at 1 or more replicas
	Total      int            `json:"total"`   // managed workloads
	Items      []profile.Item `json:"items"`
}

type stateView struct {
	GPU       json.RawMessage `json:"gpu,omitempty"`
	GPUError  string          `json:"gpu_error,omitempty"`
	Consumers []string        `json:"consumers"`
	Profiles  []profileView   `json:"profiles"`
	Error     string          `json:"error,omitempty"`
	Last      string          `json:"last"`
}

func (p *Panel) state(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	v := stateView{Consumers: []string{}, Profiles: []profileView{}, Last: p.lastResult()}
	var err error
	if v.GPU, err = p.gpu.status(ctx); err != nil {
		v.GPUError = err.Error()
	}
	if v.Consumers, err = p.consumers(ctx); err != nil {
		v.Error = err.Error()
	} else if v.Profiles, err = p.profiles(ctx); err != nil {
		v.Error = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// consumers are the names behind the gpu.kratos/consumer label, in any namespace.
func (p *Panel) consumers(ctx context.Context) ([]string, error) {
	ds, err := p.cs.AppsV1().Deployments("").List(ctx, metav1.ListOptions{LabelSelector: consumerLabel})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []string{}
	for _, d := range ds.Items {
		if n := d.Labels[consumerLabel]; !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (p *Panel) profiles(ctx context.Context) ([]profileView, error) {
	ps, err := profile.Profiles(ctx, p.cs)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ps))
	for n := range ps {
		names = append(names, n)
	}
	sort.Strings(names)
	out := []profileView{}
	for _, n := range names {
		items, err := profile.Items(ctx, p.cs, n)
		if err != nil {
			return nil, err
		}
		v := profileView{Name: n, Namespaces: ps[n], Items: items}
		for _, it := range items {
			if it.Note != "" {
				continue
			}
			v.Total++
			if it.Replicas > 0 {
				v.Running++
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (p *Panel) lastResult() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

func (p *Panel) setLast(format string, a ...any) {
	p.mu.Lock()
	p.last = time.Now().Format("15:04:05") + " " + fmt.Sprintf(format, a...)
	p.mu.Unlock()
}

// done records the outcome and answers the browser.
func (p *Panel) done(w http.ResponseWriter, err error, ok string, a ...any) {
	if err != nil {
		p.setLast("%s failed: %v", fmt.Sprintf(ok, a...), err)
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	p.setLast(ok, a...)
	w.WriteHeader(http.StatusNoContent)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (p *Panel) reserve(w http.ResponseWriter, r *http.Request) {
	d := r.URL.Query().Get("for")
	if d != "" {
		if _, err := time.ParseDuration(d); err != nil {
			fail(w, http.StatusBadRequest, "for must be a duration like 2h")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), actionWait)
	defer cancel()
	err := p.gpu.post(ctx, "/reserve", map[string]string{"by": "panel", "for": d})
	p.done(w, err, "reserved the gpu %s", orUntilReleased(d))
}

func orUntilReleased(d string) string {
	if d == "" {
		return "until released"
	}
	return "for " + d
}

func (p *Panel) unreserve(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), actionWait)
	defer cancel()
	p.done(w, p.gpu.post(ctx, "/unreserve", nil), "released the reservation")
}

func (p *Panel) release(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		fail(w, http.StatusBadRequest, "need name")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), actionWait)
	defer cancel()
	p.done(w, p.gpu.post(ctx, "/release", map[string]string{"name": name}), "released %s", name)
}

// acquire answers at once: a switch can take minutes. The page sees the result in state.
func (p *Panel) acquire(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		fail(w, http.StatusBadRequest, "need name")
		return
	}
	p.setLast("switching the gpu to %s ...", name)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), acquireWait)
		defer cancel()
		if err := p.gpu.post(ctx, "/acquire", map[string]string{"name": name, "ttl": acquireTTL}); err != nil {
			p.setLast("switch to %s failed: %v", name, err)
			return
		}
		p.setLast("%s holds the gpu for %s", name, acquireTTL)
	}()
	w.WriteHeader(http.StatusAccepted)
}

func (p *Panel) profileUp(w http.ResponseWriter, r *http.Request) {
	p.profileDo(w, r, "started", profile.Up)
}

func (p *Panel) profileDown(w http.ResponseWriter, r *http.Request) {
	p.profileDo(w, r, "stopped", profile.Down)
}

func (p *Panel) profileDo(w http.ResponseWriter, r *http.Request, verb string, f func(context.Context, kubernetes.Interface, string, io.Writer) error) {
	name := r.URL.Query().Get("name")
	if name == "" {
		fail(w, http.StatusBadRequest, "need name")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), actionWait)
	defer cancel()
	p.done(w, f(ctx, p.cs, name, io.Discard), "%s profile %s", verb, name)
}
