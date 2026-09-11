package negknow

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// The reference* functions are the key derivations exactly as they stood before SP09-D1 rebuilt
// them on stack buffers — bytes.Buffer, fmt.Fprintf and hex.EncodeToString, copied verbatim — and
// the tests below hold the new constructions to them. The goldens (TestDescriptorKey_Stable,
// TestRecordID_Stable, the descriptor rows) already freeze a handful of outputs; these cover every
// input rapid can draw, including fields that carry fieldSep and preimages long enough to spill
// out of the stack buffer.

func referenceKey(d Descriptor) []byte {
	var b bytes.Buffer
	b.WriteString(d.NormalizedPath)
	b.WriteByte(0x1f)
	b.WriteString(d.Symbol)
	b.WriteByte(0x1f)
	b.WriteString(d.ApproachClass)
	b.WriteByte(0x1f)
	b.Write(d.ReasonHash[:])
	h := core.HashBytes(core.DomainNegKnow, b.Bytes())
	return h[:]
}

func referenceMatchKey(d Descriptor) []byte {
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

func referenceDedupHex(r Record) string {
	var b bytes.Buffer
	b.WriteString(string(r.Session))
	b.WriteByte(fieldSep)
	b.WriteString(string(r.Scope))
	b.WriteByte(fieldSep)
	b.Write(referenceKey(r.Desc))
	h := core.HashBytes(domainDedup, b.Bytes())
	return hex.EncodeToString(h[:])
}

func referenceRecordID(sess core.SessionID, ts core.UnixMilli, d Descriptor) string {
	var b bytes.Buffer
	b.WriteString(string(sess))
	b.WriteByte(fieldSep)
	fmt.Fprintf(&b, "%d", int64(ts))
	b.WriteByte(fieldSep)
	b.Write(referenceKey(d))
	h := core.HashBytes(domainRecordID, b.Bytes())
	return recordIDPrefix + hex.EncodeToString(h[:])[:recordIDHexLen]
}

// keyTextGen draws the text a descriptor field can hold: arbitrary Unicode, strings built around
// fieldSep, and strings long enough that the whole preimage outgrows keyPreimageBuf.
func keyTextGen() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.String(),
		rapid.SampledFrom([]string{"", "\x1f", "a\x1fb", "\x1f\x1f", strings.Repeat("p", keyPreimageBuf+1)}),
		rapid.StringN(0, 2*keyPreimageBuf, -1),
	)
}

// TestKeyDerivation_MatchesBufferConstruction holds Key, keyHash, MatchKey, matchHash, MatchHex,
// dedupHex, dedupHexKey and recordID to the reference constructions over arbitrary input.
func TestKeyDerivation_MatchesBufferConstruction(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		var rh core.Hash
		copy(rh[:], rapid.SliceOfN(rapid.Byte(), len(rh), len(rh)).Draw(rt, "reason_hash"))
		d := Descriptor{
			NormalizedPath: keyTextGen().Draw(rt, "path"),
			Symbol:         keyTextGen().Draw(rt, "symbol"),
			ApproachClass:  keyTextGen().Draw(rt, "class"),
			ReasonHash:     rh,
		}
		r := Record{
			Session: core.SessionID(keyTextGen().Draw(rt, "session")),
			Scope:   Scope(keyTextGen().Draw(rt, "scope")),
			Desc:    d,
		}
		ts := core.UnixMilli(rapid.Int64().Draw(rt, "ts"))

		wantKey, wantMatch := referenceKey(d), referenceMatchKey(d)
		if got := d.Key(); !bytes.Equal(got, wantKey) {
			rt.Fatalf("Key %x, want %x", got, wantKey)
		}
		if got := d.keyHash(); !bytes.Equal(got[:], wantKey) {
			rt.Fatalf("keyHash %x, want %x", got, wantKey)
		}
		if got := d.MatchKey(); !bytes.Equal(got, wantMatch) {
			rt.Fatalf("MatchKey %x, want %x", got, wantMatch)
		}
		if got := d.matchHash(); !bytes.Equal(got[:], wantMatch) {
			rt.Fatalf("matchHash %x, want %x", got, wantMatch)
		}
		if got, want := d.MatchHex(), hex.EncodeToString(wantMatch); got != want {
			rt.Fatalf("MatchHex %s, want %s", got, want)
		}
		if got, want := dedupHex(r), referenceDedupHex(r); got != want {
			rt.Fatalf("dedupHex %s, want %s", got, want)
		}
		if got, want := dedupHexKey(r.Session, r.Scope, d.keyHash()), referenceDedupHex(r); got != want {
			rt.Fatalf("dedupHexKey %s, want %s", got, want)
		}
		if got, want := recordID(r.Session, ts, d), referenceRecordID(r.Session, ts, d); got != want {
			rt.Fatalf("recordID %s, want %s", got, want)
		}
	})
}

