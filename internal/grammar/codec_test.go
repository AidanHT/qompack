package grammar_test

import (
	"encoding/binary"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// This file tests the SP-15 grammar codec against plans/sdd/V5-SP-15/contract.md §4.
//
// It asserts against constants it SPELLS ITSELF rather than against grammar's own unexported ones.
// That is Rule W-2's point and internal/canon/sketchwire_test.go's: a wire format checked with the
// producer's own helpers only proves the producer agrees with itself, and the whole value of a
// version tag and a magic number is that a future encoder change fails loudly instead of quietly
// re-keying every checkpoint on disk.

// The frame's fixed prefix, transcribed from contract §4 ("magic: the 16 bytes qompack-grammar
// followed by a NUL") rather than imported from the package under test.
const (
	wantMagic      = "qompack-grammar\x00"
	wantMagicLen   = 16
	wantVersionLen = 2
	wantHeaderLen  = wantMagicLen + wantVersionLen
	// wantCodecVersion is the version this build must write. It is a literal here on purpose: if
	// someone bumps grammar.CodecVersion, this test fails and the failure is the reminder that a
	// version bump owes the world a compatibility vector for the OLD version too.
	wantCodecVersion = 1
)

// codecVectorDir is where the version-tagged compatibility vectors live (contract §8's fixture
// layout: internal/grammar/testdata/codec/).
//
// They carry the .golden extension because .gitattributes marks *.golden as -text: these files are
// byte-exact frames containing NULs, and a checkout that "helpfully" translated a line ending inside
// one would corrupt the very thing they exist to pin. The repository's global rule is
// `* text=auto eol=lf`, so relying on git's NUL sniffing rather than on an explicit attribute would
// leave the fixtures' integrity to a heuristic.
const codecVectorDir = "testdata/codec"

// vectorPerm matches the permission every other committed golden in this tree is written with:
// these are source files, not runtime store files.
const vectorPerm = 0o644

// updateVectors reports whether -update was passed.
//
// The flag is looked up before being registered because other packages in this test binary may
// already have registered one (internal/testutil does, and internal/dag/golden_test.go documents the
// same collision), and two calls to flag.Bool with one name panic during init. Looking it up first
// turns the collision into what both sides wanted: a single -update driving every golden in the
// binary. It is spelled defensively here for a second reason specific to SP-15 — role A owns
// sequitur_test.go in this same package and may land its own goldens concurrently.
var updateVectors = registerCodecUpdateFlag()

// registerCodecUpdateFlag returns a reader for -update, registering it only if nothing else has.
func registerCodecUpdateFlag() func() bool {
	const name = "update"
	if f := flag.Lookup(name); f != nil {
		return func() bool { return f.Value.String() == "true" }
	}
	p := flag.Bool(name, false, "rewrite golden files under testdata/ instead of comparing against them")
	return func() bool { return *p }
}

// codecVectors are the committed compatibility cases. Each is deliberately something a naive
// encoder would get wrong:
//
//   - empty is the zero Snapshot, whose whole payload is three counts. It is the case that proves a
//     zeroed file and an empty grammar are not the same bytes.
//   - rules is an ordinary induced grammar with a rule-of-rules reference in a Body, so the fixture
//     pins that a Symbol containing a NUL (RuleRef's sigil) survives the round trip verbatim.
//   - edges carries every representational distinction at once: a nil Body beside an EMPTY one, a
//     multi-byte UTF-8 terminal, an embedded NUL in the middle of a terminal rather than at its
//     head, and a NextID far past the rules present.
func codecVectors() []struct {
	name string
	snap grammar.Snapshot
} {
	return []struct {
		name string
		snap grammar.Snapshot
	}{
		{name: "v1-empty", snap: grammar.Snapshot{}},
		{
			name: "v1-rules",
			snap: grammar.Snapshot{
				Rules: []grammar.Rule{
					{
						ID:        1,
						Body:      []grammar.Symbol{"Read", "Edit"},
						Uses:      4,
						Expansion: []grammar.Symbol{"Read", "Edit"},
						Span:      2,
					},
					{
						ID:        2,
						Body:      []grammar.Symbol{grammar.RuleRef(1), "Bash"},
						Uses:      3,
						Expansion: []grammar.Symbol{"Read", "Edit", "Bash"},
						Span:      3,
					},
				},
				Sequence: []grammar.Symbol{grammar.RuleRef(2), grammar.RuleRef(2), "user", grammar.RuleRef(2)},
				NextID:   3,
			},
		},
		{
			name: "v1-edges",
			snap: grammar.Snapshot{
				Rules: []grammar.Rule{
					{ID: 7, Body: nil, Uses: 2, Expansion: []grammar.Symbol{}, Span: 0},
					{ID: 8, Body: []grammar.Symbol{"", "тест", "a\x00b"}, Uses: 2, Expansion: nil, Span: 3},
				},
				Sequence: []grammar.Symbol{},
				NextID:   4096,
			},
		},
	}
}

// TestEncodeSnapshot_FrameHeader pins the header contract §4 freezes: sixteen bytes of magic ending
// in a NUL, then the version. A consumer that cannot recognize the first eighteen bytes cannot tell
// a grammar snapshot from any other blob in .qompack/, which is the whole reason a magic exists.
func TestEncodeSnapshot_FrameHeader(t *testing.T) {
	b := grammar.EncodeSnapshot(grammar.Snapshot{})

	require.GreaterOrEqual(t, len(b), wantHeaderLen)
	require.Equal(t, wantMagic, string(b[:wantMagicLen]))
	require.Len(t, wantMagic, wantMagicLen, "contract §4 fixes the magic at 16 bytes")
	require.EqualValues(t, 0, b[wantMagicLen-1], "the magic's last byte is the NUL sigil")
	require.EqualValues(t, wantCodecVersion, binary.LittleEndian.Uint16(b[wantMagicLen:wantHeaderLen]))
	require.EqualValues(t, wantCodecVersion, grammar.CodecVersion,
		"CodecVersion and the version this build writes must be the same number")
}

// TestEncodeSnapshot_Deterministic asserts contract §4's determinism requirement: equal Snapshots
// encode to equal bytes, every time.
//
// It matters because the encoder's input contains slices of strings and its output is what a
// checkpoint's content hash is taken over. A codec that reordered anything — or that leaked a map's
// iteration order, which is why the implementation contains no map at all — would produce a
// different digest for an unchanged grammar on every run, and every downstream identity check would
// quietly stop meaning anything.
func TestEncodeSnapshot_Deterministic(t *testing.T) {
	for _, v := range codecVectors() {
		t.Run(v.name, func(t *testing.T) {
			first := grammar.EncodeSnapshot(v.snap)
			for i := 0; i < 8; i++ {
				require.Equal(t, first, grammar.EncodeSnapshot(v.snap), "encode #%d differs", i)
			}
			// An independently constructed equal value must encode identically too: equality of the
			// VALUE is the contract, not identity of the variable.
			require.Equal(t, first, grammar.EncodeSnapshot(cloneSnapshot(v.snap)))
		})
	}
}

// TestCodec_RoundTripIdentity asserts contract §4's round-trip identity over the cases a hand-rolled
// codec is most likely to break: the empty grammar, rule references (a Symbol whose first byte is a
// NUL), and the nil-versus-empty slice distinction that Go's own equality treats as significant.
func TestCodec_RoundTripIdentity(t *testing.T) {
	for _, v := range codecVectors() {
		t.Run(v.name, func(t *testing.T) {
			got, err := grammar.DecodeSnapshot(grammar.EncodeSnapshot(v.snap))
			require.NoError(t, err)
			require.Equal(t, v.snap, got)
		})
	}

	t.Run("nil_and_empty_are_distinct", func(t *testing.T) {
		// The two Snapshots below are NOT equal in Go, so a codec that collapsed them would break
		// identity for one of them. Asserting the encodings differ is what proves the distinction is
		// carried on the wire rather than restored by luck.
		nilled := grammar.Snapshot{Rules: nil, Sequence: nil}
		emptied := grammar.Snapshot{Rules: []grammar.Rule{}, Sequence: []grammar.Symbol{}}
		require.NotEqual(t, nilled, emptied, "fixture sanity: Go distinguishes nil from empty")
		require.NotEqual(t, grammar.EncodeSnapshot(nilled), grammar.EncodeSnapshot(emptied))

		gotNil, err := grammar.DecodeSnapshot(grammar.EncodeSnapshot(nilled))
		require.NoError(t, err)
		require.Equal(t, nilled, gotNil)

		gotEmpty, err := grammar.DecodeSnapshot(grammar.EncodeSnapshot(emptied))
		require.NoError(t, err)
		require.Equal(t, emptied, gotEmpty)
	})
}

// TestCodec_RoundTripIdentityProperty states round-trip identity over generated Snapshots rather
// than fixtures, because "for every valid s" is the contract's wording and a table can only ever
// demonstrate it for the cases somebody thought of. The generator deliberately produces empty and
// nil slices, empty strings, rule references and arbitrary Unicode terminals.
func TestCodec_RoundTripIdentityProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		snap := drawSnapshot(rt)
		encoded := grammar.EncodeSnapshot(snap)
		decoded, err := grammar.DecodeSnapshot(encoded)
		require.NoError(rt, err)
		require.Equal(rt, snap, decoded)
		// Re-encoding the decoded value must reproduce the same bytes; together with the equality
		// above this rules out a codec that round-trips values while drifting on the wire.
		require.Equal(rt, encoded, grammar.EncodeSnapshot(decoded))
	})
}

