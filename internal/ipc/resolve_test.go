package ipc

import (
	"encoding/hex"
	"path"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// noEnv is the getenv every case that must not see any environment variable passes to resolveFor.
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

// envMap returns a getenv serving kv and nothing for any key kv does not name — used by the
// override tests, which need both XDG_RUNTIME_DIR and QOMPACK_IPC_ADDR available at once.
func envMap(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

const (
	testRoot = "C:/Users/dev/proj"
	testUID  = 1000
)

// TestResolveFor_WindowsIsAlwaysANamedPipe asserts §2.4's Windows answer: \\.\pipe\qompack.<hash12>,
// regardless of XDG_RUNTIME_DIR, the temp directory or the uid — none of which mean anything on
// Windows, and all of which would silently produce a Unix socket path if the branch were wrong.
func TestResolveFor_WindowsIsAlwaysANamedPipe(t *testing.T) {
	a, err := resolveFor(goosWindows, envWith(xdgRuntimeDirEnv, "/run/user/1000"), "/tmp", testUID, testRoot)
	require.NoError(t, err)
	require.Equal(t, NamedPipe, a.Kind)

	hash12, _ := projectHashFor(goosWindows, testRoot)
	require.Equal(t, `\\.\pipe\qompack.`+hash12, a.Path)
}

// TestResolveFor_PosixPrefersXDGRuntimeDir asserts candidate 1 of §2.4's resolution order.
func TestResolveFor_PosixPrefersXDGRuntimeDir(t *testing.T) {
	a, err := resolveFor("linux", envWith(xdgRuntimeDirEnv, "/run/user/1000"), "/tmp", testUID, testRoot)
	require.NoError(t, err)
	require.Equal(t, UnixSocket, a.Kind)

	hash12, _ := projectHashFor("linux", testRoot)
	require.Equal(t, "/run/user/1000/qompack/"+hash12+".sock", a.Path)
}

// TestResolveFor_PosixFallsBackToTempDir asserts candidate 2: with XDG_RUNTIME_DIR unset — the
// normal case on macOS and inside many containers — the socket lives in a per-uid directory under
// the temp dir, which is what keeps two users on one host from colliding.
func TestResolveFor_PosixFallsBackToTempDir(t *testing.T) {
	a, err := resolveFor(goosDarwin, noEnv, "/tmp", testUID, testRoot)
	require.NoError(t, err)
	require.Equal(t, UnixSocket, a.Kind)

	hash12, _ := projectHashFor(goosDarwin, testRoot)
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

// TestResolveFor_RelativeXDGIsIgnored asserts the added absoluteness guard: XDG_RUNTIME_DIR must be
// a POSIX-style absolute path (starts with "/") or it is treated exactly like unset, falling
// through to candidate 2 rather than joining a relative path onto an unknown current directory.
func TestResolveFor_RelativeXDGIsIgnored(t *testing.T) {
	a, err := resolveFor("linux", envWith(xdgRuntimeDirEnv, "run/user/1000"), "/tmp", testUID, testRoot)
	require.NoError(t, err)

	hash12, _ := projectHashFor("linux", testRoot)
	require.Equal(t, "/tmp/qompack-"+strconv.Itoa(testUID)+"/"+hash12+".sock", a.Path)
}

// TestResolveFor_OverlongXDGSkipsToTheNextCandidate asserts the length budget is applied per
// candidate, in order: an XDG_RUNTIME_DIR long enough to push candidate 1 past sunPathMax is
// skipped in favour of candidate 2, rather than jumping straight to the short fallback.
func TestResolveFor_OverlongXDGSkipsToTheNextCandidate(t *testing.T) {
	longXDG := "/run/" + strings.Repeat("d", sunPathMax)
	a, err := resolveFor("linux", envWith(xdgRuntimeDirEnv, longXDG), "/tmp", testUID, testRoot)
	require.NoError(t, err)

	hash12, _ := projectHashFor("linux", testRoot)
	require.Equal(t, "/tmp/qompack-"+strconv.Itoa(testUID)+"/"+hash12+".sock", a.Path)
	require.LessOrEqual(t, len(a.Path), sunPathMax)
}

// TestResolveFor_ShortFallbackWhenNoCandidateFits is §2.4's sun_path escape hatch: when neither
// candidate fits in 100 bytes, the endpoint becomes <tmp>/qp-<hash8>.sock. macOS's sun_path is 104
// bytes, so without this a deeply nested temp directory would make the daemon unreachable with an
// error message about an invalid argument.
func TestResolveFor_ShortFallbackWhenNoCandidateFits(t *testing.T) {
	longTmp := "/var/folders/" + strings.Repeat("t", 60)
	a, err := resolveFor(goosDarwin, noEnv, longTmp, testUID, testRoot)
	require.NoError(t, err)

	_, hash8 := projectHashFor(goosDarwin, testRoot)
	require.Equal(t, path.Join(longTmp, "qp-"+hash8+".sock"), a.Path)
	require.LessOrEqual(t, len(a.Path), sunPathMax)
}

// TestResolveFor_ReportsWhenEvenTheShortFallbackIsTooLong asserts the one case nothing can fix is
// reported — as the exported ErrAddrTooLong sentinel — rather than silently returning an address
// the bind would reject with a bare EINVAL.
func TestResolveFor_ReportsWhenEvenTheShortFallbackIsTooLong(t *testing.T) {
	_, err := resolveFor("linux", noEnv, "/"+strings.Repeat("t", sunPathMax), testUID, testRoot)
	require.ErrorIs(t, err, ErrAddrTooLong)
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

	hash12 := ProjectHash12(testRoot)
	require.Contains(t, a.Path, hash12)
}

// TestProjectHash12_IsDeterministicAndNormalized asserts §2.4's endpoint naming: the same project
// root always produces the same endpoint, and several spellings of one root produce one endpoint.
// Without the normalization a client invoked with a trailing slash would dial a different daemon
// than one invoked without it.
func TestProjectHash12_IsDeterministicAndNormalized(t *testing.T) {
	want := ProjectHash12(testRoot)

	for _, spelling := range []string{testRoot, testRoot + "/", "C:/Users/dev/./proj", "C:/Users/dev/x/../proj"} {
		require.Equal(t, want, ProjectHash12(spelling), "spelling %q must resolve to the same endpoint", spelling)
	}
}

// TestProjectHash8_IsAPrefixOfProjectHash12 asserts hash8 and hash12 are two prefixes of one
// sha256, not two different digests.
func TestProjectHash8_IsAPrefixOfProjectHash12(t *testing.T) {
	h12 := ProjectHash12(testRoot)
	h8 := ProjectHash8(testRoot)
	require.True(t, strings.HasPrefix(h12, h8))
	require.Len(t, h8, hash8Len)
}

// TestProjectHash12_SeparatesDistinctRoots asserts two different projects never share an endpoint.
func TestProjectHash12_SeparatesDistinctRoots(t *testing.T) {
	require.NotEqual(t, ProjectHash12("C:/Users/dev/proj-a"), ProjectHash12("C:/Users/dev/proj-b"))
}

// TestProjectHash12_ShapeIsLowercaseHexOfTheStatedLength pins the 12-hex-char prefix §2.4 names.
func TestProjectHash12_ShapeIsLowercaseHexOfTheStatedLength(t *testing.T) {
	h12 := ProjectHash12(testRoot)
	require.Len(t, h12, hash12Len)

	_, err := hex.DecodeString(h12)
	require.NoError(t, err, "the endpoint name must be hex")
	require.Equal(t, strings.ToLower(h12), h12, "the endpoint name must be lowercase hex")
}

// TestNormalizeRoot_CaseFoldsOnlyOnWindowsAndDarwin asserts the case-fold decision mirrors
// paths.DefaultFold: two spellings differing only in case hash identically on windows/darwin, and
// differently everywhere else — using the injected goos so this is testable on any host.
func TestNormalizeRoot_CaseFoldsOnlyOnWindowsAndDarwin(t *testing.T) {
	lower := normalizeRoot("linux", "/proj/foo")
	upper := normalizeRoot("linux", "/PROJ/FOO")
	require.NotEqual(t, lower, upper, "linux must not fold case")

	for _, goos := range []string{goosWindows, goosDarwin} {
		require.Equal(t, normalizeRoot(goos, "/proj/foo"), normalizeRoot(goos, "/PROJ/FOO"), "%s must fold case", goos)
	}
}

// TestNormalizeRoot_StripsTrailingSlashExceptBareRoot asserts the trailing-slash rule: a plain
// POSIX root and a Windows drive root both keep their single separator rather than being stripped
// down to an empty string or a bare drive letter, either of which would change the path's meaning.
func TestNormalizeRoot_StripsTrailingSlashExceptBareRoot(t *testing.T) {
	require.Equal(t, "/", stripTrailingSlash("/"))
	require.Equal(t, "C:/", stripTrailingSlash("C:/"))
	require.Equal(t, "/proj/foo", stripTrailingSlash("/proj/foo/"))
	require.Equal(t, "/proj/foo", stripTrailingSlash("/proj/foo"))
}

// TestStripTrailingSlash_EveryVolumeRootShapeIsHostIndependent widens the case above to the rest of
// the volume-root spellings Windows' own filepath.Clean can emit with a trailing separator — a UNC
// share root, the two device-path roots, and a UNC path spelled as a device — because the decision
// used to come from filepath.VolumeName, which is compiled per-GOOS and reports no volume at all
// for any of these on linux and macOS. That is what made the drive-root case above fail on those
// two platforms, and every shape here fails the same way for the same reason. It matters beyond the
// test: this string is hashed into the name of an IPC endpoint (§2.4), so a per-host answer is a
// per-host socket name for one project.
//
// The second table is the other half of the same contract — a volume root keeps its separator and
// nothing else does, including the three near misses: "//host/" names a host with no share, "//?/"
// a device prefix with no component behind it, and "proj/foo/" carries no volume at all.
func TestStripTrailingSlash_EveryVolumeRootShapeIsHostIndependent(t *testing.T) {
	for _, root := range []string{
		"/", "C:/", "c:/", "D:/",
		"//host/share/",
		"//?/C:/", "//./pipe/",
		"//./UNC/host/share/",
	} {
		require.Equal(t, root, stripTrailingSlash(root), "%q is a bare root: its separator must survive", root)
	}

	for _, tc := range []struct{ in, want string }{
		{"C:/proj/", "C:/proj"},
		{"//host/share/proj/", "//host/share/proj"},
		{"//?/C:/proj/", "//?/C:/proj"},
		{"//./UNC/host/share/proj/", "//./UNC/host/share/proj"},
		{"//host/", "//host"},
		{"//?/", "//?"},
		{"/proj/foo/", "/proj/foo"},
		{"proj/foo/", "proj/foo"},
	} {
		require.Equal(t, tc.want, stripTrailingSlash(tc.in), "%q is not a bare root: its separator must go", tc.in)
	}
}

// TestWindowsVolumeLen_MatchesWindowsOnEveryHost pins the volume grammar stripTrailingSlash decides
// from. Every want here is the length filepath.VolumeName reports for that path on a Windows build,
// so the table doubles as the proof that replacing that call changed no Windows answer — it is
// asserted on every platform because that is the whole point of not calling filepath.VolumeName.
//
// "//./UNC" is the boundary case: it matches the `\\.\UNC` prefix with nothing behind it, so the
// host scan starts one byte past the end of the string and must find nothing rather than run off it.
func TestWindowsVolumeLen_MatchesWindowsOnEveryHost(t *testing.T) {
	for _, tc := range []struct {
		p    string
		want int
	}{
		{"", 0},
		{"proj", 0},
		{"proj/foo", 0},
		{"/", 0},
		{"/proj/foo", 0},
		{"//", 2},
		{"C:", 2},
		{"C:/proj", 2},
		{"//host/share", 12},
		{"//host/share/proj", 12},
		{"//?/C:", 6},
		{"//?/C:/proj", 6},
		{"//./UNC", 7},
		{"//./UNC/host/share/proj", 18},
	} {
		require.Equal(t, tc.want, windowsVolumeLen(tc.p), "volume name of %q", tc.p)
	}
}

// TestResolveFor_QompackIPCAddrOverridesEverything asserts QOMPACK_IPC_ADDR wins over both the
// normal resolution AND an unresolvable (empty) project root — it is the escape hatch tests and CI
// use to keep sockets inside a temp directory, and it must never depend on hashing a real root.
func TestResolveFor_QompackIPCAddrOverridesEverything(t *testing.T) {
	getenv := envMap(map[string]string{qompackIPCAddrEnv: "unix:/tmp/qompack-test/x.sock"})

	a, err := resolveFor("linux", getenv, "/tmp", testUID, testRoot)
	require.NoError(t, err)
	require.Equal(t, Addr{Kind: UnixSocket, Path: "/tmp/qompack-test/x.sock"}, a)

	// It overrides even an otherwise-refused empty root.
	a, err = resolveFor("linux", getenv, "/tmp", testUID, "")
	require.NoError(t, err)
	require.Equal(t, Addr{Kind: UnixSocket, Path: "/tmp/qompack-test/x.sock"}, a)
}

// TestResolveFor_QompackIPCAddrPipeScheme asserts the pipe: scheme is honoured on any goos — a test
// harness may want a fixed pipe name regardless of platform.
func TestResolveFor_QompackIPCAddrPipeScheme(t *testing.T) {
	getenv := envWith(qompackIPCAddrEnv, `pipe:\\.\pipe\qompack-test`)
	a, err := resolveFor("linux", getenv, "/tmp", testUID, testRoot)
	require.NoError(t, err)
	require.Equal(t, Addr{Kind: NamedPipe, Path: `\\.\pipe\qompack-test`}, a)
}

// TestResolveFor_QompackIPCAddrStillGuardsSunPath asserts the override's unix: form is still
// subject to the 100-byte sun_path budget — it is a convenience, not a way to bypass the guard.
func TestResolveFor_QompackIPCAddrStillGuardsSunPath(t *testing.T) {
	over := "unix:/" + strings.Repeat("t", sunPathMax)
	getenv := envWith(qompackIPCAddrEnv, over)

	// Malformed/refused override falls through to normal resolution rather than failing the hot
	// path on a bad environment variable.
	a, err := resolveFor("linux", getenv, "/tmp", testUID, testRoot)
	require.NoError(t, err)
	require.Equal(t, UnixSocket, a.Kind)
	hash12, _ := projectHashFor("linux", testRoot)
	require.Contains(t, a.Path, hash12, "an overlong override must be ignored, not honoured truncated")
}

// TestResolveFor_QompackIPCAddrMalformedIsIgnored asserts an unparseable override never fails the
// hot path: resolution falls through to the normal chain silently.
func TestResolveFor_QompackIPCAddrMalformedIsIgnored(t *testing.T) {
	for _, bad := range []string{"bogus", "pipe:", "unix:", "tcp:127.0.0.1:1234"} {
		getenv := envWith(qompackIPCAddrEnv, bad)
		a, err := resolveFor("linux", getenv, "/tmp", testUID, testRoot)
		require.NoError(t, err, "override %q must not error", bad)
		require.Equal(t, UnixSocket, a.Kind, "override %q must fall through to normal resolution", bad)
	}
}

// TestSocketPermissionsMatchTheDesign pins §2.4's two modes so a future bind cannot widen them: a
// world-writable socket would let any local user inject tool results into another user's store.
func TestSocketPermissionsMatchTheDesign(t *testing.T) {
	require.Equal(t, 0o700, dirPerm)
	require.Equal(t, 0o600, socketPerm)
}
