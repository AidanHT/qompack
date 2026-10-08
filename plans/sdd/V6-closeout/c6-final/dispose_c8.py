"""C6.2 on candidate 8: append c8_result and c8_evidence to every inventory row, and write the c8 map.

Reads plans/sdd/V6-remediation/inventory-current.tsv (304 rows, eleven columns: the seven original
ones, then c6_result/c6_evidence and c7_result/c7_evidence) and plans/sdd/V6-closeout/inventory-map.tsv
(D37's evidence-step map, same order). Appends two columns, c8_result and c8_evidence, judged on
candidate 8's own artifacts (verify/v6 3ec62ad2, the release's code): the eleven existing columns are
kept byte for byte, which the script checks before it writes.

Candidate 8 ran every automated step on its own code (pre-freeze, the overnight chain, hosted ci.yml
and nightly, C5.1, all eight C5.2 chunks, release-check --tag), so no row rests on a carry from
candidate 7. The only carried parts are the live scenarios the live re-check did not repeat (D76,
live/rerun-c8/CARRIED.md); a row that needs one of those is partial_verified and its cell says so.
Each cell also says whether the row's source paths changed between candidate 7 (d20309c0) and
candidate 8 (3ec62ad2), from `git diff --name-only d20309c0 3ec62ad2`.

  python plans/sdd/V6-closeout/c6-final/dispose_c8.py          # dry run: counts
  python plans/sdd/V6-closeout/c6-final/dispose_c8.py --write  # write the TSV and inventory-c8-map.md
"""
import csv, io, os, re, subprocess, sys
from collections import Counter

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", "..", ".."))
INV = os.path.join(ROOT, "plans/sdd/V6-remediation/inventory-current.tsv")
MAP = os.path.join(ROOT, "plans/sdd/V6-closeout/inventory-map.tsv")
OUT_MAP = os.path.join(ROOT, "plans/sdd/V6-closeout/inventory-c8-map.md")

C7 = "d20309c0"
C8 = "3ec62ad2"
C8_FULL = "3ec62ad2e01b985640c0f1fb832df3917f766a5f"
HDR11 = ["id", "original_assertion_reference", "current_assertion", "current_test_symbols", "source_paths",
         "disposition", "limitation", "c6_result", "c6_evidence", "c7_result", "c7_evidence"]


def read(p):
    with open(p, encoding="utf-8", newline="") as f:
        text = f.read()
    return text, list(csv.reader(io.StringIO(text), delimiter="\t", quoting=csv.QUOTE_MINIMAL))


def dump(rows):
    buf = io.StringIO()
    w = csv.writer(buf, delimiter="\t", lineterminator="\n", quoting=csv.QUOTE_MINIMAL)
    for row in rows:
        w.writerow(row)
    return buf.getvalue()


inv_text, inv = read(INV)
_, mp = read(MAP)
assert inv[0][:11] == HDR11, inv[0]
assert len(inv) == 305 and len(mp) == 305
# The file must round-trip byte for byte through csv, or rewriting it could change the eleven
# existing columns; refuse rather than risk it.
assert dump(inv) == inv_text, "the TSV does not round-trip through csv; refusing to rewrite it"
assert inv[0][11:] in ([], ["c8_result", "c8_evidence"]), inv[0]

CHANGED = subprocess.check_output(["git", "-C", ROOT, "diff", "--name-only", C7, C8], text=True).split()


def changed_c7_c8(src):
    toks = [t.strip() for t in re.sub(r"\([^)]*\)", "", src).split(";") if t.strip()]
    hit = sorted({t for t in toks if any(f == t or f.startswith(t.rstrip("/") + "/") for f in CHANGED)})
    if hit:
        return "source paths changed c7->c8: " + ", ".join(hit)
    return "source paths unchanged c7->c8"


# ---- Evidence codes (defined in inventory-c8-map.md; paths relative to plans/sdd/V6-closeout/) ----
GREEN = {"win-tree": "W8", "linux-tree": "L8", "linux-e2e": "LE2E8", "linux-child": "CHILD8",
         "win-race": "WRACE8", "fuzz": "FUZZ8", "cover": "COVER8", "bundles": "BUNDLES8",
         "win-timing": "WTIME8", "linux-timing": "LTIME8", "C5.2": "C52-8", "C5.3": "REPLAY8",
         "meta": "FREEZE8"}


