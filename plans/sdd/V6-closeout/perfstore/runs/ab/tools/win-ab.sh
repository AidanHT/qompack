#!/bin/bash
# usage: win-ab.sh <label> <rounds> <bench-regex> <benchtime>
# Interleaves base and head store test binaries round by round; one -count 1 run per variant per round.
P=C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/perfstore
L=$1; R=$2; B=$3; T=$4
cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-perfstore/internal/store || exit 2
: > $P/$L-base.txt; : > $P/$L-head.txt; : > $P/$L-load.txt
for i in $(seq 1 $R); do
  for v in base head; do
    if [ $((i % 2)) -eq 0 ]; then v=$([ $v = base ] && echo head || echo base); fi
    echo "round $i $v $(date -u +%H:%M:%S)" >> $P/$L-load.txt
    powershell -NoProfile -Command "(Get-Counter '\Processor(_Total)\% Processor Time' -SampleInterval 1 -MaxSamples 1).CounterSamples[0].CookedValue" >> $P/$L-load.txt 2>/dev/null
    $P/store-$v.exe -test.run '^$' -test.bench "$B" -test.benchmem -test.benchtime "$T" -test.count 1 -test.timeout 60m > $P/$L-$v-round$i.raw 2>&1
    echo "exit=$?" >> $P/$L-$v-round$i.raw
    grep -E '^Benchmark|^(goos|goarch|pkg|cpu):' $P/$L-$v-round$i.raw >> $P/$L-$v.txt
  done
done
echo done >> $P/$L-load.txt
