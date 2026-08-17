package dag

import (
	"time"

	"github.com/qompack/qompack/internal/core"
)

// fixedClock is a core.Clock frozen at one instant, for the two tests that need deps.jsonl
// timestamps to be reproducible.
//
// It exists rather than reusing testutil.NewFakeClock because internal/testutil is a composition
// root: importrules.go permits it to import anything and permits nothing to import it. Nothing
// enforced that for a _test.go file, so this package's tests imported it anyway and it compiled —
// until SP-05 gave internal/daemon a dependency on internal/dag in the same wave. testutil imports
// cli, cli imports daemon, daemon imports dag, and an in-package test of dag that imports testutil
// closes the loop:
//
//	imports internal/testutil from compact_test.go
//	imports internal/cli from project.go
//	imports internal/daemon from daemon.go
//	imports internal/dag from options.go: import cycle not allowed in test
//
// Neither branch is wrong on its own and neither file conflicted. The lesson is the one the import
// rule already states: a leaf package does not reach into a composition root, and "it is only a
// test file" is not an exception, it is just an exception nothing was checking. Two methods of a
// two-method interface are cheaper than the coupling.
type fixedClock struct{ at time.Time }

func newFixedClock(at time.Time) core.Clock { return fixedClock{at: at.Round(0)} }

func (c fixedClock) Now() time.Time { return c.at }

func (c fixedClock) Since(t time.Time) time.Duration { return c.at.Sub(t) }

// testEpoch is the instant testutil.Epoch names, repeated here for the same reason as fixedClock.
// Keep the two in step: a golden recorded under one and read under the other would differ in every
// timestamp for no reason a reader could see.
var testEpoch = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