def gens_code(tok):
    if "replay" in tok: return "REPLAY8"
    if "plugin-validate" in tok: return "PV8"
    if "govulncheck" in tok: return "SEC8"
    if "build-all" in tok: return "XBUILD8"
    if "test-docs" in tok or "command-docs" in tok: return "DOCS8"
    return "GATE8+DOCS8"


# The live re-check on candidate 8's frozen bundle (D76, live/rerun-c8/): these scenarios ran and passed.
LIVE8 = {"C4.2", "C4.3", "C4.4", "C4.5", "C4.6", "C4.7", "C4.9", "C1.6", "C1.7", "UAT-02", "UAT-04", "UAT-05",
         "UAT-06", "UAT-07", "UAT-08", "UAT-11", "UAT-12"}
# C4.1 (install through the real marketplace flow) ran on the released bytes, which equal candidate 8's
# frozen bin/ (D80, REH8).
REH8 = {"C4.1"}
# Not repeated on candidate 8; carried from candidate 7's pass with a D53(f) note (CARRY-L7).
CARRIED_L7 = {"C4.8", "UAT-01", "UAT-03", "UAT-09", "UAT-10"}
UNKNOWN_LIVE = {"C4.11"}
LIVE_NOTE = {
    "C1.7": "C1.7's restore smoke ran on c8 (live/rerun-c8/C4.9/cli/, records r8 to r11); its post-new-write restore is carried from c7",
    "C4.9": "C4.9 leg (b) ran on c8 and leg (a) again through C1.6; leg (c) is carried",
    "UAT-05": "UAT-05 run 2 ran on c8; run 1 is carried",
    "UAT-12": "UAT-12 steps 1-5 and 8 ran on c8; its upgrade leg (steps 6, 7, 9, 10) is carried from c7",
}


def steps_of(cell):
    out = []
    for raw in [s.strip() for s in cell.split(";") if s.strip()]:
        base = raw.split(" ")[0]
        if base.startswith("C4.") or base.startswith("C1.") or base.startswith("UAT"):
            out.append(("live", raw))
        elif base == "gens":
            out.append(("green", gens_code(raw)))
        elif base == "lint":
            out.append(("green", "GATE8"))
        elif base == "release":
            out.append(("green", "REL8"))
        elif base == "C5.1":
            out.append(("c51", "C51-8"))
        elif base == "C5.5":
            out.append(("green", "C55-8"))
        elif base == "none":
            out.append(("none", raw))
        elif base in GREEN:
            out.append(("green", GREEN[base]))
        else:
            raise SystemExit(f"unmapped step {raw!r}")
    return out


def live_judge(raw):
    """(met, note) for one live step: met when a scenario it names ran on candidate 8 and passed."""
    names = re.findall(r"(C4\.\d+|C1\.\d|UAT-\d\d)", raw)
    if raw.startswith("UAT-01..UAT-12"):
        return False, "UAT-01..UAT-12 by a human: D3, no possible artifact"
    ran = [n for n in names if n in LIVE8]
    reh = [n for n in names if n in REH8]
    car = [n for n in names if n in CARRIED_L7]
    unk = [n for n in names if n in UNKNOWN_LIVE]
    parts = []
    if ran:
        parts.append("LIVE8 (" + ", ".join(ran) + ")")
    if reh:
        parts.append("REH8 (" + ", ".join(reh) + " on the released bytes)")
    for n in ran:
        if n in LIVE_NOTE:
            parts.append(LIVE_NOTE[n])
    if car:
        parts.append("not re-run on c8: " + ", ".join(car) + " (CARRY-L7)")
    if unk:
        parts.append(", ".join(unk) + ": unknown (D34(c))")
    return bool(ran or reh), "; ".join(parts)


O = {}


def o(i, result, note, codes=None):
    O[i] = (result, note, codes)


C52_BOTH = "measured on c8 on both OSes, ten ABBA rounds against cf31e01, every side complete (C52-8)"
LINUX_HOT = ("Open half: Linux B-A/B-B fail in the container, fsync-bound, not verified in target (D53(b)): "
             "LTIME8's TestIntegration_HotPathWarmWithRealResidentState and LE2E8's timing lane's "
             "TestV3_HotPathUnchangedWithLedgerResident (p99 180 ms and 106 ms against 15 ms, 592 and 590 "
             "deliveries deferred to the client spool, 0 lost; c51-linux is report-only)")
