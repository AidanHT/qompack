package symbols_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// corpusDir is testdata/corpora/symbols/, the seed corpus this subplan owns: one small file per
// dialect, reused verbatim as FuzzExtract's seed set (plans/V2-SP-04 §"Fixtures").
const corpusDir = "../../testdata/corpora/symbols"

// decl is the projection of a Symbol every table assertion compares on: identity (Name), the
// §5.22b Kind vocabulary, and the 1-based Line. Offset and Len are deliberately excluded — they
// shift under CRLF and under any whitespace edit to a fixture, so spans are asserted separately
// and by their *text*, which is what actually has to be right.
type decl struct {
	Name string
	Kind string
	Line int
}

// decls projects a []Symbol onto the comparable form go-cmp diffs.
func decls(got []symbols.Symbol) []decl {
	out := make([]decl, 0, len(got))
	for _, s := range got {
		out = append(out, decl{Name: s.Name, Kind: s.Kind, Line: s.Line})
	}
	return out
}

// loadCorpus reads a corpus fixture and normalizes CRLF to LF. .gitattributes marks
// testdata/corpora/** as -text so git never rewrites these bytes, but normalizing anyway keeps the
// line-number and span assertions below true even in a working tree that acquired CRLF some other
// way (an editor, an archive extraction). TestExtract_StableUnderCRLF re-introduces CRLF
// deliberately, so nothing here is hiding the CRLF path from test coverage.
func loadCorpus(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(corpusDir, name))
	require.NoError(t, err, "reading corpus fixture %s", name)
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// spanText returns the exact bytes s covers in b. Every span assertion in this file goes through
// it, so a span regression shows up as a readable text diff rather than as two integers.
func spanText(t *testing.T, b []byte, s symbols.Symbol) string {
	t.Helper()
	require.GreaterOrEqual(t, s.Offset, 0, "symbol %q: Offset must be non-negative", s.Name)
	require.Positive(t, s.Len, "symbol %q: Len must be positive", s.Name)
	require.LessOrEqual(t, s.Offset+s.Len, len(b), "symbol %q: span must stay inside the source", s.Name)
	return string(b[s.Offset : s.Offset+s.Len])
}

// find returns the single symbol named name, failing the test when it is absent or ambiguous.
func find(t *testing.T, got []symbols.Symbol, name string) symbols.Symbol {
	t.Helper()
	var out []symbols.Symbol
	for _, s := range got {
		if s.Name == name {
			out = append(out, s)
		}
	}
	require.Len(t, out, 1, "expected exactly one symbol named %q in %v", name, decls(got))
	return out[0]
}

// requireContainsSpan asserts outer's span strictly contains inner's — the nesting relation
// Enclosing's minimal-sufficient-span resolution (Qompack.md §8.7) depends on.
func requireContainsSpan(t *testing.T, outer, inner symbols.Symbol) {
	t.Helper()
	require.LessOrEqual(t, outer.Offset, inner.Offset, "%q must start at or before %q", outer.Name, inner.Name)
	require.GreaterOrEqual(t, outer.Offset+outer.Len, inner.Offset+inner.Len, "%q must end at or after %q", outer.Name, inner.Name)
	require.Greater(t, outer.Len, inner.Len, "%q's span must be strictly larger than %q's", outer.Name, inner.Name)
}

func TestExtract_Go(t *testing.T) {
	src := loadCorpus(t, "sample.go")
	got := symbols.New().Extract("sample.go", src)

	want := []decl{
		{Name: "Load", Kind: "func", Line: 12},
		{Name: "Normalize", Kind: "func", Line: 28},
		{Name: "Store", Kind: "type", Line: 44},
		{Name: "defaultName", Kind: "const", Line: 54},
		{Name: "maxKeys", Kind: "const", Line: 57},
		{Name: "prefix", Kind: "const", Line: 60},
	}
	require.Empty(t, cmp.Diff(want, decls(got)))

	// Brace-matched spans: each one starts at its declaration line and ends on the matching close
	// brace, receiver group and nested if-blocks included.
	load := spanText(t, src, find(t, got, "Load"))
	require.True(t, strings.HasPrefix(load, "func (s *Store) Load("), "Load span must start at the declaration line: %q", load)
	require.True(t, strings.HasSuffix(load, "\n}"), "Load span must end on its closing brace: %q", load)
	require.Contains(t, load, "return v, true")

	store := spanText(t, src, find(t, got, "Store"))
	require.Equal(t, "type Store struct {\n\tdata map[string]string\n\tname string\n}", store)

	// A const-block member's span is its own line only: no unquoted brace follows within four
	// lines, which is exactly why the fixture puts the block last.
	require.Equal(t, "\tdefaultName = \"unnamed\"", spanText(t, src, find(t, got, "defaultName")))
	require.Equal(t, "\tmaxKeys = 64", spanText(t, src, find(t, got, "maxKeys")))
	require.Equal(t, "\tprefix = \"gofixture:\"", spanText(t, src, find(t, got, "prefix")))
}

