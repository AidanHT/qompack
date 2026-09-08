# V5-VERIFY §4.4 disposition — `TestV5_PreCompactToRehydrateToDroppedRoundTrip`

- **Identifier (retained):** `TestV5_PreCompactToRehydrateToDroppedRoundTrip`
- **Current criterion (V5-VERIFY §4, row 4.4):** SP-10/SP-11/SP-13 bounded recovery/coverage
  without optional-PostCompact dependency.
- **Disposition:** authored.
- **Level and file:** e2e, `test/e2e/v5_x04_test.go`. The seam crosses processes three times —
  the hooks are real `qompack` processes, the MCP surface is a real `qompack mcp` child speaking
  JSON-RPC over stdio to the daemon's real `mcp` op, and the slash-command frontend is a real
  `qompack dropped --json` process over the shipped retrieval proxy. The daemon itself is composed
  in process for the two reasons `test/e2e/v4_harness_test.go` records (BindCheckpoint must precede
  `daemon.New`; the segment log and idle tick are only reachable in process), plus one this row
  adds: the L6 retrieval op is installed through the exported `cli.NewToolDeps` and
  `daemon.InstallMCPOp`, exactly as `internal/cli`'s `installMCPTools` installs it, so the handler
  set the child and the frontend reach is the shipped one.
- **Base:** verify/v5 @ 87c0c1d.

## Test names and what each asserts

`TestV5_PreCompactToRehydrateToDroppedRoundTrip` (top level):

- the shipped plugin manifest (`pluginmanifest.Default`) declares `PreCompact` and `SessionStart`
  and declares **no** `PostCompact` hook — the clause in the row's title, asserted against the real
  manifest so it fails the day the hook is added without revisiting the criterion.

`.../full_budget_round_trip` — proj-a fixture (root CLAUDE.md, nested CLAUDE.md files,
`paths:`-scoped rules, three skills), default config:

- session-start, verbatim prompt, 40 real `PostToolUse` reads of `src/api/routes.ts`, three closed
  segments, one idle pass (`advance_frontier` ran);
- real PreCompact seals exactly `0001.json`, the MANIFEST line re-hashes, `state/precompact.json`
  records seq 1 with a span instruction naming `.qompack/checkpoints/0001.json` and a local
  frontier > 0, the artifact belongs to this session and carries a file pointer to
  `src/api/routes.ts`;
- real SessionStart(source=compact) injects a payload tagged `seq=1` (not the no-checkpoint path),
  closed, probe-appended, with every emitted §8.6 heading in normative order;
- item 2 quotes the L0 prompt verbatim (nonce `Q5X4-VERBATIM-7b1e`);
- item 6a restores `.claude/rules/api-conventions.md` (scope `paths: src/api/**`, body whole) and
  the nested `src/api/CLAUDE.md` body verbatim;
- item 6b carries all three skills as name + one-line description and no skill body;
- `state/rehydrate-<sess>.json` exists with seq 1, `degraded=false`,
  `budget == runtime.rehydrate.maxTokens` as configured, `tokens <= budget`; the injected span
  re-priced by the supported estimator (`tokens.NewForProject`, ClassProse) is `<= budget`;
- every persisted drop entry is either rendered as an item-7 line or covered by the counted
  "… and N more; call dropped()" tail (or item 7 is absent by ADR 0011 §18 eviction — see below);
- `dropped` over the real stdio child answers `available` unset, `count == len(state.dropped)` and
  `drops` equal to the persisted entries in order;
- `qompack dropped --json` returns an OK envelope for command `dropped` whose ToolResult text block
  is **byte-identical** to the child's;
- second severing: the state file is removed and `dropped` over stdio answers zero entries.

`.../bounded_budget_reports_complete_drops` — same fixture and session shape,
`runtime.rehydrate.{minTokens,maxTokens}` = 600:

- the payload still resolves seq 1; `state.budget == 600`, `state.tokens <= 600`, re-priced span
  `<= 600`; `state.dropped` non-empty, every entry carries a kind;
- coverage under eviction: on this tree the hard-cap loop evicts item 7 (observed: tokens 426–512,
  degraded=true, 44–45 drops, item 7 not rendered), so the test asserts the documented shape —
  `degraded=true`, item 8 present and naming `dropped()` — rather than a rendered item 7;
- `dropped` over stdio returns the COMPLETE persisted list; the frontend's text block is
  byte-identical to the child's.

`.../negative_control_reinjection_disabled` — see below.

## Negative control and how it was proven

Two real severings, no source edit needed:

