package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type fakeCluster struct {
	replicas map[string]int32
	ops      []string
	stuck    map[string]bool // WaitGone fails
	notReady map[string]bool // WaitReady fails
}

func newFake(names ...string) *fakeCluster {
	f := &fakeCluster{replicas: map[string]int32{}, stuck: map[string]bool{}, notReady: map[string]bool{}}
	for _, n := range names {
		f.replicas[n] = 0
	}
	return f
}

func (f *fakeCluster) Consumers(context.Context) ([]string, error) {
	var out []string
	for _, n := range []string{"thesis", "homelab", "bench"} {
		if _, ok := f.replicas[n]; ok {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *fakeCluster) Scale(_ context.Context, c string, n int32) error {
	f.replicas[c] = n
	f.ops = append(f.ops, fmt.Sprintf("scale %s %d", c, n))
	return nil
}

func (f *fakeCluster) WaitGone(_ context.Context, c string) error {
	if f.stuck[c] {
		return errors.New("pods still terminating")
	}
	return nil
}

func (f *fakeCluster) WaitReady(_ context.Context, c string) error {
	if f.notReady[c] {
		return errors.New("not ready")
	}
	return nil
}

type memStore struct{ s State }

func (m *memStore) Load(context.Context) (State, error)   { return m.s, nil }
func (m *memStore) Save(_ context.Context, s State) error { m.s = s; return nil }

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

var cfg = Config{Default: "thesis", IdleGrace: 10 * time.Minute, MinHold: 2 * time.Minute, SwitchTimeout: 120 * time.Second}

func setup(names ...string) (*Arbiter, *fakeCluster, *memStore, *clock) {
	f := newFake(names...)
	st := &memStore{}
	ck := &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	a := NewArbiter(cfg, f, st)
	a.now = ck.now
	return a, f, st, ck
}

// running returns the consumers with replicas > 0.
func running(f *fakeCluster) []string {
	var out []string
	for n, r := range f.replicas {
		if r > 0 {
			out = append(out, n)
		}
	}
	return out
}

func wantOnly(t *testing.T, f *fakeCluster, name string) {
	t.Helper()
	got := running(f)
	if len(got) != 1 || got[0] != name {
		t.Fatalf("running = %v, want only [%s]", got, name)
	}
}

func TestEmptyStateStartsDefault(t *testing.T) {
	a, f, _, _ := setup("thesis", "homelab")
	a.Tick(context.Background())
	wantOnly(t, f, "thesis")
	if a.Status().Holder != "thesis" {
		t.Fatalf("holder = %q", a.Status().Holder)
	}
}

func TestAcquireSwitchesFromDefault(t *testing.T) {
	a, f, _, ck := setup("thesis", "homelab")
	ctx := context.Background()
	a.Tick(ctx)
	ck.add(5 * time.Minute)
	s, err := a.Acquire(ctx, "homelab", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	wantOnly(t, f, "homelab")
	if s.Holder != "homelab" || !s.LeaseUntil.Equal(ck.now().Add(10*time.Minute)) {
		t.Fatalf("state = %+v", s)
	}
}

func TestOldStoppedBeforeNewStarts(t *testing.T) {
	a, f, _, ck := setup("thesis", "homelab")
	ctx := context.Background()
	a.Tick(ctx)
	ck.add(5 * time.Minute)
	f.ops = nil
	if _, err := a.Acquire(ctx, "homelab", time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(f.ops) != 2 || f.ops[0] != "scale thesis 0" || f.ops[1] != "scale homelab 1" {
		t.Fatalf("ops = %v", f.ops)
	}
}

func TestMinHoldMakesCallerWait(t *testing.T) {
	a, _, _, ck := setup("thesis", "homelab")
	ctx := context.Background()
	a.Tick(ctx)
	ck.add(30 * time.Second)
	_, until, err := a.tryAcquire(ctx, "homelab", time.Minute)
	if err != nil || !until.Equal(ck.now().Add(90*time.Second)) {
		t.Fatalf("until = %v, err = %v", until, err)
	}
}

func TestBusyWhenOtherLeaseOutlastsLimit(t *testing.T) {
	a, f, _, ck := setup("thesis", "homelab", "bench")
	ctx := context.Background()
	a.Tick(ctx)
	ck.add(5 * time.Minute)
	if _, err := a.Acquire(ctx, "homelab", 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	ck.add(5 * time.Minute)
	_, err := a.Acquire(ctx, "bench", time.Minute)
	var busy *BusyError
	if !errors.As(err, &busy) || busy.Holder != "homelab" {
		t.Fatalf("err = %v", err)
	}
	wantOnly(t, f, "homelab")
}

func TestHolderRenewsLease(t *testing.T) {
	a, _, _, ck := setup("thesis", "homelab")
	ctx := context.Background()
	a.Tick(ctx)
	ck.add(5 * time.Minute)
	a.Acquire(ctx, "homelab", 10*time.Minute)
	ck.add(8 * time.Minute)
	s, err := a.Acquire(ctx, "homelab", 10*time.Minute)
	if err != nil || !s.LeaseUntil.Equal(ck.now().Add(10*time.Minute)) {
		t.Fatalf("state = %+v, err = %v", s, err)
	}
}

func TestReturnsToDefaultAfterLeaseAndGrace(t *testing.T) {
	a, f, _, ck := setup("thesis", "homelab")
	ctx := context.Background()
	a.Tick(ctx)
	ck.add(5 * time.Minute)
	a.Acquire(ctx, "homelab", 10*time.Minute)
	ck.add(10*time.Minute + 9*time.Minute) // lease over, grace not yet
	a.Tick(ctx)
	wantOnly(t, f, "homelab")
	ck.add(2 * time.Minute) // grace over
	a.Tick(ctx)
	wantOnly(t, f, "thesis")
}

func TestReleaseStartsGraceNotImmediateSwitch(t *testing.T) {
	a, f, _, ck := setup("thesis", "homelab")
	ctx := context.Background()
	a.Tick(ctx)
	ck.add(5 * time.Minute)
	a.Acquire(ctx, "homelab", 30*time.Minute)
	a.Release(ctx, "homelab")
	ck.add(3 * time.Minute)
	a.Tick(ctx)
	wantOnly(t, f, "homelab")
	ck.add(8 * time.Minute)
	a.Tick(ctx)
	wantOnly(t, f, "thesis")
}

func TestReserveStopsAllAndRefusesAcquire(t *testing.T) {
	a, f, _, ck := setup("thesis", "homelab")
	ctx := context.Background()
	a.Tick(ctx)
	if err := a.Reserve(ctx, "v1s", 0); err != nil {
		t.Fatal(err)
	}
	if got := running(f); len(got) != 0 {
		t.Fatalf("running = %v", got)
	}
	ck.add(time.Hour)
	a.Tick(ctx)
	if len(running(f)) != 0 {
		t.Fatal("tick must not start anything while reserved")
	}
	_, err := a.Acquire(ctx, "homelab", time.Minute)
	var res *ReservedError
	if !errors.As(err, &res) || res.By != "v1s" {
		t.Fatalf("err = %v", err)
	}
}

func TestReserveBeatsActiveLease(t *testing.T) {
	a, f, _, ck := setup("thesis", "homelab")
	ctx := context.Background()
	a.Tick(ctx)
	ck.add(5 * time.Minute)
	a.Acquire(ctx, "homelab", time.Hour)
	if err := a.Reserve(ctx, "v1s", 0); err != nil {
		t.Fatal(err)
	}
	if len(running(f)) != 0 {
		t.Fatalf("running = %v", running(f))
	}
}

func TestUnreserveReturnsDefault(t *testing.T) {
	a, f, _, _ := setup("thesis", "homelab")
	ctx := context.Background()
	a.Reserve(ctx, "v1s", 0)
	if err := a.Unreserve(ctx); err != nil {
		t.Fatal(err)
	}
	wantOnly(t, f, "thesis")
	if a.Status().ReservedBy != "" {
		t.Fatal("still reserved")
	}
}

func TestUnreserveWithoutReservationChangesNothing(t *testing.T) {
	a, f, _, ck := setup("thesis", "homelab")
	ctx := context.Background()
	a.Tick(ctx)
	ck.add(5 * time.Minute)
	a.Acquire(ctx, "homelab", time.Hour)
	if err := a.Unreserve(ctx); err != nil {
		t.Fatal(err)
	}
	wantOnly(t, f, "homelab")
}

func TestReservationExpires(t *testing.T) {
	a, f, _, ck := setup("thesis", "homelab")
	ctx := context.Background()
	a.Reserve(ctx, "v1s", time.Hour)
	ck.add(61 * time.Minute)
	a.Tick(ctx)
	wantOnly(t, f, "thesis")
}

func TestStuckOldPodNeverSharesGPU(t *testing.T) {
	a, f, _, ck := setup("thesis", "homelab")
	ctx := context.Background()
	a.Tick(ctx)
	ck.add(5 * time.Minute)
	f.stuck["thesis"] = true
	_, err := a.Acquire(ctx, "homelab", time.Minute)
	if err == nil {
		t.Fatal("want error")
	}
	if f.replicas["homelab"] != 0 {
		t.Fatal("homelab must not start while thesis pods remain")
	}
	if a.Status().Holder != "" {
		t.Fatalf("holder = %q, want none", a.Status().Holder)
	}
}

func TestNotReadyIsScaledBackDown(t *testing.T) {
	a, f, _, ck := setup("thesis", "homelab")
	ctx := context.Background()
	a.Tick(ctx)
	ck.add(5 * time.Minute)
	f.notReady["homelab"] = true
	if _, err := a.Acquire(ctx, "homelab", time.Minute); err == nil {
		t.Fatal("want error")
	}
	if f.replicas["homelab"] != 0 {
		t.Fatal("failed consumer must be scaled to 0")
	}
}

func TestFailedSwitchRetriesToDefaultOnTick(t *testing.T) {
	a, f, _, ck := setup("thesis", "homelab")
	ctx := context.Background()
	a.Tick(ctx)
	ck.add(5 * time.Minute)
	f.notReady["homelab"] = true
	a.Acquire(ctx, "homelab", time.Minute)
	a.Tick(ctx)
	wantOnly(t, f, "thesis")
}

func TestUnknownConsumer(t *testing.T) {
	a, _, _, _ := setup("thesis")
	_, err := a.Acquire(context.Background(), "nope", time.Minute)
	var u *UnknownError
	if !errors.As(err, &u) {
		t.Fatalf("err = %v", err)
	}
}

func TestStartRestoresHolder(t *testing.T) {
	a, f, st, ck := setup("thesis", "homelab")
	st.s = State{Holder: "homelab", Since: ck.now(), LeaseUntil: ck.now().Add(time.Hour)}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantOnly(t, f, "homelab")
}

func TestStartKeepsReservation(t *testing.T) {
	a, f, st, ck := setup("thesis", "homelab")
	f.replicas["thesis"] = 1
	st.s = State{Since: ck.now(), ReservedBy: "v1s"}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(running(f)) != 0 {
		t.Fatalf("running = %v", running(f))
	}
}

// blockingCluster never finishes WaitGone until its context ends.
type blockingCluster struct{ *fakeCluster }

func (b blockingCluster) WaitGone(ctx context.Context, c string) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestReserveGivesUpAfterSwitchTimeout(t *testing.T) {
	f := newFake("thesis", "homelab")
	c := cfg
	c.SwitchTimeout = 50 * time.Millisecond
	a := NewArbiter(c, blockingCluster{f}, &memStore{})
	done := make(chan error, 1)
	go func() { done <- a.Reserve(context.Background(), "v1s", 0) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want an error when pods do not go away")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Reserve did not return, it must obey SwitchTimeout")
	}
	if a.Status().ReservedBy != "v1s" {
		t.Fatal("reservation should still be recorded")
	}
	if _, err := a.Acquire(context.Background(), "homelab", time.Minute); err == nil {
		t.Fatal("acquire must be refused while reserved")
	}
}
