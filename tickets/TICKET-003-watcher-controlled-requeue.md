---
id: TICKET-003
title: "Watcher: re-queue a decided finding only when its state actually changed"
status: To-Do
area: watcher
priority: medium
blocked_by: none
owner: none
branch: none
pr: none
source: code audit of the watcher (known_ids) plus the reopening requirement in ROLE.md section 7.1; measurement of 2026-11-22
---

## Goal

A finding that already has a Level 2 verdict is queued again only when something material about it
changed, and then exactly once, on a line that says which fields changed. A finding whose state is
unchanged is never repeated.

## Why / source

`known_ids()` treats a finding as finished forever:

```bash
known_ids() {
  [ -s "$INDEX" ] && awk -F'\t' 'NR > 1 && $1 ~ /^[0-9]+$/ {print $1}' "$INDEX"
  [ -s "$LOG" ] && sed -n 's/.*[[:space:]]id=\([0-9][0-9]*\)[[:space:]].*/\1/p' "$LOG"
}
```

An id present in `LEVEL2-INDEX.tsv` or in `inbox.log` is skipped unconditionally. That is correct for a
finding that stays put, and it is what keeps the queue clean: measured on 2026-11-22, 81 log lines hold
81 distinct ids, 0 duplicates, and 0 findings fall outside both the log and the index.

The defect is the other direction. `ROLE.md` section 7.1 requires every parked finding to name the exact
condition that would reopen it, and the queue has no way to honour that condition. The concrete case is
ARTEX findings 83 and 84, parked on 2026-11-22 because every one of the seven live IBM Cloud keys mints a
token whose claims carry `.account.frozen = true`. The recorded reopening condition is that a key's token
shows `frozen = false`. If that happens, nothing in the pipeline notices: the ids are in the index, so the
watcher will never queue them again.

The same hole covers three other changes that matter: Level 1 flipping a decided finding back to
`pending`, Level 1 adding evidence to a finding parked as not reproducible, and a severity change on a
finding parked as low.

Schema note, measured from `information_schema.columns` on 2026-11-22: the `findings` table has no
`updated_at` column. Its usable change signals are `status`, `severity`, `evidence_version`,
`report_evidence_version` and the row count in `finding_retests`. A fingerprint built from a column that
does not exist would silently never change, which is worse than the current behavior.

## Scope (files likely to change)

- `ops/triage-level2/watch.sh` (fingerprint, state file, reopen path)
- `ops/triage-level2/README.md` (the state file and the reopen line format)
- `/home/cowboy/bugbounty/programs/_ops/triage-level2/ROLE.md`, section 6 (the watcher's contract
  changes, so the profile changes with it)

The state file is written beside the queue, at
`/home/cowboy/bugbounty/programs/_ops/triage-level2/seen.tsv`, and is overridable as `WATCH_SEEN`. It
stays in the bug bounty tree on purpose: it is derived from program findings and has to stay consistent
with `inbox.log`, which is also program data. The code stays here, the data stays there.

## Acceptance

- [ ] a state file records, per finding id, the fingerprint `status`, `severity`, `evidence_version`, `report_evidence_version` and the `finding_retests` count
- [ ] a finding seen for the first time and absent from both the index and the log is queued as `NEW`, exactly as today
- [ ] a finding present in `LEVEL2-INDEX.tsv` with an unchanged fingerprint is never queued
- [ ] a finding present in `LEVEL2-INDEX.tsv` whose fingerprint changed is queued exactly once as `REOPENED`, and the line names the changed fields with their old and new values
- [ ] a decided finding whose `status` returns to `pending` is queued as `REOPENED`
- [ ] the same fingerprint is never queued twice, across restarts and across `--once` runs, proven by running `--once` twice
- [ ] findings 83 and 84, parked on 2026-11-22, produce no line on a run today, and do produce one `REOPENED` line when their `evidence_version` or `status` is changed by hand
- [ ] the state file survives a missing or corrupt copy: a truncated line is skipped with one warning, and the watcher keeps polling
- [ ] `--once` mode from TICKET-001 honours all of the above
- [ ] `go build ./...` and `go test ./...` green, stated in the PR (they are unaffected by an ops change, so state that explicitly)
- [ ] affected docs updated, including `ROLE.md` section 6
- [ ] PR opened; ticket set to `Feedback`

## Notes / dependencies

Do not implement this by dropping the index from `known_ids()`: that would re-queue all 18 decided
findings on every run and bury the real ones. The fingerprint is the point of the ticket.

Pairs with TICKET-001 and TICKET-002 through the shared `--once` mode. Order does not otherwise matter.

The `REOPENED` vocabulary is what `ROLE.md` section 7.1 needs in order to be enforceable rather than
aspirational: a reopening condition that no process can trigger is a comment, not a control.

## Log

- 2026-11-23: filed (New)
- 2026-11-23: the watcher moved to `ops/triage-level2/watch.sh` in this repository, which cleared the
  location question; released as To-Do
