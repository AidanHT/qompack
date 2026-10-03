package hostperm

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestEvaluator_AgreesWithEvaluate pins Evaluator's one promise: for every path it gives the
// Decision RuleSet.Evaluate gives, while judging without the disk only a path whose walk down the
// project's listed directories shows nothing can respell it. The project holds a denied file, a
// directory link into the denied directory, a file link to the denied file and a directory link out
// of the project, where the platform will make them; the paths cover each of those, missing files
// beside and below them, a file used as a directory, other case, and the spellings the operating
// system rewrites (a trailing dot or space, a stream, a device name, an 8.3 alias) or that a listing
// cannot be compared with (non-ASCII).
func TestEvaluator_AgreesWithEvaluate(t *testing.T) {
	e := newDiskEnv(t)
	outside := t.TempDir()
	for _, f := range []string{"private/deny.txt", "private/keep/a.txt", "docs/a.md", "src/x/y.go", "my notes.txt", "ask/q.txt"} {
		e.write(t, filepath.Join(e.root, filepath.FromSlash(f)), "x")
	}
	e.write(t, filepath.Join(outside, "key.pem"), "k")
	links := map[string]bool{}
	if makeDirLink(filepath.Join(e.root, "alias"), filepath.Join(e.root, "private")) == nil {
		links["alias"] = true
		t.Cleanup(func() { _ = os.Remove(paths.Long(filepath.Join(e.root, "alias"))) })
	}
	if makeDirLink(filepath.Join(e.root, "vendor"), outside) == nil {
		links["vendor"] = true
		t.Cleanup(func() { _ = os.Remove(paths.Long(filepath.Join(e.root, "vendor"))) })
	}
	if os.Symlink(filepath.Join(e.root, "private", "deny.txt"), filepath.Join(e.root, "innocent.txt")) == nil {
		links["innocent.txt"] = true
	}
	for _, l := range []string{"alias", "vendor", "innocent.txt"} {
		if !links[l] {
			t.Logf("platform: this host will not make the link %s, so its rows judge a missing path", l)
		}
	}
	outsideRule := "Read(//" + strings.Join(posixSegments(outside, runtime.GOOS, false), "/") + "/**)"

	rels := []string{
		"private/deny.txt", "private/keep/a.txt", "private/gone.txt", "private", "docs/a.md", "docs/gone.md",
		"docs/a.md/x", "src/x/y.go", "src/x/gone/z.go", "src/x", "gone", "gone/deeper/x.txt", "my notes.txt",
		"my notes.txt apikey", "git log --oneline", "ask/q.txt", "ask/gone.txt", "alias/deny.txt", "alias/gone.txt",
		"alias", "vendor/key.pem", "vendor/gone.pem", "innocent.txt", "café/x.txt", "docs/a.md.", "docs/a.md ",
		"private./deny.txt", "private/deny.txt:hidden", "nul", "CON", "docs/NUL",
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		rels = append(rels, "PRIVATE/DENY.TXT", "Private/Keep/A.txt", "DOCS/A.MD", "ALIAS/DENY.TXT")
	}
	if runtime.GOOS == "windows" {
		if short := shortName(e.root, "private"); short != "" {
			rels = append(rels, short+"/deny.txt", short+"/gone.txt")
		}
		if short := shortName(e.root, "my notes.txt"); short != "" {
			rels = append(rels, short)
		}
	}

	for _, rules := range []string{
		`{"permissions":{"deny":["Read(./private/**)",` + jsonString(outsideRule) + `],"ask":["Read(./ask/**)"]}}`,
		`{"permissions":{"deny":["Read(./private/deny.txt)","Read(./innocent.txt)"]}}`,
		// A rule naming an 8.3 name after a glob: a path that does not exist is refused, so every
		// path is judged on disk.
		`{"permissions":{"deny":["Read(**/CREDEN~1.SEC)"]}}`,
	} {
		t.Run(rules, func(t *testing.T) {
			e.write(t, e.project(), rules)
			rs, err := e.pol.Snapshot()
			require.NoError(t, err)
			ev := rs.Evaluator(e.root)
			for _, rel := range rels {
				abs := filepath.Join(e.root, filepath.FromSlash(rel))
				require.Equal(t, rs.Evaluate(abs), ev.Evaluate(abs), "%q", rel)
				// A second evaluator, and a second judgement by the first, answer the same.
				require.Equal(t, rs.Evaluate(abs), rs.Evaluator(e.root).Evaluate(abs), "%q", rel)
				require.Equal(t, rs.Evaluate(abs), ev.Evaluate(abs), "%q", rel)
			}
			outsideAbs := filepath.Join(outside, "key.pem")
			require.Equal(t, rs.Evaluate(outsideAbs), ev.Evaluate(outsideAbs), "a path below no base")
		})
	}
}

