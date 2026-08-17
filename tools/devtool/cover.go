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

// landedSubplans names every subplan whose packages are real implementations rather than stubs, and
// whose 00-ARCHITECTURE.md §6.4 coverage floors therefore apply. A subplan adds itself here in the
// commit that lands it; until then its packages are exempt and say so in the job log.
//
// This is the one place the exemption is not derived from OWNERS.tsv, because OWNERS.tsv records
// who OWNS a package and not whether it has been written yet, and the probe column cannot tell the
// difference for a stub that returns a zero value rather than core.ErrNotImplemented. See taskCover
// for the two packages that proved it.
// Wave 1 merges in the plans/README.md order SP-05, SP-03, SP-04, SP-06, SP-07, SP-02, and each
// merge adds its own entry here. The three below are the three that have landed at this point;
// SP-06, SP-07 and SP-02 add themselves in turn. Adding one early binds a floor against a package
// that is still a stub, and forgetting one leaves a shipped package exempt at any coverage — the
// cross-check in taskCover catches the second case and is the reason it exists.
var landedSubplans = map[string]bool{
	"SP-01": true,
	"SP-03": true,
	"SP-04": true,
	"SP-05": true,
}

// probeBlind names the packages whose OWNERS.tsv probe cannot tell a stub from an implementation,
// so the "still exempt, therefore still a stub" cross-check below has to skip them. Each entry is
// a fact about the probe's SHAPE, not a judgement about the package, and each should disappear when
// its subplan lands and the package gets a real floor.
var probeBlind = map[string]bool{
	// Evaluate returns a Decision and no error, so a stub returns a zero value rather than
	// core.ErrNotImplemented and isBareNotImplementedStub cannot see it.
	"scheduler": true,
	// Append has an empty body — no return statement at all — for the same reason.
	"grammar": true,
	// RunAll and Redact are partly real at V1: SP-01 shipped working bodies that SP-05 and SP-06
	// will extend, so "not a stub" is already true and says nothing about whether they have landed.
	"contract": true,
	"redact":   true,
}

// taskCover runs the full test suite under coverage, then applies the 00-ARCHITECTURE.md §6.4
// per-group floors. A package is exempt only while it is STILL A STUB; the exemption is mechanical
// (OWNERS.tsv's probe column, parsed by probeStillStub) rather than a judgement call, and is
// printed so it is visible in the job log.
//
// It was originally spelled `o.Owner != "SP-01"`, which was the same rule while SP-01 was the only
// subplan that had landed: every other package was a stub, so owner and stubness coincided. SP-04
// is what separated them. It implements chunk, canon and symbols, and under the owner test those
// three would have gone on being exempt forever — their 90/90/75 floors silently unenforced from
// the moment they were most worth enforcing, with the job log still calling them stubs.
//
// Deriving "has landed" from the probe alone does NOT work, and the two packages that prove it are
// worth naming: scheduler's probe Evaluate returns a Decision and no error, and grammar's probe
// Append has an empty body, so isBareNotImplementedStub — which looks for a lone
// core.ErrNotImplemented return — reports neither as a stub even though both are. A probe-only
// rule therefore turns SP-12's and SP-15's floors on years early and fails the gate on work nobody
// has started. landedSubplans is the explicit half instead: one line, added by the subplan that
// lands, reviewed in the commit that lands it.
//
// For a landed subplan a stub probe is a hard failure rather than an exemption: a package its own
// owner has already shipped must not look like a stub, which is what catches a body reverted or
// never written.
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
		if !dirExists(dir) {
			fmt.Printf("not yet present: %s\n", o.Package)
			continue
		}

		if isCompositionRoot(dir) {
			fmt.Printf("exempt (composition root, §6.4): %s\n", o.Package)
			continue
		}

		if !landedSubplans[o.Owner] {
			// A package that is exempt because its subplan has not landed must still LOOK like a
			// stub. When it stops looking like one, its subplan has landed and nobody updated
			// landedSubplans — so its floor is silently off at exactly the moment it starts
			// mattering. That is not hypothetical: SP-02 and SP-03 land in the same wave-1 merge
			// as SP-04, and without this the eval and sketch floors would stay exempt with the job
			// log still calling them stubs, which is the failure this whole function was rewritten
			// to stop happening once already.
			//
			// probeBlind is the escape hatch for the packages whose probe shape carries no signal
			// either way; it is deliberately a short, named list rather than a silent skip.
			if o.Probe != "-" && !probeBlind[o.Package] && !probeStillStub(dir, o.Probe) {
				problems = append(problems, fmt.Sprintf(
					"%s: plans/OWNERS.tsv assigns this package to %s, which tools/devtool/cover.go's "+
						"landedSubplans does not list as landed, but its probe %q is no longer a bare "+
						"core.ErrNotImplemented stub. If %s has landed, add it to landedSubplans so its "+
						"§6.4 floor is enforced; if the probe simply cannot be read, add %s to probeBlind "+
						"with a one-line reason",
					o.Package, o.Owner, o.Probe, o.Owner, o.Package))
			}
			fmt.Printf("exempt (stub, owned by %s): %s\n", o.Owner, o.Package)
			continue
		}

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
