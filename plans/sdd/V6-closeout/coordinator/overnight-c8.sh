#!/bin/sh
# overnight-c8.sh <candidate-repo> <candidate-sha> <evidence-dir>
# Candidate 8's local night (D57-D61), strictly sequential, nothing else measuring. Progress goes to
# <evidence-dir>/chain.log (its first line carries this shell's MSYS and Windows pids, for
# nightabort.ps1), every step's power record to power.tsv, and the outcome counts to
# overnight-outcome.txt (c8-night.sh copies them into night.log). Exit 0 only when every step of the
# night's plan (below: candidate 8's night or the C5.2 night) ran, passed (or, for a report-only
# step, recorded its measurement) and, where power matters, was VALID.
# It refuses (exit 2, before writing anything):
#   - a <candidate-sha> that is not a full 40-character SHA, a <candidate-repo> whose HEAD is not
#     that commit, or one that is not clean (`git status --porcelain`, untracked files included):
#     every step runs in that checkout and every verdict is logged as that candidate, so a checkout
#     at candidate 7 would let c52derive.py compare candidate 7 with itself and carry every row;
#   - an evidence directory that already holds a night's records: recrun.sh never overwrites a
#     record, so a second night there would fail every step in seconds and could be read as a run;
#   - C8_C52_ONLY other than empty or 1 (a mistyped switch would run candidate 8's night, with
#     release-check, on the frozen candidate), C8_C52_SET other than full or derived, C8_C52_STEPS
#     naming anything but a C5.2 night step or set without C8_C52_ONLY=1, C8_NIGHT1_C52 other than
#     empty, 0 or 1, and in a C5.2 night a quiet.sh whose benches() list cannot be read (the C5.2
#     chunks are made from it);
#   - a drive (TMPDIR's, where every scratch clone goes, or GOCACHE's) with less than
#     NIGHT_MIN_FREE_GB (power.sh, 40 GiB) free, or one df cannot read.
#
# Power (D57(d), power.sh). Every step's power is recorded: t0 is taken before its last power check,
# and it is judged over [t0, end] against the System log (Kernel-Power 105, a source change; 506,
# Modern Standby; 42 and 107, sleep and resume) and its end reading:
#   VALID          started and ended on AC, no event: its exit status is a pass or a fail;
#   INVALID-POWER  an event during it: neither;
#   NOT-REFERENCE  it could get no AC within the night's wait budget, ran on battery, or the power
#                  history could not be read: not a reference measurement, not a pass, not a fail;
#   SKIPPED        the deadline passed before it could start, or its estimate would end after it.
# The AC-gated steps (queued until AC) are win-timing (ci.yml's timing lane plus the three
# functional integration hot-path rows, D53(a)), win-e2e-timing (all of test/e2e alone, as ci.yml's
# test-e2e job: this is where test/e2e's timing rows are judged, the ones prefreeze.sh's e2efunc
# skips), win-x11-alone (X11 by itself), quiet C5.1 on both
# OSes (c51-win; c51-linux, report only, below), and in a C5.2 night c116-rig and the C5.2 chunks
# (c52-win-<group>, c52-linux-<group>, below). An INVALID-POWER try moves its new records to
# <step>.invalid-power-1/ and the step is retried once. The AC-independent steps (win-race,
# bundles, the Linux lanes, c52-derive) are never waited for; for the Linux *-timing lanes,
# wall-clock judgements, only a VALID run counts as a pass or a fail, and the others count by exit
# status.
# Order: a gated step runs when AC is present; otherwise it waits in a queue while the AC-independent
# work runs, whose load also drains the battery toward the charger's restore point (about 35-40 %,
# D57(d)). Waiting for AC is bounded night-wide by AC_WAIT_BUDGET_MIN one-minute polls
# (NIGHT_AC_WAITED_MIN carries what c8-night.sh already spent) and by the deadline: no step and no
# wait starts after it, and every step starts only when its estimate lets it end by the deadline
# (release-check RC_EST_S, c116-rig, c52-derive, each C5.2 chunk; the others STEP_EST below, from
# candidate 6's night). A C5.2 chunk never runs on battery (D65(c)): it waits for AC past the
# budget, until its latest start (the deadline minus its estimate), and is otherwise SKIPPED, left
# for the next C5.2 night; it is never run NOT-REFERENCE. c8-night.sh passes its
# deadline as NIGHT_DEADLINE_EPOCH, so a pre-freeze that ends late cannot carry the night to the
# next day; run alone, the next NIGHT_DEADLINE (local HH:MM, default 08:00) is used, and refused
# when it is more than NIGHT_MAX_AHEAD_H (16) hours away (a daytime launch) unless
# NIGHT_ALLOW_FAR=1.
# Two nights (D62(c)):
#   - candidate 8's night (the default): the Windows timing steps, win-race, bundles, the Linux
#     lanes, C5.1 on both OSes, then release-check (C3.12 and D57(a)'s local reference run: a
#     release gate). The C1.16 rig is not part of it. Then, only with time left (C8_NIGHT1_C52,
#     default 1; 0 turns it off): the C5.2 chunks, full list, in order (each a self-contained ABBA
#     comparison, D65(b)), each started only when the machine is on AC at that moment (no wait)
#     and its estimate ends by the deadline. A chunk that ran VALID with exit 0 is done and counts
#     as passed; a VALID red counts as failed (a real red, to classify before it is measured
#     again); a chunk that did not fit, had no AC, or ended INVALID-POWER on both tries is left
#     for the C5.2 night and counts neither for nor against the night. overnight-outcome.txt names
#     what is left (pending_c52_night=[c116-rig ...]), so the C5.2 night may not be needed;
#   - the C5.2 night (C8_C52_ONLY=1, README.md "Candidate 8", "The C5.2 night"): c116-rig, then
#     the C5.2 chunks on Windows, then on Linux (in derived mode c52-derive first), nothing else;
#     C8_C52_STEPS limits it to the steps it names.
#
# C5.1 on Linux (c51-linux, report only). quiet.sh c51-linux in its own container window: B-D, B-E
# and B-F are recorded for inventory rows 1.10.16, 1.17.5 and 1.17.6, whose Linux halves cite
# candidate 6, while candidate 8 changes checkpoint and drain code B-E runs. Its exit status is
# neither a pass nor a fail: the container's fsync-bound B-A and B-B are not verified in target
# (D53(b)) and fail it on every candidate. A VALID run counts as "reported" when it wrote its
# harness JSON, and as failed when it did not (nothing was measured); its power is judged as any
# gated step's.
#
# C5.2 (D62(b)) is measured against cf31e01 in quiet.sh's ABBA rounds, in chunks: one AC-gated step
# per OS and package group, c52-<os>-<group>, for each <group> of C52_GROUPS (observer, store and
# checkpoint, the three longest packages, one chunk each) and "other" (every other package of
# quiet.sh's benches() list, read from that list). A chunk runs quiet.sh with QUIET_PKGS set to its
# packages, into its own quiet-<step>/ directory, with its own 10 ABBA rounds: a row's base and
# candidate sides alternate inside its chunk, so a chunk's evidence stands on its own, whichever
# night measured it. Why chunks: the charger cuts AC unattended (D57(d)). The System log
# (Kernel-Power 105) shows the last three nights' on-AC stretches under load: 1 h 26 min
# (2026-10-01 21:03-22:29), 6 h 6 min (23:55-06:01) and 2 h 57 min (2026-10-03 20:23-23:20); the
# two cuts inside a loaded night were followed by 1 h 26 min and 1 h 18 min on battery. Two of the
# three stretches are shorter than either OS's whole list (about 4.2 h and 3.8 h, below). A power
# event voids one chunk (at most about 93 min), retried once as any gated step, not an OS's list.
# C8_C52_SET (default full) picks the rows, and chain.log logs every chunk's packages and filter:
#   full     the FULL C5.2 list: every row of quiet.sh's benches() list (65 rows in 19 packages),
#            the chunks' packages together exactly the list's (other is the list's packages outside
#            C52_GROUPS, so no listed package can be left out by a copy kept here), and
#            QUIET_BENCH_FILTER empty. D57(e) is file-level (D62(b)), and candidate 8 changes
#            internal/config/config.go, which runs at package init (var globalSchema =
#            buildSchemaIndex()) in every benchmark binary that links internal/config: about 59 of
#            the 65 rows execute a changed file, so D62(b) re-measures all of them;
#   derived  D57(e) by construction, for a candidate whose diff reaches fewer rows: c52-derive
#            (c52derive.py) traces every listed row on the candidate at its own listed benchtime and
#            selects those whose executed product files, the other changed .go files of a package
#            they execute (declarations have no coverage block), own benchmark file, fixtures or
#            adjacent assets changed since C8_PREV_CANDIDATE (default candidate 7, d20309c0); each
#            chunk measures its packages' selected rows (QUIET_PKGS its packages among the
#            selection's, QUIET_BENCH_FILTER the selection's), and a chunk with none is not needed.
#            When the derivation cannot run at all, the FULL C5.2 list is measured, as in full mode
#            (a file-level rule with no execution trace knows no row that keeps its carry), and the
#            derivation counts as a failed step.
# A chunk never runs with an empty QUIET_PKGS, which quiet.sh reads as its whole list: gated_cmd
# refuses one. Each chunk starts only when its estimate (C52_EST_WIN / C52_EST_LINUX below, over its
# packages) ends by the deadline; otherwise it is SKIPPED and the next chunk is tried. C8_C52_STEPS
# (space-separated, default all) runs only the C5.2 night steps it names (c116-rig and the chunks),
# so a later C5.2 night measures what an earlier one voided, skipped or ran off AC. c51-linux and
# each c52-linux-* chunk bring the container up for themselves and stop it afterwards.
#
# Docker (D56(g): never stop the owner's engine, which runs their supabase stack). Whether the
# engine was up at the night's start is decided by three probes, 30 s apart, as everywhere else
# (one failed `docker ps` is not an engine that is down). When the engine then does not answer
# three probes, Docker Desktop is asked to start it; this chain counts the engine as its own only
# when the engine was down at the start AND `docker desktop status`, read just before the start,
# said stopped. An engine this chain started is stopped afterwards only when `docker ps` lists no
# container but the night's own; when other containers run (the owner started theirs) or the
# listing fails, it is left running, and chain.log says why.
#
# Signals. Every long child (each step, release-check, quiet.sh) runs in the background and the
# shell waits for it, so a TERM, INT or HUP runs the trap at once: it stops that child (TERM to
# its pid), removes the release-check clone, and exits 143, 130 or 129; no step starts after it.
# A plain foreground child would hold the trap until it ended, up to release-check's 3 h. The
# child's own children (go test binaries, docker exec) may outlive it: only nightabort.ps1 stops
# the whole tree, and README.md's Abort steps 1-5 still apply.
#
# The C1.16 rig (c116-rig, Windows, AC-gated, D62(c)): phase3.sh's c116-rig arm, w2-lifetime's
# procedure (plans/sdd/V6-closeout/w2-lifetime/runs/08-17 and 36) on the frozen candidate:
# TestSessionStartCompact_UnderSameSessionIngest with 30 compaction cycles, 8 feeders and 256 KiB
# Reads, once with no extra load (as runs/17 and 36) and once with the rig's in-process fsync and
# CPU co-load (as runs/15). The distributions under w2-lifetime/runs are stale: before 3f2da1b3 the
# rig's Reads never reached its daemon, so the same-session ingest they describe never ran. A pass
# is both runs green (every answer the rehydration or the deferred note, every Read routed to the
# rig) with their distributions logged; the numbers are recorded in chain.log, not judged against a
# bound. It starts only when C116_EST_S lets it end by the deadline.
#
# release-check --tag v0.3.0 (C3.12/C7.4) runs in an ISOLATED scratch clone: git clone --no-local of
# the candidate, origin and its push URL set to an unreachable path, the tag created only there, the
# clone removed by an EXIT trap. No v0.3.0 tag ever exists in the shared ref store (a pushed v* tag
# would cut a real release, D57(c)). Its output is time-stamped (stamped.sh), and its power validity
# is judged only over its AC-sensitive windows: the isolated test/e2e pass of `ci-local test` and of
# `ci-local cover` (every other pass declares co-load or holds no timing judgement). A window with a
# power event is INVALID-POWER; one that ran wholly on battery is NOT-REFERENCE. A run whose log
# shows no window is VALID only when it failed before `ci-local test` began; otherwise it is
# NOT-REFERENCE. It is retried once, in a fresh clone, only when an event fell inside a window or a
# window ran on battery, AC is present (or returns within the budget), and a second run can end
# before the deadline: one as long as try 1 when try 1 passed (a whole run), and RC_EST_S when it
# failed (release-check stops at its first FAIL, so a red try's length says nothing about a green
# one's). It starts only when RC_EST_S lets it end by the deadline; on battery it first waits for AC
# (within the night's budget) until its latest start, the deadline minus RC_EST_S, then runs either
# way. RC_EST_S (power.sh, 3 h) has never been measured with release-check's current step list, so
# a run still going at the deadline plus RC_GRACE_S (30 min) is stopped (timeout, then a kill 60 s
# later) and recorded SKIPPED-OVERRUN: neither a pass nor a fail, never retried. A red run whose
# govulncheck section says the vulnerability database or the module proxy could not be reached
# (power.sh's patterns, the ones prefreeze.sh's gate uses) is labelled so and counts NOT-REFERENCE,
# not failed: it is not a product red, but release-check stopped there, so its later steps never
# ran and it is no pass either.
# Hosted ci.yml and nightly.yml on the same commit supply the native-platform lanes.
set -u
[ $# -eq 3 ] || { echo "usage: overnight-c8.sh <candidate-repo> <candidate-sha> <evidence-dir>" >&2; exit 2; }
C=$1; H=$2; E=$3
here=$(cd "$(dirname "$0")" && pwd)
. "$here/power.sh"
refuse() { echo "overnight-c8.sh: REFUSED: $*" >&2; exit 2; }
# The checkout every step runs in must be the candidate every verdict is logged as, and clean.
case $H in ''|*[!0-9a-f]*) refuse "<candidate-sha> must be the full 40-character SHA in lower case, not '$H'" ;; esac
[ ${#H} -eq 40 ] || refuse "<candidate-sha> must be the full 40-character SHA, not '$H'"
ch=$(git -C "$C" rev-parse -q --verify 'HEAD^{commit}') || refuse "$C is not a git checkout"
[ "$ch" = "$H" ] || refuse "$C is at $ch, not the candidate $H (detach it there first: git -C $C checkout --detach $H)"
cs=$(git -C "$C" status --porcelain) || refuse "cannot read the status of $C"
[ -z "$cs" ] || refuse "$C is not clean: $(printf '%s\n' "$cs" | head -n 3 | tr '\n' ' ')"
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
# RC_EST_S (power.sh, 3 h): release-check's estimate, for its latest start; a retry is sized by try
# 1's own duration instead. RC_GRACE_S: how long past the deadline a release-check still running is
# allowed before it is stopped (header).
RC_GRACE_S=${RC_GRACE_S:-1800}
QUIET_BASE=cf31e01                              # quiet.sh's pre-Phase-2 base (its header)
PREV_CANDIDATE=${C8_PREV_CANDIDATE:-d20309c03ffc364e4cc48663be73cfbb1f2309b2}   # candidate 7 (phase3/c7-CANDIDATE.md)
# The night's plan (header, "Two nights"): only an empty switch or exactly 1, so a mistyped one
# cannot run candidate 8's night, release-check included, on the frozen candidate.
case ${C8_C52_ONLY:-} in ''|1) ;; *) refuse "C8_C52_ONLY must be empty or 1, not '$C8_C52_ONLY'" ;; esac
c52only() { [ "${C8_C52_ONLY:-}" = 1 ]; }
# C8_NIGHT1_C52 (header, "Two nights"): candidate 8's night runs C5.2 chunks after release-check when
# time and AC allow; 0 turns that off.
case ${C8_NIGHT1_C52:-} in ''|0|1) ;; *) refuse "C8_NIGHT1_C52 must be empty, 0 or 1, not '$C8_NIGHT1_C52'" ;; esac
# C5.2's set (header): full, the whole list (D62(b), candidate 8's), or derived (D57(e) by
# c52derive.py, whose failure measures the whole list too).
C52_SET=${C8_C52_SET:-full}
case $C52_SET in full|derived) ;; *) refuse "C8_C52_SET must be full or derived, not '$C52_SET'" ;; esac
C52_PKGS=""; C52_FILTER=""; C52_STATE=none
# C5.2's chunks (header): C52_GROUPS' packages one chunk each, and "other" for the rest of the list.
# The three are the longest packages in C52_EST_WIN and C52_EST_LINUX below (observer 83 and 51 min,
# store 55 and 61, checkpoint 42 and 46); the other 16 take 32 min on Windows and 28 on Linux. So
# every chunk, with C52_EST_OVERHEAD_S, is at most 93 min (observer on Windows), and a power event
# costs no more than that chunk.
C52_GROUPS="observer store checkpoint"
C52_CHUNKS=""
for o in win linux; do for g in $C52_GROUPS other; do C52_CHUNKS="$C52_CHUNKS c52-$o-$g"; done; done
C52_CHUNKS=${C52_CHUNKS# }
C52_NIGHT="c116-rig $C52_CHUNKS"                # the C5.2 night's steps, in order
# c52_list <field>: quiet.sh's benches() list, as c52derive.py reads it (the heredoc after
# "benches() { cat <<'<delim>'" up to <delim>): field 1 prints each row's package, 0 the rows.
c52_list() {
  awk -v fld="$1" '
    d == "" && /^benches\(\) \{ cat <<'\''[A-Za-z0-9_]+'\''[ \t]*$/ { d = $0; sub(/^[^'\'']*'\''/, "", d); sub(/'\''.*$/, "", d); next }
    d != "" && $0 == d { exit }
    d != "" && NF >= 2 { print (fld == 0 ? $0 : $fld) }' "$here/quiet.sh"
}
# C8_C52_STEPS: the C5.2 night's steps to run (default all), so a later night runs only what an
# earlier one left; a name outside the plan, or the variable outside a C5.2 night, is a mistake.
if [ -n "${C8_C52_STEPS:-}" ]; then
  c52only || refuse "C8_C52_STEPS ('$C8_C52_STEPS') belongs to a C5.2 night (C8_C52_ONLY=1)"
  for s in $C8_C52_STEPS; do
    case " $C52_NIGHT " in *" $s "*) ;; *) refuse "C8_C52_STEPS: '$s' is not a C5.2 night step ($C52_NIGHT)" ;; esac
  done
