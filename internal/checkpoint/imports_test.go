package checkpoint_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNoForbiddenImports parses every non-test Go file in internal/checkpoint and internal/pins
// and requires each import to be either stdlib or one of the eleven allowed internal packages —
// core, paths, config, logging, obs, store, dag, negknow, pins, grammar, tokens (§3.2's
// checkpoint allow-set). hookio, scheduler, ipc, daemon, os/exec, net and net/http are asserted
// absent by name.
//
// hookio's absence is the load-bearing one: it is the mechanical form of the advisory-handling
// rule. The focus instruction §8.5 describes is advisory — a host may ignore it, and since C1.18
// Qompack hands it to none — and this package must not care, because a checkpoint is built from
// durable originals, never from the summarizer's output. With no hookio import the package cannot
// read a transcript, and therefore cannot branch on whether the summarizer complied with any focus
// instruction. The
// other absences close the same class of door: no subprocesses (os/exec), no network (net,
// net/http), no scheduling or IPC reach-around into live session state.
//
// It would fail against exactly the drift it names: any seat adding `import "…/internal/hookio"`
// (say, to peek at a PreCompact payload from inside the writer) turns the build's test gate red
// before review ever sees the diff.
func TestNoForbiddenImports(t *testing.T) {
	// The test binary runs in internal/checkpoint, so the two package directories sit at "." and
	// "../pins". Subdirectories (checkpointtest, pinstest) are separate packages with their own
	// rules and are deliberately not walked.
	for _, dir := range []string{".", "../pins"} {
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)

		scanned := 0
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			scanned++

			path := filepath.Join(dir, name)
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			require.NoError(t, err)

			for _, imp := range f.Imports {
				p, err := strconv.Unquote(imp.Path.Value)
				require.NoError(t, err)
				require.Empty(t, importViolation(p), "%s imports %q", path, p)
			}
		}
		require.Positive(t, scanned, "%s: no non-test files scanned — the guard would be vacuous", dir)
	}
}

// importViolation classifies one import path against the §3.2 rules, returning "" for an
// acceptable import and the reason otherwise. It is a separate function so
// TestImportViolationClassifier can prove the guard actually fires — a scanner whose classifier
// had gone dead would wave every import through and TestNoForbiddenImports would pass vacuously.
func importViolation(p string) string {
	const modInternal = "github.com/qompack/qompack/internal/"

	allowedInternal := map[string]bool{
		"core": true, "paths": true, "config": true, "logging": true, "obs": true,
		"store": true, "dag": true, "negknow": true, "pins": true, "grammar": true, "tokens": true,
	}
	forbiddenInternal := map[string]bool{
		"hookio": true, "scheduler": true, "ipc": true, "daemon": true,
	}
	forbiddenStdlib := map[string]bool{
		"os/exec": true, "net": true, "net/http": true,
	}

	if rest, ok := strings.CutPrefix(p, modInternal); ok {
		pkg, _, _ := strings.Cut(rest, "/")
		switch {
		case forbiddenInternal[pkg]:
			return "forbidden internal package — see TestNoForbiddenImports's doc comment"
		case !allowedInternal[rest]:
			return "outside the §3.2 allow-set"
		}
		return ""
	}
	if forbiddenStdlib[p] {
		return "forbidden stdlib package"
	}
	if first, _, _ := strings.Cut(p, "/"); strings.Contains(first, ".") {
		return "third-party imports are not in the allow-set"
	}
	return ""
}

// TestImportViolationClassifier is the positive control for TestNoForbiddenImports: every class
// of forbidden import must be flagged and every allowed class must pass, so the guard cannot rot
// into one that scans files and objects to nothing.
func TestImportViolationClassifier(t *testing.T) {
	for _, p := range []string{
		"fmt", "encoding/json", "net/url",
		"github.com/qompack/qompack/internal/core",
		"github.com/qompack/qompack/internal/tokens",
	} {
		require.Empty(t, importViolation(p), "%q must be allowed", p)
	}
	for _, p := range []string{
		"os/exec", "net", "net/http",
		"github.com/qompack/qompack/internal/hookio",
		"github.com/qompack/qompack/internal/scheduler",
		"github.com/qompack/qompack/internal/ipc",
		"github.com/qompack/qompack/internal/daemon",
		"github.com/qompack/qompack/internal/hookio/sub",
		"github.com/qompack/qompack/internal/rehydrate",
		"github.com/qompack/qompack/internal/store/storetest",
		"github.com/stretchr/testify/require",
	} {
		require.NotEmpty(t, importViolation(p), "%q must be flagged", p)
	}
}
