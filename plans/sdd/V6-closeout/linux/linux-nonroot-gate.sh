#!/bin/sh
# linux-nonroot-gate.sh — run an exact commit's Go tests in the Linux verification container as a
# NON-ROOT user, with -race, and bring every artifact home.
#
# This is the host half of the V6 close-out Linux procedure (the container half is
# linux-nonroot-inner.sh, beside it). It follows the dist/v6-remediation/linux-*.sh pattern — a git
# bundle of one exact commit, cloned into a fresh /work directory, identity-checked, run, artifacts
# and race logs retained — with the one change those scripts lacked: the tests run as an
# unprivileged user, because root passes every "a 0444 file refuses a write" fixture vacuously.
#
#   sh linux-nonroot-gate.sh [host option ...] <commit> <label> [inner option ...] -- <pkg ...>
#
# Host options (before <commit>):
#   --container NAME  default qompack-v6-linux-verification
#   --repo DIR        the repository to bundle from (default: the one containing the cwd)
#   --out DIR         where the artifacts land (default: runs/ beside this script)
#   --prefix NAME     /work directory prefix (default cx-linux)
# <commit> is any revision; it is resolved to its full SHA on the host and the container checks it
# out detached and verifies HEAD and a clean tree before anything runs. <label> names the run.
# Inner options and the package list are linux-nonroot-inner.sh's (see its header): --run, --skip,
# --timeout, --count, --gomaxprocs (default 4), --coload, --no-race, --as-root, --user, --env; and
# packages as import paths/patterns or ALL / ALL-NON-E2E.
#
# Examples:
#   sh linux-nonroot-gate.sh cf31e01 base-failing -- ./internal/ipc ./internal/paths ./test/fault
#   sh linux-nonroot-gate.sh HEAD daemon-race --run '^TestRunReturnsOnly' --count 20 -- ./internal/daemon
#   sh linux-nonroot-gate.sh <candidate> integrated --coload -- ALL-NON-E2E
#
# Exit status: the container run's (go test's, forced to 1 by a retained race log or a source tree
# the run changed), or 2 for a usage/identity refusal. The artifacts are copied home either way.
set -eu

container=qompack-v6-linux-verification
repo=''
out=''
prefix=cx-linux
while test "$#" -gt 0; do
	case $1 in
	--container) container=$2; shift 2 ;;
	--repo) repo=$2; shift 2 ;;
	--out) out=$2; shift 2 ;;
	--prefix) prefix=$2; shift 2 ;;
	--*) echo "unknown host option: $1" >&2; exit 2 ;;
	*) break ;;
	esac
done
if test "$#" -lt 2; then
	sed -n '2,32p' "$0" >&2
	exit 2
fi
rev=$1
label=$2
shift 2
case $label in
*[!A-Za-z0-9._-]* | '') echo "label must be [A-Za-z0-9._-]+: $label" >&2; exit 2 ;;
esac

# Git Bash rewrites any argument that looks like a POSIX path into a Windows one, which would turn
# every container path below into C:/Program Files/Git/work/...; the container paths must pass
# through untouched, so conversion is off and every HOST path is put in Windows form up front.
MSYS_NO_PATHCONV=1
export MSYS_NO_PATHCONV
hostpath() {
	if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi
}

here=$(hostpath "$(cd "$(dirname "$0")" && pwd)")
if test -z "$repo"; then
	repo=$(git rev-parse --show-toplevel)
fi
if test -z "$out"; then
	out=$here/runs
fi
mkdir -p "$out"
out=$(hostpath "$(cd "$out" && pwd)")

sha=$(git -C "$repo" rev-parse --verify "$rev^{commit}")
stamp=$(date -u +%Y%m%dT%H%M%SZ)
run_id=$prefix-$label-$(printf '%s' "$sha" | cut -c1-7)-$stamp

if docker exec "$container" sh -c "test -e /work/$run_id || test -e /work/$run_id-artifacts || test -e /work/$run_id.bundle"; then
	echo "refusing: /work/$run_id already exists in $container" >&2
	exit 2
fi

# A bundle needs a ref, and the commit need not be any branch's tip, so the ref is made in a
# throwaway --shared clone: the repository being tested (and every worktree sharing its refs) is
# never written to.
tmp=$(hostpath "$(mktemp -d)")
trap 'rm -rf "$tmp"' EXIT
git clone --quiet --shared --no-checkout "$repo" "$tmp/clone"
git -C "$tmp/clone" update-ref refs/heads/candidate "$sha"
git -C "$tmp/clone" bundle create "$tmp/$run_id.bundle" refs/heads/candidate 2>/dev/null
bundle_sha=$(sha256sum "$tmp/$run_id.bundle" | cut -d' ' -f1)

docker cp "$tmp/$run_id.bundle" "$container:/work/$run_id.bundle"
docker cp "$here/linux-nonroot-inner.sh" "$container:/work/$run_id.inner.sh"

{
	echo "run_id=$run_id"
	echo "container=$container"
	echo "repo=$repo"
	echo "rev=$rev"
	echo "commit=$sha"
	echo "bundle_sha256=$bundle_sha"
	echo "host_started_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "inner_args=$*"
} > "$tmp/host.txt"
cat "$tmp/host.txt"

rc=0
docker exec "$container" sh "/work/$run_id.inner.sh" "/work/$run_id.bundle" "$bundle_sha" "$sha" "$run_id" "$@" || rc=$?

echo "host_finished_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$tmp/host.txt"
echo "container_exit=$rc" >> "$tmp/host.txt"
if docker exec "$container" test -d "/work/$run_id-artifacts"; then
	docker cp "$container:/work/$run_id-artifacts" "$out/"
	cp "$tmp/host.txt" "$out/$run_id-artifacts/host.txt"
	echo "artifacts: $out/$run_id-artifacts"
else
	cp "$tmp/host.txt" "$out/$run_id.host.txt"
	echo "no artifact directory was created; host record: $out/$run_id.host.txt"
fi
echo "exit=$rc"
exit "$rc"
