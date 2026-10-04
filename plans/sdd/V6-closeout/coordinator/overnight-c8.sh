#!/bin/sh
# overnight-c8.sh <candidate-repo> <candidate-sha> <evidence-dir>
# Candidate 8's local night (D57-D61), strictly sequential, nothing else measuring. Progress goes to
# <evidence-dir>/chain.log, every step's power record to power.tsv, and the outcome counts to
# overnight-outcome.txt (c8-night.sh copies them into night.log). Exit 0 only when every step ran,
# passed and, where power matters, was VALID. It refuses (exit 2, before writing anything) an
# evidence directory that already holds a night's records: recrun.sh never overwrites a record, so a
# second night there would fail every step in seconds and could be read as a real run.
#
# Power (D57(d), power.sh). Every step's power is recorded: t0 is taken before its last power check,
# and it is judged over [t0, end] against the System log (Kernel-Power 105, a source change; 506,
# Modern Standby; 42 and 107, sleep and resume) and its end reading:
#   VALID          started and ended on AC, no event: its exit status is a pass or a fail;
#   INVALID-POWER  an event during it: neither;
#   NOT-REFERENCE  it could get no AC within the night's wait budget, ran on battery, or the power
#                  history could not be read: not a reference measurement, not a pass, not a fail;
#   SKIPPED        the deadline passed before it could start.
# The AC-gated Windows steps (queued until AC) are win-timing (ci.yml's timing lane plus the three
# functional integration hot-path rows, D53(a)), win-e2e-timing, win-x11-alone, quiet c51-win (C5.1)
# and C5.2's c52-win and c52-linux. An INVALID-POWER try moves its new records to
# <step>.invalid-power-1/ and the step is retried once. The AC-independent steps (c52-derive,
# win-race, bundles, the Linux lanes) are never waited for; for the Linux *-timing lanes, wall-clock
# judgements, only a VALID run counts as a pass or a fail, and the others count by exit status.
# Order: a gated step runs when AC is present; otherwise it waits in a queue while the AC-independent
# work runs, whose load also drains the battery toward the charger's restore point (about 35-40 %,
# D57(d)). Waiting for AC is bounded night-wide by AC_WAIT_BUDGET_MIN one-minute polls
# (NIGHT_AC_WAITED_MIN carries what c8-night.sh already spent) and by the deadline: no step and no
# wait starts after it. c8-night.sh passes its deadline as NIGHT_DEADLINE_EPOCH, so a pre-freeze that
# ends late cannot carry the night to the next day; run alone, the next NIGHT_DEADLINE (local HH:MM,
# default 08:00) is used, and refused when it is more than NIGHT_MAX_AHEAD_H (16) hours away (a
# daytime launch) unless NIGHT_ALLOW_FAR=1.
#
# C5.2 (D57(e)). c52-derive (c52derive.py) traces every C5.2 benchmark once on the candidate and
# selects those whose executed product files, own benchmark file or fixtures changed since candidate
# 7 (C8_PREV_CANDIDATE, default d20309c0); c52-win and c52-linux then measure exactly that set
# against cf31e01, logged in chain.log. When the derivation cannot run at all, the static floor (the
# rows the 2026-10-03 diff reaches) is measured and the derivation counts as a failed step.
# c52-linux brings the container up for itself and stops it afterwards. Docker Desktop is started
# only when its engine was down at the night's start and is not answering (three probes), and only
# an engine this chain started is stopped (D56(g)).
#
# release-check --tag v0.3.0 (C3.12/C7.4) runs in an ISOLATED scratch clone: git clone --no-local of
# the candidate, origin and its push URL set to an unreachable path, the tag created only there, the
# clone removed by an EXIT trap. No v0.3.0 tag ever exists in the shared ref store (a pushed v* tag
# would cut a real release, D57(c)). Its output is time-stamped (stamped.sh), and its power validity
# is judged only over its AC-sensitive windows: the isolated test/e2e pass of `ci-local test` and of
# `ci-local cover` (every other pass declares co-load or holds no timing judgement). A run whose log
# shows no window is VALID only when it failed before `ci-local test` began; otherwise it is
# NOT-REFERENCE. It is retried once, in a fresh clone, only when an event fell inside a window or a
# window ran on battery, AC is present (or returns within the budget), and a second run of the same
# length can end before the deadline. It starts only when RC_EST_S lets it end by the deadline.
# Hosted ci.yml and nightly.yml on the same commit supply the native-platform lanes.
set -u
[ $# -eq 3 ] || { echo "usage: overnight-c8.sh <candidate-repo> <candidate-sha> <evidence-dir>" >&2; exit 2; }
C=$1; H=$2; E=$3
here=$(cd "$(dirname "$0")" && pwd)
. "$here/power.sh"
refuse() { echo "overnight-c8.sh: REFUSED: $*" >&2; exit 2; }
mkdir -p "$E" || exit 2
# A night's records: refuse rather than append to them or have recrun.sh refuse every step.
for f in chain.log power.tsv overnight-outcome.txt; do
  [ -e "$E/$f" ] && refuse "$E already holds a night's records ($f); use a fresh evidence directory"
done
ex=$(ls -A "$E" | grep -E '^(p3-|quiet|c52-derive|release-check|[A-Za-z0-9-]+\.invalid-power-)' | head -n 3 | tr '\n' ' ')
[ -z "$ex" ] || refuse "$E already holds a night's records ($ex); use a fresh evidence directory"
# Each step declares its own co-load; an inherited declaration would turn timing judgements into reports.
unset QOMPACK_UNDER_COLOAD QOMPACK_NONREFERENCE_DISK
log() { echo "$* $(date -u +%FT%TZ)" >> "$E/chain.log"; }
winpath() { if command -v cygpath > /dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi; }

NIGHT_DEADLINE=${NIGHT_DEADLINE:-08:00}
AC_WAIT_BUDGET_MIN=${AC_WAIT_BUDGET_MIN:-180}   # D57(d)'s 180-minute AC wait, now one budget per night
# RC_EST_S: how long a local release-check is expected to take. SP-17's record is 8451 s on a smaller
# tree and w17-release estimates 2.5-3 h on this laptop; rounded up to 3 h. Used only to decide whether
# it can start before the deadline; a retry is sized by try 1's own duration instead.
RC_EST_S=${RC_EST_S:-10800}
QUIET_BASE=cf31e01                              # quiet.sh's pre-Phase-2 base (its header)
PREV_CANDIDATE=${C8_PREV_CANDIDATE:-d20309c03ffc364e4cc48663be73cfbb1f2309b2}   # candidate 7 (phase3/c7-CANDIDATE.md)
# The static C5.2 floor, measured only when c52derive.py cannot run at all: the rows whose executed
# packages the 2026-10-03 diff from candidate 7 reaches (closeout/integration and every closeout/w20-*
# tip change checkpoint's intent/source/writer/draft, cli's config/doctor/qompack_commands, config's
# config/migration and daemon's scheduler_runtime/state/tap), by w17-inventory's executed-file sets.
C52_FLOOR_PKGS="checkpoint cli config daemon"
C52_FLOOR_FILTER='^Benchmark(Finalize|AdvanceSegment|ExtractDecisions|StripInjections|Truncate|HookNoop_InProcess|ConfigLoad_ColdNoFiles|FeaturesFrom|ReclaimableIndexBuild_5000Blocks|AssembleCandidates_2000ToolUses|RuntimeEvaluate_2000ToolUses_32Candidates|SchedulerTap_ObserveTool)$'
C52_PKGS=$C52_FLOOR_PKGS; C52_FILTER=$C52_FLOOR_FILTER
NOPUSH_URL=file:///nonexistent/qompack-release-check-scratch-clone-never-pushes
DOCKER_START_TIMEOUT_S=600; DOCKER_STOP_TIMEOUT_S=300   # docker desktop's own --timeout (default: none)
waited=${NIGHT_AC_WAITED_MIN:-0}
for v in "$AC_WAIT_BUDGET_MIN" "$RC_EST_S" "$waited"; do
  case $v in ''|*[!0-9]*) refuse "not a whole number: '$v'" ;; esac
