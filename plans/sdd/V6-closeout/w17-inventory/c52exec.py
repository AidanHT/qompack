"""D57(e) proof: which product files each C5.2 benchmark executes, and whether any changed c5->c6.

D57(e) lets a candidate 5 C5.2 measurement count on candidates 6 and 7 when every file the benchmark
executes is byte-unchanged. This runs each benchmark the inventory's C5.2 rows rest on once
(-benchtime=1x: an execution trace, not a measurement), with a set-mode coverage profile over every
package of the module, and lists the product files with an executed statement. It then intersects
that set with the non-test .go files that differ between candidate 5 (0d06ab12) and candidate 6
(99d0b18c). Setup code counts as executed, which can only widen the set. For each executed changed file
it says whether the change is comment-only, and otherwise whether an executed block covers a changed
line (a type or struct change has no executable line, so "none" there does not prove the code equal).
Go never instruments _test.go files, so this half sees non-test files only; the _test.go files each
benchmark's test binary executes are c52tests.py's half (runs/c52-test-files.txt).

Run from the repository root on a tree whose Go code equals candidate 6's (candidate 7's code
differs only in core.Version's default literal, a file with no executable statement):
    python plans/sdd/V6-closeout/w17-inventory/c52exec.py > plans/sdd/V6-closeout/w17-inventory/runs/c52-executed-files.txt
"""
import collections, os, re, subprocess, sys, tempfile

C5, C6 = "0d06ab12", "99d0b18c"
MOD = "github.com/qompack/qompack/"
# package -> benchmark regex, the benchmarks of phase3/c5/quiet/c52-names.tsv that a C5.2 row rests on
BENCH = [
    ("internal/obs", "^BenchmarkHistogram_Observe$", "1.1.16"),
    ("internal/config", "^BenchmarkConfigLoad_ColdNoFiles$", "1.1.27"),
    ("internal/paths", "^(BenchmarkIsHome_AProjectBelowHome|BenchmarkPathsWriteAtomic_4KB)$", "1.1.27"),
    ("internal/cli", "^BenchmarkHookNoop_InProcess$", "1.1.27"),
    ("internal/eval", "^(BenchmarkBeladyDetail_400Turns|BenchmarkBreakpointOPT_256Candidates|BenchmarkCompare_400Actions|BenchmarkSynthesize_320Turns)$", "1.2.12"),
    ("internal/sketch", "^(BenchmarkRebuildBloom5000|BenchmarkL0SketchUpdate)$", "1.3.7, 1.3.16"),
    ("internal/chunk", "^(BenchmarkGearScan_1MiB|BenchmarkRootHash_1000Chunks|BenchmarkSplit_100KB|BenchmarkSplit_1MiB|BenchmarkSplitStream_4MiB)$", "1.4.14"),
    ("internal/canon", "^(BenchmarkRun_Bash100KB|BenchmarkRun_GoTest|BenchmarkRun_KeepDeltas|BenchmarkRestore_100KB)$", "1.4.14"),
    ("internal/symbols", "^(BenchmarkEnclosing_100KB|BenchmarkExtract_100KB|BenchmarkReferences_100KB_50Names)$", "1.4.14"),
    ("internal/store", "^Benchmark(GetChunk|OpenSpan_4KB_of_4MB|Search_1000Roots|Search_1000Roots_DistinctChunks|PutBytes_100KB_Cold|PutBytes_100KB_Warm|PutObject_NovelChunk|CountFold_4MiB|GC_50kObjects|MarkEncoded_100|OpenStore_50kRoots|PutBytes_100KB_Cold_NoRedact|PutBytes_100KB_Warm_KeepRaw|PutBytes_100KB_Warm_NoRedact)$", "1.6.19, 1.16.11"),
    ("internal/dag", "^(BenchmarkCrossingEdges|BenchmarkBackwardSlice5000)$", "1.7.8, 1.7.10"),
    ("internal/observer", "^BenchmarkTombstone$", "1.8.2"),
    ("internal/observer", "^(BenchmarkOnToolUse_TestOutput256KB|BenchmarkOnToolUse_TestOutput256KB_Leased|BenchmarkOnToolUse_FileRead64KB)$", "1.8.13"),
    ("internal/negknow", "^BenchmarkOpen$", "1.9.12"),
    ("internal/checkpoint", "^(BenchmarkFinalize|BenchmarkAdvanceSegment|BenchmarkExtractDecisions|BenchmarkStripInjections|BenchmarkTruncate)$", "1.10.17, 1.16.11"),
    ("internal/rules", "^BenchmarkPathScoped$", "1.11.16"),
    ("internal/skills", "^BenchmarkIndex$", "1.11.16"),
    ("internal/scheduler", "^(BenchmarkEvaluate_64Candidates|BenchmarkBOCDObserve_4Features|BenchmarkBOCDMarshal)$", "1.12.17, 1.16.11"),
    ("internal/daemon", "^(BenchmarkFeaturesFrom|BenchmarkReclaimableIndexBuild_5000Blocks|BenchmarkAssembleCandidates_2000ToolUses|BenchmarkRuntimeEvaluate_2000ToolUses_32Candidates|BenchmarkSchedulerTap_ObserveTool)$", "1.12.17, 1.16.11"),
]


