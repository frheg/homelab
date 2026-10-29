package profile

import (
	"bytes"
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func i32(n int32) *int32 { return &n }

func ns(name string, profiles ...string) *corev1.Namespace {
	l := map[string]string{}
	for _, p := range profiles {
		l[profilePrefix+p] = "true"
	}
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: l}}
}

func dep(ns, name string, replicas *int32, labels map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Spec:       appsv1.DeploymentSpec{Replicas: replicas},
	}
}

func got(t *testing.T, cs *fake.Clientset, ns, name string) int32 {
	t.Helper()
	d, err := cs.AppsV1().Deployments(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return replicasOf(d.Spec.Replicas)
}

func TestDownThenUpRestoresReplicas(t *testing.T) {
	cs := fake.NewSimpleClientset(
		ns("a", "demo"), ns("b", "demo"), ns("c", "other"),
		dep("a", "web", i32(3), nil),
		dep("b", "db", nil, nil), // nil means 1
		dep("c", "keep", i32(2), nil),
	)
	ctx := context.Background()
	var out bytes.Buffer
	if err := Down(ctx, cs, "demo", &out); err != nil {
		t.Fatal(err)
	}
	if got(t, cs, "a", "web") != 0 || got(t, cs, "b", "db") != 0 {
		t.Fatalf("not stopped:\n%s", out.String())
	}
	if got(t, cs, "c", "keep") != 2 {
		t.Fatal("a workload in another profile was touched")
	}
	if err := Up(ctx, cs, "demo", &out); err != nil {
		t.Fatal(err)
	}
	if got(t, cs, "a", "web") != 3 || got(t, cs, "b", "db") != 1 {
		t.Fatalf("replicas not restored: web=%d db=%d", got(t, cs, "a", "web"), got(t, cs, "b", "db"))
	}
	d, _ := cs.AppsV1().Deployments("a").Get(ctx, "web", metav1.GetOptions{})
	if _, ok := d.Annotations[savedReplicas]; ok {
		t.Fatal("saved annotation should be cleared after up")
	}
}

func TestGPUConsumersAreLeftAlone(t *testing.T) {
	cs := fake.NewSimpleClientset(
		ns("a", "demo"),
		dep("a", "web", i32(1), nil),
		dep("a", "sd", i32(1), map[string]string{consumerLabel: "sd"}),
	)
	var out bytes.Buffer
	if err := Down(context.Background(), cs, "demo", &out); err != nil {
		t.Fatal(err)
	}
	if got(t, cs, "a", "web") != 0 {
		t.Fatal("web should stop")
	}
	if got(t, cs, "a", "sd") != 1 {
		t.Fatal("gpu consumer must be left to gpuctl")
	}
}

func TestGPUProxiesStayUp(t *testing.T) {
	cs := fake.NewSimpleClientset(
		ns("a", "demo"),
		dep("a", "web", i32(1), nil),
		dep("a", "gpuproxy-sd", i32(1), map[string]string{proxyLabel: "sd"}),
	)
	var out bytes.Buffer
	if err := Down(context.Background(), cs, "demo", &out); err != nil {
		t.Fatal(err)
	}
	if got(t, cs, "a", "web") != 0 {
		t.Fatal("web should stop")
	}
	if got(t, cs, "a", "gpuproxy-sd") != 1 {
		t.Fatal("gpu proxy must stay up, it is what wakes the consumer")
	}
}

func TestDownTwiceKeepsTheFirstSavedCount(t *testing.T) {
	cs := fake.NewSimpleClientset(ns("a", "demo"), dep("a", "web", i32(3), nil))
	ctx := context.Background()
	var out bytes.Buffer
	Down(ctx, cs, "demo", &out)
	Down(ctx, cs, "demo", &out)
	Up(ctx, cs, "demo", &out)
	if got(t, cs, "a", "web") != 3 {
		t.Fatalf("web = %d, want 3", got(t, cs, "a", "web"))
	}
}

func TestUnknownProfileIsAnError(t *testing.T) {
	cs := fake.NewSimpleClientset(ns("a", "demo"))
	var out bytes.Buffer
	err := Down(context.Background(), cs, "nope", &out)
	if err == nil || !strings.Contains(err.Error(), "no namespace") {
		t.Fatalf("err = %v", err)
	}
}

func TestListShowsAllProfiles(t *testing.T) {
	cs := fake.NewSimpleClientset(ns("a", "x", "y"), ns("b", "x"))
	ps, err := Profiles(context.Background(), cs)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps["x"]) != 2 || len(ps["y"]) != 1 {
		t.Fatalf("profiles = %v", ps)
	}
}

func TestItemsListWorkloadsAndWhyTheyAreLeftAlone(t *testing.T) {
	cs := fake.NewSimpleClientset(
		ns("a", "demo"),
		dep("a", "web", i32(2), nil),
		dep("a", "sd", i32(0), map[string]string{consumerLabel: "sd"}),
		dep("a", "px", i32(1), map[string]string{proxyLabel: "sd"}),
	)
	items, err := Items(context.Background(), cs, "demo")
	if err != nil {
		t.Fatal(err)
	}
	notes := map[string]string{}
	for _, it := range items {
		notes[it.Name] = it.Note
	}
	if len(items) != 3 || notes["web"] != "" || notes["sd"] == "" || notes["px"] == "" {
		t.Fatalf("items = %+v", items)
	}
	if _, err := Items(context.Background(), cs, "missing"); err == nil {
		t.Fatal("unknown profile should be an error")
	}
}
