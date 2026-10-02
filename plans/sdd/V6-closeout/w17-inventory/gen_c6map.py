"""Writes plans/sdd/V6-closeout/inventory-c6-map.md from the c6 and c7 columns of inventory-current.tsv.

The prose is fixed here; the per-row tables are generated, so the map and the TSV cannot disagree.
Candidate 7's freeze state is read from dispose.py (FREEZE7, PRE7_GREEN, BUNDLES7), its single source.
"""
import csv, os, re
from collections import Counter

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", "..", ".."))
INV = os.path.join(ROOT, "plans/sdd/V6-remediation/inventory-current.tsv")
OUT = os.path.join(ROOT, "plans/sdd/V6-closeout/inventory-c6-map.md")

src = open(os.path.join(HERE, "dispose.py"), encoding="utf-8").read()
FREEZE7 = re.search(r'^FREEZE7 = (None|"[0-9a-f]+")', src, re.M).group(1).strip('"')
FREEZE7 = None if FREEZE7 == "None" else FREEZE7
PRE7_GREEN = re.search(r"^PRE7_GREEN = (True|False)", src, re.M).group(1) == "True"
BUNDLES7 = re.search(r"^BUNDLES7 = (True|False)", src, re.M).group(1) == "True"

rows = list(csv.reader(open(INV, encoding="utf-8", newline=""), delimiter="\t"))
hdr, rows = rows[0], rows[1:]
assert hdr[7:] == ["c6_result", "c6_evidence", "c7_result", "c7_evidence"], hdr
assert len(rows) == 304 and len({r[0] for r in rows}) == 304
count = Counter(r[7] for r in rows)
count7 = Counter(r[9] for r in rows)
order = ["verified_in_target", "partial_verified", "implemented_unverified", "failed", "unknown",
         "unsupported", "documented", "experimental"]


def cell(s):
    return s.replace("|", "/").replace("\n", " ")


def table(results):
    out = ["| id | current assertion | c6 result | evidence and what closes it |", "|---|---|---|---|"]
    for r in rows:
        if r[7] in results:
            out.append(f"| {r[0]} | {cell(r[2])[:90]} | `{r[7]}` | {cell(r[8])} |")
    return "\n".join(out)


def table7():
    out = ["| id | current assertion | c6 result | c7 result | candidate 7 evidence and what closes it |",
           "|---|---|---|---|---|"]
    for r in rows:
        plain = r[9] == r[7] and (r[10] == "C7-CARRY" or r[10].startswith("C7-CARRY; its P-LIVE"))
        if not plain:
            out.append(f"| {r[0]} | {cell(r[2])[:80]} | `{r[7]}` | `{r[9]}` | {cell(r[10])} |")
    return "\n".join(out)


if FREEZE7:
    IDENT7 = (f"- Candidate 7: `verify/v6` `{FREEZE7}`,\n"
              "  the `--no-ff` freeze merge of `closeout/integration` `b31d0753` (`phase3/c7/night.log`, its line\n"
              "  \"candidate 7 frozen at\"). Its pre-freeze check (gate, testpkgs, internal on `b31d0753`,\n"
              "  `phase3/c7/prefreeze/summary.log`) passed; the freeze runs only after it does. Its six bundles\n"
              "  (`qompack-bundles/c7`) were built and host-validated, every `bin/` sha256 equals w17-release's\n"
              "  independent build, and `verify/v6` was pushed (`night.log`).")
else:
    IDENT7 = ("- Candidate 7's code is `closeout/integration` `b31d0753` (D57(c)). Its freeze commit on `verify/v6`\n"
              "  had not appeared in `phase3/c7/night.log` when this map was written, so the candidate 7 column\n"
              "  is stated against `b31d0753`; the freeze is a `--no-ff` merge of that commit.")

