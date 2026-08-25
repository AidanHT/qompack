package negknow

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The domain-separation strings this package mints hashes under, and the field separator its
// key preimages are written with.
//
// The identity key's domain is core.DomainNegKnow ("qompack.neg.v1"), owned by internal/core's
// domain registry and already used by the shipped Descriptor.Key. It is deliberately NOT
// redeclared here: a second spelling of the same string is a second thing to keep in sync.
//
// Changing one of these strings re-keys every derived value already on disk. Treat them as a wire
// format, not as identifiers.
const (
	// domainMatch is the domain of MatchKey, the reason-independent key Query tests.
	domainMatch = "qompack.neg.match.v1"
	// domainReason is the domain of the normalized-reason digest in Descriptor.ReasonHash.
	domainReason = "qompack.neg.reason.v1"
	// domainRecordID is the domain of a Record's own id. Declared here with the rest of the
	// package's domain set; the ledger that mints record ids is the consumer.
	domainRecordID = "qompack.neg.id.v1"
	// domainDedup is the domain of the append-time duplicate-suppression digest. Declared here
	// with the rest of the package's domain set; the ledger's append path is the consumer.
	domainDedup = "qompack.neg.dedup.v1"
	// fieldSep is ASCII Unit Separator: a control character that cannot legitimately appear in a
	// normalized path, a symbol or an approach class, which is what makes it an unambiguous
	// delimiter between them.
	fieldSep = byte(0x1F)
)

