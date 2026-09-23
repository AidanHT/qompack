#!/bin/bash
# usage: linux-ab.sh <label> <rounds> <bench-regex> <benchtime> [tmpdir]
# Interleaves the base+bench and final store test binaries in ABBA order, one -count 1 run each.
L=$1; R=$2; B=$3; T=$4; TD=${5:-}
O=/work/cx-perfstore-ab2
export GOMAXPROCS=4
[ -n "$TD" ] && export TMPDIR=$TD
: > $O/$L-load.txt
for i in $(seq 1 $R); do
  for v in base final; do
    if [ $((i % 2)) -eq 0 ]; then v=$([ $v = base ] && echo final || echo base); fi
    src=/work/cx-perfstore-basebench2; bin=base; [ $v = final ] && src=/work/cx-perfstore-final3 && bin=final3
    echo "round $i $v $(date -u +%H:%M:%S) load $(cut -d' ' -f1-3 /proc/loadavg)" >> $O/$L-load.txt
    ( cd $src/internal/store && $O/store-$bin.test -test.run '^$' -test.bench "$B" -test.benchmem \
        -test.benchtime "$T" -test.count 1 -test.timeout 60m > $O/$L-$v-round$i.raw 2>&1; echo "exit=$?" >> $O/$L-$v-round$i.raw )
  done
done
echo "done $(date -u +%H:%M:%S) load $(cut -d' ' -f1-3 /proc/loadavg)" >> $O/$L-load.txt
