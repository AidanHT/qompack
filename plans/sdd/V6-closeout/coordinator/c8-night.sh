#!/bin/sh
# c8-night.sh
# Candidate 8's night (D58, D59), launched detached (Start-Process) so Claude Code's background-shell
# reaper cannot stop it. Normal priority, strictly sequential, nothing else measuring:
#   1. keep-awake while it runs (keepawake.ps1; it changes no power setting);
#   2. the pre-freeze check of closeout/integration: gate, e2efunc (test/e2e without X11, which the chain
#      judges on AC), integration (without its hot-path rows, likewise), testpkgs, internal;
#   3. only if every step passes: freeze candidate 8 on verify/v6, build and host-validate qompack-bundles/c8,
#      push verify/v6 (hosted ci.yml) and dispatch nightly.yml (D4);
#   4. overnight-c8.sh on the frozen candidate: AC-gated Windows timing and X11, Windows -race and reproducible
#      bundles, the Linux non-root -race lanes, quiet C5.1 on Windows, and release-check --tag v0.3.0 against a
#      local tag that is deleted afterwards.
# Every decision goes to phase3/c8/night.log; any refusal stops there.
set -u
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../../../.." && pwd)            # .../Projects
V6="$root/qompack-v6"; INT="$root/qompack-cx-int"; CAND="$root/qompack-cx-cand"
E8="$V6/plans/sdd/V6-closeout/phase3/c8"; L="$E8/night.log"
mkdir -p "$E8"
log() { echo "$* $(date -u +%FT%TZ)" >> "$L"; }
sentinel="$E8/keepawake.sentinel"
stop() { log "REFUSED: $*"; rm -f "$sentinel"; exit 1; }
: > "$sentinel"
pwsh -NoProfile -File "$(cygpath -w "$here/keepawake.ps1")" "$(cygpath -w "$sentinel")" > "$E8/keepawake.log" 2>&1 &
log "start pid $$"

H=$(git -C "$INT" rev-parse HEAD)
[ -z "$(git -C "$INT" status --porcelain)" ] || stop "the integration worktree is not clean"
log "pre-freeze on integration $H"
sh "$here/prefreeze.sh" "$INT" "$E8/prefreeze" gate e2efunc integration testpkgs internal
for s in gate e2efunc integration testpkgs internal; do
  grep -q "^step $s exit=0 " "$E8/prefreeze/summary.log" || stop "pre-freeze step $s did not pass"
done
[ "$(git -C "$INT" rev-parse HEAD)" = "$H" ] || stop "integration moved during the pre-freeze check"
log "pre-freeze passes"

cd "$V6" || stop "no verify/v6 worktree"
git merge --no-ff -q -m "chore(v6): freeze close-out candidate 8 (C3.1)" "$H" || { git merge --abort; stop "freeze merge conflicted"; }
SHA=$(git rev-parse HEAD); log "candidate 8 frozen at $SHA (integration $H)"
git -C "$CAND" checkout -q --detach "$SHA" || stop "candidate worktree checkout failed"
[ -z "$(git -C "$CAND" status --porcelain)" ] || stop "candidate worktree not clean"

B="$root/qompack-bundles/c8"
[ -e "$B" ] && stop "$B already exists"
(cd "$CAND" && go run ./tools/devtool bundle -archive -version 0.3.0 -host-validate -out "$(cygpath -m "$B")") > "$E8/host-validate.txt" 2>&1 || stop "bundle build or host validation failed (host-validate.txt)"
for t in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64 windows-amd64 windows-arm64; do
  exe=qompack; case $t in windows-*) exe=qompack.exe ;; esac
  log "bin $t $(sha256sum "$B/qompack-plugin-0.3.0-$t/bin/$exe" | cut -c1-64)"
done
sha256sum "$B/qompack-plugin-0.3.0-windows-amd64/BUNDLE.json" "$B/checksums.txt" >> "$L"
log "bundles built and host-validated in $B"

git -C "$V6" push -q origin verify/v6 >> "$L" 2>&1 && log "pushed verify/v6 $SHA" || log "push failed (hosted CI not started)"
gh workflow run nightly.yml --repo AidanHT/qompack --ref verify/v6 >> "$L" 2>&1 && log "nightly dispatched" || log "nightly dispatch failed"

log "starting overnight-c8.sh"
sh "$here/overnight-c8.sh" "$CAND" "$SHA" "$E8" >> "$L" 2>&1
log "overnight finished exit=$?"
rm -f "$sentinel"
log "done"
