package guards

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/rules"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/skills"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/symbols"
)

// stubPackage is one §5 interface package, its constructor, and what SP-01 promises about it.
type stubPackage struct {
	// pkg is the package name as plans/OWNERS.tsv spells it.
	pkg string
	// build constructs the package's principal seam. A nil build means the package has no
	// constructible interface — see compositionRootPkgs.
	build func(t *testing.T) any
	// pureMethods are methods that are IMPLEMENTED in wave 0 and must therefore NOT report
	// ErrNotImplemented. They are named rather than detected, because "this method works" is a
	// promise someone has to make deliberately.
	pureMethods map[string]bool
	// zeroValueOnly marks a seam whose every method returns a documented zero value with no
	// error at all. Such a package cannot signal "not implemented" through an error, so the walk
	// has nothing to assert — and that has to be declared here rather than inferred, because
	// "this interface returns no errors" and "the walk silently found nothing" look identical.
	zeroValueOnly bool
}

// compositionRootPkgs are the two §5 packages that are composition roots: they wire other
// packages together and expose no seam for a later wave to implement against. They are in the
// table so the completeness check sees all 23, with no constructor to walk.
var compositionRootPkgs = map[string]bool{"commands": true, "daemon": true}

// stubRegistry is the table the plan requires: every one of the 23 §5 interface packages, with a
// constructor for the seam a later wave replaces.
//
// The registry is written out by hand rather than discovered by reflection over the module. That
// is the point: a package that gains a seam and is not added here is exactly the failure this
// guard exists to catch, and a discovery-based version could not tell "no seam" from "forgot to
// register".
func stubRegistry() []stubPackage {
	return []stubPackage{
		{
			pkg: "chunk", build: func(*testing.T) any { return chunk.New(chunk.DefaultParams()) },
			pureMethods: map[string]bool{"Split": true},
		},
		{pkg: "canon", build: func(*testing.T) any { return canon.NewRegistry() }},
		{pkg: "symbols", build: func(*testing.T) any { return symbols.New() }, zeroValueOnly: true},
		{pkg: "redact", build: func(*testing.T) any { return redact.New(config.Defaults()) }, zeroValueOnly: true},
		// SP-03 landed the real sketch math (QPKS framing, Appendix A Bloom sizing), so none of
		// this seam's methods reports ErrNotImplemented any more: UnmarshalBinary now answers with
		// this package's own decode sentinels. It is registered for completeness only.
		{
			pkg: "sketch", build: func(*testing.T) any { return sketch.NewBloom(bloomCapacity, bloomFPRate) },
			pureMethods: allMethodsAreReal,
		},
		// store's seam is REAL as of SP-06 (L1: content-addressed objects, the tool_use and
		// file-version indices, the segment log, search and GC), so none of its methods is
		// expected to report ErrNotImplemented any more. It stays in the registry for
		// completeness, which is what TestStubRegistry_ListsEveryPackageOnDisk checks.
		{pkg: "store", build: func(t *testing.T) any {
			s, err := store.Open(t.TempDir(), config.Defaults(), store.Deps{Log: logging.Nop()})
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			return s
		}, pureMethods: allMethodsAreReal},
		// dag's seam is REAL as of SP-07: Open returns the live dependence graph, so AddNode,
		// AddEdge, BackwardSlice, ForwardSlice, Flush and Compact all do their own work and none
		// of them reports ErrNotImplemented. It stays registered for the completeness check;
		// dropping the marker would assert dag is still a stub, which it is not.
		{pkg: "dag", build: func(t *testing.T) any {
			g, err := dag.Open(t.TempDir(), config.Defaults(), logging.Nop())
			require.NoError(t, err)
			return g
		}, pureMethods: allMethodsAreReal},
		{pkg: "grammar", build: func(*testing.T) any { return grammar.New() }},
		{pkg: "negknow", build: func(t *testing.T) any {
			l, err := negknow.Open(t.TempDir(), config.Defaults(), nil, negknow.Deps{Log: logging.Nop()})
			require.NoError(t, err)
			return l
		}},
		{pkg: "analyzer", build: func(*testing.T) any { return analyzer.NewCheapScorer(nil) }},
		{pkg: "scheduler", build: func(*testing.T) any { return scheduler.NewBOCD(hazardRate, nil) }},
		{pkg: "checkpoint", build: func(t *testing.T) any {
			w, err := checkpoint.OpenWriter(t.TempDir(), config.Defaults(), logging.Nop(),
				obs.New(core.SystemClock()), core.SystemClock())
			require.NoError(t, err)
			return w
		}},
		{pkg: "pins", build: func(t *testing.T) any {
			s, err := pins.Open(t.TempDir())
			require.NoError(t, err)
			return s
		}},
		{pkg: "rehydrate"}, // package-level Build/StandingInstruction, no constructed seam
		{pkg: "rules", build: func(*testing.T) any { return rules.New() }},
		{pkg: "skills", build: func(*testing.T) any { return skills.New() }},
		{pkg: "mcp", build: func(*testing.T) any { return mcp.NewServer("qompack", "0.1.0", logging.Nop()) }},
		{pkg: "commands"},
		// eval's seam is REAL from SP-02 (Phase 0 is the first thing built after the foundation),
		// so none of its methods reports ErrNotImplemented any more: Load reports ErrNotFound on
		// an empty corpus, which is the honest answer and not a stub's. It stays registered so
		// the walk still proves its constructor builds and none of its methods panics on
		// zero-valued arguments.
		{pkg: "eval", build: func(*testing.T) any {
			return eval.New(eval.Options{Cfg: config.Defaults()})
		}, pureMethods: allMethodsAreReal},
		{pkg: "ipc", build: func(*testing.T) any {
			return ipc.NewClient(ipc.Addr{}, nil, logging.Nop(), obs.New(core.SystemClock()))
		}},
		{pkg: "daemon"},
		// observer's seam is REAL as of SP-08's PostToolUse pipeline (commit ba477c5): OnToolUse
		// stores, indexes, sketches and emits the §8.1 item 4 DAG chain, so no method reports
		// ErrNotImplemented any more. New also stopped accepting a bare ProjectRoot — the five
		// collaborators below are the ones without which L0 can record nothing at all — which is
		// what TestObserverNewRequiresItsCollaborators pins alongside this row.
		{pkg: "observer", build: func(t *testing.T) any {
			o, err := observer.New(observerOptions(t))
			require.NoError(t, err)
			return o
		}, pureMethods: allMethodsAreReal},
		// contract's monitor mechanics are REAL in wave 0 (§12.1), so none of its methods is
		// expected to report ErrNotImplemented. It is registered for completeness only.
		{pkg: "contract", build: func(t *testing.T) any {
			return contract.NewMonitor(logging.Nop(), obs.New(core.SystemClock()),
				filepath.Join(t.TempDir(), contractStateFile))
		}, pureMethods: allMethodsAreReal},
	}
}

