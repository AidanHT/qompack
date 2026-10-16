package guards

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// V7 F4b: pluginmanifest.WithAsyncPrompt renders UserPromptSubmit async. R12 kept that hook
// synchronous because its output must reach the model in the same turn; the F4b ruling narrows R12
// for UserPromptSubmit only, and only while no reply producer is wired. In this build the reply's
// one producer is the thrash warning: observer.collectThrash returns at once while the observer's
// Grammar is nil, and the loop-warning switch that would govern a second producer has no reader.
// Once either changes, an async prompt hook would deliver that output a turn late, silently.
//
// This guard holds the "no reply producer is wired" half of the ruling. It compiles only while
// WithAsyncPrompt exists, so removing the transform is what retires it.
var _ = pluginmanifest.WithAsyncPrompt

// asyncPromptRuling is the remedy every failure below names.
const asyncPromptRuling = "pluginmanifest.WithAsyncPrompt (V7 F4b) renders UserPromptSubmit async " +
	"on the ruling that R12's same-turn output requirement has nothing to deliver while no prompt " +
	"reply producer is wired. Wiring one makes its output arrive a turn late without notice. Either " +
	"retire WithAsyncPrompt and `devtool bundle --async-prompt` (and this guard) in the same change, " +
	"or reopen R12 with the owner first"

// grammarCarriers are the product struct types with a grammar.Sequitur field, by package-qualified
// name. The scan derives the set and the guard requires it to equal this list, so a new carrier of
// the grammar is a decision made here, not a route around the guard.
var grammarCarriers = []string{
	"checkpoint.SourceSet",
	"daemon.Options",
	"daemon.Services",
	"observer.Options",
}

// grammarWrite is one keyed `Grammar:` element in a composite literal of a carrier type, or one
// assignment to a selector named Grammar, in product code.
type grammarWrite struct {
	file  string // module-relative, slash-separated
	typ   string // the carrier's package-qualified name; empty for an assignment
	value string // the assigned expression, as types.ExprString spells it
}

// unknownCarrier is a grammarWrite's typ for a keyed Grammar element in a literal whose elided
// element type the scan cannot infer. No allowed write has it, so such a write always fails.
const unknownCarrier = "(elided type)"

func (w grammarWrite) String() string {
	if w.typ == "" {
		return w.file + ": assignment .Grammar = " + w.value
	}
	return w.file + ": " + w.typ + "{Grammar: " + w.value + "}"
}

// allowedGrammarWrites are the only grammar writes product code may make: the daemon's two
// pass-throughs of daemon.Options.Grammar, which no composition root sets (so both pass nil to the
// observer), and the checkpointer's own grammar in its SourceSet, which is not the observer's.
var allowedGrammarWrites = map[grammarWrite]string{
	{file: "internal/daemon/daemon.go", typ: "daemon.Services", value: "o.Grammar"}: "pass-through of " +
		"daemon.Options.Grammar into Services",
	{file: "internal/daemon/observer_ops.go", typ: "observer.Options", value: "o.Grammar"}: "pass-through " +
		"of daemon.Options.Grammar into the observer",
	{file: "internal/cli/scheduler_wiring.go", typ: "checkpoint.SourceSet", value: "gram"}: "the " +
		"checkpointer's own grammar (opts.Grammar, else grammar.New()); the observer never sees it",
}

// TestGuard_NoPromptReplyProducerWhileAsyncPromptExists fails if any non-test composition root
// gives the observer a Grammar, if daemon.NewOptions starts returning one, or if the loop-warning
// switch gains a reader, while WithAsyncPrompt exists.
func TestGuard_NoPromptReplyProducerWhileAsyncPromptExists(t *testing.T) {
	root := repoRoot(t)
	scan := scanGrammarWiring(t, root)
	require.GreaterOrEqual(t, scan.files, minProductFiles, "the scan parsed too few product files to prove anything")

	require.Equal(t, grammarCarriers, scan.carriers,
		"the product types with a grammar.Sequitur field changed. A new carrier is a new route to the "+
			"observer's Grammar: add it to grammarCarriers only once its writes are covered here. "+
			asyncPromptRuling)

	var unlisted, stale []string
	seen := map[grammarWrite]bool{}
	for _, w := range scan.writes {
		seen[w] = true
		if _, ok := allowedGrammarWrites[w]; !ok {
			unlisted = append(unlisted, w.String())
		}
	}
	for w := range allowedGrammarWrites {
		if !seen[w] {
			stale = append(stale, w.String())
		}
	}
	sort.Strings(unlisted)
	sort.Strings(stale)
	require.Empty(t, unlisted, "these product writes set a Grammar outside the two daemon "+
		"pass-throughs and the checkpointer's SourceSet; a Grammar on the observer wires the thrash "+
		"warning, a UserPromptSubmit reply producer. "+asyncPromptRuling)
	require.Empty(t, stale, "these allowed grammar writes no longer exist; delete the row in the same "+
		"change so the list stays exact")

	require.Empty(t, scan.loopWarningReads, "product code now reads "+
		"runtime.selection.loopWarningsEnabled, the switch for state-aware loop warnings, which "+
		"are a prompt reply producer. "+asyncPromptRuling)

	opts := daemon.NewOptions(t.TempDir(), config.Defaults())
	require.Nil(t, opts.Grammar, "daemon.NewOptions now sets a Grammar, which the daemon passes "+
		"to the observer. "+asyncPromptRuling)

	var found bool
	for _, c := range daemon.ReloadKeyClasses() {
		if c.Prefix == "runtime.selection.loopWarningsEnabled" {
			found = true
			require.Equal(t, daemon.ReloadNoEffect, c.Effect, "the loopWarningsEnabled reload row is "+
				"no longer \"no effect in this build\", so something reads it. "+asyncPromptRuling)
		}
	}
	require.True(t, found, "daemon.ReloadKeyClasses has no runtime.selection.loopWarningsEnabled "+
		"row; the guard cannot show the switch is inert. "+asyncPromptRuling)
}

