#!/bin/sh
# R2 (keep the root) then R3 (cap lifted), the night's failing co-load configuration, on the frozen candidate.
S=C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad
cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-cand || exit 2
export QOMPACK_C116_ROUNDS=30 QOMPACK_C116_WORKERS=8 QOMPACK_C116_READ_BYTES=262144 QOMPACK_C116_FSYNC_COLOAD=16 QOMPACK_C116_CPU_COLOAD=4 GOFLAGS=
for v in r2 r3; do
  case $v in r2) ov=$S/overlay-c116/overlay.json ;; r3) ov=$S/overlay-c116-r3/overlay.json ;; esac
  mkdir -p $S/c116-keep-$v
  export QOMPACK_C116_KEEP=$S/c116-keep-$v
  echo "$(date '+%F %T') start $v" >> $S/c116-repro.summary
  go test ./internal/cli/ -overlay=$ov -run '^TestSessionStartCompact_UnderSameSessionIngest$' -count=1 -v -timeout=30m > $S/c116-$v.log 2>&1
  echo "$(date '+%F %T') $v exit=$? $(grep -E 'reached neither|Reads: [0-9]+ indexed|^(ok|FAIL)' $S/c116-$v.log | tr '\n' ' ')" >> $S/c116-repro.summary
done
