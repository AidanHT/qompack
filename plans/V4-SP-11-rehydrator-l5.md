# SP-11: L5 rehydrator: SessionStart compact branching, the eight-item importance-ordered injection, instruction restoration, skill index, drop report, and the 8-12K budget

> **Recommended model: Opus 5 · xhigh effort**
>
> The eight-item ordering, `**`-aware glob matching, frontmatter scanning and the 8–12K budget fill are fiddly but entirely decided in the plan — including the rendered output verbatim. Execution fidelity, not invention.

**Branch:** `feat/sp11-rehydrator-l5` (cut from `develop`) | **Wave:** 3 | **Prerequisites:** the branches of `["SP-01","SP-06","SP-07","SP-09"]` already merged into `develop` | **Runs in parallel with:** sibling subplans of wave 3 (SP-10 checkpointer, SP-12 scheduler, SP-13 MCP retrieval) | **Design sections:** §7.2 L5, §7.3 (SessionStart source=compact), §8.6, §8.7 design note (standing instruction), §10 Phase 3 (exit criterion), §12 (rehydration budget cap) | **Gaps closed:** G3.3, G4.1, G4.2, G4.4, G4.5, G7.3, G7.5

---

## Mission

This slice is layer **L5 — the rehydrator**. It is the read end of Qompack: everything L0/L1/L2/L4 wrote to `.qompack/` is worthless to the agent unless something puts the right 8–12K tokens back into the context window at the moment Claude Code's compaction has just destroyed it. `Qompack.md` §8.6 specifies exactly what that payload contains and in exactly what order; this subplan implements that specification and nothing else.

The reason it exists is `Qompack.md` §3 G3.3: *"Eager restoration is the inverse of retrieval. Re-injecting the top 5 files against a 50K budget is a guess. A lazy affordance costs ~200 tokens and is correct rather than probabilistically correct."* Stock Claude Code spends 50K on five file bodies plus 25K on skill bodies (§2.4 step 7) and still loses every `paths:`-scoped rule (G4.1), every nested `CLAUDE.md` (G4.2), the entire skill index (G4.4), and never tells the agent that any of it went missing (G4.5). SP-11 replaces 75K of eager guessing with 8–12K of pointers, verbatim non-reconstructible facts, restored instructions, and an explicit drop report — and closes G7.3 permanently by injecting the original user intent from the L0 verbatim capture rather than from any summary, so PTL retry dropping the oldest API rounds can no longer delete the task statement. G7.5 is closed the same way: when the summarization call fails by calling a tool instead of summarizing, the checkpoint-derived injection is the fallback path and the session survives.

**What exists in the repo when you start.** SP-01 (wave 0) has shipped the whole module skeleton: `internal/core`, `internal/paths`, `internal/config` (including the §11.5 `runtime.rehydrate` namespace with `minTokens`/`maxTokens`/`skillIndexTokens`/`eliminationsTopN`), `internal/logging`, `internal/obs`, `internal/tokens`, `internal/hookio`, `internal/testutil`, the `test/e2e` harness, the `nomagic` lint pass, the import-graph DAG check, CI, and **compiling `ErrNotImplemented` stubs plus `<pkg>test` conformance suites for every interface in §5** — including `internal/rehydrate`, `internal/rules`, `internal/skills` and `rehydratetest`. SP-06 (wave 1) has shipped a real `store.Store` with `Search`, `Open`, `OpenSpan`, `FileHistory`, `Segments()` and exact chunk-level token accounting. SP-07 (wave 1) has shipped a real `dag.Graph` with `BackwardSlice` returning per-node relevance scores. SP-09 (wave 2) has shipped a real `negknow.Ledger` with the three-way `Answer` and `Active(scope)`. SP-08 (wave 2, on `develop` by construction because wave 3 is cut from post-V3 `develop`) has shipped `internal/observer`, including the `SessionStart` `source` switch that delegates the `compact` and `clear` branches outward per §5.21.

**What exists when you finish.** `rehydrate.Build` produces the complete §8.6 payload with per-item token accounting and truncation flags, wrapped in the §8.5 injection tags carrying the checkpoint sequence number. `rules.Scanner` re-reads every `paths:`-scoped rule whose glob matches a pointer-set file and every nested `CLAUDE.md` in a directory containing one. `skills.Indexer` builds the compact skill index against `runtime.rehydrate.skillIndexTokens` — never a package constant, because §11.6 forbids the literal `450` outside `internal/config/defaults.go`. A `rehydrate.Reporter` persists the drop report to `.qompack/state/rehydrate-<session>.json` and structurally satisfies `mcp.DropReporter`, which is the interface SP-13's `dropped` tool is built against. A single new file in the `internal/daemon` composition root implements the `SessionStart` `compact` and `clear` branches. And `test/replay/phase3_rehydrate_test.go` asserts the Phase 3 exit criterion of §10 against SP-02's Phase 0 baseline. Phase 3 closes here.

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
    MinTokens        core.Tokens `json:"minTokens"`        // default 8000
    MaxTokens        core.Tokens `json:"maxTokens"`        // default 12000
    SkillIndexTokens core.Tokens `json:"skillIndexTokens"` // default 450
    EliminationsTopN int         `json:"eliminationsTopN"` // default 8
}
```

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
type Query struct{ Text, Path, Symbol, Tool string; Since time.Time; K int }
type Hit struct {
    Root core.Hash; ToolUseID core.ToolUseID; Path string; Tool string
    TS core.UnixMilli; Score float64; Summary string; Span [2]int64
}
Search(ctx context.Context, q Query) ([]Hit, error)
Open(ctx context.Context, root core.Hash) (io.ReadCloser, error)
ToolUse(ctx context.Context, id core.ToolUseID) (ToolUseRecord, error)
Has(h core.Hash) bool
```

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
type SessionStartRehydrator interface {
    Compact(ctx context.Context, e hookio.Event) (hookio.Output, error)
    Clear(ctx context.Context, e hookio.Event) error
}
```

If the seam that landed on `develop` differs in name or signature, resolve it by the §0 amendment procedure — open `arch/sessionstart-rehydrator-seam` off `develop`, change 00-ARCHITECTURE §5.21, merge, rebase. **Never** by editing `internal/observer` from this branch and never by working around it.

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
    ItemDropReport                   // §8.6 item 7
    ItemAffordance                   // §8.6 item 8
    // Additive kinds for the §8.6 "Instruction restoration" clause. They are rendered between
    // ItemPointers and ItemDropReport; the eight normative kinds keep their relative order.
    ItemRestoredInstructions
    ItemSkillIndex
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
func StandingInstruction() string          // "Before committing to an approach, call already_tried."
func AffordanceNotice() string             // §8.6 item 8, one line naming the eight tools
func Sentinel(s core.SessionID, seq core.CheckpointSeq) string  // §12.1 CAdditionalContext token
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
    Sentinel  string             `json:"sentinel"`
}
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
func New(log logging.Logger) Scanner
func Match(pattern, key string) bool   // "**"-aware glob, exported for tests and SP-14
```

`internal/skills`:

```go
type Entry struct{ Name, Description, Source string }
type Indexer interface {
    Index(ctx context.Context, root string, budget core.Tokens) ([]Entry, core.Tokens, error)
}
func New(log logging.Logger) Indexer
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
// NewRehydrateService returns an observer.SessionStartRehydrator. It is installed into the
// late-bound Services set (§5.4) and is nil-tolerant at every call site.
func NewRehydrateService(o RehydrateOptions) observer.SessionStartRehydrator
```

**Consumers.** SP-13 constructs `mcp.ToolDeps{Rehydrator: <rehydrate.Reporter>}` for the `dropped` tool. SP-14's `/qompack:status` reads `.qompack/state/rehydrate-<session>.json` for the "last rehydration" panel. SP-16 reads `State.Items` to tune the tier boundaries from replay evidence.

**The SP-05 sentinel handshake, spelled out (§12.1 `hook.additional_context_delivered`).** SP-05's assertion runs inside `contract.Env`, which carries `ProjectRoot`, `Event` and `Store` but no checkpoint sequence number. It therefore obtains the expected token in this order, and this subplan guarantees both halves:

