#!/bin/sh
# prefreeze.sh <repo> <evidence-dir> [step...]
# prefreeze.sh --e2e-skips <repo>
# D53(a)'s pre-freeze merged-tree check on Windows, strictly sequential, at normal priority (it runs
# only with the owner's go). Hosted ci.yml on the same tree covers Linux and macOS. Steps (default:
# gate e2e hotpath integration testpkgs internal, in that order):
#   gate         build, vet on three OSes, fmt-check, gen-config-docs / gen-mcp-docs / gen-command-docs
#                --check, licenses --check, the lint subset, then govulncheck. A vulnerability or any
#                other govulncheck error fails the gate; a vulnerability database (or module proxy)
#                that cannot be reached does not: gate.log then carries GOVULNCHECK-UNREACHABLE and the
#                tree was NOT checked locally (hosted ci.yml's security job checks the pushed tree).
#   e2e          test/e2e alone, -p 1, no co-load: its wall-clock rows are judged
#   e2efunc      the same without test/e2e's timing rows, which the night judges alone on AC after
#                the freeze, then their purely functional arms by themselves (below); it does not
#                run when those names have drifted from the tree (exit 2)
#   hotpath      test/integration's hot-path rows (TestIntegration_HotPath*) alone, no co-load
#   integration  test/integration without the timing lane's wall-clock row
#                (TestIntegration_HotPathWarmWithRealResidentState, judged alone on AC by the night),
#                so its three functional hot-path rows run here and must each pass; -p 2 with
#                QOMPACK_UNDER_COLOAD=1, as ci.yml's test job runs it
#   testpkgs     the fault/security/platform/release (and other test/) packages, -p 2, co-load declared
#   internal     every internal package, tools/ and cmd/, -p 2, co-load declared
# The strict steps (e2e, e2efunc, hotpath) take back an inherited QOMPACK_UNDER_COLOAD, and no step
# inherits QOMPACK_NONREFERENCE_DISK (a hosted-only declaration).
# Each run starts a fresh summary.log, and PREFREEZE_RUN (default: a UTC stamp and the pid) tags each
# of its lines, so a reader accepts only this run's verdicts. Each step writes <step>.log and
#   start <name> run=<id> power=<reading> <utc>
#   step <name> exit=<code> run=<id> <utc> power=<verdict>
# where <verdict> is power.sh's VALID, INVALID-POWER <events> or NOT-REFERENCE <reason> over the step
# (its start and end readings and the System log's power, standby, sleep and resume events).
#
# test/e2e's timing rows, E2E_NIGHT_ROWS, which e2efunc skips, named exactly. The night judges them
# alone on AC after the freeze: overnight-c8.sh's win-e2e-timing runs all of test/e2e, as ci.yml's
# test-e2e does (ci.yml judges test/e2e's wall-clock rows there: its timing job names none of them),
# and win-x11-alone runs X11 by itself.
#   TestV3_HotPathUnchangedWithLedgerResident          X11 (win-x11-alone's -run names it)
#   TestE2E_SessionStartLatency                        a warm compact start's p99 over 30 spawns
#                                                      (scLatencyP99, 1.5 s); wave 21's e2e seat
#                                                      names its judges as lanes that run test/e2e
#                                                      alone undeclared (scColoadJudges, ab73828a)
#   TestV5_ThrashWarningVisibleInStatusAndCheckpoint   X10: a late first-prompt reply fails its
#                                                      full-mode and degraded arms on a reference
#                                                      disk, and a late recovery reply its late-reply
#                                                      arm (x10v5RecoveryLicence forces only the
#                                                      held prompt), unless a declaration licenses it
# and every test/e2e row ci.yml's timing lane names (phase3.sh's win-timing reads that lane and
# judges it alone on AC), so a row moved there is skipped here by construction.
# E2E_FUNC_ARMS: the subtests of those rows that judge no wall clock, which e2efunc then runs by
# themselves, each by its exact -run pattern, and each must report its own PASS (a pattern that
# matches nothing exits 0):
#   TestV5_ThrashWarningVisibleInStatusAndCheckpoint/state_aware_detector_is_progress_aware_and_cannot_feed_itself
#                    X10's detector arm: an in-memory detector and `qompack config print`
# A named row that test/e2e no longer defines, an arm whose row is not skipped or whose t.Run name
# test/e2e no longer holds, or a timing lane this script cannot read, is drift: e2efunc then does
# not run (exit 2), and --e2e-skips, which c8-night.sh runs among its preconditions, prints the
# reasons and exits 2. Otherwise --e2e-skips prints e2efunc's -skip pattern.
set -u
E2E_NIGHT_ROWS="TestV3_HotPathUnchangedWithLedgerResident TestE2E_SessionStartLatency TestV5_ThrashWarningVisibleInStatusAndCheckpoint"
E2E_FUNC_ARMS="TestV5_ThrashWarningVisibleInStatusAndCheckpoint/state_aware_detector_is_progress_aware_and_cannot_feed_itself"
# e2e_skips <repo>: the -skip pattern on stdout, or the drift on stderr and exit 2.
e2e_skips() {
  es_r=$1; es_bad=0
  es_def() { grep -qE "^func $1\(t \*testing\.T\)" "$es_r"/test/e2e/*_test.go 2> /dev/null; }
  for es_n in $E2E_NIGHT_ROWS; do
    es_def "$es_n" || { echo "prefreeze: test/e2e no longer defines $es_n: E2E_NIGHT_ROWS has drifted from the tree" >&2; es_bad=1; }
  done
  for es_a in $E2E_FUNC_ARMS; do
    case " $E2E_NIGHT_ROWS " in
      *" ${es_a%%/*} "*) ;;
      *) echo "prefreeze: $es_a is an arm of a row e2efunc does not skip: E2E_FUNC_ARMS has drifted" >&2; es_bad=1 ;;
    esac
    grep -qF "t.Run(\"${es_a#*/}\"" "$es_r"/test/e2e/*_test.go 2> /dev/null ||
      { echo "prefreeze: test/e2e no longer holds the arm t.Run(\"${es_a#*/}\", ...): E2E_FUNC_ARMS has drifted from the tree" >&2; es_bad=1; }
  done
  # ci.yml's timing lane, read as phase3.sh reads it: -run '^(A|B|...)$' and its packages.
  es_l=$(grep -E "^ *- run: go test -p 1 " "$es_r/.github/workflows/ci.yml" 2> /dev/null | head -n 1)
  es_p=$(printf '%s' "$es_l" | sed -n -E "s/.*-run '\^\(([^']*)\)\\$'.*/\1/p")
  case $es_p in
    *TestBudgetBF*) ;;
    *) echo "prefreeze: cannot read ci.yml's timing lane (-run '^(...)\$' naming TestBudgetBF): '$es_l'" >&2; es_bad=1 ;;
  esac
  es_x=""
  for es_n in $(printf '%s' "$es_p" | tr '|' ' '); do
    case " $E2E_NIGHT_ROWS " in *" $es_n "*) continue ;; esac
    es_def "$es_n" && es_x="$es_x $es_n"
  done
  [ "$es_bad" = 0 ] || return 2
  printf '^(%s)$\n' "$(echo $E2E_NIGHT_ROWS $es_x | tr ' ' '|')"
}
if [ "${1:-}" = --e2e-skips ]; then
  [ $# -eq 2 ] || { echo "usage: prefreeze.sh --e2e-skips <repo>" >&2; exit 2; }
  e2e_skips "$2"; exit
fi
[ $# -ge 2 ] || { echo "usage: prefreeze.sh <repo> <evidence-dir> [step...]" >&2; exit 2; }
R=$1; E=$2; shift 2
here=$(cd "$(dirname "$0")" && pwd)
. "$here/power.sh"
steps=${*:-gate e2e hotpath integration testpkgs internal}
RUN=${PREFREEZE_RUN:-$(date -u +%Y%m%dT%H%M%SZ)-$$}
case $RUN in *[!A-Za-z0-9._-]*|'') echo "prefreeze.sh: PREFREEZE_RUN may hold only [A-Za-z0-9._-]" >&2; exit 2 ;; esac
unset QOMPACK_NONREFERENCE_DISK
mkdir -p "$E" || exit 2
hpwall='^TestIntegration_HotPathWarmWithRealResidentState$'
hpfunc="DegradesRatherThanBlocks BAPopulationIsTheDaemonHistogram SpoolTransitionJudgedPerMode"
S="$E/summary.log"
: > "$S"
run() { name=$1; shift
  r_t0=$(date +%s); r_p0=$(power_read)
  echo "start $name run=$RUN power=$r_p0 $(date -u +%FT%TZ)" >> "$S"
  (cd "$R" && "$@") > "$E/$name.log" 2>&1
  r_rc=$?
  r_t1=$(date +%s); r_p1=$(power_read)
  if r_ev=$(power_events_since "$r_t0"); then r_ok=1; else r_ok=0; r_ev=""; fi
  r_pv=$(power_verdict "$r_p0" "$r_ok" "$(power_events_window "$r_t0" "$r_t1" "$r_ev")" "$r_p1")
  echo "step $name exit=$r_rc run=$RUN $(date -u +%FT%TZ) power=$r_pv" >> "$S"; }
gate_body() {
  go build ./... && go vet ./... && GOOS=linux go vet ./... && GOOS=darwin go vet ./... &&
    go run ./tools/devtool fmt-check && go run ./tools/devtool gen-config-docs --check &&
    go run ./tools/devtool gen-mcp-docs --check && go run ./tools/devtool gen-command-docs --check &&
    go run ./tools/devtool licenses --check &&
    go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns,coveragefloors ||
    return 1
  vuln_check
}
vuln_check() {
  v_out=$(go run -modfile=tools/pinned/go.mod golang.org/x/vuln/cmd/govulncheck ./... 2>&1); v_rc=$?
  printf '%s\n' "$v_out"
  [ "$v_rc" -eq 0 ] && return 0
  if printf '%s\n' "$v_out" | grep -qE 'Vulnerability #|Your code is affected|vulnerabilit(y|ies) found'; then
    echo "govulncheck: vulnerabilities reported (exit $v_rc)"; return 1
  fi
  if printf '%s\n' "$v_out" | grep -qiE '(vuln\.go\.dev|proxy\.golang\.org|sum\.golang\.org).*(dial tcp|no such host|i/o timeout|connection (refused|reset)|TLS handshake timeout|network is unreachable|context deadline exceeded)|(dial tcp|no such host|lookup).*(vuln\.go\.dev|proxy\.golang\.org|sum\.golang\.org)'; then
    echo "GOVULNCHECK-UNREACHABLE: the vulnerability database or the module proxy could not be reached (exit $v_rc); govulncheck did NOT check this tree. Reported, not fatal: hosted ci.yml's security job runs it on the pushed candidate."
    return 0
  fi
  echo "govulncheck: failed (exit $v_rc) for a reason other than an unreachable database"; return 1
}
integration_body() {
  i_out=$(mktemp) || return 2
  QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout 60m -v -skip "$hpwall" ./test/integration > "$i_out" 2>&1
  i_rc=$?
  cat "$i_out"
  for i_n in $hpfunc; do   # -skip must not have taken a functional row with it
    grep -q "^--- PASS: TestIntegration_HotPath$i_n " "$i_out" ||
      { echo "prefreeze: TestIntegration_HotPath$i_n did not pass (or did not run) in this step"; i_rc=1; }
  done
  rm -f "$i_out"; return "$i_rc"
}
e2efunc_body() {   # run() has already changed into the repository
  ef_skip=$(e2e_skips .) || { echo "prefreeze: e2efunc did not run: its skip list has drifted (above)"; return 2; }
  echo "prefreeze: e2efunc skips test/e2e's timing rows, judged alone on AC by the night: -skip '$ef_skip'"
  env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=1 -timeout 90m -skip "$ef_skip" ./test/e2e
  ef_rc=$?
  ef_out=$(mktemp) || return 2
  for ef_a in $E2E_FUNC_ARMS; do   # each functional arm of a skipped row, by itself
    echo "prefreeze: e2efunc runs ${ef_a%%/*}'s functional arm ${ef_a#*/}"
    env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=1 -timeout 30m -v -run "^${ef_a%%/*}\$/^${ef_a#*/}\$" ./test/e2e > "$ef_out" 2>&1
    ef_arc=$?
    cat "$ef_out"
    [ "$ef_arc" = 0 ] || ef_rc=1
    grep -qE "^ *--- PASS: ${ef_a%%/*}/${ef_a#*/} " "$ef_out" ||
      { echo "prefreeze: $ef_a did not pass (or did not run) in this step"; ef_rc=1; }
  done
  rm -f "$ef_out"; return "$ef_rc"
}
echo "head $(git -C "$R" rev-parse HEAD) go=$(go env GOVERSION) run=$RUN steps=$steps" >> "$S"
for s in $steps; do
  case $s in
    gate) run gate gate_body ;;
    e2e) run e2e env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=1 -timeout 90m ./test/e2e ;;
    e2efunc) run e2efunc e2efunc_body ;;
    hotpath) run hotpath env -u QOMPACK_UNDER_COLOAD go test -p 1 -count=1 -timeout 30m -v -run '^TestIntegration_HotPath' ./test/integration ;;
    integration) run integration integration_body ;;
    testpkgs) run testpkgs env QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout 60m ./test/fault/... ./test/security/... \
                ./test/platform/... ./test/release/... ./test/canary/... ./test/dedup/... ./test/replay/... \
                ./test/guards/... ./test/docs/... ./test/bench/... ;;
    internal) run internal env QOMPACK_UNDER_COLOAD=1 go test -p 2 -count=1 -timeout 60m ./internal/... ./tools/... ./cmd/... ;;
    *) echo "unknown step $s run=$RUN" >> "$S" ;;
  esac
done
echo "done run=$RUN $(date -u +%FT%TZ)" >> "$S"
