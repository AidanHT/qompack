# SP-02 — carried defects

Six things about the replay harness that the 2026-08-22 plan audit established and the fix round
that followed did not close. Each has a row in `plans/CARRIED-DEFECTS.tsv`, and
`test/guards/carrieddefects_test.go` will not let `V3-VERIFY` write `plans/V3-report.md` while any of
them is still deferred to it.

**These are not defects in the harness's machinery.** The driver, the gate, the 2 % rule, the
sign-off trailer and the phase checks all work and are tested. What is recorded here is that several
of the NUMBERS the harness reports are measured over an input that cannot make them move, so the
number is real and the comparison it feeds is not. A gate that cannot fail is the class V2-VERIFY
spent five passes finding by hand, and these are the instances of it that survived wave 1 inside the
instrument itself.

**Why they are carried rather than fixed.** Every one of the first three needs a different synthetic
corpus, and a corpus change re-baselines every metric for every policy at once (ADR 0003: an
instrument change re-baselines, it never takes a `Sign-off:` trailer). Doing them one at a time means
several full re-baselines and several rounds of judging whether a number moved because the policy
changed or because the corpus did. They belong together, in one deliberate re-baseline, with the
before/after recorded — which is a checkpoint's work, not a fix round's.

The last three are decisions rather than repairs: each has two defensible answers, and picking one
silently is how a metric ends up meaning something other than what its name says.

---

## SP02-D1 — the corpus raises exactly one demand kind

**Symptom.** All 177 demands across all 39 compaction events in
`testdata/sessions/synthetic` are `DemandFileContent`. `DemandToolResult` and `DemandDecision` are
never raised, so `tool_edit_distance`, `re_attempts` and `decision_preservation` are computed over an
input that cannot exercise them.

**Cause, in two halves.** `internal/eval/blocks.go` raises `DemandToolResult` only for a reference
from a LATER turn across the cut, and `internal/eval/synth.go` has the synthetic subagent report back
on the next assistant turn — adjacent to its own `Task` call, so the pair never straddles a
compaction. Nothing in the generator re-references an elimination or a decision after a cut at all.

**Acceptance (V3).** The generator produces sessions where a subagent reports at least two assistant
turns after its call, where a compaction cut falls between the two, and where eliminations and
decisions are re-referenced post-cut; the corpus is regenerated with bumped seeds per ADR 0003; and a
corpus assertion states the minimum number of events raising each demand kind, so the property is
pinned rather than observed once.

---

## SP02-D2 — `file_set_jaccard` is pinned at 1.0

**Symptom.** Every policy scores exactly 1.0 on every session.

**Cause.** The only repair the replay loop performs (`internal/eval/replay.go`) re-reads the same
path key the demanding turn already touches, so the repaired file set is the demanded file set by
construction. A metric whose numerator and denominator are the same set is 1.0 for any policy,
including one that keeps nothing.

**Acceptance (V3).** Either the repair model can change the file set — a re-read of a different path,
or a comparison over multisets or order rather than sets — or ADR 0002 records the metric as
structurally inert, the gate stops treating it as a signal, and the report labels it so no reader
mistakes 1.0 for a good score.

---

## SP02-D3 — the Belady keep budget never binds

**Symptom.** The oracle's `rewrite_span_tokens` is 0 at every compaction event in the corpus, so OPT
is "keep everything demanded" and `fraction_of_opt` grades every policy against a ceiling no policy
has to work for.

**Cause.** The corpus's sessions never accumulate enough candidate tokens at a cut to exceed
`eval.DefaultKeepBudget`. The Belady computation is correct; it is the input that is degenerate.

**Acceptance (V3).** A corpus assertion states that Σ tokens(candidates) exceeds the keep budget at a
stated fraction of compaction events, and the corpus is shaped so it holds. Until then
`fraction_of_opt` is a real number against a trivial ceiling, and the phase-exit criteria that read
it inherit that.

---

