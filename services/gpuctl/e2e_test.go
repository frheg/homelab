//go:build e2e

package main

// Needs the dummy consumers: kubectl apply -f e2e/consumers.yaml
// Run: go test -tags e2e -run TestE2E -v -timeout 10m ./...
// Uses your kubeconfig. Only touches namespace gpuctl-test and the ConfigMap gpu-state-e2e.

import (
	"context"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	e2eNS = "gpuctl-test"
	e2eCM = "gpu-state-e2e"
)

var e2eCfg = Config{Default: "thesis", IdleGrace: 8 * time.Second, MinHold: 3 * time.Second, SwitchTimeout: 90 * time.Second}

func pods(t *testing.T, k *kube, consumer string) int {
	t.Helper()
	l, err := k.cs.CoreV1().Pods(e2eNS).List(context.Background(), metav1.ListOptions{LabelSelector: consumerLabel + "=" + consumer})
	if err != nil {
		t.Fatal(err)
	}
	return len(l.Items)
}

// only fails unless exactly want has pods. An empty want means no pods at all.
func only(t *testing.T, k *kube, want string) {
	t.Helper()
	for _, c := range []string{"thesis", "homelab", "bench", "stuck"} {
		n := pods(t, k, c)
		if c == want && n == 0 || c != want && n != 0 {
			t.Fatalf("consumer %s has %d pods, want only %q running", c, n, want)
		}
	}
}

func TestE2E(t *testing.T) {
	ctx := context.Background()
	k, err := newKube(e2eNS, e2eCM)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, c := range []string{"thesis", "homelab", "bench", "stuck"} {
			k.Scale(ctx, c, 0)
		}
		zero := int64(0)
		k.cs.CoreV1().Pods(e2eNS).DeleteCollection(ctx, metav1.DeleteOptions{GracePeriodSeconds: &zero}, metav1.ListOptions{})
		k.cs.CoreV1().ConfigMaps(e2eNS).Delete(ctx, e2eCM, metav1.DeleteOptions{})
	})
	if err := k.Save(ctx, State{}); err != nil {
		t.Fatal(err)
	}
	a := NewArbiter(e2eCfg, k, k)
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}

	t.Run("empty state starts the default", func(t *testing.T) {
		a.Tick(ctx)
		only(t, k, "thesis")
	})

	t.Run("acquire switches and stops the old one first", func(t *testing.T) {
		time.Sleep(e2eCfg.MinHold)
		if _, err := a.Acquire(ctx, "homelab", 20*time.Second); err != nil {
			t.Fatal(err)
		}
		only(t, k, "homelab")
	})

	t.Run("a long lease makes others busy", func(t *testing.T) {
		if _, err := a.Acquire(ctx, "homelab", 5*time.Minute); err != nil {
			t.Fatal(err)
		}
		_, err := a.Acquire(ctx, "bench", time.Minute)
		var busy *BusyError
		if !errors.As(err, &busy) {
			t.Fatalf("err = %v, want BusyError", err)
		}
		only(t, k, "homelab")
	})

	t.Run("reserve stops everything and refuses acquire", func(t *testing.T) {
		if err := a.Reserve(ctx, "v1s", 0); err != nil {
			t.Fatal(err)
		}
		only(t, k, "")
		_, err := a.Acquire(ctx, "homelab", time.Minute)
		var res *ReservedError
		if !errors.As(err, &res) {
			t.Fatalf("err = %v, want ReservedError", err)
		}
		if err := a.Unreserve(ctx); err != nil {
			t.Fatal(err)
		}
		only(t, k, "thesis")
	})

	t.Run("a lease ends and the default returns after the grace", func(t *testing.T) {
		time.Sleep(e2eCfg.MinHold)
		if _, err := a.Acquire(ctx, "homelab", 2*time.Second); err != nil {
			t.Fatal(err)
		}
		if err := a.Release(ctx, "homelab"); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(40 * time.Second)
		for a.Status().Holder != "thesis" {
			if time.Now().After(deadline) {
				t.Fatalf("default did not return, holder = %q", a.Status().Holder)
			}
			a.Tick(ctx)
			time.Sleep(time.Second)
		}
		only(t, k, "thesis")
	})

	t.Run("a restart keeps the holder", func(t *testing.T) {
		time.Sleep(e2eCfg.MinHold)
		if _, err := a.Acquire(ctx, "homelab", 5*time.Minute); err != nil {
			t.Fatal(err)
		}
		b := NewArbiter(e2eCfg, k, k)
		if err := b.Start(ctx); err != nil {
			t.Fatal(err)
		}
		if b.Status().Holder != "homelab" {
			t.Fatalf("holder = %q", b.Status().Holder)
		}
		only(t, k, "homelab")
	})

	t.Run("a stuck pod blocks the switch and nothing else starts", func(t *testing.T) {
		if err := k.Scale(ctx, "stuck", 1); err != nil {
			t.Fatal(err)
		}
		rctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if err := k.WaitReady(rctx, "stuck"); err != nil {
			t.Fatal(err)
		}
		// pretend stuck holds the GPU, as if started by hand
		if err := k.Save(ctx, State{Holder: "stuck", Since: time.Now().Add(-time.Hour)}); err != nil {
			t.Fatal(err)
		}
		short := e2eCfg
		short.SwitchTimeout = 15 * time.Second
		b := NewArbiter(short, k, k)
		b.state = State{Holder: "stuck", Since: time.Now().Add(-time.Hour)}
		if _, err := b.Acquire(ctx, "thesis", time.Minute); err == nil {
			t.Fatal("want an error while the stuck pod is terminating")
		}
		if n := pods(t, k, "thesis") + pods(t, k, "homelab") + pods(t, k, "bench"); n != 0 {
			t.Fatalf("%d pods started next to the stuck one", n)
		}
		if b.Status().Holder != "" {
			t.Fatalf("holder = %q, want none", b.Status().Holder)
		}
	})
}
