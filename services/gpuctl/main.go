// gpuctl decides which workload holds the GPU. See docs/gpu-arbiter.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const usage = `usage: gpuctl <command> [flags]

  serve                     run the daemon
  status                    show who holds the GPU
  acquire <name> [-ttl 10m] take the GPU, wait until ready
  release <name>            end the lease
  reserve [-for 2h]         take the GPU for yourself
  unreserve                 give it back

common flags: -addr URL (env GPUCTL_ADDR), -json
exit codes: 0 ok, 1 error, 2 refused (busy or reserved)
`

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
	cmd, args := os.Args[1], os.Args[2:]
	if cmd == "serve" {
		if err := serve(args); err != nil {
			log.Fatal(err)
		}
		return
	}
	err := run(cmd, args)
	var r refused
	switch {
	case err == nil:
	case errors.As(err, &r):
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	default:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	addr := fs.String("addr", env("GPUCTL_ADDR", "http://localhost:8080"), "gpud address")
	asJSON := fs.Bool("json", false, "print JSON")
	ttl := fs.String("ttl", "", "lease length (acquire)")
	dur := fs.String("for", "", "reservation length (reserve), empty for until unreserve")
	by := fs.String("by", env("USER", "me"), "who reserves")
	name := ""
	if cmd == "acquire" || cmd == "release" {
		if len(args) == 0 {
			return fmt.Errorf("usage: gpuctl %s <name>", cmd)
		}
		name, args = args[0], args[1:]
	}
	fs.Parse(args)

	var raw []byte
	var err error
	switch cmd {
	case "status":
		raw, err = call(*addr, "GET", "/status", nil)
	case "acquire":
		raw, err = call(*addr, "POST", "/acquire", request{Name: name, TTL: *ttl})
	case "release":
		raw, err = call(*addr, "POST", "/release", request{Name: name})
	case "reserve":
		raw, err = call(*addr, "POST", "/reserve", request{By: *by, For: *dur})
	case "unreserve":
		raw, err = call(*addr, "POST", "/unreserve", nil)
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command: %s", cmd)
	}
	if err != nil {
		return err
	}
	printState(raw, *asJSON)
	return nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", ":8080", "listen address")
	def := fs.String("default", "", "default consumer (required)")
	grace := fs.Duration("idle-grace", 10*time.Minute, "wait after a lease ends before returning to default")
	hold := fs.Duration("min-hold", 2*time.Minute, "minimum time as holder before a switch")
	switchTO := fs.Duration("switch-timeout", 120*time.Second, "limit for one switch and for waiting on a busy GPU")
	ns := fs.String("namespace", env("POD_NAMESPACE", "gpu-system"), "namespace of the state ConfigMap")
	cm := fs.String("state", "gpu-state", "state ConfigMap name")
	fs.Parse(args)
	if *def == "" {
		return errors.New("-default is required")
	}

	kube, err := newKube(*ns, *cm)
	if err != nil {
		return err
	}
	a := NewArbiter(Config{Default: *def, IdleGrace: *grace, MinHold: *hold, SwitchTimeout: *switchTO}, kube, kube)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := a.Start(ctx); err != nil {
		return err
	}
	go func() {
		for t := time.NewTicker(5 * time.Second); ; {
			select {
			case <-t.C:
				a.Tick(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()

	srv := &http.Server{Addr: *listen, Handler: newMux(a)}
	go func() {
		<-ctx.Done()
		sc, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sc)
	}()
	log.Printf("listening on %s, default %s", *listen, *def)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
