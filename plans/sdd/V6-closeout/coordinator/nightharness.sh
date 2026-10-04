#!/bin/sh
# nightharness.sh [case...]
# A dry harness for candidate 8's night: c8-night.sh, overnight-c8.sh, prefreeze.sh, phase3.sh's
# multi-command arms, power.sh, stamped.sh, c52derive.py, keepawake.ps1 and nightabort.ps1. It
# exercises every branch the night can take (AC, battery, a power change, Modern Standby, sleep or
# resume during a step, the wait budget, the deadline, each step's estimate, refusals, the
# release-check clone and its tag, a signal mid-run, the C5.2 night with the full list or the
# derivation, the C5.2 chunks, the C1.16 rig, e2efunc's skips and their drift, an abort, Docker
# engine ownership, the step estimates and release-check's watchdog, the keep-awake check, the disk
# precondition) with no real night: powershell, pwsh, docker, go, claude, gh, timeout, date, sleep
# and df are stubs on PATH; git and python are real, on scratch repositories under a temporary
# directory; phase3.sh and quiet.sh are stubs beside the copied night scripts (phase3.sh's own arms
# are run for real against stubs in the P cases, and quiet.sh for real in the Q cases). Every case
# runs only after a guard checks that each stub name resolves to the stub directory (`command -v`):
# a scratch path PATH cannot hold (a Windows drive colon, as mktemp gives under a C:/ TMPDIR, splits
# PATH there) would otherwise bypass every stub and run the night against the real tools. The
# scratch path is taken in POSIX form (cygpath -u), and the harness refuses one that still holds a
# colon. Time is a fake clock: a file the date and sleep stubs and stamped.sh's
# STAMP_CLOCK_FILE seam read, so no case waits on, or depends on, the wall clock. The real
# processes outside the stubs are K1's, keepawake.ps1 under the real pwsh with a sentinel that does
# not exist, which must end without holding anything, and K2's, a probe tree (sh, sleep, cmd, ping)
# under a shell named c8-night.sh that the real nightabort.ps1 must list and stop whole. While the
# stub guard holds, it never touches the real repository, ~/.claude or ~/.qompack, and starts no
# Docker, Claude Code or Go process (the Q cases point HOME and USERPROFILE at a scratch home).
# Prints PASS/FAIL per case and a total; exits 1 if any case fails. Cases: run with -l to list.
set -u
here=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d) || exit 2
T0=$T
if command -v cygpath > /dev/null 2>&1; then T=$(cygpath -u "$T") || exit 2; fi
case $T in
  *:*) echo "nightharness: the scratch path '$T' holds a ':', which splits PATH, so the stubs would be bypassed; set TMPDIR to a POSIX path" >&2
       rm -rf "$T0"; exit 2 ;;
esac
trap 'if [ -n "${NIGHTHARNESS_KEEP:-}" ]; then echo "scratch kept: $T"; else rm -rf "$T"; fi' EXIT
HB="$T/bin"; mkdir -p "$HB"
# Every name a night script may call that must never reach the real tool. stub_guard runs inside each
# case, after PATH is set, and refuses the case unless every one resolves to $HB.
STUBS="docker gh go powershell pwsh timeout date sleep claude df"
stub_guard() {
  for sg_n in $STUBS; do
    sg_p=$(command -v "$sg_n" 2> /dev/null)
    [ "$sg_p" = "$HB/$sg_n" ] || { echo "  FAIL [$CUR] stub guard: $sg_n resolves to '${sg_p:-nothing}', not $HB/$sg_n; the case did not run"; return 1; }
  done
}
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
# keepawake.ps1's own first line (kv keepawake=fail prints its failure line instead).
. "$HB/_lib.sh"; call "pwsh $*"
case $(kv keepawake held) in
  held) echo "keep-awake held while (the sentinel) exists (pid $$)" ;;
  fail) echo "keep-awake FAILED: SetThreadExecutionState returned 0 (pid $$)"; exit 1 ;;
esac
EOF
cat > "$HB/df" <<'EOF'
#!/bin/sh
# df -Pk: kv disk_free_gb (default 500) GiB available on every path.
. "$HB/_lib.sh"; call "df $*"
printf 'Filesystem 1024-blocks Used Available Capacity Mounted on\n'
printf 'C: 2000000000 1000000 %s 50%% /c\n' "$(( $(kv disk_free_gb 500) * 1048576 ))"
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
# docker ps fails on the call kv docker_ps_fail_nth names, on every call kv docker_ps_fail_list
# names, and on all of them with kv docker_ps=1 until `desktop start` brought the engine up. Its
# listing is kv docker_others (default owner_db, the owner's stack; none for no container) plus the
# night's container while it runs. `desktop status` answers kv docker_status, or by default running
# while ps would answer and stopped otherwise (fail: the query fails).
. "$HB/_lib.sh"; call "docker $*"
case "$1 ${2:-}" in
  "ps "*|"ps") n=$(nth dockerps)
               [ "$n" = "$(kv docker_ps_fail_nth 0)" ] && exit 1   # one transient failure
               case " $(kv docker_ps_fail_list "") " in *" $n "*) exit 1 ;; esac
               [ -e "$SCEN_DIR/engine.up" ] || [ "$(kv docker_ps 0)" = 0 ] || exit 1
               case "$*" in *--format*)
                 o=$(kv docker_others owner_db); [ "$o" = none ] && o=""
                 [ -e "$SCEN_DIR/container.up" ] && o="$o qompack-v6-linux-verification"
                 for c in $o; do case "$*" in *Status*) echo "$c Up 2 hours" ;; *) echo "$c" ;; esac; done ;;
               esac ;;
  "desktop status") st=$(kv docker_status "")
                    if [ -z "$st" ]; then
                      if [ -e "$SCEN_DIR/engine.up" ] || [ "$(kv docker_ps 0)" = 0 ]; then st=running; else st=stopped; fi
                    fi
                    [ "$st" = fail ] && exit 1
                    printf 'Name                Value\r\nStatus              %s\r\nSessionID           0000\r\n' "$st" ;;
  "desktop start") : > "$SCEN_DIR/engine.up" ;;
  "desktop stop") rm -f "$SCEN_DIR/engine.up" ;;
  "start "*) rc=$(kv docker_start 0); [ "$rc" = 0 ] && : > "$SCEN_DIR/container.up"; exit "$rc" ;;
  "stop "*) rm -f "$SCEN_DIR/container.up" ;;
  *) : ;;
