package main

import (
	"go/parser"
	"go/token"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestNoMagic_Analyzer runs the Analyzer against tools/lint/nomagic/testdata/src/a/a.go, which
// carries one violation per literal class (int, float) plus one line exempted by a
// "//nomagic:allow <reason>" comment, each asserted with a `// want` comment.
func TestNoMagic_Analyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer, "a")
}

// TestScanAllowComments_BareAllowIsNotExempt asserts the two halves of the //nomagic:allow
// contract directly against scanAllowComments, independent of the diagnostic-reporting plumbing
// exercised by TestNoMagic_Analyzer: a comment carrying a reason exempts its own line, and a bare
// "//nomagic:allow" with no reason exempts nothing and is reported instead (via its returned
// position — run() turns every such position into a diagnostic).
func TestScanAllowComments_BareAllowIsNotExempt(t *testing.T) {
	const src = `package a

const withReason = 1 //nomagic:allow this is fine

const bareAllow = 2 //nomagic:allow

const untouched = 3
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "a.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	allow, bare := scanAllowComments(fset, f)

	if !allow[3] {
		t.Errorf("line 3 (//nomagic:allow with a reason) must be exempt; allow=%v", allow)
	}
	if allow[5] {
		t.Errorf("line 5 (bare //nomagic:allow) must NOT be exempt; allow=%v", allow)
	}
	if len(bare) != 1 {
		t.Fatalf("want exactly one bare //nomagic:allow position, got %d: %v", len(bare), bare)
	}
	if line := fset.Position(bare[0]).Line; line != 5 {
		t.Errorf("bare //nomagic:allow reported at line %d, want line 5", line)
	}
}
