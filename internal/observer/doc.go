// Package observer implements the L0 hook semantics of 00-ARCHITECTURE.md §5.21: what actually
// happens when Claude Code fires PostToolUse, UserPromptSubmit, Stop/SubagentStop, SessionStart
// and SessionEnd. It is the hot path — Qompack.md §8.1 budgets the whole of PostToolUse at under
// 15 ms p99 — and it is the only place user intent is captured verbatim (G2.3) and subagent detail
// is captured before the host double-compresses it (G10.1).
//
// §5.21 is explicit that no subplan other than SP-08 writes code in this package. That is a
// sole-writer rule, not a courtesy: every other layer reaches L0 through the Observer interface,
// the Signals value or the seams named below, so a change made from outside would be a change to
// a contract several subplans compile against.
//
// SP-08 lands the package across a sequence of commits. Everything below is the package's
// contract rather than a description of one commit's contents, and each rule is asserted by a test
// in the commit that lands it.
//
// # Responsibilities (Qompack.md §8.1)
//
//  1. Chunk and store the tool result, canonicalizing first so that timestamps, ANSI escapes,
//     PIDs and temp paths do not defeat exact-hash dedup.
//  2. Emit the addressable tombstone that replaces a cleared result, which is what closes G3.2 at
//     near-zero cost. See Tombstone.
//  3. Detect redundancy: a read that is a superset or near-duplicate of an earlier read of the
//     same path marks the earlier one superseded, and superseded reads evict first.
//  4. Record the DAG edges tool_use → tool_result → assistant_turn → next_tool_use, plus
//     shared-state edges keyed on file path and symbol name.
//  5. Update the Count-Min and HyperLogLog sketches. The Bloom filter is fed ONLY on explicit
//     negative-knowledge events (§8.3), which is why this package does not import negknow.
//  6. Append the tool symbol to the action grammar and warn on a high-multiplicity nonterminal,
//     which is a thrash loop the session cannot see itself in.
//  7. Capture every UserPromptSubmit verbatim and immutably. Unlike a summary, it is never
//     regenerated.
//  8. On SubagentStop, capture the subagent's returned summary and its tool-result hashes, so the
//     parent gains a retrieval path into detail it never held.
//
// # Imports
//
// The §3.2 allow-table permits observer to import hookio, store, chunk, canon, sketch, dag,
// grammar, negknow and tokens plus the foundation packages, but a permission is a ceiling rather
// than an instruction. This package declines chunk (it never chunks by hand — see decision 1) and
// negknow (§8.1 item 5 forbids L0 from touching the Bloom filter), and it never had symbols,
// scheduler, checkpoint or contract. The realized set is exactly core, paths, config, logging,
// obs, hookio, store, canon, sketch, dag, grammar and tokens, plus the standard library.
//
// The packages it may not import are what the seams are for: SymbolLister stands in for symbols,
// Signals and FeatureSample plus their callbacks stand in for scheduler and checkpoint, Mode
// stands in for contract, and Rehydrator is the seam SP-11 installs so SessionStart's compact
// branch is a delegation rather than a direct call.
//
// # Resolved decisions
//
// These are settled. None is open, and none may be revisited locally: a problem with one is an
// arch/ amendment (§0).
//
//  1. Pipeline order. §8.1 item 1 says canonicalize first; §5.22a says redaction happens at the
//     single choke point store.Put/PutBytes, BEFORE canonicalization and chunking. Both hold
//     because the observer never chunks, redacts or canonicalizes by hand: it calls
//     store.PutBytes with PutOptions{Tool, Path, Canon} and the store performs
//     redact → canonicalize → chunk internally. This package's contribution to O2 is per-tool
//     canonicalizer SELECTION, delivered as PutOptions.Tool and PutOptions.Path — which is what
//     canon.Registry.For dispatches on — and PutOptions.Canon.Strip. Its own ordering, after the
//     Put returns, is: index → file version → sketches → DAG → grammar → signals/features →
//     tombstone.
//
//  2. crlf is unconditional. Content entering the store is CRLF→LF normalized before chunking, so
//     the canon options always include the crlf class even when store.canonicalize.enabled is
//     false. The with/without-canonicalization measurement therefore compares crlf-only against
//     crlf plus the six configured classes, which is the honest A/B.
//
//  3. Verbatim capture (§8.1 item 7). The design names index/segments.jsonl; in this architecture
//     the segment log carries no per-turn payload field and W-3 forbids adding one. The
//     requirement is met with three durable artifacts, none ever regenerated from a summary: the
//     prompt bytes stored as a content-addressed object with no optional canonicalization class
//     and no MinHash; an append-only tool_use.jsonl entry under VerbatimPromptID; and a
//     KindUserPrompt DAG node enrolled in the open segment by an EdgeSequence running
//     userprompt:<turn> → segment:<id>, because members point INTO the segment. This is what
//     closes G2.3: the bytes are content-addressed, immutable and reachable by hash forever.
//
//  4. Turn accounting. Turn starts at 0. OnUserPrompt records at Turn then increments. OnToolUse
//     records at the current Turn without incrementing. OnStop increments in BOTH directions —
//     subagent=false because the assistant turn ended, subagent=true because the capture itself
//     occupies a turn slot and is recorded at the pre-increment Turn, exactly like a prompt. The
//     result is a monotone alternating user/assistant sequence in which every stored artifact
//     carries the turn it was observed at.
//
//  5. Node positions. A monotone per-session token counter gives every node its Pos as that
//     node's START position, and is then advanced by the node's own token count. Prompts, tool
//     results and subagent captures all advance it.
//
//  6. Ephemeral. A tool whose normalized name begins with mcp__qompack__ is a retrieval result:
//     it is recorded Ephemeral, excluded from supersession in both directions, and not fed to the
//     CMS, HLL or Misra-Gries, because retrieval is not exploration. It still gets DAG nodes and
//     a tombstone.
//
//  7. Error policy. No I/O failure ever escapes an Observer method. Every stage is wrapped by a
//     soft-failure helper that increments observer.err.<stage>, logs at Warn, and returns. The
//     methods return a non-nil error ONLY for ctx.Err(). Every method returns hookio.Empty()
//     unless it has a specific reason to emit: exactly two exist — OnUserPrompt may emit a thrash
//     warning, and only in ModeFull, and OnSessionStart returns verbatim whatever the Rehydrator
//     seam returns for source compact or clear. Counter bumps go through one nil-safe helper,
//     which is the only place in the package that names an obs method.
//
//  8. Nil tolerance. Grammar, Touch, Explore, Hot, Symbols, Rehydrate, Mode, OnSignals, OnFeatures
//     and Metrics may each be nil, and every call site guards. New reports an error only when
//     ProjectRoot is empty or Store, Graph, Log or Clock is nil.
//
//  9. Concurrency (two-level locking). The daemon's worker pool may deliver events for DIFFERENT
//     sessions concurrently, so every entry point is race-free under go test -race. The package
//     mutex guards ONLY the session map and the state-file write, and is held for the map
//     lookup/insert and released immediately. Each session state carries its own mutex, taken for
//     the remainder of the method, so all work for one session is serialized. Store, DAG, grammar
//     and sketch calls are made while holding the session lock: those packages own their internal
//     synchronization, and one session is inherently sequential in the host anyway. Never take
//     the package mutex while holding a session mutex; the shape is always map-lock → copy
//     pointer → map-unlock → session-lock.
//
//  10. Mode gates output, never writes. Under §12's degraded-passive mode L0 and L1 keep running:
//     observe, chunk, store, sketches, DAG, verbatim capture. Mode is therefore consulted in
//     exactly one place, the AdditionalContext emission on the prompt path, and never guards a
//     PutBytes, RecordToolUse, AddNode, AddEdge or sketch update.
//
//  11. Time. Every timestamp comes from the injected Clock, read once at the top of each entry
//     point after the ctx check and reused for the record, the DAG nodes and the feature sample,
//     so one hook firing has exactly one timestamp. time.Now never appears in this package.
//
//  12. MinHash options. §5.6 declares canon's field as MinHashOptions inside a package that
//     imports sketch, and §5.7 declares sketch.MinHashOptions, so the call site writes
//     sketch.MinHashOptions. A package-local alias on canon's side compiles unchanged; a
//     genuinely distinct struct would be a §5 divergence and an arch/ amendment request rather
//     than a local workaround.
package observer
