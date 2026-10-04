#!/bin/sh
# nightharness.sh [case...]
# A dry harness for candidate 8's night: c8-night.sh, overnight-c8.sh, prefreeze.sh, phase3.sh's
# multi-command arms, power.sh, stamped.sh, c52derive.py and keepawake.ps1. It exercises every
# branch the night can take (AC, battery, a power change, Modern Standby, sleep or resume during a
# step, the wait budget, the deadline, refusals, the release-check clone and its tag, a signal
# mid-run, the C5.2 derivation) with no real night: powershell, pwsh, docker, go, claude, gh,
# timeout, date and sleep are stubs on PATH; git and python are real, on scratch repositories under
# a temporary directory; phase3.sh and quiet.sh are stubs beside the copied night scripts
# (phase3.sh's own arms are run for real against stubs in the P cases). Time is a fake clock: a file
# the date and sleep stubs and stamped.sh's STAMP_CLOCK_FILE seam read, so no case waits on, or
# depends on, the wall clock. The one real process outside the stubs is K1's: keepawake.ps1 under
# the real pwsh with a sentinel that does not exist, which must end without holding anything. It
# never touches the real repository, ~/.claude or ~/.qompack, and starts no Docker, Claude Code or
# Go process.
# Prints PASS/FAIL per case and a total; exits 1 if any case fails. Cases: run with -l to list.
set -u
here=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d) || exit 2
trap 'if [ -n "${NIGHTHARNESS_KEEP:-}" ]; then echo "scratch kept: $T"; else rm -rf "$T"; fi' EXIT
HB="$T/bin"; mkdir -p "$HB"
REALTIMEOUT=$(command -v timeout)
REALPWSH=$(command -v pwsh 2> /dev/null || true)
START=$(date -d "2026-10-03 22:00" +%s)
export HB
# c52derive.py is a native Windows Python, which cannot start the sh stub named "go" by name.
C52_GO="sh $(cygpath -m "$HB/go")"; export C52_GO

# ---- stubs ---------------------------------------------------------------------------------------
cat > "$HB/_lib.sh" <<'EOF'
kv() { _v=$(sed -n "s/^$1=//p" "$SCEN_DIR/kv" 2> /dev/null | tail -n 1); printf '%s' "${_v:-$2}"; }
now() { cat "$FAKE_CLOCK"; }
adv() { echo $(( $(now) + $1 )) > "$FAKE_CLOCK"; }
flip() { echo "$(now) $1" >> "$SCEN_DIR/timeline"; }
nth() { _f="$SCEN_DIR/count.$1"; _n=$(( $(cat "$_f" 2> /dev/null || echo 0) + 1 )); echo "$_n" > "$_f"; echo "$_n"; }
call() { echo "$*" >> "$CALLS"; }
run_script() {
  while IFS= read -r _l || [ -n "$_l" ]; do
    case $_l in
      "adv "*) adv "${_l#adv }" ;;
      "say "*) printf '%s\n' "${_l#say }" ;;
      "flip "*) flip "${_l#flip }" ;;
      block) echo ready > "$SCEN_DIR/ready.fifo"; read _x < "$SCEN_DIR/go.fifo" ;;
      tag) printf 'TAG=%s\n' "$(git describe --tags --exact-match 2>&1)"; printf 'ORIGIN=%s\n' "$(git remote get-url --push origin 2>&1)" ;;
      dirty) echo "left by a try" > left-by-a-try.txt ;;
    esac
  done < "$1"
}
EOF
cat > "$HB/date" <<'EOF'
#!/bin/sh
for a in "$@"; do case $a in -d|-d*|--date|--date=*) exec /usr/bin/date "$@" ;; esac; done
exec /usr/bin/date -d "@$(cat "$FAKE_CLOCK")" "$@"
EOF
cat > "$HB/sleep" <<'EOF'
#!/bin/sh
. "$HB/_lib.sh"
n=${1:-0}; n=${n%%.*}; case $n in ''|*[!0-9]*) n=0 ;; esac
adv "$n"
EOF
cat > "$HB/powershell" <<'EOF'
#!/bin/sh
# Answers power.sh's two queries in their formats, and the 0f8ce75f night's two queries in theirs
# (a plain "AC 77" reading, and "HH:mm:ss AC=True|False" lines since a local time), so the base
# scripts can be run against the same scenarios (red-on-base).
. "$HB/_lib.sh"
s=""; for a in "$@"; do s=$a; done
state() { st=$(awk -v n="$(now)" '($2 == "AC" || $2 == "BAT") && $1 + 0 <= n + 0 { s = $2 } END { print s }' "$SCEN_DIR/timeline"); echo "${st:-AC}"; }
case $s in
  *BatteryStatus*'"POWER '*)
    case $(kv power_mode normal) in
      nobattery) printf 'POWER UNKNOWN nobattery\r\n' ;;
      error) printf 'Get-CimInstance : Invalid class "BatteryStatus"\r\nAt line:2 char:8\r\n' ;;
      garbage) printf 'AC\r\n"quoted" * $x\r\nPOWER AC\r\nPOWER  AC 5\r\n' ;;
      empty) : ;;
      *) printf 'POWER %s 77\r\n' "$(state)" ;;
    esac ;;
  *BatteryStatus*)
    case $(kv power_mode normal) in
      nobattery) printf '%s \r\n' "$(state)" ;;
      error) printf 'Get-CimInstance : Invalid class "BatteryStatus"\r\nAt line:2 char:8\r\nBAT \r\n' ;;
      garbage) printf 'AC\r\n' ;;
      empty) : ;;
      *) printf '%s 77\r\n' "$(state)" ;;
    esac ;;
  *Get-WinEvent*"EVENTS END"*)
    [ "$(kv events_mode normal)" = error ] && { printf 'EVENTS ERROR\r\n'; exit 0; }
    from=$(printf '%s' "$s" | sed -n 's/.*FromUnixTimeSeconds(\([0-9]*\)).*/\1/p' | head -n 1)
    awk -v f="$from" -v n="$(now)" 'NR > 1 && $1 + 0 >= f + 0 && $1 + 0 <= n + 0 {
      if ($2 == "STANDBY" || $2 == "SLEEP" || $2 == "RESUME") print "EVENT " $1 " " $2
      else if ($2 == "AC" || $2 == "BAT") print "EVENT " $1 " AC=" ($2 == "AC" ? 1 : 0) }' "$SCEN_DIR/timeline" | sed 's/$/\r/'
    printf 'EVENTS END\r\n' ;;
  *Get-WinEvent*)
    [ "$(kv events_mode normal)" = error ] && exit 0
    from=$(printf '%s' "$s" | sed -n "s/.*StartTime=\[datetime\]'\([^']*\)'.*/\1/p" | head -n 1)
    f=$(/usr/bin/date -d "$from" +%s 2> /dev/null || echo 0)
    awk -v f="$f" -v n="$(now)" 'NR > 1 && $1 + 0 >= f + 0 && $1 + 0 <= n + 0 && ($2 == "AC" || $2 == "BAT") {
      print $1, ($2 == "AC" ? "True" : "False") }' "$SCEN_DIR/timeline" |
      while read -r e v; do printf '%s AC=%s\r\n' "$(/usr/bin/date -d "@$e" +%H:%M:%S)" "$v"; done ;;
  *) echo "powershell stub: unexpected script" >&2; exit 1 ;;
esac
EOF
cat > "$HB/pwsh" <<'EOF'
#!/bin/sh
. "$HB/_lib.sh"; call "pwsh $*"
EOF
cat > "$HB/gh" <<'EOF'
#!/bin/sh
. "$HB/_lib.sh"; call "gh $*"; exit "$(kv rc_gh 0)"
EOF
cat > "$HB/claude" <<'EOF'
#!/bin/sh
. "$HB/_lib.sh"; call "claude $*"
case "$*" in *"$(kv reject_target __none__)"*) echo "plugin.json: invalid"; exit 1 ;; esac
echo "valid"
EOF
cat > "$HB/timeout" <<'EOF'
#!/bin/sh
. "$HB/_lib.sh"; call "timeout $*"
while :; do case ${1:-} in -k|-s) shift 2 ;; -*) shift ;; *) break ;; esac; done
shift
m=$(kv timeout_match "")
if [ -n "$m" ]; then case " $* " in *"$m"*) call "timeout-expired $*"; exit 124 ;; esac; fi
exec "$@"
EOF
cat > "$HB/docker" <<'EOF'
#!/bin/sh
. "$HB/_lib.sh"; call "docker $*"
case "$1 ${2:-}" in
  "ps "*|"ps") [ "$(nth dockerps)" = "$(kv docker_ps_fail_nth 0)" ] && exit 1   # one transient failure
               [ -e "$SCEN_DIR/engine.up" ] || [ "$(kv docker_ps 0)" = 0 ] || exit 1
               case "$*" in *--format*) echo "owner_db Up 2 hours" ;; esac ;;
  "desktop start") : > "$SCEN_DIR/engine.up" ;;
  "desktop stop") rm -f "$SCEN_DIR/engine.up" ;;
  "start "*) exit "$(kv docker_start 0)" ;;
  *) : ;;
