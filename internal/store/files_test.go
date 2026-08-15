package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// fileRoot mints a distinct, deterministic root hash per seed, so a test can talk about "version
// 3" without putting real content through the object layer.
func fileRoot(seed string) core.Hash {
	return core.HashBytes(core.DomainRoot, []byte(seed))
}

func TestAppendFileVersion_History(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	const p = "src/auth.ts"

	want := []FileVersion{
		{TS: 100, Root: fileRoot("v1"), Turn: 0, Bytes: 10},
		{TS: 200, Root: fileRoot("v2"), Turn: 1, Bytes: 20},
		{TS: 300, Root: fileRoot("v3"), Turn: 2, Bytes: 30},
	}
	for _, v := range want {
		require.NoError(t, f.s.AppendFileVersion(ctx, p, v))
	}

	got, err := f.s.FileHistory(ctx, p)
	require.NoError(t, err)
	require.Equal(t, want, got, "history is ascending by timestamp")
	require.Len(t, indexLines(t, f.root, filesLogFile), 3)

	// The log, not memory, is the truth.
	s2 := f.reopen(t)
	got, err = s2.FileHistory(ctx, p)
	require.NoError(t, err)
	require.Equal(t, want, got)

	_, err = s2.FileHistory(ctx, "src/never-seen.ts")
	require.ErrorIs(t, err, core.ErrNotFound)
}

func TestAppendFileVersion_Idempotent(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	const p = "src/auth.ts"

	v := FileVersion{TS: 100, Root: fileRoot("v1"), Turn: 0, Bytes: 10}
	require.NoError(t, f.s.AppendFileVersion(ctx, p, v))
	require.NoError(t, f.s.AppendFileVersion(ctx, p, v))
	require.Len(t, indexLines(t, f.root, filesLogFile), 1,
		"the same root at the same turn is one observation, however many times the WAL replays it")

	// The SAME root at a DIFFERENT turn is a second observation and IS recorded.
	require.NoError(t, f.s.AppendFileVersion(ctx, p, FileVersion{TS: 200, Root: v.Root, Turn: 1, Bytes: 10}))
	require.Len(t, indexLines(t, f.root, filesLogFile), 2)

	// A version with no root hash is refused: it could never answer a staleness comparison.
	require.ErrorIs(t, f.s.AppendFileVersion(ctx, p, FileVersion{TS: 300, Turn: 2}), core.ErrNotFound)
}

func TestFileAt(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	const p = "src/auth.ts"

	for _, v := range []FileVersion{
		{TS: 100, Root: fileRoot("v1"), Turn: 0},
		{TS: 200, Root: fileRoot("v2"), Turn: 1},
		{TS: 300, Root: fileRoot("v3"), Turn: 2},
	} {
		require.NoError(t, f.s.AppendFileVersion(ctx, p, v))
	}

	got, err := f.s.FileAt(ctx, p, time.UnixMilli(250))
	require.NoError(t, err)
	require.Equal(t, fileRoot("v2"), got.Root, "the last version at or before the query instant")

	got, err = f.s.FileAt(ctx, p, time.UnixMilli(200))
	require.NoError(t, err)
	require.Equal(t, fileRoot("v2"), got.Root, "the bound is inclusive")

	_, err = f.s.FileAt(ctx, p, time.UnixMilli(50))
	require.ErrorIs(t, err, core.ErrNotFound, "no version existed that early")

	got, err = f.s.FileAt(ctx, p, time.Time{})
	require.NoError(t, err)
	require.Equal(t, fileRoot("v3"), got.Root, "a zero instant means the latest version")
}

func TestChangedSince_Changed(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	const p = "src/auth.ts"

	require.NoError(t, f.s.AppendFileVersion(ctx, p, FileVersion{TS: 100, Root: fileRoot("v1")}))
	dep := core.Dep{Path: p, Hash: fileRoot("v1")}

	require.NoError(t, f.s.AppendFileVersion(ctx, p, FileVersion{TS: 200, Root: fileRoot("v2"), Turn: 1}))

	changed, err := f.s.ChangedSince(ctx, []core.Dep{dep})
	require.NoError(t, err)
	require.Equal(t, []core.Dep{dep}, changed,
		"a dep pinned to the OLD hash is changed once a newer version exists")
}

