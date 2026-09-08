package grammar

import (
	"encoding/binary"
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

// The grammar snapshot frame, byte for byte. All multi-byte integers are little-endian, and every
// count is a varint (encoding/binary's Uvarint/Varint), so a session with four rules costs a byte
// of framing per rule rather than a fixed-width struct's thirty-two.
//
//	offset  size      field
//	0       16        Magic     "qompack-grammar" followed by a NUL (contract §4)
//	16      2         Ver       uint16, currently CodecVersion (1)
//	18      variable  Payload   version-specific; see decodeSnapshotV1 for v1's layout
//
// v1's payload, in Snapshot's own field order so the encoder and the struct can be read side by
// side:
//
//	count(Rules)          nil-preserving count, see appendCodecCount
//	  per rule, in order: varint ID, varint Uses, varint Span,
//	                      symbols(Body), symbols(Expansion)
//	symbols(Sequence)
//	varint NextID
//
// where symbols(x) is count(x) followed by, per Symbol, a uvarint byte length and that many raw
// bytes.
//
// Why a hand-rolled frame rather than encoding/gob or JSON. The reasons internal/sketch/header.go
// gives for the QPKS frame apply here, plus one specific to this type. gob's wire format is tied to
// Go's type definitions, so adding a field to Rule would silently change what an existing
// checkpoint means, and a truncated file would decode into a plausible half-grammar — the exact
// outcome contract §4 forbids when it says a corrupt payload must report core.ErrDegraded rather
// than yield a silently empty grammar. JSON would additionally force a choice about how to escape a
// Symbol, and Symbol's whole point is that it may contain a NUL (see RuleRef in types.go): a rule
// reference is "\x00R7", and a format that mangled or re-escaped that byte would corrupt the
// grammar's internal references rather than merely its formatting. Here a Symbol is a length and
// that many opaque bytes, so NUL is not special and there is no escaping to get wrong.
//
// Why little-endian with a uint16 version rather than a single byte: that is the shape
// internal/sketch/header.go already froze for the QPKS frame, and one convention for "versioned
// binary artifact under .qompack/" is worth more than the byte it costs.
const (
	// codecMagic is the 16-byte prefix contract §4 freezes. The trailing NUL also stops a text-mode
	// tool, a CRLF-translating checkout or a stray editor from silently "fixing" the file: git's own
	// text=auto detection treats a NUL-bearing file as binary, and the committed compatibility
	// vectors under testdata/codec/ additionally carry the .golden extension that .gitattributes
	// marks -text for the same reason.
	codecMagic = "qompack-grammar\x00"
	// codecMagicLen is len(codecMagic), named so the encoder's and the decoder's arithmetic
	// provably agree rather than agreeing by careful retyping.
	codecMagicLen = 16
	// codecVersionLen is the width of the version field.
	codecVersionLen = 2
	// codecHeaderLen is the fixed prefix every version shares: magic plus version. A frame shorter
	// than this cannot be classified at all, which is why DecodeSnapshot's first check is a length
	// check and not a magic comparison.
	codecHeaderLen = codecMagicLen + codecVersionLen
)

// Capacity hints for EncodeSnapshot's output buffer. They are HINTS and nothing depends on them
// being right: a low estimate costs one append-grow and a high one costs a few unused bytes, so
// they are tuned to the median session (tool names such as "Read" and "Bash", short rule bodies)
// rather than to a worst case that would over-allocate every ordinary checkpoint.
const (
	// codecSymbolSizeHint is the estimated encoded width of one Symbol: a one-byte length plus a
	// short tool name.
	codecSymbolSizeHint = 12
	// codecRuleSizeHint is the estimated encoded width of one Rule: three small varints, two
	// counts, and a handful of symbols across Body and Expansion.
	codecRuleSizeHint = 64
)

// EncodeSnapshot renders s as the versioned frame documented above (contract §4). It has no error
// return because it cannot fail: every field of a Snapshot has an encoding, every length comes from
// len() rather than from the caller, and the result is a freshly allocated buffer the caller owns.
//
// It is DETERMINISTIC in the strong sense the contract asks for — equal Snapshots encode to equal
// bytes — and the reason is structural rather than defensive: a Snapshot is three ordered fields
// with no map in it, so there is no iteration order to stabilize. That is worth stating explicitly
// because the usual remedy (sort before encoding) is deliberately NOT applied here. Snapshot.Rules
// is documented as ordered by ID ascending; if a caller hands over an unsorted one this encoder
// preserves that order rather than silently repairing it, so DecodeSnapshot(EncodeSnapshot(s))
// still equals s and an ordering bug in the grammar core surfaces in the core's own tests instead
// of being laundered by the codec.
//
// Round-trip identity holds for EVERY Snapshot, including the distinction between a nil slice and
// an empty one — see appendCodecCount for why that distinction is carried on the wire.
func EncodeSnapshot(s Snapshot) []byte {
	buf := make([]byte, 0, codecHeaderLen+
		len(s.Rules)*codecRuleSizeHint+
		len(s.Sequence)*codecSymbolSizeHint)

	buf = append(buf, codecMagic...)
	buf = binary.LittleEndian.AppendUint16(buf, CodecVersion)

	buf = appendCodecCount(buf, s.Rules)
	for _, r := range s.Rules {
		buf = binary.AppendVarint(buf, int64(r.ID))
		buf = binary.AppendVarint(buf, int64(r.Uses))
		buf = binary.AppendVarint(buf, int64(r.Span))
		buf = appendCodecSymbols(buf, r.Body)
		buf = appendCodecSymbols(buf, r.Expansion)
	}

	buf = appendCodecSymbols(buf, s.Sequence)
	buf = binary.AppendVarint(buf, int64(s.NextID))
	return buf
}

// DecodeSnapshot reads a frame EncodeSnapshot wrote. It is the COMPATIBILITY READER of contract
// §4: a higher version, a bad magic, a truncated payload, a forged length and trailing bytes each
// report core.ErrDegraded with a message naming what was wrong, and none of them panics or returns
// a zero Snapshot with a nil error.
//
// The distinction between "reports an error" and "returns an empty grammar" is the entire point of
// this function and not a stylistic preference. A grammar is how a session's repeated actions are
// detected; an empty one is indistinguishable from a session that never repeated anything. If a
// half-written checkpoint decoded to an empty grammar, the loop detector would report "no loops"
// for a session whose whole problem was a loop, and nothing downstream would have any way to tell
// the two apart. ErrDegraded is the right sentinel for that: 00-ARCHITECTURE.md §12's
// degraded-passive mode is the documented response to "this component cannot be trusted right now".
//
// The returned error always wraps core.ErrDegraded, which is one of the four sentinels
// grammartest.requireKnownError admits.
func DecodeSnapshot(b []byte) (Snapshot, error) {
	if len(b) < codecHeaderLen {
		return Snapshot{}, degradedCodec("truncated frame: %d bytes, want at least %d for the header",
			len(b), codecHeaderLen)
	}
	if string(b[:codecMagicLen]) != codecMagic {
		return Snapshot{}, degradedCodec("bad magic: %q is not a grammar snapshot", b[:codecMagicLen])
	}

	ver := binary.LittleEndian.Uint16(b[codecMagicLen:codecHeaderLen])
	switch ver {
	case CodecVersion:
		return decodeSnapshotV1(b[codecHeaderLen:])
	default:
		// Every rejected version gets the same treatment but the message says which side is behind,
		// because the two directions fail for opposite reasons and whoever reads the log needs to
		// know which one they are looking at.
		//
		// A HIGHER version is contract §4's named case: a newer plugin wrote a layout this build
		// does not know, and half-reading it would be worse than refusing.
		//
		// A LOWER version is refused too, which deserves its own sentence because the contract
		// phrases the accept rule as "version <= CodecVersion". CodecVersion is 1 and always has
		// been, so no build has ever written a 0 and no v0 layout exists to decode: a zero version
		// field is a zeroed or partially written file, not an old one. Accepting it would mean
		// guessing that a zeroed header is followed by a v1 payload — and a fully zeroed 21-byte
		// file happens to BE a well-formed empty-grammar payload, so that guess would turn a
		// truncated write into exactly the silently empty grammar the paragraph above exists to
		// prevent. When a version 2 ships, this switch grows a "case 2:" arm and the "case 1:" arm
		// stays forever, the same upgrade path internal/sketch/header.go documents.
		return Snapshot{}, degradedCodec(
			"unsupported codec version %d: this build reads and writes version %d", ver, CodecVersion)
	}
}

// decodeSnapshotV1 decodes the v1 payload documented in this file's layout table. It is a separate
// function from DecodeSnapshot so that the version switch above stays a switch: a future v2 arm
// gets its own decoder rather than a flag threaded through this one.
func decodeSnapshotV1(payload []byte) (Snapshot, error) {
	r := codecReader{b: payload}

	ruleCount, rulesPresent, err := r.count("rule count")
	if err != nil {
		return Snapshot{}, err
	}
	var rules []Rule
	if rulesPresent {
		rules = make([]Rule, ruleCount)
		for i := range rules {
			if rules[i], err = r.rule(i); err != nil {
				return Snapshot{}, err
			}
		}
	}

	sequence, err := r.symbols("sequence")
	if err != nil {
		return Snapshot{}, err
	}

	nextID, err := r.varint("next rule ID")
	if err != nil {
		return Snapshot{}, err
	}

	// Trailing bytes are a hard error rather than something to ignore. A frame with extra bytes
	// after a well-formed payload is either two frames concatenated by a partial-write recovery or
	// a newer file whose version field was hand-edited down; both are corruption, and both would
	// otherwise decode into a plausible-looking grammar that silently dropped whatever the extra
	// bytes meant.
	if len(r.b) != 0 {
		return Snapshot{}, degradedCodec("trailing bytes: %d bytes after a complete payload", len(r.b))
	}

	return Snapshot{Rules: rules, Sequence: sequence, NextID: RuleID(nextID)}, nil
}

// appendCodecCount writes a slice's length in the NIL-PRESERVING form: 0 for a nil slice, len+1 for
// a present one.
//
// The extra byte per empty slice buys exact round-trip identity, which contract §4 asks for
// unconditionally ("DecodeSnapshot(EncodeSnapshot(s)) equals s for every valid s") and which the
// obvious encoding does not deliver. Go distinguishes []Symbol(nil) from []Symbol{}, and
// reflect.DeepEqual — which require.Equal, the conformance suite and any future checkpoint-stability
// assertion all reduce to — reports them as different. An encoder that wrote a plain length would
// collapse the two, so a grammar core that happened to build an empty Body as []Symbol{} would come
// back from a checkpoint with a nil Body and compare unequal to the grammar it was saved from. That
// failure is invisible in isolation and surfaces only as a flaky save/restore test written by
// someone else, which is the worst kind of bug to leave inside a codec.
func appendCodecCount[T any](buf []byte, s []T) []byte {
	if s == nil {
		return append(buf, 0)
	}
	return binary.AppendUvarint(buf, uint64(len(s))+1)
}

// appendCodecSymbols writes a symbol slice: a nil-preserving count, then each Symbol as a uvarint
// byte length and that many raw bytes. The bytes are raw and unescaped on purpose — see the frame
// documentation above on why a Symbol containing a NUL must survive verbatim.
func appendCodecSymbols(buf []byte, syms []Symbol) []byte {
	buf = appendCodecCount(buf, syms)
	for _, s := range syms {
		buf = binary.AppendUvarint(buf, uint64(len(s)))
		buf = append(buf, string(s)...)
	}
	return buf
}

// codecReader is a cursor over the remaining payload bytes. Every read advances b, so "how much is
// left" is always len(r.b) and no read can be written that forgets to bounds-check: the checks live
// in the methods below rather than at each of their dozen call sites.
type codecReader struct{ b []byte }

// varint reads one zig-zag signed varint. Signed rather than unsigned because RuleID, Rule.Uses and
// Rule.Span are all Go ints: a negative value is not something a correct grammar core produces, but
// a codec that could not represent one would round-trip it as a huge positive number and turn a
// caller's bug into a corrupt-looking checkpoint. Representing it faithfully keeps the blame where
// it belongs.
func (r *codecReader) varint(field string) (int64, error) {
	v, n := binary.Varint(r.b)
	if n <= 0 {
		return 0, degradedCodec("truncated or malformed %s varint at %d bytes remaining", field, len(r.b))
	}
	r.b = r.b[n:]
	return v, nil
}

// uvarint reads one unsigned varint.
func (r *codecReader) uvarint(field string) (uint64, error) {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		return 0, degradedCodec("truncated or malformed %s length at %d bytes remaining", field, len(r.b))
	}
	r.b = r.b[n:]
	return v, nil
}

