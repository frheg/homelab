# Daily use

## Start and stop

k3s does not start at boot yet.

    sudo systemctl start k3s     # about 30 s until the node is Ready
    sudo systemctl stop k3s

## Look at the cluster

    kubectl get pods -A          # needs ~/.kube/config
    http://kratos/               # Homepage, user v1s, password in ~/.config/homelab/ui-password
    http://kratos/headlamp/      # needs the trailing slash, then a token

Tokens (read-only, or use headlamp-admin-token to change things):

    kubectl -n platform-ui get secret headlamp-viewer-token -o jsonpath='{.data.token}' | base64 -d

## The GPU

One consumer holds it at a time. The default is `llm` (the shared model gateway).

    export GPUCTL_ADDR=http://10.43.0.50
    gpuctl status
    gpuctl reserve -for 2h       # all consumers stop, the GPU is yours
    gpuctl unreserve             # the default comes back
    gpuctl acquire <name>        # for batch jobs, blocks until ready

Until gpuctl is on PATH: `nix run ~/homelab#gpuctl -- status`.
Exit codes: 0 ok, 1 error, 2 refused (busy or reserved).

## Use the LLM

From a pod: http://llm.llm/v1/chat/completions (OpenAI API).
The pod's namespace must be labelled `llm.kratos/access=true`.
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
