#!/bin/sh
# c8-night.sh
# Candidate 8's night (D58-D61). Launch it detached, so Claude Code's background-shell reaper cannot
# stop it, and only with the owner's go (README.md, "Candidate 8", has the exact launch and abort
# procedure):
#   Start-Process -FilePath 'C:\Program Files\Git\bin\bash.exe' -ArgumentList @('<this file>')
# Normal priority, strictly sequential, nothing else measuring. Every decision goes to
# phase3/c8/night.log (the script also sends its own stdout and stderr there); any refusal stops it.
#   1. keep-awake while it runs (keepawake.ps1; it changes no power setting), released by an EXIT trap
#      on every way out, a refusal included;
#   2. preconditions: qompack-v6 on verify/v6 with no tracked change; qompack-cx-int clean; the tips of
#      closeout/w19-rehydrate, closeout/w19b-cmdconnect and every closeout/w19c-* and w20-* merged into
#      integration (C8_EXEMPT, a space-separated list, names a branch the coordinator deliberately
#      leaves out; each exemption is logged with its tip);
#   3. the MERGED tree (verify/v6 + integration, merged in a scratch clone that cannot push) passes plan
#      lint (runpatterns, docmarkers, coveragefloors), test/guards and test/docs: verify/v6 carries
#      plans integration lacks, and the freeze must not reveal them in release-check hours later;
#   4. the pre-freeze check of integration (prefreeze.sh under a fresh run id, so no earlier run's line
#      can pass it): gate, integration (with its functional hot-path rows), testpkgs, internal, then
#      e2efunc (test/e2e without X11). An e2efunc red whose power verdict is not VALID is re-run once on
#      AC (D57(d): a battery run is neither a pass nor a fail); a VALID red refuses;
#   5. only if all of that passes: freeze candidate 8 on verify/v6 (its tree must equal the tree that was
#      linted), build and host-validate qompack-bundles/c8 (outcome "accepted"; "unverified" means
#      built but not validated, and refuses), push verify/v6 (hosted ci.yml; no prompt, bounded) and,
#      once pushed, dispatch nightly.yml (D4);
#   6. overnight-c8.sh on the frozen candidate; its outcome counts are copied into night.log.
# NIGHT_DEADLINE (local HH:MM, default 08:00) and AC_WAIT_BUDGET_MIN (default 180) pass through to
# overnight-c8.sh; the AC wait spent here counts against the night's one budget.
set -u
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../../../.." && pwd)            # .../Projects
V6="$root/qompack-v6"; INT="$root/qompack-cx-int"; CAND="$root/qompack-cx-cand"
E8="$V6/plans/sdd/V6-closeout/phase3/c8"; L="$E8/night.log"
mkdir -p "$E8" || exit 2
exec >> "$L" 2>&1   # a detached shell owns no usable stdout; stray output and shell errors land here
. "$here/power.sh"
unset QOMPACK_UNDER_COLOAD QOMPACK_NONREFERENCE_DISK
log() { echo "$* $(date -u +%FT%TZ)"; }
winpath() { if command -v cygpath > /dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi; }
NIGHT_DEADLINE=${NIGHT_DEADLINE:-08:00}; AC_WAIT_BUDGET_MIN=${AC_WAIT_BUDGET_MIN:-180}
export NIGHT_DEADLINE AC_WAIT_BUDGET_MIN
PUSH_TIMEOUT_S=300; GH_TIMEOUT_S=120   # a credential prompt or a stalled network must not hold the night
NOPUSH_URL=file:///nonexistent/qompack-c8-night-scratch-clone-never-pushes
sentinel="$E8/keepawake.sentinel"; M=""
cleanup() { rm -f "$sentinel"; if [ -n "$M" ] && [ -d "$M" ]; then rm -rf "$M"; fi; }
trap cleanup EXIT; trap 'exit 130' INT; trap 'exit 143' TERM; trap 'exit 129' HUP
stop() { log "REFUSED: $*"; exit 1; }
case $AC_WAIT_BUDGET_MIN in ''|*[!0-9]*) stop "AC_WAIT_BUDGET_MIN must be a whole number of minutes" ;; esac
deadline=$(deadline_epoch "$NIGHT_DEADLINE") || stop "NIGHT_DEADLINE must be HH:MM, not '$NIGHT_DEADLINE'"
: > "$sentinel"
pwsh -NoProfile -File "$(cygpath -w "$here/keepawake.ps1")" "$(cygpath -w "$sentinel")" > "$E8/keepawake.log" 2>&1 &
log "start pid $$ winpid $(cat "/proc/$$/winpid" 2> /dev/null || echo '?') deadline=$(date -d "@$deadline" +%FT%T) ac_wait_budget=${AC_WAIT_BUDGET_MIN}min power=$(power_read)"

