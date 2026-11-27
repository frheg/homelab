| Subsurface Evidence (`/subsurface-evidence/`): UI, CPU API and a GPU worker for fine maps; runs ~/Documents/hackathon-force/subsurface-evidence from disk | default (worker: interactive consumer) | subsurface-evidence | gpuctl (worker, via gpuproxy), internet (worker first start) | yes (worker) | 3 Gi backend, 8 Gi worker | 0 until a fine map is asked, then ~0.5 GB | profile subsurface-evidence, gpuproxy | internal |
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
| llm-cpu (small CPU-only models under 10B: bge-m3 embeddings) | default | llm | - | - | 8 Gi | 1 | model loads on demand, unloads after 30 min | internal |
| llm-confidential (llama-swap, models marked `confidential`, keeps no captures or logs) | interactive consumer | llm-confidential | gpuctl | yes | 24 Gi | measure | gpuproxy | confidential |
| Makeup Search, synthetic (`/makeup-demo/`) | default | makeup-demo | llm | via llm | 2 Gi | measure | always on | public |
| Makeup Search, real Odfjell data (`/makeup/`) | default | makeup | llm-confidential | via llm-confidential | 8 Gi | measure | always on | confidential |
| Hackathon app (`/hackathon/`, FORCE VibeCode 2026-11-19..20): runs ~/Documents/hackathon-force/app from disk | default | hackathon | llm, llm-cpu, Postgres, Qdrant, SearXNG, internet | via llm | 4 Gi | measure | profile hackathon | internal |
| Strata | interactive consumer, hard preempt | homelab | gpuctl | yes | 22 Gi | 0 | gpuproxy, 25 min start | internal |
| Stable Diffusion | interactive consumer, hard preempt | homelab | gpuctl | yes | 24 Gi | 0 | gpuproxy | internal |

## Planned

| Service | Tier | Namespace | Needs | GPU | Budget | Wake | Sensitivity |
|---|---|---|---|---|---|---|---|
| Athena: core, ui and nine small Go services (`apps/athena`, not yet enabled) | default | athena | Postgres, SearXNG, llm, gpuctl, VictoriaMetrics | via llm | about 0.3 Gi for all ten (Go, 32 to 64 Mi each) | always on (small); the model sleeps | internal |
| Athena embeddings (an embed server, for memory search by meaning) | default consumer | llm | gpuctl or CPU | decide | measure | gpuproxy | internal |
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

## Athena

Namespace `athena`, manifests in `apps/athena`, images from athena's `scripts/publish`. It uses three databases on the shared Postgres (`athena`, `athena_memory`, `athena_notes`). Memory runs on word search until an embedding server exists. The cluster servers read only the namespaces bound in `athena.yaml` (no confidential ones) and may restart only the targets in `cluster-actions.yaml`.

## Open

- Peak budgets for every row (measure, then replace the limits above).
- Whether STT runs on CPU or takes short GPU bursts.
- The Enclave Postgres image version.
- Makeup Search: the image is the master-thesis repo's tasks/ai-assisted-search-makeup-graphs/task-baseline Dockerfile, pushed with `make push REGISTRY=kratos:5000` (no nix build).
