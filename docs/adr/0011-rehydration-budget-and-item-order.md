# ADR 0011 — Rehydration budget, item order, and whole-rule restoration

- **Status:** accepted; amended 2026-09-22 by §21 (owner decision D5: the payload fits the host's
  10,000-character additionalContext cap), 2026-09-30 by §22 (owner decisions D49 and D50:
  current authority, tier-1 order, fallbacks and pointer privacy) and 2026-10-02 by §23
  (coordinator decision D59 and D50: a loss is never silent, and argument summaries and checkpoint
  drop entries are judged as paths)
- **Date:** 2026-09-06
- **Subplan:** SP-11 (L5 rehydrator)
- **Design:** `Qompack.md` §8.6, §8.7, §6.9, §4.4, §12; `plans/00-ARCHITECTURE.md` §5.15, §11.5, §11.6, §12.1

L5 is the read end of Qompack. Everything L0/L1/L2/L4 wrote to `.qompack/` is worthless unless
something puts the right 8–12K tokens back into the context window at the moment compaction has
just destroyed it. This record covers the decisions that were live while building it — the ones a
future reader would otherwise re-litigate from the code.

---

## 1. The ten `ItemKind` constants are declared in rendered order

§8.6 numbers eight items. Its "Instruction restoration" clause calls for two more things — the
path-scoped rules and nested `CLAUDE.md` files the host does not restore (G4.1, G4.2), and the
compact skill index it never re-injects at all (G4.4) — and both belong between items 6 and 7:
they are restored *instructions*, more important than a drop report and less important than the
pointers that selected them.

Declaring them after `ItemAffordance` and rendering them in the middle would emit the kind
sequence `0,1,2,3,4,5,8,9,6,7`. The inherited conformance case `runItemOrderCase` rejects that
outright — it requires items to be emitted in strictly ascending `ItemKind`. So the constants were
inserted into the middle of the `iota` block, which required a §0 amendment to
`00-ARCHITECTURE.md` §5.15 because the shipped block carried the instruction "do not insert into
the middle".

The amendment landed on `develop` before this branch was cut. The payoff is that `renderOrder`
*is* the `iota` sequence, so both of `runItemOrderCase`'s assertions hold by construction rather
than by discipline, and a test asserts `renderOrder[i] == ItemKind(i)` for all ten so the §8.6
importance ordering cannot drift.

## 2. `Item.Rank` is 0-based and counts emitted items

Fixed by the same conformance case: `require.Equal(t, i, it.Rank, "Rank must be the item's own
position in the emitted order")`. An omitted kind therefore consumes no rank — a payload with no
eliminations has its decisions at rank 2, not rank 3. The emitted `Items` slice is a subsequence
of `renderOrder`, never a sparse array over it.

## 3. `Request.Budget` is a hard cap that is never raised

This is the single most load-bearing decision in the slice.

`clampBudget` may **lower** a request and may **fill in** an unset one. It may never raise one,
not even to `runtime.rehydrate.minTokens`. Four independent sources say so: `00-ARCHITECTURE`
§5.15 calls it "hard cap from config"; the shipped contract on `Result.Tokens` says "which never
exceeds `Request.Budget`"; `runBudgetCase` asserts `Tokens <= budget` at 9000, 600 **and 1**; and
`runDegradeCase` repeats it at 1.

The consequence is that **`minTokens` is a fill target for an unset request only**, not a floor on
every request. That is exactly what §8.6's "target 8–12K" means: the daemon's own call passes no
budget and fills to the ceiling, the min-fill pass pulls the payload up toward `minTokens` inside
that ceiling, and a caller who explicitly names 600 tokens gets at most 600. A payload that
stopped at 3K while 9K of ranked material was available and the cap was 12K would be leaving
quality on the table; a payload that overran a caller's stated 600 would be a contract violation.
Min-fill resolves the first without ever causing the second.

## 4. "Never truncated" means never *partially* emitted — not "emitted over the cap"

§8.6 says invariants are emitted "verbatim, always", and §8.5's checkpoint schema comments its
tier 1 "never truncated". Read as a licence to overrun the budget, those would contradict the hard
cap above.

They do not. §8.6's own preamble introduces the eight items as "filled in importance order **until
the budget is reached**", so the list it introduces is by its own terms a fill-until-budget list:
"verbatim, always" fixes *how* an admitted item is rendered and *where it ranks*, not whether the
cap binds. The "tier 1: never truncated" comment sits inside §8.5's description of the checkpoint
**artifact on disk** — what L4 writes — not of what L5 injects.

So when tier-1 material cannot fit, each item is admitted whole while it fits, the ones that do
not fit become drop entries, `Degraded` is set and a `Loud` is emitted. Nothing is emitted
partially and nothing is emitted over the cap. A payload that overran its budget is one the host
may refuse outright — an all-or-nothing loss where a partial one was available.

Sections 18 and 19 record the two places where holding that line turned out to need more than the
fill pass alone: what the last-resort re-truncation evicts, and how much of item 2 is tier 1 in
the first place. Both were found by reading the frozen payload goldens rather than by a failing
assertion, which is the argument for having recorded them at all.

## 5. The wrapper overhead is charged to the first emitted item

`Result.Tokens` must equal Σ `Items[i].Tokens` exactly; `runBudgetCase` asserts it at every
budget. An earlier draft of the plan recorded a synthetic `ItemStat{Kind:"overhead"}` row, which
makes the total exceed the sum by construction and fails the suite everywhere.

The open tag, document header and close tag are therefore **reserved** before filling (so the
first item can never push the payload over the cap) and **attributed** at render time to whichever
kind turns out to be emitted first. There is no overhead row in `Result` and none in the state
file.

## 6. Six discretionary shares, not five

| Item | Share |
|---|---|
| 2 — user-intent *evolution* | 10% |
| 3 — eliminations | 25% |
| 4 — decisions | 20% |
| 5 — current work | 10% |
| 6 — pointers | 20% |
| 6a — restored instructions | 15% |

`Checkpoint.UserIntent.Evolution` renders as truncatable units carrying
`DropEntry{Kind:"user_intent_evolution"}`, so it must be allocated like any other discretionary
item — the *original* intent unit is tier-1 and is admitted whole before any share is computed.
Without a share of its own, evolution would be starved by whichever item filled first. The
constants are ordered to match `renderOrder`, not by magnitude, and unused allowance carries
forward to the next item so a cheap item does not waste its share.

Item 6b (skill index) and item 7 (drop report) take **reserves**, not shares: the skill index is
budgeted by `runtime.rehydrate.skillIndexTokens` directly, and the drop report reserves `B/10`
because a truncated report is the one thing that would make every other truncation invisible.

## 7. Prefix truncation, never cheapest-first

Within an item, units are admitted in builder order and the **first** unit that does not fit ends
the item. Skipping ahead to a smaller unit that would fit is forbidden.

This is §6.9's embedded-coding principle: order the bitstream by importance so truncation at *any*
point yields the best available reconstruction for that budget. Cheapest-first packing would
produce a payload whose contents depend on the budget in a way no reader could predict, and would
break the monotonicity property `PropBuild_MonotoneInBudget` pins — the item set at a smaller
budget must be a prefix-wise subset of the set at a larger one.

## 8. A path rule is restored whole or not at all

Item 6a is the one place progressive truncation *within* a unit is deliberately not applied. A
rule either fits entirely or is dropped entirely and reported.

The reason is G4.3. A half-restored instruction set is worse than an absent one: the agent cannot
tell that it is reading half a rule, so it follows the half it has with full confidence. An absent
rule at least appears in the drop report, where the agent can ask for it. This is the same
judgement §2.7 records about the host's own head-first skill truncation — the difference is that
here we control it, so we decline to do it.

## 9. `NestedClaudeMD` walks ancestors

`Qompack.md` §8.6 said "in a directory **containing** a pointer-set file". The shipped
`internal/rules/rules.go` doc comment said "containing (**or ancestor to**)", and the executable
`rulestest` fixture puts `CLAUDE.md` at `src/pkg` with its pointer at `src/pkg/deep/thing.go` —
two levels down — asserting exactly one match.

The narrow reading did not describe a smaller feature; it described a feature with a hole in the
middle of its stated purpose. A nested `CLAUDE.md` governs its whole subtree in Claude Code, so a
rule two directories above a pointer is exactly as lost after compaction as one beside it, and
restoring only the latter would let §9's G4.2 row claim a closure it had not achieved.

The walk therefore climbs from the pointer's directory toward the root, stopping **before** the
project root (the host re-injects that file itself, §2.7) and bounded at 32 levels. Results are
deduplicated by path and sorted ascending, which yields outermost-first order — the general rule
before the specific one that refines it, which is the order Claude Code applies them in.

This required revising the design of record. `Qompack.md` went to **v1.4**;
`plans/QOMPACK-ERRATA.md` carries the full record of the conflict, the evidence on each side, and
why the documents moved to the code rather than the other way round.

## 10. The original intent is resolved by derived id, never by `Store.Search`

Item 2 is the G7.3/G2.3 closure, and it resolves this session's first prompt through
`Store.ToolUse(ctx, "prompt_<session>_0")` — the id scheme `observer.VerbatimPromptID` mints.

`Store.Search` would be the obvious alternative and it is actively wrong here. `FSStore.Search`
clamps `K` to 100 and ranks project-wide candidates newest-first, so the earliest prompt of a long
session is the first thing the window excludes. The failure is not benign: the filtered candidate
set is not empty, it holds this session's *later* prompts, so a relevance search would inject a
mid-session prompt **as** "the verbatim original user intent" and then override the checkpoint's
genuine original with it. That inverts the exact gap this item exists to close. A derived id is
exact, is O(1), and cannot degrade in that direction.

The record is sanity-checked before it is trusted — `rec.Session` must match and `rec.Turn` must
be 0 — because a drifted id scheme should produce a documented fallback and a `Warn`, not a
confidently-injected prompt from another session. When L0 and the checkpoint disagree, **L0 wins**
and the disagreement is `Loud`: that is the mechanical guarantee that no summary-derived intent
can reach the context window.

The transcript is never read. That is what closes G7.5 as a side effect: when the summarization
call fails by calling a tool instead of summarizing, the injection is unchanged, because it was
never derived from the summary in the first place.

*Review correction (2026-09-29, V6 close-out wave 13, D45).* The Phase 4 live lane found three
ways item 2 misrepresented the user's intent, and all three are corrected:

1. *The capture is read whole.* Item 2 read the L0 record through an 8,192-byte cap and quoted the
   prefix under the "verbatim" heading: a 17,774-character brief reached the model cut mid-word,
   the prefix fitted so nothing was named, and it never equalled the checkpoint's whole copy, so
   every compaction logged a spurious mismatch (F-UAT04-1). The record is now read up to
   `intentReadLimit`, three UTF-8 bytes per host character times the D5 ceiling, past which no
   text can be emitted whole. A readable original is one tier-1 record, admitted whole or named
   with `expand(tool_use_id=prompt_<session>_0)`; a longer capture is named the same way and
   compared with nothing.
2. *Evolution is every later prompt.* The checkpointer filled `user_intent.evolution` only from
   closed segments, and a session's segment closes at SessionEnd, so no correction made in a live
   session ever reached a checkpoint (F-UAT05-1, F-UAT06-2). It now recomputes the session's
   intent from its own prompt records at every refresh and just before each seal, keeping the
   newest whole entries, at most 64 and at most `checkpoint.EvolutionCeilingChars` (this ADR's
   payload ceiling, the most one rehydration can carry) of text, and naming how many earlier ones
   it left out with the expand call for the newest and the oldest. Tier 1 is never truncated, so
   the size bound is what keeps a session of long pastes from costing a checkpoint its pointers
   and decisions for restatements item 2 could never admit.
3. *A fork keeps its parent's original.* A session started with `--fork-session` has a new id,
   so `prompt_<fork>_0` is the fork's own first prompt, and L0-wins overrode the parent's original
   with it (F-UAT06-1). The daemon records the fork's lineage when it starts
   (`state/lineage-<session>.json`, the session last prompted before it started being the one it
   continues); the checkpointer inherits what that session had said before the fork, from its own
   prompt records, and item 2 verifies the original against
   the ORIGIN session's `prompt_<origin>_0`, opens the unit with a line naming that session, and
   renders the fork's own first prompt as an evolution delta. L0 still wins, loudly, when that
   session's capture and the checkpoint disagree. Another session's checkpoint no longer seeds a
   new unforked session's intent at all, which is what made every later session in a project
   report a mismatch at its first rehydration.

## 11. The no-contents guard covers items 4–6 only

`guardNoContents` rejects a unit containing a fenced code block or exceeding six lines, and it runs
over decisions, current work and pointers — the checkpoint-derived prose where §13 invariant 5's
"no snippets" rule applies at the injection boundary.

It must **never** inspect items 1, 2 or 3. A real user prompt routinely runs to twenty lines and
routinely contains a fenced stack trace or a pasted diff. Running the guard over item 2 would drop
the original task statement *for containing a code fence*, silently re-opening G7.3 by precisely
the mechanism this subplan exists to close. Item 3's units are already bounded by a 240-rune reason
truncation. So: fences and line counts are checked on checkpoint-derived prose; verbatim human
text is never inspected and never rejected. `TestUserIntent_FencedPromptSurvivesVerbatim` is the
regression guard for the guard.

## 12. Item 3 reads both elimination scopes

`eliminations.defaultScope` (default `"session"`) governs the scope *new* records are written
with. It is not a read filter, and treating it as one would drop exactly the longest-lived
negative knowledge in the store: §8.3 says project-scoped eliminations "persist and warm-start
future sessions". Item 3 therefore unions `Active("session")` and `Active("project")` with the
checkpoint's own `eliminated[]`, deduplicated by record ID with the ledger copy winning because it
carries current `Status`.