WIN_HOT = ("Windows half verified on the designated quiet C5.1 run on AC (D57(e)): C51-8 B-A p99 16.4 ms, B-B "
           "11.3 ms against 50 ms; the isolated hot-path row (WTIME8) and X11 alone (WE2ET8) pass on AC, every "
           "step VALID; hosted bench-gate on all three OSes green, report-only (Q1)")

o("1.1.1", "documented", "meta row: candidate 8 identity: verify/v6 " + C8_FULL + " (merge of closeout/integration "
  "e8c62191, tree 4f3deaf4), frozen bundle windows-amd64 BUNDLE.json sha256 61ba9c37...dcd8b, bin/qompack.exe "
  "sha256 93eb09f3...81a9 (phase3/c8-CANDIDATE.md); tagged v0.3.0 at 1a368a4b, whose bundle paths equal "
  "candidate 8's (c6-final/runs/c8-identity-proofs.txt); no test exists by design", codes="FREEZE8")
o("1.1.16", "verified_in_target", "obs BenchmarkHistogram_Observe " + C52_BOTH + ", reported (diagnostic)")
o("1.1.24", "verified_in_target", "test/guards green on c8 (W8, L8, and release-check's guards step, REL8); F-1's "
  "historical guard-rename expectation is retired, D23 ratified TestGuard_EveryProductReadIsClassified as a standing "
  "guard")
o("1.1.27", "verified_in_target", "config, paths and cli benches " + C52_BOTH + ", reported (diagnostic): "
  "BenchmarkHookNoop_InProcess 921.3 us on Windows and 502.7 us on Linux against its absolute 3 ms, PASS "
  "(D67(j)); ConfigLoad_ColdNoFiles is 1.037x the base on Linux, recorded and not acted on (D78(a)); "
  "internal/core and tools/devtool have no benchmark, so nothing there to measure")
o("1.1.28", "documented", "retired E-3: Qompack.md is v1.8 with its Revision log (v1.6 D5, v1.7 D36, v1.8 D41); "
  "no test by design", codes="")
o("1.2.8", "verified_in_target", "REPLAY8: hosted replay-gate on c8 green (the breakpoint OPT line is a "
  "diagnostic only)")
o("1.4.14", "verified_in_target", "chunk, canon and symbols benches " + C52_BOTH + ", reported; symbols "
  "Enclosing_100KB is 1.011x the base on Windows, recorded and not acted on (D75(b))")
o("1.5.12", "partial_verified", WIN_HOT + ". " + LINUX_HOT, codes="WTIME8+LTIME8+C51-8+WE2ET8")
o("1.6.19", "verified_in_target", "superseded guarantee: the store set " + C52_BOTH + ". "
  "PutBytes_100KB_Cold 12.69 ms (Windows) and 19.65 ms (Linux) against 3 ms, Warm 2.518 and 2.684 ms against "
  "400 us, over: SP06-D2 wontfix for 0.3.0 (D54), an accepted residual in the release notes' Known limits; "
  "GetChunk 48.67 and 20.01 us against 60 us, OpenSpan_4KB_of_4MB 47.85 and 20.79 us against 150 us, "
  "Search_1000Roots 9.668 and 6.603 ms against 25 ms, inside (SP20-D2 fixed)")
o("1.7.8", "verified_in_target", "TestCrossingEdges* green (W8, L8); BenchmarkCrossingEdges " + C52_BOTH +
  ", reported (diagnostic); it is 1.018x the base on Linux, recorded and not acted on (D78(a))")
o("1.8.2", "verified_in_target", "tombstone tests green (W8, L8); BenchmarkTombstone " + C52_BOTH +
  ": 272.2 ns on Windows, 294.8 ns on Linux, reported (diagnostic)")
o("1.8.13", "verified_in_target", "superseded guarantee: the three BenchmarkOnToolUse_* " + C52_BOTH +
  ". TestOutput256KB p99 on Windows: Delta 40.96 ms, AllNovel 122.9 ms; on Linux: Delta 73.73 ms, AllNovel "
  "94.2 ms; every fixture faster than the base, 10/10 rounds. The 256 KB fixtures exceed B-C's soft 50 ms: "
  "SP08-D1 wontfix for 0.3.0 (D54), after the hook's ACK, an accepted residual in the release notes' Known "
  "limits", codes="C52-8")