esac
EOF
cat > "$HB/go" <<'EOF'
#!/bin/sh
. "$HB/_lib.sh"
call "go COLOAD=${QOMPACK_UNDER_COLOAD-unset} NONREF=${QOMPACK_NONREFERENCE_DISK-unset} $*"
case "$1 ${2:-}" in
  "env GOVERSION") echo go1.26.6; exit 0 ;;
  "build "*|"vet "*) [ "$(kv block_on "")" = "$1" ] && [ ! -e "$SCEN_DIR/blocked" ] && { : > "$SCEN_DIR/blocked"; echo ready > "$SCEN_DIR/ready.fifo"; read _x < "$SCEN_DIR/go.fifo"; }
                    exit "$(kv "rc_$1" 0)" ;;
esac
if [ "$1" = run ] && [ "${2:-}" = "-modfile=tools/pinned/go.mod" ]; then
  case $(kv govuln ok) in
    ok) echo "No vulnerabilities found."; exit 0 ;;
    vuln) echo "Vulnerability #1: GO-2026-0001"; echo "Your code is affected by 1 vulnerability."; exit 3 ;;
    offline) echo 'govulncheck: fetching vulnerabilities: Get "https://vuln.go.dev/index/db.json.gz": dial tcp: lookup vuln.go.dev: no such host'; exit 1 ;;
    *) echo "govulncheck: internal error: bad flag"; exit 1 ;;
  esac
fi
if [ "$1" = run ] && [ "${2:-}" = ./tools/devtool ]; then
  task=${3:-}; n=$(nth "devtool_$task")
  case $task in
    release-check)
      ec=""; prev=""; for a in "$@"; do [ "$prev" = --evidence-copy ] && ec=$a; prev=$a; done
      sc="$SCEN_DIR/rc.$n"; [ -f "$sc" ] || sc="$SCEN_DIR/rc.default"
      run_script "$sc"
      [ -n "$ec" ] && echo '{"ok": true}' > "$ec"
      exit "$(kv "rc_release_$n" "$(kv rc_release 0)")" ;;
    bundle)
      out=""; prev=""; for a in "$@"; do case $prev in -out|--out) out=$a ;; esac; prev=$a; done
      for t in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64 windows-amd64 windows-arm64; do
        d="$out/qompack-plugin-0.3.0-$t"; mkdir -p "$d/.claude-plugin" "$d/bin"
        echo '{"name": "qompack"}' > "$d/.claude-plugin/plugin.json"
        case $t in windows-*) echo "exe $t" > "$d/bin/qompack.exe" ;; *) echo "elf $t" > "$d/bin/qompack" ;; esac
        echo "{}" > "$d/BUNDLE.json"
      done
      echo sums > "$out/checksums.txt"
      case $out in *"$(kv bundle_differ __none__)") echo "a different byte" >> "$out/checksums.txt" ;; esac
      case "$*" in *-host-validate*) printf '{\n  "kind": "claude-plugin-validate",\n  "outcome": "%s",\n  "go": "go1.26.6"\n}\n' "$(kv hv_outcome accepted)" ;; esac
      exit "$(kv "rc_devtool_bundle_$n" 0)" ;;
    *) adv "$(kv "dur_devtool_$task" 5)"; exit "$(kv "rc_devtool_${task}_$n" "$(kv "rc_devtool_$task" 0)")" ;;
  esac
fi
if [ "$1" = test ] && case " $* " in *" -coverprofile="*) true ;; *) false ;; esac; then
  # c52derive.py's execution trace: a set-mode profile naming the files the benchmark "executes"
  # (kv cov_<Name>, default: its own package's non-test files), and the benchmark's result line.
  prof=""; bench=""; pkg=""; prev=""
  for a in "$@"; do
    case $a in -coverprofile=*) prof=${a#-coverprofile=} ;; ./internal/*) pkg=${a#./} ;; esac
    [ "$prev" = -bench ] && bench=$a; prev=$a
  done
  name=$(printf '%s' "$bench" | sed 's/^\^Benchmark//; s/\$$//')
  n=$(nth "cov_$name"); call "go-cov $name n=$n"
  files=$(kv "cov_$name" "")
  [ -n "$files" ] || files=$(cd "$pkg" 2> /dev/null && ls *.go 2> /dev/null | grep -v '_test\.go$' | sed "s#^#$pkg/#")
  rc=$(kv "rc_cov_$name" "$(kv rc_cov 0)")
  if [ "$rc" = 0 ]; then
    { echo "mode: set"; for f in $files; do echo "github.com/qompack/qompack/$f:1.1,2.2 1 1"; done; } > "$prof"
    [ "$(kv "cov_norun_$name" 0)" = 1 ] || echo "Benchmark$name-8   	       1	      1000 ns/op"
    echo "ok  	github.com/qompack/qompack/$pkg	0.5s"
  else
    echo "FAIL	github.com/qompack/qompack/$pkg	0.5s"
  fi
  exit "$rc"
fi
if [ "$1" = test ]; then
  key=other
  case " $* " in
    *" -fuzz "*) key=fuzz ;;
    *" ./test/e2e "*) key=e2e ;;
    *DegradesRatherThanBlocks*) key=p3hotpath ;;
    *" ./test/integration "*) case " $* " in *" -skip "*) key=integration ;; *) key=hotpath ;; esac ;;
    *" ./test/fault/..."*) key=testpkgs ;;
    *" ./internal/..."*) key=internal ;;
    *" ./test/guards ./test/docs "*) key=mergedtests ;;
    *TestBudgetBF*) key=timinglane ;;
  esac
  n=$(nth "test_$key"); adv "$(kv "dur_$key" 60)"
  sc="$SCEN_DIR/test_$key.$n"; [ -f "$sc" ] && run_script "$sc"
  case $key in integration|p3hotpath)
    for r in DegradesRatherThanBlocks BAPopulationIsTheDaemonHistogram SpoolTransitionJudgedPerMode; do
      [ "$(kv omit_row __none__)" = "$r" ] || echo "--- PASS: TestIntegration_HotPath$r (1.00s)"
    done ;;
  esac
  rc=$(kv "rc_${key}_$n" "$(kv "rc_$key" 0)")
  [ "$rc" = 0 ] && echo "ok  	github.com/qompack/qompack/$key	1.0s" || echo "FAIL	github.com/qompack/qompack/$key	1.0s"
  exit "$rc"