Stale records are included and flagged, not hidden, when `staleResponse` is `"flag"` — §8.3's
verbatim note ("previously eliminated, but the evidence has changed since — re-verification may be
warranted") is strictly more useful to the agent than either a block or silence.

## 13. The standing instruction belongs to item 3, and is absent when item 3 is

§8.7's design note is unscoped: surface `already_tried` as a standing instruction, "otherwise the
affordance exists and goes unused". `00-ARCHITECTURE` §5.15 is scoped: `StandingInstruction` is
"item 3's companion". The shipped conformance case executes the scoped reading — with no item 3 it
asserts the sentence appears nowhere in the payload.

The scoped reading is what shipped. Telling an agent to call `already_tried` before committing to
an approach when nothing has ever been eliminated is noise that costs budget and trains the agent
to skip the line, and item 8's affordance notice still names the tool. The divergence is real,
known, and recorded in `plans/QOMPACK-ERRATA.md` rather than resolved by a second `Qompack.md`
revision: §9's G6.2 mitigation row is conditional on a non-empty ledger at `SessionStart`, and that
conditionality is now written down.

## 14. `Build` takes no clock and is a pure function

`Deps` carries no `Clock` field, which also keeps it field-for-field identical to §5.15. The
payload carries no timestamp, `PropBuild_Deterministic` requires byte-identical output for
identical input, and the only wall-clock value in the slice — `State.Emitted` — is stamped by the
daemon service from `RehydrateOptions.Clock`. A golden that changed between runs would be a bug,
not a fixture refresh.

## 15. This slice mints no sentinel

§12.1's `hook.additional_context_delivered` probe is SP-05's shipped mechanism: the daemon's
`session.start` route calls `contract.MintSentinel` (seeded on the **mint timestamp**), appends
`contract.RenderSentinel` **after** the injection's close tag, and later scans the transcript tail
for the token. Nothing reads a rehydrate state file to find it and nothing recomputes it from
`(session, seq)`.

An earlier draft of this plan specified a `rehydrate.Sentinel` reusing the identical domain string
`qompack.sentinel.v1` with a different seed and prefix, which would have put two mutually
incompatible "sentinels" in one process under one domain. There is no `sentinel.go` in this slice
and no `"sentinel"` key in the state file. What this branch owes §12.1 is exactly one thing: a
non-empty `additionalContext` produced by a bound `Services.Rehydrate` seam, which is the sole
condition `DeclareProducers` tests.

The probe deliberately sits **outside** the injection tags. That is not a bug to be tidied: the
probe is a contract observable, not injected context, and `checkpoint.StripInjections` must not
swallow it.

## 16. The `clear` branch touches no ledger state

`OnClear` deletes the rehydration state file and nothing else. A `/clear` is not a session end:
§8.3 says session-scoped eliminations "die with the session", and the session has not ended. The
store, ledger, DAG, sketches and checkpoints are all left alone, and SP-08 owns whatever
session-registry effects a clear has.

---

## 17. The phase-3 gate passes its assertions; corpus re-collection is deferred

`replay --phase 3` over the committed 24-session synthetic corpus reports:

```
phase 3: tier-1 drops 0/78 builds; max rehydration 3200 tokens; max residual 3172 tokens
```

`runPhaseChecks` executes before `checkCorpusFreshness` and prints one `FAIL` per failing
criterion; the only `FAIL` the run emits is the freshness one. A1, A2, A3 and A4 therefore all
pass — the rehydrator spends less than stock, diverges later, keeps the residual span inside
`checkpoint.frontier.maxResidualTokens`, and dropped no tier-1 material in 78 builds.

What fails is §11.4's overfitting guard: the corpus records `regeneratedAfterPhase: 0`, and
`corpusStalePhases` is 2, so asserting phase 3 against it is refused. That is bookkeeping about
the corpus, not a statement about L5.

One number in that gate needs reading with its scope attached. The replay policy wires no rule
scanner and no skill indexer, because the corpus is a set of TRANSCRIPTS and not a set of project
checkouts — there is no tree on disk for either to scan. Phase 3's "max rehydration 3200 tokens" is
therefore measured WITHOUT items 6a and 6b, and it understates the payload a real session gets: the
`full-12k` golden, with seven restored rules, is 8034 tokens. Both numbers are inside the 12K cap,
which is what the gate asserts; the smaller one is not the whole picture, and a later reader
comparing it against the goldens should not conclude that one of them is wrong.

**This branch deliberately does not discharge it.** ADR 0003's protocol re-writes all 24 committed
sessions *and* `testdata/baseline/phase0.json`. Running it here would have SP-11 move the very
yardstick it is measured against, on its own feature branch — and SP-10, SP-12 and SP-13 all land
after this one, so a single re-collection once the wave closes serves all four where four separate
ones serve none. Raising `corpusStalePhases` was rejected outright: editing the check instead of
satisfying it is precisely the failure ADR 0003 exists to trigger on.

The wave-3 coordinator owns the re-collection, and the phase-3 gate is green the moment it lands.

## 18. The hard-cap re-truncation evicts by importance, not from the tail

*Superseded in its eviction order and its golden description by §22.4. The reason below — never
evict the drop report and the retrieval line first — still stands.*

The fill pass is supposed to keep `Result.Tokens <= Budget` on its own. It can still be beaten,
because two admissions are deliberately unconditional: tier 1 (§4) and the fixed units that carry
no drop entry — chief among them item 3's standing instruction, which must survive whatever
allowance item 3 was given, since an elimination list without "call `already_tried`" is a list the
agent has no reason to act on. At a budget far below the §8.6 band those admissions can exceed the
cap between them.

`Build` therefore closes with an unconditional re-truncation loop. The obvious implementation
drops the last emitted item until the total fits — and that is wrong in exactly the case the loop
exists for. The tail of `renderOrder` is item 7, the drop report that says what was lost, followed
by item 8, the affordance line that says how to ask for it back. A payload small enough to trip
the loop is a payload that has already lost most of its material; evicting those two first leaves
the agent with no idea that either the loss or the recovery path exists. That inverts §8.6
precisely, and it does so at the worst possible moment.

The loop evicts the **last non-tier-1 item**, falling back to the last item only when every
remaining one is tier 1. Ranks are renumbered after each eviction, because `Item.Rank` is the
position among emitted items (§2) and removing from the middle leaves a hole in it.

The 400-token `degraded` golden is the frozen evidence: items 4, 5, 6, 6a and 7 are all gone, and
items 1, 2, 3 (heading and standing instruction), 6b and 8 remain.

## 19. Item 2's tier-1 admission is the verbatim original only

*Superseded by §22.2: item 2's tier-1 slice is now the original and the newest restatement. The
argument below against admitting every delta in tier 1 still stands.*

`tier1Order` names `ItemUserIntent`, but item 2 is not one unit. It is the verbatim original
prompt followed by the checkpoint's `user_intent.evolution` deltas, and only the first of those is
what §8.6 pins as "verbatim, always" — the deltas are a summary of how the ask moved, which is why
`ItemUserIntent` also appears in `shareOrder` and why the share pass refills it from `units[1:]`.

Admitting the whole item in the tier-1 step is wrong twice over. The deltas bypass the budget
entirely, so a checkpoint with a long evolution list crowds out items 3 through 6a without ever
being charged for it. And the share pass then re-fills the same deltas, drops them for want of
allowance, and writes drop entries for units the payload is still rendering — a section 7 that
names material the reader can see three lines above it.

`tier1Units` is the one-line answer: every kind admits all its units except `ItemUserIntent`,
which admits `units[:1]`. This too was found by reading a golden — the 400-token payload showed
two evolution lines it had no budget for, and named both of them as dropped.

## 20. The V4/V5/V6 test names are a contract, and two of them were corrected

`devtool lint`'s `runpatterns` check reads every `go test -run` command in `plans/` and fails a row
whose pattern matches no test — because such a row prints `ok`, exits 0, and verifies nothing. The
check holds a package's rows back until its owning subplan lands, so registering SP-11 in
`landedSubplans` is what turned fifteen rows on at once.

Thirteen of them named tests this slice should have had and did not, or had under a different name.
Those were written or renamed, and `internal/rehydrate/verify_test.go` collects the ones the
verification documents run by name so that a future rename fails in one obvious place. Two of them
were worth more than a rename:

  - `TestBuild_SmallerThanStock` turns §2.4's "50K + 25K" into a number a test fails on. The claim
    that L5 is cheaper than the host's own eager restoration had been argued in prose everywhere and
    asserted nowhere.
  - `TestBuild_NoTranscriptRead_ClosesG75` turns G7.5 into a checked property. It asserts the store
    is never searched AND that neither `Request` nor `Deps` carries a transcript or summary field —
    so a future field would fail the test before any code could read from it.

Two names were corrected in the plans instead, because satisfying them would have meant asserting
something false:

  - `TestRenderOrder_EightNormativeKindsInIotaOrder` predates the §0 amendment that made the set TEN
    kinds (§1). The row now runs `TestRenderOrder_IsIotaOrder`.
  - `TestBuild_RankIsOneBasedAmongEmitted` contradicts §2, §5.15 and the inherited conformance case,
    all of which fix `Rank` as ZERO-based. The row now runs
    `TestBuild_RankCountsEmittedItemsFromZero`.

Writing a test whose name asserts a contract the code does not have would have satisfied the gate
and left the next reader with two documents disagreeing about what `Rank` means.

One more divergence was found and deliberately NOT corrected here: V4-SP11-04's expectation text
still calls `_DoesNotWalkAncestors` "the literal §8.6 reading". Qompack.md v1.4 widened that
sentence and `plans/QOMPACK-ERRATA.md` records why; the row's `-run` pattern still resolves, so it
is a stale expectation rather than a dead command, and correcting a wave-4 verification expectation
is the coordinator's call rather than this branch's.

## 21. Amendment (2026-09-22, owner decision D5): the payload fits the host's additionalContext cap

**What changed.** Claude Code delivers a hook's `additionalContext` whole only up to 10,000
characters. Past that it saves the text to a file, hands the model the file path and a
2,000-character preview in its place, and does not ask it to read the file; the cap "has no setting
or environment variable to raise it" (`testdata/host/hooks-output-schema.json`, `limits`). A real
2.1.280 session confirmed it: an 11,082-character SessionStart context reached the model as a
2,391-character `<persisted-output>` block, and the model could quote only the preview
(`plans/sdd/V6-closeout/packaging/evidence/review/f2-live-host-cap-probe`). The payload §3 bounds in
tokens — up to `runtime.rehydrate.maxTokens` 12,000, about 48,000 characters — rendered the frozen
`full` fixture at 32,049 characters (`plans/sdd/V6-closeout/rehydrate-cap/evidence/`). Everything
after the first 2,000 characters, sections 3–8 included, would not have reached the model.

The owner decided (D5, `plans/V6-CLOSEOUT-CHECKLIST.md`) that the rehydration must fit under the
cap, as a priority-ordered payload of whole records with the rest reported as overflow plus
pointers. `Qompack.md` v1.6 §8.6 records the design change; this section records how it is built.

**The ceiling.** The whole SessionStart(source=compact) `additionalContext` — wrapper, headings,
separators, every handle, the overflow report, and the §12.1 contract probe the daemon appends after
the close tag (§15) — is at most **9,500 host characters** (`rehydrate.HostContextCeilingChars`),
500 under the host's 10,000. `Result.Text` gets 9,400 (`PayloadCeilingChars`), leaving 100 for the
probe line, which is 62 characters for every session id; `TestHostCeiling_ProbeReserveCoversTheProbeLine`
pins the margin against `contract.RenderSentinel` itself, because rehydrate may not import contract.

It is a host constant, not configuration. No key raises it: raising it could only put the payload
back behind the host's file-path fallback. `Request.Budget` and §3 are unchanged, and the token
budget can still bound the payload lower — the `token-bound` golden is that case — but at the shipped
defaults the character ceiling (about 2,400 tokens of prose) binds long before 8–12K tokens do.

**The unit.** The hooks reference says "characters". The host's own code says which: 2.1.280 tests
`field.length <= 1e4` on the parsed field, so the unit is UTF-16 code units and a field of exactly
10,000 is delivered whole (`plans/sdd/V6-closeout/rehydrate-cap/evidence/host-cap-unit.txt`).
`hookio.HostChars` and rehydrate's `hostChars` count exactly that, and a test ties the two. The count
is never smaller than the rune count, so no reading of "characters" makes a passing payload long;
the conservatism is the 500-character headroom, not a second guess at the unit.

**Two dimensions, one admission rule.** Every unit is priced in tokens (as before) and in host
characters, and is admitted only when it fits both — tier 1 against what the payload has left,
every discretionary item against its share in each dimension with carry-forward in each. The
character price is EXACT, not an estimate: units, each section's heading line and the blank-line
separator before it, and the wrapper are priced on the very strings `renderBody` and `Wrap`
concatenate, so the sum Build plans with is the length it renders. That is what makes the ceiling a
guarantee. The §18 hard-cap loop is extended to the character dimension as a defence;
`PropBuild_NeverExceedsTheHostCeiling` asserts it never fires on characters.

**Whole records, in §8.6 order, with current authority first.** Nothing about §1, §2, §7, §8 or §19
moves. Within that, four things were refined because a ceiling this much tighter than the token
band exposed them:

1. *Tier 1 is admitted record by record, not item by item* (refines §4). One pinned invariant too
   large for the ceiling no longer takes the forty that fit down with it: tier-1 records are admitted
   whole in builder order until the first one that does not fit (the §7 prefix rule), and each one
   left out is its own explicit overflow — `ID "tier1"`, the item's own kind, a detail that names the
   record, says it "is emitted whole or not at all", and ends in the pointer that restores it.
2. *The retrieval line is admitted first, and item 7's floor is held from the first admission on*
   (extends §6 and §18). Every overflow pointer depends on the model knowing the tools exist, and on
   there being room to say what is missing. So tier 1 admits item 8 before the invariants and the
   original prompt, and item 7's smallest form — its heading and the counted tail — is reserved
   throughout. Render order is untouched.
3. *Item 7 truncates instead of being evicted* (corrects §6's reserve in practice). Its lines carried
   no DropEntry, which made them fixed units, and a fixed unit is admitted wholesale: the report was
   never cut to its counted tail, and a payload too small to hold it had the whole section evicted by
   the §18 loop — the pre-D5 `degraded` golden carried no section 7 at all. Item 7 is now filled as a
   prefix of lines plus `- … and N more; call dropped()`, against its reserve (a tenth of each
   dimension, never below the floor) plus whatever the shares carried forward. The complete list is
   still persisted for `dropped()` either way.
4. *An explicit overflow sorts first in item 7.* It carries its item's own kind ("invariants",
   "user_intent"), which is not in `kindRank`, so it used to sort after every known kind — the first
   line a bounded report's tail would swallow. It now sorts with `dropKindOverflow`.

Two smaller consequences of the same shift: item 6b is offered the share carry before item 7, since
it outranks it (a tenth of the ceiling alone would cut a skill index the payload has room for); and a
payload whose only admitted section would be item 7 is no payload, as a payload with no items
already was — the floor exists so that an omission can be named next to what did fit. (§23.1
replaces that last clause when material was dropped: the payload is then the loss notice.)

**Pointers.** Every record the budget or the ceiling leaves out is named with the call that brings
it back: `why(<decision id>)` for a decision, `re_read(<path>)` for a file pointer,
`expand(tool_use_id=…)` for a tool pointer and for the verbatim original prompt (its L0 record,
`prompt_<session>_0`), `already_tried(target="…", approach="…")` with the record's own target and
approach, Go-quoted, for a shown elimination, `Read <path>` for a path rule, a nested `CLAUDE.md` or a
skill-index entry (its `SKILL.md`, whether the payload or the indexer's own `skillIndexTokens`
dropped it), and `Read .qompack/checkpoints/NNNN.json (<field>)` for material whose only durable
home is the checkpoint — invariants, evolution deltas, current work. The checkpoint path is given
relative to the project root so a drop line stays cheaper than the record it replaces, which
`PropBuild_MonotoneInBudget` holds the payload to. Pointers go in `DropEntry.Detail`, so `dropped()`
— what the counted tail points at — answers with them too.

Three kinds of drop entry are not a record with a restoring call, and are left as they were before
D5: eliminations past `runtime.rehydrate.eliminationsTopN` are one counted line (`N of M not shown`)
whose remedy is the standing `already_tried` query on the approach about to be taken (§8.7); the
entries in `Checkpoint.Dropped`, which the checkpointer recorded itself (an `open_question`, a
`narrative`, a superseded tool output), are carried with its detail; and a unit the no-contents
guard rejected, a scan or source failure, and the §2.7 skill-body warnings are reports about
withheld, unavailable or host-side material, not omissions a call could undo.

*Review correction (2026-09-23).* As first built, the skill-index drops said only "did not fit the
rehydration budget" and a shown elimination's drop said "call already_tried(target, approach)",
which named no target or approach the model could pass: the paragraph above claimed more than the
code did. Both now carry the pointers listed (`TestSkillIndex_BudgetDropNamesTheSkillFile`,
`TestSkillIndex_DropsReportUnindexedSkills`, `TestEliminations_BudgetDropNamesItsOwnQuery`), and the
`no-checkpoint` and `state.json` goldens were re-recorded through `-update` for that change alone.

**Min-fill.** It re-admits toward `minTokens` inside both bounds and never spends item 7's reserve.
Under the baseline estimator the ceiling now binds first, so an unset build fills to the ceiling
rather than to `minTokens`; `TestBuild_MinFillReadmitsUnits` asserts both regimes.

**What this supersedes, and what it does not.** §4's "each item is admitted whole while it fits" is
now per record. §6's drop-report reserve is now "B/10 and C/10, never below the floor". §17's and
§18's golden figures describe the pre-D5 payloads and are left as written: `full-12k` and `full-8k`
are now byte-identical (the ceiling binds at both ends of the old band), the new `token-bound`
golden carries the token-budget cut the old pair used to show, and `degraded` now ends in a
truncated section 7 instead of having it evicted. The five payload goldens and `state.json` were
re-recorded through their generator (`go test ./internal/rehydrate -run
'TestBuild_Golden_PayloadsMatchFrozenBytes|TestState_MatchesFrozenGolden' -update`) after reading
each diff.

**Evidence.** `internal/rehydrate` `TestBuild_HostCeiling_LargeSessionArrivesInline` (the frozen
`full` fixture: 32,049 characters before, inside 9,500 now), `PropBuild_NeverExceedsTheHostCeiling`
and `FuzzBuild_HostCeiling` (multibyte and astral text, one enormous record, thousands of small
ones, pathological session ids and sequences); `test/e2e` `TestE2E_SessionStartCompactFitsTheHostCap`
(the real binary, hook client and daemon: 20,033 characters and a Loud over-cap line before, inside
the ceiling with no Loud line now); the real-host sessions under
`plans/sdd/V6-closeout/rehydrate-cap/evidence/`.

## 22. Amendment (2026-09-30, owner decisions D49 and D50): current authority first, and no silent fallback

**What changed.** The live re-run on candidate 4 (`plans/sdd/V6-closeout/live/rerun-c4/`, audited
in `plans/sdd/V6-closeout/live/report-c4.md`) showed six ways the payload misled the model while
staying inside §21's ceiling. D49 and D50 (`plans/V6-CLOSEOUT-CHECKLIST.md`) say each one is fixed
before 0.3.0. This section records how. It supersedes §18's eviction order and golden description,
and §19.

1. *Tier 1 is one prefix over `tier1Admission`* (refines §21.1-2). The admission order is the
   retrieval line (item 8), then the pinned invariants, then item 2's tier-1 slice. The first
   record the budget cannot hold ends tier 1: every tier-1 record after it, in any item, is named
   rather than admitted, even when it is small enough to fit. §21.1's prefix was applied per item,
   so at UAT-05's 150-token budget the retrieval line was refused and the smaller pinned invariant
   after it got in; at 160 the reverse happened (F-C4-UAT05-3). One record does not end tier 1: a
   record no payload could hold, because it and its heading exceed the ceiling less the wrapper and
   item 7's floor. It is named and passed over, exactly as an L0 capture past `intentReadLimit` is.
   *How far the closure reaches.* It stays inside tier 1, with two exceptions. First, when the
   refused record is the retrieval line, no share, no skill index and no min-fill is offered any
   room: every share-filled section is records plus the call that restores or checks them, and
   those calls mean nothing to a model never told the tools exist. Second, while tier 1 is
   incomplete, item 2 is re-admitted nowhere later (not its older deltas, not by min-fill, not by
   step 9a below), because its pending units start with the tier-1 records the prefix refused.
   Otherwise the shares fill as §6 says. An earlier draft closed the shares on any tier-1 refusal,
   and a long newest restatement that could not follow a long original then emptied sections 3-6
   of a payload with thousands of characters unused.
2. *Item 2's tier-1 slice is the verbatim original and the newest restatement* (supersedes §19;
   D49, F-C4-UAT06-1 and F-C4-UAT06-3). Evolution holds every later prompt, so a correction admitted
   only from item 2's tenth was pushed out by a few ordinary prompts: UAT-06 named the only
   correction in force as "did not fit" while the block used 2,652 of 9,400 characters. The newest
   restatement is the current authority and is admitted with the original, whole or named. Older
   deltas keep the share, and they are never shown without the newest above them: a list of
   superseded statements under "most recent first" would present one of them as current.
