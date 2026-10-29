package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// fakeGPU records the posts it gets and answers like gpuctl.
type fakeGPU struct {
	mu    sync.Mutex
	posts []string
	fail  string // error text to answer with, empty for success
	seen  chan string
}

func newFakeGPU(t *testing.T) (*fakeGPU, string) {
	f := &fakeGPU{seen: make(chan string, 8)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Write([]byte(`{"holder":"llm","reserved_by":""}`))
			return
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		b, _ := json.Marshal(body)
		f.mu.Lock()
		f.posts = append(f.posts, r.URL.Path+" "+string(b))
		fail := f.fail
		f.mu.Unlock()
		f.seen <- r.URL.Path
		if fail != "" {
			w.WriteHeader(http.StatusLocked)
			json.NewEncoder(w).Encode(map[string]string{"error": fail})
			return
		}
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func i32(n int32) *int32 { return &n }

func cluster() *fake.Clientset {
	ns := func(name string, l map[string]string) *corev1.Namespace {
		return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: l}}
	}
	dep := func(ns, name string, n int32, l map[string]string) *appsv1.Deployment {
		return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: l}, Spec: appsv1.DeploymentSpec{Replicas: i32(n)}}
	}
	return fake.NewSimpleClientset(
		ns("db", map[string]string{"profile.kratos/lab": "true"}),
		dep("db", "pg", 1, nil),
		dep("db", "api", 1, nil),
		dep("db", "llm", 0, map[string]string{consumerLabel: "llm"}),
		dep("db", "proxy", 1, map[string]string{"gpu.kratos/proxy": "llm"}),
		dep("other", "sd", 0, map[string]string{consumerLabel: "sd"}),
	)
}

func do(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var panelHdr = map[string]string{"X-Panel": "1"}

func TestStateJoinsGPUConsumersAndProfiles(t *testing.T) {
	_, url := newFakeGPU(t)
	h := New(cluster(), url).Handler()
	w := do(h, "GET", "/api/state", nil)
	var s struct {
		GPU       map[string]string
		Consumers []string
		Profiles  []profileView
	}
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if s.GPU["holder"] != "llm" {
		t.Fatalf("gpu = %v", s.GPU)
	}
	if strings.Join(s.Consumers, ",") != "llm,sd" {
		t.Fatalf("consumers = %v", s.Consumers)
	}
	if len(s.Profiles) != 1 || s.Profiles[0].Name != "lab" || s.Profiles[0].Running != 2 || s.Profiles[0].Total != 2 {
		t.Fatalf("profiles = %+v", s.Profiles)
	}
}

func TestStateStillServesWhenGPUCtlIsDown(t *testing.T) {
	h := New(cluster(), "http://127.0.0.1:1").Handler()
	w := do(h, "GET", "/api/state", nil)
	var s stateView
	json.Unmarshal(w.Body.Bytes(), &s)
	if w.Code != 200 || s.GPUError == "" || len(s.Profiles) != 1 {
		t.Fatalf("code=%d state=%s", w.Code, w.Body.String())
	}
}

func TestChangesNeedTheHeaderAndTheSameOrigin(t *testing.T) {
	f, url := newFakeGPU(t)
	h := New(cluster(), url).Handler()
	if w := do(h, "POST", "/api/gpu/unreserve", nil); w.Code != 403 {
		t.Fatalf("no header: %d", w.Code)
	}
	if w := do(h, "POST", "/api/gpu/unreserve", map[string]string{"X-Panel": "1", "Origin": "http://evil.example"}); w.Code != 403 {
		t.Fatalf("cross origin: %d", w.Code)
	}
	if w := do(h, "POST", "/api/gpu/unreserve", map[string]string{"X-Panel": "1", "Origin": "http://example.com"}); w.Code != 204 {
		t.Fatalf("same origin: %d", w.Code) // httptest.NewRequest uses Host example.com
	}
	if w := do(h, "GET", "/api/gpu/unreserve", panelHdr); w.Code != 405 {
		t.Fatalf("GET must not change anything: %d", w.Code)
	}
	if len(f.posts) != 1 {
		t.Fatalf("gpuctl got %v", f.posts)
	}
}

func TestReserveSendsWhoAndHowLong(t *testing.T) {
	f, url := newFakeGPU(t)
	h := New(cluster(), url).Handler()
	if w := do(h, "POST", "/api/gpu/reserve?for=2h", panelHdr); w.Code != 204 {
		t.Fatalf("code=%d %s", w.Code, w.Body.String())
	}
	if f.posts[0] != `/reserve {"by":"panel","for":"2h"}` {
		t.Fatalf("posted %q", f.posts[0])
	}
	if w := do(h, "POST", "/api/gpu/reserve?for=banana", panelHdr); w.Code != 400 {
		t.Fatalf("bad duration: %d", w.Code)
	}
}

func TestRefusalFromGPUCtlIsShownAndRemembered(t *testing.T) {
	f, url := newFakeGPU(t)
	f.fail = "gpu reserved by v1s until released"
	p := New(cluster(), url)
	w := do(p.Handler(), "POST", "/api/gpu/release?name=llm", panelHdr)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "reserved by v1s") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(p.lastResult(), "reserved by v1s") {
		t.Fatalf("last = %q", p.lastResult())
	}
}

