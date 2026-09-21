package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The two halves of the pending/retention evidence GC reads: the in-process sources handed to Open,
// and the on-disk marker a crashed Put leaves behind.

// fakeRetentionSource is a RetentionRootSource that answers with what a test hands it.
type fakeRetentionSource struct {
	roots []RetentionRoot
	err   error
}

func (f fakeRetentionSource) RetentionRoots(context.Context) ([]RetentionRoot, error) {
	return f.roots, f.err
}

// TestRetentionFromSources_FillsInAClaimAndRefusesToGuessAtAnUnreadableOne.
//
// Four rules, and the first is the one that keeps GC honest: a source that reports an ERROR is not
// "nothing to retain". An unreadable lease set is indistinguishable from a full one, and treating it
// as empty is under-retention, which is data loss — so the whole round refuses instead. The rest are
// normalization: a nil source is not a source, a zero hash is not a claim, the FIRST source to name
// a hash owns its class and reason, and a claim that named neither is still recorded, as a lease
// held by a source, rather than entering the map unlabelled.
func TestRetentionFromSources_FillsInAClaimAndRefusesToGuessAtAnUnreadableOne(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	first := RetentionRoot{Hash: core.Hash{1}, Class: RetentionEvidence, Reason: "named first"}
	tp := newTestStore(t, func(_ *config.Config, d *Deps) {
		d.RetentionRoots = []RetentionRootSource{
			nil,
			fakeRetentionSource{roots: []RetentionRoot{
				first,
				{Hash: core.Hash{}, Class: RetentionEvidence, Reason: "a zero hash is not a claim"},
				{Hash: core.Hash{2}},
			}},
			fakeRetentionSource{roots: []RetentionRoot{
				{Hash: core.Hash{1}, Class: RetentionLease, Reason: "named second"},
			}},
		}
	})

	into := map[core.Hash]RetentionRoot{}
	require.NoError(t, tp.Store.retentionFromSources(context.Background(), into))

	require.Len(t, into, 2, "the nil source and the zero hash contributed nothing")
	require.Equal(t, first, into[core.Hash{1}], "the first source to name a hash owns its class and reason")
	require.Equal(t, RetentionLease, into[core.Hash{2}].Class, "an unclassed claim defaults to lease")
	require.NotEmpty(t, into[core.Hash{2}].Reason, "and it is given a reason a report can print")

	boom := errors.New("the lease journal could not be read")
	tp2 := newTestStore(t, func(_ *config.Config, d *Deps) {
		d.RetentionRoots = []RetentionRootSource{fakeRetentionSource{err: boom}}
	})
	err := tp2.Store.retentionFromSources(context.Background(), map[core.Hash]RetentionRoot{})
	require.ErrorIs(t, err, errRetentionRootsUnavailable)
	require.ErrorIs(t, err, boom, "the refusal carries why the source could not answer")
}

// TestPendingMarkerRoot_ReportsTheZeroHashForAMarkerItCannotRead.
//
// A pending marker records a write that had not yet been rooted, so the crash it exists to survive
// is exactly the crash that can tear it. All three unreadable shapes — gone, not JSON, and a root
// that is not a hash — answer with the zero hash rather than an error, because the marker's job at
// that point is done: GC still expires the file, and the outcome row simply names no root instead of
// naming a wrong one.
func TestPendingMarkerRoot_ReportsTheZeroHashForAMarkerItCannotRead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(paths.Long(p), []byte(body), 0o600))
		return p
	}

	require.True(t, pendingMarkerRoot(filepath.Join(dir, "gone.json")).IsZero(),
		"a marker that is no longer there names no root")
	require.True(t, pendingMarkerRoot(write("torn.json", `{"v":1,"root":`)).IsZero(),
		"a marker torn by the crash it records names no root")
	require.True(t, pendingMarkerRoot(write("bad.json", `{"v":1,"root":"not-a-hash"}`)).IsZero(),
		"a marker whose root is not a hash names no root")

	want := core.Hash{3}
	require.Equal(t, want,
		pendingMarkerRoot(write("good.json", `{"v":1,"root":"`+want.String()+`"}`)),
		"and a whole marker names the root it was written for")
}
