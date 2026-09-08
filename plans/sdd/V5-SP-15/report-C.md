# SP-15 role C — analyzer diagnostic inputs

**Status: DONE.** All four missions landed on `wip/sp15-c-diagnostics`, branched from `6eac57c`.
Every gate in the brief was run and is reproduced verbatim below. Nothing outside role C's
exclusive write set was touched.

## 1. Files touched

| File | State | What it holds |
|---|---|---|
| `internal/analyzer/delta.go` | rewritten | the real `DeltaCheap` scorer; `stubScorer` is gone |
| `internal/analyzer/delta_test.go` | new | 10 cases + `RunDeltaScorerSuite` against a real store |
| `internal/analyzer/redundancy.go` | rewritten | the real `DetectRedundancy`, plus `DetectRedundancyWithConfig` |
| `internal/analyzer/redundancy_test.go` | new | 12 cases + `RunRedundancySuite` against a real store |
| `internal/analyzer/block.go` | new | `NewBlocks`, `NewCandidates`, `NewRelations`, `RepresentationCosts` |
| `internal/analyzer/block_test.go` | new | 15 cases, including the shared-file counterexample |
| `internal/analyzer/testdata/diagnostics/**` | new | three counterexample fixtures, each with a `provenance.json` |
| `plans/sdd/V5-SP-15/report-C.md` | new | this file |

`types.go` and `selector.go` were read and never written. No file outside `internal/analyzer/`
(except this report) changed.

## 2. Gates exercised, with actual output

All four brief commands, run from the worktree root at the final tree state:

```
== gofmt -l internal/analyzer ==
exit=0
== go build ./internal/analyzer/ ==
exit=0
== go vet ./internal/analyzer/... ==
exit=0
== go test ./internal/analyzer/... -count=1 ==
ok  	github.com/qompack/qompack/internal/analyzer	4.381s
ok  	github.com/qompack/qompack/internal/analyzer/analyzertest	0.995s
exit=0
```

`gofmt -l` printed nothing, which is the pass condition.

### Conformance blocks: the two this role owns now RUN

Before this branch, `analyzertest`'s `DeltaScorer/behaviour` and `Redundancy/behaviour` blocks
skipped with the Rule W-1 message. `go test ./internal/analyzer/analyzertest/ -count=1 -v`:

```
--- PASS: TestAnalyzerSuite_ShapePassesAgainstStub
    --- SKIP: TestAnalyzerSuite_ShapePassesAgainstStub/selector
        --- PASS: .../selector/analyzer.NewSelector-stub/shape
        --- PASS: .../selector/analyzer.NewSelector-stub/constructor
    --- PASS: TestAnalyzerSuite_ShapePassesAgainstStub/delta-scorer
        --- PASS: .../delta-scorer/analyzer.NewCheapScorer-stub/shape
        --- PASS: .../delta-scorer/analyzer.NewCheapScorer-stub/behaviour
    --- PASS: TestAnalyzerSuite_ShapePassesAgainstStub/redundancy
        --- PASS: .../redundancy/analyzer.DetectRedundancy-stub/shape
        --- PASS: .../redundancy/analyzer.DetectRedundancy-stub/behaviour
ok  	github.com/qompack/qompack/internal/analyzer/analyzertest	1.035s
```

The `selector` block still skips. That is role D's, and it is expected.

Both suites are additionally bound against a **real** `store.Store` from this role's own test
files (`TestAnalyzerConformance_DeltaScorerAgainstARealStore`,
`TestAnalyzerConformance_RedundancyAgainstARealStore`), because `analyzertest/suite_test.go` binds
them to a typed nil. No suite file was edited (contract section 9).

### Additional gates run beyond the brief

```
$ go test ./internal/analyzer/... -count=2 -race -timeout=10m
ok  	github.com/qompack/qompack/internal/analyzer	10.609s
ok  	github.com/qompack/qompack/internal/analyzer/analyzertest	2.026s
exit=0

$ go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/analyzer/...
golangci exit=0

$ go run ./tools/lint/nomagic ./internal/analyzer/...
nomagic exit=0

$ go run ./tools/devtool lint --only=importgraph,testdeps
importgraph: OK (65 package(s) checked)
PASS importgraph
testdeps: OK (67 package(s) checked)
PASS testdeps

$ go test ./test/guards/ -run TestAllStubsReturnNotImplemented -count=1
ok  	github.com/qompack/qompack/test/guards	0.580s
```

