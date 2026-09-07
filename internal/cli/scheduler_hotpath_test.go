package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSchedulerNotOnHotPath is the plan's budget B-A guard (00-ARCHITECTURE §7, Qompack.md §11.3
// "hook p99 latency < 15 ms"): by call-graph inspection of internal/cli, no hook subcommand path
// reaches scheduler.Evaluate, the runtime's Evaluate, or the daemon's scheduler wiring
// (WrapServicesForScheduler and its siblings); only `qompack daemon` — runDaemon, through
// wireScheduler / registerSchedulerIdle / closeScheduler in scheduler_wiring.go — may.
//
// The graph is a go/ast reachability walk over the package's non-test files, by name: an edge is
// any call of, or reference to, a top-level function of this package, and a method call x.M()
// is an edge to every method named M in the package. That is a deliberate over-approximation —
// it can only add paths, never hide one — so a pass here is conservative. The roots are read off
// the Cmd literals that carry Hook: true, so a hook added later is guarded without editing this
// test, and the same walk proves it can see the wiring at all by finding it under runDaemon.
func TestSchedulerNotOnHotPath(t *testing.T) {
	g := parseCLIGraph(t)

	roots := g.hookRoots()
	require.NotEmpty(t, roots, "no Cmd{Hook: true} literal found; the hook table moved")
	t.Logf("hook roots: %v", roots)
	require.NotContains(t, roots, "runDaemon", "`qompack daemon` is not a hook subcommand")

	// Positive control: the walk sees the wiring under runDaemon, so the negative below is a
	// finding and not blindness.
	daemonReach, _ := g.reachable([]string{"runDaemon"})
	for _, fn := range []string{"wireScheduler", "registerSchedulerIdle", "closeScheduler"} {
		require.Contains(t, daemonReach, fn, "runDaemon must reach %s (scheduler_wiring.go)", fn)
	}
	wiring := g.externalsOf(daemonReach)
	for _, sym := range []string{
		daemonPkg + ".NewSchedulerRuntime", daemonPkg + ".WrapServicesForScheduler",
		daemonPkg + ".RegisterSchedulerIdleWork", daemonPkg + ".CloseSchedulerRuntime",
	} {
		require.Contains(t, wiring, sym, "runDaemon's closure must reference %s", sym)
	}

	// The guard: nothing a hook runs reaches the scheduler.
	reach, parent := g.reachable(roots)
	require.NotEmpty(t, reach)
	require.Contains(t, reach, "resolveProjectRoot", "sanity: every hook resolves a project root")

	forbiddenFuncs := []string{"runDaemon", "wireScheduler", "registerSchedulerIdle", "closeScheduler"}
	for _, fn := range forbiddenFuncs {
		require.NotContains(t, reach, fn, "hook path reaches %s: %s", fn, g.pathTo(parent, fn))
	}
	forbiddenSyms := map[string]bool{
		schedulerPkg + ".Evaluate":               true,
		daemonPkg + ".WrapServicesForScheduler":  true,
		daemonPkg + ".NewSchedulerRuntime":       true,
		daemonPkg + ".RegisterSchedulerIdleWork": true,
		daemonPkg + ".CloseSchedulerRuntime":     true,
	}
	for _, fn := range sortedKeys(reach) {
		for sym := range g.externals[fn] {
			require.False(t, forbiddenSyms[sym], "hook path references %s in %s: %s", sym, fn, g.pathTo(parent, fn))
			require.False(t, strings.HasPrefix(sym, schedulerPkg+"."),
				"hook path references the scheduler package (%s) in %s: %s", sym, fn, g.pathTo(parent, fn))
		}
		require.False(t, g.methodCalls[fn]["Evaluate"],
			"hook path calls an Evaluate method in %s: %s", fn, g.pathTo(parent, fn))
	}
	t.Logf("%d functions reachable from the hook roots, none on the scheduler", len(reach))
}

const (
	schedulerPkg = "github.com/qompack/qompack/internal/scheduler"
	daemonPkg    = "github.com/qompack/qompack/internal/daemon"
	cmdTypeName  = "Cmd"
)

// cliGraph is the by-name call graph of package cli's non-test files.
type cliGraph struct {
	funcs       map[string]bool            // top-level function names
	methods     map[string][]string        // method name → "T.M" keys
	edges       map[string]map[string]bool // caller key → callee key
	externals   map[string]map[string]bool // caller key → "importpath.Name" referenced
	methodCalls map[string]map[string]bool // caller key → method names called on any receiver
	hooks       []string                   // Run targets of every Cmd{Hook: true} literal
}

