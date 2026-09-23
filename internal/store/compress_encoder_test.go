package store

import (
	"bytes"
	"runtime"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

// oneMatchTableBytes is the size of ONE of the match tables a SpeedDefault zstd encoder allocates
// when it is initialized: klauspost/compress's double-fast long table, 1<<17 entries of 8 bytes.
// An Encode that allocates this much has built encoder state rather than used existing state; a
// warm Encode of a 4 KiB chunk allocates its output buffer and little else.
const oneMatchTableBytes = (1 << 17) * 8

// TestEncode_KeepsItsEncoderAcrossGarbageCollection is the encoder-lifetime half of SP06-D2's cold
// path. Two garbage collections empty a sync.Pool, victim cache included, and the store's cold
// puts are exactly the case where collections fall between Encodes. An Encode after them must not
// rebuild encoder state: rebuilding a SpeedDefault encoder allocates one table set per concurrency
// slot, which was GOMAXPROCS slots — 28 MiB on a 22-thread host — per rebuild.
func TestEncode_KeepsItsEncoderAcrossGarbageCollection(t *testing.T) {
	chunk := bytes.Repeat([]byte("a line of tool output that a chunk is made of\n"), 90)
	_, err := Encode(chunk)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		runtime.GC()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		enc, err := Encode(chunk)
		runtime.ReadMemStats(&after)
		require.NoError(t, err)
		require.NotEmpty(t, enc)
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(oneMatchTableBytes),
			"round %d: an Encode after a collection allocated %d bytes, so it rebuilt encoder state",
			i, after.TotalAlloc-before.TotalAlloc)
	}
}

// TestEncode_IsByteIdenticalToAFreshEncoder pins that how Encode obtains its encoder never shows in
// the bytes it writes: every object file must be what a freshly constructed SpeedDefault encoder
// would write, whatever the encoder encoded before. The corpus is encoded twice, in different
// orders, through Encode, and compared with a fresh encoder per input.
func TestEncode_IsByteIdenticalToAFreshEncoder(t *testing.T) {
	var corpus [][]byte
	for i := 0; i < 24; i++ {
		corpus = append(corpus, bigPayload(512+i*997))
		corpus = append(corpus, bytes.Repeat([]byte{byte(i)}, 3000+i*131))
	}
	fresh := func(b []byte) []byte {
		enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
		require.NoError(t, err)
		defer func() { _ = enc.Close() }()
		return enc.EncodeAll(b, nil)
	}
	for pass := 0; pass < 2; pass++ {
		for k := range corpus {
			i := k
			if pass == 1 {
				i = len(corpus) - 1 - k
			}
			got, err := Encode(corpus[i])
			require.NoError(t, err)
			require.Equal(t, fresh(corpus[i]), got, "pass %d, input %d", pass, i)
		}
	}
}