fi
sel() { case " ${C8_C52_STEPS:-$C52_NIGHT} " in *" $1 "*) return 0 ;; esac; return 1; }
C52_SEL=""; for s in $C52_NIGHT; do sel "$s" && C52_SEL="$C52_SEL $s"; done
C52_SEL=${C52_SEL# }                             # the steps a C5.2 night runs, in the plan's order
if c52only && [ -z "$(c52_list 1)" ]; then
  refuse "cannot read quiet.sh's benches() list ($here/quiet.sh): the C5.2 chunks are made from it"
fi
# C5.2's end-by-deadline estimates, per package, in seconds: candidate 5's measured run time of ALL
# that package's listed rows over its 10 ABBA rounds, both sides (the started_at/ended_at of every
# phase3/c5/quiet/c52-{win,linux}/c52-*-r<round>-*.json record, summed per package), rounded up to
# the minute. A package measured for a subset of its rows takes no longer than for all of them, so
# the estimate of a selection is an upper bound on its run time; a package not in the table counts
# as the table's largest entry. C52_EST_OVERHEAD_S per chunk covers the builds, warm-up runs,
# benchstat and gate preparation: candidate 5 spent 430 s (Windows) and about 330 s (Linux, its gate
# preparation included) outside the measured runs of the whole list, rounded up to 10 min. A chunk
# builds a subset of those packages and repeats the fixed part once, so the whole list's overhead
# bounds a chunk's. The chunks then sum to 15120 s (4 h 12 min) on Windows and 13560 s (3 h 46 min)
# on Linux, against candidate 5's unchunked 3 h 29 min and 3 h 03 min.
C52_EST_WIN="observer:4980 store:3300 checkpoint:2520 daemon:360 chunk:180 scheduler:180 canon:180 rules:120 eval:120 negknow:120 symbols:120 dag:120 paths:60 sketch:60 skills:60 obs:60 cli:60 config:60 hostperm:60"
C52_EST_LINUX="store:3660 observer:3060 checkpoint:2760 daemon:300 chunk:180 canon:120 scheduler:120 eval:120 negknow:120 symbols:120 dag:120 sketch:60 obs:60 paths:60 rules:60 cli:60 config:60 skills:60 hostperm:60"
C52_EST_OVERHEAD_S=600
# C52_DERIVE_EST_S: c52derive.py traces each listed row once at its own benchtime with coverage on.
# One round of every listed row took 612 s on candidate 5 (12250 s / 20 runs); with set-mode
# coverage (up to about 1.5x), a coverage-instrumented link per row (about 10 s x 65) and the first
# instrumented build of the module (about 3 min), about 30 min; 45 min with margin.
C52_DERIVE_EST_S=2700
# C116_EST_S: c116-rig's end-by-deadline estimate, an upper bound rather than a measurement. Its two
# go test runs carry -timeout=30m each (phase3.sh), which bounds what they can take, plus 5 min for
# building the internal/cli test binary (the second run reuses it). The runs it reproduces took
# 18-50 s each (w2-lifetime/runs/08-17, 36), but those Reads never reached the daemon (3f2da1b3), so
# no measured time exists for the rig as it now runs.
C116_EST_S=3900
# STEP_EST: the end-by-deadline estimates of every other step, in minutes, from candidate 6's night
# (phase3/c6/chain.log and its records, 2026-10-02), rounded up with margin:
#   win-timing 15 (p3-win-timing 4 min 49 s, then the three hot-path rows alone); win-e2e-timing 35
#   (25 min 33 s, candidate 5's 25 min 14 s; its hard stop is -timeout=45m); win-x11-alone 15
#   (10 min 54 s); win-race 50 and bundles 15 (52 min 19 s together); linux-timing 15 and
#   linux-e2e-timing 30 (25 min together); linux-tree 45, linux-e2e 40 and linux-child 15 (67 min
#   33 s together); c51-win 10 (4 min 21 s); c51-linux 20 (2 min 29 s, plus a container start that
#   may first wait for the engine).
STEP_EST="win-timing:15 win-e2e-timing:35 win-x11-alone:15 win-race:50 bundles:15 linux-timing:15 linux-e2e-timing:30 linux-tree:45 linux-e2e:40 linux-child:15 c51-win:10 c51-linux:20"
step_est() { # step_est <step>: its STEP_EST in seconds, or nothing
  for se_p in $STEP_EST; do [ "${se_p%%:*}" = "$1" ] && { echo $(( ${se_p#*:} * 60 )); return 0; }; done
}
NOPUSH_URL=file:///nonexistent/qompack-release-check-scratch-clone-never-pushes
DOCKER_START_TIMEOUT_S=600; DOCKER_STOP_TIMEOUT_S=300   # docker desktop's own --timeout (default: none)
waited=${NIGHT_AC_WAITED_MIN:-0}
for v in "$AC_WAIT_BUDGET_MIN" "$RC_EST_S" "$RC_GRACE_S" "$waited"; do
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
# The night's room (header): the last refusal, so a refused launch has run nothing but these reads.
d_msgs=""; dmsg() { d_msgs="$d_msgs${d_msgs:+; }$*"; }
disk_free_ok dmsg "${TMPDIR:-/tmp}" "$(go env GOCACHE 2> /dev/null)" ||
  refuse "not enough room for the night's clones and test binaries: $d_msgs"

RCT=""
cleanup() {
  if [ -n "$RCT" ] && [ -d "$RCT" ]; then
    if rm -rf "$RCT"; then log "release-check scratch clone $RCT removed"
    else log "release-check scratch clone $RCT could NOT be removed (a process still runs in it? README.md Abort steps 1 and 4)"; fi
  fi
  RCT=""
}
# fg <command...>: runs a long child in the background and waits for it, so a signal's trap runs at
# once instead of when the child ends (header, "Signals"); its exit status is the child's.
FG=""
fg() { "$@" & FG=$!; wait "$FG"; fg_rc=$?; FG=""; return "$fg_rc"; }
on_signal() {
  if [ -n "$FG" ] && kill -TERM "$FG" 2> /dev/null; then
    log "signal: stopped the running child (pid $FG); its own children may outlive it (README.md Abort step 1, nightabort.ps1)"
  fi
  FG=""; exit "$1"
}
trap cleanup EXIT; trap 'on_signal 130' INT; trap 'on_signal 143' TERM; trap 'on_signal 129' HUP

# ---- outcome -------------------------------------------------------------------------------------
n_pass=0; n_fail=0; n_inv=0; n_nref=0; n_nref_red=0; n_skip=0; n_rep=0; failed=""
count() { # count <pass|fail|invalid|nref|skip|report> <step> [exit]
  case $1 in
    pass) n_pass=$((n_pass + 1)) ;;
    report) n_rep=$((n_rep + 1)) ;;
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

# ---- docker (header, "Docker") -------------------------------------------------------------------
engine_up_at_start=0; cs_started=0
docker_answers() { # three probes, 30 s apart: one failed probe is not an engine that is down
  da_n=0
  while :; do
    timeout -k 10 60 docker ps > /dev/null 2>&1 && return 0
    da_n=$((da_n + 1)); [ "$da_n" -ge 3 ] && return 1
    sleep 30
  done
}
docker_desktop_state() { # Docker Desktop's own status word (running, stopped, starting, ...), or unknown
  dds=$(timeout -k 10 60 docker desktop status 2> /dev/null | tr -d '\r' |
    sed -n 's/^Status[[:space:]][[:space:]]*\([A-Za-z-][A-Za-z-]*\).*/\1/p' | head -n 1)
  echo "${dds:-unknown}"
}
others_running() { # the running containers other than the night's, one per line; 1 when unlisted
  orl=$(timeout -k 10 60 docker ps --format '{{.Names}}' 2> /dev/null) || return 1
  printf '%s\n' "$orl" | tr -d '\r' | grep -vx -e 'qompack-v6-linux-verification' -e ''
  return 0
}
docker_desktop_start() {
  timeout -k 30 $((DOCKER_START_TIMEOUT_S + 60)) docker desktop start --timeout "$DOCKER_START_TIMEOUT_S" > /dev/null 2>&1
}
container_up() { # 0 when the container runs; cs_started=1 only when this chain started the engine
  cs_started=0
  if ! docker_answers; then
    cu_st=$(docker_desktop_state)
    if [ "$engine_up_at_start" = 1 ]; then
      log "docker engine not answering (3 probes) though it was up at the night's start (Docker Desktop status: $cu_st): it is the owner's engine, so this chain asks Docker Desktop to start it but never stops it (D56(g))"
      docker_desktop_start; log "engine start (the owner's engine) exit=$?"
    elif [ "$cu_st" != stopped ]; then
      log "docker engine not answering (3 probes), but Docker Desktop's status is '$cu_st', not stopped: this chain asks Docker Desktop to start it, and since it did not find the engine stopped, never stops it (D56(g))"
      docker_desktop_start; log "engine start (not started by this chain: it was not stopped) exit=$?"
    else
      log "docker engine not answering (3 probes) and Docker Desktop's status is stopped: starting it (docker desktop start --timeout $DOCKER_START_TIMEOUT_S)"
      if docker_desktop_start; then
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
    if ! cd_o=$(others_running); then
      log "engine left running: its containers could not be listed, so this chain cannot tell that nothing else runs on it (D56(g))"
    elif [ -n "$cd_o" ]; then
      log "engine left running: other containers run on it ($(printf '%s' "$cd_o" | tr '\n' ' ' | sed 's/ *$//')), so it is not this chain's alone (D56(g))"
    else
      timeout -k 30 $((DOCKER_STOP_TIMEOUT_S + 60)) docker desktop stop --timeout "$DOCKER_STOP_TIMEOUT_S" > /dev/null 2>&1
      log "engine stopped exit=$?"
    fi
  else
    log "engine left as found (D56(g))"
  fi
  cs_started=0
}

# ---- AC-gated steps ------------------------------------------------------------------------------
gated_cmd() {
  case $1 in
    win-timing|win-e2e-timing|win-x11-alone|c116-rig) sh "$here/phase3.sh" "$C" "$E" "$1" ;;
    c51-win) sh "$here/quiet.sh" "$C" "$QUIET_BASE" "$E/quiet" c51-win ;;
    c51-linux)   # its own top-level directory, so an INVALID-POWER try's records move aside whole
      if container_up; then
        log "container start exit=0 (c51-linux)"
        sh "$here/quiet.sh" "$C" "$QUIET_BASE" "$E/quiet-c51-linux" c51-linux
        gc_rc=$?
      else
        log "container start failed: c51-linux did not run"; gc_rc=2
      fi
      container_down; return "$gc_rc" ;;
    c52-win-*|c52-linux-*)   # a C5.2 chunk, into its own top-level directory quiet-<step>/
      gc_pk=$(c52pk_of "$1")
      if [ -z "$gc_pk" ]; then   # quiet.sh reads an empty QUIET_PKGS as its whole list
        echo "overnight-c8.sh: $1 has no packages to measure, so it did not run"; return 2
      fi
      case $1 in
        c52-win-*) QUIET_PKGS=$gc_pk QUIET_BENCH_FILTER=$C52_FILTER sh "$here/quiet.sh" "$C" "$QUIET_BASE" "$E/quiet-$1" c52-win ;;
        *)
          if container_up; then
            log "container start exit=0 ($1)"
            QUIET_PKGS=$gc_pk QUIET_BENCH_FILTER=$C52_FILTER sh "$here/quiet.sh" "$C" "$QUIET_BASE" "$E/quiet-$1" c52-linux
            gc_rc=$?
          else
            log "container start failed: $1 did not run"; gc_rc=2
          fi
          container_down; return "$gc_rc" ;;
      esac ;;
    *) echo "overnight-c8.sh: unknown gated step $1" >&2; return 2 ;;
  esac
}
tries_of() { eval "echo \${tries_$(printf '%s' "$1" | tr -c 'A-Za-z0-9' '_'):-0}"; }
set_tries() { eval "tries_$(printf '%s' "$1" | tr -c 'A-Za-z0-9' '_')=$2"; }
c52pk_of() { eval "echo \"\${c52pk_$(printf '%s' "$1" | tr -c 'A-Za-z0-9' '_'):-}\""; }   # a chunk's QUIET_PKGS
set_c52pk() { eval "c52pk_$(printf '%s' "$1" | tr -c 'A-Za-z0-9' '_')=\"\$2\""; }
# c52_est_s <table> <packages>: seconds (see C52_EST_WIN)
c52_est_s() {
  printf '%s\n' $1 | awk -F: -v pk="$2" -v oh="$C52_EST_OVERHEAD_S" '
    { t[$1] = $2 + 0; if ($2 + 0 > mx) mx = $2 + 0 }
    END { s = oh; n = split(pk, a, " "); for (i = 1; i <= n; i++) s += (a[i] in t) ? t[a[i]] : mx; print s }'
}
# c52_group_pkgs <group>: a chunk's packages from quiet.sh's list, in its order: the group's own
# package, or for "other" every listed package outside C52_GROUPS. Empty when none is listed.
c52_group_pkgs() {
  c52_list 1 | awk -v g="$1" -v named="$C52_GROUPS" '
    BEGIN { n = split(named, a, " "); for (i = 1; i <= n; i++) nm[a[i]] = 1 }
    seen[$0]++ { next }
    (g == "other" && !($0 in nm)) || $0 == g { printf "%s%s", sp, $0; sp = " " }'
}
c52_isect() { # c52_isect <words> <set>: the words that are in the set, in their own order
  for ci_w in $1; do case " $2 " in *" $ci_w "*) printf '%s\n' "$ci_w" ;; esac; done | tr '\n' ' ' | sed 's/ $//'
}
gated_est() { # gated_est <step>: its end-by-deadline estimate in seconds
  case $1 in
    c116-rig) echo "$C116_EST_S" ;;
    c52-win-*) c52_est_s "$C52_EST_WIN" "$(c52pk_of "$1")" ;;
    c52-linux-*) c52_est_s "$C52_EST_LINUX" "$(c52pk_of "$1")" ;;
    *) step_est "$1" ;;
  esac
}
gated_skip_note() { # gated_skip_note <step>: what a step SKIPPED for its estimate leaves behind
  case $1 in
    c116-rig) echo "C116_EST_S: its two runs' -timeout=30m plus the build; a later C5.2 night (C8_C52_ONLY=1, C8_C52_STEPS naming it) re-runs it" ;;
    win-*|c51-*) echo "STEP_EST: candidate 6's time for it; re-run it on this candidate (README.md Abort step 7)" ;;
    c52-*)
      case $C52_STATE in
        derived) echo "C52_EST_*: candidate 5's time for its packages; its rows of c52-derive/selection.tsv wait for a later C5.2 night (C8_C52_ONLY=1, C8_C52_STEPS naming it)" ;;
        *) echo "C52_EST_*: candidate 5's time for its packages; a later C5.2 night (C8_C52_ONLY=1, C8_C52_STEPS naming it) measures it" ;;
      esac ;;
  esac
}

