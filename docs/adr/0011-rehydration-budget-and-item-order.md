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

## 23. Amendment (2026-10-02 to 2026-10-04, coordinator decisions D59, D60, D61, D63 and D64, owner decision D50): a loss is never silent, and a free-text summary is shown only when a whitelist proves it safe

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

The final verify of wave 19e found two gaps, and coordinator decision D64 rules on them and on what
that wave changed. (1) The project root's unit (item 8) was held together whatever the root's own
spelling held, so in a project under `o'brien`, `x;y` or `a,b` a shell read another path than the
unit stood for: the root's apostrophe pairs with a later one (`C:/q/o'brien/proj/notes.tx't` is one
argument, `C:/q/obrien/proj/notes.txt`), a `;` ends the command inside the root, PowerShell splits
`C:\q\a,b\proj` into `C:\q\a` and `b\proj`; and a root with a Unicode space or a tab matched a sibling
spelled with an ASCII space. D64(1) rules, STRICT, that the unit applies only when the root's spelling
consists of characters the free-text whitelist admits and no shell splits or reinterprets the root at
(item 8); a root holding any other character has no unit, and a summary spelling it is judged as the
free text it is and withheld. (2) A store cut right after a PowerShell drive's or provider's `:`
(`Get-Content Temp:…`) was shown, while one right after a single-letter drive's was withheld; D64(2)
rules it withheld (item 9). (3) A bare drive or provider name with nothing after its `:` (`Temp:`,
`Env:`, `HKCU:`, a conventional commit's `fix:` and `feat:`) names a drive root and reveals no file;
D64(3) rules it inert, no path outside the project under D50 and D63, with no code change, since
withholding it would hide every conventional commit message (item 7(b) said only that such a name
"names no path", and now says this). A single-letter bare drive (`C:`) stays withheld. (4) D64(4)
accepts wave 19e's three corpus flips to withheld (`git log --pretty=format:%h`, `curl
localhost:3000`, `{"skill":"plugin:name"}`; item 7's limits) and its Windows either-slash root, a
spelling that mixes the two slashes being no root (item 2's limits, item 8). Deciding `+` for the
root unit, as D64(1) asked, found a leak in free text too: cmd.exe's `copy` starts its next source
after a `+` glued into a word (`copy a.txt+b.txt c.txt` concatenates both, and `copy
a.txt+\Windows\win.ini out.txt` reads the drive-rooted file, measured on the Windows host), while the
screen read `+` as continuing a name, so neither a path nor a name started after it and both that
command and `copy a.txt+.env out.txt` were shown. A path and a name now start after a `+` (item 7(b)
and (c)), and `+` is outside the root unit's set (item 8); this is the stricter reading, recorded for
the coordinator's ratification with D64, which ratified it with wave 19f's open items (below). The
round-2 verify of those fixes found two more gaps, and
both are closed under D64(1)'s STRICT reading. `@` stood in the unit's set, though PowerShell splats
a word that is a whole `@name` and a space in the root starts a word (with `$Work` an array, `f
C:\q\John @Work` hands `f` the two arguments `C:\q\John` and the array's element, measured with
PowerShell 7 and 5.1), so `git -C <root> status` was shown in a root ending in ` @Work`; `@` is now
outside the set (item 8). And a drop reason held the root by its spelling with every run of
whitespace folded to one ASCII space, so under a root with a Unicode space, a tab, a run of spaces or
a trailing space it read a sibling spelled with one space as the root and showed that sibling's path,
outside the project (D50; f2171654 did the same, and the round-2 verify measured it on Windows and
Linux). A root that no sanitized text spells exactly is now held in no text, neither a summary nor a
reason, and a reason that names a path under it is redacted (items 8 and 9).

Wave 19f's final verify and its open items brought four more D64 rulings, implemented strictly. (1)
A path-named JSON value the store's cut fell inside counted as the project when it was a start of the
root's spelling compared in screen form, which deletes `` ' " ` \ ^ ``, folds whitespace runs, reads `\`
as `/`, collapses a repeated separator and lower-cases by Unicode, so a cut value that differed from
the root only there showed a directory beside the project (`<q>/John'athan` or `<q>/John  Smith`
beside the root, `<q>/obrien` beside a root `<q>/o'brien`, the Kelvin sign beside a root `kate`,
which NTFS does not fold). The raw spelling is now compared exactly: byte for byte, folding only an
ASCII letter's case and only on Windows and macOS, with nothing deleted and no whitespace folded,
and any other value is judged by the directory it spells (item 9). (2) A program that takes its
command line through the ANSI code page receives each character the code page cannot hold as
Windows' best fit for it, and some best fits are ASCII punctuation (U+02BA is `"`, U+02B9, U+02BC
and U+02C8 an apostrophe, U+0303 `~`). Those code points are now unsafe in the free-text whitelist
and in the root unit on every platform: the whole Spacing Modifier Letters block and the letters
and marks outside it that a measurement of the ANSI code pages found (item 7(a), item 8). (3) A root
with a word that starts with `-` after one of its spaces gets no root unit, since PowerShell binds
such a word as a parameter (item 8). That was the shape the round-2 verify left open. (4) Three
behaviours are ratified with no code change: the `+` path start, name start and root-unit exclusion
(b9505d53); a drop reason keeps holding an exactly spelled root whatever its characters, since a
reason is a product string and not shell input; and `recordedPath` keeps holding every root, since
learning only withholds more (items 7(b), 8 and 9).

Wave 19g's final verify found three more gaps, closed strictly. (1) The root's spelling was found
with RE2's `(?i)` and containment went through `filepath.Rel`, and on Windows both fold case by
Unicode's simple folding, while NTFS keeps a name spelled with the Kelvin sign (U+212A), the long s
(U+017F) or the Angstrom sign (U+212B) beside the one spelled with `k`, `s` or `å`. So a summary
naming a file in such a sibling of the root (an uncut path-named value, free text, a value cut past
the end of the root), a file pointer at one and a drop reason naming one were read as the project
and shown. Every comparison of a path with the root's spelling, and containment, now folds an ASCII
letter's case alone, on Windows and macOS (items 6, 8 and 9), and the daemon's host adapter reads a
path's place below the root by the same rule (since wave 19h's verify, beside the broad reading,
next paragraph); `rootCover`, which asks what the host's rules refuse,
folds as `hostperm` does, by Unicode lower-casing, since folding more there only adds screens. (2)
Item 9 and the criterion changes for wave 19f's open items said a cut sibling differing from the
root by a non-ASCII case was withheld; that held only for a cut inside the root's own spelling, and
both now say what the code does. (3) `rootCover` matched a rule anchored outside the project against
the root in screen form, which deletes `` ' " ` \ ^ ``, while `hostperm` matches raw segments with
`path.Match`, so a rule that spelled a root segment with a `?` for one of those characters, with a
negated class or with an escape refused project files at the host while its literal was never
screened; it now also matches the raw segments as `hostperm` does (item 7(d)).