fi
exit "$(kv "rc_go_other" 0)"
EOF
chmod +x "$HB"/*

# Stubs for the scripts the night calls (the night scripts themselves are real copies).
mk_p3_stub() { cat > "$1/phase3.sh" <<'EOF'
#!/bin/sh
. "$HB/_lib.sh"; call "phase3 $*"
repo=$1; ev=$2; shift 2; rc_all=0
for step in "$@"; do
  n=$(nth "p3_$step")
  if [ -e "$ev/p3-$step.json" ] || [ -e "$ev/p3-$step.log" ]; then
    echo "refusing to overwrite p3-$step"; rc=2   # recrun.sh's rule
  else
    sc="$SCEN_DIR/p3.$step.$n"; if [ -f "$sc" ]; then run_script "$sc" > "$ev/p3-$step.log"; else adv "$(kv "dur_$step" 300)"; echo "ran $step" > "$ev/p3-$step.log"; fi
    echo '{"log": "p3-'"$step"'.log"}' > "$ev/p3-$step.json"
    rc=$(kv "rc_${step}_$n" "$(kv "rc_$step" 0)")
  fi
  echo "step $step exit=$rc"; [ "$rc" -ne 0 ] && rc_all=1
done
exit $rc_all
EOF
cat > "$1/quiet.sh" <<'EOF'
#!/bin/sh
# The benchmark list in quiet.sh's own format; c52derive.py reads it from this file.
benches() { cat <<'EOS'
checkpoint Finalize 10x perfobs (SP10-D1)
checkpoint ExtractDecisions 1s D37 1.10.17
daemon SchedulerTap_ObserveTool 1s D37 1.12.17
store PutBytes_100KB_Warm 1s perfstore
cli HookNoop_InProcess 1s D37 1.1.27
cli NotOnTheCandidate 1s listed, never defined
EOS
}
. "$HB/_lib.sh"; call "quiet PKGS=${QUIET_PKGS-unset} FILTER=${QUIET_BENCH_FILTER-unset} $*"
repo=$1; base=$2; ev=$3; shift 3; mkdir -p "$ev"
for step in "$@"; do
  n=$(nth "q_$step")
  [ -e "$ev/$step.json" ] && { echo "refusing to overwrite $step"; exit 1; }
  sc="$SCEN_DIR/q.$step.$n"; if [ -f "$sc" ]; then run_script "$sc" > "$ev/$step.log"; else adv "$(kv "dur_$step" 300)"; echo "ran" > "$ev/$step.log"; fi
  echo '{}' > "$ev/$step.json"
  exit "$(kv "rc_${step}_$n" "$(kv "rc_$step" 0)")"
done
EOF
chmod +x "$1/phase3.sh" "$1/quiet.sh"; }

# ---- scratch world -------------------------------------------------------------------------------
# new_case <name>: a fresh Projects tree with real git repositories laid out as the night expects,
# the night scripts copied beside stub phase3.sh/quiet.sh, a scenario directory and a fake clock.
new_case() {
  W="$T/$1"; P="$W/Projects"; mkdir -p "$P" "$W/scen"
  SCEN_DIR="$W/scen"; FAKE_CLOCK="$W/clock"; CALLS="$W/calls.log"; STAMP_CLOCK_FILE="$FAKE_CLOCK"
  export SCEN_DIR FAKE_CLOCK CALLS STAMP_CLOCK_FILE
  echo "$START" > "$FAKE_CLOCK"; : > "$CALLS"; : > "$SCEN_DIR/kv"; echo "0 AC" > "$SCEN_DIR/timeline"
  mkfifo "$SCEN_DIR/ready.fifo" "$SCEN_DIR/go.fifo"
  G="git -c user.name=harness -c user.email=harness@invalid -c init.defaultBranch=main -c advice.detachedHead=false"
  R="$P/qompack"
  # main is "candidate 7" (C8_PREV_CANDIDATE): a module whose four packages define the benchmarks
  # the quiet.sh stub lists. Two wave branches change product code (checkpoint's intent.go and
  # daemon's scheduler_tap.go), so c52derive.py finds exactly the checkpoint and daemon rows.
  # FROZEN is verify/v6 merged with integration, as c8-night.sh freezes it; qompack-cx-cand sits
  # there for the overnight-only cases.
  {
    $G init -q "$R" && cd "$R" && echo base > base.txt &&
    printf 'module github.com/qompack/qompack\n\ngo 1.26\n' > go.mod &&
    for pf in checkpoint:intent:Finalize:ExtractDecisions daemon:scheduler_tap:SchedulerTap_ObserveTool \
              store:put:PutBytes_100KB_Warm cli:config:HookNoop_InProcess; do
      pk=${pf%%:*}; rest=${pf#*:}; fl=${rest%%:*}; bn=${rest#*:}
      mkdir -p "internal/$pk" && printf 'package %s\n\nfunc f() int { return 1 }\n' "$pk" > "internal/$pk/$fl.go" &&
        printf 'package %s\n\n// other.go is never changed\nfunc g() int { return 2 }\n' "$pk" > "internal/$pk/other.go" &&
        { printf 'package %s\n\nimport "testing"\n' "$pk"; for x in $(echo "$bn" | tr ':' ' '); do printf 'func Benchmark%s(b *testing.B) {}\n' "$x"; done; } > "internal/$pk/bench_test.go" || return 1
    done &&
    $G add . && $G commit -q -m base &&
    $G tag v0.2.0 &&
    $G branch closeout/integration && $G branch verify/v6 &&
    for b in closeout/w19-rehydrate closeout/w19b-cmdconnect closeout/w20-docs; do
      $G checkout -q -b "$b" main && echo "$b" > "${b##*/}.txt" &&
        case $b in
          */w19-rehydrate) printf 'package checkpoint\n\nfunc f() int { return 3 }\n' > internal/checkpoint/intent.go ;;
          */w20-docs) printf 'package daemon\n\nfunc f() int { return 4 }\n' > internal/daemon/scheduler_tap.go ;;
        esac &&
        $G add . && $G commit -q -m "$b" &&
        $G checkout -q closeout/integration && $G merge -q --no-ff -m "merge $b" "$b" || return 1
    done &&
    $G checkout -q verify/v6 && mkdir -p plans && echo ledger > plans/ledger.md && $G add . && $G commit -q -m "v6 plans" &&
    $G checkout -q --detach verify/v6 && $G merge -q --no-ff -m "frozen" closeout/integration &&
    FROZEN=$(git rev-parse HEAD) &&
    $G checkout -q main &&
    $G worktree add -q "$P/qompack-v6" verify/v6 && $G worktree add -q "$P/qompack-cx-int" closeout/integration &&
    $G worktree add -q --detach "$P/qompack-cx-cand" "$FROZEN" &&
    $G init -q --bare "$P/origin.git" && $G remote add origin "$P/origin.git"
  } > "$W/setup.log" 2>&1 || { echo "setup failed for $1 (see $W/setup.log)"; cat "$W/setup.log"; return 1; }
  C8_PREV_CANDIDATE=$(git -C "$R" rev-parse main); export C8_PREV_CANDIDATE
  cd "$W" || return 1
  COORD="$P/qompack-v6/plans/sdd/V6-closeout/coordinator"; mkdir -p "$COORD"
  for f in c8-night.sh overnight-c8.sh prefreeze.sh power.sh stamped.sh recrun.sh keepawake.ps1 c52derive.py; do
    [ -f "$here/$f" ] && cp "$here/$f" "$COORD/"
  done
  mk_p3_stub "$COORD"
  E8="$P/qompack-v6/plans/sdd/V6-closeout/phase3/c8"
  CAND_SHA=$(git -C "$P/qompack-cx-cand" rev-parse HEAD)
  default_rc_script > "$SCEN_DIR/rc.default"
}
kv() { echo "$1=$2" >> "$SCEN_DIR/kv"; }
timeline() { printf '%s\n' "$@" > "$SCEN_DIR/timeline"; }
at() { echo $((START + $1)); }      # at <seconds after the case's start>: an absolute fake epoch
# A release-check's output as devtool prints it: step headers, the shared pass's package lines, then
# the isolated test/e2e pass. @E2E_TEST@ and @E2E_COVER@ mark where a scenario injects lines.
rc_script() {
  sed -e "s#@E2E_TEST@#$1#" -e "s#@E2E_COVER@#$2#" -e "s#@LINT@#$3#" <<'EOF' | tr '|' '\n'
say === release-check: version agreement ===
tag
say release-check: version agreement PASS
say
say === release-check: ci-local lint ===
adv 600
@LINT@
adv 800
say release-check: ci-local lint PASS
say
say === release-check: ci-local test ===
adv 1200
say ok  	github.com/qompack/qompack/internal/store	300.1s
adv 600
say ok  	github.com/qompack/qompack/test/integration	500.2s
@E2E_TEST@
adv 1300
say ok  	github.com/qompack/qompack/test/e2e	1290.5s
say release-check: ci-local test PASS
say
say === release-check: ci-local cover ===
adv 1500
say ok  	github.com/qompack/qompack/internal/store	310.0s	coverage: 85.0% of statements
@E2E_COVER@
adv 1300
say ok  	github.com/qompack/qompack/test/e2e	1300.0s	coverage: 60.1% of statements
say release-check: ci-local cover PASS
say
say === release-check: guards ===
adv 300
say release-check: guards PASS
EOF
}
default_rc_script() { rc_script "adv 1" "adv 1" "adv 1"; }

FAILS=0; NCASES=0; CUR=""
ok() { :; }
check() { # check <description> <command...>
  c_d=$1; shift
  if "$@" > /dev/null 2>&1; then :; else echo "  FAIL [$CUR] $c_d"; CASE_BAD=1; fi
}
has() { grep -qF -- "$2" "$1"; }      # has <file> <fixed text>
hasre() { grep -qE -- "$2" "$1"; }   # hasre <file> <ERE>
lacks() { ! grep -qF -- "$2" "$1"; }
night() { sh "$COORD/c8-night.sh"; }
overnight() { sh "$COORD/overnight-c8.sh" "$P/qompack-cx-cand" "$CAND_SHA" "$W/ev"; }
row() { awk -F'\t' -v s="$1" -v n="$2" '$1 == s && $2 == n { print $4 }' "$W/ev/power.tsv"; }   # row <step> <try>: its verdict

