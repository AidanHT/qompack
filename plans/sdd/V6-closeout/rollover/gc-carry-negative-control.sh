#!/bin/sh
# gc-carry-negative-control.sh — prove the GC retention tests can fail against the bounded harvest
# (V6 close-out C1.10, gate 3; review findings 1 and 6).
#
#   sh gc-carry-negative-control.sh <commit> <run-id> <scratch-dir>
#
# It extracts <commit> into <scratch-dir>/<run-id>, applies ONE mutation to internal/store/gcrun.go —
# GC stops reading the active segment's carried-lease file, so the archived leases with no
# acknowledgement are no longer harvested — and runs the two daemon-writer GC tests there: the live
# rotation test and the step-by-step one. The control PASSES (exit 0) only when the mutated build FAILS
# on a retention assertion; the same tests passing on the unmutated commit is the other half of the
# evidence. The log goes to plans/sdd/V6-closeout/rollover/runs/<run-id>.log. The worktree itself is
# never modified.
set -eu
if test "$#" -ne 3; then
	echo 'usage: gc-carry-negative-control.sh <commit> <run-id> <scratch-dir>' >&2
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
anchor='if seq == 0 {'
if test "$(grep -c "$anchor" "$target" || true)" != 1; then
	echo "mutation anchor not found exactly once in $target" >&2
	exit 2
fi
# The mutation: never harvest a carried-lease file.
sed -i 's/if seq == 0 {/if true { \/\/ NEGATIVE CONTROL: carried leases ignored/' "$target"
tests='TestDeliveryReaders_V6_GCDuringLiveRotationsKeepsEveryArchivedUnsettledRoot|TestDeliveryReaders_V6_GCAtEveryRotationStepKeepsEveryUnsettledRoot'
{
	echo "# run: $run_id"
	echo "# commit: $sha (with the negative-control mutation below)"
	echo "# platform: $(go env GOOS)/$(go env GOARCH) $(go version)"
	echo "# started: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "# mutation:"
	grep -n 'NEGATIVE CONTROL' "$target" | sed 's/^/#   /'
	echo "# command: go test ./internal/daemon -run '^($tests)\$' -count=1 -timeout=30m -v"
} > "$log"
set +e
(cd "$snap" && go test ./internal/daemon -run "^($tests)\$" -count=1 -timeout=30m -v) >> "$log" 2>&1
status=$?
set -e
echo "# go test exit: $status" >> "$log"
if test "$status" -eq 0; then
	echo "# control: FAILED — the mutated build passed, so the tests cannot detect a harvest without the carry" >> "$log"
	echo "negative control FAILED: the tests passed against a harvest that ignores the carry (log: $log)" >&2
	exit 1
fi
for t in GCDuringLiveRotationsKeepsEveryArchivedUnsettledRoot GCAtEveryRotationStepKeepsEveryUnsettledRoot; do
	if ! grep -q -- "--- FAIL: TestDeliveryReaders_V6_$t" "$log"; then
		echo "# control: INCONCLUSIVE — TestDeliveryReaders_V6_$t did not fail" >> "$log"
		echo "negative control inconclusive: $t did not fail (log: $log)" >&2
		exit 1
	fi
done
if ! grep -q 'its root must be retained\|its root is retained' "$log"; then
	echo "# control: INCONCLUSIVE — the mutated build failed, but not on a retention assertion" >> "$log"
	echo "negative control inconclusive: failure was not a retention assertion (log: $log)" >&2
	exit 1
fi
echo "# control: PASSED — the mutated build fails both tests on retention" >> "$log"
echo "negative control passed: a harvest that ignores the carry fails both tests (log: $log)"