// TestGuard_GrammarWiringScannerSeesEveryShape is the scan's own self-test: a carrier declared in
// the file, keyed literals of a local and of an imported carrier (also behind &, and through an
// aliased import), literals whose element type is elided (inferred from the enclosing slice or
// map, else recorded as unknown), an assignment, a loop-warning read, and the shapes it must
// ignore — a non-carrier's Grammar key, elided or not, and a read of a Grammar field.
func TestGuard_GrammarWiringScannerSeesEveryShape(t *testing.T) {
	const src = `package daemon

import (
	"github.com/qompack/qompack/internal/grammar"
	obsv "github.com/qompack/qompack/internal/observer"
)

type Options struct {
	Grammar grammar.Sequitur
}

type Layout struct{ Grammar string }

type Many []Options

func wire(g grammar.Sequitur, cfg C) {
	_ = Options{Grammar: g}
	_ = &observer.Options{Grammar: grammar.New()}
	_ = obsv.Options{Grammar: g}
	_ = []observer.Options{{Grammar: grammar.New()}}
	_ = map[string]*Options{"a": {Grammar: g}}
	_ = [][]obsv.Options{{{Grammar: g}}}
	_ = Many{{Grammar: g}}
	_ = Layout{Grammar: "grammar"}
	_ = []Layout{{Grammar: "grammar"}}
	var o Options
	o.Grammar = g
	_ = o.Grammar
	if cfg.Runtime.Selection.LoopWarningsEnabled {
	}
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "sample.go", src, parser.SkipObjectResolution)
	require.NoError(t, err)
	carriers := map[string]bool{}
	grammarCarriersInFile(f, carriers)
	require.Equal(t, map[string]bool{"daemon.Options": true}, carriers,
		"a struct with a grammar.Sequitur field is a carrier; one with a string Grammar is not")

	known := map[string]bool{"daemon.Options": true, "observer.Options": true}
	writes, reads := grammarWritesInFile(f, "sample.go", known)
	require.Equal(t, []grammarWrite{
		{file: "sample.go", typ: "daemon.Options", value: "g"},
		{file: "sample.go", typ: "observer.Options", value: "grammar.New()"},
		{file: "sample.go", typ: "observer.Options", value: "g"},
		{file: "sample.go", typ: "observer.Options", value: "grammar.New()"},
		{file: "sample.go", typ: "daemon.Options", value: "g"},
		{file: "sample.go", typ: "observer.Options", value: "g"},
		{file: "sample.go", typ: unknownCarrier, value: "g"},
		{file: "sample.go", value: "g"},
	}, writes)
	require.Equal(t, []string{"sample.go"}, reads)
}

// grammarScan is what scanGrammarWiring found.
type grammarScan struct {
	files            int
	carriers         []string // sorted
	writes           []grammarWrite
	loopWarningReads []string // module-relative files
}

// scanGrammarWiring parses every non-test Go file under productReadRoots twice over its ASTs: once
// for the carrier types, then for the writes into them and the loop-warning reads.
func scanGrammarWiring(t *testing.T, root string) grammarScan {
	t.Helper()
	type parsed struct {
		rel string
		f   *ast.File
	}
	var all []parsed
	for _, top := range productReadRoots {
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				return rerr
			}
			f, perr := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
			if perr != nil {
				return perr
			}
			all = append(all, parsed{rel: filepath.ToSlash(rel), f: f})
			return nil
		})
		require.NoError(t, err, "scanning %s", top)
	}

	carriers := map[string]bool{}
	for _, p := range all {
		grammarCarriersInFile(p.f, carriers)
	}
	out := grammarScan{files: len(all)}
	for c := range carriers {
		out.carriers = append(out.carriers, c)
	}
	sort.Strings(out.carriers)
	for _, p := range all {
		w, r := grammarWritesInFile(p.f, p.rel, carriers)
		out.writes = append(out.writes, w...)
		out.loopWarningReads = append(out.loopWarningReads, r...)
	}
	return out
}

// grammarCarriersInFile adds to into the package-qualified name of every struct type declared in f
// with a field of type grammar.Sequitur.
func grammarCarriersInFile(f *ast.File, into map[string]bool) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				if types.ExprString(field.Type) == "grammar.Sequitur" {
					into[f.Name.Name+"."+ts.Name.Name] = true
				}
			}
		}
	}
}

// grammarWritesInFile returns, in source order, every keyed Grammar element of a composite literal
// whose type is a carrier and every assignment to a selector named Grammar, with the file once in
// the reads list for each selector named LoopWarningsEnabled. A literal with an elided type takes
// the element type of the slice, array or map literal around it; one whose type cannot be inferred
// that way is recorded as unknownCarrier.
func grammarWritesInFile(f *ast.File, rel string, carriers map[string]bool) ([]grammarWrite, []string) {
	imports := importNames(f)
	var writes []grammarWrite
	var reads []string
	// elided maps a literal with no Type to the type its enclosing literal gives its elements; nil
	// when the enclosing literal's type is not a slice, array or map spelled in place.
	elided := map[*ast.CompositeLit]ast.Expr{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CompositeLit:
			litType := x.Type
			if litType == nil {
				litType = elided[x]
			}
			markElidedElements(x, litType, elided)
			typ := ""
			if litType != nil {
				typ = qualifiedTypeName(f, imports, litType)
			} else {
				typ = unknownCarrier
			}
			if typ != unknownCarrier && !carriers[typ] {
				return true
			}
			for _, elt := range x.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Grammar" {
					writes = append(writes, grammarWrite{file: rel, typ: typ, value: types.ExprString(kv.Value)})
				}
			}
		case *ast.AssignStmt:
			for i, lhs := range x.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Grammar" {
					continue
				}
				value := ""
				if len(x.Rhs) == len(x.Lhs) {
					value = types.ExprString(x.Rhs[i])
				} else if len(x.Rhs) == 1 {
					value = types.ExprString(x.Rhs[0])
				}
				writes = append(writes, grammarWrite{file: rel, value: value})
			}
		case *ast.SelectorExpr:
			if x.Sel.Name == "LoopWarningsEnabled" {
				reads = append(reads, rel)
			}
		}
		return true
	})
	return writes, reads
}

// markElidedElements records, for each element (and map key) of lit that is a literal with an
// elided type, the element type litType gives it: a slice or array's element, a map's key or
// value, with a leading * dropped since Go elides &T{} to {} there. litType of any other shape
// (a named slice type, an unknown) gives nothing, so those elements stay unknown.
func markElidedElements(lit *ast.CompositeLit, litType ast.Expr, elided map[*ast.CompositeLit]ast.Expr) {
	var keyType, eltType ast.Expr
	switch t := litType.(type) {
	case *ast.ArrayType:
		eltType = t.Elt
	case *ast.MapType:
		keyType, eltType = t.Key, t.Value
	}
	deref := func(e ast.Expr) ast.Expr {
		if s, ok := e.(*ast.StarExpr); ok {
			return s.X
		}
		return e
	}
	mark := func(e, typ ast.Expr) {
		if cl, ok := e.(*ast.CompositeLit); ok && cl.Type == nil {
			if typ != nil {
				elided[cl] = deref(typ)
			} else {
				elided[cl] = nil
			}
		}
	}
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			mark(kv.Key, keyType)
			mark(kv.Value, eltType)
			continue
		}
		mark(elt, eltType)
	}
}

// importNames maps each name f uses for an imported package to the last element of its import
// path, so an aliased import (obsv ".../internal/observer") resolves to the package it names.
// Blank and dot imports bind no qualifier and are left out.
func importNames(f *ast.File) map[string]string {
	names := map[string]string{}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		base := path.Base(p)
		local := base
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if local == "_" || local == "." {
			continue
		}
		names[local] = base
	}
	return names
}

// qualifiedTypeName spells a composite literal's type as "pkg.Name": an imported type by the
// package its qualifier names (through imports, so an alias resolves; a qualifier with no import
// is taken as written), a local one by f's package name. Any other shape (a slice, a map) is not a
// carrier and returns "".
func qualifiedTypeName(f *ast.File, imports map[string]string, expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return f.Name.Name + "." + x.Name
	case *ast.SelectorExpr:
		if pkg, ok := x.X.(*ast.Ident); ok {
			if name, ok := imports[pkg.Name]; ok {
				return name + "." + x.Sel.Name
			}
			return pkg.Name + "." + x.Sel.Name
		}
	}
	return ""
}