3. *Unused room goes to evolution, newest first* (step 9a, D49). After every share and the skill
   index, Build measures what item 7 will take and gives the room beyond that to item 2's refused
   older deltas, whole and in the same newest-first prefix order. Measuring item 7 first, rather
   than holding back only its reserve, keeps the payload growing with the budget.
4. *Hard-cap eviction runs the fill backwards* (supersedes §18's order). The loop first cuts item 7
   to its floor. It then removes sections in the reverse of the order Build admitted them
   (`admissionRank`): the skill index and the share-taking items, last rendered first; then item 2,
   taken apart one record at a time (older deltas oldest first, then the newest restatement, then
   the section with its original); then the invariants; then the retrieval line. Item 7, whose floor
   is held from the first admission on, goes last. §18's "last non-tier-1 item, falling back to the
   last item" evicted tier 1 in render order, which removed the retrieval line before the invariants.
5. *Section 2 renders the correction above what it supersedes* (D50). The evolution, newest first,
   comes before the verbatim original. The original stays whole and is labelled `Original:`. D5's
   whole-record rule and item 2's admission rule are unchanged.
6. *A checkpoint fallback is never silent* (D49, F-C4-UAT03-1). When the newest checkpoint does not
   verify and the payload is rebuilt from an older one (`Request.Ref.Refused`), the header says
   "rolled back from", section 7's first line is a `checkpoint_fallback` entry naming the refused
   and used checkpoints, what may be missing and how to restore it, `Result.Degraded` is true with
   `DegradedReason`, and the daemon logs "rolled back to" Loud.
7. *Pointers never show a withheld path* (D50, C4.6, UAT-12 F4). A section-6 pointer, and its drop
   entry, never show a path the host's current Read rules deny or ask about (`Deps.HostPaths`, the
   rules `re_read` and `expand` apply), or a path outside the project. That includes a path spelled
   from the home directory or an environment variable (`~/…`, `$HOME/…`, `%USERPROFILE%\…`). Such
   pointers point by hash only. When the host's rules cannot be established, every path is withheld.
8. *A named tier-1 overflow is Loud once per session* (D50, UAT-04). The payload names it on every
   compaction. The Loud line "tier-1 material exceeds the hard budget cap" is logged once per
   session (`Request.Tier1OverflowReported`) and at Info after that. §23.3 states the scope: once
   per session per daemon.

**Evidence.** `internal/rehydrate`: `TestBuild_Tier1FollowsTheAdmissionOrderAtTinyBudgets`,
`TestBuild_TinyBudgetNeverKeepsTheInvariantWithoutTheRetrievalLine`,
`TestBuild_AnUnrepresentableTier1RecordDoesNotCloseTier1`,
`TestBuild_ARefusedNewestRestatementLeavesTheSharesTheirRoom`,
`TestBuild_NewestRestatementIsAdmittedAheadOfTheShare`, `TestBuild_OlderDeltasNeverShowWithoutTheNewest`,
`TestBuild_UnusedRoomGoesToEvolutionNewestFirst`, `TestBuild_Section2RendersTheCorrectionAboveTheOriginal`,
`TestBuild_CheckpointFallbackIsNamed`, `TestBuild_PointersNeverShowAWithheldPath`,
`TestBuild_PointersNeverShowAHomeOrVariablePath`, `TestBuild_ReportedTier1OverflowIsNotLoudAgain`;
`internal/daemon`: `TestService_CheckpointFallbackIsNeverSilent`,
`TestService_Tier1OverflowIsLoudOncePerSession`. The `degraded`, `full-12k`, `full-8k` and
`token-bound` goldens and `state.json` were re-recorded after reading each diff: they gain the newest
restatement, the unused-room deltas and the evolution-first order.

## 23. Amendment (2026-10-02 to 2026-10-04, coordinator decisions D59, D60, D61 and D63, owner decision D50): a loss is never silent, and a free-text summary is shown only when a whitelist proves it safe

**What changed.** The candidate 7 live lane (`plans/sdd/V6-closeout/live/rerun-c7/`) found one
silence and one leak in what §21 and §22 record, plus a log line whose scope the docs overstated.
Items 1 to 4 record the first round of fixes. The w19 verifier then found the first round's summary
gate too slow to answer a compaction in a project with any Read deny or ask rule, still leaking
paths that hold a delimiter, and blind to paths the checkpoint records only as drops (D60(c)). A
second round answered those by reading more shapes of more text, and its own verifier found more
leaks and new over-withholding. Coordinator decision D61 then replaced the search for paths inside
free text with a bounded screen. Items 5 to 10 record the gate as D61 rules it; the second round's
reading and its record are superseded, except where an item says a part of it carries. The review
of D61's first implementation (w19c) found spellings the screen did not read (a path glued to a
short option, PowerShell's `$env:` variables, a path-named argument holding more than one path, a
line continuation inside a word, a cut command that starts like JSON) and summaries it withheld that
name no withheld path (a rule anchored outside the project whose literal merely occurs in the root's
path, a withheld name matched inside another word, a cut path argument read as a whole path, a
comment marker read as a UNC share, `2>/dev/null`, a Docker bind mount of the project). Its second
review (w19c round 2) found an instruction file under a denied directory restored with its body and
named in section 7, a sibling of the root behind a quote glued to the root's spelling, an apostrophe
in the root's path read as an open quote, a Grep regular expression read as an absolute path and
noted as a withheld name that withheld unrelated summaries, a relative path glued to a flag, a
rule's literal matched inside another word, and the host handed a command run from the root whole
and each value of a path-named array. Its third review (w19c round 3) found PowerShell's `$Env:`
drive in any case but lower, bash's ANSI-C quoting, a cmd.exe or PowerShell backslash before a
quote and an undecoded character code before a rule's literal (which round 2's "where a name
starts" had lost), an outside path after a typographic quote or a Unicode space, a cut inside an
encoded name, a quote then an escaped space after the root, an earlier spelling of the root losing
its quotes, cmd.exe's caret-escaped separators, a drive-less path with a glob in it read as a
regular expression, a glob among several path-named values, a skill file the host refuses
indexed and named, a rule over the project through an alias of its root, an operation's path in a
drop reason, and usefulness losses (a rooted word that is no path noted as withheld, a nested
shell that changes to the root).

The w19c round-3 verify still found two majors and a minor: a path after any non-ASCII byte was read
as an outside POSIX path, so an in-project `café/sub/x.txt` or `文档/设计/说明.md` preview was withheld and
poisoned the build; `$'/home/u/x'` showed an out-of-project path; and an escaped-operator search
(`\?\?`) was withheld. Each was another turn of the same screw: D61's screen tried to undo every
shell's quoting — bash `$'…'` and `$"…"`, PowerShell `$Env:` and backticks, cmd.exe carets, Unicode
quotes and spaces, nested shells, regex-vs-path guesses — and no reading was complete. Coordinator
decision D63 stops modelling shells. A free-text summary is now SHOWN only when a WHITELIST proves
every token safe; anything the whitelist cannot vouch for is withheld. Items 5 to 10 record the gate
as D63 rules it. D61's shell-quoting state machines, `stripQuotes`, `protect`'s quote-run logic,
`regexLike`, `outsideIn`'s heuristics, `homeOrVarPath` and the Unicode-boundary guesses are deleted;
what D61 recorded is superseded, except where an item says a part of it carries (the rule-literal
derivation of item 7(a), the `ReadRulePatterns` accessor and `rootCover` of item 7(d), the
structured-value shape of item 6, the root-held-together idea of item 8, the checkpoint-drop gating of
item 4 and the reason redaction of item 9).

The review of D63's first implementation (w19d round 1) found that its path check read a backslash
only as a separator (`.\.` is `..` to bash), that a backslash ending a token (an escaped space, or a
line continuation the store collapsed) joined two whitelisted words into a denied name or a sibling
of the root, that a path could start unseen after `@` or after a short option's first letter and a
`..` after `=` or `,`, that a URL token was safe whatever it held, that the root's mark inside a
double-quoted run hid a sibling on Linux and macOS and swallowed the `//` of `file://`, and that a cut
path-named value among several asked the host; and that a cut inside a quoted run, a structured
value whose name merely begins with a rule's literal (`.env.example`) and a one-word URL with a query
string were withheld though they name nothing private. Its fix also found a cut path-named value
that ends in the start of a denied path (`{"file_path":"private/den…`) shown. Each leak is closed by
making the whitelist stricter, or by reading what it already admits both ways; the over-withholding
is closed by judging whole names where a value is one path, and by two extensions to D63(2)'s list,
the null device's redirects and a backslash inside a double-quoted run, each inside the completeness
argument (item 7).

The second review of D63 (w19d round 2) found four more leaks and the gate's largest usefulness
losses. A structured glob could respell `..` with a class or a `?` (`[.][.]/outside/x.key`), because
containment cleaned it as a literal path; a `file:` URL in a path-named JSON value was read as a
project path; an outside file's basename withheld every Read of the project's own file of the same
name (a dependency's README.md withheld `<root>/README.md`, with no rules at all); and the drop-reason
screen no longer held the root together, so in a root with a space every reason naming a project path
was redacted. Its fix found five more: path.Match lets a class match `/`, so `[/]etc[/]passwd` read as
a project path; a structured value's whole-name screen missed a literal that starts a glob segment
(`secrets.txt` under `Read(./secret*)` in a path-named array, and `<root>/se\crets.txt`); a withheld
project path named absolutely in a drop reason was not found, nor a sibling of the root that is a
whole part of an error chain; a recall `path:` selector in single quotes was not read; and three
characters D63(2) lists as safe expand in zsh or PowerShell (`=` starting a word, `#` after a
character under zsh's EXTENDED_GLOB, a whole `@name` splat). Each leak is closed by making the gate
stricter. The usefulness losses are recovered by extensions to D63(2)'s list, each with its own
completeness argument in item 7: a Glob preview of a directory under the root and a brace glob judged
as one structured glob, `;`, `|`, `&&` and `||` glued to a word splitting it, parentheses inside a
quoted run, an apostrophe between two letters, a single-quoted run, and a single `%` that no escape
or variable can use. A rooted structured value spelled in one separator style is screened by the
rules' literals alone (item 6).

The final verify of wave 19d found two majors and two minors in that round's whitelist. The root's
spelling accepted a backslash as a separator on every platform, so `/home\u/proj/x` was held together
as the root and never read in its POSIX reading, `/homeu/proj/x`, a sibling of an ancestor of the
root; a URL token was checked for a path only after an `&`, while PowerShell splits a bare argument
at `,` into an array (`https://x,/etc/passwd`); only a single letter before `:` was a drive, while
PowerShell's provider drives (`Temp:`, `Env:`, `HKCU:`, `HKLM:`, `Cert:`, `Function:`, `Alias:`,
`Variable:`, `WSMan:`) and any drive `New-PSDrive` defines make `Temp:name` a path outside the project;
and no path started after the `#` that starts a token, so `#/home/u/.ssh/id_rsa` was shown while
`# /home/u/.ssh/id_rsa` was withheld. Each is closed by making the whitelist stricter (items 7 and 8).
The verify then asked for every extension of round 2 to carry its completeness argument, pinned by an
adversarial row, and for any whose argument could not be completed to be removed. Writing them out
found three that could not be completed as they stood, each narrowed rather than removed, since the
narrower extension's argument is complete: an apostrophe between letters opens a quoted span that
runs to the next apostrophe, so after one the screen's tokens are no longer the shell's words and the
root's unit, the one unit judged by what follows it, could name a sibling inside the span (no root may
now follow such an apostrophe); a quoted run's root before a shell operator is a sibling's name to a
program that takes the run as one argument when the root stands where that argument's path starts
(the operator now ends the root only when the root is a word of its own after a word that ends in no
delimiter); and a single `%` before `u` and four hex digits is IIS's and JavaScript's escape, and one
before a digit, `*` or `~` a cmd.exe batch parameter (each is now unsafe). The other extensions'
arguments hold as written (item 7). The round-2 reviewers' open notes are closed too: the claim that
a JSON preview's keys are never judged is corrected (they are screened as every string is), the cost
row's term for items 6a and 6b now fails when 6b's judgements are dropped and 6a's doubled, and
comments that described D61's regular-expression reading as current are rewritten.

