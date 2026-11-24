---
id: TICKET-006
title: "Email OTP service: an email-client that syncs the mailboxes and serves each agent its own message via an API"
status: New          # New | To-Do | WIP | Feedback | Done
area: agent, skills, ops
priority: high
blocked_by: none
owner: psylinux
branch: none
pr: none
source: operator request (concurrency problem with the shared mailbox pool, raised 2026-11-24)
---

## Problem

The `mailotp` helper (TICKET-005) reads a shared pool of 2 Gmail mailboxes (01/02)
over IMAP. With many tasks, each running several agents/skills in parallel, the
shared pool breaks down:

- **Cross-talk / wrong OTP.** `fetch` returns the newest message (optionally
  `--from`/`--subject` filtered) received since ~run time. If agent A signs up on
  site1 and agent B signs up on site2 using the same mailbox at nearly the same
  moment, each `fetch` can return the other's OTP - or both read the same message.
  `--from`/`--subject` do not disambiguate two sign-ups on the **same** site/mailbox.
- **IMAP connection limits.** Today each agent shells the binary directly and each
  call opens its own Gmail IMAP TLS connection. Gmail caps simultaneous IMAP
  connections (~15); many parallel agents will exhaust that and start erroring.
- **Fragile "newest since now".** Time floor + `--grace`, Spam placement, and the
  "looks consumed" confusion are all symptoms of there being no durable, queryable
  record of what arrived.

## Operator's intent

The OTP side should behave like an **email client**: one component polls the
mailboxes on an interval, keeps a durable record of every message, and makes them
available so that **each agent serves itself** - it goes in, finds the message it
is looking for, and takes it - preferably through a **well-defined, structured API**.
Clear, scalable, and free of the concurrency problem above.

## Proposed direction (to refine together)

1. **Attribution by unique address (not by guessing).** Each sign-up uses a unique
   sub-address so its OTP is unambiguous. Gmail `+tag` subaddressing
   (`psylinux.hackerone.test01+<tag>@gmail.com`) delivers to the same inbox and
   keeps the `+tag` in the `To`/`Delivered-To` header; the agent fetches by its own
   tag. Zero new infra, scales to unlimited addresses from the 2 accounts.
   - Fallback for sites that reject `+`: lease a whole mailbox to one agent for the
     duration of a sign-up (serialises; limited by pool size), or add more Gmail
     accounts, or a catch-all domain for truly unlimited clean addresses.
   - Interim step already shipped under TICKET-005: a `--to` filter (matches `To`)
     plus the `+tag` convention documented in the skill.

2. **A mailbox-sync service (the email-client model).** Evolve `mailotp` into a
   client/server:
   - `mailotp serve`: one IMAP poller per account on an interval, writing into a
     durable store (SQLite on the data volume keeps it decoupled from the `artex`
     Postgres), indexed by recipient/`+tag`, `from`, `subject`, `date`,
     `message-id`, and body. Read-only on the mailbox; never deletes.
   - A small, well-defined local API (unix socket or `127.0.0.1`) the agents call
     instead of opening IMAP themselves - this removes the per-agent IMAP
     connections and the Gmail connection-limit risk.
   - The CLI becomes a thin client: `mailotp alloc --mailbox 01` returns a tagged
     address to sign up with; `mailotp fetch --handle <h>` (or `--to <addr>`)
     returns exactly that request's message from the store.
   - Keep `fetch --mailbox NN` working (served from the store) for back-compat.
   - Optional later: expose the same API as an ARTEX MCP / custom tool.

## Open design questions (the plan we will work on)

- API surface and transport: exact endpoints, request/response shapes, auth,
  unix-socket vs loopback HTTP vs MCP tool; how an agent discovers the service.
- Allocation/claim semantics: how `alloc` issues tags, TTLs, whether a message is
  "claimed" so two agents never take the same one, how retries/re-reads behave.
- Store: schema, retention/cleanup, whether SQLite or a dedicated Postgres schema,
  how it survives container recreates (data volume).
- Poller: interval, backoff, Gmail rate/connection budget, which folders (INBOX +
  Spam?), handling of multi-account pools and future accounts/catch-all domain.
- Lifecycle/deploy: where `serve` runs (sidecar process in the scopeweaver
  container via start.sh, or its own container), health, restart, and how the
  Ansible role manages it.
- Address strategy: `+tag` vs catch-all domain vs per-mailbox leasing, and how the
  skill instructs agents to choose and to fall back when `+` is rejected.

## Scope (files likely to change)

- `cmd/mailotp/` - add `serve` (poller + store + API) and `alloc`; make `fetch` a
  client of the service.
- `skills/email-otp/SKILL.md` - teach alloc -> sign up with the tagged address ->
  fetch by handle.
- `Dockerfile.hardened` / `start.sh` - run `mailotp serve` alongside the app.
- recon-server `ansible/roles/scopeweaver` - image tag bump + any service wiring.

## Acceptance (draft, to firm up with the design)

- [ ] Two agents signing up concurrently on the same site/mailbox each read only
      their own OTP (verified with a concurrency test).
- [ ] No per-agent IMAP connection: only the service talks to Gmail.
- [ ] Durable store survives a container recreate; re-reads are idempotent.
- [ ] `alloc` + `fetch --handle` documented in the skill; `fetch --mailbox NN`
      still works.
- [ ] Reproducible deploy (image + Ansible), ticket/PR/gate flow.

## Relation

Builds on TICKET-005 (the `mailotp` helper, the `+tag`/`--to` interim win, the
mailboxes and secrets). See memory note artex-otp-imap-experiment.
