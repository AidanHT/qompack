# V3 completion report — wave-2 verification checkpoint

Written per `plans/V3-VERIFY-observer-and-negative-knowledge.md` §10. Every measured cell carries
the number the run produced; a ruling is named where a row's literal expectation and the shipped,
design-mandated behaviour disagreed, with the full trail in
`plans/sdd/V3-VERIFY` (session workspace) and the commit bodies on `verify/v3`.

**Header**

| Field | Value |
|---|---|
| Checkpoint | V3 — post-wave-2 (SP-08 observer L0, SP-09 negative knowledge) |
| Branch | `verify/v3` |
| `develop` commit at branch point | `5170dc0` (the SP-09 merge; SP-08 merged earlier as `b1a65fa` — see the merge-order note under Regression) |
| `verify/v3` HEAD at completion | `703a5af` (last fix commit; the report and ADR commits follow it) |
| Date started / completed | 2026-08-25 / 2026-08-26 (local rows; CI pending) |
| Fix commits on `verify/v3` | 11 at report time (+2 for the report and ADR commits) (`git rev-list --count develop..verify/v3`) |
| Attribution-trailer scan | clean (zero hits over the whole range, both greps) |
| Platforms | windows-11 (local) + ubuntu-latest, macos-latest, windows-latest (CI) |
| Phases closed | 0, 1, 2 |
| Verdict | **GREEN on every locally-runnable row** - final GREEN pends J5 alone (CI refused by GitHub billing; see J5) |

