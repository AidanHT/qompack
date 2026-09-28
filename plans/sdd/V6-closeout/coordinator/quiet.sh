#!/bin/sh
# quiet.sh <candidate-repo> <base-rev> <evidence-dir> <step...>
# Phase 5's quiet benchmark runs (C5.1, C5.2), after Phase 3, on an otherwise idle host. Strictly
# sequential; nothing declares co-load (QOMPACK_UNDER_COLOAD is unset for every run). Stop every
# other workstream first, hold a keep-awake (keepawake.ps1) for the whole run, and never run two
# copies (the evidence directory holds a lock). Steps, run in the order given:
#   c51-win     C5.1: devtool bench-hotpath --iterations 5000 --hook observe-tool --warm-daemon
#               --json (nightly bench-deep's command; p50/p95/p99/p999/max per row), then
#               TestBudgetBF -v alone (ci.yml's timing lane's B-F row: n/p50/p95/p99/max over 200
#               MCP calls on the 40 MB corpus), both in a clean clone of the candidate
#   c51-linux   C5.1 on Linux: the same harness, non-root, no -race, in a gate-prepared clone, and
#               TestBudgetBF through the gate itself (--no-race, no --coload)
#   c52-win     C5.2: the carried benchmarks plus every one D37 lists, candidate vs <base-rev>
#   c52-linux   C5.2 on Linux: the same list and rounds, non-root, no -race, gate-prepared clones
# C5.1's "B-A..B-F": the harness measures B-A, B-B, B-D (reported), B-E and B-E_cpu; it says itself
# "B-C not measured". B-F comes from TestBudgetBF above, and B-C (l0_process, soft) from C5.2's
# observer OnToolUse rows (perfobs: SP08-D1 is B-C), which carry a p99-ms metric.
#
# <base-rev> is the pre-Phase-2 base the perf reports compared against: cf31e01, verify/v6 when the
# close-out opened (perfstore/report.md "base cf31e01"; perfobs/report.md "cut from verify/v6 @
# cf31e01"; both runs/INDEX.txt). w6-ckptsync's timing base 74bdcd7 is a scratch commit no branch
# holds, so it cannot be the base.
#
# Recording. Windows runs go through recrun.sh (<id>.json + <id>.log). The Linux gate
# (plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh, the candidate's copy) only runs `go test -json`
# and has no bench or `go run` option. So each Linux step first makes one gate run per revision:
# --no-race, no --coload, --run '^$' over the step's packages. That run bundles the exact commit,
# clones it fresh, checks HEAD and a clean tree, writes the non-root user's env.sh, compiles every
# package, and brings identity.txt home. The measured commands then run in that clone, as that
# user, under that env.sh and nothing else (docker exec; recorded host-side by recrun.sh), and the
# clone is checked clean again at the end. This keeps -benchmem and -benchtime and runs one package
# at a time. w6-ckptsync's GOFLAGS=-bench route lost both flags and ran packages in parallel.
#
# C5.2 method (as perfstore, perfobs and w6-ckptsync): each revision's test binaries are built once
# per package, then run alternately in ABBA rounds (odd rounds base first, even rounds candidate
# first), with a package's two sides back to back and one -count=PERCALL sample each. Samples per
# side = ROUNDS x PERCALL = C5.2's -count 5. Outputs per OS: base.txt and candidate.txt (raw -bench
# text), benchstat from the repo's pinned tool (tools/pinned/go.mod), and paired.txt (per-round
# candidate/base ratio, geomean, exact sign test). c52-names.tsv records every listed benchmark's
# presence on both revisions: one missing on a side is recorded there and not run on that side.
#
# Real home. Every package measured isolates its home (pathstest.Main) or uses b.TempDir(), and the
# hot-path harness makes its own temp HOME/USERPROFILE. As a check, homeguard.py (beside this file)
# snaps ~/.claude and ~/.qompack (hashes and names only) before the first step and checks them after
# the last one; a difference is a WARNING in quiet-run.txt; the check output lands in the evidence
# directory, and the snap (it lists installed plugins) stays in work.
#
# Budget overrides, for dry runs only (the defaults are the checklist's):
#   QUIET_ITERATIONS (5000)   C5.1 --iterations
#   QUIET_ROUNDS (5)          ABBA rounds
#   QUIET_PERCALL (1)         -test.count per call
#   QUIET_BENCHTIME           replaces every row's -test.benchtime
#   QUIET_PKGS                space-separated package names C5.2 is limited to (e.g. "store")
#   QUIET_BENCH_FILTER        ERE a benchmark name must match to run (e.g. '^BenchmarkGetChunk$')
#   QUIET_WORK                scratch directory for host clones and binaries (default: mktemp -d)
#   QUIET_CONTAINER (qompack-v6-linux-verification), QUIET_LXUSER (qompack-test)
set -u
if [ $# -lt 4 ]; then sed -n '2,/^set -u$/p' "$0" | sed '$d' >&2; exit 2; fi
repo=$1; base=$2; ev=$3; shift 3
here=$(cd "$(dirname "$0")" && pwd)
unset QOMPACK_UNDER_COLOAD
# Container paths must reach docker untouched, so Git Bash path conversion is off and every host path
# below is put in Windows form first (as the gate does).
MSYS_NO_PATHCONV=1; export MSYS_NO_PATHCONV
# Evidence paths in the clones pass MAX_PATH, and recrun.sh's own `git status` must read them, so
# core.longpaths is given to every git command through the environment; no git config is written.
GIT_CONFIG_COUNT=1; GIT_CONFIG_KEY_0=core.longpaths; GIT_CONFIG_VALUE_0=true
export GIT_CONFIG_COUNT GIT_CONFIG_KEY_0 GIT_CONFIG_VALUE_0
winpath() { if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi; }
mkdir -p "$ev" || exit 2
wrepo=$(winpath "$(cd "$repo" && pwd)"); wev=$(winpath "$(cd "$ev" && pwd)")
cand=$(git -C "$wrepo" rev-parse --verify 'HEAD^{commit}') || exit 2
base_sha=$(git -C "$wrepo" rev-parse --verify "$base^{commit}") || { echo "unknown base revision $base" >&2; exit 2; }
mkdir "$wev/.quiet.lock" 2>/dev/null || { echo "refusing: $wev/.quiet.lock exists (another quiet.sh?)" >&2; exit 2; }
trap 'rmdir "$wev/.quiet.lock" 2>/dev/null' EXIT
work=$(winpath "${QUIET_WORK:-$(mktemp -d)}"); mkdir -p "$work" || exit 2
rm -f "$work/units" "$work/rows"  # the benchmark plan is per invocation (a reused QUIET_WORK)
ITER=${QUIET_ITERATIONS:-5000}; ROUNDS=${QUIET_ROUNDS:-5}; PERCALL=${QUIET_PERCALL:-1}
ctr=${QUIET_CONTAINER:-qompack-v6-linux-verification}; lxuser=${QUIET_LXUSER:-qompack-test}
gate="$wrepo/plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh"
man="$wev/quiet-manifest.txt"; info="$wev/quiet-run.txt"
rec() { sh "$here/recrun.sh" "$@" < /dev/null; }
say() { echo "$*" | tee -a "$info"; }
landed() { echo "$*" >> "$man"; }

# The benchmark list: package, benchmark (without "Benchmark"), -benchtime, and the source naming it.
# The fixed-count rows keep the perf reports' benchtimes (perfobs §6), so B-C's p99-ms is taken over
# 200 events, not over whatever 1s allows.
benches() { cat <<'EOF'
store GetChunk 1s perfstore §4 (SP20-D2, C2.7); C5.2 "GetChunk"
store OpenSpan_4KB_of_4MB 1s perfstore §4 (SP20-D2 read)
store Search_1000Roots 1s perfstore §4 (SP20-D2); C5.2 "search"
store Search_1000Roots_DistinctChunks 1s perfstore §4 (SP20-D2)
store PutBytes_100KB_Cold 1s perfstore §4 (SP06-D2, C2.6); C5.2 "store"
store PutBytes_100KB_Warm 1s perfstore §4 (SP06-D2, C2.6); C5.2 "store"
store PutObject_NovelChunk 1s perfstore §4 (6f5f3ff, SP06-D2)
store CountFold_4MiB 1s D37 1.16.11 (phase-7 store family)
store GC_50kObjects 1s D37 1.16.11 (phase-7 store family)
store MarkEncoded_100 1s D37 1.16.11 (phase-7 store family)
store OpenStore_50kRoots 1s D37 1.16.11 (phase-7 store family)
store PutBytes_100KB_Cold_NoRedact 1s D37 1.16.11 (phase-7 store family)
store PutBytes_100KB_Warm_KeepRaw 1s D37 1.16.11 (phase-7 store family)
store PutBytes_100KB_Warm_NoRedact 1s D37 1.16.11 (phase-7 store family)
observer OnToolUse_TestOutput256KB 200x perfobs §2/§6 (SP08-D1 B-C, C2.3; D20); C5.2 "OnToolUse 256 KB"
observer OnToolUse_TestOutput256KB_Leased 200x inventory 1.8.13 (B-C, C5.2-mapped; perfobs's 200x)
observer OnToolUse_FileRead64KB 1s perfobs §8.1 (B-C's 64 KB payload class); inventory 1.8.13
observer Tombstone 1s D37 1.8.2
checkpoint Finalize 10x perfobs §3/§6 (SP10-D1, C2.4); w6-ckptsync (D26); D37 1.10.17; C5.2 "Finalize"
checkpoint AdvanceSegment 1s D37 1.10.17
checkpoint ExtractDecisions 1s D37 1.10.17
checkpoint StripInjections 1s D37 1.16.11 (phase-7 checkpoint family)
checkpoint Truncate 1s D37 1.16.11 (phase-7 checkpoint family)
negknow Open 10x perfobs §4/§6 (SP09-D1, C2.5); C5.2 "negknow Open"
negknow Record 1s w6-ckptsync Linux paired timing (control)
negknow IngestMCPRealStore 1s w6-ckptsync Linux paired timing (D26 record_eliminated)
daemon IngestAccept 1s w6-ckptsync Linux paired timing (control)
daemon IngestAcceptLeased 1s w6-ckptsync Linux paired timing (control)
daemon IngestAcceptExternalized 1s w6-ckptsync Linux paired timing (D26, 1 MiB payloads in B-B)
canon Run_Bash100KB 1s perfobs §2/§6 (canon BenchmarkRun_*); D37 1.4.14
canon Run_GoTest 1s perfobs §2/§6 (canon BenchmarkRun_*); D37 1.4.14
canon Run_KeepDeltas 1s perfobs §2/§6 (canon BenchmarkRun_*); D37 1.4.14
canon Restore_100KB 1s D37 1.4.14 (canon family)
chunk GearScan_1MiB 1s D37 1.4.14 (chunk family)
chunk RootHash_1000Chunks 1s D37 1.4.14 (chunk family)
chunk Split_100KB 1s D37 1.4.14 (chunk family)
chunk Split_1MiB 1s D37 1.4.14 (chunk family)
chunk SplitStream_4MiB 1s D37 1.4.14 (chunk family)
symbols Enclosing_100KB 1s D37 1.4.14 (symbols family)
symbols Extract_100KB 1s D37 1.4.14 (symbols family)
symbols References_100KB_50Names 1s D37 1.4.14 (symbols family)
obs Histogram_Observe 1s D37 1.1.16
config ConfigLoad_ColdNoFiles 1s D37 1.1.27
paths IsHome_AProjectBelowHome 1s D37 1.1.27 (config/paths/core/devtool family)
paths PathsWriteAtomic_4KB 1s D37 1.1.27 (config/paths/core/devtool family)
eval BeladyDetail_400Turns 1s D37 1.2.12 (E-1..E-5)
eval BreakpointOPT_256Candidates 1s D37 1.2.12 (E-1..E-5)
eval Compare_400Actions 1s D37 1.2.12 (E-1..E-5)
eval Synthesize_320Turns 1s D37 1.2.12 (E-1..E-5)
sketch RebuildBloom5000 1s D37 1.3.7
sketch L0SketchUpdate 1s inventory 1.3.16 (C5.2-mapped; 0-alloc hot-path sketch update)
dag CrossingEdges 1s D37 1.7.8
dag BackwardSlice5000 1s D37 1.7.10
hostperm Evaluate 1s D37 1.12.17 (inventory-map symbol)
scheduler Evaluate_64Candidates 1s D37 1.12.17 and 1.16.11 (scheduler family)
scheduler BOCDObserve_4Features 1s D37 1.12.17 and 1.16.11 (scheduler family)
scheduler BOCDMarshal 1s D37 1.12.17 and 1.16.11 (scheduler family)
rules PathScoped 1s D37 1.11.16 (rehydrate/rules/skills family)
skills Index 1s D37 1.11.16 (rehydrate/rules/skills family)
EOF
}
# D37 family directories; any with no Benchmark func at all is recorded as such in c52-names.tsv.
families="1.1.27:internal/core 1.1.27:tools/devtool 1.2.12:test/replay 1.10.17:internal/pins
1.11.16:internal/rehydrate 1.15.14:internal/analyzer 1.15.14:internal/grammar"

# bench_index <sha>: "<pkg> Benchmark<Name>" for every top-level benchmark under internal/ at <sha>.
bench_index() {
  [ -s "$work/bench-$1" ] || git -C "$wrepo" grep -o -E "^func Benchmark[A-Za-z0-9_]+\(" "$1" -- internal/ |
    sed -E 's#^[^:]*:internal/([^:]*)/[^/:]*:func (Benchmark[A-Za-z0-9_]+)\($#\1 \2#' | sort -u > "$work/bench-$1"
}
has() { bench_index "$1"; grep -qx "$2 Benchmark$3" "$work/bench-$1" && echo yes || echo no; }

# c52_plan writes c52-names.tsv (always the whole list) and, under $work, the selected rows and the
# (package, benchtime) units both sides run: "<pkg> <benchtime> <regex>".
c52_plan() {
  local names f n
  [ -s "$work/units" ] && return 0
  names="$wev/c52-names.tsv"
  printf 'package\tbenchmark\tbenchtime\tcandidate\tbase\tsource\n' > "$names"
  : > "$work/rows"
  benches | while read -r pkg name bt src; do
    c=$(has "$cand" "$pkg" "$name"); b=$(has "$base_sha" "$pkg" "$name")
    printf 'internal/%s\tBenchmark%s\t%s\t%s\t%s\t%s\n' "$pkg" "$name" "$bt" "$c" "$b" "$src" >> "$names"
    [ "$c" = no ] && echo "WARNING: Benchmark$name is not on the candidate ($src)" | tee -a "$info"
    [ "$c$b" = nono ] && continue
    if [ -n "${QUIET_PKGS:-}" ]; then case " $QUIET_PKGS " in *" $pkg "*) ;; *) continue ;; esac; fi
    if [ -n "${QUIET_BENCH_FILTER:-}" ]; then printf 'Benchmark%s\n' "$name" | grep -qE "$QUIET_BENCH_FILTER" || continue; fi
    echo "$pkg ${QUIET_BENCHTIME:-$bt} $name $c $b" >> "$work/rows"
  done
  for f in $families; do
    n=$(git -C "$wrepo" grep -c -E "^func Benchmark" "$cand" -- "${f#*:}/" 2>/dev/null | awk -F: '{s+=$NF} END {print s+0}')
    [ "$n" -eq 0 ] && printf '%s\t(no Benchmark func)\t-\tno\t-\tD37 %s: family directory with no benchmark\n' "${f#*:}" "${f%%:*}" >> "$names"
  done
  awk '{k=$1" "$2; if (!(k in re)) {o[++n]=k; re[k]=$3} else re[k]=re[k]"|"$3} END {for (i=1;i<=n;i++) print o[i]" ^Benchmark("re[o[i]]")$"}' "$work/rows" > "$work/units"
  landed "c52 benchmark presence, candidate vs base: $names"
  [ -s "$work/units" ] || { echo "no benchmark selected" >&2; return 1; }
}
# side_has <side> <pkg>: the side has at least one selected benchmark of the package.
side_has() { local col=4; [ "$1" = base ] && col=5; awk -v p="$2" -v c="$col" '$1==p && $c=="yes" {f=1} END {exit !f}' "$work/rows"; }
side_sha() { if [ "$1" = base ]; then echo "$base_sha"; else echo "$cand"; fi; }
order() { if [ $(($1 % 2)) -eq 1 ]; then echo "base candidate"; else echo "candidate base"; fi; }

# clone_side <side>: a fresh --shared clone of the side's exact commit under $work, so both sides are
# built the same way from committed trees; recrun.sh records its HEAD as the run's source head.
clone_side() {
  local cd_ sha
  cd_="$work/src-$1"; sha=$(side_sha "$1")
  if [ ! -d "$cd_" ]; then
    git clone --quiet --shared --no-checkout "$wrepo" "$cd_" &&
      git -C "$cd_" -c advice.detachedHead=false checkout --quiet --detach "$sha" || return 1
  fi
  [ "$(git -C "$cd_" rev-parse HEAD)" = "$sha" ] && [ -z "$(git -C "$cd_" status --porcelain)" ]
}

load() { # a load sample for the run record: Windows CPU % and the container's load average
  local w l
  w=$(powershell -NoProfile -Command "(Get-CimInstance Win32_Processor | Measure-Object LoadPercentage -Average).Average" 2>/dev/null | tr -d '\r')
  l=$(docker exec "$ctr" cat /proc/loadavg 2>/dev/null)
  say "load $1 $(date -u +%H:%M:%SZ): windows_cpu_pct=${w:-?} linux_loadavg=${l:-?}"
}

# lx_prepare <step-dir> <side> <label> <pkg...>: the per-revision gate run; writes <side>.runid.
lx_prepare() {
  local d side label rc rid
  d=$1; side=$2; label=$3; shift 3
  mkdir -p "$d/gate"
  sh "$gate" --container "$ctr" --prefix cx-quiet --repo "$wrepo" --out "$d/gate" "$(side_sha "$side")" "$label" \
    --no-race --run '^$' --timeout 30m -- "$@" > "$d/prep-$side.host.log" 2>&1 < /dev/null
  rc=$?
  rid=$(sed -n 's/^run_id=//p' "$d/prep-$side.host.log" | head -1)
  say "gate prep $label ($side $(side_sha "$side" | cut -c1-12)) exit=$rc run_id=${rid:-none}"
  landed "gate prep ($side): $d/prep-$side.host.log and $d/gate/$rid-artifacts"
  [ -n "$rid" ] || return 1
  echo "$rid" > "$d/$side.runid"
  return $rc
}
# lx_rec <step-dir> <side> <id> <container-dir> <cmd...>: one recorded run in the gate-prepared
# clone, as the gate's user, under the gate's env.sh only.
lx_rec() {
  local d side id dir rid
  d=$1; side=$2; id=$3; dir=$4; shift 4
  rid=$(cat "$d/$side.runid")
  rec "$work/src-$side" "$d" "$id" -- docker exec "$ctr" runuser -u "$lxuser" -- env -i \
    sh -c '. "$0"; cd "$1" || exit 2; shift; exec "$@"' "/work/$rid-artifacts/env.sh" "$dir" "$@"
}
lx_after() { # <step-dir> <side>: the clone must still be clean; binaries are hashed
  local d rid
  d=$1; rid=$(cat "$d/$2.runid")
  docker exec "$ctr" git -c "safe.directory=/work/$rid" -C "/work/$rid" status --porcelain > "$d/source-status-after-$2.txt" 2>&1
  [ -s "$d/source-status-after-$2.txt" ] && say "WARNING: /work/$rid changed during the run"
  docker exec "$ctr" sh -c "cd /work/$rid-artifacts && sha256sum bin/* 2>/dev/null" > "$d/binaries-$2.sha256"
}

# c52_analyse <step-dir> <os>: base.txt/candidate.txt, paired.txt, and the pinned benchstat.
c52_analyse() {
  local d o
  d=$1; o=$2
  python - "$d" "$o" > "$d/paired.txt" <<'EOF'
import math, os, re, statistics, sys
d, osn = sys.argv[1], sys.argv[2]
pat = re.compile(r'^c52-%s-r(\d+)-(base|candidate)-(.+)\.log$' % osn)
runs = sorted((int(m.group(1)), m.group(3), m.group(2), f) for f in os.listdir(d) for m in [pat.match(f)] if m)
line = re.compile(r'^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+(.*\S)\s*$')
num = re.compile(r'^[0-9.]+(?:e[+-]?\d+)?$')
data = {}  # bench -> side -> round -> metric -> [values]
for side in ('base', 'candidate'):
    with open(os.path.join(d, side + '.txt'), 'w', encoding='utf-8', newline='\n') as out:
        for r, _, s, f in runs:
            if s != side:
                continue
            txt = open(os.path.join(d, f), encoding='utf-8', errors='replace').read()
            out.write(txt if txt.endswith('\n') else txt + '\n')
            for l in txt.splitlines():
                m = line.match(l)
                if not m:
                    continue
                t = m.group(2).split()
                for i in range(0, len(t) - 1, 2):
                    if num.match(t[i]):
                        data.setdefault(m.group(1), {}).setdefault(side, {}).setdefault(r, {}).setdefault(t[i + 1], []).append(float(t[i]))
def med(side, metric):
    v = [x for rd in side.values() for x in rd.get(metric, [])]
    return statistics.median(v) if v else None
def fmt(ns):
    if ns is None: return '-'
    for u, s in (('s', 1e9), ('ms', 1e6), ('us', 1e3)):
        if ns >= s: return '%.4g %s' % (ns / s, u)
    return '%.4g ns' % ns
def signp(k, n):
    if n == 0: return float('nan')
    t = sum(math.comb(n, i) for i in range(0, min(k, n - k) + 1)) / 2 ** n
    return min(1.0, 2 * t)
print('C5.2 paired comparison (%s): per round, candidate/base of the median ns/op within the call.' % osn)
print('geomean < 1 means the candidate is faster; "faster k/n" counts rounds; p is the exact two-sided sign test.')
print('%-58s %5s %11s %11s %8s %7s %7s %10s %10s %9s %9s' % ('benchmark', 'pairs', 'base', 'candidate', 'geomean', 'faster', 'sign p', 'base allocs', 'cand allocs', 'base p99', 'cand p99'))
for b in sorted(data):
    sb, sc = data[b].get('base'), data[b].get('candidate')
    if not sb or not sc:
        print('%-58s absent on %s' % (b, 'base' if not sb else 'candidate'))
        continue
    rs = []
    for r in sorted(set(sb) & set(sc)):
        x, y = sb[r].get('ns/op'), sc[r].get('ns/op')
        if x and y:
            rs.append(statistics.median(y) / statistics.median(x))
    n = sum(1 for q in rs if q != 1.0); k = sum(1 for q in rs if q < 1.0)
    g = math.exp(sum(math.log(q) for q in rs) / len(rs)) if rs else float('nan')
    ab, ac = med(sb, 'allocs/op'), med(sc, 'allocs/op')
    pb, pc = med(sb, 'p99-ms'), med(sc, 'p99-ms')
    print('%-58s %5d %11s %11s %8.3f %7s %7.3f %10s %10s %9s %9s' % (b, len(rs), fmt(med(sb, 'ns/op')), fmt(med(sc, 'ns/op')), g,
          '%d/%d' % (k, n), signp(k, n), '-' if ab is None else '%g' % ab, '-' if ac is None else '%g' % ac,
          '-' if pb is None else '%g' % pb, '-' if pc is None else '%g' % pc))
EOF
  say "c52-$o paired analysis exit=$?"
  rec "$wrepo" "$d" "c52-$o-benchstat" -- go run -modfile=tools/pinned/go.mod golang.org/x/perf/cmd/benchstat \
    "base=$d/base.txt" "candidate=$d/candidate.txt"
  rec "$wrepo" "$d" "c52-$o-benchstat-csv" -- go run -modfile=tools/pinned/go.mod golang.org/x/perf/cmd/benchstat \
    -format csv "base=$d/base.txt" "candidate=$d/candidate.txt"
  landed "c52-$o raw samples: $d/base.txt $d/candidate.txt (per call: $d/c52-$o-r*-*.log + .json)"
  landed "c52-$o benchstat: $d/c52-$o-benchstat.log (csv: $d/c52-$o-benchstat-csv.log)"
  landed "c52-$o paired ABBA table: $d/paired.txt"
}

step_c51_win() {
  local rc rcf src
  src="$work/src-candidate"
  clone_side candidate || { say "c51-win: cannot clone the candidate"; return 1; }
  load before-c51-win
  rec "$src" "$wev" c51-win -- go run ./tools/devtool bench-hotpath --iterations "$ITER" --hook observe-tool \
    --warm-daemon --json "$wev/c51-win-hotpath.json"
  rc=$?; load after-c51-win
  landed "c51-win B-A/B-B/B-D/B-E: $wev/c51-win-hotpath.json (record $wev/c51-win.json, log $wev/c51-win.log)"
  rec "$src" "$wev" c51-win-bf -- go test -p 1 -count=1 -timeout=30m -run '^TestBudgetBF$' -v ./internal/mcp
  rcf=$?; load after-c51-win-bf
  landed "c51-win B-F: $wev/c51-win-bf.log (the \"B-F over\" line; record $wev/c51-win-bf.json)"
  [ $rc -eq 0 ] && return $rcf
  return $rc
}
step_c51_linux() {
  local d rid rc rcf bfrid
  d="$wev/c51-linux"; mkdir -p "$d"
  clone_side candidate || { say "c51-linux: cannot clone the candidate"; return 1; }
  lx_prepare "$d" candidate quiet-c51-prep ./test/bench/hotpath || { say "c51-linux: gate prep failed"; return 1; }
  rid=$(cat "$d/candidate.runid")
  load before-c51-linux
  lx_rec "$d" candidate c51-linux "/work/$rid" go run ./test/bench/hotpath --iterations "$ITER" --hook observe-tool \
    --warm-daemon --json "/work/$rid-artifacts/c51-linux-hotpath.json"
  rc=$?; load after-c51-linux
  docker cp "$ctr:/work/$rid-artifacts/c51-linux-hotpath.json" "$d/c51-linux-hotpath.json" || say "c51-linux: no json artifact"
  lx_after "$d" candidate
  landed "c51-linux B-A/B-B/B-D/B-E: $d/c51-linux-hotpath.json (record $d/c51-linux.json, log $d/c51-linux.log)"
  # B-F: TestBudgetBF through the gate itself, alone, non-root, --no-race, no --coload.
  mkdir -p "$d/bf"
  sh "$gate" --container "$ctr" --prefix cx-quiet --repo "$wrepo" --out "$d/bf" "$cand" quiet-c51-bf \
    --no-race --run '^TestBudgetBF$' --timeout 30m -- ./internal/mcp > "$d/bf.host.log" 2>&1 < /dev/null
  rcf=$?; load after-c51-linux-bf
  bfrid=$(sed -n 's/^run_id=//p' "$d/bf.host.log" | head -1)
  say "c51-linux B-F gate run exit=$rcf run_id=${bfrid:-none}"
  landed "c51-linux B-F: $d/bf/${bfrid:-?}-artifacts/test.jsonl (the \"B-F over\" output; host log $d/bf.host.log)"
  [ $rc -eq 0 ] && return $rcf
  return $rc
}
step_c52_win() {
  local d bin rc_s pkg side r bt re
  d="$wev/c52-win"; mkdir -p "$d"; bin="$work/bin-win"; rc_s=0
  c52_plan || return 1
  for side in base candidate; do clone_side "$side" || { say "c52-win: cannot clone $side"; return 1; }; done
  for pkg in $(cut -d' ' -f1 "$work/units" | sort -u); do
    for side in base candidate; do
      side_has "$side" "$pkg" || { say "c52-win: $side has none of internal/$pkg's benchmarks; not built"; continue; }
      mkdir -p "$bin/$side"
      rec "$work/src-$side" "$d" "c52-win-build-$side-$pkg" -- go test -c -o "$bin/$side/$pkg.test.exe" "./internal/$pkg" || rc_s=1
    done
  done
  (cd "$bin" && sha256sum */*.test.exe) > "$d/binaries.sha256" 2>&1
  load before-c52-win
  r=1
  while [ "$r" -le "$ROUNDS" ]; do
    while read -r pkg bt re; do
      for side in $(order "$r"); do
        side_has "$side" "$pkg" || continue
        [ -f "$bin/$side/$pkg.test.exe" ] || { say "c52-win: no $side binary for $pkg"; rc_s=1; continue; }
        rec "$work/src-$side/internal/$pkg" "$d" "c52-win-r$r-$side-$pkg-$bt" -- "$bin/$side/$pkg.test.exe" \
          -test.run '^$' -test.bench "$re" -test.benchtime "$bt" -test.benchmem -test.count "$PERCALL" -test.timeout 90m || rc_s=1
      done
    done < "$work/units"
    load "after-c52-win-round-$r"
    r=$((r + 1))
  done
  c52_analyse "$d" win
  return $rc_s
}
step_c52_linux() {
  local d rc_s pk pkg p side r bt re rid
  d="$wev/c52-linux"; mkdir -p "$d"; rc_s=0
  c52_plan || return 1
  for side in base candidate; do
    clone_side "$side" || { say "c52-linux: cannot clone $side"; return 1; }
    pk=''; for pkg in $(cut -d' ' -f1 "$work/units" | sort -u); do side_has "$side" "$pkg" && pk="$pk ./internal/$pkg"; done
    [ -n "$pk" ] || { say "c52-linux: $side has none of the selected benchmarks"; continue; }
    # shellcheck disable=SC2086
    lx_prepare "$d" "$side" "quiet-c52-prep-$side" $pk || { say "c52-linux: gate prep failed for $side"; rc_s=1; }
    [ -f "$d/$side.runid" ] || continue
    rid=$(cat "$d/$side.runid")
    for p in $pk; do
      pkg=${p#./internal/}
      lx_rec "$d" "$side" "c52-linux-build-$side-$pkg" "/work/$rid" go test -c -o "/work/$rid-artifacts/bin/$pkg.test" "$p" || rc_s=1
    done
  done
  load before-c52-linux
  r=1
  while [ "$r" -le "$ROUNDS" ]; do
    while read -r pkg bt re; do
      for side in $(order "$r"); do
        side_has "$side" "$pkg" && [ -f "$d/$side.runid" ] || continue
        rid=$(cat "$d/$side.runid")
        lx_rec "$d" "$side" "c52-linux-r$r-$side-$pkg-$bt" "/work/$rid/internal/$pkg" "/work/$rid-artifacts/bin/$pkg.test" \
          -test.run '^$' -test.bench "$re" -test.benchtime "$bt" -test.benchmem -test.count "$PERCALL" -test.timeout 90m || rc_s=1
      done
    done < "$work/units"
    load "after-c52-linux-round-$r"
    r=$((r + 1))
  done
  for side in base candidate; do [ -f "$d/$side.runid" ] && lx_after "$d" "$side"; done
  c52_analyse "$d" linux
  landed "c52-linux clone checks: $d/source-status-after-*.txt, binaries $d/binaries-*.sha256"
  return $rc_s
}

{
  echo "quiet.sh $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "candidate=$wrepo head=$cand"
  echo "base=$base resolved=$base_sha"
  echo "steps=$*"
  echo "iterations=$ITER rounds=$ROUNDS percall=$PERCALL benchtime=${QUIET_BENCHTIME:-per row} pkgs=${QUIET_PKGS:-all} filter=${QUIET_BENCH_FILTER:-none}"
  echo "QOMPACK_UNDER_COLOAD unset; work=$work container=$ctr user=$lxuser"
  go version; nproc 2>/dev/null
} >> "$info"
landed "run record: $info"
guard="$work/homeguard-$(date -u +%Y%m%dT%H%M%SZ).json"
python "$(winpath "$here")/homeguard.py" snap "$guard" > /dev/null || { echo "homeguard snap failed" >&2; exit 2; }
rc_all=0
for step in "$@"; do
  case $step in
    c51-win) step_c51_win ;;
    c51-linux) step_c51_linux ;;
    c52-win) step_c52_win ;;
    c52-linux) step_c52_linux ;;
    *) echo "unknown step $step" >&2; exit 2 ;;
  esac
  rc=$?; say "step $step exit=$rc"; [ $rc -ne 0 ] && rc_all=1
done
# A difference is reported, not failed: the owner's own Claude Code (a marketplace auto-update, a
# settings change) can move ~/.claude during a long run; ~/.qompack entries lost would be real.
python "$(winpath "$here")/homeguard.py" check "$guard" > "$wev/homeguard-check.txt" 2>&1 ||
  say "WARNING: homeguard check reports a real-home difference; read $wev/homeguard-check.txt"
landed "real-home check (~/.claude, ~/.qompack): $wev/homeguard-check.txt"
landed "scratch clones and binaries (not evidence): $work"
echo "== artifacts"
cat "$man"
exit $rc_all
