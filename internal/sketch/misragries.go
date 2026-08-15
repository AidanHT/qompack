package sketch

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"fmt"
	"math"
	"slices"

	"github.com/qompack/qompack/internal/core"
)

// The on-wire param names. They are lower case because the QPKS frame restricts param names to
// [a-z0-9.] (header.go), and they are constants rather than literals at four call sites because the
// encoder and the decoder have to agree: a typo in one of them would produce a summary that saves
// cleanly and then fails to load, which is the one failure §6.2 exists to prevent.
const (
	// paramErr is the accumulated decrement, the additive error bound MaxError reports. It travels
	// in the header rather than in the body because it is a property of the summary as a whole and
	// not of any one counter — and without it a decoded summary would report counts with no way to
	// say how far below the truth they may be.
	paramErr = "err"
	// paramMGK is the counter budget k. It is a separate constant from bloom.go's paramK even though
	// both encode to the same byte on the wire, because they name entirely different quantities — a
	// probe count and a counter budget — in frames of different Kind, and a future change to either
	// must not silently move the other.
	paramMGK = "k"
)

// The body layout, byte for byte. All multi-byte integers are little-endian, as everywhere in a
// QPKS frame.
//
//	offset  size      field
//	0       4         EntryCount uint32, ≤ k
//	per entry, sorted strictly ascending by key (bytewise):
//	        2         KeyLen uint16, 1..MaxMGKeyBytes
//	        KeyLen    key bytes
//	        8         Count int64, > 0
//
// The three widths are named rather than spelled inline because the encoder, the decoder and the
// size computation must provably agree on all three: a 2 that should have been a 4 would produce a
// body whose every length check still passed and whose entries were all read one field out of step.
const (
	// mgEntryCountBytes is the width of the body's leading entry count.
	mgEntryCountBytes = 4
	// mgKeyLenBytes is the width of one entry's key-length prefix.
	mgKeyLenBytes = 2
	// mgCountBytes is the width of one entry's counter.
	mgCountBytes = 8
)

// maxMGCount bounds every count in this file: the header's Count, the err param, each entry's own
// counter, and the ceiling Add saturates at.
//
// It is min(2^61, math.MaxInt) for three reasons at once. 2^61 is exactly representable in float64,
// so the CEILING ITSELF compares exactly against the err param — math.MaxInt64 does not, and a bound
// that cannot be compared exactly is not a bound. That is all the exactness this constant buys, and
// the sentence has to stop there: an err BELOW the ceiling round-trips exactly only up to 2^53,
// above which a float64 param cannot distinguish neighbouring integers and MustParamInt would accept
// the neighbour, since it rejects only non-integral floats. err ≤ total/(k+1) puts that regime out of
// reach of any stream this system can produce — it would take nine quadrillion observations — but it
// is a limit of the format, not a property it has. The min with math.MaxInt keeps the conversion into
// the int the counter map holds defined on a 32-bit build as well as on a 64-bit one. And 2^61 leaves
// room for MergeFrom to add two decoded counters — the one arithmetic this package performs on
// numbers it did not itself compute — without overflowing int64.
const maxMGCount = min(1<<61, math.MaxInt)

// mgSatTotal returns total + n, saturating at maxMGCount rather than wrapping. Both arguments must be
// non-negative, which every caller here guarantees: Add's guard rejects n ≤ 0, and total and err only
// ever grow from zero.
//
// This helper is a DELIBERATE DEVIATION from the plan, which writes the update as a plain
// `m.total += int64(n)` (plan lines 836–850). Add takes an int weight, so two calls with a weight near
// math.MaxInt wrap Total negative — and a negative Total is not merely a wrong number, it is a broken
// contract in memory rather than on disk: Total()/(k+1) stops meaning anything, Top's promised
// true(x) − MaxError() ≤ reported(x) ≤ true(x) is stated against it, and CMS.HeavyHitters hands the
// value straight through to SP-14. Saturating makes the invariant true at every instant instead of
// merely checked once on the way out to a file. satAdd64 supplies the no-wrap half — it is this
// package's existing answer to the same question for CMS.Total — and the clamp to maxMGCount is what
// keeps the result encodable.
func mgSatTotal(total, n int64) int64 {
	if s := satAdd64(uint64(total), uint64(n)); s <= uint64(maxMGCount) {
		return int64(s)
	}
	return maxMGCount
}

