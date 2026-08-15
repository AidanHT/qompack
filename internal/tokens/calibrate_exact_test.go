package tokens_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/tokens"
)

// calibWarmupSamples is the number of Calibrate calls the exact estimator requires before Factor
// leaves the identity. It is restated here, rather than exported, because a test asserting the
// warm-up should fail loudly if the implementation quietly changes it.
const calibWarmupSamples = 5

// TestCalibrate_ClampAndThreshold asserts the warm-up: Factor stays at the identity until enough
// samples have accumulated, then jumps to the clamped EWMA.
func TestCalibrate_ClampAndThreshold(t *testing.T) {
	cfg := config.Defaults()
	est := tokens.NewExact(cfg, "", "")

	for i := 0; i < calibWarmupSamples-1; i++ {
		est.Calibrate(3000, 1000) // ratio 3.0, well above the ceiling
		require.Equal(t, 1.0, est.Factor(), "Factor must stay at the identity during warm-up (sample %d)", i+1)
	}

	est.Calibrate(3000, 1000)
	require.Equal(t, cfg.Runtime.Tokens.CalibrationMax, est.Factor(),
		"once warmed up, a ratio far above the ceiling clamps to runtime.tokens.calibrationMax")
}

// TestCalibrate_LowClamp is TestCalibrate_ClampAndThreshold's mirror: the EWMA is seeded with the
// FIRST ratio, so a consistently low observation sits at the floor from the moment warm-up ends.
func TestCalibrate_LowClamp(t *testing.T) {
	cfg := config.Defaults()
	est := tokens.NewExact(cfg, "", "")

	for i := 0; i < 10; i++ {
		est.Calibrate(200, 1000) // ratio 0.2, well below the floor
	}
	require.Equal(t, cfg.Runtime.Tokens.CalibrationMin, est.Factor(),
		"a consistently low ratio clamps to runtime.tokens.calibrationMin")
}

// TestCalibrate_ReadsConfigNotLiterals overrides all three calibration keys and asserts the clamp
// follows the override — the D11 / §11.6 requirement that no estimator hardcodes a config value.
func TestCalibrate_ReadsConfigNotLiterals(t *testing.T) {
	cfg := config.Defaults()
	cfg.Runtime.Tokens.CalibrationMin = 0.8
	cfg.Runtime.Tokens.CalibrationMax = 1.2
	cfg.Runtime.Tokens.CalibrationAlpha = 0.5

	up := tokens.NewExact(cfg, "", "")
	for i := 0; i < 20; i++ {
		up.Calibrate(5000, 1000)
	}
	require.Equal(t, 1.2, up.Factor(), "the ceiling must follow the configured calibrationMax")

	down := tokens.NewExact(cfg, "", "")
	for i := 0; i < 20; i++ {
		down.Calibrate(100, 1000)
	}
	require.Equal(t, 0.8, down.Factor(), "the floor must follow the configured calibrationMin")
}

// TestCalibrate_NoLiteralClampConstantsInSource is the mechanical half of
// TestCalibrate_ReadsConfigNotLiterals: no non-test source in this package may contain the default
// calibration clamp values as FLOAT LITERALS. Those three numbers have runtime.tokens.* keys, so
// restating one in Go would create a second source of truth (D11, §11.6).
//
// The scan walks the AST rather than grepping text, so that a section reference like "G10.2" in a
// doc comment — which contains the characters "0.2" — is correctly not a float literal.
func TestCalibrate_NoLiteralClampConstantsInSource(t *testing.T) {
	banned := map[string]string{
		"0.6": "runtime.tokens.calibrationMin",
		"1.6": "runtime.tokens.calibrationMax",
		"0.2": "runtime.tokens.calibrationAlpha",
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)
	require.NotEmpty(t, pkgs)

	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.FLOAT {
					return true
				}
				key, isBanned := banned[lit.Value]
				require.False(t, isBanned,
					"%s: float literal %s duplicates %s; read it from config instead (D11, §11.6)",
					filepath.Base(name), lit.Value, key)
				return true
			})
		}
	}
}

