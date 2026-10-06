package rehydrate

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Wave 23's rows over candidate 8's diff verify, fix round 1's review (coordinator decisions D66 and
// D67(l)).

// requireRefusedNamesHoldingAPieceCharacterJudged is fix round 1's review of finding 8 over a project
// at root (inRoot spells a path under it, JSON-escaped). The round read a path-named value twice: as
// the store spells it, where a NUL is dropped and an NBSP, U+2028 or an en dash is read as part of a
// name, and in a name form where each of those, every quote and `, ; |` is a space. A refused name
// that itself holds one of those characters (`o'brien.env`, `q3–secrets.xlsx`, `a,b.env`, a name
// with an NBSP, a learned `bob's keys.txt`) was then whole in neither reading when it starts a glued
// piece: glued to the piece before it in the first, split at its own character in the second
// (screen form drops the apostrophe from the literal, so `obrien.env` is never `o brien.env`). Nor
// when it ends one: `o'brien.env<NUL>src/a.ts` ran on into the next piece. The exact refused name
// was shown (and was at 77374c3c). Each stretch of the value from a place a piece or a path starts to
// a place one ends, where the screen reads no boundary, is now screened as the value is. A name that
// only begins like the refused one, and a list of project paths, are still shown.
//
// The review's minor finding is the glob half: a glob piece was judged by what it selects only when
// the name form differed from the value, and its pieces were cut at spaces only, so a glob after an
// ASCII space or an opener (`src/a.ts .en*`, `src/a.ts:.en*`) and one under a root with a space in it
// (cut inside the root) were judged as the whole value, which selects nothing, and shown while the
// same glob alone, or after a NUL or a comma, was withheld; a value the store's cut fell inside
// judged no glob piece by what it selects at all. Each glob stretch of a value that holds several
// paths, cut or not, is now judged by what it selects, the root's own spelling held whole.
func requireRefusedNamesHoldingAPieceCharacterJudged(t *testing.T, root string, inRoot func(...string) string) {
	t.Helper()
	names := []string{"o'brien.env", "q3\u2013secrets.xlsx", "a,b.env", "my\u00a0keys.txt"}
	rules := hostRules(root, "./o'brien.env", "./q3\u2013secrets.xlsx", "./a,b.env", "./my\u00a0keys.txt")
	leaks := []string{"brien.env", "secrets.xlsx", "a,b.env", "keys.txt"}
	seps := []struct{ name, sep string }{
		{"nul", `\u0000`},
		{"us", `\u001f`},
		{"del", `\u007f`},
		{"nel", "\u0085"},
		{"nbsp", "\u00a0"},
		{"ls escaped", `\u2028`},
		{"ideographic space", "\u3000"},
		{"best fit quote", "\u02ba"},
		{"zero width space", "\u200b"},
		{"en dash", "\u2013"},
		{"quote", `\"`},
		{"apostrophe", "'"},
		{"backtick", "`"},
	}
	type row struct{ name, summary string }
	var withheld []row
	for _, s := range seps {
		for i, n := range names {
			withheld = append(withheld, row{fmt.Sprintf("%s after %d", s.name, i), `{"paths":"src/a.ts` + s.sep + n + `"}`})
		}
	}
	for _, s := range seps[:5] {
		for i, n := range names {
			withheld = append(withheld,
				row{fmt.Sprintf("%s before %d", s.name, i), `{"paths":"` + n + s.sep + `src/a.ts"}`},
				row{fmt.Sprintf("%s between %d", s.name, i), `{"paths":"docs/b.md` + s.sep + n + s.sep + `src/a.ts"}`})
		}
	}
	withheld = append(withheld,
		row{"apostrophe before", `{"paths":"o'brien.env'src/a.ts"}`},
		row{"zero width space before", "{\"paths\":\"o'brien.env\u200bsrc/a.ts\"}"},
		row{"en dash before", "{\"paths\":\"q3\u2013secrets.xlsx\u2013src/a.ts\"}"},
		row{"cut apostrophe", `{"cell_id":"c1","paths":"src/a.ts\u0000o'brien.e…`},
		row{"cut en dash", "{\"cell_id\":\"c1\",\"paths\":\"src/a.ts\u00a0q3\u2013secre…"},
		row{"cut comma", `{"cell_id":"c1","paths":"src/a.ts\u0000a,b.en…`},
		row{"array element", `{"paths":["docs/b.md","README.md\u0000o'brien.env"]}`},
		row{"several values", `{"cwd":"src","paths":"src/a.ts\u0000o'brien.env"}`},
	)
	for _, r := range withheld {
		t.Run("glued name/"+r.name, func(t *testing.T) {
			require.Equal(t, "withheld", summaryVerdict(t, root, rules, r.summary, leaks),
				"%q names a refused name that holds a character a piece starts or ends at", r.summary)
		})
	}
	for _, s := range []string{
		`{"paths":"src/a.ts\u0000src/b.ts"}`,
		`{"paths":"src/a.ts\u0000o'brien.env.example"}`,
		"{\"paths\":\"src/a.ts\u00a0docs/o'brien.md\"}",
		`{"paths":"src/a.ts\u0000q3\u2013report.xlsx"}`,
		`{"paths":"src/a.ts\u0000a,b.md"}`,
		`{"file":"docs/it's here.md"}`,
		`{"paths":"'docs/design notes.md' (src/a.ts)"}`,
	} {
		require.Equal(t, "shown", summaryVerdict(t, root, rules, s, nil), "%q names only project paths", s)
	}

	// A name the build learned as withheld, from a file pointer the host refuses through no rule whose
	// literal spells it.
	for _, n := range []string{"bob's keys.txt", "q3\u2013secrets.xlsx"} {
		learned := refusingUnder(root, nil, func(rel string) bool { return rel == n })
		for i, s := range []string{
			`{"paths":"README.md\u0000` + n + `"}`,
			"{\"paths\":\"README.md\u00a0" + n + "\"}",
			`{"paths":"README.md\u2028` + n + `"}`,
			"{\"paths\":\"README.md\u200b" + n + "\"}",
			`{"paths":"` + n + `\u0000README.md"}`,
			`{"paths":"README.md ` + n + `"}`,
		} {
			t.Run(fmt.Sprintf("glued name/learned %s %d", n, i), func(t *testing.T) {
				require.Equal(t, "withheld", summaryVerdictBeside(t, root, learned, n, s, []string{n}), "%q names the learned %s", s, n)
			})
		}
		require.Equal(t, "shown", summaryVerdictBeside(t, root, learned, n, `{"paths":"README.md\u0000src/a.ts"}`, nil))
	}

	// The glob half: a learned `.env` with no literal, and a link lnk into secrets/ learned from a
	// file pointer at lnk/token.txt.
	env := refusingUnder(root, nil, func(rel string) bool { return rel == ".env" })
	for i, s := range []string{
		`{"paths":"src/a.ts .en*"}`,
		`{"paths":"src/a.ts:.en*"}`,
		`{"paths":"src/a.ts=.en*"}`,
		`{"paths":"src/a.ts (.en*)"}`,
		`{"paths":"src/a.ts\t.en*"}`,
		`{"paths":".en* src/a.ts"}`,
		`{"paths":"src/a.ts,.en*"}`,
		`{"paths":"src/a.ts\u0000.en*"}`,
		// The store's cut after the glob piece.
		`{"cell_id":"c1","paths":"src/a.ts .en* src/b.ts src/c…`,
	} {
		t.Run(fmt.Sprintf("glob piece/learned .env %d", i), func(t *testing.T) {
			require.Equal(t, "withheld", summaryVerdictBeside(t, root, env, ".env", s, nil), "%q selects the learned .env", s)
		})
	}
	for _, s := range []string{
		`{"paths":"src/a.ts src/*.go"}`,
		`{"paths":"src/a.ts .github/*.yml"}`,
		`{"cell_id":"c1","paths":"src/a.ts src/*.go src/b.ts src/c…`,
	} {
		require.Equal(t, "shown", summaryVerdictBeside(t, root, env, ".env", s, nil), "%q selects only project paths", s)
	}
	link := refusingUnder(root, []string{"./secrets/**"}, func(rel string) bool {
		return rel == "lnk" || rel == "secrets" || strings.HasPrefix(rel, "lnk/") || strings.HasPrefix(rel, "secrets/")
	})
	relGlob := jsonEscaped(filepath.Join("lnk", "tok*"))
	for i, s := range []string{
		`{"paths":"src/a.go ` + relGlob + `"}`,
		`{"paths":"src/a.go:` + relGlob + `"}`,
		`{"paths":"` + inRoot("src", "a.go") + ` ` + relGlob + `"}`,
		`{"paths":"` + inRoot("src", "a.go") + ` ` + inRoot("lnk", "*.txt") + `"}`,
		`{"paths":"` + inRoot("src", "a.go") + `\u0000` + inRoot("lnk", "*.txt") + `"}`,
		`{"paths":"src/a.go\u0000` + inRoot("lnk", "*.txt") + `"}`,
	} {
		t.Run(fmt.Sprintf("glob piece/link %d", i), func(t *testing.T) {
			require.Equal(t, "withheld", summaryVerdictBeside(t, root, link, "lnk/token.txt", s, []string{"token.txt"}), "%q selects lnk/token.txt", s)
		})
	}
	for _, s := range []string{
		`{"paths":"src/a.go src/*.go"}`,
		`{"paths":"` + inRoot("src", "a.go") + ` ` + inRoot("src", "*.go") + `"}`,
	} {
		require.Equal(t, "shown", summaryVerdictBeside(t, root, link, "lnk/token.txt", s, []string{"token.txt"}), "%q selects only project paths", s)
	}
}

