# SP-11: L5 rehydrator: SessionStart compact branching, the eight-item importance-ordered injection, instruction restoration, skill index, drop report, and the 8-12K budget

> **Recommended model: Opus 5 · xhigh effort**
>
> The eight-item ordering, `**`-aware glob matching, frontmatter scanning and the 8–12K budget fill are fiddly but entirely decided in the plan — including the rendered output verbatim. Execution fidelity, not invention.

**Branch:** `feat/sp11-rehydrator-l5` (cut from `develop`) | **Wave:** 3 | **Prerequisites:** the branches of `["SP-01","SP-06","SP-07","SP-09"]` already merged into `develop` | **Runs in parallel with:** sibling subplans of wave 3 (SP-10 checkpointer, SP-12 scheduler, SP-13 MCP retrieval) | **Design sections:** §7.2 L5, §7.3 (SessionStart source=compact), §8.6, §8.7 design note (standing instruction), §10 Phase 3 (exit criterion), §12 (rehydration budget cap) | **Gaps closed:** G3.3, G4.1, G4.2, G4.4, G4.5, G7.3, G7.5

### Two wave-3 prerequisites that are not branch merges

**1. The daemon bootstrap block is a mid-wave constraint, and SP-11 creates it.** Wave 3 merges `SP-10 → SP-11 → SP-12 → SP-13` (`plans/README.md:44`). `plans/V4-SP-13-mcp-retrieval-layer.md` declares itself the single owner of the `internal/cli/daemon.go` wiring that constructs `opts.Store`, `opts.Ledger`, `opts.Graph` and a `checkpoint.Reader` — but SP-13 merges **fourth**, and SP-11's stated exit criterion `TestE2E_AdditionalContextProducerIsDeclared` runs against a daemon built the way `cmd/qompack` builds it. `internal/cli/daemon.go:81-84` today assigns only `Log`, `Metrics` and `Clock`. So SP-11 is the first branch in merge order that needs the block to exist, and waiting for SP-13 would make this branch's exit criterion unreachable at the moment it merges.

Resolution, recorded here so no wave-3 session improvises it: **SP-11 lands the minimal block; SP-13 owns its final shape and extends it.** This is the ruling; `plans/V4-SP-13-mcp-retrieval-layer.md` spec §11 and `plans/V4-SP-12-scheduler-l3.md`'s shared-file protocol were both corrected to conform to it, and an earlier draft of SP-13 that read *"SP-13 owns opening them"* is superseded — executing both would write `store.Open` twice into the same six-line window, and a second `store.Store` handle on one project root is a corruption bug, not a redundancy. SP-11 adds to `internal/cli/daemon.go`: `syms := symbols.New()`, `store.Open` (with `store.Deps{Symbols: syms, …}` — a nil `Symbols` there silently disables `Query.Symbol` and the §8.7 span widener, so SP-11 supplies it rather than leaving it for SP-13 two merges later), `dag.Open`, `negknow.Open`, a `checkpoint.Reader`, the two `defer Close`s, each failure logging `Loud` and leaving its `Options` field nil (`runDaemon` must still return nil however badly it goes), then builds the service and calls `BindRehydrate(o, svc)`. SP-12 reads those fields and adds nothing. SP-13 later appends `rehydrate.NewReporter`, `mcp.NewPromoter` and `InstallMCPOp`, nothing else, and is responsible for the wave closing with exactly one store, one ledger and one graph.

**The `checkpoint.Reader` line is the one that moves between branch and rebase.** `checkpoint.OpenReader` is SP-10's constructor, same-wave branches never branch from each other (`plans/README.md:38`), and SP-01 stubbed only the `checkpoint.Reader` **type** — so on `feat/sp11-*` the block declares a typed-nil `var ckptReader checkpoint.Reader` and every consumer's nil tolerance is exercised for real. SP-10 merges immediately ahead of SP-11, so the rebase before SP-11's own merge is where that line becomes `ckptReader := checkpoint.OpenReader(root, log, reg)`. Do not skip it: SP-13's rebase step reads `ckptReader` as already-real and replaces only `dropReporter`.

**What SP-11 may assume:** only what is already on `develop` when the wave's branches are cut — SP-01's config/paths/hookio, SP-05's `daemon.Options`/`Services`/`Bind` seam, SP-06's `store`, SP-04's `symbols` (needed for `store.Deps.Symbols`), SP-07's `dag`, SP-08's `observer`, SP-09's `negknow`, plus the `checkpoint.Reader` **type** SP-01 stubbed — which is all the bootstrap block needs to compile and bind. Its behaviour arrives when SP-10 merges ahead of this branch in the wave-3 order, and Rule W-2 still governs every test on this branch: goldens and `fakeReader`, never `checkpoint.Writer`. **Nothing from SP-12 or SP-13**: no `InstallMCPOp`, no scheduler runtime options, no MCP tool registration. `store.Open` is called in exactly one place in `internal/cli`, and this branch is the place that first calls it.

**2. Step 0's `arch/` branch must land before *any* wave-3 branch is cut — it is a coordinator step, not an SP-11 step.** The Commit plan's step 0 merges `arch/rehydrate-item-kinds` into `develop` and then cuts `feat/sp11-rehydrator-l5` from the merged tip. `plans/README.md:39` requires the opposite sequencing for the wave as a whole: "For every subplan in the wave, cut its branch from the current (verified) `develop` … Same-wave branches never merge into or branch from each other", and `:53` allows a new wave's branches to be cut only after the previous checkpoint has merged. Read together, an `arch/` merge performed after wave 3's branches exist moves `develop` underneath SP-10, SP-12 and SP-13, leaving three branches carrying a different §5.15 mid-wave. **Therefore the wave-3 coordinator lands step 0's amendment on `develop` first, and cuts all four wave-3 branches from that tip** — SP-11 does not merge anything into `develop` while its siblings are live. The §5.15 behavioural amendment for `NestedClaudeMD`'s ancestor walk (see the `NestedClaudeMD` spec below) folds into that same branch, so there is exactly one pre-wave-3 `arch/` merge, not two. This constraint is recorded in neither `plans/README.md`'s wave-3 row nor `plans/V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md`; the coordinator owns closing that gap.

---

## Mission

This slice is layer **L5 — the rehydrator**. It is the read end of Qompack: everything L0/L1/L2/L4 wrote to `.qompack/` is worthless to the agent unless something puts the right 8–12K tokens back into the context window at the moment Claude Code's compaction has just destroyed it. `Qompack.md` §8.6 specifies exactly what that payload contains and in exactly what order; this subplan implements that specification and nothing else.

The reason it exists is `Qompack.md` §3 G3.3: *"Eager restoration is the inverse of retrieval. Re-injecting the top 5 files against a 50K budget is a guess. A lazy affordance costs ~200 tokens and is correct rather than probabilistically correct."* Stock Claude Code spends 50K on five file bodies plus 25K on skill bodies (§2.4 step 7) and still loses every `paths:`-scoped rule (G4.1), every nested `CLAUDE.md` (G4.2), the entire skill index (G4.4), and never tells the agent that any of it went missing (G4.5). SP-11 replaces 75K of eager guessing with 8–12K of pointers, verbatim non-reconstructible facts, restored instructions, and an explicit drop report — and closes G7.3 permanently by injecting the original user intent from the L0 verbatim capture rather than from any summary, so PTL retry dropping the oldest API rounds can no longer delete the task statement. G7.5 is closed the same way: when the summarization call fails by calling a tool instead of summarizing, the checkpoint-derived injection is the fallback path and the session survives.

**What exists in the repo when you start.** SP-01 (wave 0) has shipped the whole module skeleton: `internal/core`, `internal/paths`, `internal/config` (including the §11.5 `runtime.rehydrate` namespace with `minTokens`/`maxTokens`/`skillIndexTokens`/`eliminationsTopN`), `internal/logging`, `internal/obs`, `internal/tokens`, `internal/hookio`, `internal/testutil`, the `test/e2e` harness, the `nomagic` lint pass, the import-graph DAG check, CI, and **compiling `ErrNotImplemented` stubs plus `<pkg>test` conformance suites for every interface in §5** — including `internal/rehydrate`, `internal/rules`, `internal/skills` and `rehydratetest`. SP-06 (wave 1) has shipped a real `store.Store` with `Search`, `Open`, `OpenSpan`, `FileHistory`, `Segments()` and exact chunk-level token accounting. SP-07 (wave 1) has shipped a real `dag.Graph` with `BackwardSlice` returning per-node relevance scores. SP-09 (wave 2) has shipped a real `negknow.Ledger` with the three-way `Answer` and `Active(scope)`. SP-08 (wave 2, on `develop` by construction because wave 3 is cut from post-V3 `develop`) has shipped `internal/observer`, including the `SessionStart` `source` switch that delegates the `compact` and `clear` branches outward per §5.21.

**What exists when you finish.** `rehydrate.Build` produces the complete §8.6 payload with per-item token accounting and truncation flags, wrapped in the §8.5 injection tags carrying the checkpoint sequence number. `rules.Scanner` re-reads every `paths:`-scoped rule whose glob matches a pointer-set file and every nested `CLAUDE.md` in a directory containing one. `skills.Indexer` builds the compact skill index against `runtime.rehydrate.skillIndexTokens` — never a package constant, because §11.6 forbids the literal `450` outside `internal/config/defaults.go`. A `rehydrate.Reporter` persists the drop report to `.qompack/state/rehydrate-<session>.json` and structurally satisfies `mcp.DropReporter`, which is the interface SP-13's `dropped` tool is built against. A single new file in the `internal/daemon` composition root implements the `SessionStart` `compact` and `clear` branches and binds the already-shipped `Services.Rehydrate` seam, which is what finally declares §12.1's `hook.additional_context_delivered` producer. And a `phase3` entry in `test/replay/phases.go` asserts the Phase 3 exit criterion of §10 against the registered `stock` policy inside the replay-gate driver, so it re-asserts on every later pull request. Phase 3 closes here.

---

## Design context (verbatim from Qompack.md)

Everything below is quoted verbatim. Do not open `Qompack.md`; it is quoted here in full for every fact this subplan needs.

### §8.6 — L5 Rehydrator (the whole section, verbatim)

> ### 8.6 L5 — Rehydrator
>
> **Trigger:** `SessionStart` with `source == "compact"`.
>
> **Emits** via `hookSpecificOutput.additionalContext`, filled in importance order until the budget is reached:
>
> 1. **Invariants and pins** — verbatim, always
> 2. **Verbatim original user intent** — from L0, not from any summary (closes G2.3 permanently)
> 3. **Eliminated-approaches digest** — the top-N most relevant by slice score, plus a note that `already_tried()` covers the rest
> 4. **Decisions with rationale**
> 5. **Current work and next step**
> 6. **Pointers, not contents** — file paths with hashes and one-line reasons
> 7. **Drop report** — the explicit list of path-scoped rules, nested `CLAUDE.md` files, and truncated skills that are no longer in context (closes G4.5)
> 8. **Retrieval affordance notice** — one line telling the agent that `recall`, `re_read`, and `already_tried` exist
>
> **Instruction restoration (G4.1, G4.2, G4.4).** The rehydrator re-reads from disk, independently of Claude Code's own restoration:
>
> - Every `paths:`-scoped rule whose glob matches any file in the checkpoint's pointer set
> - Every nested `CLAUDE.md` in a directory containing a pointer-set file
> - A compact skill index (names and one-line descriptions only, ~450 tokens) so skill awareness returns
>
> **Budget discipline.** Default rehydration budget is deliberately far below Claude Code's 50K + 25K: target 8–12K. The whole point is that pointers plus retrieval replace eager restoration. Measure this in the harness before relaxing it.

### §8.7 design note — the standing instruction (verbatim)

> **Design note:** `already_tried` should be surfaced in the rehydrated context as a *standing instruction*, not merely an available tool. "Before committing to an approach, call `already_tried`." Otherwise the affordance exists and goes unused.

### §8.7 — the eight tools whose existence item 8 announces (verbatim table)

> | Tool | Signature | Purpose |
> |---|---|---|
> | `recall` | `(query, k=5)` → hits | Search the store by content, path, or symbol; returns hashes and summaries |
> | `expand` | `(hash \| tool_use_id)` → content | Re-materialize a cleared tool result |
> | `re_read` | `(path, at=null)` → content | Current or historical version of a file |
> | `already_tried` | `(target, approach)` → bool + reason | Bloom membership plus the stored reason when present |
> | `record_eliminated` | `(target, approach, reason)` | Write negative knowledge |
> | `timeline` | `(from, to)` → segments | What happened between two points |
> | `why` | `(decision_id)` → rationale | Retrieve a decision and its evidence |
> | `dropped` | `()` → list | What is currently out of context |

### §7.3 — hook surface row for SessionStart (verbatim)

> | `SessionStart` | L0/L5 | Branch on `source`: `startup`/`resume` → load store; `compact` → rehydrate |

### §7.2 — the L5 row of the layer diagram (verbatim)

> ```
> ├─────────────────────────────────────────────────────────────────┤
> │ L5  REHYDRATOR            SessionStart(source=compact)           │
> │                           progressive budget fill · drop report  │
> ├─────────────────────────────────────────────────────────────────┤
> ```

### §2.4 — the stock restoration this replaces (verbatim, steps 6–9)

> ```
> 6.  readFileState.clear()
> 7.  Restore post-compact context:
>       • top 5 recently-read files (50K budget, 5K/file)
>       • invoked skills (25K budget, 5K/skill)
>       • active plan content
>       • plan mode instructions
>       • deferred tool deltas
>       • agent listing deltas
>       • MCP instruction deltas
> 8.  SessionStart hooks (source=compact)
> 9.  PostCompact hooks
> ```

### §2.7 — what survives compaction (verbatim table; the four rows this slice repairs)

> | Mechanism | After compaction |
> |---|---|
> | System prompt, output style | Unchanged; outside message history |
> | Project-root `CLAUDE.md`, unscoped rules | Re-injected from disk |
> | Auto memory (`MEMORY.md`) | Re-injected from disk |
> | Rules with `paths:` frontmatter | **Lost** until a matching file is read again |
> | Nested `CLAUDE.md` in subdirectories | **Lost** until a file in that subdir is read again |
> | Invoked skill bodies | Re-injected, 5K/skill and 25K total, oldest dropped, truncated head-first |
> | Skill *index* / descriptions | **Not re-injected at all** |
> | Hooks | N/A — hooks run as code |

### §3 — the seven gaps this subplan closes (verbatim rows)

> | G3.3 | **Eager restoration is the inverse of retrieval.** Re-injecting the top 5 files against a 50K budget is a guess. A lazy affordance costs ~200 tokens and is correct rather than probabilistically correct. |
> | G4.1 | **Path-scoped rules vanish** until a matching file is coincidentally re-read. |
> | G4.2 | **Nested `CLAUDE.md` files vanish** on the same terms. |
> | G4.4 | **The skill index is not re-injected,** so the model loses awareness of what it could invoke. `stripReinjectedAttachments()` removes `skill_discovery`/`skill_listing` before summarization, so it is not in the summarized record either. |
> | G4.5 | **No drop report.** Nothing surfaces "these four constraints are no longer in context." The agent's operating rules change silently mid-session. |
> | G7.3 | **PTL recovery sacrifices exactly the wrong thing.** It drops the *oldest* API-round groups — where the original task statement and earliest user intent live. Sections 1 and 6 exist to preserve that content and the recovery path deletes it first. |
> | G7.5 | **The compaction call can fail by calling a tool** instead of summarizing (`content: null` on the API side). |

### §9 — the traceability rows this subplan is accountable for (verbatim)

> | G3.3 eager restoration | L5 pointer-first, 8–12K budget | — |
> | G4.1 path rules lost | L5 re-reads matching rules | — |
> | G4.2 nested CLAUDE.md lost | L5 re-reads by pointer directory | — |
> | G4.3 skill head-truncation | L4 importance ordering; L5 skill index | Cannot change Claude Code's own truncation |
> | G4.4 skill index gone | L5 compact index re-injection | — |
> | G4.5 no drop report | L5 explicit drop report | — |
> | G7.3 PTL drops intent | L5 restores verbatim intent regardless | Cannot change PTL logic |
> | G7.5 tool-call-instead-of-summary | L4/L5 checkpoint is the fallback path | Cannot prevent the failure |
> | G2.3 user messages regenerated | L0 verbatim capture, L5 verbatim replay | — |

### §8.1 item 7 — the L0 verbatim capture that item 2 reads from (verbatim)

> 7. **Verbatim user capture.** Every `UserPromptSubmit` is written immutably to `index/segments.jsonl`. This is the durable version of section 6 of the summary prompt, and unlike section 6 it is never regenerated.

This is the *only* legitimate source for §8.6 item 2. `Qompack.md` fixes the durability requirement, not the physical index a plugin must read it back through; on this build the capture is materialized as a store object with a `store.ToolUseRecord` whose `Tool` is the reserved name below, which is what makes it retrievable by `store.Search`/`store.ToolUse` without `rehydrate` importing `observer`.

### §8.5 — the checkpoint schema this slice reads (verbatim)

> ```jsonc
> {
>   "version": 1,
>   "session": "…",
>   "seq": 7,
>   "created": "…",
>   "parent": "0006.json",
>   "encoded_segments": [12, 13, 14],     // DPI guard: from originals only
>
>   // ── Tier 1: never truncated ──────────────────────────────
>   "invariants": [ … ],                   // pinned, verbatim
>   "user_intent": {
>     "original": "…",                     // verbatim, from L0, never regenerated
>     "evolution": [ … ]                   // verbatim deltas
>   },
>   "eliminated": [                        // negative knowledge, structured
>     { "target": "src/auth.ts:refreshToken",
>       "approach": "widen pool timeout",
>       "reason": "pgbouncer 1.18 ignores it in transaction mode",
>       "evidence": "sha256:…",
>       "depends_on": [                     // staleness guard (§8.3):
>         { "path": "docker-compose.yml", "hash": "sha256:…" },
>         { "path": "package-lock.json",  "hash": "sha256:…" }
>       ],
>       "scope": "project",                 // "session" | "project"
>       "status": "active" }                // "active" | "stale"
>   ],
>
>   // ── Tier 2: truncate late ────────────────────────────────
>   "decisions": [
>     { "what": "…", "why": "…", "alternatives_rejected": [ … ],
>       "evidence": "sha256:…" }
>   ],
>   "open_questions": [ … ],
>   "current_work": { "goal": "…", "next_step": "…", "blocked_on": null },
>
>   // ── Tier 3: truncate first ───────────────────────────────
>   "pointers": {
>     "files":  [ { "path": "…", "hash": "sha256:…", "why": "…" } ],
>     "tools":  [ { "tool_use_id": "…", "hash": "sha256:…", "summary": "…" } ]
>   },
>   "narrative": "…",                      // prose residue, last resort
>
>   // ── Metadata ─────────────────────────────────────────────
>   "sketch_refs": { "tried": "tried.bloom", "touch": "touch.cms" },
>   "dropped": [ { "kind": "path_rule", "id": "api-conventions.md" } ],
>   "cache": { "p_chosen": 148230, "rewrite_tokens": 18770, "ttl_state": "warm" }
> }
> ```

### §8.5 — the regeneration rule and injection tagging (verbatim)

> **Regeneration rule (closes GC).** Checkpoints are **always generated from the store** — the chunk objects, segment log, verbatim intent captures, and structured records — never from content currently sitting in the context window. This matters because the rehydrator's own prior injection lives in message history and will be mangled by the next Claude Code summarization pass; a checkpoint that trusted the surviving in-context version would be compressing a compression through the back door. The rehydrator tags every injection with its checkpoint sequence number precisely so the next checkpoint pass can identify and ignore that material as a source.

### §8.5 — the O1 span instruction whose effect the exit criterion measures (verbatim)

> **Incremental summarization via focus instructions (O1).** Because a durable checkpoint already covers everything through turn `N`, the focus instructions also narrow the summarizer's *span*:
>
> > A durable checkpoint (`.qompack/checkpoints/0007.json`) fully covers the session through turn N, including all decisions, eliminations, and file state up to that point. Do not re-summarize that material. Summarize only what happened after turn N: new decisions, new eliminations, new intent, current work.

> The one caveat: `custom_instructions` is advisory — the summarizer may ignore the span restriction — so the checkpoint remains the authoritative record and the rehydrator never depends on the summary having complied.

### §8.5 — the latency claim the 8–12K budget contributes to (verbatim)

> the no-snippets rule (§4.4) cuts decode length again (a pointer is ~20 tokens where a snippet is ~500), and the 8–12K rehydration budget (§8.6) shrinks the slow first turn after.

### §4.4 — the MDL partition that justifies pointers-not-contents (verbatim)

> **Reconstructible at near-zero cost — must become pointers, never prose:**
>
> - File contents (re-read)
> - `git status`, `git diff`, branch state
> - Directory structure
> - Deterministic tool output (`Grep`, `Glob`)
> - Test results (re-run)
>
> **Non-reconstructible — this is what the budget is for:**
>
> - User intent and its evolution
> - Decisions and their rationale
> - Approaches ruled out and why
> - Exact text of an error that will not reproduce
> - Causal chains of reasoning
> - Constraints discovered empirically
>
> This is the theoretical case for G3, and it condemns section 3 of the current prompt and the 50K eager restoration budget. Five paths plus a `re_read(path)` affordance costs ~200 tokens and is *correct* rather than probabilistically correct.

### §6.9 — the embedded-coding principle the budget fill implements (verbatim)

> Embedded coders order the bitstream by importance so truncation *at any point* yields the best available reconstruction for that budget. Current truncation is positional — skills keep their head, PTL retry drops the oldest rounds, which is where intent lives.
>
> If the checkpoint is written in importance order, every budget cut is automatically near-optimal and PTL recovery stops deleting the task statement first. **This is a serialization-order change, not an algorithm** — one of the cheapest wins available.

### §8.3 — the three-way elimination response item 3 renders (verbatim)

> 4. `already_tried` responses distinguish the cases: *active* returns the reason; *stale* returns "previously eliminated, but the evidence has changed since — re-verification may be warranted," which is strictly more useful to the agent than either a block or silence.
> 5. `scope: "session" | "project"` controls cross-session carry-over: session-scoped eliminations ("this test is flaky today") die with the session; project-scoped ones ("this library fundamentally can't do X") persist and warm-start future sessions (§10 Phase 7).

### §8.3 — the slice output item 3's ordering uses (verbatim)

> **Slicing.** Backward slice from the criterion set: current todo items, files under edit, the active plan, the most recent user intent. Thin-slicing variant by default. Output: a relevance score per node, not a binary keep/drop — the score feeds submodular selection.

### §10 Phase 3 — scope and exit criterion (verbatim)

> ### Phase 3 — Checkpoint and rehydrate
>
> - `PreCompact` checkpoint writer with importance ordering
> - `SessionStart(source=compact)` rehydrator with injection tagging (§8.5 regeneration rule)
> - Drop report; path-rule and nested-`CLAUDE.md` restoration; skill index
> - Focus-instruction generation, **including the incremental-span instruction (O1)** — this rides along for free once the checkpoint exists and directly shrinks the most expensive call in the session
>
> **Exit criterion:** post-compaction first-divergence turn index improves against Phase 0 baseline at a *smaller* rehydration budget than stock; measured reduction in summarization-call input tokens when the span instruction is honoured.

### §11.2 — the three secondary metrics this slice is graded on (verbatim rows)

> | First-divergence turn | Turns after compaction before compacted and uncompacted branches diverge |
> | Rehydration budget | Tokens spent restoring context |
> | **First-turn-after latency** | Time to first token on the turn following compaction (captures rebuild + cache-write cost) |

### §12 — the risk row this slice mitigates (verbatim)

> | Rehydration crowds out working context | Medium | Hard budget cap; pointer-first design; measured in harness |

### §12 — the cannot-do rows that bound this slice (verbatim)

> - **Cannot modify the message array directly.** Everything flows through `additionalContext`.
> - **Cannot guarantee the summarizer honours focus instructions** — span narrowing (§8.5) and snippet prohibition (G3.4) are advisory; the durable checkpoint is the backstop for both.

### Appendix C — the configuration keys this slice reads (verbatim excerpt)

> ```jsonc
>   "checkpoint": {
>     "budgetTokens": 12000,
>     "incrementalSpanInstruction": true,
>     "frontier": { "advanceOnSegmentClose": true, "maxResidualTokens": 20000 },
>     "tiers": { "never": ["invariants","user_intent","eliminated"],
>                "late":  ["decisions","open_questions","current_work"],
>                "first": ["pointers","narrative"] }
>   },
>   "eliminations": {
>     "requireEvidence": true,
>     "defaultScope": "session",
>     "rebuildOnStale": "nextIdle",
>     "staleResponse": "flag"          // "flag" | "drop"
>   },
>   "retrieval": {
>     "ephemeralResults": true,
>     "defaultSpan": "minimal",        // "minimal" | "full"
>     "promoteAfterExpansions": 2
>   },
>   "selection": {
>     "slicing": "thin",
>     "deltaScoring": "cheap",
>     "submodular": { "lambda": 0.4, "lazyGreedy": true }
>   },
> ```

### 00-ARCHITECTURE §11.5 — the `runtime.rehydrate` namespace (verbatim)

> ```jsonc
>   "rehydrate": { "minTokens": 8000, "maxTokens": 12000,   // §8.6 8–12K cap; Appendix C has no key
>                  "skillIndexTokens": 450,                 // §8.6 ~450-token skill index
>                  "eliminationsTopN": 8 },
> ```

### 00-ARCHITECTURE §11.6 — the no-hardcoding rule that binds the skill index (verbatim)

> `450` is in that set, which is why `skills.Index` takes its budget from `runtime.rehydrate.skillIndexTokens` rather than a package constant (§5.15); the set is extended with `{8000, 12000}` when §11.5's `rehydrate` keys land in SP-01.

### 00-ARCHITECTURE §5.21 — the normative SessionStart ownership split (verbatim row)

