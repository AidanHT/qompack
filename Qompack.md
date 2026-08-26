# Qompack

**A Claude Code plugin for cache-aware, retrieval-backed, measurable context compaction.**

*Design document v1.0*

---

## 0. Executive summary

Claude Code's compaction system is a well-engineered three-tier pipeline whose core design decisions — deferring the expensive LLM call, reusing the conversation's own cache prefix for the summarization request, surgical `cache_edits` deletion — are correct and non-obvious. This plan does not propose replacing it. It proposes **surrounding it**.

The thesis in one sentence:

> Claude Code's compaction treats context reduction as a *summarization problem under a token budget*, when it is actually a *rate–distortion problem under a prefix-cache cost model*, and almost every observed failure is a consequence of optimizing the wrong objective with the wrong cost function and no measurement loop.

Three structural consequences follow, and they account for the overwhelming majority of user-reported failures:

1. **Compression without an index.** The transcript still exists on disk after compaction, but the agent cannot reach it. Detail is destroyed rather than demoted to a retrievable store.
2. **Compression of compressions.** Each pass summarizes the previous summary. The data processing inequality guarantees monotone, irreversible information loss. This is not a prompt-quality problem and cannot be fixed by prompt engineering.
3. **No measurement.** Nothing in the system evaluates its own output. Every threshold is a hand-tuned constant with no feedback signal, and the first symptom of a bad compaction is behavioural degradation noticed several turns later.

Qompack is a plugin that adds a durable, content-addressed, cache-aware memory layer alongside the existing pipeline. It does not intercept the summarizer. It changes *what the summarizer is asked to preserve*, *when it runs*, *what survives it*, and *whether anyone can tell if it worked*.

**Name rationale.** *Qompack* is a phonetic play on "compact" — same sound, distinct and searchable spelling, unclaimed as a package/command name. The guiding design principle behind it is that of a palimpsest: a manuscript overwritten with new text where the earlier writing remains legible underneath. That is precisely the target behaviour — compaction that overwrites the working context while keeping every prior layer recoverable from the store.

---

## Table of contents

