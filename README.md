# homelab

Cluster workloads for the k3s node on kratos. Plain Kubernetes manifests,
watched by Flux (once set up). Do not `kubectl apply` by hand except for
bootstrapping and tests. Secrets are SOPS-encrypted (age), never plain text.

The host side (NixOS, k3s install, sops-nix, GPU drivers) lives in the
dotfiles_nix repo: modules/k3s.nix and docs/k3s-kratos-plan.md.

- `platform/`  shared services other projects use (GPU plugin, later: wiki, LLM gateway)
- `tests/`     manual smoke tests, not reconciled by Flux
