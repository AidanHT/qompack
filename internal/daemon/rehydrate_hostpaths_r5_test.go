package daemon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
)

// TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames is wave 19h's
// verify of the root's ASCII-only case fold through the real host rules and real folders (ADR 0011
// §23, the two-fold rule). The project's root is `<tmp>\åsa\proj` (or `kate`, `sam`, `iris`), and the
// session recorded the project's denied private/deny.txt or private/id under the root spelled with
// another case of a non-ASCII letter: as a file pointer, and as a NotebookEdit's value under a root
// with a space (`<tmp>\Åsa berg\proj`). NTFS folds U+00C5 onto the project's own folder and keeps the
// Kelvin sign's, the long s's, the Angstrom sign's and the dotted capital I's folders beside it; the
// host lower-cases a path, so it refuses every one of those spellings but the long s's as the
// project's denied file. The recorded path is withheld, but since c16b21d5 containment read it as a
// directory beside the project and learned no project-relative name from it, so a glob that selects
// the denied file (`private/d*`, `private/de?y.txt`, `private/i?`) was shown. Where the host refuses
// the recorded spelling each glob is withheld; under the long s, and where paths do not fold, the
// recorded spelling names another file the host does not refuse, and the globs are shown.
//
// The long s's answer holds only while the root resolves to itself, so the row makes it canonical on
// every platform (filepath.EvalSymlinks, as costProject's root is): macOS spells its temporary
// directory below /var, a link to /private/var, and shortProjectDir resolves it only on Windows.
// Where the root resolves elsewhere the adapter also judges each reading of the recorded path's place
// below the resolved root, and the broad one (and on Windows filepath.Rel's) places the long s's
// spelling at the project's denied file, which the host refuses there; the spelling is then refused
// and teaches its project-relative names, which over-withholds where the volume keeps the long s's
// folder beside the root and is the safe direction wherever it does not (ADR 0011 §23).
func TestRehydrateHostPaths_AWithheldPathUnderAUnicodeCaseSpellingOfTheRootTeachesItsNames(t *testing.T) {
	fold := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	type shape struct {
		name, rel string
		spaced    bool
		globs     []string
	}
	shapes := []shape{
		{"a file pointer", "private/deny.txt", false, []string{"private/d*", "private/de?y.txt"}},
		{"a tool summary under a spaced root", "private/deny.txt", true, []string{"private/d*", "private/de?y.txt"}},
		{"a short name", "private/id", false, []string{"private/i?"}},
	}
	for _, rc := range []struct {
		name, seg, variant  string
		ntfsSame, hostFolds bool
	}{
		{"U+00C5", "åsa", "Åsa", true, true},
		{"U+212A", "kate", "Kate", false, true},
		{"U+017F", "sam", "ſam", false, false},
		{"U+212B", "åsa", "Åsa", false, true},
		{"U+0130", "iris", "İris", false, true},
	} {
		for _, sh := range shapes {
			t.Run(rc.name+"/"+sh.name, func(t *testing.T) {
				seg, vseg := rc.seg, rc.variant
				if sh.spaced {
					seg, vseg = seg+" berg", vseg+" berg"
				}
				dir := shortProjectDir(t, seg, "proj")
				require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
				root, err := filepath.EvalSymlinks(dir)
				require.NoError(t, err)
				writeProjectSettings(t, root,
					`{"permissions":{"deny":["Read(./private/deny.txt)","Read(./private/id)"]}}`)
				for _, f := range []string{"private/deny.txt", "private/id", "src/main.go"} {
					writeProjectFile(t, root, f)
				}
				variant := filepath.Join(filepath.Dir(filepath.Dir(root)), vseg, "proj")
				recorded := filepath.Join(variant, filepath.FromSlash(sh.rel))
				if runtime.GOOS == "windows" {
					if !rc.ntfsSame {
						writeProjectFile(t, variant, sh.rel)
					}
					own, err := os.Stat(paths.Long(filepath.Join(root, filepath.FromSlash(sh.rel))))
					require.NoError(t, err)
					rec, err := os.Stat(paths.Long(recorded))
					require.NoError(t, err)
					require.Equal(t, rc.ntfsSame, os.SameFile(own, rec), "fixture: NTFS folds only U+00C5 onto the root's letter")
				}
				refused := fold && rc.hostFolds
				refuses := rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())().Refuses
				require.NotNil(t, refuses)
				require.Equal(t, refused, refuses(recorded), "fixture: the host refuses the recorded spelling as it folds case")

				var files []checkpoint.FilePointer
				var tools []checkpoint.ToolPointer
				if sh.spaced {
					tools = append(tools, tpOf("toolu_rec", storePreview(t, map[string]string{"notebook_path": recorded})))
				} else {
					files = append(files, checkpoint.FilePointer{Path: recorded, Hash: core.Hash(sha256.Sum256([]byte(recorded))), Why: "referenced"})
				}
				for i, g := range sh.globs {
					tools = append(tools, tpOf(fmt.Sprintf("toolu_glob_%d", i), g))
				}
				req := toolPointerRequest(root, tools)
				req.Checkpoint.Pointers.Files = files
				deps := rehydrate.Deps{HostPaths: rehydrateHostPaths(mcpOpHostPolicy(t, root), root, logging.Nop())}
				res, err := rehydrate.Build(context.Background(), req, deps)
				require.NoError(t, err)
				require.NotContains(t, res.Text, rc.variant, "the recorded spelling is withheld")
				for _, f := range files {
					require.Contains(t, res.Text, f.Hash.String(), "the withheld file pointer still points by hash")
				}
				for _, tp := range tools {
					line := "- tool_use " + string(tp.ToolUseID) + " " + tp.Hash.String() + " — "
					if tp.ToolUseID == "toolu_rec" || refused {
						require.Contains(t, res.Text, line+"summary withheld", "%q names or selects a withheld path", tp.Summary)
						continue
					}
					require.Contains(t, res.Text, line+tp.Summary+"\n", "%q selects nothing the host refuses", tp.Summary)
				}
			})
		}
	}
}

// tpOf is a tool pointer with id whose recorded summary is s.
func tpOf(id, s string) checkpoint.ToolPointer {
	return checkpoint.ToolPointer{ToolUseID: core.ToolUseID(id), Hash: core.Hash(sha256.Sum256([]byte(id + s))), Summary: s}
}
