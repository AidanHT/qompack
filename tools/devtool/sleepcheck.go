package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// runSleepCheck is the `devtool lint` sub-check for 00-ARCHITECTURE.md §6.1's "wall-clock sleeps
// are banned": a go/ast scan for time.Sleep selector calls in every file except those under
// test/bench/ — including _test.go files, which golangci-lint's own forbidigo rule for time.Sleep
// does not reach (.golangci.yml excludes forbidigo on _test.go, test/ and tools/ paths).
func runSleepCheck() error {
	var hits []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return sleepCheckDirDecision(path, d.Name())
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		found, ferr := scanFileForSleep(path)
		if ferr != nil {
			return ferr
		}
		hits = append(hits, found...)
		return nil
	})
	if err != nil {
		return fmt.Errorf("sleepcheck: %w", err)
	}
	if len(hits) == 0 {
		fmt.Println("sleepcheck: OK")
		return nil
	}
	sort.Strings(hits)
	for _, h := range hits {
		fmt.Println("  " + h)
	}
	return fmt.Errorf("sleepcheck: %d call(s) to time.Sleep outside test/bench (00-ARCHITECTURE.md §6.1)", len(hits))
}

// sleepCheckDirDecision decides whether WalkDir should skip a directory entirely: version control
// metadata, any dot-directory, every "testdata" directory (go tool convention: never contains
// checked source), and test/bench/** (the one place §6.1 exempts, because the hotpath harness
// must measure real wall-clock spawns). It reports against the package-level root, since
// runSleepCheck always walks from there.
func sleepCheckDirDecision(path, name string) error {
	if name == ".git" {
		return filepath.SkipDir
	}
	if strings.HasPrefix(name, ".") && path != root {
		return filepath.SkipDir
	}
	if name == "testdata" {
		return filepath.SkipDir
	}
	rel, relErr := filepath.Rel(root, path)
	if relErr == nil {
		rel = filepath.ToSlash(rel)
		if rel == "test/bench" || strings.HasPrefix(rel, "test/bench/") {
			return filepath.SkipDir
		}
	}
	return nil
}

// scanFileForSleep parses one Go source file and returns a "file:line: …" entry for every call
// whose selector is <name imported as "time">.Sleep(...).
func scanFileForSleep(path string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	timeNames := map[string]bool{}
	for _, imp := range f.Imports {
		p, uerr := strconv.Unquote(imp.Path.Value)
		if uerr != nil || p != "time" {
			continue
		}
		name := "time"
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name != "_" && name != "." {
			timeNames[name] = true
		}
	}
	if len(timeNames) == 0 {
		return nil, nil
	}

	var hits []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if sel.Sel.Name == "Sleep" && timeNames[ident.Name] {
			pos := fset.Position(call.Pos())
			hits = append(hits, fmt.Sprintf("%s:%d: time.Sleep call (banned outside test/bench — take a core.Clock, §6.1)",
				relOrSelf(root, pos.Filename), pos.Line))
		}
		return true
	})
	return hits, nil
}

// relOrSelf renders p relative to base for a friendlier report, falling back to p itself if that
// fails.
func relOrSelf(base, p string) string {
	r, err := filepath.Rel(base, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}
