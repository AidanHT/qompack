#!/usr/bin/env bash
# Interleaved A/B driver for the V6 close-out perf work (Windows host).
# usage: ab_win.sh <pkg> <bench-regex> <benchtime> <rounds> <outdir> [extra test flags...]
# Runs base then new, alternating, one -count=1 sample each per round, so host drift and
# co-load hit both sides alike. Each sample's exit code is recorded in the file.
set -u
pkg=$1; bench=$2; bt=$3; rounds=$4; out=$5; shift 5
S=C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad
SRC=C:/Users/Quant/Documents/Programming/Projects/qompack-cx-perfobs/internal/$pkg
mkdir -p "$out"
cd "$SRC" || exit 2
for r in $(seq 1 "$rounds"); do
  for side in base new; do
    "$S/bin/$side/$pkg.test.exe" -test.run '^$' -test.bench "$bench" -test.benchtime "$bt" \
      -test.benchmem -test.count 1 -test.timeout 60m "$@" >> "$out/$pkg-$side.txt" 2>&1
    echo "# round=$r side=$side exit=$? at $(date -u +%H:%M:%S)" >> "$out/$pkg-$side.txt"
  done
done
echo "done $pkg" >> "$out/$pkg-done.txt"