// nestedGoSource is byte-identical to symbolstest.nestedSpanSource. Duplicating it here keeps the
// nesting contract asserted inside the owning package too, so a regression fails with a targeted
// name before the conformance suite reports it.
const nestedGoSource = "package sample\n" +
	"\n" +
	"func Outer() int {\n" +
	"\ttype Inner struct {\n" +
	"\t\tX int\n" +
	"\t}\n" +
	"\tv := Inner{X: 1}\n" +
	"\treturn v.X\n" +
	"}\n"

func TestExtract_GoNestedType(t *testing.T) {
	src := []byte(nestedGoSource)
	got := symbols.New().Extract("sample.go", src)

	want := []decl{
		{Name: "Outer", Kind: "func", Line: 3},
		{Name: "Inner", Kind: "type", Line: 4},
	}
	require.Empty(t, cmp.Diff(want, decls(got)))

	outer, inner := find(t, got, "Outer"), find(t, got, "Inner")
	requireContainsSpan(t, outer, inner)
	require.Equal(t, "\ttype Inner struct {\n\t\tX int\n\t}", spanText(t, src, inner))
	require.True(t, strings.HasSuffix(spanText(t, src, outer), "\n}"))
}

func TestExtract_TSClassMethods(t *testing.T) {
	src := loadCorpus(t, "sample.ts")
	got := symbols.New().Extract("sample.ts", src)

	want := []decl{
		{Name: "Parser", Kind: "class", Line: 1},
		{Name: "constructor", Kind: "func", Line: 4},
		{Name: "parse", Kind: "func", Line: 8},
		{Name: "reset", Kind: "func", Line: 13},
		{Name: "build", Kind: "func", Line: 18},
	}
	require.Empty(t, cmp.Diff(want, decls(got)))

	parser := find(t, got, "Parser")
	for _, method := range []string{"constructor", "parse", "reset"} {
		requireContainsSpan(t, parser, find(t, got, method))
	}

	// The arrow-function const is a `func`, and its span is its own line: nothing brace-bearing
	// follows it.
	require.Equal(t, "export const build = (src: string) => new Parser(src);", spanText(t, src, find(t, got, "build")))
	// `private pos = 0;` is a field, not a method: the method rule requires a call-parenthesis.
	require.NotContains(t, decls(got), decl{Name: "pos", Kind: "func", Line: 2})
}

func TestExtract_TSKeywordDenySet(t *testing.T) {
	src := loadCorpus(t, "keywords.ts")
	got := symbols.New().Extract("keywords.ts", src)

	want := []decl{
		{Name: "Guard", Kind: "class", Line: 1},
		{Name: "check", Kind: "func", Line: 2},
	}
	require.Empty(t, cmp.Diff(want, decls(got)))

	// Every one of these is an indented `<word> (` line the class-method rule would otherwise
	// claim as a method declaration.
	for _, kw := range []string{"if", "for", "while", "switch", "return"} {
		for _, s := range got {
			require.NotEqual(t, kw, s.Name, "keyword %q must never be extracted as a method", kw)
		}
	}
}

