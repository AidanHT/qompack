#!/bin/sh
# ci-shape.sh <src> <art> <tag> <n> <pkg...> — run as root in the container. Runs the cover job's
# command shape (go test -timeout=30m -coverprofile=... -covermode=atomic <pkg>, NO -count, so the
# test cache and -test.testlogfile are on) as the unprivileged user, n times, clearing only the
# test-result cache between runs so each run executes. <src> and <art> are a linux-nonroot-gate.sh
# run's clone and artifact dirs (the env.sh there is reused, with its GOFLAGS reset to the
# module default). Temporary diagnostic for w16e-hpcover, adapted from w16b-cover's.
set -u
src=$1
art=$2
tag=$3
n=$4
shift 4
out=/work/cx-w16e-hpcover-ci-$tag
mkdir -p "$out"
chown qompack-test: "$out"
i=1
while test "$i" -le "$n"; do
	runuser -u qompack-test -- env -i sh -c '. "$0"; export GOFLAGS=-mod=readonly; cd "$1"; shift; go clean -testcache; exec "$@"' \
		"$art/env.sh" "$src" go test -timeout=30m -coverprofile="$out/cover-$i.out" -covermode=atomic "$@" \
		> "$out/run-$i.log" 2>&1
	echo "run=$i exit=$?" | tee -a "$out/exits.txt"
	i=$((i + 1))
done
