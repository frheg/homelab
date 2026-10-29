# Conventions

The rules that keep the homelab consistent as it grows. Change a rule here
first, then the code.

## Languages

- **Go is the default** for anything we write: controllers, APIs, CLIs, glue.
  One static binary, small image, native Kubernetes client, easy Nix build.
- **Python** where the ML/data ecosystem is the point (model serving, eval,
  data pipelines, notebooks-to-service).
- **TypeScript** for browser front-ends.
- **Zig / Rust / others** only with a stated reason (tiny footprint, hard
  performance need, deliberate learning). Say which in the service README.
- **Shell** only for short glue (under ~30 lines). Beyond that, use Go.
- Every service README states its language and why, if it is not Go.

## Every service

- Lives in `services/<name>/` (code) with manifests in `platform/` (shared) or
  `apps/<project>/` (project-specific), reconciled by Flux. No hand-applied
  workloads except bootstrap and tests.
- README: purpose, language, dependencies, who may call it.
- Health endpoint, resource requests AND limits, runs as non-root, read-only
  root filesystem where possible.
- Images pinned by digest (or exact version), never `:latest` or `:main`.
- Own namespace per project; default-deny NetworkPolicy plus an explicit
  allow-list of the platform services it may call.
- Ships OFF by default (replicas 0, or only in a non-default profile). Nothing
  autostarts unless chosen.
- Secrets are SOPS-encrypted `*.sops.yaml`. Never plain text, never in images.

## Safety

- No deleting of volumes, PVCs or namespaces without asking: the local-path
  StorageClass deletes data when a claim is deleted.
- GPU exclusivity comes from the arbiter, not from Kubernetes (the device
  plugin lets two pods claim the same GPU).

## Unix philosophy

- Small programs that do one thing. Compose them, do not grow one big one.
- Text in, text out. Plain output by default, `--json` for machines.
- Meaningful exit codes. Errors go to stderr, one line each.
- No hidden state. State lives in one named place (a ConfigMap or a file).
- The CLI and the web UI call the same API.

## Writing

- Docs and code comments are short and plain. Prefer one line.
- No em-dashes, no filler, no marketing words.
- Comment the why, not the what. Skip comments the code already says.

## GPU consumers

- Label the Deployment and its pod template `gpu.kratos/consumer=<name>`.
- Leave `replicas` out of the manifest. gpuctl owns it, and Flux would reset it.
- Use `strategy: Recreate` so a rollout never runs two pods on the GPU.
- Request the GPU as `nvidia.com/gpu-0: 1` in limits.
