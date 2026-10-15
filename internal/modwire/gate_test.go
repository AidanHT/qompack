package modwire_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/modwire"
)

// TestMeetsMinHostVersion pins the one version gate. Go is the only gate: a hello whose host is
// below MinHostVersion, or whose version cannot be read, is served with enabled false.
func TestMeetsMinHostVersion(t *testing.T) {
	require.Equal(t, "2.1.300", modwire.MinHostVersion, "the rows below are written against 2.1.300")
	cases := map[string]bool{
		"2.1.300":                                 true,
		"2.1.301":                                 true,
		"2.1.3000":                                true,
		"2.2.0":                                   true,
		"3.0.0":                                   true,
		"2.1.300+build.7":                         true, // build metadata does not order
		"2.1.301-dev.20261014.t101500.sha1a2b3c4": true, // a prerelease of a later release
		"2.1.299":                                 false,
		"2.0.999":                                 false,
		"1.9.999":                                 false,
		"2.1.300-dev":                             false, // a prerelease of X counts as below X
		"2.1.300-dev.20261014.t101500.sha1a2b3c4": false,
		"2.1.300-":                                false,
		"":                                        false,
		"v2.1.300":                                false,
		"2.1":                                     false,
		"2.1.300.1":                               false,
		"2.1.x":                                   false,
		" 2.1.300":                                false,
		"2.1.300 (Claude Code)":                   false,
		"-2.1.300":                                false,
		"2.1.99999999999999999999":                false, // overflow is unparsable, not huge
	}
	for v, want := range cases {
		require.Equal(t, want, modwire.MeetsMinHostVersion(v), "%q", v)
	}
}

// TestHello_TheTwoOffEncodingsAreDistinct pins which answer means what. refused: the project is
// not served at all (home root, no store, switch off) and nothing is written. Served with enabled
// false: the project is served and the hello file is written, but this host is too old or its
// version unreadable.
func TestHello_TheTwoOffEncodingsAreDistinct(t *testing.T) {
	refused := modwire.HelloResponse{Status: modwire.Failure(modwire.ReasonRefused, "")}
	require.NoError(t, refused.Validate())
	got, err := modwire.Encode(refused)
	require.NoError(t, err)
	requireGolden(t, "hello_response_refused.json", got)

	belowMin := modwire.HelloResponse{Status: modwire.Served(), Enabled: false}
	require.NoError(t, belowMin.Validate())
	got, err = modwire.Encode(belowMin)
	require.NoError(t, err)
	requireGolden(t, "hello_response_below_min.json", got)

	require.ErrorIs(t, modwire.HelloResponse{
		Status: modwire.Failure(modwire.ReasonRefused, ""), Enabled: true,
	}.Validate(), modwire.ErrInvalid, "an unserved hello never enables anything")
	require.ErrorIs(t, modwire.HelloResponse{Status: modwire.Status{V: 1, OK: false}}.Validate(),
		modwire.ErrInvalid, "an unserved hello needs a reason")
}
