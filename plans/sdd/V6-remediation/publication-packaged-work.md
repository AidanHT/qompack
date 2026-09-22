# V6 publication accounting — packaged fault expectations (V6-RECOVERY-1)

Owner: V6 publication/recovery seat (claude-opus-4-8, high). Worktree `../qompack-v6`, branch
`verify/v6`. **No commit made. No packaged build/test run** (a capture is in flight; main runs the
packaged tests after integration). Exclusive files this round:

- `test/fault/publication_test.go` — flip the F4-1 and F4-4 row expectations.
- `test/fault/v6_fsck_test.go` — add the automatic-startup detection test.
- `plans/sdd/V6-remediation/publication-packaged-work.md` — this report.

Source files (`internal/store/publication_audit.go`, `internal/daemon/publication_audit.go`,
`internal/daemon/daemon.go`, `internal/cli/*`) are **main's now**; I did not touch them. Main wires
`LoudPublicationGaps` after `Drain` + `sweepCheckpointIntegrity` under a 250 ms context and surfaces
the publication counters + LOUD tail through `status --json`.

## 1. What changed and why

Main's startup accounting means the recovery daemon now **reports** the two publication gaps the
matrix pins as silent findings. Under the fault harness's verdict machine (`evidence_test.go`
`judgeRecovery`/`judgePin`), a row that the product now reports on a surface flips from `failed` to
`explicit_incomplete` — and a row that flips while still carrying a `Known` pin is a **test failure**
by design (`judgePin`'s "the product changed… delete the pin" arm). So the pins must come off, exactly
as the three checkpoint rows already had theirs removed when their startup Loud landed.

### F4-1 `object_written_index_line_absent`
- Removed `Known: "F4-1"`. Added row comment explaining the flip.
- The recovery daemon's startup accounting walks `objects/`, finds the object the dropped
  `roots.jsonl` line no longer indexes, and Louds `unindexed objects … found at startup`.
- Added token `"unindexed"` to `Names` (kept every existing token). The startup LOUD line reaches the
  harness on LOUD.log, the day log, and `status --json` `loud_tail`; `linesNaming` matches it
  case-insensitively — `"unindexed"` is the verbatim word, and the existing `"index"` also matches it
  as a substring.

### F4-4 `capture_sidecar_stage_one_only`
- Removed `Known: "F4-4"`. Added row comment.
- The recovery daemon's startup accounting classifies the stage-one `observe.tool` sidecar (outcome
  ok, durable bytes, unpublished — fsck calibration rule 2) and Louds `unpublished captures … found
  at startup`.
- Added token `"unpublished"` (kept `capture`, `sidecar`, `published`, `observation`).

**Why the flip is safe, not merely hopeful.** A row that is `failed` today satisfies
`NOT (recording && dangling_after == 0)` (else it would already be `recovered` and its pin would
already fail the suite). Adding `ev.Explicit()` therefore moves it to `explicit_incomplete` on the
`ev.Explicit()` arms of `judgeRecovery`, never to `recovered`. With the pin gone, `judgePin` is silent
on `explicit_incomplete`. Both added tokens pass `TestFault_LinesNamingIgnoresTheUniversalWarn`
(neither appears in the universal segment-tokens warn nor in any incidental line).

The historical "mirror" F4-1 row (`historical_test.go:261`, index line present / object **gone**) is a
**different** finding and is deliberately untouched: an absent object is a dangling index reference the
retrieval layer answers `unavailable` for, not an unindexed object the startup accounting surfaces.

## 2. New test — automatic startup detection (not recovery)

`TestV6_StartupAccountingReportsUnpublishedCaptureViaStatus` (in `v6_fsck_test.go`). It is the
automatic half the existing `TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence` (manual
fsck) is not:

1. `seedSession` through the packaged binary, stop the daemon.
2. `v6UnpublishToolCapture` returns a genuine published `observe.tool` capture (outcome ok, durable
   bytes) to stage one — the real crash-between-stages shape.
3. Fingerprint `objects/` and every sidecar (after the cut).
4. Restart the daemon with a bare `session-start` so its `Run` reaches the startup accounting; wait
   for it to answer.
5. `status --json` with the daemon up → assert the daemon's own `Snapshot` names the gap through a
   **publication counter** (`Counters[*publication*] > 0`) **or** the **loud tail** (`LoudTail`
   contains `unpublished`). Assert LOUD.log carries it too.
