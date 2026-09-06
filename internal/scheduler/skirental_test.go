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

// TestSkiRental_ThresholdTracksConfig proves the break-even genuinely moves when r/w move,
// rather than being pinned to the §5.1 worked example: at r=0.2, w=1.0 the break-even is w/r=5,
// a different threshold than the 12.5 case above would give the same expectedReads value.
func TestSkiRental_ThresholdTracksConfig(t *testing.T) {
	require.True(t, SkiRentalShouldWrite(6, 0.2, 1.0))
	require.False(t, SkiRentalShouldWrite(5, 0.2, 1.0))
	require.False(t, SkiRentalShouldWrite(-100, 0.2, 1.0), "negative expected reads can never clear a positive threshold")
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
