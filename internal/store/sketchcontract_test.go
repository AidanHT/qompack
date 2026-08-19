package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/sketch"
)

// This file is the store half of V2-MERGE-07: SP-03's frozen sketch fixtures under
// testdata/golden/contracts/sketch/ exist and are ACTUALLY CONSUMED by a test in this package.
//
// The row exists because "no t.Skip remains" and "a W-2 obligation was discharged" are not the
// same claim: an obligation nobody wrote a test for is indistinguishable from one that passes.
// SP-06 was told to test against these fixtures and could not, because the directory does not
// exist on develop before wave 1 — SP-03 creates it in its commit 6 of 8.
//
// What is checked is a WIRE-FORMAT contract between two packages that never call each other's
// codecs on the hot path. store persists a MinHash signature as {"p":<perms>,"m":<base64>} on a
// roots.jsonl and a tool_use.jsonl line, where the base64 is sketch.Signature's own compact form —
// which is, byte for byte, the BODY of a QPKS KindMinHash frame. So a frozen frame is the only
// artifact that can prove the two halves agree without this package reimplementing either.

// frozenMinHashFixture is SP-03's frozen QPKS MinHash frame:
// testdata/golden/contracts/sketch/minhash-128.v1.bin, listed in that directory's MANIFEST.json as
// "QPKS v1 MinHash frame; bytes=1076
// sha256=ea5bf9dffa6ef86b92d08a9e854dd2b19deb0d0ed9834ef6f7316a7b1beb7211".
const frozenMinHashFixture = "minhash-128.v1.bin"

// The MANIFEST.json entry, repeated so this test fails LOUDLY rather than silently changing what
// it proves if the fixture is ever regenerated. SP-03 owns the fixture; a mismatch here is a
// cross-branch notification, not something to update in place.
const (
	frozenMinHashBytes  = 1076
	frozenMinHashSHA256 = "ea5bf9dffa6ef86b92d08a9e854dd2b19deb0d0ed9834ef6f7316a7b1beb7211"
	frozenMinHashPerms  = 128
)

// The compact signature form's field widths, restated here rather than imported because
// sketch keeps them unexported: 2 bytes of little-endian permutation count, then 8 bytes per
// minimum. See sketch.Signature.MarshalBinary.
const (
	sigWirePermsLen = 2
	sigWireMinLen   = 8
)

// readFrozenMinHashFrame returns the frozen fixture's bytes, having first checked they are the
// bytes MANIFEST.json describes.
func readFrozenMinHashFrame(t *testing.T) []byte {
	t.Helper()
	p := filepath.Join("..", "..", "testdata", "golden", "contracts", "sketch", frozenMinHashFixture)
	raw, err := os.ReadFile(p)
	require.NoError(t, err, "SP-03's frozen sketch fixture is missing: %s", p)

	sum := sha256.Sum256(raw)
	require.Equal(t, frozenMinHashBytes, len(raw), "fixture %s changed size; MANIFEST.json says %d", p, frozenMinHashBytes)
	require.Equal(t, frozenMinHashSHA256, hex.EncodeToString(sum[:]),
		"fixture %s no longer matches the digest MANIFEST.json records; SP-03 owns it, so a change "+
			"here is a cross-branch notification rather than something to update in place", p)
	return raw
}

// sigOnWireOf pulls the "sig" object off one index line, failing if the key is absent.
func sigOnWireOf(t *testing.T, line string) signatureOnWire {
	t.Helper()
	var rec struct {
		Sig *signatureOnWire `json:"sig"`
	}
	require.NoError(t, json.Unmarshal([]byte(line), &rec))
	require.NotNil(t, rec.Sig, "the index line carries no \"sig\" key:\n%s", line)
	return *rec.Sig
}