Wave 19h's verify found that (1) narrowed more than what is shown. The build learns a withheld
path's project-relative names (`note`, and `recordedPath`'s hold of the root in a recorded value) so
that it can later withhold a glob, a selector or a text that names or selects the path, and the drop
reason screen finds a withheld path after the root held as one mark; both went through the same
ASCII-only comparisons. So a file pointer, or a NotebookEdit's value under a root with a space,
recorded under the root spelled with another case of a non-ASCII letter
(`C:\q\Åsa\proj\private\deny.txt` for a root `C:\q\åsa\proj`, which NTFS folds onto the project's own
folder and the host refuses as the project's denied file) was withheld and taught nothing, and
`private/d*`, `private/de?y.txt` and `private/i?` were shown; and a drop reason that glued such a
path to the text around it (`(…)`, `path=…`, `'…'`) was shown. These are closed by **the two-fold
rule**: every judgement takes the safe side under both readings of the root. The strict reading
folds an ASCII letter's case alone where the platform's paths fold (`foldLiteral`, `asciiFoldEqual`,
`RootRelative`); the broad reading also folds case by Unicode, as `filepath.Rel` and RE2's `(?i)`
fold it and as `paths.Key` and `hostperm` lower-case it (`RootRelativeBroad`,
`broadRootSpellingOf`). A judgement that decides a thing is SHOWN (containment, the root's unit in a
summary, `exactRooted`, a cut value's start) takes the strict reading. A judgement that LEARNS or
WITHHOLDS takes both and learns or withholds under the union: `note` learns a withheld path's names
as the strict reading places it and, where the broad reading places it in the project and the host
refuses it there, by its project-relative names too; `recordedPath` holds the root under either
reading; `globSelectsKnown` reads a glob's place under the broad reading; the drop reason screen
withholds a reason that either reading withholds; and the daemon's host adapter judges the resolved
root's spelling of every reading of a path's place below the root (items 6, 8, 9 and 10). Where paths
do not fold the two readings are one.

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
     this limit too (`cd && cat .ssh/config` is shown), and so, since D64(3), is a `cd` to a bare
     PowerShell drive or provider name (`cd Temp: && cat secret.txt` is shown; `cd C: && …`, a
     single-letter drive, is withheld).
   - A PowerShell drive or provider a user defines under one of the inert prefixes' names (`path`,
     `sha256`, `select`, or `http` and `https` before a URL's `//`) is not resolved: `path:src/x` is
     read as recall's selector, `sha256:…` as a hash, `select:…` as ToolSearch's selector (D67(l)) and
     `https://…` as a URL (item 7(b)). Every other name before a `:` is withheld whether or not a
     drive of that name exists.
   - On Windows a spelling of the root in backslashes alone is the root (item 8), as cmd.exe and
     PowerShell read it; Git Bash, reading it unquoted, drops each backslash and reads a drive-relative
     name built from the root's own segments (`C:qproj…`), which lies in the working directory. The
     final verify of wave 19d ruled that Windows keeps either slash, and D64(4) accepts it: a spelling
     that mixes the two is not the root.
   - A Unicode letter or mark that a Windows ANSI code page's best-fit mapping turns into ASCII
     punctuation (`ʺ` U+02BA becomes `"`; `ʹ` U+02B9, `ʼ` U+02BC and `ˈ` U+02C8 become `'`) is no
     longer a letter to the whitelist: D64's ruling on wave 19f's open items took every such code
     point, the whole Spacing Modifier Letters block among them, out of the whitelist and the root
     unit on every platform (`bestFitPunct`, item 7(a)). A fullwidth or compatibility variant that a
     best fit reads as an ASCII letter is the first bullet's limit. A character the ANSI code page
     cannot hold at all reaches a program that reads an ANSI command line as the code page's default
     character `?`, a glob to a program that expands its own arguments (`de统y.txt` as `de?y.txt`);
     it is no best fit, the screen does not model it, and coordinator decision D67(l) accepts it as a
     limit.
   - On macOS Qompack assumes the default case-insensitive APFS volume (coordinator decision D67(m),
     audit 2's finding 85). The root's spelling (`rootSpellingOf`), a cut value's start
     (`rootPrefix`) and containment (`RootRelative`) all fold an ASCII letter's case there, as the
     host's rules do, so a spelling of the root in another ASCII case is the project. On a
     case-sensitive APFS volume, an opt-in format, such a spelling names a different folder, outside
     the project, which a summary may then show; Qompack does not detect the volume's case
     sensitivity. The default volume also folds case by Unicode and ignores normalization, so a
     folder spelled with the Kelvin sign, the long s or the Angstrom sign where the root has `k`,
     `s` or `å` is the project's own folder there (hosted macos-latest, ci 37229942287); the
     screen's ASCII-only fold reads it as a folder outside the project and withholds it, which is
     over-withholding on macOS and the right answer on NTFS and Linux, which keep it beside the
     root. `TestRehydrateHostPaths_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt` asserts each
     platform's premise and the same withheld answer on all three.
   - A path-keyed checkpoint drop past the bound on host judgements (item 10) is withheld unjudged
     in section 7 and `dropped()` and learned as withheld whatever its spelling (item 12), so a text
     that names one the host would allow is withheld too: accepted over-withholding (coordinator
     decision D66(e)). The drops a text names are judged first, so it touches only a session where
     more than 64 named drops need a fresh judgement, or a text that names a drop in a way the
     build's ordering does not read as a name (a cut prefix, a part of a path, a glob). The
     unjudged answer is the drops' own: the instruction and skill files items 6a and 6b restore,
     and a structured summary's one path, are still judged by the host.
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
   depth). Containment compares an absolute path with the root byte for byte once both are
   cleaned, an ASCII letter's case folded only where the platform's paths fold (Windows and macOS)
   and no other character folded (`RootRelative`, since wave 19g's final verify of D64): it went
   through `filepath.Rel`, which folds by Unicode on Windows and folds nothing on macOS, so a
   directory spelled with the Kelvin sign (U+212A), the long s (U+017F) or the Angstrom sign
   (U+212B) where the root has `k`, `s` or `å`, which NTFS keeps beside the root, was the project.
   That reading decides what is shown; what the build learns from a withheld path, and whether a
   glob selects one, also reads the root broadly (`RootRelativeBroad`: each of the root's path
   elements equal under Unicode's simple folding or once lower-cased by `paths.Key`), under the
   two-fold rule (wave 19h's verify): a withheld `C:\q\Åsa\proj\private\deny.txt` under a root
   `C:\q\åsa\proj` teaches `private/deny.txt` wherever the host refuses it, so `private/d*` is
   withheld.
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
     relaxes D63's "a one-word summary must ALSO pass the free-text whitelist" for patterns, and
     coordinator decision D67(l) ratifies it, with 19d's stricter extensions of D63(2).
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
     `README.md` (accepted over-withholding, which D67(l) keeps as shipped).
   - *Several path-named values, and cut ones.* A path-named JSON value is exempt from the whitelist
     (it is a structured identifier, not a command), and its names are whole names too; a preview with
     SEVERAL path-named values costs no host judgement: each is screened by containment, by the rule
     literals and withheld names item 7(c) lists, and — if a glob — by what it selects, never by the
     host. A value the store's cut fell inside is judged by the directory it spells whole (by the host
     only when it is its preview's one path-named value) and by the screen's prefix rule (item 9). An
     http(s) URL one-word (a WebFetch preview, which the store reduces to its `url`) is free text,
     never a path, and asks the host nothing, whatever its query string holds. Read, Write and Edit
     take an absolute `file_path`, so a rooted plain path is judged as the one file it names.
   - *A path-named value that holds several paths* (audit 2's finding 26). A value may be a list a
     tool splits (`{"paths":"src/a.ts /etc/passwd"}`, `{"file":"src/a.ts,C:\\x"}`), and judged whole
     a list whose first piece is relative was a relative path the host, joining it under the root,
     refused nothing about. So each piece of every path-named value, split at whitespace, a control
     character (C0, DEL or C1: a NUL-separated list is what `find -print0` and `git ls-files -z`
     write, and the store's preview keeps the NUL as `\u0000`), a comma, a semicolon or a bar
     (`valueListSep`), is also judged for a path outside the project, with no host judgement,
     wherever a reader starts a path in it (`valuePathStart`): at the piece's start; after an opening
     quote or bracket (`"`, `'`, a backtick, `(`, `[`, `{`, `<`), a `:` other than a drive's, `=`,
     `@`, or a glued redirect or command separator (`>`, `>>`, `&`, `&&`), anywhere in it; after a
     character outside ASCII that the free-text whitelist reads as no letter (`wordRune`): one that is
     no letter, mark or digit (a zero-width space hides a path's start from a reader), or a letter or
     mark whose Windows ANSI best fit is ASCII punctuation (U+02BA reaches an ANSI program as `"`,
     U+01C0 as `|`; D64); after a run of punctuation that leads the piece (`>/etc/passwd`, `!/x`,
     `+\x`, `#/x`, `)/x`); and after a leading short option (`-I/x`). Inside a piece most other
     punctuation is a name's own (`c++`, `C#`, SvelteKit's `+page`, Next.js's `(auth)` and `[id]`),
     so `+ # ) ] } ! ^` start no path there: a deliberate residual, since a rooted path after one of
     them is a name's next segment to every reader but cmd.exe's `copy a.txt+\Windows\win.ini` (a
     `#`, `!` or `^` mid-word, or a closing bracket, starts no path for a shell), and reading them as
     a path's start would withhold `c++/x`, `C#/x`, `(auth)/x` and `[id]/x`; the same text in free
     text is withheld. From each such place, what runs to
     the piece's end names a path outside the project when it is not inside the project (an absolute
     path, a home, a variable, a drive-relative path, a `file:` URL, or a climb, which containment
     resolves, so `--out=../../x` and `"../x"` climb out while `src/../a.ts` stays in), with or
     without the closing quotes and brackets it ends in (`".."` is the parent); when, not rooted, it
     climbs out in either other reading of its backslashes (`..\x` on Linux, `.\./x`); or when it is a
     PowerShell drive- or provider-qualified path (a drive letter's is containment's), unless another
     place a path starts lies inside the name before its `:` (`"C:\q\proj"` and `--out=C:\q\proj`
     name no drive `"C` or `--out=C`; `--out=Temp:x` names Temp). An http(s) URL that starts there is
     judged as free text judges one, up to a quote or an angle bracket (`valueNamesOutside`,
     `pieceOutside`, `restOutside`). Wave 22's verify found the first version, which judged only a
     piece's start and a rooted path after an inner `:`, `=` or `@`, still showing a quoted later
     piece (`"src/a.ts" "/etc/passwd"`, `'~/.ssh/id_rsa'`) and a climb after an option's `=` or an
     `@` (`--out=..` is one segment the next `..` removes), all shown at eca33155 too; its fix round 2
     found a control character, a glued `>` or `&` and a best-fit letter still showing the outside
     path after them (`src/a.ts\u0000/etc/passwd`, `src/a.ts>/etc/passwd`, `src/a.ts&&/etc/passwd`,
     `src/a.tsʺ/etc/passwd`), shown at eca33155 too. The over-withholding they cost (D66(e)) is a
     project path whose segment ends in `&` or `>` directly before a separator (`R&/x`), or in a
     best-fit letter or mark (a segment ending in an okina, or an NFD spelling of `città/` whose
     combining grave ends the segment); none is in the corpus. Where a path
     may start, the root's own spelling as containment compares it is read whole (`rootSpanAt`) and
     none of its characters starts a path, so a project path under a root with a space, a comma, a
     semicolon or a parenthesis stays one piece whatever else the root holds (a value is no shell
     input, so D64(1)'s set does not apply), and a single project path with a space in it, quoted or
     not, is shown. A cut value's last piece is the start of a piece: from a place a path starts
     that begins the root's own spelling byte for byte to the cut it is the project (D64(8)).
7. *Free text is shown only when the whitelist vouches for every token* (D63(2)-(4)). Every other
   summary, and every other string of a JSON preview, is free text; an object's keys are screened as
   free text too: D63(1) judges a preview by its decoded strings, and a key is one, which may carry a
   path (`{"private/deny.txt":"x"}` is withheld), at the cost of withholding a key that spells a
   rule's literal (`{"secrets":true}` under `Read(./secrets/**)`). It costs no host judgement. The
   project root's own spelling is first held together as one token-safe unit (item 8); the text is
   then tokenized on ASCII whitespace, except that a simple double-quoted run, and a single-quoted run
   that opens at a token's start, keep their spaces. The summary is SHOWN only when all of these hold,
   and otherwise is withheld (`(summary withheld)`, explained once by section 6's legend line, item
   12; the pointer keeps its id and hash):
   - (a) *Every token is safe.* A plain token is built only from Unicode letters, marks and digits
     (none whose Windows ANSI best fit is ASCII punctuation, below) plus the ASCII set
     `- _ . , : @ + /`, with `~` and `=` allowed inside a word (`HEAD~1`, `--out=x`) but not at a
     token start or after `= : , @` (a `=` with no name after it, `=` or `==`, is allowed), `#` only
     in a token that starts with one (a comment to every shell), an apostrophe
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
     the scheme holds only letters, marks, digits (as above) and `- . _ ~ : / ? # @ & = +` (no `,`,
     at which PowerShell splits a bare argument into an array, nor any other character a shell splits
     a word at or expands), is a plain token apart from its `?` and `#`, and whose parts after an
     `&` are plain
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
     just after `=`, `:`, `,`, `@`, an apostrophe, the `#` that starts a token, a `+` (since D64) or
     (in a quoted run) a parenthesis (`--out=/x`, `a,/x`, `host:/x`, curl's `@/etc/passwd`,
     `#/etc/passwd`, cmd.exe's `copy a.txt+\Windows\win.ini`, `"(/etc/passwd)"`). At each there is no
     leading `/` or `\` (a POSIX root, a drive-less, UNC or
     device Windows path), no `~` (a home directory; at the token's own start (a) rejects it), no
     drive `X:`, no `file:` URL, and no PowerShell drive- or provider-qualified path: a name, a `:` and
     more (`Temp:x`, `Env:HOME`, `HKCU:Software`, `Registry::HKEY_CURRENT_USER`, `FileSystem::x`, or
     any name `New-PSDrive` defines), unless the name holds `.` or `~` (PowerShell 5.1 and 7 both
     refuse either in a drive's name, so `git@github.com:org/x` and `127.0.0.1:8080` name none; before
     a `::` only a `/` rules a provider out) or is an inert prefix: `path` (recall's selector, which
     (c) judges), `sha256` (a hash's text form), `select` (ToolSearch's selector, D67(l)), or `http`
     and `https` before a URL's `//`. A bare
     name with nothing after its `:` (`Temp:`, `Env:`, `HKCU:`, a conventional commit's `fix:` and
     `feat:`) names a drive root, which D64(3) rules inert: it reveals no file and is no path outside
     the project under D50 and D63 (withholding it would hide every conventional commit message). A
     single-letter bare drive (`C:`) is still withheld, and a store cut right after any drive's `:` is
     withheld (item 9). No `..` (a run of exactly two
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
     token. The root unit (item 8; only a root whose spelling admits one has it, D64(1)) followed by
     a safe relative path is in-project and allowed.
   - (c) *The text names no rule literal or withheld name.* In its case-folded screen form — with `\`
     read as a separator, and again with `\` removed, both again with a quoted run's parentheses
     removed, and always without quotes — the text holds, where a name starts (the text's start,
     after a byte that does not continue a name, `+` among them since D64 (`copy a.txt+.env out`), or
     glued to a short option; a non-ASCII letter
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
     `rootProbe`), every free text is withheld, as `re_read` fails closed. `rootCover` matches the
     rule against the root as the host does (`hostperm`'s raw segments with `path.Match`, a `\` an
     escape in its segment, and, on every platform, the alias that reads a `\` as a separator, which
     `hostperm` adds on Windows; the case lower-cased by Unicode where paths fold, as `hostperm` folds
     a rule and a path), and also in screen form, as it did before wave 19g's final verify of D64,
     since each extra reading can only add screens. Screen form alone deletes `` ' " ` \ ^ `` from
     both, so a rule whose root segment used a `?` for one of them (`o?brien` for a root `o'brien`),
     a negated class whose `^` it deleted (`[^x]brien` for `obrien`) or an escape (`o\'brien`)
     refused project files at the host while the screen never learned the literal of what follows
     the root (`.key` for `//<root>/**/*.key`).

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
   - *Windows' ANSI best fit* (D64's ruling on wave 19f's open items). A program that takes its
     command line through the ANSI code page (a C program's `argv`, `GetCommandLineA`) receives each
     character that code page cannot hold as Windows' best fit for it (`WideCharToMultiByte` without
     `WC_NO_BEST_FIT_CHARS`). Some best fits are ASCII punctuation, which no shell produces from a
     letter and (b) and (c) would not read: U+02BA becomes `"`, U+02B9, U+02BC and U+02C8 an
     apostrophe, U+0302 `^`, U+0303 `~`. So no letter, mark or digit whose best fit is ASCII
     punctuation is safe (`wordRune`, `bestFitPunct`), in a word, a quoted run, a URL or the root's
     unit (item 8). The set is every code point of the Spacing Modifier Letters block (U+02B0 to
     U+02FF), where most such mappings lie, and outside it U+01C0 (`|`), U+01C3 (`!`), U+0300 (an
     apostrophe or a backtick), U+0302 and U+0303, U+030E (`"`), and U+0331 and U+0332 (`_`). They
     were measured with `WideCharToMultiByte` over every Unicode scalar value in the ANSI code pages
     874, 932, 936, 949, 950 and 1250 to 1258 on the Windows host, counting a best fit to any ASCII
     character other than a letter or a digit (`TestWhitelist_NoANSIBestFitToPunctuationIsSafe`
     repeats the measurement on every Windows run). No other modifier letter (Lm) has such a best
     fit, and a modifier symbol (Sk) was never safe. The OEM code pages' further best fits (U+0301
     and U+0308 to an apostrophe and `"` in code page 437, U+0327 to `,`, U+20DD to a tab) are left
     as they are, since no command line is converted into an OEM code page.
   - *Where a path can start inside a token.* A program reads a path at an argument's start, as an
     option's value (glued to a short option, or after `=`), in a list (after `,` or `:`, PATH-style,
     or scp's `host:/path`) and as a response or data file (after `@`); PowerShell reads one as a
     parameter's value after `-Param:` and as each element of an array after `,`, and a comment's text
     names its path to the model after the `#` that starts it; cmd.exe's `copy` reads its next source
     after a glued `+` (`copy a.txt+\Windows\win.ini out`; D64, ratified with wave 19f's open
     items, as is `+`'s exclusion from the root unit, item 8). (b) looks at each of those places,
     and for a `..` at every delimiter and at a word's end. `-`, `_` and `.` continue a name (cmd.exe's
     built-in commands split their arguments at its documented delimiters, a space, a tab, `,`, `;`
     and `=`, each already a split or a path start here, and `copy` also at `+`; `type a,b` types
     `a` and `b` while `type a+b` reads one file named `a+b`, measured).
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
       rule; `sha256:` is the hash's text form that expand and re_read take; `select:` is ToolSearch's
       documented selector, with which Claude Code loads a deferred tool (D67(l),
       `TestBuild_ToolSearchsSelectorIsShown`); an http(s) scheme before `//` is a URL's. No
       PowerShell drive or provider has one of these names unless a user defines it (item 2's limits),
       and each is withheld glued after anything else (`--query=path:x`)
       (`TestBuild_APowerShellProviderDrivePathIsWithheld`). What follows an inert prefix's `:` is
       still a path start (`select:/etc/passwd` is withheld).
     - *A rooted structured value in one separator style* (item 6). Every reader reads the same names
       after the root, the host's judgement included, so the rules' literals alone can tell its names
       from a refused file's (`TestBuild_AnOutsideNamesakeNeverWithholdsAProjectPath`).
     - *A JSON preview's keys.* A key is a decoded string, screened as free text as every string but a
       path-named value is (`TestBuild_AJSONKeyIsScreenedAsAString`).
   - *The root's unit* (D64(1)). The unit hides the root's own characters from the token checks, so it
     is held only for a root whose characters no shell splits or reinterprets anywhere in a word (item
     8's set) and that a sanitized text spells exactly; any other root has no unit, and its spelling is
     judged character by character as the free text it is
     (`TestBuild_ARootOutsideTheWhitelistHoldsNoRootUnit`,
     `TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit`,
     `TestBuild_ARootNoTextSpellsExactlyIsNeverHeld`). A store cut right after a
     drive's `:` is judged as if a name followed it (item 9,
     `TestBuild_ACutRightAfterAProviderDriveColonIsWithheld`).
   - *What the model reads.* The model sees the text, so (c) reads both backslash readings, strips
     quotes, reads a run's parentheses both ways, folds case where the platform's paths fold, and
     finds a literal wherever a name can start.
   Nothing is left for a shell to do that the whitelist has not already rejected or that (b) and (c)
   do not read; what lies outside the claim (an alias, a link, a Unicode variant, a name relative to a
   `cd`, a glob that selects a file Qompack never recorded without spelling its literal) is item 2's
   list. So is a character the ANSI code page cannot hold at all, which a program reading an ANSI
   command line receives as the code page's default character `?`, a glob to a program that expands
   its own arguments (`de统y.txt` reaching such a program as `de?y.txt`); it is no best fit, so D64's
   ruling does not reach it, and coordinator decision D67(l) accepts it as a limit.
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
   apostrophe outside a quoted run, and a `%` before a digit (`printf %5d`); D64(4) accepts the three
   of these that wave 19e flipped from shown (`git log --pretty=format:%h`, `curl localhost:3000`,
   `{"skill":"plugin:name"}`). Since D64 so is every summary that spells a root whose own spelling has
   no unit (item 8: a root with an apostrophe, a comma, an `@`, a `~` or any other character outside
   the unit's set, a letter whose ANSI best fit is ASCII punctuation, a word that starts with `-`
   after a space, `OneDrive - Contoso` among them, or a run of spaces), a word holding such a letter
   or mark (the Hawaiian ʻokina U+02BB, the U+0300 of a decomposed `è`), and a store cut right after a
   drive's `:` (item 9). On the w19d corpus of 274 previews that is about one in five everyday
   summaries that name nothing private (53 of 246 under UAT-12's rules, 50 at f3196046, and 53 again
   at f2171654, after D64's fix, after its round-2 fixes and after its rulings on wave 19f's open
   items, whose corpus roots all keep their unit); each still points by id and hash. Aliases, 8.3
   names, links and Unicode normalization or compatibility variants typed in free text are not
   resolved, and a name
   relative to a `cd` cannot be seen (item
   2). Inside a quoted run the root is held together too, although a nested shell (`bash -c "cd
   <root> && …"`) would split a root that has a space at that space; the pieces it would read spell
   only the root's own path, which the payload shows anyway. File pointers and structured summaries
   are still judged by the host, which resolves aliases and links.
8. *The project root is held together* (D63(1), carrying D60(c); D64(1)). In a project whose path has
   a space in it (`C:\Users\John Smith\proj`), splitting a summary on whitespace would cut the root
   apart. The screen finds each contiguous spelling of the root (its separators `/` alone
   on Linux and macOS, and on Windows one style throughout, every one `/` or every one `\`, repeated or
   not; an ASCII letter in either case where the platform's paths fold, and no other character in
   another case; the MSYS and WSL drive spellings `/c/…` and `/mnt/c/…` and
   the `\\?\` prefix on Windows) and marks it as one token-safe unit, so the tokenizer never splits the
   root at its own space and the screen never reads the root's own name as a withheld name. Until
   wave 19g's final verify of D64 the spelling was matched with RE2's `(?i)`, which folds by Unicode's
   simple folding and so found the root in a directory spelled with the Kelvin sign, the long s or
   the Angstrom sign where the root has `k`, `s` or `å`, which NTFS keeps beside the root; each ASCII
   letter is now a class of its two cases (`foldLiteral`). The root spelled with another non-ASCII
   letter in another case that NTFS does fold (`Åsa`, U+00C5, for a root `åsa`) is no longer held
   either where the build decides what a summary may show, and a summary spelling it is
   over-withheld. That is the strict reading of the two-fold rule (wave 19h's verify): the learning
   of a withheld path (`recordedPath`) and the drop reason screen (item 9), which only withhold,
   also hold the root as the broad reading spells it (`broadRootSpellingOf`: RE2's `(?i)` over the
   root and the text both lower-cased by `paths.Key`, so `Åsa`, the Kelvin sign's `Kate`, `ſam` and
   `İris` for `iris` are the root there too), and withhold under the union. So a withheld path
   recorded under `C:\q\Åsa berg\proj` is learned by its project-relative names where the host refuses
   it, and a reason naming the withheld private/deny.txt through that spelling is redacted.
   *Which roots have a unit* (D64(1), STRICT). The unit stands for the root only if every shell reads
   the root's spelling as that one path, so it is held only when the root's own spelling, cleaned and
   slash-separated, consists of Unicode letters, marks and digits the whitelist admits (none whose
   Windows ANSI best fit is ASCII punctuation, item 7(a)), `- _ .`, its separators, single ASCII
   spaces between other characters and, on Windows, the drive's `:`, and no word of it starts with
   `-` after one of its spaces (`rootUnitAdmitted`). These are the free-text whitelist's characters
   at which no shell splits or reinterprets a word; a space is the case the unit exists for, read by
   the sibling rules below. D64's rulings on wave 19f's open items added the last two conditions. A
   letter whose best fit is punctuation reaches a program that reads an ANSI command line as that
   punctuation (`C:\q\aʺb\proj` as `C:\q\a"b\proj`), and since the unit hides the root's characters
   from the whitelist, the whitelist's own exclusion would not see it. A word of the root that starts
   with `-` after a space binds as a parameter of a PowerShell cmdlet or advanced function (`g
   C:\q\John -Force\proj` sets `g`'s `-Force`, measured with PowerShell 7 and 5.1). A lone `-`
   (`OneDrive - Contoso`, the folder OneDrive for Business creates) is passed as text, but it is a
   word that starts with `-` too, so it loses its unit with the rest, as the ruling words it; a `-`
   inside a word, or starting a segment after a separator, keeps it.
   The whitelist's other characters are left out: `@` (PowerShell splats a word that is a whole
   `@name`, and a space in the root starts a word: with `$Work` an array, `f C:\q\John @Work` hands
   `f` the two arguments `C:\q\John` and the array's element, measured with PowerShell 7 and 5.1, so
   in a root ending in ` @Work` `git -C <root> status` runs on another path; the round-2 verify of
   D64 found it, and D64(1) leaves out a character any shell reinterprets the root at, wherever in the
   root it stands), `,` (PowerShell splits a bare argument into an
   array at it, so `C:\q\a,b\proj` is `C:\q\a` and `b\proj`, and cmd.exe's built-in commands split at
   it: `type a,b` types `a` and `b`, measured), `=` (cmd.exe's built-in commands split at it the same
   way), `+` (cmd.exe's `copy` starts its next source at it, so `C:\q\a+b\proj\x.txt` is `C:\q\a` and
   `b\proj\x.txt` to it), `#` (zsh's EXTENDED_GLOB reads `a#b` as a pattern), and a `:` past the drive
   (PowerShell reads the name before a `:` as a drive, and a list's reader splits there). So is every
   character the whitelist rejects: a quote of either kind or a
   typographic one (a shell pairs the root's apostrophe with a later one, so
   `C:/q/o'brien/proj/notes.tx't` is the one argument `C:/q/obrien/proj/notes.txt`), a backtick, `$ !
   ; & | ( ) [ ] { } < > ^ % ~ * ?` (`/q/x;y/proj` runs `/q/x`; zsh's EXTENDED_GLOB reads the `~` of an
   8.3 name), a backslash that is no separator (on Linux and macOS a POSIX shell drops it), a control
   character, and a Unicode space (folded to an ASCII space in the root's spelling, so the unit matched
   a sibling spelled with one). So is a run of spaces, or a space at either end of the root: the
   store's preview and the screen (`sanitize`) spell each run of whitespace as one space, so no text
   spells such a root exactly, and its folded spelling matches a sibling spelled with one space (`a b`
   beside a root `a  b`, `proj` beside a root `proj `; `rootSpelledExactly`). A root holding any of
   them has no unit: a summary spelling it is judged as the free text it is and withheld, `cd <root>
   && …`, `git -C <root> …`, the root's Grep and Glob previews and a one-word Read under it among
   them. A path-named JSON value under such a root is still a structured value (item 6), which no
   shell reads. A drop reason is Qompack's own error prose, not a shell command, so its screen (item 9)
   still holds the root together whatever characters it holds, but only a root that a sanitized text
   spells exactly: that is what finds a withheld project path named absolutely
   (`<root>/private/deny.txt`, which an unheld root would leave as the tail of a longer path) and keeps
   an allowed one. A root with a Unicode space, a tab or another control character, a run of spaces or
   a space at either end is held in no reason (the round-2 verify of D64): its folded spelling matched
   a sibling spelled with one ASCII space and showed that sibling's path, outside the project
   (`checkpoint: git worktree gitdir at C:\q\a b\proj\.git\worktrees\wt is unreadable` under a root
   `C:\q\a<U+00A0>b\proj`). Unheld, such a root's own whitespace or control character splits a path
   under it, whose first piece is outside the project, so the reason fails closed: one that names a
   path under the root is redacted too. D64 ratified this with wave 19f's open items: a reason keeps
   holding an exactly spelled root whatever its characters, since it is a product string and not shell
   input. The learning of a withheld path's names (`recordedPath`) holds the root whatever its
   spelling, exact or not, since learning more only withholds more; D64 ratified that too
   (`TestBuild_AWithheldPathIsLearnedUnderEveryRoot` pins it under roots with an apostrophe, a run of
   spaces, a dash word and a best-fit letter). A POSIX
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
   withheld; a root with an apostrophe (`o'brien`), a best-fit letter (`aʼb`) or a dash word (`John
   -Force`, `OneDrive - Contoso`) has no unit since D64, so every summary that spells it is withheld;
   and a Docker `/src` target is over-withheld.
9. *A cut summary, and section 7's reasons* (D63(2), (3) and (5)). The store cuts a preview at 120
   bytes with `…`. A cut summary's last token is judged as a prefix: it is withheld when it, or a piece
   of it split at a glued operator, is itself unsafe or names an outside path, or when the text ends,
   where a name starts, in the first three bytes or more of a rule's literal, of a withheld path's
   basename or relative path, or of a rule's specifier (a literal from inside a glob run counts with
   no boundary). A cut that fell inside a quoted run of either kind leaves an open run, whose content
   is judged as a closed run's is and whose last word is the cut token, so a long quoted commit
   message is shown and one that ends in `private/den` is withheld; a backslash the cut left last
   escapes or separates what the cut hid, so the token before it is judged; a `%` within the two
   bytes before the cut is unsafe. A cut token that ends right after the `:` of a PowerShell drive or
   provider name at a path start (`Get-Content Temp:…`, `x,Env:…`, `--dir=Temp:…`, `"gc Temp:…`, a
   URL's `a=Temp:…`) is judged as if a name followed its `:` (`cutAtDriveColon`, D64(2)), since the
   cut may hide the file after it, as a single-letter drive's `C:…` already was; an inert prefix
   (`path:…`, `sha256:…`) stays inert. A percent-encoded or typographically quoted cut token is unsafe,
   so a cut inside such a name never shows its prefix; a cut inside a second spelling of the root,
   which the whitelist does not reassemble, is over-withheld. A cut structured value is judged by the
   directory it spells whole (by the host only when it is its preview's one path-named value) and by
   the same prefix rule, so `{"file_path":"private/den…` is withheld; it is never a withheld name. A
   cut value that is the start of the root's own spelling is the project (`rootPrefix`). Since D64's
   ruling on wave 19f's final verify it must start that spelling byte for byte: the cleaned root in
   the platform's separators or, on Windows, in `/` throughout, with an ASCII letter's case folded
   only on Windows and macOS, nothing deleted and no whitespace folded. It was compared in screen
   form, which deletes `` ' " ` \ ^ ``, folds whitespace runs, reads `\` as `/`, collapses a repeated
   separator and lower-cases by Unicode, so a cut value that differed from the root only there
   (`<q>/John'athan`, `<q>/John^athan` or `<q>/John  Smith` beside the root, `<q>/obrien` beside a
   root `<q>/o'brien`, on Linux `/q\Johnathan`, the entry `q\Johnathan` of `/`, and the Kelvin sign
   beside a root `kate`, which NTFS does not fold to `k`) showed a directory beside the project. Any
   other value is judged by the directory it spells, by containment (item 6) and, when it is its
   preview's one path-named value, by the host. A cut sibling that differs from the root only by a
   quote, a caret, a backslash or a whitespace run is outside the project wherever the cut falls,
   since containment never folded those. One that differs by a letter that Unicode's simple folding
   pairs with the root's (the Kelvin sign, the long s, the Angstrom sign for `k`, `s`, `å`) was
   withheld only when the cut fell inside the root's own spelling: cut past its end, the directory
   it spells went to containment, which folded case by Unicode on Windows, found it inside, and
   showed it. Since wave 19g's final verify containment folds an ASCII letter's case alone
   (`RootRelative`), so such a sibling is withheld wherever the cut falls, on every platform. A
   repeated separator in a value cut inside the root's spelling, which names the root, is
   over-withheld, and so is a cut value under the root spelled with another non-ASCII letter in
   another case that NTFS does fold (`Åsa`, U+00C5, for a root `åsa`): deciding what is shown takes
   the strict reading of the root. That is not all such a spelling does: a withheld path under it
   still teaches the build its project-relative names, and a drop reason naming one is redacted
   (the two-fold rule, item 8; a cut value itself teaches nothing).
   No drop entry's reason, in section 7 or in `dropped()`, shows an absolute path outside the project or
   names a path the build withholds. A reason is Qompack's own error prose, not a shell command, so the
   screen is a product-string rule (D63): a part of the error chain that is a path outside the
   project, whole (git's `not a git repository: <path>`), or an operation on one (`open <path>`,
   `CreateFile <path>`, the path judged whole, so a gitdir named `<root> main\.git` or a single-segment
   `/repo.git` is outside), a whitespace token that is an absolute path outside the project, or a
   withheld path's whole spelling at a path boundary (`<root>/private/deny.txt` included). The root is
   held together first (item 8), as in a summary, so in a root with a space its first piece is not
   read as a path outside the project; a root that no sanitized text spells exactly (a Unicode space,
   a tab, a run of spaces) is not held, since its folded spelling would match a sibling, and the reason
   fails closed (`TestBuild_ARootNoTextSpellsExactlyIsNeverHeld`). The screen withholds, so under
   the two-fold rule (wave 19h's verify) a reason is judged with the root held under each reading,
   its own spelling with an ASCII letter's case folded and its spelling with case folded by Unicode
   (item 8), and is redacted when either judgement withholds it: `(C:\q\Åsa\proj\private\deny.txt)`
   names the withheld private/deny.txt of a root `C:\q\åsa\proj`
   (`TestBuild_ADropReasonNamingAWithheldPathUnderAUnicodeCaseSpellingOfTheRootIsRedacted`). A
   reason that glues a path outside the project to the text around it (`path=/q/other/x`, `(…)`)
   and names no recorded withheld path is shown, under any root and on every platform, as it was
   before: the screen reads an outside path as a part of the chain, an operation's path or a
   whitespace token. A bare basename or a rule literal
   does not redact a reason: an allowed `README.md` or `src/CLAUDE.md` beside a withheld
   `private/README.md` or `~/.claude/CLAUDE.md` keeps its restore clause. An approximate count
   (`~36800 tokens`) is not a home path. A reason is read as an error chain joined by `": "`, each
   part as clauses joined by `"; "`: the clause that shows the path becomes `(path withheld)`, the
   clauses before it stay, and those after it stay while they hold no separator. *Why the reason
   screen still reads an error chain.* D63 asked for whitespace tokens; but a Go or Windows error
   names its path whole after its operation
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
    its file pointers, its path-keyed checkpoint drops (while a Read rule is in force, at most 64 of
    them that need a fresh judgement: item 12), its structured summaries (one path each: a
    one-word value, a pattern one-word whole, the directory of a Glob preview under the root, a rooted
    summary's path part, the one path-named JSON value; for a cut one, the directory it spells whole),
    the instruction and skill files items 6a and 6b would restore, and, while a rule anchored outside
    the project is in force, `rootProbe` (item 7(d)); never for free text, whatever its commands,
    queries, URLs, quotes or glued operators say, never for a brace list's alternatives, and never for
    a value among several path-named values, cut or not. Under a root with no unit (item 8, D64(1)) a
    Glob preview of the root and a rooted summary's path part are free text, and a one-word value that
    spells the root is withheld by the whitelist before the host is asked, so such a root costs fewer
    judgements, never more (the corpus's 48 judgements under UAT-12's rules are 39 under `o'brien` and
    under `John @Work`). Through the real adapter, 80 Bash previews of
    17 words, 80 canonical-JSON and URL previews (a bare URL summary included), 80 path-named JSON
    arrays of six values each, and 80 arrays of twelve values that the store cut inside a value cost
    exactly the build's 10 file pointers and 10 structured summaries, 20 judgements; 80 commands run
    from the root (`<root>/tools/lintN.ps1 --since HEAD~N && echo ok`) cost 20 + 80, each judging the
    script's path alone; and a project with 5 `paths:` rule files and 5 skills costs 20 + 2 × 5, each
    rule file and each skill file judged once (the row checks the judged rule and skill files as one
    set, each once in the spelling items 6a and 6b hand the judge, so a build that dropped item 6b's
    judgements and judged item 6a's twice, which meets the count, fails it). A judgement is
    `RuleSet.Evaluate` on the path and, when the root resolves elsewhere, on the resolved root's
    spelling of each distinct reading of the path's place below the root (`rootRelatives`:
    `RootRelative`'s, `RootRelativeBroad`'s and `filepath.Rel`'s, two distinct at most and one for a
    path every reading places alike; a refusal withholds, so under the two-fold rule each is judged,
    wave 19h's verify), each of which may read the disk; so a build costs at most three Evaluates per
    path so judged. `internal/hostperm` is byte for byte its a357d187
    code plus one read-only accessor, `RuleSet.ReadRulePatterns`, which hands the screen the rules'
    specifiers (item 7(c)); hostperm is security-critical, and the round-2 `hostperm.Evaluator` is
    reverted with its rows, nothing it bought still needed.
11. *Size* (the w19d review's simplicity lens). D63 asked that the shell-quoting state machines,
    `stripQuotes`, `protect`'s quote-run logic, `regexLike`, `outsideIn`'s heuristics, `homeOrVarPath`
    and the Unicode boundary guesses be deleted, and they are; but `internal/rehydrate/pathgate.go` is
    not smaller than at e65ada8f. Measured with the pinned gocyclo v0.6.0 and gocognit v1.2.0: lines
    2059 (e65ada8f), 1949 (f708c693), 2410 after the round-2 fixes (f3196046), 2580 after the final
    verify's (f2171654), 2658 after D64's (ee280d3a), 2687 after its round-2 fixes (895d41f4), 2766
    after its rulings on wave 19f's open items; non-blank non-comment lines 1401, 1346, 1639, 1737,
    1767, 1776, 1809; functions 91, 87, 105, 112, 115, 116, 118; gocyclo total 646, 572, 722, 765,
    784, 788, 797, maximum 26 throughout (`jsonStrings`); gocognit total 590, 563, 691, 732, 741,
    744, 756, maximum 38 throughout. The whitelist's
    completeness checks
    (path starts, `..` edges, both backslash readings, quoted runs, glob containment) replaced the
    state machines roughly line for line, and the extensions that recover everyday summaries (item
    7(a)) each add a small, separately argued rule; the structured half, the noting and the error-chain
    reader are kept by D63. No function holds a quoting state machine.
12. *Wave 22: audit 2's rehydrate findings* (coordinator decisions D66 and D67). Each is closed as
    a class, its row red on eca33155 first.
    - *A path-named value holding several paths* (finding 26): item 6. On the w19d corpus of 274
      previews nothing flips, on Windows or Linux, in any of its ten scenarios, before or after
      wave 22's verify round.
    - *The drops' host judgements are bounded* (finding 28). Every path-keyed checkpoint drop cost
      one host judgement, uncached on disk, and their number grows with the session: the checkpointer
      keeps every touched file as a pointer and its budget cut names each pointer it cuts, so 1000
      drops cost 1020 judgements and about 2.5 s through the real adapter on Windows, half the
      compaction answer's budget. While a Read rule's pattern is in force, the judge asks the host
      about at most 64 fresh drop paths (`maxDropJudgements`; a file pointer's path among them was
      judged already and costs nothing): first each drop a tool summary or a drop reason names by its
      basename or its path, then the rest in the order the checkpoint lists them (`dropOrder`,
      `textNames`). It answers every later fresh path as refused without asking, an answer kept for
      the drops alone (`unjudged`), so section 7 and `dropped()` withhold it (`dropWithheld`), and
      learns it as withheld like any refused path, whatever its spelling, so every text that names it
      is withheld (fail closed). That answer never enters the memo of the host's answers (`judged`):
      wave 22's verify, fix round 2, found the first version memoizing it there, where item 6a's
      rule files and item 6b's skill files, judged by the very project-relative keys a file
      pointer's drop carries, read it, so a nested CLAUDE.md, a `.claude/rules` file or a SKILL.md
      whose own pointer the checkpoint's budget cut past the bound, under any Read rule, was neither
      restored nor indexed, and no drop entry named it; eca33155 restored both. Every judgement
      other than a drop's, which the project's configuration or the checkpoint's budget bounds and
      not the drops, asks the host
      (`TestBuild_ARuleAndASkillWhoseDropsLiePastTheBoundAreStillRestored`, red on 290b04be at 70
      drops and green on eca33155 but for its bound).
      Wave 22's verify found the first version, which learned such a drop only when its spelling held
      a rule's literal, showing a drop the host refuses only through a link, a junction or an 8.3 name
      (`lnk/token.txt`, `lnk` a link to `secrets`, under `Read(./secrets/**)`) wherever a free text, a
      selector, a glob, a cut summary or a reason named it after 64 fresh drops, where eca33155, which
      judged every drop, withheld them all; and it withheld in section 7 a drop past the bound that a
      summary named, which eca33155 showed. Judging the named drops first keeps both answers
      eca33155's in such a session (`TestBuild_ADropPastTheJudgementBoundIsWithheldWhereverItIsNamed`
      passes there but for its bound); the over-withholding left is item 2's limit. Learning a
      thousand paths put a thousand names before every summary, reading and reason: the screen reads
      the learned paths through one index once learning ends (`learnedIndex`, a byte trie for the
      names and the reasons' paths, and for selectors and globs the keys between NUL bytes, a glob
      asking only the keys that hold its literal), whose every answer is the per-path loop's
      (`TestLearnedIndex_AnswersAsTheLoopsDo`, `TestLearnedIndex_AJudgeAnswersAsItsListsDo`), and
      `appendDistinct` stops scanning a list past 64 entries, which the index holds once each. With
      no Read rule in force the host's rules are empty and read no file (hostperm's
      `RuleSet.Empty`), and every drop is asked as before. The cap lives in `hostRefuses` while the
      judge reads the drops, so any judgement made on a drop's behalf counts. The same 1000 drops
      now cost 84 judgements in about 0.11 to 0.16 s
      (`TestBuild_PathKeyedCheckpointDropsCostABoundedNumberOfHostJudgements`, the drops variant of
      `TestRehydrateHostPaths_HostJudgementsAreStructuredSummariesAndFilePointers`). The file
      pointers and structured summaries the build judges are bounded by the checkpoint's own budget,
      and the instruction and skill files by the project's configuration.
    - *Build's CPU* (findings 33, 34 and 35). The judge keeps a per-build memo, shared by its copies
      as `judged` is: the held root keyed by the root's expression (one for each reading of the root,
      so each fold has its own entries) and the text, each distinct summary's verdict, and each
      distinct drop reason's screen, the last two made only once the judge stops learning withheld
      paths. A text without the root's last segment, folded as the expression folds it (an ASCII
      letter's case where paths fold, no other character's), skips the expression, and a text
      without `path:` in any ASCII case skips recall's selector expression (no other character folds
      onto those letters under RE2's `(?i)`). Build still gates the drop list at steps 9a and 10,
      since min-fill re-admits item 2's units between them; the second pass is memo hits.
      `BenchmarkBuild` is internal/rehydrate's first benchmark: 200 tool pointers in eight preview
      shapes, 50 file pointers and 0, 600 or 1000 drops, with no rules and with UAT-12's; beside
      ns/op it reports each build's p50 and p99. No verdict changes
      (`TestBuild_TheScreensMemoNeverChangesAnAnswer`). Wave 22's verify measured candidate 7
      (a357d187, the same fixture overlaid) and found the no-rules build still about twice its
      cost with no drops. The screen built a glob replacer per call, decoded every JSON string,
      copied every token byte by byte, listed every path start, split every token at operators it
      did not hold, and ran its anchored expressions (`homeOrVarRoot`, `driveSpelling`, `psSplat`)
      on every word; each is now built once, skipped when there is nothing to decode or split,
      sliced, visited or guarded by its first byte, each pinned as pure speed
      (`TestHotPathGuards_ChangeNoAnswer`, `TestOperatorPieces_TheShortcutChangesNoAnswer`,
      `TestSplitTokens_SlicingChangesNoAnswer`). The no-drop no-rules build allocates 13.1k
      objects (1.10 MB) where ec6e3ccd allocated 21.1k (1.42 MB) and candidate 7 7.6k (0.91 MB).
      Measured p50 at GOMAXPROCS 2 on a co-loaded Windows host, 12 interleaved rounds, against
      candidate 7: 1.69x with no drops (ec6e3ccd 1.95x), 1.39x with 600 (1.56x) and 1.47x with
      1000 (1.56x); with every CPU, 1.2x to 1.5x with no drops. The rest of the gap is the D63
      whitelist's own work on every distinct summary, which candidate 7 did not do; it is a
      known minor under D66(d), about 2 ms a build, far inside the compaction answer's 5 s budget.
      Against eca33155, the base wave 22 started from, `BenchmarkBuild` at GOMAXPROCS 2 (30
      builds a round, the median of six interleaved rounds, p99 the slowest of a round's 30)
      measured on 290b04be by wave 22's verify, p50 then p99 in ms, eca33155 → HEAD: no rules
      with 0 drops 6.18 → 3.18 and 8.90 → 4.98, 600 drops 15.02 → 3.88 and 21.45 → 11.78, 1000
      drops 22.76 → 6.46 and 33.80 → 11.80; UAT-12's rules with 0 drops 5.42 → 3.72 and 10.55 →
      5.90, 600 drops 15.68 → 4.83 and 27.30 → 7.92, 1000 drops 27.17 → 7.69 and 42.55 → 12.94.
      Fix round 2's tree (b20a40fc's code), measured the same way on a host co-loaded by the other
      seats, keeps that order at every size: p50 6.35 to 14.34 against 11.71 to 54.33, p99 15.32
      to 25.10 against 22.57 to 110.87. Build is cheaper than at eca33155 everywhere, and its p99
      at 1000 drops stays near 25 ms under that load.
    - *Qompack's own slash commands in a reason* (finding 27). The reason screen read every word led
      by `/` as an absolute path, so the checkpointer's `run /qompack:pin --list: <err>` became
      `(path withheld): <err>`. A word that is `/qompack:` and a command name is read without its
      slash; every other word led by `/` is still judged as a path
      (`TestBuild_AQompackCommandInADropReasonIsNoPath`).
    - *Section 6 explains a withheld pointer once* (finding 30). Each withheld line repeated a
      97-character explanation (a withheld file's label about 84), charged to the payload's fixed
      character ceiling. A section 6 built from a withheld pointer now carries one legend line under
      its heading, priced with the heading in both dimensions; a withheld summary reads `(summary
      withheld)` and a withheld file `file (path withheld)`. A drop entry keeps the full note, since
      `dropped()` returns it without the legend
      (`TestBuild_AWithheldPointerIsExplainedOnceInItsSection`).
    - *Rulings applied* (D67(l) and (m)): `select` is an inert prefix (finding 31); the lone-glob
      relaxation and 19d's stricter extensions are ratified, and the exact-path over-withholding of
      item 6 is kept (finding 68); the ANSI default character (findings 42 and 68) and a
      case-sensitive APFS volume (finding 85) are limits in item 2's list; the best-fit modifier
      letters' bullet there records D64's closing of it (findings 42 and 69).

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
`TestBuild_AnApostropheInTheRootHoldsNoRootUnit` (item 8);
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
`TestBuild_ADotDotRangeAndAGoPatternNeverClimb`. The D64 rows:
`TestBuild_ARootOutsideTheWhitelistHoldsNoRootUnit` and `TestBuild_AReasonHoldsTheRootASummaryDoesNot`
(item 8), `TestBuild_ARootNoTextSpellsExactlyIsNeverHeld` (items 8 and 9, the round-2 verify),
`TestBuild_ACutRightAfterAProviderDriveColonIsWithheld` (item 9),
`TestBuild_ABareDriveNameNamesADriveRoot` (item 7(b)) and `TestBuild_APathOrNameAfterAPlusIsJudged`
(item 7(b) and (c)). The rows of D64's rulings on wave 19f's open items:
`TestBuild_ACutValueIsTheRootOnlyInItsOwnSpelling` (item 9),
`TestBuild_ABestFitCharacterIsOutsideTheWhitelist` and, on Windows,
`TestWhitelist_NoANSIBestFitToPunctuationIsSafe` (item 7(a)), `TestBuild_ABestFitRootHoldsNoRootUnit`,
`TestBuild_ARootWithADashWordHoldsNoRootUnit` and `TestBuild_AWithheldPathIsLearnedUnderEveryRoot`
(item 8). The rows of wave 19g's final verify:
`TestBuild_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt` and
`TestBuild_AUnicodeCaseVariantOfTheRootIsADirectoryBesideItForPointersAndReasons` (items 6, 8 and
9), and `TestBuild_ARuleOverTheRootIsMatchedAsTheHostMatchesIt` (item 7(d)). The rows of wave 19h's
verify: `TestBuild_AWithheldFileUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames`,
`TestBuild_AWithheldToolSummaryUnderAUnicodeCaseSpellingOfASpacedRootTeachesItsNames`,
`TestBuild_AShortWithheldNameUnderAUnicodeCaseSpellingOfTheRootIsLearnedByItsPath` (items 6 and 8)
and `TestBuild_ADropReasonNamingAWithheldPathUnderAUnicodeCaseSpellingOfTheRootIsRedacted` (item 9).
`internal/daemon`, through
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
`TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit`,
`TestRehydrateHostPaths_ABestFitOrDashRootHoldsNoRootUnit`,
`TestRehydrateHostPaths_ACutValueIsTheRootOnlyInItsOwnSpelling` (the store's own cut),
`TestRehydrateHostPaths_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt` (real sibling
directories), `TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames`
(real folders, NTFS folding U+00C5 onto the project's own),
`TestRehydrateHostPaths_ARuleOverTheRootIsMatchedAsTheHostMatchesIt`,
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
`TestBuild_AnApostropheInTheRootHoldsNoRootUnit` and their `internal/daemon` twins withhold a glob in
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
`TestBuild_AProjectPathWithASpaceShowsItsOwnAbsolutePaths`, `TestBuild_AnApostropheInTheRootHoldsNoRootUnit`
and the `internal/daemon` twins `TestRehydrateHostPaths_TheProjectRootFollowedByMoreWordsIsShown` and
`TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit`; a `&&` glued to a word naming an allowed
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
in a Linux container alike; the glob, brace, operator, parentheses, comment and JSON-key audit rows
fail there only on their PowerShell-drive and `#` spellings (`<root>\src Temp:*`, `<root>\src
#/etc/*`, `{Temp:,x}*`, `a|Temp:secret.txt`, `a||#/etc/passwd`, `cat "(Temp:secret.txt)"`,
`#~/.ssh/id_rsa`, `{"Temp:secret.txt":true}`) and hold on every spelling their own extension admits,
and the null-device, root-before-a-pipe and `..` rows pass there. On the w19d corpus of 274 previews
(seven scenarios) f3196046 shows 196 and withholds 78 under UAT-12's rules in the Windows build, with
0 leaks and 50 over-withheld, and 195 and 79, with 0 leaks and 51 over-withheld, in the Linux build;
the final fixes show 193 and withhold 81 (0 leaks, 53 over-withheld) on Windows and 192 and 82 (0
leaks, 54 over-withheld) on Linux, the three flips on both being `git log --pretty=format:%h -n 3`,
`sleep 5 && curl localhost:3000` and `{"skill":"superpowers:brainstorming"}`.

*Criterion changes (D64, the final verify of wave 19e).* No row that asserted a WITHHELD spelling
changed. D64(4) accepts the three flips just above and the Windows either-slash root. Under D64(1) a
root with an apostrophe has no unit, so the rows the w19c round-2 review added to show summaries in
an `o'brien` or `John's projects` root flip from SHOWN to WITHHELD and are renamed for what they now
pin: `TestBuild_AnApostropheInTheRootIsNotAnOpenQuote` is
`TestBuild_AnApostropheInTheRootHoldsNoRootUnit` (its seven shown rows, the root's Grep and Glob
previews, `cd <root> && …` in both slash styles and `git -C <root> …` unquoted and quoted, are
withheld beside its six withheld ones), and its `internal/daemon` twin
`TestRehydrateHostPaths_AnApostropheInTheRootIsNotAnOpenQuote` is
`TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit`, which withholds the root's Grep and
Glob previews, `cd <root> && …`, `git -C <root> …` and a one-word Read under roots holding `'`, `;`,
`,`, `$` and `+`, shows a path-named JSON value under them, and kept a root of letters, digits, `@`,
`-` and `.` shown until the round-2 verify (next paragraph); earlier criterion-change paragraphs and
the evidence list name both rows by their new names. The fixture `shortProjectDir` of
`internal/daemon` now spells its temporary base by its long names on Windows
(`filepath.EvalSymlinks`): a hosted Windows runner's temporary directory is spelled with an 8.3
name (`C:\Users\RUNNER~1\AppData\Local\Temp`), whose `~` would leave every root under it without a
unit; with `TMP` and `TEMP` set to an 8.3 spelling, ten `internal/daemon` rehydrate rows fail
on the D64 gate without that change, and all pass with it. No golden changed. Red-first: with
pathgate.go of f2171654 overlaid on the new tests, `TestBuild_ARootOutsideTheWhitelistHoldsNoRootUnit`
fails on every excluded root (19 on Windows; 27 in a Linux container, where `" | < > * ?`, a `:`, a
backslash and a tab may stand in a name too), each on a summary that spells the root shown under the
unit, or for a Unicode space on its ASCII-space sibling, and passes on the four admitted ones;
`TestBuild_AnApostropheInTheRootHoldsNoRootUnit` and
`TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit` fail there on each excluded root, on
Windows and in the container alike; `TestBuild_ACutRightAfterAProviderDriveColonIsWithheld` fails on
all twelve of its drive and provider spellings, while its `C:…` row and its inert controls pass;
`TestBuild_APathOrNameAfterAPlusIsJudged` fails on all nine of its withheld spellings, its four
shown ones passing; `TestBuild_AReasonHoldsTheRootASummaryDoesNot` and
`TestBuild_ABareDriveNameNamesADriveRoot` pass there, as pins of what D64 leaves as it is. A `+`
starting a path over-withholds a `c++/` directory (`ls include/c++/v1`) and flips no earlier row. On
the w19d corpus nothing flips: f2171654 and the D64 fixes (323ffe65, b9505d53) both show 193 and
withhold 81 under UAT-12's rules in the Windows build (0 leaks, 53
over-withheld), and 192 and 82 (0 leaks, 54 over-withheld) in the Linux build, since every corpus
root keeps its unit. Run under a root of `o'brien` or of `a,b`, the same corpus shows 149 and
withholds 125 after the fix (0 leaks, 97 over-withheld), against f2171654's 193 and 81: the 44 flips
are D64(1)'s accepted cost in such a root.

*Criterion changes (the round-2 verify of D64).* No row that asserted a WITHHELD spelling changed,
and no golden changed. `@` leaves the root unit's set and a root that no sanitized text spells
exactly (`rootSpelledExactly`: a Unicode space, a tab or another control character, a run of spaces,
a space at either end) loses its unit and is held in no drop reason (item 8). In
`TestBuild_ARootOutsideTheWhitelistHoldsNoRootUnit` the admitted root `a@b` moves to the excluded
roots, so its six summaries flip from SHOWN to WITHHELD, and `John @Work` and `a  b` join the excluded
roots; the two spellings it withheld under each admitted root (`cd <root> && cat private/deny.txt`
and `<root>2/x.txt`) are now withheld under every excluded root too, so `a@b` keeps them; and the
sibling it withholds is the root's spelling with each run of whitespace folded to one space, as
`sanitize` folds it (it was each whitespace character mapped to one space). In
`TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit` the control root `a@b-c.d` moves to
the excluded roots, so its five summaries (the root's Grep and Glob previews, `cd <root> && go test
./...`, `git -C <root> status --short` and a one-word Read under it) flip from SHOWN to WITHHELD,
`John @Work` joins them, and `a-b_c.d` is the new control root, shown. The new row
`TestBuild_ARootNoTextSpellsExactlyIsNeverHeld` withholds every summary that spells such a root or
its one-space sibling, and redacts a git drop reason naming the sibling's gitdir, a rule-scan reason
naming the sibling's file, and, failing closed, a reason naming the root's own `.git\index`, which
an exactly spelled root keeps (`TestBuild_AReasonHoldsTheRootASummaryDoesNot`): that redaction of a
project path under such a root is the fix's accepted cost. `recordedPath` keeps holding the root
whatever its spelling: the verifier suggested gating it too, for consistency, but it decides only
whether a withheld path's names are learned, and learning withholds more, so the gate would leave a
withheld path under a root `a  b` unlearned (its space survives) and weaken the screen. Red-first: on
ee280d3a, `TestBuild_ARootOutsideTheWhitelistHoldsNoRootUnit` fails on `a@b`, `John @Work` and `a  b`
(each on a summary spelling the root shown under the unit) and passes on its 19 earlier excluded roots
(27 in a Linux container) and its three admitted ones; `TestBuild_ARootNoTextSpellsExactlyIsNeverHeld`
fails on every root, three on Windows (`a<U+00A0>b`, `a<U+3000>b`, `a  b`) and five in the container
(with `a<TAB>b` and `proj `), the Unicode-space and tab roots on the reason that shows the sibling's
gitdir and the others on a summary that spells the root, shown under the unit; with only the reason
gate reverted (`rootSpelledExactly` forced true in `reasonWithheld`) it fails on every root on the
reason, on both platforms; `TestRehydrateHostPaths_ARootOutsideTheWhitelistHoldsNoRootUnit` fails on
`a@b-c.d` and `John @Work` and passes on the rest, on both;
`TestBuild_AReasonHoldsTheRootASummaryDoesNot` passes there, a pin of what the round leaves as it
is. On the w19d corpus nothing flips: f2171654,
ee280d3a and the round-2 fixes (66d243a5, 1a89183c) all show 193 and withhold 81 under UAT-12's
rules in the Windows build (0 leaks, 53 over-withheld), and 192 and 82 (0 leaks, 54 over-withheld)
in the Linux build. Run under a root of `John @Work`, the same corpus shows 149 and withholds 125
after the fix (0 leaks, 97 over-withheld), against ee280d3a's 193 and 81 on Windows (44 flips) and
192 and 82 on Linux (43), and under a root of `a  b` 149 and 125, against 166 and 108 (17 flips)
and 165 and 109 (16): every flip
is from SHOWN to WITHHELD and names nothing private, D64(1)'s accepted cost in such a root. A `-`
that starts a word of the root after its space (a root ending in ` -Force`) binds as a parameter of a
PowerShell cmdlet or advanced function (`g C:\q\John -Force` sets `g`'s `-Force`, measured with
PowerShell 7 and 5.1), while a lone `-` (`OneDrive - Contoso`, the folder OneDrive for Business
creates) is passed as text; D64 lists `-` among the unit's characters and did not ask for it to be
decided, so `-` stays in the set, and that shape was left open for a coordinator ruling, which D64
has since given (next paragraph).

*Criterion changes (D64's rulings on wave 19f's open items).* No row that asserted a WITHHELD
spelling changed, and no row that asserted a SHOWN one: `internal/rehydrate` and its
`rehydratetest` package, and the 80 earlier `internal/daemon` rehydrate rows, pass unchanged on
Windows (where one skips by platform) and in a Linux container run as an unprivileged user (run as
root, `TestService_StateWriteFailureStillEmits` writes through the 0500 directory it sets up, on
895d41f4 too). No golden changed. The behaviour changes with no earlier row: a cut
path-named value is the project only when it starts the root's own spelling byte for byte (item 9),
so a cut sibling that differs from the root by a quote, a caret, a backslash or a whitespace run is
withheld, one that differs by a non-ASCII case (the Kelvin sign for `k`) is withheld when the cut
falls inside the root's own spelling (cut past its end, containment's Unicode fold still showed it
until wave 19g's final verify, next paragraph), and so is a repeated separator, which names the
root (over-withheld);
a letter, mark or digit whose ANSI best fit is ASCII punctuation is unsafe in free text and leaves
the root unit (items 7(a) and 8), so a word holding one (the Hawaiian ʻokina U+02BB, the U+0300 of
a decomposed `è`) is over-withheld; and a root with a word that starts with `-` after a space has
no unit (item 8), `OneDrive - Contoso` among them, the ruling's stated cost. Red-first: on
895d41f4, with the new rows overlaid, `TestBuild_ACutValueIsTheRootOnlyInItsOwnSpelling` fails on
all four of its roots on Windows (`Johnathan`, `John Smith`, `o'brien`, `kate`) and on three in a
Linux container, where paths do not fold and the Kelvin row passes; a Unicode-fold mutant of the
fix (`paths.Key` on both sides) fails the Kelvin row on Windows.
`TestBuild_ABestFitCharacterIsOutsideTheWhitelist` and `TestBuild_ABestFitRootHoldsNoRootUnit` each
fail on 45 of their 88 code points, the block's 37 modifier letters and the 8 outside it, while the
block's modifier symbols and the letter controls pass. `TestBuild_ARootWithADashWordHoldsNoRootUnit`
fails on its five dash roots, and its three controls pass. The Windows measurement row fails at
U+01C0. The daemon twins fail on their three best-fit and two dash roots, with the control root
passing, and on the cut sibling. Each fix is separable (measured on Windows): with only the
whitelist's exclusion reverted the character row fails on its 45 and the root row passes; with
only the root unit's reverted the root row fails on its 45 and the character row passes, since the
unit hides the root's characters
from the whitelist; with only the dash condition reverted the dash row alone fails. The learning
row passes on 895d41f4, as a pin of the ratification. On the w19d corpus nothing flips: 895d41f4
and these fixes (c2a60db3, 28637640, b283dbb3) both show 193 and withhold 81 under UAT-12's rules in
the Windows build (0 leaks, 53 over-withheld), and 192 and 82 (0 leaks, 54 over-withheld) in the
Linux build. Run under a root of `OneDrive - Contoso`, `John -Force` or `aʼb` (U+02BC), the same
corpus shows 149 and withholds 125 after the fixes (0 leaks, 97 over-withheld), against 895d41f4's
193 and 81 on Windows (44 flips each) and 192 and 82 on Linux (43). Every flip is from SHOWN to
WITHHELD and names nothing private: D64's accepted cost in such a root.

*Criterion changes (wave 19g's final verify of D64).* No row that asserted a WITHHELD spelling
changed, and no row that asserted a SHOWN one: `internal/rehydrate` and its `rehydratetest`
package, and the 82 earlier `internal/daemon` rehydrate rows, pass unchanged on Windows (where
`TestService_StateWriteFailureStillEmits` skips by platform) and in a Linux container run as an
unprivileged user. No golden changed. One test helper changed shape with the code it reads:
`screenLiteral` takes `ruleSegments`' two results, the third (`fromStart`) having moved to
`hostReadings`. The behaviour changes with no earlier row: a path in a directory beside the root
spelled with the Kelvin sign, the long s or the Angstrom sign is outside the project in every
judgement on Windows, as it already was on Linux, and on macOS, where the root's spelling read it
as the root in free text, it is withheld too (over-withheld wherever the volume's own case folding
pairs it with the root); on macOS an absolute path spelled with the root in
another ASCII case is now the project in containment too, as the root's spelling and `rootPrefix`
already read it there (it was over-withheld; no macOS host ran it, and `go vet` for darwin passes);
the root spelled with another non-ASCII letter in another case that NTFS folds (`Åsa`, U+00C5, for a
root `åsa`) is no longer held where a summary is judged for showing, and such a summary is
over-withheld (that was not all: the same comparisons stopped the build from learning a withheld
path recorded under that spelling, or under the Kelvin sign's or the Angstrom sign's, and from
redacting a drop reason that glued one to its text; wave 19h's verify found both, next paragraph,
and this paragraph's "nothing flips" held only for a corpus whose recorded paths spell the root as
it is); and a rule anchored outside the project whose raw
segments match the root adds the literal of what follows it to the screens. Red-first: on
71e5133d, with the new rows overlaid, `TestBuild_AUnicodeCaseVariantOfTheRootIsADirectoryBesideIt`
fails on all three of its roots (`kate`, `sam`, `åsa`) on Windows, where a probe of the same builds
shows all 18 sibling summaries at 71e5133d and none after, and passes in a Linux container, where
paths do not fold;
`TestBuild_AUnicodeCaseVariantOfTheRootIsADirectoryBesideItForPointersAndReasons` fails on all three
on Windows, the sibling's file pointer shown by its path, and passes in the container;
`TestBuild_ARuleOverTheRootIsMatchedAsTheHostMatchesIt` fails on six of its seven subtests on
Windows and on five of its six in the container (the upper-case rule is a folding platform's
subtest), its Kelvin-sign rule passing on both, as a pin of the host's Unicode case rule. The daemon
twins, through the real `hostperm` and real NTFS sibling directories, fail on Windows on all three
roots and all three rule shapes, and in the container on the three rule shapes, the case row passing
there. Each fix is separable (measured on Windows): with only the root regex's `(?i)` restored the
summary row alone fails, on its three roots; with only containment's `filepath.Rel` restored the
summary row and the pointer-and-reason row fail and the rule row passes; with only `rootCover`'s
raw comparison removed the rule row alone fails, on five subtests (the escape shape is caught by the
raw split alone). On the w19d corpus nothing flips: 71e5133d and these fixes (c16b21d5,
28a9ea39) both show 193 and withhold 81 under UAT-12's rules in the Windows build (0 leaks, 53
over-withheld), 192 and 82 (0 leaks, 54 over-withheld) in the Linux build, and 149 and 125 (0
leaks, 97 over-withheld) under the `OneDrive - Contoso`, `John -Force` and `aʼb` roots in both,
with the same host-call counts.

*Criterion changes (wave 19h's verify of the root's ASCII-only fold).* No row that asserted a
WITHHELD spelling changed, and no row that asserted a SHOWN one: `internal/rehydrate` and its
`rehydratetest` package, and the 84 earlier `internal/daemon` rehydrate rows, pass unchanged on
Windows (where `TestService_StateWriteFailureStillEmits` skips by platform) and in a Linux container
run as an unprivileged user, wave 19g's final-verify rows and their 18 sibling summaries among them.
No golden changed. The two-fold rule (item 8) restores what c16b21d5 narrowed and adds no SHOWN
spelling: a withheld path recorded under the root spelled with another case of a non-ASCII letter
teaches its project-relative names wherever the host refuses it (`Åsa` U+00C5, the Kelvin sign and
the Angstrom sign, as on 71e5133d, and the dotted capital I U+0130 for `i`, which only the host's
lower-casing folds and 71e5133d never learned); under the long s, which the host does not fold, the
path is withheld as a directory beside the project and teaches only its own spelling and basename
where the root resolves to itself. Where the root resolves elsewhere (through a link or an 8.3
name, or below macOS's `/var`, a link to `/private/var`), the host adapter also judges each reading
of the path's place below the resolved root, and the broad reading (on Windows `filepath.Rel`'s too,
as on 71e5133d) places the long s's spelling at the project's file there, which the host refuses;
so it is refused and teaches its project-relative names too, over-withheld where the volume keeps
the long s's folder beside the root, as NTFS does, and the safe direction wherever its case folding
pairs the two;
and a drop reason that names a withheld path through any such spelling is redacted. The host adapter
judges the resolved root's spelling of each reading of a path's place below the root, at most three
Evaluates per path (item 10). Red-first: on eca33155, with the new rows overlaid,
`TestBuild_AWithheldFileUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames`,
`TestBuild_AWithheldToolSummaryUnderAUnicodeCaseSpellingOfASpacedRootTeachesItsNames` and
`TestBuild_AShortWithheldNameUnderAUnicodeCaseSpellingOfTheRootIsLearnedByItsPath` each fail on
Windows on the four spellings the host folds (U+00C5, U+212A, U+212B, U+0130), their long-s subtest
passing as a pin of the host's own fold, and
`TestBuild_ADropReasonNamingAWithheldPathUnderAUnicodeCaseSpellingOfTheRootIsRedacted` fails on all
five; on 71e5133d all four pass but for their U+0130 subtests and, in the file-pointer and
short-name rows, the long s, whose sibling 71e5133d read as the project and showed by its path (wave
19g's final verify). In a Linux container, where paths do not fold, the learning rows pass on all
three revisions (the recorded spelling is another directory, which the host does not refuse) and the
reason row skips: a reason that glues a path outside the project to its text is shown there, as
under any root on any platform since D63 (item 9). The daemon twin
`TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames`, through the
real `hostperm` and real folders (NTFS folding U+00C5 onto the project's own and keeping the other
four apart) under a root that resolves to itself (next paragraph), fails on eca33155 on Windows on
all three shapes under the four spellings the host folds, passes on 71e5133d but for U+0130 and the
long s's two file-pointer shapes, and passes in the container on all three revisions. Each fix is separable (measured on Windows): without `note`'s
broad learning the three learning rows fail on the four host-folded spellings; without
`recordedPath`'s broad hold the tool-summary row alone fails, on the same four; without the reason
screen's broad pass the reason row alone fails, on all five; and without the host's refusal as the
gate on broad learning the three learning rows fail on the long s alone, which they then
over-withhold. `globSelectsKnown`'s broad reading and the adapter's are fail-safe by construction
and no row tells them apart: every caller of the first has already withheld a glob the strict
reading puts outside the project, and the second judges, on Windows, what `filepath.Rel` judged on
71e5133d (a probe of 144 judgements under a root handed in its long and its 8.3 spelling, so that it
resolves elsewhere, gives the same answers on 71e5133d, eca33155 and these fixes). The sweep
classified every caller of `RootRelative`, `inside()`, `holdRoot`, `rootSpellingOf`, `rootPrefix`,
`asciiFoldEqual`, `foldLiteral` and the adapter: every judgement that shows keeps the strict
reading, and none of c16b21d5's narrowing reduced a withhold but the three above and, on macOS
alone, the adapter's judgement of `filepath.Rel`'s reading, which it judges again. On macOS the
adapter's broad reading is new, since `filepath.Rel` folds nothing there and no earlier revision
judged a Unicode case spelling of the root below the resolved root: under a root that resolves
elsewhere it refuses such a spelling wherever the host refuses the project's file, the long s's
among them, which only withholds more. On the w19d
corpus nothing flips across 71e5133d, eca33155 and these fixes: 193 shown and 81 withheld under
UAT-12's rules in the Windows build (0 leaks, 53 over-withheld), 192 and 82 (0 leaks, 54
over-withheld) in the Linux build, 149 and 125 (0 leaks, 97 over-withheld) under the three unit-less
roots in both, with the same host-call counts. Spelled under the U+00C5, Kelvin-sign, long-s,
Angstrom-sign and U+0130 variants of its root (fifteen more scenarios of 274 previews each: the
summaries, the denied files' pointers, and every pointer, spelled under the variant), no preview is
SHOWN after these fixes that 71e5133d or eca33155 withheld, in either build; 71e5133d's 44 sibling
leaks under each of the Kelvin sign, the long s and the Angstrom sign stay closed, and the counts
equal eca33155's, the host-call counts returning to 71e5133d's where a recorded path is in the
project under the broad reading. On a probe of 15 drop reasons in 24 scenarios, eca33155 showed 56
that 71e5133d redacted (seven glued shapes naming a withheld file under the U+00C5, Kelvin-sign,
long-s and Angstrom-sign spellings, plain and spaced); these fixes show none that either redacted.

*Criterion changes (wave 19i's verify of the two-fold rule).* No product behaviour changed, and no
row's assertion. The daemon twin's long-s subtests expect the recorded spelling not to be refused and
their globs to be shown, which holds only while the root resolves to itself (previous paragraph),
and the twin's fixture held that only on Windows, where `shortProjectDir` spells the temporary
directory by its long names, and on Linux. macOS spells it below `/var`, a link to `/private/var`,
so there the adapter's broad reading refused the long s's spelling and the three long-s subtests
failed on the fixture's own check of the host's answer. They would have passed on 71e5133d and
eca33155, whose macOS adapter folded no Unicode case, so the fixes above would have turned the
macOS whole-tree job red. No macOS host ran it: the verify found it by reading the adapter, and two
probes reproduce it. On Windows, with the twin's base handed through its own 8.3 spelling so that
the root resolves elsewhere, 713cb8f4's twin fails those three subtests at that check, and the other
twelve pass. In a Linux container with `paths.DefaultFold` and `hostperm`'s fold forced on (macOS's
reading: case folded, `filepath.Rel` folding nothing) and `TMPDIR` reached through a symbolic link,
the same three fail on 713cb8f4 and pass with 71e5133d's and eca33155's adapter and screen, where
the other twelve fail on both: on eca33155 as on Windows, and on 71e5133d because its containment
read `filepath.Rel`, which folds nothing on macOS, so it never learned under those spellings there
either. The twin's root is now canonical on every platform (`filepath.EvalSymlinks`, as
`costProject`'s is), and it passes 15 of 15 under both probes and unprobed on Windows and in the
container. Its red-first map is unchanged: on Windows it fails 12 of 15 on eca33155 and 5 of 15 on
71e5133d, and in the container it passes on all three revisions. Under the macOS reading it fails 12
of 15 on each of 71e5133d and eca33155, the long s's three passing. The long-s sentence in the
previous paragraph, the twin's comment, the rehydrate rows' comment and `rootRelatives`' comment
now say where the long s's answer holds.

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
