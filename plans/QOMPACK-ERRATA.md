# `Qompack.md` verification record

**Current revision:** v1.5, 2026-09-06, deliberately authorized by the user for Markdown-only planning. The v1.3 record below is preserved verbatim history, not current endorsement of its arithmetic, internal constants or compatibility conclusions. See the appended v1.5 record for superseded claims.

`Qompack.md` is **read-only** (`plans/README.md`, Global rules). It carries a **Revision log**, so it
is revisable — but only deliberately, as a versioned revision, never edited in passing by a subplan.
It has been revised twice before: v1.1 (full-plan review) and v1.2 (latency made an explicit
objective).

**v1.3 landed 2026-08-23** and this file is its record: what was checked, what held, what moved, what
could not be verified, and against which sources. The read-only rule is back in force. The next
revision appends to this file rather than replacing it.

**v1.4 landed 2026-08-26**, appended below: a single §8.6 sentence widened, raised by SP-11 when the
design of record and the shipped conformance suite were found to disagree about it.

**v1.5 landed on the combined wave-3 baseline (SP-19 M0-01)**, appended after it: the 2026-09-06 planning-only
revision and its merge-first addendum, renumbered from their draft label "v1.4" (see the numbering note in that section).

**Why a record at all, when the document itself has a revision log.** The log says what changed. This
says what was *checked and did not change*, which is the more perishable half — without it the next
auditor cannot tell a claim that was verified last year from one that has never been looked at, and
re-derives everything or trusts everything. Both are worse than reading a list.

---

## v1.3 — the §5.1 cache-cost verification

### Origin

§5.1 has always carried a standing instruction to its own reader:

> "Standard documented multipliers are `r = 0.1`, `w = 1.25` — **verify against current pricing
> before tuning**, since the ratio drives several thresholds below."

Nothing had ever executed it. Everything below follows from doing so.

### Confirmed — checked, held, unchanged

These are listed because "we checked and it is fine" is a result. Without this table the next audit
re-derives them.

| Section | Claim | How it was confirmed |
|---|---|---|
| §2.5 | `effectiveContextWindow = contextWindow − min(maxOutputTokens, 20 000)`; `autoCompactThreshold = effectiveContextWindow − 13 000` | **Reproduces a published figure exactly.** Sonnet 5 at a 1M window is documented to auto-compact "at about **967K** tokens by default". `1 000 000 − 20 000 − 13 000 = 967 000`. Two independently-derived constants landing on a published number is confirmation, not coincidence |
| §2.7 | The survives-compaction table | Seven of eight rows match the published table in substance, several word-for-word. §2.7's "truncated head-first" is confirmed by *"Truncation keeps the start of the file"*. The eighth row is unverifiable — see below |
| §2.8 | The `compact_20260112` Messages API beta and `pause_after_compaction` | Still current, under `context_management.edits`, with the documented pause-and-return semantics |
| §5.1 | `r = 0.1` | *"Cache read tokens are 0.1 times the base input tokens price"* |
| §5.2 | The `p_min` argument and the A/B table | Analysis over the prefix-match rule, which is restated verbatim in current docs: *"The match is exact, so a change anywhere in the prefix recomputes everything after it. There is no per-file or per-segment caching"* |
| §5.4 | The TTL slides — it refreshes on use | *"The cache is refreshed for no additional cost each time the cached content is used."* |
| §5.5 | The cache-compatibility audit | Method-by-method analysis over the prefix rule; no price dependency |
| §5.6 | Breakpoint placement is not plugin-actionable | Confirmed twice: Claude Code manages its own `cache_control` markers, and a plugin's `settings.json` is limited to the `agent` and `subagentStatusLine` keys — it cannot install a main `statusLine` either |
| §7.3 | The six-hook surface | All six still exist. The surface has grown a great deal since (`PostCompact`, `PostToolUseFailure`, `PostToolBatch`, `SubagentStart` and others), but nothing Qompack relies on was removed or re-scoped |

### Changed

Eight items. Each was a number the document was written to expect to move, or a consequence it had
drawn only half of. **None contradicted its reasoning.** Full detail in the v1.3 revision-log entry;
the engineering record, with the arithmetic, is `V2-report.md` §19.

