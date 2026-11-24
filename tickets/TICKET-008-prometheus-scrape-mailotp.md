---
id: TICKET-008
title: "Wire the mailotp /metrics endpoint into Prometheus"
status: Done         # New | To-Do | WIP | Feedback | Done
area: ops, docs
priority: medium
blocked_by: none
owner: psylinux
branch: recon-server master + live services/monitoring
pr: none
source: operator request (one ticket per pending Fase 1b item, 2026-11-24)
---

## Goal

Prometheus scrapes the `mailotp serve` daemon and the `mailotp_*` series are
queryable. Done when the `mailotp` scrape job is live and its target shows up.

## Why / source

The daemon exposes `/metrics` on `:9469` (TICKET-006), but nothing scrapes it yet.
`monitoring-prometheus-1` is already on the `scopeweaver_default` network and
scrapes the scopeweaver container, so adding one job is all that is needed.

## Scope (files likely to change)

- `services/monitoring/prometheus/prometheus.yml` - add the `mailotp` job.
- recon-server Ansible (the role that renders the monitoring config), so the
  change is reproducible (cross-repo: `psylinux/recon-server`).

## Acceptance

- [ ] The `mailotp` job (from `psylinux/mailotp` `monitoring/prometheus-scrape.yml`,
      target `scopeweaver-local-scopeweaver-1:9469`, label `system: artex`) is added
      to `prometheus.yml`.
- [ ] Prometheus reloaded (`docker kill -s HUP monitoring-prometheus-1`); the
      target is `up` and `mailotp_messages_ingested_total` is queryable.
- [ ] The change is reproducible in the recon-server Ansible role.
- [ ] affected docs updated (monitoring README, this ticket).
- [ ] change applied/PR per the monitoring stack's flow; ticket set to `Feedback`.

## Notes / dependencies

Parent: TICKET-006. Blocked by TICKET-007 (the daemon must be live and exposing
`:9469` in the container). The ready scrape job is staged at `psylinux/mailotp`
`monitoring/prometheus-scrape.yml` (cross-repo).

## Log

- 2026-11-24: filed (New), split from TICKET-006.
- 2026-11-24: added the `mailotp` scrape job to prometheus.yml (live
  services/monitoring + the recon-server monitoring role `files/`), reloaded
  Prometheus (SIGHUP). Verified: target `job=mailotp` is up (scraping
  scopeweaver-local-scopeweaver-1:9469), and `mailotp_poller_up` (01=1, 02=1) and
  `mailotp_messages_stored` (01=20, 02=15) are queryable. Status Feedback.
- 2026-11-24: confirmed end-to-end via the Grafana dashboard (live data); operator marked Done.
- 2026-11-24: follow-up (authfix.9) - reads are now auto-attributed to the task id
  parsed from the agent working dir (/app/data/tasks/<id>/...), so
  `mailotp_reads_total` / `mailotp_messages_returned_total` carry a real `agent`
  label without the agent passing `--agent`. Verified: a query from
  /app/data/tasks/999/itest produced `agent="task999"`. Also removed the stale
  interim /app/data/bin copies.