done
if [ -n "${NIGHT_DEADLINE_EPOCH:-}" ]; then
  case $NIGHT_DEADLINE_EPOCH in *[!0-9]*) refuse "NIGHT_DEADLINE_EPOCH must be an epoch, not '$NIGHT_DEADLINE_EPOCH'" ;; esac
  deadline=$NIGHT_DEADLINE_EPOCH                # c8-night.sh's own deadline, never recomputed
else
  deadline=$(deadline_epoch "$NIGHT_DEADLINE") || refuse "NIGHT_DEADLINE must be HH:MM, not '$NIGHT_DEADLINE'"
  deadline_near "$deadline" || refuse "the deadline $(date -d "@$deadline" +%FT%T) is more than $NIGHT_MAX_AHEAD_H h away (a daytime launch would run into the owner's day); set NIGHT_DEADLINE, or NIGHT_ALLOW_FAR=1"
fi
DL=$(date -d "@$deadline" +%FT%T)
past_deadline() { [ "$(date +%s)" -ge "$deadline" ]; }

RCT=""
cleanup() {
  if [ -n "$RCT" ] && [ -d "$RCT" ]; then rm -rf "$RCT" && log "release-check scratch clone $RCT removed"; fi
  RCT=""
}
trap cleanup EXIT; trap 'exit 130' INT; trap 'exit 143' TERM; trap 'exit 129' HUP

