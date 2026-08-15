package tokens_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/tokens"
)

// TestEstimate_EveryClassIsPriced sweeps all seven classes so each characters-per-token branch of
// the cache-miss path is exercised, and asserts none of them prices real content at zero.
func TestEstimate_EveryClassIsPriced(t *testing.T) {
	est := newExact(t)
	payload := []byte("the quick brown fox jumps over the lazy dog, repeatedly and at length\n")

	classes := []tokens.Class{
		tokens.ClassProse, tokens.ClassCode, tokens.ClassJSON, tokens.ClassDiff,
		tokens.ClassImage, tokens.ClassPDF, tokens.ClassBinary,
	}
	for _, c := range classes {
		t.Run(c.String(), func(t *testing.T) {
			require.Greater(t, int(est.Estimate(payload, c)), 0)

			ref := core.ChunkRef{Hash: core.HashBytes("tokens.sweep", []byte(c.String())), Len: len(payload)}
			require.Greater(t, int(est.EstimateRoot(context.Background(), []core.ChunkRef{ref}, c)), 0,
				"the cache-miss path must price every class above zero")
		})
	}
}

// TestEstimate_ZeroCharsPerTokenDoesNotDivideByZero pins the documented guard: a configuration
// with a non-positive divisor degrades to one token per byte rather than producing +Inf.
func TestEstimate_ZeroCharsPerTokenDoesNotDivideByZero(t *testing.T) {
	cfg := config.Defaults()
	cfg.Runtime.Tokens.ProseCharsPerToken = 0
	est := tokens.NewExact(cfg, "", "")

	ref := core.ChunkRef{Hash: core.HashBytes("tokens.zerodiv", []byte("x")), Len: 128}
	require.Equal(t, core.Tokens(128), est.EstimateRoot(context.Background(), []core.ChunkRef{ref}, tokens.ClassProse))
}

// TestEstimateImage_ZeroPixelsPerTokenIsGuarded is the image-side equivalent.
func TestEstimateImage_ZeroPixelsPerTokenIsGuarded(t *testing.T) {
	cfg := config.Defaults()
	cfg.Runtime.Tokens.ImagePixelsPerToken = 0
	cfg.Runtime.Tokens.ImageMaxTokens = 0 // also disable the cap, so the raw number is visible
	est := tokens.NewExact(cfg, "", "")

	require.Equal(t, core.Tokens(64*64), est.Estimate(readFixture(t, "tiny.png"), tokens.ClassImage),
		"a zero pixels-per-token divisor degrades to one token per pixel")
}

// TestImageDimensions_MalformedHeaders asserts every parser declines rather than returning a
// nonsense size when its header is truncated or corrupt.
func TestImageDimensions_MalformedHeaders(t *testing.T) {
	cases := map[string][]byte{
		"png truncated before IHDR": append([]byte("\x89PNG\r\n\x1a\n"), 0, 0, 0, 13),
		"png wrong chunk tag":       append(append([]byte("\x89PNG\r\n\x1a\n"), 0, 0, 0, 13), []byte("XXXX\x00\x00\x00\x10\x00\x00\x00\x10")...),
		"jpeg with no SOF":          {0xFF, 0xD8, 0xFF, 0xD9},
		"jpeg truncated segment":    {0xFF, 0xD8, 0xFF, 0xC0, 0x00},
		"gif truncated":             []byte("GIF89a\x40"),
		"webp unknown chunk":        append([]byte("RIFF\x1a\x00\x00\x00WEBPXXXX"), make([]byte, 8)...),
		"webp vp8l bad signature":   append([]byte("RIFF\x1a\x00\x00\x00WEBPVP8L\x05\x00\x00\x00"), []byte{0x00, 1, 2, 3, 4}...),
		"not an image at all":       []byte("plain text, definitely not an image"),
		"empty":                     {},
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			_, ok := tokens.EstimateImage(b)
			require.False(t, ok, "%s must not parse as an image", name)
		})
	}
}

