package chunk

// gearSeed is the frozen seed the 256-entry gear table is generated from.
//
// CHANGING THIS VALUE RE-CHUNKS EVERY OBJECT IN EVERY EXISTING STORE. The gear table is the only
// thing that decides where a content-defined boundary falls; move one entry and every boundary in
// every stream moves with it. Every chunk hash in the CAS changes, every root in index/roots.jsonl
// stops resolving, every negative-knowledge dependence hash goes stale, and nothing on disk can be
// migrated — the only recovery is to re-ingest every session from scratch. Treat gearSeed the way
// core's domain strings are treated (internal/core/hash.go): as a wire format, not as a tunable.
// TestGearTableGolden freezes the resulting table's digest so this cannot happen by accident.
//
// The value itself is arbitrary — any seed produces an equally good table, because splitmix64's
// output is statistically indistinguishable from random for every seed. It is written down once
// here, and never anywhere else.
const gearSeed uint64 = 0x9067_4E5A_1CDB_7F31

// gearSize is the number of entries in the gear table: one per possible byte value. The rolling
// hash indexes it with a raw byte, so it is a property of the byte, not a tunable.
const gearSize = 256

// splitmix64 advances *x and returns the next value of the SplitMix64 stream, exactly as published
// (Steele, Lea & Flood 2014; Vigna's public-domain reference C). It is used here purely as a
// deterministic table generator, never as a source of randomness for anything security-relevant.
//
// The three constants are the reference implementation's and must not be "improved": they are what
// make the output stream reproducible across languages and platforms, which is what lets
// TestSplitMix64_ReferenceVectors pin the generator against values published outside this
// repository rather than against its own output.
func splitmix64(x *uint64) uint64 {
	*x += 0x9E37_79B9_7F4A_7C15
	z := *x
	z = (z ^ (z >> 30)) * 0xBF58_476D_1CE4_E5B9
	z = (z ^ (z >> 27)) * 0x94D0_49BB_1331_11EB
	return z ^ (z >> 31)
}

// gear is the immutable table the rolling hash mixes each input byte through: gear[b] is the 64-bit
// value byte b contributes to the fingerprint.
//
// It is GENERATED at init from gearSeed rather than committed as a 4 KB literal table, for three
// reasons. A literal table of 256 random-looking hex constants is unreviewable — no reader can tell
// a typo from a value, and no test can either. A generated table is provably identical on every
// platform and every Go version, because splitmix64 is pure integer arithmetic with no
// endianness, no floating point and no map iteration in it. And it makes the "this is frozen"
// claim checkable in one line: TestGearTableGolden digests the whole table, so the table's identity
// is one golden file rather than 256 constants nobody will ever diff.
//
// Generation cost is 256 multiplies at process start, which is not measurable.
var gear = newGearTable(gearSeed)

// newGearTable returns the gear table derived from seed. It is a function rather than an init()
// body so the table can be a `var` with no mutable window between package initialization and first
// use, and so a test can generate a table from a different seed without touching the real one.
func newGearTable(seed uint64) [gearSize]uint64 {
	var table [gearSize]uint64
	state := seed
	for i := range table {
		table[i] = splitmix64(&state)
	}
	return table
}
