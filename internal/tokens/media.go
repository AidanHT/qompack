package tokens

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
	"math"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// imageLongEdgeClamp is the host's documented long-edge pixel clamp: an image is downscaled so its
// longer side is at most this many pixels before it is priced. It has no runtime.tokens.* key
// because it is not a Qompack tuning knob — it is a property of the model API this estimate has to
// agree with — which is why it is one of only two literals this package introduces.
const imageLongEdgeClamp = 1568

// EstimateImage prices b from its real pixel dimensions using the built-in defaults. It reports
// ok == false when b's header cannot be parsed, so the caller can fall back to the unit scanner
// rather than invent a number. The estimator's own method honours a project's config override;
// this package-level form is the convenience wrapper for callers that have no Config in hand.
func EstimateImage(b []byte) (core.Tokens, bool) {
	return estimateImageWith(b, config.Defaults().Runtime.Tokens)
}

// EstimatePDF prices b from its real page count and recovered text using the built-in defaults.
// See EstimateImage for why a package-level wrapper exists alongside the estimator's method.
func EstimatePDF(b []byte) (core.Tokens, bool) {
	return estimatePDFWith(b, config.Defaults().Runtime.Tokens)
}

// estimateImage is the estimator's own image pricing, honouring its configured constants.
func (e *exact) estimateImage(b []byte) (core.Tokens, bool) { return estimateImageWith(b, e.cfg) }

// estimatePDF is the estimator's own PDF pricing, honouring its configured constants.
func (e *exact) estimatePDF(b []byte) (core.Tokens, bool) { return estimatePDFWith(b, e.cfg) }

// estimateImageWith prices an image from its dimensions: clamp the long edge to
// imageLongEdgeClamp, divide the resulting pixel area by cfg.ImagePixelsPerToken, and bound the
// result to [1, cfg.ImageMaxTokens].
func estimateImageWith(b []byte, cfg config.RTokensCfg) (core.Tokens, bool) {
	w, h, ok := imageDimensions(b)
	if !ok || w <= 0 || h <= 0 {
		return 0, false
	}

	// The downscale is integer arithmetic on purpose: scaling by a float ratio would make the
	// result depend on binary rounding (4000 * (1568.0/4000) can land just under 1568), and these
	// numbers are asserted exactly by tests and compared against the host's own sizing.
	sw, sh := int64(w), int64(h)
	maxDim := sw
	if sh > maxDim {
		maxDim = sh
	}
	if maxDim > imageLongEdgeClamp {
		sw = sw * imageLongEdgeClamp / maxDim
		sh = sh * imageLongEdgeClamp / maxDim
	}

	perToken := int64(cfg.ImagePixelsPerToken)
	if perToken <= 0 {
		perToken = 1
	}
	pixels := sw * sh
	toks := int((pixels + perToken - 1) / perToken) // ceil division, int64-safe for large images

	if toks < 1 {
		toks = 1
	}
	if maxToks := cfg.ImageMaxTokens; maxToks > 0 && toks > maxToks {
		toks = maxToks
	}
	return core.Tokens(toks), true
}

// imageDimensions reads an image's pixel dimensions straight out of its header, for the four
// formats the host accepts. It never decodes pixel data, so it is O(header) regardless of size and
// works for WebP, which the standard library cannot decode at all.
func imageDimensions(b []byte) (w, h int, ok bool) {
	switch {
	case isPNG(b):
		return pngDimensions(b)
	case isJPEG(b):
		return jpegDimensions(b)
	case isGIF(b):
		return gifDimensions(b)
	case isWebP(b):
		return webpDimensions(b)
	default:
		return 0, 0, false
	}
}

// pngMagic is the 8-byte PNG file signature.
var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}

// jpegMagic is the 3-byte prefix every JPEG starts with (SOI plus the next marker's 0xFF).
var jpegMagic = []byte{0xFF, 0xD8, 0xFF}

// gifMagic87 and gifMagic89 are the two GIF version signatures.
var (
	gifMagic87 = []byte("GIF87a")
	gifMagic89 = []byte("GIF89a")
)

func isPNG(b []byte) bool  { return bytes.HasPrefix(b, pngMagic) }
func isJPEG(b []byte) bool { return bytes.HasPrefix(b, jpegMagic) }

func isGIF(b []byte) bool {
	return bytes.HasPrefix(b, gifMagic87) || bytes.HasPrefix(b, gifMagic89)
}

func isWebP(b []byte) bool {
	return len(b) >= 12 && bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP"))
}