func TestChangedSince_Unchanged(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	const p = "src/auth.ts"

	require.NoError(t, f.s.AppendFileVersion(ctx, p, FileVersion{TS: 100, Root: fileRoot("v1")}))

	changed, err := f.s.ChangedSince(ctx, []core.Dep{{Path: p, Hash: fileRoot("v1")}})
	require.NoError(t, err)
	require.Empty(t, changed)
}

// TestChangedSince_UnknownPath pins rule 3, which is the one SP-09 codes against: a path with no
// recorded version is UNCHANGED, not changed. Reporting it changed would flip every
// scope:"project" elimination to stale on the first session against a fresh clone.
func TestChangedSince_UnknownPath(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()

	dep := core.Dep{Path: "src/never-stored.ts", Hash: fileRoot("whatever")}
	changed, err := f.s.ChangedSince(ctx, []core.Dep{dep})
	require.NoError(t, err)
	require.Empty(t, changed, "absence of a recorded version is not a difference")

	require.Equal(t, int64(1), f.reg.Counter("store.changed_since.unknown_path").Value(),
		"the situation must stay observable to /qompack:status even though it is not a change")
}

func TestChangedSince_KeyNormalization(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()

	require.NoError(t, f.s.AppendFileVersion(ctx, "src/auth.ts",
		FileVersion{TS: 100, Root: fileRoot("v1")}))

	// A dep naming the same file in a different-but-equivalent spelling must still match, or the
	// staleness comparison silently degrades to "unknown path" for every caller that spells a path
	// slightly differently.
	spellings := []string{"src/auth.ts", "./src/auth.ts"}
	if paths.DefaultFold() {
		spellings = append(spellings, "SRC/Auth.TS")
	}
	if runtime.GOOS == "windows" {
		spellings = append(spellings, `.\SRC\Auth.TS`, `src\auth.ts`)
	}

	for _, spelling := range spellings {
		t.Run(spelling, func(t *testing.T) {
			// Matching means the CURRENT hash is found, so a stale dep is reported changed...
			changed, err := f.s.ChangedSince(ctx, []core.Dep{{Path: spelling, Hash: fileRoot("old")}})
			require.NoError(t, err)
			require.Len(t, changed, 1, "spelling %q must resolve to the recorded path", spelling)

			// ...and a current one is not.
			changed, err = f.s.ChangedSince(ctx, []core.Dep{{Path: spelling, Hash: fileRoot("v1")}})
			require.NoError(t, err)
			require.Empty(t, changed)
		})
	}
}

func TestChangedSince_PreservesInputOrder(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()

	paths5 := []string{"a.ts", "b.ts", "c.ts", "d.ts", "e.ts"}
	for _, p := range paths5 {
		require.NoError(t, f.s.AppendFileVersion(ctx, p, FileVersion{TS: 100, Root: fileRoot("cur:" + p)}))
	}

	// Deps at indices 1 and 3 are stale; the rest are current.
	deps := make([]core.Dep, len(paths5))
	for i, p := range paths5 {
		h := fileRoot("cur:" + p)
		if i == 1 || i == 3 {
			h = fileRoot("stale:" + p)
		}
		deps[i] = core.Dep{Path: p, Hash: h}
	}

	changed, err := f.s.ChangedSince(ctx, deps)
	require.NoError(t, err)
	require.Equal(t, []core.Dep{deps[1], deps[3]}, changed,
		"the result preserves input order and carries the INPUT Dep values unchanged")
}