// TestEvaluator_JudgesOnDiskOnlyWhatTheDiskCouldRespell pins where the evaluator's disk work goes:
// none for a missing path or an existing one named as listed, whatever colons, trailing dots or
// spaces, 8.3-shaped words or non-ASCII text it holds where it names nothing (since the w19
// round-2 review: a stream or a non-ASCII segment was judged on disk wherever it stood), and one
// judgement each for a path through a link or a segment the Win32 layer trims to an existing name.
func TestEvaluator_JudgesOnDiskOnlyWhatTheDiskCouldRespell(t *testing.T) {
	e := newDiskEnv(t)
	e.write(t, filepath.Join(e.root, "private", "deny.txt"), "x")
	e.write(t, filepath.Join(e.root, "src", "x", "y.go"), "x")
	e.write(t, e.project(), `{"permissions":{"deny":["Read(./private/**)"]}}`)
	rs, err := e.pol.Snapshot()
	require.NoError(t, err)

	ev := rs.Evaluator(e.root)
	plain := []string{
		"private/deny.txt", "private/gone.txt", "src/x/y.go", "src/x/gone/z.go", "gone", "git log --oneline -n 7",
		"src/x/y.go docs/guide.md and grep",
		// The shapes a summary's pieces take beyond plain words (w19 round-2 review): a stream, a
		// drive or a URL scheme mid-path, a colon in a word, JSON, a trailing dot or space where the
		// walk ends at a missing entry, an 8.3-shaped word, and non-ASCII text.
		"private/deny.txt:s", "src/x/y.go:Zone.Identifier", "cd C:/q/proj && go test ./...", "https://example.com/x",
		"TODO: fix it", `{"description":"run the tests","prompt":"go test ./..."}`, "go test ./...", "gone. /x",
		"git diff HEAD~1",
	}
	if runtime.GOOS != "darwin" {
		plain = append(plain, "café.txt", "fix — never cut the root")
	}
	for _, rel := range plain {
		ev.Evaluate(filepath.Join(e.root, filepath.FromSlash(rel)))
	}
	require.Zero(t, ev.DiskEvaluations(), "a path named as listed, or missing from some segment on, needs no disk")

	// A trailing dot or space on a segment that names an entry once trimmed, and on macOS a name
	// whose Unicode normalization the listing cannot compare.
	respelt := []string{"private/deny.txt.", "private /deny.txt", "private./deny.txt"}
	if runtime.GOOS != "windows" {
		respelt = []string{}
	}
	if runtime.GOOS == "darwin" {
		respelt = append(respelt, "café.txt")
	}
	for _, rel := range respelt {
		before := ev.DiskEvaluations()
		ev.Evaluate(filepath.Join(e.root, filepath.FromSlash(rel)))
		require.Equal(t, before+1, ev.DiskEvaluations(), "%q is judged on disk", rel)
	}
	if makeDirLink(filepath.Join(e.root, "alias"), filepath.Join(e.root, "private")) == nil {
		t.Cleanup(func() { _ = os.Remove(paths.Long(filepath.Join(e.root, "alias"))) })
		fresh := rs.Evaluator(e.root)
		require.Equal(t, Deny, fresh.Evaluate(filepath.Join(e.root, "alias", "deny.txt")).Effect)
		require.Equal(t, 1, fresh.DiskEvaluations(), "a path through a link is judged on disk")
	}
}

