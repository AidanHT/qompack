package checkpoint

// SP10-D1 (plans/V2-SP-10-carried-defects.md). BenchmarkFinalize measured 264-284 ms/op against
// exit criterion 1570's 50 ms, and the profile put 88% of it in paths.Norm: ValidatePointers
// handed every file pointer to Norm, Norm resolved the WHOLE absolute path through
// filepath.EvalSymlinks — every component of the project root, twice, one Lstat each and on
// Windows one FindFirstFile each — and so the cost of one pointer was proportional to how deep the
// project root sat on disk, times the number of pointers.
//
// The first test pins the cost model, not the clock. Wall time is what the row measured, but a
// wall-clock assertion on a shared developer host judges the host (ADR 0010), and the property
// that actually went wrong is structural: the per-pointer work must not depend on the depth of the
// root. Allocations are the load-independent proxy for it — every Lstat and every FindFirstFile
// allocates its converted path and its result, so resolving eight more components per pointer
// costs tens of allocations per pointer on every platform, and a check that never resolves the
// root costs none. testing.AllocsPerRun counts mallocs, which no co-load can inflate.
//
// The remaining tests pin what the fast path is NOT allowed to change: the answers paths.Norm gave
// for symlinked components and for a pointer spelled in the wrong case.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

const (
	// pointerCostPointers is how many distinct clean files each root holds; enough that a
	// per-pointer cost shows up as hundreds of allocations, few enough to keep the test cheap.
	pointerCostPointers = 50
	// pointerCostExtraDepth is how many directory components deeper the second root sits than
	// the first. Before the fix each of them cost every pointer two Lstats (and on Windows two
	// FindFirstFiles); after it they cost nothing per pointer.
	pointerCostExtraDepth = 8
	// pointerCostRuns is the sample count handed to testing.AllocsPerRun; the function is
	// deterministic, so a small number is enough and the result is an average anyway.
	pointerCostRuns = 3
	// pointerCostMaxGrowth is the largest per-pointer allocation growth the deeper root may
	// show: under one allocation per pointer, which is to say none.
	pointerCostMaxGrowth = 1.0
	// pointerCostFileBytes and pointerCostMTime are what every materialized file carries; the
	// values are arbitrary, the files are untracked either way.
	pointerCostFileBytes = 12
	pointerCostMTime     = 1767225480
)