# run_gated <step> <now|wait>: 0 once the step has its final verdict, 1 while it stays queued. "now"
# runs it only if AC is present; "wait" waits for AC within the budget, else runs it NOT-REFERENCE.
# A step with an estimate starts (and is retried) only when the estimate lets it end by the deadline.
run_gated() {
  g_s=$1; g_mode=$2; g_n=$(( $(tries_of "$g_s") + 1 )); g_note=""; g_est=$(gated_est "$g_s"); g_noac=0
  while :; do   # the deadline and the estimate are checked again after every wait
    if past_deadline; then
      record "$g_s" "$g_n" - SKIPPED - - - - "the deadline $DL passed before it could start"
      count skip "$g_s"; return 0
    fi
    if [ -n "$g_est" ] && [ $(( $(date +%s) + g_est )) -gt "$deadline" ]; then
      record "$g_s" "$g_n" - SKIPPED - - - - "${g_note:+$g_note; }it needs about $(( (g_est + 59) / 60 )) min ($(gated_skip_note "$g_s")) and would end after the deadline $DL"
      count skip "$g_s"; return 0
    fi
    g_t0=$(date +%s); g_p0=$(power_read)      # t0 before the last power check (D57(d))
    case $g_p0 in "AC "*) break ;; esac
    [ "$g_noac" = 1 ] && break                # the wait budget is spent: it runs, NOT-REFERENCE
    [ "$g_mode" = now ] && return 1
    case $g_s in
      c52-*)   # D65(c): never on battery; wait for AC past the budget until its latest start
        if ! power_wait_ac waited - "$((deadline - g_est))" log; then
          record "$g_s" "$g_n" - SKIPPED - - - - "no AC by its latest start $(date -d "@$((deadline - g_est))" +%FT%T) (D65(c): a C5.2 chunk never runs on battery; it needs about $(( (g_est + 59) / 60 )) min and the deadline is $DL); $(gated_skip_note "$g_s")"
          count skip "$g_s"; return 0
        fi ;;
      *)
        if ! power_wait_ac waited "$AC_WAIT_BUDGET_MIN" "$deadline" log; then
          g_note="no AC within the night's wait budget"; g_noac=1
        fi ;;
    esac
  done
  g_sf=$(mktemp); snap "$g_sf"
  log "step $g_s try $g_n start power=$g_p0"
  fg gated_cmd "$g_s" >> "$E/chain.log" 2>&1; g_rc=$?
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
    VALID)
      if [ "$g_s" = c51-linux ]; then   # report only (header): recorded, never judged by its exit
        if [ -s "$E/quiet-c51-linux/c51-linux/c51-linux-hotpath.json" ]; then
          count report "$g_s"
          g_note="${g_note:+$g_note; }report only: B-D, B-E and B-F are recorded in quiet-c51-linux/; its exit status is neither a pass nor a fail (the container's B-A and B-B are not verified in target, D53(b))"
        else
          count fail "$g_s"
          g_note="${g_note:+$g_note; }report only, but it wrote no quiet-c51-linux/c51-linux/c51-linux-hotpath.json: nothing was measured"
        fi
      elif [ "$g_rc" -eq 0 ]; then count pass "$g_s"; else count fail "$g_s"; fi ;;
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
  s_est=$(step_est "$s_name")
  if [ -n "$s_est" ] && [ $(( $(date +%s) + s_est )) -gt "$deadline" ]; then
    skipped "$s_name" "it needs about $((s_est / 60)) min (STEP_EST: candidate 6's time for it) and would end after the deadline $DL"
    return 0
  fi
  s_t0=$(date +%s); s_p0=$(power_read)
  log "step $s_name start power=$s_p0"
  fg "$@" >> "$E/chain.log" 2>&1; s_rc=$?
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
  ll_fit=0   # the container starts only if at least one lane can end by the deadline
  for ll_s in $ll_steps; do [ $(( $(date +%s) + $(step_est "$ll_s") )) -le "$deadline" ] && ll_fit=1; done
  if [ "$ll_fit" = 0 ]; then
    for ll_s in $ll_steps; do skipped "$ll_s" "it needs about $(( $(step_est "$ll_s") / 60 )) min (STEP_EST: candidate 6's time for it) and would end after the deadline $DL"; done
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

