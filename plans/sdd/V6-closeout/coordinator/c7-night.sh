#!/bin/sh
# c7-night.sh
# Candidate 7's night (D57), launched detached (Start-Process) so Claude Code's background-shell reaper
# cannot stop it. Normal priority, strictly sequential, nothing else measuring:
#   1. keep-awake while it runs (keepawake.ps1; it changes no power setting);
#   2. D53(d)'s reference experiment e1 (w17-x11win/runs/x11-decisive-v2.sh): the bare harness, X11 and
#      the integration hot-path row, each alone on AC after 5 idle minutes, three rounds, on candidate 6's
#      tree (candidate 7 differs from it in product code only by core.Version's default literal). A run
#      during which the power source changes is INVALID-POWER and its block is retried once;
#   3. the pre-freeze check of closeout/integration: gate, testpkgs, internal. From candidate 6 the tree
#      changes core.Version's default, plugin.json's version, test/fault, test/guards, two workflows,
#      .goreleaser.yaml, docs and plans, so e2e and integration are carried from candidate 6's evidence;
#   4. only if every step passes: freeze candidate 7 on verify/v6, build and host-validate
#      qompack-bundles/c7, compare each bin/ sha256 with w17-release's prediction (bin/ does not depend on
#      the commit), and push verify/v6 (hosted ci.yml, D4). Any refusal stops there.
# Every decision goes to phase3/c7/night.log.
set -u
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../../../.." && pwd)            # .../Projects
V6="$root/qompack-v6"; INT="$root/qompack-cx-int"; CAND="$root/qompack-cx-cand"; X11="$root/qompack-cx-w17-x11win"
P3="$V6/plans/sdd/V6-closeout/phase3"; E7="$P3/c7"; L="$E7/night.log"
mkdir -p "$E7"
log() { echo "$* $(date -u +%FT%TZ)" >> "$L"; }
sentinel="$E7/keepawake.sentinel"
stop() { log "REFUSED: $*"; rm -f "$sentinel"; exit 1; }
: > "$sentinel"
pwsh -NoProfile -File "$(cygpath -w "$here/keepawake.ps1")" "$(cygpath -w "$sentinel")" >> "$L" 2>&1 &
log "start pid $$"

[ -z "$(git -C "$X11" status --porcelain)" ] || stop "the e1 worktree is not clean"
log "e1 on $(git -C "$X11" rev-parse HEAD)"
STOCK="$X11" AC_MAX=100 WAIT_MAX=120 IDLE=300 \
  sh "$INT/plans/sdd/V6-closeout/w17-x11win/runs/x11-decisive-v2.sh" "$P3/c6/x11-e1" e1
log "e1 exit=$? (verdicts in phase3/c6/x11-e1/summary.txt)"

H=$(git -C "$INT" rev-parse HEAD)
[ -z "$(git -C "$INT" status --porcelain)" ] || stop "the integration worktree is not clean"
log "pre-freeze on integration $H"
sh "$here/prefreeze.sh" "$INT" "$E7/prefreeze" gate testpkgs internal
for s in gate testpkgs internal; do
  grep -q "^step $s exit=0 " "$E7/prefreeze/summary.log" || stop "pre-freeze step $s did not pass"
done
log "pre-freeze passes"

cd "$V6" || stop "no verify/v6 worktree"
git merge --no-ff -q -m "chore(v6): freeze close-out candidate 7 (C3.1)" closeout/integration || { git merge --abort; stop "freeze merge conflicted"; }
SHA=$(git rev-parse HEAD); log "candidate 7 frozen at $SHA (integration $H)"
git -C "$CAND" checkout -q --detach "$SHA" || stop "candidate worktree checkout failed"
[ -z "$(git -C "$CAND" status --porcelain)" ] || stop "candidate worktree not clean"

B="$root/qompack-bundles/c7"
[ -e "$B" ] && stop "$B already exists"
(cd "$CAND" && go run ./tools/devtool bundle -archive -version 0.3.0 -host-validate -out "$(cygpath -m "$B")") > "$E7/host-validate.txt" 2>&1 || stop "bundle build or host validation failed (host-validate.txt)"
bad=0
for pair in darwin-amd64:165be7b81de89a42 darwin-arm64:e55d66ba34d9000d linux-amd64:f72206c3403fd06a \
            linux-arm64:553e005493b2466b windows-amd64:ab8046bac3bad894 windows-arm64:c276cc9ec7e9bf6c; do
  t=${pair%%:*}; want=${pair#*:}; exe=qompack; case $t in windows-*) exe=qompack.exe ;; esac
  got=$(sha256sum "$B/qompack-plugin-0.3.0-$t/bin/$exe" | cut -c1-64)
  case $got in "$want"*) log "bin $t $got matches w17-release" ;; *) log "bin $t $got MISMATCH (predicted $want)"; bad=1 ;; esac
done
sha256sum "$B/qompack-plugin-0.3.0-windows-amd64/BUNDLE.json" >> "$L"
[ "$bad" = 0 ] || stop "a bin/ differs from w17-release's prediction"
log "bundles built and host-validated in $B"

git -C "$V6" push -q origin verify/v6 >> "$L" 2>&1 && log "pushed verify/v6 $SHA" || log "push failed (hosted CI not started)"
rm -f "$sentinel"
log "done"