1. **Primary — read it.** `.qompack/state/rehydrate-<sanitized-session>.json` carries `"sentinel"` as a top-level string. The state file is written by `Reporter.Record` *before* `Compact` returns its `hookio.Output`, so by the time any later `UserPromptSubmit` fires, the file exists. SP-05 needs no `rehydrate` import: it reads one JSON key.
2. **Cross-check — recompute it.** Given `(session, seq)` — `seq` also being a field of the same state file — the token is reproducible by the documented algorithm in `internal/rehydrate/sentinel.go` below. The domain string, the input encoding and the prefix are all fixed by this subplan and are golden-tested, so a divergence between the two halves is a test failure here, not a mystery in SP-05.
3. **Absent state file ⇒ assertion is not evaluated**, never failed: there has been no `SessionStart(source=compact)` in this session yet, so there is nothing to have been delivered. §12.1's "producer absent ⇒ `OK: true, SevInfo`" rule covers it.

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
| `internal/rehydrate/rehydrate.go` | modify (replace SP-01 stub body) | `Build`, `Deps` wiring, degradation handling |
| `internal/rehydrate/items.go` | create | the ten item builders, each returning ordered units |
| `internal/rehydrate/budget.go` | create | clamp, reserves, share allocation, carry-forward, min-fill pass |
| `internal/rehydrate/render.go` | create | payload rendering, `Wrap`/`Unwrap`, `StandingInstruction`, `AffordanceNotice` |
| `internal/rehydrate/sentinel.go` | create | `Sentinel` |
| `internal/rehydrate/drops.go` | create | `Reporter`, `State`, `ItemStat`, state-file I/O |
| `internal/rehydrate/*_test.go`, `fake_test.go`, `bench_test.go` | create | tests, fakes, benchmarks |
| `internal/rehydratetest/suite.go` | modify | remove W-1 `t.Skip` calls; add behaviour assertions |
| `internal/daemon/rehydrate_service.go` | create | SessionStart `compact` + `clear` branches |
| `internal/daemon/services.go` (SP-05's `Services` declaration site) | modify | add one member: `Rehydrator observer.SessionStartRehydrator` |
| `internal/daemon/` observer construction site (SP-05/SP-08 wiring) | modify | pass `Services.Rehydrator` into the observer's deps |
| `test/e2e/sessionstart_compact_test.go` | create | real binary + real daemon, `source=compact` and `source=clear` |
| `test/replay/phase3_rehydrate_test.go` | create | Phase 3 exit-criterion assertions |
| `testdata/fixtures/rules/proj-a/**` | create | fixture tree: `paths:` rules, nested `CLAUDE.md`, skills |
| `testdata/fixtures/rules/proj-edge/**` | create | edge-case tree: CRLF, unicode, spaces, unreadable, deep nesting |
| `testdata/golden/rehydrate/*.txt` | create | golden payloads |
| `docs/adr/0011-rehydration-budget-and-item-order.md` | create | ADR |

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
func New(log logging.Logger) Scanner { if log == nil { log = logging.Nop() }; return &scanner{log: log} }
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

§8.6 says: *"Every nested `CLAUDE.md` in a directory containing a pointer-set file."* Implement that literally.

1. Normalize pointers as above.
2. For each pointer key `k`, take `d := path.Dir(k)`. If `d == "."` skip it — the project-root `CLAUDE.md` is re-injected by Claude Code itself (§2.7) and duplicating it wastes budget.
3. Candidate `c := path.Join(d, "CLAUDE.md")`. Stat `filepath.Join(root, filepath.FromSlash(c))`; if it exists and is a regular file ≤ `maxRuleBytes`, read it.
4. **Do not walk ancestors.** A `CLAUDE.md` two directories above a pointer is not returned. A test asserts this, because it is the literal reading of §8.6 and diverging from it silently is exactly the kind of drift the design document exists to prevent.
5. Emit `Rule{Path: c, Globs: nil, Body: <whole file, CRLF→LF, right-trimmed; frontmatter, if any, retained>, Tokens: baseline, Nested: true}`.
6. Deduplicate by `Path`, sort ascending, return.

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

### `internal/rehydrate/sentinel.go`

```go
// sentinelDomain is shared, by value, with SP-05's CAdditionalContext assertion (§12.1). Both
// sides recompute the token from (session, seq); no import edge and no shared state is required.
const sentinelDomain = "qompack.sentinel.v1"

// Sentinel returns the token emitted as the last line inside the injection tags. SP-05's
// assertion searches the transcript tail for it after a SessionStart(source=compact).
//   input encoding: string(s) + "|" + strconv.Itoa(int(seq))
//   token:          "qpk-ac-" + core.HashBytes(sentinelDomain, input).Short()
func Sentinel(s core.SessionID, seq core.CheckpointSeq) string {
    in := string(s) + "|" + strconv.Itoa(int(seq))
    return "qpk-ac-" + core.HashBytes(sentinelDomain, []byte(in)).Short()
}
```

`Short()` is 12 hex chars, so the token is 19 characters and costs ~8 tokens. It is rendered as `<!-- qompack:sentinel qpk-ac-3f2a1b4c5d6e -->`.

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

func StandingInstruction() string {
    // §8.7 design note, verbatim.
    return "Before committing to an approach, call already_tried."
}

func AffordanceNotice() string {
    // §8.6 item 8: "one line telling the agent that recall, re_read, and already_tried exist".
    return "Qompack retrieval is available: recall(query,k) · expand(hash|tool_use_id) · " +
        "re_read(path,at) · already_tried(target,approach) · record_eliminated(target,approach,reason) · " +
        "timeline(from,to) · why(decision_id) · dropped(). " + StandingInstruction()
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
Qompack retrieval is available: recall(query,k) · expand(hash|tool_use_id) · re_read(path,at) · already_tried(target,approach) · record_eliminated(target,approach,reason) · timeline(from,to) · why(decision_id) · dropped(). Before committing to an approach, call already_tried.
<!-- qompack:sentinel qpk-ac-3f2a1b4c5d6e -->
<!-- /qompack:injected -->
```

`renderOrder` is a package `var` and the source of `Item.Rank` (1-based index among **emitted** items):

```go
var renderOrder = []ItemKind{
    ItemInvariants, ItemUserIntent, ItemEliminations, ItemDecisions, ItemCurrentWork,
    ItemPointers, ItemRestoredInstructions, ItemSkillIndex, ItemDropReport, ItemAffordance,
}
```

A test asserts that the eight normative kinds appear in `renderOrder` in exactly their `iota` order, so the §8.6 importance ordering cannot drift.

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
// intentFromL0 resolves THIS SESSION'S first verbatim user prompt from the L0 capture.
// ToolUserPrompt is the reserved tool name SP-08 records UserPromptSubmit captures under.
const ToolUserPrompt = "UserPromptSubmit"
const maxIntentBytes  = 8192
const intentSearchK   = 512
```

**Verify the constant before implementing.** `ToolUserPrompt` is a convention SP-08 (wave 2) established and 00-ARCHITECTURE does not name. On the `develop` this branch is cut from, confirm the spelling by grepping `internal/observer` for the `store.ToolUseRecord.Tool` value written on the `UserPromptSubmit` path. If it differs, **use SP-08's spelling** and open `arch/l0-prompt-capture-tool-name` off `develop` to record the constant in 00-ARCHITECTURE §5.21 so the next reader does not have to grep. Do **not** read `Event.TranscriptPath` as a substitute: the transcript is exactly the summary-contaminated surface §8.5's regeneration rule forbids as a source.

Algorithm:
1. `hits, err := d.Store.Search(ctx, store.Query{Tool: ToolUserPrompt, K: intentSearchK})`.
2. On error or `len(hits)==0`: fall back to `Checkpoint.UserIntent.Original`, append `DropEntry{Kind:"user_intent_source", ID:"l0", Detail:"L0 verbatim capture unavailable; using the checkpoint copy, which §8.5 guarantees is itself verbatim-from-L0 and never regenerated"}`, and log at Info (not Loud — this is a degraded-but-correct path, not a contract violation).
3. **Filter to this session, then take the earliest turn.** `store.Hit` carries neither a session id nor a turn index, and `store.Query` has no session filter — the store is project-wide and long-lived, so the score-ranked hit set legitimately contains prompts from *previous* sessions. Resolve each hit through the tool-use index and discard everything that is not this session:
   ```go
   type cand struct{ rec store.ToolUseRecord }
   var cands []cand
   for _, h := range hits {
       rec, err := d.Store.ToolUse(ctx, h.ToolUseID)
       if err != nil || rec.Session != r.Session { continue }   // ErrNotFound is expected and silent
       cands = append(cands, cand{rec})
   }
   ```
   Sort `cands` ascending by `(rec.Turn, rec.TS, rec.ID)` — `Turn` first because it is the authoritative ordering of a session and is immune to clock skew — and take the first. If `cands` is empty after filtering, take the step-2 fallback path. Then `rc, err := d.Store.Open(ctx, cands[0].rec.Root)`; read at most `maxIntentBytes`; `text := strings.TrimSpace(checkpoint.StripInjections(string(b)))`.
   `intentSearchK = 512` is a ceiling, not a guarantee: if a project has more than 512 stored prompts the earliest one for *this* session may fall outside the window, in which case the filter empties and step 2's checkpoint copy — itself verbatim-from-L0 per §8.5 — is used. That is a correct degradation, not a silent one: the `user_intent_source` drop entry says so.
4. **Cross-check.** If `Checkpoint.UserIntent.Original != ""` and its `strings.TrimSpace` form differs from `text`, **L0 wins**, and emit `DropEntry{Kind:"intent_mismatch", ID:string(r.Session), Detail:"checkpoint user_intent.original differs from the L0 capture; injecting the L0 text"}` plus `d.Log.Loud(...)`. This is the mechanical guarantee that no summary-derived intent can reach the context window.
5. Render: `"> " + <each line of text, prefixed>` then, when `Checkpoint.UserIntent.Evolution` is non-empty, `"Evolution:\n"` followed by one `"> "`-prefixed unit per evolution entry.
6. The original-intent unit is **never truncated**. Evolution units are truncatable and each carries `DropEntry{Kind:"user_intent_evolution", ID:strconv.Itoa(i)}`.

`StripInjections` is applied because a prior rehydration's payload could, in a pathological transcript, have been captured as a prompt; §8.5's tagging exists precisely so this material is identifiable and ignorable.

**Item 3 — eliminated-approaches digest (`buildEliminations`).**

1. Candidate set: the union of **both** scopes — `d.Ledger.Active(ctx, negknow.Scope("session"))` and `d.Ledger.Active(ctx, negknow.Scope("project"))` — further unioned with `Checkpoint.Eliminated`, deduplicated by `Record.ID` (ledger copy wins — it carries current `Status`). If either `Active` call errors, use whatever the other returned; if both error, fall back to `Checkpoint.Eliminated` alone. On any ledger error emit `DropEntry{Kind:"elimination_source", ID:"ledger", Detail:err.Error()}`.
   **Both scopes, deliberately.** `eliminations.defaultScope` (Appendix C default `"session"`) governs the scope *new* records are written with; it is not a read filter. §8.3 says project-scoped eliminations "persist and warm-start future sessions", so a rehydration that consulted only the default scope would drop exactly the longest-lived negative knowledge in the store — the inverse of G6.2. A test asserts a project-scoped record appears when `defaultScope == "session"`.
2. **Stale records are included, not filtered**, when `r.Cfg.Eliminations.StaleResponse == "flag"` (the Appendix C default) and excluded when it is `"drop"`. A stale record renders the §8.3 note verbatim: `previously eliminated, but the evidence has changed since — re-verification may be warranted`.
3. **Slice scoring.** Criteria node set, in this order, deduplicated:
   - `dag.NodeID("file:" + paths.Key(p.Path))` for every `Checkpoint.Pointers.Files` entry
   - `dag.NodeID("tooluse:" + string(t.ToolUseID))` for every `Checkpoint.Pointers.Tools` entry
   - `dag.NodeID("decision:" + string(dec.ID))` for every `Checkpoint.Decisions` entry

   **Verify the `<kind>:` prefixes before implementing.** 00-ARCHITECTURE §5.9 fixes the *shape* `"<kind>:<stable-key>"` but not the prefix spellings, which are SP-07's (wave 1, merged). Read the `NodeID` constructors in `internal/dag` on the `develop` this branch is cut from and use SP-07's exact spellings — if SP-07 writes `tool_use:` rather than `tooluse:`, this file is wrong and SP-07 is right. Then open `arch/dag-nodeid-prefixes` off `develop` to record the six prefixes this slice depends on (`file:`, `symbol:`, `tooluse:`, `decision:`, `elimination:`, `segment:`) in §5.9, so the next consumer does not have to grep. A silently mis-spelled prefix does not fail — it returns a zero score for every record, degrading item 3's ordering to TS/ID without any signal, which is precisely the kind of drift §0 exists to stop. `TestEliminations_OrderedBySliceScore` is the regression guard and must use the same constructor the production code uses, never a hand-written string literal.
   Call `d.Graph.BackwardSlice(criteria, dag.SliceOptions{Thin: r.Cfg.Selection.Slicing == "thin", MaxNodes: sliceMaxNodes, Decay: sliceDecay, Deadline: sliceDeadline})` with `sliceMaxNodes = 5000`, `sliceDecay = 0.85`, `sliceDeadline = 50 * time.Millisecond`. On error or nil `Graph`, all scores are 0.
   Per-record score:
   ```go
   func recordScore(sc map[dag.NodeID]float32, rec negknow.Record) float32 {
       s := sc[dag.NodeID("elimination:"+rec.ID)]
       if v := sc[dag.NodeID("file:"+paths.Key(rec.Desc.NormalizedPath))]; rec.Desc.NormalizedPath != "" && v*symbolProxyDecay > s {
           s = v * symbolProxyDecay
       }
       if v := sc[dag.NodeID("symbol:"+rec.Desc.Symbol)]; rec.Desc.Symbol != "" && v*symbolProxyDecay > s {
           s = v * symbolProxyDecay
       }
       return s
   }
   const symbolProxyDecay float32 = 0.75
   ```
4. Sort descending by score; tiebreak `TS` descending; tiebreak `ID` ascending. This total order is deterministic and is asserted by a property test.
5. Take the first `r.Cfg.Runtime.Rehydrate.EliminationsTopN` records as units. Render each as
   `"- " + target + " — \"" + approach + "\" — " + reason + " [" + statusTag + "]" + evidenceSuffix + "\n"`
   where `statusTag` is `active` or `stale: previously eliminated, but the evidence has changed since — re-verification may be warranted`, and `evidenceSuffix` is `" [evidence " + Evidence.String() + "]"` when `Evidence != core.Hash{}`, else empty. `reason` is collapsed to a single line (`\n` → `" "`) and truncated to 240 runes with `…`.
6. A **trailing note unit** is always appended when `seen > len(units)`:
   `strconv.Itoa(seen-len(units)) + " further eliminations are recorded and not shown. " + StandingInstruction() + "\n"`.
   When `seen == len(units)` but `seen > 0`, the note is just `StandingInstruction() + "\n"`. When `seen == 0` the item is omitted entirely **except** for the standing instruction, which §8.7 requires unconditionally — it is emitted as part of item 8 in that case (`AffordanceNotice` already ends with it), so nothing is lost.
   Whenever `seen > len(units)`, the builder **also** contributes one entry to the drop set — this is what puts the `elimination` line into section 7, and without it the drop report would under-report the single largest omission in the payload:
   `DropEntry{Kind:"elimination", ID:"", Detail: itoa(seen-len(units)) + " of " + itoa(seen) + " not shown; call already_tried(target, approach) or dropped()"}`.
   Its empty `ID` is what makes item 7's same-`Kind`-empty-`ID` collapse rule render it as a single counted line.
7. The note unit is **never truncated** (it is what makes the digest honest) and is accounted before the record units when reserving.

**Item 4 — decisions (`buildDecisions`).** One unit per `Checkpoint.Decisions` entry, ordered by slice score descending (`dag.NodeID("decision:"+ID)`), tiebreak `Turn` descending, tiebreak `ID` ascending. Render:

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
2. `kept, used, err := d.Skills.Index(ctx, r.ProjectRoot, skillBudget)` where `skillBudget` comes from `r.Cfg.Runtime.Rehydrate.SkillIndexTokens` (never a literal — §11.6).
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

```go
// clampBudget resolves the effective budget B (§8.6 "target 8–12K", §11.5 min/max).
func clampBudget(req core.Tokens, cfg config.RehydrateCfg) core.Tokens {
    maxT, minT := cfg.MaxTokens, cfg.MinTokens
    if minT > maxT { minT = maxT }          // corrected config, never a crash (§11.3)
    b := req
    if b <= 0 { b = maxT }
    if b > maxT { b = maxT }
    if b < minT { b = minT }
    return b
}
```

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
2. Build the never-truncated items first: 1 (invariants), 2's original-intent unit, 8 (affordance). `fixed := sum(tokens)`. These are emitted whole even if `fixed > B`; in that case `Result.Degraded = true`, `d.Log.Loud("rehydration tier-1 material exceeds the hard budget cap", "fixed", fixed, "cap", B)`, and every discretionary item is skipped with its units converted to drop entries. §8.5 puts invariants and intent in the never-truncated tier and §8.6 says "verbatim, always"; the hard cap binds everything else.
3. `reserveDrop := B / dropReportReserveDiv`; `reserveSkill := min(cfg.SkillIndexTokens, B/dropReportReserveDiv)`.
4. `avail := B - fixed - reserveDrop - reserveSkill`; if negative, `avail = 0`.
5. Per-item share, computed once: `share(pct) = avail * pct / 100`, for each of the six share-taking items (intent evolution, eliminations, decisions, current work, pointers, restored instructions). Any remainder from integer division is added to the *last* share-taking item in `renderOrder` (restored instructions) so the arithmetic is lossless and deterministic.
6. Fill in `renderOrder`, carrying forward. `ItemUserIntent`'s turn fills only its *evolution* units — its original-intent unit was already emitted whole in step 2 and is not re-charged here. For each share-taking item, `allowance := share + carry`; admit units in their builder order while `used + unit.tokens <= allowance`; the first unit that does not fit ends the item (**prefix truncation**, matching §6.9's embedded-coding principle — never skip ahead to a smaller unit, because reproducibility across budgets is the entire point of importance ordering). Set `Item.Truncated = true` and emit `unit.drop` for every unadmitted unit. `carry = allowance - used`.
7. Build item 6b against `reserveSkill`, item 7 against `reserveDrop + carry`.
8. **Min-fill pass.** If `Result.Tokens < cfg.MinTokens` and at least one item is `Truncated`, compute `slack := min(cfg.MinTokens, cfg.MaxTokens) - Result.Tokens` and walk the truncated items in `renderOrder`, re-admitting previously dropped units while `slack >= unit.tokens`, removing their drop entries. This is what makes `minTokens` a *fill target* rather than dead config: the design says "target 8–12K", and a payload that stops at 3K while 9K of ranked material was available and the cap was 12K is leaving quality on the table.
9. **Hard cap assertion.** After the min-fill pass, `Result.Tokens <= cfg.MaxTokens` must hold unless `Degraded` is set by step 2. A `panic`-free `if` re-truncates the last discretionary item and logs `Loud` if it does not — the assertion is also a unit test and a property test.

Token accounting for the wrapper: `Result.Tokens` includes the header line, the injection tags, the sentinel line, and every section heading. They are estimated exactly like item text with `d.Tokens.EstimateString(..., tokens.ClassProse)` and attributed to a synthetic zero-`Kind` overhead figure recorded in `State` as `ItemStat{Kind:"overhead"}`.

### `internal/rehydrate/rehydrate.go` — `Build`

```go
func Build(ctx context.Context, r Request, d Deps) (Result, error)
```

1. Normalize `Deps`: nil `Log` → `logging.Nop()`; nil `Tokens` → a baseline estimator closure `func(s string) core.Tokens { return core.Tokens((len(s)+3)/4) }` used everywhere the estimator would be; nil `Rules`/`Skills`/`Ledger`/`Graph`/`Store` → that item's source is skipped and a `DropEntry{Kind:..., ID:"unavailable"}` is recorded. **`Build` never returns a non-nil error for a missing dependency** — it degrades and reports. There is no clock to normalize: `Build` reads none.
2. `if r.Source != "compact"` → return `Result{}, nil` with no items. The `clear`/`startup`/`resume` branches never call `Build`; this is belt-and-braces so a mis-wired caller cannot inject into a fresh session.
3. Run the builders, run the budget pass, then render the **body**: header line, then each emitted item's text in `renderOrder`, then — as the body's final line — `"<!-- qompack:sentinel " + Sentinel(r.Session, r.Ref.Seq) + " -->"`. Only then call `Wrap(r.Ref.Seq, body)`, which adds the open and close tags around it. The sentinel is *inside* the body because `Wrap` is a pure two-tag wrapper and because §12.1's assertion searches the delivered `additionalContext`, all of which is wrapped.
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
    { "kind": "overhead", "rank": 0, "tokens": 96, "truncated": false, "units": 1, "units_seen": 1 },
    { "kind": "invariants", "rank": 1, "tokens": 212, "truncated": false, "units": 4, "units_seen": 4 },
    { "kind": "eliminations", "rank": 3, "tokens": 2903, "truncated": true, "units": 8, "units_seen": 23 }
  ],
  "dropped": [
    { "kind": "path_rule", "id": ".claude/rules/db-conventions.md", "detail": "matched src/db/pool.ts; did not fit the rehydration budget" }
  ],
  "degraded": false,
  "sentinel": "qpk-ac-3f2a1b4c5d6e"
}
```

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

func NewRehydrateService(o RehydrateOptions) observer.SessionStartRehydrator
```

