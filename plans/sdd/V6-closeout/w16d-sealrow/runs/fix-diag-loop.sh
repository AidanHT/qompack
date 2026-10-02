#!/bin/sh
# usage: loop.sh <bin> <outdir> <n>
F=C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w16d/sealrow/fix
cd C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16d-sealrow/internal/cli || exit 1
i=1
while [ $i -le $3 ]; do
  timeout 600 "$1" -test.run '^TestPreCompactInSpoolSubmodeSealsTheSpooledReads$' -test.count=1 -test.v -test.coverprofile="$2/c$i.out" > "$2/r$i.log" 2>&1
  echo "run $i exit $?" >> "$2/summary.txt"
  i=$((i+1))
done
echo DONE >> "$2/summary.txt"
