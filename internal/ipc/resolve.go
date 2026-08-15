package ipc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// goosWindows and goosDarwin are runtime.GOOS's spellings for Windows and macOS, named so
// resolveFor's platform branch and normalizeRoot's case-fold decision read as decisions rather than
// magic strings.
const (
	goosWindows = "windows"
	goosDarwin  = "darwin"
)

// windowsPipePrefix and pipeName build \\.\pipe\qompack.<hash12> (00-ARCHITECTURE.md §2.4).
const (
	windowsPipePrefix = `\\.\pipe\`
	pipeName          = "qompack."
)

// The POSIX socket path components of §2.4: $XDG_RUNTIME_DIR/qompack/<hash12>.sock, then
// <os.TempDir()>/qompack-<uid>/<hash12>.sock, then <os.TempDir()>/qp-<hash8>.sock.
const (
	xdgRuntimeDirEnv = "XDG_RUNTIME_DIR"
	sockDirName      = "qompack"
	sockDirPrefix    = "qompack-"
	shortSockPrefix  = "qp-"
	sockExt          = ".sock"
)

// sunPathMax is §2.4's 100-byte budget for a Unix socket path. The kernel's own sun_path is 104
// bytes on macOS and 108 on Linux; §2.4 picks the conservative 100 so the same resolution works on
// both, and so a path that fits here fits everywhere Qompack runs.
const sunPathMax = 100

// hash12Len and hash8Len are the two prefix lengths §2.4 names: 12 hex chars for the ordinary
// endpoint name, 8 for the short fallback that has to fit inside sunPathMax.
const (
	hash12Len = 12
	hash8Len  = 8
)

// qompackIPCAddrEnv is the operator/test escape hatch that overrides §2.4's resolution entirely: a
// caller who sets it gets exactly the address it names, never a hashed one. Tests, CI and the bench
// harness use it to keep sockets inside a temp directory that Resolve's own hashing would not
// otherwise reach; production leaves it unset.
const qompackIPCAddrEnv = "QOMPACK_IPC_ADDR"

// The two schemes qompackIPCAddrEnv accepts: "pipe:<name>" names a Windows named pipe verbatim,
// "unix:<path>" names a Unix socket path, still subject to sunPathMax. Anything else — including an
// unset or empty value — is not an override at all, and resolution falls through to the normal
// chain; the hot path must never fail because of a malformed environment variable.
const (
	ipcAddrPipeScheme = "pipe:"
	ipcAddrUnixScheme = "unix:"
)

// ErrUnresolvedRoot is returned by Resolve for an empty project root. It is a programming error
// rather than a runtime condition — paths.Resolve produces the root, and it never returns an empty
// one — so it is reported rather than papered over with a default endpoint that two different
// projects would then share.
var ErrUnresolvedRoot = errors.New("ipc: project root must not be empty")

// ErrAddrTooLong is returned when even §2.4's short fallback (<os.TempDir()>/qp-<hash8>.sock)
// exceeds sunPathMax: nothing left in the resolution chain can produce a bindable path. Callers
// (NewClient) treat it as "spool-only for this process", never as a hook failure.
var ErrAddrTooLong = errors.New("qompack: ipc address exceeds sun_path limit")

// Resolve returns the local endpoint for projectRoot, exactly as 00-ARCHITECTURE.md §2.4 specifies
// it, unless QOMPACK_IPC_ADDR overrides it.
//
// On Windows the answer is always the named pipe \\.\pipe\qompack.<hash12>. On POSIX the candidates
// are tried in §2.4's order — $XDG_RUNTIME_DIR/qompack/<hash12>.sock, then
// <os.TempDir()>/qompack-<uid>/<hash12>.sock — and the first whose full path fits in sunPathMax
// wins; if neither does, the short fallback <os.TempDir()>/qp-<hash8>.sock is used.
//
// Resolve is a pure function of its inputs: it creates no directory and no socket, and it does not
// stat anything. Binding the endpoint — with the directory at 0700 and the socket at 0600, per
// §2.4 — belongs to the Server, so that resolving an address for a status query can never leave a
// directory behind in a project that is not otherwise using Qompack.
func Resolve(projectRoot string) (Addr, error) {
	return resolveFor(runtime.GOOS, os.Getenv, os.TempDir(), os.Getuid(), projectRoot)
}

// resolveFor is Resolve with every environmental input injected, so both platform branches, all
// three POSIX candidates, and the QOMPACK_IPC_ADDR override are testable on any host. Resolve
// itself is the one-line binding of the real environment to this function.
//
// POSIX paths are joined with path.Join rather than filepath.Join deliberately: a Unix socket path
// is always slash-separated, and using the host's separator would produce backslashes when this
// branch is exercised from a Windows test.
func resolveFor(goos string, getenv func(string) string, tempDir string, uid int, projectRoot string) (Addr, error) {
	if a, ok := parseIPCAddrOverride(getenv(qompackIPCAddrEnv)); ok {
		return a, nil
	}
	if strings.TrimSpace(projectRoot) == "" {
		return Addr{}, ErrUnresolvedRoot
	}
	hash12, hash8 := projectHashFor(goos, projectRoot)

	if goos == goosWindows {
		// A named pipe name has no length problem worth guarding: the whole path is 9 + 8 + 12
		// bytes, and the pipe namespace is flat rather than filesystem-backed.
		return Addr{Kind: NamedPipe, Path: windowsPipePrefix + pipeName + hash12}, nil
	}

	tmp := filepath.ToSlash(tempDir)
	var candidates []string
	if xdg := strings.TrimSpace(getenv(xdgRuntimeDirEnv)); xdg != "" && strings.HasPrefix(xdg, "/") {
		candidates = append(candidates, path.Join(xdg, sockDirName, hash12+sockExt))
	}
	candidates = append(candidates, path.Join(tmp, sockDirPrefix+strconv.Itoa(uid), hash12+sockExt))

	for _, c := range candidates {
		if len(c) <= sunPathMax {
			return Addr{Kind: UnixSocket, Path: c}, nil
		}
	}

	// §2.4's last resort. It can itself exceed sunPathMax only if os.TempDir() alone is longer
	// than the budget, which no caller can fix from here — reporting that is more useful than
	// returning an address the bind would reject with a bare EINVAL.
	short := path.Join(tmp, shortSockPrefix+hash8+sockExt)
	if len(short) > sunPathMax {
		return Addr{}, fmt.Errorf("%w: no socket path for %s fits in %d bytes (shortest candidate %q is %d)",
			ErrAddrTooLong, projectRoot, sunPathMax, short, len(short))
	}
	return Addr{Kind: UnixSocket, Path: short}, nil
}

// parseIPCAddrOverride parses QOMPACK_IPC_ADDR's raw value: "pipe:<name>" or "unix:<path>", the
// latter still subject to sunPathMax. ok is false for an unset, empty, or malformed value — the hot
// path never fails on an environment variable, it just ignores one it cannot use.
func parseIPCAddrOverride(raw string) (Addr, bool) {
	switch {
	case strings.HasPrefix(raw, ipcAddrPipeScheme):
		name := strings.TrimPrefix(raw, ipcAddrPipeScheme)
		if name == "" {
			return Addr{}, false
		}
		return Addr{Kind: NamedPipe, Path: name}, true
	case strings.HasPrefix(raw, ipcAddrUnixScheme):
		p := strings.TrimPrefix(raw, ipcAddrUnixScheme)
		if p == "" || len(p) > sunPathMax {
			return Addr{}, false
		}
		return Addr{Kind: UnixSocket, Path: p}, true
	default:
		return Addr{}, false
	}
}

// normalizeRoot canonicalizes a project root before it is hashed, mirroring paths.Key's semantics
// (paths.DefaultFold/KeyFold) so two spellings of one project resolve to one daemon: filepath.Abs,
// then filepath.Clean, then forward slashes, then a trailing slash is stripped (except a bare
// root — POSIX "/" or a Windows drive root like "C:/", either of which would change meaning if
// shortened further), then lowercased iff goos is windows or darwin, whose default filesystems are
// case-insensitive. On any filepath.Abs error, normalization continues from the raw (uncleaned)
// input rather than failing — a hot-path caller must still get a deterministic, if degraded,
// endpoint name.
//
// goos is injected, not read from runtime.GOOS, purely so the case-fold decision is testable
// cross-platform; the filesystem calls (Abs/Clean) always run against the host's own semantics.
func normalizeRoot(goos, root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	slashed := stripTrailingSlash(filepath.ToSlash(filepath.Clean(abs)))
	if goos == goosWindows || goos == goosDarwin {
		slashed = strings.ToLower(slashed)
	}
	return slashed
}

// stripTrailingSlash removes a trailing "/" from an already forward-slashed path, except when the
// result would be a bare root: POSIX "/" itself, or a Windows drive root such as "C:/" whose volume
// name is the entire remaining string once the slash is gone — stripping that would turn an
// absolute path into a different, drive-relative one.
func stripTrailingSlash(p string) string {
	if !strings.HasSuffix(p, "/") || p == "/" {
		return p
	}
	if vol := filepath.VolumeName(p); vol != "" && len(p) == len(vol)+1 {
		return p
	}
	return strings.TrimSuffix(p, "/")
}

// projectHashFor is ProjectHash12/ProjectHash8's shared body, and resolveFor's own hash source, with
// goos injected so the normalization it depends on is testable cross-platform. hash12 is the first
// 12 hex chars of sha256(normalizeRoot(goos, root)), hash8 the first 8 — both prefixes of one
// digest, per §2.4.
//
// Deliberately plain sha256, not core.HashBytes: this hash names a socket, it does not address
// content, so it deliberately does not share the domain-separated construction core.HashBytes
// applies to everything that ends up in the store.
func projectHashFor(goos, root string) (hash12, hash8 string) {
	sum := sha256.Sum256([]byte(normalizeRoot(goos, root)))
	h := hex.EncodeToString(sum[:])
	return h[:hash12Len], h[:hash8Len]
}

// ProjectHash12 returns the 12-hex-char endpoint name §2.4 hashes the (normalized) project root
// into: \\.\pipe\qompack.<hash12> on Windows, <dir>/<hash12>.sock on POSIX.
func ProjectHash12(projectRoot string) string {
	h, _ := projectHashFor(runtime.GOOS, projectRoot)
	return h
}

// ProjectHash8 returns the 8-hex-char short form §2.4 falls back to when the ordinary socket path
// would exceed sunPathMax.
func ProjectHash8(projectRoot string) string {
	_, h := projectHashFor(runtime.GOOS, projectRoot)
	return h
}