# ---- outcome -------------------------------------------------------------------------------------
n_pass=0; n_fail=0; n_inv=0; n_nref=0; n_nref_red=0; n_skip=0; failed=""
count() { # count <pass|fail|invalid|nref|skip> <step> [exit]
  case $1 in
    pass) n_pass=$((n_pass + 1)) ;;
    fail) n_fail=$((n_fail + 1)); failed="$failed $2" ;;
    invalid) n_inv=$((n_inv + 1)) ;;
    nref) n_nref=$((n_nref + 1)); [ "${3:-0}" = 0 ] || n_nref_red=$((n_nref_red + 1)) ;;
    skip) n_skip=$((n_skip + 1)) ;;
  esac
}
printf 'step\ttry\texit\tverdict\tpower_start\tpower_end\tt0\tt1\n' > "$E/power.tsv"
tsv() { printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" "$5" "$6" "$7" "$8" >> "$E/power.tsv"; }
record() { # record <step> <try> <exit> <verdict> <p0> <p1> <t0> <t1> [note]
  log "step $1 try $2 exit=$3 $4 power=$5->$6${9:+; $9}"
  tsv "$1" "$2" "$3" "$4" "$5" "$6" "$7" "$8"
}
skipped() { # skipped <step> <why>
  log "step $1 SKIPPED: $2"; tsv "$1" 1 - SKIPPED - - - -; count skip "$1"
}

# ---- try evidence --------------------------------------------------------------------------------
# A try's records are the evidence-directory entries it created; on INVALID-POWER they are moved into
# <step>.invalid-power-<try>/ with their own names, so each record's "log" field still names its log.
snap() { ls -A "$E" > "$1" 2> /dev/null; }
move_aside() { # move_aside <step> <try> <snapshot>: prints the directory
  ma_d="$E/$1.invalid-power-$2"; mkdir -p "$ma_d" || return 1
  ls -A "$E" | grep -vxF -f "$3" | while IFS= read -r ma_f; do
    [ "$ma_f" = "${ma_d##*/}" ] || mv "$E/$ma_f" "$ma_d/"
  done
  echo "$ma_d"
}

# ---- docker --------------------------------------------------------------------------------------
engine_up_at_start=0; cs_started=0
docker_answers() { # three probes, 30 s apart: one failed probe is not an engine that is down
  da_n=0
  while :; do
    timeout -k 10 60 docker ps > /dev/null 2>&1 && return 0
    da_n=$((da_n + 1)); [ "$da_n" -ge 3 ] && return 1
    sleep 30
  done
}
container_up() { # 0 when the container runs; cs_started=1 when this chain started the engine
  cs_started=0
  if ! docker_answers; then
    if [ "$engine_up_at_start" = 1 ]; then
      log "docker engine not answering (3 probes) though it was up at the night's start: it is the owner's engine, so this chain asks Docker Desktop to start it but never stops it (D56(g))"
      timeout -k 30 $((DOCKER_START_TIMEOUT_S + 60)) docker desktop start --timeout "$DOCKER_START_TIMEOUT_S" > /dev/null 2>&1
      log "engine start (the owner's engine) exit=$?"
    else
      log "docker engine not answering (3 probes): starting it (docker desktop start --timeout $DOCKER_START_TIMEOUT_S)"
      if timeout -k 30 $((DOCKER_START_TIMEOUT_S + 60)) docker desktop start --timeout "$DOCKER_START_TIMEOUT_S" > /dev/null 2>&1; then
        cs_started=1; log "engine started by this chain"
      else
        log "engine start failed or timed out exit=$?"
      fi
    fi
  fi
  timeout -k 10 300 docker start qompack-v6-linux-verification > /dev/null 2>&1 &&
    timeout -k 10 60 docker update --cpus 8 --memory 8g --memory-swap 8g qompack-v6-linux-verification > /dev/null 2>&1
}
container_down() {
  timeout -k 10 120 docker stop qompack-v6-linux-verification > /dev/null 2>&1; log "container stopped exit=$?"
  if [ "$cs_started" = 1 ] && [ "$engine_up_at_start" = 0 ]; then
    timeout -k 30 $((DOCKER_STOP_TIMEOUT_S + 60)) docker desktop stop --timeout "$DOCKER_STOP_TIMEOUT_S" > /dev/null 2>&1
    log "engine stopped exit=$?"
  else
    log "engine left as found (D56(g))"
  fi
  cs_started=0
}

# ---- AC-gated steps ------------------------------------------------------------------------------
gated_cmd() {
  case $1 in
    win-timing|win-e2e-timing|win-x11-alone) sh "$here/phase3.sh" "$C" "$E" "$1" ;;
    c51-win) sh "$here/quiet.sh" "$C" "$QUIET_BASE" "$E/quiet" c51-win ;;
    c52-win) QUIET_PKGS=$C52_PKGS QUIET_BENCH_FILTER=$C52_FILTER sh "$here/quiet.sh" "$C" "$QUIET_BASE" "$E/quiet-c52" c52-win ;;
    c52-linux)
      if container_up; then
        log "container start exit=0 (c52-linux)"
        QUIET_PKGS=$C52_PKGS QUIET_BENCH_FILTER=$C52_FILTER sh "$here/quiet.sh" "$C" "$QUIET_BASE" "$E/quiet-c52-linux" c52-linux
        gc_rc=$?
      else
        log "container start failed: c52-linux did not run"; gc_rc=2
      fi
      container_down; return "$gc_rc" ;;
    *) echo "overnight-c8.sh: unknown gated step $1" >&2; return 2 ;;
  esac
}
tries_of() { eval "echo \${tries_$(printf '%s' "$1" | tr -c 'A-Za-z0-9' '_'):-0}"; }
set_tries() { eval "tries_$(printf '%s' "$1" | tr -c 'A-Za-z0-9' '_')=$2"; }