// symbolPattern is what a ':'-suffix must look like to be treated as a symbol rather than as part
// of the path. Requiring an identifier-shaped suffix is what keeps a Windows drive-letter path
// intact: in "C:/proj/src/auth.ts" the text after the last colon is "/proj/src/auth.ts", which
// does not match, so no split happens.
var symbolPattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$.]*$`)

// lineNumberPattern is what a ':'-suffix must look like to be treated as a line number and
// DROPPED (controller ruling R17). "pkg/mod.go:12" is a file:line reference, not a symbol
// reference: the line number is a cursor position that moves with every edit above it, so keeping
// it in the normalized path would give the same file a fresh bloom key on every unrelated change.
//
// The two patterns are disjoint by construction — symbolPattern requires a non-digit first
// character — so the order in which they are tested does not change any answer.
var lineNumberPattern = regexp.MustCompile(`^[0-9]+$`)

// SplitTarget splits a free-text target into its normalized path and its symbol, applying these
// rules in order (00-ARCHITECTURE.md §5.10):
//
//  1. Trim spaces. An empty target yields ("", "").
//  2. If the target contains '#', split at the FIRST '#': left is the path, right is the symbol.
//  3. Otherwise find the LAST ':' and decide three ways on what follows it (ruling R17):
//     a SYMBOL suffix (symbolPattern) splits — the prefix is the path, the suffix is the symbol;
//     a LINE-NUMBER suffix (lineNumberPattern) is dropped — the prefix is the path and the symbol
//     is ""; anything else is NOT a split — the whole string is the path and the symbol is "".
//  4. The path is run through paths.Key (project-relative, forward-slash, case-folded on Windows
//     and macOS). The symbol is kept verbatim apart from surrounding-space trimming.
//
// The three-way rule 3 is what lets "src/auth.ts:refreshToken", "pkg/mod.go:12" and
// "C:/proj/src/auth.ts" all be right at once: the first names a symbol, the second names a line
// that must not enter the key (it moves with every edit above it), and the third's suffix
// "/proj/src/auth.ts" is neither, so a Windows drive-letter path survives whole. ":12" has a
// line-number suffix over an empty prefix and yields ("", ""), the same as any other target with
// nothing left to name.
//
// A caller holding an absolute path calls CanonicalizeAt, which runs paths.Norm against the
// project root before this key-folding step.
func SplitTarget(target string) (path, symbol string) {
	raw, sym := splitTargetRaw(target)
	return paths.Key(raw), sym
}

// splitTargetRaw is SplitTarget's rules 1 to 3, without rule 4's paths.Key fold. CanonicalizeAt
// needs the unfolded path so it can call paths.Norm against the project root first: folding an
// absolute path before measuring it against an unfolded root would make filepath.Rel compare two
// differently-cased spellings of the same directory and report an escape.
func splitTargetRaw(target string) (path, symbol string) {
	t := strings.TrimSpace(target)
	if t == "" {
		return "", ""
	}
	if i := strings.IndexByte(t, '#'); i >= 0 {
		return strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+1:])
	}
	if i := strings.LastIndexByte(t, ':'); i >= 0 {
		switch suffix := t[i+1:]; {
		case symbolPattern.MatchString(suffix):
			return strings.TrimSpace(t[:i]), suffix
		case lineNumberPattern.MatchString(suffix):
			// A line number is dropped, not kept: it is a cursor position, and keeping it would
			// re-key the same elimination on every edit above it.
			return strings.TrimSpace(t[:i]), ""
		}
	}
	return t, ""
}

// CanonicalizeAt is Canonicalize for a caller holding an absolute path: the target's path half is
// normalized against projectRoot first, so the same file canonicalizes identically whether it was
// named absolutely or relative to the project. A path that escapes projectRoot is an error
// wrapping core.ErrNotFound; an empty target has no path to normalize and is not an error.
func CanonicalizeAt(projectRoot, target, approach, reason string) (Descriptor, error) {
	raw, sym := splitTargetRaw(target)
	p := ""
	if raw != "" {
		normalized, err := paths.Norm(projectRoot, raw)
		if err != nil {
			return Descriptor{}, err
		}
		p = paths.Key(normalized)
	}
	return Descriptor{
		NormalizedPath: p,
		Symbol:         sym,
		ApproachClass:  ApproachClass(approach),
		ReasonHash:     reasonHash(reason),
	}, nil
}

// reasonHash is the domain-separated digest of a normalized reason: lowercased, with every
// whitespace run collapsed to one space and the ends trimmed. Two spellings of the same sentence
// therefore produce one hash, so re-recording the same elimination does not mint a second
// identity key.
//
// reasonHash("") is a fixed, documented value — core.HashBytes(domainReason, []byte("")) — and
// Query always produces it, because Query has no reason argument. That is precisely why MatchKey
// excludes ReasonHash.
func reasonHash(reason string) core.Hash {
	return core.HashBytes(domainReason, []byte(collapseWS(strings.ToLower(reason))))
}

// collapseWS replaces every run of whitespace with a single space and trims the ends.
func collapseWS(s string) string { return strings.Join(strings.Fields(s), " ") }

// sanitizeField makes fieldSep unambiguous: 0x1F is a control character that cannot legitimately
// appear in a path, a symbol or an approach class, so replacing it removes the only way a caller
// could forge a colliding MatchKey by embedding a separator in a field. It is used by MatchKey
// ONLY — Key's byte layout is frozen and takes its fields raw.
func sanitizeField(s string) string { return strings.ReplaceAll(s, string(rune(fieldSep)), " ") }

// MatchKey returns d's reason-independent bloom key: the domain-separated digest of the path, the
// symbol and the approach class, separated by fieldSep and with every embedded separator
// sanitized away.
//
// This is the key Query tests, and it excludes ReasonHash on purpose: already_tried is asked
// without a reason, so a key that included one could never be looked up. Every record therefore
// contributes exactly two bloom keys — Key for identity and record-level dedup, MatchKey for
// membership — and effective bloom capacity consumption is 2 x records.
//
// The returned slice is a fresh copy the caller may mutate freely.
func (d Descriptor) MatchKey() []byte {
	var b bytes.Buffer
	b.WriteString(sanitizeField(d.NormalizedPath))
	b.WriteByte(fieldSep)
	b.WriteString(sanitizeField(d.Symbol))
	b.WriteByte(fieldSep)
	b.WriteString(sanitizeField(d.ApproachClass))
	h := core.HashBytes(domainMatch, b.Bytes())
	out := make([]byte, len(h))
	copy(out, h[:])
	return out
}

// MatchHex returns MatchKey as lowercase hex: the in-memory index key the ledger maps to records.
func (d Descriptor) MatchHex() string { return hex.EncodeToString(d.MatchKey()) }

// descriptorWire is Descriptor's JSON shape. It exists so MarshalJSON and UnmarshalJSON can be
// lenient about the reason hash without re-spelling the field names: the tags here are the frozen
// tags of Descriptor itself, in the frozen order, and the emitted bytes are byte-for-byte what
// the struct tags alone would have produced.
type descriptorWire struct {
	NormalizedPath string `json:"normalized_path"`
	Symbol         string `json:"symbol"`
	ApproachClass  string `json:"approach_class"`
	ReasonHash     string `json:"reason_hash"`
}

// MarshalJSON emits the four frozen keys in their frozen order.
//
// §8.3's symbol_or_null is the empty string in Go and in JSON (00-ARCHITECTURE.md §5.10's
// `Symbol string // "" == null`), so "symbol" is always present and always a string, never null:
// a key that appears only sometimes is a second wire shape for consumers to handle.
func (d Descriptor) MarshalJSON() ([]byte, error) {
	return json.Marshal(descriptorWire{
		NormalizedPath: d.NormalizedPath,
		Symbol:         d.Symbol,
		ApproachClass:  d.ApproachClass,
		ReasonHash:     d.ReasonHash.String(),
	})
}

// UnmarshalJSON accepts null, a missing key and "" for "symbol" and normalizes all three to "".
//
// An unparseable reason_hash decodes to the zero Hash rather than failing: eliminations live in
// an append-only log that is read line by line, and one malformed digest must degrade that record
// to "no reason hash" rather than make the surrounding record unreadable. The zero Hash is already
// this package's "unset" sentinel, and core.Hash.IsZero is how a caller tests for it.
func (d *Descriptor) UnmarshalJSON(b []byte) error {
	var w descriptorWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	h, err := core.ParseHash(w.ReasonHash)
	if err != nil {
		h = core.Hash{}
	}
	*d = Descriptor{
		NormalizedPath: w.NormalizedPath,
		Symbol:         w.Symbol,
		ApproachClass:  w.ApproachClass,
		ReasonHash:     h,
	}
	return nil
}
