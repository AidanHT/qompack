package observer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

func TestV6Prompt_NewDeliveryAfterUnpersistedRestartStaysDistinct(t *testing.T) {
	for _, second := range []string{"first", "different"} {
		t.Run(second, func(t *testing.T) {
			r := newRdxRig(t)
			firstID := r.sidecar(1, "observe.prompt")
			_, err := r.o.OnUserPrompt(WithObservation(context.Background(), firstID), promptOf("first"))
			require.NoError(t, err)
			r.restart(false)
			secondID := r.sidecar(2, "observe.prompt")
			_, err = r.o.OnUserPrompt(WithObservation(context.Background(), secondID), promptOf(second))
			require.NoError(t, err, "an existing original must not prevent a distinct delivery being recorded")
			first, err := r.st.ToolUse(context.Background(), VerbatimPromptID(testSession, 0))
			require.NoError(t, err)
			rec, err := r.st.ToolUse(context.Background(), VerbatimPromptID(testSession, 1))
			require.NoError(t, err, "equal text does not make two delivery identities the same event")
			require.Equal(t, "first", readRoot(context.Background(), t, r.st, first.Root))
			require.Equal(t, second, readRoot(context.Background(), t, r.st, rec.Root))
			sc, err := store.ReadCaptureSidecar(r.root, secondID)
			require.NoError(t, err)
			require.Equal(t, rec.ID, sc.ToolUseID)
			require.Equal(t, core.TurnIndex(1), rec.Turn)
		})
	}
}

func TestV6Prompt_RestartRepairsIndexBeforeSidecarCut(t *testing.T) {
	r := newRdxRig(t)
	ctx := context.Background()
	for arrival := uint64(1); arrival <= 3; arrival++ {
		id := r.sidecar(arrival, "observe.prompt")
		_, err := r.o.OnUserPrompt(WithObservation(ctx, id), promptOf("same text"))
		require.NoError(t, err)
	}
	id := r.sidecar(3, "observe.prompt")
	sc, err := store.ReadCaptureSidecar(r.root, id)
	require.NoError(t, err)
	wantID := sc.ToolUseID
	sc.Published, sc.ToolUseID, sc.Root = false, "", core.Hash{}
	data, err := json.Marshal(sc)
	require.NoError(t, err)
	path, err := store.CaptureSidecarPath(r.root, id)
	require.NoError(t, err)
	require.NoError(t, paths.WriteAtomic(path, data, 0o600))
	before := r.index()
	r.restart(false)
	_, err = r.o.OnUserPrompt(WithObservation(ctx, id), promptOf("same text"))
	require.NoError(t, err)
	require.Equal(t, before, r.index(), "repair must not mint another record or conflate an earlier equal prompt")
	sc, err = store.ReadCaptureSidecar(r.root, id)
	require.NoError(t, err)
	require.True(t, sc.Published)
	require.Equal(t, wantID, sc.ToolUseID)
	require.Equal(t, core.TurnIndex(3), r.o.session(testSession).Turn)
}

func TestV6Prompt_PublishedMissingObjectIsNotAcknowledged(t *testing.T) {
	r := newRdxRig(t)
	ctx := context.Background()
	id := r.sidecar(1, "observe.prompt")
	_, err := r.o.OnUserPrompt(WithObservation(ctx, id), promptOf("lost object"))
	require.NoError(t, err)
	objects := paths.Of(r.root).Objects
	require.NoError(t, filepath.WalkDir(objects, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return os.Remove(path)
		}
		return nil
	}))
	_, err = r.o.OnUserPrompt(WithObservation(ctx, id), promptOf("lost object"))
	require.ErrorIs(t, err, ErrUnpublished, "a durable reference is not evidence its object remains available")
}
