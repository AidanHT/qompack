package rehydrate

// standingInstruction is the sentence 00-ARCHITECTURE.md §5.15 quotes verbatim. It is a package
// constant so the one place it is written is the one place it can be changed.
const standingInstruction = "Before committing to an approach, call already_tried."

// StandingInstruction returns item 3's companion line, emitted verbatim (00-ARCHITECTURE.md
// §5.15, Qompack.md §8.7):
//
//	Before committing to an approach, call already_tried.
//
// It is fully specified by the architecture, so SP-01 implements it for real rather than stubbing
// it (§14.1 rule 3 of plans/V1-SP-01-foundation-toolchain-and-contracts.md). The reason it is a
// function returning a constant rather than an exported constant is that SP-11 and SP-13 land in
// the same wave and must agree on the exact bytes: a function gives them one call site to assert
// against, and gives a later subplan somewhere to put a configurable wording without changing
// either caller.
//
// One sentence, no trailing newline, no surrounding markup. It is emitted verbatim alongside the
// eliminations item, which is what makes negative knowledge actionable rather than merely
// present: an agent that is told what was tried but not told to ask will re-try it.
func StandingInstruction() string { return standingInstruction }
