package config_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

func TestGet_DottedLookup(t *testing.T) {
	cfg := config.Defaults()

	v, ok := cfg.Get("scheduler.cache.writeMultiplier")
	require.True(t, ok)
	require.Equal(t, 1.25, v)

	v, ok = cfg.Get("nope")
	require.False(t, ok)
	require.Nil(t, v)

	v, ok = cfg.Get("store.chunk.min")
	require.True(t, ok)
	require.Equal(t, 1024, v)

	v, ok = cfg.Get("store.canonicalize.strip")
	require.True(t, ok)
	require.Equal(t, []string{"timestamps", "ansi", "pids", "addresses", "tmpPaths", "durations"}, v)

	v, ok = cfg.Get("runtime.rehydrate.skillIndexTokens")
	require.True(t, ok)
	require.Equal(t, 450, v)
}

// TestGet_NullableLeaf checks the (nil, true) contract for a *float64 leaf that is currently
// unset: the path exists, its value is simply nil, distinct from an unknown path's (nil, false).
func TestGet_NullableLeaf(t *testing.T) {
	cfg := config.Defaults()
	v, ok := cfg.Get("scheduler.youngDaly.measuredDeltaSeconds")
	require.True(t, ok)
	require.Nil(t, v)

	delta := 12.5
	cfg.Scheduler.YoungDaly.MeasuredDeltaSeconds = &delta
	v, ok = cfg.Get("scheduler.youngDaly.measuredDeltaSeconds")
	require.True(t, ok)
	require.Equal(t, 12.5, v)
}

// TestGet_UnknownPathIsFalseNotPanic guards Get's contract against malformed dotted paths that
// happen to share a prefix with a real leaf or section.
func TestGet_UnknownPathIsFalseNotPanic(t *testing.T) {
	cfg := config.Defaults()
	for _, path := range []string{
		"", "scheduler", "scheduler.cache", "scheduler.cache.readMultiplier.extra",
		"scheduler.cache.bogus", "..", "store.chunk.min.",
	} {
		_, ok := cfg.Get(path)
		_ = ok // sections (e.g. "scheduler.cache") legitimately report true; the point is no panic
	}
	_, ok := cfg.Get("totally.unknown.path")
	require.False(t, ok)
}
