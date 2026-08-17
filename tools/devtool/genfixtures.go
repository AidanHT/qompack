package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

// mediaFixtureDir is the directory, relative to the repository root, that gen-fixtures writes the
// internal/tokens media corpus into. The fixtures are GENERATED rather than vendored so that a
// reviewer can regenerate every byte from source and never has to trust an opaque committed blob
// (00-ARCHITECTURE.md §6.1: fixtures are code).
const mediaFixtureDir = "testdata/corpora/media"

// mediaFixtureMaxBytes is the size ceiling every generated media fixture must stay under. The
// corpus exists to exercise header parsing, not compression, so anything approaching this size
// means a generator regressed into emitting real pixel data.
const mediaFixtureMaxBytes = 12 << 10

// fixtureImageDim is the pixel width and height of the three raster fixtures. It is deliberately
// small: EstimateImage reads dimensions out of the header and never decodes pixels, so a larger
// image would cost bytes without testing anything new.
const fixtureImageDim = 64

// taskGenFixtures regenerates the internal/tokens media corpus under testdata/corpora/media.
func taskGenFixtures(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: gen-fixtures takes no arguments", errUsage)
	}

	dir := filepath.Join(root, filepath.FromSlash(mediaFixtureDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("gen-fixtures: mkdir %s: %w", dir, err)
	}

	fixtures := []struct {
		name string
		gen  func() ([]byte, error)
	}{
		{"tiny.png", genTinyPNG},
		{"tiny.jpg", genTinyJPEG},
		{"tiny.gif", genTinyGIF},
		{"tiny.webp", genTinyWebP},
		{"two-page-text.pdf", genTwoPageTextPDF},
		{"one-page-scanned.pdf", genOnePageScannedPDF},
	}

	for _, f := range fixtures {
		b, err := f.gen()
		if err != nil {
			return fmt.Errorf("gen-fixtures: %s: %w", f.name, err)
		}
		if len(b) > mediaFixtureMaxBytes {
			return fmt.Errorf("gen-fixtures: %s is %d bytes, over the %d-byte fixture ceiling",
				f.name, len(b), mediaFixtureMaxBytes)
		}
		out := filepath.Join(dir, f.name)
		if err := os.WriteFile(out, b, 0o644); err != nil {
			return fmt.Errorf("gen-fixtures: write %s: %w", out, err)
		}
		fmt.Printf("gen-fixtures: wrote %s (%d bytes)\n", filepath.ToSlash(filepath.Join(mediaFixtureDir, f.name)), len(b))
	}
	return nil
}

// fixtureImage builds the single source image every raster fixture encodes: a flat grey square, so
// that each encoder produces a small file whose header still carries honest dimensions.
func fixtureImage() *image.Gray {
	img := image.NewGray(image.Rect(0, 0, fixtureImageDim, fixtureImageDim))
	for y := 0; y < fixtureImageDim; y++ {
		for x := 0; x < fixtureImageDim; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8((x ^ y) & 0xFF)})
		}
	}
	return img
}

