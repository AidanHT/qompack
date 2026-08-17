package tokens_test

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/tokens"
)

// encodePNGDims encodes a grey PNG of the given dimensions, for the sizing assertions below.
func encodePNGDims(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := &png.Encoder{CompressionLevel: png.BestSpeed}
	require.NoError(t, enc.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))))
	return buf.Bytes()
}

// TestEstimateImage_PNG prices an image from its REAL dimensions rather than the host's flat
// 2 000 (§2.2, G10.2). 1024x768 is under the 1568 px long-edge clamp, so no downscale applies.
func TestEstimateImage_PNG(t *testing.T) {
	got, ok := tokens.EstimateImage(encodePNGDims(t, 1024, 768))
	require.True(t, ok, "a well-formed PNG must parse")
	// ceil(1024*768 / 750) == ceil(1048.576) == 1049
	require.Equal(t, core.Tokens(1049), got)
}

// TestEstimateImage_Downscale asserts the host's documented 1568 px long-edge clamp is applied
// before pricing, and that the configured ImageMaxTokens still caps the result.
func TestEstimateImage_Downscale(t *testing.T) {
	got, ok := tokens.EstimateImage(encodePNGDims(t, 4000, 3000))
	require.True(t, ok)

	// s = 1568/4000 = 0.392 -> 1568 x 1176 -> ceil(1843968/750) = 2459, capped at ImageMaxTokens.
	require.Equal(t, core.Tokens(config.Defaults().Runtime.Tokens.ImageMaxTokens), got,
		"a large image must clamp to runtime.tokens.imageMaxTokens")
	require.Equal(t, 1568, tokens.ImageLongEdgeClamp())
}

// TestEstimateImage_JPEG_GIF_WebP asserts every generated fixture's dimensions parse. WebP in
// particular has no standard-library decoder, so this proves the hand-written header parser works.
func TestEstimateImage_JPEG_GIF_WebP(t *testing.T) {
	for _, name := range []string{"tiny.jpg", "tiny.gif", "tiny.webp", "tiny.png"} {
		t.Run(name, func(t *testing.T) {
			got, ok := tokens.EstimateImage(readFixture(t, name))
			require.True(t, ok, "%s must parse", name)
			// Every fixture is 64x64: ceil(4096/750) == 6.
			require.Equal(t, core.Tokens(6), got, "%s: 64x64 at 750 px/token", name)
		})
	}
}

// TestEstimateImage_Garbage asserts an unparseable payload reports (0, false) rather than
// inventing a number, so Estimate can fall back to the unit scanner.
func TestEstimateImage_Garbage(t *testing.T) {
	garbage := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0xAB}, 56)...)
	got, ok := tokens.EstimateImage(garbage)
	require.False(t, ok, "a truncated PNG must not report a size")
	require.Equal(t, core.Tokens(0), got)
}

// TestEstimateImage_UnparseableFallsBackToUnitScanner pins Estimate's documented behaviour when
// EstimateImage declines: the bytes are priced as opaque content, never as a flat rate.
func TestEstimateImage_UnparseableFallsBackToUnitScanner(t *testing.T) {
	est := newExact(t)
	garbage := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0xAB}, 56)...)

	want := core.Tokens(math.Round(float64(tokens.Units(garbage)) * tokens.UnitWeight(tokens.ClassBinary)))
	require.Equal(t, want, est.Estimate(garbage, tokens.ClassImage))
}

// TestEstimatePDF_TextPDF asserts a text PDF is priced from its RECOVERED TEXT, not from a flat
// per-page rate — the specific behaviour §2.2 indicts and G10.2 closes.
func TestEstimatePDF_TextPDF(t *testing.T) {
	pdf := readFixture(t, "two-page-text.pdf")

	got, ok := tokens.EstimatePDF(pdf)
	require.True(t, ok, "a well-formed PDF must parse")

	perPage := config.Defaults().Runtime.Tokens.PDFTokensPerPage
	require.NotEqual(t, core.Tokens(2*perPage), got,
		"a TEXT pdf must not be priced at the flat per-page rate")
	require.NotEqual(t, core.Tokens(2000), got, "2 000 is the host's flat rate §2.2 indicts")
	require.Greater(t, int(got), 0)
	require.Less(t, int(got), 2*perPage, "recovered text must price below the scanned fallback here")
}

// TestEstimatePDF_ScannedPDF asserts a page with no recoverable text falls back to the CONFIGURED
// per-page rate — asserted against the config value, never against the literal 1800.
func TestEstimatePDF_ScannedPDF(t *testing.T) {
	got, ok := tokens.EstimatePDF(readFixture(t, "one-page-scanned.pdf"))
	require.True(t, ok)
	require.Equal(t, core.Tokens(config.Defaults().Runtime.Tokens.PDFTokensPerPage), got,
		"a scanned single page is priced at runtime.tokens.pdfTokensPerPage")
}

// TestEstimatePDF_ZeroPagesCountsAsOne pins the documented degenerate case.
func TestEstimatePDF_ZeroPagesCountsAsOne(t *testing.T) {
	got, ok := tokens.EstimatePDF([]byte("%PDF-1.4 no page objects at all"))
	require.True(t, ok)
	require.Equal(t, core.Tokens(config.Defaults().Runtime.Tokens.PDFTokensPerPage), got)
}
