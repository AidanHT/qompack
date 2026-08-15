package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/qompack/qompack/internal/eval"
)

// loadGrowthSamples reads the store-growth samples the §11.3 guardrail runs over.
//
// In wave 1 they come from testdata/golden/contracts/store/stats-growth.json, the W-2 contract
// fixture: internal/eval is foundation-only and cannot import store, so the shape is fixed here
// and SP-06 swaps in a provider that walks a replayed session through the real store.Stats. The
// fixture stays afterwards as the shape contract, and the wave-2 verification re-runs this same
// check against the real implementation.
func loadGrowthSamples(path string) ([]eval.StatsSample, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // an explicitly named fixture path
	if err != nil {
		return nil, fmt.Errorf("replay: reading growth samples %s: %w", path, err)
	}
	var out []eval.StatsSample
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("replay: parsing growth samples %s: %w", path, err)
	}
	return out, nil
}

// loadSketchHealth reads the §11.4 bloom watch-for numbers, from the same kind of provider seam:
// SP-09 supplies the real negknow health, and until then the committed fixture holds the shape.
func loadSketchHealth(path string) (eval.SketchHealth, error) {
	var out eval.SketchHealth
	if path == "" {
		return out, nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // an explicitly named fixture path
	if err != nil {
		return out, fmt.Errorf("replay: reading sketch health %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("replay: parsing sketch health %s: %w", path, err)
	}
	return out, nil
}

// checkGrowth turns the growth verdict into a gate outcome.
//
// An inconclusive verdict FAILS. An unmeasurable guardrail is not a passing guardrail: reporting
// "we could not tell" as green is how a store that grows linearly ships.
func checkGrowth(path string, result eval.GrowthResult) error {
	if path == "" {
		return nil
	}
	if result.Sublinear {
		return nil
	}
	if result.Reason != "" {
		return fmt.Errorf("store growth guardrail (§11.3) is %s", result.Reason)
	}
	return fmt.Errorf(
		"store growth is not sublinear: exponent %.3f over %d samples spanning %.1fx raw bytes "+
			"(§11.3 requires <= 0.95)", result.Exponent, result.Samples, result.RawSpan)
}
