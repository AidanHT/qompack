//go:build !race

package dag_test

// raceEnabled reports whether this test binary was built with -race.
//
// Go exposes the coverage mode at runtime through testing.CoverMode() but offers no equivalent for
// the race detector, so the build tag is the only way to ask. The pair of one-line files is the
// standard idiom for it; see bench_test.go's budgetFor for why the wall-clock gates need to know.
const raceEnabled = false
