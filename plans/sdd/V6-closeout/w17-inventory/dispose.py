"""C6.2: give every inventory row a candidate 6 and a candidate 7 disposition.

Reads plans/sdd/V6-remediation/inventory-current.tsv (304 rows) and plans/sdd/V6-closeout/
inventory-map.tsv (the D37 evidence-step map, same order) and appends four columns to the first:
c6_result (the file's own vocabulary) and c6_evidence (evidence codes, defined in
plans/sdd/V6-closeout/inventory-c6-map.md, plus the reason and what closes a pending row), then
c7_result and c7_evidence: candidate 6's disposition carried to candidate 7 where the covered code
is unchanged (D57(c), runs/c7-carry-proof.txt), and judged on candidate 7's own runs where it is not.

FREEZE7 below is candidate 7's freeze commit on verify/v6 (phase3/c7/night.log), or None while it
has not been frozen; PRE7_GREEN says whether candidate 7's pre-freeze check passed (the freeze runs
only after it does). The rulings applied are D57(a)-(g) in plans/V6-CLOSEOUT-CHECKLIST.md.
"""
import csv, io, os, re, sys
from collections import Counter

ROOT = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "..", ".."))
INV = os.path.join(ROOT, "plans/sdd/V6-remediation/inventory-current.tsv")
MAP = os.path.join(ROOT, "plans/sdd/V6-closeout/inventory-map.tsv")

def read(p):
    with open(p, encoding="utf-8", newline="") as f:
        return list(csv.reader(f, delimiter="\t", quoting=csv.QUOTE_MINIMAL))

inv, mp = read(INV), read(MAP)
assert inv[0][:7] == ["id", "original_assertion_reference", "current_assertion", "current_test_symbols",
                      "source_paths", "disposition", "limitation"], inv[0]
assert len(inv) == 305 and len(mp) == 305
inv_hdr = inv[0][:7]

C7_CODE = "b31d0753"   # candidate 7's code: closeout/integration b31d0753
FREEZE7 = "d20309c03ffc364e4cc48663be73cfbb1f2309b2"  # from phase3/c7/night.log "candidate 7 frozen at <sha>"
PRE7_GREEN = True     # phase3/c7/prefreeze/summary.log: gate, testpkgs, internal all exit=0
BUNDLES7 = True       # night.log: every bin/ matches w17-release and the c7 bundles host-validated

GREEN = {  # evidence step -> code, for a step whose candidate 6 artifact is green
    "win-tree": "WIN", "linux-tree": "LNX", "linux-e2e": "LE2E", "linux-child": "CHILD",
    "win-race": "WRACE", "fuzz": "FUZZ", "cover": "COVER", "bundles": "BUNDLES",
    "win-timing": "WTIME", "linux-timing": "LTIME", "C5.2": "C52", "C5.3": "REPLAY", "meta": "FREEZE",
}

def gens_code(tok):
    if "replay" in tok: return "REPLAY"
    if "plugin-validate" in tok: return "PV"
    if "govulncheck" in tok: return "SEC"
    if "build-all" in tok: return "XBUILD"
    if "test-docs" in tok or "command-docs" in tok: return "DOCS"
    return "GATE+DOCS"  # gen-config-docs / gen-mcp-docs run in both

def steps_of(cell):
    out = []
    for raw in [s.strip() for s in cell.split(";") if s.strip()]:
        base = raw.split(" ")[0]
        if base in ("C4.1", "C4.2", "C4.3", "C4.4", "C4.5", "C4.6", "C4.8", "C4.11", "C1.6", "UAT-01..UAT-12",
                    "UAT-12") or base.startswith("C4.") or base.startswith("UAT"):
            out.append(("live", raw))
        elif base == "gens":
            out.append(("green", gens_code(raw)))
        elif base == "lint":
            out.append(("green", "GATE"))
        elif base == "release":
            out.append(("release", raw))
        elif base in ("C5.1",):
            out.append(("c51", "C51"))  # landed; Windows green, Linux B-A/B-B red (D53(b)): per-row override
        elif base == "C5.5":
            out.append(("pending", "P-C55"))
        elif base == "none":
            out.append(("none", raw))
        elif base in GREEN:
            out.append(("green", GREEN[base]))
        else:
            raise SystemExit(f"unmapped step {raw!r}")
    return out

LIVE_C6 = {"C4.1", "C4.3", "C4.4", "C4.5", "C4.6", "C4.8", "C4.9", "C1.7",
           "UAT-01", "UAT-03", "UAT-04", "UAT-05", "UAT-06", "UAT-09", "UAT-10", "UAT-12"}

