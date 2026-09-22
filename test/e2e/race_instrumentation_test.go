package e2e

import (
	"crypto/sha256"
	"debug/buildinfo"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// The ordinary e2e -race flag instruments only the test process. The V4/V6
// product-child gate supplies GOFLAGS=-race (inherited by Build's go build) and
// requires this independent inspection of the actual executable under test.
func TestE2E_RequiredProductChildRaceInstrumentation(t *testing.T) {
	if os.Getenv("QOMPACK_REQUIRE_CHILD_RACE") != "1" {
		t.Skip("platform: product-child race mode not requested; run with GOFLAGS=-race, CGO_ENABLED=1 and QOMPACK_REQUIRE_CHILD_RACE=1")
	}
	bin := Build(t)
	info, err := buildinfo.ReadFile(bin)
	require.NoError(t, err)
	race := false
	for _, setting := range info.Settings {
		if setting.Key == "-race" && setting.Value == "true" {
			race = true
		}
	}
	require.True(t, race, "the product child itself must carry race instrumentation")
	f, err := os.Open(bin)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	h := sha256.New()
	_, err = io.Copy(h, f)
	require.NoError(t, err)
	t.Logf("instrumented product child: sha256=%x go=%s", h.Sum(nil), info.GoVersion)
}