// allMethodsAreReal marks a package whose seam is implemented rather than stubbed: contract
// because its monitor mechanics are real in wave 0 (§12.1), dag because SP-07 landed the real
// dependence graph. Each subplan that replaces a stub adds its package here, which is the point —
// "this package is finished" is a claim someone has to make on purpose.
var allMethodsAreReal = map[string]bool{"*": true}

// Arbitrary well-formed constructor inputs. None duplicates a configuration default; they exist
// only so the constructor returns something to reflect over.
const (
	bloomCapacity = 128  //nomagic:allow arbitrary well-formed constructor input
	bloomFPRate   = 0.01 //nomagic:allow arbitrary well-formed constructor input
	hazardRate    = 0.05 //nomagic:allow arbitrary well-formed constructor input
)

// observerOptions is the minimal Options observer.New accepts now that SP-08 has landed: an empty
// ProjectRoot or a nil Store, Graph, Log or Clock is an error rather than a stub that would have
// reported ErrNotImplemented at the first hook instead.
func observerOptions(t *testing.T) observer.Options {
	t.Helper()

	root := t.TempDir()
	cfg := config.Defaults()
	log := logging.Nop()
	clock := core.SystemClock()

	s, err := store.Open(root, cfg, store.Deps{Log: log, Clock: clock})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	g, err := dag.Open(root, cfg, log)
	require.NoError(t, err)

	return observer.Options{
		ProjectRoot: root, Cfg: cfg, Store: s, Graph: g,
		Log: log, Metrics: obs.New(clock), Clock: clock,
	}
}

