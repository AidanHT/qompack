#!/bin/sh
# overnight-c8.sh <candidate-repo> <candidate-sha> <evidence-dir>
# Candidate 8's local night (D57-D61), strictly sequential, nothing else measuring. Progress goes to
# <evidence-dir>/chain.log, every power-checked try to power.tsv, and the outcome counts to
# overnight-outcome.txt (c8-night.sh copies them into night.log). Exit 0 only when every step ran,
# passed and, where power matters, was VALID.
#
# Power (D57(d), power.sh). The AC-gated Windows steps are win-timing (ci.yml's timing lane plus the
# three functional integration hot-path rows, D53(a)), win-e2e-timing, win-x11-alone, quiet c51-win
# (C5.1) and quiet c52-win (C5.2 for BenchmarkFinalize, BenchmarkAdvanceSegment and
# BenchmarkHookNoop_InProcess, whose executed files candidate 8 changed: D57(e)). Each try takes its t0
# before its last power check and is judged over [t0, end] against the System log (Kernel-Power 105, a
# source change; 506, Modern Standby):
#   VALID          started on AC, no event: its exit status is a pass or a fail;
#   INVALID-POWER  an event during it: neither. Try 1's new records are moved to
#                  <step>.invalid-power-1/ (recrun.sh never overwrites a record, so this is what lets
#                  try 2 run at all) and the step is retried once;
#   NOT-REFERENCE  it could get no AC within the night's wait budget, or the power history could not
#                  be read: it still runs for its functional verdict, but it is not a reference
#                  measurement, not a pass and not a fail, and it is not retried;
#   SKIPPED        NIGHT_DEADLINE passed before it could start.
# Order: a gated step runs when AC is present; otherwise it waits in a queue while the AC-independent
# work runs (win-race, bundles, the Linux lanes, release-check), whose load also drains the battery
# toward the charger's restore point (about 35-40 %, D57(d)). Waiting for AC is bounded night-wide by
# AC_WAIT_BUDGET_MIN one-minute polls (NIGHT_AC_WAITED_MIN carries what c8-night.sh already spent)
# and by NIGHT_DEADLINE (local HH:MM, default 08:00): no step and no wait starts after it.
#
# release-check --tag v0.3.0 (C3.12/C7.4) runs in an ISOLATED scratch clone: git clone --no-local of
# the candidate, origin and its push URL set to an unreachable path, the tag created only there, the
# clone removed by an EXIT trap. No v0.3.0 tag ever exists in the shared ref store (a pushed v* tag
# would cut a real release, D57(c)). Its output is time-stamped (stamped.sh), and its power validity
# is judged only over its AC-sensitive windows: the isolated test/e2e pass of `ci-local test` and of
# `ci-local cover` (every other pass declares co-load or holds no timing judgement). It is retried once
# only when an event fell inside such a window, or the window ran on battery, and a second run of the
# same length can end before the deadline. It starts only when RC_EST_S lets it end by the deadline.
# Hosted ci.yml and nightly.yml on the same commit supply the native-platform lanes.
set -u
[ $# -eq 3 ] || { echo "usage: overnight-c8.sh <candidate-repo> <candidate-sha> <evidence-dir>" >&2; exit 2; }
C=$1; H=$2; E=$3
here=$(cd "$(dirname "$0")" && pwd)
. "$here/power.sh"
mkdir -p "$E" || exit 2
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
NOPUSH_URL=file:///nonexistent/qompack-release-check-scratch-clone-never-pushes
DOCKER_START_TIMEOUT_S=600; DOCKER_STOP_TIMEOUT_S=300   # docker desktop's own --timeout (default: none)
waited=${NIGHT_AC_WAITED_MIN:-0}
for v in "$AC_WAIT_BUDGET_MIN" "$RC_EST_S" "$waited"; do
  case $v in ''|*[!0-9]*) echo "overnight-c8.sh: not a whole number: '$v'" >&2; exit 2 ;; esac
