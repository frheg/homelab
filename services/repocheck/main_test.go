package main

import (
	"strings"
	"testing"
)

const digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

func violations(t *testing.T, y string) string {
	t.Helper()
	got, err := run(strings.NewReader(y))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(got, "\n")
}

func TestPinnedImageWithLimitsIsClean(t *testing.T) {
	y := `
kind: Deployment
metadata: {name: a, namespace: n}
spec:
  template:
    spec:
      containers:
        - name: c
          image: r/x@` + digest + `
          resources: {requests: {cpu: 1m}, limits: {memory: 8Mi}}
`
	if got := violations(t, y); got != "" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestUnpinnedImageAndMissingLimits(t *testing.T) {
	y := `
kind: DaemonSet
metadata: {name: a, namespace: n}
spec:
  template:
    spec:
      initContainers:
        - name: i
          image: r/x:v1
      containers:
        - name: c
          image: r/x@` + digest + `
`
	got := violations(t, y)
	for _, want := range []string{"container i: image", "container i: needs resource", "container c: needs resource"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestConsumerRules(t *testing.T) {
	y := `
kind: Deployment
metadata: {name: llm, namespace: llm, labels: {gpu.kratos/consumer: llm}}
spec:
  replicas: 0
  template:
    metadata: {labels: {app: llm}}
    spec:
      containers:
        - name: c
          image: r/x@` + digest + `
          resources: {requests: {cpu: 1m}, limits: {memory: 8Mi}}
`
	got := violations(t, y)
	for _, want := range []string{"must not set replicas", "strategy Recreate", "pod template lacks"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGoodConsumer(t *testing.T) {
	y := `
kind: Deployment
metadata: {name: llm, namespace: llm, labels: {gpu.kratos/consumer: llm}}
spec:
  strategy: {type: Recreate}
  template:
    metadata: {labels: {gpu.kratos/consumer: llm}}
    spec:
      containers:
        - name: c
          image: r/x@` + digest + `
          resources: {requests: {cpu: 1m}, limits: {memory: 8Mi}}
`
	if got := violations(t, y); got != "" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestSecrets(t *testing.T) {
	plain := "kind: Secret\nmetadata: {name: s, namespace: n}\nstringData: {users: hunter2}\n"
	if got := violations(t, plain); !strings.Contains(got, "not SOPS-encrypted") {
		t.Fatalf("plain secret not flagged: %q", got)
	}
	enc := "kind: Secret\nmetadata: {name: s, namespace: n}\nstringData: {users: 'ENC[AES256_GCM,data:x]'}\n"
	if got := violations(t, enc); got != "" {
		t.Fatalf("encrypted secret flagged: %s", got)
	}
	token := "kind: Secret\nmetadata: {name: t, namespace: n}\ntype: kubernetes.io/service-account-token\n"
	if got := violations(t, token); got != "" {
		t.Fatalf("empty token secret flagged: %s", got)
	}
}

func TestPVCMustNotBePruned(t *testing.T) {
	bad := "kind: PersistentVolumeClaim\nmetadata: {name: d, namespace: n}\n"
	if got := violations(t, bad); !strings.Contains(got, "prune") {
		t.Fatalf("PVC not flagged: %q", got)
	}
	ok := "kind: PersistentVolumeClaim\nmetadata: {name: d, namespace: n, annotations: {kustomize.toolkit.fluxcd.io/prune: disabled}}\n"
	if got := violations(t, ok); got != "" {
		t.Fatalf("protected PVC flagged: %s", got)
	}
}

func TestHelmVersionMustBeExact(t *testing.T) {
	for v, bad := range map[string]bool{"0.46.0": false, "": true, "^0.46": true, "*": true, ">=1": true} {
		y := "kind: HelmRelease\nmetadata: {name: h, namespace: n}\nspec:\n  chart:\n    spec:\n      version: '" + v + "'\n"
		if got := violations(t, y) != ""; got != bad {
			t.Errorf("version %q: flagged=%v, want %v", v, got, bad)
		}
	}
}

func TestEmptyInputFails(t *testing.T) {
	if _, err := run(strings.NewReader("")); err == nil {
		t.Fatal("empty input must be an error, a check that sees nothing must not pass")
	}
}

func TestMultiDocAndEmptyDocs(t *testing.T) {
	y := "---\n---\nkind: Namespace\nmetadata: {name: n}\n---\nkind: Secret\nmetadata: {name: s}\nstringData: {k: v}\n"
	if got := violations(t, y); !strings.Contains(got, "Secret s") {
		t.Fatalf("got %q", got)
	}
}

func TestProfileNamespaceMustNotSetReplicas(t *testing.T) {
	ns := "kind: Namespace\nmetadata: {name: p, labels: {profile.kratos/homelab: 'true'}}\n---\n"
	dep := func(n, extra string) string {
		return ns + "kind: Deployment\nmetadata: {name: a, namespace: " + n + "}\nspec:\n" + extra +
			"  template:\n    spec:\n      containers:\n        - name: c\n          image: r/x@" + digest +
			"\n          resources: {requests: {cpu: 1m}, limits: {memory: 8Mi}}\n"
	}
	if got := violations(t, dep("p", "  replicas: 1\n")); !strings.Contains(got, "managed by profctl") {
		t.Errorf("replicas in a profile namespace not flagged: %q", got)
	}
	if got := violations(t, dep("p", "")); got != "" {
		t.Errorf("clean profile workload flagged: %s", got)
	}
	if got := violations(t, dep("other", "  replicas: 1\n")); got != "" {
		t.Errorf("replicas outside a profile namespace flagged: %s", got)
	}
}

func TestPlaceholderDigestIsNotPinned(t *testing.T) {
	y := "kind: Deployment\nmetadata: {name: a, namespace: n}\nspec:\n  template:\n    spec:\n      containers:\n        - name: c\n          image: r/x@sha256:@PLACEHOLDER@\n          resources: {requests: {cpu: 1m}, limits: {memory: 8Mi}}\n"
	if got := violations(t, y); !strings.Contains(got, "not pinned") {
		t.Fatalf("placeholder digest passed: %q", got)
	}
}
