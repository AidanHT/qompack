# V5-VERIFY §4.5 disposition — `TestV5_EliminationThroughEveryFourSurfaces`

- **Identifier (retained):** `TestV5_EliminationThroughEveryFourSurfaces`
- **Current criterion (plan §4, row 4.5):** "SP-20/SP-13/SP-14 stale/uncertain/error state survives every
  surface and old-caller adapter."
- **Disposition:** `authored` — the criterion is asserted with real producers; the historical
  four-source guarantee is retired (see the map below) and one remainder is recorded unverified.
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
   `docker-compose.yml` and `package-lock.json`; a compact SessionStart opens the ledger;
   `record_eliminated` (stdio child, scope project, both deps) acknowledges `status:active` with two
   resolved dependencies and an `sha256:` evidence root; `already_tried` answers `active` with the exact
   stored reason and evidence for the exact phrasing AND for the synonym
   `"increasing the connection-pool timeouts"`; `qompack pin --eliminated --json` returns a schema-1
   envelope, `ok:true`, `tool:record_eliminated`, `status:active`, one resolved dependency; the
   slash-command write is visible on the MCP read surface; `qompack checkpoint` (PreCompact) seals
   `0001.json` carrying exactly the two records, both frozen `active`.
2. `stale_survives_every_read_surface` — a hook read captures the rewritten compose file; the next
   SessionStart is the compact one (after a PreCompact the host contract requires it — a `startup`
   here degrades the daemon to passive, which the first authoring run hit), it spawns the daemon, opens
   the ledger, flips the record and injects the digest with `seq=1`; the digest renders the MCP record's
   line with `[stale: <negknow.StaleNote>]` (em dash included) and the slash-command record's line
   `[active]` (within-run control: its dependency never changed); `already_tried` answers `stale`
   with the exact reason, `note == negknow.StaleNote`, `stale_because` naming `docker-compose.yml`, the
   same evidence, `degraded:false`; the synonym also answers `stale`; the control record answers
   `active`; `qompack status --json` reports `primary.source=daemon` and counters
   `negknow.stale.flipped ≥ 1`, `negknow.query.stale ≥ 1`.
3. `uncertain_under_stale_response_drop` — `.qompack/config.json` set to
   `{"eliminations":{"staleResponse":"drop"}}`, daemon restarted; the digest omits the stale record's
   line and keeps the active one; `already_tried` answers `uncertain` (never `absent`), with a reason and
   a recovery note, without disclosing the withheld record's reason or `stale_because`, `degraded:false`;
   the control record answers `active`; status counters show `negknow.query.uncertain ≥ 1`.
4. `negative_control_blind_ledger_is_unavailable_not_absent` — see below.
5. `old_caller_adapter_agrees_with_the_wire` — with every daemon gone, the real ledger is opened over
   the restored on-disk log: `Health{Records:2, Active:1, Stale:1}`; `Query` answers `AnswerStale`;
   `Answer.MCPResult()`'s `(state, reason, note, evidence)` equals what the stdio surface rendered in
   subtest 2 — the durable state on disk, the wire and the pre-struct adapter agree; `All()` holds
   exactly the two records with `Source=mcp` (MCP) and `Source ∈ {mcp, slash_command}` (pin; observed
   `mcp`, logged), statuses stale/active.

## Negative control and how it was proven

Subtest 4 severs the producer through a real degraded mode: with the daemon down,
`records/eliminations.jsonl` is moved aside and a DIRECTORY of the same name put in its place — the
portable way to make `negknow.Open` go blind (the same mechanism `test/e2e/v4_x06_test.go` uses in
process). The daemon is restarted and a compaction opens the blind ledger. Then:

- `already_tried` answers `unavailable`, `degraded:true`, with a reason and recovery note, empty
  `stale_because`, for BOTH records — asserted `≠ absent` and `≠ stale`;
- `qompack pin --eliminated --json` exits `1` with `ok:false`, an error kind that is not `usage`, a data
  member whose `is_error` is true, and no `"status":"active"` anywhere — no fabricated acknowledgement;
- `qompack status --json` still exits 0 and carries `negknow.bloom.blind_mode ≥ 1`.

Proof that the assertion is not vacuous: the same surfaces that answered `stale`/`active` in subtests
1–3 answer `unavailable`/error once the log is unreadable, on the same project, in the same run. A
surface that rendered a blind ledger as `absent` — the retired three-way behaviour — fails subtest 4.

## Old-to-new assertion map

