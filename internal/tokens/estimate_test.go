package tokens_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/tokens"
)

func TestEstimate_ProseVsCode(t *testing.T) {
	est := tokens.New(config.Defaults(), "")
	payload := bytes.Repeat([]byte("a"), 4000)

	require.Equal(t, core.Tokens(1000), est.Estimate(payload, tokens.ClassProse), "ceil(4000/4.0)")
	require.Equal(t, core.Tokens(1112), est.Estimate(payload, tokens.ClassCode), "ceil(4000/3.6)")
}

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	enc := &png.Encoder{CompressionLevel: png.BestSpeed}
	require.NoError(t, enc.Encode(&buf, img))
	return buf.Bytes()
}

func TestEstimate_ImageFromDimensions(t *testing.T) {
	est := tokens.New(config.Defaults(), "")
	png100 := encodePNG(t, 100, 100)
	// ceil(100*100 / 750) == ceil(13.33) == 14
	require.Equal(t, core.Tokens(14), est.Estimate(png100, tokens.ClassImage))
}

func TestEstimate_ImageCappedAt1600(t *testing.T) {
	est := tokens.New(config.Defaults(), "")
	png4000 := encodePNG(t, 4000, 4000)
	// ceil(4000*4000 / 750) == 21334, capped at ImageMaxTokens == 1600.
	require.Equal(t, core.Tokens(1600), est.Estimate(png4000, tokens.ClassImage))
}

func TestEstimate_ImageDecodeFailureFallsBackToByteLength(t *testing.T) {
	est := tokens.New(config.Defaults(), "")
	payload := bytes.Repeat([]byte{0xAB}, 2500) // not a decodable image of any registered format
	require.Equal(t, core.Tokens(3), est.Estimate(payload, tokens.ClassImage), "ceil(2500/1000)")
}

func TestEstimate_PDFPageCount(t *testing.T) {
	est := tokens.New(config.Defaults(), "")
	pdf := []byte(`%PDF-1.4
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R 4 0 R 5 0 R] /Count 3 >> endobj
3 0 obj << /Type /Page /Parent 2 0 R >> endobj
4 0 obj << /Type /Page /Parent 2 0 R >> endobj
5 0 obj << /Type /Page /Parent 2 0 R >> endobj
`)
	// Three "/Type /Page" objects, one "/Type /Pages" (the tree root, not counted).
	require.Equal(t, core.Tokens(3*1800), est.Estimate(pdf, tokens.ClassPDF))
}

func TestEstimate_PDFPageCountNoTagsStillCountsOnePage(t *testing.T) {
	est := tokens.New(config.Defaults(), "")
	require.Equal(t, core.Tokens(1800), est.Estimate([]byte("%PDF-1.4 no page tags at all"), tokens.ClassPDF))
}

func TestEstimateString_MatchesEstimate(t *testing.T) {
	est := tokens.New(config.Defaults(), "")
	s := "hello world, this is a test string"
	require.Equal(t, est.Estimate([]byte(s), tokens.ClassProse), est.EstimateString(s, tokens.ClassProse))
}

func TestEstimateRoot_SumsChunks(t *testing.T) {
	est := tokens.New(config.Defaults(), "")
	chunks := []core.ChunkRef{
		{Hash: core.HashBytes("test", []byte("chunk-1")), Len: 1000},
		{Hash: core.HashBytes("test", []byte("chunk-2")), Len: 1000},
		{Hash: core.HashBytes("test", []byte("chunk-3")), Len: 1000},
	}
	// 3 * ceil(1000/4.0) = 3 * 250 = 750
	require.Equal(t, core.Tokens(750), est.EstimateRoot(context.Background(), chunks, tokens.ClassProse))
}

func TestEstimateRoot_MemoizesByHash(t *testing.T) {
	est := tokens.New(config.Defaults(), "")
	h := core.HashBytes("test", []byte("repeated-chunk"))
	chunks := []core.ChunkRef{{Hash: h, Len: 400}, {Hash: h, Len: 400}, {Hash: h, Len: 400}}
	// Same hash repeated 3 times: the memoized per-chunk value is reused, but EstimateRoot still
	// sums it once per occurrence in the slice (memoization saves recomputation, not summation).
	require.Equal(t, core.Tokens(3*100), est.EstimateRoot(context.Background(), chunks, tokens.ClassProse))
}

func TestEstimateRoot_EmptyIsZero(t *testing.T) {
	est := tokens.New(config.Defaults(), "")
	require.Equal(t, core.Tokens(0), est.EstimateRoot(context.Background(), nil, tokens.ClassProse))
}

// TestEstimate_MonotoneInLength is a rapid property test: for a fixed byte-length-driven class,
// growing the input never lowers its estimate.
func TestEstimate_MonotoneInLength(t *testing.T) {
	est := tokens.New(config.Defaults(), "")
	rapid.Check(t, func(rt *rapid.T) {
		shortLen := rapid.IntRange(0, 5000).Draw(rt, "shortLen")
		growBy := rapid.IntRange(0, 5000).Draw(rt, "growBy")
		short := rapid.SliceOfN(rapid.Byte(), shortLen, shortLen).Draw(rt, "short")
		long := append(append([]byte{}, short...), rapid.SliceOfN(rapid.Byte(), growBy, growBy).Draw(rt, "grown")...)

		for _, c := range []tokens.Class{tokens.ClassProse, tokens.ClassCode, tokens.ClassJSON, tokens.ClassDiff, tokens.ClassBinary} {
			gotShort := est.Estimate(short, c)
			gotLong := est.Estimate(long, c)
			if gotLong < gotShort {
				rt.Fatalf("class %s: longer input priced lower: len=%d->%v, len=%d->%v",
					c, len(short), gotShort, len(long), gotLong)
			}
		}
	})
}

func TestEstimate_FactorScalesResult(t *testing.T) {
	cfg := config.Defaults()
	est := tokens.New(cfg, "")
	payload := bytes.Repeat([]byte("a"), 4000)
	base := est.Estimate(payload, tokens.ClassProse)
	require.Equal(t, 1.0, est.Factor())

	// Calibrate the factor upward and confirm the SAME byte input now prices higher.
	for i := 0; i < 20; i++ {
		est.Calibrate(4000, 1000) // observed 4x estimated
	}
	require.Greater(t, est.Factor(), 1.0)
	require.Greater(t, est.Estimate(payload, tokens.ClassProse), base)
}