# run_gated <step> <now|wait>: 0 once the step has its final verdict, 1 while it stays queued. "now"
# runs it only if AC is present; "wait" waits for AC within the budget, else runs it NOT-REFERENCE.
run_gated() {
  g_s=$1; g_mode=$2; g_n=$(( $(tries_of "$g_s") + 1 )); g_note=""
  while :; do
    if past_deadline; then
      record "$g_s" "$g_n" - SKIPPED - - - - "the deadline $DL passed before it could start"
      count skip "$g_s"; return 0
    fi
    g_t0=$(date +%s); g_p0=$(power_read)      # t0 before the last power check (D57(d))
    case $g_p0 in "AC "*) break ;; esac
    [ "$g_mode" = now ] && return 1
    if ! power_wait_ac waited "$AC_WAIT_BUDGET_MIN" "$deadline" log; then
      past_deadline && continue
      g_note="no AC within the night's wait budget"; g_t0=$(date +%s); g_p0=$(power_read); break
    fi
  done
  g_sf=$(mktemp); snap "$g_sf"
  log "step $g_s try $g_n start power=$g_p0"
  gated_cmd "$g_s" >> "$E/chain.log" 2>&1; g_rc=$?
  g_t1=$(date +%s); g_p1=$(power_read)
  if g_ev=$(power_events_since "$g_t0"); then g_ok=1; else g_ok=0; g_ev=""; fi
  g_v=$(power_verdict "$g_p0" "$g_ok" "$(power_events_window "$g_t0" "$g_t1" "$g_ev")" "$g_p1")
  set_tries "$g_s" "$g_n"
  case $g_v in
    INVALID-POWER*)
      if [ "$g_n" -lt 2 ]; then
        g_d=$(move_aside "$g_s" "$g_n" "$g_sf"); rm -f "$g_sf"
        record "$g_s" "$g_n" "$g_rc" "$g_v" "$g_p0" "$g_p1" "$g_t0" "$g_t1" "${g_note:+$g_note; }neither a pass nor a fail; its records moved to $g_d; retried once"
        return 1
      fi
      count invalid "$g_s" ;;
    VALID) if [ "$g_rc" -eq 0 ]; then count pass "$g_s"; else count fail "$g_s"; fi ;;
    *) count nref "$g_s" "$g_rc" ;;
  esac
  rm -f "$g_sf"
  record "$g_s" "$g_n" "$g_rc" "$g_v" "$g_p0" "$g_p1" "$g_t0" "$g_t1" "$g_note"
  return 0
}
queue=""
run_queue() { # run_queue <now|wait>
  rq_rest=""
  for rq_s in $queue; do
    if [ "$1" = wait ]; then
      until run_gated "$rq_s" wait; do :; done
    elif ! run_gated "$rq_s" now; then
      rq_rest="$rq_rest $rq_s"
    fi
  done
  queue=$rq_rest
  [ -n "$queue" ] && log "queued for AC:$queue (power $(power_read))"
  return 0
}

