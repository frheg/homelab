# Roadmap

## Done

k3s (starts at boot), Flux, sops-nix, private registry, Headlamp, Homepage, GPU plugin,
LLM gateway (llama-swap), gpuctl (GPU arbiter), CLIs on PATH (kubectl, k9s, flux,
sops, gpuctl), backups of keys (Proton Pass) and etcd (Proton Drive), CI (scripts/check,
GitHub Actions). Reboot test passed on 2026-10-29: cluster came up by itself.

## Missing for daily use

3. (done) gpuproxy: wakes llm, strata and stable-diffusion on request. Verified on the cluster.
4. (done) Panel at /panel/: GPU reserve and switch, profile start and stop. Homepage links to it.
5. (done) Shared data services: Postgres, Qdrant, Neo4j, SearXNG. See migration-homelab.md.
6. Embedding and rerank servers, as GPU or CPU consumers.
7. Backups: /srv data and the registry. Path B (etcd restore) is rehearsed, path A is not.
8. (built, unverified) Monitoring: metrics, dashboards, push alerts, see monitoring.md. Not yet: logs, VRAM in the panel.
9. Security: HTTPS, registry auth. SSH: add keys (one per device, plus a backup key in Pass), turn on Tailscale SSH as a second path, then turn off sshd passwords (local console login keeps its password). Add tailnet ACLs before anyone else joins. Delete the stale `initialPassword = "nixos"` lines in hosts/atlas.nix and hosts/kratos.nix (dead config: passwd was changed on both hosts, but a reinstall would bring it back).
11. Service template: `scripts/new-service <name>` to scaffold code, image, manifests.
12. generic-cdi-plugin: the 30 s device churn and the count of 2 are by design (one spare
    device per used one), harmless because gpuctl is the exclusivity. A dead llm pod object
    after a boot is the pod-resources race (cosmetic).
    Evaluated NVIDIA's plugin (v0.20.1) on 2026-10-29, not adopted: it reads the GPU fine (NVML)
    but its own CDI spec has no library mounts on NixOS (0 vs 55 in the host spec), needs an
    FHS-style driver root, and its dev nodes come out under that root. Revisit when the NixOS
    toolkit module or the plugin supports a non-FHS driver root. Option later: NVIDIA DRA driver.

## Migrating HomeLab (old project, ~/Documents/HomeLab): manifests done, see migration-homelab.md

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

## LLM models: own config each, GPU and CPU, VRAM and RAM

Requirement (2026-10-29): every model has its own config and can run on the GPU,
on the CPU, or split across VRAM and system RAM.

Design:
- One file per model in platform/llm/models/<name>.yaml: the llama-server flags,
  `runtime: gpu | cpu`, ttl, description. No shared flags.
- A small Go tool merges models/*.yaml into two llama-swap configs, so the gateway
  files are generated and CI checks they are up to date.
- `llm` (GPU): gpuctl consumer, as today. Models use `-ngl` and `--n-cpu-moe` to
  split between VRAM and RAM. Stops when you reserve the GPU.
- `llm-cpu` (CPU only): the llama-swap `cpu` image, no GPU claim, not a consumer.
  Keeps working while the GPU is reserved or held by another consumer.
  Memory limit set so it cannot starve the host.
- Add a model: drop one file in models/, run the generator, commit.
- Models from the old HomeLab to bring over: laguna-xs-2.1, nemotron-3.5-lightning,
  qwen3.8-27b (Q4_K_M), plus the three already in the gateway.
