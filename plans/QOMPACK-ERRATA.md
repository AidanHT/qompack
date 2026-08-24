# `Qompack.md` verification record

`Qompack.md` is **read-only** (`plans/README.md`, Global rules). It carries a **Revision log**, so it
is revisable — but only deliberately, as a versioned revision, never edited in passing by a subplan.
It has been revised twice before: v1.1 (full-plan review) and v1.2 (latency made an explicit
objective).

**v1.3 landed 2026-08-23** and this file is its record: what was checked, what held, what moved, what
could not be verified, and against which sources. The read-only rule is back in force. The next
revision appends to this file rather than replacing it.

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
