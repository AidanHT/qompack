"""C6.2: give every inventory row a candidate 6 disposition.

Reads plans/sdd/V6-remediation/inventory-current.tsv (304 rows) and plans/sdd/V6-closeout/
inventory-map.tsv (the D37 evidence-step map, same order) and appends two columns to the first:
c6_result (the file's own vocabulary) and c6_evidence (evidence codes, defined in
plans/sdd/V6-closeout/inventory-c6-map.md, plus the reason and what closes a pending row).
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

o("1.1.1", "documented", "meta row: candidate 6 identity is FREEZE (99d0b18, bundle and exe sha256); no test exists by design")
o("1.1.24", "verified_in_target", "test/guards green on c6 (WIN, LNX); F-1's historical guard-rename expectation is retired, D23 ratified TestGuard_EveryProductReadIsClassified as a standing guard")
o("1.1.27", "verified_in_target", "C52 measured config, paths and cli benches (reported, diagnostic); internal/core and tools/devtool have no benchmark (c52-names.tsv)")
o("1.1.28", "documented", "retired E-3: Qompack.md is v1.8 with its Revision log (v1.6 D5, v1.7 D36, v1.8 D41); no test by design", codes="")
o("1.2.8", "verified_in_target", "REPLAY: the replay-gate job log prints the breakpoint OPT line (4 markers, 161 candidates), a diagnostic only")
o("1.5.12", "failed", "RED-X11: X11's bench-hotpath runs (without and with the ledger) breach B-A/B-B on Windows in isolation on c6, and D28 names X11 in this row's map note; the quiet run passes on Windows (C51: B-A p99 30.72 ms, B-B 24.58 ms against 50) and so does the isolated hot-path row (WTIME); Linux B-A/B-B fail in LTIME and C51, fsync-bound, not verified in target (D53(b)); hosted timing is report-only (Q1). Clears when w17 x11win records a root cause and fix, or a disposition; it then returns to partial_verified, because the Linux half stays not verified in target", codes="WTIME+LTIME+C51")
o("1.5.15", "partial_verified", "producers and their tests green on c6; the real-observation half (F-2) needs a real session: CARRY-C4 (C4.2 passed on c4, not in the c6 lane); the c6 lane's status/doctor reads (P-LIVE) refresh it")
o("1.6.19", "verified_in_target", "C52 measured and reported (superseded-guarantee): PutBytes cold/warm stay 6-8x over the 3 ms/400 us budgets, SP06-D2 wontfix for 0.3.0 (D54)")
OBS_C52 = ("OnToolUse calls the changed collectThrash only when a Grammar is wired, and the benchmark harness "
           "wires none, so the measured path is unchanged; sessionState gained two fields (ThrashFloor, ReplyWarning); "
           "the carry rests on interpretation 2, executed files rather than the whole package "
           "(runs/c52-package-diffs-c5-to-c6.txt), which awaits a coordinator ruling")
o("1.8.13", "verified_in_target", "C52 measured and reported (superseded-guarantee): OnToolUse 256 KB Delta p99 59-74 ms against B-C's soft 50 ms, SP08-D1 wontfix for 0.3.0 (D54); " + OBS_C52)
o("1.8.2", "verified_in_target", "C52 measured and reported (diagnostic): BenchmarkTombstone runs the pure Tombstone function of tombstone.go, byte-unchanged c5->c6; internal/observer's product changes (observer.go, prompt.go, prompt_delivery.go, state.go) are not on its path; the carry rests on interpretation 2, executed files rather than the whole package (runs/c52-package-diffs-c5-to-c6.txt), which awaits a coordinator ruling")
o("1.9.12", "verified_in_target", "all seven TestBudget_* pass in WTIME and LTIME on c6 (TestBudget_DetectorScan, once a pre-existing red, passes on both); negknow Open 61.6/77.0 ms against 300 ms in C52 (SP09-D1 fixed, D54)")
o("1.10.16", "verified_in_target", "quiet B-E passes on both OSes (C51: Windows p99 170.75 ms, CPU 31.25 ms; Linux 384.11 ms, CPU 7.76 ms; against 2000 ms); also inside X11's isolated runs on Windows (p3-win-e2e-timing.log 825 ms, p3-win-x11-alone.log 786 ms)", codes="C51")
o("1.10.18", "unknown", "D37(c): no step runs the two replay --phase 4 runs with the frontier toggled; not verified in target, no new harness before release", codes="")
o("1.11.16", "partial_verified", "C52 measured rules BenchmarkPathScoped and skills BenchmarkIndex; internal/rehydrate has no benchmark, so the rehydrate share of L5 latency has no artifact")
o("1.12.14", "failed", "structural half TestSchedulerNotOnHotPath green (WIN, LNX); the B-A half has a red c6 artifact: RED-X11's bench-hotpath runs breach B-A on Windows in isolation, while the quiet run passes (C51: B-A p99 30.72 ms against 50); Linux B-A is not verified in target (D53(b)). Clears with RED-X11's root cause and fix or disposition, then partial_verified (the Linux half)", codes="WIN+LNX+C51")
o("1.12.17", "verified_in_target", "C52 measured the internal/scheduler benches and the daemon's scheduler_bench_test.go benches (covered code unchanged c5->c6); the map's internal/hostperm BenchmarkEvaluate is a name collision, not a scheduler bench, and its c5 figure does not carry (hostperm/policy.go changed)")
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
o("1.15.14", "unknown", "C52: internal/analyzer and internal/grammar have no benchmark (c52-names.tsv), so the row has nothing to measure; not verified in target", codes="")
o("1.16.5", "unknown", "D37(c): no test or step computes the warm-vs-cold O4 delta; not verified in target, no new harness before release", codes="")
o("1.16.10", "unknown", "D37(c): TestPrefixReorderingNotAttempted is absent and no declaration exists (no docs/adr/0016; no file under docs/ or Qompack.md declares prefix-reorder non-delivery); not verified in target", codes="")
o("1.16.11", "verified_in_target", "C52 measured the phase-7 store, checkpoint and scheduler families (covered code unchanged c5->c6)")
o("1.17.1", "verified_in_target", "two six-target bundle builds byte-identical on c6 (BUNDLES), assembler/archive determinism units green (WIN)")
o("1.17.3", "unknown", "D37(c): no binary-size check exists; diagnostic only, the frozen c6 binaries measure 9.29-10.49 MB (windows-amd64 qompack.exe 10,416,640 B); not verified in target", codes="BUNDLES")
o("1.17.4", "verified_in_target", "judged on TestPlatform_HookLauncherForms, renamed from TestPlatform_WindowsHookLauncherForms and now run on every OS (D37(b), inventory-map.tsv), with TestPlatform_PluginRootWithSpacesAndUnicode: green in WIN and LNX")
o("1.17.5", "unknown", "D37(c): bench-hotpath has no --bundle universal/native mode, so the launcher-overhead split cannot run as written; not verified in target. Diagnostic only: quiet B-D p99 72.23 ms on Windows, 24.06 ms on Linux, reported without a limit (C51)", codes="")
o("1.17.6", "failed", "the launcher split cannot run (see 1.17.5, D37(c)); the B-A half has a red c6 artifact (RED-X11 breaches B-A on Windows in isolation) though the quiet run passes B-A and B-E on Windows (C51: 30.72 ms against 50, 170.75 ms against 2000); Linux B-A is not verified in target (D53(b)), Linux B-E passes (384.11 ms). Clears with RED-X11's root cause and fix or disposition, then implemented_unverified (the split stays unrunnable)", codes="C51")
o("1.17.7", "partial_verified", codes="WIN+LE2E", note="backup/maintenance units and test/e2e install tests green (WIN, LE2E); TestInstall_HostCLIInstallUpgradeUninstall skips without the claude CLI (container and hosted runs), RED-RELDRY stopped release-check before its rollback-rehearsal step (the step's two tests pass in the e2e lanes)")
o("1.17.8", "partial_verified", codes="WIN+LE2E", note="maintenance/backup units green (WIN); TestRollbackRehearsal_BeforeAndAfterTheFirstNewFormatWrite passes in the e2e lanes (WIN, LE2E); release-check's rollback step not reached (RED-RELDRY); the installed upgrade/uninstall is pending")
o("1.17.9", "partial_verified", codes="WIN+LNX", note="schema-bump units green (WIN, LNX); TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting skips without the claude CLI (container, hosted) and the Windows chain log is non-verbose, so its execution on c6 is unproven; the old-released-reader matrix has no artifact (D37 map); C4.8 upgrade from the c5 bundle is pending")
o("1.17.10", "partial_verified", "test/platform green on windows/amd64 (WIN; also hosted test windows-latest, where only test/fault was red), linux/amd64 (LNX) and darwin/arm64 (hosted macos-latest); windows/arm64, linux/arm64 and darwin/amd64 are cross-compile only (XBUILD), unknown; C4.11 Linux installed host: model sessions unknown (D34(c)), no-model half not run on c6")
o("1.5.19", "failed", "RED-FAULT: the row names the whole test/fault package, which has one red on c6 (hosted windows-latest -count=2, TestFault_Lifecycle/out_of_order_sessionend); the reported failure is a dangling retention root, not a hook exit; TestFaultActive_UnsetIsInertForAllElevenSites green (WIN, LNX); clears with RED-FAULT's disposition")
o("1.17.11", "failed", "RED-FAULT: TestFault_Lifecycle/out_of_order_sessionend left a dangling retention root on hosted windows-latest's second -count=2 pass on c6 (passed in WIN and LNX); open with w17 ci; closes on a root cause and fix, or a recorded disposition")
o("1.17.12", "partial_verified", "the historical FAIL is cleared for the automated half: test/security (incl. TestV6_ArchivedReadRetainsItsAuthorizationBoundary) and test/guards pass on c6 on Windows, Linux and macOS; the real-session privacy half is pending")
o("1.17.13", "verified_in_target", "govulncheck clean in hosted security (SEC); gosec inside golangci-lint clean (GATE)", codes="SEC+GATE")
o("1.17.14", "failed", "RED-FAULT: the test/fault suite that replaces the section 12.3 rows has one red on c6 (hosted windows-latest -count=2); otherwise green in WIN and LNX; F-4's per-row mapping is still owed")
o("1.17.15", "partial_verified", "fsck and publication-accounting tests green (WIN, LNX), detection not recovery; the live restore smoke C1.7 is pending P-LIVE")
o("1.17.16", "partial_verified", "doctor tests green (WIN, LNX); D34(e)'s self-test fix is on c6; the real-session half passed on c4 only")
o("1.17.17", "partial_verified", "version units green (WIN); release-check's version agreement SKIPPED without a tag, and core.Version and plugin.json still read 0.1.0 at c6 while the bundles are stamped 0.3.0 by bundle --version: the agreement half closes at C7.1/C7.4 (release-check --tag v0.3.0)")
o("1.17.18", "failed", "RED-RELDRY: ci.yml release-dry-run on c6 failed in release-check's ci-local cover step (X11 on ubuntu without the non-reference-disk declaration); build-all, generated docs, guards, licenses, determinism, rollback rehearsal, plugin-validate and marketplace were not reached; actionlint and the goreleaser snapshot run only on a tag; open with w17 release/ci")
o("1.17.19", "failed", "ci.yml 36955046276 on c6: 19 of 21 jobs green, release-dry-run (RED-RELDRY) and test (windows-latest) (RED-FAULT) red; the four SP-17-era jobs (package-gate, platform-matrix, fault-gate, install-gate) were never created, their scope runs inside test/test-e2e/crossbuild/plugin-validate/release-dry-run; develop has no branch protection (gh api: Branch not protected) and main is absent on origin, which C7.3 closes", codes="")
o("1.17.20", "verified_in_target", "checksums.txt over the six archives in BUNDLES and freeze.log; checksum-format units green (WIN)")
o("1.18.1", "partial_verified", "TestOwnedDocsExist green (WIN, LNX, DOCS); the historical count assertions (IsTwentyOne, HasNoDuplicates, StartWithH1) have no current test (F-5)")
o("1.18.3", "partial_verified", "replacement TestGenConfigDocs_LeavesMatchDefaultsOneToOne green (WIN, GATE, DOCS); it pins leaves to defaults, not documented ranges to Validate() (F-5)")
o("1.18.4", "partial_verified", "replacement TestGenConfigDocs_GatedSwitchesRenderFromSource green (WIN, DOCS); the runtime-section additivity marker has no standalone test (F-5)")
o("1.18.8", "partial_verified", "TestADRIndexListsEveryADR green (WIN, LNX, DOCS); the per-ADR shape and decision-id checks have no current test (F-5)")
o("1.18.9", "documented", "F-5: the verbatim ten-invariants claim is retired; 00-ARCHITECTURE.md carries the invariants, only ADR-index presence is executed (TestADRIndexListsEveryADR green on c6)")
o("1.18.11", "partial_verified", "UAT shape tests green (WIN, LNX, DOCS); no test quotes the phase exit criteria")
o("1.18.12", "unknown", "D3: the human half has no possible artifact; the agent-run form is the c6 live lane UAT-01, 03, 04, 05, 06, 09, 10, 12 (P-LIVE) plus earlier-candidate UAT-02/07/08/11 results CARRY-C4; TestUATUnexecutedRowsSayUnverified green (DOCS)")
o("1.18.13", "verified_in_target", "generator determinism tests green (WIN, GATE, DOCS); B-DOC is a diagnostic with no benchmark (E-2)")

rows_out = [inv_hdr + ["c6_result", "c6_evidence"]]
counts = Counter()
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
    if rid in ("1.5.12", "1.12.14", "1.17.6"):
        red.append("RED-X11")
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
            note = "C52 measured and reported (diagnostic); covered code unchanged c5->c6"
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
            note = "C52 measured and reported (diagnostic); covered code unchanged c5->c6"
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
    rows_out.append(r[:7] + [res, ev])

buf = io.StringIO()
w = csv.writer(buf, delimiter="\t", lineterminator="\n", quoting=csv.QUOTE_MINIMAL)
for row in rows_out:
    w.writerow(row)
out = buf.getvalue()
if "--write" in sys.argv:
    with open(INV, "w", encoding="utf-8", newline="") as f:
        f.write(out)
print(dict(counts), sum(counts.values()))
print("pending/carry rows:", len(pending_rows), " ".join(pending_rows))
for row in rows_out[1:]:
    if "-v" in sys.argv:
        print(row[0], row[7], "|", row[8][:400])