def git(*a):
    return subprocess.run(["git", *a], capture_output=True, text=True, check=True).stdout


changed = set(git("diff", "--name-only", C5, C6, "--", "*.go", ":(exclude)*_test.go").split())


def change_kind(f):
    """comment-only when every added or removed line is blank or a // comment; else code, with the
    candidate 6 line numbers of the added lines (an executed block over one runs changed code)."""
    d = git("diff", "-U0", C5, C6, "--", f)
    lines, code = set(), False
    for h in re.finditer(r"^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@", d, re.M):
        start, n = int(h.group(1)), int(h.group(2) or "1")
        lines.update(range(start, start + n))
    for ln in d.splitlines():
        if ln[:1] in "+-" and not ln.startswith(("+++", "---")):
            t = ln[1:].strip()
            if t and not t.startswith("//"):
                code = True
    return ("code" if code else "comment-only"), lines


KIND = {f: change_kind(f) for f in changed}
print(f"# D57(e) executed-files proof. Tree: {git('rev-parse', 'HEAD').strip()} "
      f"(Go code equal to candidate 6 {C6} except core.Version's literal).")
print(f"# Non-test .go files changed {C5} -> {C6}: {len(changed)}.")
print("# For each benchmark set: the command, the number of product files with an executed statement,")
print("# and which of them changed c5 -> c6 (none means D57(e) holds for that set).\n")
tmp = tempfile.mkdtemp(prefix="c52exec-")
bad = 0
comment_only = 0
for i, (pkg, rx, rows) in enumerate(BENCH):
    prof = os.path.join(tmp, f"p{i}.out")
    cmd = ["go", "test", "-p", "2", "-count=1", "-run", "^$", "-bench", rx, "-benchtime=1x",
           "-covermode=set", f"-coverpkg={MOD}...", f"-coverprofile={prof}", "./" + pkg]
    r = subprocess.run(cmd, capture_output=True, text=True)
    ran = re.findall(r"^(Benchmark\S+)", r.stdout, re.M)
    ex, blocks = set(), collections.defaultdict(list)
    if r.returncode == 0 and os.path.exists(prof):
        for ln in open(prof, encoding="utf-8"):
            m = re.match(r"^(.+?):(\d+)\.\d+,(\d+)\.\d+ \d+ (\d+)$", ln.strip())
            if m and m.group(4) != "0":
                f = m.group(1)[len(MOD):] if m.group(1).startswith(MOD) else m.group(1)
                ex.add(f)
                blocks[f].append((int(m.group(2)), int(m.group(3))))
    hit = sorted(ex & changed)
    print(f"## rows {rows}: {pkg}")
    print("$ go test -p 2 -count=1 -run '^$' -bench '" + rx + "' -benchtime=1x -covermode=set "
          f"-coverpkg={MOD}... -coverprofile=<tmp> ./{pkg}")
    print(f"exit {r.returncode}; benchmarks run: {', '.join(sorted(set(b.split('-')[0] for b in ran))) or 'NONE'}")
    own = sorted(os.path.basename(f) for f in ex if os.path.dirname(f) == pkg)
    print(f"product files executed: {len(ex)}; in {pkg}: {', '.join(own) or 'none'}")
    print(f"changed c5->c6 among the executed files: {', '.join(hit) if hit else 'none'}")
    for f in hit:
        kind, lines = KIND[f]
        if kind == "comment-only":
            print(f"  {f}: comment-only change")
        else:
            over = sorted({l for a, b in blocks[f] for l in range(a, b + 1)} & lines)
            print(f"  {f}: code change; executed blocks over changed lines: "
                  + (", ".join(map(str, over)) if over else "none"))
    if r.returncode != 0 or not ran or hit:
        bad += 1
        if all(KIND[f][0] == "comment-only" for f in hit) and r.returncode == 0 and ran:
            comment_only += 1
        if r.returncode != 0:
            print("stderr/stdout tail:\n" + (r.stdout + r.stderr)[-1500:])
    print()
print(f"# sets whose executed changed files are all comment-only: {comment_only}")
print(f"# sets with an executed changed file, a failed run or no benchmark run: {bad}")
