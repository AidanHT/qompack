#!/bin/sh
# usage: linux-bench.sh <workdir> <label> <count>
set -u
W=$1; L=$2; C=${3:-10}
cd "$W" || exit 2
export GOMAXPROCS=4 GOFLAGS=-mod=mod
mkdir -p out
go test -c -o out/store.test ./internal/store || { echo build-fail; exit 3; }
cd internal/store
B='BenchmarkPutBytes_100KB_(Cold|Warm)(_NoRedact)?$|BenchmarkGetChunk$|BenchmarkOpenSpan_4KB_of_4MB$|BenchmarkSearch_1000Roots$'
{ date -u; cat /proc/loadavg; } > ../../out/$L.env
../../out/store.test -test.run '^$' -test.bench "$B" -test.benchmem -test.count "$C" -test.timeout 60m > ../../out/$L.txt 2>&1
echo "exit=$?" >> ../../out/$L.txt
{ date -u; cat /proc/loadavg; } >> ../../out/$L.env
