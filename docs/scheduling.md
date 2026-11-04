# Scheduling

Status: design, not built. Change a rule here first, then the code.

How kratos decides what runs when resources are short. One node, one GPU, about 26 Gi of pod RAM.
The GPU arbiter (docs/gpu-arbiter.md) stays the base. This adds tiers, demand priority and a RAM budget.

## Why

- The GPU is exclusive, but RAM is the tighter limit. Strata alone takes 22 Gi.
- Pairwise rules (`yields-to`) do not scale to more services.
- Autonomous work (thesis lab, training) must use idle capacity and step aside when you need the machine.
- Limits cannot be summed to check a plan: llm 24 Gi, llm-cpu 26 Gi, strata 22 Gi, qdrant 8 Gi already exceed the pod total.

## Terms

- consumer: a workload that holds the GPU (unchanged).
- demand: a request for a consumer, with an initiator and a priority.
- interactive demand: you asked, through the UI, the API, `gpuctl acquire` or a reservation.
- batch demand: an autonomous worker asked.
- budget: the declared peak RAM of a workload, separate from its limit.
- resumable: the work survives being stopped (queue item, checkpoint).

Priority belongs to the demand, not the Deployment. `llm` serves you and the lab, and they differ.

## Tiers

| Tier | Meaning | Today |
|---|---|---|
| infra | always on, never preempted, small | k3s system pods, Flux, gpuctl, cdi plugin, gpuproxies, registry, monitoring, Homepage, Headlamp, panel |
| default | the resting state, runs when nothing else is asked | llm, llm-cpu, Postgres, Qdrant, SearXNG, HomeLab API, Neo4j |
| interactive | woken or started by you, wins the GPU | Strata, Stable Diffusion, anything you acquire or reserve |
| background | resumable work in whatever is free | thesis lab workers, ingest, bench runs, training, nightly jobs |

Order: infra, interactive, default, background.

## Rules

1. Infra is never stopped by the scheduler.
2. Interactive demand beats default and background. The holder is switched as in the arbiter rules.
3. Batch demand never beats interactive demand, also on the same consumer (the lab and you both call `llm`).
4. Background runs in free capacity and in a guaranteed window (default 01:00 to 07:00, configurable), so it cannot starve.
5. A focus mode (panel button or CLI, for N hours) marks you as active. `gpuctl reserve` stays the hard form.
6. Traffic through gpuproxy extends the active window. It never starts one by itself for batch callers.
7. Background work resumes after a cooldown (10 min after the last interactive activity, the idle grace) and only if it fits the budget.
8. Resume order is by priority, then by age in the queue.

## Pausing

Per workload: `preempt: soft|hard` and a grace period.

- soft: the worker checks "interactive active until T" before taking its next item. Lab LLM batch work is soft.
- hard: the arbiter scales it to 0 when the grace has passed. Strata and Stable Diffusion are hard.
- A switch waits until the old pods are gone before the new one starts (unchanged).
- Min hold of 2 min stops flapping (unchanged).
- Databases (Postgres, Neo4j) are stopped only on purpose, with a long termination grace.
- Do not preempt what is nearly done. A long-start consumer (strata) gets a longer grace.

## Training and other GPU jobs

- You start it: interactive. Treated like a reservation for its duration.
- An agent starts it: background.
- Background GPU work is admitted only if it declares `resumable: true` and a checkpoint interval. Otherwise it waits for a window you grant.
- Limits for autonomous work: maximum runtime per lease, a daily GPU-hour budget, a kill switch.
- gpuctl needs a Job path: acquire holds a lease for the Job, release or lease end stops it. Today ingest bypasses the arbiter with a manual reserve.

## RAM budget

- Every workload declares a peak RAM budget. It is not the limit.
- CI sums the budgets for every state that can happen: infra, plus the defaults, plus one GPU holder, plus what yields.
- The sum must stay under the pod total (about 26 Gi, see docs/monitoring.md).
- Rollout: the check warns until every workload has a budget, then it blocks.
- Measure budgets from VictoriaMetrics (peak working set), do not guess.
- llm-cpu holds up to 25 Gi with a model loaded, so it conflicts with strata. It must yield or load only on demand.

CPU needs no arbiter. Background pods get low CPU requests, so the kernel shares CPU in favor of the rest under contention.

## Declaring a service

Manifest labels, checked by repocheck:

- `sched.kratos/tier: infra|default|background` on every workload. Interactive consumers are marked by the existing consumer label.
- `sched.kratos/ram-budget: <size>`.
- Background and GPU batch work: `sched.kratos/resumable: "true"` and `sched.kratos/checkpoint: <duration>`.
- `sched.kratos/preempt: soft|hard` and a termination grace.
- Dependencies: `sched.kratos/needs: <service>` (hard) or `sched.kratos/yields-to: <consumer>` (soft, replaces the current `gpu.kratos/yields-to` once built).
- Background services ship scaled to 0 or suspended.

A `scripts/new-service` scaffold (roadmap item 11) should add these.

## Dependencies

- A profile is the unit of start and stop (`profctl`). Its needs are started with it.
- Kubernetes does not order start. Apps retry their own connections at startup. The HomeLab API did not, and came up with an empty agent registry after a reboot.
- A soft dependency can pause while another consumer runs (Neo4j under strata). It returns afterwards.

## Visibility

The panel answers "why is X not running": the active window, the holder, the queue, the budget in use. Every decision is one log line in gpuctl.

## Inference engines

- `llm` runs llama.cpp (through llama-swap) and is the default. Strata is a test-bed, and other engines may follow.
- The arbiter does not care about the engine. Any Deployment with the consumer label and its own gpuproxy is a consumer.
  An engine with an OpenAI-compatible API can sit behind its own proxy like Strata does.
- A RAM upgrade or a new engine changes budgets, not the design. CI reads the pod total from the node's allocatable
  memory (kubeReserved and systemReserved in dotfiles_nix), it is not hardcoded.

## Not doing

- VRAM sharing. One GPU holder at a time, by decision.
- Kueue, Volcano and similar batch queues. Too heavy for one node.
- Guests. Note for later: lending compute over the tailnet would mean a pod with sshd, a namespace quota, default-deny egress and a granted GPU window, and tailnet ACLs first. Nothing is scaffolded for it.

## Build order

1. Measure peak RAM per workload and write the budgets.
2. repocheck: the tier and budget labels, warn only.
3. Demand tagging in gpuproxy and gpuctl, and the focus mode.
4. Background class: soft and hard pause, cooldown, windows.
5. Job path for training, with the resumable rules and the budgets.
6. Block CI on the RAM budget check.
7. Panel: reasons and queue.

## Open

- How the lab reads "interactive active until T" (gpuctl status field or a small endpoint).
- Whether llm-cpu moves to a lease or gets a model-level load rule.
- The focus mode default length.
