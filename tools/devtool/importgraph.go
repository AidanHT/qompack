package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// pkgInfo is the minimal shape importgraph and testdeps need from `go list -json`: a package's
// own import path and its direct, non-test imports.
type pkgInfo struct {
	ImportPath string
	Imports    []string
}

// goListPkg mirrors the fields of `go list -json` output that pkgInfo is built from; every other
// field `go list` prints is simply ignored by encoding/json.
type goListPkg struct {
	ImportPath string   `json:"ImportPath"`
	Imports    []string `json:"Imports"`
}

// goListJSON runs `go list -json <patterns...>` from root and decodes the resulting stream of
// concatenated JSON objects. `go list` can exit non-zero while still emitting valid JSON for
// every pattern it DID resolve (for example when one of several patterns matches an existing but
// currently package-less directory); this only treats the call as fatal when nothing at all could
// be decoded, so callers that pre-filter patterns to directories that exist (see
// existingTopLevelPatterns) see a clean, empty result instead of an error on a partial tree.
func goListJSON(patterns ...string) ([]pkgInfo, error) {
	args := append([]string{"list", "-json"}, patterns...)
	stdout, stderr, runErr := runCapture(nil, "go", args...)

	dec := json.NewDecoder(bytes.NewReader(stdout))
	var out []pkgInfo
	for dec.More() {
		var g goListPkg
		if err := dec.Decode(&g); err != nil {
			return out, fmt.Errorf("decoding `go list -json %s` output: %w", strings.Join(patterns, " "), err)
		}
		out = append(out, pkgInfo(g))
	}
	if runErr != nil && len(out) == 0 {
		return nil, fmt.Errorf("go list -json %s: %w\n%s", strings.Join(patterns, " "), runErr, stderr)
	}
	return out, nil
}

// existingTopLevelPatterns returns "./<dir>/..." for every dir in dirs that actually exists under
// root, so callers never hand `go list` a pattern that can't match anything because the directory
// itself is absent — the situation this whole repository is in before wave 1 lands.
func existingTopLevelPatterns(dirs ...string) []string {
	var pats []string
	for _, d := range dirs {
		if dirExists(filepath.Join(root, d)) {
			pats = append(pats, "./"+d+"/...")
		}
	}
	return pats
}

// classification is what importgraph and testdeps need to know about one in-module package.
type classification struct {
	key        string          // the short key used in `allow`/`compositionRoots`, e.g. "store", "cmd/qompack", "test/e2e", "store/storetest"
	isRoot     bool            // a composition root: may import anything, and nothing may import it
	allowSet   map[string]bool // the fully-resolved set of keys this package may import (nil for roots)
	recognized bool            // false => rule (d): on disk but declared nowhere
}

// classify maps a package's full import path to its §3.2 classification.
func classify(importPath string) classification {
	rel := strings.TrimPrefix(importPath, modulePath+"/")
	if rel == importPath {
		return classification{key: importPath}
	}

	switch {
	case rel == "cmd/qompack":
		if compositionRoots["cmd/qompack"] {
			return classification{key: "cmd/qompack", isRoot: true, recognized: true}
		}
		return classification{key: "cmd/qompack"}

	case strings.HasPrefix(rel, "test/"):
		if compositionRoots[rel] {
			return classification{key: rel, isRoot: true, recognized: true}
		}
		return classification{key: rel}

	case strings.HasPrefix(rel, "internal/"):
		stripped := strings.TrimPrefix(rel, "internal/")
		parts := strings.Split(stripped, "/")
		switch len(parts) {
		case 1:
			key := parts[0]
			if compositionRoots[key] {
				return classification{key: key, isRoot: true, recognized: true}
			}
			if declared, ok := allow[key]; ok {
				return classification{key: key, allowSet: effectiveAllow(key, declared), recognized: true}
			}
			return classification{key: key}
		case 2:
			base, sub := parts[0], parts[1]
			if sub == base+"test" {
				// Rule (c): a <pkg>test conformance subpackage may import its own package,
				// testutil, and core.
				return classification{
					key:        base + "/" + sub,
					allowSet:   map[string]bool{base: true, "testutil": true, "core": true},
					recognized: true,
				}
			}
			return classification{key: rel}
		default:
			return classification{key: rel}
		}

	default:
		return classification{key: rel}
	}
}

