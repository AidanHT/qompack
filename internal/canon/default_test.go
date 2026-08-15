package canon_test

import (
	"testing"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/canon/canontest"
	"github.com/qompack/qompack/internal/config"
	"github.com/stretchr/testify/require"
)

// wantBuiltinNames is the registration order 00-ARCHITECTURE.md §5.6 fixes for Default. It is
// written out here rather than read from the package so that a reordering shows up as a failing
// test rather than as a tautology — registration order is the tie-breaker Registry.Run uses for
// equal-length matches at the same offset, and it is what Result.Applied means by "application
// order", so it is behaviour, not an implementation detail.
var wantBuiltinNames = []string{
	"crlf", "ansi", "timestamps", "durations", "pids", "addresses", "tmpPaths",
	"bash", "testrunner", "grep", "glob", "fileread", "webfetch", "git",
}

// TestDefault_Names pins the fourteen built-ins and their order.
func TestDefault_Names(t *testing.T) {
	t.Parallel()

	r := canon.Default(config.Defaults().Store.Canonicalize)
	require.Equal(t, wantBuiltinNames, r.Names())
}

// TestDefault_EveryBuiltinIsMatcher is the guard that makes the ErrNotMatcher skip safe.
//
// Registry.Run silently skips a Canonicalizer that does not implement Matcher, because SP-01's
// frozen conformance suite requires Register to accept one and Run to tolerate it. That is only
// safe if none of the built-ins is ever accidentally in that category — a built-in that lost its
// Matches method would compile, register, and quietly stop canonicalizing anything.
func TestDefault_EveryBuiltinIsMatcher(t *testing.T) {
	t.Parallel()

	r := canon.Default(config.Defaults().Store.Canonicalize)
	for _, c := range r.For("Bash", "") {
		_, err := canon.MatchesOf(c, []byte("probe"), canon.Options{})
		require.NoError(t, err, "built-in %q does not implement Matcher", c.Name())
	}
	for _, c := range r.For("Read", "src/auth.ts") {
		_, err := canon.MatchesOf(c, []byte("probe"), canon.Options{})
		require.NoError(t, err, "built-in %q does not implement Matcher", c.Name())
	}
}

// TestDefault_DisabledKeepsCRLF pins the one canonicalizer that survives
// store.canonicalize.enabled = false.
//
// CRLF→LF normalization is structural per 00-ARCHITECTURE.md §4, not one of Appendix C's six
// optional strip classes. Dropping it here would fork the dedup space between a Windows and a
// POSIX read of the same file, so a project that turned canonicalization off would silently store
// every shared file twice — the exact bloat §6.1 exists to remove.
func TestDefault_DisabledKeepsCRLF(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults().Store.Canonicalize
	cfg.Enabled = false

	r := canon.Default(cfg)
	require.Equal(t, []string{"crlf"}, r.Names())

	res, err := r.Run("Bash", "", []byte("a\r\nb 2024-01-15T10:32:07Z\r\n"), canon.Options{KeepDeltas: true})
	require.NoError(t, err)
	require.Equal(t, "a\nb 2024-01-15T10:32:07Z\n", string(res.Canonical),
		"line endings are still normalized; the timestamp is not stripped")

	restored, err := canon.Restore(res.Canonical, res.Deltas)
	require.NoError(t, err)
	require.Equal(t, "a\r\nb 2024-01-15T10:32:07Z\r\n", string(restored))
}

// TestDefault_ForSelectsPerToolCanonicalizers asserts Applies actually dispatches: the seven
// generic canonicalizers apply to everything, and the per-tool ones only to their own tool names.
func TestDefault_ForSelectsPerToolCanonicalizers(t *testing.T) {
	t.Parallel()

	r := canon.Default(config.Defaults().Store.Canonicalize)

	cases := []struct {
		tool string
		path string
		want []string
	}{
		{"Bash", "", []string{"crlf", "ansi", "timestamps", "durations", "pids", "addresses", "tmpPaths", "bash", "testrunner", "git"}},
		{"PowerShell", "", []string{"crlf", "ansi", "timestamps", "durations", "pids", "addresses", "tmpPaths", "bash", "testrunner", "git"}},
		{"Grep", "", []string{"crlf", "ansi", "timestamps", "durations", "pids", "addresses", "tmpPaths", "grep"}},
		{"Glob", "", []string{"crlf", "ansi", "timestamps", "durations", "pids", "addresses", "tmpPaths", "glob"}},
		{"Read", "src/auth.ts", []string{"crlf", "ansi", "timestamps", "durations", "pids", "addresses", "tmpPaths", "fileread"}},
		{"WebFetch", "", []string{"crlf", "ansi", "timestamps", "durations", "pids", "addresses", "tmpPaths", "webfetch"}},
		{"NotATool", "", []string{"crlf", "ansi", "timestamps", "durations", "pids", "addresses", "tmpPaths"}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()
			got := make([]string, 0, len(tc.want))
			for _, c := range r.For(tc.tool, tc.path) {
				got = append(got, c.Name())
			}
			require.Equal(t, tc.want, got)
		})
	}
}

// TestDefault_AppliesIsCaseInsensitive asserts the tool name matches regardless of spelling, since
// hosts differ on "Bash" versus "bash" and §2.2's tool set is written with initial capitals.
func TestDefault_AppliesIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	r := canon.Default(config.Defaults().Store.Canonicalize)
	upper := namesOf(r.For("BASH", ""))
	lower := namesOf(r.For("bash", ""))
	mixed := namesOf(r.For("Bash", ""))
	require.Equal(t, upper, lower)
	require.Equal(t, upper, mixed)
}

// namesOf renders a Canonicalizer slice as its names.
func namesOf(cs []canon.Canonicalizer) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name()
	}
	return out
}

// TestRegistrySuite_AgainstDefault runs SP-01's frozen conformance suite against the real
// registry. canontest's own suite_test.go already points the suite at canon.Default, but running
// it here too means the owning package's `go test` reports the behaviour block directly rather
// than only through a sibling package — which is what makes the Rule W-1 skip visibly gone.
func TestRegistrySuite_AgainstDefault(t *testing.T) {
	canontest.RunRegistrySuite(t, "canon.Default", func(t *testing.T) canon.Registry {
		return canon.Default(config.Defaults().Store.Canonicalize)
	})
	canontest.RunRegistrySuite(t, "canon.NewRegistry", func(t *testing.T) canon.Registry {
		return canon.NewRegistry()
	})
}