def live_note(raw):
    names = re.findall(r"(C4\.\d+|C1\.\d|UAT-\d\d)", raw)
    in_lane = [n for n in names if n in LIVE_C6]
    carried = [n for n in names if n not in LIVE_C6 and n != "C1.6"]
    s = ""
    if in_lane:
        s += "pending P-LIVE (" + ", ".join(in_lane) + ")"
    c4 = [n for n in carried if n in ("C4.2", "UAT-02")]
    other = [n for n in carried if n not in c4]
    if c4:
        s += ("; " if s else "") + "not in the c6 lane: " + ", ".join(c4) + " (CARRY-C4)"
    if other:
        s += ("; " if s else "") + "not in the c6 lane: " + ", ".join(other) + " (D34(c): recorded unknown)"
    return s

# Per-row decisions that the step rule cannot make. (result, note); note is appended to the codes.
O = {}
def o(i, result, note=None, codes=None):
    O[i] = (result, note, codes)

# D57(e): a candidate 5 C5.2 measurement counts on candidates 6 and 7 when every file the benchmark
# executes is byte-unchanged; the row's own cell names the carry and its proof file. Which files each
# benchmark executes is measured, not inferred: runs/c52-executed-files.txt (c52exec.py, a coverage
# trace of each benchmark at -benchtime=1x, non-test files) and runs/c52-test-files.txt (c52tests.py,
# the _test.go files the benchmark's test binary executes, which Go coverage never instruments).
C52_EXEC = "runs/c52-executed-files.txt"
C52_TEST = "runs/c52-test-files.txt"
C52_PROOFS = C52_EXEC + " and " + C52_TEST
C52_NOTE = ("C52 carry (D57(e)): measured on c5 0d06ab12; no file the benchmark executes changed c5->c6, "
            "product or _test.go (" + C52_PROOFS + ")")
C52_COMMENT = ("C52 carry (D57(e)): measured on c5 0d06ab12; the one executed file that changed c5->c6 is the "
               "test helper internal/paths/pathstest/home.go, by comment lines only, and no changed _test.go "
               "file is executed, so the executed code is byte-identical (" + C52_PROOFS + "; the "
               "comment-only reading is wave 17b's, for the owner to ratify)")
# The internal/store set does not carry (runs/c52-test-files.txt): its test binary executes _test.go
# files that changed c5->c6, the fix of the zero testing.T that leaked HOME in candidate 5's own run.
C52_STORE = ("the internal/store benches' c5 figures do not carry by D57(e): their test binary executes "
             "_test.go files changed c5->c6 (" + C52_TEST + "). BenchmarkGC_50kObjects and both "
             "BenchmarkSearch_1000Roots* execute changed lines (gc_test.go, search_test.go and the helpers in "
             "testdouble_test.go now take testing.TB): the fix of the zero testing.T that leaked HOME in c5's "
             "own run, where pathstest failed the store binary after PASS and every bench after GC ran "
             "against the leaked home (phase3/c5/quiet/c52-win/c52-win-r*-candidate-store-1s.log). The other "
             "store benches reach no changed line of a function (PutBytes*, PutObject, GetChunk, OpenSpan and "
             "OpenStore reach testdouble_test.go's newFakeClock and storeOpt helpers), but the binary runs the "
             "package initializer of maint_edge_test.go, new c5->c6 (errInjectedBarrier), and testdouble_test.go "
             "is not byte-unchanged; CountFold and MarkEncoded ran after the leak on c5")
C52_TOOLNAMES = ("internal/core/toolnames.go, which gained CutHostPluginTool c5->c6; no executed block covers a "
                 "changed line, but D57(e) asks for the file byte-unchanged, so the c5 figure does not carry")
P_C52R = ("pending P-C52R (a quiet C5.2 re-run on c7)")

