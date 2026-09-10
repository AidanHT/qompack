# V5-VERIFY §4.12 disposition — `TestV5_WarmStartImprovesTheFirstCompactionOfTheNextSession`

**Disposition:** authored (partial by design — see "Unverified remainder").

## Identity

| Field | Value |
|---|---|
| Retained identifier | `TestV5_WarmStartImprovesTheFirstCompactionOfTheNextSession` |
| Current criterion (plan §4, row 4.12) | SP-16 scope/branch/version/expiry/unfinished-intent matrix; no guaranteed warm improvement. |
| Level | `test/integration` (in-process composition root) |
| File | `test/integration/v5_x12_test.go` |
| Base | `verify/v5` @ `87c0c1d` |

## Why integration rather than the historical e2e seam

The historical row ran two daemon processes (session A, idle-exit, session B) and observed
`state/warmstart.json`, a warm-seeded CMS estimate, cross-session `already_tried` carry and
`data.scheduler.breakdown` on the first evaluation through `qompack status --json`. On this tree
none of those producers exist: `internal/scheduler.WarmStart`, `internal/daemon.NewScopedReuse`
and `internal/daemon.ObserveScope` have **zero production call sites**, there is no
`state/warmstart.json` writer, no CMS `MergeFrom` call at session start, and both
`runtime.phase7.reuse.{scopedCandidates,warmPrior}` default `false`. The historical seam is
therefore unwritable, and the current criterion is exactly the gate those three types implement.
The row composes them the way a composition root would (the same shape as
`internal/daemon/reusable.go`'s doc comment describes), with every collaborator the production
type and nothing stubbed.

## Real producers exercised

- `internal/daemon.ObserveScope` over a hand-laid git on-disk layout (main checkout, three sibling
  worktrees with gitdir/commondir files, an unrelated repository, a directory with no `.git`).
  The layout is git's documented format, identical to what `internal/daemon/scope_test.go` lays
  out, so no `git` binary is on the path of the row.
- `internal/negknow.Open` / `Ledger.Record` / `Ledger.Active` / `Ledger.All` / `Ledger.Health`:
  session A records two eliminations to `records/eliminations.jsonl`, closes; session B reopens
  from disk with a fresh session id.
- `internal/daemon.NewScopedReuse` → `negknow.Applies` under a supplied `negknow.Grant`, the
  real `config.Phase7ReuseCfg` (both the loaded default and the switch turned on) and the real
  ledger's `Health().DependencyCoverage` watermark.
- `internal/scheduler.WarmStart` / `DefaultWarmStartPolicy` / `Blend`, with `Authorized`
  supplied from the real `Applies` verdict.
- `testutil.Project` (real config pipeline, real logger, `FakeClock` as the only clock) and
  `Project.AssertAppendOnly`.

## Subtests and what each asserts

| Subtest | Asserts |
|---|---|
| top-level setup | Session A's project-scoped record reaches session B at `ScopeProject`; A's session-scoped record does **not**; the carried record stays attributed to session A; the origin scope is fully observed (repository, worktree, branch, version, session). |
| `scope relation decides what a grant can cover` | Same worktree without a grant → withheld ("names no observed repository"); same worktree / same branch / same repository with a same-repository grant → allowed, with `Origin` and `Attribution` carried; unrelated repository with the same branch name and commit → denied even under the broadest grant the type can express; no `.git` → withheld, `RelationUnknown`; same session → allowed with no grant and no omissions. `ByRelation` is checked for every case. |
| `branch: …` | A same-branch grant covers a sibling worktree on the same branch and withholds a sibling on another branch ("broader than the authorized"). **Negative control:** truncating the same-branch worktree's `HEAD` to zero bytes makes `ObserveScope` report the branch unobserved ("HEAD is unreadable"), the relation degrades to same-repository, and the same grant now withholds the same tree; restoring `HEAD` makes it allowed again. |
| `version: …` | Same tip → `VersionSame`; sibling on another branch at another commit → `VersionMoved`; a branch with no tip yet → `VersionUnobserved` with the "no resolvable commit" omission. None of the three changes the verdict (all allowed under a same-repository grant): a moved version is a fact, freshness is `depends_on`'s question. |
| `expiry: …` | Live inside its declared shelf life → allowed, `DispositionLive`. **Negative control:** advancing the project `FakeClock` to the declared expiry (inclusive) turns the same candidate under a still-valid grant into a **denial** ("expired"). The grant's own expiry withholds ("authorization expired"); an undeclared candidate expiry withholds ("declares no expiry"); `deleted` / `superseded` / `corrected` each deny with the event named; an unrecognized lifecycle event withholds ("unrecognized lifecycle event"). |
| `unfinished intent: …` | `AuthorityHypothesis`, `AuthorityCandidateExtraction` and a blank authority are withheld ("unsettled") under a settled-work grant; with `AllowUnsettled` they are allowed but `Attribution` stays `AuthorityHypothesis` (never rises); a grant whose authority is `AuthorityToolObservation` authorizes nothing ("cannot authorize reuse"). |
| `the shipped switches are recorded as disabled, never as passed` | From the **real loaded config**: `scopedCandidates=false`, `warmPrior=false`. Under that config the gate reports `Enabled=false`, `Considered=1`, `Allowed=1` and offers **nothing**. Logged as "recorded as disabled, not as passed". The same log line records `sketches.cms.warmStartFromProject` (shipped `true`) as an **inert** key — no consumer outside `internal/config`, no CMS warm-seed producer — read from the loaded config and deliberately not asserted. |
| `warm start: …` | `Authorized` is taken from real `Applies` verdicts. Over the project's **real** history (1 session, 2 observations) an authorized warm start is refused ("below the … floor") with weight 0. Ample history with the denied verdict is refused ("not authorized"). Ample authorized history applies with `Weight <= policy.MaxWeight` and a label containing "warm prior"; `Blend` at n=0 returns the prior itself (a labeled candidate); for n=1..10 the answer lies strictly between the prior and the observed value, is strictly closer to the observed value than to the prior, and moves strictly closer to the observed value with every additional observation. `Blend`'s own `Weight/(Weight+n)` weighting is **not** re-derived — an equality against it would only echo `warmprior.go`'s formula, so it was dropped in favor of those formula-independent properties. A refused prior leaves the observed value unchanged. **No improvement is asserted anywhere.** |
| trailer | `records/eliminations.jsonl` still has exactly the two lines session A wrote; `Project.AssertAppendOnly` passes. |

## Negative controls and how they were proven

Three real switches, each paired with its restored path inside the same subtest so the control
rather than the fixture is what flips the verdict:

1. **Config switch (shipped default).** `runtime.phase7.reuse.scopedCandidates=false` from the
   real `config.Load` result: identical candidate, identical grant, `Allowed=1`, `Enabled=false`,
   zero candidates offered. Turning the switch on in `x12Enabled` is what every allowed case
   above depends on.
2. **Corrupt file.** Zero-byte `HEAD` in the same-branch worktree's git directory: branch
   unobserved → same-branch grant withholds; restored `HEAD` → allowed. Observed red-then-green
   in-run (the first authoring run failed on the omission wording and, because the restore was
   after the failing `require`, leaked the corruption into the version subtest — fixed by a
   `defer` restore; the leak itself was the proof the control severs the producer).
3. **Clock.** `FakeClock.Advance(x12Shelf)` to the declared expiry: allowed → denied ("expired")
   for the same candidate.

No production source edit was needed to turn the test red; `git diff` against the base is
empty apart from the two new files.

## Old-to-new assertion map

| Historical expectation (§4.12 at HEAD `7f92af5`) | Disposition |
|---|---|
| `state/warmstart.json` records `Ran:true` | **Retired.** No such file or writer exists; there is no warm-start idle task. |
| Live CMS estimate for a session-A hot path is `> 0` and within the decay band | **Retired.** No CMS merge at session start exists; `WarmStart` has no production caller. Replaced by the `Blend` bound: history is worth at most `MaxWeight` (≤ 0.5) of one present observation and fades with n. |
| `already_tried` returns `"active"` for a project-scope elimination in session B | **Corrected (kept in substance, in-process).** Session B's reopened ledger returns the project-scoped record at `ScopeProject` and does not return A's session-scoped one. Not driven through MCP because §4.12's subject is the SP-16 gate in front of that carry, not SP-09's query (covered by other rows). |
| `"stale"` if the elimination's `depends_on` file changed between sessions | **Corrected.** Version movement is reported as `VersionMoved` and does not change the verdict; freshness remains `RefreshStaleness`/`depends_on`'s question, per `negknow.Versions`' contract. Staleness flipping is owned by SP-09 rows. |
| `state/bocd.json` existed before the scheduler `Runtime` was constructed | **Retired.** `state/bocd.json` is real, but it is session-scoped, not warm state: `internal/daemon/scheduler_runtime.go` writes it in `Persist` (`:725`) and `BindSession` (`:317`) reloads it only for the bound session id — `internal/daemon/scheduler_state.go:304` (`doc.Session != r.session`) logs and discards another session's document. No cross-session prior-seeding path exists on this tree (nothing outside tests seeds a detector from a previous session's file), so the file's existence before construction says nothing about a warm start. |
| `status --json` shows `data.scheduler.breakdown` populated on the first evaluation | **Retired.** No warm-seeded scheduler path exists; the criterion explicitly makes no warm-improvement guarantee. |
| `sketches.cms.warmStartFromProject=false` yields a CMS estimate of 0 while the elimination is still carried | **Retired; key recorded as inert.** The key **exists** (`internal/config/config.go:151`, `CMSCfg.WarmStartFromProject`, json `warmStartFromProject` under `sketches.cms`), ships **`true`** (`internal/config/defaults.go:80`) and is documented in `docs/config-reference.md` — but it has no consumer outside `internal/config` on this tree (non-test grep over `internal/` and `cmd/`), and the CMS warm seed it names has no producer, so flipping it changes nothing and the historical `estimate == 0` assertion has no seam. The test reads it from the real loaded config and logs it as an inert, shipped-true key — recorded, not asserted, because neither value would evidence behavior. The switches that do gate reuse are `runtime.phase7.reuse.{scopedCandidates,warmPrior}`, both default false, asserted from the loaded config and recorded as disabled. Independence of the carry from any switch is asserted the other way round: the ledger carry works regardless of the gate switch, and the gate transcript (`Allowed=1`) is complete while the switch is off. |
| Session A: 200 tool uses on 6 hot files, 20 closed segments, daemon idle-exits; session B: fresh daemon | **Retired as setup.** No producer consumes the hot-file history across sessions. Replaced by two real ledger sessions over one project. |
| (new, from the current criterion) scope / branch / version / expiry / unfinished-intent matrix | **Added.** See the subtest table. |