**`Compact(ctx, e hookio.Event) (hookio.Output, error)`**

1. `defer func(){ if v := recover(); v != nil { s.log.Loud("rehydrate panic", "err", v); out = hookio.Empty() } }()` — §13 invariant 6: hooks exit 0, always.
2. **Degradation gate.** `if s.mon != nil && s.mon.Mode() != contract.ModeFull { return hookio.Empty(), nil }`. §12.1 is explicit: in `ModeDegradedPassive` there is "no `additionalContext` injection … no drop report". Getting this wrong would make the degradation doctrine a lie. A unit test asserts an empty output in degraded mode.
3. `cp, ref, err := s.ck.Latest(ctx, e.SessionID)`. On `core.ErrNotFound`: build with a zero `Checkpoint` and `Ref{Seq:0}` — items 1, 2 (from L0), 3 (from the ledger), 6a (from an empty pointer set → nested scan yields nothing, path-rule scan yields nothing), 6b and 8 still produce a useful payload, and `Result.Degraded` is set. On any other error: `Loud`, return `hookio.Empty()`.
4. `budget := s.cfg.Runtime.Rehydrate.MaxTokens`.
5. `res, err := rehydrate.Build(ctx, rehydrate.Request{Session: e.SessionID, Source: e.Source, ProjectRoot: s.root, Budget: budget, Checkpoint: cp, Ref: ref, Cfg: s.cfg}, s.deps)`, timed into `s.m.Hist("rehydrate.build")`.
6. `s.rep.Record(ctx, e.SessionID, s.stateFrom(e.SessionID, res))` — errors logged at Warn, never propagated. `stateFrom` is the service's method, not a package function, because it is the one place a wall clock enters this slice:
   ```go
   func (s *rehydrateService) stateFrom(sess core.SessionID, res rehydrate.Result) rehydrate.State {
       return rehydrate.State{
           Session:  sess, Seq: res.Seq,                          // Result carries Seq, not Session
           Emitted:  core.UnixMilli(s.clock.Now().UnixMilli()),   // the ONLY clock read in L5
           Tokens:   res.Tokens, Budget: s.cfg.Runtime.Rehydrate.MaxTokens,
           Items:    itemStats(res.Items),                        // incl. the "overhead" row
           Dropped:  res.Dropped, Degraded: res.Degraded,
           Sentinel: rehydrate.Sentinel(sess, res.Seq),
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

**`Clear(ctx, e hookio.Event) error`**

1. `s.rep.Reset(ctx, e.SessionID)` — deletes `state/rehydrate-<session>.json` so the next `compact` injection is a fresh full payload with no stale drop report.
2. Log at Info: `"session cleared; rehydration state reset"`, with `session` and the last `seq`.
3. **Do not** touch the ledger, the store, the DAG, sketches, or checkpoints. A `/clear` is not a session end; session-scoped eliminations "die with the session" (§8.3) and the session has not ended. SP-08 owns session-registry effects of `clear`.
4. Return `nil` always. Errors are logged, never propagated.

**Wiring (the only edits to SP-05/SP-08-owned lines).**

- In the file declaring `daemon.Services`, add exactly one member: `Rehydrator observer.SessionStartRehydrator`.
- At the observer construction site, pass `svc.Rehydrator` into the observer's deps. Both edits are additive, nil-tolerant, and are what §5.4's "late-bound Services set" exists for.
- In the daemon's startup sequence, after `store`, `negknow`, `dag` and `checkpoint.Reader` are constructed, set `svc.Rehydrator = NewRehydrateService(RehydrateOptions{...})`.

### Error handling matrix

| Failure | Response |
|---|---|
| `contract.Mode != ModeFull` | no injection, no drop report, `hookio.Empty()`, exit 0 (§12.1) |
| `checkpoint.Reader.Latest` returns `ErrNotFound` | build from L0 + ledger + pins with `Ref{Seq:0}`; `Degraded=true`; Info log |
| `checkpoint.Reader.Latest` returns any other error | `Loud`, `hookio.Empty()` |
| MANIFEST mismatch surfaced by the Reader | Reader's problem (§12.3 falls back to the parent and degrades); this service sees either a checkpoint or an error |
| `store.Search`/`Open` fails for the L0 intent | checkpoint copy of `user_intent.original`; `DropEntry{Kind:"user_intent_source"}`; Info log |
| L0 intent ≠ checkpoint intent | **L0 wins**; `DropEntry{Kind:"intent_mismatch"}`; `Loud` |
| `negknow.Ledger` error | checkpoint `eliminated[]` only; `DropEntry{Kind:"elimination_source"}`; Warn |
| `dag.BackwardSlice` error, deadline, or nil graph | all slice scores 0; ordering falls back to TS/ID; Debug log; no drop entry |
| `rules.PathScoped`/`NestedClaudeMD` walk error | that half yields nothing; `DropEntry{Kind:"path_rule"\|"nested_claude_md", ID:"scan"}`; Warn |
| individual rule/skill file unreadable | skipped, walk continues; Debug log in `rules`/`skills`; no counter (§5.15's `Scanner`/`Indexer` carry no `obs` seam) |
| `skills.Index` error | no skill index; `DropEntry{Kind:"skill", ID:"scan"}`; Warn |
| nil `tokens.Estimator` | baseline `(len+3)/4` everywhere; Warn once |
| tier-1 material exceeds the cap | emit it anyway, drop all discretionary items, `Degraded=true`, `Loud` |
| `Result.Tokens > MaxTokens` after min-fill | re-truncate the last discretionary item; `Loud`; assertion also covered by a property test |
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
| Payload size | `Result.Tokens` | **≤ `runtime.rehydrate.maxTokens`** (12 000) except tier-1 overflow | property test + replay gate |

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

`testdata/golden/contracts/checkpoint/` — SP-01-generated (Rule W-2). This branch adds none; it reads:

- `full.json` — all tiers populated: 23 eliminations, 9 decisions, 12 file pointers, 3 tool pointers, 4 invariants, `user_intent.original` plus 20 evolution entries, `seq: 7`.
- `minimal.json` — **`invariants` and `user_intent.original` only**. Note this is *narrower* than §8.5's "tier 1", which also contains `eliminated`: the fixture deliberately empties `eliminated`, `decisions`, `current_work` and `pointers` so that items 3, 4, 5, 6 and 6a are all empty and the rank arithmetic can be asserted in isolation. Tests using it run against an **empty project root** (no `.claude/` tree), so item 6b is empty too.
- `empty.json` — every field zero-valued; drives `TestBuild_NilDeps` and the degraded golden.

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
| `TestNestedClaudeMD_ContainingDirOnly` | `proj-a` | `["src/api/routes.ts"]` | exactly `[src/api/CLAUDE.md]` |
| `TestNestedClaudeMD_ExcludesProjectRoot` | `proj-a` | `["README.md"]` (root-level pointer) | empty |
| `TestNestedClaudeMD_DoesNotWalkAncestors` | `proj-a` + a new `src/CLAUDE.md` | `["src/api/routes.ts"]` | `src/CLAUDE.md` **absent** (literal §8.6 reading) |
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
| `TestRenderOrder_EightNormativeKindsInIotaOrder` | — | the eight §8.6 kinds appear in `renderOrder` in strictly increasing `ItemKind` value order; the two additive kinds appear between `ItemPointers` and `ItemDropReport` |
| `TestBuild_ItemOrderInPayload` | golden `full.json`, budget 12 000 | the substrings `"## 1. "`…`"## 8. "` appear in the payload at strictly increasing byte offsets, with `## 6a.` and `## 6b.` between `## 6.` and `## 7.` |
| `TestBuild_RankIsOneBasedAmongEmitted` | `minimal.json` against an empty project root, so items 3, 4, 5, 6, 6a and 6b are all empty | `Items[0].Rank==1` (invariants), `Items[1].Rank==2` (user intent), `Items[2].Rank==3` (drop report), `Items[3].Rank==4` (affordance) — rank counts **emitted** items, so the omitted kinds consume no rank |
| `TestBuild_InjectionTagging` | `full.json`, `Ref.Seq=7` | payload starts `<!-- qompack:injected seq=7 ver=1 -->\n`, ends `<!-- /qompack:injected -->`; `Unwrap` round-trips and returns `seq==7` |
| `TestUnwrap_RejectsMalformed` | `"no tags here"`, `"<!-- qompack:injected seq=x ver=1 -->x"` | `ok==false` |
| `TestSentinel_Deterministic` | `("sess-1", 7)` | equals `"qpk-ac-" + core.HashBytes("qompack.sentinel.v1", []byte("sess-1|7")).Short()`; differs for seq 8 |
| `TestBuild_SentinelIsLastLineInsideTags` | `full.json` | the line immediately before the close tag is `<!-- qompack:sentinel ` + `Sentinel(sess,7)` + ` -->` |