o("1.1.1", "documented", "meta row: candidate 6 identity is FREEZE (99d0b18, bundle and exe sha256); no test exists by design")
o("1.1.16", "verified_in_target", "C52 measured and reported (diagnostic); " + C52_NOTE + "; internal/obs's two new files are not executed by it (runs/c52-package-diffs-c5-to-c6.txt)")
o("1.1.24", "verified_in_target", "test/guards green on c6 (WIN, LNX); F-1's historical guard-rename expectation is retired, D23 ratified TestGuard_EveryProductReadIsClassified as a standing guard")
o("1.1.27", "partial_verified", "C52 measured config, paths and cli benches (reported, diagnostic); internal/core and tools/devtool have no benchmark (c52-names.tsv). The config and paths benches carry: " + C52_NOTE + ". BenchmarkHookNoop_InProcess does not: it executes changed lines of internal/pluginmanifest/manifest.go (ForTarget's Description, lines 276 and 279) and the changed internal/cli/dispatch.go, internal/cli/doctor.go and internal/ipc/spool.go, and its test binary runs two package initializers of precompact_spool_submode_test.go, new c5->c6 (" + C52_TEST + "); " + P_C52R + " of BenchmarkHookNoop_InProcess")
o("1.1.28", "documented", "retired E-3: Qompack.md is v1.8 with its Revision log (v1.6 D5, v1.7 D36, v1.8 D41); no test by design", codes="")
o("1.2.8", "verified_in_target", "REPLAY: the replay-gate job log prints the breakpoint OPT line (4 markers, 161 candidates), a diagnostic only")
o("1.5.12", "partial_verified", "Windows half verified on the designated quiet C5.1 run on AC (D57(e)): C51 B-A p99 30.72 ms, B-B 24.58 ms against 50; X11's own rows pass on AC on c6's tree (X11-E1: 3/3, 0 deferred, D57(g)), and the isolated hot-path row passes (WTIME, on AC); hosted bench-gate 110676058217, 110676058253, 110676058353 succeeded, report-only (Q1). Not counted (X11-BAT): X11's two c6 Windows failures, its no-ledger arm in both isolated executions (p3-win-e2e-timing.log B-A/B-B p99 81.9/57.3 ms, p3-win-x11-alone.log 98.3/81.9 ms; the ledger phase is not reached), ran on battery and are invalid as reference measurements, neither pass nor fail (D57(d)). Open half: Linux B-A/B-B fail in LTIME and C51, fsync-bound, not verified in target (D53(b))", codes="WTIME+LTIME+C51+X11-E1")
o("1.5.15", "partial_verified", "producers and their tests green on c6; the real-observation half (F-2) needs a real session: CARRY-C4 (C4.2 passed on c4, not in the c6 lane); the c6 lane's status/doctor reads (P-LIVE) refresh it")
o("1.6.19", "partial_verified", "C52 measured on c5 (superseded-guarantee): PutBytes cold/warm 6-8x over the 3 ms/400 us budgets, SP06-D2 wontfix for 0.3.0 (D54); the store product files are unchanged c5->c6 (" + C52_EXEC + "), but " + C52_STORE + "; " + P_C52R + " of the store set, or, for the PutBytes, PutObject, GetChunk and OpenSpan figures (which ran before the leak on c5), a coordinator ruling that an executed _test.go file whose changes the benchmark does not reach, plus a new package-level initializer, leaves D57(e) met; BenchmarkSearch_1000Roots needs P-C52R under either reading")
o("1.8.13", "implemented_unverified", "C52 measured on c5 (superseded-guarantee): OnToolUse 256 KB Delta p99 59-74 ms against B-C's soft 50 ms, SP08-D1 wontfix for 0.3.0 (D54). The figure does not carry by D57(e): the three BenchmarkOnToolUse_* execute internal/observer/observer.go, whose sessionState gained two fields (ThrashFloor, ReplyWarning), a layout change on the measured path, and " + C52_TOOLNAMES + " (" + C52_EXEC + "); " + P_C52R + " of the three BenchmarkOnToolUse_*", codes="")
o("1.8.2", "partial_verified", "tombstone tests green (WIN, LNX). C52 measured BenchmarkTombstone on c5 (diagnostic); it executes " + C52_TOOLNAMES + " (" + C52_EXEC + "), and no changed _test.go file (" + C52_TEST + "); " + P_C52R + " of BenchmarkTombstone, or a coordinator ruling that an appended, unexecuted function leaves D57(e) met")
o("1.9.12", "verified_in_target", "all seven TestBudget_* pass in WTIME and LTIME on c6 (TestBudget_DetectorScan, once a pre-existing red, passes on both); negknow Open 61.6/77.0 ms against 300 ms in C52 (SP09-D1 fixed, D54); " + C52_COMMENT)
o("1.10.16", "verified_in_target", "quiet B-E passes on both OSes (C51: Windows p99 170.75 ms, CPU 31.25 ms, on AC; Linux 384.11 ms, CPU 7.76 ms; against 2000 ms). The B-E figures of X11's c6 Windows runs are not cited: those runs were on battery, invalid as reference measurements (X11-BAT, D57(d))", codes="C51")
o("1.10.17", "verified_in_target", "C52 measured and reported (diagnostic); " + C52_COMMENT + "; checkpoint's own product changes (precompact.go, types.go) are not executed by its benchmarks (runs/c52-package-diffs-c5-to-c6.txt)")
o("1.10.18", "unknown", "D37(c): no step runs the two replay --phase 4 runs with the frontier toggled; not verified in target, no new harness before release", codes="")
o("1.11.16", "partial_verified", "C52 measured rules BenchmarkPathScoped and skills BenchmarkIndex; " + C52_NOTE + "; internal/rehydrate has no benchmark, so the rehydrate share of L5 latency has no artifact")
o("1.12.14", "partial_verified", "structural half TestSchedulerNotOnHotPath green (WIN, LNX); Windows B-A half verified on the designated quiet C5.1 run on AC (D57(e)): C51 B-A p99 30.72 ms against 50, corroborated by X11-E1 (B-A p99 36.9 ms, 3/3); the c6 battery runs are not counted (X11-BAT, D57(d)). Open half: Linux B-A is not verified in target (D53(b))", codes="WIN+LNX+C51+X11-E1")
o("1.12.17", "partial_verified", "C52 measured the internal/scheduler benches and the daemon's scheduler_bench_test.go benches on c5. The internal/scheduler benches carry: " + C52_NOTE + ". The five daemon benches execute " + C52_TOOLNAMES + " (" + C52_EXEC + "), and their test binary runs the package initializer of precompact_settle_retry_test.go, new c5->c6 (errInjectedSpoolLock, " + C52_TEST + "); " + P_C52R + " of the daemon scheduler benches, or a coordinator ruling that covers both an appended, unexecuted function and a new, unreached package-level initializer in a test file. The map's internal/hostperm BenchmarkEvaluate is a name collision, not a scheduler bench, and its c5 figure does not carry (hostperm/policy.go changed)")
o("1.13.4", "partial_verified", "the historical V6-AUTH FAIL is cleared for the automated half: test/security and the internal/mcp TestV6_* suites pass on c6 on Windows, Linux and macOS (WIN, LNX); the real-session half is pending")
o("1.13.5", "partial_verified", "automated half green on c6 (D7 deny-rule honouring, capture-scope suites); the real-session half is pending")
o("1.13.14", "partial_verified", "producer and seam tests green on c6; real mcp.server_registered observation (F-2) needs a real session: pending P-LIVE (C4.4 sessions run the MCP server)")
o("1.13.16", "verified_in_target", "TestBudgetBF passes in WTIME and LTIME on c6 (a pre-existing red on develop, now green on both)")
o("1.13.17", "partial_verified", "generator and drift tests green (WIN), gen-mcp-docs --check clean (GATE, DOCS); the installed-host half, every documented tool checked against the installed host, is pending P-LIVE (C4.4)")
o("1.14.1", "verified_in_target", "D36: six commands ship; wantCommands in internal/commands is the six (status, recall, pin, why, dropped, eval)")
o("1.14.5", "unsupported", "retired by D36(a): 0.3.0 ships no manual checkpoint, /qompack:checkpoint is removed with exit criterion SP14-M3-01 and its three TestCheckpoint_* tests; TestAll_CoversEverySlashCommand pins the six on c6 (WIN, LNX); the c6 lane's C4.5 (P-LIVE) runs the six in a real session")
o("1.14.6", "partial_verified", "reporting-rule unit tests green (WIN, LNX); the live trials are pending P-C55 (D53(g))")
o("1.14.10", "partial_verified", "coverage half green (COVER); no commandstest conformance package and no internal/commands benchmark exist, so those halves have no artifact")
o("1.15.6", "unsupported", "F-3: the (1-1/e) guarantee is retired; the held substance TestSelect_LazyGreedyPrunesEvaluationsWithoutChangingTheAnswer is green on c6 (WIN, LNX) and submodular selection ships disabled")
o("1.15.7", "unsupported", "E-1/section 3.5: native ephemeral eviction is retired; its replacement TestPropose_ChoosesAtMostOneRepresentationPerItem is green on c6 (WIN, LNX)")
o("1.15.8", "verified_in_target", "the executed evidence is TestSequitur_InvariantsHoldAfterEveryAppend (internal/grammar), green in WIN and LNX; the symbol column's `Prop*` names no grammar test, and symidx.py's match of internal/analyzer Propose was a false positive (runs/map-symbols-at-c6.txt)")
o("1.15.14", "unknown", "C52: internal/analyzer and internal/grammar have no benchmark (c52-names.tsv), so the row has nothing to measure; not verified in target", codes="")
o("1.16.5", "unknown", "D37(c): no test or step computes the warm-vs-cold O4 delta; not verified in target, no new harness before release", codes="")
o("1.16.10", "unknown", "D37(c): TestPrefixReorderingNotAttempted is absent and no declaration exists (no docs/adr/0016; no file under docs/ or Qompack.md declares prefix-reorder non-delivery); not verified in target", codes="")
o("1.16.11", "partial_verified", "C52 measured the phase-7 store, checkpoint and scheduler families on c5. The checkpoint benches carry (" + C52_COMMENT + "), and the internal/scheduler benches carry (no executed file changed, product or _test.go). The phase-7 store benches (GC_50kObjects, CountFold_4MiB, MarkEncoded_100, OpenStore_50kRoots and the PutBytes NoRedact/KeepRaw variants) do not: " + C52_STORE + ". The daemon scheduler benches execute " + C52_TOOLNAMES + ", and their test binary runs the package initializer of precompact_settle_retry_test.go, new c5->c6 (errInjectedSpoolLock, " + C52_TEST + "); " + P_C52R + " of the store and daemon scheduler benches, or a coordinator ruling covering both kinds of unreached change")
o("1.17.1", "verified_in_target", "two six-target bundle builds byte-identical on c6 (BUNDLES), assembler/archive determinism units green (WIN)")
o("1.17.3", "unknown", "D37(c): no binary-size check exists; diagnostic only, the frozen c6 binaries measure 9.29-10.49 MB (windows-amd64 qompack.exe 10,416,640 B); not verified in target", codes="BUNDLES")
o("1.17.4", "verified_in_target", "judged on TestPlatform_HookLauncherForms, renamed from TestPlatform_WindowsHookLauncherForms and now run on every OS (D37(b), inventory-map.tsv), with TestPlatform_PluginRootWithSpacesAndUnicode: green in WIN and LNX")
o("1.17.5", "unknown", "D37(c): bench-hotpath has no --bundle universal/native mode, so the launcher-overhead split cannot run as written; not verified in target. Diagnostic only: quiet B-D p99 72.23 ms on Windows, 24.06 ms on Linux, reported without a limit (C51)", codes="")
o("1.17.6", "partial_verified", "B-A and B-E halves: Windows verified on the designated quiet C5.1 run on AC (D57(e); C51 B-A p99 30.72 ms against 50, B-E p99 170.75 ms against 2000); Linux B-E passes (384.11 ms), Linux B-A is not verified in target (D53(b)); the c6 battery runs are not counted (X11-BAT, D57(d)). The launcher-cost half has no possible artifact: bench-hotpath has no --bundle native mode (D37(c), see 1.17.5). Unlike 1.17.5, whose whole assertion is that split, this row also asserts budgets C51 measures, so it is partial_verified, not unknown", codes="C51")
o("1.17.7", "partial_verified", codes="WIN+LE2E", note="backup/maintenance units and test/e2e install tests green (WIN, LE2E); TestInstall_HostCLIInstallUpgradeUninstall skips without the claude CLI (container and hosted runs), RED-RELDRY stopped release-check before its rollback-rehearsal step (the step's two tests pass in the e2e lanes)")
o("1.17.8", "partial_verified", codes="WIN+LE2E", note="maintenance/backup units green (WIN); TestRollbackRehearsal_BeforeAndAfterTheFirstNewFormatWrite passes in the e2e lanes (WIN, LE2E); release-check's rollback step not reached (RED-RELDRY); the installed upgrade/uninstall is pending")
o("1.17.9", "partial_verified", codes="WIN+LNX", note="schema-bump units green (WIN, LNX); TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting skips without the claude CLI (container, hosted) and the Windows chain log is non-verbose, so its execution on c6 is unproven; the old-released-reader matrix has no artifact (D37 map); C4.8 upgrade from the c5 bundle is pending")
o("1.17.10", "partial_verified", "test/platform green on windows/amd64 (WIN; also hosted test windows-latest, where only test/fault was red), linux/amd64 (LNX) and darwin/arm64 (hosted macos-latest); windows/arm64, linux/arm64 and darwin/amd64 are cross-compile only (XBUILD), unknown; C4.11 Linux installed host: model sessions unknown (D34(c)), no-model half not run on c6")
o("1.5.19", "failed", "RED-FAULT: the row names the whole test/fault package, which has one red on c6 (hosted windows-latest -count=2, TestFault_Lifecycle/out_of_order_sessionend); root cause recorded, not a product defect: a read-order race in test/fault's audit (D57(b)); the test-only fix is not in c6's tree, so c6's own result stays failed; TestFaultActive_UnsetIsInertForAllElevenSites green (WIN, LNX); candidate 7's result is in c7_result")
o("1.17.11", "failed", "RED-FAULT: TestFault_Lifecycle/out_of_order_sessionend left a dangling retention root on hosted windows-latest's second -count=2 pass on c6 (passed in WIN and LNX); root cause recorded, not a product defect: test/fault's audit read the sidecars before retention-roots.jsonl beside a daemon still publishing a Stop (D57(b)); the test-only fix is not in c6's tree, so c6's own result stays failed; candidate 7's result is in c7_result")
o("1.17.12", "partial_verified", "the historical FAIL is cleared for the automated half: test/security (incl. TestV6_ArchivedReadRetainsItsAuthorizationBoundary) and test/guards pass on c6 on Windows, Linux and macOS; the real-session privacy half is pending")
o("1.17.13", "verified_in_target", "govulncheck clean in hosted security (SEC); gosec inside golangci-lint clean (GATE)", codes="SEC+GATE")
o("1.17.14", "failed", "RED-FAULT: the test/fault suite that replaces the section 12.3 rows has one red on c6 (hosted windows-latest -count=2), a read-order race in its audit (D57(b)), whose test-only fix is not in c6's tree; otherwise green in WIN and LNX; F-4's per-row mapping is still owed; candidate 7's result is in c7_result")
o("1.17.15", "partial_verified", "fsck and publication-accounting tests green (WIN, LNX), detection not recovery; the live restore smoke C1.7 is pending P-LIVE")
o("1.17.16", "partial_verified", "doctor tests green (WIN, LNX); D34(e)'s self-test fix is on c6; the real-session half passed on c4 only")
o("1.17.17", "partial_verified", "version units green (WIN); release-check's version agreement SKIPPED without a tag, and core.Version and plugin.json still read 0.1.0 at c6 while the bundles are stamped 0.3.0 by bundle --version; c6 cannot pass release-check --tag v0.3.0 (w17-release), so the agreement half is candidate 7's (c7_result)")
o("1.17.18", "failed", "RED-RELDRY: ci.yml release-dry-run on c6 failed in release-check's ci-local cover step (X11 at the hosted fsync tail on ubuntu, without the non-reference-disk declaration, D57(a)); build-all, generated docs, guards, licenses, determinism, rollback rehearsal, plugin-validate and marketplace were not reached; actionlint and the goreleaser snapshot run only on a tag; candidate 7's result is in c7_result")
o("1.17.19", "failed", "ci.yml 36955046276 on c6: 19 of 21 jobs green, release-dry-run (RED-RELDRY, D57(a)) and test (windows-latest) (RED-FAULT, D57(b)) red; the four SP-17-era jobs (package-gate, platform-matrix, fault-gate, install-gate) were never created, their scope runs inside test/test-e2e/crossbuild/plugin-validate/release-dry-run; develop has no branch protection (gh api: Branch not protected) and main is absent on origin, which C7.3 closes; candidate 7's result is in c7_result", codes="")
o("1.17.20", "verified_in_target", "checksums.txt over the six archives in BUNDLES and freeze.log; checksum-format units green (WIN)")
o("1.18.1", "partial_verified", "TestOwnedDocsExist green (WIN, LNX, DOCS); the historical count assertions (IsTwentyOne, HasNoDuplicates, StartWithH1) have no current test (F-5)")
o("1.18.3", "partial_verified", "replacement TestGenConfigDocs_LeavesMatchDefaultsOneToOne green (WIN, GATE, DOCS); it pins leaves to defaults, not documented ranges to Validate() (F-5)")
o("1.18.4", "partial_verified", "replacement TestGenConfigDocs_GatedSwitchesRenderFromSource green (WIN, DOCS); the runtime-section additivity marker has no standalone test (F-5)")
o("1.18.8", "partial_verified", "TestADRIndexListsEveryADR green (WIN, LNX, DOCS); the per-ADR shape and decision-id checks have no current test (F-5)")
o("1.18.9", "documented", "F-5: the verbatim ten-invariants claim is retired; 00-ARCHITECTURE.md carries the invariants, only ADR-index presence is executed (TestADRIndexListsEveryADR green on c6)")
o("1.18.11", "partial_verified", "UAT shape tests green (WIN, LNX, DOCS); no test quotes the phase exit criteria")
o("1.18.12", "unknown", "D3: the human half has no possible artifact; the agent-run form is the c6 live lane UAT-01, 03, 04, 05, 06, 09, 10, 12 (P-LIVE) plus earlier-candidate UAT-02/07/08/11 results CARRY-C4; TestUATUnexecutedRowsSayUnverified green (DOCS)")
o("1.18.13", "verified_in_target", "generator determinism tests green (WIN, GATE, DOCS); B-DOC is a diagnostic with no benchmark (E-2)")

