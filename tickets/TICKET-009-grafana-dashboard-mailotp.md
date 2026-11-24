---
id: TICKET-009
title: "Provision the mailotp Grafana dashboard"
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

A `mailotp` Grafana dashboard is provisioned and shows the OTP pipeline: mail
ingested, messages stored, reads per agent, the verification queue, fetch-wait
latency, and poller health. Done when the dashboard appears in Grafana with live
data.

## Why / source

The operator wants visibility into how many emails arrived, how many were read
and by which agent, the message queue, and poller health (TICKET-006). The
dashboard is already built; this ticket provisions it.

## Scope (files likely to change)

- `services/monitoring/grafana/dashboards/mailotp.json` - the dashboard (from
  `psylinux/mailotp` `monitoring/grafana-dashboard.json`, uid `mailotp`, datasource
  `prometheus`).
- recon-server Ansible (the role that ships the Grafana dashboards), so it is
  reproducible (cross-repo: `psylinux/recon-server`).

## Acceptance

- [ ] The dashboard is dropped into `grafana/dashboards/`; the provider picks it
      up and it appears alongside `artex.json` (no Grafana restart needed).
- [ ] Panels render with live data (ingest rate, stored, reads per agent,
      allocations queue, fetch-wait quantiles, poller up, last-sync age, errors).
- [ ] The change is reproducible in the recon-server Ansible role.
- [ ] affected docs updated (monitoring README, this ticket).
- [ ] change applied/PR per the monitoring stack's flow; ticket set to `Feedback`.

## Notes / dependencies

Parent: TICKET-006. Blocked by TICKET-008 (the dashboard is empty without the
Prometheus data). The ready dashboard is staged at `psylinux/mailotp`
`monitoring/grafana-dashboard.json` (cross-repo).

## Log

- 2026-11-24: filed (New), split from TICKET-006.
- 2026-11-24: dropped the dashboard (uid `mailotp`) into grafana/dashboards (live
  services/monitoring + the recon-server monitoring role `files/`). Verified:
  Grafana loaded it (search API returns title "mailotp (email OTP service)"),
  rendering the TICKET-008 Prometheus data. Status Feedback.
- 2026-11-24: viewed live in the browser (panels render real data: pollers up 2,
  stored 35, sync age ~0-19s, 0 errors); operator marked Done.
