package testutil

import "strings"

// The five path classes 00-ARCHITECTURE.md §6.2 requires every path-handling test to survive.
// They are named so a failing assertion says which class broke, and exported so a consumer can
// assert on one of them specifically rather than re-deriving the literal.
const (
	// SpacePathFile has spaces in both a directory component and the filename — the class that
	// breaks any code path that builds a command line by string concatenation.
	SpacePathFile = "src/my folder/a b.ts"
	// CRLFFile carries Windows line endings, so anything that indexes into file content by byte
	// offset (spans, symbol extraction, chunk boundaries) has to survive the extra byte per line.
	CRLFFile = "src/crlf.ts"
	// ReadOnlyFile is the fixture a test chmods to 0o444 after creation. WindowsHostileFiles
	// returns contents only, so applying the mode is the caller's step; it is named here so no
	// caller has to guess which of the entries is meant to become read-only.
	ReadOnlyFile = "src/readonly.ts"
	// UpperCaseFile and LowerCaseFile differ only in case. On a case-insensitive filesystem —
	// the default on both Windows and macOS — creating both yields ONE file, and a test must
	// assert that outcome explicitly rather than try to force two files into existence.
	UpperCaseFile = "src/Foo.ts"
	LowerCaseFile = "src/foo.ts"
)

// The >260-character fixture's shape: twelve nested directories of 24 characters each, plus a
// 40-character filename. On Windows that clears MAX_PATH by a wide margin even before the temp
// root is prepended, which is the whole point — it is the fixture that proves paths.Long's \\?\
// prefixing is actually wired into every open, stat and rename the store performs.
const (
	longPathDepth   = 12
	longPathDirLen  = 24
	longPathNameLen = 40
)

// LongPathFile is the >260-character fixture's project-relative path. It is derived, not typed
// out, so the three constants above remain the single description of its shape.
var LongPathFile = longPathName()

// longPathName builds LongPathFile: longPathDepth directories of longPathDirLen characters, then
// a filename of longPathNameLen characters ending in ".ts".
func longPathName() string {
	var b strings.Builder
	for i := range longPathDepth {
		b.WriteString(padTo("deep"+string(rune('a'+i)), longPathDirLen))
		b.WriteByte('/')
	}
	b.WriteString(padTo("verylongfilename", longPathNameLen-len(".ts")) + ".ts")
	return b.String()
}

// padTo right-pads s with 'x' until it is exactly n characters long. It panics rather than
// silently truncating if s is already longer, because a fixture whose length no longer matches
// its documented shape is a broken fixture.
func padTo(s string, n int) string {
	if len(s) > n {
		panic("testutil: fixture path component " + s + " is longer than its declared width")
	}
	return s + strings.Repeat("x", n-len(s))
}

// WindowsHostileFiles returns the §6.2 fixture set as project-relative path → content, ready to
// hand to NewProject's WithFiles option or to (*Project).WithFiles.
//
// The five classes are: a path with spaces, a path over 260 characters, a case-colliding pair, a
// CRLF file, and a file meant to be chmod'd 0o444 by the caller (see ReadOnlyFile — the map
// carries contents, not modes).
//
// The case-colliding pair is the interesting one. On Windows and macOS the two entries collapse
// into a single file whose content is whichever of the two was written last; on Linux they are
// two files. A test must assert the outcome its filesystem actually produces rather than fight
// it, which is why the two contents are distinguishable: LowerCaseFile is written after
// UpperCaseFile by (*Project).writeFiles' sorted order, so on a case-insensitive filesystem the
// surviving content is the lower-case one.
func WindowsHostileFiles() map[string]string {
	return map[string]string{
		SpacePathFile: "export const spaced = 1;\n",
		LongPathFile:  "export const deep = 1;\n",
		UpperCaseFile: "export const which = \"upper\";\n",
		LowerCaseFile: "export const which = \"lower\";\n",
		CRLFFile:      "export const crlf = 1;\r\nexport const second = 2;\r\n",
		ReadOnlyFile:  "export const readonly = 1;\n",
	}
}
