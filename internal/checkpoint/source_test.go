package checkpoint_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/tokens"
	"github.com/stretchr/testify/require"
)

// TestSourceSetCarriesNoText reflects over SourceSet and requires every field to be a seam onto
// durable, original content. This is the compile/test-time expression of the §8.5 regeneration
// rule: there is no field into which live context text could be passed — so "checkpoint from a
// summary" (§4.6's compress-a-compression) is unbuildable. It would fail the moment anyone adds a
// string, []byte, or transcript-shaped struct field to SourceSet, which is precisely the change it
// exists to block.
//
// A seam is an interface, OR a niladic accessor that RETURNS one. The second form is
// SourceSet.LedgerFn, and it is admitted on exactly the terms the first is: it can carry nothing
// but the interface it hands back, so a caller can no more pass a summary through
// `func() negknow.Ledger` than through `negknow.Ledger`. The signature is checked, not just the
// kind — `func() string`, `func(string) negknow.Ledger` and a multi-result func are all still
// rejected, which is what keeps this a guard rather than a hole in it.
func TestSourceSetCarriesNoText(t *testing.T) {
	typ := reflect.TypeOf(checkpoint.SourceSet{})
	require.Positive(t, typ.NumField(), "SourceSet with no fields would make this test vacuous")
	for i := range typ.NumField() {
		f := typ.Field(i)
		if f.Type.Kind() == reflect.Interface {
			continue
		}
		require.Equal(t, reflect.Func, f.Type.Kind(),
			"SourceSet.%s is a %s; every SourceSet field must be an interface seam or an accessor onto one, never a text carrier",
			f.Name, f.Type.Kind())
		require.Equal(t, 0, f.Type.NumIn(),
			"SourceSet.%s is an accessor that takes arguments; a seam accessor must be niladic", f.Name)
		require.Equal(t, 1, f.Type.NumOut(),
			"SourceSet.%s must return exactly one value, and it must be the seam", f.Name)
		require.Equal(t, reflect.Interface, f.Type.Out(0).Kind(),
			"SourceSet.%s returns a %s; a seam accessor must return an interface, never a text carrier",
			f.Name, f.Type.Out(0).Kind())
	}
}

// TestSourceSetValidateAcceptsALedgerAccessor is the producer half of the lazy-ledger seam: a set
// whose ledger is still a promise is WIRED, and Validate must say so.
//
// It is the regression guard for the defect this seam was added for. Validate used to reject a nil
// Ledger outright, FileWriter.SetSources dropped every set it rejected without a word, and the
// production composition root — which cannot open a ledger at wiring time, because negknow.Open is
// lazy by design — therefore published nothing the cold PreCompact path could read. The first
// PreCompact of every fresh daemon answered with a null hookSpecificOutput and sealed no
// checkpoint.
func TestSourceSetValidateAcceptsALedgerAccessor(t *testing.T) {
	src := checkpoint.SourceSet{
		Store: stubStoreForValidate{}, Segments: stubSegmentsForValidate{}, Pins: stubPinsForValidate{},
		Graph: stubGraphForValidate{}, Grammar: grammar.New(), Tokens: stubTokensForValidate{},
	}

	require.ErrorContains(t, src.Validate(), "SourceSet.Ledger is nil",
		"neither a handle nor an accessor: the ledger seam is genuinely unwired")

	src.LedgerFn = func() negknow.Ledger { return nil }
	require.NoError(t, src.Validate(),
		"an accessor IS a wired ledger seam; a producer must be able to publish this set")
}

// TestSourceSetResolveCallsTheAccessorIn is the consumer half: Begin reads eliminations out of the
// handle, so Resolve calls the promise in and reports a promise that cannot be kept.
//
// The nil-accessor arm is the degradation contract. A ledger that failed to open must reach the
// caller as the same named, top-of-call error a nil field gets — never as a nil dereference three
// frames down inside Begin's tier-1 seeding.
func TestSourceSetResolveCallsTheAccessorIn(t *testing.T) {
	base := checkpoint.SourceSet{
		Store: stubStoreForValidate{}, Segments: stubSegmentsForValidate{}, Pins: stubPinsForValidate{},
		Graph: stubGraphForValidate{}, Grammar: grammar.New(), Tokens: stubTokensForValidate{},
	}

	t.Run("an accessor that resolves to nothing is reported, not dereferenced", func(t *testing.T) {
		src := base
		src.LedgerFn = func() negknow.Ledger { return nil }
		_, err := src.Resolve()
		require.ErrorContains(t, err, "SourceSet.Ledger is nil")
	})

	t.Run("a resolved accessor materializes the handle", func(t *testing.T) {
		want := stubLedgerForValidate{}
		calls := 0
		src := base
		src.LedgerFn = func() negknow.Ledger { calls++; return want }

		got, err := src.Resolve()
		require.NoError(t, err)
		require.Equal(t, want, got.Ledger, "Resolve must materialize the handle the accessor answered with")
		require.Equal(t, 1, calls)

		require.Nil(t, src.Ledger,
			"Resolve takes a VALUE receiver: the caller's set keeps its live accessor for the next call")
	})

	t.Run("a handle already present wins and the accessor is never called", func(t *testing.T) {
		want := stubLedgerForValidate{}
		src := base
		src.Ledger = want
		src.LedgerFn = func() negknow.Ledger {
			t.Fatal("Resolve must not consult the accessor when a handle is present")
			return nil
		}

		got, err := src.Resolve()
		require.NoError(t, err)
		require.Equal(t, want, got.Ledger)
	})
}

