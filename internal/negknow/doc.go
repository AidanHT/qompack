// Package negknow implements the L2 negative-knowledge seam of 00-ARCHITECTURE.md §5.10: the
// evidence-linked elimination ledger, canonical descriptors, staleness detection against the
// store's file version history, and the tried.bloom membership cache the ledger rebuilds — never
// regenerates from a checkpoint or a summary — from active records only.
//
// # What it is for
//
// The failure this package removes is the one users report most: after compaction the agent
// re-attempts an approach it eliminated forty minutes ago, burns time, and eliminates it again.
// The mechanism is the canonical descriptor (normalized_path, symbol_or_null, approach_class,
// reason_hash) inserted into a Bloom filter over an append-only structured record log. A
// fixed-size bit array is not context, so the filter survives arbitrarily many compactions.
//
// # The five rules of Qompack.md §8.3, quoted
//
// Negative knowledge must be able to expire. An elimination is a CONDITIONAL fact — "widening the
// pool timeout doesn't work GIVEN pgbouncer 1.18" — and a false already_tried that blocks a
// now-viable approach inverts the feature from asset to liability. Standard Bloom filters cannot
// delete, so §8.3 fixes five rules:
//
//  1. "The structured eliminated[] records are the source of truth; the Bloom filter is only a
//     cache over them. This was implicitly true; it is now load-bearing."
//  2. "Every elimination carries depends_on: the hashes of the files or configs the elimination's
//     reason rests on (lockfiles, compose files, the file under test). The Observer already
//     tracks file-version history, so detecting a change to any dependency is a hash comparison
//     it performs anyway."
//  3. "When a dependency hash changes, the elimination flips to status: "stale". On the next idle
//     window, tried.bloom is rebuilt from active records only — cheap, because rebuild is a
//     linear pass over a few thousand structured entries."
//  4. "already_tried responses distinguish the cases: active returns the reason; stale returns
//     "previously eliminated, but the evidence has changed since — re-verification may be
//     warranted," which is strictly more useful to the agent than either a block or silence."
//  5. "scope: "session" | "project" controls cross-session carry-over: session-scoped
//     eliminations ("this test is flaky today") die with the session; project-scoped ones ("this
//     library fundamentally can't do X") persist and warm-start future sessions (§10 Phase 7)."
//
// The earlier claim that Bloom false positives are "the safe direction" is scoped by those rules:
// it holds only while evidence is current.
//
// # Invariant 3 — the bloom filter is a cache, never the source of truth
//
// 00-ARCHITECTURE.md §13 invariant 3, verbatim: "The bloom filter is a cache, never the source of
// truth (§8.3). Every membership answer is backed by a record lookup or explicitly flagged
// BloomOnly."
//
// Two mechanical consequences bind every file in this package. records/eliminations.jsonl is
// written only through paths.AppendOnly, and sketches/tried.bloom is replaced only by a rebuild
// whose input is that log filtered to status:"active" — never a checkpoint, never a summary,
// never context (§3.3). And no code path may return an answer of active or stale without a
// materialized record: a bloom hit alone is either flagged BloomOnly or is not an answer.
//
// # Keys
//
// Every record contributes exactly two bloom keys. Descriptor.Key is the identity key — its byte
// layout is frozen by a golden and re-keys every entry already on disk if it moves — and
// Descriptor.MatchKey is the reason-independent key, which is what makes already_tried answerable
// when the caller has no reason to offer. Effective bloom capacity consumption is therefore
// 2 x records.
//
// # Import allow-set
//
// negknow may import ONLY sketch, store and dag, plus the foundation packages core, paths,
// config, logging and obs (00-ARCHITECTURE.md §3.2's negknow allow-set). This is the mirror image
// of store's own allow-set: store must not import negknow, so Store.ChangedSince takes []core.Dep
// rather than []negknow.Dep, and negknow.Dep is simply an alias of core.Dep (§4) so a caller
// holding either type is holding the same value.
package negknow
