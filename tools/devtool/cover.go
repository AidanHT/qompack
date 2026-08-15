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
// per-group floors — but only to packages plans/OWNERS.tsv assigns to SP-01. Every other
// package's floor is exempt until its own owner lands; the exemption is mechanical (read from
// OWNERS.tsv), not a judgement call, and is printed so it is visible in the job log.
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
		if o.Owner != "SP-01" {
			fmt.Printf("exempt (stub, owned by %s): %s\n", o.Owner, o.Package)
			continue
		}

		dir, path := packageDirAndPath(o.Package)
		if !dirExists(dir) {
			fmt.Printf("not yet present: %s\n", o.Package)
			continue
		}

		if o.Probe != "-" && probeStillStub(dir, o.Probe) {
			problems = append(problems, fmt.Sprintf(
				"%s: plans/OWNERS.tsv assigns this package to SP-01, but its probe %q still looks like a bare core.ErrNotImplemented stub",
				o.Package, o.Probe))
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
