#!/bin/bash
# usage: linux-cpu-ab.sh <label> <rounds> <bench-regex> <benchtime> [tmpdir]
# Interleaved process-CPU A/B: bash's time builtin records each run's wall, user and system time.
L=$1; R=$2; B=$3; T=$4; TD=${5:-}
O=/work/cx-perfstore-ab2
export GOMAXPROCS=4
[ -n "$TD" ] && export TMPDIR=$TD
TIMEFORMAT='%R %U %S'
printf 'round\tvariant\twall_s\tuser_s\tsys_s\tload\tbench_line\n' > $O/$L-cpu.tsv
for i in $(seq 1 $R); do
  for v in base final; do
    if [ $((i % 2)) -eq 0 ]; then v=$([ $v = base ] && echo final || echo base); fi
    src=/work/cx-perfstore-basebench2; [ $v = final ] && src=/work/cx-perfstore-final2
    ld=$(cut -d' ' -f1 /proc/loadavg)
    t=$( { time ( cd $src/internal/store && $O/store-$v.test -test.run '^$' -test.bench "$B" -test.benchmem \
        -test.benchtime "$T" -test.count 1 -test.timeout 60m > $O/$L-$v-round$i.raw 2>&1 ) ; } 2>&1 )
    line=$(grep '^Benchmark' $O/$L-$v-round$i.raw | tail -1)
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' $i $v $(echo $t) "$ld" "$line" >> $O/$L-cpu.tsv
  done
done
echo done >> $O/$L-cpu.tsv