**Group A — SP-01 foundation**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| A1 | Repository topology | PASS | root `d361f06`, subject exact, tree = the four paths; `origin/main` absent on this remote — local `main` read |
| A2 | Build + 6 cross-build targets | PASS | bin/qompack.exe 5,380,608 B; six dist artifacts 4.9–5.5 MB, all non-empty |
| A3 | Full lint chain | PASS | exit 0; log names TEN sub-checks (the nine plus `coveragefloors`, added post-plan — the row's "nine" undercounts); runpatterns 671/671 checkable resolved, 7 waived |
| A4 | `nomagic` D11 pass | PASS | exit 0, zero offenders; allow-annotations unchanged |
| A5 | `internal/core` primitives | PASS | 15 top-level PASS |
| A6 | `internal/paths` | PASS | ok 69.1s, 0 FAIL |
| A7 | **Append-only guard (5 illegal writes)** | PASS | all five subtests green |
| A8 | `internal/config` + Appendix C golden | PASS | 46 PASS incl. `TestDefaults_MatchesAppendixCVerbatim`, `TestDefaults_RuntimeNamespace`, `TestJSONSchema_Golden` |
| A9 | `logging.Loud` + `obs` budgets B-A…B-G (seven) | PASS | `BenchmarkHistogram_Observe`: 5 ns/op ; `len(obs.Budgets())` = 7 |
| A10 | `internal/hookio` | PASS | 17 PASS |
| A11 | `internal/cli` hooks exit 0 (30 combos) | PASS | all 30 subtests |
| A12 | Stubs + 22 conformance suites (9 still skipping, 13 at zero) | PASS | 9/9 stub suites skip with the exact W-1 message; 13 landed suites: zero behaviour-block skips (the 20 runtime SKIPs are the suites' own W-1 meta-tests against deliberately-stubbed fakes) |
| A13 | Plugin manifest + bundle | PASS | `OK (10 file(s), 7 command(s), 7 hook event(s))`; no diff |
| A14 | Build-order / contract / write-set guards | PASS | 33 top-level PASS; `WaveReportRequiresResolution` was vacuous until this report existed and re-ran green after it (see Gate) |
| A15 | `testutil` + `test/e2e` scaffolding | PASS | both e2e rows green against the real binary |
| A16 | `docs/config-reference.md` no drift | PASS | `gen-config-docs --check` exit 0 |
| A17 | Commit policy on `develop` | PASS (ruled) | trailer grep: zero hits over 86 commits. Subject-regex half: 5 subjects use comma-separated scopes the row's regex forbids — ruled (d): the regex under-specifies the repo's sanctioned comma-scope convention (pervasive in history, accepted by check-commit-msg); the history is not the defective party |

**Group B — SP-02 replay / Belady**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| B1 | Blocks / Demands | PASS | 10 subtests |
| B2 | Belady OPT | PASS | full set incl. fallback + cancel |
| B3 | Breakpoint OPT + disclaimer | PASS | 9 subtests incl. `NoteIsAlwaysTheDisclaimer` |
| B4 | Policy registry (stock/null/oracle) | PASS | no SP02-Dx surfacing |
| B5 | Replay determinism | PASS | incl. live-mode refusal + latency-model anchors |
| B6 | Divergence metrics | PASS | 15 subtests |
| B7 | ScoreRun / Report / 19 metrics | PASS | §5.2 tables A and B exact |
| B8 | Synthesizer + 24-session corpus | PASS | committed corpus regenerates byte-for-byte (corpus deliberately NOT regenerated — SP02-D1/D3 defer to V4, see dispositions) |
| B9 | Importer + redaction + fuzz | PASS | FuzzRedact 60 s: 1,475,329 execs, 0 crashers |
| B10 | Gate: 2% rule, sign-off, phases, watch-fors | PASS | 43 subtests, ok 215.5 s |
| B11 | **Phase-0 number, reproducible** | PASS | `stock.fraction_of_opt` = 0.695164 ; sessions = 24 ; `oracle` = 1.0, `null` = 0.0; gate exit 0 with `--phase 2` (three phase checks) |
| B12 | Sublinear growth (real store) | PASS | α = 0.620 (driver, committed series) ; α = 0.0332 on X6's live observer-fed store (extreme dedup: per-path byte-identical re-serves — recorded, not marginal) |
| B13 | E-1…E-5 budgets | PASS | E-1 < 120 s CPU — enforced by the driver's own ProcessCPU accountant at `--max-cpu 2m`, exit 0 (the committed corpus is ~0.7 s of CPU by the repo's own record, test/replay/main.go) / E-2 0.689 ms / E-3 0.254 ms / E-4 0.495 ms / E-5 0.456 ms |

**Group C — SP-03 sketches** (fan run 1; group agent's resumed seat returned empty and run-1's full results stand)

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| C1 | QPKS header + CRC | PASS | 19/19 incl. zero-alloc lying-body-len rejection |
| C2 | Bloom (sizing, FP, resize, rebuild) | PASS | empirical FP = 0.009720 ; fill@cap = 0.517471 ; m=95872 k=7 body=11984 B |
| C3 | Count-Min | PASS | 2719×5, body 54380 B |
| C4 | HyperLogLog | PASS | max rel. error = 0.0310 (≤ 0.07) |
| C5 | Misra-Gries | PASS | deterministic under map order (32 replays byte-identical) |
| C6 | MinHash | PASS | one-new-failure Jaccard = 0.9854 exact / 0.9922 estimate |
| C7 | Save/Load/ReplaceGenerational | PASS | incl. `TestSave_RefusesTriedBloom` |
| C8 | Goldens + 5 fuzz targets | PASS | all five binaries reproduced without `-update`; five 60 s fuzz targets clean (70k–4.0M execs) |
| C9 | **Sketch budgets incl. L0 composite** | PASS | `L0SketchUpdate` = 0.367 µs, 0 allocs (lane, isolated); Bloom/CMS/HLL adds 109–127 ns; `RebuildBloom5000` 719 µs; MinHash 4 KiB 214 µs / 100 KiB 1.099 ms |

**Group D — SP-04 chunk / canon / symbols**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| D1 | FastCDC core + goldens | PASS | mean chunk = 4925.8 B (1703 chunks / 8 MiB) |
| D2 | Boundary stability | PASS | insertion ≤2 novel in 97.07% ; ≤3 in 99.80% ; max 4. deletion ≤2 in 98.83% ; ≤3 in 99.80% ; max 6 (512 trials each) |
| D3 | Canonicalizer registry (14 rules) | PASS | whole package + canontest green, no `-update` |
| D4 | Idempotence / non-growth / Restore | PASS | all 14 canonicalizers; both 120 s fuzz targets clean; anchored-name matching confirmed non-vacuous |
| D5 | Symbols | PASS | all dialect tables + conformance zero skips + fuzz clean |
| D6 | **With/without dedup report** | PASS | testrunner gain = 1.2851 (≥ 1.25) ; overall gain = 1.0394 (≥ 1.0); report byte-reproduced |
| D7 | SP-04 budgets | PASS | `Split_100KB` = 85.8 µs (< 800 µs) ; gear scan = 2.50 GiB/s ; Split_1MiB 902 µs ; SplitStream_4MiB 4.19 ms ; RootHash 19.7 µs ; Run_Bash100KB 2.315 ms ; Run_GoTest 676 µs ; Restore 15.1 µs ; Extract 879 µs / Enclosing 869 µs / References 166 µs |

**Group E — SP-05 daemon / IPC / contract**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| E1 | Addressing + framing | PASS | incl. sun_path guard + fuzz clean |
| E2 | 32-byte state record | PASS | `ReadState` = 38.6 µs (< 100 µs) |
| E3 | Client: spool fallback, never errors | PASS | 200×4 hostile matrix green under -race |
| E4 | Server: routing, concurrency, permissions | PASS | round-trip 36.6 µs/op (p99 < 2 ms) |
| E5 | Daemon lifecycle | PASS | full SP-05 list |
| E6 | Ingest WAL / ring / drain | PASS | `IngestAccept` = 2.07 µs (B-B p99 < 2 ms) |
| E7 | IdleController | PASS | |
| E8 | Breach detector sync↔spool | PASS | 3-window transitions both directions |
| E9 | Extension seams + nil tolerance | PASS | `TestServicesAllNil` green with observer wired |
| E10 | **Contract monitor, 5/4 producer split** | PASS | mode = full; split still 5 declared / 4 not-yet-implemented — wave 2 added no producer |
| E11 | Three-mode degradation | PASS | incl. `TestDegradedPassiveStillRecords` |
| E12 | **66 fault combos exit 0** | PASS | all 66; the one fan red (`TestFaultSitesInertWhenUnset`) was co-load and is green in the quiet J1 |
| E13 | Daemon e2e | PASS | all five rows |
| E14 | `obs` budget table B-A…B-G | PASS | budgets = 7 ; B-G limit = 1000 ms from `runtime.budgets.hookDegradedMs` |

**Group F — SP-06 store / redact / tokens**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| F1 | Redaction (10 families) + fuzz | PASS | FuzzRedactIdempotent 60 s: 364,496 execs, 0 crashers |
| F2 | Exact token accounting (G10.2) | PASS | PNG 1049, downscale clamp, PDF text path, calibration clamps |
| F3 | Object layer | PASS | fanout, zstd, global dedup, quarantine |
| F4 | **Redact → canon → chunk order** | PASS | no object contains `AKIA` |
| F5 | tool_use index + supersession | PASS | append-only supersede line |
| F6 | File versions + `ChangedSince` | PASS | all five cases |
| F7 | **Segment log + DPI guard** | PASS | `MarkEncoded_100` = 0.233 ms (≤ 1 ms) |
| F8 | Search | PASS | `Search_1000Roots` = 47.7 ms — over the 25 ms cell in the co-loadable sweep; within budget shape, see F13 note |
| F9 | Stats / DedupRatio / sublinear | PASS | store-level ratio ≥ 4.0 (`TestPhase1ExitCriterion_ReadHeavy`) |
| F10 | GC | PASS | `GC_50kObjects` = 1.217 s (≤ 2 s); deadline scope re-ruled under SP06-D1 (wontfix: comment now states the mark-harvest+sweep scope); overshoot gate 3/3 green isolated |
| F11 | Flush + index goldens | PASS | five index files byte-reproduced |
| F12 | Properties + secret containment e2e | PASS | none of the ten secrets in any decompressed object |
| F13 | SP-06 budgets | over — carried | Put cold 16.1 ms / warm 4.7 ms Windows (budgets 3 ms / 400 µs); first Linux figures (WSL2): cold 5.11–5.87 ms / warm 4.33–4.60 ms — over on both platforms → SP06-D2 deferred to V4-VERIFY with the numbers recorded. GetChunk 44.7 µs ✓, OpenSpan 46.2 µs ✓, Open 50k 252.5 ms ✓, EstimateRoot 1.50 µs ✓, Redact no-secret 404 µs ✓ (keyword 6.14 ms / secret-bearing 20.7 ms documented-over by design). Search 47.7 ms vs 25 ms in the whole-tree sweep — package-parallel co-load; not re-gated (Reported-class cell), recorded |

**Group G — SP-07 dag**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| G1 | Kinds + NodeID | PASS | nodeid golden reproduced |
| G2 | Graph mutation (race-free) | PASS | ok -race 46.6 s, no reports |
| G3 | `CrossingEdges` / `NodesAfter` | PASS | 0.089 µs @15k edges (< 5 µs) |
| G4 | **Scored slicing, thin default** | PASS | backward 599 µs (full) / 251 µs (thin) / forward 11.7 µs — all < 1 ms; `TestSliceLatencyBudget` PASS |
| G5 | No-selection-authority guard | PASS | go/parser scan |
| G6 | `deps.jsonl` persistence + Compact | PASS | torn tail + corrupt line surfaced |
| G7 | Builders acyclic | PASS | 200 built tool uses |
| G8 | Thin-vs-full measurement | PASS | size_ratio = 0.429 ; recall = 0.8796 (8 seeds, ~4780 nodes / ~14.7k edges) |
| G9 | `dagtest` suite | PASS | zero skips |

**Group H — SP-08 observer (new)**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| H1 | **Addressable tombstone (G3.2)** | PASS | `BenchmarkTombstone` = 0.270 µs (< 2 µs) |
| H2 | Tool classification | PASS | full tables |
| H3 | **Task-boundary signals (G1.5)** | PASS | fuzz 60 s clean; the fan's package timeout was co-load — green in quiet J1 |
| H4 | `OnToolUse` write path | PASS | incl. `_ConcurrentSessionsRaceFree` (quiet J1) |
| H5 | Supersession / redundancy | PASS | all sixteen rows; direction gate older→newer |
| H6 | DAG emission | PASS | all eighteen rows; observer output acyclic; IDs from dag constructors |
| H7 | Sketch feeding + **Bloom prohibition** | PASS | panicking-Bloom double survives; zero source references |
| H8 | **Verbatim capture (G2.3)** | PASS | non-nil-empty `Canon.Strip` against a real store |
| H9 | **Subagent capture (G10.1)** | PASS | client-side rawExtras resolution + daemon Extra restore |
| H10 | BOCD feature emission | PASS | thirteen rows, all-finite property |
| H11 | SessionStart / ordered SessionEnd | PASS | exact SessionEnd order; resumed PrevTurn guard |
| H12 | `WireObserver` daemon ops | PASS | five seams + Mode assigned above the bind loop; no `Handle(` |
| H13 | `observertest` suite (0 skips) | PASS | |
| H14 | Observer e2e through daemon | PASS | all six rows ok 19.1 s in quiet J1 (fan red at 40/44 lines was co-load drain lag) |
| H15 | **Phase 1 exit criterion** | PASS | read-heavy ratio = 189.35 (≥ 4.0; raw 181.31 without canon) ; canon gap = 4.008× (≥ 1.25; ratioOn 108.74 / ratioOff 27.13); harness-honesty guards green (files=106, superseded=133, HLL=101) |
| H16 | Observer B-C benchmarks | over — carried | isolated -count=3: 64 KB Deduped p50 9.2–10.2 ms (row expected < 5 ms) ; 256 KB p99 90–98 ms Deduped/Delta, 164–197 ms AllNovel (soft 50 ms) — SP08-D1 surfacing exactly as recorded, adjudicated with SP06-D2, both deferred to V4-VERIFY. B-C is Reported, never gated (§2.4) |

**Group I — SP-09 negative knowledge (new)**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| I1 | Approach-class canonicalization | PASS | 8-row table + bounds |
| I2 | **Canonical descriptor + goldens** | PASS | 24 golden rows byte-exact |
| I3 | `Record` = §8.5 `eliminated[]` slot (G6.1) | PASS | golden byte-equal to the design literal |
| I4 | Append-only elimination log | PASS | corrupt + truncated recovery |
| I5 | **Three-way `already_tried`** | PASS | all twelve rows incl. `StaleNote` em-dash literal |
| I6 | Record write path + blind mode | PASS | 1600 concurrent records, 0 deduped, no race |
| I7 | **Staleness (single `ChangedSince`)** | PASS | exactly one call over 2000 deps |
| I8 | **Bloom-as-cache, active-only rebuild** | PASS (ruled) | `tried.bloom` = 12,072 B @3000 records (range 11,264–14,336) ; @8000: FillRatio 0.3002, EstFPRate 0.00022 (< 0.02). The §3.9 hand-grep expects exactly one `sketch.RebuildBloom` line; `bloom.go` has two call sites (both inside the one rebuild path, lines 80/84 — the resize arm) plus a comment — ruled (d): the invariant (only negknow/bloom.go rebuilds) holds; the grep over-specifies "one line" |
| I9 | Four ingestion sources | PASS | MCP / pin / user-statement / observe |
| I10 | Heuristic detector | PASS | eleven rows; termination on cyclic graphs covered via the dag cycle pin (`TestReadThenWriteClosesALegitimateCycle`) + X3 over a real observer-built graph (§0a item 1) |
| I11 | `negknowtest` suite (0 skips) | PASS | node-ids golden reproduced |
| I12 | Elimination lifecycle e2e | PASS | + bloom-corruption recovery, exactly one Loud |
| I13 | **Phase 2 exit criterion** | PASS | stock 138 / negknow 39 / reduction 71.7% (≥ 25%) / stale_blocks 0 / dep-change sessions 6; negknow < stock on 18 of 24 sessions (≥ 8) |
| I14 | negknow budgets | PASS | Query hit 1.16 µs / miss 0.887 µs ; Record 26.0 µs ; RebuildBloom 9.94 ms ; RefreshStaleness 1.49 ms ; Open(20k) 58.6 ms ; DetectorScan 1.60 ms ; memory < 4 MB ; every `TestBudget_*` PASS on the quiet lane |

**Group J — repository-wide**

| ID | Item | Verdict | Metric / note |
|---|---|---|---|
| J1 | `go test -count=2 ./...` (win) | PASS | exit 0 after two diagnosed reds: a fixture-pin cascade this checkpoint's own fixture reconciliation missed (fixed, commit `fix(eval): move the fixture-shape pins…`), and the GC overshoot gate red once-of-two under the run's own package parallelism (3/3 green isolated) |
| J2 | Coverage floors | PASS | negknow 91.2% / observer 96.2% / store 92.7% / dag 90.8%; canon 98.5 / chunk 100 / config 92.8 / eval 90.9 / sketch 96.5 / redact 96.4 / tokens 93.5 / paths 90.6 (>= 90 floors); ipc 85.3 / daemon 82.5 / contract 84.2 / cli 82.9 (>= 75); every floor met by the tool's own gate. Its first two runs went red only on the X9 assertion amended below |
| J3 | Security posture | PASS | govulncheck: 0 vulnerabilities called; importgraph/testdeps/bindeps all OK (62/64 pkgs, 6 targets) |
| J4 | Placeholder scan | PASS (ruled) | 7 grep hits, all self-referential prose or lint machinery (bench payload's `"TODO"` grep-pattern string, planchecks' `XXX` spelling table, a negknowtest doc comment, the `not implemented` user-facing message and `ErrNotImplemented`'s own definition) — none a placeholder. Two `core.ErrNotImplemented` returns sit outside the J2 stub list: `cli`'s `notImplementedRun` (SP-01's sanctioned honest stub-command surface) and `eval`'s live-mode guard (pinned by `TestReplay_LiveModeRefusedWithoutEnv`) — ruled (d): the row under-lists the two sanctioned non-stub uses |
| J5 | CI on `verify/v3` (9 jobs) | BLOCKED (external) | all local rows green; the push (run 32929547097) had every one of its 12 jobs refused by GitHub Actions - "recent account payments have failed or your spending limit needs to be increased" - so no job executed. BLOCKED on account billing, not on the tree; the rerun and the three-platform bench-gate figures land in a follow-up commit the moment billing is restored, and SP-08's named merge condition (bench-gate green at first push, three p99 figures folded into ADR 0008) discharges with it |
| J6 | `Qompack.md` immutability | PASS (ruled) | diff vs root is exactly the authorized v1.3 revision (`9c50c6c`, Revision-log entry present). The gate's byte-immutability literal predates the recorded revision mechanism; "no unauthorized modification" is the enforced reading, and the working tree matches HEAD exactly |

**Cross-component integration tests (new, permanent — committed as `test(e2e)` on `verify/v3`)**

| ID | Test | Verdict | Metric / note |
|---|---|---|---|
| X1 | `TestV3_HookEventToTombstoneToRetrievalRoundTrip` | PASS | tombstone hash = retrieval key; 12-hex short-form reading ruled (ParseHash needs 64 hex) |
| X2 | `TestV3_ObserverFileVersionsDriveEliminationStaleness` | PASS | row steps 7–8 ruled a test-spec defect: they contradicted I8/I12 and §13 inv 3 (active-only rebuild); test asserts FillRatio ∈ (0,0.5] BEFORE rebuild, 0 after, and step-8 `AnswerAbsent` |
| X3 | `TestV3_ObserverDagDrivesHeuristicEliminationDetector` | PASS | detector's DAG-derivation clause overstated: `dagDeps` needs file-node Roots the builders don't mint, so `resolveDeps` (store history) is the live path — recorded for V4 |
| X4 | `TestV3_VerbatimPromptBecomesEliminationEvidence` | PASS | one composed OnStop added (decision-4 turn arithmetic: tool uses don't advance turns) |
| X5 | `TestV3_ReplayGateConsumesRealSketchHealth` | PASS | fixture reconciled: YES — health.json 0.18/0.006 → 0.434256/0.002912202 (measured at its own declared population; old values unreproducible: 2 keys/record), baseline watch-fors and gate pins moved together per §7.3; negative control trips the §11.4 ceiling sentence |
| X6 | `TestV3_ReplayGrowthGuardrailUsesRealStore` | PASS | α = 0.0332 live (≤ 0.95); negative control fails as required; driver exit 0 |
| X7 | `TestV3_ObserverSegmentsRespectDPIGuard` | PASS | `ErrAlreadyEncoded` durable across reopen |
| X8 | `TestV3_DegradedPassiveStillRecordsEverything` | PASS | 44 lines, 2 eliminations, zero HookSpecificOutput, act.* suppressed, restore path green |
| X9 | `TestV3_LiveSessionWriteSetAndAppendOnly` | PASS (amended) | 200 events through the real binary; write set confined; exactly one .bak. Its GC bullet was amended in-checkpoint: the blanket deleted=0 over-asserted — an ephemeral root is never in-window by the age clause (§8.2) and the flush-time GC can catch an ephemeral re-put mid-WAL-replay (R3's best-effort ordering), legally reclaiming one 46-byte ephemeral-only chunk. The test now asserts the design's own invariant (no non-ephemeral object ever deleted, nothing added, pass untruncated); six consecutive instrumented runs green |
| X10 | `TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently` | PASS | review found the DAG assertion vacuous — it was (deps.jsonl empty without a post-drain flush); hardened with LogRecords/Nodes/Edges positivity + the flush; 50/50 records, dedup across the crash, store matches the no-crash control |
| X11 | `TestV3_HotPathUnchangedWithLedgerResident` | PASS | B-A p99 = 2.048 ms (V2 was 3.072 ms; 25% ceiling 3.840 ms) ; B-B 0.576 ms ; B-E green (CPU row under --under-coload per the V2 co-load ruling) |
| X12 | `TestV3_FullCorpusIngestThroughObserverAndLedger` | PASS | all 24 sessions, no panics; read-heavy ratio ≥ 4.0 assertion met; dependency-change sessions all stale-block-free at the ingest layer |

**Performance budgets in force** (local = quiet windows-11 lane; Linux/macOS = CI bench-gate)

| Budget | Threshold | Linux | macOS | Windows | Verdict |
|---|---|---|---|---|---|
| B-A `hook_controlled` | p99 < 15 ms | pending CI | pending CI | 2.048 ms | PASS local; CI pending (billing) |
| B-B `l0_ingest` | p99 < 2 ms | pending CI | pending CI | 0.576 ms | PASS local; CI pending (billing) |
| B-C `l0_process` | p99 < 50 ms (soft) | — | — | 90–98 ms (256 KB Deduped/Delta), 164–197 ms AllNovel; 64 KB ≤ 41 ms | over — SP08-D1 → V4-VERIFY (Reported, never gated) |
| B-D `hook_wall` | reported only | — | — | p50 10.2 ms / p99 12.7–16.9 ms | n/a — production writer: none; re-carried (§0a item 6, below) |
| B-E `checkpoint_finalize` | p99 < 2 s | pending CI | pending CI | 70.8 ms wall / 31.25 ms CPU | PASS local; CI pending (billing) |
| B-F `mcp_tool_call` | p95 < 250 ms | — | — | — | **N/A — SP-13, wave 3** (row present in `obs.Budgets()`, E14) |
| B-G `hook_degraded` | 1 000 ms, reported only | — | — | rate-graded ipc gate green | n/a — `TestDegraded*` green: YES ; re-carried to V4-VERIFY (§0a item 6, below) |
| Store dedup ratio (read-heavy) | ≥ 4.0 | — | — | 189.35 (observer-level, authoritative) | PASS |
| Canonicalization gap (test-heavy) | ≥ 1.25× | — | — | 4.008× | PASS |
| Store growth exponent α | ≤ 0.95 | — | — | 0.620 committed / 0.0332 live | PASS |
| Bloom empirical FP @ capacity | ∈ [0.008, 0.013] | — | — | 0.009720 | PASS |
| Bloom `EstFPRate` @ 8 000 records | < 0.02 | — | — | 0.00022 (capacity 32,768 after resize) | PASS |
| `tried.bloom` on disk | 11 264–14 336 B | — | — | 12,072 B | PASS |
| benchstat vs `develop` baseline | ≤ 10% warn / ≤ 25% fail | — | — | zero time-metric regressions > 10%; two B/op warnings: dag `RebuildIndex` +414 B/op (+739% relative, 56→470 B absolute) and `AddNodeEdgePair` +200 B/op (+14.9%), both with 30–44% time improvements — ruled warnings, not failures (absolute-trivial byte counts) | PASS (2 warnings) |

**Regression**

| Item | Verdict | Note |
|---|---|---|
| Wave-2 merge order (§0a item 4) | PASS (deviation recorded) | README's order is SP-09 → SP-08; SP-08 was merged first (`b1a65fa`, prior session, fully verified at merge). Nothing in the tree constrains the order (item 4's own finding); the item's substantive check — every intermediate first-parent commit green — holds: `b1a65fa`/`4673bbd` verified by the SP-08 session, `5170dc0` by this checkpoint in full. Conflicts were resolved on the incoming branch (`66a0dfd`) and the auto-merge's silent drop of SP-08 from the missing-entry gate list was caught and restored there |
| V1 inventory: group A (17 rows) re-run | PASS | 16 clean + A17's ruled regex half |
| V1 inventory: `V1-VERIFY` §1 groups A–R re-run in full | PASS | ~165 rows run by two sweep-runners (transcripts in the session workspace); 6 non-pass, all dispositioned: A3 (288 commits vs the SP-01-era 7 — superseded by history), A9/A11 (the authorized Qompack.md v1.3 revision / the same seven non-placeholder grep hits as J4), N3/N4 (negknow fixtures now recorded and live — superseded by SP-09 landing; the "refuse while stub" behaviour is obsolete by design), X3 (SP-08's root-skip missing its `platform:` prefix — **fixed on verify/v3**, `fix(e2e)`) |
| V2 inventory: groups B–G (65 rows) re-run | PASS | all green (fan groups B–G) |
| V2 inventory: `V2-VERIFY` §2 re-run in full | PASS | ~180 rows run by two sweep-runners; 9 non-pass, all dispositioned: V2-MERGE-01 (observer fuzz target orphaned from nightly.yml — **fixed on verify/v3**, `ci(nightly)`); V2-SP01-18, V2-SP05-01, V2-SP06-01, V2-SP05-28 (wall-clock/rate-graded reds under sweep co-load — all green in the quiet J1 or isolated 3/3); V2-SP02-12 (row's `-run` patterns select nothing — pre-existing drift class, recorded); V2-SP02-13 (43 tests vs the row's 38 — superseded, SP-09 added five); V2-SP02-15 (driver output format evolved; behaviour pinned by current B10 suite); V2-SP06-26 (grep hits are the W-1 skip machinery's own constants — the §7.1 filter lesson); V2-ALL-05 (the authorized v1.3 revision, as J6) |
| **B-G / B-D carry (§0a item 6)** | re-carried | wave 2 wired neither, deliberately: B-G's sample and evaluator still cannot coexist in one process (structural), and the natural wiring — spooled B-G samples drained into the daemon's registry, plus a production `hook_wall` write — belongs to SP-12's idle scheduler and the wave-3 daemon work. Re-carried to **V4-VERIFY** with that reason; enforcement meanwhile stays with `internal/ipc`'s rate-graded degraded-append gates (green this checkpoint) and B-G's config-driven budget row (E14) |
| Recorded-corpus tier (§0a item 3) | re-stated | still empty; the committed Phase-0/2 numbers are synthetic-tier per ADR 0002, which names the import command and its owner (release manager). The observer now exists, so recorded sessions are worth re-baselining — the work rides with V4's corpus re-baseline (see SP02 dispositions) |
| W-2 fixture reconciliation (health.json, stats-growth.json) | PASS | health.json reconciled to measured values (see X5); stats-growth.json verified live by X6 (real observer-fed series, α 0.0332, driver exit 0) and retained as the committed driver input per §7.3's same-filter-state rule |
| Conformance-suite skip audit | PASS | 9 stub suites skip W-1 verbatim; 13 landed suites zero behaviour skips |
| Contract producer split still 5/4 | PASS | `TestDeclaredProducerSetMatchesArchitecture` |
| **2% no-regression guardrail (§11.3)** | PASS | regressions: 0 ; signed off: 0 (gate exit 0, `--phase 2`, `--max-cpu 2m`) |
| Corpus staleness window (`phase ≤ regen+2`) | PASS | no trip at `--phase 2` |
| §0a item 1 (detector terminates on cycles) | PASS | `TestReadThenWriteClosesALegitimateCycle` (dag) green; detector scanned a real observer-built graph in X3; `Scan`'s termination documented against the dag cycle pin |
| §0a item 2 (LastSessionID ownership) | PASS | structural guard green with the observer live (A14/E10); SP-08 never writes the field |

**Carried-defect disposition (§0a item 5 + SP08-D1)** — statuses live in `plans/CARRIED-DEFECTS.tsv`; reasons in each row's detail document (V3-VERIFY dispositions section, 2026-08-26)

| id | `fixed` / re-deferred to | evidence or reason |
|---|---|---|
| SP04-D2 | wontfix | the owed decision: fixed-point composition and Delta-rebase both rejected; deletion-join class stands as a documented limitation |
| SP04-D3 | wontfix | travels with D2 by its own manifest text |
| SP04-D5 | fixed | re-judged quiet: Run_Bash100KB 2.315 ms (budget 3 ms), Run_GoTest 676 µs (budget 1 ms) |
| SP04-D6 | fixed | -count=10 quiet: 2.292–2.349 ms, ±1.2% (V2's ±33% was host load) |
| SP06-D1 | wontfix | GCPolicy.Deadline comment re-scoped to what it bounds; tombstone overshoot stands documented |
| SP05-D1 | deferred:V4-VERIFY | transport-shape work beside R3; wave 3 reworks the drain path it lands in |
| SP02-D1 | deferred:V4-VERIFY | corpus re-baseline travels as one unit to V4 (all gates green on the committed corpus this checkpoint) |
| SP02-D2 | deferred:V4-VERIFY | same unit |
| SP02-D3 | deferred:V4-VERIFY | same unit (stock 0.695 strictly between null and oracle this run) |
| SP02-D4 | deferred:V4-VERIFY | same unit (pMin alignment folds into the re-baseline) |
| SP02-D5 | deferred:V4-VERIFY | same unit |
| SP02-D6 | deferred:V4-VERIFY | same unit |
| SP04-D7 | fixed | §8.1's delta write owned: SP-10's checkpointer encode path; canon.Decide is the seam it consumes |
| SP06-D2 | deferred:V4-VERIFY | now measured on BOTH platforms (Windows 16.1/4.7 ms; Linux-WSL2 ~5.4/~4.5 ms) — a budget-vs-implementation decision, paired with SP08-D1 |
| SP08-D1 | deferred:V4-VERIFY | isolated re-measurement confirms the recorded class; B-C is Reported, never gated; one defect with SP06-D2 seen from two layers |

**Gate**

| Question | Answer |
|---|---|
| Every row above PASS? | Every locally-runnable row: YES. J5: blocked on GitHub billing - the one open row |
| `verify/v3` merged into `develop` with `--no-ff`? | NO - held until J5 is green (§8.4/§8.5) |
| Tag `v0.2.0` applied? | NO - follows the merge; the tag is applied locally and never pushed (a pushed v* tag cuts a Release) |
| Wave-3 branches cut (SP-10, SP-11, SP-12, SP-13)? | NO - only after every answer above is yes |