# ---- AC-independent steps ------------------------------------------------------------------------
step() { # step <name> <command...>: never waits for AC; its power is recorded all the same (D57(d))
  s_name=$1; shift
  if past_deadline; then skipped "$s_name" "the deadline $DL passed"; return 0; fi
  s_t0=$(date +%s); s_p0=$(power_read)
  log "step $s_name start power=$s_p0"
  "$@" >> "$E/chain.log" 2>&1; s_rc=$?
  s_t1=$(date +%s); s_p1=$(power_read)
  log "step $s_name finished exit=$s_rc"
  if s_ev=$(power_events_since "$s_t0"); then s_ok=1; else s_ok=0; s_ev=""; fi
  s_v=$(power_verdict "$s_p0" "$s_ok" "$(power_events_window "$s_t0" "$s_t1" "$s_ev")" "$s_p1")
  case $s_name in
    *-timing)   # a wall-clock judgement: only a VALID run is a pass or a fail
      s_note="a timing lane: judged only when VALID"
      case $s_v in
        VALID) if [ "$s_rc" -eq 0 ]; then count pass "$s_name"; else count fail "$s_name"; fi ;;
        INVALID-POWER*) count invalid "$s_name" ;;
        *) count nref "$s_name" "$s_rc" ;;
      esac ;;
    *) s_note="power reported only (no timing judgement)"
       if [ "$s_rc" -eq 0 ]; then count pass "$s_name"; else count fail "$s_name"; fi ;;
  esac
  record "$s_name" 1 "$s_rc" "$s_v" "$s_p0" "$s_p1" "$s_t0" "$s_t1" "$s_note"
  return 0
}
linux_lanes() {
  ll_steps="linux-timing linux-e2e-timing linux-tree linux-e2e linux-child"
  if past_deadline; then
    for ll_s in $ll_steps; do skipped "$ll_s" "the deadline $DL passed"; done
    return 0
  fi
  if container_up; then
    log "container start exit=0"
    for ll_s in $ll_steps; do step "$ll_s" sh "$here/phase3.sh" "$C" "$E" "$ll_s"; done
  else
    log "container start failed: the Linux lanes are SKIPPED"
    for ll_s in $ll_steps; do skipped "$ll_s" "no container"; done
  fi
  container_down
}

# ---- C5.2's set (D57(e)) -------------------------------------------------------------------------
c52_derive() {
  step c52-derive python "$here/c52derive.py" "$(winpath "$C")" "$PREV_CANDIDATE" "$(winpath "$E/c52-derive")"
  if [ -f "$E/c52-derive/pkgs.txt" ] && [ -f "$E/c52-derive/filter.txt" ]; then
    C52_PKGS=$(cat "$E/c52-derive/pkgs.txt"); C52_FILTER=$(cat "$E/c52-derive/filter.txt")
    log "c52: derived set (D57(e), executed files changed since $PREV_CANDIDATE): QUIET_PKGS='$C52_PKGS' QUIET_BENCH_FILTER='$C52_FILTER' (c52-derive/report.txt)"
  else
    log "c52: the derivation produced no selection, so the static floor is measured: QUIET_PKGS='$C52_PKGS' QUIET_BENCH_FILTER='$C52_FILTER'"
  fi
}