// parseCLIGraph parses the package directory (the test's working directory) without its tests.
func parseCLIGraph(t *testing.T) *cliGraph {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)
	require.Len(t, pkgs, 1, "expected exactly one non-test package in internal/cli")

	g := &cliGraph{
		funcs:       map[string]bool{},
		methods:     map[string][]string{},
		edges:       map[string]map[string]bool{},
		externals:   map[string]map[string]bool{},
		methodCalls: map[string]map[string]bool{},
	}
	var files []*ast.File
	for _, p := range pkgs {
		for _, f := range p.Files {
			files = append(files, f)
		}
	}
	// Pass 1: declarations.
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			key := declKey(fd)
			if fd.Recv == nil {
				g.funcs[fd.Name.Name] = true
			} else {
				g.methods[fd.Name.Name] = append(g.methods[fd.Name.Name], key)
			}
		}
	}
	// Pass 2: edges and roots.
	for _, f := range files {
		imports := importAliases(f)
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			g.walk(declKey(fd), fd.Body, imports)
		}
		g.collectHooks(f)
	}
	return g
}

// declKey names a declaration: "f" for a function, "T.M" for a method on T or *T.
func declKey(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	return typeName(fd.Recv.List[0].Type) + "." + fd.Name.Name
}

func typeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return typeName(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return typeName(x.X)
	case *ast.IndexListExpr:
		return typeName(x.X)
	default:
		return fmt.Sprintf("%T", e)
	}
}

// importAliases maps each import's local name to its path.
func importAliases(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, im := range f.Imports {
		p := strings.Trim(im.Path.Value, `"`)
		name := path.Base(p)
		if im.Name != nil {
			name = im.Name.Name
		}
		out[name] = p
	}
	return out
}

func (g *cliGraph) add(m map[string]map[string]bool, from, to string) {
	if m[from] == nil {
		m[from] = map[string]bool{}
	}
	m[from][to] = true
}

// walk records every edge out of one function body, nested closures included.
func (g *cliGraph) walk(from string, body ast.Node, imports map[string]string) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if g.funcs[x.Name] {
				g.add(g.edges, from, x.Name)
			}
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok {
				if p, isImport := imports[id.Name]; isImport {
					g.add(g.externals, from, p+"."+x.Sel.Name)
					return true
				}
			}
			for _, key := range g.methods[x.Sel.Name] {
				g.add(g.edges, from, key)
			}
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); !ok || imports[id.Name] == "" {
					g.add(g.methodCalls, from, sel.Sel.Name)
				}
			}
		}
		return true
	})
}

// collectHooks reads the Run target of every Cmd{Hook: true} composite literal in f.
func (g *cliGraph) collectHooks(f *ast.File) {
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		// A Cmd literal names its type (Cmd{...}) or elides it as an element of []Cmd{...};
		// either way the Hook and Run keys below identify it.
		if lit.Type != nil {
			if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != cmdTypeName {
				return true
			}
		}
		hook := false
		var run ast.Expr
		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "Hook":
				if v, ok := kv.Value.(*ast.Ident); ok && v.Name == "true" {
					hook = true
				}
			case "Run":
				run = kv.Value
			}
		}
		if !hook || run == nil {
			return true
		}
		switch r := run.(type) {
		case *ast.Ident:
			g.hooks = append(g.hooks, r.Name)
		case *ast.CallExpr:
			if id, ok := r.Fun.(*ast.Ident); ok {
				g.hooks = append(g.hooks, id.Name)
			}
		}
		return true
	})
}

// hookRoots returns the distinct hook Run targets, sorted.
func (g *cliGraph) hookRoots() []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range g.hooks {
		if g.funcs[h] && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}

// reachable is the transitive closure of edges from roots, with the BFS parent of each node for
// path reporting.
func (g *cliGraph) reachable(roots []string) (map[string]bool, map[string]string) {
	seen := map[string]bool{}
	parent := map[string]string{}
	queue := append([]string(nil), roots...)
	for _, r := range roots {
		seen[r] = true
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range sortedKeys(g.edges[cur]) {
			if !seen[next] {
				seen[next] = true
				parent[next] = cur
				queue = append(queue, next)
			}
		}
	}
	return seen, parent
}

// externalsOf unions the external references of a set of functions.
func (g *cliGraph) externalsOf(fns map[string]bool) map[string]bool {
	out := map[string]bool{}
	for fn := range fns {
		for sym := range g.externals[fn] {
			out[sym] = true
		}
	}
	return out
}

// pathTo renders the BFS chain that reached fn.
func (g *cliGraph) pathTo(parent map[string]string, fn string) string {
	var chain []string
	for cur := fn; cur != ""; cur = parent[cur] {
		chain = append([]string{cur}, chain...)
	}
	return strings.Join(chain, " → ")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
