package rehydrate

import (
	"path"
	"strings"
	"unicode/utf8"
)

// learnedIndex answers the screen's questions about every path a build learned to withhold (the
// judge's known, knownText and knownPaths) in one pass over a text, not one pass per learned path
// (wave 22's verify of audit 2's finding 28). A drop past the host's bound is learned as withheld
// whatever its spelling (newPathJudge, dropOrder), so a long session's build learns a path for each
// such drop, and the screen, which asked each learned path in turn about each summary, each of its
// readings and each reason, grew with the session: a thousand drops took a build from about 8 ms to
// about 60 ms. Each answer is the one the per-path loop gives (TestLearnedIndex_AnswersAsTheLoopsDo).
// newPathJudge builds it once learning ends, and only when a list is long enough for it to pay
// (learnedIndexMin); the loops answer otherwise.
type learnedIndex struct {
	// text holds knownText (textNamesWithheld, cutPrefixNamed).
	text *nameTrie
	// paths holds knownPaths (namesKnownIn).
	paths *nameTrie
	// keys is every entry of known between NUL bytes, which no key holds: a key holds a stretch
	// exactly when keys holds it (selectsKnown), and every key a glob matches holds the glob's
	// literal (globLiteral).
	keys string
}

// learnedIndexMin is the longest learned list the loops still answer for: past it a build indexes
// its learned paths (learnedIndex). A build with no drop past the host's bound learns a handful.
const learnedIndexMin = 32

// newLearnedIndex is the index of a judge's learned lists, or nil when every one is short.
func newLearnedIndex(known, knownText, knownPaths []string) *learnedIndex {
	if len(known) <= learnedIndexMin && len(knownText) <= learnedIndexMin && len(knownPaths) <= learnedIndexMin {
		return nil
	}
	return &learnedIndex{
		text:  newNameTrie(knownText),
		paths: newNameTrie(knownPaths),
		keys:  "\x00" + strings.Join(known, "\x00") + "\x00",
	}
}

// nameTrie is a byte trie over a set of names in screen form, stored as first-child and
// next-sibling links in one slice, with the root's children in a table.
type nameTrie struct {
	nodes []trieNode
	root  [256]int32
	// maxLen is the longest name's length.
	maxLen int
}

// trieNode is one byte of one or more names. Node 0 is the root, which is no node's child or
// sibling, so 0 links nothing.
type trieNode struct {
	child, sib int32
	b          byte
	// end marks a node where a name ends.
	end bool
	// cont marks a node where a name ends or goes on with a byte that starts a character: a text that
	// ends in the bytes up to it ends in a prefix endsWithPrefixOf counts.
	cont bool
}

// newNameTrie is the trie of names; an empty name is left out, as appendDistinct leaves it out.
func newNameTrie(names []string) *nameTrie {
	t := &nameTrie{nodes: make([]trieNode, 1, 1+8*len(names))}
	for _, name := range names {
		if name == "" {
			continue
		}
		var n int32
		for i := 0; i < len(name); i++ {
			if utf8.RuneStart(name[i]) {
				t.nodes[n].cont = true
			}
			n = t.child(n, name[i], true)
		}
		t.nodes[n].end, t.nodes[n].cont = true, true
		t.maxLen = max(t.maxLen, len(name))
	}
	return t
}

// child is n's child for byte c, added when add is set and there is none; 0 when there is none.
func (t *nameTrie) child(n int32, c byte, add bool) int32 {
	if n == 0 && t.root[c] != 0 {
		return t.root[c]
	}
	last := int32(0)
	if n != 0 {
		for k := t.nodes[n].child; k != 0; k = t.nodes[k].sib {
			if t.nodes[k].b == c {
				return k
			}
			last = k
		}
	}
	if !add {
		return 0
	}
	k := int32(len(t.nodes))
	t.nodes = append(t.nodes, trieNode{b: c})
	switch {
	case n == 0:
		t.root[c] = k
	case last == 0:
		t.nodes[n].child = k
	default:
		t.nodes[last].sib = k
	}
	return k
}

// namedAt reports whether some name occurs in form where a name starts and, unless open is set,
// ends where a name ends: namedAt's answer for at least one of the names.
func (t *nameTrie) namedAt(form string, open bool) bool {
	for i := 0; i < len(form); i++ {
		n := t.root[form[i]]
		if n == 0 || !nameStartsAt(form, i) {
			continue
		}
		for j := i + 1; ; j++ {
			if t.nodes[n].end && (open || nameEndsAt(form, j)) {
				return true
			}
			if j == len(form) {
				break
			}
			if n = t.child(n, form[j], false); n == 0 {
				break
			}
		}
	}
	return false
}

