package scheduler

import (
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSkiRentalShouldWrite: at r=0.1, w=1.25 the break-even is w/r = 12.5 expected reads. The
// w=0 row is the declared behaviour change over the shipped body, which reported "always write"
// for a free cache write that cannot be free.
func TestSkiRentalShouldWrite(t *testing.T) {
	require.False(t, SkiRentalShouldWrite(12, 0.1, 1.25))
	require.False(t, SkiRentalShouldWrite(12.5, 0.1, 1.25), "strictly greater than the threshold")
	require.True(t, SkiRentalShouldWrite(12.6, 0.1, 1.25))

	require.False(t, SkiRentalShouldWrite(100, 0, 1.25), "r=0 makes the ratio undefined")
	require.False(t, SkiRentalShouldWrite(100, 0.1, 0), "w=0 is not a free write")
	require.False(t, SkiRentalShouldWrite(100, -0.1, 1.25))
	require.False(t, SkiRentalShouldWrite(100, 0.1, -1.25))
}

// TestSkiRental_ComputedNotLiteral proves the write threshold is computed as w/r rather than
// hardcoded: at r=0.1, w=1.25 the break-even is w/r=12.5, so 13 expected reads should write and
// 12 should not. A non-positive r must never write, since the ratio is then undefined.
func TestSkiRental_ComputedNotLiteral(t *testing.T) {
	require.True(t, SkiRentalShouldWrite(13, 0.1, 1.25))
	require.False(t, SkiRentalShouldWrite(12, 0.1, 1.25))
	require.False(t, SkiRentalShouldWrite(1, 0, 1.25))
}

// TestSkiRentalThreshold_TracksRegimeNotConfig proves the break-even is driven by the resolved
// cache REGIME, not by cfg.Cache.WriteMultiplier read directly.
//
// This is the seam V4-VERIFY NC-6 settled. The previous spelling of this test
// (TestSkiRental_ThresholdTracksConfig) fed r and w to SkiRentalShouldWrite as bare literals,
// which cannot distinguish "w came from config" from "w came from the regime" — the two answers
// the row's two numbers depend on. Config is an INPUT to regime selection (cacheregime.go builds
// the five-minute rung entirely from cfg.Cache), never a bypass: at the one-hour TTL the regime
// reports HostOneHourWriteMultiplier while cfg.Cache.WriteMultiplier stays at Appendix C's 1.25,
// and it is the regime's number the threshold must move with.
//
// Hence the two numbers of Qompack.md §5.6 / Appendix A: w/r == 12.5 at the five-minute regime
// (r=0.1, w=1.25) and w/r == 20 at the one-hour regime (r=0.1, w=2.0). The ≈12.5 in §5.6 is the
// five-minute figure and is not the only one.
func TestSkiRentalThreshold_TracksRegimeNotConfig(t *testing.T) {
	cfg := baseCfg()
	require.Equal(t, 1.25, cfg.Cache.WriteMultiplier, "Appendix C's floor is the config input, unchanged in both regimes")

	fiveMin := ResolveCacheRegime(envOf(map[string]string{"FORCE_PROMPT_CACHING_5M": "1"}), cfg, "", false, HostOneHourTTLSeconds)
	oneHour := ResolveCacheRegime(envOf(map[string]string{"ENABLE_PROMPT_CACHING_1H": "1"}), cfg, "", false, HostOneHourTTLSeconds)

	// w is regime-derived. The five-minute rung passes Appendix C's floor through; the one-hour
	// rung reports the documented 2x while the config value is untouched.
	require.Equal(t, cfg.Cache.WriteMultiplier, fiveMin.WriteMultiplier)
	require.Equal(t, HostOneHourWriteMultiplier, oneHour.WriteMultiplier)
	require.NotEqual(t, cfg.Cache.WriteMultiplier, oneHour.WriteMultiplier,
		"reading w from cfg.Cache.WriteMultiplier would collapse the two regimes onto one threshold")

	// Threshold 12.5 under the five-minute regime: 12 and 12.5 do not write, 12.6 does.
	require.False(t, SkiRentalShouldWrite(12, fiveMin.ReadMultiplier, fiveMin.WriteMultiplier))
	require.False(t, SkiRentalShouldWrite(12.5, fiveMin.ReadMultiplier, fiveMin.WriteMultiplier), "strictly greater than the threshold")
	require.True(t, SkiRentalShouldWrite(12.6, fiveMin.ReadMultiplier, fiveMin.WriteMultiplier))

	// Threshold 20 under the one-hour regime, from the SAME config: 12.6 no longer writes.
	require.False(t, SkiRentalShouldWrite(12.6, oneHour.ReadMultiplier, oneHour.WriteMultiplier),
		"the one-hour write premium moves the break-even from 12.5 to 20")
	require.False(t, SkiRentalShouldWrite(20, oneHour.ReadMultiplier, oneHour.WriteMultiplier))
	require.True(t, SkiRentalShouldWrite(20.1, oneHour.ReadMultiplier, oneHour.WriteMultiplier))

	// A disabled cache is neither: no read discount and no write premium, so the break-even is 1.
	disabled := ResolveCacheRegime(envOf(map[string]string{"DISABLE_PROMPT_CACHING": "1"}), cfg, "", false, HostOneHourTTLSeconds)
	require.Equal(t, 1.0, disabled.WriteMultiplier)
	require.False(t, SkiRentalShouldWrite(1, disabled.ReadMultiplier, disabled.WriteMultiplier))
	require.True(t, SkiRentalShouldWrite(1.1, disabled.ReadMultiplier, disabled.WriteMultiplier))

	require.False(t, SkiRentalShouldWrite(-100, fiveMin.ReadMultiplier, fiveMin.WriteMultiplier),
		"negative expected reads can never clear a positive threshold")
}

// TestSkiRental_ThresholdLiteralOnlyInTests is the grep test the plan asks for: the literal 12.5
// appears in no production file of this package. It tokenises the sources rather than searching
// text so a doc comment quoting §5.6's "≈12.5" cannot trip it while a real literal cannot hide.
func TestSkiRental_ThresholdLiteralOnlyInTests(t *testing.T) {
	for _, path := range packageSourceFiles(t) {
		src, err := os.ReadFile(path)
		require.NoError(t, err)
		fset := token.NewFileSet()
		file := fset.AddFile(path, fset.Base(), len(src))
		var s scanner.Scanner
		s.Init(file, src, nil, 0)
		for {
			pos, tok, lit := s.Scan()
			if tok == token.EOF {
				break
			}
			if tok != token.FLOAT {
				continue
			}
			v, err := strconv.ParseFloat(lit, 64)
			require.NoError(t, err)
			require.NotEqual(t, 12.5, v, "%s: the ski-rental threshold must be computed as w/r, never written as a literal (%s)", filepath.Base(path), fset.Position(pos))
		}
	}
}
