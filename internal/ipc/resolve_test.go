package ipc

import (
	"encoding/hex"
	"path"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// noEnv is the getenv every case that must not see XDG_RUNTIME_DIR passes to resolveFor.
func noEnv(string) string { return "" }

// envWith returns a getenv reporting v for key and nothing for anything else.
func envWith(key, v string) func(string) string {
	return func(k string) string {
		if k == key {
			return v
		}
		return ""
	}
}

const (
	testRoot = "C:/Users/dev/proj"
	testUID  = 1000
)

// TestResolveFor_WindowsIsAlwaysANamedPipe asserts §2.4's Windows answer: \\.\pipe\qompack.<hash12>,
// regardless of XDG_RUNTIME_DIR, the temp directory or the uid — none of which mean anything on
// Windows, and all of which would silently produce a Unix socket path if the branch were wrong.
func TestResolveFor_WindowsIsAlwaysANamedPipe(t *testing.T) {
	a, err := resolveFor("windows", envWith(xdgRuntimeDirEnv, "/run/user/1000"), "/tmp", testUID, testRoot)
	require.NoError(t, err)
	require.Equal(t, NamedPipe, a.Kind)

	hash12, _ := endpointHash(testRoot)
	require.Equal(t, `\\.\pipe\qompack.`+hash12, a.Path)
}

// TestResolveFor_PosixPrefersXDGRuntimeDir asserts candidate 1 of §2.4's resolution order.
func TestResolveFor_PosixPrefersXDGRuntimeDir(t *testing.T) {
	a, err := resolveFor("linux", envWith(xdgRuntimeDirEnv, "/run/user/1000"), "/tmp", testUID, testRoot)
	require.NoError(t, err)
	require.Equal(t, UnixSocket, a.Kind)

	hash12, _ := endpointHash(testRoot)
	require.Equal(t, "/run/user/1000/qompack/"+hash12+".sock", a.Path)
}

// TestResolveFor_PosixFallsBackToTempDir asserts candidate 2: with XDG_RUNTIME_DIR unset — the
// normal case on macOS and inside many containers — the socket lives in a per-uid directory under
// the temp dir, which is what keeps two users on one host from colliding.
func TestResolveFor_PosixFallsBackToTempDir(t *testing.T) {
	a, err := resolveFor("darwin", noEnv, "/tmp", testUID, testRoot)
	require.NoError(t, err)
	require.Equal(t, UnixSocket, a.Kind)

	hash12, _ := endpointHash(testRoot)
	require.Equal(t, "/tmp/qompack-"+strconv.Itoa(testUID)+"/"+hash12+".sock", a.Path)
}

// TestResolveFor_BlankXDGIsTreatedAsUnset asserts a whitespace-only XDG_RUNTIME_DIR does not
// produce a socket path rooted at the current directory. An empty environment variable is a common
// artefact of a partially populated container environment, and joining it would silently yield a
// relative path that two different projects could share.
func TestResolveFor_BlankXDGIsTreatedAsUnset(t *testing.T) {
	a, err := resolveFor("linux", envWith(xdgRuntimeDirEnv, "   "), "/tmp", testUID, testRoot)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(a.Path, "/tmp/"), "got %q", a.Path)
}

// TestResolveFor_OverlongXDGSkipsToTheNextCandidate asserts the length budget is applied per
// candidate, in order: an XDG_RUNTIME_DIR long enough to push candidate 1 past sunPathMax is
// skipped in favour of candidate 2, rather than jumping straight to the short fallback.
func TestResolveFor_OverlongXDGSkipsToTheNextCandidate(t *testing.T) {
	longXDG := "/run/" + strings.Repeat("d", sunPathMax)
	a, err := resolveFor("linux", envWith(xdgRuntimeDirEnv, longXDG), "/tmp", testUID, testRoot)
	require.NoError(t, err)

	hash12, _ := endpointHash(testRoot)
	require.Equal(t, "/tmp/qompack-"+strconv.Itoa(testUID)+"/"+hash12+".sock", a.Path)
	require.LessOrEqual(t, len(a.Path), sunPathMax)
}

