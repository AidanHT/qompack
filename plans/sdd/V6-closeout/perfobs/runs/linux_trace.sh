#!/bin/sh
# Syscall-time attribution on Linux for the new build: execution trace -> syscall profile.
SUF=$1
W=/work/cx-perfobs-$SUF
A=$W-artifacts/trace
mkdir -p $A
cd $W-new/internal/checkpoint || exit 2
GOMAXPROCS=4 $W-bin/new/checkpoint.test -test.run '^$' -test.bench '^BenchmarkFinalize$' -test.benchtime 5x \
  -test.trace $A/finalize.trace -test.cpuprofile $A/finalize.cpu > $A/finalize-bench.txt 2>&1
echo "exit=$?" >> $A/finalize-bench.txt
go tool trace -pprof=syscall $A/finalize.trace > $A/finalize-syscall.pprof 2>/dev/null
go tool pprof -top -cum -nodecount=40 -focus 'FileWriter..Finalize$' $W-bin/new/checkpoint.test $A/finalize-syscall.pprof > $A/finalize-syscall-top.txt 2>&1
go tool pprof -top -cum -nodecount=40 -focus 'FileWriter..Finalize$' $W-bin/new/checkpoint.test $A/finalize.cpu > $A/finalize-cpu-top.txt 2>&1
cd $W-new/internal/observer || exit 2
GOMAXPROCS=4 $W-bin/new/observer.test -test.run '^$' -test.bench 'BenchmarkOnToolUse_TestOutput256KB/Delta$' -test.benchtime 100x \
  -test.trace $A/delta.trace -test.cpuprofile $A/delta.cpu > $A/delta-bench.txt 2>&1
echo "exit=$?" >> $A/delta-bench.txt
go tool trace -pprof=syscall $A/delta.trace > $A/delta-syscall.pprof 2>/dev/null
go tool pprof -top -cum -nodecount=45 -focus 'onToolUse$' $W-bin/new/observer.test $A/delta-syscall.pprof > $A/delta-syscall-top.txt 2>&1
go tool pprof -top -cum -nodecount=45 -focus 'onToolUse$' $W-bin/new/observer.test $A/delta.cpu > $A/delta-cpu-top.txt 2>&1
echo trace-done > $A/done.txt