// effectiveAllow resolves the full set of keys key may import: its declared allow-set, plus all
// of foundation unless key is itself a foundation package (foundation packages get exactly their
// declared set, per 00-ARCHITECTURE.md §3.2's own table).
func effectiveAllow(key string, declared []string) map[string]bool {
	set := make(map[string]bool, len(declared)+len(foundation))
	for _, d := range declared {
		set[d] = true
	}
	if !isFoundation(key) {
		for _, f := range foundation {
			set[f] = true
		}
	}
	return set
}

func isFoundation(key string) bool {
	for _, f := range foundation {
		if f == key {
			return true
		}
	}
	return false
}

// checkImportGraph applies the §3.2 rules to pkgs and returns one human-readable violation string
// per problem found, sorted for deterministic output. It has no side effects and does not shell
// out, which is what makes it independently testable against a synthetic package list.
func checkImportGraph(pkgs []pkgInfo) []string {
	class := make(map[string]classification, len(pkgs))
	var violations []string

	for _, p := range pkgs {
		c := classify(p.ImportPath)
		class[p.ImportPath] = c
		if !c.recognized {
			violations = append(violations, fmt.Sprintf(
				"%s: on disk but declared in neither `allow` nor `compositionRoots` in tools/devtool/importrules.go (00-ARCHITECTURE.md §3.2); new packages must be declared there",
				p.ImportPath))
		}
	}

	for _, p := range pkgs {
		c := class[p.ImportPath]
		if !c.recognized || c.isRoot {
			continue // composition roots may import anything; unrecognized packages are already flagged above
		}
		for _, imp := range p.Imports {
			if imp != modulePath && !strings.HasPrefix(imp, modulePath+"/") {
				continue // stdlib or an external dependency: out of scope for the §3.2 DAG
			}
			target, ok := class[imp]
			if !ok {
				// The import target was not part of the scanned package set (a narrower pattern
				// than the full internal/cmd/test tree was passed). Skip rather than guess.
				continue
			}
			if c.allowSet[target.key] {
				continue
			}
			if target.isRoot {
				violations = append(violations, fmt.Sprintf(
					"%s imports %s, a composition root — nothing may import a composition root (00-ARCHITECTURE.md §3.2)",
					p.ImportPath, imp))
				continue
			}
			violations = append(violations, fmt.Sprintf(
				"%s imports %s, which is not in %s's allow-set (00-ARCHITECTURE.md §3.2)",
				p.ImportPath, imp, c.key))
		}
	}

	sort.Strings(violations)
	return violations
}

// runImportGraph is the `devtool lint` sub-check: it loads every package under internal/, cmd/
// and test/ (whichever of those trees currently exist — see existingTopLevelPatterns) and runs
// checkImportGraph over them.
func runImportGraph() error {
	patterns := existingTopLevelPatterns("internal", "cmd", "test")
	if len(patterns) == 0 {
		fmt.Println("importgraph: no internal/, cmd/, or test/ trees yet; nothing to check")
		return nil
	}
	pkgs, err := goListJSON(patterns...)
	if err != nil {
		return fmt.Errorf("importgraph: %w", err)
	}
	if len(pkgs) == 0 {
		fmt.Println("importgraph: no packages found under internal/, cmd/, or test/; nothing to check")
		return nil
	}
	violations := checkImportGraph(pkgs)
	if len(violations) == 0 {
		fmt.Printf("importgraph: OK (%d package(s) checked)\n", len(pkgs))
		return nil
	}
	for _, v := range violations {
		fmt.Println("  " + v)
	}
	return fmt.Errorf("importgraph: %d violation(s)", len(violations))
}
