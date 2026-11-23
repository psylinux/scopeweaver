---
id: TICKET-NNN
title: "<short imperative, e.g. \"Watcher: add a single-pass mode\">"
status: New          # New | To-Do | WIP | Feedback | Done
area: <subsystem, e.g. watcher, tickets, ops, docs>
priority: high       # high | medium | low
blocked_by: none     # a decision id, a ticket id, or "none"
owner: none          # the agent's name, set on claim
branch: none         # the working branch, set on claim
pr: none             # the PR link, set at Feedback
source: <where this came from: code audit, ROLE.md section, operator request>
---

## Goal

<What "done" looks like, in 1-3 sentences. Behavior, not implementation.>

## Why / source

<The record, profile line or audit line this came from, with the citation.>

## Scope (files likely to change)

- <path>
- <path>

## Acceptance

- [ ] <specific behavioral criterion, one per line, each independently checkable>
- [ ] `go build ./...` and `go test ./...` green, stated in the PR (or the touched packages plus the suite, when the full run is impractical)
- [ ] affected docs updated, including `docs/` and any `ROLE.md` line the change makes stale
- [ ] PR opened; ticket set to `Feedback`

## Notes / dependencies

<Blocking decision, related tickets, gotchas. State any cross-repo path here, because a PR in this
repo cannot carry a file that lives in another repo.>

## Log

- <date>: filed (New)