func TestAcquireAnswersAtOnceAndFinishesInTheBackground(t *testing.T) {
	f, url := newFakeGPU(t)
	p := New(cluster(), url)
	w := do(p.Handler(), "POST", "/api/gpu/acquire?name=sd", panelHdr)
	if w.Code != http.StatusAccepted {
		t.Fatalf("code=%d", w.Code)
	}
	select {
	case path := <-f.seen:
		if path != "/acquire" {
			t.Fatalf("path %s", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gpuctl never got the acquire")
	}
	for i := 0; i < 100 && !strings.Contains(p.lastResult(), "holds the gpu"); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(p.lastResult(), "sd holds the gpu") {
		t.Fatalf("last = %q", p.lastResult())
	}
}

func replicas(t *testing.T, cs *fake.Clientset, ns, name string) int32 {
	t.Helper()
	d, err := cs.AppsV1().Deployments(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return *d.Spec.Replicas
}

func TestProfileDownAndUpLeaveGPUPartsAlone(t *testing.T) {
	_, url := newFakeGPU(t)
	cs := cluster()
	h := New(cs, url).Handler()
	if w := do(h, "POST", "/api/profile/down?name=lab", panelHdr); w.Code != 204 {
		t.Fatalf("down: %d %s", w.Code, w.Body.String())
	}
	if replicas(t, cs, "db", "pg") != 0 || replicas(t, cs, "db", "api") != 0 {
		t.Fatal("services should be stopped")
	}
	if replicas(t, cs, "db", "proxy") != 1 {
		t.Fatal("the gpu proxy must stay up")
	}
	if w := do(h, "POST", "/api/profile/up?name=lab", panelHdr); w.Code != 204 {
		t.Fatalf("up: %d", w.Code)
	}
	if replicas(t, cs, "db", "pg") != 1 {
		t.Fatal("pg should be back")
	}
	if w := do(h, "POST", "/api/profile/up?name=nope", panelHdr); w.Code != http.StatusBadGateway {
		t.Fatalf("unknown profile: %d", w.Code)
	}
}

func TestPageIsServed(t *testing.T) {
	h := New(cluster(), "http://127.0.0.1:1").Handler()
	w := do(h, "GET", "/", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<title>kratos panel</title>") {
		t.Fatalf("code=%d", w.Code)
	}
}

// fakeMonitoring answers like VictoriaMetrics (instant queries) and Alertmanager (v2 alerts).
func fakeMonitoring(t *testing.T, alerts string, missing string) (vm, am string) {
	t.Helper()
	v := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if q == missing {
			w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
			return
		}
		val := map[string]string{
			"nvidia_smi_memory_used_bytes":     "7.5e9",
			"nvidia_smi_memory_total_bytes":    "1.7e10",
			"nvidia_smi_temperature_gpu":       "52",
			"nvidia_smi_power_draw_watts":      "88.5",
			"nvidia_smi_utilization_gpu_ratio": "0.4",
		}[q]
		w.Write([]byte(`{"status":"success","data":{"result":[{"metric":{},"value":[1,"` + val + `"]}]}}`))
	}))
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/alerts" || r.URL.Query().Get("silenced") != "false" {
			t.Errorf("unexpected request %s", r.URL.String())
		}
		w.Write([]byte(alerts))
	}))
	t.Cleanup(v.Close)
	t.Cleanup(a.Close)
	return v.URL, a.URL
}

func TestStateCarriesGPUNumbersAndAlertsWorstFirst(t *testing.T) {
	_, gpu := newFakeGPU(t)
	vm, am := fakeMonitoring(t, `[
	  {"labels":{"alertname":"DiskAlmostFull","severity":"warning"},"annotations":{"summary":"Root disk under 15% free"}},
	  {"labels":{"alertname":"NodeDown","severity":"critical"},"annotations":{"summary":"host metrics are gone"}}]`, "")
	h := New(cluster(), gpu).WithMonitoring(vm, am).Handler()
	var s stateView
	json.Unmarshal(do(h, "GET", "/api/state", nil).Body.Bytes(), &s)
	if s.GPUMetrics == nil || s.GPUMetrics.TempC != 52 || s.GPUMetrics.MemUsedBytes != 7.5e9 || s.GPUMetrics.PowerW != 88.5 {
		t.Fatalf("gpu metrics = %+v", s.GPUMetrics)
	}
	if s.AlertsFiring != 2 || s.Alerts[0].Name != "NodeDown" || s.Alerts[1].Summary != "Root disk under 15% free" {
		t.Fatalf("alerts = %+v", s.Alerts)
	}
	if s.MonitoringError != "" {
		t.Fatalf("error = %q", s.MonitoringError)
	}
}

func TestMissingGPUMetricIsAnErrorNotACrash(t *testing.T) {
	_, gpu := newFakeGPU(t)
	vm, am := fakeMonitoring(t, `[]`, "nvidia_smi_temperature_gpu")
	h := New(cluster(), gpu).WithMonitoring(vm, am).Handler()
	var s stateView
	w := do(h, "GET", "/api/state", nil)
	json.Unmarshal(w.Body.Bytes(), &s)
	if w.Code != 200 || s.GPUMetrics != nil || !strings.Contains(s.MonitoringError, "no data") || len(s.Profiles) != 1 {
		t.Fatalf("code=%d state=%s", w.Code, w.Body.String())
	}
	if s.Alerts == nil || s.AlertsFiring != 0 {
		t.Fatalf("alerts must still be an empty list: %+v", s.Alerts)
	}
}

func TestMonitoringDownLeavesThePanelWorking(t *testing.T) {
	_, gpu := newFakeGPU(t)
	h := New(cluster(), gpu).WithMonitoring("http://127.0.0.1:1", "http://127.0.0.1:1").Handler()
	var s stateView
	w := do(h, "GET", "/api/state", nil)
	json.Unmarshal(w.Body.Bytes(), &s)
	if w.Code != 200 || s.MonitoringError == "" || len(s.Consumers) != 2 {
		t.Fatalf("code=%d state=%s", w.Code, w.Body.String())
	}
}