// mgSatCount returns a + b, saturating at maxMGCount rather than wrapping. It is mgSatTotal's twin
// for the counter map, which holds int rather than int64, and it exists for the same reason: a
// wrapped counter would make Top report a key as having occurred a negative number of times.
//
// Both arguments must be non-negative, and both are: a is a stored counter, which Add keeps positive
// and decodeV1 refuses at or below zero, and b is a weight Add's own guard has already rejected at
// zero.
//
// The comparison is written as b > maxMGCount−a rather than as a+b > maxMGCount because the second
// form has to compute the sum that may already have overflowed in order to test whether it did.
func mgSatCount(a, b int) int {
	if b > maxMGCount-a {
		return maxMGCount
	}
	return a + b
}

// Counted is one key and its count. The count is exact when it came from a Misra-Gries summary that
// never decremented, a LOWER bound when that summary did (see MisraGries.Top), and an UPPER bound
// when it came from CMS.HeavyHitters — the direction is a property of the source, not of this type,
// and every method that returns one says which.
type Counted struct {
	// Key is the counted key, stored verbatim.
	Key string
	// Count is the number of occurrences attributed to Key.
	Count int
}

// MisraGries is the deterministic top-k counter of §6.2's companion table: "deterministic top-k
// with no false positives, O(k)". It answers "which keys carry most of this stream?" in k counters,
// whatever the number of distinct keys.
//
// Its guarantee is two-sided and both halves matter. No false positives: keys are stored verbatim
// and a key that was never Added can never enter the table, so every key Top reports really
// occurred — that half is unconditional. Counts are LOWER bounds: 0 < reported(x) ≤ true(x) holds
// always, and the tighter true(x) − MaxError() ≤ reported(x) holds — as does "any key whose true
// frequency exceeds Total()/(k+1) is still present", which is derived from it — for every stream
// whose Total() stays below the ceiling Add saturates at. That is every stream this system can
// produce; reaching the ceiling takes a single weight near math.MaxInt, and Add's doc comment says
// what a summary that does reach it reports instead. That is the exact opposite direction to the
// Count-Min sketch next door, which over-counts and stores no keys at all — which is why
// CMS.HeavyHitters pairs the two.
//
// MergeFrom is the mergeable-summary construction SP-16 uses for Phase 7's cross-session warm start
// (O4): two summaries over disjoint sessions combine into one that still satisfies the bound above.
//
// MisraGries is NOT safe for concurrent use. Add mutates the counter map in place and Total is not
// atomic; the daemon (SP-05) owns every live sketch behind its session-registry mutex.
type MisraGries struct {
	k        int
	counters map[string]int
	total    int64
	err      int64 // the accumulated decrement, the additive error bound
	created  core.UnixMilli
}

// NewMisraGries returns a summary tracking up to k candidate keys, clamped to [1, MaxMGCounters].
//
// It never panics and never returns an error: out-of-range arguments are clamped, because a hook
// that dies takes observability down with it (§12.3). K reports what the caller actually got, so a
// silently corrected configuration is visible rather than assumed.
//
// The counter map is sized for k up front. The table is bounded by construction and is walked in
// full by every Add that reaches the decrement phase, so growing it incrementally would buy nothing
// and would put a rehash on the observer's hot path (§8.1's < 15 ms p99).
func NewMisraGries(k int) *MisraGries {
	kk := clampInt(k, 1, MaxMGCounters)
	return &MisraGries{k: kk, counters: make(map[string]int, kk)}
}

