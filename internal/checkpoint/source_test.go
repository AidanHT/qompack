package checkpoint_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/tokens"
	"github.com/stretchr/testify/require"
)

// TestSourceSetCarriesNoText reflects over SourceSet and requires every field to be of interface
// kind. This is the compile/test-time expression of the §8.5 regeneration rule: every field is a
// seam onto durable, original content, and there is no field into which live context text could
// be passed — so "checkpoint from a summary" (§4.6's compress-a-compression) is unbuildable. It
// would fail the moment anyone adds a string, []byte, or transcript-shaped struct field to
// SourceSet, which is precisely the change it exists to block.
func TestSourceSetCarriesNoText(t *testing.T) {
	typ := reflect.TypeOf(checkpoint.SourceSet{})
	require.Positive(t, typ.NumField(), "SourceSet with no fields would make this test vacuous")
	for i := range typ.NumField() {
		f := typ.Field(i)
		require.Equal(t, reflect.Interface, f.Type.Kind(),
			"SourceSet.%s is a %s; every SourceSet field must be an interface seam, never a text carrier",
			f.Name, f.Type.Kind())
	}
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