// pngIHDRTag is the chunk type that carries a PNG's dimensions; it is required to be the first
// chunk, immediately after the signature.
var pngIHDRTag = []byte("IHDR")

// pngDimensions reads width and height from the IHDR chunk: two big-endian uint32s at offsets 16
// and 20, immediately after the 8-byte signature, the 4-byte chunk length and the 4-byte tag.
func pngDimensions(b []byte) (int, int, bool) {
	const ihdrEnd = 24
	if len(b) < ihdrEnd || !bytes.Equal(b[12:16], pngIHDRTag) {
		return 0, 0, false
	}
	return int(binary.BigEndian.Uint32(b[16:20])), int(binary.BigEndian.Uint32(b[20:24])), true
}

// isSOFMarker reports whether code is a Start-Of-Frame marker carrying dimensions. The three gaps
// (0xC4 DHT, 0xC8 JPG, 0xCC DAC) are not frame headers and must not be read as one.
func isSOFMarker(code byte) bool {
	switch code {
	case 0xC4, 0xC8, 0xCC:
		return false
	default:
		return code >= 0xC0 && code <= 0xCF
	}
}

// jpegDimensions walks the marker chain to the first Start-Of-Frame segment and reads the height
// and width stored immediately after its one-byte sample precision.
func jpegDimensions(b []byte) (int, int, bool) {
	i := 2 // past SOI
	for i+1 < len(b) {
		if b[i] != 0xFF {
			i++
			continue
		}
		// Runs of 0xFF are legal marker padding; the marker code is the first non-0xFF byte.
		j := i
		for j < len(b) && b[j] == 0xFF {
			j++
		}
		if j >= len(b) {
			return 0, 0, false
		}
		code := b[j]

		// Standalone markers carry no length field.
		if code == 0x01 || (code >= 0xD0 && code <= 0xD9) {
			i = j + 1
			continue
		}
		if j+3 > len(b) {
			return 0, 0, false
		}
		segLen := int(binary.BigEndian.Uint16(b[j+1 : j+3]))
		if segLen < 2 {
			return 0, 0, false
		}
		if isSOFMarker(code) {
			// precision at j+3, height at j+4, width at j+6.
			if j+8 > len(b) {
				return 0, 0, false
			}
			h := int(binary.BigEndian.Uint16(b[j+4 : j+6]))
			w := int(binary.BigEndian.Uint16(b[j+6 : j+8]))
			return w, h, true
		}
		i = j + 1 + segLen
	}
	return 0, 0, false
}

// gifDimensions reads the logical screen descriptor's two little-endian uint16s, at offsets 6 and
// 8, immediately after the 6-byte signature.
func gifDimensions(b []byte) (int, int, bool) {
	const descriptorEnd = 10
	if len(b) < descriptorEnd {
		return 0, 0, false
	}
	return int(binary.LittleEndian.Uint16(b[6:8])), int(binary.LittleEndian.Uint16(b[8:10])), true
}

// webpDimensions reads dimensions from whichever of the three WebP bitstream forms the file uses.
// The RIFF header is 12 bytes and the first chunk's 4-byte FOURCC and 4-byte size follow, so every
// payload below is measured from offset 20.
func webpDimensions(b []byte) (int, int, bool) {
	const payload = 20
	if len(b) < payload {
		return 0, 0, false
	}
	switch string(b[12:16]) {
	case "VP8X":
		// 1 byte flags, 3 reserved, then 24-bit canvas width-1 and height-1, little-endian.
		if len(b) < payload+10 {
			return 0, 0, false
		}
		w := int(b[24]) | int(b[25])<<8 | int(b[26])<<16
		h := int(b[27]) | int(b[28])<<8 | int(b[29])<<16
		return w + 1, h + 1, true

	case "VP8 ":
		// 3-byte frame tag, the 3-byte start code, then two 14-bit dimensions.
		if len(b) < payload+10 {
			return 0, 0, false
		}
		if b[23] != 0x9D || b[24] != 0x01 || b[25] != 0x2A {
			return 0, 0, false
		}
		w := int(binary.LittleEndian.Uint16(b[26:28]) & 0x3FFF)
		h := int(binary.LittleEndian.Uint16(b[28:30]) & 0x3FFF)
		return w, h, true

	case "VP8L":
		// One signature byte, then 14 bits of width-1 and 14 bits of height-1, packed
		// little-endian.
		if len(b) < payload+5 || b[20] != 0x2F {
			return 0, 0, false
		}
		packed := binary.LittleEndian.Uint32(b[21:25])
		return int(packed&0x3FFF) + 1, int((packed>>14)&0x3FFF) + 1, true

	default:
		return 0, 0, false
	}
}

