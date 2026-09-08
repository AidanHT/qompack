package store

import (
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// maxDecodedSize bounds allocation from untrusted input (§13 invariant 7 of 00-ARCHITECTURE.md):
// no Decode call will allocate more than this many decompressed bytes, regardless of what a
// corrupt or hostile object on disk claims its size is. 64 MiB, written as a shift so the
// individual literal 67108864 never needs to appear (D11, §11.6 exempts neither number from
// nomagic on its own terms, but a shift of a small, obviously-fine base reads clearer than either
// spelling and this file is not a config-default file where a bare literal would even be
// permitted).
const maxDecodedSize = 64 << 20

// encoderPool holds package-level, reusable *zstd.Encoder values at zstd.SpeedDefault
// (00-ARCHITECTURE.md §3.3 "zstd-compressed"; §14.1 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). Constructing a zstd.Encoder allocates
// internal tables that are expensive to rebuild per call, so Encode borrows one from this pool
// instead of constructing one per invocation.
var encoderPool = sync.Pool{
	New: func() any {
		// A nil io.Writer is the documented, supported way to build an Encoder for EncodeAll-only
		// use (see the klauspost/compress/zstd README's own global-encoder example): EncodeAll
		// never touches the underlying writer, so none is needed. NewWriter can only fail on a
		// rejected option, and zstd.WithEncoderLevel(zstd.SpeedDefault) is always valid, so this
		// panics rather than threading an unreachable error through sync.Pool.New's signature.
		enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
		if err != nil {
			panic(fmt.Sprintf("store: constructing pooled zstd.Encoder: %v", err))
		}
		return enc
	},
}

// decoderPool holds package-level, reusable *zstd.Decoder values, each bounded to maxDecodedSize
// so Decode can never be tricked into an unbounded allocation by a compression bomb (§13
// invariant 7).
var decoderPool = sync.Pool{
	New: func() any {
		// A nil io.Reader is likewise the documented, supported way to build a Decoder for
		// DecodeAll-only use. NewReader can only fail on a rejected option, and
		// WithDecoderMaxMemory(maxDecodedSize) is always valid, so this panics for the same
		// reason encoderPool.New does.
		dec, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxDecodedSize))
		if err != nil {
			panic(fmt.Sprintf("store: constructing pooled zstd.Decoder: %v", err))
		}
		return dec
	},
}

// encodedObjectLimit includes the encoder's worst-case framing/block overhead for a maximum
// plaintext object. A raw MaxPutBytes limit would reject valid incompressible encoded input.
// The same configured encoder used by Encode defines the supported on-disk representation.
func encodedObjectLimit() int64 {
	pooled := encoderPool.Get()
	enc, ok := pooled.(*zstd.Encoder)
	if !ok {
		// encoderPool is package-private and its New returns nothing but a *zstd.Encoder, so a
		// value of any other type is a programming error in this package, not a runtime condition
		// a caller could recover from. Unlike Encode and Decode this function has no error to
		// return, and every limit it could invent instead is wrong in a way that fails silently: 0
		// rejects every object, and an unpadded MaxPutBytes rejects valid incompressible input,
		// which is the exact bug this function exists to avoid. Building a throwaway encoder would
		// hide the same corruption behind an allocation on every call. So it panics, naming the
		// type it actually got, exactly as encoderPool.New already panics on an option it cannot
		// reject.
		panic(fmt.Sprintf("store: encodedObjectLimit: encoder pool returned %T, want *zstd.Encoder", pooled))
	}
	defer encoderPool.Put(enc)
	return int64(enc.MaxEncodedSize(MaxPutBytes))
}

// Encode returns the zstd-compressed form of b, at zstd.SpeedDefault. This is a real
// implementation, not a stub (§14.1 of plans/V1-SP-01-foundation-toolchain-and-contracts.md):
// SP-06's real Put/PutBytes calls it directly to produce objects/ab/cd/<sha256>.zst, so it must
// work correctly today even though the rest of this package is a stub.
func Encode(b []byte) ([]byte, error) {
	// The failed assertion's own variable is a typed nil, so %T on it would print the type this
	// was hoping for rather than the one that arrived. The pooled value is what names the fault.
	pooled := encoderPool.Get()
	enc, ok := pooled.(*zstd.Encoder)
	if !ok {
		return nil, fmt.Errorf("store: Encode: encoder pool returned %T, want *zstd.Encoder", pooled)
	}
	defer encoderPool.Put(enc)
	return enc.EncodeAll(b, make([]byte, 0, len(b))), nil
}

// Decode returns the decompressed form of b, as produced by Encode. The decoder is bounded to
// maxDecodedSize (64 MiB), so a payload whose declared or actual decompressed size exceeds that
// cap fails with an error instead of allocating without bound (§13 invariant 7). This is a real
// implementation, not a stub — see Encode's doc comment.
func Decode(b []byte) ([]byte, error) {
	pooled := decoderPool.Get() // see Encode's own note on why the pooled value, not dec, names the fault
	dec, ok := pooled.(*zstd.Decoder)
	if !ok {
		return nil, fmt.Errorf("store: Decode: decoder pool returned %T, want *zstd.Decoder", pooled)
	}
	defer decoderPool.Put(dec)

	out, err := dec.DecodeAll(b, nil)
	if err != nil {
		return nil, fmt.Errorf("store: Decode: %w", err)
	}
	return out, nil
}