o("1.9.12", "verified_in_target", "all seven TestBudget_* pass in WTIME8 and LTIME8 on c8 (LTIME8's only red is "
  "the hot-path row); negknow BenchmarkOpen 59.66 ms (Windows) and 75.13 ms (Linux) against 300 ms, 1.16x and "
  "1.34x the base, recorded and not acted on (D75(b), D78(a)); SP09-D1 fixed")
o("1.10.16", "verified_in_target", "quiet B-E on the reference host: C51-8 Windows B-E p99 166.8 ms (B-E_cpu p99 "
  "46.9 ms) against 2000 ms, on AC; Linux quiet C5.1 on c8 is report-only and its quiet-c51-linux records are "
  "not committed (phase3/c8/chain.log names them)", codes="C51-8")
o("1.10.17", "verified_in_target", "checkpoint benches " + C52_BOTH + ": BenchmarkFinalize 53.01 ms on Windows "
  "and 46.6 ms on Linux (1.32x the base, D78(a)) against 50 ms, which is stated for Linux; the Windows residual "
  "is recorded (SP10-D1 fixed, D54)")
o("1.10.18", "unknown", "D37(c): no step runs the two replay --phase 4 runs with the frontier toggled; not verified "
  "in target", codes="")
o("1.11.16", "partial_verified", "rules BenchmarkPathScoped and skills BenchmarkIndex " + C52_BOTH + "; "
  "internal/rehydrate has no benchmark, so the rehydrate share of L5 latency has no artifact")
o("1.12.14", "partial_verified", "structural half TestSchedulerNotOnHotPath green (W8, L8); Windows B-A half "
  "verified on C51-8 (B-A p99 16.4 ms against 50, on AC). Open half: Linux B-A is not verified in target "
  "(D53(b))", codes="W8+L8+C51-8")
o("1.12.17", "verified_in_target", "superseded guarantee: internal/scheduler's three benches and the daemon's "
  "scheduler benches (AssembleCandidates_2000ToolUses, RuntimeEvaluate_2000ToolUses_32Candidates, "
  "SchedulerTap_ObserveTool, FeaturesFrom, ReclaimableIndexBuild_5000Blocks) " + C52_BOTH + ", reported; the "
  "map's internal/hostperm BenchmarkEvaluate is a name collision, measured candidate-only")
o("1.13.4", "verified_in_target", "test/security and the internal/mcp TestV6_* suites green on c8 (W8, L8); the "
  "real-session half ran on c8")
o("1.13.5", "verified_in_target", "automated half green on c8 (D7 deny-rule honouring, capture-scope suites); the "
  "real-session half ran on c8")
o("1.13.14", "verified_in_target", "producer and seam tests green (W8, L8); real mcp.server_registered: C4.4's "
  "session init shows the plugin's MCP server connected with its eight tools, each then called (LIVE8: C4.4, "
  "live/rerun-c8/C4.4/tool-matrix.md)", codes="W8+L8")
o("1.13.16", "verified_in_target", "TestBudgetBF passes in WTIME8 and LTIME8; quiet B-F on Windows p99 73.7 ms "
  "against 250 ms (C51-8, phase3/c8/quiet/c51-win-bf.log)")
o("1.13.17", "verified_in_target", "generator and drift tests green (W8), gen-mcp-docs --check clean (GATE8, DOCS8); "
  "the installed-host half: the eight tools listed by the frozen c8 bundle's server and each called (LIVE8: C4.4, live/rerun-c8/C4.4/tool-matrix.md)",
  codes="W8+GATE8+DOCS8")
o("1.14.1", "verified_in_target", "D36: six commands ship; wantCommands in internal/commands is the six (status, "
  "recall, pin, why, dropped, eval); C4.5 ran the six in a real session on c8 (live/rerun-c8/C4.5/), with known "
  "issue 17 (status's description names a 'last decision' the page does not show, D76(b))")
o("1.14.5", "unsupported", "retired by D36(a): 0.3.0 ships no manual checkpoint; /qompack:checkpoint is removed with "
  "SP14-M3-01 and its three TestCheckpoint_* tests; TestAll_CoversEverySlashCommand pins the six (W8, L8); C4.5 "
  "on c8 lists no qompack:checkpoint")
