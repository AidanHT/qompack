#!/bin/sh
# gc-negative-control.sh — prove the live-rotation GC retention test can fail (V6 close-out C1.10, gate 3).
#
#   sh gc-negative-control.sh <commit> <run-id> <scratch-dir>
#
# It extracts <commit> into <scratch-dir>/<run-id>, applies ONE mutation to internal/store/gcrun.go —
# GC's delivery-lease harvest reduced to the legacy segment 0, exactly what a reader that predates
# segments would harvest — and runs the live-rotation test there. The control PASSES (exit 0) only when
# the mutated build FAILS that test on the retention assertion; the same test passing on the unmutated
# commit is the other half of the evidence. The log goes to
# plans/sdd/V6-closeout/rollover/runs/<run-id>.log. The worktree itself is never modified.
set -eu
if test "$#" -ne 3; then
	echo 'usage: gc-negative-control.sh <commit> <run-id> <scratch-dir>' >&2
	exit 2
fi
commit=$1
run_id=$2
scratch=$3
repo=$(git rev-parse --show-toplevel)
sha=$(git -C "$repo" rev-parse --verify "$commit^{commit}")
snap="$scratch/$run_id"
log="$repo/plans/sdd/V6-closeout/rollover/runs/$run_id.log"
if test -e "$snap" || test -e "$log"; then
	echo "refusing to reuse $snap or $log" >&2
	exit 2
fi
mkdir -p "$snap"
git -C "$repo" archive "$sha" | tar -x -C "$snap"

target="$snap/internal/store/gcrun.go"
before=$(grep -c 'segs = append(segs, view.committed...)' "$target" || true)
if test "$before" != 1; then
	echo "mutation anchor not found exactly once in $target" >&2
	exit 2
fi
# The mutation: harvest segment 0 only, ignoring every committed and staged later segment.
sed -i 's/segs = append(segs, view.committed\.\.\.)/segs = append(segs, 0) \/\/ NEGATIVE CONTROL: legacy-only harvest/' "$target"
sed -i 's/segs = append(segs, view.staged\.\.\.)/_ = view.staged \/\/ NEGATIVE CONTROL/' "$target"
test_name='TestDeliveryReaders_V6_GCDuringLiveRotationsKeepsEveryArchivedUnsettledRoot'
{
	echo "# run: $run_id"
	echo "# commit: $sha (with the negative-control mutation below)"
	echo "# platform: $(go env GOOS)/$(go env GOARCH) $(go version)"
	echo "# started: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "# mutation:"
	grep -n 'NEGATIVE CONTROL' "$target" | sed 's/^/#   /'
	echo "# command: go test ./internal/daemon -run '^$test_name\$' -count=1 -timeout=30m -v"
} > "$log"
set +e
(cd "$snap" && go test ./internal/daemon -run "^$test_name\$" -count=1 -timeout=30m -v) >> "$log" 2>&1
status=$?
set -e
echo "# go test exit: $status" >> "$log"
if test "$status" -eq 0; then
	echo "# control: FAILED — the mutated build passed, so the test cannot detect a legacy-only harvest" >> "$log"
	echo "negative control FAILED: the test passed against a legacy-only GC harvest (log: $log)" >&2
	exit 1
fi
if ! grep -q 'its root must be retained\|its root is retained' "$log"; then
	echo "# control: INCONCLUSIVE — the mutated build failed, but not on the retention assertion" >> "$log"
	echo "negative control inconclusive: failure was not the retention assertion (log: $log)" >&2
	exit 1
fi
echo "# control: PASSED — the mutated build fails the retention assertion" >> "$log"
echo "negative control passed: the legacy-only harvest fails the test (log: $log)"
