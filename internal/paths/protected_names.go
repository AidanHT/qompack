package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// The §7.4 guard (IsProtected, mayBeProtected, ownerOf) decides from a path's spelling alone whether
// the path names a protected location, so it has to compare spellings the way the filesystem
// underneath resolves names. Two differences from an exact string comparison matter on the platforms
// Qompack ships to, and this file is where the guard accounts for both.
//
//   - Case. Where DefaultFold holds — Windows, whose filesystems fold case, and darwin,
//     conservatively, whose default volume folds and where a case-sensitive volume only makes the
//     guard refuse a little more than it must — CHECKPOINTS\0001.json and checkpoints\0001.json are
//     one file. A guard that compared exactly let WriteAtomic replace a sealed checkpoint and
//     OpenFile(O_TRUNC) empty the pins log through a case variant (w3-paths runs/fix/04). Names are
//     compared through foldKey, which maps every rune to upper and then to lower case, so for the
//     ASCII names compared here the fold covers both NTFS's upcase table and APFS's case folding:
//     the dotless ı and the long ſ upcase to I and S, and the Kelvin sign lowers to k.
//   - NTFS streams. On Windows any path element may carry a stream suffix — name::$DATA is a file's
//     own data, dir::$INDEX_ALLOCATION a directory's index — and it resolves to the same object as
//     the bare name, as an intermediate element too (measured on the development host:
//     checkpoints::$INDEX_ALLOCATION\0001.json reads the sealed checkpoint). IsProtected matched
//     sketches/tried.bloom exactly, so OpenFile(tried.bloom::$DATA, O_TRUNC) emptied the bloom.
//     streamless removes every such suffix before a path is judged, and a named stream (name:s) is
//     refused together with its file.
//
// What a textual guard cannot see is out of its scope on purpose. An 8.3 short name — CHECKP~1 for
// checkpoints, wherever a volume generates short names, which the development host's system volume
// does — resolves only through the filesystem; catching it would cost a GetLongPathName call on
// every guarded open whose path holds a '~', which is every open under an 8.3 TEMP such as
// C:\Users\RUNNER~1\AppData\Local\Temp. Hard links and reparse points are the same class. No product
// caller builds any of these spellings: every protected path comes from a Layout field.

// foldKey returns s in the form foldable names are compared in: s itself where DefaultFold is false,
// and s mapped to upper and then to lower case where it is true.
func foldKey(s string) string {
	if !DefaultFold() {
		return s
	}
	return strings.ToLower(strings.ToUpper(s))
}

// isASCII reports whether every byte of s is ASCII, the case in which strings.EqualFold is exactly
// foldKey's comparison and no allocation is needed to make it.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// sameName reports whether the path element elem spells name, a lower-case ASCII constant, as this
// platform's filesystems compare names. It allocates only for a non-ASCII elem where DefaultFold
// holds.
func sameName(elem, name string) bool {
	switch {
	case !DefaultFold():
		return elem == name
	case isASCII(elem):
		return strings.EqualFold(elem, name)
	default:
		return foldKey(elem) == name
	}
}

// asciiLowerMask turns an upper-case ASCII letter into its lower-case form when ORed in, and leaves
// a lower-case one as it is. containsName uses it only as a cheap first-byte filter.
const asciiLowerMask = 0x20

// containsName reports whether name, a lower-case ASCII constant, occurs anywhere in p's text as
// sameName would compare it: as a substring, not only as a whole element, which is the form
// mayBeProtected's proof needs. It allocates only for a non-ASCII p where DefaultFold holds.
func containsName(p, name string) bool {
	switch {
	case !DefaultFold():
		return strings.Contains(p, name)
	case !isASCII(p):
		return strings.Contains(foldKey(p), name)
	}
	for i := 0; i+len(name) <= len(p); i++ {
		if p[i]|asciiLowerMask == name[0] && strings.EqualFold(p[i:i+len(name)], name) {
			return true
		}
	}
	return false
}

// streamless returns p with the NTFS stream suffix — everything from the first colon — removed from
// every element after its volume name, on Windows, where a colon in an element can only introduce a
// stream. Elsewhere a colon is an ordinary file-name character and p is returned as it is. It
// allocates only when p carries such a suffix.
func streamless(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	vol := len(filepath.VolumeName(p))
	if strings.IndexByte(p[vol:], ':') < 0 {
		return p
	}
	var b strings.Builder
	b.Grow(len(p))
	b.WriteString(p[:vol])
	inStream := false
	for i := vol; i < len(p); i++ {
		switch c := p[i]; {
		case os.IsPathSeparator(c):
			inStream = false
			b.WriteByte(c)
		case c == ':':
			inStream = true
		case !inStream:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// relUnderDot returns target's position below dot when target lies strictly below it, comparing
// names as this platform's filesystems do. Both must be clean. The position keeps target's own
// separators and case; ok is false when target is dot itself or lies outside it.
//
// Where DefaultFold holds and both paths are ASCII, the prefix is compared in place, which costs no
// allocation; otherwise both are related through filepath.Rel, after foldKey where DefaultFold holds.
// filepath.Rel alone would not do: it compares exactly on darwin, so <root>/.QOMPACK/pins/x was
// outside <root>/.qompack there although the filesystem resolves both to one directory.
func relUnderDot(dot, target string) (string, bool) {
	if DefaultFold() && isASCII(dot) && isASCII(target) {
		if len(target) <= len(dot) || !os.IsPathSeparator(target[len(dot)]) ||
			!strings.EqualFold(target[:len(dot)], dot) {
			return "", false
		}
		return target[len(dot)+1:], true
	}
	rel, err := filepath.Rel(foldKey(dot), foldKey(target))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// cutElem splits rel at its first separator — / or this platform's own — into the first element and
// the rest, which is empty when rel has only the one element.
func cutElem(rel string) (first, rest string) {
	for i := 0; i < len(rel); i++ {
		if rel[i] == '/' || os.IsPathSeparator(rel[i]) {
			return rel[:i], rel[i+1:]
		}
	}
	return rel, ""
}
