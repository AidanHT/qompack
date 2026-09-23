#!/bin/bash
# usage: win-abn.sh <label> <rounds> <bench-regex> <benchtime> <variant>...
# Runs store-<variant>.exe binaries round-robin, rotating the order every round so no variant always
# runs first; one -test.count 1 run per variant per round.
P=C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/perfstore
L=$1; R=$2; B=$3; T=$4; shift 4; V=("$@"); N=${#V[@]}
cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-perfstore/internal/store || exit 2
: > $P/$L-load.txt
for i in $(seq 1 $R); do
  for j in $(seq 0 $((N-1))); do
    v=${V[$(( (i + j) % N ))]}
    echo "round $i $v $(date -u +%H:%M:%S) cpu% $(powershell -NoProfile -Command "[int](Get-Counter '\Processor(_Total)\% Processor Time' -SampleInterval 1 -MaxSamples 1).CounterSamples[0].CookedValue" 2>/dev/null)" >> $P/$L-load.txt
    $P/store-$v.exe -test.run '^$' -test.bench "$B" -test.benchmem -test.benchtime "$T" -test.count 1 -test.timeout 60m > $P/$L-$v-round$i.raw 2>&1
    echo "exit=$?" >> $P/$L-$v-round$i.raw
  done
done
echo "done $(date -u +%H:%M:%S)" >> $P/$L-load.txt
