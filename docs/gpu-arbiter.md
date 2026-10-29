# GPU arbiter (gpud)

Decides which workload holds the GPU. Nothing else.

## Why

Kubernetes lets two pods claim the same GPU. The card has 16 GB and no VRAM
isolation, so two big models crash each other. One program must say who holds it.

## Terms

- consumer: one or more Deployments (any namespace) that need the GPU. Label `gpu.kratos/consumer=<name>`, on the pod template too.
- holder: the one consumer scaled to 1. All others are scaled to 0.
- default: the consumer that holds when nobody else asks.
- lease: a time-limited hold by a non-default consumer.
- reservation: you take the GPU for yourself. No consumer runs.

## Rules

1. One holder at a time. Everything else is scaled to 0.
2. With no lease and no reservation, the default holds.
3. `acquire(name, ttl)` by a consumer:
   - reserved: refuse (423), say who and until when.
   - holder is the default: switch now.
   - holder has an active lease: wait up to a timeout, then refuse (409).
4. A switch scales the old holder to 0, waits until its pods are gone (VRAM
   freed), then scales the new one to 1 and waits until it is ready.
   If the old pods do not exit in time, stop and report. Never start the new one.
5. A lease ends on `release` or when `ttl` passes with no renewal.
6. After a lease ends, wait the idle grace, then return to the default.
7. A minimum hold time stops flapping. No switch away before it, except a reservation.
8. `reserve` beats everything. It scales all consumers to 0 at once.
   It ends on `unreserve` or its optional end time. Then rule 2 applies.

Priority: reservation, then active lease, then default.

## State

One ConfigMap `gpu-state`: holder, lease_until, reserved_by, reserved_until.
Config is flags on `gpuctl serve` (-default, -idle-grace, -min-hold, -switch-timeout), set in the Deployment manifest.
On start, gpud reads state and makes the cluster match it.
If gpud is down, nothing changes. Consumers stay as they are.
After a reboot everything starts from the default.

## Interface

One Go binary, `gpuctl`. `gpuctl serve` runs the daemon. Other commands call it.

    gpuctl status                     who holds, lease end, reservation
    gpuctl acquire <name> [--ttl 10m] blocks until ready
    gpuctl release <name>
    gpuctl reserve [--for 2h]
    gpuctl unreserve

HTTP on the same data: `GET /status`, `POST /acquire`, `POST /release`,
`POST /reserve`, `POST /unreserve`. The Homepage buttons use these.
Plain text output, `--json` for machines. Exit 0 ok, 1 error, 2 refused.
One log line per event on stderr.

## Proxy (gpuproxy)

The front door for HTTP consumers. Clients call a normal URL, not the arbiter.
It is a separate Go program built on `httputil.ReverseProxy`.

- Routes by host to a consumer, for example `llm.gpu` or `homelab.gpu`.
- On a request it calls `acquire` for that consumer, and holds the request until
  the consumer is ready, up to `switch_timeout`. After that it returns 503 with
  `Retry-After`.
- It counts requests in flight per consumer. While any are in flight it renews the
  lease. When the count drops to 0 it stops renewing, and gpud applies idle_grace.
- It streams responses without buffering (LLM output, SSE). No total timeout on a
  response, only on connect and on the wait for a switch.
- It keeps no state. A restart loses the counts, and the lease just expires.
- `acquire` must be idempotent: calling it for the current holder only renews.

`gpuctl acquire` stays for clients that are not HTTP, such as batch jobs.
A batch job holds the lease itself and calls `release` when done.

## Not in gpud

- Profiles (start and stop groups of services). Separate tool, `profctl`.
- Watching GPU use outside Kubernetes. Use `reserve` when you use the GPU by hand.

## Open questions

1. Values for min_hold, idle_grace, switch_timeout. Suggest 2m, 10m, 120s.
2. Should a profile be able to include GPU consumers, or only gpud touches them?
   Suggest only gpud.
3. Order of work: gpud first (small, testable alone), then gpuproxy, then profctl.