// count reads a nil-preserving count and reports both the element count and whether the slice was
// present at all (see appendCodecCount).
//
// The bound it enforces is the one that matters for a decoder reading a file it does not trust: a
// forged count of 2^60 must not reach make(). Rather than invent a maximum rule count — which would
// be a policy decision this package has no business making, and which would eventually be too small
// for some real session — it bounds the count by the bytes actually remaining. Every element costs
// at least one byte to encode, so a count larger than the remaining length is provably a lie and the
// allocation that follows is provably bounded by the size of the input.
func (r *codecReader) count(field string) (int, bool, error) {
	raw, err := r.uvarint(field)
	if err != nil {
		return 0, false, err
	}
	if raw == 0 {
		return 0, false, nil
	}
	n := raw - 1
	if n > uint64(len(r.b)) {
		return 0, false, degradedCodec("%s declares %d entries but only %d bytes remain", field, n, len(r.b))
	}
	return int(n), true, nil
}

// symbols reads a symbol slice written by appendCodecSymbols.
func (r *codecReader) symbols(field string) ([]Symbol, error) {
	n, present, err := r.count(field + " count")
	if err != nil || !present {
		return nil, err
	}
	out := make([]Symbol, n)
	for i := range out {
		length, err := r.uvarint(fmt.Sprintf("%s[%d]", field, i))
		if err != nil {
			return nil, err
		}
		if length > uint64(len(r.b)) {
			return nil, degradedCodec("%s[%d] declares %d bytes but only %d remain",
				field, i, length, len(r.b))
		}
		// The conversion COPIES. That is load bearing: the caller owns b and may reuse or overwrite
		// it, and a decoder that aliased the input would hand back a Snapshot whose Symbols mutate
		// underneath it — the kind of bug that only reproduces under a buffer pool.
		out[i] = Symbol(r.b[:length])
		r.b = r.b[length:]
	}
	return out, nil
}