// endsWithPrefix reports whether text ends in the first minCutPrefix bytes or more of some name,
// ending at a character's boundary in the name, and, when bounded, starting where a name may start:
// endsWithPrefixOf's answer for at least one of the names.
func (t *nameTrie) endsWithPrefix(text string, bounded bool) bool {
	for st := max(0, len(text)-t.maxLen); st+minCutPrefix <= len(text); st++ {
		n := t.root[text[st]]
		for j := st + 1; n != 0 && j < len(text); j++ {
			n = t.child(n, text[j], false)
		}
		if n != 0 && t.nodes[n].cont && (!bounded || nameStartsAt(text, st)) {
			return true
		}
	}
	return false
}

// namesPathIn reports whether some name occurs in t, a drop reason in screen form, as a path of its
// own: namesKnownIn's answer for at least one of the names.
func (t *nameTrie) namesPathIn(text string) bool {
	for start := 0; start < len(text); start++ {
		n := t.root[text[start]]
		if n == 0 || !pathRunStart(text, start) {
			continue
		}
		for j := start + 1; ; j++ {
			if t.nodes[n].end && reasonEndsAt(text, j) {
				return true
			}
			if j == len(text) {
				break
			}
			if n = t.child(n, text[j], false); n == 0 {
				break
			}
		}
	}
	return false
}

// reasonEndsAt reports whether a path named in t, a drop reason in screen form, may end at end: at
// t's end or a reasonBoundary, or at a closing `.` before either.
func reasonEndsAt(t string, end int) bool {
	return end == len(t) || strings.IndexByte(reasonBoundary, t[end]) >= 0 ||
		(t[end] == '.' && (end+1 == len(t) || strings.IndexByte(reasonBoundary, t[end+1]) >= 0))
}

// globLiteral is the longest stretch every path g, a glob, matches holds verbatim, path.Match's
// reading of g (rules.Match and the store's selector match each segment by it): a run of g's literal
// bytes within one segment, stopped by a wildcard, by an escape and the byte it escapes, and by a
// class, from whose `[` to the segment's end nothing is counted. "" when there is none.
func globLiteral(g string) string {
	best := ""
	start := 0
	flush := func(end int) {
		if end-start > len(best) {
			best = g[start:end]
		}
	}
	for i := 0; i < len(g); {
		switch g[i] {
		case '*', '?', '/':
			flush(i)
			i++
		case '\\':
			flush(i)
			i += 2
		case '[':
			// A class (which path.Match lets match a `/`) ends where path.Match ends it or later:
			// classEnd reads a `]` first in it, or after its `!`, as a member, and with no end the rest
			// of g counts as the class.
			flush(i)
			if e := classEnd(g, i); e >= 0 {
				i = e + 1
			} else {
				i = len(g)
			}
		default:
			i++
			continue
		}
		start = i
	}
	flush(len(g))
	return best
}

// selectsLearned is selectsKnown's answer through the index: a plain value selects a key that holds
// it; a glob selects a key it equals or is a segment suffix of, or one it matches (globSelects),
// among the keys that hold its literal.
func (x *learnedIndex) selectsLearned(k string, known []string) bool {
	if !isGlob(k) {
		return strings.Contains(x.keys, k)
	}
	if strings.Contains(x.keys, "\x00"+k+"\x00") || strings.Contains(x.keys, "/"+k+"\x00") {
		return true
	}
	lit := globLiteral(k)
	if !strings.Contains(x.keys, lit) {
		return false
	}
	for _, w := range known {
		if strings.Contains(w, lit) && globSelects(k, w) {
			return true
		}
	}
	return false
}

// matchesLearned reports whether the glob k, in key form, matches one of known by rules.Match's
// rule, among the keys that hold its literal: globSelectsKnown's answer through the index.
func (x *learnedIndex) matchesLearned(k string, known []string, match func(pattern, key string) bool) bool {
	lit := globLiteral(path.Clean(k))
	if !strings.Contains(x.keys, lit) {
		return false
	}
	for _, w := range known {
		if strings.Contains(w, lit) && match(k, w) {
			return true
		}
	}
	return false
}