# ---- Candidate 7 (D57(c)) ---------------------------------------------------------------------
# Candidate 7's code is closeout/integration b31d0753. Against candidate 6 (99d0b18c) its only
# product change is core.Version's default literal and plugin.json's version; test/fault,
# test/guards' nonrefdisk_test.go, the golden plugin.json, two workflows, .goreleaser.yaml and docs/
# also changed (runs/c7-carry-proof.txt). A row whose covered code is unchanged carries its
# candidate 6 disposition (C7-CARRY); a row over a changed path is judged on candidate 7's own runs.
# ci.yml 36981590450 on d20309c0, read with `gh run view 36981590450 --json jobs` at CI7_AT; the
# run was still in progress then.
CI7_AT = "2026-10-02 08:31Z"
CI7_GREEN = ("verify, cover, docs 110757119402, plugin-validate 110757119388, crossbuild, security, "
             "replay-gate, test (ubuntu-latest) 110757119503, test (macos-latest) 110757119443, test-e2e on "
             "ubuntu, macos and windows, timing and bench-gate on all three OSes")
CI7_GREEN_N = "18"
CI7_OPEN = "test (windows-latest) 110757119316, lint-windows 110757119431 and release-dry-run 110757119491"
FAULT_ROWS = ("1.5.19", "1.17.11", "1.17.14")
DOCS_CHG = ("1.18.1", "1.18.5", "1.18.6", "1.18.7", "1.18.8", "1.18.9", "1.18.10", "1.18.11", "1.18.12")
GUARD_ROWS = ("1.1.23", "1.1.24", "1.5.20", "1.17.12")
LANE7 = "its P-LIVE and P-C55 parts run on candidate 7's frozen bundles (D57(c))"

