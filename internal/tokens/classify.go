package tokens

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
)

// pdfMagic is the byte prefix every PDF file starts with, per the PDF spec's file-header
// requirement.
var pdfMagic = []byte("%PDF")

// diffGitPrefix is the line git prepends to every diff it generates.
var diffGitPrefix = []byte("diff --git")

// binarySniffWindow bounds how many leading bytes looksBinary inspects, so classifying a large
// tool result never means scanning all of it.
const binarySniffWindow = 8192

// binaryNonPrintableThreshold is the fraction of non-printable bytes, within the sniff window,
// above which content is classified binary.
const binaryNonPrintableThreshold = 0.30

// codeSniffMaxLines bounds how many leading lines looksCode inspects.
const codeSniffMaxLines = 200

// codeLineMatchThreshold is the fraction of inspected lines that must look code-shaped for
// looksCode to classify the content as code.
const codeLineMatchThreshold = 0.15

// codeKeywordPrefixes are the leading tokens looksCode treats as evidence of source code.
var codeKeywordPrefixes = []string{"func ", "def ", "class ", "import ", "const ", "let ", "var ", "#include"}

// Tool names Classify treats as code-shaped regardless of content, because their output is
// overwhelmingly source-file or match-line text even when it does not itself parse as code (a
// single matched line from Grep, or a file with no recognizable keyword in its first window).
const (
	toolFileRead = "FileRead"
	toolGrep     = "Grep"
)

// Classify assigns b (the content read from or written by tool at path) a Class, in this order:
// extension, then PDF magic bytes, then a binary sniff, then a "diff --git" body prefix, then
// JSON validity, then a code heuristic gated by tool name, falling back to prose.
func Classify(tool, path string, b []byte) Class {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return ClassImage
	case ".pdf":
		return ClassPDF
	case ".json", ".jsonl", ".ndjson":
		return ClassJSON
	case ".patch", ".diff":
		return ClassDiff
	case ".md", ".txt", ".rst":
		return ClassProse
	}

	// Image magic bytes, before the PDF check. Store content frequently arrives with Path == "",
	// where the extension switch above cannot fire, and without this a PNG would fall through to
	// the binary sniff and be priced as opaque bytes rather than from its real dimensions (G10.2).
	if looksImage(b) {
		return ClassImage
	}

	if len(b) >= len(pdfMagic) && bytes.HasPrefix(b, pdfMagic) {
		return ClassPDF
	}
	if looksBinary(b) {
		return ClassBinary
	}
	if bytes.HasPrefix(bytes.TrimLeft(b, " \t\r\n"), diffGitPrefix) {
		return ClassDiff
	}
	if json.Valid(b) {
		return ClassJSON
	}
	if tool == toolFileRead || tool == toolGrep || looksCode(b) {
		return ClassCode
	}
	return ClassProse
}

// looksImage reports whether b opens with the magic bytes of one of the four image formats the
// host accepts. It is header-only: the dimensions themselves are read later, by media.go.
func looksImage(b []byte) bool {
	return isPNG(b) || isJPEG(b) || isGIF(b) || isWebP(b)
}

// looksBinary reports whether the first binarySniffWindow bytes of b look like opaque binary
// content: any NUL byte, or more than binaryNonPrintableThreshold of the window is a C0 control
// character (excluding tab/LF/CR) or DEL. Bytes >= 0x80 are not counted as non-printable, so
// legitimate multi-byte UTF-8 prose and code are never misclassified.
func looksBinary(b []byte) bool {
	window := b
	if len(window) > binarySniffWindow {
		window = window[:binarySniffWindow]
	}
	if bytes.IndexByte(window, 0) >= 0 {
		return true
	}
	if len(window) == 0 {
		return false
	}
	nonPrintable := 0
	for _, c := range window {
		switch {
		case c == '\t' || c == '\n' || c == '\r':
			// whitespace, always printable for this purpose
		case c < 0x20 || c == 0x7f:
			nonPrintable++
		}
	}
	return float64(nonPrintable)/float64(len(window)) > binaryNonPrintableThreshold
}

// looksCode reports whether at least codeLineMatchThreshold of the first codeSniffMaxLines lines
// of b either end in {, ; or : or begin with a common source-code keyword.
func looksCode(b []byte) bool {
	lines := firstLines(b, codeSniffMaxLines)
	if len(lines) == 0 {
		return false
	}
	matches := 0
	for _, line := range lines {
		t := bytes.TrimSpace(bytes.TrimRight(line, "\r"))
		if len(t) == 0 {
			continue
		}
		if last := t[len(t)-1]; last == '{' || last == ';' || last == ':' {
			matches++
			continue
		}
		s := string(t)
		for _, kw := range codeKeywordPrefixes {
			if strings.HasPrefix(s, kw) {
				matches++
				break
			}
		}
	}
	return float64(matches)/float64(len(lines)) >= codeLineMatchThreshold
}

// firstLines splits b on '\n' and returns at most max lines, without allocating for lines beyond
// that cap.
func firstLines(b []byte, max int) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(b) && len(lines) < max; i++ {
		if b[i] == '\n' {
			lines = append(lines, b[start:i])
			start = i + 1
		}
	}
	if len(lines) < max && start < len(b) {
		lines = append(lines, b[start:])
	}
	return lines
}
