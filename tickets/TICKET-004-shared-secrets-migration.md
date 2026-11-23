---
id: TICKET-004
title: "Read secrets and the Discord webhook from the shared bug bounty secrets file"
status: Feedback
area: config
priority: high
blocked_by: none
owner: shared-secrets-004
branch: claude/shared-secrets-004-cd979a
pr: https://github.com/psylinux/scopeweaver/pull/NN
source: operator request; programs/_ops/SECRETS-STANDARD.md section 6 (consumer migration)
---

## Goal

ScopeWeaver reads its LLM API tokens and its Discord alert webhook from the one shared secrets file
(`~/.config/bugbounty/secrets.yaml`), resolving canonical names through the name registry
(`~/.config/bugbounty/secrets-registry.yaml`). An explicit environment variable stays an override; a
missing or empty value degrades gracefully to the feature being off, never a crash. ScopeWeaver stops
being the last consumer that keeps its own copy of the shared keys.

## Why / source

`programs/_ops/SECRETS-STANDARD.md` unifies ai-recon, MonRust, mcp-recon and ScopeWeaver onto one
flat-YAML secrets file and one name registry, both outside the archived `~/bugbounty` tree. Section 6
lists the per-consumer migration; ScopeWeaver (the ARTEX fork) was the one consumer still sourcing
secrets from `.env` and `ARTEX_*` environment variables only. The registry already lists ScopeWeaver's
Discord webhook as `DISCORD_WEBHOOK_ARTEX` and the LLM keys by canonical name, with the old names as
aliases.

## Scope (files likely to change)

- `sharedsecrets/sharedsecrets.go` (new): the loader (flat secrets file plus registry aliases,
  environment path overrides, graceful degradation, `${NAME}` reference expansion)
- `sharedsecrets/sharedsecrets_test.go` (new)
- `agent/provider.go`: `FromEnv` resolves `ANTHROPIC_API_TOKEN` / `OPENAI_API_TOKEN` from the shared
  file when `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` are unset
- `server/notifier.go`: `adapt` resolves `${NAME}` references in a channel config at send time, so the
  Discord webhook can be stored as `${DISCORD_WEBHOOK_ARTEX}` instead of the raw URL
- `.env.example`, `docs/shared-secrets.md`, `docs/README.md`
- `go.mod` / `go.sum`: `gopkg.in/yaml.v3` promoted from indirect to direct

Out of scope by design: `ARTEX_PG_DSN`, `ARTEX_CONFIG` and `ARTEX_SKILL_DIR` in `config/config.go` are
per-deployment (a database DSN and two local paths), not shared third-party secrets or shared tool
paths, so they stay as explicit environment or config-file settings and are not moved into the shared
file.

## Acceptance

- [x] A loader reads `~/.config/bugbounty/secrets.yaml` and resolves canonical names through the
  registry's `aliases`, with `BUGBOUNTY_SECRETS_DIR` / `_FILE` / `_REGISTRY` overrides and
  `~/.config/bugbounty/` as the default.
- [x] LLM keys resolve as: explicit `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` wins, else the shared file's
  `ANTHROPIC_API_TOKEN` / `OPENAI_API_TOKEN`, else the engine stays idle.
- [x] A notification channel URL set to `${DISCORD_WEBHOOK_ARTEX}` resolves from the shared file at send
  time; an unresolved reference is left visible, delivery never crashes.
- [x] A missing, unreadable or empty secrets file yields empty values and no error (feature off).
- [x] `go build ./...` green. `go test ./...` green except the `server` package's Postgres-backed
  integration tests, which require a live database that this environment lacks and fail identically on
  the untouched base commit (23 failures, all "Database configuration not found"). New and changed code
  is covered by `sharedsecrets` and `agent` tests, both green.
- [x] Affected docs updated: `docs/shared-secrets.md` (new), linked from `docs/README.md`, plus
  `.env.example`.
- [x] PR opened; ticket set to `Feedback`.

## Notes / dependencies

Cross-location change applied outside this repository (a PR here cannot carry it): the shared registry
`~/.config/bugbounty/secrets-registry.yaml` is updated to add `scopeweaver` to the `used_by` list of
`ANTHROPIC_API_TOKEN` (names only, no values). Every name ScopeWeaver reads
(`ANTHROPIC_API_TOKEN`, `OPENAI_API_TOKEN`, `DISCORD_WEBHOOK_ARTEX`) was already registered, so no new
name was added.

Deployment (owned by the coordinator, not this PR): the `scopeweaver-local` container must mount
`~/.config/bugbounty/` read-only and, if not mounting at the default path, set `BUGBOUNTY_SECRETS_DIR`.
The ARTEX Discord notification channel's URL is then set to `${DISCORD_WEBHOOK_ARTEX}`. See
`docs/shared-secrets.md`.

## Log

- 2026-11-23: filed and implemented; gate run; PR opened; set to Feedback.
