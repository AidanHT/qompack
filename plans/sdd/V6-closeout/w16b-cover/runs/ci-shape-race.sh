#!/bin/sh
# ci-shape-race.sh <src> <art> <tag> <n> <run-regex> <pkg> — the ubuntu test job's shape
# (go test -race -timeout=30m -json, NO -count, so the test log is on) restricted to <run-regex>,
# as the unprivileged user, n times, clearing only the test-result cache between runs. Temporary
# diagnostic for w16b-cover; <art>/env.sh must come from a -race gate run (CGO_ENABLED=1, GORACE).
set -u
src=$1
art=$2
tag=$3
n=$4
rx=$5
pkg=$6
out=/work/cx-w16b-cover-ci-$tag
mkdir -p "$out"
chown qompack-test: "$out"
i=1
while test "$i" -le "$n"; do
	runuser -u qompack-test -- env -i sh -c '. "$0"; cd "$1"; shift; go clean -testcache; exec "$@"' \
		"$art/env.sh" "$src" go test -race -timeout=30m -json -run "$rx" "$pkg" \
		> "$out/run-$i.jsonl" 2>&1
	echo "run=$i exit=$?" | tee -a "$out/exits.txt"
	i=$((i + 1))
done
