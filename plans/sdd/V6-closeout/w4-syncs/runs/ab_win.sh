#!/bin/sh
# Paired, interleaved ABBA A/B on the Windows host for w4-syncs (SP08-D1).
#
#   sh ab_win.sh <rounds> <outdir> [benchtime]
#
# base = git archive 31255da (closeout/integration 6aff949 is identical outside eval files) plus
#        only internal/observer/tooluse_leased_bench_test.go copied in, so the leased rows exist on
#        both sides;
# new  = git archive of the w4-syncs head the binaries were built from (new-rev.txt).
# Both test binaries are built once (go test -c) and run from their own tree's internal/observer
# (the fixture is read from ../../testdata). Round r runs base,new when r is odd and new,base when
# it is even (ABBA), one -count=1 sample per side per round, so host drift and co-load from the
# other workstreams hit both sides alike. Every sample records its start/end time, its exit code and
# the host's CPU load sampled just before it.
set -u
rounds=$1
out=$2
bt=${3:-10x}
W=C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/resume/syncs-work
bench='BenchmarkOnToolUse_TestOutput256KB(_Leased)?/(Deduped|Delta|AllNovel)$'
mkdir -p "$out"
load() {
	powershell -NoProfile -Command "(Get-CimInstance Win32_Processor | Measure-Object -Property LoadPercentage -Average).Average" 2>/dev/null | tr -d '\r'
}
run() { # side round
	cd "$W/$1/internal/observer" || exit 2
	echo "# round=$2 side=$1 start=$(date -u +%H:%M:%S) cpu_load_pct=$(load)" >> "$out/$1.txt"
	"$W/bin/$1-observer.test.exe" -test.run '^$' -test.bench "$bench" -test.benchtime "$bt" \
		-test.benchmem -test.count 1 -test.timeout 60m >> "$out/$1.txt" 2>&1
	echo "# round=$2 side=$1 exit=$? end=$(date -u +%H:%M:%S)" >> "$out/$1.txt"
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
echo "done rounds=$rounds benchtime=$bt" > "$out/done.txt"
