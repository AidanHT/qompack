package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// The two checks in this file exist because V2-VERIFY spent five passes finding the same class of
// defect by hand: a gate that reports success while checking nothing. Twenty-five instances were
// recorded (plans/V2-report.md, "Silently-disabled gates found"), and two of them are mechanical
// enough that no human should ever have to find them again:
//
//   - A `-run` pattern that matches zero tests. `go test -run` prints "ok" and exits 0 when its
//     pattern selects nothing, so a plan row whose command names a test that was renamed, moved to
//     another package, or never written at all reads as a passing verification step forever. This
//     bit the checkpoint at V2-SP04-12 (three named property tests that never existed), V2-MERGE-17,
//     V2-SP06 rows 05/21/22/26, V2-SP05 rows 21/23, V2-SP07 rows -02/-15, §3.2(c) and V2-SP02 row 04.
//
//   - A markdown-escaped pipe inside a raw `-run` pattern. RE2 reads `\|` as a literal pipe, not as
//     alternation, so `-run 'TestA\|TestB'` matches the single test named "TestA|TestB" — that is,
//     nothing — and passes silently. Inside a markdown TABLE ROW the escape is correct and required
//     (an unescaped pipe would end the cell), and the rendered document a human copies from shows a
//     bare pipe; everywhere else the escape is a live bug. Commit 84ccbfa corrected every non-table
//     instance by hand. This check keeps them corrected.
//
// Neither check can be satisfied by writing prose. Both fail the build.

// planDocRoots are the directories whose markdown is treated as specification text.
var planDocRoots = []string{"plans"}

// goTestCmd finds a `go test` invocation and captures the rest of its command span. The span ends at
// the first character that cannot be part of the same simple command: a backtick (the markdown code
// span closed), a semicolon or ampersand (shell separator), or end of line. An unescaped pipe also
// ends it, because that is a shell pipeline rather than part of the pattern; an ESCAPED pipe does
// not, because inside a table cell that is how a literal pipe is written and it may well be sitting
// inside the -run argument this check exists to inspect.
var goTestCmd = regexp.MustCompile(`go test((?:\\\||[^` + "`" + `;&|\n])*)`)

// runFlag captures the argument of -run in each of the three forms the plans use: single-quoted,
// double-quoted, or bare.
var runFlag = regexp.MustCompile(`-run[= ]+('[^']*'|"[^"]*"|[^\s]+)`)

// pkgArg captures a package path argument. Plans always spell these ./-relative.
var pkgArg = regexp.MustCompile(`(\./[^\s'"` + "`" + `]*)`)

// shellVar spots a shell variable expansion, which makes a token unresolvable at lint time. `$p` in
// `./internal/$p` is a loop variable; `$` alone (or `$` before a non-word character) is the regexp
// end-anchor and is left alone.
var shellVar = regexp.MustCompile(`\$[A-Za-z_{(]`)

// subplanInFilename pulls SP-nn out of a plan document's name.
var subplanInFilename = regexp.MustCompile(`SP-(\d\d)`)

// waveInFilename pulls the leading V<n> wave number out of a plan document's name.
var waveInFilename = regexp.MustCompile(`^V(\d)-`)

// planRunPattern is one `-run` argument found in a plan document, with the position that produced it
// so a failure names a place a human can open.
type planRunPattern struct {
	file    string
	line    int
	pattern string // the -run argument, unquoted, and unescaped when it came from a table row
	pkg     string // the ./-relative package the command targets, or "" if it named none
	escaped bool   // the raw argument contained a backslash-escaped pipe
	inTable bool   // the source line is a markdown table row, where that escape is correct
	waiver  string // non-empty when the line carries a runpatterns waiver, holding its reason
}