// pdfScannedUnitsPerPage is the recovered-text threshold, per page, below which a PDF is treated
// as SCANNED rather than text. A scanned page carries an image and essentially no extractable
// text, so pricing it by its (empty) text would badly under-count what the model actually reads.
const pdfScannedUnitsPerPage = 50

// pdfMinPrintableRun is the shortest run of printable bytes estimatePDF keeps when recovering text
// from an inflated content stream. Shorter runs are almost always operator fragments or binary
// coincidence rather than real words.
const pdfMinPrintableRun = 3

// estimatePDFWith prices a PDF from its real page count and, when it has recoverable text, from
// that text — never from the host's flat 2 000 tokens (§2.2, G10.2).
func estimatePDFWith(b []byte, cfg config.RTokensCfg) (core.Tokens, bool) {
	pages := countPDFPages(b)
	if pages < 1 {
		pages = 1
	}

	text := extractPDFText(b)
	textUnits := units(text)

	if textUnits < pdfScannedUnitsPerPage*pages {
		// Scanned: no usable text, so the configured per-page rate is the honest estimate.
		return core.Tokens(pages * cfg.PDFTokensPerPage), true
	}

	toks := int(math.Round(float64(textUnits) * unitWeight[ClassProse]))
	if toks < pages {
		toks = pages
	}
	return core.Tokens(toks), true
}

// pdfTypeTag and pdfPageTag are the two byte strings countPDFPages looks for in sequence.
var (
	pdfTypeTag = []byte("/Type")
	pdfPageTag = []byte("/Page")
)

// isPDFSpace reports whether c is PDF whitespace between a key and its value.
func isPDFSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == 0
}

// countPDFPages counts "/Type" occurrences followed by "/Page" NOT immediately followed by "s" —
// i.e. a page object, never the catalog's /Type /Pages tree root.
func countPDFPages(b []byte) int {
	count, i := 0, 0
	for {
		idx := bytes.Index(b[i:], pdfTypeTag)
		if idx < 0 {
			return count
		}
		pos := i + idx + len(pdfTypeTag)
		j := pos
		for j < len(b) && isPDFSpace(b[j]) {
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

// pdfStreamOpen and pdfStreamClose bracket a PDF content stream's raw bytes.
var (
	pdfStreamOpen  = []byte("stream")
	pdfStreamClose = []byte("endstream")
)

// pdfMaxInflatedBytes bounds how much text estimatePDF will recover from one document, so a
// maliciously or accidentally huge PDF cannot turn a token estimate into an unbounded allocation.
const pdfMaxInflatedBytes = 8 << 20

// extractPDFText inflates every deflate-compressed content stream it can and returns the printable
// runs recovered from them. A stream that will not inflate — because it uses another filter, or is
// genuinely image data — is skipped rather than treated as an error: a partial recovery still
// prices better than a flat per-page rate.
func extractPDFText(b []byte) []byte {
	var out []byte
	i := 0
	for len(out) < pdfMaxInflatedBytes {
		idx := bytes.Index(b[i:], pdfStreamOpen)
		if idx < 0 {
			break
		}
		start := i + idx + len(pdfStreamOpen)
		// The stream keyword is followed by CRLF or LF.
		for start < len(b) && (b[start] == '\r' || b[start] == '\n') {
			start++
		}
		end := bytes.Index(b[start:], pdfStreamClose)
		if end < 0 {
			break
		}
		raw := b[start : start+end]
		i = start + end + len(pdfStreamClose)

		if inflated, err := inflate(raw); err == nil {
			out = append(out, printableRuns(inflated)...)
		}
	}
	return out
}

// inflate zlib-decompresses b, bounded by pdfMaxInflatedBytes.
func inflate(b []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	return io.ReadAll(io.LimitReader(zr, pdfMaxInflatedBytes))
}

// printableRuns keeps every run of at least pdfMinPrintableRun printable bytes, joined by spaces,
// discarding the binary noise around them.
func printableRuns(b []byte) []byte {
	var out []byte
	runStart := -1
	flush := func(end int) {
		if runStart >= 0 && end-runStart >= pdfMinPrintableRun {
			out = append(out, b[runStart:end]...)
			out = append(out, ' ')
		}
		runStart = -1
	}
	for i, c := range b {
		if c >= 0x20 && c < 0x7F {
			if runStart < 0 {
				runStart = i
			}
			continue
		}
		flush(i)
	}
	flush(len(b))
	return out
}
