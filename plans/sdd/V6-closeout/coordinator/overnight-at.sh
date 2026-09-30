#!/bin/sh
# overnight-at.sh <HHMM> <candidate-repo> <candidate-sha> <evidence-dir>
# Waits until local time HHMM, then holds its own keep-awake and runs overnight.sh. Launched detached
# (Start-Process), so Claude Code's background-shell reaper cannot stop it. Remove
# <evidence-dir>/cancel before HHMM to cancel it; <evidence-dir>/at.log records what it did.
set -u
at=$1; C=$2; H=$3; E=$4
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$E"
: > "$E/cancel"
echo "armed for $at pid $$ $(date '+%F %T %Z')" >> "$E/at.log"
while [ "$(date +%H%M)" != "$at" ]; do
  [ -f "$E/cancel" ] || { echo "cancelled $(date '+%F %T %Z')" >> "$E/at.log"; exit 0; }
  sleep 20
done
sentinel="$E/keepawake.sentinel"
: > "$sentinel"
pwsh -NoProfile -File "$(cygpath -w "$here/keepawake.ps1")" "$(cygpath -w "$sentinel")" >> "$E/at.log" 2>&1 &
echo "start $(date '+%F %T %Z')" >> "$E/at.log"
sh "$here/overnight.sh" "$C" "$H" "$E"
echo "overnight exit=$? $(date '+%F %T %Z')" >> "$E/at.log"
rm -f "$sentinel"
