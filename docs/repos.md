# Repos

Status: decided, new repos not created yet. Change a rule here first, then the repos.

How the code is split, where it lives, and how it gets into the cluster.

## Repos

| Repo | Path | GitHub | Role |
|---|---|---|---|
| homelab | `~/homelab` | `frheg/homelab` | the cluster: manifests, Go infra services, docs. Flux watches `main` |
| athena | `~/athena` | `frheg/athena`, public | the chatbot, a general project anyone can run |
| thesis-lab | `~/thesis-lab` | `frheg/thesis-lab`, private | Lab, Enclave and Bench, version 2 |
| dotfiles_nix | `~/.config/dotfiles_nix` | `frheg/dotfiles_nix` | host config: k3s module, kubelet reservations, the `/srv` directories. A new data directory or a RAM change is made here, then `make kratos` |
| HomeLab_Old | `~/Documents/HomeLab_Old` | `frheg/HomeLab_Old` | old HomeLab source, the origin of today's Python images. Archive |
| masters-sandbox | `~/Documents/masters-sandbox` | `frheg/masters-sandbox` | old thesis lab (docker compose). Reference for thesis-lab |

Central repos live in `~/`, the rest in `~/Documents`.
Thesis text repos (`Masterthesis`, `Masteroppgave`, `MasterThesis_old`) and unrelated projects are not part of the cluster.

## Rules

1. `homelab` may depend on nix, NixOS and k3s. The host stays NixOS.
2. Every other repo is Docker-only: a `Dockerfile` and a `docker-compose.yml`, no flake. Anyone with plain Docker can run it.
3. App repos know nothing about the cluster:
   - config by environment variables only, no cluster service names in code
   - the LLM is any OpenAI-compatible URL
   - clients retry dependencies with backoff at startup and honor 503 with Retry-After (gpuproxy and the wake proxy rely on this)
   - `/health` says the process is alive, `/ready` checks the dependencies
   - a GPU is optional and enabled by a compose override file
4. App repos hold code only. Everything about kratos stays in `homelab`: manifests, SOPS secrets, tailnet addresses, the registry address, budgets and tiers (docs/scheduling.md).
5. No shared libraries between repos. They talk over HTTP: the llm gateway, the gpuctl status endpoint, the wake proxy.

## Public and private

- `athena` is public. Nothing about the network (registry address, tailnet hostnames), no secrets, no personal prompts or data. Start from a fresh history. The old history is not copied, files are, after a check for committed tokens and `.env` files.
- `thesis-lab` is private, but private is still hosted on GitHub. It never holds confidential data, or anything derived from it: samples, schemas, column names, fixtures. Tests use synthetic data. The data lives in `/srv`.
- `thesis-lab` keeps Lab, Enclave and Bench code in separate directories that map one to one to their namespaces. Only the Enclave directory touches confidential data.
- Add a `.gitignore` and a pre-commit check for data file types before the first commit.

## Build and deploy

1. Build the image from the repo with Docker.
2. Push it to the registry at `kratos:5000` (containerd maps that name to the node address in dotfiles_nix, so the tailnet address stays out of this public repo) and print the digest: `skopeo copy docker-daemon:<image> docker://<registry>/<name>:<tag>`, then `skopeo inspect` for the digest. A script in each repo does this. It needs no insecure-registry setting in the Docker daemon.
3. Put the digest in the manifest in `homelab` (`apps/athena`, `apps/thesis-lab`). repocheck requires digest pins.
4. Push `homelab` to `main`. Flux deploys.

Public images (athena) are published to `ghcr.io` by GitHub Actions. `homelab` then mirrors the published digest with `scripts/mirror` and never builds it.

Planned: `scripts/bump <service> <digest>` in `homelab` edits the manifest, so no copy and paste.

## Go infra services

gpuctl, gpuproxy, panel, profctl and repocheck are in `homelab` and built with nix (`scripts/push`). Only this cluster runs them, so rule 2 does not apply.
Converting them to Dockerfiles is optional and not planned. The flake also installs the host CLIs and the dev shell.

## Old images

`strata` and `stable-diffusion` were built once with Docker from HomeLab_Old and copied to the registry. They have no build path in `homelab`, and keep running as GPU consumers (`apps/homelab`). Where their source lives is open.
The old API and ingest were removed from the cluster on 2026-11-04. Their code is ported into `athena` (API, agents and ingest as separate images in one repo), not rebuilt from HomeLab_Old.

## Order

1. Create the empty repos on GitHub.
2. Scaffold `thesis-lab`: Dockerfiles, compose, the manifests in `apps/thesis-lab`.
3. Port `athena` from HomeLab_Old: startup retry, `/ready`, the rename, then the manifests in `apps/athena`.
4. Add `scripts/bump`.

## Open

- Whether `homelab` is public or private, which decides whether SOPS-encrypted files are acceptable to publish.
- Where ingest, strata and stable-diffusion live long term.
