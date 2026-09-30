#!/bin/sh
# overnight.sh <candidate-repo> <candidate-sha> <evidence-dir>
# One night's chain on a frozen candidate, strictly sequential, nothing else on the host:
#   1. D28 isolated timing on Windows (win-timing, win-e2e-timing), the container stopped;
#   2. the container started (8 CPUs / 8 GiB caps kept), D28 isolated timing on Linux;
#   3. Linux proof owed by waves 9-10 (pathstest -race x20, the hot-path harness -race x5, the
#      home-isolation guard row -race, wave 15's touched packages -race, the hot-path row under co-load);
#   4. quiet.sh C5.1 and C5.2 on both OSes against the pre-Phase-2 base cf31e01.
# Progress lines go to <evidence-dir>/chain.log. Hold a keep-awake for the whole run.
set -u
C=$1; H=$2; E=$3
here=$(cd "$(dirname "$0")" && pwd)
gate="$C/plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh"
wC=$(cygpath -m "$C"); wE=$(cygpath -m "$E")
mkdir -p "$E"
log() { echo "$* $(date -u +%FT%TZ)" >> "$E/chain.log"; }
lx() { label=$1; shift; sh "$gate" --prefix cx-c3 --repo "$wC" --out "$wE/linux" "$H" "$label" "$@" > "$E/$label-host.log" 2>&1; log "run $label exit=$?"; }

log "start candidate=$H"
docker stop qompack-v6-linux-verification > /dev/null 2>&1
sh "$here/phase3.sh" "$C" "$E" win-timing win-e2e-timing >> "$E/chain.log" 2>&1; log "windows timing exit=$?"

docker start qompack-v6-linux-verification > /dev/null && docker update --cpus 8 --memory 8g --memory-swap 8g qompack-v6-linux-verification > /dev/null; log "container start exit=$?"
sh "$here/phase3.sh" "$C" "$E" linux-timing linux-e2e-timing >> "$E/chain.log" 2>&1; log "linux timing exit=$?"
lx w9-pathstest-race --count 20 -- ./internal/paths/pathstest
lx w10-hotpath-pkg-race --count 5 -- ./test/bench/hotpath
lx w9-guards-home-race --run '^TestGuard_EveryHomeReachingTestPackageIsolatesHome$' -- ./test/guards
lx w15-pkgs-race --timeout 60m -- ./internal/daemon ./internal/negknow ./internal/mcp ./internal/rehydrate ./internal/checkpoint ./internal/store
lx hotpath-coload --no-race --coload --timeout 60m --run '^TestIntegration_HotPathWarmWithRealResidentState$' -- ./test/integration

sh "$here/quiet.sh" "$C" cf31e01 "$E/quiet" c51-win c51-linux c52-win c52-linux >> "$E/chain.log" 2>&1; log "quiet exit=$?"
log "done"
