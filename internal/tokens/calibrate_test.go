package tokens_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/tokens"
)

func TestCalibrate_ClampsAndPersists(t *testing.T) {
	home := t.TempDir()
	calibPath := filepath.Join(paths.Global(home), "calibration.json")

	est := tokens.New(config.Defaults(), calibPath)
	require.Equal(t, 1.0, est.Factor(), "an uncalibrated estimator starts at the identity factor")

	for i := 0; i < 20; i++ {
		est.Calibrate(2000, 1000) // observed 2x estimated, every round
	}
	require.LessOrEqual(t, est.Factor(), 1.6, "Factor must clamp at runtime.tokens.calibrationMax")
	require.Greater(t, est.Factor(), 1.0)

	require.FileExists(t, calibPath)

	fresh := tokens.New(config.Defaults(), calibPath)
	require.Equal(t, est.Factor(), fresh.Factor(), "a fresh Estimator over the same calibPath must read the persisted factor back")
}

func TestCalibrate_ClampsAtMinimumToo(t *testing.T) {
	home := t.TempDir()
	calibPath := filepath.Join(paths.Global(home), "calibration.json")
	est := tokens.New(config.Defaults(), calibPath)

	for i := 0; i < 20; i++ {
		est.Calibrate(1, 1000) // observed far below estimated, every round
	}
	require.GreaterOrEqual(t, est.Factor(), 0.6, "Factor must clamp at runtime.tokens.calibrationMin")
	require.Less(t, est.Factor(), 1.0)
}

func TestCalibrate_IgnoresZeroEstimated(t *testing.T) {
	est := tokens.New(config.Defaults(), "")
	before := est.Factor()
	est.Calibrate(500, 0)
	require.Equal(t, before, est.Factor(), "Calibrate must ignore calls where estimated == 0")
}

func TestCalibrate_EmptyPathIsInMemoryOnly(t *testing.T) {
	est := tokens.New(config.Defaults(), "")
	require.NotPanics(t, func() {
		for i := 0; i < 5; i++ {
			est.Calibrate(2000, 1000)
		}
	})
	require.Greater(t, est.Factor(), 1.0)
}

func TestCalibrate_DistinctCalibPathsDoNotCollide(t *testing.T) {
	home := t.TempDir()
	pathA := filepath.Join(paths.Global(home), "a", "calibration.json")
	pathB := filepath.Join(paths.Global(home), "b", "calibration.json")

	estA := tokens.New(config.Defaults(), pathA)
	for i := 0; i < 20; i++ {
		estA.Calibrate(2000, 1000) // pushes toward the ceiling
	}
	estB := tokens.New(config.Defaults(), pathB)
	require.Equal(t, 1.0, estB.Factor(), "a distinct calibPath must not see another path's calibration")
}

// TestCalibrate_PreservesUnrelatedEntriesInSharedFile proves persist() does a read-merge-write,
// not a blind overwrite: a foreign entry already in the file (standing in for another calibPath's
// previously-persisted factor, exactly the "one shared calibration.json, many projects" shape
// 00-ARCHITECTURE.md §5.20 describes) survives this Estimator's own write untouched.
func TestCalibrate_PreservesUnrelatedEntriesInSharedFile(t *testing.T) {
	home := t.TempDir()
	calibPath := filepath.Join(paths.Global(home), "calibration.json")

	require.NoError(t, os.MkdirAll(filepath.Dir(calibPath), 0o700))
	require.NoError(t, os.WriteFile(calibPath, []byte(`{"someOtherProjectsKey":1.23}`), 0o600))

	est := tokens.New(config.Defaults(), calibPath)
	for i := 0; i < 20; i++ {
		est.Calibrate(2000, 1000)
	}

	b, err := os.ReadFile(calibPath)
	require.NoError(t, err)
	var m map[string]float64
	require.NoError(t, json.Unmarshal(b, &m))
	require.Equal(t, 1.23, m["someOtherProjectsKey"], "an unrelated entry must survive this Estimator's own persist")
	require.Len(t, m, 2, "the file must now hold the foreign entry plus this Estimator's own")
}
