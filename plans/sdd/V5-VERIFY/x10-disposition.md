# V5-VERIFY §4.10 — disposition

| | |
|---|---|
| Identifier (retained) | `TestV5_ThrashWarningVisibleInStatusAndCheckpoint` |
| Current criterion (plan §4 row 4.10) | SP-15 warning-only progress-aware dedup; self-generated warnings cannot feed back. |
| Disposition | **authored** |
| Level / file | e2e (real binary, real daemon through the v4 harness) — `test/e2e/v5_x10_test.go` |
| Base | `verify/v5` @ `87c0c1d` |
| Production changes | none |

## Producers on this tree

Two real producers exist for the row, and the test drives both:

1. **The shipped warning path.** `internal/grammar`'s Sequitur (`grammar.New()`), folded once per
   tool use by `internal/observer` (`tooluse.go` step 11), whose `Thrash(thrashMinUses)` result is
   queued per rule on PostToolUse (`prompt.go` `collectThrash`) and drained into UserPromptSubmit's
   `hookSpecificOutput.additionalContext` through `grammar.FormatWarning` — and only in
   `contract.ModeFull` (§12.1 record-but-do-not-act).
2. **SP-15's state-aware detector**, `grammar.Detector`, which is where the criterion's
   "progress-aware dedup" and "self-generated warnings cannot feed back" bounds actually live. It
   ships behind `runtime.selection.loopWarningsEnabled` (default `false`,
   `internal/config/defaults.go:185`) and no production composition root constructs it on this
   tree, so it is exercised in process and the switch is recorded as DISABLED, never as passed.

**Composition gap, recorded rather than papered over.** `internal/cli` never assigns
`daemon.Options.Grammar`; `scheduler_wiring.go:171` only defaults a nil one when it builds the
checkpoint `SourceSet`. The installed binary's observer therefore has a nil grammar and never
warns. The test's `x10v5StartRig` is `v4StartRig` with that one field set to a real
`grammar.New()` — the seam `daemon.Options` exposes and `WireObserver` already honours — and it
hands the same instance to the checkpoint `SourceSet`, which is exactly the composition
`wireCheckpointSources` describes for a non-nil `Options.Grammar`. Nothing else differs from the
shipped daemon.

## Test names and what each asserts

`TestV5_ThrashWarningVisibleInStatusAndCheckpoint` with three subtests:

### `sequitur_warning_is_warning_only_and_once_per_rule` (arm 1, ModeFull)

Through the real binary's `session-start`, `observe tool`, `observe prompt`, `status --json` and
the PreCompact seam:

- A fresh project starts in ModeFull (`status --json`) and the first prompt carries no context.
- After eleven Read→Edit→Bash cycles against one file (separated by distinct one-off tools — see
  "Corrections" for why), the next prompt hook exits 0 with `hookSpecificOutput.hookEventName ==
  "UserPromptSubmit"` and an `additionalContext` that is exactly one line, begins with
  `[qompack] possible loop:`, names the induced expansion `FileRead→FileEdit→Bash`, carries
  `repeated 4×` and ends with the observer's advice.
- **Warning-only:** `customInstructions` is empty, `continue`/`suppressOutput`/`systemMessage`
  are untouched, `records/eliminations.jsonl` has no line, the contract mode is unchanged, and
  the status report does not contain the warning prefix (status carries the mode, not warnings).
- **Once per rule:** the next prompt gets nothing; three more cycles keep the rule above threshold
  and the prompt after them still gets nothing.
- **Feedback edge (shipped path):** the delivered warning is quoted back verbatim as the user's
  prompt three times; no prompt produces a warning, and every echo is still captured as its own
  `UserPromptSubmit` index record (suppression is about the grammar, not recording).
- **Checkpoint:** PreCompact seals exactly `0001.json`, the manifest verifies, and neither the
  sealed checkpoint nor the PreCompact instructions contain the warning prefix; still no
  elimination record.

### `degraded_passive_records_the_loop_and_says_nothing_until_restored` (arm 2, the negative control)

- A `contract.Monitor` on `state/contract.json` is degraded to `ModeDegradedPassive` BEFORE the
  daemon exists (the same persisted seam v3_x08 and the SP-10 degraded row use); the daemon adopts
  it at `New`, and `qompack status --json` reports the degraded mode.
- One `session-start` (clean contract run 1 of 2) leaves it degraded.
- The identical eleven-cycle loop is replayed; the prompt hook emits **no** `hookSpecificOutput`
  at all, every tool use and the prompt are recorded exactly once (primary index records), and
  the mode is still degraded at that instant.
- A second `session-start` restores ModeFull (two consecutive clean runs), `status` shows it, and
  the very next prompt delivers the loop warning that was queued while degraded — proving the
  grammar recorded the loop the whole time without the test touching the grammar.
- Ends with `p.AssertAppendOnly(t)`.

### `state_aware_detector_is_progress_aware_and_cannot_feed_itself` (arm 3, in process)

- The loaded `config.Config` and `qompack config print --json` through the real binary both report
  `runtime.selection.loopWarningsEnabled == false`: the switch is recorded as disabled.
- `grammar.NewDetector(DetectorConfig{})` with the shipped bounds: a progress-free state warns
  exactly at `MinRepeats`, confidently, rendered through `FormatWarning`; deliverable at its turn
  and not after `ExpiresAt`.