func TestFilesJSON_MaterializedByFlush(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()

	// Two paths, two versions each, appended out of lexicographic order on purpose.
	require.NoError(t, f.s.AppendFileVersion(ctx, "src/zeta.ts", FileVersion{TS: 100, Root: fileRoot("z1"), Turn: 0, Bytes: 1}))
	require.NoError(t, f.s.AppendFileVersion(ctx, "src/alpha.ts", FileVersion{TS: 150, Root: fileRoot("a1"), Turn: 1, Bytes: 2}))
	require.NoError(t, f.s.AppendFileVersion(ctx, "src/zeta.ts", FileVersion{TS: 200, Root: fileRoot("z2"), Turn: 2, Bytes: 3}))
	require.NoError(t, f.s.AppendFileVersion(ctx, "src/alpha.ts", FileVersion{TS: 250, Root: fileRoot("a2"), Turn: 3, Bytes: 4}))

	viewPath := filepath.Join(paths.Of(f.root).Index, filesViewNam)
	_, err := os.Stat(paths.Long(viewPath))
	require.True(t, os.IsNotExist(err), "the view is materialized by Flush, never on the hot path")

	require.NoError(t, f.s.materializeFilesJSON())

	b, err := os.ReadFile(paths.Long(viewPath))
	require.NoError(t, err)

	var view struct {
		Version   int                      `json:"version"`
		Generated core.UnixMilli           `json:"generated"`
		Files     map[string][]FileVersion `json:"files"`
	}
	require.NoError(t, json.Unmarshal(b, &view))
	require.Equal(t, indexRecordVersion, view.Version)
	require.Equal(t, core.UnixMilli(idxEpoch.UnixMilli()), view.Generated)
	require.Len(t, view.Files, 2)
	require.Equal(t, []FileVersion{
		{TS: 150, Root: fileRoot("a1"), Turn: 1, Bytes: 2},
		{TS: 250, Root: fileRoot("a2"), Turn: 3, Bytes: 4},
	}, view.Files["src/alpha.ts"], "versions ascend by timestamp")

	// encoding/json emits object keys in sorted order, which is the lexicographic path ordering
	// the view is specified to have; assert it on the raw bytes rather than the decoded map.
	require.Less(t, indexOf(b, `"src/alpha.ts"`), indexOf(b, `"src/zeta.ts"`), "paths are sorted lexicographically")
}

// indexOf returns the byte offset of needle in haystack, or -1.
func indexOf(haystack []byte, needle string) int {
	return bytesIndex(haystack, []byte(needle))
}

func bytesIndex(h, n []byte) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if string(h[i:i+len(n)]) == string(n) {
			return i
		}
	}
	return -1
}

// TestPropChangedSinceIsExactlyHashInequality asserts, over random dep sets, that ChangedSince
// returns exactly the deps with a RECORDED and DIFFERENT newest root — never more, never fewer.
//
// One store serves every iteration (opening a project per iteration would dominate the runtime);
// each iteration namespaces its paths with a counter so earlier iterations cannot alias later
// ones, and the expectation is recomputed from the store's own FileHistory rather than from the
// draws, so the property is checked against the recorded truth rather than against a shadow model
// that could drift from it.
func TestPropChangedSinceIsExactlyHashInequality(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	iteration := 0

	rapid.Check(t, func(rt *rapid.T) {
		iteration++
		prefix := "prop" + itoa(iteration) + "/"

		n := rapid.IntRange(0, 12).Draw(rt, "n")
		deps := make([]core.Dep, 0, n)
		for i := 0; i < n; i++ {
			p := prefix + rapid.StringMatching(`[a-z]{1,4}`).Draw(rt, "name") + ".ts"
			cur := fileRoot(p + ":cur")
			if rapid.Bool().Draw(rt, "recorded") {
				if err := f.s.AppendFileVersion(ctx, p,
					FileVersion{TS: core.UnixMilli(i + 1), Root: cur, Turn: core.TurnIndex(i)}); err != nil {
					rt.Fatalf("AppendFileVersion: %v", err)
				}
			}
			h := cur
			if !rapid.Bool().Draw(rt, "same") {
				h = fileRoot(p + ":other")
			}
			deps = append(deps, core.Dep{Path: p, Hash: h})
		}

		got, err := f.s.ChangedSince(ctx, deps)
		if err != nil {
			rt.Fatalf("ChangedSince: %v", err)
		}

		var expect []core.Dep
		for _, d := range deps {
			hist, herr := f.s.FileHistory(ctx, d.Path)
			if herr != nil || len(hist) == 0 {
				continue // rule 3: no history is not a difference
			}
			if hist[len(hist)-1].Root != d.Hash {
				expect = append(expect, d)
			}
		}
		if len(expect) != len(got) {
			rt.Fatalf("ChangedSince returned %d deps, want %d", len(got), len(expect))
		}
		for i := range expect {
			if expect[i] != got[i] {
				rt.Fatalf("dep %d: got %+v, want %+v", i, got[i], expect[i])
			}
		}
	})
}

// itoa is strconv.Itoa under a shorter name, used only to namespace property-test paths.
func itoa(n int) string { return strconv.Itoa(n) }
