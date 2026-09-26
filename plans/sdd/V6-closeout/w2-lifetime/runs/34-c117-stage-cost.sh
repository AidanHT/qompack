#!/usr/bin/env bash
# C1.17 review finding 5: what staging the daemon binary costs a cold `qompack session-start` on
# Windows, end to end, with the real binary. Git Bash on Windows only.
#
# usage: 34-c117-stage-cost.sh <repo> <workdir> [N]
#
# Three modes, N measured runs each, every run in a fresh project:
#   unstaged     the binary run from a bare directory (no plugin layout, no CLAUDE_PLUGIN_ROOT):
#                nothing is copied, the daemon runs from that binary (the pre-C1.17 path).
#   staged-cold  the binary run from a plugin layout with CLAUDE_PLUGIN_ROOT set, as a host hook
#                is, and a fresh home each run: every run copies, fsyncs and verifies (a version's
#                first spawn), and the daemon's first execution is of a file just written.
#   staged-warm  the same, sharing one home already holding the copy: hash + verify only.
# Each row: the hook's wall time, whether session-start's own request was spooled (the daemon was
# not up when EnsureRunning's 1.5 s poll ended), and the executable the daemon ran from. The
# daemon is then stopped by the pid in its own lock file — only processes this script started.
set -u
REPO=$1
W=$2
N=${3:-10}

mkdir -p "$W/bare" "$W/plugin/bin" "$W/plugin/.claude-plugin" "$W/p"
(cd "$REPO" && go build -o "$W/bare/qompack.exe" ./cmd/qompack) || exit 1
cp "$W/bare/qompack.exe" "$W/plugin/bin/qompack.exe"
printf '{"name":"qompack"}' >"$W/plugin/.claude-plugin/plugin.json"
echo "# binary: $(stat -c %s "$W/bare/qompack.exe") bytes, sha256 $(sha256sum "$W/bare/qompack.exe" | cut -c1-16)..."

now_ms() { date +%s%3N; }

daemon_pid() { # project
	sed -n 's/.*"pid":\([0-9]*\).*/\1/p' "$1/.qompack/run/daemon.lock" 2>/dev/null
}

one() { # mode label home
	local mode=$1 label=$2 home=$3
	local proj="$W/p/$label"
	mkdir -p "$proj/.git" "$home"
	local bin="$W/plugin/bin/qompack.exe" root
	root=$(cygpath -w "$W/plugin")
	if [ "$mode" = unstaged ]; then
		bin="$W/bare/qompack.exe"
		root=""
	fi
	local payload
	payload=$(printf '{"hook_event_name":"SessionStart","session_id":"%s","cwd":"%s","source":"startup"}' \
		"$label" "$(cygpath -m "$proj")")
	local t0 t1
	t0=$(now_ms)
	(cd "$W" && CLAUDE_PLUGIN_ROOT="$root" QOMPACK_PROJECT_ROOT="$(cygpath -w "$proj")" \
		HOME="$(cygpath -w "$home")" USERPROFILE="$(cygpath -w "$home")" \
		"$bin" session-start <<<"$payload" >/dev/null 2>&1)
	t1=$(now_ms)
	local spooled=no
	if grep -qs '"session.start"' "$proj"/.qompack/spool/client-*.ndjson; then spooled=yes; fi
	local pid exe=""
	for _ in $(seq 1 100); do
		pid=$(daemon_pid "$proj")
		[ -n "$pid" ] && break
		sleep 0.05
	done
	if [ -n "$pid" ]; then
		exe=$(powershell.exe -NoProfile -Command "(Get-Process -Id $pid -ErrorAction SilentlyContinue).Path" | tr -d '\r')
		taskkill //F //PID "$pid" >/dev/null 2>&1
	fi
	printf '%-12s %-18s %6d ms  spooled=%-3s daemon=%s\n' "$mode" "$label" $((t1 - t0)) "$spooled" "${exe:-none}"
}

for i in $(seq 1 "$N"); do one unstaged "unstaged-$i" "$W/h/unstaged-$i"; done
for i in $(seq 1 "$N"); do one staged-cold "cold-$i" "$W/h/cold-$i"; done
one staged-warm "warm-0-primes-the-copy" "$W/h/warm"
for i in $(seq 1 "$N"); do one staged-warm "warm-$i" "$W/h/warm"; done
