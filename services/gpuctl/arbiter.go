package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Cluster is what the arbiter needs from Kubernetes.
type Cluster interface {
	Consumers(ctx context.Context) ([]string, error)
	Scale(ctx context.Context, consumer string, replicas int32) error
	Replicas(ctx context.Context, consumer string) (int32, error)             // desired, summed over its Deployments
	StartTimeout(ctx context.Context, consumer string) (time.Duration, error) // from the gpu.kratos/start-timeout annotation, 0 if unset
	WaitGone(ctx context.Context, consumer string) error                      // no pods left, even terminating
	WaitReady(ctx context.Context, consumer string) error                     // all pods ready
}

type Config struct {
	Default       string
	IdleGrace     time.Duration // lease ended, wait this long before returning to default
	MinHold       time.Duration // no switch away before this, except a reservation
	SwitchTimeout time.Duration // limit for one switch, and for waiting on a busy GPU
}

type Arbiter struct {
	cfg Config
	cl  Cluster
	st  Store
	now func() time.Time

	mu    sync.Mutex // serializes changes, held during a switch
	smu   sync.RWMutex
	state State // guarded by smu, written only with mu held
}

func NewArbiter(cfg Config, cl Cluster, st Store) *Arbiter {
	return &Arbiter{cfg: cfg, cl: cl, st: st, now: time.Now}
}

// Start loads saved state and makes the cluster match it.
func (a *Arbiter) Start(ctx context.Context) error {
	s, err := a.st.Load(ctx)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.smu.Lock()
	a.state = s
	a.smu.Unlock()
	switch {
	case s.reserved(a.now()):
		return a.stopOthers(ctx, "")
	case s.Holder != "":
		if err := a.bring(ctx, s.Holder); err != nil {
			log.Printf("start: restore %s failed, will fall back to default: %v", s.Holder, err)
			return a.set(ctx, State{Since: a.now()})
		}
	}
	return nil
}

func (a *Arbiter) Status() State {
	a.smu.RLock()
	defer a.smu.RUnlock()
	return a.state
}

// Acquire makes name the holder and returns when it is ready.
// A repeat call by the holder renews its lease.
func (a *Arbiter) Acquire(ctx context.Context, name string, ttl time.Duration) (State, error) {
	if err := a.known(ctx, name); err != nil {
		return State{}, err
	}
	limit := a.now().Add(a.cfg.SwitchTimeout)
	for {
		s, until, err := a.tryAcquire(ctx, name, ttl)
		if err != nil || until.IsZero() {
			return s, err
		}
		if until.After(limit) {
			return s, &BusyError{Holder: s.Holder, Until: until}
		}
		select {
		case <-time.After(until.Sub(a.now())):
		case <-ctx.Done():
			return s, ctx.Err()
		}
	}
}

// tryAcquire returns a non-zero time when the caller must wait until then.
func (a *Arbiter) tryAcquire(ctx context.Context, name string, ttl time.Duration) (State, time.Time, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	s := a.Status()
	if s.ReservedBy != "" && !s.reserved(now) {
		s.ReservedBy, s.ReservedUntil = "", time.Time{}
		if err := a.set(ctx, s); err != nil {
			return s, time.Time{}, err
		}
	}
	if s.reserved(now) {
		return s, time.Time{}, &ReservedError{s.ReservedBy, s.ReservedUntil}
	}
	if s.Holder == name {
		s.LeaseUntil = a.leaseEnd(name, ttl)
		return s, time.Time{}, a.set(ctx, s)
	}
	if s.Holder != "" && s.Holder != a.cfg.Default && now.Before(s.LeaseUntil) {
		return s, s.LeaseUntil, nil
	}
	if s.Holder != "" && now.Before(s.Since.Add(a.cfg.MinHold)) {
		return s, s.Since.Add(a.cfg.MinHold), nil
	}
	err := a.switchTo(ctx, name, ttl)
	return a.Status(), time.Time{}, err
}

// Release ends the lease of name. The default returns after IdleGrace.
func (a *Arbiter) Release(ctx context.Context, name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.Status()
	if s.Holder != name || name == a.cfg.Default {
		return nil
	}
	s.LeaseUntil = a.now()
	return a.set(ctx, s)
}

// Reserve takes the GPU for a person and stops every consumer.
// A zero d means until Unreserve.
func (a *Arbiter) Reserve(ctx context.Context, by string, d time.Duration) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	ctx = context.WithoutCancel(ctx)
	s := State{Since: a.now(), ReservedBy: by}
	if d > 0 {
		s.ReservedUntil = a.now().Add(d)
	}
	if err := a.set(ctx, s); err != nil {
		return err
	}
	return a.stopOthers(ctx, "")
}

// Unreserve ends a reservation and hands the GPU to the default.
func (a *Arbiter) Unreserve(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Status().ReservedBy == "" {
		return nil
	}
	return a.switchTo(ctx, a.cfg.Default, 0)
}