# 2. preconditions
[ "$(git -C "$V6" symbolic-ref -q --short HEAD)" = verify/v6 ] || stop "qompack-v6 is not on branch verify/v6"
[ -z "$(git -C "$V6" status --porcelain --untracked-files=no)" ] || stop "verify/v6 has tracked changes"
[ -z "$(git -C "$INT" status --porcelain)" ] || stop "the integration worktree is not clean"
H=$(git -C "$INT" rev-parse HEAD) || stop "cannot read integration's HEAD"
V=$(git -C "$V6" rev-parse HEAD) || stop "cannot read verify/v6's HEAD"
log "integration $H, verify/v6 $V"
for b in closeout/w19-rehydrate closeout/w19b-cmdconnect $(git -C "$INT" for-each-ref --format='%(refname:short)' 'refs/heads/closeout/w19c-*' 'refs/heads/closeout/w20-*'); do
  t=$(git -C "$INT" rev-parse -q --verify "refs/heads/$b^{commit}") || stop "precondition: branch $b does not exist"
  case " ${C8_EXEMPT:-} " in
    *" $b "*) log "precondition: $b ($t) EXEMPT: left out of candidate 8 by the coordinator (C8_EXEMPT)"; continue ;;
  esac
  git -C "$INT" merge-base --is-ancestor "$t" "$H" || stop "precondition: $b ($t) is not merged into integration $H"
  log "precondition: $b ($t) is merged"
done

# 3. the merged tree
M=$(winpath "$(mktemp -d)") || stop "no scratch directory for the merged-tree check"
log "merged-tree scratch clone $M (removed when the check ends, and by the exit trap)"
merged_tree() {
  git clone -q --no-local --no-checkout --no-tags "$V6" "$M/repo" &&
    git -C "$M/repo" remote set-url origin "$NOPUSH_URL" &&
    git -C "$M/repo" remote set-url --push origin "$NOPUSH_URL" &&
    git -C "$M/repo" -c advice.detachedHead=false checkout -q --detach "$V" &&
    git -C "$M/repo" -c user.name=c8-night -c user.email=c8-night@invalid merge --no-ff -q \
      -m "scratch: candidate 8 merged-tree check" "$H"
}
merged_tree > "$E8/merged-tree.txt" 2>&1 || stop "the merged tree (verify/v6 $V + integration $H) could not be made (merged-tree.txt)"
MT=$(git -C "$M/repo" rev-parse 'HEAD^{tree}') || stop "cannot read the merged tree"
(cd "$M/repo" && go run ./tools/devtool lint --only=runpatterns,docmarkers,coveragefloors && go test -count=1 ./test/guards ./test/docs) \
  >> "$E8/merged-tree.txt" 2>&1 || stop "the merged tree $MT fails plan lint, test/guards or test/docs (merged-tree.txt)"
log "merged tree $MT passes plan lint (runpatterns, docmarkers, coveragefloors), test/guards and test/docs"
rm -rf "$M"; M=""

# 4. the pre-freeze check
PF="$E8/prefreeze"
aside() { # aside <dir>: an earlier run's evidence is kept, never overwritten or read as this run's
  [ -e "$1" ] || return 0
  a_k=1; while [ -e "$1.run-$a_k" ]; do a_k=$((a_k + 1)); done
  mv "$1" "$1.run-$a_k" || stop "cannot move the earlier $1 aside"
  log "an earlier run's $1 moved to $1.run-$a_k"
}
aside "$PF"
RID="c8-$(date -u +%Y%m%dT%H%M%SZ)-$$"
log "pre-freeze run $RID on integration $H"
PREFREEZE_RUN=$RID sh "$here/prefreeze.sh" "$INT" "$PF" gate integration testpkgs internal e2efunc
passed() { grep -q "^step $1 exit=0 run=$2 " "$3/summary.log" 2> /dev/null; }
grep -q 'GOVULNCHECK-UNREACHABLE' "$PF/gate.log" 2> /dev/null &&
  log "WARNING: govulncheck could not reach its database in the gate, so it did NOT check this tree locally (hosted ci.yml's security job checks the pushed candidate)"
for s in gate integration testpkgs internal; do
  passed "$s" "$RID" "$PF" || stop "pre-freeze step $s did not pass in run $RID (prefreeze/$s.log)"
