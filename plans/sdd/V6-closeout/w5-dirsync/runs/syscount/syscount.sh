#!/bin/sh
# Syscall counts per OnToolUse for the w5-dirsync report (SP08-D1, owner decision D20). Runs
# inside a THROWAWAY container:
#
#   docker run --rm -v <host dir>:/src golang:1.26.6-bookworm sh /src/syscount.sh
#
# /src/base.test and /src/new.test are linux/amd64 internal/observer test binaries cross-compiled
# on the host (CGO_ENABLED=0) from base (90e1db3) and new (the w5-dirsync head), each with
# w4-syncs/runs/syscount/syscount_harness_test.go.txt copied in, unchanged;
# /src/tree/internal/observer is their working directory and
# /src/tree/testdata/corpora/toolout/testrunner/go-test-rerun.txt the fixture they read. Every
# (fixture, leased|unleased, side) case is run once under `strace -f`; the lines between the two
# marker stats are counted per syscall name and divided by N (the harness's timed calls).
set -u
N=${N:-5}
apt-get update -qq >/dev/null 2>&1 && apt-get install -y -qq strace >/dev/null 2>&1 ||
	{ echo 'strace install failed' >&2; exit 3; }
mkdir -p /src/out
strace -V | head -1 > /src/out/strace-version.txt
for leased in 1 0; do
	mode=unleased
	test "$leased" = 1 && mode=leased
	for fx in Deduped Delta AllNovel; do
		for side in base new; do
			tag=$fx-$mode-$side
			(cd /src/tree/internal/observer &&
				QP_SYSCOUNT_FIXTURE=$fx QP_SYSCOUNT_LEASED=$leased QP_SYSCOUNT_N=$N GOMAXPROCS=4 \
					strace -f -qq -o "/tmp/$tag.strace" "/src/$side.test" \
					-test.run '^TestSyscallHarnessOnToolUse$' -test.count 1 -test.v > "/src/out/$tag.log" 2>&1)
			echo "exit=$?" >> "/src/out/$tag.log"
			awk -v n="$N" '/qompack-syscount-begin/{on=1; next} /qompack-syscount-end/{on=0} on {
				line=$0; sub(/^[0-9]+ +/, "", line); sub(/^<\.\.\. /, "", line)
				if (line ~ /resumed>/) next
				k=index(line, "("); if (k>1) { c[substr(line,1,k-1)]++ } }
				END { for (s in c) printf "%-16s %8d %10.1f\n", s, c[s], c[s]/n }' "/tmp/$tag.strace" |
				sort > "/src/out/$tag.counts"
		done
	done
done
ls -la /src/out