## SP02-D4 — `pMin` iterates candidates, not all blocks

**Symptom.** `internal/eval/belady.go`'s `pMin` scans the candidate set to find the earliest dropped
block. §5.2 defines it over ALL blocks. The deviation is recorded in a code comment and nowhere else
— not in the plan, not in ADR 0002.

**Acceptance (V3).** Pick a side and make the other one match. Changing the code to §5.2 re-baselines
every affected metric per ADR 0003; keeping the code means amending §5.2's wording in the plan and
recording the narrowing in ADR 0002 with the reason. Either is fine; the current state — code and
specification disagreeing, with the disagreement visible only to someone reading the implementation —
is not.

---

## SP02-D5 — `decision_preservation` measures the wrong horizon

**Symptom.** It measures agreement over the post-compaction horizon, which deterministic mode fixes
at 1.0 by construction. §11.2 defines it as "pre-compaction decisions still recalled".

**Acceptance (V3).** Either redefine it over the kept pre-compaction decision blocks and re-baseline,
or relabel it everywhere it appears — the plan, ADR 0002, `TRACEABILITY.md` §6 and the report key —
so its name states what it measures. A metric whose name promises one property and whose body
computes another is worse than one that is absent, because it is cited as evidence for the property
it does not measure.

---

## SP02-D6 — the stock model skips the 4/3 host padding

**Symptom.** `hostPadTokens` has no production caller and `hostSkillBudget` is entirely dead, so the
stock policy's modelled context does not include the padding the real host applies. The plan's own
pseudo-code omits it too, so this is a specification gap rather than an implementation slip.

**Acceptance (V3).** Decide: model the padding (and with it the skills restore) and re-baseline, or
delete the two constants and record in ADR 0002 that the stock model deliberately excludes host
padding, with what that does to the comparison. A dead constant that looks like it is part of the
model is a trap for the next reader of it.

**V5-VERIFY disposition (2026-09-08): `wontfix`** for the remaining `hostSkillBudget` half. The
`hostPadTokens` half was fixed at V4 (table below) and stays fixed. What was found, in order:

1. **The premise the deferral rested on is wrong, and it is right to say so.** A `Session` can
   already record a skill invocation: `ToolCall.Name` is `"Skill"` (`internal/eval/types.go`,
   `ToolCall`). No §5.18 field is missing, so "Session is fixed by §5.18" was never the blocker.
   What forbids the model is the contract and the measurement, below.
2. **The contract that forbids it.** Qompack.md v1.5 (`62f1487`) rewrote §2.4 to "Earlier
   MicroCompact/Session Memory/Full Compact descriptions are historical motivation. This plan
   depends on observed hook contracts and recoverable Qompack state, not undocumented tier
   internals", deleted the step-7 list and the §2.7 "Invoked skill bodies" row, and
   `plans/00-ARCHITECTURE.md` §0.1 ("v1.5 migration amendment and precedence") rules "do not
   implement the old guarantees as new features". The 25K/5K figures now exist only in history:
   `git show 7f92af5:Qompack.md` line 88 (the pre-v1.5 §1.3 RC-3 list of *unvalidated* constants,
   "25K skills"), lines 141–143 (step 7) and 227 (the §2.7 row); the current §1.3 carries no RC
   list at all, and v1.3's revision log had already ruled §2.4 "unchanged and … not verified". A stock stage
   built on a retired, unverified number has no line left to cite, and it changes what `stock` —
   "the policy the Phase 0 baseline number describes" (`internal/eval/policy.go`), pinned by ADR
   0002 as "modelled per §2.3/§2.4/§2.5" — means for `testdata/baseline/phase0*.json`, which this
   checkpoint may not regenerate.