// plantGitIndex gives root a .git directory holding the named committed index fixture, the way
// makeGitRoot does for a root it creates itself.
func plantGitIndex(t *testing.T, root, indexFixture string) {
	t.Helper()
	gitDir := filepath.Join(root, ".git")
	require.NoError(t, os.MkdirAll(gitDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "index"), readGitindexFixture(t, indexFixture), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
}

// TestValidatePointersCostDoesNotGrowWithRootDepth is the SP10-D1 characterization: the same
// fifty pointers validated under a root eight directories deeper must not allocate more per
// pointer. It fails on the shipped code by hundreds of allocations per pointer on Windows and by
// tens on Linux, and passes once the root is no longer resolved per pointer.
func TestValidatePointersCostDoesNotGrowWithRootDepth(t *testing.T) {
	base := t.TempDir()
	deepParts := []string{base}
	for i := range pointerCostExtraDepth {
		deepParts = append(deepParts, fmt.Sprintf("d%d", i))
	}
	deep := filepath.Join(deepParts...)
	// The shallow root is ONE component of exactly the length the deep root's eight add, so the
	// two absolute paths are the same length and differ only in how many directories they
	// cross. Length has to be held still: filepath.Join's allocation count is a function of the
	// joined path's length (one fewer once it passes ~128 bytes), which would otherwise show up
	// here as a per-pointer difference that has nothing to do with resolution.
	shallow := filepath.Join(base, strings.Repeat("s", len(deep)-len(base)-1))
	require.Equal(t, len(deep), len(shallow), "the two roots must differ in depth only")

	var ptrs Pointers
	for i := range pointerCostPointers {
		ptrs.Files = append(ptrs.Files, FilePointer{Path: fmt.Sprintf("src/f%02d.ts", i), Why: "cost"})
	}
	for _, root := range []string{shallow, deep} {
		plantGitIndex(t, root, "v2.index")
		for _, fp := range ptrs.Files {
			materialize(t, root, fp.Path, pointerCostFileBytes, pointerCostMTime)
		}
	}

	// Every pointer is present, a plain file and absent from the fixture index, so each run
	// walks the full check — Lstat and index lookup — and reports exactly one untracked drop
	// per pointer. Anything else means the run measured a different code path.
	measure := func(root string) float64 {
		var got []DropEntry
		var err error
		allocs := testing.AllocsPerRun(pointerCostRuns, func() {
			got, err = ValidatePointers(context.Background(), root, ptrs)
		})
		require.NoError(t, err)
		require.Len(t, got, pointerCostPointers)
		for _, d := range got {
			require.Equal(t, dropPointerUntracked, d.Kind)
		}
		return allocs
	}
	shallowAllocs := measure(shallow)
	deepAllocs := measure(deep)

	growth := (deepAllocs - shallowAllocs) / pointerCostPointers
	require.Less(t, growth, pointerCostMaxGrowth,
		"validating %d pointers under a root %d directories deeper allocated %.1f more per pointer "+
			"(%.0f vs %.0f per run): the project root is being resolved once per pointer (SP10-D1)",
		pointerCostPointers, pointerCostExtraDepth, growth, deepAllocs, shallowAllocs)
}

// TestValidatePointersFollowsADirectorySymlinkLikeNorm pins the answer the fast path must not
// change: a pointer that reaches a tracked file through a symlinked directory resolves to the
// file's real index entry, exactly as paths.Norm resolved it, and so is reported clean rather than
// untracked.
func TestValidatePointersFollowsADirectorySymlinkLikeNorm(t *testing.T) {
	root, idx := makeGitRoot(t, "v2.index")
	e, ok := idx[paths.Key("b/c.txt")]
	require.True(t, ok, "fixture sanity: b/c.txt is in the v2 index")
	materializeClean(t, root, e)
	if err := os.Symlink(filepath.Join(root, "b"), filepath.Join(root, "link")); err != nil {
		t.Skipf("platform: symlinks unavailable in this environment: %v", err)
	}

	drops, err := ValidatePointers(context.Background(), root, Pointers{
		Files: []FilePointer{{Path: "link/c.txt", Why: "through a directory symlink"}},
	})
	require.NoError(t, err)
	require.Empty(t, drops, "link/c.txt resolves to b/c.txt, which is tracked and clean")
}

// TestValidatePointersFollowsAFileSymlinkLikeNorm is the same pin for a symlinked FILE: Norm
// resolves the link to its target and the target's index entry is the one compared.
func TestValidatePointersFollowsAFileSymlinkLikeNorm(t *testing.T) {
	root, idx := makeGitRoot(t, "v2.index")
	e, ok := idx[paths.Key("a.txt")]
	require.True(t, ok, "fixture sanity: a.txt is in the v2 index")
	materializeClean(t, root, e)
	if err := os.Symlink(filepath.Join(root, "a.txt"), filepath.Join(root, "alias.txt")); err != nil {
		t.Skipf("platform: symlinks unavailable in this environment: %v", err)
	}

	drops, err := ValidatePointers(context.Background(), root, Pointers{
		Files: []FilePointer{{Path: "alias.txt", Why: "through a file symlink"}},
	})
	require.NoError(t, err)
	require.Empty(t, drops, "alias.txt resolves to a.txt, which is tracked and clean")
}

// TestValidatePointersMatchesTheIndexTheWayTheFilesystemMatchesNames pins the casing half of the
// argument: on a platform whose filesystem matches names case-insensitively (paths.DefaultFold),
// a pointer spelled in the wrong case still finds its file and its index entry — Norm used to
// recover the on-disk spelling, and the index is keyed by paths.Key, which folds it. Where names
// are case-sensitive the file is simply not there, before and after alike.
func TestValidatePointersMatchesTheIndexTheWayTheFilesystemMatchesNames(t *testing.T) {
	root, idx := makeGitRoot(t, "v2.index")
	e, ok := idx[paths.Key("a.txt")]
	require.True(t, ok)
	materializeClean(t, root, e)

	drops, err := ValidatePointers(context.Background(), root, Pointers{
		Files: []FilePointer{{Path: "A.TXT", Why: "wrong case"}},
	})
	require.NoError(t, err)
	if paths.DefaultFold() {
		require.Empty(t, drops, "a case-insensitive filesystem finds a.txt under A.TXT, and Key folds the lookup")
		return
	}
	require.Equal(t, []string{"pointer_missing"}, dropKinds(drops),
		"a case-sensitive filesystem has no A.TXT")
}
