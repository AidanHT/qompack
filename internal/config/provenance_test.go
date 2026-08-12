package config_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// TestProvenanceRender_AnnotatesLeaves is not one of the named required tests, but Render is
// part of the normative §5.1 surface and config's own coverage floor applies to it too.
func TestProvenanceRender_AnnotatesLeaves(t *testing.T) {
	cfg := config.Defaults()
	prov := config.Provenance{
		"scheduler.cache.readMultiplier": {Origin: config.OriginProjectFile, Location: "/proj/.qompack/config.json:11"},
	}

	var buf bytes.Buffer
	require.NoError(t, prov.Render(cfg, &buf))
	out := buf.String()

	require.Contains(t, out, `"readMultiplier": 0.1`)
	require.Contains(t, out, "// project /proj/.qompack/config.json:11")
	// A leaf with no provenance entry (e.g. a section-only path) gets no trailing comment, but
	// its value is still rendered.
	require.Contains(t, out, `"writeMultiplier": 1.25`)

	// The output is valid JSONC: stripping its comments must parse as strict JSON and round-trip
	// the annotated value.
	var v map[string]any
	require.NoError(t, json.Unmarshal(config.StripJSONC(buf.Bytes()), &v))
	scheduler, ok := v["scheduler"].(map[string]any)
	require.True(t, ok)
	cache, ok := scheduler["cache"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 0.1, cache["readMultiplier"])
}

func TestProvenanceRender_DefaultOrigin(t *testing.T) {
	cfg := config.Defaults()
	prov := config.Provenance{
		"eval.minSessions": {Origin: config.OriginDefault, Location: "config.Defaults()"},
	}
	var buf bytes.Buffer
	require.NoError(t, prov.Render(cfg, &buf))
	require.Contains(t, buf.String(), "// default config.Defaults()")
}

// alwaysFailWriter is an io.Writer that always errors, used to exercise Render's error-return
// paths (a real io.Writer, e.g. a full disk or a closed pipe, can fail mid-write).
type alwaysFailWriter struct{}

func (alwaysFailWriter) Write([]byte) (int, error) { return 0, errors.New("simulated write failure") }

func TestProvenanceRender_WriterErrorPropagates(t *testing.T) {
	prov := config.Provenance{}
	err := prov.Render(config.Defaults(), alwaysFailWriter{})
	require.Error(t, err)
}

func TestOrigin_String(t *testing.T) {
	cases := map[config.Origin]string{
		config.OriginDefault:     "default",
		config.OriginUserFile:    "user",
		config.OriginProjectFile: "project",
		config.OriginEnv:         "env",
		config.OriginFlag:        "flag",
		config.Origin(255):       "unknown",
	}
	for origin, want := range cases {
		require.Equal(t, want, origin.String())
	}
}