# ---- release-check -------------------------------------------------------------------------------
rc_prepare() {   # core.longpaths in the clone only, as c8-night.sh's merged-tree clone
  git clone -q -c core.longpaths=true --no-local --no-checkout --no-tags "$C" "$RCT/repo" &&
    git -C "$RCT/repo" remote set-url origin "$NOPUSH_URL" &&
    git -C "$RCT/repo" remote set-url --push origin "$NOPUSH_URL" || return 1
  git -C "$RCT/repo" cat-file -e "$H^{commit}" 2> /dev/null || git -C "$RCT/repo" fetch -q --no-tags "$C" "$H" || return 1
  git -C "$RCT/repo" -c advice.detachedHead=false checkout -q --detach "$H" || return 1
  [ "$(git -C "$RCT/repo" rev-parse HEAD)" = "$H" ] && [ -z "$(git -C "$RCT/repo" status --porcelain)" ] || return 1
  git -C "$RCT/repo" tag v0.3.0 "$H" || return 1
  [ "$(git -C "$RCT/repo" describe --tags --exact-match)" = v0.3.0 ]
}
rc_new_clone() { # a pristine clone for each try: nothing a try leaves behind reaches the next
  RCT=$(winpath "$(mktemp -d)") || { RCT=""; log "release-check: no scratch directory"; return 1; }
  if ! rc_prepare >> "$E/release-check-clone.txt" 2>&1; then
    log "release-check: the scratch clone $RCT could not be prepared (release-check-clone.txt)"; return 1
  fi
  log "release-check clone $RCT at $H: origin and push URL $NOPUSH_URL, tag v0.3.0 only in the clone"
}
# rc_windows <stamped log> <end epoch>: one "WINDOW <section> <start> <end> <how>" line per
# AC-sensitive window reached: the isolated test/e2e pass of `ci-local test` and of `ci-local cover`.
# A window starts at the last package result line before test/e2e's own (the shared pass had ended by
# then, so this is never later than the real start) and ends at test/e2e's result line, or at the
# section's end when no such line came.
rc_windows() {
  awk -v tend="$2" '
    function flush(endt) {
      if (sec != "" && !done) print "WINDOW", sec, (lastpkg != "" ? lastpkg : secstart), endt, "no-e2e-result-line"
      sec = ""
    }
    $1 !~ /^[0-9]+$/ { next }
    {
      t = $1; line = $0; sub(/^[0-9]+ [^ ]+ /, "", line)
      if (line ~ /^=== release-check: .* ===$/) {
        flush(t)
        name = line; sub(/^=== release-check: /, "", name); sub(/ ===$/, "", name)
        sec = (name == "ci-local test" || name == "ci-local cover") ? name : ""
        gsub(/ /, "-", sec); secstart = t; lastpkg = ""; done = 0
        next
      }
      if (sec == "" || done) next
      if (line ~ /^(ok|FAIL)[ \t]+github\.com\/qompack\/qompack\/test\/e2e([ \t]|$)/) {
        print "WINDOW", sec, (lastpkg != "" ? lastpkg : secstart), t, "e2e-result-line"; done = 1; next
      }
      if (line ~ /^(ok|FAIL|\?)[ \t]+[A-Za-z0-9.-]+\//) lastpkg = t
    }
    END { flush(tend) }' "$1"
}
# rc_verdict <start reading> <events-ok> <events> <windows file> <exit> <stamped log> <end reading>
rc_verdict() {
  [ "$2" = 1 ] || { echo "NOT-REFERENCE the power history could not be read"; return 0; }
  if [ ! -s "$4" ]; then
    # No window: VALID only when the log proves the run stopped before one could start (the first
    # step's header, in the format rc_windows reads, is there; ci-local test's is not; it failed).
    # Anything else (format drift, a stamping failure) cannot be judged against the power record.
    if [ "$5" -ne 0 ] && grep -q ' === release-check: version agreement ===$' "$6" 2> /dev/null &&
       ! grep -q ' === release-check: ci-local test ===$' "$6" 2> /dev/null; then
      echo "VALID (it failed before an AC-sensitive window began)"
    else
      echo "NOT-REFERENCE AC-sensitive windows not found in the log"
    fi
    return 0
  fi
  rv_bad=""; rv_unknown=""
  while read -r rv_w rv_sec rv_ws rv_we rv_how; do
    rv_st=$(printf '%s\n' "$3" | awk -v ws="$rv_ws" -v s="${1%% *}" '
      $1 ~ /^[0-9]+$/ && $1 + 0 < ws + 0 && $2 ~ /^AC=/ { s = ($2 == "AC=1") ? "AC" : "BAT" } END { print s }')
    rv_in=$(power_events_window "$rv_ws" "$rv_we" "$3" | tr '\n' ' ' | sed 's/ *$//')
    if [ -n "$rv_in" ]; then rv_bad="$rv_bad $rv_sec:events[$rv_in]"
    elif [ "$rv_st" = BAT ]; then rv_bad="$rv_bad $rv_sec:on-battery"
    elif [ "$rv_st" != AC ]; then rv_unknown="$rv_unknown $rv_sec:power-unknown"
    fi
  done < "$4"
  if [ -n "$rv_bad" ]; then echo "INVALID-POWER$rv_bad"
  elif [ -n "$rv_unknown" ]; then echo "NOT-REFERENCE$rv_unknown"
  elif [ -z "$3" ] && case $7 in "AC "*) false ;; *) true ;; esac; then
    echo "NOT-REFERENCE ended on $7 with no power event recorded"
  else echo "VALID"
  fi
}
# rc_retry_blocker <try-1 seconds>: sets rc_why to the reason a second try cannot run, or "". A retry
# needs AC (a battery run would void it again) within the night's budget, and time to finish.
rc_retry_blocker() {
  rc_why=""
  case $(power_read) in
    "AC "*) ;;
    *) power_wait_ac waited "$AC_WAIT_BUDGET_MIN" "$deadline" log || rc_why="no AC for a second run" ;;
  esac
  if [ -z "$rc_why" ] && [ $(( $(date +%s) + $1 )) -gt "$deadline" ]; then
    rc_why="a second $(( $1 / 60 ))-min run would end after the deadline"
  fi
  return 0
}
release_check() {
  if [ $(( $(date +%s) + RC_EST_S )) -gt "$deadline" ]; then
    log "step release-check SKIPPED: it needs about $((RC_EST_S / 60)) min (RC_EST_S) and would end after the deadline $DL"
    tsv release-check 1 - SKIPPED - - - -; count skip release-check; return 0
  fi
  if [ -e "$E/p3-release-check-tag.json" ] || [ -e "$E/p3-release-check-tag.log" ]; then
    # recrun.sh would refuse to overwrite it, and the windows would then be read from the old log.
    log "step release-check exit=2: a record p3-release-check-tag already exists in $E"
    count fail release-check; return 0
  fi
  rc_real_before=$(git -C "$C" rev-parse -q --verify refs/tags/v0.3.0 || true)
  [ -n "$rc_real_before" ] && log "WARNING: the shared ref store already holds a tag v0.3.0 ($rc_real_before), not made by this chain"
  if ! rc_new_clone; then log "step release-check exit=2: no clone to run it in"; count fail release-check; cleanup; return 0; fi
  rc_try=1
  while :; do
    rc_t0=$(date +%s); rc_p0=$(power_read); rc_sf=$(mktemp); snap "$rc_sf"
    log "step release-check try $rc_try start power=$rc_p0"
    sh "$here/recrun.sh" "$RCT/repo" "$E" p3-release-check-tag -- sh "$here/stamped.sh" \
      go run ./tools/devtool release-check --tag v0.3.0 --evidence-copy "$(winpath "$E")/release-check.json" \
      >> "$E/chain.log" 2>&1
    rc_rc=$?
    rc_t1=$(date +%s); rc_p1=$(power_read)
    if rc_ev=$(power_events_since "$rc_t0"); then rc_ok=1; else rc_ok=0; rc_ev=""; fi
    rc_wf=$(mktemp)
    rc_windows "$E/p3-release-check-tag.log" "$rc_t1" > "$rc_wf" 2> /dev/null
    while read -r rc_w; do log "release-check try $rc_try $rc_w"; done < "$rc_wf"
    rc_v=$(rc_verdict "$rc_p0" "$rc_ok" "$(power_events_window "$rc_t0" "$rc_t1" "$rc_ev")" "$rc_wf" "$rc_rc" \
      "$E/p3-release-check-tag.log" "$rc_p1"); rm -f "$rc_wf"
    rc_note="events during the run: [$(power_events_window "$rc_t0" "$rc_t1" "$rc_ev" | tr '\n' ' ' | sed 's/ *$//')]"
    case $rc_v in
      INVALID-POWER*)
        if [ "$rc_try" -lt 2 ]; then
          rc_retry_blocker $((rc_t1 - rc_t0))
          if [ -z "$rc_why" ]; then
            rc_d=$(move_aside release-check "$rc_try" "$rc_sf"); rm -f "$rc_sf"
            record release-check "$rc_try" "$rc_rc" "$rc_v" "$rc_p0" "$rc_p1" "$rc_t0" "$rc_t1" "$rc_note; neither a pass nor a fail; its records moved to $rc_d; retried once in a fresh clone"
            cleanup
            if ! rc_new_clone; then log "step release-check exit=2: no fresh clone for try 2"; count fail release-check; cleanup; return 0; fi
            rc_try=2; continue
          fi
          rc_note="$rc_note; not retried: $rc_why"
        fi
        count invalid release-check ;;
      VALID*) if [ "$rc_rc" -eq 0 ]; then count pass release-check; else count fail release-check; fi ;;
      *) count nref release-check "$rc_rc" ;;
    esac
    rm -f "$rc_sf"
    record release-check "$rc_try" "$rc_rc" "$rc_v" "$rc_p0" "$rc_p1" "$rc_t0" "$rc_t1" "$rc_note"
    break
  done
  rc_real_after=$(git -C "$C" rev-parse -q --verify refs/tags/v0.3.0 || true)
  if [ "$rc_real_after" = "$rc_real_before" ]; then
    log "shared ref store: tag v0.3.0 ${rc_real_before:+still }${rc_real_before:-absent} (the chain never tags it)"
  else
    log "ERROR: the shared ref store's v0.3.0 changed during release-check (${rc_real_before:-absent} -> ${rc_real_after:-absent})"
    count fail release-check-tag-isolation
  fi
  cleanup
}