> | `SessionStart` | SP-05: `qompack session-start` dispatch, daemon start, `contract.Monitor.RunAll` before any other work | SP-08: `startup`/`resume` branch (session registration, store open, warm state). SP-11: the `compact` branch (rehydration) and `clear` reset. The `source` switch itself lives in `observer.OnSessionStart` and delegates. |

### 00-ARCHITECTURE §12.1 — degraded-passive behaviour, which gates this whole slice (verbatim)

> `ModeDegradedPassive` behaviour: L0 and L1 keep running (observe, chunk, store, sketches, DAG, verbatim capture, elimination records — the store stays correct and the session's data is not lost). Everything that *acts* is off: no `additionalContext` injection, no `customInstructions`, no scheduler-initiated checkpoints, no drop report.

### 00-ARCHITECTURE §12.1 — the contract assertion this slice must cooperate with (verbatim row)

> | `hook.additional_context_delivered` | `SessionStart` emits a sentinel token in `additionalContext`; the next `UserPromptSubmit` reads the transcript tail and looks for it. Not found ⇒ fail |

### 00-ARCHITECTURE §5.15 — the interface this subplan implements (verbatim)

> ```go
> type ItemKind uint8
> const (
>     ItemInvariants ItemKind = iota  // 1. pins, verbatim, always
>     ItemUserIntent                  // 2. verbatim original intent, from L0 (G2.3)
>     ItemEliminations                // 3. top-N by slice score + "already_tried covers the rest"
>     ItemDecisions                   // 4. decisions with rationale
>     ItemCurrentWork                 // 5. current work and next step
>     ItemPointers                    // 6. pointers, not contents
>     ItemDropReport                  // 7. explicit drop report (G4.5)
>     ItemAffordance                  // 8. one line: recall / re_read / already_tried exist
> )                                   // ORDER IS NORMATIVE — this is the §8.6 importance order.
>
> type Item struct{ Kind ItemKind; Rank int; Tokens core.Tokens; Text string; Truncated bool }
> type DropEntry = checkpoint.DropEntry
>
> type Request struct {
>     Session     core.SessionID
>     Source      string             // startup | resume | compact | clear
>     ProjectRoot string
>     Budget      core.Tokens        // default 8_000–12_000 (§8.6); hard cap from config
>     Checkpoint  checkpoint.Checkpoint
>     Ref         checkpoint.Ref
>     Cfg         config.Config
> }
> type Result struct {
>     Items    []Item
>     Text     string        // the additionalContext payload, injection-tagged
>     Tokens   core.Tokens
>     Dropped  []DropEntry
>     Degraded bool
>     Seq      core.CheckpointSeq
> }
> type Deps struct {
>     Store store.Store; Ledger negknow.Ledger; Graph dag.Graph
>     Rules rules.Scanner; Skills skills.Indexer; Tokens tokens.Estimator; Log logging.Logger
> }
> func Build(ctx context.Context, r Request, d Deps) (Result, error)
>
> // StandingInstruction is item 3's companion, emitted verbatim (§8.7 design note):
> //   "Before committing to an approach, call already_tried."
> func StandingInstruction() string
>
> // package rules  (G4.1, G4.2)
> type Rule struct{ Path string; Globs []string; Body string; Tokens core.Tokens; Nested bool }
> type Scanner interface {
>     // PathScoped returns every rule whose `paths:` frontmatter glob matches any pointer path.
>     PathScoped(ctx context.Context, root string, pointers []string) ([]Rule, error)
>     // NestedClaudeMD returns CLAUDE.md files in directories containing a pointer-set file.
>     NestedClaudeMD(ctx context.Context, root string, pointers []string) ([]Rule, error)
> }
>
> // package skills  (G4.4)
> type Entry struct{ Name, Description, Source string }
> type Indexer interface {
>     // Index returns a compact skill index: names + one-line descriptions only, budgeted.
>     Index(ctx context.Context, root string, budget core.Tokens) ([]Entry, core.Tokens, error)
> }
> ```

> **Rehydration budget.** Appendix C has no rehydration budget key (`checkpoint.budgetTokens` is a
> different budget — what the checkpoint may cost on disk), so the 8–12K cap of §8.6 lives in the
> additive `runtime.rehydrate` namespace (§11.5). `Request.Budget` is filled from it; the hard cap
> is `runtime.rehydrate.maxTokens`.

### 00-ARCHITECTURE §5.16 — the two seams SP-13 consumes from this subplan (verbatim)

> ```go
> // DropReporter backs `dropped` (implemented by rehydrate in wave 3).
> type DropReporter interface{ CurrentDrops(ctx context.Context, sess core.SessionID) ([]checkpoint.DropEntry, error) }
> ```

### 00-ARCHITECTURE §3.2 — this subplan's import budget (verbatim rows)

> | `hookio`, `sketch`, `chunk`, `symbols`, `redact`, `grammar`, `rules`, `skills`, `pins`, `tokens`, `eval`, `scheduler` | foundation only |
> | `rehydrate` | `checkpoint` `store` `negknow` `dag` `rules` `skills` `tokens` |
> | `daemon`, `cli`, `commands`, `testutil`, `cmd/qompack` | **composition roots** — may import anything; nothing may import them |

### 00-ARCHITECTURE §6.4 — the coverage floors that gate this branch (verbatim rows)

> | `scheduler`, `dag`, `analyzer`, `rehydrate`, `eval`, `mcp` | **85%** |
> | everything else | **75%** |

---

## Out of scope

Every item below is owned by a named sibling. Do not implement, refactor, or "improve" any of them on this branch.

| Out of scope | Owner |
|---|---|
| Writing checkpoints, `checkpoint.Writer`, `Begin`/`Advance`/`Finalize`, `Truncate`, `ExtractDecisions`, `ValidatePointers`, `FocusInstructions`, the O1 span paragraph, `custom_instructions` emission, `internal/pins` | **SP-10** |
| `internal/observer` — any file, any line. The `SessionStart` `source` switch itself, session registration, store open, warm state, `startup`/`resume` branches, `SessionEnd` | **SP-08** |
| `qompack session-start` CLI subcommand, daemon startup, IPC transport, `contract.Monitor` and its assertions, `Services`/op-routing/`IdleController` seam definitions, hot-path budgets B-A…B-F | **SP-05** |
| `internal/mcp`, the `dropped` tool handler, the JSON-RPC server, ephemeral tagging, minimal-span resolution, `Promoter` | **SP-13** |
| `/qompack:dropped` and `/qompack:status` slash commands and their `--json` output | **SP-14** |
| Scheduler, p-selection, changepoints, frontier advancement (O5), idle background work (O3), `BackgroundTask` scheduling | **SP-12** |
| `dag.BackwardSlice` internals, slice scoring algorithm, `CrossingEdges` | **SP-07** |
| `negknow` ledger, descriptors, staleness flip, bloom rebuild, `already_tried` semantics | **SP-09** |
| `store` internals, `Search` ranking, GC, redaction, `tokens.Estimator` implementation and calibration | **SP-06** (redaction and store), **SP-01/SP-06** (tokens) |
| `eval.Harness`, `Synthesize`, the 24-session synthetic corpus, Belady OPT, the replay-gate driver, the Phase 0 baseline artifact | **SP-02** |
| Demand-driven promotion of frequently re-expanded hashes into the pointer tier, progressive-truncation tuning from replay evidence, per-segment blooms | **SP-16** |
| Submodular selection of what to inject; Δ-scoring | **SP-15** |
| `config` schema, `Defaults()`, validation, provenance, JSON Schema, the `runtime.rehydrate` keys themselves | **SP-01** |
| Plugin manifest, packaging, cross-platform matrix, `docs/user-guide.md`, `docs/cannot-do.md`, `docs/upstream-issues.md` | **SP-17**, **SP-18** |

---

## Interface contract

### Consumes — exact signatures called by this subplan

From `internal/core` (SP-01):

```go
type Hash [32]byte
func (h Hash) String() string          // "sha256:" + hex
func (h Hash) Short() string           // first 12 hex chars
func HashBytes(domain string, b []byte) Hash
type SessionID string
type ToolUseID string
type TurnIndex int
type CheckpointSeq int
type DecisionID string                 // "dec_" + first 12 hex of HashBytes("qompack.decision", …)
type Tokens int
type UnixMilli int64
type Clock interface{ Now() time.Time; Since(time.Time) time.Duration }
func SystemClock() Clock                // used by internal/daemon only; Build is clock-free
var ErrNotFound = errors.New("qompack: not found")
var ErrBudget   = errors.New("qompack: budget exceeded")
var ErrDegraded = errors.New("qompack: running in degraded mode")
```

From `internal/paths` (SP-01):

```go
func Norm(projectRoot, p string) (string, error)   // project-relative, forward-slash, cleaned
func Key(p string) string                          // dedup key: Norm + lowercase on Windows/macOS
func WriteAtomic(p string, b []byte) error
```

From `internal/config` (SP-01, §11.5):

```go
type Config struct{ /* … */ Runtime RuntimeCfg `json:"runtime"` }
type RuntimeCfg struct{ /* … */ Rehydrate RehydrateCfg `json:"rehydrate"` }
type RehydrateCfg struct {
    MinTokens        int `json:"minTokens"`        // default 8000
    MaxTokens        int `json:"maxTokens"`        // default 12000
    SkillIndexTokens int `json:"skillIndexTokens"` // default 450
    EliminationsTopN int `json:"eliminationsTopN"` // default 8
}
```

**All four fields are plain `int`, not `core.Tokens`** — that is what shipped in `internal/config/runtime.go`, and Go does not convert between the two implicitly. Every use site in `budget.go`, `items.go` and `rehydrate_service.go` therefore writes an explicit conversion: `core.Tokens(cfg.MaxTokens)`, `core.Tokens(cfg.MinTokens)`, `core.Tokens(cfg.SkillIndexTokens)`. `EliminationsTopN` stays an `int` and is used as a slice length. The spec code below is written with those conversions in place; do not "simplify" them away.

From `internal/tokens` (SP-01 interface, SP-06 exact accounting):

```go
type Class uint8 // ClassProse, ClassCode, ClassJSON, ClassDiff, ClassImage, ClassPDF, ClassBinary
type Estimator interface {
    Estimate(b []byte, c Class) core.Tokens
    EstimateString(s string, c Class) core.Tokens
    EstimateRoot(ctx context.Context, chunks []core.ChunkRef, c Class) core.Tokens
    Calibrate(observed core.Tokens, estimated core.Tokens)
    Factor() float64
}
```

From `internal/store` (SP-06, wave 1, merged):

```go
type ToolUseRecord struct {
    ID core.ToolUseID; Session core.SessionID; Turn core.TurnIndex; TS core.UnixMilli
    Tool string; Root core.Hash; Path string; Bytes int64; Tokens core.Tokens
    /* … plus ArgsDigest, ArgsPreview, Signature, Status, SupersededBy, Ephemeral, Subagent */
}
ToolUse(ctx context.Context, id core.ToolUseID) (ToolUseRecord, error)
Open(ctx context.Context, root core.Hash) (io.ReadCloser, error)
Has(h core.Hash) bool
```

**`Store.Search` is deliberately absent from this list: this subplan never calls it.** `FSStore.Search` clamps `K` to 100 and ranks project-wide candidates newest-first, which makes it structurally incapable of finding a long session's *first* prompt (`internal/store/search.go:40, 62-67, 230-236`). Item 2 resolves that prompt through `ToolUse` on a derived id instead; the reasoning is spelled out in full under item 2 below, and it is the difference between closing G7.3 and silently inverting it.

From `internal/dag` (SP-07, wave 1, merged):

```go
type NodeID string      // "<kind>:<stable-key>"
type SliceOptions struct{ Thin bool; MaxDepth, MaxNodes int; Decay float32; Deadline time.Duration }
type Slice struct{ Scores map[NodeID]float32; Order []NodeID; Truncated bool; Visited int }
BackwardSlice(criteria []NodeID, o SliceOptions) (Slice, error)
Node(id NodeID) (Node, bool)
```

From `internal/negknow` (SP-09, wave 2, merged):

```go
type Scope string  // "session" | "project"
type Status string // "active" | "stale"
type Record struct {
    ID string; Session core.SessionID; TS core.UnixMilli
    Target, Approach, Reason string
    Desc Descriptor; Evidence core.Hash; DependsOn []Dep
    Scope Scope; Status Status; StaleSince core.UnixMilli; StaleBecause []string
    Source SourceKind
}
type Descriptor struct{ NormalizedPath, Symbol, ApproachClass string; ReasonHash core.Hash }
type Health struct{ Records, Active, Stale int; FillRatio, EstFPRate float64; NeedsResize bool }
Active(ctx context.Context, scope Scope) ([]Record, error)
All(ctx context.Context) ([]Record, error)
Health() Health
```

From `internal/checkpoint` (SP-10, **same wave** — see Rule W-2 note below):

```go
type Checkpoint struct{ /* verbatim §8.5 schema, quoted above */ }
type Ref struct {
    Seq core.CheckpointSeq; Path string; SHA256 core.Hash
    Bytes int64; Tokens core.Tokens; Frontier core.TurnIndex; Created core.UnixMilli
}
type DropEntry struct{ Kind string `json:"kind"`; ID string `json:"id"`; Detail string `json:"detail,omitempty"` }
type Invariant = pins.Invariant   // struct{ ID, Text, Source string; Pinned core.UnixMilli }
type Decision struct {
    ID core.DecisionID; What, Why string; AlternativesRejected []string
    Evidence core.Hash; Turn core.TurnIndex
}
type CurrentWork struct{ Goal, NextStep string; BlockedOn *string }
type Pointers struct{ Files []FilePointer; Tools []ToolPointer }
type UserIntent struct{ Original string; Evolution []string }
const InjectionOpenTag  = "<!-- qompack:injected seq=%d ver=%d -->"
const InjectionCloseTag = "<!-- /qompack:injected -->"
func StripInjections(s string) string
type Reader interface {
    Latest(ctx context.Context, s core.SessionID) (Checkpoint, Ref, error)
    Get(ctx context.Context, seq core.CheckpointSeq) (Checkpoint, Ref, error)
    List(ctx context.Context) ([]Ref, error)
    Chain(ctx context.Context, seq core.CheckpointSeq) ([]Checkpoint, error)
    Verify(ctx context.Context) ([]core.CheckpointSeq, error)
}
```

**Rule W-2 compliance (mandatory, 00-ARCHITECTURE §5.22).** `checkpoint` is owned by SP-10, a **same-wave** sibling. Therefore: every test in this subplan that needs a `Checkpoint` value or a `checkpoint.Reader` reads from `testdata/golden/contracts/checkpoint/*.json` (SP-01-generated fixtures) and/or uses the in-test fake `fakeReader` defined in `internal/rehydrate/fake_test.go`. **No test on this branch constructs a checkpoint by calling `checkpoint.Writer`.** The wave-3 verification checkpoint (V4) re-runs these same tests against SP-10's real implementation; a fixture the real writer cannot reproduce is a verification failure, not a fixture bug.

From `internal/observer` (SP-08, wave 2, merged) — the delegation seam named by §5.21:

```go
// internal/observer — declared by SP-08. observer imports hookio (§3.2), so the seam is
// expressed in hookio types; rehydrate cannot implement it directly because rehydrate may not
// import hookio. The implementation therefore lives in the internal/daemon composition root.
type Rehydrator interface {
    OnCompact(ctx context.Context, e hookio.Event) (hookio.Output, error)
    OnClear(ctx context.Context, e hookio.Event) (hookio.Output, error)
}
```

This is the spelling SP-08 declares (`plans/V3-SP-08-observer-l0.md:447-449`) and it is frozen there: SP-08 owns `internal/observer` exclusively under §5.21 and merges a whole wave earlier, so its names and both signatures are what this plan is written against. Both methods return `(hookio.Output, error)` because SP-08's `source` switch returns the seam's result verbatim (`V3-SP-08:1606`, `:1610`) and because the shipped `daemon.Services.Rehydrate` func type (`internal/daemon/options.go:128`) already has that shape. **Never** edit `internal/observer` from this branch and never work around the seam.

From `internal/daemon` (SP-05, wave 1, merged) — the late-bound service seam of §5.4:

```go
// "Services is the late-bound dependency set; nil members mean 'not built yet' and every
//  call site must tolerate that (waves 1–2 run with Checkpoints and Sched nil)."
type Services struct{ /* SP-05 members */ }
```

### Produces — exact signatures later subplans rely on

`internal/rehydrate`:

```go
type ItemKind uint8
const (
    ItemInvariants ItemKind = iota   // §8.6 item 1
    ItemUserIntent                   // §8.6 item 2
    ItemEliminations                 // §8.6 item 3
    ItemDecisions                    // §8.6 item 4
    ItemCurrentWork                  // §8.6 item 5
    ItemPointers                     // §8.6 item 6
    // The two additive kinds of the §8.6 "Instruction restoration" clause, declared IN their
    // rendered positions by the §0 amendment of step 0 below. Declaring them after
    // ItemAffordance instead would make renderOrder emit kinds 0,1,2,3,4,5,8,9,6,7, which
    // rehydratetest's runItemOrderCase rejects ("items must be emitted in ascending ItemKind").
    ItemRestoredInstructions         // §8.6 item 6a — path rules and nested CLAUDE.md (G4.1, G4.2)
    ItemSkillIndex                   // §8.6 item 6b — the compact skill index (G4.4)
    ItemDropReport                   // §8.6 item 7
    ItemAffordance                   // §8.6 item 8, and still the LAST constant
)
func (k ItemKind) String() string

type Item struct{ Kind ItemKind; Rank int; Tokens core.Tokens; Text string; Truncated bool }
type DropEntry = checkpoint.DropEntry
type Request struct { /* verbatim §5.15, unchanged */ }
type Result struct { /* verbatim §5.15, unchanged */ }
type Deps struct { /* verbatim §5.15, unchanged — no field is added or removed */
    Store store.Store; Ledger negknow.Ledger; Graph dag.Graph
    Rules rules.Scanner; Skills skills.Indexer; Tokens tokens.Estimator; Log logging.Logger
}
// DELIBERATELY NO Clock FIELD. `Build` is a pure function of (Request, Deps) and reads no clock:
// the payload carries no timestamp, `PropBuild_Deterministic` requires byte-identical output for
// identical input, and the only wall-clock value in this slice — `State.Emitted` — is stamped by
// the daemon service from `RehydrateOptions.Clock`. This also keeps `Deps` field-for-field
// identical to 00-ARCHITECTURE §5.15, so no §0 amendment is needed.

func Build(ctx context.Context, r Request, d Deps) (Result, error)
// StandingInstruction() is NOT produced here: SP-01 already shipped it in
// internal/rehydrate/standing.go:22 and redeclaring it is a compile error. This branch only calls it.
func AffordanceNotice() string             // §8.6 item 8, one line naming the eight tools
func Wrap(seq core.CheckpointSeq, body string) string           // §8.5 injection tagging
func Unwrap(s string) (body string, seq core.CheckpointSeq, ok bool)

// Reporter persists the last emitted drop report and structurally satisfies mcp.DropReporter,
// which is what SP-13's `dropped` tool is constructed against.
type Reporter interface {
    CurrentDrops(ctx context.Context, sess core.SessionID) ([]checkpoint.DropEntry, error)
    Record(ctx context.Context, sess core.SessionID, st State) error
    Reset(ctx context.Context, sess core.SessionID) error   // the SessionStart source=clear branch
}
type State struct {
    Session   core.SessionID     `json:"session"`
    Seq       core.CheckpointSeq `json:"seq"`
    Emitted   core.UnixMilli     `json:"emitted"`
    Tokens    core.Tokens        `json:"tokens"`
    Budget    core.Tokens        `json:"budget"`
    Items     []ItemStat         `json:"items"`
    Dropped   []checkpoint.DropEntry `json:"dropped"`
    Degraded  bool               `json:"degraded"`
}
// NO `sentinel` KEY. The §12.1 `hook.additional_context_delivered` probe is SP-05's shipped
// contract.MintSentinel/RenderSentinel mechanism, described below; nothing in this slice mints,
// stores or mirrors a sentinel.
type ItemStat struct {
    Kind      string      `json:"kind"`
    Rank      int         `json:"rank"`
    Tokens    core.Tokens `json:"tokens"`
    Truncated bool        `json:"truncated"`
    Units     int         `json:"units"`
    UnitsSeen int         `json:"units_seen"`
}
func NewReporter(projectRoot string, log logging.Logger) Reporter
```

`internal/rules`:

```go
type Rule struct{ Path string; Globs []string; Body string; Tokens core.Tokens; Nested bool }
type Scanner interface {
    PathScoped(ctx context.Context, root string, pointers []string) ([]Rule, error)
    NestedClaudeMD(ctx context.Context, root string, pointers []string) ([]Rule, error)
}
func New(opts ...Option) Scanner       // ZERO required arguments — the shipped signature is `New() Scanner`
type Option func(*scanner)             // additive, variadic: New() keeps compiling unchanged
func WithLogger(log logging.Logger) Option
func Match(pattern, key string) bool   // "**"-aware glob, exported for tests and SP-14
```

**`New` keeps its shipped zero-argument call form, and the logger travels as an option.** `internal/rules/rules.go:41` ships `func New() Scanner`, and `internal/rules/rulestest/suite_test.go:15` calls `rules.New()` argument-free. That call site cannot be changed to pass a logger: a `<pkg>test` subpackage may import only its own package, `testutil` and `core` (00-ARCHITECTURE §3.2), so it cannot construct a `logging.Logger` at all. A variadic `Option` parameter is source-compatible with `New()` — the conformance suite and every wave-0 composition root keep compiling untouched, and `internal/daemon` passes `rules.New(rules.WithLogger(log))`. The same rule applies to `skills.New` below. **No `suite_test.go` in `rulestest`, `skillstest` or `rehydratetest` is modified by this branch.**

`internal/skills`:

```go
type Entry struct{ Name, Description, Source string }
type Indexer interface {
    Index(ctx context.Context, root string, budget core.Tokens) ([]Entry, core.Tokens, error)
}
func New(opts ...Option) Indexer       // ZERO required arguments — the shipped signature is `New() Indexer`
type Option func(*indexer)
func WithLogger(log logging.Logger) Option
// BodyTokens reports the on-disk skill body size so the drop report can warn that Claude Code
// re-injects at most 5K per skill, head-first (§2.7, G4.3). Package function, not an interface
// method, so skills.Indexer stays exactly as §5.15 declares it.
func BodyTokens(root string, e Entry) (core.Tokens, error)
```

`internal/daemon` (one new SP-11-owned file):

```go
type RehydrateOptions struct {
    ProjectRoot string
    Cfg         config.Config
    Checkpoints checkpoint.Reader
    Deps        rehydrate.Deps
    Reporter    rehydrate.Reporter
    Contract    contract.Monitor
    Log         logging.Logger
    Metrics     obs.Registry
    Clock       core.Clock
}
// NewRehydrateService returns an observer.Rehydrator. It is nil-tolerant at every
// call site.
func NewRehydrateService(o RehydrateOptions) observer.Rehydrator

// BindRehydrate installs svc into the late-bound Services set (§5.4) through the Bind seam SP-05
// already provides, adding NO member to daemon.Services. Setting Services.Rehydrate is what makes
// DeclareProducers declare contract.CAdditionalContext (internal/daemon/options.go:150-152) — the
// exit criterion below is unreachable without it, and Services.Rehydrate is referenced nowhere
// else in the package, i.e. it is the seam SP-05 provisioned for exactly this branch.
func BindRehydrate(o *daemon.Options, svc observer.Rehydrator)
```

**Consumers.** SP-13 constructs `mcp.ToolDeps{Rehydrator: <rehydrate.Reporter>}` for the `dropped` tool. SP-14's `/qompack:status` reads `.qompack/state/rehydrate-<session>.json` for the "last rehydration" panel. SP-16 reads `State.Items` to tune the tier boundaries from replay evidence.

**The §12.1 `hook.additional_context_delivered` sentinel is SP-05's, already shipped, and this slice mints nothing.** Read `internal/contract/sentinel.go` and `internal/daemon/handlers.go` before writing a line of this branch, because the mechanism on `develop` is complete and differs in every particular from anything a rehydrator might invent:

1. **The daemon mints it.** The `session.start` route calls `contract.MintSentinel(ev.SessionID, now)`, whose token is `"qompack-contract-" + core.HashBytes("qompack.sentinel.v1", []byte(string(sess)+strconv.FormatInt(int64(now),10))).Short()` — seeded on the **mint timestamp**, never on a checkpoint sequence number.
2. **The daemon appends it.** After the `SessionStart` seam returns, and only when `mode.MayAct()`, the route appends `contract.RenderSentinel(s)` — the line `<!-- qompack-contract-probe <token> -->` — to whatever `additionalContext` the seam produced, storing the token in `History.Sentinel.Token`. The append happens *after* this slice's `Wrap`, so the probe line sits **outside** the `<!-- /qompack:injected -->` close tag. That is correct and must not be "fixed": the probe is a contract observable, not injected context, and `checkpoint.StripInjections` must not swallow it.
3. **The daemon reads it back.** `scanSentinelForPrompt` scans the transcript tail for `h.Sentinel.Token` and calls `RecordSentinelScan`; `checkAdditionalContextDelivered` reads only `h.Sentinel.Observed`/`.Chances`. **Nothing reads a rehydrate state file**, and nothing recomputes the token from `(session, seq)`.

Therefore this subplan has **no** `rehydrate.Sentinel`, no `sentinelDomain` constant, no `internal/rehydrate/sentinel.go`, and no `"sentinel"` key in the state file. Reusing the identical domain string `qompack.sentinel.v1` with a different seed and prefix — which an earlier draft of this plan specified — would have put two mutually incompatible "sentinels" in one process under one domain. What this branch owes §12.1 is exactly one thing: a **non-empty `additionalContext`** on `SessionStart(source=compact)`, produced by a bound `Services.Rehydrate` seam so `DeclareProducers` stops reporting `not-yet-implemented` (see the wiring section below).

---

## Implementation spec

### File map

| Path | Action | Responsibility |
|---|---|---|
| `internal/rules/glob.go` | create | `**`-aware glob matcher over `paths.Key` form |
| `internal/rules/frontmatter.go` | create | minimal YAML-subset frontmatter reader (`paths:`, `description:`) |
| `internal/rules/scanner.go` | create | `Scanner` impl: `PathScoped`, `NestedClaudeMD`, `New` |
| `internal/rules/glob_test.go`, `frontmatter_test.go`, `scanner_test.go` | create | tests |
| `internal/skills/frontmatter.go` | create | minimal frontmatter reader for `SKILL.md` (`name:`, `description:`) |
| `internal/skills/indexer.go` | create | `Indexer` impl, `New`, `BodyTokens` |
| `internal/skills/indexer_test.go` | create | tests |
| `internal/rehydrate/build.go` | modify (replace SP-01 stub body) | `Build`, `Deps` wiring, degradation handling — **the stub lives in `build.go`; there is no `internal/rehydrate/rehydrate.go`** |
| `internal/rehydrate/items.go` | create | the ten item builders, each returning ordered units |
| `internal/rehydrate/budget.go` | create | clamp, reserves, share allocation, carry-forward, min-fill pass |
| `internal/rehydrate/render.go` | create | payload rendering, `Wrap`/`Unwrap`, `AffordanceNotice`. **Not `StandingInstruction`** — SP-01 already shipped it in `internal/rehydrate/standing.go:22`, and redeclaring it is a compile error |
| `internal/rehydrate/drops.go` | create | `Reporter`, `State`, `ItemStat`, state-file I/O |
| `internal/rehydrate/*_test.go`, `fake_test.go`, `bench_test.go` | create | tests, fakes, benchmarks |
| `internal/daemon/rehydrate_service.go` | create | SessionStart `compact` + `clear` branches, `BindRehydrate` |
| `internal/daemon/` observer construction site (SP-05/SP-08 wiring) | modify | pass the service into the observer's deps, and call `BindRehydrate` so `Services.Rehydrate` is set |
| `internal/cli/daemon.go` | modify | **create** the daemon bootstrap block: construct `symbols`, `store` (with `Deps.Symbols` set), `dag`, `negknow` and a `checkpoint.Reader` (typed-nil on the branch, `checkpoint.OpenReader` after the rebase onto SP-10), then build the service and `BindRehydrate(o, svc)`. SP-12 only reads these fields; SP-13 extends the block with `rehydrate.NewReporter`/`mcp.NewPromoter`/`InstallMCPOp` and owns its final shape. SP-11 is the first wave-3 branch that needs it (prerequisite 1) |
| `test/e2e/sessionstart_compact_test.go` | create | real binary + real daemon, `source=compact` and `source=clear` |
| `test/replay/policy_rehydrate.go` | create | `qompackRehydratePolicy` and the session→checkpoint adapter — a **non-test** file in `package main`, so the driver binary can register and run it |
| `test/replay/phases.go` | modify | add the `3: phase3` entry and the `phase3(Context) error` check |
| `test/replay/phase3_rehydrate_test.go` | create | unit coverage for the policy and the check, against a hand-built `Context` |
| `testdata/fixtures/rules/proj-a/**` | create | fixture tree: `paths:` rules, nested `CLAUDE.md`, skills |
| `testdata/fixtures/rules/proj-edge/**` | create | edge-case tree: CRLF, unicode, spaces, unreadable, deep nesting |
| `testdata/golden/rehydrate/*.txt` | create | golden payloads |
| `docs/adr/0011-rehydration-budget-and-item-order.md` | create | ADR |

**No `<pkg>test` conformance suite is edited on the feature branch.** The three suites SP-11 must turn green — `internal/rehydrate/rehydratetest` (note the path: it is *under* `internal/rehydrate`, not `internal/rehydratetest`), `internal/rules/rulestest` and `internal/skills/skillstest` — un-skip themselves. Each calls `skipIfStub`, which probes the implementation and skips only while it still returns `core.ErrNotImplemented`; the moment `Build`, `PathScoped` and `Index` stop being stubs, the guarded `/behaviour` block runs on its own. There is nothing to "flip". The one exception is the §0 amendment of step 0 below, which edits `internal/rehydrate/rehydratetest/behaviour.go` on the `arch/` branch, before this branch is cut.

### `internal/rules/glob.go`

`path.Match` does not support `**`, and 00-ARCHITECTURE §2.5 lists glob matching as "stdlib or in-repo". Implement it in-repo.

```go
// Match reports whether pattern matches key. Both are paths.Key form: project-relative,
// forward-slash, cleaned, and case-folded on Windows/macOS by the caller.
//
// Grammar:
//   **      matches zero or more whole path segments
//   *       matches zero or more characters within one segment
//   ?       matches exactly one character within one segment
//   [a-z]   character class, delegated to path.Match on the segment
// A pattern with no '/' is matched against every segment tail as if prefixed with "**/",
// so "*.ts" matches "src/api/routes.ts". A trailing "/**" also matches the directory itself.
func Match(pattern, key string) bool {
    pattern = strings.TrimPrefix(path.Clean(pattern), "./")
    key = strings.TrimPrefix(path.Clean(key), "./")
    if !strings.Contains(pattern, "/") {
        pattern = "**/" + pattern
    }
    if strings.HasSuffix(pattern, "/**") {
        if matchSegs(strings.Split(strings.TrimSuffix(pattern, "/**"), "/"), strings.Split(key, "/")) {
            return true
        }
    }
    return matchSegs(strings.Split(pattern, "/"), strings.Split(key, "/"))
}

// matchSegs is the standard two-pointer wildcard matcher lifted to whole segments.
func matchSegs(pat, seg []string) bool {
    if len(pat) == 0 {
        return len(seg) == 0
    }
    if pat[0] == "**" {
        // "**" consumes 0..len(seg) segments; try shortest first for determinism.
        for i := 0; i <= len(seg); i++ {
            if matchSegs(pat[1:], seg[i:]) {
                return true
            }
        }
        return false
    }
    if len(seg) == 0 {
        return false
    }
    ok, err := path.Match(pat[0], seg[0])
    if err != nil || !ok {
        return false
    }
    return matchSegs(pat[1:], seg[1:])
}
```

Complexity is O(|pat| · |seg|) in the worst case with `**`; patterns are ≤ 8 segments and keys ≤ 24 segments, so this is microseconds. A `MaxPatternSegments = 32` guard rejects (returns `false` for) pathological patterns rather than recursing.

### `internal/rules/frontmatter.go`

Frontmatter is delimited by a line `---` at byte 0 and a closing `---` line. Only three constructs are supported, which is the complete set Claude Code rule files use:

```
---
paths:
  - "src/**/*.ts"
  - api/**
description: API conventions for the REST layer
---
```

```
---
paths: ["src/**/*.ts", "api/**"]
---
```

```
---
paths: src/**/*.ts
---
```

```go
type Front struct {
    Paths       []string
    Description string
    Present     bool   // true iff a well-formed frontmatter block was found
}

// Parse reads the frontmatter block at the head of b and returns it plus the body offset.
// It never returns an error: a malformed block yields Front{Present:false} and body=0, which
// makes the file an unscoped rule that Claude Code re-injects itself (§2.7) — correctly ignored.
func Parse(b []byte) (Front, int)
```

Rules, in order:
1. The file must begin with `---` followed by `\n` or `\r\n`. Otherwise `Present=false`, body offset 0.
2. Scan lines until a line whose trimmed content is exactly `---`. If EOF first, `Present=false`.
3. For each line inside: split on the first `:`. Key is trimmed and lowercased. Only `paths` and `description` are retained; every other key is ignored (forward compatibility).
4. `paths` value forms: empty → collect subsequent lines matching `^\s*-\s*(.+)$` until a non-list line; `[a, b]` → split on `,`; anything else → single element.
5. Every collected element is trimmed of whitespace and of a single matching pair of `"` or `'`. Empty elements are discarded.
6. `description` is taken as the remainder of the line, trimmed, quotes stripped, truncated to the first `\n`.

Max frontmatter size scanned: 8 KiB. Beyond that, `Present=false` (a rule file with 8 KiB of frontmatter is not a rule file).

### `internal/rules/scanner.go`

```go
type scanner struct{ log logging.Logger }

// Option configures a Scanner. New takes options, not required arguments, because
// internal/rules/rulestest/suite_test.go calls rules.New() argument-free and — being a <pkg>test
// subpackage restricted to its own package, testutil and core (§3.2) — cannot construct a logger
// to pass. A variadic parameter keeps that call site compiling untouched.
type Option func(*scanner)

func WithLogger(log logging.Logger) Option { return func(s *scanner) { if log != nil { s.log = log } } }

func New(opts ...Option) Scanner {
    s := &scanner{log: logging.Nop()}
    for _, o := range opts {
        o(s)
    }
    return s
}
```

Discovery roots — normative, fixed, no config key (Appendix C and §11.5 have none, and SP-01 owns `config`):

```go
// scanGlobs are relative to projectRoot and are searched in this order.
var scanGlobs = []string{
    ".claude/rules",   // recursive: every *.md below it
    ".claude",         // non-recursive: every *.md directly inside
}
//nomagic:allow byte ceiling for rule-file reads; unrelated to runtime.mcp.maxResponseBytes
const maxRuleBytes = 262144   // a rule file larger than 256 KiB is not a rule file
```

The `//nomagic:allow` line is mandatory, not decorative: `262144` is also the default of `runtime.mcp.maxResponseBytes`, and 00-ARCHITECTURE §2.6 describes the pass as forbidding "literals that duplicate a config default". The annotation is the escape hatch §11.6 names explicitly, and the reason text must say why this is a different quantity.

**`PathScoped(ctx, root, pointers)`**

1. Normalize every pointer: `k, err := paths.Norm(root, p)`; on error skip that pointer and continue. Then `key := paths.Key(k)`. Deduplicate, preserving first-seen order.
2. Walk the discovery roots with `filepath.WalkDir`, collecting `*.md` files (recursive under `.claude/rules`, depth-1 under `.claude`). Skip symlinks (`d.Type()&fs.ModeSymlink != 0`). Skip files larger than `maxRuleBytes` and record a `Rule` **not** returned but logged at Debug.
3. For each candidate, `os.ReadFile`. On error (permission, deleted mid-walk): **skip the file and log at Debug**, then continue the walk. `PathScoped` returns `(rules, nil)` for per-file failures: the caller cannot act on them and §13 invariant 6 says the hook must not fail. Only a failure to walk the discovery roots themselves (`filepath.WalkDir` returning an error for the root) produces a non-nil error.
   *No metric is emitted from `rules`.* `Scanner` is fixed by §5.15 and carries no `obs.Registry` seam, and `rules` may not grow one without a §0 amendment. Unreadable rule files are visible in the Debug log only; the *aggregate* signal the operator needs — "instructions were not restored" — is already surfaced by the drop report and by `State.Items`, which is the §12.1-mandated observable.
4. Parse frontmatter. If `!Present` or `len(Paths)==0`, skip the file (unscoped rules are re-injected by the host, §2.7).
5. The rule matches iff **any** of its globs matches **any** pointer key, using `Match(paths.Key(glob), key)`. Case folding of the pattern uses `paths.Key` so a Windows-cased pattern matches a lowercased key.
6. Build `Rule{Path: <project-relative forward-slash path>, Globs: <as parsed, original case>, Body: <bytes after frontmatter, CRLF→LF normalized, right-trimmed>, Tokens: core.Tokens((len(Body)+3)/4), Nested: false}`.
7. Sort the result by `Path` ascending (byte order) and return. Determinism is required by the golden tests.

`Tokens` is a **baseline advisory value**: `rehydrate.Build` overwrites it with `Deps.Tokens.EstimateString(r.Body, tokens.ClassProse)` before any budget arithmetic. `rules` may not import `tokens` (§3.2), which is why the baseline exists at all; the documented contract is "advisory unless the caller re-estimates."

**`NestedClaudeMD(ctx, root, pointers)`**

**The two normative documents and the shipped code disagree here, and this plan does not get to settle that silently.** State it plainly:

- `Qompack.md:971` (§8.6) — *"Every nested `CLAUDE.md` in a directory containing a pointer-set file"*. The **containing** directory only.
- `plans/00-ARCHITECTURE.md:1813` (§5.15) — `// NestedClaudeMD returns CLAUDE.md files in directories containing a pointer-set file.` The same narrow reading, restated.
- `internal/rules/rules.go:32-33` (shipped, SP-01) — "returns every CLAUDE.md under root that lives in a directory containing (**or ancestor to**) a path in pointers", and the inherited Rule W-1 conformance case `nested_claude_md_discovery` puts the file at `src/pkg/CLAUDE.md` with the pointer at `src/pkg/deep/thing.go` — an ancestor directory — asserting `require.Len(t, matched, 1)` (`internal/rules/rulestest/suite.go:66-80`, fixture at :102-119). The **broader** reading, and it is executable.

The two documents landed together in `d361f06` (the initial commit) and the code in `3464079` (SP-01's conformance-suite commit), with no commit in between recording a decision to widen — so this is a genuine open design question, not settled precedence. Both readings are implementable, and each costs an amendment: the narrow one requires editing `rules.go`'s doc comment **and** moving the `rulestest` fixture's `CLAUDE.md` from `src/pkg` down to `src/pkg/deep`; the broad one requires amending 00-ARCHITECTURE §5.15:1603 to the "containing, or ancestor to" wording **and** a PR against `Qompack.md:971` (which this plan set may not edit — see `plans/README.md:62`). Neither is free, and 00-ARCHITECTURE.md:5's precedence rule — "disagree on *what* to build, Qompack.md wins" — points at the narrow one, while the executable conformance case points at the broad one.

**This plan is written for the broad reading — walk ancestors — and that choice is conditional, not unilateral.** The §5.15 behavioural amendment folds into the `arch/` branch step 0 already opens and merges to `develop` before this feature branch is cut, and the `Qompack.md:971` correction PR must be raised in the same round. Until both land, the ancestor walk below is unauthorized and this section is the open decision, not the answer. The reason to prefer it: a nested `CLAUDE.md` governs its whole subtree in Claude Code, so a rule two directories above a pointer is exactly as lost after compaction as one beside it, and a containing-directory-only implementation returns an empty slice for the `nested_claude_md_discovery` fixture and fails the inherited suite the moment its stub probe stops skipping.

1. Normalize pointers as above.
2. For each pointer key `k`, walk **upward** from `d := path.Dir(k)` toward the project root, one directory at a time: `d`, `path.Dir(d)`, … The walk **stops before `"."`** — the project-root `CLAUDE.md` is re-injected by Claude Code itself (§2.7) and duplicating it wastes budget — and is bounded by `maxAncestorDepth = 32` levels, which no real pointer path exceeds and which makes a malformed key incapable of looping.
3. At each level, candidate `c := path.Join(d, "CLAUDE.md")`. Stat `filepath.Join(root, filepath.FromSlash(c))`; if it exists and is a regular file ≤ `maxRuleBytes`, read it. A level with no `CLAUDE.md` does **not** stop the walk — the file two levels up is still returned.
4. **Deduplicate as you go, keyed on `c`.** One `CLAUDE.md` covering forty pointers is read once and emitted once; the same `stat` is not repeated for a directory already visited on this call.
5. Emit `Rule{Path: c, Globs: nil, Body: <whole file, CRLF→LF, right-trimmed; frontmatter, if any, retained>, Tokens: baseline, Nested: true}`.
6. Sort ascending by `Path` and return. Because a deeper `CLAUDE.md` sorts after its ancestor, the ordering is outermost-first, which is the order Claude Code itself applies nested instructions in — the general rule before the specific one that refines it.

Case-insensitive filesystems: the stat is performed on the literal `CLAUDE.md` spelling; on Windows/macOS a `claude.md` on disk will satisfy it, which is correct behaviour. The returned `Path` is always the canonical `CLAUDE.md` spelling so goldens are stable across platforms.

### `internal/skills/indexer.go`

Discovery, normative:

```go
// relative to projectRoot, searched in this order:
//   .claude/skills/<name>/SKILL.md     → Name from frontmatter `name:`, else <name>
//   .claude/skills/<name>.md           → Name from frontmatter `name:`, else <name>
//nomagic:allow byte ceiling for SKILL.md reads; unrelated to runtime.hotPath.maxPayloadBytes
const maxSkillBytes = 1048576
const maxDescriptionRunes = 100
```

`skills/frontmatter.go` is a near-copy of `rules/frontmatter.go` restricted to `name:` and `description:`. **Duplication is deliberate and required:** §3.2 permits `rules` and `skills` to import foundation packages only, so they cannot share a helper, and adding one to `core` would edit SP-01's package. Both files carry the comment `// Duplicated from internal/rules/frontmatter.go by §3.2 import policy — do not "fix".`

```go
type indexer struct{ log logging.Logger }

// Option and WithLogger mirror internal/rules exactly, and for the same reason: the shipped
// constructor is `func New() Indexer` (internal/skills/skills.go:33) and
// internal/skills/skillstest/suite_test.go calls it argument-free from a subpackage that may not
// import logging (§3.2).
type Option func(*indexer)

func WithLogger(log logging.Logger) Option { return func(ix *indexer) { if log != nil { ix.log = log } } }

func New(opts ...Option) Indexer {
    ix := &indexer{log: logging.Nop()}
    for _, o := range opts {
        o(ix)
    }
    return ix
}

func (ix *indexer) Index(ctx context.Context, root string, budget core.Tokens) ([]Entry, core.Tokens, error)
```

1. Walk both discovery shapes; skip symlinks and files > `maxSkillBytes`.
2. `Entry.Name`: frontmatter `name:` if non-empty, else the directory or file base name with `.md` stripped. `Entry.Description`: frontmatter `description:`, first line, trimmed, truncated to `maxDescriptionRunes` runes with a trailing `…` when truncated; if absent, the first non-empty, non-heading (`#`) line of the body under the same truncation; if still empty, the literal `"(no description)"`.
3. `Entry.Source`: project-relative forward-slash path of the file.
4. Sort by `Name` ascending (byte order), tiebreak by `Source` ascending.
5. Fill against `budget` using the same baseline estimator as `rules` — the rendered line for an entry is `"- " + Name + ": " + Description + "\n"`, and its cost is `core.Tokens((len(line)+3)/4)`. Add entries in sorted order while `used + cost <= budget`. Stop at the first entry that does not fit (**do not** skip ahead to a smaller one — deterministic prefix truncation is what makes the index reproducible and what the drop report reports on).
6. Return `(kept, used, nil)`. Entries that did not fit are **not** returned; the caller recovers them by calling `Index` again with a very large budget when it needs the full set for the drop report. To avoid a double walk, `Index` returns all entries when `budget <= 0`, and `rehydrate` calls it exactly twice: once with `0` to learn the full set, once with the real budget. Document this explicitly on the interface method.

```go
// BodyTokens reports the baseline token size of a skill body on disk.
func BodyTokens(root string, e Entry) (core.Tokens, error)
```
Reads `filepath.Join(root, filepath.FromSlash(e.Source))`, strips frontmatter, returns `core.Tokens((len(body)+3)/4)`. Used by the drop report to flag skills that Claude Code will head-truncate.

### `internal/rehydrate/render.go`

```go
const payloadVersion = 1

func Wrap(seq core.CheckpointSeq, body string) string {
    return fmt.Sprintf(checkpoint.InjectionOpenTag, int(seq), payloadVersion) + "\n" +
        body + "\n" + checkpoint.InjectionCloseTag
}

// Unwrap is the inverse; ok=false when the tags are absent or malformed. Used by tests and by
// SP-10's regeneration guard when it needs to identify prior injections (§8.5).
func Unwrap(s string) (string, core.CheckpointSeq, bool)

// StandingInstruction is NOT redeclared here. SP-01 already shipped it in
// internal/rehydrate/standing.go:22, returning the §8.7 sentence verbatim; a second declaration in
// this file is a compile error.

func AffordanceNotice() string {
    // §8.6 item 8: "one line telling the agent that recall, re_read, and already_tried exist".
    //
    // It deliberately does NOT append StandingInstruction(). The inherited conformance case
    // runStandingInstructionCase (internal/rehydrate/rehydratetest/behaviour.go:216-233) asserts
    // that with no item 3, the payload does not contain the sentence at all — "the standing
    // instruction belongs to item 3; with no item 3 it must not be emitted". Ending item 8 with it
    // would make that assertion fail on every checkpoint with an empty eliminations set. §8.7 asks
    // for a standing instruction alongside surfaced negative knowledge, and an instruction to call
    // already_tried when nothing has been tried is noise, not a standing instruction.
    return "Qompack retrieval is available: recall(query,k) · expand(hash|tool_use_id) · " +
        "re_read(path,at) · already_tried(target,approach) · record_eliminated(target,approach,reason) · " +
        "timeline(from,to) · why(decision_id) · dropped()."
}
```

**Payload layout, byte-for-byte.** The body is the concatenation, in `renderOrder`, of each non-empty item's text. Every item text ends with exactly one `\n`; items are joined with one additional `\n` (one blank line between sections). The document header precedes item 1.

```
<!-- qompack:injected seq=7 ver=1 -->
# Qompack rehydration — checkpoint 0007, session 3f2a9c81

## 1. Invariants (pinned, verbatim)
- [inv_a1b2c3d4e5f6] Never bypass the connection pool in transaction mode. (source: /qompack:pin)

## 2. Original user intent (verbatim from L0 capture — never summarized)
> Fix the Stripe webhook retry logic; payments are being double-charged under load.
Evolution:
> Narrowed to the pgbouncer transaction-mode path only.

## 3. Approaches already eliminated (top 8 of 23 by slice relevance)
- src/auth.ts:refreshToken — "widen pool timeout" — pgbouncer 1.18 ignores it in transaction mode [active] [evidence sha256:a3f2c81d0e45]
- src/webhooks/retry.ts — "client-side backoff" — duplicates the charge under concurrent delivery [stale: previously eliminated, but the evidence has changed since — re-verification may be warranted] [evidence sha256:77b1a0c9de32]
15 further eliminations are recorded and not shown. Before committing to an approach, call already_tried.

## 4. Decisions
- [dec_9f81c0a2b3d4] (turn 34) Move retry bookkeeping into the outbox table
  why: the pool bypass makes in-request retries unsafe under pgbouncer transaction mode
  rejected: client-side backoff; advisory locks
  evidence: sha256:5c11ae90b7f2

## 5. Current work
goal: make webhook replay idempotent
next step: add the unique index migration and re-run the load test
blocked on: none

## 6. Pointers (paths and hashes — contents are NOT restored; use expand/re_read)
- src/webhooks/retry.ts sha256:1c4fa27b8e90 — holds the retry loop under change
- tool_use toolu_01H8Z sha256:9ab2d4f16c08 — failing load-test output

## 6a. Restored instructions (re-read from disk by Qompack; the host does not restore these)
### .claude/rules/api-conventions.md — paths: src/api/**
All REST handlers return the envelope type; never a bare object.
### src/webhooks/CLAUDE.md — nested
Webhook handlers must be idempotent on delivery id.

## 6b. Skill index (names and one-line descriptions only)
- code-review: Review the current diff for correctness bugs.
- migration-runner: Apply and verify database migrations against a scratch database.

## 7. No longer in context
- path_rule .claude/rules/db-conventions.md — matched src/db/pool.ts; did not fit the rehydration budget
- skill migration-runner — body ~7500 tokens; the host re-injects at most 5000 per skill, head-first, so treat it as partial (§2.7)
- elimination — 15 of 23 not shown; call already_tried(target, approach) or dropped()
- nested_claude_md src/db/CLAUDE.md — did not fit the rehydration budget

## 8. Retrieval
Qompack retrieval is available: recall(query,k) · expand(hash|tool_use_id) · re_read(path,at) · already_tried(target,approach) · record_eliminated(target,approach,reason) · timeline(from,to) · why(decision_id) · dropped().
<!-- /qompack:injected -->
```

The daemon appends `<!-- qompack-contract-probe … -->` on its own line *after* the close tag, from the `session.start` route. That line is not this slice's to render, and no golden in this branch contains it.

`renderOrder` is a package `var` and the source of `Item.Rank` (**0-based** index among **emitted** items):

```go
var renderOrder = []ItemKind{
    ItemInvariants, ItemUserIntent, ItemEliminations, ItemDecisions, ItemCurrentWork,
    ItemPointers, ItemRestoredInstructions, ItemSkillIndex, ItemDropReport, ItemAffordance,
}
```

**`Item.Rank` is 0-based, and `renderOrder` is `iota` order.** Both are fixed by the inherited conformance case, not chosen here: `runItemOrderCase` asserts `for i, it := range got.Items { require.Equal(t, i, it.Rank, "Rank must be the item's own position in the emitted order") }` and `require.Greater(t, kinds[i], kinds[i-1], "items must be emitted in ascending ItemKind")` (`internal/rehydrate/rehydratetest/behaviour.go:139-155`). After the step-0 amendment the ten constants are declared in rendered order, so `renderOrder` *is* the `iota` sequence and both assertions hold by construction. A test asserts `renderOrder[i] == ItemKind(i)` for all ten, so the §8.6 importance ordering cannot drift — and because the emitted `Items` slice is a subsequence of `renderOrder`, an omitted kind consumes no rank.

### `internal/rehydrate/items.go`

Every builder returns `(units []unit, seen int)` where

```go
type unit struct {
    text   string      // rendered line(s), always ending in "\n"
    tokens core.Tokens // filled by the caller with Deps.Tokens.EstimateString(text, ClassProse)
    drop   checkpoint.DropEntry // the DropEntry to emit if this unit does not fit
}
```

`seen` is the total number of candidate units before budgeting, so item 3 can render "15 further eliminations are recorded" and `ItemStat.UnitsSeen` is accurate.

**Item 1 — invariants (`buildInvariants`).** One unit per `Checkpoint.Invariants` entry, rendered `"- [" + ID + "] " + Text + " (source: " + Source + ")\n"`; `Source` omitted with its parenthetical when empty. Order: as stored in the checkpoint (SP-10 writes them append-ordered; §7.4 makes pins append-only, so this is stable). **Never truncated** — §8.6 says "verbatim, always" and §8.5 puts invariants in tier 1 ("never truncated"). `drop` is the zero `DropEntry` and is never consulted.

**Item 2 — verbatim original user intent (`buildUserIntent`).** This is the G7.3/G2.3 closure and it does not trust the summary path.

```go
// firstPromptID is the tool_use id SP-08 records this session's FIRST UserPromptSubmit capture
// under. SP-08's scheme is VerbatimPromptID(session, turn) == "prompt_<session>_<turn>"
// (plans/V3-SP-08-observer-l0.md:434, :1616), so turn 0 of session s is derivable without a
// search and without an import edge: rehydrate may not import observer (§3.2).
func firstPromptID(s core.SessionID) core.ToolUseID { return core.ToolUseID("prompt_" + string(s) + "_0") }

const maxIntentBytes = 8192
```

**Do not resolve the original intent with `Store.Search`.** `FSStore.Search` clamps `K` to `maxK = 100` (`internal/store/search.go:40, 62-67`), gathers candidates project-wide, and — with only `Tool` set, so `needContent == false` and the score reduces to `wTool + wRecency*recencyRank` — returns the 100 **most recent** prompts (`search.go:230-236`). The earliest prompt of a long session is therefore the first thing the window excludes, and the failure is not benign: the filtered candidate set is not empty, it holds this session's *later* prompts, so a relevance search would inject a mid-session prompt as "the verbatim original user intent" and then override the checkpoint's genuine original with it. That inverts G7.3 and G2.3, the closure this item exists for. `Store.ToolUse` on a derived id is exact, is O(1), and cannot degrade in that direction.

Do **not** read `Event.TranscriptPath` as a substitute either: the transcript is exactly the summary-contaminated surface §8.5's regeneration rule forbids as a source.

Algorithm:
1. `rec, err := d.Store.ToolUse(ctx, firstPromptID(r.Session))`.
2. On error — including `core.ErrNotFound`, which is the ordinary case for a session whose first prompt predates the store, or one whose L0 capture never ran — or on `rec.Root == (core.Hash{})`: fall back to `Checkpoint.UserIntent.Original`, append `DropEntry{Kind:"user_intent_source", ID:"l0", Detail:"L0 verbatim capture unavailable; using the checkpoint copy, which §8.5 guarantees is itself verbatim-from-L0 and never regenerated"}`, and log at Info (not Loud — this is a degraded-but-correct path, not a contract violation).
3. **Sanity-check the record before trusting it**, then read it. `rec.Session` must equal `r.Session` and `rec.Turn` must be `0`; either mismatch means the id scheme drifted, so take the step-2 fallback and log at Warn rather than injecting a record this slice cannot vouch for. Then `rc, err := d.Store.Open(ctx, rec.Root)`; read at most `maxIntentBytes`; `text := strings.TrimSpace(checkpoint.StripInjections(string(b)))`. An empty `text` is also the step-2 fallback.
   **Verify the id scheme before implementing.** `prompt_<session>_<turn>` is SP-08's (wave 2) and 00-ARCHITECTURE does not name it. On the `develop` this branch is cut from, confirm it by reading `observer.VerbatimPromptID` and the `store.ToolUseRecord.Tool` value written on the `UserPromptSubmit` path. If SP-08 shipped a different spelling, **use SP-08's** and open `arch/l0-prompt-capture-id` off `develop` to record the scheme in 00-ARCHITECTURE §5.21 so the next reader does not have to grep. `TestUserIntent_FirstPromptIDMatchesObserver` is the regression guard and asserts the string against `observer.VerbatimPromptID(sess, 0)` from `test/e2e`, which may import both packages.
4. **Cross-check.** If `Checkpoint.UserIntent.Original != ""` and its `strings.TrimSpace` form differs from `text`, **L0 wins**, and emit `DropEntry{Kind:"intent_mismatch", ID:string(r.Session), Detail:"checkpoint user_intent.original differs from the L0 capture; injecting the L0 text"}` plus `d.Log.Loud(...)`. This is the mechanical guarantee that no summary-derived intent can reach the context window.
5. Render: `"> " + <each line of text, prefixed>` then, when `Checkpoint.UserIntent.Evolution` is non-empty, `"Evolution:\n"` followed by one `"> "`-prefixed unit per evolution entry.
6. The original-intent unit is **never truncated**. Evolution units are truncatable and each carries `DropEntry{Kind:"user_intent_evolution", ID:strconv.Itoa(i)}`.

`StripInjections` is applied because a prior rehydration's payload could, in a pathological transcript, have been captured as a prompt; §8.5's tagging exists precisely so this material is identifiable and ignorable.

**Item 3 — eliminated-approaches digest (`buildEliminations`).**

1. Candidate set: the union of **both** scopes — `d.Ledger.Active(ctx, negknow.Scope("session"))` and `d.Ledger.Active(ctx, negknow.Scope("project"))` — further unioned with `Checkpoint.Eliminated`, deduplicated by `Record.ID` (ledger copy wins — it carries current `Status`). If either `Active` call errors, use whatever the other returned; if both error, fall back to `Checkpoint.Eliminated` alone. On any ledger error emit `DropEntry{Kind:"elimination_source", ID:"ledger", Detail:err.Error()}`.
   **Both scopes, deliberately.** `eliminations.defaultScope` (Appendix C default `"session"`) governs the scope *new* records are written with; it is not a read filter. §8.3 says project-scoped eliminations "persist and warm-start future sessions", so a rehydration that consulted only the default scope would drop exactly the longest-lived negative knowledge in the store — the inverse of G6.2. A test asserts a project-scoped record appears when `defaultScope == "session"`.
2. **Stale records are included, not filtered**, when `r.Cfg.Eliminations.StaleResponse == "flag"` (the Appendix C default) and excluded when it is `"drop"`. A stale record renders the §8.3 note verbatim: `previously eliminated, but the evidence has changed since — re-verification may be warranted`.
3. **Slice scoring.** Criteria node set, in this order, deduplicated:
   - `dag.FileNode(paths.Key(p.Path))` for every `Checkpoint.Pointers.Files` entry
   - `dag.ToolUseNode(t.ToolUseID)` for every `Checkpoint.Pointers.Tools` entry
   - `dag.DecisionNode(dec.ID)` for every `Checkpoint.Decisions` entry

   **Use SP-07's exported constructors, never a hand-built string literal — anywhere in items 3 and 4.** `internal/dag` exports `FileNode`, `SymbolNode`, `ToolUseNode`, `DecisionNode`, `EliminationNode` and `SegmentNode`, and they are the drift-proof form: a mis-spelled prefix does not fail, it returns a zero score for every record, degrading item 3's ordering to TS/ID with no signal. The constructors also encode key *shapes* a literal cannot guess. In particular `dag.SymbolNode(pathKey, name)` keys on `pathKey + "#" + name` — `symbol:src/auth.ts#refreshToken`, and `symbol:#name` for a pathless symbol (`internal/dag/nodeid.go:152-158`; the frozen golden `testdata/golden/contracts/dag/graph-basic.jsonl` carries `symbol:src/auth.ts#refreshToken`). `"symbol:" + rec.Desc.Symbol` matches neither form and can never hit. There is no `arch/dag-nodeid-prefixes` amendment to open: the constructors are already exported and are the contract.
   Call `d.Graph.BackwardSlice(criteria, dag.SliceOptions{Thin: r.Cfg.Selection.Slicing == "thin", MaxNodes: sliceMaxNodes, Decay: sliceDecay, Deadline: sliceDeadline})` with `sliceMaxNodes = 5000`, `sliceDecay = 0.85`, `sliceDeadline = 50 * time.Millisecond`. On error or nil `Graph`, all scores are 0.
   Per-record score:
   ```go
   func recordScore(sc map[dag.NodeID]float32, rec negknow.Record) float32 {
       s := sc[dag.EliminationNode(rec.ID)]
       if rec.Desc.NormalizedPath != "" {
           if v := sc[dag.FileNode(paths.Key(rec.Desc.NormalizedPath))]; v*symbolProxyDecay > s {
               s = v * symbolProxyDecay
           }
       }
       if rec.Desc.Symbol != "" {
           // A record with no path yields SymbolNode("", sym) == "symbol:#sym", which is the exact
           // spelling dag mints for a symbol whose defining file is not known yet.
           if v := sc[dag.SymbolNode(paths.Key(rec.Desc.NormalizedPath), rec.Desc.Symbol)]; v*symbolProxyDecay > s {
               s = v * symbolProxyDecay
           }
       }
       return s
   }
   const symbolProxyDecay float32 = 0.75
   ```
   `TestEliminations_OrderedBySliceScore` and `TestEliminations_SymbolProxyScore` are the regression guards, and both build their fake graph's score map with the same constructors the production code calls.
4. Sort descending by score; tiebreak `TS` descending; tiebreak `ID` ascending. This total order is deterministic and is asserted by a property test.
5. Take the first `r.Cfg.Runtime.Rehydrate.EliminationsTopN` records as units. Render each as
   `"- " + target + " — \"" + approach + "\" — " + reason + " [" + statusTag + "]" + evidenceSuffix + "\n"`
   where `statusTag` is `active` or `stale: previously eliminated, but the evidence has changed since — re-verification may be warranted`, and `evidenceSuffix` is `" [evidence " + Evidence.String() + "]"` when `Evidence != core.Hash{}`, else empty. `reason` is collapsed to a single line (`\n` → `" "`) and truncated to 240 runes with `…`.
6. A **trailing note unit** is always appended when `seen > len(units)`:
   `strconv.Itoa(seen-len(units)) + " further eliminations are recorded and not shown. " + StandingInstruction() + "\n"`.
   When `seen == len(units)` but `seen > 0`, the note is just `StandingInstruction() + "\n"`. **When `seen == 0` the item is omitted entirely and the standing instruction is not emitted anywhere.** That is not an oversight, it is the inherited contract: `runStandingInstructionCase` asserts `require.NotContains(t, got.Text, rehydrate.StandingInstruction(), "the standing instruction belongs to item 3; with no item 3 it must not be emitted")`. The instruction is item 3's companion, not a free-floating banner — telling an agent to call `already_tried` before committing to an approach when no approach has been eliminated is noise that costs budget and trains the agent to ignore the line. **This diverges from `Qompack.md:991` (§8.7's design note) and the divergence is known, deliberate and unresolved.** §8.7 says the instruction is surfaced "not merely an available tool … Otherwise the affordance exists and goes unused" and scopes it to nothing; on an empty ledger this branch gives the agent precisely "merely an available tool" (item 8's affordance notice still names `already_tried`). The narrower reading is the one 00-ARCHITECTURE §5.15:1594 states — "`StandingInstruction` is item 3's companion, emitted verbatim (§8.7 design note)" — and the one the shipped conformance case executes, so it is what this branch implements. `Qompack.md` is read-only (`plans/README.md:62`) and the sentence is quoted verbatim in this plan's own §8.7 block (`:62`) plus three sibling plans (`V3-SP-09:89`, `V4-SP-13:51`, `V6-SP-18:120`), so this plan does not reconcile the two: it records that §8.7's unscoped reading and §5.15's item-3-scoped reading are both live, that the empty-ledger case is the only one where they differ, and that §9's G6.2 mitigation row (`Qompack.md:1033`) is therefore conditional on a non-empty ledger at `SessionStart`. Ruling on it is a decision for the wave-3 coordinator before `feat/sp11-rehydrator-l5` is cut; if it goes the other way, the change is a `Qompack.md` PR plus a §0 amendment to `runStandingInstructionCase` in `internal/rehydrate/rehydratetest/behaviour.go` on the pre-wave `arch/` branch — not a quiet edit here.
   Whenever `seen > len(units)`, the builder **also** contributes one entry to the drop set — this is what puts the `elimination` line into section 7, and without it the drop report would under-report the single largest omission in the payload:
   `DropEntry{Kind:"elimination", ID:"", Detail: itoa(seen-len(units)) + " of " + itoa(seen) + " not shown; call already_tried(target, approach) or dropped()"}`.
   Its empty `ID` is what makes item 7's same-`Kind`-empty-`ID` collapse rule render it as a single counted line.
7. The note unit is **never truncated** (it is what makes the digest honest) and is accounted before the record units when reserving.

**Item 4 — decisions (`buildDecisions`).** One unit per `Checkpoint.Decisions` entry, ordered by slice score descending (`sc[dag.DecisionNode(dec.ID)]` — the exported constructor, never `dag.NodeID("decision:"+…)`), tiebreak `Turn` descending, tiebreak `ID` ascending. Render:

```
- [dec_9f81c0a2b3d4] (turn 34) <What>
  why: <Why>
  rejected: <AlternativesRejected joined with "; ">
  evidence: <Evidence.String()>
```
`rejected:` and `evidence:` lines are omitted when empty. `What` and `Why` are collapsed to a single line and truncated to 200 and 320 runes respectively. `drop` = `DropEntry{Kind:"decision", ID:string(dec.ID), Detail:"did not fit the rehydration budget; call why(" + string(dec.ID) + ")"}` — which is exactly the affordance SP-13's `why` tool provides.

**Item 5 — current work (`buildCurrentWork`).** A single unit:
```
goal: <Goal>
next step: <NextStep>
blocked on: <*BlockedOn or "none">
```
Lines with empty values are omitted; if all three are empty the item is omitted. Truncatable as a whole with `DropEntry{Kind:"current_work", ID:"current_work"}`, but in practice it costs ~40 tokens and its share always covers it.

**Item 6 — pointers (`buildPointers`).** File pointers first, then tool pointers, each ordered by slice score descending then by `Path`/`ToolUseID` ascending.

```
- src/webhooks/retry.ts sha256:1c4fa27b8e90 — holds the retry loop under change
- tool_use toolu_01H8Z sha256:9ab2d4f16c08 — failing load-test output
```
`Why`/`Summary` collapsed to one line, truncated to 120 runes. **Pointer-first rule, enforced mechanically:** `guardNoContents(u unit) bool` runs at *build* time (it is a runtime guard, not a compile-time or CI check) over each unit of items **4, 5 and 6 only**, and reports the unit as rejected if the unit's text contains a fenced code block (a line whose trimmed form begins with ` ``` `) or exceeds `maxUnitLines = 6` lines. A rejected unit is dropped from its item, a `DropEntry{Kind:<item kind>, ID:<unit id>, Detail:"rejected by the no-contents guard"}` is emitted, `d.Log.Loud` is called once per build, and the rest of the payload is emitted normally. §13 invariant 5 forbids snippets in checkpoints; this is the same rule applied at the injection boundary.

**The guard deliberately does not touch items 1, 2 or 3, and that exclusion is load-bearing.** Items 1 and 2 are tier-1 material that §8.6 orders emitted "verbatim, always" and §8.5 places in the never-truncated tier. A real user prompt routinely runs to twenty lines and routinely contains a fenced stack trace or a pasted diff — running the guard over item 2 would drop the original task statement for containing a code fence, silently re-opening G7.3 by exactly the mechanism this subplan exists to close. Item 3's units are already bounded by the 240-rune reason truncation and its stale note is a fixed string. So: **fences and line counts are checked on checkpoint-derived prose (4/5/6); verbatim human text (1/2) is never inspected and never rejected.** `TestUserIntent_FencedPromptSurvivesVerbatim` is the regression guard.

`drop` = `DropEntry{Kind:"pointer", ID:<path or tool_use_id>, Detail:"did not fit the rehydration budget; call recall or re_read"}`.

**Item 6a — restored instructions (`buildRestoredInstructions`).**

1. Pointer path set = `Checkpoint.Pointers.Files[i].Path` for all *i*.
2. `pathRules, err := d.Rules.PathScoped(ctx, r.ProjectRoot, pointerPaths)`; `nested, err2 := d.Rules.NestedClaudeMD(ctx, r.ProjectRoot, pointerPaths)`. On error from either, that half yields no rules and a `DropEntry{Kind:"path_rule"|"nested_claude_md", ID:"scan", Detail:err.Error()}` is emitted plus a Warn log.
3. Re-estimate `Tokens` for every rule with `d.Tokens.EstimateString(rule.Body, tokens.ClassProse)`.
4. Order: path-scoped rules first (ascending `Path`), then nested `CLAUDE.md` (ascending `Path`). Path rules go first because they carry explicit `paths:` intent while a nested `CLAUDE.md` is proximity-inferred.
5. Unit render:
   ```
   ### .claude/rules/api-conventions.md — paths: src/api/**
   <body>
   ```
   for path rules (globs joined with `", "`, truncated to 120 runes), and
   ```
   ### src/webhooks/CLAUDE.md — nested
   <body>
   ```
   for nested files. The body is included **whole** — a partial instruction set is worse than an absent one (G4.3), so a rule either fits entirely or is dropped entirely and reported. This is the one place where progressive truncation is deliberately *not* applied within a unit, and the ADR records why.
6. `drop` = `DropEntry{Kind:"path_rule"|"nested_claude_md", ID:rule.Path, Detail:"matched " + firstMatchingPointer + "; did not fit the rehydration budget"}`.
7. `guardNoContents` is **not** applied here: rule bodies are instructions, not reconstructible file contents, and may legitimately contain fenced examples.

**Item 6b — skill index (`buildSkillIndex`).**

1. `all, _, err := d.Skills.Index(ctx, r.ProjectRoot, 0)` — the documented "give me everything" call.
2. `kept, used, err := d.Skills.Index(ctx, r.ProjectRoot, skillBudget)` where `skillBudget := core.Tokens(r.Cfg.Runtime.Rehydrate.SkillIndexTokens)` — never a literal (§11.6), and with the explicit conversion, because the config field is a plain `int`.
3. One unit per kept entry: `"- " + Name + ": " + Description + "\n"`. Re-estimated with `d.Tokens`.
4. For every entry in `all` not in `kept`: `DropEntry{Kind:"skill", ID:e.Name, Detail:"not in the compact skill index (budget " + itoa(skillBudget) + " tokens)"}`.
5. For every entry in `all` (kept or not) whose `BodyTokens` exceeds `hostSkillBodyBudgetTokens`: `DropEntry{Kind:"skill", ID:e.Name, Detail:"body ~" + itoa(bodyTokens) + " tokens; the host re-injects at most " + itoa(hostSkillBodyBudgetTokens) + " per skill, head-first, so treat it as partial (§2.7)"}`. `BodyTokens` errors are skipped silently at Debug.
   ```go
   // §2.7: "Invoked skill bodies | Re-injected, 5K/skill and 25K total, oldest dropped,
   // truncated head-first". These describe the HOST's behaviour, not a Qompack tunable.
   const hostSkillBodyBudgetTokens  core.Tokens = 5000
   const hostSkillTotalBudgetTokens core.Tokens = 25000
   ```
6. When `sum(BodyTokens of all)` exceeds `hostSkillTotalBudgetTokens`, add one further entry: `DropEntry{Kind:"skill", ID:"*", Detail:"total skill bodies ~" + itoa(total) + " tokens exceed the host's " + itoa(hostSkillTotalBudgetTokens) + "-token cap; the oldest are dropped entirely (§2.7)"}`.

**Item 7 — drop report (`buildDropReport`).** Sources, concatenated in this order:
1. `Checkpoint.Dropped` (what SP-10 already knew was dropped at checkpoint time)
2. every `DropEntry` produced by items 1–6b during this build
3. every `DropEntry` produced by budget truncation of items 3, 4, 5, 6, 6a, 6b

Ordering is by `kindRank` then `ID` ascending:

```go
var kindRank = map[string]int{
    "path_rule": 0, "nested_claude_md": 1, "skill": 2, "elimination": 3,
    "decision": 4, "pointer": 5, "open_question": 6, "narrative": 7,
    "user_intent_evolution": 8, "current_work": 9,
    "intent_mismatch": 10, "user_intent_source": 11, "elimination_source": 12,
}   // unknown kinds sort last, at rank 99, then by Kind ascending for determinism
```

Path rules and nested `CLAUDE.md` sort first because they are *operating rules the agent no longer has* — the precise thing G4.5 says nothing surfaces today.

Render one line per entry: `"- " + Kind + " " + ID + " — " + Detail + "\n"` (` — ` and Detail omitted when Detail is empty). Consecutive entries with the same `Kind` and empty `ID` are collapsed into one counted line, e.g. `- elimination — 15 of 23 not shown; call already_tried(target, approach) or dropped()`.

If the rendered report exceeds its reserve, keep the highest-ranked entries that fit and append `"- … and " + itoa(rest) + " more; call dropped()\n"` — which is precisely the affordance SP-13 provides. **The complete, untruncated list is always persisted** to `state/rehydrate-<session>.json`, so `dropped()` returns everything regardless of what fit in the payload.

**Item 8 — affordance (`buildAffordance`).** A single, never-truncated unit: `AffordanceNotice() + "\n"`. The rendered string is ~310 characters, so ~80 tokens at the baseline `(len+3)/4` estimate — still an order of magnitude under the ~200-token lazy affordance §4.4 budgets for, and two orders under the 75K it replaces.

### `internal/rehydrate/budget.go`

**`Request.Budget` is a HARD CAP and is never raised.** This is the single most load-bearing decision in the file, and it is fixed by the inherited conformance suite, not chosen here. `runBudgetCase` asks for budgets `{9000, 600, 1}` and asserts `require.LessOrEqual(t, int(got.Tokens), int(budget), "the injection must fit its budget")` for each; `runDegradeCase` repeats it at budget 1 (`internal/rehydrate/rehydratetest/behaviour.go:159-247`). The shipped `Result.Tokens` doc says the same: "the payload's total token cost, which never exceeds Request.Budget" (`internal/rehydrate/types.go:91`). So `clampBudget` may lower a request and may fill in an unset one; it may **never** raise one.

```go
// clampBudget resolves the effective budget B (§8.6 "target 8-12K", §11.5 min/max).
//
// The cfg fields are plain ints (internal/config/runtime.go:63-70), so every one of them is
// converted explicitly. req is authoritative when it is set: raising it to cfg.MinTokens would
// overrun a caller who asked for less, which the conformance suite forbids outright.
func clampBudget(req core.Tokens, cfg config.RehydrateCfg) core.Tokens {
    maxT := core.Tokens(cfg.MaxTokens)
    minT := core.Tokens(cfg.MinTokens)
    if minT > maxT { minT = maxT }          // corrected config, never a crash (§11.3)
    if req <= 0 { return maxT }             // unset: fill to the configured ceiling
    if req > maxT { return maxT }           // lowering is allowed: maxTokens is the hard cap
    return req                              // NEVER raised, not even to minT
}
```

`cfg.MinTokens` is therefore a **fill target for an unset request only** (step 8 below), not a floor on every request. That is exactly what §8.6's "target 8–12K" says: the default budget the daemon supplies is `maxTokens`, min-fill pulls the payload up toward `minTokens` inside it, and a caller who explicitly names 600 tokens gets at most 600.

Share allocation. Percentages are integers so the arithmetic is exact and platform-independent:

```go
const (
    shareIntentEvolutionPct = 10   // item 2's truncatable half (the original unit is tier-1)
    shareEliminationsPct    = 25
    shareDecisionsPct       = 20
    shareCurrentWorkPct     = 10
    sharePointersPct        = 20
    shareInstructionsPct    = 15   // sums to 100
    dropReportReserveDiv    = 10   // reserve B/10 for item 7
)
```

There are **six** share-taking items, not five: `Checkpoint.UserIntent.Evolution` is rendered as truncatable units carrying `DropEntry{Kind:"user_intent_evolution"}`, so it must be allocated like any other discretionary item. It is listed first because `renderOrder` fills in importance order and item 2 precedes item 3 — an evolution delta is closer to the never-truncated original than an elimination is. The constants are ordered here to match `renderOrder`, not to match their magnitudes.

`Build`'s budget pass, in order:

1. `B := clampBudget(r.Budget, cfg)`.
2. **Charge the wrapper first.** `overhead` is the cost of the four fixed strings the payload cannot exist without: `checkpoint.InjectionOpenTag` formatted with `(r.Ref.Seq, payloadVersion)`, the document header line, the newline joins, and `checkpoint.InjectionCloseTag`. All four are derivable before any item is built, so `overhead` is computed up front with `d.Tokens.EstimateString(..., tokens.ClassProse)`. If `overhead >= B`, nothing can be emitted: return `Result{Degraded: true}` with `Items` nil, `Text` `""` and `Tokens` 0, plus one drop entry per discretionary source. `runDegradeCase` is written for exactly this shape — "no items means no payload, not an empty tagged wrapper".
3. Build the never-truncated items: 1 (invariants), 2's original-intent unit, 8 (affordance). `fixed := sum(tokens)`. "Never truncated" means **never partially emitted** — it does not mean "emitted over the cap". If `overhead + fixed > B`, admit them in `renderOrder` while they fit whole, convert each one that does not fit into a drop entry, set `Result.Degraded = true`, and `d.Log.Loud("rehydration tier-1 material exceeds the hard budget cap", "fixed", fixed, "cap", B)`. Every discretionary item is then skipped, its units converted to drop entries. **The authority for that reading is §8.6's own preamble**, `Qompack.md:957`, the sentence that introduces the eight numbered items: "**Emits** via `hookSpecificOutput.additionalContext`, filled in importance order **until the budget is reached**:". The list it introduces is by its own terms a fill-until-budget list, so item 1's "verbatim, always" fixes *how* an admitted item is rendered — verbatim, never summarized — and *where it ranks*, not a licence to overrun the cap. ("Tier 1: never truncated" at `Qompack.md:892` is a comment inside §8.5's checkpoint **artifact** schema — what L4 writes to disk — not a statement about what L5 emits, and §8.5's own preamble at :881 says truncation "yields the best available reconstruction for that budget".) Three further sources say the same: 00-ARCHITECTURE §5.15 ("hard cap from config"), the shipped contract `internal/rehydrate/types.go:91` ("Tokens is the payload's total token cost, which never exceeds Request.Budget"), and the inherited conformance case `runBudgetCase` (`internal/rehydrate/rehydratetest/behaviour.go:166`), which asserts `Tokens <= budget` at `starvedBudget = 1`. So the budget binds, and a payload that overran its cap is a payload the host may refuse outright — an all-or-nothing loss where a partial one was available.
4. `reserveDrop := B / dropReportReserveDiv`; `reserveSkill := min(core.Tokens(cfg.SkillIndexTokens), B/dropReportReserveDiv)`.
5. `avail := B - overhead - fixed - reserveDrop - reserveSkill`; if negative, `avail = 0`.
6. Per-item share, computed once: `share(pct) = avail * pct / 100`, for each of the six share-taking items (intent evolution, eliminations, decisions, current work, pointers, restored instructions). Any remainder from integer division is added to the *last* share-taking item in `renderOrder` (restored instructions) so the arithmetic is lossless and deterministic.
7. Fill in `renderOrder`, carrying forward. `ItemUserIntent`'s turn fills only its *evolution* units — its original-intent unit was already emitted whole in step 3 and is not re-charged here. For each share-taking item, `allowance := share + carry`; admit units in their builder order while `used + unit.tokens <= allowance`; the first unit that does not fit ends the item (**prefix truncation**, matching §6.9's embedded-coding principle — never skip ahead to a smaller unit, because reproducibility across budgets is the entire point of importance ordering). Set `Item.Truncated = true` and emit `unit.drop` for every unadmitted unit. `carry = allowance - used`.
8. Build item 6b against `reserveSkill`, item 7 against `reserveDrop + carry`.
9. **Min-fill pass, for an unset request only.** If `r.Budget <= 0` — the daemon's own call, where `B == maxTokens` — and `Result.Tokens < core.Tokens(cfg.MinTokens)` and at least one item is `Truncated`, compute `slack := min(core.Tokens(cfg.MinTokens), B) - Result.Tokens` and walk the truncated items in `renderOrder`, re-admitting previously dropped units while `slack >= unit.tokens`, removing their drop entries. This is what makes `minTokens` a *fill target* rather than dead config: the design says "target 8–12K", and a payload that stops at 3K while 9K of ranked material was available and the cap was 12K is leaving quality on the table. When the caller named a budget, min-fill does not run at all — raising a named budget is precisely what the hard cap forbids.
10. **Hard cap assertion.** After the min-fill pass, `Result.Tokens <= B` must hold **unconditionally, `Degraded` or not**. A `panic`-free `if` re-truncates the last emitted item and logs `Loud` if it does not — the assertion is also a unit test and a property test.

**Token accounting: Σ `Items[i].Tokens` == `Result.Tokens`, exactly.** `runBudgetCase` asserts it at all three budgets (`require.Equal(t, sum, got.Tokens, "Result.Tokens must be the sum over Items")`), so nothing may be counted into `Result.Tokens` that no `Item` carries. Therefore:

- Each `Item.Tokens` is the estimate of that item's **whole rendered contribution** — its `## n.` section heading, its unit texts, and the blank line that separates it from the next item — not just its units.
- The `overhead` of step 2 is added to the **first emitted `Item`**, whichever kind that turns out to be. It is reserved before filling (so the first item can never push the payload over `B`) and attributed at render time (so the sum invariant holds).
- There is **no synthetic `ItemStat{Kind:"overhead"}`**, in `Result` or in `State`. An earlier draft of this plan recorded one, which made `Result.Tokens` exceed Σ `Items` by construction and failed the suite at every budget.
- `PropBuild_TokensEqualSumOfItems` is the property-test form of the same invariant, over random checkpoints and random budgets.

### `internal/rehydrate/build.go` — `Build`

```go
func Build(ctx context.Context, r Request, d Deps) (Result, error)
```

1. Normalize `Deps`: nil `Log` → `logging.Nop()`; nil `Tokens` → a baseline estimator closure `func(s string) core.Tokens { return core.Tokens((len(s)+3)/4) }` used everywhere the estimator would be; nil `Rules`/`Skills`/`Ledger`/`Graph`/`Store` → that item's source is skipped and a `DropEntry{Kind:..., ID:"unavailable"}` is recorded. **`Build` never returns a non-nil error for a missing dependency** — it degrades and reports. There is no clock to normalize: `Build` reads none.
2. `if r.Source != "compact"` → return `Result{}, nil` with no items. The `clear`/`startup`/`resume` branches never call `Build`; this is belt-and-braces so a mis-wired caller cannot inject into a fresh session.
3. Run the builders, run the budget pass, then render the **body**: header line, then each emitted item's text in `renderOrder`. Only then call `Wrap(r.Ref.Seq, body)`, which adds the open and close tags around it. Nothing else is appended: the §12.1 probe line is the daemon's, appended after this function returns and outside these tags. When no item was emitted, `Text` is `""` — never a tagged wrapper around nothing.
4. `Result.Seq = r.Ref.Seq`; `Result.Dropped` = the complete untruncated list (not the rendered subset).
5. Return `(res, nil)`. The **only** error `Build` ever returns is `ctx.Err()` when the context is already cancelled on entry.

Recorded metrics (all via `d.Log`'s registry-free path; the daemon service owns `obs`): the service wraps `Build` in `obs.Timed(m.Hist("rehydrate.build"), …)` and sets gauges `rehydrate.tokens`, `rehydrate.dropped`, `rehydrate.items`.

### `internal/rehydrate/drops.go` — `Reporter`

State file: `.qompack/state/rehydrate-<sanitized-session>.json`, written with `paths.WriteAtomic` (it is *not* an append-only location; §3.3 lists `state/` as daemon-persisted). `<sanitized-session>` replaces every rune outside `[A-Za-z0-9._-]` with `_` and truncates to 64 runes.

Byte-for-byte format — JSON, 2-space indent, trailing newline, keys in struct order:

```json
{
  "session": "3f2a9c81-...",
  "seq": 7,
  "emitted": 1765432100123,
  "tokens": 10412,
  "budget": 12000,
  "items": [
    { "kind": "invariants", "rank": 0, "tokens": 308, "truncated": false, "units": 4, "units_seen": 4 },
    { "kind": "user_intent", "rank": 1, "tokens": 154, "truncated": false, "units": 3, "units_seen": 3 },
    { "kind": "eliminations", "rank": 2, "tokens": 2903, "truncated": true, "units": 8, "units_seen": 23 }
  ],
  "dropped": [
    { "kind": "path_rule", "id": ".claude/rules/db-conventions.md", "detail": "matched src/db/pool.ts; did not fit the rehydration budget" }
  ],
  "degraded": false
}
```

`rank` is 0-based and counts emitted items, matching `Item.Rank`. The `invariants` row carries the wrapper overhead because it was the first item emitted, which is why 308 rather than 212 — `Σ items[i].tokens == tokens`, and `TestReporter_GoldenStateFile` asserts that sum on the golden. There is no `"overhead"` row and no `"sentinel"` key.

- `CurrentDrops` reads the file and returns `State.Dropped`; a missing file returns `(nil, nil)` — "nothing has been dropped yet" is not an error, and SP-13's `dropped` tool renders it as an empty list.
- A corrupt file returns `(nil, nil)` plus one `Loud` per session and is deleted so the next build rewrites it.
- `Record` writes the file. `Reset` deletes it (`os.Remove`, `fs.ErrNotExist` tolerated) — this is the `source=clear` branch.

### `internal/daemon/rehydrate_service.go`

```go
type rehydrateService struct {
    root  string
    cfg   config.Config
    ck    checkpoint.Reader
    deps  rehydrate.Deps
    rep   rehydrate.Reporter
    mon   contract.Monitor
    log   logging.Logger
    m     obs.Registry
    clock core.Clock
}

func NewRehydrateService(o RehydrateOptions) observer.Rehydrator
```

**`OnCompact(ctx, e hookio.Event) (hookio.Output, error)`**

1. `defer func(){ if v := recover(); v != nil { s.log.Loud("rehydrate panic", "err", v); out = hookio.Empty() } }()` — §13 invariant 6: hooks exit 0, always.
2. **Degradation gate.** `if s.mon != nil && s.mon.Mode() != contract.ModeFull { return hookio.Empty(), nil }`. §12.1 is explicit: in `ModeDegradedPassive` there is "no `additionalContext` injection … no drop report". Getting this wrong would make the degradation doctrine a lie. A unit test asserts an empty output in degraded mode.
3. `cp, ref, err := s.ck.Latest(ctx, e.SessionID)`. On `core.ErrNotFound`: build with a zero `Checkpoint` and `Ref{Seq:0}` — items 1, 2 (from L0), 3 (from the ledger), 6a (from an empty pointer set → nested scan yields nothing, path-rule scan yields nothing), 6b and 8 still produce a useful payload, and `Result.Degraded` is set. On any other error: `Loud`, return `hookio.Empty()`.
4. `budget := core.Tokens(s.cfg.Runtime.Rehydrate.MaxTokens)` — the explicit conversion is required; `RehydrateCfg.MaxTokens` is a plain `int`.
5. `res, err := rehydrate.Build(ctx, rehydrate.Request{Session: e.SessionID, Source: e.Source, ProjectRoot: s.root, Budget: budget, Checkpoint: cp, Ref: ref, Cfg: s.cfg}, s.deps)`, timed into `s.m.Hist("rehydrate.build")`.
6. `s.rep.Record(ctx, e.SessionID, s.stateFrom(e.SessionID, res))` — errors logged at Warn, never propagated. `stateFrom` is the service's method, not a package function, because it is the one place a wall clock enters this slice:
   ```go
   func (s *rehydrateService) stateFrom(sess core.SessionID, res rehydrate.Result) rehydrate.State {
       return rehydrate.State{
           Session:  sess, Seq: res.Seq,                          // Result carries Seq, not Session
           Emitted:  core.UnixMilli(s.clock.Now().UnixMilli()),   // the ONLY clock read in L5
           Tokens:   res.Tokens,
           Budget:   core.Tokens(s.cfg.Runtime.Rehydrate.MaxTokens),
           Items:    itemStats(res.Items),                        // one row per emitted Item, no more
           Dropped:  res.Dropped, Degraded: res.Degraded,
       }
   }
   ```
   `Reporter.Record` therefore takes a fully-formed `State` and needs no clock of its own, which is why `TestReporter_GoldenStateFile` can assert a byte-identical file from a fixed `State` literal.
7. Return
   ```go
   hookio.Output{HookSpecificOutput: &hookio.HSO{
       HookEventName:     "SessionStart",
       AdditionalContext: res.Text,
   }}
   ```
   When `res.Text == ""` return `hookio.Empty()` — an empty `additionalContext` is noise.

**`OnClear(ctx, e hookio.Event) (hookio.Output, error)`**

1. `s.rep.Reset(ctx, e.SessionID)` — deletes `state/rehydrate-<session>.json` so the next `compact` injection is a fresh full payload with no stale drop report.
2. Log at Info: `"session cleared; rehydration state reset"`, with `session` and the last `seq`.
3. **Do not** touch the ledger, the store, the DAG, sketches, or checkpoints. A `/clear` is not a session end; session-scoped eliminations "die with the session" (§8.3) and the session has not ended. SP-08 owns session-registry effects of `clear`.
4. Return `hookio.Empty(), nil` always — SP-08's `source` switch returns this value verbatim from `OnSessionStart` (`V3-SP-08:1610`), and `TestE2E_SessionStartClear` expects no `additionalContext`. Errors are logged, never propagated.

**Wiring — `daemon.Services` gains no member.**

SP-05 already provisioned the seam. `Services.Rehydrate func(ctx context.Context, e hookio.Event) (hookio.Output, error)` is declared at `internal/daemon/options.go:128` and is referenced from exactly one place in the package — `DeclareProducers`, at `options.go:150-152`:

```go
if s.Rehydrate != nil {
    contract.DeclareProducer(contract.CAdditionalContext)
}
```

That is the whole gate. Until something sets `Services.Rehydrate`, `hook.additional_context_delivered` reports `not-yet-implemented` forever and this branch's stated exit criterion is unreachable. Adding a new `Rehydrator` member would not have declared it. So:

- In `internal/daemon/rehydrate_service.go` (this branch's own file), `BindRehydrate` attaches the service through the `Bind` seam, which is exactly what §5.4 provides for a wave-3 subplan:
  ```go
  func BindRehydrate(o *Options, svc observer.Rehydrator) {
      o.Bind(func(s *Services) { s.Rehydrate = svc.OnCompact })
  }
  ```
  `Bind` functions run at daemon construction, in registration order, before `DeclareProducers`.
- At the observer construction site, pass the same `svc` into the observer's deps so `observer.OnSessionStart`'s `source` switch can delegate `compact` and `clear` to it (§5.21). That is one additive, nil-tolerant field assignment in an SP-05/SP-08-owned line, and it is the **only** one this branch makes.
- **This branch creates that startup sequence** — see prerequisite 1 at the top of this plan. `internal/cli/daemon.go:81-84` today assigns only `Log`, `Metrics` and `Clock`; add the construction of `symbols`, `store` (passing `Deps.Symbols`), `dag`, `negknow` and a `checkpoint.Reader` there, each failure logging `Loud` and leaving its `Options` field nil (`runDaemon` must still return nil however badly it goes), then build the service and call `BindRehydrate(o, svc)`. SP-12 reads the same fields; SP-13 appends `rehydrate.NewReporter`, `mcp.NewPromoter` and `InstallMCPOp` to this block and owns its final shape (`plans/V4-SP-13-mcp-retrieval-layer.md` spec §11, which conforms to prerequisite 1). Open no second `store.Open` anywhere in `internal/cli` — `git grep -c 'store\.Open' -- internal/cli` returns 1 at every point in wave 3, on every branch.

`TestService_DeclaresAdditionalContextProducer` asserts the outcome directly: build a daemon with `BindRehydrate` applied, and `contract.CAdditionalContext` must have a declared producer.

### Error handling matrix

| Failure | Response |
|---|---|
| `contract.Mode != ModeFull` | no injection, no drop report, `hookio.Empty()`, exit 0 (§12.1) |
| `checkpoint.Reader.Latest` returns `ErrNotFound` | build from L0 + ledger + pins with `Ref{Seq:0}`; `Degraded=true`; Info log |
| `checkpoint.Reader.Latest` returns any other error | `Loud`, `hookio.Empty()` |
| MANIFEST mismatch surfaced by the Reader | Reader's problem (§12.3 falls back to the parent and degrades); this service sees either a checkpoint or an error |
| `store.ToolUse`/`Open` fails for the L0 intent (`ErrNotFound` included) | checkpoint copy of `user_intent.original`; `DropEntry{Kind:"user_intent_source"}`; Info log |
| L0 record resolves but `rec.Session != r.Session` or `rec.Turn != 0` | same fallback, plus a Warn: the derived-id scheme drifted and must be re-checked against SP-08 |
| L0 intent ≠ checkpoint intent | **L0 wins**; `DropEntry{Kind:"intent_mismatch"}`; `Loud` |
| `negknow.Ledger` error | checkpoint `eliminated[]` only; `DropEntry{Kind:"elimination_source"}`; Warn |
| `dag.BackwardSlice` error, deadline, or nil graph | all slice scores 0; ordering falls back to TS/ID; Debug log; no drop entry |
| `rules.PathScoped`/`NestedClaudeMD` walk error | that half yields nothing; `DropEntry{Kind:"path_rule"\|"nested_claude_md", ID:"scan"}`; Warn |
| individual rule/skill file unreadable | skipped, walk continues; Debug log in `rules`/`skills`; no counter (§5.15's `Scanner`/`Indexer` carry no `obs` seam) |
| `skills.Index` error | no skill index; `DropEntry{Kind:"skill", ID:"scan"}`; Warn |
| nil `tokens.Estimator` | baseline `(len+3)/4` everywhere; Warn once |
| tier-1 material exceeds the cap | emit whole the tier-1 items that fit, drop the rest and every discretionary item as drop entries, `Degraded=true`, `Loud`. Nothing is emitted partially and nothing is emitted over the cap |
| the wrapper alone exceeds the cap | `Items` nil, `Text` `""`, `Tokens` 0, `Degraded=true`, drop entries for every source; never an empty tagged wrapper |
| `Result.Tokens > B` after min-fill | re-truncate the last emitted item; `Loud`; assertion also covered by a property test |
| state-file write fails | Warn; the payload is still emitted; `dropped()` degrades to the previous state file |
| state-file corrupt on read | `(nil, nil)`, one `Loud` per session, delete the file |
| panic anywhere in `Build` or the service | recovered at the service boundary, `hookio.Empty()`, `Loud`, exit 0 |

### Performance budgets

These are **local budgets**, not §2.4 IDs (this path is warm, not hot; the `SessionStart` manifest timeout is 15 s).

| Local budget | Definition | Limit | Enforced by |
|---|---|---|---|
| `L5-BUILD` | `rehydrate.Build` wall time, warm caches | **p99 < 250 ms** | `BenchmarkBuild` in `internal/rehydrate/bench_test.go` |
| `L5-RULES` | `rules.PathScoped` over a 2 000-file tree with 24 rule files and 40 pointers | **p99 < 50 ms** | `BenchmarkPathScoped` |
| `L5-SKILLS` | `skills.Index` over 60 skills | **p99 < 20 ms** | `BenchmarkIndex` |
| `L5-SESSIONSTART` | `qompack session-start` wall time with `source=compact` against a warm daemon | **p99 < 1.5 s** (10% of the 15 s manifest timeout) | `test/e2e/sessionstart_compact_test.go` |
| Payload size | `Result.Tokens` | **≤ `Request.Budget`, no exception** — and ≤ `runtime.rehydrate.maxTokens` for the daemon's own unset-budget call | property test + `rehydratetest` + replay gate |

`benchstat` compares against `testdata/bench-baseline.txt`; >25% regression fails the build (00-ARCHITECTURE §7).

---

## Test plan (TDD)

Every test below is written **before** the code it exercises, run to observe the failure, then made to pass. `require` (fail-fast) only; `assert` is banned. `go-cmp` with explicit `cmpopts` for structural diffs. `rapid` for the property tests. Every test that touches time takes `testutil.FakeClock` — which in this slice is only `internal/daemon/rehydrate_service_test.go`, since `rehydrate.Build` and `rehydrate.Reporter` read no clock at all.

### Fixtures

`testdata/fixtures/rules/proj-a/` — the happy-path tree:

```
CLAUDE.md                                     (project root; must NOT be returned by NestedClaudeMD)
src/api/routes.ts
src/api/CLAUDE.md                             "All API handlers validate with zod."
src/webhooks/retry.ts
src/webhooks/CLAUDE.md                        "Webhook handlers must be idempotent on delivery id."
src/db/pool.ts
src/db/CLAUDE.md                              "Never open a pool outside db/pool.ts."
.claude/rules/api-conventions.md              paths: ["src/api/**"]        body 180 bytes
.claude/rules/db-conventions.md               paths: src/db/**             body 4200 bytes
.claude/rules/nested/deep-rule.md             paths: ["**/*.ts"]           body 90 bytes
.claude/rules/unscoped.md                     (no paths: key)              body 100 bytes
.claude/inline-rule.md                        paths: ["src/webhooks/*.ts"] body 120 bytes
.claude/skills/code-review/SKILL.md           name: code-review, description: Review the current diff for correctness bugs.
.claude/skills/migration-runner/SKILL.md      description only; body 30 000 bytes (~7 500 tokens)
.claude/skills/quick-fmt.md                   name+description; body 400 bytes
```

`testdata/fixtures/rules/proj-edge/` — the edge tree:

```
"dir with spaces/a.ts"
"dir with spaces/CLAUDE.md"
"únïcodé/b.ts"
"únïcodé/CLAUDE.md"
deep/a/b/c/d/e/f/g/h/i/j/k/l/m/n/o/p/q/r/s/t/file.ts     (path > 260 chars on Windows)
deep/a/b/c/d/e/f/g/h/i/j/k/l/m/n/o/p/q/r/s/t/CLAUDE.md
crlf/win.ts
crlf/CLAUDE.md                                            (CRLF line endings)
Foo.ts  foo.ts                                            (case-colliding pair)
.claude/rules/crlf-rule.md                                (CRLF frontmatter, paths: ["crlf/**"])
.claude/rules/broken-front.md                             ("---" opened, never closed)
.claude/rules/huge.md                                     (300 KiB, must be skipped)
.claude/rules/empty-paths.md                              (paths: [] — must be skipped)
```

**Checkpoint fixtures (Rule W-2). This branch adds none, and there is exactly one on disk.** `testdata/golden/contracts/checkpoint/` contains `MANIFEST.json` and `want/0001.json` — nothing else. There is no `full.json`, `minimal.json` or `empty.json` anywhere in the tree, and the MANIFEST is SP-10's, so this branch may not add to it. The three checkpoint values the tests need are therefore:

- **`ckFull`** — decoded from `testdata/golden/contracts/checkpoint/want/0001.json`, the frozen `checkpoint_v1` fixture, and **the only checkpoint file this branch reads**. Its actual contents, which every test row below is written against: `seq: 1`; **2** invariants (`inv_7c1a9e2f4b60`, `inv_2d8f0a6c3e15`); `user_intent.original` plus **2** evolution entries; **1** elimination (`elim_3f9b2c7d1a48`, target `src/auth.ts:refreshToken`, `scope: project`, `status: active`, with a `descriptor` carrying `normalized_path: src/auth.ts` and `symbol: refreshToken`); **1** decision (`dec_a3f2c9e14b70`, turn 61, two rejected alternatives); **2** open questions; `current_work` with `blocked_on: null`; **2** file pointers (`src/auth.ts`, `docker-compose.yml`) and **1** tool pointer (`toolu_01A2B3C4D5E6F7G8H9J0K1L2`); a narrative; and **2** pre-existing `dropped` entries (`path_rule api-conventions.md`, `tool_output toolu_01M3N4P5Q6R7S8T9U0V1W2X3`). Any test that needs *more* records than that — 23 eliminations for the top-N digest, 9 decisions for the drop path — builds them in the fake ledger or by appending to a copy of `ckFull` in memory. It never edits the golden.
- **`ckMinimal`** — a `checkpoint.Checkpoint` **value built in `fake_test.go` by field assignment**, carrying `invariants` and `user_intent.original` only, with `eliminated`, `decisions`, `current_work` and `pointers` all empty, so items 3, 4, 5, 6 and 6a are absent and the rank arithmetic can be asserted in isolation. Tests using it run against an **empty project root** (no `.claude/` tree), so item 6b is empty too.
- **`ckEmpty`** — the zero `checkpoint.Checkpoint`; drives `TestBuild_Golden_NoCheckpoint`, the `Ref{Seq:0}` path where the reader found nothing.

Building `ckMinimal` and `ckEmpty` in-test is Rule W-2 compliant and is not a loophole: W-2 forbids *constructing a checkpoint by calling `checkpoint.Writer`*, which is SP-10's same-wave code. Assigning fields of a `checkpoint.Checkpoint` struct calls nothing SP-10 owns — it is the same technique `internal/rehydrate/rehydratetest/behaviour.go:62-103` already uses to build its own fixture. The V4 verification checkpoint re-runs these tests against SP-10's real writer output; a value the real writer cannot produce is a verification failure, not a fixture bug.

`Ref` is **not** in any golden — `checkpoint.Ref` is a Go value SP-10's reader returns, and `want/0001.json` is a `Checkpoint`. Every test supplies its own `Ref`, and every one of them uses `Ref{Seq: 1}` to match `ckFull`, so the rendered header reads `checkpoint 0001` throughout.

`testdata/golden/rehydrate/` — new goldens written by this branch: `full-12k.txt`, `full-8k.txt`, `minimal.txt`, `no-checkpoint.txt`, `degraded.txt`.

### `internal/rules`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestMatch_Star_WithinSegment` | — | `("src/*.ts","src/a.ts")`, `("src/*.ts","src/x/a.ts")` | `true`, `false` |
| `TestMatch_DoubleStar_ZeroSegments` | — | `("src/**/a.ts","src/a.ts")` | `true` |
| `TestMatch_DoubleStar_ManySegments` | — | `("src/**/a.ts","src/x/y/z/a.ts")` | `true` |
| `TestMatch_DoubleStar_Trailing_MatchesDirItself` | — | `("src/api/**","src/api")` | `true` |
| `TestMatch_BareGlob_ImpliesAnyDepth` | — | `("*.ts","src/api/routes.ts")` | `true` |
| `TestMatch_CharClass` | — | `("src/[ab].ts","src/a.ts")`, `("src/[ab].ts","src/c.ts")` | `true`, `false` |
| `TestMatch_QuestionMark` | — | `("src/?.ts","src/a.ts")`, `("src/?.ts","src/ab.ts")` | `true`, `false` |
| `TestMatch_NoMatchAcrossSegmentWithSingleStar` | — | `("*","a/b")` | `false` |
| `TestMatch_RejectsPathologicalPattern` | 40-segment pattern | any key | `false`, returns in < 1 ms |
| `PropMatch_NeverPanics` (rapid) | random patterns/keys from the alphabet `a/.*?[]` | 10 000 cases | no panic, deterministic (same result twice) |
| `TestParseFront_ListForm` | `proj-a/.claude/rules/db-conventions.md` | — | `Front{Paths:["src/db/**"],Present:true}`, body offset at the byte after the closing `---\n` |
| `TestParseFront_InlineArray` | `api-conventions.md` | — | `Paths:["src/api/**"]` |
| `TestParseFront_ScalarForm` | synthetic `paths: src/x/**` | — | `Paths:["src/x/**"]` |
| `TestParseFront_CRLF` | `proj-edge/.claude/rules/crlf-rule.md` | — | `Paths:["crlf/**"]`, `Present:true` |
| `TestParseFront_Unclosed` | `broken-front.md` | — | `Present:false`, offset 0 |
| `TestParseFront_NoFrontmatter` | `unscoped.md` | — | `Present:false` |
| `TestParseFront_UnknownKeysIgnored` | frontmatter with `owner:`, `version:` | — | parsed, unknown keys absent, no error |
| `TestParseFront_OversizeFrontmatter` | 9 KiB frontmatter | — | `Present:false` |
| `TestPathScoped_MatchesOnePointer` | `proj-a` | pointers `["src/api/routes.ts"]` | exactly `[.claude/rules/api-conventions.md, .claude/rules/nested/deep-rule.md]`, sorted |
| `TestPathScoped_MatchesInlineRuleDirectlyUnderDotClaude` | `proj-a` | `["src/webhooks/retry.ts"]` | includes `.claude/inline-rule.md` |
| `TestPathScoped_SkipsUnscoped` | `proj-a` | `["src/api/routes.ts"]` | `.claude/rules/unscoped.md` absent |
| `TestPathScoped_SkipsEmptyPaths` | `proj-edge` | any pointer | `empty-paths.md` absent |
| `TestPathScoped_SkipsOversizeFile` | `proj-edge` | any pointer | `huge.md` absent, no error |
| `TestPathScoped_NoPointers` | `proj-a` | `[]` | empty slice, nil error |
| `TestPathScoped_Deterministic` | `proj-a` | same input, 10 runs | byte-identical `[]Rule` each time (`go-cmp`) |
| `TestPathScoped_CaseInsensitivePointer` (windows/darwin) | `proj-edge` | `["FOO.ts"]` vs `["foo.ts"]` | identical result sets |
| `TestPathScoped_BodyIsCRLFNormalized` | `proj-edge` | `["crlf/win.ts"]` | `Body` contains no `\r` |
| `TestPathScoped_TokensBaselineIsSet` | `proj-a` | `["src/api/routes.ts"]` | every `Rule.Tokens == core.Tokens((len(Body)+3)/4)` |
| `TestNestedClaudeMD_ContainingDir` | `proj-a` | `["src/api/routes.ts"]` | exactly `[src/api/CLAUDE.md]` — `proj-a` has no `src/CLAUDE.md`, and the root one is excluded, so the ancestor walk finds nothing further |
| `TestNestedClaudeMD_ExcludesProjectRoot` | `proj-a` | `["README.md"]` (root-level pointer) | empty |
| `TestNestedClaudeMD_WalksAncestors` | `proj-a` + a new `src/CLAUDE.md` | `["src/api/routes.ts"]` | **both** `src/CLAUDE.md` and `src/api/CLAUDE.md`, in that (ascending-`Path`) order — the shipped `rules.Scanner` doc says "containing (or ancestor to)" and `rulestest`'s `nested_claude_md_discovery` fixture puts the file two levels above its pointer |
| `TestNestedClaudeMD_AncestorTwoLevelsUp` | temp root mirroring the `rulestest` fixture: `src/pkg/CLAUDE.md`, pointer `src/pkg/deep/thing.go` | that pointer | exactly one rule, `src/pkg/CLAUDE.md`, `Nested==true` — the same assertion the inherited suite makes, kept here so a regression is caught in `internal/rules` first |
| `TestNestedClaudeMD_AncestorWalkStopsBeforeRoot` | `proj-a` | `["src/api/routes.ts"]` | the project-root `CLAUDE.md` is **absent** at every level of the walk (§2.7: the host re-injects it) |
| `TestNestedClaudeMD_Dedups` | `proj-a` | `["src/api/routes.ts","src/api/other.ts"]` | one entry |
| `TestNestedClaudeMD_UnicodeAndSpaces` | `proj-edge` | `["dir with spaces/a.ts","únïcodé/b.ts"]` | both `CLAUDE.md` files returned, `Path` in forward-slash form |
| `TestNestedClaudeMD_LongPath` | `proj-edge` | the 260+ char pointer | the deep `CLAUDE.md` returned (exercises `paths` long-path handling) |
| `TestNestedClaudeMD_NestedFlag` | `proj-a` | any | every returned `Rule.Nested == true` |
| `BenchmarkPathScoped` | 2 000-file synthetic tree, 24 rules, 40 pointers | — | budget `L5-RULES` p99 < 50 ms |

### `internal/skills`

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestIndex_DirAndFlatForms` | `proj-a` | budget 0 | 3 entries: `code-review`, `migration-runner`, `quick-fmt`, sorted by Name |
| `TestIndex_NameFromFrontmatterElseBasename` | `proj-a` | budget 0 | `migration-runner` (no `name:` key) takes its dir name |
| `TestIndex_DescriptionFallsBackToFirstBodyLine` | skill with no `description:` | budget 0 | first non-heading body line, truncated to 100 runes with `…` |
| `TestIndex_DescriptionEmpty` | skill with empty body | budget 0 | `"(no description)"` |
| `TestIndex_BudgetPrefixTruncation` | `proj-a` | budget = cost of first entry only | exactly `[code-review]`, `used` = its cost; **not** the cheapest entry |
| `TestIndex_ZeroBudgetReturnsAll` | `proj-a` | budget 0 | all 3 |
| `TestIndex_Deterministic` | `proj-a` | 10 runs | identical `[]Entry` |
| `TestIndex_SkipsSymlinks` | `proj-edge` + a symlinked skill dir | budget 0 | symlink target absent (skipped on all platforms; test skipped on Windows when symlink creation is not permitted) |
| `TestIndex_SkipsOversize` | 2 MiB `SKILL.md` | budget 0 | absent |
| `TestBodyTokens` | `proj-a` `migration-runner` | — | `>= 7000 && <= 8000` (30 000 bytes / 4) |
| `TestBodyTokens_StripsFrontmatter` | `code-review` | — | equals `(len(body-after-frontmatter)+3)/4` |
| `BenchmarkIndex` | 60 synthetic skills | — | budget `L5-SKILLS` p99 < 20 ms |

### `internal/rehydrate`

**Ordering and structure**

| Test | Setup | Expected |
|---|---|---|
| `TestRenderOrder_IsIotaOrder` | — | `renderOrder[i] == ItemKind(i)` for all ten kinds, and `len(renderOrder) == int(ItemAffordance)+1` — after the step-0 amendment the rendered order *is* the declaration order, which is what makes `runItemOrderCase`'s ascending-kind assertion hold by construction |
| `TestBuild_ItemOrderInPayload` | `ckFull`, `ProjectRoot` = `testdata/fixtures/rules/proj-a` so items 6a and 6b are non-empty, budget 12 000 | the substrings `"## 1. "`…`"## 8. "` appear in the payload at strictly increasing byte offsets, with `## 6a.` and `## 6b.` between `## 6.` and `## 7.` |
| `TestBuild_RankIsZeroBasedAmongEmitted` | `ckMinimal` against an empty project root, so items 3, 4, 5, 6, 6a and 6b are all empty | `Items[0].Rank==0` (invariants), `Items[1].Rank==1` (user intent), `Items[2].Rank==2` (drop report), `Items[3].Rank==3` (affordance) — rank counts **emitted** items from zero, so the omitted kinds consume no rank. This is the same assertion `runItemOrderCase` makes (`require.Equal(t, i, it.Rank)`), pinned here against a fixture whose emitted set is known |
| `TestBuild_InjectionTagging` | `ckFull`, `Ref.Seq=1` | payload starts `<!-- qompack:injected seq=1 ver=1 -->\n`, ends `<!-- /qompack:injected -->`; `Unwrap` round-trips and returns `seq==1` |
| `TestUnwrap_RejectsMalformed` | `"no tags here"`, `"<!-- qompack:injected seq=x ver=1 -->x"` | `ok==false` |
| `TestBuild_EmitsNoSentinel` | `ckFull` | the payload contains neither `"qompack:sentinel"` nor `"qompack-contract-probe"` — the §12.1 probe is minted and appended by the daemon's `session.start` route, outside these tags, and this slice must not duplicate it |
| `TestBuild_TokensEqualSumOfItems` | `ckFull`, budgets 12 000 / 600 / 1 | `Result.Tokens == Σ Items[i].Tokens` at every budget, and the first emitted `Item` carries the wrapper overhead |

**Item 1 — invariants**

| Test | Setup | Expected |
|---|---|---|
| `TestInvariants_Verbatim` | 4 invariants, one 900-byte text | all four present, text byte-identical (no truncation, no ellipsis) |
| `TestInvariants_WholeOrNotAtAll_UnderTinyBudget` | 4 invariants, `Request.Budget` = 200 | every invariant that is emitted is emitted in full — `Items[0].Truncated==false`, no ellipsis, no partial text — the ones that did not fit appear in `Result.Dropped` instead, `Result.Degraded==true`, and `Result.Tokens <= 200`. "Never truncated" means never *partially* emitted; it never means emitted over the cap |
| `TestInvariants_EmptyOmitsSection` | zero invariants | `"## 1."` absent from the payload |

**Item 2 — verbatim intent (G7.3, G2.3)**

| Test | Setup | Expected |
|---|---|---|
| `TestUserIntent_FromL0NotCheckpoint` | fake store returns L0 text `"FIX THE WEBHOOK"`, checkpoint says `"working on payment stuff"` | payload contains `FIX THE WEBHOOK` and does **not** contain `working on payment stuff`; a `DropEntry{Kind:"intent_mismatch"}` is present; `Loud` was called once |
| `TestUserIntent_ResolvesTheDerivedFirstPromptID` | fake store holding `prompt_s1_0` (turn 0) and `prompt_s1_4` (turn 4) for session `s1`, plus `prompt_s0_0` for a different session; request session is `s1` | `Store.ToolUse` is called **exactly once**, with `"prompt_s1_0"`; the turn-0 text of `s1` is injected; `Store.Search` is **never called** (spy asserts zero calls) — a relevance search would return the most recent prompts and inject `prompt_s1_4` instead |
| `TestUserIntent_FirstPromptIDMatchesObserver` | — | `firstPromptID("abc") == observer.VerbatimPromptID("abc", 0)`. Lives in `test/e2e`, which may import both packages; `rehydrate` may not import `observer` (§3.2), which is why the id is derived rather than called |
| `TestUserIntent_RejectsRecordFromAnotherSession` | `prompt_s1_0` resolves to a record whose `Session` is `s0` | falls back to the checkpoint copy with `DropEntry{Kind:"user_intent_source"}` and one Warn — a previous session's prompt is never injected |
| `TestUserIntent_RejectsRecordWithNonZeroTurn` | `prompt_s1_0` resolves with `Turn == 3` | same fallback and Warn: the id scheme drifted and the record cannot be vouched for |
| `TestUserIntent_FallsBackWhenNotFound` | `ToolUse` returns `core.ErrNotFound` | checkpoint text injected, `DropEntry{Kind:"user_intent_source"}` present, no `Loud`, no error returned |
| `TestUserIntent_FallsBackWhenStoreErrors` | `ToolUse` returns `errors.New("boom")` | same as above, Info log, no error returned |
| `TestUserIntent_StripsPriorInjections` | L0 text wrapped in injection tags | injected text has the tags removed |
| `TestUserIntent_FencedPromptSurvivesVerbatim` | L0 prompt is 30 lines and contains a ` ``` ` fenced stack trace | the whole prompt is injected byte-identically; `guardNoContents` is never applied to item 2 (this is the G7.3 regression guard for the guard itself) |
| `TestUserIntent_TruncatesEvolutionNotOriginal` | 20 evolution entries, tight budget | original present in full, some evolution entries dropped with `Kind:"user_intent_evolution"` |
| `TestUserIntent_EvolutionHasItsOwnShare` | 20 evolution entries, 23 eliminations, budget 8 000 | at least one evolution unit is admitted — evolution is not starved by eliminations, because `shareIntentEvolutionPct` is allocated before item 3 fills |
| `TestUserIntent_CapsAtMaxIntentBytes` | 40 KiB L0 prompt | at most 8 192 bytes read; payload still under the cap |

**Item 3 — eliminations**

| Test | Setup | Expected |
|---|---|---|
| `TestEliminations_TopNFromConfig` | 23 records, `EliminationsTopN=8` | exactly 8 rendered + the note unit |
| `TestEliminations_OrderedBySliceScore` | fake graph scores `dag.EliminationNode("e5")`=0.9, `dag.EliminationNode("e2")`=0.5, rest 0 | `e5` first, `e2` second. The fixture builds its map with the exported constructor, never a literal |
| `TestEliminations_FileProxyScore` | no elimination node; `dag.FileNode(paths.Key("src/db/pool.ts"))`=0.8 for `e7` | `e7` sorts as if 0.6 (`0.8*0.75`) |
| `TestEliminations_SymbolProxyScore` | no elimination or file node; `dag.SymbolNode(paths.Key("src/auth.ts"), "refreshToken")`=0.8 for a record whose `Desc.NormalizedPath` is `src/auth.ts` and `Desc.Symbol` is `refreshToken` | that record sorts as if 0.6. A companion case scores `dag.SymbolNode("", "refreshToken")` for a record with an empty `NormalizedPath` and asserts the same. This is the guard against the `"symbol:" + Symbol` spelling, which can never hit: `dag` keys symbol nodes on `<pathKey>#<name>` |
| `TestEliminations_TieBreakByTSThenID` | three records, all score 0 | descending TS, then ascending ID |
| `TestEliminations_NoteCountsTheRest` | 23 seen, 8 shown | payload contains `15 further eliminations are recorded and not shown.` |
| `TestEliminations_StandingInstructionIsItem3sCompanion` | 0 records, otherwise a populated checkpoint | item 3 is absent **and** `"Before committing to an approach, call already_tried."` appears nowhere in `Result.Text` — `AffordanceNotice()` does not carry it. This mirrors `runStandingInstructionCase` exactly |
| `TestEliminations_StaleRendersTheVerbatimNote` | one `Status:"stale"` record, `staleResponse:"flag"` | payload contains `previously eliminated, but the evidence has changed since — re-verification may be warranted` |
| `TestEliminations_StaleDroppedWhenConfigured` | `staleResponse:"drop"` | the stale record is absent |
| `TestEliminations_BothScopesRead` | ledger holds 2 `session`-scoped and 3 `project`-scoped active records; `eliminations.defaultScope == "session"` | all 5 are candidates; the project-scoped ones are rendered (guards against reading only `defaultScope`) |
| `TestEliminations_NotShownProducesDropEntry` | 23 seen, `EliminationsTopN=8` | `Result.Dropped` contains exactly one `DropEntry{Kind:"elimination", ID:""}` whose `Detail` is `15 of 23 not shown; call already_tried(target, approach) or dropped()`, and section 7 renders it as one collapsed line |
| `TestEliminations_LedgerErrorFallsBackToCheckpoint` | both `Active` calls error, checkpoint has 5 | 5 rendered, `DropEntry{Kind:"elimination_source"}` present |
| `TestEliminations_OneScopeErrorUsesTheOther` | `Active("session")` errors, `Active("project")` returns 3 | the 3 project records are rendered plus `DropEntry{Kind:"elimination_source"}` |
| `TestEliminations_LedgerAndCheckpointDedupedByID` | same ID in both, differing `Status` | one line, ledger's `Status` used |
| `TestEliminations_GraphErrorDoesNotFail` | graph returns error | payload built, ordering by TS, no drop entry for the graph |

**Item 4/5/6**

| Test | Setup | Expected |
|---|---|---|
| `TestDecisions_RenderWhatWhyRejectedEvidence` | one full decision | all four lines present in the documented order |
| `TestDecisions_OmitsEmptyLines` | decision with no alternatives, zero evidence | `rejected:` and `evidence:` lines absent |
| `TestDecisions_DropEntryNamesWhyTool` | tight budget, 9 decisions | dropped entries have `Detail` containing `call why(dec_` |
| `TestCurrentWork_BlockedOnNil` | `BlockedOn == nil` | `blocked on: none` |
| `TestCurrentWork_AllEmptyOmitsSection` | zero-valued `CurrentWork` | `"## 5."` absent |
| `TestPointers_NoContents` | 12 file pointers | payload has no ` ``` ` fence anywhere in items **4, 5 or 6** (the guard's scope). Items 1–3 are excluded by design and asserted separately by `TestUserIntent_FencedPromptSurvivesVerbatim` |
| `TestPointers_FilesBeforeTools` | 3 files, 3 tools | all file lines precede all tool lines |
| `TestPointers_GuardRejectsMultilineUnit` | a pointer whose `Why` contains 8 newlines | that unit is dropped, `Loud` called, the rest of the payload is intact |
| `TestPointers_DropEntryNamesRecallAndReRead` | tight budget | `Detail` contains `call recall or re_read` |

**Items 6a/6b — instruction restoration (G4.1, G4.2, G4.4)**

| Test | Setup | Expected |
|---|---|---|
| `TestRestored_PathRulesForPointerSet` | `proj-a`, pointers `[src/api/routes.ts, src/db/pool.ts]` | `.claude/rules/api-conventions.md` and `.claude/rules/db-conventions.md` bodies present |
| `TestRestored_NestedClaudeMDForPointerDirs` | same | `src/api/CLAUDE.md` and `src/db/CLAUDE.md` bodies present; root `CLAUDE.md` absent |
| `TestRestored_PathRulesBeforeNested` | same | every `— paths:` heading precedes every `— nested` heading |
| `TestRestored_WholeRuleOrNothing` | budget allows 3 000 of the 4 200-byte `db-conventions.md` | the rule is **absent entirely**, with `DropEntry{Kind:"path_rule", ID:".claude/rules/db-conventions.md"}`; no partial body appears |
| `TestRestored_ScanErrorDegrades` | `Rules` returns an error from `PathScoped` | nested rules still restored, `DropEntry{Kind:"path_rule", ID:"scan"}` present |
| `TestRestored_NilScanner` | `Deps.Rules == nil` | no section 6a, `DropEntry{Kind:"path_rule", ID:"unavailable"}` |
| `TestSkillIndex_BudgetFromConfigNotConstant` | `SkillIndexTokens = 40` | fewer entries than at 450; a grep of `internal/skills` and `internal/rehydrate` source finds no literal `450` |
| `TestSkillIndex_DropsReportUnindexedSkills` | budget fits 1 of 3 | two `DropEntry{Kind:"skill"}` entries naming the two absent skills |
| `TestSkillIndex_WarnsOnHostHeadTruncation` | `migration-runner` body ~7 500 tokens | a `DropEntry{Kind:"skill"}` whose Detail contains `head-first` and `5000` |
| `TestSkillIndex_WarnsOnHostTotalCap` | 6 skills totalling 30 000 tokens | a `DropEntry{Kind:"skill", ID:"*"}` naming the 25 000-token cap |

**Item 7 — drop report (G4.5)**

| Test | Setup | Expected |
|---|---|---|
| `TestDropReport_OrdersPathRulesFirst` | mixed drops | rendered order is `path_rule`, `nested_claude_md`, `skill`, `elimination`, `decision`, `pointer` |
| `TestDropReport_IncludesCheckpointDropped` | `Checkpoint.Dropped` has `{path_rule, api-conventions.md}` | present in the rendered report |
| `TestDropReport_TruncationAppendsDroppedAffordance` | 200 drop entries, reserve 120 tokens | payload ends the section with `… and N more; call dropped()` |
| `TestDropReport_ResultDroppedIsComplete` | same | `len(Result.Dropped) == 200` regardless of what was rendered |
| `TestDropReport_EmptyOmitsSection` | nothing dropped | `"## 7."` absent |

**Budget (G3.3, §12 risk row)**

| Test | Setup | Expected |
|---|---|---|
| `TestClampBudget_ZeroUsesMax` | `Budget=0`, cfg 8 000/12 000 | 12 000 — an unset request fills to the ceiling |
| `TestClampBudget_BelowMinIsNotRaised` | `Budget=500`, cfg 8 000/12 000 | **500**. `Request.Budget` is a hard cap: raising it to `minTokens` would overrun a caller who asked for less, which `runBudgetCase` forbids at budgets 600 and 1 |
| `TestClampBudget_AboveMaxLowered` | `Budget=50_000` | 12 000 |
| `TestClampBudget_InvertedConfigCorrected` | min 12 000, max 8 000 | 8 000, no panic |
| `TestBuild_NeverExceedsRequestedBudget` | `ckFull`, budgets 12 000 / 9 000 / 600 / 1 | `Result.Tokens <= budget` in all four — the same assertion `runBudgetCase` and `runDegradeCase` make, pinned here against the real fixture |
| `TestBuild_TokensEqualSumAtEveryBudget` | `ckFull`, the same four budgets | `Result.Tokens == Σ Items[i].Tokens` in all four |
| `TestBuild_StarvedBudgetEmitsNothing` | `ckFull`, `Budget=1` | `Items` empty, `Text == ""` (not an empty tagged wrapper), `Tokens == 0`, `Degraded==true`, `Dropped` non-empty |
| `TestBuild_SmallerThanStock` | `ckFull` at 12 000 | `Result.Tokens < 50 000 + 25 000` — the §2.4 stock restoration figure, asserted explicitly so the G3.3 claim is a test, not a comment |
| `TestBuild_MinFillReadmitsUnits` | `Budget=0` (unset), material worth 11 000 tokens, first pass yields 6 500 | `Result.Tokens >= 8 000` and `<= 12 000`, at least one previously dropped unit re-admitted, its drop entry removed |
| `TestBuild_MinFillDoesNotRunForANamedBudget` | `Budget=3 000`, material worth 11 000 tokens | `Result.Tokens <= 3 000`; the payload is **not** pulled up toward `minTokens` |
| `TestBuild_PrefixTruncationNotCheapestFirst` | units of 100, 5 000, 100 tokens, allowance 300 | only the first unit is admitted; the third is **not** skipped ahead to |
| `TestBuild_CarryForward` | eliminations use 10% of their share | decisions' allowance exceeds their nominal share by the carry |
| `TestBuild_TruncatedFlagSet` | tight budget | every item that dropped a unit has `Truncated==true`, others `false` |
| `PropBuild_MonotoneInBudget` (rapid) | random checkpoints, budgets in [8 000, 12 000] | `Tokens(B1) <= Tokens(B2)` whenever `B1 <= B2`; the item set at `B1` is a **prefix-wise subset** of the item set at `B2` (embedded-coding property, §6.9) |
| `PropBuild_NeverPanics` (rapid) | random checkpoints incl. empty strings, nil slices, huge strings, invalid UTF-8; budgets in [0, 20 000] | no panic, and `Result.Tokens <= clampBudget(Budget, cfg)` **unconditionally**, `Degraded` or not |
| `PropBuild_TokensEqualSumOfItems` (rapid) | same generator | `Result.Tokens == Σ Items[i].Tokens` on every draw |
| `PropBuild_Deterministic` (rapid) | same input twice | byte-identical `Result.Text` |

**Reporter**

| Test | Setup | Expected |
|---|---|---|
| `TestReporter_RecordThenCurrentDrops` | record 3 entries | `CurrentDrops` returns the same 3, in order |
| `TestReporter_MissingFileIsEmptyNotError` | fresh project | `(nil, nil)` |
| `TestReporter_CorruptFileIsEmptyAndDeleted` | write `{{{` | `(nil, nil)`, one `Loud`, file removed |
| `TestReporter_ResetDeletes` | record then `Reset` | `CurrentDrops` returns `(nil, nil)`; a second `Reset` is a no-op |
| `TestReporter_SanitizesSessionID` | session `"a/b\\c:*?"` | file name matches `^rehydrate-[A-Za-z0-9._-]+\.json$` |
| `TestReporter_GoldenStateFile` | a fixed `State` literal (including a fixed `Emitted`; the Reporter reads no clock) | byte-identical to `testdata/golden/rehydrate/state.json`; the golden has no `"overhead"` item row and no `"sentinel"` key, and `Σ items[i].tokens == tokens` |

The `var _ mcp.DropReporter = rehydrate.NewReporter(...)` assertion is **not** in this package: `rehydrate` may not import `mcp` (§3.2), so the assertion lives in `test/e2e`, which is outside `internal/` and may import both. It is listed in the e2e table below.

**Goldens and degradation**

| Test | Setup | Expected |
|---|---|---|
| `TestBuild_Golden_Full12K` | `ckFull`, `Ref{Seq:1}`, budget 12 000 | byte-identical to `testdata/golden/rehydrate/full-12k.txt`. No clock is injected: `Build` reads none, and a golden that changed between runs would be a bug, not a fixture refresh |
| `TestBuild_Golden_Full8K` | budget 8 000 | matches `full-8k.txt`; every section heading present in `full-12k.txt` that survives at 8K appears in the same order |
| `TestBuild_Golden_Minimal` | `ckMinimal`, empty project root | matches `minimal.txt` |
| `TestBuild_Golden_NoCheckpoint` | `Ref{Seq:0}`, `ckEmpty`, ledger with 3 records | matches `no-checkpoint.txt`; `Degraded==true`; contains items 2, 3, 6b, 7, 8 |
| `TestBuild_Golden_Degraded` | `ckMinimal`, `Request.Budget` = 200 so the tier-1 material cannot all fit | matches `degraded.txt`; `Degraded==true`; `Result.Tokens <= 200`; whatever is emitted is emitted in full, and everything else is represented in the drop report |
| `TestBuild_NonCompactSourceEmitsNothing` | `Source:"startup"`, `"resume"`, `"clear"` | `Result.Items` empty, `Result.Text == ""` |
| `TestBuild_ContextCancelled` | pre-cancelled ctx | returns `ctx.Err()`, no partial write |
| `TestBuild_NilDeps` | `ckFull`, every `Deps` member nil except `Log` | no panic, `Degraded==true`, items 1, 2 (the checkpoint copy, since `Store` is nil) and 8 present, and a `DropEntry{…, ID:"unavailable"}` for each nil source |
| `TestBuild_NoTranscriptRead_ClosesG75` | spy `Deps` recording every call; `Event.TranscriptPath` points at a file containing **no** `<summary>` block (the §2.8 `content: null` failure, where the compaction call ran a tool instead of summarizing) | the payload is byte-identical to the run with a well-formed summary transcript, and the spies record **zero** reads of `TranscriptPath`. This is the mechanical G7.5 closure: the injection is checkpoint-and-store-derived, so a summarizer that produced nothing costs the session nothing |
| `BenchmarkBuild` | `ckFull` plus a synthetic 24 rules, 40 skills, 200 eliminations, 5 000 dag nodes | `L5-BUILD` p99 < 250 ms |

### `internal/daemon` — the service

| Test | Setup | Expected |
|---|---|---|
| `TestService_CompactEmitsAdditionalContext` | fake `checkpoint.Reader`, full deps | `Output.HookSpecificOutput.HookEventName == "SessionStart"`, `AdditionalContext` non-empty and injection-tagged |
| `TestService_DegradedPassiveEmitsNothing` | `contract.Monitor.Mode()==ModeDegradedPassive` | `hookio.Empty()`, no state file written, `Build` never called (spy) |
| `TestService_CheckpointNotFoundStillEmits` | reader returns `core.ErrNotFound` | non-empty payload, `Degraded` recorded in the state file |
| `TestService_CheckpointErrorEmitsNothing` | reader returns `errors.New("io")` | `hookio.Empty()`, one `Loud` |
| `TestService_PanicRecovered` | fake reader panics | `hookio.Empty()`, no re-panic, one `Loud` |
| `TestService_RecordsState` | happy path | `state/rehydrate-<sess>.json` exists, `seq` matches the ref |
| `TestService_StateWriteFailureStillEmits` | read-only `state/` dir | payload emitted, Warn logged |
| `TestService_ClearResetsState` | record then `OnClear` | state file gone, `hookio.Empty(), nil` returned, ledger/store untouched (spies assert zero calls) |
| `TestService_ClearOnMissingStateIsNoOp` | fresh project | `hookio.Empty(), nil` |
| `TestService_DeclaresAdditionalContextProducer` | a daemon built with `BindRehydrate(o, svc)` applied | `Services.Rehydrate != nil` after `Bind` runs, and `contract.CAdditionalContext` has a declared producer — the mechanical form of the §12.1 exit criterion below |

### `test/e2e/sessionstart_compact_test.go`

Drives the **real binary** against a **real daemon** (SP-01's harness).

| Test | Setup | Expected |
|---|---|---|
| `TestE2E_SessionStartCompact` | temp project from `proj-a`, `testdata/golden/contracts/checkpoint/want/0001.json` copied into `.qompack/checkpoints/0001.json` + MANIFEST line, daemon warm | `qompack session-start` with `{"hook_event_name":"SessionStart","source":"compact","session_id":"e2e-1"}` on stdin exits **0** and prints JSON whose `hookSpecificOutput.additionalContext` contains `## 1. Invariants`, `## 7. No longer in context`, `## 8. Retrieval`, and the `<!-- /qompack:injected -->` close tag |
| `TestE2E_ContractSentinelIsAppendedByTheDaemon` | same | `additionalContext` also contains `<!-- qompack-contract-probe `, on a line **after** the close tag — the §12.1 probe is minted by the `session.start` route via `contract.MintSentinel`, not by this slice, and it must survive `checkpoint.StripInjections` |
| `TestE2E_AdditionalContextProducerIsDeclared` | a daemon built the way `cmd/qompack` builds it, `contract.Monitor.RunAll` then `Report()` | the `hook.additional_context_delivered` row's `Observed` is **not** `"not-yet-implemented"` (the literal `contract.notYetImplementedObserved` value asserted by `internal/contract/monitor_test.go:144`) — `Services.Rehydrate` is bound, so `DeclareProducers` declared `CAdditionalContext` and the real check ran |
| `TestE2E_SessionStartCompactUnderBudget` | same | the estimated token count of `additionalContext` is `<= 12 000` |
| `TestE2E_SessionStartClear` | after a compact run | `source:"clear"` exits 0, emits no `additionalContext`, and `.qompack/state/rehydrate-e2e-1.json` is gone |
| `TestE2E_SessionStartCompactNoStore` | `.qompack/` deleted between runs | exits 0, empty or degraded payload, never non-zero |
| `TestE2E_SessionStartCompactAfterFailedSummary` (G7.5) | same as `TestE2E_SessionStartCompact`, but the session transcript's tail contains a tool-call block and **no** `<summary>` — the §2.8 "model called a tool instead of summarizing, `content: null`" failure | exits 0 and emits the *same* `additionalContext` as the well-formed-summary run: the checkpoint is the fallback path, so a failed compaction call degrades cost, not context |
| `TestE2E_DropReporterSatisfiesMCP` | — | `var _ mcp.DropReporter = rehydrate.NewReporter(root, log)` compiles (this file may import both) |
| `TestE2E_SessionStartLatency` | 30 runs, warm daemon | p99 wall time < 1.5 s (`L5-SESSIONSTART`) |

### `test/replay/` — the Phase 3 exit criterion

**One** new `eval.Policy`, registered in a non-test file, plus a `phase3` check wired into the driver's registry.

**There is exactly one "stock" policy, and this branch does not add a second.** `internal/eval/policy.go:57-60` already registers `stock` → `eval.NewStockPolicy`, whose `KeepSet` reproduces §2.4 step 7's top-5-files/5K-per-file/50K-total restore *and* §2.3's preservation window, and which is by definition the policy the committed Phase 0 baseline number describes (`test/replay/phases.go:60`, `baselinePolicyName = "stock"`). An SP-11-defined `stockRestorePolicy` named `"stock-restore"` would put two different definitions of "stock" in one gate — its own defect, and it would not be the thing `phase0` requires. **A2 therefore compares against the registered `stock` policy in the same run**, which is stronger than reading a file: both arms see the identical corpus, the identical budget and the identical demand sets, and `phase0` independently asserts that the run is byte-reproducible and that `stock` is present. §10 Phase 3's phrase "improves against Phase 0 baseline" is not abandoned by that choice, it is discharged separately and already: `compare` (`test/replay/gate.go:144-152`) applies §11.3's 2 % rule to every metric of every policy present in `testdata/baseline/phase0.json`, including `stock.first_divergence_turn = 11`, in the same `replay --baseline` invocation. A2 adds the head-to-head that file cannot express; it does not replace the file, and A2's second conjunct below makes the file's verdict part of the gate.

```go
// test/replay/policy_rehydrate.go — package main, NOT a _test.go file, because `devtool replay`
// shells out to `go run ./test/replay` (tools/devtool/replay.go:22-23) and `go run` does not
// compile test files. A policy that lives only in a _test.go file is invisible to the gate.

// qompackRehydratePolicy derives a §8.5 checkpoint from the session prefix, calls rehydrate.Build
// over it, and reports the emitted payload as its keep-set.
//
// Rule W-2: it never calls checkpoint.Writer — SP-10 is a same-wave sibling. It fills a
// checkpoint.Checkpoint by field assignment from eval.Blocks and the logged turns, which is the
// same technique internal/rehydrate/rehydratetest/behaviour.go uses.
type qompackRehydratePolicy struct{ cfg config.Config; deps rehydrate.Deps }

func (qompackRehydratePolicy) Name() string { return "qompack-rehydrate" }
func (p qompackRehydratePolicy) KeepSet(ctx context.Context, s eval.Session, at core.TurnIndex, budget core.Tokens) (eval.KeepSet, error)

func init() {
    eval.RegisterPolicy("qompack-rehydrate", func(c config.Config) eval.Policy { … })
}
```

**`KeepSet.IDs` must be the harness's own block ids, and nothing else.** `eval.Replay` satisfies a demand only when the keep-set literally contains that demand's `BlockID`: `kept := map[string]bool{…keep.IDs…}` then `for _, d := range demands { if kept[d.BlockID] { continue }; … repairs/lostDecision }` (`internal/eval/replay.go:185-213`). Block ids are prefixed forms minted by `fileBlockID`/`toolBlockID`/`decisionBlockID`/`elimBlockID` (`internal/eval/blocks.go:57-67`) — `file:<pathKey>`, `tu:<id>`, `dec:<id>`, `elim:<12 hex>`. A synthetic `"rehydrate:invariants"` matches nothing; a bare `tool_use_id` matches nothing either, because `toolBlockID` prepends its own prefix. Returning those would leave **every** demand unsatisfied: divergence maximal, so A2 could never pass, and `FractionOfOPT` — §11.1's primary metric — pinned at 0.

Those four constructors are unexported, and `test/replay` is `package main`, so the policy resolves ids through the **exported** `eval.Blocks(s, at)` instead, zipped against the session by the per-turn emission order `Blocks` documents ("the turn's own message block, one block per tool result, one file block per path key first seen here, one block per recorded elimination, and one block per decision minted in the turn's text"):

```go
type blockIndex struct {
    file map[string]string          // paths.Key(path)                    -> file block ID
    tool map[core.ToolUseID]string  // tool_use_id                        -> tool block ID
    dec  map[string]string          // decision id                        -> decision block ID
    elim map[string]string          // paths.Key(target)                  -> elimination block ID
}
```

- **file** — direct: a `BlockFile` carries `Paths[0]`, already in `paths.Key` form.
- **tool** — the k-th `BlockToolResult` of turn *i* is `s.Turns[i].ToolCalls[k]`, in order.
- **elim** — the k-th `BlockElimination` of turn *i* is that turn's k-th `record_eliminated` call; the block also carries `Paths[0] == paths.Key(target)`, which is what the map is keyed on.
- **dec** — the k-th `BlockDecision` of turn *i* is the k-th `[decision:<id>]` marker in `s.Turns[i].Text`.

The zip is deterministic, needs no unexported symbol, and a unit test (`TestBlockIndex_MatchesEvalBlockIDs`) asserts every id it produces is present in `eval.Blocks(s, at)`, so a change to eval's grammar fails here loudly instead of silently zeroing the score.

`KeepSet` then:

1. Builds `cp := sessionCheckpoint(s, at)` — invariants empty, `user_intent.original` from the session's first user turn, eliminations from the `record_eliminated` calls, decisions from the `[decision:…]` markers, file pointers from `eval.Blocks`' file blocks (most recent first), tool pointers from the most recent tool results, `current_work` from the last user turn before `at`.
2. Calls `rehydrate.Build(ctx, rehydrate.Request{Source: "compact", Budget: budget, Checkpoint: cp, Ref: checkpoint.Ref{Seq: 1, Frontier: frontierOf(s, at)}, Cfg: p.cfg}, p.deps)` with `Deps.Graph`, `Deps.Rules` and `Deps.Skills` **nil**: a synthetic session has no DAG, no `.claude/` tree and no skills, so items 6a and 6b are empty and preserve no block anyway. With a nil graph every slice score is 0, which makes item 3's documented tiebreak — TS descending, then ID ascending — fully determined, and that is what lets step 3 recompute the kept subset exactly.
3. Maps what was actually emitted back to block ids: start from everything fed in, then remove what `Result.Dropped` names — `{Kind:"pointer", ID:<path or tool_use_id>}` and `{Kind:"decision", ID:<dec id>}` are removed by id; the single collapsed `{Kind:"elimination", ID:""}` entry means "all but the first `cfg.Runtime.Rehydrate.EliminationsTopN` in the order step 2 fixed", so the survivors are the first N of that order. `IDs` is the resulting id set, sorted, deduplicated.
4. `Tokens = Result.Tokens` **exactly** — the same number `runBudgetCase`, the hard-cap property test and `TestE2E_SessionStartCompactUnderBudget` check, so A1 and the unit tests cannot disagree about what "rehydration budget" means. The policy also records `max(Result.Tokens)` across the run into a package-level `maxRehydrationTokens` that `phase3` reads, because that per-session maximum exists nowhere else: `Policies[…]["rehydration_tokens"]` is a sum. This is what keeps §8.6:974's "Measure this in the harness" true of the harness, and it is what A1's second conjunct asserts.
5. `P = 0`, the same value `eval.stockPolicy` returns and for the same reason: a Full Compact rewrites the whole message array, so `p_min` is 0. §12 is explicit that a plugin "cannot modify the message array directly", and p-selection is SP-12's (out of scope here). Holding `P` equal across both arms is what keeps A1 and A2 measuring rehydration budget and divergence rather than a rewrite-cost artifact.

**The check runs inside the driver binary, not in a test.** `test/replay/phases.go` holds `var phaseChecks = map[int]func(Context) error{ 0: phase0 }` and `runPhaseChecks` executes every entry at or below `--phase` on every pull request — that is the mechanism behind §11.3's "once a phase has landed, its exit criterion is re-asserted forever". SP-11 **appends** `3: phase3` to that map and adds `func phase3(c Context) error` beside `phase0`. `phase3` reads the driver `Context` — `c.Report`, `c.Driver` and `c.Cfg` — and has no `*testing.T`; it returns a `fmt.Errorf` naming both numbers on failure, exactly as `phase0` does. `test/replay/phase3_rehydrate_test.go` unit-tests `phase3` against hand-built `Context` values (pass, each failure arm) and unit-tests the policy and `blockIndex`, but it is not where the gate lives.

| Assertion | Definition | Threshold |
|---|---|---|
| **A1 — smaller budget** | `c.Driver.Policies["qompack-rehydrate"]["rehydration_tokens"]` vs `c.Driver.Policies["stock"]["rehydration_tokens"]`, over the 24-session synthetic corpus | `qompack < stock` on the corpus-wide `rehydration_tokens` sum, **and** `qompackRehydratePolicy` recorded no session whose `rehydrate.Build` `Result.Tokens` exceeded `cfg.Runtime.Rehydrate.MaxTokens` (12 000) — the quantity §8.6 names. The policy calls `Build` once per compaction point and is the only place the per-session number exists: `Policies[…]["rehydration_tokens"]` is a **sum** over sessions (`internal/eval/score.go:321`), so a per-session ceiling cannot be recovered from it by any arithmetic the driver has. `c.Driver.BudgetViolations` is **not** that check and must not be used as one: it fires on `eval.DefaultKeepBudget` (40 000, §2.3's host preservation cap, `internal/eval/types.go:355`), and any non-empty value already fails the whole run at `test/replay/main.go:336-341`, so it can never make A1 fail independently |
| **A2 — first-divergence improves** | `c.Driver.Policies[…]["first_divergence_turn"]` for `qompack-rehydrate` vs `stock` — the registered baseline policy, scored in the same run over the same ≥ 20 sessions carrying a `CompactionAt` point | `qompack > stock` in the run, **and** the same invocation's `compare` step reports no regression on `stock.first_divergence_turn` against the committed `testdata/baseline/phase0.json` (`test/replay/gate.go:144-152`, §11.3's 2 % rule, base 11). The gate is the conjunction: the in-run arm-vs-arm result plus the committed-file verdict on the arm it is measured against. That second conjunct is what makes a *joint* regression fail — an in-run win over a `stock` arm that has itself slipped by more than 2 % below 11 is a `compare` failure, and the whole `replay --phase 3 --baseline testdata/baseline/phase0.json` invocation exits non-zero. So the pair satisfies §10's "improves against Phase 0 baseline" on its face and is strictly stronger than reading the file, which cannot express the head-to-head at all. The error message prints both numbers so the replay-gate artifact is self-explaining |
| **A3 — O1 summarization-input reduction** | `residual` = Σ tokens of the turns in the half-open range (F, at], where `F = frontierOf(s, at)`; `stockSpan` = Σ tokens of every turn up to the compaction point | `residual <= core.Tokens(c.Cfg.Checkpoint.Frontier.MaxResidualTokens)` (20 000) **and** `1 - residual/stockSpan >= 0.5` on the multi-compaction sessions. The driver surfaces it as `c.Driver.Policies["qompack-rehydrate"]["residual_span_p95"]`, which `eval.ScoreRun` already fills from `Run.ResidualSpan` |
| **A4 — tier-1 material is not being dropped** | `tier1Drops` / `builds`, both recorded by `qompackRehydratePolicy` beside `maxRehydrationTokens`: `builds` counts `rehydrate.Build` calls, `tier1Drops` counts those whose `Result.Items` carries **no** `Item` of kind `ItemAffordance` (a fixed string that is always available, so its absence means step 3 could not admit it whole) or whose `Result.Items` carries no `ItemUserIntent` while the request's `Checkpoint.UserIntent.Original` was non-empty — the two tier-1 units the synthetic corpus always supplies | `tier1Drops == 0` over the 24-session synthetic corpus at the default 12 000 budget, and `phase3` prints `tier1Drops/builds` in the gate artifact **on pass as well as on failure**. Step 3 is allowed to drop tier-1 material rather than overrun the cap (§8.6:957), so nothing else in this branch would notice if it started happening at the configured budget — and at 12 000 it should never happen, because the corpus's tier-1 material is a few hundred tokens. A non-zero rate therefore means the builder is mis-charging, not that the budget is too small, and it is the number §8.6:974's "Measure this in the harness before relaxing it" asks for |

A3's qualifier — §10's *"when the span instruction is honoured"* — is modelled by `eval.ReplayOptions{Deterministic: true}`, which assumes compliance. Non-compliance is explicitly out of scope for this assertion: §8.5 states `custom_instructions` is advisory and *"the rehydrator never depends on the summary having complied"*, and the checkpoint remains authoritative. A comment in `phase3` cites that sentence so no future reader mistakes the assumption for an oversight.

**`frontierOf(s, at)` is defined here, deterministically, and does not wait on SP-12.** There is no `Frontier` value in any committed golden — `testdata/golden/contracts/checkpoint/want/0001.json` is a `Checkpoint`, and `Frontier` is a field of `checkpoint.Ref`, which SP-10's reader returns at run time. Live frontier advancement is SP-12's (same wave). So the driver defines the frontier as **the index of the last `Role == "user"` turn at or before `at`** — a new user prompt is the natural segment boundary §8.2's changepoint discussion describes, it is derivable from any session with no extra state, and it makes A3 a real measurement rather than a tautology (defining `F = at` would make `residual` identically 0 and A3 unfailable). The V4 verification run re-executes this file against SP-10's and SP-12's real implementations and replaces `frontierOf` with the real `Ref.Frontier` then. **That substitution is already written down on the other side of the handoff**: `plans/V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md` §A3's "What changes at V4" paragraph instructs V4 to derive the frontier from `.qompack/state/precompact.json` — `Ref.Frontier` is writer-only and is zero on any reader-returned `Ref` (`TestReaderRefLeavesWriterOnlyFieldsZero`) — and to assert that the number the test uses is the number the writer produced. Read the two together. If A3's number moves when the real frontier is substituted, that is a V4 finding, not a fixture refresh. No SP-12 handoff is needed: `frontierOf` lives entirely inside the replay driver and never touches `Ref.Frontier`.

---

## Commit plan

Work happens on **`feat/sp11-rehydrator-l5`**, cut from `develop` with `SP-01`, `SP-06`, `SP-07`, `SP-09` (and, by wave ordering, `SP-08`) already merged — **and with step 0's amendment already merged into `develop` first**. Exactly **7 commits on the feature branch**, preceded by step 0's single commit on its own `arch/` branch. Each compiles and passes `go test` for the packages it touches before it is made.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

> **`devtool test` and `devtool lint` do not take package or task arguments.** `taskTest` ignores its arguments entirely and always runs `go test -timeout=30m ./...` (`tools/devtool/test.go:22-37`); `devtool` dispatches exactly **one** task per invocation (`tools/devtool/main.go:63-64`); and `taskLint` parses only flags, so `devtool lint vet fmt` would run `lint` and silently skip `vet` and `fmt`. Every per-package check below is therefore a plain `go test <pkg>`, and every lint/vet/fmt step is three separate invocations.

### Step 0 — the §0 amendment (on `arch/rehydrate-item-kinds`, merged to `develop` before the feature branch is cut)

The two additive `ItemKind` constants cannot be declared after `ItemAffordance`. `internal/rehydrate/types.go:16-43` declares the eight §8.6 kinds as a normative `iota` block whose comment says "Do not reorder them, and do not insert into the middle", and `runItemOrderCase` asserts `require.Greater(t, kinds[i], kinds[i-1], "items must be emitted in ascending ItemKind, without repeats")` (`internal/rehydrate/rehydratetest/behaviour.go:146-149`). Rendering restored instructions and the skill index between items 6 and 7 while declaring them at values 8 and 9 emits the kind sequence `0,1,2,3,4,5,8,9,6,7`, which that assertion rejects. Inserting them into the middle is the correct fix and it is exactly what §0 exists to authorize. This is not optional and it is not deferrable to a later branch: nothing in SP-11 compiles into a passing suite without it.

- [ ] `git fetch origin && git checkout develop && git pull && git checkout -b arch/rehydrate-item-kinds`
- [ ] **00-ARCHITECTURE §5.15** — replace the `ItemKind` block with the ten-constant form, in rendered order, and change the closing comment from "ORDER IS NORMATIVE — this is the §8.6 importance order." to "ORDER IS NORMATIVE — the eight §8.6 items in importance order, with the two instruction-restoration kinds of §8.6's 'Instruction restoration' clause inserted at their rendered position between items 6 and 7.":
  ```go
  const (
      ItemInvariants ItemKind = iota  // 1. pins, verbatim, always
      ItemUserIntent                  // 2. verbatim original intent, from L0 (G2.3)
      ItemEliminations                // 3. top-N by slice score + "already_tried covers the rest"
      ItemDecisions                   // 4. decisions with rationale
      ItemCurrentWork                 // 5. current work and next step
      ItemPointers                    // 6. pointers, not contents
      ItemRestoredInstructions        // 6a. path-scoped rules and nested CLAUDE.md (G4.1, G4.2)
      ItemSkillIndex                  // 6b. the compact skill index (G4.4)
      ItemDropReport                  // 7. explicit drop report (G4.5)
      ItemAffordance                  // 8. one line: recall / re_read / already_tried exist
  )
  ```
- [ ] **00-ARCHITECTURE §5.15:1603** — the second, behavioural half of this amendment (see the `NestedClaudeMD` spec above): replace `// NestedClaudeMD returns CLAUDE.md files in directories containing a pointer-set file.` with `// NestedClaudeMD returns CLAUDE.md files in directories containing, or ancestor to, a pointer-set` / `// file (bounded at maxAncestorDepth=32, stopping before the project root, which Claude Code` / `// re-injects itself — §2.7).` This is what authorizes the ancestor walk, and it must land here rather than being assumed: `Qompack.md:971` and the shipped doc comment at `internal/rules/rules.go:32-33` disagree, and §5.15 currently restates the narrow side. Raise the companion `Qompack.md:971` correction PR in the same round — this plan set may not edit that file (`plans/README.md:62`). **If the coordinator rules the other way**, this bullet is replaced by its mirror: leave §5.15 alone, and instead amend `internal/rules/rules.go:32-33` to the containing-directory-only wording and move `internal/rules/rulestest/suite.go`'s `writeNestedClaudeMDFixture` `CLAUDE.md` from `src/pkg` down to `src/pkg/deep` — then the ancestor walk comes out of the `NestedClaudeMD` spec and its `TestNestedClaudeMD_WalksAncestors`, `TestNestedClaudeMD_AncestorTwoLevelsUp` and `TestNestedClaudeMD_AncestorWalkStopsBeforeRoot` rows come out of the test plan.
- [ ] **`internal/rehydrate/types.go`** — apply the same ten constants with the same doc comments, and replace "Do not reorder them, and do not insert into the middle." with "Do not reorder them. Inserting a kind requires a §0 amendment to 00-ARCHITECTURE §5.15, because the values are what budget truncation drops from the tail of."
- [ ] **`internal/rehydrate/rehydratetest/behaviour.go`** — one assertion, message only. The bound assertion at :153-154 still binds (`ItemAffordance` is still the last constant, so nothing may carry a kind beyond it), but its message no longer describes the constant set. Change it to:
  ```go
  require.LessOrEqual(t, int(kinds[len(kinds)-1]), int(rehydrate.ItemAffordance),
      "no item may carry a kind beyond the ten §5.15 kinds (the eight §8.6 items plus the two instruction-restoration kinds)")
  ```
  Nothing else in `runItemOrderCase` changes: with the constants in rendered order, `renderOrder` *is* the `iota` sequence, so the ascending-kind assertion and the 0-based `Rank` assertion both hold unchanged. **Do not weaken either of them, and do not touch any other case.**
- [ ] `go run ./tools/devtool fmt` · `go run ./tools/devtool vet` · `go run ./tools/devtool lint` · `go run ./tools/devtool test` — all green (the suites still skip: `Build` is still a stub at this point).
- [ ] Merge `arch/rehydrate-item-kinds` into `develop`, then cut the feature branch from the merged tip. **This merge happens before *any* wave-3 branch is cut, not just this one** — see prerequisite 2 at the top of this plan. It is the wave-3 coordinator's step; an `arch/` merge landed after SP-10, SP-12 and SP-13 exist leaves three siblings carrying a different §5.15.

Footer: `Refs: SP-11, §0, 00-ARCH §5.15, §8.6`

```
- [ ] git fetch origin && git checkout develop && git pull   # step 0 already merged
- [ ] git checkout -b feat/sp11-rehydrator-l5
- [ ] go run ./tools/devtool ci-local        # baseline must be green before any change
```

### Commit 1 — `feat(rules): paths:-frontmatter scanner, **-aware globs, nested CLAUDE.md discovery`

Closes G4.1 and G4.2.

- [ ] Create `testdata/fixtures/rules/proj-a/**` and `testdata/fixtures/rules/proj-edge/**` exactly as listed in the fixtures section.
- [ ] Write `internal/rules/glob_test.go` (the 9 `TestMatch_*` table rows + `PropMatch_NeverPanics`) — **run, must fail to compile** (`Match` undefined).
- [ ] Write `internal/rules/frontmatter_test.go` (the 8 `TestParseFront_*` rows) — must fail.
- [ ] Write `internal/rules/scanner_test.go` (the 10 `TestPathScoped_*` + 9 `TestNestedClaudeMD_*` rows + `BenchmarkPathScoped`) — must fail.
- [ ] Implement `internal/rules/glob.go`, `frontmatter.go`, `scanner.go` — including the ancestor walk, and keeping `New`'s zero-argument call form (`New(opts ...Option)`).
- [ ] `go test ./internal/rules/...` — all green, **including `rulestest`'s `/behaviour` block, which un-skips itself the moment `PathScoped` stops returning `core.ErrNotImplemented`**. Its `nested_claude_md_discovery` case is the ancestor-walk gate; if it fails, the walk is wrong, not the fixture.
- [ ] `go run ./tools/devtool bench` and confirm `BenchmarkPathScoped` meets `L5-RULES` (< 50 ms p99).
- [ ] `go run ./tools/devtool fmt` · `go run ./tools/devtool vet` · `go run ./tools/devtool lint` — three separate invocations.

Files: `internal/rules/{glob,frontmatter,scanner}.go`, `internal/rules/{glob,frontmatter,scanner}_test.go`, `testdata/fixtures/rules/**`.
Footer: `Refs: SP-11, G4.1, G4.2, §8.6`

### Commit 2 — `feat(skills): compact skill index budgeted from runtime.rehydrate.skillIndexTokens`

Closes G4.4.

- [ ] Write `internal/skills/indexer_test.go` (the 11 rows of the `internal/skills` table + `BenchmarkIndex`) — must fail.
- [ ] Implement `internal/skills/frontmatter.go` (with the mandatory "duplicated by §3.2 import policy" comment) and `internal/skills/indexer.go` including `BodyTokens`, keeping `New`'s zero-argument call form (`New(opts ...Option)`).
- [ ] `go test ./internal/skills/...` — green, **including `skillstest`'s `/behaviour` block**, which un-skips itself once `Index` stops returning `core.ErrNotImplemented`. Its `respects_the_budget` case runs at a 16-token budget over 12 skills and asserts `total <= budget`, which is the same prefix-truncation rule specified above.
- [ ] `go run ./tools/devtool lint` — the `nomagic` pass must report **no** literal `450` in `internal/skills`.
- [ ] `go run ./tools/devtool bench` — `L5-SKILLS` < 20 ms p99.

Files: `internal/skills/{frontmatter,indexer}.go`, `internal/skills/indexer_test.go`.
Footer: `Refs: SP-11, G4.4, §8.6, §11.6`

### Commit 3 — `feat(rehydrate): eight-item importance-ordered injection with L0 verbatim intent`

Closes G2.3's read side and G7.3.

- [ ] Write `internal/rehydrate/fake_test.go`: `fakeStore`, `fakeLedger`, `fakeGraph`, `fakeReader`, `spyLogger`, plus the `ckFull` decoder for `testdata/golden/contracts/checkpoint/want/0001.json` and the `ckMinimal` / `ckEmpty` in-test values (Rule W-2: no `checkpoint.Writer` call anywhere).
- [ ] Write `internal/rehydrate/order_test.go`, `items_test.go`, `render_test.go` (the "Ordering and structure", "Item 1", "Item 2", "Item 3", "Item 4/5/6" and "Items 6a/6b" tables) — must fail.
- [ ] Implement `internal/rehydrate/{render,items}.go` and the item-building half of `build.go`. No `sentinel.go` is created: the §12.1 probe is the daemon's.
- [ ] `go test ./internal/rehydrate/...` — green.
- [ ] Confirm `TestUserIntent_FromL0NotCheckpoint` and `TestUserIntent_ResolvesTheDerivedFirstPromptID` pass: together they are the mechanical G7.3 closure, and the second is what stops a relevance search from injecting a mid-session prompt as "the original intent".

Files: `internal/rehydrate/{build,items,render}.go`, `internal/rehydrate/{fake,order,items,render}_test.go`.
Footer: `Refs: SP-11, G2.3, G7.3, G7.5, §8.6, §8.7`

### Commit 4 — `feat(rehydrate): 8-12K budget fill, progressive truncation, and the drop report`

Closes G3.3 and G4.5.

- [ ] Write `internal/rehydrate/budget_test.go` (the Budget table incl. `PropBuild_MonotoneInBudget`, `PropBuild_NeverPanics`, `PropBuild_Deterministic`) — must fail.
- [ ] Write `internal/rehydrate/drops_test.go` (the Reporter table) — must fail.
- [ ] Write `internal/rehydrate/golden_test.go` with `-update` support; generate `testdata/golden/rehydrate/{full-12k,full-8k,minimal,no-checkpoint,degraded,state.json}` and **read every generated golden by eye** before committing — a golden accepted without reading is a placeholder.
- [ ] Implement `internal/rehydrate/{budget,drops}.go` and complete `Build`.
- [ ] Add `TestBuild_NoTranscriptRead_ClosesG75` and confirm it passes: the spies must record zero reads of `Event.TranscriptPath`, and the payload must be byte-identical with and without a well-formed `<summary>` in the transcript. This is G7.5's unit-level closure and it is the reason the footer below names G7.5.
- [ ] `go test ./internal/rehydrate/...` — green, and this is the commit where `rehydratetest`'s `/behaviour` block **stops skipping on its own**, because `Build` no longer returns `core.ErrNotImplemented`. All nine inherited cases must pass with **no edit to `internal/rehydrate/rehydratetest/`**: `items_are_in_the_normative_item_kind_order` (0-based `Rank`, ascending kinds), `total_tokens_never_exceed_the_budget` (hard cap at 9000/600/1, and `Result.Tokens == Σ Items`), `what_did_not_fit_is_reported_as_dropped`, `the_payload_is_injection_tagged`, `the_eliminations_item_carries_the_standing_instruction` (and, with no item 3, that the sentence appears nowhere), `a_starved_budget_degrades_rather_than_failing`, `the_result_names_the_checkpoint_it_came_from`, `build_is_deterministic`, `every_session_start_source_is_answered`. A failure here is a bug in `Build`, never a reason to edit the suite.
- [ ] `go run ./tools/devtool cover` — `rehydrate` ≥ 85%, `rules`/`skills` ≥ 75%.
- [ ] `go run ./tools/devtool bench` — `BenchmarkBuild` meets `L5-BUILD` (< 250 ms p99).

Files: `internal/rehydrate/{budget,drops}.go`, `internal/rehydrate/{budget,drops,golden,bench}_test.go`, `testdata/golden/rehydrate/**`.
Footer: `Refs: SP-11, G3.3, G4.5, G7.5, §8.6, §6.9, §12`

### Commit 5 — `feat(daemon): SessionStart compact and clear branches behind the rehydrator seam`

- [ ] Write `internal/daemon/rehydrate_service_test.go` (the service table, 10 tests) — must fail.
- [ ] Write `test/e2e/sessionstart_compact_test.go` (the 9 rows of the e2e table plus `TestUserIntent_FirstPromptIDMatchesObserver` from the item-2 table, which lives here because only `test/e2e` may import both `rehydrate` and `observer`; and including `TestE2E_SessionStartCompactAfterFailedSummary`, which is G7.5's end-to-end closure) — must fail.
- [ ] Implement `internal/daemon/rehydrate_service.go`, including `BindRehydrate`.
- [ ] **Do not add a member to `daemon.Services`.** Create the daemon startup block in `internal/cli/daemon.go` (prerequisite 1: `symbols`, `store` with `Deps.Symbols`, `dag`, `negknow`, `checkpoint.Reader`, each failure `Loud` and nil-tolerant), then build the service and call `BindRehydrate(o, svc)` so the already-shipped `Services.Rehydrate` seam is set — that is what makes `DeclareProducers` declare `contract.CAdditionalContext` (`internal/daemon/options.go:150-152`). Then pass the same `svc` into the observer construction site for the `source` switch. **That observer field assignment is the only EXISTING line this branch rewrites in an SP-05/SP-08-owned file** — which is not the same as the only edit: the bootstrap block above is ~28 ADDED lines in `internal/cli/daemon.go`, SP-05's file, and they are sanctioned by prerequisite 1 and by the four-writer shared-file protocol in `plans/V4-SP-12-scheduler-l3.md`, not by this bullet. Read the two together: this branch adds a block and rewrites one line, and nothing else outside its own files.
- [ ] `go test ./internal/daemon/... ./test/e2e/...` — green.
- [ ] `go run ./tools/devtool build-all` — all six targets compile.
- [ ] Verify by hand that `qompack session-start` exits 0 for `source` ∈ {`startup`,`resume`,`compact`,`clear`} and for malformed stdin (§13 invariant 6).

Files: `internal/daemon/rehydrate_service.go`, `internal/daemon/rehydrate_service_test.go`, `internal/cli/daemon.go` (the bootstrap block) and the observer construction site, `test/e2e/sessionstart_compact_test.go`.
Footer: `Refs: SP-11, §7.3, §8.6, §12.1, 00-ARCH §5.21`

### Commit 6 — `test(replay): Phase 3 exit criterion — divergence, budget, and O1 span reduction`

- [ ] Write `test/replay/phase3_rehydrate_test.go`: unit cases for `blockIndex` (`TestBlockIndex_MatchesEvalBlockIDs`), for `qompackRehydratePolicy.KeepSet` (every returned id is present in `eval.Blocks(s, at)`; `Tokens == Result.Tokens`; `P == 0`), and for `phase3` against hand-built `Context` values — one passing, one per failure arm — must fail.
- [ ] Implement `test/replay/policy_rehydrate.go` (a **non-test** file: `go run ./test/replay` does not compile `_test.go` files, so a policy that lives only in a test file is invisible to the gate) with `qompackRehydratePolicy` and its `init()` calling `eval.RegisterPolicy("qompack-rehydrate", …)`. **Add no second "stock" policy** — `eval` already registers the one `phase0` requires.
- [ ] Append `3: phase3` to `phaseChecks` in `test/replay/phases.go` and add `func phase3(c Context) error` beside `phase0`. `runPhaseChecks` then re-asserts it on every pull request at `--phase >= 3`, which is what §11.3's "once a phase has landed, its exit criterion is re-asserted forever" means (`test/replay/phases.go:25-34, 62-79`).
- [ ] `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --policies stock,null,qompack-rehydrate --phase 3` — A1, A2 and A3 all pass; capture the printed numbers in the commit body. (Every argument after `replay` is forwarded verbatim to the driver, whose flags are `-corpus`, `-baseline`, `-policies`, `-phase`, `-out`, `-signoff`, `-growth`, `-sketch`, `-ci`, `-max-cpu`, `-max-wall`. Never pass a bare `--`: the driver refuses positional arguments precisely because a separator would silently discard every flag after it.)
- [ ] Confirm no other metric regresses by more than 2% (§11.3). If one does, the PR body needs a `sign-off:` trailer — but the expected outcome here is a strict improvement on rehydration budget and first-divergence with no regression elsewhere, because this branch adds no hot-path work.

Files: `test/replay/policy_rehydrate.go`, `test/replay/phases.go` (+`3: phase3` and the check function), `test/replay/phase3_rehydrate_test.go`.
Footer: `Refs: SP-11, §10 Phase 3, §11.1, §11.2, §11.3`

### Commit 7 — `docs(rehydrate): ADR 0011 on item order, budget shares, and whole-rule restoration`

- [ ] Write `docs/adr/0011-rehydration-budget-and-item-order.md` covering: why the ten `ItemKind` constants are declared in rendered order and `renderOrder` is asserted to equal the `iota` sequence, and why that needed the step-0 §0 amendment rather than a trailing declaration; why `Item.Rank` is 0-based; **why `Request.Budget` is a hard cap that is never raised, and why `minTokens` is therefore a fill target for an unset request only**; **why the wrapper overhead is charged to the first emitted item rather than to a synthetic `ItemStat`** (Σ `Items` must equal `Result.Tokens`); the exact six share percentages, why intent-evolution takes a share of its own, and the carry-forward rule; why a path rule is restored whole or not at all (G4.3 — "a partial instruction set is worse than an absent one"); **why `NestedClaudeMD` walks ancestors** rather than reading §8.6's sentence literally; **why the original intent is resolved through the derived `prompt_<session>_0` id rather than `Store.Search`** (Search clamps K to 100 and ranks newest-first, so the earliest prompt of a long session is the first thing it excludes — and the resulting failure injects a mid-session prompt as "the original intent", inverting G7.3); **why `guardNoContents` covers items 4–6 only and must never inspect the verbatim tier-1 items 1–2** (a fenced stack trace in a real user prompt would otherwise re-open G7.3); **why item 3 reads both elimination scopes rather than `eliminations.defaultScope`** (§8.3 project-scoped records are the longest-lived negative knowledge in the store); **why the standing instruction belongs to item 3 and is absent when item 3 is**; why `rehydrate.Build` takes no clock and is a pure function; **why this slice mints no sentinel** and defers entirely to the daemon's `contract.MintSentinel`/`RenderSentinel`; and why the `clear` branch touches no ledger state.
- [ ] `go run ./tools/devtool gen-config-docs && git diff --exit-code` — `docs/config-reference.md` must already be current (this branch adds no config keys; SP-01 owns them). If it diffs, the diff is SP-01's and must be reported, not absorbed.
- [ ] `go run ./tools/devtool ci-local` — full local pipeline green.
- [ ] `git log --format=%B develop..HEAD | grep -Ei "Co-Authored-By|Signed-off-by|Generated with|🤖"` returns nothing.

Files: `docs/adr/0011-rehydration-budget-and-item-order.md`.
Footer: `Refs: SP-11, §8.6, §6.9, G4.3`

---

## Subagent strategy

This subplan is **heavy**. Partition it across four parallel subagents in the implementing editor's own session, then integrate sequentially. The commit plan stays strictly sequential and is executed only by the main session.

**Main session keeps (never delegated).** Every `git` operation; **step 0's §0 amendment**; the commit sequence; `internal/rehydrate/budget.go` and `build.go` (the integration point where all four subagents' outputs meet — splitting it produces merge conflicts inside one function); the golden files (they must be read by the agent that owns the whole payload); `internal/daemon` wiring; and every decision about §8.6 ordering.

**Subagent A — `internal/rules`.** Owns `glob.go`, `frontmatter.go`, `scanner.go` and their three test files, plus `testdata/fixtures/rules/proj-a/**` and `proj-edge/**`. Returns: the three source files, the three test files, the fixture tree, and the measured `BenchmarkPathScoped` p99. Constraint given to it: **may import foundation packages only** (`core`, `paths`, `config`, `logging`, `obs`) — an import of `tokens` is an architecture violation that the import-graph check will reject.

**Subagent B — `internal/skills`.** Owns `frontmatter.go`, `indexer.go`, `indexer_test.go`, and the `.claude/skills/**` half of `proj-a`. Returns those files plus the `BenchmarkIndex` p99. Constraint: same foundation-only import budget; the budget parameter is passed in, never read from a constant — the `nomagic` pass will fail on a literal `450`.

**Subagent C — `internal/rehydrate` item builders.** Owns `items.go`, `render.go` and their test files plus `fake_test.go`. Returns those files and the exact rendered text of each item for one canonical input, which the main session pastes into the ADR and uses to hand-verify the goldens. Constraint: every builder returns `([]unit, seen int)` and performs **no budget arithmetic** — budgeting is the main session's file. It writes no `sentinel.go`, and it uses `dag`'s exported node constructors everywhere.

**Subagent D — the harnesses.** Owns `internal/daemon/rehydrate_service_test.go`, `test/e2e/sessionstart_compact_test.go`, `test/replay/policy_rehydrate.go` and `test/replay/phase3_rehydrate_test.go` — one `eval.Policy`, not two. Returns those four files. It writes tests against the interfaces in this document's Interface contract section and does **not** wait for A/B/C — the tests are supposed to fail first. Constraint: `KeepSet.IDs` must come from `eval.Blocks`, never from a hand-built id string; the main session appends the `3: phase3` entry to `test/replay/phases.go` itself, since that file is shared.

**Integration order.** A and B land in commits 1 and 2 unchanged. C's output lands in commit 3 after the main session reconciles the unit signatures. The main session then writes `budget.go` and `drops.go` (commit 4) against C's builders and generates the goldens. D's daemon and e2e tests land in commit 5 alongside the main session's `rehydrate_service.go`; D's policy and replay test land in commit 6 alongside the main session's `phases.go` edit.

**Conflict avoidance.** No two subagents touch the same file. A and B both write into `testdata/fixtures/rules/proj-a/` but into disjoint subdirectories (`.claude/rules/` and `src/` for A, `.claude/skills/` for B); the main session creates the shared root files (`CLAUDE.md`, `src/*/CLAUDE.md`) before dispatching.

**What must be verified in the main session regardless of what a subagent reports.** That `renderOrder` equals the `iota` sequence and matches the §8.6 numbering; that no golden contains a fenced code block in items 4, 5 or 6 (items 1–3 are exempt by design and must be checked the other way — that a fenced user prompt *did* survive verbatim); that no literal `450`, `8000` or `12000` appears outside `_test.go`; that `Result.Tokens <= Request.Budget` and `Result.Tokens == Σ Items[i].Tokens` hold on every golden; that no `dag.NodeID("…")` string literal survives anywhere in `internal/rehydrate`; that `internal/rehydrate/rehydratetest/`, `internal/rules/rulestest/` and `internal/skills/skillstest/` are byte-identical to `develop` on the feature branch; and that every hook path exits 0.

---

## Exit criteria

### Quoted verbatim from Qompack.md §10 Phase 3

> **Exit criterion:** post-compaction first-divergence turn index improves against Phase 0 baseline at a *smaller* rehydration budget than stock; measured reduction in summarization-call input tokens when the span instruction is honoured.

Operationalized by `phase3` in `test/replay/phases.go` — assertions A1, A2 and A3 as specified above — reached through `runPhaseChecks` inside the `go run ./test/replay` driver binary, over the 24-session synthetic corpus (≥ 20 sessions, satisfying `eval.minSessions`). It is registered in the `phaseChecks` map, not in a `_test.go` file, because `devtool replay` shells out to `go run` and `go run` does not compile test files; the map is what makes the criterion re-assert on every later pull request.

### Quoted verbatim from Qompack.md §8.6

> **Budget discipline.** Default rehydration budget is deliberately far below Claude Code's 50K + 25K: target 8–12K.

Operationalized by `TestBuild_NeverExceedsRequestedBudget`, `TestBuild_SmallerThanStock`, `TestBuild_MinFillReadmitsUnits`, `PropBuild_MonotoneInBudget`, `TestE2E_SessionStartCompactUnderBudget`, and `rehydratetest`'s inherited `total_tokens_never_exceed_the_budget` case.

### Quoted verbatim from Qompack.md §11.3 (the three of its four bullets that bind this branch; the omitted one is "Store growth sublinear in session length after dedup", which is SP-06's)

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

This branch adds no L0 work and no L4 work, so B-A and B-E are unchanged; `bench-gate` must remain green on all three platforms, and `replay-gate` must report no >2% regression on any metric.

### Local criteria

- [ ] All tests enumerated in the Test plan exist, are named as written, and pass: `go run ./tools/devtool test` and `go run ./tools/devtool test-race` (both run the whole tree; neither takes package arguments).
- [ ] The three inherited conformance suites run their `/behaviour` blocks rather than skipping: `go test -v ./internal/rehydrate/... ./internal/rules/... ./internal/skills/... | grep "Rule W-1"` returns nothing.
- [ ] Coverage: `internal/rehydrate` ≥ **85%**, `internal/rules` ≥ **75%**, `internal/skills` ≥ **75%** (§6.4).
- [ ] `gofumpt -l` empty; `golangci-lint run` clean; `go vet` clean; `staticcheck` clean.
- [ ] The `nomagic` pass reports no forbidden literal in `internal/rehydrate`, `internal/rules`, `internal/skills` — specifically no `450`, `8000`, `12000`.
- [ ] The import-graph check passes: `rules` and `skills` import foundation only; `rehydrate` imports only `checkpoint store negknow dag rules skills tokens` + foundation; nothing imports `daemon`.
- [ ] Benchmarks within budget: `L5-BUILD` < 250 ms p99, `L5-RULES` < 50 ms p99, `L5-SKILLS` < 20 ms p99, `L5-SESSIONSTART` < 1.5 s p99; `benchstat` shows no >25% regression against `testdata/bench-baseline.txt`.
- [ ] `devtool plugin-validate` green (this branch changes no manifest, so the diff must be empty).
- [ ] `security` CI job green: no `net/http`, no `net/url`, no `crypto/tls`, no writes outside `.qompack/`.
- [ ] Every hook subcommand exits 0 under fault injection for `source` ∈ {`startup`,`resume`,`compact`,`clear`} and for malformed stdin.
- [ ] Freshly built `develop` + this branch reports `contract.ModeFull` (§12.1's CI assertion still holds; `hook.additional_context_delivered` now has a real producer — because `BindRehydrate` sets `Services.Rehydrate`, which is the sole condition `DeclareProducers` tests — and must report `OK` rather than `not-yet-implemented`).
- [ ] CI green on `feat/sp11-rehydrator-l5` for every job: `verify`, `test` (ubuntu/macos/windows), `cover`, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs`.
- [ ] Exactly 7 commits on the feature branch, each a valid Conventional Commit with a `Refs:` footer — plus step 0's single commit, which is already in `develop`.

---

## Done checklist

- [ ] Step 0's `arch/rehydrate-item-kinds` amendment is merged into `develop`, and branch `feat/sp11-rehydrator-l5` was cut from that merged tip with SP-01, SP-06, SP-07, SP-09 merged.
- [ ] **Spec coverage vs. the Design context section.** Walk the Design context section top to bottom and tick each quoted artifact against an implementing test: §8.6's eight numbered items (`TestBuild_ItemOrderInPayload`), its three instruction-restoration bullets (`TestRestored_*`, `TestSkillIndex_*`), its budget sentence (`TestBuild_NeverExceedsRequestedBudget`, `TestBuild_SmallerThanStock`), §8.7's design note (`TestEliminations_StandingInstructionIsItem3sCompanion`), §8.7's tool table (`AffordanceNotice` golden), §8.5's injection tagging (`TestBuild_InjectionTagging`), §8.5's schema (every checkpoint field consumed by a builder), §8.3's stale note (`TestEliminations_StaleRendersTheVerbatimNote`), §6.9's embedded coding (`PropBuild_MonotoneInBudget`), §2.7's four broken rows (G4.1/G4.2/G4.3/G4.4 tests), §11.5's four `rehydrate` keys (each read at least once, none hardcoded), §12.1's degraded-passive clause (`TestService_DegradedPassiveEmitsNothing`) and its `additionalContext` assertion (`TestService_DeclaresAdditionalContextProducer`, `TestE2E_AdditionalContextProducerIsDeclared`), §8.1 item 7's verbatim capture as the *only* intent source (`TestUserIntent_ResolvesTheDerivedFirstPromptID`, `TestUserIntent_RejectsRecordFromAnotherSession`), G7.5's checkpoint-as-fallback (`TestBuild_NoTranscriptRead_ClosesG75`, `TestE2E_SessionStartCompactAfterFailedSummary`), and §10 Phase 3's exit criterion (A1/A2/A3).
- [ ] **Placeholder scan.** `grep -rniE "TBD|TODO|FIXME|XXX|handle (this|edge cases)|implement appropriately|similar to" internal/rehydrate internal/rules internal/skills internal/daemon/rehydrate_service.go test/e2e/sessionstart_compact_test.go test/replay/policy_rehydrate.go test/replay/phase3_rehydrate_test.go docs/adr/0011-*.md` returns nothing.
- [ ] **No stub residue.** `grep -rn "ErrNotImplemented" internal/rehydrate internal/rules internal/skills` returns hits **only** in the three `<pkg>test` conformance suites, which legitimately keep `core.IsNotImplemented` as their stub probe and `t.Skip` as its guarded consequence. The correct check that the suites actually ran is the `grep "Rule W-1"` on verbose test output listed under Local criteria — a grep for `t.Skip` under `internal/rehydratetest` would match nothing regardless, because the package is at `internal/rehydrate/rehydratetest`.
- [ ] **Type consistency with the Interface contract.** `Build`, `StandingInstruction`, `Scanner.PathScoped`, `Scanner.NestedClaudeMD`, `Indexer.Index`, `Item`, `ItemKind`, `Request`, `Result`, `Deps`, `Rule`, `Entry` match 00-ARCHITECTURE §5.15 **field-for-field** — in particular `Deps` gains no `Clock` and no `Metrics` member, and `Request`/`Result` are unchanged. The one deviation is deliberate, authorized and already landed: step 0's §0 amendment inserts `ItemRestoredInstructions` and `ItemSkillIndex` into the `ItemKind` block at their rendered positions, so 00-ARCHITECTURE §5.15 and `internal/rehydrate/types.go` declare the same ten constants in the same order. Everything else added is a package-level helper in a package this subplan owns — `AffordanceNotice`, `Wrap`, `Unwrap`, `Match`, `Option`, `WithLogger`, `BodyTokens`, `NewReporter`, `Reporter`, `State`, `ItemStat`, `BindRehydrate` — none of which removes or alters anything §5 declares. `rules.New` and `skills.New` keep their zero-argument call form.
- [ ] `var _ mcp.DropReporter = rehydrate.NewReporter(...)` compiles in `test/e2e` — SP-13's `dropped` tool has a real backing implementation.
- [ ] `var _ observer.Rehydrator = NewRehydrateService(...)` compiles in `internal/daemon`.
- [ ] No file under `internal/observer` was modified on this branch: `git diff --name-only develop..HEAD | grep '^internal/observer/'` returns nothing.
- [ ] `Qompack.md` is unmodified: `git diff --name-only develop..HEAD | grep '^Qompack.md$'` returns nothing.
- [ ] `daemon.Services` gained **no** member: `git diff develop..HEAD -- internal/daemon/options.go` is empty. The only change in an SP-05/SP-08-owned daemon line is the observer construction site's field assignment, which is additive and nil-tolerant; `Services.Rehydrate` is set through `Bind`, from this branch's own file.
- [ ] The three conformance suites are untouched on the feature branch: `git diff --name-only develop..HEAD | grep -E 'rehydratetest|rulestest|skillstest'` returns nothing. (Step 0's one-line message change to `rehydratetest/behaviour.go` is already in `develop`.)
- [ ] Golden payloads were read line by line by a human/agent, not merely regenerated with `-update`.
- [ ] **Commit count verified 5–8:** `git rev-list --count develop..HEAD` returns **7** — step 0 is not counted, because it is already in `develop`.
- [ ] **No co-author trailers:** `git log --format=%B develop..HEAD | grep -Ei "Co-Authored-By|Signed-off-by|Generated with|🤖"` returns nothing.
- [ ] `devtool ci-local` green; the branch is ready to merge into `develop` in the wave-3 merge order.