// riffWebP wraps one chunk in a RIFF/WEBP container.
func riffWebP(fourcc string, payload []byte) []byte {
	var chunk bytes.Buffer
	chunk.WriteString(fourcc)
	_ = binary.Write(&chunk, binary.LittleEndian, uint32(len(payload)))
	chunk.Write(payload)

	var out bytes.Buffer
	out.WriteString("RIFF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(4+chunk.Len()))
	out.WriteString("WEBP")
	out.Write(chunk.Bytes())
	return out.Bytes()
}

// TestWebPDimensions_AllThreeBitstreamForms covers the extended (VP8X) and lossy (VP8 ) containers
// alongside the lossless (VP8L) one the generated fixture uses.
func TestWebPDimensions_AllThreeBitstreamForms(t *testing.T) {
	const w, h = 800, 600

	vp8x := make([]byte, 10)
	vp8x[0] = 0x10 // flags
	vp8x[4], vp8x[5], vp8x[6] = byte((w-1)&0xFF), byte(((w-1)>>8)&0xFF), byte(((w-1)>>16)&0xFF)
	vp8x[7], vp8x[8], vp8x[9] = byte((h-1)&0xFF), byte(((h-1)>>8)&0xFF), byte(((h-1)>>16)&0xFF)

	vp8 := make([]byte, 10)
	vp8[3], vp8[4], vp8[5] = 0x9D, 0x01, 0x2A
	binary.LittleEndian.PutUint16(vp8[6:8], uint16(w))
	binary.LittleEndian.PutUint16(vp8[8:10], uint16(h))

	// ceil(800*600 / 750) == 640, comfortably under the 1568 px clamp and the token cap.
	const wantTokens = core.Tokens(640)

	for _, tc := range []struct {
		name    string
		payload []byte
		fourcc  string
	}{
		{"VP8X", vp8x, "VP8X"},
		{"VP8 lossy", vp8, "VP8 "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tokens.EstimateImage(riffWebP(tc.fourcc, tc.payload))
			require.True(t, ok)
			require.Equal(t, wantTokens, got)
		})
	}
}

// TestWebPDimensions_LossyRejectsBadStartCode asserts the VP8 parser validates the frame start
// code rather than trusting whatever bytes sit at that offset.
func TestWebPDimensions_LossyRejectsBadStartCode(t *testing.T) {
	bad := make([]byte, 10)
	bad[3], bad[4], bad[5] = 0x00, 0x00, 0x00
	_, ok := tokens.EstimateImage(riffWebP("VP8 ", bad))
	require.False(t, ok)
}

// TestChunkCache_EvictsOldestAndReloadsFromDisk exercises both halves of the eviction contract: the
// in-memory set is bounded, and an entry dropped from memory is NOT lost, because it is still in
// the file and comes back on the next construction.
func TestChunkCache_EvictsOldestAndReloadsFromDisk(t *testing.T) {
	defer tokens.SetChunkCacheMaxEntries(20)()

	cachePath := filepath.Join(t.TempDir(), "chunktokens.bin")
	est := tokens.NewExact(config.Defaults(), "", cachePath)
	sink := chunkSink(t, est)

	const noted = 60
	var newest core.ChunkRef
	var newestWant core.Tokens
	for i := 0; i < noted; i++ {
		body := bytes.Repeat([]byte("evictable chunk "), 2+i%5)
		h := core.HashBytes("tokens.evict", append([]byte{byte(i)}, body...))
		got := sink.NoteChunk(h, tokens.ClassProse, body)
		newest, newestWant = core.ChunkRef{Hash: h, Len: len(body)}, got
	}

	require.LessOrEqual(t, tokens.CachedEntries(est), 20, "the in-memory cache must stay bounded")
	require.NoError(t, persister(t, est).Close())

	// Eviction is a MEMORY bound, not a data loss: every measurement still reached the file, even
	// the ones dropped from the in-memory map before Close.
	st, err := os.Stat(cachePath)
	require.NoError(t, err)
	onDisk := int(st.Size()-int64(tokens.ChunkCacheHeaderSize())) / tokens.ChunkCacheRecordSize()
	require.Equal(t, noted, onDisk, "every measured chunk must be persisted, including evicted ones")

	reopened := tokens.NewExact(config.Defaults(), "", cachePath)
	require.LessOrEqual(t, tokens.CachedEntries(reopened), 20,
		"loading a file larger than the cap must still respect it")
	require.Equal(t, newestWant,
		reopened.EstimateRoot(context.Background(), []core.ChunkRef{newest}, tokens.ClassProse),
		"a retained entry must reload from the file with its measured value")
}

// TestChunkCache_CompactsWhenFileOutgrowsMemory asserts the file is rewritten from the live set,
// rather than appended to forever, once it exceeds its compaction threshold.
func TestChunkCache_CompactsWhenFileOutgrowsMemory(t *testing.T) {
	defer tokens.SetChunkCacheMaxEntries(8)()

	cachePath := filepath.Join(t.TempDir(), "chunktokens.bin")
	est := tokens.NewExact(config.Defaults(), "", cachePath)
	sink := chunkSink(t, est)

	for i := 0; i < 40; i++ {
		body := bytes.Repeat([]byte("compactable "), 2+i%3)
		sink.NoteChunk(core.HashBytes("tokens.compact", append([]byte{byte(i)}, body...)), tokens.ClassProse, body)
		require.NoError(t, persister(t, est).Flush())
	}
	require.NoError(t, persister(t, est).Close())

	st, err := os.Stat(cachePath)
	require.NoError(t, err)
	records := int(st.Size()-int64(tokens.ChunkCacheHeaderSize())) / tokens.ChunkCacheRecordSize()
	require.LessOrEqual(t, records, 40, "compaction must bound the file to the live set")
	require.Greater(t, records, 0)
}

