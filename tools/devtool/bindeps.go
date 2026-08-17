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
// (00-ARCHITECTURE.md §2.5): the standard library, this module's own packages, klauspost/compress,
// Microsoft/go-winio, and golang.org/x/sys/windows.
//
// golang.org/x/sys/windows was added in SP-05's task 6: it is go-winio's own transitive
// dependency (`go mod why -m golang.org/x/sys` on a windows target resolves
// internal/ipc -> github.com/Microsoft/go-winio -> golang.org/x/sys/windows), needed for the named
// pipe's per-user SID ACL (00-ARCHITECTURE.md §2.4). It never reached the binary before task 6
// because no earlier task's code was actually wired into cmd/qompack's own import graph — ipc and
// daemon existed but nothing in internal/cli imported them yet, so go-winio's own transitive
// dependency was invisible to this check until the hook bodies and `qompack daemon` started
// importing internal/ipc / internal/daemon for real.
//
// The allow-list names the EXACT import path only, not a prefix (fix round 1, Minor M-14): only
// golang.org/x/sys/windows itself is actually reached (verified via `go list -deps`); a prefix
// match would additionally admit .../windows/registry, .../windows/svc, and every other
// subpackage go-winio does not use, widening the allow-list beyond what is justified.
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
	case importPath == "golang.org/x/sys/windows":
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