// TestKeyDerivation_FreshSlices pins the aliasing half of Key's and MatchKey's contracts under the
// new construction: each call returns its own slice, so a caller scribbling on one cannot change
// what the next call returns.
func TestKeyDerivation_FreshSlices(t *testing.T) {
	d := Canonicalize("src/a.ts:fn", "widen pool timeout", "why")
	for _, derive := range []func() []byte{d.Key, d.MatchKey} {
		first := derive()
		want := append([]byte(nil), first...)
		for i := range first {
			first[i] ^= 0xff
		}
		require.Equal(t, want, derive())
	}
}

// TestRebuildWith_DerivedKeysMatchFreshOnes pins that the rebuild Open makes from reindex's
// pre-derived keys is the rebuild RebuildBloom makes from scratch: the same filter, bit for bit and
// Count for Count, and the same Health apart from the generation counter each rebuild advances.
// The ledger mixes active, stale, foreign-session and project-scoped records, so the visibility
// filter decides which pre-derived pairs are used.
func TestRebuildWith_DerivedKeysMatchFreshOnes(t *testing.T) {
	ctx := context.Background()
	l := benchLedger(t, 300, benchDeps(nil, nil))

	foreign := benchRecordAt(1000)
	foreign.Session, foreign.Scope = "another-session", ScopeSession
	_, err := l.Record(ctx, foreign)
	require.NoError(t, err)
	project := benchRecordAt(1001)
	project.Scope = ScopeProject
	_, err = l.Record(ctx, project)
	require.NoError(t, err)
	all, err := l.All(ctx)
	require.NoError(t, err)
	var stale []string
	for i := 0; i < len(all); i += 7 {
		stale = append(stale, all[i].ID)
	}
	require.NoError(t, l.MarkStale(ctx, stale, []string{"moved"}))

	l.mu.Lock()
	defer l.mu.Unlock()
	keys := l.reindex()
	require.Len(t, keys, len(l.recs))
	for i := range l.recs {
		require.Equal(t, l.recs[i].Desc.Key(), keys[i].key[:], "record %d key", i)
		require.Equal(t, l.recs[i].Desc.MatchKey(), keys[i].match[:], "record %d match key", i)
	}

	derived, hDerived, err := l.rebuildWith(ctx, keys)
	require.NoError(t, err)
	fresh, hFresh, err := l.rebuildWith(ctx, nil)
	require.NoError(t, err)

	wantBytes, err := fresh.MarshalBinary()
	require.NoError(t, err)
	gotBytes, err := derived.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, wantBytes, gotBytes, "the filter built from derived keys must be bit-identical")
	require.Equal(t, fresh.Count(), derived.Count())
	hDerived.FilterGeneration, hFresh.FilterGeneration = 0, 0
	require.Equal(t, hFresh, hDerived)
	require.Equal(t, l.visibleActiveCount(), hFresh.Active)
	require.Less(t, hFresh.Active, len(l.recs), "the fixture must exclude some records")

	// A keys slice that does not line up with the records is ignored, not trusted.
	short, _, err := l.rebuildWith(ctx, keys[:len(keys)-1])
	require.NoError(t, err)
	shortBytes, err := short.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, wantBytes, shortBytes)
}
