package negknow_test

import (
	"encoding/hex"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/stretchr/testify/require"
)

// fixedDescriptor is TestDescriptorKey_Stable's frozen input: a specific, hand-chosen Descriptor
// so its Key() output can itself be frozen as a golden. Changing any field here invalidates
// wantDescriptorKeyHex below.
var fixedDescriptor = negknow.Descriptor{
	NormalizedPath: "src/auth.ts",
	Symbol:         "refreshToken",
	ApproachClass:  "widen-timeout",
	ReasonHash:     mustParseHash("sha256:9f2c4a7e1b8d3506e9a1c4f7b2d508e3a6c9f1b4d7e0a3c6f9b2d5e8a1c4f7b0"),
}

// wantDescriptorKeyHex is fixedDescriptor.Key(), frozen. Computed once from Descriptor.Key's own
// transcribed-from-§14.1 implementation (core.HashBytes(core.DomainNegKnow, normalized_path +
// 0x1f + symbol + 0x1f + approach_class + 0x1f + reason_hash)) and pinned here as a golden: later
// subplans must not change bloom keying without an explicit golden update (S5b's task brief).
const wantDescriptorKeyHex = "7b2149a504b5227e6b00d912b48cf01aea3a609f1c3582e0d06bb36122b87d5d"

func mustParseHash(s string) core.Hash {
	h, err := core.ParseHash(s)
	if err != nil {
		panic(err)
	}
	return h
}

// TestDescriptorKey_Stable asserts a fixed Descriptor yields a fixed 32-byte key: the golden
// TestDescriptorKey_Stable's own doc comment (and S5b's task brief) require. A sha256 digest is
// always exactly 32 bytes, so the length assertion is unconditional, not merely a sanity check.
func TestDescriptorKey_Stable(t *testing.T) {
	key := fixedDescriptor.Key()
	require.Len(t, key, 32, "Descriptor.Key must always be a 32-byte sha256 digest")
	require.Equal(t, wantDescriptorKeyHex, hex.EncodeToString(key))

	// Key must be deterministic: calling it again on the same (unexported-field-free, value-type)
	// Descriptor must reproduce the identical bytes.
	require.Equal(t, key, fixedDescriptor.Key())
}

// TestDescriptorKey_DomainSeparatesAdjacentFields asserts the 0x1f delimiter actually prevents
// two different (NormalizedPath, Symbol) pairs whose concatenation collides from producing the
// same key: ("a","bc") and ("ab","c") must hash differently, which is the entire reason
// Descriptor.Key writes a separator byte between fields rather than concatenating them directly.
func TestDescriptorKey_DomainSeparatesAdjacentFields(t *testing.T) {
	d1 := negknow.Descriptor{NormalizedPath: "a", Symbol: "bc"}
	d2 := negknow.Descriptor{NormalizedPath: "ab", Symbol: "c"}
	require.NotEqual(t, d1.Key(), d2.Key())
}

// TestDescriptorKey_DifferentReasonHashDiffers pins that ReasonHash participates in the key at
// all (a common transcription mistake would be to drop the final b.Write(d.ReasonHash[:])).
func TestDescriptorKey_DifferentReasonHashDiffers(t *testing.T) {
	base := fixedDescriptor
	changed := fixedDescriptor
	changed.ReasonHash = core.HashBytes(core.DomainNegKnow, []byte("a different reason"))
	require.NotEqual(t, base.Key(), changed.Key())
}
