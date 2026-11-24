---
id: TICKET-006
title: "Email OTP service: an email-client that syncs the mailboxes and serves each agent its own message via an API"
status: To-Do        # New | To-Do | WIP | Feedback | Done
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
mailboxes **as mail arrives** and keeps every message **available locally inside the
container**, so that **each agent serves itself** - it queries the local corpus
**however it wants** (by subject, body, sender, recipient, time, ...) through a
**well-defined, structured API** and takes the message it is looking for. Agents
never talk to Gmail themselves: if every agent kept polling IMAP we would hit
Gmail's connection limit fast; only the service connects, once and continuously,
and everything the agents do is a local read. Clear, scalable, and free of the
concurrency problem above.

## Decisions (locked 2026-11-24, operator)

- **Agent interface:** a thin CLI over a local **unix socket**. Agents use `mailotp query` / `fetch` / `alloc` against the `mailotp serve` daemon; the binary is just a thin client (it never opens IMAP). No HTTP endpoint or MCP tool for now.
- **Store:** **SQLite** on the data volume (`/app/data`), decoupled from the `artex` Postgres; survives a container recreate.
- **Deployment:** **sidecar** process inside the scopeweaver container, started and supervised by `start.sh` alongside the app (one IMAP talker per host).
- **Address strategy:** `+tag` subaddressing primary; the fallback for sites that reject `+` is **mailbox leasing** (exclusive lease of a whole mailbox to one agent for the sign-up window). Grow capacity by adding Gmail accounts; no catch-all domain for now.

## Concrete design (Fase 1 = MVP)

**Daemon + client.** `mailotp serve` runs the continuous sync and owns the store; it listens on a unix socket under the data volume (e.g. `/app/data/mailotp/mailotp.sock`). Everything the agents do is a **local read against the synced corpus** - no agent ever opens IMAP. The CLI (thin client) talks to the socket:
- `mailotp query [--mailbox NN] [--from s] [--to s] [--subject s] [--body s] [--since 15m] [--limit N] [--json]` -> **the primary command**: searches the local corpus however the agent wants and returns the matching messages (From/To/Date/Subject + decoded body, newest first). Pure local read; returns immediately.
- `mailotp fetch [same filters] [--wait 90]` -> the same search but **waits** up to `--wait` for a match to appear (for "I just triggered a sign-up, now wait for the code"). It is `query` + block-until-present.
- `mailotp alloc --mailbox 01 [--ttl 15m] [--no-plus]` -> for guaranteed isolation on a shared mailbox: returns a unique tagged address (`...test01+<tag>@gmail.com`) and a `handle`; the agent signs up with it, then `query`/`fetch --to +<tag>` reads exactly its own message. `--no-plus` leases the whole mailbox and returns the bare address (sites that reject `+`).
- `mailotp release --handle <h>` -> frees the allocation and any mailbox lease.
- `mailotp mailboxes`, and `mailotp fetch --mailbox NN` for back-compat.

**Attribution vs. free search (trade-off to keep in mind).** Free `query` by subject/body is flexible, but on a *shared* mailbox under concurrency two agents searching the same site's mail can match the same message. Free search is fine when that ambiguity does not matter (distinct senders/subjects, low concurrency); when a sign-up must be unambiguous, the agent uses `alloc` + its `+tag` so the message is keyed to it. The skill steers agents to `+tag` for sign-ups and leaves free `query` for everything else.

**Store (SQLite).** `messages(mailbox, uid, message_id, from_addr, to_addr, delivered_to, subject, date, body, ingested_at)` (unique `(mailbox, uid)` and `message_id` for idempotent ingest); `allocations(handle, mailbox, tag, address, task, intent, agent, state, ttl, created_at, matched_message_id)`; `leases(mailbox, handle, state, acquired_at, ttl)`. `query` filters map to indexed columns (from/to/subject/date/mailbox) plus a `--body` LIKE; add an FTS5 index if body search needs to scale.

**Poller.** One goroutine per mailbox; persistent IMAP with IDLE when available, else poll ~15s; tracks `UIDVALIDITY` + last UID; ingests **INBOX and Spam**; read-only (never marks read / deletes); matches each new message's `Delivered-To`/`To` tag to an open allocation.

**Supervision.** `start.sh` launches `mailotp serve` in the background and restarts it on exit; the daemon reads creds from the mounted `secrets.yaml` (so the raw App Password still never reaches the agent).

## Defaults I will set (not blocking; recorded here)

- Poll ~15s + IMAP IDLE; folders INBOX + Spam.
- Allocation/lease TTL 15 min; released on `release` or expiry.
- Claim: a matched message is bound to its allocation; `fetch` is idempotent/re-readable; one allocation resolves to one message (newest matching if several).
- Retention: prune messages after 7 days; keep allocations 7 days for audit.

## Scope (files likely to change)

- `cmd/mailotp/` - add `serve` (continuous sync + store + socket API), `query`, and
  `alloc`; make `fetch` a client (query + wait) of the service.
- `skills/email-otp/SKILL.md` - teach: query the local corpus by subject/body/from/
  etc; for a sign-up, alloc a `+tag` address and read your own message by it.
- `Dockerfile.hardened` / `start.sh` - run `mailotp serve` alongside the app.
- recon-server `ansible/roles/scopeweaver` - image tag bump + any service wiring.

## Acceptance (draft, to firm up with the design)

- [ ] Two agents signing up concurrently on the same site/mailbox each read only
      their own OTP (verified with a concurrency test).
- [ ] No per-agent IMAP connection: only the service talks to Gmail.
- [ ] Durable store survives a container recreate; re-reads are idempotent.
- [ ] agents query the local corpus by subject/body/from/to/time via the socket
      API (no IMAP from agents); `alloc` + `+tag` gives guaranteed isolation;
      `fetch --mailbox NN` still works.
- [ ] Reproducible deploy (image + Ansible), ticket/PR/gate flow.

## Relation

Builds on TICKET-005 (the `mailotp` helper, the `+tag`/`--to` interim win, the
mailboxes and secrets). See memory note artex-otp-imap-experiment.
