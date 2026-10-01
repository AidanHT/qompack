#!/bin/sh
# overnight-at.sh <HHMM> <candidate-repo> <candidate-sha> <evidence-dir> [chain-script]
# Waits until local time HHMM, then holds its own keep-awake and runs the chain script (default
# overnight.sh; overnight-c6.sh for the release candidate). Launched detached
# (Start-Process), so Claude Code's background-shell reaper cannot stop it. Remove
# <evidence-dir>/cancel before HHMM to cancel it; <evidence-dir>/at.log records what it did.
set -u
at=$1; C=$2; H=$3; E=$4; chain=${5:-overnight.sh}
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$E"
: > "$E/cancel"
target=$(date -d "today ${at%??}:${at#??}" +%s)
echo "armed for $at pid $$ $(date '+%F %T %Z')" >> "$E/at.log"
# At or after the target, not at its exact minute: a laptop asleep across HHMM starts on waking.
while [ "$(date +%s)" -lt "$target" ]; do
  [ -f "$E/cancel" ] || { echo "cancelled $(date '+%F %T %Z')" >> "$E/at.log"; exit 0; }
  sleep 20
done
sentinel="$E/keepawake.sentinel"
: > "$sentinel"
pwsh -NoProfile -File "$(cygpath -w "$here/keepawake.ps1")" "$(cygpath -w "$sentinel")" >> "$E/at.log" 2>&1 &
echo "start $(date '+%F %T %Z')" >> "$E/at.log"
sh "$here/$chain" "$C" "$H" "$E"
echo "overnight exit=$? $(date '+%F %T %Z')" >> "$E/at.log"
rm -f "$sentinel"