1. **Runtime switch** — `runtime.migration.reinjection.sessionStartCompact=false` in the project
   config (Qompack.md v1.5 Appendix C's injection kill switch), read by the daemon at composition.
   With it off: SessionStart(compact) emits no `<!-- qompack:injected` span and nothing but the
   route's contract probe line; no `state/rehydrate-<sess>.json` exists; no checkpoint was sealed;
   both `dropped` surfaces answer zero entries (and agree byte for byte); L0 capture still indexed
   the turns (the switch governs injection only); and `p.AssertAppendOnly` passes on that project.
2. **File removal** (inside the positive arm) — after the three-surface identity holds, the state
   file is deleted and `dropped` over stdio answers zero entries, proving the tool reads the file
   the rehydrator persisted rather than a cache.

Authoring-time observation: the first run of the negative arm failed on `require.Empty(ac)` because
the session.start route appends the §12.1 probe even when the rehydrator emits nothing
(`internal/daemon/handlers.go`); the assertion was corrected to "nothing but the probe". That
failure also demonstrated the arm is live.

## Old-to-new assertion map (historical §4.4 → current)

| Historical expectation | Status | Current form / reason |
|---|---|---|
| Fixture: `src/api/routes.ts`, `.claude/rules/api-conventions.md` (`paths: src/api/**`), `src/api/CLAUDE.md`, three skills | kept | the committed `testdata/fixtures/rules/proj-a` tree, which carries exactly these |
| 40 tool uses touching those paths, 3 closed segments, an idle tick, `qompack checkpoint` with a real PreCompact payload | kept | `x4v5SeedSession` + `x4v5Seal`; real hook processes, real segment log, real idle pass |
| PreCompact `customInstructions` carries the O1 span paragraph naming the checkpoint path and frontier turn | kept (corrected) | asserted from `state/precompact.json`'s recorded frontier, as v4_x03 does; the frontier is the LOCAL committed one — no host-side compaction effect is claimed |
| `qompack session-start {"source":"compact"}` then `tools/call dropped {}` then `qompack dropped --json` | kept | real `session-start` process; real `qompack mcp` child over stdio; real `qompack dropped --json` process |
| `additionalContext` contains `## 1.` through `## 8.` in order | corrected | sections with nothing admitted are legitimately absent (render.go); asserted as "every emitted heading in normative order" plus presence of 2, 6a, 6b, 8 (and 7 when not evicted) |
| the restored rule body | kept | `api-conventions.md` heading with `paths: src/api/**` and its body whole |
| the nested `CLAUDE.md` body | kept | `src/api/CLAUDE.md — nested` with body verbatim |
| the skill index | kept | all three name+description lines; a skill body must NOT appear |
| payload ≤ 12 000 tokens | corrected | ≤ the configured `runtime.rehydrate.maxTokens` (read from config, never restated); asserted both from the persisted `tokens`/`budget` pair and by re-pricing the injected span with the supported estimator. It is an estimate, labelled as such — no host-observed token count is claimed (§5) |
| MCP `dropped` and the `dropped` slash command return the byte-identical entry list | kept (strengthened) | the ToolResult text block from the frontend equals the child's text block byte for byte, and both equal the persisted list entry for entry; plus a bounded arm where item 7 could not render but the tool still returns the complete list |
| `.qompack/state/rehydrate-<sess>.json` exists with the checkpoint seq | kept | read and decoded as `rehydrate.State`; seq, budget, tokens, degraded and dropped all asserted |
| (implicit) rehydration after a PostCompact | retired | no such hook exists in the shipped manifest (asserted); the sequence driven is PreCompact → SessionStart(compact) only |
| (implicit) host accepted the instructions / native history shortened | retired | host-side effects no in-repo test can observe (reconciliation map §4.1) |

## Unverified remainder

- **Item 7 rendered under a tight budget.** On this tree a 600-token budget trips the hard-cap
  re-truncation and item 7 is the first discretionary item evicted (ADR 0011 §18; frozen in the
  400-token `degraded` golden). The row therefore verifies coverage through `state.degraded`,
  item 8's `dropped()` affordance and the tool's complete list, not through a rendered item 7.
  Whether §8.6 should protect item 7 in that loop is a design question for SP-11/SP-16, not a
  defect this row can assert either way.
- **Host delivery.** Nothing here observes that the host injected the payload or honoured the
  focus instruction; `CapInjection` remains `implemented_unverified` (internal/contract) and the
  canary suite owns that.
- **Run-to-run token variance in the bounded arm** (512 vs 426 tokens, 44 vs 45 drops across two
  runs with identical inputs) was observed but not chased; the bound held both times. Recorded
  for SP-11's owner as an observation, not a failure.

## Run command and result

```
cd <worktree> && go test ./test/e2e -list 'TestV5_PreCompactToRehydrateToDroppedRoundTrip'
cd <worktree> && go test ./test/e2e -run '^TestV5_PreCompactToRehydrateToDroppedRoundTrip$' -count=1 -timeout=30m -v
```

- `-list` prints the one test name.
- Two consecutive `-count=1` runs after the final edit: PASS and PASS (16.1 s and 29.0 s wall; the
  second run's bounded arm took 18.8 s under co-load — a wall-clock observation, not a gate).
- `gofmt -l ./test ./internal`: clean. `go vet ./test/e2e ./test/integration`: clean.
  `go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns`: PASS.
- No production change was made.