// Add records n more occurrences of key. A weight of 0 or below is a no-op: it carries no
// occurrence, and letting it through would move Total — the N that Total()/(k+1) and every retention
// claim above are stated against — while a negative weight would additionally push a stored counter
// below zero, where Top would report a key as having occurred a negative number of times.
//
// The three cases are the whole algorithm. A key already counted is incremented, which is exact. A
// new key with room left is admitted at its full weight, which is also exact. A new key with the
// table full triggers the decrement: d is the minimum of the arriving weight and every stored
// counter, every counter loses exactly d (those that reach zero are dropped), the arriving key is
// admitted with n − d, and err grows by d. Exactly (k+1)·d of accounted mass leaves the table on
// that step, which is why err can never exceed total/(k+1) — and that inequality is the entire
// source of the retention guarantee.
//
// The map iteration order is irrelevant to the result. d is a minimum over the whole table, the
// deleted set is exactly {c : c == d} because d ≤ every counter, and every survivor becomes c − d;
// all three are order-independent, which is what earns §6.2's word "deterministic" and what
// TestMG_DeterministicUnderMapOrder pins by replaying one stream 32 times and comparing bytes.
//
// A key longer than MaxMGKeyBytes is truncated to exactly that many bytes, because a Misra-Gries
// frame's size depends on its key lengths as well as on its counter count (doc.go's ceiling-rule
// exception) and an unbounded key is the one way a legally constructed summary could become
// unsaveable. The cut is at a byte boundary, so it may split a UTF-8 rune, and the no-false-positive
// guarantee is then about the truncated key rather than the original — neither matters for the keys
// this system produces, which are paths and tool names two orders of magnitude below the bound.
//
// Every counter and the total SATURATE at maxMGCount rather than wrapping (see mgSatTotal, which
// records why this deviates from the plan's plain +=). Saturation is the one condition under which
// the lower bound above is looser than MaxError() claims: a saturated count is still a lower bound —
// reported ≤ true — but it is no longer within MaxError() of the truth. Reaching it takes a single
// weight near math.MaxInt, which no caller in this system produces; the alternative, a wrapped
// negative count, breaks both halves of the bound at once and does it silently.
func (m *MisraGries) Add(key string, n int) {
	if n <= 0 || m.k < 1 {
		// The zero MisraGries has no counter table and no budget to admit anything into; silently
		// doing nothing is the only non-panicking answer, and it agrees with Top, which reports
		// nothing.
		return
	}
	if len(key) > MaxMGKeyBytes {
		key = key[:MaxMGKeyBytes]
	}

	m.total = mgSatTotal(m.total, int64(n))
	if c, ok := m.counters[key]; ok {
		m.counters[key] = mgSatCount(c, n)
		return
	}
	if len(m.counters) < m.k {
		m.counters[key] = clampInt(n, 1, maxMGCount)
		return
	}

	d := n
	for _, c := range m.counters {
		if c < d {
			d = c
		}
	}
	for kk, c := range m.counters {
		if c-d <= 0 {
			delete(m.counters, kk)
		} else {
			m.counters[kk] = c - d
		}
	}
	if n-d > 0 {
		m.counters[key] = clampInt(n-d, 1, maxMGCount)
	}
	m.err = mgSatTotal(m.err, int64(d))
}