# ---- U: power.sh and stamped.sh ------------------------------------------------------------------
case_U1_power_read_parses() {
  . "$here/power.sh"
  check "AC reading" test "$(power_read)" = "AC 77"
  timeline "0 BAT"; check "BAT reading" test "$(power_read)" = "BAT 77"
  for m in nobattery error garbage empty; do
    kv power_mode "$m"; check "$m reads UNKNOWN" test "$(power_read)" = "UNKNOWN ?"
  done
}
case_U2_power_events() {
  . "$here/power.sh"
  timeline "0 AC" "$(at 10) BAT" "$(at 20) STANDBY" "$(at 30) AC"
  echo "$(at 100)" > "$FAKE_CLOCK"
  ev=$(power_events_since "$(at 15)"); rc=$?
  check "events read" test "$rc" = 0
  check "events since a time" test "$ev" = "$(at 20) STANDBY
$(at 30) AC=1"
  check "window filter" test "$(power_events_window "$(at 25)" "$(at 35)" "$ev")" = "$(at 30) AC=1"
  check "a non-numeric epoch is refused" sh -c '. "$1"; ! power_events_since abc' sh "$here/power.sh"
  kv events_mode error
  check "an unreadable log returns 1" sh -c '. "$1"; ! power_events_since 1' sh "$here/power.sh"
  check "verdict VALID" test "$(power_verdict "AC 50" 1 "")" = VALID
  check "verdict INVALID-POWER" test "$(power_verdict "AC 50" 1 "1 AC=0")" = "INVALID-POWER 1 AC=0"
  check "verdict on battery" test "$(power_verdict "BAT 50" 1 "" | cut -d' ' -f1)" = NOT-REFERENCE
  check "verdict UNKNOWN" test "$(power_verdict "UNKNOWN ?" 1 "" | cut -d' ' -f1)" = NOT-REFERENCE
  check "verdict unreadable log" test "$(power_verdict "AC 50" 0 "" | cut -d' ' -f1)" = NOT-REFERENCE
  check "deadline refuses a bad time" sh -c '. "$1"; ! deadline_epoch 25:00 && ! deadline_epoch 8:00 && ! deadline_epoch ""' sh "$here/power.sh"
  check "deadline is the next 08:00" test "$(deadline_epoch 08:00)" = "$(date -d "2026-10-04 08:00" +%s)"
}
case_U3_power_wait_budget_counts_polls() {
  . "$here/power.sh"
  timeline "0 BAT"; used=0
  lg() { echo "$*" >> "$W/wait.log"; }
  power_wait_ac used 5 "$(at 36000)" lg; rc=$?
  check "the wait gives up" test "$rc" = 1
  check "after exactly the budget's polls" test "$used" = 5
  check "and says why" has "$W/wait.log" "wait budget is spent (5 of 5 min"
  timeline "0 BAT" "$(at 180) AC"; used=0; echo "$START" > "$FAKE_CLOCK"
  power_wait_ac used 60 "$(at 36000)" lg; rc=$?
  check "AC ends the wait" test "$rc" = 0
  check "three polls" test "$used" = 3
  timeline "0 BAT"; used=0; echo "$START" > "$FAKE_CLOCK"
  power_wait_ac used 600 "$(at 120)" lg; rc=$?
  check "the deadline ends the wait" test "$rc" = 1
  check "the deadline is named" has "$W/wait.log" "no AC before the deadline"
}
case_U4_stamped() {
  echo 1791000000 > "$FAKE_CLOCK"
  out=$(sh "$here/stamped.sh" sh -c 'echo one; echo two >&2; printf last; exit 7'); rc=$?
  check "the command's exit status" test "$rc" = 7
  check "every line stamped from the clock seam" test "$(printf '%s\n' "$out" | cut -d' ' -f1 | sort -u)" = 1791000000
  check "a last line without a newline survives" test "$(printf '%s\n' "$out" | tail -n 1 | cut -d' ' -f3-)" = last
  check "stderr merged" test "$(printf '%s\n' "$out" | grep -c two)" = 1
}
case_U5_sleep_resume_and_the_end_reading() {
  . "$here/power.sh"
  check "the query reads Kernel-Power 42 and 107 too" has "$here/power.sh" "Id=105,506,42,107"
  timeline "0 AC" "$(at 10) SLEEP" "$(at 20) RESUME"
  echo "$(at 100)" > "$FAKE_CLOCK"
  ev=$(power_events_since "$(at 5)")
  check "sleep and resume are events" test "$ev" = "$(at 10) SLEEP
$(at 20) RESUME"
  check "a sleep during a run is INVALID-POWER" sh -c 'case "$1" in INVALID-POWER*SLEEP*RESUME*) ;; *) exit 1 ;; esac' sh "$(power_verdict "AC 50" 1 "$ev" "AC 50")"
  check "an end on battery with no event is not VALID" test "$(power_verdict "AC 50" 1 "" "BAT 49" | cut -d' ' -f1)" = NOT-REFERENCE
  check "an unreadable end is not VALID" test "$(power_verdict "AC 50" 1 "" "UNKNOWN ?" | cut -d' ' -f1)" = NOT-REFERENCE
  check "AC at both ends, no event: VALID" test "$(power_verdict "AC 50" 1 "" "AC 51")" = VALID
}

# ---- K: keepawake.ps1 (the real script, under the real pwsh) --------------------------------------
# keepawake.ps1 with a sentinel that does not exist must end without creating it: an early refusal
# deletes the sentinel before pwsh has started, and a script that re-created it would hold the
# machine awake with nothing left to release it. The wait is for an event (the process ends, or the
# sentinel appears), polled; the outer timeout is only a hang guard.
case_K1_keepawake_never_creates_its_sentinel() {
  check "pwsh is installed" test -n "$REALPWSH"
  [ -n "$REALPWSH" ] || return 0
  s="$W/never-created.sentinel"
  "$REALPWSH" -NoProfile -File "$(cygpath -w "$here/keepawake.ps1")" "$(cygpath -w "$s")" > "$W/ka.log" 2>&1 & kp=$!
  "$REALTIMEOUT" 300 sh -c 'while kill -0 "$1" 2> /dev/null && [ ! -e "$2" ]; do /usr/bin/sleep 0.2; done' sh "$kp" "$s"
  created=0; [ -e "$s" ] && created=1
  alive=0; kill -0 "$kp" 2> /dev/null && alive=1
  kill "$kp" 2> /dev/null; wait "$kp" 2> /dev/null; rm -f "$s"
  check "a missing sentinel is never created" test "$created" = 0
  check "it ends by itself" test "$alive" = 0
  check "and says it does not hold" has "$W/ka.log" "not holding"
}