esac
EOF
cat > "$HB/go" <<'EOF'
#!/bin/sh
. "$HB/_lib.sh"
call "go COLOAD=${QOMPACK_UNDER_COLOAD-unset} NONREF=${QOMPACK_NONREFERENCE_DISK-unset} $*"
case "$1 ${2:-}" in
  # The toolchain go.mod pins in the working directory (GOTOOLCHAIN=auto), else kv local_go: the
  # local default a launch from outside a module would report.
  "env GOVERSION") t=$(sed -n 's/^toolchain \(go[0-9.]*\)$/\1/p' go.mod 2> /dev/null | head -n 1)
                   echo "${t:-$(kv local_go go1.26.4)}"; exit 0 ;;
  "env GOCACHE") echo "$SCEN_DIR/gocache"; exit 0 ;;
  "version "*) t=$(sed -n 's/^toolchain \(go[0-9.]*\)$/\1/p' go.mod 2> /dev/null | head -n 1)
               echo "go version ${t:-$(kv local_go go1.26.4)} windows/amd64"; exit 0 ;;
  "test -c")   # quiet.sh's builds: a fake test binary printing each selected benchmark's line
     out=""; prev=""; for a in "$@"; do [ "$prev" = -o ] && out=$a; prev=$a; done
     case $out in */base/*) side=base ;; *) side=candidate ;; esac
     [ "$(kv "rc_build_$side" 0)" = 0 ] || exit 1
     mkdir -p "$(dirname "$out")"
     printf '#!/bin/sh\n. "$HB/_lib.sh"; call "bench %s $*"\n' "$side" > "$out"
     cat >> "$out" <<'EOB'
re=""; prev=""; for a in "$@"; do [ "$prev" = -test.bench ] && re=$a; prev=$a; done
case $re in '^$'|'') exit 0 ;; esac
names=$(printf '%s' "$re" | sed 's/^\^Benchmark(\(.*\))\$$/\1/; s/^\^Benchmark\(.*\)\$$/\1/' | tr '|' ' ')
for nm in $names; do
EOB
     printf '  printf "Benchmark%%s-8   \\t    1000\\t %%s ns/op\\t     100 B/op\\t       2 allocs/op\\n" "$nm" "$(kv "ns_%s_$nm" 1000)"\ndone\nexit 0\n' "$side" >> "$out"
     chmod +x "$out"; exit 0 ;;
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
if [ "$1" = test ] && case " $* " in *TestSessionStartCompact_UnderSameSessionIngest*) true ;; *) false ;; esac; then
  # phase3.sh's c116-rig: the rig's own lines (kv c116_omit=pass|dist|reads drops one), per run.
  n=$(nth c116); adv "$(kv dur_c116 60)"
  call "go-c116 n=$n ROUNDS=${QOMPACK_C116_ROUNDS-unset} WORKERS=${QOMPACK_C116_WORKERS-unset} READ=${QOMPACK_C116_READ_BYTES-unset} FSYNC=${QOMPACK_C116_FSYNC_COLOAD-unset} CPU=${QOMPACK_C116_CPU_COLOAD-unset} COLOAD=${QOMPACK_UNDER_COLOAD-unset}"
  om=$(kv c116_omit "")
  echo "=== RUN   TestSessionStartCompact_UnderSameSessionIngest"
  [ "$om" = dist ] || echo "    sessionstart_compact_load_test.go:335: compact SessionStart wall (n=${QOMPACK_C116_ROUNDS:-3}): p50=150ms p95=250ms p99=300ms max=300ms answers=map[rehydration:${QOMPACK_C116_ROUNDS:-3}]"
  [ "$om" = reads ] || echo "    sessionstart_compact_load_test.go:409: of the rig's 271 Reads: 250 indexed, 21 in its daemon's WAL only, 0 in a client spool"
  [ "$om" = pass ] || echo "--- PASS: TestSessionStartCompact_UnderSameSessionIngest (20.00s)"
  rc=$(kv "rc_c116_$n" "$(kv rc_c116 0)")
  [ "$rc" = 0 ] && echo "ok  	github.com/qompack/qompack/internal/cli	20.3s" || echo "FAIL	github.com/qompack/qompack/internal/cli	20.3s"
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
  # A test/e2e run of one subtest (-run '^Row$/^arm$'), as prefreeze.sh's e2efunc runs a timing
  # row's functional arm: its PASS line, unless kv omit_arm names the arm.
  if [ "$key" = e2e ]; then
    rp=""; prev=""; for a in "$@"; do [ "$prev" = -run ] && rp=$a; prev=$a; done
    case $rp in
      ^*'$/^'*'$')
        arow=${rp#^}; arow=${arow%%\$/*}; aarm=${rp#*\$/^}; aarm=${aarm%\$}
        [ "$(kv omit_arm __none__)" = "$aarm" ] || echo "    --- PASS: $arow/$aarm (0.10s)" ;;
    esac
  fi
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
observer OnToolUse_TestOutput256KB 200x perfobs (SP08-D1)
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
  # quiet.sh's c51-linux lands its harness JSON here; kv c51_linux_noart=1 is a run that measured nothing.
  if [ "$step" = c51-linux ] && [ "$(kv c51_linux_noart 0)" != 1 ]; then
    mkdir -p "$ev/c51-linux" && echo '{"rows": []}' > "$ev/c51-linux/c51-linux-hotpath.json"
  fi
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
  # main is "candidate 7" (C8_PREV_CANDIDATE): a module whose five packages define the benchmarks
  # the quiet.sh stub lists (observer, store and checkpoint are C52_GROUPS' chunks; daemon and cli
  # make the "other" chunk). Two wave branches change product code (checkpoint's intent.go and
  # daemon's scheduler_tap.go), so c52derive.py finds exactly the checkpoint and daemon rows.
  # FROZEN is verify/v6 merged with integration, as c8-night.sh freezes it; qompack-cx-cand sits
  # there for the overnight-only cases.
  {
    $G init -q "$R" && cd "$R" && echo base > base.txt &&
    printf 'module github.com/qompack/qompack\n\ngo 1.26\n\ntoolchain go1.26.6\n' > go.mod &&
    for pf in checkpoint:intent:Finalize:ExtractDecisions daemon:scheduler_tap:SchedulerTap_ObserveTool \
              store:put:PutBytes_100KB_Warm cli:config:HookNoop_InProcess observer:tap:OnToolUse_TestOutput256KB; do
      pk=${pf%%:*}; rest=${pf#*:}; fl=${rest%%:*}; bn=${rest#*:}
      mkdir -p "internal/$pk" && printf 'package %s\n\nfunc f() int { return 1 }\n' "$pk" > "internal/$pk/$fl.go" &&
        printf 'package %s\n\n// other.go is never changed\nfunc g() int { return 2 }\n' "$pk" > "internal/$pk/other.go" &&
        { printf 'package %s\n\nimport "testing"\n' "$pk"; for x in $(echo "$bn" | tr ':' ' '); do printf 'func Benchmark%s(b *testing.B) {}\n' "$x"; done; } > "internal/$pk/bench_test.go" || return 1
    done &&
    # test/e2e's rows as prefreeze.sh's e2efunc reads them (its three timing rows, X10's detector
    # arm and a functional row), and a ci.yml timing lane in phase3.sh's format naming no test/e2e
    # row, as the real one does.
    mkdir -p test/e2e .github/workflows &&
    { printf 'package e2e\n\nimport "testing"\n\n'
      for x in TestE2EHookRoundTrip TestV3_HotPathUnchangedWithLedgerResident TestE2E_SessionStartLatency \
               TestV5_ThrashWarningVisibleInStatusAndCheckpoint; do printf 'func %s(t *testing.T) {}\n' "$x"; done
      printf 'func x10Arms(t *testing.T) {\n\tt.Run("state_aware_detector_is_progress_aware_and_cannot_feed_itself", nil)\n}\n'
    } > test/e2e/rows_test.go &&
    printf "jobs:\n  timing:\n    steps:\n      - run: go test -p 1 -count=1 -timeout=30m -run '^(TestBudgetBF|TestIntegration_HotPathWarmWithRealResidentState)\$' ./internal/mcp ./test/integration\n" > .github/workflows/ci.yml &&
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
  # Candidate 8's night runs C5.2 chunks after release-check by default (D65(b)). Every case runs with
  # them off except those that test them (X9-X11, X22), which unset this: the other cases test the
  # night up to release-check, and eight more gated steps would only lengthen each of them.
  C8_NIGHT1_C52=0; export C8_NIGHT1_C52
  cd "$W" || return 1
  COORD="$P/qompack-v6/plans/sdd/V6-closeout/coordinator"; mkdir -p "$COORD"
  for f in c8-night.sh overnight-c8.sh prefreeze.sh power.sh stamped.sh recrun.sh keepawake.ps1 c52derive.py homeguard.py; do
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
# rc_clones <chain.log>: every release-check clone path, one with spaces whole (the line reads
# "release-check clone <path> at <40-hex sha>: ...").
rc_clones() { sed -n 's/^release-check clone \(.*\) at [0-9a-f]\{40\}: .*/\1/p' "$1"; }

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
# ---- K2: nightabort.ps1 (the real script, under the real pwsh, on a real process tree) ------------
# An MSYS shell's children record a dead Windows parent (the fork stub exits after exec), so a
# ParentProcessId walk from the night's shell finds the shell alone. The tree here is a shell named
# c8-night.sh running a child shell, which runs an MSYS sleep and a native cmd whose own child is a
# native ping: every kind of descendant the night has. Waits are for events (the listing shows the
# native grandchild; the processes are gone), polled; the outer timeouts are only hang guards.
case_K2_nightabort_stops_exactly_the_night() {
  check "pwsh is installed" test -n "$REALPWSH"
  [ -n "$REALPWSH" ] || return 0
  k="$W/k2"; mkdir -p "$k"; ab=$(cygpath -w "$here/nightabort.ps1")
  printf '#!/bin/sh\n/usr/bin/sleep 173 &\ncmd //c "ping -n 173 127.0.0.1 > nul" &\nwait\n' > "$k/child.sh"
  # As c8-night.sh logs them: its own pids, read inside the script (after exec, which gives an MSYS
  # pid a new Windows pid).
  printf '#!/bin/sh\nsh "%s/child.sh" &\necho $$ > "%s/pids.tmp"; cat /proc/$$/winpid >> "%s/pids.tmp"; mv "%s/pids.tmp" "%s/pids"\nwait\n' \
    "$k" "$k" "$k" "$k" "$k" > "$k/c8-night.sh"
  printf '%s\n' '@(Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -match "k2/c8-night\.sh|k2/child\.sh|ping -n 173|sleep\.exe"" 173" }).Count' > "$k/count.ps1"
  left() { "$REALPWSH" -NoProfile -File "$(cygpath -w "$k/count.ps1")" | tr -d '\r'; }
  sh "$k/c8-night.sh" &
  "$REALTIMEOUT" 60 sh -c 'until [ -s "$1" ]; do /usr/bin/sleep 0.2; done' sh "$k/pids"
  mp=$(sed -n 1p "$k/pids"); wp=$(sed -n 2p "$k/pids")
  check "the probe logged its pids" test -n "$mp" -a -n "$wp"
  "$REALPWSH" -NoProfile -File "$ab" -MsysPid "$mp" -WinPid $((wp + 1)) > "$k/refuse.txt" 2>&1; rc=$?
  check "a Windows pid that is not the root's: exit 2" test "$rc" = 2
  check "and nothing stopped" kill -0 "$mp"
  "$REALPWSH" -NoProfile -File "$ab" -MsysPid "$$" -WinPid "$(cat /proc/$$/winpid)" > "$k/refuse2.txt" 2>&1; rc=$?
  check "a shell that is not a night script: exit 2" test "$rc" = 2
  "$REALTIMEOUT" 120 sh -c 'until "$1" -NoProfile -File "$2" -MsysPid "$3" -WinPid "$4" > "$5" 2>&1 && grep -q PING.EXE "$5"; do /usr/bin/sleep 0.5; done' \
    sh "$REALPWSH" "$ab" "$mp" "$wp" "$k/list.txt"
  check "the child shell is listed" has "$k/list.txt" "k2/child.sh"
  check "its MSYS child is listed" has "$k/list.txt" "sleep.exe"
  check "its native child is listed" has "$k/list.txt" "cmd.exe"
  check "and that one's native child" has "$k/list.txt" "PING.EXE"
  check "nothing above the root" lacks "$k/list.txt" "nightharness.sh"
  check "listing stops nothing" kill -0 "$mp"
  "$REALPWSH" -NoProfile -File "$ab" -MsysPid "$mp" -WinPid "$wp" -Stop > "$k/stop.txt" 2>&1; rc=$?
  check "stop: exit 0" test "$rc" = 0
  "$REALTIMEOUT" 120 sh -c 'until [ "$("$1" -NoProfile -File "$2" | tr -d "\r")" = 0 ]; do /usr/bin/sleep 0.5; done' sh "$REALPWSH" "$(cygpath -w "$k/count.ps1")"
  n=$(left)
  check "every process of the tree is gone" test "$n" = 0
  wait "$mp" 2> /dev/null
  [ "$n" = 0 ] || "$REALPWSH" -NoProfile -Command '@(Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -match "k2/c8-night\.sh|k2/child\.sh|ping -n 173|sleep\.exe"" 173" }) | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }' > /dev/null 2>&1
  return 0
}

