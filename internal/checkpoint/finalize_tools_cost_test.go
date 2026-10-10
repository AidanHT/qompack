package checkpoint

// SP10-D1 (plans/V2-SP-10-carried-defects.md), V6 close-out. After F4-8 moved keepResolvableTools
// from store.Has to store.ObjectOnDisk — the right question, "is the file really there" — its
// profile turned into filesystem metadata calls: 59 % of BenchmarkFinalize on Windows was
// GetFileAttributesEx under keepResolvableTools. A tool pointer names a ROOT, which is never an
// object, yet the predicate statted the root first: two failing stats (one per candidate
// spelling) before the in-memory root index was even consulted, on every pointer, followed by one
// stat per chunk, again on every pointer even when two pointers share a chunk.
//
// These tests pin the cost model, not the clock (ADR 0010): which hashes reach the filesystem,
// and how often. A counting ObjectPresence records every call, which no co-load can inflate. The
// second test pins what the reordering may not change: every verdict is the definition's.

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// presenceCountingStore is the shipped store with its ObjectOnDisk answers counted per hash.
type presenceCountingStore struct {
	store.Store
	disk  store.ObjectPresence
	calls map[core.Hash]int
}

func (s *presenceCountingStore) ObjectOnDisk(h core.Hash) bool {
	s.calls[h]++
	return s.disk.ObjectOnDisk(h)
}

var _ store.ObjectPresence = (*presenceCountingStore)(nil)

// openCountingStore opens a real store on a fresh project root and wraps it.
func openCountingStore(t *testing.T) (*presenceCountingStore, string) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	st, err := store.Open(root, config.Defaults(), store.Deps{Log: logging.Nop()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	disk, ok := st.(store.ObjectPresence)
	require.True(t, ok, "the shipped store answers ObjectOnDisk")
	return &presenceCountingStore{Store: st, disk: disk, calls: map[core.Hash]int{}}, root
}

// putRoot stores body and returns its root.
func putRoot(t *testing.T, s store.Store, body []byte) store.Root {
	t.Helper()
	res, err := s.PutBytes(context.Background(), body, store.PutOptions{Tool: "Bash"})
	require.NoError(t, err)
	return res.Root
}

// randomBody is incompressible, distinct content of n bytes, so a large n spans several chunks.
func randomBody(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

// removeObjectFile deletes h's object file the way a crash or a quarantine would, leaving every
// index line in place.
func removeObjectFile(t *testing.T, root string, h core.Hash) {
	t.Helper()
	hx := fmt.Sprintf("%x", h[:])
	dir := filepath.Join(paths.Of(root).Objects, hx[:2], hx[2:4])
	for _, name := range []string{hx + ".zst", hx} {
		if err := os.Remove(paths.Long(filepath.Join(dir, name))); err == nil {
			return
		}
	}
	t.Fatalf("object %s was not present to remove", h.Short())
}

// TestKeepResolvableToolsStatsEachChunkOnceAndNeverTheRoot is the SP10-D1 cost pin: a pointer that
// names a root the index resolves reaches the filesystem only for that root's chunks, and a chunk
// shared by several pointers is statted once per Finalize, not once per pointer.
func TestKeepResolvableToolsStatsEachChunkOnceAndNeverTheRoot(t *testing.T) {
	s, _ := openCountingStore(t)
	small := putRoot(t, s, []byte("pool statistics for the failing window"))
	large := putRoot(t, s, randomBody(t, 96<<10))
	require.Greater(t, len(large.Chunks), 1, "the large body must span several chunks or sharing proves nothing")

	in := []ToolPointer{
		{ToolUseID: "toolu_a", Hash: small.Hash},
		{ToolUseID: "toolu_b", Hash: large.Hash},
		{ToolUseID: "toolu_c", Hash: small.Hash}, // same root again: a repeated read
		{ToolUseID: "toolu_d", Hash: large.Hash},
	}
	out, drops := keepResolvableTools(context.Background(), in, nil, SourceSet{Store: s}, oncePerHash(objectPresent(s)))
	require.Equal(t, in, out, "every pointer resolves")
	require.Empty(t, drops)

	for _, r := range []store.Root{small, large} {
		require.Zero(t, s.calls[r.Hash],
			"root %s was statted: a root the index resolves is never an object file", r.Hash.Short())
		for _, c := range r.Chunks {
			require.Equal(t, 1, s.calls[c.Hash],
				"chunk %s of root %s must be statted exactly once per Finalize", c.Hash.Short(), r.Hash.Short())
		}
	}
}

// TestKeepResolvableToolsVerdictsMatchTheDefinition pins what the cost change may not move: a tool
// pointer survives exactly when its hash is an object on disk, or it is a root the index resolves
// and every chunk of that root is on disk — in pointer order, with the same drop line for each
// pointer that does not.
func TestKeepResolvableToolsVerdictsMatchTheDefinition(t *testing.T) {
	s, root := openCountingStore(t)
	ctx := context.Background()

	held := putRoot(t, s, randomBody(t, 96<<10))
	lost := putRoot(t, s, randomBody(t, 96<<10))
	require.Greater(t, len(lost.Chunks), 1)
	removeObjectFile(t, root, lost.Chunks[len(lost.Chunks)-1].Hash)
	chunkNamed := held.Chunks[0].Hash
	var unknown core.Hash
	copy(unknown[:], randomBody(t, len(unknown)))

	definition := func(h core.Hash) bool {
		if s.disk.ObjectOnDisk(h) {
			return true
		}
		r, err := s.GetRoot(ctx, h)
		if err != nil {
			return false
		}
		for _, c := range r.Chunks {
			if !s.disk.ObjectOnDisk(c.Hash) {
				return false
			}
		}
		return true
	}

	in := []ToolPointer{
		{ToolUseID: "toolu_held", Hash: held.Hash},
		{ToolUseID: "toolu_lost", Hash: lost.Hash},
		{ToolUseID: "toolu_chunk", Hash: chunkNamed},
		{ToolUseID: "toolu_unknown", Hash: unknown},
		{ToolUseID: "toolu_held_again", Hash: held.Hash},
		{ToolUseID: "toolu_lost_again", Hash: lost.Hash},
	}
	var wantOut []ToolPointer
	var wantDrops []DropEntry
	for _, p := range in {
		if definition(p.Hash) {
			wantOut = append(wantOut, p)
			continue
		}
		wantDrops = append(wantDrops, DropEntry{
			Kind: dropPointerUnresolvable, ID: string(p.ToolUseID),
			Detail: "object missing from the store (collected?)",
		})
	}
	require.Len(t, wantOut, 3, "held, the chunk named directly and held again survive by definition")

	prior := []DropEntry{{Kind: dropPointerMissing, ID: "src/gone.ts"}}
	out, drops := keepResolvableTools(ctx, in, prior, SourceSet{Store: s}, oncePerHash(objectPresent(s)))
	require.Equal(t, wantOut, out)
	require.Equal(t, append(prior, wantDrops...), drops, "drops append after the ones already collected")
}