func genTinyPNG() ([]byte, error) {
	var buf bytes.Buffer
	enc := &png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, fixtureImage()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func genTinyJPEG() ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, fixtureImage(), &jpeg.Options{Quality: 40}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func genTinyGIF() ([]byte, error) {
	var buf bytes.Buffer
	if err := gif.Encode(&buf, fixtureImage(), nil); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// webpVP8LSignature is the one-byte signature that opens a lossless WebP (VP8L) bitstream.
const webpVP8LSignature = 0x2F

// genTinyWebP hand-assembles a minimal lossless (VP8L) WebP container.
//
// The Go standard library has no WebP encoder, so the bytes are written directly. Only the header
// matters here: EstimateImage reads the 14-bit width and height packed immediately after the VP8L
// signature and never decodes the entropy-coded image data, so a truncated bitstream body is
// exactly the right fixture — it proves the parser reads dimensions from the header rather than by
// decoding.
func genTinyWebP() ([]byte, error) {
	const w, h = fixtureImageDim, fixtureImageDim

	// VP8L payload: signature byte, then a little-endian bitstream whose first 14 bits are
	// width-1, next 14 bits are height-1, then 1 alpha bit and 3 version bits.
	packed := uint32(w-1) | uint32(h-1)<<14
	payload := make([]byte, 5)
	payload[0] = webpVP8LSignature
	binary.LittleEndian.PutUint32(payload[1:], packed)

	var chunk bytes.Buffer
	chunk.WriteString("VP8L")
	if err := binary.Write(&chunk, binary.LittleEndian, uint32(len(payload))); err != nil {
		return nil, err
	}
	chunk.Write(payload)
	if chunk.Len()%2 == 1 { // RIFF chunks are padded to an even length.
		chunk.WriteByte(0)
	}

	var out bytes.Buffer
	out.WriteString("RIFF")
	if err := binary.Write(&out, binary.LittleEndian, uint32(4+chunk.Len())); err != nil {
		return nil, err
	}
	out.WriteString("WEBP")
	out.Write(chunk.Bytes())
	return out.Bytes(), nil
}

// pdfFixtureWords is the approximate word count genTwoPageTextPDF spreads across its two pages.
// EstimatePDF's scanned-vs-text discriminator is 50 units per page, so a text fixture has to carry
// substantially more than that to be classified as text rather than scanned.
const pdfFixtureWords = 900

// pdfLoremWords is the small vocabulary the text fixture's body is built from.
var pdfLoremWords = []string{
	"lorem", "ipsum", "dolor", "sit", "amet", "consectetur", "adipiscing", "elit",
	"sed", "eiusmod", "tempor", "incididunt", "labore", "magna", "aliqua", "veniam",
}

// flateStream zlib-compresses b, which is how EstimatePDF expects to find a /FlateDecode stream's
// payload on disk.
func flateStream(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// buildPDF assembles a minimal but structurally honest PDF from one already-compressed content
// stream per page. It is not a full writer — there is no xref table — because EstimatePDF scans
// for "/Type /Page" markers and /FlateDecode streams rather than parsing the document structure.
func buildPDF(pageStreams [][]byte) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	out.WriteString("1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n")

	kids := make([]string, 0, len(pageStreams))
	for i := range pageStreams {
		kids = append(kids, fmt.Sprintf("%d 0 R", 3+i*2))
	}
	fmt.Fprintf(&out, "2 0 obj << /Type /Pages /Kids [%s] /Count %d >> endobj\n",
		strings.Join(kids, " "), len(pageStreams))

	for i, stream := range pageStreams {
		pageObj := 3 + i*2
		contentObj := pageObj + 1
		fmt.Fprintf(&out, "%d 0 obj << /Type /Page /Parent 2 0 R /Contents %d 0 R >> endobj\n",
			pageObj, contentObj)
		fmt.Fprintf(&out, "%d 0 obj << /Length %d /Filter /FlateDecode >>\nstream\n",
			contentObj, len(stream))
		out.Write(stream)
		out.WriteString("\nendstream\nendobj\n")
	}
	out.WriteString("trailer << /Root 1 0 R >>\n%%EOF\n")
	return out.Bytes()
}

func genTwoPageTextPDF() ([]byte, error) {
	const pages = 2
	streams := make([][]byte, 0, pages)
	for p := 0; p < pages; p++ {
		var body bytes.Buffer
		body.WriteString("BT /F1 12 Tf 72 720 Td\n")
		for i := 0; i < pdfFixtureWords/pages; i++ {
			if i > 0 && i%12 == 0 {
				body.WriteString(") Tj T*\n(")
			}
			body.WriteString(pdfLoremWords[(i+p)%len(pdfLoremWords)])
			body.WriteByte(' ')
		}
		body.WriteString(") Tj ET\n")
		s, err := flateStream(body.Bytes())
		if err != nil {
			return nil, err
		}
		streams = append(streams, s)
	}
	return buildPDF(streams), nil
}

func genOnePageScannedPDF() ([]byte, error) {
	// A scanned page carries an image, not text: the compressed stream is pixel data with no
	// recoverable printable run long enough to clear EstimatePDF's 50-units-per-page threshold.
	//
	// Every byte is kept at or above 0x80 deliberately. Pixel values spread across the full 0-255
	// range would land in the printable ASCII band often enough to form runs of three or more, and
	// EstimatePDF would then "recover" enough pseudo-text to classify the page as text — which is
	// a property of the fixture, not of the estimator, and would make this fixture silently stop
	// testing the scanned path.
	pixels := make([]byte, 2048)
	for i := range pixels {
		pixels[i] = byte(0x80 + (i*7+3)%0x7F)
	}
	s, err := flateStream(pixels)
	if err != nil {
		return nil, err
	}
	return buildPDF([][]byte{s}), nil
}
