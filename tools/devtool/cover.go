package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// coverProfileName is the coverage profile file `cover` writes at the repository root, matching
// the path the `cover` CI job uploads as an artifact.
const coverProfileName = "coverage.out"

// taskCover runs the full test suite under coverage, then applies the 00-ARCHITECTURE.md §6.4
// per-group floors to every package whose implementation has actually landed. floorApplies decides
// which those are, and prints the reason whenever a floor is skipped so the exemption is visible in
// the job log rather than implied by a package's absence from it.
func taskCover(args []string) error {
	if err := goInherit("test", "-coverprofile="+coverProfileName, "-covermode=atomic", "./..."); err != nil {
		return fmt.Errorf("cover: go test -coverprofile: %w", err)
	}

	owners, err := loadOwners(filepath.Join(root, "plans", "OWNERS.tsv"))
	if err != nil {
		return fmt.Errorf("cover: %w", err)
	}
	byPkg, err := parseCoverProfile(filepath.Join(root, coverProfileName))
	if err != nil {
		return fmt.Errorf("cover: %w", err)
	}

	var problems []string
	for _, o := range owners {
		dir, path := packageDirAndPath(o.Package)
		if applies, why := floorApplies(o, dir); !applies {
			fmt.Println(why)
			continue
		}

		// A landed subplan that left its own probe as a bare core.ErrNotImplemented stub has not
		// landed. The heuristic only recognises the error-returning stub shape, so it can miss —
		// but it never fires falsely, which is the direction that matters for a merge blocker.
		if o.Probe != "-" && probeStillStub(dir, o.Probe) {
			problems = append(problems, fmt.Sprintf(
				"%s: plans/OWNERS.tsv assigns this package to %s, which has landed, but its probe %q still looks like a bare core.ErrNotImplemented stub",
				o.Package, o.Owner, o.Probe))
		}

		stat, ok := byPkg[path]
		if !ok {
			fmt.Printf("no coverage data: %s (not exercised by `go test ./...`)\n", o.Package)
			continue
		}
		pct := stat.pct()
		if pct+1e-9 < float64(o.Floor) {
			problems = append(problems, fmt.Sprintf(
				"%s: %.1f%% line coverage < floor %d%% (00-ARCHITECTURE.md §6.4)", o.Package, pct, o.Floor))
			continue
		}
		fmt.Printf("OK %s: %.1f%% >= floor %d%%\n", o.Package, pct, o.Floor)
	}

	if len(problems) == 0 {
		return nil
	}
	for _, p := range problems {
		fmt.Println("  " + p)
	}
	return fmt.Errorf("cover: %d package(s) below floor or still stubbed", len(problems))
}

// landedSubplans lists the subplans whose implementations are on develop. A package's §6.4
// coverage floor binds once its owner appears here; before that the package is a stub and its
// coverage percentage measures nothing. Each subplan adds its own ID in the commit that lands it —
// the same flip Rule W-1 already requires for the conformance-suite skips, and enforced the same
// way, by a test that fails when the set and the branch disagree.
//
// Inferring this from the OWNERS.tsv probe instead was tried and does not work. probeStillStub can
// only recognise a stub that returns core.ErrNotImplemented, and several interface methods return
// a value rather than an error — chunk.Split returns nil, grammar.Append returns nothing — so the
// inference reads those stubs as landed while packages whose probe is declared on an inner type it
// cannot find read as landed too. It gates code nobody has written and exempts code that shipped,
// which is worse than an explicit list on both counts.
var landedSubplans = map[string]bool{
	"SP-01": true,
	"SP-02": true,
}

// floorApplies reports whether o's §6.4 coverage floor binds right now, and when it does not, the
// line to print explaining why. Exemptions are printed rather than silently skipped, so a package
// missing from the job log is a bug in this function and not an intended state.
func floorApplies(o ownerRow, dir string) (applies bool, exemption string) {
	switch {
	case !dirExists(dir):
		return false, fmt.Sprintf("not yet present: %s", o.Package)
	case isCompositionRoot(dir):
		return false, fmt.Sprintf("exempt (composition root, §6.4): %s", o.Package)
	case !landedSubplans[o.Owner]:
		return false, fmt.Sprintf("exempt (stub, owned by %s): %s", o.Owner, o.Package)
	}
	return true, ""
}