1. *A degraded compaction that dropped material is never silent* (D59, UAT-05 F-C7-UAT05-1). At
   UAT-05's `runtime.rehydrate.minTokens` = `maxTokens` = 150 the retrieval line (86 tokens) does
   not fit beside the 47-token wrapper and item 7's floor, so §22.1 ends tier 1 at its first record,
   the shares are closed, and §21's "a payload whose only admitted section would be item 7 is no
   payload" left nothing: the compact injection was only the contract probe, while the state file
   named 15 drops, the pin and the original request among them. The session was told nothing. Since
   D59, when no section is admitted and material was dropped, the payload is a **loss notice**:
   section 7 alone, rendered and priced like any section (the token budget on the assembled
   payload, the host ceiling exactly), saying how many items did not fit, that `dropped()` lists
   each with the call that restores it (the user's `/qompack:dropped` shows the same list), and the
   verbatim original's restore call when the original is a tier-1 overflow. When that does not fit
   it falls to `- N items dropped; call dropped()` with the original's line, then to that line
   alone. A budget that cannot hold even the smallest form injects nothing, and the drop report
   then says so with the wrapper-alone case's shape: kind `overflow`, id `payload`, a detail that
   begins `OVERFLOW:` and says nothing was injected, and a Loud line.
   *Why a notice and not the report.* The report's lines are calls into the retrieval tools whose
   line the budget refused, and §22.1 keeps records without that line out for the same reason. The
   count, `dropped()` and the original request's restore call are what a model needs to ask for the
   rest, and they are the cheapest true statement of the loss. A compaction answered with nothing is
   what owner decision D11 already rules out for a build that failed, and D49 for a checkpoint
   fallback; a budget that is merely small is not a reason to be quieter than a failure.
   *What does not change.* The token budget stays a hard cap (§3): the notice is never emitted over
   it, and the inherited suite still holds `Result.Tokens` within `Request.Budget` at a budget of 1,
   where even the wrapper does not fit. D59's fallback clause ("emit the smallest form … and record
   the overrun as the existing overflow rules do") is read with that cap, and D60(ii) rules that
   reading: the smallest form is emitted when it fits, and when it does not the overrun is recorded
   the way the existing overflow rules record the wrapper's, as the `overflow`/`payload` drop entry
   and one Loud line, and nothing is injected. A budget below the smallest form (69 tokens on
   UAT-05's fixture with the shipped estimator) therefore still injects nothing in the session, and
   the drop report and the Loud line keep that from being silent. Emitting over the cap would make
   §3's guarantee, the suite's bound and UAT-05's "a block larger than the budget" fail each carry an
   exception, for budgets no default reaches. "No payload" still holds when nothing was dropped (a
   build that admitted nothing has always named the refused retrieval line, so that case is the
   notice's own rule). Tier-1 order, the closure, the shares, unused room and the hard-cap eviction
   are as §22 records them. `TestBuild_Tier1ThatCannotFitIsDroppedWhole` asserted the empty payload
   at 60 tokens; it now asserts the notice and no tier-1 record (criterion change, D59).
2. *Argument summaries are judged as arguments* (D50, C4.6; UAT-12 F1 on candidate 7). With the
   deny rule `Read(./private/deny.txt)` in force, section 6 showed
   `{"query":"path:private/deny.txt"}`, the arguments of a `recall` call whose `path:` selector named
   the denied file: §22.7's gate split the summary into tokens and judged `path:private/deny.txt`
   as a path, which no rule refuses. A tool pointer's summary is the call's arguments as the store
   previews them (`store.argsPreview`): for a built-in tool, the values of `file_path`, `path`,
   `pattern`, `command` and `url` joined by spaces, so no name marks a path and no token boundary
   marks where it ends; otherwise the call's canonical JSON; either cut at 120 bytes with `…`.
   Items 5 to 9 record how the gate reads it.

   *What D50 covers* (D60(i)). D50 covers pointers: file and tool pointers, their summaries, and
   section 7's drop entries, except the entries whose text is the model's own: an elimination's
   `already_tried(target, approach)` call, and an open question the checkpointer cut at its budget,
   whose reason is the question. Sections 3 and 4 are outside it too, deliberately: they render
   `record_eliminated`'s target, approach and reason, and the decisions minted from them, which are
   the model's own earlier text, the same text `already_tried` and `why` return ungated, and
   withholding them would break the `already_tried(target, approach)` call that restores them.
   Section 6 is the only section that prints argument summaries.

   *Deliberate limits* (D60(iv), as D63 leaves them; item 7 records the whitelist's own). Each is
   recorded so that no one reads the gate as stronger than it is.
   - Aliases are not resolved in free text. A command that names a denied file by its 8.3 short
     name (`PRIVAT~1\deny.txt`), through a link, or by a Unicode normalization or compatibility
     variant of its name (an NFD spelling of an NFC literal, which APFS opens as the same file; a
     fullwidth spelling, which a program that uses a Windows code page's best-fit mapping opens as
     the ASCII name; the screen folds case only) does not spell the rule's literal and is shown; a
     rule written through an 8.3 name screens by that name (a rule that covers the whole project
     through any alias the host resolves withholds every free text, item 7(d)). File pointers and
     structured summaries are judged by the host's rules, which resolve aliases and links. A summary
     that starts at the root and goes on below it with a space is judged through its path part only,
     from the root through its last word that holds a separator (item 6), so a link at a name with a
     space in its last segment (`<root>/docs/my notes.txt`, judged as `<root>/docs/my`) is not
     resolved; a rule on that name screens by its literal. Several path-named values of a JSON
     preview, and a relative Glob or Grep preview of several words, are screened without the host,
     so a link or an 8.3 name in them is not resolved either.
   - Globs, brace expansions, variables and names assembled at run time are never shown in free
     text, so none needs resolving: a token holding `*`, `?`, `[`, `{`, `$`, a `%` that could be an
     escape or a variable, a quote other than item 7(a)'s, a backtick or a caret is withheld rather
     than decoded (`cat private/den*`, `cat $F`, `type %D%deny.txt`, `cat $'de\x6ey.txt'`, `curl
     …%252Fdeny.txt`, a concatenation, a command substitution). The JSON string values of a
     canonical-JSON preview are decoded (the store wrote them as JSON), and each is then judged as
     free text. A structured glob (item 6) is judged by containment, by what it selects among the
     recorded paths and by the literals it spells, with each class read as what it nearly spells
     (`de[n]y.txt` is deny.txt); one that selects a refused file Qompack never recorded without
     spelling its literal (`private/d*`) is the next bullet's limit.
   - A name relative to a directory a command changed to is not resolved when the name itself holds
     no rule's literal, whether the `cd` is earlier in the same command line or in an earlier call
     (the shell's working directory persists between calls): after `cd secrets` a later `cat
     token.txt` is shown under `Read(./secrets/**)` unless a file pointer recorded the file (item
     7(c)). A `cd` to an absolute directory is withheld for its leading separator (`cd /q && cat
     other/x.txt`, item 7(b)), and `cd ..` and cmd.exe's glued `cd..` for their `..`; a `cd` that
     spells no directory (`cd` alone, to the home directory; `cd -`, to the previous one; `popd`) is
     this limit too (`cd && cat .ssh/config` is shown).
   - A PowerShell drive or provider a user defines under one of the inert prefixes' names (`path`,
     `sha256`, or `http` and `https` before a URL's `//`) is not resolved: `path:src/x` is read as
     recall's selector, `sha256:…` as a hash and `https://…` as a URL (item 7(b)). Every other name
     before a `:` is withheld whether or not a drive of that name exists.
   - On Windows a spelling of the root in backslashes alone is the root (item 8), as cmd.exe and
     PowerShell read it; Git Bash, reading it unquoted, drops each backslash and reads a drive-relative
     name built from the root's own segments (`C:qproj…`), which lies in the working directory. The
     final verify of wave 19d ruled that Windows keeps either slash; a spelling that mixes the two is
     not the root.
   - Free text that mentions a rule's literal or a withheld path's name where a name starts is
     withheld, whatever follows the name (`kubectl get secrets` under `Read(./secrets/**)`; `cat
     .env.local` under `Read(./.env)`; `git diff README.md` beside a withheld module-cache README.md;
     `not my secret.txt.bak at all` beside a withheld `private/my secret.txt`), and so is free text
     that holds a literal taken from inside a glob run anywhere (`process.env.NODE_ENV` under
     `Read(**/*.env)`); a literal of a character or two withholds most free text. D61 accepted that
     over-withholding and D63 keeps it for free text. A structured value is one path, so there a
     whole literal counts only as a whole name (item 7(c)): the project's own `.env.example` is shown
     under `Read(./.env)`; a literal that starts a glob segment (`secret` for `Read(./secret*)`) counts
     whatever follows it there too. A rooted structured value spelled in one separator style is
     screened by the rules' literals alone (item 6), so a Read of the project's own README.md is
     shown beside a withheld outside README.md, while `git diff README.md` stays withheld.
   - A glob that selects only files Qompack never recorded is judged as written: Build reads no
     files, so it has no listing to match against (D60(iv)). A structured glob, and recall's `path:`
     selector, are withheld when they select a path the build records as withheld (items 6 and 7).
   - A sibling of the project root whose name is the root's own last segment, a space and more
     (`C:\Users\me\proj - Copy\notes.txt`, the name Windows gives a copied folder), written unquoted
     in free text after another word, reads as the root followed by a word and is shown, as a shell
     reads it (`cat <root> old/x.txt` hands `cat` the root and `old/x.txt`). So does the root followed
     by a `;` that ends its token (`Set-Location <root>; go test`): POSIX shells and PowerShell end
     the statement there, while cmd.exe would hand an external program `<root>;`, the root's own
     spelling and a `;`, which reveals no other name. As a Read's preview it
     is judged by the host through its path part, which lies outside the project; quoted (`"<root>
     old/x.txt"`, item 8), escaped (`<root>\ old`, a backslash that ends a token, item 7(a)), as a
     path-named JSON argument, or as an operation's path in a drop reason (item 9), it is withheld.
   - A drop entry's reason that names a path a rule refuses but the build never recorded (a rule or
     skill scan error naming an in-project file) is screened for paths outside the project and for
     the whole spellings of the paths the build withholds, not for the rules' literals or a bare
     basename (item 9).
   The store's preview collapses runs of whitespace (D60(iv)); the screen collapses a rule's literal
   the same way, so a denied name holding two spaces is found in a command that spells it.
3. *Loud once per session per daemon* (F-C7-UAT05-2). §22.8's marker is the daemon's memory
   (`rehydrateService.tier1Loud`). In UAT-05's step 5 the line was logged twice in one session, once
   by the daemon that answered the first tiny-budget compaction and once by the daemon started after
   it was ended. No persisted marker exists: the state file is rewritten on every compaction and
   reset by a clear, so it records the last rehydration, not whether this session was ever Loud, and
   persisting one would add a durable write to the compaction's answer path for a log line. The rule
   is therefore once per session per daemon, which is what the code does: a restarted daemon (idle
   exit, crash, an operator ending it) reports the condition it finds once more, and every payload
   names the loss either way (item 1 above).
4. *The checkpointer's drop entries pass the same gate* (D50, C4.6). Five checkpoint drop kinds are
   keyed by a file pointer's path as recorded: `file_pointer` (a pointer cut at the checkpoint's
   own budget) and `pointer_missing`, `pointer_invalid`, `pointer_untracked` and `pointer_dirty`
   (the ground-truth checks finalize always runs). Section 7 rendered them verbatim, so a path
   section 6 withholds reached the payload one section later, most likely as
   `pointer_untracked .env — not tracked by git` for a gitignored, host-denied file, whose pointer
   finalize keeps. Each withheld one is now named the way section 6 names the pointer: by its hash
   while the checkpoint still holds it, with `restore: expand(hash=…)`, and as `(path withheld)`
   otherwise. It keeps the checkpointer's reason, drops the `re_read(path)` call that would be
   refused, and says the path is withheld. `Result.Dropped`, and so the state file and `dropped()`,
   carry the same entry: `dropped()` already withheld a host-denied captured path, and now also
   withholds one outside the project, as `re_read` does. D60(iii) accepts that shape: `dropped()`
   returns such an entry with its path redacted, by hash or as `(path withheld)`, rather than
   leaving it out, so the loss stays counted and restorable by hash. Every other checkpoint kind is
   keyed by an id, a record or nothing, which `TestCheckpointPathDrops_AreTheCheckpointersOwn` pins
   against the real `ValidatePointers` and `Truncate`. One judge, with one snapshot of the host's
   rules and one memo, serves a whole build. Every entry's reason is screened as item 9 records.
5. *A whitelist in place of a path finder* (D63, C4.6, D50). Round 1 judged every word and every run
   of words against the host's rules; round 2 read each summary from the store's shapes, each word as a
   POSIX shell and PowerShell read it, through a `hostperm.Evaluator`; D61 replaced that with a bounded
   screen that still tried to undo shell quoting. Three reviews each closed some spellings and opened
   others, because each tried to UNDO what a shell does to a name — and no undo is complete. D63 inverts
   the test. A tool pointer's summary is judged in two halves:

   - A STRUCTURED value — the store's preview of one path argument — is judged WHOLE, as a file
     pointer's path is: containment and the host's rules, one judgement per path, once per build
     (item 6).
   - Everything else is FREE TEXT, and costs no host judgement. It is SHOWN only when a whitelist
     proves it safe (item 7); otherwise it is withheld. The whitelist admits a known-safe vocabulary
     and rejects everything else, so a spelling the screen has never seen is withheld by default, not
     shown by default.

   This is complete by construction for privacy: no absolute path, no escaping path, no Read rule's
   literal and no withheld name reaches the model through a shown free-text summary, whatever shell
   produced it. It is deliberately NOT complete for usefulness — it over-withholds any command that
   escapes a space, globs or uses a variable, and quoted text holding a glob, `;`, `|` or a second
   `%` (item 7's limits). The owner
   accepted that trade: a withheld summary still points by id and hash, and `expand(tool_use_id=…)`
   restores the call.
6. *A structured value is judged whole, as a file pointer is* (D63(1), carrying D61's shape). A tool
   pointer does not carry its tool's name, so the shape decides. A structured value is: a one-word
   summary (a single token once the project root's own spelling is held together, item 8) that is a
   Read's, Write's or Edit's `file_path`, a lone Glob or Grep argument or an LS path; the one
   path-named value of a canonical-JSON preview (`path`, `file_path`, `notebook_path`, `paths`,
   `file`, `dir`, `cwd` and their kin, or the one element of such an array); a Glob preview of a
   directory under the root (the store's `path pattern`: the root, alone or followed by a safe
   relative directory, then a pattern); or the path part of a summary that starts at the root and
   goes on below it with a space (the stretch from the root through its last word that holds a
   separator, so a Read of `<root>/my docs/x.txt` and a command run from the root reach the host, item
   8, once the summary's free text has passed item 7). It is withheld when the build withholds it as a
   file pointer's path (containment, and the host's rules once per build), or when it is a glob that
   selects a path the build withholds (`rules.Match`; a glob without a separator matches at any
   depth).
   - *Containment reads a glob as a glob.* A segment that path.Match matches against `..` and that
     holds a class, a `?` or an escape (`[.][.]`, `?.`, `.?`, `\.\.`), or that starts with an
     explicit `.` (`.*`, `.[.]`, which a shell without globskipdots matches against `..`), is a parent
     directory, in either backslash reading; and each class is also read as what it nearly spells, a
     one-character class as that character and a class that may match a separator (path.Match lets
     `[/]` and `[^a]` match `/`) as `/`, so `[/]etc[/]passwd` is `/etc/passwd` and `[~]/x` a home path.
     A segment of stars and literals that does not start with a dot (`*/x`, `*.*`) is not counted: no
     directory walker yields `..`, and a shell's star never matches a leading dot. A `file:` URL is
     outside the project wherever it stands, a path-named value included.
   - *A one-word summary must ALSO pass the free-text whitelist* (item 7), so a one-word
     `$HOME/.ssh/id_rsa` can never be shown. A pattern one-word (a glob or a brace list, a lone Glob or
     recall argument) is instead required to be a safe pattern: the whitelist's characters plus the
     glob metacharacters `* ? [ ]` (so a regular expression such as `^\s*func\b` is not shown as a
     glob), with one level of `{a,b}` expanded into alternatives that must each be such a glob (a
     concrete path among them would reach no host judgement; a sequence `{1..3}` or a nested brace is
     withheld), the braces also read as path starts (PowerShell opens a script block at `{`). This
     relaxes D63's "a one-word summary must ALSO pass the free-text whitelist" for patterns; the
     owner is asked to ratify it.
   - *A Glob preview of a directory under the root* is judged as one structured glob: the directory
     by the host once, the pattern as a pattern one-word is, read from the directory and alone (a
     script's argument, read from the working directory), and the text by the rule literals and the
     withheld names. A relative directory (`src **/*.ts`) cannot be told from a command (`ls *.go`), so
     it stays free text.
   - *Names in a structured value.* A one-word value is one path, so the name screen reads whole
     names in it (item 7(c)): the project's own `<root>/.env.example`, `.gitignore`,
     `.github/workflows/ci.yml` and `secrets_test.go` are shown under `Read(./.env)`, `Read(./.git/**)`
     and `Read(./secrets/**)`. A rooted value spelled in one separator style (only `/`, or on Windows
     only `\`) is the exact path the host judged, read the same way by the host, cmd.exe, PowerShell
     and a POSIX shell, so it is screened by the rules' literals alone and not by the build's withheld
     names: a Read of a dependency's `README.md`, a global `settings.json` or `~/.kube/config` no
     longer withholds the project's own `<root>/README.md`, `<root>/.claude/settings.json` or
     `<root>/config/database.yml` (they are other files, which the host has judged). A mixed spelling
     (`<root>/private/de\ny.txt`, deny.txt to a POSIX shell), a relative value (read from a working
     directory the host does not know) and a pattern keep the withheld names. `<root>/README.md` is
     still withheld beside a denied `private/README.md`, whose rule's literal is the whole name
     `README.md` (accepted over-withholding; the owner is asked whether the host's judgement of the
     exact path should end it).
   - *Several path-named values, and cut ones.* A path-named JSON value is exempt from the whitelist
     (it is a structured identifier, not a command), and its names are whole names too; a preview with
     SEVERAL path-named values costs no host judgement: each is screened by containment, by the rule
     literals and withheld names item 7(c) lists, and — if a glob — by what it selects, never by the
     host. A value the store's cut fell inside is judged by the directory it spells whole (by the host
     only when it is its preview's one path-named value) and by the screen's prefix rule (item 9). An
     http(s) URL one-word (a WebFetch preview, which the store reduces to its `url`) is free text,
     never a path, and asks the host nothing, whatever its query string holds. Read, Write and Edit
     take an absolute `file_path`, so a rooted plain path is judged as the one file it names.
7. *Free text is shown only when the whitelist vouches for every token* (D63(2)-(4)). Every other
   summary, and every other string of a JSON preview, is free text; an object's keys are screened as
   free text too: D63(1) judges a preview by its decoded strings, and a key is one, which may carry a
   path (`{"private/deny.txt":"x"}` is withheld), at the cost of withholding a key that spells a
   rule's literal (`{"secrets":true}` under `Read(./secrets/**)`). It costs no host judgement. The
   project root's own spelling is first held together as one token-safe unit (item 8); the text is
   then tokenized on ASCII whitespace, except that a simple double-quoted run, and a single-quoted run
   that opens at a token's start, keep their spaces. The summary is SHOWN only when all of these hold,
   and otherwise is withheld ("summary withheld"; the pointer keeps its id and hash):
   - (a) *Every token is safe.* A plain token is built only from Unicode letters, marks and digits
     plus the ASCII set `- _ . , : @ + /`, with `~` and `=` allowed inside a word (`HEAD~1`,
     `--out=x`) but not at a token start or after `= : , @` (a `=` with no name after it, `=` or `==`,
     is allowed), `#` only in a token that starts with one (a comment to every shell), an apostrophe
     only between two letters (`what's`, `user's`) and with no root's unit after the first such
     apostrophe outside a quoted run (below), and `\` allowed only before a character other than
     another backslash; a whole PowerShell splat `@name` (`@args`, `@env:HOME`) is unsafe. A backslash
     that ends a token escapes the space after it, or joins a line the store collapsed to a space
     (`de\ ny.txt`, `<root>\ old`), and one before another backslash leaves a backslash that a nested
     shell would read again, so both make the token unsafe. A token may instead be one of the
     standalone shell operators `&& || | ; > >> 2>&1 2> < >&2 1>&2`; a whole null-device or
     standard-stream token (`/dev/null`, `>/dev/null`, `1>/dev/null`, `2>/dev/null`, `&>/dev/null`,
     `>>/dev/null`, `2>>/dev/null`, `/dev/stdin`, `/dev/stdout`, `/dev/stderr`, and cmd.exe's `>nul`,
     `1>nul`, `2>nul` in any case); a simple double-quoted run `"…"` whose content holds no quote,
     backtick or `$` and whose space-separated words are each safe, a word of a run also allowed to
     hold `(` and `)` (not after `+` or `@`) and a backslash (literal in every shell, below); a simple
     single-quoted run `'…'` at a token's start whose content holds no quote of either kind, backtick,
     `$` or backslash, judged as a double-quoted run's content is; or an http(s) URL whose part after
     the scheme holds only letters, marks, digits and `- . _ ~ : / ? # @ & = +` (no `,`, at which
     PowerShell splits a bare argument into an array, nor any other character a shell splits a word at
     or expands), is a plain token apart from its `?` and `#`, and whose parts after an `&` are plain
     tokens. A token with `;`, `|`, `&&` or `||` glued into it (`TODO|FIXME`, `2>&1;tail`) is split
     there, and each piece must be a safe token. A single `%` in the text is a name character when what
     follows it is not two hex digits (a percent-escape `%XX`), nor `u` and four hex digits (the
     `%uXXXX` escape IIS and JavaScript's `unescape` decode), nor a digit, `*` or `~` (a cmd.exe batch
     parameter: `%1`, `%*`, `%~dp0`), and, in a cut text, the cut did not fall within the five bytes
     after it: no escape, no `%VAR%` pair and no parameter can use it. The null-device tokens, the
     backslash and the parentheses inside a quoted run, the single-quoted run, the apostrophe between
     letters, the split at a glued operator and the single `%` extend D63(2)'s list; the completeness
     argument below covers each. Anything else makes the summary unsafe: a quote elsewhere, a backtick,
     `$`, `!`, `*`, `?`, `[`, `]`, `{`, `}`, `(`, `)` outside a quoted run, `<`, `>`, a lone `&` or `^`
     in a word; a `%` that could be an escape, half of a variable or a batch parameter; a Unicode space
     or quote; a control character; or `<`, `>` or `&` glued into a word (`cat<x`, `a&b`).
   - (b) *No token names an absolute or escaping path*, in either reading of its backslashes (every
     `\` a separator, and every `\` removed). A path may start at a token's or a piece's start, just
     after a short option's first letter or all its letters (`-I/opt`, `-oD:stash`, `-C../x`), and
     just after `=`, `:`, `,`, `@`, an apostrophe, the `#` that starts a token or (in a quoted run) a
     parenthesis (`--out=/x`, `a,/x`, `host:/x`, curl's `@/etc/passwd`, `#/etc/passwd`,
     `"(/etc/passwd)"`). At each there is no leading `/` or `\` (a POSIX root, a drive-less, UNC or
     device Windows path), no `~` (a home directory; at the token's own start (a) rejects it), no
     drive `X:`, no `file:` URL, and no PowerShell drive- or provider-qualified path: a name, a `:` and
     more (`Temp:x`, `Env:HOME`, `HKCU:Software`, `Registry::HKEY_CURRENT_USER`, `FileSystem::x`, or
     any name `New-PSDrive` defines), unless the name holds `.` or `~` (PowerShell 5.1 and 7 both
     refuse either in a drive's name, so `git@github.com:org/x` and `127.0.0.1:8080` name none; before
     a `::` only a `/` rules a provider out) or is an inert prefix: `path` (recall's selector, which
     (c) judges), `sha256` (a hash's text form), or `http` and `https` before a URL's `//`. A name
     with nothing after its `:` (`fix:`) names no path. No `..` (a run of exactly two
     dots) touches the token's start or end, a separator, the root's unit or one of those delimiters:
     that is every `..` segment, even one that stays inside the project (`<root>/a/../b`), and a `..`
     glued to a word before it (cmd.exe's `cd..` and `type..\x`, which cmd.exe reads after `if`,
     `else` or `do` too, where no token position tells a command, so `git diff main..` stays
     withheld); a `..` between two name characters is a range (`HEAD~3..HEAD`), and `...` a Go package
     pattern. Inside a quoted run each word is judged, again with its parentheses removed (a nested
     zsh's group: `(.)(.)/x` is `../x`), and the root's unit followed by a space and another word is a
     sibling of the root (item 8), unless the root is a word of its own after a word that ends in no
     delimiter and the next word is a shell operator (a nested shell's `cd <root> && make`). In an
     http(s) URL a path may start after each `=`, `:` and `@` of the URL proper (its host is no path
     start, so a port is no drive), a `..` in it climbs, and each part after an `&` is judged as a
     token. The root unit followed by a safe relative path is in-project and allowed.
   - (c) *The text names no rule literal or withheld name.* In its case-folded screen form — with `\`
     read as a separator, and again with `\` removed, both again with a quoted run's parentheses
     removed, and always without quotes — the text holds, where a name starts (the text's start,
     after a byte that does not continue a name, or glued to a short option; a non-ASCII letter
     continues a name, so `café.env` is not `.env`), no Read deny or ask rule's literal (the rule's
     last all-literal segment, or the longest literal run of a glob segment, from the specifiers
     `hostperm.RuleSet.ReadRulePatterns` lists) and no basename or relative path of a path this build
     withholds (a file pointer's, a path-keyed drop's, a structured value's that containment or the
     host withholds). In free text the name's end is not judged (`.env.local` holds `.env`; item 2's
     limits); in a structured value a whole literal and a withheld name must also end where a name
     ends: at the value's end, or, past a run of dots (Win32 drops a name's trailing dots), before a
     byte that is not a letter, a digit, `_`, `-` or `~`, so `.env.`, `.env:stream` and
     `deny.txt#L4` name the denied file and `.env.example` does not. A literal that starts a glob
     segment (`secret` for `./secret*`, `.env` for `**/.env*`) counts where a name starts whatever
     follows it, in a structured value too, since every name its rule refuses begins with it. A
     literal from inside a glob run (`.env` for `**/*.env`) may begin inside a name and counts
     anywhere; a bare basename counts only from three bytes. Nor does a `recall` `path:` selector in
     it, after whitespace, a quote of either kind or `=`, select a withheld path by the store's own
     rule.
   - (d) *The rules can tell texts apart.* When the host's rules are unavailable, or a rule has no
     literal part (`Read`, `./**`), or a rule anchored outside the project covers the root or a path
     below it whose relative spelling need hold no literal (`rootCover`, with the host asked once about
     a fresh name below the root when the root may resolve through a link, junction or 8.3 name,
     `rootProbe`), every free text is withheld, as `re_read` fails closed.

   *Why this is complete.* The claim is that no shown free-text summary spells, in any way a shell or
   the model reads it, an absolute or escaping path, a Read rule's literal or a withheld name. Each
   way a whitelisted text could name something other than what it spells is covered.
   - *What a shell does to a token.* A plain token holds no `$`, `!`, backtick, caret, glob or brace
     character, no quote but an apostrophe between letters, and no `%` an escape or a variable could
     use, so no shell expands, substitutes or globs it. A `~` expands only at a word's start or after
     `:` or `=` in an assignment, and zsh's EQUALS expands a `=` there into a command's path; both
     are withheld at (a). Under zsh's EXTENDED_GLOB a `#` repeats the character before it, and a `#`
     that starts a word begins a comment in every POSIX shell and PowerShell (cmd.exe passes it
     verbatim, below); only the second is allowed. PowerShell's
     whole-argument splat `@name` expands a variable (`@HOME` is the home directory) and is withheld;
     an `@` inside a word (`@types/node`, `git@host:x`) is not a splat.
   - *The backslash.* cmd.exe and PowerShell keep it (a separator), and a POSIX shell (bash, zsh, sh,
     fish, Git Bash) drops it and keeps the character after it. Because a safe backslash never ends a
     token and never precedes another backslash, one pass of that rule leaves no backslash behind, so
     a nested shell (`bash -c`, `sh -c`) re-reading the result changes nothing more; (b) and (c) judge
     exactly those two readings.
   - *Where a path can start inside a token.* A program reads a path at an argument's start, as an
     option's value (glued to a short option, or after `=`), in a list (after `,` or `:`, PATH-style,
     or scp's `host:/path`) and as a response or data file (after `@`); PowerShell reads one as a
     parameter's value after `-Param:` and as each element of an array after `,`, and a comment's text
     names its path to the model after the `#` that starts it. (b) looks at each of those places,
     and for a `..` at every delimiter and at a word's end. `+`, `-`, `_` and `.` continue a name.
     At each place a path can begin with a separator, a home, a drive, a `file:` URL or a PowerShell
     drive or provider; PowerShell takes a drive's name from the place a path starts to the first `:`,
     and refuses a name that holds `.`, `~`, `/` or `\`, so (b) withholds every other name before a
     `:` that has more after it, whether or not such a drive exists, except the inert prefixes below.
   - *Each extension of D63(2)'s list* (the final verify of wave 19d), with what a shell can do with
     the characters it admits and the adversarial row that pins it:
     - *The backslash inside a double-quoted run.* The run's content holds no quote, backtick or `$`,
       and a backslash in it never ends a word, so it never precedes a quote, a space or another
       backslash: bash, zsh, fish, PowerShell and cmd.exe, and the Windows argument parser's
       backslash-before-quote rule, all pass it through verbatim as one argument; a nested shell that
       re-reads the run splits it at its spaces into words judged as tokens, in both backslash
       readings (`TestBuild_EveryBackslashReadingIsJudged`).
     - *Parentheses in a run.* No shell gives `(` or `)` meaning inside quotes (PowerShell's `$(` is
       excluded by `$`). A nested shell may read one as a subshell, PowerShell as a subexpression and
       zsh as a glob group: (b) starts a path after each and bounds a `..` at each, (b) and (c) read
       the words again with the parentheses removed, a `~` or `=` after a parenthesis is unsafe, a `#`
       inside a word is unsafe (zsh's `(#i)` flag), and `+(` and `@(` (bash and ksh extglob; `!(`,
       `*(` and `?(` hold unsafe characters) are unsafe
       (`TestBuild_AQuotedRunsParenthesesNeverHideAPath`).
     - *The single-quoted run.* Its content holds no quote, backtick, `$` or backslash, so every POSIX
       shell and PowerShell pass it verbatim, and fish, which reads `\'` and `\\` there, has neither;
       cmd.exe gives `'` no meaning and splits the run at its spaces, into words that are each judged,
       and keeps the quotes, a name the screen reads without them
       (`TestBuild_AnApostropheBetweenLettersAndASingleQuotedRunAreJudged`).
     - *The apostrophe between letters* (narrowed). It opens a quoted span that a POSIX shell and
       PowerShell close only at the next apostrophe, a double-quoted run's included, so after the first
       one the screen's tokens and runs are no longer the shell's words: the span's spaces and
       operators are literal and join tokens into one word. Every token start is judged as a path
       start, and (c) reads the whole text without quotes (`de'n'y.txt` is deny.txt, `m'y s'ecret.txt`
       is `my secret.txt`), which covers that word; no separator, `~` or `..` can touch an apostrophe
       whose neighbours are letters, and (b) starts a path after one, where PowerShell may start an
       argument. The one unit judged by what follows it is the root's (item 8): inside a span the root
       after a path start and a space or an operator is a sibling's name (`echo it's x --o=<root> old
       y'z` hands a program `--o=/q/proj old yz`), so no root may follow such an apostrophe. Before the
       first one a double-quoted run's apostrophe is literal and a single-quoted run closes itself, so
       the shell's words are the screen's (`TestBuild_AnApostropheSpanNeverJoinsTheRootToASibling`).
     - *Operators glued to a word.* Every POSIX shell ends a word at `;`, `|`, `&&` and `||`,
       PowerShell ends a statement or a pipeline element there, and cmd.exe splits at `|`, `&&` and
       `||`. Each piece is judged as a token, so every piece start is a path start. A program that
       keeps `a;b` as one argument (cmd.exe hands an external program the whole token), or a span that
       makes the operator literal, reads a name the name screen already reads whole, whose only path
       start, the token's start, is a piece's start too; the root before an operator is the root's
       rule below (`TestBuild_AGluedOperatorPieceIsJudgedAsAToken`).
     - *The single `%`* (narrowed). A percent-escape needs `%` and two hex digits, IIS's and
       JavaScript's `%u` escape `u` and four, a cmd.exe variable a second `%`, and a batch parameter a
       digit, `*` or `~` after it; with none of those after it no decoder or shell reads the `%` as
       anything but itself, and a cut within the five bytes after it, where they would stand, is unsafe
       (`TestBuild_ASinglePercentIsNoEscapeOrParameter`).
     - *The null device's tokens.* Each is a whole token in a fixed spelling that holds no backslash,
       glob or variable and names no file whose content or name the payload could reveal; a longer
       token that begins with one is judged as any other (`TestBuild_ANullDeviceTokenIsAFixedSpelling`).
     - *The `#` that starts a token, and a run of `=`.* Every POSIX shell and PowerShell read a word
       that starts with `#` as a comment, in which zsh's EXTENDED_GLOB reads nothing; cmd.exe passes it
       verbatim, and a later `#` in it is a name byte, so `#de#ny.txt` names `#de#ny.txt` to cmd.exe
       and holds no rule's literal where a name starts. A path starts after every `#`. A `=` with no
       name after it gives zsh's EQUALS no command to expand
       (`TestBuild_ACommentTokenAndAnEqualsRunExpandNothing`,
       `TestBuild_APathAfterACommentMarkIsJudged`).
     - *A `..` between name characters, and `...`.* No shell or program climbs through `a..b`, a
       revision range (PowerShell's `1..5` is a range in an expression, a string in an argument), and
       Win32 path normalization keeps a component of three dots or more, or strips it at a path's end,
       and never reads it as a parent (`C:\a\b\...\c` stays as written, measured with GetFullPath in
       PowerShell 5.1 and 7); a `..` that touches a token's end, a separator, the root or a delimiter
       climbs (`TestBuild_ADotDotRangeAndAGoPatternNeverClimb`).
     - *An http(s) URL.* Its characters after the scheme are letters, marks, digits and `- . _ ~ : / ?
       # @ & = +`: no shell splits a word at any of them but `&`, after which each part is judged as a
       token, and none expands any of them in a URL (`~` and `=` expand only at a word's start or after
       a delimiter, which plainTokenSafe rejects); its `?` and `#` can glob only below a directory named
       `http:` in the project; after each `=`, `:` and `@` of the URL proper a path is judged as at any
       other path start, and a `..` climbs; its scheme is an inert prefix
       (`TestBuild_AURLIsSafeOnlyFromAStrictCharacterSet`).
     - *The root before `|`, or before a `;` that ends its token.* Every shell ends the word at a `|`,
       and every POSIX shell and PowerShell at the `;`; cmd.exe hands an external program `<root>;`,
       the root's own spelling and a `;`, which reveals no other name. Inside a quoted run neither
       character is admitted (`TestBuild_TheRootBeforeAPipeOrSemicolonIsTheRoot`).
     - *The quoted root before a shell operator* (narrowed). A nested shell splits the run at its
       spaces, so `cd <root> && make` reads the root alone; a program that is no shell takes the run as
       one argument, in which the root is a path's start only at the run's start, after a delimiter (a
       list's reader may trim the space after it) or after a short option, and from there the operator
       is part of a sibling's name (`cat "<root> && make"` opens `/q/proj && make`). The operator ends
       the root only when the root is a word of its own after a word that ends in no delimiter, where
       to that program it lies inside a relative path
       (`TestBuild_AQuotedRootBeforeAnOperatorIsASiblingAtAPathStart`).
     - *The root-led Glob preview* (item 6). Its directory is read by the Glob tool exactly as the host
       judged it, and a shell given the two words would run the directory as a command, which lists
       nothing; its pattern, a safe glob, is judged from the directory and alone, may climb or start
       outside the project in no reading, brace and class included, and its names are screened in both
       backslash readings (`TestBuild_ARootLedGlobPreviewNamesOnlyWhatTheHostJudged`).
     - *The brace glob* (item 6). One level of `{a,b}` is what bash, zsh, fish and a glob library
       expand; each alternative is judged as the glob it makes and must be one, and the braces and
       commas are path starts (PowerShell opens a script block at `{` and splits at `,`); a sequence, a
       nested or unbalanced brace, an escaped comma's backslash ending an alternative and a concrete
       alternative are withheld (`TestBuild_ABraceGlobNeverRespellsAnOutsidePath`).
     - *The inert prefixes.* `path:` is recall's selector, whose value (c) judges by the store's own
       rule; `sha256:` is the hash's text form that expand and re_read take; an http(s) scheme before
       `//` is a URL's. No PowerShell drive or provider has one of these names unless a user defines it
       (item 2's limits), and each is withheld glued after anything else (`--query=path:x`)
       (`TestBuild_APowerShellProviderDrivePathIsWithheld`).
     - *A rooted structured value in one separator style* (item 6). Every reader reads the same names
       after the root, the host's judgement included, so the rules' literals alone can tell its names
       from a refused file's (`TestBuild_AnOutsideNamesakeNeverWithholdsAProjectPath`).
     - *A JSON preview's keys.* A key is a decoded string, screened as free text as every string but a
       path-named value is (`TestBuild_AJSONKeyIsScreenedAsAString`).
   - *What the model reads.* The model sees the text, so (c) reads both backslash readings, strips
     quotes, reads a run's parentheses both ways, folds case where the platform's paths fold, and
     finds a literal wherever a name can start.
   Nothing is left for a shell to do that the whitelist has not already rejected or that (b) and (c)
   do not read; what lies outside the claim (an alias, a link, a Unicode variant, a name relative to a
   `cd`, a glob that selects a file Qompack never recorded without spelling its literal) is item 2's
   list.
   *Deliberate limits.* The whitelist over-withholds, by design: a command that uses a variable,
   globs (outside a Glob preview), runs a regular expression, holds `(`, `)`, `{` or `}` outside a
   quoted run, `;` or `|` inside one, a glob inside a quoted run, two `%` (`git log --format="%h %s"`,
   `date +%Y-%m-%d`), a `#` inside a word (`C#`), or a whole `@name` (`grep @Test`), greps for a
   `// TODO` comment, uses a cmd switch or a slash command (`cmd /c dir`, `/review-pr 1`, both led by
   a separator), mounts the project into a container (`-v "<root>:/src"`), climbs with `..` (even
   `cmake ..` from a build directory inside the project, or `git diff main..`), escapes a space, or
   glues a quoted run to a flag (`--format="…"`) is withheld whether it names a denied file or an
   allowed one, and so is a one-word path with a parenthesis outside quotes (a Next.js route group
   `app/(auth)/login/page.tsx`: PowerShell starts an argument after `)`, which would then read
   `/login/page.tsx` from the drive's root). Since the final verify of wave 19d so is any name, a `:`
   and more at a path start that is no inert prefix (`git log --pretty=format:%h`, `curl
   localhost:3000`, `git show HEAD:src/x.go`, `npm run test:unit`, `docker run -p 8080:80`, a plugin
   skill `{"skill":"plugin:name"}`, recall's `symbol:` and `tool:` selectors, `--query=path:x`), a URL
   that holds a `,` or a path after `=`, `:` or `@` (`?next=/login`, `?q=is:open`), the root after an
   apostrophe outside a quoted run, and a `%` before a digit (`printf %5d`). On the w19d corpus of 274
   previews that is about one in five everyday summaries that name nothing private (53 of 246 under
   UAT-12's rules, 50 at f3196046); each still points by id and hash. Aliases, 8.3 names, links and
   Unicode normalization or compatibility variants typed in free text are not resolved, and a name
   relative to a `cd` cannot be seen (item
   2). Inside a quoted run the root is held together too, although a nested shell (`bash -c "cd
   <root> && …"`) would split a root that has a space at that space; the pieces it would read spell
   only the root's own path, which the payload shows anyway. File pointers and structured summaries
   are still judged by the host, which resolves aliases and links.
8. *The project root is held together* (D63(1), carrying D60(c)). In a project whose path has a space,
   a comma or an apostrophe in it (`C:\Users\John Smith\proj`), splitting a summary on whitespace would
   cut the root apart. The screen finds each contiguous spelling of the root (its separators `/` alone
   on Linux and macOS, and on Windows one style throughout, every one `/` or every one `\`, repeated or
   not; any case where the platform folds; the MSYS and WSL drive spellings `/c/…` and `/mnt/c/…` and
   the `\\?\` prefix on Windows) and marks it as one token-safe unit, so the tokenizer never splits the
   root at its own space and the screen never reads the root's own name as a withheld name. A POSIX
   shell, Git Bash on Windows included, drops a backslash between two of the root's segments and joins
   them (`/home\u/proj` is `/homeu/proj`, `C:/q\proj` is `C:/qproj`, a sibling of an ancestor of the
   root), so a spelling with one there is not the root, and its text is judged as the free text it is
   (the final verify of wave 19d; Windows keeps a spelling in backslashes alone, item 2's limits). A
   spelling glued to a name character before it is not the root (`xC:\q\proj`) unless that is a short
   option's letters
   (`-I<root>/include`, the root as the option's value); and a spelling is the root only when it ends
   the text or is followed by a space, a quote of either kind, a `/` or a `|` (a pipe in every shell;
   no Windows name holds one), by a `;` that ends its token (item 2's sibling limit), or by a `\` when
   the spelling itself uses backslashes. Followed by anything else it is judged as the path outside
   the project it is: `proj2` and `proj.bak` continue the name, `<root>,x`, `<root>:x`, `<root>=x` and
   `<root>;x` name a sibling to a program that does not split at the delimiter, and after a
   forward-slash spelling a POSIX shell reads a `\` as escaping the next character onto the root's
   last segment (`/q/proj\old` is `/q/projold`). No quote state is tracked: a spelling inside a quoted
   run is marked too, and the run's words are judged around the mark. Inside a run the root followed
   by a space and another word is a sibling (`"<root> old/x.txt"` and `'<root> old/x.txt'` are one
   argument each, and so is `"<root> && make"`), while a nested shell's command line may go on after
   the root with an operator when the root is a word of its own after a word that ends in no delimiter
   (`bash -c "cd <root> && go test ./..."`). Outside a run no root may follow an apostrophe that stands
   outside a quoted run (item 7(a)). A quote glued to the root's spelling or
   splitting it (`"<root>"/notes.txt`, `<p>"proj"`) makes the token unsafe. So `cd <root> && go test
   ./...`, `git -C <root> status`, `cd "<root>" && make`, `cd '<root>' && make`, `cat
   "<root>\src\main.go"` on every platform, `Set-Location <root>; go test ./...`, a nested shell that
   changes to the root and the root followed by a word (`<root> TODO`) are shown; a quoted sibling is
   withheld; an apostrophe in the root (`o'brien`) is matched literally, never read as a quote; and a
   Docker `/src` target is over-withheld.
9. *A cut summary, and section 7's reasons* (D63(2), (3) and (5)). The store cuts a preview at 120
   bytes with `…`. A cut summary's last token is judged as a prefix: it is withheld when it, or a piece
   of it split at a glued operator, is itself unsafe or names an outside path, or when the text ends,
   where a name starts, in the first three bytes or more of a rule's literal, of a withheld path's
   basename or relative path, or of a rule's specifier (a literal from inside a glob run counts with
   no boundary). A cut that fell inside a quoted run of either kind leaves an open run, whose content
   is judged as a closed run's is and whose last word is the cut token, so a long quoted commit
   message is shown and one that ends in `private/den` is withheld; a backslash the cut left last
   escapes or separates what the cut hid, so the token before it is judged; a `%` within the two
   bytes before the cut is unsafe. A percent-encoded or typographically quoted cut token is unsafe,
   so a cut inside such a name never shows its prefix; a cut inside a second spelling of the root,
   which the whitelist does not reassemble, is over-withheld. A cut structured value is judged by the
   directory it spells whole (by the host only when it is its preview's one path-named value) and by
   the same prefix rule, so `{"file_path":"private/den…` is withheld; it is never a withheld name.
   No drop entry's reason, in section 7 or in `dropped()`, shows an absolute path outside the project or
   names a path the build withholds. A reason is Qompack's own error prose, not a shell command, so the
   screen is a product-string rule (D63): a part of the error chain that is a path outside the
   project, whole (git's `not a git repository: <path>`), or an operation on one (`open <path>`,
   `CreateFile <path>`, the path judged whole, so a gitdir named `<root> main\.git` or a single-segment
   `/repo.git` is outside), a whitespace token that is an absolute path outside the project, or a
   withheld path's whole spelling at a path boundary (`<root>/private/deny.txt` included). The root is
   held together first (item 8), as in a summary, so in a root with a space its first piece is not
   read as a path outside the project. A bare basename or a rule literal does not redact a reason: an
   allowed `README.md` or `src/CLAUDE.md` beside a withheld `private/README.md` or
   `~/.claude/CLAUDE.md` keeps its restore clause. An approximate count (`~36800 tokens`) is not a home
   path. A reason is read as an error chain joined by `": "`, each part as clauses joined by `"; "`: the
   clause that shows the path becomes `(path withheld)`, the clauses before it stay, and those after it
   stay while they hold no separator. *Why the reason screen still reads an error chain.* D63 asked for
   whitespace tokens; but a Go or Windows error names its path whole after its operation
   (`CreateFile <path>: Access is denied.`), and a path with a space (`<root>
   main\.git\worktrees\wt\index`, a linked worktree's gitdir beside the project) spans whitespace
   tokens, so a rule over whitespace tokens alone would show `proj main`
   (`TestBuild_AReasonNamingAPathAfterAnOperationIsRedacted`). The chain reader splits only at
   Qompack's own `": "` and `"; "` joins and judges a part, or the path after an operation word,
   whole, as a pointer's path is; it models no shell. Entries whose reason is the model's own text
   (item 2), and section 6's own pointer drops whose path `withheld()` has judged, are left as they
   are. The redaction is made where rehydrate reads the entries, so `internal/checkpoint` is
   unchanged. Item 6a restores no instruction file the build withholds (the scanner is handed only the
   pointers section 6 shows, and a `paths:` rule or nested CLAUDE.md outside the project or refused by
   the host is neither rendered nor named), and item 6b indexes and names no skill whose `SKILL.md` the
   build withholds. Under rules that cannot be established both restore and name nothing.
10. *Cost, and hostperm* (D63(4)). A build calls the host's judgement once for each distinct path among
    its file pointers, its path-keyed checkpoint drops, its structured summaries (one path each: a
    one-word value, a pattern one-word whole, the directory of a Glob preview under the root, a rooted
    summary's path part, the one path-named JSON value; for a cut one, the directory it spells whole),
    the instruction and skill files items 6a and 6b would restore, and, while a rule anchored outside
    the project is in force, `rootProbe` (item 7(d)); never for free text, whatever its commands,
    queries, URLs, quotes or glued operators say, never for a brace list's alternatives, and never for
    a value among several path-named values, cut or not. Through the real adapter, 80 Bash previews of
    17 words, 80 canonical-JSON and URL previews (a bare URL summary included), 80 path-named JSON
    arrays of six values each, and 80 arrays of twelve values that the store cut inside a value cost
    exactly the build's 10 file pointers and 10 structured summaries, 20 judgements; 80 commands run
    from the root (`<root>/tools/lintN.ps1 --since HEAD~N && echo ok`) cost 20 + 80, each judging the
    script's path alone; and a project with 5 `paths:` rule files and 5 skills costs 20 + 2 × 5, each
    rule file and each skill file judged once (the row checks the judged rule and skill files as one
    set, each once in the spelling items 6a and 6b hand the judge, so a build that dropped item 6b's
    judgements and judged item 6a's twice, which meets the count, fails it). A judgement is
    `RuleSet.Evaluate` on the path and, when the root resolves elsewhere, on its resolved spelling,
    each of which may read the disk; so a build costs at most two Evaluates per path so judged. `internal/hostperm` is byte for byte its a357d187
    code plus one read-only accessor, `RuleSet.ReadRulePatterns`, which hands the screen the rules'
    specifiers (item 7(c)); hostperm is security-critical, and the round-2 `hostperm.Evaluator` is
    reverted with its rows, nothing it bought still needed.
11. *Size* (the w19d review's simplicity lens). D63 asked that the shell-quoting state machines,
    `stripQuotes`, `protect`'s quote-run logic, `regexLike`, `outsideIn`'s heuristics, `homeOrVarPath`
    and the Unicode boundary guesses be deleted, and they are; but `internal/rehydrate/pathgate.go` is
    not smaller than at e65ada8f. Measured with the pinned gocyclo v0.6.0 and gocognit v1.2.0: lines
    2059 (e65ada8f), 1949 (f708c693), 2410 after the round-2 fixes (f3196046), 2580 after the final
    verify's; non-blank non-comment lines 1401, 1346, 1639, 1737; functions 91, 87, 105, 112; gocyclo
    total 646, 572, 722, 765, maximum 26 throughout (`jsonStrings`); gocognit total 590, 563, 691, 732,
    maximum 38 throughout. The whitelist's completeness checks
    (path starts, `..` edges, both backslash readings, quoted runs, glob containment) replaced the
    state machines roughly line for line, and the extensions that recover everyday summaries (item
    7(a)) each add a small, separately argued rule; the structured half, the noting and the error-chain
    reader are kept by D63. No function holds a quoting state machine.

**Evidence.** `internal/rehydrate`: `TestBuild_ATinyBudgetThatDroppedMaterialIsNeverSilent`,
`TestBuild_TheLossNoticeShrinksToItsSmallestForm`, `TestLossNotice_NothingDroppedStaysEmpty` (item
1); `TestBuild_ArgumentSummariesNeverShowAWithheldPath`,
`TestBuild_ArgumentSummariesFailClosedWithoutHostRules`,
`TestBuild_JoinedArgumentPreviewsNeverShowAWithheldPath`,
`TestBuild_AnAbsolutePathIsJudgedAsTheOneFileItNames`,
`TestBuild_DelimiterCharactersInADeniedPathNeverShowIt`,
`TestBuild_ShellQuotingAndEscapesNeverShowADeniedPath`,
`TestBuild_AKnownWithheldPathIsFoundAnywhereInAText` and
`TestBuild_APathSelectorIsJudgedByWhatItSelects` (rounds 1 and 2: every spelling they withhold is
still withheld); `TestBuild_CheckpointDropsNeverShowAWithheldPath`,
`TestCheckpointPathDrops_AreTheCheckpointersOwn`, `TestBuild_HostRulesAreEstablishedOncePerBuild`
and `TestBuild_APathKnownOnlyFromACheckpointDropIsNamedByNoSelector` (item 4);
`TestBuild_NestedShellsNeverShowADeniedPath`,
`TestBuild_ADotRelativeWithheldPathInFreeTextIsWithheld`,
`TestBuild_ADirectoryRuleNeverPoisonsAnUnrelatedSummary`,
`TestBuild_FileURLsAndDriveRelativePathsAreWithheld`,
`TestBuild_AOneWordArgumentIsJudgedByItsShape`,
`TestBuild_ARuleOverTheWholeProjectWithholdsEveryFreeText`,
`TestBuild_AWithheldAnchorPoisonsNoFreeText`, `TestBuild_UsefulSummariesAreShownUnderTheUAT12Rules`
and `TestScreenLiteral_IsTheRulePatternsLiteralPart` (items 6 and 7);
`TestBuild_AProjectPathWithASpaceShowsItsOwnAbsolutePaths` and
`TestBuild_TheProjectRootFollowedByMoreWordsIsShown` (item 8);
`TestBuild_ATruncatedSummaryNeverShowsTheCutPrefixOfADeniedPath`,
`TestBuild_ADropReasonNeverShowsAnOutsideOrWithheldPath` and
`TestBuild_AShortWithheldNameNeverRedactsAnUnrelatedReason` (item 9);
`TestBuild_FreeTextAsksTheHostNothing` (item 10). The w19c review's rows, each red on 69860c1d:
`TestBuild_AnAbsolutePathGluedToAFlagIsWithheld`, `TestBuild_AnEnvironmentVariablePathIsWithheld`,
`TestBuild_APathNamedArgumentHoldingMoreThanAPathIsScreened`,
`TestBuild_ACutCommandThatStartsLikeJSONIsScreened`,
`TestBuild_AnEscapedLineBreakNeverSplitsADeniedName`,
`TestBuild_APathSelectorAfterAQuoteOrEqualsIsJudged`,
`TestBuild_ARuleAnchoredOutsideTheProjectWithholdsOnlyWhatItCovers`,
`TestBuild_AWithheldNameIsMatchedOnlyWhereANameStarts`, `TestBuild_ACommentMarkerIsWithheld`,
`TestBuild_ADevicePathIsNotOutsideTheProject`, `TestBuild_AOneWordRevisionPoisonsNoFreeText` and
`TestScreenLiteral_ResolvesDotDotAsTheHostDoes` (items 6 and 7);
`TestBuild_ARootedPathWithASpaceIsJudgedByTheHost` (item 6);
`TestBuild_ADockerBindMountIsWithheld` (item 8);
`TestBuild_ATruncatedSummaryNeverShowsTheCutPrefixOfAGlobRulesName`,
`TestBuild_ACutPathArgumentIsAFragmentNotAPath`,
`TestBuild_ACutInsideASecondSpellingOfTheRootIsWithheld` and
`TestBuild_AReasonKeepsItsRestoreBesideAWithheldNamesake` (item 9). The w19c round-2 review's rows,
each red on 9f40a6fc: `TestBuild_ARegularExpressionIsNeitherAPathNorAWithheldName`,
`TestBuild_ARelativePathGluedToAFlagThatLeavesTheProjectIsWithheld` and
`TestBuild_ARuleLiteralIsMatchedOnlyWhereANameStarts` (item 7);
`TestBuild_ACommandRunFromTheRootIsJudgedOnlyThroughItsPath` and
`TestBuild_AJSONPreviewCostsAtMostOneHostJudgement` (items 6 and 10);
`TestBuild_AQuoteGluedAfterTheRootStillNamesASibling` and
`TestBuild_AnApostropheInTheRootIsNotAnOpenQuote` (item 8);
`TestBuild_AnInstructionFileTheHostRefusesIsNeverRestored` (item 9). The w19c round-3 review's
rows, each red on 108f8cd5: `TestBuild_AnEnvDriveSpelledInAnyCaseIsWithheld`,
`TestBuild_AnANSICQuoteOrAnUndecodedEscapeNeverHidesARuleLiteral`,
`TestBuild_AnOutsidePathAfterATypographicQuoteOrAUnicodeSpaceIsWithheld`,
`TestBuild_ARuleOverTheProjectThroughAnAliasWithholdsEveryFreeText`,
`TestBuild_ARootedWordThatIsNoPathPoisonsNoFreeText`,
`TestBuild_ACaretEscapedSeparatorIsASeparator` and
`TestBuild_ADriveLessGlobOutsideTheProjectIsWithheld` (item 7);
`TestBuild_AGlobInAMultiValuedJSONPreviewIsJudgedByWhatItSelects` (item 6);
`TestBuild_AQuoteThenAnEscapedSpaceAfterTheRootNamesASibling`,
`TestBuild_AnEarlierSpellingOfTheRootKeepsItsQuotes` and
`TestBuild_ANestedShellThatChangesToTheRootIsShown` (item 8);
`TestBuild_ACutInsideAnEncodedOrQuotedNameNeverShowsItsPrefix`,
`TestBuild_ASkillTheHostRefusesIsNeverIndexed` and
`TestBuild_AReasonNamingAPathAfterAnOperationIsRedacted` (item 9). The D63 rows:
`TestBuild_NonASCIIInProjectPathsAreShownAndPoisonNothing` and `TestBuild_D63RedFirstWithheldRows`
(the round-3 verify majors); `TestBuild_EveryBackslashReadingIsJudged`,
`TestBuild_APathStartAfterAnyDelimiterIsJudged`, `TestBuild_AURLIsSafeOnlyWithPlainCharacters` and
`TestBuild_AStructuredPathNamesWholeNames` (item 7); `TestBuild_AQuotedRootFollowedByANameIsASibling`
(item 8); `TestBuild_ACutInsideAQuotedRunIsJudgedAsTheRun` and
`TestBuild_ACutPathNamedValueIsScreenedByItsPrefix` (item 9). The D63 round-2 rows:
`TestBuild_AGlobClassNeverRespellsAParentDirectory`, `TestBuild_AFileURLInAPathNamedValueIsOutsideTheProject`,
`TestBuild_AnOutsideNamesakeNeverWithholdsAProjectPath`,
`TestBuild_ARuleLiteralFromAGlobRunKeepsItsOpenEnd` and `TestBuild_ARootLedGlobPreviewIsJudgedAsOneGlob`
(item 6); `TestBuild_AnOperatorGluedToAWordSplitsIt`, `TestBuild_ParenthesesInsideAQuotedRunAreJudged`,
`TestBuild_AnApostropheBetweenLettersAndASingleQuotedRunAreJudged`,
`TestBuild_ASinglePercentIsShownAndAPairIsNot` and `TestBuild_AZshOrPowerShellExpansionIsWithheld`
(item 7); `TestBuild_AReasonInAProjectWithASpaceShowsItsOwnPaths` (item 9);
`TestBuild_AGlobPreviewAndAnOperatorPieceCostOneJudgementEach` (item 10). The rows of the final verify
of wave 19d: `TestBuild_ABackslashInsideTheRootsSpellingIsNoRoot` (item 8),
`TestBuild_AURLIsSafeOnlyFromAStrictCharacterSet`, `TestBuild_APowerShellProviderDrivePathIsWithheld`
and `TestBuild_APathAfterACommentMarkIsJudged` (item 7); and the extension audit's rows, one for each
extension's argument in item 7: `TestBuild_ARootLedGlobPreviewNamesOnlyWhatTheHostJudged`,
`TestBuild_ABraceGlobNeverRespellsAnOutsidePath`, `TestBuild_AGluedOperatorPieceIsJudgedAsAToken`,
`TestBuild_AQuotedRunsParenthesesNeverHideAPath`,
`TestBuild_AnApostropheSpanNeverJoinsTheRootToASibling`,
`TestBuild_AQuotedRootBeforeAnOperatorIsASiblingAtAPathStart`,
`TestBuild_ASinglePercentIsNoEscapeOrParameter`, `TestBuild_ANullDeviceTokenIsAFixedSpelling`,
`TestBuild_ACommentTokenAndAnEqualsRunExpandNothing`,
`TestBuild_TheRootBeforeAPipeOrSemicolonIsTheRoot`, `TestBuild_AJSONKeyIsScreenedAsAString` and
`TestBuild_ADotDotRangeAndAGoPatternNeverClimb`. `internal/daemon`, through
the real adapter and the real host rules: `TestRehydrateHostPaths_ASelectorNamingADeniedFileIsWithheld`,
`TestRehydrateHostPaths_EverySpellingOfADeniedFileIsWithheld`,
`TestRehydrateHostPaths_ADeniedPathWithDelimitersIsWithheld`,
`TestRehydrateHostPaths_AShellEscapedDeniedPathIsWithheld`,
`TestRehydrateHostPaths_AProjectPathWithASpaceShowsItsOwnPaths`,
`TestRehydrateHostPaths_TheProjectRootFollowedByMoreWordsIsShown`,
`TestRehydrateHostPaths_UsefulSummariesAreShownUnderTheUAT12Rules` (`git diff HEAD~1` and `cd <root>
&& go test ./...` among them, in a root with a space),
`TestRehydrateHostPaths_CommonIdiomsAreShownUnderTheUAT12Rules`,
`TestRehydrateHostPaths_APathArgumentHoldingMoreThanAPathIsWithheld`,
`TestRehydrateHostPaths_ARootedPathWithASpaceIsJudgedByTheHost` (a real directory link),
`TestRehydrateHostPaths_RootedCommandsAndRegularExpressionsAreShownUnderTheUAT12Rules`,
`TestRehydrateHostPaths_AnApostropheInTheRootIsNotAnOpenQuote`,
`TestRehydrateHostPaths_EscapedAndCaseFoldedSpellingsAreWithheldUnderTheUAT12Rules`,
`TestRehydrateHostPaths_ARuleOverTheProjectThroughALinkWithholdsEveryFreeText` (a real directory
link or junction), `TestRehydrateHostPaths_AFileURLInAPathNamedValueIsOutsideTheProject`,
`TestRehydrateHostPaths_AnOutsideNamesakeNeverWithholdsAProjectPath`,
`TestRehydrateHostPaths_HandsTheBuildTheRulePatterns` and
`TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers` (item 10's count and,
since the round-2 review, its path-named-array and root-started-command variants, since the round-3
review its instruction-and-skill-files variant, since the D63 review its cut-path-named-array
variant and that every rule file and skill file is judged exactly once, and the paths it judged,
the pass criterion; the wall time is logged, never judged).
`internal/hostperm`: `TestRuleSet_ReadRulePatternsListsEveryReadRule`. The round-2 rows that pinned the evaluator's disk
work (`TestRehydrateHostPaths_SummaryJudgementsAreLinearInTheirWords`,
`TestRehydrateHostPaths_APieceThroughALinkIsJudgedOnDisk`,
`TestRehydrateHostPaths_EveryPreviewShapeIsJudgedWithoutTheDisk` and hostperm's `TestEvaluator_*`)
went with it. Criterion changes (D61): under unavailable host rules
`TestBuild_ArgumentSummariesFailClosedWithoutHostRules` now requires a free-text summary that names
no path to be withheld (D61(2)(d)), and `TestBuild_AKnownWithheldPathIsFoundAnywhereInAText` moves
`{"query":"not my secret.txt.bak at all"}` from shown to withheld (the accepted over-withholding of
item 2's limits). After the w19c review, `TestBuild_AWithheldAnchorPoisonsNoFreeText` shows `echo
$HOMEPAGE is set` in place of `echo $HOME is set`: a home directory's variable ending a word is now
that directory (item 7(c)), withheld for what it says; `$HOMEPAGE` still holds `$home` where a name
starts, and keeps the row's poisoning check. After the w19c round-2 review, the cost row counts a
summary that starts at the root as the structured summary it is (one judgement, of its path part)
and also requires that no judged path holds a space, and three D61 criteria are narrowed with the
evidence item 7 records: a rule's whole-segment literal and a withheld name count only where a name
starts, a withheld basename only from three bytes, and a JSON preview's path-named value only when
it is the preview's one; no earlier row's assertion changed. After the w19c round-3 review, the
cost row gains an instruction-and-skill-files variant (20 + 2 × 5), the build asks the host about
`rootProbe` while a rule anchored outside the project is in force (D61(4)'s bound gains that one
judgement; no exact-count row carries such a rule, so none changed), item 7(b) no longer notes a
drive-less value that names no path on the platform, and item 6b withholds skill files as item 6a
withholds rule files; no earlier row's assertion changed. No golden changed: no golden payload
carries a summary or a drop reason the screen withholds.

*Criterion changes (D63).* The whitelist supersedes D61's shell-quoting screen. Every row that
asserts a WITHHELD spelling keeps it: the review of D63's first implementation restored the ones that
implementation had moved or rewritten (the continuation, `.\.` and escaped-space rows of
`TestBuild_AnEscapedLineBreakNeverSplitsADeniedName`, the root-plus-space sibling of
`TestBuild_ANestedShellThatChangesToTheRootIsShown`, and the denied `cat "<abs>"` spelling of
`TestBuild_DelimiterCharactersInADeniedPathNeverShowIt`). A row that asserted a free-text summary
SHOWN flips to withheld only where D63 withholds it deliberately:
`TestBuild_ShellQuotingAndEscapesNeverShowADeniedPath`, `TestBuild_NestedShellsNeverShowADeniedPath`
and `TestRehydrateHostPaths_AShellEscapedDeniedPathIsWithheld` withhold the allowed files' shapes too
(a quote, a backtick, a parenthesis, an `&` glued to a word, an escaped space);
`TestBuild_DelimiterCharactersInADeniedPathNeverShowIt` and
`TestRehydrateHostPaths_ADeniedPathWithDelimitersIsWithheld` withhold an allowed path holding a
space, an apostrophe or a parenthesis (a comma or an equals sign is still shown);
`TestBuild_AnAbsolutePathIsJudgedAsTheOneFileItNames` and `TestBuild_AOneWordArgumentIsJudgedByItsShape`
withhold the project's own `<root>/README.md` beside a denied `private/README.md`;
`TestBuild_FileURLsAndDriveRelativePathsAreWithheld` withholds an in-project `file:///` URL;
`TestBuild_AnEscapedLineBreakNeverSplitsADeniedName` withholds `cat docs/my\ notes.md`;
`TestBuild_AProjectPathWithASpaceShowsItsOwnAbsolutePaths`, `TestBuild_TheProjectRootFollowedByMoreWordsIsShown`,
`TestBuild_AnApostropheInTheRootIsNotAnOpenQuote` and their `internal/daemon` twins withhold a glob in
free text (`<root>\src *.go`, `<root> **/*.go`), `err != nil` and an escaped apostrophe;
`TestBuild_ACommentMarkerIsWithheld`, `TestBuild_ADockerBindMountIsWithheld` and
`TestBuild_ACutInsideASecondSpellingOfTheRootIsWithheld` (renamed from `…IsNotAUNCShare`,
`…OfTheProjectIsShown` and `…IsShown`) and `TestRehydrateHostPaths_CommonIdiomsAreShownUnderTheUAT12Rules`
withhold comment markers, a container path and a cut inside a second root;
`TestBuild_ADevicePathIsNotOutsideTheProject` withholds `exec 3>/dev/fd/3`;
`TestBuild_AnAbsolutePathGluedToAFlagIsWithheld` withholds `cmd /c dir`;
`TestBuild_AnEnvironmentVariablePathIsWithheld`, `TestBuild_AnEnvDriveSpelledInAnyCaseIsWithheld` and
`TestBuild_AWithheldAnchorPoisonsNoFreeText` withhold `$` variables;
`TestBuild_AnANSICQuoteOrAnUndecodedEscapeNeverHidesARuleLiteral`,
`TestBuild_AnOutsidePathAfterATypographicQuoteOrAUnicodeSpaceIsWithheld`,
`TestBuild_ACutInsideAnEncodedOrQuotedNameNeverShowsItsPrefix`,
`TestBuild_AQuoteGluedAfterTheRootStillNamesASibling`,
`TestBuild_AQuoteThenAnEscapedSpaceAfterTheRootNamesASibling`,
`TestBuild_AnEarlierSpellingOfTheRootKeepsItsQuotes` and `TestBuild_ACaretEscapedSeparatorIsASeparator`
withhold ANSI-C and locale quotes, percent escapes, typographic quotes, Unicode spaces, single
quotes, quote-split roots and carets; `TestBuild_ARegularExpressionIsNeitherAPathNorAWithheldName`,
`TestBuild_ADriveLessGlobOutsideTheProjectIsWithheld`,
`TestRehydrateHostPaths_RootedCommandsAndRegularExpressionsAreShownUnderTheUAT12Rules` and
`TestRehydrateHostPaths_EscapedAndCaseFoldedSpellingsAreWithheldUnderTheUAT12Rules` withhold
backslash-led and caret-anchored regular expressions, `echo (done)`, `$'…'` and `$Env:`;
`TestBuild_ACommandRunFromTheRootIsJudgedOnlyThroughItsPath` withholds `go test ./... --since ~2 days`;
`TestBuild_APathSelectorIsJudgedByWhatItSelects` withholds glob selectors (`path:src/*.go`);
`TestBuild_ANestedShellThatChangesToTheRootIsShown` withholds `cmd /c "cd /d <root> && …"`; and
`TestBuild_ARootedWordThatIsNoPathPoisonsNoFreeText` replaces its victim `find src -name "*.ts"`,
which its own `*` now withholds, by `grep -rn TestParse src/`, so the row still proves the poison noted
nothing. Rows extended with SHOWN summaries: `TestBuild_UsefulSummariesAreShownUnderTheUAT12Rules`
(`git commit -m "fix the bug"`, `{"query":"path:src/main.go"}`, `{"url":…}`, a bare WebFetch URL with
`?a=1&b=2`, `cat "<root>\src\main.go"`, `ls src/ 2>/dev/null`, `café/sub/x.txt`, an in-project
absolute Read with a space) and its `internal/daemon` twin, and
`TestBuild_AnAbsolutePathIsJudgedAsTheOneFileItNames` (`<root>/docs/guide.md`). Restored to their
e65ada8f assertions by the null-device tokens: `TestBuild_ADevicePathIsNotOutsideTheProject` (its name
and four of its five shown rows) and the two `/dev/null` rows of
`TestRehydrateHostPaths_CommonIdiomsAreShownUnderTheUAT12Rules`. The red-first rows are the round-3
verify majors (`TestBuild_NonASCIIInProjectPathsAreShownAndPoisonNothing`,
`TestBuild_D63RedFirstWithheldRows`) and the D63 review's findings, each red on that review's worktree:
`TestBuild_EveryBackslashReadingIsJudged`, `TestBuild_APathStartAfterAnyDelimiterIsJudged`,
`TestBuild_AURLIsSafeOnlyWithPlainCharacters`, `TestBuild_AQuotedRootFollowedByANameIsASibling`,
`TestBuild_ACutInsideAQuotedRunIsJudgedAsTheRun`,
`TestBuild_AStructuredPathNamesWholeNames`, `TestBuild_ACutPathNamedValueIsScreenedByItsPrefix`,
`TestBuild_FreeTextAsksTheHostNothing`'s one-word URL and cut-array previews, and the cut-array variant
of `TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers`. Every rehydrate row,
and every `internal/daemon` rehydrate row, also passes on Linux (a static test binary run in an Alpine
container). No golden changed.

*Criterion changes (D63, round 2).* No row that asserted a WITHHELD spelling at e65ada8f changed. Rows
that D63's first implementation had flipped from SHOWN to WITHHELD, and that a round-2 extension now
proves safe, are restored to their e65ada8f SHOWN assertions: the root-led Glob preview (`<root>
**/*.go`, `<root>\src *.go`) in `TestBuild_TheProjectRootFollowedByMoreWordsIsShown`,
`TestBuild_AProjectPathWithASpaceShowsItsOwnAbsolutePaths`, `TestBuild_AnApostropheInTheRootIsNotAnOpenQuote`
and the `internal/daemon` twins `TestRehydrateHostPaths_TheProjectRootFollowedByMoreWordsIsShown` and
`TestRehydrateHostPaths_AnApostropheInTheRootIsNotAnOpenQuote`; a `&&` glued to a word naming an allowed
file (`cat docs/guide.md&&ls`, `wc -l docs/a,b.md&&echo`, `cat docs/plain.md&&ls`) in
`TestBuild_NestedShellsNeverShowADeniedPath` and `TestBuild_ShellQuotingAndEscapesNeverShowADeniedPath`;
`cat '<root>' old/x.txt` (a single-quoted run, then a separate word) in
`TestBuild_AQuoteThenAnEscapedSpaceAfterTheRootNamesASibling`; and the allowed `docs/John's notes.md`
in every spelling, and `docs/draft (1).md` and `docs/draft(2).md` inside a double-quoted run, in
`TestBuild_DelimiterCharactersInADeniedPathNeverShowIt` and
`TestRehydrateHostPaths_ADeniedPathWithDelimitersIsWithheld` (their unquoted parenthesis spellings stay
withheld). Rows extended: `TestRehydrateHostPaths_UsefulSummariesAreShownUnderTheUAT12Rules` (a Glob
preview of a directory under the root, a brace glob, `TODO|FIXME`, `…2>&1; tail …`, a conventional
commit message, `sed -n '1,50p' …`, `--pretty=format:%h`, a query with an apostrophe; and, withheld, a
Glob of a denied directory, a brace glob with a denied alternative, a single-quoted denied path and a
`file:` URL among path-named values) and the instruction variant of
`TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers`, which now requires each
rule file and skill file to be judged exactly once. Behaviour changes with no earlier row: a structured
value's literal that starts a glob segment keeps its open end; a `=` that starts a name at a word's
start or after a delimiter, a `#` inside a word and a whole `@name` token are unsafe, though D63(2)
listed `=`, `#` and `@` as safe (each a zsh or PowerShell expansion; `C#`, `issue#42`, `x:=y` and
`grep @Test` are over-withheld); a drop reason's part that is a sibling of the root, whole, is
redacted. The red-first rows are the round-2 review's findings and the leaks its fix found, each red
on f708c693 (pathgate.go of that commit overlaid on the new tests): the rows listed under *Evidence*
above as the D63 round-2 rows, and the `file:` URL and namesake rows of `internal/daemon`.

*Criterion changes (D63, the final verify of wave 19d).* No row that asserted a WITHHELD spelling
changed. Three SHOWN spellings flip to WITHHELD under the provider-drive rule of item 7(b), each a
name PowerShell accepts for a drive before a `:` with more after it: `git log --pretty=format:%h -n 3`
in `TestBuild_ASinglePercentIsShownAndAPairIsNot` and in
`TestRehydrateHostPaths_UsefulSummariesAreShownUnderTheUAT12Rules` (`--pretty=format` is a valid drive
name; each row now shows `git log --format=%h -n 3` for the single `%`), and `qompack recall
--query=path:reports` in `TestBuild_APathSelectorAfterAQuoteOrEqualsIsJudged` (`--query=path`; the row
shows `qompack recall path:reports`).
`TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers` now checks the judged rule
and skill files as one set (item 10); it fails both mutants the review
named (item 6b's judgements dropped while item 6a's are judged twice, under a second spelling or past
the memo), which met its count. The narrowed extensions (item 7) and the strict URL set flip no
earlier row. Comments that described D61's regular-expression reading as current
(`TestBuild_ACaretEscapedSeparatorIsASeparator`, `TestBuild_ADriveLessGlobOutsideTheProjectIsWithheld`,
`TestRehydrateHostPaths_RootedCommandsAndRegularExpressionsAreShownUnderTheUAT12Rules`) and the claim
that a JSON preview's keys go unjudged are corrected; `notedValues` loses a token count that
`recordedPath`'s one-word rule already made. Red-first: with pathgate.go of f3196046 overlaid on the
new tests, the four finding rows and the apostrophe, quoted-root and single-`%` audit rows fail on
the spellings their findings name, `TestBuild_ABackslashInsideTheRootsSpellingIsNoRoot` on Windows and
in a Linux container alike; the other audit rows fail there only on their PowerShell-drive and `#`
spellings (`Temp:*`, `{Temp:,x}*`, `a||#/etc/passwd`, `{"Temp:secret.txt":true}`, `#~/.ssh/id_rsa`),
and hold on every spelling their own extension admits. On the w19d corpus of 274 previews (seven
scenarios, the Windows build) f3196046 shows 196 and withholds 78 under UAT-12's rules, with 0 leaks
and 50 over-withheld; the final fixes show 193 and withhold 81, with 0 leaks and 53 over-withheld,
the three flips being `git log --pretty=format:%h -n 3`, `sleep 5 && curl localhost:3000` and
`{"skill":"superpowers:brainstorming"}`.

## Consequences

*The bullets below are the 2026-09-06 record. §21 bounds the first one's "8–12K" by the host's
character ceiling, and replaces the golden pair the fourth one describes.*

- The payload replaces the host's 50K + 25K eager restoration with 8–12K of pointers, verbatim
  non-reconstructible facts, restored instructions and an explicit drop report.
  `TestBuild_SmallerThanStock` asserts that as a test rather than a comment.
- The complete drop report is always persisted to `.qompack/state/rehydrate-<session>.json` even
  when the rendered section 7 is truncated to a counted line, so `dropped()` returns everything
  regardless of what fit.
- Because ordering is by importance and truncation is prefix-wise, every budget cut is
  automatically near-optimal: each item at a smaller budget carries a prefix of what it carried at
  a larger one, and what the prefix left out is named in section 7 instead. The pair of frozen
  goldens is the evidence — `full-8k.txt` is `full-12k.txt` minus the last two restored rules,
  plus the two section-7 lines naming them. That is the property SP-16 will tune the tier
  boundaries against.
- The five payload goldens and `state.json` under `testdata/golden/rehydrate/` are the only
  byte-level assertion on the rendered injection, and they are the reason sections 18 and 19 exist:
  both defects were visible on reading a payload and invisible to every unit test in the package.
  Re-record one only after reading the diff.
