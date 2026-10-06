package rehydrate

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/rules"
)

// TestLearnedIndex_AnswersAsTheLoopsDo pins the learned-path index (wave 22's verify of finding 28)
// as pure speed: over random names and texts drawn from the characters the screen's boundaries turn
// on, each of its answers is the answer of the per-path loop it replaces, and a glob's literal is
// held by every key the glob matches, by path.Match's rule and by rules.Match's.
func TestLearnedIndex_AnswersAsTheLoopsDo(t *testing.T) {
	rng := rand.New(rand.NewSource(22))
	const alphabet = "ab.-_/~$%#x:= ()\x01é"
	word := func(maxLen int) string {
		var b strings.Builder
		for n := 1 + rng.Intn(maxLen); b.Len() < n; {
			b.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}
		return b.String()
	}
	globWord := func() string {
		const g = "ab.x/*?[]\\"
		var b strings.Builder
		for n := 1 + rng.Intn(8); b.Len() < n; {
			b.WriteByte(g[rng.Intn(len(g))])
		}
		return b.String()
	}
	for round := 0; round < 400; round++ {
		names := make([]string, 1+rng.Intn(6))
		for i := range names {
			names[i] = word(6)
		}
		tr := newNameTrie(names)
		for k := 0; k < 30; k++ {
			text := word(16)
			for _, open := range []bool{false, true} {
				want := false
				for _, n := range names {
					want = want || namedAt(text, n, open)
				}
				require.Equal(t, want, tr.namedAt(text, open), "namedAt(%q, %q, %v)", text, names, open)
				want = false
				for _, n := range names {
					want = want || endsWithPrefixOf(text, n, open)
				}
				require.Equal(t, want, tr.endsWithPrefix(text, open), "endsWithPrefixOf(%q, %q, %v)", text, names, open)
			}
			loop := pathJudge{knownPaths: names}
			require.Equal(t, loop.namesKnownIn(text), tr.namesPathIn(text), "namesKnownIn(%q, %q)", text, names)
		}
		// The keys are as key() makes them: relative, cleaned, never empty.
		keys := make([]string, 0, 6)
		for n := 1 + rng.Intn(6); len(keys) < n; {
			if k := strings.TrimLeft(path.Clean(strings.ReplaceAll(word(10), "\x01", "")), "/"); k != "" && path.Clean(k) == k {
				keys = append(keys, k)
			}
		}
		x := &learnedIndex{keys: "\x00" + strings.Join(keys, "\x00") + "\x00"}
		loop := pathJudge{known: keys}
		for k := 0; k < 20; k++ {
			v := word(5)
			if k%2 == 1 {
				v = globWord()
			}
			sk := selectorKey(v)
			if sk == "" || strings.IndexByte(sk, 0) >= 0 {
				continue
			}
			require.Equal(t, loop.selectsKnown(v), x.selectsLearned(sk, keys), "selectsKnown(%q) over %q", v, keys)
			g := globWord()
			want := false
			for _, w := range keys {
				want = want || rules.Match(g, w)
			}
			require.Equal(t, want, x.matchesLearned(g, keys, rules.Match), "rules.Match(%q) over %q", g, keys)
			for _, w := range keys {
				if globSelects(g, w) {
					require.Contains(t, w, globLiteral(g), "path.Match(%q, %q)", g, w)
				}
				if rules.Match(g, w) {
					require.Contains(t, w, globLiteral(path.Clean(g)), "rules.Match(%q, %q)", g, w)
				}
			}
		}
	}
}

