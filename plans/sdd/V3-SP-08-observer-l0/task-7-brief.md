# Task brief — Commit 7: Phase 1 exit-criterion harness, hot-path benchmarks, ADR 0008

Source: plans/V3-SP-08-observer-l0.md (verbatim excerpts; the plan's line ranges are noted before each excerpt). Exact values, names, signatures and test rows below are binding.

## Controller rulings that OVERRIDE the plan text below where they conflict

- Commit subject unchanged (54 chars).
- Tooling reality: `go run ./tools/devtool <task>` runs ONE task per invocation. The local gate for this commit is: `ci-local` (fmt-check → lint → vet → build → test → cover → plugin-validate → gen-config-docs --check), then `test-race`, `bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json bench-observer.json` (check the harness's actual flag names in test/bench/hotpath first), `replay`, `build-all`. There is no devtool `verify`/`bench-gate`/`replay-gate`/`security`/`docs`/`crossbuild` — those are CI jobs; record in the ADR which were run locally.
- `tools/devtool/cover.go`'s `landedSubplans` is updated in the MERGE commit, not here: measure `internal/observer` coverage with `go test -cover ./internal/observer/` and record the figure in the ADR (it must be ≥ 75%).
- `store.Open(root, cfg, deps)` is the package constructor; content is read with `st.GetRoot`/`st.Open(ctx, root)`; the Phase 1 harness reads `Stats()` only.
- `eval.Synthesize` / `SynthSpec` / `Session` / `ToolCall` field names: verify against `internal/eval` before writing `eventsFor` (the plan's names were checked and match, but confirm `ToolCall.Paths`/`Args`/`ID`/`Name` spellings).
- The ADR records: the resolved decisions, the amendment (a)–(d), every ruling the controller made that changed the plan's file layout (features.go in Commit 2; WireObserver opening the store/DAG/sketches; landedSubplans at merge), and the measured numbers.


---
<!-- plan lines 157-176 -->

### §10 Phase 1 — the exit criterion this subplan is graded on

> ### Phase 1 — Store and observer
>
> - FastCDC chunker, content-addressed object store, zstd
> - Per-tool output canonicalizers (timestamps, ANSI, PIDs, addresses) + MinHash near-dedup (O2)
> - `PostToolUse` and `UserPromptSubmit` hooks
> - Merkle index, file version history
> - Addressable tombstones (G3.2)
> - Redundancy / supersession detection
>
> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

### §11.3 — guardrails

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite


---
<!-- plan lines 2205-2341 -->

### `test/e2e/phase1_exit_test.go` (new) — the Phase 1 exit criterion

**The call sequence is synthesized; the bytes are real.** `eval.Synthesize` is the right source for
the *shape* of a session — tool mix, re-read rate, changepoints, subagent calls — and the wrong
source for its *content*: every tool result it emits is a token count, `call.Result =
mustCompactJSON(map[string]int{"tokens": tokens})` (`internal/eval/synth.go`), and the committed
corpus shows it (`"result": {"tokens": 646}`, `"args": null`). Driving that through the observer
would measure roughly sixteen bytes per event with nothing volatile in them, which makes
`ratioOn == ratioOff` and the 1.25× canonicalization gate unpassable by any change to
`internal/observer`; `args: null` would also leave `pathKey` empty on every event, so no file
version, no supersession, no HLL or Misra-Gries feed and no shared-file or symbol edge would ever be
exercised — and the `FileRereadRate` the read-heavy spec turns on lives in `tc.Paths`, which the old
`eventsFor` never read.

So this harness pairs the synthesized sequence with **real tool output from
`testdata/corpora/toolout/`** — SP-04's committed bash, test-runner, grep, glob, git, ANSI, fileread
and webfetch captures, already listed under *Fixtures needed* below, and already the basis of
`test/dedup`'s honest with/without measurement:

```go
var readHeavy = eval.SynthSpec{
    Turns: 400,
    ToolMix: map[string]float64{"Read": 0.62, "Grep": 0.14, "Glob": 0.06, "Edit": 0.10, "Bash": 0.08},
    FileRereadRate: 0.55, TestOutputNoise: 0.0,
    Changepoints: 6, Eliminations: 3, SubagentCalls: 2,
    CompactionAt: []core.TurnIndex{180, 330},
}
var testOutputHeavy = eval.SynthSpec{
    Turns: 400,
    ToolMix: map[string]float64{"Bash": 0.55, "Read": 0.20, "Edit": 0.15, "Grep": 0.10},
    FileRereadRate: 0.25, TestOutputNoise: 0.85,
    Changepoints: 5, Eliminations: 4, SubagentCalls: 1,
    CompactionAt: []core.TurnIndex{200},
}
const seedReadHeavy, seedTestHeavy = 0x5108_0001, 0x5108_0002

// corpus loads testdata/corpora/toolout/<group>/*.txt once, keyed by the group directory name, in
// sorted filename order. Each file's sibling <name>.txt.meta.json carries {"tool":…,"path":…}; only
// the bytes are used here, the tool name comes from the synthesized call.
type corpus map[string][][]byte

// payloadFor picks the response bytes for one synthesized call, deterministically: the group is
// chosen from the tool name (Read/Edit → "fileread", Grep → "grep", Glob → "glob",
// Bash → "testrunner" for testOutputHeavy and "bash" for readHeavy, WebFetch → "webfetch"), and the
// file within the group is indexed by a hash of the call's first path — so re-reading a path
// re-serves the SAME bytes and FileRereadRate turns into real chunk reuse, while the "-v2" variants
// in the fileread and sp06 groups supply the "same file, two lines changed" case §6.1 names.
func payloadFor(c corpus, tool string, paths []string, seq int) []byte
```

**Driving the session through the observer.** `eval.Session` is a turn list, not a hook stream, so
the harness materializes hook events itself — one helper, no ambiguity:

```go
func eventsFor(s eval.Session, c corpus) []hookio.Event {
    var out []hookio.Event
    seq := 0
    for _, t := range s.Turns {
        if t.Role == "user" {
            out = append(out, hookio.Event{HookEventName: "UserPromptSubmit",
                SessionID: core.SessionID(s.ID), Prompt: t.Text})
            continue
        }
        for _, tc := range t.ToolCalls {
            // ToolInput is built from tc.Paths — eval.ToolCall keeps the paths in their own field
            // and leaves Args nil for ordinary calls, so without this the observer sees no path at
            // all and PathsFromInput returns nothing.
            in := json.RawMessage(`{}`)
            if len(tc.Paths) > 0 {
                in = mustJSON(map[string]string{"file_path": tc.Paths[0]})
            } else if len(tc.Args) > 0 {
                in = tc.Args
            }
            body := payloadFor(c, tc.Name, tc.Paths, seq)
            seq++
            out = append(out, hookio.Event{HookEventName: "PostToolUse",
                SessionID: core.SessionID(s.ID), ToolName: tc.Name, ToolUseID: tc.ID,
                ToolInput: in,
                ToolResponse: mustJSON(map[string]string{"content": string(body)})})
        }
    }
    return out
}
```

`{"file_path": …}` is the key `PathsFromInput` reads for `Read`/`Edit`/`Write`; for `Grep` and
`Glob` the helper emits `{"pattern":"…","path":paths[0]}` instead, matching those tools' real
payload shapes, and for `Bash` it emits `{"command":"go test ./..."}` with no path. The point is
only that a synthesized call arrives at `OnToolUse` looking like the hook payload it stands for.

The test calls `OnUserPrompt` for `UserPromptSubmit` events and `OnToolUse` for `PostToolUse`
events, in order, against a real `store.Open` on `t.TempDir()` with a `FakeClock` advancing 1 s per
event, then reads `store.Stats(ctx)`. **"Raw transcript" is `Stats().RawBytes`** (the pre-dedup byte
count SP-06 accumulates) and **"store size" is `Stats().Bytes`**; the ratio asserted is
`Stats().DedupRatio`, which §5.8 defines as `RawBytes / Bytes`. No other definition of the ratio is
used anywhere in this subplan.

`test/dedup` measures the same corpus at the canonicalizer level and is the cross-check: if
`TestPhase1_CanonicalizationGapOnTestOutput` and `test/dedup`'s `testrunnerGainFloor` (also 1.25)
disagree, the observer is doing something to the bytes on the way in, which is the bug to find —
not a reason to move either number.

| Test | Assertion |
|---|---|
| `TestPhase1_DedupRatioReadHeavy` | drive every `ToolCall` of `eval.Synthesize(seedReadHeavy, readHeavy)`, carrying `testdata/corpora/toolout/` bytes, through `observer.OnToolUse` against a real store with canonicalization **on**; `store.Stats().DedupRatio >= 4.0`. **This is the §10 Phase 1 exit criterion.** |
| `TestPhase1_CanonicalizationGapOnTestOutput` | run `testOutputHeavy` twice — once with `store.canonicalize.enabled=true`, once `false` — and assert `ratioOn >= ratioOff*1.25`. The ≥25% figure is this subplan's operational reading of *"the gap on test-output-heavy sessions justifies O2 on its own"*; both raw numbers are printed and written to `phase1-dedup.json` in the test's temp dir regardless of pass/fail. |
| `TestPhase1_PathsReachTheObserver` | the read-heavy run | `Stats().Files > 0`, at least one `AppendFileVersion`, at least one `MarkSuperseded`, and a non-zero HLL cardinality — the guard against a harness that silently stops exercising path-keyed behaviour, which is exactly how the previous `eventsFor` failed |
| `TestPhase1_ResponseBytesAreReal` | the read-heavy run | `Stats().RawBytes` divided by the tool-call count exceeds 1 KB, so the ratio is measured over real tool output rather than over `{"tokens":N}` envelopes |
| `TestPhase1_ReportArtifact` | the emitted JSON has keys `read_heavy_ratio_canon`, `read_heavy_ratio_raw`, `test_heavy_ratio_canon`, `test_heavy_ratio_raw`, `raw_bytes`, `store_bytes`, `objects`, `tool_uses` |
| `TestPhase1_StoreGrowthSublinear` (§11.3 guardrail) | bytes stored over the second half of the read-heavy session are strictly less than over the first half |
| `TestPhase1_CorpusSweep` | when `testdata/sessions/synthetic/` exists, replay every session in it — through the same `eventsFor`, so its calls also carry corpus bytes — and log each ratio; informational, fails only if any session panics |

**Hook p99 < 15 ms** is the other half of the exit criterion and is measured by SP-05's existing
harness, now with a non-trivial handler behind it:

```
go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json bench-observer.json
```

`TestPhase1_HotPathBudgetDocumented` asserts the committed ADR records a B-A p99 figure from the
three CI platforms; the gate itself is the `bench-gate` job, which fails the build if B-A p99 ≥ 15
ms. Micro-benchmarks `BenchmarkOnToolUse_*` and `BenchmarkTombstone` cover B-C.

### Fixtures needed

- `testdata/golden/observer/tombstones.txt` — 13 rendered markers, created in commit 1.
- `testdata/corpora/toolout/` — SP-04's committed raw bash/test/grep/glob/git/ANSI/fileread/webfetch
  output; **reused, not extended**, by both `BenchmarkOnToolUse_TestOutput256KB` and the Phase 1
  harness, which draws every tool-result payload from it.
- `internal/observer/testdata/transcript_tail.jsonl` — 40-line synthetic transcript with sidechain
  assistant messages, for `TestOnStop_SummaryFromTranscriptTail`.
- No new session fixtures: Phase 1 takes its call *sequence* from `eval.Synthesize` and its *bytes*
  from the corpus above. Adding real result bytes to `eval.SynthSpec` would be an `arch/` amendment
  against `internal/eval` and is explicitly not done here.

---


---
<!-- plan lines 2514-2536 -->

### Commit 7 — `test(observer): Phase 1 exit-criterion harness and hot-path benchmarks`

- [ ] Add `test/e2e/phase1_exit_test.go` with the seven rows, the two `SynthSpec` literals, the
      `corpus`/`payloadFor` loader over `testdata/corpora/toolout/`, and the `eventsFor` that builds
      `ToolInput` from `tc.Paths`. TDD ordering for a gate commit: write the assertions **at the design's numbers
      first** (`>= 4.0`, `ratioOn >= ratioOff*1.25`), run them, and only then tune the observer. If
      an assertion fails, the fix goes in `internal/observer` (or in the `PutOptions` it passes) —
      **the threshold is never weakened**; a genuine need to move it is a §11.3 sign-off, not an
      edit.
- [ ] Run `go test ./test/e2e/ -run Phase1 -v`; record `read_heavy_ratio_canon`,
      `read_heavy_ratio_raw`, `test_heavy_ratio_canon`, `test_heavy_ratio_raw`.
- [ ] Run `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon
      --json bench-observer.json` locally; record B-A p50/p99 and B-B p99.
- [ ] Add `docs/adr/0008-observer-l0.md`: the eight resolved decisions, the §8.1-item-7 destination
      rationale, the tombstone grammar, the supersession rules, the measured dedup ratios, and the
      measured B-A/B-B/B-C numbers per platform from the CI run.
- [ ] `go run ./tools/devtool ci-local` — `verify`, `test`, `cover`, `bench-gate`, `replay-gate`,
      `plugin-validate`, `security`, `docs` all green.
- [ ] Footer: `Refs: SP-08, §10 Phase 1, §11.3, §8.1 performance budget`

Seven commits, each compiling and green for the packages it touches.

---

---
<!-- plan lines 2584-2716 -->

## Exit criteria

**Quoted verbatim from `Qompack.md` §10, Phase 1 — this slice closes the phase:**

> **Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

**Quoted verbatim from `Qompack.md` §11.3:**

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - Store growth sublinear in session length after dedup

**Measured form of the above (all must hold on the branch):**

- [ ] `TestPhase1_DedupRatioReadHeavy` passes: `store.Stats().DedupRatio >= 4.0` on
      `eval.Synthesize(0x51080001, readHeavy)`.
- [ ] `TestPhase1_CanonicalizationGapOnTestOutput` passes: `ratioOn >= ratioOff * 1.25` on
      `eval.Synthesize(0x51080002, testOutputHeavy)`, with both raw numbers recorded in
      `docs/adr/0008-observer-l0.md`.
- [ ] `TestPhase1_StoreGrowthSublinear` passes.
- [ ] `bench-gate` green on ubuntu-latest, macos-latest and windows-latest with the observer wired:
      **B-A p99 < 15 ms** and B-B p99 < 2 ms, at `--iterations 2000`; the three p99 figures are
      recorded in the ADR.
- [ ] `BenchmarkOnToolUse_FileRead64KB` and `BenchmarkOnToolUse_TestOutput256KB` within **B-C p99 <
      50 ms**; `BenchmarkTombstone` < 2 µs/op.

**Gap closure:**

- [ ] **G3.2** — `Tombstone` renders the §8.1 item 2 marker with hash, size, tool, subject and the
      `re-expandable` affordance; golden test locks the format.
- [ ] **G2.3** — every `UserPromptSubmit` is content-addressed, indexed and never rewritten;
      `TestOnUserPrompt_NeverRegenerated` and `TestE2E_VerbatimPromptSurvivesRestart` prove it.
- [ ] **G10.1** — `SubagentStop` stores summary + tool-result hashes;
      `TestOnStop_RetrievalPathG10_1` round-trips them out of a real store.
- [ ] **G1.5** — `ExtractSignals` detects todo completion, passing test runs and git commits, and
      `WireObserver` delivers them to the scheduler seam.

**Local criteria:**

- [ ] The Rule W-1 skip in the observer conformance suite no longer fires. `internal/observer/
      observertest` **ships** — `func RunObserverSuite(t *testing.T, name string, factory func(t
      *testing.T) observer.Observer)` in `suite.go`, with `behaviour.go` and `suite_test.go` beside
      it — so SP-08 creates nothing here. Its `/behaviour` block is gated by `skipIfStub`, which
      probes `OnToolUse` for `core.ErrNotImplemented`; landing the real `New` lifts the gate, and
      SP-08's job is to make the eight behaviour cases pass — `post_tool_use_never_blocks_the_tool_
      call`, `user_prompt_capture_never_blocks_the_prompt`, `session_start_branches_on_source`,
      `stop_and_subagent_stop_are_both_accepted`, `session_end_flushes_without_reporting_an_error`,
      `every_entry_point_tolerates_a_malformed_event`, `replaying_one_event_twice_is_not_an_error`
      and `extract_signals_detects_todo_test_and_git` — with the real factory registered in
      `suite_test.go`. `Tombstone` stays asserted in `internal/observer/tombstone_test.go` rather
      than in the suite, because `observertest` may not import `store` (§3.2).
- [ ] `go run ./tools/devtool ci-local` green: `verify` (gofumpt clean, `golangci-lint` clean,
      `go vet`, `nomagic`, import-graph layer check, test-only-dep check, build), `test` and
      `test-race`, `cover` (≥ 75% for `internal/observer`), `crossbuild`, `bench-gate`,
      `replay-gate`, `plugin-validate`, `security`, `docs`.
- [ ] `internal/observer` imports exactly: `core paths config logging obs hookio store canon sketch
      dag grammar tokens` plus stdlib. No `scheduler`, `checkpoint`, `symbols`, `contract`,
      `negknow`, `analyzer`, or `daemon`.
- [ ] Zero occurrences of `Bloom` in non-test files under `internal/observer`
      (`TestObserverSourceHasNoBloomReference`).
- [ ] `.qompack/sketches/tried.bloom` is never created by any observer code path
      (`TestOnSessionEnd_NeverWritesTriedBloom`, `TestE2E_ObserverThroughDaemon`).
- [ ] Every hook subcommand still exits 0 under fault injection
      (`TestE2E_HooksExitZeroUnderFaultInjection`) — §13 invariant 6.
- [ ] The `arch/sp08-observer-seams` amendment is merged to `develop` **before** this branch is cut,
      and `feat/sp08-observer-l0` contains no edit to `internal/store`, to
      `internal/daemon/handlers.go`, to `internal/daemon/options.go`, to `internal/daemon/daemon.go`,
      or to any file under `internal/cli` **except**
      `internal/cli/daemon.go`, whose `runDaemon` gains the Commit 6 observer wiring block and
      nothing else. That one carve-out is deliberate: it is the repository's only non-test
      `daemon.New` call site, so without it `WireObserver` has no caller it is allowed to have.
- [ ] Exactly 7 commits on `feat/sp08-observer-l0`, all conventional, none carrying an attribution
      trailer; CI's trailer grep passes. The amendment commit is on its own branch and is not one of
      the seven.
- [ ] `docs/adr/0008-observer-l0.md` exists and records the eight resolved decisions plus every
      measured number.

---

## Done checklist

- [ ] Every quote in **Design context** has a corresponding implementation: §8.1 item 2 →
      `tombstone.go`; item 3 → `supersede.go`; item 4 → `graph.go`; item 5 → `sketches.go`
      (including the Bloom prohibition); item 7 → `prompt.go`; item 8 → `stop.go`; §7.3
      SessionStart/SessionEnd rows → `session.go`; §2.2 compactable set → `IsCompactable`; §6.6
      five features → `features.go`; §8.2 file version history and GC → `tooluse.go` step 7 and
      `session.go` step 6; §10 Phase 1 → `test/e2e/phase1_exit_test.go`.
- [ ] Placeholder scan: `git grep -nE 'TODO|TBD|FIXME|XXX|not implemented|handle (this|edge cases)'
      -- internal/observer internal/daemon/observer_ops.go test/e2e` returns nothing, and no
      function returns `core.ErrNotImplemented` in `internal/observer`.
- [ ] Type consistency with **Interface contract**: `Observer`, `Tombstone`, `Signals` and
      `ExtractSignals` match §5.21 character for character; every consumed signature is called with
      the exact argument types listed (in particular `store.ChangedSince` is not called at all here,
      `EstimateRoot` receives `[]core.ChunkRef`, and `MarkSuperseded(older, by)` argument order is
      older-first).
- [ ] No §5 interface owned by another subplan was modified, and no method was added to one
      (Rule W-3). The three behaviours that had to change were raised as the
      `arch/sp08-observer-seams` amendment and landed on `develop` first, exactly as §0 requires —
      including `Services.Mode`, which widens SP-05's struct rather than adding a method to an
      interface, and is declared in §5.4 on that branch before this one reads it.
- [ ] Only one file outside `internal/observer` and the test trees was **added** on this branch:
      `internal/daemon/observer_ops.go`; the only file **modified** outside them is
      `internal/cli/daemon.go`, by Commit 6's wiring block and nothing else. The amendment's five
      edits — `internal/store/put.go`, `internal/cli/hookclient.go`, `internal/daemon/handlers.go`,
      `internal/daemon/options.go` and `internal/daemon/daemon.go` — are on the amendment branch.
- [ ] `git diff develop..HEAD -- internal/cli/daemon.go` shows exactly the two wiring blocks — the
      `daemon.WireObserver(&opts)` call before `daemon.New` and the
      `daemon.RegisterObserverIdleWork(d, obsv)` call after it — and no other change.
- [ ] SP-09's assigned obligation is discharged, not silently dropped:
      `git grep -n 'RefreshStaleness' -- internal/daemon/observer_ops.go` returns the wrapped
      `SessionStart` call, `TestWireObserver_SessionStartRefreshesStaleness` passes, and
      `git grep -n 'negknow' -- internal/observer` returns nothing — the call is in the daemon seam
      because the observer is forbidden the import.
- [ ] `internal/observer` declares **no** NodeID constructor, no `argsPreviewMax`, no args-preview
      key table and no `argsDigest` helper:
      `git grep -nE '"(tooluse|toolresult|assistant|userprompt|file|symbol|segment):|argsPreviewMax' -- internal/observer ':!*_test.go'`
      returns nothing. The pattern is one line on purpose: the earlier wrapped form pasted a newline
      and six spaces into the middle of the alternation, so the copied command silently tested
      something else. The exclusion pathspec replaces the old "outside test fixtures" clause, which
      required a human to read the output and so was not a gate.
- [ ] `nomagic` clean: every literal in `{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}` and
      `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}` inside `internal/observer` is
      either read from `config` or carries a `//nomagic:allow <reason>` comment. After this subplan
      the only such annotation in the package is the shipped `bytesPerKB = 1024` in `tombstone.go`;
      `120` no longer appears at all, because §5.8's `store.ArgsDigest` owns the preview cap.
- [ ] Commit count verified: `git rev-list --count develop..feat/sp08-observer-l0` is **7**, within
      the 5–8 band.
- [ ] `git log develop..feat/sp08-observer-l0 --format=%B | grep -Ei 'co-authored-by|signed-off-by|
      generated with|🤖'` returns nothing. **Do not add Co-Authored-By lines or any attribution
      trailers to any commit message.**
- [ ] `Qompack.md` is unmodified: `git diff develop..HEAD -- Qompack.md` is empty.
- [ ] Self-review pass: re-read `tooluse.go`'s 14 steps against the §8.1 responsibility list, and
      re-read `session.go`'s `OnSessionEnd` against §7.3's "Flush, compact the store, write session
      index" and §8.2's "run on `SessionEnd`", confirming both orderings match this document.