1. [The problem](#1-the-problem)
2. [Current architecture — what we are building around](#2-current-architecture--what-we-are-building-around)
3. [Complete gap inventory](#3-complete-gap-inventory)
4. [Theoretical reframe](#4-theoretical-reframe)
5. [The prompt-cache cost model](#5-the-prompt-cache-cost-model)
6. [Algorithmic toolkit](#6-algorithmic-toolkit)
7. [Plugin architecture](#7-plugin-architecture)
8. [Component specifications](#8-component-specifications)
9. [Gap traceability matrix](#9-gap-traceability-matrix)
10. [Phased build plan](#10-phased-build-plan)
11. [Evaluation methodology](#11-evaluation-methodology)
12. [Risk register and honest limitations](#12-risk-register-and-honest-limitations)
13. [Appendix A — mathematical reference](#appendix-a--mathematical-reference)
14. [Appendix B — literature index](#appendix-b--literature-index)
15. [Appendix C — configuration schema](#appendix-c--configuration-schema)

---

## 1. The problem

### 1.1 The user-visible failure

A session is productive. The agent has the stack trace, a narrowed hypothesis, and the exact files. Auto-compact fires. Thirty seconds later the agent is proposing changes to the wrong file, re-reading things it already read, and re-attempting an approach it eliminated forty minutes ago.

The canonical degradation curve across successive compactions:

| Pass | Retained description |
|---|---|
| 0 | `Bug in Stripe webhook retry logic — pool bypass under pgbouncer 1.18` |
| 1 | `Found bug in Stripe webhook retry logic` |
| 2 | `Working on payment bug related to missing defaults` |
| 3 | `Fixing payment-related issue in the codebase` |

By pass 3 the agent is searching the wrong directory. This is not a bad summarizer. It is a cascade of lossy channels behaving exactly as information theory says it must.

### 1.2 Why the existing design is not simply "wrong"

It is important to be precise about this, because a redesign that discards the good parts will be worse.

What the current system gets right:

- **Tiering to defer the expensive path.** MicroCompact → Session Memory Compact → Full Compact is the correct escalation order. Cheap deterministic curation before expensive lossy summarization.
- **Cache-preserving deletion.** The `cache_edits` path deletes tool results server-side by `tool_use_id` without invalidating the cached prefix. This is the single most valuable mechanism in the system.
- **Cache-key reuse for the summarization call.** The compaction fork reuses the main conversation's system prompt, tools, model, and message prefix, appending the compaction instruction as a new user turn. The alternative — a dedicated summarizer system prompt — was measured at a 98% cache miss rate.
- **The analysis scratchpad.** `<analysis>` gives the model chain-of-thought room, then `formatCompactSummary()` strips it. Quality without post-compact token cost.
- **Circuit breaking.** `MAX_CONSECUTIVE_AUTOCOMPACT_FAILURES = 3` exists because 1,279 sessions were observed with 50+ consecutive failures, wasting roughly 250K API calls per day globally.

Qompack preserves all five. The problems are elsewhere.

### 1.3 The three root causes

Everything in §3 reduces to one of these:

**RC-1 — Destructive rather than demotive.** Compaction deletes from context without leaving a retrieval path, even though the source data is durable on disk and, for most tool results, trivially reproducible.

**RC-2 — Recursive compression.** Summaries are derived from summaries. `I(X;T₃) ≤ I(X;T₂) ≤ I(X;T₁)` by the data processing inequality. Nothing is pinned outside the cascade.

**RC-3 — Unmeasured.** No distortion metric, no ceiling, no regression signal. Constants (13K buffer, 20K reserve, 5 files, 50K budget, 5K/file, 25K skills) are all unvalidated.

---

## 2. Current architecture — what we are building around

### 2.1 The three tiers

| Tier | Mechanism | Trigger | Compression | Cache impact |
|---|---|---|---|---|
| MicroCompact | Clear old tool results | Every turn (time- or count-based) | ~10–50K tokens | Preserved via `cache_edits`, or rebuilt via content-clear |
| Session Memory | Replace old messages with pre-built memory | Auto-compact threshold | ~60–80% | Invalidates, but no LLM call |
| Full Compact | LLM summarizes entire conversation | Auto-compact or `/compact` | ~80–95% | Invalidates, costs one API call |

### 2.2 MicroCompact

Compactable tool set:

```
FileRead, Bash/PowerShell, Grep, Glob, WebSearch, WebFetch, FileEdit, FileWrite
```

Only high-volume, reproducible results are targeted. AgentTool and MCP results are preserved.

Two paths:

- **Time-based (cold cache).** When the gap since the last assistant message exceeds a threshold, directly mutate message content — replace old results with `[Old tool result content cleared]`, keeping the last N. Brute force is acceptable because the cache is already cold.
- **Cached (warm cache).** Never touches local messages. Queues `createCacheEditsBlock(state, toolsToDelete)`; the API layer injects `cache_edits`, and the server removes results from its cached copy while preserving the prefix hit.

Token estimation pads by 4/3 and flat-rates images and PDFs at 2,000 tokens. Thinking blocks count text only, not the JSON wrapper or signature.

### 2.3 Session Memory Compact

Uses a continuously-maintained session memory as the compaction summary, eliminating the summarization API call entirely.

```
Before: [msg1, msg2, ..., msg_summarized, ..., msg_recent1, msg_recent2]
After:  [boundary, session_memory_summary, msg_recent1, msg_recent2]
```

Preservation config: `minTokens: 10_000`, `minTextBlockMessages: 5`, `maxTokens: 40_000`. Expansion runs backwards from the last summarized message until both minimums are met, capped at `maxTokens`.

`adjustIndexToPreserveAPIInvariants()` (80+ lines) enforces two invariants: no `tool_result` without its matching `tool_use`, and no orphaned thinking blocks — assistant messages sharing a `message.id` from streaming must be kept together.

### 2.4 Full Compact pipeline

```
1.  PreCompact hooks
2.  stripImagesFromMessages()          → [image] markers
3.  stripReinjectedAttachments()       → removes skill_discovery / skill_listing
4.  streamCompactSummary()             → fork agent, with PTL retry
5.  formatCompactSummary()             → strip <analysis>, keep <summary>
6.  readFileState.clear()
7.  Restore post-compact context:
      • top 5 recently-read files (50K budget, 5K/file)
      • invoked skills (25K budget, 5K/skill)
      • active plan content
      • plan mode instructions
      • deferred tool deltas
      • agent listing deltas
      • MCP instruction deltas
8.  SessionStart hooks (source=compact)
9.  PostCompact hooks
10. Re-append session metadata (16KB tail window)
```

The 9-section summary schema:

1. Primary request and intent
2. Key technical concepts
3. Files and code sections (with full snippets)
4. Errors and fixes
5. Problem solving
6. All user messages
7. Pending tasks
8. Current work
9. Optional next step (verbatim quotes)

### 2.5 Trigger arithmetic

```
effectiveContextWindow = contextWindow − min(maxOutputTokens, 20_000)
autoCompactThreshold   = effectiveContextWindow − 13_000
```

For a 200K model: effective ≈ 180K, threshold ≈ 167K.

Warning state machine:

| State | Boundary |
|---|---|
| `isAboveWarningThreshold` | effectiveWindow − 20K |
| `isAboveErrorThreshold` | effectiveWindow − 20K |
| `isAboveAutoCompactThreshold` | effectiveWindow − 13K |
| `isAtBlockingLimit` | effectiveWindow − 3K (manual compact required) |

Recursion guards skip compaction when `querySource` is `session_memory`, `compact`, or `marble_origami`.

User-facing controls: `/autocompact <value>`, the `autoCompactWindow` setting, `--autocompact` flag, `CLAUDE_CODE_AUTO_COMPACT_WINDOW` env var. Range 100K–1M, capped at the model's window.

**The arithmetic above is confirmed; the default around it is per-model.** With no auto-compact
window set, current Claude Code compacts *at the model's context limit* rather than at one formula
for every model, with documented exceptions per model and per deployment. The formula still governs
where a set window lands: Sonnet 5 at a 1M window is documented to auto-compact "at about **967K**
tokens by default", and `1 000 000 − 20 000 − 13 000 = 967 000` reproduces it exactly.

Four further variables move the window Claude Code believes it has, and a scheduler that reads only
`CLAUDE_CODE_AUTO_COMPACT_WINDOW` will size against the wrong one:

| Variable | Effect |
|---|---|
| `CLAUDE_CODE_MAX_CONTEXT_TOKENS` | Declares the window to assume for a gateway or unrecognized model ID |
| `CLAUDE_CODE_DISABLE_1M_CONTEXT` | A natively-1M model compacts at the 200K boundary instead |
| `CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT` | Compact only after the API rejects the conversation |
| `DISABLE_COMPACT` | Disables compaction entirely — every trigger in §8.4 becomes advisory |

1M context windows are now available on several models, so "capped at the model's window" is a
larger number than it was when this section was first written, not a different rule.

### 2.6 PTL recovery and partial compact

When the compaction request itself exceeds the prompt-too-long limit, the system groups messages by API round (`groupMessagesByApiRound`, boundary on new assistant `message.id`) and drops the **oldest** groups until the gap is covered. Max 3 retries; fallback drops 20% of groups when the gap is unparseable.

`partialCompactConversation()` supports two directions:

| Direction | Summarized | Kept | Cache |
|---|---|---|---|
| `'from'` | Messages after pivot | Earlier messages | **Preserved** (kept messages are a prefix) |
| `'up_to'` | Messages before pivot | Later messages | **Invalidated** |

### 2.7 What survives compaction

| Mechanism | After compaction |
|---|---|
| System prompt, output style | Unchanged; outside message history |
| Project-root `CLAUDE.md`, unscoped rules | Re-injected from disk |
| Auto memory (`MEMORY.md`) | Re-injected from disk |
| Rules with `paths:` frontmatter | **Lost** until a matching file is read again |
| Nested `CLAUDE.md` in subdirectories | **Lost** until a file in that subdir is read again |
| Invoked skill bodies | Re-injected, 5K/skill and 25K total, oldest dropped, truncated head-first |
| Skill *index* / descriptions | **Not re-injected at all** |
| Hooks | N/A — hooks run as code |

The summarization request **inherits the session's extended-thinking configuration** (Claude Code
v2.1.198 onward): it reasons with thinking when the session has it enabled and stays off otherwise,
and the session's own settings are unchanged afterwards. That is a cost, not a survival rule — it
adds thinking output tokens to every compaction on a thinking-enabled session — and §5.3 carries it.

One row here is **unverified against current documentation**: the claim that the skill *index* and
descriptions are not re-injected at all. The published table has no row for it, so it is neither
confirmed nor contradicted, and it is retained as written. §12 files it as an upstream issue, which
is the disposition that does not depend on the answer.

### 2.8 The API-level surface (for reference)

The `compact_20260112` beta on the Messages API is a weaker sibling:

- `trigger: {type: "input_tokens", value: N}` — minimum 50K, default 150K
- `instructions` — **replaces** the default prompt entirely, does not supplement
- `pause_after_compaction` — returns `stop_reason: "compaction"` so the caller can inject preserved content between the summary and the continuation
- `usage.iterations[]` — compaction usage is *not* in top-level `input_tokens`/`output_tokens`

Documented limitations: summarization always uses the request's model (no cheap summarizer), and the model sometimes calls a tool instead of summarizing, returning a compaction block with `content: null`.

**Relevance to this plan:** if you later port Qompack out of Claude Code into your own harness, `pause_after_compaction` is the injection point that replaces the `SessionStart(source=compact)` hook. The design below is deliberately structured so that swap is a single-module change.

---

## 3. Complete gap inventory

Twenty-eight gaps in nine categories, each tagged for traceability.

### G1 — Trigger and timing

| ID | Gap |
|---|---|
| G1.1 | **Threshold is task-blind.** Fires at a token count, not a semantic boundary. Lands mid-hypothesis, between a `tool_use` and the reasoning that would interpret it, or halfway through a multi-file refactor. |
| G1.2 | **Agent has no agency over timing.** `PreCompact` can react but cannot delay, reschedule, or request. There is always a race between "last state written to disk" and "compaction fires," and everything in that window is in-context-only reasoning that evaporates. |
| G1.3 | **The 13K buffer is a fixed constant.** The check runs *before* each API call, so the count can grow by a full response plus tool results between checks. One large `FileRead` overshoots it. |
| G1.4 | **The blocking limit is a cliff.** At `effectiveWindow − 3K` manual compaction becomes mandatory, and a completely full window can block `/compact` from running at all — the recovery mechanism becomes unavailable exactly when needed. |
| G1.5 | **No task-boundary signals consulted.** Todo completion, passing test runs, git commits are all natural safe points; none are wired to compaction. |

### G2 — Generational information loss

| ID | Gap |
|---|---|
| G2.1 | **Each pass summarizes the previous summary.** Monotone irreversible decay by the data processing inequality. |
| G2.2 | **Nothing is pinned.** No mechanism to mark a fact load-bearing and carry it verbatim across arbitrarily many compactions. |
| G2.3 | **Section 6 ("All user messages") is regenerated, not preserved.** It drifts along with everything else despite existing precisely to prevent drift. |
| G2.4 | **The summary is prose, not typed state.** No per-field schema guarantees, so it cannot be diffed, validated, or queried. |
| G2.5 | **Nothing verifies the summary against ground truth.** Section 3's file list is never checked against `git status` or the working tree. |
| G2.6 | **Session Memory Compact has the same drift on a different clock.** The memory is itself maintained by a background agent, and the tier is experimental. |

### G3 — Compression without an index

| ID | Gap |
|---|---|
| G3.1 | **The transcript is on disk but unreachable.** No retrieval path back to detail post-compaction. |
| G3.2 | **Tombstones carry no pointers.** `[Old tool result content cleared]` says a tool ran and nothing else — even though `FileRead`, `Grep`, and `Glob` results are trivially reproducible and `Bash` output is in the session log. |
| G3.3 | **Eager restoration is the inverse of retrieval.** Re-injecting the top 5 files against a 50K budget is a guess. A lazy affordance costs ~200 tokens and is correct rather than probabilistically correct. |
| G3.4 | **Section 3 of the prompt requests full code snippets** — spending the scarcest budget in the system on the most reconstructible content that exists. |

### G4 — Uneven, silent instruction survival

| ID | Gap |
|---|---|
| G4.1 | **Path-scoped rules vanish** until a matching file is coincidentally re-read. |
| G4.2 | **Nested `CLAUDE.md` files vanish** on the same terms. |
| G4.3 | **Skill bodies return partially truncated** — head-first, 5K/skill, 25K total, oldest dropped. A partial instruction set is worse than an absent one because the model has no signal it is reading a fragment. |
| G4.4 | **The skill index is not re-injected,** so the model loses awareness of what it could invoke. `stripReinjectedAttachments()` removes `skill_discovery`/`skill_listing` before summarization, so it is not in the summarized record either. |
| G4.5 | **No drop report.** Nothing surfaces "these four constraints are no longer in context." The agent's operating rules change silently mid-session. |

### G5 — One cut, one funnel

| ID | Gap |
|---|---|
| G5.1 | **Partial compact is pivot-based and single-cut.** Cannot compact turns 20–60 while keeping 1–10 verbatim and 61+ live. |
| G5.2 | **The useful direction is the expensive one.** `'from'` preserves cache; `'up_to'` — summarize old, keep recent — invalidates by construction. Exactly backwards from the common case. |
| G5.3 | **Heterogeneous content, homogeneous treatment.** User instructions, decisions with rationale, file states, tool outputs, and ruled-out approaches have wildly different retention economics and all go through one prose summarizer with one lifetime. |

### G6 — Negative knowledge

| ID | Gap |
|---|---|
| G6.1 | **No schema slot for ruled-out approaches.** Section 4 covers errors that were *fixed*; approaches investigated and eliminated without producing a fix have no home. |
| G6.2 | **Direct cause of the most-reported failure mode** — the agent re-attempts something already eliminated, burns time, and eliminates it again. |
| G6.3 | **Negative knowledge is the highest-Δ content in the transcript.** A ruled-out branch eliminates an entire region of action space; maximal entropy reduction per token, and it is exactly what gets dropped. |

### G7 — Failure handling and cost

| ID | Gap |
|---|---|
| G7.1 | **The summarization call is structurally the most expensive call in the session** — it runs at ~167K input tokens. |
| G7.2 | **No cheap-model option.** The fork uses the session model; the API documents the same constraint. |
| G7.3 | **PTL recovery sacrifices exactly the wrong thing.** It drops the *oldest* API-round groups — where the original task statement and earliest user intent live. Sections 1 and 6 exist to preserve that content and the recovery path deletes it first. |
| G7.4 | **The circuit breaker gives up rather than degrading.** Three failures and auto-compact stops for the session, leaving it unrecoverable. |
| G7.5 | **The compaction call can fail by calling a tool** instead of summarizing (`content: null` on the API side). |
| G7.6 | **Compaction loops and redundant full-context resubmission** are open reports, as is a state where the indicator jams above 100% and every turn triggers compaction. |

### G8 — Zero observability

| ID | Gap |
|---|---|
| G8.1 | **No diff, no retention score, no drop report.** First signal of a bad compaction is behavioural degradation noticed several turns later. |
| G8.2 | **The summarizer works from the worst possible vantage point** — the same model, in the same degraded long-context state, that has been drifting all session. Summary quality is lowest exactly when stakes are highest. |
| G8.3 | **No feedback on `/compact <instructions>`.** The only steering lever is applied at the moment of compaction, not as a standing preference, and nothing reports whether it worked. |

### G9 — Not a durable checkpoint

| ID | Gap |
|---|---|
| G9.1 | **The summary lives only in context.** The next compaction re-summarizes it. No immutable versioned artifact is written as part of the transaction. |
| G9.2 | **The community keeps rebuilding this layer by hand** — plan/context/tasks three-file patterns, MemoryForge-style hook systems, PreCompact state writers. |
| G9.3 | **Those workarounds rest on undocumented contracts** — `SessionStart` firing with `source=compact`, `additionalContext` in `hookSpecificOutput` reaching context, `PreCompact` having time to write. Silent breakage if any changes. |

### G10 — Smaller but real

| ID | Gap |
|---|---|
| G10.1 | **Subagent output is compressed twice.** A subagent returns a summary; that summary is then summarized. The parent never held the detail, so there is no recovery at either level. |
| G10.2 | **Token estimation is coarse.** 4/3 padding compacts earlier than necessary; flat 2,000 tokens for images and PDFs is badly wrong in both directions for document-heavy sessions. |

---

## 4. Theoretical reframe

### 4.1 Rate–distortion, not summarization

Standard compression minimizes reconstruction error of the source. Compaction does not need to reconstruct the transcript at all. It needs to preserve whatever determines the agent's **future action distribution**. Those are different objectives, and the 9-section prompt optimizes the first.

Information bottleneck gives the clean statement. With `X` = full transcript, `T` = compacted representation, `Y` = correct future actions:

```
minimize   I(X;T) − β · I(T;Y)
```

Fidelity to `X` is a **cost**, not a goal. Only mutual information with `Y` counts. This single reframe reclassifies half of §3: G3.4 (code snippets in the summary) and G5.3 (homogeneous treatment) are both symptoms of optimizing `I(X;T)`.

The distortion measure that matters:

```
D = KL( P(a | X) ‖ P(a | T) )
```

How much the agent's next-action distribution moves when the transcript is swapped for the summary.

### 4.2 Making distortion measurable

Clean logprobs over action sequences are not available through the Messages API. The empirical estimator is, and it is the highest-leverage single component in this plan:

**Counterfactual replay.** Take real logged sessions. Fork at turn *t*. Run one branch uncompacted and one compacted. Measure divergence over the next *K* actions:

- Jaccard distance over the set of files touched
- Edit distance over the tool-call sequence
- Turn index of first divergence
- Whether the same decision was reached
- Redundant work: re-reads of files already read, re-attempts of eliminated approaches

This is an unbiased estimate of `D`. Without it, every other idea in this document is untested intuition — which is why it is Phase 0 rather than Phase 6.

### 4.3 Cross-entropy as the selection signal

A block's value is how much it reduces the model's uncertainty about tokens it will need to produce later:

```
Δ(c) = H(future | context \ c) − H(future | context)
```

Blocks with `Δ ≈ 0` are dead weight regardless of how important they look to a human reader. This replaces "keep the last 5 tool results" — a recency heuristic — with an information-contribution ranking.

Two computable proxies, since the future is unavailable at compaction time:

- **Retrospective Δ.** At compaction time you know what happened between block *c* and now. Score *c* against the already-observed continuation. Blocks that explained the recent past tend to constrain the near future.
- **Reconstruction / redundancy test.** Can the content be reproduced from the rest of the context? If yes, it compresses to a pointer. A `FileRead` superseded by a later diff is pure redundancy.

Prior art directly portable to the MicroCompact tier: Selective Context (prunes low-self-information lexical units using a small causal LM), LLMLingua (small model as a perplexity-based budget controller for coarse-to-fine compression), and H2O-style KV-cache eviction (keeps tokens by accumulated attention mass rather than recency — the empirical form of the Δ criterion, known to beat windowing). Attention scores are not exposed through the API; the first two need only a small local model.

### 4.4 MDL — encode only the non-reconstructible residue

Minimum description length gives a sharp partition rule.

**Reconstructible at near-zero cost — must become pointers, never prose:**

- File contents (re-read)
- `git status`, `git diff`, branch state
- Directory structure
- Deterministic tool output (`Grep`, `Glob`)
- Test results (re-run)

**Non-reconstructible — this is what the budget is for:**

- User intent and its evolution
- Decisions and their rationale
- Approaches ruled out and why
- Exact text of an error that will not reproduce
- Causal chains of reasoning
- Constraints discovered empirically

This is the theoretical case for G3, and it condemns section 3 of the current prompt and the 50K eager restoration budget. Five paths plus a `re_read(path)` affordance costs ~200 tokens and is *correct* rather than probabilistically correct.

### 4.5 The prior filter — encode surprisal, not facts

The model is itself a compressor with an enormous prior. The summary should not encode anything the prior already supplies.

- `Uses Postgres` — near-zero information
- `Uses Postgres, but retry logic bypasses the pool because of a pgbouncer 1.18 bug` — high surprisal, must be encoded

This yields a materially better summarizer instruction than *write down anything that would be helpful*:

> **Encode what a competent engineer with no session history would get wrong.**

### 4.6 The DPI wall

For a cascade of channels:

```
I(X; T₃) ≤ I(X; T₂) ≤ I(X; T₁)
```

Once summary 2 is derived from summary 1, no amount of prompt engineering recovers what summary 1 dropped. The degradation table in §1.1 is the DPI operating as specified.

**The only fix is structural: never compress a compression.** Encode each transcript segment exactly once, from the original on disk, and append. This is a non-negotiable invariant of the Qompack design and is enforced by construction in §8.4.

---

## 5. The prompt-cache cost model

### 5.1 Caching is not a separate concern

Compaction and caching share one object — the token prefix — and pull against each other, because compaction is the only operation in the system that mutates the prefix at scale. Compaction minimizes tokens in the window; caching minimizes price per token.

Prompt caching is **exact-prefix-match**. An edit at position `p` invalidates everything from `p` onward. With read multiplier `r` and write multiplier `w`:

```
rebuild cost = w · (n − p)
forfeited discount = (1 − r) · (n − p)
```

Standard documented multipliers are `r = 0.1`, `w = 1.25` — **verify against current pricing before tuning**, since the ratio drives several thresholds below.

**That verification was performed on 2026-08-23, and it changed one of the two numbers into a pair.**
`r = 0.1` is confirmed. `w` is **not a scalar** — it depends on which TTL the entry is written at:

| Cache TTL | Write multiplier `w` | `w/r` |
|---|---|---|
| 5 minutes | 1.25 | 12.5 |
| **1 hour** | **2.0** | **20** |

> "Cache read tokens are 0.1 times the base input tokens price"
> "5-minute cache write tokens are 1.25 times the base input tokens price"
> "1-hour cache write tokens are 2 times the base input tokens price"

Every threshold below that spells `w` therefore takes a **(TTL, `w`) pair**, never a bare number,
and the `≈ 12.5` that appears in §5.6 and Appendix A is the five-minute figure. Which pair is in
force is a property of the running session rather than of this document — see §5.4.

The instruction in bold above stands for the next reader. It is not discharged once; it is
discharged per revision, and the mechanism for doing so without editing this document is D11: `r`,
`w` and the TTL are configuration keys under a lint gate that fails the build on the literal, so a
price change moves a config value and a report, not this text.

### 5.2 The trap

**Most content-selection algorithms produce arbitrary subsets, and an arbitrary subset of a prefix-cached sequence is a worst-case edit.**

Slicing, submodular greedy, and Δ-scoring all pick a scattered keep-set. If the earliest dropped element sits at position 12,000 of 167,000, you have selected beautifully and paid to rewrite 155,000 tokens.

The governing quantity is the **earliest edit position**, not how much was dropped:

```
cost = w · (n − p_min)     where p_min = position of the earliest dropped block
```

| Scenario | Dropped | p_min | Rewrite | Verdict |
|---|---|---|---|---|
| A | 60K tokens | 150K | 17K | Good |
| B | 2K tokens | 10K | 157K | 30× the cost, 1/30 the benefit |

Every method in §6 optimizes the numerator and is blind to the denominator unless explicitly constrained.

This is precisely why the codebase splits MicroCompact into cold-cache and warm-cache paths, and why `partialCompactConversation` has the `'from'`/`'up_to'` asymmetry. Those are not quirks; they are the only two shapes the cache permits.

### 5.3 The corrected objective

```
minimize   Σ tokens_kept · r          (steady-state read cost)
         + w · (n − p_min)            (one-time rewrite)
         + c · n                       (the compaction request's own input)
         + λ · D(keep-set)            (task damage, from §4.2)

           where c = r    if the prefix is still cached when compaction runs
                 c = 1    if it is not
```

**The third term is not bookkeeping; it is a whole API request.** Compaction sends a separate call
carrying the same system prompt, tools and history as the conversation, with a summarization
instruction appended. It therefore shares the conversation's prefix and hits the same cache:

> "While the cache is warm, that request reads your prefix from the cache, so a mid-session
> `/compact` costs a fraction of what the context size suggests."
> "After a break longer than the cache lifetime, there is no cache left to read, so the
> summarization request reprocesses the full history as uncached input."

`c` is **independent of `p`**, so it cannot reorder the candidates and §5.4's argmax is unaffected.
What it changes is *when to fire at all*: `(1 − r) · n` separates a compaction run against a live
prefix from the same compaction run against a dead one — at `n = 150 000` and `r = 0.1`, **135 000
base-input-token-equivalents**, scaling linearly with `n` and so largest exactly when compaction
matters most.

One inference that looks right and is not: this does *not* make it wrong to compact once the cache
has already gone cold. At that point the comparison is `n + w·s` for compacting against `w·n` plus a
permanently larger steady state for carrying on, and with `s ≪ n` and `w > 1` compacting is the
cheaper branch. The scheduling consequence is the opposite and is drawn in §5.4: fire *before*
expiry, not after.

A fourth cost is real but unmodelled here. Since Claude Code v2.1.198 the summarization request
inherits the session's extended-thinking configuration, so with thinking enabled it also emits
thinking output tokens. Those are output-priced and their volume is not published, so the term above
counts input only and **under-states** the cold case rather than over-stating it. §8.4 records how
the scheduler carries the estimate.

The right question is never "should I drop this block." It is:

> **Given that I am already paying to rewrite from `p`, what else should I drop from there?**

That flips the algorithm into two stages:

1. **Choose `p`** — a scalar optimization over a small candidate set.
2. **Run expensive selection only on the suffix after `p`**, where it costs nothing extra.

Slicing, submodular greedy, and Δ-scoring all become cache-safe when confined to the region being rewritten anyway.

### 5.4 Choosing p

Three structural facts make this cheap:

- **Monotonicity.** Reclaimable tokens are non-increasing in `p`; cost is increasing. The objective is unimodal in the typical case, so a single pass finds the knee.
- **Small candidate set.** Do not evaluate all `n` positions — only changepoint boundaries from §6.6. Twenty candidates, not 167,000. A cut at a task boundary has low distortion *and* tends to follow a long stable prefix.
- **TTL bimodality — with a correction.** If the prefix has expired, `p = 0` is free, so the optimal policy is genuinely bimodal: **edit as late as possible, or edit when the cache is cold and rebuild everything.** The expensive region is the middle. But the TTL is *sliding*, not fixed — it refreshes on every cache hit, so an actively-used prefix never expires on its own. Cold-cache windows therefore occur only during **idle gaps**: user think-time, meetings, overnight. "Wait for expiry" operationally means "detect idle," which is precisely the signal Claude Code's own time-based MicroCompact path already keys on.
- **The TTL is a regime, not a constant, and the session does not announce it.** The API offers two, and Claude Code chooses between them from how you authenticate: the **one-hour** TTL automatically on a Claude subscription, five minutes on an API key, Bedrock, Google Cloud's Agent Platform, Microsoft Foundry or Claude Platform on AWS. `ENABLE_PROMPT_CACHING_1H=1` opts in; `FORCE_PROMPT_CACHING_5M=1` overrides "regardless of authentication"; `DISABLE_PROMPT_CACHING[_MODEL]=1` turns caching off altogether, which sets `r = w = 1` and collapses this entire section to "every token costs one token". **Subagents use the five-minute TTL even on a subscription.** A plugin can read those variables but cannot read the authentication mode, so when none is set the honest answer is a *range* — and the two errors are not symmetric. Treating a warm prefix as cold spends `w · (n − p)` on a rewrite that was not needed; treating a cold prefix as warm merely forfeits a free deep cut. Under uncertainty, assume the **longer** TTL and the **dearer** `w`.
- **The clock starts at the request, not at the response.** "The lifetime is measured from the start of the request that writes or reads the cache entry, not from the end of its response. Time spent generating a response counts against the lifetime." An idle model anchored on the *end* of the last turn therefore over-reports warmth by the whole generation time — minutes, on a long agentic turn. Anchor on the last event that precedes a request (a tool result, or the user's prompt), never on the turn's end.
- **Time is not the only way a prefix dies.** The cache key includes the **model**, the **effort level**, and the fast-mode request header; changing any of the three empties the prefix instantly, at a moment every wall-clock heuristic reads as maximally warm. That is the bimodal policy's second branch arriving on demand — and §12 records which of the three a plugin can actually observe.

The strategic consequence:

> **Compaction should be scheduled against cache state, not only against token count.** A deep cut during an idle gap that outlasts the TTL is nearly free — the rewrite was going to happen on the next message regardless. The same cut mid-burst, one minute after a fresh cache write, forfeits every discounted read the warm prefix would have served. Idle-gap detection is therefore a first-class scheduler input (§8.4), and the Young–Daly interval (§6.7) takes it as a term.

**And the best moment is just *before* expiry, not just after it.** §5.3's third term is what makes
this precise, and it points the opposite way from the `p`-choice. The `p`-choice wants a dead
prefix, because then `w · (n − p_min)` is zero for every `p`. The compaction request wants a live
one, because then it pays `r · n` instead of `n`. Those two pull against each other everywhere
except in one band: **the tail of the TTL**, where the prefix is still readable — so the
summarization call is cheap — but has so few discounted reads left to forfeit that giving it up
costs almost nothing.

| Fired at | Compaction request | Warm prefix forfeited | Total at `n` = 150 000, `r` = 0.1 |
|---|---|---|---|
| Tail of the TTL (alive, nearly dead) | `r·n` = 15 000 | almost none — it was about to expire | **15 000** |
| After expiry (dead) | `1.0·n` = 150 000 | none — already gone | **150 000** |

A scheduler that treats the expiring band only as a decay factor on `rewrite(p)` and never as a
trigger cannot fire until the prefix is already dead, and so pays that `(1 − r) · n` premium on
**every** idle-driven compaction, by construction rather than by bad luck. The expiring band is a
trigger. §8.4 carries it.

### 5.5 Cache-compatibility audit of every proposed method

| Method | Cache status | Notes |
|---|---|---|
| Content-defined chunking | **Native** | Changes what is *written in the first place*. Prevention never invalidates. |
| Bloom / CMS / HLL sketches | **Invisible** | Live outside the token stream entirely. Zero cache cost. |
| Tombstone-with-hash | **Safe** | Same-length-or-shorter replacement in the suffix. |
| Sequitur over action log | **Safe** | Folded into the summary at compaction time, not rewritten in place. |
| Changepoint detection | **Enhancing** | Supplies the candidate set for `p`. |
| Progressive / embedded encoding | **Neutral** | Serialization order only. |
| Dynamic slicing | **Conditional** | Legal only inside the suffix after `p`. |
| Submodular greedy | **Conditional** | Same constraint. |
| Δ-scoring | **Conditional** | Same constraint. |

Three invalidations belong on that list and are not methods at all, because they are not edits to
the prefix — they change the **key** it is stored under, so a byte-identical prefix misses:

| Trigger | Cache status | Notes |
|---|---|---|
| Model switch | **Fatal** | "Each model has its own cache." Nothing to optimize; the whole prefix is recomputed |
| Effort-level change | **Fatal** | Same, and it is the one of the three a plugin can observe (§12) |
| Enabling fast mode | **Fatal** | Adds a request header that is part of the key. Costs once per conversation |

They earn their place here because §5.4's bimodal policy treats a cold cache as an *opportunity*:
each of these manufactures one instantly, and a scheduler blind to them will read the cheapest
compaction moment in a session as its most expensive.

`cache_edits` is the one escape hatch: it deletes blocks server-side by `tool_use_id` without touching the cached prefix, breaking the prefix constraint entirely. Where available, arbitrary-subset algorithms become legal. Where not, the prefix constraint binds hard.

### 5.6 Information theory applied to caching itself

> **Scope note.** The first item below — breakpoint placement — is **not plugin-actionable**: Claude Code manages its own `cache_control` markers and a plugin cannot move them. The analysis is retained because it applies verbatim if Qompack is later ported to a first-party harness on the Messages API (§2.8), and because the Belady extension makes it measurable today. It is listed in the §12 "cannot do" inventory.

**Breakpoint placement as optimal stopping.** Given a growing prefix and a limited number of markers, place them to maximize expected reads before invalidation. If the hazard rate of invalidation at each position is estimable, optimal placement puts breakpoints *just before* high-hazard regions, so an edit there does not cost the stable content preceding it. Concretely: one after the system prompt, one after the stable startup block, one after the last compaction summary.

**Segmentation by mutation rate.** The deeper principle, and the same insight as generational garbage collection and column-store ordering:

> **Sort content by expected lifetime, longest-lived first.**

```
position 0  ──────────────────────────────────────────►  position n
[ immutable ][ slow-changing ][ summaries ][ live convo ][ volatile tail ]
  system       CLAUDE.md         compaction    recent        current
  prompt,      memory,           checkpoints   turns         tool results
  tools        unscoped rules
```

Under that ordering, edits concentrate at high `p` by construction and `n − p_min` stays small automatically. Most cache pain in a long session is frequently-mutating content sitting earlier in the prefix than stable content.

**Ski rental for the write decision.** Whether to pay `w` to write a cache entry is rent-or-buy under unknown horizon. Competitive ratio 2 deterministic, `e/(e−1) ≈ 1.58` randomized. Practically: write the cache when expected remaining reads exceed `w/r ≈ 12.5`. Short sessions should not be paying for cache writes at all.

The `≈ 12.5` is the **five-minute** figure (§5.1). At the one-hour TTL the write costs `w = 2.0` and
the threshold is **20**. The competitive-ratio result is unaffected — it is a statement about the
policy, not about the price — but the operating point moves, and it moves in the direction that
matters: a session on the one-hour TTL renting against a 12.5-read threshold buys entries it needs
20 reads to amortize, on exactly the short sessions the last sentence above says should not be
paying for cache writes at all. Read `w` from the resolved regime, never from a default.

**Belady extends to breakpoints.** The retrospective replay harness can compute optimal *breakpoint placement* as well as optimal keep-sets — same clairvoyant setup, different decision variable.

---

## 6. Algorithmic toolkit

Selected by cost-to-implement ÷ gap-closed. Each is cheap, deterministic, and requires no model call.

The organizing observation: **every gap in §3 is a solved problem in a field that is not NLP.** Deduplication storage solved redundant re-reads. Program analysis solved relevance. HPC checkpointing solved when-to-snapshot. Sketching solved permanent memory in constant space. LSM trees solved tiered compaction and already share the vocabulary.

### 6.1 Content-defined chunking + Merkle addressing

**Closes:** bloat, G3.1, G3.2, G2.1

The largest single source of transcript growth is the same file read four times with two lines changed. Fixed-size chunking fails because one inserted line shifts every boundary. Content-defined chunking cuts at boundaries determined by a rolling hash of a sliding window, so an insertion perturbs one chunk and the rest realign.

Hash each chunk, store once, reference by hash. Four reads of a 2,000-line file collapse to one chunk set plus three near-empty reference lists. This is `borg`/`restic`/ZFS. O(n) with a rolling hash — microseconds per megabyte.

The Merkle structure is free once content-addressing, and **it is the retrieval index**: `tool_use_id → root hash → chunk list`. Compaction becomes rehashing pointers rather than deleting content.

### 6.2 Bloom filters and sketches for permanent memory

**Closes:** G6.1, G6.2, G6.3, G2.2

A Bloom filter over canonicalized attempted-approach descriptors costs ~12KB for 10,000 entries at 1% false positive rate. False positives are the *safe* direction: you skip something you might have retried.

The property that matters: **it is immortal.** A fixed-size bit array is not context, so it survives arbitrarily many compactions without degradation. The DPI cascade does not touch it because it was never compressed.

Companion sketches:

| Sketch | Purpose | Size |
|---|---|---|
| Bloom | `already_tried(x)` membership | ~12KB / 10K entries @ 1% FP |
| Count-Min | File-touch frequency (which files are hot) | ~54KB @ ε=0.001, δ=0.01 |
| HyperLogLog | Breadth-of-exploration cardinality | ~2KB, 2.3% error |
| Misra-Gries | Deterministic top-k with no false positives | O(k) |

### 6.3 Grammar compression over the action sequence

**Closes:** loop detection, G6.2, transcript bloat

Sequitur is linear-time and online: it builds a hierarchical grammar enforcing two invariants — no digram appears twice, every rule is used more than once. Feed it the tool-call sequence as a symbol string.

`read → edit → test → fail` repeated eleven times becomes one production plus a count. That is 90%+ reduction on the most repetitive part of the transcript, and — more valuable — **the grammar rule itself is the insight**: a nonterminal with high multiplicity is a loop the agent is stuck in. Thrash detection as a byproduct of compression. Re-Pair gives better ratios offline if streaming is not required.

### 6.4 Dynamic slicing over the dependence DAG

**Closes:** G5.3, replaces "keep the last 5"

The transcript already *is* a dependence graph: `tool_use → tool_result → assistant reasoning → next tool_use`, with file paths and symbols as shared state. Program slicing answers exactly the needed question: given a criterion — current goal, open todo, file being edited — compute the backward slice of everything that could have influenced it.

Everything outside the slice is provably irrelevant *to that criterion*. This is graph reachability: BFS over a few thousand nodes, sub-millisecond. Thin slicing drops control-dependence-only edges for much smaller slices at the cost of soundness — probably the right tradeoff here.

> Recency is a proxy for relevance. Slicing is relevance.

### 6.5 Submodular maximization for budget allocation

**Closes:** G3.3, principled allocation with a guarantee

Once candidate units and a coverage notion exist, "pick the best subset under a token budget" is submodular maximization under a knapsack constraint. Coverage plus redundancy penalty are both monotone submodular, so greedy achieves `(1 − 1/e) ≈ 0.63` of optimal, and lazy greedy exploits diminishing returns to skip most re-evaluations.

This should allocate the post-compact budget instead of "top 5 files, 5K each." Slice membership and Δ-scores become the coverage weights.

### 6.6 Changepoint detection for when to compact

**Closes:** G1.1, G1.5

Bayesian online changepoint detection maintains a distribution over run length since the last changepoint, updated in O(1) amortized with pruning. Run it over cheap features:

- File-path locality (Jaccard over recently-touched paths)
- Tool-type distribution shift
- Lexical cohesion (TextTiling-style)
- Inter-turn time gaps
- Todo-list state transitions

A changepoint is a task boundary. **Compacting at one is nearly free in distortion** because the new segment does not depend on the old segment's detail. Compacting mid-segment is maximally destructive. The fixed 13K buffer has no idea which it is doing.

### 6.7 Optimal stopping / Young–Daly cadence

**Closes:** G1.1, G1.3, G9.1

HPC solved this. Given checkpoint cost `δ` and mean time between failures `M`:

```
optimal interval = √(2 · δ · M)
```

Map `δ` to compaction cost (tokens plus latency; the summarization call at 167K input runs ~15–40s) and `M` to expected time until forced compaction at the current burn rate.

Combined with §6.6 and §5.4, the policy becomes two lines:

> **Compact at the first changepoint after the Young–Daly interval has elapsed, preferring a `p` near cache expiry.**

That dominates a fixed 167K trigger on all three cost terms.

### 6.8 LSM compaction theory

**Closes:** architectural framing, G7.1, G7.6, G3.3

The three tiers *are* a log-structured merge tree — including the shared vocabulary. LSM literature gives the tradeoff space in closed form: leveled vs. tiered merge policies, and the read/write/space amplification frontier.

Two immediate transfers:

- **Bloom filter per level.** LSMs put one on each SSTable so a read can skip levels that cannot contain the key. Put one per compacted segment so the agent knows which segment to expand *without expanding any*.
- **Tombstones with deferred reclamation** rather than eager deletion.

The amplification framing also names the failures precisely: PTL retry and compaction loops are **write amplification**; eager 50K file restoration is **space amplification**.

### 6.9 Progressive / embedded encoding

**Closes:** G4.3, G7.3

Embedded coders order the bitstream by importance so truncation *at any point* yields the best available reconstruction for that budget. Current truncation is positional — skills keep their head, PTL retry drops the oldest rounds, which is where intent lives.

If the checkpoint is written in importance order, every budget cut is automatically near-optimal and PTL recovery stops deleting the task statement first. **This is a serialization-order change, not an algorithm** — one of the cheapest wins available.

### 6.10 Belady's OPT as evaluation ceiling

**Closes:** G8.1, G8.3

Belady's algorithm is clairvoyant and unimplementable online — but logged sessions make it computable *retrospectively*. Replay a session, observe what was actually needed after each compaction, compute the optimal keep-set. That is the upper bound no online policy can beat.

Now every policy has a number: **fraction of OPT achieved.** Without it you are tuning constants by vibes; with it you can tell whether slicing beats recency by 4% or 40%.

### 6.11 Explicitly considered and rejected

| Method | Why not |
|---|---|
| Normalized compression distance | Elegant and zero-model, but MinHash is faster and more interpretable for the same job |
| Random projection / product quantization | Only pays once committed to embeddings |
| Erasure coding the checkpoint | Over-engineering; the failure mode is not corruption |
| Abstract interpretation / Daikon-style invariant inference | Right idea for guaranteeing invariant survival, but bad effort-to-payoff next to simply pinning invariants in a typed slot |
| Suffix arrays / FM-index | Only pays at corpus scale; a hash index is sufficient here |

---

## 7. Plugin architecture

### 7.1 The fundamental constraint

**A plugin cannot replace Claude Code's compaction. It can only surround it.**

This is the single most important design constraint and it shapes everything below. Qompack is a *sidecar*: it observes via hooks, maintains external durable state, and re-injects via `additionalContext`. It never intercepts the summarizer, never modifies the message array directly, and never assumes it can prevent a compaction from happening.

Everything is designed to degrade gracefully. If a hook stops firing, Qompack becomes a passive recorder and the session behaves exactly as it does today.

### 7.2 Layer diagram

```
┌─────────────────────────────────────────────────────────────────┐
│ L7  EVALUATION            replay harness · Belady OPT · CI gate │
├─────────────────────────────────────────────────────────────────┤
│ L6  RETRIEVAL (MCP)       recall · re_read · already_tried      │
│                           timeline · why · dropped              │
├─────────────────────────────────────────────────────────────────┤
│ L5  REHYDRATOR            SessionStart(source=compact)           │
│                           progressive budget fill · drop report  │
├─────────────────────────────────────────────────────────────────┤
│ L4  CHECKPOINTER          PreCompact → immutable versioned       │
│                           artifact, importance-ordered           │
├─────────────────────────────────────────────────────────────────┤
│ L3  SCHEDULER             BOCD changepoints · Young–Daly ·       │
│                           p-selection · TTL awareness            │
├─────────────────────────────────────────────────────────────────┤
│ L2  ANALYZER              slicing · Δ-scoring · submodular ·     │
│                           Sequitur · redundancy detection        │
├─────────────────────────────────────────────────────────────────┤
│ L1  STORE                 CDC chunks · Merkle index · sketches · │
│                           dependence DAG · segment log           │
├─────────────────────────────────────────────────────────────────┤
│ L0  OBSERVER              PostToolUse · UserPromptSubmit ·       │
│                           SessionStart · SessionEnd · Stop       │
└─────────────────────────────────────────────────────────────────┘
```

Data flows up on the write path (L0 → L1 → L2), down on the read path (L3 → L4 → L5 → context). L6 is a lateral affordance available to the agent at any time. L7 is offline.

### 7.3 Hook surface

| Hook | Layer | Responsibility |
|---|---|---|
| `PostToolUse` | L0 | Chunk and store tool results; update DAG, sketches, Sequitur; detect redundancy |
| `UserPromptSubmit` | L0 | Capture user intent **verbatim and immutably** (closes G2.3); update BOCD features |
| `SessionStart` | L0/L5 | Branch on `source`: `startup`/`resume` → load store; `compact` → rehydrate |
| `PreCompact` | L4 | Write immutable checkpoint; emit focus instructions via `custom_instructions` |
| `PostToolUse` (todo/git) | L3 | Task-boundary signals for the scheduler |
| `Stop` / `SubagentStop` | L0 | Capture subagent detail before it is double-compressed (closes G10.1) |
| `SessionEnd` | L1 | Flush, compact the store, write session index |

### 7.4 Directory layout

```
.qompack/                       # gitignored, project-root
├── config.json
├── objects/                       # content-addressed, zstd-compressed
│   └── ab/cd/abcdef…              # sha256, 2-level fanout
├── index/
│   ├── tool_use.jsonl             # tool_use_id → root hash, ts, tool, args
│   ├── files.json                 # path → [(ts, root_hash)] version history
│   └── segments.jsonl             # changepoint-delimited segment log
├── sketches/
│   ├── tried.bloom                # negative knowledge — NEVER regenerated
│   ├── touch.cms                  # file-touch frequency
│   └── explore.hll                # exploration cardinality
├── dag/
│   └── deps.jsonl                 # dependence edges for slicing
├── grammar/
│   └── actions.seq                # Sequitur state over the action log
├── checkpoints/
│   ├── 0001.json                  # immutable, importance-ordered
│   └── 0002.json
├── pins/
│   └── invariants.json            # user- and agent-pinned, never summarized
└── eval/
    ├── replay/                    # counterfactual fork logs
    └── opt/                       # Belady keep-sets
```

**Invariant:** files under `checkpoints/`, `pins/`, and `sketches/tried.bloom` are **append-only or additive**. Nothing in the system rewrites them from a summary. This is the mechanical enforcement of §4.6.

### 7.5 Plugin manifest sketch

```json
{
  "name": "qompack",
  "version": "0.1.0",
  "description": "Cache-aware, retrieval-backed context compaction",
  "hooks": {
    "PostToolUse":       [{ "command": "qompack observe tool" }],
    "UserPromptSubmit":  [{ "command": "qompack observe prompt" }],
    "PreCompact":        [{ "command": "qompack checkpoint", "timeout": 20 }],
    "SessionStart":      [{ "command": "qompack session-start" }],
    "SessionEnd":        [{ "command": "qompack flush" }]
  },
  "mcpServers": {
    "qompack": { "command": "qompack", "args": ["mcp"] }
  },
  "commands": [
    "status", "recall", "pin", "checkpoint", "why", "dropped", "eval"
  ]
}
```

---

## 8. Component specifications

### 8.1 L0 — Observer

**Trigger:** `PostToolUse`, `UserPromptSubmit`, `Stop`, `SubagentStop`

**Responsibilities:**

1. **Chunk and store.** Run FastCDC over the tool result. Suggested parameters for source text: `min = 1KB`, `target = 4KB`, `max = 16KB` — smaller than backup workloads because source files are smaller. Store novel chunks zstd-compressed; record the chunk list.
   **Canonicalize first (O2).** Exact-hash dedup is defeated by volatile substrings: timestamps, ANSI escape codes, PIDs, memory addresses, temp-dir paths, and run durations make every `Bash` and test-runner output unique even when semantically identical. Before chunking, apply per-tool canonicalizers that strip or normalize these (store the canonical form; keep the volatile deltas as a tiny side record if byte-exact recovery matters). For content that still differs after canonicalization, a MinHash signature per result detects near-duplicates — "same test suite, one new failure" — and stores the delta against the prior version instead of the full text. Test and build output is the noisiest content class in a coding session; this is where the dedup ratio is won or lost.
2. **Emit the tombstone.** Replace the eventual cleared marker with an addressable one:
   ```
   [cleared: sha256:a3f2… · 2.4KB · FileRead src/auth.ts · re-expandable]
   ```
   This alone closes G3.2 at near-zero cost.
3. **Redundancy detection.** If the chunk set is a superset or near-duplicate of a prior read of the same path, mark the earlier one `SUPERSEDED` in the DAG. Superseded reads are the first candidates for eviction and should never appear in a summary.
4. **DAG edges.** Record `tool_use → tool_result → assistant_turn → next_tool_use`, plus shared-state edges keyed on file path and symbol name.
5. **Sketch updates.** Feed Count-Min and HyperLogLog. Feed the Bloom filter *only* on explicit negative-knowledge events (§8.3).
6. **Sequitur.** Append the tool symbol to the action grammar; check for high-multiplicity nonterminals and emit a thrash warning.
7. **Verbatim user capture.** Every `UserPromptSubmit` is written immutably to `index/segments.jsonl`. This is the durable version of section 6 of the summary prompt, and unlike section 6 it is never regenerated.
8. **Subagent capture.** On `SubagentStop`, store the subagent's returned summary *and*, where available, its tool-result hashes, so the parent has a retrieval path into detail it never held (G10.1).

**Performance budget:** the hook is on the hot path of every tool call. Target < 15ms p99. FastCDC over 100KB is well under 1ms; the rest is one append and a few hash lookups. Sequitur and BOCD updates are O(1) amortized. If the budget is exceeded, degrade to async queue-and-drain rather than blocking.

### 8.2 L1 — Store

**Content addressing.** SHA-256 over chunk bytes, two-level directory fanout. Deduplication is global across the project, so four reads of one file cost one chunk set.

**File version history.** `index/files.json` maps path → list of `(timestamp, root_hash)`. This gives cheap answers to "what did this file look like when we made that decision," which is the most common thing lost across compaction.

**Segment log.** Changepoint-delimited segments, each with: start/end turn, feature summary, encoded-once flag, and checkpoint reference. **The encoded-once flag is the DPI guard** — a segment already encoded into a checkpoint is never re-encoded from that checkpoint. It is re-encoded from the *original chunks* or not at all.

**Garbage collection.** Reference-counted, run on `SessionEnd`. Chunks unreferenced by any checkpoint, pin, or recent index entry beyond a retention window are collected. Default retention: 30 days or 10 sessions, whichever is longer.

### 8.3 L2 — Analyzer

**Negative knowledge extraction.** The plugin must recognise and canonicalize elimination events. Sources, in descending reliability:

1. Explicit agent declaration via the MCP tool `record_eliminated(target, approach, reason)`
2. Slash command `/qompack:pin --eliminated`
3. Heuristic detection: a test-fail → revert → different-approach pattern in the DAG
4. Explicit user statements ("that didn't work," "we tried that")

Canonical descriptor: `(normalized_path, symbol_or_null, approach_class, reason_hash)`. Insert into `tried.bloom`. This is the highest-value single feature in the plugin, and it is roughly 50 lines.

**Staleness — negative knowledge must be able to expire.** An elimination is a *conditional* fact: "widening the pool timeout doesn't work **given pgbouncer 1.18**." Upgrade pgbouncer and the elimination is void — and a false `already_tried` that blocks a now-viable approach inverts the feature from asset to liability. Standard Bloom filters cannot delete, so the design is:

1. **The structured `eliminated[]` records are the source of truth; the Bloom filter is only a cache over them.** This was implicitly true; it is now load-bearing.
2. Every elimination carries `depends_on`: the hashes of the files or configs the elimination's reason rests on (lockfiles, compose files, the file under test). The Observer already tracks file-version history, so detecting a change to any dependency is a hash comparison it performs anyway.
3. When a dependency hash changes, the elimination flips to `status: "stale"`. On the next idle window, `tried.bloom` is **rebuilt from active records only** — cheap, because rebuild is a linear pass over a few thousand structured entries.
4. `already_tried` responses distinguish the cases: *active* returns the reason; *stale* returns "previously eliminated, but the evidence has changed since — re-verification may be warranted," which is strictly more useful to the agent than either a block or silence.
5. `scope: "session" | "project"` controls cross-session carry-over: session-scoped eliminations ("this test is flaky today") die with the session; project-scoped ones ("this library fundamentally can't do X") persist and warm-start future sessions (§10 Phase 7).

The earlier claim that Bloom false positives are "the safe direction" is hereby scoped: it holds only while evidence is current. Staleness handling is what keeps it true.

**Δ-scoring.** Retrospective proxy: for each candidate block, measure how much of the *observed* subsequent content is predictable without it. Implementation options in ascending cost:

- Cheap: token-overlap and symbol-reference counting between block and continuation
- Medium: a small local model computing conditional perplexity, Selective Context style
- Expensive: leave-one-out with the session model (do not do this online)

Start cheap. The replay harness will say whether the expensive version is worth it.

**Slicing.** Backward slice from the criterion set: current todo items, files under edit, the active plan, the most recent user intent. Thin-slicing variant by default. Output: a relevance score per node, not a binary keep/drop — the score feeds submodular selection.

**Submodular selection.** Objective: `coverage(S) − λ · redundancy(S)` under a token knapsack. Lazy greedy. Coverage weights come from slice membership and Δ-scores. **Constrained to the suffix after `p`** (§5.3) — this is enforced in the selector's constructor, not left to the caller.

### 8.4 L3 — Scheduler

The scheduler answers two questions: **when** to compact and **where** to cut.

**When — the composite trigger:**

```
should_compact  =  tokens > soft_floor
                AND ( at_changepoint
                      OR elapsed > young_daly_interval
                      OR tokens > hard_ceiling
                      OR idle_gap > ttl_max            # cache provably cold → cut is free
                      OR ( regime_known                # §5.4: fire BEFORE expiry — the
                           AND idle_gap > 0.8 · ttl )  # summarization call still reads cache
                      OR effort_changed )              # §5.4: the key changed; prefix is gone
```

- `soft_floor` — well below the auto-compact threshold; default 55% of effective window, so the plugin acts before Claude Code's own trigger and the expensive path stays a fallback
- `young_daly_interval = √(2 · δ · M)` where `δ` is measured compaction cost and `M` is expected time to forced compaction at the current burn rate
- `hard_ceiling` — one turn's worth of headroom below Claude Code's threshold, so the plugin always gets to checkpoint first (closes G1.2 as far as a plugin can)

**Where — p-selection:**

```
candidates = changepoint boundaries ∩ API-round boundaries
for p in candidates:
    reclaimable(p) = Σ tokens of droppable blocks after p
    rewrite(p)     = w · (n − p)          # 0 if cache cold or expiring
    distortion(p)  = λ · segment_coupling(p)
    score(p)       = reclaimable(p)·r − rewrite(p) − distortion(p)
choose argmax
```

`segment_coupling(p)` is the count of DAG edges crossing `p` — a direct, cheap measure of how much the post-`p` region depends on pre-`p` detail. Cutting where coupling is low is the operational meaning of "compact at a task boundary."

**Cache-state awareness (corrected for sliding TTL).** The TTL refreshes on every hit, so the prefix does not expire during active use — only during idle gaps. The scheduler therefore tracks *time since last API call*, not time since last cache write. When an idle gap exceeds the TTL (cache provably cold), `rewrite(p) → 0` for all `p` and the scheduler prefers a deep cut it would refuse mid-burst. When the user has been idle long enough that expiry is imminent, the marginal cost of forfeiting the warm prefix approaches zero and the same preference applies. This implements the corrected bimodality in §5.4 and reuses the exact idle signal that gates Claude Code's own time-based MicroCompact.

**Four refinements the §5 cost model forces, each of which is silent when omitted.**

1. **Resolve the cache regime; do not assume one.** The TTL is 5 minutes or 1 hour depending on
   authentication (§5.4), and `w` moves with it. The scheduler resolves a `(ttl_min, ttl_max, r, w)`
   tuple from `ENABLE_PROMPT_CACHING_1H` / `FORCE_PROMPT_CACHING_5M` / `DISABLE_PROMPT_CACHING*` and
   reports **unknown** when none is set, rather than treating a default as a measurement. The two
   thresholds then key on the two *different* bounds: warm→expiring at `0.5 · ttl_min`,
   expiring→cold at `ttl_max`. Only then does "cache provably cold" above mean *provably*. Assuming
   a 5-minute TTL on a session that has an hour classifies a live prefix as free to rewrite, and
   nothing fails when it does — the cut is legal and the bill arrives later.
2. **Anchor the idle clock on the request, not the turn.** The TTL runs from the *start* of the
   request (§5.4); a clock anchored on the turn's end over-reports warmth by the whole generation
   time. Use the last event preceding a request — a tool result, or the user's prompt.
3. **Fire in the expiring band.** §5.4's table: firing while the prefix is still readable costs
   `(1 − r) · n` less than the same compaction after it dies. The band is a trigger, gated on a
   *known* regime — under an unknown regime it spans minutes to an hour and would fire on ordinary
   between-turn pauses.
4. **Resolve the window, not just the threshold.** §2.5's four further variables
   (`CLAUDE_CODE_MAX_CONTEXT_TOKENS`, `CLAUDE_CODE_DISABLE_1M_CONTEXT`,
   `CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT`, `DISABLE_COMPACT`) each change the
   window Claude Code is working to. A scheduler that reads only `CLAUDE_CODE_AUTO_COMPACT_WINDOW`
   sizes `soft_floor` and `hard_ceiling` against a window nobody is using, and under `DISABLE_COMPACT`
   there is no host trigger to stay ahead of at all — every clause above becomes advisory and must
   say so rather than promising a headroom it no longer controls.

**What the model deliberately does not price.** The compaction request's *output* — including the
thinking tokens it inherits from the session (§2.7) — is real cost and is not in `score(p)`. It is
`p`-independent, so it cannot change the cut; and its volume is unpublished, so modelling it would
substitute a guess for a measurement. The scheduler records the compaction's measured wall-clock and
token cost as `δ` for Young–Daly, which is where that cost belongs: an empirical term fed by
observation rather than an analytic one fed by assumption.

**Idle-time background work (O3).** User think-time is free compute. During detected idle, the scheduler advances the shadow checkpoint incrementally, runs store GC, precomputes backward slices from the current criterion set, and refreshes Δ-scores — so that when compaction does fire, the expensive analysis is already done and `PreCompact` only finalizes. This is also the natural moment to *perform* a deep cut: the cache is dying anyway and no user is waiting on latency.

### 8.5 L4 — Checkpointer

**Trigger:** `PreCompact` (and independently on the scheduler's own cadence, so checkpoints exist even when compaction does not fire).

**Output:** an immutable, versioned, **importance-ordered** JSON artifact. Ordering is the embedded-coding principle from §6.9 — truncation at any point yields the best available reconstruction for that budget.

```jsonc
{
  "version": 1,
  "session": "…",
  "seq": 7,
  "created": "…",
  "parent": "0006.json",
  "encoded_segments": [12, 13, 14],     // DPI guard: from originals only

  // ── Tier 1: never truncated ──────────────────────────────
  "invariants": [ … ],                   // pinned, verbatim
  "user_intent": {
    "original": "…",                     // verbatim, from L0, never regenerated
    "evolution": [ … ]                   // verbatim deltas
  },
  "eliminated": [                        // negative knowledge, structured
    { "target": "src/auth.ts:refreshToken",
      "approach": "widen pool timeout",
      "reason": "pgbouncer 1.18 ignores it in transaction mode",
      "evidence": "sha256:…",
      "depends_on": [                     // staleness guard (§8.3):
        { "path": "docker-compose.yml", "hash": "sha256:…" },
        { "path": "package-lock.json",  "hash": "sha256:…" }
      ],
      "scope": "project",                 // "session" | "project"
      "status": "active" }                // "active" | "stale"
  ],

  // ── Tier 2: truncate late ────────────────────────────────
  "decisions": [
    { "what": "…", "why": "…", "alternatives_rejected": [ … ],
      "evidence": "sha256:…" }
  ],
  "open_questions": [ … ],
  "current_work": { "goal": "…", "next_step": "…", "blocked_on": null },

  // ── Tier 3: truncate first ───────────────────────────────
  "pointers": {
    "files":  [ { "path": "…", "hash": "sha256:…", "why": "…" } ],
    "tools":  [ { "tool_use_id": "…", "hash": "sha256:…", "summary": "…" } ]
  },
  "narrative": "…",                      // prose residue, last resort

  // ── Metadata ─────────────────────────────────────────────
  "sketch_refs": { "tried": "tried.bloom", "touch": "touch.cms" },
  "dropped": [ { "kind": "path_rule", "id": "api-conventions.md" } ],
  "cache": { "p_chosen": 148230, "rewrite_tokens": 18770, "ttl_state": "warm" }
}
```

Note what is **not** here: no code snippets. Files are pointers with a one-line reason. This is §4.4 applied directly, and it is where most of the 50K eager-restore budget is reclaimed.

**Focus instruction emission.** `PreCompact` can supply `custom_instructions`. Qompack generates these from the checkpoint rather than leaving them to the user, and the standing instruction template implements §4.5:

> Encode what a competent engineer with no session history would get wrong. Do not restate file contents, directory structure, or command output — those are retrievable. Prioritise: intent, decisions and their rationale, approaches eliminated and why, and constraints discovered empirically.

**Incremental summarization via focus instructions (O1).** Because a durable checkpoint already covers everything through turn `N`, the focus instructions also narrow the summarizer's *span*:

> A durable checkpoint (`.qompack/checkpoints/0007.json`) fully covers the session through turn N, including all decisions, eliminations, and file state up to that point. Do not re-summarize that material. Summarize only what happened after turn N: new decisions, new eliminations, new intent, current work.

This is the plugin-legal analogue of Session Memory Compact, and it attacks three problems at once. The most expensive call in the session (G7.1) shrinks, because the model is asked to compress a fraction of the transcript rather than all of it. DPI exposure drops, because segments already encoded are never re-summarized — the instruction *operationalizes* the never-compress-a-compression invariant inside Claude Code's own pipeline, where the plugin otherwise has no reach. And summary quality rises, because the summarizer's attention is concentrated on the recent, relevant span instead of diluted across 167K tokens (partially mitigating G8.2). The one caveat: `custom_instructions` is advisory — the summarizer may ignore the span restriction — so the checkpoint remains the authoritative record and the rehydrator never depends on the summary having complied.

**Regeneration rule (closes GC).** Checkpoints are **always generated from the store** — the chunk objects, segment log, verbatim intent captures, and structured records — never from content currently sitting in the context window. This matters because the rehydrator's own prior injection lives in message history and will be mangled by the next Claude Code summarization pass; a checkpoint that trusted the surviving in-context version would be compressing a compression through the back door. The rehydrator tags every injection with its checkpoint sequence number precisely so the next checkpoint pass can identify and ignore that material as a source.

**Amortized compaction — the latency architecture (O5).** The incremental-writing rule above is not only a defense against the `PreCompact` timeout; taken seriously, it changes the asymptotics of compaction latency. Where the wall-clock goes in a stock long-session compact: prefill is mostly cache reads (thanks to the cache-key reuse in §1.2), so **decode dominates** — several thousand output tokens of 9-section summary plus scratchpad, generated autoregressively — with extended thinking stacked on top when the session has it enabled, and a slow first post-compact turn from re-prefilling up to 75K of eagerly restored context.

The fix is to keep the checkpoint frontier moving continuously: every time a segment closes (changepoint, todo completion, passing test run), encode it into the checkpoint during the next idle moment, advancing frontier `N` all session long. When compaction fires, the O1 instruction restricts the summarizer to turns after `N` — and that residual span is now 10–20K tokens rather than 150K, **regardless of how long the session has run**. Short novel prefill, short decode, every time.

This is precisely the stop-the-world vs. incremental garbage collection distinction. Stock compaction is a stop-the-world collector: pause, walk the entire heap, resume — pause time proportional to session size. Frontier advancement is an incremental collector: small units of encoding work interleaved with real work, so no pause is ever O(session). Per-compaction cost drops from **O(session) to O(delta)** — amortized O(1) per turn — and the plan's other latency levers compound with it: the no-snippets rule (§4.4) cuts decode length again (a pointer is ~20 tokens where a snippet is ~500), and the 8–12K rehydration budget (§8.6) shrinks the slow first turn after. What remains outside plugin reach: the summarizer's model speed, its inherited thinking configuration, and any map-reduce parallelization of the summary itself — those are harness-port material (§2.8).

### 8.6 L5 — Rehydrator

**Trigger:** `SessionStart` with `source == "compact"`.

**Emits** via `hookSpecificOutput.additionalContext`, filled in importance order until the budget is reached:

1. **Invariants and pins** — verbatim, always
2. **Verbatim original user intent** — from L0, not from any summary (closes G2.3 permanently)
3. **Eliminated-approaches digest** — the top-N most relevant by slice score, plus a note that `already_tried()` covers the rest
4. **Decisions with rationale**
5. **Current work and next step**
6. **Pointers, not contents** — file paths with hashes and one-line reasons
7. **Drop report** — the explicit list of path-scoped rules, nested `CLAUDE.md` files, and truncated skills that are no longer in context (closes G4.5)
8. **Retrieval affordance notice** — one line telling the agent that `recall`, `re_read`, and `already_tried` exist

**Instruction restoration (G4.1, G4.2, G4.4).** The rehydrator re-reads from disk, independently of Claude Code's own restoration:

- Every `paths:`-scoped rule whose glob matches any file in the checkpoint's pointer set
- Every nested `CLAUDE.md` in a directory containing — **or ancestor to** — a pointer-set file, stopping before the project root, which Claude Code re-injects itself (§2.7)
- A compact skill index (names and one-line descriptions only, ~450 tokens) so skill awareness returns

**Budget discipline.** Default rehydration budget is deliberately far below Claude Code's 50K + 25K: target 8–12K. The whole point is that pointers plus retrieval replace eager restoration. Measure this in the harness before relaxing it.

### 8.7 L6 — Retrieval tools (MCP)

The affordance layer. Without it, the store is a write-only log.

| Tool | Signature | Purpose |
|---|---|---|
| `recall` | `(query, k=5)` → hits | Search the store by content, path, or symbol; returns hashes and summaries |
| `expand` | `(hash \| tool_use_id)` → content | Re-materialize a cleared tool result |
| `re_read` | `(path, at=null)` → content | Current or historical version of a file |
| `already_tried` | `(target, approach)` → bool + reason | Bloom membership plus the stored reason when present |
| `record_eliminated` | `(target, approach, reason)` | Write negative knowledge |
| `timeline` | `(from, to)` → segments | What happened between two points |
| `why` | `(decision_id)` → rationale | Retrieve a decision and its evidence |
| `dropped` | `()` → list | What is currently out of context |

**Design note:** `already_tried` should be surfaced in the rehydrated context as a *standing instruction*, not merely an available tool. "Before committing to an approach, call `already_tried`." Otherwise the affordance exists and goes unused.

**Retrieved content is born ephemeral (closes GB).** There is a self-defeating loop hiding here: every `expand` and `re_read` returns content *into the context window*, where it accumulates like any tool result — unpoliced, the retrieval layer recreates the exact bloat it exists to solve. The fix follows from the store's own guarantee: retrieved content is already indexed, so re-clearing it is free and lossless — it can always be retrieved again for the price of a tool call. Therefore:

- Every retrieval result is tagged ephemeral at birth and becomes the **first** eviction candidate, ahead of ordinary tool results, in the plugin's droppable-block ranking (§8.4).
- Retrieval tools return the **minimum sufficient span** by default — the matching function or hunk, not the file — with an explicit `full=true` escape hatch. Most post-compaction questions are "what did that one function look like," not "give me the file."
- Repeated expansion of the same hash within a session is a signal, not a cost: the Analyzer promotes frequently-re-expanded content into the next checkpoint's pointer tier with a higher slice weight, so the system *learns* what eager restoration should have included — a demand-driven correction to the 8–12K rehydration budget.

### 8.8 L7 — Evaluation harness

Offline, not part of the hot path. See §11.

---

## 9. Gap traceability matrix

| Gap | Closed by | Residual |
|---|---|---|
| G1.1 task-blind trigger | L3 BOCD changepoints | Cannot block Claude Code's own trigger |
| G1.2 no agent agency | L3 soft floor + hard ceiling; `record_eliminated` | Partial — plugin acts first, cannot veto |
| G1.3 fixed buffer | L3 adaptive Young–Daly interval | — |
| G1.4 blocking-limit cliff | L3 soft floor keeps sessions far from the cliff | Cannot fix the cliff itself |
| G1.5 no boundary signals | L0 todo/git/test signals → L3 | — |
| G2.1 recursive compression | §7.4 append-only invariant; `encoded_segments` guard | — |
| G2.2 nothing pinned | `pins/invariants.json` + sketches | — |
| G2.3 user messages regenerated | L0 verbatim capture, L5 verbatim replay | — |
| G2.4 prose not typed state | L4 typed checkpoint schema | Claude Code's own summary stays prose |
| G2.5 no ground-truth check | L4 validates pointer set against `git status` | — |
| G2.6 session-memory drift | Qompack checkpoint is the durable source | — |
| G3.1 unreachable transcript | L1 store + L6 `expand`/`recall` | — |
| G3.2 pointerless tombstones | L0 addressable tombstone | — |
| G3.3 eager restoration | L5 pointer-first, 8–12K budget | — |
| G3.4 snippets in summary | L4 focus instructions forbid them | Advisory to the summarizer, not enforced |
| G4.1 path rules lost | L5 re-reads matching rules | — |
| G4.2 nested CLAUDE.md lost | L5 re-reads by pointer directory | — |
| G4.3 skill head-truncation | L4 importance ordering; L5 skill index | Cannot change Claude Code's own truncation |
| G4.4 skill index gone | L5 compact index re-injection | — |
| G4.5 no drop report | L5 explicit drop report | — |
| G5.1 single cut | L3 multi-segment log | Partial — cannot perform multi-cut on the live array |
| G5.2 wrong direction cheap | L3 p-selection with cache term | — |
| G5.3 homogeneous treatment | L4 three-tier typed schema | — |
| G6.1 no slot for eliminations | L4 `eliminated[]` + Bloom | — |
| G6.2 re-attempt loop | L6 `already_tried` standing instruction | — |
| G6.3 highest-Δ content dropped | L2 Δ-scoring prioritises it | — |
| G7.1 expensive call | L3 earlier, cheaper, better-targeted compaction | Cannot avoid the call entirely |
| G7.2 no cheap summarizer | — | **Not closeable from a plugin** |
| G7.3 PTL drops intent | L5 restores verbatim intent regardless | Cannot change PTL logic |
| G7.4 circuit breaker | L4 checkpoints mean the session survives a give-up | — |
| G7.5 tool-call-instead-of-summary | L4/L5 checkpoint is the fallback path | Cannot prevent the failure |
| G7.6 loops / >100% jam | L3 keeps sessions far from the failure region | Cannot fix the underlying bug |
| G8.1 no observability | L7 replay harness; `/qompack:status` | — |
| G8.2 degraded summarizer | L3 compacts earlier, at lower context | Partial — the model is still the model |
| G8.3 no feedback | L7 fraction-of-OPT metric | — |
| G9.1 not durable | L4 immutable versioned checkpoints | — |
| G9.2 hand-rebuilt layer | This plugin *is* the layer, packaged | — |
| G9.3 undocumented contracts | §12 contract monitor with fail-loud detection | Risk remains, but becomes visible |
| G10.1 subagent double-compression | L0 `SubagentStop` capture | — |
| G10.2 coarse token estimation | L1 exact chunk-level accounting | Claude Code's own estimate unchanged |

**Not closeable from a plugin:** G7.2 (cheap summarizer model) and the underlying bugs behind G1.4 and G7.6. Those require upstream changes and should be filed as issues, not worked around.

---

## 10. Phased build plan

Ordered strictly by payoff per line of code, with the cache-safety corrections from §5 applied — note that slicing and submodular selection are deliberately placed **after** p-selection, because shipping them first would be a regression.

### Phase 0 — Measurement (do this first)

**Why first:** you cannot tune anything without it, and every subsequent phase needs a regression signal.

- Session replay harness: fork logged sessions at compaction points
- Counterfactual divergence metrics (§4.2)
- Belady OPT computation over logged sessions (§6.10)
- Baseline the current system: what fraction of OPT does stock compaction achieve?

**Exit criterion:** a single number for stock behaviour, reproducible across at least 20 real sessions.

### Phase 1 — Store and observer

- FastCDC chunker, content-addressed object store, zstd
- Per-tool output canonicalizers (timestamps, ANSI, PIDs, addresses) + MinHash near-dedup (O2)
- `PostToolUse` and `UserPromptSubmit` hooks
- Merkle index, file version history
- Addressable tombstones (G3.2)
- Redundancy / supersession detection

**Exit criterion:** store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own); hook p99 < 15ms.

### Phase 2 — Negative knowledge

- Bloom filter, canonical descriptor scheme
- Evidence-linked eliminations (`depends_on` hashes), staleness flip, rebuild-from-records (§8.3)
- `record_eliminated` and `already_tried` MCP tools, including the three-way active/stale/absent response
- Standing instruction in rehydrated context
- Count-Min and HLL companions

**Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents (a viable approach wrongly refused) on sessions that include a dependency change. The core is still ~50 lines; staleness roughly doubles it and is non-negotiable for correctness.

### Phase 3 — Checkpoint and rehydrate

- `PreCompact` checkpoint writer with importance ordering
- `SessionStart(source=compact)` rehydrator with injection tagging (§8.5 regeneration rule)
- Drop report; path-rule and nested-`CLAUDE.md` restoration; skill index
- Focus-instruction generation, **including the incremental-span instruction (O1)** — this rides along for free once the checkpoint exists and directly shrinks the most expensive call in the session

**Exit criterion:** post-compaction first-divergence turn index improves against Phase 0 baseline at a *smaller* rehydration budget than stock; measured reduction in summarization-call input tokens when the span instruction is honoured.

### Phase 4 — Scheduler

- BOCD over cheap features
- Young–Daly cadence with measured δ
- p-selection with the cache term
- Idle-gap detection (sliding-TTL model, §8.4) and idle-time background work (O3)
- **Continuous checkpoint frontier advancement (O5)** — encode closed segments during idle so the residual span, and therefore the compaction pause, stays O(delta)

**Exit criterion:** measured reduction in total rewrite tokens per session, with no regression in divergence metrics; median compaction pause and residual span flat as session length grows (the amortization claim, tested directly).

### Phase 5 — Selection

- Dependence DAG construction
- Thin slicing
- Δ-scoring (start with the cheap proxy)
- Submodular greedy, **constrained to the suffix after p**

**Exit criterion:** improved fraction-of-OPT at equal budget.

### Phase 6 — Grammar and loop detection

- Sequitur over the action log
- High-multiplicity nonterminal → thrash warning
- Grammar-compressed action history in the checkpoint

**Exit criterion:** thrash detected before the user notices it, on replay.

### Phase 7 — Refinement

- **Cross-session warm start (O4).** The store outlives the session; use it. Warm-start Count-Min with the project's historical hot-file distribution, carry `scope: "project"` eliminations forward, and seed the changepoint model's feature priors from past sessions. First-compaction quality in a fresh session should benefit from every session before it.
- Demand-driven rehydration tuning: promote frequently-re-expanded hashes (§8.7) into the checkpoint pointer tier
- Per-segment Bloom filters, LSM-style (§6.8)
- Ski-rental cache-write policy
- Progressive checkpoint truncation tuning
- Prefix reordering by mutation rate (§5.6) — harness/API-port only, per §12

---

## 11. Evaluation methodology

### 11.1 Primary metric

**Fraction of Belady OPT.** For each compaction event in a logged session, compute the clairvoyant optimal keep-set under the same token budget, then score the policy's actual keep-set against it.

### 11.2 Secondary metrics

| Metric | Definition |
|---|---|
| First-divergence turn | Turns after compaction before compacted and uncompacted branches diverge |
| File-set Jaccard | Overlap of files touched over the next K turns |
| Redundant work rate | Re-reads of already-read files; re-attempts of eliminated approaches |
| Decision preservation | Fraction of pre-compaction decisions still correctly recalled |
| Rewrite tokens/session | Total `w · (n − p_min)` paid |
| Rehydration budget | Tokens spent restoring context |
| Retrieval hit rate | How often `expand`/`re_read` is called, and whether it prevented a re-read |
| **Compaction pause** | Wall-clock of the summarization call; target O(delta) under frontier advancement (O5) |
| **Residual span at compaction** | Tokens between frontier N and the compaction point — the direct driver of pause time |
| **First-turn-after latency** | Time to first token on the turn following compaction (captures rebuild + cache-write cost) |

### 11.3 Guardrails

- Hook p99 latency < 15ms (L0), < 2s (L4)
- Store growth sublinear in session length after dedup
- No metric may regress by more than 2% to improve another without explicit sign-off
- Every phase gate runs the full replay suite

### 11.4 Watch for

- **Overfitting to replay.** Logged sessions were produced by an agent operating under the *current* system. Behaviour changes when the system changes. Re-collect sessions periodically under the new policy.
- **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.

---

## 12. Risk register and honest limitations

| Risk | Severity | Mitigation |
|---|---|---|
| **Undocumented hook contracts change** (G9.3): `SessionStart` `source=compact`, `additionalContext` reaching context, `PreCompact` timing | High | Contract monitor: assert each on every session start, log loudly and degrade to passive recording on failure. Never fail silently. |
| `PreCompact` timeout too short to write a checkpoint | Medium | Write incrementally on the scheduler's cadence so `PreCompact` only finalizes. Never depend on doing all the work in the hook. |
| Storage growth | Medium | Reference-counted GC, retention window, `/qompack:status` surfaces size |
| Hook latency on the hot path | Medium | Async queue-and-drain fallback; hard p99 budget |
| Rehydration crowds out working context | Medium | Hard budget cap; pointer-first design; measured in harness |
| Bloom saturation | Low | Monitor fill ratio; resize with a rebuild from `eliminated[]` in checkpoints |
| Plugin and Claude Code compaction fight each other | Medium | Soft floor well below the auto threshold; plugin acts first by design |
| Cache multipliers change | Low | Read `r` and `w` from config, never hardcode |
| **Stale negative knowledge blocks a now-viable approach** | High | Evidence-linked eliminations with `depends_on` hashes; Bloom rebuilt from active records on dependency change (§8.3). This risk is why the filter is a cache, never the source of truth. |
| Retrieval layer re-inflates the context window | Medium | Ephemeral-at-birth policy; minimum-sufficient-span defaults; retrieval results are first eviction candidates (§8.7) |
| Summarizer ignores the incremental-span instruction | Low | `custom_instructions` is advisory; the checkpoint remains authoritative and the rehydrator never depends on summary compliance (§8.5) |

### What this plugin cannot do

State these plainly rather than discovering them in month three:

- **Cannot use a cheaper model for summarization** (G7.2). Upstream constraint.
- **Cannot prevent a compaction** — only compact earlier and better.
- **Cannot place or move `cache_control` breakpoints.** Claude Code manages its own cache markers, so the breakpoint-placement analysis in §5.6 is measurement-and-port material, not a plugin feature.
- **Cannot access attention weights**, so H2O-style eviction stays out of reach; only perplexity proxies are available.
- **Cannot force the `cache_edits` path** — where it is gated, the prefix constraint binds fully.
- **Cannot change PTL retry, the circuit breaker, or the blocking-limit cliff.** It can only keep sessions away from those regions.
- **Cannot modify the message array directly.** Everything flows through `additionalContext`.
- **Cannot guarantee the summarizer honours focus instructions** — span narrowing (§8.5) and snippet prohibition (G3.4) are advisory; the durable checkpoint is the backstop for both.
- **Cannot tell which cache TTL the session is on** unless the user has set one of the caching environment variables. Claude Code picks five minutes or one hour from the authentication mode, and no hook input carries it. With none set, the scheduler reports a range rather than guessing — setting `ENABLE_PROMPT_CACHING_1H=1` or `FORCE_PROMPT_CACHING_5M=1` is the one-line way a user sharpens every cache-timing decision in §8.4.
- **Cannot detect a mid-session model switch or a fast-mode toggle**, both of which empty the cache instantly (§5.5). `model` reaches hooks on `SessionStart` only and is not guaranteed present; `fast_mode` reaches no hook at all. An effort-level change *is* observable, and is the only one of the three the scheduler can act on.
- **Cannot measure its own cache hit rate.** `cache_read_input_tokens` and `cache_creation_input_tokens` are delivered to a status-line command, and a plugin may ship only `subagentStatusLine` — the main `statusLine` is a user setting. §5 is therefore a *model* of cache cost, never a measurement of it, unless the user opts in by pasting the status-line snippet the user guide provides.
- **Cannot correct the context window Claude Code assumes for a gateway or unrecognized model ID.** `CLAUDE_CODE_MAX_CONTEXT_TOKENS` is the user's to set (§2.5); the scheduler can read it and size against it, but cannot supply it.

### Upstream issues worth filing separately

The following are better fixed in Claude Code than worked around: agent-initiated compaction (already filed), PTL retry dropping oldest-first rather than importance-first, path-scoped rule re-injection, skill index re-injection, and a drop report surface.

Two more, both from the §5 cache work and both cheap upstream: **expose the resolved cache TTL to
hooks**, so a scheduler reasoning about prefix lifetime does not have to infer it from environment
variables the user may never have set; and **carry `cache_read_input_tokens` / `cache_creation_input_tokens`
in hook input** as they already are in the status-line payload, which would turn §5 from a model of
cache cost into a measurement of it. Neither needs a new hook — both are fields on events that
already fire.

---

## Appendix A — mathematical reference

**Information bottleneck**
```
min  I(X;T) − β·I(T;Y)
```

**Task distortion**
```
D = KL( P(a|X) ‖ P(a|T) )
```

**Block value (cross-entropy delta)**
```
Δ(c) = H(future | context \ c) − H(future | context)
```

**Data processing inequality**
```
I(X;T₃) ≤ I(X;T₂) ≤ I(X;T₁)
```

**Cache rewrite cost**
```
cost = w · (n − p_min)
```

**Composite compaction objective**
```
min  Σ tokens_kept·r  +  w·(n − p_min)  +  λ·D(keep-set)
```

**Auto-compact threshold (current system)**
```
effectiveWindow = contextWindow − min(maxOutputTokens, 20_000)
threshold       = effectiveWindow − 13_000
```

**Young–Daly optimal checkpoint interval**
```
I* = √(2·δ·M)
```

**Ski-rental cache-write threshold**
```
write when  E[remaining reads] > w/r   (12.5 at r=0.1, w=1.25 — the 5-minute TTL)
                                      (20   at r=0.1, w=2.0  — the 1-hour   TTL)
```

**Bloom filter sizing**
```
m = −n·ln(p) / (ln 2)²          k = (m/n)·ln 2
n = 10_000, p = 0.01  →  m ≈ 95_850 bits ≈ 12 KB, k = 7
```

**Count-Min sizing**
```
width = ⌈e/ε⌉      depth = ⌈ln(1/δ)⌉
ε = 0.001, δ = 0.01  →  2718 × 5 ≈ 54 KB @ 4-byte counters
```

**Submodular greedy guarantee**
```
f(S_greedy) ≥ (1 − 1/e)·f(S_opt) ≈ 0.63
```

---

## Appendix B — literature index

| Area | Key work |
|---|---|
| Rate–distortion / IB | Tishby, Pereira & Bialek — information bottleneck |
| Prompt compression | Li et al. — Selective Context; Jiang et al. — LLMLingua |
| KV-cache eviction | Zhang et al. — H2O heavy-hitter oracle |
| Content-defined chunking | Rabin (1981); LBFS (2001); Xia et al. — FastCDC (2016) |
| Sketching | Bloom (1970); Cormode & Muthukrishnan — Count-Min; Flajolet et al. — HyperLogLog; Misra & Gries |
| Grammar compression | Nevill-Manning & Witten — Sequitur (1997); Larsson & Moffat — Re-Pair |
| Program slicing | Weiser (1981); Korel & Laski (1988); Sridharan et al. — thin slicing (2007) |
| Submodular summarization | Lin & Bilmes (2011); Nemhauser, Wolsey & Fisher (1978); Minoux — lazy greedy (1978) |
| Changepoint detection | Adams & MacKay — BOCD (2007); Hearst — TextTiling (1997) |
| Checkpointing | Young (1974); Daly (2006) |
| LSM trees | O'Neil et al. (1996); Dayan, Athanassoulis & Idreos — Monkey, Dostoevsky |
| Caching | Belady (1966); Denning — working sets; Megiddo & Modha — ARC |
| Progressive coding | Shapiro — EZW; Said & Pearlman — SPIHT |

---

## Appendix C — configuration schema

```jsonc
{
  "store": {
    "chunk": { "min": 1024, "target": 4096, "max": 16384 },
    "compression": "zstd",
    "retention": { "days": 30, "sessions": 10 },
    "canonicalize": {
      "enabled": true,
      "strip": ["timestamps", "ansi", "pids", "addresses", "tmpPaths", "durations"],
      "minhash": { "enabled": true, "permutations": 128, "nearDupThreshold": 0.9 }
    }
  },
  "scheduler": {
    "softFloorPct": 0.55,
    "hardCeilingMargin": 20000,
    "youngDaly": { "enabled": true, "measuredDeltaSeconds": null },
    "changepoint": { "hazardRate": 0.004, "features": ["paths","tools","time","todos"] },
    // The FIVE-MINUTE regime (§5.1). These values are correct as written and are the floor, not
    // the whole story: at the 1-hour TTL writeMultiplier is 2.0 and ttlSeconds 3600. The running
    // regime is resolved from the environment at runtime (§5.4, §8.4) and is never written back
    // into config, so these keys stay stable and D11's lint gate keeps working.
    "cache": { "readMultiplier": 0.1, "writeMultiplier": 1.25, "ttlSeconds": 300 },
    "idle": { "detectAfterSeconds": 120, "backgroundWork": true, "deepCutWhenCold": true }
  },
  "checkpoint": {
    "budgetTokens": 12000,
    "incrementalSpanInstruction": true,
    "frontier": { "advanceOnSegmentClose": true, "maxResidualTokens": 20000 },
    "tiers": { "never": ["invariants","user_intent","eliminated"],
               "late":  ["decisions","open_questions","current_work"],
               "first": ["pointers","narrative"] }
  },
  "sketches": {
    "bloom": { "capacity": 10000, "fpRate": 0.01 },
    "cms":   { "epsilon": 0.001, "delta": 0.01, "warmStartFromProject": true },
    "hll":   { "registers": 2048 }
  },
  "eliminations": {
    "requireEvidence": true,
    "defaultScope": "session",
    "rebuildOnStale": "nextIdle",
    "staleResponse": "flag"          // "flag" | "drop"
  },
  "retrieval": {
    "ephemeralResults": true,
    "defaultSpan": "minimal",        // "minimal" | "full"
    "promoteAfterExpansions": 2
  },
  "selection": {
    "slicing": "thin",
    "deltaScoring": "cheap",
    "submodular": { "lambda": 0.4, "lazyGreedy": true }
  },
  "eval": { "replayOnPhaseGate": true, "minSessions": 20 }
}
```

---

## Closing note

The four things that actually matter, if the plan has to be cut down:

1. **Phase 0.** Without measurement, everything else is opinion.
2. **Phase 1 + 2.** Content-addressed store with canonicalization and addressable tombstones, plus evidence-linked negative knowledge. Slightly more than the original weekend estimate once staleness is included — and staleness is not optional, because negative knowledge that cannot expire eventually blocks a viable approach and inverts the feature's value.
3. **The cache correction.** Do not ship slicing or submodular selection before p-selection. Selection quality is real, but an arbitrary subset of a cached prefix is a worst-case edit, and shipping it first would make the system measurably more expensive while looking smarter.
4. **The incremental-span instruction.** Once checkpoints exist, one paragraph of `custom_instructions` shrinks the most expensive call in the session and operationalizes never-compress-a-compression inside a pipeline the plugin otherwise cannot touch. It is the cheapest line in the entire plan relative to what it buys.

Everything past Phase 5 is refinement on a system that already works.

---

## Revision log

**v1.1** — Corrections and additions from a full-plan review:
- **E1**: sliding-TTL correction — cache expiry only occurs in idle gaps; idle detection promoted to first-class scheduler input (§5.4, §8.4)
- **E2**: `cache_control` breakpoint placement rescoped as harness/API-port material, not plugin-actionable (§5.6, §12)
- **GA**: elimination staleness — evidence-linked `depends_on` hashes, active/stale status, Bloom rebuilt from records (§8.3, schema, Phase 2)
- **GB**: retrieval re-inflation — ephemeral-at-birth results, minimal-span defaults, demand-driven promotion (§8.7)
- **GC**: checkpoints regenerate from the store only, never from surviving in-context injections; injections tagged (§8.5)
- **O1**: incremental summarization via span-narrowing focus instructions (§8.5, Phase 3, closing note)
- **O2**: per-tool output canonicalization + MinHash near-dedup (§8.1, Phase 1)
- **O3**: idle-time background work (§8.4, Phase 4)
- **O4**: cross-session warm start (Phase 7)

**v1.2** — Latency made an explicit objective:
- **O5**: amortized compaction — continuous checkpoint frontier advancement keeps the residual span O(delta), turning the summarization call from a stop-the-world O(session) pause into an incremental-GC-style amortized cost (§8.5, Phase 4)
- Latency budget breakdown added: warm-cache prefill is cheap, **decode dominates**, thinking inheritance and the post-compact rebuild are the other two components (§8.5)
- Latency metrics added to §11.2: compaction pause, residual span, first-turn-after latency; Phase 4 exit criterion now tests the amortization claim directly (pause flat as session length grows)

**v1.3** — §5.1's standing instruction discharged. Every cache figure in this document was checked
against published Anthropic documentation on 2026-08-23; `plans/QOMPACK-ERRATA.md` records what was
confirmed, what changed, what could not be verified, and the sources. The reasoning in §5 survived
intact — what moved were numbers it was written to expect to move, and one scheduling consequence
it had drawn only half of:
- **`w` is a pair, not a scalar** — 1.25 at the 5-minute TTL, **2.0** at the 1-hour. `w/r` is
  therefore 12.5 **or 20**, and every threshold spelling `w` takes a (TTL, `w`) pair (§5.1, §5.6,
  Appendix A). `r = 0.1` confirmed unchanged.
- **The TTL is a regime the session does not announce** — one hour automatically on a Claude
  subscription, five minutes on API-key and third-party auth, overridable by three environment
  variables, and five minutes for subagents regardless. Under an unknown regime, assume the longer
  TTL and dearer `w`: the two errors are not symmetric (§5.4, §8.4).
- **The TTL clock starts at the request, not the response** — generation time counts against it, so
  an idle model anchored on a turn's end over-reports warmth by the whole generation (§5.4, §8.4).
- **Compaction is itself a priced request** — `r·n` against a live prefix, `n` against a dead one.
  §5.3's objective gains the `c·n` term. It is `p`-independent, so the argmax is unchanged; what it
  changes is *when to fire* (§5.3).
- **The expiring band is a trigger, not just a decay factor** — the consequence of the term above,
  and the half of "schedule against cache state" v1.1 did not draw. Firing while the prefix is still
  readable costs `(1 − r)·n` less than firing after it dies: 135 000 base-input-token-equivalents at
  a 150 000-token context (§5.4, §8.4).
- **Time is not the only way a prefix dies** — model, effort level and the fast-mode header are all
  part of the cache key. Only effort is observable from a plugin (§5.5, §5.4, §12).
- **§2.5 gains four window variables** and the per-model default framing; the section's arithmetic is
  confirmed, reproducing the published 967K Sonnet-5 figure exactly (§2.5, §8.4).
- **§2.7 gains the extended-thinking inheritance** of the summarization request (v2.1.198), and marks
  its one unverifiable row as such (§2.7).
- **§12 gains four limits and two upstream issues**, all about cache state a plugin cannot see.

Appendix C's values are **unchanged and remain correct**: they are the five-minute regime, which is
the floor. The running regime is resolved at runtime and never written back into config, so D11's
lint gate and the Appendix C golden test keep working exactly as before.

§2.2, §2.3, §2.4 and §2.6 are **unchanged and were not verified**. They describe Claude Code
internals by identifier, none of which appears in published documentation. They were not checked
against decompiled or leaked builds — §7.1 makes this a sidecar that surrounds compaction rather
than depending on its internals, and a design premised on that must not acquire the dependency
through its own revision. Treat them as motivating background at the version they were written
against. Where a §2 fact became load-bearing — §2.5's arithmetic and §2.7's table — it is publicly
documented and it checks out.
- Existing levers re-attributed as latency wins: no-snippets rule cuts decode length; 8–12K rehydration budget cuts first-turn-after latency

**v1.4** — §8.6's nested-`CLAUDE.md` rule widened to the subtree it actually governs:
- **Nested `CLAUDE.md` restoration walks ancestors.** §8.6's "in a directory *containing* a
  pointer-set file" was too narrow by the semantics of the thing it restores. A nested `CLAUDE.md`
  governs its whole subtree in Claude Code, so a `src/pkg/CLAUDE.md` is in force for
  `src/pkg/deep/thing.go` and is lost after compaction on exactly the terms G4.2 describes. The
  literal reading restored it only when a pointer sat in its own directory, leaving the deeper —
  and more common — case unrepaired while §9's G4.2 row claimed closure. The rule now reads
  "containing, or ancestor to", bounded at 32 levels and stopping before the project root, which
  the host re-injects itself (§2.7). §12's "cannot" list is unchanged; this widens what L5 reads
  from disk, not what a plugin may do.
- No other section moved. `plans/QOMPACK-ERRATA.md` records the conflict this resolved, the
  evidence on both sides, and why the widening was chosen over correcting the code to match.