HEAD = """# V6 inventory on candidates 6 and 7 (C6.2)

Workstream `w17-inventory`, 2026-10-02, on `closeout/w17-inventory` (cut from `closeout/integration`
`9a56b305`), revised by `w17b-inventory2` on `closeout/w17b-inventory2` (cut from `b31d0753`) to apply
D57 and add candidate 7. This map gives every one of the 304 inventory rows a disposition from candidate
6's evidence, and a candidate 7 disposition beside it. It supersedes the candidate 1 snapshot language of
`../V6-remediation/inventory-current-notes.md` and the `65bc8d7` gate list of
`../V6-remediation/current-candidate-gate-map.md`; both stay as history.

**What changed in `inventory-current.tsv`.** Four columns are appended to the seven that were there:
`c6_result` and `c6_evidence`, then `c7_result` and `c7_evidence`. No row was added, dropped or
renumbered (304 ids, one row each), and the seven original columns are byte-for-byte as they were:
`limitation` keeps the historical execution record, and its `result=` field is the pre-close-out state,
superseded by `c6_result` and `c7_result`. The table is written by `w17-inventory/dispose.py` from
`inventory-map.tsv` (D37's evidence-step map) and the per-row decisions in that script; this page is
written from the table by `w17-inventory/gen_c6map.py`.

The candidate 6 and 7 evidence directories (`phase3/c6/`, `phase3/c7/`) and `coordinator/` are on
`verify/v6`; citations of them resolve on `verify/v6` and on any branch merged with it.

## Candidate identity

- Candidate 6: `verify/v6` `99d0b18cf7d5991aa733e70ce52a542b80aa7710`, a `--no-ff` merge of
  `closeout/integration` `9a56b305` (`99d0b18^2`). Its tree differs from `9a56b305` only under `plans/`.
- Frozen bundles: `qompack-bundles/c6` (`bundle --archive --version 0.3.0`), windows-amd64 `BUNDLE.json`
  sha256 `a55f4c66...`, `bin/qompack.exe` sha256 `2de82d25...` (`phase3/c6/freeze.log`).
- The Windows pre-freeze check ran on integration `61b0cd66`. The only commit between it and `9a56b305`
  is the docs waiver `9a56b305`, which touches one report under `plans/`, and no file outside `plans/`
  differs between `61b0cd66` and the candidate: `git diff --stat 61b0cd66 99d0b18 -- . ':(exclude)plans'`
  is empty (`w17-inventory/runs/unchanged-proofs.txt`). The pre-freeze logs are therefore runs of the
  candidate's product and test code.
@@IDENT7@@
- Candidate 7 against candidate 6 (`w17-inventory/runs/c7-carry-proof.txt`): the only product change is
  core.Version's default literal and `plugin.json`'s version, 0.1.0 to 0.3.0. Besides that, test/fault
  (the claim-before-evidence fix `dd8e9fd2` and its read-order row `88626fdd`, D57(b)), test/guards'
  `nonrefdisk_test.go`, the golden `plugin.json`, `ci.yml`, `release.yml`, `.goreleaser.yaml`, `docs/`,
  `README.md` and `CHANGELOG.md` changed. Every other Go package and test package is byte-unchanged.
  Every `bin/` differs from candidate 6's by one unreferenced byte, the default literal (plus darwin/arm64's
  ad-hoc signature hash), and builds use `-buildvcs=false` (`w17-release/report.md`).

## Vocabulary and rule

The file's own vocabulary: `verified_in_target`, `partial_verified`, `implemented_unverified`, `failed`,
`unknown`, `unsupported`, `documented` (and `experimental` for the shipped-disabled switches below).

- `verified_in_target`: every evidence step the row names (D37 map) has an executed artifact on
  the candidate and it is green, or the row's benchmark ran on candidate 5 over files the benchmark
  executes that are byte-unchanged at the candidate (C52 below, D57(e)).
- `partial_verified`: every automated step the row names is green on the candidate, and a further part is
  pending (the live lane, C5.5) or has no possible artifact, or is a Linux fsync-bound half that is not
  verified in target by rule; the cell names the part and what closes it. On candidate 7 it also covers a
  row over changed code whose Windows re-run is green (PRE7) and whose named hosted run is pending (P-CI7).
- `implemented_unverified`: nothing the row needs has executed on the candidate yet.
- `failed`: an artifact of the candidate for the row's assertion is red. It stays on that candidate when
  the red later has a root cause and a fix: a later candidate that carries the fix is judged on its own runs.
- `unknown`: no step can produce an artifact (D37(c)); never counted as a pass.
- `unsupported`: retired by a recorded criterion change (E-1, F-3, D36). `documented`: a meta or prose
  row with no test by design.

Not verified in target, by rule: Linux fsync-bound rows (B-A, B-B) on the Docker Desktop container
(D53(b)); hosted-runner fsync figures are reports, never constants (Q1). Windows reference timing runs on
AC: a run on battery is invalid as a reference measurement, neither a pass nor a fail (D57(d)). Rows whose
subject is the Windows hot-path budget are judged by the designated quiet C5.1 run on AC (D57(e)).

**Candidate 7.** A row whose covered code is unchanged between `99d0b18c` and `b31d0753` carries its
candidate 6 result (`C7-CARRY`, D57(c)). A row over a changed path is judged on candidate 7's own runs:
test/fault (1.5.19, 1.17.11, 1.17.14), the docs that test/docs reads (1.18.1, 1.18.5-1.18.12), the
version (1.17.17), the release workflows (1.17.18, 1.17.19), and the manifest and bundles (1.1.20,
1.17.1, carried by the byte proof). The live lane and C5.5 run on candidate 7's frozen bundles (D57(c)),
so every P-LIVE and P-C55 part closes on candidate 7.

## Evidence codes

Paths are relative to `plans/sdd/V6-closeout/`. Job ids are GitHub Actions jobs of ci.yml run
`36955046276` or nightly run `36955043924`, both on head `99d0b18`.

| code | artifact | state |
|---|---|---|
| `WIN` | Windows whole tree: `phase3/c6/prefreeze/internal.log`, `testpkgs.log`, `integration.log` (tree `61b0cd66`, product-identical); `phase3/c6/p3-win-race.log` (`99d0b18`, `devtool test-race`, every non-e2e package); `phase3/c6/p3-win-e2e-timing.log` (`99d0b18`, test/e2e). The e2e pass ran 22:23-22:49 EDT on 10-01 and the AC was cut at 22:29:32 (`w17-x11win/report.md`), so its wall-clock rows are not reference measurements (D57(d)); its functional results stand | green except `RED-FAULT` |
| `LNX` | hosted `test (ubuntu-latest)` 110676058062: `-race`, whole tree but test/e2e, expected-failure reconciliation empty; `test (macos-latest)` 110676058123 likewise; container `phase3/c6/linux/cx-p3-p3-linux-tree-99d0b18-20261002T041733Z-artifacts` (non-root, `-race`, co-load declared: every package PASS, 0 fail) | green |
| `LE2E` | hosted `test-e2e` ubuntu 110676058080, macos 110676058201, windows 110676058155 (under `QOMPACK_NONREFERENCE_DISK`); container `phase3/c6/linux/cx-p3-p3-linux-e2e-timing-99d0b18-20261002T035853Z-artifacts` (335 pass, 3 skip, 1 fail: X11, fsync-bound, D53(b)) and the container `-race` lane `cx-p3-p3-linux-e2e-99d0b18-20261002T044416Z-artifacts` (336 pass, 3 skip, 0 fail; X11 reports under co-load, D39). The three skips are TestInstall_HostCLIInstallUpgradeUninstall and TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting (no claude CLI in the container) and TestE2E_RequiredProductChildRaceInstrumentation (its own lane) | green but Linux X11 (D53(b)) |
| `CHILD` | nightly `race-product-child` 110676054635 (ubuntu, `QOMPACK_REQUIRE_CHILD_RACE=1`); container `cx-p3-p3-linux-child-99d0b18-20261002T052037Z-artifacts` (8 of 8 pass, `CGO_ENABLED=1 GOFLAGS=-race`) | green |
| `WRACE` | `phase3/c6/p3-win-race.log`; hosted corroboration: nightly `race-windows` 110676054888 | green |
| `FUZZ` | nightly fuzz matrix, 25 jobs (`gh run view 36955043924 --json jobs`: 31 jobs, all success: 25 fuzz, 3 bench-deep, race-product-child, race-windows, replay-recorded) | green |
| `GATE` | `phase3/c6/prefreeze/gate.log` (build; vet for windows, linux, darwin; fmt-check; gen-config-docs and gen-mcp-docs `--check`; lint subset), green except runpatterns, which passes in `runpatterns-after-waiver.log` after the `9a56b305` waiver; hosted `verify` 110676058066 (fmt-check, full `devtool lint`, vet, build) and `lint-windows` 110676058082 | green |
| `DOCS` | hosted `docs` 110676058055 (gen-config-docs, gen-mcp-docs, gen-command-docs `--check`; test/docs) | green |
| `COVER` | hosted `cover` 110676057900 (`devtool cover`, OWNERS.tsv floors); `coveragefloors` in `gate.log` | green |
| `SEC` | hosted `security` 110676058089 (govulncheck; importgraph, testdeps, bindeps) | green |
| `XBUILD` | hosted `crossbuild` 110676058185 (`build-all`); the six-target bundle build in `freeze.log` | green |
| `PV` | hosted `plugin-validate` 110676058031; `phase3/c6/host-validate.txt` (`claude plugin validate --strict`, CLI 2.1.280, accepted) | green |
| `BUNDLES` | `phase3/c6/p3-bundleA.json`, `p3-bundleB.json` and `chain.log` ("bundles identical: 91 files", six targets) | green |
| `REPLAY` | hosted `replay-gate` 110676058077 (`devtool replay --phase 0 --ci` over the 24-session synthetic corpus, C5.3's comparison); nightly `replay-recorded` 110676054886 | green |
| `WTIME` | `phase3/c6/p3-win-timing.log` (D28 isolated timing pass, all twelve rows; 22:18:53-22:23:42 EDT on 10-01, on AC, before the AC cut at 22:29:32) | green |
| `LTIME` | `phase3/c6/linux/cx-p3-p3-linux-timing-99d0b18-20261002T035234Z-artifacts` (store 2, negknow 7, mcp 1, daemon 1 pass; TestIntegration_HotPathWarmWithRealResidentState fails, fsync-bound, D53(b)) | green but the hot-path row |
| `C52` | quiet C5.2 on candidate 5 `0d06ab12` (`phase3/c5/quiet/c52-win/`, `c52-linux/`, `c52-names.tsv`; ten ABBA rounds against `cf31e01`, D54), carried by D57(e) (next section) | green as a measurement |
| `C51` | quiet C5.1 on `99d0b18`, `phase3/c6/quiet/` (`quiet.sh` 05:25-05:32Z, `bench-hotpath --iterations 5000`, `quiet-run.txt`), Windows on AC. Windows (`c51-win.log`, `c51-win-hotpath.json`): B-A p99 30.72 ms and B-B 24.58 ms against 50 PASS, B-D 72.23 ms reported, B-E 170.75 ms (CPU 31.25 ms) against 2000 PASS, spawn floor p50 14.42 ms; B-F p99 73.73 ms against 250 PASS (`c51-win-bf.log`). Linux (`c51-linux/c51-linux.log`): B-A p99 65.54 ms and B-B 61.44 ms against 15 FAIL, 3591 of 5130 hot-path requests deferred to the client spool and 0 lost, fsync-bound, not verified in target (D53(b)); B-D 24.06 ms reported; B-E 384.11 ms (CPU 7.76 ms) PASS; B-F p99 20.48 ms PASS (`c51-linux/bf/.../test.jsonl`); quiet.sh warned the container was not idle after its run (1-min load 2.97). `homeguard-check.txt`: real home unchanged | Windows green; Linux B-A/B-B red (D53(b)) |
| `X11-E1` | D53(d)'s decisive experiment e1 on candidate 6's tree on AC (`phase3/c6/x11-e1/summary.txt`, `runs.txt`; script `w17-x11win/runs/x11-decisive-v2.sh`; D57(g)): the bare harness, TestV3_HotPathUnchangedWithLedgerResident (X11) and TestIntegration_HotPathWarmWithRealResidentState, each alone after 5 idle minutes, three rounds, every run VALID (AC 100 %, no power transition). All nine passed, 0 deferred, n=2064 in each. X11's no-ledger and ledger-resident arms: B-A p99 36.9 ms, B-B p99 22.5-24.6 ms against 50; the D42 pair passed 3/3 (hook_controlled_observed p50 5.120 ms in both arms, ceiling 6.400). Harness alone: B-A p99 28.7-32.8 ms, B-B 22.5. Spawn floor p50 13-22 ms | green (Windows) |
| `X11-BAT` | not evidence. X11's two candidate 6 Windows failures were its no-ledger arm, in both isolated executions (`p3-win-e2e-timing.log` B-A/B-B p99 81.9/57.3 ms, `p3-win-x11-alone.log` 98.3/81.9 ms; 593 deliveries deferred to the spool, spawn floor p50 29-30 ms; the test stops there, so the ledger phase is not reached). Both ran on battery (AC off 22:29:32 to 23:55:00 on 10-01, Kernel-Power event 105, `w17-x11win/report.md`). The pre-freeze `prefreeze/e2e.log` (X11's D42 pair at the ledger arm) and `prefreeze/hotpath.log` (the integration row, 593 deferred) reds on `61b0cd66` ran at below-normal priority beside the owner's evening load, not in an isolated timing pass (D28) | invalid as reference measurements (D57(d)), neither pass nor fail |
| `RED-FAULT` | hosted `test (windows-latest)` 110676058036: `-count=2`, the second pass of TestFault_Lifecycle/out_of_order_sessionend left a dangling retention root ("retention root ... names 158e3206c3f5, which is not held. Owner: internal/daemon"); the first pass, `WIN` and `LNX` passed. Root cause (D57(b)): test/fault's audit read the capture sidecars before `retention-roots.jsonl` beside a daemon still publishing a Stop, which writes the sidecar first and then its root; not a product defect. The test-only fix `dd8e9fd2` and its deterministic row `88626fdd` (TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence) are in candidate 7's code, not candidate 6's | red on c6; judged on c7 by PRE7 and P-CI7 |
| `RED-RELDRY` | hosted `release-dry-run` 110676058084: release-check passed version (skipped, no tag), fmt-check, lint, vet, build and test, then failed in `ci-local cover` on X11 at the hosted fsync tail (ubuntu, B-B p99 49 ms against 15) without the non-reference-disk declaration (D57(a)); build-all, generated docs, guards, govulncheck, licenses, determinism, rollback rehearsal, plugin-validate and marketplace were not reached. Candidate 7's release-dry-run and release.yml's tag-time job declare QOMPACK_NONREFERENCE_DISK; the delivery ledger, B-E_cpu and every structural check stay gated | red on c6; judged on c7 by P-CI7 and P-REL7 |
| `P-LIVE` | the live lane (D53(f); `coordinator/rerun-parts-c6.js`): C4.1 through a `qompack-windows-amd64` entry, C4.3, C4.4, C4.5, C4.6, C4.8 (upgrade from the c5 bundle), C4.9, C1.7 restore smoke, UAT-01, 03, 04, 05, 06, 09, 10, 12. D57(c) runs it on candidate 7's frozen bundles, so its verdicts close candidate 7's rows; candidate 6's rows keep `partial_verified` | pending |
| `P-C55` | C5.5 confirmatory evaluation (D53(g)), on candidate 7's frozen bundles (D57(c)) | pending |
| `CARRY-C4` | passed on candidate 4 `9f6a2fad` (`live/report-c4.md`) and not in the live lane: C4.2, UAT-02 (and UAT-07, 08, 11). Carry-forward note (D53(f)): 204 files changed since under internal/, cmd/ and plugin/ (`git diff --shortstat 9f6a2fad 99d0b18 -- internal cmd plugin`), 98 of them non-test product files, +5720/-654 (the same command with `':(exclude)*_test.go'`) | not re-run |
| `C7-CARRY` | candidate 6's disposition and evidence carry to candidate 7: the row's covered code is unchanged between `99d0b18c` and `b31d0753` (`w17-inventory/runs/c7-carry-proof.txt`), and bin/ differs by one unreferenced byte (D57(c), `w17-release/report.md`) | as on c6 |
| `PRE7` | candidate 7's pre-freeze check on `b31d0753`, `phase3/c7/prefreeze/` (`summary.log`, `gate.log`, `testpkgs.log`, `internal.log`): gate (build, vet on three OSes, fmt-check, gen-config-docs and gen-mcp-docs `--check`, lint subset with coverage floors), testpkgs (test/fault, security, platform, release, canary, dedup, replay, guards, docs, bench) and internal (`./internal/... ./tools/... ./cmd/...`), Windows, `-p 2 -count=1`; e2e and integration are carried from candidate 6 (`coordinator/c7-night.sh`) | @@PRE7STATE@@ |
| `P-CI7` | hosted ci.yml run `36981590450` on candidate 7's head `d20309c0` (and nightly `36981711009`), started when `coordinator/c7-night.sh` pushed `verify/v6` (`night.log`: "pushed verify/v6 d20309c0..."); in progress when this map was written; already green on c7 then: docs 110757119402, plugin-validate 110757119388, crossbuild 110757119552, security 110757119490, replay-gate 110757119428, test-e2e ubuntu 110757119506 and macos 110757119550 (the jobs the rows wait on, test on three OSes and release-dry-run, were still running) | pending |
| `P-C52R` | a quiet C5.2 re-run on candidate 7 of the benchmarks whose candidate 5 figure does not carry by D57(e): BenchmarkHookNoop_InProcess (1.1.27), BenchmarkTombstone (1.8.2), the three BenchmarkOnToolUse_* (1.8.13) and the five daemon scheduler benches (1.12.17, 1.16.11), or, for 1.8.2, 1.12.17 and 1.16.11, a coordinator ruling on `toolnames.go` (next section) | pending |
| `P-REL7` | `release-check --tag v0.3.0` on the reference host, required before the tag (`docs/release.md` section 1, D57(a)) | pending |
| `P-TAG` | the release tag (C7.4): release.yml's release-check `--tag`, actionlint and the goreleaser run | pending |

## C5.2 rows: which candidate 5 measurements carry (D57(e))

D57(e): a candidate 5 C5.2 measurement counts as `verified_in_target` on candidates 6 and 7 when every
file the benchmark executes is byte-unchanged, and the row's own cell names the carry. Every C5.2 row's
cell now says whether its figure carries, and names the proof file.

**The proof** is `w17-inventory/runs/c52-executed-files.txt`, written by `w17-inventory/c52exec.py`. It
runs each benchmark the C5.2 rows rest on once (`-benchtime=1x`, an execution trace, not a measurement)
under a set-mode coverage profile over every package of the module. It lists the files with an executed
statement, setup included, and intersects them with the non-test `.go` files that differ between candidate
5 (`0d06ab12`) and candidate 6 (`99d0b18c`). For each executed changed file it says whether the change is
comment-only and, if not, whether an executed block covers a changed line. The earlier per-file argument
(`runs/unchanged-proofs.txt`, `runs/c52-closure-c5-to-c6.txt`) had missed three of the files the trace
found: `internal/core/toolnames.go`, `internal/pluginmanifest/manifest.go` and the test helper
`internal/paths/pathstest/home.go`.

| benchmarks | rows | executed files changed c5->c6 | carries |
|---|---|---|---|
| obs, config, paths, symbols, dag, rules, skills, internal/scheduler | 1.1.16, 1.1.27 (config, paths), 1.4.14 (symbols), 1.7.8, 1.7.10, 1.11.16, 1.12.17 and 1.16.11 (scheduler) | none | yes |
| eval, sketch, chunk, canon, store, negknow, checkpoint | 1.2.12, 1.3.7, 1.3.16, 1.4.14, 1.6.19, 1.9.12, 1.10.17, 1.16.11 (store, checkpoint) | `internal/paths/pathstest/home.go` only, a test helper whose change is four comment lines (no `//go:` directive) | yes, on the comment-only reading below |
| internal/observer BenchmarkTombstone | 1.8.2 | `internal/core/toolnames.go` (gained CutHostPluginTool; no executed block covers a changed line) | no, pending P-C52R or a ruling |
| internal/daemon scheduler_bench_test.go (five benches) | 1.12.17, 1.16.11 (daemon) | `internal/core/toolnames.go`, as above | no, pending P-C52R or a ruling |
| internal/observer BenchmarkOnToolUse_* (three) | 1.8.13 | `internal/observer/observer.go` (sessionState gained two fields, a layout change on the measured path), `toolnames.go` | no, pending P-C52R |
| internal/cli BenchmarkHookNoop_InProcess | 1.1.27 (cli) | `internal/pluginmanifest/manifest.go` (executed changed lines 276 and 279, ForTarget's Description), `internal/cli/dispatch.go`, `doctor.go`, `internal/ipc/spool.go` | no, pending P-C52R |

**Two readings, for the owner.** (1) Comment-only: a file whose only change is comment lines compiles to
the same code, so wave 17b counts it as unchanged for D57(e); seven benchmark sets rest on that. If the
owner reads "byte-unchanged" literally instead, their rows move to `partial_verified`, pending P-C52R.
(2) `toolnames.go`: it gained a function that no benchmark executes, and every executed block is
unchanged. D57(e) as written asks for the file byte-unchanged, so 1.8.2, 1.12.17 and 1.16.11's daemon
part do not carry; a ruling that an appended, unexecuted function leaves D57(e) met would carry them.
1.8.13 and 1.1.27's cli bench do not carry under either reading.

Other notes:
- Taken over whole packages (`runs/c52-package-diffs-c5-to-c6.txt`), the non-test diff is empty for
  store, negknow, canon, chunk, symbols, dag, sketch, eval, config, paths (outside pathstest), scheduler,
  rules and skills, and not empty for obs (+93, two new files), cli (+82/-3), observer (+191/-9),
  checkpoint (+32) and daemon (+1320/-80). D57(e) judges executed files, not packages.
- internal/hostperm's `policy.go` changed, so its BenchmarkEvaluate figure does not carry; inventory-map.tsv
  lists it under 1.12.17 by a name collision, and 1.12.17 rests on the scheduler benches instead.
- From candidate 6 to candidate 7 no file any benchmark executes changed: the only product change is
  core.Version's default literal, in a file with no executable statement (`runs/c7-carry-proof.txt`).
"""

