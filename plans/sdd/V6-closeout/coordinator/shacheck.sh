#!/bin/sh
# shacheck.sh <worktree> <file>: every quoted 7-40 hex SHA must resolve to a commit reachable from HEAD
w=$1; f=$2; bad=0
for s in $(grep -oE '\b[0-9a-f]{7,40}\b' "$f" | sort -u); do
  echo "$s" | grep -qE '[a-f]' || continue
  full=$(git -C "$w" rev-parse --verify -q "$s^{commit}" 2>/dev/null) || { echo "unresolved $s"; continue; }
  git -C "$w" merge-base --is-ancestor "$full" HEAD || { echo "NOT-IN-HEAD $s"; bad=1; }
done
exit $bad
