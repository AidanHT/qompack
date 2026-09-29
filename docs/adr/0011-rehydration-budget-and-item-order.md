# ADR 0011 — Rehydration budget, item order, and whole-rule restoration

- **Status:** accepted; amended 2026-09-22 by §21 (owner decision D5: the payload fits the host's
  10,000-character additionalContext cap)
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
   newest 64 entries and naming how many earlier ones it left out.
3. *A fork keeps its parent's original.* A session started with `--fork-session` has a new id,
   so `prompt_<fork>_0` is the fork's own first prompt, and L0-wins overrode the parent's original
   with it (F-UAT06-1). The daemon records the fork's lineage when it starts
   (`state/lineage-<session>.json`, the project's newest checkpoint being the one it continues);
   the checkpointer inherits the parent's intent from it, and item 2 verifies the original against
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
already was — the floor exists so that an omission can be named next to what did fit.

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
