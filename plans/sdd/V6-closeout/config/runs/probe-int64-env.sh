#!/usr/bin/env bash
# Usage: probe-int64-env.sh <qompack-binary> <scratch-base>
# C1.8 review finding 1 through the real binary: a project config that sets checkpoint.budgetTokens
# to 9000, plus ONE integer leaf set to MaxInt64 in the environment (and then by --set for
# config print). Reports whether the hook recorded, what config print says the budget is (9000 means
# the project layer survived; 12000 is the default every layer was reset to), and self-test's
# config.capture row. Any daemon self-test spawns is stopped by the PID in its own lock, as probe.sh
# does.
set -u
BIN="$1"; BASE="$2"
rm -rf "$BASE"; mkdir -p "$BASE"
HOMEDIR="$BASE/home"; mkdir -p "$HOMEDIR"
P="$BASE/p"; mkdir -p "$P/.qompack"
printf '%s' '{"checkpoint":{"budgetTokens":9000}}' > "$P/.qompack/config.json"
PWIN=$(cygpath -m "$P")
MAX=9223372036854775807
export HOME="$HOMEDIR" USERPROFILE="$HOMEDIR" QOMPACK_PROJECT_ROOT="$PWIN"
echo "=== project config: $(cat "$P/.qompack/config.json"); QOMPACK_RUNTIME__DAEMON__MAXSESSIONS=$MAX"
payload=$(printf '{"hook_event_name":"UserPromptSubmit","session_id":"probe-int64","cwd":"%s","prompt":"an ordinary prompt"}' "$PWIN")
out=$(printf '%s' "$payload" | QOMPACK_RUNTIME__DAEMON__MAXSESSIONS=$MAX "$BIN" observe prompt 2>&1); rc=$?
echo "hook stdout+stderr: $out"; echo "hook exit=$rc"
echo "spool files after hook: $(find "$P/.qompack/spool" -type f 2>/dev/null | wc -l)"
for how in env flag; do
  if [ "$how" = env ]; then
    js=$(cd "$P" && QOMPACK_RUNTIME__DAEMON__MAXSESSIONS=$MAX "$BIN" config print --json 2>"$BASE/print-$how.err"); prc=$?
  else
    js=$(cd "$P" && "$BIN" config print --json --set runtime.daemon.maxSessions=$MAX 2>"$BASE/print-$how.err"); prc=$?
  fi
  budget=$(printf '%s' "$js" | python -c 'import json,sys; d=json.load(sys.stdin); print(d["checkpoint"]["budgetTokens"], d["runtime"]["daemon"]["maxSessions"])' 2>&1)
  echo "config print ($how) exit=$prc checkpoint.budgetTokens runtime.daemon.maxSessions = $budget"
  echo "config print ($how) stderr: $(tr '\n' ' ' < "$BASE/print-$how.err")"
done
st=$(cd "$P" && QOMPACK_RUNTIME__DAEMON__MAXSESSIONS=$MAX "$BIN" self-test --json 2>/dev/null); strc=$?
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
