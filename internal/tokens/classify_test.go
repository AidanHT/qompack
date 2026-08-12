package tokens_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/tokens"
)

// TestClassify_Table exercises every branch of Classify's decision order: extension-based
// classes, PDF magic bytes, the binary sniff, the diff-body sniff, JSON validity, and the
// tool-name/code-heuristic gate, falling back to prose.
func TestClassify_Table(t *testing.T) {
	goSource := []byte("package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n")

	cases := []struct {
		name string
		tool string
		path string
		body []byte
		want tokens.Class
	}{
		{"png extension", "", "diagram.png", []byte("not really a png"), tokens.ClassImage},
		{"webp extension", "", "photo.webp", []byte("not really a webp"), tokens.ClassImage},
		{"pdf extension", "", "report.pdf", []byte("not really a pdf"), tokens.ClassPDF},
		{"pdf magic bytes no extension", "", "", []byte("%PDF-1.7 rest of file"), tokens.ClassPDF},
		{"json extension", "", "data.json", []byte("not valid json at all {["), tokens.ClassJSON},
		{"jsonl extension", "", "log.jsonl", []byte(`{"a":1}`), tokens.ClassJSON},
		{"valid json sniffed without extension", "", "", []byte(`{"ok":true,"n":1}`), tokens.ClassJSON},
		{"diff --git body sniffed", "", "", []byte("diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n"), tokens.ClassDiff},
		{"diff extension", "", "change.diff", []byte("anything"), tokens.ClassDiff},
		{"md extension is prose", "", "README.md", []byte("# Title\n\nSome prose."), tokens.ClassProse},
		{"go source sniffed as code", "", "", goSource, tokens.ClassCode},
		{"NUL-containing bytes are binary", "", "", []byte{'a', 'b', 0x00, 'c', 'd'}, tokens.ClassBinary},
		{"Grep tool forces code regardless of content", "Grep", "", []byte("plain match line, no code shape"), tokens.ClassCode},
		{"empty content falls back to prose", "", "", []byte{}, tokens.ClassProse},
	}

	require.Len(t, cases, 14, "the subplan's TestClassify_Table calls for exactly 14 cases")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tokens.Classify(tc.tool, tc.path, tc.body)
			require.Equal(t, tc.want, got, "Classify(%q, %q, %d bytes)", tc.tool, tc.path, len(tc.body))
		})
	}
}

func TestClassify_FileReadToolForcesCode(t *testing.T) {
	got := tokens.Classify("FileRead", "", []byte("arbitrary text with no code shape at all"))
	require.Equal(t, tokens.ClassCode, got)
}

func TestClassify_JPEGAndGIFExtensions(t *testing.T) {
	require.Equal(t, tokens.ClassImage, tokens.Classify("", "a.jpg", nil))
	require.Equal(t, tokens.ClassImage, tokens.Classify("", "a.jpeg", nil))
	require.Equal(t, tokens.ClassImage, tokens.Classify("", "a.gif", nil))
}

func TestClassify_TxtAndRstAreProse(t *testing.T) {
	require.Equal(t, tokens.ClassProse, tokens.Classify("", "notes.txt", []byte("free text")))
	require.Equal(t, tokens.ClassProse, tokens.Classify("", "doc.rst", []byte("free text")))
}

func TestClassify_ExtensionIsCaseInsensitive(t *testing.T) {
	require.Equal(t, tokens.ClassImage, tokens.Classify("", "SCREENSHOT.PNG", nil))
	require.Equal(t, tokens.ClassPDF, tokens.Classify("", "REPORT.PDF", nil))
}

func TestClass_String(t *testing.T) {
	cases := map[tokens.Class]string{
		tokens.ClassProse:  "prose",
		tokens.ClassCode:   "code",
		tokens.ClassJSON:   "json",
		tokens.ClassDiff:   "diff",
		tokens.ClassImage:  "image",
		tokens.ClassPDF:    "pdf",
		tokens.ClassBinary: "binary",
	}
	for c, want := range cases {
		require.Equal(t, want, c.String())
	}
	require.Equal(t, "unknown", tokens.Class(255).String())
}