func TestExtract_Python(t *testing.T) {
	src := loadCorpus(t, "sample.py")
	got := symbols.New().Extract("sample.py", src)

	want := []decl{
		{Name: "Alpha", Kind: "class", Line: 4},
		{Name: "load", Kind: "func", Line: 5},
		{Name: "store", Kind: "func", Line: 8},
		{Name: "CONST_LIMIT", Kind: "const", Line: 12},
		{Name: "lowercase_total", Kind: "var", Line: 13},
	}
	require.Empty(t, cmp.Diff(want, decls(got)))

	alpha := find(t, got, "Alpha")
	requireContainsSpan(t, alpha, find(t, got, "load"))
	requireContainsSpan(t, alpha, find(t, got, "store"))

	// Indent style: the class span stops at the first later line indented no further than the
	// class itself, which is the module-level constant.
	body := spanText(t, src, alpha)
	require.Contains(t, body, "def store(self, key, value):")
	require.NotContains(t, body, "CONST_LIMIT")
}

func TestExtract_Rust(t *testing.T) {
	src := loadCorpus(t, "sample.rs")
	got := symbols.New().Extract("sample.rs", src)

	want := []decl{
		{Name: "fetch", Kind: "func", Line: 3},
		{Name: "Client", Kind: "type", Line: 9},
		{Name: "Client", Kind: "type", Line: 14},
	}
	require.Empty(t, cmp.Diff(want, decls(got)))

	// The impl rule captures the implementing type, not the trait: "impl Fetcher for Client".
	require.Equal(t, "impl Fetcher for Client {}", spanText(t, src, got[2]))
}

func TestExtract_JVM(t *testing.T) {
	cases := map[string]struct {
		file string
		want []decl
	}{
		"java": {file: "Widget.java", want: []decl{
			{Name: "Widget", Kind: "class", Line: 1},
			{Name: "size", Kind: "func", Line: 2},
			{Name: "name", Kind: "func", Line: 6},
		}},
		"kotlin": {file: "sample.kt", want: []decl{
			{Name: "Store", Kind: "class", Line: 1},
			{Name: "save", Kind: "func", Line: 2},
			{Name: "load", Kind: "func", Line: 6},
		}},
		"csharp": {file: "sample.cs", want: []decl{
			{Name: "Runner", Kind: "class", Line: 1},
			{Name: "Run", Kind: "func", Line: 3},
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			src := loadCorpus(t, tc.file)
			got := symbols.New().Extract(tc.file, src)
			require.Empty(t, cmp.Diff(tc.want, decls(got)))
			requireContainsSpan(t, got[0], got[1])
		})
	}
}

func TestExtract_CFamily(t *testing.T) {
	src := loadCorpus(t, "sample.c")
	got := symbols.New().Extract("sample.c", src)

	want := []decl{
		{Name: "MAX_ITEMS", Kind: "const", Line: 3},
		{Name: "Node", Kind: "type", Line: 10},
		{Name: "node_sum", Kind: "func", Line: 14},
	}
	require.Empty(t, cmp.Diff(want, decls(got)))

	// #define has no block at all, so its span is the directive line.
	require.Equal(t, "#define MAX_ITEMS 16", spanText(t, src, find(t, got, "MAX_ITEMS")))
	// The typedef's span ends on the closing brace, not on the trailing alias.
	require.Equal(t, "typedef struct Node {\n    int value;\n}", spanText(t, src, find(t, got, "Node")))
	// An Allman-braced definition still resolves: the opening brace is on the next line, inside
	// the four-line lookahead window.
	require.Equal(t, "int node_sum(const struct Node *n)\n{\n    return n->value;\n}", spanText(t, src, find(t, got, "node_sum")))
}

func TestExtract_Ruby_EndInclusive(t *testing.T) {
	src := loadCorpus(t, "sample.rb")
	got := symbols.New().Extract("sample.rb", src)

	want := []decl{
		{Name: "Parser", Kind: "class", Line: 1},
		{Name: "MAX_DEPTH", Kind: "const", Line: 2},
		{Name: "parse", Kind: "func", Line: 4},
	}
	require.Empty(t, cmp.Diff(want, decls(got)))

	// The `end` that terminates the indent block belongs to the span it closes.
	require.Equal(t, "  def parse(input)\n    input.strip\n  end\n", spanText(t, src, find(t, got, "parse")))
	require.Equal(t, string(src), spanText(t, src, find(t, got, "Parser")))
}