def pre7(step):
    return f"PRE7 {step} green on c7's code" if PRE7_GREEN else f"PRE7 {step} pending"

def c7_of(rid, r, res6, ev6):
    lane = ("; " + LANE7) if ("P-LIVE" in ev6 or "P-C55" in ev6) else ""
    if "P-C52R" in ev6:
        lane += "; its P-C52R part (a quiet C5.2 re-run) runs on c7's code"
    if rid in FAULT_ROWS:
        if PRE7_GREEN:
            res = "partial_verified"
            ev = ("PRE7; test/fault carries the claim-before-evidence fix (dd8e9fd2, 88626fdd; D57(b)) and passes "
                  "on Windows on c7's code (PRE7 testpkgs, -count=1), with "
                  "TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence; hosted test (ubuntu-latest) "
                  "110757119503 and test (macos-latest) 110757119443, which run test/fault, are green on c7 "
                  "(ci.yml 36981590450, at " + CI7_AT + "); closes when hosted test (windows-latest) at -count=2, "
                  "the job that was red on c6, is green on c7 (P-CI7)")
        else:
            res = "implemented_unverified"
            ev = ("test/fault changed c6->c7 (dd8e9fd2, 88626fdd; D57(b)); its c7 runs are pending: PRE7 testpkgs "
                  "and hosted test (windows-latest) at -count=2 (P-CI7)")
        if rid == "1.5.19":
            ev += "; TestFaultActive_UnsetIsInertForAllElevenSites carries (internal/cli unchanged, C7-CARRY)"
        if rid == "1.17.14":
            ev += "; F-4's per-row mapping is still owed, and keeps the row partial after P-CI7"
        return res, ev
    if rid == "1.17.17":
        return "partial_verified", (
            "C7-CARRY+PRE7; core.Version and plugin.json read 0.3.0 in c7's code (runs/c7-carry-proof.txt); "
            "TestManifest_GoldenBytes, TestVersionLdflags and TestReleaseCheckVersion: " + pre7("internal") +
            "; release-check --tag v0.3.0 at the untagged version commit stops only at 'HEAD carries no tag' "
            "(w17-release), so the version agreement closes at the tag (P-TAG, C7.4)")
    if rid == "1.17.18":
        return "partial_verified", (
            "PRE7; re-judged on c7 (c6's red, RED-RELDRY, is a workflow cause fixed in c7's ci.yml, D57(a)); "
            "executed green on c7's code: release-check's own steps that PRE7 runs (gate: build, vet, "
            "fmt-check, gen-config-docs and gen-mcp-docs --check, lint subset; testpkgs: test/release with "
            "TestReleaseCheckDeterminismVersion); c7's release-dry-run declares QOMPACK_NONREFERENCE_DISK (D57(a)) and builds the six "
            "bundles at the release version (D57(c)); closes on a green release-check: hosted release-dry-run "
            "on c7 (P-CI7) and release-check --tag v0.3.0 on the reference host (P-REL7, docs/release.md "
            "section 1); actionlint and the goreleaser snapshot run only at the tag (P-TAG)")
    if rid == "1.17.19":
        return "partial_verified", (
            "P-CI7; re-judged on c7: ci.yml run 36981590450 on c7 (started when night.log records 'pushed "
            "verify/v6 d20309c0') had " + CI7_GREEN_N + " of its 21 jobs green at " + CI7_AT + " (" + CI7_GREEN +
            "); closes when the rest are green on c7: " + CI7_OPEN + ", where test (windows-latest) is "
            "RED-FAULT's job (D57(b)) and release-dry-run RED-RELDRY's (D57(a)), and branch protection is set "
            "on develop and main (C7.3)")
    if rid in DOCS_CHG:
        chg = "docs/ changed c6->c7 (wave 17 docs; runs/c7-carry-proof.txt)"
        if res6 in ("documented", "unknown", "unsupported"):
            return res6, f"{chg}; test/docs: {pre7('testpkgs')}; disposition as on c6" + lane
        if not PRE7_GREEN:
            return "implemented_unverified", f"{chg}; test/docs on c7 pending (PRE7 testpkgs, P-CI7)" + lane
        tail = "; on c6: " + ev6.split("; ", 1)[1] if res6 == "partial_verified" and "; " in ev6 else ""
        docs7 = (f"PRE7; {chg}; test/docs passes on Windows on c7's code (PRE7 testpkgs); hosted docs "
                 f"110757119402 (test/docs and the gen-*-docs checks), test (ubuntu-latest) 110757119503 and "
                 f"test (macos-latest) 110757119443 are green on c7 (ci.yml 36981590450, at {CI7_AT})")
        if res6 == "verified_in_target":
            return "verified_in_target", docs7 + lane
        return "partial_verified", docs7 + tail + lane
    if rid == "1.1.1":
        ident = (f"candidate 7 identity: verify/v6 {FREEZE7} (code {C7_CODE})" if FREEZE7
                 else f"candidate 7 is not frozen yet; its code is closeout/integration {C7_CODE}")
        return "documented", f"meta row: {ident}; no test exists by design"
    if rid == "1.1.20":
        ev = ("C7-CARRY; plugin.json changed only its version, and c7's source plugin.json is byte-identical to "
              "the plugin.json inside every frozen c6 bundle that PV and BUNDLES validated (w17-release); "
              "TestBundle_OnDiskMatchesGenerator and TestManifest_GoldenBytes: " + pre7("internal"))
        if BUNDLES7:
            ev += "; c7's bundles built and validated by the installed CLI (phase3/c7/host-validate.txt)"
        ev += "; hosted plugin-validate 110757119388 green on c7 (ci.yml 36981590450)"
        return res6, ev
    if rid == "1.17.1":
        ev = ("C7-CARRY by the byte proof (D57(c), w17-release): c6's two builds are byte-identical (91 files) and "
              "every c7 bin/ differs from c6's by the one unreferenced version literal")
        if BUNDLES7:
            ev += "; every c7 bin/ sha256 equals w17-release's independent build (phase3/c7/night.log)"
        return res6, ev
    note = "C7-CARRY"
    if rid in GUARD_ROWS:
        note += ("; test/guards' only change c6->c7 is nonrefdisk_test.go, not this row's tests; test/guards: "
                 + pre7("testpkgs"))
    elif "internal/core" in r[4]:
        note += ("; internal/core differs only in core.Version's default literal (D57(c)); "
                 + pre7("gate and internal"))
    return res6, note + lane