// parsePlanRunPatterns extracts every statically resolvable `go test -run` from one document.
//
// "Statically resolvable" is doing real work here: a command inside a shell loop (`for p in ...; do
// go test ./internal/$p`) names a different package on each iteration and there is nothing to check,
// so it is dropped rather than guessed at. Dropping is safe for this check's purpose because the
// silent-pass class it hunts is a pattern that matches nothing ANYWHERE, and a loop body is not
// where those hide — every instance the checkpoint found by hand was a fixed, literal command.
// Fenced code blocks are deliberately NOT exempt here, unlike in findPlanMarkers. A fence is where
// the plans keep their most literally runnable commands, so exempting it would leave the check
// looking green while ignoring the majority of what it is supposed to read — the exact shape of the
// defect this file exists to prevent. A fence also renders verbatim, so a `\|` inside one is a live
// bug rather than markdown escaping, and the inTable rule below already gets that right.
func parsePlanRunPatterns(file, content string) []planRunPattern {
	var out []planRunPattern
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	// A waiver written on the line just before a fenced block covers that whole block, so a shell
	// transcript demonstrating a broken command can be waived from the prose above it instead of
	// having an HTML comment wedged into the middle of the transcript.
	pending, blockWaiver := "", ""
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := scanner.Text()
		lineWaiver := ""
		if w := runPatternWaiver.FindStringSubmatch(line); w != nil {
			lineWaiver = strings.TrimSpace(w[1])
		}
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if blockWaiver != "" {
				blockWaiver = ""
			} else {
				blockWaiver = pending
			}
			pending = ""
			continue
		}
		pending = lineWaiver
		// A table row is any line whose first non-space character is a pipe. Inside one, `\|` is
		// markdown escaping and the rendered text carries a bare pipe.
		inTable := strings.HasPrefix(strings.TrimSpace(line), "|")
		waiver := lineWaiver
		if waiver == "" {
			waiver = blockWaiver
		}
		for _, cmd := range goTestCmd.FindAllStringSubmatch(line, -1) {
			span := cmd[1]
			rf := runFlag.FindStringSubmatch(span)
			if rf == nil {
				continue
			}
			raw := strings.Trim(rf[1], `'"`)
			if shellVar.MatchString(raw) {
				continue
			}
			p := planRunPattern{file: file, line: lineNo, inTable: inTable, waiver: waiver}
			p.escaped = strings.Contains(raw, `\|`)
			p.pattern = raw
			if inTable {
				p.pattern = strings.ReplaceAll(raw, `\|`, "|")
			}
			// The package argument is whichever ./-path appears in the same command span. -run's
			// own argument is excluded first so a pattern containing a slash cannot be mistaken
			// for one.
			rest := strings.Replace(span, rf[0], " ", 1)
			if m := pkgArg.FindStringSubmatch(rest); m != nil && !shellVar.MatchString(m[1]) {
				p.pkg = strings.TrimSuffix(m[1], "/")
			}
			out = append(out, p)
		}
	}
	return out
}

// matchesNoTest reports whether pattern selects none of names under `go test -run` semantics.
//
// -run splits its argument on "/" and matches each element, unanchored, against the corresponding
// element of a test's name path. Only the first element can be judged here, because `go test -list`
// enumerates top-level functions and subtests exist only at run time — but the first element is
// enough: a pattern whose head matches nothing selects nothing no matter what follows it, and that
// is exactly the failure this check exists to catch.
func matchesNoTest(pattern string, names []string) (bool, error) {
	head := pattern
	if i := strings.Index(head, "/"); i >= 0 {
		head = head[:i]
	}
	re, err := regexp.Compile(head)
	if err != nil {
		return false, fmt.Errorf("pattern %q is not a valid regexp: %w", pattern, err)
	}
	for _, n := range names {
		if re.MatchString(n) {
			return false, nil
		}
	}
	return true, nil
}

// deliberateNoMatch holds the patterns that are SUPPOSED to select nothing. Both are the documented
// way to run a package's fuzz, bench or list step without also running its tests: `-run '^$'` says
// so with an anchor pair, and `-run=XXX` is Go's own long-standing spelling of the same idea.
var deliberateNoMatch = map[string]bool{
	"^$":  true,
	"XXX": true,
}

