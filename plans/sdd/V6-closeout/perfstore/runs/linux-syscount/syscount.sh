#!/bin/sh
# Runs inside a throwaway golang:1.26.6-bookworm container (docker run --rm).
# /src/base and /src/final are the two trees; the harness is copied into each store package, built
# once per tree, and every op is traced with strace -f. The trace between the two marker stats is
# counted per syscall name and divided by the op count the harness uses (20; search: 1).
set -u
apt-get update -qq >/dev/null 2>&1 && apt-get install -y -qq strace >/dev/null 2>&1
export GOFLAGS=-mod=mod GOMAXPROCS=4 GOTOOLCHAIN=local
for v in base final; do
  cp /src/syscount_harness_test.go /src/$v/internal/store/
  (cd /src/$v && go test -c -o /out/$v.test ./internal/store) || exit 3
done
useradd -m qtest 2>/dev/null
chown -R qtest /out /src
for op in getchunk putcold putwarm search; do
  for v in base final; do
    su qtest -s /bin/sh -c "cd /src/$v/internal/store && QP_SYSCOUNT_OP=$op strace -f -qq -o /out/$op-$v.strace /out/$v.test -test.run '^TestSyscallHarness\$' -test.count 1 > /out/$op-$v.log 2>&1"
    awk '/qompack-syscount-begin/{on=1; next} /qompack-syscount-end/{on=0} on {
           line=$0; sub(/^[0-9]+ +/, "", line); sub(/^<\.\.\. /, "", line)
           if (line ~ /resumed>/) next
           n=index(line, "("); if (n>1) { name=substr(line,1,n-1); c[name]++ } }
         END { for (k in c) print k, c[k] }' /out/$op-$v.strace | sort > /out/$op-$v.counts
  done
done
ls -la /out