# The C5.2 rows without a per-row note, by what their benchmarks execute (runs/c52-executed-files.txt):
# dag (1.7.8, 1.7.10) executes no changed file; eval (1.2.12), sketch (1.3.7, 1.3.16), chunk and canon
# (1.4.14; symbols executes none) execute only pathstest/home.go's comment change.
C52_CLEAN = ("1.7.8", "1.7.10")
C52_COMMENT_ROWS = ("1.2.12", "1.3.7", "1.3.16", "1.4.14")

def c52_default(rid):
    if rid in C52_CLEAN:
        return C52_NOTE
    assert rid in C52_COMMENT_ROWS, f"{rid}: a C5.2 row with no executed-files classification"
    return C52_COMMENT

rows_out = [inv_hdr + ["c6_result", "c6_evidence", "c7_result", "c7_evidence"]]
counts = Counter()
counts7 = Counter()
pending_rows = []
for r, m in zip(inv[1:], mp[1:]):
    rid = r[0]
    assert rid == m[0], (rid, m[0])
    st = steps_of(m[3])
    codes, pend, red, live, none = [], [], [], [], False
    for kind, v in st:
        if kind == "green":
            codes.append(v)
        elif kind == "pending":
            pend.append(v)
        elif kind == "live":
            live.append(live_note(v))
        elif kind == "release":
            pass  # handled by the per-row overrides (1.17.7/8/9/17/18)
        elif kind == "none":
            none = True
        elif kind == "c51":
            assert rid in O, f"{rid}: a C5.1 row needs a per-row decision"
    if rid in ("1.5.19", "1.17.11", "1.17.14"):
        red.append("RED-FAULT")
    if rid in O and O[rid][0] == "failed":
        assert red or rid in ("1.17.18", "1.17.19"), rid
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
        if note is None and "C52" in seen:
            note = "C52 measured and reported (diagnostic); " + c52_default(rid)
    else:
        if red:
            res = "failed"
        elif none:
            raise SystemExit(f"{rid}: none-row without override")
        elif pend or live:
            res = "partial_verified" if seen else "implemented_unverified"
        else:
            res = "verified_in_target"
        note = None
        if "C52" in seen:
            note = "C52 measured and reported (diagnostic); " + c52_default(rid)
    parts = [p for p in [code_s] if p]
    if note:
        parts.append(note)
    ovn = (O[rid][1] or "") if rid in O else ""
    if live and "P-LIVE" not in ovn and res not in ("unknown", "unsupported", "documented"):
        parts.extend(x for x in live if x)
    elif live and "P-LIVE" not in ovn:
        parts.extend(x for x in live if x)
    if pend and not any(p in ovn for p in pend) and res not in ("unknown", "unsupported", "documented"):
        parts.append("pending " + ", ".join(pend))
    ev = "; ".join(parts)
    if res in ("partial_verified", "implemented_unverified", "unknown") and ("P-" in ev or "CARRY" in ev):
        pending_rows.append(rid)
    counts[res] += 1
    res7, ev7 = c7_of(rid, r, res, ev)
    counts7[res7] += 1
    rows_out.append(r[:7] + [res, ev, res7, ev7])

buf = io.StringIO()
w = csv.writer(buf, delimiter="\t", lineterminator="\n", quoting=csv.QUOTE_MINIMAL)
for row in rows_out:
    w.writerow(row)
out = buf.getvalue()
if "--write" in sys.argv:
    with open(INV, "w", encoding="utf-8", newline="") as f:
        f.write(out)
print("c6", dict(counts), sum(counts.values()))
print("c7", dict(counts7), sum(counts7.values()))
print("pending/carry rows:", len(pending_rows), " ".join(pending_rows))
for row in rows_out[1:]:
    if "-v" in sys.argv:
        print(row[0], row[7], "|", row[8][:400], "||", row[9], "|", row[10][:300])