done
if ! passed e2efunc "$RID" "$PF"; then
  pl=$(grep "^step e2efunc exit=[0-9]* run=$RID " "$PF/summary.log" | tail -n 1)
  case $pl in
    "") stop "pre-freeze e2efunc left no verdict in run $RID" ;;
    *" power=VALID") stop "pre-freeze e2efunc failed on AC with no power event, a real red: $pl" ;;
  esac
  log "pre-freeze e2efunc failed without a VALID power record ($pl): D57(d), re-run once on AC"
  NIGHT_AC_WAITED_MIN=0
  power_wait_ac NIGHT_AC_WAITED_MIN "$AC_WAIT_BUDGET_MIN" "$deadline" log ||
    stop "pre-freeze e2efunc: no AC to re-run it on, so its red stays unclassified ($pl)"
  PF2="$E8/prefreeze-e2efunc-ac"; aside "$PF2"
  PREFREEZE_RUN="$RID-ac" sh "$here/prefreeze.sh" "$INT" "$PF2" e2efunc
  pl2=$(grep "^step e2efunc exit=[0-9]* run=$RID-ac " "$PF2/summary.log" 2> /dev/null | tail -n 1)
  passed e2efunc "$RID-ac" "$PF2" || stop "pre-freeze e2efunc failed again on its AC re-run: ${pl2:-no verdict}"
  log "pre-freeze e2efunc passes on its AC re-run: $pl2"
  export NIGHT_AC_WAITED_MIN
fi
[ "$(git -C "$INT" rev-parse HEAD)" = "$H" ] || stop "integration moved during the pre-freeze check"
[ "$(git -C "$V6" rev-parse HEAD)" = "$V" ] || stop "verify/v6 moved during the pre-freeze check"
[ -z "$(git -C "$V6" status --porcelain --untracked-files=no)" ] || stop "verify/v6 has tracked changes"
log "pre-freeze passes"

# 5. freeze, bundles, push
cd "$V6" || stop "no verify/v6 worktree"
git merge --no-ff -q -m "chore(v6): freeze close-out candidate 8 (C3.1)" "$H" || { git merge --abort; stop "freeze merge conflicted"; }
SHA=$(git rev-parse HEAD)
FT=$(git rev-parse 'HEAD^{tree}')
[ "$FT" = "$MT" ] || stop "the frozen tree $FT is not the linted merged tree $MT (verify/v6 holds the unpushed freeze commit $SHA)"
log "candidate 8 frozen at $SHA (integration $H, tree $FT)"
git -C "$CAND" checkout -q --detach "$SHA" || stop "candidate worktree checkout failed"
[ -z "$(git -C "$CAND" status --porcelain)" ] || stop "candidate worktree not clean"

B="$root/qompack-bundles/c8"
[ -e "$B" ] && stop "$B already exists"
(cd "$CAND" && go run ./tools/devtool bundle -archive -version 0.3.0 -host-validate -out "$(winpath "$B")") > "$E8/host-validate.txt" 2>&1 ||
  stop "bundle build or host validation failed (host-validate.txt)"
hv=$(sed -n 's/^ *"outcome": *"\([a-z-]*\)".*/\1/p' "$E8/host-validate.txt" | head -n 1)
[ "$hv" = accepted ] || stop "host validation outcome is '${hv:-none}', not accepted: the bundle is BUILT, not host-validated (host-validate.txt)"
for t in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64 windows-amd64 windows-arm64; do
  exe=qompack; case $t in windows-*) exe=qompack.exe ;; esac
  f="$B/qompack-plugin-0.3.0-$t/bin/$exe"
  [ -f "$f" ] || stop "the bundle lacks $f"
  log "bin $t $(sha256sum "$f" | cut -c1-64)"
done
sha256sum "$B/qompack-plugin-0.3.0-windows-amd64/BUNDLE.json" "$B/checksums.txt" || stop "the bundle lacks BUNDLE.json or checksums.txt"
log "bundles built and host-validated (outcome accepted) in $B"

if GIT_TERMINAL_PROMPT=0 GCM_INTERACTIVE=never timeout -k 30 "$PUSH_TIMEOUT_S" git -C "$V6" push -q origin verify/v6; then
  log "pushed verify/v6 $SHA"
  if GH_PROMPT_DISABLED=1 timeout -k 10 "$GH_TIMEOUT_S" gh workflow run nightly.yml --repo AidanHT/qompack --ref verify/v6; then
    log "nightly dispatched"
  else
    log "nightly dispatch failed exit=$?"
  fi
else
  pr=$?
  if [ "$pr" -eq 124 ]; then log "push timed out after ${PUSH_TIMEOUT_S}s (hosted CI not started, nightly not dispatched)"
  else log "push failed exit=$pr (hosted CI not started, nightly not dispatched)"; fi
fi

# 6. the night
log "starting overnight-c8.sh"
sh "$here/overnight-c8.sh" "$CAND" "$SHA" "$E8"
orc=$?
log "overnight finished exit=$orc: $(cat "$E8/overnight-outcome.txt" 2> /dev/null || echo 'no outcome file: overnight-c8.sh ended early (read chain.log)')"
log "done"