// TestSourceSetValidateZeroNamesStore covers the top of Validate's field walk: a completely
// unwired SourceSet must be rejected with an error naming Store, the first nil seam. It would
// fail against a Validate that returned nil for an empty set — a guard that never fires — or one
// whose message did not name the missing dependency. The remaining arms (each later field nil in
// turn, and the fully-wired nil return) need real backend instances and belong to the writer
// seat's Begin fixtures, which construct exactly those.
func TestSourceSetValidateZeroNamesStore(t *testing.T) {
	err := checkpoint.SourceSet{}.Validate()
	require.Error(t, err)
	require.ErrorContains(t, err, "Store")
}

// TestPackageFunctionsWorkWithoutObservers is §5b's usability guarantee: the receiver-less
// package functions must work — no panic, no nil dereference — with the package observers never
// set, because the defaults are a Nop logger and a discarding registry. This test deliberately
// never calls SetObservers (and every test that does restores the defaults on cleanup), so the
// observer state here is value-identical to a fresh process.
//
// It would fail against an obs.go whose curLog/curReg started nil instead of at the no-op
// implementations — the first counter or Warn inside any of the three calls would then nil-
// dereference.
//
// At the time of writing, ExtractDecisions and ValidatePointers are SP-01 stubs reporting
// core.ErrNotImplemented and Truncate is the documented identity; other seats are implementing
// them now. This test therefore asserts only the no-panic property, never their results — and it
// stays valid after the real implementations land, because a real implementation handed a
// half-wired SourceSet must reject it with an error (SourceSet.Validate exists for exactly
// that), never a panic.
func TestPackageFunctionsWorkWithoutObservers(t *testing.T) {
	require.NotPanics(t, func() {
		_, _ = checkpoint.ExtractDecisions(context.Background(), checkpoint.SourceSet{}, core.TurnIndex(0))
	})

	require.NotPanics(t, func() {
		cfg := config.Defaults()
		est := tokens.New(cfg, filepath.Join(t.TempDir(), "calibration.json"))
		_, _ = checkpoint.Truncate(minimalGoldenCheckpoint(), core.Tokens(cfg.Checkpoint.BudgetTokens),
			cfg.Checkpoint.Tiers, est)
	})

	require.NotPanics(t, func() {
		p := checkpoint.Pointers{
			Files: []checkpoint.FilePointer{{
				Path: "src/auth.ts",
				Hash: core.HashBytes(core.DomainRoot, []byte("content")),
				Why:  "refreshToken lives here",
			}},
		}
		_, _ = checkpoint.ValidatePointers(context.Background(), t.TempDir(), p)
	})
}

// TestSetSourcesReportsARejection is the observability half of the first-PreCompact fix.
//
// SetSources used to drop an invalid set on the floor and return nothing. That silence is what
// turned a wiring bug into a symptom one layer and one compaction away: the composition root
// published a set the writer would not take, nothing said so, and the failure surfaced later
// inside Begin as "SourceSet.Store is nil" — naming a seam that was wired all along — behind a
// single Warn and a null hookSpecificOutput. A rejection now names the missing seam to its caller
// AND leaves a Loud line for the call sites that have nowhere to return one, and the writer keeps
// the last set it accepted rather than being blanked by the bad one.
func TestSetSourcesReportsARejection(t *testing.T) {
	f := newFx(t)

	require.NoError(t, f.w.SetSources(f.src), "fixture sanity: the complete set is accepted")

	broken := f.src
	broken.Store = nil
	err := f.w.SetSources(broken)
	require.ErrorContains(t, err, "SourceSet.Store is nil",
		"a rejected source set must name the seam its caller failed to wire")

	// The good set survives the bad one: PreCompact still seals, which is the observable form of
	// "a mis-wired caller cannot blank live seams".
	res, pcErr := f.w.PreCompact(f.ctx(), f.precompactInput())
	require.NoError(t, pcErr)
	require.FileExists(t, paths.Long(res.Ref.Path))
}