// Top returns the summary ordered by count descending, then key ascending. n ≤ 0 returns every
// counter; an n above the counter count returns every counter rather than padding.
//
// No false positives: every key returned was Added at least once, because keys are stored verbatim
// and nothing but Add and UnmarshalBinary ever writes one into the table.
//
// Counts are lower bounds: 0 < reported(x) ≤ true(x) holds always, and the tighter
// true(x) − MaxError() ≤ reported(x) holds for every stream whose Total() stays below the ceiling
// Add saturates at — which is every stream this system can produce, since reaching it takes a single
// weight near math.MaxInt. A summary that did reach it reports a count that is still a lower bound
// but no longer within MaxError() of the truth; Add's doc comment has the detail.
//
// Absence is NOT evidence — a key may have been dropped by a decrement — except above the retention
// threshold Total()/(k+1), which is derived from the bound above and therefore carries the same
// condition. §13 invariant 3 applies throughout: the answer is a cache, and a consumer acting on it
// needs a record behind it.
//
// The tie-break on key is the load-bearing half of the ordering. Two keys with equal counts would
// otherwise come back in map iteration order, and "the three hottest files" would be a different
// list on every call.
func (m *MisraGries) Top(n int) []Counted {
	if len(m.counters) == 0 {
		return nil
	}
	out := make([]Counted, 0, len(m.counters))
	for k, c := range m.counters {
		out = append(out, Counted{Key: k, Count: c})
	}
	sortCounted(out)
	if n > 0 && n < len(out) {
		out = out[:n]
	}
	return out
}

// sortCounted orders a result slice by count descending, then key ascending. Top and
// CMS.HeavyHitters share it because they make the same promise to the same consumers — "the five
// hottest files" has to be a stable answer, and a map's iteration order is not one — and because two
// separate comparators would be free to drift into two different orders for the same data.
func sortCounted(c []Counted) {
	slices.SortFunc(c, func(a, b Counted) int {
		if a.Count != b.Count {
			return cmp.Compare(b.Count, a.Count)
		}
		return cmp.Compare(a.Key, b.Key)
	})
}

// K returns the counter budget AFTER clamping — the k of the Total()/(k+1) retention threshold.
// Reporting the realised budget rather than the requested one is what makes a silently corrected
// configuration visible.
func (m *MisraGries) K() int { return m.k }

// Total returns N, the sum of every weight added. It is the quantity the retention threshold
// N/(k+1) is stated against, so a consumer reasoning about what the summary may have dropped needs
// it alongside any count.
func (m *MisraGries) Total() int64 { return m.total }

// MaxError returns the accumulated decrement: the additive error bound on the counts Top reports,
// true(x) − MaxError() ≤ reported(x) ≤ true(x), for every stream whose Total() stays below the
// ceiling Add saturates at — which is every stream this system can produce. Past that ceiling a
// count can sit further below the truth than this number says, because saturation discards magnitude
// that no error term is tracking; 0 < reported(x) ≤ true(x) still holds there, and Add's doc comment
// has the detail.
//
// It is 0 until a decrement has actually run, which is the whole of the time fewer than k distinct
// keys have been seen — and why the summary is an exact top-k for the small key sets SP-08 feeds it.
// MergeFrom raises it as well as Add does: a merge that has to trim back to k subtracts the
// (k+1)-th largest count from every counter and adds that amount here.
func (m *MisraGries) MaxError() int64 { return m.err }

// SetCreated stamps the construction time carried in the on-disk header. It is a setter rather than
// a NewMisraGries argument because this package takes no core.Clock: the composition root that owns
// the clock stamps the sketch, and every test that needs a byte-stable frame can pin the value
// without a fake clock.
func (m *MisraGries) SetCreated(t core.UnixMilli) { m.created = t }

