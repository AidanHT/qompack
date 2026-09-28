#!/bin/sh
# Paired, interleaved ABBA A/B inside the Linux verification container for w5-dirsync (SP08-D1,
# owner decision D20), adapted from w4-syncs/runs/linux_ab.sh.
#
#   docker exec qompack-v6-linux-verification sh /work/<W>/linux_ab.sh <W> <rounds> [benchtime]
#
# /work/<W>-base and /work/<W>-new are the same two trees the Windows A/B uses (base = git archive
# 90e1db3; new = git archive of the w5-dirsync head), copied in as tars.
# linux-nonroot-gate.sh has no -bench option, so this driver builds each tree's internal/observer
# test binary once and runs it from that tree's package directory, GOMAXPROCS=4, as root (the
# benchmarks have no permission fixtures). Round r runs base,new when r is odd and new,base when it
# is even, one -count=1 sample per side per round; each sample records its exit code and the
# container's load average. It never touches another /work directory.
#
# The unleased rows are a control: they run no publication pass, so they should not move.
set -u
W=$1
rounds=$2
bt=${3:-10x}
A=/work/$W-artifacts
test -e "$A" && { echo "refusing: $A exists" >&2; exit 2; }
mkdir -p "$A" /work/$W-bin
bench='BenchmarkOnToolUse_TestOutput256KB(_Leased)?/(Deduped|Delta|AllNovel)$'
for side in base new; do
	(cd /work/$W-$side && GOMAXPROCS=4 go test -c -o /work/$W-bin/$side.test ./internal/observer) ||
		{ echo "build failed: $side" >&2; exit 3; }
done
{ go version; uname -a; nproc; } > "$A/host.txt"
run() { # side round
	cd /work/$W-$1/internal/observer || exit 2
	echo "# round=$2 side=$1 start=$(date -u +%H:%M:%S) loadavg=$(cut -d' ' -f1-3 /proc/loadavg)" >> "$A/$1.txt"
	GOMAXPROCS=4 /work/$W-bin/$1.test -test.run '^$' -test.bench "$bench" -test.benchtime "$bt" \
		-test.benchmem -test.count 1 -test.timeout 60m >> "$A/$1.txt" 2>&1
	echo "# round=$2 side=$1 exit=$? end=$(date -u +%H:%M:%S)" >> "$A/$1.txt"
}
for r in $(seq 1 "$rounds"); do
	if test $((r % 2)) -eq 1; then
		run base "$r"
		run new "$r"
	else
		run new "$r"
		run base "$r"
	fi
done
echo "done rounds=$rounds benchtime=$bt" > "$A/done.txt"