# ---- O: overnight-c8.sh ---------------------------------------------------------------------------
case_O1_all_ac() {
  overnight; rc=$?
  check "exit 0" test "$rc" = 0
  for s in win-timing win-e2e-timing win-x11-alone c51-win c52-win c52-linux release-check linux-timing linux-tree c52-derive; do
    check "$s VALID" test "$(row "$s" 1)" = VALID
  done
  check "outcome counts" has "$W/ev/overnight-outcome.txt" "failed=0 invalid_power=0 not_reference=0 (of which 0 exited non-zero) skipped=0"
  check "release-check saw its tag on HEAD" has "$W/ev/p3-release-check-tag.log" "TAG=v0.3.0"
  check "the clone cannot push" has "$W/ev/p3-release-check-tag.log" "ORIGIN=file:///nonexistent/"
  check "the shared ref store never held v0.3.0" sh -c '! git -C "$1" rev-parse -q --verify refs/tags/v0.3.0' sh "$P/qompack"
  check "the chain says so" has "$W/ev/chain.log" "tag v0.3.0 absent (the chain never tags it)"
  d=$(sed -n 's/^release-check clone \([^ ]*\) at .*/\1/p' "$W/ev/chain.log" | head -n 1)
  check "the clone path is logged" test -n "$d"
  check "the clone is removed" test ! -e "$d"
  check "a JSON record of release-check" test -s "$W/ev/p3-release-check-tag.json"
  check "--evidence-copy written" test -s "$W/ev/release-check.json"
  # D57(e) by construction: the rows whose executed files changed since candidate 7, and only those.
  check "c52-win measures the derived set" has "$CALLS" "quiet PKGS=checkpoint daemon FILTER=^Benchmark(Finalize|ExtractDecisions|SchedulerTap_ObserveTool)\$"
  check "c52-win against cf31e01" hasre "$CALLS" "quiet .* cf31e01 .*/quiet-c52 c52-win"
  check "c52-linux measures the same set" hasre "$CALLS" "quiet PKGS=checkpoint daemon FILTER=\^Benchmark\(Finalize\|ExtractDecisions\|SchedulerTap_ObserveTool\)\\$ .* cf31e01 .*/quiet-c52-linux c52-linux"
  check "the derived set is logged" has "$W/ev/chain.log" "c52: derived set"
  check "the derivation's selection is kept" sh -c '[ "$(tail -n +2 "$1" | wc -l)" = 3 ]' sh "$W/ev/c52-derive/selection.tsv"
  check "an unchanged row is not measured" lacks "$W/ev/c52-derive/selection.tsv" "PutBytes_100KB_Warm"
  check "the container is started for the lanes and again for c52-linux" sh -c '[ "$(grep -c "^docker start qompack-v6-linux-verification" "$1")" = 2 ]' sh "$CALLS"
  check "docker calls are bounded" hasre "$CALLS" "^timeout -k 10 300 docker start qompack-v6-linux-verification"
  check "the owner's engine is left alone" lacks "$CALLS" "docker desktop"
  check "timing steps run before win-race" sh -c 'a=$(grep -n "step win-timing try 1 start" "$1" | cut -d: -f1); b=$(grep -n "step win-race start" "$1" | cut -d: -f1); [ "$a" -lt "$b" ]' sh "$W/ev/chain.log"
  check "windows found in both passes" sh -c '[ "$(grep -c "release-check try 1 WINDOW ci-local-" "$1")" = 2 ]' sh "$W/ev/chain.log"
}
case_O2_transition_retries_with_own_records() {
  printf 'adv 100\nflip BAT\nadv 100\nflip AC\nadv 100\nsay done\n' > "$SCEN_DIR/p3.win-timing.1"
  overnight
  check "try 1 INVALID-POWER" sh -c 'case "$1" in INVALID-POWER*) ;; *) exit 1 ;; esac' sh "$(row win-timing 1)"
  check "try 2 VALID" test "$(row win-timing 2)" = VALID
  check "try 2 really ran (no overwrite refusal)" lacks "$W/ev/chain.log" "refusing to overwrite"
  check "try 1's records moved aside" test -s "$W/ev/win-timing.invalid-power-1/p3-win-timing.json"
  check "try 1's log moved with them" test -s "$W/ev/win-timing.invalid-power-1/p3-win-timing.log"
  check "try 2's record at the canonical id" test -s "$W/ev/p3-win-timing.json"
  check "the INVALID line names the moved records" has "$W/ev/chain.log" "records moved to $W/ev/win-timing.invalid-power-1"
  check "never VALID on battery" sh -c '[ -s "$1" ] && ! grep -qE "VALID power=BAT" "$1"' sh "$W/ev/chain.log"
}
case_O3_battery_night_is_never_valid() {
  timeline "0 BAT"; export AC_WAIT_BUDGET_MIN=5
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "AC-independent work first" sh -c 'a=$(grep -n "step win-race start" "$1" | cut -d: -f1); b=$(grep -n "step win-timing try 1 start" "$1" | cut -d: -f1); [ "$a" -lt "$b" ]' sh "$W/ev/chain.log"
  for s in win-timing win-e2e-timing win-x11-alone c51-win c52-win c52-linux linux-timing linux-e2e-timing; do
    check "$s NOT-REFERENCE" sh -c 'case "$1" in NOT-REFERENCE*) ;; *) exit 1 ;; esac' sh "$(row "$s" 1)"
  done
  check "no step is VALID" sh -c '[ -s "$1" ] && ! awk -F"\t" "\$4 == \"VALID\"" "$1" | grep -q .' sh "$W/ev/power.tsv"
  check "no VALID verdict on battery in chain.log" sh -c '[ -s "$1" ] && ! grep -qE "VALID power=BAT" "$1"' sh "$W/ev/chain.log"
  check "release-check's windows ran on battery" sh -c 'case "$1" in INVALID-POWER*on-battery*) ;; *) exit 1 ;; esac' sh "$(row release-check 1)"
  check "release-check is not retried without AC" test -z "$(row release-check 2)"
  check "and says why" has "$W/ev/chain.log" "not retried: no AC for a second run"
  check "outcome" has "$W/ev/overnight-outcome.txt" "invalid_power=1 not_reference=8"
  check "the budget is named" has "$W/ev/chain.log" "wait budget is spent (5 of 5 min"
}
case_O4_ac_returns_mid_night() {
  # AC returns during bundles (300-600 s), before the Linux timing lanes. Not at 600 s itself: an
  # event in the second a step takes its t0 counts against that step (the window is inclusive).
  timeline "0 BAT" "$(at 590) AC"
  overnight; rc=$?
  check "exit 0" test "$rc" = 0
  for s in win-timing win-e2e-timing win-x11-alone c51-win c52-win release-check; do
    check "$s VALID" test "$(row "$s" 1)" = VALID
  done
  check "queued steps ran after AC came back" sh -c 'a=$(grep -n "step bundles finished exit" "$1" | cut -d: -f1); b=$(grep -n "step win-timing try 1 start" "$1" | cut -d: -f1); [ "$a" -lt "$b" ]' sh "$W/ev/chain.log"
}
case_O5_deadline_skips() {
  timeline "0 BAT"
  export NIGHT_DEADLINE=00:30 AC_WAIT_BUDGET_MIN=600
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "release-check skipped for time" has "$W/ev/chain.log" "step release-check SKIPPED: it needs about 180 min"
  check "queued steps SKIPPED at the deadline" test "$(row win-timing 1)" = SKIPPED
  check "the release-check clone never made" lacks "$W/ev/chain.log" "release-check clone"
  check "no step starts after the deadline" sh -c '! grep -q "try 1 start" "$1"' sh "$W/ev/chain.log"
}
case_O6_release_check_window_transition() {
  rc_script "flip BAT|adv 30|flip AC" "adv 1" "adv 1" > "$SCEN_DIR/rc.1"
  overnight; rc=$?
  check "try 1 INVALID-POWER in ci-local test's e2e pass" sh -c 'case "$1" in "INVALID-POWER ci-local-test:events"*) ;; *) exit 1 ;; esac' sh "$(row release-check 1)"
  check "try 2 VALID" test "$(row release-check 2)" = VALID
  check "exit 0" test "$rc" = 0
  check "try 1's record moved aside" test -s "$W/ev/release-check.invalid-power-1/p3-release-check-tag.json"
  check "try 1's evidence copy moved aside" test -s "$W/ev/release-check.invalid-power-1/release-check.json"
  check "try 2's record" test -s "$W/ev/p3-release-check-tag.json"
}
case_O7_release_check_transition_outside_windows() {
  rc_script "adv 1" "adv 1" "flip BAT|adv 30|flip AC" > "$SCEN_DIR/rc.1"
  overnight; rc=$?
  check "VALID: lint holds no timing judgement" test "$(row release-check 1)" = VALID
  check "not retried" test -z "$(row release-check 2)"
  check "the events are still recorded" hasre "$W/ev/chain.log" "step release-check try 1 exit=0 VALID .*events during the run: \[[0-9]+ AC=0 [0-9]+ AC=1\]"
}
case_O8_unreadable_power_never_crashes() {
  kv power_mode nobattery; kv events_mode error
  export AC_WAIT_BUDGET_MIN=2
  overnight 2> "$W/stderr.txt"; rc=$?
  check "exit 1" test "$rc" = 1
  check "no unbound variable" sh -c '! grep -qi "unbound" "$1" "$2"' sh "$W/stderr.txt" "$W/ev/chain.log"
  check "steps still ran NOT-REFERENCE" sh -c 'case "$1" in NOT-REFERENCE*) ;; *) exit 1 ;; esac' sh "$(row win-timing 1)"
  check "release-check NOT-REFERENCE" sh -c 'case "$1" in NOT-REFERENCE*) ;; *) exit 1 ;; esac' sh "$(row release-check 1)"
  check "the night reached its end" test -s "$W/ev/overnight-outcome.txt"
  : > "$SCEN_DIR/kv"; kv power_mode garbage
  check "garbage power text reads UNKNOWN" sh -c '. "$1"; [ "$(power_read)" = "UNKNOWN ?" ]' sh "$here/power.sh"
}
case_O9_signal_removes_the_clone() {
  rc_script "block" "adv 1" "adv 1" > "$SCEN_DIR/rc.1"
  sh "$COORD/overnight-c8.sh" "$P/qompack-cx-cand" "$CAND_SHA" "$W/ev" & pid=$!
  "$REALTIMEOUT" 600 sh -c 'read x < "$1"' sh "$SCEN_DIR/ready.fifo"
  d=$(sed -n 's/^release-check clone \([^ ]*\) at .*/\1/p' "$W/ev/chain.log" | head -n 1)
  check "the clone existed mid-run" test -d "$d/repo"
  kill -TERM "$pid"; echo go > "$SCEN_DIR/go.fifo"; wait "$pid"; rc=$?
  check "exit 143" test "$rc" = 143
  check "the clone is removed by the trap" test ! -e "$d"
  check "no tag in the shared ref store" sh -c '! git -C "$1" rev-parse -q --verify refs/tags/v0.3.0' sh "$P/qompack"
}
case_O10_standby_invalidates() {
  printf 'adv 100\nflip STANDBY\nadv 100\n' > "$SCEN_DIR/q.c51-win.1"
  overnight
  check "c51-win try 1 INVALID-POWER (standby)" sh -c 'case "$1" in INVALID-POWER*STANDBY*) ;; *) exit 1 ;; esac' sh "$(row c51-win 1)"
  check "c51-win try 2 VALID" test "$(row c51-win 2)" = VALID
  check "try 1's quiet directory moved aside" test -s "$W/ev/c51-win.invalid-power-1/quiet/c51-win.json"
  check "try 2's quiet record" test -s "$W/ev/quiet/c51-win.json"
}
case_O11_engine_and_container() {
  kv docker_ps 1; kv docker_start 1
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "the engine start is bounded" hasre "$CALLS" "^timeout -k 30 660 docker desktop start --timeout 600"
  check "Linux lanes SKIPPED without a container" has "$W/ev/chain.log" "step linux-tree SKIPPED: no container"
  check "the engine this chain started is stopped, bounded" hasre "$CALLS" "^timeout -k 30 360 docker desktop stop --timeout 300"
  check "outcome counts the skips" has "$W/ev/overnight-outcome.txt" "skipped=5"
}
case_O12_existing_record_is_not_reread() {
  mkdir -p "$W/ev"; echo old > "$W/ev/p3-release-check-tag.log"
  overnight 2> "$W/err"; rc=$?
  check "exit 2" test "$rc" = 2
  check "refused before any step, by name" has "$W/err" "already holds a night's records (p3-release-check-tag.log"
  check "the old record is neither read nor touched" sh -c '[ "$(cat "$1")" = old ]' sh "$W/ev/p3-release-check-tag.log"
  check "nothing was written" test ! -e "$W/ev/chain.log"
}
case_O13_rerun_into_same_dir_refused() {
  overnight > /dev/null 2>&1
  cp "$W/ev/chain.log" "$W/chain.run1"; cp "$W/ev/power.tsv" "$W/power.run1"
  overnight 2> "$W/stderr2"; rc=$?
  check "exit 2" test "$rc" = 2
  check "refused by name" has "$W/stderr2" "already holds a night's records"
  check "the earlier chain.log is untouched" cmp -s "$W/ev/chain.log" "$W/chain.run1"
  check "the earlier power.tsv is untouched" cmp -s "$W/ev/power.tsv" "$W/power.run1"
}
evrow() { awk -F'\t' -v s="$2" -v n="$3" '$1 == s && $2 == n { print $4 }' "$1/power.tsv"; }   # evrow <dir> <step> <try>
case_O14_rc_windows_missing_is_not_valid() {
  # (1) release-check passes, but its output has no recognisable section header (format drift): on
  # battery, and no window found, the run cannot be VALID.
  timeline "0 BAT"; export AC_WAIT_BUDGET_MIN=1
  printf 'say --- release-check: ci-local test ---\nadv 5000\nsay ok  \tgithub.com/qompack/qompack/test/e2e\t1290.5s\nadv 4000\n' > "$SCEN_DIR/rc.1"
  overnight
  check "no window found: NOT-REFERENCE" sh -c 'case "$1" in "NOT-REFERENCE AC-sensitive windows not found"*) ;; *) exit 1 ;; esac' sh "$(row release-check 1)"
  # (2) release-check fails in version agreement, before any AC-sensitive window: a VALID failure.
  timeline "0 AC"; echo "$START" > "$FAKE_CLOCK"
  printf 'say === release-check: version agreement ===\nsay release-check: version agreement FAIL\n' > "$SCEN_DIR/rc.2"
  kv rc_release_2 1
  sh "$COORD/overnight-c8.sh" "$P/qompack-cx-cand" "$CAND_SHA" "$W/ev2"
  check "stopped before a window: VALID" sh -c 'case "$1" in VALID*) ;; *) exit 1 ;; esac' sh "$(evrow "$W/ev2" release-check 1)"
  check "counted as a failure" hasre "$W/ev2/overnight-outcome.txt" "failed=1 \[release-check\]"
}
case_O15_owner_engine_never_stopped() {
  kv docker_ps_fail_nth 3      # the first probe at the Linux lanes fails once; the engine was up at start
  overnight
  check "the engine up at the night's start is never stopped" lacks "$CALLS" "docker desktop stop"
  check "nor started" lacks "$CALLS" "docker desktop start"
  check "the lanes ran" has "$W/ev/chain.log" "container start exit=0"
}
case_O16_c52_derivation_failure_falls_back_to_floor() {
  export C8_PREV_CANDIDATE=0000000000000000000000000000000000000000
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "the derivation is a failed step" hasre "$W/ev/chain.log" "step c52-derive finished exit=[1-9]"
  check "the chain says why" has "$W/ev/chain.log" "c52: the derivation produced no selection"
  check "the static floor is measured" has "$CALLS" "quiet PKGS=checkpoint cli config daemon FILTER=^Benchmark(Finalize|AdvanceSegment|ExtractDecisions|StripInjections|Truncate|HookNoop_InProcess|ConfigLoad_ColdNoFiles|FeaturesFrom|ReclaimableIndexBuild_5000Blocks|AssembleCandidates_2000ToolUses|RuntimeEvaluate_2000ToolUses_32Candidates|SchedulerTap_ObserveTool)\$"
}
case_O17_c52_trace_failure_is_selected() {
  kv rc_cov_PutBytes_100KB_Warm 1
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "the derivation fails" hasre "$W/ev/chain.log" "step c52-derive finished exit=1"
  check "the untraced row is measured, fail-closed" has "$CALLS" "quiet PKGS=checkpoint daemon store FILTER=^Benchmark(Finalize|ExtractDecisions|SchedulerTap_ObserveTool|PutBytes_100KB_Warm)\$"
  check "the report says why" has "$W/ev/c52-derive/selection.tsv" "trace failed"
}
case_O22_c52_every_trace_failing_falls_back_to_floor() {
  kv rc_cov 1                          # a broken derivation (a flag, the toolchain), not five broken rows
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "the derivation is a failed step" hasre "$W/ev/chain.log" "step c52-derive finished exit=2"
  check "the static floor is measured, not every listed row" has "$CALLS" "quiet PKGS=checkpoint cli config daemon FILTER=^Benchmark(Finalize|AdvanceSegment|"
  check "the report says why" has "$W/ev/c52-derive/report.txt" "every trace failed"
  check "no selection is written" test ! -e "$W/ev/c52-derive/filter.txt"
}
case_O23_c52_stray_go_file_refused() {
  mkdir -p "$P/qompack-cx-cand/internal/stray" && echo 'package stray' > "$P/qompack-cx-cand/internal/stray/x.go"
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "the derivation refuses" hasre "$W/ev/chain.log" "step c52-derive finished exit=2"
  check "and names the file" has "$W/ev/chain.log" "internal/stray/x.go"
  check "the static floor is measured" has "$CALLS" "quiet PKGS=checkpoint cli config daemon FILTER="
}
case_O18_linux_steps_record_power() {
  printf 'adv 100\nflip BAT\nadv 100\nflip AC\nadv 100\n' > "$SCEN_DIR/p3.linux-timing.1"
  overnight
  check "linux-timing INVALID-POWER" sh -c 'case "$1" in INVALID-POWER*) ;; *) exit 1 ;; esac' sh "$(row linux-timing 1)"
  check "linux-tree's power recorded" test "$(row linux-tree 1)" = VALID
  check "neither a pass nor a fail" has "$W/ev/overnight-outcome.txt" "invalid_power=1"
}
case_O19_standalone_far_deadline_refused() {
  export NIGHT_DEADLINE=21:00          # 23 h after the 22:00 start
  overnight 2> "$W/err"; rc=$?
  check "exit 2" test "$rc" = 2
  check "refused" has "$W/err" "more than 16 h away"
  check "nothing ran" test ! -e "$W/ev/chain.log"
  export NIGHT_ALLOW_FAR=1
  overnight; rc=$?
  check "allowed when asked" test "$rc" = 0
}
case_O20_rc_retry_uses_a_fresh_clone() {
  rc_script "dirty|flip BAT|adv 30|flip AC" "adv 1" "adv 1" > "$SCEN_DIR/rc.1"
  overnight
  check "try 1 INVALID-POWER" sh -c 'case "$1" in INVALID-POWER*) ;; *) exit 1 ;; esac' sh "$(row release-check 1)"
  check "try 2 VALID" test "$(row release-check 2)" = VALID
  check "try 2 started clean" has "$W/ev/p3-release-check-tag.json" '"source_dirty": ""'
  check "a clone per try" sh -c '[ "$(grep -c "^release-check clone " "$1")" = 2 ]' sh "$W/ev/chain.log"
  for d in $(sed -n 's/^release-check clone \([^ ]*\) at .*/\1/p' "$W/ev/chain.log"); do check "clone $d removed" test ! -e "$d"; done
}
case_O21_rc_not_retried_on_battery() {
  timeline "0 BAT"; export AC_WAIT_BUDGET_MIN=600
  overnight
  check "try 1 INVALID-POWER on battery" sh -c 'case "$1" in INVALID-POWER*on-battery*) ;; *) exit 1 ;; esac' sh "$(row release-check 1)"
  check "no second try on battery" sh -c '! grep -q "step release-check try 2 start power=BAT" "$1"' sh "$W/ev/chain.log"
}

