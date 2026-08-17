package tokens_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/tokens"
)

// TestEstimate_ProseVsCode now lives in exact_test.go: the byte-ratio numbers this test used to
// assert (ceil(4000/4.0) and ceil(4000/3.6)) are exactly what SP-06's unit scanner replaces, so
// restating them here would pin the baseline the exact estimator exists to supersede. The
// rewritten test asserts the properties that survive — code prices above prose, and both stay in a
// plausible band relative to raw length — rather than two specific divisors.

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

// TestEstimate_ImageDecodeFailureFallsBackToUnitScanner replaces this test's original assertion.
//
// SP-01 priced an unparseable image at a flat bytes-per-token rate (ceil(2500/1000) == 3 tokens),
// which is the same flat-rating §2.2 indicts and G10.2 closes — 2 500 bytes of opaque content is
// not three tokens. The exact estimator instead falls back to the unit scanner at the BINARY unit
// weight, so undecodable media is priced exactly like any other opaque bytes.
func TestEstimate_ImageDecodeFailureFallsBackToUnitScanner(t *testing.T) {
	est := tokens.New(config.Defaults(), "")
	payload := bytes.Repeat([]byte{0xAB}, 2500) // not a decodable image of any format

	want := core.Tokens(math.Round(float64(tokens.Units(payload)) * tokens.UnitWeight(tokens.ClassBinary)))
	require.Equal(t, want, est.Estimate(payload, tokens.ClassImage))
	require.Greater(t, int(want), 100, "opaque bytes must not be flat-rated into near-nothing")
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