**Item 1 — invariants**

| Test | Setup | Expected |
|---|---|---|
| `TestInvariants_Verbatim` | 4 invariants, one 900-byte text | all four present, text byte-identical (no truncation, no ellipsis) |
| `TestInvariants_NeverTruncatedUnderTinyBudget` | budget forced to 200 via `MaxTokens` | all four present, `Result.Degraded==true`, `Items[0].Truncated==false` |
| `TestInvariants_EmptyOmitsSection` | zero invariants | `"## 1."` absent from the payload |

**Item 2 — verbatim intent (G7.3, G2.3)**

| Test | Setup | Expected |
|---|---|---|
| `TestUserIntent_FromL0NotCheckpoint` | fake store returns L0 text `"FIX THE WEBHOOK"`, checkpoint says `"working on payment stuff"` | payload contains `FIX THE WEBHOOK` and does **not** contain `working on payment stuff`; a `DropEntry{Kind:"intent_mismatch"}` is present; `Loud` was called once |
| `TestUserIntent_EarliestTurnOfThisSessionWins` | four `UserPromptSubmit` hits; three resolve to `Session=="s1"` with `Turn` 5, 1, 3 and one to `Session=="s0"` with `Turn` 0; request session is `s1` | the `Turn`-1 text of `s1` is injected; the `s0` text never appears |
| `TestUserIntent_IgnoresOtherSessions` | every hit resolves to a different session | falls back to the checkpoint copy with `DropEntry{Kind:"user_intent_source"}` — a previous session's prompt is never injected |
| `TestUserIntent_ToolUseLookupErrorSkipsHit` | one hit's `ToolUse` returns `core.ErrNotFound`, another resolves cleanly | the resolvable hit is used, no `Loud`, no error returned |
| `TestUserIntent_FallsBackToCheckpointWhenStoreEmpty` | store returns no hits | checkpoint text injected, `DropEntry{Kind:"user_intent_source"}` present, no `Loud` |
| `TestUserIntent_FallsBackWhenStoreErrors` | store returns `errors.New("boom")` | same as above, Info log, no error returned |
| `TestUserIntent_StripsPriorInjections` | L0 text wrapped in injection tags | injected text has the tags removed |
| `TestUserIntent_FencedPromptSurvivesVerbatim` | L0 prompt is 30 lines and contains a ` ``` ` fenced stack trace | the whole prompt is injected byte-identically; `guardNoContents` is never applied to item 2 (this is the G7.3 regression guard for the guard itself) |
| `TestUserIntent_TruncatesEvolutionNotOriginal` | 20 evolution entries, tight budget | original present in full, some evolution entries dropped with `Kind:"user_intent_evolution"` |
| `TestUserIntent_EvolutionHasItsOwnShare` | 20 evolution entries, 23 eliminations, budget 8 000 | at least one evolution unit is admitted — evolution is not starved by eliminations, because `shareIntentEvolutionPct` is allocated before item 3 fills |
| `TestUserIntent_CapsAtMaxIntentBytes` | 40 KiB L0 prompt | at most 8 192 bytes read; payload still under the cap |

**Item 3 — eliminations**

| Test | Setup | Expected |
|---|---|---|
| `TestEliminations_TopNFromConfig` | 23 records, `EliminationsTopN=8` | exactly 8 rendered + the note unit |
| `TestEliminations_OrderedBySliceScore` | fake graph scores `elimination:e5`=0.9, `e2`=0.5, rest 0 | `e5` first, `e2` second |
| `TestEliminations_FileProxyScore` | no `elimination:` node; `file:src/db/pool.ts`=0.8 for `e7` | `e7` sorts as if 0.6 (`0.8*0.75`) |
| `TestEliminations_TieBreakByTSThenID` | three records, all score 0 | descending TS, then ascending ID |
| `TestEliminations_NoteCountsTheRest` | 23 seen, 8 shown | payload contains `15 further eliminations are recorded and not shown.` |
| `TestEliminations_StandingInstructionAlwaysPresent` | 0 records | `"Before committing to an approach, call already_tried."` still present (via item 8) |
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
| `TestClampBudget_ZeroUsesMax` | `Budget=0`, cfg 8 000/12 000 | 12 000 |
| `TestClampBudget_BelowMinRaised` | `Budget=500` | 8 000 |
| `TestClampBudget_AboveMaxLowered` | `Budget=50_000` | 12 000 |
| `TestClampBudget_InvertedConfigCorrected` | min 12 000, max 8 000 | 8 000, no panic |
| `TestBuild_NeverExceedsMaxTokens` | `full.json`, budgets 8 000/10 000/12 000 | `Result.Tokens <= 12 000` in all three |
| `TestBuild_SmallerThanStock` | `full.json` at 12 000 | `Result.Tokens < 50 000 + 25 000` — the §2.4 stock restoration figure, asserted explicitly so the G3.3 claim is a test, not a comment |
| `TestBuild_MinFillReadmitsUnits` | material worth 11 000 tokens, first pass yields 6 500 | `Result.Tokens >= 8 000`, at least one previously dropped unit re-admitted, its drop entry removed |
| `TestBuild_PrefixTruncationNotCheapestFirst` | units of 100, 5 000, 100 tokens, allowance 300 | only the first unit is admitted; the third is **not** skipped ahead to |
| `TestBuild_CarryForward` | eliminations use 10% of their share | decisions' allowance exceeds their nominal share by the carry |
| `TestBuild_TruncatedFlagSet` | tight budget | every item that dropped a unit has `Truncated==true`, others `false` |
| `PropBuild_MonotoneInBudget` (rapid) | random checkpoints, budgets in [8 000, 12 000] | `Tokens(B1) <= Tokens(B2)` whenever `B1 <= B2`; the item set at `B1` is a **prefix-wise subset** of the item set at `B2` (embedded-coding property, §6.9) |
| `PropBuild_NeverPanics` (rapid) | random checkpoints incl. empty strings, nil slices, huge strings, invalid UTF-8 | no panic, `Result.Tokens <= MaxTokens` or `Degraded` |
| `PropBuild_Deterministic` (rapid) | same input twice | byte-identical `Result.Text` |

**Reporter**

| Test | Setup | Expected |
|---|---|---|
| `TestReporter_RecordThenCurrentDrops` | record 3 entries | `CurrentDrops` returns the same 3, in order |
| `TestReporter_MissingFileIsEmptyNotError` | fresh project | `(nil, nil)` |
| `TestReporter_CorruptFileIsEmptyAndDeleted` | write `{{{` | `(nil, nil)`, one `Loud`, file removed |
| `TestReporter_ResetDeletes` | record then `Reset` | `CurrentDrops` returns `(nil, nil)`; a second `Reset` is a no-op |
| `TestReporter_SanitizesSessionID` | session `"a/b\\c:*?"` | file name matches `^rehydrate-[A-Za-z0-9._-]+\.json$` |
| `TestReporter_GoldenStateFile` | a fixed `State` literal (including a fixed `Emitted`; the Reporter reads no clock) | byte-identical to `testdata/golden/rehydrate/state.json` |

The `var _ mcp.DropReporter = rehydrate.NewReporter(...)` assertion is **not** in this package: `rehydrate` may not import `mcp` (§3.2), so the assertion lives in `test/e2e`, which is outside `internal/` and may import both. It is listed in the e2e table below.

**Goldens and degradation**

| Test | Setup | Expected |
|---|---|---|
| `TestBuild_Golden_Full12K` | `full.json`, budget 12 000 | byte-identical to `testdata/golden/rehydrate/full-12k.txt`. No clock is injected: `Build` reads none, and a golden that changed between runs would be a bug, not a fixture refresh |
| `TestBuild_Golden_Full8K` | budget 8 000 | matches `full-8k.txt`; every section heading present in `full-12k.txt` that survives at 8K appears in the same order |
| `TestBuild_Golden_Minimal` | `minimal.json`, empty project root | matches `minimal.txt` |
| `TestBuild_Golden_NoCheckpoint` | `Ref{Seq:0}`, zero checkpoint, ledger with 3 records | matches `no-checkpoint.txt`; `Degraded==true`; contains items 2, 3, 6b, 7, 8 |
| `TestBuild_Golden_Degraded` | `empty.json`, `MaxTokens` forced to 200 so tier-1 material overflows the cap | matches `degraded.txt`; `Degraded==true`; items 1, 2 and 8 present in full; every discretionary item absent and represented in the drop report |
| `TestBuild_NonCompactSourceEmitsNothing` | `Source:"startup"`, `"resume"`, `"clear"` | `Result.Items` empty, `Result.Text == ""` |
| `TestBuild_ContextCancelled` | pre-cancelled ctx | returns `ctx.Err()`, no partial write |
| `TestBuild_NilDeps` | every `Deps` member nil except `Log` | no panic, `Degraded==true`, items 1/2 (checkpoint copy)/8 present |
| `TestBuild_NoTranscriptRead_ClosesG75` | spy `Deps` recording every call; `Event.TranscriptPath` points at a file containing **no** `<summary>` block (the §2.8 `content: null` failure, where the compaction call ran a tool instead of summarizing) | the payload is byte-identical to the run with a well-formed summary transcript, and the spies record **zero** reads of `TranscriptPath`. This is the mechanical G7.5 closure: the injection is checkpoint-and-store-derived, so a summarizer that produced nothing costs the session nothing |
| `BenchmarkBuild` | `full.json`, 24 rules, 40 skills, 200 eliminations, 5 000 dag nodes | `L5-BUILD` p99 < 250 ms |

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
| `TestService_ClearResetsState` | record then `Clear` | state file gone, `nil` returned, ledger/store untouched (spies assert zero calls) |
| `TestService_ClearOnMissingStateIsNoOp` | fresh project | `nil` |

### `test/e2e/sessionstart_compact_test.go`

Drives the **real binary** against a **real daemon** (SP-01's harness).

| Test | Setup | Expected |
|---|---|---|
| `TestE2E_SessionStartCompact` | temp project from `proj-a`, a checkpoint golden copied into `.qompack/checkpoints/0007.json` + MANIFEST line, daemon warm | `qompack session-start` with `{"hook_event_name":"SessionStart","source":"compact","session_id":"e2e-1"}` on stdin exits **0** and prints JSON whose `hookSpecificOutput.additionalContext` contains `## 1. Invariants`, `## 7. No longer in context`, `## 8. Retrieval`, and `Sentinel("e2e-1",7)` |
| `TestE2E_SessionStartCompactUnderBudget` | same | the estimated token count of `additionalContext` is `<= 12 000` |
| `TestE2E_SessionStartClear` | after a compact run | `source:"clear"` exits 0, emits no `additionalContext`, and `.qompack/state/rehydrate-e2e-1.json` is gone |
| `TestE2E_SessionStartCompactNoStore` | `.qompack/` deleted between runs | exits 0, empty or degraded payload, never non-zero |
| `TestE2E_SessionStartCompactAfterFailedSummary` (G7.5) | same as `TestE2E_SessionStartCompact`, but the session transcript's tail contains a tool-call block and **no** `<summary>` — the §2.8 "model called a tool instead of summarizing, `content: null`" failure | exits 0 and emits the *same* `additionalContext` as the well-formed-summary run: the checkpoint is the fallback path, so a failed compaction call degrades cost, not context |
| `TestE2E_DropReporterSatisfiesMCP` | — | `var _ mcp.DropReporter = rehydrate.NewReporter(root, log)` compiles (this file may import both) |
| `TestE2E_SessionStartLatency` | 30 runs, warm daemon | p99 wall time < 1.5 s (`L5-SESSIONSTART`) |

### `test/replay/phase3_rehydrate_test.go` — the Phase 3 exit criterion

Two `eval.Policy` implementations plus three assertions.

```go
// stockRestorePolicy reproduces §2.4 step 7: top 5 recently-read files at 50K/5K-per-file,
// plus invoked skills at 25K/5K-per-skill. It is the Phase 0 comparison point.
type stockRestorePolicy struct{ est tokens.Estimator }
func (stockRestorePolicy) Name() string { return "stock-restore" }
func (p stockRestorePolicy) KeepSet(ctx context.Context, s eval.Session, at core.TurnIndex, budget core.Tokens) (eval.KeepSet, error)

// qompackRehydratePolicy calls rehydrate.Build over a checkpoint golden (Rule W-2: SP-10 is a
// same-wave sibling, so no checkpoint.Writer is invoked here) and reports the emitted payload
// as its keep-set.
type qompackRehydratePolicy struct{ deps rehydrate.Deps; cfg config.Config }
```

**How the two policies populate `Score.RehydrationTokens`.** `eval.ScoreRun` derives it from the run's `KeepSet.Tokens` (§11.2 "Rehydration budget — tokens spent restoring context"), so each policy must fill that field with what it actually put back into the window and nothing else:

- `stockRestorePolicy.KeepSet` returns `IDs` = the five file `tool_use_id`s plus the invoked-skill ids it would re-inject, and `Tokens` = the sum of their per-file/per-skill costs after applying §2.4's 5K-per-file, 50K-total and 5K-per-skill, 25K-total caps. `P` is the compaction point's token position.
- `qompackRehydratePolicy.KeepSet` calls `rehydrate.Build` and returns `IDs` = one synthetic id per emitted `Item` (`"rehydrate:" + Item.Kind.String()`), `Tokens` = `Result.Tokens` exactly — the same number the hard-cap assertion and `TestE2E_SessionStartCompactUnderBudget` check, so A1 and the unit tests cannot disagree about what "rehydration budget" means.

| Assertion | Definition | Threshold |
|---|---|---|
| **A1 — smaller budget** | `median(qompack.RehydrationTokens)` vs `median(stock.RehydrationTokens)` over the 24-session synthetic corpus | `qompack < stock` **and** `qompack <= cfg.Runtime.Rehydrate.MaxTokens` on every session |
| **A2 — first-divergence improves** | `eval.Score.Divergence.FirstDivergenceTurn`, median over the ≥ 20 sessions carrying a `CompactionAt` point, compared against SP-02's committed Phase 0 baseline | `median(qompack) > median(baseline)`; the assertion message prints both numbers so the replay-gate artifact is self-explaining |
| **A3 — O1 summarization-input reduction** | `residual = Σ tokens of turns after Ref.Frontier`; `stockSpan = Σ tokens of all turns up to the compaction point` | `residual <= cfg.Checkpoint.Frontier.MaxResidualTokens` (20 000) **and** `1 - residual/stockSpan >= 0.5` on the multi-compaction sessions |

A3's qualifier — §10's *"when the span instruction is honoured"* — is modelled by `eval.ReplayOptions{Deterministic: true}`, which assumes compliance. Non-compliance is explicitly out of scope for this assertion: §8.5 states `custom_instructions` is advisory and *"the rehydrator never depends on the summary having complied"*, and the checkpoint remains authoritative. A comment in the test cites that sentence so no future reader mistakes the assumption for an oversight.

`Frontier` values come from the checkpoint goldens' `Ref.Frontier`, not from SP-12's live frontier advancement (same wave). The V4 verification run re-executes this file against SP-10's and SP-12's real implementations.

---

## Commit plan

Work happens on **`feat/sp11-rehydrator-l5`**, cut from `develop` with `SP-01`, `SP-06`, `SP-07`, `SP-09` (and, by wave ordering, `SP-08`) already merged. Exactly **7 commits**. Each compiles and passes `go run ./tools/devtool test` for the packages it touches before it is made.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

```
- [ ] git fetch origin && git checkout develop && git pull
- [ ] git checkout -b feat/sp11-rehydrator-l5
- [ ] go run ./tools/devtool ci-local        # baseline must be green before any change
```

### Commit 1 — `feat(rules): paths:-frontmatter scanner, **-aware globs, nested CLAUDE.md discovery`

Closes G4.1 and G4.2.

- [ ] Create `testdata/fixtures/rules/proj-a/**` and `testdata/fixtures/rules/proj-edge/**` exactly as listed in the fixtures section.
- [ ] Write `internal/rules/glob_test.go` (the 9 `TestMatch_*` table rows + `PropMatch_NeverPanics`) — **run, must fail to compile** (`Match` undefined).
- [ ] Write `internal/rules/frontmatter_test.go` (the 8 `TestParseFront_*` rows) — must fail.
- [ ] Write `internal/rules/scanner_test.go` (the 10 `TestPathScoped_*` + 7 `TestNestedClaudeMD_*` rows + `BenchmarkPathScoped`) — must fail.
- [ ] Implement `internal/rules/glob.go`, `frontmatter.go`, `scanner.go`.
- [ ] `go run ./tools/devtool test ./internal/rules/...` — all green.
- [ ] `go run ./tools/devtool bench` and confirm `BenchmarkPathScoped` meets `L5-RULES` (< 50 ms p99).
- [ ] `go run ./tools/devtool lint vet fmt`.

Files: `internal/rules/{glob,frontmatter,scanner}.go`, `internal/rules/{glob,frontmatter,scanner}_test.go`, `testdata/fixtures/rules/**`.
Footer: `Refs: SP-11, G4.1, G4.2, §8.6`

### Commit 2 — `feat(skills): compact skill index budgeted from runtime.rehydrate.skillIndexTokens`

Closes G4.4.

- [ ] Write `internal/skills/indexer_test.go` (the 11 rows of the `internal/skills` table + `BenchmarkIndex`) — must fail.
- [ ] Implement `internal/skills/frontmatter.go` (with the mandatory "duplicated by §3.2 import policy" comment) and `internal/skills/indexer.go` including `BodyTokens`.
- [ ] `go run ./tools/devtool test ./internal/skills/...` — green.
- [ ] `go run ./tools/devtool lint` — the `nomagic` pass must report **no** literal `450` in `internal/skills`.
- [ ] `go run ./tools/devtool bench` — `L5-SKILLS` < 20 ms p99.

Files: `internal/skills/{frontmatter,indexer}.go`, `internal/skills/indexer_test.go`.
Footer: `Refs: SP-11, G4.4, §8.6, §11.6`

### Commit 3 — `feat(rehydrate): eight-item importance-ordered injection with L0 verbatim intent`

Closes G2.3's read side and G7.3.

- [ ] Write `internal/rehydrate/fake_test.go`: `fakeStore`, `fakeLedger`, `fakeGraph`, `fakeReader`, `spyLogger`, all reading `testdata/golden/contracts/**` (Rule W-2).
- [ ] Write `internal/rehydrate/order_test.go`, `items_test.go`, `render_test.go`, `sentinel_test.go` (the "Ordering and structure", "Item 1", "Item 2", "Item 3", "Item 4/5/6" and "Items 6a/6b" tables) — must fail.
- [ ] Implement `internal/rehydrate/{render,sentinel,items}.go` and the item-building half of `rehydrate.go`.
- [ ] `go run ./tools/devtool test ./internal/rehydrate/...` — green.
- [ ] Confirm `TestUserIntent_FromL0NotCheckpoint` passes: this is the mechanical G7.3 closure.

Files: `internal/rehydrate/{rehydrate,items,render,sentinel}.go`, `internal/rehydrate/{fake,order,items,render,sentinel}_test.go`.
Footer: `Refs: SP-11, G2.3, G7.3, G7.5, §8.6, §8.7`

### Commit 4 — `feat(rehydrate): 8-12K budget fill, progressive truncation, and the drop report`

Closes G3.3 and G4.5.

- [ ] Write `internal/rehydrate/budget_test.go` (the Budget table incl. `PropBuild_MonotoneInBudget`, `PropBuild_NeverPanics`, `PropBuild_Deterministic`) — must fail.
- [ ] Write `internal/rehydrate/drops_test.go` (the Reporter table) — must fail.
- [ ] Write `internal/rehydrate/golden_test.go` with `-update` support; generate `testdata/golden/rehydrate/{full-12k,full-8k,minimal,no-checkpoint,degraded,state.json}` and **read every generated golden by eye** before committing — a golden accepted without reading is a placeholder.
- [ ] Implement `internal/rehydrate/{budget,drops}.go` and complete `Build`.
- [ ] Add `TestBuild_NoTranscriptRead_ClosesG75` and confirm it passes: the spies must record zero reads of `Event.TranscriptPath`, and the payload must be byte-identical with and without a well-formed `<summary>` in the transcript. This is G7.5's unit-level closure and it is the reason the footer below names G7.5.
- [ ] Flip the W-1 skips in `internal/rehydratetest/suite.go` and add behaviour assertions for `Build`, `StandingInstruction`, `Sentinel`, `Reporter`.
- [ ] `go run ./tools/devtool test ./internal/rehydrate/... ./internal/rehydratetest/...` — green.
- [ ] `go run ./tools/devtool cover` — `rehydrate` ≥ 85%, `rules`/`skills` ≥ 75%.
- [ ] `go run ./tools/devtool bench` — `BenchmarkBuild` meets `L5-BUILD` (< 250 ms p99).

Files: `internal/rehydrate/{budget,drops}.go`, `internal/rehydrate/{budget,drops,golden,bench}_test.go`, `internal/rehydratetest/suite.go`, `testdata/golden/rehydrate/**`.
Footer: `Refs: SP-11, G3.3, G4.5, G7.5, §8.6, §6.9, §12`

### Commit 5 — `feat(daemon): SessionStart compact and clear branches behind the rehydrator seam`

- [ ] Write `internal/daemon/rehydrate_service_test.go` (the service table, 9 tests) — must fail.
- [ ] Write `test/e2e/sessionstart_compact_test.go` (the 7 rows of the e2e table, including `TestE2E_SessionStartCompactAfterFailedSummary`, which is G7.5's end-to-end closure) — must fail.
- [ ] Implement `internal/daemon/rehydrate_service.go`.
- [ ] Add `Rehydrator observer.SessionStartRehydrator` to `daemon.Services`; pass it into the observer construction site; set it in the daemon startup sequence. **These are the only lines this branch changes in SP-05/SP-08-owned files.**
- [ ] `go run ./tools/devtool test ./internal/daemon/... ./test/e2e/...` — green.
- [ ] `go run ./tools/devtool build-all` — all six targets compile.
- [ ] Verify by hand that `qompack session-start` exits 0 for `source` ∈ {`startup`,`resume`,`compact`,`clear`} and for malformed stdin (§13 invariant 6).

Files: `internal/daemon/rehydrate_service.go`, `internal/daemon/rehydrate_service_test.go`, `internal/daemon/services.go` (+1 member), the observer construction site, `test/e2e/sessionstart_compact_test.go`.
Footer: `Refs: SP-11, §7.3, §8.6, §12.1, 00-ARCH §5.21`

### Commit 6 — `test(replay): Phase 3 exit criterion — divergence, budget, and O1 span reduction`

- [ ] Write `test/replay/phase3_rehydrate_test.go` with `stockRestorePolicy`, `qompackRehydratePolicy`, and assertions A1/A2/A3 — must fail (thresholds unmet or policies unimplemented).
- [ ] Implement the two policies against `eval.Policy`; wire them into the replay-gate's phase-exit-criteria registry.
- [ ] `go run ./tools/devtool replay --corpus testdata/sessions/synthetic --baseline develop` — A1, A2 and A3 all pass; capture the printed medians in the commit body.
- [ ] Confirm no other metric regresses by more than 2% (§11.3). If one does, the PR body needs a `sign-off:` trailer — but the expected outcome here is a strict improvement on rehydration budget and first-divergence with no regression elsewhere, because this branch adds no hot-path work.

Files: `test/replay/phase3_rehydrate_test.go`.
Footer: `Refs: SP-11, §10 Phase 3, §11.1, §11.2, §11.3`

### Commit 7 — `docs(rehydrate): ADR 0011 on item order, budget shares, and whole-rule restoration`

- [ ] Write `docs/adr/0011-rehydration-budget-and-item-order.md` covering: why the eight §8.6 kinds are `iota`-ordered and `renderOrder` is asserted against them; why the two additive kinds sit between items 6 and 7; the exact six share percentages, why intent-evolution takes a share of its own, and the carry-forward rule; why `minTokens` is a fill target rather than a floor; why a path rule is restored whole or not at all (G4.3 — "a partial instruction set is worse than an absent one"); **why `guardNoContents` covers items 4–6 only and must never inspect the verbatim tier-1 items 1–2** (a fenced stack trace in a real user prompt would otherwise re-open G7.3); **why item 3 reads both elimination scopes rather than `eliminations.defaultScope`** (§8.3 project-scoped records are the longest-lived negative knowledge in the store); why `rehydrate.Build` takes no clock and is a pure function; why the sentinel is derived from `(session, seq)` and mirrored into the state file for SP-05; and why the `clear` branch touches no ledger state.
- [ ] `go run ./tools/devtool gen-config-docs && git diff --exit-code` — `docs/config-reference.md` must already be current (this branch adds no config keys; SP-01 owns them). If it diffs, the diff is SP-01's and must be reported, not absorbed.
- [ ] `go run ./tools/devtool ci-local` — full local pipeline green.
- [ ] `git log --format=%B develop..HEAD | grep -Ei "Co-Authored-By|Signed-off-by|Generated with|🤖"` returns nothing.

Files: `docs/adr/0011-rehydration-budget-and-item-order.md`.
Footer: `Refs: SP-11, §8.6, §6.9, G4.3`

---

## Subagent strategy

This subplan is **heavy**. Partition it across four parallel subagents in the implementing editor's own session, then integrate sequentially. The commit plan stays strictly sequential and is executed only by the main session.

**Main session keeps (never delegated).** Every `git` operation; the commit sequence; `internal/rehydrate/budget.go` and `rehydrate.go` (the integration point where all four subagents' outputs meet — splitting it produces merge conflicts inside one function); the golden files (they must be read by the agent that owns the whole payload); `internal/daemon` wiring; and every decision about §8.6 ordering.

**Subagent A — `internal/rules`.** Owns `glob.go`, `frontmatter.go`, `scanner.go` and their three test files, plus `testdata/fixtures/rules/proj-a/**` and `proj-edge/**`. Returns: the three source files, the three test files, the fixture tree, and the measured `BenchmarkPathScoped` p99. Constraint given to it: **may import foundation packages only** (`core`, `paths`, `config`, `logging`, `obs`) — an import of `tokens` is an architecture violation that the import-graph check will reject.

**Subagent B — `internal/skills`.** Owns `frontmatter.go`, `indexer.go`, `indexer_test.go`, and the `.claude/skills/**` half of `proj-a`. Returns those files plus the `BenchmarkIndex` p99. Constraint: same foundation-only import budget; the budget parameter is passed in, never read from a constant — the `nomagic` pass will fail on a literal `450`.

**Subagent C — `internal/rehydrate` item builders.** Owns `items.go`, `render.go`, `sentinel.go` and their test files plus `fake_test.go`. Returns those files and the exact rendered text of each item for one canonical input, which the main session pastes into the ADR and uses to hand-verify the goldens. Constraint: every builder returns `([]unit, seen int)` and performs **no budget arithmetic** — budgeting is the main session's file.

**Subagent D — the harnesses.** Owns `internal/daemon/rehydrate_service_test.go`, `test/e2e/sessionstart_compact_test.go`, and `test/replay/phase3_rehydrate_test.go` including both `eval.Policy` implementations. Returns those three files. It writes tests against the interfaces in this document's Interface contract section and does **not** wait for A/B/C — the tests are supposed to fail first.

**Integration order.** A and B land in commits 1 and 2 unchanged. C's output lands in commit 3 after the main session reconciles the unit signatures. The main session then writes `budget.go` and `drops.go` (commit 4) against C's builders and generates the goldens. D's daemon and e2e tests land in commit 5 alongside the main session's `rehydrate_service.go`; D's replay test lands in commit 6.

**Conflict avoidance.** No two subagents touch the same file. A and B both write into `testdata/fixtures/rules/proj-a/` but into disjoint subdirectories (`.claude/rules/` and `src/` for A, `.claude/skills/` for B); the main session creates the shared root files (`CLAUDE.md`, `src/*/CLAUDE.md`) before dispatching.

**What must be verified in the main session regardless of what a subagent reports.** That `renderOrder` matches the §8.6 numbering; that no golden contains a fenced code block in items 4, 5 or 6 (items 1–3 are exempt by design and must be checked the other way — that a fenced user prompt *did* survive verbatim); that no literal `450`, `8000` or `12000` appears outside `_test.go`; that `Result.Tokens <= MaxTokens` holds on every golden; and that every hook path exits 0.

---

## Exit criteria

### Quoted verbatim from Qompack.md §10 Phase 3

> **Exit criterion:** post-compaction first-divergence turn index improves against Phase 0 baseline at a *smaller* rehydration budget than stock; measured reduction in summarization-call input tokens when the span instruction is honoured.

Operationalized by `test/replay/phase3_rehydrate_test.go` assertions A1, A2 and A3 as specified above, run by `devtool replay` over the 24-session synthetic corpus (≥ 20 sessions, satisfying `eval.minSessions`).

### Quoted verbatim from Qompack.md §8.6

> **Budget discipline.** Default rehydration budget is deliberately far below Claude Code's 50K + 25K: target 8–12K.

Operationalized by `TestBuild_NeverExceedsMaxTokens`, `TestBuild_SmallerThanStock`, `TestBuild_MinFillReadmitsUnits`, `PropBuild_MonotoneInBudget`, and `TestE2E_SessionStartCompactUnderBudget`.

### Quoted verbatim from Qompack.md §11.3 (the three of its four bullets that bind this branch; the omitted one is "Store growth sublinear in session length after dedup", which is SP-06's)

> - Hook p99 latency < 15ms (L0), < 2s (L4)
> - No metric may regress by more than 2% to improve another without explicit sign-off
> - Every phase gate runs the full replay suite

This branch adds no L0 work and no L4 work, so B-A and B-E are unchanged; `bench-gate` must remain green on all three platforms, and `replay-gate` must report no >2% regression on any metric.

### Local criteria

- [ ] All tests enumerated in the Test plan exist, are named as written, and pass: `go run ./tools/devtool test` and `test-race`.
- [ ] Coverage: `internal/rehydrate` ≥ **85%**, `internal/rules` ≥ **75%**, `internal/skills` ≥ **75%** (§6.4).
- [ ] `gofumpt -l` empty; `golangci-lint run` clean; `go vet` clean; `staticcheck` clean.
- [ ] The `nomagic` pass reports no forbidden literal in `internal/rehydrate`, `internal/rules`, `internal/skills` — specifically no `450`, `8000`, `12000`.
- [ ] The import-graph check passes: `rules` and `skills` import foundation only; `rehydrate` imports only `checkpoint store negknow dag rules skills tokens` + foundation; nothing imports `daemon`.
- [ ] Benchmarks within budget: `L5-BUILD` < 250 ms p99, `L5-RULES` < 50 ms p99, `L5-SKILLS` < 20 ms p99, `L5-SESSIONSTART` < 1.5 s p99; `benchstat` shows no >25% regression against `testdata/bench-baseline.txt`.
- [ ] `devtool plugin-validate` green (this branch changes no manifest, so the diff must be empty).
- [ ] `security` CI job green: no `net/http`, no `net/url`, no `crypto/tls`, no writes outside `.qompack/`.
- [ ] Every hook subcommand exits 0 under fault injection for `source` ∈ {`startup`,`resume`,`compact`,`clear`} and for malformed stdin.
- [ ] Freshly built `develop` + this branch reports `contract.ModeFull` (§12.1's CI assertion still holds; `hook.additional_context_delivered` now has a real producer and must report `OK` rather than `not-yet-implemented`).
- [ ] CI green on `feat/sp11-rehydrator-l5` for every job: `verify`, `test` (ubuntu/macos/windows), `cover`, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs`.
- [ ] Exactly 7 commits, each a valid Conventional Commit with a `Refs:` footer.

---

## Done checklist

- [ ] Branch `feat/sp11-rehydrator-l5` was cut from `develop` with SP-01, SP-06, SP-07, SP-09 merged.
- [ ] **Spec coverage vs. the Design context section.** Walk the Design context section top to bottom and tick each quoted artifact against an implementing test: §8.6's eight numbered items (`TestBuild_ItemOrderInPayload`), its three instruction-restoration bullets (`TestRestored_*`, `TestSkillIndex_*`), its budget sentence (`TestBuild_NeverExceedsMaxTokens`, `TestBuild_SmallerThanStock`), §8.7's design note (`TestEliminations_StandingInstructionAlwaysPresent`), §8.7's tool table (`AffordanceNotice` golden), §8.5's injection tagging (`TestBuild_InjectionTagging`), §8.5's schema (every checkpoint field consumed by a builder), §8.3's stale note (`TestEliminations_StaleRendersTheVerbatimNote`), §6.9's embedded coding (`PropBuild_MonotoneInBudget`), §2.7's four broken rows (G4.1/G4.2/G4.3/G4.4 tests), §11.5's four `rehydrate` keys (each read at least once, none hardcoded), §12.1's degraded-passive clause (`TestService_DegradedPassiveEmitsNothing`), §8.1 item 7's verbatim capture as the *only* intent source (`TestUserIntent_EarliestTurnOfThisSessionWins`, `TestUserIntent_IgnoresOtherSessions`), G7.5's checkpoint-as-fallback (`TestBuild_NoTranscriptRead_ClosesG75`, `TestE2E_SessionStartCompactAfterFailedSummary`), and §10 Phase 3's exit criterion (A1/A2/A3).
- [ ] **Placeholder scan.** `grep -rniE "TBD|TODO|FIXME|XXX|handle (this|edge cases)|implement appropriately|similar to" internal/rehydrate internal/rules internal/skills internal/daemon/rehydrate_service.go test/e2e/sessionstart_compact_test.go test/replay/phase3_rehydrate_test.go docs/adr/0011-*.md` returns nothing.
- [ ] **No stub residue.** `grep -rn "ErrNotImplemented" internal/rehydrate internal/rules internal/skills` returns nothing; `grep -rn "t.Skip" internal/rehydratetest` returns nothing (W-1 skips all flipped).
- [ ] **Type consistency with the Interface contract.** `Build`, `StandingInstruction`, `Scanner.PathScoped`, `Scanner.NestedClaudeMD`, `Indexer.Index`, `Item`, `ItemKind`, `Request`, `Result`, `Deps`, `Rule`, `Entry` match 00-ARCHITECTURE §5.15 **field-for-field with zero deviations** — in particular `Deps` gains no `Clock` and no `Metrics` member. The only additions anywhere are the two additive `ItemKind` constants and the package-level helpers `AffordanceNotice`, `Sentinel`, `Wrap`, `Unwrap`, `Match`, `New`, `BodyTokens`, `NewReporter`, `Reporter`, `State`, `ItemStat` — all in packages this subplan owns, none removing or altering anything §5 declares, so no §0 amendment is required by this branch.
- [ ] `var _ mcp.DropReporter = rehydrate.NewReporter(...)` compiles in `test/e2e` — SP-13's `dropped` tool has a real backing implementation.
- [ ] `var _ observer.SessionStartRehydrator = NewRehydrateService(...)` compiles in `internal/daemon`.
- [ ] No file under `internal/observer` was modified on this branch: `git diff --name-only develop..HEAD | grep '^internal/observer/'` returns nothing.
- [ ] `Qompack.md` is unmodified: `git diff --name-only develop..HEAD | grep '^Qompack.md$'` returns nothing.
- [ ] Only two lines changed in SP-05-owned daemon files (the `Services` member and the observer construction site), and both are additive and nil-tolerant.
- [ ] Golden payloads were read line by line by a human/agent, not merely regenerated with `-update`.
- [ ] **Commit count verified 5–8:** `git rev-list --count develop..HEAD` returns **7**.
- [ ] **No co-author trailers:** `git log --format=%B develop..HEAD | grep -Ei "Co-Authored-By|Signed-off-by|Generated with|🤖"` returns nothing.
- [ ] `devtool ci-local` green; the branch is ready to merge into `develop` in the wave-3 merge order.
