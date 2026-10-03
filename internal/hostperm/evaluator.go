package hostperm

import (
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Evaluator judges many paths against one RuleSet for one request. A caller that judges thousands
// of candidate spellings in one go (a rehydration reading every piece of every argument summary,
// ADR 0011 §23.5) would otherwise pay Evaluate's disk work for each: the path's full and long names
// and a Readlink of every component, twice over when its root has a second spelling.
//
// Evaluate answers exactly what RuleSet.Evaluate answers for the same path, as the disk stood when
// the request began, and reads the disk far less. Evaluate's spellings of a path are the path as
// written, the name the operating system opens for it (osAlias: the Win32 full path, its streams
// cut, its 8.3 names expanded) and where each of those resolves through links. Below a base
// directory (the project root), each spelling is the base's own spelling (read once, through the
// disk) followed by the rest of the path, and the rest is respelled by the disk only where it names
// an entry that could respell it. So a path below a base is walked down the directories it names,
// each listed once per evaluator, as written and as the Win32 layer normalizes it (syscall.FullPath,
// a string operation), each segment by the entry name it opens (a stream's colon cut on Windows):
//
//   - While each segment names an entry by its own name, and the entry is neither a link nor
//     another reparse point, the segment is spelled on disk as written (up to case, which spellings
//     fold where the platform does): it has no alias and redirects nothing.
//   - At the first segment that names no entry the walk ends: nothing below a missing entry exists,
//     so nothing there can redirect the path, and the rest is kept as written. Where that segment
//     ends in a dot or a space, which the Win32 layer trims from the last segment of each prefix
//     GetLongPathNameW and Readlink open, it must name nothing trimmed either.
//   - Such a path's candidates are then the base's spellings joined to the rest as written and to
//     the rest as normalized, and its 8.3 verdict is osAlias's: a kept segment shaped like an 8.3
//     name has no long name to judge (unresolvedShortName). They are matched without the disk.
//
// Any other path is judged on disk, with link reads memoized for the evaluator's life (linkMemo):
// one that names a link, a reparse point or an entry by its 8.3 alias, one through a directory that
// cannot be listed, one the Win32 layer would normalize out from below its base (a device name), a
// segment with a trailing dot or space that names an entry either way, a segment whose comparison
// with the listing cannot be settled (non-ASCII on Windows when some listed name could equal it
// under the volume's case table, non-ASCII on macOS, whose filesystems also normalize Unicode), a
// `..` the path keeps, and every path while a rule names an 8.3 name it could not expand
// (shortPending) or when the rules judge another platform's paths.
//
// The equivalence is pinned by TestEvaluator_AgreesWithEvaluate and, spelling for spelling, by
// TestEvaluator_LexicalCandidatesAreEvaluatesSpellings. An Evaluator is not safe for concurrent use.
type Evaluator struct {
	rs    *RuleSet
	memo  linkMemo
	bases []*evalBase
	// dirs holds each directory listed so far, nil for one that cannot be listed.
	dirs map[string]map[string]entryKind
	// wide holds, for each directory listed so far, the names in it that hold a non-ASCII rune
	// (Windows only): a segment may name one of them under the volume's case table although no
	// folded name in dirs equals it (lookup).
	wide map[string][]string
	disk int
}

// evalBase is one base directory and its own spellings, read the first time a path below it is
// judged without the disk.
type evalBase struct {
	dir     string
	spelled bool
	// natives holds, in this platform's form, the base as written and where it resolves through
	// links (Evaluate joins those to the rest of a path as written), then the name the operating
	// system opens for the base (osAlias) and where that resolves (joined to the rest as the Win32
	// layer normalizes it). A spelling resolveLinks could not finish (a link cycle) is left out, as
	// spellings leaves it out.
	natives    []baseSpelling
	unresolved bool
}

// baseSpelling is one native spelling of a base, and how Evaluate joins a path's rest to it.
type baseSpelling struct {
	native string
	// normalized: joined to the rest as normalized (osAlias's lineage), not as written.
	normalized bool
	// resolved: a resolveLinks result, which joins the rest one segment at a time with
	// filepath.Join (on Windows, Join puts no separator after a segment ending in a colon).
	resolved bool
}

// entryKind is what a directory listing says about one name in it.
type entryKind uint8

const (
	// entryOwn: the name is an entry's own (long) name.
	entryOwn entryKind = 1 << iota
	// entryAlias: the name is an entry's 8.3 alias, which the operating system opens under its own
	// name.
	entryAlias
	// entryLink: the entry is a symlink, a junction or another reparse point.
	entryLink
)

// Evaluator returns an evaluator for one request against rs, which walks the paths below each of
// bases down the directories they name.
func (rs *RuleSet) Evaluator(bases ...string) *Evaluator {
	e := &Evaluator{rs: rs, memo: linkMemo{}, dirs: map[string]map[string]entryKind{}, wide: map[string][]string{}}
	seen := map[string]bool{}
	for _, b := range bases {
		if b == "" {
			continue
		}
		b = filepath.Clean(b)
		if !seen[b] {
			seen[b] = true
			e.bases = append(e.bases, &evalBase{dir: b})
		}
	}
	return e
}

// Evaluate reports what the rules say about reading the absolute path abs: RuleSet.Evaluate's
// answer, as this evaluator's request sees the disk.
func (e *Evaluator) Evaluate(abs string) Decision {
	if e.rs.Empty() || abs == "" {
		return Decision{Effect: Allow}
	}
	if cands, unresolved, ok := e.lexical(abs); ok {
		if unresolved {
			return Decision{Effect: Deny, Rule: unresolvedShortName}
		}
		return e.rs.decide(cands, nil)
	}
	e.disk++
	return e.rs.evaluate(abs, e.memo)
}

// DiskEvaluations reports how many paths this evaluator judged on disk rather than by walking the
// listings of the directories they name. Listing a directory and reading a base's own spellings are
// not counted.
func (e *Evaluator) DiskEvaluations() int { return e.disk }

// lexical returns abs's candidate spellings, and osAlias's 8.3 verdict on it, when abs lies below a
// base and its walks show nothing on disk can respell it; ok is false when abs must be judged on
// disk.
func (e *Evaluator) lexical(abs string) (cands [][]string, unresolved, ok bool) {
	if e.rs.shortPending || !e.rs.resolve || keepsDotDot(abs) {
		return nil, false, false
	}
	written := filepath.Clean(abs)
	normal, ok := osFullPath(written)
	if !ok {
		return nil, false, false
	}
	for _, b := range e.bases {
		wrel, below := relBelow(b.dir, written)
		if !below {
			continue
		}
		nrel, below := relBelow(b.dir, normal)
		if !below {
			return nil, false, false
		}
		wsegs := strings.Split(wrel, string(filepath.Separator))
		nsegs := strings.Split(nrel, string(filepath.Separator))
		if _, clean := e.walk(b.dir, wsegs); !clean {
			return nil, false, false
		}
		kept, clean := e.walk(b.dir, nsegs)
		if !clean {
			return nil, false, false
		}
		e.spell(b)
		// osAlias cuts the normalized path's streams and expands its 8.3 names; a segment it keeps
		// because it names nothing, shaped like an 8.3 name, has no long name to judge.
		unresolved = b.unresolved
		for i, s := range nsegs {
			if nsegs[i] = entryName(s); i >= kept && unexpandable(nsegs[i]) {
				unresolved = true
			}
		}
		for _, s := range b.natives {
			segs := wsegs
			if s.normalized {
				segs = nsegs
			}
			p := s.native + string(filepath.Separator) + strings.Join(segs, string(filepath.Separator))
			if s.resolved {
				p = filepath.Join(append([]string{s.native}, segs...)...)
			}
			cands, _ = appendDistinct(cands, posixSegments(p, e.rs.goos, e.rs.fold))
		}
		return cands, unresolved, true
	}
	return nil, false, false
}

// spell reads b's own spellings, once: Evaluate's spellings of the base itself (spellings), kept
// apart by whether they derive from the base as written or from the name the operating system
// opens for it, and by whether they are resolveLinks results.
func (e *Evaluator) spell(b *evalBase) {
	if b.spelled {
		return
	}
	b.spelled = true
	alias, unresolved := osAlias(b.dir)
	if alias == "" {
		alias = b.dir
	}
	b.unresolved = unresolved
	for _, s := range []baseSpelling{{native: b.dir}, {native: alias, normalized: true}} {
		b.natives = append(b.natives, s)
		if r, ok := resolveLinks(s.native, e.memo); ok {
			b.natives = append(b.natives, baseSpelling{native: r, normalized: s.normalized, resolved: true})
		}
	}
}

// walk walks segs down from dir, each by the entry name it opens (entryName). It reports, as kept,
// the index of the first segment that names no entry (len(segs) when every one does), and, as
// clean, whether nothing on the way could respell the path: every segment before kept names an
// entry by its own name that is neither a link nor another reparse point, the segment at kept
// names nothing in any spelling the operating system would open it by, and no segment is one the
// listing cannot be compared with (plainSegment).
func (e *Evaluator) walk(dir string, segs []string) (kept int, clean bool) {
	for _, s := range segs {
		if !e.plainSegment(s) {
			return 0, false
		}
	}
	for i, seg := range segs {
		name := entryName(seg)
		if name == "" {
			// An empty entry name (`:stream`) opens the directory itself.
			return 0, false
		}
		if trimmed := trimmedName(name); trimmed != name {
			// The Win32 layer opens this segment trimmed where it ends a prefix, as it does for each
			// prefix GetLongPathNameW and Readlink open: it may only end the walk, naming nothing
			// in either spelling.
			if trimmed == "" || e.lookup(dir, trimmed) != entryNone || e.lookup(dir, name) != entryNone {
				return 0, false
			}
			return i, true
		}
		switch kind := e.lookup(dir, name); {
		case kind == entryNone:
			return i, true
		case kind != entryOwn:
			return 0, false
		}
		dir = filepath.Join(dir, name)
	}
	return len(segs), true
}

// entryNone is lookup's answer for a name that opens no entry; entryUnsure is its answer for a
// name it cannot tell, or a directory that cannot be listed. Neither is a listing's own kind.
const (
	entryNone   entryKind = 0
	entryUnsure entryKind = 0xff
)

// lookup is what dir's listing says about the entry name, read once per directory: its kind, folded
// as the platform folds, entryNone when no entry answers to it, and entryUnsure when the listing
// cannot settle it. On Windows a name holding a non-ASCII rune is compared with every listed name,
// and an ASCII one with every listed non-ASCII name, under sameUnderSomeCaseTable: the volume's own
// case table, not strings.ToLower, decides which names are one name there.
func (e *Evaluator) lookup(dir, name string) entryKind {
	names, ok := e.dirs[dir]
	if !ok {
		names, e.wide[dir] = listNames(dir, e.rs.fold)
		e.dirs[dir] = names
	}
	if names == nil {
		return entryUnsure
	}
	key := name
	if e.rs.fold {
		key = strings.ToLower(name)
	}
	ascii := isASCII(name)
	if kind, found := names[key]; found {
		if !ascii && e.rs.goos == "windows" {
			return entryUnsure
		}
		return kind
	}
	if e.rs.goos != "windows" {
		return entryNone
	}
	if !ascii {
		for listed := range names {
			if sameUnderSomeCaseTable(name, listed) {
				return entryUnsure
			}
		}
	}
	for _, listed := range e.wide[dir] {
		if sameUnderSomeCaseTable(name, listed) {
			return entryUnsure
		}
	}
	return entryNone
}

// sameUnderSomeCaseTable reports whether a and b could be one NTFS name under some volume's case
// table. NTFS compares names one UTF-16 code unit at a time through the volume's upcase table, so
// two names it equates have the same number of code units, and an ASCII unit equates only with an
// ASCII unit that folds to the same letter; a non-ASCII unit may equate with anything. This is
// true of every pair NTFS equates, whatever its table, and false of most other pairs.
func sameUnderSomeCaseTable(a, b string) bool {
	if utf16Len(a) != utf16Len(b) {
		return false
	}
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := range ua {
		x, y := ua[i], ub[i]
		if x >= 0x80 || y >= 0x80 {
			continue
		}
		if lowerASCII(x) != lowerASCII(y) {
			return false
		}
	}
	return true
}

// utf16Len is the number of UTF-16 code units s encodes to.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// lowerASCII folds one ASCII code unit to lower case.
func lowerASCII(c uint16) uint16 {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// isASCII reports whether s holds only ASCII bytes.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// plainSegment reports whether the listing can be compared with seg at all: it is not empty, holds
// no control character, and, on macOS, holds no non-ASCII rune (APFS and HFS+ also equate names
// that differ in Unicode normalization, which no listing comparison here models).
func (e *Evaluator) plainSegment(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f || (c >= 0x80 && e.rs.goos == "darwin") {
			return false
		}
	}
	return true
}

// keepsDotDot reports whether abs holds a `..` segment, which Evaluate's spelling as written keeps
// relative to the volume while filepath.Clean resolves it: the two agree below a base except where
// `..` climbs above the volume's root, which no walk below a base needs to tell apart.
func keepsDotDot(abs string) bool {
	for _, s := range strings.FieldsFunc(abs, func(r rune) bool { return r == '/' || r == filepath.Separator }) {
		if s == ".." {
			return true
		}
	}
	return false
}

// relBelow returns abs relative to dir when abs lies strictly below dir.
func relBelow(dir, abs string) (string, bool) {
	rel, err := filepath.Rel(dir, abs)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}