| # | Section | Change |
|---|---|---|
| 1 | §5.1, §5.6, App. A | `w` is a **pair**: 1.25 at the 5-minute TTL, **2.0** at the 1-hour. `w/r` is 12.5 **or 20** |
| 2 | §5.4, §8.4 | The TTL is a **regime the session does not announce** — 1 hour automatically on a Claude subscription, 5 minutes on API-key and third-party auth, 5 minutes for subagents regardless. Under uncertainty assume the longer TTL and dearer `w`; the two errors are not symmetric |
| 3 | §5.4, §8.4 | The TTL clock starts at the **request**, not the response; generation counts against it |
| 4 | §5.3 | Compaction is **itself a priced request** — the objective gains `c·n`, `c = r` warm and `1` cold. `p`-independent, so the argmax is unchanged |
| 5 | §5.4, §8.4 | **The expiring band is a trigger**, not just a decay factor — the consequence of #4, and the half of "schedule against cache state" v1.1 did not draw. Worth `(1 − r)·n` per idle-driven compaction |
| 6 | §5.4, §5.5, §12 | **Time is not the only way a prefix dies**: model, effort and the fast-mode header are all part of the cache key. Only effort is observable from a plugin |
| 7 | §2.5, §8.4 | Four further window variables, and the per-model default framing. The arithmetic itself is confirmed |
| 8 | §2.7, §5.3, §8.4 | The summarization request **inherits extended thinking** (v2.1.198). Deliberately *not* modelled — routed to Young–Daly's measured `δ` instead, because its volume is unpublished |

**Appendix C's values are unchanged and remain correct.** They are the five-minute regime, which is
the floor. The running regime is resolved at runtime and never written back into config, so D11's
lint gate and `TestDefaults_MatchesAppendixCVerbatim` keep working untouched. That was the point of
D11: a price change should move a config value and a report, not a schema.

### What could not be verified, and will not be

§2.2, §2.3, §2.4 and §2.6 describe Claude Code's compaction internals by identifier —
`createCacheEditsBlock`, `adjustIndexToPreserveAPIInvariants()`, `stripReinjectedAttachments()`,
`groupMessagesByApiRound`, the `marble_origami` recursion guard, the `minTokens`/`maxTokens`
preservation constants. None appears in published documentation. None can be confirmed or refuted
from it. All are **unchanged in v1.3**.

No attempt was made to check them against decompiled or leaked builds. Two reasons, the second
load-bearing:

1. Such material carries no guarantee of matching the version any user is running, so "verification"
   against it is a guess with a citation attached.
2. §7.1 makes Qompack a **sidecar**: *"A plugin cannot replace Claude Code's compaction. It can only
   surround it… Everything is designed to degrade gracefully."* A design premised on not depending on
   internals must not acquire that dependency through its own revision.

Treat §2.2–§2.6 as **motivating background at the version they were written against**, not as a live
contract. Where a §2 fact became load-bearing — §2.5's arithmetic, §2.7's table — it is publicly
documented, and it checks out.

One further item is unverifiable but harmless: §2.7's claim that the skill *index* and descriptions
are not re-injected at all. The published table has no row for it — neither confirmed nor
contradicted. Retained as written, and filed in §12's upstream-issues list, which is the disposition
that does not depend on the answer.

### Blast radius, and how it was contained

A revision to a document that 69 plan files quote verbatim is a re-sync problem. It was kept small
on purpose: **every change that touched a sentence quoted elsewhere was made additively**, leaving
the quoted text byte-identical and appending after it. Four constructs genuinely changed shape and
were re-synced by hand:

| Changed construct | Re-synced in |
|---|---|
| §12's cannot-do list, 8 bullets → 12 | `V6-SP-18` (subagent brief), `V6-VERIFY` (row 1.18.5 and §8 step 5) |
| Appendix A's ski-rental line | `V1-SP-01`, `V4-SP-12`, `V5-SP-16` |
| §5.3's objective block | `V4-SP-12`, `V5-SP-15` |
| §8.4's composite-trigger block | `V4-SP-12` (design-context quote and the `evaluate.go` comment) |

