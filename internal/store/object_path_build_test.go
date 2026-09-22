package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// TestObjectPathFor_IsFilepathJoin pins that compressedObjectPath's single-allocation spelling is
// exactly the filepath.Join it replaced, for both spellings and both compression settings, over
// many hashes (so every hex digit appears in every fanout position).
func TestObjectPathFor_IsFilepathJoin(t *testing.T) {
	for _, opts := range [][]storeOpt{nil, {withCompressionNone()}} {
		tp := newTestStore(t, opts...)
		objects := paths.Of(tp.Root).Objects
		for i := 0; i < 512; i++ {
			h := core.HashBytes(core.DomainChunk, []byte{byte(i), byte(i >> 8)})
			hx := hexOf(h)
			wantZst := filepath.Join(objects, hx[:2], hx[2:4], hx+objectSuffix)
			wantBare := filepath.Join(objects, hx[:2], hx[2:4], hx)

			require.Equal(t, [2]string{wantZst, wantBare}, tp.Store.objectCandidates(h))
			want := wantZst
			if !tp.Store.compressing() {
				want = wantBare
			}
			require.Equal(t, want, tp.Store.objectPath(h))
		}
	}
}

// TestObjectPathFor_RelativeRootIsFilepathJoin covers a store opened on a relative project root,
// where paths.Of's layout is relative too: the spelled-out path must still be Join's.
func TestObjectPathFor_RelativeRootIsFilepathJoin(t *testing.T) {
	for _, root := range []string{".", "proj", filepath.Join("a", "..", "b")} {
		s := &FSStore{l: paths.Of(root)}
		h := core.HashBytes(core.DomainChunk, []byte(root))
		hx := hexOf(h)
		require.Equal(t, filepath.Join(paths.Of(root).Objects, hx[:2], hx[2:4], hx+objectSuffix),
			s.compressedObjectPath(h), "root %q", root)
	}
}
