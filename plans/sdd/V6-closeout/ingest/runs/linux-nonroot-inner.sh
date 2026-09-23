#!/bin/sh
# linux-nonroot-inner.sh — the in-container half of the V6 close-out Linux test procedure.
#
# Runs INSIDE the Linux verification container as root, and is normally copied in and invoked by
# linux-nonroot-gate.sh (the host half). It never reuses or deletes an existing /work directory:
# the source clone and the artifact directory must both be new, or it refuses.
#
#   sh linux-nonroot-inner.sh <bundle> <bundle-sha256> <commit-sha> <run-id> [option ...] -- <pkg ...>
#
# Options (all optional, all before the `--`):
#   --run REGEX       go test -run
#   --skip REGEX      go test -skip (named in the report as a deliberate narrowing, never a hide)
#   --timeout DUR     per-binary go test -timeout (default 30m)
#   --count N         go test -count (default 1)
#   --gomaxprocs N    GOMAXPROCS for go and the test binaries (default 4)
#   --coload          set QOMPACK_UNDER_COLOAD=1 (a statement that the run is co-loaded; see
#                     internal/obs/coload.go — it never relaxes what the product did, only wall-clock
#                     judgements, and leaving it off can only make the run stricter)
#   --no-race         run without -race (default is -race with CGO_ENABLED=1)
#   --as-root         run the tests as root instead of the unprivileged user; for comparison runs
#                     only, since root makes every permission fixture vacuous
#   --user NAME       the unprivileged user (default qompack-test, created with uid 10001 if absent)
#   --env K=V         extra environment for the test run (repeatable)
#
# Package arguments are import paths or patterns as `go test` takes them, relative to the module
# root, plus two shorthands: ALL (go list ./...) and ALL-NON-E2E (the same minus ./test/e2e, the
# selection devtool test-race uses).
#
# Artifacts, all under /work/<run-id>-artifacts: identity.txt, packages.txt, test.jsonl (go test
# -json, one record per test event), stderr.log, race.* (GORACE log_path; any non-empty one fails
# the run), per-suite QOMPACK_*_ARTIFACTS dirs, source-status-after.txt, summary.txt/summary.json
# (per-package outcome and every failed case), exit.txt. The exit status is go test's, forced to 1
# when a race log exists or the source tree changed during the run.
set -eu

if test "$#" -lt 4; then
	echo 'usage: linux-nonroot-inner.sh <bundle> <bundle-sha256> <commit-sha> <run-id> [option ...] -- <pkg ...>' >&2
	exit 2
fi
bundle=$1
bundle_sha=$2
commit=$3
run_id=$4
shift 4

run_regex=''
skip_regex=''
timeout=30m
count=1
procs=4
coload=0
race=1
as_root=0
user=qompack-test
extra_env=''
while test "$#" -gt 0; do
	case $1 in
	--run) run_regex=$2; shift 2 ;;
	--skip) skip_regex=$2; shift 2 ;;
	--timeout) timeout=$2; shift 2 ;;
	--count) count=$2; shift 2 ;;
	--gomaxprocs) procs=$2; shift 2 ;;
	--coload) coload=1; shift ;;
	--no-race) race=0; shift ;;
	--as-root) as_root=1; shift ;;
	--user) user=$2; shift 2 ;;
	--env) extra_env="$extra_env $2"; shift 2 ;;
	--) shift; break ;;
	*) echo "unknown option: $1" >&2; exit 2 ;;
	esac
done
if test "$#" -eq 0; then
	echo 'no packages given (use ALL-NON-E2E, ALL, or import paths)' >&2
	exit 2
fi

