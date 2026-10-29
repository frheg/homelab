# Daily use

## Start and stop

k3s starts at boot. Workloads start by themselves only for the GPU default (llm).

    sudo systemctl start k3s     # only if you stopped it, about 30 s until Ready
    sudo systemctl stop k3s

## Look at the cluster

    kubectl get pods -A          # needs ~/.kube/config
    http://kratos/               # Homepage, user v1s, password in ~/.config/homelab/ui-password
    http://kratos/headlamp/      # needs the trailing slash, then a token

Tokens (read-only, or use headlamp-admin-token to change things):

    kubectl -n platform-ui get secret headlamp-viewer-token -o jsonpath='{.data.token}' | base64 -d

## Start and stop groups

    profctl list                  profiles and their namespaces
    profctl status homelab
    profctl down homelab          stop searxng, postgres, qdrant, neo4j, api
    profctl up homelab            start them again

A profile is the set of namespaces labelled `profile.kratos/<name>=true`.
GPU consumers and their proxies are not touched by profctl. gpuctl owns the consumers, the proxies must stay up to wake them.

## The GPU

One consumer holds it at a time. The default is `llm` (the shared model gateway).

    export GPUCTL_ADDR=http://10.43.0.50
    gpuctl status
    gpuctl reserve -for 2h       # all consumers stop, the GPU is yours
    gpuctl unreserve             # the default comes back
    gpuctl acquire <name>        # for batch jobs, blocks until ready

`gpuctl` builds from ~/homelab on first use. After a change to the shell env, log in again.
Exit codes: 0 ok, 1 error, 2 refused (busy or reserved).

## Use the LLM

From a pod: http://llm.llm/v1/chat/completions (GPU models) or
http://llm-cpu.llm/v1/chat/completions (CPU-only models, always available).
The pod namespace must be labelled `llm.kratos/access=true`.
From your shell: `kubectl -n llm port-forward deploy/llm 18080:8080`.
Models are files in /srv/models, listed in platform/llm/llm.yaml.

## Change the cluster

Everything is in git: ~/homelab. Edit, commit, push. Flux applies it within
5 minutes, or run `flux reconcile kustomization platform --with-source`.
Do not `kubectl apply` by hand.

## Add your own service (Go)

1. Code in services/<name>/, add the name to `goServices` in flake.nix.
2. `scripts/push <name>` builds the image, pushes it, prints the digest.
3. Manifests in platform/<name>/ (shared) with the image pinned by that digest.
4. GPU consumer: see conventions.md (label, no replicas, Recreate).
5. Commit and push.

## Develop against the cluster

    kubectl -n <ns> port-forward svc/<name> 8080:80
    go run ./services/<name>     # your code, talking to cluster services

## Check before you push

    nix develop -c scripts/check     # same as CI, about a minute
    git config core.hooksPath .githooks   # once, runs it on every push

## Secrets

    sops platform/<dir>/<name>.sops.yaml     # needs ~/.config/sops/age/keys.txt

Never commit a plain secret. Back up the age keys, they cannot be recreated.

## When something is wrong

    kubectl -n <ns> describe pod <pod>
    kubectl -n <ns> logs deploy/<name>
    flux get all -A
    gpuctl status

A reservation or switch that hangs: check for dead pods with the consumer
label, gpuctl ignores finished pods but waits for running ones.

## Add or change an LLM model

One file per model in platform/llm/models/<name>.yaml: `runtime: gpu` or `cpu`,
`ttl`, and the llama-server `cmd`. VRAM/RAM split: `-ngl` and `--n-cpu-moe`.
CPU only: `-ngl 0` (CI rejects a cpu model without it). Then regenerate and commit:

    (cd ~/homelab/services/llmconf && go run . -dir ../../platform/llm)

## Shared services

Each is reached by name once the project namespace has the access label:

    searxng.searxng   (searxng.kratos/access=true)
    postgres.postgres (postgres.kratos/access=true)   new database: scripts/pg-newdb <name>
    qdrant.qdrant     (qdrant.kratos/access=true)
    neo4j.neo4j       (neo4j.kratos/access=true)
    llm.llm, llm-cpu.llm (llm.kratos/access=true)

## Wake on request

Calling `http://llm.llm`, `http://strata:8080` or `http://stable-diffusion:1234` takes
the GPU for that consumer by itself (gpuproxy asks gpuctl), then forwards the request.
If you reserved the GPU or another consumer has an active lease, the call returns 503
with `Retry-After: 30` and the reason in the body.