o("1.14.6", "verified_in_target", "reporting-rule unit tests green (W8, L8); C5.5 ran on c8's frozen bundle (C55-8, "
  "D77): 40 of 40 planned trials, read as confirmatory; the pre-registered verdict is inconclusive and is quoted, "
  "not claimed as a benefit (A8 items 1-3)")
o("1.14.10", "partial_verified", "coverage half green (COVER8, and release-check's ci-local cover, REL8); no "
  "commandstest conformance package and no internal/commands benchmark exist, so those halves have no artifact",
  codes="COVER8+REL8")
o("1.15.6", "unsupported", "F-3: the (1-1/e) guarantee is retired; the held substance "
  "TestSelect_LazyGreedyPrunesEvaluationsWithoutChangingTheAnswer is green on c8 (W8, L8) and submodular "
  "selection ships disabled")
o("1.15.7", "unsupported", "E-1/section 3.5: native ephemeral eviction is retired; its replacement "
  "TestPropose_ChoosesAtMostOneRepresentationPerItem is green on c8 (W8, L8)")
o("1.15.8", "verified_in_target", "the executed evidence is TestSequitur_InvariantsHoldAfterEveryAppend "
  "(internal/grammar), green in W8 and L8; symidx.py's match of internal/analyzer Propose is a false positive "
  "(c6-final/runs/map-symbols-at-c8.txt)")
o("1.15.14", "unknown", "internal/analyzer and internal/grammar have no benchmark (none in C5.2's eight chunks on "
  "c8), so the row has nothing to measure; not verified in target", codes="")
o("1.16.5", "unknown", "D37(c): no test or step computes the warm-vs-cold O4 delta; not verified in target", codes="")
o("1.16.10", "unknown", "D37(c): TestPrefixReorderingNotAttempted is absent and no declaration exists (no "
  "docs/adr/0016); not verified in target", codes="")
o("1.16.11", "verified_in_target", "superseded guarantee: the phase-7 store, checkpoint and scheduler families "
  + C52_BOTH + " (store: GC_50kObjects, CountFold_4MiB, MarkEncoded_100, OpenStore_50kRoots and the PutBytes "
  "NoRedact/KeepRaw variants; checkpoint; internal/scheduler and the daemon's scheduler benches), reported")
o("1.17.1", "verified_in_target", "two six-target bundle builds of c8 byte-identical (BUNDLES8: "
  "phase3/c8/bundle-diff.txt empty); hosted release-dry-run's release-version bundles equal the frozen ones, 91 "
  "files (phase3/c8/hosted-release-bundles.txt); the published bin/ equal the frozen ones "
  "(phase3/c8/release-bin-compare.txt, TAG8); release-check's real-binary determinism PASS (REL8); "
  "assembler/archive determinism units green (W8)", codes="BUNDLES8+REL8+TAG8+W8")
o("1.17.3", "unknown", "D37(c): no binary-size check exists; not verified in target", codes="")
o("1.17.4", "verified_in_target", "judged on TestPlatform_HookLauncherForms (renamed from "
  "TestPlatform_WindowsHookLauncherForms, run on every OS, D37(b)) with "
  "TestPlatform_PluginRootWithSpacesAndUnicode: green in W8 and L8")
o("1.17.5", "unknown", "D37(c): bench-hotpath has no --bundle universal/native mode, so the launcher-overhead split "
  "cannot run as written; not verified in target. Diagnostic only: quiet B-D p99 88.5 ms on Windows (C51-8), "
  "reported without a limit", codes="")
o("1.17.6", "partial_verified", "B-A and B-E halves: Windows verified on C51-8 on AC (B-A p99 16.4 ms against 50, "
  "B-E p99 166.8 ms against 2000); Linux B-A is not verified in target (D53(b)) and Linux's quiet B-E on c8 is "
  "report-only, not committed. The launcher-cost half has no possible artifact (D37(c), see 1.17.5)",
  codes="C51-8")