// MergeFrom folds o into m. It is the mergeable-summary construction SP-16 uses for Phase 7's
// cross-session warm start (O4), and it is not simply "add the counters": the pairwise sum can hold
// up to 2k keys, so the (k+1)-th largest count is subtracted from every counter and the
// non-positives are dropped.
//
// That subtraction is what preserves the error bound rather than merely trimming the table. At least
// k+1 counters hold a count of d or more, so at least (k+1)·d of accounted mass leaves the summary —
// exactly the accounting a decrement inside Add does — and err ≤ total/(k+1) therefore still holds
// afterwards. A trim that simply kept the k largest would leave err understating the true error and
// every count in the result unfalsifiable.
//
// The counter budgets must match. err ≤ total/(k+1) is a statement ABOUT k, so a pairwise sum of two
// summaries built with different k would satisfy neither of their bounds, and every count that came
// out of it would still look like a plausible frequency. A nil source is the same refusal, not a
// panic.
func (m *MisraGries) MergeFrom(o *MisraGries) error {
	if o == nil {
		return fmt.Errorf("%w: MergeFrom(nil)", ErrShapeMismatch)
	}
	if o.k != m.k {
		return fmt.Errorf("%w: cannot merge k = %d into k = %d", ErrShapeMismatch, o.k, m.k)
	}

	for kk, c := range o.counters {
		m.counters[kk] += c
	}
	if len(m.counters) > m.k {
		counts := make([]int, 0, len(m.counters))
		for _, c := range m.counters {
			counts = append(counts, c)
		}
		// Descending, so counts[k] is the (k+1)-th largest — the smallest count that k counters are
		// at or above, and therefore the largest d for which the (k+1)·d accounting above holds.
		slices.SortFunc(counts, func(a, b int) int { return cmp.Compare(b, a) })
		d := counts[m.k]
		for kk, c := range m.counters {
			if c-d <= 0 {
				delete(m.counters, kk)
			} else {
				m.counters[kk] = c - d
			}
		}
		m.err += int64(d)
	}
	m.total += o.total
	m.err += o.err
	return nil
}

// Header returns the on-disk metadata for this summary: Kind KindMisraGries, the stream total as
// Count, the Created stamp, and the two params that make the frame self-describing without
// consulting config. CRC32C is zero, as Sketch documents — the checksum covers the encoded body and
// is known only to DecodeHeader.
//
// err is a param rather than a body field because it describes the summary as a whole rather than
// any one counter, and because a reader that only wants to know how trustworthy a saved summary is
// can then learn it from the header alone.
func (m *MisraGries) Header() Header {
	return Header{
		Magic: HeaderMagic,
		Ver:   FormatVersion,
		Kind:  KindMisraGries,
		Params: map[string]float64{
			paramErr: float64(m.err),
			paramMGK: float64(m.k),
		},
		Count:   uint64(m.total),
		Created: m.created,
	}
}

// MarshalBinary encodes the summary as a QPKS frame: the header above, then the body layout
// documented at the top of this file, with the entries sorted strictly ascending by key.
//
// The sort is what makes the encoding canonical. EncodeHeader already sorts the params, but the
// counter map has no order of its own and Go randomizes map iteration on purpose, so without this
// second sort the same logical summary would produce different bytes on every run — and the frozen
// golden fixtures, the byte-identical re-marshal assertion and the content-addressed store all rest
// on a given state having exactly one encoding.
//
// Four states are refused rather than written, all for the same reason: a frame this build's own
// decoder is guaranteed to reject is worse than no frame, because the refusal is loud and
// recoverable at the call site whereas the file is discovered dead on the next restart (§6.2). The
// four are exactly the four claims decodeV1 checks, so "MarshalBinary wrote it" and "decodeV1 will
// read it" are the same statement rather than two that have to be kept in step by hand.
//
// A nil receiver reports ErrMalformed rather than dereferencing (§12.3: a hook that dies takes
// observability down with it). An UNSIZED summary — the zero MisraGries, reachable as
// `var m sketch.MisraGries` from outside this package — would declare k = 0, which decodeV1 refuses
// forever. A key past MaxMGKeyBytes is refused because decodeV1 refuses one: the frame would be
// written cleanly and then be unreadable by the build that wrote it, which for a permanent-memory
// file is the worst of the three outcomes. That check is also the precondition that makes the
// uint16(len(k)) conversion below provably lossless — MaxMGKeyBytes is 8 192, two orders of
// magnitude under the uint16 ceiling, so the encoder never has to reason about a truncated length.
// And a count or a total outside [0, maxMGCount] means arithmetic somewhere has already gone wrong:
// Add saturates, but MergeFrom adds two summaries' counters and totals as they are, so merging two
// frames decoded at the ceiling can land past it.
//
// Add saturates, truncates keys and stores only positive counts, so the last two are reachable only
// through MergeFrom or a direct write to the counter map — which is exactly how the tests reach
// them.
func (m *MisraGries) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: MarshalBinary on a nil *MisraGries", ErrMalformed)
	}
	if m.k < 1 {
		return nil, fmt.Errorf(
			"%w: MarshalBinary on an unsized MisraGries (k=%d); construct it with NewMisraGries",
			ErrMalformed, m.k)
	}
	if m.total < 0 || m.total > maxMGCount {
		return nil, fmt.Errorf("%w: total of %d is outside [0, %d]", ErrMalformed, m.total, maxMGCount)
	}

	keys := make([]string, 0, len(m.counters))
	size := mgEntryCountBytes
	for k, c := range m.counters {
		if len(k) > MaxMGKeyBytes {
			return nil, fmt.Errorf("%w: key of %d bytes exceeds MaxMGKeyBytes (%d)",
				ErrTooLarge, len(k), MaxMGKeyBytes)
		}
		if c < 1 || c > maxMGCount {
			return nil, fmt.Errorf("%w: key %.32q holds count %d, outside [1, %d]",
				ErrMalformed, k, c, maxMGCount)
		}
		keys = append(keys, k)
		size += mgKeyLenBytes + len(k) + mgCountBytes
	}
	// Strings compare bytewise in Go, so this is exactly the "strictly ascending by key (bytewise)"
	// order decodeV1 verifies — the encoder and the decoder are agreeing on one collation, not two.
	slices.Sort(keys)

	body := make([]byte, 0, size)
	body = binary.LittleEndian.AppendUint32(body, uint32(len(keys)))
	for _, k := range keys {
		body = binary.LittleEndian.AppendUint16(body, uint16(len(k)))
		body = append(body, k...)
		body = binary.LittleEndian.AppendUint64(body, uint64(m.counters[k]))
	}
	return EncodeHeader(m.Header(), body)
}

