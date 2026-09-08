# SP-16 M6 — gate evidence and dispositions

Implementation record for [V5-SP-16-phase7-refinements.md](V5-SP-16-phase7-refinements.md).
Branch `feat/sp16-phase7-refinements`, cut from `develop` at 7c735ac.

Every number here was produced by a command in this document on the branch's tip. Nothing in it is
a projection, and the two things that did not get done say so in the same words they would have
used if they had.

## 1. What landed, and what did not

Six of the plan's seven commit identifiers landed. Commit 3 landed as two commits, as the plan's
own commit table directs ("D and C land as separate commits under this identifier rather than
sharing a file").

| Commit | Subject | State |
|---|---|---|
| 1 | `test(refinement): specify scoped reuse and setting compatibility` | landed |
| 2 | `feat(refinement): qualify warm priors and retrieval triggers` | landed |
| 3 | `fix(store): qualify per-segment filter coverage and generation` | landed |
| 3 | `fix(store): record demand without letting frequency stand in for usefulness` | landed |
| 4 | `feat(daemon): apply scope-aware reusable candidates` | landed |
| 5 | `feat(checkpoint): promote only future compatible representations` | **not started — blocked, see below** |
| 6 | `fix(refinement): bound phase-7 serialization and maintenance` | landed |
| 7 | `test(refinement): evaluate reuse and optional policies` | landed (this document plus the ablation) |

### Commit 5 is blocked, by the plan's own edge

The subplan's subagent strategy says: "`internal/checkpoint` and the rehydration files belong to
SP-15's owner until SP-15's handoff, and SP-16 must not edit them concurrently: only E, the slice
that integrates through the SP-11/SP-15 contracts, waits on that edge."

SP-15 had not been started when commits 1 through 4 and 6 were written, and it has since appeared
on `feat/sp15-analyzer-selection-and-grammar` — 17 commits off the same base, `develop@7c735ac`,
**unpushed, unmerged and with its mandatory independent adversarial review still open**. It
supplies `internal/rehydrate/selection.go`, which is the representation-selection contract commit 5
would consume; it touches no file under `internal/checkpoint`.

That is a branch, not a handoff. The subplan's edge is "SP-15's consumer handoff still precedes E's
shared integration", and building commit 5 on an unmerged, unreviewed sibling branch would mean
this branch could no longer be merged to `develop` on its own — it would carry SP-15's 17 commits
with it, and the two branches already conflict on `testdata/golden/config/schema.json`, which both
regenerated. Commit 5 therefore remains not attempted, pending an explicit decision to integrate
the two branches.

Every other prerequisite the subplan names is merged into `develop`: SP-19 and SP-20 arrived
through the V4 corrective integration (e194abf) rather than under their own subject lines, which is
why a commit-message search for them finds nothing, and SP-10, SP-11, SP-12 and SP-13 are all
present.

So commit 5 has no ACCEPTED contract to integrate through, and §3's "tune complete records under
SP-11/SP-15 serialized budgets" has no accepted serialized budget to tune under. It was not
attempted, no checkpoint or rehydration file was touched, and **M6-G16-C has no evidence**.

Development of the rest proceeded concurrently on the ledger's own rule: "SP-15→SP-16→SP-14 govern
integration order, not development order; under disjoint ownership their development runs
concurrently."

## 2. Gate dispositions

| Gate | Disposition | Evidence |
|---|---|---|
| M6-G16-A | **met** | scope observation and the applicability transcript, §3 below |
| M6-G16-B | **met** | bounded attempts, reminders and demand telemetry, §4 below |
| M6-G16-C | **blocked** | SP-15 exists only as an unmerged, unreviewed branch, which is not the handoff the plan's edge names; nothing was measured and nothing is claimed |
| M6-G16-D | **measured; disposition is DISABLED** | the ablation, §5 below |
| M6-G16-E | **met** | filter coverage and maintenance recovery, §6 below |

Every phase-7 switch ships `false` and every one is refused by `Validate` while its gate is
pending, so none of the above changes what a build does. §7 lists the switches.

## 3. M6-G16-A — scope, authorization and applicability

Run:

```
go test ./internal/negknow/ -run 'TestApplies|TestGrant|TestRelate|TestRelation|TestVersions|TestReuseScope|TestNewRepositoryID|TestNewWorktreeID|TestLifecycle|TestDisposition|TestReusability' -count=1
go test ./internal/daemon/ -run 'TestObserveScope|TestScopedReuse' -count=1
```

What the transcript establishes.

*Session, project, worktree, branch and version changes cannot silently cross scope.* Identity is
minted from observed evidence, not from a path: two worktrees of one repository resolve through
`.git`'s `gitdir:` pointer and its `commondir` to the same `RepositoryID` and different
`WorktreeID`s, and two unrelated repositories sharing a branch name and a commit relate as
`RelationUnrelated`. An unobserved repository relates to nothing, including itself — the zero
`ReuseScope` is `RelationUnknown`, not `RelationSameSession`.

*Denied access is never absence.* `Applies` runs the access check first: any `core.EvidenceOutcome`
other than `OutcomeOK` denies the reuse and reports the outcome, with no further omissions, so a
failed read cannot be read as a missing elimination.

*Expiry, deletion, supersession and correction invalidate reuse*, and a correction outranks a
passed expiry so the transcript says why the claim must not come back rather than only that it got
old. A lifecycle event this build cannot interpret is uncertain, never live.

*Another session's unfinished intent cannot become current intent.* Cross-scope reuse of
`AuthorityHypothesis` or `AuthorityCandidateExtraction` needs a grant that says so in as many
words, and reuse never raises a claim's authority: an unrecognised authority decays to
`AuthorityHypothesis` rather than to the caller's own.

*Branch, worktree and child failures.* A detached HEAD reports no branch rather than `"HEAD"`, so
two unrelated detached checkouts do not relate as same-branch. A worktree whose `commondir` is
unreadable looks like its own repository, refusing reuse rather than merging on a guess. A missing
`.git`, an unreadable HEAD, a branch with no commit and a dangling `gitdir:` each produce a named
omission and a usable scope; none is an error.

*Cross-repository reuse is not a permission this system can express.* `Relation.Narrower` refuses
`RelationUnrelated` as both operand and bound, so the broadest grant a user correction can issue
still denies it.

## 4. M6-G16-B — bounded retrieval, burden and usefulness

Run:

```
go test ./internal/mcp/ -run 'TestAttempt|TestReminder|TestTriggerKind' -count=1
go test ./internal/store/ -run 'TestDemand' -count=1
```

*Not tried and tried-and-failed are different answers.* `AttemptNone` is the only status meaning
untried; every recorded attempt, including one refused past the cap, reports otherwise, with the
omission that explains it.

*Bounded attempts with missing telemetry visible.* Attempts are capped per key; exhaustion reports
`AttemptExhausted`, a statement about the search rather than about the thing sought. When the
distinct-key tracker overflows, unseen keys report `AttemptUnknown` and the report carries
`TrackingOverflowed`, so the counts are known to undercount rather than being quoted as complete.

*Reminder volume is bounded and cannot amplify itself.* Reminders are capped per session, never
repeat for one key, and never fire for content Qompack itself produced — that check runs first, so
the loop is cut before any budget is spent. Every suppression is counted by reason.

*Usefulness is distinct from frequency.* `Demand` keeps requests, distinct touches, useful
outcomes, failed attempts and telemetry gaps as five separate counters and offers no total, no
score and no ranking method. `Usefulness()` returns `(rate, known)` and `known` is false when
nobody instrumented the outcome, so an uninstrumented key cannot be read as a measured zero. Its
denominator is outcomes rather than requests; `Instrumented()` is what says how much of the traffic
the rate covers. Recovery cost is reported as the maximum observed, never the mean, so unmeasured
records cannot average an expensive span down to cheap.

## 5. M6-G16-D — the optional-policy ablation

Run:

```
go test ./internal/scheduler/ -run 'TestWarmPriorAblation' -count=1 -v
```

**Declared objective.** Predict a project's true per-turn cost at the start of a session and as
observations arrive. Error measure: mean absolute error.

**Declared baseline.** One global default for every project until the session has an observation of
its own, then that session's running mean — that is, what the system does today with `warmPrior`
off. The global default is set to the mean of the θ distribution, which is the baseline at its
strongest; a real deployment would have to guess it.

**Setup.** 20 000 held-out projects, fixed seed, θ spread 0.60, observation noise 0.35, prior noise
0.55, horizon 12. Every parameter is a constant in `warmprior_ablation_test.go`, so the numbers
below are reproducible.

| n | baseline MAE | warm MAE | improvement |
|---|---|---|---|
| 0 | 0.4761 | 0.4405 | +7.5% |
| 1 | 0.2773 | 0.2387 | +13.9% |
| 2 | 0.1975 | 0.1824 | +7.6% |
| 4 | 0.1392 | 0.1337 | +4.0% |
| 8 | 0.0983 | 0.0963 | +2.1% |
| 12 | 0.0810 | 0.0798 | +1.5% |

**Uncertainty, reported.** The first run of this ablation used 200 projects and reported −9.7% at
n=0 — the wrong sign — because the standard error on each mean was about 0.026 against an effect of
roughly 0.04. The sample was raised to 20 000 for that reason, and the constant carries the note.
No claim here rests on a difference smaller than the noise at the sample used.

**Resource and privacy cost.** The mechanism reads only this project's own earlier sessions, under
the same scope and authorization gate as every other reuse (§3), and calls no external service.
`sketch.CMS.MergeFrom` and `Scale` already exist, so enabling it adds one merge per session start
and no new storage.

**Disposition: DISABLED.** `runtime.phase7.reuse.warmPrior` stays `false` and its gate stays
pending. The measured benefit is real but small and confined to the first turn or two of a session,
and this is a simulation against a synthetic θ, not a measurement against Qompack's own replay
corpus. §4's "lack of benefit leaves them disabled" is not quite the situation — there is a
benefit — but the evidence does not yet support enabling a cross-session mechanism on it. What
would change the disposition is the same ablation against the SP-02 replay corpus, which needs the
per-project history this branch does not yet record.

**The disabled arm is exactly the baseline.** `TestWarmPriorAblation_ARefusedPriorIsExactlyTheBaseline`
and `TestWarmPriorAblation_ThinEvidenceIsAlsoExactlyTheBaseline` assert that a refused prior — for
lack of authorization, thin evidence or age — moves no prediction by any amount at any horizon.
Turning `warmPrior` off is not a degraded mode; it is the unmodified system.

## 6. M6-G16-E — filters, maintenance and recovery

Run:

```
go test ./internal/store/ -run 'TestSegmentFilter|TestWriteAndReadSegmentFilter|TestReadSegmentFilter|TestPublishFilter|TestCompactDemandLog|TestSweepSegmentFilters' -count=1
```

*Stale and incomplete filters preserve valid references.* A filter answers `FilterMiss` — the only
verdict that skips work — only with complete coverage at the caller's current generation.
Everything else is `FilterBypass`, which is also the zero verdict, so a verdict nobody computed
costs a scan rather than skipping one. A positive is always `FilterMaybe`; there is no verdict
meaning "present".

*Bounded capacity is stated, not papered over.* A segment past the key cap is published
**incomplete**, as are one whose key source failed and one whose build was cancelled, each with its
own reason. Positives from a partial filter still narrow a search; its negatives are bypassed. No
false-positive-rate promise is made that a fixed filter cannot keep.

*Coverage and generation are published atomically with the index.* They live in the filter file —
one JSON header line, then the bloom's own encoding — written atomically, with the segment-log
record appended only afterwards, so a reader never meets a reference to a file that is not there.
An unreadable file is an error and never decodes to an empty filter, which would answer "definitely
not present" to everything.

*Cancellation, quotas and crash recovery.* Demand-log compaction stages, verifies by reading back,
and swaps in one rename. A cancellation, a read bound or a failed verification leaves the original
log exactly where it was; there is no state in which some records are gone and the pass has not
finished. A staging file from a crashed process is removed by the next pass, from idle or from any
later session — SessionEnd is not the recovery path.

*Explicit expiry, and no arbitrary truncation.* A record survives compaction whole or is dropped
whole, and every drop leaves an explicit gap record, so a later aggregate reports the loss as
missing telemetry rather than as an absence of demand.

*The sweep cannot mistake a failure for an instruction.* `SweepSegmentFilters` requires an explicit
keep set, including when empty: reading it from a `SegmentLog` would make a transient load failure
indistinguishable from "delete every filter". It removes only names it recognises.

## 7. Independent switches and the rollback drill

Every switch below defaults `false`, is gated in `internal/config/migration.go`, and is refused by
`Validate` while its gate is pending — `Load` then restores the default and warns. The only way to
enable one is the reviewed commit that flips its gate, never a config edit.

| Switch | Gate | Disposition |
|---|---|---|
| `runtime.phase7.reuse.scopedCandidates` | M6-G16-A | pending; report-only stage is available with the switch off |
| `runtime.phase7.reuse.warmPrior` | M6-G16-D | pending; **disposition is disabled**, see §5 |
| `runtime.phase7.retrieval.reminders` | M6-G16-B | pending |
| `runtime.phase7.retrieval.demandPromotion` | M6-G16-C | pending; **blocked**, no evidence |
| `runtime.phase7.filters.segmentBloom` | M6-G16-E | pending |

Each is independent: turning one off has no effect on the others, and none of them gates a shipped
capability. `runtime.migration.experiments.enabled` remains the SP-15/SP-16 master gate and is also
pending.

**Rollback.** There is nothing to roll back in a shipped build, because no switch is on. For a
build that had enabled one:

1. Setting the switch `false` restores the previous behaviour with no migration. The scoped-reuse
   gate returns no candidates and its report says `Enabled: false`; the reminder budget emits
   nothing; a refused warm prior is byte-identical to the baseline (§5).
2. Artifacts are additive and are read only through an explicit reference. A segment filter is
   reached only through `Segment.BloomRef`, so an unreferenced file is inert; `SweepSegmentFilters`
   removes it when asked.
3. `state/demand.jsonl` is append-only and is created on first write, so a project that never
   enabled the feature has no file, and one that did can have it deleted with no effect on any
   other record.
4. A `runtime.phase7` block written by a newer build is reset WHOLESALE by
   `applyVersionedSections`, independently of `runtime.migration`, so a rolled-back binary reads a
   forward-written config with every unknown refinement off rather than applying the half of it
   that happens to parse.

**Compatible-state readers.** No frozen wire format changed. `Segment`'s json tags, `Record`'s and
the descriptor's are untouched, and the two contract fixtures that pin them byte-for-byte still
pass. `runtime.phase7` is additive; an older binary drops it by `deepMerge`'s unknown-key rule.

## 8. What is not claimed

- **M6-G16-C has no evidence.** No promotion path was built, no complete-record budget was tuned,
  and nothing is asserted about future Qompack delivery. `runtime.phase7.retrieval.demandPromotion`
  exists as a gated-off switch and consumes nothing. SP-15's branch appearing during this work does
  not change that: the plan's edge is a handoff, and an unmerged branch with an open review is not
  one.
- **No token saving is claimed.** Nothing here was measured against a cost baseline, and correctness
  and recoverability were the only objectives.
- **The M6-G16-D result is a simulation.** It is against a synthetic θ, not against Qompack's replay
  corpus, and §5 says what would change the disposition.
- **The four M6-U16 blockers remain open.** Applicability coverage has a mechanism and a transcript
  but no dependency/scope extraction matrix over real sessions; usefulness telemetry has its
  instrumentation but no held-out report; the authorization matrix is covered by tests and not by a
  reviewed denial matrix; optional-policy value is measured and disposed disabled.
- **No independent review has run.** The subplan requires one, from a reviewer that did not write
  the work, and this document is written by the implementer.
