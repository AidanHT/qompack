package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
)

// Audit 2's #80, the fixture half (wave 22 cliwork, fix round 2): useHangGuardedStatusDials took
// the status rows' own dials off the product's 250 ms connect budget, but two of those rows still
// needed a real connect inside 250 ms before they reached anything they prove: bootstrapDaemon's
// readiness probe dialled with that budget. A host on which every connect takes longer failed them
// with "never became reachable" for a daemon that was up. The same was true of every other
// readiness wait in this package, at 250 ms or at 50 ms.
//
// A readiness wait already polls inside require.Eventually under its own bound, so its verdict is
// "the listener came up", and the per-dial budget is only a hang guard (D61(c)). The rows below pin
// that: the fixture's probe dials with the hang guard, and no wait in this package dials around it.

// TestFixtureReadiness_BootstrapDaemonProbesWithTheHangGuard starts a real daemon through
// bootstrapDaemon and records the budget its readiness wait dials with. It must be
// bootstrapCallDeadline, the bound every admin round trip in these fixtures already uses, not a
// connect budget a slow host can miss.
func TestFixtureReadiness_BootstrapDaemonProbesWithTheHangGuard(t *testing.T) {
	// Not parallel: it replaces the package-level fixtureProbe and starts a daemon.
	var (
		mu      sync.Mutex
		budgets []time.Duration
	)
	prev := fixtureProbe
	fixtureProbe = func(a ipc.Addr, budget time.Duration) bool {
		mu.Lock()
		budgets = append(budgets, budget)
		mu.Unlock()
		return prev(a, budget)
	}
	t.Cleanup(func() { fixtureProbe = prev })

	stop := bootstrapDaemon(t, bootstrapProject(t))
	stop()

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, budgets, "bootstrapDaemon's readiness wait must dial through fixtureProbe")
	for i, b := range budgets {
		require.Equal(t, bootstrapCallDeadline, b,
			"readiness dial %d used %s: a host whose connects take longer reads a live daemon as never reachable", i, b)
	}
}

// TestFixtureReadinessWaits_GoThroughDaemonReachable sweeps this package's test files for a
// require.Eventually (or assert.Eventually) whose condition dials ipc.Probe or fixtureProbe itself
// instead of calling daemonReachable. Each such wait picks its own connect budget, which is the
// class #80 names; daemonReachable is the one place that budget is the hang guard.
func TestFixtureReadinessWaits_GoThroughDaemonReachable(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*_test.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	fset := token.NewFileSet()
	var offenders []string
	for _, name := range files {
		src, readErr := os.ReadFile(name)
		require.NoError(t, readErr, "reading %s", name)
		f, parseErr := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, parseErr, "parsing %s", name)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !strings.HasPrefix(sel.Sel.Name, "Eventually") {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.FuncLit)
				if !ok {
					continue
				}
				ast.Inspect(lit.Body, func(m ast.Node) bool {
					inner, ok := m.(*ast.CallExpr)
					if ok && dialsAroundDaemonReachable(inner.Fun) {
						offenders = append(offenders, fset.Position(inner.Pos()).String())
					}
					return true
				})
			}
			return true
		})
	}
	require.Empty(t, offenders,
		"these readiness waits dial with a budget of their own; call daemonReachable instead")
}

// dialsAroundDaemonReachable reports whether fun is ipc.Probe or fixtureProbe.
func dialsAroundDaemonReachable(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		x, ok := f.X.(*ast.Ident)
		return ok && x.Name == "ipc" && f.Sel.Name == "Probe"
	case *ast.Ident:
		return f.Name == "fixtureProbe"
	}
	return false
}