func TestExtract_Shell(t *testing.T) {
	src := loadCorpus(t, "sample.sh")
	got := symbols.New().Extract("sample.sh", src)

	want := []decl{
		{Name: "build", Kind: "func", Line: 4},
		{Name: "build2", Kind: "func", Line: 8},
		{Name: "APP_NAME", Kind: "const", Line: 12},
	}
	require.Empty(t, cmp.Diff(want, decls(got)))

	require.Equal(t, "function build() {\n  echo \"building\"\n}", spanText(t, src, find(t, got, "build")))
	require.Equal(t, "build2() {\n  build\n}", spanText(t, src, find(t, got, "build2")))
	require.Equal(t, "APP_NAME=demo", spanText(t, src, find(t, got, "APP_NAME")))
}

func TestExtract_PHP(t *testing.T) {
	src := loadCorpus(t, "sample.php")
	got := symbols.New().Extract("sample.php", src)

	want := []decl{
		{Name: "Router", Kind: "class", Line: 3},
		{Name: "dispatch", Kind: "func", Line: 4},
		{Name: "VERSION", Kind: "const", Line: 8},
	}
	require.Empty(t, cmp.Diff(want, decls(got)))

	requireContainsSpan(t, find(t, got, "Router"), find(t, got, "dispatch"))
	require.Equal(t, "    const VERSION = '1.0';", spanText(t, src, find(t, got, "VERSION")))
}

func TestExtract_None(t *testing.T) {
	for _, name := range []string{"notes.md", "package.json"} {
		t.Run(name, func(t *testing.T) {
			got := symbols.New().Extract(name, loadCorpus(t, name))
			require.NotNil(t, got, "the none dialect must return an empty, non-nil slice")
			require.Empty(t, got)
		})
	}
}

func TestExtract_Generic(t *testing.T) {
	src := loadCorpus(t, "sample.zig")
	got := symbols.New().Extract("sample.zig", src)

	require.Empty(t, cmp.Diff([]decl{{Name: "main", Kind: "func", Line: 3}}, decls(got)))
	require.Equal(t, "fn main() {\n    helper();\n}", spanText(t, src, got[0]))
}

func TestExtract_BraceInStringIgnored(t *testing.T) {
	src := loadCorpus(t, "braces.go")
	got := symbols.New().Extract("braces.go", src)

	require.Equal(t, "func inString() string {\n\ts := \"}\"\n\treturn s + \"{\"\n}", spanText(t, src, find(t, got, "inString")))
	raw := spanText(t, src, find(t, got, "inRawString"))
	require.Contains(t, raw, "still inside the raw string {")
	require.True(t, strings.HasSuffix(raw, "\treturn s\n}"), "raw-string span must end on the real closing brace: %q", raw)
	require.Equal(t, "func inRuneLiteral() bool {\n\tc := '}'\n\treturn c == '{'\n}", spanText(t, src, find(t, got, "inRuneLiteral")))
}

func TestExtract_BraceInCommentIgnored(t *testing.T) {
	src := loadCorpus(t, "braces.go")
	got := symbols.New().Extract("braces.go", src)

	require.Equal(t, "func inLineComment() int {\n\t// }\n\treturn 1\n}", spanText(t, src, find(t, got, "inLineComment")))
	require.Equal(t, "func inBlockComment() int {\n\t/* } and { */\n\treturn 2\n}", spanText(t, src, find(t, got, "inBlockComment")))
}

// TestExtract_RustLifetimesAreNotCharLiterals pins the one place where treating `'` as a string
// opener would be catastrophic rather than merely wrong: `&'a str` is a lifetime, and a scanner
// that skipped from that apostrophe to the next one would run past the function's closing brace and
// hand every consumer a span covering the rest of the file.
func TestExtract_RustLifetimesAreNotCharLiterals(t *testing.T) {
	src := []byte("pub fn longest<'a>(x: &'a str, y: &'a str) -> &'a str {\n" +
		"    if x.len() > y.len() {\n" +
		"        x\n" +
		"    } else {\n" +
		"        y\n" +
		"    }\n" +
		"}\n" +
		"\n" +
		"pub struct Tail {\n" +
		"    v: u8,\n" +
		"}\n")
	got := symbols.New().Extract("lib.rs", src)

	require.Empty(t, cmp.Diff([]decl{
		{Name: "longest", Kind: "func", Line: 1},
		{Name: "Tail", Kind: "type", Line: 9},
	}, decls(got)))

	span := spanText(t, src, find(t, got, "longest"))
	require.Contains(t, span, "} else {")
	require.True(t, strings.HasSuffix(span, "\n}"), "span must end on longest's own closing brace: %q", span)
	require.NotContains(t, span, "pub struct Tail")
}

