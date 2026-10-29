# Migrating the old HomeLab

The old project (~/Documents/HomeLab, docker compose) now runs as pods.
Code stays in its own repo, this repo holds the manifests.

## Where everything went

| Old | Now | Data |
|---|---|---|
| searxng | platform/searxng | config in git |
| postgres | platform/postgres (shared, pgvector) | dump and restore, /srv/postgres |
| qdrant | platform/qdrant | /srv/qdrant |
| neo4j | platform/neo4j | /srv/neo4j |
| llama-swap | platform/llm (llm GPU, llm-cpu CPU) | /srv/models |
| api | apps/homelab, http://kratos:8080 | /srv/hf-cache, /srv/homelab-files |
| ingest | apps/homelab CronJob, suspended | /srv/wiki-dump, /srv/hf-cache |
| stable-diffusion | apps/homelab, GPU consumer | /srv/stable-diffusion |
| strata | apps/homelab, GPU consumer | /srv/strata |
| colibri | apps/homelab/optional, NOT deployed | no model data exists |

Images are the ones already built in Docker, copied to the registry as
`homelab/<name>`. The old Docker copies can be pruned once everything works.

## Steps, in order

1. Push homelab and dotfiles (you do the commits).
2. Move the data (instant, same disk). Run before `make kratos`:

       sudo ~/homelab/scripts/migrate-data

3. `make kratos`. Creates the /srv directories with the right owners, and k3s
   restarts once (it gets unlimited memlock for Strata).
4. Flux deploys the platform, then the apps. Watch: `flux get kustomizations`.
5. When Postgres is Ready, restore the old database and restart the API:

       ~/homelab/scripts/pg-restore-old
       kubectl -n homelab rollout restart deploy/api

6. Change the Neo4j password to the one in the secret:

       ~/homelab/scripts/neo4j-set-password

7. Check: `profctl status homelab`, open http://kratos:8080.

## Daily use

    profctl down homelab      # stops searxng, postgres, qdrant, neo4j and the api
    profctl up homelab        # starts them again, same replica counts
    gpuctl acquire strata     # GPU experiments are started by gpuctl, not profctl
    gpuctl reserve            # the GPU is yours; ingest also needs this

Ingest (uses the GPU, so reserve it first):

    gpuctl reserve
    kubectl -n homelab create job --from=cronjob/ingest ingest-$(date +%s)
    kubectl -n homelab logs -f job/ingest-<id>
    gpuctl unreserve

## Known differences from the old setup

- The API's Monitor page has no VRAM numbers: the API no longer claims the GPU,
  because the arbiter owns it. A metrics source for VRAM is on the roadmap.
- The API calls the llm gateway directly. If another GPU consumer holds the GPU,
  the gateway is stopped and chat fails until gpuproxy (wake on request) exists.
- Neo4j wants up to 20 GB of RAM. Do not run it next to a large model with a
  big RAM offload; use `profctl down homelab` before heavy GPU work.
- Passwords are new and strong, in SOPS secrets. The old defaults are gone.
- Colibri is not deployed, see apps/homelab/optional/colibri.yaml.

## Roll back

The old data is moved, not copied. To go back: stop the pods (`profctl down homelab`),
move the directories from /srv back to ~/Documents/HomeLab/data, and use the old
docker compose. The old Postgres data directory was never touched.