// TestCodec_CompatibilityVectors is the on-disk half of contract §4 and the reason
// internal/grammar/testdata/codec/ exists (contract §8).
//
// Every other test in this file compares this build against itself. These vectors compare it against
// the bytes a previous build committed, in both directions: the encoder must still PRODUCE them, and
// the reader must still ACCEPT them. A change that breaks either one is a change that silently
// invalidates every grammar snapshot already inside a user's .qompack/checkpoints, and the point of
// the fixture is that such a change cannot be made quietly. Regenerate with -update only when the
// version tag moves, and say why in the commit.
func TestCodec_CompatibilityVectors(t *testing.T) {
	for _, v := range codecVectors() {
		t.Run(v.name, func(t *testing.T) {
			path := filepath.Join(codecVectorDir, v.name+".golden")
			got := grammar.EncodeSnapshot(v.snap)

			if updateVectors() {
				require.NoError(t, os.MkdirAll(codecVectorDir, 0o755))
				require.NoError(t, os.WriteFile(path, got, vectorPerm))
				t.Logf("updated %s (%d bytes)", path, len(got))
				return
			}

			want, err := os.ReadFile(path)
			require.NoError(t, err, "compatibility vector missing: %s (regenerate with -update)", path)
			require.Equal(t, want, got,
				"%s drifted: this build no longer produces the committed v%d frame, which means every "+
					"grammar snapshot already on disk decodes differently", path, wantCodecVersion)

			decoded, err := grammar.DecodeSnapshot(want)
			require.NoError(t, err, "this build can no longer READ the committed v%d frame", wantCodecVersion)
			require.Equal(t, v.snap, decoded)
		})
	}
}