// TestExtract_CharLiteralBracesIgnored covers the escaped and unescaped character-literal forms a
// brace can hide in.
func TestExtract_CharLiteralBracesIgnored(t *testing.T) {
	src := []byte("package p\n\nfunc braceRunes() bool {\n" +
		"\topen := '{'\n" +
		"\tshut := '}'\n" +
		"\tquote := '\\''\n" +
		"\tfeed := '\\n'\n" +
		"\treturn open != shut && quote != feed\n" +
		"}\n")
	got := symbols.New().Extract("p.go", src)

	require.Len(t, got, 1)
	require.Equal(t, len(src)-1, got[0].Offset+got[0].Len, "the span must end on the final brace, not inside a rune literal")
}

// TestExtract_UnterminatedLexicalConstructs asserts that a source truncated mid-string,
// mid-comment or mid-raw-string still yields an in-bounds span running to EOF. Truncated captures
// are routine — tool output is size-capped long before it reaches symbols — so this is a normal
// input, not a pathological one.
func TestExtract_UnterminatedLexicalConstructs(t *testing.T) {
	cases := map[string]string{
		"string":        "package p\n\nfunc a() {\n\ts := \"never closed\n",
		"line comment":  "package p\n\nfunc a() {\n\t// never newline-terminated",
		"block comment": "package p\n\nfunc a() {\n\t/* never closed\n",
		"raw string":    "package p\n\nfunc a() {\n\ts := `never closed {\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			b := []byte(src)
			got := symbols.New().Extract("p.go", b)
			require.Len(t, got, 1)
			require.Equal(t, "a", got[0].Name)
			require.Equal(t, len(b), got[0].Offset+got[0].Len, "an unterminated construct must run to EOF, in bounds")
		})
	}
}

// TestExtract_ShellQuotedBraces covers the sqString half of the single-quote handling: in shell a
// `'` opens a string, so `echo '{'` must not open a block.
func TestExtract_ShellQuotedBraces(t *testing.T) {
	src := []byte("say() {\n  echo '{'\n  echo \"}\"\n}\n")
	got := symbols.New().Extract("q.sh", src)

	require.Len(t, got, 1)
	require.Equal(t, "say() {\n  echo '{'\n  echo \"}\"\n}", spanText(t, src, got[0]))
}

// TestExtract_RubyEndVariants pins both sides of the `end`-inclusion rule: a bare `end` and an
// `end` trailing a comment close the block they terminate, but `endpoint` — an ordinary assignment
// that merely starts with those three letters — does not.
func TestExtract_RubyEndVariants(t *testing.T) {
	src := []byte("def alpha\n  x = 1\nend # closes alpha\n\ndef beta\n  y = 2\nendpoint = 3\n")
	got := symbols.New().Extract("v.rb", src)

	require.Empty(t, cmp.Diff([]decl{
		{Name: "alpha", Kind: "func", Line: 1},
		{Name: "beta", Kind: "func", Line: 5},
	}, decls(got)))

	require.Equal(t, "def alpha\n  x = 1\nend # closes alpha\n", spanText(t, src, find(t, got, "alpha")))
	require.Equal(t, "def beta\n  y = 2\n", spanText(t, src, find(t, got, "beta")))
}

// TestExtract_TabIndentedPython covers the tab-as-four-columns rule of §5.22b's span computation.
func TestExtract_TabIndentedPython(t *testing.T) {
	src := []byte("class Tabbed:\n\tdef one(self):\n\t\treturn 1\n\nVALUE = 1\n")
	got := symbols.New().Extract("t.py", src)

	require.Empty(t, cmp.Diff([]decl{
		{Name: "Tabbed", Kind: "class", Line: 1},
		{Name: "one", Kind: "func", Line: 2},
		{Name: "VALUE", Kind: "const", Line: 5},
	}, decls(got)))
	requireContainsSpan(t, find(t, got, "Tabbed"), find(t, got, "one"))
	require.NotContains(t, spanText(t, src, find(t, got, "Tabbed")), "VALUE")
}

