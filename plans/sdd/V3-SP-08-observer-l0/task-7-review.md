# Task 7 review — Phase 1 exit-criterion harness, ADR 0008, gate runs

Reviewer: read-only pass over f1c7753..cd02d5e (one commit, cd02d5e), 2026-08-25.
All out-of-diff checks pinned to cd02d5e via `git show`/`git grep <sha>`; no working-tree
reads, no test suites re-run (worktree was quiescent at HEAD with only the untracked
`bench-observer.json`; reading raised no compile or behaviour doubt that required a run).

## Verdict

**Approved. No critical or important findings; three minor notes.**

The harness is honest work: the two SynthSpec literals and seeds are verbatim from the plan,
the four gate assertions sit at the design numbers with strong in-file documentation that they
must never be weakened, the sanctioned volatile refresh is implemented exactly as ruled and is
genuinely pure, and the ADR is a thorough, claim-checked record. The report/prep-report pair is
unusually transparent (the pre-ruling 0.969 failure is reported, not buried).

## Critical checks, one by one

1. **Design numbers not weakened** — `phase1ExitRatio = 4.0` asserted via
   `require.GreaterOrEqual(r.Stats.DedupRatio, phase1ExitRatio)`; `canonGapFloor = 1.25`
   asserted via `on >= off*canonGapFloor`. Both constants carry the "never weakened here;
   a genuine need to move it is a §11.3 sign-off" comment. VERIFIED.
2. **eventsFor builds ToolInput from tc.Paths, per-tool shapes** — `toolInputFor`:
   `{"file_path":paths[0]}` for file tools, `{"pattern":…,"path":…}` for Grep/Glob,
   `{"command":"go test ./..."}` for Bash/PowerShell, `tc.Args` kept when present, `{}`
   fallback. Matches the plan's prose exactly. `eval.ToolCall.Name/ID/Args/Paths` and
   `Session.ID/Turns` spellings verified against `internal/eval/types.go` at cd02d5e. VERIFIED.
3. **Same bytes per re-read path** — fileread/grep/glob/webfetch groups: file index =
   FNV-32a(paths[0]) % len(group), no refresh, so a re-read path re-serves identical bytes
   and FileRereadRate becomes real chunk reuse. VERIFIED.
4. **Ratio definition** — every ratio read is `store.Stats().DedupRatio`; raw is
   `Stats().RawBytes`. `internal/store/stats.go:41` confirms
   `DedupRatio = RawBytes/Bytes`; no other ratio definition appears in the file. VERIFIED.
5. **PathsReachTheObserver four guards** — `Stats().Files > 0`; AppendFileVersion proven via a
   non-empty `st.FileHistory(paths.Key(paths.Norm(root, p)))` for a driven path (a sound
   proxy — history exists only if AppendFileVersion ran); MarkSuperseded via the
   `observer.superseded` counter (name verified against `internal/observer/observer.go:202`,
   bumped per MarkSuperseded in `supersede.go`); HLL cardinality > 0 read from the injected
   `sketch.NewHLL`. VERIFIED.
6. **ResponseBytesAreReal** — `RawBytes / driven-PostToolUse-count > 1024` (strict greater,
   count from the driver not from Stats().ToolUses — the stricter reading). VERIFIED.
7. **Report artifact keys** — the eight plan keys, asserted by unmarshal-and-Contains in
   `TestPhase1_ReportArtifact`; `phase1-dedup.json` written in
   `TestPhase1_CanonicalizationGapOnTestOutput` regardless of pass/fail (write precedes the
   gate assertion). VERIFIED.
8. **StoreGrowthSublinear** — Stats().Bytes captured at the event-stream midpoint, second half
   = final minus first, `require.Less(second, first)` plus a first-half-positive guard. The
   midpoint sample is taken pre-Flush, which can only under-count the first half — the
   conservative direction. VERIFIED.
