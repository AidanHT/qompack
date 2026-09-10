# V5-VERIFY §4.5 disposition — `TestV5_EliminationThroughEveryFourSurfaces`

- **Identifier (retained):** `TestV5_EliminationThroughEveryFourSurfaces`
- **Current criterion (plan §4, row 4.5):** "SP-20/SP-13/SP-14 stale/uncertain/error state survives every
  surface and old-caller adapter."
- **Disposition:** `partial` — stale, uncertain and error state are asserted with real producers on
  `already_tried` (all three), `qompack status --json` (a counter for each of the three), and the
  `Answer.MCPResult` four-tuple adapter (all three: the stale tuple against the wire's stale
  rendering, the unavailable tuple against the wire's Phase-D rendering over the same severed log,
  the uncertain tuple against the wire's Phase-C rendering under the same `drop` switch);
  `qompack pin --eliminated` — a write — sees active and error (the blind-ledger envelope); the
  digest sees stale and uncertain. The ERROR state on the digest surface is observed NOT to survive
  (a blind ledger renders the frozen copy `[active]`) and is routed as a defect candidate rather than
  asserted either way — see "Unverified remainder". The historical four-source guarantee is retired
  (see the map below). Until the digest owner rules, this row must not be read as "error state
  survives every surface: passed".
- **Level and file:** e2e, `test/e2e/v5_x05_test.go`. The seam crosses processes on every side: real
  spawned daemons, a real `qompack mcp` stdio child, real hook subcommands and real slash-command
  subcommands through the built binary. Nothing is stubbed; no in-process rig is used.
- **Base:** `verify/v5` @ `87c0c1d`.

## What exists on this tree (producers)

| Historical §8.3 source | Production producer on this tree | Used here |
|---|---|---|
| #1 MCP `record_eliminated` | `internal/mcp` handler → `negknow.Maintainer.IngestMCP` (`Source=mcp`) | yes, over a real stdio child |
| #2 `/qompack:pin --eliminated` | `internal/commands` frontend → `runTool` → the daemon's `mcp` op → the SAME `record_eliminated` handler. `negknow.IngestPin` has **no production caller**; the record is stamped `Source=mcp` | yes, through the real binary |
| #3 heuristic (DAG pattern) | `negknow.Ledger.Observe` / `Detector.Scan` have **no production caller** (ledger.go: "source #3 is inert") | no producer — retired |
| #4 user statement | `negknow.IngestUserStatement` has **no production caller** | no producer — retired |

Read surfaces driven: `already_tried` over a real stdio child (SP-13); the rehydrated digest of
`SessionStart(source=compact)` built from the checkpoint the real PreCompact hook sealed (SP-10/SP-11);
`qompack status --json` (SP-14 — the ledger is opened with the daemon's own registry, so its counters
are the status document's counters); `negknow.Answer.MCPResult`, the four-tuple adapter that predates
`AlreadyTriedResult`, over the on-disk log with every daemon gone. SP-20 enters as the capture path:
the file versions the staleness flip is measured against are the observer's own file-version history,
appended by real PostToolUse hooks.

The flip itself is the production path: `negknow.Open`'s bounded refresh at the next open. The
scheduler plans `rebuild_bloom` only on a cold TTL (`internal/scheduler/evaluate.go planBackground`),
so no idle pass a test can trigger flips a record; a restart is what a real session walks.

## Subtests and what each asserts

1. `active_through_both_write_surfaces` — two hook reads seed file versions for
   `docker-compose.yml` and `package-lock.json`; a compact SessionStart opens the ledger — driven by
   `x5v5CompactUntilTagged` until its output carries the §8.5 injection tag with `seq=0`, so the open
   is an asserted precondition (a spooled compaction would otherwise let the next write race the
   drain's replay and reach a daemon with no ledger, whose non-error "not present in this build" body
   is now printed in the failure message); `record_eliminated` (stdio child, scope project, both deps)
   acknowledges `status:active` with two
   resolved dependencies and an `sha256:` evidence root; `already_tried` answers `active` with the exact
   stored reason and evidence for the exact phrasing AND for the synonym
   `"increasing the connection-pool timeouts"`; `qompack pin --eliminated --json` returns a schema-1
   envelope, `ok:true`, `tool:record_eliminated`, `status:active`, one resolved dependency; the
   slash-command write is visible on the MCP read surface; `qompack checkpoint` (PreCompact) seals
   `0001.json` carrying exactly the two records, both frozen `active`.
2. `stale_survives_every_read_surface` — a hook read captures the rewritten compose file; the next
   SessionStart is the compact one (after a PreCompact the host contract requires it — a `startup`
   here degrades the daemon to passive, which the first authoring run hit), it spawns the daemon, opens
   the ledger, flips the record and injects the digest with `seq=1` — when the spawning hook's own
   output is empty (its dial spooled behind `hookConnectDeadlineFloor`, see Timing) the compaction is
   re-driven under the same bounded helper until a digest crosses the hook boundary, and only that
   digest is held to `seq=1`; the digest renders the MCP record's
   line with `[stale: <negknow.StaleNote>]` (em dash included) and the slash-command record's line
   `[active]` (within-run control: its dependency never changed); `already_tried` answers `stale`
   with the exact reason, `note == negknow.StaleNote`, `stale_because` naming `docker-compose.yml`, the
   same evidence, `degraded:false`; the synonym also answers `stale`; the control record answers
   `active`; `qompack status --json` reports `primary.source=daemon` and counters
   `negknow.stale.flipped ≥ 1`, `negknow.query.stale ≥ 1`.
3. `uncertain_under_stale_response_drop` — `.qompack/config.json` set to
   `{"eliminations":{"staleResponse":"drop"}}`, daemon restarted; the compaction is driven to a tagged
   `seq=1` digest, which omits the stale record's line and keeps the active one; `already_tried`
   answers `uncertain` (never `absent`), with a reason and
   a recovery note, without disclosing the withheld record's reason or `stale_because`, `degraded:false`;
   the control record answers `active`; status counters show `negknow.query.uncertain ≥ 1`.
4. `negative_control_blind_ledger_is_unavailable_not_absent` — see below.
5. `old_caller_adapter_agrees_with_the_wire` — with every daemon gone, the real ledger is opened over
   the restored on-disk log with `Deps.Store == nil`. That nil is load-bearing: `negknow.Open`'s
   `refreshAtOpen` returns early without a store (ledger.go), so the stale state read here can only
   come from the `op:"stale"` control line subtest 2's daemon appended — a re-flip is impossible, and
   "the flip is durable in the log" is proven rather than plausible (see the sever below). Then, state
   by state:
   - **stale:** `Health{Records:2, Active:1, Stale:1}`; `Query` answers `AnswerStale`;
     `Answer.MCPResult()`'s `(state, reason, note, evidence)` equals what `already_tried` rendered in
     subtest 2; `All()` holds exactly the two records, `Source=mcp` on BOTH (the pin record's stamp is
     asserted exactly — see "Unverified remainder" for why that is a defect candidate rather than a
     mapped expectation), statuses stale/active.
   - **error:** the ledger is closed, `records/eliminations.jsonl` is moved aside and a directory put
     in its place (Phase D's switch), and the ledger is reopened with the same `Deps`; `Query` answers
     `AnswerUnavailable` and `MCPResult()` is `("unavailable", reason, note, "")` with reason and note
     non-empty and EQUAL to what `already_tried` rendered in Phase D — the wire and the adapter draw
     the same `Coverage` pair; evidence is empty. The log is restored (also on `t.Cleanup`).
   - **uncertain:** the ledger is reopened over the restored log with a copy of the resolved config
     whose `Eliminations.StaleResponse = "drop"` (the switch Phase C wrote to `.qompack/config.json`);
     `Health.Records == 2` again; `Query` answers `AnswerUncertain` and `MCPResult()` is
     `("uncertain", reason, note, "")` with reason and note non-empty, reason `≠` the withheld record's
     stored reason, and EQUAL to what `already_tried` rendered in Phase C; evidence is empty.

## Negative control and how it was proven

Subtest 4 severs the producer through a real degraded mode: with the daemon down,
`records/eliminations.jsonl` is moved aside and a DIRECTORY of the same name put in its place — the
portable way to make `negknow.Open` go blind (the same mechanism `test/e2e/v4_x06_test.go` uses in
process). The daemon is restarted and a compaction — driven until it carries a tagged `seq=1`
digest, so the open is known to have happened before anything reads — opens the blind ledger. That
ordering is itself part of the control's honesty: a daemon with NO ledger answers `already_tried`
with a non-error body that has no `state` at all, which would fail the `unavailable` assertion for
the wrong reason. Then:

- `already_tried` answers `unavailable`, `degraded:true`, with a reason and recovery note, empty
  `stale_because`, for BOTH records — asserted `≠ absent` and `≠ stale`;
- `qompack pin --eliminated --json` exits `1` with `ok:false`, an error kind that is not `usage`, a
  NON-EMPTY data member (required — `commands.callTool` keeps a failed tool's content on purpose, so an
  empty one would be the adapter dropping the tool's own error) whose `is_error` is true, and no
  `"status":"active"` anywhere — no fabricated acknowledgement;
- `qompack status --json` still exits 0 and carries `negknow.bloom.blind_mode ≥ 1`.

Proof that the assertion is not vacuous: the same surfaces that answered `stale`/`active` in subtests
1–3 answer `unavailable`/error once the log is unreadable, on the same project, in the same run. A
surface that rendered a blind ledger as `absent` — the retired three-way behaviour — fails subtest 4.
Subtest 5 repeats the same severance in-process against the four-tuple adapter, and adds the `drop`
switch for the uncertain tuple.

**Sever applied during authoring (reverted; `git status` shows only this row's files):** in
`internal/negknow/staleness.go` the `appendLine(l.f, logControl{Op: opStale, …})` call inside
`RefreshStaleness` was replaced by a no-op that still performed the in-memory flip. Result: subtests
1–4 PASS unchanged (every daemon-side surface reads the in-memory record), subtest 5 FAILS at
`Health.Stale == 1` with `{Records:2 Active:2 Stale:0}` — the on-disk log carries no flip and, with
`Deps.Store == nil`, nothing can re-flip it. Under the first version's `Deps.Store = st`, this sever
would have PASSED (`refreshAtOpen` re-flips against the store), which is exactly the gap the reviewer
named. Reverted with `git checkout -- internal/negknow/staleness.go`.

## Old-to-new assertion map

| Historical expectation (§4.5 text) | Disposition |
|---|---|
| Setup: `docker-compose.yml` and `package-lock.json` ingested into the store | **kept** — ingested through real PostToolUse Read hooks (SP-20 capture path), not `store.PutBytes` |
| Input (1) `record_eliminated` with target/approach/reason/scope project/`depends_on` both | **kept** — over a real `qompack mcp` child |
| Input (2) `qompack pin --eliminated --target src/db.ts --reason "pool is saturated" "disable pooling"` | **corrected** — the shipped flag table is `--target/--approach/--reason/--depends-on/--scope` and `--depends-on` is required (an elimination with no dependency can never go stale); driven through the real binary |
| Input (3) heuristic pattern `Edit → test:fail → revert → different Edit` through the DAG | **retired** — no production caller feeds `Observe`/`Detector.Scan`; asserting it would need a stub standing in for the producer |
| Input (4) user statement `"that didn't work"` through `observe prompt` | **retired** — `IngestUserStatement` has no production caller; the `observe prompt` hook does not reach it |
| `Health().Records == 4` with one record per `SourceKind` | **retired** and **corrected** to `Records:2, Active:1, Stale:1`, `Source=mcp` for the MCP record. The pin record is asserted `Source=mcp` too — the SHIPPED stamp, not the §8.3 one; that divergence is NOT accepted as corrected and is filed under "Unverified remainder" as a defect candidate for the SP-14 owner |
| `already_tried` returns `active` with the exact stored reason for record 1 | **kept** |
| … and for the synonym `"increasing the connection-pool timeouts"` | **kept** (classifies to `widen-pool-timeout` by `negknow.ApproachClass`) |
| Rewrite `docker-compose.yml`, re-ingest, run the negknow maintenance idle task → record 1 flips stale | **corrected** — re-ingested through a real hook; the flip runs on `negknow.Open`'s refresh at the next daemon open (the idle task is planned only on a cold TTL); asserted on the digest, on `already_tried`, on status counters and on the on-disk log |
| `already_tried` returns `stale` with the §8.3 note including the em dash | **kept** — `note == negknow.StaleNote`. The digest's `[stale: …]` tag is asserted against the SAME composed string, but on this tree the sentence is two independent literals — `negknow.StaleNote` and `internal/rehydrate/items.go`'s `staleStatusTag` — neither derived from the other; this assertion is what holds them equal (a drift on either side fails subtest 2). Routed to the SP-11 owner below |
| `tried.bloom` rebuilt from active records only; exactly one `.bak` | **retired here** — `TestE2E_EliminationLifecycle` (test/e2e/negknow_test.go) owns it; in this row the filter is rebuilt at every daemon open, so a backup count is a property of the number of restarts, not of the criterion |
| `qompack status --json` shows `data.sketches.bloom.records == 4, active == 3, stale == 1` | **retired** — no producer: `StatusExtra` is unbound in the shipped composition and the status document has no `sketches` section; **corrected** to the counters the ledger really emits into the daemon's registry (`negknow.stale.flipped`, `negknow.query.stale`, `negknow.query.uncertain`, `negknow.bloom.blind_mode`) |
| (new, from the current criterion) uncertain state on every surface | **added** — `staleResponse: drop` subtest |
| (new) error state on every surface and adapter | **added** — blind-ledger negative control, including the slash-command envelope and exit code |
| (new) old-caller adapter | **added** — `Answer.MCPResult()` vs the wire for ALL THREE states (stale vs subtest 2's rendering, unavailable vs Phase D's over the same severed log, uncertain vs Phase C's under the same `drop` switch); `commands.DecodeEnvelope` on every command output; the pin frontend's dispatch through the daemon's `mcp` op |

## Unverified remainder

- **Blind ledger vs the digest (observed, not asserted — potential defect for the SP-11/SP-13 owner).**
  With the ledger blind, `rehydrate.eliminationCandidates` cannot `Get` the checkpoint's frozen copy
  (`ErrNotFound`), so the frozen copy "stands alone" and the digest renders the MCP record with its
  seal-time status `[active]` while the real state on disk is stale and `already_tried` says
  `unavailable`. The row logs this (`blind-ledger digest carries the MCP record's line: true; the stale
  tag: false`, observed on every run) rather than asserting either way: the behaviour is documented in
  `items.go` as a deliberate degradation, but on this surface the error state does NOT survive — a
  reader of the digest is not told the ledger could not be consulted, and sees `[active]` on a record
  whose real state is stale and whose ledger state is unavailable.
  **Coordinator action:** route the blind-ledger digest rendering (`[active]` on a record whose ledger
  state is unavailable; `rehydrate.eliminationCandidates` falling back to the checkpoint's frozen copy
  on `ErrNotFound`) to the SP-11/SP-13 digest owner as a defect candidate, and hold 4.5 at PARTIAL on
  the digest surface until it is ruled. The other three read surfaces and the write adapter DO carry
  the error state and are asserted.
- **`qompack pin --eliminated` is stamped `Source=mcp` (defect candidate for the SP-14 owner).**
  Qompack.md §8.3 names the slash command as source #2 and `negknow.IngestPin` (ingest.go, documented
  as SP-14's door) stamps `SourceSlashCommand` — but `IngestPin` has NO production caller: the shipped
  path is `commands` → `runTool` → the daemon's `mcp` op → `record_eliminated` → `IngestMCP`, which
  stamps `SourceMCP`. Subtest 5 asserts the shipped stamp exactly so a later change is seen, not
  absorbed; the row does NOT accept it as the §8.3 behaviour. **Coordinator action:** route "dead
  `IngestPin`/`SourceSlashCommand`; pin records mis-stamped `mcp`" to the SP-14 owner.
- **Two copies of the §8.3 stale sentence (for the SP-11 owner).** `negknow.StaleNote` and
  `internal/rehydrate/items.go` `staleStatusTag` are independent literals whose equality only this
  row's subtest 2 enforces. The same shape exists once more on the uncertain path:
  `internal/mcp/handlers.go` `staleDroppedReason`/`staleDroppedRecovery` duplicate negknow's
  `reasonStaleDropped`/`recoveryStaleDropped` in a handler branch the ledger never lets it reach (the
  ledger already returns `AnswerUncertain` under `drop`, so `renderAnswer` takes the `Coverage` path;
  subtest 5's wire-vs-adapter equality on the uncertain tuple holds through THAT path). Observations,
  not defects: candidates for deriving one from the other.
- **`AnswerUncertain` from failed dependency coverage** (`RefreshStaleness` → `ChangedSince` error):
  no runtime switch reaches it through a spawned daemon; covered at unit level in `internal/negknow`.
- **Heuristic and user-statement sources** have no production producer (see the table); not assertable
  without a stub standing in for the producer, so not asserted.
- **Append-only assertion** (`testutil.Project.AssertAppendOnly`) is not applicable to this row: it
  seeds `checkpoints/0001.json` itself, and this row's PreCompact has already sealed `0001.json`. The
  v4 §4 rows do not carry it either.
- Timing — PRIMARY co-load suspect, the session-start dial budget: `internal/cli/hookclient.go`'s
  `hookConnectDeadlineFloor` gives session-start 250 ms to dial. On a loaded host a daemon that has just
  spawned (or is momentarily busy) may not have its accept re-posted inside that budget; the hook then
  spools the request and returns an EMPTY output, and the daemon's drain replays the spooled compaction
  with no reply to give. Both compaction-driven surfaces in this row (the ledger open in subtest 1, the
  digest in subtest 2) sit behind that dial. The first version of this row tolerated an empty subtest-1
  compaction and asserted subtest 2's spawning output directly; an independent reviewer's runs failed
  exactly there (see the run record). Now every compaction is driven by `x5v5CompactUntilTagged` —
  `assert.EventuallyWithT` over a `*testing.T`-free hook runner, bounded by
  `mcpE2EIndexBound`/`mcpE2EIndexTick`, with the spooled attempts logged — so a spooled hook is
  re-driven and named, and a daemon-side defect is what remains when the bound expires. If the row
  ever fails inside that helper under load, it is a co-load suspect first.
- Timing — secondary: the flip relies on `negknow.Open`'s 250 ms `openRefreshDeadline`; on a heavily
  co-loaded runner an expired refresh would leave the record active until a cold idle window. If
  subtest 2 ever fails on `stale` alone, treat it as a co-load suspect before anything else.

## Run command and result

```
cd <worktree> && go test ./test/e2e -run '^TestV5_EliminationThroughEveryFourSurfaces$' -count=1 -timeout=20m
```

Selection confirmed with `go test ./test/e2e -list 'TestV5_'` → `TestV5_EliminationThroughEveryFourSurfaces`.

**First version (commit `e1e474d` on `v5/x05`, landed as `783ad21`):** the author's two consecutive runs on `verify/v5` @ `87c0c1d`
(Windows 11, Go 1.26.6) were PASS 24.6 s and PASS 15.4 s. An independent reviewer's runs did NOT hold:
unloaded, sequential, 1 of 7 failed (run 2, 6.29 s, at the subtest-1 `record_eliminated` status
assertion — the body was the non-error "elimination ledger not present in this build", the daemon's
log showing the compaction's rehydration inside the drain window, i.e. a spooled session-start);
under a 12-core busy loop 2 of 3 failed (one at `e2eWaitDaemonUp`'s bound, one at subtest 2's
spawning compaction returning an empty context with `mode=full contract=null loud=`). Those runs are
what identified `hookConnectDeadlineFloor` as the primary suspect above.

**After the fixes (this commit):** two consecutive unloaded runs, PASS 27.4 s and PASS 16.3 s, all
five subtests passing both times, no spooled attempt logged. One additional run under a 22-core
self-terminating busy loop (the reviewer's load shape): PASS 85.7 s, in which subtest 2's spawning
compact SessionStart came back EMPTY with the very diagnostic the reviewer saw
(`mode=full contract=null loud=`) and the re-drive recovered a tagged `seq=1` digest on its first
retry — the failure mode reproduced and was absorbed by the fix rather than by luck. `gofmt -l ./test
./internal` clean; `go vet ./test/e2e ./test/integration` clean; `devtool lint
--only=nomagic,sleepcheck,testdeps,importgraph,runpatterns` all five PASS.

**Review-fix round (commit `cd985e1` after `05dd737` on `v5/x05`; landed as `7457c8a` after `9eb8100`):** the adversarial review found the adapter claim
overclaimed (MCPResult compared for stale only), the pin source hedged (`Contains` over two kinds),
the stale-tag comment misdescribing one exported copy, and the durability message unproven under
`Deps.Store = st`. All four are fixed in subtest 5 and in this document as described above. Two
consecutive unloaded runs: PASS 13.4 s and PASS 11.9 s, all five subtests both times, no spooled
attempt logged. The op:stale sever above was run once in between (FAIL at the intended assertion,
subtests 1–4 PASS) and reverted. `gofmt -l ./test ./internal` clean; `go vet ./test/e2e` clean;
`devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns` all five PASS.

No production code was changed.

Harness follow-up (pre-existing, not this row's): `mcpE2EStart` registers no `t.Cleanup`, so a
mid-subtest `require` failure leaves the `qompack mcp` child holding the day log open and the temp
directory undeletable on Windows; the reviewer hit it on the failed run. Worth a harness fix.