# ---- N: c8-night.sh ------------------------------------------------------------------------------
case_N1_happy_night() {
  night; rc=$?
  L8="$E8/night.log"
  check "exit 0" test "$rc" = 0
  for b in closeout/w19-rehydrate closeout/w19b-cmdconnect closeout/w20-docs; do check "$b checked" has "$L8" "precondition: $b ("; done
  check "merged tree checked" has "$L8" "passes plan lint (runpatterns, docmarkers, coveragefloors), test/guards and test/docs"
  check "merged-tree lint ran" hasre "$CALLS" "go .* run ./tools/devtool lint --only=runpatterns,docmarkers,coveragefloors"
  check "pre-freeze passes" has "$L8" "pre-freeze passes"
  check "frozen" has "$L8" "candidate 8 frozen at"
  check "host-validated (accepted)" has "$L8" "host-validated (outcome accepted)"
  check "pushed, bounded, no prompt" hasre "$CALLS" "^timeout -k 30 300 git -C .* push -q origin verify/v6"
  check "origin has the frozen commit" test "$(git -C "$P/origin.git" rev-parse verify/v6)" = "$(git -C "$P/qompack-v6" rev-parse HEAD)"
  check "nightly dispatched" has "$L8" "nightly dispatched"
  check "overnight outcome in night.log" hasre "$L8" "overnight finished exit=0: steps=[0-9]+ passed=[0-9]+ failed=0"
  check "keep-awake released" test ! -e "$E8/keepawake.sentinel"
  check "summary lines carry the run id" hasre "$E8/prefreeze/summary.log" "^step gate exit=0 run=c8-[0-9TZ]+-[0-9]+ "
  check "integration under co-load" hasre "$CALLS" "^go COLOAD=1 .* test -p 2 .* -v -skip \^TestIntegration_HotPathWarmWithRealResidentState\\$ ./test/integration"
  check "testpkgs under co-load" hasre "$CALLS" "^go COLOAD=1 .* ./test/fault/"
  check "internal under co-load" hasre "$CALLS" "^go COLOAD=1 .* ./internal/\.\.\."
  check "e2efunc strict" hasre "$CALLS" "^go COLOAD=unset .* -skip \^TestV3_HotPath ./test/e2e"
  for g in gen-command-docs licenses; do check "gate runs $g" hasre "$CALLS" "run ./tools/devtool $g --check"; done
  check "gate runs govulncheck" has "$CALLS" "golang.org/x/vuln/cmd/govulncheck ./..."
}
case_N2_unmerged_branch_refuses() {
  git -C "$P/qompack" branch closeout/w20-late main > /dev/null 2>&1
  git -c user.name=h -c user.email=h@i -C "$P/qompack" worktree add -q "$W/late" closeout/w20-late > /dev/null 2>&1
  (cd "$W/late" && echo late > late.txt && git add . && git -c user.name=h -c user.email=h@i commit -q -m late)
  v0=$(git -C "$P/qompack-v6" rev-parse HEAD)
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "refused by name" hasre "$E8/night.log" "REFUSED: precondition: closeout/w20-late \([0-9a-f]+\) is not merged"
  check "nothing frozen" test "$(git -C "$P/qompack-v6" rev-parse HEAD)" = "$v0"
  check "no pre-freeze ran" test ! -e "$E8/prefreeze"
  check "keep-awake released" test ! -e "$E8/keepawake.sentinel"
}
case_N3_exempt_branch_proceeds() {
  git -C "$P/qompack" branch closeout/w20-late main > /dev/null 2>&1
  git -c user.name=h -c user.email=h@i -C "$P/qompack" worktree add -q "$W/late" closeout/w20-late > /dev/null 2>&1
  (cd "$W/late" && echo late > late.txt && git add . && git -c user.name=h -c user.email=h@i commit -q -m late)
  export C8_EXEMPT="closeout/w20-late"
  night; rc=$?
  check "exit 0" test "$rc" = 0
  check "the exemption is logged" has "$E8/night.log" "closeout/w20-late"
  check "as EXEMPT" has "$E8/night.log" "EXEMPT: left out of candidate 8 by the coordinator"
}
case_N4_missing_branch_refuses() {
  git -C "$P/qompack" branch -D closeout/w19b-cmdconnect > /dev/null 2>&1
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "refused" has "$E8/night.log" "REFUSED: precondition: branch closeout/w19b-cmdconnect does not exist"
}
case_N5_stale_prefreeze_pass_cannot_satisfy() {
  mkdir -p "$E8/prefreeze"
  printf 'step gate exit=0 run=old 2026-10-01T00:00:00Z power=VALID\nstep gate exit=0 2026-10-01T00:00:00Z\n' > "$E8/prefreeze/summary.log"
  kv rc_build 1
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "this run's gate failure refuses" has "$E8/night.log" "REFUSED: pre-freeze step gate did not pass in run c8-"
  check "the earlier run kept aside" has "$E8/prefreeze.run-1/summary.log" "run=old"
  check "this run's summary exists" hasre "$E8/prefreeze/summary.log" "run=c8-"
  check "this run's summary is fresh" lacks "$E8/prefreeze/summary.log" "run=old"
  check "nothing frozen" lacks "$E8/night.log" "frozen at"
}
case_N6_merged_tree_lint_refuses() {
  kv rc_devtool_lint 1
  v0=$(git -C "$P/qompack-v6" rev-parse HEAD)
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "refused" hasre "$E8/night.log" "REFUSED: the merged tree [0-9a-f]+ fails plan lint"
  check "nothing frozen" test "$(git -C "$P/qompack-v6" rev-parse HEAD)" = "$v0"
  check "keep-awake released" test ! -e "$E8/keepawake.sentinel"
}
case_N7_unverified_host_validation_refuses() {
  kv hv_outcome unverified
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "refused" has "$E8/night.log" "REFUSED: host validation outcome is 'unverified', not accepted"
  check "nothing pushed" sh -c '! git -C "$1" rev-parse -q --verify refs/heads/verify/v6' sh "$P/origin.git"
}
case_N8_push_timeout_is_bounded() {
  kv timeout_match "push -q origin"
  night; rc=$?
  check "the timeout is reported" has "$E8/night.log" "push timed out after 300s (hosted CI not started, nightly not dispatched)"
  check "no dispatch without the push" lacks "$CALLS" "gh workflow run"
  check "the night goes on" has "$E8/night.log" "starting overnight-c8.sh"
}
case_N9_signal_releases_keepawake() {
  kv block_on build
  sh "$COORD/c8-night.sh" & pid=$!
  "$REALTIMEOUT" 600 sh -c 'read x < "$1"' sh "$SCEN_DIR/ready.fifo"
  check "the sentinel exists mid-run" test -e "$E8/keepawake.sentinel"
  kill -TERM "$pid"; echo go > "$SCEN_DIR/go.fifo"; wait "$pid"; rc=$?
  check "exit 143" test "$rc" = 143
  check "keep-awake released by the EXIT trap" test ! -e "$E8/keepawake.sentinel"
}
case_N10_battery_e2efunc_red_reruns_on_ac() {
  timeline "0 BAT" "$(at 9000) AC"; kv rc_e2e_1 1
  night; rc=$?
  check "exit 0" test "$rc" = 0
  check "classified, not refused" has "$E8/night.log" "pre-freeze e2efunc failed without a VALID power record"
  check "re-run on AC passes" has "$E8/night.log" "pre-freeze e2efunc passes on its AC re-run"
  check "the re-run's own evidence" hasre "$E8/prefreeze-e2efunc-ac/summary.log" "^step e2efunc exit=0 run=c8-.*-ac .* power=VALID"
  check "frozen" has "$E8/night.log" "candidate 8 frozen at"
}
case_N11_valid_e2efunc_red_refuses() {
  kv rc_e2e 1
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "a real red refuses" has "$E8/night.log" "REFUSED: pre-freeze e2efunc failed on AC with no power event, a real red"
}
case_N12_tracked_change_refuses() {
  echo dirty >> "$P/qompack-v6/plans/ledger.md"
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "refused" has "$E8/night.log" "REFUSED: verify/v6 has tracked changes"
}
case_N13_early_refusal_stops_keepawake() {
  # pwsh here never returns, as keepawake.ps1 holds while its sentinel exists; the night refuses
  # within its first checks, before a real pwsh would even have started.
  mkdir -p "$W/bin2"
  cat > "$W/bin2/pwsh" <<'EOS'
#!/bin/sh
. "$HB/_lib.sh"; call "pwsh $*"
echo $$ > "$SCEN_DIR/pwsh.pid"
exec /usr/bin/sleep 3600
EOS
  chmod +x "$W/bin2/pwsh"; PATH="$W/bin2:$PATH"; export PATH
  echo dirty >> "$P/qompack-v6/plans/ledger.md"
  night
  check "refused" has "$E8/night.log" "REFUSED: verify/v6 has tracked changes"
  kp=$(sed -n 's/.*keep-awake pid \([0-9][0-9]*\).*/\1/p' "$E8/night.log" | head -n 1)
  check "the keep-awake process is logged" test -n "$kp"
  if [ -n "$kp" ]; then
    "$REALTIMEOUT" 120 sh -c 'while kill -0 "$1" 2> /dev/null; do /usr/bin/sleep 0.2; done' sh "$kp"
    check "the exit trap stops it" sh -c '! kill -0 "$1" 2> /dev/null' sh "$kp"
  fi
  check "the sentinel is gone" test ! -e "$E8/keepawake.sentinel"
  sp=$(cat "$SCEN_DIR/pwsh.pid" 2> /dev/null); [ -n "$sp" ] && kill "$sp" 2> /dev/null   # never leave the stub behind
  return 0
}
case_N14_deadline_epoch_passes_to_overnight() {
  export NIGHT_DEADLINE=22:10
  kv dur_e2e 900                       # e2efunc ends the pre-freeze at about 22:20, after the deadline
  night
  check "the night's deadline" has "$E8/night.log" "deadline=2026-10-03T22:10:00"
  check "overnight keeps it" hasre "$E8/chain.log" "^start candidate=.* deadline=2026-10-03T22:10:00"
  check "no step starts after it" sh -c '[ -s "$1" ] && ! grep -q "try 1 start" "$1"' sh "$E8/chain.log"
  check "every step SKIPPED" has "$E8/overnight-outcome.txt" "passed=0 failed=0"
  check "the container never started" lacks "$CALLS" "docker start qompack-v6-linux-verification"
  check "release-check SKIPPED" has "$E8/chain.log" "step release-check SKIPPED"
}
case_N15_earlier_overnight_records_refuse_before_freeze() {
  mkdir -p "$E8"; echo "done: old" > "$E8/chain.log"; echo "steps=9 passed=9 failed=0" > "$E8/overnight-outcome.txt"
  v0=$(git -C "$P/qompack-v6" rev-parse HEAD)
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "refused" has "$E8/night.log" "already holds an earlier overnight's records"
  check "nothing frozen" test "$(git -C "$P/qompack-v6" rev-parse HEAD)" = "$v0"
  check "no pre-freeze ran" test ! -e "$E8/prefreeze"
}
case_N16_bundle_dir_and_dirty_candidate_refuse_before_freeze() {
  mkdir -p "$P/qompack-bundles/c8"
  v0=$(git -C "$P/qompack-v6" rev-parse HEAD)
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "an existing bundle directory refuses" has "$E8/night.log" "qompack-bundles/c8 already exists"
  check "nothing frozen" test "$(git -C "$P/qompack-v6" rev-parse HEAD)" = "$v0"
  check "no pre-freeze ran" test ! -e "$E8/prefreeze"
  rm -rf "$P/qompack-bundles/c8" "$E8"
  echo stray > "$P/qompack-cx-cand/stray.txt"
  night; rc=$?
  check "exit 1 (dirty candidate)" test "$rc" = 1
  check "a dirty candidate worktree refuses" has "$E8/night.log" "REFUSED: the candidate worktree"
  check "nothing frozen (dirty candidate)" test "$(git -C "$P/qompack-v6" rev-parse HEAD)" = "$v0"
  check "no pre-freeze ran (dirty candidate)" test ! -e "$E8/prefreeze"
}
case_N17_any_unmerged_wave_branch_refuses() {
  git -C "$P/qompack" branch closeout/w15-old main > /dev/null 2>&1      # in candidate 7 already
  git -C "$P/qompack" branch closeout/w21-late main > /dev/null 2>&1
  git -c user.name=h -c user.email=h@i -C "$P/qompack" worktree add -q "$W/late" closeout/w21-late > /dev/null 2>&1
  (cd "$W/late" && echo late > late.txt && git add . && git -c user.name=h -c user.email=h@i commit -q -m late)
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "refused by name" hasre "$E8/night.log" "REFUSED: precondition: closeout/w21-late \([0-9a-f]+\) is not merged"
  check "a branch already in candidate 7 is not a precondition" lacks "$E8/night.log" "closeout/w15-old"
}
case_N18_verify_v6_product_path_refuses() {
  (cd "$P/qompack-v6" && mkdir -p tools && echo x > tools/stray.go && git add . && git -c user.name=h -c user.email=h@i commit -q -m stray)
  v0=$(git -C "$P/qompack-v6" rev-parse HEAD)
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "refused" has "$E8/night.log" "REFUSED: verify/v6 adds paths outside plans/"
  check "and names them" has "$E8/night.log" "tools/stray.go"
  check "nothing frozen" test "$(git -C "$P/qompack-v6" rev-parse HEAD)" = "$v0"
  check "no pre-freeze ran" test ! -e "$E8/prefreeze"
}
case_N19_far_deadline_refuses() {
  export NIGHT_DEADLINE=21:00          # 23 h after the 22:00 launch: a daytime launch
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "refused" has "$E8/night.log" "REFUSED: the deadline"
  check "no pre-freeze ran" test ! -e "$E8/prefreeze"
}

