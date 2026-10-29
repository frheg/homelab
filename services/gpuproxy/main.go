// gpuproxy is the front door of one GPU consumer. It asks the arbiter (gpuctl) for
// the GPU before it forwards a request, so the consumer wakes up on demand.
// Design: docs/gpu-arbiter.md. One proxy per consumer, in the consumer's namespace.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	log.SetFlags(0)
	listen := flag.String("listen", ":8080", "listen address")
	consumer := flag.String("consumer", "", "gpuctl consumer name (required)")
	backend := flag.String("backend", "", "URL of the consumer's Service (required)")
	gpuctl := flag.String("gpuctl", "http://gpuctl.gpu-system", "gpuctl API")
	ttl := flag.Duration("ttl", 2*time.Minute, "lease length, renewed while requests are in flight")
	flag.Parse()
	if *consumer == "" || *backend == "" {
		log.Fatal("-consumer and -backend are required")
	}

	acq := httpAcquirer{base: *gpuctl, client: &http.Client{Timeout: 40 * time.Minute}}
	p, err := New(*consumer, *backend, acq, *ttl)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go p.Renew(ctx)

	srv := &http.Server{Addr: *listen, Handler: p}
	go func() {
		<-ctx.Done()
		sc, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sc)
	}()
	log.Printf("proxying %s to %s, gpuctl %s", *consumer, *backend, *gpuctl)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	os.Exit(0)
}
