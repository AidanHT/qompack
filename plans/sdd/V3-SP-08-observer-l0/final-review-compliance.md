# Final whole-branch review — lens: plan compliance and completeness

Branch `feat/sp08-observer-l0`, range 1466b73..cd02d5e (8 commits). Reviewer: compliance lens of
the three-lens final panel. All checks run read-only against the pinned SHA
cd02d5ea1e63a6b805d2bdb58221603ff1588362 (`git show` / `git grep <sha>`); the working tree was
never read. Verdict: **NOT approved as fully closed** — one Important finding (the
three-platform bench-gate exit criterion is structurally undischargeable pre-push and needs an
explicit merge-condition ruling); everything else verified compliant or already ruled.

## 1. Exit criteria, item by item

| Criterion | Status |
|---|---|
| `TestPhase1_DedupRatioReadHeavy` (DedupRatio >= 4.0) | PRESENT (test/e2e/phase1_exit_test.go); ADR records 189.35 on seed 0x51080001 — PASS |
| `TestPhase1_CanonicalizationGapOnTestOutput` (>= 1.25x, numbers in ADR) | PRESENT; ADR records 4.008x (108.74 on / 27.13 off) under the T7 volatile-refresh ruling, and honestly records the pre-ruling 0.969 whole-session number — PASS as ruled |
| `TestPhase1_StoreGrowthSublinear` | PRESENT — PASS per implementer report/ledger |
| bench-gate green on ubuntu/macos/windows with observer wired, figures in ADR | **NOT MET** — local Windows only (B-A p99 2.048 ms, B-B p99 0.704 ms, both inside gates); ADR "Measured numbers" marks CI platform figures **pending**; ledger: "Linux run + CI bench figures pending first push". See Important finding F1 |
| B-C benchmarks within 50 ms; `BenchmarkTombstone` < 2 us/op | FileRead64KB inside; TestOutput256KB **breaches on all three fixtures** — FAILED-and-recorded per the Task-2 ruling as carried defect SP08-D1 (plans/CARRIED-DEFECTS.tsv row + plans/V2-SP-08-carried-defects.md, both on-branch; cross-referenced SP06-D2, owner V3-VERIFY). Tombstone 434 ns/op — PASS. Settled; not re-litigated |
| G3.2 tombstone + golden | DONE — tombstone.go, testdata/golden/observer/tombstones.txt (13 markers), the §8.1-form test |
| G2.3 verbatim prompt | DONE — prompt.go; `TestOnUserPrompt_NeverRegenerated` and `TestE2E_VerbatimPromptSurvivesRestart` both present |
| G10.1 subagent capture | DONE — stop.go; `TestOnStop_RetrievalPathG10_1` present, round-trips out of a real store |
| G1.5 signals + delivery | DONE — signals.go; observer_ops.go's `OnSignals` callback calls `sched.Observe(..., Features{TodoTransition: 1}, 0)`; delivery is dormant while `Options.Sched` is nil (wave-2 reality; the seam exists and is wired). `TestWireObserver_SessionStartRefreshesStaleness` present |
| W-1 suite lifted, 8 behaviour cases, real factory | DONE — all eight `t.Run` case names verified in observertest/suite.go; `RunObserverSuite(t, "observer.New", newRealObserver)` registered in suite_test.go beside the stub factory |
| `ci-local` green | Evidence accepted per ledger (ci-local / race / replay / build-all / lint green; cover 96.2% >= 75%). The criterion's named gate list does not match devtool's actual task set — settled pre-flight ruling S2 (local equivalents run as separate invocations; CI runs the rest on push) |
| Import set exact | VERIFIED — package observer non-test imports are exactly canon, config, core, dag, grammar, hookio, logging, obs, paths, sketch, store, tokens + stdlib. No scheduler / checkpoint / symbols / contract / negknow / analyzer / daemon |
| Zero `Bloom` occurrences in non-test files | Letter FAILS (8 prose-comment hits in doc.go / observer.go / session.go / sketches.go); the enforced check `TestObserverSourceHasNoBloomReference` is an identifier-level go/parser walk and passes. Same class as the ruled V6-VERIFY 1.8.5 rewrite; the SP-08 plan's own bullet was not amended. Minor F3 |
| `tried.bloom` never created | Both named tests present (`TestOnSessionEnd_NeverWritesTriedBloom`, `TestE2E_ObserverThroughDaemon`) |
| Hooks exit 0 under fault injection | `TestE2E_HooksExitZeroUnderFaultInjection` present |
| Branch purity / file map | VERIFIED per commit (section 3) |
| "Exactly 7 commits", no trailers | 8 commits — the 8th is the ledger-sanctioned `chore(devtool,guards)` gates commit (Task-2 ruling: documented exception; the 5–8 band holds). Trailer grep over all bodies: clean. Minor F4 records that the plan bullet itself still says "exactly 7" |
| ADR exists, decisions + numbers | DONE — docs/adr/0008-observer-l0.md records twelve decisions, amendment (a)–(d), the rulings, the tombstone grammar and every measured number plus coverage. The exit bullet's "eight resolved decisions" is a stale count against the plan's own twelve-decision section — cosmetic, F5 |

