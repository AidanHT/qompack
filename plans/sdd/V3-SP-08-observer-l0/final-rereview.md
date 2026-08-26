# Final re-review — SP-08 fix wave (rulings R1-R5)

Reviewed delta: cd02d5ea..ef6a42c3 (one commit, `fix(observer): bridge prompt consumes edges and
load state before persist`, parent = previous head cd02d5ea, so nothing re-minted). 8 files,
+100/-13. Verdicts judged against the controller rulings as instructed.

## Verdicts

### F1 — dangling prompt edge (R1): ADDRESSED
`internal/observer/prompt.go` step 5b hand-emits `userprompt:T --consumes--> assistant:T+1`
(`Weight: edgeWeight` = 1, `Turn: st.Turn`) immediately after `dag.BuildUserPrompt`, exactly the
ruled edge; `internal/dag` and decision 4 untouched. The call-site comment names the decision-4 ×
BuildUserPrompt contradiction and the V3-VERIFY fold-in. `TestOnUserPrompt_DAGNodeAndSegmentEdge`
now asserts BOTH edges (same-turn dangler labelled D-6-inert, bridge labelled the §4.4 path); new
`TestOnUserPrompt_BackwardSliceReachesThePrompt` runs against a real `dag.Open` graph via the
existing `newRealGraphHarness`, drives prompt-then-tool-use, and asserts `userprompt:0` in the
slice thin and full.

Out-of-diff verification of the mechanism (named risk: does the bridge actually carry the slice?):
at ef6a42c3, `internal/dag/builders.go` BuildToolUse emits `assistant:T --> tooluse` where a
session's FIRST tool use is always `EdgeSequence` (turnLinkKind's no-predecessor rule), and
`internal/dag/traverse.go:220` shows thin slicing drops ONLY `EdgeControlOnly` — so the backward
walk tooluse ← assistant:1 ← userprompt:0 traverses in both thin and full modes. The path is
structurally sound, not just test-asserted.

### F2 — idle Persist wipes state file (R2): ADDRESSED
`persistState` now opens with `o.once.Do(o.loadState)` — verified it is the same `sync.Once`
field used by `session()` (`internal/observer/observer.go:308-309,410`; `state.go:175`).
Regression test `TestPersist_BeforeAnyEntryPointDoesNotWipeTheFile` matches the ruling: persists
real state (Turn 17 / PrefixTokens 4242), constructs a fresh observer over the same root, calls
Persist before any entry point, asserts the prior session survives in the file.

### F3 — same-session ordering (R3): ADDRESSED
No behaviour change, per ruling. `internal/observer/doc.go` decision 9 now states the caveat in
full strength: locking serializes one session's STATE not its ORDER; the shared worker ring has no
session affinity and the synchronous prompt path bypasses the queue, so one session's events can
be observed out of host order; Turn/PrefixTokens are monotone counters of events as OBSERVED
(SP05-D1's inherited constraint); the transport-level fix is V3-VERIFY's, beside SP05-D1. This is
at least as strong as the ruling's required sentence.

### F4 — three-platform bench-gate (R4): ADDRESSED
No code change, per ruling. `docs/adr/0008-observer-l0.md` measured section gains the bullet
naming green `bench-gate` on ubuntu/macos/windows at first push as a NAMED condition of Phase 1
closure, discharged post-push with the three p99 figures folded in, owned by V3-VERIFY.

### R5 minors: ADDRESSED
(a) `test/e2e/faultinject_test.go:440` reworded ("another subplan's unfinished surface");
`git grep TODO` on the file at ef6a42c3 returns empty (exit 1) — meaning preserved.
(b) Plan checklist: 'Exactly 7 commits' bullet records the ruled 8-plus-1; Bloom bullet points at
the AST-level test, negknow bullet at the import-graph lint + realized-import test, both with the
doc-comment distinction; "eight resolved decisions" corrected to twelve in both occurrences.

## Commit hygiene
Subject exactly as ruled; footer exactly `Refs: SP-08, §4.4, decision 4, SP05-D1`; body one
paragraph per ruling; single commit atop the previous head, no re-minting. Implementer's report
records check-commit-msg OK, trailer grep empty, `go test ./internal/observer/`, the race suite,
and `ci-local` exit 0, with the three covering tests run `-v` and passing — consistent with what
the diff shows.

## New breakage scan
None found. The bridge edge uses the package's established `edgeWeight` constant and `o.soft`
error channel; the once.Do is lock-free re-entry-safe (sync.Once); doc/plan/ADR edits are prose
only. The unchanged narrative line "Seven commits, each compiling and green" sits outside the
ruled checklist bullet and outside R5b's minimal scope — not a finding.

## Verdict
All four findings ADDRESSED per their rulings; no new Critical/Important issues. APPROVED.