// TestExtract_GenericIndentFallback covers the generic dialect's second block style: with no brace
// in the lookahead window it falls back to indent, and a column-zero comment inside the body must
// not terminate the span.
func TestExtract_GenericIndentFallback(t *testing.T) {
	src := []byte("fn noBlock()\n    inner();\n// a column-zero comment must not end the span\n    more();\n    tail();\n")
	got := symbols.New().Extract("odd.zig", src)

	require.Empty(t, cmp.Diff([]decl{{Name: "noBlock", Kind: "func", Line: 1}}, decls(got)))
	require.Equal(t, string(src), spanText(t, src, got[0]))
}

// TestExtract_PathResolution pins that only the final path element's extension selects a dialect,
// on either separator, and that a file with no extension at all falls through to generic. symbols
// is handed paths from tool output and MCP requests, so a Windows path must resolve identically on
// a Linux daemon.
func TestExtract_PathResolution(t *testing.T) {
	goSrc := []byte("package p\n\nfunc Only() {\n}\n")
	ex := symbols.New()

	for _, path := range []string{`C:\src\app.v1\main.go`, "src/app.v1/main.go", "main.go"} {
		t.Run(path, func(t *testing.T) {
			require.Empty(t, cmp.Diff([]decl{{Name: "Only", Kind: "func", Line: 3}}, decls(ex.Extract(path, goSrc))))
		})
	}

	// No extension at all: generic, whose function rule still recognizes a braced definition.
	got := ex.Extract("scripts/release", []byte("function build() {\n  ship();\n}\n"))
	require.Empty(t, cmp.Diff([]decl{{Name: "build", Kind: "func", Line: 1}}, decls(got)))
}

func TestExtract_UnterminatedBlock(t *testing.T) {
	src := []byte("package p\n\nfunc a() {\n\tif true {\n")
	got := symbols.New().Extract("p.go", src)

	require.Len(t, got, 1)
	require.Equal(t, "a", got[0].Name)
	require.Equal(t, len(src), got[0].Offset+got[0].Len, "an unterminated block runs to EOF")
}

func TestExtract_SortOrder(t *testing.T) {
	got := symbols.New().Extract("sample.ts", loadCorpus(t, "sample.ts"))
	require.Greater(t, len(got), 1)

	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		require.LessOrEqual(t, prev.Offset, cur.Offset, "results must be sorted by Offset ascending")
		if prev.Offset == cur.Offset {
			require.GreaterOrEqual(t, prev.Len, cur.Len, "equal offsets must be sorted by Len descending")
		}
	}
	// The consequence the sort exists for: a class precedes every one of its methods.
	require.Equal(t, "Parser", got[0].Name)
	require.Equal(t, "constructor", got[1].Name)
}

func TestExtract_CapAtMaxSymbols(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("package big\n")
	for i := 0; i < 25000; i++ {
		sb.WriteString("const c = 1\n")
	}

	got := symbols.New().Extract("big.go", []byte(sb.String()))
	require.Len(t, got, symbols.MaxSymbols)
}

func TestExtract_TruncatesAt4MiB(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("package big\n\nfunc Early() {}\n")
	for sb.Len() < 5<<20 {
		sb.WriteString("// pad\n")
	}
	sb.WriteString("func Late() {}\n")
	src := []byte(sb.String())
	require.Greater(t, len(src), symbols.MaxExtractBytes)

	got := symbols.New().Extract("big.go", src)
	require.Empty(t, cmp.Diff([]decl{{Name: "Early", Kind: "func", Line: 3}}, decls(got)))
	for _, s := range got {
		require.Less(t, s.Offset, symbols.MaxExtractBytes, "no symbol may start beyond the 4 MiB cut")
	}
}

func TestExtract_LineNumbersAre1Based(t *testing.T) {
	src := []byte("func a() {}\n\n\n\nfunc b() {}\n\n\n\nfunc c() {}\n")
	got := symbols.New().Extract("x.go", src)

	require.Empty(t, cmp.Diff([]decl{
		{Name: "a", Kind: "func", Line: 1},
		{Name: "b", Kind: "func", Line: 5},
		{Name: "c", Kind: "func", Line: 9},
	}, decls(got)))
}

