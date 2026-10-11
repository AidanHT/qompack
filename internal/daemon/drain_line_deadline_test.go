package daemon

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
)

// The drain hands every line it dispatches its own drainLineDeadline (5 s, drain.go dispatchPending),
// and a line that runs out of it is left unpublished for a later pass, as it must be. That is a
// wall-clock limit on a dispatch that does real I/O (the capture, the store, the session's records),
// so in a row whose assertions are about WHAT a pass publishes, holds, retires or remembers, a host
// stalled for more than 5 s inside one delivery turned a correct product into a red row: the line was
// cut, nothing was published, and the row failed on 'fixture: ... published ...' or on 'context
// deadline exceeded' at the drain call (audit 2 #64; the full internal/daemon run where a 0.6 s row
// took 11.73 s; TestRequestedDrainPass_HoldsTheCaptureGateBetweenDeliveries on the wave 22 Linux gate).
//
// So every drainer a test builds over the daemon's own dispatch says which it is:
//
//   - contentDrainConfig: the row's subject is what the pass does. Each line is dispatched without
//     the drain's deadline (withoutLineDeadline) and keeps every cancellation, so a pass the row or
//     Stop ends still ends the line it is in.
//   - budgetDrainConfig: the row's subject is a pass budget. A lifted deadline could stand in for the
//     budget (a product that put the budget on the line as a deadline is exactly what such a row must
//     catch), so the row fails on any line that reaches the dispatch with a deadline earlier than the
//     drain's own drainLineDeadline could have set; past that check the line runs without the drain's
//     deadline, as a content row's does. Keeping the product's dispatch as it is instead made such a
//     row need every healthy handler call to finish inside 5 s: one the co-loaded night host held
//     past it cut the line that was the pass's progress, and the row failed on 'fixture: the pass made
//     its progress (err context deadline exceeded)' after 20.79 s against 0.5 s alone.
//   - lineDeadlineDrainConfig: the row keeps the product's dispatch as it is, because a line's own
//     deadline is part of what it pins (a line only that deadline ends, the settle's deadline a replay
//     carries, a slow part timed against it), or because it never needs a line published on its
//     first attempt.
//
// TestDrainRows_EveryDrainerChoosesWhetherItsLinesKeepTheirDeadline holds every row to that choice.

// withoutLineDeadline is dispatch with every deadline on the line's context lifted (the drain's own
// drainLineDeadline, and any deadline a caller above it set), keeping the context's values (the
// observation and the replayed delivery the drain attaches) and every cancellation: when the line's
// context is cancelled for any cause but a deadline (a settle's cut, a test's cancel, Stop), the
// dispatch's context is cancelled with that cause.
func withoutLineDeadline(dispatch func(context.Context, ipc.Request) ipc.Response) func(context.Context, ipc.Request) ipc.Response {
	return func(ctx context.Context, req ipc.Request) ipc.Response {
		lifted, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
		defer cancel(nil)
		stop := context.AfterFunc(ctx, func() {
			if cause := context.Cause(ctx); !errors.Is(cause, context.DeadlineExceeded) {
				cancel(cause)
			}
		})
		defer stop()
		return dispatch(lifted, req)
	}
}

// contentDrainConfig is dd.drainConfig() for a row whose subject is what a pass publishes, holds,
// retires or remembers, never how fast: its lines run without the drain's deadline
// (withoutLineDeadline). A pass budget the row sets (withPassBudget) still applies to the pass.
func contentDrainConfig(dd *daemon) DrainConfig {
	cfg := dd.drainConfig()
	cfg.Dispatch = withoutLineDeadline(cfg.Dispatch)
	return cfg
}

// lineDeadlineDrainConfig is dd.drainConfig() as Run installs it, for a row that keeps each line's own
// deadline.
func lineDeadlineDrainConfig(dd *daemon) DrainConfig {
	return dd.drainConfig()
}

