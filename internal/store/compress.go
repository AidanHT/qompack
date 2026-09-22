package store

import (
	"fmt"
	"runtime"
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

// maxEncoderConcurrency caps how many EncodeAll calls the shared encoder runs at once, and so how
// many encoder states it keeps. It is klauspost/compress's own cap for its decoder's concurrency,
// applied to the encoder: object writes spend most of their time in file operations rather than
// in EncodeAll, so a fifth concurrent put waits for one chunk's encode, not for another put.
const maxEncoderConcurrency = 4

// sharedEncoder is the one *zstd.Encoder every Encode uses, at zstd.SpeedDefault
// (00-ARCHITECTURE.md §3.3 "zstd-compressed"; §14.1 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). klauspost/compress documents EncodeAll as
// safe for concurrent calls: the Encoder holds one encoder state per concurrency slot and lends
// each call one of them.
//
// It is built once and kept for the life of the process, and that is the point of it (SP06-D2).
// It replaces a sync.Pool of encoders, and a sync.Pool is emptied by every second garbage
// collection — which is exactly what falls between the Encodes of a cold put. Each miss built a
// new Encoder whose initialization allocates a match-table set (about 1.3 MB at SpeedDefault) for
// EVERY concurrency slot, and the default is GOMAXPROCS slots: 28 MiB per rebuild on a 22-thread
// host, most of it for slots the call never used. Kept, the encoder costs its slots' tables once.
//
// Concurrency does not reach the output: each EncodeAll encodes its input into one frame with one
// slot's state, reset for the call, so an object's bytes are what a fresh encoder writes
// (TestEncode_IsByteIdenticalToAFreshEncoder).
var sharedEncoder = sync.OnceValue(func() *zstd.Encoder {
	slots := runtime.GOMAXPROCS(0)
	if slots > maxEncoderConcurrency {
		slots = maxEncoderConcurrency
	}
	// A nil io.Writer is the documented, supported way to build an Encoder for EncodeAll-only use
	// (see the klauspost/compress/zstd README's own global-encoder example): EncodeAll never
	// touches the underlying writer, so none is needed. NewWriter can only fail on a rejected
	// option, and both options here are always valid, so this panics rather than threading an
	// unreachable error through every Encode.
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderConcurrency(slots))
	if err != nil {
		panic(fmt.Sprintf("store: constructing the shared zstd.Encoder: %v", err))
	}
	return enc
})

// decoderPool holds package-level, reusable *zstd.Decoder values, each bounded to maxDecodedSize
// so Decode can never be tricked into an unbounded allocation by a compression bomb (§13
// invariant 7).
var decoderPool = sync.Pool{
	New: func() any {
		// A nil io.Reader is likewise the documented, supported way to build a Decoder for
		// DecodeAll-only use. NewReader can only fail on a rejected option, and
		// WithDecoderMaxMemory(maxDecodedSize) is always valid, so this panics for the same
		// reason sharedEncoder does.
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
	return int64(sharedEncoder().MaxEncodedSize(MaxPutBytes))
}

// EncodedObjectLimit is the largest .zst object file readObjectFile will read before refusing it
// (objects.go). It is exported for `qompack fsck`, whose objects row must name the limit the
// STORE'S OWN READER applies rather than a generous bound of its own invention: a file fsck calls
// acceptable and the store then refuses is a defect the report does not have.
//
// Bare (uncompressed) objects are bounded at MaxPutBytes instead; that is the plaintext limit and
// readObjectFile applies it to the second candidate directly.
func EncodedObjectLimit() int64 { return encodedObjectLimit() }

// Encode returns the zstd-compressed form of b, at zstd.SpeedDefault. This is a real
// implementation, not a stub (§14.1 of plans/V1-SP-01-foundation-toolchain-and-contracts.md):
// SP-06's real Put/PutBytes calls it directly to produce objects/ab/cd/<sha256>.zst, so it must
// work correctly today even though the rest of this package is a stub.
func Encode(b []byte) ([]byte, error) {
	return sharedEncoder().EncodeAll(b, make([]byte, 0, len(b))), nil
}

// Decode returns the decompressed form of b, as produced by Encode. The decoder is bounded to
// maxDecodedSize (64 MiB), so a payload whose declared or actual decompressed size exceeds that
// cap fails with an error instead of allocating without bound (§13 invariant 7). This is a real
// implementation, not a stub — see Encode's doc comment.
func Decode(b []byte) ([]byte, error) {
	// The failed assertion's own variable is a typed nil, so %T on it would print the type this
	// was hoping for rather than the one that arrived. The pooled value is what names the fault.
	pooled := decoderPool.Get()
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