# ---- the night -----------------------------------------------------------------------------------
log "start candidate=$H deadline=$DL${NIGHT_DEADLINE_EPOCH:+ (from c8-night.sh)} ac_wait_budget=${AC_WAIT_BUDGET_MIN}min used=${waited}min prev_candidate=$PREV_CANDIDATE power=$(power_read)"
timeout -k 10 60 docker ps > /dev/null 2>&1 && engine_up_at_start=1
log "engine up at start=$engine_up_at_start (only an engine down now and started by this chain is ever stopped)"
timeout -k 10 60 docker ps --format "{{.Names}} {{.Status}}" >> "$E/chain.log" 2>&1
timeout -k 10 120 docker stop qompack-v6-linux-verification > /dev/null 2>&1

queue="win-timing win-e2e-timing win-x11-alone"
run_queue now
c52_derive
step win-race env GOFLAGS=-p=4 sh "$here/phase3.sh" "$C" "$E" win-race
step bundles sh "$here/phase3.sh" "$C" "$E" bundles
run_queue now
linux_lanes
queue="$queue c51-win"
if [ -n "$C52_FILTER" ]; then
  queue="$queue c52-win c52-linux"
else
  log "c52: no C5.2 benchmark executes a file changed since $PREV_CANDIDATE, so every candidate 5 C5.2 row carries (D57(e)); c52-win and c52-linux are not needed (c52-derive/report.txt)"
fi
run_queue now
release_check
run_queue wait

total=$((n_pass + n_fail + n_inv + n_nref + n_skip))
outcome="steps=$total passed=$n_pass failed=$n_fail${failed:+ [${failed# }]} invalid_power=$n_inv not_reference=$n_nref (of which $n_nref_red exited non-zero) skipped=$n_skip ac_wait_used=${waited}min"
echo "$outcome" > "$E/overnight-outcome.txt"
log "done: $outcome"
[ $((n_fail + n_inv + n_nref + n_skip)) -eq 0 ]
