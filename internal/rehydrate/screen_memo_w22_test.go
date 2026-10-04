package rehydrate

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
)

// TestBuild_TheScreensMemoNeverChangesAnAnswer pins audit 2's finding 33's memo and prefilters as
// pure speed: the root expression's prefilter (a text that does not hold the root's last segment,
// folded as the expression folds it, an ASCII letter's case where the platform's paths fold and no
// other character's) skips only texts the expression finds nothing in, and a memoized answer, the
// root held, a summary judged or a reason screened, is the answer computed afresh, asked once or
// twice. The roots hold an ASCII letter in either case, a letter the Kelvin sign or the long s
// would stand for, a non-ASCII letter and a space; the texts spell each root exactly, in another
// ASCII case, with a Unicode case variant, glued to a name, in either slash, JSON-escaped, and not
// at all.
func TestBuild_TheScreensMemoNeverChangesAnAnswer(t *testing.T) {
	for _, elem := range [][]string{{"Proj"}, {"kate", "proj"}, {"src", "José"}, {"John Smith", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := previewRoot(elem...)
			slash := filepath.ToSlash(root)
			variants := []string{
				root, slash, strings.ToUpper(root), strings.ToLower(root), asciiLower(root),
				strings.ReplaceAll(root, "k", "K"), strings.ReplaceAll(root, "s", "ſ"),
				strings.ReplaceAll(root, `\`, `\\`), root + "2", "x" + root,
			}
			if runtime.GOOS == "windows" && len(slash) > 2 && slash[1] == ':' {
				variants = append(variants, "/"+strings.ToLower(slash[:1])+slash[2:], "/mnt/c"+slash[2:])
			}
			var texts []string
			for _, v := range variants {
				texts = append(texts,
					v, "cd "+v+" && go test ./...", "git -C "+v+" status", v+"/src/main.go", `"`+v+`" x`,
					filepath.Join(v, "docs", "a b.md"), "-I"+v+"/include", v+";", v+"|wc")
			}
			texts = append(texts, "", "go test ./...", "cat private/deny.txt", elem[len(elem)-1])

			cp := ckUAT05()
			cp.Pointers.Files = []checkpoint.FilePointer{{Path: "private/deny.txt", Hash: hashOf("deny"), Why: "referenced"}}
			j := newPathJudge(Request{ProjectRoot: root, Checkpoint: cp}, Deps{HostPaths: hostRules(root, uat12Rules...)})
			fresh := j
			fresh.memo = nil
			for _, s := range texts {
				want := holdWith(j.rootSpelling, s)
				require.Equal(t, want, j.holdRoot(s), "%q: the prefilter or the memo changed the held text", s)
				require.Equal(t, want, j.holdRoot(s), "%q: asked twice", s)
				require.Equal(t, fresh.judgeSummary(s), j.summaryWithheld(s), "%q", s)
				require.Equal(t, fresh.judgeSummary(s), j.summaryWithheld(s), "%q: asked twice", s)
				reason := "checkpoint: git index unsupported: open " + s + ": gone"
				g := gatedReason{withheld: fresh.reasonWithheld(reason)}
				if g.withheld {
					g.text = fresh.redactReason(reason)
				}
				require.Equal(t, g, j.gatedReasonOf(reason), "%q", reason)
				require.Equal(t, g, j.gatedReasonOf(reason), "%q: asked twice", reason)
			}
		})
	}
}
