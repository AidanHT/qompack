//go:build windows

package hostperm

import (
	"path/filepath"
	"strings"
	"syscall"

	"github.com/qompack/qompack/internal/paths"
)

// osAlias returns the name the Win32 layer actually opens for the absolute path p when that name
// is spelled differently from p, and "" when it is not or cannot be established.
//
// Windows opens one file under several spellings (C1.9 review finding 1). GetFullPathNameW drops a
// final segment's trailing dots and spaces and a directory segment's single trailing dot, a
// `:stream` or `::$DATA` suffix names the file's own data, and an 8.3 short name names its long
// one. paths.Norm's EvalSymlinks canonicalizes all of these, so the archive serves the real name's
// history for any of them, and a rule on the real name must hold for every spelling. The OS's own
// normalization is used rather than a re-implementation of its rules: GetFullPathNameW is the
// function the Win32 file APIs apply, and GetLongPathNameW reads each name from the directory.
//
// Only the directories that exist can be expanded, so a deleted file's own short name stays
// short; its surviving parents are still expanded.
func osAlias(p string) string {
	full, err := syscall.FullPath(p)
	if err != nil {
		full = p
	}
	full = longName(stripStreams(filepath.Clean(full)))
	if full == p {
		return ""
	}
	return full
}

// stripStreams cuts every segment below the volume at its first colon. A colon cannot be part of a
// Windows file name; after the volume it only ever introduces a stream of the file named before it.
func stripStreams(p string) string {
	vol := filepath.VolumeName(p)
	rest := p[len(vol):]
	if !strings.Contains(rest, ":") {
		return p
	}
	segs := strings.Split(rest, `\`)
	for i, s := range segs {
		if j := strings.IndexByte(s, ':'); j >= 0 {
			segs[i] = s[:j]
		}
	}
	return vol + strings.Join(segs, `\`)
}

// longName expands the 8.3 names of p's longest existing prefix and keeps the rest as written.
func longName(p string) string {
	if l, ok := getLongPathName(p); ok {
		return l
	}
	dir := filepath.Dir(p)
	if dir == p {
		return p
	}
	return filepath.Join(longName(dir), filepath.Base(p))
}

// getLongPathName is GetLongPathNameW for p, which fails when any component of p does not exist.
// Long paths go through their \\?\ form, and the answer is returned without it.
func getLongPathName(p string) (string, bool) {
	in := paths.Long(p)
	u, err := syscall.UTF16PtrFromString(in)
	if err != nil {
		return "", false
	}
	buf := make([]uint16, syscall.MAX_PATH)
	for {
		n, err := syscall.GetLongPathName(u, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 {
			return "", false
		}
		if int(n) < len(buf) {
			return trimLong(syscall.UTF16ToString(buf[:n]), in != p), true
		}
		buf = make([]uint16, n)
	}
}

// trimLong removes the extended-length prefix paths.Long added, restoring a UNC path's own form.
func trimLong(s string, added bool) string {
	if !added {
		return s
	}
	if rest, ok := strings.CutPrefix(s, `\\?\UNC\`); ok {
		return `\\` + rest
	}
	return strings.TrimPrefix(s, `\\?\`)
}
