#!/usr/bin/env bash
# reproduce.sh — re-runs the w10-lostev TEMPORARY DIAGNOSTICS from their committed sources.
#
# Nothing here is built by the repository: every source is a .go.txt or a .patch, and this script
# materializes it into an untracked _w10diag/ directory at the worktree root (Go's ./... skips a
# directory whose name starts with "_"), runs it, and leaves it there until `reproduce.sh clean`.
#
#   reproduce.sh red
#       The RED half of the fix. Copies the NON-test files of test/bench/hotpath at PREFIX_COMMIT
#       (the harness before the fix) into _w10diag/red with zz_w10red_test.go.txt and
#       zz_w10dup_test.go.txt as _test.go files, and runs `go test -count=1 -v ./_w10diag/red/`.
#       Both tests are expected to FAIL (exit 1): the count-based guard calls a replayed deferral
#       LOST, and lets a spooled duplicate of a delivered request hide a real loss.
#
#   reproduce.sh prefix|postfix <scratch-dir>
#       The forced-breach reproduction. Copies the non-test files of test/bench/hotpath at
#       PREFIX_COMMIT (prefix) or POSTFIX_COMMIT (postfix) into _w10diag/<mode>, adds w10diag.go.txt
#       as w10diag.go, applies sampler-<mode>.patch to its main.go, builds it to
#       <scratch-dir>/w10diag-<mode>.exe, creates a FRESH project <scratch-dir>/proj-<mode> whose
#       .qompack/config.json is forced-breach-config.json (a 1ms B-A budget and one breach window,
#       so the daemon's §8.1/§12.2 breach detector moves the hooks to spool submode after the
#       first 512-sample window, the way w9's Phase 3 load did after three), and runs the harness
#       from the worktree root:
#           timeout 1200 <exe> --iterations 1000 --warm-daemon --under-coload --project <proj>
#       prefix is expected to exit 1 on the old guard's "N are LOST" while its own W10DIAG lines
#       show every identity that left a client spool mid-run in the store and no B-A request in
#       neither the store nor a spool file; postfix is expected to exit 0 with "0 lost".
#       The harness gives every child a fake HOME/USERPROFILE and its own pipe; it never touches
#       ~/.qompack. It spawns one hook process at a time; do not run it while an isolated timing
#       gate is running on the same machine.
#
#   reproduce.sh clean
#       Removes _w10diag/.
#
# The product code the copies run against is the worktree's own: git diff 8245d4e b7ce0e24 touches
# nothing outside test/bench/hotpath, so either copy measures the same daemon and hooks.
set -euo pipefail

PREFIX_COMMIT=8245d4ea  # closeout/integration: the count-based guard
POSTFIX_COMMIT=b7ce0e24 # closeout/w10-lostev: the identity census

WT=$(git rev-parse --show-toplevel)
DIAG="$WT/plans/sdd/V6-closeout/w10-lostev/diag"
OUT="$WT/_w10diag"

materialize() { # commit dst
	local commit=$1 dst=$2 f
	rm -rf "$dst"
	mkdir -p "$dst"
	for f in $(git -C "$WT" ls-tree --name-only "$commit" test/bench/hotpath/); do
		case "$f" in
		*_test.go) continue ;;
		*.go) git -C "$WT" show "$commit:$f" >"$dst/$(basename "$f")" ;;
		esac
	done
}

mode=${1:-}
case "$mode" in
red)
	materialize "$PREFIX_COMMIT" "$OUT/red"
	cp "$DIAG/zz_w10red_test.go.txt" "$OUT/red/zz_w10red_test.go"
	cp "$DIAG/zz_w10dup_test.go.txt" "$OUT/red/zz_w10dup_test.go"
	cd "$WT"
	set +e
	go test -count=1 -v ./_w10diag/red/
	rc=$?
	set -e
	echo "exit=$rc"
	;;
prefix | postfix)
	scratch=${2:?usage: reproduce.sh $mode <scratch-dir>}
	commit=$PREFIX_COMMIT
	[ "$mode" = postfix ] && commit=$POSTFIX_COMMIT
	materialize "$commit" "$OUT/$mode"
	cp "$DIAG/w10diag.go.txt" "$OUT/$mode/w10diag.go"
	git -C "$WT" apply -p1 --directory="_w10diag/$mode" "$DIAG/sampler-$mode.patch"
	mkdir -p "$scratch"
	exe="$scratch/w10diag-$mode.exe"
	proj="$scratch/proj-$mode"
	if [ -e "$proj" ]; then
		echo "reproduce.sh: $proj already exists; the harness needs a fresh project" >&2
		exit 2
	fi
	mkdir -p "$proj/.qompack"
	cp "$DIAG/forced-breach-config.json" "$proj/.qompack/config.json"
	cd "$WT"
	go build -o "$exe" "./_w10diag/$mode"
	echo "# $mode harness = test/bench/hotpath at $commit + sampler-$mode.patch + w10diag.go.txt; product = $(git rev-parse --short HEAD)"
	set +e
	timeout 1200 "$exe" --iterations 1000 --warm-daemon --under-coload --project "$proj"
	rc=$?
	set -e
	echo "exit=$rc"
	;;
clean)
	rm -rf "$OUT"
	;;
*)
	echo "usage: reproduce.sh red | prefix <scratch-dir> | postfix <scratch-dir> | clean" >&2
	exit 2
	;;
esac