// runPatternWaiver marks a line whose command is quoted BECAUSE it is broken — a handoff note
// demonstrating a defect, or a checkpoint row recording one. Those lines are documentation of an
// unsatisfiable command, not an instruction to run it, and failing on them would make it impossible
// to write the defect down.
//
// The reason is mandatory and every use is printed, which is the same bargain stubskips.go strikes
// with its `platform: ` prefix: an escape hatch that must be written deliberately, says why, and
// shows up in the job log is a recorded decision. A silent one is a hole.
var runPatternWaiver = regexp.MustCompile(`<!--\s*runpatterns:\s*([^->][^>]*?)\s*-->`)

// testFuncDecl finds a test, benchmark, fuzz or example function declaration in Go source.
var testFuncDecl = regexp.MustCompile(`(?m)^func ((?:Test|Benchmark|Fuzz|Example)[A-Za-z0-9_]*)\s*\(`)

// planDocsInScope decides which documents can have their patterns resolved against the tree.
//
// A plan for a subplan nobody has written yet names tests nobody has written yet, so checking it
// would fail on correct documents. Scope is derived, never hand-maintained: the wave a document
// belongs to comes from its filename, the subplans in each wave come from the SP-nn documents'
// filenames, and a wave is checkable once landedSubplans says every one of its subplans has landed.
// When a wave lands, its plans come into scope by themselves — there is no second list to forget.
//
// The escaped-pipe check does NOT consult this: a literal pipe in a regexp is wrong the day it is
// written, whether or not the test it names exists yet, and catching it in an unlanded wave's plan
// is the whole point of having a machine do it.
func planDocsInScope(files []string) map[string]bool {
	waveSubplans := map[string]map[string]bool{}
	for _, f := range files {
		base := filepath.Base(f)
		w := waveInFilename.FindStringSubmatch(base)
		if w == nil {
			continue
		}
		if m := subplanInFilename.FindStringSubmatch(base); m != nil {
			if waveSubplans[w[1]] == nil {
				waveSubplans[w[1]] = map[string]bool{}
			}
			waveSubplans[w[1]]["SP-"+m[1]] = true
		}
	}
	waveLanded := map[string]bool{}
	for wave, subplans := range waveSubplans {
		landed := true
		for sp := range subplans {
			if !landedSubplans[sp] {
				landed = false
			}
		}
		waveLanded[wave] = landed
	}

	scope := map[string]bool{}
	for _, f := range files {
		base := filepath.Base(f)
		w := waveInFilename.FindStringSubmatch(base)
		if w == nil {
			// A document with no wave prefix (00-ARCHITECTURE.md, README.md, TRACEABILITY.md)
			// describes the tree as it stands and is always in scope.
			scope[f] = true
			continue
		}
		// A document is in scope once its own wave and every wave before it has landed. The
		// ordering matters for VERIFY documents, which check everything up to their own wave.
		inScope := true
		for wave, landed := range waveLanded {
			if wave <= w[1] && !landed {
				inScope = false
			}
		}
		scope[f] = inScope
	}
	return scope
}

// planMarker is an unfilled placeholder left in a specification document.
type planMarker struct {
	file  string
	line  int
	token string
}

// markerToken matches an angle-bracketed all-caps placeholder: a head of capitals, digits, hyphens
// and underscores, optionally followed by a whitespace-introduced descriptive tail.
//
// The head must run all the way to the closing bracket or to a space, which is what keeps CamelCase
// metavariables out. These documents are full of them — <TempDir>, <ExactTestName>, <ToolDisplay>,
// <Filename(seq)>, <RFC3339.mmm> — and every one stands for a value the reader supplies rather than
// for text an author failed to write. A head of `T` followed by `empDir` is not a placeholder; a
// head of `COLLISION-LOG` followed by a prose description is.
var markerToken = regexp.MustCompile(`<([A-Z][A-Z0-9_-]*)(\s[^>\n]*)?>`)

