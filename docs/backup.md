# Backup

Kratos has one disk, so every backup lives somewhere else.

## What is backed up, and where

| What | Where | How |
|---|---|---|
| Private keys (4 items) | Proton Pass, vault `kratos` | by hand, when a key changes |
| etcd snapshot + k3s token | Proton Drive, `kratos-backups/` | nightly, encrypted with age |
| Cluster manifests, code | GitHub (homelab, dotfiles_nix) | git push |
| Databases | not yet | dumps come with the shared data services |
| Bulk data (HomeLab/data) | not yet | needs a second disk or cloud target |

Not backed up, fetch again if lost: models, Wikipedia dump, container images
(rebuild with `scripts/push`), the HF cache.

## Keys in Proton Pass

Save these as items in a vault `kratos` (app or `pass-cli`):

| Item | Source file |
|---|---|
| age key, edit secrets | ~/.config/sops/age/keys.txt |
| age key, cluster | ~/.config/sops/age/homelab-cluster.txt |
| flux deploy key (private and public) | ~/.config/homelab/flux-deploy-key and .pub |
| UI password | ~/.config/homelab/ui-password |

Without the two age keys the encrypted secrets and the Drive backups cannot be read.
Also keep your Proton recovery kit somewhere that is not Proton.

## The nightly job

    03:30  etcd-export (root)             fresh snapshot + k3s token, encrypted, into /srv/backup/outbox
    03:45  kratos-backup-upload (user)    rclone copy to proton:kratos-backups, copy only

Upload never overwrites or deletes on Proton. Local copies older than 14 days
are removed after a good upload. The hourly mirror skips `kratos-backups`
and shares one lock with the upload, because only one rclone may use proton: at a time.

Run by hand:

    sudo systemctl start etcd-export
    systemctl --user start kratos-backup-upload
    journalctl --user -u kratos-backup-upload -f

List what is on Proton (take the lock, so you do not clash with the mirror):

    flock $XDG_RUNTIME_DIR/proton-rclone.lock rclone lsf proton:kratos-backups

## Restore

Path A, rebuild from git (preferred, the cluster is all in git):
1. Install NixOS, apply dotfiles_nix, start k3s.
2. Get the age keys and the flux deploy key from Proton Pass, put them back at the paths above.
3. Recreate the Flux secrets (`flux-system`, `sops-age`) and apply `clusters/kratos/flux-system/gotk-sync.yaml`.
4. Flux brings everything back. Volumes (registry, databases) start empty.

Path B, restore etcd (cluster state and volumes' claims, not the volume data):
1. Download the newest `etcd-DATE.tar.age` from Proton Drive.
2. `age -d -i ~/.config/sops/age/keys.txt etcd-DATE.tar.age | tar -x`
3. Put `token` at /var/lib/rancher/k3s/server/token, then follow the k3s docs for
   `--cluster-reset --cluster-reset-restore-path=<snapshot>`.

Verified 2026-10-29: the nightly bundle downloads from Proton, and decrypts with the
age key fetched from Proton Pass (contains the snapshot and the k3s token).
Not rehearsed yet: a full k3s restore from that snapshot (path B), or a rebuild (path A).

## Still to do

- Database dumps once Postgres, Qdrant and Neo4j run in the cluster.
- A target for bulk data before the HomeLab migration.
- A restore rehearsal.
