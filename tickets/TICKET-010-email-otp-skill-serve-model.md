---
id: TICKET-010
title: "Update the email-otp skill to the serve/query/alloc model"
status: Feedback     # New | To-Do | WIP | Feedback | Done
area: agent, skills
priority: high
blocked_by: none
owner: psylinux
branch: v0.1.0-authfix
pr: none
source: operator request (one ticket per pending Fase 1b item, 2026-11-24)
---

## Goal

The `email-otp` skill steers agents to the daemon model: `query` the local corpus,
`fetch --handle` after an `alloc`, and `+tag` isolation on shared mailboxes, with
`fetch --mailbox NN` as the fallback. Done when the skill in the live image
describes the serve-model commands.

## Why / source

The skill currently teaches only the one-shot `fetch` (TICKET-005). It ships in
the live image and is loaded by agents, so it MUST be updated together with the
daemon (TICKET-007): updating it earlier would steer agents to `query`/`alloc`
commands whose daemon is not running yet and break in-flight sign-ups.

## Scope (files likely to change)

- `skills/email-otp/SKILL.md` - teach query/fetch --handle/alloc and the `--agent`
  attribution; keep the fallback note.
- image (the skill is copied in at build / deploy, same path as TICKET-007).

## Acceptance

- [ ] SKILL.md documents `query`, `fetch` (with `--wait` and `--handle`), `alloc`
      /`release` and the `+tag` isolation flow; mentions `--agent` for metrics.
- [ ] Keeps the `fetch --mailbox NN` / `--direct` fallback note.
- [ ] Ships in the same deploy as the daemon (TICKET-007), so agents never see
      guidance for a daemon that is not running.
- [ ] affected docs updated (this ticket, TICKET-006 remaining).
- [ ] PR opened; ticket set to `Feedback`.

## Notes / dependencies

Parent: TICKET-006. Blocked by (and ships with) TICKET-007. The client commands
it documents are in `psylinux/mailotp` (cross-repo reference).

## Log

- 2026-11-24: filed (New), split from TICKET-006.
- 2026-11-24: SKILL.md rewritten for the serve model (alloc + fetch --handle for
  per-signup isolation, query for search, fetch --mailbox NN direct fallback kept);
  shipped in image authfix.8 with TICKET-007 and verified baked into the live
  container. Status Feedback.