// isCompositionRoot reports whether pkgDir is a `main` package that declares nothing but `func
// main` — the narrow 00-ARCHITECTURE.md §6.4 coverage exemption. Such a package ends in os.Exit,
// which no in-process test can survive, so its coverage is necessarily 0.0% and the behaviour is
// credited to the test/e2e package that spawns the real binary instead.
//
// The check is deliberately strict on both axes the amendment names. Every non-test file in the
// directory must be `package main`, and across all of them the only declaration permitted is
// `func main` — no other function, no method, no package-level var, const or type. A second
// declaration is somewhere a bug can hide, and the floor applies again the moment one appears.
// Anything unreadable or unparseable returns false, so the failure mode is a reported floor
// violation rather than a silently skipped package.
func isCompositionRoot(pkgDir string) bool {
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return false
	}
	sawMain := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, filepath.Join(pkgDir, e.Name()), nil, 0)
		if perr != nil || f.Name.Name != "main" {
			return false
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				// A GenDecl that is only imports is structure, not logic; anything else
				// (var, const, type) is a declaration the exemption does not cover.
				if gd, isGen := decl.(*ast.GenDecl); isGen && gd.Tok == token.IMPORT {
					continue
				}
				return false
			}
			if fn.Recv != nil || fn.Name.Name != "main" {
				return false
			}
			sawMain = true
		}
	}
	return sawMain
}

// packageDirAndPath maps an OWNERS.tsv package key to its on-disk directory and full import path.
func packageDirAndPath(ownersKey string) (dir, importPath string) {
	if ownersKey == "cmd/qompack" {
		return filepath.Join(root, "cmd", "qompack"), modulePath + "/cmd/qompack"
	}
	return filepath.Join(root, "internal", ownersKey), modulePath + "/internal/" + ownersKey
}

// coverStat accumulates Go cover-profile statement counts for one package.
type coverStat struct{ covered, total int }

func (c coverStat) pct() float64 {
	if c.total == 0 {
		return 100
	}
	return 100 * float64(c.covered) / float64(c.total)
}

// parseCoverProfile reads a `go test -coverprofile` file (format:
// "mode: <mode>\n<file>:<startLine>.<startCol>,<endLine>.<endCol> <numStmt> <count>\n...") and
// aggregates statement counts per package import path.
func parseCoverProfile(path string) (map[string]coverStat, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening coverage profile: %w", err)
	}
	defer func() { _ = f.Close() }()

	stats := make(map[string]coverStat)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			continue // "mode: atomic" header
		}
		file, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 3 {
			continue
		}
		numStmt, err1 := strconv.Atoi(fields[1])
		count, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			continue
		}
		pkg := importPathFromCoverFile(file)
		s := stats[pkg]
		s.total += numStmt
		if count > 0 {
			s.covered += numStmt
		}
		stats[pkg] = s
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading coverage profile: %w", err)
	}
	return stats, nil
}

// importPathFromCoverFile strips the trailing "/file.go" a cover-profile line names, leaving the
// package's import path.
func importPathFromCoverFile(f string) string {
	i := strings.LastIndex(f, "/")
	if i < 0 {
		return f
	}
	return f[:i]
}

// probeStillStub is a static, conservative heuristic: it looks for a function or method literally
// named probeName in pkgDir's non-test source, and reports true only when that declaration's
// entire body is a single return statement mentioning core.ErrNotImplemented — the exact shape
// every SP-01 interface stub has (implementation spec, §14's stub rules). Anything more elaborate
// (any other statement, any branch) is treated as "probably a real implementation", because a
// false "still stubbed" verdict would incorrectly block an unrelated subplan's merge.
//
// This has to be static rather than a dynamic/reflective probe: devtool is compiled today, before
// most target packages exist, so it can never import them (doing so would break `go build
// ./tools/...` the moment this file was added). A registry-based reflective probe, in the style of
// test/guards' TestAllStubsReturnNotImplemented, belongs in test/guards once the whole tree exists
// — see the implementation spec §18.
func probeStillStub(pkgDir, probeName string) bool {
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, filepath.Join(pkgDir, e.Name()), nil, 0)
		if perr != nil {
			continue
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != probeName || fn.Body == nil {
				continue
			}
			return isBareNotImplementedStub(fn.Body)
		}
	}
	return false // no matching declaration found anywhere: can't tell, don't block
}

// isBareNotImplementedStub reports whether body is exactly one return statement, at least one of
// whose results mentions core.ErrNotImplemented.
func isBareNotImplementedStub(body *ast.BlockStmt) bool {
	if len(body.List) != 1 {
		return false
	}
	ret, ok := body.List[0].(*ast.ReturnStmt)
	if !ok {
		return false
	}
	for _, r := range ret.Results {
		if exprMentionsNotImplemented(r) {
			return true
		}
	}
	return false
}

// exprMentionsNotImplemented reports whether e is (or wraps, via a single call such as
// fmt.Errorf) a reference to an identifier named ErrNotImplemented.
func exprMentionsNotImplemented(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.SelectorExpr:
		return v.Sel.Name == "ErrNotImplemented"
	case *ast.CallExpr:
		if exprMentionsNotImplemented(v.Fun) {
			return true
		}
		for _, a := range v.Args {
			if exprMentionsNotImplemented(a) {
				return true
			}
		}
	}
	return false
}
