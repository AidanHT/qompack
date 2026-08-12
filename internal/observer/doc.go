// Package observer implements the L0 hook semantics of 00-ARCHITECTURE.md §5.21: what actually
// happens when Claude Code fires PostToolUse, UserPromptSubmit, Stop/SubagentStop, SessionStart
// and SessionEnd. It is the hot path — Qompack.md §8.1 budgets the whole of PostToolUse at under
// 15 ms p99 — and it is the only place user intent is captured verbatim (G2.3) and subagent
// detail is captured before the host double-compresses it (G10.1). SP-08 owns the real
// implementation, and §5.21 is explicit that no other subplan writes code in this package.
//
// observer may import hookio, store, chunk, canon, sketch, dag, grammar, negknow and tokens, plus
// the foundation packages core, paths, config, logging and obs (00-ARCHITECTURE.md §3.2's
// observer allow-set). It may NOT import scheduler, checkpoint, rehydrate or contract — which is
// why the L3 hand-off is the plain Signals value of §5.21 rather than a scheduler type, and why
// SessionStart's `compact` branch is a delegation SP-11 installs rather than a direct call.
//
// SP-01 ships the §5.21 surface — the Observer interface (5 methods), Signals, ExtractSignals and
// Tombstone — as real declarations, plus an Options/New constructor pair so wave-0 composition
// roots can wire an Observer today. Every method is a stub returning core.ErrNotImplemented. The
// one exception is Tombstone: a fully specified, closed-form renderer that §14.1 rule 3 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md has SP-01 implement for real, because the
// addressable marker it produces (§8.1 item 2) is what makes a cleared result re-expandable
// rather than merely gone.
package observer