# ---- C5.2's set (D62(b); D57(e) in derived mode) -------------------------------------------------
c52_full_note() { # the whole list, as chain.log names it
  cf_n=$(c52_list 0 | wc -l | tr -d ' ')
  cf_p=$(c52_list 1 | sort -u | wc -l | tr -d ' ')
  echo "all $cf_n rows of quiet.sh's benches() list ($cf_p packages), in chunks whose packages together are exactly the list's ($C52_GROUPS one chunk each, other the rest), with QUIET_BENCH_FILTER empty${C8_C52_STEPS:+; tonight only C8_C52_STEPS: $C8_C52_STEPS}"
}
c52_set() { # sets C52_STATE: full, derived, fallback (the whole list after a failed derivation) or skipped
  if [ "$C52_SET" = full ]; then
    C52_STATE=full; C52_PKGS=""; C52_FILTER=""
    log "c52: the FULL C5.2 list is measured: $(c52_full_note) (C8_C52_SET=full, D62(b): D57(e) is file-level, and candidate 8's internal/config/config.go runs at package init in every benchmark binary linking internal/config)"
    return 0
  fi
  C52_STATE=skipped
  if past_deadline; then skipped c52-derive "the deadline $DL passed"; return 0; fi
  if [ $(( $(date +%s) + C52_DERIVE_EST_S )) -gt "$deadline" ]; then
    skipped c52-derive "it needs about $((C52_DERIVE_EST_S / 60)) min (C52_DERIVE_EST_S) and would end after the deadline $DL, so C5.2 cannot run tonight either (a later C5.2 night, C8_C52_ONLY=1, measures it)"
    return 0
  fi
  step c52-derive python "$here/c52derive.py" "$(winpath "$C")" "$PREV_CANDIDATE" "$(winpath "$E/c52-derive")"
  if [ -f "$E/c52-derive/pkgs.txt" ] && [ -f "$E/c52-derive/filter.txt" ]; then
    C52_STATE=derived; C52_PKGS=$(cat "$E/c52-derive/pkgs.txt"); C52_FILTER=$(cat "$E/c52-derive/filter.txt")
    log "c52: derived set (C8_C52_SET=derived, D57(e), executed files changed since $PREV_CANDIDATE): QUIET_PKGS='$C52_PKGS' QUIET_BENCH_FILTER='$C52_FILTER' (c52-derive/report.txt)"
  else
    C52_STATE=fallback; C52_PKGS=""; C52_FILTER=""
    log "c52: the derivation produced no selection, so the FULL C5.2 list is measured: $(c52_full_note) (D62(b): D57(e) is file-level, and with no execution trace no row is known to keep its carry)"
  fi
}
c52_queue() { # queues this night's C5.2 chunks, each with its packages, or records why one does not run
  case $C52_STATE in
    skipped)
      for cq_s in $C52_CHUNKS; do sel "$cq_s" && skipped "$cq_s" "no C5.2 derivation tonight (c52-derive SKIPPED)"; done
      return 0 ;;
    derived)
      if [ -z "$C52_FILTER" ]; then
        log "c52: no C5.2 benchmark executes a file changed since $PREV_CANDIDATE, so every candidate 5 C5.2 row carries (D57(e)); no chunk is needed (c52-derive/report.txt)"
        return 0
      fi ;;
  esac
  cq_w=0; cq_l=0
  for cq_s in $C52_CHUNKS; do
    sel "$cq_s" || continue
    cq_pk=$(c52_group_pkgs "${cq_s##*-}")
    [ "$C52_STATE" = derived ] && cq_pk=$(c52_isect "$cq_pk" "$C52_PKGS")
    if [ -z "$cq_pk" ]; then
      if [ "$C52_STATE" = derived ]; then log "c52: $cq_s: no selected row in its packages (D57(e)), so it is not needed"
      else log "c52: $cq_s: quiet.sh's list has no package in its group, so it has nothing to measure"; fi
      continue
    fi
    set_c52pk "$cq_s" "$cq_pk"
    cq_e=$(gated_est "$cq_s")
    case $cq_s in c52-win-*) cq_w=$((cq_w + cq_e)) ;; *) cq_l=$((cq_l + cq_e)) ;; esac
    log "c52: $cq_s measures QUIET_PKGS='$cq_pk' QUIET_BENCH_FILTER='$C52_FILTER' into quiet-$cq_s/, about $(( (cq_e + 59) / 60 )) min"
    queue="$queue $cq_s"
  done
  log "c52: tonight's chunks need about $(( (cq_w + 59) / 60 )) min on Windows and $(( (cq_l + 59) / 60 )) min on Linux (C52_EST_*: candidate 5's time for each chunk's packages, plus $((C52_EST_OVERHEAD_S / 60)) min a chunk); each starts only if it can end by $DL"
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
  rv_bad=""; rv_nref=""
  while read -r rv_w rv_sec rv_ws rv_we rv_how; do
    rv_st=$(printf '%s\n' "$3" | awk -v ws="$rv_ws" -v s="${1%% *}" '
      $1 ~ /^[0-9]+$/ && $1 + 0 < ws + 0 && $2 ~ /^AC=/ { s = ($2 == "AC=1") ? "AC" : "BAT" } END { print s }')
    rv_in=$(power_events_window "$rv_ws" "$rv_we" "$3" | tr '\n' ' ' | sed 's/ *$//')
    if [ -n "$rv_in" ]; then rv_bad="$rv_bad $rv_sec:events[$rv_in]"     # an event during it
    elif [ "$rv_st" = BAT ]; then rv_nref="$rv_nref $rv_sec:on-battery"  # wholly on battery
    elif [ "$rv_st" != AC ]; then rv_nref="$rv_nref $rv_sec:power-unknown"
    fi
  done < "$4"
  if [ -n "$rv_bad" ]; then echo "INVALID-POWER$rv_bad"
  elif [ -n "$rv_nref" ]; then echo "NOT-REFERENCE$rv_nref"
  elif [ -z "$3" ] && case $7 in "AC "*) false ;; *) true ;; esac; then
    echo "NOT-REFERENCE ended on $7 with no power event recorded"
  else echo "VALID"
  fi
}
# rc_retry_blocker <seconds> <why that length>: sets rc_why to the reason a second try cannot run, or
# "". A retry needs AC (a battery run would void it again) within the night's budget, and time to end
# by the deadline.
rc_retry_blocker() {
  rc_why=""
  case $(power_read) in
    "AC "*) ;;
    *) power_wait_ac waited "$AC_WAIT_BUDGET_MIN" "$deadline" log || rc_why="no AC for a second run" ;;
  esac
  if [ -z "$rc_why" ] && [ $(( $(date +%s) + $1 )) -gt "$deadline" ]; then
    rc_why="a second run of about $(( ($1 + 59) / 60 )) min ($2) would end after the deadline"
  fi
  return 0
}
# rc_govuln_unreachable <stamped log>: release-check stopped in its govulncheck step, and that
# section says the database or proxy could not be reached (power.sh's govuln_unreachable).
rc_govuln_unreachable() {
  rgu_f=$(mktemp) || return 1
  awk '{ l = $0; sub(/^[0-9]+ [^ ]+ /, "", l) }
       l ~ /^=== release-check: .* ===$/ { in_v = (l == "=== release-check: govulncheck ===") ; next }
       in_v { print l }' "$1" > "$rgu_f" 2> /dev/null
  if [ -s "$rgu_f" ] && govuln_unreachable "$rgu_f"; then rm -f "$rgu_f"; return 0; fi
  rm -f "$rgu_f"; return 1
}
release_check() {
  if [ $(( $(date +%s) + RC_EST_S )) -gt "$deadline" ]; then
    log "step release-check SKIPPED: it needs about $((RC_EST_S / 60)) min (RC_EST_S) and would end after the deadline $DL"
    tsv release-check 1 - SKIPPED - - - -; count skip release-check; return 0
  fi
  # On battery, wait for AC until its latest start (header), within the night's budget, then run.
  rc_latest=$((deadline - RC_EST_S))
  case $(power_read) in
    "AC "*) ;;
    *) log "release-check: not on AC; waiting for AC until its latest start $(date -d "@$rc_latest" +%FT%T) (the deadline minus RC_EST_S), within the night's wait budget"
       power_wait_ac waited "$AC_WAIT_BUDGET_MIN" "$rc_latest" log ||
         log "release-check: no AC by its latest start or within the budget; it runs now, and its AC-sensitive windows say what the run is worth" ;;
  esac
  if past_deadline || [ $(( $(date +%s) + RC_EST_S )) -gt "$deadline" ]; then
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
    rc_lim=$((deadline + RC_GRACE_S - rc_t0))   # the watchdog (header): stopped at the deadline plus RC_GRACE_S
    log "step release-check try $rc_try start power=$rc_p0 (stopped if still running at $(date -d "@$((deadline + RC_GRACE_S))" +%FT%T), the deadline plus RC_GRACE_S)"
    fg sh "$here/recrun.sh" "$RCT/repo" "$E" p3-release-check-tag -- timeout -k 60 "$rc_lim" sh "$here/stamped.sh" \
      go run ./tools/devtool release-check --tag v0.3.0 --evidence-copy "$(winpath "$E")/release-check.json" \
      >> "$E/chain.log" 2>&1
    rc_rc=$?
    rc_t1=$(date +%s); rc_p1=$(power_read)
    if [ "$rc_rc" -eq 124 ]; then   # the watchdog stopped it: neither a pass nor a fail, never retried
      rm -f "$rc_sf"
      record release-check "$rc_try" "$rc_rc" SKIPPED-OVERRUN "$rc_p0" "$rc_p1" "$rc_t0" "$rc_t1" "still running at the deadline $DL plus RC_GRACE_S ($((RC_GRACE_S / 60)) min), so it was stopped: neither a pass nor a fail, not retried; its log ends where it stopped, and RC_EST_S ($((RC_EST_S / 60)) min) is too small for this tree"
      count skip release-check; break
    fi
    if rc_ev=$(power_events_since "$rc_t0"); then rc_ok=1; else rc_ok=0; rc_ev=""; fi
    rc_wf=$(mktemp)
    rc_windows "$E/p3-release-check-tag.log" "$rc_t1" > "$rc_wf" 2> /dev/null
    while read -r rc_w; do log "release-check try $rc_try $rc_w"; done < "$rc_wf"
    rc_v=$(rc_verdict "$rc_p0" "$rc_ok" "$(power_events_window "$rc_t0" "$rc_t1" "$rc_ev")" "$rc_wf" "$rc_rc" \
      "$E/p3-release-check-tag.log" "$rc_p1"); rm -f "$rc_wf"
    rc_note="events during the run: [$(power_events_window "$rc_t0" "$rc_t1" "$rc_ev" | tr '\n' ' ' | sed 's/ *$//')]"
    case $rc_v in
      INVALID-POWER*|NOT-REFERENCE*:on-battery*)
        if [ "$rc_try" -lt 2 ]; then
          # A green try 1 ran the whole release-check, so its length is the estimate; a red one stopped
          # at its first FAIL, so a green try 2 may need the full RC_EST_S.
          rc_len=$((rc_t1 - rc_t0))
          if [ "$rc_rc" -eq 0 ]; then rc_retry_blocker "$rc_len" "try 1's length"
          elif [ "$rc_len" -ge "$RC_EST_S" ]; then rc_retry_blocker "$rc_len" "try 1's length, over RC_EST_S"
          else rc_retry_blocker "$RC_EST_S" "RC_EST_S: try 1 failed, so its length is no estimate"
          fi
          if [ -z "$rc_why" ]; then
            rc_d=$(move_aside release-check "$rc_try" "$rc_sf"); rm -f "$rc_sf"
            record release-check "$rc_try" "$rc_rc" "$rc_v" "$rc_p0" "$rc_p1" "$rc_t0" "$rc_t1" "$rc_note; neither a pass nor a fail; its records moved to $rc_d; retried once in a fresh clone"
            cleanup
            if ! rc_new_clone; then log "step release-check exit=2: no fresh clone for try 2"; count fail release-check; cleanup; return 0; fi
            rc_try=2; continue
          fi
          rc_note="$rc_note; not retried: $rc_why"
        fi
        case $rc_v in INVALID-POWER*) count invalid release-check ;; *) count nref release-check "$rc_rc" ;; esac ;;
      VALID*)
        if [ "$rc_rc" -eq 0 ]; then count pass release-check
        elif rc_govuln_unreachable "$E/p3-release-check-tag.log"; then
          count nref release-check "$rc_rc"
          rc_note="$rc_note; govulncheck could not reach its database or the module proxy (network), not a product red: release-check stopped there, so the steps after it never ran (neither a pass nor a fail; hosted ci.yml's security job checks the pushed candidate)"
          log "release-check: govulncheck unreachable (network), not a product red"
        else count fail release-check; fi ;;
      *) count nref release-check "$rc_rc" ;;
    esac
    case $rc_v in
      VALID*) ;;
      *) if [ "$rc_rc" -ne 0 ] && rc_govuln_unreachable "$E/p3-release-check-tag.log"; then
           rc_note="$rc_note; its red is govulncheck's unreachable database or module proxy (network), not a product red"
         fi ;;
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

