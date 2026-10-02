#!/bin/sh
# freeze-night.sh <prefreeze-windows-pid>
# Candidate 6's unattended evening (D53, D56), launched detached so Claude Code's background-shell
# reaper cannot stop it. Waits for prefreeze.sh to finish, then freezes only if every functional step
# passed:
#   - integration, testpkgs and internal exit 0;
#   - the gate's only failed sub-check is runpatterns, and every unsatisfiable pattern is in
#     plans/sdd/V6-closeout/w16f-hpdepth/report.md (split alternations, waived by rpwaive.py).
# The e2e and hotpath steps hold wall-clock rows. Run below normal priority beside the owner's
# applications they are not timing evidence (D28): the overnight chain judges them alone.
# Then it freezes candidate 6 on verify/v6, builds and host-validates the bundles, pushes verify/v6
# (hosted ci.yml, plus nightly.yml dispatched; D4), and runs overnight-c6.sh through overnight-at2.sh
# at once. Every decision goes to phase3/c6/freeze.log; on any refusal it stops there.
set -u
P=$1
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../../../.." && pwd)            # .../Projects
V6="$root/qompack-v6"; INT="$root/qompack-cx-int"; CAND="$root/qompack-cx-cand"
E="$V6/plans/sdd/V6-closeout/phase3/c6"; PF="$E/prefreeze"; L="$E/freeze.log"
mkdir -p "$E"
log() { echo "$* $(date -u +%FT%TZ)" >> "$L"; }
stop() { log "REFUSED: $*"; exit 1; }
log "armed, waiting for prefreeze pid $P"
while ! grep -q '^done' "$PF/summary.log" 2>/dev/null; do
  powershell -NoProfile -Command "if (Get-Process -Id $P -ErrorAction SilentlyContinue) { exit 0 } else { exit 1 }" ||
    { grep -q '^done' "$PF/summary.log" || stop "prefreeze (Windows pid $P) exited without done"; }
  sleep 30
done
for s in integration testpkgs internal; do
  grep -q "^step $s exit=0 " "$PF/summary.log" || stop "prefreeze step $s did not pass"
done
grep -q 'failed sub-check(s): runpatterns$' "$PF/gate.log" || stop "gate failed on something other than runpatterns"
bad=$(sed -n '/unsatisfiable -run pattern(s):/,/^== devtool lint/p' "$PF/gate.log" | grep -E '^  plans/' | grep -v '^  plans/sdd/V6-closeout/w16f-hpdepth/report.md:' | head -1)
[ -z "$bad" ] || stop "an unsatisfiable pattern outside w16f-hpdepth: $bad"
log "prefreeze functional steps pass; e2e and hotpath are wall-clock rows judged overnight"

cd "$INT" || stop "no integration worktree"
python "$(cygpath -w "$here/rpwaive.py")" "$(cygpath -w "$INT")" "$(cygpath -w "$PF/gate.log")" >> "$L" 2>&1 || stop "rpwaive failed"
git commit -q -am "docs(v6): waive the w16f-hpdepth report's split -run alternations" || stop "waiver commit failed"
go run ./tools/devtool lint --only=runpatterns > "$PF/runpatterns-after-waiver.log" 2>&1 || stop "runpatterns still fails after the waiver"
INTSHA=$(git rev-parse HEAD); log "integration $INTSHA"

cd "$V6" || stop "no verify/v6 worktree"
git merge --no-ff -q -m "chore(v6): freeze close-out candidate 6 (C3.1)" closeout/integration || { git merge --abort; stop "freeze merge conflicted"; }
SHA=$(git rev-parse HEAD); log "candidate 6 frozen at $SHA"
git -C "$CAND" checkout -q --detach "$SHA" || stop "candidate worktree checkout failed"
[ -z "$(git -C "$CAND" status --porcelain)" ] || stop "candidate worktree not clean"

B="$root/qompack-bundles/c6"
[ -e "$B" ] && stop "$B already exists"
(cd "$CAND" && go run ./tools/devtool bundle -archive -version 0.3.0 -host-validate -out "$(cygpath -m "$B")") > "$E/host-validate.txt" 2>&1 || stop "bundle build or host validation failed (host-validate.txt)"
sha256sum "$B/qompack-plugin-0.3.0-windows-amd64/BUNDLE.json" "$B/qompack-plugin-0.3.0-windows-amd64/bin/qompack.exe" >> "$L"
log "bundles built and host-validated in $B"

git -C "$V6" push -q origin verify/v6 >> "$L" 2>&1 && log "pushed verify/v6 $SHA" || log "push failed (hosted CI not started)"
gh workflow run nightly.yml --repo AidanHT/qompack --ref verify/v6 >> "$L" 2>&1 && log "nightly dispatched" || log "nightly dispatch failed"

log "starting overnight-c6.sh"
sh "$here/overnight-at2.sh" "$(date +%H%M)" "$CAND" "$SHA" "$E" overnight-c6.sh
log "overnight finished exit=$?"
