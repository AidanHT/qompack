package negknow

import "unicode/utf8"

// This file is the replay's fast path: a decoder for the ONE record-line shape this package's own
// writer emits, so that materializing a log does not pay encoding/json's reflection and its
// separate validation pass on every line of it. Ruling R26 measured that decode as most of what
// Open costs; SP09-D1 is the owner's ruling that Open be made genuinely cheaper rather than
// re-budgeted.
//
// The rule that makes it safe is that it either decodes a line EXACTLY as json.Unmarshal would,
// or declines it and touches nothing. It declines everything outside a deliberately narrow
// grammar — the canonical key sequence, compact, no \u escapes, no null, integers only — and a
// declined line takes the replay's original path, json.Unmarshal into a fresh logLine and
// recoverLine on failure, unchanged. So the fast path can change how fast a line is read and
// nothing else: which lines materialize, what they decode to, and every counter and warning a
// damaged line produces are all still decided by the code that decided them before.
//
// What it accepts, and why each piece decodes identically:
//
//   - The exact key sequence recordWire's marshaller emits: id, session, ts, target, approach,
//     reason, descriptor{normalized_path, symbol, approach_class, reason_hash}, evidence,
//     depends_on[{path, hash}...], scope, status, then stale_since and stale_because only if
//     present, then source. Every key is spelled exactly as its field's tag and appears once, so
//     each lands in the one field encoding/json would put it in; no key the union adds ("op",
//     "because") can appear, so the line is a record line and Op stays "".
//   - Strings holding no control byte and no \u escape, whose raw bytes are valid UTF-8. For those
//     encoding/json's unquote copies every byte verbatim and maps the eight single-character
//     escapes to the byte each names, which is exactly what this does. A string it could decode
//     differently — an invalid byte json would replace with U+FFFD, a \u escape and its surrogate
//     rules — is declined instead.
//   - Integers with no sign (source) or an optional minus (ts, stale_since), no leading zero, no
//     fraction and no exponent — the JSON grammar's integer form — and short enough that they
//     cannot overflow: at most 18 digits for an int64, at most three and no more than 255 for the
//     uint8 SourceKind. Anything longer is declined, and json.Unmarshal decides it.
//   - Arrays of those, decoded to a non-nil slice even when empty, as encoding/json's []
//     decoding produces.
//
// Nothing is written to the caller's logLine until the whole line has matched, so a declined
// line leaves it as the zero value json.Unmarshal must start from.

// maxWireDigits is the longest integer the fast path parses itself: every 18-digit decimal fits an
// int64, whose largest value has 19 digits. A longer one is declined rather than range-checked
// here, so the overflow rules stay encoding/json's.
const maxWireDigits = 18

// maxSourceDigits and maxSourceValue bound the uint8 SourceKind the same way.
const (
	maxSourceDigits = 3
	maxSourceValue  = 1<<8 - 1
)

// maxInternedWireStrings bounds wireInterner's table. The fields it interns are low-cardinality
// by construction — one session per ledger, two scopes, two statuses, a small set of approach
// classes — and the bound is what keeps a log that happens to vary them from growing the table
// without limit. Past it, strings are simply allocated as they always were.
const maxInternedWireStrings = 1 << 8

// wireInterner shares one copy of a repeated string across the records of one replay. Strings are
// immutable, so which backing array a string value points at is invisible to every reader; what
// changes is only how many copies of "active" a 20 000-line log leaves resident.
type wireInterner struct{ m map[string]string }

// intern returns string(b), reusing an earlier copy when there is one. The map lookup through a
// string(b) conversion does not allocate.
func (in *wireInterner) intern(b []byte) string {
	if s, ok := in.m[string(b)]; ok {
		return s
	}
	s := string(b)
	if in.m == nil {
		in.m = make(map[string]string)
	}
	if len(in.m) < maxInternedWireStrings {
		in.m[s] = s
	}
	return s
}

// lineDecoder is a cursor over one trimmed log line.
type lineDecoder struct {
	b  []byte
	i  int
	in *wireInterner
	// scratch holds an escaped string's decoded bytes until they are copied into a string.
	scratch []byte
}