## Unverified remainder

- **Warm improvement of the first compaction** is not asserted and cannot be on this tree: the
  criterion says none is guaranteed, `runtime.phase7.reuse.warmPrior` is `false`, and there is no
  production seam that would apply a prior even if it were true. Recorded as *disabled*, not
  *passed*. The M6-G16-D ablation (`internal/scheduler/warmprior_ablation_test.go`) remains the
  evidence of record for the policy's value.
- **Scoped reuse reaching a session** (candidates actually offered through a hook or MCP
  surface) is not assertable: `NewScopedReuse` has no production caller and
  `scopedCandidates` is `false`. Only the report-only stage is asserted.
- **Cross-process observation** (`qompack status --json`, a spawned daemon) is not exercised;
  no status field renders the reuse transcript yet.
- **`sketches.cms.warmStartFromProject` is a shipped-`true`, inert key.** It exists
  (`internal/config/config.go:151`), defaults to `true` (`internal/config/defaults.go:80`), is
  documented in `docs/config-reference.md`, and has no consumer outside `internal/config` on this
  tree; the CMS warm seed it describes has no producer. The test records its loaded value and
  asserts nothing about it. Whether the key should be wired, defaulted off, or removed is a
  question for the SP-16/config owners, not something this row can settle.
- `gofumpt` was not on PATH in the authoring environment; `gofmt -l` is clean and the
  coordinator's lint pass covers gofumpt.