pre7_state = "green" if PRE7_GREEN else "running"
HEAD = (HEAD.replace("@@IDENT7@@", IDENT7)
        .replace("@@PRE7STATE@@", pre7_state))
assert "@@" not in HEAD

parts = [HEAD]
parts.append("## Counts\n")
parts.append("| result | candidate 6 rows | candidate 7 rows |\n|---|---|---|")
for k in order:
    if count.get(k) or count7.get(k):
        parts.append(f"| `{k}` | {count.get(k, 0)} | {count7.get(k, 0)} |")
parts.append(f"| total | {sum(count.values())} | {sum(count7.values())} |\n")
parts.append("## Candidate 6 rows that are not `verified_in_target`\n")
parts.append("Every row below names what is missing and what closes it. The other "
             f"{count['verified_in_target']} rows are verified in target on candidate 6; their cells in the TSV "
             "list the evidence codes.\n")
parts.append("### Failed on candidate 6\n")
parts.append(table({"failed"}) + "\n")
parts.append("### Pending: automated part green, live, evaluation or Linux fsync-bound part outstanding\n")
parts.append(table({"partial_verified", "implemented_unverified"}) + "\n")
parts.append("### No possible artifact, retired, or documented\n")
parts.append(table({"unknown", "unsupported", "documented"}) + "\n")
parts.append("## Candidate 7: rows that are not a plain carry\n")
parts.append("Every other row's `c7_evidence` is `C7-CARRY` (with, where the candidate 6 cell has a P-LIVE "
             "or P-C55 part, the note that it runs on candidate 7's frozen bundles), and its `c7_result` "
             "equals its `c6_result`.\n")