// TestLearnedIndex_AJudgeAnswersAsItsListsDo builds a judge that learns a thousand drops past the
// host's bound, so it reads its learned paths through the index, and asks it and the same judge
// reading each list about texts that name the learned paths in every way the screen reads one (a
// free text, a structured value, a cut summary, a selector by basename, by part of a path and by
// glob, a glob preview, a drop reason) and texts that name none: every answer is the same.
func TestLearnedIndex_AJudgeAnswersAsItsListsDo(t *testing.T) {
	root := previewRoot("proj")
	cp := ckUAT05()
	for i := 0; i < 1000; i++ {
		cp.Dropped = append(cp.Dropped, checkpoint.DropEntry{
			Kind: "file_pointer", ID: fmt.Sprintf("pkg/m%d/f%d.go", i%40, i), Detail: "truncated at budget; re_read(path) still resolves",
		})
	}
	j := newPathJudge(Request{ProjectRoot: root, Checkpoint: cp}, Deps{HostPaths: hostRules(root, uat12Rules...)})
	require.NotNil(t, j.learned, "a thousand drops past the bound are read through the index")
	lists := j
	lists.learned, lists.memo = nil, nil
	texts := []string{
		"cat pkg/m3/f3.go", "cat pkg/m3/f999.go", "cat f999.go", "go test ./pkg/...", "git diff pkg/m7/f7.goo",
		"cat pkg/m3/f99…", "cat pkg/m3/f9…", "cat pkg/zz/zz…", `{"file":"pkg/m39/f999.go"}`, `{"paths":"pkg/m1/f1.go src/a.go"}`,
		`{"query":"path:f999.go"}`, `{"query":"path:m39/f99"}`, `{"query":"path:pkg/m39/*.go"}`, `{"query":"path:**/f99?.go"}`,
		`{"query":"path:nothing.go"}`, "pkg/m2/*.go", "**/f1[0-9].go", "**/*.md", "src/**",
	}
	for _, s := range texts {
		require.Equal(t, lists.judgeSummary(s), j.judgeSummary(s), "%q", s)
		reason := "checkpoint: git index unsupported: open " + strings.TrimSuffix(s, "…") + ": gone"
		require.Equal(t, lists.reasonWithheld(reason), j.reasonWithheld(reason), "%q", reason)
	}
}

// TestHotPathGuards_ChangeNoAnswer pins the first-byte guards before the screen's anchored
// expressions as pure speed: over random words from the characters the expressions turn on, each
// guarded test answers as its expression does.
func TestHotPathGuards_ChangeNoAnswer(t *testing.T) {
	rng := rand.New(rand.NewSource(33))
	const alphabet = "~$%!@{}:/\\aZ_9.-x"
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := rng.Intn(9); b.Len() < n; {
			b.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}
		p := b.String()
		require.Equal(t, homeOrVarRoot.MatchString(p), homeOrVarRooted(p), "%q", p)
		require.Equal(t, driveSpelling.MatchString(p), driveLetter(p), "%q", p)
		require.Equal(t, psSplat.MatchString(p), strings.HasPrefix(p, "@") && psSplat.MatchString(p), "%q", p)
	}
	// A JSON string body jsonUnquote takes as it stands decodes to itself.
	const body = "a/\\\"\x01\x1f\x7f é\xc3\xff\u2028"
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := rng.Intn(7); b.Len() < n; {
			b.WriteByte(body[rng.Intn(len(body))])
		}
		s := b.String()
		if !plainJSONBody(s) {
			continue
		}
		var out string
		require.NoError(t, json.Unmarshal([]byte(`"`+s+`"`), &out), "%q", s)
		require.Equal(t, out, s, "%q", s)
	}
}

// TestOperatorPieces_TheShortcutChangesNoAnswer pins operatorPieces' shortcut for a token with no
// operator character as pure speed: it answers as the split does.
func TestOperatorPieces_TheShortcutChangesNoAnswer(t *testing.T) {
	split := func(tok string) []string {
		return strings.FieldsFunc(gluedOperators.Replace(tok), func(r rune) bool { return r == ';' })
	}
	for _, tok := range []string{"", "a", "src/a.go", "a&b", "a&&b", "a|b", "a;b", "a||b;c", ";", "|", "&", "x&", "-C../x"} {
		if shellOperators[tok] || nullDevice(tok) || isURL(tok) || strings.Contains(tok, `"`) {
			continue
		}
		if _, ok := quoteRun(tok); ok {
			continue
		}
		require.Equal(t, split(tok), operatorPieces(tok), "%q", tok)
	}
}

// TestSplitTokens_SlicingChangesNoAnswer pins splitTokens' slicing as pure speed: over random texts of
// spaces, quotes and letters it answers as the byte-by-byte copy it replaced does.
func TestSplitTokens_SlicingChangesNoAnswer(t *testing.T) {
	copyTokens := func(s string) []string {
		var toks []string
		var b strings.Builder
		var quote byte
		flush := func() {
			if b.Len() > 0 {
				toks = append(toks, b.String())
				b.Reset()
			}
		}
		for i := 0; i < len(s); i++ {
			switch c := s[i]; {
			case quote != 0:
				if c == quote {
					quote = 0
				}
				b.WriteByte(c)
			case c == '"' || (c == '\'' && b.Len() == 0):
				quote = c
				b.WriteByte(c)
			case c == ' ':
				flush()
			default:
				b.WriteByte(c)
			}
		}
		flush()
		return toks
	}
	rng := rand.New(rand.NewSource(26))
	const alphabet = "  ab'\"x"
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := rng.Intn(12); b.Len() < n; {
			b.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}
		require.Equal(t, copyTokens(b.String()), splitTokens(b.String()), "%q", b.String())
	}
}