// TestDecodeSnapshot_RejectsBadFrames is the compatibility reader of contract §4: every malformed
// input reports core.ErrDegraded and returns the zero Snapshot, and none of them panics.
//
// The zero-Snapshot half is asserted alongside the error because "returns an empty grammar" and
// "reports a problem" are the two outcomes this function exists to keep apart. An empty grammar is
// indistinguishable from a session that repeated nothing, so a decoder that returned one without an
// error would report "no loops" for a corrupted checkpoint and nothing downstream could tell.
func TestDecodeSnapshot_RejectsBadFrames(t *testing.T) {
	valid := grammar.EncodeSnapshot(codecVectors()[1].snap)

	cases := []struct {
		name string
		in   []byte
	}{
		{name: "nil", in: nil},
		{name: "empty", in: []byte{}},
		{name: "header_only_short", in: []byte(wantMagic)},
		{name: "another_artifacts_magic", in: append(otherArtifactHeader(), valid[wantHeaderLen:]...)},
		{name: "magic_without_nul", in: append([]byte("qompack-grammar!\x01\x00"), valid[wantHeaderLen:]...)},
		{name: "all_zero_frame", in: make([]byte, len(valid))},
		// A HIGHER version is contract §4's named case: a newer plugin's file must not be half-read
		// by this build.
		{name: "future_version", in: frameWithVersion(t, wantCodecVersion+1, valid[wantHeaderLen:])},
		{name: "far_future_version", in: frameWithVersion(t, 0xFFFF, valid[wantHeaderLen:])},
		// A ZERO version is not an older layout — no build ever wrote one — it is a zeroed or
		// partially written header. Accepting it would mean guessing that a zeroed header is
		// followed by a v1 payload, and a fully zeroed frame happens to BE a well-formed empty
		// grammar, which is exactly the silently-empty outcome the contract forbids.
		{name: "zero_version", in: frameWithVersion(t, 0, valid[wantHeaderLen:])},
		{name: "trailing_bytes", in: append(append([]byte{}, valid...), 0x00)},
		{name: "two_frames_concatenated", in: append(append([]byte{}, valid...), valid...)},
		// A forged count must be refused rather than reaching make(): the decoder bounds every
		// declared count by the bytes actually remaining.
		{name: "forged_rule_count", in: frameWithPayload(t, binary.AppendUvarint(nil, 1<<40))},
		{name: "forged_symbol_length", in: frameWithPayload(t, forgedSymbolPayload())},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := grammar.DecodeSnapshot(tc.in)
			require.Error(t, err)
			require.ErrorIs(t, err, core.ErrDegraded,
				"contract §4 admits only the four core sentinels; this reader reports ErrDegraded")
			require.Equal(t, grammar.Snapshot{}, got, "a rejected frame must not yield a usable Snapshot")
		})
	}
}

