#!/bin/sh
# overnight-c6.sh <candidate-repo> <candidate-sha> <evidence-dir>
# The release candidate's local night (D53), strictly sequential, nothing else on the host.
# Hosted CI on the same commit (ci.yml and nightly.yml on verify/v6) supplies the native-platform
# whole tree, lint, cover, gens/docs/security, replay, plugin-validate, release-dry-run and the fuzz
# matrix; this chain runs what only the reference host can:
#   1. D28 isolated timing on Windows (win-timing, win-e2e-timing, and X11 by itself for D53(d)),
#      our container stopped;
#   2. Windows -race whole tree (C3.3) and reproducible bundles over six targets (C3.11);
#   3. container started (8 CPUs / 8 GiB caps): D28 isolated timing on Linux, then the non-root
#      -race whole tree, -race test/e2e and the product-child race lane (C3.3/C3.4);
#   4. quiet.sh C5.1 on both OSes (C5.2 was taken on candidate 5; wave 16 touched no benchmarked path
#      except the PreCompact replay, which C5.1's B-E row covers).
# Progress lines go to <evidence-dir>/chain.log. Launch through overnight-at.sh (own keep-awake).
set -u
# Retired (wave 22, audit 2 #47): candidate 6's night decided whether it owned the Docker engine from one
# docker ps probe and then stopped an engine it thought it had started, which could stop the owner's
# engine and their stack. Candidate 8 runs c8-night.sh and overnight-c8.sh. Kept as the record of what ran.
echo "overnight-c6.sh: retired: use c8-night.sh / overnight-c8.sh (README.md, Candidate 8)" >&2; exit 2
C=$1; H=$2; E=$3
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$E"
log() { echo "$* $(date -u +%FT%TZ)" >> "$E/chain.log"; }

log "start candidate=$H"
# The owner runs their own containers on this engine (supabase_*_promptly). Stop only our container,
# and stop the engine at the end only if this chain started it.
engine_was_up=0; docker ps > /dev/null 2>&1 && engine_was_up=1; log "engine up at start=$engine_was_up"
docker ps --format "{{.Names}} {{.Status}}" >> "$E/chain.log" 2>&1
docker stop qompack-v6-linux-verification > /dev/null 2>&1
sh "$here/phase3.sh" "$C" "$E" win-timing win-e2e-timing win-x11-alone >> "$E/chain.log" 2>&1; log "windows timing exit=$?"
GOFLAGS=-p=4 sh "$here/phase3.sh" "$C" "$E" win-race bundles >> "$E/chain.log" 2>&1; log "windows race+bundles exit=$?"

docker desktop start > /dev/null 2>&1; docker start qompack-v6-linux-verification > /dev/null && docker update --cpus 8 --memory 8g --memory-swap 8g qompack-v6-linux-verification > /dev/null; log "container start exit=$?"
sh "$here/phase3.sh" "$C" "$E" linux-timing linux-e2e-timing >> "$E/chain.log" 2>&1; log "linux timing exit=$?"
sh "$here/phase3.sh" "$C" "$E" linux-tree linux-e2e linux-child >> "$E/chain.log" 2>&1; log "linux race exit=$?"

sh "$here/quiet.sh" "$C" cf31e01 "$E/quiet" c51-win c51-linux >> "$E/chain.log" 2>&1; log "quiet exit=$?"
# Hand the WSL VM's memory back when this chain started the engine: a running engine holds its page cache
# (12.7 GB after candidate 5's night). An engine the owner had running stays up with their containers.
docker stop qompack-v6-linux-verification > /dev/null 2>&1; log "container stopped exit=$?"
if [ "$engine_was_up" = 0 ]; then docker desktop stop > /dev/null 2>&1; log "engine stopped exit=$?"; else log "engine left running (owner)"; fi
log "done"