o("1.17.7", "verified_in_target", "backup/maintenance units and test/e2e install tests green (W8, LE2E8); "
  "release-check's rollback rehearsal PASS on c8 (REL8: ./internal/store/ (3), ./test/e2e/ (3)); the install "
  "through the real marketplace flow ran on the released bytes, equal to c8's frozen bin/ (REH8, D80: Windows "
  "real profile at local scope and an isolated profile, Linux container without a model, each through its "
  "qompack-<os>-<arch> entry, then uninstall); TestInstall_HostCLIInstallUpgradeUninstall skips without the "
  "claude CLI on hosted and container lanes", codes="REL8+W8+LE2E8")
o("1.17.8", "partial_verified", "maintenance/backup units green (W8); TestRollbackRehearsal_* green (W8, LE2E8) and "
  "release-check's rollback rehearsal PASS on c8 (REL8); install and uninstall on the released bytes (REH8). "
  "Open: the installed upgrade with project work preserved (C4.8, UAT-12's upgrade leg) was not re-run on c8; "
  "it passed on c7 and is carried with a D53(f) note naming the files changed since "
  "(live/rerun-c8/CARRIED.md, CARRY-L7)", codes="REL8+W8+LE2E8")
o("1.17.9", "partial_verified", "schema-bump units green (W8, L8); release-check's rollback rehearsal PASS on c8 "
  "(REL8); TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting skips without the claude CLI on hosted and "
  "container lanes and the Windows logs are non-verbose, so its execution on c8 is unproven; the old-released-"
  "reader matrix has no artifact (D37 map); a newer settingsVersion degraded as designed in a real session on c8 "
  "(LIVE8: C4.9 (b), live/rerun-c8/C4.9/notes.txt). Open: C4.8, the upgrade from the previous build, was not "
  "re-run on c8 (CARRY-L7)", codes="W8+L8+REL8")
o("1.17.10", "partial_verified", "test/platform green on windows/amd64 (W8; hosted test (windows-latest)), "
  "linux/amd64 (L8) and darwin/arm64 (hosted test (macos-latest)); windows/arm64, linux/arm64 and darwin/amd64 are "
  "cross-compile only (XBUILD8), unknown; C4.11: the Linux container install without a model kept bin/qompack's "
  "exec bit (REH8, D80(c)), but no Linux, macOS or windows/arm64 Claude Code session exists (D34(c))",
  codes="W8+L8+XBUILD8")
o("1.17.11", "verified_in_target", "test/fault green on c8 on Windows (W8 testpkgs, 458 s) and in hosted test "
  "(windows-latest) at -count=2, test (ubuntu-latest) and test (macos-latest); internal/cli's TestFaultActive_* "
  "green (W8, L8). Attempt 1's Windows red was internal/daemon's binary, an undetermined runtime crash that did not "
  "recur (D75(c), phase3/c8/hosted/windows-crash.md), not test/fault")
o("1.17.12", "verified_in_target", "test/security (incl. TestV6_ArchivedReadRetainsItsAuthorizationBoundary) and "
  "test/guards green on c8 (W8, L8); the real-session privacy half ran on c8 (planted credentials, deny-ruled "
  "files)")
o("1.17.13", "verified_in_target", "govulncheck clean in hosted security (SEC8) and in release-check (REL8); gosec "
  "inside golangci-lint clean (GATE8)", codes="SEC8+GATE8+REL8")
o("1.17.14", "partial_verified", "the test/fault suite that replaces the section 12.3 rows is green on c8 (W8, and "
  "hosted test on all three OSes); F-4's per-row mapping of the nine section 12.3 rows to test/fault cases is "
  "still owed and has no artifact")
o("1.17.15", "verified_in_target", "fsck and publication-accounting tests green (W8, L8); live on c8: C1.6's "
  "takeover and fsck after the idle exit (index.files ok), and the C1.7 restore smoke (backup create, verify and "
  "restore exit 0, fsck of source and destination exit 0); detection, not recovery; known limits: fsck beside a "
  "live daemon (D57(b)) and known issue 18 (D76(c))")
o("1.17.16", "verified_in_target", "doctor tests green (W8, L8); the real-session half ran on c8 (C4.2 through "
  "UAT-02, live/rerun-c8/UAT-02/)")
o("1.17.17", "verified_in_target", "version units green (W8); release-check --tag v0.3.0 on c8: 'tag v0.3.0, "
  "internal/core.Version 0.3.0, HEAD's exact tag agrees' (REL8); release.yml's release-check --tag at the tag "
  "passed (TAG8)", codes="W8+REL8+TAG8")