// UnmarshalBinary replaces m's entire state with the summary encoded in data, or leaves m untouched
// and reports why it could not.
//
// The version switch is the upgrade path header.go documents: case 1 decodes the layout this build
// writes, and when FormatVersion ever becomes 2 a case 2 arm is added while case 1 stays forever,
// because sketches are permanent memory (§6.2) and the oldest file on disk must still decode.
// DecodeHeader has already refused anything above FormatVersion, so the default arm is reachable
// only if that guarantee is ever broken — which is exactly when a bare sentinel beats a panic.
//
// A nil receiver reports ErrMalformed rather than panicking, for the same reason MarshalBinary does.
func (m *MisraGries) UnmarshalBinary(data []byte) error {
	if m == nil {
		return fmt.Errorf("%w: UnmarshalBinary on a nil *MisraGries", ErrMalformed)
	}
	h, body, err := DecodeHeader(data)
	if err != nil {
		return err
	}
	if h.Kind != KindMisraGries {
		return fmt.Errorf("%w: frame declares %s, want %s", ErrKindMismatch, h.Kind, KindMisraGries)
	}
	switch h.Ver {
	case 1:
		return m.decodeV1(h, body)
	default:
		return fmt.Errorf("%w: version %d", ErrUnsupportedVersion, h.Ver)
	}
}

