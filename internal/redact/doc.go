// Package redact implements the §13 invariant 7 choke point (00-ARCHITECTURE.md §5.22a,
// `runtime.redact`): secrets must never reach objects/, so redaction is applied once, at
// store.Put/PutBytes, before canonicalization and chunking — never as an after-the-fact audit
// step.
//
// redact is foundation-only (00-ARCHITECTURE.md §3.2: redact may import core, paths, config,
// logging, obs and nothing else); concretely it imports config, logging and obs below.
//
// New returns the real redactor as of SP-06: the ten built-in rules of §5.22a, applied in a fixed
// order, plus whatever runtime.redact.patterns admits. Nop is a permanently honest no-op for tests
// that need "a Redactor" but are not testing redaction — production code always goes through New.
//
// Two properties are load-bearing rather than incidental, and both are asserted by
// FuzzRedactIdempotent as well as by the redacttest conformance suite:
//
//   - Idempotence. Redact(Redact(x)) == Redact(x). It holds because an existing placeholder is
//     recognized and reserved before any rule runs, so a placeholder can never be re-matched as a
//     fresh secret. This is why placeholderClose is a string and not a rune constant: appending
//     '»' to a []byte truncates U+00BB to the single byte 0xBB, which would make every placeholder
//     invalid UTF-8 and — worse — unmatchable by the very regex that enforces this property.
//   - Bounded growth. Chunk boundaries must stay stable between a redacted and an unredacted read
//     of the same file, so a replacement may never blow the input up. Every placeholder is at most
//     maxPlaceholderBytes, every accepted match spans at least minMatchBytes, and matches never
//     overlap — see Redact for the arithmetic.
//
// A rule is deliberately allowed to match an entire input. A tool result that IS nothing but a
// private key, an AWS key, or a JWT must still be fully redacted: refusing the match there would
// put the secret in objects/, which is the exact outcome §13 invariant 7 forbids. The related
// §5.22a property — that no rule is so greedy it swallows a whole document — is asserted over the
// corpus fixtures, where it is a statement about the rules rather than about short inputs.
package redact
