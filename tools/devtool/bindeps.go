package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// releaseTargets are the six GOOS/GOARCH pairs of 00-ARCHITECTURE.md §2.6, shared by build-all
// and bindeps (bindeps runs the dependency check on all six so a build-tagged import cannot sneak
// onto one platform unnoticed).
var releaseTargets = []struct{ GOOS, GOARCH string }{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"windows", "amd64"},
	{"windows", "arm64"},
}

// allowedBinDep reports whether importPath is permitted to reach the shipped qompack binary
// (00-ARCHITECTURE.md §2.5): the standard library, this module's own packages, klauspost/compress
// and Microsoft/go-winio.
func allowedBinDep(importPath string) bool {
	if isStdlib(importPath) {
		return true
	}
	switch {
	case importPath == modulePath, strings.HasPrefix(importPath, modulePath+"/"):
		return true
	case importPath == "github.com/klauspost/compress", strings.HasPrefix(importPath, "github.com/klauspost/compress/"):
		return true
	case importPath == "github.com/Microsoft/go-winio", strings.HasPrefix(importPath, "github.com/Microsoft/go-winio/"):
		return true
	}
	return false
}

// isStdlib applies the conventional Go heuristic: a standard-library import path's first slash-
// separated component never contains a dot, while every module path does (github.com/…,
// golang.org/…, gopkg.in/…, …).
func isStdlib(importPath string) bool {
	first := importPath
	if i := strings.IndexByte(importPath, '/'); i >= 0 {
		first = importPath[:i]
	}
	return !strings.Contains(first, ".")
}

// runBinDeps is the `devtool lint` sub-check: `go list -deps ./cmd/qompack` must contain no
// module path other than the ones allowedBinDep names, checked on all six release targets so
// golang.org/x/tools (a root go.mod requirement, needed only to build tools/lint/nomagic) never
// reaches the binary CI ships. It tolerates ./cmd/qompack not existing yet.
func runBinDeps() error {
	dir := filepath.Join(root, "cmd", "qompack")
	if !dirHasGoFiles(dir) {
		fmt.Println("bindeps: cmd/qompack not present yet (owned by SP-01 main session); nothing to check")
		return nil
	}

	var problems []string
	for _, tgt := range releaseTargets {
		env := map[string]string{"CGO_ENABLED": "0", "GOOS": tgt.GOOS, "GOARCH": tgt.GOARCH}
		stdout, stderr, err := runCapture(env, "go", "list", "-deps", "./cmd/qompack")
		if err != nil {
			return fmt.Errorf("bindeps: go list -deps ./cmd/qompack (%s/%s): %w\n%s", tgt.GOOS, tgt.GOARCH, err, stderr)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(stdout)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || allowedBinDep(line) {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"%s/%s: disallowed dependency %s reaches the shipped binary (00-ARCHITECTURE.md §2.5)",
				tgt.GOOS, tgt.GOARCH, line))
		}
	}

	if len(problems) == 0 {
		fmt.Printf("bindeps: OK (%d target(s) checked)\n", len(releaseTargets))
		return nil
	}
	for _, p := range problems {
		fmt.Println("  " + p)
	}
	return fmt.Errorf("bindeps: %d violation(s)", len(problems))
}
