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

// goosWindows is runtime.GOOS's spelling for Windows, named so resolveFor's platform branch reads
// as a decision rather than as a magic string.
const goosWindows = "windows"

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

// ErrUnresolvedRoot is returned by Resolve for an empty project root. It is a programming error
// rather than a runtime condition — paths.Resolve produces the root, and it never returns an empty
// one — so it is reported rather than papered over with a default endpoint that two different
// projects would then share.
var ErrUnresolvedRoot = errors.New("ipc: project root must not be empty")

// Resolve returns the local endpoint for projectRoot, exactly as 00-ARCHITECTURE.md §2.4 specifies
// it.
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

// resolveFor is Resolve with every environmental input injected, so both platform branches and all
// three POSIX candidates are testable on any host. Resolve itself is the one-line binding of the
// real environment to this function.
//
// POSIX paths are joined with path.Join rather than filepath.Join deliberately: a Unix socket path
// is always slash-separated, and using the host's separator would produce backslashes when this
// branch is exercised from a Windows test.
func resolveFor(goos string, getenv func(string) string, tempDir string, uid int, projectRoot string) (Addr, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return Addr{}, ErrUnresolvedRoot
	}
	hash12, hash8 := endpointHash(projectRoot)

	if goos == goosWindows {
		// A named pipe name has no length problem worth guarding: the whole path is 9 + 8 + 12
		// bytes, and the pipe namespace is flat rather than filesystem-backed.
		return Addr{Kind: NamedPipe, Path: windowsPipePrefix + pipeName + hash12}, nil
	}

	tmp := filepath.ToSlash(tempDir)
	var candidates []string
	if xdg := getenv(xdgRuntimeDirEnv); strings.TrimSpace(xdg) != "" {
		candidates = append(candidates, path.Join(filepath.ToSlash(xdg), sockDirName, hash12+sockExt))
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
		return Addr{}, fmt.Errorf("ipc: Resolve: no socket path for %s fits in %d bytes (shortest candidate %q is %d)",
			projectRoot, sunPathMax, short, len(short))
	}
	return Addr{Kind: UnixSocket, Path: short}, nil
}

// endpointHash derives the endpoint name from the project root, exactly as §2.4 words it: hash12 is
// the first 12 hex chars of sha256(normalizedAbsProjectRoot), hash8 the first 8.
//
// Undomained on purpose — see the domain registry note in core/hash.go. This hash names a socket,
// it does not address content, so it deliberately does not share the domain-separated construction
// core.HashBytes applies to everything that ends up in the store.
func endpointHash(projectRoot string) (hash12, hash8 string) {
	sum := sha256.Sum256([]byte(filepath.ToSlash(filepath.Clean(projectRoot))))
	h := hex.EncodeToString(sum[:])
	return h[:hash12Len], h[:hash8Len]
}