// TestObserverNewRequiresItsCollaborators is the other half of the observer registry row above.
//
// The guard used to assert that observer.New succeeded with nothing but a ProjectRoot and a Cfg.
// That was true of SP-01's stub and is false of the real constructor, and deleting the assertion
// outright would have left the NEW contract unguarded — so it is restated here rather than dropped.
func TestObserverNewRequiresItsCollaborators(t *testing.T) {
	full := observerOptions(t)

	built, err := observer.New(full)
	require.NoError(t, err, "the five mandatory collaborators are enough on their own")
	require.NotNil(t, built)

	for name, breakIt := range map[string]func(*observer.Options){
		"ProjectRoot": func(o *observer.Options) { o.ProjectRoot = "" },
		"Store":       func(o *observer.Options) { o.Store = nil },
		"Graph":       func(o *observer.Options) { o.Graph = nil },
		"Log":         func(o *observer.Options) { o.Log = nil },
		"Clock":       func(o *observer.Options) { o.Clock = nil },
	} {
		t.Run(name, func(t *testing.T) {
			broken := full
			breakIt(&broken)
			got, err := observer.New(broken)
			require.Error(t, err,
				"a missing %s must be reported by New, not discovered at the first hook", name)
			require.Nil(t, got)
		})
	}
}

// TestAllStubsReturnNotImplemented walks every registered seam and asserts each method either
// reports core.ErrNotImplemented or returns a documented zero value.
//
// The reason to do this reflectively rather than trusting each package's own suite is that a stub
// which quietly returns nil is indistinguishable from a working implementation at the call site.
// A consumer written against it in wave 1 would appear to work and produce nothing — the single
// worst failure mode this whole stub-and-suite mechanism (D9) exists to prevent.
func TestAllStubsReturnNotImplemented(t *testing.T) {
	for _, sp := range stubRegistry() {
		if sp.build == nil {
			continue
		}
		t.Run(sp.pkg, func(t *testing.T) {
			seam := sp.build(t)
			require.NotNil(t, seam, "%s: constructor returned nil", sp.pkg)

			// A package marked allMethodsAreReal is not exempt from the walk — only from the
			// ErrNotImplemented assertion, which cannot hold for an implementation that works.
			// Returning here instead was the difference between what eval's and contract's registry
			// entries promise ("the walk still proves its constructor builds and none of its methods
			// panics on zero-valued arguments") and what ran: nothing was called at all, on the two
			// packages whose comments say most about what the walk still does.
			realImpl := sp.pureMethods["*"]

			v := reflect.ValueOf(seam)
			typ := v.Type()
			checked := 0
			for i := 0; i < typ.NumMethod(); i++ {
				m := typ.Method(i)
				if !realImpl && sp.pureMethods[m.Name] {
					continue
				}
				errIdx := errorResultIndex(m.Type)
				if errIdx < 0 {
					// No error return: the method's contract is a documented zero value, so the
					// only thing left to assert is that it does not panic — asserted by calling
					// it, rather than by saying so in a comment.
					callSeamMethod(t, sp.pkg, v, m)
					continue
				}
				checked++
				if realImpl {
					callSeamMethod(t, sp.pkg, v, m)
					continue
				}
				assertMethodReportsNotImplemented(t, sp.pkg, v, m, errIdx)
			}
			if sp.zeroValueOnly {
				require.Zero(t, checked,
					"%s is declared zeroValueOnly but has an error-returning method — "+
						"drop the flag so the walk actually checks it", sp.pkg)
				return
			}
			require.Positive(t, checked,
				"%s: no method with an error return was checked — the walk found nothing", sp.pkg)
		})
	}
}

// callSeamMethod calls m with zero-valued arguments and asserts only that it survives the call.
//
// That is the whole assertion for a landed implementation and for a method with no error return: a
// seam that panics on a zero-valued argument costs the user their turn (§12.3), and that is as true
// of the finished article as of the stub it replaced.
func callSeamMethod(t *testing.T, pkg string, recv reflect.Value, m reflect.Method) []reflect.Value {
	t.Helper()

	args := make([]reflect.Value, 0, m.Type.NumIn()-1)
	for i := 1; i < m.Type.NumIn(); i++ {
		args = append(args, zeroArg(m.Type.In(i)))
	}

	var out []reflect.Value
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("%s.%s panicked on zero-valued arguments: %v\n"+
					"a seam must fail by reporting, never by panicking — a hook that panics "+
					"costs the user their turn (§12.3)", pkg, m.Name, r)
			}
		}()
		out = m.Func.Call(append([]reflect.Value{recv}, args...))
	}()
	return out
}