// TestWithoutLineDeadline_LiftsDeadlinesAndKeepsValuesAndCancellations pins what a content row's
// dispatch is handed: no deadline, even once the line's own has passed; the line's values; and the
// line's cancellation, with its cause, whatever else ended it.
func TestWithoutLineDeadline_LiftsDeadlinesAndKeepsValuesAndCancellations(t *testing.T) {
	type key struct{}
	base := context.WithValue(context.Background(), key{}, "the line's value")

	t.Run("a deadline that has passed is lifted", func(t *testing.T) {
		line, cancel := context.WithDeadline(base, time.Now().Add(-time.Second))
		defer cancel()
		require.ErrorIs(t, line.Err(), context.DeadlineExceeded, "fixture: the line's deadline has passed")
		var dispatched bool
		withoutLineDeadline(func(ctx context.Context, _ ipc.Request) ipc.Response {
			dispatched = true
			_, has := ctx.Deadline()
			require.False(t, has, "the dispatch has no deadline")
			require.NoError(t, ctx.Err(), "the line's passed deadline does not end the dispatch")
			require.Equal(t, "the line's value", ctx.Value(key{}), "the line's values are kept")
			return ipc.Response{OK: true}
		})(line, ipc.Request{})
		require.True(t, dispatched)
	})

	t.Run("a cancellation ends the dispatch with its cause", func(t *testing.T) {
		cut := errors.New("the row's cut")
		parent, cancelParent := context.WithCancelCause(base)
		line, cancel := context.WithTimeout(parent, drainLineDeadline)
		defer cancel()
		cancelParent(cut)
		var dispatched bool
		withoutLineDeadline(func(ctx context.Context, _ ipc.Request) ipc.Response {
			dispatched = true
			<-ctx.Done() // a hang here is left to go test -timeout
			require.ErrorIs(t, context.Cause(ctx), cut, "the dispatch ends for the line's own cause")
			require.Equal(t, "the line's value", ctx.Value(key{}))
			return ipc.Response{Err: ctx.Err().Error()}
		})(line, ipc.Request{})
		require.True(t, dispatched)
	})
}

// budgetDrainConfig is dd.drainConfig() for a row whose subject is a pass budget (withPassBudget) and
// whose passes run under no deadline the row sets. Its lines are dispatched through budgetDispatch.
func budgetDrainConfig(t testing.TB, dd *daemon) DrainConfig {
	cfg := dd.drainConfig()
	cfg.Dispatch = budgetDispatch(t, cfg.Dispatch)
	return cfg
}

// budgetDispatch is dispatch for a budget row's drainer, built before the row's first pass. It checks
// every line first: the drain hands a line a context whose deadline is its own drainLineDeadline,
// counted from the dispatch, so no earlier than drainLineDeadline after budgetDispatch was called, and
// a line whose context ends sooner, as one would if the drain put the pass's budget on its lines as a
// deadline, fails the row. The line is then dispatched without the drain's deadline
// (withoutLineDeadline), so how long a healthy handler call takes on a loaded host decides nothing the
// row asserts.
func budgetDispatch(t testing.TB, dispatch func(context.Context, ipc.Request) ipc.Response,
) func(context.Context, ipc.Request) ipc.Response {
	earliest := time.Now().Add(drainLineDeadline)
	lifted := withoutLineDeadline(dispatch)
	return func(ctx context.Context, req ipc.Request) ipc.Response {
		if dl, ok := ctx.Deadline(); ok && dl.Before(earliest) {
			t.Errorf("line %s reached its handler with a deadline %v before the earliest the drain's own "+
				"drainLineDeadline sets: a pass budget must never reach a line as a deadline", req.Nonce, earliest.Sub(dl))
		}
		return lifted(ctx, req)
	}
}

// budgetRowTB is a testing.TB that records what budgetDispatch's check reports instead of failing the
// row that pins the check.
type budgetRowTB struct {
	testing.TB
	errs []string
}

func (b *budgetRowTB) Errorf(format string, args ...any) {
	b.errs = append(b.errs, fmt.Sprintf(format, args...))
}