// TestSketchContract_PersistedSignatureMatchesFrozenQPKSFrame is the V2-MERGE-07 consumer.
//
// It proves three things about the signature this store writes into index/roots.jsonl, each
// against SP-03's frozen frame rather than against this package's own idea of the format:
//
//  1. The frozen frame still decodes through the sketch this build links, and re-encodes to
//     exactly the frozen bytes. If it did not, every assertion below would be measuring a format
//     that no longer exists.
//  2. The base64 the store persists is the frame's BODY encoding — the same little-endian uint16
//     permutation count followed by the same 8-byte minima — of the same width the frozen frame
//     declares.
//  3. Wrapping the store's own signature back into a QPKS frame reproduces the frozen frame's
//     header and params block BYTE FOR BYTE, so the two differ only where they must: in the
//     minima, which are a function of the content.
func TestSketchContract_PersistedSignatureMatchesFrozenQPKSFrame(t *testing.T) {
	raw := readFrozenMinHashFrame(t)

	var frozen sketch.SigSketch
	require.NoError(t, frozen.UnmarshalBinary(raw), "the frozen QPKS MinHash frame must decode")
	require.Equal(t, uint16(frozenMinHashPerms), frozen.Sig.Perms)
	require.Len(t, frozen.Sig.Mins, frozenMinHashPerms)

	reEncoded, err := frozen.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, raw, reEncoded,
		"re-encoding the decoded fixture must reproduce it byte for byte, or the frame is not frozen")

	// The store, with MinHash at its configured 128 permutations — the same width the fixture
	// declares, which is what makes the two comparable at all.
	tp := newTestStore(t)
	require.Equal(t, frozenMinHashPerms, tp.Cfg.Store.Canonicalize.MinHash.Permutations,
		"fixture sanity: the frozen frame is 128-wide, so the store must be too")

	res, err := tp.Store.PutBytes(context.Background(),
		fixture(t, "fileread-auth-v1.txt"), PutOptions{Tool: "FileRead", Path: "src/auth.ts"})
	require.NoError(t, err)
	require.Equal(t, uint16(frozenMinHashPerms), res.Signature.Perms,
		"the real canon+sketch pipeline must attach a signature; a zero one means MinHash never ran")

	lines := tp.indexLines(t, rootsFile)
	require.Len(t, lines, 1)
	wire := sigOnWireOf(t, lines[0])
	require.Equal(t, uint16(frozenMinHashPerms), wire.P)

	body, err := base64.StdEncoding.DecodeString(wire.M)
	require.NoError(t, err, "the persisted \"m\" must be standard base64")

	// (2) The persisted bytes ARE the frame body: same length rule, same leading width field.
	require.Len(t, body, sigWirePermsLen+sigWireMinLen*frozenMinHashPerms)
	require.Equal(t, uint16(frozenMinHashPerms), binary.LittleEndian.Uint16(body))

	frozenBody := raw[len(raw)-crcSuffixLen-len(body) : len(raw)-crcSuffixLen]
	require.Equal(t, uint16(frozenMinHashPerms), binary.LittleEndian.Uint16(frozenBody),
		"the frozen frame's body must open with the same little-endian width field the store persists")

	var decoded sketch.Signature
	require.NoError(t, decoded.UnmarshalBinary(body),
		"sketch must be able to read back what this store wrote — the round trip V2-MERGE-07 is about")
	require.Equal(t, res.Signature, decoded, "the persisted signature must round-trip exactly")

	// (3) Framed with the fixture's own Created stamp, the store's signature produces a frame
	// identical to the frozen one everywhere except the minima.
	framed, err := (&sketch.SigSketch{Sig: decoded, Created: frozen.Created}).MarshalBinary()
	require.NoError(t, err)
	require.Len(t, framed, frozenMinHashBytes,
		"a 128-permutation signature must frame to exactly the size MANIFEST.json records")
	prefix := len(framed) - crcSuffixLen - len(body)
	require.Equal(t, raw[:prefix], framed[:prefix],
		"the QPKS header and params block must be byte-identical to the frozen frame's: same magic, "+
			"version, kind, count, created stamp and perms param")
}

// crcSuffixLen is the trailing CRC32C width of a QPKS frame (sketch/header.go), restated because
// sketch keeps it unexported.
const crcSuffixLen = 4

// TestSketchContract_ToolUseLineCarriesTheSameWireForm asserts index/tool_use.jsonl encodes a
// signature exactly as index/roots.jsonl does.
//
// It is a separate test because the two writers are separate code paths — roots.go's
// marshalRootLine and tooluseindex.go's tuSignature — and because the golden scripted session in
// golden_test.go supplies no Signature to RecordToolUse, so testdata/golden/store/tool_use.jsonl
// pins the ABSENT case only. The frozen fixture's own signature is used as the input, so this test
// consumes testdata/golden/contracts/sketch/ too.
func TestSketchContract_ToolUseLineCarriesTheSameWireForm(t *testing.T) {
	var frozen sketch.SigSketch
	require.NoError(t, frozen.UnmarshalBinary(readFrozenMinHashFrame(t)))

	tp := newTestStore(t)
	ctx := context.Background()

	res, err := tp.Store.PutBytes(ctx, []byte("a tool result whose record carries a signature\n"),
		PutOptions{Tool: "FileRead", Path: "src/sig.ts"})
	require.NoError(t, err)

	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: "toolu_01SKETCHCONTRACT00000000", Session: "sess-sketch", Turn: 1,
		TS: core.UnixMilli(tp.Clock.Now().UnixMilli()), Tool: "FileRead",
		Root: res.Root.Hash, Path: "src/sig.ts", Signature: frozen.Sig,
	}))

	lines := tp.indexLines(t, toolUseFile)
	require.Len(t, lines, 1)
	wire := sigOnWireOf(t, lines[0])
	require.Equal(t, frozen.Sig.Perms, wire.P)

	body, err := base64.StdEncoding.DecodeString(wire.M)
	require.NoError(t, err)
	want, err := frozen.Sig.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, want, body,
		"a tool_use line's \"m\" must be sketch.Signature.MarshalBinary verbatim, which is also the "+
			"QPKS frame body — the two index files and the frame must not drift apart")

	// And the reader half: reopening must hand back the same signature, not a width-only stub.
	require.NoError(t, tp.Store.Close())
	reopened := openOver(t, tp.project)
	rec, err := reopened.Store.ToolUse(ctx, "toolu_01SKETCHCONTRACT00000000")
	require.NoError(t, err)
	require.Equal(t, frozen.Sig, rec.Signature, "a persisted signature must survive a reopen intact")
}