# ---- O: overnight-c8.sh ---------------------------------------------------------------------------
case_O1_all_ac() {
  overnight; rc=$?
  check "exit 0" test "$rc" = 0
  for s in win-timing win-e2e-timing win-x11-alone c51-win c51-linux release-check linux-timing linux-tree; do
    check "$s VALID" test "$(row "$s" 1)" = VALID
  done
  check "outcome counts" has "$W/ev/overnight-outcome.txt" "failed=0 invalid_power=0 not_reference=0 (of which 0 exited non-zero) skipped=0 reported=1"
  # D62(c): candidate 8's night ends with release-check; C5.2 and the C1.16 rig run the next night.
  # (C8_NIGHT1_C52=0 here, new_case: X22 runs the night's own C5.2 chunks after release-check.)
  for s in c52-derive c116-rig $C52_ALL; do check "$s is not in this night" test -z "$(row "$s" 1)"; done
  check "no C5.2 measurement, no trace, no rig" sh -c '! grep -qE "^(quiet .*c52-(win|linux)$|go-cov |go-c116 |phase3 .* c116-rig$)" "$1"' sh "$CALLS"
  check "the outcome names them as pending" has "$W/ev/overnight-outcome.txt" "pending_c52_night=[c116-rig $C52_ALL]"
  check "the chain says why" has "$W/ev/chain.log" "with C8_C52_STEPS naming these): c116-rig $C52_ALL"
  check "C5.1 on Linux in its own container window" hasre "$CALLS" "quiet .* cf31e01 .*/quiet-c51-linux c51-linux"
  check "the start line carries the Windows pid" hasre "$W/ev/chain.log" "^start candidate=.* winpid "
  check "release-check saw its tag on HEAD" has "$W/ev/p3-release-check-tag.log" "TAG=v0.3.0"
  check "the clone cannot push" has "$W/ev/p3-release-check-tag.log" "ORIGIN=file:///nonexistent/"
  check "the shared ref store never held v0.3.0" sh -c '! git -C "$1" rev-parse -q --verify refs/tags/v0.3.0' sh "$P/qompack"
  check "the chain says so" has "$W/ev/chain.log" "tag v0.3.0 absent (the chain never tags it)"
  d=$(rc_clones "$W/ev/chain.log" | head -n 1)
  check "the clone path is logged" test -n "$d"
  check "the clone is removed" test ! -e "$d"
  check "a JSON record of release-check" test -s "$W/ev/p3-release-check-tag.json"
  check "--evidence-copy written" test -s "$W/ev/release-check.json"
  check "the container is started for the lanes and c51-linux" sh -c '[ "$(grep -c "^docker start qompack-v6-linux-verification" "$1")" = 2 ]' sh "$CALLS"
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
  for s in win-timing win-e2e-timing win-x11-alone c51-win c51-linux linux-timing linux-e2e-timing; do
    check "$s NOT-REFERENCE" sh -c 'case "$1" in NOT-REFERENCE*) ;; *) exit 1 ;; esac' sh "$(row "$s" 1)"
  done
  check "no step is VALID" sh -c '[ -s "$1" ] && ! awk -F"\t" "\$4 == \"VALID\"" "$1" | grep -q .' sh "$W/ev/power.tsv"
  check "no VALID verdict on battery in chain.log" sh -c '[ -s "$1" ] && ! grep -qE "VALID power=BAT" "$1"' sh "$W/ev/chain.log"
  # A window wholly on battery is NOT-REFERENCE (the header's taxonomy), not INVALID-POWER (an event).
  check "release-check's windows ran on battery" sh -c 'case "$1" in NOT-REFERENCE*on-battery*) ;; *) exit 1 ;; esac' sh "$(row release-check 1)"
  check "release-check is not retried without AC" test -z "$(row release-check 2)"
  check "and says why" has "$W/ev/chain.log" "not retried: no AC for a second run"
  check "outcome" has "$W/ev/overnight-outcome.txt" "invalid_power=0 not_reference=8"
  check "the budget is named" has "$W/ev/chain.log" "wait budget is spent (5 of 5 min"
}
case_O4_ac_returns_mid_night() {
  # AC returns during bundles (300-600 s), before the Linux timing lanes. Not at 600 s itself: an
  # event in the second a step takes its t0 counts against that step (the window is inclusive).
  timeline "0 BAT" "$(at 590) AC"
  overnight; rc=$?
  check "exit 0" test "$rc" = 0
  for s in win-timing win-e2e-timing win-x11-alone c51-win release-check; do
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
  d=$(rc_clones "$W/ev/chain.log" | head -n 1)
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
  kv docker_ps 1; kv docker_start 1; kv docker_others none   # an engine that was down runs no container
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
# The C5.2 chunks (overnight-c8.sh's header): one step per OS and package group, each calling
# quiet.sh with its own non-empty QUIET_PKGS into its own quiet-<step>/ directory. The stub lists
# observer, checkpoint, daemon, store and cli: the "other" chunk is daemon and cli, in list order.
# The derived set (C8_C52_SET=derived, D57(e)) in a C5.2 night. A derivation that cannot run at all
# measures the FULL C5.2 list (D62(b): file-level, and no trace knows a row that keeps its carry).
C52_ALL="c52-win-observer c52-win-store c52-win-checkpoint c52-win-other c52-linux-observer c52-linux-store c52-linux-checkpoint c52-linux-other"
chunk_call() { # chunk_call <chunk>: "PKGS=<pkgs> FILTER=<filter>" as quiet.sh got them for the chunk
  sed -n "s#^quiet \(PKGS=.* FILTER=[^ ]*\) [^ ]* cf31e01 [^ ]*/quiet-$1 c52-[a-z]*\$#\1#p" "$CALLS" | tail -n 1
}
full_c52() { # full_c52 <os> [<packages, sorted>]: the os's chunks measured the whole list, unfiltered
  fc_want=${2:-checkpoint cli daemon observer store}
  fc_got=$(for g in observer store checkpoint other; do chunk_call "c52-$1-$g"; done |
    sed -n 's/^PKGS=\(.*\) FILTER=$/\1/p' | tr ' ' '\n' | sed '/^$/d' | sort | tr '\n' ' ' | sed 's/ $//')
  [ "$fc_got" = "$fc_want" ] && ! grep -qE "^quiet PKGS= " "$CALLS"
}
case_O16_c52_derivation_failure_measures_the_full_list() {
  export C8_C52_ONLY=1 C8_C52_SET=derived C8_PREV_CANDIDATE=0000000000000000000000000000000000000000
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "the derivation is a failed step" hasre "$W/ev/chain.log" "step c52-derive finished exit=[1-9]"
  check "the chain says why, and what is measured" has "$W/ev/chain.log" "c52: the derivation produced no selection, so the FULL C5.2 list is measured: all 7 rows of quiet.sh's benches() list (5 packages), in chunks whose packages together are exactly the list's"
  check "the Windows chunks measure the full list" full_c52 win
  check "the Linux chunks measure the full list" full_c52 linux
  check "no static floor" lacks "$W/ev/chain.log" "floor"
}
case_O17_c52_trace_failure_is_selected() {
  export C8_C52_ONLY=1 C8_C52_SET=derived
  kv rc_cov_PutBytes_100KB_Warm 1
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "the derivation fails" hasre "$W/ev/chain.log" "step c52-derive finished exit=1"
  check "the untraced row is measured, fail-closed" test "$(chunk_call c52-win-store)" = 'PKGS=store FILTER=^Benchmark(Finalize|ExtractDecisions|SchedulerTap_ObserveTool|PutBytes_100KB_Warm)$'
  check "the derived set is logged whole" has "$W/ev/chain.log" "QUIET_PKGS='checkpoint daemon store'"
  check "the report says why" has "$W/ev/c52-derive/selection.tsv" "trace failed"
}
case_O22_c52_every_trace_failing_measures_the_full_list() {
  export C8_C52_ONLY=1 C8_C52_SET=derived
  kv rc_cov 1                          # a broken derivation (a flag, the toolchain), not five broken rows
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "the derivation is a failed step" hasre "$W/ev/chain.log" "step c52-derive finished exit=2"
  check "the full list is measured on Windows" full_c52 win
  check "and on Linux" full_c52 linux
  check "the report says why" has "$W/ev/c52-derive/report.txt" "every trace failed"
  check "no selection is written" test ! -e "$W/ev/c52-derive/filter.txt"
}
case_O23_c52_stray_go_file_refused() {
  # A git-ignored stray (as dist/ holds in a real worktree): overnight-c8.sh's own clean check reads
  # `git status --porcelain`, which lists untracked files but not ignored ones, so c52derive.py's
  # guard is the one that must see it.
  export C8_C52_ONLY=1 C8_C52_SET=derived
  mkdir -p "$P/qompack-cx-cand/internal/stray" && echo 'package stray' > "$P/qompack-cx-cand/internal/stray/x.go"
  echo 'internal/stray/' >> "$P/qompack/.git/info/exclude"      # shared by every worktree
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "the derivation refuses" hasre "$W/ev/chain.log" "step c52-derive finished exit=2"
  check "and names the file" has "$W/ev/chain.log" "internal/stray/x.go"
  check "the full list is measured" full_c52 win
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
  rc_clones "$W/ev/chain.log" > "$W/clones"
  check "two clone paths parsed" sh -c '[ "$(grep -c . "$1")" = 2 ]' sh "$W/clones"
  while IFS= read -r d; do check "clone $d removed" test ! -e "$d"; done < "$W/clones"
}
case_O21_rc_not_retried_on_battery() {
  timeline "0 BAT"; export AC_WAIT_BUDGET_MIN=600
  overnight
  check "try 1 NOT-REFERENCE on battery" sh -c 'case "$1" in NOT-REFERENCE*on-battery*) ;; *) exit 1 ;; esac' sh "$(row release-check 1)"
  check "no second try on battery" sh -c '! grep -q "step release-check try 2 start power=BAT" "$1"' sh "$W/ev/chain.log"
}
case_O24_candidate_checkout_must_be_the_candidate() {
  # Run alone (README Abort step 7), every verdict is logged as candidate <sha>, so the checkout the
  # steps run in must be exactly that commit, and clean.
  on() { sh "$COORD/overnight-c8.sh" "$P/qompack-cx-cand" "$1" "$W/$2" 2> "$W/$2.err"; }
  on "$(printf '%s' "$CAND_SHA" | cut -c1-12)" ev1; rc=$?
  check "a short SHA: exit 2" test "$rc" = 2
  check "a short SHA: refused" has "$W/ev1.err" "full 40-character SHA"
  check "a short SHA: nothing written" test ! -e "$W/ev1/chain.log"
  git -C "$P/qompack-cx-cand" checkout -q --detach main          # candidate 7
  on "$CAND_SHA" ev2; rc=$?
  check "a checkout at another commit: exit 2" test "$rc" = 2
  check "refused by name" has "$W/ev2.err" "not the candidate $CAND_SHA"
  check "nothing written" test ! -e "$W/ev2/chain.log"
  check "no step ran" sh -c '! grep -qE "^(phase3|quiet|go) " "$1"' sh "$CALLS"
  git -C "$P/qompack-cx-cand" checkout -q --detach "$CAND_SHA"
  echo dirty >> "$P/qompack-cx-cand/base.txt"
  on "$CAND_SHA" ev3; rc=$?
  check "a tracked change: exit 2" test "$rc" = 2
  check "a tracked change: refused" has "$W/ev3.err" "is not clean"
  git -C "$P/qompack-cx-cand" checkout -q -- base.txt
  echo stray > "$P/qompack-cx-cand/stray.txt"
  on "$CAND_SHA" ev4; rc=$?
  check "an untracked file: exit 2" test "$rc" = 2
  check "an untracked file: refused" has "$W/ev4.err" "is not clean"
  check "no step ran in any of them" sh -c '! grep -qE "^(phase3|quiet|go) " "$1"' sh "$CALLS"
}
case_O25_rc_retry_after_a_red_try_needs_a_full_run() {
  # release-check stops at its first FAIL, so a red try 1 says nothing about a green try 2's length.
  # rc starts at 23:00:00 on the fake clock (3 Windows timing steps, win-race, bundles, five Linux
  # lanes and both C5.1 steps at 300 s each); try 1 goes red after 630 s with a source change in its
  # e2e window. A retry sized by try 1 (10.5 min) would fit before 00:10; one sized by RC_EST_S
  # (60 min) would end at 00:10:30, after it.
  export RC_EST_S=3600 NIGHT_DEADLINE=00:10
  printf '%s\n' 'say === release-check: version agreement ===' tag 'say release-check: version agreement PASS' \
    'say === release-check: ci-local test ===' 'adv 300' 'say ok  	github.com/qompack/qompack/internal/store	300.1s' \
    'flip BAT' 'adv 30' 'flip AC' 'adv 300' 'say FAIL	github.com/qompack/qompack/test/e2e	330.0s' \
    'say release-check: ci-local test FAIL' > "$SCEN_DIR/rc.1"
  kv rc_release_1 1
  overnight
  check "try 1 INVALID-POWER in its e2e window" sh -c 'case "$1" in "INVALID-POWER ci-local-test:events"*) ;; *) exit 1 ;; esac' sh "$(row release-check 1)"
  check "not retried" test -z "$(row release-check 2)"
  check "sized by RC_EST_S, not by the red try's length" has "$W/ev/chain.log" "not retried: a second run of about 60 min (RC_EST_S: try 1 failed, so its length is no estimate) would end after the deadline"
  check "no try ran past the deadline" sh -c '! grep -q "step release-check try 2 start" "$1"' sh "$W/ev/chain.log"
}
rc_short() { # a short green release-check with both AC-sensitive windows (about 1000 s)
  printf '%s\n' 'say === release-check: version agreement ===' tag 'say release-check: version agreement PASS' \
    'say === release-check: ci-local test ===' 'adv 200' 'say ok  	github.com/qompack/qompack/internal/store	200.1s' \
    'adv 300' 'say ok  	github.com/qompack/qompack/test/e2e	300.5s' 'say release-check: ci-local test PASS' \
    'say === release-check: ci-local cover ===' 'adv 200' 'say ok  	github.com/qompack/qompack/internal/store	200.0s' \
    'adv 300' 'say ok  	github.com/qompack/qompack/test/e2e	300.0s' 'say release-check: ci-local cover PASS'
}
case_O26_c52_skipped_when_it_cannot_end_by_the_deadline() {
  # A C5.2 night with the full list (the default), deadline 23:40. Each chunk's estimate is its
  # packages' C52_EST_* plus 600 s: Windows observer 93 min, store 65, checkpoint 52, other (daemon
  # and cli) 17; Linux observer 61, store 71, checkpoint 56, other 16. Every step runs 5 min. The
  # rig runs 22:00-22:05, c52-win-observer fits (22:05 + 93 min = 23:38) and the other Windows
  # chunks follow; at 22:30 c52-linux-store would end at 23:41, so it is SKIPPED, and the smaller
  # chunks after it still run.
  export C8_C52_ONLY=1 NIGHT_DEADLINE=23:40
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "the rig ran, VALID" test "$(row c116-rig 1)" = VALID
  for s in c52-win-observer c52-win-store c52-win-checkpoint c52-win-other c52-linux-observer c52-linux-checkpoint c52-linux-other; do
    check "$s ran, VALID" test "$(row "$s" 1)" = VALID
  done
  check "c52-linux-store SKIPPED" test "$(row c52-linux-store 1)" = SKIPPED
  check "each chunk's estimate is logged" has "$W/ev/chain.log" "c52: c52-win-observer measures QUIET_PKGS='observer' QUIET_BENCH_FILTER='' into quiet-c52-win-observer/, about 93 min"
  check "the other chunk's too" has "$W/ev/chain.log" "c52: c52-linux-other measures QUIET_PKGS='daemon cli' QUIET_BENCH_FILTER='' into quiet-c52-linux-other/, about 16 min"
  check "the night's total" has "$W/ev/chain.log" "c52: tonight's chunks need about 227 min on Windows and 204 min on Linux"
  check "the reason names the estimate and the later night" hasre "$W/ev/chain.log" "step c52-linux-store try 1 exit=- SKIPPED .*needs about 71 min \(C52_EST_\*: candidate 5's time for its packages; a later C5.2 night \(C8_C52_ONLY=1, C8_C52_STEPS naming it\) measures it\) and would end after the deadline"
  check "the Windows chunks measured the full list" full_c52 win
  check "the skipped chunk never started" sh -c '! grep -qE "^quiet .*/quiet-c52-linux-store c52-linux$" "$1"' sh "$CALLS"
  check "the skip is counted" has "$W/ev/overnight-outcome.txt" "skipped=1"
}
case_O27_c52_derivation_skipped_near_the_deadline() {
  # Derived mode: the rig (C116_EST_S, 65 min, fits before 23:10) runs 40 min, to 22:40; the
  # derivation (C52_DERIVE_EST_S, 45 min) would end at 23:25, so neither it nor C5.2 starts, and no
  # list is claimed as measured.
  export C8_C52_ONLY=1 C8_C52_SET=derived NIGHT_DEADLINE=23:10
  kv dur_c116-rig 2400
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "the rig ran first" test "$(row c116-rig 1)" = VALID
  check "c52-derive SKIPPED" test "$(row c52-derive 1)" = SKIPPED
  for s in $C52_ALL; do check "$s SKIPPED" test "$(row "$s" 1)" = SKIPPED; done
  check "for want of a derivation" hasre "$W/ev/chain.log" "step c52-win-observer SKIPPED: no C5.2 derivation tonight"
  check "no trace ran" lacks "$CALLS" "go-cov "
  check "no list claimed as measured" lacks "$W/ev/chain.log" "list is measured"
  check "the skips are counted" has "$W/ev/overnight-outcome.txt" "skipped=9"
}
case_O28_c51_linux_is_report_only() {
  # The container's quiet C5.1 exits 1 on its fsync-bound B-A/B-B (D53(b)): recorded, never judged.
  kv rc_c51-linux 1
  overnight; rc=$?
  check "exit 0" test "$rc" = 0
  check "c51-linux VALID" test "$(row c51-linux 1)" = VALID
  check "counted as a report" has "$W/ev/overnight-outcome.txt" "reported=1"
  check "not as a failure" has "$W/ev/overnight-outcome.txt" "failed=0"
  check "the note says why" has "$W/ev/chain.log" "report only"
  check "measured in its own evidence directory" test -s "$W/ev/quiet-c51-linux/c51-linux/c51-linux-hotpath.json"
  # A run that measured nothing is a failure, whatever its exit status.
  : > "$SCEN_DIR/kv"; kv c51_linux_noart 1; echo "$START" > "$FAKE_CLOCK"
  sh "$COORD/overnight-c8.sh" "$P/qompack-cx-cand" "$CAND_SHA" "$W/ev2"; rc=$?
  check "no C5.1 Linux record: exit 1" test "$rc" = 1
  check "no C5.1 Linux record is a failure" hasre "$W/ev2/overnight-outcome.txt" "failed=1 \[c51-linux\]"
}
case_O29_c52_only_night() {
  # The C5.2 night (D62(c)), default set (D62(b)): the C1.16 rig, then the full C5.2 list on both
  # OSes in its chunks, nothing else.
  export C8_C52_ONLY=1
  overnight; rc=$?
  check "exit 0" test "$rc" = 0
  check "only the C5.2 night's steps, in order" test "$(awk -F'\t' 'NR > 1 { print $1 }' "$W/ev/power.tsv" | tr '\n' ' ')" = "c116-rig $C52_ALL "
  check "every step VALID" sh -c '[ "$(awk -F"\t" "NR > 1 && \$4 != \"VALID\"" "$1" | wc -l)" = 0 ]' sh "$W/ev/power.tsv"
  check "the only phase3 step is the rig" sh -c '[ "$(grep "^phase3 " "$1")" = "phase3 $2 $3 c116-rig" ]' sh "$CALLS" "$P/qompack-cx-cand" "$W/ev"
  check "no release-check" lacks "$W/ev/chain.log" "release-check"
  check "the Windows chunks measure the full list" full_c52 win
  check "the Linux chunks measure the full list" full_c52 linux
  for o in win linux; do
    for g in observer store checkpoint; do check "c52-$o-$g measures $g alone" test "$(chunk_call "c52-$o-$g")" = "PKGS=$g FILTER="; done
    check "c52-$o-other measures the rest, in list order" test "$(chunk_call "c52-$o-other")" = "PKGS=daemon cli FILTER="
    check "c52-$o-other's evidence in its own directory" test -s "$W/ev/quiet-c52-$o-other/c52-$o.json"
  done
  check "and says what it is" has "$W/ev/chain.log" "c52: the FULL C5.2 list is measured: all 7 rows of quiet.sh's benches() list (5 packages), in chunks whose packages together are exactly the list's (observer store checkpoint one chunk each, other the rest), with QUIET_BENCH_FILTER empty (C8_C52_SET=full, D62(b)"
  check "no derivation" sh -c '! grep -q "go-cov " "$1" && [ ! -e "$2" ]' sh "$CALLS" "$W/ev/c52-derive"
  check "the rig before C5.2" sh -c 'a=$(grep -n "step c116-rig try 1 start" "$1" | cut -d: -f1); b=$(grep -n "step c52-win-observer try 1 start" "$1" | cut -d: -f1); [ -n "$a" ] && [ -n "$b" ] && [ "$a" -lt "$b" ]' sh "$W/ev/chain.log"
  check "the container is started for each Linux chunk" sh -c '[ "$(grep -c "^docker start qompack-v6-linux-verification" "$1")" = 4 ]' sh "$CALLS"
  check "the mode is logged" hasre "$W/ev/chain.log" "^start candidate=.* mode=c52-only c52_set=full steps=\[c116-rig $C52_ALL\] "
  check "nothing pending" lacks "$W/ev/overnight-outcome.txt" "pending"
}
case_O31_estimate_rechecked_after_the_ac_wait() {
  # On battery, the rig fits before its wait (22:00 + 65 min against 23:10) but not after the wait
  # has spent the budget (23:00 + 65 min): it must be SKIPPED, not started on battery to run past
  # the deadline. C5.2's steps then cannot fit either.
  timeline "0 BAT"; export C8_C52_ONLY=1 AC_WAIT_BUDGET_MIN=60 NIGHT_DEADLINE=23:10
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "the wait spent the budget" has "$W/ev/chain.log" "wait budget is spent (60 of 60 min"
  check "the rig SKIPPED after the wait" test "$(row c116-rig 1)" = SKIPPED
  check "for its estimate" has "$W/ev/chain.log" "needs about 65 min (C116_EST_S"
  for s in $C52_ALL; do check "$s SKIPPED" test "$(row "$s" 1)" = SKIPPED; done
  check "the rig never started" lacks "$CALLS" "phase3 "
  check "no C5.2 measurement started" sh -c '! grep -qE "^quiet .*c52-(win|linux)$" "$1"' sh "$CALLS"
}
case_O30_c52_declarations_and_benchtime() {
  # ExtractDecisions' trace reaches only checkpoint's other.go; intent.go changed in the same package.
  # A declaration-only change (a struct field, a const) has no coverage block, so a changed file in a
  # package the benchmark executes selects it. Each row is traced at its own listed benchtime.
  export C8_C52_ONLY=1 C8_C52_SET=derived
  kv cov_ExtractDecisions internal/checkpoint/other.go
  overnight
  check "a changed file in an executed package selects it" hasre "$W/ev/c52-derive/selection.tsv" "BenchmarkExtractDecisions	.*a changed file in a package it executes internal/checkpoint/intent.go"
  check "Finalize traced at 10x" hasre "$CALLS" "-bench \^BenchmarkFinalize\\$ -benchtime=10x "
  check "ExtractDecisions traced at 1s" hasre "$CALLS" "-bench \^BenchmarkExtractDecisions\\$ -benchtime=1s "
  check "line-level evidence is reported" hasre "$W/ev/c52-derive/report.txt" "internal/checkpoint/intent.go: executed blocks over changed lines: none \(changed lines 3\)"
  check "the owner's question is counted" has "$W/ev/c52-derive/report.txt" "# selected with no executed block on a changed line"
}
case_O32_c52_night_derived_set() {
  # C8_C52_SET=derived (D57(e) by construction): the rows whose executed files changed since
  # C8_PREV_CANDIDATE, and only those, on both OSes, after the rig.
  export C8_C52_ONLY=1 C8_C52_SET=derived
  overnight; rc=$?
  check "exit 0" test "$rc" = 0
  for s in c116-rig c52-derive c52-win-checkpoint c52-win-other c52-linux-checkpoint c52-linux-other; do check "$s VALID" test "$(row "$s" 1)" = VALID; done
  f='FILTER=^Benchmark(Finalize|ExtractDecisions|SchedulerTap_ObserveTool)$'
  for o in win linux; do
    check "c52-$o-checkpoint measures its selected rows" test "$(chunk_call "c52-$o-checkpoint")" = "PKGS=checkpoint $f"
    check "c52-$o-other measures daemon's" test "$(chunk_call "c52-$o-other")" = "PKGS=daemon $f"
    for g in observer store; do
      check "c52-$o-$g has no selected row" test -z "$(row "c52-$o-$g" 1)"
      check "and says so" has "$W/ev/chain.log" "c52: c52-$o-$g: no selected row in its packages (D57(e)), so it is not needed"
    done
  done
  check "against cf31e01" hasre "$CALLS" "quiet .* cf31e01 .*/quiet-c52-win-checkpoint c52-win"
  check "the derived set is logged" has "$W/ev/chain.log" "c52: derived set (C8_C52_SET=derived"
  check "the derived estimates are logged" has "$W/ev/chain.log" "c52: tonight's chunks need about"
  check "the derivation's selection is kept" sh -c '[ "$(tail -n +2 "$1" | wc -l)" = 3 ]' sh "$W/ev/c52-derive/selection.tsv"
  check "an unchanged row is not measured" lacks "$W/ev/c52-derive/selection.tsv" "PutBytes_100KB_Warm"
  check "the container is started for the two Linux chunks" sh -c '[ "$(grep -c "^docker start qompack-v6-linux-verification" "$1")" = 2 ]' sh "$CALLS"
}
case_O33_unknown_c52_set_refused() {
  export C8_C52_ONLY=1 C8_C52_SET=floor
  overnight 2> "$W/err"; rc=$?
  check "exit 2" test "$rc" = 2
  check "refused by name" has "$W/err" "C8_C52_SET must be full or derived, not 'floor'"
  check "nothing written" test ! -e "$W/ev/chain.log"
  check "no step ran" sh -c '! grep -qE "^(phase3|quiet|go) " "$1"' sh "$CALLS"
}
case_O34_c116_rig_invalid_power_retried() {
  # The rig is AC-gated: a source change during it voids try 1, whose records move aside.
  export C8_C52_ONLY=1
  printf 'adv 30\nflip BAT\nadv 30\nflip AC\nadv 30\n' > "$SCEN_DIR/p3.c116-rig.1"
  overnight; rc=$?
  check "exit 0" test "$rc" = 0
  check "try 1 INVALID-POWER" sh -c 'case "$1" in INVALID-POWER*) ;; *) exit 1 ;; esac' sh "$(row c116-rig 1)"
  check "try 2 VALID" test "$(row c116-rig 2)" = VALID
  check "try 1's records moved aside" test -s "$W/ev/c116-rig.invalid-power-1/p3-c116-rig.json"
  check "try 2's record" test -s "$W/ev/p3-c116-rig.json"
}
case_O35_c52_switches_are_exact() {
  # A mistyped C5.2 night switch must not run candidate 8's night (release-check included) on the
  # frozen candidate, and C8_C52_STEPS must name C5.2 night steps, in a C5.2 night.
  try() { # try <dir> <expected refusal>: exit 2, the refusal, nothing written, no step
    overnight_in "$1" 2> "$W/$1.err"; t_rc=$?
    check "$1: exit 2" test "$t_rc" = 2
    check "$1: refused by name" has "$W/$1.err" "$2"
    check "$1: nothing written" test ! -e "$W/$1/chain.log"
  }
  overnight_in() { sh "$COORD/overnight-c8.sh" "$P/qompack-cx-cand" "$CAND_SHA" "$W/$1"; }
  export C8_C52_ONLY=true; try ev1 "C8_C52_ONLY must be empty or 1, not 'true'"
  export C8_C52_ONLY=' 1'; try ev2 "C8_C52_ONLY must be empty or 1, not ' 1'"
  export C8_C52_ONLY=1 C8_C52_STEPS="c52-win-store c52-win"; try ev3 "C8_C52_STEPS: 'c52-win' is not a C5.2 night step"
  unset C8_C52_ONLY; export C8_C52_STEPS=c52-win-store; try ev4 "C8_C52_STEPS ('c52-win-store') belongs to a C5.2 night (C8_C52_ONLY=1)"
  check "no step ran in any of them" sh -c '! grep -qE "^(phase3|quiet|go) " "$1"' sh "$CALLS"
}
case_O36_c52_night_runs_only_the_named_steps() {
  # A later C5.2 night measures only what an earlier one left: C8_C52_STEPS names those steps,
  # in any order; they run in the plan's.
  export C8_C52_ONLY=1 C8_C52_STEPS="c52-linux-other c52-win-store"
  overnight; rc=$?
  check "exit 0" test "$rc" = 0
  check "exactly the named steps ran" test "$(awk -F'\t' 'NR > 1 { print $1 }' "$W/ev/power.tsv" | tr '\n' ' ')" = "c52-win-store c52-linux-other "
  check "both VALID" sh -c '[ "$(awk -F"\t" "NR > 1 && \$4 != \"VALID\"" "$1" | wc -l)" = 0 ]' sh "$W/ev/power.tsv"
  check "no rig" lacks "$CALLS" "phase3 "
  check "the store chunk's packages" test "$(chunk_call c52-win-store)" = "PKGS=store FILTER="
  check "the other chunk's packages" test "$(chunk_call c52-linux-other)" = "PKGS=daemon cli FILTER="
  check "two quiet.sh calls" sh -c '[ "$(grep -c "^quiet " "$1")" = 2 ]' sh "$CALLS"
  check "the steps are logged" hasre "$W/ev/chain.log" "^start candidate=.* steps=\[c52-win-store c52-linux-other\] "
  check "and what tonight measures of the list" has "$W/ev/chain.log" "tonight only C8_C52_STEPS: c52-linux-other c52-win-store"
  check "in the plan's order" has "$W/ev/chain.log" "only these steps run, in this order: c52-win-store c52-linux-other"
}
case_O37_c52_power_event_voids_one_chunk() {
  # A source change during one chunk (Windows store, quiet.sh's second c52-win call) voids that
  # chunk alone: it is retried, and every other chunk keeps its own VALID try.
  export C8_C52_ONLY=1
  printf 'adv 60\nflip BAT\nadv 60\nflip AC\nadv 60\n' > "$SCEN_DIR/q.c52-win.2"
  overnight; rc=$?
  check "exit 0" test "$rc" = 0
  check "c52-win-store try 1 INVALID-POWER" sh -c 'case "$1" in INVALID-POWER*) ;; *) exit 1 ;; esac' sh "$(row c52-win-store 1)"
  check "c52-win-store try 2 VALID" test "$(row c52-win-store 2)" = VALID
  check "try 1's chunk directory moved aside" test -s "$W/ev/c52-win-store.invalid-power-1/quiet-c52-win-store/c52-win.json"
  check "try 2's chunk directory" test -s "$W/ev/quiet-c52-win-store/c52-win.json"
  for s in c52-win-observer c52-win-checkpoint c52-win-other c52-linux-store; do
    check "$s: one VALID try" sh -c '[ "$(awk -F"\t" -v s="$2" "\$1 == s" "$1" | cut -f4 | tr "\n" " ")" = "VALID " ]' sh "$W/ev/power.tsv" "$s"
  done
  check "the chunk before it was not moved" test -s "$W/ev/quiet-c52-win-observer/c52-win.json"
}
case_O38_c52_group_missing_from_the_list() {
  # quiet.sh's list decides the chunks: a group with no listed package has no chunk, and the
  # chunks still measure exactly the list.
  export C8_C52_ONLY=1
  sed -i '/^observer OnToolUse_TestOutput256KB /d' "$COORD/quiet.sh"
  overnight; rc=$?
  check "exit 0" test "$rc" = 0
  check "no observer chunk ran" sh -c '[ -z "$(awk -F"\t" "\$1 ~ /observer/" "$1")" ]' sh "$W/ev/power.tsv"
  check "and says why" has "$W/ev/chain.log" "c52: c52-win-observer: quiet.sh's list has no package in its group, so it has nothing to measure"
  check "the Windows chunks measure the list" full_c52 win "checkpoint cli daemon store"
  check "the Linux chunks measure the list" full_c52 linux "checkpoint cli daemon store"
}
case_O39_c52_night_needs_a_readable_list() {
  # The chunks are made from quiet.sh's benches() list: a C5.2 night refuses without it, before
  # writing anything; candidate 8's night does not read it.
  sed -i "s/^benches() { cat <<'EOS'\$/benchez() { cat <<'EOS'/" "$COORD/quiet.sh"
  export C8_C52_ONLY=1
  overnight 2> "$W/err"; rc=$?
  check "exit 2" test "$rc" = 2
  check "refused by name" has "$W/err" "cannot read quiet.sh's benches() list"
  check "nothing written" test ! -e "$W/ev/chain.log"
  unset C8_C52_ONLY
  sh "$COORD/overnight-c8.sh" "$P/qompack-cx-cand" "$CAND_SHA" "$W/ev2"; rc=$?
  check "candidate 8's night runs without it" test "$rc" = 0
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
  check "e2efunc strict, without test/e2e's timing rows" has "$CALLS" " -skip ^(TestV3_HotPathUnchangedWithLedgerResident|TestE2E_SessionStartLatency|TestV5_ThrashWarningVisibleInStatusAndCheckpoint)\$ ./test/e2e"
  check "e2efunc under no declaration" hasre "$CALLS" "^go COLOAD=unset NONREF=unset .* -skip .* ./test/e2e$"
  check "then X10's detector arm by itself" has "$CALLS" " -run ^TestV5_ThrashWarningVisibleInStatusAndCheckpoint\$/^state_aware_detector_is_progress_aware_and_cannot_feed_itself\$ ./test/e2e"
  check "the skip list is a precondition" has "$L8" "precondition: e2efunc will skip -skip '^(TestV3_HotPathUnchangedWithLedgerResident|TestE2E_SessionStartLatency|TestV5_ThrashWarningVisibleInStatusAndCheckpoint)\$'"
  check "the overnight part ends with release-check" has "$L8" "pending_c52_night=[c116-rig $C52_ALL]"
  check "the keep-awake is confirmed" has "$L8" "keep-awake held (keepawake.log)"
  check "no launch warning" lacks "$L8" "WARNING"
  check "the disk precondition is logged" has "$L8" "precondition: disk: "
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
  export NIGHT_DEADLINE=22:10 NIGHT_ALLOW_LATE=1   # 10 min away: too near without the override (X12)
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
case_N20_long_paths_in_the_scratch_clones() {
  # A tracked path that leaves a mktemp clone past Windows' MAX_PATH (260): git for Windows ships
  # with core.longpaths unset, so the merged-tree and release-check clones must set it themselves.
  # The scratch repository sets it for its own worktrees, as the real one would have to.
  git -C "$P/qompack" config core.longpaths true
  lp="plans/sdd/$(printf '%0100d' 0 | tr 0 a)/$(printf '%0100d' 0 | tr 0 b).md"
  (cd "$P/qompack-cx-int" && mkdir -p "$(dirname "$lp")" && echo long > "$lp" && git add . &&
    git -c user.name=h -c user.email=h@i commit -q -m "a long path")
  night; rc=$?
  check "exit 0" test "$rc" = 0
  check "the merged tree was made" has "$E8/night.log" "passes plan lint"
  check "frozen" has "$E8/night.log" "candidate 8 frozen at"
  check "release-check ran in its clone" test "$(row8 release-check 1)" = VALID
}
row8() { awk -F'\t' -v s="$1" -v n="$2" '$1 == s && $2 == n { print $4 }' "$E8/power.tsv"; }
case_N21_e2e_skip_drift_refuses_before_anything() {
  # X11 is renamed on integration: e2efunc's named skip list no longer matches the tree, and the
  # night refuses among its preconditions, not an hour into the pre-freeze.
  (cd "$P/qompack-cx-int" && sed -i 's/TestV3_HotPathUnchangedWithLedgerResident/TestV3_HotPathRenamed/' test/e2e/rows_test.go &&
    git -c user.name=h -c user.email=h@i commit -q -am "rename X11")
  v0=$(git -C "$P/qompack-v6" rev-parse HEAD)
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "the drift is named" has "$E8/night.log" "test/e2e no longer defines TestV3_HotPathUnchangedWithLedgerResident"
  check "refused as a precondition" has "$E8/night.log" "REFUSED: precondition: e2efunc's skip list of test/e2e's timing rows has drifted"
  check "no merged tree, no pre-freeze" sh -c '! grep -q "merged-tree scratch clone" "$1" && [ ! -e "$2" ]' sh "$E8/night.log" "$E8/prefreeze"
  check "nothing frozen" test "$(git -C "$P/qompack-v6" rev-parse HEAD)" = "$v0"
  check "keep-awake released" test ! -e "$E8/keepawake.sentinel"
}
case_N22_c52_only_switch_refuses() {
  # A C5.2 night's switch left in the launching window would make the overnight part a C5.2 night.
  export C8_C52_ONLY=1
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "refused by name" has "$E8/night.log" "REFUSED: C8_C52_ONLY is set ('1')"
  check "no keep-awake started" lacks "$CALLS" "pwsh "
  check "nothing ran" sh -c '! grep -qE "^(phase3|quiet|go) " "$1"' sh "$CALLS"
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
  check "e2efunc takes the declaration back" hasre "$CALLS" "^go COLOAD=unset NONREF=unset .*-skip \^\(TestV3_HotPath"
  check "integration declares co-load" hasre "$CALLS" "^go COLOAD=1 NONREF=unset .* ./test/integration"
  check "power recorded per step" hasre "$W/pf/summary.log" "^step e2efunc exit=0 run=r2 .* power=VALID$"
  check "a bad run id is refused" sh -c '! PREFREEZE_RUN="a b" sh "$1" "$2" "$3" gate' sh "$COORD/prefreeze.sh" "$P/qompack-cx-int" "$W/pf2"
}

e2e_call() { grep -E "^go COLOAD=[^ ]* NONREF=[^ ]* test .* \./test/e2e$" "$CALLS"; }   # e2efunc's go test, if it ran
E2E_SKIP='^(TestV3_HotPathUnchangedWithLedgerResident|TestE2E_SessionStartLatency|TestV5_ThrashWarningVisibleInStatusAndCheckpoint)$'
X10_ARM_RUN='^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$/^state_aware_detector_is_progress_aware_and_cannot_feed_itself$'
e2e_main() { e2e_call | grep -e ' -skip '; }   # e2efunc's main pass
e2e_arm() { e2e_call | grep -e ' -run '; }     # e2efunc's functional-arm passes
case_F5_e2efunc_skips_exactly_the_timing_rows() {
  # e2efunc skips test/e2e's three timing rows by their exact names, then runs X10's detector arm,
  # which judges no wall clock, by itself.
  pf r1 e2efunc
  check "e2efunc passes" has "$W/pf/summary.log" "step e2efunc exit=0 run=r1 "
  check "the main pass skips the timing rows, nothing else" test "$(e2e_main)" = "go COLOAD=unset NONREF=unset test -p 1 -count=1 -timeout 90m -skip $E2E_SKIP ./test/e2e"
  check "then the detector arm alone, undeclared" test "$(e2e_arm)" = "go COLOAD=unset NONREF=unset test -p 1 -count=1 -timeout 30m -v -run $X10_ARM_RUN ./test/e2e"
  check "the log names the pattern" has "$W/pf/e2efunc.log" "-skip '$E2E_SKIP'"
  check "and the arm's own PASS" has "$W/pf/e2efunc.log" "--- PASS: TestV5_ThrashWarningVisibleInStatusAndCheckpoint/state_aware_detector_is_progress_aware_and_cannot_feed_itself "
  out=$(sh "$COORD/prefreeze.sh" --e2e-skips "$P/qompack-cx-int"); rc=$?
  check "--e2e-skips: exit 0" test "$rc" = 0
  check "--e2e-skips prints the same pattern" test "$out" = "$E2E_SKIP"
  # Every row e2efunc skips is judged alone by the night: X11 by win-x11-alone's exact -run, the
  # others by win-e2e-timing, which runs all of test/e2e with no -run or -skip.
  check "X11 by itself" grep -qF -- "-run '^TestV3_HotPathUnchangedWithLedgerResident\$' ./test/e2e" "$here/phase3.sh"
  # -timeout=45m since wave 22 (audit 2 #53/#84): a budget for the whole binary, which judges nothing.
  check "the rest in all of test/e2e" grep -qxF -- "    win-e2e-timing) rec p3-win-e2e-timing -- go test -count=1 -timeout=45m ./test/e2e ;;" "$here/phase3.sh"
}
case_F6_e2efunc_named_row_drift_does_not_run() {
  sed -i 's/TestE2E_SessionStartLatency/TestE2E_SessionStartLatencyRenamed/' "$P/qompack-cx-int/test/e2e/rows_test.go"
  pf r1 e2efunc
  check "e2efunc exit 2" has "$W/pf/summary.log" "step e2efunc exit=2 run=r1 "
  check "the drift is named" has "$W/pf/e2efunc.log" "test/e2e no longer defines TestE2E_SessionStartLatency"
  check "test/e2e did not run" test -z "$(e2e_call)"
  sh "$COORD/prefreeze.sh" --e2e-skips "$P/qompack-cx-int" > "$W/out" 2> "$W/err"; rc=$?
  check "--e2e-skips: exit 2" test "$rc" = 2
  check "--e2e-skips prints no pattern" test ! -s "$W/out"
  : > "$CALLS"; sed -i 's/TestV3_HotPathUnchangedWithLedgerResident/TestV3_HotRenamed/; s/TestE2E_SessionStartLatencyRenamed/TestE2E_SessionStartLatency/' "$P/qompack-cx-int/test/e2e/rows_test.go"
  pf r2 e2efunc
  check "X11 by its exact name is drift too" has "$W/pf/e2efunc.log" "test/e2e no longer defines TestV3_HotPathUnchangedWithLedgerResident"
  check "and test/e2e did not run" test -z "$(e2e_call)"
}
case_F8_e2efunc_functional_arm_must_pass_and_exist() {
  # The arm's -run would exit 0 matching nothing, so its own PASS line is required; and an arm
  # test/e2e no longer holds is drift, as a renamed row is.
  kv omit_arm state_aware_detector_is_progress_aware_and_cannot_feed_itself
  pf r1 e2efunc
  check "no PASS line: e2efunc fails" has "$W/pf/summary.log" "step e2efunc exit=1 run=r1 "
  check "and names the arm" has "$W/pf/e2efunc.log" "TestV5_ThrashWarningVisibleInStatusAndCheckpoint/state_aware_detector_is_progress_aware_and_cannot_feed_itself did not pass (or did not run)"
  : > "$SCEN_DIR/kv"; : > "$CALLS"; rm -f "$SCEN_DIR/count.test_e2e"; kv rc_e2e_2 1   # call 2: the arm
  pf r2 e2efunc
  check "a red arm run: e2efunc fails" has "$W/pf/summary.log" "step e2efunc exit=1 run=r2 "
  : > "$SCEN_DIR/kv"; : > "$CALLS"
  sed -i 's/state_aware_detector_is_progress_aware_and_cannot_feed_itself/state_aware_detector_renamed/' "$P/qompack-cx-int/test/e2e/rows_test.go"
  pf r3 e2efunc
  check "a renamed arm is drift: exit 2" has "$W/pf/summary.log" "step e2efunc exit=2 run=r3 "
  check "named" has "$W/pf/e2efunc.log" "test/e2e no longer holds the arm t.Run(\"state_aware_detector_is_progress_aware_and_cannot_feed_itself\", ...)"
  check "and test/e2e did not run" test -z "$(e2e_call)"
}
case_F7_e2efunc_follows_ci_yml_timing_lane() {
  # A test/e2e row ci.yml's timing lane names is judged alone on AC by phase3.sh's win-timing, so
  # e2efunc skips it by construction; a lane row test/e2e does not define is not added.
  printf "jobs:\n  timing:\n    steps:\n      - run: go test -p 1 -count=1 -timeout=30m -run '^(TestBudgetBF|TestE2EHookRoundTrip|TestNotInE2E)\$' ./internal/mcp ./test/e2e\n" \
    > "$P/qompack-cx-int/.github/workflows/ci.yml"
  pf r1 e2efunc
  check "e2efunc passes" has "$W/pf/summary.log" "step e2efunc exit=0 run=r1 "
  check "the lane's test/e2e row is skipped, after the named ones" has "$CALLS" " -skip ^(TestV3_HotPathUnchangedWithLedgerResident|TestE2E_SessionStartLatency|TestV5_ThrashWarningVisibleInStatusAndCheckpoint|TestE2EHookRoundTrip)\$ ./test/e2e"
  check "a lane row outside test/e2e is not" lacks "$CALLS" "TestNotInE2E"
  printf "jobs:\n  timing:\n    steps:\n      - run: go test -p 2 ./...\n" > "$P/qompack-cx-int/.github/workflows/ci.yml"
  : > "$CALLS"; pf r2 e2efunc
  check "an unreadable timing lane is drift" has "$W/pf/summary.log" "step e2efunc exit=2 run=r2 "
  check "and says so" has "$W/pf/e2efunc.log" "cannot read ci.yml's timing lane"
  check "test/e2e did not run" test -z "$(e2e_call)"
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
case_P8_linux_child_declares_coload_as_nightly() {
  # nightly.yml's race-product-child sets QOMPACK_UNDER_COLOAD=1 for the same eight rows under
  # -race; the local lane that mirrors it must declare the same, or a race-slowed wall-clock row is
  # judged as if it ran alone.
  p3_world; mkdir -p "$PR/plans/sdd/V6-closeout/linux"
  printf '#!/bin/sh\n. "$HB/_lib.sh"; call "gate $*"\n' > "$PR/plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh"
  p3 linux-child; rc=$?
  check "exit 0" test "$rc" = 0
  check "the child lane declares co-load" hasre "$CALLS" "^gate .* p3-linux-child --no-race --coload --env CGO_ENABLED=1 --env GOFLAGS=-race --env QOMPACK_REQUIRE_CHILD_RACE=1 "
  p3 linux-timing; rc=$?
  check "linux-timing exit 0" test "$rc" = 0
  check "the timing lane still judges alone" sh -c 'grep -q "^gate .* p3-linux-timing " "$1" && ! grep -q "^gate .* p3-linux-timing .*--coload" "$1"' sh "$CALLS"
}

case_P9_c116_rig_reproduces_w2_lifetime() {
  # w2-lifetime's procedure: 30 cycles, 8 feeders, 256 KiB Reads; no extra load, then the rig's own
  # in-process co-load (16 fsync writers, 4 spinners); no declaration on either.
  p3_world; p3 c116-rig; rc=$?
  check "exit 0" test "$rc" = 0
  check "two runs" sh -c '[ "$(grep -c "^go-c116 " "$1")" = 2 ]' sh "$CALLS"
  check "the no-extra run" has "$CALLS" "go-c116 n=1 ROUNDS=30 WORKERS=8 READ=262144 FSYNC=unset CPU=unset COLOAD=unset"
  check "the co-load run" has "$CALLS" "go-c116 n=2 ROUNDS=30 WORKERS=8 READ=262144 FSYNC=16 CPU=4 COLOAD=unset"
  check "its exact -run" has "$CALLS" "test ./internal/cli/ -run ^TestSessionStartCompact_UnderSameSessionIngest\$ -count=1 -v -timeout=30m"
  check "a record per run" sh -c '[ -s "$1/p3-c116-rig-noextra.json" ] && [ -s "$1/p3-c116-rig-coload.json" ]' sh "$PE"
  check "the distributions in the output" sh -c '[ "$(grep -c "^c116-rig [a-z]*: compact SessionStart wall (n=30): " "$1")" = 2 ]' sh "$W/p3.out"
  check "the routing lines in the output" sh -c '[ "$(grep -c "^c116-rig [a-z]*: of the rig.s 271 Reads: 250 indexed" "$1")" = 2 ]' sh "$W/p3.out"
}
case_P10_c116_rig_needs_its_own_evidence() {
  p3_world; kv rc_c116_2 1; p3 c116-rig; rc=$?
  check "a failed co-load run: exit 1" test "$rc" = 1
  check "step c116-rig exit=1" has "$W/p3.out" "step c116-rig exit=1"
  for om in pass dist reads; do
    : > "$SCEN_DIR/kv"; : > "$CALLS"; rm -rf "$PE"; echo 0 > "$SCEN_DIR/count.c116"; kv c116_omit "$om"
    p3 c116-rig; rc=$?
    check "no $om line: exit 1" test "$rc" = 1
  done
  check "a rig without its routing check is named" has "$W/p3.out" "no Read-routing line (a rig before 3f2da1b3?)"
}

# ---- G, X, Q: wave 22 (audit 2's night findings) -------------------------------------------------
# G1 (#48): the stub guard every case runs behind.
case_G1_stub_guard_refuses_a_bypassed_stub() {
  check "the scratch path holds no colon" sh -c 'case "$1" in *:*) exit 1 ;; esac' sh "$T"
  check "PATH starts with the stub directory" sh -c 'case "$PATH" in "$1":*) ;; *) exit 1 ;; esac' sh "$HB"
  check "every stub resolves there" stub_guard
  mkdir -p "$W/shadow"; printf '#!/bin/sh\nexit 0\n' > "$W/shadow/docker"; chmod +x "$W/shadow/docker"
  ( PATH="$W/shadow:$PATH"; export PATH; stub_guard ) > "$W/g1.out" 2>&1; rc=$?
  check "a shadowed stub is refused" test "$rc" = 1
  check "and named" has "$W/g1.out" "stub guard: docker resolves to '$W/shadow/docker'"
  ( PATH=$(printf '%s' "$PATH" | sed "s#^$HB:##"); export PATH; stub_guard ) > "$W/g1b.out" 2>&1; rc=$?
  check "a PATH without the stubs is refused" test "$rc" = 1
}

# X1-X3 (#47): Docker engine ownership.
case_X1_engine_up_all_night_one_failed_start_probe() {
  # The owner's engine is up all night; the night's first probe fails once, and the first three
  # probes at the Linux lanes fail (audit 2's X1: ps calls 1, 3, 4 and 5).
  kv docker_ps_fail_list "1 3 4 5"
  overnight
  check "up at the start by three probes" has "$W/ev/chain.log" "engine up at start=1"
  check "the owner's engine is never stopped" lacks "$CALLS" "docker desktop stop"
  check "never counted as this chain's" lacks "$W/ev/chain.log" "engine started by this chain"
  check "the lanes ran" has "$W/ev/chain.log" "container start exit=0"
}
case_X2_engine_not_stopped_by_status_is_never_owned() {
  # The engine answers no probe all night, but Docker Desktop says running (a hung engine, not a
  # stopped one): the chain may ask it to start, but it is not the chain's, so it is never stopped.
  kv docker_ps 1; kv docker_status running; kv docker_others none
  overnight
  check "Docker Desktop's status is read" has "$CALLS" "docker desktop status"
  check "the engine is asked to start" has "$CALLS" "docker desktop start"
  check "but it is not this chain's" lacks "$W/ev/chain.log" "engine started by this chain"
  check "so it is never stopped" lacks "$CALLS" "docker desktop stop"
  check "the chain says why" has "$W/ev/chain.log" "Docker Desktop's status is 'running', not stopped"
}
case_X3_engine_started_but_shared_is_left_running() {
  # The engine was down and stopped, the chain started it, and the owner's containers run on it by
  # the time the night's container stops: it is not the chain's alone, so it stays up.
  kv docker_ps 1                       # down at the start; status stopped; owner_db once it is up
  overnight
  check "started by this chain" has "$W/ev/chain.log" "engine started by this chain"
  check "never stopped while other containers run" lacks "$CALLS" "docker desktop stop"
  check "the chain names them" has "$W/ev/chain.log" "other containers run on it (owner_db)"
  # A listing that fails is no proof that nothing else runs on it either.
  : > "$SCEN_DIR/kv"; : > "$CALLS"; rm -f "$SCEN_DIR/engine.up" "$SCEN_DIR/count.dockerps"; echo "$START" > "$FAKE_CLOCK"
  kv docker_ps 1; kv docker_others none; kv docker_ps_fail_list "8 9 10 11 12 13 14 15 16 17 18 19 20"
  sh "$COORD/overnight-c8.sh" "$P/qompack-cx-cand" "$CAND_SHA" "$W/ev2"
  check "an unlisted engine is never stopped" lacks "$CALLS" "docker desktop stop"
  check "and the chain says so" has "$W/ev2/chain.log" "its containers could not be listed"
}

# X4 (#55): every PowerShell query is bounded.
case_X4_power_queries_are_bounded() {
  . "$here/power.sh"
  power_read > /dev/null
  check "a reading runs under timeout" hasre "$CALLS" "^timeout -k 10 120 powershell -NoProfile -NonInteractive -Command "
  kv timeout_match "powershell"
  check "a timed-out reading is UNKNOWN" test "$(power_read)" = "UNKNOWN ?"
  check "a timed-out event query is unreadable" sh -c '. "$1"; ! power_events_since 1' sh "$here/power.sh"
  check "so the verdict is NOT-REFERENCE" test "$(power_verdict "AC 50" 0 "" | cut -d' ' -f1)" = NOT-REFERENCE
}

# X5, X6 (#51): a signal runs the trap at once, while the child is still running.
case_X5_signal_runs_the_night_trap_at_once() {
  kv block_on build
  sh "$COORD/c8-night.sh" & pid=$!
  "$REALTIMEOUT" 600 sh -c 'read x < "$1"' sh "$SCEN_DIR/ready.fifo"
  check "the sentinel exists mid-run" test -e "$E8/keepawake.sentinel"
  kill -TERM "$pid"
  # Wait for the night to end while its child is still blocked (the timeout is only a hang guard).
  "$REALTIMEOUT" 120 sh -c 'while kill -0 "$1" 2> /dev/null; do /usr/bin/sleep 0.2; done' sh "$pid"
  gone=1; kill -0 "$pid" 2> /dev/null && gone=0
  sent=0; [ -e "$E8/keepawake.sentinel" ] && sent=1
  echo go > "$SCEN_DIR/go.fifo"        # release the blocked stub
  wait "$pid"; rc=$?
  check "the night ended while its child was still blocked" test "$gone" = 1
  check "exit 143" test "$rc" = 143
  check "the sentinel was deleted at once" test "$sent" = 0
  check "the child was stopped" has "$E8/night.log" "signal: stopped the running child"
}
case_X6_signal_runs_the_overnight_trap_at_once() {
  rc_script "block" "adv 1" "adv 1" > "$SCEN_DIR/rc.1"
  sh "$COORD/overnight-c8.sh" "$P/qompack-cx-cand" "$CAND_SHA" "$W/ev" & pid=$!
  "$REALTIMEOUT" 600 sh -c 'read x < "$1"' sh "$SCEN_DIR/ready.fifo"
  d=$(rc_clones "$W/ev/chain.log" | head -n 1)
  check "the clone exists mid-run" test -d "$d/repo"
  kill -TERM "$pid"
  "$REALTIMEOUT" 120 sh -c 'while kill -0 "$1" 2> /dev/null; do /usr/bin/sleep 0.2; done' sh "$pid"
  gone=1; kill -0 "$pid" 2> /dev/null && gone=0
  left=0; [ -e "$d" ] && left=1
  echo go > "$SCEN_DIR/go.fifo"
  wait "$pid"; rc=$?
  check "the night ended while release-check was still blocked" test "$gone" = 1
  check "exit 143" test "$rc" = 143
  check "the clone was removed at once" test "$left" = 0
  check "no step after the signal" lacks "$W/ev/chain.log" "step c52-"
}

# X7, X8 (D65(c)): a C5.2 chunk never runs on battery.
case_X7_c52_chunk_waits_for_ac_past_the_budget() {
  # The budget (30 min) is spent by the rig's wait; AC returns at 23:59:30 (between two polls: an event
  # in the second a step takes its t0 counts against it), and the chunks wait for it
  # rather than run on battery.
  timeline "0 BAT" "$(at 7170) AC"; export C8_C52_ONLY=1 AC_WAIT_BUDGET_MIN=30 NIGHT_DEADLINE=02:00
  overnight; rc=$?
  check "exit 1 (the rig ran on battery)" test "$rc" = 1
  check "the rig NOT-REFERENCE after the budget" sh -c 'case "$1" in NOT-REFERENCE*) ;; *) exit 1 ;; esac' sh "$(row c116-rig 1)"
  for s in $C52_ALL; do check "$s VALID" test "$(row "$s" 1)" = VALID; done
  check "no chunk started on battery" sh -c '! grep -qE "step c52-[a-z-]+ try [0-9] start power=BAT" "$1"' sh "$W/ev/chain.log"
  check "the chunks waited past the budget" has "$W/ev/chain.log" "no budget applies, D65(c)"
}
case_X8_c52_chunk_never_runs_on_battery() {
  timeline "0 BAT"; export C8_C52_ONLY=1 C8_C52_STEPS="c52-win-other c52-linux-other" AC_WAIT_BUDGET_MIN=10 NIGHT_DEADLINE=23:30
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  for s in c52-win-other c52-linux-other; do check "$s SKIPPED" test "$(row "$s" 1)" = SKIPPED; done
  check "never NOT-REFERENCE" sh -c '! awk -F"\t" "NR > 1 { print \$4 }" "$1" | grep -q NOT-REFERENCE' sh "$W/ev/power.tsv"
  check "no chunk started" sh -c '! grep -q "^quiet " "$1"' sh "$CALLS"
  check "the reason names D65(c)" has "$W/ev/chain.log" "no AC by its latest start"
  check "it waited past the budget" has "$W/ev/chain.log" "no budget applies, D65(c)"
  check "left for the next C5.2 night" has "$W/ev/chain.log" "a later C5.2 night (C8_C52_ONLY=1, C8_C52_STEPS naming it) measures it"
}

# X9-X11 (D65(b)): candidate 8's night runs C5.2 chunks after release-check when they fit.
case_X9_night1_c52_chunks_that_do_not_fit_are_left() {
  unset C8_NIGHT1_C52                   # the production default: on
  # release-check runs 23:00-01:06:43; the deadline is 02:30. Windows observer (93 min) and Linux
  # store (71 min) cannot end by then and are left; the others fit and run, in order.
  export NIGHT_DEADLINE=02:30
  overnight; rc=$?
  check "exit 0 (a chunk left over counts neither way)" test "$rc" = 0
  for s in c52-win-store c52-win-checkpoint c52-win-other c52-linux-observer c52-linux-checkpoint c52-linux-other; do
    check "$s VALID" test "$(row "$s" 1)" = VALID
  done
  for s in c52-win-observer c52-linux-store; do check "$s did not run" test -z "$(row "$s" 1)"; done
  check "the reason" has "$W/ev/chain.log" "c52: c52-win-observer needs about 93 min (C52_EST_*) and would end after the deadline"
  check "pending names exactly what is left" has "$W/ev/overnight-outcome.txt" "pending_c52_night=[c116-rig c52-win-observer c52-linux-store]"
  check "skipped=0" has "$W/ev/overnight-outcome.txt" "skipped=0"
}
case_X10_night1_c52_never_on_battery() {
  unset C8_NIGHT1_C52                   # the production default: on
  { default_rc_script; echo "flip BAT"; } > "$SCEN_DIR/rc.1"   # AC is cut as release-check ends
  overnight; rc=$?
  check "exit 0" test "$rc" = 0
  check "release-check VALID" test "$(row release-check 1)" = VALID
  for s in $C52_ALL; do check "$s did not run" test -z "$(row "$s" 1)"; done
  check "no chunk started" sh -c '! grep -q "^quiet .* c52-" "$1"' sh "$CALLS"
  check "the reason" has "$W/ev/chain.log" "c52: c52-win-observer: not on AC (BAT 77), and a chunk never runs on battery (D65(c)): left for the C5.2 night"
  check "every chunk pending" has "$W/ev/overnight-outcome.txt" "pending_c52_night=[c116-rig $C52_ALL]"
}
case_X11_night1_c52_chunk_failing() {
  unset C8_NIGHT1_C52                   # the production default: on
  kv rc_c52-win_2 1                                                  # the store chunk: a VALID red
  printf 'adv 60\nflip BAT\nadv 60\nflip AC\nadv 60\n' > "$SCEN_DIR/q.c52-win.3"   # checkpoint try 1: a power event
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "a VALID red counts as failed" has "$W/ev/overnight-outcome.txt" "failed=1 [c52-win-store]"
  check "the chunks after it still run" test "$(row c52-win-other 1)" = VALID
  check "an INVALID-POWER try is retried" test "$(row c52-win-checkpoint 2)" = VALID
  check "and its retry made it done" sh -c '! grep -q "c52-win-checkpoint was not measured" "$1"' sh "$W/ev/chain.log"
  check "the red is pending, to classify" has "$W/ev/overnight-outcome.txt" "pending_c52_night=[c116-rig c52-win-store]"
  check "no invalid count from the retried chunk" has "$W/ev/overnight-outcome.txt" "invalid_power=0"
  check "the chain says to classify it" has "$W/ev/chain.log" "c52: c52-win-store is a VALID red"
}

case_X22_night1_c52_chunks_that_fit_all_run() {
  # The production default, through c8-night.sh: with AC and time left after release-check, every
  # chunk runs, in order (D65(b)), and the C5.2 night owes only the rig.
  unset C8_NIGHT1_C52
  night; rc=$?
  check "exit 0" test "$rc" = 0
  for s in $C52_ALL; do check "$s VALID" test "$(row8 "$s" 1)" = VALID; done
  check "after release-check, in order" test "$(awk -F'	' 'NR > 1 { print $1 }' "$E8/power.tsv" | sed -n '/^release-check$/,$p' | tr '
' ' ')" = "release-check $C52_ALL "
  check "each chunk with its own packages" has "$CALLS" "quiet PKGS=daemon cli FILTER= "
  check "no rig" lacks "$CALLS" "go-c116 "
  check "only the rig is pending" has "$E8/night.log" "pending_c52_night=[c116-rig]"
  check "the container is started for the lanes, c51-linux and each Linux chunk" sh -c '[ "$(grep -c "^docker start qompack-v6-linux-verification" "$1")" = 6 ]' sh "$CALLS"
  check "the switch off leaves them all" sh -c 'grep -q "C8_NIGHT1_C52 must be empty, 0 or 1" "$1"' sh "$COORD/overnight-c8.sh"
}
# X12 (#56): a deadline too near for the night.
case_X12_too_near_deadline() {
  export NIGHT_DEADLINE=23:00
  v0=$(git -C "$P/qompack-v6" rev-parse HEAD)
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "refused" has "$E8/night.log" "REFUSED: the deadline 2026-10-03T23:00:00 is less than 90 min away"
  check "nothing frozen" test "$(git -C "$P/qompack-v6" rev-parse HEAD)" = "$v0"
  check "no pre-freeze ran" test ! -e "$E8/prefreeze"
  export NIGHT_DEADLINE=03:00
  night; rc=$?
  check "a launch too late for release-check is warned" has "$E8/night.log" "WARNING: release-check will be SKIPPED"
  check "and goes on" has "$E8/night.log" "candidate 8 frozen at"
}

# X13 (#57): a keep-awake that does not hold is warned.
case_X13_keepawake_not_held_is_warned() {
  kv keepawake fail
  night
  check "warned" has "$E8/night.log" "WARNING: the keep-awake does not say it holds"
  check "with its own line" has "$E8/night.log" "keep-awake FAILED"
  check "the night goes on" has "$E8/night.log" "candidate 8 frozen at"
}

# X14 (#59): the disk precondition.
case_X14_disk_precondition() {
  kv disk_free_gb 10
  v0=$(git -C "$P/qompack-v6" rev-parse HEAD)
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "refused" has "$E8/night.log" "REFUSED: precondition: not enough room for the night's clones and test binaries: disk: "
  check "naming the floor" has "$E8/night.log" "10 GiB free, under NIGHT_MIN_FREE_GB=40"
  check "nothing frozen" test "$(git -C "$P/qompack-v6" rev-parse HEAD)" = "$v0"
  check "no pre-freeze ran" test ! -e "$E8/prefreeze"
  overnight 2> "$W/err"; rc=$?
  check "overnight alone: exit 2" test "$rc" = 2
  check "overnight alone: refused" has "$W/err" "REFUSED: not enough room"
  check "overnight alone: nothing written" test ! -e "$W/ev/chain.log"
}

# X15, X16, X17 (#54, #60): release-check at the end of the night.
case_X15_rc_govulncheck_unreachable_is_not_a_product_red() {
  { rc_short; printf '%s\n' 'say === release-check: govulncheck ===' \
      'say govulncheck: fetching vulnerabilities: Get "https://vuln.go.dev/index/db.json.gz": dial tcp: lookup vuln.go.dev: no such host' \
      'say release-check: govulncheck FAIL'; } > "$SCEN_DIR/rc.1"
  kv rc_release_1 1
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "labelled" has "$W/ev/chain.log" "release-check: govulncheck unreachable (network), not a product red"
  check "neither a pass nor a fail" has "$W/ev/overnight-outcome.txt" "failed=0 invalid_power=0 not_reference=1 (of which 1 exited non-zero)"
  check "the note says why" has "$W/ev/chain.log" "the steps after it never ran"
  # A vulnerability is a product red.
  printf '%s\n' 'say === release-check: govulncheck ===' 'say Vulnerability #1: GO-2026-0001' \
    'say release-check: govulncheck FAIL' > "$W/vuln.tail"
  { rc_short; cat "$W/vuln.tail"; } > "$SCEN_DIR/rc.1"; rm -f "$SCEN_DIR/count.devtool_release-check"; echo "$START" > "$FAKE_CLOCK"
  sh "$COORD/overnight-c8.sh" "$P/qompack-cx-cand" "$CAND_SHA" "$W/ev2"
  check "a vulnerability fails" has "$W/ev2/overnight-outcome.txt" "failed=1 [release-check]"
}
case_X16_rc_watchdog_stops_an_overrun() {
  kv timeout_match "stamped.sh"        # the watchdog's timeout fires
  overnight; rc=$?
  check "exit 1" test "$rc" = 1
  check "release-check runs under its watchdog" hasre "$CALLS" "^timeout -k 60 [0-9]+ sh .*/stamped.sh go run ./tools/devtool release-check --tag v0.3.0 "
  check "the limit is the deadline plus RC_GRACE_S" has "$W/ev/chain.log" "stopped if still running at 2026-10-04T08:30:00, the deadline plus RC_GRACE_S"
  check "recorded SKIPPED-OVERRUN" test "$(row release-check 1)" = SKIPPED-OVERRUN
  check "not retried" test -z "$(row release-check 2)"
  check "counted as a skip, not a fail" has "$W/ev/overnight-outcome.txt" "failed=0"
  check "the note names RC_EST_S" has "$W/ev/chain.log" "RC_EST_S (180 min) is too small for this tree"
}
case_X17_rc_waits_for_ac_until_its_latest_start() {
  timeline "0 BAT" "$(at 10800) AC"; export AC_WAIT_BUDGET_MIN=600   # AC returns at 01:00
  overnight
  check "it waited" has "$W/ev/chain.log" "release-check: not on AC; waiting for AC until its latest start 2026-10-04T05:00:00"
  check "try 1 started on AC" has "$W/ev/chain.log" "step release-check try 1 start power=AC"
  check "VALID" test "$(row release-check 1)" = VALID
  check "no second try" test -z "$(row release-check 2)"
}

# X18 (#54): every step ends by the deadline.
case_X18_steps_end_by_the_deadline() {
  export NIGHT_DEADLINE=22:20
  overnight
  check "win-e2e-timing SKIPPED for its estimate" has "$W/ev/chain.log" "step win-e2e-timing try 1 exit=- SKIPPED power=-->-; it needs about 35 min (STEP_EST: candidate 6's time for it"
  check "win-race SKIPPED for its estimate" has "$W/ev/chain.log" "step win-race SKIPPED: it needs about 50 min (STEP_EST"
  check "c51-linux SKIPPED" test "$(row c51-linux 1)" = SKIPPED
  check "the Linux lanes SKIPPED without a container" sh -c '! grep -q "docker start qompack-v6-linux-verification" "$1"' sh "$CALLS"
  check "release-check SKIPPED" has "$W/ev/chain.log" "step release-check SKIPPED"
  dl=$(date -d "2026-10-03 22:20" +%s)
  check "no step ran past the deadline" sh -c 'awk -F"\t" -v d="$2" "NR > 1 && \$8 ~ /^[0-9]+\$/ && \$8 + 0 > d + 0 { bad = 1 } END { exit bad }" "$1"' sh "$W/ev/power.tsv" "$dl"
  check "steps that fit still ran" test "$(row win-timing 1)" = VALID
}

# X19 (#52): the go version is the repository's.
case_X19_go_version_from_the_repo() {
  pf r1 gate
  check "summary.log names the repo's toolchain" has "$W/pf/summary.log" " go=go1.26.6 run=r1 "
  overnight
  check "chain.log's start line too" hasre "$W/ev/chain.log" "^start candidate=.* go=go1.26.6 "
}

# X20 (nit): a tag that shares a branch's name does not hide the branch.
case_X20_branch_and_tag_of_one_name() {
  git -C "$P/qompack" branch closeout/w20-amb main > /dev/null 2>&1
  git -c user.name=h -c user.email=h@i -C "$P/qompack" worktree add -q "$W/amb" closeout/w20-amb > /dev/null 2>&1
  (cd "$W/amb" && echo amb > amb.txt && git add . && git -c user.name=h -c user.email=h@i commit -q -m amb)
  git -C "$P/qompack" tag closeout/w20-amb closeout/w20-amb
  v0=$(git -C "$P/qompack-v6" rev-parse HEAD)
  night; rc=$?
  check "exit 1" test "$rc" = 1
  check "still refused by name" hasre "$E8/night.log" "REFUSED: precondition: closeout/w20-amb \([0-9a-f]+\) is not merged"
  check "nothing frozen" test "$(git -C "$P/qompack-v6" rev-parse HEAD)" = "$v0"
}

# X21 (wave 22): merged closeout/w22-* and w15 branches need no exemption.
case_X21_merged_w22_and_w15_branches_need_no_exemption() {
  for b in closeout/w15-ledger closeout/w22-night; do
    git -C "$P/qompack" branch "$b" main > /dev/null 2>&1
    git -c user.name=h -c user.email=h@i -C "$P/qompack" worktree add -q "$W/${b##*/}" "$b" > /dev/null 2>&1
    (cd "$W/${b##*/}" && echo "$b" > "${b##*/}.txt" && git add . && git -c user.name=h -c user.email=h@i commit -q -m "$b")
    (cd "$P/qompack-cx-int" && git -c user.name=h -c user.email=h@i merge -q --no-ff -m "merge $b" "$b")
  done
  unset C8_EXEMPT
  night; rc=$?
  check "exit 0" test "$rc" = 0
  for b in closeout/w15-ledger closeout/w22-night; do check "$b is merged" has "$E8/night.log" "precondition: $b ("; done
  check "no exemption" lacks "$E8/night.log" "EXEMPT"
}

# Q1, Q2 (#81, #59, #52, #55): quiet.sh itself, its c52-win step with stub builds, in a scratch home.
q_world() {
  QC="$W/qcoord"; mkdir -p "$QC" "$W/tmp" "$W/home"; cp "$here/quiet.sh" "$here/recrun.sh" "$here/homeguard.py" "$QC/"
  HOME="$W/home"; USERPROFILE=$(cygpath -m "$W/home"); export HOME USERPROFILE
}
q() { QUIET_PKGS=cli QUIET_ROUNDS=2 TMPDIR="$W/tmp" sh "$QC/quiet.sh" "$P/qompack-cx-cand" main "$W/qev" c52-win > "$W/q.out" 2>&1; }
case_Q1_quiet_absolute_budget_and_cleanup() {
  q_world
  kv ns_base_HookNoop_InProcess 1000000; kv ns_candidate_HookNoop_InProcess 2500000
  q; rc=$?
  check "exit 0" test "$rc" = 0
  check "1.1.27 judged by its absolute budget" has "$W/qev/c52-win/paired.txt" "BenchmarkHookNoop_InProcess"
  check "PASS under 3 ms, no ratio" hasre "$W/qev/c52-win/paired.txt" "^BenchmarkHookNoop_InProcess +candidate 2.5 ms against its absolute budget 3 ms: PASS; no ratio against base"
  check "the reason is D67(j)" has "$W/qev/c52-win/paired.txt" "not like for like: D67(j)"
  check "absolute-budget.tsv" hasre "$W/qev/c52-win/absolute-budget.tsv" "^BenchmarkHookNoop_InProcess	2500000	3000000	PASS	D67\(j\)"
  check "no geomean for it" sh -c '! grep -E "^BenchmarkHookNoop_InProcess +[0-9]+ " "$1"' sh "$W/qev/c52-win/paired.txt"
  check "the scratch directory is removed after a pass" sh -c '[ -z "$(ls -A "$1")" ]' sh "$W/tmp"
  check "and the run record says so" has "$W/qev/quiet-run.txt" "removed (every step passed)"
  check "the go version is the candidate's" has "$W/qev/quiet-run.txt" "go version go1.26.6"
  check "host queries are bounded" hasre "$CALLS" "^timeout -k 10 60 powershell "
}
case_Q2_quiet_over_budget_and_failure_keeps_scratch() {
  q_world
  kv ns_candidate_HookNoop_InProcess 3500000; kv rc_build_base 1   # over budget; base's build fails
  q; rc=$?
  check "exit 1" test "$rc" = 1
  check "OVER-BUDGET" hasre "$W/qev/c52-win/paired.txt" "^BenchmarkHookNoop_InProcess +candidate 3.5 ms against its absolute budget 3 ms: OVER-BUDGET"
  check "in absolute-budget.tsv" has "$W/qev/c52-win/absolute-budget.tsv" "	OVER-BUDGET	"
  check "the scratch directory is kept after a failure" sh -c '[ -n "$(ls -A "$1")" ]' sh "$W/tmp"
  check "and named" has "$W/qev/quiet-run.txt" "kept: a step failed"
}

# ---- driver --------------------------------------------------------------------------------------
cases=$(sed -n 's/^\(case_[A-Za-z0-9_]*\)() {$/\1/p' "$0")
if [ "${1:-}" = -l ]; then printf '%s\n' "$cases" | sed 's/^case_//'; exit 0; fi
sel=${*:-}
for c in $cases; do
  if [ -n "$sel" ]; then case " $sel " in *" ${c#case_} "*) ;; *) continue ;; esac; fi
  CUR=${c#case_}; CASE_BAD=0; NCASES=$((NCASES + 1))
  new_case "$CUR" || { echo "FAIL $CUR (setup)"; FAILS=$((FAILS + 1)); continue; }
  ( cd "$W" || exit 1; PATH="$HB:$PATH"; export PATH; stub_guard || exit 1; "$c"; exit "$CASE_BAD" ) > "$W/case.out" 2>&1
  if [ $? -eq 0 ]; then echo "PASS $CUR"; else echo "FAIL $CUR"; sed 's/^/    /' "$W/case.out"; FAILS=$((FAILS + 1)); fi
done
echo "$NCASES case(s), $FAILS failed"
[ "$FAILS" -eq 0 ]
