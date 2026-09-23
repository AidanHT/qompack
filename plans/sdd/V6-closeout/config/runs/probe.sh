#!/usr/bin/env bash
# Usage: probe.sh <qompack-binary> <scratch-base>
# One project per config body: runs the observe-prompt hook (no daemon spawn: only session-start
# spawns), then `config print --provenance` (soft loader) and `self-test --json`, and reports what
# the hook left under .qompack/. Any daemon self-test spawns is stopped by the PID in its own lock.
set -u
BIN="$1"; BASE="$2"
mkdir -p "$BASE"
HOMEDIR="$BASE/home"; mkdir -p "$HOMEDIR"
i=0
while IFS= read -r body; do
  [ -z "$body" ] && continue
  i=$((i+1))
  P="$BASE/p$i"; rm -rf "$P"; mkdir -p "$P/.qompack"
  printf '%s' "$body" > "$P/.qompack/config.json"
  PWIN=$(cygpath -m "$P")
  payload=$(printf '{"hook_event_name":"UserPromptSubmit","session_id":"probe-%d","cwd":"%s","prompt":"an ordinary prompt"}' "$i" "$PWIN")
  echo "=== case p$i: $body"
  out=$(printf '%s' "$payload" | HOME="$HOMEDIR" USERPROFILE="$HOMEDIR" QOMPACK_PROJECT_ROOT="$PWIN" "$BIN" observe prompt 2>&1); rc=$?
  echo "hook stdout+stderr: $out"; echo "hook exit=$rc"
  echo "spool files after hook: $(find "$P/.qompack/spool" -type f 2>/dev/null | wc -l)"
  echo ".qompack entries after hook: $(cd "$P/.qompack" && find . -mindepth 1 | sort | tr '\n' ' ')"
  if [ -f "$P/.qompack/state/config-violations.json" ]; then echo "config-violations.json:"; cat "$P/.qompack/state/config-violations.json"; fi
  (cd "$P" && HOME="$HOMEDIR" USERPROFILE="$HOMEDIR" QOMPACK_PROJECT_ROOT="$PWIN" "$BIN" config print --provenance >/dev/null 2>&1; echo "config print exit=$?")
  st=$(cd "$P" && HOME="$HOMEDIR" USERPROFILE="$HOMEDIR" QOMPACK_PROJECT_ROOT="$PWIN" "$BIN" self-test --json 2>/dev/null); strc=$?
  echo "self-test exit=$strc"
  printf '%s' "$st" | python -c 'import json,sys
d=json.load(sys.stdin)
for c in d["checks"]:
    if c["id"].startswith("config"):
        print("self-test row:", c["id"], "ok=%s"%c["ok"], "severity=%s"%c["severity"], "observed=%s"%c["observed"], ("detail=%s"%c.get("detail","")) if c.get("detail") else "")' 2>&1
  lock="$P/.qompack/run/daemon.lock"
  if [ -f "$lock" ]; then
    pid=$(python -c 'import json,sys;print(json.load(open(sys.argv[1]))["pid"])' "$lock" 2>/dev/null)
    if [ -n "$pid" ]; then powershell -NoProfile -Command "Stop-Process -Id $pid -Force -ErrorAction SilentlyContinue" ; echo "stopped self-test-spawned daemon pid=$pid"; fi
  fi
done
