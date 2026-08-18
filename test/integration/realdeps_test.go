package integration

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
)

// chunkCacheName mirrors internal/store's own filename for the exact token estimator's
// per-chunk cache, so the store under test persists token counts exactly where the real
// binary would.
const chunkCacheName = "chunktokens.bin"

// openRealStore opens the project's store with EVERY §5.8 dependency real — none nil, none
// faked. This is the whole point of the §4 files sharing it: store.Open's defaultDeps would
// fill the same members itself, but relying on it would leave this package green even if
// defaultDeps quietly swapped a member for a stub, so each collaborator is constructed here
// exactly as the architecture wires it. (Two §4 authors wrote this helper independently and
// identically, modulo the metrics registry; this is the union of the two.)
func openRealStore(t *testing.T, p *testutil.Project) store.Store {
	t.Helper()
	s, err := store.Open(p.Root, p.Cfg, store.Deps{
		Chunker: chunk.New(chunk.FromConfig(p.Cfg)),
		Canon:   canon.Default(p.Cfg.Store.Canonicalize),
		Symbols: symbols.New(),
		Tokens: tokens.NewExact(p.Cfg, tokens.DefaultCalibPath(),
			filepath.Join(paths.Of(p.Root).State, chunkCacheName)),
		Redact:  redact.New(p.Cfg),
		Log:     p.Log,
		Metrics: obs.New(p.Clock),
		Clock:   p.Clock,
	})
	require.NoError(t, err, "store.Open(%s) with all-real deps", p.Root)
	t.Cleanup(func() { _ = s.Close() })
	return s
}