- **Dedup:** the same state again in the window is `Deduplicated`, not re-warned.
- **Progress-aware:** an occurrence that observed progress silences the window and latches; later
  progress-free occurrences are charged to `ProgressSuppressed`.
- **Feedback edge:** six signatures carrying Qompack's own markers (the rendered warning in Goal /
  Failure / Action, an `mcp__qompack__` action, a `.qompack/state` target, a `/qompack:` goal) plus
  one explicitly `SelfOriginated` observation, each repeated `MinRepeats+1` times, never warn, are
  attributed to `SelfSuppressed`, and do not even count as `RepeatedStates`.
- **Warning-only outcomes:** `RecordOutcome` accepts one judgement and refuses a revision; a
  false-alarm judgement does not stop a genuinely new loop from warning; the session cap holds
  and is accounted as `CapSuppressed`; every phase sat inside one dedup window.
- Unknown coverage renders as an `Uncertain` warning with visibly different text.
- Switch-level control: `DetectorConfig{MaxPerSession: -1}` (the in-code equivalent of the
  config switch being off) delivers nothing on the stream that made the default detector warn.

## Negative control and how it was proven

Arm 2 is a real runtime switch, not a source edit: the contract monitor's persisted degradation.
The same loop that produced the warning in arm 1 produces no `hookSpecificOutput` in
`ModeDegradedPassive`, and the restore-then-warn tail proves the silence was the mode and not an
absent producer. Arm 3 additionally carries `MaxPerSession: -1`.

Non-vacuity was also observed the hard way during authoring: the first full run failed arm 2's
"recorded exactly once" assertion (66 index lines against 44 events) because supersession flips
(`store.MarkSuperseded`, op `supersede`, no `tool` field) are appended to the same file. The
assertion and the feed's ordering wait were corrected to count primary records only; the fix is
in the file's `x10v5Feed` comment. No source edit was needed to turn the test red.

## Old-to-new assertion map

| Historical expectation (§4.10 text) | Disposition |
|---|---|
| Prompt hook exits 0 and `additionalContext` contains `[qompack] possible loop:` | **kept** (arm 1; exact prefix, one line, expansion, advice) |
| `repeated 11×` | **corrected** to `repeated 4×`: the observer queues a rule the first time Sequitur's reference count exceeds `thrashMinUses` (3) and renders that queued `Rule.Uses`, so the count is 4 regardless of how long the loop ran. A pure periodic cycle never gets there at all (hierarchical folding keeps every rule at ≤ 3 references), which is why the replay separates cycles with distinct one-off tools. |
| Feed 11 thrash cycles through `observe tool` | **kept** (eleven cycles, plus three more for the once-per-rule tail) |
| The same cycle appears once in the checkpoint's action history | **retired**: `checkpoint.SourceSet` nil-checks `Grammar` (`source.go:81`) and nothing in `internal/checkpoint` reads it; the only non-test caller of `Compressed()` is the grammar's own snapshot. Replaced by the warning-only assertion that no durable surface (sealed checkpoint, PreCompact instructions, eliminations ledger) carries the warning. |
| `status --json` shows the thrash | **corrected**: no status field carries warnings; status carries the §12.1 mode. Arm 1 asserts the warning prefix is absent from the report, arm 2 asserts the degraded and restored modes through it. |
| Warning emitted once per rule; a second prompt does not repeat it | **kept** (arm 1, including after the rule stays above threshold) |
| With the monitor forced to `ModeDegradedPassive` the prompt hook emits nothing while the grammar still records | **kept** (arm 2, with the restore-then-warn proof of recording) |
| `status` shows the degraded banner | **kept** as "status reports `ModeDegradedPassive`" (arm 2) |
| (new criterion) progress-aware dedup | **added** (arm 3) |
| (new criterion) self-generated warnings cannot feed back | **added** (arm 1 echo prompts on the shipped path; arm 3 self-marker classifier and `SelfOriginated`) |

## Unverified remainder

- `grammar.Detector` is not wired into any production composition root on this tree, and
  `runtime.selection.loopWarningsEnabled` is `false` by default. Arm 3 records the switch as
  disabled and exercises the detector in process; **no end-to-end claim is made that the
  state-aware warnings reach a prompt hook**, because nothing on this tree delivers them.
- The shipped Sequitur path is exercised with `Options.Grammar` set by the test rig. The
  installed binary's `runDaemon` leaves it nil, so **the shipped binary never emits a thrash
  warning**; this is a wiring gap for SP-15/SP-17's owner, not something this row can assert
  around.
- No host-level (Claude Code) verification that `additionalContext` is injected; the row stops
  at the hook's stdout, as every v4_x row does.

## Run command and result

```
cd C:/Users/Quant/Documents/Programming/Projects/qompack-v5-x10
go test -list 'TestV5_Thrash' ./test/e2e
  -> TestV5_ThrashWarningVisibleInStatusAndCheckpoint
go test -count=1 -timeout=20m -run '^TestV5_ThrashWarningVisibleInStatusAndCheckpoint$' -v ./test/e2e
  run 1: PASS (23.70s) — arms 15.18s / 8.45s / 0.07s
  run 2: PASS (19.26s) — arms 11.15s / 8.05s / 0.06s
gofmt -l ./test ./internal            -> clean
go vet ./test/e2e                     -> clean
go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns -> all PASS
```

golangci-lint was not run here (coordinator runs it once).