`golangci-lint` found two `unconvert` violations on the first run (redundant `core.UnixMilli(...)`
around an already-typed constant in `block_test.go`); both are fixed and the rerun is clean.

**`test/guards` does NOT need a registry change.** I expected the analyzer row to break — it
carries no `pureMethods`, so the walk asserts `ErrNotImplemented` — but
`assertMethodReportsNotImplemented` returns early on a nil error ("a documented zero value with no
error is permitted"), so a landed `Score` passes as-is. Main may still want to flip the row to
`allMethodsAreReal` for accuracy; it is not a blocker and I did not touch it.

`stubskips` was not run (it drives the whole test tree). The one Rule W-1 skip left in this package
is the selector block, and `plans/OWNERS.tsv:55` assigns `analyzer` to SP-15, not SP-01 — so it
classifies as a permitted skip, not a hard failure.

## 3. The exact wording used for each qualified claim

These are the sentences a later reader will be held to. Each is in the doc comment of the thing it
qualifies, not in a plan document.

### Delta-scorer — `cheapScorer` (delta.go)

> It is a BEHAVIOUR PROXY, and nothing more: the fraction of the observed continuation's paths,
> symbols and vocabulary that a block could have supplied.
>
> - It is NOT an unbiased KL estimate. Nothing here estimates a divergence between two
>   distributions; there is no distribution. Reporting a lexical overlap as a KL figure would put a
>   units label on it that no part of this computation earns.
> - It is NOT an action-distribution divergence derived from edit distance. No edit distance is
>   computed, no action distribution is modelled, and the plan's section 2 names both as diagnostics
>   rather than as measurements of either quantity.
> - It is NOT a correctness label. A block scoring 0 is not established to be useless and a block
>   scoring 1 is not established to be necessary: absence of lexical overlap is absence of THIS
>   evidence, not evidence of irrelevance, and section 2's "do not prove outside content irrelevant"
>   applies to the scorer exactly as it applies to the relation graph.

### Supersession — `DetectRedundancyWithConfig` (redundancy.go)

> **Superseded is EXACT, and may be asserted as such.** A tool use is reported superseded when
> either of two exact facts holds: (1) a strictly LATER tool use of this session names the same
> normalized path [...]; or (2) the store has already recorded `store.StatusSuperseded` on the
> record. Neither is an estimate, neither involves a sketch, and both are recomputable from the
> index by anyone who doubts the answer. The claim's SCOPE, however, is exactly the record set the
> scan observed [...] and it is a claim about the recorded history, not about the world: SP05-D1
> means an edit the observer never saw is an edit this cannot know about, so a record NOT listed
> here is not thereby established to be current.

### Near-duplicates — `DetectRedundancyWithConfig` (redundancy.go)

> **NearDups are CANDIDATES PENDING EXACT VERIFICATION.** A NearDups entry means one thing only:
> two MinHash signatures agreed on at least the configured fraction of their permutations. That is
> a similarity ESTIMATE over a sampled shingle set. It establishes neither equivalence nor an exact
> delta: [...] A caller that needs equivalence has to verify it — comparing content roots is exact
> and cheap — and a caller that needs a delta has to compute one. Neither may be inferred from an
> entry here, and dropping or rewriting a result on the strength of one is the failure this wording
> exists to prevent.

Plus, in the same comment: *"Disk deduplication is not context reduction. [...] a saving counted
here would be counted in the wrong currency."* And: *"It is READ-ONLY. It reports, and it marks,
drops and rewrites nothing. `store.MarkSuperseded` is the caller's call to make."*

### Relation edges — `Relations` (block.go)

> An edge is an ASSOCIATION. A shared_file edge between two items says they touched the same path;
> it does not establish that either is relevant to the other [...]
>
> And the converse is the more dangerous direction, so it is stated too: the ABSENCE of an edge
> between two items establishes NOTHING. The graph is built from what the observer saw, SP05-D1
> means it may have missed an edit, and section 6.4's thin slicing drops control edges by design —
> so "not connected in this graph" is not "unrelated", and no caller may treat content outside a
> slice as shown to be irrelevant.

### Representation costs — `RepresentationCosts` (block.go)

> Every number here is an UNCALIBRATED ESTIMATE and is named as one. SP-15's own blocker
> M5-U15-representation-overhead says so in the plan [...] these are estimates that must be allowed
> to overflow rather than figures a consumer may serialize against.

## 4. Counterexample fixtures (M5-G15-B)

`internal/analyzer/testdata/diagnostics/` — three directories, each with a `provenance.json` naming
the signal under test, the qualified claim it supports, the over-claim it prevents and the test
that asserts it. All three are **authored by hand for SP-15**, not captured and not renamed from
any existing benchmark corpus; each `provenance.json` says so explicitly so no older label is
displaced.

| Fixture | Demonstrates | Asserted by |
|---|---|---|
| `minhash-similarity-is-not-equivalence/` | `tlsclient_permissive.txt` and `tlsclient_hardened.txt` differ in one token (`AllowUnverifiedTLS = true` vs `false`). They land far above the shipped 0.9 threshold and are **not** equivalent: one skips TLS certificate verification for every outbound call. The test asserts the pair IS reported as candidates and, in the same breath, that their content roots differ — the exact check a caller must run instead. | `TestDetectRedundancy_NearDuplicatesAreCandidatesNotEquivalence` |
| `supersession-is-exact/` | three reads of `internal/auth/session.go`. The exact claim holds and is checked exactly — element for element, not as a count or a subset, because an exact claim is one that can be checked exactly. | `TestDetectRedundancy_SupersessionChainIsExact` |
| `shared-file-edge-is-not-relevance/` | two tool uses on `docs/CHANGELOG.md` — connection-pooling release notes, and a spelling fix in a 2019 entry. The shared_file edges exist, are preserved verbatim with `Dependence: false`, are excluded from `Requires`, and carry no relevance: the delta scorer, which knows nothing about the graph, ranks the typo fix below the notes against a pooling continuation. | `TestRelations_ASharedFileEdgeIsPreservedAndProvesNothing`, `TestRelations_AbsenceOfAnEdgeEstablishesNothing` |

## 5. Decisions I made (each needs a Main ack or an objection)

**C-1. `DetectRedundancyWithConfig` is a new exported function.** Section 5.12 freezes
`DetectRedundancy(ctx, store, session)` — `analyzertest` binds it and `test/guards` walks the
package — so the signature has nowhere to put a threshold. The alternative was to read
`config.Defaults()` silently, which makes `store.canonicalize.minhash.nearDupThreshold` a declared
key that this consumer ignores. So the three-argument entry point delegates to a four-argument one
with `config.Defaults()` and **says in its doc comment that it is doing so**. Additive; nothing
existing changes. Main may rename or reject it; if rejected, the fallback is the silent default plus
a documented gap.

**C-2. `core.ErrDegraded` is returned ALONGSIDE a valid result, in three places.** `NewBlocks` when
supersession coverage is incomplete, `NewCandidates` when there is no graph to compute `Requires`
from or the closure truncated, and `DetectRedundancy*` when a store read failed or the pair scan was
truncated. This is `internal/negknow`'s `depCoverage` rule: a failed comparison degrades COVERAGE,
not the answer. It matters because `Block.Superseded` and `Representation.Requires` are both
two-valued fields over three-valued questions — "not established" has nowhere to live except the
error. A caller that ignores the error gets a weaker claim, never a false one. `ErrDegraded` is one
of the four legal sentinels, and `analyzertest`'s `requireKnownError` accepts it.

**C-3. `Provenance.Qualification` defaults to `QualUncertain`, not to the zero value.**
`Qualification`'s zero value is `QualCurrent` (types.go fixes the enum order), so a plain map index
would read "absent from the caller's map" as "authoritative and applicable right now" — the false
already_tried section 12 rates High, minted by a missing map entry. `NewCandidates` uses the
comma-ok form. **This was a real bug in my first draft and
`TestNewCandidates_QualificationIsUncertainUntilSomeoneEstablishesIt` caught it.** Contract
section 1 already puts the negknow mapping in the daemon; this is the analyzer-side consequence.

**C-4. `RepArchiveOnly` is deliberately NOT among `Candidate.Reps`.** Contract section 3 defines
overflow as a mandatory item that fits at no representation "including at RepPointer", and appends
it to `Archive`. A zero-cost archive representation in `Reps` would make that case unreachable and
turn every overflow into a silent success. Archive-only is an OUTCOME, which is role D's to produce.
Guarded by `TestNewCandidates_ArchiveOnlyIsNotACandidateRepresentation`.

**C-5. The `Requires` closure follows exactly three edge kinds**: `EdgeProduces`, `EdgeConsumes`,
`EdgeExplains` — the three SP-07 D-3 gives a 1.00 multiplier because no information is lost across
the hop. The other five (`shared_file`, `shared_symbol`, `seq`, `control`, `supersedes`) are
association, ordering or eviction facts; following `supersedes` in particular would drag every
superseded read back in behind the read that replaced it. **Their exclusion is not a claim that
they are irrelevant** — `NewRelations` preserves every one of them, kind and direction intact, which
is how "preserve unknown DAG edges" is discharged. An edge kind this build does not recognize is
explicitly NOT assumed to be a dependency.

**C-6. Scoring weights are path 0.45, symbol 0.35, token 0.20.** The ordering follows
`internal/store/search.go`'s own stated rationale: a path is an exact statement of intent, a symbol
name is nearly as exact, a bag of words is evidence that accumulates and is mostly noise. They sum
to 1 and the result is still clamped (a decimal sum of 1 need not be a float64 sum of 1). 0.4 is in
section 11.6's forbidden set, which is why the path term is 0.45.

**C-7. A nil `store.Store` is never an error** — in `NewCheapScorer`, `DetectRedundancy` or
`NewBlocks`. `test/guards` and `analyzertest/suite_test.go` both construct one on purpose, and it is
the real state of a composition root that has not opened a store. The scorer falls back to the
block's own DAG key (a file node carries its path there), the detector reports an empty report, and
`NewBlocks` reports `ErrDegraded`. `Score` also repairs a nil `ctx` rather than panicking:
`store.OpenSpan` dereferences the context it is handed.

## 6. Open questions for Main

**Q1 — the redundancy scan's coverage ceiling is a real gap, and I could not close it from here.**
`store.Store` exposes no session-scoped enumeration: `ToolUsesByPath` needs a path, `Search` returns
K ranked hits rather than "all", and `Hit` does not carry `Session`. `internal/mcp`'s `timeline`
hit the identical wall and declined to invent one, calling it a Rule W-3 amendment against SP-06.
`scanToolUses` therefore runs two phases — discovery via one `Search` (which the store clamps to its
own cap), then completion via `ToolUsesByPath(path, 0)` for every path any hit named, which returns
that path's complete history. **Consequence:** supersession is exact *per covered path*, and a path
this session touched that no longer appears among the store's most recent hits is not covered at
all. The doc comment states this rather than hiding it. If wave 5 wants full session coverage, SP-06
needs a `ToolUsesBySession`; that is Main's amendment to make, not mine.

**Q2 — `test/guards`' analyzer row.** It passes unchanged (see section 2), but it now claims
analyzer is a stub when two thirds of it is not. Flipping it to `pureMethods: allMethodsAreReal` is
accurate and is Main's file; the walk would then call `Score` with zero-valued arguments, which
`TestCheapScorer_SurvivesZeroValuedArguments` already covers. Deferring is safe.

**Q3 — should `NewBlocks`/`NewCandidates`/`NewRelations` be in `block.go` at all, or does role D
want them nearer `greedy.go`?** The brief put block construction here and D owns `selector.go` and
`greedy.go`, so I stayed inside my write set. D consumes `[]Candidate`; nothing in `block.go`
imports or is imported by `selector.go`.

**Q4 — `RepresentationCosts` defaults (wrapper 24, handle 48, capsule share 0.25, capsule coverage
0.6, pointer coverage 0.15) are uncalibrated estimates**, and blocker M5-U15-representation-overhead
says they must stay estimates until the assembled model is calibrated against provider-reported
usage. They are named as estimates in the doc comment. Whoever does commit 6's integration should
replace them from the real estimator rather than inheriting them as though they were measured.

**Q5 — commit attribution.** The repo's `commit-msg` hook (`devtool check-commit-msg`) rejects
`Co-Authored-By`, `Signed-off-by`, "generated with" and the robot emoji outright, per sections
10/20. My session instructions asked for a `Co-Authored-By` trailer. I followed the repo rule and
omitted it rather than bypassing the hook.