// markerNotation lists the heads that LOOK like placeholders but are established notation in these
// documents, standing for a class of value rather than for text somebody forgot to write.
//
// Every other head that carries a hyphen or a descriptive tail is treated as unfilled. That rule is
// not cosmetic: plans/V2-report.md shipped, was committed, merged and tagged carrying
// `<COLLISION-LOG — one line per §2.0a row; evidence …>` and `<INBOUND-DISPOSITIONS — …>` because
// the assembly script's own marker assertion used `<[A-Z][A-Z0-9-]*>`, which requires the closing
// bracket immediately after the capitals and therefore could not see either one. Short bare tokens
// like <N> and <EMAIL> are notation and pass; a hyphenated head or a prose tail is somebody's TODO.
var markerNotation = map[string]bool{
	"ISO-8601": true, // "<ISO-8601>" and "<ISO 8601 timestamp>" stand for a timestamp
	"ISO":      true,
	"SP-NN":    true, // "<SP-NN>" stands for any subplan id
	"SP":       true,
	"P":        true, // "<P nn>" stands for a numbered paragraph
}

// findPlanMarkers returns every unfilled placeholder in one document.
//
// Fenced code blocks are exempt: a shell or JSON sample may legitimately show <YOUR-TOKEN> as the
// thing a reader is meant to substitute. Prose is not exempt, because prose is where the report's
// two survivors were sitting.
func findPlanMarkers(file, content string) []planMarker {
	var out []planMarker
	inFence := false
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := scanner.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range markerToken.FindAllStringSubmatch(line, -1) {
			head, tail := m[1], m[2]
			if markerNotation[head] {
				continue
			}
			if !strings.Contains(head, "-") && strings.TrimSpace(tail) == "" {
				continue
			}
			out = append(out, planMarker{file: file, line: lineNo, token: m[0]})
		}
	}
	return out
}