parts.append(table7() + "\n")

TAIL = """## The fourteen section 3 integration identifiers

Not renumbered into the 304. Mapped cases are the notes' section 4 table, re-checked on the candidate:
every named test exists at `99d0b18` (`TestPlatform_WindowsHookLauncherForms` is now
`TestPlatform_HookLauncherForms`; `TestV5_DegradedPassiveIsStillCorrect...` is
`TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent`). The candidate 7 column follows the
same rule as the rows: C7-CARRY unless the identifier's cases changed.

| section 3 | candidate 6 cases | c6 result | c7 result | evidence; what closes it |
|---|---|---|---|---|
| 3.1 PackagedBundleObservesARealSession | TestCanary_HostInventory, TestCanary_RegisterIsInternallyConsistent, TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent | `partial_verified` | `partial_verified` | WIN, LNX, LE2E; C7-CARRY; the packaged-bundle session is P-LIVE on candidate 7's frozen bundles (C4.1, sessions) |
| 3.2 UniversalLauncherPreservesHookSemantics | TestPlatform_HookLauncherForms, TestPlatform_PluginRootWithSpacesAndUnicode, TestPlatform_ReadOnlyBundleDir and its ReadOnly siblings, TestCanary_CompetingHooks, TestCanary_InvalidHookPayload | `verified_in_target` | `verified_in_target` | WIN, LNX (ubuntu, macos); C7-CARRY |
| 3.3 DocumentedCommandsRunAgainstShippedBundle | TestCanary_PluginValidate, TestGenCommandDocs_MatchesTheInstalledHelp | `partial_verified` | `partial_verified` | WIN, DOCS, PV (the frozen c6 bundle validated by the installed CLI; c7's plugin.json equals the one inside it byte for byte); C7-CARRY; commands run against the shipped bundle in a real session: P-LIVE (C4.5) |
| 3.4 EliminationStalenessSurvivesPackaging+MCP | TestV3_ObserverFileVersionsDriveEliminationStaleness, TestCanary_MCPLauncherDiscoversTools | `partial_verified` | `partial_verified` | WIN, LE2E, LNX; C7-CARRY; survival across an upgrade: P-LIVE (C4.8, UAT-12 upgrade leg) |
| 3.5 EphemeralRetrievalResultsAreEvictedFirst | TestPropose_ChoosesAtMostOneRepresentationPerItem; legacy TestV4_EphemeralRetrievalResultsRankFirstForEviction | `unsupported` | `unsupported` | native eviction retired (E-1); the replacement and the legacy row are green (WIN, LNX, LE2E); C7-CARRY |
| 3.6 FsckRepairsSeededCorruption | internal/cli fsck tests, TestFault_AuditSeesADeletedObjectUnderALiveIndex, TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence | `partial_verified` | `partial_verified` | WIN, LNX (these cases pass; RED-FAULT is another test/fault case); on c7 test/fault changed and its Windows run is @@PRE7FAULT@@, hosted P-CI7; publication accounting is detection, not recovery; product fsck beside a live daemon can report a transient dangling evidence retention root, a known limit for 0.3.0 (D57(b): stop the daemon and run fsck again); packaged fsck on a real store: P-LIVE (C1.7 restore smoke) |
| 3.7 DoctorAgreesWithStatus+Subsystems | TestDoctor_AgreesWithStatusOnModeAndProvenance, TestDoctor_ReportsCapabilityEvidenceWithoutInventingIt | `verified_in_target` | `verified_in_target` | WIN, LNX; C7-CARRY |
| 3.8 ConfigReferenceDescribesTheBinary | TestGenConfigDocs_LeavesMatchDefaultsOneToOne, TestUserGuideCoversEveryGeneratedCommandAndTool | `verified_in_target` | `@@R38@@` | WIN, LNX, GATE, DOCS on c6; on c7 docs/ changed, so the test/docs case re-runs: Windows @@PRE7DOCS@@, hosted docs 110757119402 green on c7, the Linux tree P-CI7; the generator case carries (tools/devtool and the generated references unchanged) |
| 3.9 InstallUpgradeUninstallByteIdentical | TestInstall_HostCLIInstallUpgradeUninstall, TestRollbackOrderIsFixed, TestRollbackRehearsal_BeforeAndAfterTheFirstNewFormatWrite, internal/store TestMaintenance_* | `partial_verified` | `partial_verified` | WIN, LE2E; C7-CARRY; TestInstall_HostCLIInstallUpgradeUninstall skips without the claude CLI (container, hosted); the installed install/upgrade/uninstall: P-LIVE (C4.1, C4.8, UAT-01, UAT-12) |
| 3.10 DegradedPassiveFromPackagedBundle | TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent, TestPlatform_UnknownSettingsVersion, TestPlatform_UnsupportedOptimizationsDisabled | `partial_verified` | `partial_verified` | WIN, LNX, LE2E; C7-CARRY; from the packaged bundle: P-LIVE (C4.9) |
| 3.11 NoSecretAndNoNetworkFullPackaged | TestSecurity_NoSecretReachesAnyDurableSurface, TestSecurity_TelemetryCannotBeTurnedOn, TestSecurity_MCPServerNeverImportsOSExec, TestV6_ArchivedReadRetainsItsAuthorizationBoundary, TestV6_HashAddressesDoNotBypassPathAuthorization | `partial_verified` | `partial_verified` | WIN, LNX, SEC; C7-CARRY; the historical archive-authorization FAIL is cleared for the automated half; the full packaged session: P-LIVE (C4.6, UAT-12) |
| 3.12 CheckpointToRehydrationRoundTripBundle | TestE2E_SessionStartCompactAfterFailedSummary, the TestE2E_Checkpoint* rows, TestFault_CheckpointDropsAnUnresolvablePointer | `partial_verified` | `partial_verified` | WIN, LE2E, CHILD; C7-CARRY except the test/fault case, which re-runs on c7 (Windows @@PRE7FAULT@@, hosted P-CI7); the bundle round trip in a real compaction: P-LIVE (C4.3, UAT-03, 04, 05) |
| 3.13 ReleaseArtifactsReproducible | TestAssembleBundle_Deterministic, TestAssembleBundle_ChecksumsFormat, TestWriteArchiveChecksums, TestReleaseCheckDeterminismVersion, TestBundle_OnDiskMatchesGenerator, TestCanary_PackagingShape | `verified_in_target` | `verified_in_target` | BUNDLES (two builds, six targets, byte-identical), WIN, LNX; C7-CARRY by the byte proof (D57(c))@@BUNDLES7@@; release-check's own determinism step has not run on a hosted runner yet (RED-RELDRY on c6; P-CI7 on c7) |
| 3.14 HotPathHoldsEverySubsystemResident | TestIntegration_HotPathWarmWithRealResidentState, TestIntegration_HotPathDegradesRatherThanBlocks, TestV3_HotPathUnchangedWithLedgerResident | `partial_verified` | `partial_verified` | Windows verified on AC: X11 and the integration row pass alone, three rounds each, 0 deferred, and the D42 pair passes 3/3 (X11-E1, D57(g)); the integration row passes isolated (WTIME); the quiet run passes (C51); TestIntegration_HotPathDegradesRatherThanBlocks is green (WIN, LNX). X11's c6 Windows reds ran on battery and are not counted (X11-BAT, D57(d)); hosted release-dry-run's X11 red is the hosted fsync tail (Q1, D57(a)). Open half: Linux, where the integration row (LTIME) and X11 (LE2E's container timing lane) fail fsync-bound, not verified in target (D53(b)). C7-CARRY |

## SP-19, SP-20 and SP-21 switches

Shipped defaults read from `internal/config/defaults.go` at the candidate (unchanged on candidate 7);
tests green on candidate 6 in WIN and LNX: TestSwitch_GatedCapabilitiesAreRefusedNotDisabled
(test/release), TestDoctor_EveryMigrationGateIsPendingInThisBuild (internal/cli),
TestGenConfigDocs_GatedSwitchesRenderFromSource (tools/devtool), TestCanary_CompactionBlocking and
TestCanary_NewResultReplacement (test/canary), TestSwitchIsOffByDefault and the internal/admission suite,
TestCapturePolicyToHookEnvelopeRoundTrip and TestCapturePolicyProducesTheCompleteFidelitySet
(test/integration), and the internal/state suite. Two key names in the notes' table were wrong and are
corrected here: the selection switches live under `runtime.selection`, not `runtime.pselection` or
`runtime.grammar`.

| switch | owner / gate | default at c6 and c7 | result | note |
|---|---|---|---|---|
| `runtime.migration.capture.rawEvidence` | SP-20 M1-01 | false | `experimental` | refused until its gate; recorded disabled, never passed |
| `runtime.migration.publication.durableFrontier` | SP-20 M1-02 | false | `experimental` | refused until its gate |
| `runtime.migration.reinjection.sessionStartCompact` | injection kill switch | true | `partial_verified` | the one enabled adapter: automated tests green; the live compaction round trip is P-LIVE (C4.3); kill switches passed live on candidate 3 only (C4.7, not in the live lane) |
| `runtime.migration.replacement.newResult` | SP-21 M4 admission | false | `experimental` | admission off, output passes through unmodified |
| `runtime.migration.compaction.automaticVeto` | SP-19 M0-03 | false | `unsupported` | the native veto is retired (E-1); refused |
| `runtime.migration.compaction.blockManualCompact` | section 12 | false | `unsupported` | hardwired off |
| `runtime.migration.experiments.enabled` | SP-15/16 | false | `experimental` | disabled |
| `runtime.selection.submodularEnabled` | SP-15 | false | `experimental` | disabled; refused without p-selection |
| `runtime.selection.loopWarningsEnabled` | SP-15 | false | `experimental` | disabled; a reload reports it has no effect in 0.3.0 (D51) |

**Delivery-journal rollover (SP-20, not a config key).** The notes and the `65bc8d7` gate map record
rollover as default-off and not accepted. That is superseded: D2 and C1.10 enabled it by default
(`enableDeliveryGenerations = true`, `internal/daemon/delivery_generation.go`), with D6 and D16's
residuals documented, and SP20-D4 is `fixed` in `plans/CARRIED-DEFECTS.tsv` with
TestCarriedDefect_SP20D4_CaptureContinuesPastTheOldEntryCapAcrossRestart green on candidate 6 (WIN, LNX).

## Corrections to earlier records

- The pre-existing reds the D37 map warned about, TestBudget_DetectorScan (1.9.12) and TestBudgetBF
  (1.13.16), pass on candidate 6 in the isolated timing passes on both OSes.
- 1.13.4 and 1.17.12's historical FAILs are cleared for their automated halves; their real-session
  halves remain pending.
- 1.14.5 is retired by D36(a), not pending: the three TestCheckpoint_* tests it names no longer exist.
- 1.17.19's four SP-17-era job names never existed in ci.yml; the row is judged on the candidate's
  hosted run and on branch protection.
- `inventory-map.tsv`'s 1.12.17 entry internal/hostperm BenchmarkEvaluate is a name collision (above).
- 1.17.4's `current_test_symbols` still name TestPlatform_WindowsHookLauncherForms; the row is judged on
  its replacement TestPlatform_HookLauncherForms, which now runs on every OS (`packaging/report.md`).
- symidx.py's `Prop` prefix matched the product function internal/analyzer Propose for 1.15.8
  (`runs/map-symbols-at-c6.txt`), a false positive; 1.15.8's executed evidence is
  TestSequitur_InvariantsHoldAfterEveryAppend (internal/grammar), green in WIN and LNX.

## Evidence folded in after the first pass, and the review rounds

The chain's container `-race` lanes landed green after the first disposition pass (`linux race exit=0
2026-10-02T05:25:05Z` in `phase3/c6/chain.log`) and are folded into `LNX`, `LE2E` and `CHILD` above;
they changed no row. Quiet C5.1 then landed (`quiet exit=1 2026-10-02T05:32:09Z`, the exit from the
Linux B-A/B-B rows, D53(b)) and is the `C51` code: 1.10.16 is verified in target on it.

Wave 17's review round made 1.5.12, 1.12.14, 1.17.6 and section 3.14 `failed` on X11's candidate 6
Windows reds. Wave 17b re-disposes them under D57. The two reds ran on battery, which makes them invalid
as reference measurements (D57(d)); rows whose subject is the Windows hot-path budget are judged by the
designated quiet C5.1 run on AC (D57(e)); and X11 passes on AC (X11-E1, D57(g)). Each of the four also
asserts the Linux fsync-bound B-A or B-B, which is not verified in target (D53(b)), so each is
`partial_verified`, with the Windows half verified. 1.17.6 is `partial_verified` rather than wave 17's
stated fallback `implemented_unverified`, because C51 has executed its B-A and B-E halves; its launcher
split has no possible artifact (D37(c)). The verification round found that the 1.5.12 cell claimed
"without and with the ledger" runs: both red artifacts were X11's no-ledger arm, and the cell now says
so. It also found that five C5.2 rows (1.1.16, 1.1.27, 1.10.17, 1.12.17, 1.16.11) did not name their
carry: every C5.2 row now names it and its proof files (D57(e)).

Still to land: the live lane and C5.5 on candidate 7's frozen bundles (P-LIVE, P-C55), hosted CI on
candidate 7 (P-CI7), the reference-host release-check (P-REL7) and the tag (P-TAG).
"""
r38 = next(r for r in rows if r[0] == "1.18.5")[9]  # 3.8's test/docs case moves with the docs rows
TAIL = (TAIL.replace("@@PRE7FAULT@@", "green (PRE7)" if PRE7_GREEN else "pending (PRE7)")
        .replace("@@PRE7DOCS@@", "green (PRE7)" if PRE7_GREEN else "pending (PRE7)")
        .replace("@@R38@@", r38)
        .replace("@@BUNDLES7@@", "; every c7 bin/ equals w17-release's independent build (`phase3/c7/night.log`)"
                 if BUNDLES7 else ""))
assert "@@" not in TAIL
parts.append(TAIL)
open(OUT, "w", encoding="utf-8", newline="\n").write("\n".join(parts))
print("wrote", OUT, "c6", dict(count), "c7", dict(count7))
