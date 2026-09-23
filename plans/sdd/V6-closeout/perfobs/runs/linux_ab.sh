#!/bin/sh
# Interleaved A/B on Linux for the V6 close-out perf work: base (cf31e01) then new (HEAD of
# closeout/perfobs), one -count=1 sample each per round, GOMAXPROCS=4, so drift and co-load hit
# both sides alike. Run as root: these benchmarks have no permission fixtures.
SUF=$1
ROUNDS=${2:-8}
W=/work/cx-perfobs-$SUF
A=$W-artifacts/ab
mkdir -p $A
echo "new=$(git -C $W-new rev-parse --short HEAD) base=$(git -C $W-base rev-parse --short HEAD)" > $A/builds.txt
run() { # pkg bench benchtime side [file-suffix]
  cd $W-$4/internal/$1 || exit 2
  GOMAXPROCS=4 $W-bin/$4/$1.test -test.run '^$' -test.bench "$2" -test.benchtime "$3" -test.benchmem \
    -test.count 1 -test.timeout 60m >> $A/$1$5-$4.txt 2>&1
  echo "# round=$r side=$4 exit=$? at $(date -u +%H:%M:%S)" >> $A/$1$5-$4.txt
}
for r in $(seq 1 $ROUNDS); do
  for side in base new; do
    run negknow '^BenchmarkOpen$' 10x $side
    run canon '^BenchmarkRun_' 1s $side
    run observer 'BenchmarkOnToolUse_TestOutput256KB/(Deduped|Delta)$' 200x $side
    run checkpoint '^BenchmarkFinalize$' 10x $side
  done
done
for r in 1 2 3; do
  for side in base new; do
    run observer 'BenchmarkOnToolUse_TestOutput256KB/AllNovel$' 200x $side -allnovel
  done
done
# The SP09-D1 evidence test itself, three times per side, interleaved.
for r in 1 2 3; do
  for side in base new; do
    cd $W-$side/internal/negknow || exit 2
    GOMAXPROCS=4 $W-bin/$side/negknow.test -test.run '^TestBudget_Open$' -test.v -test.count 1 \
      -test.timeout 30m >> $A/budget-open-$side.txt 2>&1
    echo "# round=$r side=$side exit=$? at $(date -u +%H:%M:%S)" >> $A/budget-open-$side.txt
  done
done
echo ab-done > $A/ab-done.txt