// rule reads one Rule. index appears only in error messages, so a corrupt file names the rule it
// died on rather than reporting "truncated" from an unknowable position.
func (r *codecReader) rule(index int) (Rule, error) {
	id, err := r.varint(fmt.Sprintf("rule[%d] ID", index))
	if err != nil {
		return Rule{}, err
	}
	uses, err := r.varint(fmt.Sprintf("rule[%d] Uses", index))
	if err != nil {
		return Rule{}, err
	}
	span, err := r.varint(fmt.Sprintf("rule[%d] Span", index))
	if err != nil {
		return Rule{}, err
	}
	body, err := r.symbols(fmt.Sprintf("rule[%d] Body", index))
	if err != nil {
		return Rule{}, err
	}
	expansion, err := r.symbols(fmt.Sprintf("rule[%d] Expansion", index))
	if err != nil {
		return Rule{}, err
	}
	return Rule{ID: RuleID(id), Body: body, Uses: int(uses), Expansion: expansion, Span: int(span)}, nil
}

// degradedCodec wraps core.ErrDegraded with a diagnostic. Every failure in this file goes through
// it, so the sentinel is chosen in exactly one place and no code path can accidentally return a bare
// fmt.Errorf that grammartest.requireKnownError would reject.
func degradedCodec(format string, args ...any) error {
	return fmt.Errorf("%w: grammar codec: %s", core.ErrDegraded, fmt.Sprintf(format, args...))
}