# ---- C5.2 chunks in candidate 8's night (header, "Two nights") -----------------------------------
# c52_night1: after release-check, the full list's chunks in order, each only on AC at that moment
# and only when its estimate ends by the deadline. Sets c52_left to the chunks still owed to the C5.2
# night. A chunk that is not done counts neither for nor against this night, except a VALID red.
c52_night1() {
  c52_left=""
  if [ "${C8_NIGHT1_C52:-1}" = 0 ]; then
    log "c52 in this night: off (C8_NIGHT1_C52=0); every chunk is the C5.2 night's"
    c52_left=$C52_CHUNKS; return 0
  fi
  if [ -z "$(c52_list 1)" ]; then   # the chunks are made from the list; the C5.2 night refuses without it
    log "c52 in this night: cannot read quiet.sh's benches() list ($here/quiet.sh), so no chunk runs; every chunk is the C5.2 night's"
    c52_left=$C52_CHUNKS; return 0
  fi
  C52_STATE=full; C52_FILTER=""
  log "c52 in this night (D65(b)): each chunk of the full list, in order, runs now if the machine is on AC and its estimate ends by $DL; the rest are the C5.2 night's"
  for cn_s in $C52_CHUNKS; do
    cn_pk=$(c52_group_pkgs "${cn_s##*-}")
    if [ -z "$cn_pk" ]; then log "c52: $cn_s: quiet.sh's list has no package in its group, so it has nothing to measure"; continue; fi
    set_c52pk "$cn_s" "$cn_pk"
    cn_e=$(gated_est "$cn_s")
    if past_deadline || [ $(( $(date +%s) + cn_e )) -gt "$deadline" ]; then
      log "c52: $cn_s needs about $(( (cn_e + 59) / 60 )) min (C52_EST_*) and would end after the deadline $DL: left for the C5.2 night"
      c52_left="$c52_left $cn_s"; continue
    fi
    cn_p=$(power_read)
    case $cn_p in
      "AC "*) ;;
      *) log "c52: $cn_s: not on AC ($cn_p), and a chunk never runs on battery (D65(c)): left for the C5.2 night"
         c52_left="$c52_left $cn_s"; continue ;;
    esac
    log "c52: $cn_s measures QUIET_PKGS='$cn_pk' QUIET_BENCH_FILTER='' into quiet-$cn_s/, about $(( (cn_e + 59) / 60 )) min"
    cn_pass=$n_pass; cn_fail=$n_fail; cn_inv=$n_inv; cn_nref=$n_nref; cn_skip=$n_skip; cn_nred=$n_nref_red
    while :; do
      cn_t=$(tries_of "$cn_s")
      run_gated "$cn_s" now && break                 # its final verdict
      [ "$(tries_of "$cn_s")" != "$cn_t" ] && continue # INVALID-POWER try 1: retried at once, if on AC
      break                                          # no AC for its retry
    done
    if [ "$n_pass" -gt "$cn_pass" ]; then
      log "c52: $cn_s done (VALID, exit 0)"
    elif [ "$n_fail" -gt "$cn_fail" ]; then
      log "c52: $cn_s is a VALID red: classify it in the ledger before the C5.2 night measures it again"
      c52_left="$c52_left $cn_s"
    else   # not measured as a reference: it is owed, not counted
      n_inv=$cn_inv; n_nref=$cn_nref; n_skip=$cn_skip; n_nref_red=$cn_nred
      log "c52: $cn_s was not measured VALID (power.tsv has its tries): left for the C5.2 night, counted neither way"
      c52_left="$c52_left $cn_s"
    fi
  done
  c52_left=${c52_left# }
}