Left byte-identical and therefore needing no re-sync: §5.1's multiplier sentence (5 quotes), §5.6's
ski-rental bullet (3), §5.4's TTL-bimodality bullet and strategic-consequence blockquote, §2.5's
controls sentence, §2.7's table rows, §5.5's `cache_edits` paragraph, and Appendix C's `cache` line
(7 quotes — the line is unchanged; only a comment was added above it, and JSONC comments are stripped
before the golden test's deep-equal).

`V2-report.md` §19 quotes §5.1's *pre-revision* text. That is correct and stays: the report is
append-only and §19 is the record of what the document said when the discrepancy was found.

### Sources

All fetched 2026-08-23.

| Source | Used for |
|---|---|
| `platform.claude.com/docs/en/build-with-claude/prompt-caching` | Changes 1 and 3; §5.1's `r`; §5.2's prefix rule |
| `code.claude.com/docs/en/prompt-caching` | Changes 2, 4, 5, 6; §5.4's sliding TTL |
| `code.claude.com/docs/en/context-window` | §2.7's table; change 8 |
| `code.claude.com/docs/en/model-config` | §2.5's arithmetic — the 967K confirmation; change 7 |
| `code.claude.com/docs/en/statusline` | Change 6; §5.6's plugin-reach limit |
| `code.claude.com/docs/en/hooks` | Change 6; §7.3's hook surface |
| `code.claude.com/docs/en/plugins-reference` | §5.6; the `subagentStatusLine` limit |
| `platform.claude.com/docs/en/build-with-claude/compaction` | §2.8 |

---

## For the next revision

1. **Re-run the §5.1 check.** It is the one instruction the document gives its own reader, and prices
   move. Confirm `r`, both `w` values, both TTLs, and whether a third TTL has appeared.
2. **Re-check §2.7's table and §2.5's arithmetic**, the two `§2` facts that are load-bearing and
   publicly documented. If §2.5's formula stops reproducing the published per-model figure, the
   scheduler's thresholds are the first thing to correct.
3. **Check whether the three unobservable cache signals became observable** — a resolved-TTL field or
   cache-token counts in hook input would let §5 stop modelling and start measuring, and both are
   filed as upstream issues in §12.
4. **Do not verify §2.2–§2.6 from a leaked build**, however tempting. The reasoning is above and it
   has not changed.
5. **Add a section here rather than editing this one.** Same discipline as the document itself.

---

## v1.4 — §8.6's nested-`CLAUDE.md` rule

### Origin

`plans/V4-SP-11-rehydrator-l5.md` is the subplan that implements §8.6's instruction-restoration
clause. Writing it surfaced a three-way disagreement about one sentence, which no commit had ever
recorded a decision about, and which the subplan explicitly refused to settle on its own authority.

### The conflict

| Source | Reading | Status before v1.4 |
|---|---|---|
| `Qompack.md` §8.6 | "Every nested `CLAUDE.md` in a directory **containing** a pointer-set file" | narrow; design of record |
| `plans/00-ARCHITECTURE.md` §5.15 | `// NestedClaudeMD returns CLAUDE.md files in directories containing a pointer-set file.` | narrow; restated |
| `internal/rules/rules.go` (SP-01) | "every CLAUDE.md under root that lives in a directory containing (**or ancestor to**) a path in pointers" | broad; shipped doc comment |
| `internal/rules/rulestest/suite.go` `nested_claude_md_discovery` | fixture puts `CLAUDE.md` at `src/pkg`, pointer at `src/pkg/deep/thing.go`, asserts `require.Len(t, matched, 1)` | broad; **executable**, and the ancestor is two levels up |

The two documents landed together in the initial commit `d361f06`; the code landed in SP-01's
conformance-suite commit `3464079`. No commit between them records a decision to widen, so this was
a genuine open question rather than settled precedence — and the precedence rule in
`plans/00-ARCHITECTURE.md:5` ("disagree on *what* to build, `Qompack.md` wins") pointed at the
narrow reading while the only executable artifact pointed at the broad one.

### Why the widening, rather than correcting the code

Both readings were implementable and neither was free. Correcting the code would have meant amending
the shipped doc comment **and** relocating the `rulestest` fixture's `CLAUDE.md` from `src/pkg` down
to `src/pkg/deep` — editing an SP-01-owned conformance suite to make a narrower behaviour pass.

The deciding argument is behavioural, not procedural. A nested `CLAUDE.md` governs its entire
subtree in Claude Code. §2.7's own row says these files are "**Lost** until a file in that subdir is
read again", and G4.2 exists to repair exactly that loss. Under the narrow reading, a pointer at
`src/pkg/deep/thing.go` restores nothing when the governing rule sits at `src/pkg/CLAUDE.md` — an
instruction that *is* in force for that file, *is* lost after compaction, and is silently not
restored, while §9's G4.2 row reports the gap closed. The narrow reading did not describe a smaller
feature; it described a feature with a hole in the middle of its stated purpose.

### What changed

- `Qompack.md` §8.6, instruction-restoration bullet 2 — "containing" → "containing, or ancestor to",
  with the two bounds the implementation needs stated inline: stop before the project root (the host
  re-injects it, §2.7), and cap the walk.
- `plans/00-ARCHITECTURE.md` §5.15, the `NestedClaudeMD` doc line — same widening, plus
  `maxAncestorDepth=32`.

### What did not change

- `internal/rules/rules.go` and `internal/rules/rulestest/suite.go` are **untouched**. They were
  already correct; the documents moved to them, which is the direction that cost nothing to verify —
  the conformance case is executable and had never been able to run against a real implementation.
- §12's cannot-do list. This widens what L5 reads from disk on its own initiative; it does not claim
  any new power over the host.
- Every other §8.6 sentence, including the 8–12K budget and the ~450-token skill index.

### Recorded at the same time, and deliberately *not* revised

§8.7's design note — "`already_tried` should be surfaced in the rehydrated context as a *standing
instruction*, not merely an available tool" — is unscoped, while `plans/00-ARCHITECTURE.md` §5.15
calls `StandingInstruction` "item 3's companion" and `internal/rehydrate/rehydratetest/behaviour.go`
asserts that with no item 3 the sentence must not appear anywhere in the payload. The two readings
differ in exactly one case: an empty eliminations set.

This was left as the narrow (item-3-scoped) reading, unrevised, because the divergence is defensible
on its own terms rather than an error: telling an agent to call `already_tried` before committing to
an approach when nothing has ever been eliminated is noise that costs budget and trains the agent to
skip the line, and §8.6 item 8's affordance notice still names the tool. §9's G6.2 mitigation row is
therefore conditional on a non-empty ledger at `SessionStart`, and that conditionality is recorded
here rather than in the design of record.
## v1.5 — evidence-grounded planning-only migration (2026-09-06)

**Numbering.** This section was drafted as "v1.4" on the `verify/v3` snapshot (`7f92af5`) before the SP-11 v1.4 above had reached the combined baseline; SP-19 M0-01 renumbered it v1.5 when the planning revision landed on `develop`'s wave-3 baseline. Nothing in the v1.4 record above is superseded by the renumbering.

### Origin

The user authorized revising Markdown plans while preserving completed Waves 0–2 and active Wave 3. The repository design was v1.3; a separately named Pasted markdown.md was not found. The actual design and original plan/test/source locations were inspected. [MIGRATION-EVIDENCE.md](MIGRATION-EVIDENCE.md) records the baseline, sources, claims, blueprint, routing and review. The earlier v1.3 record above remains historical; its future instructions do not override the current scope or corrected contracts.

### Confirmed — inspected and preserved

The checkout remains verify/v3 at 7f92af5353a6bd084aa49043351e4183ecbcb2ad. V3's 2026-08-26 final addendum records the user's J5 waiver/closure and Wave 3 branch creation; its earlier blocked cells were not rewritten. Active sibling SP10–13 source demonstrates ongoing work, not newly passed gates. Existing negative-knowledge matching already excludes reason and confirms Bloom positives exactly; no rewrite is proposed for those correct seams.

Completed subplans/reports, NEXT-SESSION.md, session records, machine-read TSVs, source/tests/manifests/fixtures/config/CI/data and initial untracked coverage/benchmark files remain protected. The explicit design revision changes only the 21 planning Markdown paths in the ledger. Existing branch/commit/role/checkbox conventions remain future instructions.

### Changed

Qompack.md now separates documented contracts, observed code, unsupported controls and experiments. All 40 G IDs across 10 categories remain traceable. Native O(delta), guaranteed first-turn savings, custom_instructions output-setting, historical cuts/eviction/cache-marker control, generic greedy/KL/slicing/Belady guarantees and arbitrary truncation are removed from current specifications. Cache break-even is conditional N versus w+(N−1)r, with request-category accounting and unknown telemetry.

SP19 reconciles capabilities/cost/settings/baselines; SP20 remediates capture privacy/fidelity, publication, acknowledged drains, reversible legacy import, state and trust; SP13 extends actual archive recovery. SP10/11 preserve active Wave 3 and correct frontiers/lifecycle/authority/complete-record recovery. SP21 adds a distinct default-off admission extension after M1–M3. SP12/15/16 specify supported cadence, feasible future representations, warning-only loops and scoped reuse. SP14/17/18 and V4–V6 define continuous measurement, installed compatibility, held-out task evaluation, release and rollback gates.

Old source/schema examples were removed from revised subplans so future agents receive one current prose specification. Architecture's retained signatures/fixtures describe existing compatibility and are explicitly superseded by §0.1 where old behavior differs. Historical revision logs and benchmark outputs retain their old meaning; no completed implementation item was checked or reset.

### What was not verified

No application test, build, benchmark, replay, integration/capability probe, installer, generator, migration or Git mutation ran. Reading source/tests and independent-agent agreement do not establish installed runtime correctness. Host versions/managed restrictions, actual retrieval/replacement behavior, storage crash/backup/rollback, UAT, pricing/telemetry completeness and release/name support remain named future gates. This revision does not promise runtime perfection or performance improvement.

### Review and blast radius

P0–P4 scope/discovery/blueprint/pilot/drafting were followed by a fresh requested Terra-high semantic reviewer and reused Luna-low structural scout. Their findings and coordinator repairs are recorded in the ledger. Only Markdown planning edits are attributable to the pass; protected implementation and historical work were not reset. Final read-only documentation checks and remaining limitations are recorded in the ledger, separate from future implementation acceptance.

### Sources

[The source register](MIGRATION-EVIDENCE.md#source-register-and-verification-limits) distinguishes sources opened on 2026-09-06 from carried prior-review references S1–S29. No current price or installed capability is inferred from an old source or model name.

## v1.5 addendum — merge completed Wave 3 before remaining SP-19 (2026-09-06)

The user clarified that original SP-10–13 are complete and requested that their integration precede the rest of SP-19. That completion report supersedes the initial in-progress status without certifying a combined tree or the new migration requirements. Original checkboxes, reports, delivery branches and implementation remain preserved.

SP-19 now starts with M0-00/M0-G0: nominate current delivery tips, preserve worktree dirt and history, review all overlapping contracts, merge SP-10 → SP-11 → SP-12 → SP-13 into `develop` under architecture §9, resolve conflicts on the incoming branch, validate each merge and the combined baseline, and obtain independent acceptance. Already-integrated deliveries are verified rather than merged again. Unexplained integration regressions block the gate; inherited defects, stale assertions and unavailable target evidence remain explicitly attributed. The remaining M0 tasks, original eight SP-19 commits and contract amendment wait for this gate. Full revised V4 migration verification follows remediation, avoiding a prerequisite cycle.

The master execution guide, design order/closing, architecture precedence, V4 verification and evidence ledger now share that sequencing. SP-10–13 status/placement text distinguishes user-completed original deliveries from future corrections. The new merge gate includes abort/retry/revert-or-forward-fix planning without shared-history resets; merging does not authorize deployment or data migration.

This addendum changes only existing authorized planning Markdown. No merge, branch creation, code/configuration edit, test, build or runtime validation occurs here. Documentation checks and focused review are recorded in MIGRATION-EVIDENCE.md; no promise of a perfect future merge substitutes for actual reviewed evidence.