o("1.17.18", "partial_verified", "goreleaser half: release-check --tag v0.3.0 on c8 on the reference host, all "
  "18 steps PASS and none skipped (REL8); hosted release-dry-run green on c8 (CI8); release.yml at the tag "
  "passed: release-check --tag, assembly, marketplace, release notes and the goreleaser draft (TAG8). Open: the "
  "actionlint half has no artifact, because no actionlint step exists in release.yml, ci.yml or release-check "
  "(the c6/c7 cells' 'actionlint at the tag' was wrong) and no ruling retires it",
  codes="REL8+CI8+TAG8")
o("1.17.19", "partial_verified", "all 21 ci.yml jobs green on c8 (CI8; the four SP-17-era job names never existed, "
  "their scope runs inside test, test-e2e, crossbuild, plugin-validate and release-dry-run). Open: develop and "
  "main are protected against force pushes and deletion but require no status check (c6-final/runs/"
  "c8-hosted-runs.txt), so no job is 'required'; that is a repository setting, not set for 0.3.0", codes="CI8")
o("1.17.20", "verified_in_target", "checksums.txt over the six archives built and compared (BUNDLES8; hosted "
  "release-dry-run's checksums.txt equal, phase3/c8/hosted-release-bundles.txt); checksum-format units green (W8)")
o("1.18.1", "partial_verified", "TestOwnedDocsExist green (W8, L8, DOCS8); the historical count assertions "
  "(IsTwentyOne, HasNoDuplicates, StartWithH1) have no current test (F-5)")
o("1.18.3", "partial_verified", "replacement TestGenConfigDocs_LeavesMatchDefaultsOneToOne green (W8, GATE8, "
  "DOCS8); it pins leaves to defaults, not documented ranges to Validate() (F-5)")
o("1.18.4", "partial_verified", "replacement TestGenConfigDocs_GatedSwitchesRenderFromSource green (W8, DOCS8); the "
  "runtime-section additivity marker has no standalone test (F-5)")
o("1.18.8", "partial_verified", "TestADRIndexListsEveryADR green (W8, L8, DOCS8); the per-ADR shape and "
  "decision-id checks have no current test (F-5)")
o("1.18.9", "documented", "F-5: the verbatim ten-invariants claim is retired; only ADR-index presence is executed "
  "(TestADRIndexListsEveryADR green on c8)")
o("1.18.11", "partial_verified", "UAT shape tests green (W8, L8, DOCS8); no test quotes the phase exit criteria")
o("1.18.12", "unknown", "D3: the human half has no possible artifact; the agent-run form: UAT-02, 04, 05 run 2, "
  "06, 07, 08, 11 and 12 (steps 1-5, 8) ran on c8 (LIVE8), and UAT-01, 03, 05 run 1, 09, 10 and UAT-12's upgrade "
  "leg carry c7's pass with a c8 note in docs/uat.md (CARRY-L7); TestUATUnexecutedRowsSayUnverified green (DOCS8)")
o("1.18.13", "verified_in_target", "generator determinism tests green (W8, GATE8, DOCS8); B-DOC is a diagnostic "
  "with no benchmark (E-2)")
o("1.5.15", "verified_in_target", "producers and their tests green on c8 (W8, L8); the real-observation half (F-2) "
  "ran on c8: C4.2 through UAT-02's status and doctor reads (live/rerun-c8/UAT-02/)", codes="W8+L8")
o("1.5.19", "verified_in_target", "test/fault green on c8 (W8 testpkgs; hosted test on all three OSes, Windows at "
  "-count=2); TestFaultActive_UnsetIsInertForAllElevenSites green (W8, L8)")