// TestEvaluator_AnEntryNamedByItsAliasIsJudgedOnDisk: an 8.3 alias opens its entry under the
// entry's own name, which a rule may name, so a path that names an entry by an alias is judged on
// disk even when the alias has no tilde (a short name set by hand). The listing stands in for a
// volume that records one, since setting a short name needs privileges a test may not take.
func TestEvaluator_AnEntryNamedByItsAliasIsJudgedOnDisk(t *testing.T) {
	e := newDiskEnv(t)
	e.write(t, e.project(), `{"permissions":{"deny":["Read(./private/**)"]}}`)
	rs, err := e.pol.Snapshot()
	require.NoError(t, err)
	ev := rs.Evaluator(e.root)
	ev.dirs[filepath.Clean(e.root)] = map[string]entryKind{"secret notes.txt": entryOwn, "secret.txt": entryAlias}

	ev.Evaluate(filepath.Join(e.root, "secret notes.txt"))
	require.Zero(t, ev.DiskEvaluations(), "an entry named by its own name needs no disk")
	ev.Evaluate(filepath.Join(e.root, "secret.txt"))
	require.Equal(t, 1, ev.DiskEvaluations(), "an entry named by its alias is judged on disk")
}

// TestEvaluator_LexicalCandidatesAreEvaluatesSpellings pins the evaluator's equivalence spelling for
// spelling, for the shapes a rehydration's summary pieces take beyond plain words (w19 round-2
// review: a JSON, URL, colon or non-ASCII piece was judged on disk, so the build's cost was linear
// only for colon-free ASCII previews). For every path the evaluator judges without the disk, its
// candidates are exactly the spellings Evaluate matches (spellings: as written, osAlias's, and
// where each resolves) and its 8.3 verdict is osAlias's, so no rule can tell the two apart; and
// every path gets Evaluate's Decision under each rule set. The project holds existing directories
// named like the words a command starts with, a non-ASCII directory, and a link with a non-ASCII
// name, where the platform will make one.
func TestEvaluator_LexicalCandidatesAreEvaluatesSpellings(t *testing.T) {
	e := newDiskEnv(t)
	for _, f := range []string{"private/deny.txt", "src/x/y.go", "cd/z.txt", "go test/w.txt", "naïve/x.txt"} {
		e.write(t, filepath.Join(e.root, filepath.FromSlash(f)), "x")
	}
	for _, l := range []string{"alias", "ålias"} {
		if makeDirLink(filepath.Join(e.root, l), filepath.Join(e.root, "private")) == nil {
			link := filepath.Join(e.root, l)
			t.Cleanup(func() { _ = os.Remove(paths.Long(link)) })
		} else {
			t.Logf("platform: this host will not make the link %s, so its rows judge a missing path", l)
		}
	}
	// Each must be judged without the disk where the platform compares its names with a listing.
	lexical := []string{
		"private/deny.txt:hidden", "private/deny.txt:hidden:$DATA", "src/x/y.go:Zone.Identifier",
		"cd C:/q/proj && go test ./...", "cd/z.txt and more", "https://example.com/x", "TODO: fix it",
		`{"description":"run the tests","prompt":"go test ./..."}`, `{"query":"path:private/deny.txt"}`,
		"make test ./...", "gone. /x", "gone /x", "git diff HEAD~1", "gone~1/x", "private/DENY~1.TXT",
		"a:b", "x::y", "private:x/deny.txt",
	}
	if runtime.GOOS != "darwin" {
		// The decomposed spelling of the listed naïve names nothing: neither NTFS nor a POSIX
		// filesystem other than macOS's equates Unicode normalizations.
		lexical = append(lexical, "fix — never cut the root", "café.txt", "Привет мир", "naïve and more",
			"nai\u0308ve/x.txt")
	}
	// Each must be judged on disk: a link (on Windows by any stream spelling, and an empty entry
	// name, which opens the directory itself), a trailing dot or space on a segment that names an
	// entry once trimmed (`go test ./...` here, where `go test` exists), and a non-ASCII name a
	// listed name may equal. On POSIX a colon is a name's own character, so `alias:s` names nothing.
	onDisk := []string{"alias/deny.txt"}
	if runtime.GOOS != "windows" {
		lexical = append(lexical, "alias:s/deny.txt", ":s/x")
	}
	if runtime.GOOS == "windows" {
		onDisk = append(onDisk, "alias:s/deny.txt", ":s/x", "private /deny.txt", "private. /deny.txt", "private./deny.txt", "go test ./...",
			"naïve/x.txt", "NAÏVE/x.txt", "ålias/deny.txt", "ÅLIAS/deny.txt")
	}
	rels := append(append([]string{
		"private/deny.txt", "private/gone.txt", "src/x/y.go", "cd", "cd/gone", "go test", "naïve", "ålias",
		"alias", "private/deny.txt.", "src/x/y.go ", "nul", "docs/NUL", "...", "x/..", "a/./b",
	}, lexical...), onDisk...)

	for _, rules := range []string{
		`{"permissions":{"deny":["Read(./private/**)"],"ask":["Read(./src/x/**)"]}}`,
		`{"permissions":{"deny":["Read(**/deny.txt)","Read(./go test/**)","Read(./naïve/**)"]}}`,
		`{"permissions":{"deny":["Read(./cd/**)","Read(./TODO/**)"]}}`,
	} {
		t.Run(rules, func(t *testing.T) {
			e.write(t, e.project(), rules)
			rs, err := e.pol.Snapshot()
			require.NoError(t, err)
			for _, rel := range rels {
				abs := filepath.Join(e.root, filepath.FromSlash(rel))
				ev := rs.Evaluator(e.root)
				cands, unresolved, ok := ev.lexical(abs)
				if ok {
					want, _, wantUnresolved := spellings(abs, rs.goos, rs.fold, rs.resolve, nil)
					require.ElementsMatch(t, joinedSpellings(want), joinedSpellings(cands), "%q", rel)
					require.Equal(t, wantUnresolved, unresolved, "%q: the 8.3 verdict", rel)
				}
				require.Equal(t, rs.Evaluate(abs), ev.Evaluate(abs), "%q", rel)
			}
			for _, rel := range lexical {
				_, _, ok := rs.Evaluator(e.root).lexical(filepath.Join(e.root, filepath.FromSlash(rel)))
				require.True(t, ok, "%q is judged without the disk", rel)
			}
			for _, rel := range onDisk {
				_, _, ok := rs.Evaluator(e.root).lexical(filepath.Join(e.root, filepath.FromSlash(rel)))
				if strings.HasPrefix(rel, "alias") || strings.HasPrefix(strings.ToLower(rel), "ålias") {
					if _, err := os.Lstat(filepath.Join(e.root, "alias")); err != nil {
						continue
					}
				}
				require.False(t, ok, "%q is judged on disk", rel)
			}
		})
	}
}