# ---- the night -----------------------------------------------------------------------------------
if c52only; then plan="mode=c52-only c52_set=$C52_SET steps=[$C52_SEL]"
else plan="release-check must start by $(date -d "@$((deadline - RC_EST_S))" +%FT%T) (RC_EST_S)"; fi
log "start candidate=$H pid $$ winpid $(cat "/proc/$$/winpid" 2> /dev/null || echo '?') deadline=$DL${NIGHT_DEADLINE_EPOCH:+ (from c8-night.sh)} $plan ac_wait_budget=${AC_WAIT_BUDGET_MIN}min used=${waited}min prev_candidate=$PREV_CANDIDATE go=$(cd "$C" && go env GOVERSION 2> /dev/null) power=$(power_read)"
log "$d_msgs"
docker_answers && engine_up_at_start=1      # three probes, as everywhere (header, "Docker")
# Docker Desktop's status is read only for an engine that is not answering: an up engine is left
# wholly alone.
log "engine up at start=$engine_up_at_start (three probes$([ "$engine_up_at_start" = 1 ] || echo "; Docker Desktop status: $(docker_desktop_state)")); an engine is stopped only when it was down now, Docker Desktop said stopped, this chain started it, and nothing else runs on it"
timeout -k 10 60 docker ps --format "{{.Names}} {{.Status}}" >> "$E/chain.log" 2>&1
timeout -k 10 120 docker stop qompack-v6-linux-verification > /dev/null 2>&1

