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
