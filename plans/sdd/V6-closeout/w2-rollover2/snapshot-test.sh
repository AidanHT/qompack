#!/bin/sh
# snapshot-test.sh — run `go test` on an exact commit of this worktree, in a disposable snapshot, so
# the worktree can keep changing while the run proceeds and the result is tied to one commit.
# (The w2-rollover2 copy of plans/sdd/V6-closeout/rollover/snapshot-test.sh; only the log directory
# differs.)
#
#   sh snapshot-test.sh <commit> <run-id> <scratch-dir> -- <go test args...>
#
# The snapshot is `git archive <commit>` extracted under <scratch-dir>/<run-id>; the log goes to
# plans/sdd/V6-closeout/w2-rollover2/runs/<run-id>.log with a header naming the commit, the platform
# and the exact command, and a trailer with go test's own exit status (never masked by a pipe).
set -eu
if test "$#" -lt 4; then
	echo 'usage: snapshot-test.sh <commit> <run-id> <scratch-dir> -- <go test args...>' >&2
	exit 2
fi
commit=$1
run_id=$2
scratch=$3
shift 3
test "$1" = "--" || { echo 'expected -- before the go test arguments' >&2; exit 2; }
shift

repo=$(git rev-parse --show-toplevel)
sha=$(git -C "$repo" rev-parse --verify "$commit^{commit}")
snap="$scratch/$run_id"
log="$repo/plans/sdd/V6-closeout/w2-rollover2/runs/$run_id.log"
if test -e "$snap" || test -e "$log"; then
	echo "refusing to reuse $snap or $log" >&2
	exit 2
fi
mkdir -p "$snap" "$(dirname "$log")"
git -C "$repo" archive "$sha" | tar -x -C "$snap"
{
	echo "# run: $run_id"
	echo "# commit: $sha"
	echo "# platform: $(go env GOOS)/$(go env GOARCH) $(go version)"
	echo "# started: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "# env: CGO_ENABLED=${CGO_ENABLED:-} GOMAXPROCS=${GOMAXPROCS:-} QOMPACK_UNDER_COLOAD=${QOMPACK_UNDER_COLOAD:-}"
	echo "# command: go test $*"
} > "$log"
set +e
(cd "$snap" && go test "$@") >> "$log" 2>&1
status=$?
set -e
{
	echo "# finished: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "# exit: $status"
} >> "$log"
echo "exit=$status log=$log"
exit "$status"