done
deadline=$(deadline_epoch "$NIGHT_DEADLINE") || { echo "overnight-c8.sh: NIGHT_DEADLINE must be HH:MM, not '$NIGHT_DEADLINE'" >&2; exit 2; }
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
[ -s "$E/power.tsv" ] || printf 'step\ttry\texit\tverdict\tpower_start\tpower_end\tt0\tt1\n' > "$E/power.tsv"
record() { # record <step> <try> <exit> <verdict> <p0> <p1> <t0> <t1> [note]
  log "step $1 try $2 exit=$3 $4 power=$5->$6${9:+; $9}"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" "$5" "$6" "$7" "$8" >> "$E/power.tsv"
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

# ---- AC-gated steps ------------------------------------------------------------------------------
gated_cmd() {
  case $1 in
    win-timing|win-e2e-timing|win-x11-alone) sh "$here/phase3.sh" "$C" "$E" "$1" ;;
    c51-win) sh "$here/quiet.sh" "$C" "$QUIET_BASE" "$E/quiet" c51-win ;;
    c52-win) QUIET_PKGS="checkpoint cli" QUIET_BENCH_FILTER='^Benchmark(Finalize|AdvanceSegment|HookNoop_InProcess)$' \
               sh "$here/quiet.sh" "$C" "$QUIET_BASE" "$E/quiet-c52" c52-win ;;
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
      record "$g_s" "$g_n" - SKIPPED - - - - "the deadline $NIGHT_DEADLINE passed before it could start"
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
  g_v=$(power_verdict "$g_p0" "$g_ok" "$(power_events_window "$g_t0" "$g_t1" "$g_ev")")
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
step() { # step <name> <command...>
  s_name=$1; shift
  if past_deadline; then log "step $s_name SKIPPED: the deadline $NIGHT_DEADLINE passed"; count skip "$s_name"; return 0; fi
  log "step $s_name start power=$(power_read)"
  "$@" >> "$E/chain.log" 2>&1; s_rc=$?
  log "step $s_name finished exit=$s_rc"
  if [ "$s_rc" -eq 0 ]; then count pass "$s_name"; else count fail "$s_name"; fi
  return 0
}
linux_lanes() {
  ll_started=0
  if ! timeout -k 10 60 docker ps > /dev/null 2>&1; then
    log "docker engine not answering: starting it (docker desktop start --timeout $DOCKER_START_TIMEOUT_S)"
    if timeout -k 30 $((DOCKER_START_TIMEOUT_S + 60)) docker desktop start --timeout "$DOCKER_START_TIMEOUT_S" > /dev/null 2>&1; then
      ll_started=1; log "engine started by this chain"
    else
      log "engine start failed or timed out exit=$?"
    fi
  fi
  if timeout -k 10 300 docker start qompack-v6-linux-verification > /dev/null 2>&1 &&
     timeout -k 10 60 docker update --cpus 8 --memory 8g --memory-swap 8g qompack-v6-linux-verification > /dev/null 2>&1; then
    log "container start exit=0"
    for ll_s in linux-timing linux-e2e-timing linux-tree linux-e2e linux-child; do
      step "$ll_s" sh "$here/phase3.sh" "$C" "$E" "$ll_s"
    done
  else
    log "container start failed: the Linux lanes are SKIPPED"
    for ll_s in linux-timing linux-e2e-timing linux-tree linux-e2e linux-child; do
      log "step $ll_s SKIPPED: no container"; count skip "$ll_s"
    done
  fi
  timeout -k 10 120 docker stop qompack-v6-linux-verification > /dev/null 2>&1; log "container stopped exit=$?"
  if [ "$ll_started" = 1 ]; then
    timeout -k 30 $((DOCKER_STOP_TIMEOUT_S + 60)) docker desktop stop --timeout "$DOCKER_STOP_TIMEOUT_S" > /dev/null 2>&1
    log "engine stopped exit=$?"
  else
    log "engine left as found (D56(g))"
  fi
}

