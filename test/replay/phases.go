package main

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/eval"
)

// Context is everything a phase-exit check can see.
type Context struct {
	Report          eval.Report
	Driver          DriverReport
	Cfg             config.Config
	CanonicalFirst  []byte
	CanonicalSecond []byte
	Corpus          eval.CorpusManifest
	Growth          eval.GrowthResult
	Sketch          eval.SketchHealth
}

// phaseChecks holds one function per merged phase, and the driver runs every entry at or below
// --phase on every pull request.
//
// This is the mechanism behind §11.3's "every phase gate runs the full replay suite": once a phase
// has landed, its exit criterion is re-asserted forever, so a later wave cannot quietly undo it.
// Later subplans APPEND an entry here — 1 for the store, 2 for negative knowledge, and so on —
// rather than inventing a new gate. See docs/adr/0002-replay-methodology.md.
var phaseChecks = map[int]func(Context) error{
	0: phase0,
}

// phase0 is §10 Phase 0's exit criterion: "a single number for stock behaviour, reproducible
// across at least 20 real sessions."
//
// The committed number is computed over the 24-session synthetic corpus, and the report says so in
// its corpusTier field. The real-session number comes from the same driver over an imported
// recorded corpus and is a scheduled, documented step rather than a CI one; ADR 0002 names the
// command and who runs it.
func phase0(c Context) error {
	if c.Report.Sessions < c.Cfg.Eval.MinSessions {
		return fmt.Errorf("phase 0: %d sessions replayed, eval.minSessions requires %d",
			c.Report.Sessions, c.Cfg.Eval.MinSessions)
	}
	if _, ok := c.Report.Policies[baselinePolicyName]; !ok {
		return errors.New(`phase 0: no "stock" policy in the report; ` +
			"the baseline number is stock behaviour by definition")
	}
	if !bytes.Equal(c.CanonicalFirst, c.CanonicalSecond) {
		return fmt.Errorf("phase 0: replay is not reproducible; first and second runs differ:\n%s",
			firstDiffLine(c.CanonicalFirst, c.CanonicalSecond))
	}
	return nil
}

// baselinePolicyName is the policy the Phase 0 number describes.
const baselinePolicyName = "stock"

// runPhaseChecks runs every registered check at or below phase, in ascending order.
func runPhaseChecks(c Context, phase int) []error {
	levels := make([]int, 0, len(phaseChecks))
	for level := range phaseChecks {
		if level <= phase {
			levels = append(levels, level)
		}
	}
	sort.Ints(levels)

	var errs []error
	for _, level := range levels {
		if err := phaseChecks[level](c); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
