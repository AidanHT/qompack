package observer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// This retains the original defect identifier. Its assertion is the required
// invariant: one leased delivery has one derived record across an index/link cut.
func TestDerivedPublication_V6_IndexBeforeLinkCutDuplicatesAcrossRestart(t *testing.T) {
	for _, kind := range []string{"subagent", "tool"} {
		t.Run(kind, func(t *testing.T) {
			r := newRdxRig(t)
			ctx := context.Background()
			op := rdxOpStop
			if kind == "tool" {
				op = rdxOpTool
			}
			id := r.sidecar(1, op)
			capture := func() error {
				if kind == "tool" {
					_, err := r.o.OnToolUse(WithObservation(ctx, id), readOf("", supersedePath, rdxBody))
					return err
				}
				_, err := r.o.OnStop(WithObservation(ctx, id), stopOf(true), true)
				return err
			}
			require.NoError(t, capture())
			sc, err := store.ReadCaptureSidecar(r.root, id)
			require.NoError(t, err)
			originalID, originalRoot := sc.ToolUseID, sc.Root
			sc.Published, sc.ToolUseID, sc.Root = false, "", core.Hash{}
			data, err := json.Marshal(sc)
			require.NoError(t, err)
			path, err := store.CaptureSidecarPath(r.root, id)
			require.NoError(t, err)
			require.NoError(t, paths.WriteAtomic(path, data, 0o600))
			before := r.index()
			r.restart(false)
			r.clock.Advance(time.Second)
			require.NoError(t, capture())
			require.Equal(t, before, r.index(), "restart must repair the link without another record")
			sc, err = store.ReadCaptureSidecar(r.root, id)
			require.NoError(t, err)
			require.True(t, sc.Published)
			require.Equal(t, originalID, sc.ToolUseID)
			require.Equal(t, originalRoot, sc.Root)
			require.Equal(t, int64(1), r.counter(rdxCounterAbsorbed))
		})
	}
}

// Recover must use the original publication intent even after later writes
// change the supersede candidates and the original capture link has been lost.
func TestDerivedPublication_V6_MissingLinkAfterSupersessionDrift(t *testing.T) {
	r := newRdxRig(t)
	ctx := context.Background()
	for i, name := range []string{"prior", "original", "later"} {
		r.turn(core.TurnIndex(i))
		id := r.sidecar(uint64(i+1), rdxOpTool)
		_, err := r.o.OnToolUse(WithObservation(ctx, id), readOf(name, supersedePath, rdxBody))
		require.NoError(t, err)
	}
	prior, err := r.st.ToolUse(ctx, "prior")
	require.NoError(t, err)
	require.Equal(t, core.ToolUseID("original"), prior.SupersededBy)
	original, err := r.st.ToolUse(ctx, "original")
	require.NoError(t, err)
	require.Equal(t, core.ToolUseID("later"), original.SupersededBy)
	id, err := core.NewObservationID(testSession, 2)
	require.NoError(t, err)
	sc, err := store.ReadCaptureSidecar(r.root, id)
	require.NoError(t, err)
	sc.Published, sc.ToolUseID, sc.Root = false, "", core.Hash{}
	data, err := json.Marshal(sc)
	require.NoError(t, err)
	path, err := store.CaptureSidecarPath(r.root, id)
	require.NoError(t, err)
	require.NoError(t, paths.WriteAtomic(path, data, 0o600))
	beforeIndex, beforeRoots := r.index(), r.roots()
	r.restart(false)
	_, err = r.o.OnToolUse(WithObservation(ctx, id), readOf("original", supersedePath, rdxBody))
	require.NoError(t, err)
	require.Equal(t, beforeIndex, r.index(), "recovery must not recalculate supersede marks")
	require.Equal(t, beforeRoots, r.roots(), "recovery must not derive new objects")
	sc, err = store.ReadCaptureSidecar(r.root, id)
	require.NoError(t, err)
	require.True(t, sc.Published)
	require.Equal(t, original.ID, sc.ToolUseID)
	require.Equal(t, original.Root, sc.Root)
	require.Equal(t, int64(1), r.counter(rdxCounterAbsorbed))
}
