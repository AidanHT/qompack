// Package symbols implements the heuristic, language-agnostic symbol extractor of
// 00-ARCHITECTURE.md §5.22b: regex/brace-scanning over source text, with no parser dependency.
// One owner, four consumers: store.Query.Symbol (recall), dag's shared-symbol edges, analyzer's
// symbol-reference counting (a cheap Δ proxy), and the mcp symbol-aware span widener.
//
// symbols is foundation-only (00-ARCHITECTURE.md §3.2: symbols may import core, paths, config,
// logging, obs and nothing else); concretely it imports nothing beyond the standard library today.
//
// SP-01 ships the complete §5.22b type set as real declarations and every Extractor operation as a
// stub returning the documented zero value — Extract and References have no error return, and
// neither does Enclosing, so each reports the honest "found nothing" answer (nil, nil, and
// (Symbol{}, false) respectively) until SP-04 lands the real implementation.
package symbols
