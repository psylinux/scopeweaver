#!/usr/bin/env bash
# Mirror the mailotp Fase 1 sources from the standalone repo into cmd/mailotp.
#
# Source of truth is github.com/psylinux/mailotp; this copy is a generated mirror
# that the scopeweaver image builds into /usr/local/bin/mailotp (TICKET-007, build
# source Option A). Re-run this at each mailotp release and commit the result.
#
# Usage: scripts/sync-mailotp.sh [path-to-mailotp-checkout]
set -eu
SRC="${1:-$HOME/bugbounty/src/mailotp}"
DST="$(cd "$(dirname "$0")/.." && pwd)/cmd/mailotp"

files="main.go extract.go imap.go proto.go store.go serve.go client_cmds.go metrics.go extract_test.go store_test.go"
for f in $files; do
	cp "$SRC/$f" "$DST/$f"
done

# The standalone module bundles its own sharedsecrets; in the scopeweaver module
# use artex's package instead (same API). This is the only import that differs.
sed -i 's#github.com/psylinux/mailotp/internal/sharedsecrets#github.com/Autumn-27/artex/sharedsecrets#g' "$DST"/*.go

echo "synced mailotp sources -> $DST (from $SRC)"
