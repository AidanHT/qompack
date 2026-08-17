// Package canon implements the canonicalizer registry of 00-ARCHITECTURE.md §5.6: a set of
// per-tool, per-class text canonicalizers (crlf, ansi, timestamps, durations, pids, addresses,
// tmpPaths, plus per-tool passes for bash, testrunner, grep, glob, fileread, webfetch and git)
// that strips volatile, low-signal content before chunking (Qompack.md §8.1, O2), together with
// the MinHash signature computed over the canonicalized result and the byte-exact Restore inverse.
//
// # Why this package exists
//
// Exact-hash deduplication is defeated by volatile substrings. Timestamps, ANSI escape codes,
// PIDs, memory addresses, temp-dir paths and run durations make every Bash and test-runner result
// unique even when it is semantically identical to the last one, so a content-addressed store with
// no canonicalization in front of it grows linearly in the number of test runs. Qompack.md §8.1:
// "Test and build output is the noisiest content class in a coding session; this is where the
// dedup ratio is won or lost." test/dedup measures that claim rather than asserting it.
//
// # Composition is a single pass, not a chain
//
// Registry.Run does not pipe the output of one canonicalizer into the next. Every canonicalizer
// also implements Matcher and reports the spans it would rewrite in the ORIGINAL input; Run sorts
// those spans once, resolves overlaps once, and rewrites once. Three properties fall out of that
// choice rather than having to be maintained by fourteen separate implementations:
//
//   - Delta coordinates are unambiguous. In a chain, each stage shifts the previous stage's
//     offsets, so a Delta means nothing without knowing how many stages ran after it.
//   - Restore is order-independent. It replays an ascending, non-overlapping list in one forward
//     pass and never needs to know which canonicalizer produced which span.
//   - Non-growth and Options.Strip gating are structural. The accept loop drops any match whose
//     token is longer than its span, and any match whose Class is not in play, in one place — so
//     a canonicalizer cannot bypass a user's store.canonicalize.strip setting, and §5.6's "no
//     canonicalizer ever grows its input" holds by construction.
//
// # What one pass cannot promise
//
// Idempotence holds by construction for every rewrite that SUBSTITUTES a token: tokens are
// bracketed in '<' and '>', which no rule admits, and the word-boundary invariant documented above
// reRule in generic.go keeps a substitution from creating a boundary a rule could key on. It does
// NOT hold by construction for a rewrite that DELETES, because deleting concatenates the deleted
// span's neighbours, and the joined text can match a rule that neither neighbour did — stripping
// the ESC out of "000\x1b0s" leaves "0000s", which is a duration the first pass could not see. No
// rule-level anchor closes that: the hazard is concatenation, not adjacency.
//
// Closing it properly means running composition to a fixed point and rebasing every later pass's
// Delta into ORIGINAL coordinates, since a second-pass match's preimage spans the bytes a
// first-pass deletion removed. That is a change to the Delta contract SP-06 stores against, so it
// is deliberately not made here. The consequences are bounded and cheap: Restore stays byte-exact
// at every step, so what a second-pass rewrite costs is a store lookup that misses, never content
// that cannot be recovered.
//
// It does not arise on real tool output — colourizers wrap whole tokens rather than splitting
// values, and TestCorpus_StructuralProperties asserts unconditional idempotence across the whole
// captured corpus. FuzzCanonicalize hard-fails any non-idempotence a deletion cannot explain, and
// TestKnownDeletionMediatedLimit pins the ones it can.
//
// # Import surface
//
// canon may additionally import sketch (00-ARCHITECTURE.md §3.2: canon's allow-set is sketch, plus
// foundation) because Result.Signature is a sketch.Signature, computed by sketch.MinHash over the
// canonicalized output. It deliberately does NOT import chunk: the with-versus-without dedup
// measurement that needs both lives in test/dedup, outside internal/, for exactly that reason.
package canon
