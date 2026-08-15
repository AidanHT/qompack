package core_test

import (
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

func TestNewDecisionID_Format(t *testing.T) {
	re := regexp.MustCompile(`^dec_[0-9a-f]{12}$`)
	id := core.NewDecisionID([]byte("x"))
	require.True(t, re.MatchString(string(id)), "got %q", id)

	require.Equal(t, id, core.NewDecisionID([]byte("x")), "same seed yields the same id")
	require.NotEqual(t, id, core.NewDecisionID([]byte("y")))
}

func TestUnixMilli_TimeRoundTrip(t *testing.T) {
	tm := time.Date(2026, 1, 2, 3, 4, 5, 6_000_000, time.UTC)
	um := core.UnixMilli(tm.UnixMilli())
	require.True(t, um.Time().UTC().Equal(tm), "want %s got %s", tm, um.Time().UTC())
}

func TestNowMilli_UsesClock(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	require.Equal(t, core.UnixMilli(t0.UnixMilli()), core.NowMilli(fixedClock{t0}))
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time                  { return c.t }
func (c fixedClock) Since(u time.Time) time.Duration { return c.t.Sub(u) }

func TestChunkRefAndDep_AreValueTypes(t *testing.T) {
	// ChunkRef and Dep live in core precisely so tokens need not import store and store need not
	// import negknow (§3.2). Comparable value semantics are what let them be map keys.
	a := core.ChunkRef{Hash: core.HashBytes(core.DomainChunk, []byte("a")), Len: 3}
	b := a
	require.Equal(t, a, b)

	d := core.Dep{Path: "src/a.ts", Hash: a.Hash}
	m := map[core.Dep]int{d: 1}
	require.Equal(t, 1, m[core.Dep{Path: "src/a.ts", Hash: a.Hash}])
}