3. **What the comparison therefore does not measure.** `Blocks` mints no skill block and `Demand`
   has no skill kind, so a modelled restore could only *cost* stock, never earn it recall; and the
   Qompack-side harness policy sets `Skills` nil on purpose (`test/replay/policy_rehydrate.go`,
   `Graph, Rules and Skills are nil ON PURPOSE`). On the committed corpus (`SynthesizeNamed`; tool
   vocabulary `FileRead/Read/Edit/Write` plus `record_eliminated`, `internal/eval/synth.go:128`)
   the reservation would be identically zero. The exclusion is symmetric: fraction-of-OPT measures
   file, turn, tool-result, elimination and decision recall under an equal budget and says nothing
   about skill restoration on either side. Recorded in ADR 0002 ("What `stock` deliberately leaves
   out"), as the V3 acceptance asked.
4. **The production surface that covers skills restore instead.** `internal/rehydrate` item 6b,
   `buildSkillIndex` (G4.4), and the host body budgets `hostSkillBodyBudgetTokens` /
   `hostSkillTotalBudgetTokens` (`internal/rehydrate/items.go`), which — unlike the eval constant —
   have callers: the head-truncation and total-cap warnings. Run on this candidate:
   `go test ./internal/rehydrate -run TestSkillIndex -count=1 -v` — 6 PASS (`-list` confirms the
   six `TestSkillIndex_*` names: `RendersTheCompactIndex`, `DropsReportUnindexedSkills`,
   `WarnsOnHostHeadTruncation`, `WarnsOnHostTotalCap`, `BodyTokensErrorsAreSilent`,
   `NilIndexerReportsTheAbsence`); `go test ./internal/skills -count=1 -v` — 13 PASS. Artifacts:
   `rehydrate-skillindex.txt`, `rehydrate-skillindex-list.txt`, `skills-pkg.txt` in the session
   scratchpad.
5. **Code.** `hostSkillBudget` is deleted from `internal/eval/hostconst.go` — the trap the
   acceptance named — and the exclusion is written in its place.
   `TestCarriedDefect_SP02D6_StockIgnoresSkillInvocations` (`internal/eval/policy_test.go`) pins
   it: two sessions identical except that one tool call is named `Skill` yield equal keep-sets,
   with the invocation surviving only as an ordinary tool block and all five step-7 files still
   restored. It is a characterization of the exclusion, green from the start by construction, and
   it goes red the moment anyone adds a skill reservation or a skill restore to `stockPolicy`.
   No golden, baseline or corpus byte changed. Evidence:
   `go test ./internal/eval -run 'TestStockPolicy|TestHost|SP02D6' -count=1` and
   `go test ./test/guards -run TestCarriedDefects -count=1` (this row's subtests); other rows'
   `WaveReportRequiresResolution` subtests remain red on the base, as the caveat below records.

---

## V3-VERIFY dispositions (2026-08-26)

All six rows -> `deferred:V4-VERIFY`, together. The corpus re-baseline they jointly require is
SP-02-scale work no wave-2 subplan touched, and V3 measured every SP-02 gate green on the committed
corpus (B1-B10 all PASS, `stock.fraction_of_opt` 0.695164 strictly between null and oracle, the
Phase-2 criterion green, X5/X6 driver runs exit 0, baseline byte-reproducible twice). Regenerating
the corpus mid-checkpoint invalidates the Phase 0/1/2 baselines simultaneously; V4's checkpointer
replay integration re-baselines in any case, so the work lands there once, per ADR 0003. The six
rows travel as one unit because a single regeneration discharges D1/D3 and re-measures D2/D5/D6,
with D4's pMin alignment folded into the same re-baseline.

---

## V4-VERIFY dispositions (2026-09-08)

The V3 disposition above sent all six rows to V4-VERIFY as **one unit**, on the reasoning that a
single corpus regeneration discharges D1/D3 and re-measures D2/D5/D6. That regeneration has since
happened. The unit did **not** travel intact: five of the six are closed, one moved on, and each
closure is carried by a named test rather than by this document.

Source of record for status is `plans/CARRIED-DEFECTS.tsv`, whose shape and open-row evidence are
themselves guarded by `test/guards/carrieddefects_test.go`. This section records the outcomes; it
does not restate them as a second source of truth.

| Row | V3 disposition | **V4 outcome** | Evidence test |
|---|---|---|---|
| **SP02-D1** — the corpus raises exactly one demand kind | `deferred:V4-VERIFY` | **fixed** | `TestCorpus_RaisesEveryDemandKind` (`internal/eval/corpusshape_test.go:76`) — the acceptance made mechanical. The repair is the recall window in `internal/eval/synth.go` (its own comments name D1 and D3 as one mechanism, not two). |
| **SP02-D2** — `file_set_jaccard` is pinned at 1.0 | `deferred:V4-VERIFY` | **fixed** | `TestCarriedDefect_SP02D2_FileSetJaccardIsNotStructurallyOne` (`internal/eval/corpusshape_test.go:199`) |
| **SP02-D3** — the Belady keep budget never binds | `deferred:V4-VERIFY` | **fixed** | `TestCorpus_BeladyBudgetBinds` (`internal/eval/corpusshape_test.go:114`) |
| **SP02-D4** — `pMin` iterates candidates, not all blocks | `deferred:V4-VERIFY` | **fixed** | `TestCarriedDefect_SP02D4_PMinIsMeasuredOverCandidates` (`internal/eval/corpusshape_test.go:142`) — the narrowing is now **pinned by a test** rather than by prose, which is the shape D4 asked for. |
| **SP02-D5** — `decision_preservation` measures the wrong horizon | `deferred:V4-VERIFY` | **fixed** | `TestCompare_DecisionPreservationIgnoresHorizonAgreement` (`internal/eval/divergence_test.go:194`) — D5's characterization, inverted. |
| **SP02-D6** — the stock model skips the 4/3 host padding | `deferred:V4-VERIFY` | **split, and moved to `deferred:V5-VERIFY`** | No evidence test. The `hostPadTokens` half **is fixed**: the stock model now applies the 4/3 padding (`internal/eval/policy.go:198` names this as D6's repair, and `policy_test.go:80` checks the floors against the padded estimate). The `hostSkillBudget` half is **still dead** and cannot be modelled without a `Session` that records skill invocations — and `Session` is fixed by Qompack.md §5.18. That is a specification dependency, not effort, which is why it moves rather than closing. |

### SP06-D2 — **open**, and still V4-VERIFY's

`SP06-D2` is not an SP-02 row and did not travel with the unit above; it is recorded here because
V4-VERIFY inherited it in the same handoff (`docs/adr/0100-v3-verification.md:45-58`).

**Status: `deferred:V4-VERIFY`, open, no evidence test.** The `PutBytes` cold and warm budgets
(3 ms / 400 µs) are **9x and 19x over on Windows** and have **never been measured on the reference
platform**. Two things follow, and neither is discharged by this unit:

1. It is **one defect at two layers**, paired with `SP08-D1` (`BenchmarkOnToolUse_TestOutput256KB`:
   `OnToolUse` over a 256 KB tool result breaches B-C at 53–115 ms on the one-changed-line case and
   262–655 ms when every chunk is novel, dominated by `store.PutBytes`'s per-novel-chunk object-write
   path). Closing either half alone would be a partial answer to a single performance question, so
   the two must be decided together — budget-vs-implementation.
2. The reference-platform half is **not measurable on this host**. It needs the ubuntu/macOS arms of
   G2, i.e. CI or the WSL2 route, and it sits behind the same V3 J5 billing waiver that stands
   **waived-open**.

**Do not score SP06-D2 from a Windows-only run.** A Windows number confirms the breach that was
already recorded; it does not answer whether the budget or the implementation is wrong.

### Caveat on the manifest guard

`test/guards` carries pre-existing red carried-defect subtests on this candidate. They are a known
condition of the base, not a product of these dispositions, and this unit deliberately did not chase
them: `test/guards` is outside its ownership. A V4 gate report must score them on their own evidence
before treating the manifest as green.