// TestBuild_APathNamedValueWithMoreStretchesThanTheBoundIsWithheld bounds the stretch screen of fix
// round 1's review (valueReadings, globStretchSelectsKnown). Each stretch of a path-named value from
// a place a piece or a path starts to a place one ends is screened, and their number grows with the
// square of those places, so a value with more than maxValueStretches of them is withheld unread
// (coordinator decision D66(e): over-withholding is accepted, a stretch left unread is not); the
// store's 120-byte preview keeps any value near the bound cheap to read. A list of 21 paths glued by
// an NBSP, and a value of 22 glob pieces, are read and shown; 31 and 36 are withheld. With nothing to
// find, no rule's literal and no withheld path, no stretch is read and the bound does not apply.
func TestBuild_APathNamedValueWithMoreStretchesThanTheBoundIsWithheld(t *testing.T) {
	root := previewRoot("proj")
	nbsp := func(n int) string { return `{"paths":"a` + strings.Repeat("\u00a0a", n-1) + `"}` }
	globs := func(n int) string { return `{"paths":"a*` + strings.Repeat(" a*", n-1) + `"}` }
	rules := hostRules(root, "./.env")
	require.Equal(t, "shown", summaryVerdict(t, root, rules, nbsp(21), nil))
	require.Equal(t, "withheld", summaryVerdict(t, root, rules, nbsp(31), nil))
	env := refusingUnder(root, nil, func(rel string) bool { return rel == ".env" })
	require.Equal(t, "shown", summaryVerdictBeside(t, root, env, ".env", globs(22), nil))
	require.Equal(t, "withheld", summaryVerdictBeside(t, root, env, ".env", globs(36), nil))
	none := refusingUnder(root, nil, func(string) bool { return false })
	require.Equal(t, "shown", summaryVerdict(t, root, none, nbsp(31), nil))
	require.Equal(t, "shown", summaryVerdict(t, root, none, globs(36), nil))
}

