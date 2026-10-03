//go:build windows

package hostperm

import (
	"errors"
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
// Only the entries that exist can be expanded, so a deleted file's own short name stays short; its
// surviving parents are still expanded. unresolved reports that such a kept segment has the shape
// of an 8.3 name (shortShaped): its long name, which a rule may name, cannot be established, and
// Evaluate refuses the path rather than judge it on the short spelling alone.
func osAlias(p string) (alias string, unresolved bool) {
	full, err := syscall.FullPath(p)
	if err != nil {
		full = p
	}
	full, unresolved = longName(stripStreams(filepath.Clean(full)))
	if full == p {
		return "", unresolved
	}
	return full, unresolved
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
// unresolved reports whether a kept segment is shortShaped.
func longName(p string) (string, bool) {
	if l, ok := getLongPathName(p); ok {
		return l, false
	}
	dir := filepath.Dir(p)
	if dir == p {
		return p, false
	}
	parent, unresolved := longName(dir)
	base := filepath.Base(p)
	return filepath.Join(parent, base), unresolved || shortShaped(base)
}

// getLongPathName is GetLongPathNameW for p, which fails when any component of p does not exist.
// It is a variable so that a test can stand in for a volume that records 8.3 names: whether a
// volume records them is a system setting, which a test may not change (shortname_seam test).
var getLongPathName = win32LongPathName

// win32LongPathName is GetLongPathNameW. Long paths go through their \\?\ form, and the answer is
// returned without it.
func win32LongPathName(p string) (string, bool) {
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

// osShortName returns p with every segment of its longest existing prefix in its 8.3 spelling
// (GetShortPathNameW), and the rest as written; complete reports whether all of p exists, so that
// every segment's 8.3 name is known. A segment with no 8.3 name keeps its own.
func osShortName(p string) (short string, complete bool) {
	if s, ok := getShortPathName(p); ok {
		return s, true
	}
	dir := filepath.Dir(p)
	if dir == p {
		return p, false
	}
	parent, _ := osShortName(dir)
	return filepath.Join(parent, filepath.Base(p)), false
}

// getShortPathName is GetShortPathNameW for p, which fails when any component of p does not exist.
// It is a variable for the reason getLongPathName is.
var getShortPathName = win32ShortPathName

// win32ShortPathName is GetShortPathNameW. Long paths go through their \?\ form, and the answer is
// returned without it.
func win32ShortPathName(p string) (string, bool) {
	in := paths.Long(p)
	u, err := syscall.UTF16PtrFromString(in)
	if err != nil {
		return "", false
	}
	buf := make([]uint16, syscall.MAX_PATH)
	for {
		n, err := syscall.GetShortPathName(u, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 {
			return "", false
		}
		if int(n) < len(buf) {
			return trimLong(syscall.UTF16ToString(buf[:n]), in != p), true
		}
		buf = make([]uint16, n)
	}
}

// respelled reports whether the Win32 layer would open the clean absolute path p under another
// spelling without consulting the disk: GetFullPathNameW rewrites it (a reserved device name, a
// trailing dot or space), or a segment below the volume carries a stream suffix (stripStreams).
// Such a path is judged on disk by an Evaluator, as osAlias judges it.
func respelled(p string) bool {
	full, err := syscall.FullPath(p)
	return err != nil || full != p || strings.Contains(p[len(filepath.VolumeName(p)):], ":")
}

// listNames returns what dir's listing says about each name in it (Evaluator): each entry's own
// name and its 8.3 alias, case-folded (the Win32 layer opens an entry by either, in any case), and
// whether it is a reparse point (a symlink, a junction, a mount point or any other, which a path
// through it may be redirected by). It is nil when dir cannot be listed. os.ReadDir reports no 8.3
// names, so the directory is read with FindFirstFileW.
func listNames(dir string, _ bool) map[string]entryKind {
	pattern, err := syscall.UTF16PtrFromString(paths.Long(dir) + `\*`)
	if err != nil {
		return nil
	}
	var fd syscall.Win32finddata
	h, err := syscall.FindFirstFile(pattern, &fd)
	if err != nil {
		return nil
	}
	defer func() { _ = syscall.FindClose(h) }()
	names := map[string]entryKind{}
	for {
		var link entryKind
		if fd.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			link = entryLink
		}
		own := strings.ToLower(syscall.UTF16ToString(fd.FileName[:]))
		if own != "." && own != ".." {
			names[own] |= entryOwn | link
			if alias := strings.ToLower(syscall.UTF16ToString(fd.AlternateFileName[:])); alias != "" && alias != own {
				names[alias] |= entryAlias | link
			}
		}
		if err := syscall.FindNextFile(h, &fd); err != nil {
			if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
				return names
			}
			return nil
		}
	}
}
