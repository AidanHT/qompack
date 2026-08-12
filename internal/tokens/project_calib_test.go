package tokens_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/tokens"
)

// TestNewForProject_IsolatesProjectsInOneSharedFile is the reason NewForProject exists.
//
// §5.20 puts calibration in ONE shared ~/.qompack/calibration.json keyed by
// sha256(projectRoot).Short(). Keying by the file path instead would give every project on the
// machine the same key and therefore the same factor — which defeats per-project calibration
// entirely, since a Go-heavy project and a prose-heavy one have genuinely different
// characters-per-token. This asserts two projects sharing one file keep distinct factors.
func TestNewForProject_IsolatesProjectsInOneSharedFile(t *testing.T) {
	cfg := config.Defaults()
	home := t.TempDir()
	shared := filepath.Join(home, ".qompack", "calibration.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(shared), 0o700))

	projA := filepath.Join(t.TempDir(), "project-a")
	projB := filepath.Join(t.TempDir(), "project-b")

	// Project A observes twice as many tokens as estimated; project B, half as many.
	a := tokens.NewForProject(cfg, shared, projA)
	b := tokens.NewForProject(cfg, shared, projB)
	for i := 0; i < 25; i++ {
		a.Calibrate(2000, 1000)
		b.Calibrate(500, 1000)
	}

	require.Greater(t, a.Factor(), 1.0, "project A should calibrate upward")
	require.Less(t, b.Factor(), 1.0, "project B should calibrate downward")
	require.Greater(t, a.Factor()-b.Factor(), 0.1,
		"two projects sharing one calibration file must not converge on one factor")

	// Both entries coexist in the single shared file.
	raw, err := os.ReadFile(shared)
	require.NoError(t, err)
	var m map[string]float64
	require.NoError(t, json.Unmarshal(raw, &m))
	require.Len(t, m, 2, "one entry per project, in one file: %s", raw)

	// The keys are exactly §5.20's sha256(projectRoot).Short() form.
	for _, root := range []string{projA, projB} {
		key := core.HashBytes("qompack.tokens.calib.v1", []byte(filepath.Clean(root))).Short()
		require.Contains(t, m, key, "expected an entry keyed by the project root")
		require.Len(t, key, 12)
	}

	// A fresh estimator for each project reads back that project's own factor.
	require.InDelta(t, a.Factor(), tokens.NewForProject(cfg, shared, projA).Factor(), 1e-9)
	require.InDelta(t, b.Factor(), tokens.NewForProject(cfg, shared, projB).Factor(), 1e-9)
}

// TestNewForProject_EmptyRootFallsBackToPath documents the degenerate case: with no project root
// resolved, scoping falls back to the calibration file's own path, which is what plain New does.
func TestNewForProject_EmptyRootFallsBackToPath(t *testing.T) {
	cfg := config.Defaults()
	p := filepath.Join(t.TempDir(), "calibration.json")

	viaEmptyRoot := tokens.NewForProject(cfg, p, "")
	viaEmptyRoot.Calibrate(2000, 1000)

	require.InDelta(t, viaEmptyRoot.Factor(), tokens.New(cfg, p).Factor(), 1e-9,
		"an empty project root must behave exactly like the frozen §5.20 constructor")
}