case $run_id in
*/* | '' | .*) echo "run id must be a plain name: $run_id" >&2; exit 2 ;;
esac
case $commit in
*[!0-9a-f]*) echo "commit must be a full lowercase SHA: $commit" >&2; exit 2 ;;
esac
if test "${#commit}" -ne 40; then
	echo "commit must be a full 40-hex SHA: $commit" >&2
	exit 2
fi

src=/work/$run_id
art=/work/$run_id-artifacts
if test -e "$src" || test -e "$art"; then
	echo "refusing to overwrite $src or $art" >&2
	exit 2
fi

printf '%s  %s\n' "$bundle_sha" "$bundle" | sha256sum -c -
git clone --quiet --no-checkout "$bundle" "$src"
git -C "$src" -c advice.detachedHead=false checkout --quiet --detach "$commit"
test "$(git -C "$src" rev-parse HEAD)" = "$commit"
test -z "$(git -C "$src" status --porcelain)"
tree=$(git -C "$src" rev-parse 'HEAD^{tree}')
mkdir "$art"

if test "$as_root" -eq 1; then
	run_uid=0
	run_home=/root
else
	if ! id -u "$user" >/dev/null 2>&1; then
		useradd --create-home --uid 10001 --shell /bin/sh "$user"
	fi
	run_uid=$(id -u "$user")
	run_home=$(getent passwd "$user" | cut -d: -f6)
	if test "$run_uid" -eq 0; then
		echo "user $user is root; refusing (permission fixtures would be vacuous)" >&2
		exit 2
	fi
	chown -R "$user:" "$src" "$art"
fi

# The module cache is root's and read-only to the test user, so populate it here (a no-op when it is
# already complete) and let the user's go run with the network off: a module the run needs and the
# cache lacks is then a loud failure, not a silent download under a different identity.
(cd "$src" && GOFLAGS=-mod=readonly go mod download)

# The run's whole environment is this file and nothing else: as_user starts from `env -i` and
# sources it, so nothing of root's own environment (HOME=/root, a GOFLAGS, a GOCACHE) leaks in.
envfile=$art/env.sh
: > "$envfile"
# setenv NAME VALUE appends one export, single-quoted so any value survives verbatim.
setenv() {
	quoted=$(printf '%s' "$2" | sed "s/'/'\\\\''/g")
	printf 'export %s='"'"'%s'"'"'\n' "$1" "$quoted" >> "$envfile"
}
setenv HOME "$run_home"
setenv PATH /usr/local/go/bin:/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
setenv GOPATH /go
setenv GOMODCACHE /go/pkg/mod
setenv GOPROXY off
setenv GOFLAGS -mod=readonly
setenv GOTOOLCHAIN local
setenv GOMAXPROCS "$procs"
setenv TMPDIR /tmp
setenv LANG C.UTF-8
if test "$race" -eq 1; then
	setenv CGO_ENABLED 1
	setenv GORACE "halt_on_error=1 log_path=$art/race"
else
	setenv CGO_ENABLED 0
fi
setenv QOMPACK_FAULT_ARTIFACTS "$art/fault"
setenv QOMPACK_SECURITY_ARTIFACTS "$art/security"
setenv QOMPACK_PLATFORM_ARTIFACTS "$art/platform"
setenv QOMPACK_RELEASE_ARTIFACTS "$art/release"
setenv QOMPACK_CANARY_ARTIFACTS "$art/canary"
if test "$coload" -eq 1; then
	setenv QOMPACK_UNDER_COLOAD 1
fi
for kv in $extra_env; do
	setenv "${kv%%=*}" "${kv#*=}"
done
chmod 0644 "$envfile"

as_user() {
	if test "$as_root" -eq 1; then
		(cd "$src" && env -i sh -c '. "$0"; exec "$@"' "$envfile" "$@")
	else
		(cd "$src" && runuser -u "$user" -- env -i sh -c '. "$0"; exec "$@"' "$envfile" "$@")
	fi
}

{
	echo "run_id=$run_id"
	echo "commit=$commit"
	echo "tree=$tree"
	echo "bundle_sha256=$bundle_sha"
	echo "started_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "uid=$(as_user id -u) user=$(as_user id -un) groups=$(as_user id -Gn)"
	echo "race=$race count=$count timeout=$timeout gomaxprocs=$procs coload=$coload"
	echo "run=$run_regex"
	echo "skip=$skip_regex"
	echo "extra_env=$extra_env"
	as_user go version
	as_user go env GOOS GOARCH CGO_ENABLED GOCACHE GOMODCACHE GOFLAGS GOPROXY
	uname -a
	nproc
} > "$art/identity.txt" 2>&1
cat "$art/identity.txt"
if test "$as_root" -eq 0 && test "$(as_user id -u)" -eq 0; then
	echo 'the test identity resolved to uid 0; refusing' >&2
	exit 2
fi

: > "$art/packages.txt"
for p in "$@"; do
	case $p in
	ALL) as_user go list ./... >> "$art/packages.txt" ;;
	ALL-NON-E2E) as_user go list ./... | grep -v '/test/e2e$' >> "$art/packages.txt" ;;
	*) as_user go list "$p" >> "$art/packages.txt" ;;
	esac
done
test -s "$art/packages.txt"
if test "$as_root" -eq 0; then
	chown "$user:" "$art/packages.txt" "$art/identity.txt"
fi

set -- -json -count="$count" -timeout="$timeout"
if test "$race" -eq 1; then
	set -- "$@" -race
fi
if test -n "$run_regex"; then
	set -- "$@" -run "$run_regex"
fi
if test -n "$skip_regex"; then
	set -- "$@" -skip "$skip_regex"
fi

rc=0
# One go test over the whole list, so package binaries run in parallel exactly as devtool test does;
# -json records every case, and the per-package outcome is recovered from it in the summary below.
as_user go test "$@" $(cat "$art/packages.txt") > "$art/test.jsonl" 2> "$art/stderr.log" || rc=$?
echo "go_test_exit=$rc" > "$art/exit.txt"

# Root's git refuses a tree another user owns ("dubious ownership"); the exception is scoped to this
# one command and this one directory, never written to any config.
git -c safe.directory="$src" -C "$src" status --porcelain > "$art/source-status-after.txt" 2>&1 || true
if test -s "$art/source-status-after.txt"; then
	echo 'the source tree changed during the run' | tee -a "$art/exit.txt"
	rc=1
fi
if find "$art" -name 'race.*' -type f -size +0c | grep -q .; then
	echo 'a race detector report was retained' | tee -a "$art/exit.txt"
	rc=1
fi

python3 - "$art" <<'PY' || true
import json, sys, collections
art = sys.argv[1]
pkgs, fails, skips, passes = {}, collections.defaultdict(list), collections.defaultdict(list), collections.Counter()
for line in open(f"{art}/test.jsonl", encoding="utf-8", errors="replace"):
    try:
        e = json.loads(line)
    except ValueError:
        continue
    act, pkg, test = e.get("Action"), e.get("Package", ""), e.get("Test")
    if act not in ("pass", "fail", "skip"):
        continue
    if test is None:
        pkgs[pkg] = act
    elif act == "fail":
        fails[pkg].append(test)
    elif act == "skip":
        skips[pkg].append(test)
    else:
        passes[pkg] += 1
listed = [p.strip() for p in open(f"{art}/packages.txt") if p.strip()]
out = {"packages": {}, "unreported": [p for p in listed if p not in pkgs]}
lines = []
for p in listed:
    st = pkgs.get(p, "NO RESULT")
    out["packages"][p] = {"result": st, "passed": passes[p], "failed": fails[p], "skipped": skips[p]}
    lines.append(f"{st.upper():9} {p}  (pass={passes[p]} fail={len(fails[p])} skip={len(skips[p])})")
    for t in fails[p]:
        lines.append(f"    FAIL {t}")
if out["unreported"]:
    lines.append("UNREPORTED (no package result): " + " ".join(out["unreported"]))
json.dump(out, open(f"{art}/summary.json", "w"), indent=1)
open(f"{art}/summary.txt", "w").write("\n".join(lines) + "\n")
print("\n".join(lines))
PY

echo "finished_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$art/identity.txt"
echo "exit=$rc" >> "$art/exit.txt"
cat "$art/exit.txt"
exit "$rc"