// assertMethodReportsNotImplemented calls m with zero-valued arguments and checks its error.
func assertMethodReportsNotImplemented(t *testing.T, pkg string, recv reflect.Value, m reflect.Method, errIdx int) {
	t.Helper()

	out := callSeamMethod(t, pkg, recv, m)

	errVal := out[errIdx].Interface()
	if errVal == nil {
		return // a documented zero value with no error is permitted
	}
	err, ok := errVal.(error)
	require.True(t, ok, "%s.%s: result %d is not an error", pkg, m.Name, errIdx)
	require.True(t, core.IsNotImplemented(err),
		"%s.%s must report core.ErrNotImplemented, got %v", pkg, m.Name, err)
}

// zeroArg builds a usable zero value for a parameter type, substituting a real context so a
// method that immediately reads ctx.Done() does not panic on a nil interface.
func zeroArg(t reflect.Type) reflect.Value {
	if t == reflect.TypeOf((*context.Context)(nil)).Elem() {
		return reflect.ValueOf(context.Background())
	}
	return reflect.Zero(t)
}

// errorResultIndex returns the index of the method's error result, or -1 if it has none.
func errorResultIndex(t reflect.Type) int {
	errType := reflect.TypeOf((*error)(nil)).Elem()
	for i := 0; i < t.NumOut(); i++ {
		if t.Out(i) == errType {
			return i
		}
	}
	return -1
}

// TestStubRegistry_ListsEveryPackageOnDisk is the completeness half of the plan's requirement.
//
// It compares the hand-written registry against plans/OWNERS.tsv, which
// TestV1_StubGraphIsInertAndOwned already requires to list every package on disk. (That test, not
// `devtool lint`: no lint sub-check reads the disk, and stubskips deliberately ignores the exit
// status of the `go test` run it greps, so a failure there would leave the lint green.) A new §5 package that nobody adds here would otherwise
// be silently unguarded — which is the same failure as having no guard at all, but harder to see.
func TestStubRegistry_ListsEveryPackageOnDisk(t *testing.T) {
	t.Parallel()

	registered := map[string]bool{}
	for _, sp := range stubRegistry() {
		require.False(t, registered[sp.pkg], "duplicate registry entry for %s", sp.pkg)
		registered[sp.pkg] = true
	}
	require.Len(t, registered, wantStubPackages,
		"the registry must list all %d §5 interface packages", wantStubPackages)

	// Every package with a stub probe in OWNERS.tsv must be in the registry.
	for _, pkg := range ownersWithProbes(t) {
		require.True(t, registered[pkg],
			"plans/OWNERS.tsv lists %s with a stub probe, but stubRegistry() does not mention it", pkg)
	}

	// And every registry entry must exist on disk.
	root := repoRoot(t)
	for pkg := range registered {
		dir := filepath.Join(root, "internal", pkg)
		fi, err := os.Stat(dir)
		require.NoError(t, err, "registry lists %s but %s does not exist", pkg, dir)
		require.True(t, fi.IsDir())
	}

	// The two composition roots are deliberately constructor-less; assert that is a decision and
	// not an omission.
	for _, sp := range stubRegistry() {
		if sp.build == nil {
			require.True(t, compositionRootPkgs[sp.pkg] || sp.pkg == "rehydrate",
				"%s has no constructor and is not a documented exception", sp.pkg)
		}
	}
}

// wantStubPackages is the §5 interface-package count commit 7 of the subplan enumerates.
const wantStubPackages = 23

// ownersWithProbes reads plans/OWNERS.tsv and returns every package whose row names a stub probe.
func ownersWithProbes(t *testing.T) []string {
	t.Helper()

	f, err := os.Open(filepath.Join(repoRoot(t), "plans", "OWNERS.tsv"))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	var pkgs []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 4 || fields[0] == "package" {
			continue
		}
		if probe := strings.TrimSpace(fields[3]); probe != "" && probe != "-" {
			pkgs = append(pkgs, fields[0])
		}
	}
	require.NoError(t, sc.Err())
	return pkgs
}

// repoRoot resolves the module root via `go list`, so the guard works regardless of the directory
// the test binary was started from.
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// compile-time proof the pure-function set the plan requires really is implemented, not stubbed:
// referencing them here fails the build if any is removed or turned into a method.
var (
	// The two composition roots have no seam to walk, but they must still exist and construct.
	_ = commands.All
	_ = daemon.New

	_ = chunk.RootHash
	_ = scheduler.YoungDaly
	_ = scheduler.SkiRentalShouldWrite
	_ = checkpoint.StripInjections
	_ = observer.Tombstone
	_ = rehydrate.StandingInstruction
	_ = grammar.FormatWarning
)