// TestBuild_APathNamedValueNamingAPathBuiltAtRunTimeIsWithheld is fix round 2's review of candidate
// 8's diff verify (its minor finding, identical at 77374c3c). A path-named JSON value skips the
// free-text whitelist, and containment read only `$NAME`, `~`, `%VAR%` and `!VAR!` at a path start
// as rooted, so a name a shell builds at run time inside one was never resolved: a brace list (`{a,b}`,
// which bash, zsh and fish expand, as ADR 0011 §23 item 6 reads one in a one-word pattern), a command
// substitution (`$(…)`, a backtick pair) and any other expansion `$` starts (`${!x}`, `$1`). Each
// showed a path outside the project or a refused one: `~{,x}/.ssh/id_rsa` is `~/.ssh/id_rsa`,
// `$(pwd)/../other` a sibling of the project, and `.{env,x}` the `.env` that `Read(./.env)` refuses
// or the build learned as withheld. The same text as a one-word plain preview was withheld, and so
// was `$PWD/../other`. Each alternative of one brace list is now judged as the value is, a value
// holding a brace list that cannot be read as one level (a sequence, a nested or second list, one
// the store's cut left open) is withheld, and an expansion `$` or a backtick starts at a path start
// is rooted outside the project; a substitution starts a path anywhere in a piece (`..$(pwd)`), and
// any other expansion after a segment's run of dots (`..$HOME`). A brace list of project paths, a
// brace with no list in it and a `$` inside a name (a Java inner class, a Remix route) are still
// shown.
func TestBuild_APathNamedValueNamingAPathBuiltAtRunTimeIsWithheld(t *testing.T) {
	for _, elem := range [][]string{{"proj"}, {"John Smith", "proj"}, {"O'Brien (x)", "proj"}, {"R&D", "proj"}} {
		t.Run(filepath.Join(elem...), func(t *testing.T) {
			root := previewRoot(elem...)
			rules := hostRules(root, "./.env", "./secrets/**")
			leaks := []string{"passwd", "id_rsa", "secret.txt", "../other", "-old/x.txt"}
			for i, s := range []string{
				// The review's shapes.
				`{"paths":"~{,x}/.ssh/id_rsa"}`,
				`{"paths":"{,x}/etc/passwd"}`,
				`{"paths":".{.,x}/outside/secret.txt"}`,
				`{"directory":"$(pwd)/../other"}`,
				`{"cwd":"$(echo ~)/.ssh"}`,
				`{"notebook_path":"$(pwd)/../other/x.ipynb"}`,
				`{"paths":".{env,x}"}`,
				// The class: a substitution glued onto a sibling's name, a backtick pair, every other
				// expansion `$` starts, one in a later piece or after an option's `=`, one glued after
				// a climb, a brace list in a later piece or an array element, two lists, a sequence
				// (`{d..f}` is d, e and f; `{.../}` is `.` and `/`), a nested list, and the store's cut
				// inside a list or right after a `$`.
				`{"directory":"$(pwd)-old/x.txt"}`,
				"{\"directory\":\"`pwd`-old/x.txt\"}",
				`{"cwd":"${!x}/y"}`,
				`{"paths":"$1/x.txt"}`,
				`{"paths":"$@/x.txt"}`,
				`{"paths":"src/a.ts $(pwd)/../other"}`,
				`{"paths":"src/a.ts --dir=$(pwd)/../other"}`,
				`{"paths":"..$(pwd)"}`,
				"{\"paths\":\"..`pwd`\"}",
				`{"file":"..$HOME/x"}`,
				`{"paths":"src/a.ts .$(echo .)/x"}`,
				`{"paths":"src/a.ts {,x}/etc/passwd"}`,
				`{"paths":["src/a.ts","{,x}/etc/passwd"]}`,
				`{"paths":"src/{a,b}.ts {,x}/etc/passwd"}`,
				`{"paths":".{d..f}nv"}`,
				`{"paths":"{.../}etc/passwd"}`,
				`{"paths":"{a,{.,x}.}/outside/secret.txt"}`,
				`{"cell_id":"c1","paths":"~{,x}/.ss…`,
				`{"cell_id":"c1","paths":"src/{a,.en…`,
				`{"cell_id":"c1","paths":"src/a.ts $(pw…`,
				`{"cell_id":"c1","paths":"src/a.ts $…`,
				`{"cwd":"src","paths":"$(pwd)/../other"}`,
			} {
				t.Run(fmt.Sprintf("built at run time %d", i), func(t *testing.T) {
					require.Equal(t, "withheld", summaryVerdict(t, root, rules, s, leaks), "%q names a path built at run time", s)
				})
			}
			env := refusingUnder(root, nil, func(rel string) bool { return rel == ".env" })
			for i, s := range []string{
				`{"files":".{env,x}"}`,
				`{"files":"{README.md,.env}"}`,
				`{"files":"src/a.ts .{env,x}"}`,
			} {
				t.Run(fmt.Sprintf("learned .env %d", i), func(t *testing.T) {
					require.Equal(t, "withheld", summaryVerdictBeside(t, root, env, ".env", s, nil), "%q names the learned .env", s)
				})
			}
			for _, s := range []string{
				`{"paths":"src/{a,b}.ts"}`,
				`{"paths":"src/{a,b}.ts docs/b.md"}`,
				`{"paths":"**/*.{ts,tsx}"}`,
				`{"file":"templates/{{name}}/x.txt"}`,
				`{"file":"docs/{draft}.md"}`,
				`{"file":"build/classes/Outer$Inner.class"}`,
				`{"file":"app/routes/users.$userId.tsx"}`,
				`{"cell_id":"c1","paths":"src/{a,b}.ts src/c…`,
			} {
				require.Equal(t, "shown", summaryVerdict(t, root, rules, s, nil), "%q names only project paths", s)
				require.Equal(t, "shown", summaryVerdictBeside(t, root, env, ".env", s, nil), "%q names only project paths", s)
			}
		})
	}
}