// TestCalibrate_ReadsSP01FlatFile asserts the loader still understands SP-01's flat
// {"<key>": <factor>} calibration document, so upgrading does not throw away a calibrated project.
func TestCalibrate_ReadsSP01FlatFile(t *testing.T) {
	cfg := config.Defaults()
	home := t.TempDir()
	calibPath := filepath.Join(paths.Global(home), "calibration.json")
	projectRoot := filepath.Join(t.TempDir(), "proj")

	require.NoError(t, os.MkdirAll(filepath.Dir(calibPath), 0o700))

	// Write the flat shape under the key SP-01's own calibKey derives for this project root.
	seed := tokens.NewForProject(cfg, calibPath, projectRoot)
	for i := 0; i < 20; i++ {
		seed.Calibrate(1250, 1000) // ratio 1.25, inside the clamp
	}
	seeded := seed.Factor()
	require.InDelta(t, 1.25, seeded, 1e-9)

	raw, err := os.ReadFile(calibPath)
	require.NoError(t, err)
	var flat map[string]float64
	require.NoError(t, json.Unmarshal(raw, &flat),
		"the persisted document must remain SP-01's flat {key: factor} shape")
	require.Len(t, flat, 1)

	fresh := tokens.NewForProject(cfg, calibPath, projectRoot)
	require.InDelta(t, seeded, fresh.Factor(), 1e-9,
		"a flat calibration file must be read back as a warmed-up factor")
}

// TestCalibrate_ReadsVersionedFile asserts the loader also accepts the richer versioned document,
// which carries the EWMA and sample count the flat shape cannot express.
func TestCalibrate_ReadsVersionedFile(t *testing.T) {
	cfg := config.Defaults()
	home := t.TempDir()
	calibPath := filepath.Join(paths.Global(home), "calibration.json")
	projectRoot := filepath.Join(t.TempDir(), "proj")

	require.NoError(t, os.MkdirAll(filepath.Dir(calibPath), 0o700))
	key := tokens.CalibKeyForTest(projectRoot)
	doc := `{"version":1,"projects":{"` + key + `":{"factor":1.03,"ewma":1.0312,"samples":42,"updated":1734128400123}}}`
	require.NoError(t, os.WriteFile(calibPath, []byte(doc), 0o600))

	// The versioned document carries the EWMA and the sample count, so the loaded estimator resumes
	// from the EWMA (clamped) rather than from the settled "factor" field — it is already past the
	// warm-up, and the EWMA is the more precise of the two.
	est := tokens.NewForProject(cfg, calibPath, projectRoot)
	require.InDelta(t, 1.0312, est.Factor(), 1e-9, "the versioned document's EWMA must load")
}

// TestCalibrate_Persists asserts a calibrated factor survives a reopen against the same scope.
func TestCalibrate_Persists(t *testing.T) {
	cfg := config.Defaults()
	home := t.TempDir()
	calibPath := filepath.Join(paths.Global(home), "calibration.json")

	est := tokens.NewExact(cfg, calibPath, "")
	for i := 0; i < 20; i++ {
		est.Calibrate(1400, 1000)
	}
	want := est.Factor()
	require.Greater(t, want, 1.0)

	require.FileExists(t, calibPath)
	require.InDelta(t, want, tokens.NewExact(cfg, calibPath, "").Factor(), 1e-9)
}

// TestCalibrate_IgnoresZero asserts non-positive arguments are a complete no-op: no sample
// counted, no EWMA step, no write.
func TestCalibrate_IgnoresZero(t *testing.T) {
	est := tokens.NewExact(config.Defaults(), "", "")
	for i := 0; i < 10; i++ {
		est.Calibrate(0, 100)
		est.Calibrate(100, 0)
		est.Calibrate(-5, 100)
	}
	require.Equal(t, 1.0, est.Factor(),
		"ignored calls must not count toward the warm-up threshold either")
}
