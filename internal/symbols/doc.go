// Package symbols implements the heuristic, language-agnostic symbol extractor of
// 00-ARCHITECTURE.md §5.22b: regex/brace-scanning over source text, with no parser dependency.
// One owner, four consumers: store.Query.Symbol (recall), dag's shared-symbol edges, analyzer's
// symbol-reference counting (a cheap Δ proxy), and the mcp symbol-aware span widener.
//
// symbols is foundation-only (00-ARCHITECTURE.md §3.2: symbols may import core, paths, config,
// logging, obs and nothing else); concretely it imports nothing beyond the standard library.
//
// The shape of the implementation, and why. dialect.go maps a file extension to an ordered list of
// anchored regexes plus a block style; extract.go walks the source one line at a time, letting the
// first matching rule claim each line; span.go turns a matched line into a byte span by matching
// braces (skipping strings, character literals and comments) or by comparing indentation;
// references.go counts whole identifier tokens in a single pass. Nothing here parses anything.
// That is the deliberate trade §5.22b names: four consumers across three waves need symbol spans on
// the hot path, and ten real grammars — one per language family — would cost more in build weight,
// latency and maintenance than the recall they would buy. These rules find most declarations in
// most files and never claim more; every consumer treats a miss as "no symbol here", not as an
// error, which is why no operation in this package has an error return.
//
// Bounds are contracts, not defensive habits. Every returned Symbol satisfies 0 <= Offset,
// 0 < Len, Offset+Len <= len(b) and Line >= 1, because consumers slice the caller's buffer with
// Offset and Len directly; Extract examines at most MaxExtractBytes and returns at most MaxSymbols;
// and results are sorted by Offset ascending then Len descending so a class always precedes its own
// methods. FuzzExtract and TestPropSpansWellFormed pin all of that against arbitrary bytes.
package symbols