rows_out = [inv[0][:11] + ["c8_result", "c8_evidence"]]
counts = Counter()
for r, m in zip(inv[1:], mp[1:]):
    rid = r[0]
    assert rid == m[0], (rid, m[0])
    st = steps_of(m[3])
    codes, notes, unmet, none = [], [], [], False
    for kind, v in st:
        if kind == "green":
            codes.append(v)
        elif kind == "c51":
            assert rid in O, f"{rid}: a C5.1 row needs a per-row decision"
        elif kind == "none":
            none = True
        elif kind == "live":
            met, note = live_judge(v)
            if note:
                notes.append(note)
            if not met:
                unmet.append(v)
    seen = []
    for c in codes:
        for p in c.split("+"):
            if p not in seen:
                seen.append(p)
    code_s = "+".join(seen)
    if rid in O:
        res, note, oc = O[rid]
        if oc is not None:
            code_s = oc
    else:
        if none:
            raise SystemExit(f"{rid}: none-row without a decision")
        res = "partial_verified" if unmet else "verified_in_target"
        note = None
        if "C52-8" in seen:
            note = "measured on c8 (C52-8), reported (diagnostic)"
    parts = [p for p in [code_s] if p]
    if note:
        parts.append(note)
    if rid not in O or res == "verified_in_target":
        parts.extend(notes)  # a decided non-verified row's own note already names its live parts
    if unmet and rid not in O:
        parts.append("open: " + "; ".join(unmet))
    parts.append(changed_c7_c8(r[4]))
    ev = "; ".join(parts)
    counts[res] += 1
    rows_out.append(r[:11] + [res, ev])

out = dump(rows_out)
# The eleven existing columns are unchanged.
assert dump([row[:11] for row in rows_out]) == dump([row[:11] for row in inv])
print("c8", dict(counts), sum(counts.values()))
if "-v" in sys.argv:
    for row in rows_out[1:]:
        print(row[0], row[9], "->", row[11], "|", row[12][:300])

# ---------------------------------------------------------------------------------------------------
# The map page.
order = ["verified_in_target", "partial_verified", "implemented_unverified", "failed", "unknown", "unsupported",
         "documented"]
rows = rows_out[1:]
count6 = Counter(r[7] for r in rows)
count7 = Counter(r[9] for r in rows)


def cell(s):
    return s.replace("|", "/").replace("\n", " ")


def table(results):
    t = ["| id | current assertion | c7 result | c8 result | candidate 8 evidence; what is open |",
         "|---|---|---|---|---|"]
    for r in rows:
        if r[11] in results:
            t.append(f"| {r[0]} | {cell(r[2])[:80]} | `{r[9]}` | `{r[11]}` | {cell(r[12])} |")
    return "\n".join(t)


moved = [r for r in rows if r[9] != r[11]]

sys.dont_write_bytecode = True  # no __pycache__ beside the evidence
sys.path.insert(0, HERE)
from map_text import HEAD, TAIL  # noqa: E402  (the fixed prose of the page, beside this script)

parts =[HEAD.rstrip("\n") + "\n"]
parts.append("## Counts\n")
parts.append("| result | candidate 6 | candidate 7 | candidate 8 |\n|---|---|---|---|")
for k in order:
    if count6.get(k) or count7.get(k) or counts.get(k):
        parts.append(f"| `{k}` | {count6.get(k, 0)} | {count7.get(k, 0)} | {counts.get(k, 0)} |")
parts.append(f"| total | {sum(count6.values())} | {sum(count7.values())} | {sum(counts.values())} |\n")
parts.append(f"{len(moved)} rows have a different result on candidate 8 than on candidate 7; every one is in "
             "the table below. The other rows keep their candidate 7 result, now on candidate 8's own runs.\n")
parts.append("## Rows whose result moved from candidate 7\n")
mt = ["| id | current assertion | c7 result | c8 result |", "|---|---|---|---|"]
for r in moved:
    mt.append(f"| {r[0]} | {cell(r[2])[:80]} | `{r[9]}` | `{r[11]}` |")
parts.append("\n".join(mt) + "\n")
parts.append("## Candidate 8 rows that are not `verified_in_target`\n")
parts.append(f"The other {counts['verified_in_target']} rows are verified in target on candidate 8; their "
             "cells in the TSV list the evidence codes.\n")
parts.append("### Partial: a part is open, has no possible artifact, or is Linux fsync-bound\n")
parts.append(table({"partial_verified", "implemented_unverified", "failed"}) + "\n")
parts.append("### No possible artifact, retired, or documented\n")
parts.append(table({"unknown", "unsupported", "documented"}) + "\n")
parts.append(TAIL.rstrip("\n") + "\n")
page = "\n".join(parts)
assert "@@" not in page

if "--write" in sys.argv:
    with open(INV, "w", encoding="utf-8", newline="") as f:
        f.write(out)
    with open(OUT_MAP, "w", encoding="utf-8", newline="\n") as f:
        f.write(page)
    print("wrote", INV, "and", OUT_MAP)