// TestBudgetDispatch_ReportsADeadlineShorterThanTheDrainsOwnAndLiftsTheDrains pins what a budget row's
// dispatch does: a line carrying the drain's own drainLineDeadline, or a later one, passes the check and
// runs with no deadline; a line carrying a shorter one, as a pass budget put on the line would be, is
// reported, and so is one whose deadline has already passed when it arrives.
func TestBudgetDispatch_ReportsADeadlineShorterThanTheDrainsOwnAndLiftsTheDrains(t *testing.T) {
	rec := &budgetRowTB{TB: t}
	var lifted bool
	dispatch := budgetDispatch(rec, func(ctx context.Context, _ ipc.Request) ipc.Response {
		_, has := ctx.Deadline()
		lifted = !has && ctx.Err() == nil
		return ipc.Response{OK: true}
	})

	own, cancel := context.WithTimeout(context.Background(), drainLineDeadline)
	defer cancel()
	require.True(t, dispatch(own, ipc.Request{}).OK)
	require.Empty(t, rec.errs, "the drain's own drainLineDeadline passes the check")
	require.True(t, lifted, "and the line runs without it")

	later, cancel := context.WithDeadline(context.Background(), time.Now().Add(drainLineDeadline+time.Second))
	defer cancel()
	require.True(t, dispatch(later, ipc.Request{}).OK)
	require.Empty(t, rec.errs, "a deadline later than the drain's own passes the check too")

	budget, cancel := context.WithTimeout(context.Background(), idleRunBudget)
	defer cancel()
	require.True(t, dispatch(budget, ipc.Request{Nonce: "budget"}).OK)
	require.Len(t, rec.errs, 1, "a line whose deadline is a pass budget fails the row")
	require.Contains(t, rec.errs[0], "line budget")

	spent, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	require.True(t, dispatch(spent, ipc.Request{}).OK)
	require.Len(t, rec.errs, 2, "so does one whose deadline had passed before it arrived")
}

// drainDeadlineHelpers are the only functions in this package's tests that may call drainConfig().
var drainDeadlineHelpers = map[string]bool{
	"contentDrainConfig": true, "budgetDrainConfig": true, "lineDeadlineDrainConfig": true,
}

// productDispatchMethods are the daemon's dispatches a drainer can be built over: a test that hands
// one of them to a drainer bare, or calls one from a dispatch it hands a drainer, runs real I/O under
// the line's deadline without having chosen to.
var productDispatchMethods = map[string]bool{"drainDispatch": true, "runIngested": true}

// TestDrainRows_EveryDrainerChoosesWhetherItsLinesKeepTheirDeadline: in this package's tests, a drainer
// over the daemon's own dispatch is built only through contentDrainConfig, budgetDrainConfig or
// lineDeadlineDrainConfig, and a Dispatch a test writes itself never reaches the daemon's dispatch
// past that choice: it is wrapped in withoutLineDeadline, or it calls the dispatch of a config one of
// the three built.
func TestDrainRows_EveryDrainerChoosesWhetherItsLinesKeepTheirDeadline(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	fset := token.NewFileSet()
	var offenders []string
	report := func(n ast.Node, what string) {
		offenders = append(offenders, fset.Position(n.Pos()).String()+": "+what)
	}
	for _, name := range files {
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		f, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || drainDeadlineHelpers[fn.Name.Name] {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "drainConfig" && len(n.Args) == 0 {
						report(n, fn.Name.Name+" calls drainConfig() directly")
					}
					// withoutLineDeadline(...) is the choice itself: what it wraps is not inspected.
					if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "withoutLineDeadline" {
						return false
					}
				case *ast.KeyValueExpr:
					if id, ok := n.Key.(*ast.Ident); ok && id.Name == "Dispatch" {
						checkDrainDispatch(n.Value, fn.Name.Name, report)
					}
				case *ast.AssignStmt:
					for i, lhs := range n.Lhs {
						if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Dispatch" && i < len(n.Rhs) {
							checkDrainDispatch(n.Rhs[i], fn.Name.Name, report)
						}
					}
				}
				return true
			})
		}
	}
	sort.Strings(offenders)
	require.Empty(t, offenders,
		"a drainer over the daemon's dispatch must choose whether its lines keep drainLineDeadline "+
			"(contentDrainConfig, budgetDrainConfig, lineDeadlineDrainConfig or withoutLineDeadline):\n%s", strings.Join(offenders, "\n"))
}

// checkDrainDispatch reports a Dispatch value that is a product dispatch method, or a function literal
// that calls one, unless it is wrapped in withoutLineDeadline.
func checkDrainDispatch(v ast.Expr, in string, report func(ast.Node, string)) {
	switch v := v.(type) {
	case *ast.SelectorExpr:
		if productDispatchMethods[v.Sel.Name] {
			report(v, in+" hands a drainer "+v.Sel.Name+" bare")
		}
	case *ast.FuncLit:
		ast.Inspect(v.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && productDispatchMethods[sel.Sel.Name] {
					report(call, in+"'s Dispatch calls "+sel.Sel.Name+" under the line's deadline")
				}
			}
			return true
		})
	}
}
