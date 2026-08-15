// Command nomagic is the D11 / 00-ARCHITECTURE.md §11.6 analysis pass: it forbids float and int
// literals that duplicate a config default from appearing outside internal/config/defaults.go.
// It is invoked as `go run ./tools/lint/nomagic ./...` (see tools/devtool/lint.go), and is one of
// the sub-checks `devtool lint` runs in order.
package main

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Analyzer forbids literals that duplicate a config default outside internal/config/defaults.go
// (D11, 00-ARCHITECTURE.md §11.6).
var Analyzer = &analysis.Analyzer{
	Name: "nomagic",
	Doc:  "forbids literals that duplicate a config default outside internal/config/defaults.go (D11, §11.6)",
	Run:  run,
}

// modulePath is github.com/qompack/qompack's own module path. It is used only to tell a real
// in-repo package apart from the synthetic, module-less "a" package that
// TestNoMagic_Analyzer loads (via analysistest's legacy GOPATH-style testdata/src/a fixture) —
// see the comment on exemptFile for why that distinction has to exist at all.
const modulePath = "github.com/qompack/qompack"

func run(pass *analysis.Pass) (any, error) {
	// inModule is false only for analysistest's own legacy-GOPATH fixture package ("a", with no
	// module qualification at all): every real package loaded by `go run ./tools/lint/nomagic
	// ./...` against this repository has an import path under modulePath. The distinction matters
	// because the fixture in testdata/src/a/a.go — which testdata/src/a/a.go and
	// TestNoMagic_Analyzer require to exist at exactly that path, per analysistest's own
	// convention — would otherwise always match the "anything under testdata/" exemption below
	// and could never demonstrate a real diagnostic. A real file actually located under this
	// repository's tools/, test/, or testdata/ directories always has an in-module import path,
	// so gating the directory exemptions on inModule changes no production behaviour.
	inModule := pass.Pkg.Path() == modulePath || strings.HasPrefix(pass.Pkg.Path(), modulePath+"/")
	for _, f := range pass.Files {
		name := pass.Fset.Position(f.Pos()).Filename
		if exemptFile(name, inModule) {
			continue
		}
		allow, bare := scanAllowComments(pass.Fset, f)
		for _, p := range bare {
			pass.Reportf(p, "bare //nomagic:allow with no reason; state why this literal is exempt (D11, §11.6)")
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok {
				return true
			}
			line := pass.Fset.Position(lit.Pos()).Line
			if allow[line] {
				return true
			}
			switch lit.Kind {
			case token.INT:
				v, err := strconv.ParseInt(lit.Value, 0, 64)
				if err == nil && containsInt(forbiddenInts, v) {
					pass.Reportf(lit.Pos(), "literal %d duplicates a config default; read it from config (D11, §11.6)", v)
				}
			case token.FLOAT:
				v, err := strconv.ParseFloat(lit.Value, 64)
				if err == nil && containsFloat(forbiddenFloats, v) {
					pass.Reportf(lit.Pos(), "literal %s duplicates a config default; read it from config (D11, §11.6)", lit.Value)
				}
			}
			return true
		})
	}
	return nil, nil
}

// exemptFile reports whether name (an absolute or slash/backslash-mixed filename as reported by
// token.Position) is outside nomagic's scope: internal/config/defaults.go (the one file allowed
// to spell out the config defaults as literals), any _test.go file, and — for files that are part
// of this module (see the inModule comment on run) — anything under tools/, test/, or testdata/.
func exemptFile(name string, inModule bool) bool {
	p := filepath.ToSlash(name)
	if strings.HasSuffix(p, "_test.go") {
		return true
	}
	if p == "internal/config/defaults.go" || strings.HasSuffix(p, "/internal/config/defaults.go") {
		return true
	}
	if !inModule {
		return false
	}
	for _, dir := range []string{"tools/", "test/", "testdata/"} {
		if strings.HasPrefix(p, dir) {
			return true
		}
		if strings.Contains(p, "/"+dir) {
			return true
		}
	}
	return false
}

// allowMarker is the exact directive nomagic recognizes in a line comment.
const allowMarker = "nomagic:allow"

// scanAllowComments walks every comment in f and returns two things: allow, a set of line numbers
// exempted by a "//nomagic:allow <reason>" comment carrying a non-empty reason; and bare, the
// positions of every "//nomagic:allow" comment that carries no reason at all — which is itself a
// violation (a reason must be stated), so it never exempts anything.
func scanAllowComments(fset *token.FileSet, f *ast.File) (allow map[int]bool, bare []token.Pos) {
	allow = make(map[int]bool)
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			text := commentText(c.Text)
			switch {
			case text == allowMarker:
				bare = append(bare, c.Pos())
			case strings.HasPrefix(text, allowMarker+" "), strings.HasPrefix(text, allowMarker+"\t"):
				reason := strings.TrimSpace(text[len(allowMarker):])
				if reason == "" {
					bare = append(bare, c.Pos())
					continue
				}
				allow[fset.Position(c.Pos()).Line] = true
			}
		}
	}
	return allow, bare
}

// commentText strips the "//" or "/* … */" delimiters from a raw *ast.Comment.Text and trims
// surrounding whitespace, so callers can match directive text without caring which comment style
// was used.
func commentText(raw string) string {
	s := raw
	switch {
	case strings.HasPrefix(s, "//"):
		s = s[2:]
	case strings.HasPrefix(s, "/*"):
		s = strings.TrimSuffix(s[2:], "*/")
	}
	return strings.TrimSpace(s)
}