# ---- release-check -------------------------------------------------------------------------------
rc_prepare() {
  git clone -q --no-local --no-checkout --no-tags "$C" "$RCT/repo" &&
    git -C "$RCT/repo" remote set-url origin "$NOPUSH_URL" &&
    git -C "$RCT/repo" remote set-url --push origin "$NOPUSH_URL" || return 1
  git -C "$RCT/repo" cat-file -e "$H^{commit}" 2> /dev/null || git -C "$RCT/repo" fetch -q --no-tags "$C" "$H" || return 1
  git -C "$RCT/repo" -c advice.detachedHead=false checkout -q --detach "$H" || return 1
  [ "$(git -C "$RCT/repo" rev-parse HEAD)" = "$H" ] && [ -z "$(git -C "$RCT/repo" status --porcelain)" ] || return 1
  git -C "$RCT/repo" tag v0.3.0 "$H" || return 1
  [ "$(git -C "$RCT/repo" describe --tags --exact-match)" = v0.3.0 ]
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
# rc_verdict <start reading> <events-ok> <events> <windows file>
rc_verdict() {
  [ "$2" = 1 ] || { echo "NOT-REFERENCE the power history could not be read"; return 0; }
  [ -s "$4" ] || { echo "VALID (no AC-sensitive window was reached)"; return 0; }
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
  else echo "VALID"
  fi
}
release_check() {
  if [ $(( $(date +%s) + RC_EST_S )) -gt "$deadline" ]; then
    log "step release-check SKIPPED: it needs about $((RC_EST_S / 60)) min (RC_EST_S) and would end after the deadline $NIGHT_DEADLINE"
    count skip release-check; return 0
  fi
  if [ -e "$E/p3-release-check-tag.json" ] || [ -e "$E/p3-release-check-tag.log" ]; then
    # recrun.sh would refuse to overwrite it, and the windows would then be read from the old log.
    log "step release-check exit=2: a record p3-release-check-tag already exists in $E"
    count fail release-check; return 0
  fi
  rc_real_before=$(git -C "$C" rev-parse -q --verify refs/tags/v0.3.0 || true)
  [ -n "$rc_real_before" ] && log "WARNING: the shared ref store already holds a tag v0.3.0 ($rc_real_before), not made by this chain"
  RCT=$(winpath "$(mktemp -d)") || { log "step release-check exit=2: no scratch directory"; count fail release-check; return 0; }
  if ! rc_prepare > "$E/release-check-clone.txt" 2>&1; then
    log "step release-check exit=2: the scratch clone $RCT could not be prepared (release-check-clone.txt)"
    count fail release-check; cleanup; return 0
  fi
  log "release-check clone $RCT at $H: origin and push URL $NOPUSH_URL, tag v0.3.0 only in the clone"
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
    rc_v=$(rc_verdict "$rc_p0" "$rc_ok" "$rc_ev" "$rc_wf"); rm -f "$rc_wf"
    rc_note="events during the run: [$(power_events_window "$rc_t0" "$rc_t1" "$rc_ev" | tr '\n' ' ' | sed 's/ *$//')]"
    case $rc_v in
      INVALID-POWER*)
        if [ "$rc_try" -lt 2 ] && [ $(( $(date +%s) + rc_t1 - rc_t0 )) -le "$deadline" ]; then
          rc_d=$(move_aside release-check "$rc_try" "$rc_sf"); rm -f "$rc_sf"
          record release-check "$rc_try" "$rc_rc" "$rc_v" "$rc_p0" "$rc_p1" "$rc_t0" "$rc_t1" "$rc_note; neither a pass nor a fail; its records moved to $rc_d; retried once"
          rc_try=2; continue
        fi
        [ "$rc_try" -lt 2 ] && rc_note="$rc_note; not retried: a second $(( (rc_t1 - rc_t0) / 60 ))-min run would end after the deadline"
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
log "start candidate=$H deadline=$(date -d "@$deadline" +%FT%T) ac_wait_budget=${AC_WAIT_BUDGET_MIN}min used=${waited}min power=$(power_read)"
log "engine up at start=$(timeout -k 10 60 docker ps > /dev/null 2>&1 && echo 1 || echo 0)"
timeout -k 10 60 docker ps --format "{{.Names}} {{.Status}}" >> "$E/chain.log" 2>&1
timeout -k 10 120 docker stop qompack-v6-linux-verification > /dev/null 2>&1

queue="win-timing win-e2e-timing win-x11-alone"
run_queue now
step win-race env GOFLAGS=-p=4 sh "$here/phase3.sh" "$C" "$E" win-race
step bundles sh "$here/phase3.sh" "$C" "$E" bundles
run_queue now
linux_lanes
queue="$queue c51-win c52-win"
run_queue now
release_check
run_queue wait

total=$((n_pass + n_fail + n_inv + n_nref + n_skip))
outcome="steps=$total passed=$n_pass failed=$n_fail${failed:+ [${failed# }]} invalid_power=$n_inv not_reference=$n_nref (of which $n_nref_red exited non-zero) skipped=$n_skip ac_wait_used=${waited}min"
echo "$outcome" > "$E/overnight-outcome.txt"
log "done: $outcome"
[ $((n_fail + n_inv + n_nref + n_skip)) -eq 0 ]