// TestResolveFor_ShortFallbackWhenNoCandidateFits is §2.4's sun_path escape hatch: when neither
// candidate fits in 100 bytes, the endpoint becomes <tmp>/qp-<hash8>.sock. macOS's sun_path is 104
// bytes, so without this a deeply nested temp directory would make the daemon unreachable with an
// error message about an invalid argument.
func TestResolveFor_ShortFallbackWhenNoCandidateFits(t *testing.T) {
	longTmp := "/var/folders/" + strings.Repeat("t", 60)
	a, err := resolveFor("darwin", noEnv, longTmp, testUID, testRoot)
	require.NoError(t, err)

	_, hash8 := endpointHash(testRoot)
	require.Equal(t, path.Join(longTmp, "qp-"+hash8+".sock"), a.Path)
	require.LessOrEqual(t, len(a.Path), sunPathMax)
}

// TestResolveFor_ReportsWhenEvenTheShortFallbackIsTooLong asserts the one case nothing can fix is
// reported rather than silently returning an address the bind would reject with a bare EINVAL.
func TestResolveFor_ReportsWhenEvenTheShortFallbackIsTooLong(t *testing.T) {
	_, err := resolveFor("linux", noEnv, "/"+strings.Repeat("t", sunPathMax), testUID, testRoot)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fits in")
}

// TestResolve_RejectsAnEmptyProjectRoot asserts an unresolved root is refused rather than
// defaulted: two different projects sharing one endpoint would cross-contaminate their stores,
// which is the worst failure mode this package has.
func TestResolve_RejectsAnEmptyProjectRoot(t *testing.T) {
	for _, root := range []string{"", "   ", "\t"} {
		_, err := Resolve(root)
		require.ErrorIs(t, err, ErrUnresolvedRoot, "root %q must be refused", root)
	}
}

// TestResolve_UsesTheHostPlatform asserts the exported entry point actually reaches the same
// resolution the injected form does, so the testable seam cannot drift from what production calls.
func TestResolve_UsesTheHostPlatform(t *testing.T) {
	a, err := Resolve(testRoot)
	require.NoError(t, err)
	require.Contains(t, []AddrKind{NamedPipe, UnixSocket}, a.Kind)
	require.NotEmpty(t, a.Path)

	hash12, _ := endpointHash(testRoot)
	require.Contains(t, a.Path, hash12)
}

// TestEndpointHash_IsDeterministicAndNormalized asserts §2.4's endpoint naming: the same project
// root always produces the same endpoint, and three spellings of one root produce one endpoint.
// Without the normalization a client invoked with a trailing slash would dial a different daemon
// than one invoked without it.
func TestEndpointHash_IsDeterministicAndNormalized(t *testing.T) {
	want12, want8 := endpointHash(testRoot)

	for _, spelling := range []string{testRoot, testRoot + "/", "C:/Users/dev/./proj", "C:/Users/dev/x/../proj"} {
		got12, got8 := endpointHash(spelling)
		require.Equal(t, want12, got12, "spelling %q must resolve to the same endpoint", spelling)
		require.Equal(t, want8, got8)
	}
}

// TestEndpointHash_SeparatesDistinctRoots asserts two different projects never share an endpoint.
func TestEndpointHash_SeparatesDistinctRoots(t *testing.T) {
	a12, _ := endpointHash("C:/Users/dev/proj-a")
	b12, _ := endpointHash("C:/Users/dev/proj-b")
	require.NotEqual(t, a12, b12)
}

// TestEndpointHash_ShapeIsLowercaseHexOfTheStatedLengths pins the two prefix lengths §2.4 names and
// asserts hash8 is a prefix of hash12 — both are prefixes of one sha256, not two different digests.
func TestEndpointHash_ShapeIsLowercaseHexOfTheStatedLengths(t *testing.T) {
	h12, h8 := endpointHash(testRoot)
	require.Len(t, h12, hash12Len)
	require.Len(t, h8, hash8Len)
	require.True(t, strings.HasPrefix(h12, h8))

	_, err := hex.DecodeString(h12)
	require.NoError(t, err, "the endpoint name must be hex")
	require.Equal(t, strings.ToLower(h12), h12, "the endpoint name must be lowercase hex")
}

// TestSocketPermissionsMatchTheDesign pins §2.4's two modes so a future bind cannot widen them: a
// world-writable socket would let any local user inject tool results into another user's store.
func TestSocketPermissionsMatchTheDesign(t *testing.T) {
	require.Equal(t, 0o700, dirPerm)
	require.Equal(t, 0o600, socketPerm)
}