// decodeV1 decodes the FormatVersion 1 layout.
//
// It runs the same two passes DecodeHeader does, in the same order and for the same reason. Pass 1
// validates the params against the ceilings the constructor clamps to and then WALKS THE WHOLE BODY
// without allocating: every key length is checked against MaxMGKeyBytes and against the bytes
// actually remaining, every count against [1, maxMGCount], every key against its predecessor, and
// the entries must consume the body exactly. Only then does pass 2 allocate the counter map.
//
// The order is the threat model. EntryCount is the make() hint below, so a frame claiming
// MaxMGCounters entries in a four-byte body has to be refused during the walk rather than turned
// into a million-bucket map and an out-of-memory kill of the daemon. Every rejection path here
// therefore costs one wrapped error and nothing else.
//
// The strictly-ascending check is not cosmetic either: a duplicate key would silently lose one of
// its two counts when pass 2 filled the map, so a frame that round-tripped through this decoder
// would hold less mass than it declared and MaxError would understate the error by the difference.
func (m *MisraGries) decodeV1(h Header, body []byte) error {
	k, err := h.MustParamInt(paramMGK, 1, MaxMGCounters)
	if err != nil {
		return err
	}
	errBound, err := h.MustParamInt(paramErr, 0, int(maxMGCount))
	if err != nil {
		return err
	}
	if h.Count > uint64(maxMGCount) {
		return fmt.Errorf("%w: Count = %d exceeds the ceiling (%d)", ErrMalformed, h.Count, maxMGCount)
	}
	if len(body) < mgEntryCountBytes {
		return fmt.Errorf("%w: body of %d bytes cannot hold the entry count", ErrTruncated, len(body))
	}

	// Compared in uint64 rather than converted to int first: a forged EntryCount near 2^32 would
	// convert to a NEGATIVE int on a 32-bit build, and a negative loop bound is a loop that never
	// runs — the frame would then decode into an empty summary instead of being refused.
	declared := binary.LittleEndian.Uint32(body[:mgEntryCountBytes])
	if uint64(declared) > uint64(k) {
		return fmt.Errorf("%w: %d entries exceeds k = %d", ErrMalformed, declared, k)
	}
	entries := int(declared)

	off := mgEntryCountBytes
	prevStart, prevEnd := 0, 0
	for i := 0; i < entries; i++ {
		if off+mgKeyLenBytes > len(body) {
			return fmt.Errorf("%w: entry %d has no key length in a %d-byte body", ErrTruncated, i, len(body))
		}
		keyLen := int(binary.LittleEndian.Uint16(body[off : off+mgKeyLenBytes]))
		if keyLen < 1 || keyLen > MaxMGKeyBytes {
			return fmt.Errorf("%w: entry %d declares a key of %d bytes, outside [1, %d]",
				ErrMalformed, i, keyLen, MaxMGKeyBytes)
		}
		if off+mgKeyLenBytes+keyLen+mgCountBytes > len(body) {
			return fmt.Errorf("%w: entry %d runs past the %d-byte body", ErrTruncated, i, len(body))
		}
		start := off + mgKeyLenBytes
		end := start + keyLen
		// Strictly ascending, not merely non-descending. The comparison is over the raw body slices
		// so that nothing on this path converts a key to a string, which would allocate.
		if i > 0 && bytes.Compare(body[prevStart:prevEnd], body[start:end]) >= 0 {
			return fmt.Errorf("%w: entry %d is not strictly after its predecessor", ErrMalformed, i)
		}
		count := int64(binary.LittleEndian.Uint64(body[end : end+mgCountBytes]))
		if count < 1 || count > maxMGCount {
			return fmt.Errorf("%w: entry %d holds count %d, outside [1, %d]",
				ErrMalformed, i, count, maxMGCount)
		}
		prevStart, prevEnd = start, end
		off = end + mgCountBytes
	}
	if off != len(body) {
		return fmt.Errorf("%w: %d entries consume %d of the %d body bytes",
			ErrTruncated, entries, off, len(body))
	}

	// Pass 2: every declared size is now known to agree with the buffer, so allocating on them is
	// safe. The body is a subslice of the caller's buffer (DecodeHeader), and string(…) copies, so
	// nothing below retains it.
	counters := make(map[string]int, entries)
	p := mgEntryCountBytes
	for i := 0; i < entries; i++ {
		keyLen := int(binary.LittleEndian.Uint16(body[p : p+mgKeyLenBytes]))
		start := p + mgKeyLenBytes
		end := start + keyLen
		counters[string(body[start:end])] = int(int64(binary.LittleEndian.Uint64(body[end : end+mgCountBytes])))
		p = end + mgCountBytes
	}

	m.k = k
	m.counters = counters
	m.total = int64(h.Count)
	m.err = int64(errBound)
	m.created = h.Created
	return nil
}