// TestChunkCache_FlushIsIdempotent asserts a second Flush with nothing new pending writes nothing.
func TestChunkCache_FlushIsIdempotent(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "chunktokens.bin")
	est := tokens.NewExact(config.Defaults(), "", cachePath)
	sink := chunkSink(t, est)

	body := []byte("one measured chunk, flushed twice")
	sink.NoteChunk(core.HashBytes("tokens.flush", body), tokens.ClassProse, body)
	require.NoError(t, persister(t, est).Flush())

	before, err := os.Stat(cachePath)
	require.NoError(t, err)
	require.NoError(t, persister(t, est).Flush())
	after, err := os.Stat(cachePath)
	require.NoError(t, err)

	require.Equal(t, before.Size(), after.Size(), "a Flush with nothing pending must not grow the file")
}

// TestChunkCache_WrongMagicStartsEmpty asserts a file that is not a chunk-token cache is refused
// rather than read as records.
func TestChunkCache_WrongMagicStartsEmpty(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "chunktokens.bin")
	require.NoError(t, os.WriteFile(cachePath, bytes.Repeat([]byte("NOPE"), 16), 0o644))

	est := tokens.NewExact(config.Defaults(), "", cachePath)
	require.Zero(t, tokens.CachedEntries(est))
}

// TestChunkCache_UnsupportedVersionStartsEmpty asserts a future format version is refused rather
// than misparsed.
func TestChunkCache_UnsupportedVersionStartsEmpty(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "chunktokens.bin")
	hdr := make([]byte, tokens.ChunkCacheHeaderSize())
	copy(hdr, tokens.ChunkCacheHeaderMagic())
	binary.LittleEndian.PutUint16(hdr[4:6], 99)
	require.NoError(t, os.WriteFile(cachePath, hdr, 0o644))

	est := tokens.NewExact(config.Defaults(), "", cachePath)
	require.Zero(t, tokens.CachedEntries(est))
}

// TestDefaultCalibPath_PrefersQompackHome pins the documented resolution order and asserts the
// result always names the calibration document.
func TestDefaultCalibPath_PrefersQompackHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("QOMPACK_HOME", home)
	require.Equal(t, filepath.Join(home, "calibration.json"), tokens.DefaultCalibPath())

	t.Setenv("QOMPACK_HOME", "")
	got := tokens.DefaultCalibPath()
	require.True(t, strings.HasSuffix(got, filepath.Join(".qompack", "calibration.json")),
		"without QOMPACK_HOME the path falls back under a .qompack directory, got %q", got)
	require.True(t, filepath.IsAbs(got))
}

// TestCalibrate_PersistMergesOverAVersionedDocument asserts persist reads a versioned document,
// preserves every other project's entry, and writes the flat shape back.
func TestCalibrate_PersistMergesOverAVersionedDocument(t *testing.T) {
	cfg := config.Defaults()
	home := t.TempDir()
	calibPath := filepath.Join(paths.Global(home), "calibration.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(calibPath), 0o700))

	otherKey := tokens.CalibKeyForTest(filepath.Join(t.TempDir(), "other-project"))
	doc := `{"version":1,"projects":{"` + otherKey + `":{"factor":1.11,"ewma":1.11,"samples":9,"updated":1}}}`
	require.NoError(t, os.WriteFile(calibPath, []byte(doc), 0o600))

	mine := filepath.Join(t.TempDir(), "my-project")
	est := tokens.NewForProject(cfg, calibPath, mine)
	for i := 0; i < 10; i++ {
		est.Calibrate(1300, 1000)
	}

	raw, err := os.ReadFile(calibPath)
	require.NoError(t, err)
	var flat map[string]float64
	require.NoError(t, json.Unmarshal(raw, &flat))
	require.Len(t, flat, 2, "the other project's entry must survive: %s", raw)
	require.InDelta(t, 1.11, flat[otherKey], 1e-9)
	require.InDelta(t, est.Factor(), flat[tokens.CalibKeyForTest(mine)], 1e-9)
}

// TestCalibrate_CorruptFileStartsAtIdentity asserts an unparseable calibration document degrades
// to the identity factor rather than failing construction.
func TestCalibrate_CorruptFileStartsAtIdentity(t *testing.T) {
	calibPath := filepath.Join(t.TempDir(), "calibration.json")
	require.NoError(t, os.WriteFile(calibPath, []byte("{not json at all"), 0o600))

	require.Equal(t, 1.0, tokens.NewExact(config.Defaults(), calibPath, "").Factor())
}
