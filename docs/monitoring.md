# Monitoring

Metrics, alerts and dashboards. Nothing here needs a login beyond the UI basic-auth.

## Pieces

| What | Where | Does |
|---|---|---|
| node_exporter, nvidia_gpu_exporter | host (dotfiles_nix, modules/monitoring.nix) | CPU, RAM, disk, systemd units, GPU. Listen on the tailnet address only |
| VictoriaMetrics | platform/monitoring/victoria.yaml | scrapes everything, keeps 90 days in /srv/victoria |
| kube-state-metrics | kube-state-metrics.yaml | restarts, replicas, claims as metrics |
| vmalert + Alertmanager | alerting.yaml, rules/ | evaluate rules, group alerts |
| ntfy | ntfy.yaml | push to the phone, tailnet only, port 2586 |
| Grafana | grafana.yaml | dashboards at http://kratos/grafana/ |
| panel | platform/ui/panel.yaml | shows the GPU numbers and firing alerts next to the buttons |

What is scraped is in `scrape.yaml`. Rules are in `rules/*.yaml`, one file per area.
Dashboards are files in `dashboards/` (community dashboards, nothing is edited in the UI).

## Alerts

Alert, then Alertmanager, then ntfy topic `kratos-alerts`, then the phone.
Written to the topic by a write-only token. You log in as `v1s`, password in
`~/.config/homelab/ntfy-password` (also keep it in Pass).

Phone: install the ntfy app, add server `http://kratos:2586`, log in, subscribe to `kratos-alerts`.
The phone must be on the tailnet.

Backup alerts use `backup_last_success_timestamp_seconds{task=...}`, written by the backup jobs
into `/var/lib/node-textfile` (node_exporter textfile collector).

## Use

    http://kratos/grafana/                      dashboards
    kubectl -n monitoring port-forward svc/victoria 8428     then http://localhost:8428/vmui
    kubectl -n monitoring port-forward svc/vmalert 8880      then http://localhost:8880  (rules, firing)

Send a test alert:

    kubectl -n monitoring exec deploy/alertmanager -- amtool alert add TestAlert severity=warning \
      --annotation=summary="test from amtool" --alertmanager.url=http://localhost:9093

## Change

- New alert: add it to a file in `rules/`, run `scripts/check` (vmalert validates the rules).
- New scrape target: edit `scrape.yaml`. A changed file gets a new ConfigMap name, so the pod restarts.
- Another namespace may query the APIs when labelled `monitoring.kratos/access=true`.

## Pitfall

VictoriaMetrics counts the first sample of a new series as growth in `increase()` and `changes()`.
After a fresh start every restart counter looks like a burst of restarts and floods the phone.
For counters in alert rules use `x - (x offset 15m) > 0`: a series younger than the offset has
no value to subtract from, so it cannot fire.