## 2. Done checklist

- Design-context mapping: every §8.1 item has its file and its test file (item 2 tombstone.go,
  3 supersede.go, 4 graph.go, 5 sketches.go, 6 tooluse step 11 + prompt.go's collectThrash,
  7 prompt.go, 8 stop.go); §7.3 SessionStart/SessionEnd → session.go; §2.2 → IsCompactable;
  §6.6 → features.go; §8.2 → `AppendFileVersion` (tooluse.go:140) and `Store.GC` with policy
  (session.go:220); §10 → test/e2e/phase1_exit_test.go. COMPLETE.
- Placeholder grep: one hit — prose "a different subplan's TODO" in
  test/e2e/faultinject_test.go:440. Pre-existing at base 1466b73, file untouched by this branch;
  ledger-acknowledged. Minor F2.
- No `core.ErrNotImplemented` returned in internal/observer: the only returns are observertest's
  deliberate stub factory in suite_test.go (a test file, mandated by the suite's shape test). OK.
- §5.21 types: the `Observer` methods match with `Event = hookio.Event` / `Output = hookio.Output`
  aliases (type-identical; observertest already consumed `observer.Event` pre-branch);
  `Tombstone(rec store.ToolUseRecord) string`, `Signals{TodoCompleted, TestPassed, GitCommit bool;
  Paths []string}` and `ExtractSignals(e Event) Signals` all verified. `ChangedSince` is never
  called; `MarkSuperseded(older, by)` older-first is verified at supersede.go:117–119.
- NodeID-constructor / argsPreviewMax grep: clean (no hits).
- daemon.go diff: the two wiring blocks (WireObserver before New, RegisterObserverIdleWork after)
  **plus** the two T6-sanctioned extras (deferred `opts.Store.Close()` after Run; startup
  `os.Remove` of the store's empty quarantine scaffolding) and the `path/filepath` import they
  need. Matches the ruled set exactly; nothing else. The checklist bullet's "and no other change"
  is superseded by the T6 ruling, accepted pending V3-VERIFY.
- RefreshStaleness: in observer_ops.go's wrapped SessionStart, nil-tolerant on Ledger and Store,
  skipping compact/clear; `negknow` appears in internal/observer only as prose — the checklist's
  "returns nothing" grep letter fails on 10 comment hits while the import is genuinely absent
  (folded into F3).
- Trailer grep clean; Qompack.md untouched (empty diff); commit count 8, within the band.
- Subjects: all within the 64-char post-prefix cap (longest 62 after the prefix), matching the
  S4-ruled shortened forms; a `Refs:` footer is on every commit, the chore included.

## 3. File map, per commit

c0c63c9 (observer + golden fixture), d860ccd (observer + observertest/suite_test.go +
plans/CARRIED-DEFECTS.tsv + plans/V2-SP-08-carried-defects.md — the SP08-D1 ruling), 20a38e2
(devtool / guards / hookflow_test.go + the plans/V6-VERIFY 1.8.4 and 1.8.5 row edits — all four
sanctioned by Task-2 rulings; the V6-VERIFY diff was verified to match the rulings exactly),
39d87ef / e957564 / 7885650 (observer only, incl. testdata/transcript_tail.jsonl), f1c7753
(observer + observer_ops.go(+test) + cli/daemon.go + test/e2e), cd02d5e (ADR +
phase1_exit_test.go). **Nothing outside the sanctioned set.** observer_ops.go registers no ops,
binds the five seams in one Bind, and holds the single guarded `Persister` assertion — as the
contract requires.

## 4. Deferred-minor triage (ledger, all tasks)

Reviewed every deferred-minor line from Tasks 0–5 and the T6 review. **None must be fixed before
merge**: each is either owned by V3-VERIFY by explicit ruling (S19 hookio constructor; SP08-D1
adjudicated together with SP06-D2; the Task-4 parked Important on unredacted prompt text reaching
the mutable local index's ArgsPreview — parked-with-ruling, severity bounded, V3-VERIFY must rule
on SP-06's preview redaction), outside SP-08's files (core.Hash.Short allocations, the ipc
TestStateWriteIsAtomic co-load flake, handlers.go doc wording), or cosmetic/test-shape
(truncateRunes multi-byte row, the single-line sentence pin, stage-name constant split, nil Clock
in tests). The Task-4 parked Important is the one item the merge record should keep loud for
V3-VERIFY section 0a.

## 5. Findings

- **F1 (Important)** — Exit criterion "bench-gate green on ubuntu-latest, macos-latest and
  windows-latest with the observer wired ... the three p99 figures are recorded in the ADR": only
  local Windows figures exist; the ADR marks the CI figures pending, and no Linux run (even the
  local WSL2 path) has been made. The plan says "all must hold on the branch", and this one
  cannot before the first push (nothing is pushed by this session, by design). Needs an explicit
  controller ruling that merge may proceed with this deferred to the first post-merge push, with
  the CI bench-gate result and the ADR figure update as a named condition — otherwise Phase 1 is
  declared closed on one platform's numbers. Plan-mandated.
- **F2 (Minor)** — The Done-checklist placeholder grep trips on the pre-existing prose "TODO" in
  test/e2e/faultinject_test.go:440 (outside this branch's diff). A one-word reword whenever that
  file is next touched closes it.
- **F3 (Minor)** — Two checklist/exit bullets fail on the letter while the enforced checks pass
  on intent: "zero occurrences of Bloom in non-test files" (8 prose hits; the identifier-level
  AST test is the real gate, and V6-VERIFY 1.8.5 was amended to say exactly that — the SP-08
  plan's own bullet was not) and the negknow grep "returns nothing" (10 prose hits; the import is
  genuinely absent). A plan-errata note in the merge record would close the gap. Plan-mandated
  letter, ruled intent.
- **F4 (Minor)** — The exit bullet "Exactly 7 commits" stands unamended against the ruled 8th
  chore commit; the exception lives only in the ledger and the ADR. Record-keeping.
- **F5 (Minor)** — The exit bullet says the ADR records "the eight resolved decisions"; the
  plan's own Resolved-decisions section and the ADR both carry twelve. Stale count, cosmetic.

## 6. Strengths

Every named exit-criterion test, fuzz target and benchmark exists under its exact name at the
pinned SHA; the realized import set matches the binding statement to the package; the file map is
exactly the sanctioned set, with every excursion carrying a ledger ruling; breaches (B-C, the
canon gap) were recorded and carried honestly rather than tuned away; and the ADR is an unusually
complete record — decisions, rulings, raw numbers, and what is still pending are all written
down.
