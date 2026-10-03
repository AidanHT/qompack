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
// none for a missing path or an existing one named as listed, one judgement each for a path through
// a link, an 8.3 alias or a spelling the operating system rewrites.
func TestEvaluator_JudgesOnDiskOnlyWhatTheDiskCouldRespell(t *testing.T) {
	e := newDiskEnv(t)
	e.write(t, filepath.Join(e.root, "private", "deny.txt"), "x")
	e.write(t, filepath.Join(e.root, "src", "x", "y.go"), "x")
	e.write(t, e.project(), `{"permissions":{"deny":["Read(./private/**)"]}}`)
	rs, err := e.pol.Snapshot()
	require.NoError(t, err)

	ev := rs.Evaluator(e.root)
	for _, rel := range []string{
		"private/deny.txt", "private/gone.txt", "src/x/y.go", "src/x/gone/z.go", "gone", "git log --oneline -n 7",
		"src/x/y.go docs/guide.md and grep",
	} {
		ev.Evaluate(filepath.Join(e.root, filepath.FromSlash(rel)))
	}
	require.Zero(t, ev.DiskEvaluations(), "a path named as listed, or missing from some segment on, needs no disk")

	respelt := []string{"private/deny.txt.", "private/deny.txt:s", "café.txt"}
	if runtime.GOOS == "windows" {
		respelt = append(respelt, "nul")
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