// decodeRecordLineFast decodes line into ln and reports true when line is a record line in the
// canonical shape described at the top of this file; otherwise it reports false and leaves ln
// untouched. in may be shared across the lines of one replay.
func decodeRecordLineFast(line []byte, ln *logLine, in *wireInterner) bool {
	d := lineDecoder{b: line, in: in}
	var w recordWire
	if !d.lit(`{"id":`) || !d.str(&w.ID) ||
		!d.lit(`,"session":`) || !d.internStr(&w.Session) ||
		!d.lit(`,"ts":`) || !d.int64(&w.TS) ||
		!d.lit(`,"target":`) || !d.str(&w.Target) ||
		!d.lit(`,"approach":`) || !d.str(&w.Approach) ||
		!d.lit(`,"reason":`) || !d.str(&w.Reason) ||
		!d.lit(`,"descriptor":{"normalized_path":`) || !d.str(&w.Desc.NormalizedPath) ||
		!d.lit(`,"symbol":`) || !d.str(&w.Desc.Symbol) ||
		!d.lit(`,"approach_class":`) || !d.internStr(&w.Desc.ApproachClass) ||
		!d.lit(`,"reason_hash":`) || !d.str(&w.Desc.ReasonHash) ||
		!d.lit(`},"evidence":`) || !d.str(&w.Evidence) ||
		!d.lit(`,"depends_on":`) || !d.deps(&w.DependsOn) ||
		!d.lit(`,"scope":`) || !d.internStr(&w.Scope) ||
		!d.lit(`,"status":`) || !d.internStr(&w.Status) {
		return false
	}
	// The two omitempty keys, each either absent or present exactly once, in marshal order.
	if d.lit(`,"stale_since":`) && !d.int64(&w.StaleSince) {
		return false
	}
	if d.lit(`,"stale_because":`) && !d.strs(&w.StaleBecause) {
		return false
	}
	if !d.lit(`,"source":`) || !d.source(&w.Source) || !d.lit(`}`) || d.i != len(d.b) {
		return false
	}
	*ln = logLine{recordWire: w}
	return true
}

// lit consumes s when the line continues with exactly s, and consumes nothing otherwise.
func (d *lineDecoder) lit(s string) bool {
	if len(d.b)-d.i < len(s) || string(d.b[d.i:d.i+len(s)]) != s {
		return false
	}
	d.i += len(s)
	return true
}

// rawString consumes one JSON string and returns the bytes between its quotes, escapes still in
// place, and whether it held any. It declines — reports ok false — a control byte, a \u escape,
// any other backslash sequence encoding/json would reject, an unterminated string, and raw bytes
// that are not valid UTF-8.
//
// Checking the raw span with utf8.Valid is checking every unescaped run: an escape is two ASCII
// bytes, and a multi-byte UTF-8 sequence is made only of bytes at or above 0x80, so no sequence
// can straddle one and the span is valid exactly when each run between escapes is.
func (d *lineDecoder) rawString() (raw []byte, escaped, ok bool) {
	if d.i >= len(d.b) || d.b[d.i] != '"' {
		return nil, false, false
	}
	start := d.i + 1
	nonASCII := false
	for j := start; j < len(d.b); j++ {
		switch c := d.b[j]; {
		case c == '"':
			raw = d.b[start:j]
			if nonASCII && !utf8.Valid(raw) {
				return nil, false, false
			}
			d.i = j + 1
			return raw, escaped, true
		case c == '\\':
			if j+1 >= len(d.b) {
				return nil, false, false
			}
			switch d.b[j+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				escaped = true
				j++
			default:
				return nil, false, false
			}
		case c < ' ':
			return nil, false, false
		case c >= utf8.RuneSelf:
			nonASCII = true
		}
	}
	return nil, false, false
}

