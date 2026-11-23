#!/bin/bash
# watch.sh: poll ARTEX every 10 minutes and queue findings with no Level 2 verdict.
# Read only against ARTEX. It never writes to ARTEX and never decides anything.
# A finding is queued once: ids already logged in inbox.log are never repeated.
#
# Usage:
#   ./watch.sh          poll forever, sleeping INTERVAL between polls (default)
#   ./watch.sh --once   poll exactly once and exit 0, for a systemd oneshot unit
#
# Moved here from the bugbounty tree on 2026-11-23. The code lives in this repository;
# the queue it writes stays in the bug bounty tree, because program data is not product
# data. Every path below is overridable from the environment, and the defaults are the
# paths that were in use before the move, so the behavior is unchanged.
set -u

MODE=loop
case "${1:-}" in
  --once) MODE=once ;;
  "")     MODE=loop ;;
  *)      printf 'usage: %s [--once]\n' "$0" >&2; exit 2 ;;
esac

# Code and data are deliberately separate. Override for a test run.
BASE="${WATCH_BASE:-/home/cowboy/bugbounty}"
DIR="${WATCH_DIR:-$BASE/programs/_ops/triage-level2}"
INDEX="${WATCH_INDEX:-$BASE/programs/_triage/LEVEL2-INDEX.tsv}"
LOG="${WATCH_LOG:-$DIR/inbox.log}"
PG="${WATCH_PG:-scopeweaver-local-postgres-1}"
INTERVAL="${WATCH_INTERVAL:-600}"

mkdir -p "$DIR" "$(dirname "$INDEX")"
touch "$INDEX" "$LOG"

# Ids already decided (index) or already queued (inbox log), one per line.
known_ids() {
  [ -s "$INDEX" ] && awk -F'\t' 'NR > 1 && $1 ~ /^[0-9]+$/ {print $1}' "$INDEX"
  [ -s "$LOG" ] && sed -n 's/.*[[:space:]]id=\([0-9][0-9]*\)[[:space:]].*/\1/p' "$LOG"
}

# One poll. Returns 0 whether or not the query produced rows, so a oneshot unit is not
# marked failed while the database is away. The failure is recorded in the log instead;
# telling an outage apart from an empty table is TICKET-002.
poll_once() {
  local rows known ts
  rows=$(docker exec "$PG" psql -U artex -d artex -At -F '|' -c "
    SELECT id,
           coalesce(task_id::text, '-'),
           coalesce(nullif(severity, ''), '-'),
           coalesce(nullif(status, ''), '-'),
           coalesce(nullif(regexp_replace(name, '[|\r\n\t]+', ' ', 'g'), ''), '(unnamed)')
      FROM findings
     ORDER BY id" 2>/dev/null)

  if [ -z "$rows" ]; then
    printf '%s\tERROR\tARTEX query returned nothing\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$LOG"
    return 0
  fi

  known=$(known_ids | sort -u)
  ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)

  printf '%s\n' "$rows" | while IFS='|' read -r id task sev status name; do
    [ -n "${id:-}" ] || continue
    case "$id" in *[!0-9]*) continue ;; esac
    if [ -n "$known" ] && printf '%s\n' "$known" | grep -qx "$id"; then
      continue
    fi
    printf '%s\tNEW\tid=%s\ttask=%s\tseverity=%s\tstatus=%s\t%s\n' \
      "$ts" "$id" "$task" "$sev" "$status" "$name" >> "$LOG"
  done

  return 0
}

if [ "$MODE" = once ]; then
  poll_once
  exit 0
fi

while :; do
  poll_once
  sleep "$INTERVAL"
done
