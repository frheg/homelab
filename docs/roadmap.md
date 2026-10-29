# Roadmap

## Done

k3s, Flux, sops-nix, private registry, Headlamp, Homepage, GPU plugin,
LLM gateway (llama-swap), gpuctl (GPU arbiter).

## Missing for daily use

1. Boot: k3s does not start by itself. Enable it once the setup is trusted.
2. CLIs on PATH via home-manager: gpuctl, kubectl, k9s, flux.
3. gpuproxy: wake a GPU consumer on request. Design in gpu-arbiter.md.
4. profctl and Homepage buttons: start/stop groups of services, GPU reserve button.
5. Shared data services: Postgres (pgvector), Qdrant, Neo4j, SearXNG.
6. Embedding and rerank servers, as GPU or CPU consumers.
7. Backups: Postgres dumps, /srv data, registry, age keys. Test a restore.
8. Monitoring: metrics, logs, GPU use, simple alerts.
9. Security: drop initialPassword (sops), HTTPS, registry auth.
10. CI on the homelab repo: kustomize build, kubeconform, go test, gitleaks,
    check for unpinned images.
11. Service template: `scripts/new-service <name>` to scaffold code, image, manifests.
12. Known risk: generic-cdi-plugin churns devices and races after a k3s restart.
    Evaluate NVIDIA's official device plugin.

## Migrating HomeLab (old project, ~/Documents/HomeLab)

| Old service | New home | Notes |
|---|---|---|
| llama-swap | llm gateway (done) | uses the same models in /srv/models |
| postgres | platform Postgres | agent registry, sessions, run logs |
| qdrant | platform Qdrant | vector store |
| neo4j | platform Neo4j | graph, 60 GB of data |
| searxng | platform SearXNG | stateless, easy |
| ingest | Job (Wikipedia into Qdrant and Neo4j) | uses wiki_dump, 25 GB |
| api | project `homelab`, Go or keep Python | agent network, FastAPI |
| stable-diffusion | GPU consumer | 26 GB data |
| colibri, strata | project `homelab` | strata data is 86 GB, check what it is |

Data: move each directory to /srv/<service> when its service is built.
Prefer dump and restore over copying raw database files.
Order: SearXNG, Postgres, Qdrant, Neo4j, ingest, api, then the rest.

## New thesislab

Not a port. Needs a spec first: what it does, which platform services it
uses (llm, Postgres, embed, rerank, grobid), what it may reach on the network.
The old stack (masters-sandbox/lab) has Postgres, grobid, embed, rerank,
a worker, an API, a GPU scheduler and an egress proxy. Most of that now comes
from the platform.

## Order

1. CLIs on PATH, then usage docs stay true.
2. Backups and CI (so later work is safe).
3. Shared data services.
4. gpuproxy, profctl, UI buttons.
5. HomeLab migration, service by service.
6. New thesislab.