// TestDecodeSnapshot_RejectsEveryTruncation sweeps every proper prefix of a valid frame. Truncation
// is the realistic corruption for this format — a checkpoint write interrupted by a crash or a full
// disk — and it is the one a length-prefixed decoder can get wrong in a way that still returns
// something plausible, because a prefix of a valid frame is itself well-formed right up to the point
// where it stops.
func TestDecodeSnapshot_RejectsEveryTruncation(t *testing.T) {
	for _, v := range codecVectors() {
		t.Run(v.name, func(t *testing.T) {
			full := grammar.EncodeSnapshot(v.snap)
			for i := 0; i < len(full); i++ {
				got, err := grammar.DecodeSnapshot(full[:i])
				require.Error(t, err, "prefix of %d/%d bytes decoded without error", i, len(full))
				require.ErrorIs(t, err, core.ErrDegraded)
				require.Equal(t, grammar.Snapshot{}, got)
			}
		})
	}
}

// TestDecodeSnapshot_DoesNotAliasInput asserts the decoder copies rather than aliases.
//
// A decoder that returned Symbols pointing into the caller's buffer would work in every test that
// keeps the buffer alive and fail only where the buffer is reused — a pooled read buffer, a
// re-filled scratch slice — which is to say it would fail in production and pass in CI.
func TestDecodeSnapshot_DoesNotAliasInput(t *testing.T) {
	snap := codecVectors()[1].snap
	buf := grammar.EncodeSnapshot(snap)

	decoded, err := grammar.DecodeSnapshot(buf)
	require.NoError(t, err)

	for i := range buf {
		buf[i] = 0xFF
	}
	require.Equal(t, snap, decoded, "the decoded Snapshot changed when the input buffer was overwritten")
}

// otherArtifactHeader is an eighteen-byte header that is not a grammar frame: internal/sketch's QPKS
// magic followed by zeros. Pointing this decoder at a DIFFERENT artifact in .qompack/ is the
// realistic way to meet a bad magic — a mixed-up path, a restored backup, a checkpoint field holding
// the wrong handle — and it has to be refused rather than half-read.
func otherArtifactHeader() []byte {
	h := make([]byte, wantHeaderLen)
	copy(h, "QPKS")
	return h
}

// frameWithVersion builds a frame with a valid magic, an arbitrary version and the given payload.
func frameWithVersion(t *testing.T, version uint16, payload []byte) []byte {
	t.Helper()
	out := make([]byte, 0, wantHeaderLen+len(payload))
	out = append(out, wantMagic...)
	out = binary.LittleEndian.AppendUint16(out, version)
	return append(out, payload...)
}

// frameWithPayload builds a well-formed current-version frame around a hand-written payload.
func frameWithPayload(t *testing.T, payload []byte) []byte {
	t.Helper()
	return frameWithVersion(t, wantCodecVersion, payload)
}

// forgedSymbolPayload is a payload whose rule list is absent, whose sequence declares one symbol,
// and whose symbol declares a length far past the end of the buffer.
func forgedSymbolPayload() []byte {
	out := []byte{0x00}                    // rules: nil
	out = binary.AppendUvarint(out, 2)     // sequence: present, one entry (count is len+1)
	out = binary.AppendUvarint(out, 1<<40) // that entry's declared byte length
	return append(out, 'a')                // ...followed by one byte
}

