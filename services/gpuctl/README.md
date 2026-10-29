# gpuctl

Decides which workload holds the GPU. One binary: daemon and CLI.
Design and rules: [docs/gpu-arbiter.md](../../docs/gpu-arbiter.md).
Language: Go, per [conventions](../../docs/conventions.md).

## Use

    gpuctl serve -default thesis        run the daemon in the cluster
    gpuctl status
    gpuctl acquire homelab -ttl 10m     blocks until the consumer is ready
    gpuctl release homelab
    gpuctl reserve -for 2h              take the GPU yourself, stops all consumers
    gpuctl unreserve

Set `GPUCTL_ADDR` (default `http://localhost:8080`). Add `-json` for JSON.
Exit codes: 0 ok, 1 error, 2 refused (busy or reserved).

## Consumers

A consumer is any Deployment with the label `gpu.kratos/consumer=<name>`.
Put the same label on the pod template, gpud waits for labelled pods to be gone.
A consumer can be several Deployments, even in different namespaces.
Names must be unique across the cluster.

## Notes

- State lives in the ConfigMap `gpu-state` in gpud's namespace.
- gpud needs a ClusterRole: list and patch deployments, list pods, and
  get, create, update on its own ConfigMap.
- The API has no auth. Reach it only through the tailnet and allow it by NetworkPolicy.
- gpud scales consumers only at a switch, a reservation or on start.
  It does not fight manual `kubectl scale` between those.

## Test

    go vet ./... && go test -race ./...

End-to-end, against the real cluster (touches only namespace `gpuctl-test`):

    kubectl apply -f e2e/consumers.yaml
    go test -tags e2e -run TestE2E -v -timeout 10m ./...
    kubectl delete -f e2e/consumers.yaml