| Historical expectation (§4.5 text) | Disposition |
|---|---|
| Setup: `docker-compose.yml` and `package-lock.json` ingested into the store | **kept** — ingested through real PostToolUse Read hooks (SP-20 capture path), not `store.PutBytes` |
| Input (1) `record_eliminated` with target/approach/reason/scope project/`depends_on` both | **kept** — over a real `qompack mcp` child |
| Input (2) `qompack pin --eliminated --target src/db.ts --reason "pool is saturated" "disable pooling"` | **corrected** — the shipped flag table is `--target/--approach/--reason/--depends-on/--scope` and `--depends-on` is required (an elimination with no dependency can never go stale); driven through the real binary |
| Input (3) heuristic pattern `Edit → test:fail → revert → different Edit` through the DAG | **retired** — no production caller feeds `Observe`/`Detector.Scan`; asserting it would need a stub standing in for the producer |
| Input (4) user statement `"that didn't work"` through `observe prompt` | **retired** — `IngestUserStatement` has no production caller; the `observe prompt` hook does not reach it |
| `Health().Records == 4` with one record per `SourceKind` | **retired** and **corrected** to `Records:2, Active:1, Stale:1` with `Source=mcp` for the MCP record; the pin record's source is observed (`mcp`) and mapped, not asserted as `slash_command` |
| `already_tried` returns `active` with the exact stored reason for record 1 | **kept** |
| … and for the synonym `"increasing the connection-pool timeouts"` | **kept** (classifies to `widen-pool-timeout` by `negknow.ApproachClass`) |
| Rewrite `docker-compose.yml`, re-ingest, run the negknow maintenance idle task → record 1 flips stale | **corrected** — re-ingested through a real hook; the flip runs on `negknow.Open`'s refresh at the next daemon open (the idle task is planned only on a cold TTL); asserted on the digest, on `already_tried`, on status counters and on the on-disk log |
| `already_tried` returns `stale` with the §8.3 note including the em dash | **kept** — `note == negknow.StaleNote`, and the digest's `[stale: …]` tag is asserted from the same exported constant |
| `tried.bloom` rebuilt from active records only; exactly one `.bak` | **retired here** — `TestE2E_EliminationLifecycle` (test/e2e/negknow_test.go) owns it; in this row the filter is rebuilt at every daemon open, so a backup count is a property of the number of restarts, not of the criterion |
| `qompack status --json` shows `data.sketches.bloom.records == 4, active == 3, stale == 1` | **retired** — no producer: `StatusExtra` is unbound in the shipped composition and the status document has no `sketches` section; **corrected** to the counters the ledger really emits into the daemon's registry (`negknow.stale.flipped`, `negknow.query.stale`, `negknow.query.uncertain`, `negknow.bloom.blind_mode`) |
| (new, from the current criterion) uncertain state on every surface | **added** — `staleResponse: drop` subtest |
| (new) error state on every surface and adapter | **added** — blind-ledger negative control, including the slash-command envelope and exit code |
| (new) old-caller adapter | **added** — `Answer.MCPResult()` vs the wire; `commands.DecodeEnvelope` on every command output; the pin frontend's dispatch through the daemon's `mcp` op |

## Unverified remainder

- **Blind ledger vs the digest (observed, not asserted — potential defect for the SP-11/SP-13 owner).**
  With the ledger blind, `rehydrate.eliminationCandidates` cannot `Get` the checkpoint's frozen copy
  (`ErrNotFound`), so the frozen copy "stands alone" and the digest renders the MCP record with its
  seal-time status `[active]` while the real state on disk is stale and `already_tried` says
  `unavailable`. The row logs this (`blind-ledger digest carries the MCP record's line: true; the stale
  tag: false`) rather than asserting either way: the behaviour is documented in `items.go` as a
  deliberate degradation, but on this surface the error state does NOT survive — a reader of the digest
  is not told the ledger could not be consulted. Recommend the coordinator route it to the digest owner.
- **`AnswerUncertain` from failed dependency coverage** (`RefreshStaleness` → `ChangedSince` error):
  no runtime switch reaches it through a spawned daemon; covered at unit level in `internal/negknow`.
- **Heuristic and user-statement sources** have no production producer (see the table); not assertable
  without a stub standing in for the producer, so not asserted.
- **Append-only assertion** (`testutil.Project.AssertAppendOnly`) is not applicable to this row: it
  seeds `checkpoints/0001.json` itself, and this row's PreCompact has already sealed `0001.json`. The
  v4 §4 rows do not carry it either.
- Timing: the flip relies on `negknow.Open`'s 250 ms `openRefreshDeadline`; on a heavily co-loaded
  runner an expired refresh would leave the record active until a cold idle window. If subtest 2 ever
  fails on `stale` alone, treat it as a co-load suspect before anything else.

## Run command and result

```
cd <worktree> && go test ./test/e2e -run '^TestV5_EliminationThroughEveryFourSurfaces$' -count=1 -timeout=20m
```

Selection confirmed with `go test ./test/e2e -list 'TestV5_'` → `TestV5_EliminationThroughEveryFourSurfaces`.

Two consecutive runs on `verify/v5` @ `87c0c1d` (Windows 11, Go 1.26.6): PASS 24.6 s and PASS 15.4 s,
all five subtests passing both times. `gofmt -l ./test ./internal` clean; pinned `gofumpt -l` clean on
the new file; `go vet ./test/e2e ./test/integration` clean; `devtool lint
--only=nomagic,sleepcheck,testdeps,importgraph,runpatterns` clean (recorded at commit time).

No production code was changed.
