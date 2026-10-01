#!/bin/sh
# ci-shape.sh <src> <art> <tag> <n> <pkg...> — run as root in the container; runs the cover job's
# command shape (go test -timeout=30m -coverprofile=... -covermode=atomic <pkg>, NO -count, so the
# test cache and with it -test.testlogfile are on) as the unprivileged user, n times, clearing only
# the test-result cache between runs so each run executes. Temporary diagnostic for w16b-cover.
set -u
src=$1
art=$2
tag=$3
n=$4
shift 4
out=/work/cx-w16b-cover-ci-$tag
mkdir -p "$out"
chown qompack-test: "$out"
i=1
while test "$i" -le "$n"; do
	runuser -u qompack-test -- env -i sh -c '. "$0"; cd "$1"; shift; go clean -testcache; exec "$@"' \
		"$art/env.sh" "$src" go test -timeout=30m -coverprofile="$out/cover-$i.out" -covermode=atomic "$@" \
		> "$out/run-$i.log" 2>&1
	echo "run=$i exit=$?" | tee -a "$out/exits.txt"
	i=$((i + 1))
done