// unescape appends raw to dst with its single-character escapes decoded. raw has already passed
// rawString, so every backslash is followed by one of the eight characters it admits.
func unescape(dst, raw []byte) []byte {
	for j := 0; j < len(raw); j++ {
		c := raw[j]
		if c != '\\' {
			dst = append(dst, c)
			continue
		}
		j++
		switch raw[j] {
		case 'b':
			dst = append(dst, '\b')
		case 'f':
			dst = append(dst, '\f')
		case 'n':
			dst = append(dst, '\n')
		case 'r':
			dst = append(dst, '\r')
		case 't':
			dst = append(dst, '\t')
		default: // '"', '\\' and '/' stand for themselves
			dst = append(dst, raw[j])
		}
	}
	return dst
}

// strBytes consumes one string and returns its decoded bytes, which alias either the line or the
// scratch buffer and are valid only until the next call.
func (d *lineDecoder) strBytes() ([]byte, bool) {
	raw, escaped, ok := d.rawString()
	if !ok {
		return nil, false
	}
	if !escaped {
		return raw, true
	}
	d.scratch = unescape(d.scratch[:0], raw)
	return d.scratch, true
}

// str consumes one string into *dst.
func (d *lineDecoder) str(dst *string) bool {
	b, ok := d.strBytes()
	if ok {
		*dst = string(b)
	}
	return ok
}

// internStr consumes one string into *dst through the replay's interner.
func (d *lineDecoder) internStr(dst *string) bool {
	b, ok := d.strBytes()
	if ok {
		*dst = d.in.intern(b)
	}
	return ok
}

// digits consumes a JSON integer's digits — "0", or a non-zero digit followed by any digits — and
// returns them. A leading zero followed by more digits is not JSON; its "0" is consumed and the
// caller's next expected literal then fails on the digit after it, declining the line.
func (d *lineDecoder) digits() ([]byte, bool) {
	start := d.i
	if d.i >= len(d.b) || d.b[d.i] < '0' || d.b[d.i] > '9' {
		return nil, false
	}
	if d.b[d.i] == '0' {
		d.i++
		return d.b[start:d.i], true
	}
	for d.i < len(d.b) && d.b[d.i] >= '0' && d.b[d.i] <= '9' {
		d.i++
	}
	return d.b[start:d.i], true
}

// int64 consumes an optionally negative integer of at most maxWireDigits digits into *dst.
func (d *lineDecoder) int64(dst *int64) bool {
	neg := d.lit("-")
	ds, ok := d.digits()
	if !ok || len(ds) > maxWireDigits {
		return false
	}
	var n int64
	for _, c := range ds {
		n = n*10 + int64(c-'0')
	}
	if neg {
		n = -n
	}
	*dst = n
	return true
}

// source consumes an unsigned integer no larger than the uint8 SourceKind holds into *dst.
func (d *lineDecoder) source(dst *SourceKind) bool {
	ds, ok := d.digits()
	if !ok || len(ds) > maxSourceDigits {
		return false
	}
	n := 0
	for _, c := range ds {
		n = n*10 + int(c-'0')
	}
	if n > maxSourceValue {
		return false
	}
	*dst = SourceKind(n)
	return true
}

// deps consumes a depends_on array into *dst.
func (d *lineDecoder) deps(dst *[]depWire) bool {
	if !d.lit("[") {
		return false
	}
	out := []depWire{}
	if d.lit("]") {
		*dst = out
		return true
	}
	for {
		var w depWire
		if !d.lit(`{"path":`) || !d.str(&w.Path) || !d.lit(`,"hash":`) || !d.str(&w.Hash) || !d.lit("}") {
			return false
		}
		out = append(out, w)
		if d.lit("]") {
			*dst = out
			return true
		}
		if !d.lit(",") {
			return false
		}
	}
}

// strs consumes an array of strings into *dst.
func (d *lineDecoder) strs(dst *[]string) bool {
	if !d.lit("[") {
		return false
	}
	out := []string{}
	if d.lit("]") {
		*dst = out
		return true
	}
	for {
		var s string
		if !d.str(&s) {
			return false
		}
		out = append(out, s)
		if d.lit("]") {
			*dst = out
			return true
		}
		if !d.lit(",") {
			return false
		}
	}
}