9. **ADR 0008** — records twelve resolved decisions (superset of the brief's eight), amendment
   (a)–(d), the shape-changing rulings (WireObserver seams, landedSubplans-at-merge,
   features.go move, import pin, reTestFail, tombstone alloc clause, suite lift, neardup
   placement, 7th supersession filter, parked ArgsPreview question, Task-6 deviations, and the
   T7 volatile-refresh ruling itself with its rationale), tombstone grammar, supersession
   rules, the measured dedup table (matches the test-log figures row for row), B-A/B-B with
   the local-Windows figures and the explicit CI-platforms-pending caveat, the B-C table with
   the 256KB breach recorded as SP08-D1, and the 96.2% coverage figure. VERIFIED.
10. **Commit message** — subject 54 chars after `test(observer): `, body factual and within
    line limits, footer exactly `Refs: SP-08, §10 Phase 1, §11.3, §8.1 performance budget`,
    no attribution trailer. VERIFIED.
11. **Scope** — the commit adds exactly `test/e2e/phase1_exit_test.go` and
    `docs/adr/0008-observer-l0.md`; nothing else in the range. `bench-observer.json` left
    untracked (consistent with the repo's convention — no bench json is tracked). VERIFIED.

## The sanctioned volatile refresh (controller ruling) — verified, not flagged

- **Pure and seeded**: values come from a splitmix64 stream over (seed, seq); the seed is
  FNV-64a of the session ID, itself a pure function of (Synthesize seed, spec). The file
  imports no time source for values (time is only the FakeClock step) and no math/rand;
  regexp ReplaceAllFunc traversal is deterministic, so the same (bytes, seed, seq) always
  yields the same output, and canon-on/off runs ingest byte-identical streams (RawBytes agree
  in the reported table: 7,815,160 both sides). VERIFIED.
- **Volatile classes only**: the six patterns are hh:mm:ss clocks, pid=NNN,
  "goroutine NNN [", fractional/ms durations, 0x… and @… hex addresses — all inside the
  configured strip set (config.Defaults() Strip = timestamps, ansi, pids, addresses,
  tmpPaths, durations — verified at internal/config/defaults.go:29). Matcher shapes
  cross-checked against internal/canon: generic.go:744/798/801 (pid, 0x, @),
  tools.go:296 (goroutine), numeric.go numClock/numISO and the numDurationIDs family.
  Length and shape are preserved per replacement (digit-for-digit, hex-for-hex, valid clock
  fields), so canon matches refreshed spans as it matched the originals. VERIFIED.
- **Applied to bash/testrunner groups only, per serve**; fileread/grep/glob/webfetch
  byte-identical per path. VERIFIED in corpusPayloadFor.
- **No committed corpus modified**: the commit touches no testdata/ path and
  `git status --porcelain -- testdata/` is empty. VERIFIED.
- **Documented with rationale**: a 25-line comment block explains the harness artifact
  (ratioOn ~= ratioOff by construction on byte-identical replays, measured 0.969), the §8.1
  premise, and the honesty constraints; the ADR carries the same ruling record and flags
  V3-VERIFY to re-check on real sessions. VERIFIED.
- Direction-of-error note: if any refresh pattern dirtied a span canon does NOT strip, both
  runs would store the dirt equally and only the canon-on ratio could suffer — i.e. any shape
  mismatch makes the 1.25x gate harder, not easier. The measurement cannot be inflated by an
  over-broad refresh regex.

## Adaptations from the plan's verbatim code — all justified, all recorded

- payloadFor -> corpusPayloadFor: name collision confirmed real
  (test/e2e/hooks_test.go:144 has `func payloadFor(t, event, cwd)`); the seed parameter is
  the ruling's requirement. Recorded in the prep report and in-file.
- Bash-group-per-spec carried by `withBashFrom(c, "testrunner")` instead of a spec parameter —
  faithful to the plan's intent within its binding signature. Recorded.
- Group table extended for the generator's real vocabulary (FileRead/Write/Test/Task/...),
  deterministic fallback to bash. Recorded.
- TestPhase1_HotPathBudgetDocumented accepts a local figure with CI platforms marked
  pending — matches the rulings header (bench-gate is CI-only; "record in the ADR which were
  run locally") and the review instruction's own Windows/CI-pending caveat. Not a weakening
  of a numeric gate: the enforcing gate remains CI's bench-gate job.
- TestPhase1_CorpusSweep is stricter than "fails only on panic" (the shared driver's
  require.NoError also fails it on hard errors) — stricter is acceptable.

## Gate runs

Not re-run (per review protocol; the implementer's report is the evidence): ci-local,
test-race, replay, build-all all exit 0; bench-hotpath B-A p99 2.048 ms < 15 ms, B-B p99
0.704 ms < 2 ms, flags verified against the harness; coverage 96.2% >= 75%. Every symbol the
harness consumes was existence-checked at cd02d5e (store.Stats fields, store.Deps,
FileHistory, eval.Synthesize/New/Load, observer.Options, sketch constructors, moduleRoot,
counter name), so the reported compile/pass evidence is consistent with the tree.

## Minor findings

1. **phase1Cache key omits the spec** (phase1_exit_test.go:418): the memo key is
   (seed, canonOn, bashFromTestrunner) — if a future test reused a seed with a different
   SynthSpec it would silently get the wrong cached run. Harmless today (the two seeds are
   distinct constants); worth folding a spec digest into the key if the file grows.
2. **volDur's bare-integer arm** (`\d{2,7}m?s`) can rewrite integer-second spans like `45s`
   whose canon coverage depends on context-bearing matchers; per the direction-of-error note
   above this can only make the gap gate harder. Cosmetic fidelity nit, no action needed.
3. **Read-heavy ratio magnitude is corpus-bounded**: many distinct synthesized paths hash into
   a small fileread group, so 189:1 reflects corpus reuse as much as store behaviour. This is
   the plan's own prescribed indexing scheme (plan-mandated design, not an implementation
   deviation) and the gate is >= 4.0; recorded so V3-VERIFY's real-session re-check (already
   flagged in the ADR) is understood to be the meaningful magnitude measurement.

## Residuals handed to V3-VERIFY (correctly declared by the implementer)

- CI platform bench figures + bench-gate/replay-gate/security/docs/crossbuild pending first push.
- Linux run pending CI (no usable WSL distro).
- faultinject_test.go:440 prose "TODO" (Task 6's file, not this task's to edit).
- Canonicalization gap measured under the ruled refresh; re-check on real sessions.
