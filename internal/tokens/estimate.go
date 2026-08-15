package tokens

import (
	"bytes"
	"context"
	"image"
	// Registered so image.DecodeConfig can read PNG/JPEG/GIF headers. No third-party decoder is
	// registered (WebP has none in the standard library): a .webp payload simply falls through to
	// estimateImage's byte-length fallback, which is the documented, expected behaviour rather
	// than a bug.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// Estimator prices content in tokens. Estimate/EstimateString/EstimateRoot are pure given the
// current Factor; Calibrate is the only method that mutates state.
type Estimator interface {
	Estimate(b []byte, c Class) core.Tokens
	EstimateString(s string, c Class) core.Tokens
	// EstimateRoot uses per-chunk cached measurements keyed by chunk hash — the "exact chunk-level
	// accounting" G10.2 asks for — so it never re-scans bytes already accounted for. It takes
	// []core.ChunkRef, not a store type, because tokens must not import store (§3.2).
	EstimateRoot(ctx context.Context, chunks []core.ChunkRef, c Class) core.Tokens
	// Calibrate nudges Factor toward observed/estimated by one exponential-moving-average step and
	// persists the result. Calls where estimated == 0 are ignored.
	Calibrate(observed core.Tokens, estimated core.Tokens)
	Factor() float64
}

// imageFallbackBytesPerToken is the divisor Estimate falls back to when image.DecodeConfig cannot
// read b's dimensions (an unrecognized or corrupt format, including every .webp payload, since no
// third-party WebP decoder is registered).
const imageFallbackBytesPerToken = 1000

// charsPerToken returns cfg's characters-per-token constant for c. Image and PDF are priced by
// dimensions/pages, not characters, so both fall back to the binary rate: whatever raw bytes
// remain in an image/PDF estimate path (there normally are none — see estimateImage/estimatePDF)
// are at least priced consistently with other opaque content.
func charsPerToken(cfg config.RTokensCfg, c Class) float64 {
	switch c {
	case ClassProse:
		return cfg.ProseCharsPerToken
	case ClassCode:
		return cfg.CodeCharsPerToken
	case ClassJSON:
		return cfg.JSONCharsPerToken
	case ClassDiff:
		return cfg.DiffCharsPerToken
	case ClassImage, ClassPDF, ClassBinary:
		return cfg.BinaryCharsPerToken
	default:
		return cfg.BinaryCharsPerToken
	}
}

// ceilDivBytes returns ceil(n / per), treating per <= 0 as "one token per byte" rather than
// dividing by zero.
func ceilDivBytes(n int, per float64) int {
	if per <= 0 {
		return n
	}
	return int(math.Ceil(float64(n) / per))
}

// rawEstimate computes the unscaled (pre-Factor) token count for b under class c.
func rawEstimate(b []byte, c Class, cfg config.RTokensCfg) int {
	switch c {
	case ClassImage:
		return estimateImage(b, cfg)
	case ClassPDF:
		return estimatePDF(b, cfg)
	default:
		return ceilDivBytes(len(b), charsPerToken(cfg, c))
	}
}

// estimateImage prices an image from its actual pixel dimensions, read via image.DecodeConfig
// (which reads only the header, not the full pixel data). On decode failure it falls back to a
// flat bytes-per-token rate, and every result is capped at ImageMaxTokens.
func estimateImage(b []byte, cfg config.RTokensCfg) int {
	cfgImg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return ceilDivBytes(len(b), imageFallbackBytesPerToken)
	}
	perToken := int64(cfg.ImagePixelsPerToken)
	if perToken <= 0 {
		perToken = 1
	}
	pixels := int64(cfgImg.Width) * int64(cfgImg.Height)
	toks := (pixels + perToken - 1) / perToken // ceil division, int64-safe for large images
	if maxToks := int64(cfg.ImageMaxTokens); maxToks > 0 && toks > maxToks {
		toks = maxToks
	}
	return int(toks)
}

// pdfTypeTag and pdfPageTag are the two byte strings countPDFPages looks for in sequence.
var (
	pdfTypeTag = []byte("/Type")
	pdfPageTag = []byte("/Page")
)

// estimatePDF prices a PDF by its page count (at least one) times PDFTokensPerPage.
func estimatePDF(b []byte, cfg config.RTokensCfg) int {
	pages := countPDFPages(b)
	if pages < 1 {
		pages = 1
	}
	return pages * cfg.PDFTokensPerPage
}

// countPDFPages counts "/Type" occurrences (with optional spaces before the following tag)
// followed by "/Page" NOT immediately followed by "s" — i.e. a page object, not the catalog's
// /Type /Pages tree root.
func countPDFPages(b []byte) int {
	count := 0
	i := 0
	for {
		idx := bytes.Index(b[i:], pdfTypeTag)
		if idx < 0 {
			return count
		}
		pos := i + idx + len(pdfTypeTag)
		j := pos
		for j < len(b) && b[j] == ' ' {
			j++
		}
		if bytes.HasPrefix(b[j:], pdfPageTag) {
			after := j + len(pdfPageTag)
			if after >= len(b) || b[after] != 's' {
				count++
			}
		}
		i = pos
	}
}

// Estimate returns round(raw(b, c) * Factor()).
func (e *estimator) Estimate(b []byte, c Class) core.Tokens {
	raw := rawEstimate(b, c, e.cfg)
	return core.Tokens(math.Round(float64(raw) * e.Factor()))
}

// EstimateString is Estimate([]byte(s), c); it exists so a caller holding a string never has to
// think about the []byte conversion itself.
func (e *estimator) EstimateString(s string, c Class) core.Tokens {
	return e.Estimate([]byte(s), c)
}

// EstimateRoot sums each chunk's estimate, memoized by chunk hash so a chunk shared by multiple
// roots (the common case after dedup) is only priced once per process lifetime. The memo is keyed
// by hash alone, matching the shape SP-06 replaces with a measured value — callers are expected to
// classify a chunk consistently across calls, since the memo does not distinguish class per hash.
func (e *estimator) EstimateRoot(_ context.Context, chunks []core.ChunkRef, c Class) core.Tokens {
	per := charsPerToken(e.cfg, c)
	var total core.Tokens

	e.rootMu.Lock()
	defer e.rootMu.Unlock()
	for _, ch := range chunks {
		toks, ok := e.rootCache[ch.Hash]
		if !ok {
			toks = core.Tokens(ceilDivBytes(ch.Len, per))
			e.rootCache[ch.Hash] = toks
		}
		total += toks
	}
	return total
}