# ---- F: prefreeze.sh -----------------------------------------------------------------------------
pf() { pf_r=$1; shift; PREFREEZE_RUN=$pf_r sh "$COORD/prefreeze.sh" "$P/qompack-cx-int" "$W/pf" "$@"; }
case_F1_govulncheck_offline_is_reported_not_fatal() {
  kv govuln offline; pf r1 gate
  check "gate passes" has "$W/pf/summary.log" "step gate exit=0 run=r1 "
  check "the gap is named" has "$W/pf/gate.log" "GOVULNCHECK-UNREACHABLE"
}
case_F2_govulncheck_findings_fail() {
  kv govuln vuln; pf r1 gate
  check "gate fails" has "$W/pf/summary.log" "step gate exit=1 run=r1 "
  : > "$SCEN_DIR/kv"; kv govuln other; pf r2 gate
  check "another govulncheck error fails" has "$W/pf/summary.log" "step gate exit=1 run=r2 "
}
case_F3_integration_requires_functional_rows() {
  kv omit_row SpoolTransitionJudgedPerMode; pf r1 integration
  check "integration fails" has "$W/pf/summary.log" "step integration exit=1 run=r1 "
  check "and names the row" has "$W/pf/integration.log" "TestIntegration_HotPathSpoolTransitionJudgedPerMode did not pass"
}
case_F4_fresh_summary_and_declarations() {
  pf r1 gate
  ( export QOMPACK_UNDER_COLOAD=1 QOMPACK_NONREFERENCE_DISK=1; pf r2 e2efunc integration )
  check "this run's summary exists" has "$W/pf/summary.log" "run=r2"
  check "a fresh summary per run" lacks "$W/pf/summary.log" "run=r1"
  check "e2efunc takes the declaration back" hasre "$CALLS" "^go COLOAD=unset NONREF=unset .*-skip \^TestV3_HotPath"
  check "integration declares co-load" hasre "$CALLS" "^go COLOAD=1 NONREF=unset .* ./test/integration"
  check "power recorded per step" hasre "$W/pf/summary.log" "^step e2efunc exit=0 run=r2 .* power=VALID$"
  check "a bad run id is refused" sh -c '! PREFREEZE_RUN="a b" sh "$1" "$2" "$3" gate' sh "$COORD/prefreeze.sh" "$P/qompack-cx-int" "$W/pf2"
}

