#!/bin/sh
# linux-test.sh — run `go test` on an exact commit of this worktree inside the Linux verification
# container, as the unprivileged user, in a fresh /work/cx-rollover-<run-id> directory.
#
#   sh linux-test.sh <ref> <run-id> [--race] -- <go test args...>
#
# The commit is carried in by `git bundle` + `docker cp`, cloned into a new directory (an existing one
# is refused, never reused or deleted), and tested with GOMAXPROCS=4, GOPROXY=off, GOFLAGS=-mod=readonly
# and HOME inside the run directory. --race adds -race with CGO_ENABLED=1. The log goes to
# plans/sdd/V6-closeout/rollover/runs/<run-id>.log with a header (commit, platform, user, command) and a
# trailer with go test's own exit status, never masked by a pipe.
set -eu
export MSYS_NO_PATHCONV=1
container=qompack-v6-linux-verification
user=qompack-test
if test "$#" -lt 3; then
	echo 'usage: linux-test.sh <ref> <run-id> [--race] -- <go test args...>' >&2
	exit 2
fi
ref=$1
run_id=$2
shift 2
race=0
if test "$1" = "--race"; then
	race=1
	shift
fi
test "$1" = "--" || { echo 'expected -- before the go test arguments' >&2; exit 2; }
shift

repo=$(git rev-parse --show-toplevel)
sha=$(git -C "$repo" rev-parse --verify "$ref^{commit}")
dir="/work/cx-rollover-$run_id"
log="$repo/plans/sdd/V6-closeout/rollover/runs/$run_id.log"
if test -e "$log"; then
	echo "refusing to overwrite $log" >&2
	exit 2
fi
if docker exec "$container" test -e "$dir"; then
	echo "refusing to reuse $dir" >&2
	exit 2
fi
# A bundle needs a ref, so the branch tip is bundled and the requested commit (which must be in it)
# is checked out of the clone.
git -C "$repo" merge-base --is-ancestor "$sha" HEAD || { echo "$ref is not in HEAD" >&2; exit 2; }
bundle=$(cygpath -m "$(mktemp -d)")/src.bundle
git -C "$repo" bundle create "$bundle" HEAD
docker exec "$container" mkdir "$dir"
docker cp "$bundle" "$container:$dir/src.bundle" >/dev/null
rm -f "$bundle"
docker exec "$container" sh -c "cd $dir && git clone -q src.bundle src && git -C src checkout -q $sha && \
	(cd src && GOFLAGS=-mod=readonly go mod download) && mkdir home && chown -R $user:$user $dir"

cgo=0
raceflag=
if test "$race" -eq 1; then
	cgo=1
	raceflag=-race
fi
{
	echo "# run: $run_id"
	echo "# commit: $sha"
	echo "# container: $container ($(docker exec "$container" sh -c 'uname -srm; go version' | tr '\n' ' '))"
	echo "# user: $user; GOMAXPROCS=4; CGO_ENABLED=$cgo"
	echo "# started: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "# command: go test $raceflag $*"
} > "$log"
set +e
docker exec -u "$user" -w "$dir/src" \
	-e HOME="$dir/home" -e GOPATH=/go -e GOMODCACHE=/go/pkg/mod -e GOPROXY=off -e GOFLAGS=-mod=readonly \
	-e GOTOOLCHAIN=local -e GOMAXPROCS=4 -e CGO_ENABLED="$cgo" -e TMPDIR=/tmp \
	-e PATH=/usr/local/go/bin:/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
	"$container" go test $raceflag "$@" >> "$log" 2>&1
status=$?
set -e
{
	echo "# finished: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "# exit: $status"
} >> "$log"
echo "exit=$status log=$log"
exit "$status"
