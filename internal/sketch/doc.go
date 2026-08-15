// Package sketch implements the permanent-memory probabilistic sketches of 00-ARCHITECTURE.md
// §5.7: the Bloom filter behind tried.bloom (Appendix A), the Count-Min sketch behind touch.cms,
// the HyperLogLog behind explore.hll, the Misra-Gries top-k counter, and MinHash near-duplicate
// signatures over canonicalized content.
//
// # Why these structures exist (§6.2)
//
// Qompack's memory is meant to outlive any one session, and a session's worth of observations is
// far larger than what may be kept verbatim. A sketch trades exactness for a bounded, constant
// footprint: a few kilobytes answers "has this been tried?", "how often is this touched?" and "how
// many distinct paths has this session explored?" for a corpus that could never be held in full.
// The trade is only sound if the answers survive a restart byte for byte, which is why every
// structure here is versioned, checksummed and self-describing rather than gob-encoded — a sketch
// that cannot be re-read is worse than no sketch, because the system will have already stopped
// recording what it replaced.
//
// # Goroutine safety
//
// No mutable type here is safe for concurrent use: Bloom, CMS, HLL, MisraGries and SigSketch all
// require external synchronisation, and the daemon (SP-05) owns every live sketch behind its
// session-registry mutex. The pure surface — MinHash, Signature.Jaccard, Signature.IsNearDup,
// EncodeHeader, DecodeHeader and the hashing helpers — IS safe for concurrent use, because store
// (SP-06) calls into it from its worker pool.
//
// # Constructors take scalars, never config
//
// Every constructor takes plain numbers, and this package does not import internal/config. That
// keeps it testable without a config document and, more importantly, keeps it from drifting from
// Appendix C by copying defaults: the composition root reads config and passes the values in, so
// there is exactly one place a default is written down. For the same reason the package does not
// import internal/obs — counters belong to the daemon that owns the sketches, not to the data
// structures. TestImports_FoundationOnly enforces both.
//
// Keys are opaque bytes and this package normalizes nothing. Callers pass keys that are already
// normalized: SP-09 passes negknow.Descriptor.Key(), SP-08 passes []byte(paths.Key(path)).
//
// # The ceiling rule
//
// Every constructible sketch must also be marshallable, with one stated exception. The bounds in
// errors.go are chosen so the largest sketch a constructor will build still encodes to a frame
// under MaxFrameBytes, and EncodeHeader enforces that ceiling once on behalf of all five
// MarshalBinary implementations. Without it, a legally-constructed filter could be written by Save
// and then rejected by Load as ErrTooLarge — a sketch that cannot survive a restart, which is the
// one failure this package exists to prevent.
//
// The exception is Misra-Gries, and it is a real one rather than an oversight. A Bloom, a
// Count-Min and a HyperLogLog have frame sizes fixed by their dimensions alone, so a bound on the
// dimensions is a bound on the frame. A Misra-Gries frame's size depends on the KEY LENGTHS it has
// accumulated as well as on its counter count, and MaxMGCounters × MaxMGKeyBytes is roughly 8 GiB
// — about 128× MaxFrameBytes — so no bound on k alone can guarantee marshallability. For
// MisraGries the invariant is therefore the weaker but still sufficient one: MarshalBinary fails
// CLEANLY, returning ErrTooLarge and no bytes at all — never a partial frame, never a panic — so
// an over-large counter table is a loud, recoverable refusal to save rather than a corrupt file
// that Load would later reject. Both bounds are far above any real workload: a Misra-Gries key
// here is a path or a tool name, and the counter table is sized by the caller.
//
// Constructors never panic and never return an error: out-of-range, NaN and infinite arguments are
// clamped to the nearest legal value, because a hook that dies takes observability with it (§12.3,
// §11.3). Decoders are the opposite — strict, allocating nothing until every declared size has
// been checked against both MaxFrameBytes and the actual buffer.
//
// # Load versus LoadWithLog
//
// §5.7 annotates Load with "corrupt → ErrNotFound + Loud log", but its signature carries no logger
// and this package refuses package-level mutable state, so the contract is split and both halves
// are mandatory. Load delivers the CRC and version checking and the core.ErrNotFound mapping, and
// writes no log line because it hands LoadWithLog a logging.Nop with no destination behind it.
// LoadWithLog delivers the Loud half and is the form every composition root must call — SP-05's
// SketchSet and SP-09's negknow.Open both call it, never Load; Load exists for tests and for
// callers that provably have no logger. Choosing the silent path by accident would violate §13
// invariant 10, "degradation is loud".
//
// Every failure the pair can report — absent, unstattable, oversize, unreadable, corrupt — comes
// back as core.ErrNotFound with the underlying sentinel still inspectable through errors.Is, so a
// caller's "not found ⇒ start empty" branch is also its corrupt-file branch (§13 invariant 3: a
// sketch is a cache, never the source of truth).
//
// # Save refuses tried.bloom
//
// sketches/tried.bloom is the one file here that §7.4 makes append-only, and 00-ARCHITECTURE.md
// §3.3 permits its replacement only through the generational path: write the new filter, rename
// the old one to tried.bloom.<seq>.bak, keep exactly one generation. Save therefore refuses that
// base name outright, with an error satisfying both ErrGenerational and core.ErrAppendOnly, and
// ReplaceGenerational — which delegates the rename dance to paths.ReplaceBloom, the sole sanctioned
// writer of that path — is the door. The refusal is mechanical enforcement rather than a comment
// asking callers to be careful: internal/paths refuses the same path from the other side, so the
// invariant holds even for a caller that never read this doc.
//
// # Literals
//
// 00-ARCHITECTURE.md §11.6 forbids any non-test file here from spelling a literal that duplicates
// a config default: the floats 0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4 and the ints 20000, 12000,
// 10000, 8000, 2048, 1024, 4096, 16384, 300, 120, 450. Every sizing value in this package is a
// constructor parameter for exactly that reason. The single annotated exemption is FPWarnRate,
// whose 0.10 collides numerically with the forbidden 0.1 but is an unrelated quantity — a
// false-positive watch threshold (§11.4), not a cache read multiplier.
//
// §13 invariant 3 governs the whole package: a bloom filter — and by the same logic every other
// sketch here — is a cache, never the source of truth. Every membership or estimate answer must be
// backed by a record lookup or explicitly flagged, and no answer these types give may be treated
// as authoritative on its own.
package sketch