# ---- P: phase3.sh's arms (the real script, against stubs) ----------------------------------------
p3_world() {
  PC="$W/p3coord"; mkdir -p "$PC"; cp "$here/phase3.sh" "$here/recrun.sh" "$PC/"
  PR="$P/qompack-cx-cand"; mkdir -p "$PR/.github/workflows"
  printf "jobs:\n  timing:\n    steps:\n      - run: go test -p 1 -count=1 -timeout=30m -run '^(TestBudgetBF|TestGC_X)\$' ./internal/mcp ./test/integration\n" > "$PR/.github/workflows/ci.yml"
  printf "        target: [ { pkg: ./internal/a, fn: FuzzA }, { pkg: ./internal/b, fn: FuzzB } ]\n" > "$PR/.github/workflows/nightly.yml"
  PE="$W/p3ev"
}
p3() { sh "$PC/phase3.sh" "$PR" "$PE" "$@" > "$W/p3.out" 2>&1; }
case_P1_bundles_pass() {
  p3_world; p3 bundles; rc=$?
  check "exit 0" test "$rc" = 0
  check "identical" has "$W/p3.out" "bundles identical:"
  check "six targets validated" has "$W/p3.out" "claude plugin validate: 6 of 6 target(s) checked"
}
case_P2_bundle_build_failure_propagates() {
  p3_world; kv rc_devtool_bundle_2 1; p3 bundles; rc=$?
  check "exit 1" test "$rc" = 1
  check "step bundles exit=1" has "$W/p3.out" "step bundles exit=1"
}
case_P3_byte_difference_propagates() {
  p3_world; kv bundle_differ bundleB; p3 bundles; rc=$?
  check "exit 1" test "$rc" = 1
  check "named" has "$W/p3.out" "bundles DIFFER"
}
case_P4_validate_rejection_propagates() {
  p3_world; kv reject_target windows-arm64; p3 bundles; rc=$?
  check "exit 1" test "$rc" = 1
  check "the rejection recorded" has "$PE/p3-claude-plugin-validate.txt" "exit=1"
}
case_P5_lint_arm_middle_failure() {
  p3_world; kv rc_devtool_lint 1; p3 lint; rc=$?
  check "exit 1" test "$rc" = 1
  check "vet still ran" test -s "$PE/p3-vet.json"
}
case_P6_gens_and_fuzz_failures() {
  p3_world; kv rc_devtool_licenses 1; p3 gens; rc=$?
  check "gens exit 1" test "$rc" = 1
  : > "$SCEN_DIR/kv"; kv rc_fuzz_1 1; p3 fuzz; rc=$?
  check "fuzz exit 1" test "$rc" = 1
  check "both fuzz targets ran" sh -c '[ -s "$1/p3-fuzz-FuzzA.json" ] && [ -s "$1/p3-fuzz-FuzzB.json" ]' sh "$PE"
}
case_P7_win_timing_needs_the_hotpath_rows() {
  p3_world; kv omit_row BAPopulationIsTheDaemonHistogram; p3 win-timing; rc=$?
  check "exit 1" test "$rc" = 1
  check "named" has "$W/p3.out" "TestIntegration_HotPathBAPopulationIsTheDaemonHistogram did not pass"
  check "its own record" test -s "$PE/p3-win-hotpath.json"
}

# ---- driver --------------------------------------------------------------------------------------
cases=$(sed -n 's/^\(case_[A-Za-z0-9_]*\)() {$/\1/p' "$0")
if [ "${1:-}" = -l ]; then printf '%s\n' "$cases" | sed 's/^case_//'; exit 0; fi
sel=${*:-}
for c in $cases; do
  if [ -n "$sel" ]; then case " $sel " in *" ${c#case_} "*) ;; *) continue ;; esac; fi
  CUR=${c#case_}; CASE_BAD=0; NCASES=$((NCASES + 1))
  new_case "$CUR" || { echo "FAIL $CUR (setup)"; FAILS=$((FAILS + 1)); continue; }
  ( cd "$W" && PATH="$HB:$PATH" && export PATH && "$c"; exit "$CASE_BAD" ) > "$W/case.out" 2>&1
  if [ $? -eq 0 ]; then echo "PASS $CUR"; else echo "FAIL $CUR"; sed 's/^/    /' "$W/case.out"; FAILS=$((FAILS + 1)); fi
done
echo "$NCASES case(s), $FAILS failed"
[ "$FAILS" -eq 0 ]
