# Services

Status: draft. One row per service: what it is, how it is scheduled, what it needs.
It merges `proposed-highlevel-architecture.md` with the manifests and `scheduling.md`.
Rows marked planned do not exist yet. The old API, ingest and Colibri were removed on 2026-11-04.

Budget: the memory limit in the manifest, used as a provisional peak (`scheduling.md`, build step 1 replaces it with a measured peak).
Idle: working set measured on 2026-11-04 with the GPU held by nothing heavy.
Sensitivity: public, internal, confidential. Confidential data never leaves its namespace and gets no request logging.

Pod total is about 26 Gi and 13.5 cores (`docs/monitoring.md`). Host: i5-14400F, 32 GB DDR5, RTX 5060 Ti 16 GB, 1.8T NVMe.

## Running now

| Service | Tier | Namespace | Needs | GPU | Budget | Idle | Wake | Sensitivity |
|---|---|---|---|---|---|---|---|---|
| k3s system, Flux, Traefik, CDI plugin | infra | kube-system, flux-system | - | - | small | ~150 Mi | always on | internal |
| gpuctl (arbiter) and gpuproxies | infra | gpuctl, per consumer | - | - | 64 Mi each | ~10 Mi | always on | internal |
| Monitoring (VictoriaMetrics, Grafana, Alertmanager, ntfy, kube-state-metrics) | infra | monitoring | - | - | ~2.2 Gi total | ~0.6 Gi | always on | internal |
| UIs (Homepage, Headlamp, panel) and registry | infra | platform-ui, registry | gpuctl (panel) | - | ~0.9 Gi total | ~0.2 Gi | always on | internal |
| Postgres 17 | default | postgres | - | - | 2 Gi | small | profile | internal |
| Qdrant | default | qdrant | - | - | 8 Gi | 79 Mi | profile | internal |
| Neo4j | default, soft dependency | neo4j | - | - | 6 Gi | 4.5 Gi | profile, pauses while Strata runs | internal |
| SearXNG | default | searxng | - | - | 512 Mi | 128 Mi | profile | public |
| llm (llama.cpp via llama-swap, GPU) | default consumer | llm | gpuctl | yes | 24 Gi | 355 Mi | gpuproxy | internal |
| llm-cpu (GPU plus CPU offload for big models) | default consumer | llm | gpuctl | yes | 26 Gi | - | gpuproxy | internal |
| llm-confidential (llama-swap, models marked `confidential`, keeps no captures or logs) | interactive consumer | llm-confidential | gpuctl | yes | 24 Gi | measure | gpuproxy | confidential |
| Makeup Search, synthetic (`/makeup-demo/`) | default | makeup-demo | llm | via llm | 2 Gi | measure | always on | public |
| Makeup Search, real Odfjell data (`/makeup/`) | default | makeup | llm-confidential | via llm-confidential | 8 Gi | measure | always on | confidential |
| Strata | interactive consumer, hard preempt | homelab | gpuctl | yes | 22 Gi | 0 | gpuproxy, 25 min start | internal |
| Stable Diffusion | interactive consumer, hard preempt | homelab | gpuctl | yes | 24 Gi | 0 | gpuproxy | internal |

## Planned

| Service | Tier | Namespace | Needs | GPU | Budget | Wake | Sensitivity |
|---|---|---|---|---|---|---|---|
| Athena API and agents (Deployment) | default | athena | Postgres, Qdrant, Neo4j, SearXNG, llm | - | measure (old API peaked near 6 Gi) | profile | internal |
| Athena ingest (Wikipedia embeddings, Job, same repo as the API) | background, resumable | athena | Qdrant, gpuctl | yes | measure (old: 16 Gi) | Job through the arbiter | public |
| Athena UI (always on, tiny) | infra | athena | wake proxy | - | ~64 Mi | always on | internal |
| Wake proxy (generic scale-from-zero front for non-GPU backends) | infra | platform | profctl | - | ~64 Mi | always on | internal |
| STT (Whisper-class) | default, kept warm | athena | gpuctl or CPU | decide | measure | warm | internal |
| Thesis Lab: workers, egress proxy, GROBID, public-data Postgres (pgvector, job queue) | background | thesis-lab | embed, rerank, llm | yes, via llm | measure | profile, windows | public |
| embed and rerank servers | default consumer | llm or thesis-lab | gpuctl | yes | measure | gpuproxy | public |
| Thesis Enclave: agent, chat, API | background | thesis-enclave | enclave Postgres, llm | yes, via llm | measure | profile, windows | confidential |
| Enclave Postgres (separate instance) | default | thesis-enclave | - | - | measure | with the enclave | confidential |
| Bench runner (Jobs created by a small controller, no docker.sock) | background, resumable | thesis-bench | gpuctl | yes | per job | Job | confidential |
| Other engines (FreeToken and later ones) | interactive consumer | per engine | gpuctl | yes | measure | gpuproxy | internal |

## Data flow and isolation

- Lab to Enclave is one way: a read-only path for what the Lab produced. NetworkPolicies allow no traffic back.
- Bench Jobs have no network. Only the Enclave controller creates them.
- The Enclave has its own Postgres, in its own namespace, default-deny egress, an allowlist for its own pods, and uses llm-confidential for confidential traffic.
- The shared llm gateways keep recent request and response bodies in memory (llama-swap `captureBuffer`, default 5 MB), readable at `/api/captures/<id>` by any namespace with `llm.kratos/access`. Tested on v251 (2026-11-12). Confidential prompts go to llm-confidential (`captureBuffer: 0`, `logToStdout: none`), which only namespaces labelled `llm-confidential.kratos/access=true` reach.
- Check the old Enclave dump's Postgres major version (pg18) before choosing the image. The platform Postgres is pg17.
- Neo4j and Qdrant stay shared and internal. Neo4j Community has one user database, so a second project gets its own instance.

## Waking

- GPU consumers: gpuproxy already returns 503 with Retry-After and wakes the consumer through the arbiter.
- Everything else behind an always-on UI: the wake proxy runs `profctl up`, shows a starting page, and retries until ready. Cold starts of 30 to 60 s or more are accepted.
- Every backend must retry its dependencies at startup. The old API did not (empty agent registry after a boot with DNS down), so Athena must.

## Athena rename

New namespace `athena`, profile label `profile.kratos/athena`, new image paths and Secret. The `/srv/homelab-*` directories and the `homelab` database can keep their names. Do it in one commit, with the move, after the Postgres, Qdrant and Neo4j profile labels are updated to match.

## Open

- Peak budgets for every row (measure, then replace the limits above).
- Whether STT runs on CPU or takes short GPU bursts.
- The Enclave Postgres image version.
- Makeup Search: the image is the masters-sandbox/product-v2 Dockerfile, pushed to kratos:5000 by hand (no nix build).