func TestExtract_StableUnderCRLF(t *testing.T) {
	for _, name := range []string{"sample.go", "sample.ts", "sample.py", "sample.rb", "sample.c"} {
		t.Run(name, func(t *testing.T) {
			lf := loadCorpus(t, name)
			crlf := bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
			ex := symbols.New()

			gotLF, gotCRLF := ex.Extract(name, lf), ex.Extract(name, crlf)
			require.Empty(t, cmp.Diff(decls(gotLF), decls(gotCRLF)),
				"Name, Kind and Line must be identical under CRLF")
			for _, s := range gotCRLF {
				require.Positive(t, s.Len)
				require.LessOrEqual(t, s.Offset+s.Len, len(crlf))
			}
		})
	}
}

func TestEnclosing_SmallestSpanWins(t *testing.T) {
	src := loadCorpus(t, "sample.ts")
	ex := symbols.New()

	off := bytes.Index(src, []byte("this.pos += 1;"))
	require.Positive(t, off)

	got, ok := ex.Enclosing("sample.ts", src, off)
	require.True(t, ok)
	require.Equal(t, "parse", got.Name, "the method, not the enclosing class, is the minimal sufficient span")
}

func TestEnclosing_OutsideAnySymbol(t *testing.T) {
	src := loadCorpus(t, "sample.go")
	off := bytes.Index(src, []byte("Package gofixture"))
	require.Positive(t, off)

	got, ok := symbols.New().Enclosing("sample.go", src, off)
	require.False(t, ok, "an offset in the top-of-file comment is inside no declaration")
	require.Equal(t, symbols.Symbol{}, got)
}

func TestEnclosing_OutOfRange(t *testing.T) {
	src := loadCorpus(t, "sample.go")
	ex := symbols.New()

	for name, off := range map[string]int{"negative": -1, "one past the end": len(src)} {
		t.Run(name, func(t *testing.T) {
			got, ok := ex.Enclosing("sample.go", src, off)
			require.False(t, ok)
			require.Equal(t, symbols.Symbol{}, got)
		})
	}
}

// propExtensions is the extension set TestPropSpansWellFormed samples from: every dialect,
// including `none` and the empty extension that resolves to `generic`.
var propExtensions = []string{
	".go", ".ts", ".jsx", ".py", ".rs", ".java", ".kt", ".cs", ".c", ".hpp",
	".rb", ".sh", ".ps1", ".php", ".md", ".json", ".zig", "",
}

// TestPropSpansWellFormed is the §5.22b bounds contract stated as a property: whatever bytes and
// whatever extension it is handed, Extract returns spans that are in range, non-empty and sorted.
// Every downstream consumer (store.Query.Symbol, dag's shared-symbol edges, the mcp span widener)
// slices the source with Offset and Len directly, so a violated bound here is an out-of-range
// panic three waves later.
func TestPropSpansWellFormed(t *testing.T) {
	ex := symbols.New()
	rapid.Check(t, func(rt *rapid.T) {
		ext := rapid.SampledFrom(propExtensions).Draw(rt, "ext")
		b := rapid.SliceOfN(rapid.Byte(), 0, 2000).Draw(rt, "src")

		got := ex.Extract("fixture"+ext, b)
		require.NotNil(rt, got, "Extract must never return a nil slice")
		require.LessOrEqual(rt, len(got), symbols.MaxSymbols)

		for i, s := range got {
			require.GreaterOrEqual(rt, s.Offset, 0, "symbol %d", i)
			require.Positive(rt, s.Len, "symbol %d", i)
			require.LessOrEqual(rt, s.Offset+s.Len, len(b), "symbol %d", i)
			require.GreaterOrEqual(rt, s.Line, 1, "symbol %d", i)
			require.Contains(rt, []string{"func", "type", "class", "const", "var"}, s.Kind, "symbol %d", i)
			if i > 0 {
				require.LessOrEqual(rt, got[i-1].Offset, s.Offset, "symbol %d", i)
			}
		}
	})
}