## Corrections after adversarial review (round 2)

The first version of this record stated two false facts about the tree and carried two fixture
weaknesses. All four are fixed in the same branch; the test's assertions are unchanged in
strength (the retirement decisions all stand), only their honesty and independence improved.

1. **`sketches.cms.warmStartFromProject` "does not exist" — false.** It exists, ships `true`,
   is documented, and is inert (no consumer outside `internal/config`). The old-to-new map row
   and the unverified remainder now say so, and the shipped-switches subtest reads it from the
   loaded config and logs it as inert (recorded, not asserted; it is not asserted `false`
   because it ships `true`).
2. **"No BOCD warm state exists" — inaccurate reason.** `state/bocd.json` exists and is
   persisted/reloaded session-scoped by `internal/daemon/scheduler_runtime.go` with another
   session's document discarded (`scheduler_state.go:304`); what does not exist is a
   cross-session prior-seeding path. Retirement stands; reason corrected in the map row.
3. **`NoGit` fixture was a nonexistent path** while the record called it "a directory with no
   `.git`". The rig now creates the directory (`os.MkdirAll`), so the fixture matches its
   description; `ObserveScope`'s verdict (`RelationUnknown`, withheld) is unchanged.
4. **Blend equality was a formula echo.** The `InDelta` against `Weight/(Weight+n)·gap`
   re-derived `warmprior.go:229`'s own expression and was tautological. Replaced by three
   formula-independent properties (strictly between the inputs; closer to observed than to
   prior; strictly closer to observed with every additional observation) and the now-unused
   `x12FloatTolerance` constant was removed.

## Run command and result

```
cd <worktree>
go test ./test/integration -list 'TestV5_'
# TestV5_WarmStartImprovesTheFirstCompactionOfTheNextSession
go test ./test/integration -run '^TestV5_WarmStartImprovesTheFirstCompactionOfTheNextSession$' -count=1
# ok  	github.com/qompack/qompack/test/integration	0.401s   (run twice; both ok, ~0.2 s test time)
gofmt -l ./test ./internal                     # clean
go vet ./test/e2e ./test/integration           # ok
go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns   # exit 0
```

Not co-load sensitive: the row has no timing gates and its only clock is the `FakeClock`.
