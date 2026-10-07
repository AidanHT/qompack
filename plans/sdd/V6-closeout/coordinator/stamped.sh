#!/bin/sh
# stamped.sh <command...>
# Runs the command with stdout and stderr merged and prefixes every output line with the epoch
# second and the local time at which it was read: "<epoch> <YYYY-MM-DDTHH:MM:SS> <line>". Exits with
# the command's own status (a plain pipe would report awk's). overnight-c8.sh uses the epochs to find
# release-check's AC-sensitive windows and compares them with the System log's power events.
# Test seam: when STAMP_CLOCK_FILE names a file, each line's epoch is read from it instead of the
# system clock, so nightharness.sh can drive a fake clock (it is unset on a real night).
set -u
[ $# -ge 1 ] || { echo "usage: stamped.sh <command...>" >&2; exit 2; }
st=$(mktemp) || exit 2
{ "$@" 2>&1; echo $? > "$st"; } |
  awk -v cf="${STAMP_CLOCK_FILE:-}" '{
    if (cf != "") { t = ""; getline t < cf; close(cf); t = t + 0 } else t = systime()
    print t, strftime("%Y-%m-%dT%H:%M:%S", t), $0; fflush()
  }'
rc=$(cat "$st" 2>/dev/null); rm -f "$st"
case $rc in ''|*[!0-9]*) echo "stamped.sh: the command's exit status was not recorded" >&2; exit 2 ;; esac
exit "$rc"
