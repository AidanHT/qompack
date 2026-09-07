package checkpoint

// The migration LOOP's own tests.
//
// They are in-package because the migrations map is unexported, and they exist because that loop is
// dead code until the day it is not. At SchemaVersion 1 the map is empty, so every external test
// exercises only the identity path -- and the first real migration would be the first time the
// stepping, the ordering, the advance guard and the version rewrite had ever run, on a user's only
// copy of their session. Registering a throwaway migration here runs that machinery now.

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// withMigrations installs a temporary migration table and restores the real one. The map is
// package-level state, so a test that left an entry behind would silently change what every later
// test in the package is migrating.
func withMigrations(t *testing.T, m map[int]migration) {
	t.Helper()
	saved := migrations
	migrations = m
	t.Cleanup(func() { migrations = saved })
}

// bumpTo rewrites the version key to v and records that it ran, which is what lets the assertions
// below distinguish "the loop stepped once" from "the loop never ran and the document was already
// right".
func bumpTo(v int, log *[]int) migration {
	return func(raw []byte) ([]byte, error) {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
		doc["version"] = v
		*log = append(*log, v)
		return json.Marshal(doc)
	}
}

func TestMigrateStepsThroughEveryVersionInOrder(t *testing.T) {
	var ran []int
	withMigrations(t, map[int]migration{
		1: bumpTo(2, &ran),
		2: bumpTo(3, &ran),
		3: bumpTo(4, &ran),
	})
	out, err := migrateTo([]byte(`{"version":1,"session":"s"}`), 4)
	require.NoError(t, err)
	// Ascending and contiguous: a loop that applied migrations[3] before migrations[1] would
	// hand each step a document shaped for a different version than the one it was written for.
	require.Equal(t, []int{2, 3, 4}, ran)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(out, &doc))
	require.EqualValues(t, 4, doc["version"])
	require.Equal(t, "s", doc["session"], "migrations rewrite the version, not the content around it")
}

func TestMigrateStopsAtTheTargetVersion(t *testing.T) {
	var ran []int
	withMigrations(t, map[int]migration{1: bumpTo(2, &ran), 2: bumpTo(3, &ran)})

	out, err := migrateTo([]byte(`{"version":2}`), 3)
	require.NoError(t, err)
	require.Equal(t, []int{3}, ran, "a document already at v2 skips the v1 step entirely")

	var doc map[string]any
	require.NoError(t, json.Unmarshal(out, &doc))
	require.EqualValues(t, 3, doc["version"])
}

func TestMigrateReportsAMissingStep(t *testing.T) {
	withMigrations(t, map[int]migration{}) // no step from 1 to 2

	_, err := migrateTo([]byte(`{"version":1}`), 2)
	require.Error(t, err)
	require.ErrorIs(t, err, core.ErrContract)
	// A gap in the table is a build defect, not a corrupt file, and the error has to name the
	// version it could not leave or the person reading it has to diff two schema versions to find
	// out which step is missing.
	require.Contains(t, err.Error(), "1")
}

func TestMigrateSurfacesAStepsOwnError(t *testing.T) {
	withMigrations(t, map[int]migration{
		1: func([]byte) ([]byte, error) { return nil, fmt.Errorf("step exploded") },
	})

	_, err := migrateTo([]byte(`{"version":1}`), 2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "step exploded",
		"a failing migration is reported as itself, not flattened into a generic contract error")
}

func TestMigrateRejectsAMigrationThatDoesNotAdvanceTheVersion(t *testing.T) {
	// A migration that leaves the version where it found it is a loop that never ends, and this
	// check is the whole termination proof: it fires on the very iteration that fails to advance,
	// so the error names the cause rather than reporting that some number of iterations went by.
	withMigrations(t, map[int]migration{
		1: func(raw []byte) ([]byte, error) { return raw, nil }, // never bumps
	})

	_, err := migrateTo([]byte(`{"version":1}`), 2)
	require.Error(t, err)
	require.ErrorIs(t, err, core.ErrContract)
	require.Contains(t, err.Error(), "did not advance the version")
}

// TestMigrateWalksAChainLongerThanSixtyFourSteps requires a legitimate migration chain to run to
// completion however long it is. The loop used to carry a second bound — a step counter capped at
// 64 — whose stated purpose was catching a migration that fails to advance the version. The check
// above catches that case first, on the iteration it happens, so the counter was unreachable for
// any schema version up to 64 and the FIRST thing it could ever do was reject a healthy chain with
// "did not converge", on a user's only copy of their session. This walks 69 steps: without the
// counter it converges, with it the run dies at step 64.
func TestMigrateWalksAChainLongerThanSixtyFourSteps(t *testing.T) {
	const target = 70

	var ran []int
	table := make(map[int]migration, target-1)
	for v := 1; v < target; v++ {
		table[v] = bumpTo(v+1, &ran)
	}
	withMigrations(t, table)

	out, err := migrateTo([]byte(`{"version":1,"session":"s"}`), target)
	require.NoError(t, err)
	require.Len(t, ran, target-1, "every registered step must run")

	var doc map[string]any
	require.NoError(t, json.Unmarshal(out, &doc))
	require.EqualValues(t, target, doc["version"])
	require.Equal(t, "s", doc["session"], "migrations rewrite the version, not the content around it")
}