// collectPlanDocs lists every markdown document under the plan roots.
func collectPlanDocs() ([]string, error) {
	var files []string
	for _, r := range planDocRoots {
		err := filepath.WalkDir(filepath.Join(root, r), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".md") {
				rel, relErr := filepath.Rel(root, p)
				if relErr != nil {
					return relErr
				}
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

// listTests returns the top-level test, benchmark, fuzz and example names of each named package.
//
// One `go test -list` invocation covers every package, so the test binaries are built once rather
// than once per pattern. Output is a run of names followed by the package's own status line.
func listTests(pkgs []string) (map[string][]string, error) {
	if len(pkgs) == 0 {
		return map[string][]string{}, nil
	}
	args := append([]string{"test", "-list", ".*"}, pkgs...)
	stdout, stderr, err := runCapture(nil, "go", args...)
	if err != nil {
		return nil, fmt.Errorf("go test -list failed: %w\n%s", err, stderr)
	}

	byPkg := map[string][]string{}
	var pending []string
	for _, line := range strings.Split(string(stdout), "\n") {
		line = strings.TrimRight(line, "\r")
		fields := strings.Fields(line)
		if len(fields) >= 2 && (fields[0] == "ok" || fields[0] == "?" || fields[0] == "FAIL") {
			// The status line names the package in Go import-path form; the plans name it
			// ./-relative. Key on the trailing segment path so both spellings meet.
			byPkg[fields[1]] = pending
			pending = nil
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pending = append(pending, strings.TrimSpace(line))
	}
	return byPkg, nil
}

// pkgDirRel converts a ./-relative package argument to a tree-relative directory, dropping a
// trailing `/...` wildcard and reporting that it did.
//
// Both callers need the distinction. The directory is what decides whether the package has landed
// in the tree yet, and `internal/cli/...` is not a directory — asking the filesystem about it is
// how ~130 plan commands used to be classified as "package not in tree yet" or, on a host whose
// path normalization swallows the trailing dots, as checkable-but-unresolvable.
func pkgDirRel(pkg string) (dir string, wildcard bool) {
	p := strings.TrimPrefix(pkg, "./")
	switch {
	case p == "..." || p == "":
		return ".", p == "..."
	case strings.HasSuffix(p, "/..."):
		return strings.TrimSuffix(p, "/..."), true
	default:
		return p, false
	}
}

// namesFor finds the listed names for a ./-relative package path among import-path-keyed results.
//
// The join is exact rather than a suffix match on purpose: matching ./internal/cli against any
// import path ENDING in /internal/cli would let Go's map iteration order decide which package's
// test list was read, and a check that silently inspects the wrong package is the precise failure
// this file exists to make impossible.
//
// A `/...` argument is the one case where several packages answer to one spelling, and it is
// unioned rather than looked up: `go test -run X ./tools/...` is satisfied when ANY package under
// tools declares a matching test, so the pattern is unsatisfiable only when none of them does.
// Before this was handled the lookup key carried the literal "/..." and could never hit any
// import path, so every wildcard command was dropped by the `!ok` branch below AFTER being counted
// as checked — a gate reporting success while checking nothing, which is the exact class this file
// exists to prevent.
func namesFor(byPkg map[string][]string, pkg string) ([]string, bool) {
	dir, wildcard := pkgDirRel(pkg)
	key := modulePath
	if dir != "." {
		key += "/" + dir
	}
	if !wildcard {
		names, ok := byPkg[key]
		return names, ok
	}
	var names []string
	found := false
	for k, v := range byPkg {
		if k != key && !strings.HasPrefix(k, key+"/") {
			continue
		}
		found = true
		names = append(names, v...)
	}
	sort.Strings(names)
	return names, found
}

// declaredTestFuncsUnder returns every declared test name in dir, and — when the plan command named
// a `/...` wildcard — in every directory beneath it, so the build-constraint fallback below reads
// the same package set the pattern itself selects.
func declaredTestFuncsUnder(dir string, recursive bool) ([]string, error) {
	if !recursive {
		return declaredTestFuncs(dir)
	}
	var names []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if base := filepath.Base(p); p != dir && (base == "testdata" || strings.HasPrefix(base, ".")) {
			return filepath.SkipDir
		}
		found, declErr := declaredTestFuncs(p)
		if declErr != nil {
			return declErr
		}
		names = append(names, found...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// declaredTestFuncs returns every test, benchmark, fuzz and example function DECLARED in a package
// directory, ignoring build constraints entirely.
//
// `go test -list` honours build tags, so on Windows it cannot see a test in a file marked
// `//go:build !windows` — internal/ipc/server_unix_test.go holds TestUnixSocketPermissions and
// TestStaleUnixSocketReclaimed, and V2-SP05-08's row names both. Failing there would be wrong: the
// row is correct and the test is real, it just cannot run on this host. Reading the declarations
// straight out of the source distinguishes "excluded on this platform" from "does not exist", which
// is the distinction the check actually needs — and it proves it from the tree rather than assuming
// it, so a genuinely missing test still fails.
func declaredTestFuncs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			return nil, readErr
		}
		for _, m := range testFuncDecl.FindAllStringSubmatch(string(b), -1) {
			names = append(names, m[1])
		}
	}
	sort.Strings(names)
	return names, nil
}

// runPlanRunPatterns is the `runpatterns` lint sub-check.
func runPlanRunPatterns() error {
	files, err := collectPlanDocs()
	if err != nil {
		return err
	}
	scope := planDocsInScope(files)

	var patterns []planRunPattern
	for _, f := range files {
		b, readErr := os.ReadFile(filepath.Join(root, f))
		if readErr != nil {
			return readErr
		}
		patterns = append(patterns, parsePlanRunPatterns(f, string(b))...)
	}

	var problems []string
	var waived []string

	// Phase 1 — escaped pipes, over every document regardless of scope.
	for _, p := range patterns {
		if !p.escaped || p.inTable {
			continue
		}
		if p.waiver != "" {
			waived = append(waived, fmt.Sprintf("%s:%d: escaped pipe — %s", p.file, p.line, p.waiver))
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"%s:%d: -run %q contains a backslash-escaped pipe outside a markdown table; RE2 reads "+
				"\\| as a literal pipe, so this pattern matches nothing and `go test` exits 0. Write a bare |.",
			p.file, p.line, p.pattern))
	}

	// Phase 2 — zero-match, over in-scope documents whose package exists in the tree.
	wanted := map[string]bool{}
	var checkable []planRunPattern
	skippedUnlanded := 0
	for _, p := range patterns {
		if !scope[p.file] || p.pkg == "" || deliberateNoMatch[p.pattern] {
			continue
		}
		if p.waiver != "" {
			waived = append(waived, fmt.Sprintf("%s:%d: %q — %s", p.file, p.line, p.pattern, p.waiver))
			continue
		}
		if dir, _ := pkgDirRel(p.pkg); !dirExists(filepath.Join(root, filepath.FromSlash(dir))) {
			skippedUnlanded++
			continue
		}
		wanted[p.pkg] = true
		checkable = append(checkable, p)
	}

	pkgs := make([]string, 0, len(wanted))
	for p := range wanted {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	byPkg, err := listTests(pkgs)
	if err != nil {
		return err
	}
	platformExcluded, resolved := 0, 0
	for _, p := range checkable {
		names, ok := namesFor(byPkg, p.pkg)
		if !ok {
			// Not a silent skip. Reaching here means the pattern survived every filter above —
			// in scope, package on disk, no waiver — and then found no `go test -list` status
			// line to check against, so counting it as verified would be the silent pass this
			// check exists to prevent.
			problems = append(problems, fmt.Sprintf(
				"%s:%d: -run %q names package %s, which `go test -list` produced no status line for, "+
					"so this pattern was counted as checked while nothing checked it.",
				p.file, p.line, p.pattern, p.pkg))
			continue
		}
		resolved++
		empty, matchErr := matchesNoTest(p.pattern, names)
		if matchErr != nil {
			problems = append(problems, fmt.Sprintf("%s:%d: %v", p.file, p.line, matchErr))
			continue
		}
		if !empty {
			continue
		}
		// Nothing listed. Before failing, ask whether the test is merely excluded by a build
		// constraint on this host rather than absent from the tree.
		dir, wildcard := pkgDirRel(p.pkg)
		declared, declErr := declaredTestFuncsUnder(filepath.Join(root, filepath.FromSlash(dir)), wildcard)
		if declErr != nil {
			return declErr
		}
		if alsoEmpty, _ := matchesNoTest(p.pattern, declared); !alsoEmpty {
			platformExcluded++
			fmt.Printf("runpatterns: %s:%d: -run %q is declared in %s but excluded by a build constraint on %s/%s — not checked here\n",
				p.file, p.line, p.pattern, p.pkg, runtime.GOOS, runtime.GOARCH)
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"%s:%d: -run %q matches no test in %s; `go test` prints ok and exits 0 for this "+
				"command, so the row it documents verifies nothing.",
			p.file, p.line, p.pattern, p.pkg))
	}

	// The resolved count is the number of patterns that actually reached a test list, not the number
	// that entered the loop: those differed by every wildcard command before namesFor learned to
	// expand one, and the summary line reporting the larger number is what made the hole invisible.
	fmt.Printf("runpatterns: %d -run patterns parsed, %d of %d checkable resolved against %d packages, %d skipped (package not in tree yet), %d platform-excluded, %d waived\n",
		len(patterns), resolved, len(checkable), len(pkgs), skippedUnlanded, platformExcluded, len(waived))
	if len(waived) > 0 {
		sort.Strings(waived)
		fmt.Printf("runpatterns: waivers in force:\n  %s\n", strings.Join(waived, "\n  "))
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("runpatterns: %d unsatisfiable -run pattern(s):\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
	return nil
}

// runPlanMarkers is the `docmarkers` lint sub-check.
func runPlanMarkers() error {
	files, err := collectPlanDocs()
	if err != nil {
		return err
	}
	var found []planMarker
	for _, f := range files {
		b, readErr := os.ReadFile(filepath.Join(root, f))
		if readErr != nil {
			return readErr
		}
		found = append(found, findPlanMarkers(f, string(b))...)
	}
	fmt.Printf("docmarkers: %d plan document(s) scanned\n", len(files))
	if len(found) > 0 {
		lines := make([]string, 0, len(found))
		for _, m := range found {
			lines = append(lines, fmt.Sprintf("%s:%d: unfilled placeholder %s", m.file, m.line, m.token))
		}
		sort.Strings(lines)
		return fmt.Errorf("docmarkers: %d unfilled placeholder(s) left in specification documents:\n  %s",
			len(found), strings.Join(lines, "\n  "))
	}
	return nil
}