// joinedSpellings is each candidate spelling as one slash-joined string.
func joinedSpellings(cands [][]string) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, "/"+strings.Join(c, "/"))
	}
	return out
}

// TestEvaluator_ANameAListedNonASCIINameMayEqualIsJudgedOnDisk: NTFS equates names through the
// volume's own upcase table, one UTF-16 unit at a time, not through strings.ToLower, so a segment
// that no folded listed name equals may still open an entry with a non-ASCII name (`info` and a
// listed `ınfo`, where a table upcases the dotless ı to I), and a non-ASCII segment may open an
// entry under a table the listing's folding does not model (`docs` spelled with a Cyrillic o, for
// a listed `docs`). On Windows each such segment is judged on disk; a segment no listed name could
// equal (another length, another ASCII letter) still is not. Elsewhere names compare as bytes, or,
// non-ASCII on macOS, are judged on disk. The listing stands in for a directory holding such a
// name, which a test cannot rely on a volume to allow.
func TestEvaluator_ANameAListedNonASCIINameMayEqualIsJudgedOnDisk(t *testing.T) {
	e := newDiskEnv(t)
	e.write(t, e.project(), `{"permissions":{"deny":["Read(./private/**)"]}}`)
	rs, err := e.pol.Snapshot()
	require.NoError(t, err)
	ev := rs.Evaluator(e.root)
	dir := filepath.Clean(e.root)
	ev.dirs[dir] = map[string]entryKind{"ınfo": entryOwn, "docs": entryOwn}
	ev.wide[dir] = []string{"ınfo"}

	for _, rel := range []string{"other", "infos", "inf", "info.txt"} {
		ev.Evaluate(filepath.Join(e.root, rel))
	}
	require.Zero(t, ev.DiskEvaluations(), "no listed name could equal these")
	mayEqual := 0
	for _, rel := range []string{"info", "INFO", "ınfo", "ïnfo", "d\u043ecs"} {
		ev.Evaluate(filepath.Join(e.root, rel))
		if runtime.GOOS == "windows" || (runtime.GOOS == "darwin" && !isASCII(rel)) {
			mayEqual++
		}
		require.Equal(t, mayEqual, ev.DiskEvaluations(), "%q may name a listed name", rel)
	}
}
