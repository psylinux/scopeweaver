---
id: TICKET-002
title: "Watcher: tell a Postgres outage apart from an empty table, without spamming"
status: To-Do
area: watcher
priority: medium
blocked_by: none
owner: none
branch: none
pr: none
source: code audit of the watcher plus measurement of 2026-11-22 on recon-server
---

## Goal

When the ARTEX database is unreachable the watcher reports the outage once, with the real reason, stops
pretending it has nothing to enqueue, and recovers by itself when the database comes back. When the
database is healthy and simply holds no findings, it reports nothing at all.

## Why / source

The current `watch.sh` runs the query as:

```bash
rows=$(docker exec "$PG" psql -U artex -d artex -At -F '|' -c "SELECT ..." 2>/dev/null)
if [ -z "$rows" ]; then
  printf '%s\tERROR\tARTEX query returned nothing\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$LOG"
  sleep "$INTERVAL"
  continue
fi
```

Four defects follow from those five lines, all of them read from the file, none of them yet observed in
production because the log currently holds 81 lines and 0 of them are `ERROR`:

1. The exit status of `docker exec` is never checked, and `2>/dev/null` throws away the one message that
   would say whether the container is down, the credentials are wrong, or psql failed.
2. An empty result and a failed query are treated as the same event, so a healthy database with zero
   findings would be logged as an error forever.
3. The error line repeats once per 600 s cycle for the whole duration of an outage, with no start, no
   end and no duration.
4. Nobody is told. The operator learns about a dead queue by reading the log.

Measured context on 2026-11-22: database container `scopeweaver-local-postgres-1`, table `findings`
holding 86 rows, watcher log at 81 lines, 0 `ERROR` lines, and 0 findings missing from both the log and
`LEVEL2-INDEX.tsv`. So the happy path is proven; what is unproven is every failure path.

Related measurement, and a trap for the fix: the Discord webhook item `discord-webhook` exists in the
macOS Keychain, but a POST to it on 2026-11-22 returned HTTP 404 `{"message": "Unknown Webhook",
"code": 10015}`. Any notification added by this ticket must treat a failed post as non-fatal and must
never block or crash the poll.

## Scope (files likely to change)

- `ops/triage-level2/watch.sh` (query execution, failure classification, outage bookkeeping)
- `ops/triage-level2/README.md` (how to read the log, what each line means)
- `/home/cowboy/bugbounty/programs/_ops/triage-level2/ROLE.md`, section 6 (document the new log
  vocabulary)

The script and its README are in this repository. The ROLE.md edit is applied on the server, in the
separate `bugbounty` repository, and is named in the PR body.

## Acceptance

- [ ] the query's exit status is checked, and the captured stderr is logged, instead of being discarded by `2>/dev/null`
- [ ] a healthy database with zero findings produces no `ERROR` line and no `DOWN` line
- [ ] an unreachable container produces exactly one `DOWN` line at the start, carrying the failing command and its stderr
- [ ] a recovered container produces exactly one `RECOVERED` line carrying the outage duration in seconds
- [ ] a 30 minute outage produces two lines, not thirty: proven by running `--once` in a loop against a stopped container
- [ ] no `NEW` line is ever written from a failed query, proven with the container stopped
- [ ] the notification, when the webhook item is present and valid, is sent once on `DOWN` and once on `RECOVERED`, and a failed post (HTTP 404, timeout, absent item) is logged as a single warning and never aborts the poll
- [ ] `--once` mode from TICKET-001 honours all of the above
- [ ] `go build ./...` and `go test ./...` green, stated in the PR (they are unaffected by an ops change, so state that explicitly)
- [ ] affected docs updated, including `ROLE.md` section 6
- [ ] PR opened; ticket set to `Feedback`

## Notes / dependencies

Pairs with TICKET-001, which introduces the `--once` mode this ticket's tests lean on. Land 001 first if
both are worked together.

Do not "fix" this by making the watcher exit on failure and relying on systemd to restart it: that
loses the outage duration and turns a five minute database restart into a restart loop. The watcher must
stay alive and stay quiet.

## Log

- 2026-11-23: filed (New)
- 2026-11-23: the watcher moved to `ops/triage-level2/watch.sh` in this repository, which cleared the
  location question; released as To-Do