6. Assert **detection ≠ recovery**: a stage-one `observe.tool` sidecar still exists
   (`hasUnpublishedToolCapture`), and both the object fingerprints and the sidecar bytes are
   **unchanged** — the read-only accounting repaired nothing.
7. Record `explicit_incomplete` with a reason stating it is automatic detection, not recovery, and
   that recovery stays the operator's verified backup/restore path (main's ruling: reapplying a
   sidecar without the current authority is unsafe).

The counter-or-loud-tail assertion is an OR because I could not run the packaged binary to confirm
which of the two main's `status` Snapshot exposes; both are surfaced per main's note. If main wants a
strict counter-only assertion, tighten line ~142.

## 3. What main must run (I did NOT run these)

Assemble-and-run is a **packaged build** (`go run ./tools/devtool bundle` inside `assembledBundle`),
which the brief forbids this turn. Main runs, after the startup call is wired:

```
# unique new artifact directory per run; the recorder prunes and re-indexes it
QOMPACK_FAULT_ARTIFACTS=<unique new dir> \
  go test ./test/fault -count=1 -timeout=30m \
  -run 'TestFault_PublicationBoundaries/object_written_index_line_absent|TestFault_PublicationBoundaries/capture_sidecar_stage_one_only|TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence|TestV6_StartupAccountingReportsUnpublishedCaptureViaStatus|TestFault_LinesNamingIgnoresTheUniversalWarn'
```

- **Artifact env:** `QOMPACK_FAULT_ARTIFACTS` (`evidence_test.go:117`) → point at a fresh directory;
  `primeArtifactDir`/`prepareCollectDir` prune stale `*.json` and write `INDEX.json`. Unset = per-test
  temp dir (records not retained).
- **Regex** above targets exactly the affected rows + the token audit. `TestFault_PublicationBoundaries`
  is serial (one detached daemon at a time) by design; the two subtests can be named individually as
  shown.
- `-timeout=30m` and `-count=1` per the repo's fault-suite convention (bundle assembly alone can be
  minutes on a cold cache).

Expected records: `object_written_index_line_absent` and `capture_sidecar_stage_one_only` →
`explicit_incomplete`; `v6_fsck_unpublished_capture` and
`v6_startup_accounting_unpublished_capture` → `explicit_incomplete`. `TestFault_LinesNamingIgnoresTheUniversalWarn`
stays green with the two added tokens.

## 4. Confidence and what to check if a row does not flip

I authored against the source but could not execute (packaged build forbidden this turn); `gofmt` and
pinned `gofumpt -l` are clean on both touched files. Residual risks for main to confirm on the real
run:

1. **`ev` must capture the startup LOUD.** The flip depends on the recovery daemon's startup LOUD line
   reaching the post-cut snapshot (LOUD.log / day log / `status` loud_tail). If either row comes back
   `failed` with no pin (`judgePin` errors), the wiring/wording is the suspect: verify the startup
   call runs before serve, finishes within 250 ms on a seeded fixture (else it Louds the *incomplete*
   message, whose word is not `unpublished`/`unindexed`), and that the LOUD message still contains
   `unpublished captures` / `unindexed objects`.
2. **Status Snapshot surfacing.** The new startup test needs `status --json` to carry the daemon's
   `Snapshot` with `Counters` and `LoudTail`; confirm main's `status` wiring includes the
   `daemon.publication.*` counters (or at least the loud tail) when the daemon is up.
3. **Doc reconciliation (not my scope).** `judgePin`'s message and the historical evidence docs
   (`commit4-evidence.md §6`, `commit4-recovery-proposal.md`) still describe F4-1/F4-4 as pinned. The
   code pin is what the test enforces; the prose is main's to reconcile — I did not rewrite historical
   artifacts.

## 5. Remaining gaps, honestly

- **Detection, not recovery.** This round makes the product *surface* the two gaps automatically and
  proves it preserves bytes. It does **not** recover them, and the tests assert it must not. Durable
  acknowledgement / re-publish / operator-gated repair remain main's separate work; the recovery
  writer is owned by verified backup/restore (main's ruling).
- **Not fully fixed.** V6-RECOVERY-1 is not closed until the packaged tests pass on main's run **and**
  the recovery path exists. The `test/fault` records will read `explicit_incomplete` — the honest
  answer for a gap that is named but not reconstructed — not `recovered`.