// cloneSnapshot deep-copies s, so an equality test can compare two structurally equal values that
// share no backing array.
func cloneSnapshot(s grammar.Snapshot) grammar.Snapshot {
	out := grammar.Snapshot{NextID: s.NextID}
	if s.Sequence != nil {
		out.Sequence = append([]grammar.Symbol{}, s.Sequence...)
	}
	if s.Rules != nil {
		out.Rules = make([]grammar.Rule, len(s.Rules))
		for i, r := range s.Rules {
			out.Rules[i] = grammar.Rule{ID: r.ID, Uses: r.Uses, Span: r.Span}
			if r.Body != nil {
				out.Rules[i].Body = append([]grammar.Symbol{}, r.Body...)
			}
			if r.Expansion != nil {
				out.Rules[i].Expansion = append([]grammar.Symbol{}, r.Expansion...)
			}
		}
	}
	return out
}

// drawSnapshot generates an arbitrary Snapshot for the property test.
func drawSnapshot(rt *rapid.T) grammar.Snapshot {
	n := rapid.IntRange(0, 4).Draw(rt, "rules")
	var rules []grammar.Rule
	if rapid.Bool().Draw(rt, "rulesPresent") {
		rules = make([]grammar.Rule, n)
		for i := range rules {
			rules[i] = grammar.Rule{
				ID:        grammar.RuleID(rapid.IntRange(-4, 1<<20).Draw(rt, "ruleID")),
				Body:      drawSymbols(rt, "body"),
				Uses:      rapid.IntRange(-4, 1<<20).Draw(rt, "uses"),
				Expansion: drawSymbols(rt, "expansion"),
				Span:      rapid.IntRange(-4, 1<<20).Draw(rt, "span"),
			}
		}
	}
	return grammar.Snapshot{
		Rules:    rules,
		Sequence: drawSymbols(rt, "sequence"),
		NextID:   grammar.RuleID(rapid.IntRange(-4, 1<<20).Draw(rt, "nextID")),
	}
}

// drawSymbols generates a symbol slice that is sometimes nil, sometimes explicitly empty, and
// otherwise a mix of rule references and arbitrary terminals. Both flavours of "no symbols" are
// generated on purpose: they are distinct Go values and the codec claims to preserve the difference.
func drawSymbols(rt *rapid.T, label string) []grammar.Symbol {
	if rapid.Bool().Draw(rt, label+"Nil") {
		return nil
	}
	n := rapid.IntRange(0, 6).Draw(rt, label+"Len")
	out := make([]grammar.Symbol, n)
	for i := range out {
		if rapid.Bool().Draw(rt, label+"IsRef") {
			out[i] = grammar.RuleRef(grammar.RuleID(rapid.IntRange(0, 1<<16).Draw(rt, label+"RefID")))
			continue
		}
		out[i] = grammar.Symbol(rapid.String().Draw(rt, label+"Terminal"))
	}
	return out
}

// requireKnownCodecError is the same admissibility check grammartest.requireKnownError applies, kept
// here so this package's own tests hold the codec to the four-sentinel rule without importing the
// conformance suite (which imports this package).
func requireKnownCodecError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	require.True(t,
		errors.Is(err, core.ErrNotImplemented) || errors.Is(err, core.ErrNotFound) ||
			errors.Is(err, core.ErrBudget) || errors.Is(err, core.ErrDegraded),
		"unexpected error: %v", err)
}

// TestDecodeSnapshot_OnlyEverReportsKnownSentinels walks the malformed cases once more through the
// suite's own admissibility rule. It is a separate test from the assertions above because those pin
// ErrDegraded specifically, while this one pins the weaker property the conformance suite enforces —
// and a future decoder that legitimately started reporting ErrBudget for an oversized frame should
// fail exactly one of these two tests, not both.
func TestDecodeSnapshot_OnlyEverReportsKnownSentinels(t *testing.T) {
	valid := grammar.EncodeSnapshot(codecVectors()[2].snap)
	for i := 0; i <= len(valid); i++ {
		_, err := grammar.DecodeSnapshot(valid[:i])
		requireKnownCodecError(t, err)
	}
}
