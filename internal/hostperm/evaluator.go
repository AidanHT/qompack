package hostperm

import (
	"path/filepath"
	"strings"
)

// Evaluator judges many paths against one RuleSet for one request. A caller that judges thousands
// of candidate spellings in one go (a rehydration reading every piece of every argument summary,
// ADR 0011 §23.5) would otherwise pay Evaluate's disk work for each: the path's full and long names
// and a Readlink of every component, twice over when its root has a second spelling.
//
// Evaluate answers exactly what RuleSet.Evaluate answers for the same path, as the disk stood when
// the request began, and reads the disk far less:
//
//   - Below a base directory (the project root), a path is walked down the directories it names,
//     each listed once per evaluator. While each segment names an entry by its own name, and the
//     entry is neither a link nor another reparse point, the segment is spelled on disk as written
//     (up to case, which spellings fold where the platform does): it has no alias and redirects
//     nothing. At the first segment that names no entry, the path names nothing on disk from there
//     on, so nothing there can redirect it either. Such a path's spellings are therefore the base's
//     own (read once, through the disk) followed by the rest as written, and they are matched
//     without touching the disk.
//   - Any other path is judged on disk, with link reads memoized for the evaluator's life
//     (linkMemo): one that names a link, a reparse point or an entry by its 8.3 alias, one through a
//     directory that cannot be listed, one the operating system would itself spell differently (a
//     reserved device name, a trailing dot or space, a colon for a stream), one with an 8.3-shaped
//     or a non-ASCII segment (whose case folding or Unicode normalization may differ from the
//     listing's), and every path while a rule names an 8.3 name it could not expand (shortPending)
//     or when the rules judge another platform's paths.
//
// The equivalence is pinned by TestEvaluator_AgreesWithEvaluate. An Evaluator is not safe for
// concurrent use.
type Evaluator struct {
	rs    *RuleSet
	memo  linkMemo
	bases []*evalBase
	// dirs holds each directory listed so far, nil for one that cannot be listed.
	dirs map[string]map[string]entryKind
	disk int
}

// evalBase is one base directory and its own spellings, read the first time a path below it is
// judged without the disk.
type evalBase struct {
	dir        string
	spelled    bool
	spellings  [][]string
	unresolved bool
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
	e := &Evaluator{rs: rs, memo: linkMemo{}, dirs: map[string]map[string]entryKind{}}
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

// lexical returns abs's candidate spellings, and its base's 8.3 verdict, when abs lies below a base
// and its walk shows nothing on disk can respell it; ok is false when abs must be judged on disk.
func (e *Evaluator) lexical(abs string) (cands [][]string, unresolved, ok bool) {
	if e.rs.shortPending || !e.rs.resolve {
		return nil, false, false
	}
	abs = filepath.Clean(abs)
	if respelled(abs) {
		return nil, false, false
	}
	for _, b := range e.bases {
		rel, below := relBelow(b.dir, abs)
		if !below {
			continue
		}
		segs := strings.Split(rel, string(filepath.Separator))
		if !plainSegments(segs) || !e.spelledAsWritten(b.dir, segs) {
			return nil, false, false
		}
		if !b.spelled {
			b.spelled = true
			b.spellings, _, b.unresolved = spellings(b.dir, e.rs.goos, e.rs.fold, true, e.memo)
		}
		tail := posixSegments(rel, e.rs.goos, e.rs.fold)
		cands = make([][]string, len(b.spellings))
		for k, s := range b.spellings {
			cands[k] = append(append(make([]string, 0, len(s)+len(tail)), s...), tail...)
		}
		return cands, b.unresolved, true
	}
	return nil, false, false
}

// spelledAsWritten walks segs down from dir: it reports true when every segment up to the first
// that names no entry (or to the last) names an entry by its own name, and no such entry is a link
// or another reparse point.
func (e *Evaluator) spelledAsWritten(dir string, segs []string) bool {
	for i, seg := range segs {
		names := e.list(dir)
		if names == nil {
			return false
		}
		if e.rs.fold {
			seg = strings.ToLower(seg)
		}
		kind, found := names[seg]
		switch {
		case !found:
			return true
		case kind != entryOwn:
			return false
		case i == len(segs)-1:
			return true
		}
		dir = filepath.Join(dir, segs[i])
	}
	return true
}

// list is dir's listing, read once (listNames).
func (e *Evaluator) list(dir string) map[string]entryKind {
	names, ok := e.dirs[dir]
	if !ok {
		names = listNames(dir, e.rs.fold)
		e.dirs[dir] = names
	}
	return names
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

// plainSegments reports whether every segment is a name the operating system opens as spelled and
// compares as a listing does: printable ASCII, no colon, no trailing dot or space, and not
// 8.3-shaped.
func plainSegments(segs []string) bool {
	for _, s := range segs {
		if s == "" || strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") || shortShaped(s) {
			return false
		}
		for i := 0; i < len(s); i++ {
			if c := s[i]; c < 0x20 || c > 0x7e || c == ':' {
				return false
			}
		}
	}
	return true
}