pending=""
if c52only; then
  log "C8_C52_ONLY=1: a C5.2 night (D62(c)): only these steps run, in this order: $C52_SEL$([ "$C52_SET" = derived ] && echo ' (the chunks after c52-derive)')"
  # The rig goes first, so C1.16's re-run never waits behind C5.2's 8 h. Without AC it stays
  # queued (in derived mode while the derivation, which needs none, runs).
  queue=""; sel c116-rig && queue="c116-rig"
  run_queue now
  c52_any=0; for s in $C52_CHUNKS; do sel "$s" && c52_any=1; done
  if [ "$c52_any" = 1 ]; then c52_set; c52_queue; fi
  run_queue wait
else
  queue="win-timing win-e2e-timing win-x11-alone"
  run_queue now
  step win-race env GOFLAGS=-p=4 sh "$here/phase3.sh" "$C" "$E" win-race
  step bundles sh "$here/phase3.sh" "$C" "$E" bundles
  run_queue now
  linux_lanes
  queue="$queue c51-win c51-linux"
  run_queue now
  release_check   # C3.12 and D57(a): a release gate
  run_queue wait
  c52_night1
  pending="c116-rig${c52_left:+ $c52_left}"
  log "left for the C5.2 night (D62(c); C8_C52_ONLY=1 on this candidate, README.md \"The C5.2 night\", with C8_C52_STEPS naming these): $pending"
fi

total=$((n_pass + n_fail + n_inv + n_nref + n_skip + n_rep))
outcome="steps=$total passed=$n_pass failed=$n_fail${failed:+ [${failed# }]} invalid_power=$n_inv not_reference=$n_nref (of which $n_nref_red exited non-zero) skipped=$n_skip reported=$n_rep ac_wait_used=${waited}min${pending:+ pending_c52_night=[$pending]}"
echo "$outcome" > "$E/overnight-outcome.txt"
log "done: $outcome"
[ $((n_fail + n_inv + n_nref + n_skip)) -eq 0 ]
