package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
)

// The two counters SP-20's delta-base admission emits when it refuses to persist a delta-only
// representation and falls back to storing a second, full object beside the content root.
const (
	counterRoundTripUnproven = "store.delta.roundTripUnproven"
	counterBaseNotDurable    = "store.delta.baseNotDurable"
)

// observerCanonOptions reproduces internal/observer's canonOptions exactly: CRLF stripped
// unconditionally, the configured classes added when canonicalization is enabled, KeepDeltas on,
// and the configured MinHash. It is reproduced rather than imported because it is unexported there,
// and it is the ONE thing that decides which canonicalizer the observer's puts actually run under —
// which is the whole question this test exists to answer.
func observerCanonOptions(cfg config.Config) canon.Options {
	cc := cfg.Store.Canonicalize
	cls := []canon.Class{canon.Class("crlf")}
	if cc.Enabled {
		for _, s := range cc.Strip {
			if canon.Class(s) != canon.Class("crlf") {
				cls = append(cls, canon.Class(s))
			}
		}
	}
	return canon.Options{
		Strip:      cls,
		KeepDeltas: true,
		MinHash: sketch.MinHashOptions{
			Enabled:          cc.Enabled && cc.MinHash.Enabled,
			Permutations:     cc.MinHash.Permutations,
			ShingleSize:      5,
			NearDupThreshold: cc.MinHash.NearDupThreshold,
		},
	}
}

// TestIntegration_ObserverKeepRawPutsProveTheirDeltaRoundTrip answers the question unit B could not:
// every observer path puts with KeepRaw against the REAL canonicalizer, and SP-20's new admission
// rule stores a SECOND full object whenever the delta round trip cannot be proven. If that fired on
// ordinary tool output, every such put would roughly double the stored bytes.
//
// It drives the whole committed tool-output corpus — every canonicalizer class the observer can
// encounter — through the same PutOptions the observer builds, and asserts neither fallback counter
// ever fires.
func TestIntegration_ObserverKeepRawPutsProveTheirDeltaRoundTrip(t *testing.T) {
	p := testutil.NewProject(t)
	m := obs.New(p.Clock)
	s, err := store.Open(p.Root, p.Cfg, store.Deps{
		Chunker: chunk.New(chunk.FromConfig(p.Cfg)),
		Canon:   canon.Default(p.Cfg.Store.Canonicalize),
		Symbols: symbols.New(),
		Tokens: tokens.NewExact(p.Cfg, tokens.DefaultCalibPath(),
			filepath.Join(paths.Of(p.Root).State, chunkCacheName)),
		Redact:  redact.New(p.Cfg),
		Log:     p.Log,
		Metrics: m,
		Clock:   p.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	opts := observerCanonOptions(p.Cfg)
	puts := 0
	groups, err := os.ReadDir(corpusRel)
	require.NoError(t, err)
	for _, g := range groups {
		if !g.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(corpusRel, g.Name()))
		require.NoError(t, err)
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".txt") {
				continue
			}
			raw := corpus(t, g.Name(), f.Name())
			// The same options the observer builds for a tool result: KeepRaw on, the observer's
			// own canonicalizer selection, no path (the pathless Bash/test-runner class included).
			_, err := s.PutBytes(ctx, raw, store.PutOptions{
				Tool: "Bash", Canon: opts, KeepRaw: true,
			})
			require.NoError(t, err, "%s/%s", g.Name(), f.Name())
			puts++
		}
	}
	require.Greater(t, puts, 20, "the whole committed corpus must actually have been driven")

	snap := m.Snapshot()
	require.Zero(t, snap.Counters[counterRoundTripUnproven],
		"an observer put whose delta round trip is unprovable stores a second full object; "+
			"it fired %d times over %d corpus puts", snap.Counters[counterRoundTripUnproven], puts)
	require.Zero(t, snap.Counters[counterBaseNotDurable],
		"a delta whose base is not durable falls back to a full object")
}