// Tick handles expiry. Call it every few seconds.
func (a *Arbiter) Tick(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	s := a.Status()
	if s.ReservedBy != "" {
		if s.reserved(now) {
			a.enforce(ctx)
			return
		}
		log.Printf("reservation by %s ended", s.ReservedBy)
	}
	back := s.Holder == "" ||
		(s.Holder != a.cfg.Default &&
			!now.Before(s.LeaseUntil.Add(a.cfg.IdleGrace)) &&
			!now.Before(s.Since.Add(a.cfg.MinHold)))
	if !back {
		a.enforce(ctx)
		return
	}
	if err := a.switchTo(ctx, a.cfg.Default, 0); err != nil {
		log.Printf("return to %s failed: %v", a.cfg.Default, err)
	}
}

func (a *Arbiter) known(ctx context.Context, name string) error {
	all, err := a.cl.Consumers(ctx)
	if err != nil {
		return fmt.Errorf("list consumers: %w", err)
	}
	for _, c := range all {
		if c == name {
			return nil
		}
	}
	return &UnknownError{name}
}

func (a *Arbiter) leaseEnd(name string, ttl time.Duration) time.Time {
	if name == a.cfg.Default {
		return time.Time{}
	}
	return a.now().Add(ttl)
}

// switchTo changes the holder. On failure nobody holds and the next Tick retries.
// It does not stop when the caller goes away, a half-done switch is worse.
func (a *Arbiter) switchTo(ctx context.Context, name string, ttl time.Duration) error {
	ctx = context.WithoutCancel(ctx)
	log.Printf("switch to %s", name)
	if err := a.bring(ctx, name); err != nil {
		_ = a.set(ctx, State{Since: a.now()})
		return fmt.Errorf("switch to %s: %w", name, err)
	}
	return a.set(ctx, State{Holder: name, Since: a.now(), LeaseUntil: a.leaseEnd(name, ttl)})
}

// bring stops every other consumer, waits until their pods are gone, then
// starts name. The GPU is never shared, even when an old pod is stuck.
func (a *Arbiter) bring(ctx context.Context, name string) error {
	if err := a.stopOthers(ctx, name); err != nil {
		return err
	}
	if err := a.cl.Scale(ctx, name, 1); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	rctx, cancel := context.WithTimeout(ctx, a.readyTimeout(ctx, name))
	defer cancel()
	if err := a.cl.WaitReady(rctx, name); err != nil {
		_ = a.cl.Scale(context.WithoutCancel(ctx), name, 0)
		return fmt.Errorf("wait ready %s: %w", name, err)
	}
	return nil
}

// readyTimeout is how long a consumer may take to become ready: its own
// gpu.kratos/start-timeout if longer than the global limit, else the global limit.
// Engines that load tens of GB at start need minutes, llama-swap needs seconds.
func (a *Arbiter) readyTimeout(ctx context.Context, name string) time.Duration {
	d, err := a.cl.StartTimeout(ctx, name)
	if err != nil || d < a.cfg.SwitchTimeout {
		return a.cfg.SwitchTimeout
	}
	return d
}

func (a *Arbiter) stopOthers(ctx context.Context, keep string) error {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.SwitchTimeout)
	defer cancel()
	all, err := a.cl.Consumers(ctx)
	if err != nil {
		return fmt.Errorf("list consumers: %w", err)
	}
	var others []string
	for _, c := range all {
		if c != keep {
			if err := a.cl.Scale(ctx, c, 0); err != nil {
				return fmt.Errorf("stop %s: %w", c, err)
			}
			others = append(others, c)
		}
	}
	for _, c := range others {
		if err := a.cl.WaitGone(ctx, c); err != nil {
			return fmt.Errorf("wait gone %s: %w", c, err)
		}
	}
	return nil
}

func (a *Arbiter) set(ctx context.Context, s State) error {
	if err := a.st.Save(ctx, s); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	a.smu.Lock()
	a.state = s
	a.smu.Unlock()
	return nil
}

// enforce scales down every consumer that is not the holder. A new consumer
// starts with one replica and someone may scale by hand, so the one-holder rule
// is checked on every tick, not only at a switch. The holder is left alone.
func (a *Arbiter) enforce(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.SwitchTimeout)
	defer cancel()
	holder := a.Status().Holder
	all, err := a.cl.Consumers(ctx)
	if err != nil {
		log.Printf("enforce: list consumers: %v", err)
		return
	}
	for _, c := range all {
		if c == holder {
			continue
		}
		n, err := a.cl.Replicas(ctx, c)
		if err != nil {
			log.Printf("enforce: replicas of %s: %v", c, err)
			continue
		}
		if n > 0 {
			log.Printf("enforce: %s runs but holder is %q, scaling it to 0", c, holder)
			if err := a.cl.Scale(ctx, c, 0); err != nil {
				log.Printf("enforce: stop %s: %v", c, err)
			}
		}
	}
}
